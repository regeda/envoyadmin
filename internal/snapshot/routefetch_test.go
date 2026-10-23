package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/regeda/envoyadmin/internal/admin"
)

type routeFake struct {
	admin.Client
	err   error
	calls [][]string
	resp  []admin.RouteConfig
}

func (f *routeFake) RouteConfigs(_ context.Context, names []string) ([]admin.RouteConfig, error) {
	f.calls = append(f.calls, names)
	return f.resp, f.err
}

func rdsConfig() admin.RouteConfig {
	const name = "a"
	return admin.RouteConfig{
		Name:        name,
		VersionInfo: "v1",
		JSON:        []byte(`{"name":"` + name + `","virtual_hosts":[{"name":"vh","domains":["*"],"routes":[{"match":{"prefix":"/"}}]}]}`),
	}
}

func listenerWith(refs ...RouteRef) Listener {
	var l Listener
	for i := range refs {
		r := refs[i]
		l.Chains = append(l.Chains, FilterChain{Routes: &r, hcm: true})
	}
	return l
}

func TestFetchRoutes(t *testing.T) {
	ctx := context.Background()

	t.Run("one rds ref", func(t *testing.T) {
		f := &routeFake{resp: []admin.RouteConfig{rdsConfig()}}
		got, err := FetchRoutes(ctx, f, listenerWith(RouteRef{Name: "a"}))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(f.calls, [][]string{{"a"}}) {
			t.Fatalf("calls = %v", f.calls)
		}
		if len(got) != 1 || got[0].Name != "a" || len(got[0].VirtualHosts) != 1 || got[0].Inline {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("inline without a call", func(t *testing.T) {
		f := &routeFake{}
		raw := json.RawMessage(`{"virtual_hosts":[{"name":"vh","domains":["*"]}]}`)
		got, err := FetchRoutes(ctx, f, listenerWith(RouteRef{Inline: raw}))
		if err != nil {
			t.Fatal(err)
		}
		if len(f.calls) != 0 {
			t.Fatalf("calls = %v", f.calls)
		}
		if len(got) != 1 || got[0].Name != "inline" || !got[0].Inline || len(got[0].VirtualHosts) != 1 {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("mixed inline and rds", func(t *testing.T) {
		f := &routeFake{resp: []admin.RouteConfig{rdsConfig()}}
		raw := json.RawMessage(`{"name":"in","virtual_hosts":[]}`)
		got, err := FetchRoutes(ctx, f, listenerWith(RouteRef{Inline: raw}, RouteRef{Name: "a"}))
		if err != nil {
			t.Fatal(err)
		}
		if len(f.calls) != 1 || len(got) != 2 {
			t.Fatalf("calls %v got %+v", f.calls, got)
		}
		if got[0].Name != "in" || got[1].Name != "a" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		f := &routeFake{resp: []admin.RouteConfig{rdsConfig()}}
		got, err := FetchRoutes(ctx, f, listenerWith(RouteRef{Name: "a"}, RouteRef{Name: "x"}))
		var miss *MissingRouteConfigError
		if !errors.As(err, &miss) || !reflect.DeepEqual(miss.Names, []string{"x"}) {
			t.Fatalf("err = %v", err)
		}
		if err.Error() != `route config "x" not found` {
			t.Fatalf("message = %q", err.Error())
		}
		if len(got) != 1 || got[0].Name != "a" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("several missing", func(t *testing.T) {
		f := &routeFake{}
		got, err := FetchRoutes(ctx, f, listenerWith(RouteRef{Name: "x"}, RouteRef{Name: "y"}))
		if err == nil || err.Error() != `route config "x", "y" not found` {
			t.Fatalf("err = %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("no http", func(t *testing.T) {
		f := &routeFake{}
		got, err := FetchRoutes(ctx, f, Listener{Chains: []FilterChain{{}}})
		if !errors.Is(err, ErrNoHTTPRoutes) || got != nil || len(f.calls) != 0 {
			t.Fatalf("got %v err %v calls %v", got, err, f.calls)
		}
	})

	t.Run("admin error", func(t *testing.T) {
		boom := errors.New("boom")
		f := &routeFake{err: boom, resp: []admin.RouteConfig{rdsConfig()}}
		raw := json.RawMessage(`{"virtual_hosts":[]}`)
		got, err := FetchRoutes(ctx, f, listenerWith(RouteRef{Inline: raw}, RouteRef{Name: "a"}))
		if !errors.Is(err, boom) || got != nil {
			t.Fatalf("got %v err %v", got, err)
		}
	})
}
