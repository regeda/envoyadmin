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
at `/etc/envoy/envoy.yaml` and `/etc/envoy/lds.yaml` and run Envoy with `-c`. The
error capture starts from that state and then atomically replaces the LDS file
(`mv` inside the container, from a mounted directory; host-side edits are not seen
through the Docker bind mount) with `capture/lds-bad.yaml`, a tcp_proxy with no
`cluster`, which Envoy rejects with a proto constraint error. An unknown filter
name with a valid `typed_config` is accepted, so it does not produce an error.

A draining state was also provoked (LDS update to an empty resource list) but is not
saved. Warming was not provoked. Warming and draining fixtures are hand-written
(see the demo scenario and Task 3 tests).
