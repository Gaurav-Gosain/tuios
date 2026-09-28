//go:build unix

package main

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestReadForTransferRefusesAFIFO: a FIFO is not a regular file, and opening
// one with no writer must not block.
func TestReadForTransferRefusesAFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := readForTransfer(path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("ASSERTION: a FIFO was not refused as not a regular file: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ASSERTION: readForTransfer blocked on a FIFO with no writer")
	}
}
