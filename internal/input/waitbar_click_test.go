package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// waitBarPanes builds a pane printing an OSC 8 link in its first content row
// and a second pane the link can name. params is the OSC 8 parameter string
// the link carries: the real wait bar passes app.TuiosLinkParams(), and the
// guest tests pass whatever a program printing its own link would. The link
// body is "jump", five spaces and the word, so column 4 (border-relative) is
// the run's first cell.
func waitBarPanes(t *testing.T, rawURL, params string) (*app.OS, *terminal.Window, *terminal.Window) {
	t.Helper()
	prev := config.Global.Links
	config.Global.Links = config.LinksAll
	t.Cleanup(func() { config.Global.Links = prev })

	windows := make([]*terminal.Window, 0, 2)
	for i, id := range []string{"pi-pane-0001", "watched-0001"} {
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
		win := terminal.NewDaemonWindow(id, "test", i*50, 0, 40, 10, 0, "pty-"+id, ptyData, config.DefaultScrollbackLines)
		if win == nil {
			t.Fatal("NewDaemonWindow returned nil")
		}
		t.Cleanup(func() { win.Close() })
		win.Workspace = 1
		windows = append(windows, win)
	}

	body := fmt.Sprintf("xxxx\x1b]8;%s;%s\x1b\\jump\x1b]8;;\x1b\\", params, rawURL)
	windows[0].WriteOutput([]byte(body))

	o := &app.OS{
		Settings:         config.Global,
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            120,
		Height:           40,
		FocusedWindow:    0,
		Windows:          windows,
	}
	return o, windows[0], windows[1]
}

// linkCell maps the marked run's first cell to the absolute screen position a
// mouse event carries, through the pane's border allowance.
func linkCell(win *terminal.Window) (int, int) {
	return win.X + win.BorderOffset() + 4, win.Y + win.BorderOffset()
}

// TestPlainClickOnTuiosLinkJumps proves the wait bar's click path: a plain
// left press on a tuios:// marked run that carries the client's mark acts on
// the link, landing focus on the pane the link names. The shift+click rule
// stays untouched for every other scheme.
func TestPlainClickOnTuiosLinkJumps(t *testing.T) {
	o, pi, watched := waitBarPanes(t, "tuios://window/watched-0001", app.TuiosLinkParams())
	x, y := linkCell(pi)

	HandleInput(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}, o)
	HandleInput(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x, Y: y}, o)

	if o.FocusedWindow != 1 {
		t.Fatalf("focus = %d, want the pane the link named", o.FocusedWindow)
	}
	if o.Windows[indexOf(o, watched)].ID != "watched-0001" {
		t.Fatal("sanity: the watched pane vanished")
	}
}

// TestPlainClickOnWebLinkDoesNotOpen keeps the shift rule for the desktop
// schemes: a plain press on an https link is a click on the pane, never an
// open. Nothing here can observe the desktop, so the assertion is the one
// thing a wrong carve-out would break: the click did not follow the link.
func TestPlainClickOnWebLinkDoesNotOpen(t *testing.T) {
	o, pi, _ := waitBarPanes(t, "https://example.com/docs", "")
	x, y := linkCell(pi)

	HandleInput(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}, o)
	HandleInput(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x, Y: y}, o)

	if o.FocusedWindow != 0 {
		t.Fatalf("focus = %d, want the pane that was clicked", o.FocusedWindow)
	}
}

// TestPlainClickOnAGuestsTuiosLinkDoesNotJump is the boundary Gaurav drew:
// OSC 8 lets any program print a tuios:// address, and the plain-click path
// runs before mouse forwarding, so an unmarked link must be the guest's own
// text and the click must stay with the pane.
func TestPlainClickOnAGuestsTuiosLinkDoesNotJump(t *testing.T) {
	o, pi, _ := waitBarPanes(t, "tuios://window/watched-0001", "")
	x, y := linkCell(pi)

	HandleInput(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}, o)
	HandleInput(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x, Y: y}, o)

	if o.FocusedWindow != 0 {
		t.Fatalf("focus = %d, want the pane that was clicked", o.FocusedWindow)
	}
}

// TestShiftClickOnTuiosLinkStillJumps checks the modifier path still resolves
// our scheme, so the two routes agree on what the link does. The link here is
// a guest's: holding shift is the user saying this click is the terminal's,
// which is exactly the case for acting on what a program printed.
func TestShiftClickOnTuiosLinkStillJumps(t *testing.T) {
	o, pi, _ := waitBarPanes(t, "tuios://window/watched-0001", "")
	x, y := linkCell(pi)

	HandleInput(tea.MouseClickMsg{Button: tea.MouseLeft, Mod: tea.ModShift, X: x, Y: y}, o)
	HandleInput(tea.MouseReleaseMsg{Button: tea.MouseLeft, Mod: tea.ModShift, X: x, Y: y}, o)

	if o.FocusedWindow != 1 {
		t.Fatalf("focus = %d, want the pane the link named", o.FocusedWindow)
	}
}

// TestJumpBackRestoresThePaneTheJumpLeft proves the undo: the click jumps to
// the pane the link names, and jump_back lands back on the pane the link was
// on. A second press finds the stack empty and says so.
func TestJumpBackRestoresThePaneTheJumpLeft(t *testing.T) {
	o, pi, _ := waitBarPanes(t, "tuios://window/watched-0001", app.TuiosLinkParams())
	x, y := linkCell(pi)

	HandleInput(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}, o)
	HandleInput(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x, Y: y}, o)
	if o.FocusedWindow != 1 {
		t.Fatalf("focus = %d, want the jump to have landed first", o.FocusedWindow)
	}

	if !o.JumpBack() {
		t.Fatal("JumpBack = false, want the origin the click left behind")
	}
	if o.FocusedWindow != 0 {
		t.Fatalf("focus = %d, want the pane the click started on", o.FocusedWindow)
	}

	if o.JumpBack() {
		t.Fatal("JumpBack = true twice, want the stack drained by the first press")
	}
}

// TestJumpBackDropsDeadEntries checks the walk past origins whose pane has
// closed: the stack holds where the user was, and a closed pane is where they
// were, but going back to it is going nowhere.
func TestJumpBackDropsDeadEntries(t *testing.T) {
	o, pi, _ := waitBarPanes(t, "tuios://window/watched-0001", app.TuiosLinkParams())
	x, y := linkCell(pi)

	HandleInput(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}, o)
	HandleInput(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x, Y: y}, o)

	// The origin is the pane the click started on. Closing it after the jump
	// leaves the stack holding nowhere to go.
	originIdx := indexOf(o, pi)
	o.Windows = append(o.Windows[:originIdx], o.Windows[originIdx+1:]...)
	if o.FocusedWindow >= len(o.Windows) {
		o.FocusedWindow = len(o.Windows) - 1
	}

	if o.JumpBack() {
		t.Fatal("JumpBack = true with no live origin, want false")
	}
}
