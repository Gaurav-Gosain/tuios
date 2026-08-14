package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
)

// TestZoomKeepsItsRegionWhenAPendingResizeDrains is the regression test for a
// zoomed pane shrinking back to its tile on the next tick.
//
// A host resize records the size each pane is to be told and defers the
// announcement, because the size is still moving. Zoom then sets the focused
// pane's rectangle directly. It did so without retiring the deferral, so the
// pending entry was drained a tick later and resized the pane back to the tile
// it had before the zoom, while the other panes stayed hidden for the zoom. The
// frame showed the zoomed pane occupying a fraction of the region and the rest
// of it blank, and the guest was told a size it never had.
func TestZoomKeepsItsRegionWhenAPendingResizeDrains(t *testing.T) {
	tgt, err := newFuzzTarget(fuzzScratch(t))
	if err != nil {
		t.Fatal(err)
	}
	f := tgt.(*fuzzOS)
	t.Cleanup(f.Close)
	if err := f.Reset(); err != nil {
		t.Fatal(err)
	}

	for _, a := range []fuzz.Action{
		{Kind: fuzz.Resize, A: 60, B: 20},
		{Kind: fuzz.ZoomPane},
	} {
		if err := f.Apply(a); err != nil {
			t.Fatal(err)
		}
	}

	zoomed := f.m.GetFocusedWindow()
	if zoomed == nil || !zoomed.Zoomed {
		t.Fatal("the fixture did not leave a pane zoomed")
	}
	width, height := zoomed.Width, zoomed.Height
	told := *f.told[zoomed.ID]

	// The tick that drains what the resize deferred.
	if err := f.Apply(fuzz.Action{Kind: fuzz.Tick}); err != nil {
		t.Fatal(err)
	}

	if zoomed.Width != width || zoomed.Height != height {
		t.Errorf("the zoomed pane is %dx%d a tick later, it took the region as %dx%d",
			zoomed.Width, zoomed.Height, width, height)
	}
	if got := *f.told[zoomed.ID]; got.calls != told.calls {
		t.Errorf("the guest was told %dx%d again after the zoom had settled", got.w, got.h)
	}

	// The frame is where the user sees it: a zoomed pane hides the others, so
	// every row of the content region belongs to it and none may be blank.
	rows := strings.Split(stripANSIForTrace(lipgloss.Sprint(f.m.GetCanvas(true).Render())), "\n")
	top := f.m.GetTopMargin()
	for y := top; y < top+f.m.GetUsableHeight() && y < len(rows); y++ {
		if strings.TrimSpace(rows[y]) == "" {
			t.Fatalf("frame row %d is blank while a pane is zoomed over the whole region", y)
		}
	}
}
