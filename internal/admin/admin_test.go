package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A hand-written payload in the shape real Envoy emits. It deliberately does
// not come from internal/demo, so the demo builders cannot share a
// misconception with the decoder.
const realClusters = `{"cluster_statuses":[{"name":"api","added_via_api":true,"host_statuses":[
 {"address":{"socket_address":{"address":"10.0.0.1","port_value":8080}},
  "stats":[{"name":"cx_active","value":"3","type":"GAUGE"},{"name":"rq_error","value":"7"}],
  "health_status":{"failed_active_health_check":true,"eds_health_status":"UNHEALTHY"},
  "weight":2,"locality":{"zone":"us-east-1a"},"some_future_field":1}]}]}`

const realConfigDynamic = `{"configs":[
 {"@type":"type.googleapis.com/envoy.admin.v3.BootstrapConfigDump","bootstrap":{}},
 {"@type":"type.googleapis.com/envoy.admin.v3.ClustersConfigDump","version_info":"x",
  "dynamic_active_clusters":[
   {"version_info":"42","last_updated":"2026-10-14T10:00:00.123Z",
    "cluster":{"@type":"type.googleapis.com/envoy.config.cluster.v3.Cluster","name":"api","type":"EDS",
      "transport_socket":{"name":"x","typed_config":{"@type":"type.googleapis.com/acme.Unknown","foo":1}}}},
   {"version_info":"7","cluster":{"@type":"type.googleapis.com/envoy.config.cluster.v3.Cluster","name":"agg",
      "cluster_type":{"name":"envoy.clusters.aggregate"}}}]}]}`

const realConfigStatic = `{"configs":[{"@type":"type.googleapis.com/envoy.admin.v3.ClustersConfigDump",
 "static_clusters":[{"cluster":{"@type":"type.googleapis.com/envoy.config.cluster.v3.Cluster","name":"xds_cluster"},
   "last_updated":"2026-10-14T09:00:00Z"}]}]}`

func newClient(t *testing.T, h http.Handler, timeout time.Duration) *HTTP {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewHTTP(srv.URL, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClusters(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/clusters" || r.URL.Query().Get("format") != "json" {
			http.Error(w, "bad request "+r.URL.String(), 400)
			return
		}
		w.Write([]byte(realClusters))
	}), time.Second)

	got, err := c.Clusters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h := got.GetClusterStatuses()[0].GetHostStatuses()[0]
	if got.GetClusterStatuses()[0].GetName() != "api" || h.GetWeight() != 2 ||
		h.GetAddress().GetSocketAddress().GetPortValue() != 8080 ||
		h.GetLocality().GetZone() != "us-east-1a" ||
		!h.GetHealthStatus().GetFailedActiveHealthCheck() ||
		h.GetStats()[0].GetName() != "cx_active" || h.GetStats()[0].GetValue() != 3 {
		t.Fatalf("decoded wrongly: %v", got)
	}
}

func TestClusterConfigsMergesDynamicAndStatic(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("resource") {
		case "dynamic_active_clusters":
			w.Write([]byte(realConfigDynamic))
		case "static_clusters":
			w.Write([]byte(realConfigStatic))
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
	if len(by) != 3 {
		t.Fatalf("got %d configs: %+v", len(by), got)
	}
	api := by["api"]
	if api.Type != "EDS" || api.VersionInfo != "42" || api.Static ||
		api.LastUpdated.Format(time.RFC3339Nano) != "2026-10-14T10:00:00.123Z" {
		t.Fatalf("api: %+v", api)
	}
	if !strings.Contains(string(api.JSON), "acme.Unknown") {
		t.Fatal("unknown nested Any type must survive in the raw JSON, not fail the decode")
	}
	if by["agg"].Type != "envoy.clusters.aggregate" {
		t.Fatalf("custom cluster_type: %+v", by["agg"])
	}
	if x := by["xds_cluster"]; !x.Static || x.Type != "STATIC" || x.VersionInfo != "" {
		t.Fatalf("static: %+v", x)
	}
}

func TestServerInfo(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"abc/1.31.0/Clean/RELEASE","state":"LIVE","unknown":true}`))
	}), time.Second)
	info, err := c.ServerInfo(context.Background())
	if err != nil || info.GetVersion() != "abc/1.31.0/Clean/RELEASE" {
		t.Fatalf("%v %v", info, err)
	}
}

func TestErrors(t *testing.T) {
	t.Run("non-2xx", func(t *testing.T) {
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 503) }), time.Second)
		_, err := c.Clusters(context.Background())
		if err == nil || !strings.Contains(err.Error(), "GET /clusters") || !strings.Contains(err.Error(), "503") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{nope")) }), time.Second)
		if _, err := c.Clusters(context.Background()); err == nil || !strings.Contains(err.Error(), "GET /clusters") {
			t.Fatalf("got %v", err)
		}
		if _, err := c.ClusterConfigs(context.Background()); err == nil {
			t.Fatal("expected config decode error")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(2 * time.Second):
			case <-r.Context().Done():
			}
		}), 50*time.Millisecond)
		start := time.Now()
		if _, err := c.Clusters(context.Background()); err == nil {
			t.Fatal("expected timeout")
		}
		if time.Since(start) > time.Second {
			t.Fatal("timeout not applied")
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(realClusters)) }), time.Second)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := c.Clusters(ctx); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("bad base url", func(t *testing.T) {
		if _, err := NewHTTP("::nope", time.Second); err == nil {
			t.Fatal("expected error")
		}
	})
}
