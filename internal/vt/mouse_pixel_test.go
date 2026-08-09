package vt

import (
	"testing"
	"time"
)

// A kitty-graphics web browser (terminal-browser, awrit) enables SGR-pixel mouse
// (DEC mode 1016) and, once it sees it enabled, reads every mouse report as
// pixels. tuios must then report the pointer in host pixels, not cells; reporting
// cells while 1016 is on places every event a cell-count of pixels from the
// origin, which is why hover and clicks land in the top-left corner.
//
// These tests pin the encoding for both mouse paths: EncodeMouseEvent (daemon,
// over the PTY) and SendMouse (native, over the response pipe). Cell size is
// 10x20, so a click at cell (3,4) is pixel (3*10+5, 4*20+10) = (35, 90) at the
// cell centre, and MouseSgr reports it 1-based as 36;91.

func enablePixelMouse(e *Emulator) {
	// 1003 (any-motion) + 1006 (SGR) + 1016 (SGR-pixel): exactly what
	// terminal-browser turns on at startup.
	_, _ = e.Write([]byte("\x1b[?1003h\x1b[?1006h\x1b[?1016h"))
	e.SetCellSize(10, 20)
}

func enableCellMouse(e *Emulator) {
	_, _ = e.Write([]byte("\x1b[?1003h\x1b[?1006h"))
	e.SetCellSize(10, 20)
}

func TestEncodeMousePixelMode(t *testing.T) {
	e := NewEmulator(80, 24)
	defer func() { _ = e.Close() }()
	enablePixelMouse(e)

	got := e.EncodeMouseEvent(MouseClick{X: 3, Y: 4, Button: MouseLeft})
	want := "\x1b[<0;36;91M"
	if got != want {
		t.Fatalf("pixel-mode click = %q, want %q", got, want)
	}

	// Motion (any-motion mode is on) must also report pixels so hover works.
	gotMotion := e.EncodeMouseEvent(MouseMotion{X: 3, Y: 4, Button: MouseNone})
	if gotMotion != "\x1b[<35;36;91M" {
		t.Fatalf("pixel-mode motion = %q, want %q", gotMotion, "\x1b[<35;36;91M")
	}

	// Wheel-up over the page must report pixels too.
	gotWheel := e.EncodeMouseEvent(MouseWheel{X: 3, Y: 4, Button: MouseWheelUp})
	if gotWheel != "\x1b[<64;36;91M" {
		t.Fatalf("pixel-mode wheel = %q, want %q", gotWheel, "\x1b[<64;36;91M")
	}
}

func TestEncodeMouseCellModeUnchanged(t *testing.T) {
	e := NewEmulator(80, 24)
	defer func() { _ = e.Close() }()
	enableCellMouse(e)

	// Without 1016, the existing SGR-cell (1006) encoding is used: cell (3,4)
	// reported 1-based as 4;5, no pixel scaling.
	got := e.EncodeMouseEvent(MouseClick{X: 3, Y: 4, Button: MouseLeft})
	if got != "\x1b[<0;4;5M" {
		t.Fatalf("cell-mode click = %q, want %q", got, "\x1b[<0;4;5M")
	}
}

// TestSendMousePixelMode covers the native path, which writes to the emulator's
// response pipe rather than returning a string.
func TestSendMousePixelMode(t *testing.T) {
	e := NewEmulator(80, 24)
	defer func() { _ = e.Close() }()
	enablePixelMouse(e)

	out := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		n, _ := e.Read(buf)
		out <- string(buf[:n])
	}()

	e.SendMouse(MouseClick{X: 3, Y: 4, Button: MouseLeft})

	select {
	case got := <-out:
		if got != "\x1b[<0;36;91M" {
			t.Fatalf("native pixel-mode click = %q, want %q", got, "\x1b[<0;36;91M")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for native mouse report")
	}
}
