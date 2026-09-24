package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// captureImageRig is one borderless pane filling an 80x24 screen with a kitty
// image at guest cell (2,2), 20 columns by 8 rows, freshly transmitted and
// waiting for the render loop to place it.
func captureImageRig(t *testing.T) (*OS, *KittyPassthrough, *recWriter, *PassthroughPlacement) {
	t.Helper()
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})
	rec := &recWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: rec})
	kp.screenWidth, kp.screenHeight = 80, 24

	win := &terminal.Window{
		ID: "test-window-id-abcdef12", X: 0, Y: 0, Width: 80, Height: 24,
		Workspace: 1, Tiled: true,
	}
	p := &PassthroughPlacement{
		GuestImageID: 1, HostImageID: 1, WindowID: win.ID,
		GuestX: 2, AbsoluteLine: 2, Cols: 20, Rows: 8, DisplayRows: 8,
		Hidden: true,
	}
	kp.placements[win.ID] = map[uint32]*PassthroughPlacement{1: p}

	m := &OS{
		Settings: config.Global, Width: 80, Height: 24, CurrentWorkspace: 1,
		KittyPassthrough: kp, Windows: []*terminal.Window{win}, FocusedWindow: 0,
	}
	return m, kp, rec, p
}

// shownArea is the number of cells the placement's slices cover.
func shownArea(p *PassthroughPlacement) int {
	if len(p.Slices) == 0 {
		return p.MaxShowableCols * p.MaxShowable
	}
	n := 0
	for _, s := range p.Slices {
		n += s.Cols * s.Rows
	}
	return n
}

// TestKittyImageStaysVisibleInCaptureMode is the launch clip's bug: a pane
// showing a kitty image lost the image the moment capture mode opened, because
// the graphics flush treated capture mode as an opaque overlay and hid every
// placement. Capture mode draws a hint strip and a marquee over the panes and
// leaves them showing, so the image has to stay, cropped around that chrome
// where the two meet, and be whole again once the mode closes.
func TestKittyImageStaysVisibleInCaptureMode(t *testing.T) {
	m, kp, rec, p := captureImageRig(t)
	const whole = 20 * 8

	m.flushGraphicsForView()
	if p.Hidden || shownArea(p) != whole {
		t.Fatalf("before capture mode: image not placed whole: %+v", p)
	}

	// Keyboard entry: the hover outline sits on the pane's own edges and the
	// hint strip on row 0, and neither touches the image.
	m.BeginCapture(false)
	placed := rec.count("a=p")
	deleted := rec.count("a=d")
	m.flushGraphicsForView()
	if p.Hidden {
		t.Fatal("entering capture mode hid the image in the pane being captured")
	}
	if got := rec.count("a=d"); got != deleted {
		t.Fatalf("entering capture mode deleted the placement: a=d count %d -> %d", deleted, got)
	}
	if shownArea(p) != whole {
		t.Fatalf("image under no chrome was cropped: showing %d cells of %d", shownArea(p), whole)
	}

	// A drag whose marquee crosses the image: the image stays, and no slice of
	// it paints over a cell the marquee draws.
	m.BeginCaptureDrag(5, 4)
	m.UpdateCapturePointer(12, 7, true)
	m.flushGraphicsForView()
	if p.Hidden {
		t.Fatal("a marquee over the image hid it")
	}
	if rec.count("a=p") == placed {
		t.Fatal("the image was not re-placed around the marquee")
	}
	chrome := m.captureOccluders()
	if len(chrome) == 0 {
		t.Fatal("capture mode reported no chrome while dragging")
	}
	for _, s := range p.Slices {
		sr := cellRect{s.HostX, s.HostY, s.Cols, s.Rows}
		for _, c := range chrome {
			if sr.overlaps(c) {
				t.Fatalf("slice %+v paints over capture chrome %+v", sr, c)
			}
		}
	}
	// The marquee is 8x4 from (5,4): its edges take 8+8+2+2 = 20 cells, all
	// inside the image, and every other cell of the image must still show.
	if got, want := shownArea(p), whole-20; got != want {
		t.Fatalf("image around the marquee shows %d cells, want %d (fell back to one rectangle?)", got, want)
	}

	// Leaving capture mode gives the image its full rectangle back.
	m.EndCapture()
	m.flushGraphicsForView()
	if p.Hidden || shownArea(p) != whole {
		t.Fatalf("after capture mode: image not whole again: hidden=%v shown=%d", p.Hidden, shownArea(p))
	}
	if len(kp.chromeOccluders) != 0 {
		t.Fatalf("chrome occluders outlived capture mode: %+v", kp.chromeOccluders)
	}
}

// TestKittyImageHiddenUnderScreenshotPreview keeps the other half of the rule:
// the preview panel that follows a capture is opaque, so an image under it
// still goes, and comes back when the panel closes.
func TestKittyImageHiddenUnderScreenshotPreview(t *testing.T) {
	m, _, _, p := captureImageRig(t)
	m.flushGraphicsForView()
	if p.Hidden {
		t.Fatal("image not placed")
	}
	m.ShotPreview.Open = true
	m.flushGraphicsForView()
	if !p.Hidden {
		t.Fatal("image stayed on screen under the opaque preview panel")
	}
	m.ShotPreview.Open = false
	m.flushGraphicsForView()
	if p.Hidden {
		t.Fatal("image not restored after the preview closed")
	}
}
