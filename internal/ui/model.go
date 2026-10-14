// Package ui is the bubbletea front end. It only knows snapshot types.
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/envoyadmin/internal/config"
	"github.com/regeda/envoyadmin/internal/snapshot"
)

const (
	minWidth      = 80
	minHeight     = 12
	compactHeight = 24
	maxBannerRows = 4
	footerRows    = 1
)

type Fetcher func(ctx context.Context) (snapshot.Snapshot, error)

type Options struct {
	Ctx    context.Context
	Config config.Config
	Fetch  Fetcher
	Clock  func() time.Time
	Color  bool
}

type connState int

const (
	stateConnecting connState = iota
	stateConnected
	stateDisconnected
)

type focus int

const (
	focusList focus = iota
	focusDetail
)

type snapshotMsg struct {
	snap snapshot.Snapshot
	err  error
}

type tickMsg struct{}

type Model struct {
	opts Options

	snap      snapshot.Snapshot
	hasSnap   bool
	err       error
	dismissed string
	lastOK    time.Time
	inflight  bool
	state     connState

	focus         focus
	help          bool
	width, height int
	views         [kindCount]view // an array, so a Model copy never aliases another's views
	active        resourceKind
}

// New returns a model whose first fetch is already considered in flight; Init
// issues it.
func New(o Options) Model {
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Ctx == nil {
		o.Ctx = context.Background()
	}
	m := Model{opts: o, inflight: true, state: stateConnecting}
	for k := resourceKind(0); k < kindCount; k++ {
		m.views[k] = newView(resources[k], o.Color)
	}
	return m
}

// cur is the active view (a pointer into this copy of the model).
func (m *Model) cur() *view { return &m.views[m.active] }

func (m Model) fetchCmd() tea.Cmd {
	fetch, ctx := m.opts.Fetch, m.opts.Ctx
	return func() tea.Msg {
		s, err := fetch(ctx)
		return snapshotMsg{snap: s, err: err}
	}
}

func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(m.opts.Config.Refresh, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.fetchCmd(), m.tickCmd()) }

func (m Model) headerRows() int {
	if m.height < compactHeight {
		return 2
	}
	return 4
}

func (m Model) paneHeight() int { return max(m.height-m.headerRows()-footerRows, 1) }

// listWidth is 30% of the terminal, but never below 28 cells (the narrowest
// width a cluster row stays readable at).
func (m Model) listWidth() int { return max(m.width*30/100, 28) }

func (m *Model) layout() {
	listW := m.listWidth()
	detailW := max(m.width-listW-1, 1)
	for k := range m.views {
		m.views[k].list.SetSize(listW, m.paneHeight())
		m.views[k].detail.SetSize(detailW, m.paneHeight())
	}
}

func (m *Model) syncDetails() {
	for k := range m.views {
		v := &m.views[k]
		it, ok := v.list.Selected()
		v.detail.SetItem(it, ok)
	}
	if _, ok := m.cur().list.Selected(); !ok && m.focus == focusDetail {
		m.setFocus(focusList) // nothing left to show details for
	}
}

func (m Model) bannerVisible() bool {
	return m.err != nil && m.err.Error() != m.dismissed
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.syncDetails()
		return m, nil

	case tickMsg:
		if m.inflight {
			return m, m.tickCmd()
		}
		m.inflight = true
		return m, tea.Batch(m.fetchCmd(), m.tickCmd())

	case snapshotMsg:
		m.inflight = false
		if msg.err != nil {
			m.err = msg.err
			m.state = stateDisconnected
			return m, nil
		}
		m.err, m.dismissed = nil, ""
		m.state = stateConnected
		m.snap, m.hasSnap = msg.snap, true
		m.lastOK = msg.snap.FetchedAt
		if m.lastOK.IsZero() {
			m.lastOK = m.opts.Clock()
		}
		for k := resourceKind(0); k < kindCount; k++ {
			v := &m.views[k]
			v.list.SetWaiting(false)
			v.list.SetNotice(resources[k].notice(msg.snap))
			v.list.SetItems(resources[k].items(msg.snap))
		}
		m.syncDetails()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// setFocus moves the keyboard focus; only the active view shows it.
func (m *Model) setFocus(f focus) {
	m.focus = f
	for k := range m.views {
		active := resourceKind(k) == m.active
		m.views[k].list.SetFocused(!active || f == focusList)
		m.views[k].detail.SetFocused(active && f == focusDetail)
	}
}

func (m *Model) switchTo(k resourceKind) {
	m.active = k
	m.setFocus(focusList)
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.focus == focusList && m.cur().list.filtering {
		switch k.String() {
		case "tab", "shift+tab":
			return m, nil // finish the filter first
		}
		m.cur().list.HandleKey(k)
		m.syncDetails()
		return m, nil
	}
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "r":
		if !m.inflight {
			m.inflight = true
			return m, m.fetchCmd()
		}
	case "x":
		if m.err != nil {
			m.dismissed = m.err.Error()
		}
	case "?":
		m.help = !m.help
	case "tab":
		m.switchTo((m.active + 1) % kindCount)
	case "shift+tab":
		m.switchTo((m.active + kindCount - 1) % kindCount)
	case "enter":
		if m.focus == focusList {
			if _, ok := m.cur().list.Selected(); ok {
				m.setFocus(focusDetail)
			}
		}
	case "esc":
		if m.focus == focusDetail {
			m.setFocus(focusList)
		} else {
			m.cur().list.HandleKey(k) // clears an active filter
			m.syncDetails()
		}
	case "c":
		m.cur().detail.SetTab(tabConfig)
	case "o", "e":
		m.cur().detail.SetTab(tabOverview)
	default:
		if k.String() == "/" {
			m.setFocus(focusList)
		}
		if m.focus == focusList {
			m.cur().list.HandleKey(k)
			m.syncDetails()
		} else {
			m.cur().detail.HandleKey(k)
		}
	}
	return m, nil
}

func (m Model) connLines() (string, string) {
	admin := m.opts.Config.Admin
	since := func() string { return ago(m.opts.Clock().Sub(m.lastOK)) }
	switch m.state {
	case stateConnected:
		l2 := "updated " + since()
		if m.snap.Version != "" {
			l2 = "Envoy " + m.snap.Version + " · " + l2
		}
		return okStyle.Render("● connected") + "  " + admin, dimStyle.Render(l2)
	case stateDisconnected:
		l2 := "never connected"
		if !m.lastOK.IsZero() {
			l2 = "last ok " + since()
		}
		return badStyle.Render("✗ disconnected") + "  " + admin, dimStyle.Render(l2)
	default:
		return dimStyle.Render("◌ connecting…") + "  " + admin, ""
	}
}

func (m Model) bannerText() string {
	if m.err == nil {
		return ""
	}
	first := "✗ disconnected · " + m.opts.Config.Admin
	if m.lastOK.IsZero() {
		first += " · never connected"
	} else {
		first += " · last ok " + ago(m.opts.Clock().Sub(m.lastOK))
	}
	return first + "\n" + m.err.Error() + "\n(retrying every " + m.opts.Config.Refresh.String() + ")"
}

// helpLine lists every key. The ones people rarely guess come first, so a
// narrow terminal that clips the end still shows how to close help and quit.
const helpLine = "x dismiss  r refresh  ? close  q quit  tab/shift+tab  enter  esc  ↑↓/jk g/G PgUp/PgDn  / filter  c config  o/e overview"

func (m Model) statusLine() string {
	if m.help {
		return renderStatus(m.width, " "+helpLine, "") // the summary gives way to the keys
	}
	left := " " + resources[m.active].summary(m.snap)
	if m.inflight {
		left += " ↻"
	}
	right := "? help · q quit · tab switch · enter details · / filter · c config · o overview · r refresh "
	if m.focus == focusDetail {
		left += " · focus: detail"
		right = "esc back · ↑↓ scroll · c config · o overview · q quit "
	}
	return renderStatus(m.width, left, right)
}

// detailBlock pads every detail row to the pane width (the pane only clips) so
// JoinHorizontal keeps the columns aligned.
func (m Model) detailBlock() string {
	w := max(m.width-m.listWidth()-1, 1)
	rows := strings.Split(m.views[m.active].detail.View(), "\n")
	for i, r := range rows {
		rows[i] = pad(trunc(r, w), w)
	}
	return strings.Join(rows, "\n")
}

func (m Model) View() string {
	if m.width < minWidth || m.height < minHeight {
		msg := fmt.Sprintf("terminal too small (%dx%d), need at least %dx%d", m.width, m.height, minWidth, minHeight)
		if m.width > 0 {
			msg = trunc(msg, m.width)
		}
		return msg
	}
	c1, c2 := m.connLines()
	tabs := make([]headerTab, kindCount)
	for k := resourceKind(0); k < kindCount; k++ {
		tabs[k] = headerTab{Label: resources[k].tabLabel(m.snap), Active: k == m.active, Warn: resources[k].warn(m.snap)}
	}
	header := renderHeader(m.width, m.height < compactHeight, c1, c2, tabs)

	paneH := m.paneHeight()
	sep := dimStyle.Render(strings.TrimSuffix(strings.Repeat("│\n", paneH), "\n"))
	panes := lipgloss.JoinHorizontal(lipgloss.Top, m.views[m.active].list.View(), sep, m.detailBlock())

	frame := header + "\n" + panes + "\n" + m.statusLine()
	if m.bannerVisible() {
		rows := min(maxBannerRows, max(1, m.height/3))
		frame = overlay(frame, bannerLines(m.bannerText(), m.width, rows))
	}
	return frame
}
