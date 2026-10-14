package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tab int

const (
	tabOverview tab = iota
	tabConfig
)

type detailPane struct {
	tab           tab
	item          resourceItem
	has           bool
	emptyText     string
	width, height int
	scroll        int // overview tab
	vp            viewport.Model
	color         bool
	focused       bool

	cfgKey, cfgText, cfgOut string
	highlights              int
}

func newDetailPane(color bool) detailPane {
	return detailPane{color: color, vp: viewport.New(0, 0), emptyText: "nothing selected"}
}

func (d *detailPane) SetEmptyText(s string) { d.emptyText = s }

// Layout: 1 tab line, then a body of height-1 rows. The Config body is 1 header
// line plus the viewport.
func (d *detailPane) SetSize(w, h int) {
	d.width, d.height = w, h
	d.vp.Width = max(w, 1)
	d.vp.Height = max(h-2, 1)
	d.applyContent() // re-clip to the new width; never re-runs chroma
}

func (d *detailPane) SetTab(t tab) { d.tab = t }

// SetFocused marks the pane as owning the keyboard; its tab line then starts
// with a ▌ marker (a blank of the same width otherwise, so nothing shifts).
func (d *detailPane) SetFocused(f bool) { d.focused = f }

func (d *detailPane) SetItem(it resourceItem, ok bool) {
	if !ok {
		d.has, d.item = false, nil
		d.cfgKey = "" // a reappearing item is treated as new
		d.vp.GotoTop()
		return
	}
	if !d.has || d.item.Key() != it.Key() {
		d.scroll = 0
	}
	d.item, d.has = it, true
	d.scroll = min(d.scroll, d.item.OverviewMaxScroll(d.width, d.height-1))
	d.syncConfig()
}

func (d *detailPane) syncConfig() {
	text, _ := d.item.Config().Render()
	key := d.item.Key()
	if d.cfgKey != key || d.cfgText != text {
		keyChanged := d.cfgKey != key
		d.cfgOut = highlightYAML(text, d.color)
		d.cfgKey, d.cfgText = key, text
		d.highlights++
		d.applyContent()
		if keyChanged {
			d.vp.GotoTop()
		}
		return
	}
	d.applyContent()
}

// numberLines prefixes every line with a dark-grey, right-aligned line number
// and a gutter bar (no space before the bar; the column is as wide as the
// largest number). The numbers follow the YAML lines (one rendered row per line),
// so they stay correct while the viewport scrolls. Empty content gets none.
func numberLines(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	digits := len(strconv.Itoa(len(lines)))
	for i, l := range lines {
		lines[i] = lineNumStyle.Render(fmt.Sprintf("%*d│", digits, i+1)) + " " + l
	}
	return strings.Join(lines, "\n")
}

// clipLines cuts every line to w cells so each logical line is one rendered row.
func clipLines(s string, w int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = trunc(l, w)
	}
	return strings.Join(lines, "\n")
}

// applyContent feeds the viewport the (cached) highlighted YAML or the problem,
// clipped to the current width.
func (d *detailPane) applyContent() {
	if !d.has {
		return
	}
	w := max(d.width, 1)
	if _, problem := d.item.Config().Render(); problem != "" {
		d.vp.SetContent(clipLines(dimStyle.Render(problem), w))
		return
	}
	d.vp.SetContent(clipLines(numberLines(d.cfgOut), w))
}

func (d *detailPane) HandleKey(k tea.KeyMsg) {
	if !d.has {
		return
	}
	if d.tab == tabConfig {
		d.vp, _ = d.vp.Update(k)
		return
	}
	maxS := d.item.OverviewMaxScroll(d.width, d.height-1)
	switch k.String() {
	case "up", "k":
		d.scroll--
	case "down", "j":
		d.scroll++
	case "pgup":
		d.scroll -= 10
	case "pgdown":
		d.scroll += 10
	case "home", "g":
		d.scroll = 0
	case "end", "G":
		d.scroll = maxS
	}
	d.scroll = max(0, min(d.scroll, maxS))
}

func (d detailPane) tabLine() string {
	label := func(name string, active bool) string {
		if active {
			return lipgloss.NewStyle().Bold(true).Underline(true).Render(name)
		}
		return dimStyle.Render(name)
	}
	marker := "  "
	if d.focused {
		marker = logoStyle.Render("▌") + " "
	}
	return marker + boldStyle.Render(d.item.Title()) + "  ·  " +
		label(d.item.OverviewName(), d.tab == tabOverview) + " " + dimStyle.Render("|") + " " + label("Config", d.tab == tabConfig)
}

func (d detailPane) configHeader() string {
	c := d.item.Config()
	var parts []string
	if c.Static {
		parts = append(parts, "static (no xDS version)")
	} else if c.VersionInfo != "" {
		parts = append(parts, "version_info: "+c.VersionInfo)
	}
	if !c.LastUpdated.IsZero() {
		parts = append(parts, "last_updated: "+c.LastUpdated.UTC().Format("2006-01-02 15:04:05Z"))
	}
	return dimStyle.Render(strings.Join(parts, "   "))
}

func (d detailPane) View() string {
	w, h := max(d.width, 1), max(d.height, 1)
	var body string
	switch {
	case !d.has:
		body = dimStyle.Render(d.emptyText)
	case d.tab == tabConfig:
		body = trunc(d.tabLine(), w) + "\n" + trunc(d.configHeader(), w) + "\n" + d.vp.View()
	default:
		body = trunc(d.tabLine(), w) + "\n" + d.item.Overview(d.width, d.height-1, d.scroll)
	}
	// No Width(): it would wrap. Every line is already clipped to w.
	return lipgloss.NewStyle().MaxWidth(w).Height(h).MaxHeight(h).Render(body)
}
