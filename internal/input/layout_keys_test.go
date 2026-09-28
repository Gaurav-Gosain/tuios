package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// These pin issue #202: with a Ukrainian layout the key under the US I types
// "ш", and bindings must still answer to the physical key. The events are what
// Bubble Tea decodes from a Kitty keyboard report with alternate keys on, such
// as CSI 1096::105 u: the produced code, and the US-layout key behind it in
// BaseCode.

// ukr is a key on the Ukrainian layout: the character it produces and the
// US-layout key at the same position.
func ukr(produced, base rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: produced, BaseCode: base, Text: string(produced)}
}

// ukrShift is the same key with Shift held.
func ukrShift(produced, shifted, base rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: produced, ShiftedCode: shifted, BaseCode: base, Text: string(shifted), Mod: tea.ModShift}
}

func TestBaseLayoutKeyRunsWindowModeBinding(t *testing.T) {
	o, _ := osWithFocusedPane(t, config.DefaultConfig(), app.WindowManagementMode)
	o, _ = HandleKeyPress(ukr('ш', 'i'), o)
	if o.Mode != app.TerminalMode {
		t.Fatalf("ш on the I key did not run enter_terminal_mode (mode %v)", o.Mode)
	}
}

func TestBaseLayoutKeyRunsPrefixBinding(t *testing.T) {
	o, _ := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	o = leader(o, ukr('ш', 'i'))
	if !o.ShowInbox {
		t.Fatal("leader then ш on the I key did not open the Inbox")
	}
}

// A shifted letter is bound as the capital letter, and the base-layout key has
// to be read that way too: leader then Shift+S opens the session switcher.
func TestShiftedBaseLayoutKeyRunsPrefixBinding(t *testing.T) {
	o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	o = leader(o, ukrShift('і', 'І', 's'))
	if !o.ShowSessionSwitcher {
		t.Fatalf("leader then Shift+і on the S key did not open the session switcher (pane got %q)", pty.got)
	}
}

func TestBaseLayoutKeyRunsInboxBinding(t *testing.T) {
	o := inboxInputOS(t)
	o.OpenInbox("")
	// esc and q close the Inbox. q is й on the Ukrainian layout.
	o, _ = HandleKeyPress(ukr('й', 'q'), o)
	if o.ShowInbox {
		t.Fatal("й on the Q key did not close the Inbox")
	}
}

// The quit menu switches on fixed keys: n closes it. n is т on the Ukrainian
// layout.
func TestBaseLayoutKeyAnswersQuitMenu(t *testing.T) {
	o := twoPaneWM(t)
	o.ShowQuitMenu = true
	o, _ = HandleKeyPress(ukr('т', 'n'), o)
	if o.ShowQuitMenu {
		t.Fatal("т on the N key did not close the quit menu")
	}
}

// A binding on the produced character wins over the base-layout key.
func TestProducedKeyBindingWinsOverBaseLayoutKey(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Keybindings.PrefixMode["prefix_help"] = []string{"?", "ш"}
	o, _ := osWithFocusedPane(t, cfg, app.TerminalMode)
	o = leader(o, ukr('ш', 'i'))
	if o.ShowInbox {
		t.Fatal("the base-layout key ran prefix_inbox although ш is bound")
	}
	if !o.ShowHelp {
		t.Fatal("the explicit ш binding did not run")
	}
}

// A text field takes the character typed, never the base-layout key.
func TestTextFieldKeepsProducedCharacter(t *testing.T) {
	o := twoPaneOS(t)
	w := o.Windows[0]
	o.BeginRenameWindow(w)
	o.RenameBuffer = ""
	o, _ = HandleKeyPress(ukr('ш', 'i'), o)
	o, _ = HandleKeyPress(ukr('о', 'j'), o)
	if o.RenameBuffer != "шо" {
		t.Fatalf("rename buffer = %q, want %q", o.RenameBuffer, "шо")
	}
}

// Typing into a pane sends the character typed, never the base-layout key.
func TestPaneGetsProducedCharacter(t *testing.T) {
	o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	o, _ = HandleKeyPress(ukr('ш', 'i'), o)
	_, _ = HandleKeyPress(ukrShift('ш', 'Ш', 'i'), o)
	if got := string(pty.got); got != "шШ" {
		t.Fatalf("the pane got %q, want %q", got, "шШ")
	}
}

// Ctrl+с on the Ukrainian layout has no control code of its own. The shell has
// to get Ctrl+C, 0x03, from the base-layout key.
func TestCtrlOnNonLatinKeyReachesPaneAsControlCode(t *testing.T) {
	o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	_, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'с', BaseCode: 'c', Mod: tea.ModCtrl}, o)
	if got := string(pty.got); got != "\x03" {
		t.Fatalf("the pane got %q for Ctrl+с, want %q", got, "\x03")
	}
}

// In report-all-keys mode the terminal reports Shift pressed on its own. It
// must not end the prefix before the shifted letter arrives.
func TestModifierPressKeepsPrefix(t *testing.T) {
	o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}, o)
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyLeftShift, Mod: tea.ModShift}, o)
	if !o.PrefixActive {
		t.Fatalf("a bare Shift press ended the prefix (pane got %q)", pty.got)
	}
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 's', ShiftedCode: 'S', Text: "S", Mod: tea.ModShift}, o)
	if !o.ShowSessionSwitcher {
		t.Fatal("leader, Shift, S did not open the session switcher")
	}
}

// Copy mode's motions are fixed keys too: j moves down. j is о on the
// Ukrainian layout.
func TestBaseLayoutKeyDrivesCopyMode(t *testing.T) {
	o, _ := osWithFocusedPane(t, config.DefaultConfig(), app.WindowManagementMode)
	w := o.GetFocusedWindow()
	w.EnterCopyMode()
	if !w.InCopyMode() {
		t.Skip("copy mode did not start on the test pane")
	}
	w.CopyMode.CursorY = 0
	o, _ = HandleKeyPress(ukr('о', 'j'), o)
	if w.CopyMode.CursorY != 1 {
		t.Fatalf("о on the J key left the copy cursor on row %d, want 1", w.CopyMode.CursorY)
	}
}
