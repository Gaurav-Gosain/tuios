package app

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// withAnimatingHost installs a host that advertises frame edits, which is what
// the damage path needs before it will emit a patch at all.
func withAnimatingHost(t *testing.T) {
	t.Helper()
	withClientCaps(t, &HostCapabilities{
		TerminalName:   "kitty",
		KittyGraphics:  true,
		KittyAnimation: true,
		TrueColor:      true,
		CellWidth:      10,
		CellHeight:     20,
	})
}

func newDamagePassthrough(t *testing.T) *KittyPassthrough {
	t.Helper()
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: io.Discard})
	if !kp.IsEnabled() {
		t.Fatal("passthrough not enabled for a kitty host")
	}
	return kp
}

// emitAndCapture runs one frame through the damage path and returns both the
// decision and exactly the bytes that frame appended to the host stream.
func emitAndCapture(
	kp *KittyPassthrough,
	windowID string,
	hostID uint32,
	format vt.KittyGraphicsFormat,
	compression vt.KittyGraphicsCompression,
	width, height int,
	raw []byte,
) (bitmapUpdate, []byte) {
	before := len(kp.pendingOutput)
	update := kp.emitBitmap(windowID, hostID, format, compression, width, height, raw)
	return update, append([]byte(nil), kp.pendingOutput[before:]...)
}

// makeBitmap builds a deterministic bitmap that is not uniform, so a copied
// rectangle landing at the wrong offset shows up as a mismatch.
func makeBitmap(width, height, bpp int, seed int) []byte {
	b := make([]byte, width*height*bpp)
	for i := range b {
		b[i] = byte(i*31 + seed*97 + i/7)
	}
	return b
}

// paintRect overwrites a rectangle with a recognisable ramp.
func paintRect(b []byte, width, bpp, x, y, w, h, seed int) {
	stride := width * bpp
	for row := y; row < y+h; row++ {
		for col := x; col < x+w; col++ {
			off := row*stride + col*bpp
			for k := range bpp {
				b[off+k] = byte(seed*13 + row*7 + col*3 + k)
			}
		}
	}
}

// splitAPC pulls the individual APC sequences out of a host byte stream.
func splitAPC(t *testing.T, out []byte) [][]byte {
	t.Helper()
	var seqs [][]byte
	rest := out
	for {
		start := bytes.Index(rest, []byte("\x1b_G"))
		if start < 0 {
			break
		}
		rest = rest[start+3:]
		end := bytes.Index(rest, []byte("\x1b\\"))
		if end < 0 {
			t.Fatalf("unterminated APC sequence in host output: %q", rest)
		}
		seqs = append(seqs, rest[:end])
		rest = rest[end+2:]
	}
	return seqs
}

// replayHost is the real host-side kitty implementation holding one image, so
// patches can be applied to it exactly as a kitty terminal would apply them.
type replayHost struct {
	handler *vt.KittyGraphicsHandler
	state   *vt.KittyState
	id      uint32
}

func newReplayHost(id uint32, format vt.KittyGraphicsFormat, width, height int, data []byte) *replayHost {
	state := vt.NewKittyState()
	state.AddImage(&vt.KittyImage{
		ID:     id,
		Width:  width,
		Height: height,
		Format: format,
		Data:   append([]byte(nil), data...),
	})
	// Every patch carries q=2, so the host answers nothing at all: what it did
	// with the pixels is the only evidence there is.
	h := &replayHost{state: state, id: id}
	h.handler = vt.NewKittyGraphicsHandler(vt.NewScreen(80, 24), state, io.Discard)
	return h
}

// image returns what the host now holds under this id. It is resolved again
// after every replay rather than remembered, because the pane shows whatever
// the id resolves to, and a host is free to replace the image behind it.
func (h *replayHost) image(t *testing.T) *vt.KittyImage {
	t.Helper()
	img := h.state.GetImage(h.id)
	if img == nil {
		t.Fatalf("host no longer holds image %d at all", h.id)
	}
	return img
}

func (h *replayHost) replay(t *testing.T, out []byte) {
	t.Helper()
	seqs := splitAPC(t, out)
	if len(seqs) == 0 {
		t.Fatal("no APC sequences to replay")
	}
	for _, seq := range seqs {
		cmd, err := vt.ParseKittyCommand(seq)
		if err != nil {
			t.Fatalf("host could not parse %q: %v", seq, err)
		}
		if !h.handler.HandleCommand(cmd) {
			t.Fatalf("host rejected sequence %q", seq)
		}
	}
}

func rectContaining(rects []damageRect, x, y int) string {
	for _, r := range rects {
		if x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h {
			return fmt.Sprintf("inside rect %+v", r)
		}
	}
	return "outside every damage rect"
}

// assertHostShowsFrame is the round trip itself: after the patches, the pixels
// the host holds must be the pixels the guest drew.
func assertHostShowsFrame(t *testing.T, host *replayHost, want []byte, width, height, bpp int, rects []damageRect) {
	t.Helper()
	img := host.image(t)
	if img.Width != width || img.Height != height {
		t.Fatalf("host image is now %dx%d, want %dx%d", img.Width, img.Height, width, height)
	}
	got := img.Data
	if len(want) != len(got) {
		t.Fatalf("host holds %d bytes, frame is %d bytes", len(got), len(want))
	}
	stride := width * bpp
	for i := range want {
		if want[i] == got[i] {
			continue
		}
		x, y := (i%stride)/bpp, i/stride
		t.Fatalf("round trip lost pixel (%d,%d) channel %d: want %d, got %d (%s)",
			x, y, i%bpp, want[i], got[i], rectContaining(rects, x, y))
	}
}

// TestBitmapPatchRoundTripReproducesFrame is the property the whole damage path
// exists to keep: whatever tuios sends as frame edits, a correct kitty host
// applying them ends up holding exactly the bitmap the guest drew.
func TestBitmapPatchRoundTripReproducesFrame(t *testing.T) {
	cases := []struct {
		name   string
		width  int
		height int
		format vt.KittyGraphicsFormat
		mutate func(b []byte, width, height, bpp int)
	}{
		{
			name: "one small change", width: 96, height: 64, format: vt.KittyFormatRGBA,
			mutate: func(b []byte, w, _, bpp int) { paintRect(b, w, bpp, 10, 10, 3, 3, 1) },
		},
		{
			name: "scattered changes", width: 96, height: 64, format: vt.KittyFormatRGBA,
			mutate: func(b []byte, w, _, bpp int) {
				paintRect(b, w, bpp, 0, 0, 4, 4, 2)
				paintRect(b, w, bpp, 40, 20, 9, 5, 3)
				paintRect(b, w, bpp, 70, 33, 12, 7, 4)
				paintRect(b, w, bpp, 33, 55, 6, 6, 5)
			},
		},
		{
			// The grid is 32 px wide and 8 rows tall, so the final tile and the
			// final band are both partial whenever the bitmap does not divide
			// evenly. A rectangle clipped wrong here writes past the row.
			name: "last row and last column", width: 96, height: 64, format: vt.KittyFormatRGBA,
			mutate: func(b []byte, w, h, bpp int) {
				paintRect(b, w, bpp, w-1, h-1, 1, 1, 6)
				paintRect(b, w, bpp, 0, h-1, w, 1, 7)
				paintRect(b, w, bpp, w-1, 0, 1, h, 8)
			},
		},
		{
			name: "geometry off the grid", width: 37, height: 13, format: vt.KittyFormatRGBA,
			mutate: func(b []byte, w, h, bpp int) {
				paintRect(b, w, bpp, 33, 9, 4, 4, 9)
				paintRect(b, w, bpp, 1, 0, 2, 2, 10)
				paintRect(b, w, bpp, w-1, h-1, 1, 1, 11)
			},
		},
		{
			name: "three byte pixels off the grid", width: 45, height: 21, format: vt.KittyFormatRGB,
			mutate: func(b []byte, w, h, bpp int) {
				paintRect(b, w, bpp, 40, 16, 5, 5, 12)
				paintRect(b, w, bpp, 0, 8, 3, 1, 13)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withAnimatingHost(t)
			kp := newDamagePassthrough(t)

			bpp := rawBytesPerPixel(tc.format)
			const hostID = uint32(7)
			old := makeBitmap(tc.width, tc.height, bpp, 1)

			if update, out := emitAndCapture(kp, "win", hostID, tc.format,
				vt.KittyCompressionNone, tc.width, tc.height, old); update != bitmapFull || len(out) != 0 {
				t.Fatalf("seed frame: update=%v, %d bytes appended; want bitmapFull and nothing appended",
					update, len(out))
			}

			host := newReplayHost(hostID, tc.format, tc.width, tc.height, old)

			next := append([]byte(nil), old...)
			tc.mutate(next, tc.width, tc.height, bpp)

			update, out := emitAndCapture(kp, "win", hostID, tc.format,
				vt.KittyCompressionNone, tc.width, tc.height, next)
			if update != bitmapPatched {
				t.Fatalf("changed frame: update=%v, want bitmapPatched", update)
			}

			host.replay(t, out)
			assertHostShowsFrame(t, host, next, tc.width, tc.height, bpp,
				damageRects(old, next, tc.width, tc.height, bpp))
		})
	}
}

// TestBitmapChunkedPatchRoundTripReproducesFrame covers a patch too large for
// one APC sequence. A damage rectangle spanning a whole band of RGBA pixels
// passes the 4096-byte chunk limit at about 96 pixels wide, so a repainting
// pane of any real size sends chunked patches routinely.
func TestBitmapChunkedPatchRoundTripReproducesFrame(t *testing.T) {
	withAnimatingHost(t)
	kp := newDamagePassthrough(t)

	const (
		width  = 256
		height = 16
		bpp    = 4
		hostID = uint32(7)
	)
	old := makeBitmap(width, height, bpp, 1)
	if update, _ := emitAndCapture(kp, "win", hostID, vt.KittyFormatRGBA,
		vt.KittyCompressionNone, width, height, old); update != bitmapFull {
		t.Fatalf("seed frame: update=%v, want bitmapFull", update)
	}
	host := newReplayHost(hostID, vt.KittyFormatRGBA, width, height, old)

	next := append([]byte(nil), old...)
	paintRect(next, width, bpp, 0, 0, width, damageBandHeight, 3)

	update, out := emitAndCapture(kp, "win", hostID, vt.KittyFormatRGBA,
		vt.KittyCompressionNone, width, height, next)
	if update != bitmapPatched {
		t.Fatalf("changed frame: update=%v, want bitmapPatched", update)
	}
	if seqs := splitAPC(t, out); len(seqs) < 2 {
		t.Fatalf("expected a chunked patch, got %d sequence(s)", len(seqs))
	}

	host.replay(t, out)
	assertHostShowsFrame(t, host, next, width, height, bpp,
		damageRects(old, next, width, height, bpp))
}

// TestBitmapPatchRoundTripAcrossFrames walks several frames through the same
// host image, because every patch after the first is measured against a bitmap
// the host built from patches rather than from a transmission.
func TestBitmapPatchRoundTripAcrossFrames(t *testing.T) {
	withAnimatingHost(t)
	kp := newDamagePassthrough(t)

	const (
		width  = 96
		height = 64
		bpp    = 4
		hostID = uint32(3)
	)
	frame := makeBitmap(width, height, bpp, 1)
	if update, _ := emitAndCapture(kp, "win", hostID, vt.KittyFormatRGBA,
		vt.KittyCompressionNone, width, height, frame); update != bitmapFull {
		t.Fatalf("seed frame: update=%v, want bitmapFull", update)
	}
	host := newReplayHost(hostID, vt.KittyFormatRGBA, width, height, frame)

	for step := range 6 {
		prev := append([]byte(nil), frame...)
		frame = append([]byte(nil), frame...)
		paintRect(frame, width, bpp, (step*13)%80, (step*9)%56, 7, 5, step+20)

		update, out := emitAndCapture(kp, "win", hostID, vt.KittyFormatRGBA,
			vt.KittyCompressionNone, width, height, frame)
		if update != bitmapPatched {
			t.Fatalf("frame %d: update=%v, want bitmapPatched", step, update)
		}
		host.replay(t, out)
		assertHostShowsFrame(t, host, frame, width, height, bpp,
			damageRects(prev, frame, width, height, bpp))
	}
}

// TestBitmapUnchangedFrameSendsNothing covers the idle guest: a page that stops
// animating re-sends the same pixels and must cost nothing on the wire.
func TestBitmapUnchangedFrameSendsNothing(t *testing.T) {
	withAnimatingHost(t)
	kp := newDamagePassthrough(t)

	bitmap := makeBitmap(96, 64, 4, 2)
	if update, _ := emitAndCapture(kp, "win", 1, vt.KittyFormatRGBA,
		vt.KittyCompressionNone, 96, 64, bitmap); update != bitmapFull {
		t.Fatalf("seed frame: update=%v, want bitmapFull", update)
	}

	update, out := emitAndCapture(kp, "win", 1, vt.KittyFormatRGBA,
		vt.KittyCompressionNone, 96, 64, append([]byte(nil), bitmap...))
	if update != bitmapUnchanged {
		t.Fatalf("identical frame: update=%v, want bitmapUnchanged", update)
	}
	if len(out) != 0 {
		t.Fatalf("identical frame appended %d bytes: %q", len(out), out)
	}
}

// TestBitmapNonDiffablePayloadsGoWhole guards the regression that started this:
// a 3-byte payload declaring a large image was treated as a bitmap, so a later
// frame was compared against pixels that were never there.
func TestBitmapNonDiffablePayloadsGoWhole(t *testing.T) {
	cases := []struct {
		name        string
		format      vt.KittyGraphicsFormat
		compression vt.KittyGraphicsCompression
		payload     []byte
	}{
		{"short payload", vt.KittyFormatRGBA, vt.KittyCompressionNone, []byte{1, 2, 3}},
		{"zlib compressed", vt.KittyFormatRGBA, vt.KittyCompressionZlib, zlibCompress(makeBitmap(96, 64, 4, 4))},
		{"png format", vt.KittyFormatPNG, vt.KittyCompressionNone, []byte("\x89PNG\r\n\x1a\n and some more bytes")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withAnimatingHost(t)
			kp := newDamagePassthrough(t)

			seed := makeBitmap(96, 64, 4, 5)
			if update, _ := emitAndCapture(kp, "win", 9, vt.KittyFormatRGBA,
				vt.KittyCompressionNone, 96, 64, seed); update != bitmapFull {
				t.Fatal("seed frame did not go whole")
			}
			if kp.bitmapCacheFor("win", 9) == nil {
				t.Fatal("seed frame left no cache entry")
			}

			update, out := emitAndCapture(kp, "win", 9, tc.format, tc.compression, 96, 64, tc.payload)
			if update != bitmapFull {
				t.Fatalf("update=%v, want bitmapFull", update)
			}
			if len(out) != 0 {
				t.Fatalf("appended %d bytes for a frame the caller sends whole", len(out))
			}
			if entry := kp.bitmapCacheFor("win", 9); entry != nil {
				t.Fatalf("cache entry survived an undiffable frame (%d bytes, %dx%d): "+
					"a later frame would patch against pixels the host never got",
					len(entry.data), entry.width, entry.height)
			}

			// The host now holds whatever that payload drew, which we cannot
			// reason about, so the next raw frame must go whole as well.
			if update, _ := emitAndCapture(kp, "win", 9, vt.KittyFormatRGBA,
				vt.KittyCompressionNone, 96, 64, seed); update != bitmapFull {
				t.Fatalf("frame after an undiffable one: update=%v, want bitmapFull", update)
			}
		})
	}
}

// TestBitmapWithoutHostAnimationGoesWhole: with no frame-edit support on the
// host, a patch would be an error response and a frozen image.
func TestBitmapWithoutHostAnimationGoesWhole(t *testing.T) {
	withClientCaps(t, &HostCapabilities{
		TerminalName:   "xterm-kitty",
		KittyGraphics:  true,
		KittyAnimation: false,
		CellWidth:      10,
		CellHeight:     20,
	})
	kp := newDamagePassthrough(t)

	first := makeBitmap(96, 64, 4, 6)
	if update, _ := emitAndCapture(kp, "win", 2, vt.KittyFormatRGBA,
		vt.KittyCompressionNone, 96, 64, first); update != bitmapFull {
		t.Fatal("seed frame did not go whole")
	}

	second := append([]byte(nil), first...)
	paintRect(second, 96, 4, 5, 5, 4, 4, 7)

	update, out := emitAndCapture(kp, "win", 2, vt.KittyFormatRGBA,
		vt.KittyCompressionNone, 96, 64, second)
	if update != bitmapFull {
		t.Fatalf("changed frame on a non-animating host: update=%v, want bitmapFull", update)
	}
	if len(out) != 0 {
		t.Fatalf("appended %d bytes to a host that cannot apply them: %q", len(out), out)
	}
	if kp.canPatchBitmap("win", 2) {
		t.Fatal("canPatchBitmap must refuse a host without animation")
	}
}

// TestBitmapGeometryChangeReseeds: a resized image shares its id with the old
// one, and patching new dimensions into the old canvas would land the pixels at
// the wrong offsets.
func TestBitmapGeometryChangeReseeds(t *testing.T) {
	withAnimatingHost(t)
	kp := newDamagePassthrough(t)

	wide := makeBitmap(96, 64, 4, 8)
	if update, _ := emitAndCapture(kp, "win", 4, vt.KittyFormatRGBA,
		vt.KittyCompressionNone, 96, 64, wide); update != bitmapFull {
		t.Fatal("seed frame did not go whole")
	}

	tall := makeBitmap(64, 96, 4, 9)
	update, out := emitAndCapture(kp, "win", 4, vt.KittyFormatRGBA,
		vt.KittyCompressionNone, 64, 96, tall)
	if update != bitmapFull {
		t.Fatalf("resized frame: update=%v, want bitmapFull", update)
	}
	if len(out) != 0 {
		t.Fatalf("resized frame appended %d bytes", len(out))
	}

	// Reseeded means the new geometry is now the baseline.
	if update, _ := emitAndCapture(kp, "win", 4, vt.KittyFormatRGBA,
		vt.KittyCompressionNone, 64, 96, append([]byte(nil), tall...)); update != bitmapUnchanged {
		t.Fatalf("frame after resize: update=%v, want bitmapUnchanged", update)
	}
}

// TestDamageRectsCoverEveryChangedPixel: a pixel outside every rectangle is a
// pixel the host never repaints, which is a stale patch of screen that stays
// wrong until something else forces a whole frame.
func TestDamageRectsCoverEveryChangedPixel(t *testing.T) {
	const (
		width  = 256
		height = 128
		bpp    = 4
		// Few enough scattered pixels that the tiles they damage stay well
		// under maxDamageShare, so rectangles are what comes back.
		changes = 16
	)
	prev := makeBitmap(width, height, bpp, 10)
	cur := append([]byte(nil), prev...)

	rng := rand.New(rand.NewPCG(0x9e3779b9, 0x243f6a88))
	for range changes {
		x := rng.IntN(width)
		y := rng.IntN(height)
		off := (y*width + x) * bpp
		for k := range bpp {
			cur[off+k] ^= 0xA5
		}
	}

	rects := damageRects(prev, cur, width, height, bpp)
	if len(rects) == 0 {
		t.Fatal("scattered damage produced no rectangles")
	}
	stride := width * bpp
	for i := range prev {
		if prev[i] == cur[i] {
			continue
		}
		x, y := (i%stride)/bpp, i/stride
		if rectContaining(rects, x, y) == "outside every damage rect" {
			t.Fatalf("changed pixel (%d,%d) is covered by no rectangle", x, y)
		}
	}
}

// TestDamageRectsNilWhenWholeBitmapChanges: past maxDamageShare a patch costs
// what the bitmap costs, so the caller is told to send the bitmap.
func TestDamageRectsNilWhenWholeBitmapChanges(t *testing.T) {
	const (
		width  = 100
		height = 50
		bpp    = 4
	)
	prev := makeBitmap(width, height, bpp, 11)
	cur := make([]byte, len(prev))
	for i := range cur {
		cur[i] = prev[i] ^ 0xFF
	}

	if rects := damageRects(prev, cur, width, height, bpp); rects != nil {
		t.Fatalf("a bitmap that changed everywhere produced %d rectangles, want nil", len(rects))
	}
}

// TestRewriteKittyImageIDReplacesOnlyImageID: the payload can be megabytes and
// is never re-encoded, so the rewrite has to be a byte-level edit of one
// parameter and nothing else.
func TestRewriteKittyImageIDReplacesOnlyImageID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"frame edit with payload",
			"\x1b_Ga=f,i=7,r=1,X=1,f=32,s=4,v=4,x=0,y=0,q=2;AAECAwQFBgc=\x1b\\",
			"\x1b_Ga=f,i=99,r=1,X=1,f=32,s=4,v=4,x=0,y=0,q=2;AAECAwQFBgc=\x1b\\",
		},
		{
			"no payload at all",
			"\x1b_Ga=a,i=7,s=3,q=2\x1b\\",
			"\x1b_Ga=a,i=99,s=3,q=2\x1b\\",
		},
		{
			// I= is the image NUMBER and addresses a different thing entirely;
			// rewriting it would retarget the command.
			"image number left alone",
			"\x1b_Ga=f,I=7,i=7,q=2;QUJD\x1b\\",
			"\x1b_Ga=f,I=7,i=99,q=2;QUJD\x1b\\",
		},
		{
			"payload that looks like a parameter",
			"\x1b_Ga=f,i=7,q=2;i=7,i=7\x1b\\",
			"\x1b_Ga=f,i=99,q=2;i=7,i=7\x1b\\",
		},
		{
			"i comes last",
			"\x1b_Ga=f,q=2,i=7\x1b\\",
			"\x1b_Ga=f,q=2,i=99\x1b\\",
		},
		{
			"not an APC graphics sequence",
			"\x1b[31mhello",
			"\x1b[31mhello",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rewriteKittyImageID([]byte(tc.in), 99)
			if string(got) != tc.want {
				t.Fatalf("rewrite gave %q, want %q", got, tc.want)
			}
		})
	}
}

// TestForgetBitmapsAfterDeleteForcesFullFrame: a delete means the host may no
// longer hold the image, and a patch against an image that is gone paints
// nothing at all.
func TestForgetBitmapsAfterDeleteForcesFullFrame(t *testing.T) {
	withAnimatingHost(t)
	kp := newDamagePassthrough(t)

	const guestID = uint32(11)
	kp.imageIDMap["win"] = map[uint32]uint32{guestID: 5}

	bitmap := makeBitmap(96, 64, 4, 12)
	if update, _ := emitAndCapture(kp, "win", 5, vt.KittyFormatRGBA,
		vt.KittyCompressionNone, 96, 64, bitmap); update != bitmapFull {
		t.Fatal("seed frame did not go whole")
	}

	kp.forwardDelete(&vt.KittyCommand{
		Action:  vt.KittyActionDelete,
		Delete:  vt.KittyDeleteByID,
		ImageID: guestID,
	}, "win")

	if entry := kp.bitmapCacheFor("win", 5); entry != nil {
		t.Fatal("cached bitmap survived a delete")
	}

	changed := append([]byte(nil), bitmap...)
	paintRect(changed, 96, 4, 2, 2, 4, 4, 13)
	update, out := emitAndCapture(kp, "win", 5, vt.KittyFormatRGBA,
		vt.KittyCompressionNone, 96, 64, changed)
	if update != bitmapFull {
		t.Fatalf("frame after a delete: update=%v, want bitmapFull", update)
	}
	if len(out) != 0 {
		t.Fatalf("patched against a deleted image (%d bytes)", len(out))
	}
}

// TestBitmapPacingDropsOnlyReusedFrames: pacing against a slow host may only
// drop a frame that has something behind it. Dropping a first frame leaves the
// pane blank with nothing to redraw it.
func TestBitmapPacingDropsOnlyReusedFrames(t *testing.T) {
	withAnimatingHost(t)
	kp := newDamagePassthrough(t)

	const (
		width  = 32
		height = 16
	)
	path := filepath.Join(t.TempDir(), "frame.rgba")
	writeFrame := func(seed int) {
		if err := os.WriteFile(path, makeBitmap(width, height, 4, seed), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	send := func(guestID uint32) {
		kp.forwardFileTransmitInline(&vt.KittyCommand{
			Action:  vt.KittyActionTransmit,
			ImageID: guestID,
			Format:  vt.KittyFormatRGBA,
			Medium:  vt.KittyMediumFile,
			Width:   width,
			Height:  height,
			Quiet:   2,
		}, path, "win", false, 0, 0, 40, 20, 1, 1, 0, 0, 0, true)
	}

	// A slow host is simulated by setting the pacing deadline directly rather
	// than by trying to make a real write take too long. The window is set
	// afresh before each send so no amount of scheduling delay between the
	// assertions can let it lapse mid-test.
	holdHost := func() {
		kp.pacedUntilNanos.Store(time.Now().Add(time.Minute).UnixNano())
		if !kp.hostBacklogged() {
			t.Fatal("host should count as backlogged while its pacing hold stands")
		}
	}

	holdHost()
	writeFrame(1)
	send(21)
	if len(kp.pendingOutput) == 0 {
		t.Fatal("first frame of a stream was dropped: the pane has nothing to show")
	}

	afterFirst := len(kp.pendingOutput)
	holdHost()
	writeFrame(2)
	send(21)
	if len(kp.pendingOutput) != afterFirst {
		t.Fatalf("reused frame was sent while the host is backlogged (%d bytes)",
			len(kp.pendingOutput)-afterFirst)
	}

	holdHost()
	writeFrame(3)
	send(22)
	if len(kp.pendingOutput) == afterFirst {
		t.Fatal("first frame of a second stream was dropped")
	}

	// With the host draining again the stream resumes.
	kp.pacedUntilNanos.Store(0)
	afterSecond := len(kp.pendingOutput)
	writeFrame(4)
	send(21)
	if len(kp.pendingOutput) == afterSecond {
		t.Fatal("no frame sent once the host caught up")
	}
}
