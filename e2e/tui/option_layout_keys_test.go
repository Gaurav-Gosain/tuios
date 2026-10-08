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
// TUIOS_E2E_PLATFORM=darwin puts tuios on its macOS defaults (opt+N switches workspace N,
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

	// normalOptionInvExcl is Option and 1 on such a terminal: the composed ¡.
	normalOptionInvExcl = "¡"

	// kittyComposedDegree is Ghostty or kitty under the Kitty protocol with
	// Option left to compose: Option, Shift and 8 is the composed ° (176) with
	// the Alt and Shift bits set and no base-layout key. CSI 176 ; 4 u.
	kittyComposedDegree = "\x1b[176;4u"

	// kittyAltShift8Base is the same chord reported with its base-layout key,
	// 56 (8): the key that was pressed is known. CSI 176::56 ; 4 u.
	kittyAltShift8Base = "\x1b[176::56;4u"

	// optionNote is the start of the one-time note about Option not being
	// sent as Alt.
	optionNote = "Option is composing"

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
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", session}, env: []string{"TUIOS_E2E_PLATFORM=darwin"}})
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

// waitPaneOn waits for the pane to be on workspace ws. A move and its follow
// reach the daemon as two steps, so the session can show ws before the pane
// is there.
func waitPaneOn(t *testing.T, term *tuitest.Terminal, base, session string, ws int, why string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for paneWorkspace(t, base, session) != ws {
		if time.Now().After(deadline) {
			wantPaneOn(t, term, base, session, ws, why)
		}
		time.Sleep(100 * time.Millisecond)
	}
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
// keybindings.option_glyphs = "type" the character must reach the shell, and
// so must "¡" with the leader on opt+1. The left Option key sends ESC 8, and
// that must still switch.
//
// Negative control: in bindingKeys, ask for a bare character's chord as a
// plain key instead of in the Option-glyph tier. The "•" then switches to
// workspace 8, and this fails at the shell's marker.
func TestComposedOptionCharacterReachesThePane(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-opt-compose"
	term := macSession(t, base, session, "[keybindings]\nleader_key = \"opt+1\"\noption_glyphs = \"type\"\n")
	enterTerminalMode(t, term)

	// The shell prints x2•¡y only if the "•" and the "¡" reached it between
	// the two halves. The "¡" is the leader's composed character, so it also
	// shows that "type" keeps it from starting the leader.
	if err := term.SendKeys(`echo "x$((1+1))`); err != nil {
		t.Fatalf("type the command: %v", err)
	}
	press(t, term, "right Option and 8 (composed)", wezComposedBullet)
	press(t, term, "right Option and 1 (composed)", normalOptionInvExcl)
	if err := term.SendKeys(`y"`, tuitest.Enter); err != nil {
		t.Fatalf("finish the command: %v", err)
	}
	if err := term.WaitForText("x2•¡y", shellTimeout); err != nil {
		t.Fatalf("the composed characters did not reach the shell: %v\n%s", err, term.Snapshot())
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
	// The one-time note says Option is not sent as Alt. This is the positive
	// half of the test that says it stays quiet.
	if err := term.WaitForText(optionNote, uiTimeout); err != nil {
		t.Fatalf("the note about Option did not show: %v\n%s", err, term.Snapshot())
	}
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
	waitPaneOn(t, term, base, session, 3, "Option, Shift and 3")

	press(t, term, "Option, Shift and 7 (alt+&)", escPlusAmp)
	waitWorkspace(t, base, session, 7)
	waitPaneOn(t, term, base, session, 7, "Option, Shift and 7")
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
	waitPaneOn(t, term, base, session, 2, "Option and the 2 key bound to move_and_follow_2")
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
	waitPaneOn(t, term, base, session, 7, "alt+& with no layout report")
}

// TestOptionLeaderFiresOnTheComposedCharacter is the leader half of the
// default. With leader_key = "opt+1" on a terminal where Option composes,
// Option and 1 arrives as "¡", and that has to start the leader the way it
// runs an opt+ binding. The opt-out is in
// TestComposedOptionCharacterReachesThePane, where the same "¡" reaches the
// shell.
//
// Negative control: in isLeaderKey, drop the composedChords loop. The "¡" is
// then typed into the pane, and this fails at the prefix menu wait.
func TestOptionLeaderFiresOnTheComposedCharacter(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-opt-leader"
	term := macSession(t, base, session, "[keybindings]\nleader_key = \"opt+1\"\n")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((1+5))", "ready-6", shellTimeout)

	press(t, term, "Option and 1 (composed)", normalOptionInvExcl)
	if err := term.WaitForText("Toggle tiling", uiTimeout); err != nil {
		t.Fatalf("the composed character did not start the leader: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "option-leader-composed")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the prefix menu: %v", err)
	}
}

// TestKittyComposedCharacterFollowsOptionGlyphs is the Kitty protocol form of
// option_glyphs = "type". Ghostty and kitty report a composed character with
// the Alt bit set, so "type" has to cover it as well as plain text: the
// composed ° of Option, Shift and 8 must not move the pane, and the note about
// Option must stay quiet. The same chord reported with its base-layout key
// names the key that was pressed, and still moves the pane.
//
// Negative controls:
//   - In expandInto, fill the Option-glyph tier whatever option_glyphs says.
//     The first ° moves the pane to workspace 8, and this fails at the first
//     wantPaneOn.
//   - In the input handler, drop the optionGlyphsApply check before the note.
//     The note shows, and this fails at the note check.
func TestKittyComposedCharacterFollowsOptionGlyphs(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-opt-kitty-type"
	term := macSession(t, base, session, "[keybindings]\noption_glyphs = \"type\"\n")

	press(t, term, "Option, Shift and 8 (kitty, composed °, Alt bit)", kittyComposedDegree)
	press(t, term, "Option and 3 (ESC 3)", escPlus3)
	waitWorkspace(t, base, session, 3)
	wantPaneOn(t, term, base, session, 1, "the composed ° with option_glyphs = \"type\"")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the frame never settled: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), optionNote) {
		t.Fatalf("the note about Option shows with option_glyphs = \"type\":\n%s", term.Snapshot())
	}

	// The positive half: with the base-layout key, the chord is alt+shift+8.
	press(t, term, "Option and 1 (ESC 1)", escPlus1)
	waitWorkspace(t, base, session, 1)
	press(t, term, "Option, Shift and 8 (kitty, base 8)", kittyAltShift8Base)
	waitWorkspace(t, base, session, 8)
	waitPaneOn(t, term, base, session, 8, "alt+shift+8 with its base-layout key")
	saveFrame(t, term, "option-kitty-type")
}

// TestAzertyCedillaIsNotOptionC: on a French AZERTY Mac the 9 key types ç with
// no modifier. ç is also what Option and c composes on a US layout, so the
// US table read the key as opt+c. Under the Kitty protocol the report carries
// the base-layout key 9, which says the key was typed without Option, and
// the character must not run opt+c. The positive half: the same ç with no
// layout report, as Terminal.app sends it for Option and c, runs opt+c.
//
// Negative control: in composedChords, ignore the base-layout key. The first
// ç then moves the pane to workspace 4, and this fails at the first
// wantPaneOn.
func TestAzertyCedillaIsNotOptionC(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-opt-cedilla"
	term := macSession(t, base, session, "[keybindings.workspaces]\nmove_and_follow_4 = [\"opt+c\"]\n")

	// CSI 231 :: 57 u: ç, base-layout key 9, no modifier.
	press(t, term, "the AZERTY 9 key (kitty, ç, base 9)", "\x1b[231::57u")
	press(t, term, "Option and 3 (ESC 3)", escPlus3)
	waitWorkspace(t, base, session, 3)
	wantPaneOn(t, term, base, session, 1, "AZERTY ç with base-layout key 9")

	press(t, term, "Option and 1 (ESC 1)", escPlus1)
	waitWorkspace(t, base, session, 1)
	press(t, term, "Option and c (composed ç, no report)", "ç")
	waitWorkspace(t, base, session, 4)
	waitPaneOn(t, term, base, session, 4, "Option and c composed")
	saveFrame(t, term, "option-azerty-cedilla")
}
