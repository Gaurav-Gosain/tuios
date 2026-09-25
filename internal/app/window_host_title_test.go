package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// A pane whose shell runs on another machine has to be tellable from one whose
// shell is here. Nothing else on the frame differs, and the same typed line
// means different things on the two, so this is a safety marker rather than a
// label.

func hostTitleSettings() *config.Settings {
	s := config.DefaultSettings()
	return &s
}

// TestANarrowBarGivesUpTheNameAndKeepsTheMachine, and keeps the whole title
// inside the width it was given.
//
// Both halves matter and only the second was ever in doubt. Truncation takes
// from the end, so a prefix survives it however the two are ordered; what does
// not survive is a badge built wider than the bar, because layoutBorderRow
// drops a badge that does not fit rather than trimming it. Joining the machine
// on after the fit had been measured would have made the pane most worth
// marking the one that ends up with no title at all.
//
// Negative control: moving the join below the truncation fails here at the
// width check, at 30 columns against a bar of 24.
func TestANarrowBarGivesUpTheNameAndKeepsTheMachine(t *testing.T) {
	s := hostTitleSettings()
	const width = 24
	long := "a-window-name-far-longer-than-the-bar-can-show"
	// A machine name long enough to matter. A short one hides the fault: the
	// truncation already reserves six columns, so "build:" happens to fit in
	// the reserve whichever way round the two are joined.
	w := &terminal.Window{ID: "w1", CustomName: long, Host: "workstation"}

	got := getWindowTitle(w, 1, width, s)
	if !strings.Contains(got, "workstation") {
		t.Errorf("the machine was truncated away on a narrow bar: %q", got)
	}
	if strings.Contains(got, long) {
		t.Errorf("ASSERTION: the name was not truncated at this width, so this proves nothing: %q", got)
	}
	if w := ansi.StringWidth(got); w > width {
		t.Errorf("the title is %d columns wide in a bar of %d, so the badge is dropped whole: %q", w, width, got)
	}
}
