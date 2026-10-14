package ui

import (
	"fmt"

	"github.com/regeda/txenvoy/internal/snapshot"
)

type resourceKind int

const (
	kindClusters resourceKind = iota
	kindListeners
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
	emptyList   string
	emptyDetail string
}

var resources = [kindCount]resource{
	kindClusters: {
		label: "Clusters",
		items: func(s snapshot.Snapshot) []resourceItem { return clusterItems(s.Clusters) },
		count: func(s snapshot.Snapshot) int { return len(s.Clusters) },
		summary: func(s snapshot.Snapshot) string {
			return fmt.Sprintf("%d clusters · %d unhealthy", len(s.Clusters), s.UnhealthyClusters())
		},
		notice:      func(snapshot.Snapshot) string { return "" },
		warn:        func(snapshot.Snapshot) bool { return false },
		emptyList:   "no clusters",
		emptyDetail: "no cluster selected",
	},
	kindListeners: {
		label: "Listeners",
		items: func(s snapshot.Snapshot) []resourceItem { return listenerItems(s.Listeners) },
		count: func(s snapshot.Snapshot) int { return len(s.Listeners) },
		summary: func(s snapshot.Snapshot) string {
			return fmt.Sprintf("%d listeners · %d not active", len(s.Listeners), s.ListenersNotActive())
		},
		notice:      func(s snapshot.Snapshot) string { return s.ListenersErr },
		warn:        func(s snapshot.Snapshot) bool { return s.ListenersErr != "" },
		emptyList:   "no listeners",
		emptyDetail: "no listener selected",
	},
}

func newView(r resource, color bool) view {
	v := view{list: newListPane(), detail: newDetailPane(color)}
	v.list.SetWaiting(true)
	v.list.SetEmptyText(r.emptyList)
	v.detail.SetEmptyText(r.emptyDetail)
	return v
}

func (r resource) tabLabel(s snapshot.Snapshot) string {
	if r.warn(s) {
		return r.label + " (!)"
	}
	return fmt.Sprintf("%s (%d)", r.label, r.count(s))
}
