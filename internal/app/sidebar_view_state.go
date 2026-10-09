package app

// secondaryRailView holds presentation state that belongs to the opt-in rail,
// not to the original one. The original rail's fields stay on OS so existing
// session persistence and keyboard controls retain their legacy meaning.
// swapSecondaryRail is used only on the UI goroutine, around a synchronous
// draw or input gesture; it never spans an asynchronous command.
type secondaryRailView struct {
	hits             []sidebarRowHit
	nav              []sidebarNavRow
	sessionIDs       []string
	hostIDs          []string
	sectionY         [sidebarSectionCount][2]int
	stripRows        []sidebarStripRow
	scrollS, scrollT int
	scrollA, scrollF int
	scrollG, scrollC int
	agentAnchor      sidebarScrollAnchor
	agentsUnfolded   bool
	reveal           sidebarRevealState
	sectionSplit     int
	split            sidebarSplitState
	splitGeom        sidebarSplitGeom
	filesShow        int8
	peek             string
	agentFilter      string
	agentSort        string
	hover            bool
	hoverX, hoverY   int
	marqueeKey       string
	marqueeSeen      bool
	cursor           int
	drag             sidebarDragState
	edge             sidebarEdgeState
	shimmer          []shimmerSpan
}

func (m *OS) swapSecondaryRail() {
	s := &m.secondaryRailView
	m.SidebarHits, s.hits = s.hits, m.SidebarHits
	m.SidebarNav, s.nav = s.nav, m.SidebarNav
	m.SidebarSessionIDs, s.sessionIDs = s.sessionIDs, m.SidebarSessionIDs
	m.SidebarHostIDs, s.hostIDs = s.hostIDs, m.SidebarHostIDs
	m.sidebarSectionY, s.sectionY = s.sectionY, m.sidebarSectionY
	m.sidebarStripRows, s.stripRows = s.stripRows, m.sidebarStripRows
	m.SidebarScrollS, s.scrollS = s.scrollS, m.SidebarScrollS
	m.SidebarScrollT, s.scrollT = s.scrollT, m.SidebarScrollT
	m.SidebarScrollA, s.scrollA = s.scrollA, m.SidebarScrollA
	m.SidebarScrollF, s.scrollF = s.scrollF, m.SidebarScrollF
	m.SidebarScrollG, s.scrollG = s.scrollG, m.SidebarScrollG
	m.SidebarScrollC, s.scrollC = s.scrollC, m.SidebarScrollC
	m.sidebarAgentAnchor, s.agentAnchor = s.agentAnchor, m.sidebarAgentAnchor
	m.sidebarAgentsUnfolded, s.agentsUnfolded = s.agentsUnfolded, m.sidebarAgentsUnfolded
	m.sidebarReveal, s.reveal = s.reveal, m.sidebarReveal
	m.SidebarSectionSplit, s.sectionSplit = s.sectionSplit, m.SidebarSectionSplit
	m.sidebarSplit, s.split = s.split, m.sidebarSplit
	m.sidebarSplitGeom, s.splitGeom = s.splitGeom, m.sidebarSplitGeom
	// Both edges read the same focused pane's directory and repository. The
	// listing is shared (one daemon watch per client), while each edge keeps
	// its own local files visibility switch and scroll/cursor state.
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
	m.SidebarDrag, s.drag = s.drag, m.SidebarDrag
	m.SidebarEdge, s.edge = s.edge, m.SidebarEdge
	m.motion.rail, s.shimmer = s.shimmer, m.motion.rail
}
