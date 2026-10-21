//go:build linux

package herdrplugin

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestOwnerForgetsAnEmptiedGroup is the pid reuse case from the review of
// #956, as a security boundary test. A native plugin's command leads session
// S, leaves a child in its group and exits. The child ends. Then a stranger
// gets pid S and daemonizes the usual way: setsid, fork, the leader exits.
// Its child is in session S and group S, and pid S is free. Unless the
// runner dropped the entry when group S emptied, Owner counts the stranger
// as the plugin.
//
// Reusing a pid on purpose needs a pid namespace of its own, so the test
// runs itself again as pid 1 under unshare, and skips where user namespaces
// are not allowed.
func TestOwnerForgetsAnEmptiedGroup(t *testing.T) {
	if os.Getpid() != 1 {
		if os.Getenv("TUIOS_TEST_PIDNS") != "" {
			t.Fatal("unshare did not make this process pid 1")
		}
		unshare, err := exec.LookPath("unshare")
		if err != nil {
			t.Skip("needs unshare")
		}
		cmd := exec.Command(unshare, "-Urpf", "--mount-proc", os.Args[0], "-test.run=^TestOwnerForgetsAnEmptiedGroup$", "-test.v")
		cmd.Env = append(os.Environ(), "TUIOS_TEST_PIDNS=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			if !strings.Contains(string(out), "--- FAIL") {
				t.Skipf("no pid namespace here: %v\n%s", err, out)
			}
			t.Fatalf("in a pid namespace:\n%s", out)
		}
		t.Logf("in a pid namespace:\n%s", out)
		return
	}

	r := NewRunner()
	p := &Plugin{PluginID: "e2e.admin", Format: FormatTuios, PluginRoot: t.TempDir()}
	if _, perr := r.Start(Job{Plugin: p, Command: []string{"sh", "-c", "sleep 1 & exit 0"}}); perr != nil {
		t.Fatal(perr)
	}
	sid := 0
	waitFor(t, "the leader to exit with its entry kept", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for s, o := range r.owners {
			if o.exited {
				sid = s
				return true
			}
		}
		return false
	})
	// As pid 1, this process inherits the orphaned sleep and must reap it.
	waitFor(t, "the leader's group to empty", func() bool {
		for {
			if pid, _ := syscall.Wait4(-1, nil, syscall.WNOHANG, nil); pid <= 0 {
				break
			}
		}
		return !groupAlive(sid)
	})
	// Nobody asks Owner in between. The sweep alone has to drop the entry.
	time.Sleep(4 * sweepEvery)
	r.mu.Lock()
	_, kept := r.owners[sid]
	r.mu.Unlock()
	if kept {
		t.Errorf("the entry of session %d outlived its process group", sid)
	}

	if err := os.WriteFile("/proc/sys/kernel/ns_last_pid", []byte(strconv.Itoa(sid-1)), 0o644); err != nil {
		t.Skipf("cannot choose the next pid: %v", err)
	}
	stranger := exec.Command("sh", "-c", "sleep 30 </dev/null >/dev/null 2>&1 & echo $!")
	stranger.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := stranger.Output()
	if err != nil {
		t.Fatal(err)
	}
	child, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	defer func() { _ = syscall.Kill(child, syscall.SIGKILL) }()
	if stranger.Process.Pid != sid {
		t.Skipf("the stranger got pid %d, not %d", stranger.Process.Pid, sid)
	}
	if got := r.Owner(sid); got != nil {
		t.Errorf("a stranger's daemon (pid %d, session %d) is counted as plugin %s", child, sid, got.PluginID)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
