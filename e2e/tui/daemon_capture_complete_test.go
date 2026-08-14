package tuie2e

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestDaemonCaptureKeepsFastOutput pins the daemon emulator as the authority it
// is now declared to be.
//
// docs/REHYDRATION.md, "What is authoritative": the daemon emulator is
// authoritative for grid, cursor, modes and scrollback, and its feed blocks
// rather than dropping a chunk when it falls behind. Every route into a pane
// rehydrates from it, so anything it fails to see is content no client can be
// given.
//
// # Why this test exists rather than a unit test
//
// It was written as a standing reproduction of the opposite. The read loop used
// to offer each chunk to the emulator's feeder and drop it when the queue was
// full, on the stated grounds that the client's own emulator was what rendered.
// The first campaign run against the new PTY oracle found it in minutes: a pane
// printing 1200 lines left holes in capture-pane while a control file written by
// the same pipeline had every line. It was fixed in 445ca0e, "stop dropping
// output from the daemon's own emulator", as part of the rehydration audit.
//
// A drop is load dependent, which is what makes it a bad fit for a unit test and
// a good fit for here: it needs a real daemon, a real shell writing as fast as
// it can into a real PTY, and a reader that has to keep up. tee is the control,
// so one write reaches both a file and the terminal; a complete file puts printf,
// seq and the shell beyond suspicion and places any loss inside tuios.
//
// The assertion is adjacency rather than a line count, because a line count
// cannot tell a slow reader from a lossy one: the numbers say where the hole is.
func TestDaemonCaptureKeepsFastOutput(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "capture-load", "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	w := firstWindow(t, base, "capture-load")
	tag := w.tag()

	const lines = 1200
	control := t.TempDir() + "/control.txt"
	cmd := paneWitnessCmd(tag, 1, lines)
	cmd = strings.TrimSuffix(cmd, "\n") + " | tee " + control + "\n"
	if err := paneSend(base, "capture-load", w.ID, cmd); err != nil {
		t.Fatalf("seed the control: %v", err)
	}
	// A second run with nothing slowing it down, straight at the PTY. This is
	// the one that used to lose lines.
	if err := paneSend(base, "capture-load", w.ID, paneWitnessCmd(tag, lines+1, 2*lines)); err != nil {
		t.Fatalf("seed the pane: %v", err)
	}

	deadline := time.Now().Add(bulkTimeout)
	for {
		body, err := os.ReadFile(control)
		if err == nil && strings.Count(string(body), "\n") >= lines {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("the control itself never reached %d lines, so this proves nothing", lines)
		}
		time.Sleep(200 * time.Millisecond)
	}

	last := "MK" + tag + "-" + itoa(2*lines)
	for {
		hist, err := daemonScrollback(base, "capture-load", w.ID, 3*lines)
		if err != nil {
			t.Fatalf("capture-pane: %v", err)
		}
		joined := strings.Join(hist, "\n")
		if strings.Contains(joined, last) {
			if a, b, found := spliceIn(hist); found {
				t.Fatalf("the daemon's own history goes %d straight to %d, so it dropped "+
					"%d lines the pane printed; the control file has all %d of them",
					a.seq, b.seq, b.seq-a.seq-1, lines)
			}
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("the daemon never showed %s, the last line the pane printed", last)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
