package tuie2e

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestIdleDaemonEventRingIsSmall: the daemon's event hub keeps the last
// lifecycle and agent events for a subscriber that resumes. The ring grows as
// events arrive. Allocated whole up front, its 4096 slots were 1.8 MB of an
// idle daemon's 6.3 MB live heap.
//
// The heap profile samples one allocation per 512 KB on average, so a
// 1.8 MB slice is in it with a chance of about 97 in 100, and a ring of a
// few events almost never.
//
// NEGATIVE CONTROL (e2e/tui/NEGATIVE_CONTROLS.md,
// TestIdleDaemonEventRingIsSmall): with the ring made whole in
// newEventHubSize, the test fails at "holds 1792 KiB".
func TestIdleDaemonEventRingIsSmall(t *testing.T) {
	requireReal(t, "reads the heap of the real daemon")
	base := t.TempDir()
	killDaemon(t, base)
	addr := startDaemonWithPprof(t, base)
	if out, err := tuiosCLI(t, base, "new", "ring", "--detach"); err != nil {
		t.Fatalf("create a session: %v: %s", err, out)
	}
	body, err := httpGet(fmt.Sprintf("http://%s/debug/pprof/heap?gc=1&debug=1", addr))
	if err != nil {
		t.Fatalf("read the daemon heap profile: %v", err)
	}
	held := heapHeldBy(body, "session.newEventHubSize", "(*eventHub).retain")
	t.Logf("the idle daemon's event ring holds %d KiB", held/1024)
	if held >= 512<<10 {
		t.Fatalf("ASSERTION: the idle daemon's event ring holds %d KiB, want under 512 KiB", held/1024)
	}
}

// heapHeldBy sums the in-use bytes of the records in a debug=1 heap profile
// whose stack names one of funcs.
func heapHeldBy(profile string, funcs ...string) uint64 {
	var total, bytes uint64
	counted := false
	for line := range strings.SplitSeq(profile, "\n") {
		if m := heapRecord.FindStringSubmatch(line); m != nil {
			bytes, _ = strconv.ParseUint(m[1], 10, 64)
			counted = false
			continue
		}
		if counted || !strings.HasPrefix(line, "#\t") {
			continue
		}
		for _, f := range funcs {
			if strings.Contains(line, f) {
				total += bytes
				counted = true
				break
			}
		}
	}
	return total
}

var heapRecord = regexp.MustCompile(`^\d+: (\d+) \[\d+: \d+\] @`)
