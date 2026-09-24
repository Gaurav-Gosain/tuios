package tuie2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The rail's file section lists a remote pane's files from the machine the
// pane is on.
//
// This is the whole chain and no part of it can be checked anywhere else. The
// far machine is asked where its process is, because nothing here can read a
// pid over there and bash announces nothing; the daemon that owns the window
// forwards the listing to that machine, because it does not have the files
// either; and the client draws what comes back.
//
// It failed in every one of those places at some point, and the last time it
// failed silently: the window's machine was wiped by the first client sync, so
// the daemon read the pane as local and listed its own disk for a path that
// only exists on the other one.
func TestTheFileSectionListsARemotePanesFiles(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	// A file that exists only on the far machine, in the directory its shells
	// start in. Finding it named in the rail is the proof: nothing on this
	// machine could have produced that row.
	farDir := workDirIn(t, remote)
	// Short on purpose: the rail is about twenty-four cells wide and truncates a
	// long name, and an assertion that cannot fit on screen fails for the wrong
	// reason. The first version of this used a sentence and failed with the
	// file listed right there in the snapshot.
	const marker = "far-only.txt"
	if err := os.WriteFile(filepath.Join(farDir, marker), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool {
		return containsAll(s, "build", "up")
	}, "the daemon never reported build up")

	created, err := tuiosCLIEnv(t, base, env, "new-window", "faraway", "-s", "home",
		"--host", "build")
	if err != nil {
		t.Fatalf("create a window on build: %v\n%s", err, created)
	}

	toggleSidebarViaPalette(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return contains(s.Text(), marker)
	}, uiTimeout); err != nil {
		windows, _ := tuiosCLIEnv(t, base, env, "list-windows", "-s", "home", "--json")
		dumpLinkLogs(t, base, remote)
		t.Fatalf("the file section never listed the far machine's files: %v\nnew-window said: %s\nthe daemon lists: %s\n%s",
			err, created, windows, term.Snapshot())
	}
	alive(t, term, "after listing a remote pane's files")
}
