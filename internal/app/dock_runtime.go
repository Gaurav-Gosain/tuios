package app

import (
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/dockengine"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// InitDockComponents builds the component plan from the loaded config and
// starts the engine. Returns the command that listens for updates, or nil when
// there is nothing to listen to.
func (m *OS) InitDockComponents() tea.Cmd {
	m.rebuildDockPlan()
	m.dockEngine.Stop()

	comps := dockRefreshableComponents(m.UserConfig, m.dockPlan, m.Settings.ShowClock, &m.Settings)
	m.railCustom.runnable = m.railCustomRunnable()
	rail := m.railCustomComponent()
	m.railCustom.on = rail != nil
	if rail != nil {
		comps = append(comps, rail)
	}
	secondary := m.secondaryRailCustomComponent()
	m.railCustomSecond.on = secondary != nil
	if secondary != nil {
		comps = append(comps, secondary)
	}
	m.dockEngine = dockengine.New(comps, railFolder)
	m.dockEngine.SetContext(m.SessionName, dockSocketPath())
	m.railCustom.ctx = m.railContextNow()
	m.dockEngine.SetRailContext(m.railCustom.ctx)
	m.railCustomSecond.ctx = m.secondaryRailContextNow()
	m.dockEngine.SetSecondaryRailContext(m.railCustomSecond.ctx)
	for _, c := range comps {
		if c.Builtin {
			m.refreshBuiltinDockComponent(c.Name)
		}
	}
	m.dockEngine.Start()
	return ListenForDockComponents(m.dockEngine)
}

// StopDockComponents kills every component this client started. Components are
// UI: they belong to the attached client and die with it, and a push component
// is a process that would otherwise outlive the session that started it.
func (m *OS) StopDockComponents() {
	m.dockEngine.Stop()
	m.dockEngine = nil
	m.railCustom.on = false
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

// DockMetersSyncCmd keeps the CPU and RAM samplers in step with their live
// settings. Update calls it after each handler, so settings, remote commands
// and tape actions all use the existing dock reload path. An unchanged meter
// set costs no timer or reload; a meter omitted from the plan never polls.
func (m *OS) DockMetersSyncCmd() tea.Cmd {
	if m.dockEngine == nil {
		return nil
	}
	for _, meter := range []struct {
		name string
		on   bool
	}{
		{config.DockComponentCPU, m.Settings.ShowCPU},
		{config.DockComponentRAM, m.Settings.ShowRAM},
	} {
		_, running := m.dockEngine.Component(meter.name)
		if running != (m.dockPlan.Has(meter.name) && meter.on) {
			return m.ReloadDockComponents(nil)
		}
	}
	return nil
}

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
//
// The listener belongs to one engine and ends when that engine stops. A config
// reload replaces the engine, and a listener parked on the old channel would
// otherwise wait forever on a receive nothing answers: one goroutine leaked per
// reload. The message carries the engine it came from, so Update re-arms only
// the current engine's listener.
func ListenForDockComponents(e *dockengine.Engine) tea.Cmd {
	if e == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case u := <-e.Updates():
			u.From = e
			return dockComponentMsg(u)
		case <-e.Done():
			return nil
		}
	}
}

func (m *OS) handleDockComponent(msg dockComponentMsg) bool {
	if msg.Builtin {
		return m.refreshBuiltinDockComponent(msg.Name)
	}
	changed, newFailure := m.dockEngine.ApplyUpdate(dockengine.Update(msg))
	if msg.Name == railSecondaryComponent {
		if changed {
			m.railCustomSecond.gen++
		}
		if newFailure {
			title := m.secondaryRailCustomConfig().ResolvedTitle()
			m.LogWarn("Rail section %s failed: %s", title, msg.Err)
			m.ShowNotification("Rail section "+title+": "+dockFailureDetail(msg), "warning", m.Settings.NotificationDuration)
			return true
		}
		return changed
	}
	if msg.Name == railCustomComponent {
		if changed {
			m.railCustom.gen++
		}
		if newFailure {
			m.LogWarn("Rail section %s failed: %s", m.railCustomTitle(), msg.Err)
			m.ShowNotification("Rail section "+m.railCustomTitle()+": "+dockFailureDetail(msg), "warning", m.Settings.NotificationDuration)
			return true
		}
		return changed
	}
	if newFailure {
		name := strings.TrimPrefix(msg.Name, config.DockCustomPrefix)
		m.LogWarn("Dock component %s failed: %s", name, msg.Err)
		m.ShowNotification("Dock component "+name+": "+dockFailureDetail(msg), "warning", m.Settings.NotificationDuration)
		return true
	}
	return changed
}

func dockFailureDetail(msg dockComponentMsg) string {
	if msg.Exit > 0 {
		return "exit " + strconv.Itoa(msg.Exit)
	}
	return msg.Err
}

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
	m.syncRailContext()
	m.dockEngine.NotifyEvent(eventType)
	if alias := dockEventAlias(eventType); alias != "" {
		m.dockEngine.NotifyEvent(alias)
	}
}

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
	string(hooks.AfterThemeChange):     "theme-changed",
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
	if strings.HasPrefix(name, config.DockPluginPrefix) {
		return m.runPluginCellClick(name)
	}
	c, ok := m.dockEngine.Component(name)
	if !ok || strings.TrimSpace(c.OnClick) == "" {
		return false
	}
	env := m.dockEngine.CommandEnv(name, "TUIOS_CLICK_BUTTON="+strconv.Itoa(button))
	command := c.OnClick
	go func() {
		// #nosec G204 - the user's own config, run as the user, exactly as a
		cmd := exec.Command("sh", "-c", command)
		cmd.Env = env
		cmd.Stdout, cmd.Stderr = nil, nil
		_ = cmd.Run()
	}()
	return true
}

type dockComponentMsg dockengine.Update
