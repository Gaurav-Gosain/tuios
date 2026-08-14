package session

import (
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestConcurrentStartsProduceOneDaemon pins what happens when two clients decide
// at the same moment that no daemon is running and each starts one. Exactly one
// must end up owning the socket. The failure this guards is silent: the loser
// would take the stale-socket path, unlink the winner's socket and bind its own,
// leaving the winner listening on an inode no client can reach and its restored
// sessions unreachable with it.
func TestConcurrentStartsProduceOneDaemon(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Cleanup(useResurrectionDir(t.TempDir()))

	const starters = 4
	daemons := make([]*Daemon, starters)
	errs := make([]error, starters)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range starters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
			<-start
			if err := d.Start(); err != nil {
				errs[i] = err
				return
			}
			daemons[i] = d
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	for i := range starters {
		if daemons[i] != nil {
			winners++
			t.Cleanup(daemons[i].Stop)
			continue
		}
		if errs[i] == nil {
			t.Fatalf("starter %d neither started nor failed", i)
		}
		// Both refusals are correct: the loser either lost the lock or took it
		// after the winner was already listening.
		if !errors.Is(errs[i], ErrDaemonStarting) && !strings.Contains(errs[i].Error(), "already running") {
			t.Fatalf("starter %d failed for an unexpected reason: %v", i, errs[i])
		}
	}
	if winners != 1 {
		t.Fatalf("got %d daemons owning the socket, want 1", winners)
	}

	socketPath, err := GetSocketPath()
	if err != nil {
		t.Fatalf("GetSocketPath: %v", err)
	}
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("the surviving socket is not reachable: %v", err)
	}
	_ = conn.Close()
}

// TestStartLockIsReleasedOnShutdown pins that a daemon that has stopped does not
// keep the next one out.
func TestStartLockIsReleasedOnShutdown(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Cleanup(useResurrectionDir(t.TempDir()))

	first := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := first.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	first.Stop()

	second := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := second.Start(); err != nil {
		t.Fatalf("second Start after shutdown: %v", err)
	}
	t.Cleanup(second.Stop)
}
