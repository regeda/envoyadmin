package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/regeda/txenvoy/internal/snapshot"
)

func manyHosts(name string, n int) snapshot.Cluster {
	c := snapshot.Cluster{Name: name, Type: "EDS"}
	for i := 0; i < n; i++ {
		c.Hosts = append(c.Hosts, snapshot.Host{Address: fmt.Sprintf("10.0.0.%02d:80", i)})
	}
	return c
}

func keysModel() Model {
	m := sized(New(testOpts(nil)), 120, 30)
	return send(m, snapshotMsg{snap: snap(manyHosts("alpha", 60), manyHosts("beta", 3), manyHosts("gamma", 3))})
}

func selectedName(m Model) string {
	c, _ := m.list.Selected()
	return c.Name
}

// Tab used to switch a hidden focus and made j/k look dead. It must now do nothing.
func TestTabIsInert(t *testing.T) {
	m := keysModel()
	before := m.View()
	m2 := send(m, key("tab"))
	if m2.View() != before {
		t.Fatal("tab must not change anything on screen")
	}
	m2 = send(m2, key("j"))
	if selectedName(m2) != "beta" {
		t.Fatalf("j must still select the next cluster after tab, got %q", selectedName(m2))
	}
}

func TestArrowsAndJKAlwaysMoveTheSelection(t *testing.T) {
	m := keysModel()
	for _, step := range []struct{ key, want string }{
		{"j", "beta"}, {"down", "gamma"}, {"k", "beta"}, {"up", "alpha"},
	} {
		m = send(m, key(step.key))
		if got := selectedName(m); got != step.want {
			t.Fatalf("%s: selected %q, want %q", step.key, got, step.want)
		}
	}
}

func TestPgUpPgDnScrollTheDetailsNotTheSelection(t *testing.T) {
	m := keysModel()
	if strings.Contains(m.View(), "10.0.0.59:80") {
		t.Fatal("precondition: the last host starts off-screen")
	}
	m = send(m, tea_pgdown())
	if selectedName(m) != "alpha" {
		t.Fatalf("pgdown moved the selection to %q", selectedName(m))
	}
	if m.detail.scroll == 0 {
		t.Fatal("pgdown must scroll the endpoint table")
	}
	for i := 0; i < 10; i++ {
		m = send(m, tea_pgdown())
	}
	if !strings.Contains(m.View(), "10.0.0.59:80") {
		t.Fatal("repeated pgdown must reach the last host")
	}
	m = send(m, tea_pgup())
	if m.detail.scroll >= maxEndpointScroll(m.list.all[0], m.detail.height-1) {
		t.Fatal("pgup must scroll back up")
	}
	if selectedName(m) != "alpha" {
		t.Fatal("pgup moved the selection")
	}
}

func TestPgDnScrollsTheConfigTabToo(t *testing.T) {
	c := manyHosts("alpha", 2)
	c.Config.YAML = strings.Repeat("key: value\n", 80) + "last: line\n"
	m := sized(New(testOpts(nil)), 120, 30)
	m = send(m, snapshotMsg{snap: snap(c, manyHosts("beta", 1))})
	m = send(m, key("c"))
	if strings.Contains(m.View(), "last: line") {
		t.Fatal("precondition: the end of the YAML is off-screen")
	}
	for i := 0; i < 6; i++ {
		m = send(m, tea_pgdown())
	}
	if !strings.Contains(m.View(), "last: line") {
		t.Fatalf("pgdown must scroll the config viewport:\n%s", m.View())
	}
	if selectedName(m) != "alpha" {
		t.Fatal("pgdown moved the selection")
	}
	m = send(m, key("j"))
	if selectedName(m) != "beta" {
		t.Fatal("j still selects clusters while the config tab is open")
	}
}

func TestEscStillClearsTheFilter(t *testing.T) {
	m := typeText(send(keysModel(), key("/")), "alp")
	m = send(m, key("enter"))
	if len(m.list.visible) != 1 {
		t.Fatalf("filter should narrow to 1, got %d", len(m.list.visible))
	}
	m = send(m, key("esc"))
	if len(m.list.visible) != 3 {
		t.Fatalf("esc must clear the filter, %d visible", len(m.list.visible))
	}
}
