package app

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// recordingWriter keeps what the host was sent. It takes the same lock as
// Write, so it is safe to read while the async frame writer is still active.
type recordingWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *recordingWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buf.Bytes()...)
}

var (
	kittyTransmitRE     = regexp.MustCompile(`\x1b_Ga=t,i=(\d+),`)
	kittyVirtualPlaceRE = regexp.MustCompile(`\x1b_Ga=p,U=1,i=(\d+),c=3,r=2,q=2\x1b\\`)
	kittyPlacedFrameRE  = regexp.MustCompile(`\x1b\[\d+;\d+H\x1b_Ga=[pT]`)
)

// Over ssh, file-medium frames are re-encoded as direct data. An a=T with U=1
// there is still a virtual placement: the host is never told where to draw
// it, on the first frame or on the later ones a reused id would otherwise
// send as a self-placed video stream.
func TestKittyVirtualPlacementOverSSHIsNeverSelfPlaced(t *testing.T) {
	kp, host := remoteKittyRecording(t)
	// Under /tmp by name: the suite puts HOME under TMPDIR, which rules
	// TMPDIR out as a temporary directory.
	dir, err := os.MkdirTemp("/tmp", "tuios-kitty-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "tty-graphics-protocol-frame.rgba")

	for frame := range 3 {
		if err := os.WriteFile(path, bytes.Repeat([]byte{byte(frame + 1)}, 16), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := kittyFileCmd(vt.KittyMediumTempFile, path)
		cmd.Virtual = true
		cmd.Columns, cmd.Rows = 3, 2
		result := kp.ForwardCommand(cmd, nil, kittyMediumWin, 0, 0, 80, 24, 1, 1, 0, 0, 0, false, func([]byte) {})
		if result != nil {
			t.Fatalf("frame %d: a virtual placement reserved %d rows in the pane, want none", frame, result.Rows)
		}
		if data := kp.FlushPending(); len(data) > 0 {
			kp.WriteToHost(data)
		}
	}
	// The video path writes from its own goroutine.
	time.Sleep(100 * time.Millisecond)

	sent := host.Bytes()
	transmits := kittyTransmitRE.FindAllSubmatch(sent, -1)
	places := kittyVirtualPlaceRE.FindAllSubmatch(sent, -1)
	if len(transmits) != 3 || len(places) != 3 {
		t.Fatalf("3 frames sent %d transmissions and %d virtual placements, want 3 of each:\n%q", len(transmits), len(places), sent)
	}
	for i := range 3 {
		if !bytes.Equal(transmits[i][1], places[i][1]) {
			t.Errorf("frame %d transmitted as image %s but placed image %s", i, transmits[i][1], places[i][1])
		}
	}
	if m := kittyPlacedFrameRE.Find(sent); m != nil {
		t.Errorf("a frame was drawn at a cursor position: %q", m)
	}
	if bytes.Contains(sent, []byte("a=T,")) {
		t.Errorf("a frame went out as a self-placed a=T:\n%q", sent)
	}
}

// remoteKittyRecording is remoteKitty with a host that keeps what it is sent.
func remoteKittyRecording(t *testing.T) (*KittyPassthrough, *recordingWriter) {
	t.Helper()
	withClientCaps(t, &HostCapabilities{KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20})
	host := &recordingWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host, RemoteClient: true})
	if !kp.IsEnabled() {
		t.Fatal("passthrough not enabled")
	}
	return kp, host
}
