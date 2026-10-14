package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestHeaderNormal(t *testing.T) {
	h := renderHeader(100, false, "● connected  http://x", "Envoy 1.31.0 · updated 2s ago", "Clusters (3)")
	lines := strings.Split(h, "\n")
	if len(lines) != 4 {
		t.Fatalf("lines=%d", len(lines))
	}
	if !strings.Contains(lines[0], "▀█▀ ▀▄▀") || !strings.Contains(lines[0], "● connected") {
		t.Fatalf("row1: %q", lines[0])
	}
	if !strings.Contains(lines[1], "updated 2s ago") {
		t.Fatalf("row2: %q", lines[1])
	}
	if strings.TrimSpace(lines[2]) != "" {
		t.Fatalf("row3 must be a blank margin: %q", lines[2])
	}
	if !strings.Contains(lines[3], "Clusters (3)") {
		t.Fatalf("row4: %q", lines[3])
	}
	for _, l := range lines {
		if lipgloss.Width(l) > 100 {
			t.Fatalf("too wide: %d", lipgloss.Width(l))
		}
	}
}

func TestHeaderCompact(t *testing.T) {
	h := renderHeader(80, true, "● connected  http://x", "row2 ignored", "Clusters (3)")
	lines := strings.Split(h, "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "txenvoy") || !strings.Contains(lines[1], "Clusters (3)") {
		t.Fatalf("%q", h)
	}
}

func TestBannerWrapsAndPads(t *testing.T) {
	text := "✗ disconnected · last ok 14s ago\nGET /clusters: dial tcp 127.0.0.1:15000: connect: connection refused"
	rows := bannerLines(text, 40, 4)
	if len(rows) < 3 || len(rows) > 4 {
		t.Fatalf("rows=%d: %q", len(rows), rows)
	}
	for _, r := range rows {
		if lipgloss.Width(r) != 40 {
			t.Fatalf("row width %d != 40: %q", lipgloss.Width(r), r)
		}
	}
	if !strings.Contains(rows[0], "disconnected") {
		t.Fatal(rows[0])
	}
}

func TestBannerTruncatesWithEllipsis(t *testing.T) {
	long := strings.Repeat("error words that go on and on ", 50)
	rows := bannerLines(long, 50, 4)
	if len(rows) != 4 {
		t.Fatalf("rows=%d", len(rows))
	}
	if !strings.Contains(rows[3], "…") {
		t.Fatalf("last row must end with an ellipsis: %q", rows[3])
	}
	for _, r := range rows {
		if lipgloss.Width(r) != 50 {
			t.Fatalf("width %d", lipgloss.Width(r))
		}
	}
}

func TestBannerUnbrokenLongTokenAndHugeText(t *testing.T) {
	rows := bannerLines(strings.Repeat("x", 500), 30, 3)
	if len(rows) != 3 {
		t.Fatalf("rows=%d", len(rows))
	}
	for _, r := range rows {
		if lipgloss.Width(r) != 30 {
			t.Fatalf("width %d", lipgloss.Width(r))
		}
	}
	if len(bannerLines("", 30, 3)) != 1 {
		t.Fatal("empty text still yields one row")
	}
}

func TestOverlayReplacesOnlyTopLines(t *testing.T) {
	frame := strings.Join([]string{"l1", "l2", "l3", "l4", "l5"}, "\n")
	got := strings.Split(overlay(frame, []string{"B1", "B2"}), "\n")
	if len(got) != 5 || got[0] != "B1" || got[1] != "B2" || got[2] != "l3" || got[4] != "l5" {
		t.Fatalf("%q", got)
	}
	if overlay(frame, nil) != frame {
		t.Fatal("no banner leaves the frame untouched")
	}
}

func TestStatusLine(t *testing.T) {
	s := renderStatus(60, " 3 clusters · 1 unhealthy", "/ filter · q quit")
	if strings.Contains(s, "\n") || lipgloss.Width(s) > 60 || !strings.Contains(s, "3 clusters") {
		t.Fatalf("%q", s)
	}
	narrow := renderStatus(20, " 3 clusters · 1 unhealthy", "/ filter · q quit")
	if lipgloss.Width(narrow) > 20 {
		t.Fatalf("narrow width %d", lipgloss.Width(narrow))
	}
}

func TestBannerRowsAreRedAndContentIntact(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(prev)
	rows := bannerLines(strings.Repeat("x", 500), 30, 3)
	for _, r := range rows {
		if !strings.Contains(r, "\x1b[") || !strings.Contains(r, "41") {
			t.Fatalf("row not styled: %q", r)
		}
		plain := ansi.Strip(r)
		if !strings.HasPrefix(plain, " x") {
			t.Fatalf("content missing: %q", plain)
		}
	}
	if !strings.HasSuffix(ansi.Strip(rows[2]), "…"+strings.Repeat(" ", 0)) && !strings.Contains(ansi.Strip(rows[2]), "…") {
		t.Fatalf("ellipsis missing: %q", rows[2])
	}
	// no row exceeds width before any padding: exact content is <= width-1 cells
	for _, r := range rows[:2] {
		if got := strings.Count(ansi.Strip(r), "x"); got != 28 {
			t.Fatalf("token should fill inner width, got %d x", got)
		}
	}
	huge := bannerLines(strings.Repeat("word ", 100000), 40, 5)
	if len(huge) != 5 {
		t.Fatalf("huge rows=%d", len(huge))
	}
	for _, r := range huge {
		if lipgloss.Width(r) != 40 {
			t.Fatalf("huge width %d", lipgloss.Width(r))
		}
	}
	if !strings.Contains(ansi.Strip(huge[4]), "…") {
		t.Fatal("huge text needs ellipsis")
	}
}

func TestBannerShortTextNoEllipsis(t *testing.T) {
	rows := bannerLines("boom", 20, 4)
	if len(rows) != 1 || strings.Contains(rows[0], "…") || !strings.Contains(rows[0], "boom") {
		t.Fatalf("%q", rows)
	}
}

func TestJoinEndsNeverExceedsWidth(t *testing.T) {
	for w := 1; w <= 40; w++ {
		for _, c := range [][2]string{{"left side", "right side"}, {"a-very-long-left-part-here", "r"}, {"l", "a-very-long-right-part-here"}} {
			got := joinEnds(c[0], c[1], w)
			if lipgloss.Width(got) > w || strings.Contains(got, "\n") {
				t.Fatalf("w=%d %q width %d", w, got, lipgloss.Width(got))
			}
		}
	}
	got := ansi.Strip(joinEnds("left", "right", 30))
	if !strings.HasPrefix(got, "left") || !strings.HasSuffix(got, "right") || len(got) != 30 {
		t.Fatalf("%q", got)
	}
	if got := ansi.Strip(joinEnds("left side", "right side", 12)); !strings.HasPrefix(got, "left side") {
		t.Fatalf("left must be kept: %q", got)
	}
}

func TestHeaderNeverExceedsWidthWhenSmall(t *testing.T) {
	for _, w := range []int{1, 5, 10, 20, 30, 45} {
		for _, compact := range []bool{false, true} {
			h := renderHeader(w, compact, "● connected  http://localhost:15000", "Envoy 1.31.0 · updated 2s ago", "Clusters (3)")
			want := 4
			if compact {
				want = 2
			}
			lines := strings.Split(h, "\n")
			if len(lines) != want {
				t.Fatalf("w=%d lines=%d", w, len(lines))
			}
			for _, l := range lines {
				if lipgloss.Width(l) > w {
					t.Fatalf("w=%d compact=%v line %d wide: %q", w, compact, lipgloss.Width(l), l)
				}
			}
		}
	}
}

func TestStatusNeverExceedsWidth(t *testing.T) {
	for w := 1; w <= 50; w++ {
		s := renderStatus(w, " 3 clusters · 1 unhealthy", "/ filter · q quit")
		if lipgloss.Width(s) > w {
			t.Fatalf("w=%d width %d", w, lipgloss.Width(s))
		}
	}
}
