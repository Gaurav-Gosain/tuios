//go:build linux || darwin

package trust

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestAFIFOIsIneligibleAndDoesNotBlock: a process in a pane can put a FIFO
// at .tuios.tape, or at the trust store, and a plain open of one waits for a
// writer forever. Check runs on the client's UI goroutine, so that froze the
// client. The open must not wait, and the hygiene rules turn the FIFO away.
//
// Negative control: with os.Open in place of openNoBlock in Check, the tape
// case does not return.
func TestAFIFOIsIneligibleAndDoesNotBlock(t *testing.T) {
	within := func(name string, f func()) {
		t.Helper()
		done := make(chan struct{})
		go func() { f(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not return within 5s", name)
		}
	}
	dir := t.TempDir()
	fifo := filepath.Join(dir, TapeFileName)
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	link := filepath.Join(t.TempDir(), TapeFileName)
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	s := newStore(t)
	for _, path := range []string{fifo, link} {
		var res Result
		within("Check on "+path, func() { res, _ = s.Check(path) })
		if res.Status != StatusIneligible || res.Reason != "not a regular file" {
			t.Errorf("Check on %s = %v %q, want ineligible, not a regular file", path, res.Status, res.Reason)
		}
	}

	store := filepath.Join(t.TempDir(), "tape-trust.toml")
	if err := syscall.Mkfifo(store, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	var loaded *Store
	within("LoadFromPath on a FIFO", func() { loaded, _ = LoadFromPath(store) })
	if loaded == nil || loaded.Warning == "" {
		t.Errorf("a FIFO trust store loaded with no warning: %+v", loaded)
	}
}
