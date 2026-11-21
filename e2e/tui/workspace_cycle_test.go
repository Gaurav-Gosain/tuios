package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const cycleKeys = "[keybindings.workspaces]\nnext_workspace = [\"alt+y\"]\nprev_workspace = [\"alt+u\"]\n"

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
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "--workspace", "3", "--no-focus"); err != nil {
		t.Fatalf("new-window on 3: %v\n%s", err, out)
	}
	waitPanesOn(t, term, base, sess, 3, 1, "the pane on workspace 3")
	toWindowMode(t, term, base)

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
	toWindowMode(t, term, base)
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
