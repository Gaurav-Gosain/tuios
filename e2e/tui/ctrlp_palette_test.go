package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// paletteTitle is the header the command palette overlay renders. Asserting on
// it proves the palette is actually on screen, not merely that a state flag was
// flipped.
const paletteTitle = "Command Palette"

// waitPaletteOpen fails unless the command palette overlay is on screen within
// uiTimeout.
func waitPaletteOpen(t *testing.T, term *tuitest.Terminal, when string) {
	t.Helper()
	if err := term.WaitForText(paletteTitle, uiTimeout); err != nil {
		t.Fatalf("command palette did not open %s: %v\n%s", when, err, term.Snapshot())
	}
}

// waitPaletteClosed fails unless the palette overlay leaves the screen.
func waitPaletteClosed(t *testing.T, term *tuitest.Terminal, when string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), paletteTitle)
	}, uiTimeout); err != nil {
		t.Fatalf("command palette did not close %s: %v\n%s", when, err, term.Snapshot())
	}
}

// closePalette dismisses the palette with esc and waits for it to disappear.
func closePalette(t *testing.T, term *tuitest.Terminal, when string) {
	t.Helper()
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc to close palette %s: %v", when, err)
	}
	waitPaletteClosed(t, term, when)
}

// kittyCtrlP is the Kitty keyboard protocol CSI u encoding of Ctrl+P:
// ESC [ 112 ; 5 u  (112 = 'p', modifier 5 = 1 + ctrl-bit(4)). A real
// terminal that has negotiated the Kitty keyboard protocol sends this instead
// of the legacy 0x10 byte. bubbletea parses it into a KeyPressMsg regardless of
// whether tuios itself requested the enhancement, so sending the raw bytes
// reproduces exactly what the owner's terminal delivers.
const kittyCtrlP = "\x1b[112;5u"

// legacyCtrlP is the legacy control byte for Ctrl+P (0x10, DLE).
var legacyCtrlP = tuitest.Ctrl('p')

// numLockCtrlP is Ctrl+P under the Kitty keyboard protocol with Num Lock
// active: ESC [ 112 ; 133 u. The modifier field 133 = 1 + ctrl(4) + num_lock(128).
// Num Lock is the boot default on most desktop keyboards, so this is the
// real-world encoding the owner's terminal delivers. The decoder keeps the Num
// Lock bit on the event (Mod = ModCtrl|ModNumLock), which an exact Mod == ModCtrl
// check missed; this case guards the palette still opens.
const numLockCtrlP = "\x1b[112;133u"

// attachClient creates a detached daemon session with one window and attaches a
// client. This is the owner's real setup: a daemon-backed session reached with
// `tuios attach`. The returned client is settled in window-management mode with
// exactly one window focused.
func attachClient(t *testing.T) *tuitest.Terminal {
	t.Helper()
	term, _ := attachClientBase(t)
	return term
}

// attachClientBase is attachClient but also returns the isolation root, so tests
// that drive the same daemon over the CLI (e.g. `tuios tape exec`) can reach it.
func attachClientBase(t *testing.T) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-ctrlp", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-ctrlp"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	// An attached client to a session with a window boots into terminal mode;
	// normalise to window-management mode so the two mode-specific tests share a
	// known starting point.
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window Management Mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	return term, base
}

// attachTerminalModeClient attaches a client and puts it in terminal mode, the
// mode the owner types the shell in and where they report Ctrl+P failing.
func attachTerminalModeClient(t *testing.T) *tuitest.Terminal {
	t.Helper()
	term := attachClient(t)
	enterTerminalMode(t, term)
	return term
}

// TestCtrlPOpensPaletteTerminalModeLegacy is the baseline: legacy Ctrl+P in a
// daemon-attached terminal-mode session opens the palette.
func TestCtrlPOpensPaletteTerminalModeLegacy(t *testing.T) {
	term := attachTerminalModeClient(t)
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("send legacy ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "on legacy ctrl+p in terminal mode")
	closePalette(t, term, "legacy terminal mode")
	alive(t, term, "after legacy ctrl+p terminal mode")
}

// TestCtrlPOpensPaletteTerminalModeKitty reproduces the owner's failure: under
// the Kitty keyboard protocol, Ctrl+P arrives as CSI u and must still open the
// palette rather than leaking to the shell.
func TestCtrlPOpensPaletteTerminalModeKitty(t *testing.T) {
	term := attachTerminalModeClient(t)
	if err := term.SendKeys(tuitest.Key(kittyCtrlP)); err != nil {
		t.Fatalf("send kitty ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "on kitty ctrl+p in terminal mode")

	// The palette must not have leaked a history-back / ^P into the shell. The
	// pane behind the overlay must show no stray ^P text.
	if strings.Contains(term.Screen().Text(), "^P") {
		t.Fatalf("ctrl+p leaked to the shell (found ^P on screen)\n%s", term.Snapshot())
	}
	closePalette(t, term, "kitty terminal mode")
	alive(t, term, "after kitty ctrl+p terminal mode")
}

// TestCtrlPOpensPaletteTerminalModeNumLock reproduces the owner's real failure:
// under the Kitty keyboard protocol with Num Lock on (the keyboard boot default),
// Ctrl+P arrives as CSI 112;133u and decodes to Mod = ModCtrl|ModNumLock. The
// exact-equality match missed the lock bit and the palette never opened; on the
// fixed binary the lock bit is masked off and the palette opens.
func TestCtrlPOpensPaletteTerminalModeNumLock(t *testing.T) {
	term := attachTerminalModeClient(t)
	if err := term.SendKeys(tuitest.Key(numLockCtrlP)); err != nil {
		t.Fatalf("send numlock ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "on numlock ctrl+p in terminal mode")
	if strings.Contains(term.Screen().Text(), "^P") {
		t.Fatalf("ctrl+p leaked to the shell (found ^P on screen)\n%s", term.Snapshot())
	}
	closePalette(t, term, "numlock terminal mode")
	alive(t, term, "after numlock ctrl+p terminal mode")
}

// TestCtrlPOpensPaletteWindowModeNumLock is the window-management-mode counterpart.
func TestCtrlPOpensPaletteWindowModeNumLock(t *testing.T) {
	term := attachClient(t) // already in window-management mode
	if err := term.SendKeys(tuitest.Key(numLockCtrlP)); err != nil {
		t.Fatalf("send numlock ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "on numlock ctrl+p in window mode")
	closePalette(t, term, "numlock window mode")
	alive(t, term, "after numlock ctrl+p window mode")
}

// TestCtrlPOpensPaletteWindowModeKitty checks the window-management-mode path
// under the Kitty encoding.
func TestCtrlPOpensPaletteWindowModeKitty(t *testing.T) {
	term := attachClient(t) // already in window-management mode
	if err := term.SendKeys(tuitest.Key(kittyCtrlP)); err != nil {
		t.Fatalf("send kitty ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "on kitty ctrl+p in window mode")
	closePalette(t, term, "kitty window mode")
	alive(t, term, "after kitty ctrl+p window mode")
}

// TestCtrlPOpensPaletteWindowModeLegacy is the legacy baseline for window mode.
func TestCtrlPOpensPaletteWindowModeLegacy(t *testing.T) {
	term := attachClient(t) // already in window-management mode
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("send legacy ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "on legacy ctrl+p in window mode")
	closePalette(t, term, "legacy window mode")
	alive(t, term, "after legacy ctrl+p window mode")
}

// TestLeaderPStillWorks guards the other palette binding is unaffected: leader
// (Ctrl+B) then P must still cycle to the previous window, not open the palette.
// This is a regression guard for the fix, exercised in terminal mode where the
// leader chord lives.
func TestLeaderPPrevWindowStillWorks(t *testing.T) {
	term := attachTerminalModeClient(t)
	// leader+p is previous-window; with a single window it is a no-op but must
	// not open the palette.
	if err := term.SendKeys(tuitest.Ctrl('b'), "p"); err != nil {
		t.Fatalf("send leader+p: %v", err)
	}
	// Give it a beat, then assert the palette did not open.
	time.Sleep(500 * time.Millisecond)
	if strings.Contains(term.Screen().Text(), paletteTitle) {
		t.Fatalf("leader+p wrongly opened the command palette\n%s", term.Snapshot())
	}
	alive(t, term, "after leader+p")
}

// TestLeaderShiftPOpensPalette guards the configured palette binding (leader+P)
// still opens the palette, so the fix does not regress the second binding.
func TestLeaderShiftPOpensPalette(t *testing.T) {
	term := attachClient(t) // window-management mode
	if err := term.SendKeys(tuitest.Ctrl('b'), "P"); err != nil {
		t.Fatalf("send leader+P: %v", err)
	}
	waitPaletteOpen(t, term, "on leader+P")
	closePalette(t, term, "leader+P")
	alive(t, term, "after leader+P")
}

// TestCtrlPAfterTapeScript is the negative control for the real bug. After a
// tape script runs in the session and finishes, ScriptMode used to stay set
// forever, so the top-level Ctrl+P intercept kept toggling script pause/resume
// and the command palette never opened. On the fixed binary, script mode is
// left once the completion indicator's linger elapses, and Ctrl+P opens the
// palette again.
//
// FAILS on the pre-fix binary (palette never opens); PASSES after the fix.
func TestCtrlPAfterTapeScript(t *testing.T) {
	term, base := attachClientBase(t) // window-management mode

	// A trivial tape: one command that completes quickly. Running it flips the
	// client into script mode.
	tapePath := filepath.Join(t.TempDir(), "trivial.tape")
	if err := os.WriteFile(tapePath, []byte("Sleep 200ms\n"), 0o600); err != nil {
		t.Fatalf("write tape: %v", err)
	}
	if out, err := tuiosCLI(t, base, "tape", "exec", "--session", "e2e-ctrlp", tapePath); err != nil {
		t.Fatalf("tape exec: %v: %s", err, out)
	}

	// The tape has finished (tape exec blocks until the daemon reports done).
	// Wait past the completion-indicator linger so a fixed binary has left
	// script mode; a pre-fix binary is still stuck in it.
	time.Sleep(scriptDoneLingerE2E + time.Second)

	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("send ctrl+p after tape: %v", err)
	}
	waitPaletteOpen(t, term, "on ctrl+p after a finished tape script")
	closePalette(t, term, "after tape script")
	alive(t, term, "after ctrl+p following a tape script")
}

// scriptDoneLingerE2E mirrors app.scriptDoneLinger (2s), the window the "DONE"
// indicator is shown before script mode is left. Kept as a local literal so the
// e2e module needs no dependency on internal packages.
const scriptDoneLingerE2E = 2 * time.Second
