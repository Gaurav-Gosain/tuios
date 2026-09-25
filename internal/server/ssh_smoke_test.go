package server

import (
	"net"
	"testing"
)

// freePort asks the OS for an unused localhost TCP port and returns it. There is
// a small window between closing the listener and the server rebinding, which is
// acceptable for a test.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer func() { _ = l.Close() }()
	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	return port
}
