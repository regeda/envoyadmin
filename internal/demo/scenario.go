// Package demo is a fake Envoy admin API used by tests and by `envoyadmin --demo`.
// It emits the JSON shape Envoy produces (snake_case protojson, uint64 as strings).
package demo

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Host struct {
	Health   map[string]any
	Address  string
	Zone     string
	RqActive uint64
	RqError  uint64
	CxActive uint64
	Weight   uint32
	NoStats  bool
}

type Cluster struct {
	Name    string
	Type    string
	Version string
	Hosts   []Host
	Secrets []string
	Static  bool
}

// Chain is one filter chain of a Listener.
type Chain struct {
	Match       map[string]any
	Name        string
	RouteConfig string
	Filters     []string
	HTTPFilters []string
	TLS         bool
}

type Listener struct {
	Default    *Chain
	Name       string
	Address    string
	Version    string
	State      string
	Error      string
	Chains     []Chain
	Secrets    []string
	Static     bool
	ServingOld bool
}

// DemoCert describes the certificate inside a demo secret. DaysLeft is
// relative to Scenario.Updated and negative means already expired.
type DemoCert struct {
	SANs     []string
	Serial   int64
	DaysLeft int
}

// Secret is one entry of Envoy's secrets dump. State is "active", "warming" or
// "static".
type Secret struct {
	Cert    *DemoCert
	Name    string
	Version string
	Kind    string
	State   string
}

// RouteConfig is one dynamic route configuration; VirtualHosts are emitted as
// the route_config's virtual_hosts.
type RouteConfig struct {
	Name, Version string
	VirtualHosts  []map[string]any
}

type Scenario struct {
	Envoy     string
	Updated   time.Time
	Clusters  []Cluster
	Listeners []Listener
	Secrets   []Secret
	// RouteConfigs are served as dynamic_route_configs; static_route_configs is always empty.
	RouteConfigs []RouteConfig
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

// addressJSON renders a "host:port" or "unix:path" address as Envoy does.
func addressJSON(address string) map[string]any {
	if path, ok := strings.CutPrefix(address, "unix:"); ok {
		return map[string]any{"pipe": map[string]any{"path": path}}
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		host, port = "", "0"
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		n = 0
	}
	return map[string]any{"socket_address": map[string]any{"address": host, "port_value": n}}
}

func (h Host) json() map[string]any {
	addr := addressJSON(h.Address)
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
	if b, ok := s.RouteConfigsJSON(resource, ""); ok {
		return b, true
	}
	switch resource {
	case "dynamic_listeners":
		return mustJSON(map[string]any{"configs": s.listenerElements(false)}), true
	case "static_listeners":
		return mustJSON(map[string]any{"configs": s.listenerElements(true)}), true
	case "dynamic_active_clusters", "static_clusters":
		return mustJSON(map[string]any{"configs": s.clusterElements(resource == "static_clusters")}), true
	}
	return nil, false
}

// clusterElements returns one configs[] element per static or dynamic cluster.
func (s Scenario) clusterElements(static bool) []any {
	configs := []any{}
	for _, c := range s.Clusters {
		if c.Static != static {
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
	return configs
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
			tc := map[string]any{
				"@type":        "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
				"stat_prefix":  "demo",
				"http_filters": httpFilters,
			}
			if c.RouteConfig != "" {
				tc["rds"] = map[string]any{
					"config_source":     map[string]any{"ads": map[string]any{}},
					"route_config_name": c.RouteConfig,
				}
			}
			nf["typed_config"] = tc
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
	addr := addressJSON(l.Address)
	chains := []map[string]any{}
	for _, c := range l.Chains {
		chains = append(chains, c.json())
	}
	if len(l.Secrets) > 0 && len(chains) > 0 {
		chains[0]["transport_socket"] = sdsContext(l.Secrets)
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
		switch {
		case l.Static != static:
		case static:
			out = append(out, map[string]any{
				"@type":        "type.googleapis.com/envoy.admin.v3.ListenersConfigDump.StaticListener",
				"listener":     l.json(),
				"last_updated": s.Updated.Format(time.RFC3339Nano),
			})
		default:
			out = append(out, s.dynamicListenerElements(l)...)
		}
	}
	return out
}

// withServingOld adds the previous version's active_state to e when l still serves it.
func (s Scenario) withServingOld(e map[string]any, l Listener) {
	if l.ServingOld {
		old := l
		old.Version = "prev"
		e["active_state"] = s.listenerState(old)
	}
}

// dynamicListenerElements returns the configs[] elements of one dynamic listener.
func (s Scenario) dynamicListenerElements(l Listener) []any {
	e := map[string]any{
		"@type": "type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener",
		"name":  l.Name,
	}
	switch l.State {
	case "warming":
		e["warming_state"] = s.listenerState(l)
		s.withServingOld(e, l)
	case "draining":
		e["draining_state"] = s.listenerState(l)
	case "error":
		e["error_state"] = map[string]any{
			"failed_configuration": l.json(),
			"last_update_attempt":  s.Updated.Format(time.RFC3339Nano),
			"details":              l.Error + "\n", // Envoy ends details with a newline
		}
		s.withServingOld(e, l)
		// Envoy also emits a second, nameless element holding only the error.
		return []any{e, map[string]any{
			"@type": "type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener",
			"error_state": map[string]any{
				"failed_configuration": l.json(),
				"details":              l.Error + "\n",
			},
		}}
	default:
		e["active_state"] = s.listenerState(l)
	}
	return []any{e}
}

// RouteConfigsJSON returns the dump for ?resource=dynamic_route_configs or
// static_route_configs, one configs[] element per route config. A non-empty
// nameRegex keeps only the configs whose whole name it matches, like Envoy's
// name_regex; an invalid regex reports false.
func (s Scenario) RouteConfigsJSON(resource, nameRegex string) ([]byte, bool) {
	if resource != "dynamic_route_configs" && resource != "static_route_configs" {
		return nil, false
	}
	var re *regexp.Regexp
	if nameRegex != "" {
		var err error
		if re, err = regexp.Compile("^(?:" + nameRegex + ")$"); err != nil {
			return nil, false
		}
	}
	configs := []any{}
	if resource == "dynamic_route_configs" {
		for _, rc := range s.RouteConfigs {
			if re != nil && !re.MatchString(rc.Name) {
				continue
			}
			vhosts := rc.VirtualHosts
			if vhosts == nil {
				vhosts = []map[string]any{}
			}
			configs = append(configs, map[string]any{
				"@type":        "type.googleapis.com/envoy.admin.v3.RoutesConfigDump.DynamicRouteConfig",
				"version_info": rc.Version,
				"route_config": map[string]any{
					"@type":         "type.googleapis.com/envoy.config.route.v3.RouteConfiguration",
					"name":          rc.Name,
					"virtual_hosts": vhosts,
				},
				"last_updated": s.Updated.Format(time.RFC3339Nano),
			})
		}
	}
	return mustJSON(map[string]any{"configs": configs}), true
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
					Filters: hcm, HTTPFilters: sidecarHTTP(), TLS: true, RouteConfig: "inbound-routes"},
				{Name: "by-sni", Match: map[string]any{"server_names": []string{"a.example.com", "b.example.com"}}, Filters: hcm, HTTPFilters: sidecarHTTP()},
			},
			Default: &Chain{Filters: tcp}},
		{Name: "0.0.0.0_8080", Address: "0.0.0.0:8080", Version: "7", State: "active",
			Chains:  []Chain{{Filters: hcm, HTTPFilters: routerHTTP(), RouteConfig: "public-api"}},
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

func routeTo(name, cluster string, match map[string]any) map[string]any {
	r := map[string]any{"match": match, "route": map[string]any{"cluster": cluster}}
	if name != "" {
		r["name"] = name
	}
	return r
}

func authority(regex string) map[string]any {
	return map[string]any{"name": ":authority", "string_match": map[string]any{"safe_regex": map[string]any{"regex": regex}}}
}

// demoRouteConfigs gives every route search status something to show: two
// routes share one exact path and differ by an :authority regex, a few routes
// are named, and one route has a runtime_fraction match (not evaluated).
func demoRouteConfigs() []RouteConfig {
	return []RouteConfig{
		{Name: "public-api", Version: "21", VirtualHosts: []map[string]any{
			{"name": "api", "domains": []string{"api.example.com", "*.example.com"}, "routes": []any{
				routeTo("logon-a", "api-gateway", map[string]any{"path": "/v1/logon", "headers": []any{authority(`a\..*`)}}),
				routeTo("logon-b", "api-gateway", map[string]any{"path": "/v1/logon", "headers": []any{authority(`b\..*`)}}),
				routeTo("orders", "api-gateway", map[string]any{"prefix": "/v1/orders"}),
				routeTo("canary", "api-gateway", map[string]any{"prefix": "/v1/", "runtime_fraction": map[string]any{
					"default_value": map[string]any{"numerator": 10, "denominator": "HUNDRED"}}}),
				routeTo("", "api-gateway", map[string]any{"prefix": "/v1/"}),
				routeTo("api-default", "api-gateway", map[string]any{"prefix": "/"}),
			}},
			{"name": "fallback", "domains": []string{"*"}, "routes": []any{
				routeTo("search", "search", map[string]any{"prefix": "/search"}),
				routeTo("fallback-default", "api-gateway", map[string]any{"prefix": "/"}),
			}},
		}},
		{Name: "inbound-routes", Version: "7", VirtualHosts: []map[string]any{
			{"name": "inbound", "domains": []string{"a.example.com", "b.example.com"}, "routes": []any{
				routeTo("health", "local-agent", map[string]any{"path": "/healthz"}),
				routeTo("logon-inbound", "api-gateway", map[string]any{"prefix": "/v1/logon"}),
				routeTo("inbound-default", "api-gateway", map[string]any{"prefix": "/"}),
			}},
		}},
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
		if sec.State == want {
			configs = append(configs, s.secretElement(sec, typ, want != "static"))
		}
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
	sc.RouteConfigs = demoRouteConfigs()
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
	sc := base(cs...)
	for i := range sc.Listeners {
		sc.Listeners[i].Secrets = nil // Big serves no secrets for them to reference
	}
	return sc
}

// secretElement renders one secret as a configs[] element.
func (s Scenario) secretElement(sec Secret, typ string, versioned bool) map[string]any {
	var pemText string
	if sec.Cert != nil {
		pemText = certPEM(sec.Cert.Serial, sec.Cert.SANs, s.Updated.AddDate(0, 0, sec.Cert.DaysLeft))
	}
	body := map[string]any{"name": sec.Name}
	if sec.Kind == "validation_context" {
		ca := map[string]any{}
		if pemText != "" {
			ca["inline_string"] = pemText
		}
		body["validation_context"] = map[string]any{"trusted_ca": ca}
	} else {
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
	if versioned {
		e["version_info"] = sec.Version
	}
	return e
}
