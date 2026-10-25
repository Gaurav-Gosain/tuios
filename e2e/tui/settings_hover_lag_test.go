package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// hoverLagConfig draws at 240 frames a second. A frame of the settings panel
// on a 400x100 screen takes longer to compose than that period on any machine
// this suite runs on, which is the state the reporter of issue #1058 was in:
// a wide screen, a panel up, and a frame that costs more than the frame rate
// allows.
const hoverLagConfig = "[appearance]\nmax_fps = 240\n"

// hoverLagBudget is how long after the last motion report the row under the
// pointer has to be the highlighted one. A build that draws one frame per
// queued report trailed the pointer by six to twelve seconds here, and a build
// that drains the queue between frames by tens of milliseconds.
const hoverLagBudget = 1500 * time.Millisecond

// TestSettingsHoverKeepsUpWithThePointer sweeps the pointer up and down the
// settings rows for three seconds, at a report every 2 ms, then stops it on a
// row the sweep never touched. That row has to carry the cursor marker within
// hoverLagBudget.
//
// How this could pass wrongly, written down first:
//   - A stale frame from the sweep could put the marker on the last row by
//     chance. The last row is outside the band the sweep crosses, so no frame
//     before the last report can highlight it.
//   - The panel could be narrower or shorter than the band, so the pointer
//     sweeps blank space and no frame is composed for it. The band is checked
//     to be rows of the panel before the sweep: hovering its ends must move the
//     marker there.
//   - A machine fast enough to compose and write a frame inside 4 ms would
//     not show the bug at all. The negative controls in NEGATIVE_CONTROLS.md
//     name the load each one needed: none for a budget counted from the start
//     of the compose, and a loaded machine for one counted from the end of the
//     compose but not of the write.
func TestSettingsHoverKeepsUpWithThePointer(t *testing.T) {
	base := spotlightConfigFile(t, hoverLagConfig)
	term := startIn(t, base, startOpts{cols: 400, rows: 100})
	waitBoot(t, term)
	newWindow(t, term)
	openSettings(t, term)

	anchorRow, labelCol, ok := textAt(term.Screen(), "Focused border color", 0)
	if !ok {
		t.Fatalf("the settings panel has no Focused border color row\n%s", term.Snapshot())
	}
	markerCol := labelCol - 2
	top, bottom := anchorRow-5, anchorRow+5
	marked := func(row int) func(tuitest.Screen) bool {
		return func(s tuitest.Screen) bool { return s.Cell(markerCol, row).Content == "›" }
	}
	for _, row := range []int{top, bottom} {
		mouseHover(t, term, labelCol, row)
		if err := term.WaitFor(marked(row), uiTimeout); err != nil {
			t.Fatalf("hovering row %d did not highlight it, so it is not a settings row: %v\n%s",
				row, err, term.Snapshot())
		}
	}

	// The sweep: rows top to bottom-1 and back, never bottom itself.
	span := bottom - 1 - top
	start := time.Now()
	for i := 0; time.Since(start) < 3*time.Second; i++ {
		k := i % (2 * span)
		if k > span {
			k = 2*span - k
		}
		sendMouseThenWait(t, term, "sweep", tuitest.MouseEvent{
			Col: labelCol + i%7, Row: top + k, Button: tuitest.MouseNone, Action: tuitest.MouseMove,
		}, 2*time.Millisecond)
	}
	if err := term.SendMouse(tuitest.MouseEvent{
		Col: labelCol, Row: bottom, Button: tuitest.MouseNone, Action: tuitest.MouseMove,
	}); err != nil {
		t.Fatalf("last hover: %v", err)
	}
	stopped := time.Now()
	if err := term.WaitFor(marked(bottom), hoverLagBudget); err != nil {
		lag := "never, within the wait"
		if term.WaitFor(marked(bottom), 30*time.Second) == nil {
			lag = time.Since(stopped).Round(time.Millisecond).String()
		}
		t.Fatalf("the row under the pointer was highlighted %s after the pointer stopped; the budget is %s\n%s",
			lag, hoverLagBudget, term.Snapshot())
	}
	t.Logf("the row under the pointer was highlighted %s after the pointer stopped",
		time.Since(stopped).Round(time.Millisecond))
}
