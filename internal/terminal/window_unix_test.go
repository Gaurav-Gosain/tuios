//go:build unix || linux || darwin || freebsd || openbsd || netbsd

package terminal

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"golang.org/x/sys/unix"
)

// TestForegroundPgrpIsTheIdleShell checks that the foreground process group
// read from a fresh pane is the shell's own group, which is what
// HasForegroundProcess compares against.
func TestForegroundPgrpIsTheIdleShell(t *testing.T) {
	exitChan := make(chan string, 1)
	window, err := NewWindow("test-id-fgpgrp01", "Test", 0, 0, 80, 24, 0, exitChan, nil, config.DefaultScrollbackLines)
	if err != nil {
		t.Skipf("Failed to create window with PTY: %v", err)
	}
	defer window.Close()
	if window.Pty == nil || window.ShellPgid <= 0 {
		t.Skip("No PTY or shell process group available")
	}

	got, ok := foregroundPgrp(window.Pty.Fd())
	if !ok {
		t.Fatal("foregroundPgrp gave no answer for a live pane")
	}
	if got != window.ShellPgid {
		t.Errorf("foregroundPgrp = %d, want the shell's pgid %d", got, window.ShellPgid)
	}
	if window.HasForegroundProcess() {
		t.Error("HasForegroundProcess is true for a pane running only its shell")
	}
}

// TestForegroundCommandNamesTheIdleShell checks that a fresh pane names its
// shell. The keybind manager's observed tier and the rail row both read this,
// and an empty answer makes them say nothing about the pane. It used to come
// only from /proc, so on macOS, which has none, it was always empty.
func TestForegroundCommandNamesTheIdleShell(t *testing.T) {
	exitChan := make(chan string, 1)
	window, err := NewWindow("test-id-fgcomm01", "Test", 0, 0, 80, 24, 0, exitChan, nil, config.DefaultScrollbackLines)
	if err != nil {
		t.Skipf("Failed to create window with PTY: %v", err)
	}
	defer window.Close()
	if window.Pty == nil || window.Cmd == nil || window.Cmd.Process == nil {
		t.Skip("No PTY or shell process available")
	}
	if _, ok := foregroundPgrp(window.Pty.Fd()); !ok {
		t.Skip("the kernel gave no foreground process group for this pane")
	}

	got := window.ForegroundCommand()
	if got == "" {
		t.Fatal("ForegroundCommand is empty for a pane running its shell")
	}
	// The kernel truncates the name, so the shell's file name is compared by
	// prefix rather than for equality.
	shell := filepath.Base(window.Cmd.Path)
	if !strings.HasPrefix(shell, strings.TrimPrefix(got, "-")) {
		t.Errorf("ForegroundCommand = %q, want the shell %q", got, shell)
	}
}

func TestSetCellPixelDimensions(t *testing.T) {
	exitChan := make(chan string, 1)
	window, err := NewWindow("test-id-87654321", "Test", 0, 0, 80, 24, 0, exitChan, nil, config.DefaultScrollbackLines)
	if err != nil {
		t.Skipf("Failed to create window with PTY: %v", err)
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
