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
	// hostRefreshPushed is the backstop poll while the daemon pushes every
	// change to every host that is up. The push is what keeps the rail
	// current; this only catches what no event covers, such as a client
	// attaching on the host.
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
	// The push arrives on the attach connection. While the client is attached
	// to another machine that connection goes to the far daemon, not to the
	// one that pushes, so the rail keeps its poll.
	if m.federationPushed && m.AttachedHost == "" {
		return hostRefreshPushed, true
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
					Global      bool   `json:"global"`
					AgentState  string `json:"agent_state"`
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
		queued := map[string]int{}
		reports, pushes := hostStatusReports(client)
		msg.Pushed = pushes
		for _, h := range reports {
			lastOK[h.Host] = h.LastOK
			queued[h.Host] = h.Queued
			if h.Status == federation.StatusUp && h.Events != "live" {
				msg.Pushed = false
			}
		}
		for _, h := range res.Hosts {
			if h.Host != federation.LocalHostName {
				msg.Configured++
			}
			fh := FederationHost{Name: h.Host, Status: h.Status, Reason: h.Reason, LastOK: lastOK[h.Host], Queued: queued[h.Host]}
			for _, s := range h.Sessions {
				fh.Sessions = append(fh.Sessions, FederationSession{
					Name:        s.Name,
					DisplayName: s.DisplayName,
					WindowCount: s.WindowCount,
					Attached:    s.Attached,
					Global:      s.Global,
					AgentState:  s.AgentState,
				})
			}
			msg.Snapshot.Hosts = append(msg.Snapshot.Hosts, fh)
		}
		return msg
	}
}

// hostStatusReports fetches the last-contact times and the event modes the
// session listing does not carry, and whether the daemon pushes host changes. A
// failure here costs the rail a relative time and its push, and it polls as it
// always did.
func hostStatusReports(client *session.VerbClient) ([]federation.HostReport, bool) {
	raw, err := client.Call("list-hosts", nil)
	if err != nil {
		return nil, false
	}
	var res struct {
		Hosts      []federation.HostReport `json:"hosts"`
		EventsPush bool                    `json:"events_push"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return nil, false
	}
	return res.Hosts, res.EventsPush
}

// applyFederationSnapshot stores a snapshot the poll returned. It runs on the
// Update goroutine and does no I/O.
func (m *OS) applyFederationSnapshot(msg FederationHostsMsg) {
	m.federationPolling = msg.Configured > 0
	m.federationPushed = msg.Pushed
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
// A host that is not up keeps its header, carrying the reason or when it was
// last seen, and the rows of its last listing, which the daemon keeps and marks
// stale. That is section 7's rule on screen: the machine is still listed,
// greyed, rather than disappearing and leaving the user to wonder whether they
// imagined configuring it or what was running there. Its rows are muted and
// wear no agent glyph, and are not targets.
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
			HostQueued:  h.Queued,
			WindowCount: len(h.Sessions),
			// The machine's own glyph is the most urgent thing under it, so a
			// folded group still says whether anything in there wants a
			// person. That is the whole reason a header can be folded without
			// hiding an alarm.
			AgentState: hostWorstAgentState(h.Sessions),
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
				// DoneSeen is never set for another machine's row. The unread
				// bit says whether the person reading has looked at that pane,
				// and there is no way to look at one from here, so a finished
				// agent over there stays unread until that machine's own state
				// moves on.
			})
		}
	}
	return out
}

// hostAttention counts the machine's sessions that want a person, and names
// the most urgent state among them.
//
// A machine that is not up counts nothing. Its rows are the last listing that
// reached this client, and a figure drawn from them would be reporting what
// some panes were doing at a moment that has passed, on a machine nobody can
// reach to check.
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

// hostWorstAgentState is the most urgent state among a machine's sessions, by
// the ranking the rail draws with.
//
// A machine that did not answer has no sessions in the snapshot, so it returns
// empty and its header wears no glyph. That is the point rather than a side
// effect: a header that kept the last alarm it saw would be reporting a pane's
// state from a machine nobody can currently reach.
func hostWorstAgentState(sessions []FederationSession) string {
	best, state := 0, ""
	for _, s := range sessions {
		if r := sessiontree.AgentRank(s.AgentState, false); r > best {
			best, state = r, s.AgentState
		}
	}
	return state
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

// The global group: the sessions that hold panes from more than one machine.
//
// They are listed above the machines rather than under one. A global session
// is held by some daemon, because a session has to live somewhere, but where
// it is held is storage rather than meaning: its panes run wherever the user
// put them. Filing it under the machine holding it would say it belongs to
// that machine, which is the one thing it does not.

// stripGlobalRows takes the global sessions out of a machine's rows and
// returns them for the group above the machines.
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

// stripGlobalRowsCounted is stripGlobalRows for another machine's group, whose
// header carries its own count of the sessions under it. Taking rows out
// without taking them off the count leaves the header claiming sessions the
// group no longer shows.
func stripGlobalRowsCounted(rows []sessiontree.Node, count int, global *[]sessiontree.Node) ([]sessiontree.Node, int) {
	kept, found := stripGlobalRows(rows)
	if len(found) == 0 {
		return rows, count
	}
	*global = append(*global, found...)
	return kept, max(count-len(found), 0)
}

// isGlobalSessionRow reports whether a row is a global session's.
//
// The name is accepted as well as the mark. The mark is set when the session
// is created, so a global session created before the mark existed has only its
// name to say what it is, and the first one was called "global" exactly
// because that was the only way to say it at the time.
func isGlobalSessionRow(n sessiontree.Node) bool {
	if n.Kind != sessiontree.KindSession {
		return false
	}
	return n.Global || remoteSessionName(n) == GlobalSessionName
}

// globalGroupHeader is the row above the global sessions. It is shaped like a
// machine's header, which is what puts it at that level in the rail, and it
// folds like one.
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

// worstNodeAgentState is the most urgent agent state among some rows, so a
// folded group still says whether anything in it wants a person.
func worstNodeAgentState(rows []sessiontree.Node) string {
	best, state := 0, ""
	for _, n := range rows {
		if r := sessiontree.AgentRank(n.AgentState, n.DoneSeen); r > best {
			best, state = r, n.AgentState
		}
	}
	return state
}

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
	m.sidebarMachineGroups = len(remote) > 0
	if len(remote) == 0 {
		return here
	}

	// The global session is taken out of whichever machine's group holds it
	// and given a row of its own above them all. It is listed under a machine
	// because some daemon has to hold it, but that is where it is stored
	// rather than what it is: its panes run on several machines, so filing it
	// under one of them says the wrong thing.
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

	// Put the groups back into the table's order before anything else looks at
	// them.
	//
	// The attached machine's group is built from `here` rather than from the
	// listing, because the listing leaves it out: its sessions are the ones
	// already in hand. That made it the first group built, and therefore the
	// first group in the order below, so attaching a session on another machine
	// moved that machine up the rail and pushed the rest down. Which machine you
	// are attached to is not an ordering, and the rail should not reshuffle
	// underneath a switch.
	//
	// The table's order is the same for every client and does not depend on
	// where this one happens to be attached, so it is the stable base. A drag
	// order still wins, below.
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
	// A group the table does not name keeps its place after them. This machine
	// is the usual one, and it is pinned first anyway.
	for _, g := range groups {
		if !seen[g.header.Host] {
			ordered = append(ordered, g)
			seen[g.header.Host] = true
		}
	}
	groups = ordered

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

	out := make([]sessiontree.Node, 0, len(here)+len(remote)+2)
	// The global group goes above the machines. It is drawn when there is a
	// global session to show and also when there is not, because a group that
	// appears only once you have made one is a group you cannot use to make
	// one.
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

// sidebarGroupIndent is the step a machine's rows take under its heading. Two
// cells: one is not a step the eye reads at a glance, and three costs a
// twenty-eight column rail a word off every name for nothing the second cell
// does not already say.
const sidebarGroupIndent = 2

// sidebarRowIndent is the step the sessions section's rows take. It is the
// group step while the section is laid out by machine, and nothing at all
// otherwise, so the rail of a machine that stands alone is the rail it always
// was.
func (m *OS) sidebarRowIndent() int {
	if m.sidebarMachineGroups {
		return sidebarGroupIndent
	}
	return 0
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
	node sessiontree.Node, cw, variant int, pal overlay.Palette, st sidebarRowState, canCreate, showCounts bool,
	isCursor func(kind sidebarRowKind, sessionID, windowID string) bool,
	recordHit func(kind sidebarRowKind, sessionID, windowID string, windowIndex, h int),
	recordToken func(tk sidebarTokenSpan, sessionID string),
	headerHoverX int,
	compose func(content string) string,
	lines *[]string,
) {
	if node.Global {
		// The global group's header. It folds like a machine's, and its "+"
		// makes another global session rather than a session on a machine.
		collapsed := m.SidebarHostCollapsed(GlobalSessionName)
		st.Cursor = st.Cursor || isCursor(sidebarRowHost, GlobalSessionName, "")
		add := ""
		if !collapsed {
			labelW := sidebarHeaderLabelW(m.Settings.GetRailFoldOpenGlyph() + " " + node.Title)
			if tok, span, ok := sidebarHeaderAdd(sidebarRowGlobalNew, cw, labelW, pal,
				headerHoverX, isCursor(sidebarRowGlobalNew, GlobalSessionName, ""), &m.Settings,
				sidebarRowBg(st, pal)); ok {
				add = tok
				recordToken(span, GlobalSessionName)
			}
		}
		recordHit(sidebarRowHost, GlobalSessionName, "", -1, 1)
		*lines = append(*lines, compose(m.sidebarHostRow(node, cw, pal, add, st, collapsed)))
		return
	}

	if node.Kind == sessiontree.KindHost {
		collapsed := m.SidebarHostCollapsed(node.Host)
		st.Cursor = st.Cursor || isCursor(sidebarRowHost, node.Host, "")
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
					headerHoverX, isCursor(kind, id, ""), &m.Settings,
					sidebarRowBg(st, pal)); ok {
					add = tok
					recordToken(span, id)
				}
			}
		}
		recordHit(sidebarRowHost, node.Host, "", -1, 1)
		*lines = append(*lines, compose(m.sidebarHostRow(node, cw, pal, add, st, collapsed)))
		return
	}

	// A session row. It is a target only when its host is up.
	if m.hostIsUp(node.Host) {
		st.Cursor = st.Cursor || isCursor(sidebarRowHostSession, node.Host, remoteSessionName(node))
		recordHit(sidebarRowHostSession, node.Host, remoteSessionName(node), -1, 1)
	}
	*lines = append(*lines, compose(m.sidebarRemoteSessionRow(node, cw, variant, pal, st, showCounts)))
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

// hostDownLabel is the header word of a host that is not up. A machine that
// dropped off the network after answering says when it was last heard from,
// "seen 3m ago", since its rows under the header are that moment's listing. A
// machine that answered and refused says why instead, because that is a thing
// to fix: no daemon, no tuios, or the version.
func hostDownLabel(status string, lastOK int64, now time.Time) string {
	label := hostStatusLabel(status)
	switch federation.Status(status) {
	case federation.StatusNoDaemon, federation.StatusNoBinary, federation.StatusIncompatible, federation.StatusConnecting:
		return label
	}
	if lastOK > 0 {
		return inboxSeen(lastOK*int64(time.Second), now)
	}
	return label
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
func (m *OS) sidebarHostRow(node sessiontree.Node, cw int, pal overlay.Palette, add string, st sidebarRowState, collapsed bool) string {
	rowBg := sidebarRowBg(st, pal)
	up := node.HostStatus == string(federation.StatusUp)

	blocked, worst := m.hostAttention(node.Host)

	right, rightW := "", 0
	switch {
	case !up:
		// A host that is not up says why, in the slot the add control would take.
		// An unreachable machine has nothing to add a session to.
		label := hostDownLabel(node.HostStatus, node.HostLastOK, time.Now())
		// Mail waiting here for the machine is said first, in words: it is
		// the one thing on the row that will change when the link is back.
		if node.HostQueued > 0 {
			label = strconv.Itoa(node.HostQueued) + " queued, " + label
		}
		right = sidebarStyle(rowBg, pal.FgMute).Render(label)
		rightW = lipgloss.Width(label)
	case blocked > 0:
		// How many of this machine's sessions want a person, in the strip
		// badge's language. It outranks both the session count and the add
		// control, which is the rail's standing rule that an alarm outranks a
		// label: a count of sessions is a fact you can get by unfolding, and a
		// pane waiting for you is not.
		//
		// It is in this slot rather than in the fold mark's cell because that
		// cell says which way the group is folded, and that has to keep
		// working. A folded group is exactly when this figure matters most, so
		// taking the mark's place would have hidden it in the one state it was
		// added for.
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

	// A machine's name is secondary ink, and a machine that does not answer is
	// muted with its rows.
	//
	// This used to be the full ink and bold, which made the heading the loudest
	// thing on the rail. It is also the least actionable thing on the rail: you
	// do not act on a machine, you act on a session under it. Emphasis should
	// drain downward, so the session names are now the brightest ink and the
	// heading sits one step under them.
	//
	// The objection to demoting it was real: FgDim on a machine and FgDim on
	// the session under it are the same ink, and an ink step alone could not
	// carry the distinction. It is not carried by ink now. The rule that runs
	// out of the name to the right spine carries it, which is a different kind
	// of mark rather than a louder one, so a heading still reads as a heading
	// with colour switched off entirely.
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
	// No bold. The rail spends its one bold voice on a row that wants a human,
	// and a heading wearing the same weight as an alarm is what made that voice
	// stop meaning anything.
	name := sidebarStyle(rowBg, ink).Render(
		overlay.Truncate(printableTitle(node.Title), sidebarNameAvail(cw, rightW)))
	// A folded group hides the session row that wears the focus mark, so the
	// header takes it: the fold must not make the attached session vanish from
	// the rail without a trace.
	gutter := sidebarGutter(here && collapsed, "", rowBg, pal, &m.Settings)
	return sidebarComposeRuledRow(0, gutter, glyph, name, right, cw, rowBg, pal, &m.Settings)
}

// sidebarRemoteSessionRow draws one session that lives on a machine the client
// is not attached to. It sits on the same spine as a local session's row, with
// the resting mark in the glyph cell, so a machine's rows read the same
// whether the client is on it or not; what says it is elsewhere is the header
// above it.
//
// The ink follows the link. A row under a machine that is answering reads at
// the strength of a local resting row, because a click on it attaches the
// session and the row must not look weaker than what it does. A row under a
// machine that is not answering is a listing nobody can act on, and it is
// muted with its header to say so.
//
// The count takes the same gate a local session row's count takes: a rail too
// narrow for a name and a number keeps the name.
func (m *OS) sidebarRemoteSessionRow(node sessiontree.Node, cw, variant int, pal overlay.Palette, st sidebarRowState, showCounts bool) string {
	var rowBg color.Color
	if st.lit() {
		rowBg = pal.Surface
	}
	right, rightW := "", 0
	if m.Settings.SidebarShowCounts && showCounts && node.WindowCount > 0 && variant == sidebarVariantFull {
		count := strconv.Itoa(node.WindowCount)
		right = sidebarStyle(rowBg, pal.FgMute).Render(count)
		rightW = lipgloss.Width(count)
	}
	// The same ramp a local session row takes: the name is the brightest ink,
	// and it does not move when the row is lit because the band says that. A
	// machine that is not answering is the one exception, and it is muted with
	// its heading, because its rows are a cached listing rather than sessions
	// you can reach.
	ink := pal.FgMute
	if m.hostIsUp(node.Host) {
		ink = pal.Fg
	}
	indent := m.sidebarRowIndent()
	name := sidebarStyle(rowBg, ink).Render(
		overlay.Truncate(printableTitle(node.Title), sidebarNameAvailIn(cw, rightW, indent)))
	gutter := sidebarStyle(rowBg, nil).Render(" ")
	// The same glyph a session on this machine wears, from the same function.
	// The sessions section answers "who needs me", and a row that answered it
	// only for the machine the client happens to be attached to was answering
	// half the question: an agent waiting for a person waits just as long on
	// the build box.
	//
	// A machine that is not answering keeps the resting bullet whatever its
	// last listing said, because its rows are a cached listing rather than
	// sessions you can reach.
	glyph := sidebarStyle(rowBg, pal.FgMute).Render(m.Settings.GetRailBullet())
	if m.hostIsUp(node.Host) && agentStateIndicator(node.AgentState) != "" {
		glyph = sidebarGlyph(node.AgentState, node.DoneSeen, rowBg, pal, &m.Settings)
	}
	return sidebarComposeGroupRow(indent, gutter, glyph, name, right, cw, rowBg)
}
