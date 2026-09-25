//go:build unix

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The component timeout, tested against the thing that actually breaks it.
//
// A bare `sleep 30` is one simple command, and a shell execs a lone final
// command instead of forking it. That case passes with no kill of the process
// group at all, so on its own it is a false green.
// Every command below forks a child that inherits the stdout pipe, which is the
// shape reported in issue 141 and the shape almost every real component has.

// dockTimeoutSlack is how long after the deadline a component still counts as
// killed. Generous, because the assertion under test is seconds against tens of
// seconds, not milliseconds against milliseconds.
const dockTimeoutSlack = 4 * time.Second

// awaitDockUpdate waits for one update, failing with why if none arrives.
func awaitDockUpdate(t *testing.T, e *dockEngine, wait time.Duration, why string) dockComponentUpdate {
	t.Helper()
	select {
	case u := <-e.Updates():
		return u
	case <-time.After(wait):
		t.Fatal(why)
		return dockComponentUpdate{}
	}
}

// TestDockComponentTimeoutKillsAForkedChild is issue 141. Each command here
// leaves a child holding the stdout pipe after the shell is killed, so a read
// that waits for EOF waits for the child, and the deadline does nothing.
//
// The escaped case is the child the group kill cannot reach: setsid moves it
// into a session of its own, so the signal misses it and it goes on holding
// the pipe. The wait grace is what ends the read there.
func TestDockComponentTimeoutKillsAForkedChild(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the component timeout")
	}
	cases := []struct{ name, command, needs string }{
		{"trailing_command", "sleep 30; true", ""},
		{"background_job", "sleep 30 & wait", ""},
		{"subshell", "(sleep 30)", ""},
		{"pipeline", "sleep 30 | cat", ""},
		{"escaped_child", "setsid sleep 20 & wait", "setsid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needs != "" {
				if _, err := exec.LookPath(tc.needs); err != nil {
					t.Skipf("this machine has no %s", tc.needs)
				}
			}
			engine := newDockEngine([]*dockComponent{{
				Name: "custom/hang", Command: tc.command, MaxWidth: 24,
			}})
			t.Cleanup(engine.Stop)
			start := time.Now()
			engine.Start()

			u := awaitDockUpdate(t, engine, config.DockCustomTimeout+dockTimeoutSlack,
				"a hung component was never killed: its forked child still holds the stdout pipe")
			if u.Err == "" {
				t.Fatalf("a hung component reported success: %+v", u)
			}
			if elapsed := time.Since(start); elapsed > config.DockCustomTimeout+dockTimeoutSlack {
				t.Fatalf("the timeout took %s, want about %s", elapsed, config.DockCustomTimeout)
			}
			engine.applyUpdate(u)
			if got := engine.Text("custom/hang"); got != "" {
				t.Fatalf("a hung component drew %q", got)
			}
		})
	}
}

// TestDockComponentTimeoutLeavesNoOrphan is the other half: the deadline has to
// kill the tree, not just unblock the read. A component that times out every
// interval and leaves one process behind each time is a leak that grows.
//
// The orphan writes a file five seconds in, two seconds after the deadline. The
// file must never appear.
func TestDockComponentTimeoutLeavesNoOrphan(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the component timeout")
	}
	marker := filepath.Join(t.TempDir(), "orphan-ran")
	command := fmt.Sprintf("sh -c 'sleep 5; touch %s' & wait", marker)

	engine := newDockEngine([]*dockComponent{{
		Name: "custom/hang", Command: command, MaxWidth: 24,
	}})
	t.Cleanup(engine.Stop)
	engine.Start()

	u := awaitDockUpdate(t, engine, config.DockCustomTimeout+dockTimeoutSlack,
		"a hung component was never killed")
	if u.Err == "" {
		t.Fatalf("a hung component reported success: %+v", u)
	}

	// Past the orphan's own deadline, so its absence is a kill and not a race.
	time.Sleep(4 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("the orphaned child outlived the timeout and wrote %s", marker)
	} else if !os.IsNotExist(err) {
		t.Fatalf("could not check for the orphan's marker: %v", err)
	}
}
