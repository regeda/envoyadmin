package snapshot

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/regeda/envoyadmin/internal/admin"
)

type ListenerStatus int

const (
	ListenerActive ListenerStatus = iota
	ListenerWarming
	ListenerDraining
	ListenerError
)

func (s ListenerStatus) String() string {
	return [...]string{"active", "warming", "draining", "error"}[s]
}

// Filter is a network or HTTP filter: its full configured name and the short
// proto type of its typed_config ("TcpProxy"; empty without a typed_config).
type Filter struct {
	Name, Type string
}

type FilterChain struct {
	Label   string
	Match   []string
	Filters []Filter
	// HTTPFilters are the http_filters[] of the first HttpConnectionManager
	// filter in the chain.
	HTTPFilters []Filter
	// Routes is the route source of the first HttpConnectionManager: nil when
	// the chain has none or it names no route config.
	Routes *RouteRef
	TLS    bool
	hcm    bool // the chain has an HttpConnectionManager
	scoped bool // its HttpConnectionManager uses scoped_routes
}

type Listener struct {
	Name, Address string
	State         ListenerStatus
	Static        bool
	VersionInfo   string
	LastUpdated   time.Time
	ErrorDetails  string
	ServingOld    bool // a rejected (error) update with an older version still active
	Chains        []FilterChain
	Config        ConfigInfo
	Secrets       []SecretUse
}

// StateText is what the UI shows: "static" for static listeners.
func (l Listener) StateText() string {
	if l.Static && l.State == ListenerActive {
		return "static"
	}
	return l.State.String()
}

// listenerFrom turns one admin.ListenerConfig into a Listener. The shown
// listener (address, chains, Config) is the one serving: active, else warming,
// else draining; a listener that only ever failed shows the rejected config.
func listenerFrom(lc admin.ListenerConfig) Listener {
	l := Listener{Name: lc.Name, Static: lc.Static}
	var serving *admin.ListenerState
	for _, s := range []*admin.ListenerState{lc.Active, lc.Warming, lc.Draining} {
		if s != nil {
			serving = s
			break
		}
	}
	switch {
	case lc.Error != nil:
		l.State = ListenerError
		l.ErrorDetails = strings.TrimSpace(lc.Error.Details) // Envoy ends it with a newline
		l.ServingOld = lc.Active != nil
	case lc.Draining != nil && lc.Active == nil:
		// Envoy keeps reporting the old version as draining next to the new
		// active one for --drain-time-s after every update; that is healthy.
		l.State = ListenerDraining
	case lc.Warming != nil:
		l.State = ListenerWarming
	default:
		l.State = ListenerActive
	}

	var shown json.RawMessage
	if serving != nil {
		l.VersionInfo, l.LastUpdated, shown = serving.VersionInfo, serving.LastUpdated, serving.Listener
	} else if lc.Error != nil {
		l.LastUpdated, shown = lc.Error.LastUpdateAttempt, lc.Error.Listener
	}
	if string(shown) == "null" { // "listener": null is as empty as an absent body
		shown = nil
	}
	l.Address, l.Chains = parseListener(shown)
	l.Config = NewConfigInfo(shown)
	l.Config.VersionInfo, l.Config.LastUpdated, l.Config.Static = l.VersionInfo, l.LastUpdated, l.Static
	return l
}

func listenerSeverity(s ListenerStatus) int {
	switch s {
	case ListenerError:
		return 0
	case ListenerDraining:
		return 1
	case ListenerWarming:
		return 2
	default:
		return 3
	}
}

func sortListeners(ls []Listener) {
	sort.SliceStable(ls, func(i, j int) bool {
		si, sj := listenerSeverity(ls[i].State), listenerSeverity(ls[j].State)
		if si != sj {
			return si < sj
		}
		return ls[i].Name < ls[j].Name
	})
}

// parseListener extracts the bind address and filter chains from a listener's
// JSON, best effort: a malformed part yields blanks, never a panic.
func parseListener(raw json.RawMessage) (address string, chains []FilterChain) {
	var l struct {
		Address      json.RawMessage   `json:"address"`
		FilterChains []json.RawMessage `json:"filter_chains"`
		Default      json.RawMessage   `json:"default_filter_chain"`
	}
	_ = json.Unmarshal(raw, &l)
	for i, c := range l.FilterChains {
		chains = append(chains, parseChain(c, fmt.Sprintf("#%d", i+1)))
	}
	if len(l.Default) > 0 && string(l.Default) != "null" {
		chains = append(chains, parseChain(l.Default, "default"))
	}
	return parseAddress(l.Address), chains
}

func parseAddress(raw json.RawMessage) string {
	var a struct {
		Socket struct {
			Address string          `json:"address"`
			Port    json.RawMessage `json:"port_value"`
		} `json:"socket_address"`
		Pipe struct {
			Path string `json:"path"`
		} `json:"pipe"`
		Internal struct {
			Name string `json:"server_listener_name"`
		} `json:"envoy_internal_address"`
	}
	_ = json.Unmarshal(raw, &a)
	switch {
	case a.Socket.Address != "":
		port := numStr(a.Socket.Port)
		if port == "" {
			port = "0" // proto3 default, omitted by Envoy
		}
		return net.JoinHostPort(a.Socket.Address, port)
	case a.Pipe.Path != "":
		return "unix:" + a.Pipe.Path
	case a.Internal.Name != "":
		return "internal:" + a.Internal.Name
	}
	return ""
}

const (
	hcmTypeURL = "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager"
)

// shortType is the message name of a typed_config @type URL:
// "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router" -> "Router".
func shortType(typed json.RawMessage) string {
	var t struct {
		Type string `json:"@type"`
	}
	_ = json.Unmarshal(typed, &t)
	return t.Type[strings.LastIndexAny(t.Type, "/.")+1:]
}

// parseHTTPFilters returns the http_filters[] of an HttpConnectionManager
// typed_config and whether it is one (the exact @type decides, never the
// filter name). Best effort: wrong types yield fewer filters, never a panic.
func parseHTTPFilters(typed json.RawMessage) (filters []Filter, isHCM bool) {
	var tc struct {
		Type    string `json:"@type"`
		Filters []struct {
			Name        string          `json:"name"`
			TypedConfig json.RawMessage `json:"typed_config"`
		} `json:"http_filters"`
	}
	_ = json.Unmarshal(typed, &tc)
	if tc.Type != hcmTypeURL {
		return nil, false
	}
	for _, f := range tc.Filters {
		if f.Name != "" {
			filters = append(filters, Filter{Name: f.Name, Type: shortType(f.TypedConfig)})
		}
	}
	return filters, true
}

// parseRouteRef returns the route source of an HttpConnectionManager
// typed_config: rds.route_config_name, else an inline route_config. Nil for
// other types or when neither is set (the exact @type decides, as above).
// scoped reports an HttpConnectionManager using scoped_routes, which has no
// route source this tool reads.
func parseRouteRef(typed json.RawMessage) (ref *RouteRef, scoped bool) {
	var tc struct {
		Type string `json:"@type"`
		RDS  struct {
			Name string `json:"route_config_name"`
		} `json:"rds"`
		Inline json.RawMessage `json:"route_config"`
		Scoped json.RawMessage `json:"scoped_routes"`
	}
	_ = json.Unmarshal(typed, &tc)
	switch {
	case tc.Type != hcmTypeURL:
		return nil, false
	case tc.RDS.Name != "":
		return &RouteRef{Name: tc.RDS.Name}, false
	case len(tc.Inline) > 0 && string(tc.Inline) != "null":
		return &RouteRef{Inline: tc.Inline}, false
	}
	return nil, len(tc.Scoped) > 0 && string(tc.Scoped) != "null"
}

func parseChain(raw json.RawMessage, label string) FilterChain {
	var c struct {
		Name    string                     `json:"name"`
		Match   map[string]json.RawMessage `json:"filter_chain_match"`
		Filters []struct {
			Name        string          `json:"name"`
			TypedConfig json.RawMessage `json:"typed_config"`
		} `json:"filters"`
		TransportSocket struct {
			Name string `json:"name"`
		} `json:"transport_socket"`
	}
	_ = json.Unmarshal(raw, &c)
	fc := FilterChain{Label: label, Match: matchParts(c.Match), TLS: strings.Contains(c.TransportSocket.Name, "tls")}
	if label != "default" && c.Name != "" {
		fc.Label = c.Name
	}
	seenHCM := false
	for _, f := range c.Filters {
		fc.Filters = append(fc.Filters, Filter{Name: f.Name, Type: shortType(f.TypedConfig)})
		if !seenHCM { // the first HttpConnectionManager is authoritative
			fc.HTTPFilters, seenHCM = parseHTTPFilters(f.TypedConfig)
			if seenHCM {
				fc.hcm = true
				fc.Routes, fc.scoped = parseRouteRef(f.TypedConfig)
			}
		}
	}
	return fc
}

// matchParts summarises filter_chain_match in a fixed order, only for set fields.
func matchParts(m map[string]json.RawMessage) []string {
	var parts []string
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	add("sni", strings.Join(strList(m["server_names"]), ","))
	add("port", numStr(m["destination_port"]))
	add("dst", strings.Join(cidrList(m["prefix_ranges"]), ","))
	add("src", strings.Join(cidrList(m["source_prefix_ranges"]), ","))
	add("src_port", strings.Join(numList(m["source_ports"]), ","))
	if st := strVal(m["source_type"]); st != "" && st != "ANY" {
		add("src_type", strings.ToLower(st))
	}
	add("transport", strVal(m["transport_protocol"]))
	add("alpn", strings.Join(strList(m["application_protocols"]), ","))
	return parts
}

func strVal(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func strList(raw json.RawMessage) []string {
	var s []string
	_ = json.Unmarshal(raw, &s)
	return s
}

func numStr(raw json.RawMessage) string {
	var n json.Number
	if json.Unmarshal(raw, &n) != nil {
		return ""
	}
	return n.String()
}

func numList(raw json.RawMessage) []string {
	var ns []json.Number
	_ = json.Unmarshal(raw, &ns)
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.String())
	}
	return out
}

func cidrList(raw json.RawMessage) []string {
	var cs []struct {
		Prefix string      `json:"address_prefix"`
		Len    json.Number `json:"prefix_len"`
	}
	_ = json.Unmarshal(raw, &cs)
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		if c.Prefix != "" {
			out = append(out, c.Prefix+"/"+c.Len.String())
		}
	}
	return out
}
