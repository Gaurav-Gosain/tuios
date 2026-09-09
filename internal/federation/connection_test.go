package federation

import (
	"bufio"
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

func TestAConnectionReachesTheRemoteDaemonSocket(t *testing.T) {
	stub := startStubDaemon(t, func(verb string, _ json.RawMessage) (any, *RemoteError) {
		switch verb {
		case "hello":
			return Handshake{Protocol: 1, MinProtocol: 1, DaemonVersion: "far-1"}, nil
		case "whoami":
			return map[string]any{"answer": "the far daemon"}, nil
		}
		return nil, &RemoteError{Code: "unknown_verb", Message: verb}
	})
	m := managerFor(t, testOptions(proxyDialer(t, stub)), Host{Name: "build", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if r := m.Reports(ctx)[0]; r.Status != StatusUp {
		t.Fatalf("status is %q (%s), want up", r.Status, r.Reason)
	}

	// The connection is a byte pipe to the daemon socket on the far side: a
	// verb line written on it is answered by that daemon, not by the link.
	conn := openTo(t, m, "build")
	if _, err := io.WriteString(conn, `{"id":7,"verb":"whoami"}`+"\n"); err != nil {
		t.Fatalf("write on the connection: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read the answer: %v", err)
	}
	if !strings.Contains(line, "the far daemon") {
		t.Fatalf("ASSERTION: the connection did not reach the far daemon, it answered %q", line)
	}

	// The control stream is untouched by traffic on the connection.
	if _, err := m.Call(ctx, "build", "hello", nil); err != nil {
		t.Errorf("ASSERTION: the control stream stopped answering beside an open connection: %v", err)
	}
}

func TestAConnectionToADownHostFailsAtOnce(t *testing.T) {
	m := managerFor(t, testOptions(func(context.Context, Host) (Transport, error) {
		return nil, errors.New("ssh: connect to host poweredoff port 22: No route to host")
	}), Host{Name: "offline", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.Reports(ctx)

	started := time.Now()
	_, err := m.OpenConnection(ctx, "offline")
	var unreachable *UnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("ASSERTION: a connection to a down host returned %v, want UnreachableError", err)
	}
	if unreachable.Host != "offline" {
		t.Errorf("the error names %q, want the host", unreachable.Host)
	}
	if took := time.Since(started); took > time.Second {
		t.Errorf("the refusal took %v; a down host must not be waited on", took)
	}

	_, err = m.OpenConnection(ctx, "nowhere")
	if !errors.Is(err, ErrUnknownHost) {
		t.Errorf("ASSERTION: an unknown name returned %v, want ErrUnknownHost", err)
	}
}

// TestAStalledConnectionDoesNotStallTheLink is the flow-control property. The
// mux read loop is shared, so a client that stops draining its connection
// would otherwise hold every stream on the link, control stream included, and
// the next listing would tear the link down.
func TestAStalledConnectionDoesNotStallTheLink(t *testing.T) {
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

	// Ask for far more than the stream can buffer and read none of it. Every
	// answer is at least one frame, and the buffer holds streamBufferFrames.
	conn := openTo(t, m, "build")
	for i := range streamBufferFrames * 3 {
		if _, err := io.WriteString(conn, `{"id":`+string(rune('0'+i%10))+`,"verb":"flood"}`+"\n"); err != nil {
			t.Fatalf("write request %d: %v", i, err)
		}
	}

	// The mux gives up on the connection once its buffer has been full for
	// the stall limit. Waiting for that first is what makes the assertions
	// below say anything: a call made before the buffer filled would pass with
	// the bound deleted.
	select {
	case <-conn.(*Stream).closed:
	case <-time.After(10 * time.Second):
		t.Fatalf("ASSERTION: a connection nobody reads was not dropped; the read loop is parked on it")
	}

	// The link keeps answering. This call is what used to hang: the read loop
	// was parked on the full stream and the control stream's reply sat behind
	// it until the call timed out and took the link down.
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
			_ = ServeProxy(remote, remote, func() (net.Conn, error) {
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
