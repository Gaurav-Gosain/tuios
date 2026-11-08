package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// How TestCockpitHeaderChipsFilter could pass wrongly, written down first:
//   - The counts could be taken from the filtered list, so every other chip
//     goes to 0 and leaves the header, and the person cannot switch to it.
//     The test reads every count while a chip filters.
//   - A click could land on the wrong chip, or on a list row. The test clicks
//     working, then to review, and reads which heading is left each time.
//   - A second click on the active chip could leave the filter on. It must
//     give the whole list back.
//   - esc could skip the chip (the list stays filtered) or close the view.
//     The first esc must clear the chip and keep the view.
//   - The filter could miss the bands of a grouping by session, so working
//     rows hide under Group: state only. The test turns on session grouping
//     with the chip on.

// TestCockpitHeaderChipsFilter clicks the header's counts at 200x50. A click
// on "6 working" keeps the Working group alone and marks the chip, the other
// counts stay drawn, a click on "3 to review" moves the filter there, and a
// second click gives the whole list back. Then the chip with session grouping
// keeps only the working rows, and esc clears the chip with the view open.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: with the header taking
// its counts from the filtered rows again, the other counts leave the header.
func TestCockpitHeaderChipsFilter(t *testing.T) {
	term, _, _, _ := startCockpitFleet(t, 200, 50, nil)
	counts := func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "6 working", "3 to review", "2 idle")
	}
	waitFor(t, term, "the header counts", counts)
	saveCockpitFrames(t, term, "chips-all")

	click := func(text string) {
		t.Helper()
		row, col := findText(t, term, text)
		mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	}
	only := func(keep string) func(tuitest.Screen) bool {
		return func(s tuitest.Screen) bool {
			if ckHeadingRow(s, keep) < 0 || !counts(s) {
				return false
			}
			for _, h := range []string{"Needs you", "Working", "Ready for review", "Idle"} {
				if h != keep && ckHeadingRow(s, h) >= 0 {
					return false
				}
			}
			return true
		}
	}

	click("6 working")
	if err := term.WaitFor(only("Working"), uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click on 6 working did not keep the Working group alone with every count drawn: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "chips-working")

	click("3 to review")
	if err := term.WaitFor(only("Ready for review"), uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click on 3 to review did not move the filter: %v\n%s", err, term.Snapshot())
	}
	click("3 to review")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return ckHeadingRow(s, "Working") >= 0 && ckHeadingRow(s, "Ready for review") >= 0 && ckHeadingRow(s, "Idle") >= 0
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a second click on the active chip did not give the whole list back: %v\n%s", err, term.Snapshot())
	}

	// The chip filters the bands of a grouping too.
	click("6 working")
	waitFor(t, term, "the Working group alone", only("Working"))
	ckPress(t, term, "s", "2")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Group: session") && counts(s) &&
			onlyWorkingRows(s)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the working chip did not filter the session bands to the six working agents: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "chips-working-by-session")

	ckPress(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return ckHeadingRow(s, "Needs you") >= 0 && strings.Contains(s.Text(), "Group: session")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not clear the chip with the view open: %v\n%s", err, term.Snapshot())
	}
	ckPress(t, term, "s", "1")
	alive(t, term, "after the header chips")
}

// onlyWorkingRows reports whether the list shows every working agent of the
// fleet and none of the others.
func onlyWorkingRows(s tuitest.Screen) bool {
	for _, name := range cockpitWorking {
		if fleetRowWith(s, name) < 0 {
			return false
		}
	}
	for _, name := range []string{"api-auth", "docs-cli", "vt-sixel", "scratch", "release-not"} {
		if fleetRowWith(s, name) >= 0 {
			return false
		}
	}
	return ckHeadingRow(s, "Needs you") < 0
}
