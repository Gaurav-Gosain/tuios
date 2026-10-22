package session

import (
	"encoding/json"
	"log"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
)

// The first-run approval of a native plugin.
//
// A native plugin runs only when this machine holds the person's approval of
// its trust hash (herdrplugin/approval.go). One that arrives without it, by
// dotfiles, by plugins link or in a plugin folder, is listed and does not
// run. The daemon tells each attached client of this machine about it with
// MsgPluginApprovals, and the client asks the person in a dialog: the
// plugin, where it comes from, what runs on its own, and its grants in
// plain words. The answer comes back as plugin.approve on the herdr socket,
// which only the person may call (herdrPluginTrust), so a pane or a plugin
// cannot answer for itself. A plugin that asks for admin also needs the
// person to type admin.
//
// The answer names the trust hash the person saw. A plugin that changed
// after the dialog opened has another hash, and the answer is refused. The
// dialog then asks again about what the plugin is now.

// PluginApprovalAsk is one native plugin that waits for the person's answer
// on this machine.
type PluginApprovalAsk struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// Source is where it comes from: a repository and commit, or a folder.
	Source string `json:"source"`
	// Trust is the trust hash the answer must name.
	Trust string `json:"trust"`
	// Grants are the grants it asks for.
	Grants []string `json:"grants"`
	// Runs is what runs on its own once it is allowed
	// (herdrplugin.RunsLines).
	Runs []string `json:"runs"`
}

// PluginApprovalsPayload is the body of MsgPluginApprovals: every native
// plugin that waits, in id order. An empty list closes the dialog.
type PluginApprovalsPayload struct {
	Asks []PluginApprovalAsk `json:"asks"`
}

// pluginAsksOf lists the enabled native plugins that wait for the person.
// One the person denied waits for nobody: tuios plugins approve allows it.
func pluginAsksOf(entries []herdrplugin.Entry) []PluginApprovalAsk {
	var out []PluginApprovalAsk
	for _, e := range entries {
		p := e.Plugin
		if !e.Enabled || p == nil || p.Format != herdrplugin.FormatTuios || p.Approval != herdrplugin.ApprovalWaiting {
			continue
		}
		if e.Err != nil && e.Err.Code != "plugin_needs_approval" && e.Err.Code != "plugin_admin_not_approved" {
			// A shadowed copy, or one that does not run for another
			// reason: an answer would not make it run.
			continue
		}
		source := "folder " + p.PluginRoot
		if e.Lock != nil && !e.Lock.Local() {
			source = strings.TrimSuffix(strings.TrimPrefix(e.Lock.Source, "https://"), ".git")
			if c := e.Lock.Commit; c != "" {
				source += " @ " + c[:min(12, len(c))]
			}
		}
		out = append(out, PluginApprovalAsk{
			ID: p.PluginID, Name: p.Name, Version: p.Version, Source: source, Trust: p.Trust,
			Grants: slices.Clone(p.Grants), Runs: herdrplugin.RunsLines(p),
		})
	}
	return out
}

// samePluginAsks reports whether two lists ask the same questions.
func samePluginAsks(a, b []PluginApprovalAsk) bool {
	return slices.EqualFunc(a, b, func(x, y PluginApprovalAsk) bool { return x.ID == y.ID && x.Trust == y.Trust })
}

// notePluginAsks keeps the plugins that wait, and tells the attached clients
// when the list changed.
func (d *Daemon) notePluginAsks(asks []PluginApprovalAsk) {
	d.pluginAsksMu.Lock()
	if samePluginAsks(d.pluginAsks, asks) {
		d.pluginAsksMu.Unlock()
		return
	}
	d.pluginAsks = asks
	d.pluginAsksMu.Unlock()
	for _, a := range asks {
		log.Printf("[PLUGINS] %s waits for your approval. It does not run until you allow it", a.ID)
	}
	msg, err := NewMessage(MsgPluginApprovals, &PluginApprovalsPayload{Asks: asks})
	if err != nil {
		return
	}
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		if pluginAsksReader(cs) {
			d.queueBroadcast(cs, msg, "notePluginAsks")
		}
	}
}

// pluginAsksReader reports whether cs is an attached client of this machine
// that asks the person about plugins.
func pluginAsksReader(cs *connState) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.isTUIClient && cs.attached && cs.pluginAsks && !cs.viaLink && !cs.passive
}

// sendPluginAsks sends a client that just attached the plugins that wait.
func (d *Daemon) sendPluginAsks(cs *connState) {
	if d.plugins != nil {
		// A manifest can change on disk with nothing else to tell the
		// clients, so an attach reads the plugins again. It reads files
		// only, once per attach.
		d.notePluginAsks(pluginAsksOf(d.plugins.entries(true)))
	}
	d.pluginAsksMu.Lock()
	asks := d.pluginAsks
	d.pluginAsksMu.Unlock()
	if len(asks) == 0 || !pluginAsksReader(cs) {
		return
	}
	_ = d.sendMessage(cs, MsgPluginApprovals, &PluginApprovalsPayload{Asks: asks})
}

// herdrPluginApprove is plugin.approve: the person's answer for one native
// plugin at the trust hash they saw.
func (d *Daemon) herdrPluginApprove(cs *connState, params json.RawMessage) (any, *herdrError) {
	if herr := d.herdrPluginTrust(cs, "plugin.approve"); herr != nil {
		return nil, herr
	}
	var in struct {
		PluginID string `json:"plugin_id"`
		Trust    string `json:"trust"`
		Allow    bool   `json:"allow"`
		Admin    bool   `json:"admin"`
	}
	if herr := herdrDecode(params, &in, "plugin_id", "trust", "allow"); herr != nil {
		return nil, herr
	}
	id, herr := pluginIDParam(in.PluginID)
	if herr != nil || id == "" {
		return nil, cmpHerr(herr, herdrErr("invalid_plugin_id", "invalid plugin id"))
	}
	e := herdrplugin.Find(d.plugins.entries(true), id)
	switch {
	case e == nil:
		return nil, herdrErr("plugin_not_found", "plugin not found")
	case e.Plugin == nil || e.Plugin.Format != herdrplugin.FormatTuios:
		return nil, herdrErr("plugin_not_native", "plugin "+id+" is not a tuios plugin. Only a tuios plugin asks for approval")
	case e.Plugin.Trust != in.Trust:
		return nil, herdrErr("plugin_changed", "plugin "+id+" changed after you were asked. Nothing was changed. Look at it again before you answer")
	case in.Allow && slices.Contains(e.Plugin.Grants, "admin") && !in.Admin:
		return nil, herdrErr("plugin_admin_not_typed", "plugin "+id+" asks for the admin grant. Type admin to give it, or deny the plugin")
	}
	path := d.plugins.pluginDirs().ApprovalsPath()
	var err error
	if in.Allow {
		err = herdrplugin.Approve(path, e.Plugin, in.Trust, in.Admin)
	} else {
		err = herdrplugin.Deny(path, e.Plugin, in.Trust)
	}
	if err != nil {
		return nil, herdrErr("plugin_registry_save_failed", err.Error())
	}
	if in.Allow {
		log.Printf("[PLUGINS] You allowed %s. It runs now", id)
	} else {
		log.Printf("[PLUGINS] You denied %s. It stays off until you allow it", id)
		d.plugins.stopPlugin(id)
	}
	d.plugins.refresh()
	e = herdrplugin.Find(d.plugins.entries(false), id)
	if e == nil {
		return nil, herdrErr("plugin_not_found", "plugin not found")
	}
	return map[string]any{"type": "plugin_approved", "plugin": pluginRecord(*e)}, nil
}
