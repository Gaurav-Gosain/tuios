package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

const kittyUkrSha = "\x1b[1096::105u"

// TestLeaderAsksForLayoutKeys is the half of #202 a real terminal needs. The
// base-layout key only comes with a key the host sends as an escape code, and
// a plain letter is sent as text unless every key is asked for. The leader
// has to ask, so the key after it carries its US-layout key.
func TestLeaderAsksForLayoutKeys(t *testing.T) {
	out := &syncBuffer{}
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 120, rows: 40, out: out})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((1+3))", "ready-4", shellTimeout)

	mark := len(out.String())
	if err := term.SendKeys(tuitest.Ctrl('b')); err != nil {
		t.Fatalf("send the leader: %v", err)
	}
	waitForRequest(t, term, out, mark, kittyWindowModeFlags)
}

// TestBaseLayoutKeyOpensInbox is the report end to end: the leader, then the
// I key on a Ukrainian layout, opens the Inbox.
func TestBaseLayoutKeyOpensInbox(t *testing.T) {
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((2+3))", "ready-5", shellTimeout)

	if err := term.SendKeys(tuitest.Ctrl('b'), tuitest.Key(kittyUkrSha)); err != nil {
		t.Fatalf("send leader then ш: %v", err)
	}
	if err := term.WaitForText("The agents view needs the daemon.", uiTimeout); err != nil {
		t.Fatalf("leader then ш on the I key did not open the Inbox: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "ш") {
		t.Fatalf("ш reached the pane:\n%s", term.Snapshot())
	}
}

// TestNonLatinKeyTypesIntoPane is the other side: typed into a pane, the same
// report is the character typed, not the US-layout key. The shell prints the
// bytes it got, so the check cannot pass on the echo of the keystrokes.
func TestNonLatinKeyTypesIntoPane(t *testing.T) {
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((3+4))", "ready-7", shellTimeout)

	if err := term.SendKeys("printf %s x", tuitest.Key(kittyUkrSha), "шy | od -An -tx1 | tr -s ' '", tuitest.Enter); err != nil {
		t.Fatalf("type the command: %v", err)
	}
	if err := term.WaitForText("78 d1 88 d1 88 79", shellTimeout); err != nil {
		t.Fatalf("the pane did not get ш twice: %v\n%s", err, term.Snapshot())
	}
}
