// Ported from pele src/svg/theme.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package svg

import (
	"math"
	"regexp"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// Private aliases set once on the root element, so each use is short and the public
// --pele-* tokens keep their fallbacks. The text color is set as well: an icon is drawn in
// currentColor, which would otherwise be whatever color the page gives the diagram's container.
const ROOT_STYLE = "--_bg:var(--pele-bg,#fff);--_fg:var(--pele-fg,#1f1f1f);--_m:var(--pele-muted,#6e6e6e);" +
	"--_l:var(--pele-line,#1f1f1f);--_s:var(--pele-surface,#f3f3f3);--_a:var(--pele-surface-alt,#e6e6e6);" +
	"--_b:var(--pele-border,#8a8a8a);--_c:var(--pele-accent,#5b7bd5);color:var(--_fg)"

const FONT_MONO = "var(--_fm)"
const RADIUS = "var(--pele-radius,4px)"

// The color of what a diagram marks as critical. It is not one of the series colors, as a theme
// may put those in any order.
const CRITICAL = "var(--pele-critical,#e45756)"

// A list of font names, quoted or not, and nothing that could end the declaration it is put in.
// The check is types.SafeFamilies, which the theme uses too.
func families(list string, generic string) string {
	if list != "" && types.SafeFamilies(list) {
		return list
	}
	return generic
}

// FontStyle: the fonts the labels were measured in are the ones they are drawn in, unless the page names
// others. The rest of what text inherits is pinned, as none of it was measured.
func FontStyle(size float64, family string, mono string) string {
	return ";--_fm:var(--pele-font-mono," + families(mono, "monospace") + ");font:" + js.Num(size) + "px sans-serif;" +
		"letter-spacing:normal;word-spacing:normal;text-transform:none;direction:ltr;" +
		"font-family:var(--pele-font," + families(family, "sans-serif") + ")"
}

var shapeProps = map[string]bool{
	"fill": true, "fill-opacity": true, "stroke": true, "stroke-width": true, "stroke-dasharray": true,
	"stroke-dashoffset": true, "stroke-linecap": true, "stroke-linejoin": true, "stroke-opacity": true,
	"opacity": true, "rx": true, "ry": true,
}

var textProps = map[string]bool{
	"color": true, "font-size": true, "font-family": true, "font-weight": true, "font-style": true,
	"text-decoration": true, "letter-spacing": true, "word-spacing": true, "opacity": true,
}

var (
	reUnsafe    = regexp.MustCompile(`(?i)url[` + js.S + `]*\(|expression[` + js.S + `]*\(|javascript:|[<>{};\\]|@import|image-set|/\*`)
	reImportant = regexp.MustCompile(`(?i)!important`)
	rePx        = regexp.MustCompile(`^[\d.]+(px)?$`)
)

type ResolvedStyle struct {
	Shape  string
	Line   string
	Text   string
	Stroke string
	// HasStroke tells an empty stroke from none.
	HasStroke bool
	Bold      bool
	Italic    bool
	// FontSize is 0 for none.
	FontSize float64
}

var NONE = ResolvedStyle{}

// orderedProps keeps a Map's insertion order: setting a key again keeps its place.
type orderedProps struct {
	keys []string
	vals map[string]string
}

func (o *orderedProps) set(k, v string) {
	if o.vals == nil {
		o.vals = map[string]string{}
	}
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

// ResolveStyle splits Mermaid style declarations into what applies to a shape and what applies to its text.
func ResolveStyle(declarations []string) ResolvedStyle {
	if len(declarations) == 0 {
		return NONE
	}
	var shape, txt orderedProps
	for _, decl := range declarations {
		at := strings.IndexByte(decl, ':')
		if at <= 0 {
			continue
		}
		prop := strings.ToLower(js.Trim(decl[:at]))
		value := js.Trim(reImportant.ReplaceAllString(decl[at+1:], ""))
		if value == "" || reUnsafe.MatchString(value) {
			continue
		}
		if shapeProps[prop] {
			shape.set(prop, value)
		}
		if textProps[prop] {
			if prop == "color" {
				txt.set("fill", value)
			} else {
				txt.set(prop, value)
			}
		}
	}
	if len(shape.keys) == 0 && len(txt.keys) == 0 {
		return NONE
	}
	shapeCss := ""
	lineCss := ""
	for _, prop := range shape.keys {
		value := shape.vals[prop]
		shapeCss += prop + ":" + value + ";"
		if prop != "fill" && prop != "fill-opacity" {
			lineCss += prop + ":" + value + ";"
		}
	}
	textCss := ""
	for _, prop := range txt.keys {
		textCss += prop + ":" + txt.vals[prop] + ";"
	}
	weight, hasWeight := txt.vals["font-weight"]
	size := txt.vals["font-size"]
	px := math.NaN()
	if size != "" && rePx.MatchString(size) {
		px = js.ParseFloat(size)
	}
	out := ResolvedStyle{}
	if shapeCss != "" {
		out.Shape = ` style="` + Esc(shapeCss) + `"`
	}
	if lineCss != "" {
		out.Line = ` style="` + Esc(lineCss) + `"`
	}
	if textCss != "" {
		out.Text = ` style="` + Esc(textCss) + `"`
	}
	out.Stroke, out.HasStroke = shape.vals["stroke"]
	out.Bold = hasWeight && (weight == "bold" || weight == "bolder" || js.Number(weight) >= 600)
	out.Italic = txt.vals["font-style"] == "italic"
	if !math.IsNaN(px) && px > 0 && px <= 200 {
		out.FontSize = px
	}
	return out
}

var reClass = regexp.MustCompile(`[^\w-]+`)

func ClassNames(raw string) string {
	cleaned := js.Trim(reClass.ReplaceAllString(raw, " "))
	if cleaned == "" {
		return ""
	}
	return " " + cleaned
}

var series = [8]string{"#4c78a8", "#f58518", "#54a24b", "#e45756", "#72b7b2", "#eeca3b", "#b279a2", "#9d755d"}

// SeriesColor is the color for the nth data series in a chart, cycling through eight tokens.
func SeriesColor(index int) string {
	k := ((index % 8) + 8) % 8
	return "var(--pele-series-" + js.Int(k+1) + "," + series[k] + ")"
}
