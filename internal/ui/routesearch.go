package ui

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/envoyadmin/internal/snapshot"
)

// RouteFetcher pulls one listener's route configs (snapshot.FetchRoutes).
type RouteFetcher func(ctx context.Context, l snapshot.Listener) ([]snapshot.RouteConfig, error)

// routesMsg is a finished pull for listener, started by query.
type routesMsg struct {
	listener, query string
	cfgs            []snapshot.RouteConfig
	err             error
	at              time.Time
}

const (
	searchHint        = "type :name <text>, :curl ... or :all"
	searchUnavailable = "route search unavailable"
	searchPulling     = "pulling routes…"
	searchReady       = "press enter to run"
	searchChanged     = "listener changed, press enter to refresh"
	searchGone        = "listener gone"
	searchScoped      = "scoped routes are not supported"
	searchPlaceholder = "all | name <text> | curl [-H 'Name: value']... [-X method] <url or /path>"
	searchAnyHost     = " (any host, no winner)"
	maxStatusRows     = 4
)

// parseSearch splits ":name <text>", ":curl <args>" or ":all". ok is false for
// any other command, an empty argument of :name or :curl, or an argument of :all.
func parseSearch(input string) (cmd, arg string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(input), ":")
	if !found {
		return "", "", false
	}
	cmd, arg, _ = strings.Cut(rest, " ")
	arg = strings.TrimSpace(arg)
	switch {
	case cmd == "all" && arg == "":
	case (cmd == "name" || cmd == "curl") && arg != "":
	default:
		return "", "", false
	}
	return cmd, arg, true
}

// routePull is the one pull in flight, and the refs it was started with.
type routePull struct {
	active          bool
	listener, query string
	refs            []snapshot.RouteRef
}

// routeSearch is the search mode of the Listeners view. It has its own panes,
// so the listener list keeps its selection, filter and scroll.
type routeSearch struct {
	open      bool
	listener  snapshot.Listener
	input     textinput.Model
	query     string // the query whose results are shown
	want      string // the latest query entered, shown when its pull lands
	gone      bool   // the listener left the snapshot
	status    string
	statusErr bool
	list      listPane
	detail    detailPane
	w, h      int // the list column
	fullW     int // the whole terminal, the input row's width
}

func newRouteSearch(l snapshot.Listener, color bool) routeSearch {
	ti := textinput.New()
	ti.Prompt = l.Name + " :"
	ti.CharLimit = 16384 // a browser "Copy as cURL" is several KB
	ti.Placeholder = searchPlaceholder
	ti.Focus()
	s := routeSearch{open: true, listener: l, input: ti, list: newListPane(), detail: newDetailPane(color)}
	s.list.SetEmptyText(searchHint)
	s.detail.SetEmptyText("no route selected")
	s.detail.SetTab(tabConfig)
	return s
}

func (s *routeSearch) setStatus(text string, isErr bool) {
	s.status, s.statusErr = text, isErr
	s.list.SetSize(s.w, max(s.h-len(s.statusRows()), 1))
}

// statusRows is the status wrapped to the column, like the banner: at most
// maxStatusRows, ending with an ellipsis when cut, and always one row.
func (s routeSearch) statusRows() []string {
	rows := wrapLines(s.status, max(s.w, 1), min(maxStatusRows, max(s.h-2, 1)))
	if len(rows) == 0 {
		rows = []string{""}
	}
	style := dimStyle
	if s.statusErr {
		style = warnStyle
	}
	for i, r := range rows {
		rows[i] = style.Render(r)
	}
	return rows
}

// SetSize sizes the panes: the input row is fullW wide, above both panes, the
// list column is listW wide and the detail detailW.
func (s *routeSearch) SetSize(fullW, listW, detailW, h int) {
	s.w, s.h, s.fullW = listW, h, fullW
	s.list.SetSize(listW, max(h-len(s.statusRows()), 1))
	s.input.Width = max(fullW-lipgloss.Width(s.input.Prompt)-1, 1)
	s.detail.SetSize(detailW, h)
}

func (s *routeSearch) sync() {
	it, ok := s.list.Selected()
	s.detail.SetItem(it, ok)
}

// show replaces the results with the hits of query in cfgs. It reports
// whether the query is a hostless :curl.
func (s *routeSearch) show(query string, cfgs []snapshot.RouteConfig) (anyHost bool) {
	cmd, arg, _ := parseSearch(query)
	var hits []snapshot.RouteHit
	switch cmd {
	case "curl":
		if req, err := snapshot.ParseCurl(arg); err == nil {
			hits = snapshot.MatchRoutes(cfgs, req)
			anyHost = req.AnyHost
		}
	case "all":
		hits = snapshot.SearchRoutesAll(cfgs)
	default:
		hits = snapshot.SearchRoutesByName(cfgs, arg)
	}
	s.list.SetEmptyText("no routes found")
	s.list.SetItems(routeHitItems(hits))
	status := plural(len(hits), "route")
	if anyHost {
		status = anyHostStatus(hits)
	}
	s.setStatus(status, false)
	s.sync()
	return anyHost
}

// anyHostStatus counts the matches of a hostless search apart from the routes
// that could not be evaluated, which are never matches.
func anyHostStatus(hits []snapshot.RouteHit) string {
	matched, unknown := 0, 0
	for _, h := range hits {
		if h.NotEvaluated {
			unknown++
		} else {
			matched++
		}
	}
	verb := " match"
	if matched == 1 {
		verb = " matches"
	}
	status := plural(matched, "route") + verb
	if unknown > 0 {
		status += ", " + strconv.Itoa(unknown) + " not evaluated"
	}
	return status + searchAnyHost
}

// showEntry shows the hits of query in a cached pull, keeping its warning
// about missing route configs for as long as the entry is served.
func (s *routeSearch) showEntry(query string, e routeEntry) {
	anyHost := s.show(query, e.cfgs)
	if err := e.missingErr(); err != nil {
		text := err.Error()
		if anyHost {
			text += searchAnyHost
		}
		s.setStatus(text, true)
	}
}

// onlyScoped: the listener's routes come only from scoped_routes, which are
// not read.
func onlyScoped(l snapshot.Listener) bool {
	return l.HasScopedRoutes() && len(l.RouteRefs()) == 0
}

// inputView is the input row, exactly w cells wide.
func (s routeSearch) inputView(w int) string {
	return pad(trunc(s.input.View(), w), w)
}

// listView is the list column: the status, then the results.
func (s routeSearch) listView(w, h int) string {
	rows := s.statusRows()
	listRows := strings.Split(s.list.View(), "\n")
	if len(listRows) > 1 {
		rows = append(rows, listRows[1:]...) // drop the list's own filter row
	}
	if len(rows) > h {
		rows = rows[:h]
	}
	for i, r := range rows {
		rows[i] = pad(trunc(r, w), w)
	}
	return strings.Join(rows, "\n")
}

// searching is true while the route search replaces the Listeners view.
func (m Model) searching() bool { return m.search.open && m.active == kindListeners }

// openSearch starts the search on the selected listener, if the keys allow it.
func (m *Model) openSearch() bool {
	if m.active != kindListeners || m.focus != focusList || m.cur().list.filtering {
		return false
	}
	it, ok := m.cur().list.Selected()
	li, isListener := it.(listenerItem)
	if !ok || !isListener {
		return false
	}
	m.search = newRouteSearch(li.l, m.opts.Color)
	switch {
	case m.opts.Routes == nil:
		m.search.setStatus(searchUnavailable, true)
	case !li.l.HasHTTP():
		m.search.setStatus(snapshot.ErrNoHTTPRoutes.Error(), true)
	case onlyScoped(li.l):
		m.search.setStatus(searchScoped, true)
	}
	m.layout()
	return true
}

func (m Model) handleSearchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := &m.search
	switch k.String() {
	case "esc":
		s.open = false
		m.layout()
		return m, nil
	case "enter":
		return m, m.runSearch()
	case "up", "ctrl+p":
		s.list.HandleKey(tea.KeyMsg{Type: tea.KeyUp})
	case "down", "ctrl+n":
		s.list.HandleKey(tea.KeyMsg{Type: tea.KeyDown})
	case "pgup", "pgdown":
		s.detail.HandleKey(k)
		return m, nil
	case "tab", "shift+tab":
		if s.detail.tab == tabConfig {
			s.detail.SetTab(tabOverview)
		} else {
			s.detail.SetTab(tabConfig)
		}
		return m, nil
	default:
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(k)
		return m, cmd
	}
	s.sync()
	return m, nil
}

// runSearch runs the typed query: from the cache when it holds the listener's
// configs and the query is unchanged or the configs are fresh, otherwise by
// one pull. It never starts a second pull while one is in flight.
func (m *Model) runSearch() tea.Cmd {
	s := &m.search
	cmd, arg, ok := parseSearch(":" + strings.TrimPrefix(strings.TrimSpace(s.input.Value()), ":"))
	switch {
	case !ok:
		s.setStatus(searchHint, false)
		return nil
	case m.opts.Routes == nil:
		s.setStatus(searchUnavailable, true)
		return nil
	case s.gone:
		s.setStatus(searchGone, true)
		return nil
	case !s.listener.HasHTTP():
		s.setStatus(snapshot.ErrNoHTTPRoutes.Error(), true)
		return nil
	case onlyScoped(s.listener):
		s.setStatus(searchScoped, true)
		return nil
	}
	if cmd == "curl" {
		if _, err := snapshot.ParseCurl(arg); err != nil {
			s.setStatus("curl: "+err.Error(), true)
			return nil
		}
	}
	query := strings.TrimSpace(":" + cmd + " " + arg)
	s.want = query
	l, now := s.listener, m.opts.Clock()
	refs := l.RouteRefs()
	if e, hit := m.routes.Get(l.Name, refs); hit && (query == s.query || m.routes.Fresh(e.at, now)) {
		s.query = query
		s.showEntry(query, e)
		return nil
	}
	if m.pull.active {
		s.setStatus(searchPulling, false) // shown for s.want when the pull lands; nothing queued
		return nil
	}
	m.pull = routePull{active: true, listener: l.Name, query: query, refs: refs}
	s.setStatus(searchPulling, false)
	fetch, ctx := m.opts.Routes, m.opts.Ctx
	return func() tea.Msg {
		cfgs, err := fetch(ctx, l)
		return routesMsg{listener: l.Name, query: query, cfgs: cfgs, err: err, at: now}
	}
}

// handleRoutes takes the pull in flight. Its configs go into the cache
// whatever the search shows now, as long as the listener's refs did not change
// since the pull started. A search open on that listener then shows the
// latest query entered, matched from the cache; a search on another listener
// only loses its "pulling" status. A message that is not the pull in flight
// is stale and ignored. A failed pull keeps the results and their query, so
// Enter retries it.
func (m *Model) handleRoutes(msg routesMsg) {
	p := m.pull
	if !p.active || p.listener != msg.listener || p.query != msg.query {
		return
	}
	m.pull = routePull{}

	var missing *snapshot.MissingRouteConfigError
	failed := msg.err != nil && !errors.As(msg.err, &missing)
	// While the listener dump fails the listeners are unknown, not changed
	// (as in syncRoutes): the pull's own refs stand.
	unchanged := true
	if m.snap.ListenersErr == "" {
		l, live := m.liveListener(msg.listener)
		unchanged = live && refsEqual(p.refs, l.RouteRefs())
	}
	if !failed && unchanged {
		var names []string
		if missing != nil {
			names = missing.Names
		}
		m.routes.Put(msg.listener, p.refs, msg.cfgs, names, msg.at)
		m.routeRefs[msg.listener] = p.refs
	}

	s := &m.search
	switch {
	case !s.open:
		return
	case s.listener.Name != msg.listener:
		if s.status == searchPulling {
			s.setStatus(searchReady, false) // its Enter was refused while this pull ran
		}
		return
	case s.gone:
		return // the gone notice stays
	case failed:
		s.setStatus(msg.err.Error(), true) // keep the previous results
		return
	case !unchanged:
		s.query = ""
		s.list.SetItems(nil) // never hits from configs of the old refs
		s.sync()
		s.setStatus(searchChanged, true)
		return
	case s.want == "":
		s.setStatus("", false) // reopened since the pull started; nothing entered yet
		return
	}
	e, _ := m.routes.Get(msg.listener, p.refs)
	s.query = s.want
	s.showEntry(s.want, e)
}

// liveListener is the named listener of the current snapshot.
func (m Model) liveListener(name string) (snapshot.Listener, bool) {
	if m.snap.ListenersErr != "" {
		return snapshot.Listener{}, false
	}
	for _, l := range m.snap.Listeners {
		if l.Name == name {
			return l, true
		}
	}
	return snapshot.Listener{}, false
}

// syncRoutes drops cached configs of listeners that are gone or whose route
// refs (RDS names or inline bytes) changed. It never starts a pull.
func (m *Model) syncRoutes(s snapshot.Snapshot) {
	if s.ListenersErr != "" {
		return // the listeners are unknown, not gone
	}
	m.routes.Prune(s.Listeners)
	live := make(map[string]snapshot.Listener, len(s.Listeners))
	for _, l := range s.Listeners {
		live[l.Name] = l
	}
	for name, refs := range m.routeRefs {
		if l, ok := live[name]; !ok || !refsEqual(refs, l.RouteRefs()) {
			m.routes.Forget(name)
			delete(m.routeRefs, name)
		}
	}
	if !m.search.open {
		return
	}
	if l, ok := live[m.search.listener.Name]; ok {
		m.search.listener, m.search.gone = l, false
		if m.search.status == searchGone {
			m.search.setStatus(searchReady, false)
		}
	} else if !m.search.gone {
		m.search.gone = true
		m.search.setStatus(searchGone, true)
	}
}

func refsEqual(a, b []snapshot.RouteRef) bool {
	return slices.EqualFunc(a, b, func(x, y snapshot.RouteRef) bool {
		return x.Name == y.Name && bytes.Equal(x.Inline, y.Inline)
	})
}
