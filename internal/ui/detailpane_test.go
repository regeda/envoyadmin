package ui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/regeda/txenvoy/internal/snapshot"
)

func configCluster() snapshot.Cluster {
	c := hostsCluster()
	c.Config = snapshot.ConfigInfo{
		YAML:        "name: api\ntype: EDS\nconnect_timeout: 5s\n",
		VersionInfo: "42",
		LastUpdated: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC),
	}
	return c
}

func newDetail(color bool) detailPane {
	d := newDetailPane(color)
	d.SetSize(100, 14)
	return d
}

func TestHighlightYAML(t *testing.T) {
	src := "name: api\ntype: EDS\n"
	if got := highlightYAML(src, false); got != "name: api\ntype: EDS" {
		t.Fatalf("plain: %q", got)
	}
	got := highlightYAML(src, true)
	if !strings.Contains(got, "\x1b[") || !strings.Contains(got, "name") || !strings.Contains(got, "EDS") {
		t.Fatalf("color: %q", got)
	}
	if highlightYAML("", true) != "" {
		t.Fatal("empty in, empty out")
	}
}

func TestNoClusterSelected(t *testing.T) {
	d := newDetail(false)
	d.SetCluster(snapshot.Cluster{}, false)
	if !strings.Contains(d.View(), "no cluster selected") {
		t.Fatal(d.View())
	}
	d.SetTab(tabConfig)
	if !strings.Contains(d.View(), "no cluster selected") {
		t.Fatal("config tab with no cluster")
	}
}

func TestEndpointsTabIsDefault(t *testing.T) {
	d := newDetail(false)
	d.SetCluster(configCluster(), true)
	v := d.View()
	if !strings.Contains(v, "10.0.0.1:80") || !strings.Contains(v, "api") {
		t.Fatal(v)
	}
	if n := len(strings.Split(v, "\n")); n != 14 {
		t.Fatalf("height %d, want 14", n)
	}
}

func TestConfigTabShowsVersionAndYAML(t *testing.T) {
	d := newDetail(false)
	d.SetCluster(configCluster(), true)
	d.SetTab(tabConfig)
	v := d.View()
	for _, want := range []string{"version_info: 42", "last_updated: 2026-10-14", "connect_timeout: 5s"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q in:\n%s", want, v)
		}
	}
	if n := len(strings.Split(v, "\n")); n != 14 {
		t.Fatalf("height %d, want 14", n)
	}
}

func TestConfigTabStaticAndError(t *testing.T) {
	c := configCluster()
	c.Config.Static, c.Config.VersionInfo = true, ""
	d := newDetail(false)
	d.SetCluster(c, true)
	d.SetTab(tabConfig)
	if !strings.Contains(d.View(), "static") || strings.Contains(d.View(), "version_info") {
		t.Fatal(d.View())
	}

	c.Config = snapshot.ConfigInfo{Err: "config unavailable: boom"}
	d.SetCluster(c, true)
	if !strings.Contains(d.View(), "config unavailable: boom") {
		t.Fatal(d.View())
	}
}

func TestHighlightIsCached(t *testing.T) {
	d := newDetail(true)
	c := configCluster()
	d.SetCluster(c, true)
	d.SetCluster(c, true)
	d.SetCluster(c, true)
	if d.highlights != 1 {
		t.Fatalf("highlighted %d times, want 1", d.highlights)
	}
	c.Config.YAML += "extra: 1\n"
	d.SetCluster(c, true)
	if d.highlights != 2 {
		t.Fatalf("changed YAML must re-highlight, got %d", d.highlights)
	}
}

func TestEndpointScrollResetsOnClusterChange(t *testing.T) {
	big := snapshot.Cluster{Name: "big"}
	for i := 0; i < 40; i++ {
		big.Hosts = append(big.Hosts, snapshot.Host{Address: "10.0.0." + string(rune('a'+i%26)) + ":80"})
	}
	d := newDetail(false)
	d.SetCluster(big, true)
	for i := 0; i < 100; i++ {
		d.HandleKey(key("j"))
	}
	if d.scroll != maxEndpointScroll(big, 13) {
		t.Fatalf("scroll = %d, want clamp %d", d.scroll, maxEndpointScroll(big, 13))
	}
	d.SetCluster(big, true) // same cluster: keep scroll
	if d.scroll == 0 {
		t.Fatal("refresh of the same cluster must keep scroll")
	}
	other := big
	other.Name = "other"
	d.SetCluster(other, true)
	if d.scroll != 0 {
		t.Fatal("a different cluster resets scroll")
	}
}

func TestDetailViewWidthIsBounded(t *testing.T) {
	d := newDetailPane(false)
	d.SetSize(40, 10)
	c := configCluster()
	c.Config.YAML = strings.Repeat("k: "+strings.Repeat("v", 200)+"\n", 3)
	d.SetCluster(c, true)
	for _, tb := range []tab{tabEndpoints, tabConfig} {
		d.SetTab(tb)
		for _, l := range strings.Split(d.View(), "\n") {
			if lipgloss.Width(l) > 40 {
				t.Fatalf("tab %d: width %d: %q", tb, lipgloss.Width(l), l)
			}
		}
	}
}

func TestDetailDegenerateInputsDoNotPanic(t *testing.T) {
	for _, size := range [][2]int{{0, 0}, {1, 1}, {0, 5}, {5, 0}, {3, 2}} {
		for _, has := range []bool{false, true} {
			for _, tb := range []tab{tabEndpoints, tabConfig} {
				d := newDetailPane(true)
				d.SetSize(size[0], size[1])
				d.SetCluster(configCluster(), has)
				d.SetTab(tb)
				d.HandleKey(key("j"))
				d.HandleKey(key("G"))
				_ = d.View()
			}
		}
	}
}

func TestNoClusterViewHeight(t *testing.T) {
	d := newDetail(false)
	d.SetTab(tabConfig)
	if n := len(strings.Split(d.View(), "\n")); n != 14 {
		t.Fatalf("height %d, want 14", n)
	}
}

func TestConfigErrorReplacesYAMLAndRecovers(t *testing.T) {
	c := configCluster()
	c.Config.Err = "config unavailable: boom"
	d := newDetail(false)
	d.SetCluster(c, true)
	d.SetTab(tabConfig)
	v := d.View()
	if !strings.Contains(v, "config unavailable: boom") || strings.Contains(v, "connect_timeout") {
		t.Fatal(v)
	}
	c.Config.Err = ""
	d.SetCluster(c, true)
	if v := d.View(); !strings.Contains(v, "connect_timeout: 5s") || strings.Contains(v, "boom") {
		t.Fatal(v)
	}
}

func TestLongYAMLLineClippedButStartVisible(t *testing.T) {
	d := newDetailPane(false)
	d.SetSize(30, 10)
	c := configCluster()
	c.Config.YAML = "key: " + strings.Repeat("v", 200) + "\nnext: 1\n"
	d.SetCluster(c, true)
	d.SetTab(tabConfig)
	v := d.View()
	if !strings.Contains(v, "key: vvvv") || !strings.Contains(v, "next: 1") {
		t.Fatalf("content lost:\n%s", v)
	}
	if strings.Contains(v, strings.Repeat("v", 60)) {
		t.Fatalf("line not clipped:\n%s", v)
	}
	if n := len(strings.Split(v, "\n")); n != 10 {
		t.Fatalf("height %d", n)
	}
}

func TestConfigScrollKeepsAcrossRefreshResetsOnClusterChange(t *testing.T) {
	c := configCluster()
	c.Config.YAML = ""
	for i := 0; i < 50; i++ {
		c.Config.YAML += "line" + strings.Repeat("x", i%3) + ": " + string(rune('a'+i%26)) + "\n"
	}
	d := newDetail(false)
	d.SetCluster(c, true)
	d.SetTab(tabConfig)
	first := d.View()
	d.HandleKey(key("pgdown"))
	if d.View() == first {
		t.Fatal("pgdown did not scroll config viewport")
	}
	scrolled := d.View()
	d.SetCluster(c, true)
	if d.View() != scrolled {
		t.Fatal("refresh with same YAML must keep scroll")
	}
	c.Name = "other"
	d.SetCluster(c, true)
	if !strings.Contains(d.View(), "other") || !strings.Contains(d.View(), "line: a") {
		t.Fatalf("new cluster should start at top:\n%s", d.View())
	}
}

func TestPlainYAMLHasNoEscapesWhenColorOff(t *testing.T) {
	d := newDetail(false)
	d.SetCluster(configCluster(), true)
	d.SetTab(tabConfig)
	if strings.Contains(d.cfgOut, "\x1b") {
		t.Fatalf("escapes in plain YAML: %q", d.cfgOut)
	}
	d2 := newDetail(true)
	d2.SetCluster(configCluster(), true)
	if !strings.Contains(d2.cfgOut, "\x1b[") {
		t.Fatal("color on must highlight")
	}
}

func longYAML() string {
	y := "key: " + strings.Repeat("v", 200) + "\n"
	for i := 0; i < 20; i++ {
		y += "item" + string(rune('a'+i)) + ": " + strings.Repeat("w", 100) + "\n"
	}
	return y + "last: z\n"
}

func rowsOf(v string) []string { return strings.Split(v, "\n") }

func rowIndex(rows []string, sub string) int {
	for i, r := range rows {
		if strings.Contains(ansi.Strip(r), sub) {
			return i
		}
	}
	return -1
}

func TestLongLineIsOneRowClippedAtBoundary(t *testing.T) {
	d := newDetailPane(false)
	d.SetSize(30, 10)
	c := configCluster()
	c.Config.YAML = longYAML()
	d.SetCluster(c, true)
	d.SetTab(tabConfig)
	rows := rowsOf(d.View())
	if len(rows) != 10 {
		t.Fatalf("rows %d", len(rows))
	}
	// row 0 tab line, row 1 header, row 2 the long line, row 3 the next line
	if got := rowIndex(rows, "key: "); got != 2 {
		t.Fatalf("key row %d\n%s", got, strings.Join(rows, "\n"))
	}
	if got := rowIndex(rows, "itema:"); got != 3 {
		t.Fatalf("itema row %d, long line wrapped?\n%s", got, strings.Join(rows, "\n"))
	}
	if w := lipgloss.Width(rows[2]); w != 30 {
		t.Fatalf("clipped row width %d, want 30", w)
	}
	gutter := lipgloss.Width(yamlGutter(1, c.Config.YAML)) // "<n>│ " in front of every line
	if n := strings.Count(rows[2], "v"); n != 30-gutter-len("key: ") {
		t.Fatalf("v count %d: tail of long value leaked (gutter %d)", n, gutter)
	}
	// every later line is reachable by scrolling
	for i := 0; i < 10; i++ {
		d.HandleKey(key("pgdown"))
	}
	if !strings.Contains(d.View(), "last: z") {
		t.Fatalf("last line unreachable:\n%s", d.View())
	}
}

func TestLongLineColorIsCutCleanly(t *testing.T) {
	d := newDetailPane(true)
	d.SetSize(30, 10)
	c := configCluster()
	c.Config.YAML = longYAML()
	d.SetCluster(c, true)
	d.SetTab(tabConfig)
	rows := rowsOf(d.View())
	if got := rowIndex(rows, "itema:"); got != 3 {
		t.Fatalf("itema row %d\n%s", got, strings.Join(rows, "\n"))
	}
	plain := yamlGutter(1, c.Config.YAML) + "key: " + strings.Repeat("v", 200)
	for i, r := range rows {
		if lipgloss.Width(r) > 30 {
			t.Fatalf("row %d width %d", i, lipgloss.Width(r))
		}
		if strings.Contains(ansi.Strip(r), "\x1b") {
			t.Fatalf("row %d broken escape: %q", i, r)
		}
	}
	if !strings.Contains(rows[2], "\x1b[") {
		t.Fatalf("expected highlighted row: %q", rows[2])
	}
	got := strings.TrimRight(ansi.Strip(rows[2]), " ")
	if want := ansi.Truncate(plain, 30, ""); got != want {
		t.Fatalf("row 2 = %q, want %q", got, want)
	}
}

func TestLongNameAndHeaderStayOneRow(t *testing.T) {
	d := newDetailPane(false)
	d.SetSize(30, 10)
	c := configCluster()
	c.Name = strings.Repeat("n", 50)
	c.Config.VersionInfo = "2026-10-14T10:00:00Z/abcdef"
	d.SetCluster(c, true)
	d.SetTab(tabConfig)
	rows := rowsOf(d.View())
	if len(rows) != 10 {
		t.Fatalf("rows %d", len(rows))
	}
	if got := rowIndex(rows, "name: api"); got != 2 {
		t.Fatalf("yaml starts at row %d, tab/header wrapped?\n%s", got, strings.Join(rows, "\n"))
	}
	if !strings.HasPrefix(ansi.Strip(rows[1]), "version_info: 2026") {
		t.Fatalf("header row: %q", rows[1])
	}
	d.SetTab(tabEndpoints)
	rows = rowsOf(d.View())
	if len(rows) != 10 {
		t.Fatalf("endpoints rows %d", len(rows))
	}
	for i, r := range rows {
		if lipgloss.Width(r) > 30 {
			t.Fatalf("row %d width %d", i, lipgloss.Width(r))
		}
	}
}

func TestResizeRetruncatesWithoutRehighlight(t *testing.T) {
	d := newDetailPane(true)
	d.SetSize(30, 10)
	c := configCluster()
	c.Config.YAML = longYAML()
	d.SetCluster(c, true)
	d.SetTab(tabConfig)
	d.SetSize(60, 10)
	rows := rowsOf(d.View())
	if w := lipgloss.Width(rows[2]); w != 60 {
		t.Fatalf("after widening row width %d, want 60", w)
	}
	if d.highlights != 1 {
		t.Fatalf("resize re-ran chroma: %d", d.highlights)
	}
}

func TestDisappearThenReappearResetsScroll(t *testing.T) {
	c := configCluster()
	c.Config.YAML = longYAML()
	c.Hosts = nil
	for i := 0; i < 40; i++ {
		c.Hosts = append(c.Hosts, snapshot.Host{Address: "10.0.1." + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ":80"})
	}
	for _, tb := range []tab{tabEndpoints, tabConfig} {
		d := newDetail(false)
		d.SetCluster(c, true)
		d.SetTab(tb)
		top := d.View()
		d.HandleKey(key("pgdown"))
		if d.View() == top {
			t.Fatalf("tab %d: did not scroll", tb)
		}
		d.SetCluster(snapshot.Cluster{}, false)
		d.SetCluster(c, true)
		if d.View() != top {
			t.Fatalf("tab %d: scroll kept after disappear/reappear", tb)
		}
	}
}

// yamlGutter is the plain-text gutter numberLines puts before line n of yaml.
func yamlGutter(n int, yaml string) string {
	total := len(strings.Split(strings.TrimRight(yaml, "\n"), "\n"))
	return fmt.Sprintf("%*d│ ", len(strconv.Itoa(total)), n)
}

func numberedConfig(t *testing.T, yaml string, h int) (detailPane, []string) {
	t.Helper()
	d := newDetailPane(false)
	d.SetSize(60, h)
	c := configCluster()
	c.Config.YAML = yaml
	d.SetCluster(c, true)
	d.SetTab(tabConfig)
	return d, rowsOf(d.View())
}

func TestConfigLinesAreNumbered(t *testing.T) {
	yaml := "name: api\ntype: EDS\nconnect_timeout: 5s\n"
	_, rows := numberedConfig(t, yaml, 8)
	// row 0 tab line, row 1 header, rows 2.. the YAML
	want := []string{"1│ name: api", "2│ type: EDS", "3│ connect_timeout: 5s"}
	for i, w := range want {
		if got := strings.TrimRight(rows[2+i], " "); got != w {
			t.Fatalf("row %d = %q, want %q", 2+i, got, w)
		}
	}
	if strings.Contains(rows[5], "│") {
		t.Fatalf("no number past the last YAML line: %q", rows[5])
	}
}

func TestConfigLineNumbersAreRightAlignedAndStartWithOneAfterScrolling(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 120; i++ {
		fmt.Fprintf(&b, "k%d: v\n", i)
	}
	d, rows := numberedConfig(t, b.String(), 12)
	if got := strings.TrimRight(rows[2], " "); got != "  1│ k1: v" {
		t.Fatalf("first row %q: numbers must be right-aligned to the widest (3 digits)", got)
	}
	d.HandleKey(key("pgdown"))
	rows = rowsOf(d.View())
	first := strings.TrimRight(rows[2], " ")
	n := d.vp.YOffset
	if n == 0 {
		t.Fatal("pgdown should have scrolled")
	}
	if want := fmt.Sprintf("%3d│ k%d: v", n+1, n+1); first != want {
		t.Fatalf("after scrolling the first row is %q, want %q (numbers follow the YAML line)", first, want)
	}
	for i := 0; i < 20; i++ {
		d.HandleKey(key("pgdown"))
	}
	rows = rowsOf(d.View())
	if !strings.Contains(strings.Join(rows, "\n"), "120│ k120: v") {
		t.Fatalf("the last line must carry number 120:\n%s", strings.Join(rows, "\n"))
	}
}

func TestConfigNumbersAbsentForErrorsAndEmptyYAML(t *testing.T) {
	d := newDetailPane(false)
	d.SetSize(60, 8)
	c := configCluster()
	c.Config = snapshot.ConfigInfo{Err: "config unavailable: boom"}
	d.SetCluster(c, true)
	d.SetTab(tabConfig)
	v := d.View()
	if !strings.Contains(v, "config unavailable: boom") || strings.Contains(v, "│") {
		t.Fatalf("an error must not be numbered:\n%s", v)
	}
	c.Config = snapshot.ConfigInfo{}
	d.SetCluster(c, true)
	if strings.Contains(d.View(), "│") {
		t.Fatalf("empty YAML must not be numbered:\n%s", d.View())
	}
}

func TestLineNumbersDoNotTriggerReHighlighting(t *testing.T) {
	d := newDetailPane(true)
	d.SetSize(60, 10)
	c := configCluster()
	d.SetCluster(c, true)
	d.SetTab(tabConfig)
	before := d.highlights
	d.SetSize(80, 12)
	d.SetSize(60, 10)
	d.SetCluster(c, true)
	if d.highlights != before {
		t.Fatalf("resize/refresh re-ran chroma (%d -> %d)", before, d.highlights)
	}
	if !strings.Contains(ansi.Strip(d.View()), "1│ ") {
		t.Fatalf("numbers must also be present when colour is on:\n%s", ansi.Strip(d.View()))
	}
}

func TestLineNumbersAreDarkerThanDimText(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	row := strings.Split(numberLines("name: api"), "\n")[0]
	if !strings.Contains(row, "38;5;240") {
		t.Fatalf("line numbers must use grey 240: %q", row)
	}
	if strings.Contains(row, "38;5;8m") {
		t.Fatalf("line numbers must not use the lighter dim grey: %q", row)
	}
	if got := ansi.Strip(row); got != "1│ name: api" {
		t.Fatalf("no space before the bar, one after it: %q", got)
	}
	// the number and its bar are styled together; the YAML text itself is untouched
	if idx := strings.Index(row, "name: api"); idx < 0 || strings.Contains(row[idx:], "\x1b[") {
		t.Fatalf("the YAML text must not inherit the gutter colour: %q", row)
	}
}
