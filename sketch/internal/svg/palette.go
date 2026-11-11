// Ported from pele src/svg/palette.ts at 3a2f075ee0c3d914f0fad3545fc0f193693975ac.

package svg

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// paletteVars are Mermaid's palette theme variables, which set a diagram's own series
// colors. first is the number of the variable for the first series. Mindmap branches start
// at cScale1, as cScale0 is the root's in Mermaid's styles. A venn palette repeats the colors
// it sets, as in Mermaid.
var paletteVars = map[string]struct {
	prefix string
	first  int
	repeat bool
}{
	"pie":      {"pie", 1, false},
	"gitGraph": {"git", 0, false},
	"timeline": {"cScale", 0, false},
	"radar":    {"cScale", 0, false},
	"treemap":  {"cScale", 0, false},
	"mindmap":  {"cScale", 1, false},
	"venn":     {"venn", 1, true},
}

const paletteSlots = 8

// paletteFunctions are the CSS functions a palette color may call.
var paletteFunctions = wordList("rgb rgba hsl hsla hwb lab lch oklab oklch color color-mix light-dark var")

// wordByte reports whether c is in JavaScript's [\w-].
func wordByte(c byte) bool { return classByte(c) }

// paletteChars is RE_CHARS, /^[\w\s#%.,+\-/()]{1,200}$/: a value of 1 to 200 UTF-16
// units, each a word character, a JavaScript space or one of #%.,+-/().
func paletteChars(s string) bool {
	n := 0
	for _, r := range s {
		n++
		if n > 200 {
			return false
		}
		if r < 0x80 && (wordByte(byte(r)) || strings.IndexByte("#%.,+/()", byte(r)) >= 0) {
			continue
		}
		if !js.IsSpace(r) {
			return false
		}
	}
	return n > 0
}

// safeColor is a color that cannot load a resource, end its declaration or read the page's
// own variables. Values outside functions are checked as colors, so a typo keeps the theme
// color.
func safeColor(value any) (string, bool) {
	raw, ok := value.(string)
	if !ok {
		return "", false
	}
	color := js.Trim(raw)
	if !paletteChars(color) {
		return "", false
	}
	if !strings.Contains(color, "(") {
		return color, PlainColor(color)
	}
	depth := 0
	for i := 0; i < len(color); i++ {
		switch color[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return "", false
			}
		}
	}
	if depth != 0 {
		return "", false
	}
	// RE_FUNCTION, /([\w-]*)\s*\(/g: the name before each parenthesis, past the spaces
	// between them.
	for i := 0; i < len(color); i++ {
		if color[i] != '(' {
			continue
		}
		end := i
		for end > 0 {
			r, size := utf8.DecodeLastRuneInString(color[:end])
			if !js.IsSpace(r) {
				break
			}
			end -= size
		}
		start := end
		for start > 0 && wordByte(color[start-1]) {
			start--
		}
		if !listed(paletteFunctions, strings.ToLower(color[start:end])) {
			return "", false
		}
	}
	// RE_VAR, /var\(\s*([\w-]*)/gi: each variable read must be one of pele's.
	lower := strings.ToLower(color)
	for i := 0; ; {
		at := strings.Index(lower[i:], "var(")
		if at < 0 {
			break
		}
		j := i + at + len("var(")
		for j < len(color) {
			r, size := utf8.DecodeRuneInString(color[j:])
			if !js.IsSpace(r) {
				break
			}
			j += size
		}
		k := j
		for k < len(color) && wordByte(color[k]) {
			k++
		}
		if !strings.HasPrefix(color[j:k], "--pele-") {
			return "", false
		}
		i = k
	}
	return color, true
}

// splitPaletteList splits at the commas between colors, not those inside rgb() and other
// functions.
func splitPaletteList(value string) []any {
	var parts []any
	depth := 0
	start := 0
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, js.Trim(value[start:i]))
				start = i + 1
			}
		}
	}
	return append(parts, js.Trim(value[start:]))
}

func paletteEntries(typ string, vars map[string]any) (list []any, repeat bool) {
	if typ == "xychart" {
		xy, _ := vars["xyChart"].(map[string]any)
		if s, ok := xy["plotColorPalette"].(string); ok {
			return splitPaletteList(s), true
		}
		return nil, false
	}
	p, ok := paletteVars[typ]
	if !ok {
		return nil, false
	}
	for i := range paletteSlots {
		v := vars[p.prefix+strconv.Itoa(p.first+i)]
		if p.repeat && (v == nil || v == "") {
			continue
		}
		list = append(list, v)
	}
	return list, p.repeat
}

// NewPalette is the palette that a diagram's theme variables set, or nil when they set
// none that is a usable color. An entry that is not one leaves its series in the theme's
// color.
func NewPalette(typ string, config types.Config) *types.Palette {
	vars, _ := config["themeVariables"].(map[string]any)
	if vars == nil {
		return nil
	}
	list, repeat := paletteEntries(typ, vars)
	if len(list) == 0 {
		return nil
	}
	var style strings.Builder
	var inks [paletteSlots]string
	for i := range paletteSlots {
		if !repeat && i >= len(list) {
			break
		}
		color, ok := safeColor(list[i%len(list)])
		if !ok {
			continue
		}
		WriteStrings(&style, ";--pele-series-", strconv.Itoa(i+1), ":", color)
		inks[i] = Ink(";fill:"+color, "")
	}
	if style.Len() == 0 {
		return nil
	}
	return types.NewPalette(Esc(style.String()), inks)
}

// SeriesInkFill is a fill attribute for a label on series index, when the palette sets a
// color that needs one, else "".
func SeriesInkFill(p *types.Palette, index int) string {
	switch c := p.Ink(index); c {
	case "":
		return ""
	case ON_LIGHT:
		return ` fill="` + ON_LIGHT + `"`
	default:
		return ` fill="` + ON_DARK + `"`
	}
}
