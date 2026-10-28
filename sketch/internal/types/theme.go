// Not a file of pele: the port's theme, which sets the --pele-* tokens that
// pele leaves to the page.

package types

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/lazyre"
)

// A list of font names, quoted or not, and nothing that could end the
// declaration it is put in. It is the check of families() in pele's
// src/svg/theme.ts; internal/svg uses it too.
const family = `(?:"[^"'\\\n<>{}();:@]*"|'[^"'\\\n<>{}();:@]*'|[\p{L}\p{N}_-][\p{L}\p{N}_ .-]*)`

var reFamilies = lazyre.New(`^[` + js.S + `]*` + family + `(?:[` + js.S + `]*,[` + js.S + `]*` + family + `)*[` + js.S + `]*$`)

// SafeFamilies reports whether list is a font family list that cannot end
// the declaration it is put in.
func SafeFamilies(list string) bool {
	return reFamilies.MatchString(list)
}

// Theme gives the colours, fonts and corner radius of a diagram. A nil colour
// keeps pele's fallback.
type Theme struct {
	Bg, Fg, Muted, Line, Surface, SurfaceAlt, Border, Accent, Critical color.Color
	Series                                                             [8]color.Color
	// FgOnLight and FgOnDark are the text colours on a fill the diagram sets
	// itself, such as a classDef fill: FgOnLight on a light fill and FgOnDark
	// on a dark one. nil keeps pele's fallbacks, #1f1f1f and #fff.
	FgOnLight, FgOnDark color.Color
	// Font and FontMono are CSS font family lists. "" keeps the fallback, and
	// so does a list that pele's font check refuses, such as one with ";".
	Font, FontMono string
	// Radius is the corner radius in pixels. 0, a negative value, NaN and
	// infinity keep the fallback of 4.
	Radius float64
}

func hexColor(c color.Color) string {
	r, g, b, a := c.RGBA()
	if a == 0 {
		return "transparent"
	}
	// Un-premultiply. A colour with a channel above its alpha is not a valid
	// premultiplied colour; its channels are held to the alpha.
	r, g, b = min(r, a)*0xffff/a, min(g, a)*0xffff/a, min(b, a)*0xffff/a
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
	add("--pele-fg-on-light", t.FgOnLight)
	add("--pele-fg-on-dark", t.FgOnDark)
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
	if t.Font != "" && SafeFamilies(t.Font) {
		out = append(out, [2]string{"--pele-font", t.Font})
	}
	if t.FontMono != "" && SafeFamilies(t.FontMono) {
		out = append(out, [2]string{"--pele-font-mono", t.FontMono})
	}
	if t.Radius > 0 && !math.IsInf(t.Radius, 1) {
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
