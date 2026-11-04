package preview

import (
	"errors"
	"image"
	"image/color"
	"os"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// A font file shows as a specimen drawn with its own outlines, in Go: the
// window's text engine draws only the fonts it has installed.

// fontFileMax bounds a font file read whole.
const fontFileMax = 64 << 20

// FontInfo is what a font's name table says.
type FontInfo struct {
	Family, Style, Full, Version, Designer, License string
	Glyphs                                          int
	// Faces is the count of fonts in a collection, 1 for a single font.
	Faces int
}

// FontFile is a font read for its specimen.
type FontFile struct {
	Info FontInfo
	f    *sfnt.Font
}

// OpenFont reads a TrueType or OpenType font, or the first of a collection.
func OpenFont(path string) (*FontFile, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Size() > fontFileMax {
		return nil, errors.New("The font file is too large to show.")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	faces := 1
	f, err := sfnt.Parse(data)
	if err != nil {
		c, cerr := sfnt.ParseCollection(data)
		if cerr != nil {
			return nil, errors.New("The font could not be read.")
		}
		faces = c.NumFonts()
		if f, err = c.Font(0); err != nil {
			return nil, errors.New("The font could not be read.")
		}
	}
	var buf sfnt.Buffer
	name := func(id sfnt.NameID) string {
		s, err := f.Name(&buf, id)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(s)
	}
	info := FontInfo{
		Family:   name(sfnt.NameIDTypographicFamily),
		Style:    name(sfnt.NameIDTypographicSubfamily),
		Full:     name(sfnt.NameIDFull),
		Version:  strings.TrimPrefix(name(sfnt.NameIDVersion), "Version "),
		Designer: name(sfnt.NameIDDesigner),
		License:  name(sfnt.NameIDLicense),
		Glyphs:   f.NumGlyphs(),
		Faces:    faces,
	}
	if info.Family == "" {
		info.Family = name(sfnt.NameIDFamily)
	}
	if info.Style == "" {
		info.Style = name(sfnt.NameIDSubfamily)
	}
	if len(info.License) > 300 {
		info.License = info.License[:300] + "…"
	}
	return &FontFile{Info: info, f: f}, nil
}

// specimenLines are the specimen's lines: a size in points and the text.
var specimenLines = []struct {
	size float64
	text string
}{
	{64, "Aa Gg Rr"},
	{30, "ABCDEFGHIJKLMNOPQRSTUVWXYZ"},
	{30, "abcdefghijklmnopqrstuvwxyz"},
	{30, "0123456789 !?&@#$%*(){}[]"},
	{12, "The quick brown fox jumps over the lazy dog. 0O 1lI"},
	{16, "The quick brown fox jumps over the lazy dog. 0O 1lI"},
	{22, "The quick brown fox jumps over the lazy dog."},
	{32, "The quick brown fox jumps over"},
	{48, "Sphinx of black quartz"},
}

// Specimen draws the font's specimen widthPx pixels wide, at scale pixels a
// point, in fg on a clear ground. The lines past the width are cut.
func (ff *FontFile) Specimen(widthPx int, scale float64, fg color.Color) (*image.RGBA, error) {
	if widthPx <= 0 {
		return nil, errors.New("no width")
	}
	type line struct {
		face   font.Face
		text   string
		ascent int
		height int
	}
	var lines []line
	total := 0
	for _, l := range specimenLines {
		face, err := opentype.NewFace(ff.f, &opentype.FaceOptions{Size: l.size * scale, DPI: 72, Hinting: font.HintingNone})
		if err != nil {
			return nil, err
		}
		m := face.Metrics()
		a, h := m.Ascent.Ceil(), (m.Ascent + m.Descent).Ceil()
		gap := int(l.size * scale * 0.55)
		lines = append(lines, line{face, l.text, a, h + gap})
		total += h + gap
	}
	img := image.NewRGBA(image.Rect(0, 0, widthPx, total))
	src := image.NewUniform(fg)
	y := 0
	for _, l := range lines {
		d := font.Drawer{Dst: img, Src: src, Face: l.face, Dot: fixed.P(0, y+l.ascent)}
		d.DrawString(l.text)
		_ = l.face.Close()
		y += l.height
	}
	return img, nil
}
