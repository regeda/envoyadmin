package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// trunc cuts s to at most w display cells.
func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "")
}

// pad right-pads s with spaces to w display cells (never truncates).
func pad(s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", max(int(d.Seconds()), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// wrapLines word-wraps s to w cells and caps it at maxRows rows; when text is
// cut the last row ends with an ellipsis. Existing newlines are kept.
func wrapLines(s string, w, maxRows int) []string {
	if w <= 0 || maxRows <= 0 || s == "" {
		return nil
	}
	rows := strings.Split(ansi.Wrap(s, w, ""), "\n")
	if len(rows) > maxRows {
		rows = rows[:maxRows]
		last := strings.TrimRight(ansi.Truncate(rows[maxRows-1], max(w-1, 1), ""), " ")
		rows[maxRows-1] = last + "…"
	}
	return rows
}
