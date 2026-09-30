package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// scratchOverTile is one tiled pane of 100x40 with the scratch terminal shown
// over it at 20,5, 60x20, focused. The tile's raw Z is above the popup's, as
// a daemon-made popup arrives: the frame still draws the popup on top.
func scratchOverTile(t *testing.T) (*app.OS, *terminal.Window, *terminal.Window) {
	t.Helper()
	pane := func(id string, x, y, w, h int) *terminal.Window {
		em := vt.NewEmulator(w-2, h-2)
		t.Cleanup(func() { _ = em.Close() })
		return &terminal.Window{ID: id, Terminal: em, X: x, Y: y, Width: w, Height: h, Workspace: 1}
	}
	tile := pane("tile", 0, 0, 100, 40)
	tile.Tiled, tile.Z = true, 5
	scratch := pane("scratch", 20, 5, 60, 20)
	scratch.IsPopup, scratch.IsScratch, scratch.IsFloating, scratch.Z = true, true, true, 0
	o := &app.OS{
		Settings:         config.Global,
		Mode:             app.WindowManagementMode,
		Windows:          []*terminal.Window{tile, scratch},
		FocusedWindow:    1,
		CurrentWorkspace: 1,
		NumWorkspaces:    9,
		WorkspaceFocus:   map[int]int{},
		AutoTiling:       true,
		Width:            100,
		Height:           42,
	}
	return o, tile, scratch
}

// A press on the popup hits the popup, the pane the frame draws on top.
//
// Negative control, confirmed red: compare the raw Z in findClickedWindow and
// the press lands on the tile under the popup.
func TestHitTestPicksTheDrawnPopup(t *testing.T) {
	o, _, _ := scratchOverTile(t)
	if got := findClickedWindow(40, 12, o); got != 1 {
		t.Fatalf("a press on the popup hit window %d, want the popup (1)", got)
	}
}

// Neither button moves or resizes the scratch terminal, on its content or on
// its border.
func TestMouseNeverMovesOrResizesTheScratch(t *testing.T) {
	for _, c := range []struct {
		name   string
		button tea.MouseButton
		x, y   int
	}{
		{"right on content", tea.MouseRight, 40, 12},
		{"left on border", tea.MouseLeft, 20, 12},
		{"left on title", tea.MouseLeft, 50, 5},
		{"left on corner", tea.MouseLeft, 79, 24},
	} {
		t.Run(c.name, func(t *testing.T) {
			o, _, s := scratchOverTile(t)
			handleMouseClick(tea.MouseClickMsg{Button: c.button, X: c.x, Y: c.y}, o)
			for i := 1; i <= 15; i++ {
				handleMouseMotion(tea.MouseMotionMsg{Button: c.button, X: c.x - i, Y: c.y + i/2}, o)
			}
			handleMouseRelease(tea.MouseReleaseMsg{Button: c.button, X: c.x - 15, Y: c.y + 7}, o)
			if s.X != 20 || s.Y != 5 || s.Width != 60 || s.Height != 20 || s.Minimized {
				t.Fatalf("scratch box = %d,%d %dx%d minimized=%v, want 20,5 60x20 shown",
					s.X, s.Y, s.Width, s.Height, s.Minimized)
			}
		})
	}
}

// A press outside the popup hides it and focuses the pane under the pointer.
// The press does nothing else: no drag starts.
func TestPressOutsideScratchHidesIt(t *testing.T) {
	o, tile, s := scratchOverTile(t)
	handleMouseClick(tea.MouseClickMsg{Button: tea.MouseLeft, X: 5, Y: 30}, o)
	if !s.Minimized || o.GetFocusedWindow() != tile {
		t.Fatalf("minimized=%v focused tile=%v", s.Minimized, o.GetFocusedWindow() == tile)
	}
	if o.Dragging || o.Resizing || o.BorderResizing {
		t.Fatal("the press that hid the popup also started a gesture")
	}
}

// A layout key hides the scratch terminal first and acts on the pane that
// gets the focus back.
func TestLayoutActionHidesScratchFirst(t *testing.T) {
	o, tile, s := scratchOverTile(t)
	GetDispatcher().Dispatch("toggle_zoom", tea.KeyPressMsg{}, o)
	if !s.Minimized || s.Zoomed {
		t.Fatalf("minimized=%v zoomed=%v, want hidden and not zoomed", s.Minimized, s.Zoomed)
	}
	if o.GetFocusedWindow() != tile {
		t.Fatal("the focus did not go back to the tile")
	}
}
