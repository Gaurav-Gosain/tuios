package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// withSidebarGlobals turns the sidebar on for a test and restores the previous
// configuration afterwards.
func withSidebarGlobals(t *testing.T, pos string) {
	t.Helper()
	pe, pp, pw := config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth
	config.Global.SidebarEnabled = true
	config.Global.SidebarPosition = pos
	config.Global.SidebarWidth = config.SidebarDefaultWidth
	t.Cleanup(func() {
		config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth = pe, pp, pw
	})
}

// TestSidebarBandClickNeverReachesPane guards the routing order: a click inside
// the sidebar's reserved band belongs to the sidebar even when a pane's
// rectangle extends underneath it, so the pane must not be grabbed for a drag
// and window interaction state must stay clean.
func TestSidebarBandClickNeverReachesPane(t *testing.T) {
	withSidebarGlobals(t, "left")

	win := &terminal.Window{ID: "w1", X: 0, Y: 0, Width: 120, Height: 39, Workspace: 1}
	o := &app.OS{
		Settings:         config.Global,
		Width:            120,
		Height:           40,
		Mode:             app.WindowManagementMode,
		CurrentWorkspace: 1,
		WorkspaceFocus:   map[int]int{},
		Windows:          []*terminal.Window{win},
		FocusedWindow:    0,
	}

	// Click well inside the band (x < sidebar width) over the pane's rectangle.
	handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: 5, Y: 10, Button: tea.MouseLeft}), o)

	if o.Dragging || o.Resizing || o.InteractionMode {
		t.Errorf("sidebar band click leaked to the pane: dragging=%v resizing=%v interaction=%v",
			o.Dragging, o.Resizing, o.InteractionMode)
	}

	// The same click just past the band is the pane's and starts a drag.
	handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: config.SidebarDefaultWidth + 2, Y: 10, Button: tea.MouseLeft}), o)
	if !o.Dragging {
		t.Errorf("click outside the band did not reach the pane")
	}
}
