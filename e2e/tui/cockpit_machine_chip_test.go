package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// ckHostGlyph is the mark the agents view puts before a machine's name.
const ckHostGlyph = "\U000f048b"

// How TestCockpitMachineChip could pass wrongly, written down first:
//   - Every row could keep a machine label, this machine's too, which says
//     nothing and hides the session. The local rows must carry no machine
//     mark at all.
//   - A remote row could lose its machine with the label gone. ws-reconnect
//     and release-notes run on build, bench-idle on gpu.
//   - The fan's attempts are drawn by another path. cache-1 is local and must
//     carry no mark either.
//   - The rows could be read before the remote rows arrive. The test waits
//     for both remote machines first.

// TestCockpitMachineChip opens the frame A fleet (this machine plus build and
// gpu) at 200x55. A row from another machine shows the machine's chip before
// its session; a row from this machine shows its session and no chip.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: with ckPlace naming
// the machine on every row again, the local rows carry the mark.
func TestCockpitMachineChip(t *testing.T) {
	term, _ := startFrameA(t, 200, 55)
	row := func(s tuitest.Screen, name string) string {
		if r := fleetRowWith(s, name); r >= 0 {
			return fleetCol(s, r)
		}
		return ""
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(row(s, "ws-reconnect"), ckHostGlyph+" build") &&
			strings.Contains(row(s, "release-notes"), ckHostGlyph+" build") &&
			strings.Contains(row(s, "bench-idle"), ckHostGlyph+" gpu")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a row from another machine lacks the machine's chip: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "machine-chip")
	s := term.Screen()
	for _, name := range []string{"api-auth", "docs-cli", "scratch", "migrate-cfg", "cache-1"} {
		line := row(s, name)
		if line == "" {
			t.Fatalf("no row for %s\n%s", name, term.Snapshot())
		}
		if strings.Contains(line, ckHostGlyph) {
			t.Errorf("ASSERTION: the row of %s, on this machine, carries a machine chip: %q", name, line)
		}
	}
}
