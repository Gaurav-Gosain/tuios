package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCtrlClickWindowLinkCrossesSessions clicks a tuios://window link that
// names a pane of another session. The client switches to that session and
// focuses the pane, as switch-session and focus-window would.
//
// Negative control: with openWindowLink searching only the attached session's
// windows, the click raises "names no window on this machine", the client
// stays on linkhome, and waitFocused fails.
func TestCtrlClickWindowLinkCrossesSessions(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, s := range []string{"linkhome", "linkaway"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	out, err := tuiosCLI(t, base, "new-window", "-s", "linkaway", "away-pane", "--print-id")
	if err != nil {
		t.Fatalf("create the target pane: %v\n%s", err, out)
	}
	target := strings.TrimSpace(out)
	if out, err := tuiosCLI(t, base, "new-window", "-s", "linkaway", "other-pane"); err != nil {
		t.Fatalf("create the other pane: %v\n%s", err, out)
	}
	if _, focused := focusedLayout(t, base, "linkaway"); focused == target {
		t.Fatalf("setup: the target pane %s is focused before the click", target)
	}

	term := attachIn(t, base, "linkhome", startOpts{})
	enterTerminalMode(t, term)
	runInShell(t, term, fmt.Sprintf("printf 'agent \\033]8;;tuios://window/%s\\033\\\\away\\033]8;;\\033\\\\\\n'; echo LIN\"\"KSET", victimShortID(t, target)), "LINKSET", uiTimeout)
	leaveTerminalMode(t, term)

	col, row := mustFind(t, term, "agent away")
	mouseClick(t, term, col+7, row, tuitest.MouseLeft, tuitest.ModCtrl)

	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if _, focused := focusedLayout(t, base, "linkaway"); focused == target && strings.Contains(term.Snapshot(), "away-pane") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_, focused := focusedLayout(t, base, "linkaway")
	t.Fatalf("the link did not switch to linkaway and focus %s (focused %s):\n%s", target, focused, term.Snapshot())
}
