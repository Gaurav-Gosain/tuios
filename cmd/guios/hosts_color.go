package main

import (
	"hash/fnv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/settings"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// hostColors are the colors of settings.HostColors, light and dark, which
// mark a host's groups in the sidebar and its panes' headers.
var hostColors = map[string][2]ui.Color{
	"red":    {ui.Hex("#d9453b"), ui.Hex("#ff7a70")},
	"orange": {ui.Hex("#dc6a1f"), ui.Hex("#ff9d5c")},
	"amber":  {ui.Hex("#b8860b"), ui.Hex("#f2c14e")},
	"green":  {ui.Hex("#3a8f47"), ui.Hex("#6fd27c")},
	"teal":   {ui.Hex("#13897b"), ui.Hex("#4fd1c0")},
	"blue":   {ui.Hex("#3b6cf6"), ui.Hex("#7b9fff")},
	"purple": {ui.Hex("#8257e6"), ui.Hex("#b493ff")},
	"pink":   {ui.Hex("#cf4c7c"), ui.Hex("#f58fb5")},
}

// hostColorName is a host's color: its own, else the one its name picks,
// which stays the same from run to run.
func hostColorName(h *host) string {
	if h.cfg.Color != "" {
		return h.cfg.Color
	}
	f := fnv.New32a()
	f.Write([]byte(strings.ToLower(h.name)))
	return settings.HostColors[f.Sum32()%uint32(len(settings.HostColors))]
}

// hostColor is the color that marks a host's sessions: the text's for
// this machine, which no other's is.
func (pal *palette) hostColor(h *host) ui.Color {
	if !h.remote() {
		return pal.textMuted
	}
	c := hostColors[hostColorName(h)]
	if pal.dark {
		return c[1]
	}
	return c[0]
}

// colorMenu sets a host's color, in its settings.
func (a *App) colorMenu(m *ui.Menu, h *host) {
	m.Submenu(locales.HostColor(), func(m *ui.Menu) {
		if m.Item(locales.HostColorAuto()).Checked(h.cfg.Color == "").Chosen() {
			a.setHostColor(h, "")
		}
		m.Separator()
		for _, name := range settings.HostColors {
			if m.Item(colorLabel(name)).Checked(h.cfg.Color == name).Chosen() {
				a.setHostColor(h, name)
			}
		}
	})
}

func (a *App) setHostColor(h *host, color string) {
	s := a.set.Clone()
	if cfg := s.Host(h.name); cfg != nil {
		cfg.Color = color
		h.cfg.Color = color
	}
	a.set = s
}

func colorLabel(name string) string {
	switch name {
	case "red":
		return locales.ColorRed()
	case "orange":
		return locales.ColorOrange()
	case "amber":
		return locales.ColorAmber()
	case "green":
		return locales.ColorGreen()
	case "teal":
		return locales.ColorTeal()
	case "blue":
		return locales.ColorBlue()
	case "purple":
		return locales.ColorPurple()
	}
	return locales.ColorPink()
}
