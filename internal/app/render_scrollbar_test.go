package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// fillScrollback pushes enough lines through the emulator that the pane has a
// scrollback deep enough for a thumb shorter than the viewport, which is the
// last of windowNeedsScrollbar's arithmetic gates.
func fillScrollback(t *testing.T, win *terminal.Window, lines int) {
	t.Helper()
	win.LockIO()
	for i := range lines {
		_, _ = win.Terminal.Write([]byte("scrollback line " + itoa(i) + "\r\n"))
	}
	win.UnlockIO()
	win.MarkContentDirty()
	if n := win.ScrollbackLenSync(); n <= 0 {
		t.Fatalf("pane has no scrollback after %d lines: the test cannot exercise the scrollbar", lines)
	}
}

// scrollBack puts the pane into the state the scrollbar exists to report: a
// view some way up its own history.
func scrollBack(t *testing.T, win *terminal.Window, offset int) {
	t.Helper()
	win.EnterCopyModeImplicit()
	if win.CopyMode == nil {
		t.Fatal("copy mode did not start; the pane cannot be scrolled back")
	}
	win.CopyMode.ScrollOffset = offset
	win.ScrollbackOffset = offset
}

// withSharedBorders sets config.SharedBorders for the duration of fn.
func withSharedBorders(t *testing.T, shared bool, fn func()) {
	t.Helper()
	prev := config.SharedBorders
	config.SharedBorders = shared
	defer func() { config.SharedBorders = prev }()
	fn()
}

// The thumb is a position readout, so it exists exactly while there is a
// position to read. A bar pinned to the bottom of every pane with history is
// permanent chrome that says nothing, and it was the only reason a lone pane at
// the live tail fell off the fullscreen fast path.
func TestScrollbarAppearsOnlyWhileScrolledBack(t *testing.T) {
	win := newTestWindow(t, "sbvis-0001", 60, 20)
	fillScrollback(t, win, 200)

	if windowNeedsScrollbar(win) {
		t.Error("thumb at the live tail: the pane has history but is not looking at it")
	}

	scrollBack(t, win, 50)
	if !windowNeedsScrollbar(win) {
		t.Fatal("no thumb while scrolled back: the pane gives no sign of where it is")
	}

	// Back to the tail, by the route the wheel and the drag both take.
	win.CopyMode.ScrollOffset = 0
	if windowNeedsScrollbar(win) {
		t.Error("thumb persists after returning to the live tail")
	}
}

// The column is the whole point of the redesign: one formula, every mode. A
// bordered pane puts the thumb one in from its right border; a borderless pane
// under shared borders puts it on its own rightmost cell, one in from the
// separator overlay that lives in the gap between rectangles. Neither ever
// paints a border cell, which is why the two now coexist.
func TestScrollbarSitsInTheLastContentColumn(t *testing.T) {
	cases := []struct {
		name        string
		tiled       bool
		shared      bool
		borderStyle string
	}{
		{name: "bordered pane"},
		{name: "borderless pane, shared borders", tiled: true, shared: true},
		{name: "borderless pane zoomed, shared borders", tiled: true, shared: true},
		{name: "borders hidden", borderStyle: "hidden"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			win := newTestWindow(t, "sbcol-"+strings.ReplaceAll(tc.name, " ", "-"), 60, 20)
			win.X, win.Y = 7, 3
			win.SetTiled(tc.tiled)
			win.Zoomed = strings.Contains(tc.name, "zoomed")
			fillScrollback(t, win, 200)
			scrollBack(t, win, 100)

			if tc.borderStyle != "" {
				prev := config.BorderStyle
				config.BorderStyle = tc.borderStyle
				t.Cleanup(func() { config.BorderStyle = prev })
			}

			withSharedBorders(t, tc.shared, func() {
				layer := renderScrollbarLayer(win, 1000, 1)
				if layer == nil {
					t.Fatal("no thumb layer for a scrolled-back pane")
				}
				wantX := win.X + win.Width - 1 - win.BorderOffset()
				if layer.GetX() != wantX {
					t.Errorf("thumb at column %d, want the last content column %d", layer.GetX(), wantX)
				}
				// Never the border cell, and never outside the rectangle.
				if layer.GetX() >= win.X+win.Width-win.BorderOffset() {
					t.Errorf("thumb at %d overlaps the pane's right border cell", layer.GetX())
				}
			})
		})
	}
}

// A pane mid-drag may straddle the sidebar band. The band composes above the
// pane's own layer, but the thumb is composed above the band, so it has to be
// clipped by hand or it pokes through.
func TestScrollbarIsClippedToTheContentRegion(t *testing.T) {
	win := newTestWindow(t, "sbclip-0001", 60, 20)
	win.X = 30
	fillScrollback(t, win, 200)
	scrollBack(t, win, 100)

	thumbX := win.X + win.Width - 1 - win.BorderOffset()
	if layer := renderScrollbarLayer(win, thumbX, 1); layer != nil {
		t.Errorf("thumb drawn at %d with the band starting at %d: it would land in the rail",
			layer.GetX(), thumbX)
	}
	if layer := renderScrollbarLayer(win, thumbX+1, 1); layer == nil {
		t.Error("thumb withheld when its column is the last one inside the content region")
	}
}

// windowNeedsScrollbar is consulted by four render paths (compositor cached,
// sync-hold, redraw, fullscreen fast path) to decide whether a thumb exists,
// while renderScrollbarLayer is what actually draws it. If the two disagree,
// the layout believes something the screen does not show.
func TestScrollbarLayerAgreesWithWindowNeedsScrollbar(t *testing.T) {
	type variant struct {
		name          string
		tiled, shared bool
		hide          bool
		borderStyle   string
		scrolled      int
		noScrollback  bool
	}
	variants := []variant{
		{name: "bordered pane scrolled", scrolled: 100},
		{name: "bordered pane at tail"},
		{name: "borderless pane scrolled", tiled: true, shared: true, scrolled: 100},
		{name: "borderless pane at tail", tiled: true, shared: true},
		{name: "borders hidden, scrolled", borderStyle: "hidden", scrolled: 100},
		{name: "scrollbar disabled", hide: true, scrolled: 100},
		{name: "no scrollback", noScrollback: true},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			win := newTestWindow(t, "sbagree-"+strings.ReplaceAll(v.name, " ", "-"), 60, 20)
			if !v.noScrollback {
				fillScrollback(t, win, 200)
			}
			win.SetTiled(v.tiled)
			if v.scrolled > 0 {
				scrollBack(t, win, v.scrolled)
			}

			prevHide, prevStyle := config.HideScrollbar, config.BorderStyle
			config.HideScrollbar = v.hide
			if v.borderStyle != "" {
				config.BorderStyle = v.borderStyle
			}
			t.Cleanup(func() {
				config.HideScrollbar, config.BorderStyle = prevHide, prevStyle
			})

			withSharedBorders(t, v.shared, func() {
				need := windowNeedsScrollbar(win)
				layer := renderScrollbarLayer(win, 1000, 1)
				if need != (layer != nil) {
					t.Fatalf("windowNeedsScrollbar = %v but renderScrollbarLayer returned %v: "+
						"the layout and the layer disagree about whether a thumb is present",
						need, layer != nil)
				}
			})
		})
	}
}

// The thumb reports the viewport's share of the buffer and its place in it:
// short for deep history, pinned to the bottom near the tail and to the top at
// the oldest line.
func TestScrollbarThumbSizeAndTravel(t *testing.T) {
	win := newTestWindow(t, "sbgeom-0001", 60, 20)
	win.Y = 4
	fillScrollback(t, win, 400)
	contentH := win.ContentHeight()
	sbLen := win.ScrollbackLenSync()

	thumbH := scrollbarThumbHeight(contentH, sbLen)
	if thumbH < 1 || thumbH >= contentH {
		t.Fatalf("thumb height %d out of range for a %d-row viewport", thumbH, contentH)
	}

	scrollBack(t, win, 1)
	nearTail := renderScrollbarLayer(win, 1000, 1)
	scrollBack(t, win, sbLen)
	atTop := renderScrollbarLayer(win, 1000, 1)
	if nearTail == nil || atTop == nil {
		t.Fatal("a scrolled-back pane produced no thumb")
	}

	wantBottom := win.Y + win.BorderOffset() + contentH - thumbH
	if nearTail.GetY() != wantBottom {
		t.Errorf("thumb one line back sits at y=%d, want the bottom of the track %d", nearTail.GetY(), wantBottom)
	}
	if want := win.Y + win.BorderOffset(); atTop.GetY() != want {
		t.Errorf("thumb at the oldest line sits at y=%d, want the top of the track %d", atTop.GetY(), want)
	}
}

// The grab column input uses must be the column the renderer paints, and it
// must be closed at the live tail: there the cell is ordinary content and a
// press on it belongs to the guest, not to a jump-scroll.
func TestScrollbarHitTracksTheDrawnColumn(t *testing.T) {
	for _, tiled := range []bool{false, true} {
		win := newTestWindow(t, "sbhit-"+itoa(boolToInt(tiled)), 60, 20)
		win.X = 5
		win.SetTiled(tiled)
		fillScrollback(t, win, 200)

		if _, drawn := ScrollbarHit(win); drawn {
			t.Errorf("tiled=%v: a grab is offered at the live tail, where no thumb is drawn", tiled)
		}

		scrollBack(t, win, 100)
		x, drawn := ScrollbarHit(win)
		if !drawn {
			t.Fatalf("tiled=%v: no grab offered while the thumb is on screen", tiled)
		}
		layer := renderScrollbarLayer(win, 1000, 1)
		if layer == nil || layer.GetX() != x {
			t.Errorf("tiled=%v: grab column %d does not match the drawn column", tiled, x)
		}
	}
	if _, drawn := ScrollbarHit(nil); drawn {
		t.Error("a nil window offered a scrollbar grab")
	}
}

// Every new glyph needs a fallback, and the thumb is a glyph.
func TestScrollbarThumbCharDegradesToASCII(t *testing.T) {
	prev := config.UseASCIIOnly
	config.UseASCIIOnly = true
	t.Cleanup(func() { config.UseASCIIOnly = prev })
	if got := config.GetScrollbarThumbChar(); got != "|" {
		t.Errorf("ASCII thumb char = %q, want %q", got, "|")
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
