package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/regeda/envoyadmin/internal/snapshot"
)

func testHit(name string, idx int) snapshot.RouteHit {
	return snapshot.RouteHit{
		Config:      "cfg-主要",
		VirtualHost: "vh",
		Domains:     []string{"a.example.com", "*"},
		Route:       snapshot.Route{Name: name, Index: idx, Config: snapshot.ConfigInfo{YAML: "name: r\n"}},
	}
}

func TestRouteHitItem(t *testing.T) {
	win := testHit("日本語のルート-very-long-name", 0)
	win.Winner = true
	for _, h := range []snapshot.RouteHit{win, testHit("plain", 1)} {
		it := routeHitItem{h}
		for _, w := range []int{10, 28, 80} {
			for _, cur := range []bool{false, true} {
				row := it.Row(rowCtx{Width: w, Cursor: cur})
				if lipgloss.Width(row) > w {
					t.Errorf("w=%d row width %d: %q", w, lipgloss.Width(row), row)
				}
			}
			ov := it.Overview(w, 8, 0)
			lines := strings.Split(ov, "\n")
			if len(lines) != 8 {
				t.Errorf("overview lines = %d", len(lines))
			}
			for _, l := range lines {
				if lipgloss.Width(l) > w {
					t.Errorf("overview w=%d line width %d", w, lipgloss.Width(l))
				}
			}
		}
	}

	if row := (routeHitItem{testHit("", 3)}).Row(rowCtx{Width: 80}); !strings.Contains(row, "#3") {
		t.Errorf("nameless row = %q", row)
	}
	if row := (routeHitItem{win}).Row(rowCtx{Width: 80}); !strings.HasPrefix(strings.TrimLeft(row, " >"), "★") {
		t.Errorf("winner row = %q", row)
	}
	tent := testHit("t", 0)
	tent.Winner, tent.Tentative = true, true
	if row := (routeHitItem{tent}).Row(rowCtx{Width: 80}); !strings.Contains(row, "★?") {
		t.Errorf("tentative row = %q", row)
	}
	ne := testHit("n", 0)
	ne.NotEvaluated = true
	if row := (routeHitItem{ne}).Row(rowCtx{Width: 80}); !strings.Contains(row, "?") {
		t.Errorf("not evaluated row = %q", row)
	}

	it := routeHitItem{win}
	if it.Key() != "cfg-主要|vh|0" || it.OverviewName() != "Route" || it.FilterText() != win.Route.Name {
		t.Errorf("key/name/filter = %q %q %q", it.Key(), it.OverviewName(), it.FilterText())
	}
	if text, _ := it.Config().Render(); text != "name: r\n" {
		t.Errorf("config = %q", text)
	}
	if got := routeHitItems([]snapshot.RouteHit{win}); len(got) != 1 {
		t.Errorf("routeHitItems = %d", len(got))
	}
}
