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
