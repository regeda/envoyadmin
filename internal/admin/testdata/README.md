Genuine captures from Envoy `envoyproxy/envoy:v1.31-latest`, version string
`f30db0a84189cf3e681687a4a2fa1788062592d0/1.31.10/Clean/RELEASE/BoringSSL`,
taken from a container with a static bootstrap (clusters api, search, empty) plus a
file-based CDS (orders STRICT_DNS, agg aggregate). Unedited apart from the
response bodies being saved as-is; nothing host-specific is present.

- real-clusters.json      GET /clusters?format=json
- real-config-dynamic.json GET /config_dump?resource=dynamic_active_clusters
- real-config-static.json  GET /config_dump?resource=static_clusters
- real-server-info.json    GET /server_info
