package demo

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Fault injects a failure into every request until cleared with Inject(Fault{}).
type Fault struct {
	Status  int           // respond with this HTTP status
	Delay   time.Duration // wait before responding
	Garbage bool          // respond 200 with invalid JSON
	Match   string        // fault only requests whose RequestURI contains this; empty means all
}

type Server struct {
	mu    sync.Mutex
	sc    Scenario
	fault Fault
}

func NewServer(sc Scenario) *Server { return &Server{sc: sc} }

func (s *Server) SetScenario(sc Scenario) {
	s.mu.Lock()
	s.sc = sc
	s.mu.Unlock()
}

// Update applies fn to a deep copy of the current scenario and installs it.
func (s *Server) Update(fn func(*Scenario)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := s.sc
	cp.Clusters = make([]Cluster, len(s.sc.Clusters))
	for i, c := range s.sc.Clusters {
		c.Hosts = append([]Host(nil), c.Hosts...)
		for j, h := range c.Hosts {
			if h.Health != nil {
				m := make(map[string]any, len(h.Health))
				for k, v := range h.Health {
					m[k] = v
				}
				c.Hosts[j].Health = m
			}
		}
		cp.Clusters[i] = c
	}
	cp.Listeners = make([]Listener, len(s.sc.Listeners))
	for i, l := range s.sc.Listeners {
		l.Chains = make([]Chain, len(s.sc.Listeners[i].Chains))
		for j, c := range s.sc.Listeners[i].Chains {
			l.Chains[j] = c.clone()
		}
		if l.Default != nil {
			d := l.Default.clone()
			l.Default = &d
		}
		cp.Listeners[i] = l
	}
	fn(&cp)
	s.sc = cp
}

// clone deep-copies a chain, including its match map and string slices.
func (c Chain) clone() Chain {
	c.Filters = append([]string(nil), c.Filters...)
	c.HTTPFilters = append([]string(nil), c.HTTPFilters...)
	if c.Match != nil {
		m := make(map[string]any, len(c.Match))
		for k, v := range c.Match {
			if ss, ok := v.([]string); ok {
				v = append([]string(nil), ss...)
			}
			m[k] = v
		}
		c.Match = m
	}
	return c
}

func (s *Server) Inject(f Fault) {
	s.mu.Lock()
	s.fault = f
	s.mu.Unlock()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sc, f := s.sc, s.fault
	s.mu.Unlock()
	if f.Match != "" && !strings.Contains(r.URL.RequestURI(), f.Match) {
		f = Fault{}
	}

	if f.Delay > 0 {
		select {
		case <-time.After(f.Delay):
		case <-r.Context().Done():
			return
		}
	}
	if f.Status != 0 {
		http.Error(w, "injected fault", f.Status)
		return
	}
	if f.Garbage {
		_, _ = w.Write([]byte("{not json"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/clusters":
		if r.URL.Query().Get("format") != "json" {
			http.Error(w, "format=json required", http.StatusBadRequest)
			return
		}
		_, _ = w.Write(sc.ClustersJSON())
	case "/config_dump":
		b, ok := sc.ConfigDumpJSON(r.URL.Query().Get("resource"))
		if !ok {
			http.Error(w, "unsupported resource", http.StatusBadRequest)
			return
		}
		_, _ = w.Write(b)
	case "/server_info":
		_, _ = w.Write(sc.ServerInfoJSON())
	default:
		http.NotFound(w, r)
	}
}

// Start serves on a random loopback port and returns its base URL and a stop func.
func (s *Server) Start() (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	srv := &http.Server{Handler: s}
	go func() { _ = srv.Serve(ln) }()
	return "http://" + ln.Addr().String(), func() { _ = srv.Close() }, nil
}

// Jitter flips the first host of the "search" cluster between healthy and
// failing health checks every interval, so --demo shows live changes.
func (s *Server) Jitter(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			failing = !failing
			s.Update(func(sc *Scenario) {
				for i := range sc.Clusters {
					if sc.Clusters[i].Name != "search" || len(sc.Clusters[i].Hosts) == 0 {
						continue
					}
					if failing {
						sc.Clusters[i].Hosts[0].Health = map[string]any{"failed_active_health_check": true, "eds_health_status": "UNHEALTHY"}
					} else {
						sc.Clusters[i].Hosts[0].Health = nil
					}
				}
			})
		}
	}
}
