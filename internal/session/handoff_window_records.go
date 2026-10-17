package session

import (
	"slices"
	"sync"
)

// What the daemon keeps about a window outside its session's state, carried
// across a live handoff (docs/design/live-handoff.md, section 7.2): the plugin
// that opened a pane, the protocol a start-agent --protocol pane speaks, the
// pane that opened another one, and the sessions a window was moved out of.
// Each is daemon memory keyed by window id, and each changes what a call
// about the window does, so a window the next image adopts without it would
// answer differently from the moment the image is up.

// handoffWindowRecords fills the daemon record's window maps.
func (d *Daemon) handoffWindowRecords(dr *handoffDaemon) {
	if d.plugins != nil {
		d.plugins.mu.Lock()
		for id, p := range d.plugins.panes {
			if dr.PluginPanes == nil {
				dr.PluginPanes = map[string]handoffPluginPane{}
			}
			dr.PluginPanes[id] = handoffPluginPane{Plugin: p.pluginID, Entrypoint: p.entrypoint}
		}
		d.plugins.mu.Unlock()
	}
	dr.ProtocolPanes = stringSyncMap(&d.protocolPanes)
	dr.PaneCreators = stringSyncMap(&d.paneCreators)
	movedWindows.Range(func(k, v any) bool {
		id, _ := k.(string)
		m, _ := v.(*movedFrom)
		if id == "" || m == nil {
			return true
		}
		m.mu.Lock()
		rec := handoffMovedFrom{IDs: slices.Clone(m.ids), Names: slices.Clone(m.names)}
		m.mu.Unlock()
		if dr.MovedWindows == nil {
			dr.MovedWindows = map[string]handoffMovedFrom{}
		}
		dr.MovedWindows[id] = rec
		return true
	})
}

// restoreWindowRecords puts the daemon record's window maps back, before any
// pane is adopted or any call is served.
func (d *Daemon) restoreWindowRecords(dr handoffDaemon) {
	if d.plugins != nil && len(dr.PluginPanes) > 0 {
		d.plugins.mu.Lock()
		if d.plugins.panes == nil {
			d.plugins.panes = map[string]pluginPane{}
		}
		for id, p := range dr.PluginPanes {
			d.plugins.panes[id] = pluginPane{pluginID: p.Plugin, entrypoint: p.Entrypoint}
		}
		d.plugins.mu.Unlock()
	}
	for id, protocol := range dr.ProtocolPanes {
		d.markProtocolPane(id, protocol)
	}
	for id, creator := range dr.PaneCreators {
		d.paneCreators.Store(id, creator)
	}
	for id, m := range dr.MovedWindows {
		movedWindows.Store(id, &movedFrom{ids: slices.Clone(m.IDs), names: slices.Clone(m.Names)})
	}
}

// stringSyncMap copies a sync.Map of string to string, nil when it is empty.
func stringSyncMap(m *sync.Map) map[string]string {
	var out map[string]string
	m.Range(func(k, v any) bool {
		key, _ := k.(string)
		val, _ := v.(string)
		if key == "" {
			return true
		}
		if out == nil {
			out = map[string]string{}
		}
		out[key] = val
		return true
	})
	return out
}
