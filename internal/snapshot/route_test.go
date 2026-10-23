package snapshot

import (
	"strings"
	"testing"
)

const routeCfgJSON = `{"name":"rc","virtual_hosts":[
 {"name":"a","domains":["a.example.com","b.example.com"],"routes":[
  {"name":"r0","match":{"prefix":"/x"},"route":{"cluster":"c"}},
  {"match":{"path":"/y"},"route":{"cluster":"c"}}]},
 {"name":"b","domains":["*"],"routes":[
  {"name":"r2","match":{"prefix":"/"},"direct_response":{"status":200}}]}]}`

func TestParseRouteConfig(t *testing.T) {
	rc := ParseRouteConfig("rc", "v1", false, []byte(routeCfgJSON))
	if rc.Name != "rc" || rc.VersionInfo != "v1" || rc.Inline || len(rc.VirtualHosts) != 2 {
		t.Fatalf("config = %+v", rc)
	}
	a, b := rc.VirtualHosts[0], rc.VirtualHosts[1]
	if a.Name != "a" || strings.Join(a.Domains, ",") != "a.example.com,b.example.com" || len(a.Routes) != 2 {
		t.Fatalf("vhost a = %+v", a)
	}
	if len(b.Routes) != 1 || b.Routes[0].Name != "r2" || b.Routes[0].Index != 0 {
		t.Fatalf("vhost b = %+v", b)
	}
	if r := a.Routes[0]; r.Name != "r0" || r.Index != 0 {
		t.Fatalf("route 0 = %+v", r)
	}
	if r := a.Routes[1]; r.Name != "" || r.Index != 1 {
		t.Fatalf("nameless route = %+v", r)
	}
	y, problem := a.Routes[0].Config.Render()
	if problem != "" || !strings.Contains(y, "match:") || !strings.Contains(y, "prefix: /x") {
		t.Fatalf("Render = %q, %q", y, problem)
	}
}

func TestParseRouteConfigInlineName(t *testing.T) {
	if rc := ParseRouteConfig("", "", true, []byte(`{}`)); rc.Name != "inline" || !rc.Inline {
		t.Fatalf("config = %+v", rc)
	}
	if rc := ParseRouteConfig("n", "", true, []byte(`{}`)); rc.Name != "n" {
		t.Fatalf("config = %+v", rc)
	}
}

func TestParseRouteConfigMalformed(t *testing.T) {
	for _, in := range []string{`{}`, `null`, ``, `[1]`, `{"virtual_hosts":[1,"x"]}`} {
		if rc := ParseRouteConfig("x", "", false, []byte(in)); len(rc.VirtualHosts) != 0 {
			t.Fatalf("%q: %+v", in, rc.VirtualHosts)
		}
	}
	rc := ParseRouteConfig("x", "", false, []byte(`{"virtual_hosts":[{"routes":"x"},{"routes":[1]}]}`))
	if len(rc.VirtualHosts) > 1 {
		t.Fatalf("%+v", rc.VirtualHosts)
	}
}
