package shot

import (
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// Color resolution. The emulator preserves color kinds (basic, indexed,
// truecolor), so this is the one place they become concrete RGB: themed
// sessions resolve through the theme palette, unthemed sessions fall back to
// the xterm-256 table.
//
// Why the fallback renders at all instead of refusing: tuios can never read
// the host terminal's palette, so with no theme set every choice is a guess,
// and the docs already say only the host can settle it. Between refusing to
// render, demanding a theme first, and guessing the way every terminal
// tool guesses, the guess is the only option that keeps "works with no
// setup" true: the xterm table is the documented default of the reference
// implementation, truecolor cells (most modern TUIs) are exact regardless,
// and the render result carries a one-line notice naming the guess so
// nobody mistakes it for the host's colors. A --theme flag re-renders
// indexed cells through any registered palette when the guess is not good
// enough.

// Palette resolves preserved color kinds to concrete RGB.
type Palette struct {
	// FG and BG are the default foreground and background.
	FG, BG Color
	// ANSI is the basic 16.
	ANSI [16]Color
	// Indexed overrides 256-color lookups (an emulator's OSC 4 state);
	// nil falls through to ANSI and the xterm cube.
	Indexed func(i int) (Color, bool)
}

// XTermFg and XTermBg are the documented no-theme defaults: the xterm
// reference palette's own.
var (
	XTermFg = RGB(0xe5, 0xe5, 0xe5)
	XTermBg = RGB(0x00, 0x00, 0x00)
)

// xtermBasic is the xterm reference palette for the basic 16.
var xtermBasic = [16]Color{
	RGB(0x00, 0x00, 0x00), RGB(0xcd, 0x00, 0x00), RGB(0x00, 0xcd, 0x00), RGB(0xcd, 0xcd, 0x00),
	RGB(0x00, 0x00, 0xee), RGB(0xcd, 0x00, 0xcd), RGB(0x00, 0xcd, 0xcd), RGB(0xe5, 0xe5, 0xe5),
	RGB(0x7f, 0x7f, 0x7f), RGB(0xff, 0x00, 0x00), RGB(0x00, 0xff, 0x00), RGB(0xff, 0xff, 0x00),
	RGB(0x5c, 0x5c, 0xff), RGB(0xff, 0x00, 0xff), RGB(0x00, 0xff, 0xff), RGB(0xff, 0xff, 0xff),
}

// XTermPalette is the guess a render falls back to when no theme is set.
func XTermPalette() *Palette {
	return &Palette{FG: XTermFg, BG: XTermBg, ANSI: xtermBasic}
}

// xterm256 is the standard 256-color mapping above the basic 16: the 6x6x6
// cube, then the grayscale ramp.
func xterm256(i int) Color {
	switch {
	case i < 0 || i > 255:
		return Color{}
	case i < 16:
		return xtermBasic[i]
	case i < 232:
		i -= 16
		levels := [6]uint8{0, 95, 135, 175, 215, 255}
		return RGB(levels[i/36], levels[i/6%6], levels[i%6])
	default:
		v := uint8(8 + (i-232)*10)
		return RGB(v, v, v)
	}
}

// Resolve turns any color the emulator can hold into concrete RGB. def is
// what nil (no color set) means for this position.
func (p *Palette) Resolve(c color.Color, def Color) Color {
	switch v := c.(type) {
	case nil:
		return def
	case ansi.BasicColor:
		if int(v) < 16 {
			return p.ANSI[v]
		}
		return def
	case ansi.IndexedColor:
		if p.Indexed != nil {
			if rc, ok := p.Indexed(int(v)); ok {
				return rc
			}
		}
		if int(v) < 16 {
			return p.ANSI[v]
		}
		return xterm256(int(v))
	default:
		r, g, b, a := c.RGBA()
		if a == 0 {
			return def
		}
		return RGB(uint8(r>>8), uint8(g>>8), uint8(b>>8))
	}
}

// MakeCell resolves one emulator cell into a grid cell: colors made
// concrete, reverse video folded into them, conceal blanked.
func MakeCell(content string, width int, style uv.Style, link uv.Link, p *Palette) Cell {
	fg := p.Resolve(style.Fg, p.FG)
	bg := p.Resolve(style.Bg, p.BG)
	bgDefault := style.Bg == nil
	attrs := style.Attrs
	if attrs&uv.AttrReverse != 0 {
		fg, bg = bg, fg
		bgDefault = false
	}
	if attrs&uv.AttrConceal != 0 {
		content = ""
	}
	c := Cell{
		Cluster:   strings.TrimRight(content, "\x00"),
		Width:     uint8(max(0, min(width, 2))),
		FG:        fg,
		BG:        bg,
		BGDefault: bgDefault,
		Bold:      attrs&uv.AttrBold != 0,
		Faint:     attrs&uv.AttrFaint != 0,
		Italic:    attrs&uv.AttrItalic != 0,
		Strike:    attrs&uv.AttrStrikethrough != 0,
		Link:      link.URL,
	}
	if c.Cluster == " " {
		c.Cluster = ""
	}
	switch style.Underline {
	case ansi.UnderlineSingle:
		c.Underline = UnderlineSingle
	case ansi.UnderlineDouble:
		c.Underline = UnderlineDouble
	case ansi.UnderlineCurly:
		c.Underline = UnderlineCurly
	case ansi.UnderlineDotted:
		c.Underline = UnderlineDotted
	case ansi.UnderlineDashed:
		c.Underline = UnderlineDashed
	}
	return c
}

// ReverseCursor folds the cursor into the grid by swapping the cell's
// colors, the way a block cursor reads.
func (g *Grid) ReverseCursor(x, y int) {
	if y < 0 || y >= g.Rows || x < 0 || x >= g.Cols {
		return
	}
	c := &g.Cells[y][x]
	c.FG, c.BG = c.BG, c.FG
	c.BGDefault = false
}

// FrameSpec is the raw configuration of a frame, before theme derivation.
// Strings use the config spellings so config and CLI hand them over as-is.
type FrameSpec struct {
	Frame      string // window, plain, none
	Background string // auto, none, hex, hex..hex
	Padding    int
	Radius     int
	Shadow     bool
	Controls   string // auto, macos, glyphs, none
	Title      string
	FontFamily string
	FontData   []byte
	Scale      int
}

// FrameInputs is what the environment contributes: the resolved palette,
// accent candidates for the wash and controls, and the glyph-set window
// marks.
type FrameInputs struct {
	Palette *Palette
	// Accents seed the auto wash, best first. Empty falls back to the
	// palette's blue and magenta.
	Accents []Color
	// Close, Minimize, Maximize are the glyph-set marks for controls glyphs.
	Close, Minimize, Maximize string
}

// BuildFrame resolves a spec against its inputs into a concrete Frame.
func BuildFrame(spec FrameSpec, in FrameInputs) *Frame {
	p := in.Palette
	if p == nil {
		p = XTermPalette()
	}
	f := &Frame{
		Padding:       spec.Padding,
		Radius:        spec.Radius,
		Shadow:        spec.Shadow,
		Title:         spec.Title,
		FontFamily:    spec.FontFamily,
		FontData:      spec.FontData,
		Scale:         spec.Scale,
		CloseGlyph:    in.Close,
		MinimizeGlyph: in.Minimize,
		MaximizeGlyph: in.Maximize,
	}
	switch spec.Frame {
	case "none":
		f.Mode = FrameNone
	case "plain":
		f.Mode = FramePlain
	default:
		f.Mode = FrameWindow
	}
	switch spec.Controls {
	case "macos":
		f.Controls = ControlsMacOS
	case "glyphs":
		f.Controls = ControlsGlyphs
	case "none":
		f.Controls = ControlsNone
	default:
		f.Controls = ControlsDots
	}
	accents := in.Accents
	if len(accents) == 0 {
		accents = []Color{p.ANSI[4], p.ANSI[5]}
	}
	f.Accent = accents[0]
	switch {
	case spec.Background == "none":
		f.Transparent = true
	case spec.Background == "" || spec.Background == "auto":
		f.WashStart, f.WashEnd = DeriveWash(p.BG, accents)
	case strings.Contains(spec.Background, ".."):
		parts := strings.SplitN(spec.Background, "..", 2)
		a, okA := ParseHex(parts[0])
		b, okB := ParseHex(parts[1])
		if okA && okB {
			f.WashStart, f.WashEnd = a, b
		} else {
			f.WashStart, f.WashEnd = DeriveWash(p.BG, accents)
		}
	default:
		if c, ok := ParseHex(spec.Background); ok {
			f.WashStart, f.WashEnd = c, c
		} else {
			f.WashStart, f.WashEnd = DeriveWash(p.BG, accents)
		}
	}
	return f
}
