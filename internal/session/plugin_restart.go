package session

import (
	"encoding/json"
	"log"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
)

// herdrPluginRestart is plugin.restart, which tuios plugins install calls
// after it switched a plugin to a new build folder. It stops the plugin's
// processes and runs its startup again from the folder the plugin is found
// in now. It changes no config, so a restart that fails or is killed leaves
// the plugin enabled, and the next daemon starts it from that folder. It is
// refused for a plugin that is not enabled, and from a pane.
//
// With unless_root, the restart is skipped when the plugin's startup already
// ran from that folder. The answer lists the folders that open panes of the
// plugin run from, so the caller keeps them.
func (d *Daemon) herdrPluginRestart(cs *connState, params json.RawMessage) (any, *herdrError) {
	if herr := d.herdrPluginTrust(cs, "plugin.restart"); herr != nil {
		return nil, herr
	}
	var in struct {
		PluginID   string `json:"plugin_id"`
		UnlessRoot string `json:"unless_root"`
	}
	if herr := herdrDecode(params, &in, "plugin_id"); herr != nil {
		return nil, herr
	}
	e, herr := d.herdrPluginEntry(in.PluginID)
	if herr != nil {
		return nil, herr
	}
	h := d.plugins
	h.mu.Lock()
	ran := h.roots[e.ID]
	h.mu.Unlock()
	restarted := in.UnlessRoot == "" || ran == "" || !herdrplugin.SamePath(ran, in.UnlessRoot)
	if restarted {
		log.Printf("[PLUGINS] %s restarts from %s", e.ID, e.Plugin.PluginRoot)
		h.runner.StopPlugin(e.ID)
		h.mu.Lock()
		delete(h.startedUp, e.ID)
		running := h.running
		h.mu.Unlock()
		if running {
			h.runStartups()
		}
	}
	return map[string]any{"type": "plugin_restarted", "restarted": restarted, "pane_roots": d.pluginPaneRoots(e.ID)}, nil
}

// pluginPaneRoots are the folders the open panes of plugin id were opened
// from.
func (d *Daemon) pluginPaneRoots(id string) []string {
	h := d.plugins
	h.mu.Lock()
	panes := make(map[string]string, len(h.panes))
	for w, p := range h.panes {
		if p.pluginID == id {
			panes[w] = p.root
		}
	}
	h.mu.Unlock()
	out := []string{}
	for w, root := range panes {
		if root != "" && d.sessionHoldingWindow(w) != nil {
			out = append(out, root)
		}
	}
	return out
}
