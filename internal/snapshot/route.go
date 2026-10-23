package snapshot

import (
	"encoding/json"
	"strconv"
	"strings"
)

// RouteRef is one HttpConnectionManager's route source: Name from
// rds.route_config_name, or Inline from route_config.
type RouteRef struct {
	Name   string
	Inline json.RawMessage
}

// RouteRefs are the chains' route sources in chain order, RDS names
// de-duplicated.
func (l Listener) RouteRefs() []RouteRef {
	var refs []RouteRef
	seen := map[string]bool{}
	for _, c := range l.Chains {
		r := c.Routes
		if r == nil {
			continue
		}
		if r.Name != "" {
			if seen[r.Name] {
				continue
			}
			seen[r.Name] = true
		}
		refs = append(refs, *r)
	}
	return refs
}

// HasHTTP reports whether any chain has an HttpConnectionManager.
func (l Listener) HasHTTP() bool {
	for _, c := range l.Chains {
		if c.hcm {
			return true
		}
	}
	return false
}

// HasScopedRoutes reports whether any chain's HttpConnectionManager uses
// scoped_routes, whose route configs are not read.
func (l Listener) HasScopedRoutes() bool {
	for _, c := range l.Chains {
		if c.scoped {
			return true
		}
	}
	return false
}

type RouteConfig struct {
	Name         string
	VersionInfo  string
	VirtualHosts []VirtualHost
	Inline       bool
}

type VirtualHost struct {
	Name    string
	Domains []string
	Routes  []Route
}

// Route is one route entry; Index is its position in the virtual host.
type Route struct {
	match  *routeMatch
	Name   string
	JSON   []byte
	Config ConfigInfo
	Index  int
}

// ParseRouteConfig reads a route_config object, best effort: malformed parts
// yield fewer virtual hosts or routes, never a panic. An inline config with
// no name is called "inline".
func ParseRouteConfig(name, version string, inline bool, raw json.RawMessage) RouteConfig {
	rc := RouteConfig{Name: name, VersionInfo: version, Inline: inline}
	if inline && rc.Name == "" {
		rc.Name = "inline"
	}
	var c struct {
		VirtualHosts []json.RawMessage `json:"virtual_hosts"`
	}
	looseUnmarshal(raw, &c)
	for _, v := range c.VirtualHosts {
		var vh struct {
			Name    string            `json:"name"`
			Domains []string          `json:"domains"`
			Routes  []json.RawMessage `json:"routes"`
		}
		if json.Unmarshal(v, &vh) != nil {
			continue
		}
		out := VirtualHost{Name: vh.Name, Domains: vh.Domains}
		for i, r := range vh.Routes {
			var n struct {
				Name  string          `json:"name"`
				Match json.RawMessage `json:"match"`
			}
			looseUnmarshal(r, &n)
			out.Routes = append(out.Routes, Route{
				Name: n.Name, Index: i, JSON: r, Config: NewConfigInfo(r),
				match: decodeRouteMatch(n.Match),
			})
		}
		rc.VirtualHosts = append(rc.VirtualHosts, out)
	}
	return rc
}

// Supported keys of a route match, a header matcher, a query parameter
// matcher and a StringMatcher. Any other key makes the route unsupported.
var (
	routeMatchKeys = map[string]bool{
		"prefix": true, "path": true, "safe_regex": true, "path_separated_prefix": true,
		"case_sensitive": true, "headers": true, "query_parameters": true,
	}
	headerMatcherKeys = map[string]bool{
		"name": true, "string_match": true, "exact_match": true, "prefix_match": true,
		"suffix_match": true, "contains_match": true, "safe_regex_match": true,
		"present_match": true, "range_match": true, "invert_match": true,
		"treat_missing_header_as_empty": true,
	}
	queryMatcherKeys = map[string]bool{"name": true, "string_match": true, "present_match": true}
	stringMatchKinds = map[string]strKind{
		"exact": strExact, "prefix": strPrefix, "suffix": strSuffix,
		"contains": strContains, "safe_regex": strRegex,
	}
	// pseudoHeaders are the pseudo-headers a Request can supply.
	pseudoHeaders = map[string]bool{":authority": true, ":path": true, ":method": true}
)

// decodeRouteMatch decodes a route's match object once. Anything outside the
// supported matcher set, or malformed, marks the matcher unsupported, but the
// rest is still decoded so a definite miss by the supported parts is a miss.
func decodeRouteMatch(raw json.RawMessage) *routeMatch {
	m := &routeMatch{caseSensitive: true}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		m.unsupported = true
		return m
	}
	for k := range obj {
		if !routeMatchKeys[k] {
			m.unsupported = true
		}
	}
	m.decodePath(obj)
	if m.path == pathNone {
		m.unsupported = true // the path cannot rule the route out
	}
	var headers, query []json.RawMessage
	if json.Unmarshal(orNull(obj["headers"]), &headers) != nil {
		m.unsupported = true
	}
	if json.Unmarshal(orNull(obj["query_parameters"]), &query) != nil {
		m.unsupported = true
	}
	var headersOK, queryOK bool
	m.headers, headersOK = decodeAll(headers, decodeHeaderMatcher)
	m.query, queryOK = decodeAll(query, decodeQueryMatcher)
	if !headersOK || !queryOK {
		m.unsupported = true
	}
	return m
}

// decodeAll decodes every raw matcher with dec, keeping the decodable ones;
// ok is false when any of them was not decodable.
func decodeAll[T any](raws []json.RawMessage, dec func(json.RawMessage) (T, bool)) (out []T, ok bool) {
	ok = true
	for _, r := range raws {
		if v, valid := dec(r); valid {
			out = append(out, v)
		} else {
			ok = false
		}
	}
	return out, ok
}

var pathKinds = map[string]pathKind{"prefix": pathPrefix, "path": pathExact, "path_separated_prefix": pathSeparated}

// decodePath decodes the path specifier and case_sensitive of a route match;
// anything but exactly one valid specifier leaves path as pathNone.
func (m *routeMatch) decodePath(obj map[string]json.RawMessage) {
	specifiers := 0
	for _, k := range []string{"prefix", "path", "path_separated_prefix", "safe_regex"} {
		v, ok := obj[k]
		if !ok {
			continue
		}
		specifiers++
		m.decodePathSpecifier(k, v)
	}
	if v, ok := obj["case_sensitive"]; ok && json.Unmarshal(v, &m.caseSensitive) != nil {
		m.path = pathNone
	}
	if specifiers != 1 {
		m.path = pathNone
	}
}

func (m *routeMatch) decodePathSpecifier(k string, v json.RawMessage) {
	if k == "safe_regex" {
		if re, ok := decodeRegex(v); ok {
			m.path, m.pathRe = pathRegex, &strMatcher{kind: strRegex, value: re}
		}
		return
	}
	if json.Unmarshal(v, &m.pathValue) == nil {
		m.path = pathKinds[k]
	}
}

func orNull(v json.RawMessage) json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	return v
}

var legacyStringKinds = map[string]strKind{"exact_match": strExact, "prefix_match": strPrefix,
	"suffix_match": strSuffix, "contains_match": strContains}

func decodeHeaderMatcher(raw json.RawMessage) (headerMatcher, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return headerMatcher{}, false
	}
	var h headerMatcher
	if !h.decodeFields(obj) {
		return h, false
	}
	h.name = strings.ToLower(h.name)
	// Envoy keeps Host as :authority, so what a "host" matcher sees is
	// ambiguous; other pseudo-headers (e.g. :scheme) are not in a Request.
	if h.name == "" || h.name == "host" || (strings.HasPrefix(h.name, ":") && !pseudoHeaders[h.name]) {
		return h, false
	}
	return h, h.decodeSpecifiers(obj)
}

// decodeSpecifiers decodes the one match specifier of a header matcher; no
// specifier at all is a presence match, more than one is unsupported.
func (h *headerMatcher) decodeSpecifiers(obj map[string]json.RawMessage) bool {
	specifiers := 0
	for _, k := range []string{"string_match", "exact_match", "prefix_match", "suffix_match",
		"contains_match", "safe_regex_match", "present_match", "range_match"} {
		v, ok := obj[k]
		if !ok {
			continue
		}
		specifiers++
		if !h.decodeSpecifier(k, v) {
			return false
		}
	}
	switch specifiers {
	case 0: // a name alone matches on presence, as in Envoy
		h.kind, h.present = headerPresent, true
	case 1:
	default:
		return false
	}
	return true
}

// decodeFields reads name, invert_match and treat_missing_header_as_empty and
// reports false on an unknown key or a malformed value.
func (h *headerMatcher) decodeFields(obj map[string]json.RawMessage) bool {
	for k, v := range obj {
		if !headerMatcherKeys[k] {
			return false
		}
		var err error
		switch k {
		case "name":
			err = json.Unmarshal(v, &h.name)
		case "invert_match":
			err = json.Unmarshal(v, &h.invert)
		case "treat_missing_header_as_empty":
			err = json.Unmarshal(v, &h.missingAsEmpty)
		}
		if err != nil {
			return false
		}
	}
	return true
}

// decodeSpecifier decodes one match specifier (k) of a header matcher.
func (h *headerMatcher) decodeSpecifier(k string, v json.RawMessage) bool {
	switch k {
	case "string_match":
		h.kind = headerString
		var ok bool
		h.str, ok = decodeStringMatch(v)
		return ok
	case "safe_regex_match":
		h.kind = headerString
		re, ok := decodeRegex(v)
		h.str = strMatcher{kind: strRegex, value: re}
		return ok
	case "present_match":
		h.kind = headerPresent
		return json.Unmarshal(v, &h.present) == nil
	case "range_match":
		h.kind = headerRange
		var r struct{ Start, End json.RawMessage }
		ok := json.Unmarshal(v, &r) == nil
		var okS, okE bool
		h.start, okS = decodeInt64(r.Start)
		h.end, okE = decodeInt64(r.End)
		return ok && okS && okE
	default: // legacy exact/prefix/suffix/contains
		h.kind = headerString
		h.str.kind = legacyStringKinds[k]
		return json.Unmarshal(v, &h.str.value) == nil
	}
}

func decodeQueryMatcher(raw json.RawMessage) (queryMatcher, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return queryMatcher{}, false
	}
	var q queryMatcher
	for k := range obj {
		if !queryMatcherKeys[k] {
			return q, false
		}
	}
	if json.Unmarshal(obj["name"], &q.name) != nil || q.name == "" {
		return q, false
	}
	return q, q.decodeMatch(obj)
}

// decodeMatch decodes string_match or present_match (at most one of them).
func (q *queryMatcher) decodeMatch(obj map[string]json.RawMessage) bool {
	_, hasStr := obj["string_match"]
	_, hasPresent := obj["present_match"]
	switch {
	case hasStr && hasPresent:
		return false
	case hasStr:
		s, ok := decodeStringMatch(obj["string_match"])
		if !ok {
			return false
		}
		q.str = &s
	case hasPresent:
		var p bool
		// present_match false is not meaningful; treat it as unsupported.
		if json.Unmarshal(obj["present_match"], &p) != nil || !p {
			return false
		}
	}
	return true
}

// decodeStringMatch decodes an envoy.type.matcher.v3.StringMatcher.
func decodeStringMatch(raw json.RawMessage) (strMatcher, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return strMatcher{}, false
	}
	var s strMatcher
	n := 0
	for k, v := range obj {
		if k == "ignore_case" {
			if json.Unmarshal(v, &s.ignoreCase) != nil {
				return s, false
			}
			continue
		}
		kind, ok := stringMatchKinds[k]
		if !ok {
			return s, false
		}
		n++
		s.kind = kind
		if !s.decodeValue(v) {
			return s, false
		}
	}
	if n != 1 {
		return s, false
	}
	if s.kind == strRegex {
		s.ignoreCase = false // Envoy ignores ignore_case for safe_regex
	}
	if s.ignoreCase {
		s.lower = strings.ToLower(s.value)
	}
	return s, true
}

// decodeValue reads the value of a string matcher of the already set kind.
func (s *strMatcher) decodeValue(v json.RawMessage) bool {
	if s.kind == strRegex {
		var ok bool
		s.value, ok = decodeRegex(v)
		return ok
	}
	return json.Unmarshal(v, &s.value) == nil
}

// decodeRegex reads a RegexMatcher's regex; engine fields are ignored.
func decodeRegex(raw json.RawMessage) (string, bool) {
	var r struct {
		Regex *string `json:"regex"`
	}
	if json.Unmarshal(raw, &r) != nil || r.Regex == nil {
		return "", false
	}
	return *r.Regex, true
}

// decodeInt64 reads a proto int64, which JSON carries as a string or a number.
func decodeInt64(raw json.RawMessage) (int64, bool) {
	s := strings.Trim(string(raw), `"`)
	if s == "" {
		return 0, true // proto default
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}
