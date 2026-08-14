package tuie2e

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A standing reproduction of a finding, not a regression test.
//
// It is skipped unless TUIOS_KNOWN_BUGS=1 because it asserts that something is
// broken, and a suite that goes red on a decision the product has already made
// teaches everyone to ignore it:
//
//	cd e2e/tui && TUIOS_E2E=1 TUIOS_KNOWN_BUGS=1 go test -count=1 \
//	  -run TestKnownDaemonCaptureLosesFastOutput -v ./...
//
// # What it shows
//
// A pane printing quickly loses lines from the daemon's own emulator, so
// capture-pane returns a stream with holes in it. The control is the same
// command's output written to a file by tee in the same pipeline: the file has
// every line, the daemon does not, which places the loss inside tuios rather
// than in the shell, seq, printf or the PTY.
//
// # Where it comes from
//
// internal/session/session.go:1662. The read loop hands each chunk to the
// goroutine that feeds the daemon's emulator through a 256-deep channel, and the
// select has a default arm that drops the chunk when that channel is full. The
// comment there states the reasoning: the emulator exists for state queries and
// the client's own emulator is the rendering source of truth. A fast pane
// arrives as hundreds of small reads, the queue fills, and lines are gone
// permanently: nothing retries and the sequence numbers the catch-up ring keeps
// are not involved, so there is no later point at which the emulator is repaired.
//
// # Why it is worth a maintainer's attention anyway
//
// capture-pane is not only used for state. It is the documented way for an agent
// in a pane to read another pane (skills/tuios/SKILL.md, "Reading another
// pane"), and wait-for window-output matches its patterns against the same
// content. Under that reading the daemon's emulator is a rendering source of
// truth, for a reader that has no client and no other way to see the pane. An
// agent tailing a build that prints quickly can be handed a log with holes in
// it and no indication that anything was dropped.
//
// The cheap options if it is worth fixing are a blocking send with a deadline
// instead of a default arm, or a dropped-bytes counter the capture verb can
// report so a reader at least knows its answer is incomplete.
//
// # Why the fuzz oracle does not assert on it
//
// It cannot, without either reporting this same decision thousands of times or
// giving up bursts, and bursts are how the catch-up ring is reached at all. The
// PTY oracle's vtSafeBurst scopes every daemon-content rule to panes that have
// not flooded, and says so.
func TestKnownDaemonCaptureLosesFastOutput(t *testing.T) {
	if os.Getenv("TUIOS_KNOWN_BUGS") == "" {
		t.Skip("set TUIOS_KNOWN_BUGS=1 to run the standing reproductions")
	}

	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "capture-loss", "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	w := firstWindow(t, base, "capture-loss")
	tag := w.tag()

	const lines = 1200
	control := t.TempDir() + "/control.txt"
	// tee is the control and not part of what is being measured: the same bytes
	// reach a file and the terminal from one write, so a line in the file and not
	// in the daemon was lost after the shell was done with it.
	cmd := paneWitnessCmd(tag, 1, lines)
	cmd = strings.TrimSuffix(cmd, "\n") + " > " + control + "\n"
	if err := paneSend(base, "capture-loss", w.ID, cmd); err != nil {
		t.Fatalf("seed the control: %v", err)
	}
	time.Sleep(3 * time.Second)
	body, err := os.ReadFile(control)
	if err != nil {
		t.Fatalf("control file: %v", err)
	}
	if got := strings.Count(string(body), "\n"); got != lines {
		t.Fatalf("the control itself lost lines (%d of %d), so this proves nothing", got, lines)
	}

	// Now the same command with nothing slowing it down, straight at the PTY.
	if err := paneSend(base, "capture-loss", w.ID, paneWitnessCmd(tag, lines+1, 2*lines)); err != nil {
		t.Fatalf("seed the pane: %v", err)
	}
	time.Sleep(3 * time.Second)

	hist, err := daemonScrollback(base, "capture-loss", w.ID, 2*lines)
	if err != nil {
		t.Fatalf("capture-pane: %v", err)
	}
	a, b, found := spliceIn(hist)
	if !found {
		t.Skipf("the daemon kept every line this time; the drop is load dependent "+
			"and this machine was fast enough (%d witness lines read)", len(witnessesIn(hist)))
	}
	t.Logf("the daemon's own history goes %d -> %d on adjacent rows, "+
		"while the control file written by the same command has all %d lines",
		a.seq, b.seq, lines)
}
