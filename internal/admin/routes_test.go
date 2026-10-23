package admin

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRouteNameRegex(t *testing.T) {
	if got, want := routeNameRegex([]string{"a.b+c", "x"}), `(?:a\.b\+c|x)`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := routeNameRegex([]string{"x"}), `(?:x)`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDecodeRouteConfigs_Dynamic(t *testing.T) {
	body, err := os.ReadFile("testdata/real-routes-dynamic.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeRouteConfigs(body, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d configs, want 1", len(got))
	}
	rc := got[0]
	if rc.Name != "port-8444-example-gateway" {
		t.Errorf("Name = %q", rc.Name)
	}
	if rc.VersionInfo == "" || rc.Static || rc.LastUpdated.IsZero() {
		t.Errorf("version/static/updated wrong: %+v", rc)
	}
	if !strings.Contains(string(rc.JSON), `"virtual_hosts"`) {
		t.Errorf("JSON lacks virtual_hosts: %.80s", rc.JSON)
	}
}

func TestDecodeRouteConfigs_StaticAndMalformed(t *testing.T) {
	const body = `{"configs":[
 {"@type":"type.googleapis.com/envoy.admin.v3.RoutesConfigDump.StaticRouteConfig","last_updated":"2026-10-14T09:00:00Z",
  "route_config":{"name":"s1","virtual_hosts":[]}},
 {"@type":"type.googleapis.com/envoy.admin.v3.RoutesConfigDump.StaticRouteConfig","route_config":{"virtual_hosts":[]}},
 {"@type":"type.googleapis.com/envoy.admin.v3.RoutesConfigDump.StaticRouteConfig"},
 {"@type":"type.googleapis.com/envoy.admin.v3.RoutesConfigDump","static_route_configs":[{"route_config":{"name":"s2"}}],
  "dynamic_route_configs":[{"version_info":"7","route_config":{"name":"d1"}}]},
 "garbage"
]}`
	got, err := decodeRouteConfigs([]byte(body), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "s1" || got[1].Name != "s2" || !got[0].Static || !got[1].Static {
		t.Fatalf("static decode wrong: %+v", got)
	}
	if got[0].LastUpdated.IsZero() {
		t.Error("LastUpdated not parsed")
	}
	dyn, err := decodeRouteConfigs([]byte(body), false)
	if err != nil {
		t.Fatal(err)
	}
	// Per-resource elements describe themselves; only the wrapped dump is filtered by static.
	if len(dyn) != 2 || dyn[0].Name != "s1" || !dyn[0].Static || dyn[1].Name != "d1" || dyn[1].VersionInfo != "7" || dyn[1].Static {
		t.Errorf("dynamic wrapped decode wrong: %+v", dyn)
	}
	empty, err := decodeRouteConfigs([]byte(`{}`), false)
	if err != nil || len(empty) != 0 {
		t.Errorf("empty body: %v %v", empty, err)
	}
}

func TestRouteConfigsRequests(t *testing.T) {
	var mu sync.Mutex
	var seen []url.Values
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Query())
		mu.Unlock()
		if r.URL.Path != "/config_dump" {
			http.Error(w, "bad path", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"configs":[]}`))
	}), time.Second)

	if got, err := c.RouteConfigs(context.Background(), nil); got != nil || err != nil {
		t.Fatalf("nil names: %v %v", got, err)
	}
	if len(seen) != 0 {
		t.Fatalf("requests for empty names: %v", seen)
	}
	if _, err := c.RouteConfigs(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("got %d requests, want 2", len(seen))
	}
	for i, res := range []string{"dynamic_route_configs", "static_route_configs"} {
		if seen[i].Get("resource") != res || seen[i].Get("name_regex") != "(?:a|b)" {
			t.Errorf("request %d = %v", i, seen[i])
		}
	}

	bad := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusServiceUnavailable)
	}), time.Second)
	_, err := bad.RouteConfigs(context.Background(), []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "dynamic_route_configs") {
		t.Errorf("error should name the resource: %v", err)
	}
}
