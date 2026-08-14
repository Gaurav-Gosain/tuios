package app

import "testing"

// TestSaturatedSwitchNoResize isolates the workspace-switch route over a pane
// whose scrollback cap is already full, with no resize anywhere. If the two
// copies come back offset here, the shift the resized-while-producing shape
// reports is a property of priming a saturated survivor, not of the resize
// ordering that shape exists to test.
func TestSaturatedSwitchNoResize(t *testing.T) {
	r := newRig(t, 1)
	ptyID := r.win(0).PTYID
	r.feedPTY(ptyID, `printf 'SAT-READY\n'`, "SAT-READY")
	r.feedPTY(ptyID, `A=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA; `+
		`i=1; while [ $i -le 20000 ]; do echo "SAT-$i-$A$A$A$A-END"; i=$((i+1)); done; `+
		`echo SAT-DONE`, "SAT-DONE")
	r.settle()

	r.m.SwitchToWorkspace(2)
	r.m.SwitchToWorkspace(1)

	r.settle()
	r.converge(ptyID)
	compareSides(t, r, ptyID)
}
