//go:build unix

package vt

import (
	"encoding/base64"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// These guard the boundary between a pane's guest and this machine's files,
// and the emulator's lock, which every byte of the pane waits on. The ways
// the check of a file upload could fail them:
//   - an open that blocks: a FIFO opened for reading waits for a writer, and
//     a guest can swap one in after the path was checked;
//   - a look that blocks: a network mount that stops answering holds even a
//     non-blocking open, and the emulator parses with its lock held.

// TestKittyOpenRegularDoesNotWaitOnAFIFO opens a FIFO the way the check opens
// a file. Without O_NONBLOCK the open waits for a writer that never comes.
func TestKittyOpenRegularDoesNotWaitOnAFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "frame.png")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- kittyOpenRegular(fifo) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO passed as a regular file")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("opening a FIFO waited for a writer")
	}
}

// TestKittyFileUploadIsCheckedOffTheLock makes the look at a file hang, as a
// dead mount does. Handle must return at once, and the guest must still get
// an answer, EBADF, when the look runs out of time.
func TestKittyFileUploadIsCheckedOffTheLock(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	prevCheck, prevTimeout := kittyFileCheck, kittyFileCheckTimeout
	kittyFileCheck = func(KittyGraphicsMedium, string) string { <-release; return "" }
	kittyFileCheckTimeout = 200 * time.Millisecond
	t.Cleanup(func() { kittyFileCheck, kittyFileCheckTimeout = prevCheck, prevTimeout })

	path := base64.StdEncoding.EncodeToString([]byte("/mnt/dead/frame.png"))
	cmd, err := ParseKittyCommand([]byte("a=t,t=f,f=100,i=5;" + path))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var replies []string
	write := func(b []byte) { mu.Lock(); replies = append(replies, string(b)); mu.Unlock() }

	var g KittyGuestImages
	returned := make(chan bool, 1)
	go func() { returned <- g.Handle(cmd, write) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("Handle waited for the file check, under the emulator's lock")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := strings.Join(replies, "")
		mu.Unlock()
		if strings.Contains(got, "i=5;EBADF:") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no EBADF after the check ran out of time: %q", replies)
}
