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

    // Pane management
    close_pane,
    toggle_zoom,
    new_window,

    // Tab management
    new_tab,
    close_tab,
    rename_tab,
    next_tab,
    previous_tab,

    // Resize
    resize_left,
    resize_right,
    resize_up,
    resize_down,

    // Session
    detach_session,
    rename_session,
    switch_session,
    quit,

    // UI
    command_palette,

    // Floating pane
    floating_toggle,
    floating_increase_size,
    floating_decrease_size,

    // Workspaces (tuios-specific)
    workspace_1,
    workspace_2,
    workspace_3,
    workspace_4,
    workspace_5,
    workspace_6,
    workspace_7,
    workspace_8,
    workspace_9,

    // Copy mode
    enter_copy_mode,

    // Window management mode
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
