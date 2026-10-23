package admin

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// These tests decode genuine captures from Envoy 1.31.10 (see
// testdata/README.md), so a wrong assumption about Envoy's wire format cannot
// hide behind a hand-written fixture.

func capture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRealCaptureClusterConfigs(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("resource") {
		case "dynamic_active_clusters":
			writeBody(t, w, string(capture(t, "real-config-dynamic.json")))
		case "static_clusters":
			writeBody(t, w, string(capture(t, "real-config-static.json")))
		default:
			http.Error(w, "bad resource", 400)
		}
	}), time.Second)
	got, err := c.ClusterConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ClusterConfig{}
	for _, cc := range got {
		by[cc.Name] = cc
	}
	if len(by) != 5 {
		t.Fatalf("want 5 clusters (api, search, empty static; orders, agg dynamic), got %+v", got)
	}
	for _, n := range []string{"api", "search", "empty"} {
		if cc := by[n]; !cc.Static || cc.Type != "STATIC" || cc.LastUpdated.IsZero() || len(cc.JSON) == 0 {
			t.Fatalf("%s: %+v", n, cc)
		}
	}
	if cc := by["orders"]; cc.Static || cc.Type != "STRICT_DNS" || cc.VersionInfo != "7" || cc.LastUpdated.IsZero() {
		t.Fatalf("orders: %+v", cc)
	}
	if cc := by["agg"]; cc.Static || cc.Type != "envoy.clusters.aggregate" || cc.VersionInfo != "7" {
		t.Fatalf("agg: %+v", cc)
	}
}

func TestRealCaptureClustersAndServerInfo(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/clusters":
			writeBody(t, w, string(capture(t, "real-clusters.json")))
		case "/server_info":
			writeBody(t, w, string(capture(t, "real-server-info.json")))
		default:
			http.NotFound(w, r)
		}
	}), time.Second)
	cl, err := c.Clusters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hosts := map[string]int{}
	for _, st := range cl.GetClusterStatuses() {
		hosts[st.GetName()] = len(st.GetHostStatuses())
	}
	if len(hosts) != 5 || hosts["api"] != 2 || hosts["search"] != 1 || hosts["empty"] != 0 {
		t.Fatalf("hosts: %v", hosts)
	}
	weights := map[uint32]bool{}
	for _, st := range cl.GetClusterStatuses() {
		if st.GetName() != "api" {
			continue
		}
		for _, h := range st.GetHostStatuses() {
			weights[h.GetWeight()] = true
			if h.GetAddress().GetSocketAddress().GetPortValue() != 8080 {
				t.Fatalf("port: %v", h)
			}
		}
	}
	if !weights[1] || !weights[2] {
		t.Fatalf("api host weights: %v", weights)
	}
	info, err := c.ServerInfo(context.Background())
	if err != nil || !strings.Contains(info.GetVersion(), "/1.31.10/") {
		t.Fatalf("%v %v", info.GetVersion(), err)
	}
}
