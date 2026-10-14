package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/envoyadmin/internal/snapshot"
)

func TestWrapLines(t *testing.T) {
	got := wrapLines("alpha beta gamma delta", 10, 4)
	if strings.Join(got, "|") != "alpha beta|gamma|delta" {
		t.Fatalf("%q", got)
	}
	capped := wrapLines(strings.Repeat("word ", 40), 12, 3)
	if len(capped) != 3 || !strings.HasSuffix(capped[2], "…") {
		t.Fatalf("a capped wrap ends with an ellipsis: %q", capped)
	}
	for _, l := range capped {
		if lipgloss.Width(l) > 12 {
			t.Fatalf("line wider than 12: %q", l)
		}
	}
	if got := wrapLines("", 10, 3); len(got) != 0 {
		t.Fatalf("empty text has no rows: %q", got)
	}
	if got := wrapLines("a\nb", 10, 3); strings.Join(got, "|") != "a|b" {
		t.Fatalf("existing newlines are kept: %q", got)
	}
	if got := wrapLines("x", 0, 3); len(got) != 0 {
		t.Fatalf("zero width: %q", got)
	}
}

func TestCursorGlyph(t *testing.T) {
	if cursorGlyph(rowCtx{}) != " " || cursorGlyph(rowCtx{Cursor: true}) != ">" || cursorGlyph(rowCtx{Cursor: true, Unfocused: true}) != "›" {
		t.Fatal("cursor glyphs")
	}
}

func TestStyledTextHighlightsByteOffsets(t *testing.T) {
	// "日本-api": hits 7..9 are byte offsets of "api"
	out := styledText("日本-api", []int{7, 8, 9}, false)
	if got := ansiStrip(out); got != "日本-api" {
		t.Fatalf("text must be unchanged: %q", got)
	}
}

func TestClusterItemSatisfiesTheInterface(t *testing.T) {
	c := cl("api", 1, 2)
	c.Config.YAML = "name: api\n"
	it := clusterItems([]snapshot.Cluster{c})[0]
	if it.Key() != "api" || it.FilterText() != "api" || it.Title() != "api" || it.OverviewName() != "Endpoints" {
		t.Fatalf("identity: %q %q %q %q", it.Key(), it.FilterText(), it.Title(), it.OverviewName())
	}
	row := ansiStrip(it.Row(rowCtx{Width: 40, Cursor: true}))
	if !strings.HasPrefix(row, ">") || !strings.Contains(row, "1/2") || lipgloss.Width(row) > 40 {
		t.Fatalf("row %q", row)
	}
	ov := it.Overview(100, 8, 0)
	if len(strings.Split(ov, "\n")) != 8 || !strings.Contains(ov, "ADDRESS") {
		t.Fatalf("overview:\n%s", ov)
	}
	if it.OverviewMaxScroll(100, 8) != maxEndpointScroll(c, 8) {
		t.Fatal("max scroll delegates to the endpoints table")
	}
	if text, _ := it.Config().Render(); text != "name: api\n" {
		t.Fatalf("config %q", text)
	}
}
