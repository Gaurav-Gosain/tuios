package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// altF12 is Alt+F12 as a terminal sends it: F12 with xterm modifier 3 (Alt).
// It is what Ghostty sends for Option+F12 with macos-option-as-alt on.
const altF12 = "\x1b[24;3~"

// TestLeaderSpelledWithOptAliasStartsThePrefix is issue #201. On macOS a
// leader written as opt+f12 was accepted but never fired, because the leader
// was compared with the literal spelling and the key arrives as alt+f12.
//
// OSTYPE=darwin makes tuios take its macOS path on this machine, which is the
// only platform where opt+ is a valid spelling.
//
// Negative control: compare the leader with strings.EqualFold again in
// isLeaderKey and this fails at the prefix menu wait, because Alt+F12 goes to
// the pane.
func TestLeaderSpelledWithOptAliasStartsThePrefix(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[keybindings]\nleader_key = \"opt+f12\"\n")
	killDaemon(t, base)

	term := startIn(t, base, startOpts{
		args: []string{"new", "e2e-leader-alias"},
		env:  []string{"OSTYPE=darwin"},
	})
	if err := term.WaitForText(welcomeHint, bootTimeout); err != nil {
		t.Fatalf("tuios never booted with leader_key = opt+f12: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the first frame never settled: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(altF12); err != nil {
		t.Fatalf("send Alt+F12: %v", err)
	}
	if err := term.WaitForText("Toggle tiling", uiTimeout); err != nil {
		t.Fatalf("Alt+F12 did not start the prefix chord with leader_key = opt+f12: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "leader-opt-f12-prefix-menu")

	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the prefix menu: %v", err)
	}
}

// TestLeaderTwiceSendsTheLeaderToThePane is issue #213. Pressing the leader
// twice in terminal mode must send the leader itself to the pane. It sent
// Ctrl+B (0x02) whatever the leader was.
//
// The pane runs a one-byte reader that prints the byte it gets in hex. The
// markers are split with "" so the typed command cannot satisfy the waits.
//
// Negative control: restore the hardcoded 0x02 in HandleTerminalModeKey and
// this fails with GOT:02 on screen.
func TestLeaderTwiceSendsTheLeaderToThePane(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[keybindings]\nleader_key = \"ctrl+a\"\n")

	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((4+5))", "ready-9", shellTimeout)

	reader := `sh -c 'stty raw -echo; echo RE""AD; b=$(dd bs=1 count=1 2>/dev/null | od -An -tx1 | tr -d " \n"); stty sane; echo G""OT:$b'`
	runInShell(t, term, reader, "READ", shellTimeout)

	if err := term.SendKeys(tuitest.Ctrl('a')); err != nil {
		t.Fatalf("send the first Ctrl+A: %v", err)
	}
	if err := term.WaitForText("Toggle tiling", uiTimeout); err != nil {
		t.Fatalf("Ctrl+A did not start the prefix chord with leader_key = ctrl+a: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('a')); err != nil {
		t.Fatalf("send the second Ctrl+A: %v", err)
	}
	if err := term.WaitForText("GOT:", shellTimeout); err != nil {
		t.Fatalf("the reader got no byte after the leader twice: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "GOT:01") {
		t.Fatalf("the pane did not get Ctrl+A (01) after the leader twice:\n%s", term.Snapshot())
	}
}
