package main

import (
	"io"
	"os"
	"testing"
)

// `tuios attach` used to tear down its interface, print why it was stopping,
// and then sit there for four seconds before the process ended, which reads as
// a client that has hung. The stall was fang's error renderer querying the
// terminal for its background color, so the tests here pin the mechanism that
// keeps a command failure away from that renderer, and pin the exit status each
// of the three ways an attach can end must produce.

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what was
// written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = orig
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return string(data)
}
