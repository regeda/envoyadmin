# envoyadmin

A real-time terminal UI for Envoy. It has three tabs, **Clusters** (a cluster explorer with live endpoint health), **Listeners** and **Secrets**:
a list on the left, details on the right, and a header showing the connection state and errors. `Tab` switches between them.

- Cluster list: name, type (EDS / STRICT_DNS / ...), healthy/total hosts, and a colored marker when any host is unhealthy or ejected. `/` fuzzy-filters.
- Endpoints tab: address, health flags (`FAILED_ACTIVE_HC`, `FAILED_OUTLIER_CHECK`, `EDS_STATUS_UNHEALTHY`, ...), weight, zone, `rq_active` / `rq_error` / `cx_active`.
- Config tab (`c`): the cluster's config from `/config_dump` as highlighted, line-numbered YAML, with `version_info` and `last_updated`.
- Listeners tab: name and address with a state glyph (`●` active or static, `◐` warming or draining, `✗` rejected update), sorted problems first. `/` fuzzy-filters by name and address.
- Listener Overview (`o`): bind address, state, last updated, the error of a rejected update (with a note when an earlier configuration is still serving) and the filter chains with their match and filters. A chain with an HttpConnectionManager lists its HTTP filters as a tree under its filters line (`├ cors`, `└ router`). The Config tab (`c`) shows the listener as numbered YAML.
- A failing listener dump never raises the error banner: the tab reads `Listeners (!)` with an inline message and Clusters keep working.
- Secrets tab: every secret Envoy has loaded, with state (`●` active or static, `◐` warming, `✗` certificate expired), name and `version_info`; warming and expired ones sort first. `/` fuzzy-filters by name.
- Secret Overview (`o`): kind (`tls_certificate`, `validation_context`, ...), version, last updated, the certificate's serial, SANs and expiry (read from the public certificate in the secrets dump), and the clusters and listeners that reference the secret. Secret content is never fetched or shown; the Config tab (`c`) only says so. The cluster and listener Overviews list the secrets they use, and mark one Envoy has not loaded as `(not loaded)`.
- A failing secrets dump never raises the error banner: the tab reads `Secrets (!)` with an inline message (the label also shows `(!)` when a secret is warming or has an expired certificate), and a secret whose certificate cannot be read is listed without certificate details.
- Auto-refresh; the UI never blocks on HTTP. Errors appear as a red banner over the top of the screen and clear when Envoy answers again.

## Install and run

```sh
make build            # bin/envoyadmin
make run              # try it with the built-in fake Envoy, no cluster needed
bin/envoyadmin --admin http://localhost:15000
```

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--admin` | `http://localhost:15000` | Envoy admin base URL |
| `--refresh` | `3s` | Poll interval (minimum `500ms`) |
| `--timeout` | `3s` | Per-request timeout for each admin call |
| `--demo` | off | Use a built-in fake admin server (`--admin` is ignored) |

`envoyadmin version` prints the version. Set `NO_COLOR=1` to disable colors.

## Keys

| Key | Action |
|---|---|
| `↑/↓`, `j/k` | move in the list, or scroll the focused detail pane |
| `PgUp/PgDn`, `g/G` | page, jump to top / bottom |
| `Ctrl+N` / `Ctrl+P` | move while typing in the filter |
| `/` | fuzzy filter by name (and address, on Listeners); `Enter` keeps it, `Esc` clears it |
| `Tab` / `Shift+Tab` | switch between Clusters and Listeners (ignored while typing a filter) |
| `Enter` | focus the details (marked by `▌` on the detail tab line, `focus: detail` in the status bar and a `›` list cursor); arrows, `j/k`, `PgUp/PgDn` and `g/G` then scroll them |
| `Esc` | back to the list (in the list it clears the filter) |
| `c` | Config tab |
| `o` / `e` | Overview tab (Endpoints for a cluster) |
| `r` | refresh now |
| `x` | dismiss the error banner |
| `?` | toggle the full key list |
| `q`, `Ctrl+C` | quit |

## Running against a Kubernetes pod

envoyadmin reads the Envoy admin API, so forward the admin port to your machine and point `--admin` at it.

1. **Plain pod or deployment.** `deploy/<name>` or `svc/<name>` also work as the forward target.

   ```sh
   kubectl -n <ns> port-forward pod/<pod> 15000:15000
   envoyadmin --admin http://localhost:15000
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
   - If the forward drops, envoyadmin shows the error banner and recovers by itself once the forward is back.

A built-in `--pod <ns>/<name>` using client-go port-forward is planned. It will sit behind the same `admin.Client` interface.

## Development

```sh
make test          # go test -race ./...
make test-update   # regenerate the teatest golden files in internal/ui/testdata
make check         # gofmt check + lint + tests
make help          # all targets
```

The design is in `docs/superpowers/specs/` and the implementation plan in `docs/superpowers/plans/`.
