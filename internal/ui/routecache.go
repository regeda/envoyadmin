package ui

import (
	"slices"
	"time"

	"github.com/regeda/envoyadmin/internal/snapshot"
)

// routePullWindow is how long a listener's pulled route configs count as
// fresh: a new query inside the window reuses them instead of pulling again.
const routePullWindow = 10 * time.Second

// routeEntry is one pull: the configs found, and the RDS names the proxy did
// not return, reported again whenever the entry is served.
type routeEntry struct {
	names   []string
	cfgs    []snapshot.RouteConfig
	missing []string
	at      time.Time
}

// missingErr is the warning for the entry's missing configs, or nil.
func (e routeEntry) missingErr() error {
	if len(e.missing) == 0 {
		return nil
	}
	return &snapshot.MissingRouteConfigError{Names: e.missing}
}

// routeCache keeps each listener's last pulled route configs.
type routeCache struct{ m map[string]routeEntry }

func newRouteCache() *routeCache { return &routeCache{m: map[string]routeEntry{}} }

func refNames(refs []snapshot.RouteRef) []string {
	var out []string
	for _, r := range refs {
		out = append(out, r.Name)
	}
	return out
}

// Get returns the cached entry only when it was pulled for the same RDS names.
func (c *routeCache) Get(listener string, refs []snapshot.RouteRef) (routeEntry, bool) {
	e, ok := c.m[listener]
	if !ok || !slices.Equal(e.names, refNames(refs)) {
		return routeEntry{}, false
	}
	return e, true
}

// Put caches a pull; missing are the RDS names it did not find.
func (c *routeCache) Put(listener string, refs []snapshot.RouteRef, cfgs []snapshot.RouteConfig, missing []string, at time.Time) {
	c.m[listener] = routeEntry{names: refNames(refs), cfgs: cfgs, missing: missing, at: at}
}

func (c *routeCache) Forget(listener string) { delete(c.m, listener) }

// Prune drops the entries of listeners that are gone.
func (c *routeCache) Prune(live []snapshot.Listener) {
	keep := make(map[string]bool, len(live))
	for _, l := range live {
		keep[l.Name] = true
	}
	for name := range c.m {
		if !keep[name] {
			delete(c.m, name)
		}
	}
}

func (c *routeCache) Fresh(at, now time.Time) bool { return now.Sub(at) <= routePullWindow }
