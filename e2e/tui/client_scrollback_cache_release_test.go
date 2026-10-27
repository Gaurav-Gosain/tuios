package tuie2e

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestClientReleasesScrollbackCache: a client draws a scrolled pane from
// history lines it decodes and keeps in the emulator's scrollback cache, and
// once the pane is scrolled back down nothing reads them again. The client's
// maintenance tick drops them. Two clients, each on a daemon of its own, get
// the same panes and the same history; one releases every second, the other
// not within the test. Both scroll every pane back through copy mode, to the
// oldest line, page it down through about 200 lines and take it back to the
// newest. The heap gap between them must then grow over the gap the setup
// left by at least a quarter of the cells of the history each pane drew.
// Scrolled back again after the release, and down again, every pane must
// draw exactly as on the client that kept its lines.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Client scrollback cache
// released by the maintenance tick"):
//   - with the ReleaseScrollbackCache call cut from TryReleaseEmulatorScratch,
//     the test fails at "the client released ... of decoded history".
//   - with a release that also clears the ring, it fails at "never scrolled
//     back to HIST-1-END": the history is gone before the first scroll.
func TestClientReleasesScrollbackCache(t *testing.T) {
	const (
		panes   = 4
		cols    = 240
		rows    = 72
		cellLen = 112 // bytes in a uv.Cell
		history = 400
		// Lines of history each pane is paged through: more than a screen,
		// under the cache's 256 lines and its cells for these widths.
		paged = 200
	)
	// Several screens of numbered lines. The command line spells the tag with
	// a gap, so only the output holds HIST-1-END.
	const fill = `seq 1 400 | sed 's/.*/HIST-&-END/'; echo FILL$((1+1))DONE` + "\n"
	const oldest = "HIST-1-END"

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
			t.Skip("libghostty-vt keeps its own history cache, which this release does not reach")
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
	typeInto := func(c *client, id, text string) {
		t.Helper()
		if out, err := tuiosCLI(t, c.base, "send-text", "-s", "mem", "-w", id, text); err != nil {
			t.Fatalf("send-text: %v: %s", err, out)
		}
	}
	// run types text into every pane and waits until the client shows the
	// marker in each of them.
	run := func(c *client, text, marker string) {
		t.Helper()
		for _, id := range c.ids {
			typeInto(c, id, text)
		}
		if err := c.term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Count(s.Text(), marker) >= panes
		}, time.Minute); err != nil {
			t.Fatalf("the %s client never showed %s in every pane: %v\n%s", c.name, marker, err, c.term.Snapshot())
		}
	}
	// interior is the pane's own size, as stty in the pane reads it.
	interior := func(c *client, i int) (r, w int) {
		t.Helper()
		typeInto(c, c.ids[i], fmt.Sprintf("printf 'SZ%%s=%%sE\\n' %d \"$(stty size)\"\n", i))
		re := regexp.MustCompile(fmt.Sprintf(`SZ%d=(\d+) (\d+)E`, i))
		var m []string
		if err := c.term.WaitFor(func(s tuitest.Screen) bool {
			m = re.FindStringSubmatch(s.Text())
			return m != nil
		}, shellTimeout); err != nil {
			t.Fatalf("pane %d of the %s client never printed its size: %v\n%s", i, c.name, err, c.term.Snapshot())
		}
		r, _ = strconv.Atoi(m[1])
		w, _ = strconv.Atoi(m[2])
		return r, w
	}
	pressOn := func(c *client, keys ...any) {
		t.Helper()
		if err := c.term.SendKeys(keys...); err != nil {
			t.Fatalf("keys to the %s client: %v", c.name, err)
		}
	}
	waitScreen := func(c *client, what string, ok func(string) bool) {
		t.Helper()
		if err := c.term.WaitFor(func(s tuitest.Screen) bool { return ok(s.Text()) }, uiTimeout); err != nil {
			t.Fatalf("the %s client %s: %v\n%s", c.name, what, err, c.term.Snapshot())
		}
	}
	// back focuses a pane and scrolls it in copy mode to its oldest line, so
	// the client draws a whole screen of history.
	back := func(c *client, id string) {
		t.Helper()
		if out, err := tuiosCLI(t, c.base, "focus-window", "-s", "mem", id); err != nil {
			t.Fatalf("focus-window: %v: %s", err, out)
		}
		deadline := time.Now().Add(uiTimeout)
		for focusedPaneID(t, c.base, "mem") != id {
			if time.Now().After(deadline) {
				t.Fatalf("the %s client never focused %s", c.name, id)
			}
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond)
		pressOn(c, tuitest.Ctrl('b'), "[")
		waitScreen(c, "never opened copy mode", func(s string) bool { return strings.Contains(s, "y yank") })
		pressOn(c, "g", "g")
		waitScreen(c, "never scrolled back to "+oldest, func(s string) bool { return strings.Count(s, oldest) == 1 })
	}
	// through pages the focused pane down from its oldest line, one screen
	// of r rows at a time, until it has drawn paged/r*r lines of history.
	through := func(c *client, r int) {
		t.Helper()
		for k := 1; k*r <= paged; k++ {
			pressOn(c, tuitest.Ctrl('f'))
			mark := fmt.Sprintf("HIST-%d-END", k*r)
			waitScreen(c, "never paged down to "+mark, func(s string) bool { return strings.Contains(s, mark) })
		}
	}
	// down takes the focused pane back to its newest line and out of copy
	// mode.
	down := func(c *client) {
		t.Helper()
		pressOn(c, "G")
		waitScreen(c, "never scrolled back down", func(s string) bool { return !strings.Contains(s, oldest) })
		pressOn(c, "q")
		waitScreen(c, "never left copy mode", func(s string) bool { return !strings.Contains(s, "y yank") })
	}
	// heap is the least of a few readings: one reading carries whatever the
	// client was drawing at that moment, a few hundred KiB at times.
	heap := func(c *client) int64 {
		t.Helper()
		least := int64(math.MaxInt64)
		for range 5 {
			n, err := pprofHeapHeld(c.addr)
			if err != nil {
				t.Fatalf("heap of the %s client: %v", c.name, err)
			}
			least = min(least, int64(n))
			time.Sleep(200 * time.Millisecond)
		}
		return least
	}
	// settle waits until both clients have no notice drawing and nothing
	// changing, so neither holds a frame the other has already let go.
	settle := func(what string, a, b *client) {
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
	}
	same := func(what string, a, b *client) {
		t.Helper()
		settle(what, a, b)
		got, kept := a.term.SnapshotStyled(), b.term.SnapshotStyled()
		if got == kept {
			return
		}
		gotRows, keptRows := strings.Split(got, "\n"), strings.Split(kept, "\n")
		for i := range min(len(gotRows), len(keptRows)) {
			if gotRows[i] != keptRows[i] {
				t.Fatalf("ASSERTION: %s, the client that released drew row %d differently from a client that kept its lines:\n--- released ---\n%q\n--- kept ---\n%q\n--- released screen ---\n%s\n--- kept screen ---\n%s",
					what, i, gotRows[i], keptRows[i], a.term.Snapshot(), b.term.Snapshot())
			}
		}
		t.Fatalf("ASSERTION: %s, the client that released drew %d rows where a client that kept its lines drew %d", what, len(gotRows), len(keptRows))
	}

	released, kept := start("releasing", "1"), start("keeping", "3600")
	both := []*client{released, kept}

	// A quarter of the cells of the history each pane draws. Both clients
	// run the same command, so their histories hold the same lines. A
	// quarter of one screen is under the gap's own noise between two runs
	// with nothing released, up to about 400 KiB.
	var want int64
	sizes := make([]string, 0, panes)
	heights := make([]int, 0, panes)
	for i := range panes {
		r, w := interior(released, i)
		if kr, kw := interior(kept, i); kr != r || kw != w || r <= 0 || w <= 0 || r > paged {
			t.Fatalf("pane %d reads its size as %dx%d on the releasing client and %dx%d on the other", i, w, r, kw, kr)
		}
		sizes = append(sizes, fmt.Sprintf("%dx%d", w, r))
		heights = append(heights, r)
		want += int64(paged / r * r * w * cellLen / 4)
	}
	for _, c := range both {
		run(c, fill, "FILL2DONE")
	}
	// The releasing client runs its release at least twice in this time.
	settle("after the setup", released, kept)
	time.Sleep(3 * time.Second)
	gap0 := heap(kept) - heap(released)

	for _, c := range both {
		for i, id := range c.ids {
			back(c, id)
			through(c, heights[i])
			down(c)
		}
	}
	settle("after the scroll", released, kept)
	time.Sleep(3 * time.Second)
	gap1 := heap(kept) - heap(released)
	t.Logf("client heap after %d panes (%s) were scrolled back to their oldest line, paged down through about %d lines and back down: the gap grew from %d KiB to %d KiB, by %d KiB (%d KiB/pane)",
		panes, strings.Join(sizes, " "), paged, gap0/1024, gap1/1024, (gap1-gap0)/1024, (gap1-gap0)/1024/panes)
	if gap1-gap0 < want {
		t.Fatalf("ASSERTION: the client released %d KiB of decoded history across %d panes, want at least %d KiB (a quarter of the cells of the history each pane drew)",
			(gap1-gap0)/1024, panes, want/1024)
	}

	// The next read decodes the lines again and draws them as the client
	// that kept them does.
	for i := range panes {
		for _, c := range both {
			back(c, c.ids[i])
		}
		same(fmt.Sprintf("pane %d scrolled back", i), released, kept)
		for _, c := range both {
			down(c)
		}
		same(fmt.Sprintf("pane %d scrolled back down", i), released, kept)
	}
}
