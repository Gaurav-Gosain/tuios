package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestLargestClearRectGeometry pins what one placement can show when something
// is drawn over it.
func TestLargestClearRectGeometry(t *testing.T) {
	img := cellRect{X: 10, Y: 10, W: 20, H: 10}

	for _, tc := range []struct {
		name    string
		blocker cellRect
		want    cellRect
		ok      bool
	}{
		{
			name:    "a window that misses the image leaves all of it",
			blocker: cellRect{X: 100, Y: 100, W: 5, H: 5},
			want:    img, ok: true,
		},
		{
			name:    "a window over the whole image leaves nothing",
			blocker: cellRect{X: 0, Y: 0, W: 100, H: 100},
			want:    cellRect{}, ok: false,
		},
		{
			// The drag that started this: a window coming in from the right.
			name:    "covering the right leaves the left",
			blocker: cellRect{X: 25, Y: 0, W: 50, H: 40},
			want:    cellRect{X: 10, Y: 10, W: 15, H: 10}, ok: true,
		},
		{
			name:    "covering the left leaves the right",
			blocker: cellRect{X: 0, Y: 0, W: 15, H: 40},
			want:    cellRect{X: 15, Y: 10, W: 15, H: 10}, ok: true,
		},
		{
			name:    "covering the top leaves the bottom",
			blocker: cellRect{X: 0, Y: 0, W: 100, H: 13},
			want:    cellRect{X: 10, Y: 13, W: 20, H: 7}, ok: true,
		},
		{
			name:    "covering the bottom leaves the top",
			blocker: cellRect{X: 0, Y: 16, W: 100, H: 40},
			want:    cellRect{X: 10, Y: 10, W: 20, H: 6}, ok: true,
		},
		{
			// An L is two rectangles and a placement can show one, so this is
			// the larger of them rather than the whole L. The blocker takes the
			// top-right corner, leaving a 15x10 strip on the left and a 20x5
			// strip along the bottom; the left one is bigger.
			name:    "a corner leaves the larger of the two strips",
			blocker: cellRect{X: 25, Y: 5, W: 50, H: 10},
			want:    cellRect{X: 10, Y: 10, W: 15, H: 10}, ok: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := largestClearRect(img, []cellRect{tc.blocker})
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got %+v)", ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("clear rect = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestLargestClearRectTakesEveryBlocker checks the result is clear of all of
// them, which is the property that matters. It is deliberately not a claim
// about finding the true optimum for several blockers.
func TestLargestClearRectTakesEveryBlocker(t *testing.T) {
	img := cellRect{X: 0, Y: 0, W: 20, H: 20}
	blockers := []cellRect{
		{X: 15, Y: 0, W: 10, H: 20}, // right strip
		{X: 0, Y: 15, W: 20, H: 10}, // bottom strip
	}
	got, ok := largestClearRect(img, blockers)
	if !ok {
		t.Fatal("two edge strips hid the whole image")
	}
	for _, b := range blockers {
		if got.overlaps(b) {
			t.Errorf("result %+v still overlaps blocker %+v", got, b)
		}
	}
}

// TestOccludersAboveIgnoresWhatCannotCover keeps an invisible window from
// taking an image away. A minimized or off-workspace pane keeps its geometry,
// and reading that as a cover is how an image disappears for no visible reason.
func TestOccludersAboveIgnoresWhatCannotCover(t *testing.T) {
	all := map[string]*WindowPositionInfo{
		"self":   {WindowX: 0, WindowY: 0, Width: 10, Height: 10, Visible: true, WindowZ: 5},
		"below":  {WindowX: 0, WindowY: 0, Width: 10, Height: 10, Visible: true, WindowZ: 1},
		"hidden": {WindowX: 0, WindowY: 0, Width: 10, Height: 10, Visible: false, WindowZ: 9},
		"above":  {WindowX: 3, WindowY: 3, Width: 4, Height: 4, Visible: true, WindowZ: 9},
		"sameZ":  {WindowX: 0, WindowY: 0, Width: 10, Height: 10, Visible: true, WindowZ: 5},
	}
	got := occludersAbove(5, all, "self")
	if len(got) != 1 {
		t.Fatalf("got %d occluders, want only the visible higher one: %+v", len(got), got)
	}
	if want := (cellRect{3, 3, 4, 4}); got[0] != want {
		t.Errorf("occluder = %+v, want %+v", got[0], want)
	}
}

// occlusionHarness places one image in one window and lets a test drop another
// window on top of it.
type occlusionHarness struct {
	kp    *KittyPassthrough
	host  *recWriter
	winID string
	infos map[string]*WindowPositionInfo
}

// newOcclusionHarness places a 20x10 cell image at the top left of a 100x30
// window on a 200x60 screen.
func newOcclusionHarness(t *testing.T) *occlusionHarness {
	t.Helper()
	withClientCaps(t, &HostCapabilities{
		KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20,
	})
	host := &recWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{ForceEnable: true, Output: host})
	kp.enabled = true

	const winID = "window-0000-0000-0000-000000000000"
	place := &vt.KittyCommand{
		Action: vt.KittyActionPlace, ImageID: 1,
		Columns: 20, Rows: 10, Width: 200, Height: 200,
	}
	kp.ForwardCommand(place, nil, winID, 0, 0, 98, 28, 1, 1, 0, 0, 0, false, nil)

	return &occlusionHarness{
		kp: kp, host: host, winID: winID,
		infos: map[string]*WindowPositionInfo{
			winID: {
				WindowX: 0, WindowY: 0, ContentOffsetX: 1, ContentOffsetY: 1,
				Width: 100, Height: 30, Visible: true, WindowZ: 1,
				ScreenWidth: 200, ScreenHeight: 60,
			},
		},
	}
}

// refresh runs one render pass and returns what went to the host.
func (h *occlusionHarness) refresh() string {
	h.kp.RefreshAllPlacements(func() map[string]*WindowPositionInfo { return h.infos })
	out := h.kp.FlushPending()
	if len(out) > 0 {
		h.kp.WriteToHost(out)
	}
	return string(out)
}

// TestAPartlyCoveredImageIsCroppedNotHidden is the behaviour this exists for. A
// window dragged over part of an image used to take the whole picture away,
// because the occlusion test was a yes-or-no overlap. Only the covered part
// should go.
//
// Negative control: putting the isOccludedByHigherWindow call back in place of
// the crop deleted the placement and this failed.
func TestAPartlyCoveredImageIsCroppedNotHidden(t *testing.T) {
	h := newOcclusionHarness(t)
	h.refresh() // settle: the image is placed at full size

	// A window covering the right half of the image, from column 12 rightwards.
	h.infos["over"] = &WindowPositionInfo{
		WindowX: 12, WindowY: 0, Width: 40, Height: 30,
		Visible: true, WindowZ: 9, ScreenWidth: 200, ScreenHeight: 60,
	}
	out := h.refresh()

	if !strings.Contains(out, "a=p,i=") {
		t.Fatalf("the image was not re-placed when a window covered part of it:\n%q", out)
	}
	if strings.Contains(out, "a=d,d=i") {
		t.Errorf("the image was deleted rather than cropped:\n%q", out)
	}
	// The visible strip runs from the image's left edge to column 12, which is
	// 11 cells wide once the window's 1-cell border is accounted for.
	if !strings.Contains(out, ",c=11") {
		t.Errorf("the placement was not narrowed to the clear strip:\n%q", out)
	}
	// A crop off one side needs a source rectangle, or the host scales the
	// whole bitmap into the narrower cell box instead of cropping it.
	if !strings.Contains(out, ",x=") || !strings.Contains(out, ",w=") {
		t.Errorf("no source rectangle, so the image is squeezed rather than cropped:\n%q", out)
	}
}

// TestAFullyCoveredImageIsStillHidden keeps the old behaviour where it was
// right. Nothing of the picture is clear, so nothing should be drawn over the
// window that covers it.
func TestAFullyCoveredImageIsStillHidden(t *testing.T) {
	h := newOcclusionHarness(t)
	h.refresh()

	h.infos["over"] = &WindowPositionInfo{
		WindowX: 0, WindowY: 0, Width: 200, Height: 60,
		Visible: true, WindowZ: 9, ScreenWidth: 200, ScreenHeight: 60,
	}
	out := h.refresh()

	if !strings.Contains(out, "a=d") {
		t.Errorf("a fully covered image was not hidden, so it draws over the window:\n%q", out)
	}
}

// TestAnUncoveredImageIsUnaffected is the control: with nothing on top the
// placement must be the full image, or the crop is firing when it should not.
func TestAnUncoveredImageIsUnaffected(t *testing.T) {
	h := newOcclusionHarness(t)
	out := h.refresh()

	if strings.Contains(out, "a=d,d=i") {
		t.Errorf("an image nothing covers was hidden:\n%q", out)
	}
	if !strings.Contains(out, ",c=20") {
		t.Errorf("an image nothing covers was cropped:\n%q", out)
	}
}

// TestSubtractRectIsExact pins the decomposition. This is what lets an image
// keep the whole of an L rather than the larger of its two strips, which is the
// visible defect: a window at the bottom right took the top right of the
// picture with it.
func TestSubtractRectIsExact(t *testing.T) {
	img := cellRect{X: 0, Y: 0, W: 10, H: 10}

	t.Run("a corner leaves two pieces covering everything else", func(t *testing.T) {
		// Covers the bottom right quadrant.
		got := subtractRect(img, cellRect{X: 5, Y: 5, W: 20, H: 20})
		if covered := totalArea(got); covered != 75 {
			t.Errorf("pieces cover %d cells, want 75 (100 less the 25 covered): %+v", covered, got)
		}
		assertDisjointAndClear(t, got, img, cellRect{X: 5, Y: 5, W: 20, H: 20})
	})

	t.Run("a window through the middle leaves a ring", func(t *testing.T) {
		got := subtractRect(img, cellRect{X: 3, Y: 3, W: 4, H: 4})
		if covered := totalArea(got); covered != 84 {
			t.Errorf("pieces cover %d cells, want 84: %+v", covered, got)
		}
		assertDisjointAndClear(t, got, img, cellRect{X: 3, Y: 3, W: 4, H: 4})
	})

	t.Run("no overlap leaves the image whole", func(t *testing.T) {
		got := subtractRect(img, cellRect{X: 50, Y: 50, W: 5, H: 5})
		if len(got) != 1 || got[0] != img {
			t.Errorf("got %+v, want the whole image", got)
		}
	})

	t.Run("full cover leaves nothing", func(t *testing.T) {
		if got := subtractRect(img, cellRect{X: -1, Y: -1, W: 30, H: 30}); len(got) != 0 {
			t.Errorf("got %+v, want nothing", got)
		}
	})
}

func totalArea(rs []cellRect) int {
	n := 0
	for _, r := range rs {
		n += r.area()
	}
	return n
}

// assertDisjointAndClear checks the pieces do not overlap each other, stay
// inside the image, and none of them touches the blocker.
func assertDisjointAndClear(t *testing.T, pieces []cellRect, img, blocker cellRect) {
	t.Helper()
	for i, a := range pieces {
		if a.empty() {
			t.Errorf("piece %d is empty", i)
		}
		if a.X < img.X || a.Y < img.Y || a.X+a.W > img.X+img.W || a.Y+a.H > img.Y+img.H {
			t.Errorf("piece %d %+v escapes the image %+v", i, a, img)
		}
		if a.overlaps(blocker) {
			t.Errorf("piece %d %+v overlaps the blocker %+v", i, a, blocker)
		}
		for j, b := range pieces {
			if i != j && a.overlaps(b) {
				t.Errorf("pieces %d %+v and %d %+v overlap, so the image is drawn twice there", i, a, j, b)
			}
		}
	}
}

// TestClearRegionCapsTheDecomposition keeps a drag from turning into an
// unbounded number of escape sequences per frame.
func TestClearRegionCapsTheDecomposition(t *testing.T) {
	img := cellRect{X: 0, Y: 0, W: 40, H: 40}
	var blockers []cellRect
	for i := range 6 {
		blockers = append(blockers, cellRect{X: i*6 + 2, Y: 2, W: 2, H: 36})
	}
	if _, ok := clearRegion(nil, img, blockers); ok {
		t.Error("a decomposition past the cap reported success instead of asking for the fallback")
	}
	// One blocker stays well inside the cap.
	if got, ok := clearRegion(nil, img, blockers[:1]); !ok || len(got) == 0 {
		t.Errorf("one blocker should decompose, got %+v ok=%v", got, ok)
	}
}

// TestACornerCoverKeepsBothStrips is the screenshot case. A window over the
// bottom right of an image used to take the whole top right with it, because
// one placement shows one rectangle and the larger strip won. The image is now
// drawn as both pieces of the L.
//
// Negative control: routing the refresh back through largestClearRect emitted a
// single a=p and this failed.
func TestACornerCoverKeepsBothStrips(t *testing.T) {
	h := newOcclusionHarness(t)
	h.refresh()

	// The image sits at (1,1) and is 20x10. Cover its bottom right corner only.
	h.infos["over"] = &WindowPositionInfo{
		WindowX: 12, WindowY: 6, Width: 40, Height: 30,
		Visible: true, WindowZ: 9, ScreenWidth: 200, ScreenHeight: 60,
	}
	out := h.refresh()

	if n := strings.Count(out, "a=p,i="); n < 2 {
		t.Errorf("the image was drawn as %d placement(s), want two for the L:\n%q", n, out)
	}
	if strings.Contains(out, "a=d,d=i,i=1,q=2") {
		t.Errorf("the image was hidden rather than cropped:\n%q", out)
	}
	// The two pieces must use different placement ids or the second replaces
	// the first.
	if !strings.Contains(out, "p=1") || !strings.Contains(out, "p=2") {
		t.Errorf("the two pieces did not get distinct placement ids:\n%q", out)
	}
}

// TestSlicesShrinkBackToOne checks the leftovers are cleaned up. A frame that
// needed two pieces and then needs one must delete the second, or a strip that
// is no longer clear stays on screen after the window moves off it.
//
// Negative control: dropping the stale-slice delete loop from placeSlices left
// the second placement on the host and this failed.
func TestSlicesShrinkBackToOne(t *testing.T) {
	h := newOcclusionHarness(t)
	h.refresh()

	h.infos["over"] = &WindowPositionInfo{
		WindowX: 12, WindowY: 6, Width: 40, Height: 30,
		Visible: true, WindowZ: 9, ScreenWidth: 200, ScreenHeight: 60,
	}
	if out := h.refresh(); strings.Count(out, "a=p,i=") < 2 {
		t.Fatalf("setup did not produce two pieces:\n%q", out)
	}

	// Move the window so it covers a full-height band instead: one piece.
	h.infos["over"].WindowY = 0
	out := h.refresh()

	if !strings.Contains(out, "a=d,d=i,i=1,p=2") {
		t.Errorf("the second piece was not deleted when it stopped being needed:\n%q", out)
	}
}
