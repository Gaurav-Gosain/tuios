package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func browserAlertNotices(cfg *config.UserConfig) []string {
	var alerts *config.AgentAlertsConfig
	if cfg != nil {
		alerts = &cfg.Notifications.Agent
	}
	policy := config.ResolveAgentAlerts(alerts)
	if !policy.Enabled {
		return nil
	}

	var out []string
	if policy.Notify && alerts != nil && alerts.Notify != nil {
		out = append(out, "[notifications.agent] notify: a browser has no desktop "+
			"notifications, so tuios does not send them here. The dock message still appears.")
	}
	if policy.PlaysAudio() {
		out = append(out, "[notifications.agent] sound: the sound plays on the machine that runs "+
			"tuios-web, not in the browser. Set sound_mode = \"bell\" to flash the terminal instead.")
	}
	return out
}

func sshAlertNotices(cfg *config.UserConfig) []string {
	var alerts *config.AgentAlertsConfig
	if cfg != nil {
		alerts = &cfg.Notifications.Agent
	}
	policy := config.ResolveAgentAlerts(alerts)
	if !policy.Enabled || !policy.PlaysAudio() {
		return nil
	}
	return []string{"[notifications.agent] sound: the cue plays on the machine that runs " +
		"the SSH server, not where you are. Set sound_mode = \"bell\" to ring the " +
		"client terminal instead."}
}

func remoteDockComponentNotice(cfg *config.UserConfig, host string) []string {
	if cfg == nil || len(cfg.Dock.Custom) == 0 {
		return nil
	}
	placed := 0
	for _, side := range []string{"left", "center", "right"} {
		for _, name := range cfg.Dock.DockList(side) {
			if len(name) > len(config.DockCustomPrefix) && name[:len(config.DockCustomPrefix)] == config.DockCustomPrefix {
				placed++
			}
		}
	}
	if placed == 0 {
		return nil
	}
	return []string{"[dock.custom] " + host + ": a component's command runs on the machine " +
		"hosting this session, not on the machine you are viewing it from, and every " +
		"attached client runs its own copy"}
}
