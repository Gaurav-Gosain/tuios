package app

import (
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The dock engine's half of the Bubble Tea loop: build the plan, start the
// scheduler, listen on its channel, and fold each update into the model.
//
// The listener is the same shape every other off-goroutine source in this
// package uses (ListenForPTYData and friends): a command that blocks on a
// channel and is re-armed by whoever handles its message. A blocked receive is
// not a timer, which is why a dock with nothing to poll still costs nothing.

// InitDockComponents builds the component plan from the loaded config and
// starts the engine. Returns the command that listens for updates, or nil when
// there is nothing to listen to.
func (m *OS) InitDockComponents() tea.Cmd {
	m.dockPlan = buildDockPlan(m.UserConfig)
	m.dockEngine.Stop()

	comps := dockRefreshableComponents(m.UserConfig, m.dockPlan, m.Settings.ShowClock, &m.Settings)
	m.dockEngine = newDockEngine(comps)
	m.dockEngine.SetContext(m.SessionName, dockSocketPath())
	// The built-ins are filled here rather than by the engine, because their
	// values come from model state this goroutine owns.
	for _, c := range comps {
		if c.Builtin {
			m.refreshBuiltinDockComponent(c.Name)
		}
	}
	m.dockEngine.Start()
	return ListenForDockComponents(m.dockEngine.Updates())
}

// StopDockComponents kills every component this client started. Components are
// UI: they belong to the attached client and die with it, and a push component
// is a process that would otherwise outlive the session that started it.
func (m *OS) StopDockComponents() {
	m.dockEngine.Stop()
	m.dockEngine = nil
}

// SyncDockContext re-stamps the session name and socket onto the engine, for
// the paths that learn which session they are on after Init has run: a daemon
// attach names the session when the connection lands, and a session switch
// changes it. Without this a component's TUIOS_SESSION would be whatever was
// known at boot, which for an attach is nothing.
func (m *OS) SyncDockContext() {
	m.dockEngine.SetContext(m.SessionName, dockSocketPath())
}

// ReloadDockComponents rebuilds the plan after the config file changed. Without
// this a dock component added to the file would need a restart, which for a
// feature whose whole distribution story is "copy a file" would be most of the
// story missing.
func (m *OS) ReloadDockComponents(cfg *config.UserConfig) tea.Cmd {
	if cfg != nil {
		m.UserConfig = cfg
	}
	return m.InitDockComponents()
}

// dockSocketPath is the socket a component's command talks back through, so a
// component can call tuios verbs without working out where the session lives.
// Empty when there is no daemon, which is the honest answer for a standalone
// session: there is no socket to call.
func dockSocketPath() string {
	socket, err := session.GetSocketPath()
	if err != nil {
		return ""
	}
	return socket
}

// ListenForDockComponents blocks on the engine's channel and turns each update
// into a message. Returns nil when there is no engine, so a model without one
// arms nothing.
func ListenForDockComponents(ch <-chan dockComponentUpdate) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		u, ok := <-ch
		if !ok {
			return nil
		}
		return dockComponentMsg(u)
	}
}

// handleDockComponent folds one update into the model and reports whether the
// frame has to be redrawn.
//
// This is the render gate the design turns on: a poll whose value has not moved
// draws nothing. A one-second component watching a number that changes every
// five minutes costs sixty executions an hour and no frames.
func (m *OS) handleDockComponent(msg dockComponentMsg) bool {
	if msg.Builtin {
		return m.refreshBuiltinDockComponent(msg.Name)
	}
	changed, newFailure := m.dockEngine.applyUpdate(dockComponentUpdate(msg))
	if newFailure {
		// Quiet on the bar, loud exactly once. A cell that fails is hidden
		// rather than left showing a value its command can no longer produce,
		// but a component that silently shows nothing is the failure mode the
		// hackability audit kept finding, so the first failure of a streak is
		// put in front of the person who wrote it.
		name := strings.TrimPrefix(msg.Name, config.DockCustomPrefix)
		detail := msg.Err
		if msg.Exit > 0 {
			detail = "exit " + strconv.Itoa(msg.Exit)
		}
		m.LogWarn("Dock component %s failed: %s", name, msg.Err)
		m.ShowNotification("Dock component "+name+": "+detail, "warning", m.Settings.NotificationDuration)
		return true
	}
	return changed
}

// refreshBuiltinDockComponent re-measures one of the built-ins that move on
// their own and reports whether its cell text changed.
//
// These live on the same scheduler as a user's own components on purpose. If
// the clock and the meters could not be expressed as components then the
// component model would be a bolt-on rather than the thing the dock is made of.
// It is also what let NeedsDockTick go: they used to hold the maintenance tick
// at sixty frames a second for a clock that moves once a second and meters that
// move once every two.
func (m *OS) refreshBuiltinDockComponent(name string) bool {
	switch name {
	case config.DockComponentClock:
		return m.dockEngine.SetBuiltinText(name, time.Now().Format(m.dockClockFormat()))
	case config.DockComponentCPU:
		m.UpdateCPUHistory()
		return m.dockEngine.SetBuiltinText(name, m.GetCPUGraph())
	case config.DockComponentRAM:
		m.UpdateRAMUsage()
		return m.dockEngine.SetBuiltinText(name, m.GetRAMUsage())
	}
	return false
}

// dockClockFormat is the layout the clock and the status badge both render.
func (m *OS) dockClockFormat() string {
	if m.UserConfig == nil {
		return config.DefaultClockFormat
	}
	return m.UserConfig.Dock.DockClockFormat(&m.Settings)
}

// DockClockText is the clock's current reading, for the status badge. It comes
// from the engine so the badge and the dock cell can never disagree, and falls
// back to reading the wall clock for a model with no engine (a test fixture, or
// a frame drawn before Init).
func (m *OS) DockClockText() string {
	if text := m.dockEngine.Text(config.DockComponentClock); text != "" {
		return text
	}
	return time.Now().Format(m.dockClockFormat())
}

// NotifyDockEvent marks every event-driven component watching this type as due.
// Called from the paths that already know something happened, so the wake is
// paid for by the thing that happened rather than by a clock, and costs nothing
// at all when nothing is happening.
func (m *OS) NotifyDockEvent(eventType string) {
	m.dockEngine.NotifyEvent(eventType)
	// Both spellings resolve. The hook table calls it after-focus-change and
	// the daemon's event hub calls it window-focused, and a component author
	// should not have to know which of the two they are talking to.
	if alias := dockEventAlias(eventType); alias != "" {
		m.dockEngine.NotifyEvent(alias)
	}
}

// dockEventAliases maps a hook event to the name the daemon's event hub gives
// the same thing. Refresh contracts accept either.
var dockEventAliases = map[string]string{
	string(hooks.AfterNewWindow):       "window-created",
	string(hooks.AfterCloseWindow):     "window-closed",
	string(hooks.AfterFocusChange):     "window-focused",
	string(hooks.AfterWorkspaceSwitch): "workspace-switched",
	string(hooks.AfterAgentState):      "agent-state",
	string(hooks.AfterLayoutChange):    "layout-changed",
	string(hooks.AfterAttach):          "attached",
	string(hooks.AfterDetach):          "detached",
	string(hooks.AfterResize):          "resized",
}

func dockEventAlias(eventType string) string { return dockEventAliases[eventType] }

// RefreshDockComponent re-runs one component now, or every one when name is
// empty. This is what the refresh-dock verb reaches, and it is what makes a
// component scriptable from a hook, from cron, and from an agent.
func (m *OS) RefreshDockComponent(name string) error {
	if strings.TrimSpace(name) == "" {
		m.dockEngine.RefreshAll()
		return nil
	}
	// A caller may name a component with or without the custom/ prefix; the
	// short form is what they wrote in the config file.
	if _, ok := m.dockEngine.Component(name); !ok {
		if _, ok := m.dockEngine.Component(config.DockCustomPrefix + name); ok {
			name = config.DockCustomPrefix + name
		}
	}
	return m.dockEngine.Refresh(name)
}

// RunDockComponentClick runs a component's on-click command, the way a hook is
// run: detached, output discarded, and never on the render goroutine. The cell
// that was clicked is named in the environment along with the button, so one
// script can serve a left and a right click. It reports whether the component
// had a command to run, so a cell without one falls through to whatever is
// under it.
func (m *OS) RunDockComponentClick(name string, button int) bool {
	c, ok := m.dockEngine.Component(name)
	if !ok || strings.TrimSpace(c.OnClick) == "" {
		return false
	}
	env := m.dockEngine.commandEnv(name, "TUIOS_CLICK_BUTTON="+strconv.Itoa(button))
	command := c.OnClick
	go func() {
		// #nosec G204 - the user's own config, run as the user, exactly as a
		// hook's command is.
		cmd := exec.Command("sh", "-c", command)
		cmd.Env = env
		cmd.Stdout, cmd.Stderr = nil, nil
		_ = cmd.Run()
	}()
	return true
}
