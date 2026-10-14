package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// sectionAction resolves a key press against one registry section, trying every
// spelling the terminal might have sent it as.
//
// One physical chord reaches tuios under several names depending on what the host negotiated: Ctrl+P stringifies
// to "ctrl+p" under the legacy control byte and CSI-u, to "p" under Kitty
// associated-text reporting, and to "ctrl+P" under alternate-key reporting.
// bindingKeys offers String() and Keystroke() in turn, which covers the first
// three, and lookupKey lowercases compound keys, which covers the fourth. The
// lock modifiers never appear in either spelling, so Num Lock (on by default on
// most keyboards) does not hide the binding the way an exact Mod comparison
// would. TestGlobalBindsAcrossEncodings pins all of it.
func sectionAction(msg tea.KeyPressMsg, o *app.OS, lookup sectionLookup) string {
	if o.KeybindRegistry == nil {
		return ""
	}
	return lookupAction(o, msg, func(key string) string {
		return lookup(o.KeybindRegistry, key)
	})
}

// handleGlobalBinds runs the global-scope binds, the ones that act in window
// mode and terminal mode alike, and reports whether the key was consumed.
//
// It goes through the dispatcher, so the section holds whatever the dispatcher
// knows, as its documentation as general promises. The palette and the
// launcher are registered like every other action: literals in the mode
// handlers would be invisible to `tuios keybinds doctor`, unrebindable, and
// silently ahead of anything a user had put on the same key.
func handleGlobalBinds(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	// Each case records the action it ran, the way Dispatch does for every
	// action that goes through the dispatcher. These two do not, so without the
	// line here the one probe that says whether a key reached its action is
	// blind to the whole global section. See NoteAction.
	return dispatchAction(sectionAction(msg, o, (*config.KeybindRegistry).GetGlobalAction), msg, o)
}
