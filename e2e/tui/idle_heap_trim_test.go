package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestIdleGivesHeapBack: after a burst of output, an idle client and its
// daemon each run one trim (internal/memtrim) and give their free heap pages
// back to the system. At 207x55 with three shells the client keeps 5 to 9 MB
// of garbage and free pages past its live heap, and the daemon about as much.
// A threshold above that never trims, and both processes keep the memory for
// as long as they live.
//
// Each process must give at least 2 MiB of private memory back, through a
// trim: NumForcedGC counts the trims, since nothing else in tuios forces a
// collection and the profile is read without gc=1. After the trim the free
// pages not yet returned must be under 1 MB.
//
// NEGATIVE CONTROL (e2e/tui/NEGATIVE_CONTROLS.md, "Idle heap trim"): with
// minExcess back at 32 MB, the test fails at "the idle client never gave
// 2 MiB of its heap back".
func TestIdleGivesHeapBack(t *testing.T) {
	requireReal(t, "reads the heap of the real client and daemon")
	// The daemon looks for a trim at the end of its cleanup pass, which
	// runs this long after the last activity.
	t.Setenv("TUIOS_CLEANUP_SECONDS", "2")
	base := t.TempDir()
	killDaemon(t, base)
	daemonAddr := startDaemonWithPprof(t, base)
	clientAddr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	term := startIn(t, base, startOpts{
		cols: perfCols, rows: perfRows,
		args: []string{"new", "mem", "--pprof", clientAddr},
		env:  []string{"TUIOS_CLEANUP_SECONDS=2"},
	})
	waitBoot(t, term)
	for i := 1; i <= 3; i++ {
		newWindow(t, term)
		if i == 1 {
			enableTiling(t, term)
		}
	}
	waitWindowCount(t, term, 3, "opening three shells")

	ids := windowIDs(t, base, "mem")
	if out, err := tuiosCLI(t, base, "send-text", "-s", "mem", "-w", ids[0], "seq 1 30000; echo BURST$((1+1))DONE\n"); err != nil {
		t.Fatalf("send-text: %v: %s", err, out)
	}
	waitForCapture(t, base, nil, []string{"-s", "mem", "-w", ids[0], "--lines", "3"}, "BURST2DONE")

	raw, err := os.ReadFile(filepath.Join(base, "XDG_RUNTIME_DIR", "tuios", "tuios.sock.pid"))
	if err != nil {
		t.Fatalf("read the daemon pid: %v", err)
	}
	daemonPid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse the daemon pid %q: %v", raw, err)
	}
	for _, p := range []struct {
		name, addr string
		pid        int
	}{{"client", clientAddr, term.Pid()}, {"daemon", daemonAddr, daemonPid}} {
		// The trim is watched in /proc, which costs the process nothing. A
		// heap profile allocates in the process it reads, and read in a
		// loop it keeps the process from counting as quiet.
		var peak uint64
		waitUntil(t, fmt.Sprintf("the idle %s never gave 2 MiB of its heap back after the burst", p.name), 30*time.Second, func() (bool, string) {
			now := privateDirty(p.pid)
			peak = max(peak, now)
			return now+2<<20 <= peak, fmt.Sprintf("private dirty %d KiB, peak %d KiB", now/1024, peak/1024)
		})
		ms, err := memStats(p.addr)
		if err != nil {
			t.Fatalf("read the %s heap: %v", p.name, err)
		}
		kept := ms["HeapIdle"] - ms["HeapReleased"]
		t.Logf("%s after %d trim(s): live %d KiB, allocated %d KiB, in use %d KiB, free pages kept %d KiB, returned %d KiB",
			p.name, ms["NumForcedGC"], ms["NextGC"]/2/1024, ms["HeapAlloc"]/1024, ms["HeapInuse"]/1024, kept/1024, ms["HeapReleased"]/1024)
		if ms["NumForcedGC"] == 0 {
			t.Fatalf("ASSERTION: the %s gave memory back with no trim", p.name)
		}
		if kept >= 1<<20 {
			t.Fatalf("ASSERTION: the %s trimmed but kept %d KiB of free pages, want under 1024 KiB", p.name, kept/1024)
		}
	}
}

// privateDirty is the process's private dirty memory in bytes, from
// smaps_rollup: its heap, stacks and runtime data, without the binary.
func privateDirty(pid int) uint64 {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/smaps_rollup", pid))
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "Private_Dirty:" {
			n, _ := strconv.ParseUint(f[1], 10, 64)
			return n << 10
		}
	}
	return 0
}

var memStatLine = regexp.MustCompile(`(?m)^# (\w+) = (\d+)$`)

// memStats reads the runtime.MemStats lines of a heap profile, without the
// collection gc=1 would force.
func memStats(addr string) (map[string]uint64, error) {
	body, err := httpGet(fmt.Sprintf("http://%s/debug/pprof/heap?debug=1", addr))
	if err != nil {
		return nil, err
	}
	ms := map[string]uint64{}
	for _, m := range memStatLine.FindAllStringSubmatch(body, -1) {
		n, err := strconv.ParseUint(m[2], 10, 64)
		if err != nil {
			continue
		}
		ms[m[1]] = n
	}
	if _, ok := ms["NumForcedGC"]; !ok {
		return nil, fmt.Errorf("no NumForcedGC in the profile")
	}
	return ms, nil
}
