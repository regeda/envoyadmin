package snapshot

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// RouteHit is one route found by a search, with where it lives.
type RouteHit struct {
	Config       string
	VirtualHost  string
	Domains      []string
	Route        Route
	Winner       bool
	Tentative    bool
	NotEvaluated bool
	AnyHost      bool
}

// Status names the hit for display.
func (h RouteHit) Status() string {
	switch {
	case h.Winner && h.Tentative:
		return "winner (tentative)"
	case h.Winner:
		return "winner"
	case h.NotEvaluated:
		return "not evaluated"
	default:
		return "match"
	}
}

// SearchRoutesByName returns the routes whose name contains text,
// case-insensitively, in config/virtual host/route order. A nameless route
// never matches; empty text matches nothing.
func SearchRoutesByName(cfgs []RouteConfig, text string) []RouteHit {
	if text == "" {
		return nil
	}
	needle := strings.ToLower(text)
	var hits []RouteHit
	for _, c := range cfgs {
		for _, vh := range c.VirtualHosts {
			for _, r := range vh.Routes {
				if r.Name != "" && strings.Contains(strings.ToLower(r.Name), needle) {
					hits = append(hits, RouteHit{Config: c.Name, VirtualHost: vh.Name, Domains: vh.Domains, Route: r})
				}
			}
		}
	}
	return hits
}

// SearchRoutesAll returns every route of every virtual host of cfgs as a plain
// match, in config/virtual host/route order.
func SearchRoutesAll(cfgs []RouteConfig) []RouteHit {
	var hits []RouteHit
	for _, c := range cfgs {
		for _, vh := range c.VirtualHosts {
			for _, r := range vh.Routes {
				hits = append(hits, RouteHit{Config: c.Name, VirtualHost: vh.Name, Domains: vh.Domains, Route: r})
			}
		}
	}
	return hits
}

// MatchRoutes follows Envoy's route selection for req in each config: the
// virtual host is chosen by domain, then routes are tested in order. The first
// match is the winner (tentative when an unevaluated route precedes it); later
// matches are listed too. Unevaluated routes are listed only before the winner,
// or all of them when nothing matched.
//
// A hostless request (req.AnyHost) has a wildcard host: every virtual host is
// searched, :authority matchers count as matching, and no winner is claimed,
// since which route Envoy picks depends on the host. Every matching route is a
// plain match and every unevaluated one is listed, in config, virtual host and
// route order.
func MatchRoutes(cfgs []RouteConfig, req Request) []RouteHit {
	v := newRequestView(req)
	if req.AnyHost {
		return matchAnyHost(cfgs, &v)
	}
	var hits []RouteHit
	for _, c := range cfgs {
		if vh := chooseVirtualHost(c.VirtualHosts, req.Host, req.Port); vh != nil {
			hits = append(hits, matchVirtualHost(c.Name, vh, &v)...)
		}
	}
	return hits
}

// matchVirtualHost tests the routes of vh in order: unevaluated routes before
// the winner, the winner, then the matches after it.
func matchVirtualHost(config string, vh *VirtualHost, v *requestView) []RouteHit {
	hit := func(r Route) RouteHit {
		return RouteHit{Config: config, VirtualHost: vh.Name, Domains: vh.Domains, Route: r}
	}
	won, unevaluated := false, false
	var pending []RouteHit // unevaluated routes before the winner
	var after []RouteHit   // matches after the winner
	var winner RouteHit
	for _, r := range vh.Routes {
		switch r.match.eval(v) {
		case matchUnknown:
			if !won {
				unevaluated = true
				h := hit(r)
				h.NotEvaluated = true
				pending = append(pending, h)
			}
		case matchYes:
			if !won {
				won = true
				winner = hit(r)
				winner.Winner, winner.Tentative = true, unevaluated
			} else {
				after = append(after, hit(r))
			}
		}
	}
	hits := pending
	if won {
		hits = append(hits, winner)
		hits = append(hits, after...)
	}
	return hits
}

// matchAnyHost lists every route of every virtual host that matches v or
// cannot be evaluated, with no winner.
func matchAnyHost(cfgs []RouteConfig, v *requestView) []RouteHit {
	var hits []RouteHit
	for _, c := range cfgs {
		for _, vh := range c.VirtualHosts {
			for _, r := range vh.Routes {
				res := r.match.eval(v)
				if res == matchNo {
					continue
				}
				hits = append(hits, RouteHit{
					Config: c.Name, VirtualHost: vh.Name, Domains: vh.Domains, Route: r,
					NotEvaluated: res == matchUnknown, AnyHost: true,
				})
			}
		}
	}
	return hits
}

// chooseVirtualHost picks the virtual host for host[:port] the way Envoy
// does: exact domain, then the longest "*suffix", then the longest "prefix*",
// then "*"; case-insensitive. The port is compared only for domains that
// carry one. Ties go to the first in config order.
func chooseVirtualHost(vhs []VirtualHost, host, port string) *VirtualHost {
	host = strings.ToLower(host)
	withPort := host
	if port != "" {
		withPort = host + ":" + port
	}
	best, bestRank, bestLen := -1, rankNone, -1
	for i := range vhs {
		for _, d := range vhs[i].Domains {
			d = strings.ToLower(d)
			h := host
			if domainHasPort(d) {
				h = withPort
			}
			rank, n := domainRank(d, h)
			if rank > bestRank || (rank == bestRank && rank != rankNone && n > bestLen) {
				best, bestRank, bestLen = i, rank, n
			}
		}
	}
	if best < 0 {
		return nil
	}
	return &vhs[best]
}

// Ranks of a domain match, best last.
const (
	rankNone = iota
	rankAny
	rankPrefix
	rankSuffix
	rankExact
)

// domainRank is how well the lower-cased domain d matches host h, and the
// domain length that breaks ties between equal ranks.
func domainRank(d, h string) (rank, n int) {
	switch {
	case d == "*":
		return rankAny, 0
	case d == h:
		return rankExact, len(d)
	case strings.HasPrefix(d, "*") && len(h) > len(d)-1 && strings.HasSuffix(h, d[1:]):
		return rankSuffix, len(d)
	case strings.HasSuffix(d, "*") && len(h) > len(d)-1 && strings.HasPrefix(h, d[:len(d)-1]):
		return rankPrefix, len(d)
	}
	return rankNone, 0
}

// domainHasPort reports whether a configured domain ends in ":<digits>".
func domainHasPort(d string) bool {
	i := strings.LastIndexByte(d, ':')
	if i < 0 || i == len(d)-1 || strings.Contains(d[:i], ":") && !strings.HasSuffix(d[:i], "]") {
		return false
	}
	for _, c := range d[i+1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

type matchResult int

const (
	matchNo matchResult = iota
	matchYes
	matchUnknown
)

type pathKind int

const (
	pathNone pathKind = iota
	pathExact
	pathPrefix
	pathSeparated
	pathRegex
)

type strKind int

const (
	strExact strKind = iota
	strPrefix
	strSuffix
	strContains
	strRegex
)

// strMatcher is a decoded StringMatcher. re is compiled lazily by
// routeMatch.compile.
type strMatcher struct {
	re         *regexp.Regexp
	value      string
	lower      string
	kind       strKind
	ignoreCase bool
	bad        bool
}

type headerKind int

const (
	headerString headerKind = iota
	headerPresent
	headerRange
)

type headerMatcher struct {
	name           string
	str            strMatcher
	kind           headerKind
	start          int64
	end            int64
	present        bool
	invert         bool
	missingAsEmpty bool
}

type queryMatcher struct {
	str  *strMatcher
	name string
}

// routeMatch is a route's match decoded once by ParseRouteConfig.
// unsupported means a part of the match could not be decoded: the route is
// never a match, but the decoded parts can still make it a miss.
type routeMatch struct {
	pathRe        *strMatcher
	pathValue     string
	headers       []headerMatcher
	query         []queryMatcher
	path          pathKind
	once          sync.Once
	unsupported   bool
	caseSensitive bool
}

// compile compiles every regex of the route once; a failure makes that
// matcher unevaluable.
func (m *routeMatch) compile() {
	m.once.Do(func() {
		ms := []*strMatcher{m.pathRe}
		for i := range m.headers {
			if m.headers[i].kind == headerString {
				ms = append(ms, &m.headers[i].str)
			}
		}
		for _, q := range m.query {
			ms = append(ms, q.str)
		}
		for _, s := range ms {
			if s != nil && s.kind == strRegex {
				s.compileRegex()
			}
		}
	})
}

// compileRegex compiles the whole-string regex, marking the matcher bad on
// a failure.
func (s *strMatcher) compileRegex() {
	// Validate the pattern alone first: an unbalanced pattern such
	// as "x)|(.*" would close the wrapping group and compile.
	if _, err := regexp.Compile(s.value); err != nil {
		s.bad = true
		return
	}
	re, err := regexp.Compile(`^(?:` + s.value + `)$`)
	if err != nil {
		s.bad = true
		return
	}
	s.re = re
}

// requestView is a Request with the pseudo-headers precomputed.
type requestView struct {
	authority string
	req       Request
	hasQuery  bool
}

func newRequestView(req Request) requestView {
	host := req.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	authority := host
	if req.Port != "" {
		authority = host + ":" + req.Port
	}
	return requestView{req: req, authority: authority, hasQuery: len(req.Query) > 0}
}

// header returns the value Envoy's header matching sees: pseudo-headers from
// the request line, other headers with multiple values joined by ",".
func (v *requestView) header(name string) (string, bool) {
	switch name {
	case ":authority":
		return v.authority, true
	case ":path": // only consulted when the request has no query
		return v.req.Path, true
	case ":method":
		return v.req.Method, v.req.Method != ""
	}
	vals, ok := v.req.Headers[name]
	if !ok {
		return "", false
	}
	if len(vals) == 1 {
		return vals[0], true
	}
	return strings.Join(vals, ","), true
}

// eval tests the route against the request. Any definite miss by a matcher
// that can be evaluated is a miss; otherwise a matcher that cannot be
// evaluated makes the result unknown.
func (m *routeMatch) eval(v *requestView) matchResult {
	if m == nil {
		return matchUnknown
	}
	m.compile()
	unknown := m.unsupported
	if m.path == pathNone || (m.path == pathRegex && m.pathRe.bad) {
		unknown = true
	} else if !m.matchPath(v.req.Path) {
		return matchNo
	}
	rest, miss := m.evalHeadersAndQuery(v)
	if miss {
		return matchNo
	}
	if unknown || rest {
		return matchUnknown
	}
	return matchYes
}

// evalHeadersAndQuery combines evalHeaders and evalQuery.
func (m *routeMatch) evalHeadersAndQuery(v *requestView) (unknown, miss bool) {
	hu, miss := m.evalHeaders(v)
	if miss {
		return false, true
	}
	qu, miss := m.evalQuery(v)
	return hu || qu, miss
}

// evalHeaders reports whether a header matcher could not be evaluated, and
// whether one definitely missed.
func (m *routeMatch) evalHeaders(v *requestView) (unknown, miss bool) {
	for i := range m.headers {
		h := &m.headers[i]
		if (h.name == ":path" && v.hasQuery) || (h.kind == headerString && h.str.bad) {
			unknown = true // a definite miss elsewhere still wins
			continue
		}
		if h.name == ":authority" && v.req.AnyHost {
			continue // a wildcard host fits any :authority, inverted or not
		}
		if !h.match(v) {
			return unknown, true
		}
	}
	return unknown, false
}

// evalQuery is evalHeaders for the query parameter matchers.
func (m *routeMatch) evalQuery(v *requestView) (unknown, miss bool) {
	for i := range m.query {
		q := &m.query[i]
		if q.str != nil && q.str.bad {
			unknown = true
			continue
		}
		if !q.match(v) {
			return unknown, true
		}
	}
	return unknown, false
}

// matchPath matches the path without its query string.
func (m *routeMatch) matchPath(path string) bool {
	switch m.path {
	case pathExact:
		return equal(path, m.pathValue, !m.caseSensitive)
	case pathPrefix:
		return hasPrefix(path, m.pathValue, !m.caseSensitive)
	case pathSeparated:
		if !hasPrefix(path, m.pathValue, !m.caseSensitive) {
			return false
		}
		return len(path) == len(m.pathValue) || path[len(m.pathValue)] == '/'
	case pathRegex:
		return m.pathRe.match(path)
	}
	return false
}

func (h *headerMatcher) match(v *requestView) bool {
	val, ok := v.header(h.name)
	if h.kind == headerPresent {
		// Presence is checked directly; treat_missing_header_as_empty does
		// not apply.
		return (ok == h.present) != h.invert
	}
	if !ok && !h.missingAsEmpty {
		// An absent header fails every other matcher, and invert_match does
		// not rescue it.
		return false
	}
	var got bool
	switch h.kind {
	case headerRange:
		n, err := strconv.ParseInt(val, 10, 64)
		got = err == nil && n >= h.start && n < h.end
	default:
		got = h.str.match(val)
	}
	return got != h.invert
}

func (q *queryMatcher) match(v *requestView) bool {
	vals, ok := v.req.Query[q.name]
	if !ok {
		return false
	}
	if q.str == nil {
		return true
	}
	first := ""
	if len(vals) > 0 {
		first = vals[0]
	}
	return q.str.match(first)
}

func (s *strMatcher) match(v string) bool {
	switch s.kind {
	case strExact:
		return equal(v, s.value, s.ignoreCase)
	case strPrefix:
		return hasPrefix(v, s.value, s.ignoreCase)
	case strSuffix:
		return len(v) >= len(s.value) && equal(v[len(v)-len(s.value):], s.value, s.ignoreCase)
	case strContains:
		if s.ignoreCase {
			return strings.Contains(strings.ToLower(v), s.lower)
		}
		return strings.Contains(v, s.value)
	case strRegex:
		return s.re != nil && s.re.MatchString(v)
	}
	return false
}

func equal(a, b string, fold bool) bool {
	if fold {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func hasPrefix(s, prefix string, fold bool) bool {
	return len(s) >= len(prefix) && equal(s[:len(prefix)], prefix, fold)
}
