package vt

import (
	"testing"
	"time"
)

func enablePixelMouse(e *Emulator) {
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

	gotMotion := e.EncodeMouseEvent(MouseMotion{X: 3, Y: 4, Button: MouseNone})
	if gotMotion != "\x1b[<35;36;91M" {
		t.Fatalf("pixel-mode motion = %q, want %q", gotMotion, "\x1b[<35;36;91M")
	}

	gotWheel := e.EncodeMouseEvent(MouseWheel{X: 3, Y: 4, Button: MouseWheelUp})
	if gotWheel != "\x1b[<64;36;91M" {
		t.Fatalf("pixel-mode wheel = %q, want %q", gotWheel, "\x1b[<64;36;91M")
	}
}

func TestEncodeMouseCellModeUnchanged(t *testing.T) {
	e := NewEmulator(80, 24)
	defer func() { _ = e.Close() }()
	enableCellMouse(e)

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
