package session

import (
	"testing"
	"time"
)

func sessionWithInheritCwd(t *testing.T, v bool) (*Session, string) {
	t.Helper()
	t.Cleanup(useResurrectionDir(t.TempDir()))
	sess, err := NewSession("cwd", &SessionConfig{InheritCwd: v}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(sess.Stop)
	win, err := sess.AddDaemonWindow("first", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	return sess, win.ID
}

func cwdOfWindow(t *testing.T, sess *Session, windowID string) string {
	t.Helper()
	win, ok := findWindowState(sess.GetState(), windowID)
	if !ok {
		t.Fatalf("window %s not found", windowID)
	}
	pty := sess.GetPTY(win.PTYID)
	if pty == nil {
		t.Fatalf("window %s has no PTY", windowID)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cwd, ok := pty.ProcessCwd(); ok && cwd != "" {
			return cwd
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Skip("this platform does not report a process working directory")
	return ""
}

// TestCreatingASessionThroughTheManagerDoesNotDeadlock is the regression test
// for the way the inherit flag first reached a session.
//
// CreateSession holds the manager's lock while it stamps the config, and reads
// the fields around this one directly for exactly that reason. Reaching for the
// flag through a locking accessor took the same lock a second time and every
// daemon session creation stopped there, which the feature's own tests missed
// because they build a Session directly and never go through the manager.
func TestCreatingASessionThroughTheManagerDoesNotDeadlock(t *testing.T) {
	t.Cleanup(useResurrectionDir(t.TempDir()))
	m := NewManager()
	m.SetNewWindowInheritCwd(true)

	done := make(chan *Session, 1)
	go func() {
		sess, err := m.CreateSession("deadlock-check", &SessionConfig{}, 80, 24)
		if err != nil {
			done <- nil
			return
		}
		done <- sess
	}()

	select {
	case sess := <-done:
		if sess == nil {
			t.Fatal("CreateSession failed")
		}
		t.Cleanup(sess.Stop)
		if !sess.config.InheritCwd {
			t.Error("the session did not take the manager's inherit setting")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CreateSession deadlocked")
	}
}
