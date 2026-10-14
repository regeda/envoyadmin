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
