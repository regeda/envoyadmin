package ui

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	adminv3 "github.com/envoyproxy/go-control-plane/envoy/admin/v3"

	"github.com/regeda/envoyadmin/internal/admin"
	"github.com/regeda/envoyadmin/internal/snapshot"
)

// listenerDump is an admin client serving only listeners, so tests get
// snapshot.Listeners parsed the real way (with their HCM and route refs).
type listenerDump struct {
	admin.Client
	ls []admin.ListenerConfig
}

func (d listenerDump) Clusters(context.Context) (*adminv3.Clusters, error) {
	return &adminv3.Clusters{}, nil
}
func (d listenerDump) ClusterConfigs(context.Context) ([]admin.ClusterConfig, error) { return nil, nil }
func (d listenerDump) ListenerConfigs(context.Context) ([]admin.ListenerConfig, error) {
	return d.ls, nil
}
func (d listenerDump) SecretConfigs(context.Context) ([]admin.SecretConfig, error) { return nil, nil }
func (d listenerDump) ServerInfo(context.Context) (*adminv3.ServerInfo, error)     { return nil, nil }

const hcmType = "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager"

func listenerJSON(name, filter string) admin.ListenerConfig {
	body := `{"name":"` + name + `","address":{"socket_address":{"address":"0.0.0.0","port_value":8080}},` +
		`"filter_chains":[{"filters":[` + filter + `]}]}`
	return admin.ListenerConfig{Name: name, Active: &admin.ListenerState{VersionInfo: "1", Listener: []byte(body)}}
}

// rdsListener has one HCM taking its routes from RDS config rds.
func rdsListener(name, rds string) admin.ListenerConfig {
	return listenerJSON(name, `{"name":"envoy.filters.network.http_connection_manager","typed_config":{"@type":"`+
		hcmType+`","rds":{"route_config_name":"`+rds+`"}}}`)
}

// inlineListener has one HCM with an inline route_config.
func inlineListener(name, routeConfig string) admin.ListenerConfig {
	return listenerJSON(name, `{"name":"envoy.filters.network.http_connection_manager","typed_config":{"@type":"`+
		hcmType+`","route_config":`+routeConfig+`}}`)
}

func tcpListener(name string) admin.ListenerConfig {
	return listenerJSON(name, `{"name":"envoy.filters.network.tcp_proxy","typed_config":{"@type":"type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy"}}`)
}

func listenerSnap(t *testing.T, ls ...admin.ListenerConfig) snapshot.Snapshot {
	t.Helper()
	s, err := snapshot.FetchWithClock(context.Background(), listenerDump{ls: ls}, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const edgeRoutes = `{"name":"edge-routes","virtual_hosts":[{"name":"edge","domains":["*"],"routes":[` +
	`{"name":"logon","match":{"prefix":"/logon"},"route":{"cluster":"auth"}},` +
	`{"name":"logon-any","match":{"prefix":"/lo"},"route":{"cluster":"auth-legacy"}},` +
	`{"name":"catch-all","match":{"prefix":"/"},"route":{"cluster":"web"}}]}]}`

func edgeConfigs() []snapshot.RouteConfig {
	return []snapshot.RouteConfig{snapshot.ParseRouteConfig("edge-routes", "v1", false, []byte(edgeRoutes))}
}

// fakeRoutes is a RouteFetcher that counts its calls.
type fakeRoutes struct {
	calls int
	got   []string // listener names asked for
	cfgs  []snapshot.RouteConfig
	err   error
}

func (f *fakeRoutes) fetch(_ context.Context, l snapshot.Listener) ([]snapshot.RouteConfig, error) {
	f.calls++
	f.got = append(f.got, l.Name)
	return f.cfgs, f.err
}

// clock is a settable Options.Clock.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// searchModel is a model on the Listeners tab of s, with f as the route
// fetcher and c as the clock. Its snapshot fetcher returns s, so ticks can run.
func searchModel(t *testing.T, s snapshot.Snapshot, f *fakeRoutes, c *clock) Model {
	t.Helper()
	o := testOpts(func(context.Context) (snapshot.Snapshot, error) { return s, nil })
	o.Clock = c.Now
	if f != nil {
		o.Routes = f.fetch
	}
	m := sized(New(o), 120, 30)
	m = send(m, snapshotMsg{snap: s})
	return send(m, key("tab"))
}

// drive feeds every message the cmd produces back into the model.
func drive(m Model, cmd tea.Cmd) Model {
	for _, msg := range run(cmd) {
		var next tea.Cmd
		m, next = sendCmd(m, msg)
		if _, ok := msg.(routesMsg); ok && next != nil {
			m = drive(m, next)
		}
	}
	return m
}

// query replaces the search input with text, presses Enter and delivers the
// pull it starts, if any.
func query(m Model, text string) Model {
	m.search.input.SetValue(text)
	m, cmd := sendCmd(m, key("enter"))
	return drive(m, cmd)
}

func hitNames(m Model) []string {
	var out []string
	for _, v := range m.search.list.visible {
		out = append(out, m.search.list.all[v.idx].Title())
	}
	return out
}

func TestParseSearch(t *testing.T) {
	for _, tc := range []struct {
		in, cmd, arg string
		ok           bool
	}{
		{":name foo", "name", "foo", true},
		{":curl -H 'a: b' http://h/p", "curl", "-H 'a: b' http://h/p", true},
		{"  :name   foo bar  ", "name", "foo bar", true},
		{":name", "", "", false},
		{":name  ", "", "", false},
		{":curl", "", "", false},
		{"foo", "", "", false},
		{":other x", "", "", false},
		{":namefoo", "", "", false},
		{":all", "all", "", true},
		{":all   ", "all", "", true},
		{"  :all  ", "all", "", true},
		{":all foo", "", "", false},
		{":allfoo", "", "", false},
	} {
		cmd, arg, ok := parseSearch(tc.in)
		if ok != tc.ok || (ok && (cmd != tc.cmd || arg != tc.arg)) {
			t.Errorf("parseSearch(%q) = %q, %q, %v", tc.in, cmd, arg, ok)
		}
	}
}

func TestSearchOpensOnColon(t *testing.T) {
	c := &clock{now: fixedNow}
	s := listenerSnap(t, rdsListener("a-edge", "edge-routes"), rdsListener("b-edge", "edge-routes"))
	m := searchModel(t, s, &fakeRoutes{cfgs: edgeConfigs()}, c)
	m = send(m, key("j")) // b-edge
	m = send(m, key(":"))
	if !m.search.open || m.search.listener.Name != "b-edge" {
		t.Fatalf("':' did not open the search on b-edge (open=%v listener=%q)", m.search.open, m.search.listener.Name)
	}
	v := m.View()
	if !strings.Contains(v, "b-edge :") {
		t.Fatalf("no input prompt:\n%s", v)
	}
	if !strings.Contains(v, "[Listeners") {
		t.Fatalf("the Listeners tab is not active while searching:\n%s", v)
	}
	m = query(m, "name logon")
	m = send(m, key("down"))
	m = send(m, key("esc"))
	if m.search.open {
		t.Fatal("esc did not close the search")
	}
	if got := activeName(m); got != "b-edge" {
		t.Fatalf("selection after closing = %q, want b-edge", got)
	}
	if v := m.View(); !strings.Contains(v, "/ to filter") || strings.Contains(v, "b-edge :") {
		t.Fatalf("the listener list did not come back:\n%s", v)
	}

	// Not on the other tabs.
	cl := send(send(m, key("shift+tab")), key(":"))
	if cl.search.open {
		t.Fatal("':' opened the search on the Clusters tab")
	}
	sec := send(send(m, key("tab")), key(":"))
	if sec.search.open {
		t.Fatal("':' opened the search on the Secrets tab")
	}

	// Not while the filter is being typed: ':' is filter text then.
	f := send(send(m, key("/")), key(":"))
	if f.search.open || f.cur().list.query != ":" {
		t.Fatalf("':' in the filter: open=%v query=%q", f.search.open, f.cur().list.query)
	}

	// Not from the detail pane.
	d := send(send(m, key("enter")), key(":"))
	if d.search.open {
		t.Fatal("':' opened the search from the detail pane")
	}
}

func TestSearchUnavailableWithoutFetcher(t *testing.T) {
	c := &clock{now: fixedNow}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), nil, c)
	m = send(m, key(":"))
	if !strings.Contains(m.View(), "route search unavailable") {
		t.Fatalf("no notice without a fetcher:\n%s", m.View())
	}
	m, cmd := sendCmd(func() Model { m.search.input.SetValue("name logon"); return m }(), key("enter"))
	if cmd != nil {
		t.Fatal("Enter without a fetcher returned a cmd")
	}
	if !strings.Contains(m.View(), "route search unavailable") {
		t.Fatalf("no notice after Enter:\n%s", m.View())
	}
}

func TestSearchByNameShowsHits(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs()}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), f, c)
	m = query(send(m, key(":")), "name logon")
	if f.calls != 1 || f.got[0] != "a-edge" {
		t.Fatalf("calls=%d got=%v", f.calls, f.got)
	}
	if got := strings.Join(hitNames(m), ","); got != "logon,logon-any" {
		t.Fatalf("hits = %s", got)
	}
	v := m.View()
	for _, want := range []string{"logon-any", "edge-routes / edge", "Config", "prefix: /logon", "cluster: auth"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view lacks %q:\n%s", want, v)
		}
	}
	m = send(m, key("down"))
	if v := m.View(); !strings.Contains(v, "cluster: auth-legacy") {
		t.Fatalf("the detail does not follow the selection:\n%s", v)
	}
	// tab switches the detail to the overview and back
	m = send(m, key("tab"))
	if v := m.View(); !strings.Contains(v, "Virtual host") || !m.search.open {
		t.Fatalf("tab did not show the overview:\n%s", v)
	}
}

func TestSearchByCurlMarksWinner(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs()}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), f, c)
	m = query(send(m, key(":")), "curl http://h/logon/x")
	if got := strings.Join(hitNames(m), ","); got != "logon,logon-any,catch-all" {
		t.Fatalf("hits = %s", got)
	}
	if !m.search.list.all[0].(routeHitItem).h.Winner {
		t.Fatal("the first route is not the winner")
	}
}

var hostAnyRE = regexp.MustCompile(`Host\s+any`)

// A hostless :curl matches every route that fits, marks no winner and says so.
func TestSearchByCurlAnyHost(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs()}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), f, c)
	m = send(m, key(":"))
	if v := m.View(); !strings.Contains(v, "<url or /path>") {
		t.Fatalf("the empty input shows no hostless hint:\n%s", v)
	}
	m = query(m, "curl -H 'X-Tenant: acme' /logon/x")
	if f.calls != 1 {
		t.Fatalf("calls=%d, want 1", f.calls)
	}
	if got := strings.Join(hitNames(m), ","); got != "logon,logon-any,catch-all" {
		t.Fatalf("hits = %s", got)
	}
	for _, it := range m.search.list.all {
		if h := it.(routeHitItem).h; h.Winner || !h.AnyHost {
			t.Fatalf("hit %+v", h)
		}
	}
	const status = "3 routes match (any host, no winner)"
	v := m.View()
	if m.search.status != status || m.search.statusErr || !strings.Contains(v, status) {
		t.Fatalf("status = %q\n%s", m.search.status, v)
	}
	if strings.Contains(v, "★") {
		t.Fatalf("a winner glyph with any host:\n%s", v)
	}
	ov := send(m, key("tab")).View()
	if !hostAnyRE.MatchString(ov) {
		t.Fatalf("overview lacks Host any:\n%s", ov)
	}

	m = query(m, "curl /lox")
	if m.search.status != "2 routes match (any host, no winner)" {
		t.Fatalf("status = %q", m.search.status)
	}
	m = query(m, "curl /x")
	if m.search.status != "1 route matches (any host, no winner)" {
		t.Fatalf("status = %q", m.search.status)
	}

	// Back to the host form within the pull window: from the cache, a winner.
	m = query(m, "curl http://h/logon/x")
	if f.calls != 1 || m.search.status != "3 routes" || !m.search.list.all[0].(routeHitItem).h.Winner {
		t.Fatalf("host form: calls=%d status=%q", f.calls, m.search.status)
	}
	if v := send(m, key("tab")).View(); hostAnyRE.MatchString(v) || !strings.Contains(v, "Virtual host") {
		t.Fatalf("host form overview says any:\n%s", v)
	}
}

// The hostless status never counts a not-evaluated route as a match, and a
// missing-config warning keeps the any-host suffix.
func TestSearchAnyHostStatusCounts(t *testing.T) {
	const canaryRoutes = `{"name":"canary-routes","virtual_hosts":[{"name":"edge","domains":["*"],"routes":[` +
		`{"name":"canary","match":{"prefix":"/v1","runtime_fraction":{"default_value":{"numerator":5}}},"route":{"cluster":"c"}},` +
		`{"name":"v1","match":{"prefix":"/v1"},"route":{"cluster":"a"}},` +
		`{"name":"logon","match":{"path":"/v1/logon"},"route":{"cluster":"b"}},` +
		`{"name":"canary2","match":{"prefix":"/v2","runtime_fraction":{"default_value":{"numerator":5}}},"route":{"cluster":"c"}}]}]}`
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: []snapshot.RouteConfig{snapshot.ParseRouteConfig("canary-routes", "v1", false, []byte(canaryRoutes))}}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "canary-routes")), f, c)
	m = send(m, key(":"))
	for _, tc := range []struct{ q, want string }{
		{"curl /v1/logon", "2 routes match, 1 not evaluated (any host, no winner)"},
		{"curl /v1/x", "1 route matches, 1 not evaluated (any host, no winner)"},
		{"curl /v2", "0 routes match, 1 not evaluated (any host, no winner)"},
		{"curl /zzz", "0 routes match (any host, no winner)"},
		{"curl http://h/v2", "1 route"}, // host form: unchanged wording
	} {
		m = query(m, tc.q)
		if m.search.status != tc.want || m.search.statusErr {
			t.Errorf("%s: status = %q, want %q", tc.q, m.search.status, tc.want)
		}
	}
	if f.calls != 1 {
		t.Fatalf("calls=%d", f.calls)
	}

	// A missing route config keeps the warning and the any-host suffix.
	c.Advance(11 * time.Second)
	f.err = &snapshot.MissingRouteConfigError{Names: []string{"x"}}
	m = query(m, "curl /v1/logon")
	if want := `route config "x" not found (any host, no winner)`; m.search.status != want || !m.search.statusErr {
		t.Fatalf("missing config, hostless: status = %q", m.search.status)
	}
	m = query(m, "curl http://h/v1/logon")
	if want := `route config "x" not found`; m.search.status != want {
		t.Fatalf("missing config, host form: status = %q", m.search.status)
	}
}

func TestSearchPullRules(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs()}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes"), tcpListener("z-tcp")), f, c)

	// Nothing is pulled before the search runs, by keys, ticks or snapshots.
	m = send(m, key("j"))
	m = send(m, key("k"))
	m = drive(sendCmd(m, tickMsg{}))
	if f.calls != 0 {
		t.Fatalf("pulled with the search closed: calls=%d", f.calls)
	}

	m = send(m, key(":"))
	m = query(m, "name logon")
	if f.calls != 1 {
		t.Fatalf("first query: calls=%d", f.calls)
	}

	c.Advance(5 * time.Second)
	m = query(m, "name any")
	if f.calls != 1 {
		t.Fatalf("a changed query within 10s pulled: calls=%d", f.calls)
	}
	if got := strings.Join(hitNames(m), ","); got != "logon-any" {
		t.Fatalf("re-matched hits = %s", got)
	}

	c.Advance(6 * time.Second) // 11s after the pull
	m = query(m, "name catch")
	if f.calls != 2 {
		t.Fatalf("a changed query after 10s did not pull: calls=%d", f.calls)
	}

	c.Advance(30 * time.Second)
	m = query(m, "name catch")
	if f.calls != 2 {
		t.Fatalf("Enter on an unchanged query pulled: calls=%d", f.calls)
	}

	// Ticks and snapshot refreshes never pull, even with a stale cache.
	m = drive(sendCmd(m, tickMsg{}))
	m = drive(sendCmd(m, snapshotMsg{snap: m.snap}))
	if f.calls != 2 {
		t.Fatalf("a tick or a snapshot pulled: calls=%d", f.calls)
	}

	// No second pull while one is in flight.
	m.search.input.SetValue("name logon")
	m, inflight := sendCmd(m, key("enter"))
	if inflight == nil {
		t.Fatal("a changed stale query issued no pull")
	}
	m.search.input.SetValue("name any")
	m, cmd := sendCmd(m, key("enter"))
	if cmd != nil {
		t.Fatal("a second pull was issued while one was in flight")
	}
	m = drive(m, inflight)
	if f.calls != 3 {
		t.Fatalf("calls=%d", f.calls)
	}

	// An RDS-name change forgets the cache: the next query (inside the
	// window) pulls again.
	c.Advance(time.Second)
	m = send(m, snapshotMsg{snap: listenerSnap(t, rdsListener("a-edge", "edge-routes-v2"), tcpListener("z-tcp"))})
	m = query(m, "name catch")
	if f.calls != 4 {
		t.Fatalf("an RDS-name change did not forget the cache: calls=%d", f.calls)
	}

	// The listener disappearing forgets the cache too.
	c.Advance(time.Second)
	m = send(m, snapshotMsg{snap: listenerSnap(t, tcpListener("z-tcp"))})
	m = send(m, snapshotMsg{snap: listenerSnap(t, rdsListener("a-edge", "edge-routes-v2"), tcpListener("z-tcp"))})
	m = query(m, "name logon")
	if f.calls != 5 {
		t.Fatalf("a gone listener kept its cache: calls=%d", f.calls)
	}

	// Closed again: nothing pulls.
	m = send(m, key("esc"))
	c.Advance(time.Minute)
	m = drive(sendCmd(m, tickMsg{}))
	m = drive(sendCmd(m, snapshotMsg{snap: m.snap}))
	_ = send(m, key("enter"))
	if f.calls != 5 {
		t.Fatalf("pulled with the search closed: calls=%d", f.calls)
	}
}

func TestSearchInlineChangeForgetsCache(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{}
	inline := func(route string) string {
		return `{"virtual_hosts":[{"name":"vh","domains":["*"],"routes":[{"name":"` + route + `","match":{"prefix":"/"}}]}]}`
	}
	f.cfgs = []snapshot.RouteConfig{snapshot.ParseRouteConfig("inline", "", true, []byte(inline("old")))}
	m := searchModel(t, listenerSnap(t, inlineListener("a-inline", inline("old"))), f, c)
	m = query(send(m, key(":")), "name old")
	if f.calls != 1 {
		t.Fatalf("calls=%d", f.calls)
	}
	// Same listener and same (empty) RDS names, different inline bytes.
	m = send(m, snapshotMsg{snap: listenerSnap(t, inlineListener("a-inline", inline("new")))})
	f.cfgs = []snapshot.RouteConfig{snapshot.ParseRouteConfig("inline", "", true, []byte(inline("new")))}
	c.Advance(time.Second)
	m = query(m, "name new")
	if f.calls != 2 {
		t.Fatalf("an inline change did not forget the cache: calls=%d", f.calls)
	}
	if got := strings.Join(hitNames(m), ","); got != "new" {
		t.Fatalf("hits = %s", got)
	}

	// An unchanged inline config keeps the cache.
	m = send(m, snapshotMsg{snap: listenerSnap(t, inlineListener("a-inline", inline("new")))})
	c.Advance(time.Second)
	_ = query(m, "name ne")
	if f.calls != 2 {
		t.Fatalf("an unchanged snapshot forgot the cache: calls=%d", f.calls)
	}
}

func TestSearchErrors(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs()}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes"), tcpListener("z-tcp")), f, c)
	m = send(m, key(":"))

	// No argument: the hint, nothing runs.
	for _, in := range []string{"name", "curl  ", "foo", "all foo"} {
		m.search.input.SetValue(in)
		var cmd tea.Cmd
		m, cmd = sendCmd(m, key("enter"))
		if cmd != nil || f.calls != 0 {
			t.Fatalf("%q ran (calls=%d)", in, f.calls)
		}
		if !strings.Contains(m.View(), searchHint) {
			t.Fatalf("%q shows no hint:\n%s", in, m.View())
		}
	}

	// A bad curl line shows the parse error and pulls nothing.
	_, perr := snapshot.ParseCurl("'http://h/")
	m = query(m, "curl 'http://h/")
	if f.calls != 0 || perr == nil || !strings.Contains(m.View(), perr.Error()) {
		t.Fatalf("bad curl: calls=%d err=%v\n%s", f.calls, perr, m.View())
	}

	// An unsupported option is a search status error and pulls nothing.
	m = query(m, "curl -d x http://h/logon/x")
	const unsup = `curl: unsupported option "-d": :curl takes only -H, -X and a URL`
	if f.calls != 0 || m.search.status != unsup || !m.search.statusErr || !strings.Contains(m.View(), "unsupported option") {
		t.Fatalf("unsupported option: calls=%d status=%q\n%s", f.calls, m.search.status, m.View())
	}

	m = query(m, "name logon")
	if len(hitNames(m)) != 2 {
		t.Fatalf("hits = %v", hitNames(m))
	}

	// A failed pull shows the error and keeps the results.
	c.Advance(11 * time.Second)
	f.err = errors.New("fetch route configs: GET /config_dump: 503 Service Unavailable")
	m = query(m, "name catch")
	if m.search.status != f.err.Error() || !m.search.statusErr || !strings.Contains(m.View(), "503 Service") {
		t.Fatalf("no fetch error:\n%s", m.View())
	}
	if got := strings.Join(hitNames(m), ","); got != "logon,logon-any" {
		t.Fatalf("a failed pull dropped the results: %s", got)
	}

	// A missing config is shown and the found configs still match.
	f.err = &snapshot.MissingRouteConfigError{Names: []string{"x"}}
	m = query(m, "name catch")
	if !strings.Contains(m.View(), `route config "x" not found`) {
		t.Fatalf("no missing-config error:\n%s", m.View())
	}
	if got := strings.Join(hitNames(m), ","); got != "catch-all" {
		t.Fatalf("hits with a missing config = %s", got)
	}
	calls := f.calls
	f.err = nil

	// A stale routesMsg is ignored: another listener, or another query.
	for _, msg := range []routesMsg{
		{listener: "z-tcp", query: ":name catch", cfgs: nil, at: c.now},
		{listener: "a-edge", query: ":name logon", cfgs: nil, at: c.now},
	} {
		m = send(m, msg)
		if got := strings.Join(hitNames(m), ","); got != "catch-all" {
			t.Fatalf("a stale %+v changed the results: %s", msg, got)
		}
	}

	// A listener without an HCM: the notice, no pull.
	m = send(m, key("esc"))
	m = send(send(m, key("j")), key(":"))
	if m.search.listener.Name != "z-tcp" {
		t.Fatalf("searching %q", m.search.listener.Name)
	}
	m = query(m, "name logon")
	if f.calls != calls || !strings.Contains(m.View(), "no HTTP routes on this listener") {
		t.Fatalf("no-HCM listener: calls=%d\n%s", f.calls, m.View())
	}
}

func TestSearchKeysInHelpAndFooter(t *testing.T) {
	c := &clock{now: fixedNow}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), &fakeRoutes{}, c)
	last := func(m Model) string {
		lines := strings.Split(m.View(), "\n")
		return lines[len(lines)-1]
	}
	if f := last(m); !strings.Contains(f, ": routes") || !strings.Contains(f, "r refresh") {
		t.Fatalf("listeners footer: %q", f)
	}
	if h := last(send(m, key("?"))); !strings.Contains(h, ": routes") || !strings.Contains(h, "q quit") {
		t.Fatalf("listeners help: %q", h)
	}
	if f := last(send(m, key("shift+tab"))); strings.Contains(f, ": routes") {
		t.Fatalf("clusters footer offers the route search: %q", f)
	}
	s := send(m, key(":"))
	if f := last(s); !strings.Contains(f, "enter run") || !strings.Contains(f, "esc close") || !strings.Contains(f, "↑↓ move") {
		t.Fatalf("search footer: %q", f)
	}
	// '?' is input while searching; help opened before shows the search keys.
	if s2 := send(s, key("?")); s2.help || s2.search.input.Value() != "?" {
		t.Fatalf("'?' while searching: help=%v input=%q", s2.help, s2.search.input.Value())
	}
	if f := last(send(send(m, key("?")), key(":"))); !strings.Contains(f, searchHelpLine[:20]) {
		t.Fatalf("search help: %q", f)
	}
}

// Case A: a pull starts, the search closes and reopens on the same listener,
// and Enter on the same query is refused while the pull runs. The pull still
// fills the cache and shows that query; the next Enter does not pull again.
func TestSearchInflightSurvivesReopenSameListener(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs()}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), f, c)
	m = send(m, key(":"))
	m.search.input.SetValue("name logon")
	m, inflight := sendCmd(m, key("enter"))
	m = send(send(m, key("esc")), key(":"))
	m.search.input.SetValue("name logon")
	m, cmd := sendCmd(m, key("enter"))
	if cmd != nil || m.search.status != searchPulling {
		t.Fatalf("Enter during the pull: cmd=%v status=%q", cmd != nil, m.search.status)
	}
	m = drive(m, inflight)
	if got := strings.Join(hitNames(m), ","); got != "logon,logon-any" || m.search.status == searchPulling {
		t.Fatalf("after the pull: hits=%s status=%q", got, m.search.status)
	}
	c.Advance(time.Minute)
	m = query(m, "name logon")
	if f.calls != 1 {
		t.Fatalf("the pull was not cached: calls=%d", f.calls)
	}

	// Reopened with nothing entered: the status is cleared, not left pulling.
	m.search.input.SetValue("name catch")
	m, inflight = sendCmd(m, key("enter"))
	m = send(send(m, key("esc")), key(":"))
	m = drive(m, inflight)
	if m.search.status == searchPulling || f.calls != 2 {
		t.Fatalf("status=%q calls=%d", m.search.status, f.calls)
	}
	c.Advance(time.Second)
	_ = query(m, "name catch")
	if f.calls != 2 {
		t.Fatalf("the pull for a closed search was not cached: calls=%d", f.calls)
	}
}

// Case B: reopened on another listener while a pull runs. The pull fills the
// first listener's cache, and the other listener's status is not left pulling.
func TestSearchInflightSurvivesReopenOtherListener(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs()}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes"), rdsListener("b-edge", "edge-routes")), f, c)
	m = send(m, key(":"))
	m.search.input.SetValue("name logon")
	m, inflight := sendCmd(m, key("enter"))
	m = send(send(send(m, key("esc")), key("j")), key(":"))
	m.search.input.SetValue("name logon")
	m, cmd := sendCmd(m, key("enter"))
	if cmd != nil || m.search.listener.Name != "b-edge" {
		t.Fatalf("a second pull while one runs (cmd=%v) on %q", cmd != nil, m.search.listener.Name)
	}
	m = drive(m, inflight)
	if m.search.status == searchPulling || len(hitNames(m)) != 0 {
		t.Fatalf("b-edge after a-edge's pull: status=%q hits=%v", m.search.status, hitNames(m))
	}
	m = query(m, "name logon")
	if f.calls != 2 || f.got[1] != "b-edge" {
		t.Fatalf("b-edge did not pull: calls=%d got=%v", f.calls, f.got)
	}
	m = send(send(send(m, key("esc")), key("k")), key(":"))
	_ = query(m, "name logon")
	if f.calls != 2 {
		t.Fatalf("a-edge's pull was not cached: calls=%d", f.calls)
	}
}

// Enter on Q2 while Q1's pull runs: the landing pull shows Q2's hits, with one
// fetch in all.
func TestSearchInflightShowsLatestQuery(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs()}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), f, c)
	m = send(m, key(":"))
	m.search.input.SetValue("name logon")
	m, inflight := sendCmd(m, key("enter"))
	m.search.input.SetValue("name catch")
	m, cmd := sendCmd(m, key("enter"))
	if cmd != nil {
		t.Fatal("Q2 queued or started a pull")
	}
	m = drive(m, inflight)
	if got := strings.Join(hitNames(m), ","); got != "catch-all" || f.calls != 1 {
		t.Fatalf("hits=%s calls=%d, want Q2's hits and one fetch", got, f.calls)
	}
	if m.search.query != ":name catch" {
		t.Fatalf("shown query %q", m.search.query)
	}
}

// The searched listener leaving the snapshot: a notice, and no pull for it.
func TestSearchListenerGone(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs()}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes"), tcpListener("z-tcp")), f, c)
	m = send(m, key(":"))
	m = send(m, snapshotMsg{snap: listenerSnap(t, tcpListener("z-tcp"))})
	if !strings.Contains(m.View(), searchGone) {
		t.Fatalf("no gone notice:\n%s", m.View())
	}
	m.search.input.SetValue("name logon")
	m, cmd := sendCmd(m, key("enter"))
	if cmd != nil || f.calls != 0 || m.search.status != searchGone {
		t.Fatalf("pulled for a gone listener: cmd=%v calls=%d status=%q", cmd != nil, f.calls, m.search.status)
	}
	// It comes back: the search works again.
	m = send(m, snapshotMsg{snap: listenerSnap(t, rdsListener("a-edge", "edge-routes"), tcpListener("z-tcp"))})
	_ = query(m, "name logon")
	if f.calls != 1 {
		t.Fatalf("calls=%d", f.calls)
	}
}

// Refs changing while the pull runs: the old configs are neither cached nor
// shown, and the status asks for a refresh.
func TestSearchRefsChangedMidFlight(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs()}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), f, c)
	m = query(send(m, key(":")), "name logon")
	c.Advance(11 * time.Second)
	m.search.input.SetValue("name catch")
	m, inflight := sendCmd(m, key("enter"))
	m = send(m, snapshotMsg{snap: listenerSnap(t, rdsListener("a-edge", "edge-routes-v2"))})
	m = drive(m, inflight)
	if m.search.status != searchChanged || len(hitNames(m)) != 0 {
		t.Fatalf("status=%q hits=%v", m.search.status, hitNames(m))
	}
	m = query(m, "name catch")
	if f.calls != 3 {
		t.Fatalf("the old configs were cached: calls=%d", f.calls)
	}
}

// A missing route config stays reported for as long as the partial result is
// served from the cache, without pulling again.
func TestSearchMissingConfigWarningSurvivesCache(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{err: &snapshot.MissingRouteConfigError{Names: []string{"edge-routes"}}}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), f, c)
	const warn = `route config "edge-routes" not found`
	m = query(send(m, key(":")), "name logon")
	if m.search.status != warn || !m.search.statusErr {
		t.Fatalf("first query: status=%q err=%v", m.search.status, m.search.statusErr)
	}
	c.Advance(5 * time.Second)
	m = query(m, "name catch")
	if f.calls != 1 || m.search.status != warn || !m.search.statusErr {
		t.Fatalf("changed query within 10s: calls=%d status=%q", f.calls, m.search.status)
	}
	c.Advance(30 * time.Second)
	m = query(m, "name catch")
	if f.calls != 1 || m.search.status != warn || !m.search.statusErr {
		t.Fatalf("repeated query: calls=%d status=%q", f.calls, m.search.status)
	}
}

// While the listener dump fails the listeners are unknown, not changed: a
// pull is cached and shown, and the pull rule holds.
func TestSearchWorksWhileListenersUnavailable(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs()}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), f, c)
	m = send(m, key(":"))
	bad := listenerSnap(t)
	bad.ListenersErr = "listeners unavailable: 503"
	m = send(m, snapshotMsg{snap: bad})
	for i := 0; i < 3; i++ {
		m = query(m, "name logon")
		if got := strings.Join(hitNames(m), ","); got != "logon,logon-any" {
			t.Fatalf("enter %d: hits=%s status=%q", i+1, got, m.search.status)
		}
	}
	if f.calls != 1 {
		t.Fatalf("calls=%d, want 1", f.calls)
	}
}

// A browser "Copy as cURL" paste is several KB; the input keeps it whole.
func TestSearchInputAcceptsLongPaste(t *testing.T) {
	c := &clock{now: fixedNow}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), &fakeRoutes{cfgs: edgeConfigs()}, c)
	m = send(m, key(":"))
	line := "curl 'http://h/logon' -H 'cookie: " + strings.Repeat("a", 4096) + "'"
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(line), Paste: true})
	if got := m.search.input.Value(); got != line {
		t.Fatalf("input kept %d of %d bytes", len(got), len(line))
	}
}

// An HCM with scoped_routes has no route refs: say so, and pull nothing.
func TestSearchScopedRoutesNotSupported(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs()}
	scoped := listenerJSON("a-scoped", `{"name":"envoy.filters.network.http_connection_manager","typed_config":{"@type":"`+
		hcmType+`","scoped_routes":{"name":"s","scope_key_builder":{}}}}`)
	m := searchModel(t, listenerSnap(t, scoped), f, c)
	m = send(m, key(":"))
	if !strings.Contains(m.View(), "scoped routes are not supported") {
		t.Fatalf("no notice on open:\n%s", m.View())
	}
	m.search.input.SetValue("name logon")
	m, cmd := sendCmd(m, key("enter"))
	if cmd != nil || f.calls != 0 || m.search.status != "scoped routes are not supported" || !m.search.statusErr {
		t.Fatalf("cmd=%v calls=%d status=%q", cmd != nil, f.calls, m.search.status)
	}
}

func firstListRow(m Model) string {
	lines := strings.Split(m.View(), "\n")
	return lines[m.headerRows()+m.bannerRows]
}

func TestListenersTabHintsAtRouteSearch(t *testing.T) {
	c := &clock{now: fixedNow}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), &fakeRoutes{cfgs: edgeConfigs()}, c)
	if row := firstListRow(m); !strings.Contains(row, "/ to filter or : to routes") {
		t.Fatalf("Listeners first row = %q", row)
	}
	for _, k := range []string{"shift+tab", "tab tab"} {
		o := m
		for _, kk := range strings.Fields(k) {
			o = send(o, key(kk))
		}
		row := firstListRow(o)
		if !strings.Contains(row, "/ to filter") || strings.Contains(row, "to routes") {
			t.Fatalf("other tab first row = %q", row)
		}
	}
	// Filtering or a query keeps the old behaviour.
	f := send(m, key("/"))
	if strings.Contains(firstListRow(f), "to routes") {
		t.Fatalf("hint shown while filtering: %q", firstListRow(f))
	}
	f = send(send(f, key("a")), key("enter"))
	if r := firstListRow(f); strings.Contains(r, "to routes") || !strings.Contains(r, "/ a") {
		t.Fatalf("query row = %q", r)
	}
}

func TestListenersHintFitsListWidth(t *testing.T) {
	c := &clock{now: fixedNow}
	for _, w := range []int{80, 160} {
		m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), &fakeRoutes{cfgs: edgeConfigs()}, c)
		m = sized(m, w, 30)
		row := firstListRow(m)
		if !strings.Contains(row, "/ to filter or : to routes") {
			t.Fatalf("w=%d row=%q", w, row)
		}
		for i, l := range strings.Split(m.View(), "\n") {
			if lipgloss.Width(l) > w {
				t.Fatalf("w=%d line %d overflows: %d", w, i, lipgloss.Width(l))
			}
		}
	}
	// A pane narrower than the hint truncates it safely.
	l := newListPane()
	l.SetHint("/ to filter or : to routes")
	l.SetSize(10, 3)
	for _, r := range strings.Split(l.View(), "\n") {
		if lipgloss.Width(r) > 10 {
			t.Fatalf("overflow: %q", r)
		}
	}
}

// A :method header matcher sees the -X method, and GET without -X.
func TestSearchByCurlMethod(t *testing.T) {
	const rc = `{"name":"m-routes","virtual_hosts":[{"name":"vh","domains":["*"],"routes":[` +
		`{"name":"post-only","match":{"prefix":"/","headers":[{"name":":method","string_match":{"exact":"POST"}}]},"route":{"cluster":"a"}}]}]}`
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: []snapshot.RouteConfig{snapshot.ParseRouteConfig("m-routes", "v1", false, []byte(rc))}}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "m-routes")), f, c)
	m = send(m, key(":"))
	m = query(m, "curl -X POST http://h/x")
	if got := strings.Join(hitNames(m), ","); got != "post-only" {
		t.Fatalf("-X POST hits = %q", got)
	}
	m = query(m, "curl http://h/x")
	if got := hitNames(m); len(got) != 0 {
		t.Fatalf("GET hits = %v", got)
	}
}

func TestSearchHintListsAllForms(t *testing.T) {
	for _, want := range []string{":name <text>", ":curl ...", ":all"} {
		if !strings.Contains(searchHint, want) {
			t.Fatalf("hint %q lacks %q", searchHint, want)
		}
	}
}

func TestSearchAllListsEveryRoute(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs()}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), f, c)
	m = query(send(m, key(":")), "all")
	if got := strings.Join(hitNames(m), ","); got != "logon,logon-any,catch-all" {
		t.Fatalf("hits = %s", got)
	}
	v := m.View()
	if !strings.Contains(v, "3 routes") || strings.Contains(v, "any host") {
		t.Fatalf("status:\n%s", v)
	}
	// :all after :all re-matches from the cache
	m = query(m, "all  ")
	if f.calls != 1 || len(hitNames(m)) != 3 {
		t.Fatalf("calls=%d hits=%v", f.calls, hitNames(m))
	}
	f.cfgs = nil
	m = query(m, "all foo")
	if f.calls != 1 || !strings.Contains(m.View(), searchHint) {
		t.Fatalf("':all foo' calls=%d\n%s", f.calls, m.View())
	}
}

func TestSearchAllSingularAndEmptyStatus(t *testing.T) {
	for n, want := range map[int]string{0: "0 routes", 1: "1 route"} {
		var routes []string
		for i := 0; i < n; i++ {
			routes = append(routes, `{"name":"only","match":{"prefix":"/"}}`)
		}
		rc := `{"name":"e","virtual_hosts":[{"name":"vh","domains":["*"],"routes":[` + strings.Join(routes, ",") + `]}]}`
		f := &fakeRoutes{cfgs: []snapshot.RouteConfig{snapshot.ParseRouteConfig("e", "v1", false, []byte(rc))}}
		m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "e")), f, &clock{now: fixedNow})
		m = query(send(m, key(":")), "all")
		if v := m.View(); !strings.Contains(v, want+"\n") && !strings.Contains(v, want+" ") {
			t.Fatalf("n=%d lacks %q:\n%s", n, want, v)
		}
		if m.search.status != want {
			t.Fatalf("status = %q, want %q", m.search.status, want)
		}
	}
}

func TestSearchAllPullRules(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs()}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), f, c)
	m = send(m, key(":"))
	m = query(m, "name logon")
	c.Advance(5 * time.Second)
	m = query(m, "all")
	if f.calls != 1 || len(hitNames(m)) != 3 {
		t.Fatalf(":name then :all within 10s: calls=%d hits=%v", f.calls, hitNames(m))
	}
	c.Advance(30 * time.Second)
	m = query(m, "all")
	if f.calls != 1 {
		t.Fatalf("an unchanged :all pulled: calls=%d", f.calls)
	}
	_ = query(m, "name logon")
	if f.calls != 2 {
		t.Fatalf("a changed stale query did not pull: calls=%d", f.calls)
	}

	// :all with no cache pulls once; one pull in flight at most.
	f2 := &fakeRoutes{cfgs: edgeConfigs()}
	m2 := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), f2, c)
	m2 = send(m2, key(":"))
	m2.search.input.SetValue("all")
	m2, inflight := sendCmd(m2, key("enter"))
	if inflight == nil {
		t.Fatal(":all without a cache issued no pull")
	}
	m2.search.input.SetValue("name logon")
	m2, cmd := sendCmd(m2, key("enter"))
	if cmd != nil {
		t.Fatal("a second pull was issued while one was in flight")
	}
	m2 = drive(m2, inflight)
	if f2.calls != 1 {
		t.Fatalf("calls=%d", f2.calls)
	}
}

func TestSearchAllMissingConfigWarning(t *testing.T) {
	c := &clock{now: fixedNow}
	f := &fakeRoutes{cfgs: edgeConfigs(), err: &snapshot.MissingRouteConfigError{Names: []string{"gone-routes"}}}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), f, c)
	m = query(send(m, key(":")), "all")
	if len(hitNames(m)) != 3 || !m.search.statusErr || !strings.Contains(m.search.status, "gone-routes") {
		t.Fatalf("hits=%v status=%q err=%v", hitNames(m), m.search.status, m.search.statusErr)
	}
}

func TestSearchAllGuards(t *testing.T) {
	c := &clock{now: fixedNow}
	// route search unavailable
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), nil, c)
	m = query(send(m, key(":")), "all")
	if m.search.status != searchUnavailable {
		t.Fatalf("status = %q", m.search.status)
	}
	// no HTTP routes
	f := &fakeRoutes{cfgs: edgeConfigs()}
	m = searchModel(t, listenerSnap(t, tcpListener("z-tcp")), f, c)
	m = query(send(m, key(":")), "all")
	if f.calls != 0 || m.search.status != snapshot.ErrNoHTTPRoutes.Error() {
		t.Fatalf("tcp: calls=%d status=%q", f.calls, m.search.status)
	}
	// listener gone
	m = searchModel(t, listenerSnap(t, rdsListener("a-edge", "edge-routes")), f, c)
	m = send(m, key(":"))
	m = send(m, snapshotMsg{snap: listenerSnap(t, tcpListener("z-tcp"))})
	m = query(m, "all")
	if f.calls != 0 || m.search.status != searchGone {
		t.Fatalf("gone: calls=%d status=%q", f.calls, m.search.status)
	}
}

func TestSearchAllLargeConfig(t *testing.T) {
	const n = 3400
	var b strings.Builder
	b.WriteString(`{"name":"big","virtual_hosts":[`)
	for vh := 0; vh < 4; vh++ {
		if vh > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"name":"vh` + strconv.Itoa(vh) + `","domains":["h` + strconv.Itoa(vh) + `.example.com"],"routes":[`)
		for i := 0; i < n/4; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"name":"route-` + strconv.Itoa(vh) + `-` + strconv.Itoa(i) + `","match":{"prefix":"/p` + strconv.Itoa(i) + `"},"route":{"cluster":"c"}}`)
		}
		b.WriteString(`]}`)
	}
	b.WriteString(`]}`)
	f := &fakeRoutes{cfgs: []snapshot.RouteConfig{snapshot.ParseRouteConfig("big", "v1", false, []byte(b.String()))}}
	m := searchModel(t, listenerSnap(t, rdsListener("a-edge", "big")), f, &clock{now: fixedNow})
	m = query(send(m, key(":")), "all")
	if got := len(hitNames(m)); got != n {
		t.Fatalf("hits = %d", got)
	}
	if m.search.status != "3400 routes" {
		t.Fatalf("status = %q", m.search.status)
	}
	for i := 0; i < 50; i++ {
		m = send(m, key("down"))
	}
	m = send(m, key("pgdown"))
	v := m.View()
	if !strings.Contains(v, "route-0-50") || !strings.Contains(v, "3400 routes") {
		t.Fatalf("view after selecting:\n%s", v)
	}
}

func TestSearchPlaceholderFits(t *testing.T) {
	for _, want := range []string{"name <text>", "all", "curl [-H 'Name: value']... [-X method] <url or /path>"} {
		if !strings.Contains(searchPlaceholder, want) {
			t.Fatalf("placeholder lacks %q", want)
		}
	}
	// one line that fits the minimum terminal width next to a short prompt
	if w := lipgloss.Width(searchPlaceholder); w > 78 {
		t.Fatalf("placeholder row is %d wide", w)
	}
}
