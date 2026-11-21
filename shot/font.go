package shot

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"io"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Font is one face of a font file: TrueType, OpenType, or a collection of
// either. The PNG backend rasterizes with it. SVG and HTML embed it only when
// Frame.EmbedFont allows that.
type Font struct {
	// Data is the whole font file. The renderer reads it and does not keep it
	// past the render.
	Data []byte
	// File reads the font file in place of Data, when Data is empty. A
	// render reads only the tables and glyphs it uses, which for a colour
	// emoji font (Apple Color Emoji is about 190 MB) is a few hundred KB.
	// It must stay readable for the render.
	File io.ReaderAt
	// Index selects a face when Data is a collection (.ttc, .otc). Zero is the
	// first face, and the only one of a plain font file.
	Index int
}

func (f Font) present() bool { return len(f.Data) > 0 || f.File != nil }

func (f Font) reader() io.ReaderAt {
	if len(f.Data) > 0 {
		return bytes.NewReader(f.Data)
	}
	return f.File
}

type faceSet struct {
	faces []faceEntry
	size  float64
	opts  *opentype.FaceOptions
	buf   sfnt.Buffer
	picks map[rune]int
	tiles map[colorTileKey]*image.RGBA
	seqs  map[string]seqGlyph
}

type colorTileKey struct {
	face, glyph, w, h int
}

type faceEntry struct {
	src      Font
	parsed   bool
	font     *sfnt.Font
	face     font.Face
	bold     bool
	boldOnly bool
	color    *colorFace
}

func loadFaces(f *Frame, size float64) (*faceSet, error) {
	regular, bold, err := goMonoFonts()
	if err != nil {
		return nil, fmt.Errorf("inflate embedded font: %w", err)
	}
	goMono := []faceEntry{
		{src: Font{Data: bold}, bold: true, boldOnly: true},
		{src: Font{Data: regular}},
	}
	fs := &faceSet{
		faces: goMono,
		size:  size,
		opts:  &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull},
		picks: map[rune]int{},
	}
	if f == nil {
		return fs, fs.parse(&fs.faces[1])
	}
	own := f.Font.present()
	if own {
		fs.faces = []faceEntry{
			{src: f.BoldFont, bold: true, boldOnly: true},
			{src: f.Font},
		}
	}
	for _, fb := range f.Fallback {
		fs.faces = append(fs.faces, faceEntry{src: fb})
	}
	if own {
		fs.faces = append(fs.faces, goMono...)
	}
	return fs, fs.parse(&fs.faces[1])
}

func (fs *faceSet) parse(e *faceEntry) error {
	if e.parsed {
		return nil
	}
	e.parsed = true
	if !e.src.present() {
		return errors.New("parse font: no font data")
	}
	var coll *sfnt.Collection
	var err error
	if len(e.src.Data) > 0 {
		coll, err = sfnt.ParseCollection(e.src.Data)
	} else {
		coll, err = sfnt.ParseCollectionReaderAt(e.src.File)
	}
	if err != nil {
		return fmt.Errorf("parse font: %w", err)
	}
	index := e.src.Index
	if index < 0 || index >= coll.NumFonts() {
		index = 0
	}
	sf, err := coll.Font(index)
	if err != nil {
		return fmt.Errorf("font %d of collection: %w", index, err)
	}
	face, err := opentype.NewFace(sf, fs.opts)
	if err != nil {
		return fmt.Errorf("font face: %w", err)
	}
	e.font, e.face = sf, face
	e.color = readColorFace(e.src.reader(), index, sf.NumGlyphs())
	return nil
}

func (fs *faceSet) pick(r rune, bold bool) (font.Face, bool) {
	key := r * 2
	if bold {
		key++
	}
	i, ok := fs.picks[key]
	if !ok {
		i = fs.search(r, bold)
		fs.picks[key] = i
	}
	if i < 0 {
		return nil, false
	}
	return fs.faces[i].face, bold && !fs.faces[i].bold
}

func (fs *faceSet) search(r rune, bold bool) int {
	for i := range fs.faces {
		e := &fs.faces[i]
		if e.boldOnly && !bold {
			continue
		}
		if !e.parsed {
			_ = fs.parse(e)
		}
		if e.font == nil {
			continue
		}
		idx, err := e.font.GlyphIndex(&fs.buf, r)
		if err != nil || idx == 0 {
			continue
		}
		if e.color != nil && !e.color.has(int(idx)) && !fs.hasOutline(e, idx) {
			continue
		}
		return i
	}
	return -1
}

func (fs *faceSet) hasOutline(e *faceEntry, idx sfnt.GlyphIndex) bool {
	segs, err := e.font.LoadGlyph(&fs.buf, idx, fixed.I(16), nil)
	return err == nil && len(segs) > 0
}

func (fs *faceSet) pickColor(r rune, bold bool, w, h int) *image.RGBA {
	if w <= 0 || h <= 0 {
		return nil
	}
	fs.pick(r, bold)
	i := fs.picks[r*2+boolInt(bold)]
	if i < 0 {
		return nil
	}
	e := &fs.faces[i]
	if e.color == nil {
		return nil
	}
	idx, err := e.font.GlyphIndex(&fs.buf, r)
	if err != nil || idx == 0 {
		return nil
	}
	return fs.colorTile(i, int(idx), w, h)
}

func (fs *faceSet) colorTile(i, gid, w, h int) *image.RGBA {
	key := colorTileKey{face: i, glyph: gid, w: w, h: h}
	if t, ok := fs.tiles[key]; ok {
		return t
	}
	var tile *image.RGBA
	if data, ok := fs.faces[i].color.bitmap(gid, h); ok {
		if src, ok := decodeColorBitmap(data); ok {
			tile = fitColorTile(src, w, h)
		}
	}
	if fs.tiles == nil {
		fs.tiles = map[colorTileKey]*image.RGBA{}
	}
	fs.tiles[key] = tile
	return tile
}

type seqGlyph struct{ face, glyph int }

func (fs *faceSet) pickSequence(cl string, bold bool, w, h int) *image.RGBA {
	if w <= 0 || h <= 0 {
		return nil
	}
	key := cl
	if bold {
		key += "\x00"
	}
	sg, ok := fs.seqs[key]
	if !ok {
		sg = fs.searchSequence(cl, bold)
		if fs.seqs == nil {
			fs.seqs = map[string]seqGlyph{}
		}
		fs.seqs[key] = sg
	}
	if sg.face < 0 {
		return nil
	}
	return fs.colorTile(sg.face, sg.glyph, w, h)
}

func (fs *faceSet) searchSequence(cl string, bold bool) seqGlyph {
	var glyphs []int
faces:
	for i := range fs.faces {
		e := &fs.faces[i]
		if e.boldOnly && !bold {
			continue
		}
		if !e.parsed {
			_ = fs.parse(e)
		}
		if e.font == nil || e.color == nil {
			continue
		}
		glyphs = glyphs[:0]
		for _, r := range cl {
			idx, err := e.font.GlyphIndex(&fs.buf, r)
			switch {
			case err == nil && idx != 0:
				glyphs = append(glyphs, int(idx))
			case r == 0xFE0F || r == 0xFE0E:
			default:
				continue faces
			}
		}
		if len(glyphs) == 0 {
			continue
		}
		if g := e.color.shaper().shape(glyphs)[0]; e.color.has(g) {
			return seqGlyph{face: i, glyph: g}
		}
	}
	return seqGlyph{face: -1}
}

func fitColorTile(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	if b.Dx() > w || b.Dy() > h {
		return Downscale(rgba, w, h)
	}
	scale := min(float64(w)/float64(b.Dx()), float64(h)/float64(b.Dy()), 2)
	ow, oh := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
	out := image.NewRGBA(image.Rect(0, 0, ow, oh))
	ScaleBilinear(out, out.Bounds(), rgba, rgba.Bounds(), false)
	return out
}

func boolInt(b bool) rune {
	if b {
		return 1
	}
	return 0
}

func (fs *faceSet) cellSize() (float64, float64) {
	face := fs.faces[1].face
	adv, ok := face.GlyphAdvance('M')
	if !ok || adv == 0 {
		adv = fixed.I(int(fs.size * 0.6))
	}
	cw := fixedToFloat(adv)
	ch := fixedToFloat(face.Metrics().Height)
	if ch <= 0 {
		ch = fs.size * 1.25
	}
	return cw, ch
}

// MissingGlyphs returns the runes of g that no face of f can draw, in the
// order they first appear and without repeats: not the font, not a fallback,
// not the built-in Go Mono.
// Box drawing, block elements, braille and powerline glyphs are drawn as
// shapes and are never missing.
//
// A caller with a font database uses it to fill Frame.Fallback: ask the
// database for a font that covers each missing rune, add the answer, and call
// MissingGlyphs again to skip the runes that font also covers.
func MissingGlyphs(g *Grid, f *Frame) ([]rune, error) {
	if g == nil {
		return nil, nil
	}
	fs, err := loadFaces(f, pngBaseFontSize)
	if err != nil {
		return nil, err
	}
	var missing []rune
	for _, row := range g.Cells {
		for _, c := range row {
			missing = fs.appendMissing(missing, firstRune(c.Cluster))
		}
	}
	if f != nil && f.Mode == FrameWindow {
		if f.Controls == ControlsGlyphs {
			for _, m := range []string{f.CloseGlyph, f.MinimizeGlyph, f.MaximizeGlyph} {
				missing = fs.appendMissing(missing, firstRune(m))
			}
		}
		for _, r := range f.Title {
			missing = fs.appendMissing(missing, r)
		}
	}
	return missing, nil
}

func (fs *faceSet) appendMissing(missing []rune, r rune) []rune {
	if r == 0 || r == ' ' || isProcedural(r) {
		return missing
	}
	if _, seen := fs.picks[r*2]; seen {
		return missing
	}
	if face, _ := fs.pick(r, false); face == nil {
		missing = append(missing, r)
	}
	return missing
}
