package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func scratchPress(_ tea.MouseClickMsg, o *app.OS, clicked, x, y int) (*app.OS, tea.Cmd, bool) {
	outer, ok := o.ScratchBox()
	if !ok {
		return o, nil, false
	}
	if x >= outer.X && x < outer.X+outer.W && y >= outer.Y && y < outer.Y+outer.H {
		return o, nil, clicked < 0
	}
	o.HideShownScratch()
	if under := findClickedWindow(x, y, o); under >= 0 {
		o.FocusWindowFromClick(under, x, y)
	}
	o.SyncStateToDaemon()
	return o, nil, true
}
