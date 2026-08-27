package app

import (
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The pane geometry inputs - shared borders and the pane gap - are session
// state, because they are arithmetic and not appearance: they decide where the
// rectangles inside the panes' box fall and how much of each rectangle a guest
// may draw in. A PTY has exactly one size, so every client of a session has to
// run this arithmetic on identical inputs; two clients whose config files
// disagreed here computed different rectangles for the same panes and dragged
// the shared PTYs between the two answers on every push.
//
// The line is drawn on purpose and the rest of appearance sits on the other
// side of it: theme, colours, glyphs, border style, title position and dimming
// change what cells look like, never how many cells a pane gets, so they stay
// per-client and riceable. Only what moves a rectangle is settled session-wide.

// SetSharedBordersSetting is the one way a user-facing control changes shared
// borders. It writes the model (the value layout reads), mirrors the choice
// into this client's config as its preference for future sessions, and lands
// the change: retile, repaint, and a state push so the session's other clients
// adopt it.
func (m *OS) SetSharedBordersSetting(v bool) {
	m.SharedBorders = v
	config.SharedBorders = v
	m.lastConfigSharedBorders = v
	m.setAppearance(func(a *config.AppearanceConfig) { a.SharedBorders = boolPtr(v) })
	m.applyAppearanceLive(true)
}

// SetPaneGapSetting is SetSharedBordersSetting for the pane gap.
func (m *OS) SetPaneGapSetting(v int) {
	v = clampInt(v, 0, config.PaneGapMax)
	m.PaneGap = v
	config.PaneGap = v
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
	config.MasterRatioPercent = percent
	m.setAppearance(func(a *config.AppearanceConfig) { a.MasterRatio = percent })
	m.applyAppearanceLive(true)
}

// SetScrollColumnWidthSetting is SetSharedBordersSetting for a scrolling
// column's width, as a percent of the screen.
func (m *OS) SetScrollColumnWidthSetting(percent int) {
	percent = clampInt(percent, config.ScrollColumnWidthMin, config.ScrollColumnWidthMax)
	m.ScrollColumnWidth = percent
	config.ScrollColumnWidth = percent
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
		w = config.ScrollColumnWidth
	}
	return float64(clampInt(w, config.ScrollColumnWidthMin, config.ScrollColumnWidthMax)) / 100
}

// MasterRatioPercent is the session's master ratio as the settings row shows
// it. Rounded rather than truncated, so a ratio the resize keys nudged to 0.55
// reads back as 55 and not 54.
func (m *OS) MasterRatioPercent() int {
	return clampInt(int(m.MasterRatio*100+0.5), config.MasterRatioMin, config.MasterRatioMax)
}

// adoptPaneGeometry takes the pane geometry as the session has it. Nil is a
// peer that has not said - an older client, or state written before the field
// existed - and leaves this client on its own configured values, which is the
// pre-existing behaviour. It reports whether anything moved, because a moved
// input obsoletes every tiled rectangle and the caller owes the layout a
// retile that the geometry checks cannot always see: flipping shared borders
// with the gap pinned changes every pane's guest grid without moving a single
// rectangle.
func (m *OS) adoptPaneGeometry(state *session.SessionState) bool {
	if state == nil || state.PaneGeometry == nil {
		return false
	}
	pg := state.PaneGeometry
	changed := m.SharedBorders != pg.SharedBorders || m.PaneGap != pg.PaneGap
	m.SharedBorders = pg.SharedBorders
	m.PaneGap = pg.PaneGap
	// Zero is a peer that has not said - state written before the field existed
	// - and leaves this client on its own configured width, which is the
	// pre-existing behaviour and the rule a nil PaneGeometry already follows.
	if pg.ScrollColumnWidth != 0 && pg.ScrollColumnWidth != m.ScrollColumnWidth {
		m.ScrollColumnWidth = pg.ScrollColumnWidth
		changed = true
	}
	return changed
}

// sharedBordersItem is the settings row for shared borders. It is written by
// hand rather than derived from the registry because the value in force is the
// session's, not this client's config: after a peer's setting has been
// adopted, a row reading the config would show a value the layout is not
// using, and its first toggle would appear to do nothing.
func (m *OS) sharedBordersItem() settingItem {
	return boolItem(
		settingLabel("appearance.shared_borders"),
		registryDescription("appearance.shared_borders"),
		func() bool { return m.SharedBorders },
		func(m *OS, v bool) {
			m.SetSharedBordersSetting(v)
		},
	)
}

// paneGapItem is the settings row for the pane gap, hand-written for the same
// reason as sharedBordersItem.
func (m *OS) paneGapItem() settingItem {
	return settingItem{
		Label:   settingLabel("appearance.gap"),
		Desc:    registryDescription("appearance.gap"),
		Control: controlInt,
		value:   func(m *OS) string { return strconv.Itoa(m.PaneGap) },
		adjust: func(m *OS, dir int) {
			m.SetPaneGapSetting(m.PaneGap + dir)
		},
		meter: func(m *OS) float64 {
			return float64(clampInt(m.PaneGap, 0, config.PaneGapMax)) / float64(config.PaneGapMax)
		},
	}
}

// masterRatioItem is the settings row for the master pane's share, hand-written
// for the same reason sharedBordersItem is: the value in force is the session's
// and not this client's config, and the resize keys move it under the row.
func (m *OS) masterRatioItem() settingItem {
	return percentItem("appearance.master_ratio",
		config.MasterRatioMin, config.MasterRatioMax,
		(*OS).MasterRatioPercent,
		(*OS).SetMasterRatioSetting)
}

// scrollColumnWidthItem is the settings row for a scrolling column's width.
func (m *OS) scrollColumnWidthItem() settingItem {
	return percentItem("appearance.scroll_column_width",
		config.ScrollColumnWidthMin, config.ScrollColumnWidthMax,
		func(m *OS) int {
			return clampInt(m.ScrollColumnWidth, config.ScrollColumnWidthMin, config.ScrollColumnWidthMax)
		},
		(*OS).SetScrollColumnWidthSetting)
}

// percentItem is a stepper over a percentage the session settles: it reads the
// model and writes through a setter that pushes the change to the session's
// other clients. The meter fills against the option's own range rather than
// against 100, so the whole travel of the control is the travel of the setting.
func percentItem(path string, lo, hi int, get func(*OS) int, set func(*OS, int)) settingItem {
	return settingItem{
		Label:   settingLabel(path),
		Desc:    registryDescription(path),
		Control: controlInt,
		value:   func(m *OS) string { return strconv.Itoa(get(m)) + "%" },
		adjust:  func(m *OS, dir int) { set(m, get(m)+dir) },
		meter: func(m *OS) float64 {
			return float64(clampInt(get(m), lo, hi)-lo) / float64(hi-lo)
		},
	}
}

// registryDescription is the registry's own description for a path, so a
// hand-written row reads exactly as the derived row it stands in for did.
func registryDescription(path string) string {
	if o, ok := config.LookupOption(path); ok {
		return o.Description
	}
	return ""
}
