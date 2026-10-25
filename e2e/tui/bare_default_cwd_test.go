package tuie2e

import (
	"encoding/json"
	"testing"
	"time"
)

// A bare client starts an empty default session. Its first shell should open
// where that client was invoked, even when the daemon started elsewhere.
func TestBareDefaultSessionStartsInCallersDirectory(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[startup]\ndaemon = true\nopen_default_window = false\n[appearance]\nnew_window_inherit_cwd = false\n")
	if out, err := tuiosCLI(t, base, "start-server"); err != nil {
		t.Fatalf("start daemon: %v\n%s", err, out)
	}
	if rows := waitForRows(t, base, func(rows []lsRow) bool { return len(rows) == 0 }); len(rows) != 0 {
		t.Fatalf("daemon did not start empty: %+v", rows)
	}

	callerDir := projectDir(t, base, "caller")
	term := startIn(t, base, startOpts{daemonDefault: true, dir: callerDir})
	waitBoot(t, term)
	rows := waitForRows(t, base, func(rows []lsRow) bool { return len(rows) == 1 })
	if len(rows) != 1 {
		t.Fatalf("bare tuios did not create one default session: %+v", rows)
	}
	newWindow(t, term)
	firstWindowCwd(t, base, rows[0].Name, callerDir)
	saveFrame(t, term, "bare-default-cwd-first")

	// A later bare attach comes from elsewhere. With pane inheritance off, a
	// window it opens must still use the session's original start directory.
	otherDir := projectDir(t, base, "other")
	other := startIn(t, base, startOpts{args: []string{"attach"}, dir: otherDir})
	waitWindowCount(t, other, 1, "second client attaching to the existing session")
	windowManagementMode(t, other)
	newWindow(t, other)

	var got, out string
	deadline := time.Now().Add(shellTimeout)
	for time.Now().Before(deadline) {
		var err error
		out, err = tuiosCLI(t, base, "list-windows", "-s", rows[0].Name, "--json")
		if err == nil {
			var result struct {
				Windows []struct {
					Cwd     string `json:"cwd"`
					Focused bool   `json:"focused"`
				} `json:"windows"`
			}
			if json.Unmarshal([]byte(out), &result) == nil && len(result.Windows) == 2 {
				for _, window := range result.Windows {
					if window.Focused {
						got = window.Cwd
						if got == callerDir {
							saveFrame(t, other, "bare-default-cwd-existing")
							return
						}
					}
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("second client's window starts in %q, want %q\n%s", got, callerDir, out)
}
