package app

import (
	"image"
	"image/color"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// sixelTestImage is cols x rows cells of 10x20 pixels, each cell its own
// colour.
func sixelTestImage(cols, rows int) []byte {
	img := &vt.SixelImage{Width: cols * 10, Height: rows * 20, Palette: make([]color.RGBA, vt.SixelMaxRegisters)}
	img.Pix = make([]uint16, img.Width*img.Height)
	for r := range rows {
		for c := range cols {
			reg := (r*cols + c) % 255
			img.Palette[reg] = color.RGBA{uint8(reg), uint8(255 - reg), uint8(r * 10), 255}
			for y := r * 20; y < (r+1)*20; y++ {
				for x := c * 10; x < (c+1)*10; x++ {
					img.Pix[y*img.Width+x] = uint16(reg + 1)
				}
			}
		}
	}
	return vt.EncodeSixel(img, image.Rect(0, 0, img.Width, img.Height), img.Width, img.Height)
}

// sixelOS is one bordered pane at (2,1), 40x12, drawing to a sixel host with
// 10x20 cells.
func sixelOS(t *testing.T, mode sixelMode) (*OS, *terminal.Window) {
	t.Helper()
	win := newTestWindow(t, "sixel-win-0001", 40, 12)
	win.X, win.Y, win.Width, win.Height = 2, 1, 40, 12
	win.Workspace = 1
	m := paneBgOS(t, "", win)
	m.Width, m.Height = 100, 30
	m.Caps = &HostCapabilities{SixelGraphics: mode == sixelNative, KittyGraphics: mode == sixelViaKitty, CellWidth: 10, CellHeight: 20}
	m.SixelPassthrough = NewSixelPassthroughWithOptions(SixelPassthroughOptions{Caps: m.Caps})
	win.SetCellPixelDimensions(10, 20)
	win.LockIO()
	m.setupSixelPassthrough(win)
	win.UnlockIO()
	return m, win
}

func (m *OS) testFrame() []sixelRect {
	_ = m.composeFrame()
	m.SixelPassthrough.mu.Lock()
	defer m.SixelPassthrough.mu.Unlock()
	return append([]sixelRect(nil), m.SixelPassthrough.frame.rects...)
}

func TestSixelFrameFindsImageAndClips(t *testing.T) {
	m, win := sixelOS(t, sixelNative)
	// 50 columns in a pane with 38: the host gets the first 38.
	win.WriteOutput([]byte("top\r\n"))
	win.WriteOutput(sixelTestImage(50, 3))
	win.MarkContentDirty()

	rects := m.testFrame()
	if len(rects) != 1 {
		t.Fatalf("rects = %+v, want one", rects)
	}
	r := rects[0]
	// Content starts inside the border at (3,2); the image is on the pane's
	// second row.
	if r.x != 3 || r.y != 3 || r.r0 != 0 || r.c0 != 0 || r.rows() != 3 || r.cols() != 38 {
		t.Fatalf("rect = %+v, want 38x3 at 3,3 from image 0,0", r)
	}
	out := string(m.GraphicsFrameBytes(nil))
	if !strings.Contains(out, "\x1b[4;4H\x1bP") {
		t.Fatalf("host was not sent a sixel at row 4 col 4: %q", out[:min(80, len(out))])
	}
	// The crop, decoded, is 380x60 pixels.
	i := strings.Index(out, "\x1bP")
	j := strings.Index(out[i:], "\x1b\\")
	img := vt.DecodeSixel(vt.ParseSixelCommand([]byte(out[i+2 : i+j])))
	if img == nil || img.Width != 380 || img.Height != 60 {
		t.Fatalf("crop decoded to %+v, want 380x60", img)
	}
	// Nothing new and nothing written over it: nothing sent.
	m.testFrame()
	if again := m.GraphicsFrameBytes(nil); len(again) != 0 {
		t.Fatalf("an unchanged frame was sent again: %q", again[:min(40, len(again))])
	}
	// A renderer write that rewrites one of the image's cells has it sent
	// again, and one that writes elsewhere does not.
	if again := m.GraphicsFrameBytes([]byte("\x1b[1;1Hx")); len(again) != 0 {
		t.Fatalf("a write outside the image had it sent again")
	}
	if again := m.GraphicsFrameBytes([]byte("\x1b[5;10H\x1b[3X")); len(again) == 0 {
		t.Fatalf("an erase inside the image did not have it sent again")
	}

	// A popup over the image cuts it.
	m.ShowCommandPalette = true
	rects = m.testFrame()
	cells := 0
	for _, r := range rects {
		cells += r.rows() * r.cols()
	}
	if cells == 0 || cells >= 38*3 {
		t.Fatalf("with the palette open %d image cells are visible (%+v), want some but not all", cells, rects)
	}
	m.ShowCommandPalette = false
	rects = m.testFrame()
	if len(rects) != 1 || rects[0].cols() != 38 {
		t.Fatalf("after the palette closed rects = %+v", rects)
	}

	// Clearing the pane leaves nothing to show.
	win.WriteOutput([]byte("\x1b[2J"))
	win.MarkContentDirty()
	if rects = m.testFrame(); len(rects) != 0 {
		t.Fatalf("after clear rects = %+v", rects)
	}
}

// TestSixelRectsAroundAPopup checks the visible cells of one image, with a
// popup over its bottom-right corner, come out as the two rectangles that
// cover exactly what is left.
func TestSixelRectsAroundAPopup(t *testing.T) {
	var hits []sixelHit
	for row := range 4 {
		for col := range 6 {
			if row >= 2 && col >= 3 {
				continue // under the popup
			}
			hits = append(hits, sixelHit{x: 10 + col, y: 5 + row, row: row, col: col})
		}
	}
	rects := sixelRects(9, hits, true, 40)
	cells := 0
	for _, r := range rects {
		cells += r.rows() * r.cols()
	}
	if len(rects) != 2 || cells != 4*6-2*3 {
		t.Fatalf("rects = %+v, want two covering %d cells", rects, 4*6-2*3)
	}
	// On the host's last row nothing is sent: a sixel there scrolls the host.
	last := sixelRects(9, []sixelHit{{x: 0, y: 38, row: 0, col: 0}, {x: 0, y: 39, row: 1, col: 0}}, true, 40)
	if len(last) != 1 || last[0].rows() != 1 {
		t.Fatalf("an image on the last row gave %+v, want its first row only", last)
	}
}

// TestHostDamage checks the renderer's output is read into the cells it
// wrote: text, erases, a repeat, and a whole-screen redraw.
func TestHostDamage(t *testing.T) {
	var d hostDamage
	d.resize(20, 5)
	d.reset()
	rect := sixelRect{x: 5, y: 2, r1: 2, c1: 4} // cells 5..8 on rows 2..3
	for _, tc := range []struct {
		name string
		in   string
		hit  bool
	}{
		{"text beside", "\x1b[3;1Habcd", false},
		{"text over", "\x1b[3;1Habcdefg", true},
		{"relative move then text", "\x1b[4;1H\x1b[6Cx", true},
		{"erase to end of line", "\x1b[3;10H\x1b[K", false},
		{"erase line from left", "\x1b[4;7H\x1b[1K", true},
		{"repeat", "\x1b[3;1Hx\x1b[5b", true},
		{"line below", "\x1b[5;1H\x1b[2K", false},
		{"screen erase", "\x1b[2J", true},
		{"alt screen", "\x1b[?1049h", true},
		{"wrap onto the next row", "\x1b[2;18Habcdefghi", true},
	} {
		d.feed([]byte(tc.in))
		if got := d.hit(rect); got != tc.hit {
			t.Errorf("%s: hit = %v, want %v", tc.name, got, tc.hit)
		}
		d.reset()
	}
}

// TestSixelFallbackModes checks a kitty host is sent the image as kitty
// graphics with a source rectangle, and a host with neither protocol gets the
// placeholder box and no graphics at all.
func TestSixelFallbackModes(t *testing.T) {
	m, win := sixelOS(t, sixelViaKitty)
	win.WriteOutput([]byte("top\r\n"))
	win.WriteOutput(sixelTestImage(50, 3))
	win.MarkContentDirty()
	var mu sync.Mutex
	var out string
	m.SixelPassthrough.direct = func(b []byte) { mu.Lock(); out += string(b); mu.Unlock() }
	m.testFrame()
	first := m.GraphicsFrameBytes(nil)
	mu.Lock()
	out += string(first)
	mu.Unlock()
	// The transmission is built in the background and sent when ready.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := strings.Contains(out, "a=p,")
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(out, "a=t,f=32,o=z,s=500,v=60") || !strings.Contains(out, "x=0,y=0,w=380,h=60,c=38,r=3") {
		t.Fatalf("kitty host got %q", out[:min(200, len(out))])
	}
	if strings.Contains(out, "\x1bP") {
		t.Fatalf("kitty host was sent a sixel")
	}

	m, win = sixelOS(t, sixelPlaceholder)
	win.WriteOutput([]byte("top\r\n"))
	win.WriteOutput(sixelTestImage(10, 3))
	win.MarkContentDirty()
	frame := m.composeFrame()
	if !strings.Contains(frame, "┌") || !strings.Contains(frame, "image") {
		t.Fatalf("no placeholder box in the frame")
	}
	if strings.Contains(frame, vt.SixelMarkerLead) {
		t.Fatalf("a marker reached the frame")
	}
	if got := m.GraphicsFrameBytes(nil); len(got) != 0 {
		t.Fatalf("a host with no graphics was sent %q", got)
	}
}

// TestFrameHookWriterOrder checks the hook's output goes after the frame and
// inside the frame's own synchronized update.
func TestFrameHookWriterOrder(t *testing.T) {
	var buf strings.Builder
	w := NewFrameHookWriter(&buf)
	w.SetHook(func([]byte) []byte { return []byte("IMG") })
	_, _ = w.Write([]byte("\x1b[?2026hFRAME\x1b[?2026l"))
	if got := buf.String(); got != "\x1b[?2026hFRAMEIMG\x1b[?2026l" {
		t.Fatalf("wrote %q", got)
	}
}

// TestSixelSweepFreesOverwrittenImages: an animation draws each frame as a
// new image over the last. The images whose cells are all gone are freed on
// the next sweep, not held until the pane's budget evicts them, and the one
// still on screen stays.
func TestSixelSweepFreesOverwrittenImages(t *testing.T) {
	oldEvery, oldGrace := sixelSweepEvery, sixelSweepGrace
	sixelSweepEvery, sixelSweepGrace = 0, 0
	t.Cleanup(func() { sixelSweepEvery, sixelSweepGrace = oldEvery, oldGrace })

	m, win := sixelOS(t, sixelNative)
	for range 20 {
		win.WriteOutput([]byte("\x1b[H"))
		win.WriteOutput(sixelTestImage(20, 3))
	}
	win.MarkContentDirty()
	if n := m.SixelPassthrough.ImageCount(); n != 20 {
		t.Fatalf("held %d images after 20 frames, want 20 before a sweep", n)
	}
	m.testFrame()
	deadline := time.Now().Add(5 * time.Second)
	for m.SixelPassthrough.ImageCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		m.SixelPassthrough.mu.Lock()
		m.SixelPassthrough.lastSweep = time.Time{}
		m.SixelPassthrough.mu.Unlock()
		m.sweepSixelImages()
	}
	if n := m.SixelPassthrough.ImageCount(); n != 1 {
		t.Fatalf("after the sweep %d images are held, want the 1 on screen", n)
	}
	// Clearing the pane leaves nothing to hold.
	win.WriteOutput([]byte("\x1b[2J\x1b[3J"))
	win.MarkContentDirty()
	m.testFrame()
	for m.SixelPassthrough.ImageCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		m.SixelPassthrough.mu.Lock()
		m.SixelPassthrough.lastSweep = time.Time{}
		m.SixelPassthrough.mu.Unlock()
		m.sweepSixelImages()
	}
	if n := m.SixelPassthrough.ImageCount(); n != 0 {
		t.Fatalf("after clear %d images are held", n)
	}
}

// TestSixelUnknownCellsAreBlank: cells naming an image this client never held
// (a pane restored from the daemon) draw as plain blanks, not as a box or
// dots.
func TestSixelUnknownCellsAreBlank(t *testing.T) {
	m, win := sixelOS(t, sixelNative)
	win.LockIO()
	win.Terminal.SetSixelPassthroughFunc(func(*vt.SixelCommand, int, int) uint32 { return 777 })
	win.UnlockIO()
	win.WriteOutput([]byte("top\r\n"))
	win.WriteOutput(sixelTestImage(10, 2))
	win.MarkContentDirty()
	frame := m.composeFrame()
	for _, bad := range []string{"·", "┌", vt.SixelMarkerLead} {
		if strings.Contains(frame, bad) {
			t.Fatalf("unknown image cells drew %q", bad)
		}
	}
}

// TestSixelOnKittyKeepsTheOldImageUntilTheNewOne: a program that draws a new
// sixel over the last one, as a browser does for every frame, must never leave
// the cells empty. On a kitty host the new image is compressed in the
// background, so the frame that first sees it cannot place it yet. That frame
// used to delete the old placement anyway, and the pane blinked between the
// picture and nothing.
func TestSixelOnKittyKeepsTheOldImageUntilTheNewOne(t *testing.T) {
	m, win := sixelOS(t, sixelViaKitty)
	var mu sync.Mutex
	var out string
	m.SixelPassthrough.direct = func(b []byte) { mu.Lock(); out += string(b); mu.Unlock() }
	frame := func() string {
		m.testFrame()
		b := m.GraphicsFrameBytes(nil)
		mu.Lock()
		defer mu.Unlock()
		out += string(b)
		return string(b)
	}
	waitFor := func(what string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			done := strings.Count(out, what)
			mu.Unlock()
			if done > 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("no %q reached the host", what)
	}

	win.WriteOutput(sixelTestImage(20, 3))
	win.MarkContentDirty()
	frame()
	waitFor("a=p,")
	mu.Lock()
	out = ""
	mu.Unlock()

	// The same cells, a new image.
	win.WriteOutput([]byte("\x1b[H"))
	win.WriteOutput(sixelTestImage(20, 3))
	win.MarkContentDirty()
	if got := frame(); strings.Contains(got, "a=d,") {
		t.Fatalf("the old image was deleted before its replacement was placed: %q", got)
	}
	waitFor("a=p,")
	frame()
	mu.Lock()
	defer mu.Unlock()
	place, del := strings.Index(out, "a=p,"), strings.Index(out, "a=d,d=i,")
	if del < 0 {
		t.Fatalf("the old placement was never deleted: %q", out[:min(300, len(out))])
	}
	if del < place {
		t.Fatalf("the delete went out before the new placement")
	}
}
