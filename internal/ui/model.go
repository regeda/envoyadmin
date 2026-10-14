// Package ui is the bubbletea front end. It only knows snapshot types.
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/config"
	"github.com/regeda/txenvoy/internal/snapshot"
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
	list          listPane
	detail        detailPane
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
	m := Model{opts: o, inflight: true, state: stateConnecting, list: newListPane(), detail: newDetailPane(o.Color)}
	m.list.SetWaiting(true)
	return m
}

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

func (m Model) listWidth() int { return min(max(m.width*35/100, 28), 50) }

func (m *Model) layout() {
	listW := m.listWidth()
	detailW := max(m.width-listW-1, 1)
	m.list.SetSize(listW, m.paneHeight())
	m.detail.SetSize(detailW, m.paneHeight())
}

func (m *Model) syncDetail() {
	c, ok := m.list.Selected()
	m.detail.SetCluster(c, ok)
}

func (m Model) bannerVisible() bool {
	return m.err != nil && m.err.Error() != m.dismissed
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.syncDetail()
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
			m.list.SetWaiting(false) // the banner explains; stop saying "waiting"
			return m, nil
		}
		m.err, m.dismissed = nil, ""
		m.state = stateConnected
		m.snap, m.hasSnap = msg.snap, true
		m.lastOK = msg.snap.FetchedAt
		m.list.SetWaiting(false)
		m.list.SetClusters(msg.snap.Clusters)
		m.syncDetail()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.focus == focusList && m.list.filtering {
		m.list.HandleKey(k)
		m.syncDetail()
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
		if m.focus == focusList {
			m.focus = focusDetail
		} else {
			m.focus = focusList
		}
	case "c":
		m.detail.SetTab(tabConfig)
	case "e":
		m.detail.SetTab(tabEndpoints)
	default:
		if k.String() == "/" {
			m.focus = focusList
		}
		if m.focus == focusList {
			m.list.HandleKey(k)
			m.syncDetail()
		} else {
			m.detail.HandleKey(k)
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

func (m Model) statusLine() string {
	left := fmt.Sprintf(" %d clusters · %d unhealthy", len(m.snap.Clusters), m.snap.UnhealthyClusters())
	if m.inflight {
		left += " ↻"
	}
	right := "/ filter · c config · e endpoints · r refresh · ? help · q quit "
	if m.help {
		right = "↑↓/jk move · / filter · tab focus · c config · e endpoints · r refresh · x dismiss · q quit "
	}
	return renderStatus(m.width, left, right)
}

// detailBlock pads every detail row to the pane width (the pane only clips) so
// JoinHorizontal keeps the columns aligned.
func (m Model) detailBlock() string {
	w := max(m.width-m.listWidth()-1, 1)
	rows := strings.Split(m.detail.View(), "\n")
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
	header := renderHeader(m.width, m.height < compactHeight, c1, c2, fmt.Sprintf("Clusters (%d)", len(m.snap.Clusters)))

	paneH := m.paneHeight()
	sep := dimStyle.Render(strings.TrimSuffix(strings.Repeat("│\n", paneH), "\n"))
	panes := lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), sep, m.detailBlock())

	frame := header + "\n" + panes + "\n" + m.statusLine()
	if m.bannerVisible() {
		rows := min(maxBannerRows, max(1, m.height/3))
		frame = overlay(frame, bannerLines(m.bannerText(), m.width, rows))
	}
	return frame
}
