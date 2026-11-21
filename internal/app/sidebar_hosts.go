package app

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

const (
	hostRefreshActive = 5 * time.Second
	hostRefreshIdle   = 30 * time.Second
	hostRefreshPushed = time.Minute
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
	// Queued is how many messages the daemon holds for the machine until its
	// link is back.
	Queued int
	// ApprovalURL is the Tailscale sign-in page the link waits on, set only
	// while Status is federation.StatusApproval and the link has one.
	ApprovalURL string
	// ApprovalRefused says the banner named a sign-in address that is not a
	// Tailscale login origin, so there is no page to open.
	ApprovalRefused bool
	// Cores and Load1 are the machine's CPUs and one-minute load from the
	// last hello on its link, and RTTMillis that hello's round trip. Cores is
	// 0 until a hello carried a load.
	Cores int
	Load1 float64
	// LoadOK says Load1 was read: false when the machine reads no load.
	LoadOK    bool
	RTTMillis int64
}

// FederationSession is one session on another machine, as that machine
// described it.
type FederationSession struct {
	Name        string
	DisplayName string
	WindowCount int
	Attached    bool
	// Global marks a session meant to hold panes from more than one machine.
	// The rail files it in the global group rather than under this machine.
	Global bool
	// AgentState is the most urgent state among the session's panes, rolled up
	// by the daemon that owns them. Empty when nothing in it runs an agent,
	// and empty for a machine that did not answer, because a row from a
	// machine that is not there is not evidence about what its agents are
	// doing now.
	AgentState string
	// Agents are the session's agent panes, which the agents view draws as
	// rows of their own. Empty from a daemon that does not send them.
	Agents []FederationAgent
}

// FederationAgent is one agent pane of a session on another machine, as the
// daemon passed it on.
type FederationAgent struct {
	ID        string                  `json:"id"`
	Title     string                  `json:"title"`
	State     string                  `json:"state"`
	StateAt   int64                   `json:"state_at"`
	Harness   string                  `json:"harness"`
	Message   string                  `json:"message"`
	Kind      string                  `json:"kind"`
	Meta      []sessiontree.MetaToken `json:"meta"`
	Subagents int                     `json:"subagents"`
	Settled   bool                    `json:"settled"`
	Quiet     bool                    `json:"quiet"`
}

// FederationHostsMsg carries a fresh snapshot back to the Update goroutine.
type FederationHostsMsg struct {
	Snapshot FederationSnapshot
	// Configured is how many hosts the daemon holds. Zero stops the polling.
	Configured int
	// Pushed says the daemon pushes every change to every host that is up:
	// it says events_push in list-hosts, and each such host's events are
	// live. The rail then waits for the push instead of polling. A host whose
	// tuios is too old to stream, or a daemon too old to push, keeps the poll.
	Pushed bool
	// Local is the load of the daemon's own machine, from list-hosts.
	Local federation.Load
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

func (m *OS) federationRefreshTick(after time.Duration) tea.Cmd {
	m.federationTickGen++
	gen := m.federationTickGen
	return m.tickUntilStop(after, func(time.Time) tea.Msg { return FederationRefreshTickMsg{Gen: gen} })
}

func (m *OS) federationRefreshPlan() (after time.Duration, refresh bool) {
	if !m.federationPolling {
		return hostRefreshIdle, false
	}
	if m.hostSignInWatching() {
		return hostRefreshActive, true
	}
	if m.hostOpen != nil && !m.hostOpen.dialing {
		return hostOpenPoll, true
	}
	if m.federationPushed && m.AttachedHost == "" {
		return hostRefreshPushed, true
	}
	if m.SidebarActive() || m.gotoPicker.open {
		return hostRefreshActive, true
	}
	return hostRefreshIdle, true
}

func refreshFederationCmd() tea.Cmd {
	return func() tea.Msg {
		client, err := session.DialVerbClient()
		if err != nil {
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
					Name        string            `json:"name"`
					DisplayName string            `json:"display_name"`
					WindowCount int               `json:"window_count"`
					Attached    bool              `json:"attached"`
					Global      bool              `json:"global"`
					AgentState  string            `json:"agent_state"`
					Agents      []FederationAgent `json:"agents"`
				} `json:"sessions"`
			} `json:"hosts"`
		}
		if jsonx.Unmarshal(raw, &res) != nil {
			return FederationHostsMsg{}
		}

		msg := FederationHostsMsg{}
		lastOK := map[string]int64{}
		queued := map[string]int{}
		signIn := map[string]string{}
		signInRefused := map[string]bool{}
		reports, pushes, local := hostStatusReports(client)
		msg.Local = local
		loads := map[string]federation.HostReport{}
		msg.Pushed = pushes
		for _, h := range reports {
			lastOK[h.Host] = h.LastOK
			loads[h.Host] = h
			queued[h.Host] = h.Queued
			signIn[h.Host] = h.ApprovalURL
			signInRefused[h.Host] = h.ApprovalRefused
			if h.Status == federation.StatusUp && h.Events != "live" {
				msg.Pushed = false
			}
		}
		for _, h := range res.Hosts {
			if h.Host != federation.LocalHostName {
				msg.Configured++
			}
			fh := FederationHost{Name: h.Host, Status: h.Status, Reason: h.Reason, LastOK: lastOK[h.Host], Queued: queued[h.Host], ApprovalURL: signIn[h.Host], ApprovalRefused: signInRefused[h.Host]}
			if r := loads[h.Host]; r.Cores > 0 {
				fh.Cores, fh.Load1, fh.RTTMillis, fh.LoadOK = r.Cores, r.Load1, r.RTTMillis, r.LoadKnown()
			}
			for _, s := range h.Sessions {
				fh.Sessions = append(fh.Sessions, FederationSession{
					Name:        s.Name,
					DisplayName: s.DisplayName,
					WindowCount: s.WindowCount,
					Attached:    s.Attached,
					Global:      s.Global,
					AgentState:  s.AgentState,
					Agents:      s.Agents,
				})
			}
			msg.Snapshot.Hosts = append(msg.Snapshot.Hosts, fh)
		}
		return msg
	}
}

func hostStatusReports(client *session.VerbClient) ([]federation.HostReport, bool, federation.Load) {
	raw, err := client.Call("list-hosts", nil)
	if err != nil {
		return nil, false, federation.Load{}
	}
	var res struct {
		Hosts      []federation.HostReport `json:"hosts"`
		EventsPush bool                    `json:"events_push"`
		Load       *federation.Load        `json:"load"`
	}
	if jsonx.Unmarshal(raw, &res) != nil {
		return nil, false, federation.Load{}
	}
	var load federation.Load
	if res.Load != nil {
		load = *res.Load
	}
	return res.Hosts, res.EventsPush, load
}

func (m *OS) applyFederationSnapshot(msg FederationHostsMsg) {
	m.federationPolling = msg.Configured > 0
	m.federationPushed = msg.Pushed
	m.FederationHosts = msg.Snapshot.Hosts
	m.LocalLoad = msg.Local
	m.federationGen++
}

func (m *OS) attachedMachine() string {
	if m.AttachedHost == "" {
		return federation.LocalHostName
	}
	return m.AttachedHost
}

func (m *OS) hostGroupNodes() []sessiontree.Node {
	if len(m.FederationHosts) == 0 {
		return nil
	}
	out := make([]sessiontree.Node, 0, len(m.FederationHosts)*2)
	for _, h := range m.FederationHosts {
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
			HostQueued:  h.Queued,
			WindowCount: len(h.Sessions),
			AgentState:  hostWorstAgentState(h.Sessions),
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
				AgentState:  s.AgentState,
			})
		}
	}
	return out
}

func (m *OS) hostAttention(host string) (blocked int, worst string) {
	if !m.hostIsUp(host) {
		return 0, ""
	}
	rank := 0
	for _, h := range m.FederationHosts {
		if h.Name != host {
			continue
		}
		for _, s := range h.Sessions {
			if !sidebarAttention(s.AgentState) {
				continue
			}
			blocked++
			if r := sessiontree.AgentRank(s.AgentState, false); r > rank {
				worst, rank = s.AgentState, r
			}
		}
	}
	return blocked, worst
}

func hostWorstAgentState(sessions []FederationSession) string {
	best, state := 0, ""
	for _, s := range sessions {
		if r := sessiontree.AgentRank(s.AgentState, false); r > best {
			best, state = r, s.AgentState
		}
	}
	return state
}

func hostNodeID(host string) string { return "\x00host/" + host }

func isRemoteNode(n sessiontree.Node) bool {
	return n.Kind == sessiontree.KindHost || n.Host != ""
}

func (m *OS) hostStatusByName(name string) string {
	for _, h := range m.FederationHosts {
		if h.Name == name {
			return h.Status
		}
	}
	return ""
}

func (m *OS) hostIsUp(name string) bool {
	if name == federation.LocalHostName || name == m.attachedMachine() {
		return true
	}
	return m.hostStatusByName(name) == string(federation.StatusUp)
}

func remoteSessionName(node sessiontree.Node) string {
	prefix := hostNodeID(node.Host) + ":"
	return strings.TrimPrefix(node.ID, prefix)
}

func stripGlobalRows(rows []sessiontree.Node) (kept, global []sessiontree.Node) {
	kept = rows[:0:0]
	for _, n := range rows {
		if isGlobalSessionRow(n) {
			global = append(global, n)
			continue
		}
		kept = append(kept, n)
	}
	return kept, global
}

func stripGlobalRowsCounted(rows []sessiontree.Node, count int, global *[]sessiontree.Node) ([]sessiontree.Node, int) {
	kept, found := stripGlobalRows(rows)
	if len(found) == 0 {
		return rows, count
	}
	*global = append(*global, found...)
	return kept, max(count-len(found), 0)
}

func isGlobalSessionRow(n sessiontree.Node) bool {
	if n.Kind != sessiontree.KindSession {
		return false
	}
	return n.Global || remoteSessionName(n) == GlobalSessionName
}

func (m *OS) globalGroupHeader(rows []sessiontree.Node) sessiontree.Node {
	return sessiontree.Node{
		Kind:        sessiontree.KindHost,
		Global:      true,
		ID:          hostNodeID(GlobalSessionName),
		Title:       GlobalSessionName,
		Host:        GlobalSessionName,
		HostStatus:  string(federation.StatusUp),
		WindowCount: len(rows),
		AgentState:  worstNodeAgentState(rows),
	}
}

func worstNodeAgentState(rows []sessiontree.Node) string {
	best, state := 0, ""
	for _, n := range rows {
		if r := sessiontree.AgentRank(n.AgentState, n.DoneSeen); r > best {
			best, state = r, n.AgentState
		}
	}
	return state
}

func (m *OS) sidebarMachineRows(here, remote []sessiontree.Node) []sessiontree.Node {
	m.SidebarHostIDs = m.SidebarHostIDs[:0]
	m.sidebarMachineGroups = len(remote) > 0
	if len(remote) == 0 {
		return here
	}

	here, global := stripGlobalRows(here)

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
		g.rows, g.header.WindowCount = stripGlobalRowsCounted(g.rows, g.header.WindowCount, &global)
		groups = append(groups, g)
	}

	byHost := make(map[string]machineGroup, len(groups))
	for _, g := range groups {
		byHost[g.header.Host] = g
	}
	ordered := make([]machineGroup, 0, len(groups))
	seen := make(map[string]bool, len(groups))
	for _, h := range m.FederationHosts {
		if g, ok := byHost[h.Name]; ok && !seen[h.Name] {
			ordered = append(ordered, g)
			seen[h.Name] = true
		}
	}
	for _, g := range groups {
		if !seen[g.header.Host] {
			ordered = append(ordered, g)
			seen[g.header.Host] = true
		}
	}
	groups = ordered

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

	out := make([]sessiontree.Node, 0, len(here)+len(remote)+2)
	if len(global) > 0 || m.GlobalSessionOffered() {
		header := m.globalGroupHeader(global)
		out = append(out, header)
		if !m.SidebarHostCollapsed(GlobalSessionName) {
			out = append(out, global...)
		}
	}
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

const sidebarGroupIndent = 2

func (m *OS) sidebarRowIndent() int {
	if m.sidebarMachineGroups {
		return sidebarGroupIndent
	}
	return 0
}

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

func (m *OS) sidebarSessionOrderFor(host string) []string {
	if host == m.attachedMachine() && m.sessionOrderFromDaemon(host) && !m.sessionOrderPending(host) {
		return nil
	}
	if host == federation.LocalHostName {
		return m.SidebarOrder
	}
	return m.SidebarHostSessionOrder[host]
}

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

func (m *OS) drawHostRow(
	node sessiontree.Node, cw, variant int, pal overlay.Palette, st sidebarRowState, canCreate, showCounts bool,
	isCursor func(kind sidebarRowKind, sessionID, windowID string) bool,
	recordHit func(kind sidebarRowKind, sessionID, windowID string, windowIndex, h int),
	recordToken func(tk sidebarTokenSpan, sessionID string),
	headerHoverX int,
	compose func(content string) string,
	lines *[]string,
) {
	if node.Global {
		collapsed := m.SidebarHostCollapsed(GlobalSessionName)
		st.Cursor = st.Cursor || isCursor(sidebarRowHost, GlobalSessionName, "")
		add := ""
		if !collapsed {
			labelW := sidebarHeaderLabelW(m.Settings.GetRailFoldOpenGlyph()+" "+node.Title, nil)
			if tok, span, ok := sidebarHeaderAdd(sidebarRowGlobalNew, cw, labelW, pal,
				headerHoverX, isCursor(sidebarRowGlobalNew, GlobalSessionName, ""), &m.Settings,
				sidebarRowBg(st, pal)); ok {
				add = tok
				recordToken(span, GlobalSessionName)
			}
		}
		recordHit(sidebarRowHost, GlobalSessionName, "", -1, 1)
		*lines = append(*lines, compose(st.mark(pal, m.sidebarHostRow(node, cw, pal, add, st, collapsed))))
		return
	}

	if node.Kind == sessiontree.KindHost {
		collapsed := m.SidebarHostCollapsed(node.Host)
		st.Cursor = st.Cursor || isCursor(sidebarRowHost, node.Host, "")
		add := ""
		if !collapsed && node.HostStatus == string(federation.StatusUp) {
			kind, id := sidebarRowHostNew, node.Host
			if node.Host == m.attachedMachine() {
				kind, id = sidebarRowNewSession, ""
			}
			labelW := sidebarHeaderLabelW(m.Settings.GetRailFoldOpenGlyph()+" "+node.Title, nil)
			if kind != sidebarRowNewSession || canCreate {
				if tok, span, ok := sidebarHeaderAdd(kind, cw, labelW, pal,
					headerHoverX, isCursor(kind, id, ""), &m.Settings,
					sidebarRowBg(st, pal)); ok {
					add = tok
					recordToken(span, id)
				}
			}
		}
		lit := false
		if node.HostStatus == string(federation.StatusApproval) {
			figure, _ := hostDownFigure(node, lipgloss.Width(printableTitle(node.Title)), cw, m.railClock())
			if w := lipgloss.Width(figure); w > 0 && cw-1-w > 0 {
				span := sidebarTokenSpan{Kind: sidebarRowHostSignIn, X0: cw - 1 - w, X1: cw - 1}
				lit = isCursor(sidebarRowHostSignIn, node.Host, "") || (headerHoverX >= span.X0 && headerHoverX < span.X1)
				recordToken(span, node.Host)
			}
		}
		recordHit(sidebarRowHost, node.Host, "", -1, 1)
		*lines = append(*lines, compose(st.mark(pal, m.sidebarHostRowLit(node, cw, pal, add, st, collapsed, lit))))
		return
	}

	st.Cursor = st.Cursor || isCursor(sidebarRowHostSession, node.Host, remoteSessionName(node))
	recordHit(sidebarRowHostSession, node.Host, remoteSessionName(node), -1, 1)
	*lines = append(*lines, compose(st.mark(pal, m.sidebarRemoteSessionRow(node, cw, variant, pal, st, showCounts))))
}

func (m *OS) createRemoteSession(host string) {
	m.cancelHostOpen()
	if !m.hostIsUp(host) {
		m.ShowNotification(host+" is unavailable", "warning", m.Settings.NotificationWarningDuration)
		return
	}
	m.clearSidebarReturn()
	if err := m.SwitchToHostSession(host, "", true); err != nil {
		m.ShowNotification(hostAttachRefusal(host, err), "error", m.Settings.NotificationDuration*3)
		return
	}
	m.applyStartupToUnarranged()
}

func (m *OS) localSessionIndexes(nodes []sessiontree.Node) map[string]int {
	indexes := make(map[string]int, len(nodes))
	for i, s := range m.railSessionOrder(nodes) {
		indexes[s.ID] = i + 1
	}
	return indexes
}

func localSessionNodes(nodes []sessiontree.Node) []sessiontree.Node {
	for i, n := range nodes {
		if !isRemoteNode(n) {
			continue
		}
		return nodes[:i]
	}
	return nodes
}

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
	case federation.StatusApproval:
		return "sign in"
	default:
		return "offline"
	}
}

func hostDownLabel(status string, lastOK int64, now time.Time) string {
	label := hostStatusLabel(status)
	switch federation.Status(status) {
	case federation.StatusNoDaemon, federation.StatusNoBinary, federation.StatusIncompatible, federation.StatusConnecting, federation.StatusApproval:
		return label
	}
	if lastOK > 0 {
		return inboxSeen(lastOK*int64(time.Second), now)
	}
	return label
}

func hostDownMark() string {
	if overlay.UseASCII() {
		return "x"
	}
	return "✕"
}

func hostDownFigure(node sessiontree.Node, nameW, cw int, now time.Time) (figure string, nameRoom int) {
	label := hostDownLabel(node.HostStatus, node.HostLastOK, now)
	forms := []string{label}
	last := hostDownMark()
	switch federation.Status(node.HostStatus) {
	case federation.StatusConnecting, federation.StatusReconnecting:
		last = ""
	case federation.StatusApproval:
		last = agentStateIndicator("needs_input")
	}
	if node.HostQueued > 0 {
		queued := strconv.Itoa(node.HostQueued) + " queued"
		forms = []string{queued + ", " + label, queued}
		last = queued
	}
	avail := sidebarNameAvail(cw, 0)
	for _, f := range forms {
		if keep, room := railRowFitRight(nameW, lipgloss.Width(f), avail); keep {
			return f, room
		}
	}
	if last == "" {
		return "", max(avail, 1)
	}
	return last, sidebarNameAvail(cw, lipgloss.Width(last))
}

func (m *OS) sidebarHostRow(node sessiontree.Node, cw int, pal overlay.Palette, add string, st sidebarRowState, collapsed bool) string {
	return m.sidebarHostRowLit(node, cw, pal, add, st, collapsed, false)
}

func (m *OS) sidebarHostRowLit(node sessiontree.Node, cw int, pal overlay.Palette, add string, st sidebarRowState, collapsed, lit bool) string {
	rowBg := sidebarRowBg(st, pal)
	up := node.HostStatus == string(federation.StatusUp)

	blocked, worst := m.hostAttention(node.Host)

	title := printableTitle(node.Title)
	right, rightW := "", 0
	nameRoom := 0
	switch {
	case !up:
		var label string
		label, nameRoom = hostDownFigure(node, lipgloss.Width(title), cw, m.railClock())
		ink := pal.FgMute
		if node.HostStatus == string(federation.StatusApproval) {
			ink = sidebarSeverityColor("needs_input", pal)
		}
		style := sidebarStyle(rowBg, ink)
		if lit {
			style = style.Underline(true)
		}
		right = style.Render(label)
	case blocked > 0:
		fig := strconv.Itoa(blocked) + agentStateIndicator(worst)
		right = sidebarStyle(rowBg, sidebarSeverityColor(worst, pal)).Render(fig)
		rightW = lipgloss.Width(fig)
	case collapsed && node.WindowCount > 0:
		count := strconv.Itoa(node.WindowCount)
		right = sidebarStyle(rowBg, pal.FgMute).Render(count)
		rightW = lipgloss.Width(count)
	case add != "":
		right = add
		rightW = lipgloss.Width(add)
	}

	here := node.Host == m.attachedMachine()
	ink := pal.FgDim
	if !up {
		ink = pal.FgMute
	}
	mark := m.Settings.GetRailFoldOpenGlyph()
	if collapsed {
		mark = m.Settings.GetRailFoldShutGlyph()
	}
	glyph := sidebarStyle(rowBg, pal.FgMute).Render(mark)
	if up {
		if fig, w := m.transfersHostFigure(node.Host, rowBg, pal); w > 0 {
			if right != "" {
				right = fig + sidebarStyle(rowBg, nil).Render(" ") + right
				rightW += w + 1
			} else {
				right, rightW = fig, w
			}
		}
		nameRoom = sidebarNameAvail(cw, rightW)
	}
	name := sidebarStyle(rowBg, ink).Render(overlay.Truncate(title, nameRoom))
	gutter := sidebarGutter(here && collapsed, "", rowBg, pal, &m.Settings)
	return sidebarComposeRuledRow(0, gutter, glyph, name, right, cw, rowBg, pal, &m.Settings)
}

func (m *OS) sidebarRemoteSessionRow(node sessiontree.Node, cw, variant int, pal overlay.Palette, st sidebarRowState, showCounts bool) string {
	rowBg := sidebarRowBg(st, pal)
	right, rightW := "", 0
	if m.Settings.SidebarShowCounts && showCounts && node.WindowCount > 0 && variant == sidebarVariantFull {
		count := strconv.Itoa(node.WindowCount)
		right = sidebarStyle(rowBg, pal.FgMute).Render(count)
		rightW = lipgloss.Width(count)
	}
	ink := pal.FgMute
	if m.hostIsUp(node.Host) {
		ink = pal.Fg
	}
	indent := m.sidebarRowIndent()
	title := printableTitle(node.Title)
	keepCount, nameRoom := railRowFitRight(lipgloss.Width(title), rightW, sidebarNameAvailIn(cw, 0, indent))
	if !keepCount {
		right = ""
	}
	name := sidebarStyle(rowBg, ink).Render(overlay.Truncate(title, nameRoom))
	gutter := sidebarStyle(rowBg, nil).Render(" ")
	glyph := sidebarStyle(rowBg, pal.FgMute).Render(m.Settings.GetRailBullet())
	if m.hostIsUp(node.Host) && agentStateIndicator(node.AgentState) != "" {
		glyph = sidebarGlyph(node.AgentState, node.DoneSeen, node.Subagents, rowBg, pal, &m.Settings)
	}
	return sidebarComposeGroupRow(indent, gutter, glyph, name, right, cw, rowBg)
}
