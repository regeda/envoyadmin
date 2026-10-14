package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/regeda/envoyadmin/internal/snapshot"
)

func lsnap(ls ...snapshot.Listener) snapshot.Snapshot {
	return snapshot.Snapshot{
		Clusters:  []snapshot.Cluster{cl("alpha", 1, 2), cl("beta", 1, 1)},
		Listeners: ls, Version: "1.31.0", FetchedAt: fixedNow,
	}
}

// onListeners is listenersModel with the Listeners tab showing.
func onListeners() Model { return send(listenersModel(), key("tab")) }

func listenersModel() Model {
	m := sized(New(testOpts(nil)), 120, 30)
	return send(m, snapshotMsg{snap: lsnap(
		lst("l-active", "0.0.0.0:8080", snapshot.ListenerActive),
		lst("l-warming", "0.0.0.0:9090", snapshot.ListenerWarming),
		lst("l-error", "0.0.0.0:9443", snapshot.ListenerError),
	)})
}

func activeName(m Model) string {
	it, _ := m.cur().list.Selected()
	if it == nil {
		return ""
	}
	return it.Key()
}

func TestTabSwitchesResourcesAndShiftTabGoesBack(t *testing.T) {
	m := listenersModel()
	if m.active != kindClusters || activeName(m) != "alpha" {
		t.Fatal("starts on Clusters")
	}
	m = send(m, key("tab"))
	if m.active != kindListeners || activeName(m) != "l-active" {
		t.Fatalf("tab -> Listeners, got %v %q", m.active, activeName(m))
	}
	m = send(m, key("tab"))
	if m.active != kindClusters {
		t.Fatal("tab wraps back to Clusters")
	}
	m = send(m, key("shift+tab"))
	if m.active != kindListeners {
		t.Fatal("shift+tab goes backwards (and wraps)")
	}
	m = send(m, key("shift+tab"))
	if m.active != kindClusters {
		t.Fatal("shift+tab back to Clusters")
	}
}

func TestEachTabKeepsItsOwnState(t *testing.T) {
	m := listenersModel()
	m = send(m, key("j")) // clusters: beta
	m = send(m, key("tab"))
	m = send(m, key("j")) // listeners: l-warming
	m = send(m, key("c")) // listeners details: Config
	m = typeText(send(m, key("/")), "err")
	m = send(m, key("enter")) // keep the filter, leave filter mode
	if activeName(m) != "l-error" {
		t.Fatalf("filter selects l-error, got %q", activeName(m))
	}
	m = send(m, key("tab"))
	if activeName(m) != "beta" || m.cur().list.query != "" {
		t.Fatalf("clusters keep their selection and have no filter: %q %q", activeName(m), m.cur().list.query)
	}
	m = send(m, key("tab"))
	if activeName(m) != "l-error" || m.cur().list.query != "err" || m.cur().detail.tab != tabConfig {
		t.Fatalf("listeners keep selection, filter and detail tab: %q %q %v", activeName(m), m.cur().list.query, m.cur().detail.tab)
	}
}

func TestTabIsIgnoredWhileTypingAFilter(t *testing.T) {
	m := send(listenersModel(), key("/"))
	for _, k := range []string{"tab", "shift+tab"} {
		// checked after each key: a tab followed by a shift+tab would round-trip and hide the bug
		m = send(m, key(k))
		if m.active != kindClusters || !m.cur().list.filtering {
			t.Fatalf("%s must be ignored in filter mode (active=%v filtering=%v)", k, m.active, m.cur().list.filtering)
		}
	}
	m = send(m, key("esc"))
	m = send(m, key("tab"))
	if m.active != kindListeners {
		t.Fatal("tab works again after the filter is closed")
	}
}

func TestTabSwitchLandsOnTheList(t *testing.T) {
	m := send(listenersModel(), key("enter"))
	if m.focus != focusDetail {
		t.Fatal("precondition: details focused")
	}
	m = send(m, key("tab"))
	if m.focus != focusList || m.active != kindListeners {
		t.Fatalf("a switch lands on the list of the target tab (focus=%v active=%v)", m.focus, m.active)
	}
}

func TestEnterEscFocusFlow(t *testing.T) {
	m := onListeners()
	m = send(m, key("enter"))
	if m.focus != focusDetail || !strings.Contains(m.View(), "focus: detail") {
		t.Fatal("enter focuses the details")
	}
	m = send(m, key("esc"))
	if m.focus != focusList || strings.Contains(m.View(), "focus: detail") {
		t.Fatal("esc returns to the list")
	}
	// filter flow: enter keeps the filter, a second enter opens the details
	m = typeText(send(m, key("/")), "warm")
	m = send(m, key("enter"))
	if m.focus != focusList || m.cur().list.filtering || len(m.cur().list.visible) != 1 {
		t.Fatalf("enter in filter mode keeps the filter and stays on the list (focus=%v)", m.focus)
	}
	m = send(m, key("enter"))
	if m.focus != focusDetail {
		t.Fatal("the second enter opens the details")
	}
	m = send(m, key("esc"))
	m = send(m, key("esc")) // in the list: clears the filter
	if len(m.cur().list.visible) != 3 {
		t.Fatalf("esc in the list clears the filter, %d visible", len(m.cur().list.visible))
	}
	// esc in filter mode clears and leaves filter mode
	m = typeText(send(m, key("/")), "err")
	m = send(m, key("esc"))
	if m.cur().list.filtering || len(m.cur().list.visible) != 3 {
		t.Fatal("esc in filter mode clears the filter and leaves filter mode")
	}
}

func TestEnterWithAnEmptyListDoesNothing(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: lsnap()}) // no listeners
	m = send(m, key("tab"))
	m = send(m, key("enter"))
	if m.focus != focusList {
		t.Fatal("enter on an empty list must not focus the details")
	}
	m = send(m, key("tab"))
	m = typeText(send(m, key("/")), "zzzz")
	m = send(m, key("enter"))
	m = send(m, key("enter"))
	if m.focus != focusList {
		t.Fatal("enter with no match must not focus the details")
	}
}

func TestDetailsFocusScrollsConfigAndOverviewWithArrows(t *testing.T) {
	l := lst("big", "0.0.0.0:1", snapshot.ListenerActive)
	for i := 0; i < 30; i++ {
		l.Chains = append(l.Chains, snapshot.FilterChain{Label: strings.Repeat("c", 1) + string(rune('A'+i%26)), Filters: fl("tcp_proxy")})
	}
	l.Config = snapshot.ConfigInfo{YAML: strings.Repeat("key: value\n", 80) + "last: line\n"}
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: lsnap(l, lst("other", "0.0.0.0:2", snapshot.ListenerActive))})
	m = send(m, key("tab"))
	m = send(m, key("enter"))
	for i := 0; i < 200; i++ {
		m = send(m, key("down"))
	}
	if activeName(m) != "big" || m.cur().detail.scroll == 0 {
		t.Fatalf("arrows scroll the Overview and leave the selection alone (selected %q scroll %d)", activeName(m), m.cur().detail.scroll)
	}
	m = send(m, key("c"))
	for i := 0; i < 200; i++ {
		m = send(m, key("down"))
	}
	if !strings.Contains(m.View(), "last: line") || activeName(m) != "big" {
		t.Fatal("arrows scroll the Config YAML too")
	}
	m = send(m, key("o"))
	if m.cur().detail.tab != tabOverview {
		t.Fatal("o opens the first tab")
	}
	m = send(m, key("c"))
	m = send(m, key("e"))
	if m.cur().detail.tab != tabOverview {
		t.Fatal("e is an alias for the first tab")
	}
}

func TestHeaderShowsBothTabsWithTheActiveOneMarked(t *testing.T) {
	m := listenersModel()
	v := m.View()
	if !strings.Contains(v, "Clusters (2)") || !strings.Contains(v, "Listeners (3)") {
		t.Fatalf("both tabs with counts:\n%s", v)
	}
	// in a no-colour render the active tab is the one wrapped in the reversed padding; assert order and counts only
	if strings.Index(v, "Clusters (2)") > strings.Index(v, "Listeners (3)") {
		t.Fatal("tab order is Clusters, Listeners")
	}
	if compact := sized(m, 100, 20).View(); !strings.Contains(compact, "Listeners (3)") {
		t.Fatalf("the compact header keeps both tabs:\n%s", compact)
	}
}

func TestStatusCountsFollowTheActiveTab(t *testing.T) {
	m := listenersModel()
	if v := m.View(); !strings.Contains(v, "2 clusters") {
		t.Fatalf("clusters status:\n%s", v)
	}
	m = send(m, key("tab"))
	v := m.View()
	if !strings.Contains(v, "3 listeners · 2 not active") {
		t.Fatalf("listeners status:\n%s", v)
	}
}

func TestHintsPutTheKeysThatMatterFirst(t *testing.T) {
	m := sized(New(testOpts(nil)), 80, 24)
	m = send(m, snapshotMsg{snap: lsnap(lst("a", "0.0.0.0:1", snapshot.ListenerActive))})
	v := m.View()
	last := v[strings.LastIndex(v, "\n")+1:]
	for _, frag := range []string{"? help", "q quit", "tab", "enter"} {
		if !strings.Contains(last, frag) {
			t.Fatalf("at 80 columns the status line must still show %q: %q", frag, last)
		}
	}
	m = send(m, key("enter"))
	v = m.View()
	last = v[strings.LastIndex(v, "\n")+1:]
	if !strings.Contains(last, "esc back") {
		t.Fatalf("details focus hint: %q", last)
	}
}

func TestListenerListInlineMessages(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, key("tab"))
	if !strings.Contains(m.View(), "waiting for Envoy") {
		t.Fatalf("before the first poll:\n%s", m.View())
	}
	m = send(m, snapshotMsg{snap: lsnap()})
	if !strings.Contains(m.View(), "no listeners") {
		t.Fatalf("no listeners:\n%s", m.View())
	}
	failed := lsnap()
	failed.ListenersErr = "listeners unavailable: GET /config_dump: 503 Service Unavailable"
	m = send(m, snapshotMsg{snap: failed})
	v := m.View()
	if !strings.Contains(v, "listeners unavailable") || !strings.Contains(v, "Listeners (!)") {
		t.Fatalf("a failed dump shows inline and in the tab:\n%s", v)
	}
	if m.bannerVisible() {
		t.Fatal("a listener failure is not a banner")
	}
	if strings.Contains(m.View(), "no listeners") {
		t.Fatal("the failure message replaces 'no listeners'")
	}
	// the Clusters tab is unaffected
	m = send(m, key("tab"))
	if !strings.Contains(m.View(), "alpha") || strings.Contains(m.View(), "listeners unavailable") {
		t.Fatalf("clusters keep working:\n%s", m.View())
	}
	// a long message wraps inside the pane
	failed.ListenersErr = "listeners unavailable: " + strings.Repeat("very long reason ", 30)
	m = send(send(m, snapshotMsg{snap: failed}), key("tab"))
	for i, line := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(ansiStrip(line)); w > 120 {
			t.Fatalf("line %d is %d cells wide", i, w)
		}
	}
	if n := len(strings.Split(m.View(), "\n")); n != 30 {
		t.Fatalf("height %d, want 30", n)
	}
}

func TestSelectedListenerDisappears(t *testing.T) {
	m := listenersModel()
	m = send(m, key("tab"))
	m = send(m, key("j"))
	m = send(m, key("j")) // l-error, the last row
	m = send(m, snapshotMsg{snap: lsnap(lst("l-active", "0.0.0.0:8080", snapshot.ListenerActive), lst("l-warming", "0.0.0.0:9090", snapshot.ListenerWarming))})
	it, ok := m.cur().list.Selected()
	if !ok || it.Key() != "l-warming" || m.cur().detail.item.Key() != "l-warming" {
		t.Fatalf("selection falls to the nearest row and the details follow: %v %v", ok, it)
	}
	m = send(m, snapshotMsg{snap: lsnap()})
	if _, ok := m.cur().list.Selected(); ok || !strings.Contains(m.View(), "no listener selected") {
		t.Fatalf("empty listeners:\n%s", m.View())
	}
}

func TestListenersWithAwkwardData(t *testing.T) {
	bare := snapshot.Listener{Name: "bare"}
	wide := lst("日本語のリスナー", "unix:/var/run/日本.sock", snapshot.ListenerActive)
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: lsnap(bare, wide)})
	m = send(m, key("tab"))
	for _, k := range []string{"enter", "c", "o", "esc", "j"} {
		m = send(m, key(k))
		for i, line := range strings.Split(m.View(), "\n") {
			if w := lipgloss.Width(ansiStrip(line)); w > 120 {
				t.Fatalf("after %s line %d is %d cells wide", k, i, w)
			}
		}
	}
}

func TestFilteringListenersByAddress(t *testing.T) {
	m := send(listenersModel(), key("tab"))
	m = typeText(send(m, key("/")), "9443")
	if activeName(m) != "l-error" || len(m.cur().list.visible) != 1 {
		t.Fatalf("filter matches the address: %q (%d visible)", activeName(m), len(m.cur().list.visible))
	}
}

func TestViewsFitTheTerminalAtEverySize(t *testing.T) {
	for _, sz := range [][2]int{{120, 30}, {80, 24}, {100, 20}, {80, 12}, {200, 60}} {
		m := sized(New(testOpts(nil)), sz[0], sz[1])
		m = send(m, snapshotMsg{snap: lsnap(lst("a", "0.0.0.0:1", snapshot.ListenerActive))})
		for _, steps := range [][]string{{}, {"tab"}, {"tab", "enter"}, {"tab", "enter", "c"}} {
			mm := m
			for _, k := range steps {
				mm = send(mm, key(k))
			}
			if n := len(strings.Split(mm.View(), "\n")); n != sz[1] {
				t.Fatalf("%v %v: %d lines", sz, steps, n)
			}
		}
	}
}

func TestModelHeaderMarksTheActiveTab(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	m := listenersModel()
	tabRow := func(m Model) string { return strings.Split(m.View(), "\n")[3] }
	a := tabRow(m)
	b := tabRow(send(m, key("tab")))
	if a == b {
		t.Fatal("switching tabs must change which header tab is marked")
	}
	if ansiStrip(a) != ansiStrip(b) {
		t.Fatalf("only the styling may differ: %q vs %q", ansiStrip(a), ansiStrip(b))
	}
}

func TestListenerFailureWarnsOnlyItsOwnTab(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	failed := lsnap()
	failed.ListenersErr = "listeners unavailable: boom"
	m = send(m, snapshotMsg{snap: failed})
	v := ansiStrip(m.View())
	if !strings.Contains(v, "Clusters (2)") || !strings.Contains(v, "Listeners (!)") || strings.Contains(v, "disconnected") {
		t.Fatalf("isolated failure:\n%s", v)
	}
}

func TestViewIsExactAtEverySizeIncludingBeforeTheFirstResize(t *testing.T) {
	tiny := [][2]int{{0, 0}, {1, 1}, {79, 40}, {200, 11}, {80, 12}, {120, 30}}
	for _, sz := range tiny {
		for _, withBanner := range []bool{false, true} {
			m := New(testOpts(nil))
			if n := len(strings.Split(m.View(), "\n")); n != 1 {
				t.Fatalf("before the first size the view is one line, got %d", n)
			}
			m = sized(m, sz[0], sz[1])
			m = send(m, snapshotMsg{snap: lsnap(lst("a", "0.0.0.0:1", snapshot.ListenerActive))})
			if withBanner {
				m = send(m, snapshotMsg{err: errors.New("boom " + strings.Repeat("x", 200))})
			}
			for _, steps := range [][]string{{}, {"tab"}, {"tab", "enter"}, {"tab", "/", "tab"}} {
				mm := m
				for _, k := range steps {
					mm = send(mm, key(k))
				}
				v := mm.View()
				lines := strings.Split(v, "\n")
				if sz[0] >= minWidth && sz[1] >= minHeight && len(lines) != sz[1] {
					t.Fatalf("%v %v banner=%v: %d lines", sz, steps, withBanner, len(lines))
				}
				for i, l := range lines {
					if w := lipgloss.Width(l); sz[0] > 0 && w > sz[0] { // width 0 is "unknown": the too-small notice is shown whole
						t.Fatalf("%v %v banner=%v line %d is %d wide", sz, steps, withBanner, i, w)
					}
				}
			}
		}
	}
}

func TestEnterDoesNotPanicOrFocusWhileFiltering(t *testing.T) {
	m := send(listenersModel(), key("tab"))
	m = typeText(send(m, key("/")), "9")
	if len(m.cur().list.visible) == 0 {
		t.Fatal("precondition: the filter matches something")
	}
	m = send(m, key("enter"))
	if m.focus != focusList || m.cur().list.filtering || m.cur().list.query != "9" {
		t.Fatalf("Enter while filtering only accepts the filter (focus %v, filtering %v, query %q)", m.focus, m.cur().list.filtering, m.cur().list.query)
	}
	if strings.Contains(m.View(), "focus: detail") {
		t.Fatal("the details must not look focused")
	}
	m = send(m, key("esc"))
	m = send(m, key("esc"))
	if m.focus != focusList {
		t.Fatal("focus stays on the list")
	}
}

func TestModelKeepsTheListenerSelectedAcrossAFailedDump(t *testing.T) {
	good := lsnap(
		lst("l-1", "0.0.0.0:1", snapshot.ListenerActive),
		lst("l-2", "0.0.0.0:2", snapshot.ListenerActive),
		lst("l-3", "0.0.0.0:3", snapshot.ListenerActive),
	)
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: good})
	m = send(m, key("tab"))
	m = send(m, key("j"))
	m = send(m, key("j"))
	if activeName(m) != "l-3" {
		t.Fatalf("precondition: %q", activeName(m))
	}
	failed := lsnap()
	failed.ListenersErr = "listeners unavailable: boom"
	m = send(m, snapshotMsg{snap: failed})
	m = send(m, snapshotMsg{snap: good})
	if activeName(m) != "l-3" || m.cur().detail.item.Key() != "l-3" {
		t.Fatalf("the same listener must be selected again, got %q", activeName(m))
	}
}

func TestStatusSaysListenersUnavailableWhenTheDumpFailed(t *testing.T) {
	m := onListeners()
	failed := lsnap()
	failed.ListenersErr = "listeners unavailable: boom"
	m = send(m, snapshotMsg{snap: failed})
	status := ansiStrip(m.statusLine())
	if !strings.Contains(status, "listeners unavailable") || strings.Contains(status, "0 listeners") || strings.Contains(status, "not active") {
		t.Fatalf("status: %q", status)
	}
}

func TestSummariesUseSingularForms(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	s := lsnap(lst("only", "0.0.0.0:1", snapshot.ListenerActive))
	s.Clusters = []snapshot.Cluster{cl("alpha", 1, 1)}
	m = send(m, snapshotMsg{snap: s})
	if st := ansiStrip(m.statusLine()); !strings.Contains(st, " 1 cluster · 0 unhealthy") {
		t.Fatalf("clusters: %q", st)
	}
	m = send(m, key("tab"))
	if st := ansiStrip(m.statusLine()); !strings.Contains(st, " 1 listener · 0 not active") {
		t.Fatalf("listeners: %q", st)
	}
}

func TestFocusLeavesTheDetailsWhenNothingIsSelected(t *testing.T) {
	m := onListeners()
	m = send(m, key("enter"))
	if m.focus != focusDetail || !strings.Contains(m.View(), "focus: detail") {
		t.Fatal("precondition: details focused")
	}
	m = send(m, snapshotMsg{snap: lsnap()})
	if m.focus != focusList || strings.Contains(m.View(), "focus: detail") {
		t.Fatalf("with nothing selected the focus must drop to the list (focus %v):\n%s", m.focus, m.View())
	}
	if strings.Contains(ansiStrip(m.View()), "▌") {
		t.Fatal("no focus marker either")
	}
}
