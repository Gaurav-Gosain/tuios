package app

import (
	"encoding/json"
	"image/color"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// Federation in the client: the rail draws every machine's sessions as one
// group per machine, this machine first, in an order that does not move.
//
// The one rule this file exists to keep is that no part of it touches the
// network from the Update goroutine. The daemon holds the links; the client
// asks it for a snapshot inside a tea.Cmd, stores what comes back, and the rail
// renders from the stored snapshot alone. A host that is powered off cannot
// slow a frame down, because a frame never waits on one.
//
// A client whose daemon has no hosts configured stops polling after the first
// answer, so the default install pays one verb call at attach and nothing after.
// The daemon pushes MsgHostsChanged when its table changes, which is what
// starts the poll again when the first host is added.
//
// The second rule is that a row stays where it is. Switching onto a session on
// build used to make build the main group at the top of the section and push
// this machine's sessions down into a group of their own, so the row the user
// had just clicked moved out from under the pointer and every other row moved
// with it. Now the machine groups keep one order whatever the client is
// attached to: this machine first, then the other machines in the daemon's
// sorted order, overlaid with the order the user dragged them into. The
// attached session is marked current in place, under whichever machine holds
// it, which is the same promise BuildSessionTree makes for a session switch on
// one machine.

const (
	// hostRefreshActive is the poll interval while the rail or the switcher is
	// on screen. It is the same cadence the foreign-session cache uses, for the
	// same reason: a status that is on screen should not be a minute stale.
	hostRefreshActive = 5 * time.Second
	// hostRefreshIdle is the poll interval with no consumer on screen.
	hostRefreshIdle = 30 * time.Second
)

// FederationSnapshot is what the daemon last said about the configured hosts,
// plus the sessions each holds. It is a value the render path reads and never
// writes.
type FederationSnapshot struct {
	// Hosts is one entry per configured host, in the daemon's sorted order.
	Hosts []FederationHost
	// Gen counts the snapshots that have landed. The sidebar's render cache
	// folds it in, so a status change redraws the rail and nothing else does.
	Gen uint64
}

// FederationHost is one host's row set in the snapshot.
type FederationHost struct {
	Name     string
	Status   string
	Reason   string
	LastOK   int64
	Sessions []FederationSession
}

// FederationSession is one session on another machine, as that machine
// described it.
type FederationSession struct {
	Name        string
	DisplayName string
	WindowCount int
	Attached    bool
}

// FederationHostsMsg carries a fresh snapshot back to the Update goroutine.
type FederationHostsMsg struct {
	Snapshot FederationSnapshot
	// Configured is how many hosts the daemon holds. Zero stops the polling.
	Configured int
}

// FederationRefreshTickMsg re-arms the poll.
type FederationRefreshTickMsg struct {
	// Gen is the timer generation this tick was armed under. A tick from an
	// older generation is dropped. Without it a snapshot's re-arm and the
	// tick's own re-arm both stood, and the number of live timers doubled
	// every period.
	Gen uint64
}

// HostsChangedMsg is the daemon's push that its [hosts] table changed while
// this client was attached. Update answers it with one poll, whatever the
// poll gate was set to.
type HostsChangedMsg struct{}

// federationRefreshTick arms the next host poll under a new generation.
func (m *OS) federationRefreshTick(after time.Duration) tea.Cmd {
	m.federationTickGen++
	gen := m.federationTickGen
	return tea.Tick(after, func(time.Time) tea.Msg { return FederationRefreshTickMsg{Gen: gen} })
}

// federationRefreshPlan decides the next poll interval and whether to poll at
// all. Polling stops for good once the daemon reports no hosts, which is the
// default install, and starts again only on the daemon's push.
func (m *OS) federationRefreshPlan() (after time.Duration, refresh bool) {
	// federationPolling is the only gate. It is armed in Init and only when a
	// daemon client exists, and the daemon is the only thing that knows about
	// hosts, so a second check against the client would say nothing this does
	// not already say.
	if !m.federationPolling {
		return hostRefreshIdle, false
	}
	if m.SidebarActive() || m.ShowSessionSwitcher {
		return hostRefreshActive, true
	}
	return hostRefreshIdle, true
}

// refreshFederationCmd asks the local daemon for every host's status and
// sessions, off the Update goroutine.
//
// It opens its own short verb connection rather than riding the attach
// connection, which speaks the binary protocol and would need a new message
// type and a protocol bump to carry this. A unix-socket dial and one verb call
// per poll is cheaper than either.
func refreshFederationCmd() tea.Cmd {
	return func() tea.Msg {
		client, err := session.DialVerbClient()
		if err != nil {
			// The daemon is the only thing that knows about hosts. If it cannot
			// be reached the rail simply shows no host groups, which is what it
			// showed before this existed.
			return FederationHostsMsg{}
		}
		defer func() { _ = client.Close() }()

		raw, err := client.Call("list-host-sessions", nil)
		if err != nil {
			return FederationHostsMsg{}
		}
		var res struct {
			Hosts []struct {
				Host     string `json:"host"`
				Status   string `json:"status"`
				Reason   string `json:"reason"`
				Sessions []struct {
					Name        string `json:"name"`
					DisplayName string `json:"display_name"`
					WindowCount int    `json:"window_count"`
					Attached    bool   `json:"attached"`
				} `json:"sessions"`
			} `json:"hosts"`
		}
		if json.Unmarshal(raw, &res) != nil {
			return FederationHostsMsg{}
		}

		// list-host-sessions carries the local machine as its first entry. It
		// is kept: while this client is attached on another machine, the rail
		// draws this machine's sessions from it. hostGroupNodes drops it the
		// rest of the time, when the rail draws the local sessions from live
		// state.
		msg := FederationHostsMsg{}
		lastOK := map[string]int64{}
		for _, h := range hostStatusReports(client) {
			lastOK[h.Host] = h.LastOK
		}
		for _, h := range res.Hosts {
			if h.Host != federation.LocalHostName {
				msg.Configured++
			}
			fh := FederationHost{Name: h.Host, Status: h.Status, Reason: h.Reason, LastOK: lastOK[h.Host]}
			for _, s := range h.Sessions {
				fh.Sessions = append(fh.Sessions, FederationSession{
					Name:        s.Name,
					DisplayName: s.DisplayName,
					WindowCount: s.WindowCount,
					Attached:    s.Attached,
				})
			}
			msg.Snapshot.Hosts = append(msg.Snapshot.Hosts, fh)
		}
		return msg
	}
}

// hostStatusReports fetches the last-contact times the session listing does not
// carry. A failure here costs the rail a relative time and nothing else.
func hostStatusReports(client *session.VerbClient) []federation.HostReport {
	raw, err := client.Call("list-hosts", nil)
	if err != nil {
		return nil
	}
	var res struct {
		Hosts []federation.HostReport `json:"hosts"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return nil
	}
	return res.Hosts
}

// applyFederationSnapshot stores a snapshot the poll returned. It runs on the
// Update goroutine and does no I/O.
func (m *OS) applyFederationSnapshot(msg FederationHostsMsg) {
	m.federationPolling = msg.Configured > 0
	m.FederationHosts = msg.Snapshot.Hosts
	m.federationGen++
}

// attachedMachine is the machine whose sessions the tree's main group holds:
// this one, or the host the client is attached through.
func (m *OS) attachedMachine() string {
	if m.AttachedHost == "" {
		return federation.LocalHostName
	}
	return m.AttachedHost
}

// hostGroupNodes turns the stored snapshot into the rows of every machine but
// the attached one: one header per machine, then that machine's sessions.
//
// A host that is not up contributes its header alone, carrying the reason. That
// is section 7's rule on screen: the machine is still listed, greyed, with when
// it was last seen, rather than disappearing and leaving the user to wonder
// whether they imagined configuring it.
//
// Each machine's sessions keep that daemon's creation order, overlaid with the
// order the user dragged them into while attached there, so a machine's rows
// read the same whether the client is on it or looking at it from elsewhere.
func (m *OS) hostGroupNodes() []sessiontree.Node {
	if len(m.FederationHosts) == 0 {
		return nil
	}
	out := make([]sessiontree.Node, 0, len(m.FederationHosts)*2)
	for _, h := range m.FederationHosts {
		// The machine whose sessions the main group shows is not listed
		// twice: this machine while the client is here, the attached host
		// while it is away.
		if h.Name == m.attachedMachine() {
			continue
		}
		out = append(out, sessiontree.Node{
			Kind:        sessiontree.KindHost,
			ID:          hostNodeID(h.Name),
			Title:       h.Name,
			Host:        h.Name,
			HostStatus:  h.Status,
			HostNote:    h.Reason,
			HostLastOK:  h.LastOK,
			WindowCount: len(h.Sessions),
		})
		sessions := orderByKey(h.Sessions, func(s FederationSession) string { return s.Name },
			m.sidebarSessionOrderFor(h.Name))
		for _, s := range sessions {
			title := s.Name
			if s.DisplayName != "" {
				title = s.DisplayName
			}
			out = append(out, sessiontree.Node{
				Kind:        sessiontree.KindSession,
				ID:          hostNodeID(h.Name) + ":" + s.Name,
				Title:       title,
				Host:        h.Name,
				WindowCount: s.WindowCount,
				Attached:    s.Attached,
			})
		}
	}
	return out
}

// hostNodeID namespaces a host's rows so their ids can never collide with a
// local session name. Nothing resolves these ids: they exist so the render
// cache and the row loop have a stable key per row.
func hostNodeID(host string) string { return "\x00host/" + host }

// isRemoteNode reports whether a tree node belongs to a machine other than the
// attached one. It is drawn in its machine's group, never dragged, renamed,
// deleted, or switched to as a local session. A session under an up host is
// the one thing a remote row can be: a target that attaches it in this client.
// See drawHostRow.
func isRemoteNode(n sessiontree.Node) bool {
	return n.Kind == sessiontree.KindHost || n.Host != ""
}

// hostStatusByName is the link state the last snapshot reported for a host, or
// "" when the snapshot does not name it.
func (m *OS) hostStatusByName(name string) string {
	for _, h := range m.FederationHosts {
		if h.Name == name {
			return h.Status
		}
	}
	return ""
}

// hostIsUp reports whether a host's link is up, which is the one state a remote
// session can be opened from. A listing from any other state is cached, so its
// rows are shown and are not targets. This machine is always up, and so is the
// machine the client is attached to: the attach is the proof.
func (m *OS) hostIsUp(name string) bool {
	if name == federation.LocalHostName || name == m.attachedMachine() {
		return true
	}
	return m.hostStatusByName(name) == string(federation.StatusUp)
}

// remoteSessionName recovers the raw session name from a remote session node's
// namespaced id. The id is hostNodeID(host)+":"+name, so the name is what
// follows the last colon after the host prefix.
func remoteSessionName(node sessiontree.Node) string {
	prefix := hostNodeID(node.Host) + ":"
	return strings.TrimPrefix(node.ID, prefix)
}

// The machine groups of the sessions section.

// sidebarMachineRows lays the sessions section out by machine.
//
// here is the attached machine's rows, already grouped by repository and with
// folded repositories' members left out; remote is every other machine's rows
// from hostGroupNodes. With no other machine the section is here alone, so a
// person with one machine sees the rail they always had. With any other
// machine every machine gets a header, this one included, because a section
// that names some of its machines and not others leaves the reader to guess
// which rows are the unnamed one's.
//
// The order is this machine first, then the others as the daemon sorts them,
// overlaid with the user's drag order. It is the same order whatever the
// client is attached to, which is the whole point: a row is where it was.
func (m *OS) sidebarMachineRows(here, remote []sessiontree.Node) []sessiontree.Node {
	m.SidebarHostIDs = m.SidebarHostIDs[:0]
	if len(remote) == 0 {
		return here
	}

	type machineGroup struct {
		header sessiontree.Node
		rows   []sessiontree.Node
	}
	attached := m.attachedMachine()
	groups := []machineGroup{{header: m.attachedMachineHeader(attached, here), rows: here}}
	for i := 0; i < len(remote); {
		g := machineGroup{header: remote[i]}
		for i++; i < len(remote) && remote[i].Kind != sessiontree.KindHost; i++ {
			g.rows = append(g.rows, remote[i])
		}
		groups = append(groups, g)
	}

	// This machine is pinned first and is not dragged. The others take the
	// user's order, or the draft order of a drag in progress.
	var local []machineGroup
	others := make([]machineGroup, 0, len(groups))
	for _, g := range groups {
		if g.header.Host == federation.LocalHostName {
			local = append(local, g)
		} else {
			others = append(others, g)
		}
	}
	order := m.SidebarHostOrder
	if m.SidebarDrag.Dragging && m.SidebarDrag.Host {
		order = m.SidebarDrag.Order
	}
	others = orderByKey(others, func(g machineGroup) string { return g.header.Host }, order)
	groups = append(local, others...)

	out := make([]sessiontree.Node, 0, len(here)+len(remote)+1)
	for _, g := range groups {
		if g.header.Host != federation.LocalHostName {
			m.SidebarHostIDs = append(m.SidebarHostIDs, g.header.Host)
		}
		out = append(out, g.header)
		if m.SidebarHostCollapsed(g.header.Host) {
			continue
		}
		out = append(out, g.rows...)
	}
	return out
}

// attachedMachineHeader is the group header for the machine the client is
// attached to, which the snapshot does not carry as a group. It is up by
// definition, and it counts sessions rather than rows: a repository's own row
// is not a session.
func (m *OS) attachedMachineHeader(name string, rows []sessiontree.Node) sessiontree.Node {
	count := 0
	for _, n := range rows {
		if n.Kind == sessiontree.KindSession {
			count++
		}
	}
	return sessiontree.Node{
		Kind:        sessiontree.KindHost,
		ID:          hostNodeID(name),
		Title:       name,
		Host:        name,
		HostStatus:  string(federation.StatusUp),
		WindowCount: count,
	}
}

// SidebarHostCollapsed reports whether a machine's group is folded shut.
func (m *OS) SidebarHostCollapsed(host string) bool {
	return m.SidebarCollapsedHosts[host]
}

// SidebarToggleHostCollapsed folds a machine's group shut, or opens it again,
// and remembers which it is. The set is keyed by host name, so a group the
// user shut yesterday is still shut after a restart. Folding the group the
// attached session is in is allowed: the session keeps running and the
// terminals section keeps listing its panes, and the fold hides rows only.
func (m *OS) SidebarToggleHostCollapsed(host string) {
	if host == "" {
		return
	}
	if m.SidebarCollapsedHosts[host] {
		delete(m.SidebarCollapsedHosts, host)
	} else {
		if m.SidebarCollapsedHosts == nil {
			m.SidebarCollapsedHosts = make(map[string]bool, 1)
		}
		m.SidebarCollapsedHosts[host] = true
	}
	m.saveSidebarState()
}

// sidebarSessionOrderFor is the user's drag order for one machine's sessions:
// SidebarOrder for this machine, and the per-host order for any other.
func (m *OS) sidebarSessionOrderFor(host string) []string {
	if host == federation.LocalHostName {
		return m.SidebarOrder
	}
	return m.SidebarHostSessionOrder[host]
}

// setSidebarSessionOrder records a drag order for one machine's sessions.
func (m *OS) setSidebarSessionOrder(host string, order []string) {
	if host == federation.LocalHostName {
		m.SidebarOrder = order
		return
	}
	if m.SidebarHostSessionOrder == nil {
		m.SidebarHostSessionOrder = map[string][]string{}
	}
	m.SidebarHostSessionOrder[host] = order
}

// drawHostRow draws one machine row and records what a person can reach on it.
//
// A machine's header is a target that folds the group. An up host's header
// carries a "+" that creates a session there; the attached machine's carries
// the section's own new-session control. A session row under an up host is a
// target that attaches the session in this client. Every row under a host that
// is not up is drawn and is not a target, because its listing is cached and
// the machine cannot be reached right now.
func (m *OS) drawHostRow(
	node sessiontree.Node, cw int, pal overlay.Palette, hovered, canCreate bool,
	isCursor func(kind sidebarRowKind, sessionID, windowID string) bool,
	recordHit func(kind sidebarRowKind, sessionID, windowID string, windowIndex, h int),
	recordToken func(tk sidebarTokenSpan, sessionID string),
	headerHoverX int,
	compose func(content string) string,
	lines *[]string,
) {
	if node.Kind == sessiontree.KindHost {
		collapsed := m.SidebarHostCollapsed(node.Host)
		hovered = hovered || isCursor(sidebarRowHost, node.Host, "")
		add := ""
		if !collapsed && node.HostStatus == string(federation.StatusUp) {
			// The attached machine's control is the section's: it creates on
			// the daemon this client is on, which is that machine.
			kind, id := sidebarRowHostNew, node.Host
			if node.Host == m.attachedMachine() {
				kind, id = sidebarRowNewSession, ""
			}
			labelW := sidebarHeaderLabelW(m.Settings.GetRailFoldOpenGlyph() + " " + node.Title)
			if kind != sidebarRowNewSession || canCreate {
				if tok, span, ok := sidebarHeaderAdd(kind, cw, labelW, pal,
					headerHoverX, isCursor(kind, id, ""), &m.Settings); ok {
					add = tok
					recordToken(span, id)
				}
			}
		}
		recordHit(sidebarRowHost, node.Host, "", -1, 1)
		*lines = append(*lines, compose(m.sidebarHostRow(node, cw, pal, add, hovered, collapsed)))
		return
	}

	// A session row. It is a target only when its host is up.
	if m.hostIsUp(node.Host) {
		hovered = hovered || isCursor(sidebarRowHostSession, node.Host, remoteSessionName(node))
		recordHit(sidebarRowHostSession, node.Host, remoteSessionName(node), -1, 1)
	}
	*lines = append(*lines, compose(m.sidebarRemoteSessionRow(node, cw, pal, hovered)))
}

// openRemoteSession attaches a session that lives on another machine, in this
// client. The connection goes through this machine's daemon over its link,
// and the session is drawn here with this machine's theme and config. See
// SwitchToHostSession.
func (m *OS) openRemoteSession(host, sessionName string) {
	if !m.hostIsUp(host) {
		m.ShowNotification(host+" is unavailable", "warning", m.Settings.NotificationWarningDuration)
		return
	}
	m.clearSidebarReturn() // opening the session is where the user asked to end up
	if err := m.SwitchToHostSession(host, sessionName, false); err != nil {
		m.ShowNotification(hostAttachRefusal(host, err), "error", m.Settings.NotificationDuration*3)
	}
}

// createRemoteSession creates a session on another machine and attaches it in
// this client. The host's daemon creates it under the first free name.
func (m *OS) createRemoteSession(host string) {
	if !m.hostIsUp(host) {
		m.ShowNotification(host+" is unavailable", "warning", m.Settings.NotificationWarningDuration)
		return
	}
	m.clearSidebarReturn()
	if err := m.SwitchToHostSession(host, "", true); err != nil {
		m.ShowNotification(hostAttachRefusal(host, err), "error", m.Settings.NotificationDuration*3)
		return
	}
	m.applyStartupTiling()
}

// localSessionNodes drops the other machines' rows from a tree's session list.
// The surfaces that only deal with the attached machine (the colour
// arbitration, the collapsed glyph strip, session cycling) read the tree
// through it.
func localSessionNodes(nodes []sessiontree.Node) []sessiontree.Node {
	for i, n := range nodes {
		if !isRemoteNode(n) {
			continue
		}
		// Host groups are appended after every local session, so the first
		// remote row is the end of the local ones and no copy is needed.
		return nodes[:i]
	}
	return nodes
}

// hostStatusLabel is the short word a host header shows on its right. It is one
// word so the host name keeps the room.
func hostStatusLabel(status string) string {
	switch federation.Status(status) {
	case federation.StatusUp:
		return ""
	case federation.StatusNoDaemon:
		return "no daemon"
	case federation.StatusNoBinary:
		return "no tuios"
	case federation.StatusIncompatible:
		return "version"
	case federation.StatusConnecting:
		return "connecting"
	case federation.StatusReconnecting:
		return "reconnecting"
	default:
		return "offline"
	}
}

// sidebarHostRow draws a machine's group header.
//
//	▾ local                +
//	▾ build                +
//	▸ pi                   3
//	▸ work           offline
//
// The mark is the fold mark, open or shut, in both glyph modes rather than a
// machine icon: the one thing the row has to say beyond its name is that it
// folds and which way it is folded now, it is one cell wide in every font, and
// the rail's other marks are about panes rather than machines. A shut group
// shows how many sessions it is holding, ungated by the counts setting, since
// that number is the only thing on the row saying the fold is not empty. A
// host that is not answering keeps its row with one word saying why, because a
// machine that vanished from the rail reads as a machine nobody configured.
func (m *OS) sidebarHostRow(node sessiontree.Node, cw int, pal overlay.Palette, add string, hovered, collapsed bool) string {
	var rowBg color.Color
	if hovered {
		rowBg = pal.Surface
	}
	up := node.HostStatus == string(federation.StatusUp)

	right, rightW := "", 0
	switch {
	case !up:
		// A host that is not up says why, in the slot the add control would take.
		// An unreachable machine has nothing to add a session to.
		label := hostStatusLabel(node.HostStatus)
		right = sidebarStyle(rowBg, pal.FgMute).Render(label)
		rightW = lipgloss.Width(label)
	case collapsed && node.WindowCount > 0:
		count := strconv.Itoa(node.WindowCount)
		right = sidebarStyle(rowBg, pal.FgMute).Render(count)
		rightW = lipgloss.Width(count)
	case add != "":
		right = add
		rightW = lipgloss.Width(add)
	}

	// The machine the client is on reads in the full ink, the others one step
	// down, so "where am I" is answered at the machine level as well as on the
	// session row. A machine that is not up is muted with its rows.
	here := node.Host == m.attachedMachine()
	ink := pal.FgDim
	if hovered || here {
		ink = pal.Fg
	}
	if !up {
		ink = pal.FgMute
	}
	mark := m.Settings.GetRailFoldOpenGlyph()
	if collapsed {
		mark = m.Settings.GetRailFoldShutGlyph()
	}
	glyph := sidebarStyle(rowBg, pal.FgMute).Render(mark)
	name := sidebarStyle(rowBg, ink).Render(
		overlay.Truncate(printableTitle(node.Title), sidebarNameAvail(cw, rightW)))
	// A folded group hides the session row that wears the focus mark, so the
	// header takes it: the fold must not make the attached session vanish from
	// the rail without a trace.
	gutter := sidebarGutter(here && collapsed, "", rowBg, pal, &m.Settings)
	return sidebarComposeRow(gutter, glyph, name, right, cw, rowBg)
}

// sidebarRemoteSessionRow draws one session that lives on a machine the client
// is not attached to. It sits on the same spine as a local session's row, with
// the resting mark in the glyph cell, so a machine's rows read the same
// whether the client is on it or not; what says it is elsewhere is the header
// above it, and what says it is a listing rather than a place is the muted ink.
func (m *OS) sidebarRemoteSessionRow(node sessiontree.Node, cw int, pal overlay.Palette, hovered bool) string {
	var rowBg color.Color
	if hovered {
		rowBg = pal.Surface
	}
	right, rightW := "", 0
	if m.Settings.SidebarShowCounts && node.WindowCount > 0 {
		count := strconv.Itoa(node.WindowCount)
		right = sidebarStyle(rowBg, pal.FgMute).Render(count)
		rightW = lipgloss.Width(count)
	}
	ink := pal.FgMute
	if hovered {
		ink = pal.FgDim
	}
	name := sidebarStyle(rowBg, ink).Render(
		overlay.Truncate(printableTitle(node.Title), sidebarNameAvail(cw, rightW)))
	gutter := sidebarStyle(rowBg, nil).Render(" ")
	glyph := sidebarStyle(rowBg, pal.FgMute).Render(m.Settings.GetRailBullet())
	return sidebarComposeRow(gutter, glyph, name, right, cw, rowBg)
}
