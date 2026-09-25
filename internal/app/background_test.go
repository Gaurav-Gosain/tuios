package app

import (
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The backgrounds beyond the pane's: the desktop, the window chrome, the dock
// and the rail, and appearance.background, which sets them all. Each test
// composes the same frame with the option off and on and compares them cell
// by cell, which is the whole of the contract: a cell that had no background
// takes the surface's, a cell that had one keeps it, and a foreground that
// was set is kept.

const surfaceHex = "#2a1b3d"

var surfaceRGBA = color.RGBA{R: 0x2a, G: 0x1b, B: 0x3d, A: 0xff}

// surfaceOS is a client with the rail on the left and the dock at the bottom,
// holding two bordered panes with a gap between them and room around them.
func surfaceOS(t *testing.T) (*OS, *terminal.Window, *terminal.Window) {
	t.Helper()
	a := paneBgWindow(t, "surf-a", 30, 2, 40, 12)
	b := paneBgWindow(t, "surf-b", 72, 2, 30, 12)
	m := paneBgOS(t, "", a, b)
	m.Width, m.Height = 140, 40
	m.Settings.SidebarEnabled = true
	m.Settings.SidebarPosition = "left"
	m.Settings.DockbarPosition = "bottom"
	m.UserConfig = config.DefaultConfig()
	return m, a, b
}

// frameCells composes a frame with the sidebar, the overlays and the dock and
// copies its cells, since the canvas is reused by the next frame.
func composedCells(m *OS) [][]uv.Cell {
	canvas := m.GetCanvas(true)
	out := make([][]uv.Cell, len(canvas.Lines))
	for y, l := range canvas.Lines {
		out[y] = append([]uv.Cell(nil), l...)
	}
	return out
}

// paintCheck compares the frame before and after a surface was painted over
// the rectangle r, and returns how many cells took the ground.
func paintCheck(t *testing.T, name string, off, on [][]uv.Cell, r image.Rectangle, bg, fg color.Color) int {
	t.Helper()
	painted := 0
	for y := r.Min.Y; y < r.Max.Y && y < len(off); y++ {
		for x := r.Min.X; x < r.Max.X && x < len(off[y]); x++ {
			was, now := &off[y][x], &on[y][x]
			if was.IsZero() {
				continue
			}
			if isNilColor(was.Style.Bg) {
				if !samePaneColor(now.Style.Bg, bg) {
					t.Errorf("%s: (%d,%d) %q had no background and now has %v, want %v", name, x, y, was.Content, now.Style.Bg, bg)
					return painted
				}
				painted++
			} else if !samePaneColor(now.Style.Bg, was.Style.Bg) {
				t.Errorf("%s: (%d,%d) %q had background %v and now has %v; a set background has to be kept",
					name, x, y, was.Content, was.Style.Bg, now.Style.Bg)
				return painted
			}
			switch {
			case !isNilColor(was.Style.Fg):
				if !samePaneColor(now.Style.Fg, was.Style.Fg) {
					t.Errorf("%s: (%d,%d) %q had ink %v and now has %v; a set foreground has to be kept",
						name, x, y, was.Content, was.Style.Fg, now.Style.Fg)
					return painted
				}
			case isNilColor(was.Style.Bg) && fg != nil:
				if !samePaneColor(now.Style.Fg, fg) {
					t.Errorf("%s: (%d,%d) %q had the default ink on the default ground and now has %v, want %v",
						name, x, y, was.Content, now.Style.Fg, fg)
					return painted
				}
			}
		}
	}
	return painted
}

// unchanged checks that painting one surface left the cells of r as they were.
func unchanged(t *testing.T, name string, off, on [][]uv.Cell, r image.Rectangle) {
	t.Helper()
	for y := r.Min.Y; y < r.Max.Y && y < len(off); y++ {
		for x := r.Min.X; x < r.Max.X && x < len(off[y]); x++ {
			if !samePaneColor(off[y][x].Style.Bg, on[y][x].Style.Bg) {
				t.Errorf("%s: (%d,%d) belongs to another surface and changed from %v to %v",
					name, x, y, off[y][x].Style.Bg, on[y][x].Style.Bg)
				return
			}
		}
	}
}

// surfaceRects are the rectangles of the fixture's surfaces, in screen cells.
type surfaceRects struct {
	sidebar, dock image.Rectangle
	paneA, paneB  image.Rectangle // the whole window
	contentA      image.Rectangle
	gap           image.Rectangle // between the two panes
	below         image.Rectangle // the empty region under both panes
}

func rectsOf(m *OS, a, b *terminal.Window) surfaceRects {
	_, dockY := m.renderDockString()
	sw := m.GetSidebarWidth()
	return surfaceRects{
		sidebar:  image.Rect(0, m.GetTopMargin(), sw, m.GetTopMargin()+m.GetUsableHeight()),
		dock:     image.Rect(0, dockY, m.GetRenderWidth(), dockY+config.DockHeight),
		paneA:    image.Rect(a.X, a.Y, a.X+a.Width, a.Y+a.Height),
		paneB:    image.Rect(b.X, b.Y, b.X+b.Width, b.Y+b.Height),
		contentA: paneContentRect(a),
		gap:      image.Rect(a.X+a.Width, a.Y, b.X, a.Y+a.Height),
		below:    image.Rect(sw, a.Y+a.Height+1, m.GetRenderWidth(), dockY),
	}
}

func TestEachSurfacePaintsOnlyItsOwnCells(t *testing.T) {
	for _, themeID := range []string{"", "catppuccin_mocha"} {
		t.Run("theme="+themeID, func(t *testing.T) {
			withTheme(t, themeID)
			wantFg := resolveGround(surfaceHex, theme.CurrentThemeID()).fg

			type surfaceCase struct {
				name    string
				set     func(s *config.Settings, v string)
				painted func(r surfaceRects) []image.Rectangle
				other   func(r surfaceRects) []image.Rectangle
			}
			cases := []surfaceCase{
				{
					name: "desktop",
					set:  func(s *config.Settings, v string) { s.DesktopBackground = v },
					painted: func(r surfaceRects) []image.Rectangle {
						return []image.Rectangle{r.gap, r.below}
					},
					other: func(r surfaceRects) []image.Rectangle {
						return []image.Rectangle{r.sidebar, r.dock, r.paneA, r.paneB}
					},
				},
				{
					name: "window chrome",
					set:  func(s *config.Settings, v string) { s.WindowChromeBackground = v },
					painted: func(r surfaceRects) []image.Rectangle {
						// The title row and the bottom border.
						return []image.Rectangle{
							image.Rect(r.paneA.Min.X, r.paneA.Min.Y, r.paneA.Max.X, r.paneA.Min.Y+1),
							image.Rect(r.paneA.Min.X, r.paneA.Max.Y-1, r.paneA.Max.X, r.paneA.Max.Y),
						}
					},
					other: func(r surfaceRects) []image.Rectangle {
						return []image.Rectangle{r.contentA, r.gap, r.sidebar, r.dock}
					},
				},
				{
					name: "dock",
					set:  func(s *config.Settings, v string) { s.DockBackground = v },
					painted: func(r surfaceRects) []image.Rectangle {
						return []image.Rectangle{r.dock}
					},
					other: func(r surfaceRects) []image.Rectangle {
						return []image.Rectangle{r.sidebar, r.paneA, r.gap, r.below}
					},
				},
				{
					name: "sidebar",
					set:  func(s *config.Settings, v string) { s.SidebarBackground = v },
					painted: func(r surfaceRects) []image.Rectangle {
						return []image.Rectangle{r.sidebar}
					},
					other: func(r surfaceRects) []image.Rectangle {
						return []image.Rectangle{r.dock, r.paneA, r.gap, r.below}
					},
				},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					m, a, b := surfaceOS(t)
					r := rectsOf(m, a, b)
					off := composedCells(m)
					tc.set(&m.Settings, surfaceHex)
					// No dirty marking: a layer that did not change has to be
					// repainted from its cached cells when its ground does.
					on := composedCells(m)
					total := 0
					for _, pr := range tc.painted(r) {
						total += paintCheck(t, tc.name, off, on, pr, surfaceRGBA, wantFg)
					}
					if total == 0 {
						t.Errorf("%s: no cell took the ground", tc.name)
					}
					for _, or := range tc.other(r) {
						unchanged(t, tc.name, off, on, or)
					}
					// And off again puts every cell back.
					tc.set(&m.Settings, config.BackgroundOff)
					back := composedCells(m)
					unchanged(t, tc.name+" switched off", off, back, image.Rect(0, 0, m.Width, m.Height))
				})
			}
		})
	}
}

// One line paints everything: with appearance.background set there is no cell
// on the frame left on the default background.
func TestAllSurfacesLeaveNoTransparentCell(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	m, a, b := surfaceOS(t)
	r := rectsOf(m, a, b)
	off := composedCells(m)
	m.Settings.Background = surfaceHex
	on := composedCells(m)

	wantFg := resolveGround(surfaceHex, theme.CurrentThemeID()).fg
	if n := paintCheck(t, "all", off, on, image.Rect(0, 0, m.Width, m.Height), surfaceRGBA, wantFg); n == 0 {
		t.Fatal("nothing was painted")
	}
	for y, line := range on {
		for x := range line {
			if c := &line[x]; !c.IsZero() && isNilColor(c.Style.Bg) {
				t.Fatalf("(%d,%d) %q is still transparent with every surface painted", x, y, c.Content)
			}
		}
	}

	// A surface's own off overrides the default and leaves that surface as it
	// was, and only that surface.
	m.Settings.DockBackground = config.BackgroundOff
	dockOff := composedCells(m)
	unchanged(t, "dock off", off, dockOff, r.dock)
	paintCheck(t, "the rest", off, dockOff, r.sidebar, surfaceRGBA, wantFg)
	paintCheck(t, "the rest", off, dockOff, r.gap, surfaceRGBA, wantFg)

	// And a surface's own colour overrides the default for that surface.
	m.Settings.DockBackground = "#010203"
	own := composedCells(m)
	paintCheck(t, "dock's own colour", off, own, r.dock, color.RGBA{R: 1, G: 2, B: 3, A: 0xff},
		resolveGround("#010203", theme.CurrentThemeID()).fg)
}

// An empty workspace is all desktop, the splash's blank cells included, and
// the splash's own inks are kept.
func TestDesktopBackgroundPaintsAnEmptyWorkspace(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	m, a, b := surfaceOS(t)
	a.Workspace, b.Workspace = 5, 5
	off := composedCells(m)
	m.Settings.DesktopBackground = surfaceHex
	on := composedCells(m)
	sw := m.GetSidebarWidth()
	_, dockY := m.renderDockString()
	content := image.Rect(sw, m.GetTopMargin(), m.GetRenderWidth(), dockY)
	if n := paintCheck(t, "empty workspace", off, on, content, surfaceRGBA,
		resolveGround(surfaceHex, theme.CurrentThemeID()).fg); n < content.Dx()*content.Dy()/2 {
		t.Errorf("only %d cells of an empty workspace took the desktop ground", n)
	}
}

// The chrome keeps its own inks under a painted ground, on every kind of pane
// that has chrome: tiled, floating and zoomed. A borderless pane has none.
func TestWindowChromeBackgroundOnEveryKindOfPane(t *testing.T) {
	withTheme(t, "")
	for _, tc := range []struct {
		name  string
		setup func(w *terminal.Window)
		none  bool
	}{
		{name: "tiled with a border"},
		{name: "floating", setup: func(w *terminal.Window) { w.IsFloating = true }},
		{name: "zoomed", setup: func(w *terminal.Window) { w.Zoomed = true }},
		{name: "borderless", setup: func(w *terminal.Window) { w.Tiled = true }, none: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			win := paneBgWindow(t, "chrome-kind", 2, 2, 40, 10)
			m := paneBgOS(t, "", win)
			if tc.setup != nil {
				tc.setup(win)
			}
			off := m.GetCanvas(false)
			border := off.CellAt(win.X, win.Y+2)
			wasBg, wasFg, wasContent := border.Style.Bg, border.Style.Fg, border.Content
			m.Settings.WindowChromeBackground = surfaceHex
			on := m.GetCanvas(false)
			got := on.CellAt(win.X, win.Y+2)
			if tc.none {
				if !samePaneColor(got.Style.Bg, wasBg) {
					t.Errorf("a borderless pane's edge column is content, and took the chrome ground %v", got.Style.Bg)
				}
				return
			}
			if !isNilColor(wasBg) {
				t.Fatalf("the border cell %q already had a background %v; the fixture proves nothing", wasContent, wasBg)
			}
			if !samePaneColor(got.Style.Bg, surfaceRGBA) {
				t.Errorf("the border cell %q has bg %v, want the chrome ground %v", got.Content, got.Style.Bg, surfaceRGBA)
			}
			if !samePaneColor(got.Style.Fg, wasFg) {
				t.Errorf("the border's ink changed from %v to %v", wasFg, got.Style.Fg)
			}
			if bg := on.CellAt(paneContentRect(win).Min.X+20, paneContentRect(win).Min.Y+2).Style.Bg; !isNilColor(bg) {
				t.Errorf("the pane's content took %v from the chrome option", bg)
			}
		})
	}
}

// A separator layer is chrome, and its line keeps its ink.
func TestWindowChromeBackgroundPaintsTheSharedBorderLine(t *testing.T) {
	withTheme(t, "")
	m := paneBgOS(t, "")
	m.Width, m.Height = 20, 5
	ink := lipgloss.Color("#ff0000")
	line := lipgloss.NewStyle().Foreground(ink).Render("│\n│\n│")
	compose := func() *frameCanvas {
		canvas := &frameCanvas{Buffer: *uv.NewBuffer(20, 5)}
		m.composeLayers(canvas, []*lipgloss.Layer{lipgloss.NewLayer(line).X(4).Y(1).Z(5).ID("sep-1-4")})
		return canvas
	}
	if bg := compose().CellAt(4, 2).Style.Bg; !isNilColor(bg) {
		t.Fatalf("with the option off the line has bg %v", bg)
	}
	m.Settings.WindowChromeBackground = surfaceHex
	c := compose().CellAt(4, 2)
	if !samePaneColor(c.Style.Bg, surfaceRGBA) {
		t.Errorf("the shared border line has bg %v, want the chrome ground", c.Style.Bg)
	}
	if !samePaneColor(c.Style.Fg, ink) {
		t.Errorf("the shared border line's ink became %v", c.Style.Fg)
	}
}

// The fast path draws the pane, its border and the dock without the
// compositor, and paints their backgrounds into its frame string, so no
// background takes it away. TestFastPathPaintMatchesTheCompositor holds the
// cells it paints to the compositor's.
func TestBackgroundsAndTheFullscreenFastPath(t *testing.T) {
	withTheme(t, "")
	for _, tc := range []struct {
		name string
		set  func(s *config.Settings)
		fast bool
	}{
		{name: "all off", set: func(*config.Settings) {}, fast: true},
		{name: "desktop", set: func(s *config.Settings) { s.DesktopBackground = surfaceHex }, fast: true},
		{name: "sidebar", set: func(s *config.Settings) { s.SidebarBackground = surfaceHex }, fast: true},
		{name: "pane", set: func(s *config.Settings) { s.PaneBackground = surfaceHex }, fast: true},
		{name: "window chrome", set: func(s *config.Settings) { s.WindowChromeBackground = surfaceHex }, fast: true},
		{name: "dock", set: func(s *config.Settings) { s.DockBackground = surfaceHex }, fast: true},
		{name: "all surfaces", set: func(s *config.Settings) { s.Background = surfaceHex }, fast: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := keystrokeOS(t, 1, 80, 24)
			m.Settings.SidebarEnabled = false
			m.Settings.DockbarPosition = "bottom"
			win := m.Windows[0]
			win.X, win.Y, win.Width, win.Height = 0, m.GetTopMargin(), m.GetRenderWidth(), m.GetUsableHeight()
			win.MarkPositionDirty()
			if _, ok := m.fullscreenFastWindow(); !ok {
				t.Skip("the fixture does not qualify for the fast path, so this proves nothing")
			}
			tc.set(&m.Settings)
			if _, ok := m.fullscreenFastWindow(); ok != tc.fast {
				t.Errorf("fast path taken = %v, want %v", ok, tc.fast)
			}
			frame := m.composeFrame()
			if painted := strings.Contains(frame, "48;2;42;27;61"); painted == (tc.name == "all off" || tc.name == "desktop" || tc.name == "sidebar") {
				t.Errorf("the frame carries the ground = %v", painted)
			}
		})
	}
}

// Every background off has to cost nothing per frame beyond the comparisons
// that find it off: no allocation.
func TestBackgroundsOffAllocateNothing(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	m := paneBgOS(t, "")
	if n := testing.AllocsPerRun(100, func() {
		g := m.frameGrounds()
		_ = g.any()
	}); n != 0 {
		t.Errorf("resolving every background off allocates %.0f times", n)
	}
	m.Settings.Background = surfaceHex
	_ = m.frameGrounds()
	if n := testing.AllocsPerRun(100, func() { _ = m.frameGrounds() }); n != 0 {
		t.Errorf("resolving cached backgrounds allocates %.0f times", n)
	}
	canvas := &frameCanvas{Buffer: *uv.NewBuffer(80, 24)}
	canvas.ClearTo(ground{})
	if n := testing.AllocsPerRun(100, func() { canvas.ClearTo(ground{}) }); n != 0 {
		t.Errorf("clearing the canvas with the desktop off allocates %.0f times", n)
	}
	desk := m.surfaceGround(surfaceDesktop)
	canvas.ClearTo(desk)
	if n := testing.AllocsPerRun(100, func() { canvas.ClearTo(desk) }); n != 0 {
		t.Errorf("clearing the canvas to a cached desktop ground allocates %.0f times", n)
	}
}

// A program in a pane asks what it is drawn on. The pane's emulator answers
// with the painted ground once the frame is composed, and with its own answer
// again when the paint is off. The daemon's emulators are told through the
// state this client pushes, and that is checked here too.
func TestPaneBackgroundAnswersOSC11(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	win := paneBgWindow(t, "osc-11", 2, 2, 40, 10)
	m := paneBgOS(t, "", win)

	ask := func(q string) string {
		t.Helper()
		win.WriteOutput([]byte(q))
		got := make(chan string, 1)
		go func() {
			buf := make([]byte, 256)
			n, _ := win.Terminal.Read(buf)
			got <- string(buf[:n])
		}()
		select {
		case s := <-got:
			return s
		case <-time.After(2 * time.Second):
			t.Fatal("the pane's emulator did not answer")
		}
		return ""
	}
	_ = m.composeFrame()
	before := ask("\x1b]11;?\x1b\\")
	if state := m.BuildSessionState(); state.PaneReportBg != "" || state.PaneReportFg != "" {
		t.Errorf("with the pane background off the state carries %q/%q", state.PaneReportBg, state.PaneReportFg)
	}

	m.Settings.Background = paneBgHex
	_ = m.composeFrame()
	if got := ask("\x1b]11;?\x1b\\"); !strings.Contains(got, "rgb:1212/3434/5656") {
		t.Errorf("OSC 11 answered %q, want the painted rgb:1212/3434/5656", got)
	}
	wantFg := colorHex(resolveGround(paneBgHex, theme.CurrentThemeID()).fg)
	r, g, b := wantFg[1:3], wantFg[3:5], wantFg[5:7]
	if got := ask("\x1b]10;?\x1b\\"); !strings.Contains(got, "rgb:"+r+r+"/"+g+g+"/"+b+b) {
		t.Errorf("OSC 10 answered %q, want the ink the pane gives default text, %s", got, wantFg)
	}
	state := m.BuildSessionState()
	if state.PaneReportBg != paneBgHex || state.PaneReportFg != wantFg {
		t.Errorf("the state pushed to the daemon carries %q/%q, want %s/%s", state.PaneReportBg, state.PaneReportFg, paneBgHex, wantFg)
	}

	// The pane's own off wins over the default for every surface.
	m.Settings.PaneBackground = config.BackgroundOff
	_ = m.composeFrame()
	if got := ask("\x1b]11;?\x1b\\"); got != before {
		t.Errorf("with the pane background off again OSC 11 answered %q, want the original %q", got, before)
	}
}
