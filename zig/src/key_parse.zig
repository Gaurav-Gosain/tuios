//! Keyboard input parsing from MessagePack format.

const std = @import("std");

const ghostty = @import("ghostty-vt");

const msgpack = @import("msgpack.zig");

const log = std.log.scoped(.key_parse);

const KeyEvent = ghostty.input.KeyEvent;
const Key = ghostty.input.Key;

// Mods isn't publicly exposed, but we need it
const Mods = packed struct(u16) {
    shift: bool = false,
    ctrl: bool = false,
    alt: bool = false,
    super: bool = false,
    caps_lock: bool = false,
    num_lock: bool = false,
    sides: u4 = 0,
    _padding: u6 = 0,
};

pub const MouseButton = enum {
    left,
    middle,
    right,
    wheel_up,
    wheel_down,
    wheel_left,
    wheel_right,
    none,
};

pub const MouseEventType = enum {
    press,
    release,
    motion,
    drag,
};

pub const MouseEvent = struct {
    x: f64,
    y: f64,
    button: MouseButton,
    type: MouseEventType,
    mods: Mods,
};

/// Parse mouse from msgpack map
pub fn parseMouseMap(map: msgpack.Value) !MouseEvent {
    if (map != .map) return error.InvalidMouseFormat;

    var x: f64 = 0;
    var y: f64 = 0;
    var button: MouseButton = .none;
    var type_: MouseEventType = .press;
    var mods: Mods = .{};

    for (map.map) |entry| {
        if (entry.key != .string) continue;
        const field = entry.key.string;

        if (std.mem.eql(u8, field, "x")) {
            x = switch (entry.value) {
                .float => entry.value.float,
                .unsigned => @floatFromInt(entry.value.unsigned),
                .integer => @floatFromInt(entry.value.integer),
                else => 0,
            };
        } else if (std.mem.eql(u8, field, "y")) {
            y = switch (entry.value) {
                .float => entry.value.float,
                .unsigned => @floatFromInt(entry.value.unsigned),
                .integer => @floatFromInt(entry.value.integer),
                else => 0,
            };
        } else if (std.mem.eql(u8, field, "button")) {
            if (entry.value == .string) {
                const s = entry.value.string;
                if (std.mem.eql(u8, s, "left")) {
                    button = .left;
                } else if (std.mem.eql(u8, s, "middle")) {
                    button = .middle;
                } else if (std.mem.eql(u8, s, "right")) {
                    button = .right;
                } else if (std.mem.eql(u8, s, "wheel_up")) {
                    button = .wheel_up;
                } else if (std.mem.eql(u8, s, "wheel_down")) {
                    button = .wheel_down;
                } else if (std.mem.eql(u8, s, "wheel_left")) {
                    button = .wheel_left;
                } else if (std.mem.eql(u8, s, "wheel_right")) {
                    button = .wheel_right;
                }
            }
        } else if (std.mem.eql(u8, field, "event_type")) {
            if (entry.value == .string) {
                const s = entry.value.string;
                if (std.mem.eql(u8, s, "press")) {
                    type_ = .press;
                } else if (std.mem.eql(u8, s, "release")) {
                    type_ = .release;
                } else if (std.mem.eql(u8, s, "motion")) {
                    type_ = .motion;
                } else if (std.mem.eql(u8, s, "drag")) {
                    type_ = .drag;
                }
            }
        } else if (std.mem.eql(u8, field, "shiftKey")) {
            if (entry.value == .boolean) mods.shift = entry.value.boolean;
        } else if (std.mem.eql(u8, field, "ctrlKey")) {
            if (entry.value == .boolean) mods.ctrl = entry.value.boolean;
        } else if (std.mem.eql(u8, field, "altKey")) {
            if (entry.value == .boolean) mods.alt = entry.value.boolean;
        }
    }

    return .{
        .x = x,
        .y = y,
        .button = button,
        .type = type_,
        .mods = mods,
    };
}

/// Parse key from msgpack map to ghostty KeyEvent
/// Expected format: { "key": "a", "code": "KeyA", "shiftKey": false, "ctrlKey": false, "altKey": false, "metaKey": false }
pub fn parseKeyMap(map: msgpack.Value) !KeyEvent {
    return parseKeyMapWithAction(map, .press);
}

pub fn parseKeyMapWithAction(map: msgpack.Value, action: ghostty.input.KeyAction) !KeyEvent {
    if (map != .map) return error.InvalidKeyFormat;

    var key_str: ?[]const u8 = null; // W3C "key" - produced character
    var code_str: ?[]const u8 = null; // W3C "code" - physical key
    var mods: Mods = .{};

    for (map.map) |entry| {
        if (entry.key != .string) continue;
        const field = entry.key.string;

        if (std.mem.eql(u8, field, "key")) {
            if (entry.value == .string) {
                key_str = entry.value.string;
            }
        } else if (std.mem.eql(u8, field, "code")) {
            if (entry.value == .string) {
                code_str = entry.value.string;
            }
        } else if (std.mem.eql(u8, field, "shiftKey")) {
            if (entry.value == .boolean) mods.shift = entry.value.boolean;
        } else if (std.mem.eql(u8, field, "ctrlKey")) {
            if (entry.value == .boolean) mods.ctrl = entry.value.boolean;
        } else if (std.mem.eql(u8, field, "altKey")) {
            if (entry.value == .boolean) mods.alt = entry.value.boolean;
        } else if (std.mem.eql(u8, field, "metaKey")) {
            if (entry.value == .boolean) mods.super = entry.value.boolean;
        }
    }

    const code = code_str orelse return error.MissingCode;
    const key = key_str orelse "";

    // Map physical key code to ghostty Key enum
    const key_enum = mapCodeToKey(code);

    // utf8 should only contain actual character data, not key names
    // If key equals code (e.g., "Backspace" == "Backspace"), it's a named key with no character
    const utf8 = if (std.mem.eql(u8, key, code)) "" else key;

    // For kitty keyboard protocol, we need unshifted_codepoint for character keys.
    // This is used by the encoder to generate CSI u sequences.
    // For letter keys, derive from the key enum. Otherwise use the first UTF-8 codepoint.
    const unshifted_codepoint: u21 = key_enum.codepoint() orelse codepoint: {
        if (utf8.len == 0) break :codepoint 0;
        const view = std.unicode.Utf8View.init(utf8) catch break :codepoint 0;
        var it = view.iterator();
        const cp = it.nextCodepoint() orelse break :codepoint 0;
        // If shift is held, try to get the lowercase version for letters
        if (mods.shift and cp >= 'A' and cp <= 'Z') {
            break :codepoint std.ascii.toLower(@intCast(cp));
        }
        break :codepoint cp;
    };

    // Determine consumed_mods: if shift was used to produce a different character
    // (e.g., shift+; produces ":"), mark shift as consumed so the encoder treats
    // it as a plain character, not as shift+semicolon.
    const utf8_codepoint: u21 = utf8_cp: {
        if (utf8.len == 0) break :utf8_cp 0;
        const view = std.unicode.Utf8View.init(utf8) catch break :utf8_cp 0;
        var it = view.iterator();
        break :utf8_cp it.nextCodepoint() orelse 0;
    };

    const shift_consumed = mods.shift and unshifted_codepoint != 0 and
        utf8_codepoint != 0 and unshifted_codepoint != utf8_codepoint;

    return .{
        .action = action,
        .key = key_enum,
        .utf8 = utf8,
        .mods = @bitCast(mods),
        .consumed_mods = .{ .shift = shift_consumed },
        .unshifted_codepoint = unshifted_codepoint,
    };
}

// W3C code -> ghostty Key mapping
const code_map = std.StaticStringMap(Key).initComptime(.{
    // Special keys
    .{ "Enter", .enter },
    .{ "Tab", .tab },
    .{ "Backspace", .backspace },
    .{ "Escape", .escape },
    .{ "Space", .space },
    .{ "Delete", .delete },
    .{ "Insert", .insert },
    .{ "Home", .home },
    .{ "End", .end },
    .{ "PageUp", .page_up },
    .{ "PageDown", .page_down },
    .{ "ArrowUp", .arrow_up },
    .{ "ArrowDown", .arrow_down },
    .{ "ArrowLeft", .arrow_left },
    .{ "ArrowRight", .arrow_right },
    // Function keys
    .{ "F1", .f1 },
    .{ "F2", .f2 },
    .{ "F3", .f3 },
    .{ "F4", .f4 },
    .{ "F5", .f5 },
    .{ "F6", .f6 },
    .{ "F7", .f7 },
    .{ "F8", .f8 },
    .{ "F9", .f9 },
    .{ "F10", .f10 },
    .{ "F11", .f11 },
    .{ "F12", .f12 },
    // Modifier keys
    .{ "ShiftLeft", .shift_left },
    .{ "ShiftRight", .shift_right },
    .{ "ControlLeft", .control_left },
    .{ "ControlRight", .control_right },
    .{ "AltLeft", .alt_left },
    .{ "AltRight", .alt_right },
    .{ "MetaLeft", .meta_left },
    .{ "MetaRight", .meta_right },
    .{ "CapsLock", .caps_lock },
    .{ "NumLock", .num_lock },
    .{ "ScrollLock", .scroll_lock },
    // Letter keys
    .{ "KeyA", .key_a },
    .{ "KeyB", .key_b },
    .{ "KeyC", .key_c },
    .{ "KeyD", .key_d },
    .{ "KeyE", .key_e },
    .{ "KeyF", .key_f },
    .{ "KeyG", .key_g },
    .{ "KeyH", .key_h },
    .{ "KeyI", .key_i },
    .{ "KeyJ", .key_j },
    .{ "KeyK", .key_k },
    .{ "KeyL", .key_l },
    .{ "KeyM", .key_m },
    .{ "KeyN", .key_n },
    .{ "KeyO", .key_o },
    .{ "KeyP", .key_p },
    .{ "KeyQ", .key_q },
    .{ "KeyR", .key_r },
    .{ "KeyS", .key_s },
    .{ "KeyT", .key_t },
    .{ "KeyU", .key_u },
    .{ "KeyV", .key_v },
    .{ "KeyW", .key_w },
    .{ "KeyX", .key_x },
    .{ "KeyY", .key_y },
    .{ "KeyZ", .key_z },
    // Digit keys
    .{ "Digit0", .digit_0 },
    .{ "Digit1", .digit_1 },
    .{ "Digit2", .digit_2 },
    .{ "Digit3", .digit_3 },
    .{ "Digit4", .digit_4 },
    .{ "Digit5", .digit_5 },
    .{ "Digit6", .digit_6 },
    .{ "Digit7", .digit_7 },
    .{ "Digit8", .digit_8 },
    .{ "Digit9", .digit_9 },
    // Punctuation
    .{ "Minus", .minus },
    .{ "Equal", .equal },
    .{ "BracketLeft", .bracket_left },
    .{ "BracketRight", .bracket_right },
    .{ "Backslash", .backslash },
    .{ "Semicolon", .semicolon },
    .{ "Quote", .quote },
    .{ "Backquote", .backquote },
    .{ "Comma", .comma },
    .{ "Period", .period },
    .{ "Slash", .slash },
});

fn mapCodeToKey(code: []const u8) Key {
    return code_map.get(code) orelse .unidentified;
}
