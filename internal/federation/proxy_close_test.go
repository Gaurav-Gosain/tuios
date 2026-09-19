package federation

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// A daemon on the far side that stops talking has to be reported here, and
// promptly, without anything being written first.
//
// The fault this pins: serveStream closed the stream from a defer that ran
// after waiting for the other direction, and the other direction was a copy
// blocked reading this same stream for bytes from this side. So a far daemon
// that closed its connection was never reported at all. The stream ended only
// when the caller happened to write something, which failed, which ended that
// copy, which finally released the close.
//
// On screen it looked like this: a pane running on another machine, ended with
// ctrl+D. The shell printed "exit" and went, and the window stayed open until
// the next key was pressed, which is what reported it.
//
// Negative control: moving the Close back into the defer makes this time out.
func TestAFarDaemonThatHangsUpIsReportedWithoutAWrite(t *testing.T) {
	// The first dial is the link's own handshake and has to reach a daemon.
	// Every dial after it is the connection under test: one that says
	// something and then hangs up, which is what a pane's shell exiting looks
	// like from the other end of a relay.
	stub := startStubDaemon(t, helloOK("far-1", 0))
	var mu sync.Mutex
	dials := 0
	dial := func() (net.Conn, error) {
		mu.Lock()
		dials++
		first := dials == 1
		mu.Unlock()
		if first {
			return stub.dial()
		}
		mine, theirs := net.Pipe()
		go func() {
			_, _ = theirs.Write([]byte("exit\n"))
			_ = theirs.Close()
		}()
		return mine, nil
	}

	dialer := func(_ context.Context, _ Host) (Transport, error) {
		hub, remote := duplexPipe(t)
		go func() { _ = ServeProxy(remote, remote, dial) }()
		return hub, nil
	}
	m := managerFor(t, testOptions(dialer), Host{Name: "build", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if r := m.Reports(ctx)[0]; r.Status != StatusUp {
		t.Fatalf("status is %q (%s), want up", r.Status, r.Reason)
	}

	conn := openTo(t, m, "build")

	// Read what the far side said, then keep reading. The second read is the
	// assertion: it has to end, and it has to end without this side writing.
	type result struct {
		err error
	}
	got := make(chan result, 1)
	go func() {
		buf := make([]byte, 64)
		for {
			_, err := conn.Read(buf)
			if err != nil {
				got <- result{err: err}
				return
			}
		}
	}()

	select {
	case r := <-got:
		if !errors.Is(r.err, io.EOF) && !errors.Is(r.err, ErrStreamClosed) {
			t.Fatalf("the stream ended with %v, want end of file", r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the far daemon hung up and this side was never told, so a pane whose shell exited would stay on screen")
	}
}
