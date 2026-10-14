package ui

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"

	"github.com/regeda/envoyadmin/internal/admin"
	"github.com/regeda/envoyadmin/internal/demo"
	"github.com/regeda/envoyadmin/internal/snapshot"
)

func init() {
	// Deterministic output: no colors in goldens. Tests that need ANSI set
	// their own profile and restore this one.
	lipgloss.SetColorProfile(termenv.Ascii)
}

// goldenOpts is testOpts with a realistic refresh so banners read as real ones.
func goldenOpts() Options {
	o := testOpts(nil)
	o.Config.Refresh = 3 * time.Second
	return o
}

func fetchSnap(t *testing.T, sc demo.Scenario) snapshot.Snapshot {
	t.Helper()
	srv := httptest.NewServer(demo.NewServer(sc))
	t.Cleanup(srv.Close)
	c, err := admin.NewHTTP(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	s, err := snapshot.Fetch(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	s.FetchedAt = fixedNow
	return s
}

func loaded(t *testing.T, sc demo.Scenario, w, h int) Model {
	t.Helper()
	m := sized(New(goldenOpts()), w, h)
	return send(m, snapshotMsg{snap: fetchSnap(t, sc)})
}

func typeText(m Model, s string) Model {
	for _, r := range s {
		m = send(m, key(string(r)))
	}
	return m
}

func golden(t *testing.T, m Model) {
	t.Helper()
	teatest.RequireEqualOutput(t, []byte(m.View()))
}

func TestGoldenHealthy(t *testing.T)  { golden(t, loaded(t, demo.Healthy(), 120, 30)) }
func TestGoldenDegraded(t *testing.T) { golden(t, loaded(t, demo.Degraded(), 120, 30)) }
func TestGoldenCompact(t *testing.T)  { golden(t, loaded(t, demo.Degraded(), 100, 20)) }

func TestGoldenFiltered(t *testing.T) {
	m := loaded(t, demo.Big(40), 120, 30)
	m = typeText(send(m, key("/")), "svc-01")
	golden(t, send(m, key("enter")))
}

func TestGoldenConfigTab(t *testing.T) {
	m := loaded(t, demo.Degraded(), 120, 30)
	golden(t, send(m, key("c")))
}

func TestGoldenConfigTabStatic(t *testing.T) {
	m := loaded(t, demo.Degraded(), 120, 30)
	for _, r := range "jjjjj" { // down to xds_cluster (static)
		m = send(m, key(string(r)))
	}
	golden(t, send(m, key("c")))
}

func TestGoldenConnecting(t *testing.T) { golden(t, sized(New(goldenOpts()), 120, 30)) }

func TestGoldenErrorFirstFetch(t *testing.T) {
	m := sized(New(goldenOpts()), 120, 30)
	golden(t, send(m, snapshotMsg{err: errors.New("GET /clusters: dial tcp 127.0.0.1:15000: connect: connection refused")}))
}

func TestGoldenErrorShort(t *testing.T) {
	m := loaded(t, demo.Degraded(), 120, 30)
	golden(t, send(m, snapshotMsg{err: errors.New("GET /clusters: 503 Service Unavailable")}))
}

func TestGoldenErrorWrapped(t *testing.T) {
	m := loaded(t, demo.Degraded(), 90, 30)
	golden(t, send(m, snapshotMsg{err: errors.New(
		"GET /clusters: Get \"http://localhost:15000/clusters?format=json\": dial tcp 127.0.0.1:15000: connect: connection refused")}))
}

func TestGoldenErrorTruncated(t *testing.T) {
	m := loaded(t, demo.Degraded(), 80, 30)
	golden(t, send(m, snapshotMsg{err: errors.New(strings.Repeat("decode failure in a very long message ", 20))}))
}

func TestGoldenDetailFocus(t *testing.T) {
	m := loaded(t, demo.Degraded(), 120, 30)
	golden(t, send(m, key("enter")))
}

func TestGoldenListenersList(t *testing.T) {
	golden(t, send(loaded(t, demo.Degraded(), 120, 30), key("tab")))
}

func TestGoldenListenerConfig(t *testing.T) {
	m := send(loaded(t, demo.Degraded(), 120, 30), key("tab"))
	golden(t, send(m, key("c")))
}

func TestGoldenListenerDetailFocus(t *testing.T) {
	m := send(loaded(t, demo.Degraded(), 120, 30), key("tab"))
	golden(t, send(m, key("enter")))
}

func TestGoldenListenerFiltered(t *testing.T) {
	m := send(loaded(t, demo.Degraded(), 120, 30), key("tab"))
	m = typeText(send(m, key("/")), "150")
	golden(t, send(m, key("enter")))
}

func TestGoldenListenerActiveWithChains(t *testing.T) {
	m := send(loaded(t, demo.Degraded(), 120, 30), key("tab"))
	m = typeText(send(m, key("/")), "virtualInbound")
	golden(t, send(m, key("enter")))
}

func TestGoldenListenersFailedInline(t *testing.T) {
	m := sized(New(goldenOpts()), 120, 30)
	s := fetchSnap(t, demo.Degraded())
	s.Listeners = nil
	s.ListenersErr = "listeners unavailable: GET /config_dump: 503 Service Unavailable"
	m = send(m, snapshotMsg{snap: s})
	golden(t, send(m, key("tab")))
}

func TestGoldenSecretsList(t *testing.T) {
	m := loaded(t, demo.Degraded(), 120, 30)
	golden(t, send(send(m, key("tab")), key("tab")))
}

func TestGoldenSecretOverview(t *testing.T) {
	m := loaded(t, demo.Degraded(), 120, 30)
	m = send(send(m, key("tab")), key("tab"))
	golden(t, send(m, key("enter")))
}

func TestGoldenSecretsFailedInline(t *testing.T) {
	m := sized(New(goldenOpts()), 120, 30)
	s := fetchSnap(t, demo.Degraded())
	s.Secrets, s.SecretsErr = nil, "secrets unavailable: GET /config_dump: 500 Internal Server Error"
	m = send(send(send(m, snapshotMsg{snap: s}), key("tab")), key("tab"))
	golden(t, m)
}

// The active server-cert row is the one the api-gateway cluster and the
// 0.0.0.0_8080 listener use; its Overview carries the certificate facts.
func activeServerCertOverview(t *testing.T) Model {
	t.Helper()
	m := send(send(loaded(t, demo.Degraded(), 120, 30), key("tab")), key("tab"))
	m = typeText(send(m, key("/")), "server-cert")
	m = send(m, key("enter"))
	m = send(m, key("down")) // warming v13 first, then active v12
	return send(m, key("enter"))
}

func TestGoldenSecretOverviewActiveCert(t *testing.T) {
	golden(t, activeServerCertOverview(t))
}

func TestSecretOverviewActiveCertListsUsers(t *testing.T) {
	v := activeServerCertOverview(t).View()
	for _, want := range []string{"Used by", "api-gateway", "0.0.0.0_8080", "a1b2c", "api.example.com", "Expires       2026-12-15"} {
		if !strings.Contains(v, want) {
			t.Fatalf("overview of the active server-cert lacks %q:\n%s", want, v)
		}
	}
}
