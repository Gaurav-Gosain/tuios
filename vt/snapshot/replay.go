package snapshot

import (
	"bytes"
	"strconv"

	uv "github.com/charmbracelet/ultraviolet"

	"tuios.dev/vt"
)

// Replay turns a State into the bytes that paint it on a fresh
// xterm-compatible emulator of the same size. A client that is not written in
// Go cannot read a State, and every terminal emulator can read bytes. In tuios
// it is the SNAP frame of stream-pane.
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
//   - The tab stops, the window title, insert mode (IRM) and newline mode
//     (LNM).
//   - The sequence the daemon's parser was in the middle of, last, so the
//     stream that follows the snapshot finishes it.
//
// The tuios-gpui renderer primes each pane with the same bytes, through
// tuios gui-bridge (Replay), so one encoder serves both.
//
// What it does not: left and right margins, the cursor DECSC
// saved, protected cells, the character REP repeats, the title stack and the
// colours the guest set with OSC 4, 10, 11 and 12. They decide how later
// output is painted, and a phone that misses them paints a rare sequence
// differently until the next snapshot; the input it sends is the same.
//
// The state may be packed, as one read off the wire is. It is not changed.
func Replay(st *State) []byte {
	if st == nil {
		return nil
	}
	cp := *st
	if err := cp.Unpack(); err != nil {
		return nil
	}
	return replay(&cp)
}

func replay(st *State) []byte {
	if st == nil {
		return nil
	}
	var b bytes.Buffer
	b.Grow(st.Width * (st.Height + len(st.Scrollback)) * 2)
	w := &vtWriter{b: &b, width: st.Width}

	b.WriteString("\x1b[0m\x1b[?7h\x1b[H")

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
		if c := st.MainSavedCursor; c != nil {
			w.cup(c.X, c.Y)
		}
		w.kittyStack(st.KittyKbdMainStack)
		alt := "1049"
		switch {
		case st.Modes[1047]:
			alt = "1047"
		case st.Modes[47]:
			alt = "47"
		}
		b.WriteString("\x1b[?")
		b.WriteString(alt)
		b.WriteString("h")
		w.rows(st.Screen, wrapFlags(st.ScreenWraps, len(st.Screen)), true)
	}

	if len(st.TabStops) > 0 {
		b.WriteString("\x1b[3g")
		for _, x := range tabStopCols(st.TabStops) {
			if x < st.Width {
				w.cup(x, 0)
				b.WriteString("\x1bH")
			}
		}
	}

	top := 0
	if m := st.Margins; len(m) == 4 && m[3] > 0 {
		top = m[1]
		b.WriteString("\x1b[")
		b.WriteString(strconv.Itoa(m[1] + 1))
		b.WriteString(";")
		b.WriteString(strconv.Itoa(m[1] + m[3]))
		b.WriteString("r")
	}

	cy := st.CursorY
	if st.Modes[6] {
		b.WriteString("\x1b[?6h")
		cy -= top
	}

	w.cup(st.CursorX, cy)
	if st.PendingWrap && st.CursorX == st.Width-1 && st.CursorY < len(st.Screen) {
		if row := st.Screen[st.CursorY]; st.CursorX < len(row) && row[st.CursorX].Width != 0 {
			w.cell(row[st.CursorX])
			w.resetPen()
		}
	}

	charsets(&b, st.Charsets)
	decModes(&b, st.Modes)
	w.kittyStack(st.KittyKbdStack)
	if st.ModifyOtherKeysKnown && st.ModifyOtherKeys > 0 {
		b.WriteString("\x1b[>4;")
		b.WriteString(strconv.Itoa(st.ModifyOtherKeys))
		b.WriteString("m")
	}
	if st.CursorShape > 0 {
		b.WriteString("\x1b[")
		b.WriteString(strconv.Itoa(st.CursorShape))
		b.WriteString(" q")
	}
	if st.Pen != nil {
		w.pen(*st.Pen)
	}
	if st.TitlesKnown {
		b.WriteString("\x1b]2;")
		b.WriteString(st.Title)
		b.WriteString("\x1b\\")
	}
	for _, m := range []int{4, 20} {
		if on, ok := st.ANSIModes[m]; ok {
			b.WriteString("\x1b[")
			b.WriteString(strconv.Itoa(m))
			if on {
				b.WriteByte('h')
			} else {
				b.WriteByte('l')
			}
		}
	}
	if len(st.PendingInput) <= vt.MaxPendingInput {
		b.Write(st.PendingInput)
	}
	return b.Bytes()
}

type vtWriter struct {
	b     *bytes.Buffer
	width int
	cur   StyleState
	set   bool
}

func (w *vtWriter) rows(rows [][]CellState, wraps []bool, absolute bool) {
	for i, row := range rows {
		if absolute {
			w.resetPen()
			w.cup(0, i)
		}
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
			continue
		}
		if c.Width > 1 && x+c.Width > w.width {
			c = CellState{Content: " ", Width: 1, StyleState: c.StyleState}
		}
		w.cell(c)
	}
}

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

func blankCell(c CellState) bool {
	return (c.Content == "" || c.Content == " ") && c.StyleState == (StyleState{})
}

func (w *vtWriter) cup(x, y int) {
	w.b.WriteString("\x1b[")
	w.b.WriteString(strconv.Itoa(y + 1))
	w.b.WriteString(";")
	w.b.WriteString(strconv.Itoa(x + 1))
	w.b.WriteString("H")
}

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

func (w *vtWriter) pen(s StyleState) {
	if s.LinkURL != w.cur.LinkURL || s.LinkParams != w.cur.LinkParams {
		w.b.WriteString("\x1b]8;")
		w.b.WriteString(s.LinkParams)
		w.b.WriteString(";")
		w.b.WriteString(s.LinkURL)
		w.b.WriteString("\x1b\\")
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
			w.b.WriteString(";")
			w.b.WriteString(a.code)
		}
	}
	switch {
	case s.Underline == 1:
		w.b.WriteString(";4")
	case s.Underline > 1:
		w.b.WriteString(";4:")
		w.b.WriteString(strconv.Itoa(int(s.Underline)))
	}
	sgrColor(w.b, s.FgColor, 30, 90, "38")
	sgrColor(w.b, s.BgColor, 40, 100, "48")
	sgrColor(w.b, s.UlColor, -1, -1, "58")
	w.b.WriteByte('m')
	w.cur, w.set = s, true
}

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
			b.WriteString(";")
			b.WriteString(strconv.Itoa(base + n))
		case wire[0] == 'a' && bright >= 0 && n < 16:
			b.WriteString(";")
			b.WriteString(strconv.Itoa(bright + n - 8))
		default:
			b.WriteString(";")
			b.WriteString(ext)
			b.WriteString(";5;")
			b.WriteString(strconv.Itoa(n))
		}
	case '#':
		if len(wire) != 7 {
			return
		}
		b.WriteString(";")
		b.WriteString(ext)
		b.WriteString(";2")
		for i := range 3 {
			hi, lo := hexNibble(wire[1+2*i]), hexNibble(wire[2+2*i])
			if hi < 0 || lo < 0 {
				return
			}
			b.WriteString(";")
			b.WriteString(strconv.Itoa(hi<<4 | lo))
		}
	}
}

func (w *vtWriter) kittyStack(stack []int) {
	for i, flags := range stack {
		if i == 0 {
			w.b.WriteString("\x1b[=")
			w.b.WriteString(strconv.Itoa(flags))
			w.b.WriteString(";1u")
			continue
		}
		w.b.WriteString("\x1b[>")
		w.b.WriteString(strconv.Itoa(flags))
		w.b.WriteString("u")
	}
}

func charsets(b *bytes.Buffer, cs []int) {
	if len(cs) != 6 {
		return
	}
	intro := [4]string{"(", ")", "*", "+"}
	for i := range 4 {
		if c := cs[i]; c != 0 && c != 'B' && c > ' ' && c < 0x7f {
			b.WriteString("\x1b")
			b.WriteString(intro[i])
			b.WriteString(string(rune(c)))
		}
	}
	switch cs[4] {
	case 1:
		b.WriteByte(0x0e)
	case 2:
		b.WriteString("\x1bn")
	case 3:
		b.WriteString("\x1bo")
	}
}

var snapshotModes = []int{
	1,
	7,
	25,
	9,
	1000,
	1002,
	1003,
	1004,
	1005,
	1006,
	1015,
	1016,
	1007,
	2004,
}

func decModes(b *bytes.Buffer, modes map[int]bool) {
	for _, want := range []bool{false, true} {
		for _, m := range snapshotModes {
			on, ok := modes[m]
			if !ok || on != want {
				continue
			}
			b.WriteString("\x1b[?")
			b.WriteString(strconv.Itoa(m))
			if on {
				b.WriteByte('h')
			} else {
				b.WriteByte('l')
			}
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
