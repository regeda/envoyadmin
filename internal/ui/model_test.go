package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/regeda/envoyadmin/internal/config"
	"github.com/regeda/envoyadmin/internal/snapshot"
)

var fixedNow = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

func testOpts(fetch Fetcher) Options {
	return Options{
		Ctx:    context.Background(),
		Config: config.Config{Admin: "http://envoy:15000", Refresh: time.Millisecond, Timeout: time.Second},
		Fetch:  fetch,
		Clock:  func() time.Time { return fixedNow.Add(2 * time.Second) },
	}
}

func send(m Model, msg tea.Msg) Model {
	nm, _ := m.Update(msg)
	return nm.(Model)
}

func sendCmd(m Model, msg tea.Msg) (Model, tea.Cmd) {
	nm, cmd := m.Update(msg)
	return nm.(Model), cmd
}

// run executes a cmd (recursing into batches) and returns the messages produced.
func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, run(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func snap(cs ...snapshot.Cluster) snapshot.Snapshot {
	return snapshot.Snapshot{Clusters: cs, Version: "1.31.0", FetchedAt: fixedNow}
}

func sized(m Model, w, h int) Model { return send(m, tea.WindowSizeMsg{Width: w, Height: h}) }

func TestInitStartsFetchAndTick(t *testing.T) {
	calls := 0
	m := New(testOpts(func(context.Context) (snapshot.Snapshot, error) { calls++; return snap(), nil }))
	var gotSnap, gotTick bool
	for _, msg := range run(m.Init()) {
		switch msg.(type) {
		case snapshotMsg:
			gotSnap = true
		case tickMsg:
			gotTick = true
		}
	}
	if !gotSnap || !gotTick || calls != 1 {
		t.Fatalf("snap=%v tick=%v calls=%d", gotSnap, gotTick, calls)
	}
}

func TestTickDoesNotOverlapFetches(t *testing.T) {
	calls := 0
	m := New(testOpts(func(context.Context) (snapshot.Snapshot, error) { calls++; return snap(), nil }))
	// New is in flight already: a tick must only reschedule.
	m, cmd := sendCmd(m, tickMsg{})
	for _, msg := range run(cmd) {
		if _, ok := msg.(snapshotMsg); ok {
			t.Fatal("tick started a fetch while one was in flight")
		}
	}
	if calls != 0 {
		t.Fatalf("calls=%d", calls)
	}
	m = send(m, snapshotMsg{snap: snap()}) // fetch lands
	_, cmd = sendCmd(m, tickMsg{})
	var fetched bool
	for _, msg := range run(cmd) {
		if _, ok := msg.(snapshotMsg); ok {
			fetched = true
		}
	}
	if !fetched || calls != 1 {
		t.Fatalf("fetched=%v calls=%d", fetched, calls)
	}
}

func TestConnectionStateMachine(t *testing.T) {
	m := New(testOpts(nil))
	if m.state != stateConnecting || m.bannerVisible() {
		t.Fatal("starts connecting without a banner")
	}
	m = send(m, snapshotMsg{err: errors.New("dial refused")})
	if m.state != stateDisconnected || !m.bannerVisible() || m.hasSnap {
		t.Fatalf("first failure: state=%v banner=%v", m.state, m.bannerVisible())
	}
	m = send(m, snapshotMsg{snap: snap(cl("api", 1, 1))})
	if m.state != stateConnected || m.bannerVisible() || !m.hasSnap {
		t.Fatal("recovery clears the banner")
	}
	m = send(m, snapshotMsg{err: errors.New("boom")})
	if m.state != stateDisconnected || len(m.snap.Clusters) != 1 {
		t.Fatal("failure keeps the last good snapshot")
	}
}

func TestBannerDismissLifecycle(t *testing.T) {
	m := New(testOpts(nil))
	m = send(m, snapshotMsg{err: errors.New("boom")})
	m = send(m, key("x"))
	if m.bannerVisible() {
		t.Fatal("x dismisses")
	}
	m = send(m, snapshotMsg{err: errors.New("boom")})
	if m.bannerVisible() {
		t.Fatal("same error stays dismissed")
	}
	m = send(m, snapshotMsg{err: errors.New("different")})
	if !m.bannerVisible() {
		t.Fatal("a new error re-shows the banner")
	}
	m = send(m, key("x"))
	m = send(m, snapshotMsg{snap: snap()})
	m = send(m, snapshotMsg{err: errors.New("different")})
	if !m.bannerVisible() {
		t.Fatal("after recovery the same error shows again")
	}
}

func TestRefreshKey(t *testing.T) {
	calls := 0
	m := New(testOpts(func(context.Context) (snapshot.Snapshot, error) { calls++; return snap(), nil }))
	m = send(m, snapshotMsg{snap: snap()})
	m, cmd := sendCmd(m, key("r"))
	run(cmd)
	if calls != 1 || !m.inflight {
		t.Fatalf("calls=%d inflight=%v", calls, m.inflight)
	}
	_, cmd = sendCmd(m, key("r")) // already in flight
	if cmd != nil {
		t.Fatal("r while in flight must not start another fetch")
	}
}

func TestQuitKeys(t *testing.T) {
	m := New(testOpts(nil))
	if _, cmd := sendCmd(m, key("q")); cmd == nil || cmd() != tea.Quit() {
		t.Fatal("q quits")
	}
	m = send(m, key("/")) // filtering: q is text
	if _, cmd := sendCmd(m, key("q")); cmd != nil {
		t.Fatal("q must not quit while filtering")
	}
	if _, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Fatal("ctrl+c always quits")
	}
}

func TestSelectionDrivesDetailAndKeys(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snap(cl("alpha", 1, 1), cl("beta", 1, 1))})
	if !strings.Contains(m.View(), "alpha") {
		t.Fatal(m.View())
	}
	m = send(m, key("down"))
	if it, _ := m.cur().list.Selected(); it.Key() != "beta" {
		t.Fatal("down selects beta")
	}
	if !strings.Contains(m.cur().detail.View(), "beta") {
		t.Fatal("detail follows the selection")
	}
	m = send(m, key("c"))
	if m.cur().detail.tab != tabConfig {
		t.Fatal("c opens config")
	}
	m = send(m, key("e"))
	if m.cur().detail.tab != tabOverview {
		t.Fatal("e opens endpoints")
	}
	m = send(m, key("enter"))
	if m.focus != focusDetail {
		t.Fatal("enter moves focus")
	}
	m = send(m, key("/"))
	if m.focus != focusList || !m.cur().list.filtering {
		t.Fatal("/ focuses the list filter")
	}
}

func TestEmptyAndFilteredToNothing(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snap()})
	if v := m.View(); !strings.Contains(v, "no cluster selected") || !strings.Contains(v, "no clusters") {
		t.Fatal(v)
	}
	m = send(m, snapshotMsg{snap: snap(cl("api", 1, 1))})
	m = send(m, key("/"))
	for _, r := range "zzz" {
		m = send(m, key(string(r)))
	}
	if v := m.View(); !strings.Contains(v, "no matches") || !strings.Contains(v, "no cluster selected") {
		t.Fatal(v)
	}
}

func TestTinyAndZeroSizedTerminals(t *testing.T) {
	m := New(testOpts(nil)) // View before any WindowSizeMsg
	if !strings.Contains(m.View(), "terminal too small") {
		t.Fatal(m.View())
	}
	for _, sz := range [][2]int{{0, 0}, {1, 1}, {79, 40}, {200, 11}, {80, 12}} {
		mm := send(sized(m, sz[0], sz[1]), snapshotMsg{snap: snap(cl("api", 1, 1))})
		_ = mm.View() // must not panic
		mm = send(mm, snapshotMsg{err: errors.New("boom")})
		_ = mm.View()
	}
	// shrinking then growing again recovers
	mm := sized(sized(sized(m, 120, 30), 10, 5), 120, 30)
	mm = send(mm, snapshotMsg{snap: snap(cl("api", 1, 1))})
	if strings.Contains(mm.View(), "too small") {
		t.Fatal("must recover after resize")
	}
}

func TestViewFitsTheTerminal(t *testing.T) {
	for _, sz := range [][2]int{{120, 30}, {80, 24}, {100, 20}, {80, 12}, {200, 60}} {
		m := sized(New(testOpts(nil)), sz[0], sz[1])
		m = send(m, snapshotMsg{snap: snap(cl("api", 1, 2), cl("db", 2, 2))})
		for _, withErr := range []bool{false, true} {
			if withErr {
				m = send(m, snapshotMsg{err: errors.New(strings.Repeat("long error ", 40))})
			}
			lines := strings.Split(m.View(), "\n")
			if len(lines) != sz[1] {
				t.Fatalf("%v err=%v: %d lines, want %d", sz, withErr, len(lines), sz[1])
			}
			if !strings.Contains(lines[m.headerRows()-1], "Clusters") {
				t.Fatalf("%v err=%v: tab row hidden: %q", sz, withErr, lines[m.headerRows()-1])
			}
			for _, l := range lines {
				if lipgloss.Width(l) > sz[0] {
					t.Fatalf("%v err=%v: line width %d: %q", sz, withErr, lipgloss.Width(l), l)
				}
			}
		}
	}
}

func TestBannerKeepsHeaderAndShrinksPanes(t *testing.T) {
	longErr := errors.New(strings.Repeat("context deadline exceeded ", 12))
	for _, sz := range [][2]int{{120, 30}, {80, 24}, {100, 20}, {80, 12}} {
		m := sized(New(testOpts(nil)), sz[0], sz[1])
		m = send(m, snapshotMsg{snap: snap(cl("api", 1, 2), cl("db", 2, 2))})
		before := m.paneHeight()

		m = send(m, snapshotMsg{err: longErr})
		lines := strings.Split(m.View(), "\n")
		if len(lines) != sz[1] {
			t.Fatalf("%v: %d lines, want %d", sz, len(lines), sz[1])
		}
		hr, rows := m.headerRows(), len(m.banner())
		if rows < 2 {
			t.Fatalf("%v: banner rows = %d", sz, rows)
		}
		for _, tab := range []string{"Clusters", "Listeners", "Secrets"} {
			if !strings.Contains(lines[hr-1], tab) {
				t.Fatalf("%v: tab row lost %q: %q", sz, tab, lines[hr-1])
			}
		}
		if !strings.Contains(lines[hr], "disconnected") {
			t.Fatalf("%v: banner should follow the tab row: %q", sz, lines[hr])
		}
		if got := m.paneHeight(); got != before-rows {
			t.Fatalf("%v: pane height %d, want %d", sz, got, before-rows)
		}

		m = send(m, key("x"))
		if m.bannerVisible() || m.paneHeight() != before {
			t.Fatalf("%v: dismissing must give the rows back (pane height %d, want %d)", sz, m.paneHeight(), before)
		}
		if n := len(strings.Split(m.View(), "\n")); n != sz[1] {
			t.Fatalf("%v: %d lines after dismissal, want %d", sz, n, sz[1])
		}
	}
}

func TestBannerKeepsFilterAndSelection(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snap(cl("alpha", 1, 1), cl("beta", 1, 1), cl("delta", 1, 1))})
	m = send(m, key("/"))
	m = send(m, key("d"))
	for _, step := range []tea.Msg{snapshotMsg{err: errors.New("context deadline exceeded")}, snapshotMsg{snap: snap(cl("alpha", 1, 1), cl("beta", 1, 1), cl("delta", 1, 1))}} {
		m = send(m, step)
		if !m.cur().list.filtering {
			t.Fatal("the filter must stay open")
		}
		if it, ok := m.cur().list.Selected(); !ok || it.Key() != "delta" {
			t.Fatalf("selection lost: %v %v", it, ok)
		}
		if v := m.View(); !strings.Contains(v, "delta") {
			t.Fatalf("filtered row missing:\n%s", v)
		}
	}
}

func TestBannerFollowsResize(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snap(cl("api", 1, 2))})
	m = send(m, snapshotMsg{err: errors.New(strings.Repeat("context deadline exceeded ", 12))})
	for _, sz := range [][2]int{{100, 26}, {80, 14}} {
		m = sized(m, sz[0], sz[1])
		if n := len(strings.Split(m.View(), "\n")); n != sz[1] {
			t.Fatalf("%v: %d lines", sz, n)
		}
		want := max(sz[1]-m.headerRows()-len(m.banner())-footerRows, 1)
		if got := m.paneHeight(); got != want {
			t.Fatalf("%v: pane height %d, want %d", sz, got, want)
		}
	}
}

func TestBannerKeepsDetailFocus(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snap(cl("alpha", 1, 1), cl("beta", 1, 1))})
	m = send(m, key("down"))
	m = send(m, key("enter"))
	for _, step := range []tea.Msg{snapshotMsg{err: errors.New("context deadline exceeded")}, snapshotMsg{snap: snap(cl("alpha", 1, 1), cl("beta", 1, 1))}} {
		m = send(m, step)
		it, ok := m.cur().list.Selected()
		if m.focus != focusDetail || !ok || it.Key() != "beta" {
			t.Fatalf("focus=%v selected=%v ok=%v", m.focus, it, ok)
		}
	}
}

func TestConnectionBlock(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	if v := m.View(); !strings.Contains(v, "connecting") {
		t.Fatal(v)
	}
	m = send(m, snapshotMsg{snap: snap(cl("api", 1, 1))})
	v := m.View()
	for _, want := range []string{"● connected", "http://envoy:15000", "Envoy 1.31.0", "updated 2s ago", "Clusters (1)"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q in:\n%s", want, v)
		}
	}
	if !strings.Contains(v, "1 clusters") && !strings.Contains(v, "1 cluster") {
		t.Fatalf("status counts missing:\n%s", v)
	}
}

func TestHelpToggle(t *testing.T) {
	m := sized(New(testOpts(nil)), 160, 30)
	short := m.View()
	m = send(m, key("?"))
	if m.View() == short || !m.help {
		t.Fatal("? changes the hint line")
	}
}

func TestPanesAlignOnTheSeparator(t *testing.T) {
	// the list pane is 30% of the width, never below 28 cells
	for _, c := range []struct{ w, h, listW int }{{120, 30, 36}, {80, 24, 28}, {80, 12, 28}, {200, 60, 60}, {100, 30, 30}} {
		sz, listW := [2]int{c.w, c.h}, c.listW
		m := sized(New(testOpts(nil)), sz[0], sz[1])
		m = send(m, snapshotMsg{snap: snap(cl("api", 1, 2), cl("db", 2, 2))})
		lines := strings.Split(m.View(), "\n")
		first := m.headerRows()
		last := sz[1] - footerRows - 1
		for i := first; i <= last; i++ {
			plain := ansi.Strip(lines[i])
			rs := []rune(plain)
			if len(rs) <= listW || rs[listW] != '│' {
				t.Fatalf("%v row %d: separator not at column %d: %q", sz, i, listW, plain)
			}
		}
		if !strings.Contains(ansi.Strip(lines[sz[1]-1]), "2 clusters") {
			t.Fatalf("%v: status bar missing on the last row: %q", sz, lines[sz[1]-1])
		}
	}
}

func TestSelectedClusterDisappearing(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snap(cl("alpha", 1, 1), cl("beta", 1, 1), cl("gamma", 1, 1))})
	m = send(m, key("down"))
	m = send(m, snapshotMsg{snap: snap(cl("alpha", 1, 1), cl("gamma", 1, 1))})
	it, ok := m.cur().list.Selected()
	if !ok || m.cur().detail.item.Key() != it.Key() || it.Key() == "beta" {
		t.Fatalf("detail=%q list=%q", m.cur().detail.item.Key(), it.Key())
	}
	m = send(m, snapshotMsg{snap: snap()})
	if !strings.Contains(m.View(), "no cluster selected") {
		t.Fatal("empty snapshot clears the detail")
	}
}

func TestFirstFetchFailureShowsBannerWithoutSnapshot(t *testing.T) {
	m := sized(New(testOpts(nil)), 100, 24)
	m = send(m, snapshotMsg{err: errors.New("dial refused")})
	v := m.View()
	for _, want := range []string{"dial refused", "never connected", "waiting for Envoy…"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "no clusters") {
		t.Fatalf("list must keep waiting until the first success:\n%s", v)
	}
	if !strings.Contains(strings.Split(v, "\n")[m.headerRows()-1], "Clusters") {
		t.Fatalf("tab row must stay visible:\n%s", v)
	}
}

func TestHelpHintVisibleAtMinimumWidth(t *testing.T) {
	m := sized(New(testOpts(nil)), 80, 24)
	m = send(m, snapshotMsg{snap: snap(cl("api", 1, 1))})
	lines := strings.Split(m.View(), "\n")
	status := ansi.Strip(lines[len(lines)-1])
	for _, want := range []string{"? help", "q quit"} {
		if !strings.Contains(status, want) {
			t.Fatalf("missing %q in status %q", want, status)
		}
	}
}

func TestRefreshAfterError(t *testing.T) {
	m := New(testOpts(func(context.Context) (snapshot.Snapshot, error) { return snap(), nil }))
	m = send(m, snapshotMsg{err: errors.New("boom")})
	m, cmd := sendCmd(m, key("r"))
	if cmd == nil || !m.inflight {
		t.Fatalf("r after an error must fetch: cmd=%v inflight=%v", cmd != nil, m.inflight)
	}
}

func TestZeroFetchedAtFallsBackToClock(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snapshot.Snapshot{Clusters: []snapshot.Cluster{cl("api", 1, 1)}}})
	if v := m.View(); !strings.Contains(v, "updated 0s ago") {
		t.Fatalf("age must be measured from receipt:\n%s", v)
	}
	m = send(m, snapshotMsg{err: errors.New("boom")})
	if strings.Contains(m.bannerText(), "never connected") {
		t.Fatal("a failure after a success must not say never connected")
	}
}

func helpStatus(t *testing.T, w int, detail bool) string {
	t.Helper()
	m := sized(New(testOpts(nil)), w, 30)
	m = send(m, snapshotMsg{snap: snap(cl("api", 1, 1))})
	if detail {
		m = send(m, key("enter"))
	}
	m = send(m, key("?"))
	lines := strings.Split(m.View(), "\n")
	status := ansi.Strip(lines[len(lines)-1])
	if lipgloss.Width(status) > w {
		t.Fatalf("w=%d: status is %d wide", w, lipgloss.Width(status))
	}
	return status
}

func TestHelpLineShowsEveryKeyFrom120Columns(t *testing.T) {
	every := []string{"tab/shift+tab", "enter", "esc", "↑↓/jk", "g/G", "PgUp/PgDn", "/ filter", "c config", "o/e overview", "r refresh", "x dismiss", "? close", "q quit"}
	for _, w := range []int{120, 140, 200} {
		for _, detail := range []bool{false, true} {
			status := helpStatus(t, w, detail)
			for _, want := range every {
				if !strings.Contains(status, want) {
					t.Fatalf("w=%d detail=%v: help line misses %q: %q", w, detail, want, status)
				}
			}
		}
	}
}

func TestHelpLineKeepsTheRarelyKnownKeysWhenNarrow(t *testing.T) {
	for w, want := range map[int][]string{100: {"q quit", "x dismiss", "r refresh", "? close"}, 80: {"q quit", "? close", "x dismiss"}} {
		status := helpStatus(t, w, false)
		for _, f := range want {
			if !strings.Contains(status, f) {
				t.Fatalf("w=%d: help line misses %q: %q", w, f, status)
			}
		}
	}
}

// The banner's first row carries "last ok Ns ago", whose length changes with
// the clock. The row count is fixed when the layout runs, so a render between
// events must still fit the terminal.
func TestBannerHeightIgnoresTheClock(t *testing.T) {
	now := fixedNow.Add(9 * time.Second)
	o := testOpts(nil)
	o.Config.Admin = "http://" + strings.Repeat("a", 31) + ":15000"
	o.Clock = func() time.Time { return now }
	m := sized(New(o), 80, 30)
	m = send(m, snapshotMsg{snap: snap(cl("api", 1, 2))})
	m = send(m, snapshotMsg{err: errors.New("context deadline exceeded")})

	now = fixedNow.Add(10 * time.Second) // "9s" -> "10s" pushes the first row past the width
	if n := len(strings.Split(m.View(), "\n")); n != 30 {
		t.Fatalf("frame is %d lines, want 30", n)
	}
	if got, want := m.cur().list.height, m.paneHeight(); got != want {
		t.Fatalf("list sized for %d rows but the frame gives it %d", got, want)
	}
}
