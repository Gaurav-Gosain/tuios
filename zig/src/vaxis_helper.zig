//! Helper utilities for vaxis terminal library.

const std = @import("std");

const vaxis = @import("vaxis");

const log = std.log.scoped(.vaxis_helper);

pub const KeyStrings = struct {
    key: []const u8, // W3C "key" - produced character
    code: []const u8, // W3C "code" - physical key name
};

pub fn vaxisKeyToStrings(allocator: std.mem.Allocator, key: vaxis.Key) !KeyStrings {
    const code = try codepointToCode(allocator, key.codepoint);

    // "key" is the produced text, or the code name for non-printable keys
    const key_str = if (key.text) |text|
        try allocator.dupe(u8, text)
    else if (isNamedKey(key.codepoint))
        try allocator.dupe(u8, code)
    else blk: {
        // Encode codepoint as UTF-8
        var buf: [4]u8 = undefined;
        const len = std.unicode.utf8Encode(key.codepoint, &buf) catch
            break :blk try allocator.dupe(u8, "Unidentified");
        break :blk try allocator.dupe(u8, buf[0..len]);
    };

    return .{ .key = key_str, .code = code };
}

fn isNamedKey(codepoint: u21) bool {
    const Key = vaxis.Key;
    return switch (codepoint) {
        Key.enter,
        Key.tab,
        Key.backspace,
        Key.escape,
        Key.space,
        Key.delete,
        Key.insert,
        Key.home,
        Key.end,
        Key.page_up,
        Key.page_down,
        Key.up,
        Key.down,
        Key.left,
        Key.right,
        Key.f1,
        Key.f2,
        Key.f3,
        Key.f4,
        Key.f5,
        Key.f6,
        Key.f7,
        Key.f8,
        Key.f9,
        Key.f10,
        Key.f11,
        Key.f12,
        Key.left_shift,
        Key.right_shift,
        Key.left_control,
        Key.right_control,
        Key.left_alt,
        Key.right_alt,
        Key.left_super,
        Key.right_super,
        Key.caps_lock,
        Key.num_lock,
        Key.scroll_lock,
        => true,
        else => false,
    };
}

fn codepointToCode(allocator: std.mem.Allocator, codepoint: u21) ![]const u8 {
    const code = specialKeyCode(codepoint) orelse
        letterKeyCode(codepoint) orelse
        digitKeyCode(codepoint) orelse
        punctuationKeyCode(codepoint) orelse
        "Unidentified";

    return try allocator.dupe(u8, code);
}

fn specialKeyCode(codepoint: u21) ?[]const u8 {
    const Key = vaxis.Key;
    return switch (codepoint) {
        Key.enter => "Enter",
        Key.tab => "Tab",
        Key.backspace => "Backspace",
        Key.escape => "Escape",
        Key.space => "Space",
        Key.delete => "Delete",
        Key.insert => "Insert",
        Key.home => "Home",
        Key.end => "End",
        Key.page_up => "PageUp",
        Key.page_down => "PageDown",
        Key.up => "ArrowUp",
        Key.down => "ArrowDown",
        Key.left => "ArrowLeft",
        Key.right => "ArrowRight",
        Key.f1 => "F1",
        Key.f2 => "F2",
        Key.f3 => "F3",
        Key.f4 => "F4",
        Key.f5 => "F5",
        Key.f6 => "F6",
        Key.f7 => "F7",
        Key.f8 => "F8",
        Key.f9 => "F9",
        Key.f10 => "F10",
        Key.f11 => "F11",
        Key.f12 => "F12",
        Key.left_shift => "ShiftLeft",
        Key.right_shift => "ShiftRight",
        Key.left_control => "ControlLeft",
        Key.right_control => "ControlRight",
        Key.left_alt => "AltLeft",
        Key.right_alt => "AltRight",
        Key.left_super => "MetaLeft",
        Key.right_super => "MetaRight",
        Key.caps_lock => "CapsLock",
        Key.num_lock => "NumLock",
        Key.scroll_lock => "ScrollLock",
        else => null,
    };
}

fn letterKeyCode(codepoint: u21) ?[]const u8 {
    return switch (codepoint) {
        'a' => "KeyA",
        'b' => "KeyB",
        'c' => "KeyC",
        'd' => "KeyD",
        'e' => "KeyE",
        'f' => "KeyF",
        'g' => "KeyG",
        'h' => "KeyH",
        'i' => "KeyI",
        'j' => "KeyJ",
        'k' => "KeyK",
        'l' => "KeyL",
        'm' => "KeyM",
        'n' => "KeyN",
        'o' => "KeyO",
        'p' => "KeyP",
        'q' => "KeyQ",
        'r' => "KeyR",
        's' => "KeyS",
        't' => "KeyT",
        'u' => "KeyU",
        'v' => "KeyV",
        'w' => "KeyW",
        'x' => "KeyX",
        'y' => "KeyY",
        'z' => "KeyZ",
        else => null,
    };
}

fn digitKeyCode(codepoint: u21) ?[]const u8 {
    return switch (codepoint) {
        '0' => "Digit0",
        '1' => "Digit1",
        '2' => "Digit2",
        '3' => "Digit3",
        '4' => "Digit4",
        '5' => "Digit5",
        '6' => "Digit6",
        '7' => "Digit7",
        '8' => "Digit8",
        '9' => "Digit9",
        else => null,
    };
}

fn punctuationKeyCode(codepoint: u21) ?[]const u8 {
    return switch (codepoint) {
        '-' => "Minus",
        '=' => "Equal",
        '[' => "BracketLeft",
        ']' => "BracketRight",
        '\\' => "Backslash",
        ';' => "Semicolon",
        '\'' => "Quote",
        '`' => "Backquote",
        ',' => "Comma",
        '.' => "Period",
        '/' => "Slash",
        else => null,
    };
}
