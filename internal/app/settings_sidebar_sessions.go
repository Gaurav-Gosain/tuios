package app

import (
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// editableSidebarEdge creates a file-backed edge table without changing the
// original rail's shared appearance options or any other edge's assignment.
func (m *OS) editableSidebarEdge(edge sidebarEdge) *config.SidebarEdgeConfig {
	if m.UserConfig == nil {
		m.UserConfig = config.DefaultConfig()
	}
	if edge == sidebarLeft {
		if m.UserConfig.Appearance.Sidebar.Left == nil {
			m.UserConfig.Appearance.Sidebar.Left = &config.SidebarEdgeConfig{}
		}
		return m.UserConfig.Appearance.Sidebar.Left
	}
	if m.UserConfig.Appearance.Sidebar.Right == nil {
		m.UserConfig.Appearance.Sidebar.Right = &config.SidebarEdgeConfig{}
	}
	return m.UserConfig.Appearance.Sidebar.Right
}

func (m *OS) sidebarEdgeEnabled(edge sidebarEdge) bool {
	if m.UserConfig == nil {
		return false
	}
	left, right := config.ResolveSidebarEdges(m.UserConfig.Appearance.Sidebar)
	if edge == sidebarLeft {
		return left.Enabled
	}
	return right.Enabled
}

func (m *OS) sidebarSetEdgeEnabled(edge sidebarEdge, enabled bool) {
	cfg := m.editableSidebarEdge(edge)
	cfg.Enabled = &enabled
	if edge == m.legacySidebarEdge() {
		m.UserConfig.Appearance.Sidebar.Enabled = &enabled
		m.Settings.SidebarEnabled = enabled
	}
	m.MarkAllDirty()
}

func (m *OS) sidebarEdgePreferredWidth(edge sidebarEdge) int {
	if edge == m.legacySidebarEdge() {
		return m.sidebarWidthPreference()
	}
	if cfg := m.sidebarSessionConfig(edge); cfg != nil && cfg.Width > 0 {
		return cfg.Width
	}
	return config.SidebarDefaultWidth
}

func (m *OS) sidebarSetEdgeWidth(edge sidebarEdge, width int) {
	cfg := m.editableSidebarEdge(edge)
	cfg.Width = width
	if edge == m.legacySidebarEdge() {
		m.UserConfig.Appearance.Sidebar.Width = width
		m.Settings.SidebarWidth = width
		m.SidebarWidthPref = width
	}
	m.MarkAllDirty()
}

func (m *OS) sidebarSessionSettingItems() []settingItem {
	var rows []settingItem
	workspaces := []string{"all"}
	for slot := 1; slot <= 9; slot++ {
		workspaces = append(workspaces, strconv.Itoa(m.workspaceDisplayNumber(slot)))
	}
	for _, side := range []struct {
		edge sidebarEdge
		key  string
	}{
		{sidebarLeft, "left"}, {sidebarRight, "right"},
	} {
		edge, key := side.edge, side.key
		loc := m.loc()
		rows = append(rows,
			settingItem{
				Label: loc.Row("sidebar." + key + "_rail.label"), Desc: loc.Row("sidebar.edge_rail.desc"),
				Control: controlBool,
				boolVal: func(m *OS) bool { return m.sidebarEdgeEnabled(edge) },
				adjust:  func(m *OS, _ int) { m.sidebarSetEdgeEnabled(edge, !m.sidebarEdgeEnabled(edge)) },
			},
			settingItem{
				Label: loc.Row("sidebar." + key + "_width.label"), Desc: loc.Row("sidebar.edge_width.desc"),
				Control: controlInt, numMin: 10, numMax: 80,
				value: func(m *OS) string { return strconv.Itoa(m.sidebarEdgePreferredWidth(edge)) },
				adjust: func(m *OS, dir int) {
					m.sidebarSetEdgeWidth(edge, clampInt(m.sidebarEdgePreferredWidth(edge)+dir, 10, 80))
				},
				setNum: func(m *OS, width int) { m.sidebarSetEdgeWidth(edge, width) },
			},
			settingItem{
				Label: loc.Row("sidebar." + key + "_session.label"), Desc: loc.Row("sidebar.edge_session.desc"),
				Control: controlString, Unset: loc.Row("sidebar.edge_session.unset"), Placeholder: loc.Row("sidebar.edge_session.placeholder"),
				value: func(m *OS) string {
					if cfg := m.sidebarSessionConfig(edge); cfg != nil {
						return cfg.Session
					}
					return ""
				},
				setStr: func(m *OS, value string) {
					m.editableSidebarEdge(edge).Session = value
					if value != "" {
						m.sidebarSetEdgeEnabled(edge, true)
					}
					m.MarkAllDirty()
				},
			},
			settingItem{
				Label: loc.Row("sidebar." + key + "_workspaces.label"), Desc: loc.Row("sidebar.edge_workspaces.desc"),
				Control: controlEnum, Options: workspaces,
				value: func(m *OS) string {
					if cfg := m.sidebarSessionConfig(edge); cfg != nil && cfg.SessionWorkspace > 0 {
						return strconv.Itoa(m.workspaceDisplayNumber(cfg.SessionWorkspace))
					}
					return m.loc().Value("sidebar.all")
				},
				adjust: func(m *OS, dir int) {
					cfg := m.editableSidebarEdge(edge)
					cfg.SessionWorkspace = (cfg.SessionWorkspace + dir + 10) % 10
					m.MarkAllDirty()
				},
			},
		)
	}
	return rows
}
