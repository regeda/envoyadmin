package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/regeda/envoyadmin/internal/snapshot"
)

func lst(name, addr string, state snapshot.ListenerStatus) snapshot.Listener {
	return snapshot.Listener{
		Name: name, Address: addr, State: state, VersionInfo: "42",
		LastUpdated: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC),
		Chains: []snapshot.FilterChain{
			{Label: "#1", Match: []string{"sni=a.com,b.com", "port=443", "transport=tls"}, Filters: fl("http_connection_manager"), TLS: true},
			{Label: "#2", Filters: fl("tcp_proxy")},
			{Label: "default", Filters: fl("tcp_proxy")},
		},
	}
}

func item0(l snapshot.Listener) resourceItem { return listenerItems([]snapshot.Listener{l})[0] }

func TestListenerItemIdentity(t *testing.T) {
	it := item0(lst("0.0.0.0_15001", "0.0.0.0:15001", snapshot.ListenerActive))
	if it.Key() != "0.0.0.0_15001" || it.Title() != "0.0.0.0_15001" || it.OverviewName() != "Overview" ||
		it.FilterText() != "0.0.0.0_15001 0.0.0.0:15001" {
		t.Fatalf("%q %q %q %q", it.Key(), it.Title(), it.OverviewName(), it.FilterText())
	}
}

func TestListenerRowGlyphsAndLayout(t *testing.T) {
	cases := []struct {
		state snapshot.ListenerStatus
		glyph string
	}{
		{snapshot.ListenerActive, "●"}, {snapshot.ListenerWarming, "◐"},
		{snapshot.ListenerDraining, "◐"}, {snapshot.ListenerError, "✗"},
	}
	for _, c := range cases {
		row := ansiStrip(item0(lst("api", "0.0.0.0:8080", c.state)).Row(rowCtx{Width: 36}))
		if !strings.HasPrefix(row, " "+c.glyph+" api") || !strings.HasSuffix(row, "0.0.0.0:8080") || lipgloss.Width(row) != 36 {
			t.Fatalf("%v: %q (width %d)", c.state, row, lipgloss.Width(row))
		}
	}
	if row := ansiStrip(item0(lst("api", "0.0.0.0:8080", snapshot.ListenerActive)).Row(rowCtx{Width: 36, Cursor: true})); !strings.HasPrefix(row, ">") {
		t.Fatalf("cursor: %q", row)
	}
	if row := ansiStrip(item0(lst("api", "0.0.0.0:8080", snapshot.ListenerActive)).Row(rowCtx{Width: 36, Cursor: true, Unfocused: true})); !strings.HasPrefix(row, "›") {
		t.Fatalf("unfocused cursor: %q", row)
	}
	static := lst("agent", "unix:/var/run/agent.sock", snapshot.ListenerActive)
	static.Static = true
	if row := ansiStrip(item0(static).Row(rowCtx{Width: 36})); !strings.HasPrefix(row, " ● agent") {
		t.Fatalf("static listeners show the active glyph: %q", row)
	}
}

func TestListenerStateIsNotColourOnly(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	a := item0(lst("x", "1.1.1.1:1", snapshot.ListenerActive)).Row(rowCtx{Width: 30})
	e := item0(lst("x", "1.1.1.1:1", snapshot.ListenerError)).Row(rowCtx{Width: 30})
	if a == e {
		t.Fatal("with no colours at all, an error row must still differ from an active row")
	}
}

func TestListenerRowWidthNeverExceeded(t *testing.T) {
	long := lst(strings.Repeat("very-long-listener-name-", 5), "[2001:db8:aaaa:bbbb:cccc:dddd:eeee:ffff]:443", snapshot.ListenerActive)
	wide := lst("日本語のリスナー名前日本語のリスナー名前", "0.0.0.0:80", snapshot.ListenerWarming)
	for _, w := range []int{1, 5, 12, 28, 36, 60} {
		for _, l := range []snapshot.Listener{long, wide, lst("a", "", snapshot.ListenerActive)} {
			if got := lipgloss.Width(item0(l).Row(rowCtx{Width: w, Cursor: true})); got > w {
				t.Fatalf("width %d: row is %d wide for %q", w, got, l.Name)
			}
		}
	}
	// the name keeps at least 8 cells when the pane allows it
	row := ansiStrip(item0(long).Row(rowCtx{Width: 36}))
	if !strings.Contains(row, "very-lon") || lipgloss.Width(row) != 36 {
		t.Fatalf("name starved by the address: %q", row)
	}
}

func TestListenerRowHighlightsHitsInNameAndAddress(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	it := item0(lst("alpha", "0.0.0.0:9090", snapshot.ListenerActive))
	text := it.FilterText() // "alpha 0.0.0.0:9090"
	hits := []int{strings.Index(text, "pha"), strings.Index(text, "pha") + 1, strings.Index(text, "pha") + 2, strings.Index(text, "9090")}
	row := it.Row(rowCtx{Width: 36, Hits: hits})
	// lipgloss emits bold+underline as "1;4;4"; one run per hit rune (3 in the name, 1 in the address)
	if n := strings.Count(row, "\x1b[1;4;4m"); n != 4 {
		t.Fatalf("expected 4 underlined hit runes, got %d: %q", n, row)
	}
	if strings.Contains(ansiStrip(row), "\x1b") || strings.Contains(it.Row(rowCtx{Width: 36}), ";4m") {
		t.Fatal("no hits, no underline")
	}
	if ansiStrip(row) != ansiStrip(it.Row(rowCtx{Width: 36})) {
		t.Fatal("highlighting must not change the visible text")
	}
}

func overviewRows(it resourceItem, w, h, scroll int) []string {
	return strings.Split(it.Overview(w, h, scroll), "\n")
}

// kv is a property row as the Overview draws it: the label padded to 14 cells.
func kv(label, value string) string { return fmt.Sprintf("%-14s%s", label, value) }

func TestListenerOverviewLayout(t *testing.T) {
	rows := overviewRows(item0(lst("x", "0.0.0.0:15001", snapshot.ListenerActive)), 70, 14, 0)
	want := []string{
		kv("Bind address", "0.0.0.0:15001"),
		kv("State", "active"),
		kv("Version", "42"),
		kv("Last updated", "2026-10-14 10:00:00Z"),
		"",
		"Filter chains (3)",
	}
	if !strings.HasPrefix(want[0], "Bind address  0.0.0.0") {
		t.Fatal("the label column is 14 cells: two spaces after the longest label")
	}
	for i, w := range want {
		if got := strings.TrimRight(ansiStrip(rows[i]), " "); got != w {
			t.Fatalf("row %d = %q, want %q", i, got, w)
		}
	}
	if strings.Contains(ansiStrip(strings.Join(rows, "\n")), "version_info") {
		t.Fatal("the Overview does not show version_info (it is in the Config tab header)")
	}
	chains := ansiStrip(strings.Join(rows[5:13], "\n"))
	for _, frag := range []string{
		fmt.Sprintf(" %-7s  %s", "#1", "sni=a.com,b.com · port=443 · transport=tls"),
		"http_connection_manager\n          TLS",
		fmt.Sprintf(" %-7s  %s", "#2", "any"),
		fmt.Sprintf(" %-7s  %s", "default", "any"),
	} {
		if !strings.Contains(chains, frag) {
			t.Fatalf("missing %q in:\n%s", frag, chains)
		}
	}
	if len(rows) != 14 {
		t.Fatalf("exactly h rows, got %d", len(rows))
	}
}

func TestListenerOverviewStateAndBlanks(t *testing.T) {
	static := lst("s", "", snapshot.ListenerActive)
	static.Static, static.VersionInfo, static.LastUpdated, static.Chains = true, "", time.Time{}, nil
	rows := overviewRows(item0(static), 60, 10, 0)
	text := ansiStrip(strings.Join(rows, "\n"))
	for _, frag := range []string{kv("Bind address", "-"), kv("State", "static"), kv("Version", "-"), kv("Last updated", "-"), "Filter chains (0)"} {
		if !strings.Contains(text, frag) {
			t.Fatalf("missing %q:\n%s", frag, text)
		}
	}
}

func TestListenerOverviewErrorBlock(t *testing.T) {
	l := lst("bad", "0.0.0.0:9443", snapshot.ListenerError)
	l.ErrorDetails = "error adding listener: unknown filter 'envoy.filters.network.nope' while processing LDS update version 9, then a lot more words to force wrapping across rows and beyond the cap of four lines for sure"
	l.ServingOld = true
	rows := overviewRows(item0(l), 50, 20, 0)
	plain := make([]string, len(rows))
	for i, r := range rows {
		plain[i] = strings.TrimRight(ansiStrip(r), " ")
	}
	text := strings.Join(plain, "\n")
	if !strings.Contains(text, kv("State", "error")) || !strings.Contains(text, kv("Error", "error adding listener")) {
		t.Fatalf("error block missing:\n%s", text)
	}

	// the error text occupies the rows from "Error" up to the "still serving" note
	first, note := -1, -1
	for i, r := range plain {
		if strings.HasPrefix(r, "Error ") && first < 0 {
			first = i
		}
		if strings.Contains(r, "still serving") {
			note = i
		}
	}
	if first < 0 || note < 0 {
		t.Fatalf("error start %d / note %d:\n%s", first, note, text)
	}
	if n := note - first; n != 4 {
		t.Fatalf("the error text is capped at exactly 4 rows (the text is far longer), got %d:\n%s", n, text)
	}
	if !strings.HasSuffix(plain[note-1], "…") {
		t.Fatalf("a capped error ends with an ellipsis:\n%s", text)
	}
	for _, r := range plain[first+1 : note] {
		if !strings.HasPrefix(r, strings.Repeat(" ", 14)) {
			t.Fatalf("continuation rows are indented under the value: %q", r)
		}
	}

	l.ServingOld = false
	if strings.Contains(ansiStrip(strings.Join(overviewRows(item0(l), 50, 20, 0), "\n")), "still serving") {
		t.Fatal("the note appears only when an older version is still serving")
	}
}

func TestListenerOverviewNeverExceedsBoundsAndKeepsStickyRows(t *testing.T) {
	l := lst("x", "0.0.0.0:1", snapshot.ListenerError)
	l.ErrorDetails = strings.Repeat("boom ", 40)
	for i := 0; i < 30; i++ {
		l.Chains = append(l.Chains, snapshot.FilterChain{Label: fmt.Sprintf("chain-%d", i), Match: []string{strings.Repeat("sni=long.example.com,", 10)}, Filters: fl("tcp_proxy")})
	}
	for _, w := range []int{20, 40, 80} {
		for _, h := range []int{1, 2, 3, 6, 10, 25} {
			for _, scroll := range []int{0, 3, 999} {
				rows := overviewRows(item0(l), w, h, scroll)
				if len(rows) != h {
					t.Fatalf("w=%d h=%d scroll=%d: %d rows", w, h, scroll, len(rows))
				}
				for _, r := range rows {
					if lipgloss.Width(r) > w {
						t.Fatalf("w=%d h=%d: row %d wide: %q", w, h, lipgloss.Width(r), r)
					}
				}
			}
		}
	}
	it := item0(l)
	// sticky rows stay while the chain lines scroll
	top := ansiStrip(strings.Join(overviewRows(it, 80, 14, 0), "\n"))
	mid := ansiStrip(strings.Join(overviewRows(it, 80, 14, 10), "\n"))
	for _, frag := range []string{"Bind address", "State", fmt.Sprintf("Filter chains (%d)", len(l.Chains))} {
		if !strings.Contains(top, frag) || !strings.Contains(mid, frag) {
			t.Fatalf("sticky %q must stay visible while scrolling", frag)
		}
	}
	if !strings.Contains(top, " #1 ") || strings.Contains(mid, " #1 ") || !strings.Contains(mid, "chain-2") {
		t.Fatalf("chain lines must scroll:\n%s\n---\n%s", top, mid)
	}
}

func TestListenerOverviewMaxScrollMatchesRendering(t *testing.T) {
	l := lst("x", "0.0.0.0:1", snapshot.ListenerActive)
	for i := 0; i < 20; i++ {
		l.Chains = append(l.Chains, snapshot.FilterChain{Label: fmt.Sprintf("chain-%02d", i), Filters: fl("tcp_proxy")})
	}
	it := item0(l)
	maxScroll := it.OverviewMaxScroll(60, 12)
	if maxScroll <= 0 {
		t.Fatal("a long chain list must scroll")
	}
	last := ansiStrip(strings.Join(overviewRows(it, 60, 12, maxScroll), "\n"))
	if !strings.Contains(last, "chain-19") {
		t.Fatalf("scrolling to the max must reveal the last chain:\n%s", last)
	}
	beyond := ansiStrip(strings.Join(overviewRows(it, 60, 12, maxScroll+50), "\n"))
	if beyond != last {
		t.Fatal("an oversized scroll clamps to the max")
	}
	short := item0(lst("y", "1.1.1.1:1", snapshot.ListenerActive))
	if short.OverviewMaxScroll(60, 30) != 0 {
		t.Fatal("content that fits does not scroll")
	}
	if it.OverviewMaxScroll(60, 1) != 0 || it.OverviewMaxScroll(60, 0) != 0 {
		t.Fatal("no room for the chain area means no scroll")
	}
}

func TestListenerOverviewLongLinesAreClippedWithEllipsis(t *testing.T) {
	l := lst("x", "0.0.0.0:1", snapshot.ListenerActive)
	l.Chains = []snapshot.FilterChain{{Label: "c", Match: []string{"sni=" + strings.Repeat("host.example.com,", 20)}, Filters: fl("tcp_proxy")}}
	rows := overviewRows(item0(l), 40, 12, 0)
	found := false
	for _, r := range rows {
		if strings.Contains(r, "sni=") {
			found = true
			if !strings.HasSuffix(ansiStrip(r), "…") || lipgloss.Width(r) > 40 {
				t.Fatalf("a long match line ends with … within the width: %q", r)
			}
		}
	}
	if !found {
		t.Fatal("match line not found")
	}
}

func TestListenerItemConfig(t *testing.T) {
	l := lst("x", "0.0.0.0:1", snapshot.ListenerActive)
	l.Config = snapshot.ConfigInfo{YAML: "name: x\n", VersionInfo: "42"}
	if text, _ := item0(l).Config().Render(); text != "name: x\n" {
		t.Fatalf("%q", text)
	}
}

func TestListenerItemsEmpty(t *testing.T) {
	if got := listenerItems(nil); len(got) != 0 {
		t.Fatal("no listeners, no items")
	}
}

func TestListenerOverviewTinyAndWideNeverPanicOrOverflow(t *testing.T) {
	wide := lst("日本語", "", snapshot.ListenerError)
	wide.ErrorDetails = strings.Repeat("日本語のエラー ", 10)
	wide.ServingOld = true
	wide.Chains = []snapshot.FilterChain{{Label: "日本語のチェーン名前", Match: []string{"sni=日本語.example.com"}, Filters: fl("フィルター"), TLS: true}}
	empty := lst("e", "", snapshot.ListenerActive)
	empty.Chains = nil
	for _, l := range []snapshot.Listener{wide, empty} {
		it := item0(l)
		for _, w := range []int{0, 1, 2, 5, 12, 40} {
			for h := 1; h <= 20; h++ {
				for _, scroll := range []int{-5, 0, 1, 1 << 30} {
					rows := strings.Split(it.Overview(w, h, scroll), "\n")
					if len(rows) != h {
						t.Fatalf("w=%d h=%d scroll=%d: %d rows", w, h, scroll, len(rows))
					}
					for _, r := range rows {
						if lipgloss.Width(r) > w {
							t.Fatalf("w=%d h=%d: %d wide: %q", w, h, lipgloss.Width(r), r)
						}
					}
				}
				_ = it.OverviewMaxScroll(w, h)
			}
		}
	}
}

func TestListenerOverviewMaxScrollCountsTheErrorBlock(t *testing.T) {
	l := lst("x", "0.0.0.0:1", snapshot.ListenerError)
	l.ErrorDetails = strings.Repeat("boom ", 40)
	l.ServingOld = true
	for i := 0; i < 15; i++ {
		l.Chains = append(l.Chains, snapshot.FilterChain{Label: fmt.Sprintf("chain-%02d", i), Filters: fl("tcp_proxy")})
	}
	it := item0(l)
	max := it.OverviewMaxScroll(60, 20)
	last := ansiStrip(strings.Join(overviewRows(it, 60, 20, max), "\n"))
	if !strings.Contains(last, "chain-14") || !strings.Contains(last, "tcp_proxy") {
		t.Fatalf("max scroll with an error block must reveal the last chain:\n%s", last)
	}
	if max2 := it.OverviewMaxScroll(60, 21); max2 != max-1 {
		t.Fatalf("one more row of height is one less scroll: %d vs %d", max2, max)
	}
	// the very last line is the last chain's filter line, so max is not too large either
	rows := strings.Split(last, "\n")
	if !strings.Contains(rows[len(rows)-1], "tcp_proxy") {
		t.Fatalf("max scroll leaves blank rows at the bottom:\n%s", last)
	}
}

func TestListenerStateColours(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	for state, want := range map[snapshot.ListenerStatus]string{
		snapshot.ListenerActive:   "\x1b[32m●",
		snapshot.ListenerWarming:  "\x1b[33m◐",
		snapshot.ListenerDraining: "\x1b[33m◐",
		snapshot.ListenerError:    "\x1b[31m✗",
	} {
		it := item0(lst("x", "1.1.1.1:1", state))
		if row := it.Row(rowCtx{Width: 30}); !strings.Contains(row, want) {
			t.Fatalf("%v: want %q in %q", state, want, row)
		}
		col := strings.TrimSuffix(strings.TrimPrefix(want, "\x1b["), "m"+string([]rune(want)[len([]rune(want))-1:]))
		if ov := it.Overview(40, 3, 0); !strings.Contains(ov, "\x1b["+col+"m") {
			t.Fatalf("%v: the State value in the Overview carries colour %s: %q", state, col, ov)
		}
	}
}

func TestListenerStillServingNoteIsNeverClipped(t *testing.T) {
	l := lst("bad", "0.0.0.0:9443", snapshot.ListenerError)
	l.ErrorDetails = "boom"
	l.ServingOld = true
	it := item0(l)
	for _, w := range []int{50, 51, 60, 84} {
		rows := overviewRows(it, w, 20, 0)
		var note []string
		in := false
		for _, r := range rows {
			p := strings.TrimRight(ansiStrip(r), " ")
			if strings.HasPrefix(strings.TrimSpace(p), "still") {
				in = true
			} else if p == "" {
				in = false
			}
			if in {
				if lipgloss.Width(p) > w {
					t.Fatalf("w=%d: row too wide %q", w, p)
				}
				if strings.Contains(p, "…") {
					t.Fatalf("w=%d: the note is clipped: %q", w, p)
				}
				note = append(note, strings.TrimSpace(p))
			}
		}
		if got := strings.Join(note, " "); got != "still serving the previous configuration" {
			t.Fatalf("w=%d: note = %q", w, got)
		}
	}
}

func TestListenerWrappedErrorRowsHaveNoTrailingSpaces(t *testing.T) {
	l := lst("bad", "0.0.0.0:9443", snapshot.ListenerError)
	l.ErrorDetails = strings.Repeat("boom ", 20)
	for _, r := range overviewRows(item0(l), 60, 20, 0) {
		if p := ansiStrip(r); strings.Contains(p, "boom") && strings.HasSuffix(p, " ") {
			t.Fatalf("trailing space in a wrapped row: %q", r)
		}
	}
}

func TestListenerOverviewMaxScrollWithWrappedNote(t *testing.T) {
	l := lst("x", "0.0.0.0:1", snapshot.ListenerError)
	l.ErrorDetails = "boom"
	l.ServingOld = true // wraps to two rows at width 50
	for i := 0; i < 15; i++ {
		l.Chains = append(l.Chains, snapshot.FilterChain{Label: fmt.Sprintf("chain-%02d", i), Filters: fl("tcp_proxy")})
	}
	it := item0(l)
	last := ansiStrip(strings.Join(overviewRows(it, 50, 16, it.OverviewMaxScroll(50, 16)), "\n"))
	rows := strings.Split(last, "\n")
	if !strings.Contains(last, "chain-14") || !strings.Contains(rows[len(rows)-1], "tcp_proxy") {
		t.Fatalf("max scroll must land exactly on the last chain:\n%s", last)
	}
}

func istioListener() snapshot.Listener {
	l := lst("virtualInbound", "0.0.0.0:15006", snapshot.ListenerActive)
	l.Chains = []snapshot.FilterChain{
		{Label: "virtualInbound-blackhole", Match: []string{"port=1"}, Filters: fl("tcp_proxy")},
		{Label: "virtualInbound-catchall-http", Match: []string{"transport=tls"}, Filters: fl("http_connection_manager")},
		{Label: "short", Filters: fl("tcp_proxy")},
	}
	return l
}

func TestChainLabelsStayDistinguishableAndCutsAreMarked(t *testing.T) {
	it := item0(istioListener())
	v := ansiStrip(strings.Join(overviewRows(it, 84, 20, 0), "\n"))
	if !strings.Contains(v, "virtualInbound-blackhole ") || !strings.Contains(v, "virtualInbound-catchall-http ") {
		t.Fatalf("both long labels must be readable in full at 84 cells:\n%s", v)
	}

	long := istioListener()
	long.Chains[0].Label = "virtualInbound-" + strings.Repeat("x", 80)
	long.Chains[1].Label = "virtualInbound-" + strings.Repeat("y", 80)
	for w := 20; w <= 120; w++ {
		rows := overviewRows(item0(long), w, 20, 0)
		for _, r := range rows {
			if lipgloss.Width(r) > w {
				t.Fatalf("w=%d: row %d wide: %q", w, lipgloss.Width(r), r)
			}
		}
	}
	rows := overviewRows(item0(long), 60, 20, 0)
	var cut int
	for _, r := range rows {
		r = ansiStrip(r)
		if strings.HasPrefix(r, " virtualInbound-") {
			cut++
			if !strings.Contains(r, "…") {
				t.Fatalf("a cut label must end with an ellipsis: %q", r)
			}
		}
	}
	if cut != 2 {
		t.Fatalf("want two cut labels, saw %d:\n%s", cut, strings.Join(rows, "\n"))
	}
}

func TestChainLabelWidthKeepsScrollMaxConsistent(t *testing.T) {
	l := istioListener()
	for i := 0; i < 20; i++ {
		l.Chains = append(l.Chains, snapshot.FilterChain{Label: fmt.Sprintf("c-%02d", i), Filters: fl("tcp_proxy")})
	}
	it := item0(l)
	for _, w := range []int{20, 60, 84, 120} {
		maxS := it.OverviewMaxScroll(w, 14)
		last := ansiStrip(strings.Join(overviewRows(it, w, 14, maxS), "\n"))
		if !strings.Contains(last, "c-19") && w >= 40 {
			t.Fatalf("w=%d: the max scroll must reveal the last chain:\n%s", w, last)
		}
		if got := ansiStrip(strings.Join(overviewRows(it, w, 14, maxS+5), "\n")); got != last {
			t.Fatalf("w=%d: scrolling past the max must not move", w)
		}
		if maxS > 0 && ansiStrip(strings.Join(overviewRows(it, w, 14, maxS-1), "\n")) == last {
			t.Fatalf("w=%d: max-1 must differ from max", w)
		}
	}
}

func hcmListener() snapshot.Listener {
	l := lst("x", "0.0.0.0:1", snapshot.ListenerActive)
	l.Chains = []snapshot.FilterChain{
		{Label: "#1", Filters: fl("http_connection_manager"), HTTPFilters: fl("cors", "istio.stats", "router")},
		{Label: "#2", Filters: fl("tcp_proxy")},
		{Label: "default", Filters: fl("http_connection_manager"), HTTPFilters: fl("router")},
	}
	return l
}

func plainRows(it resourceItem, w, h, scroll int) []string {
	rows := overviewRows(it, w, h, scroll)
	for i, r := range rows {
		rows[i] = strings.TrimRight(ansiStrip(r), " ")
	}
	return rows
}

func TestListenerOverviewHTTPFilterTree(t *testing.T) {
	rows := plainRows(item0(hcmListener()), 70, 24, 0)
	idx := func(sub string) int {
		for i, r := range rows {
			if strings.Contains(r, sub) {
				return i
			}
		}
		t.Fatalf("%q not found in:\n%s", sub, strings.Join(rows, "\n"))
		return -1
	}
	// chain 1: filters line, then the tree directly below, indented under the filters text
	f := idx("http_connection_manager")
	want := []string{
		fmt.Sprintf(" %-7s  %s", "", "http_connection_manager"),
		fmt.Sprintf(" %-7s    %s", "", "├ cors"),
		fmt.Sprintf(" %-7s    %s", "", "├ istio.stats"),
		fmt.Sprintf(" %-7s    %s", "", "└ router"),
		fmt.Sprintf(" %-7s  %s", "#2", "any"),
		fmt.Sprintf(" %-7s  %s", "", "tcp_proxy"),
		fmt.Sprintf(" %-7s  %s", "default", "any"),
		fmt.Sprintf(" %-7s  %s", "", "http_connection_manager"),
		fmt.Sprintf(" %-7s    %s", "", "└ router"),
	}
	for i, w := range want {
		if rows[f+i] != w {
			t.Fatalf("row %d = %q, want %q\n%s", f+i, rows[f+i], w, strings.Join(rows, "\n"))
		}
	}
	text := strings.Join(rows, "\n")
	if n := strings.Count(text, "└"); n != 2 {
		t.Fatalf("one └ per tree (the last entry), got %d", n)
	}
	if n := strings.Count(text, "├"); n != 2 {
		t.Fatalf("├ for all but the last, got %d", n)
	}
	// tcp_proxy chain has no tree: its filters line is followed by the next chain
	if i := idx("tcp_proxy"); strings.ContainsAny(rows[i+1], "├└") {
		t.Fatalf("a chain without HCM has no tree: %q", rows[i+1])
	}
}

func TestListenerOverviewHTTPFilterTreeEdgeCases(t *testing.T) {
	l := hcmListener()
	l.Chains = []snapshot.FilterChain{
		{Label: "none", Filters: fl("http_connection_manager")},
		{Label: "one", Filters: fl("http_connection_manager"), HTTPFilters: fl("router")},
	}
	text := strings.Join(plainRows(item0(l), 60, 14, 0), "\n")
	if strings.Count(text, "├")+strings.Count(text, "└") != 1 || !strings.Contains(text, "└ router") {
		t.Fatalf("an HCM without http filters has no tree, a single entry is └:\n%s", text)
	}
}

func TestListenerOverviewHTTPFilterLongNamesAreClipped(t *testing.T) {
	l := hcmListener()
	l.Chains = []snapshot.FilterChain{{Label: "c", Filters: fl("http_connection_manager"),
		HTTPFilters: fl(strings.Repeat("very.long.filter.name.", 8), "router")}}
	rows := plainRows(item0(l), 40, 12, 0)
	var clipped bool
	for _, r := range rows {
		if strings.Contains(r, "├ very.long") {
			clipped = true
			if !strings.HasSuffix(r, "…") || lipgloss.Width(r) > 40 {
				t.Fatalf("a long http filter name ends with … within the width: %q", r)
			}
		}
	}
	if !clipped {
		t.Fatalf("long name row missing:\n%s", strings.Join(rows, "\n"))
	}
}

func TestListenerOverviewHTTPFilterTreeBounds(t *testing.T) {
	l := hcmListener()
	for i := 0; i < 6; i++ {
		l.Chains = append(l.Chains, snapshot.FilterChain{Label: fmt.Sprintf("hcm-%d", i), Filters: fl("http_connection_manager"),
			HTTPFilters: fl("envoy-very-long-name-"+strings.Repeat("z", 60), "b", "c")})
	}
	it := item0(l)
	for w := 20; w <= 120; w += 5 {
		for h := 1; h <= 30; h++ {
			for _, scroll := range []int{0, 2, 1 << 20} {
				rows := overviewRows(it, w, h, scroll)
				if len(rows) != h {
					t.Fatalf("w=%d h=%d: %d rows", w, h, len(rows))
				}
				for _, r := range rows {
					if lipgloss.Width(r) > w {
						t.Fatalf("w=%d h=%d: row %d wide: %q", w, h, lipgloss.Width(r), r)
					}
				}
			}
		}
	}
}

func TestListenerOverviewHTTPFilterScrollReachesLastNestedLine(t *testing.T) {
	l := hcmListener()
	for i := 0; i < 5; i++ {
		l.Chains = append(l.Chains, snapshot.FilterChain{Label: fmt.Sprintf("hcm-%d", i), Filters: fl("http_connection_manager"),
			HTTPFilters: fl("a", "b", fmt.Sprintf("last-%d", i))})
	}
	it := item0(l)
	for _, h := range []int{8, 10, 14, 20} {
		maxS := it.OverviewMaxScroll(70, h)
		if maxS <= 0 {
			t.Fatalf("h=%d: must scroll", h)
		}
		last := plainRows(it, 70, h, maxS)
		if !strings.HasSuffix(last[h-1], "└ last-4") {
			t.Fatalf("h=%d: the last nested line must be the bottom row at max scroll:\n%s", h, strings.Join(last, "\n"))
		}
		if got := plainRows(it, 70, h, maxS+7); strings.Join(got, "\n") != strings.Join(last, "\n") {
			t.Fatalf("h=%d: scrolling past the max clamps", h)
		}
		if strings.Join(plainRows(it, 70, h, maxS-1), "\n") == strings.Join(last, "\n") {
			t.Fatalf("h=%d: max-1 must differ", h)
		}
		for _, frag := range []string{"Bind address", "State", "Filter chains (8)"} {
			if !strings.Contains(strings.Join(last, "\n"), frag) {
				t.Fatalf("h=%d: sticky %q must remain", h, frag)
			}
		}
	}
}

// fl builds filters by name; the UI tests need no types.
func fl(names ...string) []snapshot.Filter {
	out := make([]snapshot.Filter, len(names))
	for i, n := range names {
		out[i] = snapshot.Filter{Name: n}
	}
	return out
}

func TestListenerOverviewFilterRowsShowNameThenType(t *testing.T) {
	l := lst("x", "0.0.0.0:1", snapshot.ListenerActive)
	l.Chains = []snapshot.FilterChain{{Label: "c", TLS: true,
		Filters:     []snapshot.Filter{{Name: "envoy.filters.network.http_connection_manager", Type: "HttpConnectionManager"}, {Name: "envoy.filters.network.tcp_proxy"}},
		HTTPFilters: []snapshot.Filter{{Name: "envoy.filters.http.cors", Type: "Cors"}, {Name: "envoy.filters.http.router", Type: "Router"}}}}
	text := strings.Join(plainRows(item0(l), 100, 20, 0), "\n")
	for _, frag := range []string{
		" c  any\n",
		"    envoy.filters.network.http_connection_manager  HttpConnectionManager\n",
		"    envoy.filters.network.tcp_proxy\n",
		"    TLS\n",
		"      ├ envoy.filters.http.cors  Cors\n",
		"      └ envoy.filters.http.router  Router",
	} {
		if !strings.Contains(text, frag) {
			t.Fatalf("missing %q:\n%s", frag, text)
		}
	}
	// too narrow for name + type: the type goes, the name stays
	narrow := strings.Join(plainRows(item0(l), 50, 20, 0), "\n")
	if strings.Contains(narrow, "HttpConnectionManager") || !strings.Contains(narrow, "envoy.filters.network.http_conn") {
		t.Fatalf("narrow pane:\n%s", narrow)
	}
}

func TestListenerOverviewListsSecrets(t *testing.T) {
	l := lst("api", "0.0.0.0:8080", snapshot.ListenerActive)
	l.Secrets = []snapshot.SecretUse{{Name: "server-cert", Loaded: true}, {Name: "ghost", Loaded: false}}
	out := ansiStrip(item0(l).Overview(100, 20, 0))
	if !strings.Contains(out, "Secrets") || !strings.Contains(out, "server-cert") || !strings.Contains(out, "ghost (not loaded)") {
		t.Fatalf("listener overview:\n%s", out)
	}
	none := ansiStrip(item0(lst("api", "0.0.0.0:8080", snapshot.ListenerActive)).Overview(100, 20, 0))
	if strings.Contains(none, "Secrets") {
		t.Fatalf("no row without secrets:\n%s", none)
	}
}
