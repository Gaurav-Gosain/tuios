package app

import (
	"errors"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

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
		UserConfig:           config.DefaultConfig(),
		Width:                183,
		Height:               42,
		IsDaemonSession:      true,
		GraphicsOutput:       panicWriter{},
		GraphicsRemoteClient: true,
	})

	win := newTestWindow(t, "panicwin-0001", 183, 42)
	win.DaemonMode = true
	win.Workspace = m.CurrentWorkspace
	m.setupKittyPassthrough(win)
	m.Windows = append(m.Windows, win)
	m.FocusedWindow = 0

	raw := []byte("\x1b_Ga=T,i=1,f=32,s=2,v=2;AAAAAAAAAAAAAAAA\x1b\\")
	win.LockIO()
	_, _ = win.Terminal.Write(raw)
	win.UnlockIO()

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
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("async frame write propagated a panic (would crash the server process): %v", r)
		}
	}()
	kp.writeFrameSafely(asyncFrame{data: []byte("frame-bytes")})
}
