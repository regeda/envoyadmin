package snapshot

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/regeda/envoyadmin/internal/admin"
	"github.com/regeda/envoyadmin/internal/demo"
)

// The demo scenario must let every route search status be seen.
func TestDemoRoutesShowEveryStatus(t *testing.T) {
	srv := httptest.NewServer(demo.NewServer(demo.Degraded()))
	defer srv.Close()
	c, _ := admin.NewHTTP(srv.URL, time.Second)
	snap, err := Fetch(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	var l Listener
	for _, x := range snap.Listeners {
		if x.Name == "0.0.0.0_8080" {
			l = x
		}
	}
	cfgs, err := FetchRoutes(context.Background(), c, l)
	if err != nil || len(cfgs) != 1 || cfgs[0].Name != "public-api" {
		t.Fatalf("cfgs=%v err=%v", cfgs, err)
	}
	status := func(line string) map[string]string {
		req, err := ParseCurl(line)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		for _, h := range MatchRoutes(cfgs, req) {
			m[h.Route.Name] = h.Status()
		}
		return m
	}
	// two routes share one exact path and differ by an :authority regex.
	if got := status("-H 'Host: a.example.com' http://x/v1/logon"); got["logon-a"] != "winner" || got["logon-b"] != "" {
		t.Fatalf("a: %v", got)
	}
	if got := status("-H 'Host: b.example.com' http://x/v1/logon"); got["logon-b"] != "winner (tentative)" && got["logon-b"] != "winner" {
		t.Fatalf("b: %v", got)
	}
	if got := status("http://api.example.com/v1/x"); got["canary"] != "not evaluated" {
		t.Fatalf("canary: %v", got)
	}
	if hits := SearchRoutesByName(cfgs, "logon"); len(hits) != 2 {
		t.Fatalf("name search = %d hits", len(hits))
	}
}
