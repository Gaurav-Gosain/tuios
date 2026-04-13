//! Built-in actions for the tuios keybind system.

const std = @import("std");

/// Actions that can be bound to keys.
pub const Action = union(enum) {
    // Splitting
    split_horizontal,
    split_vertical,
    split_auto,

    // Focus movement
    focus_left,
    focus_right,
    focus_up,
    focus_down,
    cycle_next,
    cycle_prev,

    // Pane management
    close_pane,
    toggle_zoom,
    new_window,

    // Resize
    resize_left,
    resize_right,
    resize_up,
    resize_down,

    // Split manipulation
    rotate_split,
    equalize_splits,
    swap_left,
    swap_right,

    // Session
    detach_session,
    rename_session,
    switch_session,
    quit,

    // UI overlays
    command_palette,
    session_switcher,
    help_toggle,

    // Floating pane
    floating_toggle,

    // Workspaces
    workspace_1,
    workspace_2,
    workspace_3,
    workspace_4,
    workspace_5,
    workspace_6,
    workspace_7,
    workspace_8,
    workspace_9,
    move_to_workspace_1,
    move_to_workspace_2,
    move_to_workspace_3,
    move_to_workspace_4,
    move_to_workspace_5,
    move_to_workspace_6,
    move_to_workspace_7,
    move_to_workspace_8,
    move_to_workspace_9,

    // Copy mode
    enter_copy_mode,

    // Mode switching
    enter_wm_mode,
    enter_terminal_mode,

    /// Convert action to its canonical string name.
    pub fn toString(self: Action) []const u8 {
        return @tagName(self);
    }

    /// Parse an action from a string name.
    pub fn fromString(name: []const u8) ?Action {
        const Tag = @typeInfo(Action).@"union".tag_type.?;
        const tag = std.meta.stringToEnum(Tag, name) orelse return null;
        return @unionInit(Action, @tagName(tag), {});
    }
};
