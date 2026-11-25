package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestIdleGivesHeapBack: an idle client and its daemon run a trim
// (internal/memtrim) after a burst of output and give their free heap pages
// back to the system. At 207x55 with three shells the client keeps 5 to 9 MB
// of garbage and free pages past its live heap, and the daemon about as much.
// A threshold above that never trims, and both processes keep the memory for
// as long as they live.
//
// Both processes run with GOGC=off, so a trim is the only collection. Where a
// collection of the runtime's own fell in the burst would decide how much
// garbage is left at its end: after the start trim, 1.3 to 2.1 MB with GOGC
// at 100, around memtrim's floor. With GOGC=off the burst leaves all it
// allocated.
//
// The test sets the order. It waits for the trim of what the start left, and
// only then sends the burst. The trim after the burst comes inside memtrim's
// 30 s minimum interval, so it checks that a look inside the interval waits
// for the interval to end. A daemon asks for a trim only while its cleanup
// runs, which stops soon after the output does: a look that gave up left the
// burst untrimmed for good.
//
// After the burst each process must count a new trim in NumForcedGC and
// fall at least 1 MiB below its peak. Nothing else in tuios forces a
// collection, and the profile is read without gc=1.
//
// NEGATIVE CONTROL (e2e/tui/NEGATIVE_CONTROLS.md, TestIdleGivesHeapBack):
// with minExcess back at 32 MB, the test fails at "did not trim after the
// start". With a look inside the interval that returns, it fails at "did not
// both trim again after the burst".
func TestIdleGivesHeapBack(t *testing.T) {
	requireReal(t, "reads the heap of the real client and daemon")
	if runtime.GOOS != "linux" {
		t.Skip("the trim watch reads /proc/PID/smaps_rollup, a Linux file")
	}
	// The daemon looks for a trim at the end of its cleanup pass, which
	// runs this long after the last activity.
	t.Setenv("TUIOS_CLEANUP_SECONDS", "2")
	t.Setenv("GOGC", "off")
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
		env:  []string{"TUIOS_CLEANUP_SECONDS=2", "GOGC=off"},
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

	waitUntil(t, "the idle client and daemon did not trim after the start", 20*time.Second, func() (bool, string) {
		return allTrimmed(t, procs, map[string]uint64{})
	})
	trims := map[string]uint64{}
	for _, p := range procs {
		trims[p.name] = p.trimCount(t, true)
		p.reset()
	}

	ids := windowIDs(t, base, "mem")
	if out, err := tuiosCLI(t, base, "send-text", "-s", "mem", "-w", ids[0], "seq 1 30000; echo BURST$((1+1))DONE\n"); err != nil {
		t.Fatalf("send-text: %v: %s", err, out)
	}
	waitForCapture(t, base, nil, []string{"-s", "mem", "-w", ids[0], "--lines", "3"}, "BURST2DONE")

	// The trim after the burst waits for the end of the 30 s interval that
	// the start trim began, and then for a 2 s settle.
	waitUntil(t, "the idle client and daemon did not both trim again after the burst", 45*time.Second, func() (bool, string) {
		return allTrimmed(t, procs, trims)
	})
	for _, p := range procs {
		ms, err := memStats(p.addr)
		if err != nil {
			t.Fatalf("read the %s heap: %v", p.name, err)
		}
		t.Logf("%s, %d trim(s): allocated %d KiB, in use %d KiB, free pages kept %d KiB, returned %d KiB",
			p, ms["NumForcedGC"], ms["HeapAlloc"]/1024, ms["HeapInuse"]/1024, (ms["HeapIdle"]-ms["HeapReleased"])/1024, ms["HeapReleased"]/1024)
	}
}

// allTrimmed reports whether every process fell 1 MiB below its peak and ran
// a trim past the count in before. A fall alone does not show a trim: the
// daemon's private memory fell 5 MB once with none, outside the Go heap. The
// count is read only after the fall, since a read allocates in the process.
func allTrimmed(t *testing.T, procs []*trimWatch, before map[string]uint64) (bool, string) {
	all := true
	var state []string
	for _, p := range procs {
		var n uint64
		if p.gaveBack() {
			n = p.trimCount(t, false)
		}
		all = all && n > before[p.name]
		state = append(state, fmt.Sprintf("%s, %d trim(s) seen, %d before", p, n, before[p.name]))
	}
	return all, strings.Join(state, "\n")
}

// trimWatch follows one process's private memory: the highest it has reached
// since the last reset, and the largest fall below that.
type trimWatch struct {
	name, addr string
	pid        int

	mu         sync.Mutex
	peak, fell uint64

	trims  uint64
	readAt time.Time
}

// trimCount is the process's count of forced collections, its trims. It
// reads the heap profile at most once in 3 s unless now is set: a profile
// allocates in the process it reads, and read faster it keeps the process
// from counting as quiet.
func (w *trimWatch) trimCount(t *testing.T, now bool) uint64 {
	t.Helper()
	if !now && time.Since(w.readAt) < 3*time.Second {
		return w.trims
	}
	ms, err := memStats(w.addr)
	if err != nil {
		t.Fatalf("read the %s heap: %v", w.name, err)
	}
	w.trims, w.readAt = ms["NumForcedGC"], time.Now()
	return w.trims
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

// reset forgets the peak and the fall, so the next sample starts again.
func (w *trimWatch) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.peak, w.fell = 0, 0
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
