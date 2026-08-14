package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// These tests exist because the ones next door were not enough. Selection had
// its own tests, they drove the real mouse handlers, and they passed for months
// while copying a selection did nothing at all: every one of them read the
// selection back through extractVisualText, the function the selection is made
// with. Nothing asked the questions the rest of the program asks -- is there a
// selection to copy, and what does the copy action produce -- so the answer
// being "no" and "nothing" went unnoticed.
//
// So these drive the mouse and then go out through the consumers: the context
// menu's enablement, and the copy action reached through the dispatcher.

// clipboardText runs a command and returns the text it wrote to the clipboard,
// or "" when it wrote nothing. tea's set-clipboard message is an unexported
// string type, which is why this reads it as one rather than type-asserting.
func clipboardText(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		return ""
	}
	msg := cmd()
	if msg == nil {
		return ""
	}
	return fmt.Sprintf("%s", msg)
}

// copySelection runs the action the "Copy selection" row and the copy key both
// dispatch, and returns what reached the clipboard.
func copySelection(t *testing.T, o *app.OS) string {
	t.Helper()
	_, cmd := GetDispatcher().Dispatch("copy_selection", tea.KeyPressMsg{}, o)
	return clipboardText(t, cmd)
}

// copyRowDim reports whether the pane menu built for windowIndex offers its
// copy row greyed out. It opens the menu the way a right-click does.
func copyRowDim(t *testing.T, o *app.OS, x, y int) bool {
	t.Helper()
	o.OpenContextMenu(x, y)
	if o.ContextMenu == nil {
		t.Fatal("no context menu opened")
	}
	for _, it := range o.ContextMenu.Items {
		if it.Action == "copy_selection" {
			return it.Dim
		}
	}
	t.Fatal("pane menu has no copy row")
	return false
}

// TestMouseSelectionIsCopyable drives each selection gesture through the real
// mouse handlers and then asks for the text the way the user does. A gesture
// that highlights text but yields nothing to copy is the bug this pins.
func TestMouseSelectionIsCopyable(t *testing.T) {
	cases := []struct {
		name string
		do   func(o *app.OS)
		want string
	}{
		{
			"character drag",
			func(o *app.OS) { pressAt(o, 0, 0); dragTo(o, 10, 0); release(o, 10, 0) },
			"alpha bravo",
		},
		{
			"double-click word",
			func(o *app.OS) { pressAt(o, 7, 0); pressAt(o, 7, 0); release(o, 7, 0) },
			"bravo",
		},
		{
			"triple-click line",
			func(o *app.OS) { pressAt(o, 6, 0); pressAt(o, 6, 0); pressAt(o, 6, 0); release(o, 6, 0) },
			"alpha bravo charlie",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, win := selectPane(t, "alpha bravo charlie")

			if win.HasSelection() {
				t.Fatal("the pane reported a selection before anything was selected")
			}
			tc.do(o)

			if !win.HasSelection() {
				t.Error("the gesture highlighted text but the pane reports no selection, " +
					"so every consumer of it sees nothing")
			}
			if got := copySelection(t, o); got != tc.want {
				t.Errorf("the copy action produced %q, want %q", got, tc.want)
			}
		})
	}
}

// The copy action must stay quiet when there is nothing selected, so a stray
// key or a menu reached some other way cannot clear the clipboard.
func TestCopySelectionWithNothingSelectedCopiesNothing(t *testing.T) {
	o, _ := selectPane(t, "alpha bravo charlie")

	if got := copySelection(t, o); got != "" {
		t.Errorf("copying with no selection wrote %q to the clipboard", got)
	}
}

// TestPaneMenuOffersCopyAfterADrag closes the loop the screenshot showed: a
// selection plainly visible in the pane, and a "Copy selection" row greyed out
// over it.
func TestPaneMenuOffersCopyAfterADrag(t *testing.T) {
	o, _ := selectPane(t, "alpha bravo charlie")

	if !copyRowDim(t, o, 5, 5) {
		t.Error("copy is offered on a pane with no selection")
	}
	o.CloseContextMenu()

	pressAt(o, 0, 0)
	dragTo(o, 10, 0)
	release(o, 10, 0)

	if copyRowDim(t, o, 5, 5) {
		t.Error("copy is dimmed on a pane the user just drag-selected in")
	}
	// The row is not decoration: running what it carries has to produce the
	// selected text, so this dispatches the row's own action ID.
	o.CloseContextMenu()
	if got, want := copySelection(t, o), "alpha bravo"; got != want {
		t.Errorf("the menu's copy row produced %q, want %q", got, want)
	}
}

// TestSelectionMenuOpensOnADraggedSelection covers the terminal-mode right
// click, which only means anything when there is a selection to act on. It read
// the same dead field the menu did.
func TestSelectionMenuOpensOnADraggedSelection(t *testing.T) {
	o, _ := selectPane(t, "alpha bravo charlie")

	pressAt(o, 0, 0)
	dragTo(o, 10, 0)
	release(o, 10, 0)

	handleMouseClick(tea.MouseClickMsg{X: 5, Y: 5, Button: tea.MouseRight}, o)
	if !o.ContextMenuActive() {
		t.Fatal("right-clicking a drag-selected pane in terminal mode opened no selection menu")
	}
	if o.ContextMenu.Title != "Selection" {
		t.Errorf("menu title = %q, want the selection menu", o.ContextMenu.Title)
	}
}

// TestMenuReadsTheSelectionOfThePaneItWasOpenedOn drives the whole thing across
// two panes: select in one, focus the other, then right-click back on the first.
// The menu has to answer for the pane under the pointer.
func TestMenuReadsTheSelectionOfThePaneItWasOpenedOn(t *testing.T) {
	o, panes := selectTwoPanes(t, "alpha bravo charlie", "delta echo foxtrot")

	// Select in the right-hand pane, then hand focus back to the left one.
	pressAt2(o, panes[1], 0, 0)
	dragTo2(o, panes[1], 10, 0)
	release2(o, panes[1], 10, 0)
	o.FocusWindow(0)

	if copyRowDim(t, o, panes[1].X+5, 5) {
		t.Error("copy is dimmed on the pane the menu was opened on, which holds a selection")
	}
	o.CloseContextMenu()
	if got, want := copySelection(t, o), "delta echo"; got != want {
		t.Errorf("copy produced %q, want the targeted pane's selection %q", got, want)
	}

	// And the pane with nothing selected still says so.
	o.FocusWindow(1)
	if !copyRowDim(t, o, panes[0].X+5, 5) {
		t.Error("copy is offered on a pane with no selection because another pane has one")
	}
}

// selectTwoPanes builds two side-by-side panes, each with a line of text, in
// terminal mode. Pane 0 is focused.
func selectTwoPanes(t *testing.T, left, right string) (*app.OS, []*terminal.Window) {
	t.Helper()
	mk := func(line string, x int) *terminal.Window {
		em := vt.NewEmulator(40, 20)
		t.Cleanup(func() { _ = em.Close() })
		if _, err := em.Write([]byte(line)); err != nil {
			t.Fatalf("paint fixture line: %v", err)
		}
		return &terminal.Window{Terminal: em, X: x, Y: 0, Width: 42, Height: 22, Workspace: 1}
	}
	panes := []*terminal.Window{mk(left, 0), mk(right, 50)}
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	o.Width, o.Height = 120, 30
	o.EffectiveWidth, o.EffectiveHeight = 120, 30
	o.Windows = panes
	o.Mode, o.CurrentWorkspace, o.FocusedWindow = app.TerminalMode, 1, 0
	return o, panes
}

// pressAt2, dragTo2 and release2 are the single-pane helpers with the pane's
// own origin added, so a content cell is addressed the same way in either pane.
func pressAt2(o *app.OS, win *terminal.Window, x, y int) {
	handleMouseClick(tea.MouseClickMsg{Button: tea.MouseLeft, X: win.X + x + 1, Y: win.Y + y + 1}, o)
}

func dragTo2(o *app.OS, win *terminal.Window, x, y int) {
	handleMouseMotion(tea.MouseMotionMsg{Button: tea.MouseLeft, X: win.X + x + 1, Y: win.Y + y + 1}, o)
}

func release2(o *app.OS, win *terminal.Window, x, y int) {
	handleMouseRelease(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: win.X + x + 1, Y: win.Y + y + 1}, o)
}
