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

	"github.com/regeda/txenvoy/internal/config"
	"github.com/regeda/txenvoy/internal/snapshot"
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
	if c, _ := m.list.Selected(); c.Name != "beta" {
		t.Fatal("down selects beta")
	}
	if !strings.Contains(m.detail.View(), "beta") {
		t.Fatal("detail follows the selection")
	}
	m = send(m, key("c"))
	if m.detail.tab != tabConfig {
		t.Fatal("c opens config")
	}
	m = send(m, key("e"))
	if m.detail.tab != tabEndpoints {
		t.Fatal("e opens endpoints")
	}
	m = send(m, key("/"))
	if !m.list.filtering {
		t.Fatal("/ starts the list filter")
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
			for _, l := range lines {
				if lipgloss.Width(l) > sz[0] {
					t.Fatalf("%v err=%v: line width %d: %q", sz, withErr, lipgloss.Width(l), l)
				}
			}
		}
	}
}

func TestBannerDoesNotMoveContent(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snap(cl("api", 1, 2), cl("db", 2, 2))})
	before := strings.Split(m.View(), "\n")

	m = send(m, snapshotMsg{err: errors.New(strings.Repeat("multi line error ", 30))})
	after := strings.Split(m.View(), "\n")
	if len(before) != len(after) {
		t.Fatal("banner changed the frame height")
	}
	rows := len(bannerLines(m.bannerText(), 120, maxBannerRows))
	if rows < 2 || rows > maxBannerRows {
		t.Fatalf("banner rows = %d", rows)
	}
	for i := rows; i < len(before); i++ {
		// status bar text is unchanged by the error, and panes must not move
		if before[i] != after[i] {
			t.Fatalf("line %d moved:\n%q\n%q", i, before[i], after[i])
		}
	}
	if !strings.Contains(after[0], "disconnected") {
		t.Fatalf("banner first row: %q", after[0])
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
	for _, sz := range [][2]int{{120, 30}, {80, 24}, {80, 12}, {200, 60}} {
		m := sized(New(testOpts(nil)), sz[0], sz[1])
		m = send(m, snapshotMsg{snap: snap(cl("api", 1, 2), cl("db", 2, 2))})
		listW := min(max(sz[0]*35/100, 28), 50)
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
	c, ok := m.list.Selected()
	if !ok || m.detail.cluster.Name != c.Name || c.Name == "beta" {
		t.Fatalf("detail=%q list=%q", m.detail.cluster.Name, c.Name)
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
