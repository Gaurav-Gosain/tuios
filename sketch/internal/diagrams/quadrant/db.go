// Ported from pele src/diagrams/quadrant/db.ts at 8aff183be57021b533f319a29341a529be934b08.

package quadrant

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

type QuadrantText struct {
	text string
	// "text" or "markdown".
	typ string
}

// PointStyle holds the styles a point or a class sets. A field left unset is nil.
type PointStyle struct {
	radius      *float64
	color       *string
	strokeColor *string
	strokeWidth *string
}

type QuadrantPoint struct {
	PointStyle
	x, y      float64
	text      QuadrantText
	className string
}

type QuadrantDb struct {
	title    string
	accTitle string
	accDescr string
	// Quadrants 1 to 4: top right, top left, bottom left, bottom right.
	quadrants   [4]*QuadrantText
	xAxisLeft   *QuadrantText
	xAxisRight  *QuadrantText
	yAxisBottom *QuadrantText
	yAxisTop    *QuadrantText
	points      []QuadrantPoint
	classes     map[string]PointStyle
}

func newQuadrantDb() *QuadrantDb {
	return &QuadrantDb{classes: map[string]PointStyle{}}
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isHexDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// RE_NUMBER is /^\d+$/.
func RE_NUMBER(s string) bool { return digitsOnly(s) }

// RE_HEX is /^#?([\dA-Fa-f]{6}|[\dA-Fa-f]{3})$/.
func RE_HEX(s string) bool {
	s = strings.TrimPrefix(s, "#")
	return (len(s) == 6 || len(s) == 3) && isHexDigits(s)
}

// RE_PIXELS is /^\d+px$/.
func RE_PIXELS(s string) bool {
	return strings.HasSuffix(s, "px") && digitsOnly(s[:len(s)-2])
}

func invalid(style, value, kind string) {
	types.Throw("value for "+style+" "+value+" is invalid, please use a valid "+kind, types.CodeSemantic, types.ErrorInfo{Type: "quadrantChart"})
}

// keyValue gives the first two parts of Mermaid's `style.trim().split(/\s*:\s*/)`, found in one pass.
func keyValue(style string) (string, string) {
	s := js.Trim(style)
	colon := strings.IndexByte(s, ':')
	if colon == -1 {
		return s, "undefined"
	}
	rest := js.TrimStart(s[colon+1:])
	next := strings.IndexByte(rest, ':')
	if next == -1 {
		return js.TrimEnd(s[:colon]), rest
	}
	return js.TrimEnd(s[:colon]), js.TrimEnd(rest[:next])
}

func parseStyles(styles []string) PointStyle {
	var out PointStyle
	for _, style := range styles {
		key, value := keyValue(style)
		switch key {
		case "radius":
			if !RE_NUMBER(value) {
				invalid(key, value, "number")
			}
			out.radius = types.Ptr(js.ParseIntPrefix(value, 10))
		case "color":
			if !RE_HEX(value) {
				invalid(key, value, "hex code")
			}
			out.color = types.Ptr(value)
		case "stroke-color":
			if !RE_HEX(value) {
				invalid(key, value, "hex code")
			}
			out.strokeColor = types.Ptr(value)
		case "stroke-width":
			if !RE_PIXELS(value) {
				invalid(key, value, "number of pixels (eg. 10px)")
			}
			out.strokeWidth = types.Ptr(value)
		default:
			types.Throw("style named "+key+" is not supported.", types.CodeSemantic, types.ErrorInfo{Type: "quadrantChart"})
		}
	}
	return out
}

func trimmed(label QuadrantText) *QuadrantText {
	return &QuadrantText{text: js.Trim(label.text), typ: label.typ}
}

func (db *QuadrantDb) setQuadrantText(k int, label QuadrantText) { db.quadrants[k] = trimmed(label) }

func (db *QuadrantDb) setXAxisLeftText(label QuadrantText) { db.xAxisLeft = trimmed(label) }

func (db *QuadrantDb) setXAxisRightText(label QuadrantText) { db.xAxisRight = trimmed(label) }

func (db *QuadrantDb) setYAxisBottomText(label QuadrantText) { db.yAxisBottom = trimmed(label) }

func (db *QuadrantDb) setYAxisTopText(label QuadrantText) { db.yAxisTop = trimmed(label) }

func (db *QuadrantDb) addPoint(label QuadrantText, className, x, y string, styles []string) {
	db.points = append(db.points, QuadrantPoint{
		x:          js.Number(x),
		y:          js.Number(y),
		text:       *trimmed(label),
		className:  className,
		PointStyle: parseStyles(styles),
	})
}

func (db *QuadrantDb) addClass(className string, styles []string) {
	db.classes[className] = parseStyles(styles)
}

func (db *QuadrantDb) setDiagramTitle(text string) { db.title = text }

func (db *QuadrantDb) setAccTitle(text string) { db.accTitle = js.TrimStart(text) }

func (db *QuadrantDb) setAccDescription(text string) { db.accDescr = joinLines(text) }

// joinLines is text.replace(/\n\s+/g, '\n').
func joinLines(text string) string {
	if !strings.Contains(text, "\n") {
		return text
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\n' {
			out.WriteByte(text[i])
			i++
			continue
		}
		j := i + 1
		for j < len(text) {
			c, size := utf8.DecodeRuneInString(text[j:])
			if !js.IsSpace(c) {
				break
			}
			j += size
		}
		out.WriteByte('\n')
		i = j
	}
	return out.String()
}
