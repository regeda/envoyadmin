// Package snapshot turns admin API payloads into plain Go types. The UI never
// sees protobuf types.
package snapshot

import (
	"context"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	adminv3 "github.com/envoyproxy/go-control-plane/envoy/admin/v3"

	"github.com/regeda/txenvoy/internal/admin"
)

type Health int

const (
	Healthy Health = iota
	Empty
	Degraded
	Unhealthy
)

func (h Health) String() string {
	return [...]string{"healthy", "empty", "degraded", "unhealthy"}[h]
}

type Host struct {
	Address, Zone               string
	Flags                       []string
	Weight                      uint32
	RqActive, RqError, CxActive uint64
}

func (h Host) Healthy() bool {
	for _, f := range h.Flags {
		if IsUnhealthyFlag(f) {
			return false
		}
	}
	return true
}

func (h Host) Ejected() bool {
	for _, f := range h.Flags {
		if f == "FAILED_OUTLIER_CHECK" {
			return true
		}
	}
	return false
}

type Cluster struct {
	Name, Type string
	Hosts      []Host
	Config     ConfigInfo
}

func (c Cluster) HealthyCount() int {
	n := 0
	for _, h := range c.Hosts {
		if h.Healthy() {
			n++
		}
	}
	return n
}

func (c Cluster) EjectedCount() int {
	n := 0
	for _, h := range c.Hosts {
		if h.Ejected() {
			n++
		}
	}
	return n
}

func (c Cluster) Health() Health {
	switch healthy := c.HealthyCount(); {
	case len(c.Hosts) == 0:
		return Empty
	case healthy == len(c.Hosts):
		return Healthy
	case healthy == 0:
		return Unhealthy
	default:
		return Degraded
	}
}

type Snapshot struct {
	Clusters  []Cluster
	Version   string
	FetchedAt time.Time
}

// UnhealthyClusters counts clusters that are Degraded or Unhealthy.
func (s Snapshot) UnhealthyClusters() int {
	n := 0
	for _, c := range s.Clusters {
		if h := c.Health(); h == Degraded || h == Unhealthy {
			n++
		}
	}
	return n
}

// Fetch calls the admin API and joins /clusters with the config dump by name.
// Only a /clusters failure is fatal; missing config or server info degrade the
// snapshot instead.
func Fetch(ctx context.Context, c admin.Client) (Snapshot, error) {
	cl, err := c.Clusters(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	var (
		wg     sync.WaitGroup
		cfgs   []admin.ClusterConfig
		cfgErr error
		info   *adminv3.ServerInfo
	)
	wg.Add(2)
	go func() { defer wg.Done(); cfgs, cfgErr = c.ClusterConfigs(ctx) }()
	go func() { defer wg.Done(); info, _ = c.ServerInfo(ctx) }()
	wg.Wait()

	cfgByName := make(map[string]admin.ClusterConfig, len(cfgs))
	for _, cc := range cfgs {
		cfgByName[cc.Name] = cc
	}

	out := Snapshot{FetchedAt: time.Now(), Version: parseVersion(info.GetVersion())}
	for _, st := range cl.GetClusterStatuses() {
		cluster := Cluster{Name: st.GetName(), Type: "?"}
		for _, hs := range st.GetHostStatuses() {
			cluster.Hosts = append(cluster.Hosts, hostFrom(hs))
		}
		switch cc, found := cfgByName[st.GetName()]; {
		case cfgErr != nil:
			cluster.Config.Err = "config unavailable: " + cfgErr.Error()
		case !found:
			cluster.Config.Err = "config unavailable: not present in config_dump"
		default:
			cluster.Type = cc.Type
			cluster.Config = newConfigInfo(cc.JSON)
			cluster.Config.VersionInfo = cc.VersionInfo
			cluster.Config.LastUpdated = cc.LastUpdated
			cluster.Config.Static = cc.Static
		}
		out.Clusters = append(out.Clusters, cluster)
	}
	sortClusters(out.Clusters)
	return out, nil
}

func severity(h Health) int {
	switch h {
	case Unhealthy:
		return 0
	case Degraded:
		return 1
	default:
		return 2
	}
}

func sortClusters(cs []Cluster) {
	sort.SliceStable(cs, func(i, j int) bool {
		si, sj := severity(cs[i].Health()), severity(cs[j].Health())
		if si != sj {
			return si < sj
		}
		return cs[i].Name < cs[j].Name
	})
}

func hostFrom(hs *adminv3.HostStatus) Host {
	h := Host{
		Zone:   hs.GetLocality().GetZone(),
		Weight: hs.GetWeight(),
		Flags:  flagsOf(hs.GetHealthStatus()),
	}
	if sa := hs.GetAddress().GetSocketAddress(); sa != nil {
		h.Address = net.JoinHostPort(sa.GetAddress(), strconv.Itoa(int(sa.GetPortValue())))
	} else if p := hs.GetAddress().GetPipe(); p != nil {
		h.Address = "unix:" + p.GetPath()
	}
	for _, m := range hs.GetStats() {
		switch m.GetName() {
		case "rq_active":
			h.RqActive = m.GetValue()
		case "rq_error":
			h.RqError = m.GetValue()
		case "cx_active":
			h.CxActive = m.GetValue()
		}
	}
	return h
}

// parseVersion extracts "1.31.0" from "hash/1.31.0/Clean/RELEASE/BoringSSL".
func parseVersion(v string) string {
	if parts := strings.Split(v, "/"); len(parts) >= 2 {
		return parts[1]
	}
	return v
}
