package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRestoredSessionKeepsItsStartDirectory is #478: a session made with
// tuios new --cwd keeps its start directory across tuios kill-server and a
// restore, so a window opened on an empty workspace afterwards starts in the
// project and not where the new daemon was started.
//
// The ways it could pass without testing that:
//   - The new window could inherit the directory from a pane. Inheritance
//     is off in the config, which is what a client on an empty workspace
//     gets anyway: it has no focused pane there. The only pane also moves
//     out of the project before the restart, so an inherit that slipped
//     through gives the wrong directory.
//   - The daemon could run in the project. It runs in base/cwd, which is
//     neither directory.
//   - The window read could be one from before the restart. The session is
//     checked to be marked restored first, and the window is opened after.
//
// list-windows before and after the restart is saved under artifactDir.
func TestRestoredSessionKeepsItsStartDirectory(t *testing.T) {
	const session = "e2e-start-dir"
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[appearance]\nnew_window_inherit_cwd = false\n")

	proj := projectDir(t, base, "proj")
	elsewhere := projectDir(t, base, "elsewhere")
	artifacts := artifactDir(t)

	if out, err := tuiosCLI(t, base, "new", session, "--detach", "--cwd", proj); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	firstWindowCwd(t, base, session, proj)
	// The first pane leaves the project, so a window that inherits from it
	// lands in the wrong directory.
	if !paneInDir(t, base, session, "cd '"+elsewhere+"' && ", elsewhere) {
		t.Fatalf("the shell did not move into %s", elsewhere)
	}
	saveListWindows(t, base, session, filepath.Join(artifacts, "before-restart.json"))

	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	// Starting any session starts a daemon, and a daemon restores on start.
	if out, err := tuiosCLI(t, base, "new", "e2e-start-dir-trigger", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v\n%s", err, out)
	}
	if info := waitForSessionInfo(t, base, session); !info.Restored {
		t.Fatalf("the session is not marked restored")
	}

	if out, err := tuiosCLI(t, base, "new-window", "-s", session, "--workspace", "3", "fresh"); err != nil {
		t.Fatalf("open a window on workspace 3: %v\n%s", err, out)
	}
	got := windowCwdOn(t, base, session, 3, proj)
	saveListWindows(t, base, session, filepath.Join(artifacts, "after-restart.json"))
	if got != proj {
		t.Errorf("ASSERTION: the new window on the empty workspace is in %q, want the session's start directory %q", got, proj)
	}
}

// windowCwdOn waits for a window on workspace ws of session to report want as
// its directory, and returns the last directory it reported.
func windowCwdOn(t *testing.T, base, session string, ws int, want string) string {
	t.Helper()
	var got string
	deadline := time.Now().Add(shellTimeout)
	for time.Now().Before(deadline) {
		out, err := tuiosCLI(t, base, "list-windows", "-s", session, "--json")
		if err == nil {
			var res struct {
				Windows []struct {
					Cwd       string `json:"cwd"`
					Workspace int    `json:"workspace"`
				} `json:"windows"`
			}
			if json.Unmarshal([]byte(out), &res) == nil {
				for _, w := range res.Windows {
					if w.Workspace == ws && w.Cwd != "" {
						got = w.Cwd
					}
				}
				if got == want {
					return got
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return got
}

// saveListWindows writes list-windows --json for session to path.
func saveListWindows(t *testing.T, base, session, path string) {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "-s", session, "--json")
	if err != nil {
		t.Logf("list windows for %s: %v", path, err)
		return
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(out)+"\n"), 0o644); err != nil {
		t.Logf("save %s: %v", path, err)
	}
}

// TestStartDirectoryOutlivesARestartWhileMissing: the project folder is
// gone while the daemon restores the session, as a drive that is not
// mounted yet would be. The daemon saves the session again with the folder
// still gone. The folder comes back, the daemon restarts once more, and a
// new window on an empty workspace starts in it. A restore that dropped the
// missing folder would have saved the session with no start directory.
func TestStartDirectoryOutlivesARestartWhileMissing(t *testing.T) {
	const session = "e2e-start-dir-missing"
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[appearance]\nnew_window_inherit_cwd = false\n")

	proj := projectDir(t, base, "proj")
	away := filepath.Join(base, "proj-unmounted")

	if out, err := tuiosCLI(t, base, "new", session, "--detach", "--cwd", proj); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	firstWindowCwd(t, base, session, proj)

	restart := func(trigger string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
			t.Fatalf("kill-server: %v\n%s", err, out)
		}
		if out, err := tuiosCLI(t, base, "new", trigger, "--detach"); err != nil {
			t.Fatalf("start a fresh daemon: %v\n%s", err, out)
		}
		if info := waitForSessionInfo(t, base, session); !info.Restored {
			t.Fatalf("the session is not marked restored")
		}
	}

	if err := os.Rename(proj, away); err != nil {
		t.Fatal(err)
	}
	// Restored with the folder gone, then saved by the next kill-server.
	restart("e2e-start-dir-trigger-1")
	if err := os.Rename(away, proj); err != nil {
		t.Fatal(err)
	}
	restart("e2e-start-dir-trigger-2")

	if out, err := tuiosCLI(t, base, "new-window", "-s", session, "--workspace", "3", "fresh"); err != nil {
		t.Fatalf("open a window on workspace 3: %v\n%s", err, out)
	}
	got := windowCwdOn(t, base, session, 3, proj)
	saveListWindows(t, base, session, filepath.Join(artifactDir(t), "after-second-restart.json"))
	if got != proj {
		t.Errorf("ASSERTION: the new window is in %q, want the start directory %q that was missing during a restart", got, proj)
	}
}
