package session

import (
	"encoding/binary"
	"fmt"
)

// The packed form of a snapshot's cells.
//
// A TerminalState carries its screen and history as [][]CellState, and gob
// writes every cell as a struct: a field tag, a length and the bytes for the
// content, a tag and a value for the width, and the same again for each colour
// set, then a terminator. That is about thirteen bytes for a plain letter, so
// a 207x55 screen is 147 KB and a thousand rows of history is 2.9 MB, per pane,
// and the client decodes every one of those structs with reflection on its UI
// goroutine during a workspace switch.
//
// The packed form is the same cells with the repetition taken out. Styles go in
// a table once and each cell names its index, cells in one style are run
// together, a plain letter is two bytes, and the blank tail of a row is left
// off. The cells are the same on both sides: Pack and unpack round trip through
// the [][]CellState form, so ApplyTerminalState reads a packed snapshot with
// the same code as an unpacked one, and every fidelity test that covers the
// cell form covers this one by running it through both.
//
// It is negotiated per request, with no protocol bump. A client that wants it
// asks (GetTerminalStatePayload.Packed); a daemon that predates the field does
// not see the request and answers with cells, which the client still reads;
// and a client that predates it never asks, so a newer daemon answers it with
// cells too. The client unpacks as soon as the reply is decoded, so nothing
// past TUIClient.GetTerminalState sees anything but cells.

// isBlankCell reports whether a cell is what a never-written cell looks like,
// which is what a row's tail is trimmed down to. A wide rune's continuation is
// empty too, but it is not blank: writing a blank over it would empty the
// rune it belongs to, so a tail stops short of one.
func (c *CellState) isBlank() bool {
	return c.Content == " " && c.Width == 1 && c.StyleState == StyleState{}
}

// Pack moves the snapshot's cells into their packed form, if they are not
// there already. Only the cell grids move; every scalar field stays where the
// older reader expects it.
func (st *TerminalState) Pack() {
	if st == nil || st.isPacked() {
		return
	}
	p := &rowPacker{index: map[StyleState]uint32{{}: 0}, styles: []StyleState{{}}}
	st.PackedScreen = p.pack(st.Screen)
	st.PackedScrollback = p.pack(st.Scrollback)
	st.PackedMain = p.pack(st.MainScreen)
	st.Styles = p.styles
	st.Screen, st.Scrollback, st.MainScreen = nil, nil, nil
}

// isPacked reports whether the cells travelled packed. The style table is
// never empty on a packed snapshot, since index zero is always the default.
func (st *TerminalState) isPacked() bool {
	return len(st.Styles) > 0
}

// unpack restores the cell grids from the packed form, so every reader of a
// snapshot sees the cells it has always read: TUIClient.GetTerminalState
// unpacks before it returns, and ApplyTerminalState unpacks in case it is
// handed a packed one directly. A malformed blob unpacks to no rows at all
// rather than to a partial screen.
func (st *TerminalState) unpack() error {
	if st == nil || !st.isPacked() {
		return nil
	}
	var err error
	if st.Screen, err = unpackRows(st.PackedScreen, st.Styles); err != nil {
		return err
	}
	if st.Scrollback, err = unpackRows(st.PackedScrollback, st.Styles); err != nil {
		return err
	}
	if st.MainScreen, err = unpackRows(st.PackedMain, st.Styles); err != nil {
		return err
	}
	st.PackedScreen, st.PackedScrollback, st.PackedMain, st.Styles = nil, nil, nil, nil
	return nil
}

type rowPacker struct {
	index  map[StyleState]uint32
	styles []StyleState
}

func (p *rowPacker) styleIndex(s StyleState) uint32 {
	if i, ok := p.index[s]; ok {
		return i
	}
	i := uint32(len(p.styles))
	p.styles = append(p.styles, s)
	p.index[s] = i
	return i
}

// pack writes rows as:
//
//	uvarint rows
//	per row: uvarint width, uvarint cells kept, then runs until kept is used up
//	per run: uvarint style index, uvarint run length, then that many cells
//	per cell: uvarint len(content)<<2 | widthCode, [uvarint width], content
//
// widthCode is the cell width for 0, 1 and 2; 3 means the width follows.
func (p *rowPacker) pack(rows [][]CellState) []byte {
	if rows == nil {
		return nil
	}
	buf := make([]byte, 0, 64+len(rows)*32)
	buf = binary.AppendUvarint(buf, uint64(len(rows)))
	for _, row := range rows {
		kept := len(row)
		for kept > 0 && row[kept-1].isBlank() {
			kept--
		}
		buf = binary.AppendUvarint(buf, uint64(len(row)))
		buf = binary.AppendUvarint(buf, uint64(kept))
		for i := 0; i < kept; {
			style := row[i].StyleState
			j := i + 1
			for j < kept && row[j].StyleState == style {
				j++
			}
			buf = binary.AppendUvarint(buf, uint64(p.styleIndex(style)))
			buf = binary.AppendUvarint(buf, uint64(j-i))
			for _, c := range row[i:j] {
				w := max(c.Width, 0)
				code := uint64(w)
				if w > 2 {
					code = 3
				}
				buf = binary.AppendUvarint(buf, uint64(len(c.Content))<<2|code)
				if code == 3 {
					buf = binary.AppendUvarint(buf, uint64(w))
				}
				buf = append(buf, c.Content...)
			}
			i = j
		}
	}
	return buf
}

// packedCell is one cell as the walker hands it out: the content as a
// substring of the blob, the width, and the index into the style table.
type packedCell struct {
	content string
	width   int
	style   uint32
}

// packedBlank is the cell a trimmed row tail stands for, as the walker fills
// it in.
var packedBlank = packedCell{content: " ", width: 1}

// walkPacked is the one reader of the packed form. It decodes blob row by row
// and hands each row to fn at its full width, the trimmed tail refilled with
// blanks, so a reader writes every row whole into an emulator that may still
// be showing an older one. The row slice is reused between calls. Cell
// contents are substrings of one copy of the blob, so a cell costs no
// allocation of its own.
//
// A malformed blob stops at the first bad byte with an error. Rows already
// handed out stay handed out; the callers below treat an error as a snapshot
// that carried no cells, which is what a snapshot from before the field
// looked like.
func walkPacked(blob []byte, nStyles int, fn func(y int, cells []packedCell) error) error {
	if blob == nil {
		return nil
	}
	r := &blobReader{s: string(blob)}
	nrows, err := r.uvarint()
	if err != nil {
		return err
	}
	if nrows > uint64(len(r.s)) {
		return fmt.Errorf("packed snapshot: %d rows in %d bytes", nrows, len(r.s))
	}
	var row []packedCell
	for y := range int(nrows) {
		width, err := r.uvarint()
		if err != nil {
			return err
		}
		kept, err := r.uvarint()
		if err != nil {
			return err
		}
		if kept > width {
			return fmt.Errorf("packed snapshot: row keeps %d cells of %d", kept, width)
		}
		// A blank row is two bytes whatever its width, so the width is not
		// bounded by the blob; it is bounded here instead.
		if width > 1<<16 {
			return fmt.Errorf("packed snapshot: row of %d cells", width)
		}
		if uint64(cap(row)) < width {
			row = make([]packedCell, width)
		}
		row = row[:width]
		for i := uint64(0); i < kept; {
			idx, err := r.uvarint()
			if err != nil {
				return err
			}
			if idx >= uint64(nStyles) {
				return fmt.Errorf("packed snapshot: style %d of %d", idx, nStyles)
			}
			run, err := r.uvarint()
			if err != nil {
				return err
			}
			if run == 0 || i+run > kept {
				return fmt.Errorf("packed snapshot: run of %d at cell %d of %d", run, i, kept)
			}
			for range run {
				hdr, err := r.uvarint()
				if err != nil {
					return err
				}
				n := int(hdr >> 2)
				cw := int(hdr & 3)
				if cw == 3 {
					w, err := r.uvarint()
					if err != nil {
						return err
					}
					cw = int(w)
				}
				content, err := r.take(n)
				if err != nil {
					return err
				}
				row[i] = packedCell{content: content, width: cw, style: uint32(idx)}
				i++
			}
		}
		for i := kept; i < width; i++ {
			row[i] = packedBlank
		}
		if err := fn(y, row); err != nil {
			return err
		}
	}
	return nil
}

// packedRowCount reads how many rows a blob holds without decoding them.
func packedRowCount(blob []byte) int {
	if blob == nil {
		return 0
	}
	n, err := (&blobReader{s: string(blob)}).uvarint()
	if err != nil {
		return 0
	}
	return int(n)
}

// unpackRows is walkPacked materialised as cells. Cell contents are
// substrings of one copy of the blob, so a cell costs no allocation of its
// own beyond the row arrays.
func unpackRows(blob []byte, styles []StyleState) ([][]CellState, error) {
	if blob == nil {
		return nil, nil
	}
	rows := make([][]CellState, 0, packedRowCount(blob))
	err := walkPacked(blob, len(styles), func(_ int, cells []packedCell) error {
		row := make([]CellState, len(cells))
		for x, pc := range cells {
			row[x] = CellState{Content: pc.content, Width: pc.width, StyleState: styles[pc.style]}
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// blobReader reads varints and substrings out of one string.
type blobReader struct {
	s   string
	pos int
}

func (r *blobReader) uvarint() (uint64, error) {
	var v uint64
	var shift uint
	for i := 0; ; i++ {
		if r.pos >= len(r.s) {
			return 0, fmt.Errorf("packed snapshot: truncated at byte %d", r.pos)
		}
		b := r.s[r.pos]
		r.pos++
		if i == binary.MaxVarintLen64 {
			return 0, fmt.Errorf("packed snapshot: varint overflow at byte %d", r.pos)
		}
		v |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return v, nil
		}
		shift += 7
	}
}

func (r *blobReader) take(n int) (string, error) {
	if n < 0 || r.pos+n > len(r.s) {
		return "", fmt.Errorf("packed snapshot: %d bytes wanted at byte %d of %d", n, r.pos, len(r.s))
	}
	s := r.s[r.pos : r.pos+n]
	r.pos += n
	return s, nil
}
