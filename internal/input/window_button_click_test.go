package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

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

func controlCells(o *OS2, win *terminal.Window) map[int]app.WindowButtonAction {
	cells := map[int]app.WindowButtonAction{}
	for x := win.X; x < win.X+win.Width; x++ {
		if action, ok := o.WindowButtonIn(win.ID, x, win.Y); ok {
			cells[x] = action
		}
	}
	return cells
}

type pressOutcome struct {
	closed, minimized, moved bool
}

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
	prevAnim := config.Global.Motion
	config.Global.Motion = config.MotionNone
	t.Cleanup(func() { config.Global.Motion = prevAnim })

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
