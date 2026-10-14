package ui

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest"

	"github.com/regeda/txenvoy/internal/admin"
	"github.com/regeda/txenvoy/internal/config"
	"github.com/regeda/txenvoy/internal/demo"
	"github.com/regeda/txenvoy/internal/snapshot"
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
	waitFor(t, tm, "✗ disconnected", "503")

	fake.Inject(demo.Fault{})
	waitFor(t, tm, "● connected")

	tm.Send(key("q"))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}
