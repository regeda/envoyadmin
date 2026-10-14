package admin

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	typeDynamicListener = ".ListenersConfigDump.DynamicListener"
	typeStaticListener  = ".ListenersConfigDump.StaticListener"
	typeListenersDump   = ".ListenersConfigDump"
)

type ListenerState struct {
	VersionInfo string
	LastUpdated time.Time
	Listener    json.RawMessage
}

type ListenerError struct {
	LastUpdateAttempt time.Time
	Details           string
	Listener          json.RawMessage
}

// ListenerConfig is one listener as Envoy emitted it. A static listener has
// Static set and its single state in Active (empty VersionInfo).
type ListenerConfig struct {
	Name                      string
	Static                    bool
	Active, Warming, Draining *ListenerState
	Error                     *ListenerError
}

type rawState struct {
	VersionInfo string          `json:"version_info"`
	LastUpdated string          `json:"last_updated"`
	Listener    json.RawMessage `json:"listener"`
}

type rawDynamic struct {
	Name     string    `json:"name"`
	Active   *rawState `json:"active_state"`
	Warming  *rawState `json:"warming_state"`
	Draining *rawState `json:"draining_state"`
	Error    *struct {
		Failed  json.RawMessage `json:"failed_configuration"`
		Attempt string          `json:"last_update_attempt"`
		Details string          `json:"details"`
	} `json:"error_state"`
}

type rawStatic struct {
	Listener    json.RawMessage `json:"listener"`
	LastUpdated string          `json:"last_updated"`
}

type listenersDump struct {
	Static  []rawStatic  `json:"static_listeners"`
	Dynamic []rawDynamic `json:"dynamic_listeners"`
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func state(r *rawState) *ListenerState {
	if r == nil {
		return nil
	}
	return &ListenerState{VersionInfo: r.VersionInfo, LastUpdated: parseTime(r.LastUpdated), Listener: r.Listener}
}

func dynamicListener(d rawDynamic) (ListenerConfig, bool) {
	name := d.Name
	if name == "" && d.Error != nil {
		// A rejected update also yields a nameless element; the listener's name
		// is then only in the failed configuration.
		var fc struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(d.Error.Failed, &fc)
		name = fc.Name
	}
	lc := ListenerConfig{
		Name: name, Active: state(d.Active), Warming: state(d.Warming), Draining: state(d.Draining),
	}
	if d.Error != nil {
		lc.Error = &ListenerError{LastUpdateAttempt: parseTime(d.Error.Attempt), Details: d.Error.Details, Listener: d.Error.Failed}
	}
	if lc.Name == "" || (lc.Active == nil && lc.Warming == nil && lc.Draining == nil && lc.Error == nil) {
		return ListenerConfig{}, false
	}
	return lc, true
}

func staticListener(s rawStatic) (ListenerConfig, bool) {
	var l struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(s.Listener, &l) != nil || l.Name == "" {
		return ListenerConfig{}, false
	}
	return ListenerConfig{
		Name: l.Name, Static: true,
		Active: &ListenerState{LastUpdated: parseTime(s.LastUpdated), Listener: s.Listener},
	}, true
}

// mergeListener folds b into a: b only fills what a lacks, except that the error
// carrying a last_update_attempt wins over one without.
func mergeListener(a, b ListenerConfig) ListenerConfig {
	if a.Active == nil {
		a.Active = b.Active
	}
	if a.Warming == nil {
		a.Warming = b.Warming
	}
	if a.Draining == nil {
		a.Draining = b.Draining
	}
	switch {
	case a.Error == nil:
		a.Error = b.Error
	case b.Error != nil && a.Error.LastUpdateAttempt.IsZero() && !b.Error.LastUpdateAttempt.IsZero():
		a.Error = b.Error
	}
	return a
}

// decodeListenerConfigs walks /config_dump as raw JSON, like decodeClusterConfigs.
// With ?resource=dynamic_listeners|static_listeners Envoy emits one configs[]
// element per listener, typed ListenersConfigDump.DynamicListener or
// .StaticListener. A plain /config_dump has one wrapped ListenersConfigDump with
// nested arrays; that shape is accepted too, picking the list named by static.
// Anything unrecognised or malformed is skipped, never fatal.
func decodeListenerConfigs(body []byte, static bool) ([]ListenerConfig, error) {
	var dump struct {
		Configs []json.RawMessage `json:"configs"`
	}
	if err := json.Unmarshal(body, &dump); err != nil {
		return nil, err
	}
	var out []ListenerConfig
	idx := map[string]int{}
	add := func(lc ListenerConfig) { // elements for the same listener are merged
		if i, ok := idx[lc.Name]; ok {
			out[i] = mergeListener(out[i], lc)
			return
		}
		idx[lc.Name] = len(out)
		out = append(out, lc)
	}
	for _, raw := range dump.Configs {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			continue
		}
		var typ string
		if json.Unmarshal(obj["@type"], &typ) != nil {
			continue
		}
		switch {
		case strings.HasSuffix(typ, typeDynamicListener):
			var d rawDynamic
			if json.Unmarshal(raw, &d) != nil {
				continue
			}
			if lc, ok := dynamicListener(d); ok {
				add(lc)
			}
		case strings.HasSuffix(typ, typeStaticListener):
			var s rawStatic
			if json.Unmarshal(raw, &s) != nil {
				continue
			}
			if lc, ok := staticListener(s); ok {
				add(lc)
			}
		case strings.HasSuffix(typ, typeListenersDump):
			var ld listenersDump
			if json.Unmarshal(raw, &ld) != nil {
				continue
			}
			if static {
				for _, s := range ld.Static {
					if lc, ok := staticListener(s); ok {
						add(lc)
					}
				}
			} else {
				for _, d := range ld.Dynamic {
					if lc, ok := dynamicListener(d); ok {
						add(lc)
					}
				}
			}
		}
	}
	return out, nil
}
