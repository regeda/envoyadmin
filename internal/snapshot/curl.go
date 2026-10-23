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
	Method, Host, Port, Path string
	AnyHost                  bool
	Query                    url.Values
	Headers                  map[string][]string
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

	rawURL, method := "", "GET"
	headers := map[string][]string{}

	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			// A blank word or a lone backslash is a pasted line continuation.
			if t := strings.TrimSpace(a); t == "" || t == "\\" {
				continue
			}
			if rawURL != "" {
				return Request{}, errors.New("more than one URL")
			}
			rawURL = a
			continue
		}

		var value string
		isMethod := false
		switch {
		case a == "-X" || a == "--request":
			if i+1 >= len(args) {
				return Request{}, fmt.Errorf("curl flag %s needs a value", a)
			}
			i++
			value, isMethod = args[i], true
		case strings.HasPrefix(a, "--request="):
			value, isMethod = strings.TrimPrefix(a, "--request="), true
		case strings.HasPrefix(a, "-X"):
			value, isMethod = a[2:], true
		case a == "-H" || a == "--header":
			if i+1 >= len(args) {
				return Request{}, fmt.Errorf("curl flag %s needs a value", a)
			}
			i++
			value = args[i]
		case strings.HasPrefix(a, "--header="):
			value = strings.TrimPrefix(a, "--header=")
		case strings.HasPrefix(a, "-H") && !strings.HasPrefix(a, "--"):
			value = a[2:]
		default:
			return Request{}, fmt.Errorf("unsupported option %q: :curl takes only -H, -X and a URL", a)
		}
		if isMethod {
			if value == "" {
				return Request{}, fmt.Errorf("curl flag %s needs a value", a)
			}
			method = value
			continue
		}
		name, val, ok := strings.Cut(value, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		if !ok || name == "" {
			return Request{}, fmt.Errorf("bad header %q: want Name: value", value)
		}
		headers[name] = append(headers[name], strings.TrimSpace(val))
	}

	if rawURL == "" {
		return Request{}, errors.New("no URL in curl line")
	}
	typed := rawURL
	hv := headers["host"]
	// Hostless form: a path with no Host header; the host is a wildcard.
	anyHost := strings.HasPrefix(rawURL, "/") && len(hv) == 0
	switch {
	case anyHost:
		// A placeholder authority keeps "//x" a path, not a host.
		rawURL = "http://any" + rawURL
	case !schemeRE.MatchString(rawURL):
		rawURL = "http://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		if anyHost {
			// Report the path as typed, not the placeholder authority.
			return Request{}, fmt.Errorf("bad URL %q: %w", typed, errors.Unwrap(err))
		}
		return Request{}, fmt.Errorf("bad URL: %w", err)
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
	var (
		out     []string
		cur     strings.Builder
		inWord  bool
		runes   = []rune(s)
		flushed = func() {
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		}
	)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case ' ', '\t', '\n', '\r':
			flushed()
		case '\\':
			inWord = true
			if i+1 < len(runes) {
				i++
				if runes[i] == '\n' { // line continuation
					continue
				}
				cur.WriteRune(runes[i])
			} else {
				cur.WriteRune('\\')
			}
		case '\'':
			inWord = true
			i++
			for ; i < len(runes) && runes[i] != '\''; i++ {
				cur.WriteRune(runes[i])
			}
			if i >= len(runes) {
				return nil, errors.New("unbalanced single quote")
			}
		case '"':
			inWord = true
			i++
			for ; i < len(runes) && runes[i] != '"'; i++ {
				if runes[i] == '\\' && i+1 < len(runes) && strings.ContainsRune("\"\\$`", runes[i+1]) {
					i++
				}
				cur.WriteRune(runes[i])
			}
			if i >= len(runes) {
				return nil, errors.New("unbalanced double quote")
			}
		default:
			inWord = true
			cur.WriteRune(r)
		}
	}
	flushed()
	return out, nil
}
