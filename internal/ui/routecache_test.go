package ui

import (
	"testing"
	"time"

	"github.com/regeda/envoyadmin/internal/snapshot"
)

func refs(names ...string) []snapshot.RouteRef {
	var out []snapshot.RouteRef
	for _, n := range names {
		out = append(out, snapshot.RouteRef{Name: n})
	}
	return out
}

func TestRouteCache(t *testing.T) {
	c := newRouteCache()
	at := time.Unix(100, 0)
	if _, ok := c.Get("l", refs("a")); ok {
		t.Fatal("hit before Put")
	}
	cfgs := []snapshot.RouteConfig{{Name: "a"}}
	c.Put("l", refs("a"), cfgs, nil, at)
	e, ok := c.Get("l", refs("a"))
	if !ok || len(e.cfgs) != 1 || e.cfgs[0].Name != "a" || !e.at.Equal(at) || e.missingErr() != nil {
		t.Fatalf("Get = %+v %v", e, ok)
	}
	c.Put("l", refs("a"), cfgs, []string{"b"}, at)
	if e, _ := c.Get("l", refs("a")); e.missingErr() == nil || e.missingErr().Error() != `route config "b" not found` {
		t.Fatalf("missing = %v", e.missingErr())
	}
	if _, ok := c.Get("l", refs("a", "b")); ok {
		t.Fatal("hit with changed names")
	}
	c.Forget("l")
	if _, ok := c.Get("l", refs("a")); ok {
		t.Fatal("hit after Forget")
	}

	c.Put("keep", refs("a"), cfgs, nil, at)
	c.Put("gone", refs("a"), cfgs, nil, at)
	c.Prune([]snapshot.Listener{{Name: "keep"}})
	if _, ok := c.Get("gone", refs("a")); ok {
		t.Fatal("hit after Prune")
	}
	if _, ok := c.Get("keep", refs("a")); !ok {
		t.Fatal("Prune dropped a live listener")
	}
}

func TestRouteCacheFresh(t *testing.T) {
	c := newRouteCache()
	at := time.Unix(100, 0)
	if !c.Fresh(at, at.Add(routePullWindow)) {
		t.Fatal("not fresh at 10s")
	}
	if c.Fresh(at, at.Add(routePullWindow+time.Nanosecond)) {
		t.Fatal("fresh past 10s")
	}
}
