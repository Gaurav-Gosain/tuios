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
// panes' box is the same on every client (the session's size is the minimum
// over them and the chrome reserve the maximum), so one offset shows every
// client the same columns.
//
// NEGATIVE CONTROLS, each run by mutating the shipped code and watching the
// named assertion fail:
//
//   - ApplyStateSync not calling adoptScrollStrip:
//     TestPeerAdoptsADeliberateScroll fails saying A stayed at 0 while B is
//     at 60. The focus half of the same fault is e2e
//     TestScrollingPeerFollowsFocusIntoView.
//   - BuildSessionState not sending the strip: TestPeerAdoptsADeliberateScroll
//     fails.
//
// Three controls recorded here named unit tests that were removed in favour
// of e2e coverage (scroll_strip_workspace_test.go and
// scroll_peer_focus_test.go). They have not been rerun against those e2e
// tests, so read them as the faults to check for, not as measured results:
// RestoreFromState not taking the strip (a joining client lands at 65 while
// the session is at 45), tiledLayoutStale measuring the strip against the box
// again (a sync that only renamed a pane drags both clients back to 0), and
// scrollingLayoutStale never answering true (a pane left seven cells from
// where the strip puts it).
//   - adoptScrollStrip run against the workspace the sync arrived from rather
//     than the one it names: TestWorkspaceSwitchLandsBothClientsOnOneStrip
//     fails with the two clients on 65 and 45 after a round trip through
//     another workspace.
//   - ScrollingOnFocusChange calling ScrollToFocusedColumn again, and
//     EnsureFocusedVisible given the peek margin or never revealing, are
//     caught end to end by TestWorkspaceRoundTripKeepsTheScrolledStrip and
//     TestWorkspaceRoundTripRevealsAHiddenColumn in e2e/tui.
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

// TestTwoSizesShareOneStrip is the question a shared offset raises: a client
// whose screen is narrower than the offset assumes would be left looking at the
// wrong part of the strip, and a local correction on top of the shared value
// would have the two clients pushing it back and forth.
//
// It cannot happen, and this is why. The panes' box is settled across the
// session: the size is the minimum over the attached clients and the chrome
// reserve is the maximum, so GetContentWidth (the width every strip
// computation runs against) is the same number on every client whatever their
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
// e2e TestWorkspaceRoundTripKeepsTheScrolledStrip.
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
