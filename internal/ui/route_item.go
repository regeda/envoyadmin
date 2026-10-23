package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/envoyadmin/internal/snapshot"
)

const routeGlyphW = 2 // "★?" is the widest glyph

type routeHitItem struct{ h snapshot.RouteHit }

func routeHitItems(hs []snapshot.RouteHit) []resourceItem {
	out := make([]resourceItem, len(hs))
	for i, h := range hs {
		out[i] = routeHitItem{h}
	}
	return out
}

func (it routeHitItem) Key() string {
	return it.h.Config + "|" + it.h.VirtualHost + "|" + strconv.Itoa(it.h.Route.Index)
}
func (it routeHitItem) FilterText() string          { return it.h.Route.Name }
func (it routeHitItem) OverviewName() string        { return "Route" }
func (it routeHitItem) Config() snapshot.ConfigInfo { return it.h.Route.Config }

// routeName is the route's name, or its position when it has none.
func (it routeHitItem) routeName() string {
	if it.h.Route.Name == "" {
		return "#" + strconv.Itoa(it.h.Route.Index)
	}
	return it.h.Route.Name
}

func (it routeHitItem) Title() string { return it.routeName() }

func (it routeHitItem) glyph() (string, lipgloss.Style) {
	switch {
	case it.h.NotEvaluated:
		return "?", warnStyle
	case it.h.Winner && it.h.Tentative:
		return "★?", warnStyle
	case it.h.Winner:
		return "★", okStyle
	default:
		return "·", dimStyle
	}
}

func (it routeHitItem) Row(ctx rowCtx) string {
	h := it.h
	g, style := it.glyph()
	where := h.Config + " / " + h.VirtualHost
	avail := max(ctx.Width-1-routeGlyphW-1, 0) // prefix, glyph, space
	name := it.routeName()
	whereW := max(min(lipgloss.Width(where), avail-nameMinW-1), 0)
	nameW := avail - whereW
	if whereW > 0 {
		nameW--
	}
	nameW = max(nameW, 0)

	var hits []int
	if h.Route.Name != "" {
		hits = ctx.Hits
	}
	plain := trunc(name, nameW)
	line := cursorGlyph(ctx) + style.Render(pad(g, routeGlyphW)) + " " +
		styledText(plain, hits, ctx.Cursor) + strings.Repeat(" ", max(nameW-lipgloss.Width(plain), 0))
	if whereW > 0 {
		line += " " + dimStyle.Render(trunc(where, whereW))
	}
	return trunc(line, ctx.Width)
}

func (it routeHitItem) lines() []string {
	h := it.h
	lines := []string{
		prop("Route config", orDash(h.Config)),
		prop("Virtual host", orDash(h.VirtualHost)),
		prop("Domains", orDash(strings.Join(h.Domains, ", "))),
		prop("Index", strconv.Itoa(h.Route.Index)),
		prop("Status", h.Status()),
	}
	if h.AnyHost {
		lines = append(lines, prop("Host", "any")) // hostless :curl: no winner
	}
	return lines
}

func (it routeHitItem) OverviewMaxScroll(w, h int) int { return 0 }

func (it routeHitItem) Overview(w, h, scroll int) string {
	if h <= 0 {
		return ""
	}
	if w <= 0 {
		return strings.Repeat("\n", h-1)
	}
	lines := it.lines()
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = truncEllipsis(l, w)
	}
	return strings.Join(lines, "\n")
}
