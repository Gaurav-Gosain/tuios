package app

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// ToggleFloating toggles the focused window between floating and tiled mode.
// A TML surface moves between a popup and a tiled pane instead, with its
// state, through the daemon: see tml_place.go.
func (m *OS) ToggleFloating() {
	fw := m.GetFocusedWindow()
	if m.DrawsTML(fw) {
		m.queueTMLCmd(m.tmlTogglePlacement(fw))
		return
	}
	if fw == nil || fw.IsPopup || fw.IsScratch {
		return
	}

	fw.IsFloating = !fw.IsFloating

	if fw.IsFloating {
		m.liftFromLayout(fw)
		m.ShowNotification("Window: floating", "info", m.Settings.NotificationDuration)
	} else {
		m.addToLayout(fw)
		m.ShowNotification("Window: tiled", "info", m.Settings.NotificationDuration)
	}
}

func (m *OS) liftFromLayout(fw *terminal.Window) {
	if m.AutoTiling {
		if m.UseScrollingLayout {
			sl := m.GetOrCreateScrollingLayout()
			sl.RemoveWindow(m.GetWindowIntID(fw.ID))
			m.ScrollingSetPositions()
		} else if m.UseBSPLayout {
			m.RemoveWindowFromBSPTree(fw)
		} else {
			m.TileAllWindows()
		}
	}
	fw.SetTiled(false)
	fw.InvalidateCache()
	m.RecalcZOrder()
}

func (m *OS) addToLayout(fw *terminal.Window) {
	if m.AutoTiling {
		if m.UseScrollingLayout {
			intID := m.GetWindowIntID(fw.ID)
			sl := m.GetOrCreateScrollingLayout()
			if !sl.HasWindow(intID) {
				sl.AddColumn(intID)
			}
			m.TileAllWindows()
		} else if m.UseBSPLayout {
			m.AddWindowToBSPTree(fw)
		} else {
			m.TileAllWindows()
		}
	}
	fw.InvalidateCache()
	m.RecalcZOrder()
}

func (m *OS) installPassthroughs(window *terminal.Window) {
	window.LockIO()
	m.setupKittyPassthrough(window)
	m.setupSixelPassthrough(window)
	m.setupTextSizingPassthrough(window)
	m.setupReflowRemap(window)
	m.setupClipboardPassthrough(window)
	m.setupNotificationPassthrough(window)
	m.setupNvimNavigation(window)
	m.setupDnD(window)
	window.UnlockIO()
}

func (m *OS) setupClipboardPassthrough(window *terminal.Window) {
	if window == nil {
		return
	}
	windowID := window.ID
	window.ClipboardSetFunc = func(text string) {
		if m.PendingClipboardSet != nil {
			select {
			case m.PendingClipboardSet <- ClipboardSetMsg{Text: text, WindowID: windowID}:
			default:
			}
		}
	}
}

// ToggleMultifocus toggles a window in/out of the multifocus set.
// When multiple windows are in the set, keystrokes are sent to all of them.
//
// A popup cannot join the set. It runs one command and closes, so it is not a
// pane the user types at, and ToggleMultifocusAll leaves popups out for the
// same reason. A popup already in the set can still leave it.
func (m *OS) ToggleMultifocus(windowIndex int) {
	if windowIndex < 0 || windowIndex >= len(m.Windows) {
		return
	}
	window := m.Windows[windowIndex]
	windowID := window.ID
	if window.IsPopup && !m.MultifocusSet[windowID] {
		m.ShowNotification("Cannot add a popup to multifocus", "warning", m.Settings.NotificationDuration)
		return
	}
	if m.MultifocusSet == nil {
		m.MultifocusSet = make(map[string]bool)
	}
	if m.MultifocusSet[windowID] {
		delete(m.MultifocusSet, windowID)
		if len(m.MultifocusSet) == 0 {
			m.MultifocusSet = nil
		}
		m.ShowNotification("Multifocus: removed window", "info", m.Settings.NotificationDuration)
	} else {
		m.MultifocusSet[windowID] = true
		m.showMultifocusCount()
	}
	window.InvalidateCache()
	for _, w := range m.Windows {
		if m.MultifocusSet[w.ID] {
			w.InvalidateCache()
		}
	}
}

// MultifocusPeers is the panes a key or a paste in the focused pane also
// goes to: the members of the multifocus set, other than the focused pane,
// that are on the current workspace and not minimized, in window-list order.
// A member on another workspace, or minimized, is off screen, and the person
// cannot see what the key does there. It stays in the set and gets keys again
// when it is on screen.
func (m *OS) MultifocusPeers() []*terminal.Window {
	if len(m.MultifocusSet) == 0 {
		return nil
	}
	var out []*terminal.Window
	for idx, w := range m.Windows {
		if idx == m.FocusedWindow || w == nil || !m.MultifocusSet[w.ID] ||
			w.Workspace != m.CurrentWorkspace || w.Minimized {
			continue
		}
		out = append(out, w)
	}
	return out
}

func (m *OS) showMultifocusCount() {
	n := 0
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && m.MultifocusSet[w.ID] {
			n++
		}
	}
	msg := fmt.Sprintf("Multifocus: %d windows", n)
	if n == 1 {
		msg = "Multifocus: 1 window"
	}
	m.ShowNotification(msg, "info", m.Settings.NotificationDuration)
}

// ClearMultifocus removes all windows from the multifocus set.
func (m *OS) ClearMultifocus() {
	if m.MultifocusSet != nil {
		for _, w := range m.Windows {
			if m.MultifocusSet[w.ID] {
				w.InvalidateCache()
			}
		}
	}
	m.MultifocusSet = nil
	m.ShowNotification("Multifocus: cleared", "info", m.Settings.NotificationDuration)
}

// ToggleMultifocusAll puts every visible pane on the current workspace in the
// multifocus set. When all of them are in it already, it takes every pane of
// the current workspace out of the set. Members on other workspaces stay.
//
// The panes are the ones the window cycle steps through: minimized panes and
// popups are left out, since neither is a pane the user is typing at.
func (m *OS) ToggleMultifocusAll() {
	panes := m.cyclableWindows()
	if len(panes) == 0 {
		return
	}
	allIn := true
	for _, i := range panes {
		if !m.MultifocusSet[m.Windows[i].ID] {
			allIn = false
			break
		}
	}
	if allIn {
		for _, w := range m.Windows {
			if w.Workspace == m.CurrentWorkspace && m.MultifocusSet[w.ID] {
				delete(m.MultifocusSet, w.ID)
				w.InvalidateCache()
			}
		}
		if len(m.MultifocusSet) == 0 {
			m.MultifocusSet = nil
		}
		m.ShowNotification("Multifocus: cleared", "info", m.Settings.NotificationDuration)
		return
	}
	if m.MultifocusSet == nil {
		m.MultifocusSet = make(map[string]bool)
	}
	for _, i := range panes {
		w := m.Windows[i]
		if !m.MultifocusSet[w.ID] {
			m.MultifocusSet[w.ID] = true
			w.InvalidateCache()
		}
	}
	m.showMultifocusCount()
}

// MultifocusUndimmed reports whether the pane is in the multifocus set and
// appearance.dim_multifocus leaves such panes undimmed. The panes in the set
// take the keys the user types, so they are drawn at full strength like the
// focused pane.
func (m *OS) MultifocusUndimmed(id string) bool {
	return !m.Settings.DimMultifocus && m.MultifocusSet[id]
}

func (m *OS) cyclableWindows() []int {
	out := []int{}
	for i, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsPopup {
			out = append(out, i)
		}
	}
	return out
}

// CycleToNextVisibleWindow cycles focus to the next visible window in the current workspace.
func (m *OS) CycleToNextVisibleWindow() {
	if len(m.Windows) == 0 {
		return
	}
	visibleWindows := m.cyclableWindows()
	if len(visibleWindows) == 0 {
		return
	}

	currentPos := -1
	for i, idx := range visibleWindows {
		if idx == m.FocusedWindow {
			currentPos = i
			break
		}
	}

	if currentPos >= 0 && currentPos < len(visibleWindows)-1 {
		m.FocusWindow(visibleWindows[currentPos+1])
	} else {
		m.FocusWindow(visibleWindows[0])
	}
}

// CycleToPreviousVisibleWindow cycles focus to the previous visible window in the current workspace.
func (m *OS) CycleToPreviousVisibleWindow() {
	if len(m.Windows) == 0 {
		return
	}
	visibleWindows := m.cyclableWindows()
	if len(visibleWindows) == 0 {
		return
	}

	currentPos := -1
	for i, idx := range visibleWindows {
		if idx == m.FocusedWindow {
			currentPos = i
			break
		}
	}

	if currentPos > 0 {
		m.FocusWindow(visibleWindows[currentPos-1])
	} else {
		m.FocusWindow(visibleWindows[len(visibleWindows)-1])
	}
}

func (m *OS) reconcilePrevFocus() {
	var cur string
	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		cur = m.Windows[m.FocusedWindow].ID
	}
	if cur == m.lastFocusedID {
		return
	}
	if m.lastFocusedID != "" && m.windowIndexByID(m.lastFocusedID) >= 0 {
		m.PrevFocusedID = m.lastFocusedID
	}
	m.lastFocusedID = cur
}

// LastPane flips focus back to the window focus came from most recently.
// Alternating presses walk back and forth between the last two panes, because
// focusing the previous window makes the current one previous. Reports whether
// it moved; a dead target (closed pane, nothing recorded yet) says so rather
// than moving somewhere random.
func (m *OS) LastPane() bool {
	m.reconcilePrevFocus()
	if m.PrevFocusedID == "" {
		return false
	}
	idx := m.windowIndexByID(m.PrevFocusedID)
	if idx < 0 {
		return false
	}
	m.FocusWindow(idx)
	return true
}

// FocusWindow sets focus to the window at the specified index.
func (m *OS) FocusWindow(i int) *OS {
	if len(m.Windows) == 0 || i < 0 || i >= len(m.Windows) {
		return m
	}
	if isScratch(m.Windows[i]) && !m.Windows[i].IsPopup && m.Windows[i].Workspace != m.CurrentWorkspace && !m.InScratchView() {
		m.rememberScratchReturn()
		m.scratchViewName = scratchNameOf(m.Windows[i])
	}
	if m.hints != nil && m.Windows[i].ID != m.hints.focusID {
		m.CloseHints()
	}

	m.markFocusedAgentSeen(i)
	m.refoldAgentsOnPaneFocus()

	if m.Windows[i].Workspace != m.CurrentWorkspace {
		m.switchToWorkspace(m.Windows[i].Workspace, i)
	}

	if m.FocusedWindow == i {
		return m
	}

	oldFocused := m.FocusedWindow
	if oldFocused >= 0 && oldFocused < len(m.Windows) {
		m.markAgentSeenAt(m.Windows[oldFocused])
	}

	m.FocusedWindow = i
	m.FocusHistory = session.RecordFocus(m.FocusHistory, m.Windows[i].Workspace, m.Windows[i].ID)
	if m.focusReportedID == "" && oldFocused >= 0 && oldFocused < len(m.Windows) {
		m.focusReportedID = m.Windows[oldFocused].ID
	}
	m.reportFocusChange()

	if m.Windows[i].Workspace == m.CurrentWorkspace {
		m.WorkspaceFocus[m.CurrentWorkspace] = i
	}

	m.RecalcZOrder()

	if oldFocused >= 0 && oldFocused < len(m.Windows) {
		m.Windows[oldFocused].MarkPositionDirty()
	}

	m.Windows[i].InvalidateCache()

	m.ZoomFollowsFocus(i)

	m.FireHook(hooks.AfterFocusChange, m.Windows[i].ID, m.Windows[i].Title())

	if m.AutoTiling && m.UseScrollingLayout && !m.scrollingFocusSyncing {
		m.LogInfo("[SCROLL-FOCUS] FocusWindow(%d) -> triggering ScrollingOnFocusChange (old=%d)", i, oldFocused)
		m.ScrollingOnFocusChange()
	}

	return m
}

// RecalcZOrder renumbers every window's Z so that floating windows sit above
// tiled ones and the focused window sits on top of its band. Call after
// toggling IsFloating or moving the focus.
//
// Within a band the existing stacking order is kept. It used to be renumbered
// by position in the window list, which is creation order, so raising one
// window silently reshuffled the others: with three floating panes stacked
// A, C, B, clicking A put B back under C. Sorting by the current Z first
// means the only window that moves is the one being raised.
func (m *OS) RecalcZOrder() {
	m.recalcZOrderWithTop(nil)
}

// RaiseNewSurfaces renumbers Z as RecalcZOrder does, and then puts the TML
// surfaces among the windows this client has just placed on top of their
// band, above the focused one. A surface a program shows without the focus,
// with tuios tml show --pane, is there to be seen. It otherwise opened under
// the focused window, and with tiling off the person could not see it.
//
// Only surfaces. Any other pane opened without the focus, such as the one
// tmux split-window -d opens, was asked to stay in the background, and
// raising it covered the pane the person was working in.
func (m *OS) RaiseNewSurfaces(placed []*terminal.Window) {
	var surfaces []*terminal.Window
	for _, w := range placed {
		if session.TMLSurfaceID(w.URI) != "" {
			surfaces = append(surfaces, w)
		}
	}
	m.recalcZOrderWithTop(surfaces)
}

func (m *OS) recalcZOrderWithTop(top []*terminal.Window) {
	focused := m.FocusedWindow
	if focused < 0 || focused >= len(m.Windows) {
		focused = -1
	}
	order := make([]int, len(m.Windows))
	for j := range order {
		order[j] = j
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Compare(m.Windows[a].Z, m.Windows[b].Z)
	})
	raised := func(j int) bool { return slices.Contains(top, m.Windows[j]) }
	z := 0
	place := func(floating bool) {
		for _, j := range order {
			if j != focused && !raised(j) && m.Windows[j].IsFloating == floating {
				m.Windows[j].Z = z
				z++
			}
		}
		if focused >= 0 && !raised(focused) && m.Windows[focused].IsFloating == floating {
			m.Windows[focused].Z = z
			z++
		}
		for _, w := range top {
			if w.IsFloating == floating && slices.Contains(m.Windows, w) {
				w.Z = z
				z++
			}
		}
	}
	place(false)
	place(true)
	m.MarkAllDirty()
}

// NewWindowPlacement returns the position and size a freshly created window gets
// on this client: half the usable screen, at the mouse in floating mode and
// centered otherwise. Auto-tiling overwrites it on the next retile; floating mode
// is where it is what the user actually sees.
//
// It is a property of the viewport, which is why the daemon cannot compute it and
// why a window the daemon created arrives marked Unplaced for a client to run
// this on.
func (m *OS) NewWindowPlacement() (x, y, width, height int) {
	leftMargin := m.PaneLeft()
	contentWidth := m.PaneWidth()
	screenHeight := m.PaneHeight()
	if contentWidth <= 0 || screenHeight <= 0 {
		leftMargin = 0
		contentWidth = 80
		screenHeight = 24
	}

	width = contentWidth / 2
	height = screenHeight / 2

	if px, py := m.PointerSeen(); !m.AutoTiling && px > 0 && py > 0 {
		px, py = m.sessionView.toLayout(px, py)
		x = min(px, leftMargin+contentWidth-width)
		y = min(py, screenHeight-height)
		return max(x, leftMargin), max(y, 0), width, height
	}

	homeX, homeY := leftMargin+contentWidth/4, screenHeight/4
	x, y = m.cascadeFrom(homeX, homeY, width, height, leftMargin, contentWidth, screenHeight)
	return x, y, width, height
}

func (m *OS) cascadeFrom(homeX, homeY, width, height, leftMargin, contentWidth, screenHeight int) (x, y int) {
	const (
		stepX = 2
		stepY = 1
		steps = 8
	)

	maxX := leftMargin + contentWidth - width
	maxY := screenHeight - height
	clamp := func(px, py int) (int, int) {
		return max(min(px, maxX), leftMargin), max(min(py, maxY), 0)
	}

	taken := func(px, py int) bool {
		for _, w := range m.Windows {
			if w == nil || w.Workspace != m.CurrentWorkspace || w.Minimized {
				continue
			}
			if w.X == px && w.Y == py {
				return true
			}
		}
		return false
	}

	for i := range steps {
		px, py := clamp(homeX+i*stepX, homeY+i*stepY)
		if !taken(px, py) {
			return px, py
		}
	}
	return clamp(homeX, homeY)
}

// QuitSession performs a deliberate, user-initiated quit. In a daemon session
// that also kills the session, so it records the intent first: the daemon
// announces the session ending and the connection dropping, and either can
// arrive before the program finishes quitting. Update consults QuitRequested so
// those announcements are not mistaken for a session killed from elsewhere,
// which would make a normal exit report an error.
//
// Every deliberate quit path routes through here so they cannot drift apart.
func (m *OS) QuitSession() {
	if m.LearnMode {
		return
	}
	m.QuitRequested = true
	if m.IsDaemonSession && m.DaemonClient != nil {
		_ = m.DaemonClient.KillSession()
	}
	m.Cleanup()
}

// AddWindow adds a new window to the current workspace.
//
// In a daemon session this asks the daemon for the window rather than building
// one: the daemon owns the PTY and the window set, so it creates both and pushes
// the resulting state. Everything a renderer contributes (placement, a slot in
// the tiling layout, the PTY subscription, the terminal content) happens when
// that push lands, in the same code that materializes a window created by a
// script or by another client. That is the point: one creation path, whoever
// asked for it.
// name, when non-empty, becomes the window's CustomName. It is the same argument
// the NewWindow verb takes and it means the same thing on both paths, which it
// did not when the daemon set CustomName and the client set the shell title.
//
// command, when given, is an argv exec'd as the pane's process instead of a
// shell. It rides the same NewWindow request on both paths: locally straight
// into terminal.NewWindow, in a daemon session as the intent args after the
// name, so whichever side spawns the PTY is the side that execs and there is
// no quoting and no pane to find afterwards.
func (m *OS) AddWindow(name string, command ...string) *OS {
	return m.AddWindowIn("", name, command...)
}

// AddWindowIn is AddWindow with the directory the pane's process starts in.
// Empty keeps the default. The side that spawns the process sets it, so no cd
// is typed into the pane afterwards.
func (m *OS) AddWindowIn(dir, name string, command ...string) *OS {
	if m.IsDaemonSession && m.DaemonClient != nil {
		return m.addDaemonWindow(dir, name, "", command)
	}
	return m.addLocalWindow(dir, name, command)
}

func (m *OS) addDaemonWindow(dir, name, sshFrom string, command []string) *OS {
	return m.addDaemonWindowWith(dir, name, sshFrom, command, false)
}

func (m *OS) addDaemonWindowWith(dir, name, sshFrom string, command []string, keepOpen bool) *OS {
	var args []string
	if name != "" || len(command) > 0 {
		args = append([]string{name}, command...)
	}
	ws := 0
	if m.InScratchView() {
		ws = m.CurrentWorkspace
	}
	if m.pendingSplitTarget == "" && m.PreselectionDir != layout.PreselectionNone {
		if fw := m.GetFocusedWindow(); fw != nil {
			m.pendingSplitDir = m.PreselectionDir
			m.pendingSplitTarget = fw.ID
		}
		m.PreselectionDir = layout.PreselectionNone
	}
	cols, rows, _ := m.newPaneContentSize()
	send := func() error {
		if keepOpen && len(command) > 0 {
			return m.DaemonClient.SendRunIntent(dir, ws, cols, rows, true, name, command...)
		}
		return m.DaemonClient.SendNewWindowIntentSized(dir, ws, cols, rows, sshFrom, "NewWindow", args...)
	}
	if err := send(); err != nil {
		m.LogError("Failed to ask the daemon for a new window: %v", err)
		m.windowIntentErr = err
		m.CancelPendingSplit()
	} else {
		m.daemonWindowIntent = true
	}
	return m
}

func (m *OS) addLocalWindow(dir, name string, command []string) *OS {
	newID := createID()
	title := fmt.Sprintf("Terminal %s", newID[:8])

	m.LogInfo("Creating new window: %s (workspace %d)", title, m.CurrentWorkspace)

	x, y, width, height := m.NewWindowPlacement()
	if m.AutoTiling {
		if cols, rows, ok := m.newPaneContentSize(); ok {
			width, height = cols+2, rows+2
		}
	}

	caps := m.hostCaps()
	window, err := terminal.NewWindowInCell(dir, newID, title, x, y, width, height, len(m.Windows), m.WindowExitChan, m.PTYDataChan, m.Settings.ScrollbackLines, caps.CellWidth, caps.CellHeight, command...)
	if err != nil {
		m.LogError("Failed to create window %s: %v", title, err)
		return m
	}

	if caps.CellWidth > 0 && caps.CellHeight > 0 {
		window.SetCellPixelDimensions(caps.CellWidth, caps.CellHeight)
	}

	window.Workspace = m.CurrentWorkspace
	window.CustomName = name
	if m.InScratchView() {
		window.IsScratch = true
		window.ScratchName = m.scratchViewName
		if window.ScratchName == scratchName {
			window.ScratchName = ""
		}
	}

	m.installPassthroughs(window)
	m.setupCwdWatch(window)

	m.Windows = append(m.Windows, window)
	m.LogInfo("Window created successfully: %s (ID: %s, total windows: %d)", title, newID[:8], len(m.Windows))
	m.FireHook(hooks.AfterNewWindow, newID, title)

	if m.AutoTiling && m.UseScrollingLayout {
		m.ScrollingOnWindowAdded(window)
	}

	if m.AutoTiling {
		window.Opening = true
		if m.UseScrollingLayout || !m.UseBSPLayout {
			m.TileAllWindows()
		} else {
			tree := m.GetOrCreateBSPTree()
			if tree != nil {
				m.AddWindowToBSPTree(window)
			} else {
				m.TileAllWindows()
			}
		}
	}

	m.FocusWindow(len(m.Windows) - 1)
	m.RevealFocusedColumn()

	return m
}

func (m *OS) applyStartupTiling() {
	if m.UserConfig == nil || !m.UserConfig.Startup.Tiled || m.AutoTiling {
		return
	}
	m.layoutInstant = true
	defer func() { m.layoutInstant = false }()
	m.ApplyLayoutModeName(m.UserConfig.Startup.Layout)
	m.ToggleAutoTiling()
}

func (m *OS) applyStartupPreferences() {
	if m.forceTerminalMode {
		m.pendingStartTerminalMode = true
		m.maybeEnterPendingTerminalMode()
	}
	if m.UserConfig == nil {
		return
	}
	if len(m.Windows) > 0 && !m.sessionUnarranged {
		return
	}

	s := m.UserConfig.Startup

	m.applyStartupTiling()

	opened := s.OpenDefaultWindow && len(m.Windows) == 0
	if opened {
		m.AddWindow("")
	}

	if s.StartInTerminalMode && (opened || m.hasFocusedWindow()) {
		m.pendingStartTerminalMode = true
		m.maybeEnterPendingTerminalMode()
	}
}

func (m *OS) hasFocusedWindow() bool {
	return m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows)
}

func (m *OS) maybeEnterPendingTerminalMode() {
	if !m.pendingStartTerminalMode || !m.hasFocusedWindow() {
		return
	}
	m.pendingStartTerminalMode = false
	m.EnterTerminalMode()
}

// UpdateAllWindowThemes updates the terminal colors for all windows when the theme changes
func (m *OS) UpdateAllWindowThemes() {
	m.LogInfo("Updating terminal colors for all windows after theme change")
	for _, window := range m.Windows {
		if window != nil {
			window.UpdateThemeColors()
		}
	}
}

// DeleteWindow removes the window at the specified index.
//
// In a daemon session this asks the daemon to close it rather than closing it
// here: the window set and the PTY are the daemon's, so it removes the window,
// kills the shell, repairs focus and pushes the result. This client tears down
// its own copy when that push lands, in the same code that handles a window
// closed by a script or by another client.
func (m *OS) DeleteWindow(i int) *OS {
	if len(m.Windows) == 0 || i < 0 || i >= len(m.Windows) {
		m.LogWarn("Cannot delete window: invalid index %d (total windows: %d)", i, len(m.Windows))
		return m
	}

	if m.IsDaemonSession && m.DaemonClient != nil {
		windowID := m.Windows[i].ID
		if err := m.DaemonClient.SendIntent("CloseWindow", windowID); err != nil {
			m.LogError("Failed to ask the daemon to close window %s: %v", shortID(windowID), err)
		} else {
			m.daemonWindowIntent = true
		}
		return m
	}

	deletedWindow := m.Windows[i]
	wasZoomed := deletedWindow.Zoomed
	deletedWasPopup := deletedWindow.IsPopup
	m.clearNvimNavigation(deletedWindow.ID)
	m.dndWindowClosed(deletedWindow.ID)
	m.pipSourceClosed(deletedWindow)
	m.LogInfo("Deleting window: %s (index: %d, ID: %s)", deletedWindow.Title(), i, shortID(deletedWindow.ID))

	if deletedWindow.DaemonMode && deletedWindow.PTYID != "" && m.DaemonClient != nil {
		m.DaemonClient.UnsubscribePTY(deletedWindow.PTYID)
		if err := m.DaemonClient.ClosePTY(deletedWindow.PTYID); err != nil {
			m.LogError("Failed to close daemon PTY: %v", err)
		}
	}

	windowIntID := m.GetWindowIntID(deletedWindow.ID)
	deletedWorkspace := deletedWindow.Workspace

	if m.WindowToBSPID != nil {
		delete(m.WindowToBSPID, deletedWindow.ID)
		if m.BSPIDToWindowID != nil {
			delete(m.BSPIDToWindowID, windowIntID)
		}
		m.LogInfo("BSP: Removed ID mapping for window %s (int ID %d)", shortID(deletedWindow.ID), windowIntID)
	}

	if m.SixelPassthrough != nil {
		m.SixelPassthrough.ClearWindow(deletedWindow.ID)
	}
	m.kittySymbols.forget(deletedWindow.ID)
	if m.KittyPassthrough != nil {
		m.KittyPassthrough.OnWindowClose(deletedWindow.ID)
		if data := m.KittyPassthrough.FlushPending(); len(data) > 0 {
			m.KittyPassthrough.WriteToHost(data)
		}
	}

	if len(m.MultifocusSet) > 0 {
		delete(m.MultifocusSet, deletedWindow.ID)
		if len(m.MultifocusSet) == 0 {
			m.MultifocusSet = nil
		}
	}

	deletedWindow.Close()

	cleanedAnimations := make([]*ui.Animation, 0, len(m.Animations))
	animsCleaned := 0
	for _, anim := range m.Animations {
		if anim.Window != deletedWindow {
			cleanedAnimations = append(cleanedAnimations, anim)
		} else {
			animsCleaned++
		}
	}
	m.Animations = cleanedAnimations
	if animsCleaned > 0 {
		m.LogInfo("Cleaned up %d animations for deleted window", animsCleaned)
	}

	movedZ := deletedWindow.Z
	for j := range m.Windows {
		if m.Windows[j].Z > movedZ {
			m.Windows[j].Z--
			m.Windows[j].InvalidateCache()
		}
	}

	m.Windows = slices.Delete(m.Windows, i, i+1)
	m.FocusHistory = session.RemoveFocus(m.FocusHistory, deletedWindow.ID)
	m.closeStaleHints()

	deletedWindow = nil

	m.LogInfo("Window deleted successfully (remaining windows: %d)", len(m.Windows))

	if len(m.Windows) == 0 {
		m.FocusedWindow = -1
		m.LogInfo("No windows remaining, switching to window management mode")
		m.ChooseMode(WindowManagementMode, ModeCauseNoPane)
	} else if i < m.FocusedWindow {
		m.FocusedWindow--
	} else if i == m.FocusedWindow {
		m.FocusMostRecentVisibleWindow()
	}

	if m.AutoTiling {
		if m.UseScrollingLayout {
			if windowIntID > 0 {
				m.ScrollingOnWindowRemoved(windowIntID)
			}
		} else {
			if owner := m.WorkspaceTrees[deletedWorkspace]; owner != nil && windowIntID > 0 {
				owner.RemoveWindow(windowIntID)
				m.openOrphanStacks()
				m.LogInfo("BSP: Removed window from the workspace %d tree, which now has %d windows",
					deletedWorkspace, owner.WindowCount())
				if owner.IsEmpty() {
					m.LogInfo("BSP: Tree is now empty, clearing workspace tree")
					m.WorkspaceTrees[deletedWorkspace] = nil
				}
			}

			hasVisibleInWorkspace := false
			for _, w := range m.Windows {
				if w.Workspace == m.CurrentWorkspace && !w.Minimized {
					hasVisibleInWorkspace = true
					break
				}
			}
			if hasVisibleInWorkspace {
				m.TileAllWindows()
			}
		}
	}

	if wasZoomed && !deletedWasPopup {
		if fw := m.GetFocusedWindow(); fw != nil && !fw.IsPopup && !fw.Zoomed {
			m.settleSizes(func() {
				m.requireRealLayout()
				if m.zoomPane(fw) && m.AutoTiling {
					m.tileAllWindows()
				}
				m.FlushPTYBuffersAfterResize()
				m.MarkAllDirty()
			})
		}
	}

	m.SyncStateToDaemon()

	return m
}

// FocusMostRecentVisibleWindow restores the most recently focused visible pane
// on this workspace. If history predates this feature, it falls back to the
// existing deterministic first-visible rule.
func (m *OS) FocusMostRecentVisibleWindow() {
	for _, id := range m.FocusHistory[m.CurrentWorkspace] {
		for i, w := range m.Windows {
			if w.ID == id && w.Workspace == m.CurrentWorkspace && !w.Minimized {
				m.FocusWindow(i)
				return
			}
		}
	}
	m.FocusNextVisibleWindow()
}

// GetFocusedWindow returns the currently focused window.
func (m *OS) GetFocusedWindow() *terminal.Window {
	if len(m.Windows) > 0 && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		if m.Windows[m.FocusedWindow].Workspace == m.CurrentWorkspace {
			return m.Windows[m.FocusedWindow]
		}
	}
	return nil
}
