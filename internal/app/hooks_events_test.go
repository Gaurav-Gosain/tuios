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
