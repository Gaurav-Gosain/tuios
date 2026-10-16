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
	"terminal_focus_left":  true,
	"terminal_focus_right": true,
	"terminal_focus_up":    true,
	"terminal_focus_down":  true,
	"focus_up":             true,
	"focus_down":           true,
	"next_window":          true,
	"prev_window":          true,
	"terminal_next_window": true,
	"terminal_prev_window": true,
	"next_workspace":       true,
	"prev_workspace":       true,
	// tmux's resize-pane -L/-R/-U/-D, which tmux binds with -r.
	"resize_pane_left_5":   true,
	"resize_pane_right_5":  true,
	"resize_pane_up_5":     true,
	"resize_pane_down_5":   true,
	"resize_pane_left_20":  true,
	"resize_pane_right_20": true,
	"resize_pane_up_20":    true,
	"resize_pane_down_20":  true,
	// The tiling resize steps, from either edge.
	"resize_master_shrink":      true,
	"resize_master_grow":        true,
	"resize_master_shrink_left": true,
	"resize_master_grow_left":   true,
	"resize_height_shrink":      true,
	"resize_height_grow":        true,
	"resize_height_shrink_top":  true,
	"resize_height_grow_top":    true,
	// The scrolling layout: walking columns, moving one, cycling its width.
	"scroll_focus_left":  true,
	"scroll_focus_right": true,
	"scroll_move_left":   true,
	"scroll_move_right":  true,
	"scroll_cycle_width": true,
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
	action := lookupAction(o, msg, o.KeybindRegistry.GetPrefixAction)
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
