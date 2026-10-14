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
}

func (d *detailPane) SetTab(t tab) { d.tab = t }

func (d *detailPane) SetCluster(c snapshot.Cluster, ok bool) {
	if !ok {
		d.has, d.cluster = false, snapshot.Cluster{}
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
		d.vp.SetContent(d.cfgOut)
		if nameChanged {
			d.vp.GotoTop()
		}
	}
	if c.Config.Err != "" {
		d.vp.SetContent(dimStyle.Render(c.Config.Err))
	} else {
		d.vp.SetContent(d.cfgOut)
	}
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
	return boldStyle.Render(d.cluster.Name) + "  ·  " +
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
	var body string
	switch {
	case !d.has:
		body = dimStyle.Render("no cluster selected")
	case d.tab == tabConfig:
		body = d.tabLine() + "\n" + d.configHeader() + "\n" + d.vp.View()
	default:
		body = d.tabLine() + "\n" + renderEndpoints(d.cluster, d.width, d.height-1, d.scroll)
	}
	w, h := max(d.width, 1), max(d.height, 1)
	return lipgloss.NewStyle().Width(w).MaxWidth(w).Height(h).MaxHeight(h).Render(body)
}
