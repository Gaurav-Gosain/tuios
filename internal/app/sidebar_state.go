package app

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/adrg/xdg"
)

var sidebarStateDir = func() string {
	return filepath.Join(xdg.StateHome, "tuios")
}

const sidebarStateFileName = "sidebar.json"

type sidebarStateFile struct {
	Order                  []string            `json:"order,omitempty"`
	Width                  int                 `json:"width,omitempty"`
	DragWidth              bool                `json:"drag_width,omitempty"`
	Accents                map[string]int      `json:"accents,omitempty"`
	AccentColors           map[string]string   `json:"accent_colors,omitempty"`
	AgentSeen              map[string]bool     `json:"agent_seen,omitempty"`
	AgentSeenSeq           map[string]uint64   `json:"agent_seen_seq,omitempty"`
	AgentSeenAt            map[string]int64    `json:"agent_seen_at,omitempty"`
	AgentViewed            map[string]int64    `json:"agent_viewed,omitempty"`
	AgentsFilter           string              `json:"agents_filter,omitempty"`
	AgentsSort             string              `json:"agents_sort,omitempty"`
	AgentsSeen             bool                `json:"agents_seen,omitempty"`
	SpawnPreset            *spawnPreset        `json:"spawn_preset,omitempty"`
	AgentNoticesDismissed  []string            `json:"agent_notices_dismissed,omitempty"`
	MachineOffersNever     []string            `json:"machine_offers_never,omitempty"`
	Collapsed              bool                `json:"collapsed,omitempty"`
	SectionSplit           int                 `json:"section_split,omitempty"`
	ReposCollapsed         []string            `json:"repos_collapsed,omitempty"`
	AgentsFolds            map[string]bool     `json:"agents_folds,omitempty"`
	AgentsPins             []string            `json:"agents_pins,omitempty"`
	BoardList              bool                `json:"board_list,omitempty"`
	AgentsList             string              `json:"agents_list,omitempty"`
	AgentsListWidth        int                 `json:"agents_list_width,omitempty"`
	AgentsCardPreviewWidth int                 `json:"agents_card_preview_width,omitempty"`
	AgentsSideHidden       bool                `json:"agents_side_hidden,omitempty"`
	AgentsGroup            string              `json:"agents_group,omitempty"`
	HostsOrder             []string            `json:"hosts_order,omitempty"`
	HostsCollapsed         []string            `json:"hosts_collapsed,omitempty"`
	HostSessionOrder       map[string][]string `json:"host_session_order,omitempty"`
	Socket                 string              `json:"socket,omitempty"`
}

func (m *OS) loadSidebarState() {
	data, err := os.ReadFile(filepath.Join(sidebarStateDir(), sidebarStateFileName))
	if err != nil {
		return
	}
	var st sidebarStateFile
	if jsonx.Unmarshal(data, &st) != nil {
		return
	}
	if len(st.Order) > 0 {
		m.SidebarOrder = st.Order
	}
	if a := accentsFromFile(st); len(a) > 0 {
		m.SidebarAccents = a
	}
	if len(st.AgentSeen) > 0 {
		m.SidebarAgentSeen = st.AgentSeen
	}
	if len(st.AgentSeenSeq) > 0 {
		m.SidebarAgentSeenSeq = st.AgentSeenSeq
	}
	if len(st.AgentSeenAt) > 0 {
		m.SidebarAgentSeenAt = st.AgentSeenAt
	}
	if len(st.AgentViewed) > 0 {
		m.SidebarAgentViewed = st.AgentViewed
	}
	m.SidebarAgentFilter, m.SidebarAgentSort = st.AgentsFilter, st.AgentsSort
	m.SidebarAgentsSeen = st.AgentsSeen
	m.Inbox.ck.spawnPreset = st.SpawnPreset
	m.Inbox.ck.ckFoldsFromFile(st.AgentsFolds)
	m.Inbox.ck.ckPinsFromFile(st.AgentsPins)
	m.Inbox.ck.boardList = st.BoardList
	if st.AgentsList == config.AgentsViewListColumn || st.AgentsList == config.AgentsViewListStrip {
		m.Inbox.ck.listMode = st.AgentsList
	}
	m.Inbox.ck.listWidth = max(st.AgentsListWidth, 0)
	m.Inbox.ck.kb.pv.width = max(st.AgentsCardPreviewWidth, 0)
	m.Inbox.ck.sideHidden = st.AgentsSideHidden
	switch st.AgentsGroup {
	case ckGroupSession, ckGroupMachine, ckGroupProject:
		m.Inbox.ck.groupBy = st.AgentsGroup
	}
	if len(st.AgentNoticesDismissed) > 0 {
		m.agentNoticesDismissed = make(map[string]bool, len(st.AgentNoticesDismissed))
		for _, k := range st.AgentNoticesDismissed {
			m.agentNoticesDismissed[k] = true
		}
	}
	if len(st.MachineOffersNever) > 0 {
		m.machineOffersNever = make(map[string]bool, len(st.MachineOffersNever))
		for _, k := range st.MachineOffersNever {
			m.machineOffersNever[k] = true
		}
	}
	m.SidebarCollapsed = st.Collapsed
	if st.SectionSplit >= sidebarSplitMin && st.SectionSplit <= sidebarSplitMax {
		m.SidebarSectionSplit = st.SectionSplit
	}
	if len(st.ReposCollapsed) > 0 {
		m.SidebarCollapsedRepos = make(map[string]bool, len(st.ReposCollapsed))
		for _, repo := range st.ReposCollapsed {
			m.SidebarCollapsedRepos[repo] = true
		}
	}
	if len(st.HostsOrder) > 0 {
		m.SidebarHostOrder = st.HostsOrder
	}
	if len(st.HostsCollapsed) > 0 {
		m.SidebarCollapsedHosts = make(map[string]bool, len(st.HostsCollapsed))
		for _, host := range st.HostsCollapsed {
			m.SidebarCollapsedHosts[host] = true
		}
	}
	if len(st.HostSessionOrder) > 0 {
		m.SidebarHostSessionOrder = st.HostSessionOrder
	}
	m.sidebarStateSocket = st.Socket
	if st.Width >= config.SidebarGlyphWidth && !legacyUndraggedWidth(st) {
		m.SidebarWidthPref = st.Width
	}
}

const legacySidebarDefaultWidth = 28

func legacyUndraggedWidth(st sidebarStateFile) bool {
	return !st.DragWidth && st.Width == legacySidebarDefaultWidth
}

func (m *OS) saveSidebarState() {
	dir := sidebarStateDir()
	if os.MkdirAll(dir, 0o750) != nil {
		return
	}
	if data, err := os.ReadFile(filepath.Join(dir, sidebarStateFileName)); err == nil {
		var disk sidebarStateFile
		if jsonx.Unmarshal(data, &disk) == nil {
			for _, k := range disk.AgentNoticesDismissed {
				if m.agentNoticesDismissed == nil {
					m.agentNoticesDismissed = map[string]bool{}
				}
				m.agentNoticesDismissed[k] = true
			}
			for _, k := range disk.MachineOffersNever {
				if m.machineOffersRemoved[k] {
					continue
				}
				if m.machineOffersNever == nil {
					m.machineOffersNever = map[string]bool{}
				}
				m.machineOffersNever[k] = true
			}
		}
	}
	slots, colors := accentsToFile(m.SidebarAccents)
	data, err := jsonx.Marshal(sidebarStateFile{
		Order:                  m.SidebarOrder,
		Width:                  m.SidebarWidthPref,
		DragWidth:              true,
		Accents:                slots,
		AccentColors:           colors,
		AgentSeen:              m.SidebarAgentSeen,
		AgentSeenSeq:           m.SidebarAgentSeenSeq,
		AgentSeenAt:            m.SidebarAgentSeenAt,
		AgentViewed:            m.SidebarAgentViewed,
		AgentsFilter:           m.SidebarAgentFilter,
		AgentsSort:             m.SidebarAgentSort,
		AgentsSeen:             m.SidebarAgentsSeen,
		SpawnPreset:            m.Inbox.ck.spawnPreset,
		AgentsFolds:            m.Inbox.ck.ckFoldsToFile(),
		AgentsPins:             m.Inbox.ck.ckPinsToFile(),
		BoardList:              m.Inbox.ck.boardList,
		AgentsList:             m.Inbox.ck.listMode,
		AgentsListWidth:        m.Inbox.ck.listWidth,
		AgentsCardPreviewWidth: m.Inbox.ck.kb.pv.width,
		AgentsSideHidden:       m.Inbox.ck.sideHidden,
		AgentsGroup:            m.Inbox.ck.groupBy,
		AgentNoticesDismissed:  collapsedRepoList(m.agentNoticesDismissed),
		MachineOffersNever:     collapsedRepoList(m.machineOffersNever),
		Collapsed:              m.SidebarCollapsed,
		SectionSplit:           m.SidebarSectionSplit,
		ReposCollapsed:         collapsedRepoList(m.SidebarCollapsedRepos),
		HostsOrder:             m.SidebarHostOrder,
		HostsCollapsed:         collapsedRepoList(m.SidebarCollapsedHosts),
		HostSessionOrder:       m.SidebarHostSessionOrder,
		Socket:                 m.sidebarStateSocket,
	})
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, sidebarStateFileName), data, 0o600)
}

func collapsedRepoList(repos map[string]bool) []string {
	if len(repos) == 0 {
		return nil
	}
	out := make([]string, 0, len(repos))
	for repo, collapsed := range repos {
		if collapsed {
			out = append(out, repo)
		}
	}
	sort.Strings(out)
	return out
}

func (m *OS) ownsSidebarState() bool {
	socket, err := session.GetSocketPath()
	if err != nil || socket == "" {
		return false
	}
	if m.sidebarStateSocket == "" {
		m.sidebarStateSocket = socket
		m.saveSidebarState()
		return true
	}
	return m.sidebarStateSocket == socket
}

func (m *OS) pruneWindowKeyedState() {
	if len(m.SidebarAccents) == 0 && len(m.SidebarAgentSeen) == 0 && len(m.SidebarAgentSeenSeq) == 0 && len(m.SidebarAgentSeenAt) == 0 && len(m.SidebarAgentViewed) == 0 {
		return
	}
	known, ok := m.knownWindowIDs()
	if !ok {
		return
	}
	changed := false
	for id := range m.SidebarAccents {
		if !known[id] {
			delete(m.SidebarAccents, id)
			changed = true
		}
	}
	for id := range m.SidebarAgentSeen {
		if !known[id] {
			delete(m.SidebarAgentSeen, id)
			changed = true
		}
	}
	for id := range m.SidebarAgentSeenSeq {
		if !known[id] {
			delete(m.SidebarAgentSeenSeq, id)
			changed = true
		}
	}
	for id := range m.SidebarAgentSeenAt {
		if !known[id] {
			delete(m.SidebarAgentSeenAt, id)
			changed = true
		}
	}
	for id := range m.SidebarAgentViewed {
		if !known[id] {
			delete(m.SidebarAgentViewed, id)
			changed = true
		}
	}
	if changed {
		m.saveSidebarState()
	}
}

func (m *OS) knownWindowIDs() (map[string]bool, bool) {
	if m.DaemonClient == nil || !m.ownsSidebarState() {
		return nil, false
	}
	known := make(map[string]bool, len(m.Windows))
	for _, w := range m.Windows {
		if w != nil {
			known[w.ID] = true
		}
	}
	attached := false
	for _, s := range m.DaemonClient.CachedSessions() {
		if len(s.Windows) != s.WindowCount {
			return nil, false
		}
		if s.Name == m.SessionName {
			attached = true
		}
		for _, w := range s.Windows {
			known[w.ID] = true
		}
	}
	if !attached {
		return nil, false
	}
	return known, true
}

func accentsFromFile(st sidebarStateFile) map[string]Accent {
	out := make(map[string]Accent, len(st.Accents)+len(st.AccentColors))
	for id, idx := range st.Accents {
		if idx < 0 || idx >= accentSwatchCount {
			continue
		}
		out[id] = SlotAccent(idx)
	}
	for id, hex := range st.AccentColors {
		if c, ok := parseHexColor(hex); ok {
			out[id] = RGBAccent(c)
		}
	}
	return out
}

func accentsToFile(accents map[string]Accent) (map[string]int, map[string]string) {
	if len(accents) == 0 {
		return nil, nil
	}
	var slots map[string]int
	var colors map[string]string
	for id, a := range accents {
		if a.IsSlot() {
			if slots == nil {
				slots = make(map[string]int, len(accents))
			}
			slots[id] = a.Slot
			continue
		}
		if colors == nil {
			colors = make(map[string]string, len(accents))
		}
		colors[id] = a.Hex()
	}
	return slots, colors
}

func orderByKey[T any](items []T, key func(T) string, order []string) []T {
	if len(order) == 0 || len(items) < 2 {
		return items
	}
	rank := make(map[string]int, len(order))
	for i, k := range order {
		if _, ok := rank[k]; !ok {
			rank[k] = i
		}
	}
	out := append([]T(nil), items...)
	sort.SliceStable(out, func(a, b int) bool {
		ra, oka := rank[key(out[a])]
		rb, okb := rank[key(out[b])]
		switch {
		case oka && okb:
			return ra < rb
		case oka:
			return true
		default:
			return false
		}
	})
	return out
}
