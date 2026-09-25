package app

import (
	"image/color"
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

func samePaneColor(a, b color.Color) bool {
	if isNilColor(a) || isNilColor(b) {
		return isNilColor(a) && isNilColor(b)
	}
	return safeColorEquals(a, b)
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
