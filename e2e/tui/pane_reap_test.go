package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

// TestKillServerEndsEveryPaneProcess: kill-server ends every process of the
// daemon's panes, also one the pane's hangup does not end. The pane's shell
// starts a child in a process group of its own that ignores SIGHUP, which
// must be gone once kill-server returns. It also starts a child that left
// the pane's session with setsid, which must still run: setsid is how a
// person, the worker of restart-server and a daemon started from a pane
// leave a pane on purpose.
//
// The scope part runs the same with the pane_scope knob, so each pane is in
// a systemd scope of its own. It changes the real user manager, so it runs
// only where TUIOS_E2E_OWN_USER_MANAGER=1 says the machine is kept for tests,
// as TestPaneRunsInItsOwnScope does.
//
// Negative control: see NEGATIVE_CONTROLS.md, "kill-server ends every pane
// process".
func TestKillServerEndsEveryPaneProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads the processes from /proc")
	}
	requireReal(t, "starts processes that must outlive a hangup")
	t.Run("plain", func(t *testing.T) { killServerReaps(t) })
	t.Run("scope", func(t *testing.T) {
		if os.Getenv("TUIOS_E2E_OWN_USER_MANAGER") != "1" {
			t.Skip("changes the real systemd user manager; set TUIOS_E2E_OWN_USER_MANAGER=1 on a machine kept for tests")
		}
		if err := exec.Command("systemctl", "--user", "show", "-p", "Version").Run(); err != nil {
			t.Skipf("no systemd user manager: %v", err)
		}
		t.Setenv(testknob.Env, testknob.Merge(os.Getenv(testknob.Env), "pane_scope=1"))
		killServerReaps(t)
	})
}

// markedPIDs lists the processes whose command line is sleep and mark.
func markedPIDs(mark string) []int {
	var out []int
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, _ := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if len(args) == 2 && filepath.Base(args[0]) == "sleep" && args[1] == mark {
			stat, _ := os.ReadFile("/proc/" + e.Name() + "/stat")
			if i := strings.LastIndexByte(string(stat), ')'); i >= 0 && i+2 < len(stat) && stat[i+2] == 'Z' {
				continue
			}
			out = append(out, pid)
		}
	}
	return out
}

func killServerReaps(t *testing.T) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-reap"
	mustCLI(t, base, "new", sess, "--detach")
	// Marks no other process on the machine has: a sleep of this many
	// seconds, from the test's own pid.
	hupMark := strconv.Itoa(900000 + os.Getpid()%50000*2)
	sidMark := strconv.Itoa(900001 + os.Getpid()%50000*2)
	t.Cleanup(func() {
		// Only the processes this test started.
		for _, m := range []string{hupMark, sidMark} {
			for _, pid := range markedPIDs(m) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	ready := filepath.Join(base, "ready")
	// set -m puts the first job in a process group of its own, and the
	// ignored SIGHUP stays ignored across exec. setsid takes the second out
	// of the pane's session, with its environment as it was.
	cmd := fmt.Sprintf("set -m; (trap '' HUP; exec sleep %s) & setsid sleep %s & touch %s\n", hupMark, sidMark, ready)
	// The shell reads the line once it starts.
	mustCLI(t, base, "send-text", "-s", sess, cmd)
	waitUntil(t, "the pane starts its children", uiTimeout, func() (bool, string) {
		if _, err := os.Stat(ready); err != nil {
			return false, "no ready file yet"
		}
		hup, sid := markedPIDs(hupMark), markedPIDs(sidMark)
		return len(hup) == 1 && len(sid) == 1, fmt.Sprintf("ignores SIGHUP %v, left the session %v", hup, sid)
	})
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	waitUntil(t, "the child that ignores SIGHUP is gone after kill-server", 3*time.Second, func() (bool, string) {
		hup := markedPIDs(hupMark)
		return len(hup) == 0, fmt.Sprintf("ASSERTION: still running after kill-server: ignores SIGHUP %v", hup)
	})
	if sid := markedPIDs(sidMark); len(sid) != 1 {
		t.Errorf("ASSERTION: the child that left the pane's session with setsid did not run on: %v", sid)
	}
}
