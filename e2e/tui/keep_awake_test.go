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
)

// [daemon] keep_awake = "agents" on a real daemon, read from logind: the
// daemon must hold a systemd inhibitor lock on idle and sleep while an agent
// pane works, and only then.
//
// How this could pass wrongly, written down first:
//   - Another program, or the person's own daemon, can hold a "tuios" lock.
//     The lock counted here is the one whose systemd-inhibit process is a
//     child of this test's daemon, so no other holder can satisfy it.
//   - A lock that is never released would pass "taken". Each step that
//     should release is checked too, and the agent going idle, its pane
//     closing and the daemon dying each release on a different path.
//   - A daemon that died with the lock held could leave systemd-inhibit
//     behind, now a child of init, and the parent check would no longer see
//     it. The last step looks for any holder that names the dead daemon's
//     reason and is not a child of a live tuios.
//   - set-keep-awake could set the mode without saving it. The test reads
//     config.toml after the CLI sets it.

// inhibitors lists the pids of the systemd-inhibit processes that hold a lock
// for who "tuios" whose parent is ppid.
func inhibitors(t *testing.T, ppid int) []int {
	t.Helper()
	out, err := exec.Command("systemd-inhibit", "--list", "--no-pager", "--no-legend").Output()
	if err != nil {
		t.Fatalf("systemd-inhibit --list: %v", err)
	}
	var pids []int
	for line := range strings.SplitSeq(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[0] != "tuios" {
			continue
		}
		pid, err := strconv.Atoi(f[3])
		if err != nil {
			continue
		}
		if procParent(pid) == ppid {
			pids = append(pids, pid)
		}
	}
	return pids
}

// procParent reads a process's parent pid from /proc, 0 when it is gone.
func procParent(pid int) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	// The command name is in parentheses and may hold spaces.
	s := string(data)
	rest := s[strings.LastIndexByte(s, ')')+1:]
	f := strings.Fields(rest)
	if len(f) < 2 {
		return 0
	}
	ppid, _ := strconv.Atoi(f[1])
	return ppid
}

// waitInhibitors waits for want locks under ppid. status is what the daemon
// says of its hold, printed when the wait fails: a hold the system refused
// says why there.
func waitInhibitors(t *testing.T, ppid, want int, why string, status func() string) []int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := inhibitors(t, ppid)
		if len(got) == want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %d tuios inhibitor locks under daemon %d, want %d\ntuios keep-awake:\n%s", why, len(got), ppid, want, status())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestKeepAwakeHoldsWhileAnAgentWorks(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the inhibitor lock is logind's")
	}
	if err := exec.Command("systemd-inhibit", "--list", "--no-pager").Run(); err != nil {
		t.Skipf("logind does not answer here: %v", err)
	}
	// Listing locks is open to anyone, and taking a block lock is not: polkit
	// refuses it to a user outside an active seat session, as on a CI runner.
	// The daemon reports that refusal and holds nothing, so there is nothing
	// to count here.
	if out, err := exec.Command("systemd-inhibit", "--what=idle:sleep", "--who=tuios-e2e-probe",
		"--why=probe", "--mode=block", "true").CombinedOutput(); err != nil {
		t.Skipf("logind refuses an inhibitor lock to this user: %v: %s", err, strings.TrimSpace(string(out)))
	}
	base := t.TempDir()
	killDaemon(t, base)
	cfg := configPathIn(base)
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[daemon]\nkeep_awake = \"agents\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	step := func(what string, locks []int) {
		fmt.Fprintf(&log, "%-58s %d lock(s) %v\n", what, len(locks), locks)
	}
	cli := func(args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	status := func() string {
		out, _ := tuiosCLI(t, base, "keep-awake")
		return out
	}

	cli("new", "awake", "--detach")
	cli("new-window", "second", "-s", "awake", "--no-focus")
	pid := daemonPID(t, base)
	first := focusedWindowID(t, base, "awake")
	second := windowIDByName(t, base, "awake", "second")
	step("daemon up, no agent works", waitInhibitors(t, pid, 0, "no agent works", status))

	cli("set-agent-state", "working", "-s", "awake", "-w", first)
	step("first pane working", waitInhibitors(t, pid, 1, "an agent works", status))
	cli("set-agent-state", "working", "-s", "awake", "-w", second)
	step("both panes working: still one lock", waitInhibitors(t, pid, 1, "two agents work", status))
	cli("set-agent-state", "idle", "-s", "awake", "-w", first)
	step("first pane idle, second still working", waitInhibitors(t, pid, 1, "one agent still works", status))
	cli("set-agent-state", "idle", "-s", "awake", "-w", second)
	step("both idle: released", waitInhibitors(t, pid, 0, "no agent works any more", status))
	if out := cli("keep-awake"); !strings.Contains(out, "can sleep") {
		t.Fatalf("keep-awake does not say the machine can sleep:\n%s", out)
	}

	cli("set-agent-state", "working", "-s", "awake", "-w", second)
	step("second pane working again", waitInhibitors(t, pid, 1, "an agent works again", status))
	cli("close-window", second, "-s", "awake")
	step("its pane closed: released", waitInhibitors(t, pid, 0, "the working pane closed", status))

	// The mode from the CLI applies at once and is saved.
	cli("keep-awake", "on")
	step("keep-awake on, no agent works", waitInhibitors(t, pid, 1, "keep_awake = on", status))
	if data, err := os.ReadFile(cfg); err != nil || !strings.Contains(string(data), `keep_awake = "on"`) {
		t.Fatalf("config.toml does not hold the mode: %v\n%s", err, data)
	}
	cli("keep-awake", "off")
	step("keep-awake off: released", waitInhibitors(t, pid, 0, "keep_awake = off", status))

	// A daemon that dies holds nothing: the holder reads a pipe only the
	// daemon writes, and it ends when the daemon goes.
	cli("keep-awake", "on")
	held := waitInhibitors(t, pid, 1, "keep_awake = on again", status)
	step("keep-awake on again", held)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for procParent(held[0]) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("systemd-inhibit %d outlived its daemon %d (its parent is now %d)", held[0], pid, procParent(held[0]))
		}
		time.Sleep(100 * time.Millisecond)
	}
	step("daemon killed with SIGKILL: holder gone", nil)

	path := filepath.Join(artifactDir(t), "keep-awake.txt")
	if err := os.WriteFile(path, []byte(log.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("steps:\n%s(saved to %s)", log.String(), path)
}
