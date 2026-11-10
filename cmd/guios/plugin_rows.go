package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/herdrcli"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The herdr plugins the tuios config enables, as palette rows (parity gap
// 18c): one per action and one per pane, as the TUI lists them
// (internal/app/plugin_palette.go). The daemon runs them, through its herdr
// socket, which is this machine's: a row is offered for a session of this
// machine only, and the daemon refuses a plugin that is not enabled there.

// pluginItems are the rows of the enabled plugins, for session s.
func (a *App) pluginItems(s *proto.Session) []paletteItem {
	if s == nil || a.hostOf(s.ID).remote() || a.tk.reg == nil || a.tk.reg.GetConfig() == nil {
		return nil
	}
	cfg := a.tk.reg.GetConfig().Plugins
	if len(cfg.Enabled) == 0 {
		return nil
	}
	path, err := config.GetConfigPath()
	if err != nil {
		return nil
	}
	var out []paletteItem
	for _, e := range herdrplugin.Discover(herdrplugin.DefaultDirs(path, cfg.Dirs), cfg.Enabled) {
		if !e.Runnable() {
			continue
		}
		p := e.Plugin
		for _, act := range p.Actions {
			if p.Supported(act.Platforms, "") != nil {
				continue
			}
			id, action, label := p.PluginID, act.ID, p.Name+": "+act.Title
			out = append(out, paletteItem{title: label, detail: locales.PluginActionDetail(), words: "plugin " + label,
				run: func() { a.invokePluginAction(a.session(a.selected), id, action, label) }})
		}
		for _, x := range p.Panes {
			if p.Supported(x.Platforms, "") != nil {
				continue
			}
			id, entry, placement, label := p.PluginID, x.ID, x.Placement, p.Name+": "+x.Title
			out = append(out, paletteItem{title: label, detail: locales.PluginPaneDetail(placement), words: "plugin pane " + label,
				run: func() { a.openPluginPane(a.session(a.selected), id, entry, placement, label) }})
		}
	}
	return out
}

// pluginCall sends one herdr method to this machine's daemon and returns
// its error, off the main thread; done runs on it.
func (a *App) pluginCall(method string, params map[string]any, done func(error)) {
	go func() {
		err := func() error {
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
			return nil
		}()
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
	if placement == herdrplugin.PlacementSplit || placement == herdrplugin.PlacementZoomed {
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
