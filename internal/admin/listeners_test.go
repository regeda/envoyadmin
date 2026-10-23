package admin

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Hand-written from Envoy's addResourceToDump behaviour: NOT captured. The
// real captures are exercised in TestListenerConfigsFromRealCapture.
const handDynamic = `{"configs":[
 {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"a",
  "active_state":{"version_info":"7","last_updated":"2026-10-14T10:00:00.123Z",
    "listener":{"@type":"type.googleapis.com/envoy.config.listener.v3.Listener","name":"a",
      "address":{"socket_address":{"address":"0.0.0.0","port_value":8080}},
      "filter_chains":[{"filters":[{"name":"envoy.filters.network.tcp_proxy",
        "typed_config":{"@type":"type.googleapis.com/acme.Unknown","x":1}}]}]}}},
 {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"b",
  "active_state":{"version_info":"6","listener":{"name":"b"}},
  "warming_state":{"version_info":"7","listener":{"name":"b"}}},
 {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"c",
  "draining_state":{"version_info":"5","listener":{"name":"c"}}},
 {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"d",
  "active_state":{"version_info":"3","listener":{"name":"d"}},
  "error_state":{"failed_configuration":{"name":"d","bad":true},"last_update_attempt":"2026-10-14T10:01:00Z","details":"boom\nsecond line"}},
 {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"e",
  "error_state":{"failed_configuration":{"name":"e"},"details":"never worked"}}
]}`

const handStatic = `{"configs":[
 {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.StaticListener",
  "listener":{"@type":"type.googleapis.com/envoy.config.listener.v3.Listener","name":"s",
   "address":{"pipe":{"path":"/tmp/s.sock"}}},"last_updated":"2026-10-14T09:00:00Z"}]}`

func serveListeners(t *testing.T, dyn, stat string) *HTTP {
	t.Helper()
	return newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("resource") {
		case "dynamic_listeners":
			writeBody(t, w, dyn)
		case "static_listeners":
			writeBody(t, w, stat)
		default:
			http.Error(w, "bad resource", http.StatusBadRequest)
		}
	}), time.Second)
}

func byName(cfgs []ListenerConfig) map[string]ListenerConfig {
	m := map[string]ListenerConfig{}
	for _, c := range cfgs {
		m[c.Name] = c
	}
	return m
}

func TestListenerConfigsDecodesEveryState(t *testing.T) {
	c := serveListeners(t, handDynamic, handStatic)
	got, err := c.ListenerConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := byName(got)
	if len(by) != 6 {
		t.Fatalf("got %d listeners: %+v", len(by), got)
	}

	a := by["a"]
	if a.Static || a.Active == nil || a.Active.VersionInfo != "7" ||
		a.Active.LastUpdated.Format(time.RFC3339Nano) != "2026-10-14T10:00:00.123Z" {
		t.Fatalf("a: %+v", a)
	}
	if !strings.Contains(string(a.Active.Listener), "acme.Unknown") {
		t.Fatal("an unknown nested Any type must survive in the raw JSON")
	}

	b := by["b"]
	if b.Active == nil || b.Warming == nil || b.Active.VersionInfo != "6" || b.Warming.VersionInfo != "7" {
		t.Fatalf("b: %+v", b)
	}
	if c := by["c"]; c.Draining == nil || c.Active != nil {
		t.Fatalf("c: %+v", c)
	}
	d := by["d"]
	if d.Error == nil || d.Active == nil || d.Error.Details != "boom\nsecond line" ||
		!strings.Contains(string(d.Error.Listener), `"bad":true`) || d.Error.LastUpdateAttempt.IsZero() {
		t.Fatalf("d: %+v", d)
	}
	if e := by["e"]; e.Error == nil || e.Active != nil || e.Error.Details != "never worked" {
		t.Fatalf("e: %+v", e)
	}

	s := by["s"]
	if !s.Static || s.Active == nil || s.Active.VersionInfo != "" || s.Active.LastUpdated.IsZero() ||
		!strings.Contains(string(s.Active.Listener), "/tmp/s.sock") {
		t.Fatalf("a static listener is named by listener.name and held in Active: %+v", s)
	}
}

func TestListenerConfigsWrappedFallback(t *testing.T) {
	wrapped := `{"configs":[{"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump",
	 "version_info":"x",
	 "static_listeners":[{"listener":{"name":"ws"},"last_updated":"2026-10-14T09:00:00Z"}],
	 "dynamic_listeners":[{"name":"wd","active_state":{"version_info":"4","listener":{"name":"wd"}}}]}]}`
	c := serveListeners(t, wrapped, wrapped)
	got, err := c.ListenerConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := byName(got)
	if len(by) != 2 || !by["ws"].Static || by["wd"].Static || by["wd"].Active.VersionInfo != "4" {
		t.Fatalf("wrapped shape: %+v", got)
	}
}

func TestListenerConfigsSkipBadEntriesAndAcceptEmpty(t *testing.T) {
	junk := `{"configs":[
	  42, null, "str", {}, {"@type":123},
	  {"@type":"type.googleapis.com/envoy.admin.v3.BootstrapConfigDump","bootstrap":{}},
	  {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener"},
	  {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"x"},
	  {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"y","active_state":"nope"},
	  {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.StaticListener","listener":null},
	  {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.StaticListener","listener":{}},
	  {"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"good",
	   "active_state":{"version_info":"1","listener":{"name":"good"}}}
	]}`
	c := serveListeners(t, junk, `{}`)
	got, err := c.ListenerConfigs(context.Background())
	if err != nil {
		t.Fatalf("bad entries must be skipped, not fatal: %v", err)
	}
	if by := byName(got); by["good"].Active == nil || len(by) != 1 {
		t.Fatalf("only the good listener survives: %+v", got)
	}
	c = serveListeners(t, `{}`, `{}`)
	if got, err := c.ListenerConfigs(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("empty dumps are fine: %v %v", got, err)
	}
}

func TestRejectedUpdateElementsAreMergedByName(t *testing.T) {
	// Hand-written copy of the pair Envoy emits (see the real capture test below):
	// the named element has active_state + error_state, the twin is nameless.
	const named = `{"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"d",
	  "active_state":{"version_info":"7","listener":{"name":"d"},"last_updated":"2026-10-14T02:48:19.499Z"},
	  "error_state":{"failed_configuration":{"@type":"type.googleapis.com/envoy.config.listener.v3.Listener","name":"d","bad":1},
	    "last_update_attempt":"2026-10-14T02:48:40.320Z","details":"boom\n"}}`
	const twin = `{"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener",
	  "error_state":{"failed_configuration":{"name":"d","bad":1},"details":"boom\n"}}`

	for name, body := range map[string]string{
		"named first": `{"configs":[` + named + `,` + twin + `]}`,
		"twin first":  `{"configs":[` + twin + `,` + named + `]}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := serveListeners(t, body, `{}`).ListenerConfigs(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("the pair is one listener, got %d: %+v", len(got), got)
			}
			d := got[0]
			if d.Name != "d" || d.Active == nil || d.Active.VersionInfo != "7" || d.Error == nil ||
				d.Error.LastUpdateAttempt.IsZero() || d.Error.Details != "boom\n" {
				t.Fatalf("merged listener (the attempt time must survive either order): %+v", d)
			}
		})
	}
}

func TestNamelessErrorOnlyListenerIsNamedFromTheFailedConfiguration(t *testing.T) {
	body := `{"configs":[{"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener",
	  "error_state":{"failed_configuration":{"name":"never-worked"},"details":"nope"}}]}`
	got, err := serveListeners(t, body, `{}`).ListenerConfigs(context.Background())
	if err != nil || len(got) != 1 || got[0].Name != "never-worked" || got[0].Error == nil || got[0].Active != nil {
		t.Fatalf("%v %+v", err, got)
	}
	// no name anywhere: skipped, never fatal
	anon := `{"configs":[{"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener",
	  "error_state":{"failed_configuration":{},"details":"nope"}}]}`
	if got, err := serveListeners(t, anon, `{}`).ListenerConfigs(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("an error with no name at all is skipped: %v %+v", err, got)
	}
}

func TestRealRejectedUpdateCapture(t *testing.T) {
	dyn, err := os.ReadFile("testdata/real-listeners-error.json")
	if err != nil {
		t.Fatalf("committed capture missing: %v", err)
	}
	got, derr := serveListeners(t, string(dyn), `{}`).ListenerConfigs(context.Background())
	if derr != nil {
		t.Fatal(derr)
	}
	if len(got) != 1 {
		t.Fatalf("Envoy's two elements for one rejected update must merge into one listener, got %d: %+v", len(got), got)
	}
	d := got[0]
	if d.Name != "dynamic_one" || d.Active == nil || d.Active.VersionInfo != "7" || d.Error == nil ||
		d.Error.LastUpdateAttempt.IsZero() || !strings.Contains(d.Error.Details, "cluster_specifier") ||
		!strings.Contains(string(d.Error.Listener), "dynamic_one") {
		t.Fatalf("%+v", d)
	}
}

func TestListenerConfigsErrors(t *testing.T) {
	c := serveListeners(t, `{nope`, `{}`)
	if _, err := c.ListenerConfigs(context.Background()); err == nil || !strings.Contains(err.Error(), "dynamic_listeners") {
		t.Fatalf("invalid JSON must name the resource: %v", err)
	}
	c = newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", http.StatusServiceUnavailable) }), time.Second)
	if _, err := c.ListenerConfigs(context.Background()); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("non-2xx must surface: %v", err)
	}
}

func TestListenerConfigsErrorNamesTheFailedDump(t *testing.T) {
	for _, res := range []string{"dynamic_listeners", "static_listeners"} {
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("resource") == res {
				http.Error(w, "x", http.StatusServiceUnavailable)
				return
			}
			writeBody(t, w, `{}`)
		}), time.Second)
		_, err := c.ListenerConfigs(context.Background())
		if err == nil || !strings.HasPrefix(err.Error(), res+": ") || !strings.Contains(err.Error(), "GET /config_dump:") || !strings.Contains(err.Error(), "503") {
			t.Fatalf("%s: want %q-prefixed GET /config_dump 503 error, got %v", res, res+": ", err)
		}
	}
}

func TestListenerConfigsFromRealCapture(t *testing.T) {
	dyn, err1 := os.ReadFile("testdata/real-listeners-dynamic.json")
	stat, err2 := os.ReadFile("testdata/real-listeners-static.json")
	if err1 != nil || err2 != nil {
		t.Fatalf("committed captures missing: %v %v", err1, err2)
	}
	c := serveListeners(t, string(dyn), string(stat))
	got, err := c.ListenerConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := byName(got)
	if s := by["static_tcp"]; !s.Static || s.Active == nil || !strings.Contains(string(s.Active.Listener), "127.0.0.1") {
		t.Fatalf("static_tcp: %+v", s)
	}
	if m := by["static_multi"]; !m.Static || !strings.Contains(string(m.Active.Listener), "by-sni") {
		t.Fatalf("static_multi: %+v", m)
	}
	d := by["dynamic_one"]
	if d.Static || d.Active == nil || d.Active.VersionInfo == "" || d.Active.LastUpdated.IsZero() {
		t.Fatalf("dynamic_one: %+v", d)
	}
}

func TestListenerStateWithoutListenerDoesNotPanic(t *testing.T) {
	body := `{"configs":[{"@type":"type.googleapis.com/envoy.admin.v3.ListenersConfigDump.DynamicListener","name":"n",
	  "active_state":{"version_info":"1"},"warming_state":{}}]}`
	got, err := serveListeners(t, body, `{}`).ListenerConfigs(context.Background())
	if err != nil || len(got) != 1 || got[0].Active == nil || got[0].Active.Listener != nil || got[0].Warming == nil {
		t.Fatalf("a state without a listener is kept, with a nil raw listener: %v %+v", err, got)
	}
}
