package tuie2e

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Two real tuios clients on one daemon session, reshaping the same BSP tree at
// the same moment.
//
// The tree travels as an op the daemon applies and versions (see
// internal/session/layout_tree.go). Before that it rode in every client's
// state push, the later push won, and two clients that changed the tree inside
// one round trip of each other could each drop the other's push as older than
// their own and keep two different layouts until one of them pushed again.
//
// The pair is checked two ways. First the screens: each client's pane borders
// are compared, with nothing pushed, because a push is itself what ends the
// disagreement on the old code. Then the pushes: each client pushes in turn
// (two focus moves, which put the focus back where it was) and the daemon's
// rectangles are read after each.
//
// NEGATIVE CONTROL: measured against a binary built from main.
// TestTwoClientsReshapeOneTreeAtOnce failed 5 runs in 10 there, at rounds 0,
// 7, 20, 22 and 25: one client showed the panes stacked and the other side by
// side, and stayed that way for the whole five seconds the check waits.
// TestTwoClientsResizeWhilePeerSplits passes on main; it pins the new-pane
// race, which the op has to keep working.

// treeOpsSession is a detached session with two tiled panes and two clients of
// the same size attached to it.
func treeOpsSession(t *testing.T, name string) (string, *tuitest.Terminal, *tuitest.Terminal) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, "\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	a := attachIn(t, base, name, startOpts{cols: bigCols, rows: bigRows})
	newWindow(t, a)
	waitWindowCount(t, a, 2, "the split on the first client")
	enableTiling(t, a)
	b := attachIn(t, base, name, startOpts{cols: bigCols, rows: bigRows})
	waitForSettledGeometryIn(t, base, name, 2)
	return base, a, b
}

// pushedGeometry makes term push its layout and returns what the daemon holds
// once it has settled.
func pushedGeometry(t *testing.T, base, name string, term *tuitest.Terminal, n int) []winRect {
	t.Helper()
	for range 2 {
		if err := term.SendKeys("\t"); err != nil {
			t.Fatalf("focus cycle: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	return waitForSettledGeometryIn(t, base, name, n)
}

// borderSignature is where the pane borders are on a client's screen: the
// columns of every box-drawing cell, row by row, above the dock. Two clients of
// one size holding one layout draw the same borders.
func borderSignature(s tuitest.Screen) string {
	cols, rows := s.Size()
	var b strings.Builder
	for r := range max(0, rows-3) {
		for c := range cols {
			if ru := s.Cell(c, r).Rune; ru >= 0x2500 && ru <= 0x257f {
				fmt.Fprintf(&b, "%d,", c)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// requireSameScreens fails when the two clients draw their borders in
// different places once both have settled. It pushes nothing, so it sees a
// disagreement a push would paper over: a client adopts the tree a peer pushes,
// so pushing to find out is also the thing that ends the disagreement.
func requireSameScreens(t *testing.T, a, b *tuitest.Terminal, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var sa, sb string
	for time.Now().Before(deadline) {
		sa, sb = borderSignature(a.Screen()), borderSignature(b.Screen())
		if !strings.Contains(sa, ",") {
			t.Fatalf("%s: no pane borders on the first client's screen, so there is nothing to compare\n%s", what, a.Snapshot())
		}
		if sa == sb {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: the two clients draw different layouts:\n%s\n%s", what, a.Snapshot(), b.Snapshot())
}

// requireOneLayout fails when the two clients show or push different
// rectangles.
func requireOneLayout(t *testing.T, base, name string, a, b *tuitest.Terminal, n int, what string) []winRect {
	t.Helper()
	requireSameScreens(t, a, b, what)
	ga := pushedGeometry(t, base, name, a, n)
	gb := pushedGeometry(t, base, name, b, n)
	if !sameGeometry(ga, gb) {
		t.Fatalf("%s: the two clients hold different layouts:\n first  %v\n second %v\n%s\n%s",
			what, ga, gb, a.Snapshot(), b.Snapshot())
	}
	// And the first client again: a pair that agrees only because the second
	// client's push was the last word is not a pair that agrees.
	if again := pushedGeometry(t, base, name, a, n); !sameGeometry(ga, again) {
		t.Fatalf("%s: the layout moved when the first client pushed again:\n before %v\n after  %v", what, ga, again)
	}
	return ga
}

// together sends one key to each client at the same moment.
func together(t *testing.T, a *tuitest.Terminal, ka string, b *tuitest.Terminal, kb string) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Go(func() { errs[0] = a.SendKeys(ka) })
	wg.Go(func() { errs[1] = b.SendKeys(kb) })
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("send keys: %v", err)
		}
	}
}

// TestTwoClientsReshapeOneTreeAtOnce: one client resizes a split while the
// other rotates it, pressed at the same moment, round after round. After each
// round the two clients draw one layout, and at the end they push one.
func TestTwoClientsReshapeOneTreeAtOnce(t *testing.T) {
	const name = "treeops"
	base, a, b := treeOpsSession(t, name)

	keys := [][2]string{{">", "R"}, {"R", "<"}, {">", "<"}, {"R", "R"}, {">R", "R<"}}
	for round := range 30 {
		k := keys[round%len(keys)]
		together(t, a, k[0], b, k[1])
		time.Sleep(150 * time.Millisecond)
		requireSameScreens(t, a, b, fmt.Sprintf("round %d", round))
	}
	requireOneLayout(t, base, name, a, b, 2, "after every round")
}

// TestTwoClientsResizeWhilePeerSplits: one client resizes a split key after
// key, the way a held key or a drag does, while the other opens a pane beside
// it. The pair has to end on one layout with all three panes tiled.
func TestTwoClientsResizeWhilePeerSplits(t *testing.T) {
	const name = "treesplit"
	base, a, b := treeOpsSession(t, name)

	var wg sync.WaitGroup
	wg.Go(func() {
		for range 8 {
			if err := a.SendKeys(">"); err != nil {
				t.Errorf("resize: %v", err)
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
	})
	time.Sleep(60 * time.Millisecond)
	if err := b.SendKeys("|"); err != nil {
		t.Fatalf("split: %v", err)
	}
	wg.Wait()
	waitWindowCount(t, a, 3, "the new pane on the resizing client")
	waitWindowCount(t, b, 3, "the new pane on the splitting client")

	rects := requireOneLayout(t, base, name, a, b, 3, "after the resize and the split")
	for _, r := range rects {
		if r.Width >= bigCols {
			t.Errorf("pane %s spans the full width (%d): it was never tiled", r.ID, r.Width)
		}
	}
	for i := range rects {
		for j := i + 1; j < len(rects); j++ {
			if geomOverlap(rects[i], rects[j]) {
				t.Errorf("panes overlap: %v and %v", rects[i], rects[j])
			}
		}
	}
}
