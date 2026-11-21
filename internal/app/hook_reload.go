package app

import (
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

const hooksWaitNotice = "The config file has a new or changed hook. To turn it on, run Reload config."

func clientHookTable(cfg *config.UserConfig) *hooks.Manager {
	m := hooks.NewManager()
	if cfg.Hooks != nil {
		m.LoadFromConfig(cfg.Hooks)
	}
	if cmd := strings.TrimSpace(cfg.Notifications.Agent.Command); cmd != "" {
		m.Register(hooks.AfterAgentState, cmd)
	}
	return m
}

func (m *OS) reloadHooks(cfg *config.UserConfig, byPerson bool) {
	if m.HookManager == nil || cfg == nil {
		return
	}
	next := clientHookTable(cfg)
	running, want := m.HookManager.Commands(), next.Commands()
	if slices.Equal(running, want) {
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

func (m *OS) notThePersonsAction() bool {
	return m.ProcessingRemoteKeys || m.pressingTapeKeys || m.runningScriptAction
}
