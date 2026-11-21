package tuie2e

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const paletteTitle = "Command Palette"

func waitPaletteOpen(t *testing.T, term *tuitest.Terminal, when string) {
	t.Helper()
	if err := term.WaitForText(paletteTitle, uiTimeout); err != nil {
		t.Fatalf("command palette did not open %s: %v\n%s", when, err, term.Snapshot())
	}
}

func waitPaletteClosed(t *testing.T, term *tuitest.Terminal, when string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), paletteTitle)
	}, uiTimeout); err != nil {
		t.Fatalf("command palette did not close %s: %v\n%s", when, err, term.Snapshot())
	}
}

func closePalette(t *testing.T, term *tuitest.Terminal, when string) {
	t.Helper()
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc to close palette %s: %v", when, err)
	}
	waitPaletteClosed(t, term, when)
}

const kittyCtrlP = "\x1b[112;5u"

var legacyCtrlP = tuitest.Ctrl('p')

const numLockCtrlP = "\x1b[112;133u"

func attachClient(t *testing.T) *tuitest.Terminal {
	t.Helper()
	term, _ := attachClientBase(t)
	return term
}

func attachClientBase(t *testing.T) (*tuitest.Terminal, string) {
	t.Helper()
	return attachClientBaseOut(t, nil)
}

func attachClientBaseOut(t *testing.T, out io.Writer) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	return attachClientIn(t, base, out), base
}

func attachClientIn(t *testing.T, base string, out io.Writer) *tuitest.Terminal {
	t.Helper()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-ctrlp", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-ctrlp"}, out: out})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	return term
}

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
	term := attachClient(t)
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
	term := attachClient(t)
	if err := term.SendKeys(tuitest.Key(kittyCtrlP)); err != nil {
		t.Fatalf("send kitty ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "on kitty ctrl+p in window mode")
	closePalette(t, term, "kitty window mode")
	alive(t, term, "after kitty ctrl+p window mode")
}

// TestCtrlPOpensPaletteWindowModeLegacy is the legacy baseline for window mode.
func TestCtrlPOpensPaletteWindowModeLegacy(t *testing.T) {
	term := attachClient(t)
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("send legacy ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "on legacy ctrl+p in window mode")
	closePalette(t, term, "legacy window mode")
	alive(t, term, "after legacy ctrl+p window mode")
}

// TestLeaderPPrevWindowStillWorks guards the other palette binding is unaffected: leader
// (Ctrl+B) then P must still cycle to the previous window, not open the palette.
// This is a regression guard for the fix, exercised in terminal mode where the
// leader chord lives.
func TestLeaderPPrevWindowStillWorks(t *testing.T) {
	term := attachTerminalModeClient(t)
	if err := term.SendKeys(tuitest.Ctrl('b'), "p"); err != nil {
		t.Fatalf("send leader+p: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if strings.Contains(term.Screen().Text(), paletteTitle) {
		t.Fatalf("leader+p wrongly opened the command palette\n%s", term.Snapshot())
	}
	alive(t, term, "after leader+p")
}

// TestLeaderShiftPOpensPalette guards the configured palette binding (leader+P)
// still opens the palette, so the fix does not regress the second binding.
func TestLeaderShiftPOpensPalette(t *testing.T) {
	term := attachClient(t)
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
	term, base := attachClientBase(t)

	tapePath := filepath.Join(t.TempDir(), "trivial.tape")
	if err := os.WriteFile(tapePath, []byte("Sleep 200ms\n"), 0o600); err != nil {
		t.Fatalf("write tape: %v", err)
	}
	if out, err := tuiosCLI(t, base, "tape", "exec", "--session", "e2e-ctrlp", tapePath); err != nil {
		t.Fatalf("tape exec: %v: %s", err, out)
	}

	time.Sleep(scriptDoneLingerE2E + time.Second)

	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("send ctrl+p after tape: %v", err)
	}
	waitPaletteOpen(t, term, "on ctrl+p after a finished tape script")
	closePalette(t, term, "after tape script")
	alive(t, term, "after ctrl+p following a tape script")
}

const scriptDoneLingerE2E = 2 * time.Second
