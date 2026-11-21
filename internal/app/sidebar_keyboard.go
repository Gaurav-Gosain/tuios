package app

import (
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

type sidebarNavRow struct {
	Kind        sidebarRowKind
	SessionID   string
	WindowID    string
	WindowIndex int
}

func sidebarNavRowsEqual(a, b sidebarNavRow) bool {
	return a.Kind == b.Kind && a.SessionID == b.SessionID && a.WindowID == b.WindowID
}

func (m *OS) sidebarCursorRow() (sidebarNavRow, bool) {
	if m.sidebarCursorWantSet {
		return m.sidebarCursorWant, true
	}
	if m.SidebarCursor < 0 || m.SidebarCursor >= len(m.SidebarNav) {
		return sidebarNavRow{}, false
	}
	return m.SidebarNav[m.SidebarCursor], true
}

// EnterSidebarFocus gives the keyboard to the rail. If the sidebar is off it is
// revealed first (and hidden again on exit), so the scope is reachable even when
// the rail is not already showing. The cursor lands on the current session so
// navigation starts where the eye is.
func (m *OS) EnterSidebarFocus() {
	if m.SidebarFocused && m.sidebarFocusSecondary == m.sidebarDrawing {
		return
	}
	m.sidebarFocusSecondary = m.sidebarDrawing
	if !m.Settings.SidebarEnabled {
		m.SidebarRevealedForFocus = true
		m.applySidebarFlag(true)
	}
	m.SidebarFocused = true
	m.beginSidebarReturn()
	if m.restoreSidebarRow() {
		return
	}
	m.sidebarFollowSession = m.sidebarCurrentSessionID()
	m.sidebarSetCursor(m.sidebarCurrentSessionNavIndex())
}

// FocusOtherSidebar moves keyboard ownership between visible rails without
// changing either rail's saved cursor or leaking a key to the pane underneath.
func (m *OS) FocusOtherSidebar() {
	if !m.SidebarFocused || m.sidebarWidthFor(m.sidebarPreferredWidth()) == 0 || m.secondarySidebarWidth() == 0 {
		return
	}
	m.sidebarClearPeek()
	m.sidebarFocusSecondary = !m.sidebarFocusSecondary
}

func (m *OS) sidebarSetCursor(i int) {
	m.sidebarCursorWantSet = false
	m.SidebarCursor = i
	row, ok := m.sidebarCursorRow()
	if !ok || !m.sidebarPeekAllowed() || row.Kind != sidebarRowSession || row.SessionID == m.sidebarCurrentSessionID() {
		m.sidebarClearPeek()
		return
	}
	m.SidebarPeek = row.SessionID
}

// ExitSidebarFocus returns the keyboard to the panes. A sidebar revealed only to
// host the scope is hidden again, matching how it was found.
func (m *OS) ExitSidebarFocus() {
	if !m.SidebarFocused {
		return
	}
	m.SidebarFocused = false
	m.sidebarFocusSecondary = false
	m.sidebarAgentsUnfolded = false
	m.recordSidebarRow()
	m.sidebarClearPeek()
	m.endSidebarReturn()
	if m.SidebarRevealedForFocus {
		m.SidebarRevealedForFocus = false
		m.applySidebarFlag(false)
	}
}

func railEdgeToward(direction string) (sidebarEdge, bool) {
	switch direction {
	case "left":
		return sidebarLeft, true
	case "right":
		return sidebarRight, true
	}
	return 0, false
}

func (m *OS) enterRailAtEdge(direction string) {
	edge, ok := railEdgeToward(direction)
	if !ok || m.sidebarWidthFor(m.sidebarPreferredWidth()) == 0 {
		return
	}
	if m.legacySidebarEdge() == edge {
		m.EnterSidebarFocus()
		return
	}
	if m.secondarySidebarWidth() > 0 {
		m.EnterSidebarFocus()
		m.FocusOtherSidebar()
	}
}

// LeaveRailToward returns the keyboard to the panes when a focus key points
// from the focused rail back at them, the way enterRailAtEdge came in. It
// reports whether it left.
func (m *OS) LeaveRailToward(direction string) bool {
	if !m.SidebarFocused {
		return false
	}
	edge := m.legacySidebarEdge()
	if m.sidebarFocusSecondary {
		edge = sidebarLeft
		if m.legacySidebarEdge() == sidebarLeft {
			edge = sidebarRight
		}
	}
	away, ok := railEdgeToward(direction)
	if !ok || away == edge {
		return false
	}
	m.ExitSidebarFocus()
	return true
}

func (m *OS) sidebarCurrentSessionNavIndex() int {
	cur := m.sidebarCurrentSessionID()
	for i, r := range m.SidebarNav {
		if r.Kind == sidebarRowSession && r.SessionID == cur {
			return i
		}
	}
	return 0
}

// SidebarCursorMove steps the cursor by delta over the nav rows, clamped to the
// ends (no wrap, matching a scroll that stops at the top and bottom).
func (m *OS) SidebarCursorMove(delta int) {
	if len(m.SidebarNav) == 0 {
		return
	}
	if m.sidebarFilesCursorMove(delta) {
		return
	}
	m.sidebarSetCursor(max(min(m.SidebarCursor+delta, len(m.SidebarNav)-1), 0))
}

// SidebarCursorFirst and SidebarCursorLast jump to the ends of the rail. On a
// name of the files section they jump to the ends of the listing first, which
// the rail may not draw, and a second press goes on to the end of the rail.
func (m *OS) SidebarCursorFirst() {
	if m.sidebarFilesCursorJump(false) {
		return
	}
	m.sidebarSetCursor(0)
}

func (m *OS) SidebarCursorLast() {
	if m.sidebarFilesCursorJump(true) {
		return
	}
	if n := len(m.SidebarNav); n > 0 {
		m.sidebarSetCursor(n - 1)
	}
}

func (m *OS) sidebarWantCursor(row sidebarNavRow) {
	m.sidebarClearPeek()
	m.sidebarCursorWant, m.sidebarCursorWantSet = row, true
}

func isFileListRow(kind sidebarRowKind) bool {
	return kind == sidebarRowFileUp || kind == sidebarRowFileBack || kind == sidebarRowFileEntry
}

func fileNavRow(r fileRowSpec) sidebarNavRow {
	return sidebarNavRow{Kind: r.Kind, WindowID: r.Key, WindowIndex: r.Index}
}

func (m *OS) sidebarFilesCursorMove(delta int) bool {
	cur, ok := m.sidebarCursorRow()
	if !ok || !isFileListRow(cur.Kind) {
		return false
	}
	rows := m.sidebarFileRows()
	at := -1
	for i, r := range rows {
		if !r.Note && r.Kind == cur.Kind && r.Key == cur.WindowID {
			at = i
			break
		}
	}
	if at < 0 {
		return false
	}
	for j := at + delta; j >= 0 && j < len(rows); j += delta {
		if rows[j].Note || rows[j].Kind == 0 {
			continue
		}
		m.sidebarWantCursor(fileNavRow(rows[j]))
		return true
	}
	edge := -1
	for i, r := range m.SidebarNav {
		if !isFileListRow(r.Kind) {
			continue
		}
		if delta > 0 || edge < 0 {
			edge = i
		}
	}
	if edge < 0 {
		return false
	}
	m.sidebarSetCursor(max(min(edge+delta, len(m.SidebarNav)-1), 0))
	return true
}

func (m *OS) sidebarFilesCursorJump(last bool) bool {
	cur, ok := m.sidebarCursorRow()
	if !ok || !isFileListRow(cur.Kind) {
		return false
	}
	rows := m.sidebarFileRows()
	pick := -1
	for i, r := range rows {
		if r.Note || r.Kind == 0 {
			continue
		}
		if last || pick < 0 {
			pick = i
		}
	}
	if pick < 0 {
		return false
	}
	target := fileNavRow(rows[pick])
	if sidebarNavRowsEqual(target, cur) {
		return false
	}
	m.sidebarWantCursor(target)
	return true
}

// SidebarCursorExpand and SidebarCursorCollapse step the cursor one section
// down and up. The session tree they once walked is gone, and the keys
// that walked its depth are the natural pair for walking the sections. The
// config action names stay, so an older config file still resolves both keys.
//
// On a name of the files section drawn as a tree they are the tree's own keys,
// as in neo-tree: l opens a folder and h closes it or goes up to its folder.
func (m *OS) SidebarCursorExpand() {
	if m.fileTreeCursorExpand() {
		return
	}
	m.sidebarStepSection(1)
}

func (m *OS) SidebarCursorCollapse() {
	if m.fileTreeCursorCollapse() {
		return
	}
	m.sidebarStepSection(-1)
}

func (m *OS) sidebarStepSection(delta int) {
	plans := m.sidebarPlans()
	here := 0
	if row, ok := m.sidebarCursorRow(); ok {
		for i, p := range plans {
			if sidebarSectionOfKind(row.Kind) == p.Section {
				here = i
				break
			}
		}
	}
	for i := here + delta; i >= 0 && i < len(plans); i += delta {
		if j := m.sidebarFirstRowOfSection(plans[i].Section); j >= 0 {
			m.sidebarSetCursor(j)
			return
		}
	}
}

func sidebarSectionOfKind(kind sidebarRowKind) sidebarSection {
	switch kind {
	case sidebarRowSession, sidebarRowHostSession, sidebarRowHostNew, sidebarRowHostSignIn, sidebarRowGlobalNew, sidebarRowHost, sidebarRowRepo:
		return sidebarSectionSessions
	case sidebarRowWindow:
		return sidebarSectionTerminals
	case sidebarRowAgent, sidebarRowAgentFilter, sidebarRowAgentSort, sidebarRowAgentMail, sidebarRowAgentFold, sidebarRowSubagentToggle, sidebarRowAgentKin:
		return sidebarSectionAgents
	case sidebarRowFileUp, sidebarRowFileBack, sidebarRowFileEntry, sidebarRowFileCd, sidebarRowFileClose:
		return sidebarSectionFiles
	default:
		return sidebarSectionCount
	}
}

func (m *OS) sidebarFirstRowOfSection(section sidebarSection) int {
	for i, r := range m.SidebarNav {
		if sidebarSectionOfKind(r.Kind) == section {
			return i
		}
	}
	return -1
}

func (m *OS) sidebarFirstRowOfKind(kind sidebarRowKind) int {
	for i, r := range m.SidebarNav {
		if r.Kind == kind {
			return i
		}
	}
	return -1
}

// SidebarActivateCursor is the keyboard's enter: it runs exactly what a click on
// the cursor row would (attach to a session, focus a pane, work a footer
// control), reusing the mouse handlers so the two never diverge. It
// reports whether activation should leave the rail: focusing a window is a
// request for that pane, so the scope exits; navigating sessions keeps it.
func (m *OS) SidebarActivateCursor() bool {
	row, ok := m.sidebarCursorRow()
	if !ok {
		return false
	}
	switch row.Kind {
	case sidebarRowWindow, sidebarRowAgent:
		m.sidebarFocusWindow(sidebarRowHit{
			Kind:        row.Kind,
			SessionID:   row.SessionID,
			WindowID:    row.WindowID,
			WindowIndex: row.WindowIndex,
		})
		return true
	case sidebarRowAgentFilter:
		m.SidebarCycleAgentsFilter()
	case sidebarRowAgentSort:
		m.SidebarCycleAgentsSort()
	case sidebarRowAgentMail:
		m.queueSidebarCmd(m.OpenAgentMail())
	case sidebarRowAgentFold:
		m.SidebarUnfoldAgents()
	case sidebarRowSubagentToggle:
		m.SidebarToggleSubagents(row.SessionID, row.WindowID)
	case sidebarRowAgentKin:
		m.SidebarToggleKin(row.SessionID, row.WindowID)
	case sidebarRowNewSession:
		m.SidebarNewSessionHere()
	case sidebarRowNewWindow:
		m.SidebarNewWindow(row.SessionID)
		return true
	case sidebarRowHostSession:
		m.openRemoteSession(row.SessionID, row.WindowID)
		return true
	case sidebarRowHostNew:
		m.createRemoteSession(row.SessionID)
		return true
	case sidebarRowHostSignIn:
		m.queueSidebarCmd(m.openHostSignIn(row.SessionID))
	case sidebarRowRailPaneStart:
		m.RestartRailPane()
	case sidebarRowCustomLink:
		m.queueSidebarCmd(m.OpenLink(row.WindowID))
	case sidebarRowGlobalNew:
		m.SidebarNewGlobalSession()
		return true
	case sidebarRowCollapse:
		m.SidebarToggleCollapsed()
	case sidebarRowRepo:
		m.SidebarToggleRepoCollapsed(row.SessionID)
	case sidebarRowHost:
		m.SidebarToggleHostCollapsed(row.SessionID)
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
		m.queueSidebarCmd(m.fileViewEnterRow(row.WindowIndex, row.WindowID))
	case sidebarRowDivider:
		m.SidebarResetSplit()
	case sidebarRowSession:
		m.sidebarSwitchSession(row.SessionID)
		m.sidebarFollowSession = row.SessionID
	}
	return false
}

// SidebarReorderCursor moves the cursor's session up or down in the rail order
// and persists it, the keyboard equivalent of a drag-reorder. The cursor rides
// with the moved session so successive presses keep moving the same one. On a
// machine's header it moves the machine among the others, the keyboard twin
// of dragging the header; this machine stays first.
func (m *OS) SidebarReorderCursor(delta int) {
	row, ok := m.sidebarCursorRow()
	if !ok {
		return
	}
	if row.Kind == sidebarRowHost {
		m.sidebarReorderHost(row.SessionID, delta)
		return
	}
	if row.Kind != sidebarRowSession && row.Kind != sidebarRowRepo {
		return
	}
	order, blocks := m.sidebarDrawnOrder()
	moved := false
	if row.Kind == sidebarRowRepo {
		order, moved = blocks.stepGroup(order, sidebarRepoBlockKey(row.WindowID, row.SessionID), delta)
	} else {
		order, moved = blocks.stepRow(order, row.SessionID, delta)
	}
	if !moved {
		return
	}
	m.commitSessionOrder(order)
	if row.Kind == sidebarRowSession {
		m.sidebarFollowSession = row.SessionID
	}
}

func (m *OS) sidebarReorderHost(host string, delta int) {
	order := append([]string(nil), m.SidebarHostIDs...)
	from := slices.Index(order, host)
	if from < 0 {
		m.sidebarSayMachinePinned()
		return
	}
	to := from + delta
	if to < 0 || to >= len(order) {
		return
	}
	order[from], order[to] = order[to], order[from]
	m.SidebarHostOrder = order
	m.saveSidebarState()
}

func (m *OS) sidebarSayMachinePinned() {
	m.ShowNotification("This machine stays first. Move another machine instead.",
		"info", m.Settings.NotificationDuration)
}

// SidebarCycleSection walks the cursor forward through the rail's three
// sections, wrapping at the end and skipping any that has no rows.
func (m *OS) SidebarCycleSection() {
	order := [sidebarSectionCount]sidebarRowKind{sidebarRowSession, sidebarRowWindow, sidebarRowAgent}
	here := 0
	if row, ok := m.sidebarCursorRow(); ok {
		for i, k := range order {
			if row.Kind == k {
				here = i
			}
		}
	}
	for step := 1; step <= len(order); step++ {
		if j := m.sidebarFirstRowOfKind(order[(here+step)%len(order)]); j >= 0 {
			m.sidebarSetCursor(j)
			return
		}
	}
}

// SidebarJumpToSession switches to the n-th session (1-based) in the rail and
// moves the cursor there, mirroring a click on that session row.
func (m *OS) SidebarJumpToSession(n int) {
	count := 0
	for i, r := range m.SidebarNav {
		if r.Kind != sidebarRowSession {
			continue
		}
		count++
		if count == n {
			m.sidebarSetCursor(i)
			m.sidebarSwitchSession(r.SessionID)
			return
		}
	}
}

// SidebarOpenCursorMenu opens the context menu for the cursor row, reusing the
// mouse path so a key and a right-click on the same row open the same menu.
//
// It targets the row the cursor is on and nothing else, which is the rule every
// other cursor key in the rail already follows: a key that acts on something
// other than the row under the cursor is a key the user cannot aim.
//
// destructive is what is left of that key's intent, now that the row decides
// the menu: it lands the selection on the row's own destructive action, so the
// kill key still opens on Kill for a session and on Close for a pane. Nothing
// is destroyed without a second keypress either way.
//
// A row that names no target of its own (the footer's toggle, the agents
// header's tokens) says so rather than quietly borrowing a session.
func (m *OS) SidebarOpenCursorMenu(destructive bool) {
	row, ok := m.sidebarCursorRow()
	if !ok {
		return
	}
	if !sidebarRowHasMenu(row) {
		m.ShowNotification("Nothing on this row to act on", "info", m.Settings.NotificationDuration)
		return
	}
	x, y := m.sidebarCursorAnchor(row)
	m.openSidebarContextMenu(sidebarRowHit{
		Kind:        row.Kind,
		SessionID:   row.SessionID,
		WindowID:    row.WindowID,
		WindowIndex: row.WindowIndex,
	}, x, y)
	if destructive && m.ContextMenu != nil {
		m.ContextMenu.selectWarn()
	}
}

func sidebarRowHasMenu(row sidebarNavRow) bool {
	switch row.Kind {
	case sidebarRowSession, sidebarRowHost:
		return row.SessionID != ""
	case sidebarRowWindow, sidebarRowAgent:
		return row.WindowID != "" || row.WindowIndex >= 0
	case sidebarRowFileCd, sidebarRowFileClose, sidebarRowFileUp, sidebarRowFileBack, sidebarRowFileEntry:
		return true
	default:
		return false
	}
}

func (m *OS) sidebarCursorAnchor(row sidebarNavRow) (int, int) {
	for _, h := range m.SidebarHits {
		if sidebarNavRowsEqual(navRowOf(h), row) {
			return h.X0, h.Y0
		}
	}
	x := 0
	if m.railEdge() == sidebarRight {
		x = m.GetRenderWidth() - m.GetSidebarWidth()
	}
	return x, m.viewReserve().Top
}

func (m *OS) sidebarSetCursorToHit(hit sidebarRowHit) {
	target := navRowOf(hit)
	for i, r := range m.SidebarNav {
		if sidebarNavRowsEqual(r, target) {
			m.sidebarCursorWantSet = false
			m.SidebarCursor = i
			return
		}
	}
}

func navRowOf(h sidebarRowHit) sidebarNavRow {
	return sidebarNavRow{
		Kind:        h.Kind,
		SessionID:   h.SessionID,
		WindowID:    h.WindowID,
		WindowIndex: h.WindowIndex,
	}
}

func (m *OS) sidebarCursorWindow() *terminal.Window {
	row, ok := m.sidebarCursorRow()
	if !ok || row.WindowID == "" {
		return nil
	}
	if i := m.windowIndexByID(row.WindowID); i >= 0 {
		return m.Windows[i]
	}
	return nil
}

// SidebarRenameCursor starts a rename on the cursor row, whatever it is. A
// window is renamed locally. A session row renames the session through the
// daemon, which owns its name.
func (m *OS) SidebarRenameCursor() {
	if w := m.sidebarCursorWindow(); w != nil {
		m.BeginRenameWindow(w)
		return
	}
	row, ok := m.sidebarCursorRow()
	if ok && row.Kind == sidebarRowSession && row.SessionID != "" && m.DaemonClient != nil {
		m.BeginRenameSession(row.SessionID)
		return
	}
	m.ShowNotification("Nothing on this row to rename", "info", m.Settings.NotificationDuration)
}

// SidebarCanCreateSession reports whether this client can make a session at
// all. Standalone has no daemon and so no session list, which is why the rail's
// new-session row and its key are absent there rather than dimmed.
func (m *OS) SidebarCanCreateSession() bool {
	return m.DaemonClient != nil
}

// SidebarToggleCollapsed folds the rail down to its glyph strip, or back out to
// the width the user last sized it to. Two states and one control: the old
// three-stop ladder had a middle width no control named and no way back out of
// except by stepping through it, and the responsive clamp already owns that
// width on the screens where it belongs.
func (m *OS) SidebarToggleCollapsed() {
	if m.sidebarDrawing {
		m.SidebarSetCollapsed(!m.secondaryCollapsed)
		return
	}
	m.SidebarSetCollapsed(!m.SidebarCollapsed)
}

// SidebarSetCollapsed is the directed half: the footer's arrow and the keys
// point somewhere, so pressing the same one twice is a no-op rather than a
// flicker.
func (m *OS) SidebarSetCollapsed(collapsed bool) {
	if m.sidebarDrawing {
		if collapsed == m.secondaryCollapsed {
			return
		}
		m.secondaryCollapsed = collapsed
		m.sidebarClearPeek()
		m.tooltipClear()
		if m.AutoTiling {
			m.TileAllWindows()
		} else {
			m.ClampWindowsToView()
		}
		return
	}
	if collapsed == m.SidebarCollapsed {
		return
	}
	if !collapsed && sidebarVariant(m.sidebarWidthFor(m.sidebarStoredWidth())) == sidebarVariantGlyph {
		m.ShowNotification("The screen is too narrow for a wider rail", "info", m.Settings.NotificationDuration)
		return
	}
	m.SidebarCollapsed = collapsed
	m.sidebarClearPeek()
	m.tooltipClear()
	if m.AutoTiling {
		m.TileAllWindows()
	} else {
		m.ClampWindowsToView()
	}
	m.saveSidebarState()
	m.MarkAllDirty()
}

// SidebarNewSessionHere is the rail's "+" on the sessions header. It asks
// which machine when there is more than one, the same way the dock's control
// and the palette entry do, so the three agree about what the verb means.
func (m *OS) SidebarNewSessionHere() {
	m.OpenNewSessionPicker()
}

// SidebarNewSession makes a session on this machine, with no question asked.
// It is what the picker calls once a machine has been chosen, and what the
// rail did before the picker existed.
func (m *OS) SidebarNewSession() {
	if !m.SidebarCanCreateSession() {
		m.ShowNotification("Sessions need the daemon", "info", m.Settings.NotificationDuration)
		return
	}
	m.clearSidebarReturn()
	if m.AttachedHost != "" {
		m.makeSessionHere(false)
		return
	}
	name := m.nextSessionName()
	client, ch := m.DaemonClient, m.sessionCreateChan()
	w, h := m.GetContentWidth(), m.GetUsableHeight()
	go func() {
		ch <- SessionCreatedMsg{Name: name, Err: client.CreateDetachedSession(name, w, h)}
	}()
}

// SidebarNewGlobalSession makes another global session and switches to it,
// which is what the global group's "+" means.
//
// The session is created with no windows. Its first pane is the one the user
// picks a machine for, and the picker is opened for them once the switch has
// landed; see SessionCreatedMsg.
func (m *OS) SidebarNewGlobalSession() {
	if !m.SidebarCanCreateSession() {
		m.ShowNotification("Sessions need the daemon", "info", m.Settings.NotificationDuration)
		return
	}
	m.clearSidebarReturn()
	if m.AttachedHost != "" {
		m.makeSessionHere(true)
		return
	}
	name := m.nextGlobalSessionName()
	client, ch := m.DaemonClient, m.sessionCreateChan()
	w, h := m.GetContentWidth(), m.GetUsableHeight()
	go func() {
		ch <- SessionCreatedMsg{Name: name, Global: true, Err: client.CreateGlobalSession(name, w, h)}
	}()
}

// SidebarNewWindow makes a pane in the session the terminals section is
// listing, which is what its header's "+" means. It is the terminals half of
// the add affordance: the sessions header makes another session, this makes
// another terminal in one.
//
// The section only ever lists the attached session's panes when this is
// reachable: a peek is the pointer's own transient state and is dropped the
// moment the pointer or the cursor leaves the session rows, which is what
// moving to this control does. The guard is here anyway, because a control that
// silently acts on the wrong session is worse than one that says it cannot.
func (m *OS) SidebarNewWindow(sessionID string) {
	if sessionID != "" && sessionID != m.sidebarCurrentSessionID() {
		m.ShowNotification("Attach to that session first", "info", m.Settings.NotificationDuration)
		return
	}
	m.clearSidebarReturn()
	m.NewWindowHere()
}

func (m *OS) sessionCreateChan() chan SessionCreatedMsg {
	if m.PendingSessionCreate == nil {
		m.PendingSessionCreate = make(chan SessionCreatedMsg, 4)
	}
	return m.PendingSessionCreate
}

func (m *OS) nextSessionName() string {
	taken := make(map[string]bool)
	if m.DaemonClient != nil {
		for _, n := range m.DaemonClient.AvailableSessionNames() {
			taken[n] = true
		}
	}
	for i := 0; ; i++ {
		name := fmt.Sprintf("session-%d", i)
		if !taken[name] {
			return name
		}
	}
}

// SidebarAccentCursor opens the colour picker on the cursor row: the pane's own
// accent on a window row, and the session's on a session row, which is the
// colour every pane in it inherits.
func (m *OS) SidebarAccentCursor() {
	if w := m.sidebarCursorWindow(); w != nil {
		m.OpenAccentPicker(w.ID)
		return
	}
	row, ok := m.sidebarCursorRow()
	if ok && row.Kind == sidebarRowSession && row.SessionID != "" {
		m.OpenSessionAccentPicker(row.SessionID)
		return
	}
	m.ShowNotification("Accents work on a pane or a session row", "info", m.Settings.NotificationDuration)
}

// SidebarOpenMail opens the mailbox from the rail: narrowed to the pane under
// the cursor when the cursor is on a pane row, else the whole session's mail.
// The keyboard twin of the agents header's mail token.
func (m *OS) SidebarOpenMail() tea.Cmd {
	if row, ok := m.sidebarCursorRow(); ok && row.WindowID != "" {
		switch row.Kind {
		case sidebarRowAgent, sidebarRowWindow:
			return m.OpenAgentMailForWindow(row.WindowID)
		}
	}
	return m.OpenAgentMail()
}
