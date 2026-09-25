package app

import (
	"encoding/json"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The beam is a colour transform over a frame that already exists, so every
// property worth holding is a property of one pass over one canvas: what it
// leaves alone, what it must not leave alone, and what it must not allocate.

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
//
// Three screens take different paths through the pass: a themed one, a
// themeless one mixing colours the pass can scale with ones it cannot (the
// blend cache must still serve the first kind), and one where nothing
// resolves and every cell goes to SGR 2.
func TestSpotlightAllocatesNothing(t *testing.T) {
	// Boxed once, outside the measurement: an interface parameter taking a
	// color.RGBA value allocates at every call, which would be counted as the
	// pass's own.
	var fg color.Color = color.RGBA{R: 200, G: 200, B: 200, A: 0xFF}
	var bg color.Color = color.RGBA{R: 40, G: 40, B: 60, A: 0xFF}
	var cube color.Color = ansi.IndexedColor(214)
	var basic color.Color = ansi.BasicColor(2)
	inks := []color.Color{fg, cube, basic, nil}

	for _, tc := range []struct {
		name    string
		theme   string
		canvas  func(t testing.TB, w, h int) *lipgloss.Canvas
		restore func(canvas *lipgloss.Canvas)
	}{
		{"themed", "catppuccin_mocha", spotlightTestCanvas,
			func(c *lipgloss.Canvas) { spotlightRestore(c, fg, bg) }},
		{"mixed", "", spotlightMixedCanvas,
			func(c *lipgloss.Canvas) { spotlightRestoreMixed(c, inks) }},
		{"faint", "", spotlightTestCanvas,
			func(c *lipgloss.Canvas) { spotlightRestore(c, basic, nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withTheme(t, tc.theme)
			canvas := tc.canvas(t, realCols, realRows)
			tc.restore(canvas)
			s := newSpotlightTestState()

			// The restore is inside the measured function, so it has to be
			// free or the number below is not the pass's.
			if base := testing.AllocsPerRun(20, func() { tc.restore(canvas) }); base != 0 {
				t.Fatalf("restoring the canvas allocates %.0f times; the measurement below would not be the pass's", base)
			}

			// AllocsPerRun counts every allocation in the process, so a
			// goroutine an earlier test left winding down is charged to the
			// pass: a full package run in a loaded container measured 2 per
			// frame this way. The least of three measurements is taken. An
			// allocation the pass makes itself is in every one of them, so
			// this still fails for any pass that allocates.
			frame := func() {
				s.apply(canvas, realCols/2, realRows/2, 10, 60, true)
				tc.restore(canvas)
			}
			allocs := testing.AllocsPerRun(20, frame)
			for range 2 {
				allocs = min(allocs, testing.AllocsPerRun(20, frame))
			}
			if allocs != 0 {
				t.Errorf("the pass allocated %.0f times per frame; it must allocate none", allocs)
			}
		})
	}
}

// spotlightMixedCanvas is what a themeless screen looks like to the pass: some
// cells carrying a colour it can scale, some carrying one of the host's own
// sixteen, and some carrying none at all.
func spotlightMixedCanvas(t testing.TB, w, h int) *lipgloss.Canvas {
	t.Helper()
	canvas := lipgloss.NewCanvas(w, h)
	var rgba color.Color = color.RGBA{R: 200, G: 200, B: 200, A: 0xFF}
	var cube color.Color = ansi.IndexedColor(214)
	var basic color.Color = ansi.BasicColor(2)
	inks := []color.Color{rgba, cube, basic, nil}
	const word = "hello world "
	for y := range h {
		for x := range w {
			cell := uv.Cell{
				Content: string(word[x%len(word)]),
				Width:   1,
				Style:   uv.Style{Fg: inks[(x/8+y)%len(inks)]},
			}
			canvas.SetCell(x, y, &cell)
		}
	}
	return canvas
}

// spotlightRestoreMixed puts a mixed canvas back the way spotlightMixedCanvas
// left it, without allocating.
func spotlightRestoreMixed(canvas *lipgloss.Canvas, inks []color.Color) {
	for y := range canvas.Height() {
		for x := range canvas.Width() {
			cell := canvas.CellAt(x, y)
			cell.Style.Fg, cell.Style.Bg, cell.Style.Attrs = inks[(x/8+y)%len(inks)], nil, 0
		}
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

func cellStyleAt(canvas *lipgloss.Canvas, x, y int) uv.Style {
	return canvas.CellAt(x, y).Style
}

// TestSpotlightDoesNotSplitStyleRuns states the same property in the unit that
// matters, which is bytes on the wire. A pass that dimmed only the glyphs would
// double the escape sequences in the rendered frame.
func TestSpotlightDoesNotSplitStyleRuns(t *testing.T) {
	// Both screens: a themed one, where every cell is scaled, and a themeless
	// one, where the pass writes a colour to some cells and SGR 2 to others. A
	// mixed screen must not defeat the run cache: the cells that share a
	// source colour and a level still come out of it as one style.
	for _, themeID := range []string{"catppuccin_mocha", ""} {
		name := themeID
		if name == "" {
			name = "no theme"
		}
		t.Run(name, func(t *testing.T) {
			withTheme(t, themeID)
			canvas := spotlightMixedCanvas(t, 80, 24)
			plainSGR := strings.Count(canvas.Render(), "\x1b[")

			newSpotlightTestState().apply(canvas, 40, 12, 8, 60, true)
			dimmedSGR := strings.Count(canvas.Render(), "\x1b[")

			// A row that crosses the beam pays a handful of style changes for
			// the two rim crossings and nothing else, because the levels inside
			// a row change only where the rim passes. Splitting at every blank
			// instead costs one per word: on this fixture that is 712 sequences
			// against 208.
			if limit := plainSGR + 8*canvas.Height(); dimmedSGR > limit {
				t.Errorf("the pass produced %d escape sequences against %d before it (limit %d); "+
					"the style runs were split", dimmedSGR, plainSGR, limit)
			}
		})
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

// spotlightBrightness is how much light a colour carries, as the mean of its
// three channels over 255. It is not a luminance and does not need to be: the
// tests below compare one colour with a scaled copy of itself, so any monotonic
// reading of the channels answers them.
func spotlightBrightness(t *testing.T, c color.Color) float64 {
	t.Helper()
	if isNilColor(c) {
		t.Fatal("no colour to measure")
	}
	r, g, b, _ := c.RGBA()
	return float64(r>>8+g>>8+b>>8) / (3 * 255)
}

// TestSpotlightDimsALightThemeDownwards is the half a dark theme cannot show.
//
// Carrying a colour toward the theme's ground does not merely fail on a light
// theme, it runs backwards: the ground is bright there, so at dim 95 the text
// went from (76,79,105) to (230,232,238) and the screen washed out rather than
// going quiet. Both inks have to end up carrying less light than they started
// with, on every theme.
func TestSpotlightDimsALightThemeDownwards(t *testing.T) {
	withTheme(t, "catppuccin_latte")
	canvas := lipgloss.NewCanvas(80, 24)
	canvas.SetCell(2, 2, &uv.Cell{Content: "x", Width: 1})

	newSpotlightTestState().apply(canvas, 40, 12, 8, config.SpotlightDefaultDim, false)

	style := cellStyleAt(canvas, 2, 2)
	if got, want := spotlightBrightness(t, style.Fg), spotlightBrightness(t, theme.TerminalFg()); got >= want {
		t.Errorf("the foreground outside the beam went from %.2f to %.2f; the beam brightened it", want, got)
	}
	if got, want := spotlightBrightness(t, style.Bg), spotlightBrightness(t, theme.TerminalBg()); got >= want {
		t.Errorf("the background outside the beam went from %.2f to %.2f; the beam brightened it", want, got)
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

// TestSpotlightStandsAMouseBeamOnTheCursor. The beam follows the mouse by
// default, and a pointer that has not moved since the client started has never
// reported a position. The cursor stands in until it does.
//
// The stand-in has to be recomputed every frame, not latched. The first frame
// is composed before the client knows its size, so a latched one would put the
// beam in the top left corner and leave it there for the whole session on any
// client nobody touches with a mouse. A real frame is what found that.
//
// It is a stand-in and not a mix: the first pointer move latches the anchor and
// nothing reads the cursor after that.
func TestSpotlightStandsAMouseBeamOnTheCursor(t *testing.T) {
	win := newTestWindow(t, "spotlight-seed", 60, 12)
	m := newTestOS(win)
	m.Width, m.Height = 90, 30
	m.Mode = TerminalMode
	win.Tiled = true
	m.UserConfig = config.DefaultConfig()
	m.UserConfig.Spotlight.Follow = config.SpotlightFollowMouse

	cursor := m.getRealCursor()
	if cursor == nil {
		t.Fatal("the fixture has no cursor, so it cannot show what the seed is")
	}
	x, y := m.spotlightAnchor()
	if x != cursor.X || y != cursor.Y {
		t.Errorf("a mouse beam with no pointer position started at (%d,%d), want the cursor (%d,%d)",
			x, y, cursor.X, cursor.Y)
	}

	// The stand-in is recomputed rather than latched. A position nothing else
	// would produce is written over it, so a latched beam is caught by the
	// number rather than by the fact that it did not change.
	const staleX, staleY = 71, 23
	if staleX == cursor.X && staleY == cursor.Y {
		t.Fatal("the stale position is the cursor, so this cannot tell the two apart")
	}
	m.spotlight.x, m.spotlight.y = staleX, staleY
	if x, y = m.spotlightAnchor(); x != cursor.X || y != cursor.Y {
		t.Errorf("the beam held (%d,%d) rather than standing on the cursor (%d,%d); "+
			"the stand-in was latched on the first frame", x, y, cursor.X, cursor.Y)
	}

	// And the pointer takes it from there.
	m.LastMouseX, m.LastMouseY = 11, 4
	if x, y = m.spotlightAnchor(); x != 11 || y != 4 {
		t.Errorf("the beam sat at (%d,%d) after the pointer moved to (11,4)", x, y)
	}
	// The pointer owns it now, so the cursor is never read again.
	m.spotlight.x, m.spotlight.y = 11, 4
	if x, y = m.spotlightAnchor(); x != 11 || y != 4 {
		t.Errorf("the beam moved to (%d,%d) with the pointer still at (11,4)", x, y)
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

// TestSpotlightPendingMotionWakesTheTick is the other half of the throttle. The
// beam has no tick of its own, so the skipped position is flushed by the one
// term the maintenance tick carries for it, and that term must be false the
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
