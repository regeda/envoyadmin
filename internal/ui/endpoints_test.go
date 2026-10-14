package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/snapshot"
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
