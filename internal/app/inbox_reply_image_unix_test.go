//go:build unix

package app

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestAPastedPathToAPipeIsRefusedAtOnce: a pasted path is read in a
// background task. A named pipe called shot.png must be refused, not opened
// and waited on, or the task waits for a writer that never comes.
func TestAPastedPathToAPipeIsRefusedAtOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shot.png")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := readImageFile(path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("readImageFile read a named pipe as an image")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readImageFile is still waiting on a named pipe after 5s")
	}
}
