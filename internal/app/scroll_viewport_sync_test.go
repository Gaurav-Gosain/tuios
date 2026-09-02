// Two clients on one session in the scrolling (niri) layout, and what one of
// them doing something does to the other's view of the strip.
//
// The report these exist for: with a local client and a web client on one
// session, moving focus on either one redrew the focus border on the other and
// left its viewport where it was, so the focused window could be entirely off
// the peer's screen. The trace was that ApplyStateSync adopts the peer's focus
// and nothing else: EnsureFocusedVisible appears nowhere in it, and the strip's
// own idea of which column is focused was never moved either, so a client that
// took a peer's focus went on inserting new columns and stepping left and right
// from the column it last focused itself.
//
// The strip's offset is session state (SessionState.ScrollStrip): the strip is
// one long row of columns and the offset is a place on it, so two clients
// holding one offset are looking at the same place. That is safe because the
// panes' box is the same on every client - the session's size is the minimum
// over them and the chrome reserve the maximum - so one offset shows every
// client the same columns.
//
// NEGATIVE CONTROLS, each run by mutating the shipped code and watching the
// named assertion fail:
//
//   - ApplyStateSync not calling adoptScrollStrip: TestPeerScrollsFocusIntoView
//     fails saying B's strip still points at the column it last focused itself,
//     and TestPeerAdoptsADeliberateScroll fails saying A stayed at 0 while B is
//     at 60. On the tree before any of this, the same test failed with B drawing
//     the focused pane at x=-65 on a screen 100 wide.
//   - adoptScrollStrip not pointing the strip at the adopted focus:
//     TestPeerScrollsFocusIntoView fails with the pane at x=-65 again, because
//     EnsureFocusedVisible then reveals the column this client last focused and
//     drags the strip back to it.
//   - BuildSessionState not sending the strip: TestPeerAdoptsADeliberateScroll,
//     TestTwoColumnStepsBothReachThePeer and
//     TestJoiningClientLandsOnTheSessionsStrip all fail.
//   - RestoreFromState not taking it: TestJoiningClientLandsOnTheSessionsStrip
//     fails with the joining client at 65 while the session is at 45.
//   - tiledLayoutStale measuring the strip against the box again:
//     TestUnrelatedSyncLeavesTheStripAlone fails, both clients dragged from 65
//     back to 0 by a sync that only renamed a pane.
//   - scrollingLayoutStale never answering true: TestScrolledStripIsNotStale
//     fails on a pane seven cells from where the strip puts it.
//   - adoptScrollStrip run against the workspace the sync arrived from rather
//     than the one it names: TestWorkspaceSwitchLandsBothClientsOnOneStrip
//     fails with the two clients on 65 and 45 after a round trip through
//     another workspace.
//   - ScrollingOnFocusChange calling ScrollToFocusedColumn again, which is what
//     it did before this fix: TestWorkspaceRoundTripKeepsTheParkedStrip's first
//     row fails saying the round trip moved the strip from 20 to 0 while the
//     focused column was on screen the whole time.
//   - EnsureFocusedVisible given the peek margin, so it moves a column that is
//     already on screen: the same row fails the same way. That is what binds
//     the test to the threshold rather than to the name of the call.
//   - EnsureFocusedVisible never revealing: the second row fails saying the
//     round trip left the focused column off screen at 65, on both clients.
//     Removing the reveal from tileAllWindows' scrolling branch instead fails
//     nothing here, so the reveal on the way back into a workspace is this
//     function's and not the retile's.
//
// StateFingerprint and reconcileStale are pinned in internal/session, where
// their own controls are recorded.

package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// strip renders where every column sits on the strip and where this client's
// viewport is, so a failure says what was on screen rather than that a number
// was wrong.
func strip(m *OS, label string) string {
	sl := m.GetOrCreateScrollingLayout()
	w := m.ScrollingViewWidth()
	var b strings.Builder
	fmt.Fprintf(&b, "%s: view %d wide, offset %d, showing strip cells [%d,%d)\n",
		label, w, sl.ViewportX, sl.ViewportX, sl.ViewportX+w)
	x := 0
	for i := range sl.Columns {
		cw := sl.ResolveColumnWidth(i, w)
		focus := " "
		if i == sl.FocusedCol {
			focus = "*"
		}
		on := "off screen"
		if x < sl.ViewportX+w && x+cw > sl.ViewportX {
			on = "on screen"
			if x < sl.ViewportX || x+cw > sl.ViewportX+w {
				on = "part on screen"
			}
		}
		fmt.Fprintf(&b, "  %scol %d strip [%d,%d) %s\n", focus, i, x, x+cw, on)
		x += cw + sl.Gap
	}
	return b.String()
}

// scrollingFleet is two clients on one session, both in the scrolling layout.
type scrollingFleet struct {
	r  *rig
	p  *peer
	ex *exchange
}

func newScrollingFleet(t *testing.T, panes, cols, rows int) *scrollingFleet {
	return newScrollingFleetSized(t, panes, cols, rows, cols, rows)
}

// newScrollingFleetSized lets the two clients run at different terminal sizes,
// which is the case a shared offset has to be safe under.
func newScrollingFleetSized(t *testing.T, panes, aCols, aRows, bCols, bRows int) *scrollingFleet {
	t.Helper()
	prevAnim := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() { config.Global.AnimationsEnabled = prevAnim })

	r := newRigSized(t, panes, aCols, aRows)
	ex := &exchange{t: t}
	p := joinPeerOS(t, r, bCols, bRows)
	ex.route(r.client, r.m, "A")
	ex.route(p.c, p.m, "B")
	ex.settleBox(r, p)

	// A turns tiling on and picks the scrolling layout, which is session state:
	// B adopts the mode from the sync.
	r.m.AutoTiling = true
	r.m.ApplyLayoutModeName(config.LayoutModeScrolling)
	r.m.TileAllWindows()
	r.m.SyncStateToDaemon()
	f := &scrollingFleet{r: r, p: p, ex: ex}
	f.settle()
	if !p.m.UseScrollingLayout || !p.m.AutoTiling {
		t.Fatalf("the peer never took the scrolling layout: tiling=%t scrolling=%t",
			p.m.AutoTiling, p.m.UseScrollingLayout)
	}
	return f
}

func (f *scrollingFleet) settle() {
	f.ex.settle(200, 60*time.Millisecond)
	f.r.m.CompleteAllAnimations()
	f.p.m.CompleteAllAnimations()
}

// focusOn moves one client's focus to the window at index i the way a click or
// a column step does, and pushes.
func focusOn(m *OS, i int) {
	m.FocusWindow(i)
	m.SyncStateToDaemon()
}

// TestPeerScrollsFocusIntoView is the report: two clients on one session in the
// scrolling layout, one of them moves focus, and the other draws the focus
// border on a column that is not on its screen.
func TestPeerScrollsFocusIntoView(t *testing.T) {
	f := newScrollingFleet(t, 3, 100, 30)
	a, b := f.r.m, f.p.m

	// Park both viewports on the far right of the strip by focusing the last
	// column from A.
	focusOn(a, len(a.Windows)-1)
	f.settle()
	t.Logf("both clients on the last column\n%s%s", strip(a, "A"), strip(b, "B"))

	// The gesture in the report: A moves focus to the first column.
	focusOn(a, 0)
	f.settle()
	t.Logf("A moved focus to the first column\n%s%s", strip(a, "A"), strip(b, "B"))

	if a.FocusedWindow != 0 || b.FocusedWindow != 0 {
		t.Fatalf("the focus did not reach both clients: A %d B %d", a.FocusedWindow, b.FocusedWindow)
	}
	bsl := b.GetOrCreateScrollingLayout()
	if bsl.FocusedCol != 0 {
		t.Errorf("B's strip still points at column %d, not the focused one:\n%s", bsl.FocusedCol, strip(b, "B"))
	}
	fw := b.GetFocusedWindow()
	if fw == nil {
		t.Fatal("B has no focused window")
	}
	if fw.X+fw.Width <= 0 || fw.X >= b.GetRenderWidth() {
		t.Errorf("B draws the focused pane off its screen at x=%d w=%d (screen %d wide):\n%s",
			fw.X, fw.Width, b.GetRenderWidth(), strip(b, "B"))
	}
	if asl := a.GetOrCreateScrollingLayout(); asl.ViewportX != bsl.ViewportX {
		t.Errorf("the two clients are looking at different places on the strip: A %d, B %d\n%s%s",
			asl.ViewportX, bsl.ViewportX, strip(a, "A"), strip(b, "B"))
	}
}

// TestPeerAdoptsADeliberateScroll is the other half of one strip: where the
// session is looking is the session's, so a client that scrolls the strip with
// the wheel takes the other client with it.
func TestPeerAdoptsADeliberateScroll(t *testing.T) {
	f := newScrollingFleet(t, 3, 100, 30)
	a, b := f.r.m, f.p.m

	focusOn(a, 0)
	f.settle()
	before := b.GetOrCreateScrollingLayout().ViewportX

	// What the wheel does on B, and the push every input makes after it.
	b.ScrollingScrollViewport(3)
	b.SyncStateToDaemon()
	f.settle()

	bx := b.GetOrCreateScrollingLayout().ViewportX
	ax := a.GetOrCreateScrollingLayout().ViewportX
	t.Logf("B scrolled the strip from %d to %d\n%s%s", before, bx, strip(a, "A"), strip(b, "B"))
	if bx == before {
		t.Fatalf("the wheel did not move B's own strip: still at %d", bx)
	}
	if ax != bx {
		t.Errorf("B scrolled the strip and A stayed at %d while B is at %d\n%s%s",
			ax, bx, strip(a, "A"), strip(b, "B"))
	}
}

// TestUnrelatedSyncLeavesTheStripAlone is the limit on the fix. A scroll away
// from the focused column is a decision, and every state push repeats the
// session's state, so a sync that changes something else must not read as a
// request to scroll back.
func TestUnrelatedSyncLeavesTheStripAlone(t *testing.T) {
	f := newScrollingFleet(t, 3, 100, 30)
	a, b := f.r.m, f.p.m

	// Focus the first column, then scroll deliberately to the far end of the
	// strip, which leaves the focused column off screen on both clients.
	focusOn(a, 0)
	f.settle()
	for range 5 {
		b.ScrollingScrollViewport(1)
	}
	b.SyncStateToDaemon()
	f.settle()

	parked := b.GetOrCreateScrollingLayout().ViewportX
	t.Logf("both clients parked away from the focused column\n%s%s", strip(a, "A"), strip(b, "B"))
	if fw := b.GetFocusedWindow(); fw != nil && fw.X+fw.Width > 0 {
		t.Fatalf("the focused column is still on screen, so this proves nothing:\n%s", strip(b, "B"))
	}

	// Something else changes on A: a pane is renamed. It is a state push like
	// any other and it says nothing about the strip.
	a.Windows[len(a.Windows)-1].CustomName = "renamed"
	a.SyncStateToDaemon()
	f.settle()

	t.Logf("after an unrelated sync\n%s%s", strip(a, "A"), strip(b, "B"))
	if got := b.GetOrCreateScrollingLayout().ViewportX; got != parked {
		t.Errorf("an unrelated sync dragged B's strip from %d back to %d\n%s", parked, got, strip(b, "B"))
	}
	if got := a.GetOrCreateScrollingLayout().ViewportX; got != parked {
		t.Errorf("an unrelated sync dragged A's strip from %d back to %d\n%s", parked, got, strip(a, "A"))
	}
}

// TestTwoColumnStepsBothReachThePeer walks the strip the way alt+p does, one
// column at a time, because a step is the gesture in the report and two of them
// in a row is where a lost sync would show.
func TestTwoColumnStepsBothReachThePeer(t *testing.T) {
	f := newScrollingFleet(t, 3, 100, 30)
	a, b := f.r.m, f.p.m

	focusOn(a, len(a.Windows)-1)
	f.settle()
	for step := range 2 {
		a.ScrollingFocusLeft()
		a.SyncStateToDaemon()
		f.settle()
		ax := a.GetOrCreateScrollingLayout().ViewportX
		bx := b.GetOrCreateScrollingLayout().ViewportX
		t.Logf("step %d: A at %d, B at %d\n%s%s", step+1, ax, bx, strip(a, "A"), strip(b, "B"))
		if ax != bx {
			t.Errorf("step %d: A is at %d and B at %d", step+1, ax, bx)
		}
	}
}

// TestJoiningClientLandsOnTheSessionsStrip is the attach half. A client that
// arrives at a scrolling session is looking at the same session as everyone
// else, so it starts where the strip is rather than at its left end.
func TestJoiningClientLandsOnTheSessionsStrip(t *testing.T) {
	f := newScrollingFleet(t, 3, 100, 30)
	a := f.r.m

	// Focus the last column and then scroll back off it a little, which parks
	// the strip somewhere no rule about the focused column would put it: the
	// focused column is still partly on screen, so a client that arrived and
	// only revealed the focus would land somewhere else.
	focusOn(a, len(a.Windows)-1)
	f.settle()
	a.ScrollingScrollViewport(-1)
	a.SyncStateToDaemon()
	f.settle()
	parked := a.GetOrCreateScrollingLayout().ViewportX
	if parked == 0 {
		t.Fatalf("the strip is at home, so a client joining could not tell:\n%s", strip(a, "A"))
	}

	c := joinPeerOS(t, f.r, 100, 30)
	f.ex.route(c.c, c.m, "C")
	f.settle()

	if got := c.m.GetOrCreateScrollingLayout().ViewportX; got != parked {
		t.Errorf("the joining client came up at %d while the session's strip is at %d\n%s%s",
			got, parked, strip(a, "A"), strip(c.m, "C"))
	}
}

// TestScrolledStripIsNotStale is the check underneath the test above, taken at
// the function rather than through a whole sync.
//
// The strip is longer than the screen, so the box test the other two layouts
// use answers "stale" for every settled strip that is not at its left end. What
// stale has to mean here is that a pane is somewhere this client's own strip
// does not put it, which is the same thing the box test means and the only
// thing it is for: catching a rectangle computed by somebody else.
func TestScrolledStripIsNotStale(t *testing.T) {
	prevAnim := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() { config.Global.AnimationsEnabled = prevAnim })

	m := nPaneTiledOS(t, 3, 160, 48)
	m.UseBSPLayout, m.UseScrollingLayout = false, true
	m.TileAllWindows()
	if m.tiledLayoutStale() {
		t.Fatalf("a strip this client just laid out reads as stale")
	}

	sl := m.GetOrCreateScrollingLayout()
	sl.ViewportX = 40
	m.ScrollingSetPositions()
	m.CompleteAllAnimations()
	if m.tiledLayoutStale() {
		t.Errorf("a strip scrolled to %d reads as stale, so every sync would retile "+
			"and drag it back to the focused column", sl.ViewportX)
	}

	// The direction it does have to catch: a pane holding a rectangle the strip
	// does not put it at, which is what adopting a peer's numbers looks like.
	m.Windows[0].X += 7
	if !m.tiledLayoutStale() {
		t.Error("a pane seven cells from where the strip puts it reads as settled")
	}
}

// TestTwoSizesShareOneStrip is the question a shared offset raises: a client
// whose screen is narrower than the offset assumes would be left looking at the
// wrong part of the strip, and a local correction on top of the shared value
// would have the two clients pushing it back and forth.
//
// It cannot happen, and this is why. The panes' box is settled across the
// session: the size is the minimum over the attached clients and the chrome
// reserve is the maximum, so GetContentWidth - the width every strip
// computation runs against - is the same number on every client whatever their
// terminals are. A wider client draws a blank band around the box, not more of
// the strip. So one offset shows every client the same columns, and there is
// nothing for a local correction to correct.
func TestTwoSizesShareOneStrip(t *testing.T) {
	f := newScrollingFleetSized(t, 3, 100, 30, 150, 44)
	a, b := f.r.m, f.p.m

	if aw, bw := a.ScrollingViewWidth(), b.ScrollingViewWidth(); aw != bw {
		t.Fatalf("the two clients' strips run at different widths (%d and %d), "+
			"so one offset would show them different columns", aw, bw)
	}

	focusOn(a, len(a.Windows)-1)
	f.settle()
	focusOn(a, 0)
	f.settle()
	t.Logf("a 100-wide client and a 150-wide client on one strip\n%s%s", strip(a, "A"), strip(b, "B"))

	asl, bsl := a.GetOrCreateScrollingLayout(), b.GetOrCreateScrollingLayout()
	if asl.ViewportX != bsl.ViewportX {
		t.Errorf("clients of different sizes ended up at different offsets: %d and %d",
			asl.ViewportX, bsl.ViewportX)
	}
	for _, c := range []struct {
		name string
		m    *OS
	}{{"A", a}, {"B", b}} {
		fw := c.m.GetFocusedWindow()
		if fw == nil {
			t.Fatalf("%s has no focused window", c.name)
		}
		if fw.X+fw.Width <= 0 || fw.X >= c.m.GetRenderWidth() {
			t.Errorf("%s draws the focused pane off its screen at x=%d w=%d:\n%s",
				c.name, fw.X, fw.Width, strip(c.m, c.name))
		}
	}
}

// TestWorkspaceSwitchLandsBothClientsOnOneStrip is the interaction with a
// workspace change arriving in the same sync. Each workspace keeps its own
// strip and the offset in a state belongs to the workspace that state names, so
// a client following a peer through a switch and back has to put each offset on
// the strip it was measured on.
//
// What it asserts is that both clients end up on one offset, on the right
// workspace. Whether that offset is the one the workspace was parked at is
// TestWorkspaceRoundTripKeepsTheParkedStrip, below.
func TestWorkspaceSwitchLandsBothClientsOnOneStrip(t *testing.T) {
	f := newScrollingFleet(t, 3, 100, 30)
	a, b := f.r.m, f.p.m

	// Parked away from where revealing the focused column would put it, so the
	// two clients agreeing afterwards cannot be a coincidence of both revealing.
	focusOn(a, len(a.Windows)-1)
	f.settle()
	a.ScrollingScrollViewport(-1)
	a.SyncStateToDaemon()
	f.settle()
	parked := a.GetOrCreateScrollingLayout().ViewportX
	if parked == 0 || b.GetOrCreateScrollingLayout().ViewportX != parked {
		t.Fatalf("the two clients did not start on one strip: A %d B %d",
			parked, b.GetOrCreateScrollingLayout().ViewportX)
	}

	// Away to an empty workspace, which has a strip of its own at its own
	// offset of zero, and back.
	a.SwitchToWorkspace(2)
	a.SyncStateToDaemon()
	f.settle()
	if b.CurrentWorkspace != 2 {
		t.Fatalf("the peer did not follow the workspace switch: it is on %d", b.CurrentWorkspace)
	}
	a.SwitchToWorkspace(1)
	a.SyncStateToDaemon()
	f.settle()

	if a.CurrentWorkspace != 1 || b.CurrentWorkspace != 1 {
		t.Fatalf("the clients are on workspaces %d and %d", a.CurrentWorkspace, b.CurrentWorkspace)
	}
	ax := a.GetOrCreateScrollingLayout().ViewportX
	bx := b.GetOrCreateScrollingLayout().ViewportX
	t.Logf("back on workspace 1, parked at %d before the trip\n%s%s", parked, strip(a, "A"), strip(b, "B"))
	if ax != bx {
		t.Errorf("the round trip left the two clients on different offsets: A %d, B %d", ax, bx)
	}
	for _, c := range []struct {
		name string
		m    *OS
	}{{"A", a}, {"B", b}} {
		fw := c.m.GetFocusedWindow()
		if fw == nil {
			t.Fatalf("%s came back with nothing focused", c.name)
		}
		if fw.X+fw.Width <= 0 || fw.X >= c.m.GetRenderWidth() {
			t.Errorf("%s came back with the focused pane off its screen at x=%d w=%d:\n%s",
				c.name, fw.X, fw.Width, strip(c.m, c.name))
		}
	}
}

// TestWorkspaceRoundTripKeepsTheParkedStrip is the report: scroll the strip,
// switch workspace, come back, and the scroll is gone.
//
// Each workspace keeps its own strip and nothing on the wire loses the offset.
// What lost it was local: switching to a workspace restores that workspace's
// saved focus, FocusWindow calls ScrollingOnFocusChange, and that used
// ScrollToFocusedColumn - the reveal the keyboard column steps use, which moves
// the strip to the focused column whether or not the user could already see it.
// So a round trip overwrote the offset in place.
//
// The two rows are the rule and its limit. A column the user can still see does
// not move the strip. A column with none of it on screen is revealed, which is
// what every retile does and what this fix does not change.
func TestWorkspaceRoundTripKeepsTheParkedStrip(t *testing.T) {
	for _, row := range []struct {
		name string
		// notches is how far the wheel takes the strip off the focused column.
		notches int
		// keep says the round trip must leave the offset exactly where it was.
		keep bool
	}{
		{"the focused column is still on screen", 1, true},
		{"the focused column is entirely off screen", 5, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			f := newScrollingFleet(t, 3, 100, 30)
			a, b := f.r.m, f.p.m

			// Focus the first column, then wheel the strip away from it. The
			// wheel is the gesture in the report and it is the one path that
			// moves the strip without moving the focus.
			focusOn(a, 0)
			f.settle()
			for range row.notches {
				a.ScrollingScrollViewport(1)
			}
			a.SyncStateToDaemon()
			f.settle()

			parked := a.GetOrCreateScrollingLayout().ViewportX
			if parked == 0 {
				t.Fatalf("the wheel did not move the strip, so the round trip proves nothing:\n%s",
					strip(a, "A"))
			}
			if bx := b.GetOrCreateScrollingLayout().ViewportX; bx != parked {
				t.Fatalf("the two clients did not start on one strip: A %d B %d", parked, bx)
			}
			onScreen := func(m *OS) bool {
				fw := m.GetFocusedWindow()
				return fw != nil && fw.X+fw.Width > 0 && fw.X < m.GetRenderWidth()
			}
			if onScreen(a) != row.keep {
				t.Fatalf("this row parks the strip in the wrong place: the focused column "+
					"on screen is %t, want %t\n%s", onScreen(a), row.keep, strip(a, "A"))
			}

			// Away to an empty workspace and back, which is the report's gesture.
			a.SwitchToWorkspace(2)
			a.SyncStateToDaemon()
			f.settle()
			a.SwitchToWorkspace(1)
			a.SyncStateToDaemon()
			f.settle()

			ax := a.GetOrCreateScrollingLayout().ViewportX
			bx := b.GetOrCreateScrollingLayout().ViewportX
			t.Logf("parked at %d, back at %d\n%s%s", parked, ax, strip(a, "A"), strip(b, "B"))
			if ax != bx {
				t.Errorf("the round trip left the clients on different offsets: A %d, B %d", ax, bx)
			}
			if row.keep {
				if ax != parked {
					t.Errorf("a workspace round trip moved the strip from %d to %d while the "+
						"focused column was on screen the whole time\n%s", parked, ax, strip(a, "A"))
				}
				return
			}
			// The limit: nothing of the focused column was on screen, so the
			// strip is allowed to move, and the column has to be there now.
			if ax == parked {
				t.Errorf("the round trip left the focused column off screen at %d\n%s", ax, strip(a, "A"))
			}
			for _, c := range []struct {
				name string
				m    *OS
			}{{"A", a}, {"B", b}} {
				if !onScreen(c.m) {
					fw := c.m.GetFocusedWindow()
					t.Errorf("%s came back with the focused pane off its screen at x=%d w=%d:\n%s",
						c.name, fw.X, fw.Width, strip(c.m, c.name))
				}
			}
		})
	}
}
