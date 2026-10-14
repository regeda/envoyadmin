package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/regeda/txenvoy/internal/snapshot"
)

func lst(name, addr string, state snapshot.ListenerStatus) snapshot.Listener {
	return snapshot.Listener{
		Name: name, Address: addr, State: state, VersionInfo: "42",
		LastUpdated: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC),
		Chains: []snapshot.FilterChain{
			{Label: "#1", Match: []string{"sni=a.com,b.com", "port=443", "transport=tls"}, Filters: []string{"http_connection_manager"}, TLS: true},
			{Label: "#2", Filters: []string{"tcp_proxy"}},
			{Label: "default", Filters: []string{"tcp_proxy"}},
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
		kv("version_info", "42"),
		kv("last_updated", "2026-10-14 10:00:00Z"),
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
	chains := ansiStrip(strings.Join(rows[6:12], "\n"))
	for _, frag := range []string{
		fmt.Sprintf(" %-7s  %s", "#1", "sni=a.com,b.com · port=443 · transport=tls"),
		"http_connection_manager · TLS",
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
	for _, frag := range []string{kv("Bind address", "-"), kv("State", "static"), kv("version_info", "-"), kv("last_updated", "-"), "Filter chains (0)"} {
		if !strings.Contains(text, frag) {
			t.Fatalf("missing %q:\n%s", frag, text)
		}
	}
}

func TestListenerOverviewErrorBlock(t *testing.T) {
	l := lst("bad", "0.0.0.0:9443", snapshot.ListenerError)
	l.ErrorDetails = "error adding listener: unknown filter 'envoy.filters.network.nope' while processing LDS update version 9, then a lot more words to force wrapping across rows and beyond the cap of four lines for sure"
	l.ServingOld = true
	rows := overviewRows(item0(l), 60, 20, 0)
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
		if strings.Contains(r, "still serving the previous configuration") {
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
	if strings.Contains(ansiStrip(strings.Join(overviewRows(item0(l), 60, 20, 0), "\n")), "still serving") {
		t.Fatal("the note appears only when an older version is still serving")
	}
}

func TestListenerOverviewNeverExceedsBoundsAndKeepsStickyRows(t *testing.T) {
	l := lst("x", "0.0.0.0:1", snapshot.ListenerError)
	l.ErrorDetails = strings.Repeat("boom ", 40)
	for i := 0; i < 30; i++ {
		l.Chains = append(l.Chains, snapshot.FilterChain{Label: fmt.Sprintf("chain-%d", i), Match: []string{strings.Repeat("sni=long.example.com,", 10)}, Filters: []string{"tcp_proxy"}})
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
		l.Chains = append(l.Chains, snapshot.FilterChain{Label: fmt.Sprintf("chain-%02d", i), Filters: []string{"tcp_proxy"}})
	}
	it := item0(l)
	max := it.OverviewMaxScroll(60, 12)
	if max <= 0 {
		t.Fatal("a long chain list must scroll")
	}
	last := ansiStrip(strings.Join(overviewRows(it, 60, 12, max), "\n"))
	if !strings.Contains(last, "chain-19") {
		t.Fatalf("scrolling to the max must reveal the last chain:\n%s", last)
	}
	beyond := ansiStrip(strings.Join(overviewRows(it, 60, 12, max+50), "\n"))
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
	l.Chains = []snapshot.FilterChain{{Label: "c", Match: []string{"sni=" + strings.Repeat("host.example.com,", 20)}, Filters: []string{"tcp_proxy"}}}
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
	wide.Chains = []snapshot.FilterChain{{Label: "日本語のチェーン名前", Match: []string{"sni=日本語.example.com"}, Filters: []string{"フィルター"}, TLS: true}}
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
		l.Chains = append(l.Chains, snapshot.FilterChain{Label: fmt.Sprintf("chain-%02d", i), Filters: []string{"tcp_proxy"}})
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
