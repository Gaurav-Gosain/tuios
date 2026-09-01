package tuie2e

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Issue #123: a reattached pane comes back with blank rows where a full-screen
// program had drawn.
//
// The reported steps do not reach it. What does is a pane whose output the
// daemon's own emulator cannot keep up with. The daemon reads the PTY into a
// 64 KB catch-up ring and feeds its emulator from a second goroutine, so under
// a flood the emulator's position (vtSeq) trails the ring's head. A client
// attaching then gets a snapshot taken at the emulator's position, and by the
// time it subscribes the ring has rolled past it. That is a "rolled" catch-up.
//
// A rolled catch-up used to open with a clear (\x1b[H\x1b[2J\x1b[3J), so the
// replayed tail would paint against a known screen instead of splicing onto a
// half-stale one. For a client that drew its screen byte by byte that is right.
// For a client that has just laid down a snapshot it is wrong: the snapshot is
// already the whole stream up to that point, and the clear throws away exactly
// the rows the ring no longer holds.
//
// This test builds that shape with a real binary in a real PTY. The pane draws
// a header once, then floods rows below it forever with per-cell colour, which
// is cheap to produce and expensive to parse. The header is only ever in the
// snapshot: it left the ring millions of bytes ago and the flood never
// addresses those rows. Detach, reattach at a different width, and the header
// must still be there.
//
// The body carries its own marker, and the test waits for that marker before it
// reads the header. That separates the failure this is about (the pane is live,
// the header is gone) from a pane that is simply blank or dead, which would
// otherwise pass for the same thing.
//
// Negative controls, both confirmed to fail here as assertions:
//   - internal/session/session.go, put the guard back to plain `rolled`
//   - internal/app/session.go, pass false instead of fromSeq > 0
const (
	// The flood's geometry. Every figure is small enough to fit the floating
	// pane a fresh session opens with, which is about 58x19 inside its border.
	// A row wider than the pane would wrap, and a wrap on the last row scrolls
	// the header away for reasons that have nothing to do with the reattach.
	rolledHeaderTop  = 1
	rolledBodyTop    = 5
	rolledBodyBottom = 12
	rolledBodyCols   = 30
)

// rolledFloodBytes is the size of the fixture the pane cats in a loop. It only
// has to be large enough that one pass keeps the daemon emulator behind for
// longer than an attach takes; the loop supplies the rest.
const rolledFloodBytes = 24 << 20

// rolledBodyMark is stamped on the body's last row every frame, so the test can
// tell a live pane from a blank one before it judges the header.
const rolledBodyMark = "BODYMARK-issue123"

// rolledPalette is a colour ramp wide enough that consecutive cells rarely
// repeat, so almost every cell carries its own SGR. That is what makes these
// bytes expensive for the emulator relative to the cost of producing them, and
// expense is the whole mechanism: a flood the emulator keeps up with never
// leaves vtSeq behind the ring.
var rolledPalette = []int{0, 52, 88, 124, 160, 196, 202, 208, 214, 220, 226, 231}

// writeRolledFlood generates repaints of rows rolledBodyTop..rolledBodyBottom
// into path, using absolute cursor addressing so the flood can never scroll the
// header off. Returns the number of frames written.
func writeRolledFlood(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create flood fixture: %v", err)
	}
	defer func() { _ = f.Close() }()

	w := bufio.NewWriterSize(f, 1<<20)
	rng := rand.New(rand.NewSource(11))
	frames, written := 0, 0
	for written < rolledFloodBytes {
		frames++
		var b strings.Builder
		for y := rolledBodyTop; y < rolledBodyBottom; y++ {
			fmt.Fprintf(&b, "\x1b[%d;1H", y)
			last := -1
			for range rolledBodyCols {
				c := rolledPalette[rng.Intn(len(rolledPalette))]
				if c != last {
					fmt.Fprintf(&b, "\x1b[48;5;%dm", c)
					last = c
				}
				b.WriteByte(' ')
			}
			b.WriteString("\x1b[0m")
		}
		// The body's last row says the pane is still being painted.
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[0m%s", rolledBodyBottom, rolledBodyMark)
		// Park the cursor at the top of the body, never on the last row, where
		// one more cell would scroll the header away and the test would be
		// measuring its own fixture rather than the reattach.
		fmt.Fprintf(&b, "\x1b[%d;1H", rolledBodyTop)
		n, err := w.WriteString(b.String())
		if err != nil {
			t.Fatalf("write flood fixture: %v", err)
		}
		written += n
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush flood fixture: %v", err)
	}
	return frames
}

// TestReattachKeepsSnapshotRowsWhenRingRolled is the issue #123 reproduction.
func TestReattachKeepsSnapshotRowsWhenRingRolled(t *testing.T) {
	// Unique enough that a stray match cannot come from tuios chrome, the
	// shell prompt, or the command line the shell echoes back.
	const (
		hdrA = "HDRAAA-issue123"
		hdrB = "HDRBBB-issue123"
		hdrC = "HDRCCC-issue123"
	)

	base := t.TempDir()
	killDaemon(t, base)

	fixture := filepath.Join(t.TempDir(), "flood.bin")
	frames := writeRolledFlood(t, fixture)
	t.Logf("flood fixture: %d frames, %d bytes", frames, rolledFloodBytes)

	if out, err := tuiosCLI(t, base, "new", "e2e-rolled", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	first := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "e2e-rolled"}})
	if err := first.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("first client never attached: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	// Draw the header once at absolute rows, then flood forever. printf writes
	// the header straight to the tty, so it is in the daemon's emulator before
	// the first flood byte and in the ring only until the flood rolls it out.
	header := fmt.Sprintf(`printf '\033[%d;1H%s\033[%d;1H%s\033[%d;1H%s'`,
		rolledHeaderTop, hdrA, rolledHeaderTop+1, hdrB, rolledHeaderTop+2, hdrC)
	cmd := header + "; while :; do cat " + fixture + "; done"
	if err := first.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("start the flood: %v", err)
	}

	// Both must be on the first client before the detach: the header, or the
	// snapshot has nothing to preserve and a pass would mean nothing, and the
	// body marker, or the flood never started.
	if err := first.WaitForText(hdrA, shellTimeout); err != nil {
		t.Fatalf("the header never appeared before the detach: %v\n%s", err, first.Snapshot())
	}
	if err := first.WaitForText(rolledBodyMark, soakTimeout); err != nil {
		t.Fatalf("the flood never started: %v\n%s", err, first.Snapshot())
	}
	// The header has to still be there with the flood running, or the fixture
	// is scrolling it off by itself and the reattach is not what this measures.
	if !strings.Contains(first.Screen().Text(), hdrA) {
		t.Fatalf("the flood scrolled the header off before any detach; the fixture geometry does not fit the pane\n%s",
			first.Snapshot())
	}

	// Let the flood run on, so the 64 KB ring rolls far past where the daemon
	// emulator has got to. That gap is the condition the bug lives in.
	time.Sleep(4 * time.Second)

	if err := first.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("send leader d: %v", err)
	}
	waitExit(t, first, "after leader d")

	if !sessionListed(t, base, "e2e-rolled") {
		out, _ := tuiosCLI(t, base, "ls")
		t.Fatalf("the session did not survive the detach\nls:\n%s", out)
	}

	// Reattach at a different width, which is what makes the client restore
	// the snapshot rather than keep the screen it already had.
	second := startIn(t, base, startOpts{cols: 110, rows: 40, args: []string{"attach", "e2e-rolled"}})
	if err := second.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the reattached client never got its window back: %v\n%s", err, second.Snapshot())
	}

	// Wait for the body first. Once the marker is on screen the pane is live
	// and the replay has landed, so a missing header is the loss this test is
	// about and not a pane that has yet to paint anything.
	if err := second.WaitForText(rolledBodyMark, soakTimeout); err != nil {
		t.Fatalf("the reattached pane never painted its body: %v\n%s", err, second.Snapshot())
	}

	deadline := time.Now().Add(uiTimeout)
	var missing []string
	for time.Now().Before(deadline) {
		text := second.Screen().Text()
		missing = missing[:0]
		for _, want := range []string{hdrA, hdrB, hdrC} {
			if !strings.Contains(text, want) {
				missing = append(missing, want)
			}
		}
		if len(missing) == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(missing) > 0 {
		t.Fatalf("the reattached pane lost header rows the snapshot held (issue #123): missing %v\n%s",
			missing, second.Snapshot())
	}
	alive(t, second, "after reattach over a rolled ring")
}
