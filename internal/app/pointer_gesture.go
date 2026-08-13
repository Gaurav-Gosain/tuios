package app

// A pointer gesture borrows window-management mode for as long as it lasts,
// then gives back whatever mode it interrupted.
//
// While a button is down on a pane the pointer belongs to the gesture. In
// terminal mode it did not: a guest that had asked for mouse tracking was handed
// the motion and the release, so the drag neither moved the pane nor ended.
// Window management is the mode where the pointer manages windows, so that is
// the mode a gesture runs in.
//
// Giving it back is the other half. Resizing a pane or moving one is not a
// request to stop typing. This started as the resize gesture's own bargain and
// is now the one alt-drag moves take too, rather than each gesture keeping a
// flag of its own.

// BeginPointerGesture puts a starting gesture into window management,
// remembering a terminal mode to return to. Guarded, so the flag doubles as
// "a restore is owed".
func (m *OS) BeginPointerGesture() {
	if m.Mode == TerminalMode {
		m.pointerGestureWasTerminal = true
		m.Mode = WindowManagementMode
	}
}

// EndPointerGesture returns the mode the gesture interrupted. Idempotent: every
// path a gesture can end by runs it, and only the first has anything to do.
func (m *OS) EndPointerGesture() {
	if m.pointerGestureWasTerminal {
		m.pointerGestureWasTerminal = false
		m.Mode = TerminalMode
	}
}
