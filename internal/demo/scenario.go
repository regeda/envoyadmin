// Package demo is a fake Envoy admin API used by tests and by `envoyadmin --demo`.
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
	Secrets []string // names of secrets referenced through SDS
}

// Chain is one filter chain of a Listener.
type Chain struct {
	Name    string
	Match   map[string]any // filter_chain_match as Envoy emits it
	Filters []string       // short names, e.g. "tcp_proxy"
	// HTTPFilters are full http filter names emitted under the
	// http_connection_manager filter's typed_config (ignored for other filters).
	HTTPFilters []string
	TLS         bool
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
	Secrets    []string // names of secrets referenced through SDS by the first chain
}

// DemoCert describes the certificate inside a demo secret. DaysLeft is
// relative to Scenario.Updated and negative means already expired.
type DemoCert struct {
	Serial   int64
	SANs     []string
	DaysLeft int
}

// Secret is one entry of Envoy's secrets dump. State is "active", "warming" or
// "static".
type Secret struct {
	Name, Version, Kind, State string
	Cert                       *DemoCert
}

type Scenario struct {
	Envoy     string
	Updated   time.Time
	Clusters  []Cluster
	Listeners []Listener
	Secrets   []Secret
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

// ConfigDumpJSON returns the dump for "dynamic_active_clusters",
// "static_clusters", "dynamic_listeners" or "static_listeners" in the shape Envoy uses for ?resource=: one configs[]
// element per cluster, typed ClustersConfigDump.DynamicCluster or .StaticCluster.
func (s Scenario) ConfigDumpJSON(resource string) ([]byte, bool) {
	if b, ok := s.SecretsJSON(resource); ok {
		return b, true
	}
	switch resource {
	case "dynamic_listeners":
		return mustJSON(map[string]any{"configs": s.listenerElements(false)}), true
	case "static_listeners":
		return mustJSON(map[string]any{"configs": s.listenerElements(true)}), true
	case "dynamic_active_clusters", "static_clusters":
	default:
		return nil, false
	}
	configs := []any{}
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
		if len(c.Secrets) > 0 {
			cl["transport_socket"] = sdsContext(c.Secrets)
		}
		e := map[string]any{
			"@type":        "type.googleapis.com/envoy.admin.v3.ClustersConfigDump.DynamicCluster",
			"cluster":      cl,
			"last_updated": s.Updated.Format(time.RFC3339Nano),
		}
		if c.Static {
			e["@type"] = "type.googleapis.com/envoy.admin.v3.ClustersConfigDump.StaticCluster"
		} else {
			e["version_info"] = c.Version
		}
		configs = append(configs, e)
	}
	return mustJSON(map[string]any{"configs": configs}), true
}

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
		nf := map[string]any{"name": "envoy.filters.network." + f}
		if f == "tcp_proxy" {
			nf["typed_config"] = map[string]any{
				"@type":       "type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy",
				"stat_prefix": "demo",
			}
		}
		if f == "http_connection_manager" {
			httpFilters := []any{}
			for _, h := range c.HTTPFilters {
				httpFilters = append(httpFilters, map[string]any{
					"name":         h,
					"typed_config": map[string]any{"@type": "type.googleapis.com/" + httpFilterProto(h)},
				})
			}
			nf["typed_config"] = map[string]any{
				"@type":        "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
				"stat_prefix":  "demo",
				"http_filters": httpFilters,
			}
		}
		filters = append(filters, nf)
	}
	out["filters"] = filters
	if c.TLS {
		out["transport_socket"] = map[string]any{"name": "envoy.transport_sockets.tls"}
	}
	return out
}

// httpFilterProto is a plausible proto type for a demo http filter's typed_config.
func httpFilterProto(name string) string {
	switch name {
	case "envoy.filters.http.router":
		return "envoy.extensions.filters.http.router.v3.Router"
	case "envoy.filters.http.cors":
		return "envoy.extensions.filters.http.cors.v3.Cors"
	}
	return "udpa.type.v1.TypedStruct"
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
	if len(l.Secrets) > 0 && len(chains) > 0 {
		chains[0].(map[string]any)["transport_socket"] = sdsContext(l.Secrets)
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

var (
	hcm = []string{"http_connection_manager"}
	tcp = []string{"tcp_proxy"}
)

// The http filters of an Istio-style sidecar and of a plain edge listener; a
// fresh slice per call so scenarios never share backing arrays.
func sidecarHTTP() []string {
	return []string{"envoy.filters.http.cors", "istio.stats", "envoy.filters.http.router"}
}

func routerHTTP() []string { return []string{"envoy.filters.http.router"} }

func healthyListeners() []Listener {
	return []Listener{
		{Name: "0.0.0.0_8080", Address: "0.0.0.0:8080", Version: "42", State: "active",
			Chains: []Chain{{Filters: hcm, HTTPFilters: routerHTTP()}}, Secrets: []string{"server-cert"}},
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
					Filters: hcm, HTTPFilters: sidecarHTTP(), TLS: true},
				{Name: "by-sni", Match: map[string]any{"server_names": []string{"a.example.com", "b.example.com"}}, Filters: hcm, HTTPFilters: sidecarHTTP()},
			},
			Default: &Chain{Filters: tcp}},
		{Name: "0.0.0.0_8080", Address: "0.0.0.0:8080", Version: "7", State: "active", Chains: []Chain{{Filters: hcm, HTTPFilters: routerHTTP()}},
			Secrets: []string{"server-cert"}},
		{Name: "orders-egress", Address: "10.0.2.1:5432", Version: "8", State: "warming", ServingOld: true, Chains: []Chain{{Filters: tcp}}},
		{Name: "legacy-9090", Address: "0.0.0.0:9090", Version: "6", State: "draining", Chains: []Chain{{Filters: tcp}}},
		{Name: "bad-update", Address: "0.0.0.0:9443", Version: "9", State: "error", ServingOld: true,
			Error:  "error adding listener '0.0.0.0:9443': unknown filter 'envoy.filters.network.nope'\nwhile processing LDS update version 9",
			Chains: []Chain{{Filters: tcp}}},
		{Name: "admin-static", Address: "127.0.0.1:15000", Static: true, Chains: []Chain{{Filters: tcp}}},
		{Name: "uds-agent", Address: "unix:/var/run/agent.sock", Static: true, Chains: []Chain{{Filters: tcp}}},
	}
}

func base(clusters ...Cluster) Scenario {
	return Scenario{Envoy: "deadbeef/1.31.0/Clean/RELEASE/BoringSSL", Updated: epoch, Clusters: clusters,
		Listeners: healthyListeners()}
}

func demoSecrets() []Secret {
	return []Secret{
		{Name: "server-cert", Version: "12", Kind: "tls_certificate", State: "active",
			Cert: &DemoCert{Serial: 0x0a1b2c, SANs: []string{"api.example.com", "internal.example.com"}, DaysLeft: 62}},
		// Envoy reports a rotating secret twice under one name: the active
		// version and the warming one.
		{Name: "server-cert", Version: "13", Kind: "tls_certificate", State: "warming"},
		{Name: "mesh-ca", Version: "3", Kind: "validation_context", State: "active",
			Cert: &DemoCert{Serial: 1, DaysLeft: 900}},
		{Name: "rotating-cert", Version: "8", Kind: "tls_certificate", State: "warming"},
		{Name: "bootstrap-cert", Kind: "tls_certificate", State: "static",
			Cert: &DemoCert{Serial: 0xff00, DaysLeft: -3}},
	}
}

func sdsContext(names []string) map[string]any {
	cfgs := make([]any, 0, len(names))
	for _, n := range names {
		cfgs = append(cfgs, map[string]any{"name": n, "sds_config": map[string]any{"path_config_source": map[string]any{"path": "/etc/envoy/sds.yaml"}}})
	}
	return map[string]any{
		"name": "envoy.transport_sockets.tls",
		"typed_config": map[string]any{
			"@type":              "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext",
			"common_tls_context": map[string]any{"tls_certificate_sds_secret_configs": cfgs},
		},
	}
}

// SecretsJSON returns the dump for ?resource=dynamic_active_secrets,
// dynamic_warming_secrets or static_secrets, one configs[] element per secret
// as Envoy emits it. Like Envoy, it keeps the public certificate and redacts
// the private key.
func (s Scenario) SecretsJSON(resource string) ([]byte, bool) {
	var want, typ string
	switch resource {
	case "dynamic_active_secrets":
		want, typ = "active", "DynamicSecret"
	case "dynamic_warming_secrets":
		want, typ = "warming", "DynamicSecret"
	case "static_secrets":
		want, typ = "static", "StaticSecret"
	default:
		return nil, false
	}
	configs := []any{}
	for _, sec := range s.Secrets {
		if sec.State != want {
			continue
		}
		body := map[string]any{"name": sec.Name}
		var pemText string
		if sec.Cert != nil {
			pemText = certPEM(sec.Cert.Serial, sec.Cert.SANs, s.Updated.AddDate(0, 0, sec.Cert.DaysLeft))
		}
		switch sec.Kind {
		case "validation_context":
			ca := map[string]any{}
			if pemText != "" {
				ca["inline_string"] = pemText
			}
			body["validation_context"] = map[string]any{"trusted_ca": ca}
		default:
			chain := map[string]any{}
			if pemText != "" {
				chain["inline_string"] = pemText
			}
			body["tls_certificate"] = map[string]any{
				"certificate_chain": chain,
				"private_key":       map[string]any{"inline_string": "[redacted]"},
			}
		}
		e := map[string]any{
			"@type":        "type.googleapis.com/envoy.admin.v3.SecretsConfigDump." + typ,
			"name":         sec.Name,
			"last_updated": s.Updated.Format(time.RFC3339Nano),
			"secret":       body,
		}
		if want != "static" {
			e["version_info"] = sec.Version
		}
		configs = append(configs, e)
	}
	return mustJSON(map[string]any{"configs": configs}), true
}

func Healthy() Scenario {
	sc := base(
		Cluster{Name: "api-gateway", Type: "EDS", Version: "42", Hosts: []Host{
			ok("10.0.1.11:8080", "us-east-1a", 1, 2, 5), ok("10.0.1.12:8080", "us-east-1b", 1, 1, 4),
		}},
		Cluster{Name: "search", Type: "EDS", Version: "42", Hosts: []Host{ok("10.0.3.31:9200", "us-east-1a", 1, 0, 1)}},
		Cluster{Name: "xds_cluster", Static: true, Hosts: []Host{ok("127.0.0.1:18000", "", 1, 0, 1)}},
	)
	sc.Secrets = demoSecrets()
	return sc
}

// Degraded mixes failed health checks, outlier ejection, an empty cluster, a
// static cluster and a unix-socket host with no stats or locality.
func Degraded() Scenario {
	api := Cluster{Name: "api-gateway", Type: "EDS", Version: "42", Hosts: []Host{
		ok("10.0.1.11:8080", "us-east-1a", 1, 2, 5),
		{Address: "10.0.1.12:8080", Zone: "us-east-1b", Weight: 1, RqActive: 0, RqError: 17, CxActive: 0,
			Health: map[string]any{"failed_active_health_check": true, "eds_health_status": "UNHEALTHY"}},
		ok("10.0.1.13:8080", "us-east-1c", 1, 1, 3),
	}, Secrets: []string{"server-cert", "mesh-ca"}}
	db := Cluster{Name: "orders-db", Type: "STRICT_DNS", Version: "41", Hosts: []Host{
		{Address: "10.0.2.21:5432", Zone: "us-east-1a", Weight: 1, RqError: 4, Health: map[string]any{"failed_outlier_check": true}},
		{Address: "10.0.2.22:5432", Zone: "us-east-1b", Weight: 1, RqError: 9, Health: map[string]any{"failed_outlier_check": true}},
	}, Secrets: []string{"mesh-ca", "missing-cert"}}
	sc := base(
		api, db,
		Cluster{Name: "cache-redis", Type: "EDS", Version: "40"},
		Cluster{Name: "search", Type: "EDS", Version: "42", Hosts: []Host{
			ok("10.0.3.31:9200", "us-east-1a", 1, 0, 1), ok("10.0.3.32:9200", "us-east-1b", 2, 3, 2),
		}},
		Cluster{Name: "xds_cluster", Static: true, Hosts: []Host{ok("127.0.0.1:18000", "", 1, 0, 1)}},
		Cluster{Name: "local-agent", Static: true, Hosts: []Host{{Address: "unix:/var/run/agent.sock", NoStats: true}}},
	)
	sc.Listeners = degradedListeners()
	sc.Secrets = demoSecrets()
	return sc
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
