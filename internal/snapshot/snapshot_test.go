package snapshot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	adminv3 "github.com/envoyproxy/go-control-plane/envoy/admin/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"

	"github.com/regeda/txenvoy/internal/admin"
	"github.com/regeda/txenvoy/internal/demo"
)

func fetchFrom(t *testing.T, sc demo.Scenario) Snapshot {
	t.Helper()
	srv := httptest.NewServer(demo.NewServer(sc))
	t.Cleanup(srv.Close)
	c, err := admin.NewHTTP(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := Fetch(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func byName(s Snapshot) map[string]Cluster {
	m := map[string]Cluster{}
	for _, c := range s.Clusters {
		m[c.Name] = c
	}
	return m
}

func TestFetchDegraded(t *testing.T) {
	s := fetchFrom(t, demo.Degraded())

	var order []string
	for _, c := range s.Clusters {
		order = append(order, c.Name)
	}
	want := "orders-db api-gateway cache-redis local-agent search xds_cluster"
	if strings.Join(order, " ") != want {
		t.Fatalf("order = %v, want %s", order, want)
	}
	if s.Version != "1.31.0" || s.FetchedAt.IsZero() {
		t.Fatalf("version=%q fetchedAt=%v", s.Version, s.FetchedAt)
	}
	if s.UnhealthyClusters() != 2 {
		t.Fatalf("unhealthy clusters = %d", s.UnhealthyClusters())
	}

	m := byName(s)
	api := m["api-gateway"]
	if api.Type != "EDS" || api.Health() != Degraded || api.HealthyCount() != 2 || len(api.Hosts) != 3 {
		t.Fatalf("api: %+v health=%v", api, api.Health())
	}
	if !strings.Contains(api.Config.YAML, "name: api-gateway") || api.Config.VersionInfo != "42" || api.Config.LastUpdated.IsZero() {
		t.Fatalf("api config: %+v", api.Config)
	}
	var bad Host
	for _, h := range api.Hosts {
		if !h.Healthy() {
			bad = h
		}
	}
	if strings.Join(bad.Flags, ",") != "FAILED_ACTIVE_HC,EDS_STATUS_UNHEALTHY" || bad.RqError != 17 {
		t.Fatalf("bad host: %+v", bad)
	}

	db := m["orders-db"]
	if db.Health() != Unhealthy || db.EjectedCount() != 2 || db.Type != "STRICT_DNS" {
		t.Fatalf("db: %+v", db)
	}
	if m["cache-redis"].Health() != Empty {
		t.Fatal("cache-redis should be Empty")
	}
	x := m["xds_cluster"]
	if x.Type != "STATIC" || !x.Config.Static || x.Config.VersionInfo != "" || x.Health() != Healthy {
		t.Fatalf("static: %+v", x)
	}
}

func TestHostWithPipeAddressAndNoStats(t *testing.T) {
	h := byName(fetchFrom(t, demo.Degraded()))["local-agent"].Hosts[0]
	if h.Address != "unix:/var/run/agent.sock" || h.Zone != "" || h.RqActive != 0 || h.CxActive != 0 || len(h.Flags) != 0 {
		t.Fatalf("pipe host: %+v", h)
	}
}

type stub struct {
	admin.Client
	clustersErr, cfgErr, infoErr error
}

func (s stub) Clusters(ctx context.Context) (*adminv3.Clusters, error) {
	if s.clustersErr != nil {
		return nil, s.clustersErr
	}
	return s.Client.Clusters(ctx)
}
func (s stub) ClusterConfigs(ctx context.Context) ([]admin.ClusterConfig, error) {
	if s.cfgErr != nil {
		return nil, s.cfgErr
	}
	return s.Client.ClusterConfigs(ctx)
}
func (s stub) ServerInfo(ctx context.Context) (*adminv3.ServerInfo, error) {
	if s.infoErr != nil {
		return nil, s.infoErr
	}
	return s.Client.ServerInfo(ctx)
}

func TestPartialFailures(t *testing.T) {
	srv := httptest.NewServer(demo.NewServer(demo.Healthy()))
	defer srv.Close()
	real, _ := admin.NewHTTP(srv.URL, time.Second)
	boom := errors.New("boom")

	s, err := Fetch(context.Background(), stub{Client: real, cfgErr: boom})
	if err != nil {
		t.Fatalf("config failure must not fail the snapshot: %v", err)
	}
	for _, c := range s.Clusters {
		if !strings.HasPrefix(c.Config.Err, "config unavailable") || c.Config.YAML != "" {
			t.Fatalf("%s: %+v", c.Name, c.Config)
		}
	}

	s, err = Fetch(context.Background(), stub{Client: real, infoErr: boom})
	if err != nil || s.Version != "" || len(s.Clusters) == 0 {
		t.Fatalf("server_info failure: %v %+v", err, s)
	}

	if _, err = Fetch(context.Background(), stub{Client: real, clustersErr: boom}); !errors.Is(err, boom) {
		t.Fatalf("clusters failure must fail: %v", err)
	}
}

func TestEmptyEnvoy(t *testing.T) {
	s := fetchFrom(t, demo.Scenario{Envoy: "x/1.0.0/Clean", Updated: time.Now()})
	if len(s.Clusters) != 0 || s.UnhealthyClusters() != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestFlagsOf(t *testing.T) {
	hs := &adminv3.HostHealthStatus{
		FailedActiveHealthCheck:    true,
		FailedOutlierCheck:         true,
		FailedActiveDegradedCheck:  true,
		PendingDynamicRemoval:      true,
		PendingActiveHc:            true,
		ExcludedViaImmediateHcFail: true,
		ActiveHcTimeout:            true,
		EdsHealthStatus:            corev3.HealthStatus_DRAINING,
	}
	got := strings.Join(flagsOf(hs), ",")
	want := "FAILED_ACTIVE_HC,FAILED_OUTLIER_CHECK,DEGRADED_ACTIVE_HC,PENDING_DYNAMIC_REMOVAL,PENDING_ACTIVE_HC,EXCLUDED_VIA_IMMEDIATE_HC_FAIL,ACTIVE_HC_TIMEOUT,EDS_STATUS_DRAINING"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if len(flagsOf(nil)) != 0 || len(flagsOf(&adminv3.HostHealthStatus{EdsHealthStatus: corev3.HealthStatus_HEALTHY})) != 0 {
		t.Fatal("healthy host must have no flags")
	}
	if !IsUnhealthyFlag("FAILED_OUTLIER_CHECK") || IsUnhealthyFlag("DEGRADED_ACTIVE_HC") || IsUnhealthyFlag("PENDING_ACTIVE_HC") {
		t.Fatal("IsUnhealthyFlag misclassifies")
	}
}

// envoyServer serves canned admin responses through the real HTTP client.
func envoyServer(t *testing.T, files map[string]string) *admin.HTTP {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if res := r.URL.Query().Get("resource"); res != "" {
			key += "?resource=" + res
		}
		body, ok := files[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := admin.NewHTTP(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func capturedFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../admin/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func checkRealShapeJoin(t *testing.T, files map[string]string) {
	t.Helper()
	snap, err := Fetch(context.Background(), envoyServer(t, files))
	if err != nil {
		t.Fatal(err)
	}
	m := byName(snap)
	for name, want := range map[string]struct {
		typ, version string
		static       bool
	}{
		"api":    {"STATIC", "", true},
		"orders": {"STRICT_DNS", "7", false},
		"agg":    {"envoy.clusters.aggregate", "7", false},
	} {
		c, ok := m[name]
		if !ok {
			t.Fatalf("%s missing from %v", name, m)
		}
		if c.Type == "?" || c.Type != want.typ || c.Config.Err != "" || c.Config.YAML == "" ||
			c.Config.VersionInfo != want.version || c.Config.Static != want.static ||
			!strings.Contains(c.Config.YAML, "name: "+name) || c.Config.LastUpdated.IsZero() {
			t.Fatalf("%s: type=%q config=%+v", name, c.Type, c.Config)
		}
	}
}

// Genuine Envoy 1.31.10 captures, served through the real client.
func TestFetchFromRealCaptures(t *testing.T) {
	checkRealShapeJoin(t, map[string]string{
		"/clusters": capturedFile(t, "real-clusters.json"),
		"/config_dump?resource=dynamic_active_clusters": capturedFile(t, "real-config-dynamic.json"),
		"/config_dump?resource=static_clusters":         capturedFile(t, "real-config-static.json"),
		"/server_info":                                  capturedFile(t, "real-server-info.json"),
	})
}

// Hand-written per-element shape (from Envoy's addResourceToDump behaviour,
// not captured), independent of internal/demo.
func TestFetchFromPerElementShape(t *testing.T) {
	const cl = `"cluster":{"@type":"type.googleapis.com/envoy.config.cluster.v3.Cluster",`
	const el = `"@type":"type.googleapis.com/envoy.admin.v3.ClustersConfigDump.`
	checkRealShapeJoin(t, map[string]string{
		"/clusters": `{"cluster_statuses":[{"name":"api"},{"name":"orders"},{"name":"agg"}]}`,
		"/config_dump?resource=dynamic_active_clusters": `{"configs":[
		 {` + el + `DynamicCluster","version_info":"7","last_updated":"2026-10-14T01:00:00Z",` + cl + `"name":"orders","type":"STRICT_DNS"}},
		 {` + el + `DynamicCluster","version_info":"7","last_updated":"2026-10-14T01:00:00Z",` + cl + `"name":"agg","cluster_type":{"name":"envoy.clusters.aggregate"}}}]}`,
		"/config_dump?resource=static_clusters": `{"configs":[
		 {` + el + `StaticCluster","last_updated":"2026-10-14T01:00:00Z",` + cl + `"name":"api"}}]}`,
	})
}

func TestClusterMissingFromConfigDump(t *testing.T) {
	snap, err := Fetch(context.Background(), envoyServer(t, map[string]string{
		"/clusters": `{"cluster_statuses":[{"name":"ghost"}]}`,
		"/config_dump?resource=dynamic_active_clusters": `{}`,
		"/config_dump?resource=static_clusters":         `{}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	g := byName(snap)["ghost"]
	if g.Type != "?" || g.Config.Err != "config unavailable: not present in config_dump" || g.Config.YAML != "" {
		t.Fatalf("%+v", g)
	}
}

type fixedConfigs struct {
	admin.Client
	cfgs []admin.ClusterConfig
}

func (f fixedConfigs) ClusterConfigs(context.Context) ([]admin.ClusterConfig, error) {
	return f.cfgs, nil
}

func TestYAMLRenderErrorIsReportedPerCluster(t *testing.T) {
	real := envoyServer(t, map[string]string{
		"/clusters": `{"cluster_statuses":[{"name":"bad"},{"name":"fine"}]}`,
	})
	snap, err := Fetch(context.Background(), fixedConfigs{Client: real, cfgs: []admin.ClusterConfig{
		{Name: "bad", Type: "EDS", JSON: []byte("{not json")},
		{Name: "fine", Type: "EDS", JSON: []byte(`{"name":"fine"}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	m := byName(snap)
	if b := m["bad"]; !strings.HasPrefix(b.Config.Err, "config unavailable: render yaml:") || b.Config.YAML != "" {
		t.Fatalf("bad: %+v", b.Config)
	}
	if f := m["fine"]; f.Config.Err != "" || !strings.Contains(f.Config.YAML, "name: fine") {
		t.Fatalf("fine: %+v", f.Config)
	}
}
