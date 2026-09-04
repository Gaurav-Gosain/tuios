package app

import (
	"encoding/json"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The beam is a colour transform over a frame that already exists, so every
// property worth holding is a property of one pass over one canvas: what it
// leaves alone, what it must not leave alone, and what it must not allocate.

// withTrueColorFrames makes composeFrame emit colour for one test.
//
// lipgloss.Sprint downsamples every frame through lipgloss.Writer, whose
// profile is detected from this process's stdout. Under go test that is not a
// TTY, so a frame composed in a test is colourless and any assertion about what
// the pass did to a colour would pass with the pass removed. The SSH server and
// tuios-web both pin this global at startup for the same reason.
func withTrueColorFrames(t *testing.T) {
	t.Helper()
	prev := lipgloss.Writer.Profile
	lipgloss.Writer.Profile = colorprofile.TrueColor
	t.Cleanup(func() { lipgloss.Writer.Profile = prev })
}

// spotlightTestCanvas is a canvas of coloured text with coloured blanks in it,
// which is what a composed pane looks like to the pass.
func spotlightTestCanvas(t testing.TB, w, h int) *lipgloss.Canvas {
	t.Helper()
	canvas := lipgloss.NewCanvas(w, h)
	fg := color.RGBA{R: 200, G: 200, B: 200, A: 0xFF}
	bg := color.RGBA{R: 40, G: 40, B: 60, A: 0xFF}
	const word = "hello world "
	for y := range h {
		for x := range w {
			cell := uv.Cell{
				Content: string(word[x%len(word)]),
				Width:   1,
				Style:   uv.Style{Fg: fg, Bg: bg},
			}
			canvas.SetCell(x, y, &cell)
		}
	}
	return canvas
}

// spotlightTestState is a beam state with its ground already read, so a test
// can call apply without an OS around it.
func newSpotlightTestState() *spotlightState { return &spotlightState{} }

func cellStyleAt(canvas *lipgloss.Canvas, x, y int) uv.Style {
	return canvas.CellAt(x, y).Style
}

// TestSpotlightLeavesTheBeamUntouched is the property the whole feature rests
// on: what the compositor drew inside the light is what reaches the screen.
func TestSpotlightLeavesTheBeamUntouched(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	canvas := spotlightTestCanvas(t, 80, 24)
	before := cellStyleAt(canvas, 40, 12)

	newSpotlightTestState().apply(canvas, 40, 12, 8, 60, true)

	after := cellStyleAt(canvas, 40, 12)
	if !after.Equal(&before) {
		t.Errorf("the cell under the beam changed: fg %v -> %v, bg %v -> %v",
			before.Fg, after.Fg, before.Bg, after.Bg)
	}
}

// TestSpotlightDimsOutsideTheBeam is the other half. A cell far from the light
// must come back carrying a colour it did not have.
func TestSpotlightDimsOutsideTheBeam(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	canvas := spotlightTestCanvas(t, 80, 24)
	before := cellStyleAt(canvas, 2, 2)

	newSpotlightTestState().apply(canvas, 40, 12, 8, 60, true)

	after := cellStyleAt(canvas, 2, 2)
	if after.Equal(&before) {
		t.Fatalf("a cell far outside the beam was left alone: %v on %v", after.Fg, after.Bg)
	}
	// Dimmed, not erased: the text is still the user's and must still be there.
	if canvas.CellAt(2, 2).Content == "" {
		t.Error("the pass ate the cell's content")
	}
}

// TestSpotlightDimsAColouredBlank is the trap the design named. Skipping a
// blank cell "because nothing is visible there" reads as the obvious saving and
// is the opposite: it ends the style run at every space, so a line of text
// becomes one escape sequence per word and the frame quadruples.
func TestSpotlightDimsAColouredBlank(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	canvas := lipgloss.NewCanvas(40, 3)
	bg := color.RGBA{R: 40, G: 40, B: 60, A: 0xFF}
	fg := color.RGBA{R: 200, G: 200, B: 200, A: 0xFF}
	for x := range 40 {
		content := "x"
		if x%4 == 3 {
			content = " " // a blank that carries the same colours as its neighbours
		}
		cell := uv.Cell{Content: content, Width: 1, Style: uv.Style{Fg: fg, Bg: bg}}
		canvas.SetCell(x, 1, &cell)
	}

	newSpotlightTestState().apply(canvas, 0, 0, 3, 60, true)

	// Column 35 is one of the blanks, 34 its neighbour. Named as constants so
	// this cannot end up comparing a cell with itself.
	const blankCol, glyphCol = 35, 34
	if blankCol%4 != 3 || glyphCol%4 == 3 {
		t.Fatalf("the fixture moved: %d is meant to be a blank and %d a glyph", blankCol, glyphCol)
	}
	glyph := cellStyleAt(canvas, glyphCol, 1)
	blank := cellStyleAt(canvas, blankCol, 1)
	if !blank.Equal(&glyph) {
		t.Errorf("a coloured blank outside the beam was not dimmed like its neighbours: "+
			"blank %v on %v, glyph %v on %v", blank.Fg, blank.Bg, glyph.Fg, glyph.Bg)
	}
}

// TestSpotlightDoesNotSplitStyleRuns states the same property in the unit that
// matters, which is bytes on the wire. A pass that dimmed only the glyphs would
// double the escape sequences in the rendered frame.
func TestSpotlightDoesNotSplitStyleRuns(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	canvas := spotlightTestCanvas(t, 80, 24)
	plainSGR := strings.Count(canvas.Render(), "\x1b[")

	newSpotlightTestState().apply(canvas, 40, 12, 8, 60, true)
	dimmedSGR := strings.Count(canvas.Render(), "\x1b[")

	// A row that crosses the beam pays a handful of style changes for the two
	// rim crossings and nothing else, because the levels inside a row change
	// only where the rim passes. Splitting at every blank instead costs one per
	// word: on this fixture that is 712 sequences against 208.
	if limit := plainSGR + 8*canvas.Height(); dimmedSGR > limit {
		t.Errorf("the pass produced %d escape sequences against %d before it (limit %d); "+
			"the style runs were split", dimmedSGR, plainSGR, limit)
	}
}

// spotlightRestore puts every cell back to the style spotlightTestCanvas gave
// it, so the pass can be measured over the same input again.
//
// It writes through the pointers CellAt returns and allocates nothing, which
// the tests below check before they trust it.
func spotlightRestore(canvas *lipgloss.Canvas, fg, bg color.Color) {
	for y := range canvas.Height() {
		for x := range canvas.Width() {
			cell := canvas.CellAt(x, y)
			cell.Style.Fg, cell.Style.Bg, cell.Style.Attrs = fg, bg, 0
		}
	}
}

// TestSpotlightAllocatesNothing is the regression this feature would otherwise
// grow back. The naive spelling allocates about 16,000 times per frame, once
// per cell, and no test that only reads colours would ever notice.
//
// The canvas is restored between runs rather than dimmed again over its own
// output: the pass edits in place, so applying it twice carries each colour a
// step further and mints colours the cache has not seen. A real frame is
// composed fresh each time and carries the palette it carried last frame, which
// is what restoring reproduces.
func TestSpotlightAllocatesNothing(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	// Boxed once, outside the measurement: an interface parameter taking a
	// color.RGBA value allocates at every call, which would be counted as the
	// pass's own.
	var fg color.Color = color.RGBA{R: 200, G: 200, B: 200, A: 0xFF}
	var bg color.Color = color.RGBA{R: 40, G: 40, B: 60, A: 0xFF}
	canvas := spotlightTestCanvas(t, realCols, realRows)
	s := newSpotlightTestState()

	// The restore is inside the measured function, so it has to be free or the
	// number below is not the pass's.
	if base := testing.AllocsPerRun(20, func() { spotlightRestore(canvas, fg, bg) }); base != 0 {
		t.Fatalf("restoring the canvas allocates %.0f times; the measurement below would not be the pass's", base)
	}

	allocs := testing.AllocsPerRun(20, func() {
		s.apply(canvas, realCols/2, realRows/2, 10, 60, true)
		spotlightRestore(canvas, fg, bg)
	})
	if allocs != 0 {
		t.Errorf("the pass allocated %.0f times per frame; it must allocate none", allocs)
	}
}

// TestSpotlightFaintPathAllocatesNothing holds the same bar for the branch a
// client with no theme takes.
func TestSpotlightFaintPathAllocatesNothing(t *testing.T) {
	withTheme(t, "")
	// Boxed once, outside the measurement: an interface parameter taking a
	// color.RGBA value allocates at every call, which would be counted as the
	// pass's own.
	var fg color.Color = color.RGBA{R: 200, G: 200, B: 200, A: 0xFF}
	var bg color.Color = color.RGBA{R: 40, G: 40, B: 60, A: 0xFF}
	canvas := spotlightTestCanvas(t, realCols, realRows)
	s := newSpotlightTestState()

	allocs := testing.AllocsPerRun(20, func() {
		s.apply(canvas, realCols/2, realRows/2, 10, 60, true)
		spotlightRestore(canvas, fg, bg)
	})
	if allocs != 0 {
		t.Errorf("the faint pass allocated %.0f times per frame", allocs)
	}
}

// TestSpotlightWithoutAThemeGoesFaint is the degrade rule. Untheme there is no
// RGB to carry anything toward - the case dimGround already returns nil for -
// so the honest answer is SGR 2 and a hard edge.
func TestSpotlightWithoutAThemeGoesFaint(t *testing.T) {
	withTheme(t, "")
	canvas := spotlightTestCanvas(t, 80, 24)
	inBefore := cellStyleAt(canvas, 40, 12)
	outBefore := cellStyleAt(canvas, 2, 2)

	newSpotlightTestState().apply(canvas, 40, 12, 8, 60, true)

	in, out := cellStyleAt(canvas, 40, 12), cellStyleAt(canvas, 2, 2)
	if out.Attrs&uv.AttrFaint == 0 {
		t.Error("a cell outside the beam did not get AttrFaint with no theme set")
	}
	if in.Attrs&uv.AttrFaint != 0 {
		t.Error("a cell inside the beam was made faint")
	}
	// And no colour was invented for either.
	if out.Fg != outBefore.Fg || out.Bg != outBefore.Bg {
		t.Errorf("the faint path repainted a colour: %v on %v", out.Fg, out.Bg)
	}
	if !in.Equal(&inBefore) {
		t.Error("the faint path touched a cell inside the beam")
	}
}

// TestSpotlightLeavesWideGlyphPlaceholdersAlone. A cell of width two is stored
// with a zero cell after it, which the renderer skips. Writing a style to that
// placeholder makes it render as a cell of its own, which puts a phantom column
// after every wide character on the screen.
func TestSpotlightLeavesWideGlyphPlaceholdersAlone(t *testing.T) {
	// Both branches, because only one of them can get this wrong. The blend
	// path skips a placeholder anyway, since a zero cell carries no colour to
	// carry anywhere; the faint path writes an attribute without asking about
	// colour, so the guard is what stops it there.
	for _, themeID := range []string{"catppuccin_mocha", ""} {
		name := "themed"
		if themeID == "" {
			name = "faint"
		}
		t.Run(name, func(t *testing.T) {
			withTheme(t, themeID)
			canvas := lipgloss.NewCanvas(20, 3)
			wide := uv.Cell{
				Content: "世",
				Width:   2,
				Style:   uv.Style{Fg: color.RGBA{R: 200, G: 200, B: 200, A: 0xFF}},
			}
			canvas.SetCell(10, 1, &wide)

			newSpotlightTestState().apply(canvas, 0, 0, 2, 60, true)

			if placeholder := canvas.CellAt(11, 1); !placeholder.IsZero() {
				t.Errorf("the placeholder after a wide glyph was written to: %+v", *placeholder)
			}
		})
	}
}

// TestSpotlightDimsTextTheGuestLeftAtTheDefault is the case most of a real
// screen is in, and the one a fixture full of explicit SGR hides. tuios emits
// no colour for text the guest never coloured - a shell prompt, ls output,
// almost everything - so a pass that only touched cells carrying a colour of
// their own dimmed the syntax highlighting and left the rest at full
// brightness. An e2e reading a real screen is what found it.
func TestSpotlightDimsTextTheGuestLeftAtTheDefault(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	canvas := lipgloss.NewCanvas(20, 3)
	plain := uv.Cell{Content: "x", Width: 1}
	canvas.SetCell(15, 1, &plain)

	newSpotlightTestState().apply(canvas, 0, 0, 2, 60, true)

	style := cellStyleAt(canvas, 15, 1)
	if isNilColor(style.Fg) {
		t.Error("a cell at the terminal default was left undimmed outside the beam")
	}
	// The background stays absent: the cell already shows the ground it would
	// be carried toward, and the whole unlit region then shares one style
	// instead of carrying a background per cell.
	if !isNilColor(style.Bg) {
		t.Errorf("the pass painted a background on a cell that had none: %v", style.Bg)
	}
}

// TestSpotlightLeavesDefaultTextInsideTheBeamAlone is the other side of it.
func TestSpotlightLeavesDefaultTextInsideTheBeamAlone(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	canvas := lipgloss.NewCanvas(20, 3)
	plain := uv.Cell{Content: "x", Width: 1}
	canvas.SetCell(15, 1, &plain)

	newSpotlightTestState().apply(canvas, 15, 1, 6, 60, true)

	if style := cellStyleAt(canvas, 15, 1); !style.IsZero() {
		t.Errorf("a cell under the beam was given a colour: %v on %v", style.Fg, style.Bg)
	}
}

// TestSpotlightRadiusIsACircleOnScreen. A terminal cell is about twice as tall
// as it is wide, so a beam whose radius is counted in rows has to reach twice
// as far sideways or it draws as a tall oval.
func TestSpotlightRadiusIsACircleOnScreen(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	canvas := spotlightTestCanvas(t, 80, 40)
	before := cellStyleAt(canvas, 40, 20)

	newSpotlightTestState().apply(canvas, 40, 20, 10, 60, false)

	// Ten rows up is inside, and so is nineteen columns across.
	for _, p := range []struct{ x, y int }{{40, 11}, {58, 20}} {
		if got := cellStyleAt(canvas, p.x, p.y); !got.Equal(&before) {
			t.Errorf("(%d,%d) is inside the beam and was dimmed", p.x, p.y)
		}
	}
	// Eleven rows up is outside, and so is twenty-one columns across.
	for _, p := range []struct{ x, y int }{{40, 9}, {62, 20}} {
		if got := cellStyleAt(canvas, p.x, p.y); got.Equal(&before) {
			t.Errorf("(%d,%d) is outside the beam and was not dimmed", p.x, p.y)
		}
	}
}

// TestSpotlightHardEdgeHasNoRim. The hard edge is the cheap one on the wire:
// every cell outside the radius is at the same level, so a moving beam repaints
// two arcs rather than a gradient.
func TestSpotlightHardEdgeHasNoRim(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	canvas := spotlightTestCanvas(t, 80, 40)
	// Row 28 is eight rows from the middle of a ten-row beam: inside the
	// radius, and inside the band a soft edge would have started fading at.
	pristine := cellStyleAt(canvas, 40, 28)

	newSpotlightTestState().apply(canvas, 40, 20, 10, 60, false)

	if rim := cellStyleAt(canvas, 40, 28); !rim.Equal(&pristine) {
		t.Errorf("the hard edge faded a cell inside the radius: %v, was %v", rim.Fg, pristine.Fg)
	}
	// And everything past the radius is at one level, not a gradient.
	near, far := cellStyleAt(canvas, 40, 31), cellStyleAt(canvas, 40, 39)
	if !near.Equal(&far) {
		t.Errorf("the hard edge has a gradient: %v just outside, %v far outside", near.Fg, far.Fg)
	}
}

// TestSpotlightSoftEdgeHasARim is the negative of the row above.
func TestSpotlightSoftEdgeHasARim(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	canvas := spotlightTestCanvas(t, 80, 40)
	pristine := cellStyleAt(canvas, 40, 28)

	newSpotlightTestState().apply(canvas, 40, 20, 10, 60, true)

	// The same cell the hard edge leaves alone is part way down the rim here:
	// dimmer than the middle, brighter than the outside.
	rim := cellStyleAt(canvas, 40, 28)
	if rim.Equal(&pristine) {
		t.Errorf("the soft edge did not fade a cell inside the radius: %v", rim.Fg)
	}
	if far := cellStyleAt(canvas, 40, 39); rim.Equal(&far) {
		t.Errorf("the soft edge has no rim: %v on the rim, %v far outside", rim.Fg, far.Fg)
	}
}

// TestSpotlightDisqualifiesTheFullscreenFastPath. A lone fullscreen pane skips
// the compositor, and there is then no canvas for the pass to run over. The
// keycast overlay disqualifies the fast path for the same reason.
func TestSpotlightDisqualifiesTheFullscreenFastPath(t *testing.T) {
	win := newTestWindow(t, "spotlight-fast", 80, 24)
	m := newTestOS(win)
	m.Width, m.Height = 80, 26
	win.X, win.Y = 0, m.GetTopMargin()
	win.Width, win.Height = m.GetRenderWidth(), m.GetUsableHeight()

	if _, ok := m.fullscreenFastWindow(); !ok {
		t.Fatal("the geometry is not on the fast path, so this proves nothing")
	}
	m.spotlight.on = true
	if _, ok := m.fullscreenFastWindow(); ok {
		t.Error("the fast path stayed eligible with the spotlight on, so the beam would not be drawn")
	}
}

// TestSpotlightYieldsToTheScreensaver. The saver owns the whole screen while it
// runs, so the two must never draw in one frame.
func TestSpotlightYieldsToTheScreensaver(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	withTrueColorFrames(t)
	win := newTestWindow(t, "spotlight-saver", 60, 12)
	win.WriteOutput([]byte("\x1b[38;2;200;200;200;48;2;40;40;60mhello world\x1b[0m\r\n"))
	win.MarkContentDirty()
	m := newTestOS(win)
	m.Width, m.Height = 90, 30
	m.spotlight.on = true

	m.screensaver.active = true
	quiet := m.composeFrame()
	m.MarkAllDirty()
	m.screensaver.active = false
	m.spotlight = spotlightState{on: true}
	lit := m.composeFrame()

	if quiet == lit {
		t.Error("the frame was the same with the saver running and with it off; the pass did not yield")
	}
}

// TestSpotlightAnchorHoldsWhenTheCursorGoesAway. An overlay hides the pane's
// cursor, and the beam must stay where it was rather than snapping to the
// middle of the screen every time a picker opens.
func TestSpotlightAnchorHoldsWhenTheCursorGoesAway(t *testing.T) {
	win := newTestWindow(t, "spotlight-anchor", 60, 12)
	m := newTestOS(win)
	m.Width, m.Height = 90, 30
	m.Mode = TerminalMode
	win.Tiled = true

	// A position nothing else would produce, so a beam that jumped to the
	// middle of the screen or back to the pane's cursor is caught by the
	// numbers rather than by the fact that they did not change.
	const wantX, wantY = 7, 3
	m.spotlight.x, m.spotlight.y, m.spotlight.anchored = wantX, wantY, true

	m.ShowSettings = true // an overlay hides the pane's cursor
	x, y := m.spotlightAnchor()
	if x != wantX || y != wantY {
		t.Errorf("the beam moved to (%d,%d) when an overlay hid the cursor; it was at (%d,%d)",
			x, y, wantX, wantY)
	}
}

// TestSpotlightAnchorStartsInTheMiddle. A beam that has never had an answer -
// turned on in window mode, where no pane owns a cursor - has to draw
// somewhere, or the toggle reads as a toggle that did nothing.
func TestSpotlightAnchorStartsInTheMiddle(t *testing.T) {
	win := newTestWindow(t, "spotlight-first", 60, 12)
	m := newTestOS(win)
	m.Width, m.Height = 90, 30
	m.ShowSettings = true

	x, y := m.spotlightAnchor()
	if x != m.GetRenderWidth()/2 || y != m.GetRenderHeight()/2 {
		t.Errorf("the first beam with no anchor sat at (%d,%d), want the middle (%d,%d)",
			x, y, m.GetRenderWidth()/2, m.GetRenderHeight()/2)
	}
}

// TestSpotlightIsNotSessionState. The beam is what this client's screen looks
// like, not what the workspace holds, so nothing about it may reach the state a
// peer reads.
func TestSpotlightIsNotSessionState(t *testing.T) {
	win := newTestWindow(t, "spotlight-local", 40, 8)
	m := newTestOS(win)
	m.Width, m.Height = 60, 20

	off, err := json.Marshal(m.BuildSessionState())
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	m.spotlight.on = true
	on, err := json.Marshal(m.BuildSessionState())
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if string(off) != string(on) {
		t.Error("turning the spotlight on changed the session state a peer reads")
	}
}

// mouseBeamOS is a client with the beam on and following the pointer.
func mouseBeamOS(t *testing.T) *OS {
	t.Helper()
	win := newTestWindow(t, "spotlight-motion", 40, 10)
	m := newTestOS(win)
	m.Width, m.Height = 90, 30
	m.UserConfig = config.DefaultConfig()
	m.UserConfig.Spotlight.Follow = config.SpotlightFollowMouse
	m.Settings.NormalFPS = config.DefaultSettings().NormalFPS
	m.spotlight.on = true
	// The throttle sits after the input handler, so a model with none never
	// reaches it and the test would prove nothing.
	withSpyInputHandler(t)
	return m
}

// TestSpotlightMouseMotionIsThrottled. A pointer emits one motion event per
// cell it crosses, and with the beam anchored to it every one of those events
// makes the frame differ from the last, so every one of them turns into a
// compose and a flush. The drag throttle is what caps that at the frame rate.
func TestSpotlightMouseMotionIsThrottled(t *testing.T) {
	m := mouseBeamOS(t)

	next, _ := m.Update(tea.MouseMotionMsg{X: 10, Y: 5})
	m = next.(*OS)
	if m.renderSkipped {
		t.Fatal("the first move was skipped; there was no budget to spend yet")
	}
	next, _ = m.Update(tea.MouseMotionMsg{X: 11, Y: 5})
	m = next.(*OS)
	if !m.renderSkipped {
		t.Error("a second move inside one frame budget was drawn; the throttle is not on")
	}
	if !m.spotlightMotionPending {
		t.Error("the skipped move was not recorded, so nothing will flush it")
	}
}

// TestSpotlightMotionThrottleIsOffWithTheBeamOff. The throttle exists for the
// beam, and a client that is not drawing one must keep the hover behaviour it
// had.
func TestSpotlightMotionThrottleIsOffWithTheBeamOff(t *testing.T) {
	m := mouseBeamOS(t)
	m.spotlight.on = false

	for range 3 {
		next, _ := m.Update(tea.MouseMotionMsg{X: 10, Y: 5})
		m = next.(*OS)
		if m.renderSkipped {
			t.Fatal("a motion event was skipped with the spotlight off")
		}
	}
}

// TestSpotlightPendingMotionWakesTheTick is the other half of the throttle. The
// beam has no tick of its own, so the skipped position is flushed by the one
// term the maintenance tick carries for it - and that term must be false the
// rest of the time, or every idle client pays a wake-up for a setting it is not
// using. BenchmarkIdleTick is what that costs.
func TestSpotlightPendingMotionWakesTheTick(t *testing.T) {
	m := mouseBeamOS(t)
	if m.tickNeedsWork() {
		t.Fatal("an idle client with the beam on already wants work; the idle diet is broken")
	}
	m.spotlightMotionPending = true
	if !m.tickNeedsWork() {
		t.Error("a skipped beam move does not wake the tick, so it would never be drawn")
	}
}

// TestSpotlightOffLeavesTheIdleTickAlone states the same property as the
// benchmark does, in a form that names the number.
func TestSpotlightOffLeavesTheIdleTickAlone(t *testing.T) {
	m := idleOS(t, 3)
	for range 5 {
		m.Update(TickerMsg(time.Now()))
	}
	_, work0, render0 := m.TickStats()

	m.spotlight.on = true
	for range 50 {
		m.Update(TickerMsg(time.Now()))
	}

	_, work, render := m.TickStats()
	if work != work0 || render != render0 {
		t.Errorf("turning the beam on cost %d work and %d renders over 50 idle ticks; both must be 0",
			work-work0, render-render0)
	}
}

// BenchmarkSpotlightApply measures the pass on its own, over the canvas a
// nine-pane 207x55 frame composes to.
func BenchmarkSpotlightApply(b *testing.B) {
	prev := theme.CurrentThemeID()
	_ = theme.Initialize("catppuccin_mocha")
	b.Cleanup(func() { _ = theme.Initialize(prev) })

	canvas := spotlightTestCanvas(b, realCols, realRows)
	s := newSpotlightTestState()
	cx, cy := realCols/2, realRows/2
	// The pass edits in place, so applying it to its own output carries each
	// colour a step further and keeps minting colours the cache has not seen.
	// A real frame is composed fresh each time and carries the palette it
	// carried last frame, which is what running to convergence reproduces.
	// BenchmarkSpotlightCanvas measures the same thing without the trick, by
	// composing the frame each iteration; this one is the pass on its own.
	for range 64 {
		s.apply(canvas, cx, cy, 10, 60, true)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.apply(canvas, cx, cy, 10, 60, true)
	}
}

// BenchmarkSpotlightCanvas is the pair to read the pass's cost off: the
// existing one-dirty compositor benchmark, run with and without the pass in the
// same invocation so the difference is taken under one load. It mirrors
// BenchmarkCompositorGetCanvas/windows-9/one-dirty exactly, plus the pass.
func BenchmarkSpotlightCanvas(b *testing.B) {
	prev := theme.CurrentThemeID()
	_ = theme.Initialize("catppuccin_mocha")
	b.Cleanup(func() { _ = theme.Initialize(prev) })

	for _, on := range []bool{false, true} {
		name := "one-dirty/beam-off"
		if on {
			name = "one-dirty/beam-on"
		}
		b.Run(name, func(b *testing.B) {
			m := benchOS(b, 9)
			m.UserConfig = config.DefaultConfig()
			m.spotlight.on = on
			for _, w := range m.Windows {
				w.MarkContentDirty()
			}
			canvas := m.GetCanvas(false)
			if on {
				m.applySpotlight(canvas) // warm the blend cache
			}

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				m.Windows[0].MarkContentDirty()
				canvas := m.GetCanvas(false)
				if on {
					m.applySpotlight(canvas)
				}
			}
		})
	}
}

// BenchmarkSpotlightFrame measures a whole nine-pane frame with and without the
// pass, which is the number a user pays.
func BenchmarkSpotlightFrame(b *testing.B) {
	prev := theme.CurrentThemeID()
	_ = theme.Initialize("catppuccin_mocha")
	b.Cleanup(func() { _ = theme.Initialize(prev) })

	for _, on := range []bool{false, true} {
		name := "off"
		if on {
			name = "on"
		}
		b.Run(name, func(b *testing.B) {
			m := benchOS(b, 9)
			m.UserConfig = config.DefaultConfig()
			m.spotlight.on = on
			m.composeFrame()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				m.Windows[0].MarkContentDirty()
				_ = m.composeFrame()
			}
		})
	}
}
