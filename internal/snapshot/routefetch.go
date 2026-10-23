package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/regeda/envoyadmin/internal/admin"
)

// ErrNoHTTPRoutes is returned for a listener without an HttpConnectionManager.
var ErrNoHTTPRoutes = errors.New("no HTTP routes on this listener")

// MissingRouteConfigError names route configs the listener refers to that the
// proxy did not return.
type MissingRouteConfigError struct{ Names []string }

func (e *MissingRouteConfigError) Error() string {
	q := make([]string, len(e.Names))
	for i, n := range e.Names {
		q[i] = strconv.Quote(n)
	}
	return "route config " + strings.Join(q, ", ") + " not found"
}

// FetchRoutes returns the listener's route configs: inline ones are parsed
// as is, RDS ones come from one RouteConfigs call. When some names are
// missing the found configs are returned together with a
// *MissingRouteConfigError. An admin error returns no configs.
func FetchRoutes(ctx context.Context, c admin.Client, l Listener) ([]RouteConfig, error) {
	if !l.HasHTTP() {
		return nil, ErrNoHTTPRoutes
	}
	refs := l.RouteRefs()
	names := rdsNames(refs)
	var fetched map[string]admin.RouteConfig
	if len(names) > 0 {
		got, err := c.RouteConfigs(ctx, names)
		if err != nil {
			return nil, fmt.Errorf("fetch route configs: %w", err)
		}
		fetched = make(map[string]admin.RouteConfig, len(got))
		for _, g := range got {
			fetched[g.Name] = g
		}
	}
	var out []RouteConfig
	var missing []string
	for _, r := range refs {
		if r.Name == "" {
			out = append(out, ParseRouteConfig(inlineName(r.Inline), "", true, r.Inline))
			continue
		}
		g, ok := fetched[r.Name]
		if !ok {
			missing = append(missing, r.Name)
			continue
		}
		out = append(out, ParseRouteConfig(g.Name, g.VersionInfo, false, g.JSON))
	}
	if len(missing) > 0 {
		return out, &MissingRouteConfigError{Names: missing}
	}
	return out, nil
}

// rdsNames are the names of the refs that are fetched, not inline.
func rdsNames(refs []RouteRef) []string {
	var names []string
	for _, r := range refs {
		if r.Name != "" {
			names = append(names, r.Name)
		}
	}
	return names
}

// inlineName is the "name" of an inline route config, "" when absent.
func inlineName(inline json.RawMessage) string {
	var head struct {
		Name string `json:"name"`
	}
	looseUnmarshal(inline, &head)
	return head.Name
}
