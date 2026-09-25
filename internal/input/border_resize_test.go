package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// twoPaneBSP builds a tiled BSP workspace of two daemon panes split vertically,
// with shared borders on so a separator cell sits between them.
func twoPaneBSP(t *testing.T) (*OS2, *terminal.Window, *terminal.Window) {
	t.Helper()
	const cols, rows = 120, 40

	m := &app.OS{
		Settings:         config.Global,
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            cols,
		Height:           rows,
		AutoTiling:       true,
		UseBSPLayout:     true,
		// The layout reads the model's session-settled value, not the global;
		// seed it from the global the caller just set. NewOS does the same.
		SharedBorders:        config.Global.SharedBorders,
		FocusedWindow:        0,
		PendingResizes:       make(map[string][2]int),
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceLayouts:     map[int][]app.WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
	}
	for i := range 2 {
		id := fmt.Sprintf("border-%d", i)
		ptyData := make(chan struct{}, 1)
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-ptyData:
				case <-done:
					return
				}
			}
		}()
		t.Cleanup(func() { close(done) })
		win := terminal.NewDaemonWindow(id, "test", 0, 0, cols, rows, 0, "pty-"+id, ptyData, config.DefaultScrollbackLines)
		if win == nil {
			t.Fatal("NewDaemonWindow returned nil")
		}
		t.Cleanup(win.Close)
		win.Workspace = 1
		win.Tiled = true
		m.Windows = append(m.Windows, win)
	}
	m.TileAllWindows()
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		t.Fatal("no BSP tree")
	}
	for intID, rect := range tree.ApplyLayout(m.GetBSPBounds(), sharedGap()) {
		if win := m.GetWindowByIntID(intID); win != nil {
			win.X, win.Y, win.Width, win.Height = rect.X, rect.Y, rect.W, rect.H
		}
	}
	return (*OS2)(m), m.Windows[0], m.Windows[1]
}

// OS2 is app.OS under a local alias so the test can pass it to the unexported
// handlers without importing app cosmetics.
type OS2 = app.OS

func leftPaneOf(a, b *terminal.Window) (left, right *terminal.Window) {
	if a.X < b.X {
		return a, b
	}
	return b, a
}

func clickMsg(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}
}
func motionMsg(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{Button: tea.MouseLeft, X: x, Y: y}
}
func releaseMsg(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x, Y: y}
}
