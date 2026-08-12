package app

import (
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// videoDragHarness is a headless ssh browser-pane rig: a self-placed remote
// video stream for one window, a mutable window info the test drives, and a
// refresh that runs one render pass and flushes it to the host.
type videoDragHarness struct {
	kp      *KittyPassthrough
	host    *recWriter
	shmName string
	winID   string
	info    *WindowPositionInfo
	infos   map[string]*WindowPositionInfo
	send    func()
}

// newVideoDragHarness establishes the stream. Geometry: 400x300px frame at
// 10x20 cells = 40 cols x 15 rows; window 100x30 with a 1-cell border on a
// 200x60 screen; guest cursor at (0,0).
//
// out is the writer handed to the passthrough and rec the recorder used for
// assertions; they are the same object except when a test wraps the recorder
// (gatedWriter).
func newVideoDragHarness(t *testing.T, out io.Writer, rec *recWriter) *videoDragHarness {
	t.Helper()
	host := rec
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})
	shmName := makeShmFrame(t, 400, 300)
	cmd, raw := synthShmTransmitPlace(shmName, 400, 300)
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: out, RemoteClient: true})

	const winID = "window-0000-0000-0000-000000000000"
	send := func() {
		kp.ForwardCommand(cmd, raw, winID, 0, 0, 100, 30, 1, 1, 0, 0, 0, false, func([]byte) {})
	}
	send() // frame 1: establishes the id via the placement path
	send() // frame 2: reused -> handed off to the self-placing path
	if !waitUntil(func() bool { return host.has("a=T,i=") }, 2*time.Second) {
		t.Fatal("remote video stream never self-placed")
	}

	info := &WindowPositionInfo{
		WindowX: 0, WindowY: 0, ContentOffsetX: 1, ContentOffsetY: 1,
		Width: 100, Height: 30, Visible: true,
		ScreenWidth: 200, ScreenHeight: 60, IsAltScreen: false,
	}
	return &videoDragHarness{
		kp: kp, host: host, shmName: shmName, winID: winID, info: info,
		infos: map[string]*WindowPositionInfo{winID: info}, send: send,
	}
}

// refresh runs one render pass and returns what it forwarded to the host.
func (h *videoDragHarness) refresh() string {
	h.kp.RefreshAllPlacements(func() map[string]*WindowPositionInfo { return h.infos })
	out := h.kp.FlushPending()
	if len(out) > 0 {
		h.kp.WriteToHost(out)
	}
	return string(out)
}

// TestRemoteVideoTracksDragCoalesced proves the drag contract for a self-placed
// video image: however many mouse events move the window between two render
// passes, one pass emits exactly ONE a=p, carrying the FINAL position, and a
// pass with unchanged geometry emits nothing.
func TestRemoteVideoTracksDragCoalesced(t *testing.T) {
	rec := &recWriter{}
	h := newVideoDragHarness(t, rec, rec)

	_ = h.refresh() // prime: records show geometry at the resting spot

	// A drag updates the window position on every mouse event; the render loop
	// only runs refresh once per frame. All intermediate positions must
	// coalesce into a single a=p at the last one.
	h.info.IsBeingManipulated = true
	for _, p := range [][2]int{{5, 2}, {11, 4}, {17, 3}} {
		h.info.WindowX, h.info.WindowY = p[0], p[1]
	}
	out := h.refresh()
	if n := countCmd(out, "a=p"); n != 1 {
		t.Fatalf("drag tick emitted %d a=p, want exactly 1 (coalesced): %q", n, out)
	}
	// Window (17,3), border 1, guest (0,0) -> host cell (18,4) -> CUP row 5 col 19.
	if !strings.Contains(out, "\x1b[5;19H") {
		t.Fatalf("a=p not at the window's current content origin: %q", out)
	}

	// Same geometry again: nothing may be re-emitted (no per-tick spam).
	if out := h.refresh(); countCmd(out, "a=p")+countCmd(out, "a=d") != 0 {
		t.Fatalf("refresh with unchanged geometry churned the placement: %q", out)
	}
}

// TestRemoteVideoFreezesDuringResize proves a self-placed video image follows
// the same interactive-resize discipline as regular placements: while the
// gesture changes the window SIZE (PTY resize deferred, guest still drawing the
// old size), the image is held untouched - no a=p per tick even though the
// window origin moves - and when the gesture settles it re-places exactly once
// at the final geometry.
//
// Pre-fix this failed twice over: remote video windows never got a
// resizeFreezeSize record (the image is not in `placements`), and the remote
// video loop re-placed on every position change regardless of manipulation.
func TestRemoteVideoFreezesDuringResize(t *testing.T) {
	rec := &recWriter{}
	h := newVideoDragHarness(t, rec, rec)

	_ = h.refresh() // prime: records the settled size for the freeze baseline

	// Top-left corner drag: both origin and size change every tick.
	h.info.IsBeingManipulated = true
	for _, g := range [][4]int{{2, 1, 96, 28}, {4, 2, 92, 26}, {6, 3, 88, 24}} {
		h.info.WindowX, h.info.WindowY, h.info.Width, h.info.Height = g[0], g[1], g[2], g[3]
		if out := h.refresh(); countCmd(out, "a=p")+countCmd(out, "a=d") != 0 {
			t.Fatalf("mid-resize tick emitted placement traffic (flicker): %q", out)
		}
	}

	// Gesture settles: exactly one re-place at the final geometry.
	h.info.IsBeingManipulated = false
	out := h.refresh()
	if n := countCmd(out, "a=p"); n != 1 {
		t.Fatalf("after resize settled, expected exactly one a=p, got %d: %q", n, out)
	}
	// Window (6,3), border 1 -> host cell (7,4) -> CUP row 5 col 8.
	if !strings.Contains(out, "\x1b[5;8H") {
		t.Fatalf("settled a=p not at the final content origin: %q", out)
	}
}

// TestResizeHidesGraphicsAndRestoresThem proves a pane's images are taken off
// screen for the length of a resize gesture and put back when it ends. An image
// occupies host cells that the guest has not been told about yet while the
// layout is moving, so left up it smears across the resizing panes. It goes
// through the same hide path an overlay uses, keeping the image data resident,
// so coming back costs one a=p and no round trip to whatever drew it.
func TestResizeHidesGraphicsAndRestoresThem(t *testing.T) {
	rec := &recWriter{}
	h := newVideoDragHarness(t, rec, rec)
	_ = h.refresh() // prime: the image is placed and visible

	m := &OS{KittyPassthrough: h.kp}

	hidden := rec.count("a=d,d=i,i=")
	m.Resizing = true
	m.flushGraphicsForView()
	if got := rec.count("a=d,d=i,i="); got != hidden+1 {
		t.Fatalf("resize did not hide the image: a=d,d=i count %d -> %d, want one more", hidden, got)
	}

	// Further frames mid-gesture must not put it back on screen.
	shown := rec.count("a=p")
	m.flushGraphicsForView()
	if got := rec.count("a=p"); got != shown {
		t.Fatalf("a mid-resize frame re-showed the image: a=p count %d -> %d", shown, got)
	}

	m.Resizing = false
	m.flushGraphicsForView()
	if got := rec.count("a=p"); got != shown+1 {
		t.Fatalf("the image was not restored after the resize: a=p count %d -> %d, want one more", shown, got)
	}
}

// gatedWriter is a recWriter whose next Write (once armed) blocks until
// released, to hold an async video frame mid-write while the test moves the
// window underneath it.
type gatedWriter struct {
	recWriter
	armed   atomic.Bool
	blocked chan struct{}
	release chan struct{}
}

func newGatedWriter() *gatedWriter {
	return &gatedWriter{blocked: make(chan struct{}), release: make(chan struct{})}
}

func (w *gatedWriter) Write(p []byte) (int, error) {
	if w.armed.CompareAndSwap(true, false) {
		close(w.blocked)
		<-w.release
	}
	return w.recWriter.Write(p)
}

// TestRemoteVideoFrameConvergesAfterMidWriteMove reproduces the drag fight
// between the async a=T frame stream and the render loop's a=p reposition: a
// frame is in flight while the window moves, the reposition reaches the host
// first (or, equivalently, its flush already happened), and the late frame
// paints the image back at the stale position with nothing left to correct it.
// The image visibly snaps behind the pointer and sticks there.
//
// The fix resolves the frame's position at write time and, if the desired
// geometry changed while the frame was being written, follows up with a
// corrective a=p, so the host always converges on the window's current content
// rectangle.
func TestRemoteVideoFrameConvergesAfterMidWriteMove(t *testing.T) {
	host := newGatedWriter()
	h := newVideoDragHarness(t, host, &host.recWriter)

	_ = h.refresh() // prime at the resting position

	// Arm the gate and send a changed frame: the async writer picks it up and
	// blocks inside the host write.
	host.armed.Store(true)
	rewriteShm(t, h.shmName, 400, 300, 77)
	h.send()
	select {
	case <-host.blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("async frame writer never reached the host write")
	}

	// The window moves while the frame is mid-write. The render pass records
	// the new desired position, but its own a=p flush is NOT written to the
	// host, modeling the production ordering where it reached the host before
	// the in-flight frame did.
	h.info.WindowX, h.info.WindowY = 30, 10
	h.kp.RefreshAllPlacements(func() map[string]*WindowPositionInfo { return h.infos })
	_ = h.kp.FlushPending()

	close(host.release)

	// Window (30,10), border 1 -> host cell (31,11) -> CUP row 12 col 32. The
	// frame stream must leave the host at that position, not the stale one.
	if !waitUntil(func() bool { return host.has("\x1b[12;32H") }, 2*time.Second) {
		t.Fatal("after a mid-write move, the host was left with the image at the stale position; " +
			"the frame stream and the reposition are fighting")
	}
}

// TestRemoteVideoClampsAtScreenEdge proves a dragged video pane is clipped at
// the screen edges the same way regular placements are: the visible cell area
// shrinks (with a matching source-rect crop) and never reaches the final screen
// row, which would make the host terminal scroll and cascade duplicate frames.
func TestRemoteVideoClampsAtScreenEdge(t *testing.T) {
	rec := &recWriter{}
	h := newVideoDragHarness(t, rec, rec)

	_ = h.refresh() // prime

	// Bottom-right: host origin (171,51) on a 200x60 screen; the 40x15 image
	// must clamp to 29 cols x 8 rows (last row kept free) with a matching crop.
	h.info.WindowX, h.info.WindowY = 170, 50
	out := h.refresh()
	if countCmd(out, "a=p") != 1 {
		t.Fatalf("edge move did not re-place exactly once: %q", out)
	}
	for _, want := range []string{",c=29", ",r=8", ",w=290", ",h=160"} {
		if !strings.Contains(out, want) {
			t.Fatalf("edge placement missing %q (image would scroll the host screen): %q", want, out)
		}
	}
}

// TestRemoteVideoHidesOffscreenAndOccluded proves a self-placed video image is
// hidden (data kept resident) when a higher window covers it, that frames
// arriving while hidden are dropped instead of painting over the occluder, and
// that it re-shows with a plain a=p when visible again.
func TestRemoteVideoHidesOffscreenAndOccluded(t *testing.T) {
	host := &recWriter{}
	h := newVideoDragHarness(t, host, host)
	_ = h.refresh() // prime

	// A higher-z window covers the pane: the image must be hidden with d=i.
	h.infos["occluder"] = &WindowPositionInfo{
		WindowX: 0, WindowY: 0, Width: 200, Height: 60, Visible: true,
		ScreenWidth: 200, ScreenHeight: 60, WindowZ: 5,
	}
	out := h.refresh()
	if !strings.Contains(out, "a=d,d=i,i=") {
		t.Fatalf("occluded video image was not hidden; it draws over the covering window: %q", out)
	}

	// Frames arriving while hidden must be dropped, not painted.
	placed := host.count("a=T,i=")
	rewriteShm(t, h.shmName, 400, 300, 123)
	h.send()
	time.Sleep(150 * time.Millisecond)
	if host.count("a=T,i=") != placed {
		t.Fatal("a frame painted while the image was occluded")
	}

	// Occluder gone: the image must re-show via a=p from resident data.
	delete(h.infos, "occluder")
	out = h.refresh()
	if countCmd(out, "a=p") != 1 {
		t.Fatalf("uncovered video image did not re-show with a single a=p: %q", out)
	}
}
