//go:build linux || darwin

package session

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// TestASharedProcessDaemonPlacesOnlyWhatItStarted runs the daemon the way the
// in-process test backend does: in a process that starts CLI calls of its
// own. Such a call is the person, as it is with a daemon in a process of its
// own. A pane's shell and a process that shell started are inside the pane,
// because the daemon recorded the shell when it started it. A daemon in a
// process of its own still holds its own children, which no pane started.
//
// Negative control: with the daemon's own pid taken as a spawn root also when
// SharedProcess is set (spawnRootOf), the plain child of this process is
// placed inside a pane and the first check fails.
func TestASharedProcessDaemonPlacesOnlyWhatItStarted(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemonWith(t, &DaemonConfig{SharedProcess: true})
	sess := makeSessionWithWindow(t, d, "shared")

	// A child of this process, with no tuios variable, stands for a CLI call
	// the harness makes.
	child := exec.Command("sleep", "30")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TUIOS_") {
			child.Env = append(child.Env, kv)
		}
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	if from, why := d.paneOrigin(child.Process.Pid); from {
		t.Errorf("a child of the shared process is placed inside a pane: %s", why)
	}

	pty := sess.GetPTY(sess.GetState().Windows[0].PTYID)
	if pty == nil || pty.ShellPID() <= 0 {
		t.Fatal("the window has no shell")
	}
	shell := pty.ShellPID()
	if from, _ := d.paneOrigin(shell); !from {
		t.Error("a pane's shell is not inside a pane")
	}

	// A process the pane's shell started.
	if _, err := pty.Write([]byte("sleep 30\r")); err != nil {
		t.Fatal(err)
	}
	below := 0
	sid, _ := procinfo.SessionID(shell)
	for deadline := time.Now().Add(10 * time.Second); below == 0; {
		for _, pid := range procinfo.SessionMembers(sid) {
			if ppid, ok := procinfo.ParentPID(pid); ok && ppid == shell {
				below = pid
			}
		}
		if below == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("the shell %d started no sleep", shell)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if w := d.spawnRootOf(below, 0, true); !w.found || w.root.kind != spawnPane || w.root.pid != shell {
		t.Errorf("a process the pane's shell started has root %+v (found %v), want the pane shell %d", w.root, w.found, shell)
	}

	if _, err := os.Stat(sp + ".pid"); !os.IsNotExist(err) {
		t.Errorf("a shared-process daemon wrote a pid file, which kill-server would signal: %v", err)
	}

	// A daemon in a process of its own holds the same child: no pane
	// started it, and it is the daemon's.
	own := NewDaemon(&DaemonConfig{})
	t.Cleanup(own.manager.Shutdown)
	if from, _ := own.paneOrigin(child.Process.Pid); !from {
		t.Error("a child of a daemon in its own process is not held as inside a pane")
	}
}
