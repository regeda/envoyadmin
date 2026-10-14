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
	minAddrW      = 7 // header "ADDRESS"
	maxAddrW      = 26
	weightW       = 6
	zoneW         = 12
	rqActiveW     = 9
	rqErrW        = 6
	cxActiveW     = 9
	minFlagsW     = 14 // below this the ZONE column is dropped
	numericFixedW = weightW + rqActiveW + rqErrW + cxActiveW
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

func digits(n int) int { return len(strconv.Itoa(n)) }

// flagCell renders flags into a cell exactly width cells wide. When not all
// flags fit it ends with a "+N" marker, whose room is reserved before each flag
// is accepted; a first flag that is too wide is truncated rather than dropped.
func flagCell(flags []string, width int) string {
	if width <= 0 {
		return ""
	}
	if len(flags) == 0 {
		return pad(okStyle.Render(trunc("OK", width)), width)
	}
	var parts []string
	used := 0
	for i, f := range flags {
		sep := 0
		if used > 0 {
			sep = 1
		}
		remaining := len(flags) - i - 1
		reserve := 0
		if remaining > 0 {
			reserve = 2 + digits(remaining) // " +N"
		}
		if used+sep+lipgloss.Width(f)+reserve <= width {
			parts = append(parts, flagStyle(f).Render(f))
			used += sep + lipgloss.Width(f)
			continue
		}
		if i == 0 {
			room := width - reserve
			if room < 1 { // no space for a marker beside even one cell of the flag
				room, remaining = width, 0
			}
			parts = append(parts, flagStyle(f).Render(trunc(f, room)))
		} else {
			remaining = len(flags) - i
		}
		if remaining > 0 {
			parts = append(parts, dimStyle.Render("+"+strconv.Itoa(remaining)))
		}
		break
	}
	return pad(strings.Join(parts, " "), width)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// endpointLines builds the h lines of the table without clipping. The layout
// is sized so that no line is wider than w (barring absurdly small widths).
func endpointLines(c snapshot.Cluster, w, h, scroll int) []string {
	hosts := sortedHosts(c.Hosts)
	addrW := minAddrW
	for _, hst := range hosts {
		addrW = max(addrW, min(lipgloss.Width(hst.Address), maxAddrW))
	}
	// columns: ADDRESS FLAGS WEIGHT [ZONE] RQ_ACTIVE RQ_ERR CX_ACTIVE
	showZone := true
	flagsW := w - (addrW + zoneW + numericFixedW + 6)
	if flagsW < minFlagsW {
		showZone = false
		flagsW = w - (addrW + numericFixedW + 5)
	}
	flagsW = max(flagsW, 1)

	row := func(addr, flags, weight, zone, rqa, rqe, cxa string) string {
		cells := []string{pad(trunc(addr, addrW), addrW), flags, pad(weight, weightW)}
		if showZone {
			cells = append(cells, pad(trunc(zone, zoneW), zoneW))
		}
		cells = append(cells, pad(rqa, rqActiveW), pad(rqe, rqErrW), pad(cxa, cxActiveW))
		return strings.Join(cells, " ")
	}

	summary := fmt.Sprintf("%d hosts · %d healthy · %d ejected", len(c.Hosts), c.HealthyCount(), c.EjectedCount())
	header := dimStyle.Render(row("ADDRESS", pad("FLAGS", flagsW), "WEIGHT", "ZONE", "RQ_ACTIVE", "RQ_ERR", "CX_ACTIVE"))

	lines := []string{summary, header}
	if len(hosts) == 0 {
		lines = append(lines, dimStyle.Render("no hosts"))
	}
	scroll = max(0, min(scroll, maxEndpointScroll(c, h)))
	end := min(scroll+max(h-2, 0), len(hosts))
	for _, hst := range hosts[min(scroll, len(hosts)):end] {
		lines = append(lines, row(hst.Address, flagCell(hst.Flags, flagsW),
			strconv.FormatUint(uint64(hst.Weight), 10), orDash(hst.Zone),
			strconv.FormatUint(hst.RqActive, 10), strconv.FormatUint(hst.RqError, 10),
			strconv.FormatUint(hst.CxActive, 10)))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines[:h]
}

func renderEndpoints(c snapshot.Cluster, w, h int, scroll int) string {
	if h <= 0 || w <= 0 {
		return ""
	}
	lines := endpointLines(c, w, h, scroll)
	style := lipgloss.NewStyle().MaxWidth(w)
	for i, l := range lines {
		lines[i] = style.Render(l)
	}
	return strings.Join(lines, "\n")
}
