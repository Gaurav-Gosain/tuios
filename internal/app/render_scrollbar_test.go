package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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

// scrollbarDefaults pins the globals the bar reads to a fresh install's for the
// duration of a test. They are process-wide, and the package's other tests move
// several of them (ASCII, border style) on their way past.
func scrollbarDefaults(t *testing.T) {
	t.Helper()
	ascii, border, hide := config.Global.UseASCIIOnly, config.Global.BorderStyle, config.Global.HideScrollbar
	style, thumb, track, tint := config.Global.ScrollbarStyle, config.Global.ScrollbarThumb, config.Global.ScrollbarTrack, config.Global.ScrollbarTint
	t.Cleanup(func() {
		config.Global.UseASCIIOnly, config.Global.BorderStyle, config.Global.HideScrollbar = ascii, border, hide
		config.Global.ScrollbarStyle, config.Global.ScrollbarThumb, config.Global.ScrollbarTrack, config.Global.ScrollbarTint = style, thumb, track, tint
	})
	config.Global.UseASCIIOnly, config.Global.BorderStyle, config.Global.HideScrollbar = false, "rounded", false
	config.Global.ScrollbarStyle, config.Global.ScrollbarThumb, config.Global.ScrollbarTrack = config.ScrollbarStyleThin, "", ""
	config.Global.ScrollbarTint = config.ScrollbarTintBorder
}

// withSharedBorders sets config.SharedBorders for the duration of fn.
func withSharedBorders(t *testing.T, shared bool, fn func()) {
	t.Helper()
	prev := config.Global.SharedBorders
	config.Global.SharedBorders = shared
	defer func() { config.Global.SharedBorders = prev }()
	fn()
}

// withScrollbarStyle sets the scrollbar style for the duration of fn, on the
// process seed and on any model already built from it.
func withScrollbarStyle(t *testing.T, m *OS, style string, fn func()) {
	t.Helper()
	prev := config.Global.ScrollbarStyle
	config.Global.ScrollbarStyle = style
	if m != nil {
		m.Settings.ScrollbarStyle = style
	}
	defer func() {
		config.Global.ScrollbarStyle = prev
		if m != nil {
			m.Settings.ScrollbarStyle = prev
		}
	}()
	fn()
}

// barFrame composes the bar the way the compositor does and returns the frame's
// rows. Every visual claim below is read off this rather than off the
// arithmetic that produced it: the bar is one column of glyphs on a frame, and
// that is where a bug in it shows.
func barFrame(t *testing.T, m *OS, win *terminal.Window, focused bool) []string {
	t.Helper()
	layer := m.renderScrollbarLayer(win, 1000, 1, focused)
	if layer == nil {
		t.Fatal("a scrolled-back pane produced no bar")
	}
	canvas := lipgloss.NewCanvas(win.X+win.Width+1, win.Y+win.Height+1)
	canvas.Compose(lipgloss.NewCompositor(layer))
	return strings.Split(canvas.Render(), "\n")
}

// barGlyphs returns what the frame shows in the bar's column, one entry per
// viewport row, styling stripped.
func barGlyphs(t *testing.T, frame []string, win *terminal.Window) []string {
	t.Helper()
	x := scrollbarColumn(win)
	top := win.Y + win.BorderOffset()
	glyphs := make([]string, win.ContentHeight())
	for i := range glyphs {
		row := []rune(ansi.Strip(frame[top+i]))
		glyphs[i] = " " // a row the bar left untouched shows the guest's own cell
		if x < len(row) {
			glyphs[i] = string(row[x])
		}
	}
	return glyphs
}

// A pane mid-drag may straddle the sidebar band. The band composes above the
// pane's own layer, but the bar is composed above the band, so it has to be
// clipped by hand or it pokes through.
func TestScrollbarIsClippedToTheContentRegion(t *testing.T) {
	scrollbarDefaults(t)
	win := newTestWindow(t, "sbclip-0001", 60, 20)
	win.X = 30
	fillScrollback(t, win, 200)
	scrollBack(t, win, 100)
	m := newTestOS(win)

	barX := win.X + win.Width - 1 - win.BorderOffset()
	if layer := m.renderScrollbarLayer(win, barX, 1, true); layer != nil {
		t.Errorf("bar drawn at %d with the band starting at %d: it would land in the rail",
			layer.GetX(), barX)
	}
	if layer := m.renderScrollbarLayer(win, barX+1, 1, true); layer == nil {
		t.Error("bar withheld when its column is the last one inside the content region")
	}
}

// windowNeedsScrollbar is consulted by four render paths (compositor cached,
// sync-hold, redraw, fullscreen fast path) to decide whether a bar exists,
// while renderScrollbarLayer is what actually draws it. If the two disagree,
// the layout believes something the screen does not show.
func TestScrollbarLayerAgreesWithWindowNeedsScrollbar(t *testing.T) {
	scrollbarDefaults(t)
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
			m := newTestOS(win)

			prevHide, prevStyle := m.Settings.HideScrollbar, m.Settings.BorderStyle
			m.Settings.HideScrollbar = v.hide
			if v.borderStyle != "" {
				m.Settings.BorderStyle = v.borderStyle
			}
			t.Cleanup(func() {
				m.Settings.HideScrollbar, m.Settings.BorderStyle = prevHide, prevStyle
			})

			withSharedBorders(t, v.shared, func() {
				need := windowNeedsScrollbar(win, &m.Settings)
				layer := m.renderScrollbarLayer(win, 1000, 1, true)
				if need != (layer != nil) {
					t.Fatalf("windowNeedsScrollbar = %v but renderScrollbarLayer returned %v: "+
						"the layout and the layer disagree about whether a bar is present",
						need, layer != nil)
				}
			})
			m.Settings = config.Global
		})
	}
}

// opentui's Slider rounds the thumb over the remaining travel; tuios truncated,
// which biases every position toward the live tail. A pane one line off its
// oldest is scrolled back as far as the eye can tell, and it drew the thumb a
// whole row shy of the top of the track. Both ends are asserted on the frame,
// because "the thumb is at the top" is a claim about pixels.
func TestScrollbarPinsToTheEndsOfItsTravel(t *testing.T) {
	scrollbarDefaults(t)
	for _, style := range []string{config.ScrollbarStyleThin, config.ScrollbarStyleTrack} {
		t.Run(style, func(t *testing.T) {
			win := newTestWindow(t, "sbpin-"+style, 60, 20)
			win.Y = 2
			fillScrollback(t, win, 400)
			m := newTestOS(win)
			sbLen := win.ScrollbackLenSync()
			contentH := win.ContentHeight()

			withScrollbarStyle(t, m, style, func() {
				for _, tc := range []struct {
					name   string
					offset int
				}{
					{"at the oldest line", sbLen},
					{"one line off the oldest", sbLen - 1},
				} {
					scrollBack(t, win, tc.offset)
					glyphs := barGlyphs(t, barFrame(t, m, win, true), win)
					if glyphs[0] == " " || glyphs[0] == m.Settings.GetScrollbarTrackChar() {
						t.Errorf("%s: the top track cell shows %q, so the thumb is short of the end of its travel",
							tc.name, glyphs[0])
					}
				}

				scrollBack(t, win, 1)
				glyphs := barGlyphs(t, barFrame(t, m, win, true), win)
				if last := glyphs[contentH-1]; last == " " || last == m.Settings.GetScrollbarTrackChar() {
					t.Errorf("one line back from the tail: the bottom track cell shows %q, so the thumb is short of the other end", last)
				}
			})
			m.Settings = config.Global
		})
	}
}

// The grab rect input reads is recorded by the renderer as it draws, so a press
// can only land on cells that took ink. At the live tail the column is ordinary
// content and the press belongs to the guest.
func TestScrollbarHitIsRecordedAsItIsDrawn(t *testing.T) {
	scrollbarDefaults(t)
	for _, tiled := range []bool{false, true} {
		win := newTestWindow(t, "sbhit-"+itoa(boolToInt(tiled)), 60, 20)
		win.X, win.Y = 5, 2
		win.SetTiled(tiled)
		fillScrollback(t, win, 200)
		m := newTestOS(win)

		m.resetScrollbarRects()
		if _, ok := m.ScrollbarHit(win); ok {
			t.Errorf("tiled=%v: a grab is offered before anything was drawn", tiled)
		}

		scrollBack(t, win, 100)
		layer := m.renderScrollbarLayer(win, 1000, 1, true)
		rect, ok := m.ScrollbarHit(win)
		if layer == nil || !ok {
			t.Fatalf("tiled=%v: the drawn bar recorded no grab", tiled)
		}
		if rect.X != layer.GetX() {
			t.Errorf("tiled=%v: grab column %d does not match the drawn column %d", tiled, rect.X, layer.GetX())
		}
		if rect.TrackY != layer.GetY() || rect.TrackH != lipgloss.Height(layer.GetContent()) {
			t.Errorf("tiled=%v: grab rows %d+%d do not match the drawn rows %d+%d",
				tiled, rect.TrackY, rect.TrackH, layer.GetY(), lipgloss.Height(layer.GetContent()))
		}
		if !rect.Contains(rect.X, rect.ThumbY) || rect.Contains(rect.X+1, rect.ThumbY) {
			t.Errorf("tiled=%v: the rect does not contain its own thumb, or spills into the next column", tiled)
		}
		if !rect.OnThumb(rect.ThumbY) || rect.OnThumb(rect.ThumbY+rect.ThumbH) {
			t.Errorf("tiled=%v: the thumb rows are misreported", tiled)
		}

		// The frame that stops drawing the bar takes the grab with it.
		m.resetScrollbarRects()
		if _, ok := m.ScrollbarHit(win); ok {
			t.Errorf("tiled=%v: last frame's grab survived into a frame without a bar", tiled)
		}
	}
	m := newTestOS(nil)
	if _, ok := m.ScrollbarHit(nil); ok {
		t.Error("a nil window offered a scrollbar grab")
	}
}

// The drag inverts the renderer's own arithmetic, so a thumb dropped on a row
// is drawn back on that row rather than a rounding error away from it.
func TestScrollbarDragRoundTripsThroughTheRenderer(t *testing.T) {
	scrollbarDefaults(t)
	for _, style := range []string{config.ScrollbarStyleThin, config.ScrollbarStyleTrack} {
		t.Run(style, func(t *testing.T) {
			win := newTestWindow(t, "sbdrag-"+style, 60, 20)
			win.Y = 3
			fillScrollback(t, win, 400)
			scrollBack(t, win, 200)

			withScrollbarStyle(t, nil, style, func() {
				top := win.Y + win.BorderOffset()
				_, thumbUnits, travel := scrollbarTravel(win.ContentHeight(), win.ScrollbackLenSync(), &config.Global)
				perRow, _, _ := scrollbarTravel(win.ContentHeight(), win.ScrollbackLenSync(), &config.Global)
				lastRow := top + (travel+thumbUnits)/perRow - (thumbUnits+perRow-1)/perRow
				for row := top; row <= lastRow; row++ {
					win.CopyMode.ScrollOffset = ScrollbarOffsetForThumbRow(win, row, &config.Global)
					if got := ScrollbarThumbRow(win, &config.Global); got != row {
						t.Errorf("thumb dropped on row %d is drawn on row %d", row, got)
					}
				}
			})
		})
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
