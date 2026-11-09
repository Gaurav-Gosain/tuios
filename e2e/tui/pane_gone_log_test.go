package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A pane that is gone was closed or its process ended. On 2026-11-09 one went
// missing around a live upgrade and the daemon log had no line for either, so
// nothing said which. Now a close names the window and who asked, and an
// exit names the pane, its window and the exit code or signal. A closed
// window also leaves the session's window_to_bsp_id, which kept its number
// after it was gone.
//
// How this could pass wrongly, written down first:
//   - A line could come from another pane. Each check names the window's
//     short ID, and each pane runs a different program.
//   - The log could hold the line from an earlier run. The daemon and its
//     log live under the test's own base.
//   - The BSP map could never have held the window, so its absence proves
//     nothing. The test waits for the saved session to hold it first.
//   - The saved map could be old. A pane ended by a signal holds the saved
//     layout, so the map is read before any pane ends.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestPaneGoneSaysWhyInTheLog).
func TestPaneGoneSaysWhyInTheLog(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-pane-gone"
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", session}})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "two panes")
	enableTiling(t, term)
	rows := xpanesRowsIn(t, base, session)
	if len(rows) != 2 {
		t.Fatalf("the session has %d panes, want 2", len(rows))
	}
	closed := rows[1].ID

	// The client places both panes, which gives each a BSP number, and the
	// daemon saves the session within two seconds of a change.
	waitSavedBSP(t, base, session, closed, true, "the window before its close")

	if out, err := tuiosCLI(t, base, "close-window", "-s", session, closed); err != nil {
		t.Fatalf("close-window: %v\n%s", err, out)
	}
	// Read before the panes below end: a pane ended by a signal from outside
	// keeps the saved layout as it was for two minutes (signalHoldFor).
	waitSavedBSP(t, base, session, closed, false, "the window after its close")
	exits, err := tuiosCLI(t, base, "new-window", "-s", session, "--no-focus", "--print-id", "exits", "--", "sh", "-c", "sleep 1; exit 3")
	if err != nil {
		t.Fatalf("new-window exits: %v\n%s", err, exits)
	}
	killed, err := tuiosCLI(t, base, "new-window", "-s", session, "--no-focus", "--print-id", "killed", "--", "sh", "-c", "sleep 1; kill -9 $$")
	if err != nil {
		t.Fatalf("new-window killed: %v\n%s", err, killed)
	}
	exits, killed = lastLine(exits), lastLine(killed)

	wantLog(t, base, "the CLI close", func(line string) bool {
		return strings.Contains(line, "Window "+closed[:8]+" ") &&
			strings.Contains(line, "in session "+session+" closed, asked for by ") &&
			strings.Contains(line, "client-")
	})
	wantLog(t, base, "the exit with code 3", func(line string) bool {
		return strings.Contains(line, "(window "+exits[:8]+") in session "+session+": its process exited with code 3")
	})
	wantLog(t, base, "the kill by signal 9", func(line string) bool {
		return strings.Contains(line, "(window "+killed[:8]+") in session "+session+": its process was ended by signal 9")
	})
	wantLog(t, base, "the closed window's own pane", func(line string) bool {
		return strings.Contains(line, "(window "+closed[:8]+") in session "+session+": its process ") &&
			strings.Contains(line, "after its pane was closed")
	})

	// The artifact: the lines the daemon wrote about the three panes.
	if b, err := os.ReadFile(daemonLogOf(t, base)); err == nil {
		out := filepath.Join(artifactDir(t), "pane-gone-daemon-log.txt")
		if err := os.WriteFile(out, []byte(paneGoneLines(string(b))+"\n"), 0o644); err != nil {
			t.Errorf("write %s: %v", out, err)
		}
	}
}

// lastLine is the last non-empty line of out.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// wantLog waits for a line of the daemon log under base that match accepts.
func wantLog(t *testing.T, base, what string, match func(string) bool) {
	t.Helper()
	path := daemonLogOf(t, base)
	deadline := time.Now().Add(uiTimeout)
	for {
		b, _ := os.ReadFile(path)
		for line := range strings.SplitSeq(string(b), "\n") {
			if match(line) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: no daemon log line for %s. The lines about panes and windows:\n%s", what, paneGoneLines(string(b)))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// paneGoneLines is the lines of a daemon log that say a pane or a window is
// gone.
func paneGoneLines(log string) string {
	var out []string
	for line := range strings.SplitSeq(log, "\n") {
		if strings.Contains(line, "closed, asked for by") || strings.Contains(line, ": its process ") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// waitSavedBSP waits for the session's saved state to hold, or not hold, a
// BSP number for window.
func waitSavedBSP(t *testing.T, base, session, window string, want bool, what string) {
	t.Helper()
	path := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "sessions", session+".json")
	deadline := time.Now().Add(10 * time.Second)
	var ids map[string]int
	for {
		var saved struct {
			WindowToBSPID map[string]int `json:"window_to_bsp_id"`
		}
		if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &saved) == nil {
			ids = saved.WindowToBSPID
			if _, has := ids[window]; has == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: %s: the saved window_to_bsp_id holds %s: %v, want %v (%v)", what, window, !want, want, ids)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
