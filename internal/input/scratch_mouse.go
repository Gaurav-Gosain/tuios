package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// scratchPress handles a press while the scratch terminal is on the screen.
//
// The scratch terminal is a dropdown. A press outside it hides it and focuses
// the pane under the pointer, and that is all the press does: it is not
// passed on, so a click meant to leave the popup does not also type into,
// select in or start a drag on the pane. A drag that starts outside the popup
// therefore hides it and moves nothing.
//
// A press on the popup's border or title only focuses it, except the close
// button, which hides it. The popup is never moved or resized by the mouse.
// A press on its content goes on to the usual handling (focus, selection,
// forwarding to the program), which beginWindowDrag and the resize setup
// refuse for the scratch terminal.
func scratchPress(msg tea.MouseClickMsg, o *app.OS, clicked, x, y int) (*app.OS, tea.Cmd, bool) {
	si := o.ShownScratch()
	if si < 0 {
		return o, nil, false
	}
	if clicked != si {
		o.HideShownScratch()
		if clicked >= 0 {
			o.FocusWindowFromClick(clicked, x, y)
		}
		o.SyncStateToDaemon()
		return o, nil, true
	}
	win := o.Windows[si]
	if msg.Mod != 0 {
		o.FocusWindowFromClick(si, x, y)
		return o, nil, true
	}
	if _, _, inContent := win.ScreenToTerminal(x, y); inContent {
		return o, nil, false
	}
	if action, ok := o.WindowButtonIn(win.ID, x, y); ok && action == app.WindowButtonClose && msg.Button == tea.MouseLeft {
		o.CloseWindowByHand(si)
		return o, nil, true
	}
	o.FocusWindowFromClick(si, x, y)
	return o, nil, true
}
