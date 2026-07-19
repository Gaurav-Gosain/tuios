package terminal

import (
	"os/exec"
	"sync"
	"testing"
	"testing/synctest"
)

// blockingPty is a fake xpty.Pty whose Write parks until the test releases it.
// It models the real failure: a guest that has stopped reading its stdin, so
// the kernel PTY input buffer fills and Pty.Write blocks indefinitely. A paste
// into a suspended process does exactly this.
//
// The park is a receive on a channel created inside the synctest bubble, which
// is what makes the test deterministic: synctest.Wait returns only once every
// other goroutine in the bubble is durably blocked, so when it returns we know
// SendInput has reached the write and is sitting in it.
type blockingPty struct {
	entered  chan struct{}
	release  chan struct{}
	writeLen int
}

func (p *blockingPty) Write(b []byte) (int, error) {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	<-p.release
	p.writeLen = len(b)
	return len(b), nil
}

func (p *blockingPty) Read([]byte) (int, error) { return 0, nil }
func (p *blockingPty) Close() error             { return nil }
func (p *blockingPty) Fd() uintptr              { return 0 }
func (p *blockingPty) Resize(_, _ int) error    { return nil }
func (p *blockingPty) Size() (int, int, error)  { return 80, 24, nil }
func (p *blockingPty) Name() string             { return "fake-pty" }
func (p *blockingPty) Start(_ *exec.Cmd) error  { return nil }

// TestSendInputDoesNotHoldIOLockAcrossPtyWrite is the regression test for the
// second window-lock hazard: SendInput used to hold ioMu.RLock across the
// blocking Pty.Write.
//
// That is fatal without any recursion. sync.RWMutex does not let a reader in
// once a writer is queued, and the PTY reader queues LockIO on every chunk of
// shell output. So:
//
//	UI goroutine    RLockIO, then block forever inside Pty.Write
//	PTY goroutine   LockIO            (queues behind the reader)
//	render path     RLockIO           (parks behind the queued writer)
//
// The renderer can no longer draw, and the UI is frozen at zero CPU for as
// long as the guest refuses to drain its stdin.
//
// The assertion is a non-blocking TryLock rather than a real Lock, so the test
// FAILS on the unfixed code instead of hanging: a mutex wait is not a
// "durably blocked" state for synctest, so a real Lock here would deadlock the
// bubble rather than report anything useful.
//
// Verified to fail on the unfixed code: restoring the original
//
//	w.ioMu.RLock(); defer w.ioMu.RUnlock(); ... w.Pty.Write(input)
//
// makes this test report "SendInput is holding ioMu across the blocking PTY
// write".
func TestSendInputDoesNotHoldIOLockAcrossPtyWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pty := &blockingPty{
			entered: make(chan struct{}, 1),
			release: make(chan struct{}),
		}

		w := &Window{ID: "lock-discipline-0001"}
		w.Pty = pty

		sendDone := make(chan error, 1)
		go func() {
			sendDone <- w.SendInput([]byte("ls -la\r"))
		}()

		// Deterministic barrier: returns once the sender goroutine is parked
		// inside blockingPty.Write, with no timing assumptions.
		synctest.Wait()

		select {
		case <-pty.entered:
		default:
			t.Fatal("SendInput never reached Pty.Write")
		}

		// The whole point of the fix. If SendInput still held the read lock
		// across the write, no exclusive acquisition could succeed here, and
		// on the real UI that is the PTY reader and the renderer wedging.
		if !w.ioMu.TryLock() {
			close(pty.release)
			t.Fatal("SendInput is holding ioMu across the blocking PTY write: " +
				"a queued writer starves every later reader, so the render path " +
				"can never take its read side and the UI freezes")
		}
		w.ioMu.Unlock()

		// A reader must get in too: that is literally the render path.
		if !w.ioMu.TryRLock() {
			close(pty.release)
			t.Fatal("ioMu read side unavailable while SendInput blocks in Pty.Write")
		}
		w.ioMu.RUnlock()

		close(pty.release)
		if err := <-sendDone; err != nil {
			t.Fatalf("SendInput returned %v", err)
		}
		if pty.writeLen != len("ls -la\r") {
			t.Fatalf("PTY received %d bytes, want %d", pty.writeLen, len("ls -la\r"))
		}
	})
}

// nopPty is a non-blocking fake PTY for the teardown race test.
type nopPty struct{ blockingPty }

func (p *nopPty) Write(b []byte) (int, error) { return len(b), nil }

// TestSendInputSnapshotsPtyUnderTeardown guards the other half of the SendInput
// fix. Moving the write out of the lock is only safe because the handle is
// snapshotted under it: Close() assigns w.Pty = nil while holding the exclusive
// lock, so reading the field without the lock is an unsynchronized read of an
// interface value racing that write - a torn read, which is undefined
// behaviour, not merely a stale value.
//
// This must be run with -race; it is the race detector, not an assertion, that
// catches the fault.
//
// Verified to fail on broken code: dropping the RLock around the field read in
// SendInput (calling w.Pty.Write directly) makes this test report
// "DATA RACE ... Write at ... Previous read at ...  SendInput".
func TestSendInputSnapshotsPtyUnderTeardown(t *testing.T) {
	w := &Window{ID: "lock-discipline-0002"}
	w.Pty = &nopPty{}

	// One sender, matching reality: SendInput's trailing Dirty/ContentDirty
	// writes are UI-goroutine-owned window model fields, so a multi-sender
	// test would flag those instead of the field read under test.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 4000 {
			_ = w.SendInput([]byte("x"))
		}
	}()

	// Teardown lands in the middle of the flood, exactly as Close() does.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			w.ioMu.Lock()
			w.Pty = nil
			w.ioMu.Unlock()
			w.ioMu.Lock()
			w.Pty = &nopPty{}
			w.ioMu.Unlock()
		}
	}()

	wg.Wait()
}

// TestSendInputReportsMissingPty pins the error path so a future refactor
// cannot turn a nil PTY into a silent success.
func TestSendInputReportsMissingPty(t *testing.T) {
	w := &Window{ID: "lock-discipline-0003"}
	if err := w.SendInput([]byte("x")); err == nil {
		t.Fatal("SendInput with no PTY should return an error")
	}
	// Zero-length input is a no-op regardless.
	if err := w.SendInput(nil); err != nil {
		t.Fatalf("SendInput with empty input returned %v", err)
	}
}
