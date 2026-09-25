package session

import (
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// Helpers for the tests that watch the daemon's hook table fire.

// hookRecorder collects the firings a daemon's hook table produces, without
// spawning a shell per event.
type hookRecorder struct {
	mu    sync.Mutex
	fired []hooks.Context
}

func (r *hookRecorder) add(_ string, ctx hooks.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fired = append(r.fired, ctx)
}

// of returns the firings for one event, in order.
func (r *hookRecorder) of(event hooks.Event) []hooks.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]hooks.Context, 0, len(r.fired))
	for _, ctx := range r.fired {
		if ctx.EventType == event {
			out = append(out, ctx)
		}
	}
	return out
}

// await waits for want firings of an event and returns them. It fails rather
// than returning short, so a caller never asserts against a half-arrived slice.
func (r *hookRecorder) await(t *testing.T, event hooks.Event, want int) []hooks.Context {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := r.of(event)
		if len(got) >= want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s fired %d times in 5s, want %d", event, len(got), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// startHookDaemon starts a daemon whose hook table holds one command per named
// event and reports the firings instead of running a shell.
func startHookDaemon(t *testing.T, events ...hooks.Event) (*Daemon, string, *hookRecorder) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	t.Cleanup(useResurrectionDir(t.TempDir()))

	table := make(map[string]any, len(events))
	for _, e := range events {
		table[string(e)] = "true"
	}
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true, Hooks: table})
	if d.hooks == nil {
		t.Fatal("the daemon loaded no hook table from its config")
	}
	rec := &hookRecorder{}
	d.hooks.SetRunner(rec.add)

	if err := d.Start(); err != nil {
		t.Fatalf("daemon Start: %v", err)
	}
	t.Cleanup(d.Stop)

	sp, err := GetSocketPath()
	if err != nil {
		t.Fatalf("GetSocketPath: %v", err)
	}
	return d, sp, rec
}

func intPtr(v int) *int { return &v }

// waitUntilHookTest polls until cond holds or the test fails.
func waitUntilHookTest(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
