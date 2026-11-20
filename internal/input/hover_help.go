package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// handleToggleHoverHelp turns hover help on or off. It is the only way in and
// the only way out: no other key closes the explainer.
func handleToggleHoverHelp(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleHoverHelp()
	return o, nil
}

// hoverHelpKey runs one of hover help's own keys, and reports whether the key
// was one. Hover help is a view, not a mode: every other key goes on to the
// rest of the handler, and so does every key while something that owns the
// keyboard is open, the leader and its menus included, so a help key never
// takes a key from what the person is typing into.
func hoverHelpKey(msg tea.KeyPressMsg, o *app.OS) bool {
	if !o.HoverHelpOn() || o.Locked() || !viewerTakesKey(msg, o) ||
		o.PanelOwnsKeyboard() || o.HintsOpen() || o.PaneLabelsOpen() ||
		o.CaptureActive() || o.ReviewOpen() {
		return false
	}
	action := tableAction(o, msg, overlayKeys(o), config.TableHelp)
	switch action {
	case config.ActionHelpNext:
		o.HelpNext(1)
	case config.ActionHelpPrev:
		o.HelpNext(-1)
	case config.ActionHelpDeeper:
		o.HelpDeeper()
	case config.ActionHelpShallower:
		o.HelpShallower()
	case config.ActionHelpScrollUp:
		o.HelpScroll(-1)
	case config.ActionHelpScrollDown:
		o.HelpScroll(1)
	case config.ActionHelpKeys:
		o.HelpToggleKeys()
	default:
		return false
	}
	o.NoteAction(action)
	return true
}
