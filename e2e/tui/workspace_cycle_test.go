package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// next_workspace and prev_workspace (#612) go to the next or the previous
// workspace that has panes, and wrap.
//
// How these could pass wrongly, written down first:
//   - A cycle that visits every workspace would also reach 3 from 2 on a
//     second press. The test presses once from 1 with panes on 1 and 3 only,
//     so a cycle that does not skip lands on 2.
//   - A wrap could look right because the key did nothing. The test goes
//     forward to 3 first, then forward again to 1, then back to 3.
//   - With workspaces.new_window_when_empty on, a cycle that lands on empty
//     workspaces opens a pane on each. The second test presses the key three
//     times with one pane in the session, waits for every pane request to
//     finish, and counts the panes.
//
// The workspace after each press, and the final list-windows output, are
// saved under artifactDir.

const cycleKeys = "[keybindings.workspaces]\nnext_workspace = [\"alt+y\"]\nprev_workspace = [\"alt+u\"]\n"

// waitClientWorkspace waits for the client that took the last input to show
// workspace want, and returns what it shows.
func waitClientWorkspace(t *testing.T, base string, want int) int {
	t.Helper()
	got := 0
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if got = clientWorkspace(t, base); got == want {
			return got
		}
		time.Sleep(100 * time.Millisecond)
	}
	return got
}

func TestWorkspaceCycleSkipsEmptyWorkspaces(t *testing.T) {
	const sess = "cyc"
	base := t.TempDir()
	writeConfig(t, base, cycleKeys)
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", sess}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "setup")
	toWindowMode(t, term)
	sendKeys(t, term, tuitest.Alt("3"))
	waitShowing(t, base, sess, 3, "", term)
	newWindow(t, term)
	waitPanesOn(t, term, base, sess, 3, 1, "the pane on workspace 3")
	toWindowMode(t, term)
	sendKeys(t, term, tuitest.Alt("1"))
	waitShowing(t, base, sess, 1, "", term)

	var log string
	for _, step := range []struct {
		key  string
		want int
	}{{"y", 3}, {"y", 1}, {"u", 3}} {
		sendKeys(t, term, tuitest.Alt(step.key))
		got := waitClientWorkspace(t, base, step.want)
		log += fmt.Sprintf("alt+%s -> workspace %d (want %d)\n", step.key, got, step.want)
		if got != step.want {
			t.Errorf("ASSERTION: alt+%s shows workspace %d, want %d\n%s", step.key, got, step.want, term.Snapshot())
		}
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "cycle.txt"), []byte(log), 0o644); err != nil {
		t.Logf("save cycle.txt: %v", err)
	}
	alive(t, term, "after cycling")
}

func TestWorkspaceCycleOpensNoPanes(t *testing.T) {
	const sess = "cyc2"
	base := t.TempDir()
	writeConfig(t, base, "[workspaces]\nnew_window_when_empty = true\n"+cycleKeys)
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", sess}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "setup")
	toWindowMode(t, term)
	for range 3 {
		sendKeys(t, term, tuitest.Alt("y"))
		time.Sleep(300 * time.Millisecond)
	}
	paneRequestsDone(t, base, "after three presses")
	if n := len(xpanesRowsIn(t, base, sess)); n != 1 {
		t.Errorf("ASSERTION: the session has %d panes after cycling, want 1\n%s", n, term.Snapshot())
	}
	if ws := clientWorkspace(t, base); ws != 1 {
		t.Errorf("ASSERTION: the client shows workspace %d, want 1: no other workspace has panes", ws)
	}
	saveWindowList(t, base, sess, filepath.Join(artifactDir(t), "list-windows.json"))
	alive(t, term, "after cycling")
}
