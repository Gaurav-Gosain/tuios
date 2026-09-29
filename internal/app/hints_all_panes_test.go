package app

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// hintsTwoPaneOS is two panes side by side on workspace 1, the left one
// focused, each with its own text printed in it.
func hintsTwoPaneOS(t *testing.T, left, right string) (*OS, *terminal.Window, *terminal.Window) {
	t.Helper()
	a := newTestWindow(t, "hintsA000001", 60, 12)
	b := newTestWindow(t, "hintsB000001", 60, 12)
	a.Workspace, b.Workspace = 1, 1
	b.X = 60
	a.WriteOutput([]byte(left))
	b.WriteOutput([]byte(right))
	m := newTestOS(a)
	m.Windows = append(m.Windows, b)
	m.CurrentWorkspace = 1
	m.UserConfig = config.DefaultConfig()
	return m, a, b
}

// shaLine prints n distinct hashes, one per line.
func shaLines(prefix string, n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "%s%04d\r\n", prefix, i)
	}
	return b.String()
}

// The all-panes form labels every pane with one set of labels. No label names
// two texts, and every label on the focused pane is as short as the shortest
// label on the other pane.
func TestHintsAllPanesLabelsAreUniqueAndShortestOnTheFocusedPane(t *testing.T) {
	// The focused pane's matches are far from its cursor, and the other
	// pane's are near its own, so only the pane order can put the focused
	// pane first. The other pane alone has more texts than there are letters.
	var right strings.Builder
	for i := range 6 {
		fmt.Fprintf(&right, "def%04d fed%04d\r\n", i, i)
	}
	m, _, _ := hintsTwoPaneOS(t, shaLines("abc", 3)+strings.Repeat("\r\n", 6), right.String())
	m.OpenHintsAllPanes()
	if !m.HintsOpen() {
		t.Fatal("hints did not open")
	}
	labels := m.HintLabels()
	if len(labels) != 15 {
		t.Fatalf("want 15 labelled texts across both panes, got %d: %v", len(labels), labels)
	}
	owner := map[string]string{}
	for text, label := range labels {
		if prev, dup := owner[label]; dup {
			t.Fatalf("%q and %q share the label %q", prev, text, label)
		}
		owner[label] = text
	}
	longestFocused, shortestOther := 0, 1<<30
	for text, label := range labels {
		if strings.HasPrefix(text, "abc") {
			longestFocused = max(longestFocused, len(label))
		} else {
			shortestOther = min(shortestOther, len(label))
		}
	}
	if longestFocused != 1 || longestFocused > shortestOther {
		t.Errorf("the focused pane's labels are up to %d letters, the other pane's from %d: %v",
			longestFocused, shortestOther, labels)
	}
}

// A pane the workspace does not show is not labelled: a minimised pane, a
// pane on another workspace, the part of a pane under a floating pane, and a
// pane behind a zoomed one.
func TestHintsAllPanesSkipPanesNotOnScreen(t *testing.T) {
	m, a, _ := hintsTwoPaneOS(t, "focus deadbee01\r\n", "other deadbee02\r\n")
	mini := newTestWindow(t, "hintsC000001", 60, 12)
	mini.Workspace, mini.Minimized = 1, true
	mini.WriteOutput([]byte("mini deadbee03\r\n"))
	away := newTestWindow(t, "hintsD000001", 60, 12)
	away.Workspace = 2
	away.WriteOutput([]byte("away deadbee04\r\n"))
	m.Windows = append(m.Windows, mini, away)

	m.OpenHintsAllPanes()
	got := m.HintLabels()
	for _, want := range []string{"deadbee01", "deadbee02"} {
		if _, ok := got[want]; !ok {
			t.Errorf("%q has no label: %v", want, got)
		}
	}
	for _, hidden := range []string{"deadbee03", "deadbee04"} {
		if _, ok := got[hidden]; ok {
			t.Errorf("%q is on a pane the workspace does not show, and has a label", hidden)
		}
	}
	m.CloseHints()

	// A floating pane over the right pane's first row hides its match.
	float := newTestWindow(t, "hintsE000001", 30, 5)
	float.Workspace, float.IsFloating = 1, true
	float.X, float.Y = 60, 0
	m.Windows = append(m.Windows, float)
	m.OpenHintsAllPanes()
	if _, ok := m.HintLabels()["deadbee02"]; ok {
		t.Error("a match under a floating pane has a label nobody can see")
	}
	m.CloseHints()
	m.Windows = m.Windows[:len(m.Windows)-1]

	// A zoom that fills the region hides every other pane.
	a.Zoomed = true
	m.OpenHintsAllPanes()
	if _, ok := m.HintLabels()["deadbee02"]; ok {
		t.Error("a pane behind the zoomed pane has a label")
	}
	if _, ok := m.HintLabels()["deadbee01"]; !ok {
		t.Error("the zoomed pane has no label")
	}
	m.CloseHints()
}

// Shift and a label on another pane's match types the text into the focused
// pane, the one the person is in, and copies it. The pane the match is on
// gets nothing.
func TestHintsShiftTypesIntoTheFocusedPane(t *testing.T) {
	m, a, b := hintsTwoPaneOS(t, "$ \r\n", "id 30c0acf5-8dd0-48f2-8d86-cf0aae99aa4a\r\n")
	var mu sync.Mutex
	var toA, toB bytes.Buffer
	a.DaemonWriteFunc = func(p []byte) error { mu.Lock(); defer mu.Unlock(); toA.Write(p); return nil }
	b.DaemonWriteFunc = func(p []byte) error { mu.Lock(); defer mu.Unlock(); toB.Write(p); return nil }

	const uuid = "30c0acf5-8dd0-48f2-8d86-cf0aae99aa4a"
	m.OpenHintsAllPanes()
	label, ok := m.HintLabels()[uuid]
	if !ok {
		t.Fatalf("the other pane's UUID has no label: %v", m.HintLabels())
	}
	for _, r := range label {
		m.HintsPress(r, HintType)
	}
	if m.HintsOpen() {
		t.Fatal("the label did not complete")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(toA.String(), uuid) {
		t.Errorf("the focused pane was not typed into: %q", toA.String())
	}
	if toB.Len() != 0 {
		t.Errorf("the pane the match is on was typed into: %q", toB.String())
	}
	if note := hintsLastNote(m); !strings.Contains(note, "Copied") {
		t.Errorf("the match was not copied: %q", note)
	}
}

// hints.all_panes turns the hints action into the all-panes form. Off, the
// action labels the focused pane only.
func TestHintsAllPanesSetting(t *testing.T) {
	m, _, _ := hintsTwoPaneOS(t, "focus deadbee01\r\n", "other deadbee02\r\n")
	m.OpenHints()
	if _, ok := m.HintLabels()["deadbee02"]; ok {
		t.Error("with hints.all_panes off, the hints action labelled another pane")
	}
	m.CloseHints()

	m.UserConfig.Hints.AllPanes = true
	m.OpenHints()
	for _, want := range []string{"deadbee01", "deadbee02"} {
		if _, ok := m.HintLabels()[want]; !ok {
			t.Errorf("with hints.all_panes on, %q has no label: %v", want, m.HintLabels())
		}
	}
	if def := config.DefaultConfig().Hints.AllPanes; def {
		t.Error("hints.all_panes is on by default")
	}
}

// A relative path names a file in its own pane's folder, so the same path on
// two panes gets two labels. A hash is the same thing wherever it is, and
// keeps one label.
func TestHintsAllPanesScopePathsToTheirPane(t *testing.T) {
	m, _, _ := hintsTwoPaneOS(t, "src/main.go deadbee01\r\n", "src/main.go deadbee01\r\n")
	m.OpenHintsAllPanes()
	paths, hashes := map[string]bool{}, map[string]bool{}
	for _, match := range m.hints.matches {
		switch match.text {
		case "src/main.go":
			paths[match.label] = true
		case "deadbee01":
			hashes[match.label] = true
		}
	}
	if len(paths) != 2 {
		t.Errorf("the path on two panes has labels %v, want two", paths)
	}
	if len(hashes) != 1 {
		t.Errorf("the hash on two panes has labels %v, want one", hashes)
	}
}

// Focus moving to another pane closes hints mode, as it does with one pane:
// Shift and a label types into the pane that had focus.
func TestHintsAllPanesCloseWhenFocusMoves(t *testing.T) {
	m, _, _ := hintsTwoPaneOS(t, "focus deadbee01\r\n", "other deadbee02\r\n")
	m.OpenHintsAllPanes()
	if !m.HintsOpen() {
		t.Fatal("hints did not open")
	}
	m.FocusWindow(1)
	if m.HintsOpen() {
		t.Error("hints stayed open after focus moved to another pane")
	}
}

// The frame: labels on both panes of a tiled layout under shared borders, one
// pane scrolled back. Each label sits on its match's first cell on screen,
// drawn in the label colours.
func TestHintsAllPanesDrawOnEveryPane(t *testing.T) {
	swapBool(t, &config.Global.SharedBorders, true)
	m := gapTestOS(t, 2)
	m.UseBSPLayout = true
	m.TileAllWindows()
	m.UserConfig = config.DefaultConfig()
	left, right := m.Windows[0], m.Windows[1]
	left.WriteOutput([]byte("left deadbee01\r\n"))
	// The right pane prints its hash and then enough lines to push it into
	// the scrollback, and is scrolled back to show it again.
	right.WriteOutput([]byte("right deadbee02\r\n" + strings.Repeat("filler\r\n", right.ContentHeight()+4)))
	if right.ScrollbackLen() == 0 {
		t.Fatal("the right pane has no scrollback")
	}
	right.ScrollbackOffset = right.ScrollbackLen()
	right.MarkContentDirty()
	m.FocusWindow(0)

	m.OpenHintsAllPanes()
	if !m.HintsOpen() {
		t.Fatal("hints did not open")
	}
	canvas := m.GetCanvas(true)
	accent := theme.UI().Accent
	for _, match := range m.hints.matches {
		p := m.hints.panes[match.pane]
		w := m.windowByID(p.windowID)
		rect := paneContentRect(w)
		x, y := rect.Min.X+match.labelAt.x, rect.Min.Y+match.labelAt.y
		cell := canvas.Lines[y][x]
		if cell.Content != match.label[:1] || packColor8(cell.Style.Bg) != packColor8(accent) {
			t.Errorf("pane %s: the label %q of %q is not drawn at (%d,%d): got %+v",
				p.windowID, match.label, match.text, x, y, cell)
		}
	}
	seen := map[string]bool{}
	for _, match := range m.hints.matches {
		seen[match.text] = true
	}
	if !seen["deadbee01"] || !seen["deadbee02"] {
		t.Errorf("want a match on each pane, got %v", seen)
	}
}
