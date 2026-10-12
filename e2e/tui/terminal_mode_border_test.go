package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTerminalModeRecolorsTheFocusedBorder is issue #642: pressing i on an idle
// pane changed the mode pill at once, but the focused border kept the window
// mode colour until the guest drew something. The renderer reused the pane's
// cached layer, which was drawn with the old border, because nothing marked the
// pane dirty on the way into terminal mode.
//
// The pane runs an idle /bin/sh, so nothing but the mode switch can repaint
// it. Every assertion is the colour the terminal was given for the pane's top
// left corner. Both directions are checked, and the two colours have to
// differ, or the first half would pass on a theme that draws both modes alike.
//
// It runs with the shipped looks. With the pre-v0.8.0 looks the suite pins,
// something else repaints the pane on the switch and the bug does not show.
//
// Frames: before.styled.txt (window mode), terminal.styled.txt (after i) and
// after.styled.txt (after Alt+Esc), under artifactDir.
func TestTerminalModeRecolorsTheFocusedBorder(t *testing.T) {
	term, _ := start(t, startOpts{env: []string{"COLORTERM=truecolor"}, shippedLooks: true})
	waitBoot(t, term)
	newWindow(t, term)

	dir := artifactDir(t)

	// The window mode colour, read once the pane has stopped changing.
	var windowInk tuitest.Color
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		ink, ok := screenBorderInk(s)
		if !ok {
			return false
		}
		same := ink == windowInk
		windowInk = ink
		return same
	}, uiTimeout); err != nil {
		t.Fatalf("no settled pane corner in window mode: %v\n%s", err, term.Snapshot())
	}
	if windowInk.Kind != tuitest.ColorRGB {
		t.Fatalf("the border is not drawn in an RGB colour (%+v), so the modes cannot be told apart", windowInk)
	}
	// The pane must be quiet before i, so a late prompt redraw cannot repaint
	// the border for the mode switch.
	time.Sleep(500 * time.Millisecond)
	saveArtifact(t, term, dir, "before")

	enterTerminalMode(t, term)
	err := term.WaitFor(func(s tuitest.Screen) bool {
		ink, ok := screenBorderInk(s)
		return ok && ink != windowInk
	}, 3*time.Second)
	saveArtifact(t, term, dir, "terminal")
	if err != nil {
		t.Fatalf("the focused border kept the window mode colour %+v after entering terminal mode: %v\n%s",
			windowInk, err, term.SnapshotStyled())
	}
	terminalInk := borderInk(t, term)
	if terminalInk.Kind != tuitest.ColorRGB {
		t.Fatalf("the terminal mode border is not an RGB colour (%+v)", terminalInk)
	}

	leaveTerminalMode(t, term)
	err = term.WaitFor(func(s tuitest.Screen) bool {
		ink, ok := screenBorderInk(s)
		return ok && ink == windowInk
	}, 3*time.Second)
	saveArtifact(t, term, dir, "after")
	if err != nil {
		t.Fatalf("the focused border is %+v after leaving terminal mode, want the window mode colour %+v: %v\n%s",
			borderInk(t, term), windowInk, err, term.SnapshotStyled())
	}
	t.Logf("window mode border %+v, terminal mode border %+v; frames in %s", windowInk, terminalInk, dir)
}
