package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// What a browser tab cannot do, and what tuios says about it.
//
// Everything in [appearance] renders as cells and reaches xterm.js intact, so
// the whole of it applies over the web. Two of the [notifications.agent] sinks
// do not, and both used to fail silently: an agent would finish, the config
// said to say so, and nothing said anything.
//
//   - notify writes OSC 9 in-band (see host_notify.go). sip's terminal
//     registers OSC handlers for 0, 1, 2, 4, 8, 10-12, 52, 104, 110-112 and
//     1337, and none for 9, so the bytes are parsed and discarded. There is no
//     Notification API bridge in its frontend to route them to either.
//   - sound in "audio" mode shells out to paplay/aplay/afplay (internal/sound),
//     which plays on the machine running tuios-web. That is the right machine
//     for a local attach and the wrong one for a phone across the room.
//
// Neither is worth silently rewriting the user's config over, so tuios reports
// them through the config-warning channel that already exists for settings that
// will not do what they say, and skips the OSC 9 write that has nowhere to go.
// The sinks that do work are left alone: the dock message is drawn in the frame,
// and BEL reaches the browser, where sip flashes the terminal's outline.

// browserAlertWarnings names the alert sinks this session's config asks for and
// a browser cannot deliver. It returns nothing when the config asks for neither,
// so a user who never turned them on is not told about them.
func browserAlertWarnings(cfg *config.UserConfig) []string {
	var alerts *config.AgentAlertsConfig
	if cfg != nil {
		alerts = &cfg.Notifications.Agent
	}
	policy := config.ResolveAgentAlerts(alerts)
	if !policy.Enabled {
		return nil
	}

	var out []string
	if policy.Notify {
		out = append(out, "[notifications.agent] notify: a browser terminal has no desktop "+
			"notification, so this is skipped; the dock message still appears")
	}
	if policy.PlaysAudio() {
		out = append(out, "[notifications.agent] sound: the cue plays on the machine running "+
			"tuios-web, not in the browser; sound_mode = \"bell\" flashes the terminal instead")
	}
	return out
}

// remoteDockComponentWarning names where a custom dock component's command
// actually runs when the client is not on the user's own machine.
//
// A component is UI: it is composed in the client, so it runs wherever the
// client runs. For a local session that is the user's machine, which is what
// everyone expects. For tuios-web the client is the server process, and for an
// SSH session it is the SSH host, so a component reading the git branch reports
// the server's checkout and one reading the battery reports the server's
// battery, which servers do not have.
//
// That is the correct behaviour and not a bug to route around: a component
// rendering into the bar has to run where the bar is composed, and the
// alternative is a protocol for executing commands on the viewer's machine,
// which is a much larger thing to own than a dock cell. What was wrong was
// leaving it to be discovered. Every attached client also runs its own copy,
// the way waybar runs one per monitor.
func remoteDockComponentWarning(cfg *config.UserConfig, host string) []string {
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
