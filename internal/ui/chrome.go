package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	logoRow1 = "▀█▀ ▀▄▀ █▀▀ █▄ █ █ █ █▀█ █ █"
	logoRow2 = " █  █ █ ██▄ █ ▀█ ▀▄▀ █▄█  █"
)

var bannerStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("1")).
	Foreground(lipgloss.Color("15")).
	Bold(true)

// joinEnds puts left at the left edge and right at the right edge of one line.
// The result is never wider than width: left is kept (clipped only if it alone
// overflows) and right is clipped or dropped to whatever room remains.
func joinEnds(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	left = trunc(left, width)
	lw := lipgloss.Width(left)
	avail := width - lw - 1
	if avail <= 0 {
		return left
	}
	right = trunc(right, avail)
	gap := max(width-lw-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

func renderHeader(width int, compact bool, conn1, conn2, tab string) string {
	tabLine := trunc(" "+lipgloss.NewStyle().Bold(true).Reverse(true).Render(" "+tab+" "), width)
	if compact {
		return joinEnds(logoStyle.Render("txenvoy"), conn1, width) + "\n" + tabLine
	}
	return strings.Join([]string{
		joinEnds(logoStyle.Render(logoRow1), conn1, width),
		joinEnds(logoStyle.Render(logoRow2), conn2, width),
		"",
		tabLine,
	}, "\n")
}

// bannerLines wraps text to width, caps it at maxRows (ending with an
// ellipsis when cut) and paints each row exactly width cells wide on a red
// background. Rows are clipped explicitly; lipgloss Width() is not used since
// it wraps rather than clips.
func bannerLines(text string, width, maxRows int) []string {
	if width <= 0 || maxRows <= 0 {
		return nil
	}
	inner := max(width-2, 1)
	rows := strings.Split(ansi.Wrap(text, inner, ""), "\n")
	if len(rows) > maxRows {
		rows = rows[:maxRows]
		last := strings.TrimRight(trunc(rows[maxRows-1], max(inner-1, 1)), " ")
		rows[maxRows-1] = last + "…"
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = bannerStyle.Render(pad(trunc(" "+r, width), width))
	}
	return out
}

// overlay paints banner over the first len(banner) lines of frame. The frame
// was laid out as if the banner did not exist, so nothing below it moves.
func overlay(frame string, banner []string) string {
	if len(banner) == 0 {
		return frame
	}
	lines := strings.Split(frame, "\n")
	for i, b := range banner {
		if i < len(lines) {
			lines[i] = b
		} else {
			lines = append(lines, b)
		}
	}
	return strings.Join(lines, "\n")
}

func renderStatus(width int, left, right string) string {
	return dimStyle.Render(joinEnds(left, right, width))
}
