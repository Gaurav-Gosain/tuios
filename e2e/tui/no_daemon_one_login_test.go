package tuie2e

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestAHostWithNoDaemonCostsOneLogin is issue #1820. A linked host that is up
// with no tuios daemon on it was dialed again every minute, each time with a
// new ssh and so a new key authentication, and a person whose SSH agent asks
// for each one (1Password) was asked every few minutes. The link now keeps
// the one ssh it has and asks the host again over it.
//
// Each run of the ssh stand-in is one login. The test counts them.
//
// How this could pass wrongly, written down first:
//   - The count could be read before a redial would happen. On main the
//     second ssh runs one second after the first, and the test waits 20s.
//   - The link could stop asking at all, and so never see the daemon come.
//     The far daemon is started, and the host must go up with no new login.
//   - The link could come up only by a new dial that the hosts command
//     started. The count must not grow when the host goes up.
//   - Holding the ssh could hide a dropped one, or redial it in a loop. The
//     held ssh is killed: one more login, then held again.
//
// The listings and the count are saved under artifactDir.
//
// Negative control: on origin/main 8915c61d1 the count is 4 or more after
// 20s, and the first assertion fails.
func TestAHostWithNoDaemonCostsOneLogin(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t) // no daemon there yet
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the hub daemon: %v\n%s", err, out)
	}
	logins := func() int {
		b, _ := os.ReadFile(fakeSSHPids(remote))
		return len(strings.Fields(string(b)))
	}
	listing := waitForHostListing(t, base, func(s string) bool {
		return strings.Contains(s, "no_daemon")
	}, "the hub never reported build with no daemon")
	saveSyncArtifact(t, "hosts-no-daemon.txt", listing)

	time.Sleep(20 * time.Second)
	n := logins()
	saveSyncArtifact(t, "logins-after-20s.txt", strings.Repeat("ssh\n", n))
	if n != 1 {
		t.Fatalf("ASSERTION: the hub ran ssh %d times in 20s to a host with no daemon, want 1: each run is a new login", n)
	}
	if out, _ := tuiosCLI(t, base, "hosts"); !strings.Contains(out, "no_daemon") {
		t.Fatalf("ASSERTION: build should still list as no_daemon:\n%s", out)
	}

	// The held ssh drops (the network, a sleep). The link dials again under
	// its backoff, which is one more login, and holds the new ssh.
	pids, _ := os.ReadFile(fakeSSHPids(remote))
	pid, err := strconv.Atoi(strings.Fields(string(pids))[0])
	if err != nil {
		t.Fatalf("read the ssh pid: %v", err)
	}
	// The stand-in's shell execs the far command on macOS. Linux's dash runs
	// it as a child, which then still holds the link's pipes, so the whole
	// tree under the recorded pid is ended.
	killTree(t, pid)
	deadline := time.Now().Add(30 * time.Second)
	for logins() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the held ssh was ended and the hub did not dial again in 30s: %d ssh runs in all, want 2", logins())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n := logins(); n != 2 {
		t.Fatalf("ASSERTION: after the held ssh dropped the hub ran ssh %d times in all, want 2: one redial, then held again", n)
	}

	// The daemon starts on build. The next ask, which the hosts command makes
	// at once, finds it over the same ssh.
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("start the far daemon: %v\n%s", err, out)
	}
	up := waitForHostListing(t, base, func(s string) bool {
		return strings.Contains(s, "build") && strings.Contains(s, " up ")
	}, "build never came up after its daemon started")
	saveSyncArtifact(t, "hosts-up.txt", up)
	if n := logins(); n != 2 {
		t.Fatalf("ASSERTION: build came up after %d ssh runs, want 2: the link dialed again instead of asking over its ssh", n)
	}
}

// killTree ends pid and every process under it, children first.
func killTree(t *testing.T, pid int) {
	t.Helper()
	out, _ := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	for _, f := range strings.Fields(string(out)) {
		if child, err := strconv.Atoi(f); err == nil {
			killTree(t, child)
		}
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		t.Fatalf("end process %d: %v", pid, err)
	}
}
