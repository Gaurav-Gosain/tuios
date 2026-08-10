package app

import (
	"errors"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// panicWriter panics on Write, standing in for any graphics-path failure that
// could bubble up while the render goroutine flushes kitty output to the host
// (ssh session). A panic on this path escapes View() into bubbletea's top-level
// recover, which returns from Program.Run and tears the ssh session down. That
// is the shape of the captured crash, so the render path must contain it.
type panicWriter struct{}

func (panicWriter) Write(p []byte) (int, error) {
	panic(errors.New("host write blew up"))
}

// TestGetKittyGraphicsCmdSurvivesHostWritePanic asserts the render-path graphics
// flush never propagates a panic. On the unfixed build the panic escapes and, in
// production, kills the tea program (and thus the ssh session).
func TestGetKittyGraphicsCmdSurvivesHostWritePanic(t *testing.T) {
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})

	m := NewOS(OSOptions{
		UserConfig:                config.DefaultConfig(),
		Width:                     183,
		Height:                    42,
		IsDaemonSession:           true,
		EnableGraphicsPassthrough: true,
		GraphicsOutput:            panicWriter{},
		GraphicsRemoteClient:      true,
	})

	win := newTestWindow(t, "panicwin-0001", 183, 42)
	win.DaemonMode = true
	win.Workspace = m.CurrentWorkspace
	m.setupKittyPassthrough(win)
	m.Windows = append(m.Windows, win)
	m.FocusedWindow = 0

	// A direct (inline) transmit produces pending output that the render flush
	// writes to the host, which panics.
	raw := []byte("\x1b_Ga=T,i=1,f=32,s=2,v=2;AAAAAAAAAAAAAAAA\x1b\\")
	win.LockIO()
	_, _ = win.Terminal.Write(raw)
	win.UnlockIO()

	// flushGraphicsForView is exactly what View() runs after setting content.
	// Before the fix its body (GetKittyGraphicsCmd -> WriteToHost) let this
	// panic escape into bubbletea's top-level recover, ending Program.Run and,
	// over SSH, tearing the session down. It must now swallow the panic and
	// drop the frame.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("render-path graphics flush propagated a panic (would kill the ssh session): %v", r)
		}
	}()
	m.flushGraphicsForView()
}

// TestAsyncFrameWriterSurvivesPanic asserts a panic while writing a video frame
// on the async writer goroutine is contained. That goroutine is spawned by the
// passthrough, not by bubbletea, so an unrecovered panic there crashes the
// whole SSH server process (every session), not just one pane.
func TestAsyncFrameWriterSurvivesPanic(t *testing.T) {
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{
		Output:       panicWriter{},
		RemoteClient: true,
	})
	// writeFrameSafely is the unit the async goroutine runs per frame; it must
	// not propagate a panic from the host write.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("async frame write propagated a panic (would crash the server process): %v", r)
		}
	}()
	kp.writeFrameSafely(asyncFrame{data: []byte("frame-bytes")})
}
