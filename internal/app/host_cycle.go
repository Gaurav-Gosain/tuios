package app

import (
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// Stepping between machines: next_host and prev_host.
//
// The keys move the client to the next or previous machine the way
// next_session and prev_session move it between sessions on one machine. They
// read only what the client already holds, the host snapshot the rail draws
// from, so a step costs no probe and nothing runs while the keys are idle. The
// switch itself goes through openSession, the path the rail, the switcher and
// last_session take.

// rememberShownOn records that this client showed the session name on host,
// so a later step back to that machine lands on it again.
func (m *OS) rememberShownOn(host, name string) {
	if host == "" || name == "" {
		return
	}
	if m.shownOn == nil {
		m.shownOn = make(map[string]string, 2)
	}
	m.shownOn[host] = name
}

// machineCycleOrder is the machines in the order the rail draws their groups:
// this machine first, then the [hosts] table in the daemon's order, overlaid
// with the order the user dragged the groups into.
func (m *OS) machineCycleOrder() []string {
	others := make([]string, 0, len(m.FederationHosts))
	for _, h := range m.FederationHosts {
		if h.Name != federation.LocalHostName {
			others = append(others, h.Name)
		}
	}
	others = orderByKey(others, func(s string) string { return s }, m.SidebarHostOrder)
	return append([]string{federation.LocalHostName}, others...)
}

// hostCycleSession is the session a step onto host lands on: the one this
// client last showed there, else the one attached there, else the first in the
// rail's order. It is "" when the host lists no sessions. A remembered session
// that is gone from the listing is passed over.
func (m *OS) hostCycleSession(host string) string {
	i := slices.IndexFunc(m.FederationHosts, func(h FederationHost) bool { return h.Name == host })
	if i < 0 {
		return ""
	}
	sessions := orderByKey(m.FederationHosts[i].Sessions, func(s FederationSession) string { return s.Name },
		m.sidebarSessionOrderFor(host))
	if len(sessions) == 0 {
		return ""
	}
	if want := m.shownOn[host]; want != "" &&
		slices.ContainsFunc(sessions, func(s FederationSession) bool { return s.Name == want }) {
		return want
	}
	if j := slices.IndexFunc(sessions, func(s FederationSession) bool { return s.Attached }); j >= 0 {
		return sessions[j].Name
	}
	return sessions[0].Name
}

// CycleHost switches to a session on the next (delta 1) or previous (delta -1)
// machine, wrapping at both ends. A machine whose link is not up, or that
// holds no session, is passed over. With one machine it says there is nowhere
// to go, as CycleSession does with one session.
func (m *OS) CycleHost(delta int) {
	order := m.machineCycleOrder()
	if len(order) < 2 {
		m.ShowNotification("No other machines", "info", m.Settings.NotificationDuration)
		return
	}
	n := len(order)
	cur := slices.Index(order, m.attachedMachine())
	if cur < 0 {
		// The attached machine left the table. Step from just outside it, so
		// next lands on the first machine and previous on the last.
		cur = -1
		if delta < 0 {
			cur = n
		}
	}
	for step := 1; step <= n; step++ {
		host := order[((cur+delta*step)%n+n)%n]
		if host == m.attachedMachine() || !m.hostIsUp(host) {
			continue
		}
		if name := m.hostCycleSession(host); name != "" {
			m.openSession(host, name)
			return
		}
	}
	m.ShowNotification("No other machine is up", "info", m.Settings.NotificationDuration)
}
