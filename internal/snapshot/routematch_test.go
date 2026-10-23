package snapshot

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
)

// oneVHost builds a route config with one "*" virtual host holding routes.
func oneVHost(t *testing.T, routes ...string) []RouteConfig {
	t.Helper()
	raw := `{"virtual_hosts":[{"name":"vh","domains":["*"],"routes":[` + strings.Join(routes, ",") + `]}]}`
	return []RouteConfig{ParseRouteConfig("rc", "", false, []byte(raw))}
}

func req(method, host, port, rawPath string, headers map[string][]string) Request {
	p, q, _ := strings.Cut(rawPath, "?")
	vals, err := url.ParseQuery(q)
	if err != nil {
		panic(err) // a malformed test fixture
	}
	if headers == nil {
		headers = map[string][]string{}
	}
	return Request{Method: method, Host: host, Port: port, Path: p, Query: vals, Headers: headers}
}

// hitNames renders hits as "name:status" for compact comparison.
func hitNames(hits []RouteHit) string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Route.Name+":"+h.Status())
	}
	return strings.Join(out, ",")
}

// matches reports whether the single route r matches the request.
func matches(t *testing.T, match string, r Request) bool {
	t.Helper()
	hits := MatchRoutes(oneVHost(t, `{"name":"r","match":`+match+`}`), r)
	if len(hits) > 1 {
		t.Fatalf("%s: %d hits", match, len(hits))
	}
	if len(hits) == 1 && hits[0].NotEvaluated {
		t.Fatalf("%s: not evaluated", match)
	}
	return len(hits) == 1
}

func TestMatchRoutesPathKinds(t *testing.T) {
	tests := []struct {
		match, path string
		want        bool
	}{
		{`{"path":"/a"}`, "/a", true},
		{`{"path":"/a"}`, "/a/", false},
		{`{"path":"/a"}`, "/A", false},
		{`{"path":"/a"}`, "/a?x=1", true},
		{`{"path":"/a","case_sensitive":false}`, "/A", true},
		{`{"path":"/a","case_sensitive":true}`, "/A", false},
		{`{"prefix":"/a"}`, "/ab", true},
		{`{"prefix":"/a"}`, "/b", false},
		{`{"prefix":"/a"}`, "/A/b", false},
		{`{"prefix":"/a","case_sensitive":false}`, "/A/b", true},
		{`{"prefix":"/a?x"}`, "/a?x=1", false},
		{`{"prefix":""}`, "/anything", true},
		{`{"path_separated_prefix":"/a"}`, "/a", true},
		{`{"path_separated_prefix":"/a"}`, "/a/b", true},
		{`{"path_separated_prefix":"/a"}`, "/ab", false},
		{`{"path_separated_prefix":"/a"}`, "/a?x=1", true},
		{`{"path_separated_prefix":"/a","case_sensitive":false}`, "/A/b", true},
		{`{"safe_regex":{"regex":"/a/[0-9]+"}}`, "/a/12", true},
		{`{"safe_regex":{"regex":"/a/[0-9]+"}}`, "/a/12/x", false},
		{`{"safe_regex":{"regex":"/a/[0-9]+"}}`, "/x/a/12", false},
		{`{"safe_regex":{"regex":"/a|/b"}}`, "/b", true},
		{`{"safe_regex":{"google_re2":{},"regex":"/a/[0-9]+"}}`, "/a/1?q=2", true},
	}
	for _, tc := range tests {
		if got := matches(t, tc.match, req("GET", "h", "", tc.path, nil)); got != tc.want {
			t.Errorf("%s vs %s = %v, want %v", tc.match, tc.path, got, tc.want)
		}
	}
}

func TestMatchRoutesHeaders(t *testing.T) {
	hdr := func(h string) string { return `{"prefix":"/","headers":[` + h + `]}` }
	xa := map[string][]string{"x-a": {"Hello-World"}}
	tests := []struct {
		name  string
		match string
		r     Request
		want  bool
	}{
		{name: "authority regex", match: hdr(`{"name":":authority","string_match":{"safe_regex":{"regex":"a\\.example\\.com|b\\.example\\.com"}}}`), r: req("GET", "b.example.com", "", "/", nil), want: true},
		{name: "authority regex miss", match: hdr(`{"name":":authority","string_match":{"safe_regex":{"regex":"a\\.example\\.com"}}}`), r: req("GET", "xa.example.com", "", "/", nil), want: false},
		{name: "authority carries port", match: hdr(`{"name":":authority","string_match":{"safe_regex":{"regex":"a\\.example\\.com(:[0-9]+)?"}}}`), r: req("GET", "a.example.com", "8443", "/", nil), want: true},
		{name: "authority port exact", match: hdr(`{"name":":authority","string_match":{"exact":"a.example.com:8443"}}`), r: req("GET", "a.example.com", "8443", "/", nil), want: true},
		{name: "exact", match: hdr(`{"name":"x-a","string_match":{"exact":"Hello-World"}}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "exact case", match: hdr(`{"name":"x-a","string_match":{"exact":"hello-world"}}`), r: req("GET", "h", "", "/", xa), want: false},
		{name: "exact ignore_case", match: hdr(`{"name":"x-a","string_match":{"exact":"hello-world","ignore_case":true}}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "name case-insensitive", match: hdr(`{"name":"X-A","string_match":{"exact":"Hello-World"}}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "prefix", match: hdr(`{"name":"x-a","string_match":{"prefix":"Hello"}}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "prefix miss", match: hdr(`{"name":"x-a","string_match":{"prefix":"World"}}`), r: req("GET", "h", "", "/", xa), want: false},
		{name: "suffix", match: hdr(`{"name":"x-a","string_match":{"suffix":"World"}}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "contains", match: hdr(`{"name":"x-a","string_match":{"contains":"o-W"}}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "contains ignore_case", match: hdr(`{"name":"x-a","string_match":{"contains":"O-w","ignore_case":true}}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "legacy exact", match: hdr(`{"name":"x-a","exact_match":"Hello-World"}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "legacy prefix", match: hdr(`{"name":"x-a","prefix_match":"Hel"}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "legacy suffix", match: hdr(`{"name":"x-a","suffix_match":"xyz"}`), r: req("GET", "h", "", "/", xa), want: false},
		{name: "legacy contains", match: hdr(`{"name":"x-a","contains_match":"lo-Wo"}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "legacy regex", match: hdr(`{"name":"x-a","safe_regex_match":{"regex":"Hello-.*"}}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "regex full match", match: hdr(`{"name":"x-a","safe_regex_match":{"regex":"Hello"}}`), r: req("GET", "h", "", "/", xa), want: false},
		{name: "present", match: hdr(`{"name":"x-a","present_match":true}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "present absent", match: hdr(`{"name":"x-b","present_match":true}`), r: req("GET", "h", "", "/", xa), want: false},
		{name: "present false absent", match: hdr(`{"name":"x-b","present_match":false}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "name only is present", match: hdr(`{"name":"x-a"}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "range in", match: hdr(`{"name":"x-n","range_match":{"start":"10","end":"20"}}`), r: req("GET", "h", "", "/", map[string][]string{"x-n": {"10"}}), want: true},
		{name: "range end exclusive", match: hdr(`{"name":"x-n","range_match":{"start":10,"end":20}}`), r: req("GET", "h", "", "/", map[string][]string{"x-n": {"20"}}), want: false},
		{name: "range not a number", match: hdr(`{"name":"x-n","range_match":{"start":"10","end":"20"}}`), r: req("GET", "h", "", "/", map[string][]string{"x-n": {"abc"}}), want: false},
		{name: "invert", match: hdr(`{"name":"x-a","string_match":{"exact":"nope"},"invert_match":true}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "invert miss", match: hdr(`{"name":"x-a","string_match":{"exact":"Hello-World"},"invert_match":true}`), r: req("GET", "h", "", "/", xa), want: false},
		{name: "invert present", match: hdr(`{"name":"x-a","present_match":true,"invert_match":true}`), r: req("GET", "h", "", "/", xa), want: false},
		{name: "invert absent string", match: hdr(`{"name":"x-b","string_match":{"exact":"v"},"invert_match":true}`), r: req("GET", "h", "", "/", xa), want: false},
		{name: "missing as empty", match: hdr(`{"name":"x-b","string_match":{"exact":"v"},"invert_match":true,"treat_missing_header_as_empty":true}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "multi values joined", match: hdr(`{"name":"x-m","string_match":{"exact":"a,b"}}`), r: req("GET", "h", "", "/", map[string][]string{"x-m": {"a", "b"}}), want: true},
		{name: "method", match: hdr(`{"name":":method","string_match":{"safe_regex":{"regex":"POST|GET"}}}`), r: req("GET", "h", "", "/", nil), want: true},
		{name: "method miss", match: hdr(`{"name":":method","string_match":{"exact":"POST"}}`), r: req("GET", "h", "", "/", nil), want: false},
		{name: "present ignores missing as empty", match: hdr(`{"name":"x-b","present_match":true,"treat_missing_header_as_empty":true}`), r: req("GET", "h", "", "/", xa), want: false},
		{name: "present false ignores missing as empty", match: hdr(`{"name":"x-b","present_match":false,"treat_missing_header_as_empty":true}`), r: req("GET", "h", "", "/", xa), want: true},
		{name: "path pseudo header exact no query", match: hdr(`{"name":":path","string_match":{"exact":"/p"}}`), r: req("GET", "h", "", "/p", nil), want: true},
		{name: "path pseudo header regex no query", match: hdr(`{"name":":path","string_match":{"safe_regex":{"regex":"/q"}}}`), r: req("GET", "h", "", "/p", nil), want: false},
		{name: "path pseudo header plain", match: hdr(`{"name":":path","string_match":{"prefix":"/p"}}`), r: req("GET", "h", "", "/p", nil), want: true},
		{name: "all headers must match", match: hdr(`{"name":"x-a","present_match":true},{"name":"x-b","present_match":true}`), r: req("GET", "h", "", "/", xa), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := matches(t, tc.match, tc.r); got != tc.want {
				t.Errorf("%s = %v, want %v", tc.match, got, tc.want)
			}
		})
	}
}

func TestMatchRoutesHeadersLiveShape(t *testing.T) {
	raw, err := os.ReadFile("../admin/testdata/real-routes-dynamic.json")
	if err != nil {
		t.Fatal(err)
	}
	var dump struct {
		Configs []struct {
			RouteConfig json.RawMessage `json:"route_config"`
		} `json:"configs"`
	}
	if err := json.Unmarshal(raw, &dump); err != nil || len(dump.Configs) == 0 {
		t.Fatalf("fixture: %v", err)
	}
	cfgs := []RouteConfig{ParseRouteConfig("live", "", false, dump.Configs[0].RouteConfig)}
	vh := cfgs[0].VirtualHosts[0]
	r0, r1 := vh.Routes[0], vh.Routes[1]

	at := func(host string) []RouteHit {
		return MatchRoutes(cfgs, req("POST", host, "", "/demo", nil))
	}
	if hits := at("api.example.com"); len(hits) != 1 || hits[0].Route.Index != r0.Index || !hits[0].Winner {
		t.Fatalf("api host: %+v", hits)
	}
	if hits := at("eu.example.com"); len(hits) != 1 || hits[0].Route.Index != r1.Index || !hits[0].Winner {
		t.Fatalf("eu host: %+v", hits)
	}
	if hits := at("other.example.com"); len(hits) != 0 {
		t.Fatalf("other host: %+v", hits)
	}

	// Two routes with one exact path, each :authority regex fits the host.
	both := oneVHost(t,
		`{"name":"one","match":{"path":"/p","headers":[{"name":":authority","string_match":{"safe_regex":{"regex":"a\\.example\\.com|b\\.example\\.com"}}}]}}`,
		`{"name":"two","match":{"path":"/p","headers":[{"name":":authority","string_match":{"safe_regex":{"regex":"b\\.example\\.com|c\\.example\\.com"}}}]}}`)
	if got := hitNames(MatchRoutes(both, req("GET", "b.example.com", "", "/p", nil))); got != "one:winner,two:match" {
		t.Fatalf("b host = %q", got)
	}
	if got := hitNames(MatchRoutes(both, req("GET", "c.example.com", "", "/p", nil))); got != "two:winner" {
		t.Fatalf("c host = %q", got)
	}
	if got := hitNames(MatchRoutes(both, req("GET", "d.example.com", "", "/p", nil))); got != "" {
		t.Fatalf("d host = %q", got)
	}
}

func TestMatchRoutesQueryParameters(t *testing.T) {
	qp := func(q string) string { return `{"prefix":"/","query_parameters":[` + q + `]}` }
	tests := []struct {
		match, path string
		want        bool
	}{
		{qp(`{"name":"a","string_match":{"exact":"1"}}`), "/?a=1", true},
		{qp(`{"name":"a","string_match":{"exact":"1"}}`), "/?a=2", false},
		{qp(`{"name":"a","string_match":{"exact":"1"}}`), "/", false},
		{qp(`{"name":"a","string_match":{"exact":"X","ignore_case":true}}`), "/?a=x", true},
		{qp(`{"name":"a","string_match":{"prefix":"ab"}}`), "/?a=abc", true},
		{qp(`{"name":"a","present_match":true}`), "/?a=", true},
		{qp(`{"name":"a","present_match":true}`), "/?b=1", false},
		{qp(`{"name":"a","present_match":true},{"name":"b","string_match":{"exact":"2"}}`), "/?a=1&b=2", true},
		{qp(`{"name":"a","present_match":true},{"name":"b","string_match":{"exact":"2"}}`), "/?a=1&b=3", false},
	}
	for _, tc := range tests {
		if got := matches(t, tc.match, req("GET", "h", "", tc.path, nil)); got != tc.want {
			t.Errorf("%s vs %s = %v, want %v", tc.match, tc.path, got, tc.want)
		}
	}
}

func TestMatchRoutesVirtualHostChoice(t *testing.T) {
	vh := func(name string, domains ...string) string {
		return fmt.Sprintf(`{"name":%q,"domains":["%s"],"routes":[{"name":%q,"match":{"prefix":"/"}}]}`,
			name, strings.Join(domains, `","`), name)
	}
	cfg := func(vhs ...string) []RouteConfig {
		return []RouteConfig{ParseRouteConfig("rc", "", false, []byte(`{"virtual_hosts":[`+strings.Join(vhs, ",")+`]}`))}
	}
	all := cfg(
		vh("any", "*"),
		vh("pre", "api.*"),
		vh("suf", "*.example.com"),
		vh("sufLong", "*.eu.example.com"),
		vh("exact", "api.example.com"),
		vh("ported", "port.example.com:8443"),
	)
	tests := []struct {
		host, port, want string
	}{
		{"api.example.com", "", "exact"},
		{"API.Example.COM", "", "exact"},
		{"api.example.com", "8443", "exact"},
		{"x.example.com", "", "suf"},
		{"x.eu.example.com", "", "sufLong"},
		{"api.other.org", "", "pre"},
		{"other.org", "", "any"},
		{"example.com", "", "any"},
		{"port.example.com", "8443", "ported"},
		{"port.example.com", "", "suf"},
		{"port.example.com", "9000", "suf"},
	}
	for _, tc := range tests {
		hits := MatchRoutes(all, req("GET", tc.host, tc.port, "/", nil))
		if len(hits) != 1 || hits[0].VirtualHost != tc.want || hits[0].Config != "rc" {
			t.Errorf("%s:%s = %+v, want vhost %s", tc.host, tc.port, hits, tc.want)
			continue
		}
		if len(hits[0].Domains) == 0 {
			t.Errorf("%s: hit has no domains", tc.host)
		}
	}
	// The longest prefix wildcard wins too.
	pre := cfg(vh("short", "api.*"), vh("long", "api.example.*"))
	if hits := MatchRoutes(pre, req("GET", "api.example.org", "", "/", nil)); len(hits) != 1 || hits[0].VirtualHost != "long" {
		t.Errorf("prefix wildcard: %+v", hits)
	}
	// No virtual host fits: no hits.
	none := cfg(vh("only", "a.example.com"))
	if hits := MatchRoutes(none, req("GET", "b.example.com", "", "/", nil)); len(hits) != 0 {
		t.Errorf("no vhost: %+v", hits)
	}
}

func TestMatchRoutesWinner(t *testing.T) {
	cfgs := oneVHost(t,
		`{"name":"miss","match":{"prefix":"/b"}}`,
		`{"name":"first","match":{"prefix":"/a"}}`,
		`{"name":"miss2","match":{"path":"/x"}}`,
		`{"name":"second","match":{"path":"/a/1"}}`,
	)
	hits := MatchRoutes(cfgs, req("GET", "h", "", "/a/1", nil))
	if got := hitNames(hits); got != "first:winner,second:match" {
		t.Fatalf("hits = %q", got)
	}
	if !hits[0].Winner || hits[0].Tentative || hits[1].Winner || hits[1].NotEvaluated {
		t.Fatalf("flags = %+v", hits)
	}
	if hits := MatchRoutes(cfgs, req("GET", "h", "", "/zzz", nil)); len(hits) != 0 {
		t.Fatalf("no match: %+v", hits)
	}
	// One winner per route config.
	two := append(oneVHost(t, `{"name":"x","match":{"prefix":"/"}}`), oneVHost(t, `{"name":"y","match":{"prefix":"/"}}`)...)
	if got := hitNames(MatchRoutes(two, req("GET", "h", "", "/", nil))); got != "x:winner,y:winner" {
		t.Fatalf("two configs = %q", got)
	}
}

func TestMatchRoutesNotEvaluated(t *testing.T) {
	rf := `{"name":"rf","match":{"prefix":"/","runtime_fraction":{"default_value":{"numerator":50}}}}`
	win := `{"name":"win","match":{"prefix":"/a"}}`
	if got := hitNames(MatchRoutes(oneVHost(t, rf, win), req("GET", "h", "", "/a", nil))); got != "rf:not evaluated,win:winner (tentative)" {
		t.Fatalf("before winner = %q", got)
	}
	if got := hitNames(MatchRoutes(oneVHost(t, win, rf), req("GET", "h", "", "/a", nil))); got != "win:winner" {
		t.Fatalf("after winner = %q", got)
	}
	if got := hitNames(MatchRoutes(oneVHost(t, win, rf), req("GET", "h", "", "/z", nil))); got != "rf:not evaluated" {
		t.Fatalf("nothing matched = %q", got)
	}

	unsupported := []string{
		`{"prefix":"/","grpc":{}}`,
		`{"prefix":"/","tls_context":{"presented":true}}`,
		`{"prefix":"/","dynamic_metadata":[{"filter":"f"}]}`,
		`{"prefix":"/","runtime":{"runtime_key":"k"}}`,
		`{"path_match_policy":{"name":"x"}}`,
		`{"connect_matcher":{}}`,
		`{}`,
		`{"prefix":"/","headers":[{"name":"x","string_match":{"custom":{}}}]}`,
		`{"prefix":"/","headers":[{"name":":scheme","string_match":{"exact":"https"}}]}`,
		`{"prefix":"/","query_parameters":[{"name":"a","string_match":{"safe_regex":{"regex":"("}}}]}`,
		// Review Focus 2: invalid safe_regex in path or header.
		`{"safe_regex":{"regex":"/a/(["}}`,
		// Unbalanced patterns that would compile once wrapped in ^(?:...)$.
		`{"safe_regex":{"regex":"x)|(.*"}}`,
		`{"safe_regex":{"regex":"a)|(b"}}`,
		`{"prefix":"/","headers":[{"name":"x","string_match":{"safe_regex":{"regex":"x)|(.*"}}}]}`,
		`{"prefix":"/","headers":[{"name":"x","safe_regex_match":{"regex":"a)|(b"}}]}`,
		`{"prefix":"/","query_parameters":[{"name":"a","string_match":{"safe_regex":{"regex":"x)|(.*"}}}]}`,
		`{"prefix":"/","query_parameters":[{"name":"a","string_match":{"safe_regex":{"regex":"a)|(b"}}}]}`,
		// Envoy keeps Host as :authority; a "host" matcher is ambiguous.
		`{"prefix":"/","headers":[{"name":"host","string_match":{"exact":"h"}}]}`,
		`{"prefix":"/","headers":[{"name":"Host","present_match":true}]}`,
		`{"prefix":"/","headers":[{"name":":authority","string_match":{"safe_regex":{"regex":"a(b"}}}]}`,
		`{"prefix":"/","headers":[{"name":"x","safe_regex_match":{"regex":"*"}}]}`,
	}
	for _, m := range unsupported {
		cfgs := oneVHost(t, `{"name":"u","match":`+m+`}`)
		for i := 0; i < 2; i++ { // second call uses the cached compile result
			hits := MatchRoutes(cfgs, req("GET", "h", "", "/a/1", nil))
			if len(hits) != 1 || !hits[0].NotEvaluated || hits[0].Winner || hits[0].Status() != "not evaluated" {
				t.Errorf("%s: %+v", m, hits)
			}
		}
	}

	// A :path header matcher with a query in the request is not evaluated:
	// the original query text is not known.
	pathHdr := oneVHost(t, `{"name":"ph","match":{"prefix":"/","headers":[{"name":":path","string_match":{"exact":"/p?a=1"}}]}}`)
	if got := hitNames(MatchRoutes(pathHdr, req("GET", "h", "", "/p?a=1", nil))); got != "ph:not evaluated" {
		t.Fatalf(":path with query = %q", got)
	}
	// A definite miss elsewhere still makes it a miss.
	pathMiss := oneVHost(t, `{"name":"pm","match":{"prefix":"/z","headers":[{"name":":path","string_match":{"prefix":"/"}}]}}`)
	if got := hitNames(MatchRoutes(pathMiss, req("GET", "h", "", "/p?a=1", nil))); got != "" {
		t.Fatalf(":path with query, path miss = %q", got)
	}

	// A route with no match object at all is not evaluated either.
	if got := hitNames(MatchRoutes(oneVHost(t, `{"name":"nm","route":{}}`), req("GET", "h", "", "/", nil))); got != "nm:not evaluated" {
		t.Fatalf("no match object = %q", got)
	}
}

// An unsupported matcher does not hide a definite miss by the matchers that
// can be evaluated: a canary on /canary is not a candidate for /other.
func TestMatchRoutesUnsupportedWithDefiniteMiss(t *testing.T) {
	canary := `{"name":"canary","match":{"prefix":"/canary","runtime_fraction":{"default_value":{"numerator":5}}}}`
	all := `{"name":"all","match":{"prefix":"/"}}`
	cfgs := oneVHost(t, canary, all)
	if got := hitNames(MatchRoutes(cfgs, req("GET", "h", "", "/other", nil))); got != "all:winner" {
		t.Fatalf("/other = %q", got)
	}
	if got := hitNames(MatchRoutes(cfgs, req("GET", "h", "", "/canary/x", nil))); got != "canary:not evaluated,all:winner (tentative)" {
		t.Fatalf("/canary/x = %q", got)
	}

	// The same holds for an unsupported header or query matcher, and for an
	// invalid regex in another matcher: a miss elsewhere is a miss.
	misses := []string{
		`{"prefix":"/canary","grpc":{}}`,
		`{"prefix":"/canary","headers":[{"name":":scheme","string_match":{"exact":"https"}}]}`,
		`{"prefix":"/canary","headers":[{"name":"x","safe_regex_match":{"regex":"a(b"}}]}`,
		`{"prefix":"/","grpc":{},"headers":[{"name":"x-env","string_match":{"exact":"canary"}}]}`,
		`{"prefix":"/","runtime_fraction":{},"query_parameters":[{"name":"canary","present_match":true}]}`,
		`{"safe_regex":{"regex":"/canary.*"},"runtime_fraction":{}}`,
	}
	for _, m := range misses {
		got := hitNames(MatchRoutes(oneVHost(t, `{"name":"u","match":`+m+`}`, all), req("GET", "h", "", "/other", nil)))
		if got != "all:winner" {
			t.Errorf("%s: /other = %q", m, got)
		}
	}
}

// anyHost is the hostless request: :curl /path.
func anyHost(method, rawPath string, headers map[string][]string) Request {
	r := req(method, "", "", rawPath, headers)
	r.AnyHost = true
	return r
}

// hitPlaces renders hits as "vhost/name:status".
func hitPlaces(hits []RouteHit) string {
	var out []string
	for _, h := range hits {
		out = append(out, h.VirtualHost+"/"+h.Route.Name+":"+h.Status())
	}
	return strings.Join(out, ",")
}

func TestMatchRoutesAnyHost(t *testing.T) {
	// Two routes share one exact path and differ by an :authority regex.
	both := oneVHost(t,
		`{"name":"one","match":{"path":"/p","headers":[{"name":":authority","string_match":{"safe_regex":{"regex":"a\\.example\\.com"}}}]}}`,
		`{"name":"two","match":{"path":"/p","headers":[{"name":":authority","string_match":{"safe_regex":{"regex":"b\\.example\\.com"}}}]}}`,
		`{"name":"other","match":{"path":"/q"}}`)
	hits := MatchRoutes(both, anyHost("GET", "/p", nil))
	if got := hitNames(hits); got != "one:match,two:match" {
		t.Fatalf("shared path = %q", got)
	}
	for _, h := range hits {
		if h.Winner || h.Tentative || h.NotEvaluated || !h.AnyHost || h.VirtualHost != "vh" || len(h.Domains) == 0 {
			t.Fatalf("flags: %+v", h)
		}
	}
	// The host form is unchanged: the one whose regex fits wins.
	if got := hitNames(MatchRoutes(both, req("GET", "b.example.com", "", "/p", nil))); got != "two:winner" {
		t.Fatalf("host form = %q", got)
	}
	for _, h := range MatchRoutes(both, req("GET", "b.example.com", "", "/p", nil)) {
		if h.AnyHost {
			t.Fatalf("host form hit marked AnyHost: %+v", h)
		}
	}

	// Every virtual host is a candidate, in config order.
	vhosts := []RouteConfig{
		ParseRouteConfig("rc1", "", false, []byte(`{"virtual_hosts":[
			{"name":"a","domains":["a.example.com"],"routes":[{"name":"ra","match":{"prefix":"/"}},{"name":"ra2","match":{"prefix":"/v1"}}]},
			{"name":"b","domains":["b.example.com"],"routes":[{"name":"rb","match":{"prefix":"/v1"}},{"name":"rbmiss","match":{"prefix":"/v2"}}]}]}`)),
		ParseRouteConfig("rc2", "", false, []byte(`{"virtual_hosts":[
			{"name":"c","domains":["*"],"routes":[{"name":"rc","match":{"path":"/v1/logon"}}]}]}`)),
	}
	hits = MatchRoutes(vhosts, anyHost("GET", "/v1/logon", nil))
	if got := hitPlaces(hits); got != "a/ra:match,a/ra2:match,b/rb:match,c/rc:match" {
		t.Fatalf("every vhost = %q", got)
	}
	if hits[2].Config != "rc1" || hits[2].Domains[0] != "b.example.com" || hits[3].Config != "rc2" {
		t.Fatalf("hit context: %+v", hits)
	}
	// The host form still picks one virtual host by domain.
	if got := hitPlaces(MatchRoutes(vhosts, req("GET", "b.example.com", "", "/v1/logon", nil))); got != "b/rb:winner,c/rc:winner" {
		t.Fatalf("host form vhosts = %q", got)
	}

	// An inverted :authority matcher matches too: the host could be anything.
	inv := oneVHost(t,
		`{"name":"inv","match":{"prefix":"/","headers":[{"name":":authority","string_match":{"exact":"a.example.com"},"invert_match":true}]}}`,
		`{"name":"pres","match":{"prefix":"/","headers":[{"name":":authority","present_match":true,"invert_match":true}]}}`)
	if got := hitNames(MatchRoutes(inv, anyHost("GET", "/x", nil))); got != "inv:match,pres:match" {
		t.Fatalf("inverted :authority = %q", got)
	}
	if got := hitNames(MatchRoutes(inv, req("GET", "a.example.com", "", "/x", nil))); got != "" {
		t.Fatalf("inverted :authority, host form = %q", got)
	}

	// A canary is listed as not evaluated, before and after the matches: no
	// winner cuts the list off.
	canary := `{"name":"canary","match":{"prefix":"/v1","runtime_fraction":{"default_value":{"numerator":5}}}}`
	all := `{"name":"all","match":{"prefix":"/"}}`
	canary2 := `{"name":"canary2","match":{"prefix":"/","runtime_fraction":{"default_value":{"numerator":5}}}}`
	cfgs := oneVHost(t, canary, all, canary2)
	hits = MatchRoutes(cfgs, anyHost("GET", "/v1/logon", nil))
	if got := hitNames(hits); got != "canary:not evaluated,all:match,canary2:not evaluated" {
		t.Fatalf("canary = %q", got)
	}
	if hits[0].Winner || hits[1].Winner || hits[1].Tentative {
		t.Fatalf("flags: %+v", hits)
	}
	if got := hitNames(MatchRoutes(cfgs, req("GET", "h", "", "/v1/logon", nil))); got != "canary:not evaluated,all:winner (tentative)" {
		t.Fatalf("canary host form = %q", got)
	}

	// A "host" matcher stays not evaluated; other matchers evaluate as usual.
	cases := []struct {
		name  string
		match string
		want  string
		r     Request
	}{
		{name: "host header", match: `{"prefix":"/","headers":[{"name":"host","string_match":{"exact":"h"}}]}`, r: anyHost("GET", "/", nil), want: "u:not evaluated"},
		{name: "path miss", match: `{"path":"/a","headers":[{"name":":authority","string_match":{"exact":"h"}}]}`, r: anyHost("GET", "/b", nil), want: ""},
		{name: "method", match: `{"prefix":"/","headers":[{"name":":method","string_match":{"exact":"POST"}}]}`, r: anyHost("GET", "/", nil), want: ""},
		{name: "method hit", match: `{"prefix":"/","headers":[{"name":":method","string_match":{"exact":"POST"}}]}`, r: anyHost("POST", "/", nil), want: "u:match"},
		{name: "other header", match: `{"prefix":"/","headers":[{"name":"x-tenant","string_match":{"exact":"acme"}}]}`, r: anyHost("GET", "/", map[string][]string{"x-tenant": {"acme"}}), want: "u:match"},
		{name: "other header miss", match: `{"prefix":"/","headers":[{"name":"x-tenant","string_match":{"exact":"acme"}}]}`, r: anyHost("GET", "/", nil), want: ""},
		{name: "query", match: `{"prefix":"/","query_parameters":[{"name":"a","string_match":{"exact":"1"}}]}`, r: anyHost("GET", "/?a=1", nil), want: "u:match"},
		{name: "query miss", match: `{"prefix":"/","query_parameters":[{"name":"a","string_match":{"exact":"1"}}]}`, r: anyHost("GET", "/?a=2", nil), want: ""},
		{name: ":path no query", match: `{"prefix":"/","headers":[{"name":":path","string_match":{"exact":"/p"}}]}`, r: anyHost("GET", "/p", nil), want: "u:match"},
		{name: ":path with query", match: `{"prefix":"/","headers":[{"name":":path","string_match":{"exact":"/p"}}]}`, r: anyHost("GET", "/p?a=1", nil), want: "u:not evaluated"},
		{name: "bad :authority regex", match: `{"prefix":"/","headers":[{"name":":authority","string_match":{"safe_regex":{"regex":"a(b"}}}]}`, r: anyHost("GET", "/", nil), want: "u:not evaluated"},
	}
	for _, tc := range cases {
		if got := hitNames(MatchRoutes(oneVHost(t, `{"name":"u","match":`+tc.match+`}`), tc.r)); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSearchRoutesByName(t *testing.T) {
	cfgs := []RouteConfig{
		ParseRouteConfig("a", "", false, []byte(`{"virtual_hosts":[{"name":"v1","domains":["*"],"routes":[
			{"name":"acme-logon.logon-stage.logon.0","match":{"prefix":"/"}},
			{"match":{"prefix":"/"}},
			{"name":"other","match":{"prefix":"/"}}]}]}`)),
		ParseRouteConfig("b", "", false, []byte(`{"virtual_hosts":[{"name":"v2","domains":["x.example.com"],"routes":[
			{"name":"logonXb","match":{"prefix":"/"}},
			{"name":"logon.b","match":{"prefix":"/"}}]}]}`)),
	}
	hits := SearchRoutesByName(cfgs, "LOGON")
	if got := hitNames(hits); got != "acme-logon.logon-stage.logon.0:match,logonXb:match,logon.b:match" {
		t.Fatalf("LOGON = %q", got)
	}
	for _, h := range hits {
		if h.Winner || h.Tentative || h.NotEvaluated {
			t.Fatalf("flags: %+v", h)
		}
	}
	if hits[0].Config != "a" || hits[0].VirtualHost != "v1" || hits[2].Config != "b" || hits[2].Domains[0] != "x.example.com" {
		t.Fatalf("hit context: %+v", hits)
	}
	if got := hitNames(SearchRoutesByName(cfgs, "logon.b")); got != "logon.b:match" {
		t.Fatalf("literal dot = %q", got)
	}
	if hits := SearchRoutesByName(cfgs, ""); len(hits) != 0 {
		t.Fatalf("empty text: %+v", hits)
	}
}

func TestRouteHitStatus(t *testing.T) {
	for want, h := range map[string]RouteHit{
		"winner":             {Winner: true},
		"winner (tentative)": {Winner: true, Tentative: true},
		"match":              {},
		"not evaluated":      {NotEvaluated: true},
	} {
		if got := h.Status(); got != want {
			t.Errorf("%+v = %q, want %q", h, got, want)
		}
	}
}

// TestMatchRoutesRegexCached checks that matching a large config twice does
// not recompile regexes on the second pass.
func TestMatchRoutesRegexCached(t *testing.T) {
	var routes []string
	for i := 0; i < 3000; i++ {
		routes = append(routes, fmt.Sprintf(`{"name":"r%d","match":{"path":"/p%d","headers":[{"name":":authority","string_match":{"safe_regex":{"regex":"h%d\\.example\\.com"}}}]}}`, i, i, i))
	}
	cfgs := oneVHost(t, routes...)
	r := req("GET", "h2999.example.com", "", "/p2999", nil)
	if got := hitNames(MatchRoutes(cfgs, r)); got != "r2999:winner" {
		t.Fatalf("first pass = %q", got)
	}
	allocs := testing.AllocsPerRun(5, func() { MatchRoutes(cfgs, r) })
	if allocs > 50 {
		t.Fatalf("second pass allocates %v times; regexes not cached?", allocs)
	}
}

func TestSearchRoutesAll(t *testing.T) {
	cfgs := []RouteConfig{
		ParseRouteConfig("a", "", false, []byte(`{"virtual_hosts":[
			{"name":"v1","domains":["*"],"routes":[{"name":"r1","match":{"prefix":"/"}},{"match":{"prefix":"/x"}}]},
			{"name":"v2","domains":["h.example.com"],"routes":[{"name":"r3","match":{"prefix":"/"}}]}]}`)),
		ParseRouteConfig("b", "", false, []byte(`{"virtual_hosts":[{"name":"v3","domains":["*"],"routes":[
			{"name":"r4","match":{"prefix":"/"}}]}]}`)),
	}
	hits := SearchRoutesAll(cfgs)
	if got := hitNames(hits); got != "r1:match,:match,r3:match,r4:match" {
		t.Fatalf("all = %q", got)
	}
	for _, h := range hits {
		if h.Winner || h.Tentative || h.NotEvaluated || h.AnyHost {
			t.Fatalf("flags: %+v", h)
		}
	}
	if hits[2].Config != "a" || hits[2].VirtualHost != "v2" || hits[2].Domains[0] != "h.example.com" || hits[3].Config != "b" {
		t.Fatalf("hit context: %+v", hits)
	}
	if got := SearchRoutesAll(nil); len(got) != 0 {
		t.Fatalf("nil configs: %+v", got)
	}
}
