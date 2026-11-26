package app

import tea "charm.land/bubbletea/v2"

// quickLookFilesSync is QuickLookSync while the Files view owns the preview:
// it follows the view's cursor, and goes when the view or its look goes.
func (m *OS) quickLookFilesSync() tea.Cmd {
	ql := &m.quickLook
	if !m.explorer.open || !m.explorer.look {
		m.dropQuickLook()
		return nil
	}
	t, ok := m.filesLookTarget()
	if ql.walked {
		// The view's cursor moved to another row: the preview follows it
		// again.
		if !ok || t == ql.walkFrom {
			return nil
		}
		ql.walked, ql.rejoin, ql.inside, ql.dirWant = false, false, false, ""
	}
	if !ok || t == ql.target {
		return nil
	}
	return m.quickLookShow(t)
}

// quickLookCovered reports whether a panel is over the preview, which then
// draws no picture. The Files view is under its own preview.
func (m *OS) quickLookCovered() bool {
	if !m.AnyOverlayOpen() {
		return false
	}
	return !m.quickLook.files || m.topmostOverlayKind() != overlayKindFiles
}
