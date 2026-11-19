package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// simpleShot saves the frame as text and as a PNG drawn by tuios.dev/shot.
func simpleShot(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	saveFrame(t, term, name)
	savePNG(t, term.Screen(), shot.XTermPalette(), artifactDir(t), name)
}

// paneCount counts the panes on screen by their top-left corners.
func paneCount(s tuitest.Screen) int {
	return strings.Count(s.Text(), "╭")
}

// TestSimpleProfile is profile = "simple" on the real binary, with nothing
// else in the file. The dock is at the bottom with the button row, keys go
// to the pane with no mode to enter, the Menu button opens the short menu,
// and a workspace is a tab.
//
// Negative control: return at the top of applyProfile. The dock goes back to
// the top with no buttons, and the wait for the Menu button times out.
func TestSimpleProfile(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "profile = \"simple\"\n")
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)

	// The button row, on the last row of the screen.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, rows := s.Size()
		return screenHas(tuitest.Screen(s), "Menu", "New", "Split", "Settings") && strings.Contains(s.Line(rows-1), "Settings")
	}, uiTimeout); err != nil {
		t.Fatalf("the Simple dock never showed its buttons on the bottom row: %v\n%s", err, term.Snapshot())
	}
	clickDockWord(t, term, "New")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return paneCount(s) == 1 }, uiTimeout); err != nil {
		t.Fatalf("the New button opened no pane: %v\n%s", err, term.Snapshot())
	}

	// Typing reaches the shell at once: there is no mode to enter.
	if err := term.SendKeys("echo SIMPLE$((40+2))\r"); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := term.WaitForText("SIMPLE42", uiTimeout); err != nil {
		t.Fatalf("typed keys never reached the shell: %v\n%s", err, term.Snapshot())
	}
	simpleShot(t, term, "simple-typing")

	// The Menu button opens the short menu, and a row runs its action.
	clickDockWord(t, term, "Menu")
	waitMenu(t, term, "the Menu button", "New terminal", "All commands", "Help")
	simpleShot(t, term, "simple-menu")
	row, line := 0, ""
	s := term.Screen()
	_, rows := s.Size()
	for r := range rows {
		if strings.Contains(s.Line(r), "New terminal") {
			row, line = r, s.Line(r)
		}
	}
	leftClick(t, term, len([]rune(line[:strings.Index(line, "New terminal")]))+2, row)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return paneCount(s) == 2 }, uiTimeout); err != nil {
		t.Fatalf("New terminal in the menu opened no pane: %v\n%s", err, term.Snapshot())
	}

	// A workspace is a tab.
	r, l := dockRowWith(t, term, "Menu")
	_ = l
	mouseClick(t, term, 3, r, tuitest.MouseRight, 0)
	waitMenu(t, term, "the tab menu", "Tab 1")
	simpleShot(t, term, "simple-tab-menu")
}

// TestSimpleProfileKeepsYourKeys: a key the file sets wins over the profile.
// With dockbar_position = "top" the Simple dock is on the first row.
func TestSimpleProfileKeepsYourKeys(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "profile = \"simple\"\n\n[appearance]\ndockbar_position = \"top\"\n")
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Line(0), "Settings")
	}, uiTimeout); err != nil {
		t.Fatalf("the dock is not on the first row: %v\n%s", err, term.Snapshot())
	}
}
