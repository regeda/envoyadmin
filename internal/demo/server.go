package demo

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Fault injects a failure into every request until cleared with Inject(Fault{}).
type Fault struct {
	Status  int           // respond with this HTTP status
	Delay   time.Duration // wait before responding
	Garbage bool          // respond 200 with invalid JSON
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
	fn(&cp)
	s.sc = cp
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
		w.Write([]byte("{not json"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/clusters":
		if r.URL.Query().Get("format") != "json" {
			http.Error(w, "format=json required", http.StatusBadRequest)
			return
		}
		w.Write(sc.ClustersJSON())
	case "/config_dump":
		b, ok := sc.ConfigDumpJSON(r.URL.Query().Get("resource"))
		if !ok {
			http.Error(w, "unsupported resource", http.StatusBadRequest)
			return
		}
		w.Write(b)
	case "/server_info":
		w.Write(sc.ServerInfoJSON())
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
	go srv.Serve(ln)
	return "http://" + ln.Addr().String(), func() { srv.Close() }, nil
}
