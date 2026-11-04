package main

import (
	"context"
	"image/color"
	"path/filepath"
	"sync/atomic"

	"github.com/Gaurav-Gosain/mygo"
	"github.com/Gaurav-Gosain/mygo/plugins/terminal"
	"github.com/Gaurav-Gosain/mygo/ui"
	"tuios.dev/swatch"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// `theme = tuios` colors guios with the theme the TUI has: the one
// config.toml names in appearance.theme (appearance.theme_light while the
// window is light), Omarchy's while tuios follows Omarchy. Every role of
// the palette derives from it as from a Ghostty theme (ghosttyLook), so a
// ricer's theme reaches the sidebar, the chips, the menus and the agents
// view as well as the terminals (DESIGN.md section 1). It follows a change
// of config.toml, and a switch of the Omarchy theme, live.

const tuiosThemeName = "tuios"

// tuiosThemeGen counts changes of the tuios theme, for the looks' key.
var tuiosThemeGen atomic.Uint64

// tuiosNamesNone tells, per appearance (0 light, 1 dark), that the tuios
// theme fell back to paper as the looks were last built: Settings then
// marks Paper as the theme in force.
var tuiosNamesNone [2]atomic.Bool

// tuiosLook is the look of the tuios theme, nil while tuios names no
// theme guios can read.
func tuiosLook(dark bool) *look {
	th := tuiosTermTheme(dark)
	if th == nil {
		return nil
	}
	return ghosttyLook(th)
}

// tuiosTermTheme is the tuios theme as a terminal theme.
func tuiosTermTheme(dark bool) *terminal.Theme {
	cfg, err := config.LoadUserConfig()
	if err != nil {
		return nil
	}
	id := cfg.Appearance.Theme
	if !dark && cfg.Appearance.ThemeLight != "" {
		id = cfg.Appearance.ThemeLight
	}
	if theme.FollowsOmarchy(id) {
		id = theme.OmarchyID
	}
	if id == "" || id == theme.NoneID {
		return nil
	}
	theme.EnsureRegistry()
	pal, fg, bg, cursor, ok := theme.Colors(id)
	if !ok || fg == nil || bg == nil {
		return nil
	}
	th := &terminal.Theme{Foreground: uiColor(fg), Background: uiColor(bg)}
	for i, c := range pal {
		if c != nil {
			th.Palette[i] = uiColor(c)
		}
	}
	th.Cursor = th.Foreground
	if cursor != nil {
		th.Cursor = uiColor(cursor)
	}
	th.CursorText = th.Background
	th.Selection = th.Palette[4].Mix(th.Background, 0.6)
	th.SelectionText = th.Foreground
	return th
}

func uiColor(c color.Color) ui.Color {
	r, g, b, _ := c.RGBA()
	return ui.RGB(uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

// followTuiosTheme watches config.toml's folder, and the applied Omarchy
// theme on a machine with Omarchy, and recolors the app as they change
// while it shows the tuios theme. Each watch is one inotify watch and a
// goroutine blocked on it: no timer runs.
func (a *App) followTuiosTheme() {
	changed := func() {
		a.update(func() {
			if a.set.LightTheme != tuiosThemeName && a.set.DarkTheme != tuiosThemeName {
				return
			}
			tuiosThemeGen.Add(1)
			a.retheme()
		})
	}
	if path, err := config.GetConfigPath(); err == nil {
		go func() {
			w, err := mygo.WatchFiles(context.Background(), filepath.Dir(path), mygo.FileWatchOptions{HintsOnly: true})
			if err != nil {
				return
			}
			defer w.Close()
			for {
				if _, err := w.Next(context.Background()); err != nil {
					return
				}
				changed()
			}
		}()
	}
	if theme.OmarchyInstalled() {
		if w, err := swatch.Watch(theme.OmarchySource()); err == nil {
			go func() {
				for p := range w.Changes() {
					theme.SetOmarchyPalette(p)
					changed()
				}
			}()
		}
	}
}

// retheme gives every terminal the window's terminal themes again, and
// draws the windows anew: the looks changed without a setting changing.
func (a *App) retheme() {
	light, dark := a.termThemes()
	for _, e := range a.terms {
		if e.term != nil {
			e.term.SetTheme(light, dark)
		}
	}
	if t := a.quickTermFor(a.quick.pane); t != nil {
		t.SetTheme(light, dark)
	}
	for _, w := range []*mygo.Window{a.win, a.setWin, a.quick.win} {
		if w != nil {
			w.Invalidate()
		}
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
