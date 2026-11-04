package main

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/mygo/plugins/terminal"
	"github.com/Gaurav-Gosain/mygo/ui"
)

// cellStyle is how a terminal cell draws: copy mode keeps the terminal's
// colours and attributes, and puts only its selection, search hits and
// cursor on top (council round 1, visual 10). The zero value is the
// terminal's default.
type cellStyle struct {
	fg, bg                   ui.Color
	bold, dim, italic, under bool
	strike, reverse          bool
}

// styledLines reads what the terminal shows as VT rows (its History: SGR
// and text, one logical line to a CRLF, soft wraps joined) into lines of
// runes and the style of each rune. It reads only SGR and skips every
// other sequence. Colours by index take the theme's palette.
func styledLines(b []byte, th *terminal.Theme) ([][]rune, [][]cellStyle) {
	var lines [][]rune
	var styles [][]cellStyle
	var line []rune
	var ls []cellStyle
	var st cellStyle
	s := string(b)
	for i := 0; i < len(s); {
		ch := s[i]
		switch {
		case ch == '\r':
			i++
		case ch == '\n':
			lines, styles = append(lines, line), append(styles, ls)
			line, ls = nil, nil
			i++
		case ch == 0x1b && i+1 < len(s) && s[i+1] == '[':
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j >= len(s) {
				i = len(s)
				break
			}
			params := s[i+2 : j]
			switch s[j] {
			case 'm':
				st = applySGR(st, params, th)
			case 'C':
				// Cursor forward: blank cells.
				n, _ := strconv.Atoi(params)
				for range max(n, 1) {
					line, ls = append(line, ' '), append(ls, st)
				}
			}
			i = j + 1
		case ch == 0x1b && i+1 < len(s) && s[i+1] == ']':
			// An OSC (a hyperlink): to its BEL or ST.
			j := i + 2
			for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			if j < len(s) && s[j] == 0x1b {
				j++
			}
			i = j + 1
		case ch == 0x1b:
			i += 2
		case ch < 0x20:
			i++
		default:
			r, n := utf8.DecodeRuneInString(s[i:])
			line, ls = append(line, r), append(ls, st)
			i += n
		}
	}
	if len(line) > 0 {
		lines, styles = append(lines, line), append(styles, ls)
	}
	// The lines' trailing blanks go, as wrapRows trims them.
	for k, l := range lines {
		n := len(l)
		for n > 0 && l[n-1] == ' ' && styles[k][n-1].bg == (ui.Color{}) {
			n--
		}
		lines[k], styles[k] = l[:n], styles[k][:n]
	}
	return lines, styles
}

// applySGR is st after the SGR parameters p.
func applySGR(st cellStyle, p string, th *terminal.Theme) cellStyle {
	if p == "" {
		return cellStyle{}
	}
	f := strings.FieldsFunc(p, func(r rune) bool { return r == ';' || r == ':' })
	num := func(k int) int {
		if k < len(f) {
			n, _ := strconv.Atoi(f[k])
			return n
		}
		return 0
	}
	for k := 0; k < len(f); k++ {
		n := num(k)
		switch {
		case n == 0:
			st = cellStyle{}
		case n == 1:
			st.bold = true
		case n == 2:
			st.dim = true
		case n == 3:
			st.italic = true
		case n == 4:
			st.under = true
		case n == 7:
			st.reverse = true
		case n == 9:
			st.strike = true
		case n == 22:
			st.bold, st.dim = false, false
		case n == 23:
			st.italic = false
		case n == 24:
			st.under = false
		case n == 27:
			st.reverse = false
		case n == 29:
			st.strike = false
		case n >= 30 && n <= 37:
			st.fg = th.Palette[n-30]
		case n >= 90 && n <= 97:
			st.fg = th.Palette[n-90+8]
		case n == 39:
			st.fg = ui.Color{}
		case n >= 40 && n <= 47:
			st.bg = th.Palette[n-40]
		case n >= 100 && n <= 107:
			st.bg = th.Palette[n-100+8]
		case n == 49:
			st.bg = ui.Color{}
		case n == 38 || n == 48 || n == 58:
			var c ui.Color
			switch num(k + 1) {
			case 5:
				c = indexColor(num(k+2), th)
				k += 2
			case 2:
				c = ui.RGB(uint8(num(k+2)), uint8(num(k+3)), uint8(num(k+4)))
				k += 4
			}
			switch n {
			case 38:
				st.fg = c
			case 48:
				st.bg = c
			}
		}
	}
	return st
}

// indexColor is a colour of the 256: the theme's 16, the 6x6x6 cube and
// the grays.
func indexColor(i int, th *terminal.Theme) ui.Color {
	switch {
	case i < 0:
		return ui.Color{}
	case i < 16:
		return th.Palette[i]
	case i < 232:
		i -= 16
		v := func(n int) uint8 {
			if n == 0 {
				return 0
			}
			return uint8(55 + 40*n)
		}
		return ui.RGB(v(i/36), v(i/6%6), v(i%6))
	case i < 256:
		g := uint8(8 + 10*(i-232))
		return ui.RGB(g, g, g)
	}
	return ui.Color{}
}

// wrapStyled cuts styled lines into rows of cols, as wrapRows does.
func wrapStyled(lines [][]rune, styles [][]cellStyle, cols int) ([][]rune, [][]cellStyle) {
	cols = max(cols, 1)
	var rows [][]rune
	var rs [][]cellStyle
	for k, l := range lines {
		s := styles[k]
		for len(l) > cols {
			rows, rs = append(rows, l[:cols]), append(rs, s[:cols])
			l, s = l[cols:], s[cols:]
		}
		rows, rs = append(rows, l), append(rs, s)
	}
	if len(rows) == 0 {
		rows, rs = [][]rune{{}}, [][]cellStyle{{}}
	}
	return rows, rs
}

// span is the cell style as a span of text, on the theme's default
// foreground fg and background bg.
func (st cellStyle) span(text string, fg, bg ui.Color) ui.Span {
	sp := ui.Span{Text: text, Color: st.fg, Italic: st.italic, Underline: st.under, Strikethrough: st.strike}
	if sp.Color == (ui.Color{}) {
		sp.Color = fg
	}
	if st.bold {
		sp.Weight = wSemibold
	}
	if st.bg != (ui.Color{}) {
		sp.Background = st.bg
	}
	if st.reverse {
		b := sp.Background
		if b == (ui.Color{}) {
			b = bg
		}
		sp.Color, sp.Background = b, sp.Color
	}
	if st.dim {
		sp.Color = sp.Color.Mix(bg, 0.4)
	}
	return sp
}
