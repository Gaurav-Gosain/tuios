package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A session can hold a window whose process runs on another machine. The rail
// lists that window, so it has to say which machine it is on: a command typed
// into a pane does a different thing depending on which machine answers it,
// and the rail is where someone looks before typing.
//
// This is the whole path in one test, which is why it is here rather than only
// in the unit tests. The daemon puts the host on the window state, pushes it,
// the client adopts it onto its own window, the session tree carries it, and
// the row draws it. A break anywhere along that chain is a row that silently
// says nothing, and nothing below the top of it would fail.
func TestTheRailNamesTheMachineAPaneRunsOn(t *testing.T) {
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

	if out, err := tuiosCLIEnv(t, base, env, "new-window", "faraway", "-s", "home", "--host", "build"); err != nil {
		t.Fatalf("create a window on build: %v\n%s\n%s", err, out, term.Snapshot())
	}

	toggleSidebarViaPalette(t, term)
	railShows(t, term, "faraway")

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railRowWith(s.Text(), "faraway", "build")
	}, uiTimeout); err != nil {
		t.Fatalf("no rail row names both the pane and the machine it runs on: %v\n%s", err, term.Snapshot())
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "build:faraway")
	}, uiTimeout); err != nil {
		t.Errorf("the pane's frame does not name the machine: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLIEnv(t, base, env, "new-window", "local-one", "-s", "home"); err != nil {
		t.Fatalf("make a second window: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return contains(s.Text(), "local-one")
	}, uiTimeout); err != nil {
		t.Fatalf("the second window never appeared: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(time.Second)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railRowWith(s.Text(), "faraway", "build")
	}, uiTimeout); err != nil {
		t.Fatalf("the rail forgot the machine after a client sync: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after a window on another machine")
}

func railRowWith(screen string, parts ...string) bool {
	for line := range strings.SplitSeq(screen, "\n") {
		rail := line
		if before, _, ok := strings.Cut(line, "│"); ok {
			rail = before
		}
		all := true
		for _, p := range parts {
			if !strings.Contains(rail, p) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func railRowIndex(screen string, parts ...string) int {
	for i, line := range strings.Split(screen, "\n") {
		rail := line
		if before, _, ok := strings.Cut(line, "│"); ok {
			rail = before
		}
		all := true
		for _, p := range parts {
			if !strings.Contains(rail, p) {
				all = false
				break
			}
		}
		if all {
			return i
		}
	}
	return -1
}

// The rail groups sessions by machine once there is more than one machine, and
// the global sessions are not one of those groups: they hold panes from
// several machines, so they get a group of their own above the machines.
//
// This is an end to end test because the ordering is what was wrong, twice,
// and ordering is the one thing a unit test on the row builder can agree with
// while the screen still reads differently: the rail draws sections, the
// machine groups sit inside one of them, and a row in the right list can still
// come out in the wrong place on screen.
func TestTheRailPutsTheGlobalGroupAboveTheMachines(t *testing.T) {
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

	toggleSidebarViaPalette(t, term)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		global := railRowIndex(s.Text(), "global")
		local := railRowIndex(s.Text(), "local")
		return global >= 0 && local >= 0 && global < local
	}, uiTimeout); err != nil {
		t.Fatalf("the rail does not put the global group above this machine: %v\n%s", err, term.Snapshot())
	}
}
