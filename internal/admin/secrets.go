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
	LastUpdated time.Time
	Cert        *CertFacts
	Name        string
	VersionInfo string
	Kind        string
	State       SecretState
}

type rawSecret struct {
	Secret      map[string]json.RawMessage `json:"secret"`
	Name        string                     `json:"name"`
	VersionInfo string                     `json:"version_info"`
	LastUpdated string                     `json:"last_updated"`
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

// secretConfig builds a SecretConfig from a rawSecret. The name field is required
// at the top level; we do not fall back to secret.name. Elements without a top-level
// name are deliberately skipped.
func secretConfig(r rawSecret, state SecretState) (SecretConfig, bool) {
	if r.Name == "" {
		return SecretConfig{}, false
	}
	return SecretConfig{
		Name: r.Name, VersionInfo: r.VersionInfo, Kind: secretKind(r.Secret),
		LastUpdated: parseTime(r.LastUpdated), State: state,
		Cert: certFacts(r.Secret),
	}, true
}

// decodeSecretConfigs walks /config_dump as raw JSON, like decodeClusterConfigs.
// With ?resource=<list> Envoy emits one configs[] element per secret, typed
// SecretsConfigDump.DynamicSecret or .StaticSecret; the requested resource, not
// the element, says whether a dynamic one is active or warming. A plain
// /config_dump has one wrapped SecretsConfigDump; the list matching state is
// taken from it. Anything unrecognised or malformed is skipped, never fatal.
func decodeSecretConfigs(body []byte, state SecretState) ([]SecretConfig, error) {
	var out []SecretConfig
	err := walkConfigs(body, func(raw json.RawMessage, typ string) {
		out = append(out, secretElements(raw, typ, state)...)
	})
	return out, err
}

// secretElements decodes one configs[] element into zero or more secrets.
func secretElements(raw json.RawMessage, typ string, state SecretState) []SecretConfig {
	var list []rawSecret
	switch {
	case strings.HasSuffix(typ, typeDynamicSecret), strings.HasSuffix(typ, typeStaticSecret):
		var r rawSecret
		if json.Unmarshal(raw, &r) != nil {
			return nil
		}
		list = []rawSecret{r}
	case strings.HasSuffix(typ, typeSecretsDump):
		var sd secretsDump
		if json.Unmarshal(raw, &sd) != nil {
			return nil
		}
		list = sd.Active
		switch state {
		case SecretWarming:
			list = sd.Warming
		case SecretStatic:
			list = sd.Static
		}
	}
	var out []SecretConfig
	for _, r := range list {
		if sc, ok := secretConfig(r, state); ok {
			out = append(out, sc)
		}
	}
	return out
}
