package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The agents view's layout: the list's width, the side column and the zoom.

// ckListEdge is where the list ends: the end of the region mark while the
// list has the keys.
func ckListEdge(s tuitest.Screen) int {
	x0, x1, ok := ckMarkSpan(s)
	if !ok || x0 > 4 {
		return -1
	}
	return x1
}

// waitListEdge waits for the list to end at want.
func waitListEdge(t *testing.T, term *tuitest.Terminal, want int, when string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return ckListEdge(s) == want }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s: the list ends at %d, want %d: %v\n%s", when, ckListEdge(term.Screen()), want, err, term.Snapshot())
	}
}

// TestCockpitLayout sets the list's width at 200x50: 56 cells by default, >
// and < step it by 4, a drag on its edge sets it, a new client keeps the
// width, and a double click on the edge gives it back to the default. While
// the pointer is on the edge the edge shows a bar and the key bar says to
// drag. w hides the side column and shows it again. Z shows the focus alone
// and esc gives the whole view back with the same region. A second fleet
// with [agents] list_width = "30" draws a 30 cell list.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: with the person's
// width not read from the state file, the new client check fails; with the
// drag not followed, the drag check fails; with w doing nothing, the side
// column check fails; with Z doing nothing, the zoom check fails.
func TestCockpitLayout(t *testing.T) {
	term, base, _, _ := startCockpitFleet(t, 200, 50, nil)
	waitListEdge(t, term, 56, "the default width at 200 columns")
	ckPress(t, term, ">")
	waitListEdge(t, term, 60, "> once")
	ckPress(t, term, "<", "<")
	waitListEdge(t, term, 52, "< twice")

	// The edge is the gap column right of the list. Hover lights it.
	row := 20
	mouseHover(t, term, 53, row)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return s.Cell(53, row).Content == "┃" && strings.Contains(ckKeyBar(s), "drag to resize")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the pointer on the edge did not light it or offer the drag: %q: %v\n%s", ckKeyBar(term.Screen()), err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "layout-edge-hover")
	cursor := ckCursorRow(term.Screen())
	mouseDrag(t, term, 53, row, 71, row, tuitest.MouseLeft, 0)
	waitListEdge(t, term, 70, "a drag of the edge to column 71")
	if got := ckCursorRow(term.Screen()); got != cursor {
		t.Fatalf("ASSERTION: the drag moved the list cursor from %q to %q\n%s", cursor, got, term.Snapshot())
	}
	saveCockpitFrames(t, term, "layout-dragged-70")

	// A new client of the same machine keeps the width.
	ckPress(t, term, "q")
	again := openCockpitClient(t, base, 200, 50, nil)
	waitListEdge(t, again, 70, "a new client after the drag")
	// A double click on the edge gives the width back.
	clickAt(t, again, 71, row, 2)
	waitListEdge(t, again, 56, "a double click on the edge")

	// w hides the side column and shows it again.
	cursorTo(t, again, func(line string) bool { return strings.Contains(line, "bench-idle") })
	if !strings.Contains(again.Screen().Text(), "Last turn") {
		t.Fatalf("the side column is not drawn at 200 columns\n%s", again.Snapshot())
	}
	ckPress(t, again, "w")
	if err := again.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Last turn") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: w did not hide the side column: %v\n%s", err, again.Snapshot())
	}
	saveCockpitFrames(t, again, "layout-side-hidden")
	ckPress(t, again, "w")
	waitText(t, again, "the side column again", "Last turn")

	// Z shows the focus alone; esc gives the view back.
	ckPress(t, again, tuitest.Tab)
	waitRegion(t, again, "Focus", 48, 70, "tab to the focus")
	ckPress(t, again, "Z")
	if err := again.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "release-not") && !strings.Contains(s.Text(), "Last turn") &&
			strings.Contains(s.Text(), "Says:") && strings.Contains(ckKeyBar(s), "unzoom")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: Z did not show the focus alone: %v\n%s", err, again.Snapshot())
	}
	saveCockpitFrames(t, again, "layout-zoom-focus")
	ckPress(t, again, tuitest.Esc)
	if err := again.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "release-not") && strings.Contains(s.Text(), "Last turn") && strings.HasPrefix(ckKeyBar(s), "Focus ")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not give the whole view back on the focus: %v\n%s", err, again.Snapshot())
	}
	alive(t, again, "after the layout keys")

	// [agents] list_width sets the default.
	cfg, _, _, _ := startCockpitFleetWith(t, 200, 50, nil, "[agents]\nlist_width = \"30\"\n")
	waitListEdge(t, cfg, 30, "list_width = 30")
}
