package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestLayoutSurvivesAnOSShutdown is the reboot. At a shutdown the system sends
// SIGHUP to every shell, and the daemon gets its SIGTERM some time later. The
// panes that die in that burst are not panes a person closed: the layout saved
// on the way out must still hold them, so the next start brings it back.
//
// It runs with a client attached and without one. The attached client is the
// harder case: it hears each pane close and answers with a layout that has one
// fewer window, the same message a person's close sends.
//
// The frame of the restored layout is saved as PNG when TUIOS_E2E_FRAMES names
// a directory.
func TestLayoutSurvivesAnOSShutdown(t *testing.T) {
	for _, attached := range []bool{false, true} {
		name := "detached"
		if attached {
			name = "attached"
		}
		t.Run(name, func(t *testing.T) { layoutSurvivesShutdown(t, name, attached) })
	}
}

func layoutSurvivesShutdown(t *testing.T, name string, attached bool) {
	base := t.TempDir()
	session := "reboot-" + name
	startDetached(t, base, session)
	newWindowIn(t, base, session)
	newWindowIn(t, base, session)
	shells := shellPIDsIn(t, base, session)
	if len(shells) != 3 {
		t.Fatalf("the session has %d panes, want 3", len(shells))
	}

	var client *tuitest.Terminal
	if attached {
		client = startIn(t, base, startOpts{args: []string{"attach", session}})
		if err := client.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 3 }, bootTimeout); err != nil {
			t.Fatalf("the client never showed the three panes: %v\n%s", err, client.Snapshot())
		}
	}

	daemon := daemonPID(t, base)
	// A person's layout has been on disk for a while when the machine goes
	// down. Wait for the checkpoint, so the test asks what the shutdown does
	// to it.
	waitSavedWindows(t, base, session, len(shells))

	// The shutdown: every shell hangs up, then the daemon is told to stop.
	for _, pid := range shells {
		if err := syscall.Kill(pid, syscall.SIGHUP); err != nil {
			t.Fatalf("SIGHUP shell %d: %v", pid, err)
		}
	}
	time.Sleep(5 * time.Second)
	if err := syscall.Kill(daemon, syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM daemon %d: %v", daemon, err)
	}
	deadline := time.Now().Add(uiTimeout)
	for syscall.Kill(daemon, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the daemon %d did not stop after SIGTERM", daemon)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if client != nil {
		waitExit(t, client, "after the daemon stopped")
	}

	// The next login: starting any session starts a daemon, which restores.
	if out, err := tuiosCLI(t, base, "new", "after-login", "--detach"); err != nil {
		t.Fatalf("start a new daemon: %v: %s", err, out)
	}
	restored := waitForSessionInfo(t, base, session)
	if restored.WindowCount != len(shells) {
		t.Fatalf("ASSERTION: the session came back with %d windows, want the %d it had before the shutdown", restored.WindowCount, len(shells))
	}

	back := startIn(t, base, startOpts{args: []string{"attach", session}})
	if err := back.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == len(shells) }, bootTimeout); err != nil {
		t.Fatalf("ASSERTION: the attach did not show the %d panes (count %d): %v\n%s",
			len(shells), countWindows(back.Screen()), err, back.Snapshot())
	}
	if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
		time.Sleep(500 * time.Millisecond)
		savePNG(t, back.Screen(), shot.XTermPalette(), dir, "resurrect-after-shutdown-"+name)
	}
}

// shellPIDsIn is the pid of the shell in each pane of a session.
func shellPIDsIn(t *testing.T, base, session string) []int {
	t.Helper()
	list, err := daemonJSON[struct {
		Windows []struct {
			PID int `json:"pid"`
		} `json:"windows"`
	}](base, "list-windows", "--json", "-s", session)
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	pids := make([]int, 0, len(list.Windows))
	for _, w := range list.Windows {
		if w.PID <= 0 {
			t.Fatalf("list-windows gave a pane with no pid")
		}
		pids = append(pids, w.PID)
	}
	return pids
}

// waitSavedWindows waits until the saved state of a session holds n windows.
func waitSavedWindows(t *testing.T, base, session string, n int) {
	t.Helper()
	path := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "sessions", session+".json")
	deadline := time.Now().Add(uiTimeout)
	got := -1
	for time.Now().Before(deadline) {
		var state struct {
			Windows []json.RawMessage `json:"windows"`
		}
		if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &state) == nil {
			if got = len(state.Windows); got == n {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the saved state of %s never held %d windows, last read %d", session, n, got)
}
