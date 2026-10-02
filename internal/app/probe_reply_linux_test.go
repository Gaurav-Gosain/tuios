package app

import (
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// fionread is the ioctl that says how many bytes wait to be read. Linux names
// it TIOCINQ.
const fionread = unix.TIOCINQ

// A signal that lands while the probe waits for the rest of a reply interrupts
// the wait with EINTR. The probe has to wait again for the time it has left.
// It used to stop there, and the tail of the reply reached the program's input
// as keys. The Go runtime sends SIGURG to preempt goroutines, so this is not
// a rare signal: it is how the late-reply test above flaked in CI.
//
// The signals go to the thread that reads, so each one interrupts its wait.
func TestProbeReadSurvivesASignal(t *testing.T) {
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

	tid := make(chan int, 1)
	var stop atomic.Bool
	go func() {
		reader := <-tid
		waitDrained(t, r)
		// The probe is in its wait now. Interrupt it for a while, then
		// send the tail.
		for range 50 {
			_ = unix.Tgkill(os.Getpid(), reader, unix.SIGURG)
			time.Sleep(time.Millisecond)
		}
		_, _ = w.WriteString(tail)
		for !stop.Load() {
			_ = unix.Tgkill(os.Getpid(), reader, unix.SIGURG)
			time.Sleep(time.Millisecond)
		}
	}()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	tid <- unix.Gettid()
	got := readTTYResponse(r, 5*time.Second, da1Response.MatchString)
	stop.Store(true)
	if got != head+tail {
		t.Fatalf("the probe read %q, want the whole late reply %q", got, head+tail)
	}
}
