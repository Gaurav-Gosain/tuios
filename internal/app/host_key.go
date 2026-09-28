package app

import tea "charm.land/bubbletea/v2"

// NoteHostKey records a key press as the host terminal sent it. The input
// layer reads the press without its base-layout key; a pane is still owed
// that key, and HostBaseCode hands it back on the way there.
func (m *OS) NoteHostKey(msg tea.KeyPressMsg) {
	m.hostKey = msg
}

// HostBaseCode returns the base-layout key the host sent with the press
// NoteHostKey last saw, when msg has its code, and zero otherwise. The code
// alone identifies the press: the hold trigger may have taken a modifier off
// the chord on its way through.
func (m *OS) HostBaseCode(msg tea.KeyPressMsg) rune {
	if m.hostKey.Code != msg.Code {
		return 0
	}
	return m.hostKey.BaseCode
}
