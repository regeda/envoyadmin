package admin

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	typeDynamicCluster = ".ClustersConfigDump.DynamicCluster"
	typeStaticCluster  = ".ClustersConfigDump.StaticCluster"
	typeClustersDump   = ".ClustersConfigDump"
)

type entry struct {
	VersionInfo string          `json:"version_info"`
	LastUpdated string          `json:"last_updated"`
	Cluster     json.RawMessage `json:"cluster"`
}

type clustersDump struct {
	Static  []entry `json:"static_clusters"`
	Dynamic []entry `json:"dynamic_active_clusters"`
}

// decodeClusterConfigs walks /config_dump as raw JSON so a cluster whose config
// holds an Any type that is not compiled in can never fail the whole decode.
//
// With ?resource=<repeated field> Envoy emits one configs[] element per cluster,
// each typed ClustersConfigDump.DynamicCluster or .StaticCluster, with
// version_info, cluster and last_updated at the element's top level. A plain
// /config_dump instead has one wrapped ClustersConfigDump holding nested
// arrays; that shape is still accepted, picking the list named by static.
// Anything unrecognised or malformed is skipped rather than failing the fetch.
func decodeClusterConfigs(body []byte, static bool) ([]ClusterConfig, error) {
	var dump struct {
		Configs []json.RawMessage `json:"configs"`
	}
	if err := json.Unmarshal(body, &dump); err != nil {
		return nil, err
	}
	var out []ClusterConfig
	add := func(e entry, isStatic bool) {
		if cc, ok := clusterConfig(e, isStatic); ok {
			out = append(out, cc)
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
		case strings.HasSuffix(typ, typeDynamicCluster), strings.HasSuffix(typ, typeStaticCluster):
			var e entry
			if json.Unmarshal(raw, &e) == nil {
				add(e, strings.HasSuffix(typ, typeStaticCluster))
			}
		case strings.HasSuffix(typ, typeClustersDump):
			var cd clustersDump
			if json.Unmarshal(raw, &cd) != nil {
				continue
			}
			entries := cd.Dynamic
			if static {
				entries = cd.Static
			}
			for _, e := range entries {
				add(e, static)
			}
		}
	}
	return out, nil
}

func clusterConfig(e entry, static bool) (ClusterConfig, bool) {
	var cl struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		ClusterType struct {
			Name string `json:"name"`
		} `json:"cluster_type"`
	}
	if json.Unmarshal(e.Cluster, &cl) != nil || cl.Name == "" {
		return ClusterConfig{}, false
	}
	typ := cl.Type
	switch {
	case cl.ClusterType.Name != "":
		typ = cl.ClusterType.Name
	case typ == "":
		typ = "STATIC" // proto3 default, omitted by Envoy
	}
	updated, _ := time.Parse(time.RFC3339Nano, e.LastUpdated)
	return ClusterConfig{
		Name: cl.Name, Type: typ, VersionInfo: e.VersionInfo,
		LastUpdated: updated, Static: static, JSON: e.Cluster,
	}, true
}
