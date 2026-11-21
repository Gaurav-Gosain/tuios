package session

import (
	"encoding/json"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

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

func (d *Daemon) nativePluginsRun() bool {
	return d.plugins != nil && d.plugins.runner.NativeRunning()
}

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

var pluginUIVerbs = map[string]bool{
	"plugin-cell": true, "plugin-notify": true, "prompt": true,
	"tml-open": true, "tml-close": true, "tml-patch": true, "tml-replace": true,
}

func (d *Daemon) checkPluginGrants(cs *connState, verb string, pa *paneAuth, params json.RawMessage, deny func(string) *verbError) (json.RawMessage, *verbError) {
	if verb == "respond" {
		return nil, deny("a plugin cannot answer a prompt. Only the person or a pane given respond does that")
	}
	if verb == "queue-prompt" || verb == "send-review" {
		return nil, deny(verb + " queues for later, and a plugin cannot queue. Use send-text or ask-agent")
	}
	if pluginUIVerbs[verb] && pa.grants.Has(GrantUI) {
		return params, nil
	}
	if verb == "plugin-cell" || verb == "plugin-notify" || verb == "prompt" {
		return nil, deny(verb + " needs the ui grant")
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
		case scopeTask:
			need, name = GrantTask, "task"
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
