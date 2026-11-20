package app

import (
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

// The client's hook table, and how a config reload changes it.
//
// The client read [hooks] once, at start, so a hook added to config.toml did
// nothing until the next attach. A reload now builds the table again, by the
// rule the daemon follows for its own table (session.Daemon.reloadHooks): a
// hook runs a command, and a process in a pane runs as the person and can
// write config.toml, the apply stamp beside it, a set-config or a tape that
// runs Reload config. So a reload the person did not ask for applies the new
// table only when every hook in it already runs: it removes hooks, or changes
// nothing. A hook that is new or changed waits for Reload config, run by the
// person's own key or palette row, and the client says so once.

// hooksWaitNotice is what the client says when the file has a hook that waits.
const hooksWaitNotice = "The config file has a new or changed hook. To turn it on, run Reload config."

// clientHookTable is the hook table a config describes for a client.
func clientHookTable(cfg *config.UserConfig) *hooks.Manager {
	m := hooks.NewManager()
	if cfg.Hooks != nil {
		m.LoadFromConfig(cfg.Hooks)
	}
	// [notifications.agent].command is shorthand for the after-agent-state
	// hook, so it is discoverable beside the toggles that gate it. Registering
	// rather than replacing means a user who wrote both spellings gets both,
	// which is what [hooks] does for two commands on any other event.
	if cmd := strings.TrimSpace(cfg.Notifications.Agent.Command); cmd != "" {
		m.Register(hooks.AfterAgentState, cmd)
	}
	return m
}

// reloadHooks puts the hook table of cfg in force. byPerson says the person
// asked for the reload with their own key. Without it, a table with a hook
// that does not run now is not applied, and the person is told once.
func (m *OS) reloadHooks(cfg *config.UserConfig, byPerson bool) {
	if m.HookManager == nil || cfg == nil {
		return
	}
	next := clientHookTable(cfg)
	running, want := m.HookManager.Commands(), next.Commands()
	if slices.Equal(running, want) {
		// Nothing changed. The table stays, and so does what list-hooks
		// says about its last runs.
		m.hooksWaitTold = false
		return
	}
	if !byPerson {
		for _, c := range want {
			if slices.Contains(running, c) {
				continue
			}
			if !m.hooksWaitTold {
				m.hooksWaitTold = true
				m.ShowNotification(hooksWaitNotice, "warning", m.Settings.NotificationWarningDuration)
			}
			return
		}
	}
	m.HookManager.ReplaceFrom(next)
	m.hooksWaitTold = false
}

// notThePersonsAction reports whether the action being run came from
// send-keys, a tape's Press or Action, or run-command, and not from the
// person's own key or palette row.
func (m *OS) notThePersonsAction() bool {
	return m.ProcessingRemoteKeys || m.pressingTapeKeys || m.runningScriptAction
}
