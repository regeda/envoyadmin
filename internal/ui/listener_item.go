package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/regeda/envoyadmin/internal/snapshot"
)

const (
	overviewLabelW = 14 // "Bind address"/"last_updated" (12) + 2 spaces
	addrMaxW       = 21 // widest address cell in a row
	nameMinW       = 8
	errorMaxRows   = 4
)

type listenerItem struct{ l snapshot.Listener }

func listenerItems(ls []snapshot.Listener) []resourceItem {
	out := make([]resourceItem, len(ls))
	for i, l := range ls {
		out[i] = listenerItem{l}
	}
	return out
}

func (it listenerItem) Key() string                 { return it.l.Name }
func (it listenerItem) FilterText() string          { return it.l.Name + " " + it.l.Address }
func (it listenerItem) Title() string               { return it.l.Name }
func (it listenerItem) OverviewName() string        { return "Overview" }
func (it listenerItem) Config() snapshot.ConfigInfo { return it.l.Config }

// stateGlyph carries the state without relying on colour.
func stateGlyph(l snapshot.Listener) (string, lipgloss.Style) {
	switch l.State {
	case snapshot.ListenerError:
		return "✗", badStyle
	case snapshot.ListenerWarming, snapshot.ListenerDraining:
		return "◐", warnStyle
	default:
		return "●", okStyle
	}
}

func (it listenerItem) Row(ctx rowCtx) string {
	l := it.l
	glyph, style := stateGlyph(l)
	avail := max(ctx.Width-3, 0) // prefix, glyph, space
	addrW := max(min(lipgloss.Width(l.Address), addrMaxW, avail-nameMinW-1), 0)
	nameW := avail - addrW
	if addrW > 0 {
		nameW-- // gap
	}
	nameW = max(nameW, 0)

	// FilterText is "name address": hits past the name belong to the address.
	var nameHits, addrHits []int
	for _, h := range ctx.Hits {
		switch {
		case h < len(l.Name):
			nameHits = append(nameHits, h)
		case h > len(l.Name):
			addrHits = append(addrHits, h-len(l.Name)-1)
		}
	}
	plainName := trunc(l.Name, nameW)
	name := styledText(plainName, nameHits, ctx.Cursor) + strings.Repeat(" ", max(nameW-lipgloss.Width(plainName), 0))

	line := cursorGlyph(ctx) + style.Render(glyph) + " " + name
	if addrW > 0 {
		line += " " + styledText(trunc(l.Address, addrW), addrHits, false)
	}
	return trunc(line, ctx.Width) // tiny panes: never wider than the pane
}

func prop(label, value string) string {
	return dimStyle.Render(pad(label, overviewLabelW)) + value
}

// sticky returns the rows that never scroll: the four properties, the error
// block (error state only) and the chain heading.
func (it listenerItem) sticky(w int) []string {
	l := it.l
	_, stStyle := stateGlyph(l)
	lines := []string{
		prop("Bind address", orDash(l.Address)),
		prop("State", stStyle.Render(l.StateText())),
		prop("Version", orDash(l.VersionInfo)),
	}
	updated := "-"
	if !l.LastUpdated.IsZero() {
		updated = l.LastUpdated.UTC().Format("2006-01-02 15:04:05Z")
	}
	lines = append(lines, prop("last_updated", updated))

	if l.State == snapshot.ListenerError {
		valW := max(w-overviewLabelW, 1)
		rows := wrapLines(sanitizeBannerText(l.ErrorDetails), valW, errorMaxRows)
		for i, r := range rows {
			r = strings.TrimRight(r, " ")
			label := ""
			if i == 0 {
				label = "Error"
			}
			lines = append(lines, dimStyle.Render(pad(label, overviewLabelW))+badStyle.Render(r))
		}
		if l.ServingOld {
			// wrapped, never capped or ellipsised: this sentence must stay readable
			for _, r := range strings.Split(ansi.Wrap("still serving the previous configuration", valW, ""), "\n") {
				lines = append(lines, dimStyle.Render(pad("", overviewLabelW)+strings.TrimRight(r, " ")))
			}
		}
	}
	return append(lines, "", boldStyle.Render("Filter chains ("+strconv.Itoa(len(l.Chains))+")"))
}

// filterRow is a filter's full name followed by its type in the shadow. The
// type is dropped, not cut, when the pane is too narrow to show both.
func filterRow(prefix string, f snapshot.Filter, w int) string {
	row := prefix + f.Name
	if f.Type != "" && lipgloss.Width(row)+2+lipgloss.Width(f.Type) <= w {
		row += "  " + dimStyle.Render(f.Type)
	}
	return row
}

// chainLines is a row for the label and match of every chain, one row per
// network filter (full name, then its type in the shadow), a TLS row, and the
// HTTP filters of the chain's HttpConnectionManager drawn as a tree below them.
// The label column is as wide as the widest label, but at most a third of the
// pane (never under overviewLabelW), so long Istio-style names stay
// distinguishable; a cut label always ends with "…".
func (it listenerItem) chainLines(w int) []string {
	var out []string
	labelW := 0
	for _, c := range it.l.Chains {
		labelW = max(labelW, lipgloss.Width(c.Label))
	}
	labelW = min(labelW, max(w/3, overviewLabelW))
	indent := " " + strings.Repeat(" ", labelW) + "  "
	for _, c := range it.l.Chains {
		match := "any"
		if len(c.Match) > 0 {
			match = strings.Join(c.Match, " · ")
		}
		out = append(out, " "+pad(truncEllipsis(c.Label, labelW), labelW)+"  "+match)
		for _, f := range c.Filters {
			out = append(out, filterRow(indent, f, w))
		}
		if c.TLS {
			out = append(out, indent+dimStyle.Render("TLS"))
		}
		for i, f := range c.HTTPFilters {
			glyph := "├"
			if i == len(c.HTTPFilters)-1 {
				glyph = "└"
			}
			out = append(out, filterRow(indent+"  "+dimStyle.Render(glyph)+" ", f, w))
		}
	}
	return out
}

func (it listenerItem) OverviewMaxScroll(w, h int) int {
	rows := h - len(it.sticky(w))
	if rows <= 0 {
		return 0
	}
	return max(len(it.chainLines(w))-rows, 0)
}

func (it listenerItem) Overview(w, h, scroll int) string {
	if h <= 0 {
		return ""
	}
	if w <= 0 {
		return strings.Repeat("\n", h-1)
	}
	sticky := it.sticky(w)
	var lines []string
	if h <= len(sticky) {
		lines = sticky[:h]
	} else {
		lines = append(lines, sticky...)
		chains := it.chainLines(w)
		rows := h - len(sticky)
		start := max(0, min(scroll, max(len(chains)-rows, 0)))
		lines = append(lines, chains[start:min(start+rows, len(chains))]...)
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = truncEllipsis(l, w)
	}
	return strings.Join(lines, "\n")
}
