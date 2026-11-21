package app

import (
	"image/color"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type colorSetting struct {
	Path       string
	Label      string
	Desc       string
	Unset      string
	apply      func(m *OS, v string)
	effective  func(ground color.Color, s *config.Settings) color.Color
	namedColor func(keyword string, ground color.Color) color.Color
}

var colorSettings = []colorSetting{
	{
		Path:  "appearance.border_focused_color",
		Label: "Focused border color",
		Desc:  "Colour of the focused pane's border",
		Unset: "(theme)",
		apply: func(m *OS, _ string) { m.applyBorderColors() },
		effective: func(color.Color, *config.Settings) color.Color {
			return theme.BorderFocusedWindow()
		},
	},
	{
		Path:  "appearance.border_focused_color_window",
		Label: "Window mode border",
		Desc:  "Colour of the focused pane's border in window mode",
		Unset: "(focused border)",
		apply: func(m *OS, _ string) { m.applyBorderColors() },
		effective: func(color.Color, *config.Settings) color.Color {
			return theme.BorderFocusedWindow()
		},
	},
	{
		Path:  "appearance.border_focused_color_terminal",
		Label: "Terminal mode border",
		Desc:  "Colour of the focused pane's border in terminal mode",
		Unset: "(focused border)",
		apply: func(m *OS, _ string) { m.applyBorderColors() },
		effective: func(color.Color, *config.Settings) color.Color {
			return theme.BorderFocusedTerminal()
		},
	},
	{
		Path:  "appearance.border_unfocused_color",
		Label: "Unfocused border color",
		Desc:  "Colour of every unfocused pane's border",
		Unset: "(theme)",
		apply: func(m *OS, _ string) { m.applyBorderColors() },
		effective: func(color.Color, *config.Settings) color.Color {
			return theme.BorderUnfocused()
		},
	},
	{
		Path:  "appearance.overlay_border_color",
		Label: "Overlay frame color",
		Desc:  "Colour of every overlay's frame: dialogs, framed panels and which-key",
		Unset: "(overlay's own)",
		apply: func(m *OS, v string) {
			m.Settings.OverlayBorderColor = v
			m.MarkAllDirty()
		},
		effective: func(_ color.Color, s *config.Settings) color.Color {
			if c := theme.ConfigColor(s.OverlayBorderColor); !isNilColor(c) {
				return c
			}
			if s.PaneOverlays {
				return theme.BorderFocusedWindow()
			}
			return theme.UI().FgMute
		},
	},
	{
		Path:  "appearance.scrollbar.tint",
		Label: "Scrollbar tint",
		Desc:  "quiet: the pane's own ink, dimmed. border: the focused pane's accent. muted: one grey",
		Unset: "(quiet)",
		apply: func(m *OS, v string) {
			m.Settings.ScrollbarTint = v
			m.MarkAllDirty()
		},
		effective:  scrollbarTintColor,
		namedColor: scrollbarTintKeywordColor,
	},

	backgroundSetting("appearance.background", "All surfaces",
		"Every surface below that is not set on its own. off: your terminal shows through. theme: the theme's background",
		"(off)",
		func(s *config.Settings, v string) { s.Background = v },
		(*config.Settings).AllBackgroundResolved),
	backgroundSetting("appearance.pane_background", "Pane background",
		"Behind pane content. A colour a program chose wins",
		backgroundFollowsAll,
		func(s *config.Settings, v string) { s.PaneBackground = v },
		(*config.Settings).PaneBackgroundResolved),
	backgroundSetting("appearance.desktop_background", "Desktop background",
		"Behind and between panes: the gaps, the space around floating panes, an empty workspace",
		backgroundFollowsAll,
		func(s *config.Settings, v string) { s.DesktopBackground = v },
		(*config.Settings).DesktopBackgroundResolved),
	backgroundSetting("appearance.window_chrome_background", "Window chrome background",
		"Under pane borders and title bars. Their own colours are kept",
		backgroundFollowsAll,
		func(s *config.Settings, v string) { s.WindowChromeBackground = v },
		(*config.Settings).WindowChromeBackgroundResolved),
	backgroundSetting("appearance.dock_background", "Dock background",
		"Under the dock. Its pills and indicators keep their own colours",
		backgroundFollowsAll,
		func(s *config.Settings, v string) { s.DockBackground = v },
		(*config.Settings).DockBackgroundResolved),
	backgroundSetting("appearance.sidebar.background", "Sidebar background",
		"Under the rail. Its highlights keep their own colours",
		backgroundFollowsAll,
		func(s *config.Settings, v string) { s.SidebarBackground = v },
		(*config.Settings).SidebarBackgroundResolved),

	{
		Path:  "appearance.sidebar.cursor_background",
		Label: "Sidebar cursor",
		Desc:  "Background of the row under the cursor while the sidebar has the keyboard",
		Unset: "(theme)",
		apply: func(m *OS, v string) {
			m.Settings.SidebarCursorBackground = v
			m.MarkAllDirty()
		},
		effective: func(_ color.Color, s *config.Settings) color.Color {
			pal := theme.GroundUI()
			if c := railCursorBg(s.SidebarCursorBackground, pal); c != nil {
				return c
			}
			return pal.Surface
		},
	},

	{
		Path:  "appearance.selection.bg",
		Label: "Selection",
		Desc:  "Background behind text selected in copy mode",
		apply: selectionColorApply(func(s *config.Settings, v string) { s.SelectionBg = v }),
		effective: func(_ color.Color, s *config.Settings) color.Color {
			return configInk(s.SelectionBg)
		},
	},
	{
		Path:  "appearance.selection.fg",
		Label: "Selected text",
		Desc:  "Colour of selected text. Empty keeps the colour it already has.",
		Unset: "(keep)",
		apply: selectionColorApply(func(s *config.Settings, v string) { s.SelectionFg = v }),
		effective: func(ground color.Color, s *config.Settings) color.Color {
			return selectionInk(s.SelectionFg, ground)
		},
	},
	{
		Path:  "appearance.selection.search_bg",
		Label: "Search match",
		Desc:  "Background behind every match of a search",
		apply: selectionColorApply(func(s *config.Settings, v string) { s.SearchBg = v }),
		effective: func(_ color.Color, s *config.Settings) color.Color {
			return configInk(s.SearchBg)
		},
	},
	{
		Path:  "appearance.selection.search_fg",
		Label: "Search match text",
		Desc:  "Colour of matched text. Empty keeps the colour it has.",
		Unset: "(keep)",
		apply: selectionColorApply(func(s *config.Settings, v string) { s.SearchFg = v }),
		effective: func(ground color.Color, s *config.Settings) color.Color {
			return selectionInk(s.SearchFg, ground)
		},
	},
	{
		Path:  "appearance.selection.match_bg",
		Label: "Current match",
		Desc:  "Background behind the one match the cursor is on",
		apply: selectionColorApply(func(s *config.Settings, v string) { s.MatchBg = v }),
		effective: func(_ color.Color, s *config.Settings) color.Color {
			return configInk(s.MatchBg)
		},
	},
	{
		Path:  "appearance.selection.match_fg",
		Label: "Current match text",
		Desc:  "Colour of the match under the cursor. Empty keeps it.",
		Unset: "(keep)",
		apply: selectionColorApply(func(s *config.Settings, v string) { s.MatchFg = v }),
		effective: func(ground color.Color, s *config.Settings) color.Color {
			return selectionInk(s.MatchFg, ground)
		},
	},
	{
		Path:  "appearance.selection.cursor_bg",
		Label: "Copy mode cursor",
		Desc:  "Background of the block copy mode draws where its cursor is",
		apply: selectionColorApply(func(s *config.Settings, v string) { s.CopyCursorBg = v }),
		effective: func(_ color.Color, s *config.Settings) color.Color {
			return configInk(s.CopyCursorBg)
		},
	},
	{
		Path:  "appearance.selection.cursor_fg",
		Label: "Copy mode cursor text",
		Desc:  "Colour under the copy mode cursor. Empty keeps it.",
		Unset: "(keep)",
		apply: selectionColorApply(func(s *config.Settings, v string) { s.CopyCursorFg = v }),
		effective: func(ground color.Color, s *config.Settings) color.Color {
			return selectionInk(s.CopyCursorFg, ground)
		},
	},
	{
		Path:  "appearance.selection.flash_color",
		Label: "Copy sweep",
		Desc:  "The light that crosses text you just copied",
		apply: selectionColorApply(func(s *config.Settings, v string) { s.CopyFlashColor = v }),
		effective: func(_ color.Color, s *config.Settings) color.Color {
			return configInk(s.CopyFlashColor)
		},
	},
}

func selectionColorApply(set func(*config.Settings, string)) func(*OS, string) {
	return func(m *OS, v string) {
		set(&m.Settings, v)
		m.MarkAllDirty()
	}
}

func selectionInk(v string, ground color.Color) color.Color {
	if v == "" {
		return ground
	}
	return configInk(v)
}

func configInk(v string) color.Color {
	if c := theme.ConfigColor(v); c != nil {
		return c
	}
	return lipgloss.NoColor{}
}

func scrollbarTintColor(ground color.Color, s *config.Settings) color.Color {
	if v, ok := s.ScrollbarTintColor(); ok {
		if c := theme.ConfigColor(v); c != nil {
			return c
		}
	}
	return scrollbarTintKeywordColor(s.ScrollbarTintResolved(), ground)
}

func scrollbarTintKeywordColor(keyword string, ground color.Color) color.Color {
	switch keyword {
	case config.ScrollbarTintMuted:
		return theme.BorderUnfocused()
	case config.ScrollbarTintBorder:
		return theme.BorderFocusedWindow()
	default:
		return scrollbarQuietInk(ground, scrollbarQuietThumbContrast)
	}
}

const backgroundFollowsAll = "(all surfaces)"

func backgroundSetting(path, label, desc, unset string,
	set func(*config.Settings, string), resolved func(*config.Settings) string) colorSetting {
	return colorSetting{
		Path:  path,
		Label: label,
		Desc:  desc,
		Unset: unset,
		apply: func(m *OS, v string) {
			set(&m.Settings, v)
			m.backgroundsChanged()
		},
		effective: func(ground color.Color, s *config.Settings) color.Color {
			v := resolved(s)
			if c := theme.ConfigColor(v); c != nil {
				return c
			}
			return backgroundKeywordColor(v, ground)
		},
		namedColor: backgroundKeywordColor,
	}
}

func backgroundKeywordColor(keyword string, ground color.Color) color.Color {
	if keyword == config.BackgroundTheme && theme.CurrentThemeID() != "" {
		return theme.TerminalBg()
	}
	return ground
}

func lookupColorSetting(path string) (colorSetting, bool) {
	for _, cs := range colorSettings {
		if cs.Path == path {
			return cs, true
		}
	}
	return colorSetting{}, false
}

func (cs colorSetting) value(m *OS) string {
	if m.UserConfig == nil {
		return ""
	}
	v, _ := config.GetOptionValue(m.UserConfig, cs.Path)
	return v
}

func (cs colorSetting) named() []string {
	opt, ok := config.LookupOption(cs.Path)
	if !ok {
		return nil
	}
	return opt.Accepted
}

func (cs colorSetting) label(v string) string {
	if v == "" {
		return cs.Unset
	}
	return v
}

func (m *OS) setColorOption(path, value string) tea.Cmd {
	cs, ok := lookupColorSetting(path)
	if !ok {
		return nil
	}
	if m.UserConfig != nil {
		if err := config.SetOptionValue(m.UserConfig, path, value); err != nil {
			m.ShowNotification(err.Error(), "error", m.Settings.NotificationDuration)
			return nil
		}
	}
	cs.apply(m, value)
	return m.persistSettings()
}

func colorSettingItem(path string) settingItem {
	setting, ok := lookupColorSetting(path)
	if !ok {
		return settingItem{Label: path, Desc: "no colour setting at this path", Control: controlColor}
	}
	return settingItem{
		Label:    setting.Label,
		Desc:     setting.Desc,
		Control:  controlColor,
		Unset:    setting.Unset,
		value:    func(m *OS) string { return setting.label(setting.value(m)) },
		swatch:   func(ground color.Color, s *config.Settings) color.Color { return setting.effective(ground, s) },
		activate: func(m *OS) tea.Cmd { m.OpenColorSetting(setting.Path); return nil },
	}
}

func colorSwatch(c, ground color.Color) string {
	return overlay.Style(accentPaint(c)).Render("  ")
}
