// Package demo is a fake Envoy admin API used by tests and by `txenvoy --demo`.
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
}

type Scenario struct {
	Envoy    string
	Updated  time.Time
	Clusters []Cluster
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

// ConfigDumpJSON returns the dump for "dynamic_active_clusters" or "static_clusters".
func (s Scenario) ConfigDumpJSON(resource string) ([]byte, bool) {
	if resource != "dynamic_active_clusters" && resource != "static_clusters" {
		return nil, false
	}
	entries := []any{}
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
		e := map[string]any{"cluster": cl, "last_updated": s.Updated.Format(time.RFC3339Nano)}
		if !c.Static {
			e["version_info"] = c.Version
		}
		entries = append(entries, e)
	}
	return mustJSON(map[string]any{"configs": []any{map[string]any{
		"@type":  "type.googleapis.com/envoy.admin.v3.ClustersConfigDump",
		resource: entries,
	}}}), true
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

func base(clusters ...Cluster) Scenario {
	return Scenario{Envoy: "deadbeef/1.31.0/Clean/RELEASE/BoringSSL", Updated: epoch, Clusters: clusters}
}

func Healthy() Scenario {
	return base(
		Cluster{Name: "api-gateway", Type: "EDS", Version: "42", Hosts: []Host{
			ok("10.0.1.11:8080", "us-east-1a", 1, 2, 5), ok("10.0.1.12:8080", "us-east-1b", 1, 1, 4),
		}},
		Cluster{Name: "search", Type: "EDS", Version: "42", Hosts: []Host{ok("10.0.3.31:9200", "us-east-1a", 1, 0, 1)}},
		Cluster{Name: "xds_cluster", Static: true, Hosts: []Host{ok("127.0.0.1:18000", "", 1, 0, 1)}},
	)
}

// Degraded mixes failed health checks, outlier ejection, an empty cluster, a
// static cluster and a unix-socket host with no stats or locality.
func Degraded() Scenario {
	api := Cluster{Name: "api-gateway", Type: "EDS", Version: "42", Hosts: []Host{
		ok("10.0.1.11:8080", "us-east-1a", 1, 2, 5),
		{Address: "10.0.1.12:8080", Zone: "us-east-1b", Weight: 1, RqActive: 0, RqError: 17, CxActive: 0,
			Health: map[string]any{"failed_active_health_check": true, "eds_health_status": "UNHEALTHY"}},
		ok("10.0.1.13:8080", "us-east-1c", 1, 1, 3),
	}}
	db := Cluster{Name: "orders-db", Type: "STRICT_DNS", Version: "41", Hosts: []Host{
		{Address: "10.0.2.21:5432", Zone: "us-east-1a", Weight: 1, RqError: 4, Health: map[string]any{"failed_outlier_check": true}},
		{Address: "10.0.2.22:5432", Zone: "us-east-1b", Weight: 1, RqError: 9, Health: map[string]any{"failed_outlier_check": true}},
	}}
	return base(
		api, db,
		Cluster{Name: "cache-redis", Type: "EDS", Version: "40"},
		Cluster{Name: "search", Type: "EDS", Version: "42", Hosts: []Host{
			ok("10.0.3.31:9200", "us-east-1a", 1, 0, 1), ok("10.0.3.32:9200", "us-east-1b", 2, 3, 2),
		}},
		Cluster{Name: "xds_cluster", Static: true, Hosts: []Host{ok("127.0.0.1:18000", "", 1, 0, 1)}},
		Cluster{Name: "local-agent", Static: true, Hosts: []Host{{Address: "unix:/var/run/agent.sock", NoStats: true}}},
	)
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
