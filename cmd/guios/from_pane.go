package main

import (
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// fromPaneNotice is a line under the session list when this machine's daemon
// does not take the app as the person. The app was opened from a tuios
// pane's shell and took on the pane's environment, and a program in a pane
// cannot act as the person: the daemon refuses its presence, so the app
// answers nothing for the person, and its panes never take a session's
// latest from a TUI client, whose size they then keep. Nothing else says
// why, so the line does, with the way out.
func (a *App) fromPaneNotice(c *ui.Context, pal *palette) {
	if !a.fromPane {
		return
	}
	ui.Column(c.Key("from-pane")).Padding(8, 16, 8, 16).Shrink(0).Children(func() {
		ui.Text(c, locales.RailFromPane()).FontSize(fsSmall).TextColor(pal.attnText).MaxLines(4)
	})
}
