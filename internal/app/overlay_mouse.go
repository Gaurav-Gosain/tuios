package app

import (
	tea "charm.land/bubbletea/v2"
)

// OverlayActive reports whether any hit-testable floating overlay panel is on
// screen. Used by the mouse handlers to consume events before the window layer.
func (m *OS) OverlayActive() bool {
	return len(m.OverlayHits) > 0
}

// OverlayDragActive reports whether an overlay panel is being dragged.
func (m *OS) OverlayDragActive() bool {
	return m.OverlayDrag.Active
}

// overlayHitAt returns the highest-z overlay panel containing screen (x, y).
func (m *OS) overlayHitAt(x, y int) (overlayPanelHit, bool) {
	best := overlayPanelHit{}
	found := false
	for _, h := range m.OverlayHits {
		if x >= h.OriginX && x < h.OriginX+h.Geo.Width && y >= h.OriginY && y < h.OriginY+h.Geo.Height {
			if !found || h.Z > best.Z {
				best, found = h, true
			}
		}
	}
	return best, found
}

// topmostOverlayKind returns the kind of the frontmost open overlay, or "".
func (m *OS) topmostOverlayKind() string {
	if len(m.OverlayZOrder) == 0 {
		return ""
	}
	return m.OverlayZOrder[len(m.OverlayZOrder)-1]
}

// inboxCovered reports whether a panel is open over the agents view. The
// view fills the screen, so a pointer outside that panel still lands on the
// view: it belongs to the panel on top, not to the rows under it.
func (m *OS) inboxCovered() bool {
	stack := m.OverlayStack()
	return len(stack) > 0 && stack[len(stack)-1] != "inbox"
}

// overlayHitByKind returns the recorded hit geometry for a kind.
func (m *OS) overlayHitByKind(kind string) (overlayPanelHit, bool) {
	for _, h := range m.OverlayHits {
		if h.Kind == kind {
			return h, true
		}
	}
	return overlayPanelHit{}, false
}

// OverlayMouseClick routes a click at absolute screen (x, y) to the topmost
// overlay panel under the cursor: a right-click or click on chrome starts a
// drag of that panel, a left click hits a tab/row/control, and a click that
// lands on no panel dismisses the topmost. Returns whether the event was
// consumed and any command produced (e.g. running a palette entry).
func (m *OS) OverlayMouseClick(x, y int, right bool) (bool, tea.Cmd) {
	if len(m.OverlayHits) == 0 {
		return false, nil
	}

	h, ok := m.overlayHitAt(x, y)
	if !ok || h.Kind == "inbox" && m.inboxCovered() {
		// Clicked outside every panel, or on the agents view outside the
		// panel over it: dismiss the frontmost. The row under the pointer
		// is not selected.
		m.closeOverlay(m.topmostOverlayKind())
		return true, nil
	}

	// Clicking a panel brings it to the front.
	m.raiseOverlay(h.Kind)

	// The cockpit fills the screen and is not dragged: a click selects, a
	// second one goes. See cockpit.go.
	if h.Kind == "inbox" {
		if right {
			return true, nil
		}
		cmd, _ := m.CockpitClick(x, y)
		return true, cmd
	}

	// A click on the open draft of the panel places its cursor or selects.
	if !right && m.draftClick(h.Kind, x, y) {
		return true, nil
	}

	lx, ly := x-h.OriginX, y-h.OriginY

	// Right-click anywhere on the panel grabs it for dragging.
	if right {
		m.startOverlayDrag(h.Kind, lx, ly)
		return true, nil
	}

	// Left-click on a tab switches section. The strip's overflow arrows step to
	// the neighbouring section, which is the tab the run would scroll to anyway.
	switch {
	case h.Geo.TabPrev.Contains(lx, ly):
		m.stepOverlayTab(h.Kind, -1)
		return true, nil
	case h.Geo.TabNext.Contains(lx, ly):
		m.stepOverlayTab(h.Kind, 1)
		return true, nil
	}
	for i, r := range h.Geo.Tabs {
		if !r.Empty() && r.Contains(lx, ly) {
			m.setOverlayTab(h.Kind, i)
			return true, nil
		}
	}

	// The accent picker is a field of cells rather than a list of rows, so it
	// routes off its own recorded geometry. Ahead of the row loop because it
	// registers no rows for the generic path to find.
	if h.Kind == "accent" {
		if handled, cmd := m.accentPickerPress(lx, ly); handled {
			return true, cmd
		}
	}

	// Left-click on a body row selects/activates it.
	for _, row := range h.Rows {
		if row.Rect.Contains(lx, ly) {
			return true, m.overlayRowClick(h.Kind, row, lx, ly)
		}
	}

	// Left-click on any other part of the panel (title, padding, footer, blank
	// space) grabs it for dragging, so the panel is easy to move.
	m.startOverlayDrag(h.Kind, lx, ly)
	return true, nil
}

// startOverlayDrag begins dragging the given panel, remembering the grab point
// within it so it tracks the cursor.
func (m *OS) startOverlayDrag(kind string, lx, ly int) {
	m.OverlayDrag = overlayDragState{Active: true, Kind: kind, OffsetX: lx, OffsetY: ly}
}

// OverlayMouseMotion routes mouse motion to the overlay layer. A panel grabbed
// by its chrome keeps dragging; otherwise the panel under the cursor
// highlights the row the pointer is on, so the row a click would run is always
// the row under the cursor, exactly as the keyboard selection would be.
// Returns true when the motion was consumed (a drag is in progress or the
// cursor is over a panel), so the pane underneath never sees it.
func (m *OS) OverlayMouseMotion(x, y int) bool {
	if m.OverlayDrag.Active {
		h, ok := m.overlayHitByKind(m.OverlayDrag.Kind)
		if !ok {
			return true // still dragging; geometry will be back next frame
		}
		rw, rh := m.GetRenderWidth(), m.GetRenderHeight()
		centerX := (rw - h.Geo.Width) / 2
		centerY := (rh - h.Geo.Height) / 2
		m.setOverlayOffset(m.OverlayDrag.Kind, x-m.OverlayDrag.OffsetX-centerX, y-m.OverlayDrag.OffsetY-centerY)
		return true
	}

	if len(m.OverlayHits) == 0 {
		return false
	}
	h, ok := m.overlayHitAt(x, y)
	if !ok {
		return false
	}
	if h.Kind == "inbox" {
		// A panel over the view keeps the hover off the rows under it.
		if !m.inboxCovered() {
			m.CockpitHover(x, y)
		}
		return true
	}
	lx, ly := x-h.OriginX, y-h.OriginY
	if h.Kind == "accent" {
		// Only a held button paints in the picker. Bare hover must not, or the
		// colour under the pointer would keep overwriting the one the user
		// chose just by crossing the dialog on the way somewhere else.
		m.accentPickerDragTo(lx, ly)
		return true
	}
	for _, row := range h.Rows {
		if row.Rect.Contains(lx, ly) {
			m.overlayRowHover(h.Kind, row.Idx)
			break
		}
	}
	return true
}

// overlayRowHover moves an overlay's selection to the hovered row. It selects
// only, never activates: the theme picker's live preview counts as selection
// because that is what its keyboard selection does too.
func (m *OS) overlayRowHover(kind string, idx int) {
	if p := listPickerOfKind(kind); p != nil {
		// A hover cannot return a command; a preview runs its own frames.
		_ = p.hover(m, idx)
		return
	}
	switch kind {
	case "settings":
		m.SettingsSelected = idx
	case "keybinds":
		m.KeybindMove(idx - m.KeybindSelected())
	case "themeeditor":
		m.themeEditor.cursor[m.themeEditor.tab] = idx
	case "dockeditor":
		m.DockEditorSelected = idx
	case "sectioneditor":
		m.SectionEditorSelected = idx
	case "agentmail":
		m.AgentMailSelect(idx)
	case "inbox":
		m.InboxSelect(idx)
	case overlayKindBuffers:
		m.BufferChooserSelect(idx)
	case overlayKindTransfers:
		m.TransfersSelect(idx)
	case overlayKindCopyDialog:
		m.CopyDialogSelect(idx)
	case overlayKindPluginApproval:
		m.PluginApprovalSelect(idx)
	case overlayKindIntegrationAsk:
		m.IntegrationAskSelect(idx)
	case "navigator":
		m.NavigatorSelect(idx)
	case "quit":
		m.QuitMenuSelected = idx
	case "sessionclose":
		m.SessionCloseSelected = idx
	case overlayKindRestart:
		if m.restart.phase == restartConfirm {
			m.restart.selected = idx
		}
	case overlayKindLogs:
		m.LogSelected = idx
	case "filedialog":
		// Hover moves the cursor onto the answer under the pointer. It cannot
		// run one: the dialog is answered by a click or by enter, and a pointer
		// crossing the rows on its way somewhere else must not delete a file.
		m.filePrompt.Selected = idx
	}
}

// OverlayMouseRelease ends any in-progress overlay drag.
func (m *OS) OverlayMouseRelease() {
	m.OverlayDrag.Active = false
	m.accentDragging = false
	m.accentDrag = accentHitNone
}

// OverlayMouseWheel scrolls the overlay panel under the cursor (falling back to
// the topmost panel). Returns whether it was consumed.
func (m *OS) OverlayMouseWheel(x, y int, up bool) bool {
	h, ok := m.overlayHitAt(x, y)
	if !ok || h.Kind == "inbox" && m.inboxCovered() {
		// Off every panel, or on the view under a panel: the wheel moves the
		// panel on top.
		h, ok = m.overlayHitByKind(m.topmostOverlayKind())
		if !ok {
			return false
		}
	}
	// A wheel move stops at the ends of a list. See listWraps.
	m.wheelMoving = true
	defer func() { m.wheelMoving = false }()
	if p := listPickerOfKind(h.Kind); p != nil {
		_ = p.wheel(m, up)
		return true
	}
	switch h.Kind {
	case "help":
		if up {
			m.HelpScrollOffset = max(m.HelpScrollOffset-2, 0)
		} else {
			m.HelpScrollOffset += 2 // clamped against row count on next render
		}
	case "settings":
		m.SettingsMove(wheelDelta(up))
	case "keybinds":
		m.KeybindMove(wheelDelta(up))
	case "dockeditor":
		m.DockEditorMove(wheelDelta(up))
	case "sectioneditor":
		m.SectionEditorMove(wheelDelta(up))
	case "agentmail":
		m.AgentMailMove(wheelDelta(up))
	case "inbox":
		m.CockpitWheel(x, y, up)
	case overlayKindBuffers:
		m.BufferChooserMove(wheelDelta(up))
	case overlayKindTransfers:
		m.TransfersMove(wheelDelta(up))
	case "navigator":
		m.NavigatorMove(wheelDelta(up))
	case "sessionclose":
		m.SessionCloseMove(wheelDelta(up))
	case overlayKindRestart:
		m.DaemonRestartMove(wheelDelta(up))
	case "filedialog":
		m.FileConfirmMove(wheelDelta(up))
	case "accent":
		// The wheel drives whatever is under it: the strip turns the hue, the
		// grid steps through lightness.
		lx, ly := x-h.OriginX, y-h.OriginY
		if hit, ok := m.accentHitAt(lx, ly); ok && hit.Kind == accentHitHue {
			m.AccentPickerMoveHue(wheelDelta(up))
		} else {
			m.AccentPickerMoveCell(0, wheelDelta(up))
		}
	case "quit":
		m.QuitMenuMove(wheelDelta(up))
	case overlayKindLogs:
		m.LogViewerMove(wheelDelta(up))
	case overlayKindMessage:
		m.MessageViewScroll(wheelDelta(up))
	default:
		return false
	}
	return true
}

// wheelDelta maps a wheel direction to a selection delta.
func wheelDelta(up bool) int {
	if up {
		return -1
	}
	return 1
}

// setOverlayTab switches the active section tab of the overlay.
func (m *OS) setOverlayTab(kind string, i int) {
	switch kind {
	case "help":
		m.HelpCategory = i
		m.HelpScrollOffset = 0
	case "settings":
		// A tab clicked during a search is a request for that tab, so the
		// search gives way to it.
		m.settingsSearch = settingsSearchState{}
		m.SettingsCategory = i
		m.SettingsSelected = 0
		m.SettingsScroll = 0
	case "keybinds":
		m.KeybindSetTab(i)
	case "themeeditor":
		m.ThemeEditorTab(i - m.themeEditor.tab)
	}
}

// stepOverlayTab moves the overlay's active section by delta. The arrow that
// calls this is only drawn while there is a section that way, so the neighbour
// always exists.
func (m *OS) stepOverlayTab(kind string, delta int) {
	switch kind {
	case "help":
		m.setOverlayTab(kind, m.HelpCategory+delta)
	case "settings":
		m.setOverlayTab(kind, m.SettingsCategory+delta)
	case "keybinds":
		m.setOverlayTab(kind, m.KeybindTab+delta)
	case "themeeditor":
		m.ThemeEditorTab(delta)
	}
}

// overlayRowClick handles a click on a body row of the given overlay.
func (m *OS) overlayRowClick(kind string, row overlayRowHit, lx, ly int) tea.Cmd {
	if p := listPickerOfKind(kind); p != nil {
		return p.click(m, row.Idx)
	}
	switch kind {
	case "settings":
		m.SettingsSelected = row.Idx
		items := m.settingsCurrentItems()
		if row.Idx < len(items) {
			switch items[row.Idx].Control {
			case controlString:
				// A click anywhere on a text row opens its inline editor.
				m.SettingsBeginEdit()
				return nil
			case controlStatus:
				// A status row has nothing to step, so a click anywhere on it
				// is the row's enter.
				return m.SettingsActivate()
			case controlColor:
				// And anywhere on a colour row, swatch included, opens its picker.
				// The row's rect is the one the renderer recorded as it drew, so
				// this lands where the user is pointing whatever the panel reflowed
				// to.
				return m.SettingsActivate()
			}
		}
		switch {
		case !row.Dec.Empty() && row.Dec.Contains(lx, ly):
			return m.SettingsAdjust(-1)
		case !row.Inc.Empty() && row.Inc.Contains(lx, ly):
			return m.SettingsAdjust(1)
		case settingsControlSpanContains(row, lx, ly):
			// A click on the value itself, between the stepper arrows, does what
			// Enter does: toggles a bool, cycles an enum, or runs the row's
			// activate hook (the Theme row opens the picker).
			return m.SettingsActivate()
		}
	case "keybinds":
		// Selecting only. Every row here is a finding to read, and the detail box
		// under the list is the thing a click is asking for; there is no action to
		// activate and nothing a stray click could change.
		m.KeybindMove(row.Idx - m.KeybindSelected())
	case "themeeditor":
		m.themeEditor.cursor[m.themeEditor.tab] = row.Idx
		m.ThemeEditorPick()
	case "dockeditor":
		m.DockEditorSelected = row.Idx
		return m.DockEditorToggle()
	case "sectioneditor":
		m.SectionEditorSelected = row.Idx
		return m.SectionEditorToggle()
	case "agentmail":
		// A click opens, exactly like enter on the selected row.
		m.AgentMailSelect(row.Idx)
		return m.AgentMailOpenSelected()
	case "inbox":
		if m.Inbox.agents.on {
			// A click with the bulk allow's sheet up closes the sheet,
			// as any key but enter does, and goes nowhere.
			if m.Inbox.agents.sheet != nil {
				m.Inbox.agents.sheet = nil
				return nil
			}
			m.InboxSelect(row.Idx)
			return m.InboxAgentsGo()
		}
		// A click goes, exactly like enter on the selected row. A click on a
		// group heading selects nothing and so does nothing.
		if rows := m.inboxRows(); row.Idx < 0 || row.Idx >= len(rows) || rows[row.Idx].item == nil {
			return nil
		}
		m.InboxSelect(row.Idx)
		return m.InboxActivate()
	case overlayKindBuffers:
		return m.BufferChooserActivate(row.Idx)
	case overlayKindTransfers:
		// A click puts the cursor on the copy; a copy that waits for an
		// answer opens its question.
		m.TransfersSelect(row.Idx)
		if r, ok := m.selectedTransfer(); ok && r.State == "conflict" {
			m.TransfersAnswer()
		}
	case overlayKindCopyDialog:
		return m.CopyDialogActivate(row.Idx)
	case overlayKindPluginApproval:
		return m.PluginApprovalActivate(row.Idx)
	case overlayKindIntegrationAsk:
		return m.IntegrationAskActivate(row.Idx)
	case "navigator":
		// A click on a session or a workspace row folds it, and a click on
		// a pane goes to it, as enter does.
		m.NavigatorSelect(row.Idx)
		if !m.NavigatorClickFolds(row.Idx) {
			m.NavigatorActivate(row.Idx)
			// A session on another machine opens off the Update goroutine.
			return m.TakeSidebarCmd()
		}
	case "quit":
		m.QuitMenuSelected = row.Idx
		return m.QuitMenuActivate(row.Idx)
	case "sessionclose":
		m.SessionCloseSelected = row.Idx
		return m.SessionCloseActivate(row.Idx)
	case overlayKindRestart:
		return m.DaemonRestartActivate(row.Idx)
	case "filedialog":
		m.filePrompt.Selected = row.Idx
		return m.FileConfirmActivate(row.Idx)
	case overlayKindLogs:
		// A click opens the entry in full, exactly like enter on it.
		m.LogSelected = row.Idx
		m.LogViewerOpenSelected()
	}
	return nil
}

// settingsControlSpanContains reports whether panel-relative (lx, ly) falls on
// a settings row's control area: the whole span from the left stepper arrow to
// the right one, so the value between them is clickable, not only the arrows.
func settingsControlSpanContains(row overlayRowHit, lx, ly int) bool {
	left := row.Inc
	if !row.Dec.Empty() {
		left = row.Dec
	}
	if left.Empty() || row.Inc.Empty() {
		return false
	}
	return ly >= left.Y0 && ly < left.Y1 && lx >= left.X0 && lx < row.Inc.X1
}

// layoutPickerActivate applies the layout at the given index of the filtered
// picker list and closes the picker, mirroring its Enter binding.
func (m *OS) layoutPickerActivate(idx int) {
	filtered := m.layoutPickerRows()
	if idx < 0 || idx >= len(filtered) {
		return
	}
	selected := filtered[idx]
	ApplyLayoutTemplate(selected, m)
	m.ShowNotification("Layout applied: "+selected.Name, "success", m.Settings.NotificationDuration)
	m.CloseLayoutPicker()
}

// closeOverlay dismisses a specific floating overlay by kind.
func (m *OS) closeOverlay(kind string) {
	if p := listPickerOfKind(kind); p != nil {
		// A click away has nowhere to return a command; the save a cancel
		// returns is the same file the next save writes.
		_ = p.close(m)
		return
	}
	switch kind {
	case "help":
		m.ShowHelp = false
		m.HelpCategory = -1
		m.HelpSearchMode = false
		m.HelpSearchQuery = ""
		m.HelpScrollOffset = 0
	case "settings":
		m.CloseSettings()
	case "keybinds":
		m.CloseKeybindManager()
	case "dockeditor":
		// Click-away closes and keeps, matching Esc: the layout is already
		// applied and saved.
		m.CloseDockEditor()
	case "sectioneditor":
		// Click-away closes and keeps, for the same reason.
		m.CloseSectionEditor()
	case "agentmail":
		m.CloseAgentMail()
	case "inbox":
		m.CloseInbox()
	case overlayKindBuffers:
		m.CloseBufferChooser()
	case overlayKindTransfers:
		m.CloseTransfers()
	case overlayKindCopyDialog:
		m.CloseCopyDialog()
	case overlayKindPluginApproval:
		// Click-away is not now: the plugin keeps waiting.
		m.ClosePluginApproval()
	case overlayKindIntegrationAsk:
		// Click-away is not now: nothing is written.
		m.CloseIntegrationAsk()
	case "worktreeprompt":
		// Click-away cancels, matching Esc: nothing was asked yet, so nothing
		// is lost.
		m.WorktreePromptCancel()
	case "navigator":
		m.CloseNavigator()
	case overlayKindShot:
		// Click-away keeps the file. Only esc discards, because a click that
		// lands outside a panel is as often a miss as an intention, and a
		// deleted screenshot cannot be got back.
		m.CloseScreenshotPreview(false)
	case "accent":
		m.CloseAccentPicker()
	case "quit":
		m.CloseQuitMenu()
	case "sessionclose":
		// Click-away cancels, like esc. Dismissing is the safe answer, so it is
		// the one an ambiguous gesture gets.
		m.CloseSessionClose()
	case overlayKindRestart:
		// The same rule. A failure that left no daemon is the exception:
		// CloseDaemonRestart quits then, and an ambiguous gesture must not.
		if !(m.restart.phase == restartFailed && m.restart.lost) {
			_ = m.CloseDaemonRestart()
		}
	case "filedialog":
		// Same rule, and it matters more here: the ambiguous gesture must never
		// be the one that removes a file.
		m.FilePromptCancel()
	case overlayKindLogs:
		m.CloseLogViewer()
	case overlayKindMessage:
		m.CloseMessageView()
	}
	if m.OverlayDrag.Kind == kind {
		m.OverlayDrag.Active = false
	}
}
