package app

import (
	"fmt"
	"image/color"
	"math/rand"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The fullscreen fast path paints the pane, chrome and dock backgrounds into
// its frame string, and the compositor paints them on parsed cells. They are
// two implementations of one rule, so these tests put the same content
// through both and compare what the host would draw, cell by cell.

// fastPathContent is one of every kind of cell the paint has to tell apart:
// text on the default colours, text with an application background (basic,
// 256 and truecolor), text with only a foreground, reverse video, an underline
// colour, wide characters, a background run erased to the right edge so it
// meets the right border, and blank rows below.
const fastPathContent = "plain default\r\n" +
	"\x1b[41mRED\x1b[0m mid \x1b[48;2;1;2;3mTRUE\x1b[m \x1b[48;5;17mIDX\x1b[m end\r\n" +
	"\x1b[32mgreen\x1b[39m \x1b[38;2;200;100;50mink\x1b[m \x1b[7mreversed\x1b[27m back\r\n" +
	"\x1b[4:3;58;2;9;9;9mcurly\x1b[m \x1b[1;2;3mbold faint italic\x1b[m\r\n" +
	"wide 漢字 and more\r\n" +
	"\x1b[44mblue to the edge\x1b[K\x1b[m\r\n" +
	"\x1b[7m\x1b[K\x1b[m\r\n" +
	"last line"

// fastPathOS is a lone pane filling the region, which is the shape the fast
// path takes, holding fastPathContent.
func fastPathOS(t *testing.T, dockPos string) (*OS, *terminal.Window) {
	t.Helper()
	m := keystrokeOS(t, 1, 80, 24)
	m.Settings.SidebarEnabled = false
	m.Settings.DockbarPosition = dockPos
	win := m.Windows[0]
	win.X, win.Y, win.Width, win.Height = 0, m.GetTopMargin(), m.GetRenderWidth(), m.GetUsableHeight()
	win.Resize(win.Width, win.Height)
	win.WriteOutput([]byte("\x1b[2J\x1b[H" + fastPathContent))
	win.MarkPositionDirty()
	win.MarkContentDirty()
	return m, win
}

// hostCells parses a frame the way the host's renderer does.
func hostCells(frame string, w, h int) uv.ScreenBuffer {
	buf := uv.NewScreenBuffer(w, h)
	uv.NewStyledString(frame).Draw(buf, buf.Bounds())
	return buf
}

// fastAndComposed renders the current state once through the fast path and
// once through the compositor, and returns the two frames as the host parses
// them.
func fastAndComposed(t *testing.T, m *OS, wantFast bool) (fast, composed uv.ScreenBuffer) {
	t.Helper()
	if _, ok := m.fullscreenFastWindow(); ok != wantFast {
		t.Fatalf("fast path eligible = %v, want %v", ok, wantFast)
	}
	for _, w := range m.Windows {
		w.MarkContentDirty()
	}
	fastFrame := m.composeFrame()
	prev := fastPathDisabled
	fastPathDisabled = true
	defer func() { fastPathDisabled = prev }()
	for _, w := range m.Windows {
		w.MarkContentDirty()
	}
	composedFrame := m.composeFrame()
	rw, rh := m.GetRenderWidth(), m.GetRenderHeight()
	return hostCells(fastFrame, rw, rh), hostCells(composedFrame, rw, rh)
}

// sameCells compares two parsed frames cell by cell and reports the first
// few differences. It returns how many cells carry the ground bg, so a case
// can tell that it painted something.
func sameCells(t *testing.T, fast, composed uv.ScreenBuffer, bg color.Color) int {
	t.Helper()
	painted, bad := 0, 0
	for y := range composed.Height() {
		for x := range composed.Width() {
			a, b := fast.CellAt(x, y), composed.CellAt(x, y)
			if a == nil || b == nil {
				if (a == nil) != (b == nil) {
					t.Errorf("(%d,%d): fast %v, composed %v", x, y, a, b)
				}
				continue
			}
			if bg != nil && samePaneColor(b.Style.Bg, bg) {
				painted++
			}
			if a.Content == b.Content && a.Width == b.Width && a.Style.Attrs == b.Style.Attrs &&
				a.Style.Underline == b.Style.Underline && a.Link == b.Link &&
				samePaneColor(a.Style.Fg, b.Style.Fg) && samePaneColor(a.Style.Bg, b.Style.Bg) &&
				samePaneColor(a.Style.UnderlineColor, b.Style.UnderlineColor) {
				continue
			}
			if bad++; bad <= 5 {
				t.Errorf("(%d,%d): fast %q fg %v bg %v attrs %d ul %d/%v, composed %q fg %v bg %v attrs %d ul %d/%v",
					x, y, a.Content, a.Style.Fg, a.Style.Bg, a.Style.Attrs, a.Style.Underline, a.Style.UnderlineColor,
					b.Content, b.Style.Fg, b.Style.Bg, b.Style.Attrs, b.Style.Underline, b.Style.UnderlineColor)
			}
		}
	}
	if bad > 5 {
		t.Errorf("%d cells differ in all", bad)
	}
	return painted
}

func TestFastPathPaintMatchesTheCompositor(t *testing.T) {
	const paneHex, chromeHex, dockHex = "#123456", "#2a1b3d", "#f0e0d0"
	settings := []struct {
		name  string
		theme string
		set   func(s *config.Settings)
		bg    color.Color // a ground the frame has to show somewhere, nil for none
	}{
		{name: "off", set: func(*config.Settings) {}},
		{name: "all colour", set: func(s *config.Settings) { s.Background = surfaceHex }, bg: surfaceRGBA},
		{name: "all colour with theme", theme: "catppuccin_mocha", set: func(s *config.Settings) { s.Background = surfaceHex }, bg: surfaceRGBA},
		{name: "all theme", theme: "catppuccin_mocha", set: func(s *config.Settings) { s.Background = config.BackgroundTheme }},
		{name: "pane", set: func(s *config.Settings) { s.PaneBackground = paneHex }, bg: paneBgRGBA},
		{name: "pane theme", theme: "nord", set: func(s *config.Settings) { s.PaneBackground = config.BackgroundTheme }},
		{name: "chrome", set: func(s *config.Settings) { s.WindowChromeBackground = chromeHex }, bg: surfaceRGBA},
		{name: "dock", set: func(s *config.Settings) { s.DockBackground = dockHex }},
		{name: "desktop only", set: func(s *config.Settings) { s.DesktopBackground = dockHex }},
		{name: "each its own", theme: "catppuccin_mocha", set: func(s *config.Settings) {
			s.PaneBackground, s.WindowChromeBackground, s.DockBackground, s.DesktopBackground = paneHex, chromeHex, dockHex, surfaceHex
		}, bg: paneBgRGBA},
		{name: "pane and dock, bare chrome", theme: "nord", set: func(s *config.Settings) {
			s.PaneBackground, s.DockBackground = paneHex, dockHex
		}, bg: paneBgRGBA},
	}
	states := []struct {
		name     string
		dock     string
		prepare  func(m *OS, w *terminal.Window)
		wantFast bool
	}{
		{name: "focused", dock: "bottom", wantFast: true},
		{name: "dock top", dock: "top", wantFast: true},
		{name: "dock hidden", dock: "hidden", wantFast: true},
		{name: "window mode", dock: "bottom", wantFast: true, prepare: func(m *OS, _ *terminal.Window) {
			m.Mode = WindowManagementMode
		}},
		{name: "unfocused", dock: "bottom", wantFast: true, prepare: func(m *OS, _ *terminal.Window) {
			// The emulator's own Render, and the lipgloss border box around it
			// when its rows are ragged.
			m.FocusedWindow = -1
		}},
		{name: "unfocused and dimmed", dock: "bottom", wantFast: true, prepare: func(m *OS, _ *terminal.Window) {
			m.FocusedWindow = -1
			m.Settings.DimUnfocused = 40
		}},
		{name: "zen hides the border", dock: "bottom", wantFast: true, prepare: func(m *OS, _ *terminal.Window) {
			m.FocusedWindow = -1
			m.Settings.ZenMode = config.ZenModeAlways
		}},
		{name: "copy mode cursor and selection", dock: "bottom", wantFast: true, prepare: func(_ *OS, w *terminal.Window) {
			w.EnterCopyMode()
			top := w.ScrollbackLen()
			w.CopyMode.CursorX, w.CopyMode.CursorY = 3, 2
			w.CopyMode.State = terminal.CopyModeVisualChar
			w.CopyMode.VisualStart = terminal.Position{X: 2, Y: top + 1}
			w.CopyMode.VisualEnd = terminal.Position{X: 40, Y: top + 3}
		}},
		{name: "scrolled back with a scrollbar", dock: "bottom", wantFast: false, prepare: func(m *OS, w *terminal.Window) {
			for i := range 60 {
				w.WriteOutput(fmt.Appendf(nil, "\r\nline %d \x1b[41mred\x1b[m", i))
			}
			w.EnterCopyMode()
			w.CopyMode.ScrollOffset = 5
			w.ScrollbackOffset = 5
			m.Settings.ScrollbarStyle = config.ScrollbarStyleThin
			m.Settings.HideScrollbar = false
		}},
	}
	for _, st := range settings {
		for _, state := range states {
			t.Run(st.name+"/"+state.name, func(t *testing.T) {
				withTheme(t, st.theme)
				m, win := fastPathOS(t, state.dock)
				if state.prepare != nil {
					state.prepare(m, win)
				}
				st.set(&m.Settings)
				fast, composed := fastAndComposed(t, m, state.wantFast)
				painted := sameCells(t, fast, composed, st.bg)
				if st.bg != nil && painted == 0 {
					t.Errorf("no cell carries the ground %v, so this compared nothing painted", st.bg)
				}
			})
		}
	}
}

// Painting a frame allocates the frame string and nothing else: the colour
// sequences were formatted when the ground was resolved and the buffer is
// kept.
func TestFastPathPaintAllocatesOnlyTheFrame(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	m, win := fastPathOS(t, "bottom")
	m.Settings.Background = surfaceHex
	box := m.renderWindowBox(win, 0, true, color.White)
	dock, _ := m.renderDockString()
	_ = m.paintFullscreenFrame(win, box, dock, "bottom")
	if n := testing.AllocsPerRun(50, func() {
		_ = m.paintFullscreenFrame(win, box, dock, "bottom")
	}); n > 1 {
		t.Errorf("painting the frame allocates %.0f times, want 1 (the frame)", n)
	}
}

// sgrColorSpan has to read a colour's parameters exactly as ReadStyleColor
// does, or the painter's pen and the host's drift apart after an odd colour.
func TestSGRColorSpanMatchesReadStyleColor(t *testing.T) {
	var inputs []string
	for _, which := range []string{"38", "48", "58"} {
		for _, sep := range []string{";", ":"} {
			for _, rest := range []string{
				"", "0", "1", "2", "3", "4", "5", "6", "7", "5" + sep + "200", "5" + sep,
				"2" + sep + "1" + sep + "2" + sep + "3", "2" + sep + "1" + sep + "2",
				"2" + sep + sep + "1" + sep + "2" + sep + "3", "3" + sep + "1" + sep + "2" + sep + "3",
				"4" + sep + "1" + sep + "2" + sep + "3" + sep + "4", "6" + sep + "1" + sep + "2" + sep + "3" + sep + "4",
				"2" + sep + "0" + sep + "1" + sep + "2" + sep + "3" + sep + "4" + sep + "5" + sep + "6",
				"2:1:2:3;1", "2;1;2;3:4", "5:1;2", "2::1:2:3:4:5",
			} {
				inputs = append(inputs, which+sep+rest, which+sep+rest+";31", which+sep+rest+":7;1")
			}
		}
	}
	rng := rand.New(rand.NewSource(7))
	for range 3000 {
		var b strings.Builder
		b.WriteString([]string{"38", "48", "58"}[rng.Intn(3)])
		for range rng.Intn(10) {
			b.WriteByte(";:"[rng.Intn(2)])
			if rng.Intn(5) > 0 {
				fmt.Fprintf(&b, "%d", rng.Intn(8))
			}
		}
		inputs = append(inputs, b.String())
	}
	p := ansi.NewParser()
	for _, in := range inputs {
		seq := "\x1b[" + in + "m"
		ansi.DecodeSequence(seq, ansi.NormalState, p)
		params := p.Params()
		var c color.Color
		wantN := ansi.ReadStyleColor(params, &c)
		gotN, gotNil := sgrColorSpan(params)
		if gotN != wantN || (wantN > 0 && gotNil != (c == nil)) {
			t.Errorf("%q: span %d nil %v, ReadStyleColor %d nil %v", seq, gotN, gotNil, wantN, c == nil)
		}
	}
}

// The painter's pen has to be the host's pen after any SGR, which is what
// decides whether a ground goes in front of the next text.
func TestFastPainterFollowsThePen(t *testing.T) {
	ink := color.RGBA{R: 0xee, G: 0xdd, B: 0xcc, A: 0xff}
	grounds := []ground{
		{bg: surfaceRGBA, bgSGR: sgrColor(48, surfaceRGBA)},
		{bg: surfaceRGBA, fg: ink, bgSGR: sgrColor(48, surfaceRGBA), fgSGR: sgrColor(38, ink)},
	}
	pieces := []string{
		"\x1b[m", "\x1b[0m", "\x1b[41m", "\x1b[49m", "\x1b[31m", "\x1b[39m", "\x1b[48;2;1;2;3m", "\x1b[38:2::1:2:3m",
		"\x1b[48;5;9m", "\x1b[48:0m", "\x1b[48;1m", "\x1b[4:3;48;2;5;5;5m", "\x1b[4;41m", "\x1b[1;22;7m", "\x1b[48;2;1m",
		"\x1b[38;5m", "\x1b[;41m", "\x1b[58;2;1;2;3;41m", "\x1b]8;;http://x\x1b\\",
		"\x1b[1;2;3;4;5;6;7;8;9m", "\x1b[4:3m", "\x1b[58;5;3m", "\x1b[m", "\x1b[0m",
		"a", "bc", "_", "漢", "\n",
	}
	for gi := range grounds {
		g := &grounds[gi]
		rng := rand.New(rand.NewSource(11))
		for range 1000 {
			var b strings.Builder
			for range 1 + rng.Intn(20) {
				b.WriteString(pieces[rng.Intn(len(pieces))])
			}
			src := b.String()
			var p fastPainter
			p.parser = ansi.NewParser()
			p.fgNil, p.bgNil = true, true
			p.block(src, g)
			// No piece prints a space, so a cell holding one is a cell the
			// text never reached, which the paint must leave alone.
			plain := hostCells(src, 20, 8)
			painted := hostCells(string(p.out), 20, 8)
			for y := range 8 {
				for x := range 20 {
					a, c := plain.CellAt(x, y), painted.CellAt(x, y)
					wantBg, wantFg := a.Style.Bg, a.Style.Fg
					if a.Content != " " && a.Content != "" {
						if isNilColor(wantBg) {
							wantBg = g.bg
						}
						if isNilColor(wantFg) && g.fg != nil {
							wantFg = g.fg
						}
					}
					if a.Content != c.Content || !samePaneColor(c.Style.Bg, wantBg) || !samePaneColor(c.Style.Fg, wantFg) ||
						c.Style.Attrs != a.Style.Attrs || c.Style.Underline != a.Style.Underline ||
						!samePaneColor(c.Style.UnderlineColor, a.Style.UnderlineColor) {
						t.Fatalf("ground %d, %q at (%d,%d): painted %q bg %v fg %v attrs %d ul %d, want %q bg %v fg %v attrs %d ul %d",
							gi, src, x, y, c.Content, c.Style.Bg, c.Style.Fg, c.Style.Attrs, c.Style.Underline,
							a.Content, wantBg, wantFg, a.Style.Attrs, a.Style.Underline)
					}
				}
			}
		}
	}
}
