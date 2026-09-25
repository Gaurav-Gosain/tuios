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

// TestAPaneOnAnotherMachineSaysSoOnItsFrame.
//
// Negative control: dropping the host prefix from getWindowTitle leaves the two
// titles identical and this fails.
func TestAPaneOnAnotherMachineSaysSoOnItsFrame(t *testing.T) {
	s := hostTitleSettings()
	w := &terminal.Window{ID: "w1", CustomName: "deploy", Host: "build"}

	got := getWindowTitle(w, 1, 40, s)
	if !strings.Contains(got, "build") {
		t.Errorf("a pane running on another machine does not name it: %q", got)
	}
	if !strings.Contains(got, "deploy") {
		t.Errorf("the window's own name was lost: %q", got)
	}
}

// TestAPaneWhoseLinkIsLostSaysSoOnItsFrame: while the far machine keeps the
// process through a dropped link, the frame says reconnecting in words, ahead
// of the name so truncation keeps it.
func TestAPaneWhoseLinkIsLostSaysSoOnItsFrame(t *testing.T) {
	s := hostTitleSettings()
	w := &terminal.Window{ID: "w1", CustomName: "a-rather-long-window-name", Host: "build", HostLink: "reconnecting"}
	got := ansi.Strip(getWindowTitle(w, 1, 30, s))
	if !strings.HasPrefix(strings.TrimSpace(got), "[reconnecting] build") {
		t.Errorf("a pane whose link is lost does not say so first: %q", got)
	}
	w.HostLink = ""
	if got := getWindowTitle(w, 1, 30, s); strings.Contains(got, "reconnecting") {
		t.Errorf("a pane whose link is up says reconnecting: %q", got)
	}
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

// TestAnUnnamedPaneOnAnotherMachineStillSaysWhere. A window with no title of
// its own is the common case for a fresh pane, and it is exactly the one a
// person cannot place from anything else on screen.
func TestAnUnnamedPaneOnAnotherMachineStillSaysWhere(t *testing.T) {
	s := hostTitleSettings()
	w := &terminal.Window{ID: "w1", Host: "build"}

	if got := getWindowTitle(w, 1, 40, s); !strings.Contains(got, "build") {
		t.Errorf("an unnamed pane on another machine says nothing about where it is: %q", got)
	}
}
