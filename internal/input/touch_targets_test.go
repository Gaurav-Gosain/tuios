package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A divider is one column wide. On a phone that column is about 8 pixels, which
// is a quarter of what either mobile platform asks a target to be, so a touch
// client gets the columns either side of it as well.
func TestTouchWidensATiledDividerGrab(t *testing.T) {
	prev := config.Global.SharedBorders
	config.Global.SharedBorders = true
	defer func() { config.Global.SharedBorders = prev }()

	for _, off := range []int{-1, 0, 1} {
		o, wa, wb := twoPaneBSP(t)
		o.TouchClient = true
		left, _ := leftPaneOf(wa, wb)
		x := left.X + left.Width + off
		y := left.Y + left.Height/2

		handleMouseClick(clickMsg(x, y), o)
		if !o.BorderResizing {
			t.Errorf("a finger %+d cells from the divider grabbed nothing", off)
		}
		handleMouseRelease(releaseMsg(x, y), o)
	}
}

// The desktop keeps the exact cell. The cells either side belong to somebody's
// shell, and a pointer can hit one column.
func TestAPointerStillNeedsTheDividerItself(t *testing.T) {
	prev := config.Global.SharedBorders
	config.Global.SharedBorders = true
	defer func() { config.Global.SharedBorders = prev }()

	for _, off := range []int{-1, 1} {
		o, wa, wb := twoPaneBSP(t)
		left, _ := leftPaneOf(wa, wb)
		handleMouseClick(clickMsg(left.X+left.Width+off, left.Y+left.Height/2), o)
		if o.BorderResizing {
			t.Errorf("a pointer %+d cells from the divider grabbed it anyway", off)
		}
	}
}

// The slop is one cell and no more: two cells off is a click in a pane, and a
// grab that reached that far would swallow the content column beside it.
func TestTheTouchGrabStopsAtOneCell(t *testing.T) {
	prev := config.Global.SharedBorders
	config.Global.SharedBorders = true
	defer func() { config.Global.SharedBorders = prev }()

	for _, off := range []int{-3, 3} {
		o, wa, wb := twoPaneBSP(t)
		o.TouchClient = true
		left, _ := leftPaneOf(wa, wb)
		handleMouseClick(clickMsg(left.X+left.Width+off, left.Y+left.Height/2), o)
		if o.BorderResizing {
			t.Errorf("a finger %+d cells from the divider still grabbed it", off)
		}
	}
}

// A finger has no ctrl and no shift, so a long press is the only right click a
// phone can make. Terminal mode is where a user spends their time and where it
// used to reach nothing.
func TestTouchLongPressOpensThePaneMenuWhileTyping(t *testing.T) {
	o, wa, wb := twoPaneBSP(t)
	o.TouchClient = true
	o.Mode = app.TerminalMode
	left, _ := leftPaneOf(wa, wb)

	x, y := left.X+left.Width/2, left.Y+left.Height/2
	handleMouseClick(tea.MouseClickMsg{Button: tea.MouseRight, X: x, Y: y}, o)
	if !o.ContextMenuActive() {
		t.Fatal("a long press over a pane in terminal mode opened nothing")
	}
}

// The desktop contract is untouched: a plain right click in terminal mode is
// still consumed, and the menu is still reached with a modifier held.
func TestAPointerRightClickWhileTypingStillOpensNothing(t *testing.T) {
	o, wa, wb := twoPaneBSP(t)
	o.Mode = app.TerminalMode
	left, _ := leftPaneOf(wa, wb)

	x, y := left.X+left.Width/2, left.Y+left.Height/2
	handleMouseClick(tea.MouseClickMsg{Button: tea.MouseRight, X: x, Y: y}, o)
	if o.ContextMenuActive() {
		t.Fatal("a plain right click in terminal mode opened the menu for a pointer")
	}
	handleMouseClick(tea.MouseClickMsg{Button: tea.MouseRight, X: x, Y: y, Mod: tea.ModCtrl}, o)
	if !o.ContextMenuActive() {
		t.Fatal("ctrl+right click stopped reaching the menu")
	}
}
