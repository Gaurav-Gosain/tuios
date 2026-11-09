package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The scrolling strip as daemon state (notes: strip-ops-plan.md). A TUI used
// to send its whole columns in every state push, and the later push won, so
// a strip change the daemon made (the arrange verb's scroll ops, which guios
// uses) was undone by the next push of a TUI built on the old columns. The
// columns now travel as an op of their own, MsgScrollColumns, and a push
// never moves them while every attached TUI sends that op.

// stripSession is a scrolling session of n one-pane columns with two TUI
// clients of one size, the focus on the first column.
func stripSession(t *testing.T, name string, n int) (string, *tuitest.Terminal, *tuitest.Terminal) {
	t.Helper()
	base, a, b := daSession(t, name, n)
	if out, err := tuiosCLI(t, base, "call", "arrange", `{"session":"`+name+`","op":"mode","mode":"scrolling"}`); err != nil {
		t.Fatalf("arrange mode scrolling: %v: %s", err, out)
	}
	daWait(t, base, name, "the strip", func(daState) bool { s, _ := daReadStrip(t, base, name); return len(s.Scroll.Columns) == n })
	daFocusFirstColumn(t, base, name)
	return base, a, b
}

// stripCols reads the strip's column count once it holds still, and checks
// every pane of the session is in it once.
func stripCols(t *testing.T, base, sess string, panes int) (int, string) {
	t.Helper()
	var last string
	for range 20 {
		st, sj := daReadStrip(t, base, sess)
		if sj == last {
			seen := map[string]int{}
			for _, c := range st.Scroll.Columns {
				for _, w := range c.Windows {
					seen[w]++
				}
			}
			if len(seen) != panes {
				t.Fatalf("the strip holds %d panes, want %d: %s", len(seen), panes, sj)
			}
			for w, n := range seen {
				if n != 1 {
					t.Fatalf("pane %s is in the strip %d times: %s", w, n, sj)
				}
			}
			return len(st.Scroll.Columns), sj
		}
		last = sj
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("the strip never held still: %s", last)
	return 0, ""
}

// TestStripChangeByTheDaemonOutlivesATUIPush (S4): with two TUIs on a
// scrolling session of four one-pane columns, the arrange verb's
// scroll.consume (guios's path) makes three columns. Each TUI then pushes (a
// focus cycle), and the strip must still hold three, on both screens.
//
// How this could pass wrongly, written down first:
//   - The consume could fail and nothing push after it: the count is read
//     after the consume, and again after each client pushed.
//   - A push could be skipped as unchanged: pushedGeometry cycles the focus
//     twice on each client, which pushes.
func TestStripChangeByTheDaemonOutlivesATUIPush(t *testing.T) {
	const name = "strip-s4"
	base, a, b := stripSession(t, name, 4)
	var log strings.Builder
	if out, err := tuiosCLI(t, base, "call", "arrange", `{"session":"`+name+`","op":"scroll.consume"}`); err != nil {
		t.Fatalf("arrange scroll.consume: %v: %s", err, out)
	}
	if n, sj := stripCols(t, base, name, 4); n != 3 {
		t.Fatalf("after scroll.consume: %d columns, want 3: %s", n, sj)
	}
	fmt.Fprintf(&log, "scroll.consume: 3 columns\n")
	for i, term := range []*tuitest.Terminal{a, b} {
		pushedGeometry(t, base, name, term, 4)
		n, sj := stripCols(t, base, name, 4)
		fmt.Fprintf(&log, "after client %d pushed: %d columns %s\n", i, n, sj)
		if n != 3 {
			t.Fatalf("client %d's push put the strip back to %d columns, want 3: %s\n%s", i, n, sj, log.String())
		}
	}
	requireSameScreens(t, a, b, "after both pushed")
	if err := os.WriteFile(filepath.Join(artifactDir(t), "strip-s4.txt"), []byte(log.String()), 0o600); err != nil {
		t.Logf("write the transcript: %v", err)
	}
}

// TestStripChangeByTheDaemonRacesATUIPush (S4b): the race S4 does not reach.
// The arrange verb's scroll.consume is sent at the same moment as a key on a
// TUI that makes it push (a focus cycle), then scroll.expel the same way, for
// ten rounds on four one-pane columns. A push built before the TUI heard of
// the daemon's change must not undo it: every round ends on three columns,
// then four.
//
// How this could pass wrongly, written down first:
//   - The two could never overlap: each round sends both from two goroutines
//     at once, ten rounds, and every one must hold.
//   - The focus cycle could move the focus to where consume has nothing to
//     take: the op names the first column's pane, not the focus.
//   - The verb could fail and the count look right: its reply is checked.
func TestStripChangeByTheDaemonRacesATUIPush(t *testing.T) {
	const name = "strip-s4b"
	base, a, _ := stripSession(t, name, 4)
	var log strings.Builder
	// The op names the first column's pane, so where the focus cycle moves
	// the focus does not change what it acts on.
	race := func(op string) {
		t.Helper()
		st, _ := daReadStrip(t, base, name)
		pane := st.Scroll.Columns[0].Windows[0]
		var wg sync.WaitGroup
		var out string
		var err error
		wg.Go(func() {
			out, err = tuiosCLI(t, base, "call", "arrange", `{"session":"`+name+`","op":"`+op+`","pane":"`+pane+`"}`)
		})
		wg.Go(func() { _ = a.SendKeys("\t") })
		wg.Wait()
		if err != nil {
			t.Fatalf("arrange %s: %v: %s", op, err, out)
		}
		fmt.Fprintf(&log, "  %s answered %s, focus now %s\n", op, strings.TrimSpace(out), daRead(t, base, name).Focused)
	}
	for round := range 10 {
		race("scroll.consume")
		if n, sj := stripCols(t, base, name, 4); n != 3 {
			t.Fatalf("round %d: scroll.consume raced a TUI push and the strip has %d columns, want 3: %s\n%s", round, n, sj, log.String())
		}
		race("scroll.expel")
		if n, sj := stripCols(t, base, name, 4); n != 4 {
			t.Fatalf("round %d: scroll.expel raced a TUI push and the strip has %d columns, want 4: %s\n%s", round, n, sj, log.String())
		}
		fmt.Fprintf(&log, "round %d: 4 -> 3 -> 4 columns\n", round)
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "strip-s4b.txt"), []byte(log.String()), 0o600); err != nil {
		t.Logf("write the transcript: %v", err)
	}
}

// TestTwoStripChangesByTheDaemonAtOnce (S4c): with two TUIs on a scrolling
// session of six one-pane columns, two callers send the arrange verb's
// scroll.consume on the first column at the same moment (guios's path, two
// windows at once), for four rounds. Both must land: two columns fewer each
// round, every pane in the strip once. Between rounds the strip is put back
// by expelling, one at a time.
//
// How this could pass wrongly, written down first:
//   - The two could run one after the other by chance: each round sends both
//     from two goroutines at once, and every round is checked.
//   - The ops could act on different columns: both name the first column's
//     pane, and consume pulls in the column right of it.
func TestTwoStripChangesByTheDaemonAtOnce(t *testing.T) {
	const name = "strip-s4c"
	base, _, _ := stripSession(t, name, 6)
	var log strings.Builder
	for round := range 4 {
		st, _ := daReadStrip(t, base, name)
		pane := st.Scroll.Columns[0].Windows[0]
		var wg sync.WaitGroup
		errs := make([]string, 2)
		for i := range 2 {
			wg.Go(func() {
				if out, err := tuiosCLI(t, base, "call", "arrange", `{"session":"`+name+`","op":"scroll.consume","pane":"`+pane+`"}`); err != nil {
					errs[i] = err.Error() + ": " + out
				}
			})
		}
		wg.Wait()
		for _, e := range errs {
			if e != "" {
				t.Fatalf("round %d: arrange scroll.consume: %s", round, e)
			}
		}
		n, sj := stripCols(t, base, name, 6)
		fmt.Fprintf(&log, "round %d: two scroll.consume at once: %d columns\n", round, n)
		if n != 4 {
			t.Fatalf("round %d: two scroll.consume at once left %d columns, want 4: a change was lost: %s\n%s", round, n, sj, log.String())
		}
		// Back to six: expel the first column's panes below its top one.
		for range 2 {
			st, _ := daReadStrip(t, base, name)
			col := st.Scroll.Columns[0].Windows
			if out, err := tuiosCLI(t, base, "call", "arrange", `{"session":"`+name+`","op":"scroll.expel","pane":"`+col[len(col)-1]+`"}`); err != nil {
				t.Fatalf("round %d: arrange scroll.expel: %v: %s", round, err, out)
			}
			stripCols(t, base, name, 6)
		}
		if n, sj := stripCols(t, base, name, 6); n != 6 {
			t.Fatalf("round %d: the strip did not come back to 6 columns: %s", round, sj)
		}
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "strip-s4c.txt"), []byte(log.String()), 0o600); err != nil {
		t.Logf("write the transcript: %v", err)
	}
}

// TestAStripKeyReachesThePeer (S2): on a scrolling session of four one-pane
// columns with two TUIs, scroll_consume pressed on the first makes three
// columns; the second draws the same, and its push and then the first's do
// not put the strip back.
//
// How this could pass wrongly, written down first:
//   - The key could do nothing and both screens still match: the count must
//     drop to three.
//   - A push could be skipped: stripPushes makes each client push.
func TestAStripKeyReachesThePeer(t *testing.T) {
	const name = "strip-s2"
	base, a, b := stripSession(t, name, 4)
	if err := a.SendKeys(tuitest.Alt("c")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		if n, _ := stripCols(t, base, name, 4); n == 3 {
			break
		}
		if time.Now().After(deadline) {
			_, sj := daReadStrip(t, base, name)
			t.Fatalf("scroll_consume on the first client did not reach the session: %s", sj)
		}
		time.Sleep(100 * time.Millisecond)
	}
	stripPushes(t, base, name, a, b, 4, "after scroll_consume on the first client")
	if n, sj := stripCols(t, base, name, 4); n != 3 {
		t.Fatalf("a push put the strip back to %d columns: %s", n, sj)
	}
}

// TestPanesComeAndGoInTheStrip (S6): with two TUIs on a scrolling session of
// three columns, a pane opened from the first TUI and one closed from the
// CLI leave every pane of the session in the strip once, with no column for
// a pane that is gone, and both clients draw one layout.
//
// How this could pass wrongly, written down first:
//   - The strip could miss the new pane and the count still look right:
//     stripCols checks every pane of the session is in the strip once.
//   - A closed pane could stay as a column of its own: the count of panes in
//     the strip must equal the session's after the close.
func TestPanesComeAndGoInTheStrip(t *testing.T) {
	const name = "strip-s6"
	base, a, b := stripSession(t, name, 3)
	newWindow(t, a)
	deadline := time.Now().Add(uiTimeout)
	for {
		st, _ := daReadStrip(t, base, name)
		n := 0
		for _, c := range st.Scroll.Columns {
			n += len(c.Windows)
		}
		if n == 4 {
			break
		}
		if time.Now().After(deadline) {
			_, sj := daReadStrip(t, base, name)
			t.Fatalf("the new pane never joined the strip: %s", sj)
		}
		time.Sleep(100 * time.Millisecond)
	}
	stripCols(t, base, name, 4)
	requireSameScreens(t, a, b, "after a pane opened")
	gone := daRead(t, base, name).Windows[0].ID
	if out, err := tuiosCLI(t, base, "close-window", "-s", name, gone); err != nil {
		t.Fatalf("close-window: %v: %s", err, out)
	}
	deadline = time.Now().Add(uiTimeout)
	for {
		_, sj := daReadStrip(t, base, name)
		if !strings.Contains(sj, gone) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the closed pane is still in the strip: %s", sj)
		}
		time.Sleep(100 * time.Millisecond)
	}
	stripCols(t, base, name, 3)
	stripPushes(t, base, name, a, b, 3, "after a pane closed")
	if _, sj := daReadStrip(t, base, name); strings.Contains(sj, gone) {
		t.Fatalf("a push put the closed pane back in the strip: %s", sj)
	}
}

// stripPushes makes each client push (a focus cycle) and checks the strip
// holds as it was and both clients draw the same. requireOneLayout compares
// the rectangles each client pushes, which in the scrolling layout depend on
// where the focus cycle left the strip scrolled, so the strip is compared
// instead.
func stripPushes(t *testing.T, base, sess string, a, b *tuitest.Terminal, panes int, what string) {
	t.Helper()
	_, before := stripCols(t, base, sess, panes)
	for i, term := range []*tuitest.Terminal{a, b} {
		pushedGeometry(t, base, sess, term, panes)
		if _, now := stripCols(t, base, sess, panes); now != before {
			t.Fatalf("%s: client %d's push changed the strip:\n before %s\n now    %s", what, i, before, now)
		}
	}
	requireSameScreens(t, a, b, what)
}

// TestAStripColumnWidthHolds (S5): on a scrolling session of three columns
// with two TUIs, scroll_cycle_width gives the focused column a width of its
// own. The session's columns carry it, and each client's push keeps it, and
// both clients draw the same.
//
// How this could pass wrongly, written down first:
//   - The width could not be carried at all and every check compare zero
//     with zero: the first column's proportion or fixed width must change.
//   - A push could be skipped: stripPushes makes each client push.
func TestAStripColumnWidthHolds(t *testing.T) {
	const name = "strip-s5"
	base, a, b := stripSession(t, name, 3)
	width := func() (float64, int) {
		st, _ := daReadStrip(t, base, name)
		return st.Scroll.Columns[0].Proportion, st.Scroll.Columns[0].FixedWidth
	}
	p0, f0 := width()
	if daRun(t, base, name, "scroll_cycle_width") {
		t.Logf("scroll_cycle_width was routed to a client")
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		if p, f := width(); p != p0 || f != f0 {
			break
		}
		if time.Now().After(deadline) {
			_, sj := daReadStrip(t, base, name)
			t.Fatalf("scroll_cycle_width changed no width the session holds: %s", sj)
		}
		time.Sleep(100 * time.Millisecond)
	}
	stripPushes(t, base, name, a, b, 3, "after scroll_cycle_width")
	if p, f := width(); p == p0 && f == f0 {
		t.Fatalf("a push put the first column's width back")
	}
}

// TestAStripChangeFromTheDaemonSlides (S8): with animations on, a TUI on a
// scrolling session of three one-pane columns takes the arrange verb's
// scroll.consume (a change the daemon made) from a state. The pane that joins
// the first column must slide there, through the frames in between, not cut,
// and one way. The frames go to strip-s8-frames.txt.
//
// How this could pass wrongly, written down first:
//   - The consume could do nothing and no pane move: the pane must end in
//     the first column's place.
//   - The trace could miss frames: the move must pass through at least three
//     values in between (oneWay).
func TestAStripChangeFromTheDaemonSlides(t *testing.T) {
	const name = "strip-s8"
	trace := filepath.Join(t.TempDir(), "render-trace.log")
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", name}, animations: true,
		env: []string{"TUIOS_RENDER_TRACE=" + trace}})
	killDaemon(t, base)
	waitBoot(t, term)
	// A session started this way has no pane until the first one opens.
	for range 3 {
		newWindow(t, term)
	}
	enableTiling(t, term)
	if out, err := tuiosCLI(t, base, "call", "arrange", `{"session":"`+name+`","op":"mode","mode":"scrolling"}`); err != nil {
		t.Fatalf("arrange mode scrolling: %v: %s", err, out)
	}
	daWait(t, base, name, "the strip", func(daState) bool { s, _ := daReadStrip(t, base, name); return len(s.Scroll.Columns) == 3 })
	daFocusFirstColumn(t, base, name)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled: %v", err)
	}
	st, _ := daReadStrip(t, base, name)
	first, moved := st.Scroll.Columns[0].Windows[0], st.Scroll.Columns[1].Windows[0]
	short := moved[:8]
	mark := fileSize(t, trace)
	if out, err := tuiosCLI(t, base, "call", "arrange", `{"session":"`+name+`","op":"scroll.consume","pane":"`+first+`"}`); err != nil {
		t.Fatalf("arrange scroll.consume: %v: %s", err, out)
	}
	if n, sj := stripCols(t, base, name, 3); n != 2 {
		t.Fatalf("after scroll.consume: %d columns, want 2: %s", n, sj)
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled: %v", err)
	}
	frames := readTraceFrames(t, trace, mark, short)
	var b strings.Builder
	var xs []int
	for _, f := range frames {
		fmt.Fprintf(&b, "anims=%d syncs=%d %v\n", f.anims, f.syncs, f.panes)
		if r, ok := f.panes[short]; ok {
			xs = append(xs, r.X)
		}
	}
	_ = os.WriteFile(filepath.Join(artifactDir(t), "strip-s8-frames.txt"), []byte(b.String()), 0o644)
	if len(xs) == 0 {
		t.Fatalf("the trace has no frame of the moved pane")
	}
	firstX := frames[len(frames)-1].panes[first[:8]].X
	t.Logf("the moved pane's left edges %v, the first column at %d", compactSeq(xs), firstX)
	if xs[len(xs)-1] != firstX {
		t.Fatalf("the moved pane ended at x=%d, want the first column's x=%d", xs[len(xs)-1], firstX)
	}
	if msg := oneWay(xs, xs[0], firstX); msg != "" {
		t.Errorf("the moved pane's left edge %s: %v", msg, compactSeq(xs))
	}
}

// TestStripActionsRunWithATUIAttached (S1, part 2): with two TUIs on a
// scrolling session of four one-pane columns, run-command runs the strip
// actions in the daemon (routed false), each changing the session's strip by
// its step, and both clients draw the same.
//
// How this could pass wrongly, written down first:
//   - The TUI could run each and push: routed false is the daemon's answer.
//   - A step could hold already: each compares the strip before and after,
//     and consume and expel need the column count to move by one.
func TestStripActionsRunWithATUIAttached(t *testing.T) {
	const name = "strip-s1"
	base, a, b := stripSession(t, name, 4)
	var log strings.Builder
	step := func(action, what string, ok func(before, now daStrip, bj, nj string) bool) {
		t.Helper()
		before, bj := daReadStrip(t, base, name)
		if daRun(t, base, name, action) {
			t.Fatalf("%s: run-command answered routed true; the TUI ran it, not the daemon", action)
		}
		deadline := time.Now().Add(uiTimeout)
		for {
			now, nj := daReadStrip(t, base, name)
			if ok(before, now, bj, nj) {
				fmt.Fprintf(&log, "%s: routed false, %s: %s\n", action, what, nj)
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %s did not hold: before %s now %s", action, what, bj, nj)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	changed := func(_, _ daStrip, bj, nj string) bool { return bj != nj }
	step("scroll_consume", "one column fewer", func(b, n daStrip, _, _ string) bool { return len(n.Scroll.Columns) == len(b.Scroll.Columns)-1 })
	step("scroll_expel", "one column more", func(b, n daStrip, _, _ string) bool { return len(n.Scroll.Columns) == len(b.Scroll.Columns)+1 })
	daFocusFirstColumn(t, base, name)
	step("scroll_move_right", "the columns are in another order", changed)
	step("scroll_cycle_width", "a column's width changed", changed)
	step("scroll_maximize", "a column's width changed", changed)
	stripPushes(t, base, name, a, b, 4, "after the strip actions")
	if err := os.WriteFile(filepath.Join(artifactDir(t), "strip-s1.txt"), []byte(log.String()), 0o600); err != nil {
		t.Logf("write the transcript: %v", err)
	}
}

// TestStripKeysFromTwoClientsAtOnce (S3, part 2): both TUIs press
// scroll_consume at the same moment on six one-pane columns, for four
// rounds. Both presses must land: six columns to four, every pane once, and
// both clients draw one layout. The strip is put back between rounds by
// expelling, one at a time (two expels at once on one column are one expel:
// the focus follows the pane out).
//
// How this could pass wrongly, written down first:
//   - The two presses could land one after the other by chance: each is sent
//     from two goroutines at once, and every round is checked.
//   - The first column could be a different one each round: the focus is put
//     on the strip's first column before each round.
func TestStripKeysFromTwoClientsAtOnce(t *testing.T) {
	const name = "strip-s3"
	base, a, b := stripSession(t, name, 6)
	var log strings.Builder
	for round := range 4 {
		daFocusFirstColumn(t, base, name)
		together(t, a, string(tuitest.Alt("c")), b, string(tuitest.Alt("c")))
		deadline := time.Now().Add(uiTimeout)
		n, sj := stripCols(t, base, name, 6)
		for n > 4 && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			n, sj = stripCols(t, base, name, 6)
		}
		fmt.Fprintf(&log, "round %d: two scroll_consume at once: %d columns\n", round, n)
		if n != 4 {
			t.Fatalf("round %d: two scroll_consume at once left %d columns, want 4: a press was lost: %s\n%s", round, n, sj, log.String())
		}
		requireSameScreens(t, a, b, fmt.Sprintf("round %d", round))
		for range 2 {
			st, _ := daReadStrip(t, base, name)
			col := st.Scroll.Columns[0].Windows
			if out, err := tuiosCLI(t, base, "call", "arrange", `{"session":"`+name+`","op":"scroll.expel","pane":"`+col[len(col)-1]+`"}`); err != nil {
				t.Fatalf("round %d: arrange scroll.expel: %v: %s", round, err, out)
			}
			stripCols(t, base, name, 6)
		}
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "strip-s3.txt"), []byte(log.String()), 0o600); err != nil {
		t.Logf("write the transcript: %v", err)
	}
}
