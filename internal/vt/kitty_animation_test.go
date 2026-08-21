package vt

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// animHarness is a graphics handler wired to a throwaway screen, with the
// bytes it writes back to the guest captured so the error paths can be
// asserted on the wire rather than on internal state.
type animHarness struct {
	handler *KittyGraphicsHandler
	state   *KittyState
	out     *bytes.Buffer
}

func newAnimHarness(t *testing.T) *animHarness {
	t.Helper()
	state := NewKittyState()
	out := new(bytes.Buffer)
	return &animHarness{
		handler: NewKittyGraphicsHandler(NewScreen(80, 24), state, out),
		state:   state,
		out:     out,
	}
}

// send drives one APC payload through the real parse path. Animation reuses
// r/c/z/X/Y/C for different meanings per action, and building the wire string
// is the only way to cover that mapping instead of assuming it.
func (a *animHarness) send(t *testing.T, control string, payload []byte) {
	t.Helper()
	a.out.Reset()
	apc := control
	if payload != nil {
		apc += ";" + base64.StdEncoding.EncodeToString(payload)
	}
	cmd, err := ParseKittyCommand([]byte(apc))
	if err != nil {
		t.Fatalf("parse %q: %v", control, err)
	}
	if cmd == nil {
		t.Fatalf("parse %q: nil command", control)
	}
	if !a.handler.HandleCommand(cmd) {
		t.Fatalf("handler did not claim %q", control)
	}
}

// transmitRGBA stores a raw RGBA image and returns it.
func (a *animHarness) transmitRGBA(t *testing.T, id uint32, w, h int, data []byte) *KittyImage {
	t.Helper()
	a.send(t, fmt.Sprintf("a=t,i=%d,f=32,t=d,s=%d,v=%d", id, w, h), data)
	img := a.state.GetImage(id)
	if img == nil {
		t.Fatalf("image %d was not stored", id)
	}
	return img
}

// gradientRGBA builds a canvas where every pixel is distinct, so a copy that
// lands at the wrong coordinate shows up as the wrong pixel rather than as a
// plausible colour.
func gradientRGBA(w, h int) []byte {
	data := make([]byte, w*h*4)
	for y := range h {
		for x := range w {
			off := (y*w + x) * 4
			data[off] = byte(0x10 + x)
			data[off+1] = byte(0x20 + y)
			data[off+2] = byte(0x30 + y*w + x)
			data[off+3] = 0xFF
		}
	}
	return data
}

// solidRGBA builds a w x h canvas of one repeated pixel.
func solidRGBA(w, h int, px []byte) []byte {
	data := make([]byte, 0, w*h*len(px))
	for range w * h {
		data = append(data, px...)
	}
	return data
}

// pixelAt returns one pixel of a raw canvas of the given width.
func pixelAt(t *testing.T, data []byte, w, bpp, x, y int) []byte {
	t.Helper()
	off := (y*w + x) * bpp
	if off+bpp > len(data) {
		t.Fatalf("canvas of %d bytes has no pixel (%d,%d)", len(data), x, y)
	}
	return data[off : off+bpp]
}

// assertPixels compares every pixel of a frame against want, reporting the
// first mismatch by coordinate.
func assertPixels(t *testing.T, got []byte, w, h, bpp int, want func(x, y int) []byte) {
	t.Helper()
	for y := range h {
		for x := range w {
			if g, w2 := pixelAt(t, got, w, bpp, x, y), want(x, y); !bytes.Equal(g, w2) {
				t.Fatalf("pixel (%d,%d) = % x, want % x", x, y, g, w2)
			}
		}
	}
}

// animatedImage builds a 2x2 image with one frame per gap, frame n holding for
// gaps[n-1] milliseconds. Pixels do not matter to the timing tests; known gaps
// do.
func animatedImage(t *testing.T, a *animHarness, id uint32, gaps []int) *KittyImage {
	t.Helper()
	img := a.transmitRGBA(t, id, 2, 2, gradientRGBA(2, 2))
	for i, gap := range gaps {
		if i == 0 {
			// The root frame has no a=f of its own, so its gap is set the
			// same way a client retimes any frame.
			a.send(t, fmt.Sprintf("a=a,i=%d,r=1,z=%d", id, gap), nil)
			continue
		}
		a.send(t, fmt.Sprintf("a=f,i=%d,z=%d,s=1,v=1,x=0,y=0,X=1", id, gap),
			solidRGBA(1, 1, []byte{byte(i), byte(i), byte(i), 0xFF}))
	}
	if got := img.FrameCount(); got != len(gaps) {
		t.Fatalf("FrameCount() = %d, want %d", got, len(gaps))
	}
	return img
}

func TestKittyAnimFrameAppendsCompositeOverBackground(t *testing.T) {
	a := newAnimHarness(t)
	root := gradientRGBA(4, 4)
	img := a.transmitRGBA(t, 1, 4, 4, append([]byte(nil), root...))

	const bg = uint32(0x11223344)
	patch := []byte{0xAA, 0xBB, 0xCC, 0xFF}
	a.send(t, fmt.Sprintf("a=f,i=1,s=2,v=2,x=1,y=1,Y=%d", bg), solidRGBA(2, 2, patch))

	if got := img.FrameCount(); got != 2 {
		t.Fatalf("FrameCount() = %d, want 2", got)
	}
	bgPixel := []byte{0x11, 0x22, 0x33, 0x44}
	assertPixels(t, img.frameData(2), 4, 4, 4, func(x, y int) []byte {
		if x >= 1 && x <= 2 && y >= 1 && y <= 2 {
			return patch
		}
		return bgPixel
	})

	// An append must leave the frame it was built beside alone.
	if !bytes.Equal(img.Data, root) {
		t.Fatal("appending a frame modified the root frame")
	}
	if got := img.frameGap(2); got != defaultKittyGap {
		t.Fatalf("frameGap(2) = %d, want the default %d", got, defaultKittyGap)
	}
}

func TestKittyAnimFrameEditsExistingFrameInPlace(t *testing.T) {
	a := newAnimHarness(t)
	root := gradientRGBA(4, 4)
	img := a.transmitRGBA(t, 1, 4, 4, append([]byte(nil), root...))

	patch := []byte{0x01, 0x02, 0x03, 0x04}
	a.send(t, "a=f,i=1,r=1,X=1,s=2,v=2,x=1,y=2", solidRGBA(2, 2, patch))

	if got := img.FrameCount(); got != 1 {
		t.Fatalf("editing frame 1 changed FrameCount() to %d, want 1", got)
	}
	assertPixels(t, img.Data, 4, 4, 4, func(x, y int) []byte {
		if x >= 1 && x <= 2 && y >= 2 && y <= 3 {
			return patch
		}
		return pixelAt(t, root, 4, 4, x, y)
	})
}

func TestKittyAnimFrameCopiesNamedBaseFrame(t *testing.T) {
	a := newAnimHarness(t)
	img := a.transmitRGBA(t, 1, 4, 4, gradientRGBA(4, 4))

	const bg = uint32(0x11223344)
	bgPixel := []byte{0x11, 0x22, 0x33, 0x44}
	second := []byte{0xAA, 0xBB, 0xCC, 0xFF}
	a.send(t, fmt.Sprintf("a=f,i=1,s=2,v=2,x=1,y=1,Y=%d", bg), solidRGBA(2, 2, second))

	third := []byte{0x11, 0x99, 0x11, 0xFF}
	a.send(t, "a=f,i=1,c=2,s=1,v=1,x=0,y=0", solidRGBA(1, 1, third))

	if got := img.FrameCount(); got != 3 {
		t.Fatalf("FrameCount() = %d, want 3", got)
	}
	assertPixels(t, img.frameData(3), 4, 4, 4, func(x, y int) []byte {
		switch {
		case x == 0 && y == 0:
			return third
		case x >= 1 && x <= 2 && y >= 1 && y <= 2:
			return second
		default:
			return bgPixel
		}
	})

	// The base is copied, not aliased, so writing frame 3 must not reach
	// frame 2.
	if got := pixelAt(t, img.frameData(2), 4, 4, 0, 0); !bytes.Equal(got, bgPixel) {
		t.Fatalf("base frame pixel (0,0) = % x, want % x", got, bgPixel)
	}
}

func TestKittyAnimFrameBlendsOrOverwrites(t *testing.T) {
	dst := []byte{0x00, 0x00, 0xFF, 0xFF}
	src := []byte{0xFF, 0x00, 0x00, 0x80}

	tests := []struct {
		name  string
		blend int
		want  []byte
	}{
		// 0x80 over 0xFF blue: (255*128 + 0*127)/255 = 128 red,
		// (0*128 + 255*127)/255 = 127 blue, alpha saturates back to 0xFF.
		{name: "X=0 alpha blends", blend: 0, want: []byte{0x80, 0x00, 0x7F, 0xFF}},
		{name: "X=1 overwrites", blend: 1, want: src},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAnimHarness(t)
			img := a.transmitRGBA(t, 1, 2, 2, solidRGBA(2, 2, dst))

			a.send(t, fmt.Sprintf("a=f,i=1,r=1,X=%d,s=1,v=1,x=0,y=0", tt.blend),
				solidRGBA(1, 1, src))

			assertPixels(t, img.Data, 2, 2, 4, func(x, y int) []byte {
				if x == 0 && y == 0 {
					return tt.want
				}
				return dst
			})
		})
	}
}

func TestKittyAnimFrameErrorsReachTheGuest(t *testing.T) {
	tests := []struct {
		name    string
		control string
		payload []byte
		want    string
	}{
		{
			name:    "unknown image",
			control: "a=f,i=99,s=2,v=2,x=0,y=0",
			payload: solidRGBA(2, 2, []byte{1, 2, 3, 4}),
			want:    "\x1b_Gi=99;ENOENT:image not found\x1b\\",
		},
		{
			name:    "rect leaves the canvas",
			control: "a=f,i=1,s=4,v=4,x=1,y=1",
			payload: solidRGBA(4, 4, []byte{1, 2, 3, 4}),
			want:    "\x1b_Gi=1;EINVAL:frame rectangle out of bounds\x1b\\",
		},
		{
			name:    "frame number past the end",
			control: "a=f,i=1,r=5,s=2,v=2,x=0,y=0",
			payload: solidRGBA(2, 2, []byte{1, 2, 3, 4}),
			want:    "\x1b_Gi=1;ENOENT:frame not found\x1b\\",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAnimHarness(t)
			img := a.transmitRGBA(t, 1, 4, 4, gradientRGBA(4, 4))

			a.send(t, tt.control, tt.payload)

			if got := a.out.String(); got != tt.want {
				t.Fatalf("response = %q, want %q", got, tt.want)
			}
			if got := img.FrameCount(); got != 1 {
				t.Fatalf("a rejected a=f left %d frames, want 1", got)
			}
		})
	}
}

func TestKittyAnimPlaybackStartsAndStops(t *testing.T) {
	t.Run("s=3 runs", func(t *testing.T) {
		a := newAnimHarness(t)
		img := animatedImage(t, a, 1, []int{100, 100, 100})

		a.send(t, "a=a,i=1,s=3", nil)

		if img.Anim.State != KittyAnimRunning {
			t.Fatalf("State = %d, want running", img.Anim.State)
		}
		if img.Anim.Started.IsZero() {
			t.Fatal("s=3 left Started unset, so nothing can advance")
		}
		if got := img.CurrentFrame(img.Anim.Started.Add(150 * time.Millisecond)); got != 2 {
			t.Fatalf("CurrentFrame(+150ms) = %d, want 2", got)
		}
	})

	t.Run("s=1 freezes the frame that was showing", func(t *testing.T) {
		a := newAnimHarness(t)
		// Gaps of five seconds so the wall-clock cost of the calls between
		// backdating and stopping cannot move the answer.
		img := animatedImage(t, a, 1, []int{5000, 5000, 5000})
		a.send(t, "a=a,i=1,s=3", nil)
		img.Anim.Started = img.Anim.Started.Add(-7500 * time.Millisecond)

		a.send(t, "a=a,i=1,s=1", nil)

		if img.Anim.State != KittyAnimStopped {
			t.Fatalf("State = %d, want stopped", img.Anim.State)
		}
		if img.Anim.Current != 2 {
			t.Fatalf("stopped on frame %d, want the frame that was showing, 2", img.Anim.Current)
		}
		if got := img.CurrentFrame(img.Anim.Started.Add(time.Hour)); got != 2 {
			t.Fatalf("a stopped animation advanced to frame %d", got)
		}
	})

	t.Run("r= and z= retime without disturbing playback", func(t *testing.T) {
		a := newAnimHarness(t)
		img := animatedImage(t, a, 1, []int{100, 100, 100})
		a.send(t, "a=a,i=1,s=3", nil)
		started, state, current := img.Anim.Started, img.Anim.State, img.Anim.Current

		a.send(t, "a=a,i=1,r=2,z=250", nil)

		if got := img.frameGap(2); got != 250 {
			t.Fatalf("frameGap(2) = %d, want 250", got)
		}
		if !img.Anim.Started.Equal(started) {
			t.Fatal("a gap edit restarted the clock")
		}
		if img.Anim.State != state || img.Anim.Current != current {
			t.Fatalf("a gap edit changed playback to state %d frame %d",
				img.Anim.State, img.Anim.Current)
		}
	})

	t.Run("c= jumps to a frame", func(t *testing.T) {
		a := newAnimHarness(t)
		img := animatedImage(t, a, 1, []int{100, 100, 100})
		a.send(t, "a=a,i=1,s=3", nil)

		a.send(t, "a=a,i=1,c=3", nil)

		if img.Anim.Current != 3 {
			t.Fatalf("Current = %d, want 3", img.Anim.Current)
		}
		if got := img.CurrentFrame(img.Anim.Started.Add(50 * time.Millisecond)); got != 3 {
			t.Fatalf("CurrentFrame just after the jump = %d, want 3", got)
		}
		if got := img.CurrentFrame(img.Anim.Started.Add(150 * time.Millisecond)); got != 1 {
			t.Fatalf("CurrentFrame past frame 3's gap = %d, want the wrap to 1", got)
		}
	})
}

func TestKittyAnimCurrentFrameTimeline(t *testing.T) {
	tests := []struct {
		name    string
		control string
		elapsed int
		want    int
	}{
		{name: "before the first gap expires", control: "a=a,i=1,s=3", elapsed: 99, want: 1},
		{name: "on the gap boundary", control: "a=a,i=1,s=3", elapsed: 100, want: 2},
		{name: "last frame", control: "a=a,i=1,s=3", elapsed: 250, want: 3},
		{name: "wraps to the first frame", control: "a=a,i=1,s=3", elapsed: 300, want: 1},
		{name: "second time round", control: "a=a,i=1,s=3", elapsed: 550, want: 3},
		{name: "v=-1 loops forever", control: "a=a,i=1,s=3,v=-1", elapsed: 600, want: 1},
		{name: "v=2 stops on the last frame", control: "a=a,i=1,s=3,v=2", elapsed: 600, want: 3},
		{name: "v=2 stays there", control: "a=a,i=1,s=3,v=2", elapsed: 5000, want: 3},
		{name: "s=2 stops on the last frame", control: "a=a,i=1,s=2", elapsed: 300, want: 3},
		{name: "s=2 waits there", control: "a=a,i=1,s=2", elapsed: 10000, want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAnimHarness(t)
			img := animatedImage(t, a, 1, []int{100, 100, 100})

			a.send(t, tt.control, nil)

			at := img.Anim.Started.Add(time.Duration(tt.elapsed) * time.Millisecond)
			if got := img.CurrentFrame(at); got != tt.want {
				t.Fatalf("CurrentFrame(+%dms) = %d, want %d", tt.elapsed, got, tt.want)
			}
		})
	}
}

func TestKittyAnimAnimationLoopCount(t *testing.T) {
	tests := []struct {
		name    string
		control string
		want    int
	}{
		{name: "v=2 keeps the count", control: "a=a,i=1,s=3,v=2", want: 2},
		{name: "negative v loops forever", control: "a=a,i=1,s=3,v=-1", want: 0},
		{name: "absent v leaves the count alone", control: "a=a,i=1,s=3", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAnimHarness(t)
			img := animatedImage(t, a, 1, []int{100, 100, 100})

			a.send(t, tt.control, nil)

			if img.Anim.Loops != tt.want {
				t.Fatalf("Loops = %d, want %d", img.Anim.Loops, tt.want)
			}
		})
	}
}

func TestKittyAnimComposeCopiesRectBetweenFrames(t *testing.T) {
	a := newAnimHarness(t)
	root := gradientRGBA(4, 4)
	img := a.transmitRGBA(t, 1, 4, 4, append([]byte(nil), root...))

	// Frame 2 is a full-canvas overwrite of a distinct gradient, so a compose
	// that reads the wrong source pixel is visible as the wrong coordinate.
	second := gradientRGBA(4, 4)
	for i := range second {
		second[i] ^= 0x55
	}
	for i := 3; i < len(second); i += 4 {
		second[i] = 0xFF
	}
	a.send(t, "a=f,i=1,s=4,v=4,x=0,y=0,X=1", append([]byte(nil), second...))

	// X and Y are the source origin here, not a blend mode and a background.
	a.send(t, "a=c,i=1,r=1,c=2,w=2,h=2,x=2,y=2,X=1,Y=1,C=1", nil)

	assertPixels(t, img.Data, 4, 4, 4, func(x, y int) []byte {
		if x >= 2 && y >= 2 {
			return pixelAt(t, second, 4, 4, x-1, y-1)
		}
		return pixelAt(t, root, 4, 4, x, y)
	})
	// The source frame is read, never written.
	if !bytes.Equal(img.frameData(2), second) {
		t.Fatal("compose modified its source frame")
	}
}

func TestKittyAnimComposeBlendsOrOverwrites(t *testing.T) {
	dst := []byte{0x00, 0x00, 0xFF, 0xFF}
	src := []byte{0xFF, 0x00, 0x00, 0x80}

	tests := []struct {
		name    string
		compose int
		want    []byte
	}{
		{name: "C=0 alpha blends", compose: 0, want: []byte{0x80, 0x00, 0x7F, 0xFF}},
		{name: "C=1 overwrites", compose: 1, want: src},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAnimHarness(t)
			img := a.transmitRGBA(t, 1, 2, 2, solidRGBA(2, 2, dst))
			a.send(t, "a=f,i=1,s=2,v=2,x=0,y=0,X=1", solidRGBA(2, 2, src))

			a.send(t, fmt.Sprintf("a=c,i=1,r=1,c=2,w=1,h=1,x=0,y=0,X=0,Y=0,C=%d", tt.compose), nil)

			assertPixels(t, img.Data, 2, 2, 4, func(x, y int) []byte {
				if x == 0 && y == 0 {
					return tt.want
				}
				return dst
			})
		})
	}
}

func TestKittyAnimComposeSelfWithOverlappingRect(t *testing.T) {
	a := newAnimHarness(t)
	root := gradientRGBA(4, 4)
	img := a.transmitRGBA(t, 1, 4, 4, append([]byte(nil), root...))

	// Source and destination overlap, so a naive row walk would read pixels it
	// had already overwritten and smear the gradient down the canvas.
	a.send(t, "a=c,i=1,r=1,c=1,w=3,h=3,x=1,y=1,X=0,Y=0,C=1", nil)

	assertPixels(t, img.Data, 4, 4, 4, func(x, y int) []byte {
		if x >= 1 && y >= 1 {
			return pixelAt(t, root, 4, 4, x-1, y-1)
		}
		return pixelAt(t, root, 4, 4, x, y)
	})
}

func TestKittyAnimFrameChunkedTransmission(t *testing.T) {
	a := newAnimHarness(t)
	root := gradientRGBA(4, 4)
	img := a.transmitRGBA(t, 1, 4, 4, append([]byte(nil), root...))

	patch := []byte{
		0x01, 0x02, 0x03, 0xFF,
		0x04, 0x05, 0x06, 0xFF,
		0x07, 0x08, 0x09, 0xFF,
		0x0A, 0x0B, 0x0C, 0xFF,
	}
	const bg = uint32(0x11223344)
	// Parameters ride on the first chunk only; the rest is bare payload.
	a.send(t, fmt.Sprintf("a=f,i=1,s=2,v=2,x=2,y=1,X=1,Y=%d,m=1", bg), patch[:12])
	a.send(t, "a=f,m=0", patch[12:])

	if pending := a.state.GetPending(); pending != nil {
		t.Fatal("the finished chunked frame left a pending transmission behind")
	}
	if got := img.FrameCount(); got != 2 {
		t.Fatalf("FrameCount() = %d, want 2", got)
	}
	bgPixel := []byte{0x11, 0x22, 0x33, 0x44}
	assertPixels(t, img.frameData(2), 4, 4, 4, func(x, y int) []byte {
		if x >= 2 && y >= 1 && y <= 2 {
			return pixelAt(t, patch, 2, 4, x-2, y-1)
		}
		return bgPixel
	})
	if !bytes.Equal(img.Data, root) {
		t.Fatal("the chunked frame overwrote the root frame")
	}
}

// nonTempDir returns a directory that removeTempTransmitFile must refuse to
// delete from. The refusal is keyed on the temp roots, so a checkout that
// itself lives under one leaves nothing to assert.
func nonTempDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("cannot resolve the working directory: %v", err)
	}
	for _, tmp := range []string{os.TempDir(), "/tmp", "/var/tmp", "/dev/shm"} {
		if tmp == "" {
			continue
		}
		if wd == tmp || strings.HasPrefix(wd, strings.TrimSuffix(tmp, "/")+string(filepath.Separator)) {
			t.Skipf("the package directory %s is itself a temp root", wd)
		}
	}
	dir, err := os.MkdirTemp(wd, "kitty-anim-nontemp-")
	if err != nil {
		t.Skipf("cannot create a directory outside the temp roots: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestKittyAnimFrameTempFileRemoval(t *testing.T) {
	tests := []struct {
		name       string
		dir        func(t *testing.T) string
		base       string
		wantRemove bool
	}{
		{
			name:       "marked file in a temp dir",
			dir:        func(t *testing.T) string { return t.TempDir() },
			base:       "tty-graphics-protocol-frame",
			wantRemove: true,
		},
		{
			name:       "unmarked file in a temp dir",
			dir:        func(t *testing.T) string { return t.TempDir() },
			base:       "someones-spreadsheet",
			wantRemove: false,
		},
		{
			name:       "marked file outside a temp dir",
			dir:        nonTempDir,
			base:       "tty-graphics-protocol-frame",
			wantRemove: false,
		},
	}

	patch := []byte{0x01, 0x02, 0x03, 0x04}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(tt.dir(t), tt.base)
			if err := os.WriteFile(path, solidRGBA(2, 2, patch), 0o600); err != nil {
				t.Fatalf("write transmit file: %v", err)
			}

			a := newAnimHarness(t)
			root := gradientRGBA(4, 4)
			img := a.transmitRGBA(t, 1, 4, 4, append([]byte(nil), root...))
			a.send(t, "a=f,i=1,r=1,t=t,X=1,s=2,v=2,x=0,y=0", []byte(path))

			// The file is read either way; only the deletion differs.
			assertPixels(t, img.Data, 4, 4, 4, func(x, y int) []byte {
				if x <= 1 && y <= 1 {
					return patch
				}
				return pixelAt(t, root, 4, 4, x, y)
			})

			_, err := os.Stat(path)
			if tt.wantRemove && !os.IsNotExist(err) {
				t.Fatalf("expected %s to be deleted, stat gave %v", path, err)
			}
			if !tt.wantRemove && err != nil {
				t.Fatalf("expected %s to survive: %v", path, err)
			}
		})
	}
}

func TestKittyAnimFrameSharedMemoryUnlinks(t *testing.T) {
	f, err := os.CreateTemp("/dev/shm", "tuios-anim-shm-*")
	if err != nil {
		t.Skipf("cannot create a /dev/shm test file: %v", err)
	}
	shmPath := f.Name()
	name := filepath.Base(shmPath)
	t.Cleanup(func() { _ = os.Remove(shmPath) })

	patch := []byte{0x0A, 0x0B, 0x0C, 0xFF}
	if _, err := f.Write(solidRGBA(2, 2, patch)); err != nil {
		_ = f.Close()
		t.Fatalf("write shm file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close shm file: %v", err)
	}

	a := newAnimHarness(t)
	root := gradientRGBA(4, 4)
	img := a.transmitRGBA(t, 1, 4, 4, append([]byte(nil), root...))
	a.send(t, "a=f,i=1,r=1,t=s,X=1,s=2,v=2,x=1,y=1", []byte(name))

	assertPixels(t, img.Data, 4, 4, 4, func(x, y int) []byte {
		if x >= 1 && x <= 2 && y >= 1 && y <= 2 {
			return patch
		}
		return pixelAt(t, root, 4, 4, x, y)
	})

	// Nothing else ever removes these, so a guest streaming frames leaks tmpfs
	// at frame rate if the read does not unlink.
	if _, err := os.Stat(shmPath); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be unlinked after the read, stat gave %v", shmPath, err)
	}
}
