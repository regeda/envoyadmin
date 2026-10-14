package demo

import (
	"context"
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

	sc := Healthy()
	sc.Clusters[0].Hosts[0].Health = map[string]any{"a": true}
	origZone := sc.Clusters[0].Hosts[0].Zone
	s.SetScenario(sc)
	s.Update(func(x *Scenario) {
		x.Clusters[0].Hosts[0].Health["a"] = false
		x.Clusters[0].Hosts[0].Zone = "z"
	})
	if sc.Clusters[0].Hosts[0].Health["a"] != true || sc.Clusters[0].Hosts[0].Zone != origZone {
		t.Fatal("Update must not mutate shared builder data")
	}
	_, body = get(t, srv.URL+"/clusters?format=json")
	if !strings.Contains(body, `"a":false`) || !strings.Contains(body, `"zone":"z"`) {
		t.Fatal("update not visible")
	}
}

func TestConcurrentUpdateAndServe(t *testing.T) {
	s := NewServer(Healthy())
	srv := httptest.NewServer(s)
	defer srv.Close()

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			s.Update(func(x *Scenario) {
				x.Clusters[0].Hosts[0].Health = map[string]any{"n": i}
			})
		}
	}()
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if code, _ := get(t, srv.URL+"/clusters?format=json"); code != 200 {
			t.Fatalf("status %d", code)
		}
	}
	close(stop)
	<-done
}

func TestJitterTogglesHealth(t *testing.T) {
	s := NewServer(Degraded())
	srv := httptest.NewServer(s)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, before := get(t, srv.URL+"/clusters?format=json")
	go s.Jitter(ctx, 10*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, now := get(t, srv.URL+"/clusters?format=json"); now != before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("jitter never changed the cluster health")
}
