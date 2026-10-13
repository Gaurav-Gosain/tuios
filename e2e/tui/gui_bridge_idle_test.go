package tuie2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// procTicks is a process's user and system CPU time, in clock ticks.
func procTicks(t *testing.T, pid int) int64 {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		t.Skipf("no /proc here: %v", err)
	}
	// The command name is in parentheses and may hold spaces.
	f := strings.Fields(string(b[strings.LastIndexByte(string(b), ')')+2:]))
	utime, _ := strconv.ParseInt(f[11], 10, 64)
	stime, _ := strconv.ParseInt(f[12], 10, 64)
	return utime + stime
}

// procReads is how many read calls a process has made, from /proc.
func procReads(t *testing.T, pid int) int64 {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "io"))
	if err != nil {
		t.Skipf("no /proc io here: %v", err)
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "syscr:"); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			return n
		}
	}
	t.Fatalf("no syscr in /proc/%d/io", pid)
	return 0
}

// TestGUIBridgeIdleCostsLittle: a bridge on a session of idle shells, with no
// hosts configured, does almost nothing. Its own CPU over 10 s stays under a
// budget, and it asks the daemon nothing, so the daemon's read calls barely
// move (about 18 read calls in 10 s with the fix, 35 to 40 with a poll a
// second). Then a host added from the command line reaches the renderer by the
// daemon's push, without a poll.
//
// What fails it: the palette key printed from the whole config on every
// message (78 to 94 ticks per 10 s before the fix), and list-hosts read every
// second with no hosts (ten verb calls the daemon reads in 10 s).
func TestGUIBridgeIdleCostsLittle(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	env := []string{"TUIOS_SSH=" + ssh}
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "start-server"); err != nil {
		t.Fatalf("start-server: %v\n%s", err, out)
	}
	b := startBridgeWith(t, base, bridgeOpts{args: []string{"--session", "idle"}, env: env, cols: 120, rows: 40, keepDaemon: true, name: "idle"})
	b.tiledPanes(2)
	daemon := daemonPID(t, base)
	time.Sleep(3 * time.Second)

	const window = 10 * time.Second
	ticks0, reads0 := procTicks(t, b.pid), procReads(t, daemon)
	time.Sleep(window)
	ticks, reads := procTicks(t, b.pid)-ticks0, procReads(t, daemon)-reads0
	t.Logf("over %v idle: bridge %d CPU ticks, daemon %d read calls", window, ticks, reads)
	saveTransferArtifact(t, "gui-bridge-idle", map[string]any{"bridge_ticks": ticks, "daemon_reads": reads, "seconds": window.Seconds()})
	if ticks > 30 {
		t.Errorf("ASSERTION: the idle bridge took %d CPU ticks in %v, budget 30", ticks, window)
	}
	if reads > 26 {
		t.Errorf("ASSERTION: the daemon made %d read calls in %v for an idle bridge with no hosts, budget 26: the bridge is polling it", reads, window)
	}

	// A host added now comes to the renderer by the daemon's push.
	if out, err := tuiosCLIEnv(t, base, env, "hosts", "add", "build", "someone@buildbox", "--command", tuiosBin, "--connect-timeout", "5"); err != nil {
		t.Fatalf("hosts add: %v\n%s", err, out)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		ev := eventsOf[wireHosts](b, "hosts", "hosts")
		found := false
		if len(ev) > 0 {
			for _, h := range ev[len(ev)-1].Hosts {
				if h.Name == "build" {
					found = true
				}
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the host added from the command line never reached the renderer")
		}
		time.Sleep(100 * time.Millisecond)
	}
	b.saveLog("idle-costs-little")
}
