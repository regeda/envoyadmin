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
	Active, Warming, Draining *ListenerState
	Error                     *ListenerError
	Name                      string
	Static                    bool
}

type rawState struct {
	VersionInfo string          `json:"version_info"`
	LastUpdated string          `json:"last_updated"`
	Listener    json.RawMessage `json:"listener"`
}

type rawDynamic struct {
	Active   *rawState `json:"active_state"`
	Warming  *rawState `json:"warming_state"`
	Draining *rawState `json:"draining_state"`
	Error    *struct {
		Attempt string          `json:"last_update_attempt"`
		Details string          `json:"details"`
		Failed  json.RawMessage `json:"failed_configuration"`
	} `json:"error_state"`
	Name string `json:"name"`
}

type rawStatic struct {
	LastUpdated string          `json:"last_updated"`
	Listener    json.RawMessage `json:"listener"`
}

type listenersDump struct {
	Static  []rawStatic  `json:"static_listeners"`
	Dynamic []rawDynamic `json:"dynamic_listeners"`
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
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
		if json.Unmarshal(d.Error.Failed, &fc) == nil {
			name = fc.Name
		}
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
	var out []ListenerConfig
	idx := map[string]int{}
	err := walkConfigs(body, func(raw json.RawMessage, typ string) {
		for _, lc := range listenerElements(raw, typ, static) {
			if i, ok := idx[lc.Name]; ok { // elements for the same listener are merged
				out[i] = mergeListener(out[i], lc)
				continue
			}
			idx[lc.Name] = len(out)
			out = append(out, lc)
		}
	})
	return out, err
}

// listenerElements decodes one configs[] element into zero or more listeners.
func listenerElements(raw json.RawMessage, typ string, static bool) []ListenerConfig {
	switch {
	case strings.HasSuffix(typ, typeDynamicListener):
		var d rawDynamic
		if json.Unmarshal(raw, &d) != nil {
			return nil
		}
		return dynamicListeners([]rawDynamic{d})
	case strings.HasSuffix(typ, typeStaticListener):
		var s rawStatic
		if json.Unmarshal(raw, &s) != nil {
			return nil
		}
		return staticListeners([]rawStatic{s})
	case strings.HasSuffix(typ, typeListenersDump):
		var ld listenersDump
		if json.Unmarshal(raw, &ld) != nil {
			return nil
		}
		if static {
			return staticListeners(ld.Static)
		}
		return dynamicListeners(ld.Dynamic)
	}
	return nil
}

func dynamicListeners(list []rawDynamic) []ListenerConfig {
	var out []ListenerConfig
	for _, d := range list {
		if lc, ok := dynamicListener(d); ok {
			out = append(out, lc)
		}
	}
	return out
}

func staticListeners(list []rawStatic) []ListenerConfig {
	var out []ListenerConfig
	for _, s := range list {
		if lc, ok := staticListener(s); ok {
			out = append(out, lc)
		}
	}
	return out
}
