package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

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
