package session

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestPTYProgressParking checks the hand-off the VT callback uses: a parked state
// is returned once and collapses a burst to the newest value.
func TestPTYProgressParking(t *testing.T) {
	p := &PTY{}
	if _, ok := p.takeAgentProgress(); ok {
		t.Fatal("takeAgentProgress reported a state with none parked")
	}

	// Clear is state 0, so it has to survive the "nothing parked" encoding.
	p.storeAgentProgress(vt.ProgressClear, 0)
	if state, ok := p.takeAgentProgress(); !ok || state != vt.ProgressClear {
		t.Fatalf("parked clear came back as (%d, %v), want (0, true)", state, ok)
	}
	if _, ok := p.takeAgentProgress(); ok {
		t.Fatal("a taken state was returned twice")
	}

	p.storeAgentProgress(vt.ProgressNormal, 40)
	p.storeAgentProgress(vt.ProgressError, 55)
	if state, ok := p.takeAgentProgress(); !ok || state != vt.ProgressError {
		t.Fatalf("a burst came back as (%d, %v), want the newest (error, true)", state, ok)
	}
}
