package app

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// These run on whichever VT backend the test binary was built with, so the
// same assertions cover the pure Go emulator and, under -tags ghostty,
// libghostty-vt. The paint is applied to the cells a pane's layer parses to,
// so what differs between the backends is only the cells they hand over.

const paneBgHex = "#123456"

var paneBgRGBA = color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}

// paneBgOS is a client with the given windows and pane background, laid out
// untiled at fixed places so a cell can be read at a known coordinate.
func paneBgOS(t *testing.T, setting string, wins ...*terminal.Window) *OS {
	t.Helper()
	m := &OS{
		Settings:         config.Global,
		Windows:          wins,
		FocusedWindow:    0,
		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            100,
		Height:           30,
		Mode:             TerminalMode,
	}
	m.Settings.SidebarEnabled = false
	m.Settings.PaneBackground = setting
	return m
}

// paneBgWindow is a bordered pane at (x, y) holding one line with a red
// background run, a default-coloured run, and nothing after it, so the three
// kinds of cell the paint has to tell apart are all on row 0.
func paneBgWindow(t *testing.T, id string, x, y, w, h int) *terminal.Window {
	t.Helper()
	win := newTestWindow(t, id, w, h)
	win.X, win.Y, win.Width, win.Height = x, y, w, h
	win.Workspace = 1
	win.WriteOutput([]byte("\x1b[41mRED\x1b[0m plain"))
	win.MarkContentDirty()
	return win
}

// cellColors reads a composed cell's background and foreground.
func cellColors(t *testing.T, c *frameCanvas, x, y int) (bg, fg color.Color) {
	t.Helper()
	cell := c.CellAt(x, y)
	if cell == nil {
		t.Fatalf("no cell at (%d,%d)", x, y)
	}
	return cell.Style.Bg, cell.Style.Fg
}

func samePaneColor(a, b color.Color) bool {
	if isNilColor(a) || isNilColor(b) {
		return isNilColor(a) && isNilColor(b)
	}
	return safeColorEquals(a, b)
}

func TestPaneBackgroundThemePaintsTheThemeGroundAndInk(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	win := paneBgWindow(t, "pbg-theme", 2, 2, 40, 10)
	m := paneBgOS(t, config.PaneBackgroundTheme, win)
	canvas := m.GetCanvas(false)
	cx, cy := win.X+1, win.Y+1

	wantBg, wantFg := theme.TerminalBg(), theme.TerminalFg()
	bg, fg := cellColors(t, canvas, cx+4, cy)
	if !samePaneColor(bg, wantBg) {
		t.Errorf("theme painted bg %v, want the theme's %v", bg, wantBg)
	}
	// The theme's ink goes with its ground: a host's own foreground was chosen
	// for the host's background, not this one.
	if !samePaneColor(fg, wantFg) {
		t.Errorf("theme left fg %v, want the theme's %v", fg, wantFg)
	}
	if bg, _ := cellColors(t, canvas, cx, cy); samePaneColor(bg, wantBg) {
		t.Error("the app's red background was replaced by the theme's")
	}
}

func TestPaneBackgroundThemeWithNoThemePaintsNothing(t *testing.T) {
	withTheme(t, "")
	win := paneBgWindow(t, "pbg-notheme", 2, 2, 40, 10)
	m := paneBgOS(t, config.PaneBackgroundTheme, win)
	canvas := m.GetCanvas(false)
	if bg, _ := cellColors(t, canvas, win.X+5, win.Y+1); !isNilColor(bg) {
		t.Errorf("theme with no theme painted %v", bg)
	}
}

// Every render path a pane can take reaches the compositor the same way, so
// every one of them is painted: the focused per-cell path, the unfocused fast
// path, scrollback, copy mode, a floating pane, a zoomed pane, a borderless
// tiled pane, and the blank area a short body is padded out with.
func TestPaneBackgroundCoversEveryRenderPath(t *testing.T) {
	withTheme(t, "")
	cases := []struct {
		name  string
		setup func(t *testing.T, m *OS, win *terminal.Window)
		// the cell to read, relative to the pane's content origin
		dx, dy int
	}{
		{name: "focused", dx: 20, dy: 0},
		{name: "unfocused fast path", setup: func(_ *testing.T, m *OS, _ *terminal.Window) {
			m.FocusedWindow = -1
		}, dx: 20, dy: 0},
		{name: "window management mode", setup: func(_ *testing.T, m *OS, _ *terminal.Window) {
			m.Mode = WindowManagementMode
		}, dx: 20, dy: 3},
		{name: "scrollback", setup: func(_ *testing.T, _ *OS, win *terminal.Window) {
			for i := range 40 {
				win.WriteOutput(fmt.Appendf(nil, "\r\nline %d", i))
			}
			win.ScrollbackOffset = 3
			win.MarkContentDirty()
		}, dx: 25, dy: 1},
		{name: "copy mode", setup: func(_ *testing.T, _ *OS, win *terminal.Window) {
			win.EnterCopyMode()
			win.MarkContentDirty()
		}, dx: 25, dy: 1},
		{name: "floating", setup: func(_ *testing.T, _ *OS, win *terminal.Window) {
			win.IsFloating = true
		}, dx: 20, dy: 2},
		{name: "zoomed", setup: func(_ *testing.T, _ *OS, win *terminal.Window) {
			win.Zoomed = true
		}, dx: 20, dy: 2},
		{name: "borderless tiled", setup: func(_ *testing.T, _ *OS, win *terminal.Window) {
			win.Tiled = true
		}, dx: 20, dy: 2},
		{name: "padded past the emulator", setup: func(_ *testing.T, _ *OS, win *terminal.Window) {
			// The rectangle grows and the emulator does not, as it does for
			// the length of a snap animation: the body is padded out to the
			// box, and the padding is pane too.
			win.Width += 12
			win.MarkPositionDirty()
		}, dx: 44, dy: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			win := paneBgWindow(t, "pbg-path", 2, 2, 40, 10)
			m := paneBgOS(t, paneBgHex, win)
			if tc.setup != nil {
				tc.setup(t, m, win)
			}
			canvas := m.GetCanvas(false)
			r := paneContentRect(win)
			x, y := r.Min.X+tc.dx, r.Min.Y+tc.dy
			if !image.Pt(x, y).In(r) {
				t.Fatalf("(%d,%d) is outside the content %v", x, y, r)
			}
			if bg, _ := cellColors(t, canvas, x, y); !samePaneColor(bg, paneBgRGBA) {
				t.Errorf("(%d,%d) has bg %v, want %v", x, y, bg, paneBgRGBA)
			}
		})
	}
}

// A selection, a search match and the copy mode cursor set their own
// backgrounds, so they show over a painted pane as they do over a bare one.
func TestPaneBackgroundKeepsTheSelection(t *testing.T) {
	withTheme(t, "")
	win := paneBgWindow(t, "pbg-sel", 2, 2, 40, 10)
	m := paneBgOS(t, paneBgHex, win)
	win.EnterCopyMode()
	top := win.ScrollbackLen()
	win.CopyMode.State = terminal.CopyModeVisualChar
	win.CopyMode.VisualStart = terminal.Position{X: 4, Y: top}
	win.CopyMode.VisualEnd = terminal.Position{X: 8, Y: top}
	win.MarkContentDirty()
	canvas := m.GetCanvas(false)

	sel := lipgloss.Color(m.Settings.SelectionBg)
	cx, cy := win.X+1, win.Y+1
	if bg, _ := cellColors(t, canvas, cx+5, cy); !samePaneColor(bg, sel) {
		t.Errorf("a selected cell has bg %v, want the selection's %v", bg, sel)
	}
	if bg, _ := cellColors(t, canvas, cx+20, cy); !samePaneColor(bg, paneBgRGBA) {
		t.Errorf("an unselected cell has bg %v, want the pane's %v", bg, paneBgRGBA)
	}
}

// A lone fullscreen pane skips the compositor, and keeps skipping it with the
// option on: the fast path paints the ground into its own frame.
func TestPaneBackgroundOnTheFullscreenFastPath(t *testing.T) {
	withTheme(t, "")
	m := keystrokeOS(t, 1, 80, 24)
	m.Settings.SidebarEnabled = false
	m.Settings.DockbarPosition = "hidden"
	win := m.Windows[0]
	win.X, win.Y, win.Width, win.Height = 0, m.GetTopMargin(), m.GetRenderWidth(), m.GetUsableHeight()
	win.MarkPositionDirty()
	if _, ok := m.fullscreenFastWindow(); !ok {
		t.Skip("the fixture does not qualify for the fast path, so this proves nothing")
	}
	if frame := m.composeFrame(); strings.Contains(frame, "48;2;18;52;86") {
		t.Fatal("the fast path painted a ground with the option off")
	}
	m.Settings.PaneBackground = paneBgHex
	if _, ok := m.fullscreenFastWindow(); !ok {
		t.Error("the fullscreen fast path stands down for a pane background")
	}
	win.MarkContentDirty()
	if frame := m.composeFrame(); !strings.Contains(frame, "48;2;18;52;86") {
		t.Error("the fast path's frame carries no pane background")
	}
}

// The thin scrollbar floats over the pane and replaces the cells under it, so
// on a painted pane it carries the paint, or it cuts a column of the host's
// background through the pane.
func TestPaneBackgroundUnderTheThinScrollbar(t *testing.T) {
	withTheme(t, "")
	win := paneBgWindow(t, "pbg-sb", 2, 2, 40, 10)
	for i := range 40 {
		win.WriteOutput(fmt.Appendf(nil, "\r\nline %d", i))
	}
	win.EnterCopyMode()
	win.CopyMode.ScrollOffset = 5
	win.ScrollbackOffset = 5
	win.MarkContentDirty()
	m := paneBgOS(t, paneBgHex, win)
	m.Settings.ScrollbarStyle = config.ScrollbarStyleThin
	m.Settings.HideScrollbar = false
	if !windowNeedsScrollbar(win, &m.Settings) {
		t.Fatal("no scrollbar in this fixture, so this proves nothing")
	}
	canvas := m.GetCanvas(false)
	x := scrollbarColumn(win)
	for y := win.Y + 1; y < win.Y+win.Height-1; y++ {
		if bg, _ := cellColors(t, canvas, x, y); !samePaneColor(bg, paneBgRGBA) {
			t.Errorf("scrollbar cell (%d,%d) has bg %v, want the pane's %v", x, y, bg, paneBgRGBA)
		}
	}
}

// The fake cursor is drawn in the cell's colours swapped. On a painted pane a
// cell with no colours of its own takes the pane's, so the block is the ink
// and the glyph in it is the ground, rather than a white block whatever the
// ground is.
func TestPaneBackgroundFakeCursorUsesTheGround(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	g := resolveGround(config.PaneBackgroundTheme, theme.CurrentThemeID())
	var scratch uv.Cell
	src := &uv.Cell{Content: "x", Width: 1}
	got := paneGroundCell(&scratch, src, g)
	if !samePaneColor(got.Style.Bg, g.bg) || !samePaneColor(got.Style.Fg, g.fg) {
		t.Errorf("cursor cell colours %v on %v, want %v on %v", got.Style.Fg, got.Style.Bg, g.fg, g.bg)
	}
	if !isNilColor(src.Style.Bg) {
		t.Error("the emulator's own cell was written to")
	}
	if paneGroundCell(&scratch, src, ground{}) != src {
		t.Error("with the option off the cursor cell was copied")
	}
}

// The dim carries an unfocused pane toward its own ground, and a painted
// ground is that pane's ground.
func TestPaneBackgroundIsTheDimGround(t *testing.T) {
	withTheme(t, "")
	m := paneBgOS(t, paneBgHex)
	if _, bg := m.paneDimGround(); !samePaneColor(bg, paneBgRGBA) {
		t.Errorf("dim ground %v, want the pane background %v", bg, paneBgRGBA)
	}
	m.Settings.PaneBackground = config.PaneBackgroundOff
	if _, bg := m.paneDimGround(); bg != nil {
		t.Errorf("with no theme and no pane background the dim ground is %v", bg)
	}
}

// The option off has to cost nothing per frame beyond a comparison: no
// allocation, and no map written.
func TestPaneBackgroundOffAllocatesNothing(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	m := paneBgOS(t, config.PaneBackgroundOff)
	if n := testing.AllocsPerRun(100, func() { _ = m.paneGround() }); n != 0 {
		t.Errorf("resolving an off pane background allocates %.0f times", n)
	}
	m.Settings.PaneBackground = paneBgHex
	_ = m.paneGround()
	if n := testing.AllocsPerRun(100, func() { _ = m.paneGround() }); n != 0 {
		t.Errorf("resolving a cached pane background allocates %.0f times", n)
	}
}
