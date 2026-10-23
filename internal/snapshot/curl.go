package snapshot

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// Request is the HTTP request a curl-style line describes, reduced to what
// route matching needs. Host and Port are the effective :authority pieces:
// a Host header overrides the URL host, and the port is empty when it is the
// scheme's default. Header names are lower-cased.
//
// AnyHost marks the hostless form (a path-only URL and no Host header): the
// host is a wildcard, so Host and Port are empty and route matching neither
// picks a virtual host by domain nor claims a winner.
type Request struct {
	Query   url.Values
	Headers map[string][]string
	Method  string
	Host    string
	Port    string
	Path    string
	AnyHost bool
}

// schemeRE detects a URL that starts with a scheme.
var schemeRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// ParseCurl parses a curl-style line; a leading "curl" word is dropped. Only
// -H/--header (repeatable), -X/--request (the last wins, used as typed) and one
// URL are understood: any other token that starts with "-" is an error, so an
// option that would change the answer is never silently ignored. The method
// defaults to GET.
func ParseCurl(line string) (Request, error) {
	args, err := splitShell(line)
	if err != nil {
		return Request{}, err
	}
	if len(args) > 0 && args[0] == "curl" {
		args = args[1:]
	}
	if len(args) == 0 {
		return Request{}, errors.New("empty curl line")
	}
	rawURL, method, headers, err := parseCurlArgs(args)
	if err != nil {
		return Request{}, err
	}
	if rawURL == "" {
		return Request{}, errors.New("no URL in curl line")
	}
	return buildRequest(rawURL, method, headers)
}

// parseCurlArgs walks the words: flags give the method and headers, the one
// non-flag word is the URL.
func parseCurlArgs(args []string) (rawURL, method string, headers map[string][]string, err error) {
	method = "GET"
	headers = map[string][]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			if rawURL, err = setCurlURL(rawURL, a); err != nil {
				return "", "", nil, err
			}
			continue
		}

		value, isMethod, next, err := curlFlag(args, i)
		if err != nil {
			return "", "", nil, err
		}
		i = next
		if isMethod {
			if value == "" {
				return "", "", nil, fmt.Errorf("curl flag %s needs a value", a)
			}
			method = value
			continue
		}
		name, val, err := splitHeader(value)
		if err != nil {
			return "", "", nil, err
		}
		headers[name] = append(headers[name], val)
	}
	return rawURL, method, headers, nil
}

// splitHeader splits "Name: value" into a lower-cased name and a trimmed value.
func splitHeader(value string) (name, val string, err error) {
	name, val, ok := strings.Cut(value, ":")
	name = strings.ToLower(strings.TrimSpace(name))
	if !ok || name == "" {
		return "", "", fmt.Errorf("bad header %q: want Name: value", value)
	}
	return name, strings.TrimSpace(val), nil
}

// setCurlURL takes the non-flag word a as the URL; a blank word or a lone
// backslash is a pasted line continuation and keeps rawURL.
func setCurlURL(rawURL, a string) (string, error) {
	if t := strings.TrimSpace(a); t == "" || t == "\\" {
		return rawURL, nil
	}
	if rawURL != "" {
		return "", errors.New("more than one URL")
	}
	return a, nil
}

// curlFlag reads the flag at args[i] and its value; next is the index of the
// last word consumed.
func curlFlag(args []string, i int) (value string, isMethod bool, next int, err error) {
	a := args[i]
	switch {
	case a == "-X" || a == "--request":
		value, next, err = curlFlagValue(args, i)
		return value, true, next, err
	case strings.HasPrefix(a, "--request="):
		return strings.TrimPrefix(a, "--request="), true, i, nil
	case strings.HasPrefix(a, "-X"):
		return a[2:], true, i, nil
	case a == "-H" || a == "--header":
		value, next, err = curlFlagValue(args, i)
		return value, false, next, err
	case strings.HasPrefix(a, "--header="):
		return strings.TrimPrefix(a, "--header="), false, i, nil
	case strings.HasPrefix(a, "-H") && !strings.HasPrefix(a, "--"):
		return a[2:], false, i, nil
	}
	return "", false, i, fmt.Errorf("unsupported option %q: :curl takes only -H, -X and a URL", a)
}

// curlFlagValue takes the word after the flag at args[i] as its value.
func curlFlagValue(args []string, i int) (value string, next int, err error) {
	if i+1 >= len(args) {
		return "", i, fmt.Errorf("curl flag %s needs a value", args[i])
	}
	return args[i+1], i + 1, nil
}

// buildRequest turns the URL, method and headers into a Request.
func buildRequest(rawURL, method string, headers map[string][]string) (Request, error) {
	hv := headers["host"]
	u, anyHost, err := parseCurlURL(rawURL, len(hv) == 0)
	if err != nil {
		return Request{}, err
	}

	host, port := u.Hostname(), u.Port()
	if anyHost {
		host, port = "", ""
	}
	if len(hv) > 0 {
		host, port = splitHostPort(hv[0])
	}
	if host == "" && !anyHost {
		return Request{}, errors.New("URL has no host")
	}
	switch {
	case port == "80" && strings.EqualFold(u.Scheme, "http"),
		port == "443" && strings.EqualFold(u.Scheme, "https"):
		port = ""
	}

	path := u.Path
	if path == "" {
		path = "/"
	}
	return Request{
		Method:  method,
		Host:    host,
		Port:    port,
		AnyHost: anyHost,
		Path:    path,
		Query:   u.Query(),
		Headers: headers,
	}, nil
}

// parseCurlURL parses the typed URL. Hostless form: a path with no Host
// header (noHostHeader); the host is then a wildcard (anyHost).
func parseCurlURL(typed string, noHostHeader bool) (u *url.URL, anyHost bool, err error) {
	rawURL := typed
	anyHost = strings.HasPrefix(rawURL, "/") && noHostHeader
	switch {
	case anyHost:
		// A placeholder authority keeps "//x" a path, not a host.
		rawURL = "http://any" + rawURL
	case !schemeRE.MatchString(rawURL):
		rawURL = "http://" + rawURL
	}
	u, err = url.Parse(rawURL)
	if err != nil {
		if anyHost {
			// Report the path as typed, not the placeholder authority.
			return nil, false, fmt.Errorf("bad URL %q: %w", typed, errors.Unwrap(err))
		}
		return nil, false, fmt.Errorf("bad URL: %w", err)
	}
	return u, anyHost, nil
}

// splitHostPort splits a Host header value; a value without a port is all host.
func splitHostPort(v string) (host, port string) {
	if h, p, err := net.SplitHostPort(v); err == nil {
		return h, p
	}
	return strings.Trim(v, "[]"), ""
}

// splitShell splits a line into words the way a POSIX shell does for quotes
// and backslashes: single quotes are literal, double quotes honour \" \\ \$
// and \`, a bare backslash escapes the next rune. No expansion is performed.
func splitShell(s string) ([]string, error) {
	t := shellSplitter{runes: []rune(s)}
	for t.i = 0; t.i < len(t.runes); t.i++ {
		if err := t.step(); err != nil {
			return nil, err
		}
	}
	t.flush()
	return t.out, nil
}

// shellSplitter is the state of splitShell: i is the current rune.
type shellSplitter struct {
	cur    strings.Builder
	out    []string
	runes  []rune
	i      int
	inWord bool
}

func (t *shellSplitter) flush() {
	if t.inWord {
		t.out = append(t.out, t.cur.String())
		t.cur.Reset()
		t.inWord = false
	}
}

// step consumes the rune at i and whatever it quotes or escapes.
func (t *shellSplitter) step() error {
	r := t.runes[t.i]
	switch r {
	case ' ', '\t', '\n', '\r':
		t.flush()
	case '\\':
		t.inWord = true
		t.escape()
	case '\'':
		t.inWord = true
		return t.quoted('\'', "unbalanced single quote", false)
	case '"':
		t.inWord = true
		return t.quoted('"', "unbalanced double quote", true)
	default:
		t.inWord = true
		t.cur.WriteRune(r)
	}
	return nil
}

// escape handles a backslash outside quotes.
func (t *shellSplitter) escape() {
	if t.i+1 >= len(t.runes) {
		t.cur.WriteRune('\\')
		return
	}
	t.i++
	if t.runes[t.i] == '\n' { // line continuation
		return
	}
	t.cur.WriteRune(t.runes[t.i])
}

// quoted reads up to the closing quote; in double quotes a backslash escapes
// \" \\ $ and `.
func (t *shellSplitter) quoted(quote rune, unbalanced string, escapes bool) error {
	t.i++
	for ; t.i < len(t.runes) && t.runes[t.i] != quote; t.i++ {
		if escapes && t.runes[t.i] == '\\' && t.i+1 < len(t.runes) && strings.ContainsRune("\"\\$`", t.runes[t.i+1]) {
			t.i++
		}
		t.cur.WriteRune(t.runes[t.i])
	}
	if t.i >= len(t.runes) {
		return errors.New(unbalanced)
	}
	return nil
}
