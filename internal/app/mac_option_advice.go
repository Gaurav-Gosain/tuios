package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// NoteComposedOptionChord reports, once per run, that the host terminal is
// composing characters out of Option chords instead of sending Alt.
//
// It fires on a chord tuios recognised anyway, which is deliberate: that is the
// moment it can be certain of the diagnosis, having just had to translate a
// composed glyph back into the chord the user meant. The glyph tables cover the
// characters macOS composes, and nothing can cover a dead key the terminal
// swallows whole (Option+n emits nothing at all until a second key ends the
// composition) or the alt+arrow chords Ghostty rewrites before they are encoded.
// Those keep missing until the setting named here is turned on, so the user is
// told about it the first time the subject comes up rather than left to find out
// which of their bindings are quietly unreliable.
func (m *OS) NoteComposedOptionChord(chord string) {
	if m.optionAdviceShown {
		return
	}
	if m.KeybindRegistry == nil {
		return
	}
	// Only worth saying for a chord that is bound to something. A stray µ typed
	// into a shell is not a misfiring keybinding.
	if m.KeybindRegistry.GetAction(chord) == "" &&
		m.KeybindRegistry.GetTerminalModeAction(chord) == "" {
		return
	}
	// A browser tab has no terminal settings to change, so there is no advice to
	// give. The composition happens in macOS before the page ever sees the key,
	// and nothing in the browser can undo it.
	if m.BrowserClient {
		return
	}
	m.optionAdviceShown = true

	// The environment this process reads belongs to the machine tuios runs on.
	// For a client on the far end of a network that is the server, not the
	// terminal the user is sitting at, so name no product and give the step that
	// is true of every terminal.
	host := config.DetectHostTerminal()
	if m.RemoteClient {
		host = config.HostUnknown
	}
	advice := config.MacOptionAdvice(host, chord)
	if host == config.HostGhostty {
		advice += ". " + config.GhosttyAltArrowAdvice
	}
	m.ShowNotification(advice, "warning", config.NotificationDuration)
}
