package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Closing the quit menu changes the keyboard flags the view asks the host for,
// and Bubble Tea answers a change with a query (ESC [ ? u). A quit cancels the
// input reader before it stops the renderer, so a frame flushed in between
// leaves the host's answer unread, and the shell the client returns to prints
// the end of it as "5u". These tests pin the part tuios owns: once the client
// has decided to quit, the view asks for what the last frame asked for, so the
// frame that can still be flushed carries no change and no query.
//
// How these could pass wrongly, written down first:
//   - The flags might never differ in the setup, and then "unchanged" proves
//     nothing. The test checks that opening the menu and closing it does move
//     them on a client that is not quitting.
//   - The freeze could use the flags of the frame after the menu closed, which
//     is the changed value. The test closes the menu in the same step as the
//     quit, as the quit menu does, and requires the flags of the frame drawn
//     with the menu still open.
//   - Learn mode never quits, so a freeze there would leave a tour that no
//     longer follows the keys. It must keep moving.
//   - A new quit path could return tea.Quit directly and skip the freeze. The
//     scan fails on any tea.Quit outside QuitCmd.

func quitFlagsOS(t *testing.T) *OS {
	t.Helper()
	m := newTestOS(newTestWindow(t, "quit-flags-0001", 60, 20))
	m.Mode = TerminalMode
	return m
}

func TestQuitCmdKeepsTheKeyboardFlagsOfTheLastFrame(t *testing.T) {
	m := quitFlagsOS(t)
	resting := m.keyboardEnhancements()
	m.ShowQuitMenu = true
	menu := m.keyboardEnhancements()
	if menu == resting {
		t.Fatal("setup: the quit menu being open must change what the view asks the host for")
	}
	m.ShowQuitMenu = false
	if got := m.keyboardEnhancements(); got != resting {
		t.Fatalf("setup: closing the menu on a client that is not quitting must change the flags back, got %+v want %+v", got, resting)
	}

	// The quit menu's Detach row: the menu closes and the client quits in the
	// same update, so the next frame is built after both.
	m.ShowQuitMenu = true
	if got := m.keyboardEnhancements(); got != menu {
		t.Fatalf("setup: the frame drawn with the menu open asks for %+v, want %+v", got, menu)
	}
	m.ShowQuitMenu = false
	if cmd := m.QuitCmd(); cmd == nil {
		t.Fatal("QuitCmd returned no command")
	}
	if got := m.keyboardEnhancements(); got != menu {
		t.Errorf("after the quit the view asks for %+v, want the last frame's %+v, so no query is sent", got, menu)
	}
}

func TestQuitCmdInLearnModeKeepsTheFlagsMoving(t *testing.T) {
	m := quitFlagsOS(t)
	m.LearnMode = true
	resting := m.keyboardEnhancements()
	m.ShowQuitMenu = true
	_ = m.keyboardEnhancements()
	m.ShowQuitMenu = false
	m.QuitCmd()
	if got := m.keyboardEnhancements(); got != resting {
		t.Errorf("in learn mode the flags are %+v after a quit that does not happen, want them following the keys: %+v", got, resting)
	}
}

// TestEveryQuitGoesThroughQuitCmd fails on a tea.Quit that is not QuitCmd's, in
// the packages that decide a quit. Such a path would leave the flags free to
// change on the frame before the quit.
func TestEveryQuitGoesThroughQuitCmd(t *testing.T) {
	bareQuit := regexp.MustCompile(`\btea\.Quit\b`) // not tea.QuitMsg
	for _, dir := range []string{".", "../input"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") || filepath.Base(f) == "keyboard_enhancements.go" {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(src), "\n") {
				if bareQuit.MatchString(line) && !strings.HasPrefix(strings.TrimSpace(line), "//") {
					t.Errorf("%s:%d returns tea.Quit directly; use QuitCmd, which keeps the keyboard flags still: %s", f, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}
