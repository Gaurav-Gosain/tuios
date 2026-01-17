//go:build unix || linux || darwin || freebsd || openbsd || netbsd

package terminal

import (
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestSetPtyPixelSize(t *testing.T) {
	exitChan := make(chan string, 1)
	window := NewWindow("test-id-12345678", "Test", 0, 0, 80, 24, 0, exitChan)
	if window == nil {
		t.Skip("Failed to create window with PTY")
	}
	defer window.Close()

	if window.Pty == nil {
		t.Skip("No PTY available")
	}

	cellWidth := 10
	cellHeight := 20
	termWidth := 78
	termHeight := 22
	xpixel := termWidth * cellWidth
	ypixel := termHeight * cellHeight

	err := window.SetPtyPixelSize(termWidth, termHeight, xpixel, ypixel)
	if err != nil {
		t.Fatalf("SetPtyPixelSize failed: %v", err)
	}

	var ws unix.Winsize
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		window.Pty.Fd(),
		uintptr(unix.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 {
		t.Fatalf("TIOCGWINSZ failed: %v", errno)
	}

	t.Logf("PTY size: cols=%d, rows=%d, xpixel=%d, ypixel=%d", ws.Col, ws.Row, ws.Xpixel, ws.Ypixel)

	if ws.Xpixel != uint16(xpixel) {
		t.Errorf("Expected Xpixel=%d, got %d", xpixel, ws.Xpixel)
	}
	if ws.Ypixel != uint16(ypixel) {
		t.Errorf("Expected Ypixel=%d, got %d", ypixel, ws.Ypixel)
	}
}

func TestSetCellPixelDimensions(t *testing.T) {
	exitChan := make(chan string, 1)
	window := NewWindow("test-id-87654321", "Test", 0, 0, 80, 24, 0, exitChan)
	if window == nil {
		t.Skip("Failed to create window with PTY")
	}
	defer window.Close()

	if window.Pty == nil {
		t.Skip("No PTY available")
	}

	window.SetCellPixelDimensions(10, 20)

	if window.CellPixelWidth != 10 {
		t.Errorf("Expected CellPixelWidth=10, got %d", window.CellPixelWidth)
	}
	if window.CellPixelHeight != 20 {
		t.Errorf("Expected CellPixelHeight=20, got %d", window.CellPixelHeight)
	}

	var ws unix.Winsize
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		window.Pty.Fd(),
		uintptr(unix.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 {
		t.Fatalf("TIOCGWINSZ failed: %v", errno)
	}

	t.Logf("PTY size after SetCellPixelDimensions: cols=%d, rows=%d, xpixel=%d, ypixel=%d",
		ws.Col, ws.Row, ws.Xpixel, ws.Ypixel)

	termWidth := 78
	termHeight := 22
	expectedXpixel := termWidth * 10
	expectedYpixel := termHeight * 20

	if ws.Xpixel != uint16(expectedXpixel) {
		t.Errorf("Expected Xpixel=%d, got %d", expectedXpixel, ws.Xpixel)
	}
	if ws.Ypixel != uint16(expectedYpixel) {
		t.Errorf("Expected Ypixel=%d, got %d", expectedYpixel, ws.Ypixel)
	}
}
