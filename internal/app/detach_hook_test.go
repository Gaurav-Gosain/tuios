package app

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

// after-detach is documented as belonging to the client: three clients
// attaching is three attaches, and the same for leaving. The attach half has
// held for every client since cd9637cf. The detach half fired from
// DetachClient alone, which is the leader-d path, so an SSH client whose
// connection closed and a browser tab that went away left without a word.

// detachHookOS is a daemon-session client with one after-detach command
// registered, and the file that command would create.
func detachHookOS(t *testing.T) (*OS, string) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "detached")

	mgr := hooks.NewManager()
	mgr.Register(hooks.AfterDetach, "touch "+marker)

	return &OS{
		Settings:        config.Global,
		HookManager:     mgr,
		IsDaemonSession: true,
		SessionName:     "work",
	}, marker
}

func fired(marker string) bool {
	_, err := os.Stat(marker)
	return err == nil
}

// TestAConnectionThatEndedIsADetach. This is the report: the client never
// passes through DetachClient, and until now nothing else fired the hook.
//
// FireDetached waits for what it fired, so there is nothing to poll for.
//
// Negative control: removing the call from Cleanup fails here with no marker.
func TestAConnectionThatEndedIsADetach(t *testing.T) {
	m, marker := detachHookOS(t)

	m.Cleanup()

	if !fired(marker) {
		t.Error("a client whose connection ended fired no after-detach hook")
	}
}

// TestOnePersonLeavingIsOneDetach.
//
// Over SSH one client reaches both arrivals: leader-d fires the hook, and the
// session middleware runs Cleanup afterwards regardless. Firing per arrival
// rather than per client would run a person's command twice for one detach,
// and a command that posts a message or releases a lock is not idempotent
// because we would like it to be.
//
// Negative control: dropping the CompareAndSwap in FireDetached fails here
// with two runs.
func TestOnePersonLeavingIsOneDetach(t *testing.T) {
	m, _ := detachHookOS(t)

	var mu sync.Mutex
	runs := 0
	mgr := hooks.NewManager()
	mgr.SetRunner(func(string, hooks.Context) {
		mu.Lock()
		runs++
		mu.Unlock()
	})
	mgr.Register(hooks.AfterDetach, "anything")
	m.HookManager = mgr

	// Both arrivals, in the order an SSH client meets them: leader-d detaches,
	// then the middleware tears the session down anyway.
	m.FireDetached()
	m.Cleanup()

	mu.Lock()
	defer mu.Unlock()
	if runs != 1 {
		t.Errorf("one person leaving ran the after-detach command %d times", runs)
	}
}

// TestADetachFiresTheCommandAtAll is the control for the count above: a test
// that asserts "exactly one" passes just as well when the answer is always
// zero, so the runner has to be shown running.
func TestADetachFiresTheCommandAtAll(t *testing.T) {
	m, _ := detachHookOS(t)

	var mu sync.Mutex
	runs := 0
	mgr := hooks.NewManager()
	mgr.SetRunner(func(string, hooks.Context) {
		mu.Lock()
		runs++
		mu.Unlock()
	})
	mgr.Register(hooks.AfterDetach, "anything")
	m.HookManager = mgr

	m.FireDetached()

	mu.Lock()
	defer mu.Unlock()
	if runs != 1 {
		t.Errorf("a deliberate detach ran the command %d times, want 1", runs)
	}
}

// TestAKilledSessionIsNotADetach, and neither is a daemon that went away. The
// hook answers "this person left", and running it when the session was
// destroyed under them reports a departure that did not happen.
//
// Negative control: firing from Cleanup without the ExitReason check fails
// here for all three.
func TestAKilledSessionIsNotADetach(t *testing.T) {
	for _, c := range []struct {
		name   string
		reason ExitReason
	}{
		{"the session was killed", ExitSessionKilled},
		{"the daemon went away", ExitDaemonLost},
		{"the link to the host closed", ExitHostLost},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, marker := detachHookOS(t)
			m.ExitReason = c.reason

			m.Cleanup()

			if fired(marker) {
				t.Error("an after-detach hook ran for a client that did not leave")
			}
		})
	}
}

// TestAnEphemeralClientHasNoDetachToFire. The hook is about a session someone
// can come back to, and a client that owns its own shells is not leaving one
// behind.
func TestAnEphemeralClientHasNoDetachToFire(t *testing.T) {
	m, marker := detachHookOS(t)
	m.IsDaemonSession = false

	m.Cleanup()

	if fired(marker) {
		t.Error("an ephemeral client fired an after-detach hook")
	}
}
