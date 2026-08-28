package app

import (
	"image/color"
	"math"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
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

// withScrollbarTint sets config.ScrollbarTint for the duration of fn.
func withScrollbarTint(t *testing.T, m *OS, tint string, fn func()) {
	t.Helper()
	prev := config.Global.ScrollbarTint
	config.Global.ScrollbarTint = tint
	if m != nil {
		m.Settings.ScrollbarTint = tint
	}
	defer func() {
		config.Global.ScrollbarTint = prev
		if m != nil {
			m.Settings.ScrollbarTint = prev
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

// sgrOf returns the SGR parameters lipgloss writes for a colour, foreground or
// background. A composed frame folds several attributes into one escape, so the
// parameters are what a cell can be searched for, not the whole sequence.
func sgrOf(c color.Color, background bool) string {
	style := lipgloss.NewStyle().Foreground(c)
	if background {
		style = lipgloss.NewStyle().Background(c)
	}
	rendered := style.Render("X")
	return strings.TrimSuffix(strings.TrimPrefix(rendered[:strings.Index(rendered, "X")], "\x1b["), "m")
}

// drawnIn reports whether a rendered cell carries a colour, matching only on a
// parameter boundary so one colour cannot be read as the prefix of another.
func drawnIn(cell string, c color.Color, background bool) bool {
	sgr := sgrOf(c, background)
	return strings.Contains(cell, sgr+"m") || strings.Contains(cell, sgr+";")
}

// barInk reports whether the frame draws the thumb in a colour, on the row the
// renderer recorded it on.
func barInk(t *testing.T, m *OS, win *terminal.Window, frame []string, c color.Color) bool {
	t.Helper()
	rect, ok := m.ScrollbarHit(win)
	if !ok {
		t.Fatal("the drawn bar recorded no rect to read the thumb's row from")
	}
	return drawnIn(frame[rect.ThumbY], c, false)
}

// The thumb is a position readout, so it exists exactly while there is a
// position to read. A bar pinned to the bottom of every pane with history is
// permanent chrome that says nothing, and it was the only reason a lone pane at
// the live tail fell off the fullscreen fast path.
func TestScrollbarAppearsOnlyWhileScrolledBack(t *testing.T) {
	scrollbarDefaults(t)
	win := newTestWindow(t, "sbvis-0001", 60, 20)
	fillScrollback(t, win, 200)

	if windowNeedsScrollbar(win, &config.Global) {
		t.Error("thumb at the live tail: the pane has history but is not looking at it")
	}

	scrollBack(t, win, 50)
	if !windowNeedsScrollbar(win, &config.Global) {
		t.Fatal("no thumb while scrolled back: the pane gives no sign of where it is")
	}

	// Back to the tail, by the route the wheel and the drag both take.
	win.CopyMode.ScrollOffset = 0
	if windowNeedsScrollbar(win, &config.Global) {
		t.Error("thumb persists after returning to the live tail")
	}
}

// The column is the whole point of the redesign: one formula, every mode. A
// bordered pane puts the bar one in from its right border; a borderless pane
// under shared borders puts it on its own rightmost cell, one in from the
// separator overlay that lives in the gap between rectangles. Neither ever
// paints a border cell, which is why the two now coexist.
func TestScrollbarSitsInTheLastContentColumn(t *testing.T) {
	scrollbarDefaults(t)
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
			m := newTestOS(win)

			if tc.borderStyle != "" {
				prev := m.Settings.BorderStyle
				m.Settings.BorderStyle = tc.borderStyle
				t.Cleanup(func() { m.Settings.BorderStyle = prev })
			}

			withSharedBorders(t, tc.shared, func() {
				layer := m.renderScrollbarLayer(win, 1000, 1, true)
				if layer == nil {
					t.Fatal("no bar layer for a scrolled-back pane")
				}
				wantX := win.X + win.Width - 1 - win.BorderOffset()
				if layer.GetX() != wantX {
					t.Errorf("bar at column %d, want the last content column %d", layer.GetX(), wantX)
				}
				// Never the border cell, and never outside the rectangle.
				if layer.GetX() >= win.X+win.Width-win.BorderOffset() {
					t.Errorf("bar at %d overlaps the pane's right border cell", layer.GetX())
				}
			})
			m.Settings = config.Global
		})
	}
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

// The thumb reports the viewport's share of the buffer and its place in it:
// short for deep history, at the bottom of the track near the tail and at the
// top at the oldest line.
func TestScrollbarThumbSizeAndTravel(t *testing.T) {
	scrollbarDefaults(t)
	win := newTestWindow(t, "sbgeom-0001", 60, 20)
	win.Y = 4
	fillScrollback(t, win, 400)
	m := newTestOS(win)
	contentH := win.ContentHeight()
	sbLen := win.ScrollbackLenSync()

	thumbH := scrollbarThumbHeight(contentH, sbLen)
	if thumbH < 1 || thumbH >= contentH {
		t.Fatalf("thumb height %d out of range for a %d-row viewport", thumbH, contentH)
	}

	scrollBack(t, win, 1)
	nearTail := barGlyphs(t, barFrame(t, m, win, true), win)
	scrollBack(t, win, sbLen)
	atTop := barGlyphs(t, barFrame(t, m, win, true), win)

	thumb := m.Settings.GetScrollbarThumbChar()
	if nearTail[contentH-1] != thumb {
		t.Errorf("one line back from the tail the last row shows %q, want the thumb at the bottom of the track",
			nearTail[contentH-1])
	}
	if nearTail[0] == thumb {
		t.Error("one line back from the tail the thumb reaches the top of the track")
	}
	if atTop[0] != thumb {
		t.Errorf("at the oldest line the first row shows %q, want the thumb at the top of the track", atTop[0])
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

// The thin style's look: one box-drawing vertical at two weights, heavy where
// the viewport is and light everywhere else.
func TestScrollbarThinStyleIsAHeavyStrokeOnAHairline(t *testing.T) {
	scrollbarDefaults(t)
	win := newTestWindow(t, "sbthin-0001", 60, 20)
	win.X, win.Y = 3, 1
	fillScrollback(t, win, 400)
	scrollBack(t, win, 200)
	m := newTestOS(win)

	withScrollbarStyle(t, m, config.ScrollbarStyleThin, func() {
		glyphs := barGlyphs(t, barFrame(t, m, win, true), win)
		thumbRows := 0
		for i, glyph := range glyphs {
			switch glyph {
			case "┃":
				thumbRows++
			case "│":
			default:
				t.Fatalf("row %d of the thin bar drew %q, want the ┃ thumb or the │ track", i, glyph)
			}
		}
		if thumbRows == 0 || thumbRows >= len(glyphs) {
			t.Errorf("the thumb covers %d of %d rows; it reports a position, so it can be neither absent nor the whole track",
				thumbRows, len(glyphs))
		}
	})
	m.Settings = config.Global
}

// The track style is the one that fills its column with a surface: a block
// thumb on a fill rather than two hairlines. Both styles still obey the rules
// that make the bar composable - same column, same visibility, same clip.
func TestScrollbarTrackStyleFillsTheColumn(t *testing.T) {
	scrollbarDefaults(t)
	win := newTestWindow(t, "sbtrack-0001", 60, 20)
	win.X, win.Y = 4, 2
	fillScrollback(t, win, 400)
	scrollBack(t, win, 100)
	m := newTestOS(win)

	var thin, track *lipgloss.Layer
	withScrollbarStyle(t, m, config.ScrollbarStyleThin, func() {
		thin = m.renderScrollbarLayer(win, 1000, 1, true)
	})
	m.Settings = config.Global
	withScrollbarStyle(t, m, config.ScrollbarStyleTrack, func() {
		track = m.renderScrollbarLayer(win, 1000, 1, true)
	})
	m.Settings = config.Global
	if thin == nil || track == nil {
		t.Fatal("a scrolled-back pane produced no bar in one of the styles")
	}

	if thin.GetX() != track.GetX() {
		t.Errorf("the styles disagree about the column: thin %d, track %d", thin.GetX(), track.GetX())
	}
	if want := win.Y + win.BorderOffset(); track.GetY() != want {
		t.Errorf("the track starts at y=%d, want the top of the content box %d", track.GetY(), want)
	}
	for name, layer := range map[string]*lipgloss.Layer{"thin": thin, "track": track} {
		if h := lipgloss.Height(layer.GetContent()); h != win.ContentHeight() {
			t.Errorf("the %s bar is %d rows tall, want the viewport's %d", name, h, win.ContentHeight())
		}
		if w := lipgloss.Width(layer.GetContent()); w != 1 {
			t.Errorf("the %s bar is %d columns wide, want 1", name, w)
		}
	}
	// The surface fill is what makes the track style a track rather than a
	// second hairline, and the thumb on it is a block, whole or halved.
	if !drawnIn(track.GetContent(), theme.UI().Surface, true) {
		t.Error("the track style drew no surface behind its thumb")
	}
	withScrollbarStyle(t, m, config.ScrollbarStyleTrack, func() {
		glyphs := strings.Join(barGlyphs(t, barFrame(t, m, win, true), win), "")
		if !strings.ContainsAny(glyphs, "█▀▄") {
			t.Errorf("the track style drew %q, want a block thumb", glyphs)
		}
	})
	m.Settings = config.Global
}

// opentui's half-cell track is what the track style borrows: the bar is
// measured in half rows, so the thumb's ends can land mid-cell as ▀ or ▄ and it
// carries twice the resolution of a whole-cell bar.
func TestScrollbarTrackResolvesToHalfCells(t *testing.T) {
	scrollbarDefaults(t)
	const contentH, scrollbackLen = 20, 400

	withScrollbarStyle(t, nil, config.ScrollbarStyleTrack, func() {
		seenHalf := false
		for offset := 1; offset <= scrollbackLen; offset++ {
			rows, thumbTop, thumbRows := scrollbarRows(contentH, scrollbackLen, offset, &config.Global)
			if len(rows) != contentH {
				t.Fatalf("offset %d produced %d rows, want %d", offset, len(rows), contentH)
			}
			thumb := 0
			for _, r := range rows {
				switch r {
				case "▀", "▄":
					seenHalf = true
					thumb++
				case "█":
					thumb++
				case " ":
				default:
					t.Fatalf("offset %d drew an unexpected glyph %q", offset, r)
				}
			}
			if thumb == 0 {
				t.Fatalf("offset %d drew no thumb at all", offset)
			}
			if thumb != thumbRows || rows[thumbTop] == " " {
				t.Fatalf("offset %d drew %d thumb rows but reported %d starting at %d",
					offset, thumb, thumbRows, thumbTop)
			}
		}
		if !seenHalf {
			t.Error("no offset put a thumb end on a half cell; the track is whole-cell after all")
		}
	})
}

// Half blocks are glyphs ASCII does not have, and neither is the hairline, so
// ASCII keeps the bar it always had: a pipe on the pane's own content, no track.
func TestScrollbarDegradesToASCII(t *testing.T) {
	scrollbarDefaults(t)
	prev := config.Global.UseASCIIOnly
	config.Global.UseASCIIOnly = true
	t.Cleanup(func() { config.Global.UseASCIIOnly = prev })

	if got := config.Global.GetScrollbarThumbChar(); got != "|" {
		t.Errorf("ASCII thumb char = %q, want %q", got, "|")
	}
	withScrollbarStyle(t, nil, config.ScrollbarStyleTrack, func() {
		for _, row := range must(scrollbarRows(20, 400, 137, &config.Global)) {
			if row != " " && row != "|" {
				t.Errorf("ASCII track drew %q, which is neither blank nor the ASCII thumb", row)
			}
		}
	})

	win := newTestWindow(t, "sbascii-0001", 60, 20)
	win.Y = 3
	fillScrollback(t, win, 400)
	scrollBack(t, win, 200)
	m := newTestOS(win)
	withScrollbarStyle(t, m, config.ScrollbarStyleThin, func() {
		layer := m.renderScrollbarLayer(win, 1000, 1, true)
		if layer == nil {
			t.Fatal("no bar for a scrolled-back pane in ASCII")
		}
		// Without a track glyph the bar covers its thumb and nothing else: a
		// blank row would paint over a column of the guest's content.
		if h := lipgloss.Height(layer.GetContent()); h != scrollbarThumbHeight(win.ContentHeight(), win.ScrollbackLenSync()) {
			t.Errorf("the ASCII bar is %d rows tall, want just its thumb", h)
		}
		if got := ansi.Strip(layer.GetContent()); strings.Trim(got, "|\n") != "" {
			t.Errorf("the ASCII bar drew %q, want pipes only", got)
		}
	})
	m.Settings = config.Global
}

// must unwraps scrollbarRows for the assertions that only care about the rows.
func must(rows []string, _, _ int) []string { return rows }

// The owner's rule: the bar matches the highlighted terminal. The focused
// pane's thumb takes its accent, every other pane keeps the quiet grey - and
// both still draw, because the bar reports a scroll position and hiding it on
// an unfocused pane would hide that position.
func TestScrollbarTintFollowsTheFocusedPane(t *testing.T) {
	scrollbarDefaults(t)
	const greenAccent = 2 // accentNames: gray, red, green, ...
	win := newTestWindow(t, "sbtint-0001", 60, 20)
	win.Y = 1
	fillScrollback(t, win, 400)
	scrollBack(t, win, 200)
	m := newTestOS(win)
	m.SidebarAccents = map[string]Accent{win.ID: SlotAccent(greenAccent)}
	m.Mode = TerminalMode

	accent := accentColor(greenAccent)
	if got := theme.ContrastRatio(accent, theme.UI().Canvas); got < scrollbarMinContrast {
		t.Fatalf("the readable accent chosen for this test measures %.2f:1; pick another", got)
	}

	focused := barFrame(t, m, win, true)
	if !barInk(t, m, win, focused, accent) {
		t.Error("the focused pane's thumb is not drawn in its accent")
	}
	unfocused := barFrame(t, m, win, false)
	if barInk(t, m, win, unfocused, accent) {
		t.Error("an unfocused pane's thumb took the accent; only the highlighted terminal is tinted")
	}
	if !barInk(t, m, win, unfocused, theme.BorderUnfocused()) {
		t.Error("an unfocused pane's thumb is not the quiet grey the unfocused frames use")
	}
	for name, frame := range map[string][]string{"focused": focused, "unfocused": unfocused} {
		glyphs := barGlyphs(t, frame, win)
		if !strings.Contains(strings.Join(glyphs, ""), m.Settings.GetScrollbarThumbChar()) {
			t.Errorf("the %s pane drew no bar at all; tint is a colour rule, not a visibility one", name)
		}
	}

	// Without an accent the focused pane's bar is the colour its border is, so
	// bar and border match by construction.
	m.SidebarAccents = nil
	if !barInk(t, m, win, barFrame(t, m, win, true), theme.BorderFocusedTerminal()) {
		t.Error("an accentless focused pane's thumb does not match its terminal-mode border")
	}
	m.Mode = WindowManagementMode
	if !barInk(t, m, win, barFrame(t, m, win, true), theme.BorderFocusedWindow()) {
		t.Error("an accentless focused pane's thumb does not match its window-mode border")
	}
}

// A derived tint has to be legible on the ground it lands on. Dark blue is the
// measured failure: 1.02:1 on the canvas the thin bar floats over and 1.41:1 on
// the track style's surface, which is a bar that is present and invisible.
//
// With no theme loaded a slot is the user terminal's colour, and the numbers
// here are what the index resolves to in this process. They are a stand-in for
// a palette only the terminal knows, which is why the floor is checked against
// them rather than trusted as the shade on screen.
func TestScrollbarTintFloorRejectsAnUnreadableAccent(t *testing.T) {
	scrollbarDefaults(t)
	const darkBlueAccent = 11 // accentNames: ..., blue(4), magenta, cyan, white, dark red, dark green, dark yellow, dark blue
	accent := accentColor(darkBlueAccent)
	pal := theme.UI()

	if got := theme.ContrastRatio(accent, pal.Canvas); math.Abs(got-1.02) > 0.05 {
		t.Errorf("dark blue on the canvas measures %.2f:1, want the documented 1.02:1", got)
	}
	if got := theme.ContrastRatio(accent, pal.Surface); math.Abs(got-1.41) > 0.05 {
		t.Errorf("dark blue on the surface measures %.2f:1, want the documented 1.41:1", got)
	}

	win := newTestWindow(t, "sbfloor-0001", 60, 20)
	win.Y = 1
	fillScrollback(t, win, 400)
	scrollBack(t, win, 200)
	m := newTestOS(win)
	m.SidebarAccents = map[string]Accent{win.ID: SlotAccent(darkBlueAccent)}
	m.Mode = TerminalMode

	for _, style := range []string{config.ScrollbarStyleThin, config.ScrollbarStyleTrack} {
		withScrollbarStyle(t, m, style, func() {
			frame := barFrame(t, m, win, true)
			if barInk(t, m, win, frame, accent) {
				t.Errorf("%s style: the thumb kept an accent that measures below %.1f:1", style, scrollbarMinContrast)
			}
			if !barInk(t, m, win, frame, theme.BorderFocusedTerminal()) {
				t.Errorf("%s style: the rejected accent did not fall back to the mode's focus colour", style)
			}
		})
		m.Settings = config.Global
	}
}

// A hex in the config is a deliberate override of the measurement, so it is
// used as given - including the very colour the floor rejects when it is
// derived. muted is the look every bar had before the rule.
func TestScrollbarTintKeywordsAndHex(t *testing.T) {
	scrollbarDefaults(t)
	const darkBlue = "#0000EE"
	win := newTestWindow(t, "sbhex-0001", 60, 20)
	win.Y = 1
	fillScrollback(t, win, 400)
	scrollBack(t, win, 200)
	m := newTestOS(win)
	m.SidebarAccents = map[string]Accent{win.ID: SlotAccent(2)}
	m.Mode = TerminalMode

	withScrollbarTint(t, m, darkBlue, func() {
		if got := theme.ContrastRatio(lipgloss.Color(darkBlue), theme.UI().Canvas); got >= scrollbarMinContrast {
			t.Fatalf("the hex chosen for this test measures %.2f:1, so it never meets the floor", got)
		}
		if !barInk(t, m, win, barFrame(t, m, win, true), lipgloss.Color(darkBlue)) {
			t.Error("a configured hex was not used as given; the floor is for derived tints only")
		}
	})
	m.Settings = config.Global

	withScrollbarTint(t, m, config.ScrollbarTintMuted, func() {
		for _, focused := range []bool{true, false} {
			if !barInk(t, m, win, barFrame(t, m, win, focused), theme.BorderUnfocused()) {
				t.Errorf("tint = muted, focused = %v: the bar is not the unfocused grey", focused)
			}
		}
	})
	m.Settings = config.Global
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
