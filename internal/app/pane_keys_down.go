package app

// NotePaneKeyDown records that the press of the key with this code went to the
// pane with this window ID.
//
// A pane that asked for key releases must get the release of every key it saw
// pressed, and must not get the release of a key it never saw. The leader's b
// is the plain case: tuios keeps the press, so its release is tuios's too.
func (m *OS) NotePaneKeyDown(code rune, windowID string) {
	if m.paneKeysDown == nil {
		m.paneKeysDown = map[rune]string{}
	}
	m.paneKeysDown[code] = windowID
}

// TakePaneKeyDown returns the window ID the press of the key with this code
// went to, and forgets it. ok is false when the press went to no pane.
func (m *OS) TakePaneKeyDown(code rune) (windowID string, ok bool) {
	windowID, ok = m.paneKeysDown[code]
	if ok {
		delete(m.paneKeysDown, code)
	}
	return windowID, ok
}
