package main

import (
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// themeGroup is Settings > Appearance's theme: for light windows and for
// dark ones, the tuios theme guios and tuios share. A pick goes to tuios
// (shareTheme), as a pick on the TUI's settings page does, so the TUI on
// the session and config.toml have it too. The choices are every tuios
// theme by its id, as tuios set-config takes it. A preview beside each
// draws the theme in its own colors, the chrome's text tiers included:
// the one in force, or the one the open menu highlights.
func (a *App) themeGroup(c *ui.Context, pal *palette) {
	themes := theme.AvailableThemes()
	settingsGroup(c, pal, locales.SetBackground(), locales.SetColorsDetail(), func() {
		for _, dark := range []bool{false, true} {
			a.themeRow(c, pal, themes, dark)
		}
	})
}

func (a *App) themeRow(c *ui.Context, pal *palette, themes []string, dark bool) {
	slot, key, label := &a.set.LightTheme, "theme-light", locales.SetLightWindows()
	if dark {
		slot, key, label = &a.set.DarkTheme, "theme-dark", locales.SetDarkWindows()
	}
	same := locales.SetThemeSameAsTuios()
	tuiosID := tuiosThemeID(dark)
	// A theme for guios alone comes from an older setting: the row offers
	// Same as tuios to end it. guios offers no such choice of its own.
	pinned := tuiosThemeExists(*slot)
	value, options := tuiosID, themes
	switch {
	case pinned:
		value, options = *slot, append([]string{same}, themes...)
	case tuiosID == "":
		value = same
	}
	// The preview shows the highlighted choice while the menu is open.
	shown := value
	if m := a.ss.menus[key]; m != nil && m.open && m.lit != "" {
		shown = m.lit
	}
	id := shown
	if shown == same {
		id = tuiosID
	}
	detail := locales.SetThemeShared()
	switch {
	case pinned:
		detail = locales.SetThemeOwn()
	case tuiosID == "":
		detail = locales.SetThemeFollowsNone()
	}
	settingRowWith(c, pal, label, func() {
		ui.Text(c, detail).FontSize(fsSmall).TextColor(pal.textMuted).LineHeight(1.4)
		a.themePreview(c, previewLook(id, dark))
	}, func() {
		a.searchPopup(c, key, value, options, 240, nil, pal, func(v string) {
			*slot = tuiosThemeName
			if v != same {
				a.shareTheme(v, dark)
			}
		})
	})
}

// themePreview draws a look as a window shows it, small: the ground and
// the sidebar with its three text tiers and a selected row, and a pane's
// card with a prompt, a listing, the 16 colors and the cursor.
func (a *App) themePreview(c *ui.Context, l *look) {
	lp, th := l.pal, l.term
	mono := a.termFamily()
	tile := raised(ui.Box(c).Size(232, 112).Radius(rCard).Margin(6, 0, 2, 0), lp).Clip()
	tile.LinearGradient(l.base)
	tile.Children(func() {
		ui.Column(c).Absolute().Top(10).Left(8).Width(64).Gap(2).Children(func() {
			ui.Box(c).Radius(rChip).Padding(2, 5).Background(lp.rowSelected).Children(func() {
				ui.Text(c, "main").FontSize(fsCaption).FontWeight(wSemibold).TextColor(lp.text).SingleLine()
			})
			ui.Text(c, "~/dev").FontSize(fsCaption).TextColor(lp.textMuted).Padding(2, 5).SingleLine()
			ui.Text(c, "2m").FontSize(fsCaption).TextColor(lp.textFaint).Padding(2, 5).SingleLine()
			ui.Box(c).Size(6, 6).Radius(3).Margin(4, 5, 0, 5).Background(lp.accent)
		})
		ui.Column(c).Absolute().Top(8).Left(78).Right(8).Bottom(8).Radius(rControl).Background(th.Background).
			Padding(7, 8).Gap(3).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				// No breakable space: a line of the preview never wraps.
				ui.Text(c, "~").Font(mono).FontSize(fsCaption).TextColor(th.Palette[4])
				ui.Text(c, "$\u00a0ls").Font(mono).FontSize(fsCaption).TextColor(th.Foreground)
				ui.Box(c).Size(6, 12).Radius(1).Background(th.Cursor)
			})
			ui.Row(c).Gap(6).Children(func() {
				ui.Text(c, "src/").Font(mono).FontSize(fsCaption).TextColor(th.Palette[4])
				ui.Text(c, "go.mod").Font(mono).FontSize(fsCaption).TextColor(th.Foreground)
				ui.Text(c, "ok").Font(mono).FontSize(fsCaption).TextColor(th.Palette[2])
				ui.Text(c, "err").Font(mono).FontSize(fsCaption).TextColor(th.Palette[1])
			})
			ui.Box(c).Grow(1)
			for row := range 2 {
				ui.Row(c).Gap(3).Children(func() {
					for i := range 8 {
						ui.Box(c).Size(9, 9).Radius(2).Background(th.Palette[row*8+i])
					}
				})
			}
		})
	})
}
