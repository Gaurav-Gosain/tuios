package tuie2e

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRestoredPaneComesBackInItsDirectory is session restore bringing a pane
// back where its shell was, on every platform that can read another process's
// working directory: Linux through procfs and macOS through libproc.
//
// The daemon reads each shell's directory when it saves, because a shell does
// not have to announce one. The shell here is /bin/sh, which announces
// nothing over OSC 7, so the only way the restored pane can land in the
// directory is the daemon having read it from the process. The capture was
// Linux-only once, and every macOS restore then opened its panes wherever the
// daemon was started.
//
// The ways it could pass without testing that:
//   - The restored shell could start in the directory by accident. The
//     directory is one made for this test, and the daemon runs elsewhere.
//   - The line read could be the shell's echo of the command. The shell
//     answers through a format, so only its output has a line that is exactly
//     CWD-SAME or CWD-ELSEWHERE.
//   - The pane read could be the one from before the restart. The session is
//     checked to be marked restored first, which only a new daemon does.
//
// The pane's text before and after the restart is saved under artifactDir.
func TestRestoredPaneComesBackInItsDirectory(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("reading a shell's directory needs procfs or libproc")
	}
	const session = "e2e-restore-cwd"
	base := t.TempDir()
	killDaemon(t, base)

	dir := filepath.Join(base, "restored-here")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The kernel reports the directory with its symlinks resolved, and on
	// macOS the temporary directory is under one (/var is /private/var).
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if !paneInDir(t, base, session, "cd '"+dir+"' && ", want) {
		t.Fatalf("the shell did not move into %s", want)
	}
	artifacts := artifactDir(t)
	savePane(t, base, session, filepath.Join(artifacts, "before-restart.txt"))

	// kill-server saves every session while its shells are still alive.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	// Starting any session starts a daemon, and a daemon restores on start.
	if out, err := tuiosCLI(t, base, "new", "e2e-restore-cwd-trigger", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v\n%s", err, out)
	}
	if info := waitForSessionInfo(t, base, session); !info.Restored {
		t.Fatalf("the session is not marked restored, so its pane is not the respawned one")
	}

	// The restored shell is asked where it is before anything moves it.
	inDir := paneInDir(t, base, session, "", want)
	savePane(t, base, session, filepath.Join(artifacts, "after-restart.txt"))
	if !inDir {
		t.Errorf("ASSERTION: the restored pane's shell did not start in %s, the directory it was in when the daemon stopped", want)
	}
}

// paneInDir runs prefix and then asks the shell in the session's pane whether
// its working directory is dir, and returns its answer. The shell compares,
// rather than printing the path, because a long path wraps in the capture.
func paneInDir(t *testing.T, base, session, prefix, dir string) bool {
	t.Helper()
	cmd := prefix + `if [ "$(pwd -P)" = '` + dir + `' ]; then printf 'CWD-%s\n' SAME; else printf 'CWD-%s\n' ELSEWHERE; fi` + "\r"
	if out, err := tuiosCLI(t, base, "send-keys", "-s", session, "-l", cmd); err != nil {
		t.Fatalf("ask the shell where it is: %v\n%s", err, out)
	}
	var pane string
	deadline := time.Now().Add(shellTimeout)
	for {
		pane, _ = tuiosCLI(t, base, "capture-pane", "-s", session)
		for _, line := range strings.Split(pane, "\n") {
			switch strings.TrimSpace(line) {
			case "CWD-SAME":
				return true
			case "CWD-ELSEWHERE":
				t.Logf("the shell is not in %s:\n%s", dir, pane)
				return false
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the shell never answered:\n%s", pane)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// savePane writes the session's pane text to path.
func savePane(t *testing.T, base, session, path string) {
	t.Helper()
	pane, err := tuiosCLI(t, base, "capture-pane", "-s", session)
	if err != nil {
		t.Logf("capture the pane for %s: %v", path, err)
		return
	}
	if err := os.WriteFile(path, []byte(pane), 0o644); err != nil {
		t.Logf("save %s: %v", path, err)
	}
}
