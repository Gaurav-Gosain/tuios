package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func currentWorkspace(t *testing.T, base, sess string) int {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-workspaces", "--json", "--session", sess)
	if err != nil {
		t.Fatalf("list-workspaces: %v\n%s", err, out)
	}
	var res struct {
		Current int `json:"current_workspace"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("list-workspaces json: %v\n%s", err, out)
	}
	return res.Current
}

func panesOn(t *testing.T, base, sess string, ws int) int {
	t.Helper()
	n := 0
	for _, r := range xpanesRowsIn(t, base, sess) {
		if r.Workspace == ws {
			n++
		}
	}
	return n
}

func waitPanesOn(t *testing.T, term *tuitest.Terminal, base, sess string, ws, n int, what string) {
	t.Helper()
	deadline := time.Now().Add(shellTimeout)
	for panesOn(t, base, sess, ws) != n {
		if time.Now().After(deadline) {
			t.Fatalf("%s: workspace %d has %d panes, want %d\n%s", what, ws, panesOn(t, base, sess, ws), n, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitShowing(t *testing.T, base, sess string, ws int, text string, clients ...*tuitest.Terminal) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for currentWorkspace(t, base, sess) != ws {
		if time.Now().After(deadline) {
			t.Fatalf("the session shows workspace %d, want %d\n%s", currentWorkspace(t, base, sess), ws, clients[0].Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	for i, c := range clients {
		if err := c.WaitForText(text, uiTimeout); err != nil {
			t.Fatalf("client %d does not show %q from workspace %d: %v\n%s", i+1, text, ws, err, c.Snapshot())
		}
	}
}

func waitXpanesDone(t *testing.T, term *tuitest.Terminal, done string) {
	t.Helper()
	deadline := time.Now().Add(shellTimeout)
	for {
		if _, err := os.Stat(done); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("xpanes never finished\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func startHome(t *testing.T, base, sess string) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", sess}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "setup")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo HOME-$((6*7))", "HOME-42", shellTimeout)
	return term
}

// Test 1: -ss closes every pane with its command, and the client is back on
// workspace 1. The commands are true, so a pane may close before the next one
// opens: the run ends when xpanes writes its done file.
func TestEmptyWorkspaceReturnsAfterXpanesSuperSpeedy(t *testing.T) {
	base := t.TempDir()
	term := startHome(t, base, "ew")

	done := filepath.Join(base, "xpanes-done")
	if err := term.SendKeys(tuiosBin+" xpanes -ss -c 'true' a b c; touch "+done, tuitest.Enter); err != nil {
		t.Fatalf("type xpanes: %v", err)
	}
	waitXpanesDone(t, term, done)
	waitPanesOn(t, term, base, "ew", 2, 0, "the -ss panes close")
	waitShowing(t, base, "ew", 1, "HOME-42", term)
	t.Logf("after the -ss panes closed:\n%s", term.Snapshot())
	alive(t, term, "after the return")
}

// Test 2: -s holds each pane. Enter goes to all three through multifocus, the
// shells exit, and both attached clients are back on workspace 1.
func TestEmptyWorkspaceReturnsAfterXpanesSpeedy(t *testing.T) {
	base := t.TempDir()
	term := startHome(t, base, "ew")
	second := attachIn(t, base, "ew", startOpts{cols: 120, rows: 40})

	done := filepath.Join(base, "xpanes-done")
	if err := term.SendKeys(tuiosBin+" xpanes -s -c 'echo HELD-{}' a b c; touch "+done, tuitest.Enter); err != nil {
		t.Fatalf("type xpanes: %v", err)
	}
	for i, c := range []*tuitest.Terminal{term, second} {
		if err := c.WaitFor(func(s tuitest.Screen) bool {
			return strings.Count(s.Text(), "Press Enter to close the pane") == 3 && !strings.Contains(s.Text(), "HOME-42")
		}, shellTimeout); err != nil {
			t.Fatalf("client %d does not show the three held panes: %v\n%s", i+1, err, c.Snapshot())
		}
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return multifocusCorners(s) >= 2 }, uiTimeout); err != nil {
		t.Fatalf("multifocus never showed on the held panes: %v\n%s", err, term.Snapshot())
	}

	waitXpanesDone(t, term, done)

	leaveTerminalMode(t, term)
	enterTerminalMode(t, term)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("press Enter: %v", err)
	}
	waitPanesOn(t, term, base, "ew", 2, 0, "Enter closes the held panes")
	waitShowing(t, base, "ew", 1, "HOME-42", term, second)
	t.Logf("after Enter, the first client:\n%s", term.Snapshot())
	t.Logf("after Enter, the second client:\n%s", second.Snapshot())
	alive(t, term, "after the return")
}

// Test 3: with workspaces.return_when_empty = false the session stays on the
// empty workspace, as before.
func TestEmptyWorkspaceStaysWhenTheSettingIsOff(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[workspaces]\nreturn_when_empty = false\n")
	term := startHome(t, base, "ew")

	if err := term.SendKeys(tuiosBin+" xpanes -ss -c 'sleep 2' a b c", tuitest.Enter); err != nil {
		t.Fatalf("type xpanes: %v", err)
	}
	waitPanesOn(t, term, base, "ew", 2, 3, "the -ss panes open")
	waitPanesOn(t, term, base, "ew", 2, 0, "the -ss panes close")
	time.Sleep(time.Second)
	if ws := currentWorkspace(t, base, "ew"); ws != 2 {
		t.Fatalf("the session went to workspace %d with return_when_empty off, want 2\n%s", ws, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "HOME-42") {
		t.Fatalf("the client draws workspace 1 with return_when_empty off:\n%s", term.Snapshot())
	}
	alive(t, term, "with the setting off")
}

// Test 4: workspace 1, then 2, then 3. The pane on 3 exits, and the session
// shows 2. close-workspace empties 2, and the session shows 1.
func TestEmptyWorkspaceWalksBackAChain(t *testing.T) {
	base := t.TempDir()
	term := startHome(t, base, "ew")

	openOn := func(ws int, line, want string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "select-workspace", "--session", "ew", strconv.Itoa(ws)); err != nil {
			t.Fatalf("select-workspace %d: %v\n%s", ws, err, out)
		}
		if out, err := tuiosCLI(t, base, "new-window", "--session", "ew"); err != nil {
			t.Fatalf("new-window on %d: %v\n%s", ws, err, out)
		}
		waitPanesOn(t, term, base, "ew", ws, 1, "the pane on the new workspace")
		leaveTerminalMode(t, term)
		enterTerminalMode(t, term)
		runInShell(t, term, line, want, shellTimeout)
	}
	openOn(2, "echo TWO-$((6*7))", "TWO-42")
	openOn(3, "echo THREE-$((6*7))", "THREE-42")
	if ws := currentWorkspace(t, base, "ew"); ws != 3 {
		t.Fatalf("the session shows workspace %d, want 3", ws)
	}

	if err := term.SendKeys("exit", tuitest.Enter); err != nil {
		t.Fatalf("type exit: %v", err)
	}
	waitPanesOn(t, term, base, "ew", 3, 0, "the shell on workspace 3 exits")
	waitShowing(t, base, "ew", 2, "TWO-42", term)

	if out, err := tuiosCLI(t, base, "close-workspace", "--session", "ew", "2"); err != nil {
		t.Fatalf("close-workspace 2: %v\n%s", err, out)
	}
	waitPanesOn(t, term, base, "ew", 2, 0, "close-workspace 2")
	waitShowing(t, base, "ew", 1, "HOME-42", term)
	t.Logf("after the chain:\n%s", term.Snapshot())
	alive(t, term, "after the chain")
}

// Test 5: tuios xpanes from outside the session remembers the workspace that
// was showing. The person looks at workspace 3 and comes back to the xpanes
// workspace. When it empties, the session shows workspace 1, where xpanes ran,
// and not workspace 3, which was shown last.
func TestEmptyWorkspaceReturnsWhereXpanesRan(t *testing.T) {
	base := t.TempDir()
	term := startHome(t, base, "ew")

	if out, err := tuiosCLI(t, base, "new-window", "--session", "ew", "--workspace", "3", "--no-focus"); err != nil {
		t.Fatalf("new-window on 3: %v\n%s", err, out)
	}
	waitPanesOn(t, term, base, "ew", 3, 1, "the pane on workspace 3")
	if out, err := tuiosCLI(t, base, "xpanes", "--session", "ew", "--no-sync", "a", "b"); err != nil {
		t.Fatalf("xpanes: %v\n%s", err, out)
	}
	waitPanesOn(t, term, base, "ew", 2, 2, "the xpanes panes")
	for _, ws := range []string{"3", "2"} {
		if out, err := tuiosCLI(t, base, "select-workspace", "--session", "ew", ws); err != nil {
			t.Fatalf("select-workspace %s: %v\n%s", ws, err, out)
		}
	}
	if ws := currentWorkspace(t, base, "ew"); ws != 2 {
		t.Fatalf("the session shows workspace %d, want 2", ws)
	}
	if out, err := tuiosCLI(t, base, "close-workspace", "--session", "ew", "2"); err != nil {
		t.Fatalf("close-workspace 2: %v\n%s", err, out)
	}
	waitPanesOn(t, term, base, "ew", 3, 0, "close-workspace 2")
	waitPanesOn(t, term, base, "ew", 2, 1, "the pane from workspace 3 after close-workspace 2")
	waitShowing(t, base, "ew", 1, "HOME-42", term)
	alive(t, term, "after the return")
}

func multifocusCorners(s tuitest.Screen) int {
	w, h := s.Size()
	n := 0
	for y := range h {
		for x := range w {
			if c := s.Cell(x, y); c.Content == "╭" && c.Fg.Kind == tuitest.ColorIndexed && c.Fg.Index == 3 {
				n++
			}
		}
	}
	return n
}
