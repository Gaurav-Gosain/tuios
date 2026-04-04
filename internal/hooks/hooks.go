// Package hooks implements a shell-command hooks system for tuios.
// Hooks fire asynchronously when specific events occur (window creation,
// focus changes, workspace switches, etc.) and execute user-defined
// shell commands with environment variables providing context.
package hooks

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
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
type Context struct {
	WindowID   string
	WindowName string
	Workspace  int
	SessionID  string
	EventType  Event
}

// Manager manages hook registrations and execution.
type Manager struct {
	mu    sync.RWMutex
	hooks map[Event][]string // event -> list of shell commands
}

// NewManager creates a new hooks manager.
func NewManager() *Manager {
	return &Manager{
		hooks: make(map[Event][]string),
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
		event := Event(key)
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
	m.mu.RUnlock()

	if len(commands) == 0 {
		return
	}

	ctx.EventType = event

	for _, cmdStr := range commands {
		go executeHook(cmdStr, ctx)
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
