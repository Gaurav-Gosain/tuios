package vt_test

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A 16x12 sixel: red on the top band, blue on the bottom one, with raster
// attributes stating the size, the way img2sixel and chafa write it.
const testSixel = "\x1bPq\"1;1;16;12#1;2;100;0;0#2;2;0;0;100#1!16~-#2!16~\x1b\\"

// TestSixelMarksCells checks both backends write an image's cells as markers
// naming the image, at the cursor, and leave the cursor under it. That is what
// lets the compositor find the image on the frame and what makes text written
// over it, an erase, or a scroll act on the picture the way a sixel terminal
// does.
func TestSixelMarksCells(t *testing.T) {
	term := vt.New(20, 6)
	t.Cleanup(func() { _ = term.Close() })
	term.SetCellSize(8, 6)
	var gotX, gotY int
	term.SetSixelPassthroughFunc(func(cmd *vt.SixelCommand, x, y int) uint32 {
		gotX, gotY = x, y
		return 77
	})
	if _, err := term.Write([]byte("ab\x1b[2;3H" + testSixel)); err != nil {
		t.Fatal(err)
	}
	if gotX != 2 || gotY != 1 {
		t.Fatalf("passthrough saw cursor %d,%d, want 2,1", gotX, gotY)
	}
	// 16x12 pixels at 8x6 cells is 2 columns by 2 rows, from (2,1).
	for y := range 6 {
		for x := range 20 {
			c := term.CellAt(x, y)
			content := ""
			if c != nil {
				content = c.Content
			}
			inImage := x >= 2 && x < 4 && y >= 1 && y < 3
			id, row, col, ok := vt.ParseSixelMarker(content)
			if ok != inImage {
				t.Fatalf("cell %d,%d marker=%v, want %v (content %q)", x, y, ok, inImage, content)
			}
			if ok && (id != 77 || row != y-1 || col != x-2) {
				t.Fatalf("cell %d,%d names image %d row %d col %d", x, y, id, row, col)
			}
			if ok && c.Width != 1 {
				t.Fatalf("marker cell %d,%d has width %d", x, y, c.Width)
			}
		}
	}
	if pos := term.CursorPosition(); pos.X != 0 || pos.Y != 3 {
		t.Fatalf("cursor at %v after the image, want 0,3", pos)
	}

	// Text over one cell takes that cell out of the image; an erase takes
	// the rest.
	if _, err := term.Write([]byte("\x1b[2;3HX")); err != nil {
		t.Fatal(err)
	}
	if c := term.CellAt(2, 1); c == nil || c.Content != "X" {
		t.Fatalf("text over the image left %#v", c)
	}
	if _, err := term.Write([]byte("\x1b[2J")); err != nil {
		t.Fatal(err)
	}
	for y := range 6 {
		for x := range 20 {
			if c := term.CellAt(x, y); c != nil && vt.IsSixelMarker(c.Content) {
				t.Fatalf("erase left a marker at %d,%d", x, y)
			}
		}
	}
}

// TestSixelScrollsIntoScrollback checks an image drawn at the bottom scrolls
// the screen, and that the rows which leave the screen keep their markers in
// the scrollback, so the picture is there when the pane is scrolled back.
func TestSixelScrollsIntoScrollback(t *testing.T) {
	term := vt.New(10, 3)
	t.Cleanup(func() { _ = term.Close() })
	term.SetCellSize(8, 3)
	term.SetSixelPassthroughFunc(func(*vt.SixelCommand, int, int) uint32 { return 5 })
	// 12 pixels at 3 per row is 4 rows on a 3-row screen, from the last row.
	if _, err := term.Write([]byte("\x1b[3;1H" + testSixel)); err != nil {
		t.Fatal(err)
	}
	found := 0
	for i := range term.ScrollbackLen() {
		line := term.ScrollbackLine(i)
		if len(line) > 0 && vt.IsSixelMarker(line[0].Content) {
			found++
		}
	}
	for y := range 3 {
		if c := term.CellAt(0, y); c != nil && vt.IsSixelMarker(c.Content) {
			found++
		}
	}
	if found < 3 {
		t.Fatalf("found %d image rows on screen and in the scrollback, want at least 3", found)
	}
}

// TestSixelDecodeEncodeRoundTrip checks a crop re-encoded from the decoded
// image decodes to the same pixels, which is the promise the compositor's
// clipping rests on.
func TestSixelDecodeEncodeRoundTrip(t *testing.T) {
	cmd := vt.ParseSixelCommand([]byte(testSixel[2 : len(testSixel)-2]))
	img := vt.DecodeSixel(cmd)
	if img == nil || img.Width != 16 || img.Height != 12 {
		t.Fatalf("decoded %+v", img)
	}
	if c, ok := img.At(3, 2); !ok || c.R != 255 || c.B != 0 {
		t.Fatalf("top band pixel = %v %v, want red", c, ok)
	}
	if c, ok := img.At(3, 8); !ok || c.B != 255 || c.R != 0 {
		t.Fatalf("bottom band pixel = %v %v, want blue", c, ok)
	}
	// Crop the right half of rows 4..10, which straddles the band edge.
	crop := image.Rect(8, 4, 16, 10)
	seq := vt.EncodeSixel(img, crop, crop.Dx(), crop.Dy())
	if !strings.HasPrefix(string(seq), "\x1bP") || !strings.HasSuffix(string(seq), "\x1b\\") {
		t.Fatalf("not a DCS: %q", seq)
	}
	back := vt.DecodeSixel(vt.ParseSixelCommand(seq[2 : len(seq)-2]))
	if back == nil || back.Width != 8 || back.Height != 6 {
		t.Fatalf("re-decoded %+v", back)
	}
	for y := range 6 {
		for x := range 8 {
			want, wok := img.At(crop.Min.X+x, crop.Min.Y+y)
			got, gok := back.At(x, y)
			if want != got || wok != gok {
				t.Fatalf("pixel %d,%d = %v %v, want %v %v", x, y, got, gok, want, wok)
			}
		}
	}
	// Scaled to double size, each source pixel becomes a 2x2 block.
	big := vt.DecodeSixel(vt.ParseSixelCommand(func() []byte {
		s := vt.EncodeSixel(img, crop, 16, 12)
		return s[2 : len(s)-2]
	}()))
	if big == nil || big.Width != 16 || big.Height != 12 {
		t.Fatalf("scaled %+v", big)
	}
	w, _ := img.At(8, 5)
	g, _ := big.At(0, 3)
	if w != g {
		t.Fatalf("scaled pixel = %v, want %v", g, w)
	}
}

// TestSixelMarkerRoundTrip checks every field survives the marker encoding,
// at its limits, and that markers leave text as blanks.
func TestSixelMarkerRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		id       uint32
		row, col int
	}{{1, 0, 0}, {vt.SixelMaxID, vt.SixelMaxCells, vt.SixelMaxCells}, {300, 7, 296}} {
		m := vt.SixelMarker(tc.id, tc.row, tc.col)
		id, row, col, ok := vt.ParseSixelMarker(m)
		if !ok || id != tc.id || row != tc.row || col != tc.col {
			t.Fatalf("marker %+v read back as %d %d %d %v", tc, id, row, col, ok)
		}
	}
	s := "a" + vt.SixelMarker(3, 1, 2) + vt.SixelMarker(3, 1, 3) + "b"
	if got := vt.StripSixelMarkers(s); got != "a  b" {
		t.Fatalf("StripSixelMarkers = %q", got)
	}
}

// TestSixelDecodeLimits checks an image bigger than the budget is refused
// rather than allocated.
func TestSixelDecodeLimits(t *testing.T) {
	huge := vt.ParseSixelCommand([]byte("q\"1;1;20000;20000#0~"))
	if img := vt.DecodeSixel(huge); img != nil {
		t.Fatalf("a 20000x20000 image was decoded (%d bytes)", img.Bytes())
	}
}

// benchImage is a 1000x600 picture using all 256 registers in bands, about
// what a photo-sized sixel from chafa or img2sixel holds.
func benchImage() *vt.SixelImage {
	img := &vt.SixelImage{Width: 1000, Height: 600, Palette: make([]color.RGBA, vt.SixelMaxRegisters)}
	img.Pix = make([]uint16, img.Width*img.Height)
	for i := range img.Palette {
		img.Palette[i] = color.RGBA{uint8(i), uint8(255 - i), uint8(i * 7), 255}
	}
	for y := range img.Height {
		for x := range img.Width {
			img.Pix[y*img.Width+x] = uint16((x/4+y/3)%256 + 1)
		}
	}
	return img
}

// BenchmarkEncodeSixelCrop is the cost of one cropped resend, which runs on
// the renderer's goroutine.
func BenchmarkEncodeSixelCrop(b *testing.B) {
	img := benchImage()
	crop := image.Rect(0, 100, 800, 600)
	b.ReportAllocs()
	for b.Loop() {
		_ = vt.EncodeSixel(img, crop, crop.Dx(), crop.Dy())
	}
}

// BenchmarkDecodeSixel is the cost of taking an image in, which runs on the
// pane's PTY reader.
func BenchmarkDecodeSixel(b *testing.B) {
	img := benchImage()
	seq := vt.EncodeSixel(img, image.Rect(0, 0, img.Width, img.Height), img.Width, img.Height)
	cmd := vt.ParseSixelCommand(seq[2 : len(seq)-2])
	b.SetBytes(int64(len(seq)))
	b.ReportAllocs()
	for b.Loop() {
		_ = vt.DecodeSixel(cmd)
	}
}

// BenchmarkEncodeSixelNoise is the worst case: every colour in every band
// spread across the whole width, as dithered noise is.
func BenchmarkEncodeSixelNoise(b *testing.B) {
	img := benchImage()
	seed := uint32(1)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[i] = uint16(seed>>24) + 1
	}
	crop := image.Rect(0, 100, 800, 600)
	b.ReportAllocs()
	for b.Loop() {
		_ = vt.EncodeSixel(img, crop, crop.Dx(), crop.Dy())
	}
}

// TestSixelCursorBelowImage: after an image at the bottom of the screen the
// cursor is on the row under it, as xterm leaves it, so text printed next
// does not land on the image's last row. Both backends.
func TestSixelCursorBelowImage(t *testing.T) {
	for _, startRow := range []int{1, 4, 5} {
		term := vt.New(10, 6)
		term.SetCellSize(8, 6)
		term.SetSixelPassthroughFunc(func(*vt.SixelCommand, int, int) uint32 { return 9 })
		// Two image rows (12 pixels at 6 per row).
		if _, err := fmt.Fprintf(term, "\x1b[%d;1H%sAFTER", startRow+1, testSixel); err != nil {
			t.Fatal(err)
		}
		markers := 0
		for y := range 6 {
			for x := range 10 {
				if c := term.CellAt(x, y); c != nil && vt.IsSixelMarker(c.Content) {
					markers++
				}
			}
		}
		if markers != 4 {
			t.Errorf("image from row %d: %d image cells left after the next text, want 4", startRow, markers)
		}
		_ = term.Close()
	}
}

// TestSixelUnderScrollRegionAndOriginMode: an image drawn with a scroll
// region and origin mode set lands at the cursor and leaves the cursor under
// it on both backends, and each image cell keeps the background of the cell
// it replaced.
func TestSixelUnderScrollRegionAndOriginMode(t *testing.T) {
	term := vt.New(10, 8)
	t.Cleanup(func() { _ = term.Close() })
	term.SetCellSize(8, 6)
	term.SetSixelPassthroughFunc(func(*vt.SixelCommand, int, int) uint32 { return 4 })
	// Region rows 2..7, origin mode on, cursor at region row 3 (screen row
	// 4, index 3), which is painted blue first.
	in := "\x1b[2;7r\x1b[?6h\x1b[3;1H\x1b[44mABCD\x1b[0m\x1b[3;1H" + testSixel + "Z"
	if _, err := term.Write([]byte(in)); err != nil {
		t.Fatal(err)
	}
	for _, y := range []int{3, 4} {
		for x := range 2 {
			c := term.CellAt(x, y)
			if c == nil || !vt.IsSixelMarker(c.Content) {
				t.Fatalf("no image cell at %d,%d", x, y)
			}
			if y == 3 && c.Style.Bg == nil {
				t.Errorf("image cell %d,%d lost the blue background under it", x, y)
			}
		}
	}
	if c := term.CellAt(0, 5); c == nil || c.Content != "Z" {
		t.Errorf("text after the image is not on the row under it: %#v", c)
	}
}

// TestSixelExact: the guest's bytes may stand in for the decoded image only
// when they draw the same pixels. Painting past the declared size, using a
// register past 255, or leaving pixels unpainted under an opaque background
// each rule that out.
func TestSixelExact(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		exact      bool
	}{
		{"fits", "0;1;0q\"1;1;10;12#1;2;100;0;0!10~-!10~", true},
		{"paints past the width", "0;1;0q\"1;1;10;20#1;2;100;0;0!400~-!400~-!400~-!400~-!400~", false},
		{"paints past the height", "0;1;0q\"1;1;10;6#1;2;100;0;0!10~-!10~", false},
		{"register 300", "0;1;0q\"1;1;10;6#300;2;100;0;0!10~", false},
		{"opaque with a gap", "0;0;0q\"1;1;10;6#1;2;100;0;0!5~", false},
		{"opaque and full", "0;0;0q\"1;1;10;6#1;2;100;0;0!10~", true},
	} {
		img := vt.DecodeSixel(vt.ParseSixelCommand([]byte(tc.body)))
		if img == nil || img.Exact != tc.exact {
			t.Errorf("%s: exact = %v, want %v", tc.name, img != nil && img.Exact, tc.exact)
		}
	}
	// An opaque gap is filled with register 0, so every crop agrees.
	img := vt.DecodeSixel(vt.ParseSixelCommand([]byte("0;0;0q\"1;1;10;6#0;2;0;0;100#1;2;100;0;0!5~")))
	if c, ok := img.At(8, 2); !ok || c.B != 255 {
		t.Errorf("unpainted pixel under an opaque background = %v %v, want register 0 (blue)", c, ok)
	}
	// Colours past the 256 a pane is told fold into the first 256.
	var b strings.Builder
	b.WriteString("0;1;0q\"1;1;300;6")
	for i := range 300 {
		fmt.Fprintf(&b, "#%d;2;%d;%d;50$%s~", i, i%101, (i/3)%101, strings.Repeat("?", i))
	}
	big := vt.DecodeSixel(vt.ParseSixelCommand([]byte(b.String())))
	seq := vt.EncodeSixel(big, image.Rect(0, 0, 300, 6), 300, 6)
	if strings.Contains(string(seq), "#256;") {
		t.Errorf("a re-encoded image defines more than 256 registers")
	}
}
