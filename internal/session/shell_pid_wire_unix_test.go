//go:build unix

package session

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"testing"
)

// TestTheShellPidReachesAClientOverTheSocket is the whole transport, end to
// end: the daemon reads the pid off the PTY it owns, stamps it on the
// detector's poll, and the attached client is pushed a state carrying it. The
// pid has to name the pane's real shell, because the client corroborates the
// pane's reported directory by reading that process's /proc entry.
func TestTheShellPidReachesAClientOverTheSocket(t *testing.T) {
	d, _ := startTestDaemon(t)
	if _, err := d.manager.CreateSession("pids", &SessionConfig{}, 80, 24); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess := d.manager.GetSession("pids")

	client := attachTestClient(t, "pids")
	var got syncWatcher
	client.OnStateSync(got.add)

	if _, err := sess.AddDaemonWindow("Window", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	state := sess.GetState()
	if len(state.Windows) != 1 {
		t.Fatalf("the session holds %d windows, want 1", len(state.Windows))
	}
	winID := state.Windows[0].ID
	pty := sess.GetPTY(state.Windows[0].PTYID)
	if pty == nil {
		t.Fatal("the window has no PTY, so there is no pid to send")
	}
	want := pty.ShellPID()
	if want <= 0 {
		t.Fatal("the daemon's own PTY reports no shell pid")
	}

	s := got.await(t, "a state carrying the pane's shell pid", func(s *SessionState) bool {
		for _, w := range s.Windows {
			if w.ID == winID && w.ShellPID != 0 {
				return true
			}
		}
		return false
	})
	if pid := stateWindow(t, s, winID).ShellPID; pid != want {
		t.Fatalf("the client was sent shell pid %d, want %d", pid, want)
	}

	if runtime.GOOS == "linux" {
		if _, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", want)); err != nil {
			t.Fatalf("the pid the client got names no readable process: %v", err)
		}
	} else if err := syscall.Kill(want, 0); err != nil {
		t.Fatalf("the pid the client got names no live process: %v", err)
	}

	if pgid, err := syscall.Getpgid(want); err != nil {
		t.Fatalf("Getpgid(%d): %v", want, err)
	} else if pgid != want {
		t.Fatalf("the pane's shell is pid %d in group %d; the client reads the number as a group", want, pgid)
	}

	for i := range 3 {
		sess.applyAgentDetection(d.foregroundResolver(sess), d.agentMatcher.identifyDetail)
		if pid := stateWindow(t, sess.GetState(), winID).ShellPID; pid != want {
			t.Fatalf("poll %d moved the pid to %d, want %d", i+1, pid, want)
		}
	}
}
