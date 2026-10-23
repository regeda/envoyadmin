// Package admin talks to Envoy's admin interface. It is the only transport
// seam: a port-forward implementation can later satisfy Client unchanged.
package admin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	adminv3 "github.com/envoyproxy/go-control-plane/envoy/admin/v3"
	"google.golang.org/protobuf/encoding/protojson"
)

const maxBody = 64 << 20

type Client interface {
	Clusters(ctx context.Context) (*adminv3.Clusters, error)
	ClusterConfigs(ctx context.Context) ([]ClusterConfig, error)
	ListenerConfigs(ctx context.Context) ([]ListenerConfig, error)
	SecretConfigs(ctx context.Context) ([]SecretConfig, error)
	// RouteConfigs fetches only the named route configs; no names, no request.
	RouteConfigs(ctx context.Context, names []string) ([]RouteConfig, error)
	ServerInfo(ctx context.Context) (*adminv3.ServerInfo, error)
}

// ClusterConfig is one cluster's config as Envoy emitted it.
type ClusterConfig struct {
	LastUpdated time.Time
	Name        string
	Type        string
	VersionInfo string
	JSON        []byte
	Static      bool
}

type HTTP struct {
	base    *url.URL
	hc      *http.Client
	timeout time.Duration
}

func NewHTTP(base string, timeout time.Duration) (*HTTP, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("admin url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("admin url %q needs a scheme and host", base)
	}
	return &HTTP{base: u, hc: &http.Client{}, timeout: timeout}, nil
}

func (h *HTTP) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	u := *h.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	resp, err := h.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body, nothing to recover
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	return body, nil
}

var lenient = protojson.UnmarshalOptions{DiscardUnknown: true}

func (h *HTTP) Clusters(ctx context.Context) (*adminv3.Clusters, error) {
	body, err := h.get(ctx, "/clusters", url.Values{"format": {"json"}})
	if err != nil {
		return nil, err
	}
	out := &adminv3.Clusters{}
	if err := lenient.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("GET /clusters: decode: %w", err)
	}
	return out, nil
}

func (h *HTTP) ServerInfo(ctx context.Context) (*adminv3.ServerInfo, error) {
	body, err := h.get(ctx, "/server_info", nil)
	if err != nil {
		return nil, err
	}
	out := &adminv3.ServerInfo{}
	if err := lenient.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("GET /server_info: decode: %w", err)
	}
	return out, nil
}

func (h *HTTP) ClusterConfigs(ctx context.Context) ([]ClusterConfig, error) {
	var all []ClusterConfig
	for _, res := range []string{"dynamic_active_clusters", "static_clusters"} {
		body, err := h.get(ctx, "/config_dump", url.Values{"resource": {res}})
		if err != nil {
			return nil, err
		}
		cfgs, err := decodeClusterConfigs(body, res == "static_clusters")
		if err != nil {
			return nil, fmt.Errorf("GET /config_dump: decode %s: %w", res, err)
		}
		all = append(all, cfgs...)
	}
	return all, nil
}

func (h *HTTP) ListenerConfigs(ctx context.Context) ([]ListenerConfig, error) {
	var all []ListenerConfig
	for _, res := range []string{"dynamic_listeners", "static_listeners"} {
		body, err := h.get(ctx, "/config_dump", url.Values{"resource": {res}})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", res, err)
		}
		cfgs, err := decodeListenerConfigs(body, res == "static_listeners")
		if err != nil {
			return nil, fmt.Errorf("GET /config_dump: decode %s: %w", res, err)
		}
		all = append(all, cfgs...)
	}
	return all, nil
}

func (h *HTTP) SecretConfigs(ctx context.Context) ([]SecretConfig, error) {
	var all []SecretConfig
	for _, r := range []struct {
		resource string
		state    SecretState
	}{
		{"dynamic_active_secrets", SecretActive},
		{"dynamic_warming_secrets", SecretWarming},
		{"static_secrets", SecretStatic},
	} {
		body, err := h.get(ctx, "/config_dump", url.Values{"resource": {r.resource}})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.resource, err)
		}
		cfgs, err := decodeSecretConfigs(body, r.state)
		if err != nil {
			return nil, fmt.Errorf("GET /config_dump: decode %s: %w", r.resource, err)
		}
		all = append(all, cfgs...)
	}
	return all, nil
}

func (h *HTTP) RouteConfigs(ctx context.Context, names []string) ([]RouteConfig, error) {
	if len(names) == 0 {
		return nil, nil
	}
	re := routeNameRegex(names)
	var all []RouteConfig
	for _, res := range []string{"dynamic_route_configs", "static_route_configs"} {
		body, err := h.get(ctx, "/config_dump", url.Values{"resource": {res}, "name_regex": {re}})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", res, err)
		}
		cfgs, err := decodeRouteConfigs(body, res == "static_route_configs")
		if err != nil {
			return nil, fmt.Errorf("GET /config_dump: decode %s: %w", res, err)
		}
		all = append(all, cfgs...)
	}
	return all, nil
}
