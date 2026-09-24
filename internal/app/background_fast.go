package app

import (
	"image"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The backgrounds on the fullscreen fast path.
//
// The compositor paints a ground on the cells a layer parses to (see
// background.go). The fast path builds no layer: it hands the pane's box and
// the dock to the host as one string, and parsing that string into cells to
// paint them is most of what the fast path exists to skip. So here the paint
// goes into the string instead. The frame's text and its own escape sequences
// are copied through, and a colour SGR is inserted in front of any text that
// is about to be printed with no background (or no foreground) of its own,
// naming the ground of the surface that text lands on. A cell that sets its
// own colour keeps it, because the pen already holds that colour and nothing
// is inserted. The one sequence rewritten is a bare reset while a painted
// colour is in force, which becomes a reset of everything but that colour
// (see resetKeepBoth), so the ground is not set again after every styled run.
//
// That is the compositor's rule stated on the pen rather than on the cell: a
// cell parsed from the painted string has the ground exactly where the cell
// parsed from the plain string had no colour, which is where paintGround would
// have put it. TestFastPathPaintMatchesTheCompositor holds the two paths to
// the same cells.
//
// Which surface a character lands on is decided by line and by position in the
// line, without measuring anything. The frame is the pane's box, which is its
// title row, one row per content row, and its bottom row, plus the dock's rows
// above or below it. A title, bottom or dock row is one surface from end to
// end. A content row of a bordered pane is its left border glyph, the pane's
// content, and its right border glyph: the first character on the row and the
// last are chrome, and everything between them is pane. Both border sides are
// one rune (borderSideCells declines any other shape, and lipgloss draws the
// same), so no width is ever needed.
//
// The colour sequences are formatted once, when a ground is resolved, and the
// buffer the frame is painted into is kept across frames, so the paint
// allocates nothing per frame beyond the frame string itself, which the plain
// fast path allocates too.

// fastPainter paints the fast path's frame. It is kept on the OS so its buffer
// and parser are reused from frame to frame.
type fastPainter struct {
	out    []byte
	parser *ansi.Parser
	// fgNil and bgNil are the source pen: whether the frame as written has no
	// foreground or background set at this point. The pen runs on across line
	// ends, the way the host parses the frame.
	fgNil, bgNil bool
	// fg and bg are the colour sequence this painter last inserted and that is
	// still in force, or empty while the emitted pen is the source's own.
	fg, bg string
}

// fastPaintFits reports whether the fast path can paint window: its content
// rectangle has to be either the whole box or the box less a one cell frame.
// Every pane the fast path takes is one or the other; this is the guard that
// keeps a shape nobody draws today from being painted wrongly.
func fastPaintFits(window *terminal.Window) bool {
	r := paneContentRect(window).Sub(image.Pt(window.X, window.Y))
	switch {
	case r.Min.X == 0 && r.Max.X == window.Width:
		return true
	case r.Min.X == 1 && r.Max.X == window.Width-1 && window.Width >= 2:
		return true
	}
	return false
}

// paintFullscreenFrame builds the fast path's frame with the pane, chrome and
// dock grounds painted in. box is the pane's box as renderWindowBox drew it,
// dock the dock's rows, placed by dockPos ("top", "bottom" or "hidden").
func (m *OS) paintFullscreenFrame(window *terminal.Window, box, dock, dockPos string) string {
	pane := m.surfaceGround(surfacePane)
	chrome := m.surfaceGround(surfaceChrome)
	dockGround := m.surfaceGround(surfaceDock)
	rect := paneContentRect(window).Sub(image.Pt(window.X, window.Y))

	p := &m.fastPaint
	if p.parser == nil {
		// The same buffers ultraviolet's pooled parser has, which is the one
		// the host parses the frame with.
		p.parser = ansi.NewParser()
		p.parser.SetDataSize(4 * 1024)
	}
	p.out = p.out[:0]
	p.fgNil, p.bgNil = true, true
	p.fg, p.bg = "", ""

	if dockPos == "top" {
		p.block(dock, &dockGround)
		p.out = append(p.out, '\n')
	}
	row := 0
	for line := range strings.SplitSeq(box, "\n") {
		if row > 0 {
			p.out = append(p.out, '\n')
		}
		switch {
		case row < rect.Min.Y || row >= rect.Max.Y:
			p.line(line, &chrome, nil)
		case rect.Min.X == 0:
			p.line(line, &pane, nil)
		default:
			p.line(line, &chrome, &pane)
		}
		row++
	}
	if dockPos != "top" && dockPos != "hidden" {
		p.out = append(p.out, '\n')
		p.block(dock, &dockGround)
	}
	return string(p.out)
}

// block paints every line of s on one ground.
func (p *fastPainter) block(s string, g *ground) {
	first := true
	for line := range strings.SplitSeq(s, "\n") {
		if !first {
			p.out = append(p.out, '\n')
		}
		first = false
		p.line(line, g, nil)
	}
}

// line paints one line. With inner nil the whole line is on outer. With inner
// set, the line's first character and its last are on outer and everything
// between them is on inner, which is a bordered pane's content row.
func (p *fastPainter) line(s string, outer, inner *ground) {
	last := -1
	if inner != nil {
		last = lastTextRune(s)
	}
	seenText := false
	for i := 0; i < len(s); {
		if s[i] == ansi.ESC {
			i += p.escape(s[i:])
			continue
		}
		end := len(s)
		if j := strings.IndexByte(s[i:], ansi.ESC); j >= 0 {
			end = i + j
		}
		if inner == nil {
			p.text(s[i:end], outer)
			i = end
			continue
		}
		pos := i
		if !seenText {
			seenText = true
			_, size := utf8.DecodeRuneInString(s[pos:end])
			p.text(s[pos:pos+size], outer)
			pos += size
		}
		if pos < end {
			if last >= pos && last < end {
				p.text(s[pos:last], inner)
				p.text(s[last:end], outer)
			} else {
				p.text(s[pos:end], inner)
			}
		}
		i = end
	}
}

// text copies a run of text with no escape sequence in it, first bringing the
// emitted pen to the ground g where the source pen has no colour of its own.
func (p *fastPainter) text(s string, g *ground) {
	if s == "" {
		return
	}
	if p.bgNil && p.bg != g.bgSGR {
		if g.bgSGR == "" {
			p.out = append(p.out, "\x1b[49m"...)
		} else {
			p.out = append(p.out, g.bgSGR...)
		}
		p.bg = g.bgSGR
	}
	if p.fgNil && p.fg != g.fgSGR {
		if g.fgSGR == "" {
			p.out = append(p.out, "\x1b[39m"...)
		} else {
			p.out = append(p.out, g.fgSGR...)
		}
		p.fg = g.fgSGR
	}
	p.out = append(p.out, s...)
}

// Resets that keep the ground. A bare reset clears the colours this painter
// put in force along with everything else, and the text after one nearly
// always needs them back, so a frame painted by inserting them again carries
// one colour SGR per styled run. The host parses every frame whole, and it
// boxes each direct colour it reads, so each of those is an allocation per
// frame on the client. These say the same reset attribute by attribute and
// leave the painted colours standing: bold and faint, italic, underline,
// blink, reverse, conceal, strikethrough and underline colour, which with the
// colours are every field of the pen ultraviolet keeps.
const (
	resetKeepBoth = "\x1b[22;23;24;25;27;28;29;59m"
	resetKeepBg   = "\x1b[22;23;24;25;27;28;29;39;59m"
	resetKeepFg   = "\x1b[22;23;24;25;27;28;29;49;59m"
)

// escape copies the escape sequence at the start of s and returns its length,
// following the pen through it when it is an SGR.
func (p *fastPainter) escape(s string) int {
	seq, _, n, _ := ansi.DecodeSequence(s, ansi.NormalState, p.parser)
	if n <= 0 {
		n = 1
	}
	if (p.bg != "" || p.fg != "") && (seq == ansi.ResetStyle || seq == "\x1b[0m") {
		switch {
		case p.bg != "" && p.fg != "":
			p.out = append(p.out, resetKeepBoth...)
		case p.bg != "":
			p.out = append(p.out, resetKeepBg...)
		default:
			p.out = append(p.out, resetKeepFg...)
		}
		// The source pen is bare, and the painted colours are still what the
		// host's pen holds, which is what p.bg and p.fg already say.
		p.fgNil, p.bgNil = true, true
		return n
	}
	p.out = append(p.out, s[:n]...)
	if ansi.HasCsiPrefix(seq) && p.parser.Command() == 'm' {
		p.sgr(p.parser.Params())
	}
	return n
}

// sgr follows the source pen's foreground and background through one SGR,
// reading the parameters exactly as ultraviolet's ReadStyle does, so that the
// pen here is the pen the host will have. Only whether each colour is set is
// kept; which colour it is does not matter to the paint.
func (p *fastPainter) sgr(params ansi.Params) {
	if len(params) == 0 {
		p.resetPen()
		return
	}
	for i := 0; i < len(params); i++ {
		param, hasMore, _ := params.Param(i, 0)
		switch {
		case param == 0:
			p.resetPen()
		case param == 4:
			// An underline style is a subparameter, and it is consumed.
			if next, _, ok := params.Param(i+1, 0); hasMore && ok && next >= 0 && next <= 5 {
				i++
			}
		case param >= 30 && param <= 37, param >= 90 && param <= 97:
			p.setFg(false)
		case param == 39:
			p.setFg(true)
		case param >= 40 && param <= 47, param >= 100 && param <= 107:
			p.setBg(false)
		case param == 49:
			p.setBg(true)
		case param == 38, param == 48, param == 58:
			n, isNil := sgrColorSpan(params[i:])
			if n > 0 {
				switch param {
				case 38:
					p.setFg(isNil)
				case 48:
					p.setBg(isNil)
				}
				i += n - 1
			}
		}
	}
}

func (p *fastPainter) resetPen() {
	p.fgNil, p.bgNil = true, true
	p.fg, p.bg = "", ""
}

// setFg records that the source set its foreground, to nothing when isNil.
// The emitted foreground is the source's again either way.
func (p *fastPainter) setFg(isNil bool) { p.fgNil, p.fg = isNil, "" }

// setBg is setFg for the background.
func (p *fastPainter) setBg(isNil bool) { p.bgNil, p.bg = isNil, "" }

// lastTextRune is the byte offset of the last rune of s that is text rather
// than part of an escape sequence, or -1 when s has no text.
func lastTextRune(s string) int {
	last := -1
	for i := 0; i < len(s); {
		if s[i] == ansi.ESC {
			i += escapeLen(s[i:])
			continue
		}
		last = i
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return last
}

// escapeLen is the length of the escape sequence at the start of s, found the
// way the painter's own pass finds it.
func escapeLen(s string) int {
	_, _, n, _ := ansi.DecodeSequence(s, ansi.NormalState, nil)
	if n <= 0 {
		return 1
	}
	return n
}

// sgrColorSpan is ansi.ReadStyleColor without the colour: how many parameters
// the colour at the start of params takes, and whether it leaves the colour
// unset. ReadStyleColor boxes the colour it reads, which allocates for every
// direct colour, and the painter only needs the count and the nil. It follows
// ReadStyleColor branch for branch; TestSGRColorSpanMatchesReadStyleColor holds
// the two together.
func sgrColorSpan(params ansi.Params) (n int, isNil bool) {
	if len(params) < 2 {
		return 0, false
	}
	s, p := params[0], params[1]
	colorType := p.Param(0)

	// direct is ReadStyleColor's paramsfn: how many parameters a direct colour
	// takes, whether it carries a fourth component, and whether it can be read
	// at all.
	direct := func() (n int, four, ok bool) {
		switch {
		case s.HasMore() && p.HasMore() && len(params) > 8 && params[2].HasMore() && params[3].HasMore() && params[4].HasMore() && params[5].HasMore() && params[6].HasMore() && params[7].HasMore():
			return 9, true, true
		case s.HasMore() && p.HasMore() && len(params) > 7 && params[2].HasMore() && params[3].HasMore() && params[4].HasMore() && params[5].HasMore() && params[6].HasMore():
			return 8, true, true
		case s.HasMore() && p.HasMore() && len(params) > 6 && params[2].HasMore() && params[3].HasMore() && params[4].HasMore() && params[5].HasMore():
			return 7, true, true
		case s.HasMore() && p.HasMore() && len(params) > 5 && params[2].HasMore() && params[3].HasMore() && params[4].HasMore() && !params[5].HasMore():
			return 6, false, true
		case s.HasMore() && p.HasMore() && p.Param(0) == 2 && params[2].HasMore() && params[3].HasMore() && !params[4].HasMore():
			return 5, false, true
		case !s.HasMore() && !p.HasMore() && p.Param(0) == 2 && !params[2].HasMore() && !params[3].HasMore() && !params[4].HasMore():
			return 5, false, true
		}
		return 0, false, false
	}

	switch colorType {
	case 0: // implementation defined: read, and the colour is left unset
		return 2, true
	case 1: // transparent
		return 2, false
	case 2, 3: // RGB, CMY
		if len(params) < 5 {
			return 0, false
		}
		if n, _, ok := direct(); ok {
			return n, false
		}
		return 0, false
	case 4, 6: // CMYK, RGBA
		if len(params) < 6 {
			return 0, false
		}
		if n, four, ok := direct(); ok && four {
			return n, false
		}
		return 0, false
	case 5: // indexed
		if len(params) < 3 {
			return 0, false
		}
		switch {
		case s.HasMore() && p.HasMore() && !params[2].HasMore():
		case !s.HasMore() && !p.HasMore() && !params[2].HasMore():
		default:
			return 0, false
		}
		return 3, false
	}
	return 0, false
}
