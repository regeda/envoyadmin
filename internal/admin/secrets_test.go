package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Hand-written from Envoy's addResourceToDump behaviour: NOT captured. The
// private key text is a sentinel the decoder must never keep.
const handActiveSecrets = `{"configs":[
 {"@type":"type.googleapis.com/envoy.admin.v3.SecretsConfigDump.DynamicSecret","name":"server-cert",
  "version_info":"12","last_updated":"2026-10-14T10:00:00.123Z",
  "secret":{"@type":"type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.Secret","name":"server-cert",
   "tls_certificate":{"certificate_chain":{"inline_bytes":"Q0VSVA=="},"private_key":{"inline_string":"SENTINEL-PRIVATE-KEY"}}}},
 {"@type":"type.googleapis.com/envoy.admin.v3.SecretsConfigDump.DynamicSecret","name":"mesh-ca",
  "version_info":"3","secret":{"name":"mesh-ca","validation_context":{"trusted_ca":{"inline_bytes":"Q0E="}}}},
 {"@type":"type.googleapis.com/envoy.admin.v3.SecretsConfigDump.DynamicSecret","version_info":"1","secret":{"name":"x"}},
 "garbage"
]}`

const handWarmingSecrets = `{"configs":[
 {"@type":"type.googleapis.com/envoy.admin.v3.SecretsConfigDump.DynamicSecret","name":"server-cert",
  "version_info":"13","secret":{"name":"server-cert","tls_certificate":{}}}]}`

const handStaticSecrets = `{"configs":[
 {"@type":"type.googleapis.com/envoy.admin.v3.SecretsConfigDump.StaticSecret","name":"bootstrap-cert",
  "last_updated":"2026-10-14T09:00:00Z","secret":{"name":"bootstrap-cert","session_ticket_keys":{}}}]}`

func serveSecrets(t *testing.T, active, warming, static string) *HTTP {
	t.Helper()
	return newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("resource") {
		case "dynamic_active_secrets":
			_, _ = w.Write([]byte(active))
		case "dynamic_warming_secrets":
			_, _ = w.Write([]byte(warming))
		case "static_secrets":
			_, _ = w.Write([]byte(static))
		default:
			http.Error(w, "bad resource", http.StatusBadRequest)
		}
	}), time.Second)
}

func TestSecretConfigsDecodesEveryState(t *testing.T) {
	c := serveSecrets(t, handActiveSecrets, handWarmingSecrets, handStaticSecrets)
	got, err := c.SecretConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	type key struct {
		name  string
		state SecretState
	}
	by := map[key]SecretConfig{}
	for _, s := range got {
		by[key{s.Name, s.State}] = s
	}
	if len(got) != 4 {
		t.Fatalf("want 4 (nameless and garbage elements skipped), got %+v", got)
	}
	if s := by[key{"server-cert", SecretActive}]; s.VersionInfo != "12" || s.Kind != "tls_certificate" || s.LastUpdated.IsZero() {
		t.Fatalf("active: %+v", s)
	}
	if s := by[key{"server-cert", SecretWarming}]; s.VersionInfo != "13" {
		t.Fatalf("warming: %+v", s)
	}
	if s := by[key{"mesh-ca", SecretActive}]; s.Kind != "validation_context" || !s.LastUpdated.IsZero() {
		t.Fatalf("mesh-ca: %+v", s)
	}
	if s := by[key{"bootstrap-cert", SecretStatic}]; s.Kind != "session_ticket_keys" || s.VersionInfo != "" {
		t.Fatalf("static: %+v", s)
	}
}

func TestSecretConfigsNeverKeepContent(t *testing.T) {
	c := serveSecrets(t, handActiveSecrets, `{}`, `{}`)
	got, err := c.SecretConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(formatAll(got)), "sentinel") {
		t.Fatalf("secret content leaked into %+v", got)
	}
}

func formatAll(v []SecretConfig) string {
	var b strings.Builder
	for _, s := range v {
		b.WriteString(s.Name + "|" + s.VersionInfo + "|" + s.Kind + "|" + string(s.State) + ";")
	}
	return b.String()
}

func TestSecretConfigsAcceptsWrappedDump(t *testing.T) {
	wrapped := `{"configs":[{"@type":"type.googleapis.com/envoy.admin.v3.SecretsConfigDump",
	 "static_secrets":[{"name":"s","secret":{"name":"s","tls_certificate":{}}}],
	 "dynamic_active_secrets":[{"name":"a","version_info":"1","secret":{"name":"a","generic_secret":{}}}],
	 "dynamic_warming_secrets":[{"name":"w","version_info":"2","secret":{"name":"w","tls_certificate":{}}}]}]}`
	c := serveSecrets(t, wrapped, wrapped, wrapped)
	got, err := c.SecretConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want exactly 3, got %+v", got)
	}
	type key struct {
		name  string
		state SecretState
	}
	by := map[key]SecretConfig{}
	for _, sc := range got {
		by[key{sc.Name, sc.State}] = sc
	}
	if _, ok := by[key{"s", SecretStatic}]; !ok {
		t.Fatalf("each resource must pick only its own list, missing static s: %+v", got)
	}
	if _, ok := by[key{"a", SecretActive}]; !ok {
		t.Fatalf("each resource must pick only its own list, missing active a: %+v", got)
	}
	if _, ok := by[key{"w", SecretWarming}]; !ok {
		t.Fatalf("each resource must pick only its own list, missing warming w: %+v", got)
	}
}

func TestSecretConfigsHTTPErrorNamesTheResource(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}), time.Second)
	_, err := c.SecretConfigs(context.Background())
	if err == nil || !strings.Contains(err.Error(), "dynamic_active_secrets") {
		t.Fatalf("err = %v", err)
	}
}

func TestRealCaptureSecretConfigs(t *testing.T) {
	c := serveSecrets(t, string(capture(t, "real-secrets-dynamic.json")), `{}`, string(capture(t, "real-secrets-static.json")))
	got, err := c.SecretConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]SecretConfig{}
	for _, s := range got {
		by[s.Name] = s
	}
	if s := by["server-cert"]; s.State != SecretActive || s.VersionInfo != "12" || s.Kind != "tls_certificate" || s.LastUpdated.IsZero() {
		t.Fatalf("server-cert: %+v", s)
	}
	if s := by["bootstrap-cert"]; s.State != SecretStatic || s.Kind != "tls_certificate" {
		t.Fatalf("bootstrap-cert: %+v", s)
	}
	if strings.Contains(formatAll(got), "PRIVATE KEY") {
		t.Fatal("secret content leaked")
	}
}
