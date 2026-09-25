package app

import (
	"testing"
)

// A master-stack resize goes through the geometry scan, which moves the pane
// rectangles and nothing else, while the master-stack tiler recomputes every
// rectangle from its ratios on the next retile. These cases resize, retile,
// and check the resize is still there. Before the ratios were written back
// from the geometry, every one of them came back at the split it started at.

// TestTheStackRatioRoundTripsThroughSessionState: the ratios a resize records
// are what BuildSessionState sends and what RestoreFromState adopts, and a
// client that adopts them lays the workspace out the same way.
//
// NEGATIVE CONTROL: drop the WorkspaceStackRatio block from BuildSessionState,
// or the adopt call from RestoreFromState, and the fresh client holds no stack
// ratio.
func TestTheStackRatioRoundTripsThroughSessionState(t *testing.T) {
	m := modeOS(t, LayoutModeMasterStack, true, 0, 3, 160, 40)
	m.FocusedWindow = 0
	m.SetFocusedWindowWidthPercent(35)
	m.FocusedWindow = 1
	m.SetFocusedWindowHeightPercent(30)
	stack, ok := m.WorkspaceStackRatio[m.CurrentWorkspace]
	if !ok {
		t.Fatal("setup: the stack resize recorded no stack ratio")
	}

	state := m.BuildSessionState()
	if got := state.WorkspaceStackRatio[m.CurrentWorkspace]; got != stack {
		t.Fatalf("the state carries stack ratio %v, want %v", got, stack)
	}
	if got := state.WorkspaceMasterRatio[m.CurrentWorkspace]; got != m.MasterRatio {
		t.Fatalf("the state carries master ratio %v, want %v", got, m.MasterRatio)
	}

	fresh := ratioClient(t)
	if err := fresh.RestoreFromState(state); err != nil {
		t.Fatalf("RestoreFromState: %v", err)
	}
	if got := fresh.WorkspaceStackRatio[state.CurrentWorkspace]; got != stack {
		t.Errorf("the restored client holds stack ratio %v, want %v", got, stack)
	}
	if fresh.MasterRatio != m.MasterRatio {
		t.Errorf("the restored client holds master ratio %v, want %v", fresh.MasterRatio, m.MasterRatio)
	}
}
