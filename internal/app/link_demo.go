package app

import (
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The jump-link demo is a test surface for the tuios:// click path: it asks
// for a pane and paints a link naming it into the pane the user was on, so
// the click that follows exercises the whole route — LinkAt, the nonce check,
// the jump. The bar is painted into the origin pane's own buffer, which means
// the guest there does not know its cursor moved; the next thing the guest
// prints redraws from where it was, and the bar scrolls away like any other
// output.

// SpawnJumpLinkDemo asks for a pane in the current workspace. In a daemon
// session the pane arrives later through a state sync, so the painting waits
// for it; locally the pane exists by the time AddWindow returns. Either way
// the new pane does not keep the focus: the demo refocuses the origin, and
// the link is the thing that is supposed to move it.
func (m *OS) SpawnJumpLinkDemo() {
	origin := m.GetFocusedWindow()
	if origin == nil {
		m.ShowNotification("No pane to paint the link on.", "info", m.Settings.NotificationDuration)
		return
	}
	originID := origin.ID
	before := len(m.Windows)
	m.pendingLinkDemoOrigin = originID
	m.AddWindow("")
	if !m.IsDaemonSession || m.DaemonClient == nil {
		// The window is here now; the sync will never run to spend the
		// intent. The demo pane is the one this call appended.
		var demo []*terminal.Window
		if len(m.Windows) > before {
			demo = append(demo, m.Windows[len(m.Windows)-1])
		}
		m.spendPendingLinkDemo(demo)
	}
}

// spendPendingLinkDemo paints the demo bar if the sync brought exactly the
// pane the demo asked for. Any other shape of sync clears the intent: a
// creation this client did not ask for must not paint anything, and a batch
// has no pane to name.
func (m *OS) spendPendingLinkDemo(created []*terminal.Window) {
	originID := m.pendingLinkDemoOrigin
	m.pendingLinkDemoOrigin = ""
	if originID == "" || len(created) != 1 {
		return
	}
	demo := created[0]
	idx := m.windowIndexByID(originID)
	if idx < 0 {
		return
	}
	origin := m.Windows[idx]

	// The daemon focused the pane it made. The demo is about the click, so
	// the eye stays where it was.
	m.FocusWindow(idx)

	origin.WriteOutput([]byte(fmt.Sprintf(
		"\r\n\x1b[2m[jump demo]\x1b[0m \x1b]8;%s;tuios://window/%s\x1b\\click here to jump\x1b]8;;\x1b\\\r\n",
		TuiosLinkParams(), demo.ID,
	)))
}
