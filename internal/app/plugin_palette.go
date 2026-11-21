package app

import (
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/herdrcli"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const paletteCategoryPlugins = "Plugins"

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
		return errfmt.Errorf("%v", e["message"])
	}
	return nil
}

var errPluginsNeedDaemon = errors.New("plugins run in a daemon session on this machine")

func (m *OS) runPluginActionKey(c config.CommandBinding) tea.Cmd {
	label := c.Label()
	if !m.IsDaemonSession || m.AttachedHost != "" {
		return func() tea.Msg { return CommandRanMsg{Label: label, Err: errPluginsNeedDaemon} }
	}
	ctx := map[string]any{"invocation_source": "keybinding"}
	if w := m.GetFocusedWindow(); w != nil {
		ctx["focused_pane_id"] = w.ID
	}
	params := map[string]any{"action_id": strings.TrimSpace(c.Command), "context": ctx}
	return func() tea.Msg { return CommandRanMsg{Label: label, Err: pluginCaller("plugin.action.invoke", params)} }
}

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
