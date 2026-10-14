package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/snapshot"
)

const (
	addrW   = 26
	weightW = 6
	zoneW   = 12
	numW    = 9
)

func sortedHosts(hs []snapshot.Host) []snapshot.Host {
	out := append([]snapshot.Host(nil), hs...)
	sort.SliceStable(out, func(i, j int) bool {
		if hi, hj := out[i].Healthy(), out[j].Healthy(); hi != hj {
			return !hi
		}
		return out[i].Address < out[j].Address
	})
	return out
}

func maxEndpointScroll(c snapshot.Cluster, h int) int {
	return max(len(c.Hosts)-max(h-2, 0), 0)
}

func flagStyle(f string) lipgloss.Style {
	if snapshot.IsUnhealthyFlag(f) {
		return badStyle
	}
	return warnStyle
}

// flagCell renders flags to exactly width cells where possible, adding +N when
// they do not all fit.
func flagCell(flags []string, width int) string {
	if len(flags) == 0 {
		return pad(okStyle.Render("OK"), width)
	}
	var parts []string
	used := 0
	for i, f := range flags {
		need := lipgloss.Width(f)
		if used > 0 {
			need++
		}
		if used+need > width {
			parts = append(parts, dimStyle.Render(fmt.Sprintf("+%d", len(flags)-i)))
			used += 3
			break
		}
		parts = append(parts, flagStyle(f).Render(f))
		used += need
	}
	return pad(strings.Join(parts, " "), width)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func renderEndpoints(c snapshot.Cluster, w, h, scroll int) string {
	if h <= 0 || w <= 0 {
		return ""
	}
	flagsW := max(w-(addrW+weightW+zoneW+3*numW+6), 12)

	ejected := c.EjectedCount()
	summary := fmt.Sprintf("%d hosts · %d healthy · %d ejected", len(c.Hosts), c.HealthyCount(), ejected)
	header := dimStyle.Render(strings.Join([]string{
		pad("ADDRESS", addrW), pad("FLAGS", flagsW), pad("WEIGHT", weightW), pad("ZONE", zoneW),
		pad("RQ_ACTIVE", numW), pad("RQ_ERR", numW), pad("CX_ACTIVE", numW),
	}, " "))

	lines := []string{summary, header}
	hosts := sortedHosts(c.Hosts)
	if len(hosts) == 0 {
		lines = append(lines, dimStyle.Render("no hosts"))
	}
	scroll = max(0, min(scroll, maxEndpointScroll(c, h)))
	end := min(scroll+max(h-2, 0), len(hosts))
	for _, hst := range hosts[min(scroll, len(hosts)):end] {
		lines = append(lines, strings.Join([]string{
			pad(trunc(hst.Address, addrW), addrW),
			flagCell(hst.Flags, flagsW),
			pad(strconv.FormatUint(uint64(hst.Weight), 10), weightW),
			pad(trunc(orDash(hst.Zone), zoneW), zoneW),
			pad(strconv.FormatUint(hst.RqActive, 10), numW),
			pad(strconv.FormatUint(hst.RqError, 10), numW),
			pad(strconv.FormatUint(hst.CxActive, 10), numW),
		}, " "))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	style := lipgloss.NewStyle().MaxWidth(w)
	for i, l := range lines[:h] {
		lines[i] = style.Render(l)
	}
	return strings.Join(lines[:h], "\n")
}
