package shot

import "fmt"

// Format is an output backend.
type Format string

const (
	FormatPNG  Format = "png"
	FormatSVG  Format = "svg"
	FormatANSI Format = "ansi"
	FormatHTML Format = "html"
	FormatText Format = "txt"
)

// Formats lists every backend, default first.
var Formats = []string{"png", "svg", "ansi", "html", "txt"}

// ParseFormat resolves a format name, accepting the extension spellings.
func ParseFormat(s string) (Format, bool) {
	switch s {
	case "png":
		return FormatPNG, true
	case "svg":
		return FormatSVG, true
	case "ansi", "ans":
		return FormatANSI, true
	case "html", "htm":
		return FormatHTML, true
	case "txt", "text":
		return FormatText, true
	}
	return "", false
}

// Ext is the file extension for a format, without the dot.
func (f Format) Ext() string {
	if f == FormatANSI {
		return "ans"
	}
	return string(f)
}

// MediaType is the MIME type of a format's output.
func (f Format) MediaType() string {
	switch f {
	case FormatPNG:
		return "image/png"
	case FormatSVG:
		return "image/svg+xml"
	case FormatHTML:
		return "text/html"
	default:
		return "text/plain"
	}
}

// Render renders the grid in the given format. The frame applies to the
// pixel formats and HTML; ANSI and text are frameless by nature. The
// decorations slice is the annotation hook from the design's section 13 and
// is empty everywhere today.
func Render(format Format, g *Grid, f *Frame, decorations []Decoration) ([]byte, error) {
	if g == nil || g.Cols <= 0 || g.Rows <= 0 {
		return nil, fmt.Errorf("empty grid")
	}
	switch format {
	case FormatPNG:
		return RenderPNG(g, f, decorations)
	case FormatSVG:
		return RenderSVG(g, f, decorations), nil
	case FormatANSI:
		return RenderANSI(g), nil
	case FormatHTML:
		return RenderHTML(g, f, decorations), nil
	case FormatText:
		return RenderText(g), nil
	}
	return nil, fmt.Errorf("unknown format %q", format)
}
