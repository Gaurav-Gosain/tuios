package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// sectionAction resolves a key press against one registry section, trying every
// spelling the terminal might have sent it as.
//
// This is what replaced the hand-rolled isCtrlP and isLauncherKey matchers, and
// it has to keep the property they existed for. One physical chord reaches tuios
// under several names depending on what the host negotiated: Ctrl+P stringifies
// to "ctrl+p" under the legacy control byte and CSI-u, to "p" under Kitty
// associated-text reporting, and to "ctrl+P" under alternate-key reporting.
// bindingKeys offers String() and Keystroke() in turn, which covers the first
// three, and lookupKey lowercases compound keys, which covers the fourth. The
// lock modifiers never appear in either spelling, so Num Lock (on by default on
// most keyboards) no longer hides the binding the way an exact Mod comparison
// did. TestGlobalBindsAcrossEncodings pins all of it.
func sectionAction(msg tea.KeyPressMsg, o *app.OS, lookup sectionLookup) string {
	if o.KeybindRegistry == nil {
		return ""
	}
	return lookupAction(msg, func(key string) string {
		return lookup(o.KeybindRegistry, key)
	})
}

// handleGlobalBinds runs the global-scope binds, the ones that act in window
// mode and terminal mode alike, and reports whether the key was consumed.
//
// The palette and the launcher used to be literals in both mode handlers. As
// literals they were invisible to `tuios keybinds doctor`, unrebindable, and
// silently ahead of anything a user had put on the same key.
func handleGlobalBinds(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	// Each case records the action it ran, the way Dispatch does for every
	// action that goes through the dispatcher. These two do not, so without the
	// line here the one probe that says whether a key reached its action is
	// blind to the whole global section. See NoteAction.
	switch action := sectionAction(msg, o, (*config.KeybindRegistry).GetGlobalAction); action {
	case "command_palette":
		o.NoteAction(action)
		return o, o.OpenCommandPalette(), true
	case "launcher":
		o.NoteAction(action)
		return o, o.OpenLauncher(), true
	}
	return o, nil, false
}
