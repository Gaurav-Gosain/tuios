package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// scrollPane builds a pane whose emulator has far more history than fits on
// screen, sized like a real window rather than the minimum, so the midpoint the
// old wheel path walked the cursor to is many rows away from the bottom.
func scrollPane(t *testing.T) (*app.OS, *terminal.Window) {
	t.Helper()
	em := vt.NewEmulator(40, 20)
	t.Cleanup(func() { _ = em.Close() })
	for i := range 200 {
		_, _ = em.Write([]byte(fmt.Sprintf("line %d\r\n", i)))
	}
	if em.ScrollbackLen() < 50 {
		t.Fatalf("emulator produced %d scrollback lines; the test needs more", em.ScrollbackLen())
	}
	win := &terminal.Window{Terminal: em, Width: 42, Height: 22}
	o := &app.OS{
		Mode:          app.TerminalMode,
		FocusedWindow: 0,
		Windows:       []*terminal.Window{win},
	}
	return o, win
}

func wheel(o *app.OS, button tea.MouseButton, times int) {
	for range times {
		handleMouseWheel(tea.MouseWheelMsg{Button: button}, o)
	}
}

// The wheel used to drive the k/j cursor motions, which keep the cursor near
// the middle of the pane and only scroll once it gets there. With the cursor
// down by the shell prompt, every early click moved the cursor and nothing
// else: on a tall pane that is several whole clicks of the view not moving.
//
// One click, one scroll, from wherever the cursor happens to be.
func TestWheelUpScrollsOnTheFirstClickWhateverTheCursorRow(t *testing.T) {
	for _, cursorRow := range []int{0, 10, 19} {
		t.Run(fmt.Sprintf("cursor row %d", cursorRow), func(t *testing.T) {
			o, win := scrollPane(t)
			win.EnterCopyMode()
			win.CopyMode.CursorY = cursorRow

			wheel(o, tea.MouseWheelUp, 1)

			if win.CopyMode.ScrollOffset != config.ScrollLines {
				t.Fatalf("one wheel click from cursor row %d scrolled %d lines, want %d. "+
					"The wheel is walking the cursor instead of moving the viewport.",
					cursorRow, win.CopyMode.ScrollOffset, config.ScrollLines)
			}
			if win.ScrollbackOffset != win.CopyMode.ScrollOffset {
				t.Errorf("ScrollbackOffset = %d, want it in step with the copy-mode offset %d",
					win.ScrollbackOffset, win.CopyMode.ScrollOffset)
			}
		})
	}
}

// Entering copy mode is a mechanism for rendering scrollback, not a mode the
// user asked for, so the wheel says nothing and the dock does not change.
func TestWheelDoesNotAnnounceCopyMode(t *testing.T) {
	o, win := scrollPane(t)

	wheel(o, tea.MouseWheelUp, 2)

	if len(o.Notifications) != 0 {
		t.Fatalf("scrolling raised %d notification(s), first %q; turning the wheel must not "+
			"announce a mode or teach keybindings", len(o.Notifications), o.Notifications[0].Message)
	}
	if !win.InCopyMode() {
		t.Fatal("the wheel did not scroll at all")
	}
	if win.CopyModeVisible() {
		t.Error("a wheel scroll presented itself as copy mode; the dock would show the copy-mode key hints")
	}
}

// Scrolling back to the bottom is the same event as being done: the session
// that existed only to render scrollback goes away, silently, and the pane is
// on live output again.
func TestWheelDownToBottomReturnsToLiveOutput(t *testing.T) {
	o, win := scrollPane(t)

	wheel(o, tea.MouseWheelUp, 4)
	if win.CopyMode.ScrollOffset == 0 {
		t.Fatal("wheel up did not scroll")
	}

	wheel(o, tea.MouseWheelDown, 4)

	if win.InCopyMode() {
		t.Fatalf("still in copy mode after scrolling back to the bottom "+
			"(offset %d, cursor row %d); the user is stranded in a mode with no way "+
			"back that they were told about", win.CopyMode.ScrollOffset, win.CopyMode.CursorY)
	}
	if win.ScrollbackOffset != 0 {
		t.Errorf("ScrollbackOffset = %d, want 0 (live output)", win.ScrollbackOffset)
	}
	if len(o.Notifications) != 0 {
		t.Errorf("leaving raised %d notification(s), first %q; a silent entry deserves a silent exit",
			len(o.Notifications), o.Notifications[0].Message)
	}
}

// Typing is how a user says they are done reading. A terminal with no modes
// snaps to the bottom and types the character; so does this.
func TestTypingAfterAWheelScrollReturnsToLiveOutput(t *testing.T) {
	o, win := scrollPane(t)
	wheel(o, tea.MouseWheelUp, 3)
	if win.ScrollbackOffset == 0 {
		t.Fatal("wheel up did not scroll")
	}

	// "e" is a copy-mode motion (word end). Under the old behaviour it moved
	// the cursor and never reached the shell.
	HandleTerminalModeKey(tea.KeyPressMsg{Code: 'e', Text: "e"}, o)

	if win.InCopyMode() {
		t.Fatal("typing left the pane in copy mode; the keystroke was eaten as a motion " +
			"instead of reaching the shell")
	}
	if win.ScrollbackOffset != 0 {
		t.Errorf("ScrollbackOffset = %d after typing, want 0: typing must snap back to live output",
			win.ScrollbackOffset)
	}
}

// Esc is the reflex for "get me out of this", and a bare Esc into a shell in vi
// mode or a readline meta prefix is not a no-op, so it is the one key that
// leaves the scrolled view without also being typed.
func TestEscapeAfterAWheelScrollIsNotForwarded(t *testing.T) {
	o, win := scrollPane(t)
	wheel(o, tea.MouseWheelUp, 3)

	HandleTerminalModeKey(tea.KeyPressMsg{Code: tea.KeyEscape}, o)

	if win.InCopyMode() {
		t.Error("esc did not leave the scrolled view")
	}
	if o.Mode != app.TerminalMode {
		t.Errorf("Mode = %v after esc, want terminal mode", o.Mode)
	}
}

// Copy mode the user asked for is a mode they are holding. The wheel scrolls it
// and leaves it alone: it announces itself in the dock, and q and Esc exit.
func TestWheelDoesNotDropAnExplicitCopyModeSession(t *testing.T) {
	o, win := scrollPane(t)
	win.EnterCopyMode() // explicit: what the prefix binding and the palette do

	wheel(o, tea.MouseWheelUp, 3)
	wheel(o, tea.MouseWheelDown, 10)

	if !win.InCopyMode() {
		t.Fatal("the wheel threw the user out of a copy mode session they asked for")
	}
	if !win.CopyModeVisible() {
		t.Error("an explicit session stopped presenting itself as a mode")
	}
	if win.CopyMode.ScrollOffset != 0 {
		t.Errorf("ScrollOffset = %d, want 0: scrolling down must still reach the bottom", win.CopyMode.ScrollOffset)
	}
}

// A pane with nothing behind it has nothing to scroll to. Turning the wheel
// over a fresh shell used to drop it into copy mode showing the same screen.
func TestWheelOverAPaneWithNoScrollbackDoesNothing(t *testing.T) {
	em := vt.NewEmulator(40, 20)
	t.Cleanup(func() { _ = em.Close() })
	win := &terminal.Window{Terminal: em, Width: 42, Height: 22}
	o := &app.OS{Mode: app.TerminalMode, FocusedWindow: 0, Windows: []*terminal.Window{win}}

	wheel(o, tea.MouseWheelUp, 3)

	if win.InCopyMode() {
		t.Error("the wheel entered copy mode over a pane with no scrollback")
	}
	if len(o.Notifications) != 0 {
		t.Errorf("the wheel raised %q over a pane with nothing to scroll", o.Notifications[0].Message)
	}
}

// vim, less and htop ask for the mouse and must keep getting the wheel
// themselves. tuios must not scroll its own scrollback underneath them.
func TestWheelOverAMouseTrackingAppIsNotConsumedByScrollback(t *testing.T) {
	o, win := scrollPane(t)
	if _, err := win.Terminal.Write([]byte("\x1b[?1000h")); err != nil {
		t.Fatalf("enable mouse tracking: %v", err)
	}
	if !win.Terminal.HasMouseMode() {
		t.Fatal("fixture did not enable mouse tracking")
	}

	wheel(o, tea.MouseWheelUp, 3)

	if win.InCopyMode() {
		t.Error("tuios scrolled its own scrollback for a pane whose application owns the mouse")
	}
	if win.ScrollbackOffset != 0 {
		t.Errorf("ScrollbackOffset = %d, want 0", win.ScrollbackOffset)
	}
}

// The alternate screen has no scrollback worth showing, and an application
// sitting on it redraws the whole screen anyway.
func TestWheelOverAnAltScreenPaneDoesNotScroll(t *testing.T) {
	o, win := scrollPane(t)
	win.SetAltScreen(true)

	wheel(o, tea.MouseWheelUp, 3)

	if win.InCopyMode() {
		t.Error("the wheel scrolled scrollback for an alt-screen pane")
	}
}

// The cursor rides the viewport rather than staying on a screen row, so a v
// pressed after a scroll starts from the line the user is looking at. Whatever
// it does, it may never leave the rows being drawn.
func TestWheelKeepsTheCopyCursorOnScreen(t *testing.T) {
	o, win := scrollPane(t)
	win.EnterCopyMode()

	for range 30 {
		wheel(o, tea.MouseWheelUp, 1)
		if win.CopyMode.CursorY < 0 || win.CopyMode.CursorY >= win.ContentHeight() {
			t.Fatalf("cursor row %d is outside the %d drawn rows after scrolling up",
				win.CopyMode.CursorY, win.ContentHeight())
		}
	}
	for range 60 {
		wheel(o, tea.MouseWheelDown, 1)
		if win.CopyMode.CursorY < 0 || win.CopyMode.CursorY >= win.ContentHeight() {
			t.Fatalf("cursor row %d is outside the %d drawn rows after scrolling down",
				win.CopyMode.CursorY, win.ContentHeight())
		}
	}
}

// Shift+Up is the keyboard spelling of the wheel and shares its entry and exit:
// one line per press, silent, and back to live output at the bottom.
func TestShiftArrowScrollIsSilentAndReturnsToLiveOutput(t *testing.T) {
	o, win := scrollPane(t)

	for range 5 {
		HandleTerminalModeKey(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}, o)
	}
	if win.CopyMode.ScrollOffset != 5 {
		t.Fatalf("five shift+up presses scrolled %d lines, want 5", win.CopyMode.ScrollOffset)
	}
	if len(o.Notifications) != 0 {
		t.Errorf("shift+up raised %q", o.Notifications[0].Message)
	}

	for range 5 {
		HandleTerminalModeKey(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}, o)
	}
	if win.InCopyMode() {
		t.Error("shift+down to the bottom left the pane in copy mode")
	}
}

// Scrolling in window management mode is the same gesture and gets the same
// treatment, and a window-manager binding pressed afterwards must not be eaten.
func TestWindowModeWheelIsSilentAndYieldsToBindings(t *testing.T) {
	o, win := scrollPane(t)
	o.Mode = app.WindowManagementMode

	wheel(o, tea.MouseWheelUp, 2)
	if win.CopyMode.ScrollOffset != 2*config.ScrollLines {
		t.Fatalf("ScrollOffset = %d, want %d", win.CopyMode.ScrollOffset, 2*config.ScrollLines)
	}
	if len(o.Notifications) != 0 {
		t.Fatalf("the wheel raised %q in window management mode", o.Notifications[0].Message)
	}

	HandleWindowManagementModeKey(tea.KeyPressMsg{Code: 'n', Text: "n"}, o)
	if win.InCopyMode() {
		t.Error("a window-manager binding was swallowed by the scrolled view")
	}
}
