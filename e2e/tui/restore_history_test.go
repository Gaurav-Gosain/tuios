package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRestoredPaneShowsItsHistoryAboveTheDivider is pane history surviving the
// daemon, read off the frame a real client renders.
//
// A pane prints numbered lines, the daemon is stopped and a new one restores
// the session, and a client attaches. The frame has to show, in this order:
// the old lines, the dim divider that says the history is restored, and a
// shell that answers a command under it. Copy mode then has to reach the
// oldest line, which is history the new shell never printed.
//
// The pane works in a folder that is removed while the daemon is down, so the
// new shell starts in the daemon's folder. The divider names that folder and
// says the saved one is gone; it named the saved folder once, where the shell
// had never been. A second session keeps its folder, and its banner names that
// folder alone.
//
// The ways it could pass without testing that:
//   - The old lines could be the typed command line echoed back. The shell
//     computes each marker with arithmetic, so the text HIST-n-END appears
//     only as output.
//   - The pane read could be the one from before the restart. The session is
//     checked to be marked restored first, which only a new daemon does, and
//     the divider is written only by a restore.
//   - The prompt under the divider could be the old one. The command typed
//     after the restore prints a marker only a live shell can compute.
//
// Frames, plain and styled, are saved under artifactDir.
//
// TUIOS_E2E_HISTORY_WRITER names another build to run the first daemon, the
// one that saves the history. A history saved by an older build has to read
// in this one, and the other way round.
func TestRestoredPaneShowsItsHistoryAboveTheDivider(t *testing.T) {
	const session = "e2e-history"
	const last = 120
	artifacts := artifactDir(t)
	base := t.TempDir()
	killDaemon(t, base)
	// The daemon saves the folders the kernel reports, with symlinks resolved,
	// and on macOS the temporary directory is under one.
	gone, kept := filepath.Join(base, "gone"), filepath.Join(base, "kept")
	for _, dir := range []*string{&gone, &kept} {
		if err := os.Mkdir(*dir, 0o755); err != nil {
			t.Fatal(err)
		}
		resolved, err := filepath.EvalSymlinks(*dir)
		if err != nil {
			t.Fatal(err)
		}
		*dir = resolved
	}
	daemonDir, err := filepath.EvalSymlinks(workDirIn(t, base))
	if err != nil {
		t.Fatal(err)
	}

	withBinary("TUIOS_E2E_HISTORY_WRITER", func() {
		if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
			t.Fatalf("create the session: %v\n%s", err, out)
		}
		// Moved there by its shell, because a writer from before new --cwd
		// has to run this too.
		if out, err := tuiosCLI(t, base, "new", session+"-kept", "--detach"); err != nil {
			t.Fatalf("create the session that keeps its folder: %v\n%s", err, out)
		}
		if out, err := tuiosCLI(t, base, "send-keys", "-s", session+"-kept", "-l", "cd '"+kept+"'\r"); err != nil {
			t.Fatalf("move the session that keeps its folder: %v\n%s", err, out)
		}
		first := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", session}})
		if err := first.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
			t.Fatalf("the client never attached: %v\n%s", err, first.Snapshot())
		}
		time.Sleep(insertGuard + 150*time.Millisecond)
		runInShell(t, first, `cd '`+gone+`' && echo "IN-$((1+1))-GONE"`, "IN-2-GONE", shellTimeout)
		runInShell(t, first,
			fmt.Sprintf(`i=1; while [ $i -le %d ]; do echo "HIST-$i-END"; i=$((i+1)); done`, last),
			fmt.Sprintf("HIST-%d-END", last), bulkTimeout)
		saveArtifact(t, first, artifacts, "before-restart")

		// kill-server saves every session, and every pane's history, on the way out.
		if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
			t.Fatalf("kill-server: %v\n%s", err, out)
		}
		waitExit(t, first, "after kill-server")
	})
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	// The history file is private: it holds whatever the pane printed.
	files, _ := filepath.Glob(filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "sessions", "scrollback", session, "*.hist.gz"))
	if len(files) != 1 {
		t.Fatalf("want one saved history file, got %v", files)
	}
	if fi, err := os.Stat(files[0]); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) {
		t.Errorf("history file %v: mode %v, want 0600", err, fi.Mode().Perm())
	}

	if out, err := tuiosCLI(t, base, "new", session+"-trigger", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v\n%s", err, out)
	}
	if info := waitForSessionInfo(t, base, session); !info.Restored {
		t.Fatal("the session is not marked restored, so its pane is not the respawned one")
	}

	second := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", session}})
	lastMark := fmt.Sprintf("HIST-%d-END", last)
	if err := second.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "restored from") && strings.Contains(s.Text(), lastMark)
	}, bootTimeout); err != nil {
		t.Fatalf("the restored pane does not show its history and the divider: %v\n%s", err, second.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	// A live shell under the divider.
	runInShell(t, second, `echo "AFTER-$((40+2))-RESTORE"`, "AFTER-42-RESTORE", shellTimeout)
	if err := second.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	saveArtifact(t, second, artifacts, "after-restart")

	scr := second.Screen()
	_, rows := scr.Size()
	oldRow, divRow, newRow := -1, -1, -1
	for y := range rows {
		line := scr.Line(y)
		switch {
		case strings.Contains(line, lastMark):
			oldRow = y
		case strings.Contains(line, "-- tuios: restored from"):
			divRow = y
		case strings.Contains(line, "AFTER-42-RESTORE") && !strings.Contains(line, "echo"):
			newRow = y
		}
	}
	if oldRow < 0 || divRow < 0 || newRow < 0 || !(oldRow < divRow && divRow < newRow) {
		t.Fatalf("ASSERTION: want the old lines, then the divider, then the new output; rows old=%d divider=%d new=%d\n%s",
			oldRow, divRow, newRow, second.Snapshot())
	}
	// The divider is dim, so it reads as a note and not as output.
	line := scr.Line(divRow)
	col := strings.Index(line, "-- tuios")
	if c := scr.Cell(col+3, divRow); !c.Faint {
		t.Errorf("the divider is not dim: %+v", c)
	}

	// Copy mode reaches the oldest restored line.
	windowManagementMode(t, second)
	if err := second.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("enter copy mode: %v", err)
	}
	if err := second.WaitForText("y yank", uiTimeout); err != nil {
		t.Fatalf("copy mode never opened: %v\n%s", err, second.Snapshot())
	}
	if err := second.SendKeys("g", "g"); err != nil {
		t.Fatalf("jump to oldest: %v", err)
	}
	if err := second.WaitForText("HIST-1-END", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: copy mode does not reach the oldest restored line: %v\n%s", err, second.Snapshot())
	}
	saveArtifact(t, second, artifacts, "copy-mode-oldest")

	// The folders the banners name. The paths can be wider than the pane, so
	// the panes are read unwrapped.
	if got, want := restoreBannerOf(t, base, session), "fresh shell in "+daemonDir+" ("+gone+" no longer available) --"; !strings.HasSuffix(got, want) {
		t.Errorf("ASSERTION: the divider of the pane whose folder is gone is %q, want it to end %q", got, want)
	}
	if got, want := restoreBannerOf(t, base, session+"-kept"), "fresh shell in "+kept+" --"; !strings.HasSuffix(got, want) {
		t.Errorf("ASSERTION: the banner of the pane whose folder stayed is %q, want it to end %q", got, want)
	}
	alive(t, second, "after restoring history")
}

// restoreBannerOf is the last restore banner in the session's whole pane, read
// unwrapped, or "" when the pane has none.
func restoreBannerOf(t *testing.T, base, session string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "capture-pane", "-s", session, "--scrollback", "--lines", "0", "--unwrap")
	if err != nil {
		t.Fatalf("capture %s: %v\n%s", session, err, out)
	}
	var banner string
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "-- tuios: ") {
			banner = strings.TrimSpace(line)
		}
	}
	return banner
}
