package session

import (
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// Helpers for the tests that watch the daemon's hook table fire.

func intPtr(v int) *int { return &v }

// TestASessionSideHookFiresOncePerEventNotOncePerClient is the multi-client
// rule. Three clients are attached to the same session, they all hear about the
// window, they all push the converged state back the way a real TUI does after
// every keystroke, and the command still runs once. The hook belongs to the
// session, not to whoever is looking at it.
func TestASessionSideHookFiresOncePerEventNotOncePerClient(t *testing.T) {
	d, sp, rec := startHookDaemon(t, hooks.AfterNewWindow)
	makeSessionWithWindow(t, d, "crowded")

	// Each client keeps the last state it was sent, which is what it would push
	// back on its next sync.
	type peer struct {
		client *TUIClient
		mu     sync.Mutex
		state  *SessionState
	}
	peers := make([]*peer, 0, 3)
	for i := range 3 {
		p := &peer{client: NewTUIClient()}
		if err := p.client.Connect("test", 80, 24); err != nil {
			t.Fatalf("client %d connect: %v", i, err)
		}
		p.client.OnStateSync(func(state *SessionState, _, _ string) {
			p.mu.Lock()
			p.state = state
			p.mu.Unlock()
		})
		state, err := p.client.AttachSession("crowded", false, 80, 24)
		if err != nil {
			t.Fatalf("client %d attach: %v", i, err)
		}
		p.state = state
		p.client.StartReadLoop()
		t.Cleanup(func() { _ = p.client.Close() })
		peers = append(peers, p)
	}
	waitUntilHookTest(t, "three clients to be attached", func() bool {
		return d.getSessionClientCount(d.manager.GetSession("crowded").ID) == 3
	})
	rec.settle()
	before := len(rec.of(hooks.AfterNewWindow))

	c := dialVerb(t, sp)
	result(t, c.call(t, `{"verb":"new-window","params":{"session":"crowded","name":"shared"}}`))

	rec.await(t, hooks.AfterNewWindow, before+1)

	// Every client now pushes back the state it holds. This is the echo that
	// would fire the hook a second and third time if the events were emitted per
	// push rather than derived from the difference between two states.
	waitUntilHookTest(t, "every client to see the new window", func() bool {
		for _, p := range peers {
			p.mu.Lock()
			n := len(p.state.Windows)
			p.mu.Unlock()
			if n != 2 {
				return false
			}
		}
		return true
	})
	for i, p := range peers {
		p.mu.Lock()
		state := p.state
		p.mu.Unlock()
		if err := p.client.UpdateState(state); err != nil {
			t.Fatalf("client %d push: %v", i, err)
		}
	}
	rec.settle()

	after := rec.of(hooks.AfterNewWindow)
	if len(after)-before != 1 {
		t.Fatalf("one window created with three clients attached fired the hook %d times, want 1",
			len(after)-before)
	}
	if after[len(after)-1].WindowName != "shared" {
		t.Errorf("hook window name = %q, want shared", after[len(after)-1].WindowName)
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

// hookRecorder collects the firings a daemon's hook table produces, without
// spawning a shell per event.
type hookRecorder struct {
	mu    sync.Mutex
	fired []hooks.Context
}

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

// settle gives any further firing a chance to arrive before a count is trusted.
// It is only used where the assertion is that nothing more happens.
func (r *hookRecorder) settle() { time.Sleep(300 * time.Millisecond) }
