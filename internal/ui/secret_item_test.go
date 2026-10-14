package ui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
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
	if got := r.summary(s); got != "2 secrets · 1 to check" {
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

func TestSecretOverviewShowsFactsAndUsedBy(t *testing.T) {
	s := sec("server-cert", "12", snapshot.SecretActive)
	s.Cert = &snapshot.CertInfo{Serial: "0a1b", SANs: []string{"a.example.com", "b.example.com"}, NotAfter: fixedNow.Add(7 * 24 * time.Hour), DaysLeft: 7}
	s.UsedBy = []snapshot.Ref{{Kind: "cluster", Name: "api"}, {Kind: "listener", Name: "0.0.0.0_8080"}}
	out := ansiStrip(secItem0(s).Overview(80, 20, 0))
	for _, want := range []string{
		"State", "active", "Kind", "tls_certificate", "Version", "12", "Last updated", "2026-10-14 10:00:00Z",
		"Serial", "0a1b", "SANs", "a.example.com, b.example.com", "Expires", "2026-10-21", "7 days",
		"Used by (2)", "cluster api", "listener 0.0.0.0_8080",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if lines := strings.Split(out, "\n"); len(lines) != 20 {
		t.Fatalf("Overview must be exactly h lines, got %d", len(lines))
	}
}

func TestSecretOverviewWithoutCertOrUsers(t *testing.T) {
	out := ansiStrip(secItem0(sec("bare", "", snapshot.SecretStatic)).Overview(80, 12, 0))
	if strings.Contains(out, "Serial") || !strings.Contains(out, "Used by (0)") || !strings.Contains(out, "not referenced") {
		t.Fatalf("bare secret:\n%s", out)
	}
	if !strings.Contains(out, "static") {
		t.Fatalf("static state:\n%s", out)
	}
}

func TestSecretOverviewExpiredCert(t *testing.T) {
	s := sec("old", "1", snapshot.SecretActive)
	s.Cert = &snapshot.CertInfo{Serial: "02", NotAfter: fixedNow.Add(-time.Hour)}
	if out := ansiStrip(secItem0(s).Overview(80, 12, 0)); !strings.Contains(out, "(expired)") {
		t.Fatalf("expired cert:\n%s", out)
	}
}

func TestSecretOverviewExpiresWithinTheDayReadsToday(t *testing.T) {
	s := sec("soon", "1", snapshot.SecretActive)
	s.Cert = &snapshot.CertInfo{Serial: "03", NotAfter: fixedNow.Add(3 * time.Hour), DaysLeft: 0}
	out := ansiStrip(secItem0(s).Overview(80, 12, 0))
	if !strings.Contains(out, "(today)") || strings.Contains(out, "0 days") || strings.Contains(out, "(expired)") {
		t.Fatalf("expires today:\n%s", out)
	}
}

func TestSecretOverviewUnknownExpiryIsDash(t *testing.T) {
	s := sec("noexp", "1", snapshot.SecretActive)
	s.Cert = &snapshot.CertInfo{Serial: "04"}
	out := ansiStrip(secItem0(s).Overview(80, 12, 0))
	if !regexp.MustCompile(`Expires\s+-\s*(\n|$)`).MatchString(out) || strings.Contains(out, "expired") || strings.Contains(out, "days") {
		t.Fatalf("zero NotAfter:\n%s", out)
	}
}

func TestSecretOverviewScrollsUsedBy(t *testing.T) {
	s := sec("busy", "1", snapshot.SecretActive)
	for i := 0; i < 30; i++ {
		s.UsedBy = append(s.UsedBy, snapshot.Ref{Kind: "cluster", Name: fmt.Sprintf("c%02d", i)})
	}
	it := secItem0(s)
	if it.OverviewMaxScroll(80, 12) == 0 {
		t.Fatal("30 users in 12 rows must scroll")
	}
	last := ansiStrip(it.Overview(80, 12, it.OverviewMaxScroll(80, 12)))
	if !strings.Contains(last, "c29") || strings.Contains(last, "c00") {
		t.Fatalf("scrolled to the end:\n%s", last)
	}
}

func TestSecretConfigTabSaysContentIsNeverDisplayed(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snapshot.Snapshot{FetchedAt: fixedNow, Secrets: []snapshot.Secret{sec("server-cert", "12", snapshot.SecretActive)}}})
	m = send(send(m, key("tab")), key("tab"))
	for _, k := range []string{"o", "c"} {
		m = send(m, key(k))
	}
	if v := m.View(); !strings.Contains(v, "secret content is never displayed") {
		t.Fatalf("Config tab note missing:\n%s", v)
	}
}

func TestSecretOverviewHasNoKeyMaterial(t *testing.T) {
	s := sec("server-cert", "12", snapshot.SecretActive)
	s.Cert = &snapshot.CertInfo{Serial: "0a1b", SANs: []string{"a.example.com"}, NotAfter: fixedNow.Add(24 * time.Hour), DaysLeft: 1}
	s.UsedBy = []snapshot.Ref{{Kind: "cluster", Name: "api"}}
	out := secItem0(s).Overview(100, 20, 0)
	if strings.Contains(out, "BEGIN") || strings.Contains(out, "PRIVATE KEY") {
		t.Fatalf("overview leaked content:\n%s", out)
	}
}

// The snapshot types must have no field that could hold a secret body: only
// plain scalars, time, and the allowed nested shapes.
func TestSecretSnapshotTypesCannotCarryContent(t *testing.T) {
	var walk func(path string, ty reflect.Type)
	walk = func(path string, ty reflect.Type) {
		if ty == reflect.TypeOf(time.Time{}) {
			return
		}
		if ty == reflect.TypeOf(json.RawMessage(nil)) {
			t.Errorf("%s is json.RawMessage", path)
			return
		}
		switch ty.Kind() {
		case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		case reflect.Pointer:
			walk(path+"*", ty.Elem())
		case reflect.Slice:
			if ty.Elem().Kind() == reflect.Uint8 {
				t.Errorf("%s is a byte slice", path)
				return
			}
			walk(path+"[]", ty.Elem())
		case reflect.Struct:
			for i := 0; i < ty.NumField(); i++ {
				f := ty.Field(i)
				walk(path+"."+f.Name, f.Type)
			}
		default: // map, interface, array, chan, func, ...
			t.Errorf("%s has kind %s, which could hold a secret body", path, ty.Kind())
		}
	}
	walk("Secret", reflect.TypeOf(snapshot.Secret{}))
	walk("CertInfo", reflect.TypeOf(snapshot.CertInfo{}))
}

func TestSecretOverviewSmallSizes(t *testing.T) {
	s := sec("busy", "1", snapshot.SecretActive)
	s.Cert = &snapshot.CertInfo{Serial: "0a", SANs: []string{"a.example.com"}, NotAfter: fixedNow.Add(48 * time.Hour), DaysLeft: 2}
	s.UsedBy = []snapshot.Ref{{Kind: "cluster", Name: "api"}}
	it := secItem0(s)
	for _, h := range []int{1, 3, 5} { // h <= sticky length
		if n := len(strings.Split(it.Overview(80, h, 0), "\n")); n != h {
			t.Fatalf("h=%d: got %d lines", h, n)
		}
	}
	for _, w := range []int{1, 3, 5} {
		out := ansiStrip(it.Overview(w, 12, 0))
		lines := strings.Split(out, "\n")
		if len(lines) != 12 {
			t.Fatalf("w=%d: got %d lines", w, len(lines))
		}
		for _, l := range lines {
			if lipgloss.Width(l) > w {
				t.Fatalf("w=%d: line %q too wide", w, l)
			}
		}
	}
}
