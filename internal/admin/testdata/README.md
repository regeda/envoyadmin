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
