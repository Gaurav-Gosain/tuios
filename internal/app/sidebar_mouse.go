package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

type sidebarDragState struct {
	PressActive bool
	SessionID   string
	Host        bool
	Repo        bool
	Space       string
	PressX      int
	PressY      int
	Dragging    bool
	Order       []string
	Blocks      sidebarOrderBlocks
	PinnedTold  bool
}

type sidebarEdgeState struct {
	Active  bool
	PressX  int
	Row     sidebarRowHit
	HaveRow bool
}

// ToggleSidebar flips the sidebar on or off live, re-tiles or re-clamps the
// windows into the changed content region, and records the new state on the
// loaded config so a later save keeps it. This is what the palette entry and the
// keybind both call.
//
// In a daemon session that keeps the rail as session state, the change goes
// to the daemon as well, and every client of the session shows or hides its
// rail with this one. See session/sidebar_visibility.go.
func (m *OS) ToggleSidebar() {
	on := !m.Settings.SidebarEnabled
	m.setSidebarShown(on)
	if m.UserConfig != nil {
		v := on
		m.UserConfig.Appearance.Sidebar.Enabled = &v
	}
	m.SidebarRevealedForFocus = false
	if m.sidebarOpsOn() {
		m.sidebarSession = sidebarVisibility(on)
		if err := m.DaemonClient.SendSidebarVisibility(on, false, !m.Settings.SidebarPerSession); err != nil {
			m.LogError("Failed to send the sidebar state: %v", err)
		}
	}
}

// ToggleOtherSidebar independently shows or hides the explicitly configured
// opposite edge. Unlike the legacy rail this is client-local: the opposite
// edge's opt-in belongs to this client's config, not to daemon session state.
func (m *OS) ToggleOtherSidebar() bool {
	if m.UserConfig == nil {
		return false
	}
	cfg := m.UserConfig.Appearance.Sidebar.Left
	if m.legacySidebarEdge() == sidebarLeft {
		cfg = m.UserConfig.Appearance.Sidebar.Right
	}
	if cfg == nil || cfg.Enabled == nil {
		return false
	}
	on := !*cfg.Enabled
	if !on && m.SidebarFocusOnSecondary() {
		m.WithSecondarySidebar(func() { m.ExitSidebarFocus() })
	}
	cfg.Enabled = &on
	m.secondarySidebarCache.invalidate()
	m.secondarySidebarHits = m.secondarySidebarHits[:0]
	m.tooltipClear()
	if m.AutoTiling {
		m.TileAllWindows()
	} else {
		m.ClampWindowsToView()
	}
	return on
}

func (m *OS) setSidebarShown(on bool) {
	if !m.applySidebarFlag(on) {
		return
	}
	if m.AutoTiling {
		m.TileAllWindows()
	} else {
		m.ClampWindowsToView()
	}
}

func (m *OS) applySidebarFlag(on bool) bool {
	if m.Settings.SidebarEnabled == on {
		return false
	}
	m.Settings.SidebarEnabled = on
	m.SidebarScroll = [sidebarSectionCount]int{}
	m.sidebarClearPeek()
	m.tooltipClear()
	if on {
		m.foreignSessionReplan = true
	}
	return true
}

func (m *OS) sidebarOpsOn() bool {
	return !m.NoRail && m.IsDaemonSession && m.DaemonClient != nil
}

func sidebarVisibility(on bool) string {
	if on {
		return session.SidebarShown
	}
	return session.SidebarHidden
}

func (m *OS) seedSidebar() {
	if !m.sidebarOpsOn() || m.sidebarSeeded || m.sidebarSession != "" {
		return
	}
	m.sidebarSeeded = true
	on := m.Settings.SidebarEnabled && !m.SidebarRevealedForFocus
	if err := m.DaemonClient.SendSidebarVisibility(on, true, false); err != nil {
		m.LogError("Failed to offer the sidebar state: %v", err)
	}
}

func (m *OS) syncSidebarToSession() {
	if !m.sidebarOpsOn() || m.sidebarSession == "" || m.SidebarRevealedForFocus {
		return
	}
	v := sidebarVisibility(m.Settings.SidebarEnabled)
	if v == m.sidebarSession {
		return
	}
	m.sidebarSession = v
	if err := m.DaemonClient.SendSidebarVisibility(m.Settings.SidebarEnabled, false, !m.Settings.SidebarPerSession); err != nil {
		m.LogError("Failed to send the sidebar state: %v", err)
	}
}

func (m *OS) keepSessionSidebar(changed bool) {
	if changed || !m.sidebarOpsOn() || m.sidebarSession == "" {
		return
	}
	m.Settings.SidebarEnabled = m.sidebarSession == session.SidebarShown || m.SidebarRevealedForFocus
}

func (m *OS) joinSession(state *session.SessionState) {
	m.layoutGenPushed = 0
	if m.DaemonClient != nil {
		m.layoutGenApplied = m.DaemonClient.SessionLayoutGeneration()
	}
	m.sidebarSeeded = false
	m.sidebarSession = ""
	if state != nil {
		m.adoptSidebarVisibility(state)
	}
	m.seedSidebar()
}

func (m *OS) adoptSidebarVisibility(state *session.SessionState) bool {
	if !m.sidebarOpsOn() {
		m.sidebarSession = ""
		return false
	}
	v := state.Sidebar
	if v == "" || v == m.sidebarSession {
		return false
	}
	m.sidebarSession = v
	on := v == session.SidebarShown
	if on {
		m.SidebarRevealedForFocus = false
	} else if m.SidebarFocused {
		m.SidebarRevealedForFocus = true
		return false
	}
	return m.applySidebarFlag(on)
}

// SidebarActive reports whether the sidebar reserves any columns this frame, so
// the mouse handlers know to test it before the window layer.
func (m *OS) SidebarActive() bool {
	return m.GetSidebarWidth() > 0 || m.secondarySidebarWidth() > 0
}

// SidebarBandContains reports whether the absolute cell (x, y) falls inside the
// sidebar's reserved column band. A click or wheel anywhere in the band is the
// sidebar's, even on a blank row, so it never leaks to the pane the sidebar sits
// in front of.
func (m *OS) SidebarBandContains(x, y int) bool {
	x, y = m.ScreenPoint(x, y)
	topMargin := m.viewReserve().Top
	if y < topMargin || y >= topMargin+m.ViewUsableHeight() {
		return false
	}
	w := m.GetSidebarWidth()
	primaryX := sidebarEdgeX(m.legacySidebarEdge(), w, m.GetRenderWidth())
	if w > 0 && x >= primaryX && x < primaryX+w {
		return true
	}
	secondary := m.secondarySidebarWidth()
	if secondary <= 0 {
		return false
	}
	opposite := sidebarRight
	if m.legacySidebarEdge() == sidebarRight {
		opposite = sidebarLeft
	}
	secondaryX := sidebarEdgeX(opposite, secondary, m.GetRenderWidth())
	return x >= secondaryX && x < secondaryX+secondary
}

func (m *OS) sidebarRowAt(x, y int) (sidebarRowHit, bool) {
	for _, h := range m.SidebarHits {
		if h.Contains(x, y) {
			return h, true
		}
	}
	return sidebarRowHit{}, false
}

func (m *OS) sidebarClearPeek() {
	m.SidebarPeek = ""
}

func (m *OS) sidebarPeekAllowed() bool {
	return m.Settings.SidebarPeekEnabled
}

func (m *OS) sidebarPeekAt(x, y int) {
	if !m.sidebarPeekAllowed() {
		m.sidebarClearPeek()
		return
	}
	if sidebarVariant(m.GetSidebarWidth()) == sidebarVariantGlyph {
		m.sidebarClearPeek()
		return
	}
	hit, ok := m.sidebarRowAt(x, y)
	if !ok || hit.Kind != sidebarRowSession || hit.SessionID == m.sidebarCurrentSessionID() {
		m.sidebarClearPeek()
		return
	}
	m.SidebarPeek = hit.SessionID
}

// SidebarClick routes a left or right press at absolute (x, y) to the sidebar.
// It returns whether the event was consumed (any press in the band is), so the
// caller stops before the press can reach a pane underneath.
//
//   - Terminal or agent row, left press: focus that window, switching session
//     first when the window belongs to another session.
//   - Session row, left press: arm the click-or-drag gesture; the release
//     attaches to that session, a vertical drag reorders the session list.
//   - Footer control, left press: make a session, or step the rail's width.
//   - Right press on any row: open the context menu (pane menu for a window or
//     agent row, the session/desktop menu for a session row).
func (m *OS) SidebarClick(x, y int, right bool) bool {
	if edge, ok := m.SidebarSessionHeaderAt(x, y); ok {
		if right {
			m.OpenSettingsAt("Sidebar")
		} else {
			m.FocusSidebarSession(edge)
		}
		return true
	}
	if edge, ok := m.SidebarSessionAt(x, y); ok {
		if right {
			m.OpenSettingsAt("Sidebar")
		} else {
			m.FocusSidebarSession(edge)
		}
		return true
	}
	if m.secondarySidebarBandContains(x, y) {
		return m.withSecondaryRail(func() bool { return m.sidebarClick(x, y, right) })
	}
	return m.sidebarClick(x, y, right)
}

func (m *OS) sidebarClick(x, y int, right bool) bool {
	if !m.SidebarBandContains(x, y) {
		return false
	}
	m.tooltipClear()

	if !right && m.sidebarOnEdge(x) {
		m.SidebarEdge = sidebarEdgeState{Active: true, PressX: x}
		if sidebarVariant(m.GetSidebarWidth()) == sidebarVariantGlyph {
			if hit, ok := m.sidebarRowAt(x, y); ok {
				m.SidebarEdge.Row, m.SidebarEdge.HaveRow = hit, true
			}
		}
		return true
	}

	hit, ok := m.sidebarRowAt(x, y)
	if !ok {
		if right {
			if m.filesBandContains(y) {
				m.openFilesSectionMenu(x, y, fileMenuTarget{Dir: m.filesView.Dir})
				return true
			}
			m.openRailSettingsMenu(x, y)
			return true
		}
		m.EnterSidebarFocus()
		return true
	}

	if right {
		m.openSidebarContextMenu(hit, x, y)
		return true
	}

	if m.SidebarFocused {
		m.sidebarSetCursorToHit(hit)
	}

	switch hit.Kind {
	case sidebarRowWindow, sidebarRowAgent:
		m.sidebarFocusWindow(hit)
	case sidebarRowAgentFilter:
		m.SidebarCycleAgentsFilter()
	case sidebarRowAgentSort:
		m.SidebarCycleAgentsSort()
	case sidebarRowAgentMail:
		m.queueSidebarCmd(m.OpenAgentMail())
	case sidebarRowAgentFold:
		m.SidebarUnfoldAgents()
	case sidebarRowSubagentToggle:
		m.SidebarToggleSubagents(hit.SessionID, hit.WindowID)
	case sidebarRowAgentKin:
		m.SidebarToggleKin(hit.SessionID, hit.WindowID)
	case sidebarRowNewSession:
		m.SidebarNewSessionHere()
	case sidebarRowNewWindow:
		m.SidebarNewWindow(hit.SessionID)
	case sidebarRowHostSession:
		m.openRemoteSession(hit.SessionID, hit.WindowID)
	case sidebarRowHostNew:
		m.createRemoteSession(hit.SessionID)
	case sidebarRowHostSignIn:
		m.queueSidebarCmd(m.openHostSignIn(hit.SessionID))
	case sidebarRowGlobalNew:
		m.SidebarNewGlobalSession()
	case sidebarRowRailPaneStart:
		m.RestartRailPane()
	case sidebarRowCustomLink:
		m.queueSidebarCmd(m.OpenLink(hit.WindowID))
	case sidebarRowCollapse:
		m.SidebarToggleCollapsed()
	case sidebarRowRepo:
		m.SidebarDrag = sidebarDragState{
			PressActive: true,
			SessionID:   hit.SessionID,
			Repo:        true,
			Space:       hit.WindowID,
			PressX:      x,
			PressY:      y,
		}
	case sidebarRowFiles:
		m.queueSidebarCmd(m.ToggleFileView())
	case sidebarRowFileClose:
		m.CloseFileView()
	case sidebarRowFileCd:
		m.FileViewCd()
	case sidebarRowFileUp:
		m.queueSidebarCmd(m.FileViewUp())
	case sidebarRowFileBack:
		m.queueSidebarCmd(m.FileViewBack())
	case sidebarRowFileEntry:
		if !m.armFileDrag(hit, x, y) {
			m.fileRowClick(hit, m.sidebarPressCopy)
		}
	case sidebarRowDivider:
		m.sidebarSplitPress()
	case sidebarRowSession:
		m.SidebarDrag = sidebarDragState{
			PressActive: true,
			SessionID:   hit.SessionID,
			PressX:      x,
			PressY:      y,
		}
	case sidebarRowHost:
		m.SidebarDrag = sidebarDragState{
			PressActive: true,
			SessionID:   hit.SessionID,
			Host:        true,
			PressX:      x,
			PressY:      y,
		}
	}
	return true
}

// SidebarDragActive reports whether a session-row press or drag is in
// progress, so the motion and release handlers route to the sidebar first.
func (m *OS) SidebarDragActive() bool {
	return m.SidebarDrag.PressActive || m.SidebarDrag.Dragging || m.secondaryRailView.drag.PressActive || m.secondaryRailView.drag.Dragging
}

func (m *OS) sidebarOnEdge(x int) bool {
	w := m.GetSidebarWidth()
	if w <= 0 {
		return false
	}
	edge := m.legacySidebarEdge()
	if m.sidebarDrawing {
		edge = m.sidebarDrawingEdge
	}
	if edge == sidebarRight {
		return x == m.GetRenderWidth()-w
	}
	return x == w-1
}

// SidebarEdgeActive reports whether a width-resize gesture is in progress, so
// the motion and release handlers route to the sidebar first.
func (m *OS) SidebarEdgeActive() bool {
	return m.SidebarEdge.Active || m.secondaryRailView.edge.Active
}

func (m *OS) sidebarWidthBounds() (int, int) {
	lo := config.SidebarGlyphWidth
	hi := max(m.GetRenderWidth()*2/5, lo)
	return lo, hi
}

// SidebarEdgeMotion sets the rail width to the pointer column and re-lays the
// panes into the changed content region, exactly as ToggleSidebar does. The
// width comes from the pointer's distance from the far edge, so the hairline
// tracks the cursor.
func (m *OS) SidebarEdgeMotion(x, y int) bool {
	if m.secondaryRailView.edge.Active {
		return m.withSecondaryRail(func() bool { return m.sidebarEdgeMotion(x, y) })
	}
	return m.sidebarEdgeMotion(x, y)
}

func (m *OS) sidebarEdgeMotion(x, y int) bool {
	if !m.SidebarEdge.Active {
		return false
	}
	if x != m.SidebarEdge.PressX {
		m.SidebarEdge.HaveRow = false
	}
	var w int
	edge := m.legacySidebarEdge()
	if m.sidebarDrawing {
		edge = m.sidebarDrawingEdge
	}
	if edge == sidebarRight {
		w = m.GetRenderWidth() - x
	} else {
		w = x + 1
	}
	lo, hi := m.sidebarWidthBounds()
	w = max(min(w, hi), lo)
	if m.sidebarDrawing {
		m.secondaryCollapsed = w <= config.SidebarGlyphWidth
		m.secondaryWidthPref = w
		if m.AutoTiling {
			m.TileAllWindows()
		} else {
			m.ClampWindowsToView()
		}
		return true
	}
	collapsed := m.SidebarCollapsed && w <= config.SidebarGlyphWidth
	if w == m.sidebarWidthPreference() && collapsed == m.SidebarCollapsed {
		return true
	}
	m.SidebarCollapsed = collapsed
	m.SidebarWidthPref = w
	if m.AutoTiling {
		m.TileAllWindows()
	} else {
		m.ClampWindowsToView()
	}
	return true
}

// SidebarEdgeRelease ends the resize and persists the new width beside the
// order and collapse state.
func (m *OS) SidebarEdgeRelease(x, y int) bool {
	if m.secondaryRailView.edge.Active {
		return m.withSecondaryRail(func() bool { return m.sidebarEdgeRelease(x, y) })
	}
	return m.sidebarEdgeRelease(x, y)
}

func (m *OS) sidebarEdgeRelease(x, y int) bool {
	if !m.SidebarEdge.Active {
		return false
	}
	edge := m.SidebarEdge
	m.SidebarEdge = sidebarEdgeState{}
	if !m.sidebarDrawing {
		m.saveSidebarState()
	}
	if edge.HaveRow && x == edge.PressX {
		m.sidebarActivateRow(edge.Row)
	}
	return true
}

func (m *OS) sidebarActivateRow(hit sidebarRowHit) {
	if m.SidebarFocused {
		m.sidebarSetCursorToHit(hit)
	}
	switch hit.Kind {
	case sidebarRowWindow, sidebarRowAgent:
		m.sidebarFocusWindow(hit)
	case sidebarRowAgentFilter:
		m.SidebarCycleAgentsFilter()
	case sidebarRowAgentSort:
		m.SidebarCycleAgentsSort()
	case sidebarRowAgentMail:
		m.queueSidebarCmd(m.OpenAgentMail())
	case sidebarRowAgentFold:
		m.SidebarUnfoldAgents()
	case sidebarRowSubagentToggle:
		m.SidebarToggleSubagents(hit.SessionID, hit.WindowID)
	case sidebarRowAgentKin:
		m.SidebarToggleKin(hit.SessionID, hit.WindowID)
	case sidebarRowNewSession:
		m.SidebarNewSessionHere()
	case sidebarRowNewWindow:
		m.SidebarNewWindow(hit.SessionID)
	case sidebarRowHostSession:
		m.openRemoteSession(hit.SessionID, hit.WindowID)
	case sidebarRowHostNew:
		m.createRemoteSession(hit.SessionID)
	case sidebarRowHostSignIn:
		m.queueSidebarCmd(m.openHostSignIn(hit.SessionID))
	case sidebarRowCollapse:
		m.SidebarToggleCollapsed()
	case sidebarRowRepo:
		m.SidebarToggleRepoCollapsed(hit.SessionID)
	case sidebarRowHost:
		m.SidebarToggleHostCollapsed(hit.SessionID)
	case sidebarRowFiles:
		m.queueSidebarCmd(m.ToggleFileView())
	case sidebarRowFileClose:
		m.CloseFileView()
	case sidebarRowFileCd:
		m.FileViewCd()
	case sidebarRowFileUp:
		m.queueSidebarCmd(m.FileViewUp())
	case sidebarRowFileBack:
		m.queueSidebarCmd(m.FileViewBack())
	case sidebarRowFileEntry:
		m.queueSidebarCmd(m.fileViewEnterRow(hit.WindowIndex, hit.WindowID))
	case sidebarRowDivider:
		m.SidebarResetSplit()
	case sidebarRowSession:
		m.sidebarSwitchSession(hit.SessionID)
	}
}

// SidebarDragMotion advances the click-or-drag gesture. The first vertical
// step past the press row commits the gesture to a reorder drag; from then on
// the dragged session follows the row under the pointer in a draft order that
// the render displays live.
func (m *OS) SidebarDragMotion(x, y int) bool {
	if m.secondaryRailView.drag.PressActive || m.secondaryRailView.drag.Dragging {
		return m.withSecondaryRail(func() bool { return m.sidebarDragMotion(x, y) })
	}
	return m.sidebarDragMotion(x, y)
}

func (m *OS) sidebarDragMotion(x, y int) bool {
	d := &m.SidebarDrag
	if !d.PressActive && !d.Dragging {
		return false
	}
	if !d.Dragging {
		if y == d.PressY {
			return true
		}
		if d.Host && d.SessionID == federation.LocalHostName {
			if !d.PinnedTold {
				d.PinnedTold = true
				m.sidebarSayMachinePinned()
			}
			return true
		}
		d.Dragging = true
		if d.Host {
			d.Order = append([]string(nil), m.SidebarHostIDs...)
		} else {
			d.Order, d.Blocks = m.sidebarDrawnOrder()
		}
	}
	if !d.Host {
		m.sidebarDragGroupMotion(d, y)
		return true
	}

	targetID := m.sidebarHostRowIDAt(y)
	if targetID == "" || targetID == d.SessionID {
		return true
	}
	from, target := -1, -1
	for i, id := range d.Order {
		if id == d.SessionID {
			from = i
		}
		if id == targetID {
			target = i
		}
	}
	if from < 0 || target < 0 || target == from {
		return true
	}
	id := d.Order[from]
	d.Order = append(d.Order[:from], d.Order[from+1:]...)
	at := min(target, len(d.Order))
	d.Order = append(d.Order[:at], append([]string{id}, d.Order[at:]...)...)
	return true
}

func (m *OS) sidebarSessionRowIDAt(y int) string {
	id, first := "", ""
	for _, h := range m.SidebarHits {
		if h.Kind != sidebarRowSession {
			continue
		}
		if first == "" {
			first = h.SessionID
		}
		if y >= h.Y0 {
			id = h.SessionID
		}
	}
	if id == "" {
		return first
	}
	return id
}

func (m *OS) sidebarHostRowIDAt(y int) string {
	id, first := "", ""
	for _, h := range m.SidebarHits {
		if h.Kind != sidebarRowHost || h.SessionID == federation.LocalHostName {
			continue
		}
		if first == "" {
			first = h.SessionID
		}
		if y >= h.Y0 {
			id = h.SessionID
		}
	}
	if id == "" {
		return first
	}
	return id
}

// SidebarRelease finishes the click-or-drag gesture: a drag commits its draft
// order and persists it; a plain release on the pressed row attaches to that
// session, or folds that machine's group. One gesture, one meaning: a release
// on the session already attached is simply nothing to do.
func (m *OS) SidebarRelease(x, y int) bool {
	if m.secondaryRailView.drag.PressActive || m.secondaryRailView.drag.Dragging {
		return m.withSecondaryRail(func() bool { return m.sidebarRelease(x, y) })
	}
	return m.sidebarRelease(x, y)
}

func (m *OS) sidebarRelease(x, y int) bool {
	d := m.SidebarDrag
	if !d.PressActive && !d.Dragging {
		return false
	}
	m.SidebarDrag = sidebarDragState{}

	if d.Dragging {
		if d.Host {
			m.SidebarHostOrder = d.Order
			m.saveSidebarState()
		} else {
			m.commitSessionOrder(d.Order)
		}
		return true
	}

	hit, ok := m.sidebarRowAt(x, y)
	if !ok || hit.SessionID != d.SessionID {
		return true
	}
	switch {
	case d.Host && hit.Kind == sidebarRowHost:
		m.SidebarToggleHostCollapsed(hit.SessionID)
	case d.Repo && hit.Kind == sidebarRowRepo:
		m.SidebarToggleRepoCollapsed(hit.SessionID)
	case !d.Host && !d.Repo && hit.Kind == sidebarRowSession:
		m.sidebarSwitchSession(hit.SessionID)
	}
	return true
}

// SidebarMotion tracks the pointer over the sidebar band so the row under the
// cursor is highlighted, the way overlay rows track hover. It returns whether
// the motion was consumed (any motion inside the band is, so it never reaches
// the pane the sidebar sits in front of). Motion outside the band clears the
// hover so no stale highlight lingers.
func (m *OS) SidebarMotion(x, y int) bool {
	if m.secondarySidebarBandContains(x, y) {
		m.SidebarHoverActive = false
		return m.withSecondaryRail(func() bool { return m.sidebarMotion(x, y) })
	}
	m.secondaryRailView.hover = false
	return m.sidebarMotion(x, y)
}

func (m *OS) sidebarMotion(x, y int) bool {
	if !m.SidebarBandContains(x, y) {
		m.SidebarHoverActive = false
		m.sidebarClearPeek()
		m.tooltipClear()
		return false
	}
	m.SidebarHoverActive = true
	m.SidebarHoverX, m.SidebarHoverY = x, y
	m.sidebarPeekAt(x, y)
	m.sidebarTooltipTrack(x, y)
	return true
}

// SidebarWheel scrolls the section under the pointer and reports whether it
// consumed the event. Per-section rather than rail-wide: one offset over the
// whole rail unpins the headers and can scroll the agents section, the alarm,
// off the screen entirely.
func (m *OS) SidebarWheel(x, y int, up bool) bool {
	if !m.sidebarDrawing && m.secondarySidebarBandContains(x, y) {
		return m.withSecondaryRail(func() bool { return m.sidebarWheel(x, y, up) })
	}
	return m.sidebarWheel(x, y, up)
}

func (m *OS) sidebarWheel(x, y int, up bool) bool {
	if !m.SidebarBandContains(x, y) {
		return false
	}
	offsets := m.sidebarScrollOffsets()
	for s, band := range m.sidebarSectionY {
		if y < band[0] || y >= band[1] {
			continue
		}
		if up {
			*offsets[s] = max(*offsets[s]-m.Settings.ScrollLines, 0)
		} else {
			*offsets[s] += m.Settings.ScrollLines
		}
		break
	}
	return true
}

func (m *OS) sidebarCurrentSessionID() string {
	if m.SessionName == "" {
		return "local"
	}
	return m.SessionName
}

func (m *OS) sidebarSwitchSession(sessionID string) {
	m.sidebarClearPeek()
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() {
		return
	}
	m.clearSidebarReturn()
	m.openSession("", sessionID)
}

func (m *OS) sidebarFocusWindow(hit sidebarRowHit) (idx int, ok bool) {
	return m.sidebarPickWindow(hit, true)
}

func (m *OS) sidebarPickWindow(hit sidebarRowHit, restore bool) (idx int, ok bool) {
	focus := func(i int) {
		if restore && m.Windows[i].Minimized {
			m.RestoreWindow(i)
		}
		m.FocusWindow(i)
	}
	m.clearSidebarReturn()
	defer func() {
		if ok {
			if m.Settings.NiriClickReveals {
				m.RevealFocusedColumn()
			}
		}
	}()
	if hit.WindowID != "" {
		if idx := m.windowIndexByID(hit.WindowID); idx >= 0 {
			focus(idx)
			return idx, true
		}
	}
	if hit.WindowID == "" && hit.WindowIndex >= 0 && hit.WindowIndex < len(m.Windows) {
		focus(hit.WindowIndex)
		return hit.WindowIndex, true
	}
	if hit.SessionID != "" && hit.SessionID != m.sidebarCurrentSessionID() {
		if !m.openSession("", hit.SessionID) {
			return -1, false
		}
	}
	if idx := m.windowIndexByID(hit.WindowID); idx >= 0 {
		focus(idx)
		return idx, true
	}
	return -1, false
}

func (m *OS) openSidebarContextMenu(hit sidebarRowHit, x, y int) {
	cm := &ContextMenu{
		AnchorX: x,
		AnchorY: y,
		ItemH:   1,
	}

	switch hit.Kind {
	case sidebarRowCustomLink:
		m.openRailSettingsMenu(x, y)
		return
	case sidebarRowHost:
		if hit.SessionID == "" {
			m.openRailSettingsMenu(x, y)
			return
		}
		m.sidebarSetCursorToHit(hit)
		cm.Target = CtxTargetMachine
		cm.SessionID = hit.SessionID
		cm.Title, cm.Items = m.machineMenu(hit.SessionID)
	case sidebarRowRepo:
		if !m.IsDaemonSession || m.worktreeRepoRoot(hit.SessionID) == "" {
			m.openRailSettingsMenu(x, y)
			return
		}
		cm.Target = CtxTargetRepo
		cm.Repo = hit.SessionID
		cm.Title, cm.Items = m.repoHeaderMenu(hit.SessionID)
	case sidebarRowHostSession, sidebarRowHostNew, sidebarRowHostSignIn, sidebarRowGlobalNew:
		m.openRailSettingsMenu(x, y)
		return
	case sidebarRowFileCd, sidebarRowFileClose, sidebarRowFileUp, sidebarRowFileBack, sidebarRowFileEntry:
		m.openFilesSectionMenu(x, y, m.fileMenuTargetFor(hit))
		return
	case sidebarRowWindow, sidebarRowAgent:
		idx, ok := m.sidebarPickWindow(hit, false)
		if !ok {
			m.CloseContextMenu()
			return
		}
		cm.Target = CtxTargetPane
		cm.WindowIndex = idx
		cm.Title, cm.Items = m.paneMenu(idx)
	default:
		cm.Target = CtxTargetDesktop
		cm.WindowIndex = -1
		if m.IsDaemonSession {
			cm.SessionID = hit.SessionID
			cm.Repo = m.sidebarHeadRepo(hit.SessionID)
			cm.Title, cm.Items = m.sessionMenu(hit.SessionID)
		} else {
			cm.Title, cm.Items = m.desktopMenu()
		}
	}

	cm.Items = append(cm.Items, separator(), m.railSettingsItem())

	cm.home()
	m.ContextMenu = cm
}

func (m *OS) openFilesSectionMenu(x, y int, t fileMenuTarget) {
	cm := &ContextMenu{
		AnchorX:     x,
		AnchorY:     y,
		ItemH:       1,
		Target:      CtxTargetFileRow,
		WindowIndex: -1,
		File:        t,
	}
	cm.Title, cm.Items = m.fileRowMenu(t)
	cm.Items = append(cm.Items, separator(), m.railSettingsItem())
	cm.home()
	m.ContextMenu = cm
}

func (m *OS) fileMenuTargetFor(hit sidebarRowHit) fileMenuTarget {
	t := fileMenuTarget{Dir: m.filesView.Dir}
	switch hit.Kind {
	case sidebarRowFileUp:
		t.Up = true
	case sidebarRowFileEntry:
		if m.filesTreeOn() {
			if dir, e, ok := m.fileTreeEntry(hit.WindowID); ok {
				t.Dir, t.Name, t.IsDir = dir, e.Name, e.Dir
			}
			break
		}
		for _, e := range m.filesView.Entries {
			if e.Name == hit.WindowID {
				t.Name, t.IsDir = e.Name, e.Dir
				break
			}
		}
	}
	return t
}

func (m *OS) filesBandContains(y int) bool {
	if !m.filesSectionEnabled() {
		return false
	}
	band := m.sidebarSectionY[sidebarSectionFiles]
	return y >= band[0] && y < band[1]
}

func (m *OS) openRailSettingsMenu(x, y int) {
	m.ContextMenu = &ContextMenu{
		AnchorX:     x,
		AnchorY:     y,
		ItemH:       1,
		Target:      CtxTargetDesktop,
		WindowIndex: -1,
		Title:       "Sidebar",
		Items:       []ContextMenuItem{m.railSettingsItem()},
	}
}

func (m *OS) railSettingsItem() ContextMenuItem {
	return m.item(glyphSettings, "Sidebar settings", "settings_sidebar", false)
}
