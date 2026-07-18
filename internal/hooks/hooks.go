// Package hooks implements a shell-command hooks system for tuios.
// Hooks fire asynchronously when specific events occur (window creation,
// focus changes, workspace switches, etc.) and execute user-defined
// shell commands with environment variables providing context.
package hooks

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

// Event represents a hook event type.
type Event string

const (
	AfterNewWindow       Event = "after-new-window"
	AfterCloseWindow     Event = "after-close-window"
	AfterFocusChange     Event = "after-focus-change"
	AfterWorkspaceSwitch Event = "after-workspace-switch"
	AfterAttach          Event = "after-attach"
	AfterDetach          Event = "after-detach"
	AfterLayoutChange    Event = "after-layout-change"
	AfterResize          Event = "after-resize"
)

// AllEvents returns all valid hook event names.
func AllEvents() []Event {
	return []Event{
		AfterNewWindow, AfterCloseWindow, AfterFocusChange,
		AfterWorkspaceSwitch, AfterAttach, AfterDetach,
		AfterLayoutChange, AfterResize,
	}
}

// Context provides environment variables passed to hook commands.
//
// The fields below WindowID apply to every event. The ones after it are
// event-specific and stay at their zero value for the events they do not
// describe, so a hook script can read them unconditionally.
type Context struct {
	WindowID   string
	WindowName string
	Workspace  int
	SessionID  string
	EventType  Event
	// PreviousWorkspace is the workspace that was active before an
	// after-workspace-switch. Zero for every other event.
	PreviousWorkspace int
	// Layout names the tiling layout in force after an after-layout-change:
	// one of bsp, master-stack, scrolling or floating. Empty otherwise.
	Layout string
	// Width and Height are the window's new size in cells after an
	// after-resize. Zero for every other event.
	Width  int
	Height int
}

// Manager manages hook registrations and execution.
type Manager struct {
	mu    sync.RWMutex
	hooks map[Event][]string // event -> list of shell commands
	// run executes one hook command. It is a field so tests can observe which
	// hooks fired, with what context, without spawning a shell per event.
	run func(command string, ctx Context)
	// inFlight tracks running hooks so a test (or a caller that needs the
	// side effects to have landed) can join them instead of sleeping.
	inFlight sync.WaitGroup
}

// NewManager creates a new hooks manager.
func NewManager() *Manager {
	return &Manager{
		hooks: make(map[Event][]string),
		run:   executeHook,
	}
}

// SetRunner replaces the command runner. It exists for tests: the real runner
// spawns a shell, which makes asserting that an event fired with the right
// payload both slow and timing-dependent.
func (m *Manager) SetRunner(run func(command string, ctx Context)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.run = run
}

// Wait blocks until every hook fired so far has finished.
func (m *Manager) Wait() {
	m.inFlight.Wait()
}

// WaitTimeout waits for in-flight hooks, giving up after d. It exists for the
// events fired on the way out: hooks run in their own goroutines, which the
// process exit would otherwise kill before they ran at all. The timeout is what
// keeps a hook that never returns from holding the client open, which is the
// failure this is supposed to prevent rather than cause.
func (m *Manager) WaitTimeout(d time.Duration) {
	done := make(chan struct{})
	go func() {
		m.inFlight.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		log.Printf("hooks: gave up waiting for hooks to finish after %s", d)
	}
}

// Register adds a shell command to be executed for a given event.
func (m *Manager) Register(event Event, command string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks[event] = append(m.hooks[event], command)
}

// Clear removes all hooks for a given event.
func (m *Manager) Clear(event Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.hooks, event)
}

// ClearAll removes all hooks.
func (m *Manager) ClearAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks = make(map[Event][]string)
}

// LoadFromConfig loads hooks from a map (parsed from TOML config).
// The map keys are event names, values are shell commands (string or []string).
func (m *Manager) LoadFromConfig(hookConfig map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks = make(map[Event][]string)

	for key, val := range hookConfig {
		event, ok := ParseEventName(key)
		if !ok {
			log.Printf("hooks: ignoring unknown event %q (valid events: %v)", key, AllEvents())
			continue
		}
		switch v := val.(type) {
		case string:
			if v != "" {
				m.hooks[event] = []string{v}
			}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					m.hooks[event] = append(m.hooks[event], s)
				}
			}
		}
	}
}

// Fire executes all hooks registered for the given event asynchronously.
// Each hook runs in its own goroutine with the provided context as env vars.
func (m *Manager) Fire(event Event, ctx Context) {
	m.mu.RLock()
	commands := m.hooks[event]
	run := m.run
	m.mu.RUnlock()

	if len(commands) == 0 {
		return
	}

	ctx.EventType = event

	for _, cmdStr := range commands {
		m.inFlight.Add(1)
		go func() {
			defer m.inFlight.Done()
			run(cmdStr, ctx)
		}()
	}
}

// HasHooks returns true if any hooks are registered.
func (m *Manager) HasHooks() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.hooks) > 0
}

// executeHook runs a shell command with context as environment variables.
func executeHook(cmdStr string, ctx Context) {
	// Use sh -c for shell interpretation
	cmd := exec.Command("sh", "-c", cmdStr)

	// Set environment variables
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("TUIOS_EVENT=%s", ctx.EventType),
		fmt.Sprintf("TUIOS_WINDOW_ID=%s", ctx.WindowID),
		fmt.Sprintf("TUIOS_WINDOW_NAME=%s", ctx.WindowName),
		fmt.Sprintf("TUIOS_WORKSPACE=%d", ctx.Workspace),
		fmt.Sprintf("TUIOS_SESSION_ID=%s", ctx.SessionID),
		fmt.Sprintf("TUIOS_PREV_WORKSPACE=%d", ctx.PreviousWorkspace),
		fmt.Sprintf("TUIOS_LAYOUT=%s", ctx.Layout),
		fmt.Sprintf("TUIOS_WIDTH=%d", ctx.Width),
		fmt.Sprintf("TUIOS_HEIGHT=%d", ctx.Height),
	)

	// Run silently - don't capture output or block
	cmd.Stdout = nil
	cmd.Stderr = nil

	_ = cmd.Run()
}

// ParseEventName validates and returns an Event from a string.
func ParseEventName(name string) (Event, bool) {
	event := Event(strings.TrimSpace(name))
	if slices.Contains(AllEvents(), event) {
		return event, true
	}
	return "", false
}
