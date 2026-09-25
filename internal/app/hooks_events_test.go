package app

import (
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

// The hooks system declared eight events and fired three. These tests pin one
// event each to the action that must produce it, so an event cannot go back to
// being a name in the config reference that nothing ever emits.

func hookTestOS(t *testing.T) *OS {
	t.Helper()
	m := NewOS(OSOptions{})
	m.Width, m.Height = 120, 40
	m.SessionName = "test-session"
	return m
}

// FireDetached must not return before its hooks have run: the caller quits
// immediately after, and an unwaited hook goroutine dies with the process.
func TestAfterDetachWaitsForHooks(t *testing.T) {
	m := hookTestOS(t)
	if m.HookManager == nil {
		m.HookManager = hooks.NewManager()
	}
	var ran bool
	var mu sync.Mutex
	m.HookManager.SetRunner(func(string, hooks.Context) {
		mu.Lock()
		defer mu.Unlock()
		ran = true
	})
	m.HookManager.Register(hooks.AfterDetach, "true")

	m.FireDetached()

	mu.Lock()
	defer mu.Unlock()
	if !ran {
		t.Error("FireDetached returned before its hook ran")
	}
}

// hookRecorder collects the contexts hooks fired with, in place of running a
// shell per event.
type hookRecorder struct {
	mu    sync.Mutex
	fired []hooks.Context
}

// record registers every event on m and returns the recorder collecting them.
// Registering all of them (rather than only the one under test) is what catches
// an action that fires the wrong event as well as one that fires none.
func record(t *testing.T, m *OS) *hookRecorder {
	t.Helper()
	if m.HookManager == nil {
		m.HookManager = hooks.NewManager()
	}
	// NewOS loads the config of whoever is running the tests, hooks included.
	// Drop those first: a developer with a real hook configured would otherwise
	// see it fire alongside the test's and fail the one-event-per-action checks.
	m.HookManager.ClearAll()
	r := &hookRecorder{}
	m.HookManager.SetRunner(func(_ string, ctx hooks.Context) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.fired = append(r.fired, ctx)
	})
	for _, e := range hooks.AllEvents() {
		m.HookManager.Register(e, "true")
	}
	return r
}
