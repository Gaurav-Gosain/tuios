package app

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
