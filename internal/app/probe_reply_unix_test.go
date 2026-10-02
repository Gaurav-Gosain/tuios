//go:build linux || darwin || freebsd || openbsd

package app

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// waitDrained waits until the reader has taken every byte written to the pipe,
// so the next write is a reply that arrives after a read, not one that a single
// read takes whole.
func waitDrained(t *testing.T, r *os.File) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n, err := unix.IoctlGetInt(int(r.Fd()), fionread)
		if err != nil {
			t.Errorf("FIONREAD: %v", err)
			return
		}
		if n == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Error("the probe never read the head of the reply")
}

// A reply that arrives after the one the probe waits for can be cut by a
// read. The probe must read it to its end, or its tail reaches the program's
// input as keys. A nested tuios typed "ost terminal does not support
// animation" into the scratch shell that way.
func TestProbeReadsALateReplyToItsEnd(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()

	head := "\x1b[?62;4c\x1b_Gi=4;ENOTSUPPORTED:h"
	tail := "ost terminal does not support animation\x1b\\"
	if _, err := w.WriteString(head); err != nil {
		t.Fatal(err)
	}
	go func() {
		waitDrained(t, r)
		_, _ = w.WriteString(tail)
	}()

	got := readTTYResponse(r, 5*time.Second, da1Response.MatchString)
	if got != head+tail {
		t.Fatalf("the probe read %q, want the whole late reply %q", got, head+tail)
	}
}
