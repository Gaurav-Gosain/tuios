package tuie2e

import (
	"encoding/json"
	"os"
	"strconv"
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
//   - ITEM-a-42 could be the typed command line. The line holds $((6*7)),
//     and only the shell makes 42.
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

	if err := term.SendKeys("printf 'a\\nb\\nc\\n' | "+tuiosBin+" xpanes -c 'echo ITEM-{}-$((6*7))'", tuitest.Enter); err != nil {
		t.Fatalf("type xpanes: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "ITEM-a-42") && strings.Contains(text, "ITEM-b-42") && strings.Contains(text, "ITEM-c-42")
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
	showWorkspace("2", "ITEM-c-42")
	time.Sleep(500 * time.Millisecond)
	if text := term.Screen().Text(); strings.Contains(text, "LEAK") {
		t.Fatalf("a line typed on workspace 1 reached the panes on workspace 2:\n%s", term.Snapshot())
	}
	alive(t, term, "after xpanes")
}

// Speedy mode, the interval and close-workspace against the real binary
// (discussion #273, after tmux-xpanes -s, -ss and --interval).
//
// How this could pass wrongly, written down first:
//   - The -ss panes could never open. They must write their marker files
//     before they close.
//   - The -s panes could close and something else show the message. The
//     panes must still be in list-windows after their commands stopped.
//   - The interval could be one wait at the start. Each start must be at
//     least 0.4 s after the one before it.
//   - close-workspace could close everything. The pane on workspace 1 stays.
func TestXpanesSpeedyIntervalAndCloseWorkspace(t *testing.T) {
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "xp"}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "setup")
	enterTerminalMode(t, term)
	onWorkspace := func(ws int) []xpanesRow {
		var out []xpanesRow
		for _, r := range xpanesRows(t, base) {
			if r.Workspace == ws {
				out = append(out, r)
			}
		}
		return out
	}
	waitPanes := func(ws, n int, what string) {
		t.Helper()
		deadline := time.Now().Add(shellTimeout)
		for len(onWorkspace(ws)) != n {
			if time.Now().After(deadline) {
				t.Fatalf("%s: workspace %d has %d panes, want %d\n%s", what, ws, len(onWorkspace(ws)), n, term.Snapshot())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// -ss: each pane runs its command and closes with it.
	ss := base + "/ss-"
	if err := term.SendKeys(tuiosBin+" xpanes --no-sync -ss -c 'touch "+ss+"{}; sleep 1' a b", tuitest.Enter); err != nil {
		t.Fatalf("type xpanes -ss: %v", err)
	}
	waitPanes(2, 2, "-ss panes open")
	waitPanes(2, 0, "-ss panes close with their commands")
	for _, it := range []string{"a", "b"} {
		if _, err := os.Stat(ss + it); err != nil {
			t.Fatalf("the -ss pane for %s never ran its command: %v", it, err)
		}
	}

	// -s with --interval: the commands start 0.5 s apart, stop, and the panes
	// stay with the message.
	stamp := base + "/start-"
	if out, err := tuiosCLI(t, base, "select-workspace", "-s", "xp", "1"); err != nil {
		t.Fatalf("back to workspace 1: %v\n%s", err, out)
	}
	if err := term.WaitForText("Opened 2 panes", uiTimeout); err != nil {
		t.Fatalf("workspace 1 is not showing: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("send alt+esc: %v", err)
	}
	time.Sleep(insertGuard + 150*time.Millisecond)
	enterTerminalMode(t, term)
	if err := term.SendKeys(tuiosBin+" xpanes --no-sync -s --interval 0.5 -c 'date +%s%N > "+stamp+"{}; echo HELD-{}' p q r", tuitest.Enter); err != nil {
		t.Fatalf("type xpanes -s: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "Press Enter to close the pane") == 3
	}, shellTimeout); err != nil {
		t.Fatalf("the -s panes do not hold: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(500 * time.Millisecond)
	if n := len(onWorkspace(2)); n != 3 {
		t.Fatalf("after their commands stopped, %d -s panes are left, want 3\n%s", n, term.Snapshot())
	}
	var starts []int64
	for _, it := range []string{"p", "q", "r"} {
		data, err := os.ReadFile(stamp + it)
		if err != nil {
			t.Fatalf("no start time for %s: %v", it, err)
		}
		ns, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err != nil {
			t.Fatalf("start time %q: %v", data, err)
		}
		starts = append(starts, ns)
	}
	for i := 1; i < len(starts); i++ {
		if gap := time.Duration(starts[i] - starts[i-1]); gap < 400*time.Millisecond {
			t.Fatalf("pane %d started %v after pane %d, want at least 0.4 s: %v", i+1, gap, i, starts)
		}
	}
	t.Logf("the held -s panes:\n%s", term.Snapshot())

	// close-workspace closes the three and leaves workspace 1.
	if out, err := tuiosCLI(t, base, "close-workspace", "-s", "xp", "2"); err != nil || !strings.Contains(out, "Closed 3 panes on workspace 2.") {
		t.Fatalf("close-workspace: %v\n%s", err, out)
	}
	waitPanes(2, 0, "close-workspace")
	if n := len(onWorkspace(1)); n != 1 {
		t.Fatalf("close-workspace 2 left %d panes on workspace 1, want 1", n)
	}
	alive(t, term, "after close-workspace")
}
