package admin

import (
	"encoding/json"
	"strings"
	"time"
)

type entry struct {
	VersionInfo string          `json:"version_info"`
	LastUpdated string          `json:"last_updated"`
	Cluster     json.RawMessage `json:"cluster"`
}

type clustersDump struct {
	Type    string  `json:"@type"`
	Static  []entry `json:"static_clusters"`
	Dynamic []entry `json:"dynamic_active_clusters"`
}

// decodeClusterConfigs walks /config_dump as raw JSON so a cluster whose config
// holds an Any type that is not compiled in can never fail the whole decode.
func decodeClusterConfigs(body []byte, static bool) ([]ClusterConfig, error) {
	var dump struct {
		Configs []json.RawMessage `json:"configs"`
	}
	if err := json.Unmarshal(body, &dump); err != nil {
		return nil, err
	}
	var out []ClusterConfig
	for _, raw := range dump.Configs {
		var cd clustersDump
		if err := json.Unmarshal(raw, &cd); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(cd.Type, ".ClustersConfigDump") {
			continue
		}
		entries := cd.Dynamic
		if static {
			entries = cd.Static
		}
		for _, e := range entries {
			var cl struct {
				Name        string `json:"name"`
				Type        string `json:"type"`
				ClusterType struct {
					Name string `json:"name"`
				} `json:"cluster_type"`
			}
			if err := json.Unmarshal(e.Cluster, &cl); err != nil {
				return nil, err
			}
			typ := cl.Type
			switch {
			case cl.ClusterType.Name != "":
				typ = cl.ClusterType.Name
			case typ == "":
				typ = "STATIC" // proto3 default, omitted by Envoy
			}
			updated, _ := time.Parse(time.RFC3339Nano, e.LastUpdated)
			out = append(out, ClusterConfig{
				Name: cl.Name, Type: typ, VersionInfo: e.VersionInfo,
				LastUpdated: updated, Static: static, JSON: e.Cluster,
			})
		}
	}
	return out, nil
}
