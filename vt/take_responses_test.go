package vt_test

import (
	"testing"
	"time"

	"tuios.dev/vt"
)

// TestTakeResponsesDrainsWithoutBlocking pins the contract a host that drains
// on its write goroutine depends on, on the backend this build selects. The
// ways it could fail: it blocks on an empty queue as Read does, it returns an
// empty non-nil slice, it hands back only part of the queue or leaves a copy
// for Read to return again, it reorders a reply made with WriteResponse, or
// it hands back replies after Close.
func TestTakeResponsesDrainsWithoutBlocking(t *testing.T) {
	term := vt.New(80, 24)
	t.Cleanup(func() { _ = term.Close() })

	took := func() []byte {
		t.Helper()
		got := make(chan []byte, 1)
		go func() { got <- term.TakeResponses() }()
		select {
		case b := <-got:
			return b
		case <-time.After(2 * time.Second):
			t.Fatal("TakeResponses blocked")
			return nil
		}
	}

	if got := took(); got != nil {
		t.Fatalf("empty queue: got %q, want nil", got)
	}

	_, _ = term.Write([]byte("\x1b[3;5H\x1b[6n"))
	term.WriteResponse([]byte("x"))
	_, _ = term.Write([]byte("\x1b[1;1H\x1b[6n"))
	if got, want := string(took()), "\x1b[3;5Rx\x1b[1;1R"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := took(); got != nil {
		t.Fatalf("second take: got %q, want nil", got)
	}

	_, _ = term.Write([]byte("\x1b[6n"))
	_ = term.Close()
	if got := took(); got != nil {
		t.Fatalf("after Close: got %q, want nil", got)
	}
}

// TestOnResponseCallsForEveryQueuedReply pins what a host that drains with
// TakeResponses when told depends on, on the backend this build selects. The
// ways it could fail: a reply queued outside Write (a cell size change under
// mode 2048, WriteResponse) calls nothing, so the host never drains it; the
// function is called with the queue's lock held, so a host that drains from it
// deadlocks; a nil function panics on the next reply.
func TestOnResponseCallsForEveryQueuedReply(t *testing.T) {
	term := vt.New(80, 24)
	t.Cleanup(func() { _ = term.Close() })

	calls := 0
	var drained []byte
	term.OnResponse(func() {
		calls++
		drained = append(drained, term.TakeResponses()...)
	})

	_, _ = term.Write([]byte("\x1b[?2048h"))
	drained = drained[:0]
	before := calls
	term.SetCellSize(11, 23)
	if calls == before || len(drained) == 0 {
		t.Fatalf("a cell size change under mode 2048 queued %q and called %d times", drained, calls-before)
	}

	before = calls
	term.WriteResponse([]byte("x"))
	if calls == before || string(drained[len(drained)-1:]) != "x" {
		t.Fatalf("WriteResponse called %d times, drained %q", calls-before, drained)
	}

	term.OnResponse(nil)
	term.WriteResponse([]byte("y"))
	if got := string(term.TakeResponses()); got != "y" {
		t.Fatalf("after OnResponse(nil): got %q, want %q", got, "y")
	}
}
