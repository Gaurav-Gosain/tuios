package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

const (
	benchCols = 207
	benchRows = 55
)

// benchResizeOS builds a tiled BSP workspace of n windows with painted content,
// already in the middle of a bottom-right resize drag on the focused window.
// This is the exact state the model is in between two motion events of a drag.
func benchResizeOS(tb testing.TB, n int) *app.OS {
	tb.Helper()

	m := &app.OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            benchCols,
		Height:           benchRows,
		AutoTiling:       true,
		UseBSPLayout:     true,
		FocusedWindow:    0,
		PendingResizes:   make(map[string][2]int),
	}

	for i := range n {
		id := fmt.Sprintf("bench-resize-%d-%d", n, i)
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
		tb.Cleanup(func() { close(done) })

		win := terminal.NewDaemonWindow(id, "test", 0, 0, benchCols, benchRows, 0, "pty-"+id, ptyData)
		if win == nil {
			tb.Fatal("NewDaemonWindow returned nil")
		}
		tb.Cleanup(win.Close)

		win.LockIO()
		for y := 1; y <= benchRows; y++ {
			line := fmt.Sprintf("line %03d ", y)
			for len(line) < benchCols-12 {
				line += "content "
			}
			_, _ = win.Terminal.Write(fmt.Appendf(nil, "\x1b[%d;1H\x1b[38;5;%dm%s\x1b[m", y, 16+(y%200), line))
		}
		win.UnlockIO()

		win.Workspace = 1
		win.Tiled = true
		m.Windows = append(m.Windows, win)
	}

	m.TileAllWindows()
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		tb.Fatal("benchResizeOS: no BSP tree")
	}
	for intID, rect := range tree.ApplyLayout(m.GetBSPBounds()) {
		if win := m.GetWindowByIntID(intID); win != nil {
			win.X, win.Y, win.Width, win.Height = rect.X, rect.Y, rect.W, rect.H
		}
	}

	// Arm a bottom-right resize drag on the focused window, the way
	// handleMouseClick would have on press.
	focused := m.GetFocusedWindow()
	m.Resizing = true
	m.ResizeCorner = app.BottomRight
	m.PreResizeState = terminal.Window{
		Width:  focused.Width,
		Height: focused.Height,
		X:      focused.X,
		Y:      focused.Y,
		Z:      focused.Z,
		ID:     focused.ID,
	}
	m.ResizeStartX = focused.X + focused.Width
	m.ResizeStartY = focused.Y + focused.Height
	m.InteractionMode = true

	return m
}

// motionAt is the message bubbletea delivers for one pointer move during a drag.
func motionAt(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg(uv.MouseMotionEvent{
		X:      x,
		Y:      y,
		Button: uv.MouseButton(tea.MouseLeft),
	})
}

// BenchmarkResizeMotionHandler measures just the Update side of one motion
// event during a tiling resize: the layout adjust plus, in shared-borders mode,
// the BSP ratio sync and layout reapply.
func BenchmarkResizeMotionHandler(b *testing.B) {
	for _, shared := range []bool{false, true} {
		for _, n := range []int{2, 4, 9} {
			name := fmt.Sprintf("windows-%d/shared-%v", n, shared)
			b.Run(name, func(b *testing.B) {
				prev := config.SharedBorders
				config.SharedBorders = shared
				b.Cleanup(func() { config.SharedBorders = prev })

				m := benchResizeOS(b, n)
				startX, startY := m.ResizeStartX, m.ResizeStartY

				b.ReportAllocs()
				b.ResetTimer()
				i := 0
				for b.Loop() {
					// Sweep the pointer back and forth by a cell so every event
					// is a real geometry change, not a no-op.
					dx := i%6 - 3
					_, _ = HandleInput(motionAt(startX+dx, startY), m)
					i++
				}
			})
		}
	}
}

// BenchmarkResizeMotionFrame measures the whole per-event cost the user
// actually pays, through the real loop: Update, which is where motion
// coalescing lives, followed by the View bubbletea runs after every Update.
func BenchmarkResizeMotionFrame(b *testing.B) {
	app.SetInputHandler(HandleInput)

	for _, shared := range []bool{false, true} {
		for _, n := range []int{2, 4, 9} {
			name := fmt.Sprintf("windows-%d/shared-%v", n, shared)
			b.Run(name, func(b *testing.B) {
				prev := config.SharedBorders
				config.SharedBorders = shared
				b.Cleanup(func() { config.SharedBorders = prev })

				m := benchResizeOS(b, n)
				startX, startY := m.ResizeStartX, m.ResizeStartY

				b.ReportAllocs()
				b.ResetTimer()
				i := 0
				for b.Loop() {
					dx := i%6 - 3
					_, _ = m.Update(motionAt(startX+dx, startY))
					_ = m.View()
					i++
				}
			})
		}
	}
}
