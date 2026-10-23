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
	Match   string
	Status  int
	Delay   time.Duration
	Garbage bool
}

type Server struct {
	sc    Scenario
	fault Fault
	mu    sync.Mutex
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
	cp.Clusters = cloneClusters(s.sc.Clusters)
	cp.Listeners = cloneListeners(s.sc.Listeners)
	cp.RouteConfigs = append([]RouteConfig(nil), s.sc.RouteConfigs...)
	cp.Secrets = cloneSecrets(s.sc.Secrets)
	fn(&cp)
	s.sc = cp
}

func cloneClusters(in []Cluster) []Cluster {
	out := make([]Cluster, len(in))
	for i, c := range in {
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
		c.Secrets = append([]string(nil), c.Secrets...)
		out[i] = c
	}
	return out
}

func cloneListeners(in []Listener) []Listener {
	out := make([]Listener, len(in))
	for i, l := range in {
		l.Chains = make([]Chain, len(in[i].Chains))
		for j, c := range in[i].Chains {
			l.Chains[j] = c.clone()
		}
		if l.Default != nil {
			d := l.Default.clone()
			l.Default = &d
		}
		l.Secrets = append([]string(nil), l.Secrets...)
		out[i] = l
	}
	return out
}

func cloneSecrets(in []Secret) []Secret {
	out := make([]Secret, len(in))
	for i, sec := range in {
		if sec.Cert != nil {
			c := *sec.Cert
			c.SANs = append([]string(nil), c.SANs...)
			sec.Cert = &c
		}
		out[i] = sec
	}
	return out
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
	if !applyFault(w, r, f) {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/clusters":
		if r.URL.Query().Get("format") != "json" {
			http.Error(w, "format=json required", http.StatusBadRequest)
			return
		}
		writeBody(w, sc.ClustersJSON())
	case "/config_dump":
		serveConfigDump(w, r, sc)
	case "/server_info":
		writeBody(w, sc.ServerInfoJSON())
	default:
		http.NotFound(w, r)
	}
}

// applyFault reports whether the request should proceed past the injected fault.
func applyFault(w http.ResponseWriter, r *http.Request, f Fault) bool {
	if f.Delay > 0 {
		select {
		case <-time.After(f.Delay):
		case <-r.Context().Done():
			return false
		}
	}
	if f.Status != 0 {
		http.Error(w, "injected fault", f.Status)
		return false
	}
	if f.Garbage {
		writeBody(w, []byte("{not json"))
		return false
	}
	return true
}

func serveConfigDump(w http.ResponseWriter, r *http.Request, sc Scenario) {
	q := r.URL.Query()
	var b []byte
	var ok bool
	if re := q.Get("name_regex"); re != "" {
		b, ok = sc.RouteConfigsJSON(q.Get("resource"), re)
	} else {
		b, ok = sc.ConfigDumpJSON(q.Get("resource"))
	}
	if !ok {
		http.Error(w, "unsupported resource", http.StatusBadRequest)
		return
	}
	writeBody(w, b)
}

// Start serves on a random loopback port and returns its base URL and a stop func.
func (s *Server) Start() (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	srv := &http.Server{Handler: s}
	go srv.Serve(ln)                                                   //nolint:errcheck // only ever returns http.ErrServerClosed after stop
	return "http://" + ln.Addr().String(), func() { srv.Close() }, nil //nolint:errcheck // best-effort shutdown of a demo server
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
			s.Update(func(sc *Scenario) { setSearchHealth(sc, failing) })
		}
	}
}

// setSearchHealth marks the first host of the "search" cluster failing or healthy.
func setSearchHealth(sc *Scenario, failing bool) {
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
}

// writeBody sends b; a failed write means the client left, so there is nobody to tell.
func writeBody(w http.ResponseWriter, b []byte) {
	w.Write(b) //nolint:errcheck // client disconnected
}
