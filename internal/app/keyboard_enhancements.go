package app

import (
	tea "charm.land/bubbletea/v2"
)

// keyboardEnhancements is what tuios asks the host terminal for through the
// Kitty keyboard protocol. Bubble Tea already requests key disambiguation on
// every view; these are the additions tuios has a use for.
//
// The request is made from the view because that is where Bubble Tea reads it:
// changing what is asked for re-runs the negotiation and the answer arrives as a
// [tea.KeyboardEnhancementsMsg]. Terminals that speak none of this ignore the
// sequence, so there is nothing to fall back from.
func (m *OS) keyboardEnhancements() tea.KeyboardEnhancements {
	enhancements := tea.KeyboardEnhancements{
		// Alternate-key reporting adds the PC-101 key behind whatever character
		// the layout produced. That is what lets a chord be recognised when the
		// OS composed something else out of it, which is the whole of the macOS
		// Option problem, and it fixes the same class of miss on any non-US
		// layout. It only ever adds subparameters to a report tuios already
		// parses.
		ReportAlternateKeys: true,
	}
	if !m.HoldModeAvailable() {
		return enhancements
	}
	// Release events are the only honest way to know a key is still down.
	enhancements.ReportEventTypes = true
	if m.HoldModeNeedsAllKeys() {
		// A modifier key is only reported as a key of its own in this mode. It
		// also stops text being sent as text, so associated-text reporting comes
		// with it: without that pair, composed and IME input would be lost on
		// its way to a pane.
		enhancements.ReportAllKeysAsEscapeCodes = true
		enhancements.ReportAssociatedText = true
	}
	return enhancements
}

// NoteKeyboardEnhancements records what the host answered the enhancement query
// with.
func (m *OS) NoteKeyboardEnhancements(msg tea.KeyboardEnhancementsMsg) {
	m.KeyboardFlags = msg.Flags
	m.KeyboardEnhancementsEnabled = msg.SupportsKeyDisambiguation()
}
