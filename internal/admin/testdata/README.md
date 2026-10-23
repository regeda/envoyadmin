Genuine captures from Envoy `envoyproxy/envoy:v1.31-latest`, version string
`f30db0a84189cf3e681687a4a2fa1788062592d0/1.31.10/Clean/RELEASE/BoringSSL`,
taken from a container with a static bootstrap (clusters api, search, empty) plus a
file-based CDS (orders STRICT_DNS, agg aggregate). Unedited apart from the
response bodies being saved as-is; nothing host-specific is present.

- real-clusters.json      GET /clusters?format=json
- real-config-dynamic.json GET /config_dump?resource=dynamic_active_clusters
- real-config-static.json  GET /config_dump?resource=static_clusters
- real-server-info.json    GET /server_info

## Listener captures

Genuine captures from `envoyproxy/envoy:v1.31-latest`, version string
`f30db0a84189cf3e681687a4a2fa1788062592d0/1.31.10/Clean/RELEASE/BoringSSL`
(`/server_info`). Bodies are saved as-is; nothing host-specific is present.

- real-listeners-static.json   GET /config_dump?resource=static_listeners
- real-listeners-dynamic.json  GET /config_dump?resource=dynamic_listeners (one active listener)
- real-listeners-error.json    GET /config_dump?resource=dynamic_listeners after a rejected LDS update

`capture/bootstrap.yaml` and `capture/lds.yaml` reproduce the first two: mount them
at `/etc/envoy/envoy.yaml` and `/etc/envoy/lds.yaml` and run Envoy with `-c`. `lds.yaml` is version "7" and `lds-bad.yaml` is version "8"; the rejected version
never appears in the dump. The error capture starts from the fresh lds.yaml state
(no intermediate update) and then atomically replaces the LDS file
(`mv` inside the container, from a mounted directory; host-side edits are not seen
through the Docker bind mount) with `capture/lds-bad.yaml`, a tcp_proxy with no
`cluster`, which Envoy rejects with a proto constraint error. An unknown filter
name with a valid `typed_config` is accepted, so it does not produce an error.

A draining state was also provoked (LDS update to an empty resource list) but is not
saved. Warming was not provoked. Warming and draining fixtures are hand-written
(see the demo scenario and Task 3 tests).

### Listener shape notes

- A rejected update yields two elements: the old listener (name, active_state and a
  sibling error_state) and a nameless twin holding only error_state; its name is in
  `error_state.failed_configuration.name`.
- `failed_configuration` is a bare Listener (with its own `@type`), not `{listener: ...}`.
- The error timestamp is `last_update_attempt` and may be absent; `details` ends with a newline.
- Static elements have no element-level name (it is in `listener.name`).
- `port_value`, `destination_port` and `prefix_len` are JSON numbers.
- transport_socket was not captured (no TLS in the capture).
- Warming was not provoked; draining was provoked but not saved.

## HttpConnectionManager capture

- real-listeners-hcm.json  GET /config_dump?resource=static_listeners

Genuine capture from `envoyproxy/envoy:v1.31-latest` (version
`f30db0a84189cf3e681687a4a2fa1788062592d0/1.31.10/Clean/RELEASE/BoringSSL`), saved
as-is. It is made from `capture/bootstrap-hcm.yaml`: one static listener `static_hcm`
(0.0.0.0:10080) whose chain `http` holds an HttpConnectionManager with an inline
`route_config` and `http_filters` `envoy.filters.http.cors` and
`envoy.filters.http.router`. Mount the bootstrap at `/etc/envoy/envoy.yaml`, run
`envoy -c /etc/envoy/envoy.yaml`, and fetch `/config_dump?resource=static_listeners`
from the admin port (9901). The HCM `typed_config` carries
`@type: type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager`
and `http_filters[].name`, which is what the Listener Overview's HTTP filter tree reads.

## Secrets captures

Genuine captures from `envoyproxy/envoy:v1.31-latest`, version string
`f30db0a84189cf3e681687a4a2fa1788062592d0/1.31.10/Clean/RELEASE/BoringSSL`, saved
as-is. The certificate and key are throwaway (CN=envoyadmin-capture); Envoy redacts
the key in every dump.

- real-secrets-dynamic.json  GET /config_dump?resource=dynamic_active_secrets
- real-secrets-static.json   GET /config_dump?resource=static_secrets
- real-certs.json            GET /certs

Inputs are `capture/bootstrap-sds.yaml` (static secret `bootstrap-cert`, listener
`tls_listener` using SDS secret `server-cert`), `capture/sds.yaml` (file-based SDS,
version "12") and `capture/server.crt`. Mount `capture/` at `/etc/envoy`:

    docker run -d --name envoy-cap -v "$PWD/internal/admin/testdata/capture:/etc/envoy" -p 9901:9901 envoyproxy/envoy:v1.31-latest -c /etc/envoy/bootstrap-sds.yaml
    curl -s 'localhost:9901/config_dump?resource=dynamic_active_secrets' > real-secrets-dynamic.json
    curl -s 'localhost:9901/config_dump?resource=static_secrets' > real-secrets-static.json
    curl -s localhost:9901/certs > real-certs.json
    docker rm -f envoy-cap

### How /certs identifies an SDS secret

It does not: `/certs` carries no secret name. The `server-cert` entry (the only one
listed; `bootstrap-cert` is used by no TLS context, so it is absent) is
`"path": "\u003cinline\u003e"` (the literal string `<inline>`) inside `cert_chain[]`,
next to `serial_number`, `subject_alt_names`, `days_until_expiration`, `valid_from`
and `expiration_time`. An inline SDS cert is identifiable only by `path == "<inline>"`
(shared with any other inline cert) and by matching serial number or SANs, never by
name. A file-based cert would show its file path instead. The body is
`{"certificates":[{"ca_cert":[],"cert_chain":[...]}]}`.

### Secret config_dump shape notes

- With `?resource=`, `configs[]` holds one element per secret, `@type`
  `type.googleapis.com/envoy.admin.v3.SecretsConfigDump.DynamicSecret` or `.StaticSecret`.
- Each element has a top-level `name`; dynamic ones also have `version_info` and
  `last_updated` (e.g. `2026-10-14T11:52:22.479Z`); static ones have neither.
- `secret` is the Secret proto with its own `@type` and `name`, plus one kind field
  (`tls_certificate`). The private key is `"[redacted]"`, but the certificate chain
  PEM is included, so the decoder still drops the body.

`real-certs.json` is kept as evidence of why `/certs` is not used: it names an SDS certificate `<inline>`, so it cannot be joined to a secret by name.

## Route config fixture

- real-routes-dynamic.json  GET /config_dump?resource=dynamic_route_configs&name_regex=<name>

Taken from a live internal proxy (one route config, one virtual host `*`), then sanitised:
the repo is public, so every hostname, `:authority` pattern, cluster name, route name,
path, API product id, policy value, issuer and token was replaced with an invented value
(example.com hosts, `Demo*` products). The structure is as Envoy emitted it
(`DynamicRouteConfig`, `metadata.filter_metadata`, `typed_per_filter_config`, a
`direct_response` route). The live dump had no `:authority` header matcher, so one route was
duplicated by hand and the two copies given different `:authority` `safe_regex` headers on
the same exact path; that pair is synthetic.
