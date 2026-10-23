package snapshot

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestParseCurl(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    Request
		wantErr bool
	}{
		{
			name: "host header wins over url host",
			line: `-H 'Host: a.example.com' https://b.example.com:8443/x/y?a=1&a=2&b=`,
			want: Request{
				Method: "GET", Host: "a.example.com", Port: "", Path: "/x/y",
				Query:   url.Values{"a": {"1", "2"}, "b": {""}},
				Headers: map[string][]string{"host": {"a.example.com"}},
			},
		},
		{
			name: "host header with port",
			line: `-H 'Host: a.example.com:8080' http://b/x`,
			want: Request{
				Method: "GET", Host: "a.example.com", Port: "8080", Path: "/x",
				Query:   url.Values{},
				Headers: map[string][]string{"host": {"a.example.com:8080"}},
			},
		},
		{
			name: "non-default port kept, empty path",
			line: `https://h:8443`,
			want: Request{Method: "GET", Host: "h", Port: "8443", Path: "/", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "https default port dropped",
			line: `https://h:443/p`,
			want: Request{Method: "GET", Host: "h", Port: "", Path: "/p", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "http default port dropped",
			line: `http://h:80`,
			want: Request{Method: "GET", Host: "h", Port: "", Path: "/", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "no scheme",
			line: `example.com`,
			want: Request{Method: "GET", Host: "example.com", Port: "", Path: "/", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "no scheme with port and path",
			line: `example.com:8080/a`,
			want: Request{Method: "GET", Host: "example.com", Port: "8080", Path: "/a", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "repeated headers, url between them",
			line: `--header "X-A: b" http://h/p -H 'X-A: c'`,
			want: Request{
				Method: "GET", Host: "h", Path: "/p", Query: url.Values{},
				Headers: map[string][]string{"x-a": {"b", "c"}},
			},
		},
		{
			name: "equals form and attached short value",
			line: `--header=X-B:v -HX-C:w http://h/p`,
			want: Request{
				Method: "GET", Host: "h", Path: "/p", Query: url.Values{},
				Headers: map[string][]string{"x-b": {"v"}, "x-c": {"w"}},
			},
		},
		{
			name: "double quote escapes and backslash outside quotes",
			line: `-H "X-Q: a \"b\" c" http://h/a\ b`,
			want: Request{
				Method: "GET", Host: "h", Path: "/a b", Query: url.Values{},
				Headers: map[string][]string{"x-q": {`a "b" c`}},
			},
		},
		{
			name: "leading curl word dropped",
			line: `curl 'http://h/p'`,
			want: Request{Method: "GET", Host: "h", Path: "/p", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "pasted curl with headers",
			line: `curl 'https://h/api?x=1' -H 'accept: */*' -H 'cookie: sid=1'`,
			want: Request{
				Method: "GET", Host: "h", Path: "/api", Query: url.Values{"x": {"1"}},
				Headers: map[string][]string{"accept": {"*/*"}, "cookie": {"sid=1"}},
			},
		},
		{
			name: "ipv6 with port",
			line: `http://[::1]:8080/p`,
			want: Request{Method: "GET", Host: "::1", Port: "8080", Path: "/p", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "header value with colons",
			line: `-H 'X: a:b' http://h/p`,
			want: Request{Method: "GET", Host: "h", Path: "/p", Query: url.Values{}, Headers: map[string][]string{"x": {"a:b"}}},
		},
		{
			name: "fragment ignored",
			line: `http://h/p?a=1#frag`,
			want: Request{Method: "GET", Host: "h", Path: "/p", Query: url.Values{"a": {"1"}}, Headers: map[string][]string{}},
		},
		{
			name: "userinfo ignored",
			line: `http://u:pw@h:8080/p`,
			want: Request{Method: "GET", Host: "h", Port: "8080", Path: "/p", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "trailing backslash",
			line: `http://h/p \`,
			want: Request{Method: "GET", Host: "h", Path: "/p", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "continuation pasted as one line",
			line: `curl \  'http://h/p' \  -H 'a: b'`,
			want: Request{Method: "GET", Host: "h", Path: "/p", Query: url.Values{}, Headers: map[string][]string{"a": {"b"}}},
		},
		{
			name: "uppercase scheme",
			line: `HTTPS://h:443/p`,
			want: Request{Method: "GET", Host: "h", Path: "/p", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "no scheme, url in query",
			line: `h/p?next=http://x`,
			want: Request{Method: "GET", Host: "h", Path: "/p", Query: url.Values{"next": {"http://x"}}, Headers: map[string][]string{}},
		},
		{
			name: "hostless path",
			line: `/v1/logon`,
			want: Request{Method: "GET", AnyHost: true, Path: "/v1/logon", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "hostless path with query",
			line: `/v1/logon?a=1&a=2`,
			want: Request{Method: "GET", AnyHost: true, Path: "/v1/logon", Query: url.Values{"a": {"1", "2"}}, Headers: map[string][]string{}},
		},
		{
			name: "hostless root",
			line: `curl /`,
			want: Request{Method: "GET", AnyHost: true, Path: "/", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "hostless with other headers",
			line: `-H 'X-Tenant: acme' /v1/logon`,
			want: Request{
				Method: "GET", AnyHost: true, Path: "/v1/logon", Query: url.Values{},
				Headers: map[string][]string{"x-tenant": {"acme"}},
			},
		},
		{
			name: "hostless double slash is a path",
			line: `//h/p`,
			want: Request{Method: "GET", AnyHost: true, Path: "//h/p", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "path with host header is the host form",
			line: `-H 'Host: x.example.com:8443' /v1/logon?a=1`,
			want: Request{
				Method: "GET", Host: "x.example.com", Port: "8443", Path: "/v1/logon", Query: url.Values{"a": {"1"}},
				Headers: map[string][]string{"host": {"x.example.com:8443"}},
			},
		},
		{
			name: "host without scheme stays the host form",
			line: `example.com/x`,
			want: Request{Method: "GET", Host: "example.com", Path: "/x", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "method 0",
			line: `-X POST /p`,
			want: Request{Method: "POST", AnyHost: true, Path: "/p", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "method 1",
			line: `-XDELETE http://h/p`,
			want: Request{Method: "DELETE", Host: "h", Path: "/p", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "method 2",
			line: `--request PUT http://h/p`,
			want: Request{Method: "PUT", Host: "h", Path: "/p", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "method 3",
			line: `--request=PATCH http://h/p`,
			want: Request{Method: "PATCH", Host: "h", Path: "/p", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "method 4",
			line: `-X GET -X POST http://h/p`,
			want: Request{Method: "POST", Host: "h", Path: "/p", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "method 5",
			line: `-X post http://h/p`,
			want: Request{Method: "post", Host: "h", Path: "/p", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "method 6",
			line: `http://h/p -X POST`,
			want: Request{Method: "POST", Host: "h", Path: "/p", Query: url.Values{}, Headers: map[string][]string{}},
		},
		{
			name: "method with header, either order",
			line: `-H 'a: b' -X POST http://h/p`,
			want: Request{Method: "POST", Host: "h", Path: "/p", Query: url.Values{}, Headers: map[string][]string{"a": {"b"}}},
		},
		{name: "method without value", line: `http://h/p -X`, wantErr: true},
		{name: "empty method", line: `-X '' http://h/p`, wantErr: true},
		{name: "request without value", line: `http://h/p --request`, wantErr: true},
		{name: "empty host header with path", line: `-H 'Host:' /p`, wantErr: true},
		{name: "empty", line: ``, wantErr: true},
		{name: "blank", line: `   `, wantErr: true},
		{name: "header without value", line: `-H`, wantErr: true},
		{name: "unbalanced quote", line: `'http://h`, wantErr: true},
		{name: "header without colon", line: `-H 'noColon' http://h`, wantErr: true},
		{name: "no url", line: `-H 'a: b'`, wantErr: true},
		{name: "two urls", line: `http://h/a http://h/b`, wantErr: true},
		{name: "bad url", line: `http://[::1`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCurl(tc.line)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

// A bad URL is reported as typed, not with the hostless placeholder.
func TestParseCurlBadURLText(t *testing.T) {
	for _, line := range []string{`/%zz`, `/a%zz`} {
		_, err := ParseCurl(line)
		if err == nil {
			t.Fatalf("%s: no error", line)
		}
		if msg := err.Error(); strings.Contains(msg, "http://any") || !strings.Contains(msg, "%zz") {
			t.Errorf("%s: error = %q", line, msg)
		}
	}
}

// Every option but -H/--header is rejected, naming the token as typed.
func TestParseCurlUnsupportedOption(t *testing.T) {
	for _, tc := range []struct{ line, tok string }{
		{`-d x http://h/p`, "-d"},
		{`-sS http://h/p`, "-sS"},
		{`-sX POST http://h/p`, "-sX"},
		{`-sXPOST http://h/p`, "-sXPOST"},
		{`--url /p`, "--url"},
		{`--data-raw x http://h/p`, "--data-raw"},
		{`-- http://h/p`, "--"},
		{`- http://h/p`, "-"},
		{`http://h/p -v`, "-v"},
	} {
		_, err := ParseCurl(tc.line)
		want := fmt.Sprintf("unsupported option %q: :curl takes only -H, -X and a URL", tc.tok)
		if err == nil || err.Error() != want {
			t.Errorf("%s: err = %v, want %q", tc.line, err, want)
		}
	}
}

func TestParseCurlMoreThanOneURL(t *testing.T) {
	_, err := ParseCurl(`http://h/a /b`)
	if err == nil || err.Error() != "more than one URL" {
		t.Fatalf("err = %v", err)
	}
}
