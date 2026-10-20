package app

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
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

// TestUnnamedTransmitIsNotKept is a security boundary: the pane decides how
// many images it sends, and what tuios keeps for a screenshot must stay
// bounded however many that is. The ways it could go wrong, written down
// before the fix:
//
//  1. An a=t under i=0 keeps an entry. An image sent with no id cannot be
//     named by a later a=p, and an a=t places nothing, so the entry is never
//     shown. A t=f entry holds only a path and counts no bytes, so the bound
//     on kept bytes never evicts it, and a guest that repeats the command
//     grows the store without end.
//  2. A fix that keeps nothing for any a=t drops an image a later a=p shows:
//     an a=t under an id the guest chose must still be kept.
//  3. A fix that keeps nothing under i=0 drops one on screen: an a=T under
//     i=0 must still be kept.
func TestUnnamedTransmitIsNotKept(t *testing.T) {
	withClientCaps(t, &HostCapabilities{KittyGraphics: true, KittyFileTransfer: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20})
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: &hostRecorder{}})
	if !kp.hostReadsFiles() {
		t.Fatal("the host should read files in this test")
	}
	path := filepath.Join(kittyTestTempDir(t), "frame.rgba")
	if err := os.WriteFile(path, make([]byte, 2*2*4), 0o600); err != nil {
		t.Fatal(err)
	}
	send := func(action vt.KittyGraphicsAction, id uint32) {
		cmd := kittyFileCmd(vt.KittyMediumFile, path)
		cmd.Action, cmd.ImageID = action, id
		sendKitty(kp, cmd)
	}

	for range 100 {
		send(vt.KittyActionTransmit, 0)
	}
	if n := len(kp.kept[kittyMediumWin]); n != 0 {
		t.Fatalf("100 a=t under i=0 left %d kept entries, want none: nothing can show them", n)
	}

	send(vt.KittyActionTransmit, 7)
	if n := len(kp.kept[kittyMediumWin]); n != 1 {
		t.Fatalf("an a=t under i=7 left %d kept entries, want 1: a later a=p can show it", n)
	}

	send(vt.KittyActionTransmitPlace, 0)
	if n := len(kp.kept[kittyMediumWin]); n != 2 {
		t.Fatalf("an a=T under i=0 left %d kept entries, want 2: it is on screen", n)
	}
}
