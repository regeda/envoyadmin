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

func TestFetchSecretsFromDemo(t *testing.T) {
	t.Skip("needs demo secrets, Task 8")
	srv := httptest.NewServer(demo.NewServer(demo.Degraded()))
	t.Cleanup(srv.Close)
	c, err := admin.NewHTTP(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Fetch(context.Background(), c)
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
}
