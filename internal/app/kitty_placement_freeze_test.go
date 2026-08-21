package app

import (
	"strings"
	"testing"
)

// TestPlacementFreezeReleasesOnceSizeSettles pins the rule that a placement is
// recomputed whenever the pane's geometry changes, and is held only while it is
// still changing.
//
// The resize freeze exists to coalesce an interactive drag. It used to be armed
// by IsBeingManipulated and released only by that flag going false, comparing
// the live size against the size recorded on the last unfrozen pass - a record
// the frozen passes never update. A gesture whose release is lost (the pointer
// leaving the surface mid-drag, which is why clearStaleManipulation exists, and
// which that sweep skips while InteractionMode is set) therefore leaves the
// pane frozen for good.
//
// Frozen is not idle. The guest is resized, redraws at its new size and keeps
// streaming frames, and every one of those frames is forwarded to the host as a
// fresh bitmap for the same image id. What is not forwarded is a placement, so
// the host keeps scaling each new bitmap into the cell rectangle from before the
// resize: the image stretches, and stays stretched until something clears the
// flag. Clicking the pane does (press sets it, release clears it on every
// window), which is exactly the repair the report describes.
func TestPlacementFreezeReleasesOnceSizeSettles(t *testing.T) {
	_, em, info, refresh := placementHarness(t, 120, 40, 1)

	// Prime at the starting size.
	if got := countCmd(refresh(), "a=p"); got != 1 {
		t.Fatalf("prime: expected one placement, got %d", got)
	}

	// Drag the shared border: the pane narrows every tick.
	info.IsBeingManipulated = true
	for _, w := range []int{110, 100, 90, 80} {
		info.Width = w
		if out := refresh(); countCmd(out, "a=p") != 0 {
			t.Fatalf("placement churned mid-drag at width %d: %q", w, out)
		}
	}

	// The pointer stops. The size has settled at 80 and the guest, resized,
	// now streams frames drawn for the settled pane. The release never
	// arrived, so IsBeingManipulated is still set.
	var settled string
	for frame := range 4 {
		streamBrowserFrame(em, 1, (80-2)*10, (40-2)*20)
		out := refresh()
		if !strings.Contains(out, "a=t") {
			t.Fatalf("frame %d: the new bitmap was not forwarded (%q)", frame, out)
		}
		if p := lastPlacement(out); p != "" {
			settled = p
		}
	}
	if settled == "" {
		t.Fatalf("the pane has been at its settled size for four frames and the host " +
			"has been given four new bitmaps, but no placement was emitted for any of " +
			"them: the host is still showing them in the pre-resize rectangle")
	}

	// The repair: a click clears the flag. The geometry it produces is the
	// correct one, and it must be what the settled passes already emitted.
	info.IsBeingManipulated = false
	streamBrowserFrame(em, 1, (80-2)*10, (40-2)*20)
	want := lastPlacement(refresh())
	if want == "" {
		t.Fatal("no placement after the flag cleared")
	}
	if settled != want {
		t.Fatalf("placement emitted while settled differs from the one a full redraw "+
			"produces:\n settled=%q\n redraw =%q", settled, want)
	}
}
