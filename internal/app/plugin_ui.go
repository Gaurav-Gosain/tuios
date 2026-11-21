package app

import (
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/dockengine"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// PluginUIMsg is the daemon's push of a plugin's cells or notice.
type PluginUIMsg struct {
	Payload session.PluginUIPayload
}

type pluginCellsMsg struct {
	Payload session.PluginUIPayload
}

func (m *OS) queuePluginCells(p session.PluginUIPayload) bool {
	if m.pluginCellsIn == nil {
		return false
	}
	for {
		select {
		case m.pluginCellsIn <- p:
			return true
		default:
		}
		select {
		case <-m.pluginCellsIn:
		default:
		}
	}
}

func (m *OS) listenPluginCells() tea.Cmd {
	return listenOnce(m.stopSignal(), m.pluginCellsIn, func(p session.PluginUIPayload) tea.Msg {
		return pluginCellsMsg{Payload: p}
	})
}

func pluginCellName(c session.PluginCell) string {
	return config.DockPluginPrefix + c.Plugin + "/" + c.ID
}

func (m *OS) rebuildDockPlan() {
	m.dockPlan = buildDockPlan(m.UserConfig)
	for _, name := range sortedPluginCells(m.pluginCells) {
		if m.dockPlan.placed[name] {
			continue
		}
		m.dockPlan.placed[name] = true
		switch m.pluginCells[name].Side {
		case "left":
			m.dockPlan.Left = append(m.dockPlan.Left, name)
		case "center":
			m.dockPlan.Center = append(m.dockPlan.Center, name)
		default:
			m.dockPlan.Right = append([]string{name}, m.dockPlan.Right...)
		}
	}
}

func (m *OS) handlePluginUI(p session.PluginUIPayload) {
	m.MarkAllDirty()
	if p.Notice != "" {
		m.ShowNotification(p.Notice, p.Level, m.Settings.NotificationDuration)
		return
	}
	cells := make(map[string]session.PluginCell, len(p.Cells))
	for _, c := range p.Cells {
		c.Text = dockengine.Sanitize(c.Text)
		cells[pluginCellName(c)] = c
	}
	m.pluginCells = cells
	m.rebuildDockPlan()
}

func (m *OS) pluginCellDrawn(name string) string {
	c, ok := m.pluginCells[name]
	if !ok || c.Text == "" {
		return ""
	}
	text := dockengine.TruncateCell(c.Text, config.DockCustomDefaultMaxWidth)
	return renderDockCell(text, config.DockCellStyle{}, m.groundUI())
}

func (m *OS) runPluginCellClick(name string) bool {
	c, ok := m.pluginCells[name]
	if !ok || c.Action == "" {
		return false
	}
	ctx := map[string]any{"invocation_source": "dock"}
	if w := m.GetFocusedWindow(); w != nil {
		ctx["focused_pane_id"] = w.ID
	}
	params := map[string]any{"plugin_id": c.Plugin, "action_id": c.Action, "context": ctx}
	go func() {
		if err := pluginCaller("plugin.action.invoke", params); err != nil {
			clientLog("the action of cell %s failed: %v", name, err)
		}
	}()
	return true
}

func sortedPluginCells(cells map[string]session.PluginCell) []string {
	out := make([]string, 0, len(cells))
	for name := range cells {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}
