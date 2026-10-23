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
	LastUpdated time.Time
	Name        string
	VersionInfo string
	JSON        []byte
	Static      bool
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
	var out []RouteConfig
	err := walkConfigs(body, func(raw json.RawMessage, typ string) {
		out = append(out, routeElements(raw, typ, static)...)
	})
	return out, err
}

// routeElements decodes one configs[] element into zero or more route configs.
func routeElements(raw json.RawMessage, typ string, static bool) []RouteConfig {
	var list []rawRoute
	isStatic := static
	switch {
	case strings.HasSuffix(typ, typeDynamicRoute), strings.HasSuffix(typ, typeStaticRoute):
		var r rawRoute
		if json.Unmarshal(raw, &r) != nil {
			return nil
		}
		list = []rawRoute{r}
		isStatic = strings.HasSuffix(typ, typeStaticRoute)
	case strings.HasSuffix(typ, typeRoutesDump):
		var rd routesDump
		if json.Unmarshal(raw, &rd) != nil {
			return nil
		}
		list = rd.Dynamic
		if static {
			list = rd.Static
		}
	}
	var out []RouteConfig
	for _, r := range list {
		if rc, ok := routeConfig(r, isStatic); ok {
			out = append(out, rc)
		}
	}
	return out
}
