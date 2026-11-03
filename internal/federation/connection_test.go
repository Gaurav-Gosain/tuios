package federation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// A connection to a remote daemon is the primitive under every verb that is
// not a listing. What is proved here is the primitive itself: a stream opened
// on a live link reaches the far side's daemon socket, a stream against a dead
// link fails at once, a stream whose reader stops does not take the link down
// with it, and a far side with nothing to connect to ends the stream cleanly.

// openTo opens a connection to the named host and fails the test if it cannot.
func openTo(t *testing.T, m *Manager, host string) io.ReadWriteCloser {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := m.OpenConnection(ctx, host)
	if err != nil {
		t.Fatalf("open a connection to %s: %v", host, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// TestAStalledConnectionDoesNotStallTheLink is the flow-control property. The
// mux read loop is shared, so a client that stops draining its connection
// would otherwise hold every stream on the link, control stream included, and
// the next listing would tear the link down.
//
// A connection has a window, so the far writer waits for the reader and the
// read loop never does. Before windows the read loop waited up to the stall
// limit (30 s for a connection) on the one full stream, and every pane and
// listing on the machine waited with it: issue 1106.
//
// The small answers come first because they are the case a frame count gets
// wrong: thousands of frames fit in the byte window, far more than the
// stream's channel holds, and a queue bounded by frames parks the read loop
// again.
func TestAStalledConnectionDoesNotStallTheLink(t *testing.T) {
	big := strings.Repeat("x", 64<<10)
	stub := startStubDaemon(t, func(verb string, _ json.RawMessage) (any, *RemoteError) {
		switch verb {
		case "hello":
			return Handshake{Protocol: 1, MinProtocol: 1, DaemonVersion: "far-1"}, nil
		case "flood":
			return map[string]any{"payload": big}, nil
		case "small":
			return map[string]any{"ok": true}, nil
		}
		return map[string]any{"sessions": []any{}}, nil
	})
	opts := testOptions(proxyDialer(t, stub))
	// Short limits, so a read loop that still waits on the stream would drop
	// it within the test.
	opts.stallLimit = 200 * time.Millisecond
	opts.connStallLimit = 200 * time.Millisecond
	opts.CallTimeout = 5 * time.Second
	m := managerFor(t, opts, Host{Name: "build", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if r := m.Reports(ctx)[0]; r.Status != StatusUp {
		t.Fatalf("status is %q (%s), want up", r.Status, r.Reason)
	}

	conn := openTo(t, m, "build")
	s := conn.(*Stream)
	const smalls, floods = 4000, 200
	go func() {
		for range smalls {
			if _, err := io.WriteString(conn, `{"id":1,"verb":"small"}`+"\n"); err != nil {
				return
			}
		}
		for range floods {
			if _, err := io.WriteString(conn, `{"id":2,"verb":"flood"}`+"\n"); err != nil {
				return
			}
		}
	}()

	// Wait for the far writer to fill the window and stop. Asserting before
	// that would pass with no window at all.
	var held int
	deadline := time.Now().Add(15 * time.Second)
	for stable := 0; stable < 5 && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		s.mu.Lock()
		q := s.queued
		s.mu.Unlock()
		if q == held && q > 0 {
			stable++
		} else {
			held, stable = q, 0
		}
	}
	select {
	case <-s.closed:
		t.Fatalf("ASSERTION: a connection nobody reads was dropped: the read loop waited on it")
	default:
	}
	if s.win == nil {
		t.Fatalf("ASSERTION: a connection has no window, so its far writer cannot wait for its reader")
	}
	if held < bulkWindowMin/2 {
		t.Fatalf("the stream holds %d bytes; the far side never filled the window", held)
	}
	if held > windowRecvLimit {
		t.Fatalf("ASSERTION: the stream holds %d bytes, past the %d a window allows", held, windowRecvLimit)
	}
	// Ten stall limits have passed with nobody reading.
	time.Sleep(10 * opts.connStallLimit)
	select {
	case <-s.closed:
		t.Fatalf("ASSERTION: a connection nobody reads was dropped: the read loop waited on it")
	default:
	}

	// The link answers at once. This call is what used to hang: the read
	// loop was parked on the full stream and the control stream's reply sat
	// behind it.
	t0 := time.Now()
	if _, err := m.Call(ctx, "build", "list-sessions", nil); err != nil {
		t.Fatalf("ASSERTION: the link stopped answering behind a stalled connection: %v", err)
	}
	if took := time.Since(t0); took > time.Second {
		t.Fatalf("ASSERTION: a listing took %v behind a stalled connection", took)
	}

	// The reader comes back and gets every answer: the window lost nothing.
	dec := json.NewDecoder(conn)
	for i := range smalls + floods {
		var ans map[string]any
		if err := dec.Decode(&ans); err != nil {
			t.Fatalf("ASSERTION: answer %d of %d did not arrive: %v", i+1, smalls+floods, err)
		}
	}
}

// TestAStalledConnectionWithoutAWindowIsDropped is the same stall on a stream
// with no window, which is every stream to a proxy that predates windows. The
// read loop waits on it for the stall limit, then drops it alone, and the link
// goes on answering.
func TestAStalledConnectionWithoutAWindowIsDropped(t *testing.T) {
	big := strings.Repeat("x", 64<<10)
	stub := startStubDaemon(t, func(verb string, _ json.RawMessage) (any, *RemoteError) {
		switch verb {
		case "hello":
			return Handshake{Protocol: 1, MinProtocol: 1, DaemonVersion: "far-1"}, nil
		case "flood":
			return map[string]any{"payload": big}, nil
		}
		return map[string]any{"sessions": []any{}}, nil
	})
	opts := testOptions(proxyDialer(t, stub))
	opts.stallLimit = 200 * time.Millisecond
	opts.CallTimeout = 5 * time.Second
	m := managerFor(t, opts, Host{Name: "build", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if r := m.Reports(ctx)[0]; r.Status != StatusUp {
		t.Fatalf("status is %q (%s), want up", r.Status, r.Reason)
	}
	// Open the stream the way a hub did before windows.
	l := m.link("build")
	l.mu.Lock()
	mx := l.mux
	l.mu.Unlock()
	conn, err := mx.OpenWithStall(200 * time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if conn.win != nil {
		t.Fatal("a stream opened without a window has one")
	}

	// Ask for far more than the stream can buffer and read none of it. Every
	// answer is at least one frame, and the buffer holds streamBufferFrames.
	for i := range streamBufferFrames * 3 {
		if _, err := io.WriteString(conn, `{"id":`+string(rune('0'+i%10))+`,"verb":"flood"}`+"\n"); err != nil {
			t.Fatalf("write request %d: %v", i, err)
		}
	}
	select {
	case <-conn.closed:
	case <-time.After(10 * time.Second):
		t.Fatalf("ASSERTION: a connection nobody reads was not dropped; the read loop is parked on it")
	}
	if _, err := m.Call(ctx, "build", "list-sessions", nil); err != nil {
		t.Fatalf("ASSERTION: the link stopped answering behind a stalled connection: %v", err)
	}
	if r := m.Reports(ctx)[0]; r.Status != StatusUp {
		t.Fatalf("ASSERTION: the link is %q after one connection stalled, want up", r.Status)
	}

	// The stalled connection, and only it, was ended, and its reader is told
	// why once it drains what was buffered.
	buf := make([]byte, 1<<20)
	var readErr error
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, readErr = conn.Read(buf); readErr != nil {
			break
		}
	}
	if !errors.Is(readErr, ErrStreamStalled) {
		t.Fatalf("ASSERTION: the stalled connection ended with %v, want ErrStreamStalled", readErr)
	}
}

func TestAConnectionEndsWhenTheFarSideHasNoDaemon(t *testing.T) {
	stub := startStubDaemon(t, helloOK("far-1", 0))
	// Every dial after the first reaches nothing: the far side's daemon has
	// gone away since the link came up.
	dials := 0
	dialer := func(_ context.Context, _ Host) (Transport, error) {
		hub, remote := duplexPipe(t)
		go func() {
			_ = ServeProxyFor(remote, remote, func(StreamOpen) (net.Conn, error) {
				dials++
				if dials > 1 {
					return nil, errors.New("connect: no such file or directory")
				}
				return stub.dial()
			})
		}()
		return hub, nil
	}
	m := managerFor(t, testOptions(dialer), Host{Name: "build", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if r := m.Reports(ctx)[0]; r.Status != StatusUp {
		t.Fatalf("status is %q (%s), want up", r.Status, r.Reason)
	}

	conn := openTo(t, m, "build")
	buf := make([]byte, 16)
	n, err := conn.Read(buf)
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("ASSERTION: a connection the far side could not serve read %d byte(s) and %v, want a clean end", n, err)
	}
	// The link itself is fine: the proxy is there, the daemon behind it is not.
	if r := m.Reports(ctx)[0]; r.Status != StatusUp {
		t.Errorf("the link is %q after one connection was refused, want up", r.Status)
	}
}
