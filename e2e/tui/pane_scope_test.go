package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

// TestPaneRunsInItsOwnScope: a daemon that runs as a systemd user service
// starts each pane in a transient scope of its own with OOMPolicy=continue,
// so the OOM killer taking a pane's process never stops the daemon's unit.
// The suite's daemon is no service, so the pane_scope knob turns the check
// of its cgroup off; the scope itself is made by the person's real user
// manager. The pane's shell reads its own cgroup, the manager reports the
// scope's OOMPolicy, and the scope is gone once the pane closes.
//
// It changes the real user manager, so it runs only on a machine or in a
// container kept for tests, which says so with TUIOS_E2E_OWN_USER_MANAGER=1,
// as TestDaemonRunsAsAService does. TestPaneWithoutAUserManagerStartsAsBefore
// runs everywhere.
//
// Negative control: see NEGATIVE_CONTROLS.md, "A pane's own scope".
func TestPaneRunsInItsOwnScope(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd scopes are on Linux")
	}
	requireReal(t, "the scope holds the daemon's own pane processes")
	if os.Getenv("TUIOS_E2E_OWN_USER_MANAGER") != "1" {
		t.Skip("changes the real systemd user manager; set TUIOS_E2E_OWN_USER_MANAGER=1 on a machine kept for tests")
	}
	if err := exec.Command("systemctl", "--user", "show", "-p", "Version").Run(); err != nil {
		t.Skipf("no systemd user manager: %v", err)
	}
	private := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "systemd", "private")
	for _, c := range []string{"bus", "private socket"} {
		t.Run(c, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			t.Setenv(testknob.Env, testknob.Merge(os.Getenv(testknob.Env), "pane_scope=1"))
			if c == "private socket" {
				// The daemon's runtime dir is the test's, so the manager's
				// socket is linked in and the bus is taken away.
				if _, err := os.Stat(private); err != nil {
					t.Skipf("no private socket of the user manager: %v", err)
				}
				dir := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "systemd")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(private, filepath.Join(dir, "private")); err != nil {
					t.Fatal(err)
				}
				t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
			}
			paneScope(t, base)
		})
	}
}

// TestPaneWithoutAUserManagerStartsAsBefore is the fallback of
// TestPaneRunsInItsOwnScope, and it runs everywhere: with no user manager to
// ask, the pane starts as before, in the daemon's cgroup, and the daemon says
// once why. The daemon's runtime dir is the test's, with no systemd/private
// and no bus in it, and the bus address is cleared, so nothing reaches the
// real user manager.
func TestPaneWithoutAUserManagerStartsAsBefore(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd scopes are on Linux")
	}
	requireReal(t, "reads the cgroup of the daemon's own pane process")
	base := t.TempDir()
	killDaemon(t, base)
	t.Setenv(testknob.Env, testknob.Merge(os.Getenv(testknob.Env), "pane_scope=1"))
	// The daemon's runtime dir is the test's, with no systemd/private
	// and no bus in it.
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	const sess = "e2e-noscope"
	mustCLI(t, base, "new", sess, "--detach")
	out := filepath.Join(base, "cgroup.txt")
	deadline := time.Now().Add(uiTimeout)
	for {
		// Sent again until it runs, as paneScope does.
		mustCLI(t, base, "send-text", "-s", sess, "cat /proc/$$/cgroup > "+out+".tmp && mv "+out+".tmp "+out+"\n")
		pause(t, "poll again until the state it waits for shows", 100*time.Millisecond)
		if raw, err := os.ReadFile(out); err == nil {
			if strings.Contains(string(raw), "tuios-pane-") {
				t.Fatalf("ASSERTION: a pane got a scope with no user manager: %s", raw)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ASSERTION: the pane never ran its command")
		}
	}
	for {
		raw, _ := os.ReadFile(daemonLogOf(t, base))
		if strings.Contains(string(raw), "Panes stay in the daemon's unit") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the daemon did not say why the panes have no scope:\n%s", raw)
		}
		pause(t, "poll again until the state it waits for shows", 50*time.Millisecond)
	}
}

// paneScope checks the scope of the first pane of a new session, and that it
// goes with the pane.
func paneScope(t *testing.T, base string) {
	const sess = "e2e-scope"
	mustCLI(t, base, "new", sess, "--detach")
	out := filepath.Join(base, "cgroup.txt")
	mustCLI(t, base, "send-text", "-s", sess, "cat /proc/$$/cgroup > "+out+".tmp && mv "+out+".tmp "+out+"\n")
	var scope string
	deadline := time.Now().Add(uiTimeout)
	for {
		raw, _ := os.ReadFile(out)
		line := strings.TrimSpace(string(raw))
		if unit := filepath.Base(line); strings.HasPrefix(unit, "tuios-pane-") && strings.HasSuffix(unit, ".scope") {
			scope = unit
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the pane's shell is not in a scope of its own: %q", line)
		}
		pause(t, "poll again until the state it waits for shows", 50*time.Millisecond)
		// The scope is made just after the shell starts: read again.
		mustCLI(t, base, "send-text", "-s", sess, "cat /proc/$$/cgroup > "+out+".tmp && mv "+out+".tmp "+out+"\n")
	}
	show := func() string {
		b, _ := exec.Command("systemctl", "--user", "show", "-p", "OOMPolicy", "-p", "LoadState", "-p", "ActiveState", scope).Output()
		return string(b)
	}
	if got := show(); !strings.Contains(got, "OOMPolicy=continue") || !strings.Contains(got, "ActiveState=active") {
		t.Fatalf("ASSERTION: the scope %s is not active with OOMPolicy=continue:\n%s", scope, got)
	}
	mustCLI(t, base, "kill-session", sess)
	deadline = time.Now().Add(uiTimeout)
	for !strings.Contains(show(), "LoadState=not-found") {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the scope %s stayed after its pane closed:\n%s", scope, show())
		}
		pause(t, "poll again until the state it waits for shows", 100*time.Millisecond)
	}
}
