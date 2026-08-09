package app

import (
	"bytes"
	"compress/zlib"
	"io"
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
		kp.ForwardCommand(cmd, raw, winID, 0, 0, 183, 42, 1, 1, 0, 0, 0, false, func([]byte) {})
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
		kp.ForwardCommand(cmd, raw, winID, 0, 0, 183, 42, 1, 1, 0, 0, 0, false, func([]byte) {})
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
	kp.ForwardCommand(cmd, raw, winID, 0, 0, 183, 42, 1, 1, 0, 0, 0, false, func([]byte) {})
	kp.ForwardCommand(cmd, raw, winID, 0, 0, 183, 42, 1, 1, 0, 0, 0, false, func([]byte) {})
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
	kp.ForwardCommand(cmd, raw, winID, 0, 0, 183, 42, 1, 1, 0, 0, 0, true, func([]byte) {})
	kp.ForwardCommand(cmd, raw, winID, 0, 0, 183, 42, 1, 1, 0, 0, 0, true, func([]byte) {})
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
