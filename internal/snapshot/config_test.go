package snapshot

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	adminv3 "github.com/envoyproxy/go-control-plane/envoy/admin/v3"
	"github.com/regeda/envoyadmin/internal/admin"
)

func TestRenderConvertsLazilyAndCaches(t *testing.T) {
	c := NewConfigInfo([]byte(`{"name":"api","type":"EDS"}`))
	if c.Rendered() {
		t.Fatal("nothing is converted until Render is called")
	}
	text, problem := c.Render()
	if problem != "" || !strings.Contains(text, "name: api") || !strings.Contains(text, "type: EDS") {
		t.Fatalf("text=%q problem=%q", text, problem)
	}
	if !c.Rendered() {
		t.Fatal("Render must mark the cache")
	}
	again, _ := c.Render()
	if again != text {
		t.Fatal("cached text must be stable")
	}
	// a copy of the struct shares the cache
	cp := c
	if !cp.Rendered() {
		t.Fatal("copies share one cache")
	}
}

func TestRenderPrecedenceAndProblems(t *testing.T) {
	if text, problem := (ConfigInfo{Err: "config unavailable: x", YAML: "a: 1"}).Render(); text != "" || problem != "config unavailable: x" {
		t.Fatalf("Err wins: %q %q", text, problem)
	}
	if text, problem := (ConfigInfo{YAML: "a: 1\n", JSON: []byte(`{"b":2}`)}).Render(); text != "a: 1\n" || problem != "" {
		t.Fatalf("explicit YAML wins over JSON: %q %q", text, problem)
	}
	if text, problem := (ConfigInfo{}).Render(); text != "" || problem != "" {
		t.Fatalf("empty: %q %q", text, problem)
	}
	_, problem := NewConfigInfo([]byte(`{not json`)).Render()
	if !strings.HasPrefix(problem, "config unavailable: render yaml:") {
		t.Fatalf("bad JSON must surface as a problem: %q", problem)
	}
	// a value built without the constructor still renders (uncached)
	if text, _ := (ConfigInfo{JSON: []byte(`{"a":1}`)}).Render(); !strings.Contains(text, "a: 1") {
		t.Fatalf("uncached render: %q", text)
	}
}

func TestRenderIsSafeConcurrently(t *testing.T) {
	c := NewConfigInfo([]byte(`{"a":1}`))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Render(); c.Rendered() }()
	}
	wg.Wait()
}

// Fetch must not convert any config: only a viewed item is ever rendered.
func TestFetchDoesNotConvertYAML(t *testing.T) {
	s := fetchFromDemo(t)
	if len(s.Clusters) == 0 {
		t.Fatal("precondition: clusters")
	}
	for _, c := range s.Clusters {
		if c.Config.Rendered() {
			t.Fatalf("%s was converted during Fetch", c.Name)
		}
		if c.Config.YAML != "" || len(c.Config.JSON) == 0 {
			t.Fatalf("%s: JSON must be kept raw, YAML empty (got YAML %d bytes, JSON %d)", c.Name, len(c.Config.YAML), len(c.Config.JSON))
		}
	}
	text, _ := s.Clusters[0].Config.Render()
	if text == "" || !s.Clusters[0].Config.Rendered() {
		t.Fatal("rendering the selected item converts it")
	}
}

// barrierClient proves the secondary calls run in parallel: each waits until
// all three have started, so a sequential Fetch would deadlock until the timeout.
type barrierClient struct {
	admin.Client
	started chan struct{}
}

func (b barrierClient) wait() {
	b.started <- struct{}{}
	deadline := time.After(2 * time.Second)
	for len(b.started) < 3 {
		select {
		case <-deadline:
			return
		case <-time.After(time.Millisecond):
		}
	}
}

func (b barrierClient) ClusterConfigs(ctx context.Context) ([]admin.ClusterConfig, error) {
	b.wait()
	return b.Client.ClusterConfigs(ctx)
}

func (b barrierClient) ListenerConfigs(ctx context.Context) ([]admin.ListenerConfig, error) {
	b.wait()
	return b.Client.ListenerConfigs(ctx)
}

func (b barrierClient) ServerInfo(ctx context.Context) (*adminv3.ServerInfo, error) {
	b.wait()
	return b.Client.ServerInfo(ctx)
}

func TestFetchRunsSecondaryCallsInParallel(t *testing.T) {
	client := demoClient(t)
	b := barrierClient{Client: client, started: make(chan struct{}, 3)}
	start := time.Now()
	if _, err := Fetch(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) >= 1900*time.Millisecond {
		t.Fatal("ClusterConfigs, ServerInfo and ListenerConfigs were not called concurrently")
	}
}
