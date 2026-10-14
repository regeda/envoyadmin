package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

type match struct {
	idx  int   // index into listPane.all
	hits []int // matched byte offsets in FilterText (as reported by fuzzy)
}

type listPane struct {
	all           []resourceItem
	visible       []match
	cursor        int
	selected      string // the selected item's Key
	query         string
	filtering     bool
	unfocused     bool // zero value = focused
	waiting       bool
	emptyText     string
	notice        string
	input         textinput.Model
	width, height int
	offset        int
}

func newListPane() listPane {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.CharLimit = 100
	return listPane{input: ti, emptyText: "no items"}
}

type itemTexts []resourceItem

func (n itemTexts) String(i int) string { return n[i].FilterText() }
func (n itemTexts) Len() int            { return len(n) }

func (l *listPane) SetSize(w, h int) {
	l.width, l.height = w, h
	l.input.Width = max(w-4, 1)
	l.clampOffset()
}

func (l *listPane) SetWaiting(v bool)     { l.waiting = v }
func (l *listPane) SetEmptyText(s string) { l.emptyText = s }
func (l *listPane) SetNotice(s string)    { l.notice = s }

// SetFocused tells the pane whether it owns the keyboard; an unfocused pane
// draws its cursor as › instead of >.
func (l *listPane) SetFocused(f bool) { l.unfocused = !f }

func (l *listPane) SetItems(items []resourceItem) {
	l.all = items
	l.refilter()
}

func (l *listPane) Selected() (resourceItem, bool) {
	if len(l.visible) == 0 {
		return nil, false
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
		for _, m := range fuzzy.FindFrom(l.query, itemTexts(l.all)) {
			vis = append(vis, match{idx: m.Index, hits: m.MatchedIndexes})
		}
	}
	l.visible = vis
	l.restoreCursor()
}

// restoreCursor keeps the selection on the same Key; if it is gone while other
// rows exist it falls to the nearest row.
func (l *listPane) restoreCursor() {
	for i, m := range l.visible {
		if l.all[m.idx].Key() == l.selected {
			l.cursor = i
			l.clampOffset()
			return
		}
	}
	l.cursor = min(l.cursor, len(l.visible)-1)
	l.cursor = max(l.cursor, 0)
	if len(l.visible) > 0 {
		l.selected = l.all[l.visible[l.cursor].idx].Key()
	}
	// An empty list (a failed dump, a filter matching nothing) keeps `selected`,
	// so the place is restored when items return.
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
	l.offset = min(l.offset, max(len(l.visible)-l.rows(), 0))
	l.offset = max(l.offset, 0)
}

func (l *listPane) move(d int) {
	if len(l.visible) == 0 {
		return
	}
	l.cursor = max(0, min(l.cursor+d, len(l.visible)-1))
	l.selected = l.all[l.visible[l.cursor].idx].Key()
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
	return l.all[m.idx].Row(rowCtx{Width: l.width, Cursor: i == l.cursor, Unfocused: l.unfocused, Hits: m.hits})
}

func (l listPane) View() string {
	lines := []string{l.filterLine()}
	if len(l.visible) == 0 {
		switch {
		case l.waiting:
			lines = append(lines, dimStyle.Render("waiting for Envoy…"))
		case l.notice != "":
			for _, row := range wrapLines(l.notice, max(l.width, 1), max(l.rows(), 1)) {
				lines = append(lines, warnStyle.Render(row))
			}
		case l.query != "":
			lines = append(lines, dimStyle.Render("no matches"))
		default:
			lines = append(lines, dimStyle.Render(l.emptyText))
		}
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
