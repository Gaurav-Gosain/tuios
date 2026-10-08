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
	m.noteOptionProblem(chord, chord)
}

// NoteRewrittenAltArrow reports the same thing for the other way a macOS
// terminal loses an Option chord: Option+Left and Option+Right arrive as
// alt+b and alt+f, the readline word motions, instead of as arrow keys.
//
// This is the case the advice was written for and the one it could never
// reach. NoteComposedOptionChord fires on a composed glyph, and these two
// arrive with a real Alt modifier and no glyph at all, so pressing Option+Left
// produced no hint, no action, and nothing to read. Ghostty rewrites them in
// its own shipped keybinds, before any encoding, so they miss even with
// macos-option-as-alt turned on; see GhosttyAltArrowAdvice.
//
// arrow is the chord the user meant, and got is what arrived instead. The
// chord that has to be bound for this to be worth saying is the arrow, since
// alt+b is bound to nothing and is not supposed to be.
func (m *OS) NoteRewrittenAltArrow(got, arrow string) {
	m.noteOptionProblem(arrow, arrow+" (it arrives as "+got+")")
}

// noteOptionProblem is the one advice, shown once per run. bound is the chord
// to check is worth talking about; named is how the message spells it.
func (m *OS) noteOptionProblem(bound, named string) {
	if m.optionAdviceShown {
		return
	}
	if m.KeybindRegistry == nil {
		return
	}
	// Only worth saying for a chord that is bound to something. A stray µ typed
	// into a shell is not a misfiring keybinding.
	if m.KeybindRegistry.GetAction(bound) == "" &&
		m.KeybindRegistry.GetTerminalModeAction(bound) == "" {
		return
	}
	chord := named
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
	m.ShowNotification(advice, "warning", m.Settings.NotificationDuration)
}
