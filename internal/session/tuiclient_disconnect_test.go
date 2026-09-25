package session

import (
	"net"
	"testing"
	"time"
)

// newTestTUIClient builds a TUIClient wired to one end of an in-memory pipe so
// the read loop can be exercised without a real daemon.
func newTestTUIClient(conn net.Conn) *TUIClient {
	c := NewTUIClient()
	c.conn = conn
	return c
}

// TestReadLoopAppCloseNoDisconnect verifies that an app-initiated Close is
// treated as expected teardown and does NOT fire the disconnect handler.
func TestReadLoopAppCloseNoDisconnect(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	c := newTestTUIClient(client)

	fired := make(chan error, 1)
	c.OnDisconnect(func(err error) {
		fired <- err
	})

	c.StartReadLoop()

	// App-initiated close: closes done then conn.
	_ = c.Close()

	select {
	case <-fired:
		t.Fatal("disconnect handler fired on app-initiated Close")
	case <-time.After(300 * time.Millisecond):
		// Good: no spurious disconnect notification.
	}
}
