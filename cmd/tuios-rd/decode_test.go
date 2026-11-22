//go:build h264 && (linux || darwin)

package main

import (
	"encoding/binary"
	"os"
	"testing"
)

// TestDecodeRecording decodes a recorded screen-relay stream: 4-byte length,
// then one /stream message, repeated. SRTERM_REC names the file.
func TestDecodeRecording(t *testing.T) {
	path := os.Getenv("SRTERM_REC")
	if path == "" {
		t.Skip("set SRTERM_REC to a recorded stream")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := loadOpenH264()
	if err != nil {
		t.Fatal(err)
	}
	d, err := lib.newDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	var b yuv
	pics := 0
	for len(data) >= 4 && pics < 60 {
		l := int(binary.BigEndian.Uint32(data))
		m := data[4 : 4+l]
		data = data[4+l:]
		w, h := int(binary.BigEndian.Uint16(m[20:])), int(binary.BigEndian.Uint16(m[22:]))
		p, got, err := d.decode(m[32:])
		if err != nil {
			t.Fatalf("frame %d: %v", pics, err)
		}
		if !got {
			continue
		}
		if p.w != w || p.h != h {
			t.Fatalf("picture %dx%d, header says %dx%d", p.w, p.h, w, h)
		}
		b.from(p)
		pics++
	}
	if pics == 0 {
		t.Fatal("no picture decoded")
	}
	// Full size and a downscale both give a picture that is not flat.
	for _, sz := range [][2]int{{b.w, b.h}, {b.w / 3, b.h / 3}, {b.w * 2, b.h * 2}} {
		rgb := make([]byte, sz[0]*sz[1]*3)
		b.rgb(rgb, sz[0], sz[1], 4)
		var lo, hi byte = 255, 0
		for _, v := range rgb {
			lo, hi = min(lo, v), max(hi, v)
		}
		if hi-lo < 100 {
			t.Fatalf("%dx%d: flat picture (%d..%d)", sz[0], sz[1], lo, hi)
		}
	}
	t.Logf("decoded %d pictures of %dx%d", pics, b.w, b.h)
}

func TestParser(t *testing.T) {
	var ps parser
	var got []termEv
	ps.feed([]byte("a\x1b[A\x1b[<0;10;20M\x1b[<0;10;20m\x1b_Gi=5;OK\x1b\\\x1d\x1b[93;5u\x1b[200~hi\x1b[201~\x1b[1;3C\x1b[99;9u"), true, func(e termEv) { got = append(got, e) })
	if len(got) != 10 {
		t.Fatalf("got %d events: %+v", len(got), got)
	}
	if got[0].key.text != 'a' || got[1].key.name != "up" || got[2].mouse.x != 10 || !got[3].mouse.release {
		t.Fatalf("bad: %+v", got[:4])
	}
	if got[4].reply.id != 5 || got[4].reply.msg != "OK" {
		t.Fatalf("reply: %+v", got[4].reply)
	}
	k, _ := parseKeySpec("ctrl+]")
	if !k.matches(got[5].key) || !k.matches(got[6].key) {
		t.Fatalf("ctrl+]: %+v %+v", got[5].key, got[6].key)
	}
	if got[7].paste != "hi" {
		t.Fatalf("paste: %+v", got[7])
	}
	if got[8].key.name != "right" || got[8].key.mods != modAlt {
		t.Fatalf("alt+right: %+v", got[8].key)
	}
	if got[9].key.name != "c" || got[9].key.mods != modSuper || got[9].key.text != 0 {
		t.Fatalf("super+c: %+v", got[9].key)
	}
	// ESC split across reads, then a lone ESC.
	got = nil
	ps.feed([]byte("\x1b["), false, func(e termEv) { got = append(got, e) })
	ps.feed([]byte("B\x1b"), false, func(e termEv) { got = append(got, e) })
	ps.feed(nil, true, func(e termEv) { got = append(got, e) })
	if len(got) != 2 || got[0].key.name != "down" || got[1].key.name != "escape" {
		t.Fatalf("split: %+v", got)
	}
}

func TestIsBlack(t *testing.T) {
	b := &yuv{w: 320, h: 200, y: make([]byte, 320*200)}
	for i := range b.y {
		b.y[i] = 16
	}
	if !isBlack(b) {
		t.Fatal("a black picture is not black")
	}
	b.y[100*320+160] = 200 // one bright pixel on the sample grid? not always
	for x := 0; x < 320; x++ {
		b.y[100*320+x] = 200 // a bright line across the middle
	}
	if isBlack(b) {
		t.Fatal("a picture with a bright line is black")
	}
}

func TestKeyRecords(t *testing.T) {
	r := keyRecords(&keyEv{name: "c", mods: modCtrl})
	if len(r) != 4 || r[0][0] != recKeyDown || binary.BigEndian.Uint16(r[0][2:]) != hidLCtrl || binary.BigEndian.Uint16(r[1][2:]) != 0x06 {
		t.Fatalf("ctrl+c: %v", r)
	}
	r = keyRecords(&keyEv{name: "A", text: 'A', mods: modShift})
	if len(r) != 1 || r[0][0] != recText {
		t.Fatalf("A: %v", r)
	}
	r = keyRecords(&keyEv{name: "space", mods: modSuper})
	if len(r) != 4 || binary.BigEndian.Uint16(r[0][2:]) != hidLGUI {
		t.Fatalf("cmd+space: %v", r)
	}
}
