package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/regeda/envoyadmin/internal/demo"
	"github.com/regeda/envoyadmin/internal/snapshot"
)

func hostsCluster() snapshot.Cluster {
	return snapshot.Cluster{Name: "api", Hosts: []snapshot.Host{
		{Address: "10.0.0.2:80", Zone: "a", Weight: 1, RqActive: 2, CxActive: 5},
		{Address: "10.0.0.1:80", Zone: "b", Weight: 1, RqError: 17,
			Flags: []string{"FAILED_ACTIVE_HC", "EDS_STATUS_UNHEALTHY"}},
		{Address: "10.0.0.3:80", Weight: 2, Flags: []string{"FAILED_OUTLIER_CHECK"}},
	}}
}

func TestEndpointsOrderAndSummary(t *testing.T) {
	out := renderEndpoints(hostsCluster(), 120, 10, 0)
	lines := strings.Split(out, "\n")
	if len(lines) != 10 {
		t.Fatalf("lines = %d, want 10", len(lines))
	}
	if !strings.Contains(lines[0], "3 hosts") || !strings.Contains(lines[0], "1 healthy") || !strings.Contains(lines[0], "1 ejected") {
		t.Fatalf("summary: %q", lines[0])
	}
	for _, col := range []string{"ADDRESS", "FLAGS", "WEIGHT", "ZONE", "RQ_ACTIVE", "RQ_ERR", "CX_ACTIVE"} {
		if !strings.Contains(lines[1], col) {
			t.Fatalf("header missing %s: %q", col, lines[1])
		}
	}
	// unhealthy hosts first, then by address; healthy host last
	if !strings.Contains(lines[2], "10.0.0.1:80") || !strings.Contains(lines[3], "10.0.0.3:80") || !strings.Contains(lines[4], "10.0.0.2:80") {
		t.Fatalf("order wrong:\n%s", out)
	}
	if !strings.Contains(lines[2], "FAILED_ACTIVE_HC") || !strings.Contains(lines[2], "EDS_STATUS_UNHEALTHY") {
		t.Fatalf("flags missing: %q", lines[2])
	}
	if !strings.Contains(lines[4], "OK") {
		t.Fatalf("healthy host should show OK: %q", lines[4])
	}
}

func TestEndpointsMissingFieldsAndPipeAddress(t *testing.T) {
	c := snapshot.Cluster{Name: "x", Hosts: []snapshot.Host{{Address: "unix:/var/run/agent.sock"}}}
	out := renderEndpoints(c, 100, 6, 0)
	if !strings.Contains(out, "unix:/var/run/agent.sock") {
		t.Fatal(out)
	}
	if !strings.Contains(strings.Split(out, "\n")[2], " - ") {
		t.Fatalf("empty zone should render '-': %q", strings.Split(out, "\n")[2])
	}
}

func TestEndpointsNoHosts(t *testing.T) {
	out := renderEndpoints(snapshot.Cluster{Name: "x"}, 80, 6, 0)
	if !strings.Contains(out, "no hosts") || len(strings.Split(out, "\n")) != 6 {
		t.Fatal(out)
	}
}

func TestEndpointsNeverExceedWidthOrHeight(t *testing.T) {
	c := hostsCluster()
	c.Hosts[1].Flags = append(c.Hosts[1].Flags, "FAILED_OUTLIER_CHECK", "ACTIVE_HC_TIMEOUT", "PENDING_DYNAMIC_REMOVAL")
	for _, w := range []int{30, 60, 100} {
		out := renderEndpoints(c, w, 8, 0)
		lines := strings.Split(out, "\n")
		if len(lines) != 8 {
			t.Fatalf("w=%d lines=%d", w, len(lines))
		}
		for _, l := range lines {
			if lipgloss.Width(l) > w {
				t.Fatalf("w=%d line width %d: %q", w, lipgloss.Width(l), l)
			}
		}
	}
}

func TestEndpointsScrollAndClamp(t *testing.T) {
	c := snapshot.Cluster{Name: "big"}
	for i := 0; i < 20; i++ {
		c.Hosts = append(c.Hosts, snapshot.Host{Address: fmt.Sprintf("10.0.0.%02d:80", i)})
	}
	if m := maxEndpointScroll(c, 10); m != 12 {
		t.Fatalf("max scroll = %d, want 12", m)
	}
	out := renderEndpoints(c, 100, 10, 9999) // clamped, not out of range
	if !strings.Contains(out, "10.0.0.19:80") {
		t.Fatal("scroll should clamp to the last page")
	}
	if maxEndpointScroll(snapshot.Cluster{}, 10) != 0 {
		t.Fatal("no hosts: no scroll")
	}
}

func TestEndpointsNarrowWidthsDoNotPanic(t *testing.T) {
	c := hostsCluster()
	c.Hosts = append(c.Hosts, snapshot.Host{Address: "unix:/var/run/日本語.sock", Zone: "ゾーン-東京-1"})
	for _, w := range []int{1, 2, 5, 10, 20} {
		for _, h := range []int{1, 2, 3, 8} {
			out := renderEndpoints(c, w, h, 0)
			lines := strings.Split(out, "\n")
			if len(lines) != h {
				t.Fatalf("w=%d h=%d lines=%d", w, h, len(lines))
			}
			for _, l := range lines {
				if lipgloss.Width(l) > w {
					t.Fatalf("w=%d h=%d width %d: %q", w, h, lipgloss.Width(l), l)
				}
			}
		}
	}
	if renderEndpoints(c, 0, 5, 0) != "" || renderEndpoints(c, 50, 0, 0) != "" {
		t.Fatal("zero size should render empty")
	}
}

func TestEndpointsBlankStatsShowZeroAndDash(t *testing.T) {
	c := snapshot.Cluster{Name: "x", Hosts: []snapshot.Host{{Address: "unix:/a.sock"}}}
	row := strings.Fields(strings.Split(renderEndpoints(c, 120, 4, 0), "\n")[2])
	// address OK weight zone rq_active rq_err cx_active
	want := []string{"unix:/a.sock", "OK", "0", "-", "0", "0", "0"}
	if strings.Join(row, "|") != strings.Join(want, "|") {
		t.Fatalf("row = %v, want %v", row, want)
	}
}

func TestEndpointsFlagOverflowShowsCount(t *testing.T) {
	c := snapshot.Cluster{Name: "x", Hosts: []snapshot.Host{{Address: "a", Flags: []string{
		"FAILED_ACTIVE_HC", "FAILED_OUTLIER_CHECK", "EDS_STATUS_UNHEALTHY"}}}}
	out := renderEndpoints(c, 100, 4, 0)
	if !strings.Contains(strings.Split(out, "\n")[2], "+") {
		t.Fatalf("expected +N overflow marker: %q", out)
	}
}

func TestFlagCellExactWidth(t *testing.T) {
	lists := [][]string{
		nil,
		{"FAILED_ACTIVE_HC"},
		{"FAILED_ACTIVE_HC", "EDS_STATUS_UNHEALTHY"},
		{"FAILED_ACTIVE_HC", "FAILED_OUTLIER_CHECK", "EDS_STATUS_UNHEALTHY", "ACTIVE_HC_TIMEOUT"},
		{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L"},
	}
	for _, fl := range lists {
		for w := 1; w <= 90; w++ {
			if got := lipgloss.Width(flagCell(fl, w)); got != w {
				t.Fatalf("flags=%v width=%d: cell width %d", fl, w, got)
			}
		}
	}
}

func TestFlagCellOverflowMarker(t *testing.T) {
	fl := []string{"FAILED_ACTIVE_HC", "FAILED_OUTLIER_CHECK", "EDS_STATUS_UNHEALTHY"}
	// 16 + 1 + 20 = 37 fits two flags only with the marker: "+1" needs 3 more.
	got := ansiStrip(flagCell(fl, 40))
	if strings.TrimRight(got, " ") != "FAILED_ACTIVE_HC FAILED_OUTLIER_CHECK +1" {
		t.Fatalf("got %q", got)
	}
	got = ansiStrip(flagCell(fl, 38))
	if strings.TrimRight(got, " ") != "FAILED_ACTIVE_HC +2" {
		t.Fatalf("got %q", got)
	}
	// two-digit count
	many := make([]string, 12)
	for i := range many {
		many[i] = "FLAG_NUMBER_" + fmt.Sprint(i)
	}
	got = strings.TrimRight(ansiStrip(flagCell(many, 20)), " ")
	if !strings.HasSuffix(got, "+11") || lipgloss.Width(got) > 20 {
		t.Fatalf("got %q", got)
	}
}

func TestFlagCellTruncatesFirstFlag(t *testing.T) {
	fl := []string{"FAILED_ACTIVE_HC", "EDS_STATUS_UNHEALTHY"}
	got := strings.TrimRight(ansiStrip(flagCell(fl, 12)), " ")
	if got != "FAILED_A… +1" {
		t.Fatalf("got %q", got)
	}
	got = strings.TrimRight(ansiStrip(flagCell(fl[:1], 12)), " ")
	if got != "FAILED_ACTI…" {
		t.Fatalf("got %q", got)
	}
}

func ansiStrip(s string) string { return ansi.Strip(s) }

func typicalCluster() snapshot.Cluster {
	return snapshot.Cluster{Name: "t", Hosts: []snapshot.Host{
		{Address: "10.0.1.5:8080", Zone: "eu-west-1a", Weight: 1, RqActive: 3, RqError: 4, CxActive: 5,
			Flags: []string{"FAILED_ACTIVE_HC", "EDS_STATUS_UNHEALTHY", "ACTIVE_HC_TIMEOUT"}},
		{Address: "10.0.1.6:8080", Zone: "eu-west-1b", Weight: 1, RqActive: 1, CxActive: 2},
		{Address: "10.0.1.7:8080", Zone: "eu-west-1c", Weight: 1, Flags: []string{"FAILED_OUTLIER_CHECK", "DEGRADED_ACTIVE_HC"}},
	}}
}

func TestEndpointsLayoutFitsWithoutClip(t *testing.T) {
	cols := []string{"ADDRESS", "FLAGS", "WEIGHT", "ZONE", "RQ_ACTIVE", "RQ_ERR", "CX_ACTIVE"}
	one := snapshot.Cluster{Name: "o", Hosts: []snapshot.Host{
		{Address: "10.0.1.5:8080", Zone: "eu-west-1a", Weight: 1, Flags: []string{"FAILED_ACTIVE_HC"}}}}
	// all seven columns whenever the pane holds them at their needed widths
	for _, tc := range []struct {
		c snapshot.Cluster
		w int
	}{{one, 77}, {typicalCluster(), 100}} {
		w := tc.w
		lines := endpointLines(tc.c, w, 8, 0)
		for _, col := range cols {
			if !strings.Contains(lines[1], col) {
				t.Fatalf("w=%d header missing %s: %q", w, col, lines[1])
			}
		}
		hw := lipgloss.Width(lines[1])
		if hw > w {
			t.Fatalf("w=%d unclipped header width %d", w, hw)
		}
		for _, l := range lines[2 : 2+len(tc.c.Hosts)] {
			if lipgloss.Width(l) != hw {
				t.Fatalf("w=%d row width %d != header %d: %q", w, lipgloss.Width(l), hw, l)
			}
		}
	}
}

func TestEndpointsDropsWeightThenZoneWhenNarrow(t *testing.T) {
	lines := endpointLines(typicalCluster(), 60, 8, 0)
	if strings.Contains(lines[1], "ZONE") || strings.Contains(lines[2], "eu-west") {
		t.Fatalf("zone should be dropped: %q", lines[1])
	}
	if strings.Contains(lines[1], "WEIGHT") {
		t.Fatalf("weight should be dropped before flags are squeezed: %q", lines[1])
	}
	if f := strings.Fields(lines[1]); strings.Join(f, " ") != "ADDRESS FLAGS RQ_ACTIVE RQ_ERR CX_ACTIVE" {
		t.Fatalf("columns: %v", f)
	}
	for _, col := range []string{"ADDRESS", "FLAGS", "RQ_ACTIVE", "RQ_ERR", "CX_ACTIVE"} {
		if !strings.Contains(lines[1], col) {
			t.Fatalf("header missing %s: %q", col, lines[1])
		}
	}
	hw := lipgloss.Width(lines[1])
	if hw > 60 {
		t.Fatalf("header width %d", hw)
	}
	for _, l := range lines[2:5] {
		if lipgloss.Width(l) != hw {
			t.Fatalf("row width %d != %d: %q", lipgloss.Width(l), hw, l)
		}
	}
}

// The degraded demo at 120x30 (77-cell detail pane): no flag name is cut.
func TestEndpointsFullFlagNamesAtDefaultSize(t *testing.T) {
	snap := fetchSnap(t, demo.Degraded())
	seen := map[string]bool{}
	for _, c := range snap.Clusters {
		out := ansiStrip(strings.Join(endpointLines(c, 77, 12, 0)[1:], "\n"))
		for _, h := range c.Hosts {
			for i, f := range h.Flags {
				if i > 0 {
					break // later flags may fold into +N
				}
				if !strings.Contains(out, f) {
					t.Fatalf("%s: first flag %q not fully visible:\n%s", c.Name, f, out)
				}
				seen[f] = true
			}
		}
		if strings.Contains(out, "…") {
			t.Fatalf("%s: unexpected truncation:\n%s", c.Name, out)
		}
	}
	if !seen["FAILED_OUTLIER_CHECK"] || !seen["FAILED_ACTIVE_HC"] {
		t.Fatalf("demo no longer exercises both flags: %v", seen)
	}
}

// At the 80-col minimum (51-cell pane) a cut flag ends in an ellipsis and the
// numeric columns survive.
func TestEndpointsTruncatedFlagShowsEllipsisAtMinimumWidth(t *testing.T) {
	lines := endpointLines(typicalCluster(), 51, 8, 0)
	for _, col := range []string{"RQ_ACTIVE", "RQ_ERR", "CX_ACTIVE"} {
		if !strings.Contains(lines[1], col) {
			t.Fatalf("header lost %s: %q", col, lines[1])
		}
	}
	if !strings.Contains(ansiStrip(lines[2]), "…") {
		t.Fatalf("truncated flag has no ellipsis: %q", lines[2])
	}
	for _, l := range lines[1:5] {
		if lipgloss.Width(l) > 51 {
			t.Fatalf("width %d: %q", lipgloss.Width(l), l)
		}
	}
}

func TestEndpointsUnixAddressWholeAtPaneWidth(t *testing.T) {
	c := snapshot.Cluster{Name: "x", Hosts: []snapshot.Host{{Address: "unix:/var/run/agent.sock", Zone: "z"}}}
	lines := endpointLines(c, 77, 4, 0)
	if !strings.Contains(lines[2], "unix:/var/run/agent.sock") {
		t.Fatalf("%q", lines[2])
	}
	if lipgloss.Width(lines[2]) > 77 || !strings.Contains(lines[1], "CX_ACTIVE") {
		t.Fatalf("width %d header %q", lipgloss.Width(lines[2]), lines[1])
	}
}

// At the 80-column minimum the detail pane is 51 wide; the unclipped header
// must still show every remaining column (found by the narrow goldens).
func TestEndpointsHeaderFitsAtMinimumTerminalWidth(t *testing.T) {
	for _, w := range []int{51, 55} {
		lines := endpointLines(typicalCluster(), w, 8, 0)
		if hw := lipgloss.Width(lines[1]); hw > w {
			t.Fatalf("w=%d unclipped header width %d: %q", w, hw, lines[1])
		}
		if !strings.Contains(lines[1], "CX_ACTIVE") {
			t.Fatalf("w=%d header lost CX_ACTIVE: %q", w, lines[1])
		}
	}
}

// Drop priority: WEIGHT goes first, then ZONE, before FLAGS is squeezed.
// (Replaces the round-1 order, which dropped ZONE before WEIGHT.)
func TestEndpointsDropPriorityWeightBeforeZone(t *testing.T) {
	snap := fetchSnap(t, demo.Degraded())
	var c snapshot.Cluster
	for _, cc := range snap.Clusters {
		if cc.Name == "orders-db" {
			c = cc
		}
	}
	// 120x30: 77-cell pane; WEIGHT dropped, ZONE kept, flag whole.
	lines := endpointLines(c, 77, 6, 0)
	if got := strings.Join(strings.Fields(lines[1]), " "); got != "ADDRESS FLAGS ZONE RQ_ACTIVE RQ_ERR CX_ACTIVE" {
		t.Fatalf("w=77 columns: %q", got)
	}
	if !strings.Contains(lines[2], "FAILED_OUTLIER_CHECK") || strings.Contains(lines[2], "…") {
		t.Fatalf("flag cut: %q", lines[2])
	}
	// Only FLAGS + numeric columns fit (flags still whole at their needed width).
	lines = endpointLines(c, 66, 6, 0)
	if got := strings.Join(strings.Fields(lines[1]), " "); got != "ADDRESS FLAGS RQ_ACTIVE RQ_ERR CX_ACTIVE" {
		t.Fatalf("w=66 columns: %q", got)
	}
	if !strings.Contains(lines[2], "FAILED_OUTLIER_CHECK") {
		t.Fatalf("flag cut at w=66: %q", lines[2])
	}
}

func TestHealthyOKFlagIsDim(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(prev)

	got := flagCell(nil, 6)
	if !strings.Contains(got, dimStyle.Render("OK")) || strings.Contains(got, okStyle.Render("OK")) {
		t.Fatalf("OK must be dim, not green (spec 5.6): %q", got)
	}
}
