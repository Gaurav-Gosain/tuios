package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Issues #566 and #575: Option chords on macOS and the US layout assumption.
//
// Each sequence below is byte for byte what a terminal writes for the key, so
// the tests run the same decoder and the same key path as the reports.
// OSTYPE=darwin puts tuios on its macOS defaults (opt+N switches workspace N,
// opt+shift+N moves the pane there) and turns on its macOS key paths on the
// Linux machine that runs the suite.
//
// How these could pass wrongly, written down first:
//   - The key could do nothing for an unrelated reason. Every test that says
//     a key does nothing also presses a key that does something, in the same
//     session, and waits for it.
//   - A wrong action could leave no trace. The wrong action in #575 is
//     move_and_follow_7, which moves the pane, so each test reads the pane's
//     workspace from the daemon as well as the session's.
//   - Typed text could match the echo of the command. The marker is computed
//     by the shell.
const (
	// wezComposedBullet is WezTerm with send_composed_key_when_right_alt_is_pressed:
	// right Option and 8 on a US layout sends the composed character as text,
	// with no ESC and no modifier.
	wezComposedBullet = "•"

	// normalOptionPound is Terminal.app, or iTerm2 with Option on "Normal", as
	// they ship: Option and 3 on a US layout sends the composed £ as text.
	normalOptionPound = "£"

	// escPlus8, escPlus3, escPlus1 and escPlusHash are the left Option key as
	// Meta (WezTerm) or Esc+ (iTerm2): ESC, then the character the key types
	// without Option. Option, Shift and 3 on a US layout types #.
	escPlus8    = "\x1b8"
	escPlus3    = "\x1b3"
	escPlus1    = "\x1b1"
	escPlusHash = "\x1b#"

	// escPlusAmp and escPlusEacute are iTerm2 Esc+ on a French AZERTY Mac:
	// Option and the 1 key, and Option and the 2 key. The terminal says
	// nothing about the layout.
	escPlusAmp    = "\x1b&"
	escPlusEacute = "\x1bé"

	// kittyAzertyAltAmp is Option and the 1 key on AZERTY under the Kitty
	// protocol with alternate keys: key code 38 (&), base-layout key 49 (1),
	// Alt. CSI 38::49 ; 3 u.
	kittyAzertyAltAmp = "\x1b[38::49;3u"

	// kittyAzertyAltShiftAmp is Option, Shift and the 1 key on AZERTY: key
	// code 38 (&), shifted key 49 (1), base-layout key 49, Alt and Shift.
	kittyAzertyAltShiftAmp = "\x1b[38:49:49;4u"

	// kittyAltAmp is alt+& from a terminal on the Kitty protocol that reports
	// no alternate keys. Nothing in it says which layout typed it.
	kittyAltAmp = "\x1b[38;3u"
)

// macSession starts a daemon session with tuios on its macOS defaults and one
// pane on workspace 1, in window mode. config is written first when not empty.
func macSession(t *testing.T, base, session, config string) *tuitest.Terminal {
	t.Helper()
	if config != "" {
		writeConfig(t, base, config)
	}
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", session}, env: []string{"OSTYPE=darwin"}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "one pane")
	waitWorkspace(t, base, session, 1)
	return term
}

// press sends one raw key sequence.
func press(t *testing.T, term *tuitest.Terminal, what, seq string) {
	t.Helper()
	if err := term.SendKeys(tuitest.Key(seq)); err != nil {
		t.Fatalf("send %s: %v", what, err)
	}
}

// paneWorkspace is the workspace the session's only pane is on.
func paneWorkspace(t *testing.T, base, session string) int {
	t.Helper()
	rows := xpanesRowsIn(t, base, session)
	if len(rows) != 1 {
		t.Fatalf("the session has %d panes, want 1", len(rows))
	}
	return rows[0].Workspace
}

// wantPaneOn fails unless the pane is on workspace ws.
func wantPaneOn(t *testing.T, term *tuitest.Terminal, base, session string, ws int, why string) {
	t.Helper()
	if got := paneWorkspace(t, base, session); got != ws {
		t.Fatalf("%s: the pane is on workspace %d, want %d\n%s", why, got, ws, term.Snapshot())
	}
}

// TestComposedOptionCharacterReachesThePane is issue #566. WezTerm composes with
// the right Option key, so right Option and 8 sends "•" as text. tuios read
// that character as opt+8 and switched to workspace 8. With
// keybindings.option_glyphs = "type" the character must reach the shell. The
// left Option key sends ESC 8, and that must still switch.
//
// Negative control: in bindingKeys, ask for a bare character's chord as a
// plain key instead of in the Option-glyph tier. The "•" then switches to
// workspace 8, and this fails at the shell's marker.
func TestComposedOptionCharacterReachesThePane(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-opt-compose"
	term := macSession(t, base, session, "[keybindings]\noption_glyphs = \"type\"\n")
	enterTerminalMode(t, term)

	// The shell prints x2•y only if the "•" reached it between the two halves.
	if err := term.SendKeys(`echo "x$((1+1))`); err != nil {
		t.Fatalf("type the command: %v", err)
	}
	press(t, term, "right Option and 8 (composed)", wezComposedBullet)
	if err := term.SendKeys(`y"`, tuitest.Enter); err != nil {
		t.Fatalf("finish the command: %v", err)
	}
	if err := term.WaitForText("x2•y", shellTimeout); err != nil {
		t.Fatalf("the composed character did not reach the shell: %v\n%s", err, term.Snapshot())
	}
	if ws := currentWorkspace(t, base, session); ws != 1 {
		t.Fatalf("the composed character switched to workspace %d\n%s", ws, term.Snapshot())
	}
	saveFrame(t, term, "option-composed-bullet-typed")

	// The positive half: the left Option key, sent as Alt, still switches.
	press(t, term, "left Option and 8 (ESC 8)", escPlus8)
	waitWorkspace(t, base, session, 8)
}

// TestNormalOptionCharacterSwitchesWorkspaceByDefault is the shipped default.
// Terminal.app and iTerm2 ship with Option on "Normal", so Option and 3 sends
// the composed "£" and nothing else. With no config, that is opt+3 and
// switches to workspace 3, as it did before #566. The character must not
// reach the shell.
//
// Negative control: in expandInto, skip the OptionGlyphKey claim. The "£" is
// then typed into the shell, and this fails at the workspace wait.
func TestNormalOptionCharacterSwitchesWorkspaceByDefault(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-opt-normal"
	term := macSession(t, base, session, "")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((2+3))", "ready-5", shellTimeout)

	press(t, term, "Option and 3 (composed)", normalOptionPound)
	waitWorkspace(t, base, session, 3)
	saveFrame(t, term, "option-normal-pound-bound")

	// Back on workspace 1 the shell's prompt line holds no "£". A "£" the
	// shell got is echoed there, since the prompt waits for the rest of a line.
	press(t, term, "Option and 1 (ESC 1)", escPlus1)
	waitWorkspace(t, base, session, 1)
	if err := term.WaitForText("ready-5", uiTimeout); err != nil {
		t.Fatalf("workspace 1 is not drawn again: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the frame never settled: %v\n%s", err, term.Snapshot())
	}
	if text := term.Screen().Text(); strings.Contains(text, "£") {
		t.Fatalf("the composed character was typed into the pane:\n%s", term.Snapshot())
	}
}

// TestEscPlusOptionChordsOnAUSLayout is iTerm2 with Esc+ on a US layout. ESC 3
// switches to workspace 3. ESC # is Option, Shift and 3, and moves the pane
// to workspace 3. ESC & is Option, Shift and 7 on a US layout, so with no word
// from the terminal about the layout it moves the pane to workspace 7. That
// last step is the positive half of the AZERTY tests below: the US alias is
// still there when nothing better is known.
//
// Negative control: in bindingKeys, never ask for the US-layout tier. ESC #
// then does nothing, and this fails at the workspace wait after it.
func TestEscPlusOptionChordsOnAUSLayout(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-opt-us"
	term := macSession(t, base, session, "")

	press(t, term, "Option and 3", escPlus3)
	waitWorkspace(t, base, session, 3)
	press(t, term, "Option and 1", escPlus1)
	waitWorkspace(t, base, session, 1)

	press(t, term, "Option, Shift and 3", escPlusHash)
	waitWorkspace(t, base, session, 3)
	wantPaneOn(t, term, base, session, 3, "Option, Shift and 3")

	press(t, term, "Option, Shift and 7 (alt+&)", escPlusAmp)
	waitWorkspace(t, base, session, 7)
	wantPaneOn(t, term, base, session, 7, "Option, Shift and 7")
	saveFrame(t, term, "option-escplus-us")
}

// TestAzertyEscPlusWithLayoutOther is issue #575 with iTerm2 Esc+, which says
// nothing about the layout. With keybindings.keyboard_layout = "other", Option
// and the 1 key (alt+&) no longer runs move_and_follow_7, and the recipe's
// move_and_follow_2 = ["opt+é"] moves the pane with Option and the 2 key.
//
// Negative control: in expandInto, drop the keyboard_layout check. ESC & then
// moves the pane to workspace 7, and this fails at wantPaneOn after the
// workspace 3 wait.
func TestAzertyEscPlusWithLayoutOther(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-opt-azerty-esc"
	term := macSession(t, base, session, `[keybindings]
keyboard_layout = "other"

[keybindings.workspaces]
move_and_follow_2 = ["opt+é"]
`)

	press(t, term, "Option and the 1 key (alt+&)", escPlusAmp)
	// A key that does something, so the one before it has been read.
	press(t, term, "Option, Shift and the 3 key (alt+3)", escPlus3)
	waitWorkspace(t, base, session, 3)
	wantPaneOn(t, term, base, session, 1, "Option and the 1 key with keyboard_layout = \"other\"")

	// Back to the pane, which the move takes with it.
	press(t, term, "Option, Shift and the 1 key (alt+1)", escPlus1)
	waitWorkspace(t, base, session, 1)
	press(t, term, "Option and the 2 key (alt+é)", escPlusEacute)
	waitWorkspace(t, base, session, 2)
	wantPaneOn(t, term, base, session, 2, "Option and the 2 key bound to move_and_follow_2")
	saveFrame(t, term, "option-escplus-azerty")
}

// TestOwnBindingBeatsTheUSAlias is the other half of issue #575. The reporter
// bound switch_workspace_1 to opt+&, and the key still ran move_and_follow_7,
// because the alt+& alias of opt+shift+7 belonged to the action first in name
// order. A binding written for the key now wins over an alias of another one,
// on the default layout setting too.
//
// Negative control: in expandInto, claim a US alias as a plain key, not in the
// US-layout tier. ESC & then moves the pane to workspace 7, and this fails at
// the workspace wait.
func TestOwnBindingBeatsTheUSAlias(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-opt-own"
	term := macSession(t, base, session, "[keybindings.workspaces]\nswitch_workspace_2 = [\"opt+&\"]\n")

	press(t, term, "Option and the 1 key (alt+&)", escPlusAmp)
	waitWorkspace(t, base, session, 2)
	wantPaneOn(t, term, base, session, 1, "opt+& bound to switch_workspace_2")
}

// TestAzertyKittyReportsPickTheRightWorkspace is issue #575 on a terminal that
// reports the layout through the Kitty protocol, with the shipped config.
//
//   - Option and the 1 key is alt+& with base-layout key 1. The base key says
//     the layout is not US, so the alias to opt+shift+7 does not apply and
//     nothing runs.
//   - Option, Shift and the 1 key types 1, which the protocol reports as the
//     shifted key. That is opt+1, and it switches to workspace 1.
//   - The positive half: alt+& with no alternate keys is still read through
//     the US alias, and moves the pane to workspace 7.
//
// Negative controls:
//   - In bindingKeys, ask for the US-layout tier without KeyFitsUSLayout. The
//     first key moves the pane to 7, and this fails at the first wantPaneOn.
//   - In bindingKeys, drop the shiftedKey spelling. Option, Shift and the 1
//     key then does nothing, and this fails at the workspace 1 wait.
func TestAzertyKittyReportsPickTheRightWorkspace(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-opt-azerty-kitty"
	term := macSession(t, base, session, "")

	press(t, term, "Option and the 1 key (kitty, base 1)", kittyAzertyAltAmp)
	press(t, term, "Option, Shift and 3 (alt+3)", escPlus3)
	waitWorkspace(t, base, session, 3)
	wantPaneOn(t, term, base, session, 1, "Option and the 1 key on AZERTY")

	press(t, term, "Option, Shift and the 1 key (kitty, shifted 1)", kittyAzertyAltShiftAmp)
	waitWorkspace(t, base, session, 1)
	wantPaneOn(t, term, base, session, 1, "Option, Shift and the 1 key on AZERTY")
	saveFrame(t, term, "option-kitty-azerty")

	time.Sleep(insertGuard)
	press(t, term, "alt+& with no layout report", kittyAltAmp)
	waitWorkspace(t, base, session, 7)
	wantPaneOn(t, term, base, session, 7, "alt+& with no layout report")
}
