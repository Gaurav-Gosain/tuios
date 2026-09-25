package app

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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
