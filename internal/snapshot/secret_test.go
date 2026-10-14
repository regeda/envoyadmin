package snapshot

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/regeda/envoyadmin/internal/admin"
	"github.com/regeda/envoyadmin/internal/demo"
)

var secNow = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

func TestSecretsFromMapsCertFactsAndSorts(t *testing.T) {
	scs := []admin.SecretConfig{
		{Name: "b-ok", VersionInfo: "2", Kind: "tls_certificate", State: admin.SecretActive,
			Cert: &admin.CertFacts{Serial: "01", SANs: []string{"b.example.com"}, NotAfter: secNow.Add(50 * time.Hour)}},
		{Name: "a-warm", VersionInfo: "3", Kind: "tls_certificate", State: admin.SecretWarming},
		{Name: "c-expired", VersionInfo: "1", Kind: "tls_certificate", State: admin.SecretActive,
			Cert: &admin.CertFacts{Serial: "02", NotAfter: secNow.Add(-time.Hour)}},
		{Name: "d-static", Kind: "validation_context", State: admin.SecretStatic},
	}
	got := secretsFrom(scs, secNow)
	var order []string
	for _, s := range got {
		order = append(order, s.Name)
	}
	// warming first, then expired, then by name
	if want := []string{"a-warm", "c-expired", "b-ok", "d-static"}; len(order) != 4 ||
		order[0] != want[0] || order[1] != want[1] || order[2] != want[2] || order[3] != want[3] {
		t.Fatalf("order = %v, want %v", order, want)
	}
	by := map[string]Secret{}
	for _, s := range got {
		by[s.Name] = s
	}
	if c := by["b-ok"].Cert; c == nil || c.Serial != "01" || c.SANs[0] != "b.example.com" || c.DaysLeft != 2 || c.Expired(secNow) {
		t.Fatalf("b-ok cert: %+v", c)
	}
	if c := by["c-expired"].Cert; c == nil || !c.Expired(secNow) || c.DaysLeft > 0 {
		t.Fatalf("c-expired cert: %+v", c)
	}
	if by["a-warm"].Cert != nil || by["d-static"].Cert != nil {
		t.Fatal("a secret without readable certificate facts has no Cert")
	}
	if by["a-warm"].State != SecretWarming || by["d-static"].State != SecretStatic {
		t.Fatal("state mapping")
	}
}

func TestSecretsNeedAttention(t *testing.T) {
	s := Snapshot{FetchedAt: secNow, Secrets: []Secret{
		{Name: "ok", State: SecretActive},
		{Name: "warm", State: SecretWarming},
		{Name: "old", State: SecretActive, Cert: &CertInfo{NotAfter: secNow.Add(-time.Minute)}},
	}}
	if n := s.SecretsNeedAttention(); n != 2 {
		t.Fatalf("n = %d", n)
	}
}

type secretsFail struct {
	admin.Client
	err error
}

func (s secretsFail) SecretConfigs(context.Context) ([]admin.SecretConfig, error) { return nil, s.err }

func TestSecretsFailureDegradesInline(t *testing.T) {
	srv := httptest.NewServer(demo.NewServer(demo.Degraded()))
	t.Cleanup(srv.Close)
	real, err := admin.NewHTTP(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Fetch(context.Background(), secretsFail{Client: real, err: errors.New("boom")})
	if err != nil {
		t.Fatalf("a secrets failure must not fail the snapshot: %v", err)
	}
	if s.SecretsErr != "secrets unavailable: boom" || len(s.Secrets) != 0 || len(s.Clusters) == 0 {
		t.Fatalf("secrets failure: %q %d %d", s.SecretsErr, len(s.Secrets), len(s.Clusters))
	}
}

func TestFetchWithClockDrivesCertificateAge(t *testing.T) {
	srv := httptest.NewServer(demo.NewServer(demo.Degraded()))
	t.Cleanup(srv.Close)
	c, err := admin.NewHTTP(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	serverCert := func(now time.Time) *CertInfo {
		t.Helper()
		s, err := FetchWithClock(context.Background(), c, func() time.Time { return now })
		if err != nil {
			t.Fatal(err)
		}
		if !s.FetchedAt.Equal(now) {
			t.Fatalf("FetchedAt = %v, want %v", s.FetchedAt, now)
		}
		for _, sec := range s.Secrets {
			if sec.Name == "server-cert" && sec.State == SecretActive {
				return sec.Cert
			}
		}
		t.Fatal("no active server-cert")
		return nil
	}
	early, late := serverCert(secNow), serverCert(secNow.AddDate(0, 0, 100))
	if early.DaysLeft != 62 || early.Expired(secNow) {
		t.Errorf("at the epoch: %+v", early)
	}
	if late.DaysLeft >= 0 || !late.Expired(secNow.AddDate(0, 0, 100)) {
		t.Errorf("100 days later it must be expired: %+v", late)
	}
	if again := serverCert(secNow); again.DaysLeft != early.DaysLeft {
		t.Errorf("the same clock must give the same answer: %d vs %d", again.DaysLeft, early.DaysLeft)
	}
}

func TestSecretStatusStringOutOfRange(t *testing.T) {
	if got := SecretStatus(99).String(); got != "unknown" {
		t.Fatalf("got %q", got)
	}
}

func TestFetchSecretsFromDemo(t *testing.T) {
	srv := httptest.NewServer(demo.NewServer(demo.Degraded()))
	t.Cleanup(srv.Close)
	c, err := admin.NewHTTP(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	s, err := FetchWithClock(context.Background(), c, func() time.Time { return secNow })
	if err != nil {
		t.Fatal(err)
	}
	if s.SecretsErr != "" || len(s.Secrets) == 0 {
		t.Fatalf("err=%q n=%d", s.SecretsErr, len(s.Secrets))
	}
	var withCert int
	for _, sec := range s.Secrets {
		if sec.Cert != nil {
			withCert++
		}
	}
	if withCert == 0 {
		t.Fatal("demo certificates must show up as certificate facts")
	}
	var servers int
	for _, sec := range s.Secrets {
		switch sec.Name {
		case "server-cert":
			servers++
			var cl, ls bool
			for _, u := range sec.UsedBy {
				cl = cl || u == (Ref{"cluster", "api-gateway"})
				ls = ls || u == (Ref{"listener", "0.0.0.0_8080"})
			}
			if !cl || !ls {
				t.Errorf("server-cert %s UsedBy = %v", sec.VersionInfo, sec.UsedBy)
			}
			if sec.State == SecretActive && (sec.Cert == nil || sec.Cert.Serial != "a1b2c") {
				t.Errorf("server-cert cert = %+v", sec.Cert)
			}
		case "bootstrap-cert":
			if sec.Cert == nil || !sec.Cert.Expired(s.FetchedAt) {
				t.Errorf("bootstrap-cert must be expired: %+v", sec.Cert)
			}
		}
	}
	if servers != 2 {
		t.Errorf("want the active and warming server-cert, got %d", servers)
	}
	var found bool
	for _, c := range s.Clusters {
		if c.Name == "orders-db" {
			for _, u := range c.Secrets {
				found = found || u == (SecretUse{"missing-cert", false})
			}
		}
	}
	if !found {
		t.Error("orders-db must reference the unloaded missing-cert")
	}
}

func TestSecretRefsFindsEverySDSKey(t *testing.T) {
	raw := []byte(`{"transport_socket":{"typed_config":{"common_tls_context":{
	  "tls_certificate_sds_secret_configs":[{"name":"server-cert"},{"name":"extra"}],
	  "combined_validation_context":{"validation_context_sds_secret_config":{"name":"mesh-ca"}}}}},
	 "transport_socket_matches":[{"transport_socket":{"typed_config":{"common_tls_context":{
	  "tls_certificate_sds_secret_configs":[{"name":"server-cert"}]}}}}]}`)
	got := secretRefs(raw)
	want := []string{"extra", "mesh-ca", "server-cert"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("refs = %v, want %v", got, want)
	}
}

func TestSecretRefsIgnoresConfigWithoutSDS(t *testing.T) {
	for _, raw := range []string{``, `null`, `{"name":"plain"}`, `{not json`, `{"sds_secret_configs":"oops"}`} {
		if got := secretRefs([]byte(raw)); len(got) != 0 {
			t.Fatalf("%q: refs = %v", raw, got)
		}
	}
}

func TestLinkSecrets(t *testing.T) {
	sdsCfg := func(names ...string) ConfigInfo {
		raw := `{"tls_certificate_sds_secret_configs":[`
		for i, n := range names {
			if i > 0 {
				raw += ","
			}
			raw += `{"name":"` + n + `"}`
		}
		return ConfigInfo{JSON: []byte(raw + `]}`)}
	}
	s := Snapshot{
		Secrets:   []Secret{{Name: "server-cert"}, {Name: "orphan"}},
		Clusters:  []Cluster{{Name: "api", Config: sdsCfg("server-cert", "ghost")}, {Name: "plain"}},
		Listeners: []Listener{{Name: "l1", Config: sdsCfg("server-cert")}},
	}
	linkSecrets(&s)
	if u := s.Secrets[0].UsedBy; len(u) != 2 || u[0] != (Ref{"cluster", "api"}) || u[1] != (Ref{"listener", "l1"}) {
		t.Fatalf("UsedBy = %+v", u)
	}
	if len(s.Secrets[1].UsedBy) != 0 {
		t.Fatalf("orphan: %+v", s.Secrets[1].UsedBy)
	}
	api := s.Clusters[0].Secrets
	if len(api) != 2 || api[0] != (SecretUse{"ghost", false}) || api[1] != (SecretUse{"server-cert", true}) {
		t.Fatalf("api uses = %+v", api)
	}
	if len(s.Clusters[1].Secrets) != 0 {
		t.Fatal("a cluster without SDS has no secret uses")
	}
}

func TestLinkSecretsLinksEverySameNamedEntry(t *testing.T) {
	cfg := ConfigInfo{JSON: []byte(`{"tls_certificate_sds_secret_configs":[{"name":"server-cert"}]}`)}
	s := Snapshot{
		Secrets:  []Secret{{Name: "server-cert", State: SecretActive}, {Name: "server-cert", State: SecretWarming}},
		Clusters: []Cluster{{Name: "api", Config: cfg}},
	}
	linkSecrets(&s)
	for i, sec := range s.Secrets {
		if len(sec.UsedBy) != 1 || sec.UsedBy[0] != (Ref{"cluster", "api"}) {
			t.Fatalf("entry %d UsedBy = %+v", i, sec.UsedBy)
		}
	}
	if u := s.Clusters[0].Secrets; len(u) != 1 || !u[0].Loaded {
		t.Fatalf("uses = %+v", u)
	}
}

func TestLinkSecretsIsIdempotent(t *testing.T) {
	cfg := ConfigInfo{JSON: []byte(`{"tls_certificate_sds_secret_configs":[{"name":"server-cert"}]}`)}
	s := Snapshot{
		Secrets:   []Secret{{Name: "server-cert"}},
		Clusters:  []Cluster{{Name: "api", Config: cfg}},
		Listeners: []Listener{{Name: "l1", Config: cfg}},
	}
	linkSecrets(&s)
	linkSecrets(&s)
	if u := s.Secrets[0].UsedBy; len(u) != 2 || u[0] != (Ref{"cluster", "api"}) || u[1] != (Ref{"listener", "l1"}) {
		t.Fatalf("UsedBy = %+v", u)
	}
	if len(s.Clusters[0].Secrets) != 1 || len(s.Listeners[0].Secrets) != 1 {
		t.Fatalf("uses duplicated: %+v %+v", s.Clusters[0].Secrets, s.Listeners[0].Secrets)
	}
}

func TestLinkSecretsAssumesLoadedWhenDumpFailed(t *testing.T) {
	s := Snapshot{
		SecretsErr: "secrets unavailable: boom",
		Clusters:   []Cluster{{Name: "api", Config: ConfigInfo{JSON: []byte(`{"validation_context_sds_secret_config":{"name":"ca"}}`)}}},
	}
	linkSecrets(&s)
	if u := s.Clusters[0].Secrets; len(u) != 1 || !u[0].Loaded {
		t.Fatalf("unknown must not be flagged not loaded: %+v", u)
	}
}
