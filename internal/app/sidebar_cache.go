package app

import (
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type sidebarRenderCache struct {
	valid      bool
	sig        uint64
	lines      []string
	panel      string
	w          int
	hits       []sidebarRowHit
	sessionIDs []string
	hostIDs    []string
	nav        []sidebarNavRow
	sections   [sidebarSectionCount][2]int
	stripRows  []sidebarStripRow
	shimmer    []shimmerSpan
}

func (c *sidebarRenderCache) invalidate() { c.valid = false }

func (m *OS) sidebarPanel() (string, int) {
	lines, w := m.sidebarPanelLines()
	if lines == nil {
		return "", w
	}
	if m.sidebarCache.valid && m.sidebarCache.panel != "" {
		return m.sidebarCache.panel, w
	}
	panel := strings.Join(lines, "\n")
	if m.sidebarCache.valid {
		m.sidebarCache.panel = panel
	}
	return panel, w
}

var railTimeNow = time.Now

func (m *OS) railClock() time.Time {
	if m.railNow.IsZero() {
		return railTimeNow()
	}
	return m.railNow
}

func (m *OS) sidebarPanelLines() ([]string, int) {
	m.railNow = railTimeNow()
	defer func() { m.railNow = time.Time{} }()
	animating := m.SidebarMarqueeKey != "" || m.SidebarDrag.Dragging
	var ext railGens
	if railCheckPath != "" {
		ext = m.railExternalGens()
	}
	sig := m.sidebarSignature()
	if !animating && m.sidebarCache.valid && m.sidebarCache.sig == sig {
		m.checkRailCache(ext)
		m.SidebarHits = append(m.SidebarHits[:0], m.sidebarCache.hits...)
		m.SidebarSessionIDs = append(m.SidebarSessionIDs[:0], m.sidebarCache.sessionIDs...)
		m.SidebarHostIDs = append(m.SidebarHostIDs[:0], m.sidebarCache.hostIDs...)
		m.SidebarNav = append(m.SidebarNav[:0], m.sidebarCache.nav...)
		m.sidebarSectionY = m.sidebarCache.sections
		m.sidebarStripRows = append(m.sidebarStripRows[:0], m.sidebarCache.stripRows...)
		m.motion.rail = append(m.motion.rail[:0], m.sidebarCache.shimmer...)
		return m.sidebarCache.lines, m.sidebarCache.w
	}

	m.tickStats.Rail++
	lines, w := m.sidebarPanelLinesForTree(m.BuildSessionTree())

	m.sidebarCache = sidebarRenderCache{
		valid:      !animating,
		sig:        sig,
		lines:      lines,
		w:          w,
		hits:       append([]sidebarRowHit(nil), m.SidebarHits...),
		sessionIDs: append([]string(nil), m.SidebarSessionIDs...),
		hostIDs:    append([]string(nil), m.SidebarHostIDs...),
		nav:        append([]sidebarNavRow(nil), m.SidebarNav...),
		sections:   m.sidebarSectionY,
		stripRows:  append([]sidebarStripRow(nil), m.sidebarStripRows...),
		shimmer:    append([]shimmerSpan(nil), m.motion.rail...),
	}
	return lines, w
}

func (m *OS) sidebarSignature() uint64 {
	const prime = 1099511628211
	h := uint64(1469598103934665603)
	mixU := func(v uint64) {
		for range 8 {
			h ^= v & 0xff
			h *= prime
			v >>= 8
		}
	}
	mixI := func(v int) { mixU(uint64(v)) }
	mixB := func(b bool) {
		if b {
			mixU(1)
		} else {
			mixU(2)
		}
	}
	mixS := func(s string) {
		mixU(uint64(len(s)))
		for i := range len(s) {
			h ^= uint64(s[i])
			h *= prime
		}
	}

	mixU(m.host.gen)
	mixU(theme.Generation())

	mixI(m.GetSidebarWidth())
	mixI(m.ViewUsableHeight())
	mixI(m.viewReserve().Top)
	mixI(m.GetRenderWidth())
	mixS(m.Settings.SidebarPosition)
	mixS(m.Settings.SidebarSections)
	mixI(m.SidebarSectionSplit)
	mixS(m.Settings.SidebarAgentRow.Fingerprint)
	mixB(m.sidebarSplit.Active)
	mixB(m.Settings.SidebarShowGlyphs)
	mixB(m.Settings.SidebarShowCounts)
	mixU(m.AgentMail.Gen)
	mixU(m.Inbox.Gen)
	mixB(m.Inbox.Live)

	mixB(m.Settings.UseASCIIOnly)
	mixB(m.Settings.NoNerdFont)
	mixS(m.Settings.BorderStyle)

	mixI(m.SidebarScroll[sidebarSectionSessions])
	mixI(m.SidebarScroll[sidebarSectionTerminals])
	mixI(m.SidebarScroll[sidebarSectionAgents])
	mixB(m.sidebarAgentAnchor.Valid)
	mixI(m.sidebarAgentAnchor.Offset)
	mixS(m.sidebarAgentAnchor.SessionID)
	mixS(m.sidebarAgentAnchor.WindowID)
	mixI(m.FocusedWindow)
	mixB(m.SidebarHoverActive)
	mixI(m.SidebarHoverX)
	mixI(m.SidebarHoverY)

	mixS(m.SidebarPeek)

	mixS(m.Settings.SidebarSections)
	mixB(m.Settings.SidebarFileIcons)
	mixB(m.Settings.SidebarFileIconColors)
	mixI(int(m.filesView.Show))
	mixS(m.filesView.Dir)
	mixS(m.filesView.Origin)
	mixS(m.filesView.Host)
	mixS(m.filesView.Err)
	mixB(m.filesView.Loading)
	mixI(m.SidebarScroll[sidebarSectionFiles])
	mixU(m.filesView.Gen)
	mixS(m.fileDragHeldKey())
	mixS(m.Settings.SidebarFilesStyle)
	mixU(m.filesTree.gen)
	if m.filesTreeOn() {
		mixS(m.sidebarCurrentSessionID())
	}
	if m.sidebarDrawing {
		mixU(m.railCustomSecond.gen)
	} else {
		mixU(m.railCustom.gen)
	}
	mixI(m.SidebarScroll[sidebarSectionCustom])
	mixU(m.railPaneView.gen)
	mixB(m.RailPaneFocused())
	mixB(m.railPaneView.stopped)
	mixS(m.railPaneView.err)
	mixS(m.railPaneView.windowID)
	if !m.sidebarDrawing && m.UserConfig != nil {
		pane := m.railPaneConfig()
		mixS(pane.Command)
		mixS(pane.Title)
		mixI(pane.Height)
	}
	mixI(m.SidebarScroll[sidebarSectionGit])
	mixU(m.gitView.gen)

	mixB(m.agentsOn())
	mixS(m.sidebarAgentsFilter())
	mixS(m.sidebarAgentsSort())
	mixB(m.sidebarAgentsUnfolded)
	mixI(int(m.Settings.SidebarAgentRestFold / time.Second))
	if m.Settings.SidebarAgentRestFold > 0 && m.SidebarAgentsSeen {
		mixI(int(sidebarFoldClock().Unix() / 60))
	}
	sv := m.Settings.SidebarSubagents
	mixB(sv.Rows)
	mixB(sv.Tree)
	mixS(sv.Collapse)
	mixB(sv.Fields.Mark)
	mixB(sv.Fields.Name)
	mixB(sv.Fields.Type)
	mixB(sv.Fields.Time)
	mixB(sv.Fields.Note)
	mixI(sv.MaxRows)
	mixB(sv.Hide)
	mixI(int(sv.HideAfter / time.Millisecond))
	mixI(m.sidebarSubagentFoldGen)
	var hideNow time.Time
	if sv.Hide {
		hideNow = time.Now()
	}

	mixB(m.sidebarRailFocused())
	mixI(m.SidebarCursor)
	mixB(m.sidebarCursorWantSet)
	if target, ok := m.sidebarCursorTarget(); ok && m.sidebarRailFocused() {
		mixI(int(target.Kind))
		mixS(target.SessionID)
		mixS(target.WindowID)
	} else {
		mixI(-1)
	}

	mixB(m.Settings.SidebarShowNumbers)
	mixS(m.Settings.RailRowStyle)
	mixS(m.Settings.RailHeaderStyle)
	mixS(m.Settings.RailHeaderCase)
	mixB(m.Settings.RailRoomy)

	mixI(m.CurrentWorkspace)
	for _, ws := range m.pendingWorkspaceOrder {
		mixI(ws)
	}
	mixB(m.dockWorkspaceDrag.Dragging)
	for _, ws := range m.dockWorkspaceDrag.Order {
		mixI(ws)
	}

	var wsFold uint64
	for ws, name := range m.WorkspaceNames {
		e := uint64(1469598103934665603) ^ uint64(ws)
		e *= prime
		for i := range len(name) {
			e ^= uint64(name[i])
			e *= prime
		}
		wsFold ^= e
	}
	mixU(wsFold)

	mixB(m.UseScrollingLayout && m.AutoTiling)
	var stripFold uint64
	if m.UseScrollingLayout && m.AutoTiling {
		for ws, sl := range m.WorkspaceScrollingLayouts {
			if sl == nil {
				continue
			}
			e := uint64(1469598103934665603) ^ uint64(ws)
			e *= prime
			for _, col := range sl.Columns {
				for _, id := range col.WindowIDs {
					if _, ok := m.BSPIDToWindowID[id]; ok {
						e ^= uint64(id)
						e *= prime
					}
				}
				e ^= 0x3f
				e *= prime
			}
			stripFold ^= e
		}
	}
	mixU(stripFold)

	mixS(m.SessionName)
	for _, o := range m.SidebarOrder {
		mixS(o)
	}
	attached := m.attachedMachine()
	mixB(m.sessionOrderPending(attached))
	mixB(m.sessionOrderFromDaemon(attached))

	var repoFold uint64
	for repo, collapsed := range m.SidebarCollapsedRepos {
		if !collapsed {
			continue
		}
		e := uint64(1469598103934665603)
		for i := range len(repo) {
			e ^= uint64(repo[i])
			e *= prime
		}
		repoFold ^= e
	}
	mixU(repoFold)

	mixS(m.AttachedHost)
	for _, h := range m.SidebarHostOrder {
		mixS(h)
	}
	var hostFold uint64
	for host, collapsed := range m.SidebarCollapsedHosts {
		if !collapsed {
			continue
		}
		e := uint64(1469598103934665603)
		for i := range len(host) {
			e ^= uint64(host[i])
			e *= prime
		}
		hostFold ^= e
	}
	mixU(hostFold)
	var hostOrder uint64
	for host, order := range m.SidebarHostSessionOrder {
		e := uint64(1469598103934665603)
		for _, s := range append([]string{host}, order...) {
			for i := range len(s) {
				e ^= uint64(s[i])
				e *= prime
			}
			e ^= 0x2f
			e *= prime
		}
		hostOrder ^= e
	}
	mixU(hostOrder)
	if m.SessionWorktree != nil {
		mixS(m.SessionWorktree.Repo)
		mixS(m.SessionWorktree.Branch)
		mixB(m.SessionWorktree.Gone)
		mixS(m.SessionWorktree.PR.Badge())
		if s := m.SessionWorktree.Setup; s != nil {
			mixS(s.State)
		}
	} else {
		mixI(-1)
	}
	mixS(m.SessionSpace)
	mixS(m.SessionNote)

	if m.DaemonClient != nil {
		mixU(m.DaemonClient.CacheGen())
	}

	mixU(m.federationGen)
	if len(m.FederationHosts) > 0 {
		now := m.railClock()
		for _, h := range m.FederationHosts {
			mixS(hostDownLabel(h.Status, h.LastOK, now))
		}
	}

	mixB(m.ShowAccentPicker)
	if m.ShowAccentPicker {
		mixI(int(m.AccentPickerTarget))
		mixS(m.AccentPickerTargetID)
		preview, ok := m.accentPreview(m.AccentPickerTarget, m.AccentPickerTargetID)
		if !ok {
			mixI(-1)
		} else {
			mixU(preview.fold())
		}
	}

	mixB(m.Settings.SessionColors)
	if m.Settings.SessionColors {
		mixS(m.SessionAccent)
	}

	mixS(m.sessionTitle(m.SessionName))
	for _, w := range m.Windows {
		if w == nil {
			continue
		}
		mixS(w.ID)
		mixS(m.railTitleShown(w))
		mixS(m.paneIconName(w))
		mixS(w.AgentState)
		mixU(w.AgentCompletionSeq)
		mixS(w.AgentHarness)
		mixS(w.AgentMessage)
		mixS(w.AgentKind)
		if n := len(w.AgentMeta); n > 0 {
			mixI(n)
			for _, t := range w.AgentMeta {
				mixS(t.Key)
				mixS(t.Value)
			}
		}
		if w.AgentQueued > 0 {
			mixI(w.AgentQueued)
		}
		if w.AgentDraftHeld {
			mixI(1)
		}
		if w.AgentSettled {
			mixS("settled")
		}
		if w.AgentQuiet {
			mixS("quiet")
		}
		mixS(w.Cwd)
		mixS(w.DaemonCwd)
		if w.AgentSubagents > 0 {
			mixI(w.AgentSubagents)
		}
		if n := len(w.AgentSubagentList); n > 0 {
			mixI(n)
			for _, sa := range w.AgentSubagentList {
				mixS(sa.ID)
				mixS(sa.State)
				mixS(sa.Description)
				mixS(sa.Type)
				mixS(sa.Now)
				mixS(sa.Last)
				mixI(sa.Tools)
				mixS(sa.Result)
				if sv.Hide {
					mixB(sidebarSubagentHidden(sv, sa, hideNow))
				}
				if sa.EndedAt == 0 {
					mixI(int(agentElapsedBucket(sa.StartedAt)))
				} else {
					mixI(int((sa.EndedAt - sa.StartedAt) / int64(time.Minute)))
				}
			}
		}
		if n := len(w.ProgramStatus); n > 0 {
			mixI(n)
			for _, r := range w.ProgramStatus {
				mixS(r.ID)
				mixS(r.State)
				mixS(r.App)
				mixI(r.Progress)
				mixS(r.Title)
				mixS(r.Msg)
			}
		}
		mixI(int(agentElapsedBucket(w.AgentStateAt)))
		mixI(w.Workspace)
		if accent, ok := m.WindowAccent(w.ID); ok {
			mixU(accent.fold())
		} else {
			mixI(-1)
		}
	}

	var seenFold uint64
	for id, seen := range m.SidebarAgentSeen {
		if !seen {
			continue
		}
		e := uint64(1469598103934665603)
		for i := range len(id) {
			e ^= uint64(id[i])
			e *= prime
		}
		seenFold ^= e
	}
	mixU(seenFold)
	var seqFold uint64
	for id, seq := range m.SidebarAgentSeenSeq {
		e := uint64(1469598103934665603) ^ seq
		for i := range len(id) {
			e ^= uint64(id[i])
			e *= prime
		}
		seqFold ^= e
	}
	mixU(seqFold)

	return h
}
