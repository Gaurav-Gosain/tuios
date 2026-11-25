package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestIdleGivesHeapBack: an idle client and its daemon each run a trim
// (internal/memtrim) and give their free heap pages back to the system. At
// 207x55 with three shells the client keeps 5 to 9 MB of garbage and free
// pages past its live heap, and the daemon about as much. A threshold above
// that never trims, and both processes keep the memory for as long as they
// live.
//
// Each process must fall at least 1 MiB below a peak of its private memory,
// through a trim: NumForcedGC counts the trims, since nothing else in tuios
// forces a collection and the profile is read without gc=1.
//
// Most of what a trim gives back here is left by the start of the shells: 4.1
// to 6.4 MB past the live heap. The burst alone leaves 1.7 to 2.3 MB, around
// memtrim's floor, and where the last collection fell decides the amount. A
// process can trim after the setup and before the burst, as a slow runner's
// daemon does, or once after both. So the test watches both processes from
// their start and counts the largest fall below the highest point so far, at
// any time.
//
// NEGATIVE CONTROL (e2e/tui/NEGATIVE_CONTROLS.md, TestIdleGivesHeapBack):
// with minExcess back at 32 MB, the test fails at "did not both give 1 MiB of
// their heap back".
func TestIdleGivesHeapBack(t *testing.T) {
	requireReal(t, "reads the heap of the real client and daemon")
	// The daemon looks for a trim at the end of its cleanup pass, which
	// runs this long after the last activity.
	t.Setenv("TUIOS_CLEANUP_SECONDS", "2")
	base := t.TempDir()
	killDaemon(t, base)
	daemonAddr := startDaemonWithPprof(t, base)
	pidFile := filepath.Join(base, "XDG_RUNTIME_DIR", "tuios", "tuios.sock.pid")
	daemonPid := 0
	waitUntil(t, "the daemon wrote no pid file", 10*time.Second, func() (bool, string) {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return false, err.Error()
		}
		daemonPid, err = strconv.Atoi(strings.TrimSpace(string(raw)))
		return err == nil, string(raw)
	})
	clientAddr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	client := &trimWatch{name: "client", addr: clientAddr}
	procs := []*trimWatch{client, {name: "daemon", addr: daemonAddr, pid: daemonPid}}
	// Both processes are watched in /proc from their start, which costs them
	// nothing, so each peak is seen whichever process trims first, and
	// whether it trims before the burst or after it. A heap profile
	// allocates in the process it reads, and read in a loop it keeps the
	// process from counting as quiet.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			for _, p := range procs {
				p.sample()
			}
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()
	defer func() {
		close(stop)
		<-done
	}()

	term := startIn(t, base, startOpts{
		cols: perfCols, rows: perfRows,
		args: []string{"new", "mem", "--pprof", clientAddr},
		env:  []string{"TUIOS_CLEANUP_SECONDS=2"},
	})
	client.setPid(term.Pid())
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

	waitUntil(t, "the idle client and daemon did not both give 1 MiB of their heap back", 30*time.Second, func() (bool, string) {
		all := true
		var state []string
		for _, p := range procs {
			gave := p.gaveBack()
			all = all && gave
			state = append(state, p.String())
		}
		return all, strings.Join(state, "\n")
	})
	// The free pages kept are not checked: a burst after a trim can leave
	// up to memtrim's floor behind, which it keeps by design.
	for _, p := range procs {
		ms, err := memStats(p.addr)
		if err != nil {
			t.Fatalf("read the %s heap: %v", p.name, err)
		}
		t.Logf("%s after %d trim(s): live %d KiB, allocated %d KiB, in use %d KiB, free pages kept %d KiB, returned %d KiB",
			p.name, ms["NumForcedGC"], ms["NextGC"]/2/1024, ms["HeapAlloc"]/1024, ms["HeapInuse"]/1024, (ms["HeapIdle"]-ms["HeapReleased"])/1024, ms["HeapReleased"]/1024)
		if ms["NumForcedGC"] == 0 {
			t.Fatalf("ASSERTION: the %s gave memory back with no trim", p.name)
		}
	}
}

// trimWatch follows one process's private memory: the highest it has reached
// so far, and the largest fall below that.
type trimWatch struct {
	name, addr string
	pid        int

	mu         sync.Mutex
	peak, fell uint64
}

// setPid starts the watch of a process that started after the sampling.
func (w *trimWatch) setPid(pid int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pid = pid
}

func (w *trimWatch) sample() {
	w.mu.Lock()
	pid := w.pid
	w.mu.Unlock()
	if pid == 0 {
		return
	}
	now := privateDirty(pid)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.peak = max(w.peak, now)
	w.fell = max(w.fell, w.peak-now)
}

// gaveBack reports whether the process fell 1 MiB or more below a peak.
func (w *trimWatch) gaveBack() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fell >= 1<<20
}

func (w *trimWatch) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return fmt.Sprintf("%s: private dirty peak %d KiB, largest fall below a peak %d KiB", w.name, w.peak/1024, w.fell/1024)
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
