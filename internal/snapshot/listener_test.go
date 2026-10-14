package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/regeda/txenvoy/internal/admin"
	"github.com/regeda/txenvoy/internal/demo"
)

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("committed capture missing: %v", err)
	}
	return b
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func st(version, listener string) *admin.ListenerState {
	return &admin.ListenerState{VersionInfo: version, LastUpdated: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC), Listener: raw(listener)}
}

func TestListenerStatePrecedence(t *testing.T) {
	l := `{"name":"x","address":{"socket_address":{"address":"1.2.3.4","port_value":80}}}`
	tests := []struct {
		name       string
		lc         admin.ListenerConfig
		wantState  ListenerStatus
		wantText   string
		wantVer    string
		servingOld bool
	}{
		{"active", admin.ListenerConfig{Name: "x", Active: st("7", l)}, ListenerActive, "active", "7", false},
		{"static", admin.ListenerConfig{Name: "x", Static: true, Active: st("", l)}, ListenerActive, "static", "", false},
		{"warming with old", admin.ListenerConfig{Name: "x", Active: st("6", l), Warming: st("7", l)}, ListenerWarming, "warming", "6", false},
		{"warming only", admin.ListenerConfig{Name: "x", Warming: st("7", l)}, ListenerWarming, "warming", "7", false},
		{"draining beats warming", admin.ListenerConfig{Name: "x", Warming: st("8", l), Draining: st("5", l)}, ListenerDraining, "draining", "8", false},
		{"error beats all", admin.ListenerConfig{Name: "x", Active: st("3", l), Draining: st("2", l),
			Error: &admin.ListenerError{Details: "boom", Listener: raw(l)}}, ListenerError, "error", "3", true},
		{"error never worked", admin.ListenerConfig{Name: "x", Error: &admin.ListenerError{Details: "nope",
			LastUpdateAttempt: time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC), Listener: raw(l)}}, ListenerError, "error", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := listenerFrom(tt.lc)
			if got.State != tt.wantState || got.StateText() != tt.wantText || got.VersionInfo != tt.wantVer || got.ServingOld != tt.servingOld {
				t.Fatalf("state=%v text=%q ver=%q servingOld=%v", got.State, got.StateText(), got.VersionInfo, got.ServingOld)
			}
			if got.Address != "1.2.3.4:80" {
				t.Fatalf("address %q", got.Address)
			}
			if text, _ := got.Config.Render(); !strings.Contains(text, "name: x") {
				t.Fatalf("the Config tab shows the shown listener: %q", text)
			}
		})
	}
	e := listenerFrom(admin.ListenerConfig{Name: "x", Error: &admin.ListenerError{Details: "nope\nline2\n",
		LastUpdateAttempt: time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC), Listener: raw(l)}})
	if e.ErrorDetails != "nope\nline2" || e.LastUpdated.Hour() != 11 {
		t.Fatalf("error-only listeners use the attempt time: %+v", e)
	}
}

func TestListenerAddressFormats(t *testing.T) {
	cases := map[string]string{
		`{"address":{"socket_address":{"address":"0.0.0.0","port_value":8080}}}`:  "0.0.0.0:8080",
		`{"address":{"socket_address":{"address":"::1","port_value":443}}}`:       "[::1]:443",
		`{"address":{"socket_address":{"address":"0.0.0.0"}}}`:                    "0.0.0.0:0",
		`{"address":{"pipe":{"path":"/var/run/a.sock"}}}`:                         "unix:/var/run/a.sock",
		`{"address":{"envoy_internal_address":{"server_listener_name":"inner"}}}`: "internal:inner",
		`{"address":{}}`: "",
		`{}`:             "",
		`not json`:       "",
	}
	for in, want := range cases {
		if got, _ := parseListener(raw(in)); got != want {
			t.Errorf("%s -> %q, want %q", in, got, want)
		}
	}
}

func TestFilterChainParsing(t *testing.T) {
	_, chains := parseListener(raw(`{
	 "filter_chains":[
	  {"name":"by-sni","filter_chain_match":{"server_names":["a.com","b.com"],"destination_port":443,
	     "prefix_ranges":[{"address_prefix":"10.0.0.0","prefix_len":8}],
	     "source_prefix_ranges":[{"address_prefix":"192.168.0.0","prefix_len":16}],
	     "source_ports":[1,2],"source_type":"SAME_IP_OR_LOOPBACK","transport_protocol":"tls",
	     "application_protocols":["h2","http/1.1"]},
	   "filters":[{"name":"envoy.filters.network.http_connection_manager"},{"name":"istio.stats"}],
	   "transport_socket":{"name":"envoy.transport_sockets.tls"}},
	  {"filters":[{"name":"envoy.filters.network.tcp_proxy"}]},
	  {"filter_chain_match":{},"filters":[]}
	 ],
	 "default_filter_chain":{"name":"ignored-name","filters":[{"name":"envoy.filters.network.tcp_proxy"}]}}`))
	if len(chains) != 4 {
		t.Fatalf("got %d chains: %+v", len(chains), chains)
	}
	a := chains[0]
	wantMatch := "sni=a.com,b.com port=443 dst=10.0.0.0/8 src=192.168.0.0/16 src_port=1,2 src_type=same_ip_or_loopback transport=tls alpn=h2,http/1.1"
	if a.Label != "by-sni" || strings.Join(a.Match, " ") != wantMatch {
		t.Fatalf("chain 1: %q / %q", a.Label, strings.Join(a.Match, " "))
	}
	if strings.Join(a.Filters, ",") != "http_connection_manager,istio.stats" || !a.TLS {
		t.Fatalf("filters %v tls=%v (the envoy.filters.network. prefix is stripped, others kept)", a.Filters, a.TLS)
	}
	if b := chains[1]; b.Label != "#2" || len(b.Match) != 0 || b.TLS || strings.Join(b.Filters, ",") != "tcp_proxy" {
		t.Fatalf("an unnamed chain is #N with no match: %+v", b)
	}
	if c := chains[2]; c.Label != "#3" || len(c.Match) != 0 || len(c.Filters) != 0 {
		t.Fatalf("an empty match is no match: %+v", c)
	}
	if d := chains[3]; d.Label != "default" {
		t.Fatalf("default_filter_chain is labelled default: %+v", d)
	}
	if _, none := parseListener(raw(`{"address":{}}`)); len(none) != 0 {
		t.Fatal("no filter_chains means no chains")
	}
	// source_type ANY is not shown
	_, c := parseListener(raw(`{"filter_chains":[{"filter_chain_match":{"source_type":"ANY"}}]}`))
	if len(c) != 1 || len(c[0].Match) != 0 {
		t.Fatalf("ANY source type is omitted: %+v", c)
	}
}

func TestListenerSortOrder(t *testing.T) {
	l := `{"name":"x"}`
	cfgs := []admin.ListenerConfig{
		{Name: "z-active", Active: st("1", l)},
		{Name: "a-active", Active: st("1", l)},
		{Name: "m-warming", Warming: st("2", l)},
		{Name: "n-draining", Draining: st("2", l)},
		{Name: "e-error", Error: &admin.ListenerError{Listener: raw(l)}},
		{Name: "s-static", Static: true, Active: st("", l)},
	}
	var ls []Listener
	for _, c := range cfgs {
		ls = append(ls, listenerFrom(c))
	}
	sortListeners(ls)
	var order []string
	for _, l := range ls {
		order = append(order, l.Name)
	}
	if got := strings.Join(order, " "); got != "e-error n-draining m-warming a-active s-static z-active" {
		t.Fatalf("order = %s", got)
	}
}

func TestNoAddressNoChainsDoesNotPanic(t *testing.T) {
	l := listenerFrom(admin.ListenerConfig{Name: "bare", Active: st("1", `{"name":"bare"}`)})
	if l.Address != "" || len(l.Chains) != 0 {
		t.Fatalf("%+v", l)
	}
	if got := listenerFrom(admin.ListenerConfig{Name: "nolistener", Active: &admin.ListenerState{VersionInfo: "1"}}); got.Address != "" {
		t.Fatalf("a state without a listener: %+v", got)
	}
}

func TestFetchListenersFromDemo(t *testing.T) {
	s := fetchFrom(t, demo.Degraded())
	if s.ListenersErr != "" || len(s.Listeners) != 7 {
		t.Fatalf("err=%q listeners=%d", s.ListenersErr, len(s.Listeners))
	}
	by := map[string]Listener{}
	for _, l := range s.Listeners {
		by[l.Name] = l
	}
	if l := by["bad-update"]; l.State != ListenerError || !l.ServingOld || !strings.Contains(l.ErrorDetails, "unknown filter") {
		t.Fatalf("bad-update: %+v", l)
	}
	vi := by["virtualInbound"]
	if vi.State != ListenerActive || vi.Address != "0.0.0.0:15006" || vi.VersionInfo != "7" || len(vi.Chains) != 3 {
		t.Fatalf("virtualInbound: %+v", vi)
	}
	if vi.Chains[0].Label != "#1" || !vi.Chains[0].TLS || vi.Chains[1].Label != "by-sni" || vi.Chains[2].Label != "default" {
		t.Fatalf("chains: %+v", vi.Chains)
	}
	if l := by["uds-agent"]; !l.Static || l.Address != "unix:/var/run/agent.sock" || l.StateText() != "static" {
		t.Fatalf("uds-agent: %+v", l)
	}
	if l := by["orders-egress"]; l.State != ListenerWarming || l.VersionInfo != "prev" {
		t.Fatalf("a warming update shows the serving version: %+v", l)
	}
	if got := s.ListenersNotActive(); got != 3 {
		t.Fatalf("not active (warming, draining, error) = %d, want 3", got)
	}
	// ordering: error, draining, warming, then active/static by name
	var order []string
	for _, l := range s.Listeners {
		order = append(order, l.Name)
	}
	if got := strings.Join(order, " "); got != "bad-update legacy-9090 orders-egress 0.0.0.0_8080 admin-static uds-agent virtualInbound" {
		t.Fatalf("order = %s", got)
	}
	// nothing was converted to YAML during Fetch
	for _, l := range s.Listeners {
		if l.Config.rendered() {
			t.Fatalf("%s converted during Fetch", l.Name)
		}
	}
}

func TestListenerDumpFailureIsIsolated(t *testing.T) {
	srv := httptest.NewServer(demo.NewServer(demo.Degraded()))
	defer srv.Close()
	c, _ := admin.NewHTTP(srv.URL, time.Second)

	s, err := Fetch(context.Background(), stub{Client: c, listenersErr: errors.New("boom")})
	if err != nil {
		t.Fatalf("a listener failure must not fail the snapshot: %v", err)
	}
	if s.ListenersErr != "listeners unavailable: boom" || len(s.Listeners) != 0 || len(s.Clusters) == 0 {
		t.Fatalf("err=%q listeners=%d clusters=%d", s.ListenersErr, len(s.Listeners), len(s.Clusters))
	}

	// the same through real HTTP: only the listener dump is failed
	d := demo.NewServer(demo.Degraded())
	h := httptest.NewServer(d)
	defer h.Close()
	d.Inject(demo.Fault{Status: http.StatusServiceUnavailable, Match: "dynamic_listeners"})
	c2, _ := admin.NewHTTP(h.URL, time.Second)
	s, err = Fetch(context.Background(), c2)
	if err != nil || !strings.HasPrefix(s.ListenersErr, "listeners unavailable: ") || !strings.Contains(s.ListenersErr, "503") || len(s.Clusters) == 0 {
		t.Fatalf("err=%v listenersErr=%q clusters=%d", err, s.ListenersErr, len(s.Clusters))
	}
}

func TestEmptyListenerDumps(t *testing.T) {
	s := fetchFrom(t, demo.Scenario{Envoy: "x/1.0.0/Clean", Updated: time.Now()})
	if len(s.Listeners) != 0 || s.ListenersErr != "" || s.ListenersNotActive() != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestRealRejectedUpdateThroughFetch(t *testing.T) {
	dyn, err := os.ReadFile("../admin/testdata/real-listeners-error.json")
	if err != nil {
		t.Fatalf("committed capture missing: %v", err)
	}
	clusters := mustRead(t, "../admin/testdata/real-clusters.json")
	cdyn := mustRead(t, "../admin/testdata/real-config-dynamic.json")
	cstat := mustRead(t, "../admin/testdata/real-config-static.json")
	info := mustRead(t, "../admin/testdata/real-server-info.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/clusters":
			_, _ = w.Write(clusters)
		case r.URL.Path == "/server_info":
			_, _ = w.Write(info)
		case r.URL.Query().Get("resource") == "dynamic_listeners":
			_, _ = w.Write(dyn)
		case r.URL.Query().Get("resource") == "static_listeners":
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Query().Get("resource") == "dynamic_active_clusters":
			_, _ = w.Write(cdyn)
		default:
			_, _ = w.Write(cstat)
		}
	}))
	defer srv.Close()
	c, _ := admin.NewHTTP(srv.URL, time.Second)
	s, err := Fetch(context.Background(), c)
	if err != nil || len(s.Listeners) != 1 {
		t.Fatalf("%v listeners=%d: Envoy's two elements are one listener", err, len(s.Listeners))
	}
	l := s.Listeners[0]
	if l.Name != "dynamic_one" || l.State != ListenerError || !l.ServingOld || l.VersionInfo != "7" ||
		!strings.Contains(l.ErrorDetails, "cluster_specifier") || strings.HasSuffix(l.ErrorDetails, "\n") {
		t.Fatalf("%+v", l)
	}
	if text, _ := l.Config.Render(); !strings.Contains(text, "name: dynamic_one") {
		t.Fatalf("the Config tab shows the serving (old) listener:\n%s", text)
	}
}

func TestRealListenerCapturesThroughFetch(t *testing.T) {
	dyn, err1 := os.ReadFile("../admin/testdata/real-listeners-dynamic.json")
	stat, err2 := os.ReadFile("../admin/testdata/real-listeners-static.json")
	if err1 != nil || err2 != nil {
		t.Fatalf("committed captures missing: %v %v", err1, err2)
	}
	clusters := mustRead(t, "../admin/testdata/real-clusters.json")
	cdyn := mustRead(t, "../admin/testdata/real-config-dynamic.json")
	cstat := mustRead(t, "../admin/testdata/real-config-static.json")
	info := mustRead(t, "../admin/testdata/real-server-info.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/clusters":
			_, _ = w.Write(clusters)
		case r.URL.Path == "/server_info":
			_, _ = w.Write(info)
		case r.URL.Query().Get("resource") == "dynamic_listeners":
			_, _ = w.Write(dyn)
		case r.URL.Query().Get("resource") == "static_listeners":
			_, _ = w.Write(stat)
		case r.URL.Query().Get("resource") == "dynamic_active_clusters":
			_, _ = w.Write(cdyn)
		default:
			_, _ = w.Write(cstat)
		}
	}))
	defer srv.Close()
	c, _ := admin.NewHTTP(srv.URL, time.Second)
	s, err := Fetch(context.Background(), c)
	if err != nil || s.ListenersErr != "" {
		t.Fatalf("%v %q", err, s.ListenersErr)
	}
	by := map[string]Listener{}
	for _, l := range s.Listeners {
		by[l.Name] = l
	}
	m := by["static_multi"]
	if !m.Static || m.Address != "0.0.0.0:10002" || len(m.Chains) != 3 || m.Chains[0].Label != "by-sni" ||
		!strings.Contains(strings.Join(m.Chains[0].Match, " "), "sni=a.example.com,b.example.com") || m.Chains[2].Label != "default" {
		t.Fatalf("static_multi from a real capture: %+v", m)
	}
	if d := by["dynamic_one"]; d.Static || d.State != ListenerActive || d.VersionInfo == "" || d.Address != "0.0.0.0:10003" {
		t.Fatalf("dynamic_one: %+v", d)
	}
}

func TestNullListenerJSONIsEmpty(t *testing.T) {
	for _, js := range []string{"null", ""} {
		lc := admin.ListenerConfig{Name: "n", Active: &admin.ListenerState{VersionInfo: "1", Listener: raw(js)}}
		if js == "" {
			lc.Active.Listener = nil
		}
		l := listenerFrom(lc)
		if l.Address != "" || len(l.Chains) != 0 {
			t.Fatalf("%q: %+v", js, l)
		}
		if text, problem := l.Config.Render(); text != "" || problem != "" {
			t.Fatalf("%q: Config renders (%q, %q), want empty", js, text, problem)
		}
	}
	// a rejected-only listener with a null body
	l := listenerFrom(admin.ListenerConfig{Name: "n", Error: &admin.ListenerError{Details: "x", Listener: raw("null")}})
	if text, problem := l.Config.Render(); text != "" || problem != "" {
		t.Fatalf("error-only null: (%q, %q)", text, problem)
	}
}

func TestManyUnnamedChains(t *testing.T) {
	_, chains := parseListener(raw(`{"filter_chains":[{},{},{},{}]}`))
	if len(chains) != 4 || chains[3].Label != "#4" {
		t.Fatalf("%+v", chains)
	}
}

func TestListenerPortTolerance(t *testing.T) {
	cases := map[string]string{
		`{"address":{"socket_address":{"address":"1.2.3.4","port_value":"abc"}}}`:  "1.2.3.4:0",
		`{"address":{"socket_address":{"address":"1.2.3.4","port_value":"8080"}}}`: "1.2.3.4:8080",
		`{"address":{"socket_address":{"address":"1.2.3.4","port_value":null}}}`:   "1.2.3.4:0",
	}
	for in, want := range cases {
		if got, _ := parseListener(raw(in)); got != want {
			t.Errorf("%s -> %q, want %q", in, got, want)
		}
	}
}
