package tuie2e

import (
	"strings"
	"testing"
	"time"
)

// TestStripSnapsToTheNearestColumn is appearance.niri_scroll_snap: once the
// wheel stops, the strip lines up on the nearest column. niri_scroll_cells is
// 3 here, so a step never lands on a column by itself. The columns are 55
// cells of a 100-cell screen.
//
//  1. One step, 3 cells, is nearer the first column than the second: the strip
//     goes back to where it was, and the frame is the frame before the step.
//  2. Ten steps, 30 cells, is past half of the first column: the strip lines
//     up on the second. BRAVO starts at the screen's left edge, where its top
//     left corner is drawn, and none of ALPHA is on screen.
//
// Negative control: with SnapStrip doing nothing, step 1 leaves the strip 3
// cells along and its frame differs. The run is in NEGATIVE_CONTROLS.md.
func TestStripSnapsToTheNearestColumn(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nniri_scroll_cells = 3\nniri_scroll_snap = true\n")
	a := stripClient(t, base)
	before := stripBand(a)

	// 1. Back to the first column.
	wheelStrip(t, a, 1)
	if after := stripBand(a); after != before {
		t.Fatalf("ASSERTION: one step did not snap back to the first column.\nBEFORE:\n%s\nAFTER:\n%s", before, after)
	}

	// 2. On to the second.
	wheelStrip(t, a, 10)
	deadline := time.Now().Add(uiTimeout)
	var band string
	for time.Now().Before(deadline) {
		band = stripBand(a)
		if strings.Contains(band, "BRAVO") && !strings.Contains(band, "ALPHA") && strings.HasPrefix(band, "╭") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: ten steps did not line the strip up on BRAVO, at the left edge:\n%s", band)
}
