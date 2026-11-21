package snapshot

import (
	"encoding/binary"
	"fmt"
)

// PackedFormatVersion names the packed layout below. Bump it with any change
// to what pack writes or walkPacked reads: saved pane history
// (scrollback_persist.go) stores packed rows on disk and takes its version
// from this one.
const PackedFormatVersion = 1

func (c *CellState) isBlank() bool {
	return c.Content == " " && c.Width == 1 && c.StyleState == StyleState{}
}

// Pack moves the snapshot's cells into their packed form, if they are not
// there already. Only the cell grids move; every scalar field stays where the
// older reader expects it.
func (st *State) Pack() {
	if st == nil || st.Packed() {
		return
	}
	p := newRowPacker()
	st.PackedScreen = p.pack(st.Screen)
	st.PackedScrollback = p.pack(st.Scrollback)
	st.PackedMain = p.pack(st.MainScreen)
	st.Styles = p.styles
	st.Screen, st.Scrollback, st.MainScreen = nil, nil, nil
}

// Packed reports whether the cells are in the packed form. The style table is
// never empty on a packed snapshot, since index zero is always the default.
func (st *State) Packed() bool {
	return len(st.Styles) > 0
}

// Unpack restores the cell grids from the packed form, for a reader that wants
// Screen, Scrollback and MainScreen as cells. Apply does not need
// it. A malformed blob is an error.
func (st *State) Unpack() error {
	if st == nil || !st.Packed() {
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

// CheckPacked reports whether every packed grid decodes, without keeping the
// cells. A client runs it on receipt, so a malformed snapshot fails the
// request instead of being applied up to its first bad byte. A state that is
// not packed passes.
func (st *State) CheckPacked() error {
	if st == nil || !st.Packed() {
		return nil
	}
	nop := func(int, []packedCell) error { return nil }
	for _, blob := range [][]byte{st.PackedScreen, st.PackedScrollback, st.PackedMain} {
		if err := walkPacked(blob, len(st.Styles), nop); err != nil {
			return err
		}
	}
	return nil
}

type rowPacker struct {
	index  map[StyleState]uint32
	styles []StyleState
}

func newRowPacker() *rowPacker {
	return &rowPacker{index: map[StyleState]uint32{{}: 0}, styles: []StyleState{{}}}
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

func (p *rowPacker) pack(rows [][]CellState) []byte {
	if rows == nil {
		return nil
	}
	b := newPackedRows(64 + len(rows)*32)
	for _, row := range rows {
		b.add(p, row)
	}
	return b.blob()
}

func (p *rowPacker) appendRow(buf []byte, row []CellState) []byte {
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
	return buf
}

type packedRows struct {
	rows int
	buf  []byte
}

func newPackedRows(capacity int) packedRows {
	buf := make([]byte, binary.MaxVarintLen64, binary.MaxVarintLen64+capacity)
	return packedRows{buf: buf}
}

func (b *packedRows) add(p *rowPacker, row []CellState) {
	b.rows++
	b.buf = p.appendRow(b.buf, row)
}

func (b *packedRows) blob() []byte {
	var count [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(count[:], uint64(b.rows))
	start := binary.MaxVarintLen64 - n
	copy(b.buf[start:], count[:n])
	return b.buf[start:]
}

type packedCell struct {
	content string
	width   int
	style   uint32
}

var packedBlank = packedCell{content: " ", width: 1}

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
