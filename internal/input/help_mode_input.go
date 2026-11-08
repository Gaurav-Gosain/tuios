package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// helpOwnsKey reports whether a key is help mode's to read. Help is a view,
// not a mode: it takes the keys it acts on only while nothing else owns the
// keyboard, and every other key reaches the app as if it were not up. A
// leader chord part-way through, a panel above it, the pointer's labels or a
// capture belong to the app, and a key the leader itself is bound to is the
// leader's first.
func helpOwnsKey(msg tea.KeyPressMsg, o *app.OS) bool {
	return viewerTakesKey(msg, o) && !o.PanelOwnsKeyboard() &&
		!o.HintsOpen() && !o.PaneLabelsOpen() && !o.CaptureActive() && !o.ReviewOpen()
}

// helpOwnsPointer reports whether the pointer is help mode's to read. A panel
// above it, a capture or the review takes the pointer the way it takes the
// keys, and a key that hid the explanation hands the pointer back with it, so
// help does not scroll or re-target under them.
func helpOwnsPointer(o *app.OS) bool {
	return !o.HelpHidden() && !o.MenuOpen() && !o.PanelOwnsKeyboard() &&
		!o.CaptureActive() && !o.ReviewOpen()
}

// handleHelpModeKey runs the keys help mode binds and reports whether it did.
// A key it does not bind is not help's: the caller lets it through, so the
// rail's own chords, a workspace switch and the pane itself keep working while
// the explanation is up. The pane follows whatever layout they leave on the
// next frame, which is what makes the listed chords worth pressing.
//
// Shift+Esc is the way out by default, because esc is what a pane wants for
// itself and help must not take it. The keys themselves come from
// [keybindings.help], through GetHelpAction, so a rebind moves the pane and the
// hint that names it together.
//
// Every key below is a navigation key, and a navigation key is the pane's own:
// one pressed while a key has the pane hidden brings it back at once rather
// than waiting out the hide.
func handleHelpModeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	if o.KeybindRegistry == nil {
		return o, nil, false
	}
	action := lookupAction(o, msg, o.KeybindRegistry.GetHelpAction)
	switch action {
	case config.ActionHelpExit:
		noteHelpAction(o, action)
		o.ExitHelpMode()
	case config.ActionHelpNext:
		noteHelpAction(o, action)
		o.HelpNext(1)
	case config.ActionHelpPrev:
		noteHelpAction(o, action)
		o.HelpNext(-1)
	case config.ActionHelpDeeper:
		noteHelpAction(o, action)
		o.HelpDeeper()
	case config.ActionHelpShallower:
		noteHelpAction(o, action)
		o.HelpShallower()
	case config.ActionHelpScrollUp:
		noteHelpAction(o, action)
		o.HelpScroll(-1)
	case config.ActionHelpScrollDown:
		noteHelpAction(o, action)
		o.HelpScroll(1)
	case config.ActionHelpKeys:
		noteHelpAction(o, action)
		o.HelpToggleKeys()
	default:
		return o, nil, false
	}
	return o, nil, true
}

// noteHelpAction records that a help-mode key ran, the way the dispatcher
// records every other action: the crash report's last-actions line and a
// host's OnAction hook both read it.
func noteHelpAction(o *app.OS, action string) {
	o.NoteAction(action)
	if o.OnAction != nil {
		o.OnAction(action)
	}
}
