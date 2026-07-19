package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSustainedOutputKeepsRendering is the regression test for the freeze.
//
// renderTerminal held the focused window's I/O read lock for its cell walk and
// then called getRealCursor, which took that same sync.RWMutex again on the same
// goroutine. Go's RWMutex is not reentrant for readers: once a writer calls
// Lock, later RLock calls block so the writer cannot be starved. The PTY reader
// takes the exclusive side for every chunk of shell output, so it regularly
// queued between the render path's two acquisitions. The UI goroutine then
// parked on its second RLock behind a writer waiting for the first RLock to be
// released, and neither could proceed. The process sat at zero CPU with a dead
// UI, within seconds of any command producing output.
//
// The assertion is deliberately about *continued* rendering rather than about a
// single frame: a frozen tuios keeps showing whatever was on screen when it
// died, so "content is present" passes against a corpse. Each round emits a
// marker the shell computes, and the marker must appear on screen while the
// previous rounds' output is still streaming. A frozen UI never shows the next
// marker.
//
// Negative control: verified to fail against a binary with commit 6ca26b1's
// change to internal/app/render_terminal.go reverted, where it hangs on an early
// round with a stale screen. See NEGATIVE_CONTROLS.md.
func TestSustainedOutputKeepsRendering(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	// A directory big enough that each listing is a real burst of output, so
	// the PTY reader is taking the exclusive lock continuously while the UI
	// goroutine renders.
	runInShell(t, term, "mkdir -p /tmp/tuios-e2e-ls && (cd /tmp/tuios-e2e-ls && for i in $(seq 1 200); do : > f$i; done) && echo SETUP-DONE",
		"SETUP-DONE", 30*time.Second)

	const rounds = 6
	for i := 1; i <= rounds; i++ {
		marker := fmt.Sprintf("ROUND-%d-OK", i)
		// ls of the large directory, then a marker the shell must compute. The
		// marker can only render if the UI goroutine is still alive after the
		// burst it just had to draw.
		cmd := fmt.Sprintf("ls /tmp/tuios-e2e-ls; ls /tmp/tuios-e2e-ls; echo ROUND-$((%d))-OK", i)
		if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
			t.Fatalf("round %d: send: %v", i, err)
		}
		if err := term.WaitForText(marker, 20*time.Second); err != nil {
			t.Fatalf("UI stopped rendering at round %d/%d: %q never appeared. "+
				"This is the render/IO lock reentry freeze: the UI goroutine is parked "+
				"on a second read lock behind the PTY writer.\nerr: %v\n%s",
				i, rounds, marker, err, term.Snapshot())
		}
		alive(t, term, fmt.Sprintf("during output round %d", i))
	}

	// The UI must still respond to input, not merely have painted. A frozen
	// tuios cannot service the keystroke that toggles the mode indicator.
	leaveTerminalMode(t, term)
	if !strings.Contains(term.Screen().Text(), "Window Management Mode") {
		t.Fatalf("UI did not react to the mode switch after sustained output\n%s", term.Snapshot())
	}
}
