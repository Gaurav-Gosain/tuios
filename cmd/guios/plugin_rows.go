package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/herdrcli"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The herdr plugins the tuios config enables, as palette rows (parity gap
// 18c): one per action and one per pane, as the TUI lists them
// (internal/app/plugin_palette.go). The daemon runs them, through its herdr
// socket, which is this machine's: a row is offered for a session of this
// machine only, and the daemon refuses a plugin that is not enabled there.
//
// The rows come from the daemon's own list (plugin.list), read as the
// palette opens, rather than from a discovery of guios's own: the daemon
// has found the plugins already, and reading the manifests here linked
// all of internal/herdrplugin into guios for one list.

// pluginRow is one action or pane of an enabled plugin.
type pluginRow struct {
	plugin, entry, placement, label string
	pane                            bool
}

// pluginList is plugin.list's answer, the fields the rows use.
type pluginList struct {
	Plugins []struct {
		PluginID  string   `json:"plugin_id"`
		Name      string   `json:"name"`
		Enabled   bool     `json:"enabled"`
		Platforms []string `json:"platforms"`
		Warnings  []string `json:"warnings"`
		Actions   []struct {
			ID        string   `json:"id"`
			Title     string   `json:"title"`
			Platforms []string `json:"platforms"`
		} `json:"actions"`
		Panes []struct {
			ID        string   `json:"id"`
			Title     string   `json:"title"`
			Placement string   `json:"placement"`
			Platforms []string `json:"platforms"`
		} `json:"panes"`
	} `json:"plugins"`
}

// thisPlatform is this machine as a herdr manifest names it
// (herdrplugin.CurrentPlatform).
func thisPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	}
	return "linux"
}

// runsHere reports whether an entry's platforms, else its plugin's, take
// this machine, as herdrplugin's Supported does.
func runsHere(item, plugin []string) bool {
	list := item
	if list == nil {
		list = plugin
	}
	return list == nil || slices.Contains(list, thisPlatform())
}

// pluginRowsOf are the rows of plugin.list's answer: the runnable entries
// of the enabled plugins whose manifest loaded.
func pluginRowsOf(l pluginList) []pluginRow {
	var out []pluginRow
	for _, p := range l.Plugins {
		if !p.Enabled || slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.HasPrefix(w, "manifest unavailable") }) {
			continue
		}
		for _, x := range p.Actions {
			if runsHere(x.Platforms, p.Platforms) {
				out = append(out, pluginRow{plugin: p.PluginID, entry: x.ID, label: p.Name + ": " + x.Title})
			}
		}
		for _, x := range p.Panes {
			if runsHere(x.Platforms, p.Platforms) {
				out = append(out, pluginRow{plugin: p.PluginID, entry: x.ID, placement: x.Placement, label: p.Name + ": " + x.Title, pane: true})
			}
		}
	}
	return out
}

// loadPlugins reads the daemon's plugin list for the palette, when the
// config enables any: the rows show once the answer lands.
func (a *App) loadPlugins() {
	if a.tk.reg == nil || a.tk.reg.GetConfig() == nil || len(a.tk.reg.GetConfig().Plugins.Enabled) == 0 {
		a.plugins = nil
		return
	}
	go func() {
		var l pluginList
		if pluginRequest("plugin.list", map[string]any{}, &l) != nil {
			return
		}
		rows := pluginRowsOf(l)
		a.update(func() { a.plugins = rows })
	}()
}

// pluginItems are the rows of the enabled plugins, for session s.
func (a *App) pluginItems(s *proto.Session) []paletteItem {
	if s == nil || a.hostOf(s.ID).remote() {
		return nil
	}
	var out []paletteItem
	for _, r := range a.plugins {
		if r.pane {
			out = append(out, paletteItem{title: r.label, detail: locales.PluginPaneDetail(r.placement), words: "plugin pane " + r.label,
				run: func() { a.openPluginPane(a.session(a.selected), r.plugin, r.entry, r.placement, r.label) }})
			continue
		}
		out = append(out, paletteItem{title: r.label, detail: locales.PluginActionDetail(), words: "plugin " + r.label,
			run: func() { a.invokePluginAction(a.session(a.selected), r.plugin, r.entry, r.label) }})
	}
	return out
}

// pluginRequest sends one herdr method to this machine's daemon and
// decodes its answer into out, when out is not nil.
func pluginRequest(method string, params map[string]any, out any) error {
	sock, err := session.GetSocketPath()
	if err != nil {
		return err
	}
	resp, err := herdrcli.Request(session.HerdrSocketPath(sock), method, params, 30*time.Second)
	if err != nil {
		return errors.New(locales.PluginNoDaemon())
	}
	if e, ok := resp["error"].(map[string]any); ok {
		return fmt.Errorf("%v", e["message"])
	}
	if out == nil {
		return nil
	}
	// The answer is the response line: the result is under result.
	var body any = resp
	if r, ok := resp["result"]; ok {
		body = r
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// pluginCall sends one herdr method to this machine's daemon off the main
// thread; done gets its error on it.
func (a *App) pluginCall(method string, params map[string]any, done func(error)) {
	go func() {
		err := pluginRequest(method, params, nil)
		a.update(func() { done(err) })
	}()
}

// pluginContext is the focused pane of s, for a plugin call's context.
func (a *App) pluginContext(s *proto.Session, source string) map[string]any {
	ctx := map[string]any{"invocation_source": source}
	if s != nil {
		if p := a.focusOf(s); p != "" {
			ctx["focused_pane_id"] = p
		}
	}
	return ctx
}

// invokePluginAction runs a plugin action. pluginID may be empty: the daemon
// then finds the action by its id, bare or qualified, as for a command
// entry of type plugin_action.
func (a *App) invokePluginAction(s *proto.Session, pluginID, action, label string) {
	if s == nil || a.hostOf(s.ID).remote() {
		a.toast(locales.PluginNeedsLocal(label))
		return
	}
	params := map[string]any{"action_id": action, "context": a.pluginContext(s, "palette")}
	if pluginID != "" {
		params["plugin_id"] = pluginID
	}
	a.pluginCall("plugin.action.invoke", params, func(err error) {
		if err != nil {
			a.toast(locales.CommandEntryFailed(label, err.Error()))
		}
	})
}

// openPluginPane opens a plugin's pane, beside the focused one when its
// placement splits or zooms.
func (a *App) openPluginPane(s *proto.Session, pluginID, entry, placement, label string) {
	if s == nil || a.hostOf(s.ID).remote() {
		a.toast(locales.PluginNeedsLocal(label))
		return
	}
	params := map[string]any{"plugin_id": pluginID, "entrypoint": entry, "focus": true}
	if placement == "split" || placement == "zoomed" {
		if p := a.focusOf(s); p != "" {
			params["target_pane_id"] = p
		}
	}
	a.pluginCall("plugin.pane.open", params, func(err error) {
		if err != nil {
			a.toast(locales.CommandEntryFailed(label, err.Error()))
		}
	})
}
