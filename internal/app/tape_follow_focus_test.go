package app

import "testing"

// TestTapeFollowFocusIdleIsFree: tapeFollowFocus runs after every message,
// so on a client whose focused pane and folder did not change it must
// allocate nothing and return no command.
//
// Negative control: without the early return for an unchanged pane and
// folder, every call schedules a check and the test fails.
func TestTapeFollowFocusIdleIsFree(t *testing.T) {
	m := idleOS(t, 3)
	m.Windows[m.FocusedWindow].Cwd = "/home/someone/project"
	if cmd := m.tapeFollowFocus(); cmd == nil {
		t.Fatal("the first look at a known folder scheduled no check")
	}
	if allocs := testing.AllocsPerRun(1000, func() {
		if m.tapeFollowFocus() != nil {
			t.Fatal("an unchanged pane and folder scheduled a check")
		}
	}); allocs != 0 {
		t.Fatalf("tapeFollowFocus allocated %v times per call on an idle client, want 0", allocs)
	}
}

// BenchmarkTapeFollowFocusIdle is the cost tapeFollowFocus adds to every
// message on a client whose focused pane and folder did not change.
func BenchmarkTapeFollowFocusIdle(b *testing.B) {
	m := idleOS(b, 3)
	m.Windows[m.FocusedWindow].Cwd = "/home/someone/project"
	m.tapeFollowFocus()
	b.ReportAllocs()
	for b.Loop() {
		m.tapeFollowFocus()
	}
}
