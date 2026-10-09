package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestDualRailResizeEdges proves resizing the optional rail does not change
// the width of the legacy rail and the original edge remains resizable too.
func TestDualRailResizeEdges(t *testing.T) {
	config := `[appearance.sidebar]
position = "right"
enabled = true
width = 28
sections = "sessions"
[appearance.sidebar.left]
enabled = true
width = 28
sections = "sessions"
`
	term, _ := railClient(t, "dual-resize", config, startOpts{cols: 120, rows: 40})
	mousePress(t, term, 27, 12, tuitest.MouseLeft, 0)
	mouseMotion(t, term, 36, 12, tuitest.MouseLeft, 0)
	mouseRelease(t, term, 36, 12, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return s.Cell(36, 12).Rune == '│' && s.Cell(92, 12).Rune == '│'
	}, uiTimeout); err != nil {
		t.Fatalf("left resize changed the other edge or did not move its border: %v\n%s", err, term.Snapshot())
	}
	t.Logf("opposite edge resized independently:\n%s", term.Snapshot())
}

// TestDualRailNarrowScreen keeps the main pane usable while both configured
// edges contract. The optional edge gives up width before it crowds out the
// pane, and neither rail can leave a stale reserved band on a tiny client.
func TestDualRailNarrowScreen(t *testing.T) {
	config := `[appearance.sidebar]
position = "right"
enabled = true
width = 28
sections = "sessions"
[appearance.sidebar.left]
enabled = true
width = 28
sections = "sessions"
`
	term, _ := railClient(t, "dual-narrow", config, startOpts{cols: 120, rows: 40})
	if err := term.Resize(53, 40); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, _ := s.Size()
		return cols == 53 && s.Cell(2, 12).Rune == '│' && s.Cell(50, 12).Rune == '│' && countWindows(s) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("two glyph strips did not leave a usable pane at 53 columns: %v\n%s", err, term.Snapshot())
	}
	t.Logf("two narrow rails and pane:\n%s", term.Snapshot())
	if err := term.Resize(39, 40); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, _ := s.Size()
		return cols == 39 && countWindows(s) == 1 && s.Cell(2, 12).Rune != '│' && s.Cell(36, 12).Rune != '│'
	}, uiTimeout); err != nil {
		t.Fatalf("hidden rails left stale reserved strips at 39 columns: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after both rails contract and hide")
}

// TestDualRailLeftFiles proves that a section present only on the optional
// edge receives its asynchronous directory listing, not just an empty header.
func TestDualRailLeftFiles(t *testing.T) {
	config := `[appearance.sidebar]
position = "right"
enabled = true
width = 28
sections = "sessions"
[appearance.sidebar.left]
enabled = true
width = 28
sections = "sessions,files"
`
	term, base := railClient(t, "dual-files", config, startOpts{cols: 120, rows: 40})
	dir := filepath.Join(base, "listed")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "LEFT-ONLY.txt"), []byte("yes"), 0600); err != nil {
		t.Fatal(err)
	}
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+dir+` && printf '\033]7;file://%s\033\\%s\n' "$PWD" mar""ked`, "marked", uiTimeout)
	leaveTerminalMode(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, rows := s.Size()
		for y := 0; y < rows; y++ {
			line := []rune(s.Line(y))
			if strings.Contains(string(line[:min(28, len(line))]), "LEFT-ONLY.txt") {
				return len(line) <= cols-28 || !strings.Contains(string(line[cols-28:]), "LEFT-ONLY.txt")
			}
		}
		return false
	}, uiTimeout); err != nil {
		t.Fatalf("left-only files section did not list its pane folder: %v\n%s", err, term.Snapshot())
	}
	t.Logf("left-only files listing:\n%s", term.Snapshot())
}

// TestDualRailLeftGit checks the other asynchronous section when only the
// optional edge names it. A repository reading must land on the left rail.
func TestDualRailLeftGit(t *testing.T) {
	config := `[appearance.sidebar]
position = "right"
enabled = true
width = 28
sections = "sessions"
[appearance.sidebar.left]
enabled = true
width = 28
sections = "sessions,git"
`
	term, base := railClient(t, "dual-git", config, startOpts{cols: 120, rows: 40})
	dir := filepath.Join(base, "left-repo")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", dir, "init", "-q"},
		{"-C", dir, "branch", "-M", "left-only-branch"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+dir+` && printf '\033]7;file://%s\033\\%s\n' "$PWD" mar""ked`, "marked", uiTimeout)
	leaveTerminalMode(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, rows := s.Size()
		for y := 0; y < rows; y++ {
			line := []rune(s.Line(y))
			if strings.Contains(string(line[:min(28, len(line))]), "left-only-branch") {
				return len(line) <= cols-28 || !strings.Contains(string(line[cols-28:]), "left-only-branch")
			}
		}
		return false
	}, uiTimeout); err != nil {
		t.Fatalf("left-only git section did not show its branch: %v\n%s", err, term.Snapshot())
	}
	t.Logf("left-only git reading:\n%s", term.Snapshot())
}

// TestDualRailScrollIsolation exercises the two rails through a real mouse
// wheel. The second edge must not borrow the original rail's scroll offset.
func TestDualRailScrollIsolation(t *testing.T) {
	config := `[appearance.sidebar]
position = "right"
enabled = true
width = 28
sections = "sessions,custom"
[appearance.sidebar.custom]
command = "i=1; while [ $i -le 70 ]; do printf 'RIGHT-%02d\\n' \"$i\"; i=$((i+1)); done"
[appearance.sidebar.left]
enabled = true
width = 28
sections = "sessions,custom"
[appearance.sidebar.left.custom]
command = "i=1; while [ $i -le 70 ]; do printf 'LEFT-%02d\\n' \"$i\"; i=$((i+1)); done"
`
	term, _ := railClient(t, "dual-scroll", config, startOpts{cols: 120, rows: 40})
	waitForAll(t, term, uiTimeout, "both custom lists", "LEFT-01", "RIGHT-01")
	leftCol, leftRow, _ := findOnGrid(term.Screen(), "LEFT-01")
	wheelAt(t, term, leftCol, leftRow, tuitest.MouseWheelDown, 12)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		leftMoved, rightStayed := true, false
		cols, rows := s.Size()
		for y := 0; y < rows; y++ {
			line := []rune(s.Line(y))
			leftMoved = leftMoved && !strings.Contains(string(line[:min(28, len(line))]), "LEFT-01")
			if len(line) > cols-28 {
				rightStayed = rightStayed || strings.Contains(string(line[cols-28:]), "RIGHT-01")
			}
		}
		return leftMoved && rightStayed
	}, uiTimeout); err != nil {
		t.Fatalf("scrolling the left rail also changed or failed to scroll a rail: %v\n%s", err, term.Snapshot())
	}
	t.Logf("independent rail scroll screen:\n%s", term.Snapshot())
}

// TestDualRailClickSwitchesSession exercises the actual pointer path on both
// edges: a row on the left must not be mistaken for a pane click or a hit on
// the legacy right rail. Switching back through the right proves both remain
// usable after the session tree changes.
func TestDualRailClickSwitchesSession(t *testing.T) {
	config := `[appearance.sidebar]
position = "right"
enabled = true
width = 28
sections = "sessions"
[appearance.sidebar.left]
enabled = true
width = 28
sections = "sessions"
`
	term, base := railClient(t, "dual-input", config, startOpts{cols: 120, rows: 40})
	if out, err := tuiosCLI(t, base, "new", "other-edge", "--detach"); err != nil {
		t.Fatalf("create second session: %v: %s", err, out)
	}
	waitForAll(t, term, uiTimeout, "both session rows", "dual-input", "other-edge")
	leftCol, leftRow, _ := findOnGrid(term.Screen(), "other-edge")
	if leftCol >= 28 {
		t.Fatalf("other-edge not on left rail: %d\n%s", leftCol, term.Snapshot())
	}
	mouseClick(t, term, leftCol, leftRow, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Session: other-edge", uiTimeout); err != nil {
		t.Fatalf("left rail did not attach to other-edge: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, rows := s.Size()
		for y := 0; y < rows; y++ {
			line := []rune(s.Line(y))
			if len(line) > cols-28 && strings.Contains(string(line[cols-28:]), "dual-input") {
				return true
			}
		}
		return false
	}, uiTimeout); err != nil {
		t.Fatalf("original session not listed on right rail: %v\n%s", err, term.Snapshot())
	}
	screen := term.Screen()
	cols, rows := screen.Size()
	found := false
	for y := 0; y < rows; y++ {
		line := []rune(screen.Line(y))
		if len(line) <= cols-28 {
			continue
		}
		if offset := strings.Index(string(line[cols-28:]), "dual-input"); offset >= 0 {
			mouseClick(t, term, cols-28+offset, y, tuitest.MouseLeft, 0)
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("could not locate original session on right rail\n%s", term.Snapshot())
	}
	if err := term.WaitForText("Session: dual-input", uiTimeout); err != nil {
		t.Fatalf("right rail did not attach back: %v\n%s", err, term.Snapshot())
	}
	// Empty space belongs to the left rail too: click there to move keyboard
	// focus into its own nav list, then choose the next session with j/Enter.
	mouseClick(t, term, 4, 12, tuitest.MouseLeft, 0)
	if err := term.SendKeys("j", tuitest.Enter); err != nil {
		t.Fatalf("navigate the left rail by keyboard: %v", err)
	}
	if err := term.WaitForText("Session: other-edge", uiTimeout); err != nil {
		t.Fatalf("left rail did not own keyboard focus: %v\n%s", err, term.Snapshot())
	}
	t.Logf("session switched by left click, right click, and left keyboard focus:\n%s", term.Snapshot())
}

// TestDualRailCustomSections keeps the original right-hand rail and its
// command while a second, independently configured rail draws on the left.
// Checking the actual edge columns detects a command that ran but drew on the
// wrong rail, or two rails that accidentally share one component's output.
func TestDualRailCustomSections(t *testing.T) {
	config := `[appearance.sidebar]
position = "right"
enabled = true
width = 28
sections = "sessions,custom"

[appearance.sidebar.custom]
title = "Right brief"
command = "printf 'RIGHT-CUSTOM-ROW\\n'"

[appearance.sidebar.left]
enabled = true
width = 28
sections = "sessions,custom"

[appearance.sidebar.left.custom]
title = "Left brief"
command = "printf 'LEFT-CUSTOM-ROW\\n'"
`
	term, _ := railClient(t, "dual-rail", config, startOpts{cols: 120, rows: 40})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, rows := s.Size()
		left, right := false, false
		for y := 0; y < rows; y++ {
			line := []rune(s.Line(y))
			left = left || strings.Contains(string(line[:min(28, len(line))]), "LEFT-CUSTOM-ROW")
			// Screen.Line may omit trailing blank cells. Only require
			// enough cells to reach the right edge, not all screen columns.
			if len(line) > cols-28 {
				right = right || strings.Contains(string(line[cols-28:]), "RIGHT-CUSTOM-ROW")
			}
		}
		return left && right
	}, uiTimeout); err != nil {
		t.Fatalf("both independently configured custom sections did not render on their edges: %v\n%s", err, term.Snapshot())
	}
	// The snapshot is the repeatable artifact: run this test with -v to see
	// the real client's settled screen, including both edge regions.
	t.Logf("dual-rail screen:\n%s", term.Snapshot())
	alive(t, term, "with both rails visible")
}
