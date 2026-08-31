package session

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

// Hooks used to be wired entirely into the client, so a session running
// detached under the daemon fired none of them: a window could be created, be
// closed, or finish an agent turn with nobody attached and no command ran.
// These pin the fix at the level the fix works at, which is the daemon's own
// event sink.

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

// settle gives any further firing a chance to arrive before a count is trusted.
// It is only used where the assertion is that nothing more happens.
func (r *hookRecorder) settle() { time.Sleep(300 * time.Millisecond) }

// startHookDaemon starts a daemon whose hook table holds one command per named
// event and reports the firings instead of running a shell.
func startHookDaemon(t *testing.T, events ...hooks.Event) (*Daemon, string, *hookRecorder) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
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

// TestADetachedSessionFiresTheNewWindowHook is the gap itself. Nothing is
// attached, nothing is drawing, and the command still has to run: the detached
// session is the one a program is watching instead of a person.
func TestADetachedSessionFiresTheNewWindowHook(t *testing.T) {
	d, sp, rec := startHookDaemon(t, hooks.AfterNewWindow)
	// The session's own first window already fires the hook, which is itself the
	// point. Let it land so the assertion is about the window this test creates.
	makeSessionWithWindow(t, d, "detached")
	rec.await(t, hooks.AfterNewWindow, 1)
	c := dialVerb(t, sp)

	created := result(t, c.call(t, `{"verb":"new-window","params":{"session":"detached","name":"build"}}`))
	windowID, _ := created["window_id"].(string)

	fired := rec.await(t, hooks.AfterNewWindow, 2)
	got := fired[len(fired)-1]
	if got.WindowID != windowID {
		t.Errorf("hook window id = %q, want the created window %q", got.WindowID, windowID)
	}
	if got.WindowName != "build" {
		t.Errorf("hook window name = %q, want build", got.WindowName)
	}
	if got.SessionID != "detached" {
		t.Errorf("hook session = %q, want detached", got.SessionID)
	}
	if got.Workspace != 1 {
		t.Errorf("hook workspace = %d, want 1", got.Workspace)
	}
}

// TestADetachedSessionFiresTheCloseWindowHook is the event the brief names: a
// pane goes away and the hook has to fire with no client attached. It also pins
// the name, which is the one field that cannot be looked up afterwards because
// the window is already gone.
func TestADetachedSessionFiresTheCloseWindowHook(t *testing.T) {
	d, sp, rec := startHookDaemon(t, hooks.AfterCloseWindow)
	makeSessionWithWindow(t, d, "detached")
	c := dialVerb(t, sp)

	created := result(t, c.call(t, `{"verb":"new-window","params":{"session":"detached","name":"doomed"}}`))
	windowID := created["window_id"].(string)
	rec.settle()

	result(t, c.call(t, `{"verb":"close-window","params":{"session":"detached","window":"doomed"}}`))

	fired := rec.await(t, hooks.AfterCloseWindow, 1)
	got := fired[0]
	if got.WindowID != windowID {
		t.Errorf("hook window id = %q, want the closed window %q", got.WindowID, windowID)
	}
	if got.WindowName != "doomed" {
		t.Errorf("hook window name = %q, want the name the window had", got.WindowName)
	}
}

// TestAWorkspaceSwitchHookNamesTheWorkspaceItLeft covers the one field the
// daemon cannot read back from state: the workspace is already gone by the time
// the event is read, so the diff has to carry it.
func TestAWorkspaceSwitchHookNamesTheWorkspaceItLeft(t *testing.T) {
	d, sp, rec := startHookDaemon(t, hooks.AfterWorkspaceSwitch)
	makeSessionWithWindow(t, d, "spaces")
	c := dialVerb(t, sp)

	result(t, c.call(t, `{"verb":"select-workspace","params":{"session":"spaces","workspace":3}}`))

	got := rec.await(t, hooks.AfterWorkspaceSwitch, 1)[0]
	if got.Workspace != 3 {
		t.Errorf("hook workspace = %d, want 3", got.Workspace)
	}
	if got.PreviousWorkspace != 1 {
		t.Errorf("hook previous workspace = %d, want the 1 it left", got.PreviousWorkspace)
	}
}

// TestAFocusChangeFiresInADetachedSession keeps focus on the daemon's side of
// the line: the focused window is session state, not one client's idea of it.
func TestAFocusChangeFiresInADetachedSession(t *testing.T) {
	d, sp, rec := startHookDaemon(t, hooks.AfterFocusChange)
	makeSessionWithWindow(t, d, "focus")
	c := dialVerb(t, sp)

	first := result(t, c.call(t, `{"verb":"new-window","params":{"session":"focus","name":"one"}}`))["window_id"].(string)
	result(t, c.call(t, `{"verb":"new-window","params":{"session":"focus","name":"two"}}`))
	rec.settle()

	result(t, c.call(t, `{"verb":"focus-window","params":{"session":"focus","window":"one"}}`))

	rec.await(t, hooks.AfterFocusChange, 1)
	rec.settle()
	fired := rec.of(hooks.AfterFocusChange)
	last := fired[len(fired)-1]
	if last.WindowID != first {
		t.Errorf("hook window id = %q, want the newly focused %q", last.WindowID, first)
	}
	if last.WindowName != "one" {
		t.Errorf("hook window name = %q, want one", last.WindowName)
	}
}

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

// TestTheAgentStateHookObeysTheAlertPolicy keeps the meaning of the event: it
// is the only one gated by config rather than by the raw fact, and moving it to
// the daemon must not quietly turn a muted state back on.
func TestTheAgentStateHookObeysTheAlertPolicy(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Cleanup(useResurrectionDir(t.TempDir()))

	off := false
	d := NewDaemon(&DaemonConfig{
		Version:            "test",
		DisableAutoRestore: true,
		Hooks:              map[string]any{string(hooks.AfterAgentState): "true"},
		// No settle window, so the assertion is about the policy and not about a
		// timer. suppress_focused off, because the pane under test is the
		// focused one and this is not the test for that rule.
		AgentAlerts: &config.AgentAlertsConfig{
			SettleSeconds:   intPtr(0),
			SuppressFocused: &off,
		},
	})
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
	makeSessionWithWindow(t, d, "agents")
	c := dialVerb(t, sp)

	// working is muted by default, so it must not reach the command.
	result(t, c.call(t, `{"verb":"set-agent-state","params":{"session":"agents","state":"working"}}`))
	rec.settle()
	if got := rec.of(hooks.AfterAgentState); len(got) != 0 {
		t.Fatalf("a muted state fired the hook %d times: %+v", len(got), got)
	}

	// done alerts by default, so it must.
	result(t, c.call(t, `{"verb":"set-agent-state","params":{"session":"agents","state":"done","harness":"claude-code"}}`))
	got := rec.await(t, hooks.AfterAgentState, 1)[0]
	if got.AgentState != "done" {
		t.Errorf("hook agent state = %q, want done", got.AgentState)
	}
	if got.PrevAgentState != "working" {
		t.Errorf("hook previous agent state = %q, want the working it came from", got.PrevAgentState)
	}
	if got.AgentHarness != "claude-code" {
		t.Errorf("hook harness = %q, want claude-code", got.AgentHarness)
	}
}

// TestTheClientOnlyEventsAreNotFiredByTheDaemon names the line. Attach, detach,
// resize and layout describe one client's terminal, so the daemon must not
// claim them; firing them here would mean three attaches for three clients and
// a command running on the wrong machine.
func TestTheClientOnlyEventsAreNotFiredByTheDaemon(t *testing.T) {
	clientOnly := []hooks.Event{
		hooks.AfterAttach, hooks.AfterDetach, hooks.AfterResize, hooks.AfterLayoutChange,
	}
	sessionSide := SessionSideHookEvents()
	for _, e := range clientOnly {
		if slices.Contains(sessionSide, e) {
			t.Errorf("%s is fired by the daemon, but it describes one client's terminal", e)
		}
	}
	// And the split is exhaustive: every event belongs to exactly one side.
	for _, e := range hooks.AllEvents() {
		inSession := slices.Contains(sessionSide, e)
		inClient := slices.Contains(clientOnly, e)
		if inSession == inClient {
			t.Errorf("%s is on %s side", e, map[bool]string{true: "both", false: "neither"}[inSession])
		}
	}
}

// TestADaemonWithNoHooksHoldsNoTable keeps the cost of the common case at one
// nil check. Every event a pane writes passes through the sink.
func TestADaemonWithNoHooksHoldsNoTable(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	if d.hooks != nil {
		t.Error("a daemon with no hooks configured built a hook table anyway")
	}
	if d.agentHooks != nil {
		t.Error("a daemon with no hooks configured built the agent settle gate anyway")
	}
}

// TestTheAgentCommandShorthandIsAHook pins the second spelling.
// [notifications.agent].command is documented as shorthand for an
// after-agent-state hook, and the daemon has to honour it or a user who wrote
// only that spelling gets nothing when detached.
func TestTheAgentCommandShorthandIsAHook(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	d := NewDaemon(&DaemonConfig{
		Version:            "test",
		DisableAutoRestore: true,
		AgentHookCommand:   "notify-send done",
	})
	if d.hooks == nil {
		t.Fatal("the shorthand built no hook table")
	}
	statuses := d.hooks.Statuses()
	if len(statuses) != 1 || statuses[0].Event != string(hooks.AfterAgentState) {
		t.Fatalf("the shorthand registered %+v, want one after-agent-state hook", statuses)
	}
	if statuses[0].Command != "notify-send done" {
		t.Errorf("command = %q, want the configured one", statuses[0].Command)
	}
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

// TestApplyUserHooksCarriesBothSpellings covers the helper every DaemonConfig
// builder calls. There are three builders and missing one is silent: an
// attached client leaves the session-side events to the daemon, so a daemon
// started without hooks stops running those commands rather than running them
// twice.
func TestApplyUserHooksCarriesBothSpellings(t *testing.T) {
	uc := &config.UserConfig{
		Hooks: config.HooksConfig{"after-new-window": "echo hi"},
	}
	uc.Notifications.Agent.Command = "notify-send done"

	cfg := &DaemonConfig{}
	cfg.ApplyUserHooks(uc)

	if cfg.Hooks["after-new-window"] != "echo hi" {
		t.Errorf("the [hooks] table did not reach the daemon config: %v", cfg.Hooks)
	}
	if cfg.AgentHookCommand != "notify-send done" {
		t.Errorf("agent command = %q, want the configured one", cfg.AgentHookCommand)
	}
	if cfg.AgentAlerts == nil {
		t.Error("the alert policy did not reach the daemon config, so the gate falls back to defaults")
	}

	// And a daemon built from it really holds both.
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true,
		Hooks: cfg.Hooks, AgentAlerts: cfg.AgentAlerts, AgentHookCommand: cfg.AgentHookCommand})
	if d.hooks == nil {
		t.Fatal("the daemon built no hook table from the applied config")
	}
	if got := len(d.hooks.Statuses()); got != 2 {
		t.Errorf("the daemon holds %d hooks, want the table entry and the shorthand", got)
	}

	// A nil user config is a caller that could not read the file, not a panic.
	var empty DaemonConfig
	empty.ApplyUserHooks(nil)
	if empty.Hooks != nil {
		t.Error("a nil user config produced a hook table")
	}
}
