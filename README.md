# envoyadmin

A real-time terminal UI for the Envoy admin API. See your clusters, listeners and secrets at a glance, without reading `config_dump` by hand.

Three tabs, each with a list on the left and details on the right. `Tab` switches between them, and a header shows the connection state and any errors.

## Features

### Clusters
- **Live health at a glance.** Each cluster shows its type (EDS, STRICT_DNS, ...), healthy/total hosts, and a colored marker when any host is unhealthy or ejected.
- **Endpoints** (`e`): address, health flags (`FAILED_ACTIVE_HC`, `FAILED_OUTLIER_CHECK`, `EDS_STATUS_UNHEALTHY`, ...), weight, zone, and `rq_active` / `rq_error` / `cx_active`.
- **Config** (`c`): the cluster's config from `/config_dump` as highlighted, line-numbered YAML, with `version_info` and `last_updated`.

### Listeners
- **State at a glance.** `●` active or static, `◐` warming or draining, `✗` rejected update. Problems sort first.
- **Overview** (`o`): bind address, state, last update, the error of a rejected update (noting when an earlier configuration is still serving), and every filter chain with its match and filters. HTTP filters show as a tree (`├ cors`, `└ router`).
- **Config** (`c`): the listener as numbered YAML.

### Secrets
- **Every loaded secret** with its state (`●` active or static, `◐` warming, `✗` certificate expired), name and `version_info`. Warming and expired ones sort first.
- **Overview** (`o`): kind (`tls_certificate`, `validation_context`, ...), version, last update, the certificate's serial, SANs and expiry, and the clusters and listeners that use the secret.
- **Safe by design.** Secret content is never fetched or shown. Only the public certificate is read.
- **Cross-references.** Cluster and listener overviews list the secrets they use and mark any that Envoy has not loaded as `(not loaded)`.

### Everywhere
- **Fuzzy filter** with `/` on every list (by name, and by address on Listeners).
- **Never blocks.** Data refreshes automatically and HTTP calls stay off the UI thread.
- **Errors stay out of the way.** A failed fetch shows a red banner that clears when Envoy answers again. A failing listener or secrets dump instead marks its own tab, `Listeners (!)` or `Secrets (!)`, with an inline message, and the other tabs keep working. `Secrets (!)` also appears when a secret is warming or expired.

## Install and run

Download a binary for Linux or macOS (amd64 or arm64) from the [GitHub releases](https://github.com/regeda/envoyadmin/releases), or install with Go:

```sh
go install github.com/regeda/envoyadmin/cmd/envoyadmin@latest
```

Or build from source:

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
| `Tab` / `Shift+Tab` | switch between tabs (ignored while typing a filter) |
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

## License

MIT, see [LICENSE](LICENSE).
