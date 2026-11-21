package app

type sidebarScrollAnchor struct {
	SessionID string
	WindowID  string
	Offset    int
	Valid     bool
}

func (m *OS) sidebarReanchorAgents(agents []sidebarAgentEntry) {
	a := m.sidebarAgentAnchor
	if !a.Valid || a.Offset != m.SidebarScroll[sidebarSectionAgents] {
		return
	}
	for i, e := range agents {
		if e.WindowID == a.WindowID && e.SessionID == a.SessionID {
			m.SidebarScroll[sidebarSectionAgents] = i
			return
		}
	}
}

func (m *OS) sidebarRecordAgentAnchor(agents []sidebarAgentEntry, start, shown int) {
	if shown <= 0 || start <= 0 || start >= len(agents) {
		m.sidebarAgentAnchor = sidebarScrollAnchor{}
		return
	}
	e := agents[start]
	m.sidebarAgentAnchor = sidebarScrollAnchor{
		SessionID: e.SessionID,
		WindowID:  e.WindowID,
		Offset:    start,
		Valid:     true,
	}
}
