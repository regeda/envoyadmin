package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/regeda/envoyadmin/internal/snapshot"
)

func cl(name string, healthy, total int) snapshot.Cluster {
	c := snapshot.Cluster{Name: name, Type: "EDS"}
	for i := 0; i < total; i++ {
		h := snapshot.Host{Address: fmt.Sprintf("10.0.0.%d:80", i)}
		if i >= healthy {
			h.Flags = []string{"FAILED_ACTIVE_HC"}
		}
		c.Hosts = append(c.Hosts, h)
	}
	return c
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typeInto(l *listPane, s string) {
	for _, r := range s {
		l.HandleKey(key(string(r)))
	}
}

func newList(cs ...snapshot.Cluster) listPane {
	l := newListPane()
	l.SetSize(60, 12)
	l.SetItems(clusterItems(cs))
	return l
}

func TestCursorMovesAndClamps(t *testing.T) {
	l := newList(cl("a", 1, 1), cl("b", 1, 1), cl("c", 1, 1))
	l.HandleKey(key("up"))
	if it, _ := l.Selected(); it.Key() != "a" {
		t.Fatal("up at top must clamp")
	}
	for i := 0; i < 5; i++ {
		l.HandleKey(key("down"))
	}
	if it, _ := l.Selected(); it.Key() != "c" {
		t.Fatal("down at bottom must clamp")
	}
}

func TestSelectionFollowsNameAcrossRefresh(t *testing.T) {
	l := newList(cl("a", 1, 1), cl("b", 1, 1), cl("c", 1, 1))
	l.HandleKey(key("down")) // b
	l.SetItems(clusterItems([]snapshot.Cluster{cl("z", 1, 1), cl("c", 1, 1), cl("b", 1, 1)}))
	if it, _ := l.Selected(); it.Key() != "b" {
		t.Fatalf("selection jumped to %q", it.Key())
	}
}

func TestSelectedClusterDisappears(t *testing.T) {
	l := newList(cl("a", 1, 1), cl("b", 1, 1), cl("c", 1, 1))
	l.HandleKey(key("down"))
	l.HandleKey(key("down")) // c, last row
	l.SetItems(clusterItems([]snapshot.Cluster{cl("a", 1, 1), cl("b", 1, 1)}))
	if it, ok := l.Selected(); !ok || it.Key() != "b" {
		t.Fatalf("should fall to nearest row, got %q ok=%v", it.Key(), ok)
	}
	l.SetItems(clusterItems(nil))
	if _, ok := l.Selected(); ok {
		t.Fatal("empty list must have no selection")
	}
	_ = l.View() // must not panic
}

func TestFilter(t *testing.T) {
	l := newList(cl("api-gateway", 1, 1), cl("orders-db", 1, 1), cl("apiary", 1, 1))
	l.HandleKey(key("/"))
	if !l.filtering {
		t.Fatal("/ must start filtering")
	}
	typeInto(&l, "api")
	if len(l.visible) != 2 {
		t.Fatalf("visible=%d", len(l.visible))
	}
	l.HandleKey(key("enter"))
	if l.filtering || len(l.visible) != 2 {
		t.Fatal("enter keeps the filter and leaves filter mode")
	}
	l.HandleKey(key("esc"))
	if len(l.visible) != 3 {
		t.Fatal("esc clears the filter")
	}
}

func TestFilterWithNoMatch(t *testing.T) {
	l := newList(cl("api", 1, 1))
	l.HandleKey(key("/"))
	typeInto(&l, "zzzz")
	if _, ok := l.Selected(); ok {
		t.Fatal("no match means no selection")
	}
	if !strings.Contains(l.View(), "no matches") {
		t.Fatal("view should say no matches")
	}
	l.HandleKey(key("down")) // must not panic
}

func TestEmptyAndWaiting(t *testing.T) {
	l := newList()
	l.SetEmptyText("no clusters")
	if !strings.Contains(l.View(), "no clusters") {
		t.Fatal(l.View())
	}
	l.SetWaiting(true)
	if !strings.Contains(l.View(), "waiting for Envoy") {
		t.Fatal(l.View())
	}
}

func TestRowShowsHealthCounts(t *testing.T) {
	l := newList(cl("api", 3, 4))
	if v := l.View(); !strings.Contains(v, "3/4") || !strings.Contains(v, "EDS") || !strings.Contains(v, "api") {
		t.Fatal(v)
	}
}

func TestLongAndWideNamesNeverExceedWidth(t *testing.T) {
	long := strings.Repeat("very-long-cluster-name-", 10)
	l := newList(cl(long, 1, 2), cl("日本語のクラスター名前日本語のクラスター名前", 1, 1), cl("ok", 1, 1))
	for _, line := range strings.Split(l.View(), "\n") {
		if w := lipgloss.Width(line); w > 60 {
			t.Fatalf("line width %d > 60: %q", w, line)
		}
	}
	if n := strings.Count(l.View(), "\n") + 1; n != 12 {
		t.Fatalf("height = %d lines, want 12", n)
	}
}

func TestLongNameTruncatedKeepsCountColumn(t *testing.T) {
	long := strings.Repeat("very-long-cluster-name-", 10)
	l := newList(cl(long, 1, 2), cl("日本語のクラスター名前日本語のクラスター名前", 1, 1), cl("ok", 3, 3))
	lines := strings.Split(ansi.Strip(l.View()), "\n")
	for i, want := range []string{"1/2", "1/1", "3/3"} {
		line := strings.TrimRight(lines[1+i], " ")
		if !strings.HasSuffix(line, want) {
			t.Fatalf("row %d %q must end with %q", i, line, want)
		}
	}
	if !strings.Contains(lines[1], "very-long-cluster-name-") || strings.Contains(lines[1], long) {
		t.Fatalf("name must be truncated at the tail: %q", lines[1])
	}
}

func TestZeroHostRow(t *testing.T) {
	l := newList(cl("empty", 0, 0))
	if v := l.View(); !strings.Contains(v, "0/0") || !strings.Contains(v, "empty") {
		t.Fatal(v)
	}
}

func TestHighlightUsesByteOffsetsForMultiByteNames(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })

	l := newList(cl("日本-api", 1, 1))
	l.HandleKey(key("/"))
	typeInto(&l, "api")
	hl := lipgloss.NewStyle().Bold(true).Underline(true).Render
	v := l.View()
	for _, r := range "api" {
		if !strings.Contains(v, hl(string(r))) {
			t.Fatalf("%q not highlighted in %q", r, v)
		}
	}
	for _, r := range "日本-" {
		if strings.Contains(v, hl(string(r))) {
			t.Fatalf("%q wrongly highlighted in %q", r, v)
		}
	}
}

func TestScrollWindowShrinksWithList(t *testing.T) {
	var cs []snapshot.Cluster
	for i := 0; i < 50; i++ {
		cs = append(cs, cl(fmt.Sprintf("svc-%02d", i), 1, 1))
	}
	l := newList(cs...)
	for i := 0; i < 49; i++ {
		l.HandleKey(key("down"))
	}
	l.SetItems(clusterItems(cs[:20]))
	v := l.View()
	if !strings.Contains(v, "svc-19") || !strings.Contains(v, "svc-09") {
		t.Fatalf("window must be full after shrink:\n%s", v)
	}
}

func TestScrollWindowAfterPaneGrows(t *testing.T) {
	var cs []snapshot.Cluster
	for i := 0; i < 20; i++ {
		cs = append(cs, cl(fmt.Sprintf("svc-%02d", i), 1, 1))
	}
	l := newList(cs...)
	for i := 0; i < 19; i++ {
		l.HandleKey(key("down"))
	}
	l.SetSize(60, 40)
	if !strings.Contains(l.View(), "svc-00") {
		t.Fatalf("all rows must show once the pane fits them:\n%s", l.View())
	}
}

func TestScrollDownThenUpKeepsCursorVisible(t *testing.T) {
	var cs []snapshot.Cluster
	for i := 0; i < 50; i++ {
		cs = append(cs, cl(fmt.Sprintf("svc-%02d", i), 1, 1))
	}
	l := newList(cs...)
	for i := 0; i < 40; i++ {
		l.HandleKey(key("down"))
	}
	for i := 0; i < 35; i++ {
		l.HandleKey(key("up"))
	}
	if !strings.Contains(l.View(), "svc-05") {
		t.Fatalf("cursor row must be visible:\n%s", l.View())
	}
}

func TestScrollKeepsCursorVisible(t *testing.T) {
	var cs []snapshot.Cluster
	for i := 0; i < 50; i++ {
		cs = append(cs, cl(fmt.Sprintf("svc-%02d", i), 1, 1))
	}
	l := newList(cs...)
	for i := 0; i < 30; i++ {
		l.HandleKey(key("down"))
	}
	if !strings.Contains(l.View(), "svc-30") {
		t.Fatal("selected row must be visible after scrolling")
	}
}

func TestTinyPaneDoesNotPanic(t *testing.T) {
	l := newListPane()
	_ = l.View() // zero size, no data
	l.SetSize(1, 1)
	l.SetItems(clusterItems([]snapshot.Cluster{cl("a", 1, 1)}))
	_ = l.View()
}

// Rows must never be wider than the pane, even at the narrowest list width
// (28), otherwise the pane wraps them onto a second line.
func TestListRowsFitNarrowPane(t *testing.T) {
	for _, w := range []int{28, 31, 32, 40} {
		l := newListPane()
		l.SetSize(w, 8)
		l.SetItems(clusterItems([]snapshot.Cluster{cl("orders-database-primary", 0, 2), cl("api-gateway", 2, 3)}))
		for i := range l.visible {
			if rw := lipgloss.Width(l.row(i)); rw > w {
				t.Fatalf("w=%d row %d width %d: %q", w, i, rw, l.row(i))
			}
		}
	}
}

func TestEmptyTextNoticeAndPrecedence(t *testing.T) {
	l := newListPane()
	l.SetSize(60, 12)
	l.SetEmptyText("no widgets")
	if !strings.Contains(l.View(), "no widgets") {
		t.Fatal(l.View())
	}
	l.SetNotice("widgets unavailable: boom")
	if v := l.View(); !strings.Contains(v, "widgets unavailable: boom") || strings.Contains(v, "no widgets") {
		t.Fatalf("a notice replaces the empty text:\n%s", v)
	}
	l.SetWaiting(true)
	if !strings.Contains(l.View(), "waiting for Envoy") {
		t.Fatal("waiting wins over a notice")
	}
	l.SetWaiting(false)
	l.SetNotice(strings.Repeat("long error words ", 30))
	for _, line := range strings.Split(l.View(), "\n") {
		if lipgloss.Width(line) > 60 {
			t.Fatalf("notice line wider than the pane: %q", line)
		}
	}
	if n := len(strings.Split(l.View(), "\n")); n != 12 {
		t.Fatalf("height %d, want 12", n)
	}
}

func TestSelectionSurvivesATransientEmptyList(t *testing.T) {
	items := func() []resourceItem {
		return listenerItems([]snapshot.Listener{{Name: "a"}, {Name: "b"}, {Name: "c"}})
	}
	l := newListPane()
	l.SetSize(40, 10)
	l.SetItems(items())
	l.HandleKey(key("j"))
	l.HandleKey(key("j"))
	l.SetItems(nil) // a failed dump
	if _, ok := l.Selected(); ok {
		t.Fatal("an empty list has no selection")
	}
	l.SetItems(items())
	if it, ok := l.Selected(); !ok || it.Key() != "c" {
		t.Fatalf("the selection must come back by key, got %v %v", it, ok)
	}

	// a filter that matches nothing, then cleared
	l.HandleKey(key("/"))
	for _, r := range "zzzz" {
		l.HandleKey(key(string(r)))
	}
	if len(l.visible) != 0 {
		t.Fatal("precondition: nothing matches")
	}
	l.HandleKey(key("esc"))
	if it, ok := l.Selected(); !ok || it.Key() != "c" {
		t.Fatalf("clearing the filter restores the selection, got %v %v", it, ok)
	}

	// a vanished key with other items present still falls to the nearest row
	l.SetItems(listenerItems([]snapshot.Listener{{Name: "a"}, {Name: "b"}}))
	if it, _ := l.Selected(); it == nil || it.Key() != "b" {
		t.Fatalf("a gone key falls to the nearest row, got %v", it)
	}
}
