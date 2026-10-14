package demo

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestEndpointsServeValidJSON(t *testing.T) {
	srv := httptest.NewServer(NewServer(Degraded()))
	defer srv.Close()

	code, body := get(t, srv.URL+"/clusters?format=json")
	if code != 200 {
		t.Fatalf("clusters: %d", code)
	}
	var clusters struct {
		ClusterStatuses []map[string]any `json:"cluster_statuses"`
	}
	if err := json.Unmarshal([]byte(body), &clusters); err != nil {
		t.Fatal(err)
	}
	if len(clusters.ClusterStatuses) != len(Degraded().Clusters) {
		t.Fatalf("got %d statuses", len(clusters.ClusterStatuses))
	}

	for _, res := range []string{"dynamic_active_clusters", "static_clusters"} {
		code, body = get(t, srv.URL+"/config_dump?resource="+res)
		if code != 200 || !strings.Contains(body, "ClustersConfigDump") || !strings.Contains(body, res) {
			t.Fatalf("%s: %d %s", res, code, body)
		}
	}
	if code, _ = get(t, srv.URL+"/config_dump?resource=bogus"); code != 400 {
		t.Fatalf("bogus resource: %d", code)
	}
	if code, body = get(t, srv.URL+"/server_info"); code != 200 || !strings.Contains(body, "1.31.0") {
		t.Fatalf("server_info: %d %s", code, body)
	}
	if code, _ = get(t, srv.URL+"/nope"); code != 404 {
		t.Fatalf("unknown path: %d", code)
	}
	if code, _ = get(t, srv.URL+"/clusters"); code != 400 {
		t.Fatalf("/clusters without format=json: %d", code)
	}
}

func TestStaticClustersAreSplitFromDynamic(t *testing.T) {
	s := Degraded()
	dyn, _ := s.ConfigDumpJSON("dynamic_active_clusters")
	stat, _ := s.ConfigDumpJSON("static_clusters")
	if strings.Contains(string(dyn), "xds_cluster") || !strings.Contains(string(stat), "xds_cluster") {
		t.Fatal("static cluster must only appear under static_clusters")
	}
}

func TestFaults(t *testing.T) {
	s := NewServer(Healthy())
	srv := httptest.NewServer(s)
	defer srv.Close()

	s.Inject(Fault{Status: 503})
	if code, _ := get(t, srv.URL+"/clusters?format=json"); code != 503 {
		t.Fatalf("status fault: %d", code)
	}
	s.Inject(Fault{Garbage: true})
	if _, body := get(t, srv.URL+"/clusters?format=json"); json.Valid([]byte(body)) {
		t.Fatal("garbage fault returned valid JSON")
	}
	s.Inject(Fault{Delay: 50 * time.Millisecond})
	start := time.Now()
	get(t, srv.URL+"/clusters?format=json")
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("delay fault did not delay")
	}
	s.Inject(Fault{})
	if code, _ := get(t, srv.URL+"/clusters?format=json"); code != 200 {
		t.Fatalf("cleared fault: %d", code)
	}
}

func TestSetScenarioAndUpdate(t *testing.T) {
	s := NewServer(Healthy())
	srv := httptest.NewServer(s)
	defer srv.Close()

	s.SetScenario(Big(300))
	_, body := get(t, srv.URL+"/clusters?format=json")
	if n := strings.Count(body, `"host_statuses"`); n != 300 {
		t.Fatalf("big scenario has %d clusters", n)
	}

	s.SetScenario(Healthy())
	s.Update(func(sc *Scenario) {
		sc.Clusters[0].Hosts[0].Health = map[string]any{"failed_active_health_check": true}
	})
	_, body = get(t, srv.URL+"/clusters?format=json")
	if !strings.Contains(body, "failed_active_health_check") {
		t.Fatal("update not visible")
	}
	if Healthy().Clusters[0].Hosts[0].Health != nil {
		t.Fatal("Update must not mutate shared builder data")
	}
}
