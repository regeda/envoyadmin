package snapshot

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
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
	DaysLeft int64 // whole days until NotAfter at fetch time; not positive once expired
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

func certInfo(f *admin.CertFacts, now time.Time) *CertInfo {
	if f == nil {
		return nil
	}
	c := &CertInfo{Serial: f.Serial, SANs: f.SANs, NotAfter: f.NotAfter}
	if !f.NotAfter.IsZero() {
		c.DaysLeft = int64(f.NotAfter.Sub(now).Hours() / 24)
	}
	return c
}

// secretsFrom builds the sorted secret list.
func secretsFrom(scs []admin.SecretConfig, now time.Time) []Secret {
	out := make([]Secret, 0, len(scs))
	for _, sc := range scs {
		out = append(out, Secret{
			Name: sc.Name, Kind: sc.Kind, VersionInfo: sc.VersionInfo,
			State: secretStatus(sc.State), LastUpdated: sc.LastUpdated,
			Cert: certInfo(sc.Cert, now),
		})
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
	// Envoy lists a rotating secret twice (active and warming) under one name.
	idx := make(map[string][]int, len(s.Secrets))
	for i := range s.Secrets {
		idx[s.Secrets[i].Name] = append(idx[s.Secrets[i].Name], i)
		s.Secrets[i].UsedBy = nil
	}
	uses := func(kind, name string, cfg ConfigInfo) []SecretUse {
		var out []SecretUse
		for _, ref := range secretRefs(cfg.JSON) {
			entries := idx[ref]
			loaded := len(entries) > 0
			for _, i := range entries {
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
