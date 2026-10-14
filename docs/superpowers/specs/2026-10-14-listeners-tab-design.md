# txenvoy: Listeners tab Design

Date: 2026-10-14
Builds on: `2026-10-14-cluster-explorer-design.md` (the v1 cluster explorer, merged to `main`).

## 1. Goal

Add a second resource tab, **Listeners**, with the same pattern as Clusters: a filterable list on the left, details on the right (an Overview and a Config tab). The user can find a listener by name or address, see whether Envoy is serving it, was asked to drain it, is warming a new version, or rejected an update, and read its config as numbered YAML.

Success criteria:

- A listener is found by typing part of its name or bind address in the fuzzy filter.
- State (active, warming, draining, error, static) is visible in the list without relying on colour.
- The Overview shows exactly: bind address, state, `version_info`, `last_updated`, and the filter chains.
- A rejected update (`error_state`) is obvious in the list and explained in the Overview.
- Tab / Shift+Tab switch between Clusters and Listeners; Enter focuses the details; Esc returns to the list.
- One shared list pane and one shared details pane serve both resources, so a later resource (routes, certs) is one more item type.
- Behaviour of the existing Clusters tab does not change (other than the keys named in §3).

## 2. Scope

### In scope

- Listeners tab: list, fuzzy filter, Overview, Config (numbered YAML).
- A shared `resourceItem` interface behind the list and details panes (behaviour-preserving refactor first).
- Per-resource view state: each tab keeps its own filter text, selection, details tab and scroll.
- New key semantics (§3): Tab / Shift+Tab switch resources, Enter focuses the details, Esc returns.
- Admin and snapshot support for listeners (`config_dump` only).
- Parallel secondary fetches; lazy YAML conversion.
- Demo scenarios, fixtures, real Envoy captures, goldens, an e2e test, README and spec updates.

### Out of scope

- `listener_filters` (for example `tls_inspector`), listener stats and drain statistics.
- Route config, virtual hosts, cluster references from filters, and request-path navigation between resources. The Overview deliberately does not show what a filter points to.
- `/listeners?format=json` (the bind address comes from the config dump).
- Per-chain TLS details beyond a `TLS` tag; sorting options; mouse support.

## 3. Navigation

### Resource tabs

- `Tab` cycles `Clusters → Listeners → Clusters`; `Shift+Tab` cycles backwards. Both work from either focus (list or details).
- A switch always lands on the list of the target tab (details focus is not carried across).
- Each tab keeps its own filter text, selection, details tab and scroll while the other tab is shown.
- Tab and Shift+Tab are ignored while the filter input is active; finish the filter first (Enter or Esc).

### Focus

- `Enter` in the list (when not typing a filter, and an item is selected) moves focus to the details. With an empty list it does nothing.
- `Esc` in the details returns focus to the list.
- `Esc` in the list clears an active filter; with no filter it does nothing.
- Filter mode (started with `/`): `Enter` keeps the filter and leaves filter mode, `Esc` clears the filter and leaves filter mode. A second `Enter` then opens the details.
- With focus on the details, the arrows, `j`/`k`, PgUp/PgDn, `g`/`G` scroll the details pane (the Overview body or the Config YAML).
- Focus indicators stay as they are: `▌` on the detail tab line, a `›` list cursor while the list is unfocused, `focus: detail` in the status bar.

### Detail tabs

- `c` opens the Config tab (both resources).
- `o` opens the first tab: Overview for listeners, Endpoints for clusters. `e` remains an alias for the cluster Endpoints tab.

### Unchanged

`/` filter, `r` refresh, `x` dismiss the banner, `?` long hint line, `q` quit (not while typing a filter), `Ctrl+C` always quits.

### Header and status bar

- The tab row shows both resources: `Clusters (6)  Listeners (3)`. The active tab is reversed and bold, the other is dim. If the listener dump failed the label reads `Listeners (!)` in yellow. The compact header keeps the same tab row.
- Status bar counts follow the active tab: `6 clusters · 2 unhealthy`, or `3 listeners · 1 not active` (warming, draining or error). `↻` and `focus: detail` appear as today.
- Hint line, list focused: `? help · q quit · tab switch · enter details · / filter · c config · o overview · r refresh` (`? help` and `q quit` first so they survive clipping at 80 columns). Details focused: `esc back · ↑↓ scroll · c config · o overview · q quit`. The `?` long line lists every key.

## 4. Shared components

### The item interface

Both resources implement one small interface; the panes never mention clusters or listeners:

```go
type resourceItem interface {
    Key() string                     // stable id; keeps the selection across refreshes
    FilterText() string              // text the fuzzy filter matches
    Row(rowCtx) string               // one list row (width, cursor, focus, match hits)
    Title() string                   // detail header (the name)
    OverviewName() string            // "Endpoints" or "Overview"
    Overview(w, h, scroll int) string // exactly h lines, none wider than w
    OverviewMaxScroll(w, h int) int      // w too: the error block wraps to the pane width
    Config() snapshot.ConfigInfo     // JSON/YAML, version_info, last_updated, static, error
}
```

`rowCtx` carries the row width, whether the row has the cursor, whether the list is unfocused, and the fuzzy match hit offsets (byte offsets into `FilterText()`).

### listPane

Stores `[]resourceItem`. Everything generic stays in one place: fuzzy filter, cursor and scroll clamping, selection tracked by `Key()`, highlights, the empty / waiting / error messages, and the focus cursor glyph. `SetItems([]resourceItem)`, `Selected() (resourceItem, bool)`. The empty-state texts are configured per resource.

### detailPane

Keeps its two tabs; the first tab renders through `Overview`. The Config tab is unchanged: viewport, numbered YAML, highlight cache keyed by `Key()` plus the YAML, clipping, the error and static header. Overview scroll and its clamp come through the interface.

### Files

- `internal/ui/item.go`: `resourceItem`, `rowCtx`, highlight helper.
- `internal/ui/listpane.go`, `detailpane.go`: generic panes.
- `internal/ui/cluster_item.go`: the cluster row (moved from `listPane.row`) and the existing endpoints Overview (`endpoints.go` stays).
- `internal/ui/listener_item.go`: the listener row and Overview.
- `internal/ui/view.go`: a `view` bundle (one `listPane` plus one `detailPane`) and a descriptor per resource (tab label, snapshot → items, status summary, empty texts).
- `internal/ui/model.go`: holds one `view` per resource and an `active` index; polling, banner, layout and the rest are untouched.

### Refactor order

The pane refactor lands first and is behaviour-preserving: every existing test and golden is unchanged except test helpers that now build items. A golden moving during the refactor is a defect.

## 5. Listener data

### Source

`/config_dump?resource=dynamic_listeners` and `/config_dump?resource=static_listeners`, merged. Decoded as raw JSON like clusters:

- Elements with `@type` ending `.ListenersConfigDump.DynamicListener` or `.ListenersConfigDump.StaticListener` are read as elements.
- The wrapped `.ListenersConfigDump` shape (nested `dynamic_listeners` / `static_listeners` arrays) is a fallback.
- Bad entries are skipped (non-object, missing or non-string `@type`, no name, no listener); an empty `{}` is fine; unknown `Any` types never fail the decode.
- A static listener has no top-level `name`; its name is `listener.name`.

### admin layer

`Client` gains `ListenerConfigs(ctx) ([]ListenerConfig, error)`.

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
    Name                     string
    Static                   bool
    Active, Warming, Draining *ListenerState
    Error                    *ListenerError
}
```

### snapshot layer

`Snapshot` gains `Listeners []Listener` and `ListenersErr string`.

```go
type Listener struct {
    Name, Address string
    State         ListenerStatus // Active, Warming, Draining, Error
    Static        bool
    VersionInfo   string
    LastUpdated   time.Time
    ErrorDetails  string
    ServingOld    bool          // an error with a previous version still active
    Chains        []FilterChain
    Config        ConfigInfo
}
type FilterChain struct {
    Label   string   // name, "#N", "default"
    Match   []string // ordered parts, empty means any
    Filters []string // network filter names, prefix stripped
    TLS     bool
}
```

- **State precedence:** error > draining > warming > active. A listener with a rejected update and an older version still active has `State = Error` and `ServingOld = true`. Static listeners are `Active` with `Static = true` (shown as `static`).
- **VersionInfo / LastUpdated** come from the serving state: active, else warming, else draining, else the error attempt.
- **Address:** `address.socket_address` as `host:port` (IPv6 bracketed); a pipe as `unix:<path>`; empty if neither is present.
- **Filter chains** are parsed from the listener JSON:
  - Label: the chain name, or `#1`, `#2` when unnamed; `default_filter_chain` is labelled `default`. A regular chain without a match shows match `any`.
  - Match parts, in this order and only when set: `sni=a.com,b.com`, `port=443`, `dst=10.0.0.0/8`, `src=…`, `src_port=…`, `src_type=…` (when not `ANY`), `transport=tls`, `alpn=h2,http/1.1`.
  - Filters: network filter names in order with the `envoy.filters.network.` prefix stripped. Their targets (routes, clusters) are not shown.
  - `TLS` is set when a transport socket whose name contains `tls` is configured.
- **Config tab:** the YAML of the listener that is serving (active, else warming, else draining); for a listener with only a rejected configuration, the rejected configuration.
- **Sort:** error first, then draining and warming, then active and static, each by name.

### Fetch behaviour

- `/clusters` stays the only required call and its failure is the only banner trigger.
- After it succeeds, the cluster configs, listener configs and server info are fetched in parallel; one poll takes about the slowest call, not the sum. (This also resolves the earlier "up to four timeouts per fetch" note.)
- If the listener dump fails, `Snapshot.ListenersErr` holds `listeners unavailable: <reason>`; the Listeners tab shows it inline, Clusters keeps working, and there is no banner.
- A cluster-config failure keeps the existing partial behaviour.

### Lazy YAML

`ConfigInfo` carries the raw `JSON` and a pointer to a shared lazy cache. `Render() (text, problem string)` converts on first use and caches the result for the life of that snapshot; a conversion failure is returned as `problem`, while `Err` is set at fetch time only. The `YAML` field remains as an explicit pre-rendered override (it wins over `JSON` when set); `Fetch` no longer fills it. Only the selected item is ever converted. This applies to clusters as well, which removes the "every poll converts every cluster" cost; cluster behaviour is otherwise unchanged and covered by the existing tests.

## 6. Listener UI

### List row

`›● name  address`. The state is a glyph, not only a colour:

- `●` green: active and static.
- `◐` yellow: warming and draining.
- `✗` red: error.

The name is on the left, the address is right-aligned and sized per row, not to the widest address in the list (at most 21 cells, always leaving at least 8 for the name), so a row needs no knowledge of its neighbours; the address shrinks first when space is tight and is clipped without an ellipsis (the full address is in the Overview). `FilterText()` is `name + " " + address`, so `15001` or `0.0.0.0` find a listener; highlights appear in whichever part matched. Rows never exceed the pane width and handle wide characters.

### Overview tab

```
Bind address  0.0.0.0:15001
State         active
version_info  42
last_updated  2026-10-14 10:00:00Z

Filter chains (3)
 #1       sni=a.com,b.com · port=443 · transport=tls
          http_connection_manager · TLS
 #2       any
          tcp_proxy
 default  any
          tcp_proxy
```

- The property rows and the `Filter chains (N)` heading are sticky; only the chain lines scroll (two lines per chain, clamped by `OverviewMaxScroll`).
- State values: `active`, `static`, `warming`, `draining`, `error`.
- An error adds a red block under State with the details, word-wrapped and capped at 4 lines; it says `still serving the previous configuration` when `ServingOld` is set. The rejected YAML is on the Config tab.
- Long match and filter lines are clipped with `…`, never silently; the full text is in Config.
- Tab line: `▌ name · Overview | Config`, same shape as clusters.

### Inline messages in the Listeners list

`no listeners`, `waiting for Envoy…` (before the first poll), `listeners unavailable: <reason>` (dump failed), `no matches` (filter matches nothing).

## 7. Error handling

| Situation | Behaviour |
|---|---|
| `/clusters` fails | Banner as today; both tabs keep the last good snapshot. |
| Listener dump fails | `Listeners (!)` in the tab row; inline message in the Listeners list; no banner; Clusters unaffected. |
| Cluster-config dump fails | Unchanged (per-cluster `config unavailable`). |
| Bad or unknown listener entry | Skipped; the rest decode. |
| Selected listener disappears on refresh | Selection moves to the nearest row; details follow. |
| Listener with no address or no chains | Blank address; `Filter chains (0)`; no panic. |
| Tiny or zero-size terminal | Existing "terminal too small" behaviour. |

## 8. Testing

- **Refactor phase:** every existing test and golden stays green unchanged except test helpers; `make check` after the refactor, before any listener code.
- **admin:** real captures from Envoy (static listeners and file-based LDS dynamic listeners) under `internal/admin/testdata/`; hand-written, clearly labelled fixtures for warming, draining and error; tests for the wrapped fallback, skipping bad entries, empty `{}`, unknown `Any`, and static names read from `listener.name`.
- **demo:** `Fault.Match` is a substring of the request URI; only matching requests fail, so a test can fail just the listener dump (`dynamic_listeners`).
- **snapshot:** state precedence table; address formatting (IPv4, bracketed IPv6, pipe, missing); match summary order and omission; `any` / `default` labels; filter-name stripping; the `TLS` tag; version and timestamp source; an error with the old config still serving; sort order; a listener-dump failure leaving clusters intact; lazy YAML computed once and only on demand; parallel fetch returns the same data.
- **ui:** both items against the shared interface (row width bounds, wide characters, filter text); listener glyphs; filter by address; Overview sticky rows, scrolling, clipping and the error block; Tab / Shift+Tab cycling keeping each tab's selection, filter and scroll; Tab ignored while typing a filter; Enter and Esc including the filter cases and the empty list; header tabs including `Listeners (!)`; status counts; the inline messages.
- **Review-focus inputs, each with a test that can fail:** zero listeners; the selected listener disappearing; a tiny terminal; a pipe address; a very long SNI list; wide-character names; many unnamed chains.
- **Goldens:** Listeners list with Overview; Listeners Config tab; the inline listener error; details focus on a listener; plus the existing goldens (header tab row changes are the only expected diffs there).
- **e2e:** start, Tab to Listeners, inject a listener-endpoint failure, assert the inline error appears while the Clusters tab still works.
- Key behaviours are mutation-checked as in the previous work.

## 9. Delivery order

1. Capture real listener output; add fixtures and demo scenarios.
2. admin: `ListenerConfigs` and the decoder.
3. snapshot: listeners, filter chains, lazy YAML, parallel fetch.
4. Behaviour-preserving refactor of the panes onto `resourceItem`.
5. Listener item, row and Overview.
6. Tab / Enter / Esc handling, header tab row, status bar, hints.
7. Goldens, e2e, README and the original spec's key and header sections updated.

## 10. Risks

- Listener JSON varies across Envoy and Istio versions; the real capture comes first and the walker stays tolerant.
- The refactor could shift existing goldens; that is treated as a defect.
- Tab changes meaning again (resource switch instead of focus toggle); this is the explicitly requested behaviour, and Enter / Esc cover the focus need.
- Large Istio listeners (thousands of YAML lines) are handled by converting lazily and rendering only the visible window.
