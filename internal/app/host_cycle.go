package app

import (
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

func (m *OS) rememberShownOn(host, name string) {
	if host == "" || name == "" {
		return
	}
	if m.shownOn == nil {
		m.shownOn = make(map[string]string, 2)
	}
	m.shownOn[host] = name
}

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
