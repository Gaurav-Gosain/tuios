package vt

import (
	"encoding/binary"
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
)

// AppendScrollbackText writes the main screen's scrollback to buf as plain
// text, oldest line first, each line followed by a newline. The bytes are the
// same as ScrollbackLine(i).String() plus "\n" for every line, but no line is
// decoded into cells: a cell is 112 bytes, so decoding a full 10000-line ring
// to read its text allocated about 100 MB at 80 columns.
//
// It reads the ring and its intern tables and writes nothing to them, so it is
// safe under the same read lock ScrollbackLine is.
func (e *Emulator) AppendScrollbackText(buf *strings.Builder) {
	sb := e.scrs[0].scrollback
	if sb == nil {
		return
	}
	for i := range sb.Len() {
		sb.appendLineText(buf, sb.lines[sb.slot(i)])
		buf.WriteByte('\n')
	}
}

// appendLineText writes what uv.Line.String would return for
// decodeLine(data), without building the line.
//
// uv.Line.String skips a cell equal to the zero Cell, holds back a cell equal
// to EmptyCell as a pending space, and writes any other cell's content after
// the pending spaces. Spaces still pending at the end are dropped. The rules
// below are those two comparisons applied to the style and link in force:
//
//   - A zero Cell has no content, width 0, a zero style and no link.
//   - EmptyCell is " " of width 1 with a zero style and no link.
//
// Both comparisons need the style to be the zero style. Cell.IsZero compares
// with ==, and Cell.Equal compares colours by RGBA, but a colour only equals a
// nil colour when it is nil, so the two agree. A link compares with == in both.
//
// The decoder's padding past the stored cells is EmptyCell, which is only ever
// pending, so the stored cells are all that can reach buf. A record that ends
// in the middle of a token stops the decoder, and the rest of its line is
// padding, so it stops here too.
func (sb *Scrollback) appendLineText(buf *strings.Builder, data []byte) {
	width, n := binary.Uvarint(data)
	if n <= 0 {
		return
	}
	// plain is whether the style and link in force are both zero, which is
	// when a space can be pending and an empty cell of width 0 is skipped.
	plain := true
	var style uv.Style
	var link uv.Link
	pending := 0
	flush := func() {
		for ; pending > 0; pending-- {
			buf.WriteByte(' ')
		}
	}
	x := uint64(0)
	i := n
	for i < len(data) && x < width {
		switch data[i] {
		case sbStyle:
			st, j, ok := readStyle(data, i+1)
			if !ok {
				return
			}
			i = j
			style = sb.unpackStyle(st)
			plain = style == (uv.Style{}) && link == (uv.Link{})
		case sbLink:
			v, m := binary.Uvarint(data[i+1:])
			if m <= 0 {
				return
			}
			i += 1 + m
			link = sb.unpackLink(v)
			plain = style == (uv.Style{}) && link == (uv.Link{})
		case sbCell:
			if i+1 >= len(data) {
				return
			}
			w := data[i+1]
			content, j, ok := sb.readContent(data, i+2)
			if !ok {
				return
			}
			i = j
			x++
			switch {
			case plain && w == 0 && content == "":
				// The zero Cell: String skips it. A styled or linked cell
				// of width 0 is not the zero Cell, so it writes its empty
				// content and releases the pending spaces like any other.
			case plain && w == 1 && content == " ":
				pending++
			default:
				flush()
				buf.WriteString(content)
			}
		default:
			if c := data[i]; c < utf8.RuneSelf {
				// One ASCII byte is a whole cell of width one: most of a log.
				i++
				x++
				if c == ' ' && plain {
					pending++
				} else {
					flush()
					buf.WriteByte(c)
				}
				continue
			}
			content, j, ok := sb.readContent(data, i)
			if !ok {
				return
			}
			i = j
			x++
			// Width one, and not a space, which is ASCII: never skipped or
			// held back.
			flush()
			buf.WriteString(content)
		}
	}
}
