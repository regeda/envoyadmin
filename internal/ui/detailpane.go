package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/snapshot"
)

type tab int

const (
	tabEndpoints tab = iota
	tabConfig
)

type detailPane struct {
	tab           tab
	cluster       snapshot.Cluster
	has           bool
	width, height int
	scroll        int // endpoints tab
	vp            viewport.Model
	color         bool
	focused       bool

	cfgName, cfgYAML, cfgOut string
	highlights               int
}

func newDetailPane(color bool) detailPane {
	return detailPane{color: color, vp: viewport.New(0, 0)}
}

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

func (d *detailPane) SetCluster(c snapshot.Cluster, ok bool) {
	if !ok {
		d.has, d.cluster = false, snapshot.Cluster{}
		d.cfgName = "" // a reappearing cluster is treated as new
		d.vp.GotoTop()
		return
	}
	if !d.has || d.cluster.Name != c.Name {
		d.scroll = 0
	}
	d.cluster, d.has = c, true
	d.scroll = min(d.scroll, maxEndpointScroll(c, d.height-1))
	d.syncConfig()
}

func (d *detailPane) syncConfig() {
	c := d.cluster
	if d.cfgName != c.Name || d.cfgYAML != c.Config.YAML {
		nameChanged := d.cfgName != c.Name
		d.cfgOut = highlightYAML(c.Config.YAML, d.color)
		d.cfgName, d.cfgYAML = c.Name, c.Config.YAML
		d.highlights++
		d.applyContent()
		if nameChanged {
			d.vp.GotoTop()
		}
		return
	}
	d.applyContent()
}

// clipLines cuts every line to w cells so each logical line is one rendered row.
func clipLines(s string, w int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = trunc(l, w)
	}
	return strings.Join(lines, "\n")
}

// applyContent feeds the viewport the (cached) highlighted YAML or the error,
// clipped to the current width.
func (d *detailPane) applyContent() {
	w := max(d.width, 1)
	if d.cluster.Config.Err != "" {
		d.vp.SetContent(clipLines(dimStyle.Render(d.cluster.Config.Err), w))
		return
	}
	d.vp.SetContent(clipLines(d.cfgOut, w))
}

func (d *detailPane) HandleKey(k tea.KeyMsg) {
	if !d.has {
		return
	}
	if d.tab == tabConfig {
		d.vp, _ = d.vp.Update(k)
		return
	}
	maxS := maxEndpointScroll(d.cluster, d.height-1)
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
	return marker + boldStyle.Render(d.cluster.Name) + "  ·  " +
		label("Endpoints", d.tab == tabEndpoints) + " " + dimStyle.Render("|") + " " + label("Config", d.tab == tabConfig)
}

func (d detailPane) configHeader() string {
	c := d.cluster.Config
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
		body = dimStyle.Render("no cluster selected")
	case d.tab == tabConfig:
		body = trunc(d.tabLine(), w) + "\n" + trunc(d.configHeader(), w) + "\n" + d.vp.View()
	default:
		body = trunc(d.tabLine(), w) + "\n" + renderEndpoints(d.cluster, d.width, d.height-1, d.scroll)
	}
	// No Width(): it would wrap. Every line is already clipped to w.
	return lipgloss.NewStyle().MaxWidth(w).Height(h).MaxHeight(h).Render(body)
}
