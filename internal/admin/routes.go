package admin

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

const (
	typeDynamicRoute = ".RoutesConfigDump.DynamicRouteConfig"
	typeStaticRoute  = ".RoutesConfigDump.StaticRouteConfig"
	typeRoutesDump   = ".RoutesConfigDump"
)

// RouteConfig is one route configuration as Envoy emitted it.
type RouteConfig struct {
	Name, VersionInfo string
	LastUpdated       time.Time
	Static            bool
	// JSON is the route_config object.
	JSON []byte
}

// routeNameRegex builds Envoy's name_regex for a set of exact names; Envoy
// matches it against the whole route-config name.
func routeNameRegex(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = regexp.QuoteMeta(n)
	}
	return "(?:" + strings.Join(q, "|") + ")"
}

type rawRoute struct {
	VersionInfo string          `json:"version_info"`
	LastUpdated string          `json:"last_updated"`
	RouteConfig json.RawMessage `json:"route_config"`
}

type routesDump struct {
	Static  []rawRoute `json:"static_route_configs"`
	Dynamic []rawRoute `json:"dynamic_route_configs"`
}

// routeConfig builds a RouteConfig; elements without route_config.name are skipped.
func routeConfig(r rawRoute, static bool) (RouteConfig, bool) {
	var head struct {
		Name string `json:"name"`
	}
	if len(r.RouteConfig) == 0 || json.Unmarshal(r.RouteConfig, &head) != nil || head.Name == "" {
		return RouteConfig{}, false
	}
	return RouteConfig{
		Name: head.Name, VersionInfo: r.VersionInfo, LastUpdated: parseTime(r.LastUpdated),
		Static: static, JSON: []byte(r.RouteConfig),
	}, true
}

// decodeRouteConfigs walks /config_dump as raw JSON, like decodeListenerConfigs.
// With ?resource=<list> each configs[] element is one DynamicRouteConfig or
// StaticRouteConfig; a plain dump has one wrapped RoutesConfigDump, from which
// the list matching static is taken. Malformed or unrecognised elements are skipped.
func decodeRouteConfigs(body []byte, static bool) ([]RouteConfig, error) {
	var dump struct {
		Configs []json.RawMessage `json:"configs"`
	}
	if err := json.Unmarshal(body, &dump); err != nil {
		return nil, err
	}
	var out []RouteConfig
	add := func(r rawRoute, static bool) {
		if rc, ok := routeConfig(r, static); ok {
			out = append(out, rc)
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
		case strings.HasSuffix(typ, typeDynamicRoute), strings.HasSuffix(typ, typeStaticRoute):
			var r rawRoute
			if json.Unmarshal(raw, &r) == nil {
				add(r, strings.HasSuffix(typ, typeStaticRoute))
			}
		case strings.HasSuffix(typ, typeRoutesDump):
			var rd routesDump
			if json.Unmarshal(raw, &rd) != nil {
				continue
			}
			list := rd.Dynamic
			if static {
				list = rd.Static
			}
			for _, r := range list {
				add(r, static)
			}
		}
	}
	return out, nil
}
