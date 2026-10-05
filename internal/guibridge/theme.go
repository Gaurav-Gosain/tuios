package guibridge

import (
	"fmt"
	"image/color"
	"reflect"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Theme is the theme as the renderer paints it, worked out by the same code
// the terminal client uses, at truecolor depth: the terminal's colours, and
// the chrome palettes tuios derives from them (theme.UI for dialogs,
// theme.GroundUI for the rail and the dock). A theme name therefore looks the
// same in both clients. Colours are "#rrggbb"; an empty string is no colour.
type Theme struct {
	Name  string   `json:"name"`
	Names []string `json:"names"`
	// Light says the theme's ground is light.
	Light    bool              `json:"light"`
	Terminal TerminalColors    `json:"terminal"`
	UI       map[string]string `json:"ui"`
	Ground   map[string]string `json:"ground"`
	// Single colours the terminal client draws its frame with.
	RailGround            string `json:"rail_ground"`
	RailRule              string `json:"rail_rule"`
	BorderFocused         string `json:"border_focused"`
	BorderFocusedTerminal string `json:"border_focused_terminal"`
	BorderUnfocused       string `json:"border_unfocused"`
	// Agent state colours, as the rail draws them.
	Agent map[string]string `json:"agent"`
}

// TerminalColors are a pane's default colours and its 16-colour palette.
type TerminalColors struct {
	Fg     string     `json:"fg"`
	Bg     string     `json:"bg"`
	Cursor string     `json:"cursor"`
	ANSI   [16]string `json:"ansi"`
}

func hex(c color.Color) string {
	if c == nil {
		return ""
	}
	if v := reflect.ValueOf(c); v.Kind() == reflect.Pointer && v.IsNil() {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// paletteMap lists every colour field of an overlay.Palette by name.
func paletteMap(p overlay.Palette) map[string]string {
	out := map[string]string{}
	v := reflect.ValueOf(p)
	ct := reflect.TypeFor[color.Color]()
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if f.Type != ct {
			continue
		}
		c, _ := v.Field(i).Interface().(color.Color)
		out[f.Name] = hex(overlay.Shown(c))
	}
	return out
}

// CurrentTheme exports the theme in force.
func CurrentTheme() Theme {
	t := Theme{Name: theme.CurrentThemeID(), Names: theme.AvailableThemes()}
	t.Terminal.Fg = hex(theme.TerminalFg())
	t.Terminal.Bg = hex(theme.TerminalBg())
	t.Terminal.Cursor = hex(theme.TerminalCursor())
	if theme.IsEnabled() {
		for i, c := range theme.GetANSIPalette() {
			t.Terminal.ANSI[i] = hex(c)
		}
	}
	ground := theme.RailGround()
	t.Light = theme.GroundIsLight(ground)
	ui := theme.UI()
	gp := theme.GroundUI()
	t.UI = paletteMap(ui)
	t.Ground = paletteMap(gp)
	t.RailGround = hex(ground)
	t.RailRule = hex(theme.RailRuleOn(ground))
	t.BorderFocused = hex(theme.BorderFocusedWindow())
	t.BorderFocusedTerminal = hex(theme.BorderFocusedTerminal())
	t.BorderUnfocused = hex(theme.BorderUnfocused())
	t.Agent = map[string]string{
		"working":     hex(overlay.Shown(gp.Info)),
		"needs_input": hex(overlay.Shown(gp.Warning)),
		"idle":        hex(overlay.Shown(gp.FgMute)),
		"done":        hex(overlay.Shown(gp.Success)),
		"errored":     hex(overlay.Shown(gp.Warn)),
	}
	return t
}
