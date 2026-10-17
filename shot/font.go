package shot

import (
	"errors"
	"fmt"

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
	// Index selects a face when Data is a collection (.ttc, .otc). Zero is the
	// first face, and the only one of a plain font file.
	Index int
}

// faceSet is the order a PNG render looks for a glyph in:
//
//  1. the primary font: Frame.Font (and Frame.BoldFont for bold cells), or Go
//     Mono when the frame names no font;
//  2. Frame.Fallback, in order;
//  3. Go Mono, when the primary font is not Go Mono already.
//
// A glyph in none of them draws as a dotted box. The first face that covers a
// rune ends the search, and the answer is kept for the rest of the render, so
// a screen of one missing glyph asks each face once and not once per cell.
type faceSet struct {
	faces []faceEntry
	size  float64
	opts  *opentype.FaceOptions
	buf   sfnt.Buffer
	// picks caches the search. The key is the rune doubled, plus one for
	// bold. The value is the index of the face in faces, or -1 for none.
	picks map[rune]int
}

// faceEntry is one face in the search order.
type faceEntry struct {
	// src is the font to parse on first use. A fallback face is parsed only
	// when a rune reaches it, so a fallback list costs nothing on a screen
	// the primary font covers.
	src    Font
	parsed bool
	font   *sfnt.Font
	face   font.Face
	// bold is true for a bold cut. A regular face drawing a bold cell is
	// double-struck.
	bold bool
	// boldOnly faces answer bold cells only.
	boldOnly bool
}

// loadFaces builds the search order for a frame at one pixel size. The
// primary font must parse. A bold cut or a fallback that does not parse is
// passed over: the capture is the point, and the font is how it looks.
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
	own := len(f.Font.Data) > 0
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
	// The cell is measured from the primary face, so it is parsed now.
	return fs, fs.parse(&fs.faces[1])
}

// parse makes the face of an entry. It runs once per entry: a failure leaves
// the entry with no font, and the search passes over it.
//
// The font is read as a collection because fontconfig answers plenty of
// families with a .ttc, and opentype.Parse refuses one ("invalid single
// font"). ParseCollection reads a plain TTF or OTF as a collection of one, so
// it is the one path for both.
func (fs *faceSet) parse(e *faceEntry) error {
	if e.parsed {
		return nil
	}
	e.parsed = true
	if len(e.src.Data) == 0 {
		// An empty font is a face the caller does not have, such as the bold
		// cut of a font with none. sfnt.ParseCollection panics on it.
		return errors.New("parse font: no font data")
	}
	coll, err := sfnt.ParseCollection(e.src.Data)
	if err != nil {
		return fmt.Errorf("parse font: %w", err)
	}
	index := e.src.Index
	if index < 0 || index >= coll.NumFonts() {
		// An index the file does not have is a resolver and a file that
		// disagree. The first face is from the family that was asked for,
		// which beats no capture at all.
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
	return nil
}

// pick returns the face that draws r, and whether a bold cell drawn in it
// needs a double strike because the face is not a bold cut. A nil face means
// no face covers r.
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

// search returns the index of the first face that has r, or -1.
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
		if idx, err := e.font.GlyphIndex(&fs.buf, r); err == nil && idx != 0 {
			return i
		}
	}
	return -1
}

// cellSize derives the grid cell from the primary face: the advance of "M"
// wide, the face's own line box tall.
//
// Both dimensions are measured from the font. A hardcoded 1.25 em height
// against JetBrainsMono (advance 0.600 em, hhea line box 1.320 em) gives an
// aspect of 0.486 where the terminal draws 0.455, so every capture comes out
// about seven percent wider per cell than the screen it pictures.
// Metrics().Height is ascent - descent + lineGap, which is the same line box
// kitty and ghostty size their cells from, so taking it makes the picture's
// aspect the host's.
func (fs *faceSet) cellSize() (float64, float64) {
	face := fs.faces[1].face // the primary face, which loadFaces parsed
	adv, ok := face.GlyphAdvance('M')
	if !ok || adv == 0 {
		adv = fixed.I(int(fs.size * 0.6))
	}
	cw := fixedToFloat(adv)
	ch := fixedToFloat(face.Metrics().Height)
	if ch <= 0 {
		// A face with no usable metrics still has to draw somewhere. The old
		// hardcode is the fallback rather than the rule.
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
			// A cell draws the first rune of its cluster in a face that
			// covers that rune, so that rune is the one a face must have.
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

// appendMissing appends r to missing when no face has it and it is not there
// already.
func (fs *faceSet) appendMissing(missing []rune, r rune) []rune {
	if r == 0 || r == ' ' || isProcedural(r) {
		return missing
	}
	if _, seen := fs.picks[r*2]; seen {
		// The search ran for r already, and pick keeps every answer.
		return missing
	}
	if face, _ := fs.pick(r, false); face == nil {
		missing = append(missing, r)
	}
	return missing
}
