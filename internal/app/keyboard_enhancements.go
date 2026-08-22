package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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
	// A pane that pushed the event-type flag is asking to be told when a key
	// comes up, and tuios cannot pass on what it never receives: unless the host
	// is asked for releases too, the pane sees an endless press. That is fatal
	// for a compositor in a pane, whose Wayland clients hold the key down and let
	// xkb repeat it until a release arrives, and it is why the request tracks the
	// focused pane rather than being fixed at startup.
	paneFlags := m.PaneKeyboardFlags()
	if paneFlags&ansi.KittyReportEventTypes != 0 {
		enhancements.ReportEventTypes = true
		// A terminal only reports the release of a key it sends as an escape
		// code, so Enter and Tab and every plain character come up silently
		// unless all keys are asked for as well. Associated text comes with that
		// or the character the user typed is lost on its way to the pane. Both
		// ride on the pane's own request, so a session with no such pane focused
		// is left exactly as it was.
		if paneFlags&ansi.KittyReportAllKeysAsEscapeCodes != 0 {
			enhancements.ReportAllKeysAsEscapeCodes = true
			enhancements.ReportAssociatedText = true
		}
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

// PaneKeyboardFlags returns the kitty keyboard protocol flags the focused pane
// has in effect, or zero when no pane is focused or none were pushed.
func (m *OS) PaneKeyboardFlags() int {
	window := m.GetFocusedWindow()
	if window == nil || window.Terminal == nil {
		return 0
	}
	return window.Terminal.KittyKeyboardFlags()
}

// NoteKeyboardEnhancements records what the host answered the enhancement query
// with.
func (m *OS) NoteKeyboardEnhancements(msg tea.KeyboardEnhancementsMsg) {
	m.KeyboardFlags = msg.Flags
	m.KeyboardEnhancementsEnabled = msg.SupportsKeyDisambiguation()
}
