package shot

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand/v2"
	"strings"
	"testing"
)

// TestOpaquePNGDecodesToTheSamePixels holds EncodePNG's output to the pixels
// it was given: every viewer, the preview panel and the kitty path decode it,
// so image/png must read back exactly the raster that went in, a transparent
// one with its alpha and a sub-image read through its own stride and origin.
func TestOpaquePNGDecodesToTheSamePixels(t *testing.T) {
	g := NewGrid(24, 4, RGB(0xe5, 0xe5, 0xe5), RGB(0x1e, 0x1e, 0x2e))
	for y, line := range []string{"$ go test ./shot/", "ok  tuios.dev/shot 0.4s", "PASS", "$ "} {
		put(g, 0, y, line, nil)
	}
	put(g, 0, 1, "ok", func(c *Cell) { c.FG, c.Bold = RGB(0xa6, 0xe3, 0xa1), true })
	put(g, 6, 2, "  span  ", func(c *Cell) { c.FG, c.BG, c.BGDefault = RGB(0x11, 0x11, 0x1b), RGB(0xf9, 0xe2, 0xaf), false })
	for x := range g.Cols {
		g.Cells[3][x].BG, g.Cells[3][x].BGDefault = RGB(0x31, 0x32, 0x44), false
	}

	raster := func(spec FrameSpec) *image.RGBA {
		img, err := Raster(g, BuildFrame(spec, FrameInputs{}))
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	framed := raster(FrameSpec{Frame: "window", Title: "build", Padding: 24, Radius: 10, Shadow: true, Scale: 2})
	bare := raster(FrameSpec{Frame: "none", Scale: 2})
	transparent := raster(FrameSpec{Frame: "window", Background: "none", Title: "build", Padding: 24, Radius: 10, Shadow: true, Scale: 2})
	// The wash strip above the card, clear of the shadow: 48 px of padding at
	// scale 2, and the shadow's blur starts below row 16.
	wash, ok := framed.SubImage(image.Rect(3, 2, framed.Rect.Dx()-7, 16)).(*image.RGBA)
	if !ok {
		t.Fatal("SubImage did not return an *image.RGBA")
	}
	noise := image.NewRGBA(image.Rect(0, 0, 37, 11))
	rng := rand.New(rand.NewPCG(1215, 11))
	for i := range noise.Pix {
		noise.Pix[i] = uint8(rng.UintN(256))
		if i%4 == 3 {
			noise.Pix[i] = 0xff
		}
	}
	one := image.NewRGBA(image.Rect(0, 0, 1, 1))
	one.SetRGBA(0, 0, RGB(0x12, 0x34, 0x56))

	for _, tc := range []struct {
		name   string
		img    *image.RGBA
		opaque bool
	}{
		{"framed", framed, true},
		{"bare", bare, true},
		{"random 37x11", noise, true},
		{"wash sub-image", wash, true},
		{"1x1", one, true},
		{"transparent", transparent, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.img.Opaque() != tc.opaque {
				t.Fatalf("Opaque() = %v, want %v", tc.img.Opaque(), tc.opaque)
			}
			out, err := EncodePNG(tc.img)
			if err != nil {
				t.Fatal(err)
			}
			got, err := png.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			src := tc.img.Rect
			gb := got.Bounds()
			if gb.Dx() != src.Dx() || gb.Dy() != src.Dy() {
				t.Fatalf("decoded %dx%d, want %dx%d", gb.Dx(), gb.Dy(), src.Dx(), src.Dy())
			}
			for y := range src.Dy() {
				for x := range src.Dx() {
					want := color.NRGBAModel.Convert(tc.img.At(src.Min.X+x, src.Min.Y+y)).(color.NRGBA)
					have := color.NRGBAModel.Convert(got.At(gb.Min.X+x, gb.Min.Y+y)).(color.NRGBA)
					if have != want {
						t.Fatalf("pixel (%d,%d): decoded %v, source %v", x, y, have, want)
					}
				}
			}
		})
	}

	t.Run("empty", func(t *testing.T) {
		empty, err := Raster(NewGrid(0, 3, g.FG, g.BG), nil)
		if err != nil {
			t.Fatal(err)
		}
		if !empty.Rect.Empty() {
			t.Fatalf("a 0-column grid rasterised to %v", empty.Rect)
		}
		if out, err := EncodePNG(empty); err == nil {
			t.Fatalf("EncodePNG of an empty raster returned %d bytes and no error", len(out))
		}
	})

	t.Run("route", func(t *testing.T) {
		for name, img := range map[string]*image.RGBA{"framed": framed, "wash sub-image": wash} {
			out, err := EncodePNG(img)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(out, []byte("\x89PNG\r\n\x1a\n")) {
				t.Fatalf("%s: no PNG signature", name)
			}
			var idat []byte
			chunks := 0
			for rest := out[8:]; len(rest) >= 12; {
				n := int(binary.BigEndian.Uint32(rest))
				if len(rest) < 12+n {
					t.Fatalf("%s: chunk %q runs past the end", name, rest[4:8])
				}
				if string(rest[4:8]) == "IDAT" {
					chunks++
					idat = append(idat, rest[8:8+n]...)
				}
				rest = rest[12+n:]
			}
			if chunks != 1 {
				t.Errorf("%s: %d IDAT chunks, want 1", name, chunks)
			}
			zr, err := zlib.NewReader(bytes.NewReader(idat))
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			raw, err := io.ReadAll(zr)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			w, h := img.Rect.Dx(), img.Rect.Dy()
			row := 1 + 3*w
			if len(raw) != h*row {
				t.Fatalf("%s: inflated %d bytes, want %d rows of %d", name, len(raw), h, row)
			}
			var filtered []string
			for y := range h {
				if f := raw[y*row]; f != 0 {
					filtered = append(filtered, fmt.Sprintf("row %d: %d", y, f))
				}
			}
			if len(filtered) > 0 {
				t.Errorf("%s: %d of %d rows filtered, want filter 0 on every row; first: %s",
					name, len(filtered), h, strings.Join(filtered[:min(5, len(filtered))], ", "))
			}
		}
	})
}

// TestOpaquePNGEncodeBudget holds the opaque route to its trade: a screenshot
// encodes in well under the time image/png takes at BestSpeed, for a file at
// most a tenth larger.
func TestOpaquePNGEncodeBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("timed")
	}
	bg, odd := RGB(0x1e, 0x1e, 0x2e), RGB(0x31, 0x32, 0x44)
	g := NewGrid(120, 40, RGB(0xcd, 0xd6, 0xf4), bg)
	for r := range g.Rows {
		text := fmt.Sprintf("%3d  func render(x int) string { return x.Format(%q) }", r, "abc")
		text += strings.Repeat(" ", g.Cols-len(text))
		put(g, 0, r, text, nil)
		if r%2 == 1 {
			for c := range g.Cols {
				g.Cells[r][c].BG, g.Cells[r][c].BGDefault = odd, false
			}
		}
		for c := 0; c < g.Cols; c += 17 {
			fg, sbg := XTerm256(9+c/17), XTerm256(52+c/17*23)
			for x := c; x < min(c+6, g.Cols); x++ {
				g.Cells[r][x].FG, g.Cells[r][x].BG, g.Cells[r][x].BGDefault = fg, sbg, false
			}
		}
	}
	raster := func(spec FrameSpec) *image.RGBA {
		img, err := Raster(g, BuildFrame(spec, FrameInputs{}))
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	bare := raster(FrameSpec{Frame: "none", Scale: 2})
	framed := raster(FrameSpec{Frame: "window", Title: "build", Padding: 24, Radius: 10, Shadow: true, Scale: 2})

	stdlib := func(img *image.RGBA) []byte {
		var buf bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	encode := func(img *image.RGBA) []byte {
		out, err := EncodePNG(img)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	bench := func(f func(*image.RGBA) []byte) func(*testing.B) {
		return func(b *testing.B) {
			for b.Loop() {
				f(bare)
			}
		}
	}
	var stdNs, encNs int64
	for range 2 {
		if ns := testing.Benchmark(bench(stdlib)).NsPerOp(); stdNs == 0 || ns < stdNs {
			stdNs = ns
		}
		if ns := testing.Benchmark(bench(encode)).NsPerOp(); encNs == 0 || ns < encNs {
			encNs = ns
		}
	}
	t.Logf("bare %dx%d: EncodePNG %d µs, image/png BestSpeed %d µs, ratio %.2f",
		bare.Rect.Dx(), bare.Rect.Dy(), encNs/1000, stdNs/1000, float64(encNs)/float64(stdNs))
	if float64(encNs) > 0.7*float64(stdNs) {
		t.Errorf("EncodePNG took %d µs, the budget is 0.7x image/png's %d µs", encNs/1000, stdNs/1000)
	}

	for _, tc := range []struct {
		name string
		img  *image.RGBA
	}{{"bare", bare}, {"framed", framed}} {
		ours, theirs := len(encode(tc.img)), len(stdlib(tc.img))
		t.Logf("%s: EncodePNG %d B, image/png BestSpeed %d B, ratio %.3f", tc.name, ours, theirs, float64(ours)/float64(theirs))
		if float64(ours) > 1.10*float64(theirs) {
			t.Errorf("%s: EncodePNG wrote %d B, more than 1.10x image/png's %d B", tc.name, ours, theirs)
		}
	}
}
