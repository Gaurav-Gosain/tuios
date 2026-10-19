package raster

import (
	"image/color"
	"math"
	"strconv"
	"strings"
	"sync"
)

// resolveVars replaces each var(--name, fallback) with the variable's value, or
// the fallback when it is not set. Values may refer to other variables; the
// depth is bounded so that a cycle ends.
func resolveVars(value string, env map[string]string) string {
	for depth := 0; depth < 8 && strings.Contains(value, "var("); depth++ {
		var b strings.Builder
		i := 0
		for {
			at := strings.Index(value[i:], "var(")
			if at < 0 {
				b.WriteString(value[i:])
				break
			}
			at += i
			b.WriteString(value[i:at])
			// Find the matching close paren.
			level := 0
			end := -1
			for k := at + 3; k < len(value); k++ {
				if value[k] == '(' {
					level++
				} else if value[k] == ')' {
					level--
					if level == 0 {
						end = k
						break
					}
				}
			}
			if end < 0 {
				b.WriteString(value[at:])
				break
			}
			inner := value[at+4 : end]
			name, fallback, hasFallback := strings.Cut(inner, ",")
			name = strings.TrimSpace(name)
			if v, ok := env[name]; ok {
				b.WriteString(v)
			} else if hasFallback {
				b.WriteString(strings.TrimSpace(fallback))
			}
			i = end + 1
		}
		value = b.String()
	}
	return value
}

// declarations splits a CSS declaration list into name, value pairs.
func declarations(style string) [][2]string {
	var out [][2]string
	level := 0
	start := 0
	add := func(decl string) {
		name, value, ok := strings.Cut(decl, ":")
		if !ok {
			return
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "!important"))
		if name != "" {
			out = append(out, [2]string{name, strings.TrimSpace(value)})
		}
	}
	for i := 0; i < len(style); i++ {
		switch style[i] {
		case '(':
			level++
		case ')':
			level--
		case ';':
			if level <= 0 {
				add(style[start:i])
				start = i + 1
			}
		}
	}
	add(style[start:])
	return out
}

// paint is a fill or stroke: none, the current colour, or a colour.
type paint struct {
	none    bool
	current bool
	c       color.NRGBA
}

func parsePaint(s string) (paint, bool) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "none":
		return paint{none: true}, true
	case "currentcolor":
		return paint{current: true}, true
	}
	c, ok := parseColor(s)
	return paint{c: c}, ok
}

func parseColor(s string) (color.NRGBA, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return color.NRGBA{}, false
	}
	if s == "transparent" {
		return color.NRGBA{}, true
	}
	if s[0] == '#' {
		return parseHex(s[1:])
	}
	if open := strings.IndexByte(s, '('); open > 0 && strings.HasSuffix(s, ")") {
		fn := strings.TrimSpace(s[:open])
		args := s[open+1 : len(s)-1]
		switch fn {
		case "rgb", "rgba":
			return parseRGB(args)
		case "hsl", "hsla":
			return parseHSL(args)
		case "color-mix":
			return parseMix(args)
		}
		return color.NRGBA{}, false
	}
	if v, ok := namedColors()[s]; ok {
		return v, true
	}
	return color.NRGBA{}, false
}

func parseHex(h string) (color.NRGBA, bool) {
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return color.NRGBA{}, false
	}
	n := func(x uint64) uint8 { return uint8(x * 17) }
	switch len(h) {
	case 3:
		return color.NRGBA{n(v >> 8 & 15), n(v >> 4 & 15), n(v & 15), 255}, true
	case 4:
		return color.NRGBA{n(v >> 12 & 15), n(v >> 8 & 15), n(v >> 4 & 15), n(v & 15)}, true
	case 6:
		return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}, true
	case 8:
		return color.NRGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}, true
	}
	return color.NRGBA{}, false
}

// splitArgs splits function arguments on commas, or on spaces and a slash.
func splitArgs(args string) []string {
	if strings.Contains(args, ",") {
		parts := strings.Split(args, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}
	return strings.Fields(strings.ReplaceAll(args, "/", " "))
}

func channel(s string, scale float64) float64 {
	if strings.HasSuffix(s, "%") {
		v, _ := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		return v / 100 * scale
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func clamp255(v float64) uint8 {
	return uint8(math.Max(0, math.Min(255, math.Round(v))))
}

func alphaArg(parts []string) uint8 {
	if len(parts) < 4 {
		return 255
	}
	return clamp255(channel(parts[3], 1) * 255)
}

func parseRGB(args string) (color.NRGBA, bool) {
	p := splitArgs(args)
	if len(p) < 3 {
		return color.NRGBA{}, false
	}
	a := alphaArg(p)
	if len(p) >= 4 && strings.HasSuffix(p[3], "%") {
		a = clamp255(channel(p[3], 255))
	}
	return color.NRGBA{clamp255(channel(p[0], 255)), clamp255(channel(p[1], 255)), clamp255(channel(p[2], 255)), a}, true
}

func parseHSL(args string) (color.NRGBA, bool) {
	p := splitArgs(args)
	if len(p) < 3 {
		return color.NRGBA{}, false
	}
	h, _ := strconv.ParseFloat(strings.TrimSuffix(p[0], "deg"), 64)
	s := channel(p[1], 1)
	l := channel(p[2], 1)
	h = math.Mod(math.Mod(h, 360)+360, 360) / 360
	hue := func(t float64) float64 {
		t = math.Mod(t+1, 1)
		q := l + s - l*s
		if l < 0.5 {
			q = l * (1 + s)
		}
		pp := 2*l - q
		switch {
		case t < 1.0/6:
			return pp + (q-pp)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return pp + (q-pp)*(2.0/3-t)*6
		}
		return pp
	}
	a := alphaArg(p)
	return color.NRGBA{clamp255(hue(h+1.0/3) * 255), clamp255(hue(h) * 255), clamp255(hue(h-1.0/3) * 255), a}, true
}

// parseMix reads color-mix(in srgb, A [p%], B [q%]).
func parseMix(args string) (color.NRGBA, bool) {
	var parts []string
	level, start := 0, 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case '(':
			level++
		case ')':
			level--
		case ',':
			if level == 0 {
				parts = append(parts, strings.TrimSpace(args[start:i]))
				start = i + 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(args[start:]))
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "in ") {
		return color.NRGBA{}, false
	}
	pick := func(s string) (color.NRGBA, float64, bool) {
		pct := -1.0
		if i := strings.LastIndexByte(s, ' '); i > 0 && strings.HasSuffix(s, "%") {
			pct, _ = strconv.ParseFloat(s[i+1:len(s)-1], 64)
			s = s[:i]
		}
		c, ok := parseColor(s)
		return c, pct, ok
	}
	a, pa, ok1 := pick(parts[1])
	b, pb, ok2 := pick(parts[2])
	if !ok1 || !ok2 {
		return color.NRGBA{}, false
	}
	switch {
	case pa < 0 && pb < 0:
		pa, pb = 50, 50
	case pa < 0:
		pa = 100 - pb
	case pb < 0:
		pb = 100 - pa
	}
	total := pa + pb
	if total <= 0 {
		return color.NRGBA{}, false
	}
	wa, wb := pa/total, pb/total
	// Mix premultiplied, as CSS does.
	aa, ab := float64(a.A)/255*wa, float64(b.A)/255*wb
	alpha := aa + ab
	if alpha == 0 {
		return color.NRGBA{}, true
	}
	mix := func(x, y uint8) uint8 { return clamp255((float64(x)*aa + float64(y)*ab) / alpha) }
	out := color.NRGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), clamp255(alpha * 255)}
	if total < 100 {
		out.A = clamp255(float64(out.A) * total / 100)
	}
	return out, true
}

var (
	namedOnce  sync.Once
	namedTable map[string]color.NRGBA
)

func namedColors() map[string]color.NRGBA {
	namedOnce.Do(buildNamed)
	return namedTable
}

func buildNamed() {
	t := map[string]color.NRGBA{}
	for _, pair := range strings.Split(cssNames, ",") {
		name, hex, _ := strings.Cut(pair, ":")
		c, _ := parseHex(hex)
		t[name] = c
	}
	namedTable = t
}

// The CSS named colours.
const cssNames = "aliceblue:f0f8ff,antiquewhite:faebd7,aqua:00ffff,aquamarine:7fffd4,azure:f0ffff,beige:f5f5dc,bisque:ffe4c4,black:000000," +
	"blanchedalmond:ffebcd,blue:0000ff,blueviolet:8a2be2,brown:a52a2a,burlywood:deb887,cadetblue:5f9ea0,chartreuse:7fff00," +
	"chocolate:d2691e,coral:ff7f50,cornflowerblue:6495ed,cornsilk:fff8dc,crimson:dc143c,cyan:00ffff,darkblue:00008b,darkcyan:008b8b," +
	"darkgoldenrod:b8860b,darkgray:a9a9a9,darkgreen:006400,darkgrey:a9a9a9,darkkhaki:bdb76b,darkmagenta:8b008b,darkolivegreen:556b2f," +
	"darkorange:ff8c00,darkorchid:9932cc,darkred:8b0000,darksalmon:e9967a,darkseagreen:8fbc8f,darkslateblue:483d8b,darkslategray:2f4f4f," +
	"darkslategrey:2f4f4f,darkturquoise:00ced1,darkviolet:9400d3,deeppink:ff1493,deepskyblue:00bfff,dimgray:696969,dimgrey:696969," +
	"dodgerblue:1e90ff,firebrick:b22222,floralwhite:fffaf0,forestgreen:228b22,fuchsia:ff00ff,gainsboro:dcdcdc,ghostwhite:f8f8ff," +
	"gold:ffd700,goldenrod:daa520,gray:808080,green:008000,greenyellow:adff2f,grey:808080,honeydew:f0fff0,hotpink:ff69b4,indianred:cd5c5c," +
	"indigo:4b0082,ivory:fffff0,khaki:f0e68c,lavender:e6e6fa,lavenderblush:fff0f5,lawngreen:7cfc00,lemonchiffon:fffacd,lightblue:add8e6," +
	"lightcoral:f08080,lightcyan:e0ffff,lightgoldenrodyellow:fafad2,lightgray:d3d3d3,lightgreen:90ee90,lightgrey:d3d3d3,lightpink:ffb6c1," +
	"lightsalmon:ffa07a,lightseagreen:20b2aa,lightskyblue:87cefa,lightslategray:778899,lightslategrey:778899,lightsteelblue:b0c4de," +
	"lightyellow:ffffe0,lime:00ff00,limegreen:32cd32,linen:faf0e6,magenta:ff00ff,maroon:800000,mediumaquamarine:66cdaa,mediumblue:0000cd," +
	"mediumorchid:ba55d3,mediumpurple:9370db,mediumseagreen:3cb371,mediumslateblue:7b68ee,mediumspringgreen:00fa9a,mediumturquoise:48d1cc," +
	"mediumvioletred:c71585,midnightblue:191970,mintcream:f5fffa,mistyrose:ffe4e1,moccasin:ffe4b5,navajowhite:ffdead,navy:000080," +
	"oldlace:fdf5e6,olive:808000,olivedrab:6b8e23,orange:ffa500,orangered:ff4500,orchid:da70d6,palegoldenrod:eee8aa,palegreen:98fb98," +
	"paleturquoise:afeeee,palevioletred:db7093,papayawhip:ffefd5,peachpuff:ffdab9,peru:cd853f,pink:ffc0cb,plum:dda0dd,powderblue:b0e0e6," +
	"purple:800080,rebeccapurple:663399,red:ff0000,rosybrown:bc8f8f,royalblue:4169e1,saddlebrown:8b4513,salmon:fa8072,sandybrown:f4a460," +
	"seagreen:2e8b57,seashell:fff5ee,sienna:a0522d,silver:c0c0c0,skyblue:87ceeb,slateblue:6a5acd,slategray:708090,slategrey:708090," +
	"snow:fffafa,springgreen:00ff7f,steelblue:4682b4,tan:d2b48c,teal:008080,thistle:d8bfd8,tomato:ff6347,turquoise:40e0d0,violet:ee82ee," +
	"wheat:f5deb3,white:ffffff,whitesmoke:f5f5f5,yellow:ffff00,yellowgreen:9acd32"
