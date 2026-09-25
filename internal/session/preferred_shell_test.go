//go:build !windows

package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// linkShell makes a symlink to /bin/sh under a name of its own, so the pane's
// process path says which setting picked it.
func linkShell(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.Symlink("/bin/sh", path); err != nil {
		t.Fatalf("symlink %s: %v", path, err)
	}
	return path
}

// A daemon pane runs appearance.preferred_shell, as a standalone pane does,
// and a config reload reaches the next pane of a session that already exists.
// The daemon used to ignore the setting and run $SHELL.
func TestDaemonPaneRunsThePreferredShell(t *testing.T) {
	dir := t.TempDir()
	first := linkShell(t, dir, "first-sh")
	second := linkShell(t, dir, "second-sh")
	t.Setenv("SHELL", "/bin/sh")

	uc := config.DefaultConfig()
	uc.Appearance.PreferredShell = first
	d := NewDaemon(DaemonConfigFromUser(uc))

	sess, err := d.manager.CreateSession("preferred-shell", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer sess.Stop()

	pty, err := sess.CreatePTY("win-1", 40, 20, nil)
	if err != nil {
		t.Fatalf("CreatePTY: %v", err)
	}
	if got := pty.cmd.Path; got != first {
		t.Errorf("ASSERTION: a daemon pane ran %q, want the preferred shell %q", got, first)
	}

	next := config.DefaultConfig()
	next.Appearance.PreferredShell = second
	d.onConfigReload(next, nil)

	pty2, err := sess.CreatePTY("win-2", 40, 20, nil)
	if err != nil {
		t.Fatalf("CreatePTY after reload: %v", err)
	}
	if got := pty2.cmd.Path; got != second {
		t.Errorf("ASSERTION: after a config reload a new pane ran %q, want %q", got, second)
	}
}

// A preferred shell that does not exist falls back to $SHELL rather than
// failing the spawn.
func TestMissingPreferredShellFallsBackToSHELL(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	m := NewManager()
	m.SetPreferredShell(filepath.Join(t.TempDir(), "no-such-shell"))
	sess, err := m.CreateSession("missing-shell", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer sess.Stop()
	if got := sess.getShell(); got != "/bin/sh" {
		t.Errorf("getShell = %q, want $SHELL /bin/sh", got)
	}
}
