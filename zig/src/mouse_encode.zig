//! Mouse input encoding for terminal protocols.

const std = @import("std");

const ghostty = @import("ghostty-vt");

const key_parse = @import("key_parse.zig");

const log = std.log.scoped(.mouse_encode);

const X10_COORD_LIMIT: u16 = 222;

const ButtonCode = struct {
    const LEFT: u8 = 0;
    const MIDDLE: u8 = 1;
    const RIGHT: u8 = 2;
    const RELEASE: u8 = 3;
    const MOTION_NO_BUTTON: u8 = 35;
    const WHEEL_UP: u8 = 64;
    const WHEEL_DOWN: u8 = 65;
    const WHEEL_LEFT: u8 = 66;
    const WHEEL_RIGHT: u8 = 67;
};

const ModifierMask = struct {
    const SHIFT: u8 = 4;
    const ALT: u8 = 8;
    const CTRL: u8 = 16;
    const MOTION: u8 = 32;
};

const X10_OFFSET: u8 = 32;

pub const TerminalState = struct {
    flags: @FieldType(ghostty.Terminal, "flags"),
    modes: ghostty.modes.ModeState,
    cols: u16,
    rows: u16,
    width_px: u32,
    height_px: u32,

    pub fn init(terminal: *const ghostty.Terminal) TerminalState {
        return .{
            .flags = terminal.flags,
            .modes = terminal.modes,
            .cols = terminal.cols,
            .rows = terminal.rows,
            .width_px = terminal.width_px,
            .height_px = terminal.height_px,
        };
    }
};

pub fn encode(
    writer: anytype,
    event: key_parse.MouseEvent,
    state: TerminalState,
) !void {
    const flags = state.flags;

    // Check if mouse reporting is enabled
    if (flags.mouse_event == .none) return;

    // Filter based on event type and enabled mode
    const report = switch (flags.mouse_event) {
        .x10 => event.type == .press, // X10 only reports press
        .normal => event.type == .press or event.type == .release,
        .button => event.type == .press or event.type == .release or event.type == .drag,
        .any => true, // Report everything including motion
        .none => false,
    };

    if (!report) return;

    // Compute cell coordinates from float (floor)
    const col: u16 = @intFromFloat(@max(0, @floor(event.x)));
    const row: u16 = @intFromFloat(@max(0, @floor(event.y)));

    // SGR encoding (1006)
    if (flags.mouse_format == .sgr) {
        try encodeSGR(writer, col, row, event);
        return;
    }

    // SGR pixels (1016) - compute pixel coordinates from float
    if (flags.mouse_format == .sgr_pixels) {
        const cell_width: f64 = if (state.cols > 0 and state.width_px > 0)
            @as(f64, @floatFromInt(state.width_px)) / @as(f64, @floatFromInt(state.cols))
        else
            1.0;
        const cell_height: f64 = if (state.rows > 0 and state.height_px > 0)
            @as(f64, @floatFromInt(state.height_px)) / @as(f64, @floatFromInt(state.rows))
        else
            1.0;

        const px_x: u16 = @intFromFloat(@max(0, @round(event.x * cell_width)));
        const px_y: u16 = @intFromFloat(@max(0, @round(event.y * cell_height)));

        try encodeSGR(writer, px_x, px_y, event);
        return;
    }

    // Fallback to X10/Normal (max 223 coords)
    // If coordinates are too large for X10, we skip reporting
    if (col > X10_COORD_LIMIT or row > X10_COORD_LIMIT) return;

    try encodeX10(writer, col, row, event);
}

fn encodeSGR(writer: anytype, col: u16, row: u16, event: key_parse.MouseEvent) !void {
    var cb: u8 = 0;

    // Button mapping
    switch (event.button) {
        .left => cb = ButtonCode.LEFT,
        .middle => cb = ButtonCode.MIDDLE,
        .right => cb = ButtonCode.RIGHT,
        .wheel_up => cb = ButtonCode.WHEEL_UP,
        .wheel_down => cb = ButtonCode.WHEEL_DOWN,
        .wheel_left => cb = ButtonCode.WHEEL_LEFT,
        .wheel_right => cb = ButtonCode.WHEEL_RIGHT,
        .none => if (event.type == .motion) {
            cb = ButtonCode.MOTION_NO_BUTTON;
        } else {
            cb = ButtonCode.LEFT;
        },
    }

    // Modifiers
    if (event.mods.shift) cb |= ModifierMask.SHIFT;
    if (event.mods.alt) cb |= ModifierMask.ALT;
    if (event.mods.ctrl) cb |= ModifierMask.CTRL;

    // Drag/Motion
    if (event.type == .drag) cb |= ModifierMask.MOTION;
    if (event.type == .motion) cb |= ModifierMask.MOTION;

    // Format: CSI < Cb ; Cx ; Cy M (or m for release)
    const char: u8 = if (event.type == .release) 'm' else 'M';

    try writer.print("\x1b[<{};{};{}{c}", .{ cb, col + 1, row + 1, char });
}

fn encodeX10(writer: anytype, col: u16, row: u16, event: key_parse.MouseEvent) !void {
    var cb: u8 = 0;
    switch (event.button) {
        .left => cb = ButtonCode.LEFT,
        .middle => cb = ButtonCode.MIDDLE,
        .right => cb = ButtonCode.RIGHT,
        .wheel_up => cb = ButtonCode.WHEEL_UP,
        .wheel_down => cb = ButtonCode.WHEEL_DOWN,
        .wheel_left => cb = ButtonCode.WHEEL_LEFT,
        .wheel_right => cb = ButtonCode.WHEEL_RIGHT,
        .none => cb = ButtonCode.LEFT,
    }

    if (event.type == .release) cb = ButtonCode.RELEASE;
    if (event.type == .drag) cb += ModifierMask.MOTION;
    if (event.type == .motion) cb += ModifierMask.MOTION;

    if (event.mods.shift) cb |= ModifierMask.SHIFT;
    if (event.mods.alt) cb |= ModifierMask.ALT;
    if (event.mods.ctrl) cb |= ModifierMask.CTRL;

    try writer.print("\x1b[M{c}{c}{c}", .{ cb + X10_OFFSET, @as(u8, @intCast(col + 1)) + X10_OFFSET, @as(u8, @intCast(row + 1)) + X10_OFFSET });
}
