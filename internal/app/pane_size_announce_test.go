package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// These tests pin the pane size invariant: the width and height the guest is
// told (the PTY winsize, captured from DaemonResizeFunc and mirrored by
// AnnouncedSize) must equal the emulator's own grid and the drawable box the
// renderer clips to (ContentWidth/ContentHeight). A guest told one more column
// than it can draw in wraps its prompt; told one less, it letterboxes. The
// invariant is checked across every bordered/borderless, sidebar, and zoom
// combination, and again after each layout transition.

// toldSize is the last size the fake daemon PTY was told, standing in for the
// real PTY's winsize.
type toldSize struct {
	w, h  int
	calls int
}

func newAnnounceWindow(t testing.TB, id string, w, h int) (*terminal.Window, *toldSize) {
	t.Helper()
	win := newTestWindow(t, id, w, h)
	told := &toldSize{}
	win.DaemonResizeFunc = func(rw, rh int) error {
		told.w, told.h = rw, rh
		told.calls++
		return nil
	}
	return win, told
}

// checkPaneSizes asserts the three-way invariant for every visible pane.
func checkPaneSizes(t *testing.T, m *OS, told map[string]*toldSize, label string) {
	t.Helper()
	for _, win := range m.Windows {
		if win.Workspace != m.CurrentWorkspace || win.Minimized {
			continue
		}
		cw, ch := win.ContentWidth(), win.ContentHeight()
		ew, eh := win.Terminal.Width(), win.Terminal.Height()
		aw, ah := win.AnnouncedSize()
		if ew != cw || eh != ch {
			t.Errorf("%s: %s emulator %dx%d, drawable %dx%d", label, win.ID, ew, eh, cw, ch)
		}
		if aw != cw || ah != ch {
			t.Errorf("%s: %s announced %dx%d, drawable %dx%d", label, win.ID, aw, ah, cw, ch)
		}
		if rec := told[win.ID]; rec != nil && rec.calls > 0 && (rec.w != aw || rec.h != ah) {
			t.Errorf("%s: %s PTY told %dx%d, announce record %dx%d", label, win.ID, rec.w, rec.h, aw, ah)
		}
	}
}

// newAnnounceOS builds a client with two tiled daemon panes on workspace 1.
func newAnnounceOS(t *testing.T, width, height int) (*OS, map[string]*toldSize) {
	t.Helper()
	m := &OS{
		NumWorkspaces:        9,
		CurrentWorkspace:     1,
		WorkspaceFocus:       make(map[int]int),
		WorkspaceLayouts:     make(map[int][]WindowLayout),
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceMasterRatio: map[int]float64{},
		Width:                width,
		Height:               height,
		AutoTiling:           true,
		UseBSPLayout:         true,
		PendingResizes:       make(map[string][2]int),
	}
	told := make(map[string]*toldSize)
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("announce-win-%d", i+1)
		win, rec := newAnnounceWindow(t, id, 60, 20)
		win.Workspace = 1
		told[id] = rec
		m.Windows = append(m.Windows, win)
	}
	m.FocusedWindow = 0
	return m, told
}

// TestNewWindowInNewWorkspaceAnnouncesItsSize drives the daemon window path: a
// client on a fresh workspace asks for a window, the daemon pushes it Unplaced
// at the session's nominal box, and the client places and tiles it. The size
// the guest is told must be the size the tile gives it, not the nominal box.
func TestNewWindowInNewWorkspaceAnnouncesItsSize(t *testing.T) {
	prevAnim := config.AnimationsEnabled
	prevShared := config.SharedBorders
	config.AnimationsEnabled = false
	config.SharedBorders = true
	t.Cleanup(func() {
		config.AnimationsEnabled = prevAnim
		config.SharedBorders = prevShared
	})

	const width, height = 200, 50
	ptyDataChan := make(chan struct{}, 1)
	drainDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-ptyDataChan:
			case <-drainDone:
				return
			}
		}
	}()
	t.Cleanup(func() { close(drainDone) })

	m := &OS{
		NumWorkspaces:        9,
		CurrentWorkspace:     1,
		WorkspaceFocus:       make(map[int]int),
		WorkspaceLayouts:     make(map[int][]WindowLayout),
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceMasterRatio: map[int]float64{},
		Width:                width,
		Height:               height,
		AutoTiling:           true,
		UseBSPLayout:         true,
		PendingResizes:       make(map[string][2]int),
		PTYDataChan:          ptyDataChan,
	}
	t.Cleanup(func() {
		for _, w := range m.Windows {
			w.Close()
		}
	})

	daemonState := &session.SessionState{
		Name:             "fresh-workspace",
		CurrentWorkspace: 1,
		AutoTiling:       true,
		WorkspaceFocus:   map[int]string{},
		Version:          1,
	}
	push := func(ws int) {
		id := fmt.Sprintf("ws%d-win-%036d", ws, len(daemonState.Windows)+1)
		daemonState.Windows = append(daemonState.Windows, session.WindowState{
			ID:        id,
			PTYID:     fmt.Sprintf("pty-%d", len(daemonState.Windows)+1),
			Title:     id,
			Width:     width,
			Height:    height,
			Workspace: ws,
			Unplaced:  true,
		})
		daemonState.FocusedWindowID = id
		daemonState.CurrentWorkspace = ws
		daemonState.Version++
		if err := m.ApplyStateSync(daemonState); err != nil {
			t.Fatalf("ApplyStateSync: %v", err)
		}
	}

	push(1)
	m.SwitchToWorkspace(2)
	push(2)

	told := map[string]*toldSize{}
	checkPaneSizes(t, m, told, "new-window-in-new-workspace")

	// The lone window on the fresh workspace owns the whole content box, no
	// more and no less: still Unplaced-sized means placement never ran.
	bounds := m.GetBSPBounds()
	for _, w := range m.Windows {
		if w.Workspace != 2 {
			continue
		}
		if w.X != bounds.X || w.Y != bounds.Y || w.Width != bounds.W || w.Height != bounds.H {
			t.Errorf("workspace 2 window at (%d,%d) %dx%d, want the content box (%d,%d) %dx%d",
				w.X, w.Y, w.Width, w.Height, bounds.X, bounds.Y, bounds.W, bounds.H)
		}
	}
}

func TestAnnouncedSizeMatchesDrawable(t *testing.T) {
	prevAnim := config.AnimationsEnabled
	prevShared := config.SharedBorders
	prevSidebarEnabled := config.SidebarEnabled
	prevSidebarPos := config.SidebarPosition
	config.AnimationsEnabled = false
	t.Cleanup(func() {
		config.AnimationsEnabled = prevAnim
		config.SharedBorders = prevShared
		config.SidebarEnabled = prevSidebarEnabled
		config.SidebarPosition = prevSidebarPos
	})

	for _, bsp := range []bool{true, false} {
		for _, shared := range []bool{false, true} {
			for _, sidebar := range []string{"off", "left", "right"} {
				name := fmt.Sprintf("bsp=%v/shared=%v/sidebar=%s", bsp, shared, sidebar)
				t.Run(name, func(t *testing.T) {
					config.SharedBorders = shared
					config.SidebarEnabled = sidebar != "off"
					if sidebar != "off" {
						config.SidebarPosition = sidebar
					}

					m, told := newAnnounceOS(t, 200, 50)
					m.UseBSPLayout = bsp
					m.TileAllWindows()
					checkPaneSizes(t, m, told, name+"/tiled")

					m.ToggleZoom()
					checkPaneSizes(t, m, told, name+"/zoomed")
					m.ToggleZoom()
					checkPaneSizes(t, m, told, name+"/unzoomed")

					m.SwitchToWorkspace(2)
					m.SwitchToWorkspace(1)
					checkPaneSizes(t, m, told, name+"/workspace-roundtrip")

					m.ToggleAutoTiling()
					checkPaneSizes(t, m, told, name+"/tiling-off")
					m.ToggleAutoTiling()
					checkPaneSizes(t, m, told, name+"/tiling-on")

					config.SharedBorders = !shared
					m.TileAllWindows()
					checkPaneSizes(t, m, told, name+"/shared-toggled")
					config.SharedBorders = shared
					m.TileAllWindows()
					checkPaneSizes(t, m, told, name+"/shared-restored")

					// A border drag defers the PTY half of each resize; the drain
					// on release must land every deferred announcement.
					win := m.Windows[0]
					win.ResizeVisual(win.Width-3, win.Height-2)
					m.PendingResizes[win.ID] = [2]int{win.Width, win.Height}
					m.ApplyPendingResizes()
					checkPaneSizes(t, m, told, name+"/drag-drained")
					m.TileAllWindows()
					checkPaneSizes(t, m, told, name+"/drag-retiled")

					// The viewport itself resizes: a one-column change leaves the
					// master pane's tile size untouched, which is exactly the pane
					// whose announcement used to go stale.
					m.Width++
					m.TileAllWindows()
					m.SyncDaemonPTYDimensions()
					checkPaneSizes(t, m, told, name+"/viewport-grown")

					// A scrolled-back pane shows the scrollbar thumb. It is an
					// overlay in the pane's last content column: no size may move,
					// and the column must lie inside the guest's own box.
					win = m.Windows[0]
					win.LockIO()
					for range 200 {
						_, _ = win.Terminal.Write([]byte("scrollback line\r\n"))
					}
					win.UnlockIO()
					win.EnterCopyMode()
					win.CopyMode.ScrollOffset = 10
					if !windowNeedsScrollbar(win) {
						t.Fatalf("%s: scrolled-back pane shows no scrollbar", name)
					}
					checkPaneSizes(t, m, told, name+"/scrollbar-shown")
					col := scrollbarColumn(win)
					lo := win.X + win.BorderOffset()
					if col < lo || col >= lo+win.ContentWidth() {
						t.Errorf("%s: scrollbar column %d outside content [%d,%d)",
							name, col, lo, lo+win.ContentWidth())
					}
					win.ExitCopyMode()
					checkPaneSizes(t, m, told, name+"/scrollbar-hidden")

					// Reattach: the daemon PTY already carries the announced size,
					// so the dimension sync must both agree and stay silent.
					for _, w := range m.Windows {
						w.SeedAnnouncedSize(w.ContentWidth(), w.ContentHeight())
					}
					calls := 0
					for _, rec := range told {
						calls += rec.calls
					}
					m.SyncDaemonPTYDimensions()
					checkPaneSizes(t, m, told, name+"/reattach-synced")
					after := 0
					for _, rec := range told {
						after += rec.calls
					}
					if after != calls {
						t.Errorf("%s: same-size reattach issued %d PTY resizes, want 0", name, after-calls)
					}
				})
			}
		}
	}
}
