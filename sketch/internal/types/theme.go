// Not a file of pele: the port's theme, which sets the --pele-* tokens that
// pele leaves to the page.

package types

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
)

// Theme gives the colours, fonts and corner radius of a diagram. A nil colour
// keeps pele's fallback.
type Theme struct {
	Bg, Fg, Muted, Line, Surface, SurfaceAlt, Border, Accent, Critical color.Color
	Series                                                             [8]color.Color
	// Font and FontMono are CSS font family lists. "" keeps the fallback.
	Font, FontMono string
	// Radius is the corner radius in pixels. 0 keeps the fallback of 4.
	Radius float64
}

func hexColor(c color.Color) string {
	r, g, b, a := c.RGBA()
	if a == 0 {
		return "transparent"
	}
	// Un-premultiply.
	r, g, b = r*0xffff/a, g*0xffff/a, b*0xffff/a
	if a == 0xffff {
		return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	}
	return fmt.Sprintf("#%02x%02x%02x%02x", r>>8, g>>8, b>>8, a>>8)
}

// Vars lists the --pele-* tokens the theme sets, in a fixed order.
func (t *Theme) Vars() [][2]string {
	if t == nil {
		return nil
	}
	var out [][2]string
	add := func(name string, c color.Color) {
		if c != nil {
			out = append(out, [2]string{name, hexColor(c)})
		}
	}
	add("--pele-bg", t.Bg)
	add("--pele-fg", t.Fg)
	add("--pele-muted", t.Muted)
	add("--pele-line", t.Line)
	add("--pele-surface", t.Surface)
	add("--pele-surface-alt", t.SurfaceAlt)
	add("--pele-border", t.Border)
	add("--pele-accent", t.Accent)
	add("--pele-critical", t.Critical)
	for i, c := range t.Series {
		add("--pele-series-"+strconv.Itoa(i+1), c)
	}
	if t.Font != "" {
		out = append(out, [2]string{"--pele-font", t.Font})
	}
	if t.FontMono != "" {
		out = append(out, [2]string{"--pele-font-mono", t.FontMono})
	}
	if t.Radius > 0 {
		out = append(out, [2]string{"--pele-radius", strconv.FormatFloat(t.Radius, 'f', -1, 64) + "px"})
	}
	return out
}

// Style is the theme as CSS declarations, each ending in a semicolon.
func (t *Theme) Style() string {
	var b strings.Builder
	for _, v := range t.Vars() {
		b.WriteString(v[0] + ":" + v[1] + ";")
	}
	return b.String()
}
