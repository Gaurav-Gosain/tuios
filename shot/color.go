package shot

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"io"
)

// Colour glyphs: the bitmap emoji of a colour font.
//
// A colour emoji font has no outline for its emoji. Apple Color Emoji keeps a
// PNG per glyph and size in its sbix table; Noto Color Emoji keeps them in
// CBDT, indexed by CBLC. x/image's sfnt reads neither, and gives no access to
// tables it does not read, so this file reads the table directory itself and
// then only the bytes of the glyphs a render draws.
//
// Every offset and count comes from the font file, so each read is bounded by
// the table it is in, and a table that does not add up leaves the face with
// no colour glyphs: it never panics and never reads past the table.
//
// COLR (layered outlines) is not read yet: no machine this was checked on has
// such a font.

// maxColorBitmap bounds the side of a glyph bitmap a render decodes. Emoji
// strikes go up to 160 px (Apple) and 136 px (Noto).
const maxColorBitmap = 1024

// colorFace is the colour tables of one face.
type colorFace struct {
	src       io.ReaderAt
	numGlyphs int
	// sbix strikes, by ppem, and their offsets in the file.
	sbix []sbixStrike
	// cblc sizes, and the file offsets of CBLC and CBDT.
	cblc       []cblcSize
	cblcOffset int64
	cblcLength int64
	cbdtOffset int64
	cbdtLength int64
}

type sbixStrike struct {
	ppem   int
	offset int64 // of the strike in the file
}

type cblcSize struct {
	ppem          int
	first, last   int // glyph range of the size
	arrayOffset   int64
	numSubtables  int
	indexTableEnd int64
}

// tableRecord is one entry of the table directory.
type tableRecord struct {
	offset, length int64
}

// readColorFace finds the colour tables of face index of src, or nil when the
// face has none it can read. numGlyphs is the face's glyph count.
func readColorFace(src io.ReaderAt, index, numGlyphs int) *colorFace {
	tables, ok := readTableDirectory(src, index)
	if !ok {
		return nil
	}
	cf := &colorFace{src: src, numGlyphs: numGlyphs}
	if t, ok := tables["sbix"]; ok {
		cf.sbix = readSbix(src, t, numGlyphs)
	}
	cblc, okL := tables["CBLC"]
	cbdt, okD := tables["CBDT"]
	if okL && okD {
		cf.cblcOffset, cf.cblcLength = cblc.offset, cblc.length
		cf.cbdtOffset, cf.cbdtLength = cbdt.offset, cbdt.length
		cf.cblc = readCBLC(src, cblc)
	}
	if len(cf.sbix) == 0 && len(cf.cblc) == 0 {
		return nil
	}
	return cf
}

// readAt reads n bytes at off, or reports false.
func readAt(src io.ReaderAt, off int64, n int) ([]byte, bool) {
	if off < 0 || n < 0 || n > 1<<26 {
		return nil, false
	}
	b := make([]byte, n)
	if _, err := src.ReadAt(b, off); err != nil {
		return nil, false
	}
	return b, true
}

func u16(b []byte, i int) int { return int(binary.BigEndian.Uint16(b[i:])) }
func u32(b []byte, i int) int64 {
	return int64(binary.BigEndian.Uint32(b[i:]))
}

// readTableDirectory reads the table records of face index: the collection
// header first when the file is a .ttc.
func readTableDirectory(src io.ReaderAt, index int) (map[string]tableRecord, bool) {
	head, ok := readAt(src, 0, 12)
	if !ok {
		return nil, false
	}
	var faceOff int64
	if string(head[:4]) == "ttcf" {
		n := u32(head, 8)
		if index < 0 || int64(index) >= n || n > 4096 {
			index = 0
		}
		b, ok := readAt(src, 12+4*int64(index), 4)
		if !ok {
			return nil, false
		}
		faceOff = u32(b, 0)
	}
	off, ok := readAt(src, faceOff, 12)
	if !ok {
		return nil, false
	}
	n := u16(off, 4)
	if n == 0 || n > 512 {
		return nil, false
	}
	recs, ok := readAt(src, faceOff+12, 16*n)
	if !ok {
		return nil, false
	}
	out := map[string]tableRecord{}
	for i := range n {
		r := recs[16*i:]
		out[string(r[:4])] = tableRecord{offset: u32(r, 8), length: u32(r, 12)}
	}
	return out, true
}

// readSbix reads the strikes of an sbix table.
func readSbix(src io.ReaderAt, t tableRecord, numGlyphs int) []sbixStrike {
	head, ok := readAt(src, t.offset, 8)
	if !ok {
		return nil
	}
	n := u32(head, 4)
	if n <= 0 || n > 64 || 8+4*n > t.length {
		return nil
	}
	offs, ok := readAt(src, t.offset+8, int(4*n))
	if !ok {
		return nil
	}
	var out []sbixStrike
	for i := range int(n) {
		so := u32(offs, 4*i)
		// The strike's header and its glyph offsets must be in the table.
		if so+4+4*int64(numGlyphs+1) > t.length {
			continue
		}
		sh, ok := readAt(src, t.offset+so, 4)
		if !ok {
			continue
		}
		out = append(out, sbixStrike{ppem: u16(sh, 0), offset: t.offset + so})
	}
	return out
}

// readCBLC reads the bitmap sizes of a CBLC table.
func readCBLC(src io.ReaderAt, t tableRecord) []cblcSize {
	head, ok := readAt(src, t.offset, 8)
	if !ok {
		return nil
	}
	n := u32(head, 4)
	if n <= 0 || n > 64 || 8+48*n > t.length {
		return nil
	}
	recs, ok := readAt(src, t.offset+8, int(48*n))
	if !ok {
		return nil
	}
	var out []cblcSize
	for i := range int(n) {
		r := recs[48*i:]
		arr, size, num := u32(r, 0), u32(r, 4), u32(r, 8)
		if num <= 0 || num > 4096 || arr+size > t.length || 8*num > size {
			continue
		}
		out = append(out, cblcSize{
			ppem: int(r[45]), first: u16(r, 40), last: u16(r, 42),
			arrayOffset: arr, numSubtables: int(num), indexTableEnd: arr + size,
		})
	}
	return out
}

// has reports whether the face has a colour form of glyph gid.
func (cf *colorFace) has(gid int) bool {
	_, ok := cf.bitmap(gid, 0)
	return ok
}

// bitmap is the PNG bytes of glyph gid at the strike that suits a box px
// tall: the smallest strike at least that big, else the biggest. px 0 takes
// any strike, for the coverage question.
func (cf *colorFace) bitmap(gid, px int) ([]byte, bool) {
	if gid <= 0 || gid >= cf.numGlyphs {
		return nil, false
	}
	if len(cf.sbix) > 0 {
		for _, s := range orderStrikes(len(cf.sbix), px, func(i int) int { return cf.sbix[i].ppem }) {
			if data, ok := cf.sbixGlyph(cf.sbix[s], gid, 1); ok {
				return data, true
			}
		}
	}
	for _, s := range orderStrikes(len(cf.cblc), px, func(i int) int { return cf.cblc[i].ppem }) {
		if data, ok := cf.cbdtGlyph(cf.cblc[s], gid); ok {
			return data, true
		}
	}
	return nil, false
}

// orderStrikes lists the strikes to try for a box px tall: those at least px
// big, smallest first, then the rest, biggest first.
func orderStrikes(n, px int, ppem func(int) int) []int {
	var up, down []int
	for i := range n {
		if ppem(i) >= px {
			up = append(up, i)
		} else {
			down = append(down, i)
		}
	}
	for i := 1; i < len(up); i++ {
		for j := i; j > 0 && ppem(up[j]) < ppem(up[j-1]); j-- {
			up[j], up[j-1] = up[j-1], up[j]
		}
	}
	for i := 1; i < len(down); i++ {
		for j := i; j > 0 && ppem(down[j]) > ppem(down[j-1]); j-- {
			down[j], down[j-1] = down[j-1], down[j]
		}
	}
	return append(up, down...)
}

// sbixGlyph reads glyph gid of strike s: a PNG, or a dupe that names another
// glyph of the strike, followed hops times at most.
func (cf *colorFace) sbixGlyph(s sbixStrike, gid, hops int) ([]byte, bool) {
	offs, ok := readAt(cf.src, s.offset+4+4*int64(gid), 8)
	if !ok {
		return nil, false
	}
	start, end := u32(offs, 0), u32(offs, 4)
	if end-start <= 8 || end-start > 4<<20 {
		return nil, false
	}
	rec, ok := readAt(cf.src, s.offset+start, int(end-start))
	if !ok {
		return nil, false
	}
	switch string(rec[4:8]) {
	case "png ":
		return rec[8:], true
	case "dupe":
		if hops > 0 && len(rec) >= 10 {
			return cf.sbixGlyph(s, u16(rec, 8), hops-1)
		}
	}
	// jpg and tiff are not drawn: no colour emoji font uses them.
	return nil, false
}

// cbdtGlyph reads glyph gid at size s from CBDT, through its CBLC index.
func (cf *colorFace) cbdtGlyph(s cblcSize, gid int) ([]byte, bool) {
	if gid < s.first || gid > s.last {
		return nil, false
	}
	arr, ok := readAt(cf.src, cf.cblcOffset+s.arrayOffset, 8*s.numSubtables)
	if !ok {
		return nil, false
	}
	for i := range s.numSubtables {
		e := arr[8*i:]
		first, last, add := u16(e, 0), u16(e, 2), u32(e, 4)
		if gid < first || gid > last {
			continue
		}
		sub := s.arrayOffset + add
		if sub+8 > s.indexTableEnd {
			return nil, false
		}
		h, ok := readAt(cf.src, cf.cblcOffset+sub, 8)
		if !ok {
			return nil, false
		}
		indexFormat, imageFormat, dataOff := u16(h, 0), u16(h, 2), u32(h, 4)
		off, length, ok := cf.cblcGlyphSpan(sub+8, s.indexTableEnd, indexFormat, first, last, gid)
		if !ok {
			return nil, false
		}
		return cf.cbdtImage(dataOff+off, length, imageFormat)
	}
	return nil, false
}

// cblcGlyphSpan finds glyph gid in an index subtable whose body starts at
// body (CBLC-relative) and ends by end, and returns its offset from the
// subtable's image data and its length.
func (cf *colorFace) cblcGlyphSpan(body, end int64, format, first, last, gid int) (int64, int64, bool) {
	read := func(off int64, n int) ([]byte, bool) {
		if off+int64(n) > end {
			return nil, false
		}
		return readAt(cf.src, cf.cblcOffset+off, n)
	}
	i := gid - first
	switch format {
	case 1, 3:
		w := 4
		if format == 3 {
			w = 2
		}
		b, ok := read(body+int64(w*i), 2*w)
		if !ok {
			return 0, 0, false
		}
		get := func(j int) int64 {
			if w == 4 {
				return u32(b, j*4)
			}
			return int64(u16(b, j*2))
		}
		return get(0), get(1) - get(0), get(1) > get(0)
	case 2:
		b, ok := read(body, 4)
		if !ok {
			return 0, 0, false
		}
		size := u32(b, 0)
		return size * int64(i), size, size > 0
	case 4:
		b, ok := read(body, 4)
		if !ok {
			return 0, 0, false
		}
		n := int(u32(b, 0))
		if n <= 0 || n > last-first+1 {
			return 0, 0, false
		}
		pairs, ok := read(body+4, 4*(n+1))
		if !ok {
			return 0, 0, false
		}
		for j := range n {
			if u16(pairs, 4*j) == gid {
				o, next := int64(u16(pairs, 4*j+2)), int64(u16(pairs, 4*j+6))
				return o, next - o, next > o
			}
		}
	case 5:
		b, ok := read(body, 16)
		if !ok {
			return 0, 0, false
		}
		size, n := u32(b, 0), int(u32(b, 12))
		if size <= 0 || n <= 0 || n > last-first+1 {
			return 0, 0, false
		}
		ids, ok := read(body+16, 2*n)
		if !ok {
			return 0, 0, false
		}
		for j := range n {
			if u16(ids, 2*j) == gid {
				return size * int64(j), size, true
			}
		}
	}
	return 0, 0, false
}

// cbdtImage reads a CBDT glyph record of imageFormat 17, 18 or 19 and returns
// its PNG.
func (cf *colorFace) cbdtImage(off, length int64, format int) ([]byte, bool) {
	if length <= 0 || off+length > cf.cbdtLength || length > 4<<20 {
		return nil, false
	}
	rec, ok := readAt(cf.src, cf.cbdtOffset+off, int(length))
	if !ok {
		return nil, false
	}
	// The metrics before the length: small (5 bytes), big (8), or none.
	var m int
	switch format {
	case 17:
		m = 5
	case 18:
		m = 8
	case 19:
	default:
		return nil, false
	}
	if len(rec) < m+4 {
		return nil, false
	}
	n := int(u32(rec, m))
	if m+4+n > len(rec) {
		return nil, false
	}
	return rec[m+4 : m+4+n], true
}

// decodeColorBitmap decodes a glyph's PNG, refusing one bigger than
// maxColorBitmap on a side before it is decoded.
func decodeColorBitmap(data []byte) (image.Image, bool) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxColorBitmap || cfg.Height > maxColorBitmap {
		return nil, false
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	return img, true
}
