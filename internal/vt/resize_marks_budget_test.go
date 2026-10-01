package vt_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestResizeCostDoesNotScaleWithMarks is the budget: a guest that leaves
// 1,000 marks in a long line, with 1,000 placements to remap, must not make
// a resize step more than a few times dearer than the same line without
// them. Finding each mark by a walk of its line made it 50 times dearer, so
// a guest could stall a drag.
func TestResizeCostDoesNotScaleWithMarks(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("a timing budget; run without -short and without -race")
	}
	with := testing.Benchmark(vt.BenchmarkResizeMarks1000).NsPerOp()
	without := testing.Benchmark(vt.BenchmarkResizeMarks0).NsPerOp()
	t.Logf("resize step: %d ns with 1,000 marks and placements, %d ns without", with, without)
	if with > 4*without {
		t.Errorf("a resize step costs %d ns with 1,000 marks and placements, over 4 times the %d ns without", with, without)
	}
}
