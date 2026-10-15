package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestReflowScratchReleasedBySweep: a pane resized more than once keeps a
// spare block of cells and the history rows its last reflow decoded, so the
// next step of a drag allocates nothing. The daemon's cleanup sweep drops
// them. Two daemons get the same panes, the same text and the same resizes;
// one sweeps every second, the other not within the test. The one that sweeps
// must hold less heap by at least half a screen of cells per pane, which is
// below the spare block alone. After the sweep, one more resize must lay the
// text out exactly as the daemon that kept its buffers does.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Reflow scratch released
// by the cleanup sweep"):
//   - with the releaseReflowScratch call cut from cleanupLoop, the test fails
//     at "the sweep released".
//   - with ReleaseReflowScratch also dropping the main screen's rows, it
//     fails at "laid pane 0 out differently".
func TestReflowScratchReleasedBySweep(t *testing.T) {
	const (
		panes   = 4
		width   = 200
		narrow  = 190
		height  = 60
		cellLen = 112 // bytes in a uv.Cell
	)
	// The line wraps over every row of the screen and 240 rows of history,
	// so each width change reflows the screen and the history rows the line
	// started in. Commas, not spaces: capture-pane trims a row's trailing
	// blanks, and a row that ended on one would not join back.
	const fill = `awk 'BEGIN{for(i=1;i<=20000;i++) printf "%d,", i; print ""}'; echo FILL$((1+1))DONE` + "\n"
	var line strings.Builder
	for i := 1; i <= 20000; i++ {
		fmt.Fprintf(&line, "%d,", i)
	}

	type daemon struct {
		base, addr string
		ids        []string
	}
	start := func(sweepSeconds string) daemon {
		t.Helper()
		base := t.TempDir()
		killDaemon(t, base)
		t.Setenv("TUIOS_CLEANUP_SECONDS", sweepSeconds)
		addr := startDaemonWithPprof(t, base)
		if out, err := tuiosCLI(t, base, "new", "mem", "--detach"); err != nil {
			t.Fatalf("create session: %v: %s", err, out)
		}
		if out, err := tuiosCLI(t, base, "--version"); err == nil && strings.Contains(out, "[ghostty backend]") {
			t.Skip("libghostty-vt keeps no reflow buffers of tuios's to release")
		}
		for range panes - 1 {
			if out, err := tuiosCLI(t, base, "new-window", "-s", "mem"); err != nil {
				t.Fatalf("new-window: %v: %s", err, out)
			}
		}
		return daemon{base: base, addr: addr, ids: windowIDs(t, base, "mem")}
	}
	resize := func(d daemon, w int) {
		t.Helper()
		for _, id := range d.ids {
			params := fmt.Sprintf(`{"session":"mem","window":%q,"width":%d,"height":%d}`, id, w, height)
			if out, err := tuiosCLI(t, d.base, "call", "resize", params); err != nil {
				t.Fatalf("resize %s to %dx%d: %v: %s", id, w, height, err, out)
			}
		}
	}
	capture := func(d daemon, id string) string {
		t.Helper()
		out, err := tuiosCLI(t, d.base, "capture-pane", "-s", "mem", "-w", id)
		if err != nil {
			t.Fatalf("capture-pane %s: %v: %s", id, err, out)
		}
		return out
	}

	swept, held := start("1"), start("3600")
	for _, d := range []daemon{swept, held} {
		resize(d, width)
		for _, id := range d.ids {
			if out, err := tuiosCLI(t, d.base, "send-text", "-s", "mem", "-w", id, fill); err != nil {
				t.Fatalf("send-text: %v: %s", err, out)
			}
		}
		deadline := time.Now().Add(time.Minute)
		for _, id := range d.ids {
			for !strings.Contains(capture(d, id), "FILL2DONE") {
				if time.Now().After(deadline) {
					t.Fatalf("window %s never printed its line:\n%s", id, capture(d, id))
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		// Two reflows leave a spare block; the third reuses it.
		resize(d, narrow)
		resize(d, width)
		resize(d, narrow)
	}
	// The sweeping daemon runs its sweep at least twice in this time.
	time.Sleep(3 * time.Second)

	sweptHeap, err := pprofHeapHeld(swept.addr)
	if err != nil {
		t.Fatalf("heap of the sweeping daemon: %v", err)
	}
	heldHeap, err := pprofHeapHeld(held.addr)
	if err != nil {
		t.Fatalf("heap of the other daemon: %v", err)
	}
	released := int64(heldHeap) - int64(sweptHeap)
	want := int64(panes * narrow * height * cellLen / 2)
	t.Logf("daemon heap after %d panes resized three times at %dx%d: %d KiB without a sweep, %d KiB with one (%d KiB released, %d KiB/pane)",
		panes, narrow, height, heldHeap/1024, sweptHeap/1024, released/1024, released/1024/panes)
	if released < want {
		t.Fatalf("ASSERTION: the sweep released %d KiB of heap across %d panes, want at least %d KiB (half a %dx%d screen of cells per pane)",
			released/1024, panes, want/1024, narrow, height)
	}

	// The next reflow allocates what the sweep dropped and lays the text out
	// as the daemon that kept its buffers does.
	resize(swept, 150)
	resize(held, 150)
	time.Sleep(300 * time.Millisecond)
	for i := range swept.ids {
		got, want := capture(swept, swept.ids[i]), capture(held, held.ids[i])
		if got != want {
			t.Fatalf("ASSERTION: after the sweep, a resize to 150 columns laid pane %d out differently from a daemon that kept its buffers:\n--- swept ---\n%s\n--- kept ---\n%s", i, got, want)
		}
		rows := strings.Split(got, "\n")
		end := -1
		for r, row := range rows {
			if strings.Contains(row, "FILL2DONE") {
				end = r
				break
			}
		}
		if end < 2 {
			t.Fatalf("ASSERTION: no rows of the line above its marker after the resize:\n%s", got)
		}
		for _, row := range rows[:end-1] {
			if len(row) != 150 {
				t.Fatalf("ASSERTION: a wrapped row of the line is %d columns after a resize to 150, want 150: %q\n%s", len(row), row, got)
			}
		}
		if joined := strings.Join(rows[:end], ""); !strings.HasSuffix(line.String(), joined) {
			t.Fatalf("ASSERTION: the rows above the marker do not read back as the end of the line after the resize:\n%s", got)
		}
	}
}
