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
// TestDockComponentTimeoutHidesTheCell runs `sleep 30`, one simple command, and
// a shell execs a lone final command instead of forking it. That case passes
// with no kill of the process group at all, so on its own it is a false green.
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
func TestDockComponentTimeoutKillsAForkedChild(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the component timeout")
	}
	cases := []struct{ name, command string }{
		{"trailing_command", "sleep 30; true"},
		{"background_job", "sleep 30 & wait"},
		{"subshell", "(sleep 30)"},
		{"pipeline", "sleep 30 | cat"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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

// TestDockComponentTimeoutSurvivesAnEscapedChild covers the child the group kill
// cannot reach. setsid moves it into a session of its own, so the signal misses
// it and it goes on holding the pipe. The wait grace is what ends the read.
func TestDockComponentTimeoutSurvivesAnEscapedChild(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the component timeout")
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("this machine has no setsid, so a child cannot leave the group")
	}
	engine := newDockEngine([]*dockComponent{{
		Name: "custom/escaped", Command: "setsid sleep 20 & wait", MaxWidth: 24,
	}})
	t.Cleanup(engine.Stop)
	start := time.Now()
	engine.Start()

	u := awaitDockUpdate(t, engine, config.DockCustomTimeout+dockTimeoutSlack,
		"a child that left the process group held the read open past the deadline")
	if u.Err == "" {
		t.Fatalf("a hung component reported success: %+v", u)
	}
	if elapsed := time.Since(start); elapsed > config.DockCustomTimeout+dockTimeoutSlack {
		t.Fatalf("the timeout took %s, want about %s", elapsed, config.DockCustomTimeout)
	}
}

// TestDockComponentTimeoutDropsPartialOutput states the deliberate choice about
// a component that printed a value and then hung. The value is dropped and the
// cell is hidden, the same as any other failure, because half a reading shown as
// a whole one is worse than no reading. list-dock-components still says what
// happened.
func TestDockComponentTimeoutDropsPartialOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the component timeout")
	}
	engine := newDockEngine([]*dockComponent{{
		Name: "custom/partial", Command: "printf 'ready\\n'; sleep 30", MaxWidth: 24,
	}})
	t.Cleanup(engine.Stop)
	engine.Start()

	u := awaitDockUpdate(t, engine, config.DockCustomTimeout+dockTimeoutSlack,
		"a hung component was never killed")
	if u.Text != "" {
		t.Fatalf("a component that timed out kept its partial output %q", u.Text)
	}
	if u.Exit != -1 {
		t.Fatalf("a timed out component reported exit %d, want -1", u.Exit)
	}
	engine.applyUpdate(u)

	snap := engine.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot has %d components, want 1", len(snap))
	}
	if snap[0].text != "" {
		t.Fatalf("a timed out component still shows %q", snap[0].text)
	}
	if snap[0].lastErr == "" {
		t.Fatal("a timed out component recorded no last_error, so list-dock-components explains nothing")
	}
	if snap[0].lastExit != -1 {
		t.Fatalf("last_exit is %d, want -1", snap[0].lastExit)
	}
}
