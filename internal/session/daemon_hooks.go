package session

import (
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

var sessionHookEvents = map[string]hooks.Event{
	EventWindowCreated:     hooks.AfterNewWindow,
	EventWindowClosed:      hooks.AfterCloseWindow,
	EventWindowFocused:     hooks.AfterFocusChange,
	EventWorkspaceSwitched: hooks.AfterWorkspaceSwitch,
	EventAgentState:        hooks.AfterAgentState,
	EventCommandFinished:   hooks.AfterCommandFinished,
}

var sessionSideHooks = func() map[hooks.Event]bool {
	set := make(map[hooks.Event]bool, len(sessionHookEvents))
	for _, ev := range sessionHookEvents {
		set[ev] = true
	}
	set[hooks.AfterNewWorktree] = true
	set[hooks.AfterSettle] = true
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

const hookDrainTimeout = 2 * time.Second

type agentHookGate struct {
	mu      sync.Mutex
	pending map[string]*time.Timer
}

func newAgentHookGate() *agentHookGate {
	return &agentHookGate{pending: make(map[string]*time.Timer)}
}

func (g *agentHookGate) park(key string, d time.Duration, fire func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, ok := g.pending[key]; ok {
		t.Stop()
		delete(g.pending, key)
	}
	g.pending[key] = time.AfterFunc(d, func() {
		g.mu.Lock()
		delete(g.pending, key)
		g.mu.Unlock()
		fire()
	})
}

func (g *agentHookGate) cancel(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, ok := g.pending[key]; ok {
		t.Stop()
		delete(g.pending, key)
	}
}

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

func (d *Daemon) loadHooks(cfg *DaemonConfig) {
	m, policy := buildHookTable(cfg)
	m.SetHold(d.pipes.hold)
	d.agentAlerts.Store(&policy)
	d.hooks = m
	d.agentHooks = newAgentHookGate()
}

func buildHookTable(cfg *DaemonConfig) (*hooks.Manager, config.AgentAlertPolicy) {
	m := hooks.NewManager()
	m.SetDaemonEnv(true)
	if len(cfg.Hooks) > 0 {
		m.LoadFromConfig(cfg.Hooks)
	}
	for _, e := range hooks.AllEvents() {
		if !sessionSideHooks[e] {
			m.Clear(e)
		}
	}
	if cmd := strings.TrimSpace(cfg.AgentHookCommand); cmd != "" {
		m.Register(hooks.AfterAgentState, cmd)
	}
	return m, config.ResolveAgentAlerts(cfg.AgentAlerts)
}

func (d *Daemon) reloadHooks(uc *config.UserConfig, byPerson bool) {
	if d.hooks == nil || uc == nil {
		return
	}
	var cfg DaemonConfig
	cfg.ApplyUserHooks(uc)
	next, policy := buildHookTable(&cfg)
	d.agentAlerts.Store(&policy)
	if !byPerson {
		running := d.hooks.Commands()
		for _, c := range next.Commands() {
			if !slices.Contains(running, c) {
				if !d.hooksWaiting.Swap(true) {
					log.Printf("[hooks] config.toml has a hook the daemon does not run yet. It runs after tuios config apply from outside tuios, or a daemon restart.")
				}
				return
			}
		}
	}
	d.hooks.ReplaceFrom(next)
	d.hooksWaiting.Store(false)
}

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
	if d.agentsOff.Load() && isAgentEvent(ev.Type) {
		return
	}
	if ev.Type == EventAgentState {
		d.fireAgentStateHook(sess, ev)
		return
	}
	d.hooks.Fire(event, hookContext(sess.Name(), ev))
}

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

func (d *Daemon) fireAgentStateHook(sess *Session, ev SessionEvent) {
	var policy config.AgentAlertPolicy
	if p := d.agentAlerts.Load(); p != nil {
		policy = *p
	}
	key := sess.Name() + "\x00" + ev.Window

	d.agentHooks.cancel(key)

	if !policy.Alerts(ev.State) {
		return
	}
	if policy.Quiet(time.Now()) {
		return
	}

	sessionID, sessionName, window, to := sess.ID, sess.Name(), ev.Window, ev.State
	ctx := hookContext(sessionName, ev)

	suppressed := func() bool {
		if !policy.SuppressFocused || d.findTUIClient(sessionID) == nil {
			return false
		}
		if d.sessionHostFocus(sessionID) == HostFocusUnfocused {
			return false
		}
		live := d.manager.GetSessionByID(sessionID)
		if live == nil {
			return false
		}
		state := live.GetState()
		return state != nil && state.FocusedWindowID == window
	}

	if policy.Settle <= 0 {
		go func() {
			if !suppressed() {
				d.hooks.Fire(hooks.AfterAgentState, ctx)
			}
		}()
		return
	}

	d.agentHooks.park(key, policy.Settle, func() {
		if suppressed() {
			return
		}
		live := d.manager.GetSessionByID(sessionID)
		if live == nil {
			return
		}
		w, ok := findWindowState(live.GetState(), window)
		if !ok || w.AgentState.Name() != to {
			return
		}
		d.hooks.Fire(hooks.AfterAgentState, ctx)
	})
}
