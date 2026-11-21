package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
)

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

func copySelection(t *testing.T, o *app.OS) string {
	t.Helper()
	_, cmd := GetDispatcher().Dispatch("copy_selection", tea.KeyPressMsg{}, o)
	return clipboardText(t, cmd)
}

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

// The copy action must stay quiet when there is nothing selected, so a stray
// key or a menu reached some other way cannot clear the clipboard.
func TestCopySelectionWithNothingSelectedCopiesNothing(t *testing.T) {
	o, _ := selectPane(t, "alpha bravo charlie")

	if got := copySelection(t, o); got != "" {
		t.Errorf("copying with no selection wrote %q to the clipboard", got)
	}
}

// TestMenuReadsTheSelectionOfThePaneItWasOpenedOn drives the whole thing across
// two panes: select in one, focus the other, then right-click back on the first.
// The menu has to answer for the pane under the pointer.
func TestMenuReadsTheSelectionOfThePaneItWasOpenedOn(t *testing.T) {
	o, panes := selectTwoPanes(t, "alpha bravo charlie", "delta echo foxtrot")

	pressAt2(o, panes[1], 0, 0)
	dragTo2(o, panes[1], 10, 0)
	release2(o, panes[1], 10, 0)
	o.FocusWindow(0)

	if copyRowDim(t, o, panes[1].X+5, 5) {
		t.Error("copy is dimmed on the pane the menu was opened on, which holds a selection")
	}
	o.CloseContextMenu()
	if got, want := copySelection(t, o), "delta echo "; got != want {
		t.Errorf("copy produced %q, want the targeted pane's selection %q", got, want)
	}

	o.FocusWindow(1)
	if !copyRowDim(t, o, panes[0].X+5, 5) {
		t.Error("copy is offered on a pane with no selection because another pane has one")
	}
}

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

func pressAt2(o *app.OS, win *terminal.Window, x, y int) {
	handleMouseClick(tea.MouseClickMsg{Button: tea.MouseLeft, X: win.X + x + 1, Y: win.Y + y + 1}, o)
}

func dragTo2(o *app.OS, win *terminal.Window, x, y int) {
	handleMouseMotion(tea.MouseMotionMsg{Button: tea.MouseLeft, X: win.X + x + 1, Y: win.Y + y + 1}, o)
}

func release2(o *app.OS, win *terminal.Window, x, y int) {
	handleMouseRelease(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: win.X + x + 1, Y: win.Y + y + 1}, o)
}
