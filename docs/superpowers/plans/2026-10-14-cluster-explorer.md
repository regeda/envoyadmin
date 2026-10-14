# txenvoy Cluster Explorer (v1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `txenvoy`, a Go terminal UI that shows Envoy clusters with live endpoint health, highlighted config YAML, an error banner and auto-refresh.

**Architecture:** Layered: `admin` (HTTP client behind an interface, protojson decode) → `snapshot` (plain domain types) → `ui` (bubbletea). A fake admin server in `internal/demo` backs tests and `--demo`. All I/O runs in `tea.Cmd`s.

**Tech Stack:** Go, cobra, bubbletea v1, bubbles, lipgloss, go-control-plane, `sigs.k8s.io/yaml`, chroma, `sahilm/fuzzy`, teatest.

**Spec:** `docs/superpowers/specs/2026-10-14-cluster-explorer-design.md`

## Deviations from the spec (applied to the spec in Task 1)

1. **Config fetch.** `Client.ConfigDump` is replaced by `Client.ClusterConfigs(ctx) ([]ClusterConfig, error)`. protojson fails on any `Any` whose type is not compiled in, and a cluster's config can contain such an `Any` (for example a `transport_socket` extension). So the config dump is walked as raw JSON. A cluster's YAML is made from Envoy's own protojson output with `yaml.JSONToYAML`, and nothing can fail on an unknown type. `/clusters` and `/server_info` still decode through go-control-plane types.
2. **Static clusters.** `ClusterConfigs` calls `/config_dump?resource=dynamic_active_clusters` and `/config_dump?resource=static_clusters` and merges the results. The first alone never contains static clusters.
3. **Table.** The endpoints table is hand-rolled. `bubbles/table` cannot hold styled cells without breaking width math.
4. **bubbletea major version.** The code targets bubbletea **v1** (`View() string`, `tea.KeyMsg`). v2 changes those signatures, so do not `go get` it.

## Global Constraints

- Module path: `github.com/regeda/txenvoy`.
- Dependency direction: `ui → snapshot → admin`. Only `admin` and `snapshot` import go-control-plane.
- `Update` and `View` perform no I/O. All I/O happens inside `tea.Cmd`s. Every fetch uses the context cancelled on quit.
- No timeout or interval is a constant. They come from `config.Config` (`--refresh`, `--timeout`). The only bound is `config.MinRefresh = 500ms`.
- CLI is cobra only: no viper.
- Flags: `--admin` (default `http://localhost:15000`), `--refresh` (default `3s`), `--timeout` (default `3s`), `--demo`. Subcommand `version`.
- Banner: at most 4 rows, full width, red background, drawn as an overlay that never moves the layout. Minimum terminal 80x12. Compact header below 24 rows.
- Refresh never overlaps: a tick starts a fetch only if none is in flight.
- All commits use Conventional Commits and end with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>` (pass it as a second `-m`).
- Makefile recipes are indented with a TAB.

## Review Focus

Inputs and conditions the spec implies but no feature test would naturally exercise. Each has a test in the owning task.

1. Zero clusters, or a filter that matches nothing: no panic, cursor and selection are empty, the detail pane says "no cluster selected" (Tasks 5, 7, 9).
2. The selected cluster disappears between refreshes: selection moves to the nearest row and the detail pane follows (Task 5).
3. A zero-size or tiny terminal, including `View()` before the first `WindowSizeMsg`: shows the "terminal too small" message and never panics (Task 9).
4. Hosts with a unix-socket (pipe) address, or with no locality or stats: render without panic, blank fields show `-` or `0` (Tasks 4, 6).
5. Very long error text and long or wide-character cluster names: the banner stays within 4 rows and the terminal width, and list rows never exceed the pane width (Tasks 5, 8).

---

## File structure

```
go.mod, go.sum, Makefile, README.md, .gitignore
cmd/txenvoy/main.go          entrypoint, version var, exit code
cmd/txenvoy/root.go          cobra root + version subcommand
cmd/txenvoy/run.go           wiring: client, fetcher, demo server, tea program
cmd/txenvoy/root_test.go
internal/config/config.go    Config, Default, Validate
internal/config/config_test.go
internal/demo/scenario.go    Scenario/Cluster/Host builders and JSON emitters
internal/demo/server.go      fake admin server, faults, Jitter
internal/demo/*_test.go
internal/admin/client.go     Client interface, ClusterConfig, HTTP impl
internal/admin/configdump.go raw config_dump walker
internal/admin/*_test.go
internal/snapshot/snapshot.go  domain types, Fetch
internal/snapshot/flags.go     health flag mapping
internal/snapshot/*_test.go
internal/ui/styles.go        colors and style helpers
internal/ui/text.go          trunc/pad/ago helpers
internal/ui/listpane.go      cluster list + fuzzy filter
internal/ui/endpoints.go     endpoints table renderer
internal/ui/detailpane.go    detail pane with Endpoints/Config tabs
internal/ui/highlight.go     chroma YAML highlighting
internal/ui/chrome.go        header, banner overlay, status bar
internal/ui/model.go         root model: state machine, layout, View
internal/ui/*_test.go, internal/ui/testdata/*.golden
```

---

### Task 1: Scaffold, config, cobra CLI, Makefile, spec sync

**Files:**
- Create: `go.mod`, `.gitignore`, `Makefile`, `README.md` (stub), `cmd/txenvoy/main.go`, `cmd/txenvoy/root.go`, `cmd/txenvoy/run.go`, `cmd/txenvoy/root_test.go`, `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `docs/superpowers/specs/2026-10-14-cluster-explorer-design.md`

**Interfaces:**
- Produces: `config.Config{Admin string; Refresh, Timeout time.Duration; Demo bool}`, `config.Default() Config`, `(Config) Validate() error`, `config.MinRefresh`. `newRootCmd(version string) *cobra.Command`. `run(ctx context.Context, cfg config.Config) error` (stub here, real in Task 11).

- [ ] **Step 1: Init the module and dependencies**

```bash
cd /Users/regeda/code/txenvoy
go mod init github.com/regeda/txenvoy
go get github.com/spf13/cobra@latest
```

- [ ] **Step 2: Write the failing config test**

`internal/config/config_test.go`:

```go
package config

import (
	"strings"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	ok := Default()
	tests := []struct {
		name    string
		mut     func(*Config)
		wantErr string
	}{
		{"defaults are valid", func(*Config) {}, ""},
		{"https admin", func(c *Config) { c.Admin = "https://envoy.local:9901" }, ""},
		{"admin without scheme", func(c *Config) { c.Admin = "localhost:15000" }, "--admin"},
		{"admin ftp", func(c *Config) { c.Admin = "ftp://x" }, "--admin"},
		{"admin empty", func(c *Config) { c.Admin = "" }, "--admin"},
		{"demo ignores admin", func(c *Config) { c.Demo = true; c.Admin = "garbage" }, ""},
		{"refresh at floor", func(c *Config) { c.Refresh = MinRefresh }, ""},
		{"refresh below floor", func(c *Config) { c.Refresh = 100 * time.Millisecond }, "--refresh"},
		{"timeout zero", func(c *Config) { c.Timeout = 0 }, "--timeout"},
		{"timeout longer than refresh is fine", func(c *Config) { c.Timeout = time.Minute }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ok
			tt.mut(&c)
			err := c.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 3: Run it and confirm it fails**

Run: `go test ./internal/config/...`
Expected: FAIL (`Default`, `Validate`, `MinRefresh` undefined).

- [ ] **Step 4: Implement config**

`internal/config/config.go`:

```go
// Package config holds the validated runtime configuration. Every duration in
// the program comes from here; nothing else hardcodes a timeout.
package config

import (
	"fmt"
	"net/url"
	"time"
)

// MinRefresh is the floor for --refresh so a typo cannot hammer Envoy.
const MinRefresh = 500 * time.Millisecond

type Config struct {
	Admin   string
	Refresh time.Duration
	Timeout time.Duration
	Demo    bool
}

func Default() Config {
	return Config{
		Admin:   "http://localhost:15000",
		Refresh: 3 * time.Second,
		Timeout: 3 * time.Second,
	}
}

func (c Config) Validate() error {
	if c.Refresh < MinRefresh {
		return fmt.Errorf("--refresh must be at least %s, got %s", MinRefresh, c.Refresh)
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("--timeout must be positive, got %s", c.Timeout)
	}
	if c.Demo {
		return nil
	}
	u, err := url.Parse(c.Admin)
	if err != nil {
		return fmt.Errorf("--admin: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("--admin must be an http(s) URL with a host, got %q", c.Admin)
	}
	return nil
}
```

- [ ] **Step 5: Run the config test**

Run: `go test ./internal/config/...`
Expected: PASS.

- [ ] **Step 6: Write the failing CLI test**

`cmd/txenvoy/root_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func execRoot(args ...string) (string, error) {
	cmd := newRootCmd("v-test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestVersion(t *testing.T) {
	out, err := execRoot("version")
	if err != nil || strings.TrimSpace(out) != "v-test" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestInvalidFlagsFailBeforeRun(t *testing.T) {
	for _, args := range [][]string{
		{"--refresh", "100ms"},
		{"--timeout", "0s"},
		{"--admin", "nope"},
	} {
		if _, err := execRoot(args...); err == nil {
			t.Fatalf("args %v: expected validation error", args)
		}
	}
}
```

- [ ] **Step 7: Run it and confirm it fails**

Run: `go test ./cmd/...`
Expected: FAIL (`newRootCmd` undefined).

- [ ] **Step 8: Implement the CLI**

`cmd/txenvoy/root.go`:

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/regeda/txenvoy/internal/config"
)

func newRootCmd(version string) *cobra.Command {
	cfg := config.Default()
	cmd := &cobra.Command{
		Use:           "txenvoy",
		Short:         "Real-time terminal UI for Envoy",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE:       func(*cobra.Command, []string) error { return cfg.Validate() },
		RunE:          func(cmd *cobra.Command, _ []string) error { return run(cmd.Context(), cfg) },
	}
	f := cmd.Flags()
	f.StringVar(&cfg.Admin, "admin", cfg.Admin, "Envoy admin base URL")
	f.DurationVar(&cfg.Refresh, "refresh", cfg.Refresh, "poll interval (minimum 500ms)")
	f.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "per-request timeout for each admin call")
	f.BoolVar(&cfg.Demo, "demo", false, "run against a built-in fake admin server")

	cmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, _ []string) { fmt.Fprintln(cmd.OutOrStdout(), version) },
	})
	return cmd
}
```

`cmd/txenvoy/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

func main() { os.Exit(realMain()) }

func realMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newRootCmd(version).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "txenvoy:", err)
		return 1
	}
	return 0
}
```

`cmd/txenvoy/run.go` (scaffold, replaced in Task 11):

```go
package main

import (
	"context"
	"errors"

	"github.com/regeda/txenvoy/internal/config"
)

func run(ctx context.Context, cfg config.Config) error {
	return errors.New("UI not wired yet")
}
```

- [ ] **Step 9: Run the CLI test**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 10: Add Makefile, .gitignore, README stub**

`.gitignore`:

```
bin/
coverage.out
```

`README.md`:

```markdown
# txenvoy

Real-time terminal UI for Envoy. See `docs/superpowers/specs/` for the design.
```

`Makefile` (recipes use a TAB):

```make
.DEFAULT_GOAL := help

BIN     := bin/txenvoy
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

GOLANGCI_LINT_VERSION ?= v2.1.6
GOLANGCI_LINT         := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: help build run test test-update lint fmt tidy cover clean check

help: ## List targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

build: ## Build bin/txenvoy
	go build $(LDFLAGS) -o $(BIN) ./cmd/txenvoy

run: build ## Build and run against the built-in demo server
	$(BIN) --demo

test: ## Run all tests with the race detector
	go test -race ./...

test-update: ## Regenerate teatest golden files
	go test ./internal/ui/... -update

lint: ## go vet + golangci-lint
	go vet ./...
	$(GOLANGCI_LINT) run

fmt: ## Format the code
	gofmt -s -w .

tidy: ## go mod tidy
	go mod tidy

cover: ## Coverage summary
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

clean: ## Remove build and coverage output
	rm -rf bin coverage.out

check: ## fmt check + lint + test (the "is it green" command)
	@test -z "$$(gofmt -s -l .)" || { gofmt -s -l .; echo "gofmt needed: run make fmt"; exit 1; }
	$(MAKE) lint
	$(MAKE) test
```

- [ ] **Step 11: Verify the Makefile**

Run: `make help && make build && bin/txenvoy version && make test`
Expected: target list prints, binary prints `dev` (or a git describe), tests PASS.

- [ ] **Step 12: Sync the spec with the deviations**

Edit `docs/superpowers/specs/2026-10-14-cluster-explorer-design.md`:

1. Replace the interface block in §3.1 with:

   ```go
   type Client interface {
       Clusters(ctx context.Context) (*adminv3.Clusters, error)
       ClusterConfigs(ctx context.Context) ([]ClusterConfig, error)
       ServerInfo(ctx context.Context) (*adminv3.ServerInfo, error)
   }

   type ClusterConfig struct {
       Name, Type, VersionInfo string
       LastUpdated             time.Time
       Static                  bool
       JSON                    []byte // the cluster's own protojson, as Envoy emitted it
   }
   ```
2. In the §3.1 bullets, replace the `/config_dump` and `Any` bullet with: `ClusterConfigs` calls `/config_dump?resource=dynamic_active_clusters` and `/config_dump?resource=static_clusters` and walks the result as raw JSON, so a cluster whose config holds an `Any` type that is not compiled in never fails the fetch. The snapshot renders each cluster's JSON to YAML with `yaml.JSONToYAML`.
3. In §5.6 replace "A `bubbles/table`" with "A hand-rolled table renderer (`bubbles/table` cannot hold styled cells without breaking width math)".
4. In §11 change "bubbletea" to "bubbletea v1".

- [ ] **Step 13: Commit**

```bash
git add -A
git commit -m "chore: scaffold module, config, cobra CLI, Makefile" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Demo scenarios and fake admin server

**Files:**
- Create: `internal/demo/scenario.go`, `internal/demo/server.go`, `internal/demo/server_test.go`

**Interfaces:**
- Produces:
  - `demo.Host{Address, Zone string; Weight uint32; Health map[string]any; RqActive, RqError, CxActive uint64; NoStats bool}`. An `Address` starting with `unix:` becomes a pipe address.
  - `demo.Cluster{Name, Type string; Static bool; Version string; Hosts []Host}`. `Type == ""` means STATIC and is omitted from the JSON.
  - `demo.Scenario{Envoy string; Updated time.Time; Clusters []Cluster}` with `ClustersJSON() []byte`, `ConfigDumpJSON(resource string) ([]byte, bool)`, `ServerInfoJSON() []byte`.
  - `demo.Healthy() Scenario`, `demo.Degraded() Scenario`, `demo.Big(n int) Scenario`.
  - `demo.Fault{Status int; Delay time.Duration; Garbage bool}`.
  - `demo.NewServer(Scenario) *Server`, `(*Server).ServeHTTP`, `SetScenario(Scenario)`, `Update(func(*Scenario))`, `Inject(Fault)`, `Start() (url string, stop func(), err error)`.

- [ ] **Step 1: Write the failing test**

`internal/demo/server_test.go`:

```go
package demo

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestEndpointsServeValidJSON(t *testing.T) {
	srv := httptest.NewServer(NewServer(Degraded()))
	defer srv.Close()

	code, body := get(t, srv.URL+"/clusters?format=json")
	if code != 200 {
		t.Fatalf("clusters: %d", code)
	}
	var clusters struct {
		ClusterStatuses []map[string]any `json:"cluster_statuses"`
	}
	if err := json.Unmarshal([]byte(body), &clusters); err != nil {
		t.Fatal(err)
	}
	if len(clusters.ClusterStatuses) != len(Degraded().Clusters) {
		t.Fatalf("got %d statuses", len(clusters.ClusterStatuses))
	}

	for _, res := range []string{"dynamic_active_clusters", "static_clusters"} {
		code, body = get(t, srv.URL+"/config_dump?resource="+res)
		if code != 200 || !strings.Contains(body, "ClustersConfigDump") || !strings.Contains(body, res) {
			t.Fatalf("%s: %d %s", res, code, body)
		}
	}
	if code, _ = get(t, srv.URL+"/config_dump?resource=bogus"); code != 400 {
		t.Fatalf("bogus resource: %d", code)
	}
	if code, body = get(t, srv.URL+"/server_info"); code != 200 || !strings.Contains(body, "1.31.0") {
		t.Fatalf("server_info: %d %s", code, body)
	}
	if code, _ = get(t, srv.URL+"/nope"); code != 404 {
		t.Fatalf("unknown path: %d", code)
	}
	if code, _ = get(t, srv.URL+"/clusters"); code != 400 {
		t.Fatalf("/clusters without format=json: %d", code)
	}
}

func TestStaticClustersAreSplitFromDynamic(t *testing.T) {
	s := Degraded()
	dyn, _ := s.ConfigDumpJSON("dynamic_active_clusters")
	stat, _ := s.ConfigDumpJSON("static_clusters")
	if strings.Contains(string(dyn), "xds_cluster") || !strings.Contains(string(stat), "xds_cluster") {
		t.Fatal("static cluster must only appear under static_clusters")
	}
}

func TestFaults(t *testing.T) {
	s := NewServer(Healthy())
	srv := httptest.NewServer(s)
	defer srv.Close()

	s.Inject(Fault{Status: 503})
	if code, _ := get(t, srv.URL+"/clusters?format=json"); code != 503 {
		t.Fatalf("status fault: %d", code)
	}
	s.Inject(Fault{Garbage: true})
	if _, body := get(t, srv.URL+"/clusters?format=json"); json.Valid([]byte(body)) {
		t.Fatal("garbage fault returned valid JSON")
	}
	s.Inject(Fault{Delay: 50 * time.Millisecond})
	start := time.Now()
	get(t, srv.URL+"/clusters?format=json")
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("delay fault did not delay")
	}
	s.Inject(Fault{})
	if code, _ := get(t, srv.URL+"/clusters?format=json"); code != 200 {
		t.Fatalf("cleared fault: %d", code)
	}
}

func TestSetScenarioAndUpdate(t *testing.T) {
	s := NewServer(Healthy())
	srv := httptest.NewServer(s)
	defer srv.Close()

	s.SetScenario(Big(300))
	_, body := get(t, srv.URL+"/clusters?format=json")
	if n := strings.Count(body, `"host_statuses"`); n != 300 {
		t.Fatalf("big scenario has %d clusters", n)
	}

	s.SetScenario(Healthy())
	s.Update(func(sc *Scenario) { sc.Clusters[0].Hosts[0].Health = map[string]any{"failed_active_health_check": true} })
	_, body = get(t, srv.URL+"/clusters?format=json")
	if !strings.Contains(body, "failed_active_health_check") {
		t.Fatal("update not visible")
	}
	if Healthy().Clusters[0].Hosts[0].Health != nil {
		t.Fatal("Update must not mutate shared builder data")
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/demo/...`
Expected: FAIL (package symbols undefined).

- [ ] **Step 3: Implement scenarios**

`internal/demo/scenario.go`:

```go
// Package demo is a fake Envoy admin API used by tests and by `txenvoy --demo`.
// It emits the JSON shape Envoy produces (snake_case protojson, uint64 as strings).
package demo

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

type Host struct {
	Address  string // "10.0.0.1:8080", or "unix:/path" for a pipe address
	Zone     string
	Weight   uint32
	Health   map[string]any // extra health_status fields, e.g. {"failed_outlier_check": true}
	RqActive uint64
	RqError  uint64
	CxActive uint64
	NoStats  bool
}

type Cluster struct {
	Name    string
	Type    string // "" means STATIC (omitted from JSON, as Envoy omits defaults)
	Static  bool
	Version string // dynamic clusters only
	Hosts   []Host
}

type Scenario struct {
	Envoy    string
	Updated  time.Time
	Clusters []Cluster
}

var epoch = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

func (s Scenario) ClustersJSON() []byte {
	statuses := make([]any, 0, len(s.Clusters))
	for _, c := range s.Clusters {
		hosts := make([]any, 0, len(c.Hosts))
		for _, h := range c.Hosts {
			hosts = append(hosts, h.json())
		}
		statuses = append(statuses, map[string]any{
			"name":          c.Name,
			"added_via_api": !c.Static,
			"host_statuses": hosts,
		})
	}
	return mustJSON(map[string]any{"cluster_statuses": statuses})
}

func (h Host) json() map[string]any {
	addr := map[string]any{}
	if path, ok := strings.CutPrefix(h.Address, "unix:"); ok {
		addr["pipe"] = map[string]any{"path": path}
	} else {
		host, port, _ := net.SplitHostPort(h.Address)
		n, _ := strconv.Atoi(port)
		addr["socket_address"] = map[string]any{"address": host, "port_value": n}
	}
	hs := map[string]any{"eds_health_status": "HEALTHY"}
	for k, v := range h.Health {
		hs[k] = v
	}
	out := map[string]any{"address": addr, "weight": h.Weight, "health_status": hs}
	if h.Zone != "" {
		out["locality"] = map[string]any{"zone": h.Zone}
	}
	if !h.NoStats {
		out["stats"] = []any{
			metric("cx_active", h.CxActive, "GAUGE"),
			metric("rq_active", h.RqActive, "GAUGE"),
			metric("rq_error", h.RqError, "COUNTER"),
		}
	}
	return out
}

func metric(name string, v uint64, typ string) map[string]any {
	return map[string]any{"name": name, "value": strconv.FormatUint(v, 10), "type": typ}
}

// ConfigDumpJSON returns the dump for "dynamic_active_clusters" or "static_clusters".
func (s Scenario) ConfigDumpJSON(resource string) ([]byte, bool) {
	if resource != "dynamic_active_clusters" && resource != "static_clusters" {
		return nil, false
	}
	entries := []any{}
	for _, c := range s.Clusters {
		if c.Static != (resource == "static_clusters") {
			continue
		}
		cl := map[string]any{
			"@type":           "type.googleapis.com/envoy.config.cluster.v3.Cluster",
			"name":            c.Name,
			"connect_timeout": "5s",
		}
		if c.Type != "" {
			cl["type"] = c.Type
		}
		e := map[string]any{"cluster": cl, "last_updated": s.Updated.Format(time.RFC3339Nano)}
		if !c.Static {
			e["version_info"] = c.Version
		}
		entries = append(entries, e)
	}
	return mustJSON(map[string]any{"configs": []any{map[string]any{
		"@type":  "type.googleapis.com/envoy.admin.v3.ClustersConfigDump",
		resource: entries,
	}}}), true
}

func (s Scenario) ServerInfoJSON() []byte {
	return mustJSON(map[string]any{"version": s.Envoy, "state": "LIVE"})
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func ok(addr, zone string, w uint32, rq, cx uint64) Host {
	return Host{Address: addr, Zone: zone, Weight: w, RqActive: rq, CxActive: cx}
}

func base(clusters ...Cluster) Scenario {
	return Scenario{Envoy: "deadbeef/1.31.0/Clean/RELEASE/BoringSSL", Updated: epoch, Clusters: clusters}
}

func Healthy() Scenario {
	return base(
		Cluster{Name: "api-gateway", Type: "EDS", Version: "42", Hosts: []Host{
			ok("10.0.1.11:8080", "us-east-1a", 1, 2, 5), ok("10.0.1.12:8080", "us-east-1b", 1, 1, 4),
		}},
		Cluster{Name: "search", Type: "EDS", Version: "42", Hosts: []Host{ok("10.0.3.31:9200", "us-east-1a", 1, 0, 1)}},
		Cluster{Name: "xds_cluster", Static: true, Hosts: []Host{ok("127.0.0.1:18000", "", 1, 0, 1)}},
	)
}

// Degraded mixes failed health checks, outlier ejection, an empty cluster, a
// static cluster and a unix-socket host with no stats or locality.
func Degraded() Scenario {
	api := Cluster{Name: "api-gateway", Type: "EDS", Version: "42", Hosts: []Host{
		ok("10.0.1.11:8080", "us-east-1a", 1, 2, 5),
		{Address: "10.0.1.12:8080", Zone: "us-east-1b", Weight: 1, RqActive: 0, RqError: 17, CxActive: 0,
			Health: map[string]any{"failed_active_health_check": true, "eds_health_status": "UNHEALTHY"}},
		ok("10.0.1.13:8080", "us-east-1c", 1, 1, 3),
	}}
	db := Cluster{Name: "orders-db", Type: "STRICT_DNS", Version: "41", Hosts: []Host{
		{Address: "10.0.2.21:5432", Zone: "us-east-1a", Weight: 1, RqError: 4, Health: map[string]any{"failed_outlier_check": true}},
		{Address: "10.0.2.22:5432", Zone: "us-east-1b", Weight: 1, RqError: 9, Health: map[string]any{"failed_outlier_check": true}},
	}}
	return base(
		api, db,
		Cluster{Name: "cache-redis", Type: "EDS", Version: "40"},
		Cluster{Name: "search", Type: "EDS", Version: "42", Hosts: []Host{
			ok("10.0.3.31:9200", "us-east-1a", 1, 0, 1), ok("10.0.3.32:9200", "us-east-1b", 2, 3, 2),
		}},
		Cluster{Name: "xds_cluster", Static: true, Hosts: []Host{ok("127.0.0.1:18000", "", 1, 0, 1)}},
		Cluster{Name: "local-agent", Static: true, Hosts: []Host{{Address: "unix:/var/run/agent.sock", NoStats: true}}},
	)
}

// Big returns n EDS clusters with two hosts each; every seventh has a failing host.
func Big(n int) Scenario {
	cs := make([]Cluster, 0, n)
	for i := 0; i < n; i++ {
		h1 := ok(fmt.Sprintf("10.1.%d.1:8080", i%250), "zone-a", 1, 1, 1)
		h2 := ok(fmt.Sprintf("10.1.%d.2:8080", i%250), "zone-b", 1, 1, 1)
		if i%7 == 0 {
			h2.Health = map[string]any{"failed_active_health_check": true}
		}
		cs = append(cs, Cluster{Name: fmt.Sprintf("svc-%03d", i), Type: "EDS", Version: "7", Hosts: []Host{h1, h2}})
	}
	return base(cs...)
}
```

- [ ] **Step 4: Implement the server**

`internal/demo/server.go`:

```go
package demo

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Fault injects a failure into every request until cleared with Inject(Fault{}).
type Fault struct {
	Status  int           // respond with this HTTP status
	Delay   time.Duration // wait before responding
	Garbage bool          // respond 200 with invalid JSON
}

type Server struct {
	mu    sync.Mutex
	sc    Scenario
	fault Fault
}

func NewServer(sc Scenario) *Server { return &Server{sc: sc} }

func (s *Server) SetScenario(sc Scenario) {
	s.mu.Lock()
	s.sc = sc
	s.mu.Unlock()
}

// Update applies fn to a deep copy of the current scenario and installs it.
func (s *Server) Update(fn func(*Scenario)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := s.sc
	cp.Clusters = make([]Cluster, len(s.sc.Clusters))
	for i, c := range s.sc.Clusters {
		c.Hosts = append([]Host(nil), c.Hosts...)
		for j, h := range c.Hosts {
			if h.Health != nil {
				m := make(map[string]any, len(h.Health))
				for k, v := range h.Health {
					m[k] = v
				}
				c.Hosts[j].Health = m
			}
		}
		cp.Clusters[i] = c
	}
	fn(&cp)
	s.sc = cp
}

func (s *Server) Inject(f Fault) {
	s.mu.Lock()
	s.fault = f
	s.mu.Unlock()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sc, f := s.sc, s.fault
	s.mu.Unlock()

	if f.Delay > 0 {
		select {
		case <-time.After(f.Delay):
		case <-r.Context().Done():
			return
		}
	}
	if f.Status != 0 {
		http.Error(w, "injected fault", f.Status)
		return
	}
	if f.Garbage {
		w.Write([]byte("{not json"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/clusters":
		if r.URL.Query().Get("format") != "json" {
			http.Error(w, "format=json required", http.StatusBadRequest)
			return
		}
		w.Write(sc.ClustersJSON())
	case "/config_dump":
		b, ok := sc.ConfigDumpJSON(r.URL.Query().Get("resource"))
		if !ok {
			http.Error(w, "unsupported resource", http.StatusBadRequest)
			return
		}
		w.Write(b)
	case "/server_info":
		w.Write(sc.ServerInfoJSON())
	default:
		http.NotFound(w, r)
	}
}

// Start serves on a random loopback port and returns its base URL and a stop func.
func (s *Server) Start() (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	srv := &http.Server{Handler: s}
	go srv.Serve(ln)
	return "http://" + ln.Addr().String(), func() { srv.Close() }, nil
}
```

- [ ] **Step 5: Run the tests**

Run: `go test -race ./internal/demo/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(demo): add scenarios and fake admin server with fault injection" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: admin client

**Files:**
- Create: `internal/admin/client.go`, `internal/admin/configdump.go`, `internal/admin/admin_test.go`

**Interfaces:**
- Produces:

```go
type Client interface {
    Clusters(ctx context.Context) (*adminv3.Clusters, error)
    ClusterConfigs(ctx context.Context) ([]ClusterConfig, error)
    ServerInfo(ctx context.Context) (*adminv3.ServerInfo, error)
}
type ClusterConfig struct {
    Name, Type, VersionInfo string
    LastUpdated             time.Time
    Static                  bool
    JSON                    []byte
}
func NewHTTP(base string, timeout time.Duration) (*HTTP, error) // *HTTP implements Client
```

Error messages are prefixed `GET <path>:`.

- [ ] **Step 1: Add dependencies**

```bash
go get github.com/envoyproxy/go-control-plane@latest google.golang.org/protobuf@latest
```

- [ ] **Step 2: Write the failing tests**

`internal/admin/admin_test.go`:

```go
package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A hand-written payload in the shape real Envoy emits. It deliberately does
// not come from internal/demo, so the demo builders cannot share a
// misconception with the decoder.
const realClusters = `{"cluster_statuses":[{"name":"api","added_via_api":true,"host_statuses":[
 {"address":{"socket_address":{"address":"10.0.0.1","port_value":8080}},
  "stats":[{"name":"cx_active","value":"3","type":"GAUGE"},{"name":"rq_error","value":"7"}],
  "health_status":{"failed_active_health_check":true,"eds_health_status":"UNHEALTHY"},
  "weight":2,"locality":{"zone":"us-east-1a"},"some_future_field":1}]}]}`

const realConfigDynamic = `{"configs":[
 {"@type":"type.googleapis.com/envoy.admin.v3.BootstrapConfigDump","bootstrap":{}},
 {"@type":"type.googleapis.com/envoy.admin.v3.ClustersConfigDump","version_info":"x",
  "dynamic_active_clusters":[
   {"version_info":"42","last_updated":"2026-10-14T10:00:00.123Z",
    "cluster":{"@type":"type.googleapis.com/envoy.config.cluster.v3.Cluster","name":"api","type":"EDS",
      "transport_socket":{"name":"x","typed_config":{"@type":"type.googleapis.com/acme.Unknown","foo":1}}}},
   {"version_info":"7","cluster":{"@type":"type.googleapis.com/envoy.config.cluster.v3.Cluster","name":"agg",
      "cluster_type":{"name":"envoy.clusters.aggregate"}}}]}]}`

const realConfigStatic = `{"configs":[{"@type":"type.googleapis.com/envoy.admin.v3.ClustersConfigDump",
 "static_clusters":[{"cluster":{"@type":"type.googleapis.com/envoy.config.cluster.v3.Cluster","name":"xds_cluster"},
   "last_updated":"2026-10-14T09:00:00Z"}]}]}`

func newClient(t *testing.T, h http.Handler, timeout time.Duration) *HTTP {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewHTTP(srv.URL, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClusters(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/clusters" || r.URL.Query().Get("format") != "json" {
			http.Error(w, "bad request "+r.URL.String(), 400)
			return
		}
		w.Write([]byte(realClusters))
	}), time.Second)

	got, err := c.Clusters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h := got.GetClusterStatuses()[0].GetHostStatuses()[0]
	if got.GetClusterStatuses()[0].GetName() != "api" || h.GetWeight() != 2 ||
		h.GetAddress().GetSocketAddress().GetPortValue() != 8080 ||
		h.GetLocality().GetZone() != "us-east-1a" ||
		!h.GetHealthStatus().GetFailedActiveHealthCheck() ||
		h.GetStats()[0].GetName() != "cx_active" || h.GetStats()[0].GetValue() != 3 {
		t.Fatalf("decoded wrongly: %v", got)
	}
}

func TestClusterConfigsMergesDynamicAndStatic(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("resource") {
		case "dynamic_active_clusters":
			w.Write([]byte(realConfigDynamic))
		case "static_clusters":
			w.Write([]byte(realConfigStatic))
		default:
			http.Error(w, "bad resource", 400)
		}
	}), time.Second)

	got, err := c.ClusterConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ClusterConfig{}
	for _, cc := range got {
		by[cc.Name] = cc
	}
	if len(by) != 3 {
		t.Fatalf("got %d configs: %+v", len(by), got)
	}
	api := by["api"]
	if api.Type != "EDS" || api.VersionInfo != "42" || api.Static ||
		api.LastUpdated.Format(time.RFC3339Nano) != "2026-10-14T10:00:00.123Z" {
		t.Fatalf("api: %+v", api)
	}
	if !strings.Contains(string(api.JSON), "acme.Unknown") {
		t.Fatal("unknown nested Any type must survive in the raw JSON, not fail the decode")
	}
	if by["agg"].Type != "envoy.clusters.aggregate" {
		t.Fatalf("custom cluster_type: %+v", by["agg"])
	}
	if x := by["xds_cluster"]; !x.Static || x.Type != "STATIC" || x.VersionInfo != "" {
		t.Fatalf("static: %+v", x)
	}
}

func TestServerInfo(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"abc/1.31.0/Clean/RELEASE","state":"LIVE","unknown":true}`))
	}), time.Second)
	info, err := c.ServerInfo(context.Background())
	if err != nil || info.GetVersion() != "abc/1.31.0/Clean/RELEASE" {
		t.Fatalf("%v %v", info, err)
	}
}

func TestErrors(t *testing.T) {
	t.Run("non-2xx", func(t *testing.T) {
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 503) }), time.Second)
		_, err := c.Clusters(context.Background())
		if err == nil || !strings.Contains(err.Error(), "GET /clusters") || !strings.Contains(err.Error(), "503") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{nope")) }), time.Second)
		if _, err := c.Clusters(context.Background()); err == nil || !strings.Contains(err.Error(), "GET /clusters") {
			t.Fatalf("got %v", err)
		}
		if _, err := c.ClusterConfigs(context.Background()); err == nil {
			t.Fatal("expected config decode error")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(2 * time.Second):
			case <-r.Context().Done():
			}
		}), 50*time.Millisecond)
		start := time.Now()
		if _, err := c.Clusters(context.Background()); err == nil {
			t.Fatal("expected timeout")
		}
		if time.Since(start) > time.Second {
			t.Fatal("timeout not applied")
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(realClusters)) }), time.Second)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := c.Clusters(ctx); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("bad base url", func(t *testing.T) {
		if _, err := NewHTTP("::nope", time.Second); err == nil {
			t.Fatal("expected error")
		}
	})
}
```

- [ ] **Step 3: Run them and confirm they fail**

Run: `go test ./internal/admin/...`
Expected: FAIL (package symbols undefined).

- [ ] **Step 4: Implement the client**

`internal/admin/client.go`:

```go
// Package admin talks to Envoy's admin interface. It is the only transport
// seam: a port-forward implementation can later satisfy Client unchanged.
package admin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	adminv3 "github.com/envoyproxy/go-control-plane/envoy/admin/v3"
	"google.golang.org/protobuf/encoding/protojson"
)

const maxBody = 64 << 20

type Client interface {
	Clusters(ctx context.Context) (*adminv3.Clusters, error)
	ClusterConfigs(ctx context.Context) ([]ClusterConfig, error)
	ServerInfo(ctx context.Context) (*adminv3.ServerInfo, error)
}

// ClusterConfig is one cluster's config as Envoy emitted it.
type ClusterConfig struct {
	Name, Type, VersionInfo string
	LastUpdated             time.Time
	Static                  bool
	JSON                    []byte
}

type HTTP struct {
	base    *url.URL
	hc      *http.Client
	timeout time.Duration
}

func NewHTTP(base string, timeout time.Duration) (*HTTP, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("admin url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("admin url %q needs a scheme and host", base)
	}
	return &HTTP{base: u, hc: &http.Client{}, timeout: timeout}, nil
}

func (h *HTTP) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	u := *h.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	resp, err := h.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	return body, nil
}

var lenient = protojson.UnmarshalOptions{DiscardUnknown: true}

func (h *HTTP) Clusters(ctx context.Context) (*adminv3.Clusters, error) {
	body, err := h.get(ctx, "/clusters", url.Values{"format": {"json"}})
	if err != nil {
		return nil, err
	}
	out := &adminv3.Clusters{}
	if err := lenient.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("GET /clusters: decode: %w", err)
	}
	return out, nil
}

func (h *HTTP) ServerInfo(ctx context.Context) (*adminv3.ServerInfo, error) {
	body, err := h.get(ctx, "/server_info", nil)
	if err != nil {
		return nil, err
	}
	out := &adminv3.ServerInfo{}
	if err := lenient.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("GET /server_info: decode: %w", err)
	}
	return out, nil
}

func (h *HTTP) ClusterConfigs(ctx context.Context) ([]ClusterConfig, error) {
	var all []ClusterConfig
	for _, res := range []string{"dynamic_active_clusters", "static_clusters"} {
		body, err := h.get(ctx, "/config_dump", url.Values{"resource": {res}})
		if err != nil {
			return nil, err
		}
		cfgs, err := decodeClusterConfigs(body, res == "static_clusters")
		if err != nil {
			return nil, fmt.Errorf("GET /config_dump: decode %s: %w", res, err)
		}
		all = append(all, cfgs...)
	}
	return all, nil
}
```

`internal/admin/configdump.go`:

```go
package admin

import (
	"encoding/json"
	"strings"
	"time"
)

type entry struct {
	VersionInfo string          `json:"version_info"`
	LastUpdated string          `json:"last_updated"`
	Cluster     json.RawMessage `json:"cluster"`
}

type clustersDump struct {
	Type    string  `json:"@type"`
	Static  []entry `json:"static_clusters"`
	Dynamic []entry `json:"dynamic_active_clusters"`
}

// decodeClusterConfigs walks /config_dump as raw JSON so a cluster whose config
// holds an Any type that is not compiled in can never fail the whole decode.
func decodeClusterConfigs(body []byte, static bool) ([]ClusterConfig, error) {
	var dump struct {
		Configs []json.RawMessage `json:"configs"`
	}
	if err := json.Unmarshal(body, &dump); err != nil {
		return nil, err
	}
	var out []ClusterConfig
	for _, raw := range dump.Configs {
		var cd clustersDump
		if err := json.Unmarshal(raw, &cd); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(cd.Type, ".ClustersConfigDump") {
			continue
		}
		entries := cd.Dynamic
		if static {
			entries = cd.Static
		}
		for _, e := range entries {
			var cl struct {
				Name        string `json:"name"`
				Type        string `json:"type"`
				ClusterType struct {
					Name string `json:"name"`
				} `json:"cluster_type"`
			}
			if err := json.Unmarshal(e.Cluster, &cl); err != nil {
				return nil, err
			}
			typ := cl.Type
			switch {
			case cl.ClusterType.Name != "":
				typ = cl.ClusterType.Name
			case typ == "":
				typ = "STATIC" // proto3 default, omitted by Envoy
			}
			updated, _ := time.Parse(time.RFC3339Nano, e.LastUpdated)
			out = append(out, ClusterConfig{
				Name: cl.Name, Type: typ, VersionInfo: e.VersionInfo,
				LastUpdated: updated, Static: static, JSON: e.Cluster,
			})
		}
	}
	return out, nil
}
```

- [ ] **Step 5: Run the tests**

Run: `go test -race ./internal/admin/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(admin): add admin Client with HTTP implementation and tolerant config walker" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: snapshot

**Files:**
- Create: `internal/snapshot/snapshot.go`, `internal/snapshot/flags.go`, `internal/snapshot/snapshot_test.go`

**Interfaces:**
- Consumes: `admin.Client`, `admin.ClusterConfig` (Task 3), `demo.*` (tests only).
- Produces:

```go
type Health int // Healthy, Empty, Degraded, Unhealthy
func (Health) String() string
type Host struct { Address, Zone string; Flags []string; Weight uint32; RqActive, RqError, CxActive uint64 }
func (Host) Healthy() bool; func (Host) Ejected() bool
type ConfigInfo struct { YAML, VersionInfo string; LastUpdated time.Time; Static bool; Err string }
type Cluster struct { Name, Type string; Hosts []Host; Config ConfigInfo }
func (Cluster) Health() Health; func (Cluster) HealthyCount() int; func (Cluster) EjectedCount() int
type Snapshot struct { Clusters []Cluster; Version string; FetchedAt time.Time }
func (Snapshot) UnhealthyClusters() int
func Fetch(ctx context.Context, c admin.Client) (Snapshot, error)
func IsUnhealthyFlag(flag string) bool
```

Cluster health: no hosts → `Empty`; no unhealthy hosts → `Healthy`; some → `Degraded`; all → `Unhealthy`. Hosts with only degraded flags (`DEGRADED_ACTIVE_HC`, `EDS_STATUS_DEGRADED`, `PENDING_ACTIVE_HC`) count as healthy.

- [ ] **Step 1: Add the YAML dependency**

```bash
go get sigs.k8s.io/yaml@latest
```

- [ ] **Step 2: Write the failing tests**

`internal/snapshot/snapshot_test.go`:

```go
package snapshot

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	adminv3 "github.com/envoyproxy/go-control-plane/envoy/admin/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"

	"github.com/regeda/txenvoy/internal/admin"
	"github.com/regeda/txenvoy/internal/demo"
)

func fetchFrom(t *testing.T, sc demo.Scenario) Snapshot {
	t.Helper()
	srv := httptest.NewServer(demo.NewServer(sc))
	t.Cleanup(srv.Close)
	c, err := admin.NewHTTP(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := Fetch(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func byName(s Snapshot) map[string]Cluster {
	m := map[string]Cluster{}
	for _, c := range s.Clusters {
		m[c.Name] = c
	}
	return m
}

func TestFetchDegraded(t *testing.T) {
	s := fetchFrom(t, demo.Degraded())

	var order []string
	for _, c := range s.Clusters {
		order = append(order, c.Name)
	}
	want := "orders-db api-gateway cache-redis local-agent search xds_cluster"
	if strings.Join(order, " ") != want {
		t.Fatalf("order = %v, want %s", order, want)
	}
	if s.Version != "1.31.0" || s.FetchedAt.IsZero() {
		t.Fatalf("version=%q fetchedAt=%v", s.Version, s.FetchedAt)
	}
	if s.UnhealthyClusters() != 2 {
		t.Fatalf("unhealthy clusters = %d", s.UnhealthyClusters())
	}

	m := byName(s)
	api := m["api-gateway"]
	if api.Type != "EDS" || api.Health() != Degraded || api.HealthyCount() != 2 || len(api.Hosts) != 3 {
		t.Fatalf("api: %+v health=%v", api, api.Health())
	}
	if !strings.Contains(api.Config.YAML, "name: api-gateway") || api.Config.VersionInfo != "42" || api.Config.LastUpdated.IsZero() {
		t.Fatalf("api config: %+v", api.Config)
	}
	var bad Host
	for _, h := range api.Hosts {
		if !h.Healthy() {
			bad = h
		}
	}
	if strings.Join(bad.Flags, ",") != "FAILED_ACTIVE_HC,EDS_STATUS_UNHEALTHY" || bad.RqError != 17 {
		t.Fatalf("bad host: %+v", bad)
	}

	db := m["orders-db"]
	if db.Health() != Unhealthy || db.EjectedCount() != 2 || db.Type != "STRICT_DNS" {
		t.Fatalf("db: %+v", db)
	}
	if m["cache-redis"].Health() != Empty {
		t.Fatal("cache-redis should be Empty")
	}
	x := m["xds_cluster"]
	if x.Type != "STATIC" || !x.Config.Static || x.Config.VersionInfo != "" || x.Health() != Healthy {
		t.Fatalf("static: %+v", x)
	}
}

func TestHostWithPipeAddressAndNoStats(t *testing.T) {
	h := byName(fetchFrom(t, demo.Degraded()))["local-agent"].Hosts[0]
	if h.Address != "unix:/var/run/agent.sock" || h.Zone != "" || h.RqActive != 0 || h.CxActive != 0 || len(h.Flags) != 0 {
		t.Fatalf("pipe host: %+v", h)
	}
}

type stub struct {
	admin.Client
	clustersErr, cfgErr, infoErr error
}

func (s stub) Clusters(ctx context.Context) (*adminv3.Clusters, error) {
	if s.clustersErr != nil {
		return nil, s.clustersErr
	}
	return s.Client.Clusters(ctx)
}
func (s stub) ClusterConfigs(ctx context.Context) ([]admin.ClusterConfig, error) {
	if s.cfgErr != nil {
		return nil, s.cfgErr
	}
	return s.Client.ClusterConfigs(ctx)
}
func (s stub) ServerInfo(ctx context.Context) (*adminv3.ServerInfo, error) {
	if s.infoErr != nil {
		return nil, s.infoErr
	}
	return s.Client.ServerInfo(ctx)
}

func TestPartialFailures(t *testing.T) {
	srv := httptest.NewServer(demo.NewServer(demo.Healthy()))
	defer srv.Close()
	real, _ := admin.NewHTTP(srv.URL, time.Second)
	boom := errors.New("boom")

	s, err := Fetch(context.Background(), stub{Client: real, cfgErr: boom})
	if err != nil {
		t.Fatalf("config failure must not fail the snapshot: %v", err)
	}
	for _, c := range s.Clusters {
		if !strings.HasPrefix(c.Config.Err, "config unavailable") || c.Config.YAML != "" {
			t.Fatalf("%s: %+v", c.Name, c.Config)
		}
	}

	s, err = Fetch(context.Background(), stub{Client: real, infoErr: boom})
	if err != nil || s.Version != "" || len(s.Clusters) == 0 {
		t.Fatalf("server_info failure: %v %+v", err, s)
	}

	if _, err = Fetch(context.Background(), stub{Client: real, clustersErr: boom}); !errors.Is(err, boom) {
		t.Fatalf("clusters failure must fail: %v", err)
	}
}

func TestEmptyEnvoy(t *testing.T) {
	s := fetchFrom(t, demo.Scenario{Envoy: "x/1.0.0/Clean", Updated: time.Now()})
	if len(s.Clusters) != 0 || s.UnhealthyClusters() != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestFlagsOf(t *testing.T) {
	hs := &adminv3.HostHealthStatus{
		FailedActiveHealthCheck:    true,
		FailedOutlierCheck:         true,
		FailedActiveDegradedCheck:  true,
		PendingDynamicRemoval:      true,
		PendingActiveHc:            true,
		ExcludedViaImmediateHcFail: true,
		ActiveHcTimeout:            true,
		EdsHealthStatus:            corev3.HealthStatus_DRAINING,
	}
	got := strings.Join(flagsOf(hs), ",")
	want := "FAILED_ACTIVE_HC,FAILED_OUTLIER_CHECK,DEGRADED_ACTIVE_HC,PENDING_DYNAMIC_REMOVAL,PENDING_ACTIVE_HC,EXCLUDED_VIA_IMMEDIATE_HC_FAIL,ACTIVE_HC_TIMEOUT,EDS_STATUS_DRAINING"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if len(flagsOf(nil)) != 0 || len(flagsOf(&adminv3.HostHealthStatus{EdsHealthStatus: corev3.HealthStatus_HEALTHY})) != 0 {
		t.Fatal("healthy host must have no flags")
	}
	if !IsUnhealthyFlag("FAILED_OUTLIER_CHECK") || IsUnhealthyFlag("DEGRADED_ACTIVE_HC") || IsUnhealthyFlag("PENDING_ACTIVE_HC") {
		t.Fatal("IsUnhealthyFlag misclassifies")
	}
}
```

- [ ] **Step 3: Run and confirm failure**

Run: `go test ./internal/snapshot/...`
Expected: FAIL (symbols undefined).

- [ ] **Step 4: Implement flags**

`internal/snapshot/flags.go`:

```go
package snapshot

import (
	adminv3 "github.com/envoyproxy/go-control-plane/envoy/admin/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
)

var unhealthyFlags = map[string]bool{
	"FAILED_ACTIVE_HC":               true,
	"FAILED_OUTLIER_CHECK":           true,
	"EDS_STATUS_UNHEALTHY":           true,
	"EDS_STATUS_TIMEOUT":             true,
	"ACTIVE_HC_TIMEOUT":              true,
	"EXCLUDED_VIA_IMMEDIATE_HC_FAIL": true,
	"PENDING_DYNAMIC_REMOVAL":        true,
}

// IsUnhealthyFlag reports whether a flag means the host is not serving traffic.
// Degraded and pending flags are informational.
func IsUnhealthyFlag(flag string) bool { return unhealthyFlags[flag] }

func flagsOf(hs *adminv3.HostHealthStatus) []string {
	if hs == nil {
		return nil
	}
	var f []string
	add := func(on bool, name string) {
		if on {
			f = append(f, name)
		}
	}
	add(hs.GetFailedActiveHealthCheck(), "FAILED_ACTIVE_HC")
	add(hs.GetFailedOutlierCheck(), "FAILED_OUTLIER_CHECK")
	add(hs.GetFailedActiveDegradedCheck(), "DEGRADED_ACTIVE_HC")
	add(hs.GetPendingDynamicRemoval(), "PENDING_DYNAMIC_REMOVAL")
	add(hs.GetPendingActiveHc(), "PENDING_ACTIVE_HC")
	add(hs.GetExcludedViaImmediateHcFail(), "EXCLUDED_VIA_IMMEDIATE_HC_FAIL")
	add(hs.GetActiveHcTimeout(), "ACTIVE_HC_TIMEOUT")
	switch hs.GetEdsHealthStatus() {
	case corev3.HealthStatus_UNHEALTHY:
		f = append(f, "EDS_STATUS_UNHEALTHY")
	case corev3.HealthStatus_DEGRADED:
		f = append(f, "EDS_STATUS_DEGRADED")
	case corev3.HealthStatus_TIMEOUT:
		f = append(f, "EDS_STATUS_TIMEOUT")
	case corev3.HealthStatus_DRAINING:
		f = append(f, "EDS_STATUS_DRAINING")
	}
	return f
}
```

- [ ] **Step 5: Implement the snapshot**

`internal/snapshot/snapshot.go`:

```go
// Package snapshot turns admin API payloads into plain Go types. The UI never
// sees protobuf types.
package snapshot

import (
	"context"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	adminv3 "github.com/envoyproxy/go-control-plane/envoy/admin/v3"
	"sigs.k8s.io/yaml"

	"github.com/regeda/txenvoy/internal/admin"
)

type Health int

const (
	Healthy Health = iota
	Empty
	Degraded
	Unhealthy
)

func (h Health) String() string {
	return [...]string{"healthy", "empty", "degraded", "unhealthy"}[h]
}

type Host struct {
	Address, Zone                string
	Flags                        []string
	Weight                       uint32
	RqActive, RqError, CxActive uint64
}

func (h Host) Healthy() bool {
	for _, f := range h.Flags {
		if IsUnhealthyFlag(f) {
			return false
		}
	}
	return true
}

func (h Host) Ejected() bool {
	for _, f := range h.Flags {
		if f == "FAILED_OUTLIER_CHECK" {
			return true
		}
	}
	return false
}

type ConfigInfo struct {
	YAML, VersionInfo string
	LastUpdated       time.Time
	Static            bool
	Err               string
}

type Cluster struct {
	Name, Type string
	Hosts      []Host
	Config     ConfigInfo
}

func (c Cluster) HealthyCount() int {
	n := 0
	for _, h := range c.Hosts {
		if h.Healthy() {
			n++
		}
	}
	return n
}

func (c Cluster) EjectedCount() int {
	n := 0
	for _, h := range c.Hosts {
		if h.Ejected() {
			n++
		}
	}
	return n
}

func (c Cluster) Health() Health {
	switch healthy := c.HealthyCount(); {
	case len(c.Hosts) == 0:
		return Empty
	case healthy == len(c.Hosts):
		return Healthy
	case healthy == 0:
		return Unhealthy
	default:
		return Degraded
	}
}

type Snapshot struct {
	Clusters  []Cluster
	Version   string
	FetchedAt time.Time
}

// UnhealthyClusters counts clusters that are Degraded or Unhealthy.
func (s Snapshot) UnhealthyClusters() int {
	n := 0
	for _, c := range s.Clusters {
		if h := c.Health(); h == Degraded || h == Unhealthy {
			n++
		}
	}
	return n
}

// Fetch calls the admin API and joins /clusters with the config dump by name.
// Only a /clusters failure is fatal; missing config or server info degrade the
// snapshot instead.
func Fetch(ctx context.Context, c admin.Client) (Snapshot, error) {
	cl, err := c.Clusters(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	cfgs, cfgErr := c.ClusterConfigs(ctx)
	info, _ := c.ServerInfo(ctx)

	cfgByName := make(map[string]admin.ClusterConfig, len(cfgs))
	for _, cc := range cfgs {
		cfgByName[cc.Name] = cc
	}

	out := Snapshot{FetchedAt: time.Now(), Version: parseVersion(info.GetVersion())}
	for _, st := range cl.GetClusterStatuses() {
		cluster := Cluster{Name: st.GetName(), Type: "?"}
		for _, hs := range st.GetHostStatuses() {
			cluster.Hosts = append(cluster.Hosts, hostFrom(hs))
		}
		switch cc, found := cfgByName[st.GetName()]; {
		case cfgErr != nil:
			cluster.Config.Err = "config unavailable: " + cfgErr.Error()
		case !found:
			cluster.Config.Err = "config unavailable: not present in config_dump"
		default:
			cluster.Type = cc.Type
			cluster.Config.VersionInfo = cc.VersionInfo
			cluster.Config.LastUpdated = cc.LastUpdated
			cluster.Config.Static = cc.Static
			if y, yerr := yaml.JSONToYAML(cc.JSON); yerr != nil {
				cluster.Config.Err = "config unavailable: render yaml: " + yerr.Error()
			} else {
				cluster.Config.YAML = string(y)
			}
		}
		out.Clusters = append(out.Clusters, cluster)
	}
	sortClusters(out.Clusters)
	return out, nil
}

func severity(h Health) int {
	switch h {
	case Unhealthy:
		return 0
	case Degraded:
		return 1
	default:
		return 2
	}
}

func sortClusters(cs []Cluster) {
	sort.SliceStable(cs, func(i, j int) bool {
		si, sj := severity(cs[i].Health()), severity(cs[j].Health())
		if si != sj {
			return si < sj
		}
		return cs[i].Name < cs[j].Name
	})
}

func hostFrom(hs *adminv3.HostStatus) Host {
	h := Host{
		Zone:   hs.GetLocality().GetZone(),
		Weight: hs.GetWeight(),
		Flags:  flagsOf(hs.GetHealthStatus()),
	}
	if sa := hs.GetAddress().GetSocketAddress(); sa != nil {
		h.Address = net.JoinHostPort(sa.GetAddress(), strconv.Itoa(int(sa.GetPortValue())))
	} else if p := hs.GetAddress().GetPipe(); p != nil {
		h.Address = "unix:" + p.GetPath()
	}
	for _, m := range hs.GetStats() {
		switch m.GetName() {
		case "rq_active":
			h.RqActive = m.GetValue()
		case "rq_error":
			h.RqError = m.GetValue()
		case "cx_active":
			h.CxActive = m.GetValue()
		}
	}
	return h
}

// parseVersion extracts "1.31.0" from "hash/1.31.0/Clean/RELEASE/BoringSSL".
func parseVersion(v string) string {
	if parts := strings.Split(v, "/"); len(parts) >= 2 {
		return parts[1]
	}
	return v
}
```

- [ ] **Step 6: Run the tests**

Run: `go test -race ./internal/snapshot/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(snapshot): join clusters, config and server info into domain types" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: UI foundations and the cluster list with fuzzy filter

**Files:**
- Create: `internal/ui/styles.go`, `internal/ui/text.go`, `internal/ui/listpane.go`, `internal/ui/listpane_test.go`

**Interfaces:**
- Consumes: `snapshot.Cluster`, `snapshot.Health`.
- Produces:
  - Styles: `dimStyle, okStyle, warnStyle, badStyle, boldStyle, logoStyle lipgloss.Style`, `markerStyle(snapshot.Health) lipgloss.Style`.
  - Text helpers: `trunc(s string, w int) string`, `pad(s string, w int) string`, `ago(d time.Duration) string`.
  - `newListPane() listPane`; `(*listPane).SetClusters([]snapshot.Cluster)`, `SetSize(w, h int)`, `SetWaiting(bool)`, `HandleKey(tea.KeyMsg)`, `Selected() (snapshot.Cluster, bool)`; `(listPane).View() string`; field `filtering bool`.

- [ ] **Step 1: Add dependencies**

```bash
go get github.com/charmbracelet/bubbletea@v1 github.com/charmbracelet/bubbles@latest github.com/charmbracelet/lipgloss@v1 github.com/charmbracelet/x/ansi@latest github.com/sahilm/fuzzy@latest
```

If `bubbles@latest` requires bubbletea v2, pin the newest bubbles release that still targets bubbletea v1 (`go list -m -versions github.com/charmbracelet/bubbles`).

- [ ] **Step 2: Write the failing tests**

`internal/ui/listpane_test.go`:

```go
package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/snapshot"
)

func cl(name string, healthy, total int) snapshot.Cluster {
	c := snapshot.Cluster{Name: name, Type: "EDS"}
	for i := 0; i < total; i++ {
		h := snapshot.Host{Address: fmt.Sprintf("10.0.0.%d:80", i)}
		if i >= healthy {
			h.Flags = []string{"FAILED_ACTIVE_HC"}
		}
		c.Hosts = append(c.Hosts, h)
	}
	return c
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typeInto(l *listPane, s string) {
	for _, r := range s {
		l.HandleKey(key(string(r)))
	}
}

func newList(cs ...snapshot.Cluster) listPane {
	l := newListPane()
	l.SetSize(60, 12)
	l.SetClusters(cs)
	return l
}

func TestCursorMovesAndClamps(t *testing.T) {
	l := newList(cl("a", 1, 1), cl("b", 1, 1), cl("c", 1, 1))
	l.HandleKey(key("up"))
	if c, _ := l.Selected(); c.Name != "a" {
		t.Fatal("up at top must clamp")
	}
	for i := 0; i < 5; i++ {
		l.HandleKey(key("down"))
	}
	if c, _ := l.Selected(); c.Name != "c" {
		t.Fatal("down at bottom must clamp")
	}
}

func TestSelectionFollowsNameAcrossRefresh(t *testing.T) {
	l := newList(cl("a", 1, 1), cl("b", 1, 1), cl("c", 1, 1))
	l.HandleKey(key("down")) // b
	l.SetClusters([]snapshot.Cluster{cl("z", 1, 1), cl("c", 1, 1), cl("b", 1, 1)})
	if c, _ := l.Selected(); c.Name != "b" {
		t.Fatalf("selection jumped to %q", c.Name)
	}
}

func TestSelectedClusterDisappears(t *testing.T) {
	l := newList(cl("a", 1, 1), cl("b", 1, 1), cl("c", 1, 1))
	l.HandleKey(key("down"))
	l.HandleKey(key("down")) // c, last row
	l.SetClusters([]snapshot.Cluster{cl("a", 1, 1), cl("b", 1, 1)})
	if c, ok := l.Selected(); !ok || c.Name != "b" {
		t.Fatalf("should fall to nearest row, got %q ok=%v", c.Name, ok)
	}
	l.SetClusters(nil)
	if _, ok := l.Selected(); ok {
		t.Fatal("empty list must have no selection")
	}
	_ = l.View() // must not panic
}

func TestFilter(t *testing.T) {
	l := newList(cl("api-gateway", 1, 1), cl("orders-db", 1, 1), cl("apiary", 1, 1))
	l.HandleKey(key("/"))
	if !l.filtering {
		t.Fatal("/ must start filtering")
	}
	typeInto(&l, "api")
	if len(l.visible) != 2 {
		t.Fatalf("visible=%d", len(l.visible))
	}
	l.HandleKey(key("enter"))
	if l.filtering || len(l.visible) != 2 {
		t.Fatal("enter keeps the filter and leaves filter mode")
	}
	l.HandleKey(key("esc"))
	if len(l.visible) != 3 {
		t.Fatal("esc clears the filter")
	}
}

func TestFilterWithNoMatch(t *testing.T) {
	l := newList(cl("api", 1, 1))
	l.HandleKey(key("/"))
	typeInto(&l, "zzzz")
	if _, ok := l.Selected(); ok {
		t.Fatal("no match means no selection")
	}
	if !strings.Contains(l.View(), "no matches") {
		t.Fatal("view should say no matches")
	}
	l.HandleKey(key("down")) // must not panic
}

func TestEmptyAndWaiting(t *testing.T) {
	l := newList()
	if !strings.Contains(l.View(), "no clusters") {
		t.Fatal(l.View())
	}
	l.SetWaiting(true)
	if !strings.Contains(l.View(), "waiting for Envoy") {
		t.Fatal(l.View())
	}
}

func TestRowShowsHealthCounts(t *testing.T) {
	l := newList(cl("api", 3, 4))
	if v := l.View(); !strings.Contains(v, "3/4") || !strings.Contains(v, "EDS") || !strings.Contains(v, "api") {
		t.Fatal(v)
	}
}

func TestLongAndWideNamesNeverExceedWidth(t *testing.T) {
	long := strings.Repeat("very-long-cluster-name-", 10)
	l := newList(cl(long, 1, 2), cl("日本語のクラスター名前日本語のクラスター名前", 1, 1), cl("ok", 1, 1))
	for _, line := range strings.Split(l.View(), "\n") {
		if w := lipgloss.Width(line); w > 60 {
			t.Fatalf("line width %d > 60: %q", w, line)
		}
	}
	if n := strings.Count(l.View(), "\n") + 1; n != 12 {
		t.Fatalf("height = %d lines, want 12", n)
	}
}

func TestScrollKeepsCursorVisible(t *testing.T) {
	var cs []snapshot.Cluster
	for i := 0; i < 50; i++ {
		cs = append(cs, cl(fmt.Sprintf("svc-%02d", i), 1, 1))
	}
	l := newList(cs...)
	for i := 0; i < 30; i++ {
		l.HandleKey(key("down"))
	}
	if !strings.Contains(l.View(), "svc-30") {
		t.Fatal("selected row must be visible after scrolling")
	}
}

func TestTinyPaneDoesNotPanic(t *testing.T) {
	l := newListPane()
	_ = l.View() // zero size, no data
	l.SetSize(1, 1)
	l.SetClusters([]snapshot.Cluster{cl("a", 1, 1)})
	_ = l.View()
}
```

- [ ] **Step 3: Run and confirm failure**

Run: `go test ./internal/ui/...`
Expected: FAIL (symbols undefined).

- [ ] **Step 4: Implement styles and text helpers**

`internal/ui/styles.go`:

```go
package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/snapshot"
)

var (
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	badStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	boldStyle = lipgloss.NewStyle().Bold(true)
	logoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
)

func markerStyle(h snapshot.Health) lipgloss.Style {
	switch h {
	case snapshot.Unhealthy:
		return badStyle
	case snapshot.Degraded:
		return warnStyle
	case snapshot.Empty:
		return dimStyle
	default:
		return okStyle
	}
}
```

`internal/ui/text.go`:

```go
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// trunc cuts s to at most w display cells.
func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "")
}

// pad right-pads s with spaces to w display cells (never truncates).
func pad(s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", max(int(d.Seconds()), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}
```

- [ ] **Step 5: Implement the list pane**

`internal/ui/listpane.go`:

```go
package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/regeda/txenvoy/internal/snapshot"
)

const (
	typeW  = 12
	countW = 7
	fixedW = 3 + 1 + typeW + 1 + countW // prefix, gap, type, gap, count
)

type match struct {
	idx  int   // index into listPane.all
	hits []int // matched rune positions in the name
}

type listPane struct {
	all           []snapshot.Cluster
	visible       []match
	cursor        int
	selected      string
	query         string
	filtering     bool
	waiting       bool
	input         textinput.Model
	width, height int
	offset        int
}

func newListPane() listPane {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.CharLimit = 100
	return listPane{input: ti}
}

type clusterNames []snapshot.Cluster

func (n clusterNames) String(i int) string { return n[i].Name }
func (n clusterNames) Len() int            { return len(n) }

func (l *listPane) SetSize(w, h int) {
	l.width, l.height = w, h
	l.input.Width = max(w-4, 1)
	l.clampOffset()
}

func (l *listPane) SetWaiting(v bool) { l.waiting = v }

func (l *listPane) SetClusters(cs []snapshot.Cluster) {
	l.all = cs
	l.refilter()
}

func (l *listPane) Selected() (snapshot.Cluster, bool) {
	if len(l.visible) == 0 {
		return snapshot.Cluster{}, false
	}
	return l.all[l.visible[l.cursor].idx], true
}

func (l *listPane) refilter() {
	vis := make([]match, 0, len(l.all))
	if l.query == "" {
		for i := range l.all {
			vis = append(vis, match{idx: i})
		}
	} else {
		for _, m := range fuzzy.FindFrom(l.query, clusterNames(l.all)) {
			vis = append(vis, match{idx: m.Index, hits: m.MatchedIndexes})
		}
	}
	l.visible = vis
	l.restoreCursor()
}

// restoreCursor keeps the selection on the same cluster name; if it is gone
// it falls to the nearest row.
func (l *listPane) restoreCursor() {
	for i, m := range l.visible {
		if l.all[m.idx].Name == l.selected {
			l.cursor = i
			l.clampOffset()
			return
		}
	}
	l.cursor = min(l.cursor, len(l.visible)-1)
	l.cursor = max(l.cursor, 0)
	l.selected = ""
	if len(l.visible) > 0 {
		l.selected = l.all[l.visible[l.cursor].idx].Name
	}
	l.clampOffset()
}

func (l *listPane) rows() int { return max(l.height-1, 1) }

func (l *listPane) clampOffset() {
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+l.rows() {
		l.offset = l.cursor - l.rows() + 1
	}
	l.offset = max(l.offset, 0)
}

func (l *listPane) move(d int) {
	if len(l.visible) == 0 {
		return
	}
	l.cursor = max(0, min(l.cursor+d, len(l.visible)-1))
	l.selected = l.all[l.visible[l.cursor].idx].Name
	l.clampOffset()
}

func (l *listPane) clearFilter() {
	l.filtering = false
	l.input.Blur()
	l.input.SetValue("")
	l.query = ""
	l.refilter()
}

func (l *listPane) HandleKey(k tea.KeyMsg) {
	if l.filtering {
		switch k.String() {
		case "enter":
			l.filtering = false
			l.input.Blur()
		case "esc":
			l.clearFilter()
		case "up", "ctrl+p":
			l.move(-1)
		case "down", "ctrl+n":
			l.move(1)
		default:
			l.input, _ = l.input.Update(k)
			l.query = l.input.Value()
			l.refilter()
		}
		return
	}
	switch k.String() {
	case "up", "k":
		l.move(-1)
	case "down", "j":
		l.move(1)
	case "pgup":
		l.move(-l.rows())
	case "pgdown":
		l.move(l.rows())
	case "home", "g":
		l.move(-len(l.visible))
	case "end", "G":
		l.move(len(l.visible))
	case "/":
		l.filtering = true
		l.input.Focus()
	case "esc":
		if l.query != "" {
			l.clearFilter()
		}
	}
}

func (l listPane) filterLine() string {
	if l.filtering {
		return l.input.View()
	}
	if l.query != "" {
		return dimStyle.Render("/ " + l.query)
	}
	return dimStyle.Render("/ to filter")
}

func (l listPane) row(i int) string {
	m := l.visible[i]
	c := l.all[m.idx]
	nameW := max(l.width-fixedW, 8)

	plain := trunc(c.Name, nameW)
	hit := map[int]bool{}
	for _, h := range m.hits {
		hit[h] = true
	}
	var name strings.Builder
	for ri, r := range []rune(plain) {
		s := string(r)
		switch {
		case hit[ri]:
			s = lipgloss.NewStyle().Bold(true).Underline(true).Render(s)
		case i == l.cursor:
			s = boldStyle.Render(s)
		}
		name.WriteString(s)
	}
	namePad := strings.Repeat(" ", max(nameW-lipgloss.Width(plain), 0))

	prefix := " "
	if i == l.cursor {
		prefix = ">"
	}
	count := fmt.Sprintf("%d/%d", c.HealthyCount(), len(c.Hosts))
	countCell := strings.Repeat(" ", max(countW-lipgloss.Width(count), 0)) + count
	typeCell := pad(trunc(c.Type, typeW), typeW)

	line := prefix + markerStyle(c.Health()).Render("●") + " " + name.String() + namePad + " " + typeCell + " " + countCell
	if c.Health() == snapshot.Empty {
		line = prefix + markerStyle(c.Health()).Render("●") + " " + name.String() + namePad + " " + dimStyle.Render(typeCell+" "+countCell)
	}
	return line
}

func (l listPane) View() string {
	lines := []string{l.filterLine()}
	if len(l.visible) == 0 {
		msg := "no clusters"
		switch {
		case l.waiting:
			msg = "waiting for Envoy…"
		case l.query != "":
			msg = "no matches"
		}
		lines = append(lines, dimStyle.Render(msg))
	}
	end := min(l.offset+l.rows(), len(l.visible))
	for i := l.offset; i < end; i++ {
		lines = append(lines, l.row(i))
	}
	for len(lines) < l.height {
		lines = append(lines, "")
	}
	return lipgloss.NewStyle().Width(max(l.width, 1)).MaxWidth(max(l.width, 1)).
		Height(max(l.height, 1)).MaxHeight(max(l.height, 1)).
		Render(strings.Join(lines, "\n"))
}
```

- [ ] **Step 6: Run the tests**

Run: `go test -race ./internal/ui/...`
Expected: PASS. If `TestLongAndWideNamesNeverExceedWidth` fails on wide-character width, fix `trunc`/`row` so that the visible width never exceeds the pane width, since the test is the contract.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(ui): add cluster list pane with fuzzy filter" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Endpoints table renderer

**Files:**
- Create: `internal/ui/endpoints.go`, `internal/ui/endpoints_test.go`

**Interfaces:**
- Consumes: `snapshot.Cluster`, `snapshot.Host`, `snapshot.IsUnhealthyFlag`, `trunc`, `pad`, styles.
- Produces: `renderEndpoints(c snapshot.Cluster, w, h, scroll int) string`. It returns exactly `h` lines: a summary line, a header line, then `h-2` host rows starting at `scroll` (clamped). `maxEndpointScroll(c snapshot.Cluster, h int) int`.

- [ ] **Step 1: Write the failing tests**

`internal/ui/endpoints_test.go`:

```go
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
```

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/ui/ -run Endpoints`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/ui/endpoints.go`:

```go
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
	addrW   = 22
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
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(ui): add endpoints table renderer" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Detail pane with Config tab and YAML highlighting

**Files:**
- Create: `internal/ui/highlight.go`, `internal/ui/detailpane.go`, `internal/ui/detailpane_test.go`

**Interfaces:**
- Consumes: `renderEndpoints`, `maxEndpointScroll` (Task 6), `snapshot.Cluster`.
- Produces: `highlightYAML(src string, color bool) string`; `tab` type with `tabEndpoints`, `tabConfig`; `newDetailPane(color bool) detailPane`; `(*detailPane).SetSize(w, h int)`, `SetCluster(c snapshot.Cluster, ok bool)`, `SetTab(tab)`, `HandleKey(tea.KeyMsg)`; `(detailPane).View() string`, which returns exactly `h` lines. Field `highlights int` counts how often chroma ran.

- [ ] **Step 1: Add chroma**

```bash
go get github.com/alecthomas/chroma/v2@latest
```

- [ ] **Step 2: Write the failing tests**

`internal/ui/detailpane_test.go`:

```go
package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/snapshot"
)

func configCluster() snapshot.Cluster {
	c := hostsCluster()
	c.Config = snapshot.ConfigInfo{
		YAML:        "name: api\ntype: EDS\nconnect_timeout: 5s\n",
		VersionInfo: "42",
		LastUpdated: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC),
	}
	return c
}

func newDetail(color bool) detailPane {
	d := newDetailPane(color)
	d.SetSize(100, 14)
	return d
}

func TestHighlightYAML(t *testing.T) {
	src := "name: api\ntype: EDS\n"
	if got := highlightYAML(src, false); got != "name: api\ntype: EDS" {
		t.Fatalf("plain: %q", got)
	}
	got := highlightYAML(src, true)
	if !strings.Contains(got, "\x1b[") || !strings.Contains(got, "name") || !strings.Contains(got, "EDS") {
		t.Fatalf("color: %q", got)
	}
	if highlightYAML("", true) != "" {
		t.Fatal("empty in, empty out")
	}
}

func TestNoClusterSelected(t *testing.T) {
	d := newDetail(false)
	d.SetCluster(snapshot.Cluster{}, false)
	if !strings.Contains(d.View(), "no cluster selected") {
		t.Fatal(d.View())
	}
	d.SetTab(tabConfig)
	if !strings.Contains(d.View(), "no cluster selected") {
		t.Fatal("config tab with no cluster")
	}
}

func TestEndpointsTabIsDefault(t *testing.T) {
	d := newDetail(false)
	d.SetCluster(configCluster(), true)
	v := d.View()
	if !strings.Contains(v, "10.0.0.1:80") || !strings.Contains(v, "api") {
		t.Fatal(v)
	}
	if n := len(strings.Split(v, "\n")); n != 14 {
		t.Fatalf("height %d, want 14", n)
	}
}

func TestConfigTabShowsVersionAndYAML(t *testing.T) {
	d := newDetail(false)
	d.SetCluster(configCluster(), true)
	d.SetTab(tabConfig)
	v := d.View()
	for _, want := range []string{"version_info: 42", "last_updated: 2026-10-14", "connect_timeout: 5s"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q in:\n%s", want, v)
		}
	}
	if n := len(strings.Split(v, "\n")); n != 14 {
		t.Fatalf("height %d, want 14", n)
	}
}

func TestConfigTabStaticAndError(t *testing.T) {
	c := configCluster()
	c.Config.Static, c.Config.VersionInfo = true, ""
	d := newDetail(false)
	d.SetCluster(c, true)
	d.SetTab(tabConfig)
	if !strings.Contains(d.View(), "static") || strings.Contains(d.View(), "version_info") {
		t.Fatal(d.View())
	}

	c.Config = snapshot.ConfigInfo{Err: "config unavailable: boom"}
	d.SetCluster(c, true)
	if !strings.Contains(d.View(), "config unavailable: boom") {
		t.Fatal(d.View())
	}
}

func TestHighlightIsCached(t *testing.T) {
	d := newDetail(true)
	c := configCluster()
	d.SetCluster(c, true)
	d.SetCluster(c, true)
	d.SetCluster(c, true)
	if d.highlights != 1 {
		t.Fatalf("highlighted %d times, want 1", d.highlights)
	}
	c.Config.YAML += "extra: 1\n"
	d.SetCluster(c, true)
	if d.highlights != 2 {
		t.Fatalf("changed YAML must re-highlight, got %d", d.highlights)
	}
}

func TestEndpointScrollResetsOnClusterChange(t *testing.T) {
	big := snapshot.Cluster{Name: "big"}
	for i := 0; i < 40; i++ {
		big.Hosts = append(big.Hosts, snapshot.Host{Address: "10.0.0." + string(rune('a'+i%26)) + ":80"})
	}
	d := newDetail(false)
	d.SetCluster(big, true)
	for i := 0; i < 100; i++ {
		d.HandleKey(key("j"))
	}
	if d.scroll != maxEndpointScroll(big, 13) {
		t.Fatalf("scroll = %d, want clamp %d", d.scroll, maxEndpointScroll(big, 13))
	}
	d.SetCluster(big, true) // same cluster: keep scroll
	if d.scroll == 0 {
		t.Fatal("refresh of the same cluster must keep scroll")
	}
	other := big
	other.Name = "other"
	d.SetCluster(other, true)
	if d.scroll != 0 {
		t.Fatal("a different cluster resets scroll")
	}
}

func TestDetailViewWidthIsBounded(t *testing.T) {
	d := newDetailPane(false)
	d.SetSize(40, 10)
	c := configCluster()
	c.Config.YAML = strings.Repeat("k: "+strings.Repeat("v", 200)+"\n", 3)
	d.SetCluster(c, true)
	for _, tb := range []tab{tabEndpoints, tabConfig} {
		d.SetTab(tb)
		for _, l := range strings.Split(d.View(), "\n") {
			if lipgloss.Width(l) > 40 {
				t.Fatalf("tab %d: width %d: %q", tb, lipgloss.Width(l), l)
			}
		}
	}
}
```

- [ ] **Step 3: Run and confirm failure**

Run: `go test ./internal/ui/ -run 'Highlight|Detail|Config|Endpoints|NoCluster'`
Expected: FAIL.

- [ ] **Step 4: Implement highlighting and the detail pane**

`internal/ui/highlight.go`:

```go
package ui

import (
	"strings"

	"github.com/alecthomas/chroma/v2/quick"
)

// highlightYAML colors YAML with chroma, or returns it plain when color is off.
func highlightYAML(src string, color bool) string {
	if !color || src == "" {
		return strings.TrimRight(src, "\n")
	}
	var b strings.Builder
	if err := quick.Highlight(&b, src, "yaml", "terminal256", "monokai"); err != nil {
		return strings.TrimRight(src, "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
```

`internal/ui/detailpane.go`:

```go
package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/snapshot"
)

type tab int

const (
	tabEndpoints tab = iota
	tabConfig
)

type detailPane struct {
	tab           tab
	cluster       snapshot.Cluster
	has           bool
	width, height int
	scroll        int // endpoints tab
	vp            viewport.Model
	color         bool

	cfgName, cfgYAML, cfgOut string
	highlights               int
}

func newDetailPane(color bool) detailPane {
	return detailPane{color: color, vp: viewport.New(0, 0)}
}

// Layout: 1 tab line, then a body of height-1 rows. The Config body is 1 header
// line plus the viewport.
func (d *detailPane) SetSize(w, h int) {
	d.width, d.height = w, h
	d.vp.Width = max(w, 1)
	d.vp.Height = max(h-2, 1)
}

func (d *detailPane) SetTab(t tab) { d.tab = t }

func (d *detailPane) SetCluster(c snapshot.Cluster, ok bool) {
	if !ok {
		d.has, d.cluster = false, snapshot.Cluster{}
		return
	}
	if !d.has || d.cluster.Name != c.Name {
		d.scroll = 0
	}
	d.cluster, d.has = c, true
	d.scroll = min(d.scroll, maxEndpointScroll(c, d.height-1))
	d.syncConfig()
}

func (d *detailPane) syncConfig() {
	c := d.cluster
	if d.cfgName != c.Name || d.cfgYAML != c.Config.YAML {
		nameChanged := d.cfgName != c.Name
		d.cfgOut = highlightYAML(c.Config.YAML, d.color)
		d.cfgName, d.cfgYAML = c.Name, c.Config.YAML
		d.highlights++
		d.vp.SetContent(d.cfgOut)
		if nameChanged {
			d.vp.GotoTop()
		}
	}
	if c.Config.Err != "" {
		d.vp.SetContent(dimStyle.Render(c.Config.Err))
	} else {
		d.vp.SetContent(d.cfgOut)
	}
}

func (d *detailPane) HandleKey(k tea.KeyMsg) {
	if !d.has {
		return
	}
	if d.tab == tabConfig {
		d.vp, _ = d.vp.Update(k)
		return
	}
	maxS := maxEndpointScroll(d.cluster, d.height-1)
	switch k.String() {
	case "up", "k":
		d.scroll--
	case "down", "j":
		d.scroll++
	case "pgup":
		d.scroll -= 10
	case "pgdown":
		d.scroll += 10
	case "home", "g":
		d.scroll = 0
	case "end", "G":
		d.scroll = maxS
	}
	d.scroll = max(0, min(d.scroll, maxS))
}

func (d detailPane) tabLine() string {
	label := func(name string, active bool) string {
		if active {
			return lipgloss.NewStyle().Bold(true).Underline(true).Render(name)
		}
		return dimStyle.Render(name)
	}
	return boldStyle.Render(d.cluster.Name) + "  ·  " +
		label("Endpoints", d.tab == tabEndpoints) + " " + dimStyle.Render("|") + " " + label("Config", d.tab == tabConfig)
}

func (d detailPane) configHeader() string {
	c := d.cluster.Config
	var parts []string
	if c.Static {
		parts = append(parts, "static (no xDS version)")
	} else if c.VersionInfo != "" {
		parts = append(parts, "version_info: "+c.VersionInfo)
	}
	if !c.LastUpdated.IsZero() {
		parts = append(parts, "last_updated: "+c.LastUpdated.UTC().Format("2006-01-02 15:04:05Z"))
	}
	return dimStyle.Render(strings.Join(parts, "   "))
}

func (d detailPane) View() string {
	var body string
	switch {
	case !d.has:
		body = dimStyle.Render("no cluster selected")
	case d.tab == tabConfig:
		body = d.tabLine() + "\n" + d.configHeader() + "\n" + d.vp.View()
	default:
		body = d.tabLine() + "\n" + renderEndpoints(d.cluster, d.width, d.height-1, d.scroll)
	}
	w, h := max(d.width, 1), max(d.height, 1)
	return lipgloss.NewStyle().Width(w).MaxWidth(w).Height(h).MaxHeight(h).Render(body)
}
```

- [ ] **Step 5: Run the tests**

Run: `go test -race ./internal/ui/`
Expected: PASS. If `TestDetailViewWidthIsBounded` fails because the viewport lets long lines through, wrap `d.vp.View()` in `lipgloss.NewStyle().MaxWidth(w).Render(...)` in `View`.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(ui): add detail pane with endpoints and highlighted config tabs" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Header, error banner overlay, status bar

**Files:**
- Create: `internal/ui/chrome.go`, `internal/ui/chrome_test.go`

**Interfaces:**
- Consumes: styles, `trunc`, `pad`.
- Produces:
  - `renderHeader(width int, compact bool, conn1, conn2, tab string) string`. Four lines normally: logo row 1, logo row 2, blank margin, tab. Two lines when compact: `txenvoy` plus connection, then tab. `tab` is the label only, for example `Clusters (3)`.
  - `bannerLines(text string, width, maxRows int) []string`: wrapped, at most `maxRows` rows, each exactly `width` cells wide, red background.
  - `overlay(frame string, banner []string) string`: replaces the first `len(banner)` lines of `frame`.
  - `renderStatus(width int, left, right string) string`: one line.
  - `joinEnds(left, right string, width int) string`.

- [ ] **Step 1: Write the failing tests**

`internal/ui/chrome_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestHeaderNormal(t *testing.T) {
	h := renderHeader(100, false, "● connected  http://x", "Envoy 1.31.0 · updated 2s ago", "Clusters (3)")
	lines := strings.Split(h, "\n")
	if len(lines) != 4 {
		t.Fatalf("lines=%d", len(lines))
	}
	if !strings.Contains(lines[0], "▀█▀ ▀▄▀") || !strings.Contains(lines[0], "● connected") {
		t.Fatalf("row1: %q", lines[0])
	}
	if !strings.Contains(lines[1], "updated 2s ago") {
		t.Fatalf("row2: %q", lines[1])
	}
	if strings.TrimSpace(lines[2]) != "" {
		t.Fatalf("row3 must be a blank margin: %q", lines[2])
	}
	if !strings.Contains(lines[3], "Clusters (3)") {
		t.Fatalf("row4: %q", lines[3])
	}
	for _, l := range lines {
		if lipgloss.Width(l) > 100 {
			t.Fatalf("too wide: %d", lipgloss.Width(l))
		}
	}
}

func TestHeaderCompact(t *testing.T) {
	h := renderHeader(80, true, "● connected  http://x", "row2 ignored", "Clusters (3)")
	lines := strings.Split(h, "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "txenvoy") || !strings.Contains(lines[1], "Clusters (3)") {
		t.Fatalf("%q", h)
	}
}

func TestBannerWrapsAndPads(t *testing.T) {
	text := "✗ disconnected · last ok 14s ago\nGET /clusters: dial tcp 127.0.0.1:15000: connect: connection refused"
	rows := bannerLines(text, 40, 4)
	if len(rows) < 3 || len(rows) > 4 {
		t.Fatalf("rows=%d: %q", len(rows), rows)
	}
	for _, r := range rows {
		if lipgloss.Width(r) != 40 {
			t.Fatalf("row width %d != 40: %q", lipgloss.Width(r), r)
		}
	}
	if !strings.Contains(rows[0], "disconnected") {
		t.Fatal(rows[0])
	}
}

func TestBannerTruncatesWithEllipsis(t *testing.T) {
	long := strings.Repeat("error words that go on and on ", 50)
	rows := bannerLines(long, 50, 4)
	if len(rows) != 4 {
		t.Fatalf("rows=%d", len(rows))
	}
	if !strings.Contains(rows[3], "…") {
		t.Fatalf("last row must end with an ellipsis: %q", rows[3])
	}
	for _, r := range rows {
		if lipgloss.Width(r) != 50 {
			t.Fatalf("width %d", lipgloss.Width(r))
		}
	}
}

func TestBannerUnbrokenLongTokenAndHugeText(t *testing.T) {
	rows := bannerLines(strings.Repeat("x", 500), 30, 3)
	if len(rows) != 3 {
		t.Fatalf("rows=%d", len(rows))
	}
	for _, r := range rows {
		if lipgloss.Width(r) != 30 {
			t.Fatalf("width %d", lipgloss.Width(r))
		}
	}
	if len(bannerLines("", 30, 3)) != 1 {
		t.Fatal("empty text still yields one row")
	}
}

func TestOverlayReplacesOnlyTopLines(t *testing.T) {
	frame := strings.Join([]string{"l1", "l2", "l3", "l4", "l5"}, "\n")
	got := strings.Split(overlay(frame, []string{"B1", "B2"}), "\n")
	if len(got) != 5 || got[0] != "B1" || got[1] != "B2" || got[2] != "l3" || got[4] != "l5" {
		t.Fatalf("%q", got)
	}
	if overlay(frame, nil) != frame {
		t.Fatal("no banner leaves the frame untouched")
	}
}

func TestStatusLine(t *testing.T) {
	s := renderStatus(60, " 3 clusters · 1 unhealthy", "/ filter · q quit")
	if strings.Contains(s, "\n") || lipgloss.Width(s) > 60 || !strings.Contains(s, "3 clusters") {
		t.Fatalf("%q", s)
	}
	narrow := renderStatus(20, " 3 clusters · 1 unhealthy", "/ filter · q quit")
	if lipgloss.Width(narrow) > 20 {
		t.Fatalf("narrow width %d", lipgloss.Width(narrow))
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/ui/ -run 'Header|Banner|Overlay|Status'`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/ui/chrome.go`:

```go
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	logoRow1 = "▀█▀ ▀▄▀ █▀▀ █▄ █ █ █ █▀█ █ █"
	logoRow2 = " █  █ █ ██▄ █ ▀█ ▀▄▀ █▄█  █"
)

var bannerStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("1")).
	Foreground(lipgloss.Color("15")).
	Bold(true)

// joinEnds puts left at the left edge and right at the right edge of one line.
func joinEnds(left, right string, width int) string {
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return lipgloss.NewStyle().MaxWidth(width).Render(left + strings.Repeat(" ", gap) + right)
}

func renderHeader(width int, compact bool, conn1, conn2, tab string) string {
	tabLine := " " + lipgloss.NewStyle().Bold(true).Reverse(true).Render(" "+tab+" ")
	if compact {
		return joinEnds(logoStyle.Render("txenvoy"), conn1, width) + "\n" + tabLine
	}
	return strings.Join([]string{
		joinEnds(logoStyle.Render(logoRow1), conn1, width),
		joinEnds(logoStyle.Render(logoRow2), conn2, width),
		"",
		tabLine,
	}, "\n")
}

// bannerLines wraps text to width, caps it at maxRows (ending with an
// ellipsis when cut) and paints each row full width on a red background.
func bannerLines(text string, width, maxRows int) []string {
	inner := max(width-2, 1)
	rows := strings.Split(ansi.Wrap(text, inner, ""), "\n")
	if len(rows) > maxRows {
		rows = rows[:maxRows]
		last := strings.TrimRight(ansi.Truncate(rows[maxRows-1], max(inner-1, 1), ""), " ")
		rows[maxRows-1] = last + "…"
	}
	style := bannerStyle.Width(width)
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = style.Render(" " + r)
	}
	return out
}

// overlay paints banner over the first len(banner) lines of frame. The frame
// was laid out as if the banner did not exist, so nothing below it moves.
func overlay(frame string, banner []string) string {
	if len(banner) == 0 {
		return frame
	}
	lines := strings.Split(frame, "\n")
	for i, b := range banner {
		if i < len(lines) {
			lines[i] = b
		} else {
			lines = append(lines, b)
		}
	}
	return strings.Join(lines, "\n")
}

func renderStatus(width int, left, right string) string {
	return dimStyle.Render(joinEnds(left, right, width))
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/ui/`
Expected: PASS. If a banner row comes out wider than `width` (a lipgloss version difference in `Width` wrapping), pre-truncate each row with `trunc(" "+r, width)` before `style.Render`.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(ui): add header, error banner overlay and status bar" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Root model: state machine, layout, keys, View

**Files:**
- Create: `internal/ui/model.go`, `internal/ui/model_test.go`

**Interfaces:**
- Consumes: everything from Tasks 4–8, `config.Config`.
- Produces:

```go
type Fetcher func(ctx context.Context) (snapshot.Snapshot, error)
type Options struct {
    Ctx    context.Context
    Config config.Config
    Fetch  Fetcher
    Clock  func() time.Time // defaults to time.Now
    Color  bool
}
func New(Options) Model       // implements tea.Model; starts with a fetch in flight
type snapshotMsg struct{ snap snapshot.Snapshot; err error }
type tickMsg struct{}
```

Behavior: constants `minWidth=80`, `minHeight=12`, `compactHeight=24`, `maxBannerRows=4`. Keys: `q`/`ctrl+c` quit (ctrl+c always), `r` refresh now, `x` dismiss banner, `?` toggle help, `tab` toggle focus, `c`/`e` switch tab, `/` focuses the list filter. While the list filter is active, every key except `ctrl+c` goes to the filter.

- [ ] **Step 1: Write the failing tests**

`internal/ui/model_test.go`:

```go
package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/config"
	"github.com/regeda/txenvoy/internal/snapshot"
)

var fixedNow = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

func testOpts(fetch Fetcher) Options {
	return Options{
		Ctx:    context.Background(),
		Config: config.Config{Admin: "http://envoy:15000", Refresh: time.Millisecond, Timeout: time.Second},
		Fetch:  fetch,
		Clock:  func() time.Time { return fixedNow.Add(2 * time.Second) },
	}
}

func send(m Model, msg tea.Msg) Model {
	nm, _ := m.Update(msg)
	return nm.(Model)
}

func sendCmd(m Model, msg tea.Msg) (Model, tea.Cmd) {
	nm, cmd := m.Update(msg)
	return nm.(Model), cmd
}

// run executes a cmd (recursing into batches) and returns the messages produced.
func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, run(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func snap(cs ...snapshot.Cluster) snapshot.Snapshot {
	return snapshot.Snapshot{Clusters: cs, Version: "1.31.0", FetchedAt: fixedNow}
}

func sized(m Model, w, h int) Model { return send(m, tea.WindowSizeMsg{Width: w, Height: h}) }

func TestInitStartsFetchAndTick(t *testing.T) {
	calls := 0
	m := New(testOpts(func(context.Context) (snapshot.Snapshot, error) { calls++; return snap(), nil }))
	var gotSnap, gotTick bool
	for _, msg := range run(m.Init()) {
		switch msg.(type) {
		case snapshotMsg:
			gotSnap = true
		case tickMsg:
			gotTick = true
		}
	}
	if !gotSnap || !gotTick || calls != 1 {
		t.Fatalf("snap=%v tick=%v calls=%d", gotSnap, gotTick, calls)
	}
}

func TestTickDoesNotOverlapFetches(t *testing.T) {
	calls := 0
	m := New(testOpts(func(context.Context) (snapshot.Snapshot, error) { calls++; return snap(), nil }))
	// New is in flight already: a tick must only reschedule.
	m, cmd := sendCmd(m, tickMsg{})
	for _, msg := range run(cmd) {
		if _, ok := msg.(snapshotMsg); ok {
			t.Fatal("tick started a fetch while one was in flight")
		}
	}
	if calls != 0 {
		t.Fatalf("calls=%d", calls)
	}
	m = send(m, snapshotMsg{snap: snap()}) // fetch lands
	m, cmd = sendCmd(m, tickMsg{})
	var fetched bool
	for _, msg := range run(cmd) {
		if _, ok := msg.(snapshotMsg); ok {
			fetched = true
		}
	}
	if !fetched || calls != 1 {
		t.Fatalf("fetched=%v calls=%d", fetched, calls)
	}
	_ = m
}

func TestConnectionStateMachine(t *testing.T) {
	m := New(testOpts(nil))
	if m.state != stateConnecting || m.bannerVisible() {
		t.Fatal("starts connecting without a banner")
	}
	m = send(m, snapshotMsg{err: errors.New("dial refused")})
	if m.state != stateDisconnected || !m.bannerVisible() || m.hasSnap {
		t.Fatalf("first failure: state=%v banner=%v", m.state, m.bannerVisible())
	}
	m = send(m, snapshotMsg{snap: snap(cl("api", 1, 1))})
	if m.state != stateConnected || m.bannerVisible() || !m.hasSnap {
		t.Fatal("recovery clears the banner")
	}
	m = send(m, snapshotMsg{err: errors.New("boom")})
	if m.state != stateDisconnected || len(m.snap.Clusters) != 1 {
		t.Fatal("failure keeps the last good snapshot")
	}
}

func TestBannerDismissLifecycle(t *testing.T) {
	m := New(testOpts(nil))
	m = send(m, snapshotMsg{err: errors.New("boom")})
	m = send(m, key("x"))
	if m.bannerVisible() {
		t.Fatal("x dismisses")
	}
	m = send(m, snapshotMsg{err: errors.New("boom")})
	if m.bannerVisible() {
		t.Fatal("same error stays dismissed")
	}
	m = send(m, snapshotMsg{err: errors.New("different")})
	if !m.bannerVisible() {
		t.Fatal("a new error re-shows the banner")
	}
	m = send(m, key("x"))
	m = send(m, snapshotMsg{snap: snap()})
	m = send(m, snapshotMsg{err: errors.New("different")})
	if !m.bannerVisible() {
		t.Fatal("after recovery the same error shows again")
	}
}

func TestRefreshKey(t *testing.T) {
	calls := 0
	m := New(testOpts(func(context.Context) (snapshot.Snapshot, error) { calls++; return snap(), nil }))
	m = send(m, snapshotMsg{snap: snap()})
	m, cmd := sendCmd(m, key("r"))
	run(cmd)
	if calls != 1 || !m.inflight {
		t.Fatalf("calls=%d inflight=%v", calls, m.inflight)
	}
	_, cmd = sendCmd(m, key("r")) // already in flight
	if cmd != nil {
		t.Fatal("r while in flight must not start another fetch")
	}
}

func TestQuitKeys(t *testing.T) {
	m := New(testOpts(nil))
	if _, cmd := sendCmd(m, key("q")); cmd == nil || cmd() != tea.Quit() {
		t.Fatal("q quits")
	}
	m = send(m, key("/")) // filtering: q is text
	if _, cmd := sendCmd(m, key("q")); cmd != nil {
		t.Fatal("q must not quit while filtering")
	}
	if _, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Fatal("ctrl+c always quits")
	}
}

func TestSelectionDrivesDetailAndKeys(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snap(cl("alpha", 1, 1), cl("beta", 1, 1))})
	if !strings.Contains(m.View(), "alpha") {
		t.Fatal(m.View())
	}
	m = send(m, key("down"))
	if c, _ := m.list.Selected(); c.Name != "beta" {
		t.Fatal("down selects beta")
	}
	if !strings.Contains(m.detail.View(), "beta") {
		t.Fatal("detail follows the selection")
	}
	m = send(m, key("c"))
	if m.detail.tab != tabConfig {
		t.Fatal("c opens config")
	}
	m = send(m, key("e"))
	if m.detail.tab != tabEndpoints {
		t.Fatal("e opens endpoints")
	}
	m = send(m, key("tab"))
	if m.focus != focusDetail {
		t.Fatal("tab moves focus")
	}
	m = send(m, key("/"))
	if m.focus != focusList || !m.list.filtering {
		t.Fatal("/ focuses the list filter")
	}
}

func TestEmptyAndFilteredToNothing(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snap()})
	if v := m.View(); !strings.Contains(v, "no cluster selected") || !strings.Contains(v, "no clusters") {
		t.Fatal(v)
	}
	m = send(m, snapshotMsg{snap: snap(cl("api", 1, 1))})
	m = send(m, key("/"))
	for _, r := range "zzz" {
		m = send(m, key(string(r)))
	}
	if v := m.View(); !strings.Contains(v, "no matches") || !strings.Contains(v, "no cluster selected") {
		t.Fatal(v)
	}
}

func TestTinyAndZeroSizedTerminals(t *testing.T) {
	m := New(testOpts(nil)) // View before any WindowSizeMsg
	if !strings.Contains(m.View(), "terminal too small") {
		t.Fatal(m.View())
	}
	for _, sz := range [][2]int{{0, 0}, {1, 1}, {79, 40}, {200, 11}, {80, 12}} {
		mm := send(sized(m, sz[0], sz[1]), snapshotMsg{snap: snap(cl("api", 1, 1))})
		_ = mm.View() // must not panic
		mm = send(mm, snapshotMsg{err: errors.New("boom")})
		_ = mm.View()
	}
	// shrinking then growing again recovers
	mm := sized(sized(sized(m, 120, 30), 10, 5), 120, 30)
	mm = send(mm, snapshotMsg{snap: snap(cl("api", 1, 1))})
	if strings.Contains(mm.View(), "too small") {
		t.Fatal("must recover after resize")
	}
}

func TestViewFitsTheTerminal(t *testing.T) {
	for _, sz := range [][2]int{{120, 30}, {80, 24}, {100, 20}, {80, 12}, {200, 60}} {
		m := sized(New(testOpts(nil)), sz[0], sz[1])
		m = send(m, snapshotMsg{snap: snap(cl("api", 1, 2), cl("db", 2, 2))})
		for _, withErr := range []bool{false, true} {
			if withErr {
				m = send(m, snapshotMsg{err: errors.New(strings.Repeat("long error ", 40))})
			}
			lines := strings.Split(m.View(), "\n")
			if len(lines) != sz[1] {
				t.Fatalf("%v err=%v: %d lines, want %d", sz, withErr, len(lines), sz[1])
			}
			for _, l := range lines {
				if lipgloss.Width(l) > sz[0] {
					t.Fatalf("%v err=%v: line width %d: %q", sz, withErr, lipgloss.Width(l), l)
				}
			}
		}
	}
}

func TestBannerDoesNotMoveContent(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snap(cl("api", 1, 2), cl("db", 2, 2))})
	before := strings.Split(m.View(), "\n")

	m = send(m, snapshotMsg{err: errors.New(strings.Repeat("multi line error ", 30))})
	after := strings.Split(m.View(), "\n")
	if len(before) != len(after) {
		t.Fatal("banner changed the frame height")
	}
	rows := len(bannerLines(m.bannerText(), 120, maxBannerRows))
	if rows < 2 || rows > maxBannerRows {
		t.Fatalf("banner rows = %d", rows)
	}
	for i := rows; i < len(before); i++ {
		// status bar text is unchanged by the error, and panes must not move
		if before[i] != after[i] {
			t.Fatalf("line %d moved:\n%q\n%q", i, before[i], after[i])
		}
	}
	if !strings.Contains(after[0], "disconnected") {
		t.Fatalf("banner first row: %q", after[0])
	}
}

func TestConnectionBlock(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	if v := m.View(); !strings.Contains(v, "connecting") {
		t.Fatal(v)
	}
	m = send(m, snapshotMsg{snap: snap(cl("api", 1, 1))})
	v := m.View()
	for _, want := range []string{"● connected", "http://envoy:15000", "Envoy 1.31.0", "updated 2s ago", "Clusters (1)"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q in:\n%s", want, v)
		}
	}
	if !strings.Contains(v, "1 clusters") && !strings.Contains(v, "1 cluster") {
		t.Fatalf("status counts missing:\n%s", v)
	}
}

func TestHelpToggle(t *testing.T) {
	m := sized(New(testOpts(nil)), 160, 30)
	short := m.View()
	m = send(m, key("?"))
	if m.View() == short || !m.help {
		t.Fatal("? changes the hint line")
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/ui/ -run 'Init|Tick|Connection|Banner|Refresh|Quit|Selection|Empty|Tiny|ViewFits|Help'`
Expected: FAIL.

- [ ] **Step 3: Implement the model**

`internal/ui/model.go`:

```go
// Package ui is the bubbletea front end. It only knows snapshot types.
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/config"
	"github.com/regeda/txenvoy/internal/snapshot"
)

const (
	minWidth      = 80
	minHeight     = 12
	compactHeight = 24
	maxBannerRows = 4
	footerRows    = 1
)

type Fetcher func(ctx context.Context) (snapshot.Snapshot, error)

type Options struct {
	Ctx    context.Context
	Config config.Config
	Fetch  Fetcher
	Clock  func() time.Time
	Color  bool
}

type connState int

const (
	stateConnecting connState = iota
	stateConnected
	stateDisconnected
)

type focus int

const (
	focusList focus = iota
	focusDetail
)

type snapshotMsg struct {
	snap snapshot.Snapshot
	err  error
}

type tickMsg struct{}

type Model struct {
	opts Options

	snap      snapshot.Snapshot
	hasSnap   bool
	err       error
	dismissed string
	lastOK    time.Time
	inflight  bool
	state     connState

	focus         focus
	help          bool
	width, height int
	list          listPane
	detail        detailPane
}

// New returns a model whose first fetch is already considered in flight; Init
// issues it.
func New(o Options) Model {
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Ctx == nil {
		o.Ctx = context.Background()
	}
	m := Model{opts: o, inflight: true, state: stateConnecting, list: newListPane(), detail: newDetailPane(o.Color)}
	m.list.SetWaiting(true)
	return m
}

func (m Model) fetchCmd() tea.Cmd {
	fetch, ctx := m.opts.Fetch, m.opts.Ctx
	return func() tea.Msg {
		s, err := fetch(ctx)
		return snapshotMsg{snap: s, err: err}
	}
}

func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(m.opts.Config.Refresh, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.fetchCmd(), m.tickCmd()) }

func (m Model) headerRows() int {
	if m.height < compactHeight {
		return 2
	}
	return 4
}

func (m Model) paneHeight() int { return max(m.height-m.headerRows()-footerRows, 1) }

func (m *Model) layout() {
	listW := min(max(m.width*35/100, 28), 50)
	detailW := max(m.width-listW-1, 1)
	m.list.SetSize(listW, m.paneHeight())
	m.detail.SetSize(detailW, m.paneHeight())
}

func (m *Model) syncDetail() {
	c, ok := m.list.Selected()
	m.detail.SetCluster(c, ok)
}

func (m Model) bannerVisible() bool {
	return m.err != nil && m.err.Error() != m.dismissed
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.syncDetail()
		return m, nil

	case tickMsg:
		if m.inflight {
			return m, m.tickCmd()
		}
		m.inflight = true
		return m, tea.Batch(m.fetchCmd(), m.tickCmd())

	case snapshotMsg:
		m.inflight = false
		if msg.err != nil {
			m.err = msg.err
			m.state = stateDisconnected
			return m, nil
		}
		m.err, m.dismissed = nil, ""
		m.state = stateConnected
		m.snap, m.hasSnap = msg.snap, true
		m.lastOK = msg.snap.FetchedAt
		m.list.SetWaiting(false)
		m.list.SetClusters(msg.snap.Clusters)
		m.syncDetail()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.focus == focusList && m.list.filtering {
		m.list.HandleKey(k)
		m.syncDetail()
		return m, nil
	}
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "r":
		if !m.inflight {
			m.inflight = true
			return m, m.fetchCmd()
		}
	case "x":
		if m.err != nil {
			m.dismissed = m.err.Error()
		}
	case "?":
		m.help = !m.help
	case "tab":
		if m.focus == focusList {
			m.focus = focusDetail
		} else {
			m.focus = focusList
		}
	case "c":
		m.detail.SetTab(tabConfig)
	case "e":
		m.detail.SetTab(tabEndpoints)
	default:
		if k.String() == "/" {
			m.focus = focusList
		}
		if m.focus == focusList {
			m.list.HandleKey(k)
			m.syncDetail()
		} else {
			m.detail.HandleKey(k)
		}
	}
	return m, nil
}

func (m Model) connLines() (string, string) {
	admin := m.opts.Config.Admin
	since := func() string { return ago(m.opts.Clock().Sub(m.lastOK)) }
	switch m.state {
	case stateConnected:
		l2 := "updated " + since()
		if m.snap.Version != "" {
			l2 = "Envoy " + m.snap.Version + " · " + l2
		}
		return okStyle.Render("● connected") + "  " + admin, dimStyle.Render(l2)
	case stateDisconnected:
		l2 := "never connected"
		if !m.lastOK.IsZero() {
			l2 = "last ok " + since()
		}
		return badStyle.Render("✗ disconnected") + "  " + admin, dimStyle.Render(l2)
	default:
		return dimStyle.Render("◌ connecting…") + "  " + admin, ""
	}
}

func (m Model) bannerText() string {
	first := "✗ disconnected · " + m.opts.Config.Admin
	if m.lastOK.IsZero() {
		first += " · never connected"
	} else {
		first += " · last ok " + ago(m.opts.Clock().Sub(m.lastOK))
	}
	return first + "\n" + m.err.Error() + "\n(retrying every " + m.opts.Config.Refresh.String() + ")"
}

func (m Model) statusLine() string {
	left := fmt.Sprintf(" %d clusters · %d unhealthy", len(m.snap.Clusters), m.snap.UnhealthyClusters())
	if m.inflight {
		left += " ↻"
	}
	right := "/ filter · c config · e endpoints · r refresh · ? help · q quit "
	if m.help {
		right = "↑↓/jk move · / filter · tab focus · c config · e endpoints · r refresh · x dismiss · q quit "
	}
	return renderStatus(m.width, left, right)
}

func (m Model) View() string {
	if m.width < minWidth || m.height < minHeight {
		return fmt.Sprintf("terminal too small (%dx%d), need at least %dx%d", m.width, m.height, minWidth, minHeight)
	}
	c1, c2 := m.connLines()
	header := renderHeader(m.width, m.height < compactHeight, c1, c2, fmt.Sprintf("Clusters (%d)", len(m.snap.Clusters)))

	paneH := m.paneHeight()
	sep := dimStyle.Render(strings.TrimSuffix(strings.Repeat("│\n", paneH), "\n"))
	panes := lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), sep, m.detail.View())

	frame := header + "\n" + panes + "\n" + m.statusLine()
	if m.bannerVisible() {
		rows := min(maxBannerRows, max(1, m.height/3))
		frame = overlay(frame, bannerLines(m.bannerText(), m.width, rows))
	}
	return frame
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/ui/`
Expected: PASS. Typical fixes if a test fails: a frame whose line count is not `height` (check `headerRows` against the lines `renderHeader` really emits), or a line wider than the terminal (pane widths: `listW + 1 + detailW` must equal `width`).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(ui): add root model with polling, layout, keys and error banner" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Golden tests and end-to-end test

**Files:**
- Create: `internal/ui/golden_test.go`, `internal/ui/e2e_test.go`, `internal/ui/testdata/*.golden` (generated)

**Interfaces:**
- Consumes: `New`, `Options`, `snapshotMsg`, helpers from Task 9's test file (`send`, `sized`, `fixedNow`, `testOpts`), `demo`, `admin`, `snapshot`.

- [ ] **Step 1: Add teatest**

```bash
go get github.com/charmbracelet/x/exp/teatest@latest
```

- [ ] **Step 2: Write the golden tests**

`internal/ui/golden_test.go`:

```go
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

	"github.com/regeda/txenvoy/internal/admin"
	"github.com/regeda/txenvoy/internal/demo"
	"github.com/regeda/txenvoy/internal/snapshot"
)

func init() {
	// Deterministic output: no colors in goldens.
	lipgloss.SetColorProfile(termenv.Ascii)
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
	m := sized(New(testOpts(nil)), w, h)
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

func TestGoldenConnecting(t *testing.T) { golden(t, sized(New(testOpts(nil)), 120, 30)) }

func TestGoldenErrorFirstFetch(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
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
```

- [ ] **Step 3: Generate the goldens and inspect them**

Run: `go test ./internal/ui/ -run Golden -update`
Then read every file in `internal/ui/testdata/` and check it by eye against the spec (§5):
- the header has the logo, connection block, a blank margin row, and a `Clusters (N)` tab;
- the list is sorted unhealthy first, with `healthy/total` counts;
- the endpoints table has all seven columns and unhealthy hosts first;
- the Config tab shows `version_info` and `last_updated` (or `static`);
- in the error goldens the banner covers the top rows, the rows below it match the non-error golden, and long text ends in `…`;
- no line is wider than the terminal.

If anything is wrong, fix the code, not the golden, and regenerate.

- [ ] **Step 4: Run the goldens without `-update`**

Run: `go test -race ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Write the end-to-end test**

`internal/ui/e2e_test.go`:

```go
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

func waitFor(t *testing.T, tm *teatest.TestModel, want string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte(want)) },
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

	waitFor(t, tm, "● connected")
	waitFor(t, tm, "api-gateway")

	fake.Inject(demo.Fault{Status: 503})
	waitFor(t, tm, "✗ disconnected")
	waitFor(t, tm, "503")

	fake.Inject(demo.Fault{})
	waitFor(t, tm, "● connected")

	tm.Send(key("q"))
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}
```

- [ ] **Step 6: Run everything**

Run: `go test -race -count=3 ./...`
Expected: PASS on every repetition (the e2e test must not be flaky). If it flakes, lengthen `WithDuration`, never shorten `--refresh` below what the test needs.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "test(ui): add golden snapshots and end-to-end failure/recovery test" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Wire the app, demo jitter, README, final checks

**Files:**
- Modify: `cmd/txenvoy/run.go`, `internal/demo/server.go`, `internal/demo/server_test.go`, `README.md`

**Interfaces:**
- Consumes: `admin.NewHTTP`, `snapshot.Fetch`, `ui.New`, `ui.Options`, `demo.NewServer`.
- Produces: `(*demo.Server).Jitter(ctx context.Context, every time.Duration)`, which toggles a host of the `search` cluster between healthy and `failed_active_health_check` on each tick until `ctx` is cancelled.

- [ ] **Step 1: Write the failing Jitter test**

Append to `internal/demo/server_test.go`:

```go
func TestJitterTogglesHealth(t *testing.T) {
	s := NewServer(Degraded())
	srv := httptest.NewServer(s)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, before := get(t, srv.URL+"/clusters?format=json")
	go s.Jitter(ctx, 10*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, now := get(t, srv.URL+"/clusters?format=json"); now != before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("jitter never changed the cluster health")
}
```

Add `"context"` to the test file's imports.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/demo/ -run Jitter`
Expected: FAIL (`Jitter` undefined).

- [ ] **Step 3: Implement Jitter**

Append to `internal/demo/server.go` (add `"context"` to its imports):

```go
// Jitter flips the first host of the "search" cluster between healthy and
// failing health checks every interval, so --demo shows live changes.
func (s *Server) Jitter(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			failing = !failing
			s.Update(func(sc *Scenario) {
				for i := range sc.Clusters {
					if sc.Clusters[i].Name != "search" || len(sc.Clusters[i].Hosts) == 0 {
						continue
					}
					if failing {
						sc.Clusters[i].Hosts[0].Health = map[string]any{"failed_active_health_check": true, "eds_health_status": "UNHEALTHY"}
					} else {
						sc.Clusters[i].Hosts[0].Health = nil
					}
				}
			})
		}
	}
}
```

- [ ] **Step 4: Run the demo tests**

Run: `go test -race ./internal/demo/...`
Expected: PASS.

- [ ] **Step 5: Wire the real program**

Replace `cmd/txenvoy/run.go`:

```go
package main

import (
	"context"
	"errors"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/regeda/txenvoy/internal/admin"
	"github.com/regeda/txenvoy/internal/config"
	"github.com/regeda/txenvoy/internal/demo"
	"github.com/regeda/txenvoy/internal/snapshot"
	"github.com/regeda/txenvoy/internal/ui"
)

func run(ctx context.Context, cfg config.Config) error {
	if cfg.Demo {
		srv := demo.NewServer(demo.Degraded())
		url, stop, err := srv.Start()
		if err != nil {
			return err
		}
		defer stop()
		cfg.Admin = url
		go srv.Jitter(ctx, 2*cfg.Refresh)
	}

	client, err := admin.NewHTTP(cfg.Admin, cfg.Timeout)
	if err != nil {
		return err
	}
	model := ui.New(ui.Options{
		Ctx:    ctx,
		Config: cfg,
		Fetch:  func(ctx context.Context) (snapshot.Snapshot, error) { return snapshot.Fetch(ctx, client) },
		Color:  os.Getenv("NO_COLOR") == "",
	})
	_, err = tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
```

- [ ] **Step 6: Write the README**

`README.md`:

````markdown
# txenvoy

A real-time terminal UI for Envoy. The first feature is a **cluster explorer with live endpoint health**:
cluster list on the left, details on the right, and a header showing the connection state and errors.

- Cluster list: name, type (EDS / STRICT_DNS / ...), healthy/total hosts, and a colored marker when any host is unhealthy or ejected. `/` fuzzy-filters.
- Endpoints tab: address, health flags (`FAILED_ACTIVE_HC`, `FAILED_OUTLIER_CHECK`, `EDS_STATUS_UNHEALTHY`, ...), weight, zone, `rq_active` / `rq_error` / `cx_active`.
- Config tab (`c`): the cluster's config from `/config_dump` as highlighted YAML, with `version_info` and `last_updated`.
- Auto-refresh; the UI never blocks on HTTP. Errors appear as a red banner over the top of the screen and clear when Envoy answers again.

## Install and run

```sh
make build            # bin/txenvoy
make run              # try it with the built-in fake Envoy, no cluster needed
bin/txenvoy --admin http://localhost:15000
```

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--admin` | `http://localhost:15000` | Envoy admin base URL |
| `--refresh` | `3s` | Poll interval (minimum `500ms`) |
| `--timeout` | `3s` | Per-request timeout for each admin call |
| `--demo` | off | Use a built-in fake admin server (`--admin` is ignored) |

`txenvoy version` prints the version. Set `NO_COLOR=1` to disable colors.

## Keys

| Key | Action |
|---|---|
| `↑/↓`, `j/k` | move in the list, or scroll the focused detail pane |
| `/` | fuzzy filter (`Enter` keeps it, `Esc` clears it) |
| `Tab` | switch focus between list and detail |
| `c` / `e` | Config / Endpoints tab |
| `r` | refresh now |
| `x` | dismiss the error banner |
| `?` | toggle the full key list |
| `q`, `Ctrl+C` | quit |

## Running against a Kubernetes pod

txenvoy reads the Envoy admin API, so forward the admin port to your machine and point `--admin` at it.

1. **Plain pod or deployment.** `deploy/<name>` or `svc/<name>` also work as the forward target.

   ```sh
   kubectl -n <ns> port-forward pod/<pod> 15000:15000
   txenvoy --admin http://localhost:15000
   ```

2. **Istio sidecar.** The admin port is `15000` on the `istio-proxy` container, so the same forward works.
   `istioctl dashboard envoy <pod>.<ns>` is an alternative that opens a forward for you.

3. **Standalone Envoy.** The admin port is whatever `admin.address` says in the bootstrap, often `9901`. Map it:

   ```sh
   kubectl -n <ns> port-forward pod/<pod> 15000:9901
   ```

4. **Caveats.**
   - Keep the forward running in another terminal.
   - The admin interface is unauthenticated: bind to `127.0.0.1` only and never expose it.
   - If the forward drops, txenvoy shows the error banner and recovers by itself once the forward is back.

A built-in `--pod <ns>/<name>` using client-go port-forward is planned. It will sit behind the same `admin.Client` interface.

## Development

```sh
make test          # go test -race ./...
make test-update   # regenerate the teatest golden files in internal/ui/testdata
make check         # gofmt check + lint + tests
make help          # all targets
```

The design is in `docs/superpowers/specs/` and the implementation plan in `docs/superpowers/plans/`.
````

- [ ] **Step 7: Full verification**

Run: `make check`
Expected: formatting clean, lint clean, all tests PASS. Fix findings in the code.

- [ ] **Step 8: Smoke-test the real binary**

Run: `make build`
Then run `bin/txenvoy --refresh 100ms --timeout 0s` and confirm it exits non-zero with `--timeout must be positive` before any UI starts.
Then run `make run` in a real terminal and confirm by eye:
- the header (logo, `● connected`, `Envoy 1.31.0`, `Clusters (6)`) renders and the margin row sits above the tab;
- `orders-db` and `api-gateway` sort to the top with red and yellow markers;
- the `search` cluster's marker flips every few seconds (the jitter);
- `/` filters, `c` shows highlighted YAML with `version_info`, and `e` returns to the table;
- stopping the demo is not possible from outside, so check the error path by running `bin/txenvoy --admin http://localhost:1` instead: the red banner covers the top rows, the panes stay put, and `x` dismisses it.

Report what you saw. If a step cannot be checked in the current environment, say so rather than claiming it works.

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "feat: wire the CLI to the UI, add demo jitter and README" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```
