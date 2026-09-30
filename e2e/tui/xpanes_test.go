package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// tuios xpanes against the real binary (discussion #273), run from inside a
// pane of the session as a person does: three items on stdin, one pane each,
// on a new workspace, tiled, with multifocus on.
//
// How this could pass wrongly, written down first:
//   - ITEM-a could be the typed command line. The line holds ITEM-{}, and
//     only sh -c makes ITEM-a.
//   - The panes could be on the old workspace. list-windows must put all three
//     on workspace 2, and the pane that ran xpanes alone on workspace 1.
//   - Multifocus could be a message and nothing more. One typed line must run
//     in all three panes, so its output shows three times.
//   - The layout could be BSP's own spiral. Tiled puts a and b side by side
//     on the top row and c under them, as wide as both.

type xpanesRow struct {
	ID        string `json:"window_id"`
	Name      string `json:"display_name"`
	Workspace int    `json:"workspace"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	W         int    `json:"width"`
	H         int    `json:"height"`
}

func xpanesRows(t *testing.T, base string) []xpanesRow {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", "xp")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var res struct {
		Windows []xpanesRow `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("list-windows json: %v\n%s", err, out)
	}
	return res.Windows
}

func TestXpanesOpensTiledPanesWithMultifocus(t *testing.T) {
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "xp"}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "setup")
	enterTerminalMode(t, term)

	if err := term.SendKeys("printf 'a\\nb\\nc\\n' | "+tuiosBin+" xpanes -c 'echo ITEM-{}; exec sh'", tuitest.Enter); err != nil {
		t.Fatalf("type xpanes: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "ITEM-a") && strings.Contains(text, "ITEM-b") && strings.Contains(text, "ITEM-c")
	}, shellTimeout); err != nil {
		t.Fatalf("the three panes never showed their items: %v\n%s", err, term.Snapshot())
	}

	// Three panes on workspace 2, and the arranged geometry reaches the daemon.
	var panes map[string]xpanesRow
	deadline := time.Now().Add(uiTimeout)
	for {
		panes = map[string]xpanesRow{}
		other := 0
		for _, r := range xpanesRows(t, base) {
			if r.Workspace == 2 {
				panes[r.Name] = r
			} else {
				other++
			}
		}
		a, b, c := panes["a"], panes["b"], panes["c"]
		tiled := len(panes) == 3 && other == 1 &&
			a.Y == b.Y && c.Y > a.Y && a.X < b.X && c.W > a.W+b.W/2
		if tiled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("want a and b side by side over a wide c on workspace 2, and one pane elsewhere: %+v (other %d)\n%s", panes, other, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}

	// One typed line runs in every pane. The client may still be in terminal
	// mode from typing the xpanes line, so it goes to window management first,
	// and an "i" typed into three shells cannot spoil the line.
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("send alt+esc: %v", err)
	}
	time.Sleep(insertGuard + 150*time.Millisecond)
	enterTerminalMode(t, term)
	if err := term.SendKeys("echo SYNC-$((6*7))", tuitest.Enter); err != nil {
		t.Fatalf("type into multifocus: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "SYNC-42") == 3
	}, shellTimeout); err != nil {
		t.Fatalf("the line did not run in all three panes (%d): %v\n%s",
			strings.Count(term.Screen().Text(), "SYNC-42"), err, term.Snapshot())
	}
	t.Logf("after xpanes and one typed line:\n%s", term.Snapshot())

	// Back on workspace 1, a typed line stays there. The set's panes on
	// workspace 2 are off screen, so the line must not run in them.
	showWorkspace := func(ws string, want string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "select-workspace", "-s", "xp", ws); err != nil {
			t.Fatalf("select-workspace %s: %v\n%s", ws, err, out)
		}
		if err := term.WaitForText(want, uiTimeout); err != nil {
			t.Fatalf("workspace %s never showed %q: %v\n%s", ws, want, err, term.Snapshot())
		}
	}
	showWorkspace("1", "Opened 3 panes")
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("send alt+esc: %v", err)
	}
	time.Sleep(insertGuard + 150*time.Millisecond)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo LEAK-$((5*5))", "LEAK-25", shellTimeout)
	showWorkspace("2", "ITEM-c")
	time.Sleep(500 * time.Millisecond)
	if text := term.Screen().Text(); strings.Contains(text, "LEAK") {
		t.Fatalf("a line typed on workspace 1 reached the panes on workspace 2:\n%s", term.Snapshot())
	}
	alive(t, term, "after xpanes")
}
