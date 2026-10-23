package ui

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/regeda/envoyadmin/internal/snapshot"
)

const secretVersionW = 7

type secretItem struct {
	now time.Time
	s   snapshot.Secret
}

func secretItems(ss []snapshot.Secret, now time.Time) []resourceItem {
	out := make([]resourceItem, len(ss))
	for i, s := range ss {
		out[i] = secretItem{s: s, now: now}
	}
	return out
}

// Key includes the state: during rotation Envoy reports one name twice
// (active and warming), and list identity must stay unique and stable.
func (it secretItem) Key() string          { return it.s.Name + "|" + it.s.State.String() }
func (it secretItem) FilterText() string   { return it.s.Name }
func (it secretItem) Title() string        { return it.s.Name }
func (it secretItem) OverviewName() string { return "Overview" }

// Config never shows content: the Config tab only says so.
func (it secretItem) Config() snapshot.ConfigInfo {
	return snapshot.ConfigInfo{Err: "secret content is never displayed"}
}

func (it secretItem) expired() bool { return it.s.Cert != nil && it.s.Cert.Expired(it.now) }

// secretGlyph carries the state without relying on colour.
func (it secretItem) glyph() (string, lipgloss.Style) {
	switch {
	case it.expired():
		return "✗", badStyle
	case it.s.State == snapshot.SecretWarming:
		return "◐", warnStyle
	default:
		return "●", okStyle
	}
}

func (it secretItem) Row(ctx rowCtx) string {
	glyph, style := it.glyph()
	avail := max(ctx.Width-3, 0) // prefix, glyph, space
	verW := max(min(lipgloss.Width(it.s.VersionInfo), secretVersionW, avail-nameMinW-1), 0)
	nameW := avail - verW
	if verW > 0 {
		nameW-- // gap
	}
	nameW = max(nameW, 0)

	plainName := trunc(it.s.Name, nameW)
	name := styledText(plainName, ctx.Hits, ctx.Cursor) + strings.Repeat(" ", max(nameW-lipgloss.Width(plainName), 0))
	line := cursorGlyph(ctx) + style.Render(glyph) + " " + name
	if verW > 0 {
		line += " " + dimStyle.Render(pad(trunc(it.s.VersionInfo, verW), verW))
	}
	return trunc(line, ctx.Width)
}

const notLoadedMarker = "(not loaded)"

// secretUseLine is a secret a cluster or listener references; one Envoy has not
// loaded carries a leading marker, since that is the out-of-sync case, and
// truncation must cut the name rather than the marker.
func secretUseLine(u snapshot.SecretUse) string {
	if u.Loaded {
		return u.Name
	}
	return badStyle.Render(notLoadedMarker) + " " + u.Name
}

// secretUsesRows are the "Secrets" rows shared by cluster and listener
// overviews: one row per secret, unloaded first, nil when nothing is
// referenced. The row count never depends on w. When the value column is too
// narrow to hold the marker and some of the name, the label column is dropped
// so the marker stays visible.
func secretUsesRows(uses []snapshot.SecretUse, w int) []string {
	if len(uses) == 0 {
		return nil
	}
	sorted := append([]snapshot.SecretUse(nil), uses...)
	sort.SliceStable(sorted, func(i, j int) bool { return !sorted[i].Loaded && sorted[j].Loaded })
	compact := w < overviewLabelW+len(notLoadedMarker)+2
	rows := make([]string, len(sorted))
	for i, u := range sorted {
		switch {
		case compact:
			rows[i] = secretUseLine(u)
		case i == 0:
			rows[i] = prop("Secrets", secretUseLine(u))
		default:
			rows[i] = prop("", secretUseLine(u))
		}
	}
	return rows
}

func (it secretItem) sticky() []string {
	s := it.s
	state := s.State.String()
	_, stStyle := it.glyph()
	updated := "-"
	if !s.LastUpdated.IsZero() {
		updated = s.LastUpdated.UTC().Format("2006-01-02 15:04:05Z")
	}
	lines := []string{
		prop("State", stStyle.Render(state)),
		prop("Kind", orDash(s.Kind)),
		prop("Version", orDash(s.VersionInfo)),
		prop("Last updated", updated),
	}
	if c := s.Cert; c != nil {
		lines = append(lines, prop("Serial", orDash(c.Serial)))
		if len(c.SANs) > 0 {
			lines = append(lines, prop("SANs", strings.Join(c.SANs, ", ")))
		}
		exp := "-"
		if !c.NotAfter.IsZero() {
			exp = c.NotAfter.UTC().Format("2006-01-02")
			switch {
			case it.expired():
				exp = badStyle.Render(exp + " (expired)")
			case c.DaysLeft <= 0:
				exp += " (today)"
			default:
				exp += " (" + plural(int(c.DaysLeft), "day") + ")"
			}
		}
		lines = append(lines, prop("Expires", exp))
	}
	return append(lines, "", boldStyle.Render("Used by ("+strconv.Itoa(len(s.UsedBy))+")"))
}

func (it secretItem) usedByLines() []string {
	if len(it.s.UsedBy) == 0 {
		return []string{" " + dimStyle.Render("not referenced by any cluster or listener")}
	}
	out := make([]string, 0, len(it.s.UsedBy))
	for _, r := range it.s.UsedBy {
		out = append(out, " "+r.Kind+" "+r.Name)
	}
	return out
}

func (it secretItem) OverviewMaxScroll(_, h int) int {
	rows := h - len(it.sticky())
	if rows <= 0 {
		return 0
	}
	return max(len(it.usedByLines())-rows, 0)
}

func (it secretItem) Overview(w, h, scroll int) string {
	if h <= 0 {
		return ""
	}
	if w <= 0 {
		return strings.Repeat("\n", h-1)
	}
	sticky := it.sticky()
	var lines []string
	if h <= len(sticky) {
		lines = sticky[:h]
	} else {
		lines = append(lines, sticky...)
		users := it.usedByLines()
		rows := h - len(sticky)
		start := max(0, min(scroll, max(len(users)-rows, 0)))
		lines = append(lines, users[start:min(start+rows, len(users))]...)
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = truncEllipsis(l, w)
	}
	return strings.Join(lines, "\n")
}
