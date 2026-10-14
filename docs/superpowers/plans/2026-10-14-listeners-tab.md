# Listeners Tab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Listeners tab (list, fuzzy filter, Overview, numbered Config YAML) next to Clusters, switched with Tab, with Enter focusing the details and Esc returning to the list.

**Architecture:** A shared `resourceItem` interface sits behind one generic `listPane` and one generic `detailPane`; the root `Model` holds one `view` per resource and an `active` index. Listener data comes from `/config_dump?resource=dynamic_listeners|static_listeners` decoded as tolerant raw JSON in `admin`, turned into plain types in `snapshot` (secondary fetches run in parallel, YAML is rendered lazily), and rendered by a `listenerItem`.

**Tech Stack:** Go, bubbletea v1, bubbles, lipgloss, cobra, go-control-plane, `sigs.k8s.io/yaml`, chroma, `sahilm/fuzzy`, teatest (all already in `go.mod`).

**Spec:** `docs/superpowers/specs/2026-10-14-listeners-tab-design.md` (builds on `2026-10-14-cluster-explorer-design.md`)

## Deviations from the spec (applied to the spec in Task 9)

1. `resourceItem.OverviewMaxScroll` takes `(w, h int)`, not `(h int)`: the listener Overview wraps its error block to the pane width, so its sticky height depends on `w`.
2. Listener row: the address is right-aligned and sized per row (at most 21 cells, leaving at least 8 for the name), not sized to the widest address across the list. A row needs no knowledge of its neighbours.
3. `ConfigInfo` keeps its `YAML` field as an explicit pre-rendered override and gains `JSON` plus `Render() (text, problem string)`, which converts lazily and caches. `Fetch` no longer fills `YAML`.
4. The demo `Fault` gains a `Match` substring so a test can fail only the listener dump.
5. The short hint line reads `? help · q quit · tab switch · enter details · / filter …` (not the spec's order): `? help` and `q quit` must survive clipping at 80 columns, as an earlier review required.

## Findings from the real capture (Task 1, applied below)

A rejected LDS update is emitted by Envoy as **two elements** for the same listener: the named element carries `active_state` (the old, still-serving version) next to `error_state` (with `last_update_attempt`), and a second **nameless** element carries only `error_state` (no attempt time). `error_state.failed_configuration` is a bare `Listener` (it has its own `@type`), `details` ends with a newline, and the nameless element's listener name exists only in `failed_configuration.name`. Static elements have no element-level name (it is `listener.name`); `port_value`, `destination_port` and `prefix_len` are JSON numbers; `last_updated` is RFC3339 with milliseconds. Tasks 2, 3 and 5 below reflect this: the demo emits the same pair, the decoder names the nameless element from the failed configuration and merges elements with the same name, and error details are trimmed.

## Global Constraints

- Module path `github.com/regeda/txenvoy`; bubbletea **v1** (`View() string`, `tea.KeyMsg`); no viper.
- Dependency direction `ui → snapshot → admin`; only `admin` and `snapshot` import go-control-plane.
- `Update` and `View` perform no I/O. No timeout or interval is a constant (they come from `config.Config`).
- Keys: `Tab`/`Shift+Tab` switch resources (ignored while the filter input is active); `Enter` (list focused, an item selected) focuses the details; `Esc` in the details returns to the list, `Esc` in the list clears an active filter; in filter mode `Enter` keeps the filter and `Esc` clears it; `c` opens Config, `o` (and `e`) open the first tab; `/`, `r`, `x`, `?`, `q`, `Ctrl+C` unchanged. With the details focused, arrows, `j`/`k`, PgUp/PgDn, `g`/`G` scroll the details.
- Overview shows only: bind address, state, `version_info`, `last_updated`, filter chains (label, match, filter names, `TLS`). No route-config or cluster references.
- No line is wider than the terminal; `View()` is exactly `height` lines; clipping never relies on lipgloss `Width()` (it wraps).
- State is never colour-only: glyphs `●` active/static, `◐` warming/draining, `✗` error.
- Tests must be able to fail for the reason they name (mutation-check the key behaviours); never weaken an assertion to make a task pass.
- Every commit uses Conventional Commits and ends with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>` (second `-m`). `.superpowers/` is git-ignored: never commit it. Docker containers named `envoy-stack-*` on this machine are not ours: never touch them.
- `make check` (gofmt, golangci-lint v2.14.0, vet, `go test -race ./...`) is green after every task.

## Review Focus

Inputs and conditions the spec implies that no feature test would naturally cover. Each has a test in the owning task.

1. Real-Envoy listener shapes that differ from the happy path: a state with no `listener`, an `error_state` next to an `active_state`, both `warming_state` and `active_state`, a nameless or non-object element, and `{}` (Task 3, Task 5).
2. A listener with no address, a pipe address, no `filter_chains`, or an unnamed-chain flood: no panic, blank address, `Filter chains (0)` (Task 5, Task 7).
3. Tab while typing a filter is ignored; Enter with an empty list does nothing; switching tabs keeps each tab's filter, selection and scroll (Task 8).
4. A failing listener dump is isolated: Clusters and the banner are unaffected, the tab label reads `Listeners (!)`, and the inline message wraps inside the pane (Task 5, Task 8).
5. Huge listeners (thousands of YAML lines, many chains): YAML is converted only for the selected item, never during `Fetch`; Overview scroll bounds hold (Task 4, Task 5, Task 7).

---

## File structure

```
internal/admin/testdata/capture/        bootstrap.yaml, lds.yaml, lds-bad.yaml (how the captures were made)
internal/admin/testdata/real-listeners-*.json   genuine Envoy captures
internal/admin/listeners.go             ListenerConfig types + decoder
internal/admin/client.go                + ListenerConfigs
internal/demo/scenario.go               + Listener/Chain types, listener emission
internal/demo/server.go                 + Fault.Match
internal/snapshot/config.go             ConfigInfo (lazy YAML)
internal/snapshot/listener.go           Listener types and parsing
internal/snapshot/snapshot.go           parallel Fetch, Snapshot.Listeners
internal/ui/item.go                     resourceItem, rowCtx, text helpers
internal/ui/cluster_item.go             cluster row + Overview adapter
internal/ui/listener_item.go            listener row + Overview
internal/ui/listpane.go, detailpane.go  generic panes
internal/ui/view.go                     view bundle + resource descriptors
internal/ui/chrome.go                   header tab row
internal/ui/model.go                    views, Tab/Enter/Esc, status, hints
```

---

### Task 1: Capture real listener output from Envoy

**Files:**
- Create: `internal/admin/testdata/capture/bootstrap.yaml`, `internal/admin/testdata/capture/lds.yaml`, `internal/admin/testdata/capture/lds-bad.yaml` (only if the error capture works), `internal/admin/testdata/real-listeners-static.json`, `internal/admin/testdata/real-listeners-dynamic.json`, optionally `internal/admin/testdata/real-listeners-error.json`
- Modify: `internal/admin/testdata/README.md`

**Interfaces:**
- Produces: genuine fixtures consumed by the admin tests (Task 3) and snapshot tests (Task 5). No Go code.

This task is exploratory: its output is evidence about what real Envoy emits. If Docker or the image is unavailable, do not work around it: write the hand-written fixtures in Task 3 instead, label them, and say so in the report.

- [ ] **Step 1: Write the bootstrap and LDS files**

`internal/admin/testdata/capture/bootstrap.yaml`:

```yaml
node: {id: txenvoy-capture, cluster: txenvoy}
admin:
  address: {socket_address: {address: 0.0.0.0, port_value: 9901}}
static_resources:
  clusters:
  - name: api
    type: STATIC
    connect_timeout: 1s
    load_assignment:
      cluster_name: api
      endpoints:
      - lb_endpoints:
        - endpoint: {address: {socket_address: {address: 10.0.0.1, port_value: 8080}}}
  listeners:
  - name: static_tcp
    address: {socket_address: {address: 127.0.0.1, port_value: 10001}}
    filter_chains:
    - filters:
      - name: envoy.filters.network.tcp_proxy
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy
          stat_prefix: static_tcp
          cluster: api
  - name: static_multi
    address: {socket_address: {address: 0.0.0.0, port_value: 10002}}
    listener_filters:
    - name: envoy.filters.listener.tls_inspector
      typed_config: {"@type": type.googleapis.com/envoy.extensions.filters.listener.tls_inspector.v3.TlsInspector}
    filter_chains:
    - name: by-sni
      filter_chain_match: {server_names: [a.example.com, b.example.com], transport_protocol: tls}
      filters:
      - name: envoy.filters.network.tcp_proxy
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy
          stat_prefix: by_sni
          cluster: api
    - name: by-port-and-cidr
      filter_chain_match:
        destination_port: 10002
        prefix_ranges: [{address_prefix: 10.0.0.0, prefix_len: 8}]
      filters:
      - name: envoy.filters.network.tcp_proxy
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy
          stat_prefix: by_port
          cluster: api
    default_filter_chain:
      filters:
      - name: envoy.filters.network.tcp_proxy
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy
          stat_prefix: default
          cluster: api
dynamic_resources:
  lds_config:
    resource_api_version: V3
    path_config_source: {path: /etc/envoy/lds.yaml}
```

`internal/admin/testdata/capture/lds.yaml`:

```yaml
version_info: "7"
resources:
- "@type": type.googleapis.com/envoy.config.listener.v3.Listener
  name: dynamic_one
  address: {socket_address: {address: 0.0.0.0, port_value: 10003}}
  filter_chains:
  - filters:
    - name: envoy.filters.network.tcp_proxy
      typed_config:
        "@type": type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy
        stat_prefix: dynamic_one
        cluster: api
```

- [ ] **Step 2: Run Envoy and capture**

Reuse the image if it is already local (`docker images envoyproxy/envoy`); otherwise pull `envoyproxy/envoy:v1.31-latest`. Mount the capture directory read-only and bind the admin port to loopback only, on a port that is free:

```bash
CAP=$PWD/internal/admin/testdata/capture
docker run -d --rm --name txenvoy-capture -p 127.0.0.1:19902:9901 \
  -v $CAP/bootstrap.yaml:/etc/envoy/envoy.yaml:ro -v $CAP/lds.yaml:/etc/envoy/lds.yaml:ro \
  envoyproxy/envoy:v1.31-latest -c /etc/envoy/envoy.yaml
sleep 4
curl -s 'http://127.0.0.1:19902/config_dump?resource=static_listeners'  > internal/admin/testdata/real-listeners-static.json
curl -s 'http://127.0.0.1:19902/config_dump?resource=dynamic_listeners' > internal/admin/testdata/real-listeners-dynamic.json
```

Expected: both files are non-empty JSON; static contains elements typed `...ListenersConfigDump.StaticListener`, dynamic one typed `...DynamicListener` with `active_state`. Open them and record the exact field names and whether numbers such as `destination_port` and `prefix_len` are JSON numbers.

- [ ] **Step 3: Try to capture an error state (optional)**

Write `lds-bad.yaml` as a copy of `lds.yaml` with `version_info: "8"` and the `dynamic_one` filter name changed to `envoy.filters.network.does_not_exist`. Copy it over the mounted `lds.yaml` target (or restart with it mounted), wait a few seconds, and fetch `dynamic_listeners` again into `real-listeners-error.json`. Keep the file only if it genuinely contains an `error_state`; otherwise delete `lds-bad.yaml` and note in the README that error, warming and draining fixtures are hand-written.

- [ ] **Step 4: Always remove the container**

```bash
docker rm -f txenvoy-capture
docker ps -a --format '{{.Names}}'
```

Expected: `txenvoy-capture` is gone; any `envoy-stack-*` containers are still there, untouched.

- [ ] **Step 5: Scrub and document**

Check the captured files for anything host-specific (hostnames, user paths, secrets); the node id should be `txenvoy-capture`. Append to `internal/admin/testdata/README.md` a section "Listener captures" naming the Envoy version string from `/server_info`, the files above, how `capture/` reproduces them, and which states are hand-written elsewhere.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/testdata
git commit -m "test(admin): capture real Envoy listener config dumps" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Demo listeners and a scoped fault

**Files:**
- Modify: `internal/demo/scenario.go`, `internal/demo/server.go`, `internal/demo/server_test.go`

**Interfaces:**
- Produces:
  - `demo.Chain{Name string; Match map[string]any; Filters []string; TLS bool}`
  - `demo.Listener{Name, Address string; Static bool; Version, State string; ServingOld bool; Error string; Chains []Chain; Default *Chain}` where `State` is `"active"`, `"warming"`, `"draining"` or `"error"` (static listeners ignore `State`).
  - `Scenario.Listeners []Listener`; `Scenario.ConfigDumpJSON(resource)` now also accepts `"dynamic_listeners"` and `"static_listeners"`.
  - `demo.Fault.Match string`: when non-empty the fault applies only to requests whose `RequestURI` contains it.
  - Scenarios: `Healthy()` gets three listeners; `Degraded()` gets the seven listed in Step 3.

- [ ] **Step 1: Write the failing tests**

Append to `internal/demo/server_test.go`:

```go
func TestListenerDumpsUsePerElementShape(t *testing.T) {
	s := Degraded()
	dyn, ok := s.ConfigDumpJSON("dynamic_listeners")
	if !ok {
		t.Fatal("dynamic_listeners must be supported")
	}
	stat, _ := s.ConfigDumpJSON("static_listeners")

	var d struct {
		Configs []map[string]any `json:"configs"`
	}
	if err := json.Unmarshal(dyn, &d); err != nil {
		t.Fatal(err)
	}
	names := map[string]map[string]any{}
	nameless := 0
	for _, c := range d.Configs {
		if !strings.HasSuffix(c["@type"].(string), "ListenersConfigDump.DynamicListener") {
			t.Fatalf("element type %v", c["@type"])
		}
		name, ok := c["name"].(string)
		if !ok {
			// Envoy's second element for a rejected update: error_state only
			if _, hasErr := c["error_state"]; !hasErr || len(c) != 2 {
				t.Fatalf("a nameless element holds only @type and error_state: %v", c)
			}
			nameless++
			continue
		}
		names[name] = c
	}
	if nameless != 1 {
		t.Fatalf("one rejected update emits exactly one nameless twin, got %d", nameless)
	}
	for _, want := range []string{"virtualInbound", "0.0.0.0_8080", "orders-egress", "legacy-9090", "bad-update"} {
		if names[want] == nil {
			t.Fatalf("dynamic dump is missing %s: %v", want, names)
		}
	}
	if _, ok := names["virtualInbound"]["active_state"]; !ok {
		t.Fatal("an active listener carries active_state")
	}
	if _, ok := names["orders-egress"]["warming_state"]; !ok {
		t.Fatal("a warming listener carries warming_state")
	}
	if _, ok := names["orders-egress"]["active_state"]; !ok {
		t.Fatal("a warming update with ServingOld also carries active_state")
	}
	if _, ok := names["legacy-9090"]["draining_state"]; !ok {
		t.Fatal("a draining listener carries draining_state")
	}
	es, ok := names["bad-update"]["error_state"].(map[string]any)
	if !ok || es["details"] == "" || es["failed_configuration"] == nil {
		t.Fatalf("error_state incomplete: %v", names["bad-update"])
	}
	if _, ok := names["bad-update"]["active_state"]; !ok {
		t.Fatal("a rejected update with ServingOld keeps its active_state")
	}

	if !strings.Contains(string(stat), "StaticListener") || strings.Contains(string(stat), "DynamicListener") {
		t.Fatalf("static dump must hold only static listeners: %s", stat)
	}
	if strings.Contains(string(dyn), "admin-static") {
		t.Fatal("static listeners must not appear in the dynamic dump")
	}
	if _, ok := Degraded().ConfigDumpJSON("bogus_listeners"); ok {
		t.Fatal("unknown resource")
	}
}

func TestListenerJSONCarriesAddressAndChains(t *testing.T) {
	dyn, _ := Degraded().ConfigDumpJSON("dynamic_listeners")
	body := string(dyn)
	for _, want := range []string{
		`"port_value":15006`, `"server_names":["a.example.com","b.example.com"]`,
		`envoy.filters.network.http_connection_manager`, `"default_filter_chain"`,
		`envoy.transport_sockets.tls`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("listener JSON misses %s", want)
		}
	}
	stat, _ := Degraded().ConfigDumpJSON("static_listeners")
	if !strings.Contains(string(stat), `"path":"/var/run/agent.sock"`) {
		t.Fatalf("pipe address missing: %s", stat)
	}
}

func TestFaultMatchScopesTheFault(t *testing.T) {
	s := NewServer(Degraded())
	srv := httptest.NewServer(s)
	defer srv.Close()

	s.Inject(Fault{Status: 503, Match: "dynamic_listeners"})
	if code, _ := get(t, srv.URL+"/config_dump?resource=dynamic_listeners"); code != 503 {
		t.Fatalf("matching request must fail, got %d", code)
	}
	for _, path := range []string{"/clusters?format=json", "/config_dump?resource=dynamic_active_clusters", "/server_info", "/config_dump?resource=static_listeners"} {
		if code, _ := get(t, srv.URL+path); code != 200 {
			t.Fatalf("%s must be unaffected, got %d", path, code)
		}
	}
	s.Inject(Fault{})
	if code, _ := get(t, srv.URL+"/config_dump?resource=dynamic_listeners"); code != 200 {
		t.Fatal("cleared fault")
	}
}

func TestScenarioListenerCounts(t *testing.T) {
	if n := len(Degraded().Listeners); n != 7 {
		t.Fatalf("Degraded has %d listeners, want 7", n)
	}
	if n := len(Healthy().Listeners); n != 3 {
		t.Fatalf("Healthy has %d listeners, want 3", n)
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/demo/...`
Expected: FAIL (`Listeners`, `Fault.Match` undefined).

- [ ] **Step 3: Implement listeners in the scenario**

In `internal/demo/scenario.go` add the types, the field, the emission and the data.

```go
type Chain struct {
	Name    string
	Match   map[string]any // filter_chain_match as Envoy emits it
	Filters []string       // short names, e.g. "tcp_proxy"
	TLS     bool
}

type Listener struct {
	Name       string
	Address    string // "host:port" or "unix:/path"
	Static     bool
	Version    string
	State      string // "active", "warming", "draining" or "error" (ignored when Static)
	ServingOld bool   // warming/error update next to a still-active older version
	Error      string // error details when State == "error"
	Chains     []Chain
	Default    *Chain
}
```

Add `Listeners []Listener` to `Scenario`. Then:

```go
func (c Chain) json() map[string]any {
	out := map[string]any{}
	if c.Name != "" {
		out["name"] = c.Name
	}
	if len(c.Match) > 0 {
		out["filter_chain_match"] = c.Match
	}
	filters := []any{}
	for _, f := range c.Filters {
		filters = append(filters, map[string]any{"name": "envoy.filters.network." + f})
	}
	out["filters"] = filters
	if c.TLS {
		out["transport_socket"] = map[string]any{"name": "envoy.transport_sockets.tls"}
	}
	return out
}

func (l Listener) json() map[string]any {
	addr := map[string]any{}
	if path, ok := strings.CutPrefix(l.Address, "unix:"); ok {
		addr["pipe"] = map[string]any{"path": path}
	} else {
		host, port, _ := net.SplitHostPort(l.Address)
		n, _ := strconv.Atoi(port)
		addr["socket_address"] = map[string]any{"address": host, "port_value": n}
	}
	chains := []any{}
	for _, c := range l.Chains {
		chains = append(chains, c.json())
	}
	out := map[string]any{
		"@type":         "type.googleapis.com/envoy.config.listener.v3.Listener",
		"name":          l.Name,
		"address":       addr,
		"filter_chains": chains,
	}
	if l.Default != nil {
		out["default_filter_chain"] = l.Default.json()
	}
	return out
}

func (s Scenario) listenerState(l Listener) map[string]any {
	return map[string]any{
		"version_info": l.Version,
		"listener":     l.json(),
		"last_updated": s.Updated.Format(time.RFC3339Nano),
	}
}

// listenerElements returns one configs[] element per listener for
// ?resource=dynamic_listeners or static_listeners, in the shape Envoy uses.
func (s Scenario) listenerElements(static bool) []any {
	out := []any{}
	for _, l := range s.Listeners {
		if l.Static != static {
			continue
		}
		if static {
			out = append(out, map[string]any{
				"@type":        "type.googleapis.com/envoy.admin.v3.ListenersConfigDump.StaticListener",
				"listener":     l.json(),
				"last_updated": s.Updated.Format(time.RFC3339Nano),
			})
			continue
		}
		e := map[string]any{
			"@type": "type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener",
			"name":  l.Name,
		}
		var twin map[string]any
		switch l.State {
		case "warming":
			e["warming_state"] = s.listenerState(l)
			if l.ServingOld {
				old := l
				old.Version = "prev"
				e["active_state"] = s.listenerState(old)
			}
		case "draining":
			e["draining_state"] = s.listenerState(l)
		case "error":
			e["error_state"] = map[string]any{
				"failed_configuration": l.json(),
				"last_update_attempt":  s.Updated.Format(time.RFC3339Nano),
				"details":              l.Error + "\n", // Envoy ends details with a newline
			}
			if l.ServingOld {
				old := l
				old.Version = "prev"
				e["active_state"] = s.listenerState(old)
			}
			// Envoy also emits a second, nameless element holding only the error.
			twin = map[string]any{
				"@type": "type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener",
				"error_state": map[string]any{
					"failed_configuration": l.json(),
					"details":              l.Error + "\n",
				},
			}
		default:
			e["active_state"] = s.listenerState(l)
		}
		out = append(out, e)
		if twin != nil {
			out = append(out, twin)
		}
	}
	return out
}
```

Change `ConfigDumpJSON` so its first lines become:

```go
func (s Scenario) ConfigDumpJSON(resource string) ([]byte, bool) {
	switch resource {
	case "dynamic_listeners":
		return mustJSON(map[string]any{"configs": s.listenerElements(false)}), true
	case "static_listeners":
		return mustJSON(map[string]any{"configs": s.listenerElements(true)}), true
	case "dynamic_active_clusters", "static_clusters":
	default:
		return nil, false
	}
```

(removing the old `if resource != ... { return nil, false }` guard; the rest of the cluster body is unchanged).

Add data helpers and the scenarios' listeners:

```go
var (
	hcm   = []string{"http_connection_manager"}
	tcp   = []string{"tcp_proxy"}
	tcpCh = Chain{Filters: tcp}
)

func healthyListeners() []Listener {
	return []Listener{
		{Name: "0.0.0.0_8080", Address: "0.0.0.0:8080", Version: "42", State: "active",
			Chains: []Chain{{Filters: hcm}}},
		{Name: "0.0.0.0_9200", Address: "0.0.0.0:9200", Version: "42", State: "active",
			Chains: []Chain{{Filters: tcp}}},
		{Name: "admin-static", Address: "127.0.0.1:15000", Static: true, Chains: []Chain{{Filters: tcp}}},
	}
}

func degradedListeners() []Listener {
	return []Listener{
		{Name: "virtualInbound", Address: "0.0.0.0:15006", Version: "7", State: "active",
			Chains: []Chain{
				{Match: map[string]any{"destination_port": 8080, "transport_protocol": "tls", "application_protocols": []string{"h2"}},
					Filters: hcm, TLS: true},
				{Name: "by-sni", Match: map[string]any{"server_names": []string{"a.example.com", "b.example.com"}}, Filters: hcm},
			},
			Default: &tcpCh},
		{Name: "0.0.0.0_8080", Address: "0.0.0.0:8080", Version: "7", State: "active", Chains: []Chain{{Filters: hcm}}},
		{Name: "orders-egress", Address: "10.0.2.1:5432", Version: "8", State: "warming", ServingOld: true, Chains: []Chain{{Filters: tcp}}},
		{Name: "legacy-9090", Address: "0.0.0.0:9090", Version: "6", State: "draining", Chains: []Chain{{Filters: tcp}}},
		{Name: "bad-update", Address: "0.0.0.0:9443", Version: "9", State: "error", ServingOld: true,
			Error:  "error adding listener '0.0.0.0:9443': unknown filter 'envoy.filters.network.nope'\nwhile processing LDS update version 9",
			Chains: []Chain{{Filters: tcp}}},
		{Name: "admin-static", Address: "127.0.0.1:15000", Static: true, Chains: []Chain{{Filters: tcp}}},
		{Name: "uds-agent", Address: "unix:/var/run/agent.sock", Static: true, Chains: []Chain{{Filters: tcp}}},
	}
}
```

Make `base(...)` set `Listeners: healthyListeners()` and make `Degraded()` replace it: after `return base(...)` build `sc := base(...)`, set `sc.Listeners = degradedListeners()`, return `sc`. (`Big(n)` keeps the default healthy listeners.)

Add `"strings"` to the imports of `scenario.go` if it is not already imported.

- [ ] **Step 4: Implement the scoped fault**

In `internal/demo/server.go` add `Match string // fault only requests whose RequestURI contains this; empty means all` to `Fault`, and make `ServeHTTP` skip the whole fault block when it does not apply:

```go
	sc, f := s.sc, s.fault
	s.mu.Unlock()
	if f.Match != "" && !strings.Contains(r.URL.RequestURI(), f.Match) {
		f = Fault{}
	}
```

(insert immediately after the unlock; add `"strings"` to the imports.)

- [ ] **Step 5: Run and verify**

Run: `go test -race ./internal/demo/...` and `make check`
Expected: PASS. Existing demo tests (`TestEndpointsServeValidJSON`, `TestStaticClustersAreSplitFromDynamic`, faults) still pass; if one asserted that an unknown resource returns 400 for a listener name, adjust nothing: `bogus` still returns 400.

- [ ] **Step 6: Commit**

```bash
git add internal/demo
git commit -m "feat(demo): add listener scenarios and a scoped fault" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: admin listener configs

**Files:**
- Create: `internal/admin/listeners.go`, `internal/admin/listeners_test.go`
- Modify: `internal/admin/client.go`

**Interfaces:**
- Consumes: the Task 1 captures (`real-listeners-*.json`) when present; `demo` (Task 2) is not imported by `admin` tests.
- Produces:

```go
type ListenerState struct {
    VersionInfo string
    LastUpdated time.Time
    Listener    json.RawMessage
}
type ListenerError struct {
    LastUpdateAttempt time.Time
    Details           string
    Listener          json.RawMessage // the rejected configuration
}
type ListenerConfig struct {
    Name                      string
    Static                    bool
    Active, Warming, Draining *ListenerState
    Error                     *ListenerError
}
// Client gains:
ListenerConfigs(ctx context.Context) ([]ListenerConfig, error)
```

A static listener is returned with `Static: true` and its single state in `Active` (empty `VersionInfo`); its name is `listener.name`.

- [ ] **Step 1: Write the failing tests**

`internal/admin/listeners_test.go`:

```go
package admin

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Hand-written from Envoy's addResourceToDump behaviour: NOT captured. The
// real captures are exercised in TestListenerConfigsFromRealCapture.
const handDynamic = `{"configs":[
 {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"a",
  "active_state":{"version_info":"7","last_updated":"2026-10-14T10:00:00.123Z",
    "listener":{"@type":"type.googleapis.com/envoy.config.listener.v3.Listener","name":"a",
      "address":{"socket_address":{"address":"0.0.0.0","port_value":8080}},
      "filter_chains":[{"filters":[{"name":"envoy.filters.network.tcp_proxy",
        "typed_config":{"@type":"type.googleapis.com/acme.Unknown","x":1}}]}]}}},
 {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"b",
  "active_state":{"version_info":"6","listener":{"name":"b"}},
  "warming_state":{"version_info":"7","listener":{"name":"b"}}},
 {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"c",
  "draining_state":{"version_info":"5","listener":{"name":"c"}}},
 {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"d",
  "active_state":{"version_info":"3","listener":{"name":"d"}},
  "error_state":{"failed_configuration":{"name":"d","bad":true},"last_update_attempt":"2026-10-14T10:01:00Z","details":"boom\nsecond line"}},
 {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"e",
  "error_state":{"failed_configuration":{"name":"e"},"details":"never worked"}}
]}`

const handStatic = `{"configs":[
 {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.StaticListener",
  "listener":{"@type":"type.googleapis.com/envoy.config.listener.v3.Listener","name":"s",
   "address":{"pipe":{"path":"/tmp/s.sock"}}},"last_updated":"2026-10-14T09:00:00Z"}]}`

func serveListeners(t *testing.T, dyn, stat string) *HTTP {
	t.Helper()
	return newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("resource") {
		case "dynamic_listeners":
			_, _ = w.Write([]byte(dyn))
		case "static_listeners":
			_, _ = w.Write([]byte(stat))
		default:
			http.Error(w, "bad resource", 400)
		}
	}), time.Second)
}

func byName(cfgs []ListenerConfig) map[string]ListenerConfig {
	m := map[string]ListenerConfig{}
	for _, c := range cfgs {
		m[c.Name] = c
	}
	return m
}

func TestListenerConfigsDecodesEveryState(t *testing.T) {
	c := serveListeners(t, handDynamic, handStatic)
	got, err := c.ListenerConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := byName(got)
	if len(by) != 6 {
		t.Fatalf("got %d listeners: %+v", len(by), got)
	}

	a := by["a"]
	if a.Static || a.Active == nil || a.Active.VersionInfo != "7" ||
		a.Active.LastUpdated.Format(time.RFC3339Nano) != "2026-10-14T10:00:00.123Z" {
		t.Fatalf("a: %+v", a)
	}
	if !strings.Contains(string(a.Active.Listener), "acme.Unknown") {
		t.Fatal("an unknown nested Any type must survive in the raw JSON")
	}

	b := by["b"]
	if b.Active == nil || b.Warming == nil || b.Active.VersionInfo != "6" || b.Warming.VersionInfo != "7" {
		t.Fatalf("b: %+v", b)
	}
	if c := by["c"]; c.Draining == nil || c.Active != nil {
		t.Fatalf("c: %+v", c)
	}
	d := by["d"]
	if d.Error == nil || d.Active == nil || d.Error.Details != "boom\nsecond line" ||
		!strings.Contains(string(d.Error.Listener), `"bad":true`) || d.Error.LastUpdateAttempt.IsZero() {
		t.Fatalf("d: %+v", d)
	}
	if e := by["e"]; e.Error == nil || e.Active != nil || e.Error.Details != "never worked" {
		t.Fatalf("e: %+v", e)
	}

	s := by["s"]
	if !s.Static || s.Active == nil || s.Active.VersionInfo != "" || s.Active.LastUpdated.IsZero() ||
		!strings.Contains(string(s.Active.Listener), "/tmp/s.sock") {
		t.Fatalf("a static listener is named by listener.name and held in Active: %+v", s)
	}
}

func TestListenerConfigsWrappedFallback(t *testing.T) {
	wrapped := `{"configs":[{"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump",
	 "version_info":"x",
	 "static_listeners":[{"listener":{"name":"ws"},"last_updated":"2026-10-14T09:00:00Z"}],
	 "dynamic_listeners":[{"name":"wd","active_state":{"version_info":"4","listener":{"name":"wd"}}}]}]}`
	c := serveListeners(t, wrapped, wrapped)
	got, err := c.ListenerConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := byName(got)
	if len(by) != 2 || !by["ws"].Static || by["wd"].Static || by["wd"].Active.VersionInfo != "4" {
		t.Fatalf("wrapped shape: %+v", got)
	}
}

func TestListenerConfigsSkipBadEntriesAndAcceptEmpty(t *testing.T) {
	junk := `{"configs":[
	  42, null, "str", {}, {"@type":123},
	  {"@type":"type.googleapis.com/envoy.admin.v3.BootstrapConfigDump","bootstrap":{}},
	  {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener"},
	  {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"x"},
	  {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"y","active_state":"nope"},
	  {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.StaticListener","listener":null},
	  {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.StaticListener","listener":{}},
	  {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"good",
	   "active_state":{"version_info":"1","listener":{"name":"good"}}}
	]}`
	c := serveListeners(t, junk, `{}`)
	got, err := c.ListenerConfigs(context.Background())
	if err != nil {
		t.Fatalf("bad entries must be skipped, not fatal: %v", err)
	}
	if by := byName(got); by["good"].Active == nil || len(by) != 1 {
		t.Fatalf("only the good listener survives: %+v", got)
	}
	c = serveListeners(t, `{}`, `{}`)
	if got, err := c.ListenerConfigs(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("empty dumps are fine: %v %v", got, err)
	}
}

func TestRejectedUpdateElementsAreMergedByName(t *testing.T) {
	// Hand-written copy of the pair Envoy emits (see the real capture test below):
	// the named element has active_state + error_state, the twin is nameless.
	const named = `{"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"d",
	  "active_state":{"version_info":"7","listener":{"name":"d"},"last_updated":"2026-10-14T02:48:19.499Z"},
	  "error_state":{"failed_configuration":{"@type":"type.googleapis.com/envoy.config.listener.v3.Listener","name":"d","bad":1},
	    "last_update_attempt":"2026-10-14T02:48:40.320Z","details":"boom\n"}}`
	const twin = `{"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener",
	  "error_state":{"failed_configuration":{"name":"d","bad":1},"details":"boom\n"}}`

	for name, body := range map[string]string{
		"named first": `{"configs":[` + named + `,` + twin + `]}`,
		"twin first":  `{"configs":[` + twin + `,` + named + `]}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := serveListeners(t, body, `{}`).ListenerConfigs(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("the pair is one listener, got %d: %+v", len(got), got)
			}
			d := got[0]
			if d.Name != "d" || d.Active == nil || d.Active.VersionInfo != "7" || d.Error == nil ||
				d.Error.LastUpdateAttempt.IsZero() || d.Error.Details != "boom\n" {
				t.Fatalf("merged listener (the attempt time must survive either order): %+v", d)
			}
		})
	}
}

func TestNamelessErrorOnlyListenerIsNamedFromTheFailedConfiguration(t *testing.T) {
	body := `{"configs":[{"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener",
	  "error_state":{"failed_configuration":{"name":"never-worked"},"details":"nope"}}]}`
	got, err := serveListeners(t, body, `{}`).ListenerConfigs(context.Background())
	if err != nil || len(got) != 1 || got[0].Name != "never-worked" || got[0].Error == nil || got[0].Active != nil {
		t.Fatalf("%v %+v", err, got)
	}
	// no name anywhere: skipped, never fatal
	anon := `{"configs":[{"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener",
	  "error_state":{"failed_configuration":{},"details":"nope"}}]}`
	if got, err := serveListeners(t, anon, `{}`).ListenerConfigs(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("an error with no name at all is skipped: %v %+v", err, got)
	}
}

func TestRealRejectedUpdateCapture(t *testing.T) {
	dyn, err := os.ReadFile("testdata/real-listeners-error.json")
	if err != nil {
		t.Skip("no real error capture committed")
	}
	got, derr := serveListeners(t, string(dyn), `{}`).ListenerConfigs(context.Background())
	if derr != nil {
		t.Fatal(derr)
	}
	if len(got) != 1 {
		t.Fatalf("Envoy's two elements for one rejected update must merge into one listener, got %d: %+v", len(got), got)
	}
	d := got[0]
	if d.Name != "dynamic_one" || d.Active == nil || d.Active.VersionInfo != "7" || d.Error == nil ||
		d.Error.LastUpdateAttempt.IsZero() || !strings.Contains(d.Error.Details, "cluster_specifier") ||
		!strings.Contains(string(d.Error.Listener), "dynamic_one") {
		t.Fatalf("%+v", d)
	}
}

func TestListenerConfigsErrors(t *testing.T) {
	c := serveListeners(t, `{nope`, `{}`)
	if _, err := c.ListenerConfigs(context.Background()); err == nil || !strings.Contains(err.Error(), "dynamic_listeners") {
		t.Fatalf("invalid JSON must name the resource: %v", err)
	}
	c = newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 503) }), time.Second)
	if _, err := c.ListenerConfigs(context.Background()); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("non-2xx must surface: %v", err)
	}
}

func TestListenerConfigsFromRealCapture(t *testing.T) {
	dyn, err1 := os.ReadFile("testdata/real-listeners-dynamic.json")
	stat, err2 := os.ReadFile("testdata/real-listeners-static.json")
	if err1 != nil || err2 != nil {
		t.Skip("no real listener captures committed (see testdata/README.md)")
	}
	c := serveListeners(t, string(dyn), string(stat))
	got, err := c.ListenerConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := byName(got)
	if s := by["static_tcp"]; !s.Static || s.Active == nil || !strings.Contains(string(s.Active.Listener), "127.0.0.1") {
		t.Fatalf("static_tcp: %+v", s)
	}
	if m := by["static_multi"]; !m.Static || !strings.Contains(string(m.Active.Listener), "by-sni") {
		t.Fatalf("static_multi: %+v", m)
	}
	d := by["dynamic_one"]
	if d.Static || d.Active == nil || d.Active.VersionInfo == "" || d.Active.LastUpdated.IsZero() {
		t.Fatalf("dynamic_one: %+v", d)
	}
}
```

(`newClient` already exists in `admin_test.go` in the same package.)

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/admin/...`
Expected: FAIL (`ListenerConfigs`, `ListenerConfig` undefined).

- [ ] **Step 3: Implement the decoder**

`internal/admin/listeners.go`:

```go
package admin

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	typeDynamicListener = ".ListenersConfigDump.DynamicListener"
	typeStaticListener  = ".ListenersConfigDump.StaticListener"
	typeListenersDump   = ".ListenersConfigDump"
)

type ListenerState struct {
	VersionInfo string
	LastUpdated time.Time
	Listener    json.RawMessage
}

type ListenerError struct {
	LastUpdateAttempt time.Time
	Details           string
	Listener          json.RawMessage
}

// ListenerConfig is one listener as Envoy emitted it. A static listener has
// Static set and its single state in Active (empty VersionInfo).
type ListenerConfig struct {
	Name                      string
	Static                    bool
	Active, Warming, Draining *ListenerState
	Error                     *ListenerError
}

type rawState struct {
	VersionInfo string          `json:"version_info"`
	LastUpdated string          `json:"last_updated"`
	Listener    json.RawMessage `json:"listener"`
}

type rawDynamic struct {
	Name     string    `json:"name"`
	Active   *rawState `json:"active_state"`
	Warming  *rawState `json:"warming_state"`
	Draining *rawState `json:"draining_state"`
	Error    *struct {
		Failed  json.RawMessage `json:"failed_configuration"`
		Attempt string          `json:"last_update_attempt"`
		Details string          `json:"details"`
	} `json:"error_state"`
}

type rawStatic struct {
	Listener    json.RawMessage `json:"listener"`
	LastUpdated string          `json:"last_updated"`
}

type listenersDump struct {
	Static  []rawStatic  `json:"static_listeners"`
	Dynamic []rawDynamic `json:"dynamic_listeners"`
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func state(r *rawState) *ListenerState {
	if r == nil {
		return nil
	}
	return &ListenerState{VersionInfo: r.VersionInfo, LastUpdated: parseTime(r.LastUpdated), Listener: r.Listener}
}

func dynamicListener(d rawDynamic) (ListenerConfig, bool) {
	name := d.Name
	if name == "" && d.Error != nil {
		// A rejected update also yields a nameless element; the listener's name
		// is then only in the failed configuration.
		var fc struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(d.Error.Failed, &fc)
		name = fc.Name
	}
	lc := ListenerConfig{
		Name: name, Active: state(d.Active), Warming: state(d.Warming), Draining: state(d.Draining),
	}
	if d.Error != nil {
		lc.Error = &ListenerError{LastUpdateAttempt: parseTime(d.Error.Attempt), Details: d.Error.Details, Listener: d.Error.Failed}
	}
	if lc.Name == "" || (lc.Active == nil && lc.Warming == nil && lc.Draining == nil && lc.Error == nil) {
		return ListenerConfig{}, false
	}
	return lc, true
}

func staticListener(s rawStatic) (ListenerConfig, bool) {
	var l struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(s.Listener, &l) != nil || l.Name == "" {
		return ListenerConfig{}, false
	}
	return ListenerConfig{
		Name: l.Name, Static: true,
		Active: &ListenerState{LastUpdated: parseTime(s.LastUpdated), Listener: s.Listener},
	}, true
}

// mergeListener folds b into a: b only fills what a lacks, except that the error
// carrying a last_update_attempt wins over one without.
func mergeListener(a, b ListenerConfig) ListenerConfig {
	if a.Active == nil {
		a.Active = b.Active
	}
	if a.Warming == nil {
		a.Warming = b.Warming
	}
	if a.Draining == nil {
		a.Draining = b.Draining
	}
	switch {
	case a.Error == nil:
		a.Error = b.Error
	case b.Error != nil && a.Error.LastUpdateAttempt.IsZero() && !b.Error.LastUpdateAttempt.IsZero():
		a.Error = b.Error
	}
	return a
}

// decodeListenerConfigs walks /config_dump as raw JSON, like decodeClusterConfigs.
// With ?resource=dynamic_listeners|static_listeners Envoy emits one configs[]
// element per listener, typed ListenersConfigDump.DynamicListener or
// .StaticListener. A plain /config_dump has one wrapped ListenersConfigDump with
// nested arrays; that shape is accepted too, picking the list named by static.
// Anything unrecognised or malformed is skipped, never fatal.
func decodeListenerConfigs(body []byte, static bool) ([]ListenerConfig, error) {
	var dump struct {
		Configs []json.RawMessage `json:"configs"`
	}
	if err := json.Unmarshal(body, &dump); err != nil {
		return nil, err
	}
	var out []ListenerConfig
	idx := map[string]int{}
	add := func(lc ListenerConfig) { // elements for the same listener are merged
		if i, ok := idx[lc.Name]; ok {
			out[i] = mergeListener(out[i], lc)
			return
		}
		idx[lc.Name] = len(out)
		out = append(out, lc)
	}
	for _, raw := range dump.Configs {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			continue
		}
		var typ string
		if json.Unmarshal(obj["@type"], &typ) != nil {
			continue
		}
		switch {
		case strings.HasSuffix(typ, typeDynamicListener):
			var d rawDynamic
			if json.Unmarshal(raw, &d) != nil {
				continue
			}
			if lc, ok := dynamicListener(d); ok {
				add(lc)
			}
		case strings.HasSuffix(typ, typeStaticListener):
			var s rawStatic
			if json.Unmarshal(raw, &s) != nil {
				continue
			}
			if lc, ok := staticListener(s); ok {
				add(lc)
			}
		case strings.HasSuffix(typ, typeListenersDump):
			var ld listenersDump
			if json.Unmarshal(raw, &ld) != nil {
				continue
			}
			if static {
				for _, s := range ld.Static {
					if lc, ok := staticListener(s); ok {
						add(lc)
					}
				}
			} else {
				for _, d := range ld.Dynamic {
					if lc, ok := dynamicListener(d); ok {
						add(lc)
					}
				}
			}
		}
	}
	return out, nil
}
```

In `internal/admin/client.go` add `ListenerConfigs(ctx context.Context) ([]ListenerConfig, error)` to the `Client` interface and implement it next to `ClusterConfigs`:

```go
func (h *HTTP) ListenerConfigs(ctx context.Context) ([]ListenerConfig, error) {
	var all []ListenerConfig
	for _, res := range []string{"dynamic_listeners", "static_listeners"} {
		body, err := h.get(ctx, "/config_dump", url.Values{"resource": {res}})
		if err != nil {
			return nil, err
		}
		cfgs, err := decodeListenerConfigs(body, res == "static_listeners")
		if err != nil {
			return nil, fmt.Errorf("GET /config_dump: decode %s: %w", res, err)
		}
		all = append(all, cfgs...)
	}
	return all, nil
}
```

Any test stub that embeds `admin.Client` (snapshot tests) keeps compiling because the interface is embedded.

- [ ] **Step 4: Run and verify**

Run: `go test -race ./internal/admin/...` and `make check`
Expected: PASS (the real-capture test passes when Task 1 produced files, otherwise it reports SKIP; say which in the report).

- [ ] **Step 5: Mutation-check and commit**

Temporarily make `decodeListenerConfigs` skip the `typeStaticListener` case: `TestListenerConfigsDecodesEveryState` must fail. Restore it (copy the file back; do not use `git checkout` on uncommitted work).

```bash
git add internal/admin
git commit -m "feat(admin): decode listener config dumps" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Lazy YAML and parallel secondary fetches

**Files:**
- Create: `internal/snapshot/config.go`, `internal/snapshot/config_test.go`
- Modify: `internal/snapshot/snapshot.go`, `internal/snapshot/snapshot_test.go`, `internal/ui/detailpane.go` (two small reads), tests that read `Config.YAML` from a fetched snapshot

**Interfaces:**
- Produces:

```go
type ConfigInfo struct {
    YAML        string // pre-rendered text; wins over JSON when set (tests and callers that already have YAML)
    JSON        []byte // raw JSON as Envoy emitted it, rendered lazily by Render
    VersionInfo string
    LastUpdated time.Time
    Static      bool
    Err         string
    // unexported: lazy cache
}
func newConfigInfo(json []byte) ConfigInfo                 // JSON plus a fresh cache
func (c ConfigInfo) Render() (text, problem string)        // YAML text, or why there is none
func (c ConfigInfo) rendered() bool                        // test hook: has the conversion run?
```

`Render` returns `("", c.Err)` when `Err` is set; `(c.YAML, "")` when `YAML` is set; `("", "")` when there is neither JSON nor YAML; otherwise the converted YAML, or `("", "config unavailable: render yaml: <err>")`.

- [ ] **Step 1: Write the failing tests**

`internal/snapshot/config_test.go`:

```go
package snapshot

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	adminv3 "github.com/envoyproxy/go-control-plane/envoy/admin/v3"

	"github.com/regeda/txenvoy/internal/admin"
)

func TestRenderConvertsLazilyAndCaches(t *testing.T) {
	c := newConfigInfo([]byte(`{"name":"api","type":"EDS"}`))
	if c.rendered() {
		t.Fatal("nothing is converted until Render is called")
	}
	text, problem := c.Render()
	if problem != "" || !strings.Contains(text, "name: api") || !strings.Contains(text, "type: EDS") {
		t.Fatalf("text=%q problem=%q", text, problem)
	}
	if !c.rendered() {
		t.Fatal("Render must mark the cache")
	}
	again, _ := c.Render()
	if again != text {
		t.Fatal("cached text must be stable")
	}
	// a copy of the struct shares the cache
	cp := c
	if !cp.rendered() {
		t.Fatal("copies share one cache")
	}
}

func TestRenderPrecedenceAndProblems(t *testing.T) {
	if text, problem := (ConfigInfo{Err: "config unavailable: x", YAML: "a: 1"}).Render(); text != "" || problem != "config unavailable: x" {
		t.Fatalf("Err wins: %q %q", text, problem)
	}
	if text, problem := (ConfigInfo{YAML: "a: 1\n", JSON: []byte(`{"b":2}`)}).Render(); text != "a: 1\n" || problem != "" {
		t.Fatalf("explicit YAML wins over JSON: %q %q", text, problem)
	}
	if text, problem := (ConfigInfo{}).Render(); text != "" || problem != "" {
		t.Fatalf("empty: %q %q", text, problem)
	}
	_, problem := newConfigInfo([]byte(`{not json`)).Render()
	if !strings.HasPrefix(problem, "config unavailable: render yaml:") {
		t.Fatalf("bad JSON must surface as a problem: %q", problem)
	}
	// a value built without the constructor still renders (uncached)
	if text, _ := (ConfigInfo{JSON: []byte(`{"a":1}`)}).Render(); !strings.Contains(text, "a: 1") {
		t.Fatalf("uncached render: %q", text)
	}
}

func TestRenderIsSafeConcurrently(t *testing.T) {
	c := newConfigInfo([]byte(`{"a":1}`))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Render() }()
	}
	wg.Wait()
}

// Fetch must not convert any config: only a viewed item is ever rendered.
func TestFetchDoesNotConvertYAML(t *testing.T) {
	s := fetchFromDemo(t)
	if len(s.Clusters) == 0 {
		t.Fatal("precondition: clusters")
	}
	for _, c := range s.Clusters {
		if c.Config.rendered() {
			t.Fatalf("%s was converted during Fetch", c.Name)
		}
		if c.Config.YAML != "" || len(c.Config.JSON) == 0 {
			t.Fatalf("%s: JSON must be kept raw, YAML empty (got YAML %d bytes, JSON %d)", c.Name, len(c.Config.YAML), len(c.Config.JSON))
		}
	}
	text, _ := s.Clusters[0].Config.Render()
	if text == "" || !s.Clusters[0].Config.rendered() {
		t.Fatal("rendering the selected item converts it")
	}
}

// barrierClient proves the secondary calls run in parallel: each waits until
// both have started, so a sequential Fetch would deadlock until the timeout.
type barrierClient struct {
	admin.Client
	started chan struct{}
}

func (b barrierClient) wait() {
	b.started <- struct{}{}
	deadline := time.After(2 * time.Second)
	for len(b.started) < 2 {
		select {
		case <-deadline:
			return
		case <-time.After(time.Millisecond):
		}
	}
}

func (b barrierClient) ClusterConfigs(ctx context.Context) ([]admin.ClusterConfig, error) {
	b.wait()
	return b.Client.ClusterConfigs(ctx)
}

func (b barrierClient) ServerInfo(ctx context.Context) (*adminv3.ServerInfo, error) {
	b.wait()
	return b.Client.ServerInfo(ctx)
}

func TestFetchRunsSecondaryCallsInParallel(t *testing.T) {
	real := demoClient(t)
	b := barrierClient{Client: real, started: make(chan struct{}, 4)}
	start := time.Now()
	if _, err := Fetch(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) >= 1900*time.Millisecond {
		t.Fatal("ClusterConfigs and ServerInfo were not called concurrently")
	}
}
```

Add two small helpers to `internal/snapshot/snapshot_test.go` (reuse existing code: `fetchFrom(t, sc)` already builds a client from a scenario) :

```go
func demoClient(t *testing.T) admin.Client {
	t.Helper()
	srv := httptest.NewServer(demo.NewServer(demo.Degraded()))
	t.Cleanup(srv.Close)
	c, err := admin.NewHTTP(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func fetchFromDemo(t *testing.T) Snapshot { return fetchFrom(t, demo.Degraded()) }
```

Also update existing assertions in `snapshot_test.go` that read `Config.YAML` from a fetched snapshot to go through `Render()`: `api.Config.YAML` containing `name: api-gateway` becomes `text, _ := api.Config.Render()` and the same `strings.Contains`; `Config.YAML != ""` checks for config-failure cases (`TestPartialFailures`: `c.Config.YAML != ""`) become `text, _ := c.Config.Render(); text != ""`. Anything that asserted `Config.YAML == ""` still holds.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/snapshot/...`
Expected: FAIL (`newConfigInfo`, `Render`, `rendered` undefined).

- [ ] **Step 3: Implement ConfigInfo**

`internal/snapshot/config.go`:

```go
package snapshot

import (
	"sync"
	"time"

	"sigs.k8s.io/yaml"
)

// ConfigInfo is one item's config. JSON is converted to YAML lazily, on first
// Render, and cached for every copy of this value; Fetch never converts, so a
// poll over thousands of listeners costs nothing until one is viewed.
type ConfigInfo struct {
	YAML        string // pre-rendered text; wins over JSON when set
	JSON        []byte // raw JSON as Envoy emitted it
	VersionInfo string
	LastUpdated time.Time
	Static      bool
	Err         string

	lazy *lazyYAML
}

type lazyYAML struct {
	once sync.Once
	done bool
	text string
	err  error
}

func newConfigInfo(json []byte) ConfigInfo {
	return ConfigInfo{JSON: json, lazy: &lazyYAML{}}
}

// Render returns the YAML text, or the reason there is none.
func (c ConfigInfo) Render() (text, problem string) {
	switch {
	case c.Err != "":
		return "", c.Err
	case c.YAML != "":
		return c.YAML, ""
	case len(c.JSON) == 0:
		return "", ""
	}
	lz := c.lazy
	if lz == nil {
		lz = &lazyYAML{} // built without newConfigInfo: convert, uncached
	}
	lz.once.Do(func() {
		y, err := yaml.JSONToYAML(c.JSON)
		lz.text, lz.err, lz.done = string(y), err, true
	})
	if lz.err != nil {
		return "", "config unavailable: render yaml: " + lz.err.Error()
	}
	return lz.text, ""
}

// rendered reports whether the conversion has run (a test hook).
func (c ConfigInfo) rendered() bool { return c.lazy != nil && c.lazy.done }
```

`rendered()` reads `done` without synchronisation; it is only used by tests after `Render`/`Fetch` have returned in the same goroutine, so no race arises. If `go test -race` flags it, guard `done` with `sync/atomic` (`atomic.Bool`).

- [ ] **Step 4: Rework Fetch (clusters only so far) and ConfigInfo use**

In `internal/snapshot/snapshot.go` delete the old `ConfigInfo` type, remove the `sigs.k8s.io/yaml` import, and replace `Fetch` with a version that runs the two secondary calls in parallel and stores JSON instead of YAML:

```go
func Fetch(ctx context.Context, c admin.Client) (Snapshot, error) {
	cl, err := c.Clusters(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	var (
		wg      sync.WaitGroup
		cfgs    []admin.ClusterConfig
		cfgErr  error
		info    *adminv3.ServerInfo
	)
	wg.Add(2)
	go func() { defer wg.Done(); cfgs, cfgErr = c.ClusterConfigs(ctx) }()
	go func() { defer wg.Done(); info, _ = c.ServerInfo(ctx) }()
	wg.Wait()

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
			cluster.Config = newConfigInfo(cc.JSON)
			cluster.Config.VersionInfo = cc.VersionInfo
			cluster.Config.LastUpdated = cc.LastUpdated
			cluster.Config.Static = cc.Static
		}
		out.Clusters = append(out.Clusters, cluster)
	}
	sortClusters(out.Clusters)
	return out, nil
}
```

Add `"sync"` to the imports and run `gofmt`. `info.GetVersion()` is nil-safe.

- [ ] **Step 5: Adapt the detail pane's two reads**

In `internal/ui/detailpane.go` replace every read of `c.Config.YAML` / `d.cluster.Config.YAML` / `Config.Err` with `Render()`:
- In `syncConfig`: `text, _ := c.Config.Render()`; compare `d.cfgYAML != text`, call `highlightYAML(text, d.color)`, and store `d.cfgYAML = text`.
- In `applyContent`: `if _, problem := d.cluster.Config.Render(); problem != "" { d.vp.SetContent(clipLines(dimStyle.Render(problem), w)); return }`.

UI tests that set `Config.YAML` or `Config.Err` directly keep working because `Render` honours both.

- [ ] **Step 6: Run and verify**

Run: `make check` (the whole suite, goldens unchanged: `go test ./internal/ui/ -run Golden` without `-update` must pass).
Expected: PASS. Mutation check: make `Fetch` call `cluster.Config.Render()` for every cluster; `TestFetchDoesNotConvertYAML` must fail. Restore the file by copy.

- [ ] **Step 7: Commit**

```bash
git add internal/snapshot internal/ui
git commit -m "feat(snapshot): render config YAML lazily and fetch secondary data in parallel" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Snapshot listeners

**Files:**
- Create: `internal/snapshot/listener.go`, `internal/snapshot/listener_test.go`
- Modify: `internal/snapshot/snapshot.go`

**Interfaces:**
- Consumes: `admin.ListenerConfig`, `admin.Client.ListenerConfigs` (Task 3), `ConfigInfo`/`newConfigInfo` (Task 4), demo listeners (Task 2).
- Produces:

```go
type ListenerStatus int // ListenerActive, ListenerWarming, ListenerDraining, ListenerError
func (ListenerStatus) String() string
type FilterChain struct { Label string; Match, Filters []string; TLS bool }
type Listener struct {
    Name, Address string
    State         ListenerStatus
    Static        bool
    VersionInfo   string
    LastUpdated   time.Time
    ErrorDetails  string
    ServingOld    bool
    Chains        []FilterChain
    Config        ConfigInfo
}
func (Listener) StateText() string   // "static" for static listeners, else State.String()
// Snapshot gains:
//   Listeners    []Listener
//   ListenersErr string                // "listeners unavailable: <reason>" when the dump failed
func (Snapshot) ListenersNotActive() int
```

- [ ] **Step 1: Write the failing tests**

`internal/snapshot/listener_test.go`:

```go
package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/regeda/txenvoy/internal/admin"
	"github.com/regeda/txenvoy/internal/demo"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func st(version, listener string) *admin.ListenerState {
	return &admin.ListenerState{VersionInfo: version, LastUpdated: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC), Listener: raw(listener)}
}

func TestListenerStatePrecedence(t *testing.T) {
	l := `{"name":"x","address":{"socket_address":{"address":"1.2.3.4","port_value":80}}}`
	tests := []struct {
		name       string
		lc         admin.ListenerConfig
		wantState  ListenerStatus
		wantText   string
		wantVer    string
		servingOld bool
	}{
		{"active", admin.ListenerConfig{Name: "x", Active: st("7", l)}, ListenerActive, "active", "7", false},
		{"static", admin.ListenerConfig{Name: "x", Static: true, Active: st("", l)}, ListenerActive, "static", "", false},
		{"warming with old", admin.ListenerConfig{Name: "x", Active: st("6", l), Warming: st("7", l)}, ListenerWarming, "warming", "6", false},
		{"warming only", admin.ListenerConfig{Name: "x", Warming: st("7", l)}, ListenerWarming, "warming", "7", false},
		{"draining beats warming", admin.ListenerConfig{Name: "x", Warming: st("8", l), Draining: st("5", l)}, ListenerDraining, "draining", "8", false},
		{"error beats all", admin.ListenerConfig{Name: "x", Active: st("3", l), Draining: st("2", l),
			Error: &admin.ListenerError{Details: "boom", Listener: raw(l)}}, ListenerError, "error", "3", true},
		{"error never worked", admin.ListenerConfig{Name: "x", Error: &admin.ListenerError{Details: "nope",
			LastUpdateAttempt: time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC), Listener: raw(l)}}, ListenerError, "error", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := listenerFrom(tt.lc)
			if got.State != tt.wantState || got.StateText() != tt.wantText || got.VersionInfo != tt.wantVer || got.ServingOld != tt.servingOld {
				t.Fatalf("state=%v text=%q ver=%q servingOld=%v", got.State, got.StateText(), got.VersionInfo, got.ServingOld)
			}
			if got.Address != "1.2.3.4:80" {
				t.Fatalf("address %q", got.Address)
			}
			if text, _ := got.Config.Render(); !strings.Contains(text, "name: x") {
				t.Fatalf("the Config tab shows the shown listener: %q", text)
			}
		})
	}
	e := listenerFrom(admin.ListenerConfig{Name: "x", Error: &admin.ListenerError{Details: "nope\nline2\n",
		LastUpdateAttempt: time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC), Listener: raw(l)}})
	if e.ErrorDetails != "nope\nline2" || e.LastUpdated.Hour() != 11 {
		t.Fatalf("error-only listeners use the attempt time: %+v", e)
	}
}

func TestListenerAddressFormats(t *testing.T) {
	cases := map[string]string{
		`{"address":{"socket_address":{"address":"0.0.0.0","port_value":8080}}}`: "0.0.0.0:8080",
		`{"address":{"socket_address":{"address":"::1","port_value":443}}}`:      "[::1]:443",
		`{"address":{"socket_address":{"address":"0.0.0.0"}}}`:                   "0.0.0.0:0",
		`{"address":{"pipe":{"path":"/var/run/a.sock"}}}`:                        "unix:/var/run/a.sock",
		`{"address":{"envoy_internal_address":{"server_listener_name":"inner"}}}`: "internal:inner",
		`{"address":{}}`: "",
		`{}`:             "",
		`not json`:       "",
	}
	for in, want := range cases {
		if got, _ := parseListener(raw(in)); got != want {
			t.Errorf("%s -> %q, want %q", in, got, want)
		}
	}
}

func TestFilterChainParsing(t *testing.T) {
	_, chains := parseListener(raw(`{
	 "filter_chains":[
	  {"name":"by-sni","filter_chain_match":{"server_names":["a.com","b.com"],"destination_port":443,
	     "prefix_ranges":[{"address_prefix":"10.0.0.0","prefix_len":8}],
	     "source_prefix_ranges":[{"address_prefix":"192.168.0.0","prefix_len":16}],
	     "source_ports":[1,2],"source_type":"SAME_IP_OR_LOOPBACK","transport_protocol":"tls",
	     "application_protocols":["h2","http/1.1"]},
	   "filters":[{"name":"envoy.filters.network.http_connection_manager"},{"name":"istio.stats"}],
	   "transport_socket":{"name":"envoy.transport_sockets.tls"}},
	  {"filters":[{"name":"envoy.filters.network.tcp_proxy"}]},
	  {"filter_chain_match":{},"filters":[]}
	 ],
	 "default_filter_chain":{"name":"ignored-name","filters":[{"name":"envoy.filters.network.tcp_proxy"}]}}`))
	if len(chains) != 4 {
		t.Fatalf("got %d chains: %+v", len(chains), chains)
	}
	a := chains[0]
	wantMatch := "sni=a.com,b.com port=443 dst=10.0.0.0/8 src=192.168.0.0/16 src_port=1,2 src_type=same_ip_or_loopback transport=tls alpn=h2,http/1.1"
	if a.Label != "by-sni" || strings.Join(a.Match, " ") != wantMatch {
		t.Fatalf("chain 1: %q / %q", a.Label, strings.Join(a.Match, " "))
	}
	if strings.Join(a.Filters, ",") != "http_connection_manager,istio.stats" || !a.TLS {
		t.Fatalf("filters %v tls=%v (the envoy.filters.network. prefix is stripped, others kept)", a.Filters, a.TLS)
	}
	if b := chains[1]; b.Label != "#2" || len(b.Match) != 0 || b.TLS || strings.Join(b.Filters, ",") != "tcp_proxy" {
		t.Fatalf("an unnamed chain is #N with no match: %+v", b)
	}
	if c := chains[2]; c.Label != "#3" || len(c.Match) != 0 || len(c.Filters) != 0 {
		t.Fatalf("an empty match is no match: %+v", c)
	}
	if d := chains[3]; d.Label != "default" {
		t.Fatalf("default_filter_chain is labelled default: %+v", d)
	}
	if _, none := parseListener(raw(`{"address":{}}`)); len(none) != 0 {
		t.Fatal("no filter_chains means no chains")
	}
	// source_type ANY is not shown
	_, c := parseListener(raw(`{"filter_chains":[{"filter_chain_match":{"source_type":"ANY"}}]}`))
	if len(c) != 1 || len(c[0].Match) != 0 {
		t.Fatalf("ANY source type is omitted: %+v", c)
	}
}

func TestListenerSortOrder(t *testing.T) {
	l := `{"name":"x"}`
	cfgs := []admin.ListenerConfig{
		{Name: "z-active", Active: st("1", l)},
		{Name: "a-active", Active: st("1", l)},
		{Name: "m-warming", Warming: st("2", l)},
		{Name: "n-draining", Draining: st("2", l)},
		{Name: "e-error", Error: &admin.ListenerError{Listener: raw(l)}},
		{Name: "s-static", Static: true, Active: st("", l)},
	}
	var ls []Listener
	for _, c := range cfgs {
		ls = append(ls, listenerFrom(c))
	}
	sortListeners(ls)
	var order []string
	for _, l := range ls {
		order = append(order, l.Name)
	}
	if got := strings.Join(order, " "); got != "e-error n-draining m-warming a-active s-static z-active" {
		t.Fatalf("order = %s", got)
	}
}

func TestNoAddressNoChainsDoesNotPanic(t *testing.T) {
	l := listenerFrom(admin.ListenerConfig{Name: "bare", Active: st("1", `{"name":"bare"}`)})
	if l.Address != "" || len(l.Chains) != 0 {
		t.Fatalf("%+v", l)
	}
	if got := listenerFrom(admin.ListenerConfig{Name: "nolistener", Active: &admin.ListenerState{VersionInfo: "1"}}); got.Address != "" {
		t.Fatalf("a state without a listener: %+v", got)
	}
}

func TestFetchListenersFromDemo(t *testing.T) {
	s := fetchFrom(t, demo.Degraded())
	if s.ListenersErr != "" || len(s.Listeners) != 7 {
		t.Fatalf("err=%q listeners=%d", s.ListenersErr, len(s.Listeners))
	}
	by := map[string]Listener{}
	for _, l := range s.Listeners {
		by[l.Name] = l
	}
	if l := by["bad-update"]; l.State != ListenerError || !l.ServingOld || !strings.Contains(l.ErrorDetails, "unknown filter") {
		t.Fatalf("bad-update: %+v", l)
	}
	vi := by["virtualInbound"]
	if vi.State != ListenerActive || vi.Address != "0.0.0.0:15006" || vi.VersionInfo != "7" || len(vi.Chains) != 3 {
		t.Fatalf("virtualInbound: %+v", vi)
	}
	if vi.Chains[0].Label != "#1" || !vi.Chains[0].TLS || vi.Chains[1].Label != "by-sni" || vi.Chains[2].Label != "default" {
		t.Fatalf("chains: %+v", vi.Chains)
	}
	if l := by["uds-agent"]; !l.Static || l.Address != "unix:/var/run/agent.sock" || l.StateText() != "static" {
		t.Fatalf("uds-agent: %+v", l)
	}
	if l := by["orders-egress"]; l.State != ListenerWarming || l.VersionInfo != "prev" {
		t.Fatalf("a warming update shows the serving version: %+v", l)
	}
	if got := s.ListenersNotActive(); got != 3 {
		t.Fatalf("not active (warming, draining, error) = %d, want 3", got)
	}
	// ordering: error, draining, warming, then active/static by name
	var order []string
	for _, l := range s.Listeners {
		order = append(order, l.Name)
	}
	if got := strings.Join(order, " "); got != "bad-update legacy-9090 orders-egress 0.0.0.0_8080 admin-static uds-agent virtualInbound" {
		t.Fatalf("order = %s", got)
	}
	// nothing was converted to YAML during Fetch
	for _, l := range s.Listeners {
		if l.Config.rendered() {
			t.Fatalf("%s converted during Fetch", l.Name)
		}
	}
}

func TestListenerDumpFailureIsIsolated(t *testing.T) {
	srv := httptest.NewServer(demo.NewServer(demo.Degraded()))
	defer srv.Close()
	c, _ := admin.NewHTTP(srv.URL, time.Second)

	s, err := Fetch(context.Background(), stub{Client: c, listenersErr: errors.New("boom")})
	if err != nil {
		t.Fatalf("a listener failure must not fail the snapshot: %v", err)
	}
	if s.ListenersErr != "listeners unavailable: boom" || len(s.Listeners) != 0 || len(s.Clusters) == 0 {
		t.Fatalf("err=%q listeners=%d clusters=%d", s.ListenersErr, len(s.Listeners), len(s.Clusters))
	}

	// the same through real HTTP: only the listener dump is failed
	d := demo.NewServer(demo.Degraded())
	h := httptest.NewServer(d)
	defer h.Close()
	d.Inject(demo.Fault{Status: http.StatusServiceUnavailable, Match: "dynamic_listeners"})
	c2, _ := admin.NewHTTP(h.URL, time.Second)
	s, err = Fetch(context.Background(), c2)
	if err != nil || !strings.HasPrefix(s.ListenersErr, "listeners unavailable: ") || !strings.Contains(s.ListenersErr, "503") || len(s.Clusters) == 0 {
		t.Fatalf("err=%v listenersErr=%q clusters=%d", err, s.ListenersErr, len(s.Clusters))
	}
}

func TestEmptyListenerDumps(t *testing.T) {
	s := fetchFrom(t, demo.Scenario{Envoy: "x/1.0.0/Clean", Updated: time.Now()})
	if len(s.Listeners) != 0 || s.ListenersErr != "" || s.ListenersNotActive() != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestRealRejectedUpdateThroughFetch(t *testing.T) {
	dyn, err := os.ReadFile("../admin/testdata/real-listeners-error.json")
	if err != nil {
		t.Skip("no real error capture committed")
	}
	clusters, _ := os.ReadFile("../admin/testdata/real-clusters.json")
	cdyn, _ := os.ReadFile("../admin/testdata/real-config-dynamic.json")
	cstat, _ := os.ReadFile("../admin/testdata/real-config-static.json")
	info, _ := os.ReadFile("../admin/testdata/real-server-info.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/clusters":
			_, _ = w.Write(clusters)
		case r.URL.Path == "/server_info":
			_, _ = w.Write(info)
		case r.URL.Query().Get("resource") == "dynamic_listeners":
			_, _ = w.Write(dyn)
		case r.URL.Query().Get("resource") == "static_listeners":
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Query().Get("resource") == "dynamic_active_clusters":
			_, _ = w.Write(cdyn)
		default:
			_, _ = w.Write(cstat)
		}
	}))
	defer srv.Close()
	c, _ := admin.NewHTTP(srv.URL, time.Second)
	s, err := Fetch(context.Background(), c)
	if err != nil || len(s.Listeners) != 1 {
		t.Fatalf("%v listeners=%d: Envoy's two elements are one listener", err, len(s.Listeners))
	}
	l := s.Listeners[0]
	if l.Name != "dynamic_one" || l.State != ListenerError || !l.ServingOld || l.VersionInfo != "7" ||
		!strings.Contains(l.ErrorDetails, "cluster_specifier") || strings.HasSuffix(l.ErrorDetails, "\n") {
		t.Fatalf("%+v", l)
	}
	if text, _ := l.Config.Render(); !strings.Contains(text, "name: dynamic_one") {
		t.Fatalf("the Config tab shows the serving (old) listener:\n%s", text)
	}
}

func TestRealListenerCapturesThroughFetch(t *testing.T) {
	dyn, err1 := os.ReadFile("../admin/testdata/real-listeners-dynamic.json")
	stat, err2 := os.ReadFile("../admin/testdata/real-listeners-static.json")
	if err1 != nil || err2 != nil {
		t.Skip("no real listener captures committed")
	}
	clusters, _ := os.ReadFile("../admin/testdata/real-clusters.json")
	cdyn, _ := os.ReadFile("../admin/testdata/real-config-dynamic.json")
	cstat, _ := os.ReadFile("../admin/testdata/real-config-static.json")
	info, _ := os.ReadFile("../admin/testdata/real-server-info.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/clusters":
			_, _ = w.Write(clusters)
		case r.URL.Path == "/server_info":
			_, _ = w.Write(info)
		case r.URL.Query().Get("resource") == "dynamic_listeners":
			_, _ = w.Write(dyn)
		case r.URL.Query().Get("resource") == "static_listeners":
			_, _ = w.Write(stat)
		case r.URL.Query().Get("resource") == "dynamic_active_clusters":
			_, _ = w.Write(cdyn)
		default:
			_, _ = w.Write(cstat)
		}
	}))
	defer srv.Close()
	c, _ := admin.NewHTTP(srv.URL, time.Second)
	s, err := Fetch(context.Background(), c)
	if err != nil || s.ListenersErr != "" {
		t.Fatalf("%v %q", err, s.ListenersErr)
	}
	by := map[string]Listener{}
	for _, l := range s.Listeners {
		by[l.Name] = l
	}
	m := by["static_multi"]
	if !m.Static || m.Address != "0.0.0.0:10002" || len(m.Chains) != 3 || m.Chains[0].Label != "by-sni" ||
		!strings.Contains(strings.Join(m.Chains[0].Match, " "), "sni=a.example.com,b.example.com") || m.Chains[2].Label != "default" {
		t.Fatalf("static_multi from a real capture: %+v", m)
	}
	if d := by["dynamic_one"]; d.Static || d.State != ListenerActive || d.VersionInfo == "" || d.Address != "0.0.0.0:10003" {
		t.Fatalf("dynamic_one: %+v", d)
	}
}
```

Extend the existing `stub` type in `snapshot_test.go` with a listener hook:

```go
type stub struct {
	admin.Client
	clustersErr, cfgErr, infoErr, listenersErr error
}

func (s stub) ListenerConfigs(ctx context.Context) ([]admin.ListenerConfig, error) {
	if s.listenersErr != nil {
		return nil, s.listenersErr
	}
	return s.Client.ListenerConfigs(ctx)
}
```

(keep the other three overrides as they are; `real-clusters.json` etc. are the cluster captures from the earlier work.)

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/snapshot/...`
Expected: FAIL (`listenerFrom`, `parseListener`, `Listener`, ... undefined).

- [ ] **Step 3: Implement**

`internal/snapshot/listener.go`:

```go
package snapshot

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/regeda/txenvoy/internal/admin"
)

type ListenerStatus int

const (
	ListenerActive ListenerStatus = iota
	ListenerWarming
	ListenerDraining
	ListenerError
)

func (s ListenerStatus) String() string {
	return [...]string{"active", "warming", "draining", "error"}[s]
}

type FilterChain struct {
	Label   string
	Match   []string
	Filters []string
	TLS     bool
}

type Listener struct {
	Name, Address string
	State         ListenerStatus
	Static        bool
	VersionInfo   string
	LastUpdated   time.Time
	ErrorDetails  string
	ServingOld    bool // an error/warming update with an older version still active
	Chains        []FilterChain
	Config        ConfigInfo
}

// StateText is what the UI shows: "static" for static listeners.
func (l Listener) StateText() string {
	if l.Static && l.State == ListenerActive {
		return "static"
	}
	return l.State.String()
}

// listenerFrom turns one admin.ListenerConfig into a Listener. The shown
// listener (address, chains, Config) is the one serving: active, else warming,
// else draining; a listener that only ever failed shows the rejected config.
func listenerFrom(lc admin.ListenerConfig) Listener {
	l := Listener{Name: lc.Name, Static: lc.Static}
	var serving *admin.ListenerState
	for _, s := range []*admin.ListenerState{lc.Active, lc.Warming, lc.Draining} {
		if s != nil {
			serving = s
			break
		}
	}
	switch {
	case lc.Error != nil:
		l.State = ListenerError
		l.ErrorDetails = strings.TrimSpace(lc.Error.Details) // Envoy ends it with a newline
		l.ServingOld = lc.Active != nil
	case lc.Draining != nil:
		l.State = ListenerDraining
	case lc.Warming != nil:
		l.State = ListenerWarming
	default:
		l.State = ListenerActive
	}

	var shown json.RawMessage
	if serving != nil {
		l.VersionInfo, l.LastUpdated, shown = serving.VersionInfo, serving.LastUpdated, serving.Listener
	} else if lc.Error != nil {
		l.LastUpdated, shown = lc.Error.LastUpdateAttempt, lc.Error.Listener
	}
	l.Address, l.Chains = parseListener(shown)
	l.Config = newConfigInfo(shown)
	l.Config.VersionInfo, l.Config.LastUpdated, l.Config.Static = l.VersionInfo, l.LastUpdated, l.Static
	return l
}

func listenerSeverity(s ListenerStatus) int {
	switch s {
	case ListenerError:
		return 0
	case ListenerDraining:
		return 1
	case ListenerWarming:
		return 2
	default:
		return 3
	}
}

func sortListeners(ls []Listener) {
	sort.SliceStable(ls, func(i, j int) bool {
		si, sj := listenerSeverity(ls[i].State), listenerSeverity(ls[j].State)
		if si != sj {
			return si < sj
		}
		return ls[i].Name < ls[j].Name
	})
}

// parseListener extracts the bind address and filter chains from a listener's
// JSON, best effort: a malformed part yields blanks, never a panic.
func parseListener(raw json.RawMessage) (address string, chains []FilterChain) {
	var l struct {
		Address      json.RawMessage   `json:"address"`
		FilterChains []json.RawMessage `json:"filter_chains"`
		Default      json.RawMessage   `json:"default_filter_chain"`
	}
	_ = json.Unmarshal(raw, &l)
	for i, c := range l.FilterChains {
		chains = append(chains, parseChain(c, fmt.Sprintf("#%d", i+1)))
	}
	if len(l.Default) > 0 && string(l.Default) != "null" {
		chains = append(chains, parseChain(l.Default, "default"))
	}
	return parseAddress(l.Address), chains
}

func parseAddress(raw json.RawMessage) string {
	var a struct {
		Socket struct {
			Address string      `json:"address"`
			Port    json.Number `json:"port_value"`
		} `json:"socket_address"`
		Pipe struct {
			Path string `json:"path"`
		} `json:"pipe"`
		Internal struct {
			Name string `json:"server_listener_name"`
		} `json:"envoy_internal_address"`
	}
	_ = json.Unmarshal(raw, &a)
	switch {
	case a.Socket.Address != "":
		port := a.Socket.Port.String()
		if port == "" {
			port = "0" // proto3 default, omitted by Envoy
		}
		return net.JoinHostPort(a.Socket.Address, port)
	case a.Pipe.Path != "":
		return "unix:" + a.Pipe.Path
	case a.Internal.Name != "":
		return "internal:" + a.Internal.Name
	}
	return ""
}

const networkFilterPrefix = "envoy.filters.network."

func parseChain(raw json.RawMessage, label string) FilterChain {
	var c struct {
		Name    string                     `json:"name"`
		Match   map[string]json.RawMessage `json:"filter_chain_match"`
		Filters []struct {
			Name string `json:"name"`
		} `json:"filters"`
		TransportSocket struct {
			Name string `json:"name"`
		} `json:"transport_socket"`
	}
	_ = json.Unmarshal(raw, &c)
	fc := FilterChain{Label: label, Match: matchParts(c.Match), TLS: strings.Contains(c.TransportSocket.Name, "tls")}
	if label != "default" && c.Name != "" {
		fc.Label = c.Name
	}
	for _, f := range c.Filters {
		fc.Filters = append(fc.Filters, strings.TrimPrefix(f.Name, networkFilterPrefix))
	}
	return fc
}

// matchParts summarises filter_chain_match in a fixed order, only for set fields.
func matchParts(m map[string]json.RawMessage) []string {
	var parts []string
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	add("sni", strings.Join(strList(m["server_names"]), ","))
	add("port", numStr(m["destination_port"]))
	add("dst", strings.Join(cidrList(m["prefix_ranges"]), ","))
	add("src", strings.Join(cidrList(m["source_prefix_ranges"]), ","))
	add("src_port", strings.Join(numList(m["source_ports"]), ","))
	if st := strVal(m["source_type"]); st != "" && st != "ANY" {
		add("src_type", strings.ToLower(st))
	}
	add("transport", strVal(m["transport_protocol"]))
	add("alpn", strings.Join(strList(m["application_protocols"]), ","))
	return parts
}

func strVal(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func strList(raw json.RawMessage) []string {
	var s []string
	_ = json.Unmarshal(raw, &s)
	return s
}

func numStr(raw json.RawMessage) string {
	var n json.Number
	if json.Unmarshal(raw, &n) != nil {
		return ""
	}
	return n.String()
}

func numList(raw json.RawMessage) []string {
	var ns []json.Number
	_ = json.Unmarshal(raw, &ns)
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.String())
	}
	return out
}

func cidrList(raw json.RawMessage) []string {
	var cs []struct {
		Prefix string      `json:"address_prefix"`
		Len    json.Number `json:"prefix_len"`
	}
	_ = json.Unmarshal(raw, &cs)
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		if c.Prefix != "" {
			out = append(out, c.Prefix+"/"+c.Len.String())
		}
	}
	return out
}
```

Add `"time"` to the imports. Then in `snapshot.go` extend `Snapshot` with `Listeners []Listener` and `ListenersErr string`, add

```go
// ListenersNotActive counts listeners that are warming, draining or in error.
func (s Snapshot) ListenersNotActive() int {
	n := 0
	for _, l := range s.Listeners {
		if l.State != ListenerActive {
			n++
		}
	}
	return n
}
```

and make `Fetch` fetch listeners in the same parallel group (three goroutines):

```go
	var (
		wg           sync.WaitGroup
		cfgs         []admin.ClusterConfig
		cfgErr       error
		info         *adminv3.ServerInfo
		lcs          []admin.ListenerConfig
		listenersErr error
	)
	wg.Add(3)
	go func() { defer wg.Done(); cfgs, cfgErr = c.ClusterConfigs(ctx) }()
	go func() { defer wg.Done(); info, _ = c.ServerInfo(ctx) }()
	go func() { defer wg.Done(); lcs, listenersErr = c.ListenerConfigs(ctx) }()
	wg.Wait()
```

and, just before `return out, nil`:

```go
	if listenersErr != nil {
		out.ListenersErr = "listeners unavailable: " + listenersErr.Error()
	}
	for _, lc := range lcs {
		out.Listeners = append(out.Listeners, listenerFrom(lc))
	}
	sortListeners(out.Listeners)
```

The barrier test in Task 4 uses two parallel calls and still holds (the third call is not blocked).

- [ ] **Step 4: Run and verify**

Run: `go test -race ./internal/snapshot/...` and `make check`
Expected: PASS (the real-capture test PASSes or SKIPs as in Task 3).

- [ ] **Step 5: Mutation-check and commit**

Swap the precedence in `listenerFrom` so `Warming` is checked before `Draining`: the precedence table test must fail. Restore by copying the file back.

```bash
git add internal/snapshot
git commit -m "feat(snapshot): add listeners with state, address and filter chains" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Shared panes behind `resourceItem` (behaviour-preserving)

**Files:**
- Create: `internal/ui/item.go`, `internal/ui/cluster_item.go`
- Modify (rewrite): `internal/ui/listpane.go`, `internal/ui/detailpane.go`
- Modify: `internal/ui/model.go`, `internal/ui/text.go`, and the existing UI tests (mechanical renames only)

**Interfaces:**
- Consumes: `snapshot.Cluster`, `renderEndpoints`, `maxEndpointScroll` (unchanged).
- Produces:

```go
type rowCtx struct { Width int; Cursor, Unfocused bool; Hits []int }   // Hits: byte offsets into FilterText()
type resourceItem interface {
    Key() string
    FilterText() string
    Row(rowCtx) string
    Title() string
    OverviewName() string
    Overview(w, h, scroll int) string
    OverviewMaxScroll(w, h int) int
    Config() snapshot.ConfigInfo
}
func clusterItems([]snapshot.Cluster) []resourceItem
// listPane: SetItems([]resourceItem), Selected() (resourceItem, bool), SetEmptyText(string), SetNotice(string), SetWaiting(bool), SetSize, SetFocused, HandleKey, View, field filtering
// detailPane: SetItem(resourceItem, bool), SetEmptyText(string), SetTab, SetSize, SetFocused, HandleKey, View; tab constants tabOverview, tabConfig (tabEndpoints is renamed to tabOverview)
func wrapLines(s string, w, maxRows int) []string   // text.go: word-wrap, capped, last row ends with …
func cursorGlyph(rowCtx) string
func styledText(text string, hits []int, cursor bool) string
```

This task changes **no behaviour**. Every existing test and every golden must pass unchanged except for the mechanical test rewrites listed in Step 7; no assertion may be weakened and no golden regenerated.

- [ ] **Step 1: Baseline**

Run: `go test -count=1 ./internal/ui/ && go test ./internal/ui/ -run Golden`
Expected: PASS (this is the reference the refactor must keep).

- [ ] **Step 2: Write the new tests first**

`internal/ui/item_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/regeda/txenvoy/internal/snapshot"
)

func ansiStrip(s string) string { return ansi.Strip(s) }

func TestWrapLines(t *testing.T) {
	got := wrapLines("alpha beta gamma delta", 10, 4)
	if strings.Join(got, "|") != "alpha beta|gamma|delta" {
		t.Fatalf("%q", got)
	}
	capped := wrapLines(strings.Repeat("word ", 40), 12, 3)
	if len(capped) != 3 || !strings.HasSuffix(capped[2], "…") {
		t.Fatalf("a capped wrap ends with an ellipsis: %q", capped)
	}
	for _, l := range capped {
		if lipgloss.Width(l) > 12 {
			t.Fatalf("line wider than 12: %q", l)
		}
	}
	if got := wrapLines("", 10, 3); len(got) != 0 {
		t.Fatalf("empty text has no rows: %q", got)
	}
	if got := wrapLines("a\nb", 10, 3); strings.Join(got, "|") != "a|b" {
		t.Fatalf("existing newlines are kept: %q", got)
	}
	if got := wrapLines("x", 0, 3); len(got) != 0 {
		t.Fatalf("zero width: %q", got)
	}
}

func TestCursorGlyph(t *testing.T) {
	if cursorGlyph(rowCtx{}) != " " || cursorGlyph(rowCtx{Cursor: true}) != ">" || cursorGlyph(rowCtx{Cursor: true, Unfocused: true}) != "›" {
		t.Fatal("cursor glyphs")
	}
}

func TestStyledTextHighlightsByteOffsets(t *testing.T) {
	// "日本-api": hits 7..9 are byte offsets of "api"
	out := styledText("日本-api", []int{7, 8, 9}, false)
	if got := ansiStrip(out); got != "日本-api" {
		t.Fatalf("text must be unchanged: %q", got)
	}
}

func TestClusterItemSatisfiesTheInterface(t *testing.T) {
	c := cl("api", 1, 2)
	c.Config.YAML = "name: api\n"
	var it resourceItem = clusterItems([]snapshot.Cluster{c})[0]
	if it.Key() != "api" || it.FilterText() != "api" || it.Title() != "api" || it.OverviewName() != "Endpoints" {
		t.Fatalf("identity: %q %q %q %q", it.Key(), it.FilterText(), it.Title(), it.OverviewName())
	}
	row := ansiStrip(it.Row(rowCtx{Width: 40, Cursor: true}))
	if !strings.HasPrefix(row, ">") || !strings.Contains(row, "1/2") || lipgloss.Width(row) > 40 {
		t.Fatalf("row %q", row)
	}
	ov := it.Overview(100, 8, 0)
	if len(strings.Split(ov, "\n")) != 8 || !strings.Contains(ov, "ADDRESS") {
		t.Fatalf("overview:\n%s", ov)
	}
	if it.OverviewMaxScroll(100, 8) != maxEndpointScroll(c, 8) {
		t.Fatal("max scroll delegates to the endpoints table")
	}
	if text, _ := it.Config().Render(); text != "name: api\n" {
		t.Fatalf("config %q", text)
	}
}
```

Add to `internal/ui/listpane_test.go` a notice/empty-text test:

```go
func TestEmptyTextNoticeAndPrecedence(t *testing.T) {
	l := newListPane()
	l.SetSize(60, 12)
	l.SetEmptyText("no widgets")
	if !strings.Contains(l.View(), "no widgets") {
		t.Fatal(l.View())
	}
	l.SetNotice("widgets unavailable: boom")
	if v := l.View(); !strings.Contains(v, "widgets unavailable: boom") || strings.Contains(v, "no widgets") {
		t.Fatalf("a notice replaces the empty text:\n%s", v)
	}
	l.SetWaiting(true)
	if !strings.Contains(l.View(), "waiting for Envoy") {
		t.Fatal("waiting wins over a notice")
	}
	l.SetWaiting(false)
	l.SetNotice(strings.Repeat("long error words ", 30))
	for _, line := range strings.Split(l.View(), "\n") {
		if lipgloss.Width(line) > 60 {
			t.Fatalf("notice line wider than the pane: %q", line)
		}
	}
	if n := len(strings.Split(l.View(), "\n")); n != 12 {
		t.Fatalf("height %d, want 12", n)
	}
}
```

Run `go test ./internal/ui/ -run 'TestWrapLines|TestCursorGlyph|TestStyledText|TestClusterItemSatisfies|TestEmptyTextNotice'`. Expected: FAIL (undefined).

- [ ] **Step 3: Add `item.go`, the text helper and `cluster_item.go`**

`internal/ui/item.go`:

```go
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/snapshot"
)

// rowCtx is what a list row needs to draw itself.
type rowCtx struct {
	Width     int
	Cursor    bool
	Unfocused bool
	Hits      []int // fuzzy match offsets (bytes) into FilterText()
}

// resourceItem is one list entry plus everything its details need. The panes
// know nothing else about clusters or listeners.
type resourceItem interface {
	Key() string                      // stable id; keeps the selection across refreshes
	FilterText() string               // text the fuzzy filter matches
	Row(rowCtx) string                // one list row, never wider than Width
	Title() string                    // detail header
	OverviewName() string             // first detail tab: "Endpoints" or "Overview"
	Overview(w, h, scroll int) string // exactly h lines, none wider than w
	OverviewMaxScroll(w, h int) int
	Config() snapshot.ConfigInfo
}

// cursorGlyph is the one-cell row prefix.
func cursorGlyph(c rowCtx) string {
	switch {
	case !c.Cursor:
		return " "
	case c.Unfocused:
		return "›"
	default:
		return ">"
	}
}

// styledText renders text with the fuzzy hits (byte offsets into text)
// underlined and bold; on the cursor row the rest is bold.
func styledText(text string, hits []int, cursor bool) string {
	hit := map[int]bool{}
	for _, h := range hits {
		hit[h] = true
	}
	var b strings.Builder
	for bi, r := range text {
		s := string(r)
		switch {
		case hit[bi]:
			s = lipgloss.NewStyle().Bold(true).Underline(true).Render(s)
		case cursor:
			s = boldStyle.Render(s)
		}
		b.WriteString(s)
	}
	return b.String()
}
```

Append to `internal/ui/text.go`:

```go
// wrapLines word-wraps s to w cells and caps it at maxRows rows; when text is
// cut the last row ends with an ellipsis. Existing newlines are kept.
func wrapLines(s string, w, maxRows int) []string {
	if w <= 0 || maxRows <= 0 || s == "" {
		return nil
	}
	rows := strings.Split(ansi.Wrap(s, w, ""), "\n")
	if len(rows) > maxRows {
		rows = rows[:maxRows]
		last := strings.TrimRight(ansi.Truncate(rows[maxRows-1], max(w-1, 1), ""), " ")
		rows[maxRows-1] = last + "…"
	}
	return rows
}
```

`internal/ui/cluster_item.go` (the row body is the existing `listPane.row`, moved; widths come from `c.Width`):

```go
package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/snapshot"
)

const (
	typeW  = 12
	countW = 7
	fixedW = 3 + 1 + typeW + 1 + countW // prefix, gap, type, gap, count
)

type clusterItem struct{ c snapshot.Cluster }

func clusterItems(cs []snapshot.Cluster) []resourceItem {
	out := make([]resourceItem, len(cs))
	for i, c := range cs {
		out[i] = clusterItem{c}
	}
	return out
}

func (it clusterItem) Key() string          { return it.c.Name }
func (it clusterItem) FilterText() string   { return it.c.Name }
func (it clusterItem) Title() string        { return it.c.Name }
func (it clusterItem) OverviewName() string { return "Endpoints" }
func (it clusterItem) Overview(w, h, scroll int) string {
	return renderEndpoints(it.c, w, h, scroll)
}
func (it clusterItem) OverviewMaxScroll(_, h int) int { return maxEndpointScroll(it.c, h) }
func (it clusterItem) Config() snapshot.ConfigInfo   { return it.c.Config }

func (it clusterItem) Row(ctx rowCtx) string {
	c := it.c
	// In the narrowest pane the type column gives way so a row never wraps.
	tw := max(min(typeW, ctx.Width-(fixedW-typeW)-8), 1)
	nameW := max(ctx.Width-(fixedW-typeW+tw), 8)

	plain := trunc(c.Name, nameW)
	name := styledText(plain, ctx.Hits, ctx.Cursor)
	namePad := strings.Repeat(" ", max(nameW-lipgloss.Width(plain), 0))

	count := fmt.Sprintf("%d/%d", c.HealthyCount(), len(c.Hosts))
	countCell := strings.Repeat(" ", max(countW-lipgloss.Width(count), 0)) + count
	typeCell := pad(trunc(c.Type, tw), tw)

	prefix := cursorGlyph(ctx) + markerStyle(c.Health()).Render("●") + " "
	if c.Health() == snapshot.Empty {
		return prefix + name + namePad + " " + dimStyle.Render(typeCell+" "+countCell)
	}
	return prefix + name + namePad + " " + typeCell + " " + countCell
}
```

- [ ] **Step 4: Rewrite `listpane.go` onto items**

Replace the whole file with the generic pane. Everything behavioural is copied from the current file; the differences are marked.

```go
package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

type match struct {
	idx  int   // index into listPane.all
	hits []int // matched byte offsets in FilterText (as reported by fuzzy)
}

type listPane struct {
	all           []resourceItem
	visible       []match
	cursor        int
	selected      string // the selected item's Key
	query         string
	filtering     bool
	unfocused     bool // zero value = focused
	waiting       bool
	emptyText     string
	notice        string
	input         textinput.Model
	width, height int
	offset        int
}

func newListPane() listPane {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.CharLimit = 100
	return listPane{input: ti, emptyText: "no items"}
}

type itemTexts []resourceItem

func (n itemTexts) String(i int) string { return n[i].FilterText() }
func (n itemTexts) Len() int            { return len(n) }

func (l *listPane) SetSize(w, h int) {
	l.width, l.height = w, h
	l.input.Width = max(w-4, 1)
	l.clampOffset()
}

func (l *listPane) SetWaiting(v bool)       { l.waiting = v }
func (l *listPane) SetEmptyText(s string)   { l.emptyText = s }
func (l *listPane) SetNotice(s string)      { l.notice = s }

// SetFocused tells the pane whether it owns the keyboard; an unfocused pane
// draws its cursor as › instead of >.
func (l *listPane) SetFocused(f bool) { l.unfocused = !f }

func (l *listPane) SetItems(items []resourceItem) {
	l.all = items
	l.refilter()
}

func (l *listPane) Selected() (resourceItem, bool) {
	if len(l.visible) == 0 {
		return nil, false
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
		for _, m := range fuzzy.FindFrom(l.query, itemTexts(l.all)) {
			vis = append(vis, match{idx: m.Index, hits: m.MatchedIndexes})
		}
	}
	l.visible = vis
	l.restoreCursor()
}

// restoreCursor keeps the selection on the same Key; if it is gone it falls
// to the nearest row.
func (l *listPane) restoreCursor() {
	for i, m := range l.visible {
		if l.all[m.idx].Key() == l.selected {
			l.cursor = i
			l.clampOffset()
			return
		}
	}
	l.cursor = min(l.cursor, len(l.visible)-1)
	l.cursor = max(l.cursor, 0)
	l.selected = ""
	if len(l.visible) > 0 {
		l.selected = l.all[l.visible[l.cursor].idx].Key()
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
	l.offset = min(l.offset, max(len(l.visible)-l.rows(), 0))
	l.offset = max(l.offset, 0)
}

func (l *listPane) move(d int) {
	if len(l.visible) == 0 {
		return
	}
	l.cursor = max(0, min(l.cursor+d, len(l.visible)-1))
	l.selected = l.all[l.visible[l.cursor].idx].Key()
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
	return l.all[m.idx].Row(rowCtx{Width: l.width, Cursor: i == l.cursor, Unfocused: l.unfocused, Hits: m.hits})
}

func (l listPane) View() string {
	lines := []string{l.filterLine()}
	if len(l.visible) == 0 {
		switch {
		case l.waiting:
			lines = append(lines, dimStyle.Render("waiting for Envoy…"))
		case l.notice != "":
			for _, row := range wrapLines(l.notice, max(l.width, 1), max(l.rows(), 1)) {
				lines = append(lines, warnStyle.Render(row))
			}
		case l.query != "":
			lines = append(lines, dimStyle.Render("no matches"))
		default:
			lines = append(lines, dimStyle.Render(l.emptyText))
		}
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

Behavioural note: the old `View` used `no clusters` by default; `Model` now calls `SetEmptyText("no clusters")` (Step 6), so the text is unchanged. The cluster highlight and padding now live in `clusterItem.Row` (hits are byte offsets into `FilterText()`, which is the name).

- [ ] **Step 5: Rewrite `detailpane.go` onto items**

Replace the whole file:

```go
package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tab int

const (
	tabOverview tab = iota
	tabConfig
)

type detailPane struct {
	tab           tab
	item          resourceItem
	has           bool
	emptyText     string
	width, height int
	scroll        int // overview tab
	vp            viewport.Model
	color         bool
	focused       bool

	cfgKey, cfgText, cfgOut string
	highlights              int
}

func newDetailPane(color bool) detailPane {
	return detailPane{color: color, vp: viewport.New(0, 0), emptyText: "nothing selected"}
}

func (d *detailPane) SetEmptyText(s string) { d.emptyText = s }

// Layout: 1 tab line, then a body of height-1 rows. The Config body is 1 header
// line plus the viewport.
func (d *detailPane) SetSize(w, h int) {
	d.width, d.height = w, h
	d.vp.Width = max(w, 1)
	d.vp.Height = max(h-2, 1)
	d.applyContent() // re-clip to the new width; never re-runs chroma
}

func (d *detailPane) SetTab(t tab) { d.tab = t }

// SetFocused marks the pane as owning the keyboard; its tab line then starts
// with a ▌ marker (a blank of the same width otherwise, so nothing shifts).
func (d *detailPane) SetFocused(f bool) { d.focused = f }

func (d *detailPane) SetItem(it resourceItem, ok bool) {
	if !ok {
		d.has, d.item = false, nil
		d.cfgKey = "" // a reappearing item is treated as new
		d.vp.GotoTop()
		return
	}
	if !d.has || d.item.Key() != it.Key() {
		d.scroll = 0
	}
	d.item, d.has = it, true
	d.scroll = min(d.scroll, d.item.OverviewMaxScroll(d.width, d.height-1))
	d.syncConfig()
}

func (d *detailPane) syncConfig() {
	text, _ := d.item.Config().Render()
	key := d.item.Key()
	if d.cfgKey != key || d.cfgText != text {
		keyChanged := d.cfgKey != key
		d.cfgOut = highlightYAML(text, d.color)
		d.cfgKey, d.cfgText = key, text
		d.highlights++
		d.applyContent()
		if keyChanged {
			d.vp.GotoTop()
		}
		return
	}
	d.applyContent()
}

// numberLines prefixes every line with a dark-grey, right-aligned line number
// and a gutter bar (no space before the bar; the column is as wide as the
// largest number). The numbers follow the YAML lines (one rendered row per line),
// so they stay correct while the viewport scrolls. Empty content gets none.
func numberLines(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	digits := len(strconv.Itoa(len(lines)))
	for i, l := range lines {
		lines[i] = lineNumStyle.Render(fmt.Sprintf("%*d│", digits, i+1)) + " " + l
	}
	return strings.Join(lines, "\n")
}

// clipLines cuts every line to w cells so each logical line is one rendered row.
func clipLines(s string, w int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = trunc(l, w)
	}
	return strings.Join(lines, "\n")
}

// applyContent feeds the viewport the (cached) highlighted YAML or the problem,
// clipped to the current width.
func (d *detailPane) applyContent() {
	if !d.has {
		return
	}
	w := max(d.width, 1)
	if _, problem := d.item.Config().Render(); problem != "" {
		d.vp.SetContent(clipLines(dimStyle.Render(problem), w))
		return
	}
	d.vp.SetContent(clipLines(numberLines(d.cfgOut), w))
}

func (d *detailPane) HandleKey(k tea.KeyMsg) {
	if !d.has {
		return
	}
	if d.tab == tabConfig {
		d.vp, _ = d.vp.Update(k)
		return
	}
	maxS := d.item.OverviewMaxScroll(d.width, d.height-1)
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
	marker := "  "
	if d.focused {
		marker = logoStyle.Render("▌") + " "
	}
	return marker + boldStyle.Render(d.item.Title()) + "  ·  " +
		label(d.item.OverviewName(), d.tab == tabOverview) + " " + dimStyle.Render("|") + " " + label("Config", d.tab == tabConfig)
}

func (d detailPane) configHeader() string {
	c := d.item.Config()
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
	w, h := max(d.width, 1), max(d.height, 1)
	var body string
	switch {
	case !d.has:
		body = dimStyle.Render(d.emptyText)
	case d.tab == tabConfig:
		body = trunc(d.tabLine(), w) + "\n" + trunc(d.configHeader(), w) + "\n" + d.vp.View()
	default:
		body = trunc(d.tabLine(), w) + "\n" + d.item.Overview(d.width, d.height-1, d.scroll)
	}
	// No Width(): it would wrap. Every line is already clipped to w.
	return lipgloss.NewStyle().MaxWidth(w).Height(h).MaxHeight(h).Render(body)
}
```

- [ ] **Step 6: Point `model.go` at the new APIs**

In `internal/ui/model.go` (no structural change yet):
- In `New`: after `m.list.SetWaiting(true)` add `m.list.SetEmptyText("no clusters")` and `m.detail.SetEmptyText("no cluster selected")`.
- `syncDetail`: `it, ok := m.list.Selected(); m.detail.SetItem(it, ok)`.
- In the `snapshotMsg` branch: `m.list.SetItems(clusterItems(msg.snap.Clusters))`.
- In `handleKey`: `case "e": m.detail.SetTab(tabOverview)`.

- [ ] **Step 7: Mechanical test rewrites (the only permitted test edits)**

- `l.SetClusters(x)` → `l.SetItems(clusterItems(x))`; in `newList` likewise.
- `c, ok := l.Selected()` then `c.Name` → `it, ok := l.Selected()` then `it.Key()`; `m.list.Selected()` the same.
- `d.SetCluster(c, true)` → `d.SetItem(clusterItem{c}, true)`; `d.SetCluster(snapshot.Cluster{}, false)` → `d.SetItem(nil, false)`.
- `m.detail.cluster.Name` → `m.detail.item.Key()`; `d.cluster` (tests that read the cluster) → `d.item`.
- `tabEndpoints` → `tabOverview` everywhere (`grep -rn tabEndpoints internal`).
- `TestNoClusterSelected`-style tests need `d.SetEmptyText("no cluster selected")` before asserting the text, since the pane default is now generic. (`New` sets it for the model, so model tests and goldens are unaffected.)
- `highlight` caching tests that read `d.cfgYAML` use `d.cfgText`.

Do not touch assertions, expected strings or goldens.

- [ ] **Step 8: Verify behaviour is preserved**

Run: `go test -race -count=1 ./internal/ui/` and `go test ./internal/ui/ -run Golden` (no `-update`) and `make check`
Expected: all PASS, no golden modified (`git status --short internal/ui/testdata` is empty). If a golden fails the refactor changed behaviour: fix the code, never the golden.

- [ ] **Step 9: Mutation-check and commit**

Change `cursorGlyph` to return `>` for the unfocused case: `TestCursorGlyph` and the focus tests must fail. Restore by copying the file back.

```bash
git add internal/ui
git commit -m "refactor(ui): drive the panes through a resourceItem interface" -m "Behaviour-preserving: no golden changes." -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Listener item: row and Overview

**Files:**
- Create: `internal/ui/listener_item.go`, `internal/ui/listener_item_test.go`

**Interfaces:**
- Consumes: `resourceItem`, `rowCtx`, `styledText`, `cursorGlyph`, `wrapLines`, `trunc`, `pad`, `truncEllipsis`, styles (Task 6), `snapshot.Listener` (Task 5).
- Produces: `listenerItems([]snapshot.Listener) []resourceItem` and `listenerItem` implementing `resourceItem`; `OverviewName()` is `"Overview"`; `Key()` is the listener name; `FilterText()` is `name + " " + address`.

Row: `<cursor><glyph> <name padded><address>`; glyph `●` green (active/static), `◐` yellow (warming/draining), `✗` red (error). The address width is `min(width of address, 21, avail-9)` (never negative), the name takes the rest.

Overview lines (all clipped with `truncEllipsis`, exactly `h` lines): sticky rows `Bind address`, `State`, `version_info`, `last_updated`, then (error only) the error block, then a blank row and `Filter chains (N)`; then two scrolling lines per chain. Labels are padded to 14 cells (so `Bind address` and `version_info` are followed by two spaces).

- [ ] **Step 1: Write the failing tests**

`internal/ui/listener_item_test.go`:

```go
package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/regeda/txenvoy/internal/snapshot"
)

func lst(name, addr string, state snapshot.ListenerStatus) snapshot.Listener {
	return snapshot.Listener{
		Name: name, Address: addr, State: state, VersionInfo: "42",
		LastUpdated: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC),
		Chains: []snapshot.FilterChain{
			{Label: "#1", Match: []string{"sni=a.com,b.com", "port=443", "transport=tls"}, Filters: []string{"http_connection_manager"}, TLS: true},
			{Label: "#2", Filters: []string{"tcp_proxy"}},
			{Label: "default", Filters: []string{"tcp_proxy"}},
		},
	}
}

func item0(l snapshot.Listener) resourceItem { return listenerItems([]snapshot.Listener{l})[0] }

func TestListenerItemIdentity(t *testing.T) {
	it := item0(lst("0.0.0.0_15001", "0.0.0.0:15001", snapshot.ListenerActive))
	if it.Key() != "0.0.0.0_15001" || it.Title() != "0.0.0.0_15001" || it.OverviewName() != "Overview" ||
		it.FilterText() != "0.0.0.0_15001 0.0.0.0:15001" {
		t.Fatalf("%q %q %q %q", it.Key(), it.Title(), it.OverviewName(), it.FilterText())
	}
}

func TestListenerRowGlyphsAndLayout(t *testing.T) {
	cases := []struct {
		state snapshot.ListenerStatus
		glyph string
	}{
		{snapshot.ListenerActive, "●"}, {snapshot.ListenerWarming, "◐"},
		{snapshot.ListenerDraining, "◐"}, {snapshot.ListenerError, "✗"},
	}
	for _, c := range cases {
		row := ansiStrip(item0(lst("api", "0.0.0.0:8080", c.state)).Row(rowCtx{Width: 36}))
		if !strings.HasPrefix(row, " "+c.glyph+" api") || !strings.HasSuffix(row, "0.0.0.0:8080") || lipgloss.Width(row) != 36 {
			t.Fatalf("%v: %q (width %d)", c.state, row, lipgloss.Width(row))
		}
	}
	if row := ansiStrip(item0(lst("api", "0.0.0.0:8080", snapshot.ListenerActive)).Row(rowCtx{Width: 36, Cursor: true})); !strings.HasPrefix(row, ">") {
		t.Fatalf("cursor: %q", row)
	}
	if row := ansiStrip(item0(lst("api", "0.0.0.0:8080", snapshot.ListenerActive)).Row(rowCtx{Width: 36, Cursor: true, Unfocused: true})); !strings.HasPrefix(row, "›") {
		t.Fatalf("unfocused cursor: %q", row)
	}
	static := lst("agent", "unix:/var/run/agent.sock", snapshot.ListenerActive)
	static.Static = true
	if row := ansiStrip(item0(static).Row(rowCtx{Width: 36})); !strings.HasPrefix(row, " ● agent") {
		t.Fatalf("static listeners show the active glyph: %q", row)
	}
}

func TestListenerStateIsNotColourOnly(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	a := item0(lst("x", "1.1.1.1:1", snapshot.ListenerActive)).Row(rowCtx{Width: 30})
	e := item0(lst("x", "1.1.1.1:1", snapshot.ListenerError)).Row(rowCtx{Width: 30})
	if a == e {
		t.Fatal("with no colours at all, an error row must still differ from an active row")
	}
}

func TestListenerRowWidthNeverExceeded(t *testing.T) {
	long := lst(strings.Repeat("very-long-listener-name-", 5), "[2001:db8:aaaa:bbbb:cccc:dddd:eeee:ffff]:443", snapshot.ListenerActive)
	wide := lst("日本語のリスナー名前日本語のリスナー名前", "0.0.0.0:80", snapshot.ListenerWarming)
	for _, w := range []int{1, 5, 12, 28, 36, 60} {
		for _, l := range []snapshot.Listener{long, wide, lst("a", "", snapshot.ListenerActive)} {
			if got := lipgloss.Width(item0(l).Row(rowCtx{Width: w, Cursor: true})); got > w {
				t.Fatalf("width %d: row is %d wide for %q", w, got, l.Name)
			}
		}
	}
	// the name keeps at least 8 cells when the pane allows it
	row := ansiStrip(item0(long).Row(rowCtx{Width: 36}))
	if !strings.Contains(row, "very-lon") || lipgloss.Width(row) != 36 {
		t.Fatalf("name starved by the address: %q", row)
	}
}

func TestListenerRowHighlightsHitsInNameAndAddress(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	it := item0(lst("alpha", "0.0.0.0:9090", snapshot.ListenerActive))
	text := it.FilterText() // "alpha 0.0.0.0:9090"
	hits := []int{strings.Index(text, "pha"), strings.Index(text, "pha") + 1, strings.Index(text, "pha") + 2, strings.Index(text, "9090")}
	row := it.Row(rowCtx{Width: 36, Hits: hits})
	if !strings.Contains(row, "\x1b[1;4m") && !strings.Contains(row, "\x1b[4;1m") {
		t.Fatalf("expected underlined hits: %q", row)
	}
	if ansiStrip(row) != ansiStrip(it.Row(rowCtx{Width: 36})) {
		t.Fatal("highlighting must not change the visible text")
	}
}

func overviewRows(it resourceItem, w, h, scroll int) []string {
	return strings.Split(it.Overview(w, h, scroll), "\n")
}

// kv is a property row as the Overview draws it: the label padded to 14 cells.
func kv(label, value string) string { return fmt.Sprintf("%-14s%s", label, value) }

func TestListenerOverviewLayout(t *testing.T) {
	rows := overviewRows(item0(lst("x", "0.0.0.0:15001", snapshot.ListenerActive)), 70, 14, 0)
	want := []string{
		kv("Bind address", "0.0.0.0:15001"),
		kv("State", "active"),
		kv("version_info", "42"),
		kv("last_updated", "2026-10-14 10:00:00Z"),
		"",
		"Filter chains (3)",
	}
	if !strings.HasPrefix(want[0], "Bind address  0.0.0.0") {
		t.Fatal("the label column is 14 cells: two spaces after the longest label")
	}
	for i, w := range want {
		if got := strings.TrimRight(ansiStrip(rows[i]), " "); got != w {
			t.Fatalf("row %d = %q, want %q", i, got, w)
		}
	}
	chains := ansiStrip(strings.Join(rows[6:12], "\n"))
	for _, frag := range []string{
		fmt.Sprintf(" %-7s  %s", "#1", "sni=a.com,b.com · port=443 · transport=tls"),
		"http_connection_manager · TLS",
		fmt.Sprintf(" %-7s  %s", "#2", "any"),
		fmt.Sprintf(" %-7s  %s", "default", "any"),
	} {
		if !strings.Contains(chains, frag) {
			t.Fatalf("missing %q in:\n%s", frag, chains)
		}
	}
	if len(rows) != 14 {
		t.Fatalf("exactly h rows, got %d", len(rows))
	}
}

func TestListenerOverviewStateAndBlanks(t *testing.T) {
	static := lst("s", "", snapshot.ListenerActive)
	static.Static, static.VersionInfo, static.LastUpdated, static.Chains = true, "", time.Time{}, nil
	rows := overviewRows(item0(static), 60, 10, 0)
	text := ansiStrip(strings.Join(rows, "\n"))
	for _, frag := range []string{kv("Bind address", "-"), kv("State", "static"), kv("version_info", "-"), kv("last_updated", "-"), "Filter chains (0)"} {
		if !strings.Contains(text, frag) {
			t.Fatalf("missing %q:\n%s", frag, text)
		}
	}
}

func TestListenerOverviewErrorBlock(t *testing.T) {
	l := lst("bad", "0.0.0.0:9443", snapshot.ListenerError)
	l.ErrorDetails = "error adding listener: unknown filter 'envoy.filters.network.nope' while processing LDS update version 9, then a lot more words to force wrapping across rows and beyond the cap of four lines for sure"
	l.ServingOld = true
	rows := overviewRows(item0(l), 50, 20, 0)
	plain := make([]string, len(rows))
	for i, r := range rows {
		plain[i] = strings.TrimRight(ansiStrip(r), " ")
	}
	text := strings.Join(plain, "\n")
	if !strings.Contains(text, kv("State", "error")) || !strings.Contains(text, kv("Error", "error adding listener")) {
		t.Fatalf("error block missing:\n%s", text)
	}

	// the error text occupies the rows from "Error" up to the "still serving" note
	first, note := -1, -1
	for i, r := range plain {
		if strings.HasPrefix(r, "Error ") && first < 0 {
			first = i
		}
		if strings.Contains(r, "still serving the previous configuration") {
			note = i
		}
	}
	if first < 0 || note < 0 {
		t.Fatalf("error start %d / note %d:\n%s", first, note, text)
	}
	if n := note - first; n > 4 {
		t.Fatalf("the error text is capped at 4 rows, got %d:\n%s", n, text)
	}
	if !strings.HasSuffix(plain[note-1], "…") {
		t.Fatalf("a capped error ends with an ellipsis:\n%s", text)
	}
	for _, r := range plain[first+1 : note] {
		if !strings.HasPrefix(r, strings.Repeat(" ", 14)) {
			t.Fatalf("continuation rows are indented under the value: %q", r)
		}
	}

	l.ServingOld = false
	if strings.Contains(ansiStrip(strings.Join(overviewRows(item0(l), 50, 20, 0), "\n")), "still serving") {
		t.Fatal("the note appears only when an older version is still serving")
	}
}

func TestListenerOverviewNeverExceedsBoundsAndKeepsStickyRows(t *testing.T) {
	l := lst("x", "0.0.0.0:1", snapshot.ListenerError)
	l.ErrorDetails = strings.Repeat("boom ", 40)
	for i := 0; i < 30; i++ {
		l.Chains = append(l.Chains, snapshot.FilterChain{Label: fmt.Sprintf("chain-%d", i), Match: []string{strings.Repeat("sni=long.example.com,", 10)}, Filters: []string{"tcp_proxy"}})
	}
	for _, w := range []int{20, 40, 80} {
		for _, h := range []int{1, 2, 3, 6, 10, 25} {
			for _, scroll := range []int{0, 3, 999} {
				rows := overviewRows(item0(l), w, h, scroll)
				if len(rows) != h {
					t.Fatalf("w=%d h=%d scroll=%d: %d rows", w, h, scroll, len(rows))
				}
				for _, r := range rows {
					if lipgloss.Width(r) > w {
						t.Fatalf("w=%d h=%d: row %d wide: %q", w, h, lipgloss.Width(r), r)
					}
				}
			}
		}
	}
	it := item0(l)
	// sticky rows stay while the chain lines scroll
	top := ansiStrip(strings.Join(overviewRows(it, 80, 14, 0), "\n"))
	mid := ansiStrip(strings.Join(overviewRows(it, 80, 14, 10), "\n"))
	for _, frag := range []string{"Bind address", "State", "Filter chains (30)"} {
		if !strings.Contains(top, frag) || !strings.Contains(mid, frag) {
			t.Fatalf("sticky %q must stay visible while scrolling", frag)
		}
	}
	if !strings.Contains(top, "chain-0") || strings.Contains(mid, "chain-0") || !strings.Contains(mid, "chain-5") {
		t.Fatalf("chain lines must scroll:\n%s\n---\n%s", top, mid)
	}
}

func TestListenerOverviewMaxScrollMatchesRendering(t *testing.T) {
	l := lst("x", "0.0.0.0:1", snapshot.ListenerActive)
	for i := 0; i < 20; i++ {
		l.Chains = append(l.Chains, snapshot.FilterChain{Label: fmt.Sprintf("chain-%02d", i), Filters: []string{"tcp_proxy"}})
	}
	it := item0(l)
	max := it.OverviewMaxScroll(60, 12)
	if max <= 0 {
		t.Fatal("a long chain list must scroll")
	}
	last := ansiStrip(strings.Join(overviewRows(it, 60, 12, max), "\n"))
	if !strings.Contains(last, "chain-19") {
		t.Fatalf("scrolling to the max must reveal the last chain:\n%s", last)
	}
	beyond := ansiStrip(strings.Join(overviewRows(it, 60, 12, max+50), "\n"))
	if beyond != last {
		t.Fatal("an oversized scroll clamps to the max")
	}
	short := item0(lst("y", "1.1.1.1:1", snapshot.ListenerActive))
	if short.OverviewMaxScroll(60, 30) != 0 {
		t.Fatal("content that fits does not scroll")
	}
	if it.OverviewMaxScroll(60, 1) != 0 || it.OverviewMaxScroll(60, 0) != 0 {
		t.Fatal("no room for the chain area means no scroll")
	}
}

func TestListenerOverviewLongLinesAreClippedWithEllipsis(t *testing.T) {
	l := lst("x", "0.0.0.0:1", snapshot.ListenerActive)
	l.Chains = []snapshot.FilterChain{{Label: "c", Match: []string{"sni=" + strings.Repeat("host.example.com,", 20)}, Filters: []string{"tcp_proxy"}}}
	rows := overviewRows(item0(l), 40, 12, 0)
	found := false
	for _, r := range rows {
		if strings.Contains(r, "sni=") {
			found = true
			if !strings.HasSuffix(ansiStrip(r), "…") || lipgloss.Width(r) > 40 {
				t.Fatalf("a long match line ends with … within the width: %q", r)
			}
		}
	}
	if !found {
		t.Fatal("match line not found")
	}
}

func TestListenerItemConfig(t *testing.T) {
	l := lst("x", "0.0.0.0:1", snapshot.ListenerActive)
	l.Config = snapshot.ConfigInfo{YAML: "name: x\n", VersionInfo: "42"}
	if text, _ := item0(l).Config().Render(); text != "name: x\n" {
		t.Fatalf("%q", text)
	}
}

func TestListenerItemsEmpty(t *testing.T) {
	if got := listenerItems(nil); len(got) != 0 {
		t.Fatal("no listeners, no items")
	}
}
```

Run: `go test ./internal/ui/ -run Listener`. Expected: FAIL (undefined).

- [ ] **Step 2: Implement**

`internal/ui/listener_item.go`:

```go
package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/snapshot"
)

const (
	overviewLabelW = 14 // "version_info" (12) + 2 spaces
	addrMaxW       = 21 // widest address cell in a row
	nameMinW       = 8
	errorMaxRows   = 4
)

type listenerItem struct{ l snapshot.Listener }

func listenerItems(ls []snapshot.Listener) []resourceItem {
	out := make([]resourceItem, len(ls))
	for i, l := range ls {
		out[i] = listenerItem{l}
	}
	return out
}

func (it listenerItem) Key() string                  { return it.l.Name }
func (it listenerItem) FilterText() string           { return it.l.Name + " " + it.l.Address }
func (it listenerItem) Title() string                { return it.l.Name }
func (it listenerItem) OverviewName() string         { return "Overview" }
func (it listenerItem) Config() snapshot.ConfigInfo { return it.l.Config }

// stateGlyph carries the state without relying on colour.
func stateGlyph(l snapshot.Listener) (string, lipgloss.Style) {
	switch l.State {
	case snapshot.ListenerError:
		return "✗", badStyle
	case snapshot.ListenerWarming, snapshot.ListenerDraining:
		return "◐", warnStyle
	default:
		return "●", okStyle
	}
}

func (it listenerItem) Row(ctx rowCtx) string {
	l := it.l
	glyph, style := stateGlyph(l)
	avail := max(ctx.Width-3, 0) // prefix, glyph, space
	addrW := max(min(lipgloss.Width(l.Address), addrMaxW, avail-nameMinW-1), 0)
	nameW := avail - addrW
	if addrW > 0 {
		nameW-- // gap
	}
	nameW = max(nameW, 0)

	// FilterText is "name address": hits past the name belong to the address.
	var nameHits, addrHits []int
	for _, h := range ctx.Hits {
		switch {
		case h < len(l.Name):
			nameHits = append(nameHits, h)
		case h > len(l.Name):
			addrHits = append(addrHits, h-len(l.Name)-1)
		}
	}
	plainName := trunc(l.Name, nameW)
	name := styledText(plainName, nameHits, ctx.Cursor) + strings.Repeat(" ", max(nameW-lipgloss.Width(plainName), 0))

	line := cursorGlyph(ctx) + style.Render(glyph) + " " + name
	if addrW > 0 {
		line += " " + styledText(trunc(l.Address, addrW), addrHits, false)
	}
	return trunc(line, ctx.Width) // tiny panes: never wider than the pane
}

func prop(label, value string) string {
	return dimStyle.Render(pad(label, overviewLabelW)) + value
}

func orDashText(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func stateStyle(l snapshot.Listener) lipgloss.Style {
	_, s := stateGlyph(l)
	return s
}

// sticky returns the rows that never scroll: the four properties, the error
// block (error state only) and the chain heading.
func (it listenerItem) sticky(w int) []string {
	l := it.l
	lines := []string{
		prop("Bind address", orDashText(l.Address)),
		prop("State", stateStyle(l).Render(l.StateText())),
		prop("version_info", orDashText(l.VersionInfo)),
	}
	updated := "-"
	if !l.LastUpdated.IsZero() {
		updated = l.LastUpdated.UTC().Format("2006-01-02 15:04:05Z")
	}
	lines = append(lines, prop("last_updated", updated))

	if l.State == snapshot.ListenerError {
		rows := wrapLines(sanitizeBannerText(l.ErrorDetails), max(w-overviewLabelW, 1), errorMaxRows)
		for i, r := range rows {
			label := ""
			if i == 0 {
				label = "Error"
			}
			lines = append(lines, dimStyle.Render(pad(label, overviewLabelW))+badStyle.Render(r))
		}
		if l.ServingOld {
			lines = append(lines, dimStyle.Render(pad("", overviewLabelW)+"still serving the previous configuration"))
		}
	}
	return append(lines, "", boldStyle.Render("Filter chains ("+strconv.Itoa(len(l.Chains))+")"))
}

func (it listenerItem) chainLines() []string {
	var out []string
	labelW := 0
	for _, c := range it.l.Chains {
		labelW = max(labelW, lipgloss.Width(c.Label))
	}
	labelW = min(labelW, overviewLabelW)
	for _, c := range it.l.Chains {
		match := "any"
		if len(c.Match) > 0 {
			match = strings.Join(c.Match, " · ")
		}
		filters := strings.Join(c.Filters, ", ")
		if c.TLS {
			if filters != "" {
				filters += " · "
			}
			filters += "TLS"
		}
		out = append(out,
			" "+pad(trunc(c.Label, labelW), labelW)+"  "+match,
			" "+strings.Repeat(" ", labelW)+"  "+dimStyle.Render(filters))
	}
	return out
}

func clipRow(s string, w int) string { return truncEllipsis(s, w) }

func (it listenerItem) OverviewMaxScroll(w, h int) int {
	rows := h - len(it.sticky(w))
	if rows <= 0 {
		return 0
	}
	return max(len(it.chainLines())-rows, 0)
}

func (it listenerItem) Overview(w, h, scroll int) string {
	if h <= 0 || w <= 0 {
		return ""
	}
	sticky := it.sticky(w)
	var lines []string
	if h <= len(sticky) {
		lines = sticky[:h]
	} else {
		lines = append(lines, sticky...)
		chains := it.chainLines()
		rows := h - len(sticky)
		start := max(0, min(scroll, max(len(chains)-rows, 0)))
		lines = append(lines, chains[start:min(start+rows, len(chains))]...)
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = clipRow(l, w)
	}
	return strings.Join(lines, "\n")
}
```

`truncEllipsis` already exists in `endpoints.go`, and `sanitizeBannerText` in `chrome.go` makes error text safe (no tabs, no control characters) before it is wrapped.

- [ ] **Step 3: Run and verify**

Run: `go test -race ./internal/ui/ -run Listener` then `make check`
Expected: PASS. The error-row counting loop in `TestListenerOverviewErrorBlock` is deliberately loose; if it miscounts, tighten the test to count the rows between the `Error` row and the `still serving` / blank row instead, never loosen the cap assertion.

- [ ] **Step 4: Mutation-check and commit**

Make `wrapLines` uncapped in the error block (pass `1000` for the cap): `TestListenerOverviewErrorBlock` must fail. Restore by copy.

```bash
git add internal/ui
git commit -m "feat(ui): add the listener list row and Overview" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Views, Tab / Enter / Esc, header and status

**Files:**
- Create: `internal/ui/view.go`, `internal/ui/views_test.go`
- Modify: `internal/ui/model.go`, `internal/ui/chrome.go`, `internal/ui/chrome_test.go`, existing model tests (mechanical renames)

**Interfaces:**
- Consumes: everything above.
- Produces:

```go
type resourceKind int // kindClusters, kindListeners
type view struct { list listPane; detail detailPane }
type resource struct {
    label       string
    items       func(snapshot.Snapshot) []resourceItem
    count       func(snapshot.Snapshot) int
    summary     func(snapshot.Snapshot) string // status-bar left text
    notice      func(snapshot.Snapshot) string // inline list message, "" when none
    warn        func(snapshot.Snapshot) bool   // tab label shows (!)
    emptyList, emptyDetail string
}
var resources = [...]resource{ /* clusters, listeners */ }
type headerTab struct { Label string; Active, Warn bool }
func renderHeader(width int, compact bool, conn1, conn2 string, tabs []headerTab) string
// Model: views [2]view; active resourceKind; func (m Model) cur() *view
```

- [ ] **Step 1: Write the failing tests**

`internal/ui/views_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"github.com/regeda/txenvoy/internal/snapshot"
)

func lsnap(ls ...snapshot.Listener) snapshot.Snapshot {
	return snapshot.Snapshot{
		Clusters: []snapshot.Cluster{cl("alpha", 1, 2), cl("beta", 1, 1)},
		Listeners: ls, Version: "1.31.0", FetchedAt: fixedNow,
	}
}

// onListeners is listenersModel with the Listeners tab showing.
func onListeners() Model { return send(listenersModel(), key("tab")) }

func listenersModel() Model {
	m := sized(New(testOpts(nil)), 120, 30)
	return send(m, snapshotMsg{snap: lsnap(
		lst("l-active", "0.0.0.0:8080", snapshot.ListenerActive),
		lst("l-warming", "0.0.0.0:9090", snapshot.ListenerWarming),
		lst("l-error", "0.0.0.0:9443", snapshot.ListenerError),
	)})
}

func activeName(m Model) string {
	it, _ := m.cur().list.Selected()
	if it == nil {
		return ""
	}
	return it.Key()
}

func TestTabSwitchesResourcesAndShiftTabGoesBack(t *testing.T) {
	m := listenersModel()
	if m.active != kindClusters || activeName(m) != "alpha" {
		t.Fatal("starts on Clusters")
	}
	m = send(m, key("tab"))
	if m.active != kindListeners || activeName(m) != "l-active" {
		t.Fatalf("tab -> Listeners, got %v %q", m.active, activeName(m))
	}
	m = send(m, key("tab"))
	if m.active != kindClusters {
		t.Fatal("tab wraps back to Clusters")
	}
	m = send(m, key("shift+tab"))
	if m.active != kindListeners {
		t.Fatal("shift+tab goes backwards (and wraps)")
	}
	m = send(m, key("shift+tab"))
	if m.active != kindClusters {
		t.Fatal("shift+tab back to Clusters")
	}
}

func TestEachTabKeepsItsOwnState(t *testing.T) {
	m := listenersModel()
	m = send(m, key("j")) // clusters: beta
	m = send(m, key("tab"))
	m = send(m, key("j")) // listeners: l-warming
	m = send(m, key("c")) // listeners details: Config
	m = typeText(send(m, key("/")), "err")
	m = send(m, key("enter")) // keep the filter, leave filter mode
	if activeName(m) != "l-error" {
		t.Fatalf("filter selects l-error, got %q", activeName(m))
	}
	m = send(m, key("tab"))
	if activeName(m) != "beta" || m.cur().list.query != "" {
		t.Fatalf("clusters keep their selection and have no filter: %q %q", activeName(m), m.cur().list.query)
	}
	m = send(m, key("tab"))
	if activeName(m) != "l-error" || m.cur().list.query != "err" || m.cur().detail.tab != tabConfig {
		t.Fatalf("listeners keep selection, filter and detail tab: %q %q %v", activeName(m), m.cur().list.query, m.cur().detail.tab)
	}
}

func TestTabIsIgnoredWhileTypingAFilter(t *testing.T) {
	m := send(listenersModel(), key("/"))
	m = send(m, key("tab"))
	m = send(m, key("shift+tab"))
	if m.active != kindClusters || !m.cur().list.filtering {
		t.Fatalf("tab must be ignored in filter mode (active=%v filtering=%v)", m.active, m.cur().list.filtering)
	}
	m = send(m, key("esc"))
	m = send(m, key("tab"))
	if m.active != kindListeners {
		t.Fatal("tab works again after the filter is closed")
	}
}

func TestTabSwitchLandsOnTheList(t *testing.T) {
	m := send(listenersModel(), key("enter"))
	if m.focus != focusDetail {
		t.Fatal("precondition: details focused")
	}
	m = send(m, key("tab"))
	if m.focus != focusList || m.active != kindListeners {
		t.Fatalf("a switch lands on the list of the target tab (focus=%v active=%v)", m.focus, m.active)
	}
}

func TestEnterEscFocusFlow(t *testing.T) {
	m := onListeners()
	m = send(m, key("enter"))
	if m.focus != focusDetail || !strings.Contains(m.View(), "focus: detail") {
		t.Fatal("enter focuses the details")
	}
	m = send(m, key("esc"))
	if m.focus != focusList || strings.Contains(m.View(), "focus: detail") {
		t.Fatal("esc returns to the list")
	}
	// filter flow: enter keeps the filter, a second enter opens the details
	m = typeText(send(m, key("/")), "warm")
	m = send(m, key("enter"))
	if m.focus != focusList || m.cur().list.filtering || len(m.cur().list.visible) != 1 {
		t.Fatalf("enter in filter mode keeps the filter and stays on the list (focus=%v)", m.focus)
	}
	m = send(m, key("enter"))
	if m.focus != focusDetail {
		t.Fatal("the second enter opens the details")
	}
	m = send(m, key("esc"))
	m = send(m, key("esc")) // in the list: clears the filter
	if len(m.cur().list.visible) != 3 {
		t.Fatalf("esc in the list clears the filter, %d visible", len(m.cur().list.visible))
	}
	// esc in filter mode clears and leaves filter mode
	m = typeText(send(m, key("/")), "err")
	m = send(m, key("esc"))
	if m.cur().list.filtering || len(m.cur().list.visible) != 3 {
		t.Fatal("esc in filter mode clears the filter and leaves filter mode")
	}
}

func TestEnterWithAnEmptyListDoesNothing(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: lsnap()}) // no listeners
	m = send(m, key("tab"))
	m = send(m, key("enter"))
	if m.focus != focusList {
		t.Fatal("enter on an empty list must not focus the details")
	}
	m = send(m, key("tab"))
	m = typeText(send(m, key("/")), "zzzz")
	m = send(m, key("enter"))
	m = send(m, key("enter"))
	if m.focus != focusList {
		t.Fatal("enter with no match must not focus the details")
	}
}

func TestDetailsFocusScrollsConfigAndOverviewWithArrows(t *testing.T) {
	l := lst("big", "0.0.0.0:1", snapshot.ListenerActive)
	for i := 0; i < 30; i++ {
		l.Chains = append(l.Chains, snapshot.FilterChain{Label: strings.Repeat("c", 1) + string(rune('A'+i%26)), Filters: []string{"tcp_proxy"}})
	}
	l.Config = snapshot.ConfigInfo{YAML: strings.Repeat("key: value\n", 80) + "last: line\n"}
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: lsnap(l, lst("other", "0.0.0.0:2", snapshot.ListenerActive))})
	m = send(m, key("tab"))
	m = send(m, key("enter"))
	for i := 0; i < 200; i++ {
		m = send(m, key("down"))
	}
	if activeName(m) != "big" || m.cur().detail.scroll == 0 {
		t.Fatalf("arrows scroll the Overview and leave the selection alone (selected %q scroll %d)", activeName(m), m.cur().detail.scroll)
	}
	m = send(m, key("c"))
	for i := 0; i < 200; i++ {
		m = send(m, key("down"))
	}
	if !strings.Contains(m.View(), "last: line") || activeName(m) != "big" {
		t.Fatal("arrows scroll the Config YAML too")
	}
	m = send(m, key("o"))
	if m.cur().detail.tab != tabOverview {
		t.Fatal("o opens the first tab")
	}
	m = send(m, key("c"))
	m = send(m, key("e"))
	if m.cur().detail.tab != tabOverview {
		t.Fatal("e is an alias for the first tab")
	}
}

func TestHeaderShowsBothTabsWithTheActiveOneMarked(t *testing.T) {
	m := listenersModel()
	v := m.View()
	if !strings.Contains(v, "Clusters (2)") || !strings.Contains(v, "Listeners (3)") {
		t.Fatalf("both tabs with counts:\n%s", v)
	}
	// in a no-colour render the active tab is the one wrapped in the reversed padding; assert order and counts only
	if strings.Index(v, "Clusters (2)") > strings.Index(v, "Listeners (3)") {
		t.Fatal("tab order is Clusters, Listeners")
	}
	if compact := sized(m, 100, 20).View(); !strings.Contains(compact, "Listeners (3)") {
		t.Fatalf("the compact header keeps both tabs:\n%s", compact)
	}
}

func TestStatusCountsFollowTheActiveTab(t *testing.T) {
	m := listenersModel()
	if v := m.View(); !strings.Contains(v, "2 clusters") {
		t.Fatalf("clusters status:\n%s", v)
	}
	m = send(m, key("tab"))
	v := m.View()
	if !strings.Contains(v, "3 listeners · 2 not active") {
		t.Fatalf("listeners status:\n%s", v)
	}
}

func TestHintsPutTheKeysThatMatterFirst(t *testing.T) {
	m := sized(New(testOpts(nil)), 80, 24)
	m = send(m, snapshotMsg{snap: lsnap(lst("a", "0.0.0.0:1", snapshot.ListenerActive))})
	v := m.View()
	last := v[strings.LastIndex(v, "\n")+1:]
	for _, frag := range []string{"? help", "q quit", "tab", "enter"} {
		if !strings.Contains(last, frag) {
			t.Fatalf("at 80 columns the status line must still show %q: %q", frag, last)
		}
	}
	m = send(m, key("enter"))
	v = m.View()
	last = v[strings.LastIndex(v, "\n")+1:]
	if !strings.Contains(last, "esc back") {
		t.Fatalf("details focus hint: %q", last)
	}
}

func TestListenerListInlineMessages(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, key("tab"))
	if !strings.Contains(m.View(), "waiting for Envoy") {
		t.Fatalf("before the first poll:\n%s", m.View())
	}
	m = send(m, snapshotMsg{snap: lsnap()})
	if !strings.Contains(m.View(), "no listeners") {
		t.Fatalf("no listeners:\n%s", m.View())
	}
	failed := lsnap()
	failed.ListenersErr = "listeners unavailable: GET /config_dump: 503 Service Unavailable"
	m = send(m, snapshotMsg{snap: failed})
	v := m.View()
	if !strings.Contains(v, "listeners unavailable") || !strings.Contains(v, "Listeners (!)") {
		t.Fatalf("a failed dump shows inline and in the tab:\n%s", v)
	}
	if m.bannerVisible() {
		t.Fatal("a listener failure is not a banner")
	}
	if strings.Contains(m.View(), "no listeners") {
		t.Fatal("the failure message replaces 'no listeners'")
	}
	// the Clusters tab is unaffected
	m = send(m, key("tab"))
	if !strings.Contains(m.View(), "alpha") || strings.Contains(m.View(), "listeners unavailable") {
		t.Fatalf("clusters keep working:\n%s", m.View())
	}
	// a long message wraps inside the pane
	failed.ListenersErr = "listeners unavailable: " + strings.Repeat("very long reason ", 30)
	m = send(send(m, snapshotMsg{snap: failed}), key("tab"))
	for i, line := range strings.Split(m.View(), "\n") {
		if w := len([]rune(ansiStrip(line))); w > 120 {
			t.Fatalf("line %d is %d runes wide", i, w)
		}
	}
	if n := len(strings.Split(m.View(), "\n")); n != 30 {
		t.Fatalf("height %d, want 30", n)
	}
}

func TestSelectedListenerDisappears(t *testing.T) {
	m := listenersModel()
	m = send(m, key("tab"))
	m = send(m, key("j"))
	m = send(m, key("j")) // l-error, the last row
	m = send(m, snapshotMsg{snap: lsnap(lst("l-active", "0.0.0.0:8080", snapshot.ListenerActive), lst("l-warming", "0.0.0.0:9090", snapshot.ListenerWarming))})
	it, ok := m.cur().list.Selected()
	if !ok || it.Key() != "l-warming" || m.cur().detail.item.Key() != "l-warming" {
		t.Fatalf("selection falls to the nearest row and the details follow: %v %v", ok, it)
	}
	m = send(m, snapshotMsg{snap: lsnap()})
	if _, ok := m.cur().list.Selected(); ok || !strings.Contains(m.View(), "no listener selected") {
		t.Fatalf("empty listeners:\n%s", m.View())
	}
}

func TestListenersWithAwkwardData(t *testing.T) {
	bare := snapshot.Listener{Name: "bare"}
	wide := lst("日本語のリスナー", "unix:/var/run/日本.sock", snapshot.ListenerActive)
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: lsnap(bare, wide)})
	m = send(m, key("tab"))
	for _, k := range []string{"enter", "c", "o", "esc", "j"} {
		m = send(m, key(k))
		for i, line := range strings.Split(m.View(), "\n") {
			if w := len([]rune(ansiStrip(line))); w > 120 {
				t.Fatalf("after %s line %d is %d runes wide", k, i, w)
			}
		}
	}
}

func TestFilteringListenersByAddress(t *testing.T) {
	m := send(listenersModel(), key("tab"))
	m = typeText(send(m, key("/")), "9443")
	if activeName(m) != "l-error" || len(m.cur().list.visible) != 1 {
		t.Fatalf("filter matches the address: %q (%d visible)", activeName(m), len(m.cur().list.visible))
	}
}

func TestViewsFitTheTerminalAtEverySize(t *testing.T) {
	for _, sz := range [][2]int{{120, 30}, {80, 24}, {100, 20}, {80, 12}, {200, 60}} {
		m := sized(New(testOpts(nil)), sz[0], sz[1])
		m = send(m, snapshotMsg{snap: lsnap(lst("a", "0.0.0.0:1", snapshot.ListenerActive))})
		for _, steps := range [][]string{{}, {"tab"}, {"tab", "enter"}, {"tab", "enter", "c"}} {
			mm := m
			for _, k := range steps {
				mm = send(mm, key(k))
			}
			if n := len(strings.Split(mm.View(), "\n")); n != sz[1] {
				t.Fatalf("%v %v: %d lines", sz, steps, n)
			}
		}
	}
}
```

(`fixedNow`, `testOpts`, `send`, `sized`, `key`, `cl`, `typeText`, `lst` come from the existing test helpers and Task 7.)

Update `internal/ui/chrome_test.go`: calls `renderHeader(w, compact, c1, c2, "Clusters (3)")` become `renderHeader(w, compact, c1, c2, []headerTab{{Label: "Clusters (3)", Active: true}})`; add

```go
func TestHeaderTabRowShowsAllTabs(t *testing.T) {
	tabs := []headerTab{{Label: "Clusters (6)", Active: true}, {Label: "Listeners (!)", Warn: true}}
	for _, compact := range []bool{false, true} {
		h := renderHeader(100, compact, "● connected", "row2", tabs)
		lines := strings.Split(h, "\n")
		tabLine := lines[len(lines)-1]
		if !strings.Contains(tabLine, "Clusters (6)") || !strings.Contains(tabLine, "Listeners (!)") ||
			strings.Index(tabLine, "Clusters (6)") > strings.Index(tabLine, "Listeners (!)") {
			t.Fatalf("compact=%v: %q", compact, tabLine)
		}
		for _, l := range lines {
			if lipgloss.Width(l) > 100 {
				t.Fatalf("wide line %q", l)
			}
		}
	}
	narrow := renderHeader(20, false, "x", "y", tabs)
	for _, l := range strings.Split(narrow, "\n") {
		if lipgloss.Width(l) > 20 {
			t.Fatalf("narrow: %q", l)
		}
	}
}

func TestActiveHeaderTabDiffersFromInactive(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	a := renderHeader(100, true, "", "", []headerTab{{Label: "One", Active: true}, {Label: "Two"}})
	b := renderHeader(100, true, "", "", []headerTab{{Label: "One"}, {Label: "Two", Active: true}})
	if a == b {
		t.Fatal("the active tab must be styled differently from an inactive one")
	}
}
```

Run: `go test ./internal/ui/ -run 'TestTab|TestEach|TestEnter|TestDetails|TestHeader|TestStatus|TestHints|TestListener|TestSelectedListener|TestListeners|TestFiltering|TestViews'`
Expected: FAIL (undefined).

- [ ] **Step 2: Implement `view.go`**

```go
package ui

import (
	"fmt"

	"github.com/regeda/txenvoy/internal/snapshot"
)

type resourceKind int

const (
	kindClusters resourceKind = iota
	kindListeners
	kindCount
)

// view is one resource's list and details. The model keeps one per resource, so
// each tab keeps its own filter text, selection, details tab and scroll.
type view struct {
	list   listPane
	detail detailPane
}

// resource describes how a snapshot turns into a view and its chrome.
type resource struct {
	label       string
	items       func(snapshot.Snapshot) []resourceItem
	count       func(snapshot.Snapshot) int
	summary     func(snapshot.Snapshot) string
	notice      func(snapshot.Snapshot) string
	warn        func(snapshot.Snapshot) bool
	emptyList   string
	emptyDetail string
}

var resources = [kindCount]resource{
	kindClusters: {
		label:       "Clusters",
		items:       func(s snapshot.Snapshot) []resourceItem { return clusterItems(s.Clusters) },
		count:       func(s snapshot.Snapshot) int { return len(s.Clusters) },
		summary:     func(s snapshot.Snapshot) string { return fmt.Sprintf("%d clusters · %d unhealthy", len(s.Clusters), s.UnhealthyClusters()) },
		notice:      func(snapshot.Snapshot) string { return "" },
		warn:        func(snapshot.Snapshot) bool { return false },
		emptyList:   "no clusters",
		emptyDetail: "no cluster selected",
	},
	kindListeners: {
		label:       "Listeners",
		items:       func(s snapshot.Snapshot) []resourceItem { return listenerItems(s.Listeners) },
		count:       func(s snapshot.Snapshot) int { return len(s.Listeners) },
		summary:     func(s snapshot.Snapshot) string { return fmt.Sprintf("%d listeners · %d not active", len(s.Listeners), s.ListenersNotActive()) },
		notice:      func(s snapshot.Snapshot) string { return s.ListenersErr },
		warn:        func(s snapshot.Snapshot) bool { return s.ListenersErr != "" },
		emptyList:   "no listeners",
		emptyDetail: "no listener selected",
	},
}

func newView(r resource, color bool) view {
	v := view{list: newListPane(), detail: newDetailPane(color)}
	v.list.SetWaiting(true)
	v.list.SetEmptyText(r.emptyList)
	v.detail.SetEmptyText(r.emptyDetail)
	return v
}

func (r resource) tabLabel(s snapshot.Snapshot) string {
	if r.warn(s) {
		return r.label + " (!)"
	}
	return fmt.Sprintf("%s (%d)", r.label, r.count(s))
}
```

- [ ] **Step 3: Rework `chrome.go`'s header**

```go
// headerTab is one entry of the tab row.
type headerTab struct {
	Label  string
	Active bool
	Warn   bool
}

func renderTabRow(width int, tabs []headerTab) string {
	var parts []string
	for _, t := range tabs {
		label := " " + t.Label + " "
		switch {
		case t.Active:
			parts = append(parts, lipgloss.NewStyle().Bold(true).Reverse(true).Render(label))
		case t.Warn:
			parts = append(parts, warnStyle.Render(label))
		default:
			parts = append(parts, dimStyle.Render(label))
		}
	}
	return trunc(" "+strings.Join(parts, " "), width)
}

func renderHeader(width int, compact bool, conn1, conn2 string, tabs []headerTab) string {
	tabLine := renderTabRow(width, tabs)
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
```

- [ ] **Step 4: Rework `model.go`**

Replace the `list`/`detail` fields and everything that touched them:

```go
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
	views         [kindCount]view // an array, so a Model copy never aliases another's views
	active        resourceKind
}

func New(o Options) Model {
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Ctx == nil {
		o.Ctx = context.Background()
	}
	m := Model{opts: o, inflight: true, state: stateConnecting}
	for k := resourceKind(0); k < kindCount; k++ {
		m.views[k] = newView(resources[k], o.Color)
	}
	return m
}

// cur is the active view (a pointer into this copy of the model).
func (m *Model) cur() *view { return &m.views[m.active] }
```

`cur()` has a pointer receiver; in value-receiver methods (`View`, `statusLine`, `detailBlock`) use `m.views[m.active]` directly, and in tests call `m.cur()` on an addressable `Model` variable.

```go
func (m *Model) layout() {
	listW := m.listWidth()
	detailW := max(m.width-listW-1, 1)
	for k := range m.views {
		m.views[k].list.SetSize(listW, m.paneHeight())
		m.views[k].detail.SetSize(detailW, m.paneHeight())
	}
}

func (m *Model) syncDetails() {
	for k := range m.views {
		v := &m.views[k]
		it, ok := v.list.Selected()
		v.detail.SetItem(it, ok)
	}
}

// setFocus moves the keyboard focus; only the active view shows it.
func (m *Model) setFocus(f focus) {
	m.focus = f
	for k := range m.views {
		active := resourceKind(k) == m.active
		m.views[k].list.SetFocused(!active || f == focusList)
		m.views[k].detail.SetFocused(active && f == focusDetail)
	}
}

func (m *Model) switchTo(k resourceKind) {
	m.active = k
	m.setFocus(focusList)
}
```

In `Update`: `tea.WindowSizeMsg` calls `m.layout(); m.syncDetails()`. The `snapshotMsg` success branch becomes:

```go
		m.snap, m.hasSnap = msg.snap, true
		m.lastOK = msg.snap.FetchedAt
		if m.lastOK.IsZero() {
			m.lastOK = m.opts.Clock()
		}
		for k := resourceKind(0); k < kindCount; k++ {
			v := &m.views[k]
			v.list.SetWaiting(false)
			v.list.SetNotice(resources[k].notice(msg.snap))
			v.list.SetItems(resources[k].items(msg.snap))
		}
		m.syncDetails()
		return m, nil
```

`handleKey` becomes:

```go
func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.focus == focusList && m.cur().list.filtering {
		switch k.String() {
		case "tab", "shift+tab":
			return m, nil // finish the filter first
		}
		m.cur().list.HandleKey(k)
		m.syncDetails()
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
		m.switchTo((m.active + 1) % kindCount)
	case "shift+tab":
		m.switchTo((m.active + kindCount - 1) % kindCount)
	case "enter":
		if m.focus == focusList {
			if _, ok := m.cur().list.Selected(); ok {
				m.setFocus(focusDetail)
			}
		}
	case "esc":
		if m.focus == focusDetail {
			m.setFocus(focusList)
		} else {
			m.cur().list.HandleKey(k) // clears an active filter
			m.syncDetails()
		}
	case "c":
		m.cur().detail.SetTab(tabConfig)
	case "o", "e":
		m.cur().detail.SetTab(tabOverview)
	default:
		if k.String() == "/" {
			m.setFocus(focusList)
		}
		if m.focus == focusList {
			m.cur().list.HandleKey(k)
			m.syncDetails()
		} else {
			m.cur().detail.HandleKey(k)
		}
	}
	return m, nil
}
```

`statusLine`, `detailBlock` and `View` read the active view:

```go
func (m Model) statusLine() string {
	left := " " + resources[m.active].summary(m.snap)
	if m.inflight {
		left += " ↻"
	}
	right := "? help · q quit · tab switch · enter details · / filter · c config · o overview · r refresh "
	if m.focus == focusDetail {
		left += " · focus: detail"
		right = "esc back · ↑↓ scroll · c config · o overview · q quit "
	}
	if m.help {
		right = "tab switch · enter details · esc back · ↑↓/jk move · / filter · c config · o overview · r refresh · x dismiss · q quit "
	}
	return renderStatus(m.width, left, right)
}
```

In `detailBlock` use `m.views[m.active].detail.View()`; in `View` use `m.views[m.active].list.View()`, and build the header with:

```go
	tabs := make([]headerTab, kindCount)
	for k := resourceKind(0); k < kindCount; k++ {
		tabs[k] = headerTab{Label: resources[k].tabLabel(m.snap), Active: k == m.active, Warn: resources[k].warn(m.snap)}
	}
	header := renderHeader(m.width, m.height < compactHeight, c1, c2, tabs)
```

- [ ] **Step 5: Mechanical test rewrites**

In the existing tests (`model_test.go`, `golden_test.go`, `focus_test.go`, `keys` helpers): `m.list.` → `m.cur().list.`; `m.detail.` → `m.cur().detail.`; `m.syncDetail()` → `m.syncDetails()`; `m.list.Selected()` returns an item (use `.Key()`); `snap(...)` keeps building cluster-only snapshots (listeners empty). In `focus_test.go` the old "Tab toggles focus" tests change meaning: delete `TestTabShowsWhichPaneHasFocus`'s Tab step in favour of `Enter`, and rewrite `TestEscAndShiftTabReturnFocusToList` to loop over `{"esc"}` only (Shift+Tab and Tab now switch resources and are covered by `views_test.go`); `TestTabThenArrowsScrollTheConfig` becomes `Enter` then arrows. `TestSlashFromDetailFocusesListFilter` stays. Keep every assertion about indicators (`▌`, `›`, `focus: detail`, hint text updated to `esc back`). The hint-text assertion `tab/esc back to list` becomes `esc back`.

- [ ] **Step 6: Verify**

Run: `go test -race -count=1 ./internal/ui/` — the new tests PASS and every pre-existing non-golden test PASSes. Goldens are expected to differ now (the header gained a tab); do **not** regenerate them in this task. Confirm only the header tab row changed:

```bash
go test ./internal/ui/ -run Golden 2>&1 | grep -E '^\s+[-+]' | grep -vE 'Clusters \(|Listeners \(' | head
```

Expected: no lines outside the tab-row changes. (Goldens are regenerated and reviewed in Task 9.)

- [ ] **Step 7: Mutation checks and commit**

(a) Make `handleKey` ignore the `filtering` guard for `tab`: `TestTabIsIgnoredWhileTypingAFilter` must fail. (b) Make `switchTo` not reset focus: `TestTabSwitchLandsOnTheList` must fail. (c) Make `Enter` focus the details even with no selection: `TestEnterWithAnEmptyListDoesNothing` must fail. Restore by copy after each.

```bash
git add internal/ui
git commit -m "feat(ui): add the Listeners tab with Tab, Enter and Esc navigation" -m "Goldens are regenerated in the next commit." -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Goldens, e2e, docs and the real binary

**Files:**
- Modify: `internal/ui/golden_test.go`, `internal/ui/testdata/*.golden`, `internal/ui/e2e_test.go`, `README.md`, `docs/superpowers/specs/2026-10-14-cluster-explorer-design.md`, `docs/superpowers/specs/2026-10-14-listeners-tab-design.md`
- Create: new `internal/ui/testdata/TestGolden*Listener*.golden`

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Add the golden tests**

Append to `internal/ui/golden_test.go` (helpers `loaded`, `golden`, `typeText`, `send`, `key` already exist; `loaded` fetches through `demo`, so the Degraded scenario's seven listeners are present):

```go
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
```

(`bad-update` sorts first, so `TestGoldenListenersList` shows the error Overview with its error block and the `still serving the previous configuration` note.)

Run `go test ./internal/ui/ -run Golden -update`, then **read every changed and new golden** and check against the spec (§3, §6): header `Clusters (N)  Listeners (M)` with the active tab marked and `Listeners (!)` in the failed case; the Listeners list sorted error → draining → warming → active/static with glyphs `✗ ◐ ◐ ●`; the Overview rows and chain lines as specified; the error block with `still serving the previous configuration`; numbered Config YAML for a listener; `focus: detail` and `▌` after Enter; the status line `7 listeners · 3 not active`; every golden exactly `height` lines and nothing wider than the terminal. The existing Clusters goldens must differ from before **only** by the tab row. If anything else moved or looks wrong, fix the code, not the golden, and regenerate.

- [ ] **Step 2: Extend the e2e test**

In `internal/ui/e2e_test.go` add a second test (reuse `waitFor`, the setup of `TestEndToEndFailureAndRecovery`, and the demo Fault):

```go
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

	waitFor(t, tm, "Clusters (6)", "Listeners (7)")
	tm.Send(key("tab"))
	waitFor(t, tm, "virtualInbound", "0.0.0.0:15006")

	// fail only the dynamic listener dump: no banner, an inline message instead
	fake.Inject(demo.Fault{Status: 503, Match: "dynamic_listeners"})
	waitFor(t, tm, "listeners unavailable", "Listeners (!)")
	tm.Send(key("tab"))
	waitFor(t, tm, "api-gateway") // the Clusters tab still works

	fake.Inject(demo.Fault{})
	tm.Send(key("tab"))
	waitFor(t, tm, "virtualInbound")

	tm.Send(key("q"))
	final := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second)).(Model)
	if final.bannerVisible() || final.err != nil {
		t.Fatalf("a listener failure must never raise the banner (err=%v)", final.err)
	}
}

Run: `go test -race -count=10 -run EndToEnd ./internal/ui/`
Expected: PASS ten times. Mutation: make `Fetch` treat a listener failure as fatal; the test must fail (the banner appears or the clusters vanish).

- [ ] **Step 3: Docs**

- `README.md`: describe the Listeners tab in the feature list; replace the Keys table rows for Tab/Esc with: `Tab` / `Shift+Tab` switch between Clusters and Listeners; `Enter` focus the details; `Esc` back to the list (in the list it clears the filter); arrows scroll the details while focused; `c` Config, `o`/`e` Overview/Endpoints. Mention that the filter matches listener name and address.
- `docs/superpowers/specs/2026-10-14-cluster-explorer-design.md`: update §5.1/§5.2 (the tab row shows every resource) and §5.8 (the key table) to point at the listeners spec for the new keys.
- `docs/superpowers/specs/2026-10-14-listeners-tab-design.md`: apply the five deviations listed at the top of this plan (`OverviewMaxScroll(w, h)`; right-aligned per-row address; `ConfigInfo` with `JSON` and `Render`; `Fault.Match`; the hint-line order `? help · q quit · tab switch · enter details · / filter`).

- [ ] **Step 4: Run the real binary under tmux and look at it**

```bash
make build
# in a throwaway tmux server (not the user's): -L txlisteners
```

Start `bin/txenvoy --demo --refresh 1s` in a 120x30 tmux window, press Tab, capture the pane, press Enter, then `c`, then Esc, Tab, and `q`; capture after each step and read the screens. Confirm: both tabs in the header, the glyphs, the Overview, numbered Config YAML, the `focus: detail` indicator, Tab back to Clusters, `q` exits cleanly. Report what was verified and what could not be (colours are not checked by eye).

- [ ] **Step 5: Verify and commit**

Run: `make check` and `go test -race -count=3 ./internal/ui/`
Expected: all green.

```bash
git add -A
git commit -m "test(ui): add listener goldens and an e2e test; document the Listeners tab" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```
