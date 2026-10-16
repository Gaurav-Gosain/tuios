package config

// DefaultPrefixRepeat is the prefix commands that stay armed when
// keybindings.repeat is unset, so the same key pressed again continues rather
// than being typed into a pane.
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
var DefaultPrefixRepeat = []string{
	"terminal_focus_left",
	"terminal_focus_right",
	"terminal_focus_up",
	"terminal_focus_down",
	"focus_up",
	"focus_down",
	"next_window",
	"prev_window",
	"terminal_next_window",
	"terminal_prev_window",
	"next_workspace",
	"prev_workspace",
	// tmux's resize-pane -L/-R/-U/-D, which tmux binds with -r.
	"resize_pane_left_5",
	"resize_pane_right_5",
	"resize_pane_up_5",
	"resize_pane_down_5",
	"resize_pane_left_20",
	"resize_pane_right_20",
	"resize_pane_up_20",
	"resize_pane_down_20",
	// The tiling resize steps, from either edge.
	"resize_master_shrink",
	"resize_master_grow",
	"resize_master_shrink_left",
	"resize_master_grow_left",
	"resize_height_shrink",
	"resize_height_grow",
	"resize_height_shrink_top",
	"resize_height_grow_top",
	// The scrolling layout: walking columns, moving one, cycling its width.
	"scroll_focus_left",
	"scroll_focus_right",
	"scroll_move_left",
	"scroll_move_right",
	"scroll_cycle_width",
	// Each press goes to the next item waiting for the person, so a run of
	// presses walks the Inbox without opening it.
	"prefix_next_attention",
	// The same for the newest finished turn nobody has seen, walking back.
	// Until that work lands the action reports it did nothing, and neither
	// the prefix press nor a repeat arms the window (see runPrefixWork).
	"prefix_next_finished",
}
