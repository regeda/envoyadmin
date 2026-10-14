# txenvoy: Cluster Explorer (v1) Design

Date: 2026-10-14
Module: `github.com/regeda/txenvoy`

## 1. Goal

A real-time terminal UI for inspecting Envoy. v1 is a **cluster explorer with live endpoint health**. It answers "why is traffic failing to this backend?" without reading a raw `/config_dump`.

Success criteria:

- Unhealthy hosts, outlier ejections and stale xDS versions are visible at a glance.
- Hundreds of clusters stay navigable through a fuzzy filter.
- The UI never blocks on HTTP and recovers by itself after Envoy or the connection comes back.
- The structure carries the later features (listeners, routes, certs, request-path navigation, diffs, k8s port-forward) without rework.

## 2. Scope

### In scope (v1)

- Cluster list, endpoint table, Config tab (highlighted YAML), header, error banner, status bar.
- Polling every `--refresh` via `tea.Tick`, with all I/O in `tea.Cmd`s.
- Direct admin URL access through a `Client` interface.
- Cobra CLI with timeouts configurable by flags.
- Fixtures, a fake admin server and a `--demo` mode.
- Makefile covering the lifecycle, and a README.

### Out of scope (v1)

- Built-in k8s port-forward (`--pod`) and client-go. This comes later behind the `Client` interface.
- Diff of config between refreshes ("what changed").
- Request-path navigator (listener → filter chain → route config → virtual host → route → cluster → endpoints) and the listeners, routes and certs panes.
- Write operations on Envoy (drain, reset counters, and so on).
- Authentication, TLS client certificates and non-loopback hardening.
- Config files and persisted preferences. Flags only.
- A stacked small-terminal layout. v1 shows a "terminal too small" message instead.

## 3. Architecture

Dependencies point one way: `ui → snapshot → admin`. Only `admin` and the `snapshot` builders import go-control-plane.

```
cmd/txenvoy/        main.go, root.go (cobra), version subcommand
internal/admin/     Client interface, HTTP implementation, protojson decode, Any unpacking
internal/snapshot/  domain types and builders from admin protos
internal/ui/        bubbletea root model, list pane, detail pane, header, banner, status bar
internal/demo/      fake admin server and fixtures (also used by tests and --demo)
```

### 3.1 admin

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

- The HTTP implementation takes the base URL and a request timeout (from `--timeout`) and applies the timeout to every call through a per-call context.
- `/clusters?format=json` is decoded as `envoy.admin.v3.Clusters`. `ClusterConfigs` calls `/config_dump?resource=dynamic_active_clusters` and `/config_dump?resource=static_clusters` and walks the result as raw JSON, so a cluster whose config holds an `Any` type that is not compiled in never fails the fetch. With `resource=`, Envoy emits one `configs[]` element per cluster, typed `type.googleapis.com/envoy.admin.v3.ClustersConfigDump.DynamicCluster` or `.StaticCluster`, with `version_info`, `cluster` and `last_updated` at the element's top level (the Static flag comes from that type). A plain `/config_dump` instead returns one wrapped `ClustersConfigDump` with nested `dynamic_active_clusters` / `static_clusters` arrays; the walker keeps that as a fallback. Elements that are not objects, have a non-string or unrelated `@type`, or whose `cluster` is missing, null, empty or nameless are skipped, never failing the decode. The snapshot renders each cluster's JSON to YAML with `yaml.JSONToYAML`.
- A port-forward transport can later supply the `http.Client` or implement `Client` itself without UI changes.

### 3.2 snapshot

`Fetch(ctx, Client) (Snapshot, error)` calls the endpoints and joins them by cluster name.

```go
type Snapshot struct {
    Clusters    []Cluster
    ServerInfo  *ServerInfo // may be nil
    FetchedAt   time.Time
}
type Cluster struct {
    Name, Type  string      // EDS / STRICT_DNS / STATIC ...
    Hosts       []Host
    Health      Health      // enum: Healthy / Degraded / Unhealthy / Empty
    Config      ConfigInfo  // YAML string, VersionInfo, LastUpdated, Static bool, Err string
}
type Host struct {
    Address, Zone string
    Flags         []string  // FAILED_ACTIVE_HC, FAILED_OUTLIER_CHECK, EDS_STATUS_UNHEALTHY ...
    Weight        uint32
    RqActive, RqError, CxActive uint64
}
```

- Config is rendered to YAML here (protojson → `sigs.k8s.io/yaml`). The UI only handles strings.
- `Health` is classified in this layer. The UI maps the enum to a color.
- Partial success is allowed. If `/config_dump` fails, the snapshot is still built and `Config.Err` is set on each cluster. If `/server_info` fails, `ServerInfo` is nil.

### 3.3 ui

A root `Model` owns the snapshot, the selection (kept by cluster name), the focus, the tick and the in-flight flag. It composes sub-models, each with its own `Update` and `View` and sized by the root:

- `header`: logo, connection block, tab.
- `listPane`: cluster list with a `bubbles/textinput` filter.
- `detailPane`: Endpoints and Config tabs.
- `statusBar`: counts and key hints.
- `banner`: the error overlay.

The cluster explorer is concrete code. The list/detail pane contains no cluster-specific logic, and a generic component is extracted when a second resource type arrives.

## 4. Data flow

1. `Init` fires a fetch `tea.Cmd` and a `tea.Tick`.
2. A fetch returns `snapshotMsg{snap, err}`.
3. Each tick starts a new fetch only if none is in flight, so slow responses cannot pile up.
4. On success the model swaps in the new snapshot, keeps the selection by cluster name and records the time.
5. On failure the model keeps the last good snapshot and raises the banner.
6. `Update` and `View` perform no I/O. Each fetch uses a context cancelled on quit, so no goroutine outlives the program.

## 5. UI

### 5.1 Layout

```
rows 1-2   logo (left)            connection block (right)
row  3     margin (blank)
row  4     tab: Clusters (N)
rows 5..H-1  list pane (~35%, with floor and cap) | detail pane
row  H     status bar
```

- Panes get `height − 5`.
- Under 24 rows the header collapses to a one-line `txenvoy` and the margin row is dropped.
- Below a minimum width (about 80 columns) the UI shows a "terminal too small" message.

Logo (2 rows, half-block characters, one accent color):

```
▀█▀ ▀▄▀ █▀▀ █▄ █ █ █ █▀█ █ █
 █  █ █ ██▄ █ ▀█ ▀▄▀ █▄█  █
```

### 5.2 Header

- **Connection block**, right-aligned:
  - Row 1 is `● connected` (green), `◌ connecting…` (dim, before the first response) or `✗ disconnected` (red), followed by the admin URL.
  - Row 2 is the Envoy version (from `/server_info`, omitted if unavailable) and "updated Ns ago", or "last ok Ns ago" on error.
- **Tab** shows the active pane and its count. Only `Clusters` appears in v1. Placeholder tabs for future panes are not shown.

### 5.3 Error banner

- An overlay painted over the top of the screen, full width, with a solid red background and light text.
- Long errors are word-wrapped to the terminal width. The banner is capped at 4 rows, and text beyond the cap ends with `…`.
- The layout is computed as if the banner did not exist. The banner replaces the first N lines of the rendered frame, so the panes, table and selection never move whatever the number of lines. `overlay(frame string, banner []string) string` swaps whole lines and never cuts through ANSI sequences.
- The banner hides the logo and connection block while shown, so its first line repeats the state (`✗ disconnected · last ok 14s ago`) and the text includes the admin URL when relevant.
- It clears on the next successful fetch. `x` dismisses it until the error text changes. A new, different error brings it back.
- Triggers: admin API failures (connection, timeout, non-2xx, invalid JSON, protojson failure). A single cluster's config decode problem is not a banner and shows a placeholder in the Config tab.

### 5.4 Cluster list

- Row: `● name  TYPE  healthy/total`.
- The marker is green for all healthy, yellow for some unhealthy, red for ejected or failing hosts making up the whole cluster. The `Health` enum decides, and the UI only maps it to color.
- Sort order is unhealthy first, then by name.
- A cluster with zero hosts shows `0/0` in a dim style.

### 5.5 Fuzzy filter

- `/` focuses the filter and the list narrows as you type. Matching uses `github.com/sahilm/fuzzy` and matched characters are highlighted.
- `Enter` keeps the filter and returns to the list. `Esc` clears it.
- The selection is tracked by cluster name, so refreshes and filter changes do not make the cursor jump.

### 5.6 Endpoints tab (default)

- A hand-rolled table renderer (`bubbles/table` cannot hold styled cells without breaking width math) with columns ADDRESS, FLAGS, WEIGHT, ZONE, RQ_ACTIVE, RQ_ERR, CX_ACTIVE.
- Flags are colored tags. Healthy hosts show a dim `OK`. Unhealthy hosts sort to the top.
- A one-line summary above the table gives totals and the number of ejected hosts.

### 5.7 Config tab

- A `bubbles/viewport` with chroma-highlighted YAML, computed once per snapshot and cached, not on every `View`.
- A header shows `version_info` and `last_updated`. For a static cluster it shows `static` and no version.
- Under `NO_COLOR` it falls back to plain YAML.

### 5.8 Keys

| Key | Action |
|---|---|
| `↑/↓`, `j/k` | move in the list, or scroll the focused detail pane |
| `/` | filter |
| `Tab` | switch focus list ↔ detail |
| `c` / `e` | Config / Endpoints tab |
| `r` | refresh now |
| `x` | dismiss the error banner |
| `?` | toggle help |
| `q`, `Ctrl+C` | quit |

### 5.9 Status bar

Counts and key hints only: `214 clusters · 3 unhealthy`. A dot indicates a fetch in flight. Errors and connection state live in the header and banner.

## 6. CLI

Cobra. `cmd/txenvoy/root.go` builds the root command. Running `txenvoy` launches the TUI. There is one subcommand, `txenvoy version`. Flags are collected into a `Config` struct and validated in `PreRunE`. `Config` is the only thing passed to `ui`, which has no cobra dependency. There is no viper.

| Flag | Default | Meaning |
|---|---|---|
| `--admin` | `http://localhost:15000` | Envoy admin base URL |
| `--refresh` | `3s` | Poll interval |
| `--timeout` | `3s` | Per-request timeout for each admin call |
| `--demo` | off | Start the built-in fake admin server on a random loopback port and connect to it (`--admin` is ignored) |

Validation (errors are printed before the TUI starts, with a non-zero exit):

- `--admin` must be a valid `http` or `https` URL.
- `--refresh` and `--timeout` must be positive, and `--refresh` has a floor of `500ms`.
- `--timeout` may exceed `--refresh`, because the in-flight guard prevents overlapping fetches.

No timeout or interval is a constant. They flow through `Config`, so tests pass short values and never wait on real clocks. The banner's row cap and the minimum terminal size are named constants in `ui`.

## 7. Error handling

| Situation | Behavior |
|---|---|
| Connection refused, timeout, non-2xx on `/clusters` | Banner. Last good snapshot stays, header shows `✗ disconnected`. |
| `/config_dump` fails, `/clusters` succeeds | Snapshot built without config. Config tab shows "config unavailable: <reason>". No banner. |
| `/server_info` fails | Version omitted. No banner. |
| Invalid JSON or protojson failure | Banner with a short message. Previous snapshot kept. |
| Unknown `Any` type URL | That resource gets a placeholder. The fetch does not fail. |
| Slow response | Per-request `--timeout`. A fetch in flight blocks the next one. |
| First fetch fails | Header `◌ connecting…` while retrying, then `✗ disconnected` plus banner. Empty list shows "waiting for Envoy…". |

## 8. Testing

Fixtures are Go builders in `internal/demo` (scenarios, emitting the per-element `config_dump` shape above) plus genuine captures from Envoy 1.31.10 in `internal/admin/testdata/real-*.json` (`/clusters?format=json`, `/config_dump?resource=dynamic_active_clusters`, `/config_dump?resource=static_clusters`, `/server_info`), which the admin and snapshot tests decode. The scenarios are:

- `healthy`: all hosts OK.
- `degraded`: `FAILED_ACTIVE_HC`, `FAILED_OUTLIER_CHECK` and `EDS_STATUS_UNHEALTHY` mixed.
- `empty`: a cluster with zero hosts.
- `static`: a static cluster with no `version_info`.
- `big`: about 300 generated clusters.

The fake admin server in `internal/demo` serves a scenario, supports switching scenarios and injecting failures at runtime (status codes, delays, garbage bodies), and backs `--demo`. In demo mode it also mutates host health over time so live refresh is visible.

Test layers:

1. **admin:** table-driven decode tests against the fixtures, including unknown `Any` types and malformed JSON.
2. **snapshot:** the join, the `Health` classification, flag parsing, sort order, and YAML output compared to golden strings.
3. **ui unit:** Update-level tests with injected messages: selection kept across refreshes, filter behavior, in-flight guard, error lifecycle (appear, auto-clear, `x` dismiss, new error re-shows).
4. **ui golden** (teatest, fixed terminal size, fixed clock, pinned color profile): healthy, degraded, filtered, Config tab, connecting, and banner variants (one line, long wrapped, truncated). Updated with `make test-update`.
5. **end to end:** the fake server and the real model through teatest. The server flips from healthy to failing, and the test asserts that the banner appears and then clears.

All tests run under `go test -race` with no Envoy and no network beyond loopback.

## 9. Makefile

A root `Makefile` is the single lifecycle entry point. All targets are `.PHONY` and `make help` is the default.

| Target | Does |
|---|---|
| `build` | `go build` into `bin/txenvoy`, version injected with `-ldflags` |
| `run` | build and run `bin/txenvoy --demo` |
| `test` | `go test -race ./...` |
| `test-update` | regenerate teatest golden files |
| `lint` | `golangci-lint run` plus `go vet` |
| `fmt` | `gofmt` / `goimports` |
| `tidy` | `go mod tidy` |
| `cover` | coverage profile and summary |
| `clean` | remove `bin/` and coverage output |
| `check` | `fmt` check + `lint` + `test` |

Tool versions are pinned so `make check` behaves the same on every machine.

## 10. README

Covers install, `make` targets, the flags table, key bindings, `make run` for a no-cluster demo, and a "Running against a Kubernetes pod" section:

1. Plain pod or deployment:
   ```sh
   kubectl -n <ns> port-forward pod/<pod> 15000:15000
   txenvoy --admin http://localhost:15000
   ```
   `deploy/<name>` or `svc/<name>` also work as the forward target.
2. Istio sidecar: the admin port is `15000` on the `istio-proxy` container and the same forward works. `istioctl dashboard envoy <pod>.<ns>` is an alternative.
3. Standalone Envoy: the admin port is whatever `admin.address` says in the bootstrap, often `9901`, so use `15000:9901`.
4. Caveats: keep the forward running in another terminal, bind to `127.0.0.1` only (the admin interface is unauthenticated), and if the forward drops the UI shows the error banner and recovers once the forward is back.
5. Roadmap: a built-in `--pod ns/name` using client-go port-forward behind the same `Client` interface.

## 11. Dependencies

bubbletea v1, bubbles, lipgloss, cobra, go-control-plane, `sigs.k8s.io/yaml`, chroma, `sahilm/fuzzy`, and teatest (`charmbracelet/x/exp/teatest`). `go-cmp` or `go-diff` and client-go are deferred with their features.

## 12. Later (not designed here)

After v1: the config diff between refreshes, listeners, routes and certs panes (extracting the generic list/detail component at that point), the request-path navigator with Enter jumping between resources, and the client-go port-forward transport.
