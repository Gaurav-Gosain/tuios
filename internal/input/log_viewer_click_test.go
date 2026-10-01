package input

import "testing"

// The log viewer is modal to the keyboard but was not modal to the mouse: a
// click anywhere fell through the popup and focused the pane underneath it.
// While it is up the desktop is inert, the same contract its keys follow.
func TestClickOnLogViewerDoesNotFocusUnderlyingPanes(t *testing.T) {
	o, _, _ := twoPaneBSP(t)
	for i := range 30 {
		o.Log("INFO", "line %d", i)
	}
	o.ShowLogs = !o.ShowLogs
	if !o.ShowLogs {
		t.Fatal("viewer did not open")
	}

	cx, cy := contentCell(o.Windows[1])
	clickPane(o, cx, cy)

	if o.FocusedWindow != 0 {
		t.Fatalf("focus moved to %d; a click through the viewer must not focus a pane", o.FocusedWindow)
	}
	if !o.ShowLogs {
		t.Fatal("a plain click closed the viewer")
	}
}

// A click when the viewer is closed behaves as before: it focuses the pane.
func TestClickWithoutLogViewerStillFocusesPanes(t *testing.T) {
	o, _, right := twoPaneBSP(t)
	idx := indexOf(o, right)
	cx, cy := contentCell(right)

	clickPane(o, cx, cy)

	if o.FocusedWindow != idx {
		t.Fatalf("focus = %d, want the clicked pane %d", o.FocusedWindow, idx)
	}
}
