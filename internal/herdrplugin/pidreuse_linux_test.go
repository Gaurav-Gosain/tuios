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
// #956, as a security boundary test. A native plugin's command runs under
// the reaper, which leads session S. The command leaves a child and exits:
// the session is still the plugin's. The child ends, and the reaper exits.
// Then a stranger gets pid S and daemonizes the usual way: setsid, fork,
// the leader exits. Its child is in session S, and pid S is free. Unless
// the runner dropped the entry when the reaper exited, Owner counts the
// stranger as the plugin.
//
// Reusing a pid on purpose needs a pid namespace of its own, so the test
// runs itself again as pid 1 under unshare, and skips where user namespaces
// are not allowed.
func TestOwnerForgetsAnEmptiedGroup(t *testing.T) {
	if os.Getpid() != 1 {
		if os.Getenv("TUIOS_TEST_PIDNS") != "" {
			t.Fatal("unshare did not make this process pid 1")
		}
		// Under CI the workflow allows user namespaces, and a skip there
		// would hide that the test never ran.
		skip := t.Skipf
		if os.Getenv("CI") != "" {
			skip = t.Fatalf
		}
		unshare, err := exec.LookPath("unshare")
		if err != nil {
			skip("needs unshare")
			return
		}
		cmd := exec.Command(unshare, "-Urpf", "--mount-proc", os.Args[0], "-test.run=^TestOwnerForgetsAnEmptiedGroup$", "-test.v")
		cmd.Env = append(os.Environ(), "TUIOS_TEST_PIDNS=1")
		out, err := cmd.CombinedOutput()
		switch {
		case strings.Contains(string(out), "--- FAIL"):
			t.Fatalf("in a pid namespace:\n%s", out)
		case err != nil || !strings.Contains(string(out), "--- PASS: TestOwnerForgetsAnEmptiedGroup"):
			skip("the test did not run in a pid namespace: %v\n%s", err, out)
			return
		}
		t.Logf("in a pid namespace:\n%s", out)
		return
	}

	r := NewRunner()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r.Reaper = []string{self, ReaperArg}
	p := &Plugin{PluginID: "e2e.admin", Format: FormatTuios, PluginRoot: t.TempDir()}
	if _, perr := r.Start(Job{Plugin: p, Command: []string{"sh", "-c", "sleep 2 & exit 0"}}); perr != nil {
		t.Fatal(perr)
	}
	sid := 0
	r.mu.Lock()
	for s := range r.owners {
		sid = s
	}
	r.mu.Unlock()
	// The command exits at once. What it left still runs, so the session
	// is still the plugin's.
	time.Sleep(500 * time.Millisecond)
	if got := r.Owner(sid); got != p {
		t.Fatalf("while the command's child runs, session %d belongs to %v, want the plugin", sid, got)
	}
	// The child ends. The reaper exits with it, and the entry goes then,
	// with nobody asking Owner and no timer.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		r.mu.Lock()
		_, kept := r.owners[sid]
		r.mu.Unlock()
		if !kept {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("the entry of session %d outlived the reaper", sid)
			break
		}
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
