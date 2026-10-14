package snapshot

import (
	"context"
	"errors"
	"net/http/httptest"
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
