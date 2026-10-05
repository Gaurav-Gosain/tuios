package guibridge

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// SnapshotVT turns a pane snapshot into the VT byte stream that rebuilds it in
// a fresh emulator of the snapshot's size: the scrollback and the main screen
// first, written as lines so the older ones scroll into history, then the
// alternate screen if one is active, then the modes, margins, keyboard state,
// cursor and pen the guest left in force.
//
// The receiving emulator must be reset and sized to Width x Height before the
// bytes are written. The state is not modified.
func SnapshotVT(st *session.TerminalState) []byte {
	if st == nil {
		return nil
	}
	cp := *st
	if err := cp.Unpack(); err != nil {
		return nil
	}
	var b strings.Builder
	w := &sgrWriter{b: &b}

	main := cp.Screen
	mainWraps := cp.ScreenWraps
	if cp.IsAltScreen {
		main = cp.MainScreen
		mainWraps = nil
	}
	lines := make([][]session.CellState, 0, len(cp.Scrollback)+len(main))
	lines = append(lines, cp.Scrollback...)
	lines = append(lines, main...)
	nsb := len(cp.Scrollback)
	wrapped := func(i int) bool {
		if i < nsb {
			return bit(cp.ScrollbackWraps, i)
		}
		return bit(mainWraps, i-nsb)
	}
	b.WriteString("\x1b[H")
	for i, line := range lines {
		soft := wrapped(i) && i < len(lines)-1
		w.row(line, soft, cp.Width)
		if i < len(lines)-1 && !soft {
			w.reset()
			b.WriteString("\r\n")
		}
	}
	w.reset()

	setStack := func(stack []int) {
		// The base entry is set in place, the rest are pushed over it.
		for i, f := range stack {
			if i == 0 {
				b.WriteString("\x1b[=" + strconv.Itoa(f) + ";1u")
				continue
			}
			b.WriteString("\x1b[>" + strconv.Itoa(f) + "u")
		}
	}
	if cp.IsAltScreen {
		setStack(cp.KittyKbdMainStack)
		// Entered the way the guest entered it, so leaving it later does what
		// the guest expects. The alternate screen is painted row by row below.
		alt := "1049"
		switch {
		case cp.Modes[1047]:
			alt = "1047"
		case cp.Modes[47]:
			alt = "47"
		}
		b.WriteString("\x1b[?" + alt + "h\x1b[H\x1b[2J")
		for y, line := range cp.Screen {
			b.WriteString("\x1b[" + strconv.Itoa(y+1) + ";1H")
			w.row(line, false, cp.Width)
			w.reset()
		}
	}
	setStack(cp.KittyKbdStack)

	if len(cp.Charsets) == 6 {
		for i, inter := range []byte{'(', ')', '*', '+'} {
			if id := byte(cp.Charsets[i]); id != 0 && id != 'B' {
				b.WriteString("\x1b" + string(inter) + string(id))
			}
		}
		if cp.Charsets[4] == 1 {
			b.WriteByte(0x0e)
		}
	}
	if cp.ModifyOtherKeysKnown || cp.ModifyOtherKeys > 0 {
		b.WriteString("\x1b[>4;" + strconv.Itoa(cp.ModifyOtherKeys) + "m")
	}
	if cp.CursorShape > 0 {
		b.WriteString("\x1b[" + strconv.Itoa(cp.CursorShape) + " q")
	}
	top := 0
	if len(cp.Margins) == 4 && (cp.Margins[1] > 0 || cp.Margins[3] < cp.Height) {
		top = cp.Margins[1]
		b.WriteString("\x1b[" + strconv.Itoa(top+1) + ";" + strconv.Itoa(top+cp.Margins[3]) + "r")
	}
	keys := make([]int, 0, len(cp.Modes))
	for k := range cp.Modes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	origin := false
	for _, k := range keys {
		switch k {
		case 47, 1047, 1049, 2026, 6:
			// The screen switch is done above, a synchronized-output hold
			// would freeze the renderer until the guest ends it, and origin
			// mode moves the cursor, so it goes last.
			if k == 6 {
				origin = cp.Modes[k]
			}
			continue
		}
		if cp.Modes[k] {
			b.WriteString("\x1b[?" + strconv.Itoa(k) + "h")
		} else {
			b.WriteString("\x1b[?" + strconv.Itoa(k) + "l")
		}
	}
	y := cp.CursorY
	if origin {
		b.WriteString("\x1b[?6h")
		y -= top
	}
	b.WriteString("\x1b[" + strconv.Itoa(y+1) + ";" + strconv.Itoa(cp.CursorX+1) + "H")
	if cp.Pen != nil {
		w.style(*cp.Pen)
	}
	return []byte(b.String())
}

func bit(bits []byte, i int) bool {
	if i < 0 || i/8 >= len(bits) {
		return false
	}
	return bits[i/8]&(1<<(i%8)) != 0
}

type sgrWriter struct {
	b    *strings.Builder
	cur  session.StyleState
	link string
	set  bool
}

func (w *sgrWriter) reset() {
	if w.link != "" {
		w.b.WriteString("\x1b]8;;\x1b\\")
		w.link = ""
	}
	if w.set {
		w.b.WriteString("\x1b[0m")
	}
	w.cur = session.StyleState{}
	w.set = false
}

func blankStyle(s session.StyleState) bool {
	return s.BgColor == "" && s.Attrs&(1<<5) == 0 && s.Underline == 0 && s.LinkURL == ""
}

// row writes one row's cells. A soft-wrapped row is written to its full width
// so the emulator wraps onto the next row itself; any other row stops at its
// last cell that shows anything.
func (w *sgrWriter) row(cells []session.CellState, soft bool, width int) {
	end := len(cells)
	if width > 0 && end > width {
		end = width
	}
	if !soft {
		for end > 0 {
			c := cells[end-1]
			if (c.Content == "" || c.Content == " ") && blankStyle(c.StyleState) {
				end--
				continue
			}
			break
		}
	}
	for _, c := range cells[:end] {
		if c.Width == 0 && c.Content == "" && end > 0 {
			// The second column of a wide character.
			continue
		}
		w.style(c.StyleState)
		if c.Content == "" {
			w.b.WriteByte(' ')
		} else {
			w.b.WriteString(c.Content)
		}
	}
}

func (w *sgrWriter) style(s session.StyleState) {
	if s.LinkURL != w.link {
		w.b.WriteString("\x1b]8;" + s.LinkParams + ";" + s.LinkURL + "\x1b\\")
		w.link = s.LinkURL
	}
	if w.set && s.FgColor == w.cur.FgColor && s.BgColor == w.cur.BgColor && s.UlColor == w.cur.UlColor &&
		s.Attrs == w.cur.Attrs && s.Underline == w.cur.Underline {
		return
	}
	w.b.WriteString(SGR(s))
	w.cur = s
	w.set = true
}

// SGR is the full select-graphic-rendition sequence for a wire style,
// starting from a reset.
func SGR(s session.StyleState) string {
	parts := []string{"0"}
	for i, code := range []string{"1", "2", "3", "5", "6", "7", "8", "9"} {
		if s.Attrs&(1<<i) != 0 {
			parts = append(parts, code)
		}
	}
	switch s.Underline {
	case 0:
	case 1:
		parts = append(parts, "4")
	default:
		parts = append(parts, "4:"+strconv.Itoa(int(s.Underline)))
	}
	if c := colorSGR(s.FgColor, 30, 90, "38"); c != "" {
		parts = append(parts, c)
	}
	if c := colorSGR(s.BgColor, 40, 100, "48"); c != "" {
		parts = append(parts, c)
	}
	if c := colorSGR(s.UlColor, -1, -1, "58"); c != "" {
		parts = append(parts, c)
	}
	return "\x1b[" + strings.Join(parts, ";") + "m"
}

// colorSGR encodes a wire colour ("" default, aN basic, iN indexed, #rrggbb).
// base and bright are the SGR bases for the basic 16; a negative base means
// the attribute has no short form and uses the extended one.
func colorSGR(c string, base, bright int, ext string) string {
	if c == "" {
		return ""
	}
	switch c[0] {
	case 'a', 'i':
		n, err := strconv.Atoi(c[1:])
		if err != nil || n < 0 || n > 255 {
			return ""
		}
		if c[0] == 'a' && base >= 0 {
			if n < 8 {
				return strconv.Itoa(base + n)
			}
			if n < 16 {
				return strconv.Itoa(bright + n - 8)
			}
		}
		return ext + ";5;" + strconv.Itoa(n)
	case '#':
		if len(c) != 7 {
			return ""
		}
		v, err := strconv.ParseUint(c[1:], 16, 32)
		if err != nil {
			return ""
		}
		return ext + ";2;" + strconv.Itoa(int(v>>16&0xff)) + ";" + strconv.Itoa(int(v>>8&0xff)) + ";" + strconv.Itoa(int(v&0xff))
	}
	return ""
}
