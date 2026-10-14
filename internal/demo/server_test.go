package demo

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
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
	defer func() { _ = resp.Body.Close() }()
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

	// Envoy emits one configs[] element per cluster, typed by the resource.
	for res, elem := range map[string]string{
		"dynamic_active_clusters": "ClustersConfigDump.DynamicCluster",
		"static_clusters":         "ClustersConfigDump.StaticCluster",
	} {
		code, body = get(t, srv.URL+"/config_dump?resource="+res)
		if code != 200 || !strings.Contains(body, elem) || strings.Contains(body, res) {
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

func TestListenerDumpsUsePerElementShape(t *testing.T) {
	s := Degraded()
	dyn, ok := s.ConfigDumpJSON("dynamic_listeners")
	if !ok {
		t.Fatal("dynamic_listeners must be supported")
	}
	stat, _ := s.ConfigDumpJSON("static_listeners")

	var d struct {
		Configs []map[string]any `json:"configs"`
	}
	if err := json.Unmarshal(dyn, &d); err != nil {
		t.Fatal(err)
	}
	names := map[string]map[string]any{}
	nameless := 0
	for _, c := range d.Configs {
		if !strings.HasSuffix(c["@type"].(string), "ListenersConfigDump.DynamicListener") {
			t.Fatalf("element type %v", c["@type"])
		}
		name, ok := c["name"].(string)
		if !ok {
			// Envoy's second element for a rejected update: error_state only
			if _, hasErr := c["error_state"]; !hasErr || len(c) != 2 {
				t.Fatalf("a nameless element holds only @type and error_state: %v", c)
			}
			nameless++
			continue
		}
		names[name] = c
	}
	if nameless != 1 {
		t.Fatalf("one rejected update emits exactly one nameless twin, got %d", nameless)
	}
	for _, want := range []string{"virtualInbound", "0.0.0.0_8080", "orders-egress", "legacy-9090", "bad-update"} {
		if names[want] == nil {
			t.Fatalf("dynamic dump is missing %s: %v", want, names)
		}
	}
	if _, ok := names["virtualInbound"]["active_state"]; !ok {
		t.Fatal("an active listener carries active_state")
	}
	if _, ok := names["orders-egress"]["warming_state"]; !ok {
		t.Fatal("a warming listener carries warming_state")
	}
	if _, ok := names["orders-egress"]["active_state"]; !ok {
		t.Fatal("a warming update with ServingOld also carries active_state")
	}
	if _, ok := names["legacy-9090"]["draining_state"]; !ok {
		t.Fatal("a draining listener carries draining_state")
	}
	es, ok := names["bad-update"]["error_state"].(map[string]any)
	if !ok || es["details"] == "" || es["failed_configuration"] == nil {
		t.Fatalf("error_state incomplete: %v", names["bad-update"])
	}
	if es["last_update_attempt"] == nil {
		t.Fatal("the named element carries last_update_attempt, as Envoy does")
	}
	if !strings.HasSuffix(es["details"].(string), "\n") {
		t.Fatal("details ends with a newline, as Envoy's do")
	}
	if _, ok := names["bad-update"]["active_state"]; !ok {
		t.Fatal("a rejected update with ServingOld keeps its active_state")
	}

	if !strings.Contains(string(stat), "StaticListener") || strings.Contains(string(stat), "DynamicListener") {
		t.Fatalf("static dump must hold only static listeners: %s", stat)
	}
	if strings.Contains(string(dyn), "admin-static") {
		t.Fatal("static listeners must not appear in the dynamic dump")
	}
	if _, ok := Degraded().ConfigDumpJSON("bogus_listeners"); ok {
		t.Fatal("unknown resource")
	}
}

func TestListenerJSONCarriesAddressAndChains(t *testing.T) {
	dyn, _ := Degraded().ConfigDumpJSON("dynamic_listeners")
	body := string(dyn)
	for _, want := range []string{
		`"port_value":15006`, `"server_names":["a.example.com","b.example.com"]`,
		`envoy.filters.network.http_connection_manager`, `"default_filter_chain"`,
		`envoy.transport_sockets.tls`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("listener JSON misses %s", want)
		}
	}
	stat, _ := Degraded().ConfigDumpJSON("static_listeners")
	if !strings.Contains(string(stat), `"path":"/var/run/agent.sock"`) {
		t.Fatalf("pipe address missing: %s", stat)
	}
}

func TestHCMChainsCarryTypedConfigWithHTTPFilters(t *testing.T) {
	dyn, _ := Degraded().ConfigDumpJSON("dynamic_listeners")
	var dump struct {
		Configs []struct {
			Name   string `json:"name"`
			Active *struct {
				Listener struct {
					Chains []struct {
						Filters []struct {
							Name        string `json:"name"`
							TypedConfig *struct {
								Type        string `json:"@type"`
								StatPrefix  string `json:"stat_prefix"`
								HTTPFilters []struct {
									Name        string            `json:"name"`
									TypedConfig map[string]string `json:"typed_config"`
								} `json:"http_filters"`
							} `json:"typed_config"`
						} `json:"filters"`
					} `json:"filter_chains"`
				} `json:"listener"`
			} `json:"active_state"`
		} `json:"configs"`
	}
	if err := json.Unmarshal(dyn, &dump); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range dump.Configs {
		if c.Name != "virtualInbound" {
			continue
		}
		found = true
		for i, ch := range c.Active.Listener.Chains {
			tc := ch.Filters[0].TypedConfig
			if tc == nil || tc.Type != "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager" || tc.StatPrefix == "" {
				t.Fatalf("chain %d: typed_config = %+v", i, tc)
			}
			var names []string
			for _, h := range tc.HTTPFilters {
				if h.TypedConfig["@type"] == "" {
					t.Fatalf("http filter %s has no typed_config @type", h.Name)
				}
				names = append(names, h.Name)
			}
			if got := strings.Join(names, ","); got != "envoy.filters.http.cors,istio.stats,envoy.filters.http.router" {
				t.Fatalf("chain %d http_filters = %s", i, got)
			}
		}
	}
	if !found {
		t.Fatal("virtualInbound missing")
	}
	// tcp_proxy carries a typed_config, but no http_filters
	if j := string(mustJSON(Chain{Filters: tcp}.json())); !strings.Contains(j, "TcpProxy") || strings.Contains(j, "http_filters") {
		t.Fatalf("tcp_proxy typed_config: %s", j)
	}
}

func TestFaultMatchScopesTheFault(t *testing.T) {
	s := NewServer(Degraded())
	srv := httptest.NewServer(s)
	defer srv.Close()

	s.Inject(Fault{Status: 503, Match: "dynamic_listeners"})
	if code, _ := get(t, srv.URL+"/config_dump?resource=dynamic_listeners"); code != 503 {
		t.Fatalf("matching request must fail, got %d", code)
	}
	for _, path := range []string{"/clusters?format=json", "/config_dump?resource=dynamic_active_clusters", "/server_info", "/config_dump?resource=static_listeners"} {
		if code, _ := get(t, srv.URL+path); code != 200 {
			t.Fatalf("%s must be unaffected, got %d", path, code)
		}
	}
	s.Inject(Fault{})
	if code, _ := get(t, srv.URL+"/config_dump?resource=dynamic_listeners"); code != 200 {
		t.Fatal("cleared fault")
	}
}

func TestScenarioListenerCounts(t *testing.T) {
	if n := len(Degraded().Listeners); n != 7 {
		t.Fatalf("Degraded has %d listeners, want 7", n)
	}
	if n := len(Healthy().Listeners); n != 3 {
		t.Fatalf("Healthy has %d listeners, want 3", n)
	}
}

func TestUpdateDeepCopiesListeners(t *testing.T) {
	sc := Degraded()
	s := NewServer(sc)
	origName := sc.Listeners[0].Name
	origChain := sc.Listeners[0].Chains[0].Match["transport_protocol"]
	origSNI := sc.Listeners[0].Chains[1].Match["server_names"].([]string)[0]
	origFilter := sc.Listeners[0].Chains[0].Filters[0]
	origDefault := sc.Listeners[0].Default.Filters[0]
	origDefaultName := sc.Listeners[0].Default.Name
	origHTTP := sc.Listeners[0].Chains[0].HTTPFilters[0]

	s.Update(func(x *Scenario) {
		l := &x.Listeners[0]
		l.Name = "mutated"
		l.Chains[0].Match["transport_protocol"] = "raw_buffer"
		l.Chains[1].Match["server_names"].([]string)[0] = "mutated"
		l.Chains[0].Filters[0] = "mutated"
		l.Default.Filters[0] = "mutated"
		l.Default.Name = "mutated"
		l.Chains[0].HTTPFilters[0] = "mutated"
	})

	l := sc.Listeners[0]
	if l.Name != origName || l.Chains[0].Match["transport_protocol"] != origChain ||
		l.Chains[1].Match["server_names"].([]string)[0] != origSNI ||
		l.Chains[0].Filters[0] != origFilter || l.Default.Filters[0] != origDefault ||
		l.Default.Name != origDefaultName || l.Chains[0].HTTPFilters[0] != origHTTP {
		t.Fatal("Update must not mutate the scenario passed to SetScenario/NewServer")
	}
	srv := httptest.NewServer(s)
	defer srv.Close()
	if _, body := get(t, srv.URL+"/config_dump?resource=dynamic_listeners"); !strings.Contains(body, "mutated") {
		t.Fatal("update not visible")
	}
}

func TestUpdateDeepCopiesSecrets(t *testing.T) {
	sc := Degraded()
	s := NewServer(sc)
	ci, li := -1, -1
	for i, c := range sc.Clusters {
		if ci < 0 && len(c.Secrets) > 0 {
			ci = i
		}
	}
	for i, l := range sc.Listeners {
		if li < 0 && len(l.Secrets) > 0 {
			li = i
		}
	}
	if ci < 0 || li < 0 {
		t.Fatal("fixture: expected a cluster and a listener that reference secrets")
	}
	origSecret := sc.Secrets[0]
	origCert := *sc.Secrets[0].Cert
	origSAN := sc.Secrets[0].Cert.SANs[0]
	origCluster := sc.Clusters[ci].Secrets[0]
	origListener := sc.Listeners[li].Secrets[0]

	s.Update(func(x *Scenario) {
		x.Secrets[0].Name = "mutated"
		x.Secrets[0].Cert.Serial = 99
		x.Secrets[0].Cert.DaysLeft = 1
		x.Secrets[0].Cert.SANs[0] = "mutated"
		x.Clusters[ci].Secrets[0] = "mutated"
		x.Listeners[li].Secrets[0] = "mutated"
	})

	if sc.Secrets[0].Name != origSecret.Name ||
		sc.Secrets[0].Cert.Serial != origCert.Serial || sc.Secrets[0].Cert.DaysLeft != origCert.DaysLeft ||
		sc.Secrets[0].Cert.SANs[0] != origSAN || sc.Clusters[ci].Secrets[0] != origCluster ||
		sc.Listeners[li].Secrets[0] != origListener {
		t.Fatal("Update must not mutate the secrets of the scenario passed to NewServer")
	}
}

func TestServerServesSecrets(t *testing.T) {
	srv := httptest.NewServer(NewServer(Degraded()))
	defer srv.Close()
	fetch := func(path string) string {
		code, b := get(t, srv.URL+path)
		if code != 200 {
			t.Fatalf("%s: %d", path, code)
		}
		return b
	}
	active := fetch("/config_dump?resource=dynamic_active_secrets")
	if !strings.Contains(active, "SecretsConfigDump.DynamicSecret") || !strings.Contains(active, "server-cert") ||
		!strings.Contains(active, "BEGIN CERTIFICATE") {
		t.Fatalf("active: %s", active)
	}
	if strings.Contains(active, "PRIVATE KEY") || !strings.Contains(active, "[redacted]") {
		t.Fatal("like Envoy, the demo redacts private keys and never emits one")
	}
	warming := fetch("/config_dump?resource=dynamic_warming_secrets")
	if !strings.Contains(warming, "rotating-cert") || !strings.Contains(warming, "server-cert") {
		t.Fatalf("warming: %s", warming)
	}
	if b := fetch("/config_dump?resource=static_secrets"); !strings.Contains(b, "SecretsConfigDump.StaticSecret") {
		t.Fatalf("static: %s", b)
	}
	if b := fetch("/config_dump?resource=dynamic_active_clusters"); !strings.Contains(b, "tls_certificate_sds_secret_configs") {
		t.Fatalf("a demo cluster must reference a secret: %s", b)
	}
}

func TestCertPEMRoundTrips(t *testing.T) {
	notAfter := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	block, _ := pem.Decode([]byte(certPEM(0x0a1b2c, []string{"api.example.com"}, notAfter)))
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("block: %+v", block)
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil || c.SerialNumber.Int64() != 0x0a1b2c || c.DNSNames[0] != "api.example.com" || !c.NotAfter.Equal(notAfter) {
		t.Fatalf("%v %+v", err, c)
	}
}
