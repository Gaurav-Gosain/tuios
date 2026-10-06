package app

import (
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/invisible"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// The navigator's preview shows a pane's screen in its own colours.
//
// A pane this client draws is read from the client's own emulator, live,
// the way the picture-in-picture view reads it (pipCells). A pane in another
// session or on another machine is read with a styled capture-pane, and the
// capture is never written to the host terminal as it came. It is parsed
// into cells by an emulator of its own, and each row is drawn again from
// those cells: the colours and the attributes survive, and nothing else
// does. An OSC sequence (a clipboard write, a hyperlink, a title) is
// consumed by the parse, a cursor movement or an erase moves nothing outside
// the parse's own grid, and a control character in a cell is dropped. What
// the preview writes is SGR and printable text.

// navStyledMaxCols bounds the columns a parsed capture keeps. A pane wider
// than this is cut: the preview is narrower still.
const navStyledMaxCols = 400

// navInk is the theme the capture is parsed under, read on the UI goroutine
// before the load starts, so the sixteen ANSI colours of another session's
// pane are this client's, as they are on the panes it draws.
type navInk struct {
	on          bool
	fg, bg, cur color.Color
	ansiPalette [16]color.Color
}

// navThemeInk is the theme in force now.
func navThemeInk() navInk {
	if !theme.IsEnabled() {
		return navInk{}
	}
	return navInk{
		on: true, fg: theme.TerminalFg(), bg: theme.TerminalBg(), cur: theme.TerminalCursor(),
		ansiPalette: theme.GetANSIPalette(),
	}
}

// navParser parses styled captures, one after another, with one emulator. A
// load makes one for each machine it reads, rather than one for each pane:
// an emulator costs about a megabyte and two milliseconds to make, which at
// the cap of 150 panes was most of what opening the navigator cost.
//
// The emulator is one row of navStyledMaxCols cells. Each line starts from a
// full reset (the pen, the modes, the margins, the main screen) with wrapping
// off, so whatever one line holds, it cannot reach another line, or the next
// capture. The theme's sixteen survive the reset.
type navParser struct {
	emu *vt.Emulator
}

// newNavParser makes a parser that resolves the sixteen ANSI colours with
// ink.
func newNavParser(ink navInk) *navParser {
	emu := vt.NewEmulator(navStyledMaxCols, 1)
	if ink.on {
		emu.SetThemeColors(ink.fg, ink.bg, ink.cur, ink.ansiPalette)
	}
	return &navParser{emu: emu}
}

// Close lets the emulator go.
func (p *navParser) Close() { _ = p.emu.Close() }

// navParseStyled parses one capture with a parser of its own. The load uses
// a navParser for each machine instead.
func navParseStyled(content string, ink navInk, keep int) (plain, styled []string) {
	p := newNavParser(ink)
	defer p.Close()
	return p.parse(content, keep)
}

// parse parses a styled capture into its last keep rows: each row as plain
// text, for the search, and as SGR-styled text, for the preview. The blank
// rows at the end are dropped first.
func (p *navParser) parse(content string, keep int) (plain, styled []string) {
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, "\r")
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > keep {
		lines = lines[len(lines)-keep:]
	}
	if len(lines) == 0 {
		return nil, nil
	}
	cols := 1
	for _, l := range lines {
		cols = max(cols, ansi.StringWidth(l))
	}
	cols = min(cols, navStyledMaxCols)

	emu := p.emu
	plain = make([]string, 0, len(lines))
	styled = make([]string, 0, len(lines))
	for _, l := range lines {
		_, _ = emu.WriteString("\x1bc\x1b[?7l")
		_, _ = emu.WriteString(l)
		line := navCellRow(emu, 0, cols)
		plain = append(plain, strings.TrimRight(line.String(), " "))
		styled = append(styled, line.Render())
	}
	return plain, styled
}

// navCellRow is row y of a parsed capture as cells: links dropped, image
// placeholders and control characters blanked, and cut after the last cell
// that shows anything.
func navCellRow(emu *vt.Emulator, y, cols int) uv.Line {
	line := make(uv.Line, cols)
	end := 0
	for x := range cols {
		line[x] = uv.EmptyCell
		c := emu.CellAt(x, y)
		if c == nil {
			continue
		}
		cell := *c
		cell.Link = uv.Link{}
		switch {
		case cell.Width == 0 && cell.Content == "":
			// The tail of a wide glyph.
		case vt.IsKittyPlaceholder(cell.Content):
			cell = uv.EmptyCell
		default:
			if txt := navPrintable(cell.Content); txt != cell.Content {
				cell.Content = txt
				if txt == "" {
					cell.Content, cell.Width = " ", 1
				}
			}
		}
		line[x] = cell
		if (cell.Content != "" && cell.Content != " ") || cell.Style.Bg != nil || cell.Style.Attrs&uv.AttrReverse != 0 {
			end = x + max(cell.Width, 1)
		}
	}
	return line[:min(end, cols)]
}

// navPrintable drops the C0 and C1 control characters from a cell's text,
// and the characters that draw nothing but change how text reads (see
// invisible.Rune): a bidi override in another machine's capture would
// reorder the preview, and the rest hide text from the person reading it.
func navPrintable(s string) string {
	clean := true
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) {
			clean = false
			break
		}
	}
	if !clean {
		var b strings.Builder
		for _, r := range s {
			if r >= 0x20 && (r < 0x7f || r >= 0xa0) {
				b.WriteRune(r)
			}
		}
		s = b.String()
	}
	return invisible.Strip(s)
}

// navScreenLine fits one styled preview row to cols cells: cut where it is
// wider, a wide glyph that would cross the edge dropped whole, the pen reset
// after it, and padded on the pane's own ground.
func navScreenLine(s string, cols int) string {
	if cols <= 0 {
		return ""
	}
	cut := ansi.Truncate(s, cols, "")
	w := ansi.StringWidth(cut)
	return cut + "\x1b[m" + strings.Repeat(" ", max(cols-w, 0))
}
