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

	// Creating a session on the far machine is what starts its daemon. Without
	// it the link comes up against a machine with no tuios running on it, and
	// a pane cannot be opened there.
	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)

	// Wait for the link, since a window cannot be put on a machine the daemon
	// has not reached yet.
	waitForHostListing(t, base, func(s string) bool {
		return containsAll(s, "build", "up")
	}, "the daemon never reported build up")

	if out, err := tuiosCLIEnv(t, base, env, "new-window", "faraway", "-s", "home", "--host", "build"); err != nil {
		t.Fatalf("create a window on build: %v\n%s\n%s", err, out, term.Snapshot())
	}

	toggleSidebarViaPalette(t, term)
	railShows(t, term, "faraway")

	// On the pane's own rail row, and separately on the pane's frame.
	//
	// Two earlier versions of this passed with the feature taken out. The
	// first asked whether "build" appeared anywhere, and the rail draws a
	// heading for every configured machine, so it was already on screen. The
	// second asked for a line holding both names, and the pane's own title bar
	// holds "build:faraway", which is a different feature on a different part
	// of the screen. An assertion about the rail has to look only at the rail.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railRowWith(s.Text(), "faraway", "build")
	}, uiTimeout); err != nil {
		t.Fatalf("no rail row names both the pane and the machine it runs on: %v\n%s", err, term.Snapshot())
	}

	// The frame says it too, which is what someone looks at before typing.
	// Waited on rather than read once: the rail row and the pane frame are
	// painted from different places and need not land in the same frame.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "build:faraway")
	}, uiTimeout); err != nil {
		t.Errorf("the pane's frame does not name the machine: %v\n%s", err, term.Snapshot())
	}
	// And it survives a client pushing its own state back.
	//
	// This is the half that was broken and that the first version of this test
	// walked straight past. The machine is daemon-owned and a client sync does
	// not carry it, so until it was added to the carry-over the first push
	// wiped it: the rail named the machine for a moment and then stopped, and
	// which panes still showed one depended on when each last synced. Making
	// another window is the cheapest way to make this client push.
	if out, err := tuiosCLIEnv(t, base, env, "new-window", "local-one", "-s", "home"); err != nil {
		t.Fatalf("make a second window: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return contains(s.Text(), "local-one")
	}, uiTimeout); err != nil {
		t.Fatalf("the second window never appeared: %v\n%s", err, term.Snapshot())
	}
	// Given a moment for the push to land and come back.
	time.Sleep(time.Second)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railRowWith(s.Text(), "faraway", "build")
	}, uiTimeout); err != nil {
		t.Fatalf("the rail forgot the machine after a client sync: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after a window on another machine")
}

// railRowWith reports whether one rail row holds every one of parts.
//
// The rail is a column, and a screen line runs past it into the panes. So the
// line is cut at the rail's right edge and only that side is read: without the
// cut, anything a pane happens to be drawing counts as the rail saying it.
func railRowWith(screen string, parts ...string) bool {
	for _, line := range strings.Split(screen, "\n") {
		rail := line
		if i := strings.Index(line, "│"); i >= 0 {
			rail = line[:i]
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

// containsAll is a small helper so the host wait reads as one condition.
func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

// railRowIndex is the first rail line holding every part, or -1. The line is
// cut at the rail's edge first, so a match on the pane area next to it does
// not count as a rail row.
func railRowIndex(screen string, parts ...string) int {
	for i, line := range strings.Split(screen, "\n") {
		rail := line
		if j := strings.Index(line, "│"); j >= 0 {
			rail = line[:j]
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

	// The group appears once a second machine is reachable, so the far daemon
	// has to be up before the rail is read.
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
