package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestAltScreenReleasedBySweep: a pane that has run a full-screen program
// and gone back to its shell keeps the alternate screen's grid of cells,
// which nothing reads until the next program clears it. The daemon's cleanup
// sweep drops it. Two daemons get the same panes and run the same program;
// one sweeps every second, the other not within the test. The one that
// sweeps must hold less heap by at least half a screen of cells per pane.
// After the sweep, a resize and a new full-screen program must draw exactly
// as on the daemon that kept the grid: the program's text where it put it
// and nothing left from the program before, also after a sweep that ran
// while the program was on the alternate screen, and the shell's screen
// intact once it exits.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Alternate screen released
// by the cleanup sweep"):
//   - with the releaseAltScreens call cut from cleanupLoop, the test fails at
//     "the sweep released".
//   - with ReleaseAltScreen's check for the alternate screen being shown
//     cut, it fails at "drew pane 0 differently": the sweep blanked the
//     program's screen.
func TestAltScreenReleasedBySweep(t *testing.T) {
	const (
		panes   = 6
		width   = 200
		narrow  = 180
		height  = 60
		cellLen = 112 // bytes in a uv.Cell
	)
	// seq writes every row of the alternate screen, as a full-screen
	// program's first frame does.
	const fullScreen = `printf '\033[?1049h'; seq 1 3000; printf '\033[?1049l'; echo ALT$((1+1))DONE` + "\n"
	corner := fmt.Sprintf(`printf '\033[?1049h\033[%d;%dHCORNER'; read x; printf '\033[?1049l'; echo BACK$((1+1))HOME`+"\n", height, narrow-5)

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
			t.Skip("libghostty-vt keeps no alternate screen of tuios's to release")
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
	run := func(d daemon, text, marker string) {
		t.Helper()
		for _, id := range d.ids {
			if out, err := tuiosCLI(t, d.base, "send-text", "-s", "mem", "-w", id, text); err != nil {
				t.Fatalf("send-text: %v: %s", err, out)
			}
		}
		deadline := time.Now().Add(time.Minute)
		for _, id := range d.ids {
			for !strings.Contains(capture(d, id), marker) {
				if time.Now().After(deadline) {
					t.Fatalf("window %s never showed %s:\n%s", id, marker, capture(d, id))
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}

	swept, held := start("1"), start("3600")
	for _, d := range []daemon{swept, held} {
		resize(d, width)
		run(d, fullScreen, "ALT2DONE")
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
	want := int64(panes * width * height * cellLen / 2)
	t.Logf("daemon heap after %d panes at %dx%d left a full-screen program: %d KiB without a sweep, %d KiB with one (%d KiB released, %d KiB/pane)",
		panes, width, height, heldHeap/1024, sweptHeap/1024, released/1024, released/1024/panes)
	if released < want {
		t.Fatalf("ASSERTION: the sweep released %d KiB of heap across %d panes, want at least %d KiB (half a %dx%d screen of cells per pane)",
			released/1024, panes, want/1024, width, height)
	}

	// A resize, then a program that draws only in the bottom right corner of
	// the new size.
	for _, d := range []daemon{swept, held} {
		resize(d, narrow)
		run(d, corner, "CORNER")
	}
	// The sweep runs while the program is on the alternate screen, and must
	// leave the screen being shown alone.
	time.Sleep(1500 * time.Millisecond)
	for i := range swept.ids {
		got, want := capture(swept, swept.ids[i]), capture(held, held.ids[i])
		if got != want {
			t.Fatalf("ASSERTION: after the sweep, a full-screen program drew pane %d differently from a daemon that kept the grid:\n--- swept ---\n%s\n--- kept ---\n%s", i, got, want)
		}
		rows := strings.Split(strings.TrimRight(got, "\n"), "\n")
		if strings.TrimSpace(strings.Join(rows, "")) != "CORNER" || len(rows) != height || rows[height-1] != strings.Repeat(" ", narrow-6)+"CORNER" {
			t.Fatalf("ASSERTION: the alternate screen after the sweep should hold only CORNER, at the end of row %d of %d:\n%q", height, height, got)
		}
	}
	for _, d := range []daemon{swept, held} {
		run(d, "\n", "BACK2HOME")
	}
	for i := range swept.ids {
		got, want := capture(swept, swept.ids[i]), capture(held, held.ids[i])
		if got != want || !strings.Contains(got, "ALT2DONE") {
			t.Fatalf("ASSERTION: the shell's screen after the program differs from a daemon that kept the grid:\n--- swept ---\n%s\n--- kept ---\n%s", got, want)
		}
	}
}
