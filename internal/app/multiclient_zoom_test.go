package app

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Zoom is shared session state, and these are the two halves of what that has
// to mean with two people looking at one session.
//
// A pane's PTY has one size, and zooming resizes it: toggleZoom routes the
// zoomed rectangle through Window.Resize. So a client that does not know a pane
// is zoomed is not merely drawing a different picture, it is drawing a guest
// grid the shell is not running in. Worse, it counts the pane among the
// tiled ones, tiles it back and pushes that, which takes the zoom away from the
// person who asked for it.
//
// What travels is the flag. The rectangle does not: a zoomed pane covers the
// content region of whichever client zoomed it, and every client works its own
// out from the box the session agreed on, the same way it works out a tiled
// layout.

// twoClientsOnOneTiledSession brings up a tiled session with two full clients
// on it, each with its own connection, its own OS and its own chrome, and
// leaves the box settled and every pane's size agreed.
//
// The rails are on opposite edges on purpose. That is what makes the reserve
// negotiation load-bearing, and the zoom box is measured against the negotiated
// reserve rather than either client's own chrome, so a rail nobody shares is
// exactly the way a zoomed rectangle could come out different on the two sides.
func twoClientsOnOneTiledSession(t *testing.T) (*rig, *peer, *exchange) {
	return clientsOnOneTiledSession(t, 2, false)
}

// panes is how many shells the session starts with, and shared is whether the
// clients draw one border between two panes instead of one each. Shared borders
// take the titles off the panes, so a test that reads a name out of the frame
// asks for them off and the one about the dividers asks for them on.
func clientsOnOneTiledSession(t *testing.T, panes int, shared bool) (*rig, *peer, *exchange) {
	t.Helper()
	prevAnim := config.Global.AnimationsEnabled
	prevEnabled := config.Global.SidebarEnabled
	prevWidth := config.Global.SidebarWidth
	prevPos := config.Global.SidebarPosition
	prevShared := config.Global.SharedBorders
	config.Global.SharedBorders = shared
	config.Global.AnimationsEnabled = false
	config.Global.SidebarEnabled = true
	config.Global.SidebarWidth = 24
	config.Global.SidebarPosition = "left"
	t.Cleanup(func() {
		config.Global.AnimationsEnabled = prevAnim
		config.Global.SidebarEnabled = prevEnabled
		config.Global.SidebarWidth = prevWidth
		config.Global.SidebarPosition = prevPos
		config.Global.SharedBorders = prevShared
	})

	r := newRigSized(t, panes, holderCols, holderRows)
	r.tile()

	// The tiling topology reaches the daemon before the second client attaches.
	// A tree is adopted from the attach reply and from a strictly newer daemon
	// state, never from a peer's push, so a client that joins a session no one
	// has pushed builds its own tree against its own box, and two clients on
	// two trees disagree about every rectangle, zoom or no zoom. That is a
	// divergence of its own, not this one.
	var daemonState atomic.Pointer[session.SessionState]
	r.ctl.OnStateSync(func(st *session.SessionState, _, _ string) { daemonState.Store(st) })
	r.m.SyncStateToDaemon()
	deadline := time.Now().Add(rigWait)
	for {
		if st := daemonState.Load(); st != nil && len(st.WorkspaceTrees) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the tiled session never reached the daemon")
		}
		time.Sleep(5 * time.Millisecond)
	}

	config.Global.SidebarPosition = "right"
	p := joinPeerOS(t, r, holderCols-14, holderRows-4)
	p.m.AutoTiling = true
	config.Global.SidebarPosition = "left"

	ex := &exchange{t: t}
	ex.route(r.client, r.m, "A")
	ex.route(p.c, p.m, "B")

	r.m.AnnounceLayoutReserve()
	p.m.AnnounceLayoutReserve()
	ex.settleBox(r, p)

	p.m.TileAllWindows()
	ex.settle(60, 300*time.Millisecond)
	return r, p, ex
}

// nameZoomPanes gives the two panes names a frame can be read for, so the proof
// is what is on the screen rather than a flag read back out of the model.
func nameZoomPanes(t *testing.T, r *rig, ex *exchange) (string, string) {
	t.Helper()
	r.m.Windows[0].CustomName = "ZOOMED"
	if len(r.m.Windows) > 1 {
		r.m.Windows[1].CustomName = "HIDDEN"
	}
	r.m.MarkAllDirty()
	r.m.SyncStateToDaemon()
	ex.settle(60, 300*time.Millisecond)
	if len(r.m.Windows) == 1 {
		return r.m.Windows[0].PTYID, ""
	}
	return r.m.Windows[0].PTYID, r.m.Windows[1].PTYID
}

func winByPTY(m *OS, ptyID string) *terminal.Window {
	for _, w := range m.Windows {
		if w.PTYID == ptyID {
			return w
		}
	}
	return nil
}

// paneFrame is the composed frame cropped to the box the panes go in. The rail
// is left out on purpose: it lists every pane by name whatever the layout is
// doing, so a frame read whole says "HIDDEN is on screen" for a pane that
// is not being drawn at all.
func paneFrame(m *OS) string {
	left, width := m.GetLeftMargin(), m.GetContentWidth()
	var b strings.Builder
	for _, row := range strings.Split(frame(m), "\n") {
		r := []rune(row)
		if len(r) <= left {
			b.WriteString("\n")
			continue
		}
		r = r[left:]
		if len(r) > width {
			r = r[:width]
		}
		b.WriteString(string(r))
		b.WriteString("\n")
	}
	return b.String()
}

func rectOf(w *terminal.Window) string {
	if w == nil {
		return "<missing>"
	}
	return fmt.Sprintf("@%d,%d %dx%d guest %dx%d zoom=%t", w.X, w.Y, w.Width, w.Height, w.ContentWidth(), w.ContentHeight(), w.Zoomed)
}

// TestZoomTravelsToTheOtherClient is the first half: A zooms, and B has to show
// the same pane filling its own box and must not tile it away.
//
// NEGATIVE CONTROL: measured. Dropping Zoomed from BuildSessionState (the one
// line that puts the flag on the wire) fails it with B holding the pane at
// "@24,2 82x30 guest 80x28 zoom=false" against A's "@24,2 82x30 zoom=true", and
// B's frame still carrying HIDDEN beside it. That is the whole bug in one
// line: the rectangle arrived, the reason did not, and B is one retile away
// from taking the zoom off A.
func TestZoomTravelsToTheOtherClient(t *testing.T) {
	r, p, ex := twoClientsOnOneTiledSession(t)
	zoomPTY, _ := nameZoomPanes(t, r, ex)

	t.Logf("before, A: %s", rects(r.m))
	t.Logf("before, B: %s", rects(p.m))

	r.m.FocusedWindow = 0
	r.m.ToggleZoom()
	r.m.SyncStateToDaemon()
	ex.settle(60, 300*time.Millisecond)

	a := winByPTY(r.m, zoomPTY)
	b := winByPTY(p.m, zoomPTY)
	t.Logf("after A zoomed, A: %s", rectOf(a))
	t.Logf("after A zoomed, B: %s", rectOf(b))

	if b == nil || !b.Zoomed {
		t.Fatalf("B did not take the zoom: %s", rectOf(b))
	}
	if a.X != b.X || a.Y != b.Y || a.Width != b.Width || a.Height != b.Height {
		t.Fatalf("the two clients put the zoomed pane in different boxes:\n A %s\n B %s", rectOf(a), rectOf(b))
	}

	// The box is the one the session agreed on, not either client's own. Read
	// off B, which is the client that did not zoom.
	bounds := p.m.GetBSPBounds()
	if b.X != bounds.X || b.Y != bounds.Y || b.Width != bounds.W || b.Height != bounds.H {
		t.Fatalf("B's zoom box %s is not the box the panes go in %+v", rectOf(b), bounds)
	}

	// The pane B tiled away is the failure this exists for, so it is asserted
	// on the frame rather than on the flag.
	fa, fb := paneFrame(r.m), paneFrame(p.m)
	t.Logf("A's frame while zoomed:\n%s", fa)
	t.Logf("B's frame while zoomed:\n%s", fb)
	if !strings.Contains(fb, "ZOOMED") {
		t.Fatalf("B is not drawing the zoomed pane:\n%s", fb)
	}
	if strings.Contains(fb, "HIDDEN") {
		t.Fatalf("B is still drawing the tiled layout underneath the zoom:\n%s", fb)
	}

	// A PTY has one size, and the zoom just changed it.
	dw, dh := r.ptySize(zoomPTY)
	if dw != a.ContentWidth() || dh != a.ContentHeight() || dw != b.ContentWidth() || dh != b.ContentHeight() {
		t.Fatalf("pane %s: the daemon runs the shell at %dx%d, A draws it at %dx%d, B at %dx%d",
			shortID(zoomPTY), dw, dh, a.ContentWidth(), a.ContentHeight(), b.ContentWidth(), b.ContentHeight())
	}
}

// TestUnzoomOutsideTilingRestoresTheRectangle is why the pre-zoom rectangle is
// on the wire beside the flag.
//
// With tiling on, the rectangle a pane comes back to is the layout's and the
// pre-zoom one is only a shortcut. With tiling off nothing will ever place the
// pane again, so the rectangle it was zoomed from is the only record of where it
// belongs, and the client doing the unzooming need not be the client that made
// the record. That is the same argument the four PreMinimize fields are already
// synced for.
//
// NEGATIVE CONTROL: measured. Dropping the pre-zoom four from BuildSessionState
// fails it with B putting the pane back at 0x0.
func TestUnzoomOutsideTilingRestoresTheRectangle(t *testing.T) {
	r, p, ex := twoClientsOnOneTiledSession(t)
	zoomPTY, _ := nameZoomPanes(t, r, ex)

	// Tiling off on both, which is a session-level setting and travels.
	r.m.AutoTiling = false
	r.m.SyncStateToDaemon()
	ex.settle(60, 300*time.Millisecond)
	if p.m.AutoTiling {
		t.Fatalf("B did not take the tiling setting")
	}

	before := rectOf(winByPTY(r.m, zoomPTY))
	t.Logf("before the zoom, A: %s", before)

	r.m.FocusedWindow = 0
	r.m.ToggleZoom()
	r.m.SyncStateToDaemon()
	ex.settle(60, 300*time.Millisecond)
	t.Logf("while zoomed, A: %s", rectOf(winByPTY(r.m, zoomPTY)))
	t.Logf("while zoomed, B: %s", rectOf(winByPTY(p.m, zoomPTY)))

	for i, w := range p.m.Windows {
		if w.PTYID == zoomPTY {
			p.m.FocusedWindow = i
		}
	}
	p.m.ToggleZoom()
	p.m.SyncStateToDaemon()
	ex.settle(60, 300*time.Millisecond)

	after := rectOf(winByPTY(p.m, zoomPTY))
	t.Logf("after B unzoomed, B: %s", after)
	t.Logf("after B unzoomed, A: %s", rectOf(winByPTY(r.m, zoomPTY)))
	if after != before {
		t.Fatalf("B put the pane back somewhere else:\n before %s\n after  %s", before, after)
	}
	if got := rectOf(winByPTY(r.m, zoomPTY)); got != before {
		t.Fatalf("A did not follow B out of the zoom:\n want %s\n got  %s", before, got)
	}
}

// focusElsewhere points a client's focus at some pane other than the zoomed one
// without telling anybody, which is the state every reader that used to ask
// GetFocusedWindow().Zoomed got wrong.
//
// It is not a contrived state. Focus travels in the same broadcast the zoom
// does and a client applies the two a step apart; a client sitting in its
// sidebar has no focused pane at all; and a client whose focused id is not in
// the list it was handed holds -1. Each of those is a frame in which "is the
// focused pane zoomed" answers no while a pane is zoomed.
func focusElsewhere(t *testing.T, m *OS, zoomPTY string) {
	t.Helper()
	for i, w := range m.Windows {
		if w.PTYID != zoomPTY {
			m.FocusedWindow = i
			m.MarkAllDirty()
			return
		}
	}
	t.Fatal("there is no other pane to focus")
}

// TestAPeersZoomIsDrawnWhoeverIsFocused is the render half of the same
// question. B holds the zoom flag for a pane it is not focused on, which is what
// a shared zoom looks like for the moment between two broadcasts, and it still
// has to draw that pane over everything.
//
// NEGATIVE CONTROL: measured. Putting render.go back on
// GetFocusedWindow().Zoomed fails it with B drawing the whole tiled layout
// (HIDDEN beside ZOOMED) underneath a pane that is covering the box
// on every other client.
func TestAPeersZoomIsDrawnWhoeverIsFocused(t *testing.T) {
	r, p, ex := twoClientsOnOneTiledSession(t)
	zoomPTY, _ := nameZoomPanes(t, r, ex)

	r.m.FocusedWindow = 0
	r.m.ToggleZoom()
	r.m.SyncStateToDaemon()
	ex.settle(60, 300*time.Millisecond)

	focusElsewhere(t, p.m, zoomPTY)
	fb := paneFrame(p.m)
	t.Logf("B's frame while zoomed, focused on the other pane:\n%s", fb)
	if !strings.Contains(fb, "ZOOMED") {
		t.Fatalf("B stopped drawing the zoomed pane when its focus moved:\n%s", fb)
	}
	if strings.Contains(fb, "HIDDEN") {
		t.Fatalf("B drew the tiled layout underneath a pane the session has zoomed:\n%s", fb)
	}

	// The other shape of the same moment: a client with nothing focused at all,
	// which is what a sidebar has the focus or a focused id that is not in the
	// list leaves behind.
	p.m.FocusedWindow = -1
	p.m.MarkAllDirty()
	fb = paneFrame(p.m)
	if !strings.Contains(fb, "ZOOMED") || strings.Contains(fb, "HIDDEN") {
		t.Fatalf("B with nothing focused stopped drawing the zoom:\n%s", fb)
	}
}

// TestNoDividersAcrossAPeersZoom is the same question for the shared borders.
// The divider grid is drawn from the tiling splits, which are still there
// behind a zoom, so the overlay has to be told a zoom is up, and it has to be
// told by the session rather than by this client's focus.
//
// NEGATIVE CONTROL: measured. Putting renderSeparatorOverlay back on
// GetFocusedWindow().Zoomed fails it with a divider column drawn down the middle
// of the zoomed pane.
func TestNoDividersAcrossAPeersZoom(t *testing.T) {
	r, p, ex := clientsOnOneTiledSession(t, 2, true)
	zoomPTY, _ := nameZoomPanes(t, r, ex)

	// The dividers are there to begin with, or the test asserts nothing.
	focusElsewhere(t, p.m, zoomPTY)
	if n := len(p.m.renderSeparatorOverlay()); n == 0 {
		t.Fatalf("this session draws no dividers, so there is nothing to keep off the zoom")
	} else {
		t.Logf("tiled, B draws %d divider layers", n)
	}

	r.m.FocusedWindow = 0
	r.m.ToggleZoom()
	r.m.SyncStateToDaemon()
	ex.settle(60, 300*time.Millisecond)

	focusElsewhere(t, p.m, zoomPTY)
	if n := len(p.m.renderSeparatorOverlay()); n != 0 {
		t.Fatalf("B drew %d divider layers across a pane the session has zoomed", n)
	}
	fb := paneFrame(p.m)
	t.Logf("B's frame while zoomed, shared borders on:\n%s", fb)
	if strings.Contains(fb, "│") {
		t.Fatalf("B drew a divider across the zoomed pane:\n%s", fb)
	}
}

// TestZoomOfTheOnlyPaneStillTravels is the case where the flag is the entire
// news. One pane on a workspace already fills the box, so zooming it moves
// nothing: the rectangle before and the rectangle after are the same numbers,
// and a client only pushes when its state's fingerprint has changed.
//
// NEGATIVE CONTROL: measured. Taking Zoomed back out of StateFingerprint fails
// it: A's push is suppressed as a no-op and B never hears that anything
// happened.
func TestZoomOfTheOnlyPaneStillTravels(t *testing.T) {
	r, p, ex := clientsOnOneTiledSession(t, 1, false)
	zoomPTY, _ := nameZoomPanes(t, r, ex)

	before := rectOf(winByPTY(r.m, zoomPTY))
	r.m.FocusedWindow = 0
	r.m.ToggleZoom()
	after := rectOf(winByPTY(r.m, zoomPTY))
	t.Logf("A before %s", before)
	t.Logf("A after  %s", after)

	r.m.SyncStateToDaemon()
	ex.settle(60, 300*time.Millisecond)

	b := winByPTY(p.m, zoomPTY)
	t.Logf("B after  %s", rectOf(b))
	if b == nil || !b.Zoomed {
		t.Fatalf("B never heard about the zoom: %s", rectOf(b))
	}
}

// TestAClientAttachingFindsTheZoom is the third client's view. A session is not
// only the clients that were there when something happened: somebody opening a
// second terminal has to walk in on the zoom, because the shell they are about
// to draw is running at the zoomed size whether they know it or not.
//
// It is a different carry path from the sync one (the attach reply is restored
// by RestoreFromState, which builds every window from scratch), and it is a path
// that has gone missing on its own before (see adoptWindowState's own comment about
// the agents section).
//
// NEGATIVE CONTROL: measured. Taking the flag back out of adoptWindowState fails
// it with C attached, drawing the pane at the zoom box and calling it a tiled
// layout, one retile away from taking the zoom off everybody.
func TestAClientAttachingFindsTheZoom(t *testing.T) {
	r, _, ex := twoClientsOnOneTiledSession(t)
	zoomPTY, _ := nameZoomPanes(t, r, ex)

	r.m.FocusedWindow = 0
	r.m.ToggleZoom()
	r.m.SyncStateToDaemon()
	ex.settle(60, 300*time.Millisecond)

	c := joinPeerOS(t, r, holderCols, holderRows)
	c.m.AutoTiling = true
	ex.route(c.c, c.m, "C")
	c.m.AnnounceLayoutReserve()
	ex.settle(60, 300*time.Millisecond)

	cw := winByPTY(c.m, zoomPTY)
	t.Logf("C on attaching: %s", rectOf(cw))
	t.Logf("A: %s", rectOf(winByPTY(r.m, zoomPTY)))
	if cw == nil || !cw.Zoomed {
		t.Fatalf("C attached without the zoom: %s", rectOf(cw))
	}
	bounds := c.m.GetBSPBounds()
	if cw.X != bounds.X || cw.Y != bounds.Y || cw.Width != bounds.W || cw.Height != bounds.H {
		t.Fatalf("C put the zoomed pane at %s, not in the box the panes go in %+v", rectOf(cw), bounds)
	}

	fc := paneFrame(c.m)
	t.Logf("C's first frame:\n%s", fc)
	if !strings.Contains(fc, "ZOOMED") || strings.Contains(fc, "HIDDEN") {
		t.Fatalf("C did not come up on the zoom:\n%s", fc)
	}

	dw, dh := r.ptySize(zoomPTY)
	if dw != cw.ContentWidth() || dh != cw.ContentHeight() {
		t.Fatalf("the daemon runs the shell at %dx%d and C draws it at %dx%d",
			dw, dh, cw.ContentWidth(), cw.ContentHeight())
	}
}
