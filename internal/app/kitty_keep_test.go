package app

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"math"
	"testing"

	"tuios.dev/vt"
)

// TestKeptImageDecodeBounds is a security boundary: every size in a kitty
// payload is the guest's to choose, and a screenshot decodes it. The ways a
// decode could go wrong, written down before the decoder:
//
//  1. A PNG whose header declares a huge size makes png.Decode allocate it.
//  2. A zlib payload inflates far past the bytes sent (a bomb).
//  3. A raw image declares s and v whose product overflows or is huge.
//  4. A raw image has fewer bytes than its s and v need.
//  5. A file medium entry names a file larger than the cap.
//
// Each must be refused without the allocation it asks for.
func TestKeptImageDecodeBounds(t *testing.T) {
	const budget = shotImageMaxPixels

	small := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	var good bytes.Buffer
	if err := png.Encode(&good, small); err != nil {
		t.Fatal(err)
	}
	huge := append([]byte(nil), good.Bytes()...)
	// IHDR starts at byte 8: length, "IHDR", width, height, ..., CRC.
	binary.BigEndian.PutUint32(huge[16:], 100000)
	binary.BigEndian.PutUint32(huge[20:], 100000)
	binary.BigEndian.PutUint32(huge[29:], crc32.ChecksumIEEE(huge[12:29]))

	var bomb bytes.Buffer
	zw := zlib.NewWriter(&bomb)
	_, _ = zw.Write(make([]byte, 64<<20))
	_ = zw.Close()

	cases := []struct {
		name string
		k    keptImage
		ok   bool
	}{
		{"a small png decodes", keptImage{format: vt.KittyFormatPNG, data: good.Bytes()}, true},
		{"a png declaring 100000x100000", keptImage{format: vt.KittyFormatPNG, data: huge}, false},
		{"a zlib bomb as raw rgba", keptImage{format: vt.KittyFormatRGBA, compression: vt.KittyCompressionZlib,
			width: 64, height: 64, data: bomb.Bytes()}, false},
		{"a zlib bomb as png", keptImage{format: vt.KittyFormatPNG, compression: vt.KittyCompressionZlib,
			data: bomb.Bytes()}, false},
		{"raw rgba past the budget", keptImage{format: vt.KittyFormatRGBA, width: 1 << 16, height: 1 << 16,
			data: make([]byte, 16)}, false},
		{"raw rgb with an overflowing size", keptImage{format: vt.KittyFormatRGB, width: math.MaxInt, height: math.MaxInt,
			data: make([]byte, 16)}, false},
		{"raw rgb short of its size", keptImage{format: vt.KittyFormatRGB, width: 8, height: 8,
			data: make([]byte, 10)}, false},
		{"a file past the cap", keptImage{format: vt.KittyFormatPNG, path: "/dev/zero"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			img, err := c.k.decode(budget)
			if c.ok {
				if err != nil || img == nil {
					t.Fatalf("decode failed: %v", err)
				}
				if b := img.Bounds(); b.Dx()*b.Dy() > budget {
					t.Fatalf("decoded %v, past the budget", b)
				}
				return
			}
			if err == nil {
				t.Fatalf("decode gave %v, want a refusal", img.Bounds())
			}
		})
	}
}
