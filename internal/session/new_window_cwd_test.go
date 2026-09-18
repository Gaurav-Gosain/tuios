package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sessionWithInheritCwd builds a one-window session with the inherit setting
// at v, and returns it with the id of the window that starts out focused.
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

// cwdOfWindow reads a window's live shell directory, waiting for the shell to
// exist. A shell that never reports one skips the test rather than failing it:
// ProcessCwd is platform-specific and this test is about the choice tuios
// makes, not about whether this OS can answer.
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

// TestANewWindowStartsWhereTheFocusedPaneIs is the feature from #187: opening a
// window from a pane deep in a project should land in that project, not back in
// the directory the daemon happened to be started in.
//
// Negative control: dropping the inheritedCwd call from AddDaemonWindowWith made
// the second window start in the daemon's directory and this failed.
func TestANewWindowStartsWhereTheFocusedPaneIs(t *testing.T) {
	dir := t.TempDir()
	// macOS hands out a symlinked temp dir, and a shell reports the resolved
	// path, so compare what the OS will actually say.
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	sess, first := sessionWithInheritCwd(t, true)
	// Put the first pane somewhere specific by starting it there, which is the
	// same state a user reaches by cd'ing.
	if _, err := sess.AddDaemonWindowWith(NewWindowOptions{Title: "anchor", Cwd: real, Focus: true}, nil); err != nil {
		t.Fatalf("AddDaemonWindowWith: %v", err)
	}
	_ = first

	win, err := sess.AddDaemonWindowWith(NewWindowOptions{Title: "inheritor"}, nil)
	if err != nil {
		t.Fatalf("AddDaemonWindowWith: %v", err)
	}
	if got := cwdOfWindow(t, sess, win.ID); got != real {
		t.Errorf("new window started in %q, want the focused pane's %q", got, real)
	}
}

// TestTheSettingOffKeepsTheDaemonsDirectory is the other half: someone who
// turns the option off gets exactly what every window did before it existed.
func TestTheSettingOffKeepsTheDaemonsDirectory(t *testing.T) {
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	daemonDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	sess, _ := sessionWithInheritCwd(t, false)
	if _, err := sess.AddDaemonWindowWith(NewWindowOptions{Title: "anchor", Cwd: real, Focus: true}, nil); err != nil {
		t.Fatalf("AddDaemonWindowWith: %v", err)
	}
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{Title: "plain"}, nil)
	if err != nil {
		t.Fatalf("AddDaemonWindowWith: %v", err)
	}
	if got := cwdOfWindow(t, sess, win.ID); got == real {
		t.Errorf("new window inherited %q with the setting off", got)
	} else if want, _ := filepath.EvalSymlinks(daemonDir); got != want {
		t.Errorf("new window started in %q, want the daemon's %q", got, want)
	}
}

// TestAnExplicitDirectoryWinsOverTheFocusedPane keeps the precedence the verb
// protocol depends on: `tuios new-window --cwd` names a directory and that is
// the one used, inherit setting or not.
func TestAnExplicitDirectoryWinsOverTheFocusedPane(t *testing.T) {
	anchor, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	asked, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	sess, _ := sessionWithInheritCwd(t, true)
	if _, err := sess.AddDaemonWindowWith(NewWindowOptions{Title: "anchor", Cwd: anchor, Focus: true}, nil); err != nil {
		t.Fatalf("AddDaemonWindowWith: %v", err)
	}
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{Title: "explicit", Cwd: asked}, nil)
	if err != nil {
		t.Fatalf("AddDaemonWindowWith: %v", err)
	}
	if got := cwdOfWindow(t, sess, win.ID); got != asked {
		t.Errorf("new window started in %q, want the asked-for %q", got, asked)
	}
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
