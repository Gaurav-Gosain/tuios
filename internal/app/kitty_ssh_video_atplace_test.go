package app

import (
	"bytes"
	"compress/zlib"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// recWriter captures host output, safe for the async frame-writer goroutine to
// write while the test reads.
type recWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *recWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *recWriter) has(sub string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Contains(w.buf.Bytes(), []byte(sub))
}

func waitUntil(cond func() bool, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// TestRemoteVideoSelfPlacesWithAT proves the remote-terminal video path: a reused
// image id (a browser/video stream) is re-sent as a self-contained a=T
// (transmit AND place) at a fixed cursor position, not a bare a=t that a real
// kitty would never repaint, and the image is taken out of `placements` so the
// render loop cannot race it. Pre-fix, reused frames were transmit-only.
func TestRemoteVideoSelfPlacesWithAT(t *testing.T) {
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})

	shmName := makeShmFrame(t, 400, 300)
	cmd, raw := synthShmTransmitPlace(shmName, 400, 300)

	host := &recWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host, RemoteClient: true})

	const winID = "window-0000-0000-0000-000000000000"
	send := func() {
		kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, false, func([]byte) {})
	}

	send() // frame 1: establishes the id (non-reused, goes to pendingOutput)
	send() // frame 2: reused -> self-placed a=T on the async writer

	if !waitUntil(func() bool { return host.has("a=T,i=") }, 2*time.Second) {
		t.Fatal("remote video: no self-placed a=T reached the host; a real terminal would not repaint")
	}
	if !host.has("\x1b7") || !host.has("\x1b8") {
		t.Fatal("remote video a=T is not wrapped in cursor save/restore")
	}
	if host.has("a=t,i=") {
		t.Fatal("remote video emitted a transmit-only a=t; the host will not repaint it")
	}

	// The reused image must be handed off to the self-placing path, i.e. removed
	// from placements so RefreshAllPlacements cannot delete-and-replace it.
	kp.mu.Lock()
	_, stillTracked := kp.placements[winID][hostIDForGuest(kp, winID, 1)]
	inRemoteVideo := len(kp.remoteVideo[winID]) > 0
	kp.mu.Unlock()
	if stillTracked {
		t.Fatal("reused remote-video image is still in placements; the render loop will race it")
	}
	if !inRemoteVideo {
		t.Fatal("reused remote-video image was not recorded for cleanup")
	}
}

// TestInlineOverlayVideoStaysTransmitOnly is the negative control: the tuios-web
// overlay (inlineGraphics, not remoteClient) re-renders live placements on
// re-transmit itself, so its reused frames must stay transmit-only (a=t) and must
// NOT self-place with a=T.
func TestInlineOverlayVideoStaysTransmitOnly(t *testing.T) {
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})

	shmName := makeShmFrame(t, 400, 300)
	cmd, raw := synthShmTransmitPlace(shmName, 400, 300)

	host := &recWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host, ForceEnable: true})

	const winID = "window-0000-0000-0000-000000000000"
	for range 2 {
		kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, false, func([]byte) {})
	}
	// Let any async frame land.
	time.Sleep(200 * time.Millisecond)
	if host.has("a=T,i=") {
		t.Fatal("inline overlay self-placed with a=T; it must stay transmit-only")
	}
}

// hostIDForGuest resolves the host image id a guest id maps to, for assertions.
func hostIDForGuest(kp *KittyPassthrough, winID string, guestID uint32) uint32 {
	return kp.imageIDMap[winID][guestID]
}

// TestZlibCompressRoundTrips proves the frame compression is valid zlib, so a
// real kitty (which inflates o=z with standard zlib) reconstructs the exact
// bitmap.
func TestZlibCompressRoundTrips(t *testing.T) {
	raw := make([]byte, 400*300*4)
	for i := range raw {
		raw[i] = byte(i * 7)
	}
	z := zlibCompress(raw)
	if z == nil {
		t.Fatal("zlibCompress returned nil")
	}
	if len(z) >= len(raw) {
		t.Fatalf("compressed size %d not smaller than raw %d", len(z), len(raw))
	}
	zr, err := zlib.NewReader(bytes.NewReader(z))
	if err != nil {
		t.Fatalf("zlib reader: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("inflate: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("round trip mismatch: inflated bytes differ from the original bitmap")
	}
}

// TestRemoteVideoFrameIsCompressed proves the remote video path advertises o=z,
// so the large RGBA does not go over the ssh link uncompressed.
func TestRemoteVideoFrameIsCompressed(t *testing.T) {
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})
	shmName := makeShmFrame(t, 400, 300)
	cmd, raw := synthShmTransmitPlace(shmName, 400, 300)
	host := &recWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host, RemoteClient: true})
	const winID = "window-0000-0000-0000-000000000000"
	kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, false, func([]byte) {})
	kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, false, func([]byte) {})
	if !waitUntil(func() bool { return host.has("o=z") }, 2*time.Second) {
		t.Fatal("remote video frame was not compressed (no o=z); the ssh link gets raw RGBA")
	}
}

// TestRemoteVideoClearedOnLeavingAltScreen proves the last frame does not linger:
// when the window leaves the screen the video was placed on (a browser quitting
// back to the shell), RefreshAllPlacements deletes the self-placed image.
func TestRemoteVideoClearedOnLeavingAltScreen(t *testing.T) {
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})
	shmName := makeShmFrame(t, 400, 300)
	cmd, raw := synthShmTransmitPlace(shmName, 400, 300)
	host := &recWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host, RemoteClient: true})

	const winID = "window-0000-0000-0000-000000000000"
	// Two frames on the alt screen (isAltScreen=true): the second self-places.
	kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, true, func([]byte) {})
	kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, true, func([]byte) {})
	if !waitUntil(func() bool { return host.has("a=T,i=") }, 2*time.Second) {
		t.Fatal("video was never placed")
	}

	// The window is now back on the main screen (browser quit).
	onMain := func() map[string]*WindowPositionInfo {
		return map[string]*WindowPositionInfo{
			winID: {WindowX: 0, WindowY: 0, Width: 183, Height: 42, Visible: true,
				ScreenWidth: 183, ScreenHeight: 42, IsAltScreen: false},
		}
	}
	kp.RefreshAllPlacements(onMain)
	if data := kp.FlushPending(); len(data) > 0 {
		kp.WriteToHost(data)
	}

	if !host.has("a=d,d=i,i=") {
		t.Fatal("leaving the alt screen did not delete the self-placed video image; it lingers over the shell")
	}
	kp.mu.Lock()
	leftover := len(kp.remoteVideo[winID])
	kp.mu.Unlock()
	if leftover != 0 {
		t.Fatalf("remoteVideo still tracks %d image(s) after cleanup", leftover)
	}
}

func (w *recWriter) count(sub string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Count(w.buf.Bytes(), []byte(sub))
}

func rewriteShm(t *testing.T, name string, w, h int, seed byte) {
	t.Helper()
	data := make([]byte, w*h*4)
	for i := range data {
		data[i] = seed + byte(i)
	}
	if err := os.WriteFile("/dev/shm/"+name, data, 0o600); err != nil {
		t.Fatalf("rewrite shm: %v", err)
	}
}

// sendUntil keeps handing frames to the passthrough until cond holds.
//
// A stream is paced against what the host's last frame cost, so an individual
// frame may be dropped by design, and these harnesses produce frames far
// faster than any guest (and slower hosts, under the race detector, drop
// more). A real stream keeps arriving until one lands, which is what this
// stands in for. It never retries an assertion that nothing was sent.
func sendUntil(t *testing.T, send func(), cond func() bool, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		send()
		if waitUntil(cond, 50*time.Millisecond) {
			return true
		}
	}
	return cond()
}

// TestRemoteVideoSkipsUnchangedFrames proves the idle-frame skip: an identical
// re-sent bitmap is not re-transmitted (the biggest idle-load/lag win), while a
// changed bitmap still goes out.
func TestRemoteVideoSkipsUnchangedFrames(t *testing.T) {
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})
	shmName := makeShmFrame(t, 400, 300)
	cmd, raw := synthShmTransmitPlace(shmName, 400, 300)
	host := &recWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host, RemoteClient: true})
	const winID = "window-0000-0000-0000-000000000000"
	send := func() {
		kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, false, func([]byte) {})
	}

	send() // frame 1: establishes the id
	// frame 2 onward: the first reused frame self-places and records the hash
	if !sendUntil(t, send, func() bool { return host.count("a=T,i=") >= 1 }, 2*time.Second) {
		t.Fatal("first self-placed frame never sent")
	}

	send() // frame 3: identical content -> must be skipped
	time.Sleep(200 * time.Millisecond)
	if n := host.count("a=T,i="); n != 1 {
		t.Fatalf("identical frame not skipped: a=T count = %d, want 1", n)
	}

	rewriteShm(t, shmName, 400, 300, 99) // change the pixels
	// frame 4 onward: changed content must reach the host
	if !sendUntil(t, send, func() bool { return host.count("a=T,i=") >= 2 }, 2*time.Second) {
		t.Fatal("changed frame was not sent (skip is too aggressive)")
	}
}

// TestRemoteVideoCountsInHasPlacements proves a self-placed video image makes
// HasPlacements report true, so the render loop keeps running its passes
// (RefreshAllPlacements, which clears the image when the browser quits, and the
// overlay hide) even though the image is not in `placements`.
func TestRemoteVideoCountsInHasPlacements(t *testing.T) {
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})
	shmName := makeShmFrame(t, 400, 300)
	cmd, raw := synthShmTransmitPlace(shmName, 400, 300)
	host := &recWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host, RemoteClient: true})
	const winID = "window-0000-0000-0000-000000000000"
	kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, true, func([]byte) {})
	kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, true, func([]byte) {})
	if !waitUntil(func() bool { return host.has("a=T,i=") }, 2*time.Second) {
		t.Fatal("video never placed")
	}
	if !kp.HasPlacements() {
		t.Fatal("HasPlacements is false with a live self-placed video image; the render loop would stop refreshing it")
	}
}

// TestOverlayHidesAndRestoresRemoteVideo proves an overlay deletes the video
// image and drops incoming frames, then the stream re-places it when the overlay
// closes.
func TestOverlayHidesAndRestoresRemoteVideo(t *testing.T) {
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})
	shmName := makeShmFrame(t, 400, 300)
	cmd, raw := synthShmTransmitPlace(shmName, 400, 300)
	host := &recWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host, RemoteClient: true})
	const winID = "window-0000-0000-0000-000000000000"
	send := func() {
		kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, true, func([]byte) {})
	}

	send() // establish
	if !sendUntil(t, send, func() bool { return host.count("a=T,i=") >= 1 }, 2*time.Second) {
		t.Fatal("video never placed")
	}

	// Overlay opens: the image is deleted and further frames are dropped.
	kp.SetOverlayActive(true)
	if !host.has("a=d,d=i,i=") {
		t.Fatal("opening an overlay did not delete the video image; the overlay draws behind it")
	}
	placed := host.count("a=T,i=")
	rewriteShm(t, shmName, 400, 300, 55)
	send() // must be dropped while the overlay is up
	time.Sleep(150 * time.Millisecond)
	if host.count("a=T,i=") != placed {
		t.Fatal("a frame drew over the overlay while it was open")
	}

	// Overlay closes: the next frame re-places the image.
	kp.SetOverlayActive(false)
	rewriteShm(t, shmName, 400, 300, 200)
	if !sendUntil(t, send, func() bool { return host.count("a=T,i=") > placed }, 2*time.Second) {
		t.Fatal("video did not re-place after the overlay closed")
	}
}

// TestOverlayCloseReshowsWithoutNewFrame proves the image comes back the instant
// the overlay closes, via a=p from the resident data, without the browser
// sending another frame. The bug was that a static page sends no new frame, so
// the image only returned when the user scrolled.
func TestOverlayCloseReshowsWithoutNewFrame(t *testing.T) {
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})
	shmName := makeShmFrame(t, 400, 300)
	cmd, raw := synthShmTransmitPlace(shmName, 400, 300)
	host := &recWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host, RemoteClient: true})
	const winID = "window-0000-0000-0000-000000000000"
	kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, true, func([]byte) {})
	kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, true, func([]byte) {})
	if !waitUntil(func() bool { return host.has("a=T,i=") }, 2*time.Second) {
		t.Fatal("video never placed")
	}

	kp.SetOverlayActive(true) // hide
	base := host.count("a=p,i=")
	kp.SetOverlayActive(false) // must re-show via a=p, no frame sent
	if host.count("a=p,i=") <= base {
		t.Fatal("overlay close did not re-show the image with a=p; it would stay gone until the browser sends a frame")
	}
}

// TestVideoFollowsWindowMove proves a self-placed image is re-placed (a=p) at the
// new position when its window moves, so it tracks a drag even without a new
// browser frame.
func TestVideoFollowsWindowMove(t *testing.T) {
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})
	shmName := makeShmFrame(t, 400, 300)
	cmd, raw := synthShmTransmitPlace(shmName, 400, 300)
	host := &recWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host, RemoteClient: true})
	const winID = "window-0000-0000-0000-000000000000"
	// Placed with the window at X=0.
	kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, true, func([]byte) {})
	kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, true, func([]byte) {})
	if !waitUntil(func() bool { return host.has("a=T,i=") }, 2*time.Second) {
		t.Fatal("video never placed")
	}

	// The window moves; RefreshAllPlacements must re-place the image with a=p.
	before := host.count("a=p,i=")
	moved := func() map[string]*WindowPositionInfo {
		return map[string]*WindowPositionInfo{
			winID: {WindowX: 20, WindowY: 5, ContentOffsetX: 1, ContentOffsetY: 1,
				Width: 183, Height: 42, Visible: true, ScreenWidth: 183, ScreenHeight: 42, IsAltScreen: true},
		}
	}
	kp.RefreshAllPlacements(moved)
	if data := kp.FlushPending(); len(data) > 0 {
		kp.WriteToHost(data)
	}
	if host.count("a=p,i=") <= before {
		t.Fatal("video did not re-place after the window moved; it stays behind on a drag")
	}
}

// TestRemoteVideoCropsRatherThanSqueezes is the reported stretch on the remote
// path. A frame bigger than its pane has its cell count capped to what fits,
// and kitty maps whatever source rectangle it is given onto that cell area - so
// a cap with no source rectangle to match is not a crop, it is a scale, and a
// scale on one axis and not the other is a distorted picture.
//
// The state carried the image's pixel dimensions next to its CAPPED cell count,
// so the fraction of the image to show worked out as all of it however little
// fitted. The uncapped footprint is what those pixels were divided into, and is
// what the fraction has to be measured against.
func TestRemoteVideoCropsRatherThanSqueezes(t *testing.T) {
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})

	// 40x15 cells of image at a 10x20 cell, in a pane with room for 30x12.
	const imgW, imgH = 400, 300
	shmName := makeShmFrame(t, imgW, imgH)
	cmd, raw := synthShmTransmitPlace(shmName, imgW, imgH)

	host := &recWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host, RemoteClient: true})

	const winID = "window-0000-0000-0000-000000000000"
	for range 2 { // the second frame reuses the id and self-places
		kp.ForwardCommand(cmd, raw, winID, 0, 0, 30, 12, 0, 0, 0, 0, 0, false, func([]byte) {})
	}
	if !waitUntil(func() bool { return host.has("a=T,i=") }, 2*time.Second) {
		t.Fatal("remote video never self-placed")
	}

	got := lastATParams(t, hostText(host))
	cols, rows := got["c"], got["r"]
	srcW, srcH := got["w"], got["h"]
	if srcW == 0 {
		srcW = imgW
	}
	if srcH == 0 {
		srcH = imgH
	}
	// What the host is given must be exactly what it is told to fill. Anything
	// else is a scale factor applied to the bitmap.
	if srcW != cols*10 || srcH != rows*20 {
		t.Fatalf("a %dx%d px source drawn in a %dx%d px cell box (c=%d r=%d): kitty "+
			"rescales the frame by %.3fx horizontally and %.3fx vertically, and a "+
			"frame scaled unevenly is the stretch that was reported\nplacement: %v",
			srcW, srcH, cols*10, rows*20, cols, rows,
			float64(cols*10)/float64(srcW), float64(rows*20)/float64(srcH), got)
	}
}

// hostText returns everything written to the host so far.
func hostText(w *recWriter) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// lastATParams parses the parameters of the last a=T the host was sent.
func lastATParams(t *testing.T, s string) map[string]int {
	t.Helper()
	i := strings.LastIndex(s, "a=T,")
	if i < 0 {
		t.Fatalf("no a=T in host output")
	}
	rest := s[i:]
	if j := strings.IndexAny(rest, ";\x1b"); j >= 0 {
		rest = rest[:j]
	}
	out := map[string]int{}
	for _, kv := range strings.Split(rest, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(v); err == nil {
			out[k] = n
		}
	}
	return out
}
