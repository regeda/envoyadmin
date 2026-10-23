package ui

import (
	"fmt"

	"github.com/regeda/envoyadmin/internal/snapshot"
)

type resourceKind int

const (
	kindClusters resourceKind = iota
	kindListeners
	kindSecrets
	kindCount
)

// view is one resource's list and details. The model keeps one per resource, so
// each tab keeps its own filter text, selection, details tab and scroll.
type view struct {
	list   listPane
	detail detailPane
}

// resource describes how a snapshot turns into a view and its chrome.
type resource struct {
	label       string
	items       func(snapshot.Snapshot) []resourceItem
	count       func(snapshot.Snapshot) int
	summary     func(snapshot.Snapshot) string
	notice      func(snapshot.Snapshot) string
	warn        func(snapshot.Snapshot) bool
	hint        string // the list's idle first row; empty means the default
	emptyList   string
	emptyDetail string
}

var resources = [kindCount]resource{
	kindClusters: {
		label: "Clusters",
		items: func(s snapshot.Snapshot) []resourceItem { return clusterItems(s.Clusters) },
		count: func(s snapshot.Snapshot) int { return len(s.Clusters) },
		summary: func(s snapshot.Snapshot) string {
			return fmt.Sprintf("%s · %d unhealthy", plural(len(s.Clusters), "cluster"), s.UnhealthyClusters())
		},
		notice:      func(snapshot.Snapshot) string { return "" },
		warn:        func(snapshot.Snapshot) bool { return false },
		emptyList:   "no clusters",
		emptyDetail: "no cluster selected",
	},
	kindListeners: {
		label: "Listeners",
		hint:  "/ to filter or : to routes",
		items: func(s snapshot.Snapshot) []resourceItem { return listenerItems(s.Listeners) },
		count: func(s snapshot.Snapshot) int { return len(s.Listeners) },
		summary: func(s snapshot.Snapshot) string {
			if s.ListenersErr != "" {
				return "listeners unavailable"
			}
			return fmt.Sprintf("%s · %d not active", plural(len(s.Listeners), "listener"), s.ListenersNotActive())
		},
		notice:      func(s snapshot.Snapshot) string { return s.ListenersErr },
		warn:        func(s snapshot.Snapshot) bool { return s.ListenersErr != "" },
		emptyList:   "no listeners",
		emptyDetail: "no listener selected",
	},
	kindSecrets: {
		label: "Secrets",
		items: func(s snapshot.Snapshot) []resourceItem { return secretItems(s.Secrets, s.FetchedAt) },
		count: func(s snapshot.Snapshot) int { return len(s.Secrets) },
		summary: func(s snapshot.Snapshot) string {
			if s.SecretsErr != "" {
				return "secrets unavailable"
			}
			return fmt.Sprintf("%s · %d to check", plural(len(s.Secrets), "secret"), s.SecretsNeedAttention())
		},
		notice:      func(s snapshot.Snapshot) string { return s.SecretsErr },
		warn:        func(s snapshot.Snapshot) bool { return s.SecretsErr != "" || s.SecretsNeedAttention() > 0 },
		emptyList:   "no secrets",
		emptyDetail: "no secret selected",
	},
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func newView(r resource, color bool) view {
	v := view{list: newListPane(), detail: newDetailPane(color)}
	v.list.SetWaiting(true)
	v.list.SetEmptyText(r.emptyList)
	if r.hint != "" {
		v.list.SetHint(r.hint)
	}
	v.detail.SetEmptyText(r.emptyDetail)
	return v
}

func (r resource) tabLabel(s snapshot.Snapshot) string {
	if r.warn(s) {
		return r.label + " (!)"
	}
	return fmt.Sprintf("%s (%d)", r.label, r.count(s))
}
