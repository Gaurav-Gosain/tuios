package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// testPane is a window with a real emulator.
func testPane(t *testing.T, id string, x, y, w, h, ws int) *terminal.Window {
	t.Helper()
	em := vt.NewEmulator(w-2, h-2)
	t.Cleanup(func() { _ = em.Close() })
	return &terminal.Window{ID: id, Terminal: em, X: x, Y: y, Width: w, Height: h, Workspace: ws}
}

// testOS is a local client of 100x42 with the given windows, focus on the
// first, in window mode.
func testOS(wins ...*terminal.Window) *app.OS {
	return &app.OS{
		Settings:             config.Global,
		Mode:                 app.WindowManagementMode,
		Windows:              wins,
		FocusedWindow:        0,
		CurrentWorkspace:     1,
		NumWorkspaces:        9,
		WorkspaceFocus:       map[int]int{},
		WorkspaceLayouts:     map[int][]app.WindowLayout{},
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceMasterRatio: map[int]float64{},
		WorkspaceStackRatio:  map[int]float64{},
		PendingResizes:       map[string][2]int{},
		Width:                100,
		Height:               42,
	}
}

// scratchOverTile is one pane on workspace 1 with a scratch group of two
// panes shown over it.
func scratchOverTile(t *testing.T) (*app.OS, *terminal.Window, *terminal.Window, *terminal.Window) {
	t.Helper()
	tile := testPane(t, "tile", 0, 0, 100, 40, 1)
	ws := session.ScratchWorkspaceBase
	s1 := testPane(t, "s1", 0, 0, 40, 20, ws)
	s2 := testPane(t, "s2", 0, 0, 40, 20, ws)
	s1.IsScratch, s2.IsScratch = true, true
	o := testOS(tile, s1, s2)
	o.FocusWindow(1)
	if !o.InScratchView() {
		t.Fatal("the fixture did not show the group")
	}
	// The two panes side by side inside the box, as the tiler puts them.
	box, _ := o.ScratchBox()
	half := (box.W - 2) / 2
	s1.X, s1.Y, s1.Width, s1.Height = box.X+1, box.Y+1, half, box.H-2
	s2.X, s2.Y, s2.Width, s2.Height = box.X+1+half, box.Y+1, box.W-2-half, box.H-2
	return o, tile, s1, s2
}

// A press on a popup hits the popup, the pane the frame draws on top, even
// when a tile under it has a higher raw Z.
//
// Negative control, confirmed red: compare the raw Z in findClickedWindow and
// the press lands on the tile under the popup.
func TestHitTestPicksTheDrawnPopup(t *testing.T) {
	tile := testPane(t, "tile", 0, 0, 100, 40, 1)
	tile.Tiled, tile.Z = true, 5
	popup := testPane(t, "popup", 20, 5, 60, 20, 1)
	popup.IsPopup, popup.IsFloating, popup.Z = true, true, 0
	o := testOS(tile, popup)
	if got := findClickedWindow(40, 12, o); got != 1 {
		t.Fatalf("a press on the popup hit window %d, want the popup (1)", got)
	}
}

// A press outside the scratch box hides the group and focuses the pane under
// the pointer on the workspace it was shown over. The press does nothing
// else: no drag starts.
func TestPressOutsideScratchHidesIt(t *testing.T) {
	o, tile, _, _ := scratchOverTile(t)
	box, ok := o.ScratchBox()
	if !ok {
		t.Fatal("no box")
	}
	handleMouseClick(tea.MouseClickMsg{Button: tea.MouseLeft, X: 1, Y: box.Y + box.H + 1}, o)
	if o.InScratchView() || o.GetFocusedWindow() != tile {
		t.Fatalf("view=%v focused tile=%v", o.InScratchView(), o.GetFocusedWindow() == tile)
	}
	if o.Dragging || o.Resizing || o.BorderResizing {
		t.Fatal("the press that hid the group also started a gesture")
	}
}

// A press on the frame of the box does nothing: it is not a pane.
func TestPressOnTheScratchFrameDoesNothing(t *testing.T) {
	o, _, s1, _ := scratchOverTile(t)
	box, _ := o.ScratchBox()
	handleMouseClick(tea.MouseClickMsg{Button: tea.MouseLeft, X: box.X, Y: box.Y + 3}, o)
	if !o.InScratchView() || o.GetFocusedWindow() != s1 || o.Dragging || o.Resizing {
		t.Fatal("a press on the frame changed something")
	}
}

// Window keys act inside the group: zoom zooms the scratch pane, and the tile
// under the group is untouched.
func TestWindowKeysActInsideTheGroup(t *testing.T) {
	o, tile, s1, _ := scratchOverTile(t)
	GetDispatcher().Dispatch("toggle_zoom", tea.KeyPressMsg{}, o)
	if !s1.Zoomed || tile.Zoomed || !o.InScratchView() {
		t.Fatalf("scratch zoomed=%v tile zoomed=%v view=%v", s1.Zoomed, tile.Zoomed, o.InScratchView())
	}
	GetDispatcher().Dispatch("minimize_window", tea.KeyPressMsg{}, o)
	if s1.Minimized || !o.InScratchView() {
		t.Fatal("minimize acted on a scratch pane or hid the group")
	}
}

// A hover outside the box does not take the focus out of the group: the
// backdrop is not the current workspace.
func TestHoverKeepsScratchShown(t *testing.T) {
	o, _, s1, _ := scratchOverTile(t)
	o.Settings.FocusFollowsMouse = true
	o.Mode = app.TerminalMode
	box, _ := o.ScratchBox()
	for x := 1; x < 10; x++ {
		handleMouseMotion(tea.MouseMotionMsg{Button: tea.MouseNone, X: x, Y: box.Y + box.H + 1}, o)
	}
	if !o.InScratchView() || o.GetFocusedWindow() != s1 {
		t.Fatal("a hover outside the box moved the focus out of the group")
	}
}

// Alt+N numbers the panes on the screen: the group's inside the group, the
// workspace's outside it.
func TestSelectByNumberInsideTheGroup(t *testing.T) {
	o, tile, _, s2 := scratchOverTile(t)
	selectWindowByIndex(2, o)
	if o.GetFocusedWindow() != s2 {
		t.Fatal("number 2 inside the group did not select its second pane")
	}
	o.HideShownScratch()
	selectWindowByIndex(1, o)
	if o.GetFocusedWindow() != tile {
		t.Fatal("number 1 outside the group did not select the tile")
	}
}
