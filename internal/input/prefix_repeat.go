package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// repeatablePrefixActions are the prefix commands that stay armed, so the same
// key pressed again continues rather than being typed into a pane.
//
// They are the ones with a direction or a size: walking panes, moving one,
// resizing one, cycling a width, stepping through workspaces. What they have
// in common is that one press is rarely the whole intention. tmux marks the
// same kind of binding with -r, and all seventeen of its repeatable defaults
// are of this shape.
//
// Nothing destructive is here, and nothing that opens a panel. A second press
// of those is either a mistake or a dismissal, and neither should be made
// easier to do by accident.
var repeatablePrefixActions = map[string]bool{
	"terminal_focus_left":   true,
	"terminal_focus_right":  true,
	"terminal_focus_up":     true,
	"terminal_focus_down":   true,
	"focus_left":            true,
	"focus_right":           true,
	"focus_up":              true,
	"focus_down":            true,
	"next_window":           true,
	"prev_window":           true,
	"terminal_next_window":  true,
	"terminal_prev_window":  true,
	"next_workspace":        true,
	"prev_workspace":        true,
	"resize_left":           true,
	"resize_right":          true,
	"resize_up":             true,
	"resize_down":           true,
	"scrolling_focus_left":  true,
	"scrolling_focus_right": true,
	"scrolling_move_left":   true,
	"scrolling_move_right":  true,
	"scrolling_cycle_width": true,
	// Each press goes to the next item waiting for the person, so a run of
	// presses walks the Inbox without opening it.
	"prefix_next_attention": true,
	// The same for the newest finished turn nobody has seen, walking back.
	// Until that work lands the action reports it did nothing, and neither
	// the prefix press nor a repeat arms the window (see runPrefixWork).
	"prefix_next_finished": true,
}

// armIfRepeatable keeps the prefix live when the command just run is one worth
// pressing again.
func armIfRepeatable(o *app.OS, action string) {
	if repeatablePrefixActions[action] {
		o.ArmPrefixRepeat()
	} else {
		o.ClearPrefixRepeat()
	}
}

// tryPrefixRepeat runs a repeatable prefix command without a second prefix
// press, when one arrives inside the window the last one left open.
//
// Only a repeatable command continues. A key inside the window that means
// something else, or nothing, closes the window and is handled as the ordinary
// key it is: the window exists to make one command easy to repeat, not to
// swallow whatever is typed next.
func tryPrefixRepeat(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	if !o.PrefixRepeatLive() {
		return o, nil, false
	}
	if o.KeybindRegistry == nil {
		// Nothing can be looked up, so nothing can repeat. The window closes
		// rather than staying open against a registry that is not there.
		o.ClearPrefixRepeat()
		return o, nil, false
	}
	action := lookupAction(msg, o.KeybindRegistry.GetPrefixAction)
	if !repeatablePrefixActions[action] {
		o.ClearPrefixRepeat()
		return o, nil, false
	}
	// A work action that did nothing closes the window and leaves the key to
	// the ordinary path, so it is not swallowed while its work is unbuilt.
	if app.PrefixWorkActions[action] {
		cmd, handled := runPrefixWork(action, o)
		if !handled {
			o.ClearPrefixRepeat()
			return o, nil, false
		}
		o.ArmPrefixRepeat()
		return o, cmd, true
	}
	m, cmd, ok := dispatchAction(action, msg, o)
	if !ok {
		o.ClearPrefixRepeat()
		return o, nil, false
	}
	m.ArmPrefixRepeat()
	return m, cmd, true
}
