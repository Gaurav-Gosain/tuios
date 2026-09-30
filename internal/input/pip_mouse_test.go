package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A pane that tracks the mouse must not see the mouse over the
// picture-in-picture view drawn on top of it. Each case failed before the
// view took its events: the wheel scrolled the pane, hover reached it, and the
// release of a click on the view went to the pane the click jumped to.

// pipMouseOS is a client in terminal mode with a full-screen pane "under"
// that tracks all motion, and a pane "pinned" on workspace 2, also tracking
// the mouse, pinned and drawn in the bottom-right corner over "under". It
// returns the bytes each pane received and a cell inside the view.
func pipMouseOS(t *testing.T) (o *app.OS, under, pinned *[]byte, x, y int) {
	t.Helper()
	cfg := config.DefaultConfig()
	o = app.NewOS(app.OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	o.Width, o.Height = 120, 40
	mk := func(id string, ws int, got *[]byte) *terminal.Window {
		em := vt.NewEmulator(118, 36)
		t.Cleanup(func() { _ = em.Close() })
		// Any-event tracking with SGR encoding, as a TUI that wants hover does.
		_, _ = em.Write([]byte("\x1b[?1003h\x1b[?1006h"))
		return &terminal.Window{
			ID: id, Terminal: em, X: 0, Y: 0, Width: 120, Height: 38, Workspace: ws,
			DaemonMode:      true,
			DaemonWriteFunc: func(b []byte) error { *got = append(*got, b...); return nil },
		}
	}
	under, pinned = new([]byte), new([]byte)
	o.Windows = []*terminal.Window{mk("under", o.CurrentWorkspace, under), mk("pinned", o.CurrentWorkspace+1, pinned)}
	o.FocusedWindow = 0
	o.Mode = app.TerminalMode
	if err := o.PinPiP("pinned"); err != nil {
		t.Fatal(err)
	}
	o.GetCanvas(true)
	for y = 39; y >= 0; y-- {
		for x = 119; x >= 0; x-- {
			if o.PiPAt(x, y) {
				// A cell well inside the box, not on its border.
				return o, under, pinned, x - 5, y - 3
			}
		}
	}
	t.Fatal("the view was not drawn")
	return
}

func TestPiPTakesTheWheel(t *testing.T) {
	o, under, _, x, y := pipMouseOS(t)
	handleMouseWheel(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp}, o)
	if len(*under) != 0 {
		t.Fatalf("the wheel over the view reached the pane under it: %q", *under)
	}
	// The same wheel beside the view does reach the pane, so the fixture can
	// see a leak at all.
	handleMouseWheel(tea.MouseWheelMsg{X: 10, Y: 10, Button: tea.MouseWheelUp}, o)
	if len(*under) == 0 {
		t.Fatal("the wheel beside the view did not reach the pane; the test cannot see a leak")
	}
}

func TestPiPTakesHover(t *testing.T) {
	o, under, _, x, y := pipMouseOS(t)
	handleMouseMotion(tea.MouseMotionMsg{X: x, Y: y}, o)
	if len(*under) != 0 {
		t.Fatalf("hover over the view reached the pane under it: %q", *under)
	}
	handleMouseMotion(tea.MouseMotionMsg{X: 10, Y: 10}, o)
	if len(*under) == 0 {
		t.Fatal("hover beside the view did not reach the pane; the test cannot see a leak")
	}
}

func TestPiPTakesTheReleaseOfItsClick(t *testing.T) {
	o, under, pinned, x, y := pipMouseOS(t)
	handleMouseClick(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}, o)
	if f := o.GetFocusedWindow(); f == nil || f.ID != "pinned" {
		t.Fatalf("the click on the view did not go to the pinned pane")
	}
	o.Mode = app.TerminalMode
	handleMouseRelease(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}, o)
	if len(*pinned) != 0 || len(*under) != 0 {
		t.Fatalf("the release of the click on the view reached a pane: pinned %q, under %q", *pinned, *under)
	}
	if o.PiPPressed {
		t.Fatal("the release did not end the view's press")
	}
}
