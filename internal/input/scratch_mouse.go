package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// scratchPress handles a press while a scratch group is on the screen.
//
// The group is a dropdown. A press outside its box hides it and focuses the
// pane under the pointer on the workspace it was shown over, and that is all
// the press does: it is not passed on, so a click meant to leave the group
// does not also type into, select in or start a drag on that pane. A press on
// the frame does nothing. A press inside the box goes on to the usual
// handling, which acts on the group's own panes: they are the current
// workspace.
func scratchPress(_ tea.MouseClickMsg, o *app.OS, clicked, x, y int) (*app.OS, tea.Cmd, bool) {
	outer, ok := o.ScratchBox()
	if !ok {
		return o, nil, false
	}
	if x >= outer.X && x < outer.X+outer.W && y >= outer.Y && y < outer.Y+outer.H {
		// The frame is not a pane.
		return o, nil, clicked < 0
	}
	o.HideShownScratch()
	if under := findClickedWindow(x, y, o); under >= 0 {
		o.FocusWindowFromClick(under, x, y)
	}
	o.SyncStateToDaemon()
	return o, nil, true
}
