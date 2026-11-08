package app

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"

	"tuios.dev/vt"
)

// FuzzKittySymbols feeds the kitty glyph assembler any run of commands: any
// action, format, size, source rectangle, chunk flag and payload. A guest
// picks all of them. It must not panic, never hold more than its bounds, and
// every picture it hands back must fit the cells it names and the sixel size
// limits.
func FuzzKittySymbols(f *testing.F) {
	rgba := make([]byte, 4*4*4)
	for i := range rgba {
		rgba[i] = byte(i * 7)
	}
	var pngBuf bytes.Buffer
	pic := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	pic.Set(1, 1, color.NRGBA{200, 10, 10, 255})
	_ = png.Encode(&pngBuf, pic)
	seed := func(action byte, format byte, w, h uint16, more byte, data []byte) []byte {
		b := []byte{action, format, more}
		b = binary.BigEndian.AppendUint16(b, w)
		b = binary.BigEndian.AppendUint16(b, h)
		return append(b, data...)
	}
	f.Add(seed('T', 32, 4, 4, 0, rgba))
	f.Add(append(seed('T', 32, 4, 4, 1, rgba[:20]), seed('t', 0, 0, 0, 0, rgba[20:])...))
	f.Add(seed('T', 100, 0, 0, 0, pngBuf.Bytes()))
	f.Add(append(seed('t', 24, 2, 2, 0, rgba[:12]), seed('p', 0, 0, 0, 0, nil)...))
	f.Add(seed('d', 'A', 0, 0, 0, nil))
	f.Add(seed('T', 32, 0xffff, 0xffff, 0, rgba))
	f.Fuzz(func(t *testing.T, in []byte) {
		var ks kittySymbols
		cells := func(c *vt.KittyCommand) (int, int) {
			r, col := c.Rows, c.Columns
			if r <= 0 {
				r = (c.Height + 19) / 20
			}
			if col <= 0 {
				col = (c.Width + 9) / 10
			}
			return min(max(r, 0), 500), min(max(col, 0), 500)
		}
		for len(in) >= 7 {
			cmd := &vt.KittyCommand{
				Action: vt.KittyGraphicsAction(in[0]),
				Format: vt.KittyGraphicsFormat(in[1]),
				More:   in[2]&1 == 1,
				Width:  int(binary.BigEndian.Uint16(in[3:])),
				Height: int(binary.BigEndian.Uint16(in[5:])),
			}
			if cmd.Action == vt.KittyActionDelete {
				cmd.Delete = vt.KittyDeleteTarget(in[1])
			}
			cmd.ImageID = uint32(in[2] >> 1)
			cmd.SourceX, cmd.SourceY = int(in[3]), int(in[4])
			n := min(len(in)-7, int(in[6])+1)
			cmd.Data = in[7 : 7+n]
			in = in[7+n:]
			place, _ := ks.take("w", cmd, cells)
			if place == nil {
				continue
			}
			img := place.img
			if img.Width <= 0 || img.Height <= 0 || img.Width*img.Height != len(img.Pix) || img.Width*img.Height > vt.SixelMaxPixels {
				t.Fatalf("a picture of %dx%d with %d pixels", img.Width, img.Height, len(img.Pix))
			}
			for _, p := range img.Pix {
				if int(p) > len(img.Palette) {
					t.Fatalf("pixel index %d past the palette of %d", p, len(img.Palette))
				}
			}
			if place.rows > 0 && place.cols > 0 && place.rows*20*place.cols*10 <= vt.SixelMaxPixels {
				out := resampleKittySymbol(img, place.cols*10, place.rows*20)
				if len(out.Pix) != place.cols*10*place.rows*20 {
					t.Fatalf("resampled to %d pixels, want %d", len(out.Pix), place.cols*10*place.rows*20)
				}
			}
			ks.drew("w", place.imageID, 1)
		}
		w := ks.wins["w"]
		if w != nil && (len(w.stored) > kittySymbolsMaxStored || w.nDrawn > kittySymbolsMaxDrawn || w.bytes < 0 || len(w.stored) > 1 && w.bytes > kittySymbolsMaxStoredBytes) {
			t.Fatalf("holds %d stored and %d drawn, past the bounds", len(w.stored), w.nDrawn)
		}
	})
}
