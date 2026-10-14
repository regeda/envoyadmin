package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/snapshot"
)

const (
	typeW  = 12
	countW = 7
	fixedW = 3 + 1 + typeW + 1 + countW // prefix, gap, type, gap, count
)

type clusterItem struct{ c snapshot.Cluster }

func clusterItems(cs []snapshot.Cluster) []resourceItem {
	out := make([]resourceItem, len(cs))
	for i, c := range cs {
		out[i] = clusterItem{c}
	}
	return out
}

func (it clusterItem) Key() string          { return it.c.Name }
func (it clusterItem) FilterText() string   { return it.c.Name }
func (it clusterItem) Title() string        { return it.c.Name }
func (it clusterItem) OverviewName() string { return "Endpoints" }
func (it clusterItem) Overview(w, h, scroll int) string {
	return renderEndpoints(it.c, w, h, scroll)
}
func (it clusterItem) OverviewMaxScroll(_, h int) int { return maxEndpointScroll(it.c, h) }
func (it clusterItem) Config() snapshot.ConfigInfo    { return it.c.Config }

func (it clusterItem) Row(ctx rowCtx) string {
	c := it.c
	// In the narrowest pane the type column gives way so a row never wraps.
	tw := max(min(typeW, ctx.Width-(fixedW-typeW)-8), 1)
	nameW := max(ctx.Width-(fixedW-typeW+tw), 8)

	plain := trunc(c.Name, nameW)
	name := styledText(plain, ctx.Hits, ctx.Cursor)
	namePad := strings.Repeat(" ", max(nameW-lipgloss.Width(plain), 0))

	count := fmt.Sprintf("%d/%d", c.HealthyCount(), len(c.Hosts))
	countCell := strings.Repeat(" ", max(countW-lipgloss.Width(count), 0)) + count
	typeCell := pad(trunc(c.Type, tw), tw)

	prefix := cursorGlyph(ctx) + markerStyle(c.Health()).Render("●") + " "
	if c.Health() == snapshot.Empty {
		return prefix + name + namePad + " " + dimStyle.Render(typeCell+" "+countCell)
	}
	return prefix + name + namePad + " " + typeCell + " " + countCell
}
