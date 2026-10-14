package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/envoyadmin/internal/snapshot"
)

func sec(name, version string, state snapshot.SecretStatus) snapshot.Secret {
	return snapshot.Secret{
		Name: name, VersionInfo: version, State: state, Kind: "tls_certificate",
		LastUpdated: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC),
	}
}

func secItem0(s snapshot.Secret) resourceItem { return secretItems([]snapshot.Secret{s}, fixedNow)[0] }

func TestSecretItemIdentity(t *testing.T) {
	it := secItem0(sec("server-cert", "12", snapshot.SecretActive))
	if it.Key() != "server-cert|active" || it.Title() != "server-cert" || it.OverviewName() != "Overview" || it.FilterText() != "server-cert" {
		t.Fatalf("%q %q %q %q", it.Key(), it.Title(), it.OverviewName(), it.FilterText())
	}
	if _, problem := it.Config().Render(); problem != "secret content is never displayed" {
		t.Fatalf("config note = %q", problem)
	}
}

func TestSecretRowGlyphsAndLayout(t *testing.T) {
	cases := []struct {
		state snapshot.SecretStatus
		glyph string
	}{{snapshot.SecretActive, "●"}, {snapshot.SecretStatic, "●"}, {snapshot.SecretWarming, "◐"}}
	for _, c := range cases {
		row := ansiStrip(secItem0(sec("server-cert", "12", c.state)).Row(rowCtx{Width: 36}))
		if !strings.HasPrefix(row, " "+c.glyph+" server-cert") || !strings.HasSuffix(row, "12") || lipgloss.Width(row) != 36 {
			t.Fatalf("%v: %q (width %d)", c.state, row, lipgloss.Width(row))
		}
	}
	expired := sec("old", "1", snapshot.SecretActive)
	expired.Cert = &snapshot.CertInfo{NotAfter: fixedNow.Add(-time.Hour)}
	if row := ansiStrip(secItem0(expired).Row(rowCtx{Width: 36})); !strings.HasPrefix(row, " ✗ old") {
		t.Fatalf("expired cert shows ✗: %q", row)
	}
}

func TestSecretRowNeverExceedsTinyPanes(t *testing.T) {
	long := sec(strings.Repeat("n", 80), "9999999", snapshot.SecretWarming)
	for _, w := range []int{1, 5, 12, 28, 36} {
		if got := lipgloss.Width(secItem0(long).Row(rowCtx{Width: w})); got > w {
			t.Fatalf("width %d: row is %d wide", w, got)
		}
	}
}

func TestSecretsResourceChrome(t *testing.T) {
	r := resources[kindSecrets]
	s := snapshot.Snapshot{FetchedAt: fixedNow, Secrets: []snapshot.Secret{sec("a", "1", snapshot.SecretWarming), sec("b", "1", snapshot.SecretActive)}}
	if r.tabLabel(s) != "Secrets (!)" || !r.warn(s) {
		t.Fatalf("warming secret must warn: %q", r.tabLabel(s))
	}
	if got := r.summary(s); got != "2 secrets · 1 need attention" {
		t.Fatalf("summary = %q", got)
	}
	s.Secrets = s.Secrets[1:]
	if r.tabLabel(s) != "Secrets (1)" || r.warn(s) {
		t.Fatalf("healthy label = %q", r.tabLabel(s))
	}
	s.SecretsErr = "secrets unavailable: boom"
	if r.tabLabel(s) != "Secrets (!)" || r.summary(s) != "secrets unavailable" || r.notice(s) != s.SecretsErr {
		t.Fatalf("failed dump: %q %q %q", r.tabLabel(s), r.summary(s), r.notice(s))
	}
}

func TestTabCyclesThroughThreeKinds(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snap(cl("alpha", 1, 1))})
	for i, want := range []resourceKind{kindListeners, kindSecrets, kindClusters} {
		m = send(m, key("tab"))
		if m.active != want {
			t.Fatalf("tab %d: active = %v, want %v", i+1, m.active, want)
		}
	}
	m = send(m, key("shift+tab"))
	if m.active != kindSecrets {
		t.Fatalf("shift+tab from Clusters = %v", m.active)
	}
}

func TestSecretKeysAreUniqueForRotatingSecrets(t *testing.T) {
	a := secItem0(sec("server-cert", "12", snapshot.SecretActive))
	w := secItem0(sec("server-cert", "13", snapshot.SecretWarming))
	if a.Key() == w.Key() {
		t.Fatalf("active and warming share key %q", a.Key())
	}
	if a.Title() != w.Title() || a.FilterText() != "server-cert" {
		t.Fatalf("title/filter must stay the plain name: %q %q", a.Title(), a.FilterText())
	}
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snapshot.Snapshot{
		FetchedAt: fixedNow,
		Secrets: []snapshot.Secret{
			sec("server-cert", "12", snapshot.SecretActive),
			sec("server-cert", "13", snapshot.SecretWarming),
		},
	}})
	m = send(m, key("tab"))
	m = send(m, key("tab"))
	out := ansiStrip(m.View())
	if strings.Count(out, "server-cert") < 2 {
		t.Fatalf("both rows must be listed:\n%s", out)
	}
	m = send(m, key("down"))
	sel, ok := m.views[kindSecrets].list.Selected()
	if !ok || sel.Key() != "server-cert|warming" {
		t.Fatalf("selection after down = %v %v", sel, ok)
	}
}
