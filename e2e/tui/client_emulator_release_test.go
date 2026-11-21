package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestClientReleasesEmulatorScratch: an attached client holds an emulator per
// pane, and like the daemon's it keeps the reflow scratch of a pane resized
// more than once and the alternate screen's cells of a pane that has run a
// full-screen program. The client's maintenance tick drops both at the
// daemon's cleanup interval. Two clients, each on a daemon of its own, get
// the same panes, the same text, the same resizes and the same program; one
// releases every second, the other not within the test. The one that
// releases must hold less heap by at least half a pane of cells per pane for
// the reflow scratch, and by a quarter more for the alternate screen. After
// the releases, a resize, a full-screen program and the shell it returns to
// must draw exactly as on the client that kept its buffers.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Client emulator buffers
// released by the maintenance tick"):
//   - with the releaseEmulatorScratch call cut from the TickerMsg case, the
//     test fails at "the client released ... of reflow scratch".
//   - with the ReleaseAltScreen call cut, it fails at "the client released
//     ... of alternate screen".
//   - with ReleaseAltScreen's check for the alternate screen being shown
//     cut, it fails at "drew row ... differently": the release blanked the
//     program's screen.
//   - with the release outside ioMu, a -race binary reports the release
//     against the window's output writer.
func TestClientReleasesEmulatorScratch(t *testing.T) {
	const (
		panes   = 4
		cols    = 240
		narrow  = 220
		rows    = 72
		cellLen = 112
	)
	const fill = `awk 'BEGIN{for(i=1;i<=20000;i++) printf "%d,", i; print ""}'; echo FILL$((1+1))DONE` + "\n"
	const fullScreen = `printf '\033[?1049h'; seq 1 3000; printf '\033[?1049l'; echo ALT$((1+1))DONE` + "\n"
	const corner = `printf '\033[?1049h\033[999;999H\033[6D%sR' CORNE; sleep 2; printf '\033[H%sN' AGAI; read x; printf '\033[?1049l'; echo BACK$((1+1))HOME` + "\n"

	type client struct {
		name, base, addr string
		term             *tuitest.Terminal
		ids              []string
	}
	t.Setenv("TUIOS_CLEANUP_SECONDS", "3600")
	start := func(name, releaseSeconds string) *client {
		t.Helper()
		base := t.TempDir()
		killDaemon(t, base)
		startDaemonWithPprof(t, base)
		if out, err := tuiosCLI(t, base, "--version"); err == nil && strings.Contains(out, "[ghostty backend]") {
			t.Skip("libghostty-vt keeps no reflow scratch or alternate screen of tuios's to release")
		}
		addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
		term := startIn(t, base, startOpts{
			cols: cols, rows: rows,
			args: []string{"new", "mem", "--pprof", addr},
			env:  []string{"TUIOS_CLEANUP_SECONDS=" + releaseSeconds},
		})
		waitBoot(t, term)
		for i := 1; i <= panes; i++ {
			newWindow(t, term)
			if i == 1 {
				enableTiling(t, term)
			}
		}
		return &client{name: name, base: base, addr: addr, term: term, ids: windowIDs(t, base, "mem")}
	}
	run := func(c *client, text, marker string) {
		t.Helper()
		for _, id := range c.ids {
			if out, err := tuiosCLI(t, c.base, "send-text", "-s", "mem", "-w", id, text); err != nil {
				t.Fatalf("send-text: %v: %s", err, out)
			}
		}
		if err := c.term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Count(s.Text(), marker) >= panes
		}, time.Minute); err != nil {
			t.Fatalf("the %s client never showed %s in every pane: %v\n%s", c.name, marker, err, c.term.Snapshot())
		}
	}
	resize := func(c *client, w int) {
		t.Helper()
		if err := c.term.Resize(w, rows); err != nil {
			t.Fatalf("resize the %s client to %dx%d: %v", c.name, w, rows, err)
		}
		if err := c.term.WaitStable(10 * time.Second); err != nil {
			t.Fatalf("the %s client never settled after a resize to %dx%d: %v", c.name, w, rows, err)
		}
	}
	heap := func(c *client) int64 {
		t.Helper()
		n, err := pprofHeapHeld(c.addr)
		if err != nil {
			t.Fatalf("heap of the %s client: %v", c.name, err)
		}
		return int64(n)
	}
	want := int64(panes * (cols / 2) * (rows / 2) * cellLen / 2)
	same := func(what string, a, b *client) {
		t.Helper()
		for _, c := range []*client{a, b} {
			if err := c.term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), "▎")
			}, 15*time.Second); err != nil {
				t.Fatalf("a notice never cleared from the %s client: %v\n%s", c.name, err, c.term.Snapshot())
			}
			if err := c.term.WaitStable(10 * time.Second); err != nil {
				t.Fatalf("the %s client never settled %s: %v", c.name, what, err)
			}
		}
		got, kept := a.term.SnapshotStyled(), b.term.SnapshotStyled()
		if got == kept {
			return
		}
		gotRows, keptRows := strings.Split(got, "\n"), strings.Split(kept, "\n")
		for i := range min(len(gotRows), len(keptRows)) {
			if gotRows[i] != keptRows[i] {
				t.Fatalf("ASSERTION: %s, the client that released drew row %d differently from a client that kept its buffers:\n--- released ---\n%q\n--- kept ---\n%q\n--- released screen ---\n%s\n--- kept screen ---\n%s",
					what, i, gotRows[i], keptRows[i], a.term.Snapshot(), b.term.Snapshot())
			}
		}
		t.Fatalf("ASSERTION: %s, the client that released drew %d rows where a client that kept its buffers drew %d", what, len(gotRows), len(keptRows))
	}

	released, kept := start("releasing", "1"), start("keeping", "3600")
	both := []*client{released, kept}

	for _, c := range both {
		run(c, fill, "FILL2DONE")
		resize(c, narrow)
		resize(c, cols)
		resize(c, narrow)
	}
	time.Sleep(3 * time.Second)
	scratch := heap(kept) - heap(released)
	t.Logf("client heap after %d panes resized three times: %d KiB more on the client that kept its buffers (%d KiB/pane)",
		panes, scratch/1024, scratch/1024/panes)
	if scratch < want {
		t.Fatalf("ASSERTION: the client released %d KiB of reflow scratch across %d panes, want at least %d KiB (half a %dx%d pane of cells per pane)",
			scratch/1024, panes, want/1024, cols/2, rows/2)
	}

	for _, c := range both {
		run(c, fullScreen, "ALT2DONE")
	}
	time.Sleep(3 * time.Second)
	alt := heap(kept) - heap(released) - scratch
	t.Logf("client heap after %d panes left a full-screen program: %d KiB more on the client that kept its buffers (%d KiB/pane)",
		panes, alt/1024, alt/1024/panes)
	if altWant := want / 2; alt < altWant {
		t.Fatalf("ASSERTION: the client released %d KiB of alternate screen across %d panes, want at least %d KiB (a quarter of a %dx%d pane of cells per pane)",
			alt/1024, panes, altWant/1024, cols/2, rows/2)
	}

	for _, c := range both {
		resize(c, cols)
	}
	same("after a resize", released, kept)

	for _, c := range both {
		run(c, corner, "AGAIN")
	}
	same("with a full-screen program on the alternate screen", released, kept)
	for _, c := range both {
		run(c, "\n", "BACK2HOME")
	}
	same("once the program went back to the shell", released, kept)
}
