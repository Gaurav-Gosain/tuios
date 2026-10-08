//go:build windows

package stopevent

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The ways the stop event can fail, which these tests hold it to:
//
//   - kill-server sets an event the daemon does not wait on;
//   - a request for one daemon stops another;
//   - the socket path in another case names another event;
//   - a set event left from the last daemon stops the next one at once;
//   - a request with no daemon waiting reports success, so kill-server
//     waits for a socket that never goes away.

func TestRequestReachesTheDaemon(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "tuios.sock")
	other := filepath.Join(t.TempDir(), "tuios.sock")

	if err := Request(sock); err == nil {
		t.Fatal("a stop request succeeded with no daemon waiting")
	}

	done := make(chan struct{})
	defer close(done)
	stop, err := Watch(sock, done)
	if err != nil {
		t.Fatal(err)
	}

	if err := Request(other); err == nil {
		t.Fatal("a stop request for another socket succeeded")
	}
	select {
	case <-stop:
		t.Fatal("the daemon stopped on another socket's request")
	case <-time.After(300 * time.Millisecond):
	}

	if err := Request(strings.ToUpper(sock)); err != nil {
		t.Fatalf("Request: %v", err)
	}
	select {
	case <-stop:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon did not see the stop request")
	}
}

func TestASetEventDoesNotStopTheNextDaemon(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "tuios.sock")
	lastDone := make(chan struct{})
	if _, err := Watch(sock, lastDone); err != nil {
		t.Fatal(err)
	}
	// The last daemon is told to stop and still holds its event when the
	// next one starts.
	if err := Request(sock); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	defer close(done)
	stop, err := Watch(sock, done)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-stop:
		t.Fatal("the next daemon stopped on the last daemon's request")
	case <-time.After(500 * time.Millisecond):
	}
	close(lastDone)
}
