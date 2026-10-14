package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/envoyadmin/internal/snapshot"
)

// rowCtx is what a list row needs to draw itself.
type rowCtx struct {
	Width     int
	Cursor    bool
	Unfocused bool
	Hits      []int // fuzzy match offsets (bytes) into FilterText()
}

// resourceItem is one list entry plus everything its details need. The panes
// know nothing else about clusters or listeners.
type resourceItem interface {
	Key() string                      // stable id; keeps the selection across refreshes
	FilterText() string               // text the fuzzy filter matches
	Row(rowCtx) string                // one list row, never wider than Width
	Title() string                    // detail header
	OverviewName() string             // first detail tab: "Endpoints" or "Overview"
	Overview(w, h, scroll int) string // exactly h lines, none wider than w
	OverviewMaxScroll(w, h int) int
	Config() snapshot.ConfigInfo
}

// cursorGlyph is the one-cell row prefix.
func cursorGlyph(c rowCtx) string {
	switch {
	case !c.Cursor:
		return " "
	case c.Unfocused:
		return "›"
	default:
		return ">"
	}
}

// styledText renders text with the fuzzy hits (byte offsets into text)
// underlined and bold; on the cursor row the rest is bold.
func styledText(text string, hits []int, cursor bool) string {
	hit := map[int]bool{}
	for _, h := range hits {
		hit[h] = true
	}
	var b strings.Builder
	for bi, r := range text {
		s := string(r)
		switch {
		case hit[bi]:
			s = lipgloss.NewStyle().Bold(true).Underline(true).Render(s)
		case cursor:
			s = boldStyle.Render(s)
		}
		b.WriteString(s)
	}
	return b.String()
}
