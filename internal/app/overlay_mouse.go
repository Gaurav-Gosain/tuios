package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
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
	if !ok {
		// Clicked outside every panel: dismiss the frontmost.
		m.closeOverlay(m.topmostOverlayKind())
		return true, nil
	}

	// Clicking a panel brings it to the front.
	m.raiseOverlay(h.Kind)

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
	switch kind {
	case "settings":
		m.SettingsSelected = idx
	case "palette":
		m.CommandPaletteSelected = idx
	case "keybinds":
		m.KeybindMove(idx - m.KeybindSelected())
	case "launcher":
		m.LauncherSelected = idx
	case "themepicker":
		if idx != m.ThemePickerSelected {
			m.ThemePickerMove(idx - m.ThemePickerSelected)
		}
	case "glyphpicker":
		if idx != m.GlyphPickerSelected {
			m.GlyphPickerMove(idx - m.GlyphPickerSelected)
		}
	case "effectpicker":
		// Hover previews, as it does in the other two pickers. The command the
		// move returns is dropped: hover cannot return one, and the preview is
		// already running its own chain of frames, so there is nothing to start.
		if idx != m.EffectPickerSelected {
			_ = m.EffectPickerMove(idx - m.EffectPickerSelected)
		}
	case "dockeditor":
		m.DockEditorSelected = idx
	case "sectioneditor":
		m.SectionEditorSelected = idx
	case "session":
		m.SessionSwitcherSelected = idx
	case "workspace":
		m.WorkspaceSwitcherSelected = idx
	case "aggregate":
		m.AggregateViewSelected = idx
	case "layout":
		m.LayoutPickerSelected = idx
	case "quit":
		m.QuitMenuSelected = idx
	case "sessionclose":
		m.SessionCloseSelected = idx
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
	if !ok {
		h, ok = m.overlayHitByKind(m.topmostOverlayKind())
		if !ok {
			return false
		}
	}
	switch h.Kind {
	case "help":
		if up {
			m.HelpScrollOffset = max(m.HelpScrollOffset-2, 0)
		} else {
			m.HelpScrollOffset += 2 // clamped against row count on next render
		}
	case "settings":
		if up {
			m.SettingsMoveUp()
		} else {
			m.SettingsMoveDown()
		}
	case "palette":
		m.PaletteMove(wheelDelta(up))
	case "keybinds":
		m.KeybindMove(wheelDelta(up))
	case "launcher":
		m.LauncherMove(wheelDelta(up))
	case "themepicker":
		m.ThemePickerMove(wheelDelta(up))
	case "glyphpicker":
		m.GlyphPickerMove(wheelDelta(up))
	case "effectpicker":
		_ = m.EffectPickerMove(wheelDelta(up))
	case "dockeditor":
		m.DockEditorMove(wheelDelta(up))
	case "sectioneditor":
		m.SectionEditorMove(wheelDelta(up))
	case "session":
		n := len(FilterSessionItems(m.SessionSwitcherItems, m.SessionSwitcherQuery))
		moveListSelection(&m.SessionSwitcherSelected, &m.SessionSwitcherScroll, n, 10, wheelDelta(up))
	case "workspace":
		n := len(FilterWorkspaceItems(m.WorkspaceSwitcherItems, m.WorkspaceSwitcherQuery))
		moveListSelection(&m.WorkspaceSwitcherSelected, &m.WorkspaceSwitcherScroll, n, workspaceSwitcherRows, wheelDelta(up))
	case "layout":
		n := len(FilterLayoutTemplates(m.LayoutPickerItems, m.LayoutPickerQuery))
		moveListSelection(&m.LayoutPickerSelected, &m.LayoutPickerScroll, n, 10, wheelDelta(up))
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
		moveListSelection(&m.QuitMenuSelected, &m.QuitMenuScroll, len(m.QuitMenuItems), 10, wheelDelta(up))
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
		m.SettingsCategory = i
		m.SettingsSelected = 0
		m.SettingsScroll = 0
	case "keybinds":
		m.KeybindSetTab(i)
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
	}
}

// overlayRowClick handles a click on a body row of the given overlay.
func (m *OS) overlayRowClick(kind string, row overlayRowHit, lx, ly int) tea.Cmd {
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
	case "palette":
		m.CommandPaletteSelected = row.Idx
		return m.ActivateCommandPalette()
	case "keybinds":
		// Selecting only. Every row here is a finding to read, and the detail box
		// under the list is the thing a click is asking for; there is no action to
		// activate and nothing a stray click could change.
		m.KeybindMove(row.Idx - m.KeybindSelected())
	case "launcher":
		// A click runs the row. Typing it out instead is a keyboard choice: the
		// row has no second control to aim at, and inventing one would put a
		// hit target on every line of a list a few thousand rows long.
		m.LauncherSelected = row.Idx
		return m.LauncherRun(row.Idx)
	case "themepicker":
		m.ThemePickerSelected = row.Idx
		return m.ThemePickerApplySelection()
	case "glyphpicker":
		m.GlyphPickerSelected = row.Idx
		return m.GlyphPickerApplySelection()
	case "effectpicker":
		m.EffectPickerSelected = row.Idx
		return m.EffectPickerApplySelection()
	case "dockeditor":
		m.DockEditorSelected = row.Idx
		return m.DockEditorToggle()
	case "sectioneditor":
		m.SectionEditorSelected = row.Idx
		return m.SectionEditorToggle()
	case "session":
		// A click activates, exactly like Enter on the selected row. Selecting
		// only, as this used to, made the switcher the one list where a click
		// did nothing visible, and it is why "clicking a session does not
		// switch" was reportable at all.
		m.SessionSwitcherSelected = row.Idx
		m.sessionSwitcherActivate(row.Idx)
	case "workspace":
		m.WorkspaceSwitcherSelected = row.Idx
		m.WorkspaceSwitcherActivate(row.Idx)
	case "aggregate":
		// The picker used to return no hit rows at all, so the one overlay whose
		// entire purpose is choosing a window was the one that could not be
		// clicked.
		m.AggregateViewSelected = row.Idx
		m.AggregateViewJump(row.Idx)
	case "layout":
		m.LayoutPickerSelected = row.Idx
		m.layoutPickerActivate(row.Idx)
	case "quit":
		m.QuitMenuSelected = row.Idx
		return m.QuitMenuActivate(row.Idx)
	case "sessionclose":
		m.SessionCloseSelected = row.Idx
		return m.SessionCloseActivate(row.Idx)
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

// sessionSwitcherActivate switches to the session at the given index of the
// filtered switcher list and closes the switcher, mirroring its Enter binding.
func (m *OS) sessionSwitcherActivate(idx int) {
	selected, ok := m.SessionSwitcherTarget(idx)
	if !ok {
		return
	}
	if selected.IsCurrent {
		m.ShowNotification("Already on this session", "info", config.NotificationDuration)
	} else if err := m.SwitchToSession(selected.ID); err != nil {
		m.ShowNotification("Switch failed: "+err.Error(), "error", config.NotificationDuration*2)
	}
	m.closeOverlay("session")
}

// layoutPickerActivate applies the layout at the given index of the filtered
// picker list and closes the picker, mirroring its Enter binding.
func (m *OS) layoutPickerActivate(idx int) {
	filtered := FilterLayoutTemplates(m.LayoutPickerItems, m.LayoutPickerQuery)
	if idx < 0 || idx >= len(filtered) {
		return
	}
	selected := filtered[idx]
	ApplyLayoutTemplate(selected, m)
	m.ShowNotification("Layout applied: "+selected.Name, "success", config.NotificationDuration)
	m.closeOverlay("layout")
}

// closeOverlay dismisses a specific floating overlay by kind.
func (m *OS) closeOverlay(kind string) {
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
	case "palette":
		m.CloseCommandPalette()
	case "launcher":
		m.CloseLauncher()
	case "themepicker":
		// Click-away leaves the previewed theme reverted, matching Esc.
		m.CancelThemePicker()
	case "glyphpicker":
		// Click-away leaves the previewed set reverted, matching Esc.
		m.CancelGlyphPicker()
	case "effectpicker":
		// Click-away closes without changing the row, matching Esc. There is no
		// preview to revert: this picker previews by animating rather than by
		// applying, so nothing was written on the way through.
		m.CancelEffectPicker()
	case "dockeditor":
		// Click-away closes and keeps, matching Esc: the layout is already
		// applied and saved.
		m.CloseDockEditor()
	case "sectioneditor":
		// Click-away closes and keeps, for the same reason.
		m.CloseSectionEditor()
	case "session":
		m.ShowSessionSwitcher = false
		m.SessionSwitcherQuery = ""
		m.SessionSwitcherSelected = 0
		m.SessionSwitcherScroll = 0
	case "workspace":
		m.CloseWorkspaceSwitcher()
	case overlayKindShot:
		// Click-away keeps the file. Only esc discards, because a click that
		// lands outside a panel is as often a miss as an intention, and a
		// deleted screenshot cannot be got back.
		m.CloseScreenshotPreview(false)
	case "layout":
		m.ShowLayoutPicker = false
	case "accent":
		m.CloseAccentPicker()
	case "aggregate":
		m.ShowAggregateView = false
	case "quit":
		m.CloseQuitMenu()
	case "sessionclose":
		// Click-away cancels, like esc. Dismissing is the safe answer, so it is
		// the one an ambiguous gesture gets.
		m.CloseSessionClose()
	}
	if m.OverlayDrag.Kind == kind {
		m.OverlayDrag.Active = false
	}
}
