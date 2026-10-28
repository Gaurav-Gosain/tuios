// Ported from pele src/svg/ink.ts at 8aff183be57021b533f319a29341a529be934b08.

package svg

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/lazyre"
)

// Named colors that #fff contrasts with more than #1f1f1f does, and the rest.
var (
	inkDark  = wordList("black blue blueviolet brown crimson darkblue darkcyan darkgreen darkmagenta darkolivegreen darkorchid darkred darkslateblue darkslategray darkviolet dimgray firebrick forestgreen green indigo maroon mediumblue mediumslateblue mediumvioletred midnightblue navy olive purple rebeccapurple royalblue saddlebrown seagreen sienna slateblue steelblue teal")
	inkLight = wordList("aliceblue antiquewhite aqua aquamarine azure beige bisque blanchedalmond burlywood cadetblue chartreuse chocolate coral cornflowerblue cornsilk cyan darkgoldenrod darkgray darkkhaki darkorange darksalmon darkseagreen darkturquoise deeppink deepskyblue dodgerblue floralwhite fuchsia gainsboro ghostwhite gold goldenrod gray greenyellow honeydew hotpink indianred ivory khaki lavender lavenderblush lawngreen lemonchiffon lightblue lightcoral lightcyan lightgoldenrodyellow lightgray lightgreen lightpink lightsalmon lightseagreen lightskyblue lightslategray lightsteelblue lightyellow lime limegreen linen magenta mediumaquamarine mediumorchid mediumpurple mediumseagreen mediumspringgreen mediumturquoise mintcream mistyrose moccasin navajowhite oldlace olivedrab orange orangered orchid palegoldenrod palegreen paleturquoise palevioletred papayawhip peachpuff peru pink plum powderblue red rosybrown salmon sandybrown seashell silver skyblue slategray snow springgreen tan thistle tomato turquoise violet wheat white whitesmoke yellow yellowgreen")
)

// wordList is a list of words, with a space at each end, so that a lookup
// needs no map built at init.
func wordList(words string) string { return " " + words + " " }

func listed(list, name string) bool {
	return name != "" && !strings.Contains(name, " ") && strings.Contains(list, " "+name+" ")
}

// The text colors for a fill: dark text on a light fill, light text on a dark one.
const (
	ON_LIGHT = "var(--pele-fg-on-light,#1f1f1f)"
	ON_DARK  = "var(--pele-fg-on-dark,#fff)"
)

var (
	reInkFill   = lazyre.New(`[";]fill:([^;"]*)`)
	reOpacities = lazyre.New(`[";](?:fill-)?opacity:([^;"]*)`)
	reHex       = lazyre.New(`(?i)^#(?:[\da-f]{3,4}|[\da-f]{6}|[\da-f]{8})$`)
	reFunction  = lazyre.New(`(?i)^(rgba?|hsla?)\(([^()]*)\)$`)
	reNumber    = lazyre.New(`(?i)^[+-]?(?:\d+(?:\.\d+)?|\.\d+)(?:e[+-]?\d+)?(%|deg|grad|rad|turn)?$`)
	reComma     = lazyre.New(`[` + js.S + `]*,[` + js.S + `]*`)
	reBlanks    = lazyre.New(`[` + js.S + `]+`)
)

func linear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func luminance(r, g, b float64) float64 {
	return float64(0.2126*linear(r)) + float64(0.7152*linear(g)) + float64(0.0722*linear(b))
}

func clamp01(n float64) float64 { return math.Min(math.Max(n, 0), 1) }

// Luminance where #1f1f1f and #fff have equal contrast.
var inkEven = math.Sqrt(1.05*(luminance(31.0/255, 31.0/255, 31.0/255)+0.05)) - 0.05

func amount(raw string, full float64) float64 {
	m := reNumber.FindStringSubmatch(raw)
	if m == nil || (m[1] != "" && m[1] != "%") {
		return math.NaN()
	}
	if m[1] != "" {
		full = 100
	}
	return clamp01(js.ParseFloat(raw) / full)
}

func hue(raw string) float64 {
	m := reNumber.FindStringSubmatch(raw)
	if m == nil || m[1] == "%" {
		return math.NaN()
	}
	n := js.ParseFloat(raw)
	switch strings.ToLower(m[1]) {
	case "turn":
		return n * 360
	case "rad":
		return (n * 180) / math.Pi
	case "grad":
		return n * 0.9
	}
	return n
}

// The comma syntax takes no `none`, rgb() channels all or none in %, and hsl() saturation and lightness in %.
func colorArgs(body string, hsl bool) []string {
	legacy := strings.Contains(body, ",")
	main := body
	var alpha *string
	if !legacy {
		parts := strings.Split(body, "/")
		if len(parts) > 2 {
			return nil
		}
		main = parts[0]
		if len(parts) == 2 {
			alpha = &parts[1]
		}
	}
	sep := reBlanks
	if legacy {
		sep = reComma
	}
	list := sep.Re().Split(js.Trim(main), -1)
	if legacy {
		if len(list) < 3 || len(list) > 4 {
			return nil
		}
	} else if len(list) != 3 {
		return nil
	}
	if alpha != nil {
		list = append(list, js.Trim(*alpha))
	}
	if !legacy {
		for i, c := range list {
			if c == "none" {
				list[i] = "0"
			}
		}
		return list
	}
	from := 0
	if hsl {
		from = 1
	}
	percent := 0
	for _, c := range list[from:3] {
		if strings.HasSuffix(c, "%") {
			percent++
		}
	}
	if (hsl && percent == 2) || (!hsl && (percent == 3 || percent == 0)) {
		return list
	}
	return nil
}

// Luminance and alpha; named colors use 0 or 1 to preserve the contrast choice.
func parseInkColor(raw string) (lum, alpha float64, ok bool) {
	value := strings.ToLower(js.Trim(raw))
	name := strings.Replace(value, "grey", "gray", 1)
	if listed(inkDark, name) {
		return 0, 1, true
	}
	if listed(inkLight, name) {
		return 1, 1, true
	}
	if reHex.MatchString(value) {
		digits := value[1:]
		if len(value) < 6 {
			var b strings.Builder
			for i := 0; i < len(digits); i++ {
				b.WriteByte(digits[i])
				b.WriteByte(digits[i])
			}
			digits = b.String()
		}
		n := func(i int) float64 { return js.ParseIntPrefix(digits[2*i:2*i+2], 16) / 255 }
		a := 1.0
		if len(digits) == 8 {
			a = n(3)
		}
		return luminance(n(0), n(1), n(2)), a, true
	}
	fn := reFunction.FindStringSubmatch(value)
	if fn == nil {
		return 0, 0, false
	}
	hsl := strings.HasPrefix(fn[1], "hsl")
	list := colorArgs(fn[2], hsl)
	if list == nil {
		return 0, 0, false
	}
	alpha = 1
	if len(list) == 4 {
		alpha = amount(list[3], 1)
	}
	var rgb [3]float64
	if !hsl {
		for i := range rgb {
			rgb[i] = amount(list[i], 255)
		}
	} else {
		h := math.Mod(math.Mod(hue(list[0]), 360)+360, 360) / 30
		s := amount(list[1], 100)
		l := amount(list[2], 100)
		a := s * math.Min(l, 1-l)
		for i, k := range [3]float64{0, 8, 4} {
			t := math.Mod(k+h, 12)
			rgb[i] = l - float64(a*math.Max(-1, math.Min(math.Min(t-3, 9-t), 1)))
		}
	}
	lum = luminance(rgb[0], rgb[1], rgb[2])
	if math.IsNaN(lum + alpha) {
		return 0, 0, false
	}
	return lum, alpha, true
}

// How opaque a style's fill-opacity and opacity leave its fill, or NaN when either isn't a plain number.
func styleOpacity(shape string) float64 {
	product := 1.0
	for _, m := range reOpacities.Re().FindAllStringSubmatch(shape, -1) {
		product *= amount(js.Trim(m[1]), 1)
	}
	return product
}

// Ink is the text color for a fill a style sets: ON_LIGHT or ON_DARK, or "" when the fill's
// color is unknown or below half opacity, which leaves it on an unknown background. Styles come
// from ResolveStyle.
func Ink(shape, text string) string {
	if !SetsFill(shape) {
		return ""
	}
	fill := reInkFill.FindStringSubmatch(shape)
	if fill == nil || reInkFill.MatchString(text) {
		return ""
	}
	lum, alpha, ok := parseInkColor(fill[1])
	if !ok {
		return ""
	}
	// An opacity that isn't a plain number, such as calc() or var(), may still be low.
	if !(alpha*styleOpacity(shape) >= 0.5) {
		return ""
	}
	if lum > inkEven {
		return ON_LIGHT
	}
	return ON_DARK
}

// Paints reports whether a style's fill shows. Unknown fill colors and opacities are treated as opaque.
func Paints(shape string) bool {
	if !SetsFill(shape) {
		return false
	}
	m := reInkFill.FindStringSubmatch(shape)
	if m == nil {
		return false
	}
	fill := strings.ToLower(js.Trim(m[1]))
	if fill == "" || fill == "none" || fill == "transparent" {
		return false
	}
	scale := styleOpacity(shape)
	if math.IsNaN(scale) {
		scale = 1
	}
	_, alpha, ok := parseInkColor(fill)
	if !ok {
		alpha = 1
	}
	return alpha*scale > 0
}

// InkFill is a fill attribute in the ink for a style's fill, else in fallback, else nothing.
func InkFill(style ResolvedStyle, fallback string) string {
	color := Ink(style.Shape, style.Text)
	if color == "" {
		color = fallback
	}
	if color == "" {
		return ""
	}
	return ` fill="` + color + `"`
}
