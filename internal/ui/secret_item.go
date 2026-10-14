package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/envoyadmin/internal/snapshot"
)

const secretVersionW = 7

type secretItem struct {
	s   snapshot.Secret
	now time.Time
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

func (it secretItem) Overview(w, h, scroll int) string {
	return strings.Repeat("\n", max(h-1, 0)) // filled in by the Overview task
}

func (it secretItem) OverviewMaxScroll(w, h int) int { return 0 }
