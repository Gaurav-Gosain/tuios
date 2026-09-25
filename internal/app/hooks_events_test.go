package app

import (
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

// The hooks system declared eight events and fired three. These tests pin one
// event each to the action that must produce it, so an event cannot go back to
// being a name in the config reference that nothing ever emits.

// hookRecorder collects the contexts hooks fired with, in place of running a
// shell per event.
type hookRecorder struct {
	mu    sync.Mutex
	fired []hooks.Context
}

// only returns the single context fired for event, failing when the count is
// anything but one: a hook firing twice for one action is as wrong as not
// firing, and a resize hook that runs per mouse-motion event is the specific
// version of that this guards.
func (r *hookRecorder) only(t *testing.T, m *OS, event hooks.Event) hooks.Context {
	t.Helper()
	m.HookManager.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()

	var got []hooks.Context
	for _, c := range r.fired {
		if c.EventType == event {
			got = append(got, c)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%s fired %d times, want 1 (all fired: %v)", event, len(got), r.events())
	}
	return got[0]
}

// events lists what fired, for failure messages.
func (r *hookRecorder) events() []hooks.Event {
	out := make([]hooks.Event, 0, len(r.fired))
	for _, c := range r.fired {
		out = append(out, c.EventType)
	}
	return out
}

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
