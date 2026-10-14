package snapshot

import (
	"sync"
	"sync/atomic"
	"time"

	"sigs.k8s.io/yaml"
)

// ConfigInfo is one item's config. JSON is converted to YAML lazily, on first
// Render, and cached for every copy of this value; Fetch never converts, so a
// poll over thousands of listeners costs nothing until one is viewed.
type ConfigInfo struct {
	YAML        string // pre-rendered text; wins over JSON when set
	JSON        []byte // raw JSON as Envoy emitted it
	VersionInfo string
	LastUpdated time.Time
	Static      bool
	// Err is set at fetch time only (config missing or its dump failed). A
	// problem converting the JSON surfaces later, through Render.
	Err string

	lazy *lazyYAML
}

type lazyYAML struct {
	once sync.Once
	done atomic.Bool
	text string
	err  error
}

func newConfigInfo(json []byte) ConfigInfo {
	return ConfigInfo{JSON: json, lazy: &lazyYAML{}}
}

// Render returns the YAML text, or the reason there is none.
func (c ConfigInfo) Render() (text, problem string) {
	switch {
	case c.Err != "":
		return "", c.Err
	case c.YAML != "":
		return c.YAML, ""
	case len(c.JSON) == 0 || string(c.JSON) == "null":
		return "", ""
	}
	lz := c.lazy
	if lz == nil {
		lz = &lazyYAML{} // built without newConfigInfo: convert, uncached
	}
	lz.once.Do(func() {
		y, err := yaml.JSONToYAML(c.JSON)
		lz.text, lz.err = string(y), err
		lz.done.Store(true)
	})
	if lz.err != nil {
		return "", "config unavailable: render yaml: " + lz.err.Error()
	}
	return lz.text, ""
}

// rendered reports whether the conversion has run (a test hook).
func (c ConfigInfo) rendered() bool { return c.lazy != nil && c.lazy.done.Load() }
