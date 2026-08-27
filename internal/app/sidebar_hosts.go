package app

import (
	"encoding/json"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// Federation stage 1 in the client: the rail groups the sessions of other
// machines under a host header, below this machine's own sessions.
//
// The one rule this file exists to keep is that no part of it touches the
// network from the Update goroutine. The daemon holds the links; the client
// asks it for a snapshot inside a tea.Cmd, stores what comes back, and the rail
// renders from the stored snapshot alone. A host that is powered off cannot
// slow a frame down, because a frame never waits on one.
//
// A client whose daemon has no hosts configured stops polling after the first
// answer, so the default install pays one verb call at attach and nothing after.

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
// described it. Nothing here is addressable from this client: stage 1 carries
// listings, so these rows are shown and never selected.
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
type FederationRefreshTickMsg struct{}

func federationRefreshTick(after time.Duration) tea.Cmd {
	return tea.Tick(after, func(time.Time) tea.Msg { return FederationRefreshTickMsg{} })
}

// federationRefreshPlan decides the next poll interval and whether to poll at
// all. Polling stops for good once the daemon reports no hosts, which is the
// default install.
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

		// list-host-sessions carries the local machine as its first entry. The
		// rail already draws local sessions from live state, so that entry is
		// dropped here rather than drawn twice.
		msg := FederationHostsMsg{}
		lastOK := map[string]int64{}
		for _, h := range hostStatusReports(client) {
			lastOK[h.Host] = h.LastOK
		}
		for _, h := range res.Hosts {
			if h.Host == federation.LocalHostName {
				continue
			}
			msg.Configured++
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

// hostGroupNodes turns the stored snapshot into the rows the rail draws: one
// header per host, then that host's sessions.
//
// A host that is not up contributes its header alone, carrying the reason. That
// is section 7's rule on screen: the machine is still listed, greyed, with when
// it was last seen, rather than disappearing and leaving the user to wonder
// whether they imagined configuring it.
func (m *OS) hostGroupNodes() []sessiontree.Node {
	if len(m.FederationHosts) == 0 {
		return nil
	}
	out := make([]sessiontree.Node, 0, len(m.FederationHosts)*2)
	for _, h := range m.FederationHosts {
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
		for _, s := range h.Sessions {
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

// isRemoteNode reports whether a tree node belongs to another machine. Every
// interactive path in the rail checks it: a remote row is drawn and never
// clicked, dragged, renamed, deleted or switched to, because no write and no
// attach crosses a link in this release.
func isRemoteNode(n sessiontree.Node) bool {
	return n.Kind == sessiontree.KindHost || n.Host != ""
}

// localSessionNodes drops the host groups from a tree's session list. The
// surfaces that only deal with this machine (the colour arbitration, the
// collapsed glyph strip) read the tree through it.
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
	case federation.StatusIncompatible:
		return "version"
	case federation.StatusConnecting:
		return "connecting"
	default:
		return "offline"
	}
}

// sidebarHostRow draws a federated row: a host group header, or one of that
// host's sessions under it.
//
//	@ build              2
//	    api              3
//	  @ work       offline
//
// Everything here is muted. A remote row is a listing and not a place the user
// can go, and drawing it at the strength of a local row would promise a click
// this release does not carry. A host that is not answering keeps its row with
// one word saying why, because a machine that vanished from the rail reads as a
// machine nobody configured.
//
// The mark is "@" in both glyph modes rather than a nerd font icon: it is the
// character a machine address already carries, it is one cell wide in every
// font, and the rail's other marks are about panes rather than machines.
func (m *OS) sidebarHostRow(node sessiontree.Node, cw int, pal overlay.Palette) string {
	if node.Kind != sessiontree.KindHost {
		return m.sidebarRemoteSessionRow(node, cw, pal)
	}

	right, rightW := "", 0
	if label := hostStatusLabel(node.HostStatus); label != "" {
		right = sidebarStyle(nil, pal.FgMute).Render(label)
		rightW = lipgloss.Width(label)
	} else if config.SidebarShowCounts && node.WindowCount > 0 {
		count := strconv.Itoa(node.WindowCount)
		right = sidebarStyle(nil, pal.FgMute).Render(count)
		rightW = lipgloss.Width(count)
	}

	ink := pal.FgDim
	if node.HostStatus != string(federation.StatusUp) {
		ink = pal.FgMute
	}
	glyph := sidebarStyle(nil, pal.FgMute).Render("@")
	name := sidebarStyle(nil, ink).Render(
		overlay.Truncate(printableTitle(node.Title), sidebarNameAvail(cw, rightW)))
	gutter := sidebarStyle(nil, nil).Render(" ")
	return sidebarComposeRow(gutter, glyph, name, right, cw, nil)
}

// sidebarRemoteSessionRow draws one session that lives on another machine.
func (m *OS) sidebarRemoteSessionRow(node sessiontree.Node, cw int, pal overlay.Palette) string {
	right, rightW := "", 0
	if config.SidebarShowCounts && node.WindowCount > 0 {
		count := strconv.Itoa(node.WindowCount)
		right = sidebarStyle(nil, pal.FgMute).Render(count)
		rightW = lipgloss.Width(count)
	}
	// Indented one cell under its host header, which is the only thing that
	// says these rows belong to the machine above them.
	avail := max(sidebarNameAvail(cw, rightW)-1, 1)
	name := sidebarStyle(nil, pal.FgMute).Render(
		" " + overlay.Truncate(printableTitle(node.Title), avail))
	gutter := sidebarStyle(nil, nil).Render(" ")
	glyph := sidebarStyle(nil, nil).Render(" ")
	return sidebarComposeRow(gutter, glyph, name, right, cw, nil)
}
