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

func (m *OS) topmostOverlayKind() string {
	if len(m.OverlayZOrder) == 0 {
		return ""
	}
	return m.OverlayZOrder[len(m.OverlayZOrder)-1]
}

func (m *OS) inboxCovered() bool {
	stack := m.OverlayStack()
	return len(stack) > 0 && stack[len(stack)-1] != "inbox"
}

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
		m.closeOverlay(m.topmostOverlayKind())
		return true, nil
	}

	m.raiseOverlay(h.Kind)

	if h.Kind == "inbox" {
		if right {
			m.OpenCockpitSelectionMenu(x, y)
			return true, nil
		}
		cmd, _ := m.CockpitClick(x, y)
		return true, cmd
	}

	if !right && m.draftClick(h.Kind, x, y) {
		return true, nil
	}

	lx, ly := x-h.OriginX, y-h.OriginY

	if right {
		m.startOverlayDrag(h.Kind, lx, ly)
		return true, nil
	}

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

	if h.Kind == "accent" {
		if handled, cmd := m.accentPickerPress(lx, ly); handled {
			return true, cmd
		}
	}

	for _, row := range h.Rows {
		if row.Rect.Contains(lx, ly) {
			return true, m.overlayRowClick(h.Kind, row, lx, ly)
		}
	}

	m.startOverlayDrag(h.Kind, lx, ly)
	return true, nil
}

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
			return true
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
		if !m.inboxCovered() {
			m.CockpitHover(x, y)
		}
		return true
	}
	lx, ly := x-h.OriginX, y-h.OriginY
	if h.Kind == "accent" {
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

func (m *OS) overlayRowHover(kind string, idx int) {
	if p := listPickerOfKind(kind); p != nil {
		_ = p.hover(m, idx)
		return
	}
	switch kind {
	case "settings":
		m.settingsList.Cursor = idx
	case "keybinds":
		m.KeybindSelect(idx)
	case "themeeditor":
		m.themeEditor.lists[m.themeEditor.tab].Cursor = idx
	case "dockeditor":
		m.dockList.Cursor = idx
	case "sectioneditor":
		m.sectionList.Cursor = idx
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
		m.quitList.Select(idx, len(m.QuitMenuItems), listOverlayRows)
	case "sessionclose":
		m.closeList.Select(idx, m.sessionCloseRowCount(), m.sessionCloseRowCount())
	case overlayKindRestart:
		if m.restart.phase == restartConfirm {
			m.restart.list.Cursor = idx
		}
	case overlayKindLogs:
		m.logList.Cursor = idx
	case "filedialog":
		m.filePrompt.List.Cursor = idx
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
		h, ok = m.overlayHitByKind(m.topmostOverlayKind())
		if !ok {
			return false
		}
	}
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
			m.HelpScrollOffset += 2
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
		if m.restart.scrollMax > 0 {
			m.DaemonRestartScroll(3 * wheelDelta(up))
		} else {
			m.DaemonRestartMove(wheelDelta(up))
		}
	case "filedialog":
		m.FileConfirmMove(wheelDelta(up))
	case "accent":
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

func wheelDelta(up bool) int {
	if up {
		return -1
	}
	return 1
}

func (m *OS) setOverlayTab(kind string, i int) {
	switch kind {
	case "help":
		m.HelpCategory = i
		m.HelpScrollOffset = 0
	case "settings":
		m.settingsSearch = settingsSearchState{}
		m.SettingsCategory = i
		m.settingsList.Cursor = 0
		m.settingsList.Scroll = 0
	case "keybinds":
		m.KeybindSetTab(i)
	case "themeeditor":
		m.ThemeEditorTab(i - m.themeEditor.tab)
	}
}

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

func (m *OS) overlayRowClick(kind string, row overlayRowHit, lx, ly int) tea.Cmd {
	if p := listPickerOfKind(kind); p != nil {
		return p.click(m, row.Idx)
	}
	switch kind {
	case "settings":
		m.settingsList.Cursor = row.Idx
		items := m.settingsCurrentItems()
		if row.Idx < len(items) {
			switch items[row.Idx].Control {
			case controlString:
				m.SettingsBeginEdit()
				return nil
			case controlStatus:
				return m.SettingsActivate()
			case controlColor:
				return m.SettingsActivate()
			}
		}
		switch {
		case !row.Dec.Empty() && row.Dec.Contains(lx, ly):
			return m.SettingsAdjust(-1)
		case !row.Inc.Empty() && row.Inc.Contains(lx, ly):
			return m.SettingsAdjust(1)
		case settingsControlSpanContains(row, lx, ly):
			return m.SettingsActivate()
		}
	case "keybinds":
		m.KeybindSelect(row.Idx)
	case "themeeditor":
		m.themeEditor.lists[m.themeEditor.tab].Cursor = row.Idx
		m.ThemeEditorPick()
	case "dockeditor":
		m.dockList.Cursor = row.Idx
		return m.DockEditorToggle()
	case "sectioneditor":
		m.sectionList.Cursor = row.Idx
		return m.SectionEditorToggle()
	case "agentmail":
		m.AgentMailSelect(row.Idx)
		return m.AgentMailOpenSelected()
	case "inbox":
		if m.Inbox.agents.on {
			if m.Inbox.agents.sheet != nil {
				m.Inbox.agents.sheet = nil
				return nil
			}
			m.InboxSelect(row.Idx)
			return m.InboxAgentsGo()
		}
		if rows := m.inboxRows(); row.Idx < 0 || row.Idx >= len(rows) || rows[row.Idx].item == nil {
			return nil
		}
		m.InboxSelect(row.Idx)
		return m.InboxActivate()
	case overlayKindBuffers:
		return m.BufferChooserActivate(row.Idx)
	case overlayKindTransfers:
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
		m.NavigatorSelect(row.Idx)
		if !m.NavigatorClickFolds(row.Idx) {
			m.NavigatorActivate(row.Idx)
			return m.TakeSidebarCmd()
		}
	case "quit":
		m.quitList.Select(row.Idx, len(m.QuitMenuItems), listOverlayRows)
		return m.QuitMenuActivate(row.Idx)
	case "sessionclose":
		m.closeList.Select(row.Idx, m.sessionCloseRowCount(), m.sessionCloseRowCount())
		return m.SessionCloseActivate(row.Idx)
	case overlayKindRestart:
		return m.DaemonRestartActivate(row.Idx)
	case "filedialog":
		m.filePrompt.List.Cursor = row.Idx
		return m.FileConfirmActivate(row.Idx)
	case overlayKindLogs:
		m.logList.Cursor = row.Idx
		m.LogViewerOpenSelected()
	}
	return nil
}

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

func (m *OS) closeOverlay(kind string) {
	if p := listPickerOfKind(kind); p != nil {
		_ = p.close(m)
		return
	}
	switch kind {
	case "help":
		m.ShowHelp = false
		m.HelpCategory = -1
		m.HelpSearchMode = false
		m.helpSearch.Set("")
		m.HelpScrollOffset = 0
	case "settings":
		m.CloseSettings()
	case "keybinds":
		m.CloseKeybindManager()
	case "dockeditor":
		m.CloseDockEditor()
	case "sectioneditor":
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
		m.ClosePluginApproval()
	case overlayKindIntegrationAsk:
		m.CloseIntegrationAsk()
	case "worktreeprompt":
		m.WorktreePromptCancel()
	case "navigator":
		m.CloseNavigator()
	case overlayKindShot:
		m.CloseScreenshotPreview(false)
	case "accent":
		m.CloseAccentPicker()
	case "quit":
		m.CloseQuitMenu()
	case "sessionclose":
		m.CloseSessionClose()
	case overlayKindRestart:
		if !(m.restart.phase == restartFailed && m.restart.lost) {
			_ = m.CloseDaemonRestart()
		}
	case "filedialog":
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
