package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/snapshot"
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
	l.SetClusters(cs)
	return l
}

func TestCursorMovesAndClamps(t *testing.T) {
	l := newList(cl("a", 1, 1), cl("b", 1, 1), cl("c", 1, 1))
	l.HandleKey(key("up"))
	if c, _ := l.Selected(); c.Name != "a" {
		t.Fatal("up at top must clamp")
	}
	for i := 0; i < 5; i++ {
		l.HandleKey(key("down"))
	}
	if c, _ := l.Selected(); c.Name != "c" {
		t.Fatal("down at bottom must clamp")
	}
}

func TestSelectionFollowsNameAcrossRefresh(t *testing.T) {
	l := newList(cl("a", 1, 1), cl("b", 1, 1), cl("c", 1, 1))
	l.HandleKey(key("down")) // b
	l.SetClusters([]snapshot.Cluster{cl("z", 1, 1), cl("c", 1, 1), cl("b", 1, 1)})
	if c, _ := l.Selected(); c.Name != "b" {
		t.Fatalf("selection jumped to %q", c.Name)
	}
}

func TestSelectedClusterDisappears(t *testing.T) {
	l := newList(cl("a", 1, 1), cl("b", 1, 1), cl("c", 1, 1))
	l.HandleKey(key("down"))
	l.HandleKey(key("down")) // c, last row
	l.SetClusters([]snapshot.Cluster{cl("a", 1, 1), cl("b", 1, 1)})
	if c, ok := l.Selected(); !ok || c.Name != "b" {
		t.Fatalf("should fall to nearest row, got %q ok=%v", c.Name, ok)
	}
	l.SetClusters(nil)
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
	l.SetClusters([]snapshot.Cluster{cl("a", 1, 1)})
	_ = l.View()
}
