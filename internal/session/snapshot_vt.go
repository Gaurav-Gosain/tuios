package session

import (
	"bytes"
	"strconv"

	uv "github.com/charmbracelet/ultraviolet"
)

// snapshotVT turns a TerminalState into the bytes that paint it on a fresh
// xterm-compatible emulator of the same size. It is the SNAP frame of
// stream-pane (verb_stream_pane.go): a client that is not written in Go
// cannot read a TerminalState, and every terminal emulator can read bytes.
//
// What it reproduces:
//
//   - The scrollback rows the state carries, above the screen they belong
//     to, by printing them from the top and letting them scroll off.
//   - Every cell's text, colours and attributes. A palette colour stays a
//     palette index and a truecolour stays truecolour, so the client's theme
//     applies the way it does to live output. Hyperlinks travel as OSC 8.
//   - Soft wraps: a wrapped row is printed to its last column and the next
//     row continues it, so the client emulator marks the row as wrapped too.
//   - The alternate screen: the main screen is painted first, then 1049
//     switches to the alternate one and paints it, so quitting the program
//     brings the shell's screen back.
//   - The cursor position, its shape and its visibility, a pending wrap, the
//     pen, the scroll region (top and bottom margins), the character sets,
//     the kitty keyboard flags, modifyOtherKeys, and the DEC modes that
//     change what the client must send: application cursor keys, the keypad
//     mode, bracketed paste, focus reports, every mouse tracking mode and its
//     encodings, and alternate scroll.
//
// What it does not: left and right margins, origin mode, insert mode (IRM),
// newline mode (LNM), the cursor DECSC saved, protected cells and the
// character REP repeats. They decide how
// later output is painted, and a phone that misses them paints a rare
// sequence differently until the next snapshot; the input it sends is the
// same.
func snapshotVT(st *TerminalState) []byte {
	if st == nil {
		return nil
	}
	var b bytes.Buffer
	b.Grow(st.Width * (st.Height + len(st.Scrollback)) * 2)
	w := &vtWriter{b: &b, width: st.Width}

	// Autowrap on and the pen reset, whatever the client was left at.
	b.WriteString("\x1b[0m\x1b[?7h\x1b[H")

	// The rows from the top: history, then the screen the history belongs
	// to. Under an alternate screen that is the main screen, whose wrap flags
	// the state does not carry.
	main, mainWraps := st.Screen, wrapFlags(st.ScreenWraps, len(st.Screen))
	if st.IsAltScreen && st.MainScreen != nil {
		main, mainWraps = st.MainScreen, make([]bool, len(st.MainScreen))
	}
	rows := make([][]CellState, 0, len(st.Scrollback)+len(main))
	rows = append(rows, st.Scrollback...)
	rows = append(rows, main...)
	wraps := append(wrapFlags(st.ScrollbackWraps, len(st.Scrollback)), mainWraps...)
	w.rows(rows, wraps, false)

	if st.IsAltScreen {
		// 1049 saves the cursor on the main screen, which is where leaving
		// the program puts it back.
		if c := st.MainSavedCursor; c != nil {
			w.cup(c.X, c.Y)
		}
		w.kittyStack(st.KittyKbdMainStack)
		b.WriteString("\x1b[?1049h")
		w.rows(st.Screen, wrapFlags(st.ScreenWraps, len(st.Screen)), true)
	}

	if m := st.Margins; len(m) == 4 && m[3] > 0 {
		// DECSTBM homes the cursor, so it goes before the cursor is placed.
		b.WriteString("\x1b[" + strconv.Itoa(m[1]+1) + ";" + strconv.Itoa(m[1]+m[3]) + "r")
	}

	// The cursor, with its pending wrap: printing the cell under it again
	// leaves the cursor where it is with the wrap armed, which no cursor
	// movement can do.
	w.cup(st.CursorX, st.CursorY)
	if st.PendingWrap && st.CursorX == st.Width-1 && st.CursorY < len(st.Screen) {
		if row := st.Screen[st.CursorY]; st.CursorX < len(row) && row[st.CursorX].Width != 0 {
			w.cell(row[st.CursorX])
			w.resetPen()
		}
	}

	charsets(&b, st.Charsets)
	decModes(&b, st.Modes)
	// The active screen's stack. Under the alternate screen the main
	// screen's was pushed before the switch.
	w.kittyStack(st.KittyKbdStack)
	if st.ModifyOtherKeysKnown && st.ModifyOtherKeys > 0 {
		b.WriteString("\x1b[>4;" + strconv.Itoa(st.ModifyOtherKeys) + "m")
	}
	if st.CursorShape > 0 {
		b.WriteString("\x1b[" + strconv.Itoa(st.CursorShape) + " q")
	}
	if st.Pen != nil {
		w.pen(*st.Pen)
	}
	return b.Bytes()
}

// vtWriter writes rows of cells, keeping track of the pen and the link it
// last set so a run of cells painted the same way costs one SGR.
type vtWriter struct {
	b     *bytes.Buffer
	width int
	cur   StyleState
	set   bool // cur was written; false means the pen is the default
}

// rows writes rows from the cursor down. absolute places each row with a
// cursor movement instead of a newline, for a screen that must not scroll.
func (w *vtWriter) rows(rows [][]CellState, wraps []bool, absolute bool) {
	for i, row := range rows {
		if absolute {
			w.resetPen()
			w.cup(0, i)
		}
		// A wrapped row is printed through its last column and the next row
		// carries on from it. It counts as wrapped only when the next row
		// prints something, or the newline after that row would land on the
		// wrong line.
		wrapped := !absolute && i < len(wraps) && wraps[i] && i+1 < len(rows) && len(rows[i+1]) > 0
		w.row(row, wrapped, !absolute && i > 0 && i-1 < len(wraps) && wraps[i-1])
		if absolute || i == len(rows)-1 || wrapped {
			continue
		}
		w.resetPen()
		w.b.WriteString("\r\n")
	}
	w.resetPen()
}

// row writes one row's cells. full prints every column, for a wrapped row;
// otherwise trailing blanks are left out. lead prints at least the first
// cell, so a row a wrapped row carries on into starts on the next line.
func (w *vtWriter) row(row []CellState, full, lead bool) {
	n := min(len(row), w.width)
	end := n
	if !full {
		for end > 0 && blankCell(row[end-1]) {
			end--
		}
		if lead && end == 0 && n > 0 {
			end = 1
		}
	}
	for x := 0; x < end; x++ {
		c := row[x]
		if c.Width == 0 && c.Content == "" && x > 0 && row[x-1].Width > 1 {
			// The second column of a wide character.
			continue
		}
		if c.Width > 1 && x+c.Width > w.width {
			// A wide character that no longer fits the width. Print a blank
			// in its place rather than wrap it onto the next row.
			c = CellState{Content: " ", Width: 1, StyleState: c.StyleState}
		}
		w.cell(c)
	}
}

// cell prints one cell with its pen.
func (w *vtWriter) cell(c CellState) {
	if !w.set || c.StyleState != w.cur {
		w.pen(c.StyleState)
	}
	if c.Content == "" {
		w.b.WriteByte(' ')
		return
	}
	w.b.WriteString(c.Content)
}

// blankCell reports whether a cell prints nothing and paints nothing.
func blankCell(c CellState) bool {
	return (c.Content == "" || c.Content == " ") && c.StyleState == (StyleState{})
}

// cup moves the cursor to column x, row y, counted from zero.
func (w *vtWriter) cup(x, y int) {
	w.b.WriteString("\x1b[" + strconv.Itoa(y+1) + ";" + strconv.Itoa(x+1) + "H")
}

// resetPen puts the pen back to the default and closes an open link.
func (w *vtWriter) resetPen() {
	if !w.set {
		return
	}
	if w.cur.LinkURL != "" {
		w.b.WriteString("\x1b]8;;\x1b\\")
	}
	w.b.WriteString("\x1b[0m")
	w.cur, w.set = StyleState{}, false
}

// pen sets the pen to s: one SGR from a reset, and the link when it changed.
func (w *vtWriter) pen(s StyleState) {
	if s.LinkURL != w.cur.LinkURL || s.LinkParams != w.cur.LinkParams {
		w.b.WriteString("\x1b]8;" + s.LinkParams + ";" + s.LinkURL + "\x1b\\")
	}
	w.b.WriteString("\x1b[0")
	attrs := []struct {
		bit  uint8
		code string
	}{
		{uv.AttrBold, "1"}, {uv.AttrFaint, "2"}, {uv.AttrItalic, "3"},
		{uv.AttrBlink, "5"}, {uv.AttrRapidBlink, "6"}, {uv.AttrReverse, "7"},
		{uv.AttrConceal, "8"}, {uv.AttrStrikethrough, "9"},
	}
	for _, a := range attrs {
		if s.Attrs&a.bit != 0 {
			w.b.WriteString(";" + a.code)
		}
	}
	switch {
	case s.Underline == 1:
		w.b.WriteString(";4")
	case s.Underline > 1:
		w.b.WriteString(";4:" + strconv.Itoa(int(s.Underline)))
	}
	sgrColor(w.b, s.FgColor, 30, 90, "38")
	sgrColor(w.b, s.BgColor, 40, 100, "48")
	sgrColor(w.b, s.UlColor, -1, -1, "58")
	w.b.WriteByte('m')
	w.cur, w.set = s, true
}

// sgrColor appends one colour in the wire form colorToWire writes: a<n> for
// one of the 16 palette colours, i<n> for the 256-colour palette, #rrggbb
// for truecolour. base and bright are the SGR codes of palette colour 0 and
// 8; -1 means the colour has only the extended form, as the underline
// colour does.
func sgrColor(b *bytes.Buffer, wire string, base, bright int, ext string) {
	if len(wire) < 2 {
		return
	}
	switch wire[0] {
	case 'a', 'i':
		n, err := strconv.Atoi(wire[1:])
		if err != nil || n < 0 || n > 255 {
			return
		}
		switch {
		case wire[0] == 'a' && base >= 0 && n < 8:
			b.WriteString(";" + strconv.Itoa(base+n))
		case wire[0] == 'a' && bright >= 0 && n < 16:
			b.WriteString(";" + strconv.Itoa(bright+n-8))
		default:
			b.WriteString(";" + ext + ";5;" + strconv.Itoa(n))
		}
	case '#':
		if len(wire) != 7 {
			return
		}
		b.WriteString(";" + ext + ";2")
		for i := range 3 {
			hi, lo := hexNibble(wire[1+2*i]), hexNibble(wire[2+2*i])
			if hi < 0 || lo < 0 {
				return
			}
			b.WriteString(";" + strconv.Itoa(hi<<4|lo))
		}
	}
}

// kittyStack pushes the kitty keyboard flags, base entry first.
func (w *vtWriter) kittyStack(stack []int) {
	for _, flags := range stack {
		w.b.WriteString("\x1b[>" + strconv.Itoa(flags) + "u")
	}
}

// charsets designates G0 to G3 and shifts GL, in the layout of
// TerminalState.Charsets. US ASCII in G0 shifted into GL is the default and
// writes nothing.
func charsets(b *bytes.Buffer, cs []int) {
	if len(cs) != 6 {
		return
	}
	intro := [4]string{"(", ")", "*", "+"}
	for i := range 4 {
		if c := cs[i]; c != 0 && c != 'B' && c > ' ' && c < 0x7f {
			b.WriteString("\x1b" + intro[i] + string(rune(c)))
		}
	}
	switch cs[4] {
	case 1:
		b.WriteByte(0x0e) // SO
	case 2:
		b.WriteString("\x1bn") // LS2
	case 3:
		b.WriteString("\x1bo") // LS3
	}
}

// snapshotModes are the DEC private modes a SNAP frame sets, in the order it
// sets them: what changes the bytes the client sends, and the two that
// change how the screen shows (autowrap and the cursor's visibility).
// Autowrap comes after the rows, which are painted with it on.
var snapshotModes = []int{
	1,    // DECCKM: application cursor keys
	7,    // DECAWM: autowrap
	25,   // DECTCEM: the cursor is shown
	9,    // X10 mouse
	1000, // mouse button tracking
	1002, // mouse button and drag tracking
	1003, // mouse any-motion tracking
	1004, // focus reports
	1005, // UTF-8 mouse encoding
	1006, // SGR mouse encoding
	1015, // urxvt mouse encoding
	1016, // SGR pixel mouse encoding
	1007, // alternate scroll
	2004, // bracketed paste
}

// decModes writes the snapshot's DEC modes. The keypad mode is mode 66 in the
// state and is written as DECKPAM or DECKPNM, which every emulator knows.
func decModes(b *bytes.Buffer, modes map[int]bool) {
	for _, m := range snapshotModes {
		on, ok := modes[m]
		if !ok {
			continue
		}
		b.WriteString("\x1b[?" + strconv.Itoa(m))
		if on {
			b.WriteByte('h')
		} else {
			b.WriteByte('l')
		}
	}
	if on, ok := modes[66]; ok {
		if on {
			b.WriteString("\x1b=")
		} else {
			b.WriteString("\x1b>")
		}
	}
}
