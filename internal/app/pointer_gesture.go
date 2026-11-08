package app

// A pointer gesture borrows window-management mode for as long as it lasts,
// then gives back whatever mode it interrupted.
//
// While a button is down on a pane the pointer belongs to the gesture. In
// terminal mode a guest that had asked for mouse tracking would be handed the
// motion and the release, so the drag would neither move the pane nor end.
// Window management is the mode where the pointer manages windows, so that is
// the mode a gesture runs in.
//
// Giving it back is the other half. Resizing a pane or moving one is not a
// request to stop typing. The resize gesture, alt-drag moves and ctrl-drag
// moves share this rule: each is a lease on window management (mode.go),
// which hands back the mode chosen beneath it when it ends.

// BeginPointerGesture puts a starting gesture into window management.
func (m *OS) BeginPointerGesture() {
	m.borrowMode(leaseGesture, WindowManagementMode)
}

// EndPointerGesture hands back the mode the gesture interrupted. Idempotent:
// every path a gesture can end by runs it, and only the first has anything
// to do.
func (m *OS) EndPointerGesture() {
	m.returnMode(leaseGesture)
}

// BeginCtrlDrag puts a committed ctrl-drag into window management. Dropping
// the pane hands back the mode the drag began in: moving a pane is not a
// request to stop typing.
func (m *OS) BeginCtrlDrag() {
	m.borrowMode(leaseCtrlDrag, WindowManagementMode)
}

// EndCtrlDrag hands back the mode a ctrl-drag interrupted. Idempotent.
func (m *OS) EndCtrlDrag() {
	m.returnMode(leaseCtrlDrag)
}
