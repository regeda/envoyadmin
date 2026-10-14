package admin

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testPEM(t *testing.T, serial int64, dns []string, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    notAfter.Add(-time.Hour),
		NotAfter:     notAfter,
		DNSNames:     dns,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func jsonStr(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// secretJSON wraps a secret body as a one-element active dump.
func secretJSON(t *testing.T, name, body string) string {
	t.Helper()
	return `{"configs":[{"@type":"type.googleapis.com/envoy.admin.v3.SecretsConfigDump.DynamicSecret","name":` +
		jsonStr(t, name) + `,"version_info":"1","secret":{"name":` + jsonStr(t, name) + `,` + body + `}}]}`
}

func oneSecret(t *testing.T, body string) SecretConfig {
	t.Helper()
	c := serveSecrets(t, secretJSON(t, "s", body), `{}`, `{}`)
	got, err := c.SecretConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("secret must still be returned, got %+v", got)
	}
	return got[0]
}

func tlsBody(chainField string) string {
	return `"tls_certificate":{"certificate_chain":{` + chainField + `},"private_key":{"inline_string":"[redacted]"}}`
}

// walkStrings visits every string reachable from v.
func walkStrings(v reflect.Value, f func(string)) {
	switch v.Kind() {
	case reflect.String:
		f(v.String())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			walkStrings(v.Field(i), f)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			walkStrings(v.Index(i), f)
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			walkStrings(v.Elem(), f)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			walkStrings(k, f)
			walkStrings(v.MapIndex(k), f)
		}
	}
}

func assertNoBodies(t *testing.T, got []SecretConfig, banned ...string) {
	t.Helper()
	walkStrings(reflect.ValueOf(got), func(s string) {
		for _, b := range banned {
			if strings.Contains(s, b) {
				t.Fatalf("string %q contains %q", s, b)
			}
		}
	})
}

func TestCertFactsRealCapture(t *testing.T) {
	c := serveSecrets(t, string(capture(t, "real-secrets-dynamic.json")), `{}`, string(capture(t, "real-secrets-static.json")))
	got, err := c.SecretConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]SecretConfig{}
	for _, s := range got {
		by[s.Name] = s
	}
	cert := by["server-cert"].Cert
	if cert == nil {
		t.Fatal("server-cert has no Cert")
	}
	if cert.Serial != "4e77ed433ec7c87a1736f90cbaf85b985c25d9eb" {
		t.Fatalf("serial %q", cert.Serial)
	}
	if !reflect.DeepEqual(cert.SANs, []string{"capture.example.com"}) {
		t.Fatalf("sans %v", cert.SANs)
	}
	want := time.Date(2036, 10, 11, 11, 52, 18, 0, time.UTC)
	if !cert.NotAfter.Equal(want) {
		t.Fatalf("not after %v", cert.NotAfter)
	}
	if by["bootstrap-cert"].Cert == nil {
		t.Fatal("bootstrap-cert has no Cert")
	}
}

func TestCertFactsInlineStringAndBytesAgree(t *testing.T) {
	notAfter := time.Date(2031, 5, 6, 7, 8, 9, 0, time.UTC)
	p := testPEM(t, 0xabc123, []string{"a.example.com", "b.example.com"}, notAfter)
	s := oneSecret(t, tlsBody(`"inline_string":`+jsonStr(t, p)))
	b := oneSecret(t, tlsBody(`"inline_bytes":"`+base64.StdEncoding.EncodeToString([]byte(p))+`"`))
	if s.Cert == nil || b.Cert == nil {
		t.Fatalf("missing cert: %+v %+v", s.Cert, b.Cert)
	}
	if !reflect.DeepEqual(s.Cert, b.Cert) {
		t.Fatalf("differ: %+v vs %+v", s.Cert, b.Cert)
	}
	if s.Cert.Serial != "abc123" || !s.Cert.NotAfter.Equal(notAfter) ||
		!reflect.DeepEqual(s.Cert.SANs, []string{"a.example.com", "b.example.com"}) {
		t.Fatalf("facts %+v", s.Cert)
	}
}

func TestCertFactsTrustedCABundleYieldsFirst(t *testing.T) {
	na := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	bundle := testPEM(t, 1, []string{"first.example.com"}, na) + testPEM(t, 2, []string{"second.example.com"}, na)
	s := oneSecret(t, `"validation_context":{"trusted_ca":{"inline_string":`+jsonStr(t, bundle)+`}}`)
	if s.Cert == nil || s.Cert.Serial != "1" || !reflect.DeepEqual(s.Cert.SANs, []string{"first.example.com"}) {
		t.Fatalf("got %+v", s.Cert)
	}
}

func TestCertFactsUnreadableYieldsNil(t *testing.T) {
	na := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	good := testPEM(t, 7, nil, na)
	other := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("x")}))
	cases := map[string]string{
		"filename":       `"filename":"/etc/cert.pem"`,
		"garbage":        `"inline_string":"not a pem"`,
		"empty":          ``,
		"other type":     `"inline_string":` + jsonStr(t, other),
		"non-cert first": `"inline_string":` + jsonStr(t, other+good),
		"bad base64":     `"inline_bytes":"!!!"`,
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			if s := oneSecret(t, tlsBody(field)); s.Cert != nil {
				t.Fatalf("want nil, got %+v", s.Cert)
			}
		})
	}
}

func TestCertFactsNeverKeepContent(t *testing.T) {
	p := testPEM(t, 9, []string{"x.example.com"}, time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
	body := `"tls_certificate":{"certificate_chain":{"inline_string":` + jsonStr(t, p) +
		`},"private_key":{"inline_string":"SENTINEL-PRIVATE-KEY"}}`
	c := serveSecrets(t, secretJSON(t, "s", body), `{}`, `{}`)
	got, err := c.SecretConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Cert == nil {
		t.Fatalf("got %+v", got)
	}
	assertNoBodies(t, got, "SENTINEL", "BEGIN CERTIFICATE", "PRIVATE KEY")
}
