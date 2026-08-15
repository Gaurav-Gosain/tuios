package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
)

// holdOS is an OS in terminal mode with hold-to-window-mode bound to trigger and
// a host that has agreed to report key releases.
func holdOS(t *testing.T, trigger string) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Keybindings.ModeControl[app.HoldModeAction] = []string{trigger}
	o := app.NewOS(app.OSOptions{
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
	})
	o.Width, o.Height = 160, 40
	o.EffectiveWidth, o.EffectiveHeight = 160, 40
	o.Windows = []*terminal.Window{
		{ID: "a", CustomName: "one", X: 0, Y: 0, Width: 60, Height: 30, Workspace: 1},
	}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	o.Mode = app.TerminalMode
	o.NoteKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: ansi.KittyAllFlags})
	return o
}

// leftAltPress is what a terminal in report-all-keys mode sends for the Option
// key itself: a key event of its own, carrying the modifier it sets.
func leftAltPress() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyLeftAlt, Mod: tea.ModAlt}
}

func leftAltRelease() tea.KeyReleaseMsg {
	return tea.KeyReleaseMsg{Code: tea.KeyLeftAlt}
}

func feed(o *app.OS, msg tea.Msg) *app.OS {
	m, _ := HandleInput(msg, o)
	return m.(*app.OS)
}

// The whole contract: hold the key and you are in window mode, let go and you
// are exactly where you were.
func TestHoldKeyBorrowsWindowModeAndGivesItBack(t *testing.T) {
	o := holdOS(t, "leftalt")

	o = feed(o, leftAltPress())
	if o.Mode != app.WindowManagementMode || !o.HoldModeActive() {
		t.Fatalf("holding the key left mode %v, active %v", o.Mode, o.HoldModeActive())
	}

	o = feed(o, leftAltRelease())
	if o.Mode != app.TerminalMode || o.HoldModeActive() {
		t.Fatalf("releasing left mode %v, active %v; want terminal mode", o.Mode, o.HoldModeActive())
	}
	if o.FocusedWindow != 0 {
		t.Errorf("releasing moved the focus to window %d", o.FocusedWindow)
	}
}

// A chord struck while the trigger is held has the trigger's own modifier on it.
// It has to run the window-mode action bound to the key that was tapped, and the
// pane must never see it: mode is already window mode when it arrives, which is
// what keeps it off the PTY.
func TestChordWhileHeldRunsTheWindowModeAction(t *testing.T) {
	o := holdOS(t, "leftalt")
	o = feed(o, leftAltPress())

	// toggle_help is bound to "?" in window mode; held, it arrives as alt+?.
	o = feed(o, tea.KeyPressMsg{Code: '?', Mod: tea.ModAlt, Text: "?"})
	if !o.ShowHelp {
		t.Fatal("the chord did not reach the window-mode action bound to its key")
	}
	if o.Mode != app.WindowManagementMode {
		t.Fatalf("mode was %v while held, so the key could have reached the pane", o.Mode)
	}
}

// Repeat events keep the hold; they are the same physical press.
func TestKeyRepeatDoesNotEndTheHold(t *testing.T) {
	o := holdOS(t, "leftalt")
	o = feed(o, leftAltPress())

	repeat := leftAltPress()
	repeat.IsRepeat = true
	o = feed(o, repeat)
	if !o.HoldModeActive() {
		t.Fatal("a repeat of the held key ended the hold")
	}
}

// A second press with no release between means the release was lost. Ending the
// hold is the only reading that cannot strand the user in a mode they did not
// choose and cannot leave.
func TestSecondPressEndsAHoldWhoseReleaseWasLost(t *testing.T) {
	o := holdOS(t, "leftalt")
	o = feed(o, leftAltPress())
	o = feed(o, leftAltPress())

	if o.HoldModeActive() || o.Mode != app.TerminalMode {
		t.Fatalf("a second press left active %v mode %v", o.HoldModeActive(), o.Mode)
	}
}

// Acting on the mode while holding it is the point, so a mode the action chose
// outranks the one the release would restore.
func TestModeChosenWhileHeldSurvivesTheRelease(t *testing.T) {
	o := holdOS(t, "leftalt")
	o.Mode = app.WindowManagementMode
	o = feed(o, leftAltPress())

	// enter_terminal_mode is bound to "i".
	o = feed(o, tea.KeyPressMsg{Code: 'i', Mod: tea.ModAlt, Text: "i"})
	o = feed(o, leftAltRelease())

	if o.Mode != app.TerminalMode {
		t.Fatalf("the release undid the mode the held chord chose: %v", o.Mode)
	}
}

// Unbound is the default, and an unbound trigger must leave every key alone.
func TestNoHoldKeyLeavesKeysAlone(t *testing.T) {
	o := holdOS(t, "")
	o = feed(o, leftAltPress())

	if o.HoldModeActive() || o.Mode != app.TerminalMode {
		t.Fatalf("an unbound hold key still armed: active %v mode %v", o.HoldModeActive(), o.Mode)
	}
}

// A terminal that answered the enhancement query without event-type support
// cannot report a release, so the feature must do nothing at all rather than
// arm a mode with no way out.
func TestHoldDoesNotArmWhereReleasesAreNotReported(t *testing.T) {
	o := holdOS(t, "leftalt")
	o.NoteKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: ansi.KittyDisambiguateEscapeCodes})

	o = feed(o, leftAltPress())
	if o.HoldModeActive() {
		t.Fatal("armed on a terminal that cannot report the release")
	}
	if o.HoldModeUnsupportedReason() == "" {
		t.Error("nothing explains why the configured hold key is inert")
	}
}

// Losing focus while holding means the release will never arrive.
func TestFocusLossEndsTheHold(t *testing.T) {
	o := holdOS(t, "leftalt")
	o = feed(o, leftAltPress())

	m, _ := o.Update(tea.BlurMsg{})
	o = m.(*app.OS)
	if o.HoldModeActive() || o.Mode != app.TerminalMode {
		t.Fatalf("focus loss left active %v mode %v", o.HoldModeActive(), o.Mode)
	}
}

// The pill has to say which mode is in effect, and a momentary one has to look
// different from the mode it borrows.
func TestHoldModeIsVisibleInTheDock(t *testing.T) {
	o := holdOS(t, "leftalt")
	o = feed(o, leftAltPress())

	if content := o.View().Content; !strings.Contains(content, "HOLD") {
		t.Fatalf("the dock does not say HOLD while the key is held:\n%s", content)
	}
}

// An ordinary key works as a trigger too, and asking for one must not drag the
// whole session into report-all-keys mode.
func TestOrdinaryKeyTriggerDoesNotNeedReportAllKeys(t *testing.T) {
	o := holdOS(t, "f13")
	if o.HoldModeNeedsAllKeys() {
		t.Fatal("an ordinary trigger asked the terminal for report-all-keys")
	}

	o = feed(o, tea.KeyPressMsg{Code: tea.KeyF13})
	if !o.HoldModeActive() || o.Mode != app.WindowManagementMode {
		t.Fatalf("f13 did not arm: active %v mode %v", o.HoldModeActive(), o.Mode)
	}
	o = feed(o, tea.KeyReleaseMsg{Code: tea.KeyF13})
	if o.HoldModeActive() || o.Mode != app.TerminalMode {
		t.Fatalf("releasing f13 left active %v mode %v", o.HoldModeActive(), o.Mode)
	}
}
