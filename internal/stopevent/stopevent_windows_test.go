//go:build windows

package stopevent

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRequestReachesTheDaemon(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "tuios.sock")
	other := filepath.Join(t.TempDir(), "tuios.sock")

	if err := Request(sock); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("a stop request with no daemon waiting returned %v, want ErrNotWaiting", err)
	}

	done := make(chan struct{})
	defer close(done)
	stop, err := Watch(sock, done)
	if err != nil {
		t.Fatal(err)
	}

	if err := Request(other); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("a stop request for another socket returned %v, want ErrNotWaiting", err)
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
