package tuie2e

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// clientWindowGoroutines are the functions that run once per window, or once
// per client, on an attached client. A window the client closes must take its
// own with it.
var clientWindowGoroutines = []string{
	"terminal.(*Window).outputWriter",
	"terminal.(*Window).renderCoalescer",
	"app.(*KittyPassthrough).asyncFrameWriter",
}

// goroutineGroups reads the debug=1 goroutine profile and counts the
// goroutines whose stack names each of fns, plus the total.
func goroutineGroups(addr string, fns []string) (map[string]int, int, error) {
	body, err := httpGet(fmt.Sprintf("http://%s/debug/pprof/goroutine?debug=1", addr))
	if err != nil {
		return nil, 0, err
	}
	m := goroutineCountRe.FindStringSubmatch(body)
	if m == nil {
		return nil, 0, fmt.Errorf("no goroutine total in profile")
	}
	total, _ := strconv.Atoi(m[1])
	counts := make(map[string]int, len(fns))
	// Each group is a block that starts "N @ addr addr ..." and lists its
	// frames, one per line, after it.
	for block := range strings.SplitSeq(body, "\n\n") {
		// The first group follows the profile's own header line.
		if i := strings.Index(block, "\n"); strings.HasPrefix(block, "goroutine profile:") && i >= 0 {
			block = block[i+1:]
		}
		head, _, _ := strings.Cut(block, " @ ")
		n, err := strconv.Atoi(strings.TrimSpace(head))
		if err != nil {
			continue
		}
		for _, fn := range fns {
			if strings.Contains(block, fn) {
				counts[fn] += n
			}
		}
	}
	return counts, total, nil
}

// TestClientClosesWindowGoroutines: an attached client runs an output writer
// and a render coalescer for each window, and one video frame writer for
// itself. Fifty windows are opened and closed through the CLI. Afterwards the
// client must run the same number of each as before, and no more goroutines in
// all than before plus a small margin.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Pane goroutines end when a
// window closes"): with the w.Close() cut from the sync removal of a window,
// the test fails at "the client let go of the goroutines".
func TestClientClosesWindowGoroutines(t *testing.T) {
	const windows = 50
	base := t.TempDir()
	startDaemonWithPprof(t, base)
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	term := startIn(t, base, startOpts{cols: 160, rows: 48, args: []string{"new", "leak", "--pprof", addr}})
	waitBoot(t, term)
	newWindow(t, term)

	read := func() (map[string]int, int, string) {
		t.Helper()
		counts, total, err := goroutineGroups(addr, clientWindowGoroutines)
		if err != nil {
			return nil, 0, err.Error()
		}
		return counts, total, fmt.Sprintf("total %d, %v", total, counts)
	}
	var before map[string]int
	var baseTotal int
	waitUntil(t, "the client served a goroutine profile with one window's writer in it", 30*time.Second, func() (bool, string) {
		c, total, state := read()
		before, baseTotal = c, total
		return c != nil && c[clientWindowGoroutines[0]] == 1, state
	})
	t.Logf("before: %d goroutines, %v", baseTotal, before)

	var opened []string
	for i := range windows {
		out, err := tuiosCLI(t, base, "new-window", "-s", "leak", fmt.Sprintf("w%d", i))
		if err != nil {
			t.Fatalf("new-window %d: %v\n%s", i, err, out)
		}
	}
	waitUntil(t, "the client runs a coalescer for every window it opened", time.Minute, func() (bool, string) {
		c, _, state := read()
		return c != nil && c[clientWindowGoroutines[1]] == windows+1, state
	})
	_, peak, _ := read()
	t.Logf("with %d more windows: %d goroutines", windows, peak)

	ids := windowIDs(t, base, "leak")
	keep := ids[0]
	for _, id := range ids[1:] {
		opened = append(opened, id)
		if out, err := tuiosCLI(t, base, "close-window", "-s", "leak", id); err != nil {
			t.Fatalf("close-window %s: %v\n%s", id, err, out)
		}
	}
	waitUntil(t, "the session is back to one window", time.Minute, func() (bool, string) {
		ids := windowIDs(t, base, "leak")
		return len(ids) == 1 && ids[0] == keep, fmt.Sprintf("%d windows", len(ids))
	})

	// The margin is for goroutines that come and go on their own, such as a
	// command bubbletea is running at the time of the read. A leak of one per
	// window is fifty.
	const margin = 10
	waitUntil(t, fmt.Sprintf("the client let go of the goroutines of the %d windows it closed", len(opened)), time.Minute, func() (bool, string) {
		c, total, state := read()
		if c == nil {
			return false, state
		}
		for _, fn := range clientWindowGoroutines {
			if c[fn] != before[fn] {
				return false, state + fmt.Sprintf(" (before: total %d, %v)", baseTotal, before)
			}
		}
		return total <= baseTotal+margin, state + fmt.Sprintf(" (before: total %d, %v)", baseTotal, before)
	})
	_, after, _ := read()
	t.Logf("after closing them: %d goroutines (before %d, peak %d)", after, baseTotal, peak)
}
