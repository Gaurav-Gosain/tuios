// Package hooks implements a shell-command hooks system for tuios.
// Hooks fire asynchronously when specific events occur (window creation,
// focus changes, workspace switches, etc.) and execute user-defined
// shell commands with environment variables providing context. The one
// exception is after-new-worktree, which its caller runs and waits for (see
// Run).
package hooks

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
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
	// AfterAgentState fires when a pane's agent state changes to one the
	// [notifications.agent] policy alerts on. It is the only event gated by
	// config rather than by the raw fact, because it is an alert sink: firing it
	// on every flip would make it the thing people mute.
	AfterAgentState Event = "after-agent-state"
	// AfterCommandFinished fires when a shell with OSC 133 integration reports
	// that a command finished. It needs the shell to send the marks; a shell
	// that does not never fires it.
	AfterCommandFinished Event = "after-command-finished"
	// AfterThemeChange fires when a client puts other theme colours in force:
	// a theme picked, the Omarchy theme switched, the light and dark pair
	// followed, or the terminal's own colours changed with no theme. It fires
	// once a pick is made, not for each theme the picker shows on the way.
	AfterThemeChange Event = "after-theme-change"
	// AfterNewWorktree fires when the daemon has made a worktree and its
	// session, before the session's first window starts. It is the one event
	// run synchronously (see Run): its job is to put files into the tree, and a
	// copy running in the background would race the shell or agent the first
	// window starts there.
	AfterNewWorktree Event = "after-new-worktree"
	// AfterSettle fires when an agent is settled: put away on the agents
	// view's Settled shelf. The daemon fires it, so it runs with nobody
	// attached. It does not fire when the agent comes back.
	AfterSettle Event = "after-settle"
)

// AllEvents returns all valid hook event names.
func AllEvents() []Event {
	return []Event{
		AfterNewWindow, AfterCloseWindow, AfterFocusChange,
		AfterWorkspaceSwitch, AfterAttach, AfterDetach,
		AfterLayoutChange, AfterResize, AfterAgentState,
		AfterCommandFinished, AfterThemeChange, AfterNewWorktree,
		AfterSettle,
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
	// AgentState is the state the pane moved into on an after-agent-state, and
	// PrevAgentState the one it came from, both in the wire spelling
	// set-agent-state accepts. AgentHarness is the harness id the reporting
	// source named and AgentMessage the free text it carried. All empty for
	// every other event.
	//
	// The ranked source that won is deliberately absent: it lives in the
	// daemon's claim map rather than in synced window state, and get-agent-state
	// is where it is read.
	AgentState     string
	PrevAgentState string
	AgentHarness   string
	AgentMessage   string
	// Command is the command line a finished command ran, ExitCode its exit
	// status and DurationMS how long it ran, for after-command-finished.
	// ExitCode is empty when the shell sent no status. All empty for every
	// other event.
	Command    string
	ExitCode   string
	DurationMS string
	// Theme is the theme id in force after an after-theme-change, or none
	// when no theme is. ThemeBg is the background panes are told they are on,
	// as #rrggbb, and empty when tuios does not know it. ThemeLight is 1 when
	// that background is light and 0 when it is dark. All empty for every
	// other event.
	Theme      string
	ThemeBg    string
	ThemeLight string
	// WorktreePath, WorktreeBranch and WorktreeRepoRoot describe the worktree
	// an after-new-worktree made: its directory, its branch and the main
	// checkout it was made from. WorktreeCreatedBranch is 1 when the branch
	// was made with it and 0 when it existed. All empty for every other event.
	WorktreePath          string
	WorktreeBranch        string
	WorktreeRepoRoot      string
	WorktreeCreatedBranch string
	// SettleReason says what settled the agent on an after-settle: person
	// for the D key or mark-attention settle. Empty for every other event.
	SettleReason string
	// Dir is the working directory the command starts in. Empty runs it in
	// the process's own, which is what every event but after-new-worktree has.
	Dir string
}

// Manager manages hook registrations and execution.
type Manager struct {
	mu    sync.RWMutex
	hooks map[Event][]string // event -> list of shell commands
	// run executes one hook command and reports what it did. timeout bounds
	// it, and zero leaves it unbounded. It is a field so tests can observe
	// which hooks fired, with what context, without spawning a shell per event.
	run func(command string, ctx Context, timeout time.Duration) hookResult
	// status is what each registered command last did, keyed by its event and
	// its position in that event's list. See status.go.
	status map[statusKey]*Status
	// inFlight tracks running hooks so a test (or a caller that needs the
	// side effects to have landed) can join them instead of sleeping.
	inFlight sync.WaitGroup
	// hold, when set, is held while a hook runs: the daemon will not hand
	// off while a pipe it reads is open. See SetHold.
	hold func(what string) (release func())
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
// payload both slow and timing-dependent. A replaced runner reports success, so
// a test that only watches for firings does not have to describe an exit code.
func (m *Manager) SetRunner(run func(command string, ctx Context)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.run = func(command string, ctx Context, _ time.Duration) hookResult {
		run(command, ctx)
		return hookResult{}
	}
}

// SetHold sets what each hook holds while it runs. It may wait before the
// hook starts.
func (m *Manager) SetHold(hold func(what string) (release func())) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hold = hold
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

// HasEvent reports whether any command is registered for an event.
func (m *Manager) HasEvent(event Event) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.hooks[event]) > 0
}

// Clear removes all hooks for a given event.
func (m *Manager) Clear(event Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.hooks, event)
	m.clearStatus()
}

// ClearAll removes all hooks.
func (m *Manager) ClearAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks = make(map[Event][]string)
	m.clearStatus()
}

// LoadFromConfig loads hooks from a map (parsed from TOML config).
// The map keys are event names, values are shell commands (string or []string).
func (m *Manager) LoadFromConfig(hookConfig map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks = make(map[Event][]string)
	m.clearStatus()

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
	hold := m.hold
	m.mu.RUnlock()

	if len(commands) == 0 {
		return
	}

	ctx.EventType = event

	for i, cmdStr := range commands {
		m.inFlight.Go(func() {
			if hold != nil {
				defer hold("the " + string(event) + " hook is running")()
			}
			if Verbose() {
				log.Printf("hooks: the %s event fires: %s", event, cmdStr)
			}
			m.record(event, i, cmdStr, run(cmdStr, ctx, 0))
		})
	}
}

// Run executes the commands registered for event one after another and waits
// for each, unlike Fire. Each one gets at most timeout. It stops at the first
// command that fails, since a later step usually needs the earlier one, and
// returns an error that names the exit (or the timeout) and the last lines of
// that command's stderr. No command registered is a nil error.
//
// The caller blocks for as long as the commands take, so it must hold no lock
// another caller waits on.
func (m *Manager) Run(event Event, ctx Context, timeout time.Duration) error {
	m.mu.RLock()
	commands := slices.Clone(m.hooks[event])
	run := m.run
	m.mu.RUnlock()

	ctx.EventType = event
	for i, cmdStr := range commands {
		if Verbose() {
			log.Printf("hooks: the %s event runs: %s", event, cmdStr)
		}
		res := run(cmdStr, ctx, timeout)
		m.record(event, i, cmdStr, res)
		if res.err == nil && res.exitCode == 0 {
			continue
		}
		what := fmt.Sprintf("the %s hook exited with %d", event, res.exitCode)
		if res.err != nil {
			what = fmt.Sprintf("the %s hook failed: %v", event, res.err)
		}
		if tail := lastLines(res.stderr, runErrorLines); tail != "" {
			what += ": " + tail
		}
		return errors.New(what)
	}
	return nil
}

// runErrorLines is how many stderr lines a Run error carries. The error ends
// up in a notification and a CLI warning, where the last few lines say why
// and a full log would push everything else away.
const runErrorLines = 3

// lastLines returns the last n non-empty lines of s, joined by " / " so the
// result stays on one line.
func lastLines(s string, n int) string {
	var lines []string
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " / ")
}

// HasHooks returns true if any hooks are registered.
func (m *Manager) HasHooks() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.hooks) > 0
}

// executeHook runs a shell command with context as environment variables. It
// reports the exit code, how long the command took, and the tail of its stderr,
// which is what makes a failing hook visible instead of silent.
func executeHook(cmdStr string, ctx Context, timeout time.Duration) hookResult {
	cctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(cctx, timeout)
		defer cancel()
	}
	// Use sh -c for shell interpretation
	cmd := childproc.CommandContext(cctx, "sh", "-c", cmdStr)
	cmd.Dir = ctx.Dir
	if timeout > 0 {
		// The timeout ends whatever the command started, not just sh. And a
		// child it left running in the background, holding stderr open, does
		// not keep the caller waiting past the command's own exit.
		childproc.OwnGroup(cmd)
		cmd.WaitDelay = time.Second
	}

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
		fmt.Sprintf("TUIOS_AGENT_STATE=%s", ctx.AgentState),
		fmt.Sprintf("TUIOS_AGENT_PREV_STATE=%s", ctx.PrevAgentState),
		fmt.Sprintf("TUIOS_AGENT_HARNESS=%s", ctx.AgentHarness),
		fmt.Sprintf("TUIOS_AGENT_MESSAGE=%s", ctx.AgentMessage),
		fmt.Sprintf("TUIOS_COMMAND=%s", ctx.Command),
		fmt.Sprintf("TUIOS_EXIT_CODE=%s", ctx.ExitCode),
		fmt.Sprintf("TUIOS_DURATION_MS=%s", ctx.DurationMS),
		fmt.Sprintf("TUIOS_THEME=%s", ctx.Theme),
		fmt.Sprintf("TUIOS_THEME_BG=%s", ctx.ThemeBg),
		fmt.Sprintf("TUIOS_THEME_LIGHT=%s", ctx.ThemeLight),
		fmt.Sprintf("TUIOS_WORKTREE_PATH=%s", ctx.WorktreePath),
		fmt.Sprintf("TUIOS_WORKTREE_BRANCH=%s", ctx.WorktreeBranch),
		fmt.Sprintf("TUIOS_WORKTREE_REPO_ROOT=%s", ctx.WorktreeRepoRoot),
		fmt.Sprintf("TUIOS_WORKTREE_CREATED_BRANCH=%s", ctx.WorktreeCreatedBranch),
		fmt.Sprintf("TUIOS_SETTLE_REASON=%s", ctx.SettleReason),
	)

	// Stdout stays discarded: a hook is run for its side effects and nothing
	// reads what it prints. Stderr is kept, but only its tail, because that is
	// the one thing that explains a failure and a user command may write
	// without limit.
	cmd.Stdout = nil
	tail := &tailBuffer{limit: stderrTailLimit}
	cmd.Stderr = tail

	start := time.Now()
	err := cmd.Run()
	res := hookResult{duration: time.Since(start), stderr: tail.String()}
	if timeout > 0 && errors.Is(cctx.Err(), context.DeadlineExceeded) {
		res.err = fmt.Errorf("it did not finish in %s and was stopped", timeout)
		res.exitCode = -1
		return res
	}
	if err != nil {
		res.err = err
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			res.exitCode = exit.ExitCode()
			// The error text for an exit status says only "exit status 3",
			// which the exit code already says. Drop it so the message is the
			// stderr the user actually needs.
			res.err = nil
			if res.stderr == "" {
				res.stderr = "the hook wrote nothing to stderr"
			}
		}
	}
	return res
}

// ParseEventName validates and returns an Event from a string.
func ParseEventName(name string) (Event, bool) {
	event := Event(strings.TrimSpace(name))
	if slices.Contains(AllEvents(), event) {
		return event, true
	}
	return "", false
}
