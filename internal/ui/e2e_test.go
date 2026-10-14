package ui

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest"

	"github.com/regeda/envoyadmin/internal/admin"
	"github.com/regeda/envoyadmin/internal/config"
	"github.com/regeda/envoyadmin/internal/demo"
	"github.com/regeda/envoyadmin/internal/snapshot"
)

// waitFor consumes the program output until every want string has appeared.
// Output is consumed as it is read, so strings that render in the same frame
// must be waited for in a single call.
func waitFor(t *testing.T, tm *teatest.TestModel, want ...string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		for _, w := range want {
			if !bytes.Contains(b, []byte(w)) {
				return false
			}
		}
		return true
	},
		teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(20*time.Millisecond))
}

// The real model runs against the fake server; the server fails and recovers.
func TestEndToEndFailureAndRecovery(t *testing.T) {
	fake := demo.NewServer(demo.Degraded())
	srv := httptest.NewServer(fake)
	defer srv.Close()

	cfg := config.Config{Admin: srv.URL, Refresh: 100 * time.Millisecond, Timeout: 200 * time.Millisecond}
	client, err := admin.NewHTTP(cfg.Admin, cfg.Timeout)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := New(Options{
		Ctx: ctx, Config: cfg, Color: false,
		Fetch: func(ctx context.Context) (snapshot.Snapshot, error) { return snapshot.Fetch(ctx, client) },
	})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 30))

	waitFor(t, tm, "● connected", "api-gateway")

	fake.Inject(demo.Fault{Status: 503})
	// Wait for the specific error text, not a bare "503": the admin URL shown
	// in the header carries a random port that may itself contain 503.
	waitFor(t, tm, "✗ disconnected", "503 Service Unavailable")

	fake.Inject(demo.Fault{})
	// The header reads "✗ disconnected" until a fetch succeeds, so the next
	// "● connected" can only come from the recovery.
	waitFor(t, tm, "● connected")

	tm.Send(key("q"))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))

	// Output is a diff stream, so a banner that was never cleared would leave
	// no trace in the frames after recovery. Assert on the final view instead.
	view := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second)).View()
	for _, stale := range []string{"Service Unavailable", "retrying every", "✗ disconnected"} {
		if strings.Contains(view, stale) {
			t.Fatalf("banner still shown after recovery (%q):\n%s", stale, view)
		}
	}
}

// A listener failure is isolated: no banner, the clusters keep working.
func TestEndToEndListenersTabAndIsolatedFailure(t *testing.T) {
	fake := demo.NewServer(demo.Degraded())
	srv := httptest.NewServer(fake)
	defer srv.Close()

	cfg := config.Config{Admin: srv.URL, Refresh: 100 * time.Millisecond, Timeout: 200 * time.Millisecond}
	client, err := admin.NewHTTP(cfg.Admin, cfg.Timeout)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := New(Options{
		Ctx: ctx, Config: cfg,
		Fetch: func(ctx context.Context) (snapshot.Snapshot, error) { return snapshot.Fetch(ctx, client) },
	})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 30))

	waitFor(t, tm, "Clusters (6)", "Listeners (7)", "Secrets (!)")
	tm.Send(key("tab"))
	waitFor(t, tm, "virtualInbound", "0.0.0.0:15006")

	// fail only the dynamic listener dump: no banner, an inline message instead
	fake.Inject(demo.Fault{Status: 503, Match: "dynamic_listeners"})
	waitFor(t, tm, "listeners unavailable", "Listeners (!)")
	tm.Send(key("tab")) // Secrets
	tm.Send(key("tab"))
	waitFor(t, tm, "api-gateway") // the Clusters tab still works

	fake.Inject(demo.Fault{})
	waitFor(t, tm, "Listeners (7)") // the failed label can only clear through a successful fetch
	tm.Send(key("tab"))             // Listeners
	waitFor(t, tm, "virtualInbound")

	tm.Send(key("q"))
	final := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second)).(Model)
	if final.bannerVisible() || final.err != nil {
		t.Fatalf("a listener failure must never raise the banner (err=%v)", final.err)
	}
}

// A secrets failure is isolated: no banner, the other tabs keep working.
func TestEndToEndSecretsTabAndIsolatedFailure(t *testing.T) {
	fake := demo.NewServer(demo.Degraded())
	srv := httptest.NewServer(fake)
	defer srv.Close()

	cfg := config.Config{Admin: srv.URL, Refresh: 100 * time.Millisecond, Timeout: 200 * time.Millisecond}
	client, err := admin.NewHTTP(cfg.Admin, cfg.Timeout)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := New(Options{
		Ctx: ctx, Config: cfg,
		Fetch: func(ctx context.Context) (snapshot.Snapshot, error) { return snapshot.Fetch(ctx, client) },
	})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 30))

	// the degraded demo has a warming and an expired secret, so the label warns
	waitFor(t, tm, "Clusters (6)", "Secrets (!)")
	tm.Send(key("tab")) // Listeners
	tm.Send(key("tab")) // Secrets
	waitFor(t, tm, "server-cert", "rotating-cert", "bootstrap-cert")

	fake.Inject(demo.Fault{Status: 500, Match: "secrets"})
	waitFor(t, tm, "secrets unavailable") // the label stays Secrets (!), so the diff stream does not repeat it

	fake.Inject(demo.Fault{})
	// "secrets unavailable" is only replaced by the list after a successful fetch
	waitFor(t, tm, "server-cert", "bootstrap-cert")

	tm.Send(key("q"))
	final := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second)).(Model)
	if final.bannerVisible() || final.err != nil {
		t.Fatalf("a secrets failure must never raise the banner (err=%v)", final.err)
	}
	if strings.Contains(final.View(), "disconnected") {
		t.Fatalf("a secrets failure must not show a disconnected state:\n%s", final.View())
	}
}
