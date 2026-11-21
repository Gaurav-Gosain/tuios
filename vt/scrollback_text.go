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
	size := sb.Len() + e.Height()*(e.Width()+1)
	for i := range sb.Len() {
		size += len(sb.lines[sb.slot(i)])
	}
	buf.Grow(size)
	for i := range sb.Len() {
		sb.appendLineText(buf, sb.lines[sb.slot(i)])
		buf.WriteByte('\n')
	}
}

func (sb *Scrollback) appendLineText(buf *strings.Builder, data []byte) {
	width, n := binary.Uvarint(data)
	if n <= 0 {
		return
	}
	plain := true
	var style uv.Style
	var link uv.Link
	var links []uv.Link
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
			var ok bool
			link, links, i, ok = readLink(data, i+1, links)
			if !ok {
				return
			}
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
			case plain && w == 1 && content == " ":
				pending++
			default:
				flush()
				buf.WriteString(content)
			}
		default:
			if c := data[i]; c < utf8.RuneSelf {
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
			flush()
			buf.WriteString(content)
		}
	}
}
