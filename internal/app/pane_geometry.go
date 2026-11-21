package app

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// SetSharedBordersSetting is the one way a user-facing control changes shared
// borders. It writes the model (the value layout reads), mirrors the choice
// into this client's config as its preference for future sessions, and lands
// the change: retile, repaint, and a state push so the session's other clients
// adopt it.
func (m *OS) SetSharedBordersSetting(v bool) {
	if m.OwnPaneBorders {
		m.SessionSharedBorders = v
	} else {
		m.SharedBorders = v
	}
	m.Settings.SharedBorders = v
	m.lastConfigSharedBorders = v
	m.setAppearance(func(a *config.AppearanceConfig) { a.SharedBorders = new(v) })
	m.applyAppearanceLive(true)
}

// ToggleSharedBorders turns shared borders on or off, says so, and saves the
// choice. It is the toggle_shared_borders action.
func (m *OS) ToggleSharedBorders() tea.Cmd {
	m.SetSharedBordersSetting(!m.SharedBorders)
	save := m.persistSettings()
	if m.SharedBorders {
		m.ShowNotification("Shared borders on", "success", m.Settings.NotificationDuration)
	} else {
		m.ShowNotification("Shared borders off", "info", m.Settings.NotificationDuration)
	}
	return save
}

// SetPaneGapSetting is SetSharedBordersSetting for the pane gap.
func (m *OS) SetPaneGapSetting(v int) {
	v = clampInt(v, 0, config.PaneGapMax)
	m.PaneGap = v
	m.Settings.PaneGap = v
	m.lastConfigPaneGap = v
	m.setAppearance(func(a *config.AppearanceConfig) { a.Gap = v })
	m.applyAppearanceLive(true)
}

// SetMasterRatioSetting is SetSharedBordersSetting for the master pane's share
// of the screen, taken as a percent. The model keeps the fraction the tilers
// want; the config and the settings row speak percent, which is what a person
// types and what an int option can carry.
//
// The master ratio was already session state, moved by the resize keys and
// carried in SessionState. What it had no way to be was a preference: every
// session started at 50 and the only way to change that was to press the resize
// key again on every one of them.
func (m *OS) SetMasterRatioSetting(percent int) {
	percent = clampInt(percent, config.MasterRatioMin, config.MasterRatioMax)
	m.MasterRatio = float64(percent) / 100
	m.Settings.MasterRatioPercent = percent
	m.setAppearance(func(a *config.AppearanceConfig) { a.MasterRatio = percent })
	m.applyAppearanceLive(true)
}

// SetScrollColumnWidthSetting is SetSharedBordersSetting for a scrolling
// column's width, as a percent of the screen.
func (m *OS) SetScrollColumnWidthSetting(percent int) {
	percent = clampInt(percent, config.ScrollColumnWidthMin, m.scrollColumnMax())
	m.ScrollColumnWidth = percent
	m.Settings.ScrollColumnWidth = percent
	m.lastConfigScrollWidth = percent
	m.setAppearance(func(a *config.AppearanceConfig) { a.ScrollColumnWidth = percent })
	m.applyAppearanceLive(true)
}

// ScrollColumnWidthFraction is the session's column width as the proportion the
// strip resolves against, with the config's value standing in for a model built
// without one (the tape and fuzz entrypoints construct an OS by hand).
func (m *OS) ScrollColumnWidthFraction() float64 {
	w := m.ScrollColumnWidth
	if w == 0 {
		w = m.Settings.ScrollColumnWidth
	}
	return float64(clampInt(w, config.ScrollColumnWidthMin, m.scrollColumnMax())) / 100
}

func (m *OS) scrollColumnMax() int { return m.Settings.GetScrollColumnMax() }

// MasterRatioPercent is the session's master ratio as the settings row shows
// it. Rounded rather than truncated, so a ratio the resize keys nudged to 0.55
// reads back as 55 and not 54.
func (m *OS) MasterRatioPercent() int {
	return clampInt(int(m.MasterRatio*100+0.5), config.MasterRatioMin, config.MasterRatioMax)
}

func (m *OS) adoptPaneGeometry(state *session.SessionState) bool {
	if state == nil || state.PaneGeometry == nil {
		return false
	}
	pg := state.PaneGeometry
	shared := pg.SharedBorders
	if m.OwnPaneBorders {
		m.SessionSharedBorders, shared = pg.SharedBorders, true
	}
	changed := m.SharedBorders != shared || m.PaneGap != pg.PaneGap
	m.SharedBorders = shared
	m.PaneGap = pg.PaneGap
	if pg.ScrollColumnWidth != 0 && pg.ScrollColumnWidth != m.ScrollColumnWidth {
		m.ScrollColumnWidth = pg.ScrollColumnWidth
		changed = true
	}
	return changed
}

func (m *OS) sharedBordersItem() settingItem {
	item := boolItem(
		settingLabel("appearance.shared_borders"),
		registryDescription("appearance.shared_borders"),
		func() bool { return m.SharedBorders },
		func(m *OS, v bool) {
			m.SetSharedBordersSetting(v)
		},
	)
	item.differs = differsFromDefault("appearance.shared_borders", func(m *OS) string { return strconv.FormatBool(m.SharedBorders) })
	return item
}

func (m *OS) paneGapItem() settingItem {
	return settingItem{
		Label:   settingLabel("appearance.gap"),
		Desc:    registryDescription("appearance.gap"),
		Control: controlInt,
		value:   func(m *OS) string { return strconv.Itoa(m.PaneGap) },
		adjust: func(m *OS, dir int) {
			m.SetPaneGapSetting(m.PaneGap + dir)
		},
		setNum:  func(m *OS, v int) { m.SetPaneGapSetting(v) },
		numMin:  0,
		numMax:  config.PaneGapMax,
		differs: differsFromDefault("appearance.gap", func(m *OS) string { return strconv.Itoa(m.PaneGap) }),
	}
}

func (m *OS) masterRatioItem() settingItem {
	return percentItem("appearance.master_ratio",
		config.MasterRatioMin, config.MasterRatioMax,
		(*OS).MasterRatioPercent,
		(*OS).SetMasterRatioSetting)
}

func (m *OS) scrollColumnWidthItem() settingItem {
	return percentItem("appearance.scroll_column_width",
		config.ScrollColumnWidthMin, m.scrollColumnMax(),
		func(m *OS) int {
			return clampInt(m.ScrollColumnWidth, config.ScrollColumnWidthMin, m.scrollColumnMax())
		},
		(*OS).SetScrollColumnWidthSetting)
}

func percentItem(path string, lo, hi int, get func(*OS) int, set func(*OS, int)) settingItem {
	return settingItem{
		Label:   settingLabel(path),
		Desc:    registryDescription(path),
		Control: controlInt,
		value:   func(m *OS) string { return strconv.Itoa(get(m)) + "%" },
		adjust:  func(m *OS, dir int) { set(m, get(m)+dir) },
		setNum:  func(m *OS, v int) { set(m, v) },
		numMin:  lo,
		numMax:  hi,
		meter: func(m *OS) float64 {
			if hi <= lo {
				return 0
			}
			return float64(clampInt(get(m), lo, hi)-lo) / float64(hi-lo)
		},
		differs: differsFromDefault(path, func(m *OS) string { return strconv.Itoa(get(m)) }),
	}
}

func registryDescription(path string) string {
	if o, ok := config.LookupOption(path); ok {
		return o.Description
	}
	return ""
}
