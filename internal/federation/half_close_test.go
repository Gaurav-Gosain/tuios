package federation

import (
	"io"
	"net"
	"testing"
	"time"
)

// TestHalfCloseKeepsTheOtherDirection: CloseWrite on one end of a stream ends
// the peer's reads, after every byte written before it, and the peer can still
// write back. A whole Close then ends the stream for both.
func TestHalfCloseKeepsTheOtherDirection(t *testing.T) {
	a, b := net.Pipe()
	accepted := make(chan *Stream, 1)
	hub := newMux(a, nil, dialerFirstID)
	far := newMux(b, func(s *Stream) { accepted <- s }, answererFirstID)
	go func() { _ = hub.run() }()
	go func() { _ = far.run() }()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })

	s, err := hub.open(0, StreamOpen{HalfClose: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	// The far side's answer comes right after the open.
	for i := 0; !s.halfOK.Load(); i++ {
		if i > 500 {
			t.Fatal("the far side never answered the HalfClose open")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := s.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("more")); err == nil {
		t.Fatal("a write after CloseWrite was accepted")
	}

	var peer *Stream
	select {
	case peer = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the far side never accepted the stream")
	}
	if !peer.open.HalfClose {
		t.Fatal("the open frame lost HalfClose")
	}
	got, err := io.ReadAll(peer)
	if err != nil || string(got) != "request" {
		t.Fatalf("the far side read %q, %v; want \"request\" and a clean end", got, err)
	}
	if !peer.PeerWriteClosed() {
		t.Fatal("the far side does not see the half-close")
	}
	if _, err := peer.Write([]byte("answer")); err != nil {
		t.Fatalf("the far side cannot write after the half-close: %v", err)
	}
	_ = peer.Close()
	back, err := io.ReadAll(s)
	if err != nil || string(back) != "answer" {
		t.Fatalf("the hub read %q, %v; want \"answer\" and a clean end", back, err)
	}
	if s.PeerWriteClosed() {
		t.Fatal("a whole close reads as a half-close")
	}

	// A stream opened without HalfClose gets no answer, so CloseWrite must
	// refuse rather than send a frame an older peer cannot read.
	plain, err := hub.open(0, StreamOpen{})
	if err != nil {
		t.Fatal(err)
	}
	<-accepted
	if err := plain.CloseWrite(); err != ErrNoHalfClose {
		t.Fatalf("CloseWrite on a stream without HalfClose returned %v, want ErrNoHalfClose", err)
	}
}
