// Ported from pele src/svg/edges.ts at 8aff183be57021b533f319a29341a529be934b08: the checks of
// an edge against the fills a diagram's styles set, and the casing under an edge that crosses one.

package svg

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/lazyre"
)

// pathTokens reads a path's d. Each command stores its character code, argument count, and arguments.
func pathTokens(d string) []float64 {
	at := func(i int) int {
		if i < 0 || i >= len(d) {
			return -1
		}
		return int(d[i])
	}
	var out []float64
	cmd := -1
	for i := 0; i < len(d); {
		c := at(i)
		if c >= 65 && c <= 90 && c != 69 {
			cmd = len(out) + 1
			out = append(out, float64(c), 0)
			i++
		} else if c == 45 || c == 46 || (c >= 48 && c <= 57) {
			// Scale exact integers to match Number() rounding; otherwise use Number().
			j := i
			if c == 45 {
				j = i + 1
			}
			m := 0.0
			places := -1
			for k := at(j); (k >= 48 && k <= 57) || (k == 46 && places < 0); k = at(j) {
				if k == 46 {
					places = 0
				} else {
					m = m*10 + float64(k-48)
					if places >= 0 {
						places++
					}
				}
				j++
			}
			var v float64
			if at(j)|32 == 101 || m > 9007199254740991 || places > 22 {
				for k := at(j); k == 46 || k == 101 || k == 69 || (k >= 48 && k <= 57) || ((k == 45 || k == 43) && at(j-1)|32 == 101); k = at(j) {
					j++
				}
				v = js.Number(d[i:j])
			} else {
				v = m
				if places > 0 {
					v = m / math.Pow10(places)
				}
				if c == 45 {
					v = -v
				}
			}
			if cmd >= 0 {
				out = append(out, v)
				out[cmd]++
			}
			i = j
		} else {
			i++
		}
	}
	return out
}

// samplePath samples the curves and arcs of tokenized path data; NaN separates subpaths.
func samplePath(tokens []float64) []float64 {
	var out []float64
	x, y := 0.0, 0.0
	for i := 0; i < len(tokens); {
		cmd := tokens[i]
		count := int(tokens[i+1])
		s := i + 2
		e := s + count
		i = e
		switch cmd {
		case 'H', 'V':
			for k := s; k < e; k++ {
				if cmd == 'H' {
					x = tokens[k]
				} else {
					y = tokens[k]
				}
				out = append(out, x, y)
			}
		case 'C', 'Q':
			cubic := cmd == 'C'
			step := 4
			if cubic {
				step = 6
			}
			for k := s; k+step <= e; k += step {
				x1, y1, x2, y2 := tokens[k], tokens[k+1], tokens[k+2], tokens[k+3]
				x3, y3 := x2, y2
				if cubic {
					x3, y3 = tokens[k+4], tokens[k+5]
				}
				for t := 0.125; t <= 1; t += 0.125 {
					// Preserve the original De Casteljau operation order to keep rounding unchanged.
					ax := x + float64((x1-x)*t)
					ay := y + float64((y1-y)*t)
					bx := x1 + float64((x2-x1)*t)
					by := y1 + float64((y2-y1)*t)
					if cubic {
						cx := x2 + float64((x3-x2)*t)
						cy := y2 + float64((y3-y2)*t)
						ax += float64((bx - ax) * t)
						ay += float64((by - ay) * t)
						bx += float64((cx - bx) * t)
						by += float64((cy - by) * t)
					}
					out = append(out, ax+float64((bx-ax)*t), ay+float64((by-ay)*t))
				}
				x, y = x3, y3
			}
		case 'A':
			for k := s; k+7 <= e; k += 7 {
				out = sampleArc(out, x, y, tokens[k], tokens[k+1], tokens[k+3], tokens[k+4], tokens[k+5], tokens[k+6])
				x, y = tokens[k+5], tokens[k+6]
			}
		default:
			for k := s; k+2 <= e; k += 2 {
				if cmd == 'M' && k == s && len(out) > 0 {
					out = append(out, math.NaN(), math.NaN())
				}
				x, y = tokens[k], tokens[k+1]
				out = append(out, x, y)
			}
		}
	}
	return out
}

// sampleArc samples an unrotated arc from (x1, y1), after SVG's conversion from end points to a center.
func sampleArc(out []float64, x1, y1, rx, ry, large, sweep, x2, y2 float64) []float64 {
	hx := (x1 - x2) / 2
	hy := (y1 - y2) / 2
	if rx == 0 || ry == 0 || (hx == 0 && hy == 0) {
		return append(out, x2, y2)
	}
	scale := math.Max(1, math.Sqrt(float64(hx*hx)/float64(rx*rx)+float64(hy*hy)/float64(ry*ry)))
	rx *= scale
	ry *= scale
	sign := 1.0
	if large == sweep {
		sign = -1
	}
	k := sign * math.Sqrt(math.Max(0, (float64(rx*rx*ry*ry)-float64(rx*rx*hy*hy)-float64(ry*ry*hx*hx))/(float64(rx*rx*hy*hy)+float64(ry*ry*hx*hx))))
	cx := (k*rx*hy)/ry + (x1+x2)/2
	cy := (-k*ry*hx)/rx + (y1+y2)/2
	from := js.Atan2((y1-cy)/ry, (x1-cx)/rx)
	turn := js.Atan2((y2-cy)/ry, (x2-cx)/rx) - from
	if sweep != 0 && turn < 0 {
		turn += 2 * math.Pi
	} else if sweep == 0 && turn > 0 {
		turn -= 2 * math.Pi
	}
	for i := 1; i <= 8; i++ {
		a := from + (turn*float64(i))/8
		out = append(out, cx+float64(rx*math.Cos(a)), cy+float64(ry*math.Sin(a)))
	}
	return out
}

// clips tests segment overlap with the box using Liang-Barsky clipping.
func clips(x, y, dx, dy, l, t, r, b float64) bool {
	lo, hi := 0.0, 1.0
	for side := 0; side < 4; side++ {
		var p, q float64
		switch side {
		case 0:
			p, q = -dx, x-l
		case 1:
			p, q = dx, r-x
		case 2:
			p, q = -dy, y-t
		default:
			p, q = dy, b-y
		}
		if p == 0 {
			if q < 0 {
				hi = -1
			}
		} else if p < 0 {
			lo = jsMax(lo, q/p)
		} else {
			hi = jsMin(hi, q/p)
		}
	}
	return lo < hi
}

// jsMax and jsMin are Math.max and Math.min of two numbers: NaN wins.
func jsMax(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Max(a, b)
}

func jsMin(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Min(a, b)
}

func enters(points []float64, l, t, r, b float64) bool {
	for k := 0; k+2 < len(points); k += 2 {
		x, y := points[k], points[k+1]
		x2, y2 := points[k+2], points[k+3]
		if (x < l && x2 < l) || (x > r && x2 > r) || (y < t && y2 < t) || (y > b && y2 > b) {
			continue
		}
		if clips(x, y, x2-x, y2-y, l, t, r, b) {
			return true
		}
	}
	return false
}

// Fill is a box a style fills: its bounds, [left, top, right, bottom], and the shape style.
type Fill struct {
	Box   [4]float64
	Shape string
}

// FillUnder finds the innermost fill holding the whole edge and its marker tips. It gives the
// fill and true; nil and true when the edge crosses a fill's border; nil and false off fills.
func FillUnder(d string, tips []float64, fills []Fill, tokens []float64) (*Fill, bool) {
	if len(fills) == 0 {
		return nil, false
	}
	if tokens == nil {
		tokens = pathTokens(d)
	}
	points := samplePath(tokens)
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for k := 0; k < len(points)+len(tips); k += 2 {
		var x, y float64
		if k < len(points) {
			x, y = points[k], points[k+1]
		} else {
			x, y = tips[k-len(points)], tips[k+1-len(points)]
		}
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}
	var holder *Fill
	least := math.Inf(1)
	for i := range fills {
		fill := &fills[i]
		l, t, r, b := fill.Box[0], fill.Box[1], fill.Box[2], fill.Box[3]
		if maxX < l+0.5 || minX > r-0.5 || maxY < t+0.5 || minY > b-0.5 {
			continue
		}
		touched := enters(points, l+0.5, t+0.5, r-0.5, b-0.5)
		for k := 0; k < len(tips) && !touched; k += 2 {
			touched = tips[k] > l+0.5 && tips[k] < r-0.5 && tips[k+1] > t+0.5 && tips[k+1] < b-0.5
		}
		if !touched {
			continue
		}
		if minX < l || maxX > r || minY < t || maxY > b {
			return nil, true
		}
		area := (r - l) * (b - t)
		if area < least {
			least = area
			holder = fill
		}
	}
	return holder, holder != nil
}

var (
	reCaseWidth = lazyre.New(`^[\d.]+(?:px)?$`)
)

// caseDecl finds the value of a declaration `[";]prop:value;` in a line style.
func caseDecl(line, prop string) (string, bool) {
	for i := 0; ; {
		at := strings.Index(line[i:], prop+":")
		if at < 0 {
			return "", false
		}
		at += i
		i = at + 1
		if at == 0 || (line[at-1] != '"' && line[at-1] != ';') {
			continue
		}
		start := at + len(prop) + 1
		end := start
		for end < len(line) && line[end] != ';' && line[end] != '"' {
			end++
		}
		// [^;"]+ then ;
		if end == start || end >= len(line) || line[end] != ';' {
			continue
		}
		return line[start:end], true
	}
}

// EdgeInk is the fill's ink for an edge on it, or "" for a casing when unknown or when the line
// style sets a stroke.
func EdgeInk(fill *Fill, line string) string {
	if Declares(line, "stroke") {
		return ""
	}
	return Ink(fill.Shape, "")
}

// HollowOn gives markers with the page color inside them the fill's color there instead.
func HollowOn(markers string, fill *Fill) string {
	m := reInkFill.FindStringSubmatch(fill.Shape)
	if m == nil {
		return markers
	}
	color := js.Trim(m[1])
	if color == "" {
		return markers
	}
	return strings.ReplaceAll(markers, ` fill="var(--_bg)"`, ` fill="`+color+`"`)
}

// OverFills is the ink of the innermost fill that holds the whole edge, "" for a casing where it
// crosses a border; ok is false off fills.
func OverFills(d string, tips []float64, fills []Fill, line string, tokens []float64) (ink string, ok bool) {
	fill, ok := FillUnder(d, tips, fills, tokens)
	if fill == nil {
		return "", ok
	}
	return EdgeInk(fill, line), true
}

const caseWidth = 4

// Casing is a wider page-colored stroke under an edge, as faint as it; markers drop their class
// so marker styles skip it.
func Casing(d, markers, line string, thick bool) string {
	decl := func(prop string) (string, bool) {
		if !strings.Contains(line, prop) {
			return "", false
		}
		value, ok := caseDecl(line, prop)
		if !ok {
			return "", false
		}
		return js.Trim(value), true
	}
	if stroke, ok := decl("stroke"); ok && (stroke == "none" || stroke == "transparent") {
		return ""
	}
	width, hasWidth := "2.5", true
	if !thick {
		width, hasWidth = decl("stroke-width")
	}
	attrs := ""
	style := ""
	if hasWidth && reCaseWidth.MatchString(width) {
		if w := js.ParseFloat(width); w != 1 {
			attrs = ` stroke-width="` + Num(w+caseWidth) + `"`
		}
	} else if hasWidth {
		style += "stroke-width:calc(" + width + " + " + js.Int(caseWidth) + "px);"
	}
	for _, prop := range [...]string{"opacity", "stroke-opacity"} {
		if value, ok := decl(prop); ok {
			style += prop + ":" + value + ";"
		}
	}
	body := `<path d="` + d + `"/>` + strings.ReplaceAll(markers, ` class="pele-marker"`, "")
	if attrs == "" && style == "" {
		return body
	}
	if style != "" {
		style = ` style="` + style + `"`
	}
	return "<g" + attrs + style + ">" + body + "</g>"
}

// EdgeCasing is the casing of an edge path and its markers.
func EdgeCasing(path EdgePath, startType, endType, line string, thick bool) string {
	return Casing(
		path.D,
		MarkerStroke(startType, path.Sx, path.Sy, path.Sdx, path.Sdy, "var(--_bg)", "var(--_bg)")+
			MarkerStroke(endType, path.Ex, path.Ey, path.Edx, path.Edy, "var(--_bg)", "var(--_bg)"),
		line,
		thick,
	)
}

// CasingsSvg is the group that holds the casings, drawn under the edges.
func CasingsSvg(casings string) string {
	if casings == "" {
		return ""
	}
	return `<g class="pele-casings" fill="none" stroke="var(--_bg)" stroke-width="` + js.Int(1+caseWidth) +
		`" stroke-linecap="round" stroke-linejoin="round" pointer-events="none">` + casings + "</g>"
}
