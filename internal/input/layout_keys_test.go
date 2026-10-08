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
	_, _ = HandleKeyPress(ukr('о', 'j'), o)
	if w.CopyMode.CursorY != 1 {
		t.Fatalf("о on the J key left the copy cursor on row %d, want 1", w.CopyMode.CursorY)
	}
}

// A Latin layout's own letters keep their meaning. With report-all keys on,
// every plain letter carries its US-position key, and reading an unbound one
// by position ran the binding there: AZERTY "a" is on the US q key (quit), and
// Dvorak puts "'" on q, "o" on s (the sidebar) and "y" on t (tiling). Dvorak
// "p" on r (rename) was a case here until p became toggle_pip's own key.
func TestLatinLayoutLetterDoesNotRunUSPositionBinding(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{"azerty a", tea.KeyPressMsg{Code: 'a', BaseCode: 'q', Text: "a"}},
		{"dvorak quote", tea.KeyPressMsg{Code: '\'', BaseCode: 'q', Text: "'"}},
		{"dvorak o", tea.KeyPressMsg{Code: 'o', BaseCode: 's', Text: "o"}},
		{"dvorak y", tea.KeyPressMsg{Code: 'y', BaseCode: 't', Text: "y"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := twoPaneWM(t)
			o.AutoTiling = false
			if got := lookupAction(nil, tc.msg, o.KeybindRegistry.GetAction); got != "" {
				t.Fatalf("%q resolves to %q, want nothing", tc.msg.Text, got)
			}
			o, _ = HandleKeyPress(tc.msg, o)
			if o.ShowQuitMenu || o.Renaming() || o.AutoTiling {
				t.Fatalf("%q ran a binding (quit %v, rename %v, tiling %v)",
					tc.msg.Text, o.ShowQuitMenu, o.Renaming(), o.AutoTiling)
			}
		})
	}
}

// A key that beat the switch to report-all keys arrives as bare text with no
// base-layout key. After the leader it is dropped, not typed into the pane.
func TestLeaderDropsLayoutKeyThatBeatTheFlags(t *testing.T) {
	o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	o.NoteKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: 29})
	o.NoteKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: 5})
	o = leader(o, tea.KeyPressMsg{Code: 'ш', Text: "ш"})
	if len(pty.got) != 0 {
		t.Fatalf("the pane got %q for a ш typed before the host switched", pty.got)
	}
	// The prefix stays pending, so the key can be typed again once the host
	// has switched and reports its base-layout key.
	if !o.PrefixActive {
		t.Fatal("the dropped key ended the prefix")
	}
	o.NoteKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: 29})
	o, _ = HandleKeyPress(ukr('ш', 'i'), o)
	if !o.ShowInbox {
		t.Fatal("ш typed again after the switch did not open the Inbox")
	}
}

// A host that never granted report-all keys keeps the old behaviour: the
// unbound key after the leader goes to the pane.
func TestLeaderForwardsLayoutKeyWhenHostNeverSwitches(t *testing.T) {
	o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	o.NoteKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: 5})
	_ = leader(o, tea.KeyPressMsg{Code: 'ш', Text: "ш"})
	if got := string(pty.got); got != "ш" {
		t.Fatalf("the pane got %q, want %q", got, "ш")
	}
}

// A bare Shift press goes to a pane only when the pane asked for every key,
// and then under its kitty code.
func TestModifierPressReachesOnlyAnAllKeysPane(t *testing.T) {
	shift := tea.KeyPressMsg{Code: tea.KeyLeftShift, Mod: tea.ModShift}

	o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	_, _ = o.Windows[0].Terminal.Write([]byte("\x1b[>1u"))
	_, _ = HandleKeyPress(shift, o)
	if len(pty.got) != 0 {
		t.Fatalf("a disambiguate-only pane got %q for a bare Shift", pty.got)
	}

	o, pty = osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	_, _ = o.Windows[0].Terminal.Write([]byte("\x1b[>9u"))
	_, _ = HandleKeyPress(shift, o)
	if got := string(pty.got); got != "\x1b[57441;2u" {
		t.Fatalf("an all-keys pane got %q for a bare Shift, want %q", got, "\x1b[57441;2u")
	}
}

// The shifted key survives the decoder writing the base key over it, with
// Ctrl held as well.
func TestShiftedCodeIsRebuilt(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.Key
		want rune
	}{
		{"shift", tea.Key{Code: 'ш', ShiftedCode: 'i', BaseCode: 'i', Text: "Ш", Mod: tea.ModShift}, 'Ш'},
		{"ctrl+shift", tea.Key{Code: 'ш', ShiftedCode: 'i', BaseCode: 'i', Mod: tea.ModShift | tea.ModCtrl}, 'Ш'},
		{"no shift", tea.Key{Code: 'ш', ShiftedCode: 'i', BaseCode: 'i', Text: "ш"}, 0},
		{"real shifted key", tea.Key{Code: 'x', ShiftedCode: 'X', Text: "X", Mod: tea.ModShift}, 'X'},
	} {
		if got := shiftedCode(tc.key); got != tc.want {
			t.Errorf("%s: shifted code %q, want %q", tc.name, got, tc.want)
		}
	}
}
