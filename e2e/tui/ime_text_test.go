package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestInputMethodTextReachesKittyPane is issue #255 end to end. The pane runs
// a reader that pushes the kitty keyboard flags Claude Code pushes (CSI >5u,
// disambiguate and alternate keys), reads six bytes raw and prints them in
// hex. The host then commits "，" twice, the way an input method does: once
// as UTF-8 text, and once as the kitty text-only event CSI 0 ; ; 65292 u that
// a terminal sends in report-all-keys mode. Both must reach the pane as the
// UTF-8 text ef bc 8c.
//
// Negative controls: v0.8.0 sent the first as CSI 65292u (1b 5b 36 ...), and
// without fixKittyTextOnlyKey the second arrives as CSI 32;5u.
func TestInputMethodTextReachesKittyPane(t *testing.T) {
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((5+6))", "ready-11", shellTimeout)

	reader := `sh -c 'printf "\033[>5u"; stty raw -echo; echo RE""AD; b=$(dd bs=1 count=6 2>/dev/null | od -An -tx1 | tr -s " \n" " "); stty sane; printf "\033[<u"; echo G""OT:$b:E""ND'`
	runInShell(t, term, reader, "READ", shellTimeout)

	if err := term.SendKeys(tuitest.Key("，"), tuitest.Key("\x1b[0;;65292u")); err != nil {
		t.Fatalf("send the input method text: %v", err)
	}
	if err := term.WaitForText("END", shellTimeout); err != nil {
		t.Fatalf("the reader did not get six bytes: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "GOT: ef bc 8c ef bc 8c :END") {
		t.Fatalf("the pane did not get the full-width comma as text twice:\n%s", term.Snapshot())
	}
}
