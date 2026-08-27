package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The window controls are pressable only on the cells the renderer recorded as
// it drew them, and appearance.window_button_position moves where that is. What
// these pin is that the move carries all the way through: on either end, every
// cell a control was drawn on runs that control's action and no other's, and
// the corner glyphs the pill sits next to still run nothing.
//
// Driven through handleMouseClick rather than the hit test alone, because the
// bug this class of test exists for was the handler and the renderer disagreeing
// about a column.

// floatingPane composes a frame holding one floating pane, so all three
// controls are drawn: a tiled pane has no zoom.
func floatingPane(t *testing.T) (*OS2, *terminal.Window) {
	t.Helper()
	const cols, rows = 120, 40

	m := &app.OS{
		Settings:             config.Global,
		NumWorkspaces:        9,
		CurrentWorkspace:     1,
		WorkspaceFocus:       make(map[int]int),
		Width:                cols,
		Height:               rows,
		FocusedWindow:        0,
		PendingResizes:       make(map[string][2]int),
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceLayouts:     map[int][]app.WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
	}

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

	win := terminal.NewDaemonWindow("float", "test", 6, 3, 70, 18, 0, "pty-float", ptyData, config.DefaultScrollbackLines)
	if win == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	t.Cleanup(win.Close)
	win.Workspace = 1
	win.Tiled = false
	m.Windows = append(m.Windows, win)

	o := (*OS2)(m)
	o.GetCanvas(true)
	return o, win
}

// controlCells asks the frame which control it recorded on each cell of a
// window's title row. It reads the hit test rather than recomputing the layout,
// so it cannot agree with a renderer that got the columns wrong.
func controlCells(o *OS2, win *terminal.Window) map[int]app.WindowButtonAction {
	cells := map[int]app.WindowButtonAction{}
	for x := win.X; x < win.X+win.Width; x++ {
		if action, ok := o.WindowButtonIn(win.ID, x, win.Y); ok {
			cells[x] = action
		}
	}
	return cells
}

// pressOutcome is what one press did to the pane, in the three ways the three
// controls differ.
type pressOutcome struct {
	closed, minimized, moved bool
}

// pressControl composes a fresh frame, presses one cell of its title row, and
// reports what that did. Fresh each time because closing a pane is not undoable.
func pressControl(t *testing.T, x int) pressOutcome {
	t.Helper()
	o, win := floatingPane(t)
	before := [4]int{win.X, win.Y, win.Width, win.Height}

	handleMouseClick(clickMsg(x, win.Y), o)

	if len(o.Windows) == 0 {
		return pressOutcome{closed: true}
	}
	after := [4]int{win.X, win.Y, win.Width, win.Height}
	return pressOutcome{minimized: win.Minimized, moved: after != before}
}

func TestWindowControlsRunTheirOwnActionAtEitherEnd(t *testing.T) {
	// Snapping animates, and an animation would leave the pane mid-flight with
	// its geometry unchanged. With animations off the snap lands on the press,
	// which is what makes zoom tellable from the other two.
	prevAnim := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() { config.Global.AnimationsEnabled = prevAnim })

	prevStyle, prevPos := config.Global.WindowButtonStyle, config.Global.WindowButtonPosition
	t.Cleanup(func() {
		config.Global.WindowButtonStyle, config.Global.WindowButtonPosition = prevStyle, prevPos
	})

	want := map[app.WindowButtonAction]pressOutcome{
		app.WindowButtonClose:    {closed: true},
		app.WindowButtonMinimize: {minimized: true},
		app.WindowButtonZoom:     {moved: true},
	}

	for _, style := range config.WindowButtonStyles {
		for _, position := range config.WindowButtonPositions {
			t.Run(style+"/"+position, func(t *testing.T) {
				config.Global.WindowButtonStyle = style
				config.Global.WindowButtonPosition = position

				o, win := floatingPane(t)
				cells := controlCells(o, win)
				if len(cells) == 0 {
					t.Fatal("the composed frame recorded no controls to press")
				}

				seen := map[app.WindowButtonAction]bool{}
				for x, action := range cells {
					seen[action] = true
					if got := pressControl(t, x); got != want[action] {
						t.Errorf("column %d was drawn as %v, but pressing it gave %+v, want %+v",
							x, action, got, want[action])
					}
				}

				for action := range want {
					if !seen[action] {
						t.Errorf("a floating pane drew no %v control", action)
					}
				}
			})
		}
	}
}

// The corner glyph the pill sits next to is border, not a button. Pressing it
// used to close the window, and moving the pill to the leading corner puts the
// same trap on the other end.
func TestWindowCornersAreNotControls(t *testing.T) {
	prevStyle, prevPos := config.Global.WindowButtonStyle, config.Global.WindowButtonPosition
	t.Cleanup(func() {
		config.Global.WindowButtonStyle, config.Global.WindowButtonPosition = prevStyle, prevPos
	})

	for _, style := range config.WindowButtonStyles {
		for _, position := range config.WindowButtonPositions {
			t.Run(style+"/"+position, func(t *testing.T) {
				config.Global.WindowButtonStyle = style
				config.Global.WindowButtonPosition = position

				o, win := floatingPane(t)
				for _, x := range []int{win.X, win.X + win.Width - 1} {
					if action, ok := o.WindowButtonIn(win.ID, x, win.Y); ok {
						t.Errorf("the corner at column %d is hit-tested as %v", x, action)
					}
					handleMouseClick(clickMsg(x, win.Y), o)
					if len(o.Windows) != 1 || o.Windows[0].Minimized {
						t.Fatalf("pressing the corner at column %d closed or minimized the pane", x)
					}
				}
			})
		}
	}
}
