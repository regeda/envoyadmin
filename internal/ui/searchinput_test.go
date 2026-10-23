package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestSearchInputSpansBothPanes(t *testing.T) {
	c := &clock{now: fixedNow}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), &fakeRoutes{cfgs: edgeConfigs()}, c)
	before := strings.Count(m.View(), "\n")
	m = send(m, key(":"))
	check := func(m Model) {
		t.Helper()
		v := m.View()
		lines := strings.Split(v, "\n")
		if len(lines) != m.height {
			t.Fatalf("frame has %d lines, want %d", len(lines), m.height)
		}
		row := lines[m.headerRows()+m.bannerRows]
		if lipgloss.Width(row) != m.width {
			t.Fatalf("input row width %d, want %d: %q", lipgloss.Width(row), m.width, row)
		}
		if !strings.Contains(row, "a-edge :") {
			t.Fatalf("input row lacks prompt: %q", row)
		}
		for i, l := range lines {
			if lipgloss.Width(l) > m.width {
				t.Fatalf("line %d overflows", i)
			}
		}
		if strings.Contains(row, "│") || !strings.Contains(lines[m.headerRows()+m.bannerRows+1], "│") {
			t.Fatalf("input row is not above both panes:\n%s", v)
		}
		if strings.Contains(lines[m.headerRows()+m.bannerRows+1], "a-edge :") {
			t.Fatal("input shown twice")
		}
	}
	check(m)
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("name logon")})
	check(m)
	if row := strings.Split(m.View(), "\n")[m.headerRows()]; !strings.Contains(row, "name logon") {
		t.Fatalf("typed text missing: %q", row)
	}
	long := ":curl http://h/" + strings.Repeat("x", 300)
	m.search.input.SetValue(long)
	check(m)
	m = send(m, key("esc"))
	if got := strings.Count(m.View(), "\n"); got != before {
		t.Fatalf("after esc %d newlines, before %d", got, before)
	}
	if len(strings.Split(m.View(), "\n")) != m.height {
		t.Fatal("frame height after close")
	}
}

func TestSearchInputRowWithBannerAndResize(t *testing.T) {
	c := &clock{now: fixedNow}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), &fakeRoutes{cfgs: edgeConfigs()}, c)
	m = send(m, key(":"))
	m = send(m, snapshotMsg{err: errors.New("boom")})
	m = sized(m, 100, 24)
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 24 {
		t.Fatalf("lines=%d", len(lines))
	}
	if m.bannerRows == 0 || !strings.Contains(lines[m.headerRows()+m.bannerRows], "a-edge :") {
		t.Fatalf("input not below banner: banner=%d\n%s", m.bannerRows, m.View())
	}
	if lipgloss.Width(lines[m.headerRows()+m.bannerRows]) != 100 {
		t.Fatal("input row not full width")
	}
}
