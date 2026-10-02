package session

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

// Session-side hooks.
//
// Hooks used to be wired entirely into the client, so a session running
// detached under the daemon fired none at all: a pane could be created, exit,
// or finish an agent turn with nobody attached and no hook would run. That is
// the opposite of what a hook is for, because the detached session is exactly
// the one a program is watching instead of a person.
//
// The rule applied here is that a hook fires from the side that owns the fact.
//
//   - The window set, the focused window, the current workspace and a pane's
//     agent state are the daemon's. It owns them whether or not anybody is
//     attached, so it fires their hooks. A client attached to a daemon session
//     no longer fires them (see FireHookContext in internal/app), which is also
//     what makes three attached clients produce one firing rather than three.
//   - A client's viewport size, its attach and its detach belong to that one
//     client. They stay client-side: three clients attaching is three attaches,
//     and the command has to run on the machine the person is sitting at. The
//     layout stays client-side too, because tiling runs in the attached
//     renderer and no daemon-side operation can change it.
//
// A standalone tuios (no daemon) is unaffected: it is the whole system, so it
// keeps firing everything itself.
//
// Firing happens off the session's event sink, which is fed by the state diff
// in state_events.go. The diff runs once per state convergence however the
// mutation arrived, so a hook fires exactly once no matter how many clients are
// attached or which one made the change.

// sessionHookEvents maps a daemon stream event to the hook event it raises. An
// event that is not in this map raises no hook, which is how the high-frequency
// output and bell events cost nothing.
var sessionHookEvents = map[string]hooks.Event{
	EventWindowCreated:     hooks.AfterNewWindow,
	EventWindowClosed:      hooks.AfterCloseWindow,
	EventWindowFocused:     hooks.AfterFocusChange,
	EventWorkspaceSwitched: hooks.AfterWorkspaceSwitch,
	EventAgentState:        hooks.AfterAgentState,
	EventCommandFinished:   hooks.AfterCommandFinished,
}

// sessionSideHooks is sessionHookEvents keyed the other way: the hook events the
// daemon fires. Derived rather than written out, so the two can never disagree
// about which side owns an event.
var sessionSideHooks = func() map[hooks.Event]bool {
	set := make(map[hooks.Event]bool, len(sessionHookEvents))
	for _, ev := range sessionHookEvents {
		set[ev] = true
	}
	return set
}()

// SessionSideHookEvents lists the hook events the daemon fires, in the order
// AllEvents declares them. It is exported so the client can stay silent on
// exactly these and no others, and so the two lists cannot drift.
func SessionSideHookEvents() []hooks.Event {
	out := make([]hooks.Event, 0, len(sessionSideHooks))
	for _, ev := range hooks.AllEvents() {
		if sessionSideHooks[ev] {
			out = append(out, ev)
		}
	}
	return out
}

// hookDrainTimeout bounds how long a shutting-down daemon waits for the hooks
// it has already started.
const hookDrainTimeout = 2 * time.Second

// agentHookGate holds the settle timers for after-agent-state. The settle
// window is the anti-flicker rule the client already applies: a pane that flips
// out of a state and back inside the window produces nothing rather than two
// firings.
type agentHookGate struct {
	mu      sync.Mutex
	pending map[string]*time.Timer // session name + "\x00" + window id
}

func newAgentHookGate() *agentHookGate {
	return &agentHookGate{pending: make(map[string]*time.Timer)}
}

// stop cancels every parked firing. It runs on daemon shutdown.
func (g *agentHookGate) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for key, t := range g.pending {
		t.Stop()
		delete(g.pending, key)
	}
}

// ApplyUserHooks copies the hook tables out of the user config.
//
// Every place that builds a DaemonConfig calls this rather than assigning the
// three fields itself. There are three such places and they are easy to miss:
// `tuios daemon`, the ssh server's in-process daemon, and tuios-web's. A daemon
// started by one of them without hooks is worse than one with no hooks at all,
// because an attached client leaves the session-side events to the daemon, so
// the commands would stop running rather than run twice.
//
// A nil user config leaves the daemon with no hooks, which is what a caller
// that could not read the file has.
func (cfg *DaemonConfig) ApplyUserHooks(uc *config.UserConfig) {
	if cfg == nil || uc == nil {
		return
	}
	cfg.Hooks = uc.Hooks
	cfg.AgentAlerts = &uc.Notifications.Agent
	cfg.AgentHookCommand = uc.Notifications.Agent.Command
}

// loadHooks builds the daemon's hook table from the user config. A daemon with
// no hooks configured keeps a nil manager, so the sink's first check is a nil
// test and the event path costs nothing.
func (d *Daemon) loadHooks(cfg *DaemonConfig) {
	m := hooks.NewManager()
	if len(cfg.Hooks) > 0 {
		m.LoadFromConfig(cfg.Hooks)
	}
	// The client-side events are dropped rather than held and never fired. A
	// table the daemon keeps but cannot run would appear in list-hooks as a
	// command with no runs, which reads as "your hook is broken" when the truth
	// is that the client runs it.
	for _, e := range hooks.AllEvents() {
		if !sessionSideHooks[e] {
			m.Clear(e)
		}
	}
	// [notifications.agent].command is the shorthand spelling of an
	// after-agent-state hook. It is registered rather than substituted, so a
	// user who wrote both spellings gets both, which is what [hooks] does for
	// two commands on one event.
	if cmd := strings.TrimSpace(cfg.AgentHookCommand); cmd != "" {
		m.Register(hooks.AfterAgentState, cmd)
	}
	d.agentAlerts = config.ResolveAgentAlerts(cfg.AgentAlerts)
	if !m.HasHooks() {
		return
	}
	d.hooks = m
	d.agentHooks = newAgentHookGate()
}

// fireSessionHooks raises the hook a session event implies.
//
// It runs on the session's event sink, which is called with the session's state
// lock held. Two rules follow from that and neither is optional. It must return
// immediately for the events that carry no hook, because output and bell arrive
// on every chunk a pane writes. And it must never read the session back: the
// whole hook environment is built from the event, which is why the diff carries
// the window's name and workspace rather than leaving them to be looked up.
func (d *Daemon) fireSessionHooks(sess *Session, ev SessionEvent) {
	if d.hooks == nil {
		return
	}
	event, ok := sessionHookEvents[ev.Type]
	if !ok {
		return
	}
	if !d.hooks.HasEvent(event) {
		return
	}
	if ev.Type == EventAgentState {
		d.fireAgentStateHook(sess, ev)
		return
	}
	d.hooks.Fire(event, hookContext(sess.Name(), ev))
}

// hookContext is the environment a hook command reads, built from the event
// alone.
func hookContext(sessionName string, ev SessionEvent) hooks.Context {
	ctx := hooks.Context{
		SessionID:  sessionName,
		WindowID:   ev.Window,
		WindowName: ev.hookTitle,
		Workspace:  ev.hookWorkspace,
	}
	if ev.Type == EventWorkspaceSwitched {
		ctx.Workspace = ev.Workspace
		ctx.PreviousWorkspace = ev.hookPrevWorkspace
	}
	if ev.Type == EventAgentState {
		ctx.AgentState = ev.State
		ctx.PrevAgentState = ev.hookPrevState
		ctx.AgentHarness = ev.hookHarness
		ctx.AgentMessage = ev.hookMessage
	}
	if ev.Type == EventCommandFinished {
		ctx.Command = ev.Cmdline
		if ev.ExitCode != nil {
			ctx.ExitCode = strconv.Itoa(*ev.ExitCode)
		}
		ctx.DurationMS = strconv.FormatInt(ev.DurationMS, 10)
	}
	return ctx
}

// findWindowState returns a window row by id.
func findWindowState(state *SessionState, id string) (WindowState, bool) {
	if state == nil {
		return WindowState{}, false
	}
	for i := range state.Windows {
		if state.Windows[i].ID == id {
			return state.Windows[i], true
		}
	}
	return WindowState{}, false
}
