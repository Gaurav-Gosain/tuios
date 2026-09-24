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
	"github.com/Gaurav-Gosain/tuios/internal/vt"
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

func TestPaneBackgroundPaintsDefaultCellsAndKeepsAppBackgrounds(t *testing.T) {
	withTheme(t, "")
	win := paneBgWindow(t, "pbg-1", 2, 2, 40, 10)
	m := paneBgOS(t, paneBgHex, win)
	canvas := m.GetCanvas(false)

	// Content starts inside the border: one column in, one row down.
	cx, cy := win.X+1, win.Y+1

	if bg, _ := cellColors(t, canvas, cx, cy); isNilColor(bg) || samePaneColor(bg, paneBgRGBA) {
		t.Errorf("the app's red background at (%d,%d) became %v; an app-set background has to win", cx, cy, bg)
	}
	// "RED" then a space then "plain": column 4 is the p.
	if bg, _ := cellColors(t, canvas, cx+4, cy); !samePaneColor(bg, paneBgRGBA) {
		t.Errorf("a default-background cell holding text has bg %v, want %v", bg, paneBgRGBA)
	}
	// Past the end of the text, and a row the program never wrote.
	if bg, _ := cellColors(t, canvas, cx+30, cy); !samePaneColor(bg, paneBgRGBA) {
		t.Errorf("a blank cell after the text has bg %v, want %v", bg, paneBgRGBA)
	}
	if bg, _ := cellColors(t, canvas, cx+5, cy+5); !samePaneColor(bg, paneBgRGBA) {
		t.Errorf("a blank row has bg %v, want %v", bg, paneBgRGBA)
	}
	// The border is chrome, and so is the title row: neither is painted.
	if bg, _ := cellColors(t, canvas, win.X, cy); !isNilColor(bg) {
		t.Errorf("the left border cell was painted %v", bg)
	}
	if bg, _ := cellColors(t, canvas, win.X+win.Width-1, cy+2); !isNilColor(bg) {
		t.Errorf("the right border cell was painted %v", bg)
	}
	if bg, _ := cellColors(t, canvas, cx+10, win.Y+win.Height-1); !isNilColor(bg) {
		t.Errorf("the bottom border cell was painted %v", bg)
	}
	// Outside the pane entirely.
	if bg, _ := cellColors(t, canvas, 70, 20); !isNilColor(bg) {
		t.Errorf("a cell outside every pane was painted %v", bg)
	}
	// With no theme, text in the default colour keeps the host's own.
	if _, fg := cellColors(t, canvas, cx+4, cy); !isNilColor(fg) {
		t.Errorf("with no theme the default foreground was replaced by %v", fg)
	}
}

func TestPaneBackgroundOffPaintsNothing(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	win := paneBgWindow(t, "pbg-off", 2, 2, 40, 10)
	m := paneBgOS(t, config.PaneBackgroundOff, win)
	canvas := m.GetCanvas(false)
	for _, pt := range [][2]int{{win.X + 5, win.Y + 1}, {win.X + 30, win.Y + 1}, {win.X + 5, win.Y + 6}} {
		if bg, _ := cellColors(t, canvas, pt[0], pt[1]); !isNilColor(bg) {
			t.Errorf("off painted (%d,%d) with %v", pt[0], pt[1], bg)
		}
	}
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

// A pane whose content did not change keeps its parsed cells from frame to
// frame. Switching the option has to reach it anyway, or the ground changes
// only on the panes that happen to print something next.
func TestPaneBackgroundChangeRepaintsACachedLayer(t *testing.T) {
	withTheme(t, "")
	win := paneBgWindow(t, "pbg-cache", 2, 2, 40, 10)
	m := paneBgOS(t, config.PaneBackgroundOff, win)
	x, y := win.X+20, win.Y+2

	if bg, _ := cellColors(t, m.GetCanvas(false), x, y); !isNilColor(bg) {
		t.Fatalf("off painted %v", bg)
	}
	// No dirty marking at all: the layer and its string are reused as they
	// are, which is the case the cellLayer key exists for.
	m.Settings.PaneBackground = paneBgHex
	if bg, _ := cellColors(t, m.GetCanvas(false), x, y); !samePaneColor(bg, paneBgRGBA) {
		t.Errorf("after switching on, a cached pane has bg %v, want %v", bg, paneBgRGBA)
	}
	m.Settings.PaneBackground = "#654321"
	if bg, _ := cellColors(t, m.GetCanvas(false), x, y); !samePaneColor(bg, color.RGBA{R: 0x65, G: 0x43, B: 0x21, A: 0xff}) {
		t.Errorf("after a colour change, a cached pane has bg %v", bg)
	}
	m.Settings.PaneBackground = config.PaneBackgroundOff
	if bg, _ := cellColors(t, m.GetCanvas(false), x, y); !isNilColor(bg) {
		t.Errorf("after switching off, a cached pane kept %v", bg)
	}
}

// A lone fullscreen pane normally skips the compositor. The paint lives in the
// compositor, so that pane has to come back to it while the option is on.
func TestPaneBackgroundLeavesTheFullscreenFastPath(t *testing.T) {
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
	if _, ok := m.fullscreenFastWindow(); ok {
		t.Error("the fullscreen fast path is still taken with a pane background on")
	}
	win.MarkContentDirty()
	if frame := m.composeFrame(); !strings.Contains(frame, "48;2;18;52;86") {
		t.Error("the composed frame carries no pane background")
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

// The option has a row on the Backgrounds tab, drawn as a colour row so it
// opens the picker, and a value chosen there reaches the frame.
func TestPaneBackgroundSettingsRow(t *testing.T) {
	withTheme(t, "")
	win := paneBgWindow(t, "pbg-row", 2, 2, 40, 10)
	m := paneBgOS(t, "", win)
	m.UserConfig = config.DefaultConfig()

	var row *settingItem
	for _, cat := range m.settingsCategories() {
		for i := range cat.Items {
			if cat.Items[i].Path == "appearance.pane_background" {
				if cat.Name != "Backgrounds" {
					t.Errorf("the row is on the %s tab, want Backgrounds", cat.Name)
				}
				row = &cat.Items[i]
			}
		}
	}
	if row == nil {
		t.Fatal("the settings page has no pane background row")
	}
	if row.Control != controlColor || row.activate == nil {
		t.Errorf("the row is not a colour row that opens the picker: %+v", row)
	}
	// Unset on a default config, which follows the All surfaces row.
	if got := row.value(m); got != backgroundFollowsAll {
		t.Errorf("the row reads %q on a default config, want %q", got, backgroundFollowsAll)
	}

	_ = m.setColorOption("appearance.pane_background", paneBgHex)
	if m.Settings.PaneBackground != paneBgHex || m.UserConfig.Appearance.PaneBackground != paneBgHex {
		t.Fatalf("setting from the row left settings %q and config %q",
			m.Settings.PaneBackground, m.UserConfig.Appearance.PaneBackground)
	}
	if got := row.value(m); got != paneBgHex {
		t.Errorf("the row reads %q after the change, want %s", got, paneBgHex)
	}
	if bg, _ := cellColors(t, m.GetCanvas(false), win.X+20, win.Y+2); !samePaneColor(bg, paneBgRGBA) {
		t.Errorf("the frame after the change has bg %v, want %v", bg, paneBgRGBA)
	}
	if got := m.setColorOption("appearance.pane_background", "navy"); got != nil || m.Settings.PaneBackground != paneBgHex {
		t.Errorf("a value that is not a colour was applied: %q", m.Settings.PaneBackground)
	}
}

// TestPaneBackgroundBackend names the backend these ran on, so a -tags ghostty
// run says in its own output that it covered libghostty-vt.
func TestPaneBackgroundBackend(t *testing.T) {
	t.Logf("pane background tests ran on the %s VT backend", vt.Backend)
}
