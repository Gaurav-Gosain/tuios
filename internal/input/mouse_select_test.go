package input

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// selectPane builds a focused, floating pane at the origin with one line of
// text on screen. Screen cell (x, y) inside the content is at (x+1, y+1),
// because a floating window spends one cell per border edge.
func selectPane(t *testing.T, line string) (*app.OS, *terminal.Window) {
	t.Helper()
	em := vt.NewEmulator(40, 20)
	t.Cleanup(func() { _ = em.Close() })
	if _, err := em.Write([]byte(line)); err != nil {
		t.Fatalf("paint fixture line: %v", err)
	}
	win := &terminal.Window{Terminal: em, X: 0, Y: 0, Width: 42, Height: 22}
	o := &app.OS{
		Settings:      config.Global,
		Mode:          app.TerminalMode,
		FocusedWindow: 0,
		Windows:       []*terminal.Window{win},
		Width:         80,
		Height:        30,
	}
	return o, win
}

// press sends a left button press at a content cell.
func pressAt(o *app.OS, x, y int) {
	handleMouseClick(tea.MouseClickMsg{Button: tea.MouseLeft, X: x + 1, Y: y + 1}, o)
}

// dragTo sends a motion with the button held to a content cell.
func dragTo(o *app.OS, x, y int) {
	handleMouseMotion(tea.MouseMotionMsg{Button: tea.MouseLeft, X: x + 1, Y: y + 1}, o)
}

// release lets the button up at a content cell and returns the command the
// handler produced, which is the clipboard write when one happened.
func release(o *app.OS, x, y int) tea.Cmd {
	_, cmd := handleMouseRelease(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x + 1, Y: y + 1}, o)
	return cmd
}

// selectedText reads back what the pane's visual selection covers.
func selectedText(win *terminal.Window) string {
	if win.CopyMode == nil {
		return ""
	}
	win.RLockIO()
	defer win.RUnlockIO()
	return extractVisualText(win.CopyMode, win)
}

// A left drag over a pane's output used to grab the window, move it, and drop
// the user into window management mode. In a terminal, dragging over text
// selects the text.
func TestDragInTerminalModeSelectsTextInsteadOfMovingTheWindow(t *testing.T) {
	o, win := selectPane(t, "alpha bravo charlie")

	pressAt(o, 0, 0)
	dragTo(o, 10, 0)
	cmd := release(o, 10, 0)

	if o.Mode != app.TerminalMode {
		t.Errorf("Mode = %v after a drag inside the pane, want terminal mode: clicking output "+
			"must not throw the user out of the shell", o.Mode)
	}
	if win.X != 0 || win.Y != 0 {
		t.Errorf("window moved to (%d,%d) during a text drag", win.X, win.Y)
	}
	if got, want := selectedText(win), "alpha bravo"; got != want {
		t.Errorf("selection = %q, want %q", got, want)
	}
	if cmd == nil {
		t.Error("releasing a selection did not copy it; copy_on_select is on by default")
	}
	if !hasNotificationPrefix(o, "Copied") {
		t.Errorf("no copy confirmation in the dock; messages were %v", notificationMessages(o))
	}
}

// A stray click must never clobber the clipboard.
func TestPlainClickDoesNotCopy(t *testing.T) {
	o, win := selectPane(t, "alpha bravo charlie")

	pressAt(o, 4, 0)
	cmd := release(o, 4, 0)

	if cmd != nil {
		t.Error("a click with no drag wrote to the clipboard")
	}
	if hasNotificationPrefix(o, "Copied") {
		t.Errorf("a click with no drag reported a copy; messages were %v", notificationMessages(o))
	}
	if win.InCopyMode() {
		t.Error("a click left the pane in a copy-mode session with nothing to show")
	}
}

// Double-click selects a word, and a word in a terminal is more than a run of
// letters: a path, a URL or a flag is one thing the user wants in one grab.
func TestDoubleClickSelectsAWordIncludingItsPunctuation(t *testing.T) {
	cases := []struct {
		name string
		line string
		at   int
		want string
	}{
		{"path", "run /usr/bin/env now", 8, "/usr/bin/env"},
		{"flag", "cargo build --no-vm ok", 14, "--no-vm"},
		{"plain word", "alpha bravo charlie", 7, "bravo"},
		{"host and query", "GET example.com/a?b=1 200", 10, "example.com/a?b=1"},
		{"version", "installed v1.2.3-rc1 ok", 13, "v1.2.3-rc1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, win := selectPane(t, tc.line)

			pressAt(o, tc.at, 0)
			pressAt(o, tc.at, 0)
			release(o, tc.at, 0)

			if got := selectedText(win); got != tc.want {
				t.Errorf("double-click at column %d of %q selected %q, want %q",
					tc.at, tc.line, got, tc.want)
			}
		})
	}
}

// A colon is deliberately not a word character, so host:port and file:line come
// apart into the pieces a user usually wants on their own.
func TestDoubleClickStopsAtACharacterOutsideTheWordSet(t *testing.T) {
	o, win := selectPane(t, "main.go:42:9 error")

	pressAt(o, 2, 0)
	pressAt(o, 2, 0)
	release(o, 2, 0)

	if got, want := selectedText(win), "main.go"; got != want {
		t.Errorf("selection = %q, want %q", got, want)
	}
}

// The word set is configurable, so a user who wants host:port in one grab can
// have it.
func TestWordCharactersIsConfigurable(t *testing.T) {
	prev := config.Global.WordCharacters
	t.Cleanup(func() { config.Global.WordCharacters = prev })
	config.Global.WordCharacters = prev + ":"

	o, win := selectPane(t, "main.go:42:9 error")

	pressAt(o, 2, 0)
	pressAt(o, 2, 0)
	release(o, 2, 0)

	if got, want := selectedText(win), "main.go:42:9"; got != want {
		t.Errorf("selection = %q, want %q", got, want)
	}
}

// Double-clicking whitespace selects nothing rather than putting a space on the
// clipboard.
func TestDoubleClickOnWhitespaceSelectsNothing(t *testing.T) {
	o, win := selectPane(t, "alpha bravo")

	pressAt(o, 5, 0)
	pressAt(o, 5, 0)
	cmd := release(o, 5, 0)

	if cmd != nil {
		t.Error("double-clicking a space wrote to the clipboard")
	}
	if win.InCopyMode() && win.CopyMode.State != terminal.CopyModeNormal {
		t.Error("double-clicking a space left a selection behind")
	}
}

// Three clicks take the line. A line, not a sentence: terminal content is
// line-oriented and sentence detection over log lines or code is guesswork.
func TestTripleClickSelectsTheLine(t *testing.T) {
	o, win := selectPane(t, "alpha bravo charlie")

	pressAt(o, 6, 0)
	pressAt(o, 6, 0)
	pressAt(o, 6, 0)
	release(o, 6, 0)

	if got, want := selectedText(win), "alpha bravo charlie"; got != want {
		t.Errorf("triple-click selected %q, want the whole line %q", got, want)
	}
}

// A fourth click starts a new gesture rather than continuing to climb, and a
// click that arrives too late is a fresh single click, not the next step of the
// last one.
func TestMultiClickCountingWrapsAndTimesOut(t *testing.T) {
	_, win := selectPane(t, "alpha bravo charlie")

	for want := 1; want <= 3; want++ {
		if got := registerClick(win, 4, 0); got != want {
			t.Fatalf("click %d counted as %d", want, got)
		}
	}
	if got := registerClick(win, 4, 0); got != 1 {
		t.Errorf("a fourth click counted as %d, want 1: the gesture should start over", got)
	}

	win.LastClickTime = time.Now().Add(-2 * multiClickInterval)
	if got := registerClick(win, 4, 0); got != 1 {
		t.Errorf("a click %v after the last counted as %d, want 1", 2*multiClickInterval, got)
	}

	// A cell of pointer drift between clicks is a double-click, not two
	// singles: one cell is a handful of pixels.
	win.ClickCount = 0
	win.LastClickTime = time.Time{}
	if got := registerClick(win, 4, 0); got != 1 {
		t.Fatalf("first click of a fresh gesture counted as %d", got)
	}
	if got := registerClick(win, 5, 0); got != 2 {
		t.Errorf("a click one cell over counted as %d, want 2", got)
	}
	if got := registerClick(win, 9, 0); got != 1 {
		t.Errorf("a click five cells over counted as %d, want 1", got)
	}
}

// Some people do not want a stray drag overwriting their clipboard.
func TestCopyOnSelectCanBeTurnedOff(t *testing.T) {
	prev := config.Global.CopyOnSelect
	t.Cleanup(func() { config.Global.CopyOnSelect = prev })
	config.Global.CopyOnSelect = false

	o, win := selectPane(t, "alpha bravo charlie")

	pressAt(o, 0, 0)
	dragTo(o, 10, 0)
	cmd := release(o, 10, 0)

	if cmd != nil {
		t.Error("copy_on_select is off but the release still wrote to the clipboard")
	}
	if got, want := selectedText(win), "alpha bravo"; got != want {
		t.Errorf("the selection itself should still be made: got %q, want %q", got, want)
	}
}

// A pane whose application asked for the mouse owns the whole gesture, exactly
// as it owns the wheel. Selection must not steal a press from vim.
func TestPressInAMouseTrackingPaneIsNotStolenBySelection(t *testing.T) {
	o, win := selectPane(t, "alpha bravo charlie")
	if _, err := win.Terminal.Write([]byte("\x1b[?1000h")); err != nil {
		t.Fatalf("enable mouse tracking: %v", err)
	}

	pressAt(o, 4, 0)

	if win.InCopyMode() {
		t.Error("a press in a mouse-tracking pane started a tuios selection")
	}
	if o.Dragging {
		t.Error("a press in a mouse-tracking pane started a tuios drag gesture")
	}
}

// The selection stays highlighted after the copy, so the user can see what they
// got, and the next press replaces it.
func TestSelectionSurvivesTheCopyAndIsReplacedByTheNextPress(t *testing.T) {
	o, win := selectPane(t, "alpha bravo charlie")

	pressAt(o, 0, 0)
	dragTo(o, 10, 0)
	release(o, 10, 0)

	if win.CopyMode.State != terminal.CopyModeVisualChar {
		t.Fatalf("selection state after copy = %v, want the highlight to stay up", win.CopyMode.State)
	}

	pressAt(o, 12, 0)
	dragTo(o, 18, 0)
	release(o, 18, 0)

	if got, want := selectedText(win), "charlie"; got != want {
		t.Errorf("second selection = %q, want %q", got, want)
	}
}

// Selecting is not entering copy mode: the dock must not start showing the
// copy-mode key hints because someone dragged over a line.
func TestSelectingDoesNotPresentAsCopyMode(t *testing.T) {
	o, win := selectPane(t, "alpha bravo charlie")

	pressAt(o, 0, 0)
	dragTo(o, 10, 0)
	release(o, 10, 0)

	if win.CopyModeVisible() {
		t.Error("a mouse selection put the pane into a visible copy mode")
	}
}

// Typing after a selection returns the pane to normal, selection and all.
func TestTypingClearsAMouseSelection(t *testing.T) {
	o, win := selectPane(t, "alpha bravo charlie")

	pressAt(o, 0, 0)
	dragTo(o, 10, 0)
	release(o, 10, 0)

	HandleTerminalModeKey(tea.KeyPressMsg{Code: 'x', Text: "x"}, o)

	if win.InCopyMode() {
		t.Error("typing left the selection and its copy-mode session in place")
	}
}
