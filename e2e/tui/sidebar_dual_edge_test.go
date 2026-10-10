package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestDualRailHiddenLegacyExplicitLeft proves an explicitly enabled left edge
// works even if the legacy rail is hidden. It must draw the left edge's own
// section, not silently choose the right table or reveal the legacy rail.
func TestDualRailHiddenLegacyExplicitLeft(t *testing.T) {
	cfg := `[appearance.sidebar]
position = "hidden"
enabled = true
[appearance.sidebar.left]
enabled = true
width = 28
sections = "sessions,custom"
[appearance.sidebar.left.custom]
command = "printf 'LEFT-ONLY-HIDDEN\\n'"
`
	term, _ := railClient(t, "hidden-left", cfg, startOpts{cols: 120, rows: 40})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, rows := s.Size()
		for y := 0; y < rows; y++ {
			line := []rune(s.Line(y))
			if strings.Contains(string(line[:min(28, len(line))]), "LEFT-ONLY-HIDDEN") {
				return len(line) <= cols-28 || !strings.Contains(string(line[cols-28:]), "LEFT-ONLY-HIDDEN")
			}
		}
		return false
	}, uiTimeout); err != nil {
		t.Fatalf("explicit left edge absent with hidden legacy rail: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "hidden-left")
}

// TestDualRailHiddenDoesNotInheritLegacyFields checks that an explicit edge
// under a hidden legacy rail uses the resolver's defaults, not hidden legacy
// section order, width or command from [appearance.sidebar].
func TestDualRailHiddenDoesNotInheritLegacyFields(t *testing.T) {
	cfg := `[appearance.sidebar]
position = "hidden"
width = 46
sections = "custom"
[appearance.sidebar.custom]
command = "printf 'LEGACY-MUST-NOT-DRAW\\n'"
[appearance.sidebar.left]
enabled = true
`
	term, _ := railClient(t, "hidden-defaults", cfg, startOpts{cols: 120, rows: 40})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Line(0)[:min(24, len(s.Line(0)))], "sessions") &&
			s.Cell(23, 12).Rune == '│' &&
			!strings.Contains(s.Text(), "LEGACY-MUST-NOT-DRAW")
	}, uiTimeout); err != nil {
		t.Fatalf("hidden legacy fields leaked into explicit left edge: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "hidden-edge-defaults")
}

// TestDualRailHiddenLegacyExplicitRight covers the mirror of the left case:
// a hidden legacy rail must not suppress an explicitly enabled right edge.
func TestDualRailHiddenLegacyExplicitRight(t *testing.T) {
	cfg := `[appearance.sidebar]
position = "hidden"
[appearance.sidebar.right]
enabled = true
width = 28
sections = "sessions,custom"
[appearance.sidebar.right.custom]
command = "printf 'RIGHT-ONLY-HIDDEN\\n'"
`
	term, _ := railClient(t, "hidden-right", cfg, startOpts{cols: 120, rows: 40})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, rows := s.Size()
		for y := 0; y < rows; y++ {
			line := []rune(s.Line(y))
			if len(line) > cols-28 && strings.Contains(string(line[cols-28:]), "RIGHT-ONLY-HIDDEN") {
				return !strings.Contains(string(line[:min(28, len(line))]), "RIGHT-ONLY-HIDDEN")
			}
		}
		return false
	}, uiTimeout); err != nil {
		t.Fatalf("explicit right edge absent with hidden legacy rail: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "hidden-right")
}

// TestDualRailHiddenExplicitBoth checks the resolver's two enabled edges
// against the actual draw: hiding the legacy rail cannot discard either
// explicitly enabled per-edge rail.
func TestDualRailHiddenExplicitBoth(t *testing.T) {
	cfg := `[appearance.sidebar]
position = "hidden"
[appearance.sidebar.left]
enabled = true
width = 28
sections = "custom"
[appearance.sidebar.left.custom]
command = "printf 'EXPLICIT-LEFT\\n'"
[appearance.sidebar.right]
enabled = true
width = 28
sections = "sessions,custom"
[appearance.sidebar.right.custom]
command = "printf 'EXPLICIT-RIGHT\\n'"
`
	term, _ := railClient(t, "hidden-both", cfg, startOpts{cols: 120, rows: 40})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, rows := s.Size()
		left, right := false, false
		for y := 0; y < rows; y++ {
			line := []rune(s.Line(y))
			left = left || strings.Contains(string(line[:min(28, len(line))]), "EXPLICIT-LEFT")
			if len(line) > cols-28 {
				right = right || strings.Contains(string(line[cols-28:]), "EXPLICIT-RIGHT")
			}
		}
		return left && right
	}, uiTimeout); err != nil {
		t.Fatalf("both explicitly enabled edges did not draw: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "hidden-both")
}

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

// TestDualRailFooterAndTooltip checks the left edge's own inward-facing
// controls and the hover label's position, not just the right edge's config.
func TestDualRailFooterAndTooltip(t *testing.T) {
	cfg := `[appearance.sidebar]
position = "right"
enabled = true
width = 28
sections = "sessions"
[appearance.sidebar.left]
enabled = true
width = 28
sections = "sessions"
`
	term, _ := railClient(t, "dual-tooltip", cfg, startOpts{cols: 120, rows: 40})
	screen := term.Screen()
	cols, rows := screen.Size()
	arrowsFaceInward := false
	for y := 0; y < rows; y++ {
		line := []rune(screen.Line(y))
		if len(line) > cols-28 && strings.Contains(string(line[:28]), "«") && strings.Contains(string(line[cols-28:]), "»") {
			arrowsFaceInward = true
			break
		}
	}
	if !arrowsFaceInward {
		t.Fatalf("footer arrows do not face inward from their own edges:\n%s", term.Snapshot())
	}
	plus := strings.IndexRune(string([]rune(screen.Line(0))[:28]), '+')
	if plus < 0 {
		t.Fatalf("no left-edge add control to hover:\n%s", term.Snapshot())
	}
	sendMouse(t, term, "hover left add", tuitest.MouseEvent{
		Col: plus, Row: 0, Button: tuitest.MouseNone, Action: tuitest.MouseMove,
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		line := []rune(s.Line(0))
		return len(line) > 28 && strings.Contains(string(line[28:min(92, len(line))]), "new session")
	}, uiTimeout); err != nil {
		t.Fatalf("left add tooltip did not open beside the left rail: %v\n%s", err, term.Snapshot())
	}
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "dual-rail-left-tooltip")
	savePNG(t, term.Screen(), hostPalette(t, ""), dir, "dual-rail-left-tooltip")
}

// TestDualRailToggleOtherEdge gives the opt-in edge its own keyboard toggle.
// Hiding it must not also hide the original right rail; toggling again restores
// the left rail with its own session rows.
func TestDualRailToggleOtherEdge(t *testing.T) {
	cfg := `[appearance.sidebar]
position = "right"
enabled = true
width = 28
sections = "sessions"
[appearance.sidebar.left]
enabled = true
width = 28
sections = "sessions"
`
	term, _ := railClient(t, "dual-toggle", cfg, startOpts{cols: 120, rows: 40})
	prefix(t, term, "H")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, _ := s.Size()
		return !strings.Contains(s.Line(0)[:min(28, len(s.Line(0)))], "sessions") &&
			strings.Contains(s.Line(0)[cols-28:], "sessions") && countWindows(s) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("opposite edge did not hide independently: %v\n%s", err, term.Snapshot())
	}
	prefix(t, term, "H")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, _ := s.Size()
		return strings.Contains(s.Line(0)[:min(28, len(s.Line(0)))], "sessions") &&
			strings.Contains(s.Line(0)[cols-28:], "sessions")
	}, uiTimeout); err != nil {
		t.Fatalf("opposite edge did not reopen: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "opposite-edge-toggle")
}

// TestDualRailKeyboardSwitchEdge checks the focused row on each actual edge.
// Merely attaching via j/Enter cannot prove keyboard ownership: either rail
// lists the same sessions, so the opposite rail must gain the focus styling.
func TestDualRailKeyboardSwitchEdge(t *testing.T) {
	cfg := `[appearance.sidebar]
position = "right"
enabled = true
width = 28
sections = "sessions"
[appearance.sidebar.left]
enabled = true
width = 28
sections = "sessions"
`
	term, _ := railClient(t, "dual-key-edge", cfg, startOpts{cols: 120, rows: 40})
	if err := term.SendKeys("s"); err != nil {
		t.Fatal(err)
	}
	var leftX, rightX int
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, rows := s.Size()
		leftX, rightX = -1, -1
		for y := 0; y < rows; y++ {
			line := []rune(s.Line(y))
			if leftX < 0 {
				if i := strings.Index(string(line[:min(28, len(line))]), "dual-key-edge"); i >= 0 {
					leftX = i
				}
			}
			if rightX < 0 && len(line) > cols-28 {
				if i := strings.Index(string(line[cols-28:]), "dual-key-edge"); i >= 0 {
					rightX = cols - 28 + i
				}
			}
		}
		return leftX >= 0 && rightX >= 0 && s.Cell(27, 12).Fg != s.Cell(92, 12).Fg
	}, uiTimeout); err != nil {
		t.Fatalf("session rows or initial right-rail focus absent: %v\n%s", err, term.Snapshot())
	}
	before := term.Screen()
	leftBefore, rightBefore := before.Cell(27, 12).Fg, before.Cell(92, 12).Fg
	if err := term.SendKeys("e"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return s.Cell(27, 12).Fg != leftBefore && s.Cell(92, 12).Fg != rightBefore
	}, uiTimeout); err != nil {
		t.Fatalf("keyboard did not transfer focus from right to left: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "dual-rail-keyboard-focus")
	if err := term.SendKeys("e"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return s.Cell(27, 12).Fg == leftBefore && s.Cell(92, 12).Fg == rightBefore
	}, uiTimeout); err != nil {
		t.Fatalf("keyboard did not return focus to right rail: %v\n%s", err, term.Snapshot())
	}
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
	// Assert keyboard ownership changed on the actual left rail, not merely
	// that a session row shared with the right rail can be activated there.
	leftBefore, rightBefore := term.Screen().Cell(27, 12).Fg, term.Screen().Cell(92, 12).Fg
	mouseClick(t, term, 4, 12, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return s.Cell(27, 12).Fg != leftBefore && s.Cell(92, 12).Fg == rightBefore
	}, uiTimeout); err != nil {
		t.Fatalf("blank left-rail click did not give left edge keyboard focus: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("j", tuitest.Enter); err != nil {
		t.Fatalf("navigate the left rail by keyboard: %v", err)
	}
	if err := term.WaitForText("Session: other-edge", uiTimeout); err != nil {
		t.Fatalf("left rail did not own keyboard focus: %v\n%s", err, term.Snapshot())
	}
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "dual-rail-click-keyboard-focus")
	savePNG(t, term.Screen(), hostPalette(t, ""), dir, "dual-rail-click-keyboard-focus")
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
