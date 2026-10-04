package app

import (
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/herdrcli"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Palette rows for the herdr plugins config.toml enables: one per action
// and one per pane. The daemon runs them (internal/session/plugin_host.go),
// so a row is offered only in a daemon session on this machine, and the
// daemon refuses a plugin that is not enabled there.

// paletteCategoryPlugins is the palette category of the plugin rows.
const paletteCategoryPlugins = "Plugins"

// pluginCaller sends one plugin method to the daemon. Tests replace it.
var pluginCaller = callPluginMethod

func callPluginMethod(method string, params map[string]any) error {
	sock, err := session.GetSocketPath()
	if err != nil {
		return err
	}
	resp, err := herdrcli.Request(session.HerdrSocketPath(sock), method, params, 30*time.Second)
	if err != nil {
		return errors.New("the daemon cannot be reached")
	}
	if e, ok := resp["error"].(map[string]any); ok {
		return fmt.Errorf("%v", e["message"])
	}
	return nil
}

// pluginPaletteItems are the rows of the enabled plugins.
func (m *OS) pluginPaletteItems() []CommandPaletteItem {
	if m.UserConfig == nil || len(m.UserConfig.Plugins.Enabled) == 0 || !m.IsDaemonSession || m.AttachedHost != "" {
		return nil
	}
	path, err := config.GetConfigPath()
	if err != nil {
		return nil
	}
	cfg := m.UserConfig.Plugins
	var items []CommandPaletteItem
	for _, e := range herdrplugin.Discover(herdrplugin.DefaultDirs(path, cfg.Dirs), cfg.Enabled) {
		if !e.Runnable() {
			continue
		}
		p := e.Plugin
		for _, a := range p.Actions {
			if p.Supported(a.Platforms, "") != nil {
				continue
			}
			id, action, label := p.PluginID, a.ID, p.Name+": "+a.Title
			items = append(items, CommandPaletteItem{
				Name: label, Category: paletteCategoryPlugins, Shortcut: "plugin action",
				Action: func(m *OS) (*OS, tea.Cmd) {
					ctx := map[string]any{"invocation_source": "palette"}
					if w := m.GetFocusedWindow(); w != nil {
						ctx["focused_pane_id"] = w.ID
					}
					params := map[string]any{"plugin_id": id, "action_id": action, "context": ctx}
					return m, func() tea.Msg { return CommandRanMsg{Label: label, Err: pluginCaller("plugin.action.invoke", params)} }
				},
			})
		}
		for _, x := range p.Panes {
			if p.Supported(x.Platforms, "") != nil {
				continue
			}
			id, entry, placement, label := p.PluginID, x.ID, x.Placement, p.Name+": "+x.Title
			items = append(items, CommandPaletteItem{
				Name: label, Category: paletteCategoryPlugins, Shortcut: "plugin " + placement,
				Action: func(m *OS) (*OS, tea.Cmd) {
					params := map[string]any{"plugin_id": id, "entrypoint": entry, "focus": true}
					if w := m.GetFocusedWindow(); w != nil && (placement == herdrplugin.PlacementSplit || placement == herdrplugin.PlacementZoomed) {
						params["target_pane_id"] = w.ID
					}
					return m, func() tea.Msg { return CommandRanMsg{Label: label, Err: pluginCaller("plugin.pane.open", params)} }
				},
			})
		}
	}
	return items
}
