package session

import (
	"encoding/json"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// Grants per plugin: what a native plugin's processes may do through tuios.
//
// The runner starts each plugin command in a process session of its own, and
// enters the session in its owner table before the command can connect
// (herdrplugin.Runner.Owner). A caller is a plugin's when the kernel's record
// of its pid is in such a session. The session's leader is the reaper
// (herdrplugin/reaper.go), which stays until nothing the command started is
// left, so a daemon a startup command forks, or a process it leaves behind
// with its environment cleaned, is still the plugin's, and the session id
// cannot name another process while the entry exists. Nothing the process
// sends or sets in its environment changes the answer. A process that
// starts a session of its own leaves the plugin and is placed as any other
// process. This scopes mistakes and tools talked into misuse, not a program
// set on escaping. Where the kernel gives no peer pid (Windows, the BSDs) no
// call is placed in a plugin, and the runner does not start a native
// plugin's commands.
//
// A herdr plugin holds the default grants, as before. A native plugin holds
// the grants the person approved at install (Plugin.Held), and none when it
// was not installed. A plugin is in no session: its grants reach every
// session of this daemon, because it is the person's extension and not an
// agent in a pane. read reads every session, write types into any pane (held
// by the prompt rule, holdTypingTarget), and fan starts agents. Verbs about
// the caller's own pane are refused, since it has none. respond is never
// held. Every plugin is refused acting as the person (connFromPane).

// pluginOfPeer is the plugin whose process session the caller is in, or nil.
func (d *Daemon) pluginOfPeer(cs *connState) *herdrplugin.Plugin {
	if d.plugins == nil || cs == nil || cs.viaLink || cs.peerPID <= 0 {
		return nil
	}
	sid, ok := procinfo.SessionID(cs.peerPID)
	if !ok {
		return nil
	}
	return d.plugins.runner.Owner(sid)
}

// nativePluginsRun reports whether a native plugin may have a process
// running, so a call has to be placed before it is let through.
func (d *Daemon) nativePluginsRun() bool {
	return d.plugins != nil && d.plugins.runner.NativeRunning()
}

// pluginAuthority places a caller in a native plugin and says what it
// holds, or returns nil for any other caller.
//
// The kernel's answer comes first. A caller the kernel places in no plugin
// is placed by the plugin token it presented (plugin_token.go), which only
// narrows: such a caller would otherwise be held to nothing new.
func (d *Daemon) pluginAuthority(cs *connState) *paneAuth {
	bound := cs.pluginBound.Load()
	if bound == nil && !d.nativePluginsRun() {
		return nil
	}
	if p := d.pluginOfPeer(cs); p != nil {
		if p.Format != herdrplugin.FormatTuios {
			return nil
		}
		return &paneAuth{window: unplacedWindow, via: "plugin", plugin: p.PluginID, grants: grantsFromNames(p.Held)}
	}
	if bound == nil {
		return nil
	}
	return &paneAuth{window: unplacedWindow, via: "plugin-token", plugin: *bound, grants: d.boundPluginGrants(*bound)}
}

// checkPluginGrants holds a call from a native plugin to its grants, across
// every session.
func (d *Daemon) checkPluginGrants(cs *connState, verb string, pa *paneAuth, params json.RawMessage, deny func(string) *verbError) (json.RawMessage, *verbError) {
	if verb == "respond" {
		return nil, deny("a plugin cannot answer a prompt. Only the person or a pane given respond does that")
	}
	if verb == "queue-prompt" || verb == "send-review" {
		// The queue types later, as the pane that queued, and a plugin has
		// no pane: delivery would drop the entry. Refused, so the plugin
		// is told now.
		return nil, deny(verb + " queues for later, and a plugin cannot queue. Use send-text or ask-agent")
	}
	if !pa.grants.Has(GrantAdmin) {
		var need Grants
		name := ""
		switch grantKind(verb) {
		case scopeOpen:
			return params, nil
		case scopeGlobal, scopeRead, scopeBufferRead:
			need, name = GrantRead, "read"
		case scopeMail, scopeWrite, scopeBufferWrite:
			need, name = GrantWrite, "write"
		case scopeLaunch:
			need, name = GrantFan, "fan"
		case scopeSelf:
			return nil, deny(verb + " reports for the caller's own pane, and a plugin has no pane")
		default:
			return nil, deny(verb + " needs the admin grant")
		}
		if !pa.grants.Has(need) {
			return nil, deny(verb + " needs the " + name + " grant")
		}
		if readsBuffers(verb) && !pa.grants.Has(GrantRead) {
			return nil, deny(verb + " reads the paste buffers, which needs the read grant")
		}
	}
	if typingVerbs[verb] {
		return d.holdTypingTarget(cs, verb, pa, params, deny)
	}
	return params, nil
}
