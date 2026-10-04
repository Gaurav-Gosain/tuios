package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// layoutPrompt is the shell prompt of every pane in TestALoadedLayoutShowsItsShells.
// It is unusual so a count of it on screen is a count of panes that drew.
const layoutPrompt = "LYT>"

// promptCount is the number of screen rows that show layoutPrompt.
func promptCount(s tuitest.Screen) int {
	n := 0
	_, rows := s.Size()
	for y := range rows {
		n += strings.Count(s.Line(y), layoutPrompt)
	}
	return n
}

// TestALoadedLayoutShowsItsShells saves a layout of three panes, loads it on
// an empty workspace, and holds every new pane to showing its shell's prompt
// without a workspace switch (issue #411).
//
// In a daemon session the layout asks the daemon for the new panes, and they
// reach the client in a later state sync. Before e1af811a (#384) nothing
// subscribed a pane that arrived that way, so it stayed blank until the user
// switched away and back, and the switch subscribed it.
//
// How this could pass wrongly, written down first:
//   - The prompts counted could be the panes of workspace 1. The count is read
//     on workspace 2 only, after the dock shows 2:3.
//   - A pane could show the prompt from a snapshot and still stream nothing.
//     A command typed into the focused pane has to print its output too.
//   - The prompt could be the command line typed into a pane. The marker is
//     split in the command, and the prompt string is only ever the shell's.
//
// NEGATIVE CONTROL: fails on v0.8.5 and on main with the reconcilePaneStreams
// call in ApplyStateSyncFrom cut, with 0 of 3 prompts on workspace 2.
func TestALoadedLayoutShowsItsShells(t *testing.T) {
	term, _ := start(t, startOpts{
		args: []string{"new", "lay"},
		env:  []string{"PS1=" + layoutPrompt + " "},
	})
	waitBoot(t, term)

	newWindow(t, term)
	newWindow(t, term)
	newWindow(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return promptCount(s) >= 3 }, shellTimeout); err != nil {
		t.Fatalf("the three panes of workspace 1 never showed their prompts: %v\n%s", err, term.Snapshot())
	}

	// Save it: prefix, the layout prefix, save.
	sendKeys(t, term, tuitest.Ctrl('b'), "L", "s")
	sendKeys(t, term, "t", "r", "i", "o", tuitest.Enter)
	if err := term.WaitForText("Layout saved", uiTimeout); err != nil {
		t.Fatalf("the layout was never saved: %v\n%s", err, term.Snapshot())
	}

	sendKeys(t, term, tuitest.Alt("2"))
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "2:0")
	}, uiTimeout); err != nil {
		t.Fatalf("workspace 2 never showed: %v\n%s", err, term.Snapshot())
	}

	// Load it: prefix, the layout prefix, load, and the only entry.
	sendKeys(t, term, tuitest.Ctrl('b'), "L", "l")
	if err := term.WaitForText("trio", uiTimeout); err != nil {
		t.Fatalf("the layout picker never listed the layout: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Enter)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "2:3")
	}, uiTimeout); err != nil {
		t.Fatalf("the layout never opened three panes on workspace 2: %v\n%s", err, term.Snapshot())
	}

	dir := artifactDir(t)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return promptCount(s) >= 3 }, shellTimeout); err != nil {
		saveArtifact(t, term, dir, "layout-loaded-blank")
		t.Fatalf("the loaded layout's panes never showed their shells (%d of 3 prompts): %v\n%s",
			promptCount(term.Screen()), err, term.Snapshot())
	}

	// The pane has to stream, not only show a snapshot taken once.
	enterTerminalMode(t, term)
	runInShell(t, term, "echo "+splitMarker("LAYOUTLIVE"), "LAYOUTLIVE", shellTimeout)
	time.Sleep(100 * time.Millisecond)
	saveArtifact(t, term, dir, "layout-loaded")
}
