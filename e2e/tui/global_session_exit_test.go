package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A pane running on another machine whose shell exits has to close its window,
// without anything else being typed.
//
// This has to be an end to end test and cannot be a unit test. The unit tests
// reach the far daemon by dialling its socket directly, which is the shape of
// the transport but not the transport: it never goes through stdio-proxy or
// the link's stream multiplexer, and that is exactly where the end of the
// pane's stream has to be noticed and passed on.
//
// Reported as: ctrl+D printed "exit" and the window stayed until another key
// was pressed.
func TestAPaneWhoseFarShellExitsClosesItsWindow(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool {
		return containsAll(s, "build", "up")
	}, "the daemon never reported build up")

	if out, err := tuiosCLIEnv(t, base, env, "new-window", "goner", "-s", "home",
		"--host", "build", "--", "/bin/sh", "-c", "echo READY; sleep 2; exit 0"); err != nil {
		t.Fatalf("create a window on build: %v\n%s", err, out)
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return contains(s.Text(), "READY") && contains(s.Text(), "build:goner")
	}, uiTimeout); err != nil {
		t.Fatalf("the far pane never appeared: %v\n%s", err, term.Snapshot())
	}
	wl, err := daemonWindows(base, "home")
	if err != nil {
		t.Fatalf("list the session's windows: %v", err)
	}
	before := len(wl.Windows)
	if before < 1 {
		t.Fatal("ASSERTION: the session holds no windows, so there is nothing to lose")
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		wl, err := daemonWindows(base, "home")
		if err != nil || len(wl.Windows) < before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the far shell exited and the daemon still holds %d windows", len(wl.Windows))
		}
		time.Sleep(100 * time.Millisecond)
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !contains(s.Text(), "build:goner")
	}, uiTimeout); err != nil {
		t.Fatalf("the daemon closed the window and the client kept drawing it: %v\n%s", err, term.Snapshot())
	}
}
