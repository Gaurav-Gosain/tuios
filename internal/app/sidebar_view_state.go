package app

type secondaryRailView struct {
	hits           []sidebarRowHit
	nav            []sidebarNavRow
	sessionIDs     []string
	hostIDs        []string
	sectionY       [sidebarSectionCount][2]int
	stripRows      []sidebarStripRow
	scroll         [sidebarSectionCount]int
	agentAnchor    sidebarScrollAnchor
	agentsUnfolded bool
	subagentFold   map[string]bool
	reveal         sidebarRevealState
	sectionSplit   int
	split          sidebarSplitState
	splitGeom      sidebarSplitGeom
	filesShow      int8
	peek           string
	agentFilter    string
	agentSort      string
	hover          bool
	hoverX, hoverY int
	marqueeKey     string
	marqueeSeen    bool
	cursor         int
	cursorWant     sidebarNavRow
	cursorWantSet  bool
	drag           sidebarDragState
	edge           sidebarEdgeState
	shimmer        []shimmerSpan
}

func (m *OS) swapSecondaryRail() {
	s := &m.secondaryRailView
	m.SidebarHits, s.hits = s.hits, m.SidebarHits
	m.SidebarNav, s.nav = s.nav, m.SidebarNav
	m.SidebarSessionIDs, s.sessionIDs = s.sessionIDs, m.SidebarSessionIDs
	m.SidebarHostIDs, s.hostIDs = s.hostIDs, m.SidebarHostIDs
	m.sidebarSectionY, s.sectionY = s.sectionY, m.sidebarSectionY
	m.sidebarStripRows, s.stripRows = s.stripRows, m.sidebarStripRows
	m.SidebarScroll, s.scroll = s.scroll, m.SidebarScroll
	m.sidebarAgentAnchor, s.agentAnchor = s.agentAnchor, m.sidebarAgentAnchor
	m.sidebarAgentsUnfolded, s.agentsUnfolded = s.agentsUnfolded, m.sidebarAgentsUnfolded
	m.sidebarSubagentFold, s.subagentFold = s.subagentFold, m.sidebarSubagentFold
	m.sidebarReveal, s.reveal = s.reveal, m.sidebarReveal
	m.SidebarSectionSplit, s.sectionSplit = s.sectionSplit, m.SidebarSectionSplit
	m.sidebarSplit, s.split = s.split, m.sidebarSplit
	m.sidebarSplitGeom, s.splitGeom = s.splitGeom, m.sidebarSplitGeom
	m.filesView.Show, s.filesShow = s.filesShow, m.filesView.Show
	m.SidebarPeek, s.peek = s.peek, m.SidebarPeek
	m.SidebarAgentFilter, s.agentFilter = s.agentFilter, m.SidebarAgentFilter
	m.SidebarAgentSort, s.agentSort = s.agentSort, m.SidebarAgentSort
	m.SidebarHoverActive, s.hover = s.hover, m.SidebarHoverActive
	m.SidebarHoverX, s.hoverX = s.hoverX, m.SidebarHoverX
	m.SidebarHoverY, s.hoverY = s.hoverY, m.SidebarHoverY
	m.SidebarMarqueeKey, s.marqueeKey = s.marqueeKey, m.SidebarMarqueeKey
	m.sidebarMarqueeSeen, s.marqueeSeen = s.marqueeSeen, m.sidebarMarqueeSeen
	m.SidebarCursor, s.cursor = s.cursor, m.SidebarCursor
	m.sidebarCursorWant, s.cursorWant = s.cursorWant, m.sidebarCursorWant
	m.sidebarCursorWantSet, s.cursorWantSet = s.cursorWantSet, m.sidebarCursorWantSet
	m.SidebarDrag, s.drag = s.drag, m.SidebarDrag
	m.SidebarEdge, s.edge = s.edge, m.SidebarEdge
	m.motion.rail, s.shimmer = s.shimmer, m.motion.rail
}
