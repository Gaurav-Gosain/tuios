package session

import (
	"encoding/json"
	"log"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
)

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
		h.dropCells(e.ID)
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
