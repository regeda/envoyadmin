# Secrets Tab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a third **Secrets** tab to envoyadmin listing every secret Envoy has loaded with name, version, state, kind, certificate facts and the clusters and listeners that use it, and never any secret content.

**Architecture:** `internal/admin` gains `SecretConfigs` (config_dump secrets) and `Certs` (`/certs`) decoders that drop secret bodies at decode time. `snapshot.Fetch` fetches both in parallel with the existing calls, joins certificate facts onto secrets, and derives "used by" by walking the cluster and listener JSON it already holds. `internal/ui` adds a third `resourceKind` with its own item type, and a "Secrets" row in the cluster and listener Overviews.

**Tech Stack:** Go, bubbletea/lipgloss, teatest golden files, `make test` (`go test -race ./...`).

**Spec:** `docs/superpowers/specs/2026-10-14-secrets-tab-design.md`

## Global Constraints

- Secret content is never fetched into the snapshot, stored or rendered: only name, `version_info`, `last_updated`, state, kind and certificate facts survive decoding.
- A failed secrets dump never raises the global error banner; the tab shows an inline "secrets unavailable: <reason>" message and reads `Secrets (!)`.
- If only `/certs` fails, secrets are still listed and an inline note says "certificate details unavailable: <reason>".
- "Used by" is display only: no navigation between tabs.
- No Config tab content for secrets: `c` shows "secret content is never displayed".
- Commit messages follow Conventional Commits (`feat(admin): ...`) and end with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- Follow existing style: raw-JSON decoding that skips unknown or malformed elements, plain Go types for the UI, `internal/demo` for fake Envoy.

## Review Focus

- A secret whose body holds a private key: the key text never appears in any rendered view (Task 7 sentinel test).
- `/certs` returns an error or an entry matching no secret: the secret is listed without certificate facts (Tasks 3 and 4).
- A cluster or listener references a secret Envoy has not loaded: marked "not loaded", but not when the secrets dump itself failed (Task 5).
- A cluster or listener with huge JSON and no SDS references: no generic JSON walk (Task 5 pre-check).
- Terminal width at the 80-column minimum: rows never wrap and never exceed the pane (Task 7).
- Two `tab` presses on a three-tab layout wrap Clusters → Listeners → Secrets → Clusters (Task 6).

---

## File Structure

- Create `internal/admin/secrets.go`: `SecretConfig`, `SecretState`, `decodeSecretConfigs`.
- Create `internal/admin/certs.go`: `Cert`, `decodeCerts`.
- Modify `internal/admin/client.go`: two interface methods, two `HTTP` methods.
- Create `internal/snapshot/secret.go`: `Secret`, `SecretStatus`, `CertInfo`, `Ref`, `SecretUse`, `secretsFrom`, `certForSecret`, `secretRefs`, `linkSecrets`, sorting.
- Modify `internal/snapshot/snapshot.go`: `Cluster.Secrets`, `Snapshot.Secrets/SecretsErr/CertsErr`, parallel fetch, `SecretsNeedAttention`.
- Modify `internal/snapshot/listener.go`: `Listener.Secrets`.
- Create `internal/ui/secret_item.go`: `secretItem` (row, overview, config note).
- Modify `internal/ui/view.go`: `kindSecrets` and its `resource`.
- Modify `internal/ui/cluster_item.go`, `internal/ui/endpoints.go`, `internal/ui/listener_item.go`: "Secrets" rows.
- Modify `internal/demo/scenario.go`, `internal/demo/server.go`: demo secrets, `/certs`, SDS references.
- Modify `README.md`: document the tab.
- Tests next to each file; capture fixtures under `internal/admin/testdata/`.

---

### Task 1: Decode secrets from the config dump

**Files:**
- Create: `internal/admin/secrets.go`
- Create: `internal/admin/secrets_test.go`
- Modify: `internal/admin/client.go` (interface and `HTTP.SecretConfigs`)

**Interfaces:**
- Produces:
  - `type SecretState string` with `SecretActive = "active"`, `SecretWarming = "warming"`, `SecretStatic = "static"`.
  - `type SecretConfig struct { Name, VersionInfo, Kind string; LastUpdated time.Time; State SecretState }`.
  - `Client.SecretConfigs(ctx context.Context) ([]SecretConfig, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/secrets_test.go`:

```go
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
		t.Fatalf("each resource must pick only its own list, got %+v", got)
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/admin/ -run SecretConfigs -v`
Expected: FAIL to compile (`SecretState`, `SecretConfigs` undefined).

- [ ] **Step 3: Write the minimal implementation**

Create `internal/admin/secrets.go`:

```go
package admin

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

const (
	typeDynamicSecret = ".SecretsConfigDump.DynamicSecret"
	typeStaticSecret  = ".SecretsConfigDump.StaticSecret"
	typeSecretsDump   = ".SecretsConfigDump"
)

// SecretState says which of Envoy's three secret lists an entry came from.
type SecretState string

const (
	SecretActive  SecretState = "active"
	SecretWarming SecretState = "warming"
	SecretStatic  SecretState = "static"
)

// SecretConfig is one secret's metadata. It deliberately has no field for the
// secret body: the decoder drops it, so no later code can show it.
type SecretConfig struct {
	Name, VersionInfo string
	// Kind is the one populated field of the secret ("tls_certificate",
	// "validation_context", "session_ticket_keys", "generic_secret").
	Kind        string
	LastUpdated time.Time
	State       SecretState
}

type rawSecret struct {
	Name        string                     `json:"name"`
	VersionInfo string                     `json:"version_info"`
	LastUpdated string                     `json:"last_updated"`
	Secret      map[string]json.RawMessage `json:"secret"`
}

type secretsDump struct {
	Static  []rawSecret `json:"static_secrets"`
	Active  []rawSecret `json:"dynamic_active_secrets"`
	Warming []rawSecret `json:"dynamic_warming_secrets"`
}

// secretKind names the populated field of a secret, ignoring the envelope keys.
func secretKind(m map[string]json.RawMessage) string {
	var kinds []string
	for k := range m {
		if k != "name" && k != "@type" {
			kinds = append(kinds, k)
		}
	}
	sort.Strings(kinds)
	if len(kinds) == 0 {
		return ""
	}
	return kinds[0]
}

func secretConfig(r rawSecret, state SecretState) (SecretConfig, bool) {
	name := r.Name
	if name == "" {
		_ = json.Unmarshal(r.Secret["name"], &name)
	}
	if name == "" {
		return SecretConfig{}, false
	}
	return SecretConfig{
		Name: name, VersionInfo: r.VersionInfo, Kind: secretKind(r.Secret),
		LastUpdated: parseTime(r.LastUpdated), State: state,
	}, true
}

// decodeSecretConfigs walks /config_dump as raw JSON, like decodeClusterConfigs.
// With ?resource=<list> Envoy emits one configs[] element per secret, typed
// SecretsConfigDump.DynamicSecret or .StaticSecret; the requested resource, not
// the element, says whether a dynamic one is active or warming. A plain
// /config_dump has one wrapped SecretsConfigDump; the list matching state is
// taken from it. Anything unrecognised or malformed is skipped, never fatal.
func decodeSecretConfigs(body []byte, state SecretState) ([]SecretConfig, error) {
	var dump struct {
		Configs []json.RawMessage `json:"configs"`
	}
	if err := json.Unmarshal(body, &dump); err != nil {
		return nil, err
	}
	var out []SecretConfig
	add := func(r rawSecret) {
		if sc, ok := secretConfig(r, state); ok {
			out = append(out, sc)
		}
	}
	for _, raw := range dump.Configs {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			continue
		}
		var typ string
		if json.Unmarshal(obj["@type"], &typ) != nil {
			continue
		}
		switch {
		case strings.HasSuffix(typ, typeDynamicSecret), strings.HasSuffix(typ, typeStaticSecret):
			var r rawSecret
			if json.Unmarshal(raw, &r) == nil {
				add(r)
			}
		case strings.HasSuffix(typ, typeSecretsDump):
			var sd secretsDump
			if json.Unmarshal(raw, &sd) != nil {
				continue
			}
			list := sd.Active
			switch state {
			case SecretWarming:
				list = sd.Warming
			case SecretStatic:
				list = sd.Static
			}
			for _, r := range list {
				add(r)
			}
		}
	}
	return out, nil
}
```

Modify `internal/admin/client.go`: add to the `Client` interface

```go
	SecretConfigs(ctx context.Context) ([]SecretConfig, error)
```

and append after `ListenerConfigs`:

```go
func (h *HTTP) SecretConfigs(ctx context.Context) ([]SecretConfig, error) {
	var all []SecretConfig
	for _, r := range []struct {
		resource string
		state    SecretState
	}{
		{"dynamic_active_secrets", SecretActive},
		{"dynamic_warming_secrets", SecretWarming},
		{"static_secrets", SecretStatic},
	} {
		body, err := h.get(ctx, "/config_dump", url.Values{"resource": {r.resource}})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.resource, err)
		}
		cfgs, err := decodeSecretConfigs(body, r.state)
		if err != nil {
			return nil, fmt.Errorf("GET /config_dump: decode %s: %w", r.resource, err)
		}
		all = append(all, cfgs...)
	}
	return all, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/admin/ -run SecretConfigs -v && go build ./...`
Expected: PASS and a clean build (`internal/snapshot` tests embed `admin.Client`, so they still compile).

- [ ] **Step 5: Commit**

```bash
git add internal/admin/secrets.go internal/admin/secrets_test.go internal/admin/client.go
git commit -m "feat(admin): decode secrets from the config dump without their content"
```

---

### Task 2: Real Envoy capture for secrets and `/certs`

**Files:**
- Create: `internal/admin/testdata/capture/bootstrap-sds.yaml`, `internal/admin/testdata/capture/sds.yaml`, `internal/admin/testdata/capture/server.crt`
- Create: `internal/admin/testdata/real-secrets-dynamic.json`, `real-secrets-static.json`, `real-certs.json`
- Modify: `internal/admin/testdata/README.md`
- Test: `internal/admin/secrets_test.go` (append)

**Interfaces:**
- Produces: genuine fixtures that Tasks 3 and 4 decode, and the answer to the spec's open item: what `path` (or other field) a `/certs` entry has for an SDS secret.

This task settles the spec's open item. It needs Docker.

- [ ] **Step 1: Write the capture inputs**

Create `internal/admin/testdata/capture/bootstrap-sds.yaml`: an Envoy bootstrap with admin on 9901, one static cluster `api`, one static secret `bootstrap-cert` (self-signed test cert, inline), a file-based SDS config for dynamic secrets, and a static listener `tls_listener` whose downstream TLS context references `server-cert` through `tls_certificate_sds_secret_configs` with `sds_config: {path_config_source: {path: /etc/envoy/sds.yaml}}`. Create `sds.yaml` as a `DiscoveryResponse` (`version_info: "12"`) holding one `Secret` named `server-cert` with an inline self-signed certificate and key generated for the capture. Generate the pair with `openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj /CN=envoyadmin-capture -addext subjectAltName=DNS:capture.example.com -keyout /dev/null -out server.crt`, and use a throwaway key. Nothing host-specific may appear.

- [ ] **Step 2: Capture**

Run Envoy as the other captures do (see `testdata/README.md`), then from the admin port:

```bash
docker run -d --name envoy-cap -v "$PWD/internal/admin/testdata/capture:/etc/envoy" -p 9901:9901 envoyproxy/envoy:v1.31-latest -c /etc/envoy/bootstrap-sds.yaml
curl -s 'localhost:9901/config_dump?resource=dynamic_active_secrets' > internal/admin/testdata/real-secrets-dynamic.json
curl -s 'localhost:9901/config_dump?resource=static_secrets' > internal/admin/testdata/real-secrets-static.json
curl -s localhost:9901/certs > internal/admin/testdata/real-certs.json
docker rm -f envoy-cap
```

Expected: `real-secrets-dynamic.json` has a `DynamicSecret` element for `server-cert` with `version_info` `"12"`; `real-certs.json` lists both certificates. If Docker is unavailable, stop and report; do not hand-write these files as captures.

- [ ] **Step 3: Record what the capture shows**

Open `real-certs.json` and note, for the `server-cert` entry, the exact value of `path` (or whichever field identifies it). Add a "Secrets captures" section to `testdata/README.md` listing the three files, the Envoy version string, the commands above, and one sentence stating how a `/certs` entry for an SDS secret is identified. Task 3 uses that statement.

- [ ] **Step 4: Write the failing test**

Append to `internal/admin/secrets_test.go`:

```go
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
```

- [ ] **Step 5: Run it and make it pass**

Run: `go test ./internal/admin/ -run RealCaptureSecretConfigs -v`
Expected: PASS. If a field name differs from the hand-written shape (for example the element `@type`), fix `decodeSecretConfigs` and keep the Task 1 tests green: `go test ./internal/admin/ -v`.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/testdata internal/admin/secrets_test.go internal/admin/secrets.go
git commit -m "test(admin): capture secrets and certs from a real Envoy 1.31.10"
```

---

### Task 3: Decode certificates from `/certs`

**Files:**
- Create: `internal/admin/certs.go`
- Create: `internal/admin/certs_test.go`
- Modify: `internal/admin/client.go`

**Interfaces:**
- Produces:
  - `type Cert struct { Path, Serial string; SANs []string; NotAfter time.Time; DaysLeft int64; CA bool }`.
  - `Client.Certs(ctx context.Context) ([]Cert, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/certs_test.go`. Use the real capture for the primary assertion, using the identification rule recorded in Task 2's README note (replace `PATH` below with the captured `path` value for `server-cert`):

```go
package admin

import (
	"context"
	"net/http"
	"testing"
	"time"
)

const handCerts = `{"certificates":[
 {"ca_cert":[{"path":"mesh-ca","serial_number":"01","days_until_expiration":"30",
   "valid_from":"2026-01-01T00:00:00Z","expiration_time":"2026-11-13T00:00:00Z"}],
  "cert_chain":[{"path":"server-cert","serial_number":"0a1b","days_until_expiration":"7",
   "subject_alt_names":[{"dns":"a.example.com"},{"uri":"spiffe://x/y"},{"ip_address":"10.0.0.1"}],
   "valid_from":"2026-01-01T00:00:00Z","expiration_time":"2026-10-21T00:00:00Z"}]},
 {"cert_chain":"garbage"}
]}`

func serveCerts(t *testing.T, body string, status int) *HTTP {
	t.Helper()
	return newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/certs" {
			http.NotFound(w, r)
			return
		}
		if status != 0 {
			http.Error(w, "boom", status)
			return
		}
		_, _ = w.Write([]byte(body))
	}), time.Second)
}

func TestCertsDecodesChainAndCA(t *testing.T) {
	got, err := serveCerts(t, handCerts, 0).Certs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 certs (malformed entry skipped), got %+v", got)
	}
	var leaf, ca Cert
	for _, c := range got {
		if c.CA {
			ca = c
		} else {
			leaf = c
		}
	}
	if leaf.Path != "server-cert" || leaf.Serial != "0a1b" || leaf.DaysLeft != 7 ||
		leaf.NotAfter.IsZero() || len(leaf.SANs) != 3 || leaf.SANs[0] != "a.example.com" || leaf.SANs[1] != "spiffe://x/y" {
		t.Fatalf("leaf: %+v", leaf)
	}
	if ca.Path != "mesh-ca" || !ca.CA || ca.DaysLeft != 30 {
		t.Fatalf("ca: %+v", ca)
	}
}

func TestCertsHTTPError(t *testing.T) {
	if _, err := serveCerts(t, "", http.StatusInternalServerError).Certs(context.Background()); err == nil {
		t.Fatal("want an error")
	}
}

func TestRealCaptureCerts(t *testing.T) {
	got, err := serveCerts(t, string(capture(t, "real-certs.json")), 0).Certs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range got {
		if c.Path == "PATH" && c.Serial != "" && !c.NotAfter.IsZero() {
			found = true
		}
	}
	if !found {
		t.Fatalf("no cert identified as the SDS secret in %+v", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/admin/ -run Cert -v`
Expected: FAIL to compile (`Cert`, `Certs` undefined).

- [ ] **Step 3: Write the minimal implementation**

Create `internal/admin/certs.go`:

```go
package admin

import (
	"encoding/json"
	"strconv"
	"time"
)

// Cert is one certificate of /certs: facts only, never the certificate body.
type Cert struct {
	Path, Serial string
	SANs         []string
	NotAfter     time.Time
	DaysLeft     int64
	CA           bool // from ca_cert[] rather than cert_chain[]
}

type rawCert struct {
	Path     string `json:"path"`
	Serial   string `json:"serial_number"`
	Days     json.RawMessage `json:"days_until_expiration"`
	Expires  string `json:"expiration_time"`
	SANs     []struct {
		DNS string `json:"dns"`
		URI string `json:"uri"`
		IP  string `json:"ip_address"`
	} `json:"subject_alt_names"`
}

func (r rawCert) cert(ca bool) Cert {
	c := Cert{Path: r.Path, Serial: r.Serial, NotAfter: parseTime(r.Expires), CA: ca}
	var s string
	if json.Unmarshal(r.Days, &s) == nil {
		c.DaysLeft, _ = strconv.ParseInt(s, 10, 64)
	} else {
		_ = json.Unmarshal(r.Days, &c.DaysLeft) // proto3 JSON may send a number
	}
	for _, san := range r.SANs {
		for _, v := range []string{san.DNS, san.URI, san.IP} {
			if v != "" {
				c.SANs = append(c.SANs, v)
			}
		}
	}
	return c
}

// decodeCerts reads /certs as raw JSON. A malformed certificate or group is
// skipped, never fatal.
func decodeCerts(body []byte) ([]Cert, error) {
	var doc struct {
		Certificates []json.RawMessage `json:"certificates"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	var out []Cert
	for _, g := range doc.Certificates {
		var group struct {
			CACert    []rawCert `json:"ca_cert"`
			CertChain []rawCert `json:"cert_chain"`
		}
		if json.Unmarshal(g, &group) != nil {
			continue
		}
		for _, r := range group.CertChain {
			out = append(out, r.cert(false))
		}
		for _, r := range group.CACert {
			out = append(out, r.cert(true))
		}
	}
	return out, nil
}
```

Run `gofmt -w internal/admin/certs.go`. Modify `client.go`: add `Certs(ctx context.Context) ([]Cert, error)` to the interface and

```go
func (h *HTTP) Certs(ctx context.Context) ([]Cert, error) {
	body, err := h.get(ctx, "/certs", nil)
	if err != nil {
		return nil, err
	}
	certs, err := decodeCerts(body)
	if err != nil {
		return nil, fmt.Errorf("GET /certs: decode: %w", err)
	}
	return certs, nil
}
```

If the real capture shows a different field layout from the hand-written one (for example `days_until_expiration` as a number, or SANs under a different key), adjust `rawCert` until `TestRealCaptureCerts` passes and keep `TestCertsDecodesChainAndCA` green by updating `handCerts` to the real shape.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/admin/ -v && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin
git commit -m "feat(admin): decode certificate facts from /certs"
```

---

### Task 4: Snapshot secrets and the certificate join

**Files:**
- Create: `internal/snapshot/secret.go`
- Create: `internal/snapshot/secret_test.go`
- Modify: `internal/snapshot/snapshot.go`

**Interfaces:**
- Consumes: `admin.SecretConfig`, `admin.Cert`, `Client.SecretConfigs`, `Client.Certs` (Tasks 1 and 3).
- Produces:
  - `type SecretStatus int` with `SecretActive`, `SecretWarming`, `SecretStatic`; `String()`.
  - `type CertInfo struct { Serial string; SANs []string; NotAfter time.Time; DaysLeft int64 }`; `func (c CertInfo) Expired(now time.Time) bool`.
  - `type Ref struct{ Kind, Name string }` (`Kind` is `"cluster"` or `"listener"`).
  - `type Secret struct { Name, Kind, VersionInfo string; State SecretStatus; LastUpdated time.Time; Cert *CertInfo; UsedBy []Ref }`.
  - `Snapshot.Secrets []Secret`, `Snapshot.SecretsErr string`, `Snapshot.CertsErr string`.
  - `func (s Snapshot) SecretsNeedAttention() int` (warming secrets plus secrets with an expired certificate).
  - `func secretsFrom(scs []admin.SecretConfig, certs []admin.Cert, now time.Time) []Secret`.
  - `func certForSecret(name string, certs []admin.Cert) *admin.Cert`: the only place that knows how `/certs` entries map to secrets.

- [ ] **Step 1: Write the failing test**

Create `internal/snapshot/secret_test.go`:

```go
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

func TestSecretsFromJoinsCertsAndSorts(t *testing.T) {
	scs := []admin.SecretConfig{
		{Name: "b-ok", VersionInfo: "2", Kind: "tls_certificate", State: admin.SecretActive},
		{Name: "a-warm", VersionInfo: "3", Kind: "tls_certificate", State: admin.SecretWarming},
		{Name: "c-expired", VersionInfo: "1", Kind: "tls_certificate", State: admin.SecretActive},
		{Name: "d-static", Kind: "validation_context", State: admin.SecretStatic},
	}
	certs := []admin.Cert{
		{Path: "b-ok", Serial: "01", SANs: []string{"b.example.com"}, NotAfter: secNow.Add(48 * time.Hour), DaysLeft: 2},
		{Path: "c-expired", Serial: "02", NotAfter: secNow.Add(-time.Hour)},
		{Path: "unrelated", Serial: "09"},
	}
	got := secretsFrom(scs, certs, secNow)
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
	if c := by["c-expired"].Cert; c == nil || !c.Expired(secNow) {
		t.Fatalf("c-expired cert: %+v", c)
	}
	if by["a-warm"].Cert != nil || by["d-static"].Cert != nil {
		t.Fatal("a secret without a matching cert has no Cert")
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

func fetchWith(t *testing.T, sc demo.Scenario, wrap func(admin.Client) admin.Client) Snapshot {
	t.Helper()
	srv := httptest.NewServer(demo.NewServer(sc))
	t.Cleanup(srv.Close)
	real, err := admin.NewHTTP(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Fetch(context.Background(), wrap(real))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFetchSecretsFromDemo(t *testing.T) {
	s := fetchWith(t, demo.Degraded(), func(c admin.Client) admin.Client { return c })
	if s.SecretsErr != "" || s.CertsErr != "" || len(s.Secrets) == 0 {
		t.Fatalf("err=%q certsErr=%q n=%d", s.SecretsErr, s.CertsErr, len(s.Secrets))
	}
	var withCert int
	for _, sec := range s.Secrets {
		if sec.Cert != nil {
			withCert++
		}
	}
	if withCert == 0 {
		t.Fatal("demo certs must join onto demo secrets")
	}
}

type secretsFail struct {
	admin.Client
	secretsErr, certsErr error
}

func (s secretsFail) SecretConfigs(ctx context.Context) ([]admin.SecretConfig, error) {
	if s.secretsErr != nil {
		return nil, s.secretsErr
	}
	return s.Client.SecretConfigs(ctx)
}

func (s secretsFail) Certs(ctx context.Context) ([]admin.Cert, error) {
	if s.certsErr != nil {
		return nil, s.certsErr
	}
	return s.Client.Certs(ctx)
}

func TestSecretsFailuresDegradeInline(t *testing.T) {
	boom := errors.New("boom")
	s := fetchWith(t, demo.Degraded(), func(c admin.Client) admin.Client { return secretsFail{Client: c, secretsErr: boom} })
	if s.SecretsErr != "secrets unavailable: boom" || len(s.Secrets) != 0 || len(s.Clusters) == 0 {
		t.Fatalf("secrets failure: %q %d %d", s.SecretsErr, len(s.Secrets), len(s.Clusters))
	}
	s = fetchWith(t, demo.Degraded(), func(c admin.Client) admin.Client { return secretsFail{Client: c, certsErr: boom} })
	if s.CertsErr != "certificate details unavailable: boom" || s.SecretsErr != "" || len(s.Secrets) == 0 {
		t.Fatalf("certs failure: %q %q %d", s.CertsErr, s.SecretsErr, len(s.Secrets))
	}
	for _, sec := range s.Secrets {
		if sec.Cert != nil {
			t.Fatalf("no cert facts when /certs failed: %+v", sec)
		}
	}
}
```

(The demo helpers these tests rely on are added in Task 8; until then Tasks 4's `fetchWith` tests are written but expected to fail on the demo, see Step 2. To keep Task 4 independently green, mark the three demo-dependent tests with `t.Skip("needs demo secrets, Task 8")` in this task and remove the skips in Task 8 Step 5.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/snapshot/ -run 'Secrets' -v`
Expected: FAIL to compile (`secretsFrom`, `Secret` ... undefined).

- [ ] **Step 3: Write the minimal implementation**

Create `internal/snapshot/secret.go`:

```go
package snapshot

import (
	"sort"
	"time"

	"github.com/regeda/envoyadmin/internal/admin"
)

type SecretStatus int

const (
	SecretActive SecretStatus = iota
	SecretWarming
	SecretStatic
)

func (s SecretStatus) String() string {
	return [...]string{"active", "warming", "static"}[s]
}

// CertInfo is a certificate's facts. There is no field for its body.
type CertInfo struct {
	Serial   string
	SANs     []string
	NotAfter time.Time
	DaysLeft int64
}

// Expired reports whether the certificate is past its expiry at now.
func (c CertInfo) Expired(now time.Time) bool {
	return !c.NotAfter.IsZero() && !c.NotAfter.After(now)
}

// Ref names a cluster or listener ("cluster" or "listener").
type Ref struct{ Kind, Name string }

// Secret is one secret as Envoy has it loaded. It never carries the secret body.
type Secret struct {
	Name, Kind, VersionInfo string
	State                   SecretStatus
	LastUpdated             time.Time
	Cert                    *CertInfo
	UsedBy                  []Ref
}

// SecretUse is a secret a cluster or listener references. Loaded is false when
// Envoy's secrets dump does not hold it.
type SecretUse struct {
	Name   string
	Loaded bool
}

// certForSecret picks the certificate /certs reports for the named secret,
// preferring the leaf over a CA. It is the only place that knows how a /certs
// entry maps back to an SDS secret.
func certForSecret(name string, certs []admin.Cert) *admin.Cert {
	var ca *admin.Cert
	for i := range certs {
		if certs[i].Path != name {
			continue
		}
		if !certs[i].CA {
			return &certs[i]
		}
		if ca == nil {
			ca = &certs[i]
		}
	}
	return ca
}

func secretStatus(s admin.SecretState) SecretStatus {
	switch s {
	case admin.SecretWarming:
		return SecretWarming
	case admin.SecretStatic:
		return SecretStatic
	default:
		return SecretActive
	}
}

// secretsFrom builds the sorted secret list and joins certificate facts by name.
func secretsFrom(scs []admin.SecretConfig, certs []admin.Cert, now time.Time) []Secret {
	out := make([]Secret, 0, len(scs))
	for _, sc := range scs {
		s := Secret{
			Name: sc.Name, Kind: sc.Kind, VersionInfo: sc.VersionInfo,
			State: secretStatus(sc.State), LastUpdated: sc.LastUpdated,
		}
		if c := certForSecret(sc.Name, certs); c != nil {
			s.Cert = &CertInfo{Serial: c.Serial, SANs: c.SANs, NotAfter: c.NotAfter, DaysLeft: c.DaysLeft}
		}
		out = append(out, s)
	}
	sortSecrets(out, now)
	return out
}

func secretSeverity(s Secret, now time.Time) int {
	switch {
	case s.State == SecretWarming:
		return 0
	case s.Cert != nil && s.Cert.Expired(now):
		return 1
	default:
		return 2
	}
}

func sortSecrets(ss []Secret, now time.Time) {
	sort.SliceStable(ss, func(i, j int) bool {
		si, sj := secretSeverity(ss[i], now), secretSeverity(ss[j], now)
		if si != sj {
			return si < sj
		}
		return ss[i].Name < ss[j].Name
	})
}

// SecretsNeedAttention counts secrets that are warming or whose certificate
// has expired.
func (s Snapshot) SecretsNeedAttention() int {
	n := 0
	for _, sec := range s.Secrets {
		if secretSeverity(sec, s.FetchedAt) < 2 {
			n++
		}
	}
	return n
}
```

Modify `internal/snapshot/snapshot.go`:

In `Snapshot` add:

```go
	Secrets      []Secret
	SecretsErr   string // "secrets unavailable: <reason>" when the dump failed
	CertsErr     string // "certificate details unavailable: <reason>" when /certs failed
```

In `Fetch`, extend the variable block and the goroutines:

```go
		scs          []admin.SecretConfig
		secretsErr   error
		certs        []admin.Cert
		certsErr     error
	)
	wg.Add(5)
	// ...existing three goroutines...
	go func() { defer wg.Done(); scs, secretsErr = c.SecretConfigs(ctx) }()
	go func() { defer wg.Done(); certs, certsErr = c.Certs(ctx) }()
```

and, after `sortListeners(out.Listeners)`:

```go
	switch {
	case secretsErr != nil:
		out.SecretsErr = "secrets unavailable: " + secretsErr.Error()
	case certsErr != nil:
		out.CertsErr = "certificate details unavailable: " + certsErr.Error()
		out.Secrets = secretsFrom(scs, nil, out.FetchedAt)
	default:
		out.Secrets = secretsFrom(scs, certs, out.FetchedAt)
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/snapshot/ -v`
Expected: PASS (demo-dependent tests skipped).

- [ ] **Step 5: Commit**

```bash
git add internal/snapshot
git commit -m "feat(snapshot): fetch secrets and join certificate facts"
```

---

### Task 5: "Used by" derivation

**Files:**
- Modify: `internal/snapshot/secret.go`, `internal/snapshot/snapshot.go`, `internal/snapshot/listener.go`
- Test: `internal/snapshot/secret_test.go` (append)

**Interfaces:**
- Consumes: `Cluster.Config.JSON`, `Listener.Config.JSON` (existing), `Secret`, `SecretUse`, `Ref` (Task 4).
- Produces:
  - `Cluster.Secrets []SecretUse` and `Listener.Secrets []SecretUse`.
  - `func secretRefs(raw []byte) []string`: sorted, de-duplicated secret names a config references.
  - `func linkSecrets(s *Snapshot)`: fills `Secret.UsedBy`, `Cluster.Secrets`, `Listener.Secrets`. When `s.SecretsErr != ""`, every use counts as loaded.

- [ ] **Step 1: Write the failing test**

Append to `internal/snapshot/secret_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/snapshot/ -run 'SecretRefs|LinkSecrets' -v`
Expected: FAIL to compile.

- [ ] **Step 3: Write the minimal implementation**

Add `Secrets []SecretUse` to `Cluster` (in `snapshot.go`) and to `Listener` (in `listener.go`).

Append to `internal/snapshot/secret.go` (add `bytes`, `encoding/json` and `strings` to the imports):

```go
// secretRefs returns the sorted, de-duplicated names under every key ending in
// sds_secret_config or sds_secret_configs. The walk is generic so it covers
// TLS certificates, validation contexts and combined contexts, upstream and
// downstream, without hard-coded paths. Configs with no such key are never
// parsed: a poll over thousands of clusters stays cheap.
func secretRefs(raw []byte) []string {
	if !bytes.Contains(raw, []byte("sds_secret_config")) {
		return nil
	}
	var doc any
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	seen := map[string]bool{}
	var walk func(v any)
	nameOf := func(v any) {
		if m, ok := v.(map[string]any); ok {
			if n, ok := m["name"].(string); ok && n != "" {
				seen[n] = true
			}
		}
	}
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				switch {
				case strings.HasSuffix(k, "sds_secret_configs"):
					if list, ok := child.([]any); ok {
						for _, e := range list {
							nameOf(e)
						}
					}
				case strings.HasSuffix(k, "sds_secret_config"):
					nameOf(child)
				}
				walk(child)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(doc)
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// linkSecrets fills Secret.UsedBy and the Secrets of every cluster and
// listener from the config JSON the snapshot already holds. A referenced name
// Envoy has not loaded is marked not loaded, unless the secrets dump failed:
// then nothing is known and nothing is flagged.
func linkSecrets(s *Snapshot) {
	idx := make(map[string]int, len(s.Secrets))
	for i := range s.Secrets {
		idx[s.Secrets[i].Name] = i
		s.Secrets[i].UsedBy = nil
	}
	uses := func(kind, name string, cfg ConfigInfo) []SecretUse {
		var out []SecretUse
		for _, ref := range secretRefs(cfg.JSON) {
			i, loaded := idx[ref]
			if loaded {
				s.Secrets[i].UsedBy = append(s.Secrets[i].UsedBy, Ref{Kind: kind, Name: name})
			}
			out = append(out, SecretUse{Name: ref, Loaded: loaded || s.SecretsErr != ""})
		}
		return out
	}
	for i := range s.Clusters {
		s.Clusters[i].Secrets = uses("cluster", s.Clusters[i].Name, s.Clusters[i].Config)
	}
	for i := range s.Listeners {
		s.Listeners[i].Secrets = uses("listener", s.Listeners[i].Name, s.Listeners[i].Config)
	}
}
```

In `Fetch`, call `linkSecrets(&out)` after the secrets `switch`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/snapshot/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/snapshot
git commit -m "feat(snapshot): derive which clusters and listeners use each secret"
```

---

### Task 6: Secrets view kind and list rows

**Files:**
- Create: `internal/ui/secret_item.go`
- Create: `internal/ui/secret_item_test.go`
- Modify: `internal/ui/view.go`
- Modify (fix, if they assume two tabs): `internal/ui/views_test.go`, `internal/ui/focus_test.go`, `internal/ui/model_test.go`

**Interfaces:**
- Consumes: `snapshot.Secret`, `Snapshot.SecretsErr`, `Snapshot.CertsErr`, `SecretsNeedAttention` (Task 4).
- Produces:
  - `kindSecrets` (after `kindListeners`) and its `resource`.
  - `type secretItem struct{ s snapshot.Secret; now time.Time }` implementing `resourceItem`; `func secretItems(ss []snapshot.Secret, now time.Time) []resourceItem`.
  - `Overview` and `OverviewMaxScroll` are completed in Task 7; here `Overview` returns `h` blank lines.
  - `Config()` returns `snapshot.ConfigInfo{Err: "secret content is never displayed"}`.

- [ ] **Step 1: Write the failing test**

Create `internal/ui/secret_item_test.go`:

```go
package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/envoyadmin/internal/snapshot"
)

func sec(name, version string, state snapshot.SecretStatus) snapshot.Secret {
	return snapshot.Secret{
		Name: name, VersionInfo: version, State: state, Kind: "tls_certificate",
		LastUpdated: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC),
	}
}

func secItem0(s snapshot.Secret) resourceItem { return secretItems([]snapshot.Secret{s}, fixedNow)[0] }

func TestSecretItemIdentity(t *testing.T) {
	it := secItem0(sec("server-cert", "12", snapshot.SecretActive))
	if it.Key() != "server-cert" || it.Title() != "server-cert" || it.OverviewName() != "Overview" || it.FilterText() != "server-cert" {
		t.Fatalf("%q %q %q %q", it.Key(), it.Title(), it.OverviewName(), it.FilterText())
	}
	if _, problem := it.Config().Render(); problem != "secret content is never displayed" {
		t.Fatalf("config note = %q", problem)
	}
}

func TestSecretRowGlyphsAndLayout(t *testing.T) {
	cases := []struct {
		state snapshot.SecretStatus
		glyph string
	}{{snapshot.SecretActive, "●"}, {snapshot.SecretStatic, "●"}, {snapshot.SecretWarming, "◐"}}
	for _, c := range cases {
		row := ansiStrip(secItem0(sec("server-cert", "12", c.state)).Row(rowCtx{Width: 36}))
		if !strings.HasPrefix(row, " "+c.glyph+" server-cert") || !strings.HasSuffix(row, "12") || lipgloss.Width(row) != 36 {
			t.Fatalf("%v: %q (width %d)", c.state, row, lipgloss.Width(row))
		}
	}
	expired := sec("old", "1", snapshot.SecretActive)
	expired.Cert = &snapshot.CertInfo{NotAfter: fixedNow.Add(-time.Hour)}
	if row := ansiStrip(secItem0(expired).Row(rowCtx{Width: 36})); !strings.HasPrefix(row, " ✗ old") {
		t.Fatalf("expired cert shows ✗: %q", row)
	}
}

func TestSecretRowNeverExceedsTinyPanes(t *testing.T) {
	long := sec(strings.Repeat("n", 80), "9999999", snapshot.SecretWarming)
	for _, w := range []int{1, 5, 12, 28, 36} {
		if got := lipgloss.Width(secItem0(long).Row(rowCtx{Width: w})); got > w {
			t.Fatalf("width %d: row is %d wide", w, got)
		}
	}
}

func TestSecretsResourceChrome(t *testing.T) {
	r := resources[kindSecrets]
	s := snapshot.Snapshot{FetchedAt: fixedNow, Secrets: []snapshot.Secret{sec("a", "1", snapshot.SecretWarming), sec("b", "1", snapshot.SecretActive)}}
	if r.tabLabel(s) != "Secrets (!)" || !r.warn(s) {
		t.Fatalf("warming secret must warn: %q", r.tabLabel(s))
	}
	if got := r.summary(s); got != "2 secrets · 1 need attention" {
		t.Fatalf("summary = %q", got)
	}
	s.Secrets = s.Secrets[1:]
	if r.tabLabel(s) != "Secrets (1)" || r.warn(s) {
		t.Fatalf("healthy label = %q", r.tabLabel(s))
	}
	s.SecretsErr = "secrets unavailable: boom"
	if r.tabLabel(s) != "Secrets (!)" || r.summary(s) != "secrets unavailable" || r.notice(s) != s.SecretsErr {
		t.Fatalf("failed dump: %q %q %q", r.tabLabel(s), r.summary(s), r.notice(s))
	}
	s.SecretsErr, s.CertsErr = "", "certificate details unavailable: boom"
	if r.notice(s) != s.CertsErr || r.warn(s) {
		t.Fatalf("certs-only failure is a notice, not a warning: %q", r.notice(s))
	}
}

func TestTabCyclesThroughThreeKinds(t *testing.T) {
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snap(cl("alpha", 1, 1))})
	for i, want := range []resourceKind{kindListeners, kindSecrets, kindClusters} {
		m = send(m, key("tab"))
		if m.active != want {
			t.Fatalf("tab %d: active = %v, want %v", i+1, m.active, want)
		}
	}
	m = send(m, key("shift+tab"))
	if m.active != kindSecrets {
		t.Fatalf("shift+tab from Clusters = %v", m.active)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/ui/ -run 'Secret|ThreeKinds' -v`
Expected: FAIL to compile.

- [ ] **Step 3: Write the minimal implementation**

In `internal/ui/view.go` add `kindSecrets` after `kindListeners` and append to `resources`:

```go
	kindSecrets: {
		label: "Secrets",
		items: func(s snapshot.Snapshot) []resourceItem { return secretItems(s.Secrets, s.FetchedAt) },
		count: func(s snapshot.Snapshot) int { return len(s.Secrets) },
		summary: func(s snapshot.Snapshot) string {
			if s.SecretsErr != "" {
				return "secrets unavailable"
			}
			return fmt.Sprintf("%s · %d need attention", plural(len(s.Secrets), "secret"), s.SecretsNeedAttention())
		},
		notice: func(s snapshot.Snapshot) string {
			if s.SecretsErr != "" {
				return s.SecretsErr
			}
			return s.CertsErr
		},
		warn:        func(s snapshot.Snapshot) bool { return s.SecretsErr != "" || s.SecretsNeedAttention() > 0 },
		emptyList:   "no secrets",
		emptyDetail: "no secret selected",
	},
```

Create `internal/ui/secret_item.go`:

```go
package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/envoyadmin/internal/snapshot"
)

const secretVersionW = 7

type secretItem struct {
	s   snapshot.Secret
	now time.Time
}

func secretItems(ss []snapshot.Secret, now time.Time) []resourceItem {
	out := make([]resourceItem, len(ss))
	for i, s := range ss {
		out[i] = secretItem{s: s, now: now}
	}
	return out
}

func (it secretItem) Key() string          { return it.s.Name }
func (it secretItem) FilterText() string   { return it.s.Name }
func (it secretItem) Title() string        { return it.s.Name }
func (it secretItem) OverviewName() string { return "Overview" }

// Config never shows content: the Config tab only says so.
func (it secretItem) Config() snapshot.ConfigInfo {
	return snapshot.ConfigInfo{Err: "secret content is never displayed"}
}

func (it secretItem) expired() bool { return it.s.Cert != nil && it.s.Cert.Expired(it.now) }

// secretGlyph carries the state without relying on colour.
func (it secretItem) glyph() (string, lipgloss.Style) {
	switch {
	case it.expired():
		return "✗", badStyle
	case it.s.State == snapshot.SecretWarming:
		return "◐", warnStyle
	default:
		return "●", okStyle
	}
}

func (it secretItem) Row(ctx rowCtx) string {
	glyph, style := it.glyph()
	avail := max(ctx.Width-3, 0) // prefix, glyph, space
	verW := max(min(lipgloss.Width(it.s.VersionInfo), secretVersionW, avail-nameMinW-1), 0)
	nameW := avail - verW
	if verW > 0 {
		nameW-- // gap
	}
	nameW = max(nameW, 0)

	plainName := trunc(it.s.Name, nameW)
	name := styledText(plainName, ctx.Hits, ctx.Cursor) + strings.Repeat(" ", max(nameW-lipgloss.Width(plainName), 0))
	line := cursorGlyph(ctx) + style.Render(glyph) + " " + name
	if verW > 0 {
		line += " " + dimStyle.Render(pad(trunc(it.s.VersionInfo, verW), verW))
	}
	return trunc(line, ctx.Width)
}

func (it secretItem) Overview(w, h, scroll int) string {
	return strings.Repeat("\n", max(h-1, 0)) // filled in by the Overview task
}

func (it secretItem) OverviewMaxScroll(w, h int) int { return 0 }
```

The row right-aligns nothing: version sits after the name, padded, so `strings.HasSuffix(row, "12")` holds only if the version fills its column. If `TestSecretRowGlyphsAndLayout` fails on the `HasSuffix`/width check, right-align the version cell with `strings.Repeat(" ", verW-lipgloss.Width(v)) + v` and pad the name so the row is exactly `ctx.Width` wide, as `listenerItem.Row` does for its address.

Then run `go test ./internal/ui/` and fix every existing test that assumed two tabs (the likely ones are in `views_test.go` and `focus_test.go`: a second `tab` used to return to Clusters). Change those expectations to the three-kind cycle; do not change production code for them.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/ui/ -run 'Secret|ThreeKinds|Tab' -v`
Expected: PASS. Golden tests fail now because every header gains a Secrets tab; that is handled in Task 9.

- [ ] **Step 5: Commit**

```bash
git add internal/ui
git commit -m "feat(ui): add the Secrets tab with a list of secrets"
```

---

### Task 7: Secret Overview and "Secrets" rows in cluster and listener Overviews

**Files:**
- Modify: `internal/ui/secret_item.go`, `internal/ui/endpoints.go`, `internal/ui/listener_item.go`
- Test: `internal/ui/secret_item_test.go` (append), `internal/ui/listener_item_test.go` (append), `internal/ui/endpoints_test.go` (append)

**Interfaces:**
- Consumes: `snapshot.Cluster.Secrets`, `snapshot.Listener.Secrets`, `snapshot.SecretUse`, `Secret.UsedBy`, `Secret.Cert` (Tasks 4 and 5); existing `prop`, `orDash`, `truncEllipsis`, `overviewLabelW`.
- Produces:
  - `func secretUseLine(u snapshot.SecretUse) string`: name, plus a "not loaded" marker in `badStyle` when `!u.Loaded`.
  - `secretItem.Overview/OverviewMaxScroll` rendering: name, State, Kind, Version, Last updated, certificate rows (Serial, SANs, Expires), then "Used by (N)" and one row per `Ref`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ui/secret_item_test.go`:

```go
func TestSecretOverviewShowsFactsAndUsedBy(t *testing.T) {
	s := sec("server-cert", "12", snapshot.SecretActive)
	s.Cert = &snapshot.CertInfo{Serial: "0a1b", SANs: []string{"a.example.com", "b.example.com"}, NotAfter: fixedNow.Add(7 * 24 * time.Hour), DaysLeft: 7}
	s.UsedBy = []snapshot.Ref{{Kind: "cluster", Name: "api"}, {Kind: "listener", Name: "0.0.0.0_8080"}}
	out := ansiStrip(secItem0(s).Overview(80, 20, 0))
	for _, want := range []string{
		"State", "active", "Kind", "tls_certificate", "Version", "12", "Last updated", "2026-10-14 10:00:00Z",
		"Serial", "0a1b", "SANs", "a.example.com, b.example.com", "Expires", "2026-10-21", "7 days",
		"Used by (2)", "cluster api", "listener 0.0.0.0_8080",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if lines := strings.Split(out, "\n"); len(lines) != 20 {
		t.Fatalf("Overview must be exactly h lines, got %d", len(lines))
	}
}

func TestSecretOverviewWithoutCertOrUsers(t *testing.T) {
	out := ansiStrip(secItem0(sec("bare", "", snapshot.SecretStatic)).Overview(80, 12, 0))
	if strings.Contains(out, "Serial") || !strings.Contains(out, "Used by (0)") || !strings.Contains(out, "not referenced") {
		t.Fatalf("bare secret:\n%s", out)
	}
	if !strings.Contains(out, "static") {
		t.Fatalf("static state:\n%s", out)
	}
}

func TestSecretOverviewExpiredCert(t *testing.T) {
	s := sec("old", "1", snapshot.SecretActive)
	s.Cert = &snapshot.CertInfo{Serial: "02", NotAfter: fixedNow.Add(-time.Hour)}
	if out := ansiStrip(secItem0(s).Overview(80, 12, 0)); !strings.Contains(out, "expired") {
		t.Fatalf("expired cert:\n%s", out)
	}
}

func TestSecretOverviewScrollsUsedBy(t *testing.T) {
	s := sec("busy", "1", snapshot.SecretActive)
	for i := 0; i < 30; i++ {
		s.UsedBy = append(s.UsedBy, snapshot.Ref{Kind: "cluster", Name: fmt.Sprintf("c%02d", i)})
	}
	it := secItem0(s)
	if it.OverviewMaxScroll(80, 12) == 0 {
		t.Fatal("30 users in 12 rows must scroll")
	}
	last := ansiStrip(it.Overview(80, 12, it.OverviewMaxScroll(80, 12)))
	if !strings.Contains(last, "c29") || strings.Contains(last, "c00") {
		t.Fatalf("scrolled to the end:\n%s", last)
	}
}

// Review focus: secret content must never be rendered. The snapshot types have
// no field for it, so this guards the decode-to-render path end to end.
func TestSecretContentNeverRendered(t *testing.T) {
	s := sec("server-cert", "12", snapshot.SecretActive)
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snapshot.Snapshot{FetchedAt: fixedNow, Secrets: []snapshot.Secret{s}}})
	m = send(send(m, key("tab")), key("tab"))
	for _, k := range []string{"o", "c"} {
		if v := m.View(); strings.Contains(v, "SENTINEL-PRIVATE-KEY") || strings.Contains(v, "BEGIN") {
			t.Fatalf("view leaked content:\n%s", v)
		}
		m = send(m, key(k))
	}
	if v := m.View(); !strings.Contains(v, "secret content is never displayed") {
		t.Fatalf("Config tab note missing:\n%s", v)
	}
}
```

(add `"fmt"` to that file's imports). Append to `listener_item_test.go`:

```go
func TestListenerOverviewListsSecrets(t *testing.T) {
	l := lst("api", "0.0.0.0:8080", snapshot.ListenerActive)
	l.Secrets = []snapshot.SecretUse{{Name: "server-cert", Loaded: true}, {Name: "ghost", Loaded: false}}
	out := ansiStrip(item0(l).Overview(100, 20, 0))
	if !strings.Contains(out, "Secrets") || !strings.Contains(out, "server-cert") || !strings.Contains(out, "ghost (not loaded)") {
		t.Fatalf("listener overview:\n%s", out)
	}
	none := ansiStrip(item0(lst("api", "0.0.0.0:8080", snapshot.ListenerActive)).Overview(100, 20, 0))
	if strings.Contains(none, "Secrets") {
		t.Fatalf("no row without secrets:\n%s", none)
	}
}
```

Append to `endpoints_test.go` (use that file's existing cluster helper, `cl(name, healthy, total)` from `model_test.go`):

```go
func TestClusterEndpointsViewListsSecrets(t *testing.T) {
	c := cl("api", 1, 2)
	c.Secrets = []snapshot.SecretUse{{Name: "mesh-ca", Loaded: true}, {Name: "ghost", Loaded: false}}
	out := ansiStrip(renderEndpoints(c, 100, 20, 0))
	if !strings.Contains(out, "Secrets") || !strings.Contains(out, "mesh-ca") || !strings.Contains(out, "ghost (not loaded)") {
		t.Fatalf("cluster view:\n%s", out)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/ui/ -run 'SecretOverview|Secrets|SecretContent' -v`
Expected: FAIL (assertions; blank Overview, no Secrets rows).

- [ ] **Step 3: Write the minimal implementation**

In `secret_item.go` replace the placeholder `Overview` and `OverviewMaxScroll` with:

```go
// secretUseLine is a secret a cluster or listener references; one Envoy has not
// loaded carries a marker, since that is the out-of-sync case.
func secretUseLine(u snapshot.SecretUse) string {
	if u.Loaded {
		return u.Name
	}
	return u.Name + " " + badStyle.Render("(not loaded)")
}

func (it secretItem) sticky() []string {
	s := it.s
	state := s.State.String()
	_, stStyle := it.glyph()
	updated := "-"
	if !s.LastUpdated.IsZero() {
		updated = s.LastUpdated.UTC().Format("2006-01-02 15:04:05Z")
	}
	lines := []string{
		prop("State", stStyle.Render(state)),
		prop("Kind", orDash(s.Kind)),
		prop("Version", orDash(s.VersionInfo)),
		prop("Last updated", updated),
	}
	if c := s.Cert; c != nil {
		lines = append(lines, prop("Serial", orDash(c.Serial)))
		if len(c.SANs) > 0 {
			lines = append(lines, prop("SANs", strings.Join(c.SANs, ", ")))
		}
		exp := "-"
		if !c.NotAfter.IsZero() {
			exp = c.NotAfter.UTC().Format("2006-01-02")
			if it.expired() {
				exp = badStyle.Render(exp + " (expired)")
			} else {
				exp += " (" + plural(int(c.DaysLeft), "day") + ")"
			}
		}
		lines = append(lines, prop("Expires", exp))
	}
	return append(lines, "", boldStyle.Render("Used by ("+strconv.Itoa(len(s.UsedBy))+")"))
}

func (it secretItem) usedByLines() []string {
	if len(it.s.UsedBy) == 0 {
		return []string{" " + dimStyle.Render("not referenced by any cluster or listener")}
	}
	out := make([]string, 0, len(it.s.UsedBy))
	for _, r := range it.s.UsedBy {
		out = append(out, " "+r.Kind+" "+r.Name)
	}
	return out
}

func (it secretItem) OverviewMaxScroll(_, h int) int {
	rows := h - len(it.sticky())
	if rows <= 0 {
		return 0
	}
	return max(len(it.usedByLines())-rows, 0)
}

func (it secretItem) Overview(w, h, scroll int) string {
	if h <= 0 {
		return ""
	}
	if w <= 0 {
		return strings.Repeat("\n", h-1)
	}
	sticky := it.sticky()
	var lines []string
	if h <= len(sticky) {
		lines = sticky[:h]
	} else {
		lines = append(lines, sticky...)
		users := it.usedByLines()
		rows := h - len(sticky)
		start := max(0, min(scroll, max(len(users)-rows, 0)))
		lines = append(lines, users[start:min(start+rows, len(users))]...)
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = truncEllipsis(l, w)
	}
	return strings.Join(lines, "\n")
}
```

Add `"strconv"` to the imports and drop the placeholder methods. `plural` is defined in `view.go` (`plural(1, "day")` gives "1 day", `plural(7, "day")` gives "7 days").

In `listener_item.go`, inside `sticky`, directly before the final `return append(lines, "", boldStyle.Render("Filter chains ..."))`, add:

```go
	if len(l.Secrets) > 0 {
		names := make([]string, len(l.Secrets))
		for i, u := range l.Secrets {
			names[i] = secretUseLine(u)
		}
		lines = append(lines, prop("Secrets", strings.Join(names, ", ")))
	}
```

In `endpoints.go`, locate where `endpointLines`/`renderEndpoints` draw the cluster's header rows and add the same row the same way (a `prop("Secrets", ...)` line after the existing summary rows, only when `len(c.Secrets) > 0`). Read `endpointLines` first and keep `maxEndpointScroll` consistent with the added sticky row, exactly as `listenerItem.sticky` is counted by `OverviewMaxScroll`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/ui/ -run 'SecretOverview|Secrets|SecretContent|ListenerOverview|ClusterEndpoints' -v`
Expected: PASS. If an existing Overview or Endpoints test now fails because a row count changed, those tests set no `Secrets`, so no row is added and they must still pass; if one does fail, the sticky-row accounting is wrong.

- [ ] **Step 5: Commit**

```bash
git add internal/ui
git commit -m "feat(ui): show secret details and the secrets each cluster and listener use"
```

---

### Task 8: Demo server secrets and `/certs`

**Files:**
- Modify: `internal/demo/scenario.go`, `internal/demo/server.go`
- Modify: `internal/demo/server_test.go`
- Modify: `internal/snapshot/secret_test.go` (remove the Task 4 skips)

**Interfaces:**
- Consumes: the request shapes of Tasks 1 and 3.
- Produces:
  - `demo.Secret{Name, Version, Kind, State string; Cert *DemoCert}`; `demo.DemoCert{Serial string; SANs []string; DaysLeft int; CA bool}`.
  - `Scenario.Secrets []Secret`; `Cluster.Secrets []string` and `Listener.Secrets []string` (names, emitted as SDS references in their config JSON).
  - `Scenario.SecretsJSON(resource string) ([]byte, bool)` and `Scenario.CertsJSON() []byte`; routes `config_dump?resource=dynamic_active_secrets|dynamic_warming_secrets|static_secrets` and `/certs`.

- [ ] **Step 1: Write the failing test**

Append to `internal/demo/server_test.go` (match the file's existing request helper; it uses `httptest` against `NewServer`; if no helper exists, use `httptest.NewServer(NewServer(Degraded()))` and `http.Get`):

```go
func TestServerServesSecretsAndCerts(t *testing.T) {
	srv := httptest.NewServer(NewServer(Degraded()))
	defer srv.Close()
	get := func(path string) string {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %s", path, resp.Status)
		}
		return string(b)
	}
	if b := get("/config_dump?resource=dynamic_active_secrets"); !strings.Contains(b, "SecretsConfigDump.DynamicSecret") || !strings.Contains(b, "server-cert") {
		t.Fatalf("active: %s", b)
	}
	if b := get("/config_dump?resource=dynamic_warming_secrets"); !strings.Contains(b, "rotating-cert") {
		t.Fatalf("warming: %s", b)
	}
	if b := get("/config_dump?resource=static_secrets"); !strings.Contains(b, "SecretsConfigDump.StaticSecret") {
		t.Fatalf("static: %s", b)
	}
	if b := get("/certs"); !strings.Contains(b, "server-cert") || !strings.Contains(b, "days_until_expiration") {
		t.Fatalf("certs: %s", b)
	}
	if b := get("/config_dump?resource=dynamic_active_secrets"); strings.Contains(strings.ToLower(b), "private_key") && !strings.Contains(b, "[redacted]") {
		t.Fatal("the demo mimics Envoy: private keys are redacted in the dump")
	}
	if b := get("/config_dump?resource=dynamic_active_clusters"); !strings.Contains(b, "tls_certificate_sds_secret_configs") {
		t.Fatalf("a demo cluster must reference a secret: %s", b)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/demo/ -run SecretsAndCerts -v`
Expected: FAIL (`unsupported resource` or 404).

- [ ] **Step 3: Write the minimal implementation**

In `scenario.go` add the types and fields:

```go
type DemoCert struct {
	Serial   string
	SANs     []string
	DaysLeft int
	CA       bool
}

// Secret is one entry of Envoy's secrets dump. State is "active", "warming" or
// "static".
type Secret struct {
	Name, Version, Kind, State string
	Cert                       *DemoCert
}
```

`Scenario` gains `Secrets []Secret`; `Cluster` and `Listener` gain `Secrets []string`.

Emit SDS references inside the cluster JSON (in the `cl` map of `ConfigDumpJSON`) and the listener JSON (in `Chain.json` through `Listener.json`; put them on the first chain's `transport_socket`):

```go
func sdsContext(names []string) map[string]any {
	cfgs := make([]any, 0, len(names))
	for _, n := range names {
		cfgs = append(cfgs, map[string]any{"name": n, "sds_config": map[string]any{"path_config_source": map[string]any{"path": "/etc/envoy/sds.yaml"}}})
	}
	return map[string]any{
		"name": "envoy.transport_sockets.tls",
		"typed_config": map[string]any{
			"@type":              "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext",
			"common_tls_context": map[string]any{"tls_certificate_sds_secret_configs": cfgs},
		},
	}
}
```

Set `cl["transport_socket"] = sdsContext(c.Secrets)` when `len(c.Secrets) > 0`, and in `Listener.json` set `chains[0].(map[string]any)["transport_socket"] = sdsContext(l.Secrets)` when `len(l.Secrets) > 0 && len(chains) > 0`.

Add the dump builders:

```go
// SecretsJSON returns the dump for ?resource=dynamic_active_secrets,
// dynamic_warming_secrets or static_secrets, one configs[] element per secret
// as Envoy emits it. Like Envoy, it redacts the private key.
func (s Scenario) SecretsJSON(resource string) ([]byte, bool) {
	var want, typ string
	switch resource {
	case "dynamic_active_secrets":
		want, typ = "active", "DynamicSecret"
	case "dynamic_warming_secrets":
		want, typ = "warming", "DynamicSecret"
	case "static_secrets":
		want, typ = "static", "StaticSecret"
	default:
		return nil, false
	}
	configs := []any{}
	for _, sec := range s.Secrets {
		if sec.State != want {
			continue
		}
		body := map[string]any{"name": sec.Name}
		switch sec.Kind {
		case "validation_context":
			body["validation_context"] = map[string]any{"trusted_ca": map[string]any{"inline_bytes": "[redacted]"}}
		default:
			body["tls_certificate"] = map[string]any{
				"certificate_chain": map[string]any{"inline_bytes": "[redacted]"},
				"private_key":       map[string]any{"inline_bytes": "[redacted]"},
			}
		}
		e := map[string]any{
			"@type":        "type.googleapis.com/envoy.admin.v3.SecretsConfigDump." + typ,
			"name":         sec.Name,
			"last_updated": s.Updated.Format(time.RFC3339Nano),
			"secret":       body,
		}
		if want != "static" {
			e["version_info"] = sec.Version
		}
		configs = append(configs, e)
	}
	return mustJSON(map[string]any{"configs": configs}), true
}

// CertsJSON mirrors /certs: one group per secret that carries a certificate.
func (s Scenario) CertsJSON() []byte {
	groups := []any{}
	for _, sec := range s.Secrets {
		c := sec.Cert
		if c == nil {
			continue
		}
		sans := []any{}
		for _, n := range c.SANs {
			sans = append(sans, map[string]any{"dns": n})
		}
		d := map[string]any{
			"path": sec.Name, "serial_number": c.Serial, "subject_alt_names": sans,
			"days_until_expiration": strconv.Itoa(c.DaysLeft),
			"valid_from":            s.Updated.AddDate(-1, 0, 0).Format(time.RFC3339),
			"expiration_time":       s.Updated.AddDate(0, 0, c.DaysLeft).Format(time.RFC3339),
		}
		key := "cert_chain"
		if c.CA {
			key = "ca_cert"
		}
		groups = append(groups, map[string]any{key: []any{d}})
	}
	return mustJSON(map[string]any{"certificates": groups})
}
```

(If Task 2's real capture used a different `path` identification, make `CertsJSON` emit the same identification the capture shows, so the demo and `certForSecret` agree.)

In `ConfigDumpJSON` add a first lookup: `if b, ok := s.SecretsJSON(resource); ok { return b, true }` ahead of the `switch`. In `server.go` add `case "/certs": _, _ = w.Write(sc.CertsJSON())`. Also deep-copy `Secrets` and the new `Secrets []string` fields in `Server.Update`.

Give the scenarios data: add to `base()` a helper `demoSecrets()` returning

```go
[]Secret{
	{Name: "server-cert", Version: "12", Kind: "tls_certificate", State: "active",
		Cert: &DemoCert{Serial: "0a1b2c", SANs: []string{"api.example.com", "*.internal"}, DaysLeft: 62}},
	{Name: "mesh-ca", Version: "3", Kind: "validation_context", State: "active",
		Cert: &DemoCert{Serial: "01", DaysLeft: 900, CA: true}},
	{Name: "rotating-cert", Version: "8", Kind: "tls_certificate", State: "warming"},
	{Name: "bootstrap-cert", Kind: "tls_certificate", State: "static",
		Cert: &DemoCert{Serial: "ff00", DaysLeft: 0}},
}
```

and reference them: `api-gateway` gets `Secrets: []string{"server-cert", "mesh-ca"}`, `orders-db` gets `[]string{"mesh-ca", "missing-cert"}` (an unloaded reference), and the `0.0.0.0_8080` listener gets `Secrets: []string{"server-cert"}`. `Big(n)` keeps no secrets.

- [ ] **Step 4: Run the tests to verify they pass**

Remove the three `t.Skip` lines added in Task 4, then run:

Run: `go test ./internal/demo/ ./internal/snapshot/ -v`
Expected: PASS, including the Task 4 demo-based tests and an extra assertion you should add to `TestFetchSecretsFromDemo`: the secret `server-cert` has `UsedBy` containing `{cluster api-gateway}` and `{listener 0.0.0.0_8080}`, and the `orders-db` cluster has a `SecretUse{"missing-cert", false}`.

- [ ] **Step 5: Commit**

```bash
git add internal/demo internal/snapshot
git commit -m "feat(demo): serve secrets and certs from the fake Envoy"
```

---

### Task 9: Goldens, end-to-end and docs

**Files:**
- Modify: `internal/ui/golden_test.go`, `internal/ui/e2e_test.go`
- Regenerate: `internal/ui/testdata/*.golden`
- Modify: `README.md`

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ui/golden_test.go`:

```go
func TestGoldenSecretsList(t *testing.T) {
	m := loaded(t, demo.Degraded(), 120, 30)
	golden(t, send(send(m, key("tab")), key("tab")))
}

func TestGoldenSecretOverview(t *testing.T) {
	m := loaded(t, demo.Degraded(), 120, 30)
	m = send(send(m, key("tab")), key("tab"))
	golden(t, send(m, key("enter")))
}

func TestGoldenSecretsFailedInline(t *testing.T) {
	m := sized(New(goldenOpts()), 120, 30)
	s := fetchSnap(t, demo.Degraded())
	s.Secrets, s.SecretsErr = nil, "secrets unavailable: GET /config_dump: 500 Internal Server Error"
	m = send(send(send(m, snapshotMsg{snap: s}), key("tab")), key("tab"))
	golden(t, m)
}
```

In `e2e_test.go`, next to the existing `waitFor(t, tm, "Clusters (6)", "Listeners (7)")`, assert the new label `Secrets (4)` for the degraded demo, following that test's style; add a case mirroring the listeners one at lines 105 to 110: inject a `demo.Fault{Status: 500, Match: "secrets"}` and wait for `Secrets (!)`, then clear it and wait for `Secrets (4)`, confirming the global banner never shows (`disconnected` absent).

- [ ] **Step 2: Run to see the failures**

Run: `go test ./internal/ui/ -v 2>&1 | tail -40`
Expected: the new goldens fail ("no golden file") and every existing golden fails because the header now has a third tab.

- [ ] **Step 3: Regenerate and review the goldens**

Run: `make test-update`
Then review the diff: `git diff --stat internal/ui/testdata` and open at least `TestGoldenHealthy.golden`, `TestGoldenListenerConfig.golden` and the three new files. Check:
- every changed golden differs only by the added `Secrets (N)` tab and, for goldens of `api-gateway`, `orders-db` and `0.0.0.0_8080`, the new "Secrets" row;
- `TestGoldenSecretsList.golden` shows `rotating-cert` first (warming), then by name, with glyphs and versions;
- no golden contains `[redacted]`, `PRIVATE`, or key material.

If any golden differs in other ways, stop and fix the cause instead of accepting it.

- [ ] **Step 4: Update the README**

In `README.md`: change "It has two tabs, **Clusters** ... and **Listeners**" to three tabs and add a bullet after the Listeners entries:

```markdown
- Secrets tab: every secret Envoy has loaded, with state (`●` active or static, `◐` warming, `✗` certificate expired), name and `version_info`; warming and expired ones sort first. `/` fuzzy-filters by name.
- Secret Overview (`o`): kind (`tls_certificate`, `validation_context`, ...), version, last updated, the certificate's serial, SANs and expiry from `/certs`, and the clusters and listeners that reference the secret. Secret content is never fetched or shown; the Config tab (`c`) only says so. The cluster and listener Overviews list the secrets they use, and mark one Envoy has not loaded as `(not loaded)`.
- A failing secrets dump never raises the error banner: the tab reads `Secrets (!)` with an inline message, and a failing `/certs` only drops the certificate details.
```

- [ ] **Step 5: Run the whole suite and lint**

Run: `make test && make lint`
Expected: PASS, no lint findings. Then try it: `make run`, press `tab` twice and check the list, `enter` on `server-cert`, `o`, `c`, and the Secrets row on the `api-gateway` cluster and on the `0.0.0.0_8080` listener.

- [ ] **Step 6: Commit**

```bash
git add README.md internal/ui
git commit -m "docs: document the Secrets tab and refresh the goldens"
```

---

## Self-review

- **Spec coverage:** data fetching and decoding (Tasks 1 to 3), join and snapshot types (Task 4), "used by" and not-loaded (Task 5), tab, rows, Overview, Config note, tab label warnings, cluster and listener rows (Tasks 6 and 7), demo and README (Tasks 8 and 9), errors (Tasks 4, 6 and 9), testing items: real capture (2), join, `UsedBy`, not-loaded (4 and 5), goldens (9), sentinel test (1 for the decoder, 7 for rendering), the open `/certs` item (Tasks 2 and 3, with `certForSecret` as the single place to change).
- **Placeholders:** the `PATH` marker in Task 3's real-capture test and the endpoint-row wording in Task 7's `endpoints.go` step depend on the Task 2 capture and on reading `endpointLines`; both are stated as explicit instructions, not left open.
- **Types:** `SecretConfig`, `Cert`, `Secret`, `CertInfo`, `Ref`, `SecretUse`, `secretsFrom`, `certForSecret`, `secretRefs`, `linkSecrets`, `secretItems`, `kindSecrets` are named identically across tasks.
