package ui

import (
	"strings"
	"testing"

	"github.com/regeda/txenvoy/internal/snapshot"
)

func focusModel() Model {
	m := sized(New(testOpts(nil)), 120, 30)
	return send(m, snapshotMsg{snap: snap(cl("alpha", 1, 2), cl("beta", 1, 1))})
}

// selectedRow returns the list row that carries the cursor glyph (> or ›).
func selectedRow(t *testing.T, v string) string {
	t.Helper()
	for _, l := range strings.Split(v, "\n") {
		if strings.HasPrefix(l, ">") || strings.HasPrefix(l, "›") {
			return l
		}
	}
	t.Fatalf("no selected row in:\n%s", v)
	return ""
}

func detailTabLine(t *testing.T, v string) string {
	t.Helper()
	for _, l := range strings.Split(v, "\n") {
		if i := strings.Index(l, "│"); i >= 0 && strings.Contains(l, "Endpoints | Config") {
			return l[i+len("│"):]
		}
	}
	t.Fatalf("no detail tab line in:\n%s", v)
	return ""
}

func TestEnterShowsWhichPaneHasFocus(t *testing.T) {
	m := focusModel()
	v := m.View()
	if strings.Contains(v, "focus: detail") || strings.Contains(detailTabLine(t, v), "▌") {
		t.Fatalf("list starts focused, nothing should say detail:\n%s", v)
	}
	if !strings.HasPrefix(selectedRow(t, v), ">") {
		t.Fatalf("focused list uses the > cursor: %q", selectedRow(t, v))
	}

	m = send(m, key("enter"))
	v = m.View()
	if !strings.Contains(v, "focus: detail") {
		t.Fatalf("status bar must say the detail pane has focus:\n%s", v)
	}
	if !strings.HasPrefix(strings.TrimLeft(detailTabLine(t, v), " "), "▌") {
		t.Fatalf("detail tab line must carry the focus marker: %q", detailTabLine(t, v))
	}
	if !strings.HasPrefix(selectedRow(t, v), "›") {
		t.Fatalf("unfocused list uses the › cursor: %q", selectedRow(t, v))
	}
	if !strings.Contains(v, "esc back") {
		t.Fatalf("status bar must say how to get back:\n%s", v)
	}
}

func TestEscReturnsFocusToList(t *testing.T) {
	for _, back := range []string{"esc"} {
		m := send(focusModel(), key("enter"))
		if m.focus != focusDetail {
			t.Fatal("enter should move focus to the detail pane")
		}
		m = send(m, key(back))
		if m.focus != focusList {
			t.Fatalf("%s must return focus to the list", back)
		}
		v := m.View()
		if strings.Contains(v, "focus: detail") || !strings.HasPrefix(selectedRow(t, v), ">") {
			t.Fatalf("%s: indicators must revert:\n%s", back, v)
		}
	}
}

func TestSlashFromDetailFocusesListFilter(t *testing.T) {
	m := send(focusModel(), key("enter"))
	m = send(m, key("/"))
	if m.focus != focusList || !m.cur().list.filtering {
		t.Fatalf("/ must hand focus to the list filter (focus=%v filtering=%v)", m.focus, m.cur().list.filtering)
	}
	if strings.Contains(m.View(), "focus: detail") {
		t.Fatal("indicator must clear when / moves focus to the list")
	}
}

func TestEscInListStillClearsFilter(t *testing.T) {
	m := focusModel()
	m = typeText(send(m, key("/")), "alp")
	m = send(m, key("enter")) // keep the filter, leave filter mode
	if len(m.cur().list.visible) != 1 {
		t.Fatalf("filter should narrow to 1, got %d", len(m.cur().list.visible))
	}
	m = send(m, key("esc"))
	if len(m.cur().list.visible) != 2 || m.focus != focusList {
		t.Fatalf("esc in the list clears the filter (visible=%d focus=%v)", len(m.cur().list.visible), m.focus)
	}
}

// In detail focus j/k scroll the table, not the cluster selection: that is
// intended, which is exactly why the focus has to be visible.
func TestDetailFocusKeysDoNotMoveTheClusterSelection(t *testing.T) {
	m := send(focusModel(), key("enter"))
	before, _ := m.cur().list.Selected()
	m = send(m, key("j"))
	after, _ := m.cur().list.Selected()
	if before.Key() != after.Key() {
		t.Fatalf("j in detail focus moved the selection %q -> %q", before.Key(), after.Key())
	}
	m = send(m, key("esc"))
	m = send(m, key("j"))
	if it, _ := m.cur().list.Selected(); it.Key() == before.Key() {
		t.Fatal("j in list focus must move the selection")
	}
}

func TestFocusIndicatorKeepsFrameExact(t *testing.T) {
	for _, sz := range [][2]int{{120, 30}, {80, 24}, {100, 20}, {80, 12}} {
		m := sized(New(testOpts(nil)), sz[0], sz[1])
		m = send(send(m, snapshotMsg{snap: snap(cl("alpha", 1, 2))}), key("enter"))
		lines := strings.Split(m.View(), "\n")
		if len(lines) != sz[1] {
			t.Fatalf("%v: %d lines", sz, len(lines))
		}
	}
	_ = snapshot.Healthy
}

// The reason Enter focuses the details: with the details focused the arrow keys scroll the
// Config YAML, while the cluster selection stays put.
func TestEnterThenArrowsScrollTheConfig(t *testing.T) {
	c := cl("alpha", 1, 2)
	c.Config.YAML = strings.Repeat("key: value\n", 80) + "last: line\n"
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snap(c, cl("beta", 1, 1))})
	m = send(m, key("c"))
	if strings.Contains(m.View(), "last: line") {
		t.Fatal("precondition: the end of the YAML is off-screen")
	}

	m = send(m, key("down")) // list still focused: moves the selection
	if selectedName(m) != "beta" {
		t.Fatalf("before enter, down selects the next cluster, got %q", selectedName(m))
	}
	m = send(m, key("up"))

	m = send(m, key("enter"))
	for i := 0; i < 100; i++ {
		m = send(m, key("down"))
	}
	if selectedName(m) != "alpha" {
		t.Fatalf("after enter the arrows must not move the selection, got %q", selectedName(m))
	}
	if !strings.Contains(m.View(), "last: line") {
		t.Fatalf("after enter the arrows must scroll the config to its end:\n%s", m.View())
	}
}

func selectedName(m Model) string {
	it, _ := m.cur().list.Selected()
	return it.Key()
}
