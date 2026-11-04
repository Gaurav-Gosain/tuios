package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A plugin's dock cells and notices (session/plugin_ui.go). The daemon holds
// the cells, and this client draws what it pushed last. A cell draws on the
// side its manifest names unless the person placed plugin/ID/CELL in a list.

// PluginUIMsg is the daemon's push of a plugin's cells or notice.
type PluginUIMsg struct {
	Payload session.PluginUIPayload
}

// pluginCellsMsg is the newest push of the plugins' cells, off pluginCellsIn.
type pluginCellsMsg struct {
	Payload session.PluginUIPayload
}

// queuePluginCells keeps a push of the cells for the Update loop, in place of
// one that waits there still. It reports false when the model has no slot
// for cells, and the push then goes on ClientEventChan. It runs on the
// client's read loop, the one writer.
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

// listenPluginCells arms the listener for the next push of the cells.
func (m *OS) listenPluginCells() tea.Cmd {
	return listenOnce(m.stopSignal(), m.pluginCellsIn, func(p session.PluginUIPayload) tea.Msg {
		return pluginCellsMsg{Payload: p}
	})
}

// pluginCellName is a cell's name in the dock lists.
func pluginCellName(c session.PluginCell) string {
	return config.DockPluginPrefix + c.Plugin + "/" + c.ID
}

// rebuildDockPlan resolves the config's lists, then adds the plugin cells
// the person did not place.
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

// handlePluginUI applies one push. It draws a frame only for what changed.
func (m *OS) handlePluginUI(p session.PluginUIPayload) {
	m.MarkAllDirty()
	if p.Notice != "" {
		m.ShowNotification(p.Notice, p.Level, m.Settings.NotificationDuration)
		return
	}
	cells := make(map[string]session.PluginCell, len(p.Cells))
	for _, c := range p.Cells {
		c.Text = dockSanitize(c.Text)
		cells[pluginCellName(c)] = c
	}
	m.pluginCells = cells
	m.rebuildDockPlan()
}

// pluginCellDrawn is one plugin cell, "" when no plugin set it.
func (m *OS) pluginCellDrawn(name string) string {
	c, ok := m.pluginCells[name]
	if !ok || c.Text == "" {
		return ""
	}
	text := dockTruncateCell(c.Text, config.DockCustomDefaultMaxWidth)
	return renderDockCell(text, config.DockCellStyle{}, m.groundUI())
}

// runPluginCellClick runs the action a cell names. The daemon refuses a
// plugin that is not enabled, as it does for the palette.
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

// sortedPluginCells are the names of the cells in a stable order.
func sortedPluginCells(cells map[string]session.PluginCell) []string {
	out := make([]string, 0, len(cells))
	for name := range cells {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}
