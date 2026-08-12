package app

// A resize gesture borrows window-management mode for as long as it lasts, then
// gives back whatever mode it interrupted.
//
// While a button is down on a pane's edge the pointer belongs to the gesture.
// In terminal mode it did not: a guest that had asked for mouse tracking was
// handed the motion and the release, so the drag neither moved the edge nor
// ended. Window management is the mode where the pointer manages windows, so
// that is the mode a resize runs in.
//
// Giving it back is the other half. A resize is not a request to stop typing,
// any more than a ctrl-drag move is (CtrlDragWasTerminal, the same shape).

// BeginResizeMode puts a starting resize gesture into window management,
// remembering a terminal mode to return to.
func (m *OS) BeginResizeMode() {
	if m.Mode == TerminalMode {
		m.ResizeWasTerminal = true
		m.Mode = WindowManagementMode
	}
}

// EndResizeMode returns the mode the gesture interrupted. Idempotent: every
// path a gesture can end by runs it, and only the first has anything to do.
func (m *OS) EndResizeMode() {
	if m.ResizeWasTerminal {
		m.ResizeWasTerminal = false
		m.Mode = TerminalMode
	}
}
