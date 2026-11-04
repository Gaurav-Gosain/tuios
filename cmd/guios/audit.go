package main

import (
	"github.com/Gaurav-Gosain/mygo/plugins/terminal"
	"github.com/Gaurav-Gosain/mygo/ui"
)

// auditPane and auditFrame, set by the e2e build's GUIOS_FRAME_AUDIT, note
// how each frame draws each pane's terminal: live, as a copy, or without
// one, blank. The QA check blankcheck.py reads what they write. Nil in
// other builds: they cost nothing there.
var (
	auditPane  func(id, mode string, t *terminal.Terminal)
	auditFrame func(a *App, c *ui.Context)
)
