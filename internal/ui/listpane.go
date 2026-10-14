package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/regeda/txenvoy/internal/snapshot"
)

const (
	typeW  = 12
	countW = 7
	fixedW = 3 + 1 + typeW + 1 + countW // prefix, gap, type, gap, count
)

type match struct {
	idx  int   // index into listPane.all
	hits []int // matched rune positions in the name
}

type listPane struct {
	all           []snapshot.Cluster
	visible       []match
	cursor        int
	selected      string
	query         string
	filtering     bool
	waiting       bool
	input         textinput.Model
	width, height int
	offset        int
}

func newListPane() listPane {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.CharLimit = 100
	return listPane{input: ti}
}

type clusterNames []snapshot.Cluster

func (n clusterNames) String(i int) string { return n[i].Name }
func (n clusterNames) Len() int            { return len(n) }

func (l *listPane) SetSize(w, h int) {
	l.width, l.height = w, h
	l.input.Width = max(w-4, 1)
	l.clampOffset()
}

func (l *listPane) SetWaiting(v bool) { l.waiting = v }

func (l *listPane) SetClusters(cs []snapshot.Cluster) {
	l.all = cs
	l.refilter()
}

func (l *listPane) Selected() (snapshot.Cluster, bool) {
	if len(l.visible) == 0 {
		return snapshot.Cluster{}, false
	}
	return l.all[l.visible[l.cursor].idx], true
}

func (l *listPane) refilter() {
	vis := make([]match, 0, len(l.all))
	if l.query == "" {
		for i := range l.all {
			vis = append(vis, match{idx: i})
		}
	} else {
		for _, m := range fuzzy.FindFrom(l.query, clusterNames(l.all)) {
			vis = append(vis, match{idx: m.Index, hits: m.MatchedIndexes})
		}
	}
	l.visible = vis
	l.restoreCursor()
}

// restoreCursor keeps the selection on the same cluster name; if it is gone
// it falls to the nearest row.
func (l *listPane) restoreCursor() {
	for i, m := range l.visible {
		if l.all[m.idx].Name == l.selected {
			l.cursor = i
			l.clampOffset()
			return
		}
	}
	l.cursor = min(l.cursor, len(l.visible)-1)
	l.cursor = max(l.cursor, 0)
	l.selected = ""
	if len(l.visible) > 0 {
		l.selected = l.all[l.visible[l.cursor].idx].Name
	}
	l.clampOffset()
}

func (l *listPane) rows() int { return max(l.height-1, 1) }

func (l *listPane) clampOffset() {
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+l.rows() {
		l.offset = l.cursor - l.rows() + 1
	}
	l.offset = max(l.offset, 0)
}

func (l *listPane) move(d int) {
	if len(l.visible) == 0 {
		return
	}
	l.cursor = max(0, min(l.cursor+d, len(l.visible)-1))
	l.selected = l.all[l.visible[l.cursor].idx].Name
	l.clampOffset()
}

func (l *listPane) clearFilter() {
	l.filtering = false
	l.input.Blur()
	l.input.SetValue("")
	l.query = ""
	l.refilter()
}

func (l *listPane) HandleKey(k tea.KeyMsg) {
	if l.filtering {
		switch k.String() {
		case "enter":
			l.filtering = false
			l.input.Blur()
		case "esc":
			l.clearFilter()
		case "up", "ctrl+p":
			l.move(-1)
		case "down", "ctrl+n":
			l.move(1)
		default:
			l.input, _ = l.input.Update(k)
			l.query = l.input.Value()
			l.refilter()
		}
		return
	}
	switch k.String() {
	case "up", "k":
		l.move(-1)
	case "down", "j":
		l.move(1)
	case "pgup":
		l.move(-l.rows())
	case "pgdown":
		l.move(l.rows())
	case "home", "g":
		l.move(-len(l.visible))
	case "end", "G":
		l.move(len(l.visible))
	case "/":
		l.filtering = true
		l.input.Focus()
	case "esc":
		if l.query != "" {
			l.clearFilter()
		}
	}
}

func (l listPane) filterLine() string {
	if l.filtering {
		return l.input.View()
	}
	if l.query != "" {
		return dimStyle.Render("/ " + l.query)
	}
	return dimStyle.Render("/ to filter")
}

func (l listPane) row(i int) string {
	m := l.visible[i]
	c := l.all[m.idx]
	nameW := max(l.width-fixedW, 8)

	plain := trunc(c.Name, nameW)
	hit := map[int]bool{}
	for _, h := range m.hits {
		hit[h] = true
	}
	var name strings.Builder
	for ri, r := range []rune(plain) {
		s := string(r)
		switch {
		case hit[ri]:
			s = lipgloss.NewStyle().Bold(true).Underline(true).Render(s)
		case i == l.cursor:
			s = boldStyle.Render(s)
		}
		name.WriteString(s)
	}
	namePad := strings.Repeat(" ", max(nameW-lipgloss.Width(plain), 0))

	prefix := " "
	if i == l.cursor {
		prefix = ">"
	}
	count := fmt.Sprintf("%d/%d", c.HealthyCount(), len(c.Hosts))
	countCell := strings.Repeat(" ", max(countW-lipgloss.Width(count), 0)) + count
	typeCell := pad(trunc(c.Type, typeW), typeW)

	line := prefix + markerStyle(c.Health()).Render("●") + " " + name.String() + namePad + " " + typeCell + " " + countCell
	if c.Health() == snapshot.Empty {
		line = prefix + markerStyle(c.Health()).Render("●") + " " + name.String() + namePad + " " + dimStyle.Render(typeCell+" "+countCell)
	}
	return line
}

func (l listPane) View() string {
	lines := []string{l.filterLine()}
	if len(l.visible) == 0 {
		msg := "no clusters"
		switch {
		case l.waiting:
			msg = "waiting for Envoy…"
		case l.query != "":
			msg = "no matches"
		}
		lines = append(lines, dimStyle.Render(msg))
	}
	end := min(l.offset+l.rows(), len(l.visible))
	for i := l.offset; i < end; i++ {
		lines = append(lines, l.row(i))
	}
	for len(lines) < l.height {
		lines = append(lines, "")
	}
	return lipgloss.NewStyle().Width(max(l.width, 1)).MaxWidth(max(l.width, 1)).
		Height(max(l.height, 1)).MaxHeight(max(l.height, 1)).
		Render(strings.Join(lines, "\n"))
}
