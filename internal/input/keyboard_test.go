package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// TestKeysTypedRightAfterEnteringTerminalModeReachThePTY. A key typed within
// 150 ms of entering terminal mode used to be dropped on the floor by a guard
// against mouse-sequence fragments from a host mouse-mode switch that the client
// no longer makes (the view holds the host in all-motion tracking for the whole
// session). What the guard did in practice was eat the first keystrokes of
// anyone who presses i or clicks a pane and types at once, and the loss was
// invisible: no notification, no log, no echo. Measured on the real binary, a
// line typed 50 ms after entering terminal mode never reached the shell.
//
// Negative control: put the guard back at the top of HandleTerminalModeKey and
// both cases fail with an empty PTY.
func TestKeysTypedRightAfterEnteringTerminalModeReachThePTY(t *testing.T) {
	t.Run("i then a letter", func(t *testing.T) {
		o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.WindowManagementMode)
		o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'i', Text: "i"}, o)
		if o.Mode != app.TerminalMode {
			t.Fatalf("i did not enter terminal mode (mode %v)", o.Mode)
		}
		o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'a', Text: "a"}, o)
		_, _ = HandleKeyPress(tea.KeyPressMsg{Code: '2', Text: "2"}, o)
		if string(pty.got) != "a2" {
			t.Fatalf("the shell got %q for keys typed at once after entering terminal mode, want %q", pty.got, "a2")
		}
	})
	t.Run("click then a letter", func(t *testing.T) {
		o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.WindowManagementMode)
		win := o.Windows[0]
		cx, cy := win.X+2, win.Y+2
		HandleInput(tea.MouseClickMsg{Button: tea.MouseLeft, X: cx, Y: cy}, o)
		HandleInput(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: cx, Y: cy}, o)
		if o.Mode != app.TerminalMode {
			t.Fatalf("the click did not enter terminal mode (mode %v)", o.Mode)
		}
		HandleInput(tea.KeyPressMsg{Code: 'l', Text: "l"}, o)
		HandleInput(tea.KeyPressMsg{Code: 's', Text: "s"}, o)
		if string(pty.got) != "ls" {
			t.Fatalf("the shell got %q for keys typed at once after a click, want %q", pty.got, "ls")
		}
	})
}

// TestMacOSOptionGlyphsAreReservedOnlyOnDarwin verifies that the macOS
// Option-key glyphs only count as reserved chords on darwin. On other platforms
// these glyphs (e.g. £, ⇥) are ordinary typed characters and must fall through
// to the shell rather than being intercepted as workspace or window shortcuts.
func TestMacOSOptionGlyphsAreReservedOnlyOnDarwin(t *testing.T) {
	// £ is Option+3 on a US Mac layout, but Shift+3 on a UK layout.
	if got := isReservedTerminalChord(tea.KeyPressMsg{Code: '£', Text: "£"}); got != runtimeIsDarwin() {
		t.Errorf("isReservedTerminalChord(£) = %v, want %v", got, runtimeIsDarwin())
	}

	// ⇥ is Option+Tab on macOS, but an ordinary glyph elsewhere.
	if got := isReservedTerminalChord(tea.KeyPressMsg{Code: '⇥', Text: "⇥"}); got != runtimeIsDarwin() {
		t.Errorf("isReservedTerminalChord(⇥) = %v, want %v", got, runtimeIsDarwin())
	}

	// A real Alt chord is reserved on every platform.
	if !isReservedTerminalChord(tea.KeyPressMsg{Code: '1', Mod: tea.ModAlt, Text: "1"}) {
		t.Error("alt+1 should be a reserved chord on every platform")
	}

	// A bare letter never is, whatever it happens to be bound to.
	if isReservedTerminalChord(tea.KeyPressMsg{Code: 'a', Text: "a"}) {
		t.Error("a bare letter must reach the shell, not be treated as a chord")
	}

}

// TestTerminalPrefixChordNotRecorded verifies that prefix chords in terminal
// mode are not captured into a tape recording. Previously the leader key and
// its following command key were recorded before prefix routing, so a
// ctrl+b <cmd> chord replayed a stray 0x02 and command character into the shell.
func TestTerminalPrefixChordNotRecorded(t *testing.T) {
	rec := tape.NewRecorder()
	rec.Start()
	o := &app.OS{Settings: config.Global, Mode: app.TerminalMode, TapeRecorder: rec}

	// ctrl+b activates prefix mode and must not be recorded.
	ctrlB := tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}
	HandleTerminalModeKey(ctrlB, o)
	if !o.PrefixActive {
		t.Fatal("ctrl+b should activate prefix mode")
	}

	// The following prefix command key routes to the prefix dispatcher, not the
	// PTY, and must not be recorded either.
	esc := tea.KeyPressMsg{Code: tea.KeyEscape}
	HandleTerminalModeKey(esc, o)

	if got := rec.CommandCount(); got != 0 {
		t.Errorf("prefix chord recorded %d commands, want 0: %+v", got, rec.GetCommands())
	}
}

// TestRecordTerminalKeyClassification verifies that keys forwarded to the PTY
// are classified correctly: printable ASCII accumulates as a Type command while
// special keys are recorded as KeyCombos (and flush any pending typed text).
func TestRecordTerminalKeyClassification(t *testing.T) {
	rec := tape.NewRecorder()
	rec.Start()
	o := &app.OS{Settings: config.Global, TapeRecorder: rec}

	recordTerminalKey(o, tea.KeyPressMsg{Code: 'l', Text: "l"})
	recordTerminalKey(o, tea.KeyPressMsg{Code: 's', Text: "s"})
	recordTerminalKey(o, tea.KeyPressMsg{Code: tea.KeyEnter})

	cmds := rec.GetCommands()
	// "ls" accumulates into one Type command, flushed by the Enter KeyCombo.
	if len(cmds) != 2 {
		t.Fatalf("got %d commands, want 2: %+v", len(cmds), cmds)
	}
	if cmds[0].Type != tape.CommandTypeType || len(cmds[0].Args) == 0 || cmds[0].Args[0] != "ls" {
		t.Errorf("first command = %+v, want Type \"ls\"", cmds[0])
	}
	if cmds[1].Type != tape.CommandTypeEnter {
		t.Errorf("second command = %+v, want Enter", cmds[1])
	}
}
