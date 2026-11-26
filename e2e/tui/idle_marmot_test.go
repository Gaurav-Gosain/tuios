package tuie2e

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// marmotWakeups is tuiosWakeups for a binary that marmot built, which has no
// runtime trace. The client was started with MARMOT_TRACE=wakeups=dir/, so
// its runtime writes a record to dir/wakeups.PID each time one of its threads
// wakes from a sleep. This waits d, and counts the wakeups in that window
// whose site is tuios, Bubble Tea or ultraviolet code: the code that made the
// timer or readied the goroutine. Sites of the runtime alone (the scheduler,
// the GC, the poller) are not counted, as the Go trace path does not count
// the runtime's own goroutines. The window is read with marmot prof (the
// marmot binary on PATH, or MARMOT), which names the sites from the binary's
// pc table.
func marmotWakeups(t *testing.T, pid int, dir string, d time.Duration) (int, []string) {
	t.Helper()
	path := filepath.Join(dir, "wakeups."+strconv.Itoa(pid))
	from := monotonicNow(t)
	time.Sleep(d)
	to := monotonicNow(t)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the trace endpoint says %q, and the client wrote no wakeup trace: %v", traceUnsupported, err)
	}
	// The header: "MRMTWAKE", u32 version, u32 pid, u64 image base, and at
	// offset 24 the runtime's start on CLOCK_MONOTONIC, which the records'
	// times and the window count from.
	if len(data) < 32 || string(data[:8]) != "MRMTWAKE" {
		t.Fatalf("%s is not a marmot wakeup trace", path)
	}
	started := binary.LittleEndian.Uint64(data[24:])
	if started > from {
		t.Fatalf("the wakeup trace starts at %d ns, after the window at %d ns", started, from)
	}

	marmot := os.Getenv("MARMOT")
	if marmot == "" {
		marmot = "marmot"
	}
	cmd := exec.Command(marmot, "prof", "-wakeups", "-json", "-trace", path,
		"-settle", fmt.Sprintf("%dns", from-started), "-window", fmt.Sprintf("%dns", to-from), tuiosBin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("read the wakeup trace with %s: %v\n%s", cmd, err, out)
	}
	var rep struct {
		Total int `json:"total"`
		Sites []struct {
			Cause    string `json:"cause"`
			Function string `json:"function"`
			File     string `json:"file"`
			Line     int    `json:"line"`
			Count    int    `json:"count"`
		} `json:"sites"`
	}
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("parse marmot prof -wakeups -json: %v\n%s", err, out)
	}
	named := false
	total := 0
	var sites []string
	for _, s := range rep.Sites {
		named = named || s.Function != ""
		if !tuiosFunc(s.Function) || pprofFunc(s.Function) {
			continue
		}
		total += s.Count
		sites = append(sites, fmt.Sprintf("%d at %s (%s:%d), %s", s.Count, s.Function, s.File, s.Line, s.Cause))
	}
	// Without a pc table no site has a name, and none would count as tuios
	// code: the check would pass whatever woke the client.
	if rep.Total > 0 && !named {
		t.Fatalf("marmot prof named no site of %d wakeups: the binary has no pc table, so build it without -s", rep.Total)
	}
	slices.Sort(sites)
	t.Logf("no runtime trace in this build: read the marmot wakeup trace, %d wakeups in the window, %d in tuios code", rep.Total, total)
	return total, sites
}

// monotonicNow is CLOCK_MONOTONIC in nanoseconds, the clock of the marmot
// wakeup trace.
func monotonicNow(t *testing.T) uint64 {
	t.Helper()
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		t.Fatalf("read the monotonic clock: %v", err)
	}
	return uint64(ts.Nano())
}
