//! Parse vim-style key strings into key sequences.
//!
//! Parses strings like `<D-k>vg` into a sequence of key objects:
//! `[{key="k", super=true}, {key="v"}, {key="g"}]`
//!
//! Modifiers: C- (ctrl), A- (alt), S- (shift), D- (super)
//! Special keys: <Enter>, <Tab>, <Esc>, <Space>, <BS>, etc.

const std = @import("std");

const log = std.log.scoped(.key_string);

pub const Key = struct {
    key: []const u8,
    ctrl: bool = false,
    alt: bool = false,
    shift: bool = false,
    super: bool = false,
};

pub const ParseError = error{
    InvalidFormat,
    UnterminatedBracket,
    EmptyKey,
    UnknownSpecialKey,
};

pub fn parseKeyString(allocator: std.mem.Allocator, input: []const u8) ParseError![]Key {
    var keys: std.ArrayList(Key) = .empty;
    errdefer keys.deinit(allocator);

    var i: usize = 0;
    while (i < input.len) {
        if (input[i] == '<') {
            const key = try parseBracketedKey(input, &i);
            keys.append(allocator, key) catch return error.InvalidFormat;
        } else {
            const key = Key{ .key = input[i .. i + 1] };
            keys.append(allocator, key) catch return error.InvalidFormat;
            i += 1;
        }
    }

    return keys.toOwnedSlice(allocator) catch return error.InvalidFormat;
}

fn parseBracketedKey(input: []const u8, pos: *usize) ParseError!Key {
    std.debug.assert(input[pos.*] == '<');
    pos.* += 1;

    const start = pos.*;
    while (pos.* < input.len and input[pos.*] != '>') {
        pos.* += 1;
    }

    if (pos.* >= input.len) return error.UnterminatedBracket;

    const content = input[start..pos.*];
    pos.* += 1; // skip '>'

    if (content.len == 0) return error.EmptyKey;

    var key = Key{ .key = "" };
    var remaining = content;

    while (remaining.len >= 2 and remaining[1] == '-') {
        const mod = remaining[0];
        switch (mod) {
            'C', 'c' => key.ctrl = true,
            'A', 'a' => key.alt = true,
            'S', 's' => key.shift = true,
            'D', 'd' => key.super = true,
            else => break,
        }
        remaining = remaining[2..];
    }

    if (remaining.len == 0) return error.EmptyKey;

    // Handle <lt> and <gt> as literal < and > characters (vim-style)
    if (std.mem.eql(u8, remaining, "lt")) {
        key.key = "<";
        return key;
    }
    if (std.mem.eql(u8, remaining, "gt")) {
        key.key = ">";
        return key;
    }

    key.key = mapSpecialKey(remaining) orelse remaining;
    return key;
}

fn mapSpecialKey(name: []const u8) ?[]const u8 {
    const map = std.StaticStringMap([]const u8).initComptime(.{
        .{ "Enter", "Enter" },
        .{ "Return", "Enter" },
        .{ "CR", "Enter" },
        .{ "Tab", "Tab" },
        .{ "Esc", "Escape" },
        .{ "Escape", "Escape" },
        .{ "Space", "Space" },
        .{ "BS", "Backspace" },
        .{ "Backspace", "Backspace" },
        .{ "Del", "Delete" },
        .{ "Delete", "Delete" },
        .{ "Up", "Up" },
        .{ "Down", "Down" },
        .{ "Left", "Left" },
        .{ "Right", "Right" },
        .{ "Home", "Home" },
        .{ "End", "End" },
        .{ "PageUp", "PageUp" },
        .{ "PageDown", "PageDown" },
        .{ "Insert", "Insert" },
        .{ "F1", "F1" },
        .{ "F2", "F2" },
        .{ "F3", "F3" },
        .{ "F4", "F4" },
        .{ "F5", "F5" },
        .{ "F6", "F6" },
        .{ "F7", "F7" },
        .{ "F8", "F8" },
        .{ "F9", "F9" },
        .{ "F10", "F10" },
        .{ "F11", "F11" },
        .{ "F12", "F12" },
        .{ "Comma", "Comma" },
        .{ "Period", "Period" },
        .{ "Minus", "Minus" },
        .{ "Equal", "Equal" },
        .{ "Slash", "Slash" },
        .{ "Backslash", "Backslash" },
        .{ "BracketLeft", "BracketLeft" },
        .{ "BracketRight", "BracketRight" },
        .{ "Semicolon", "Semicolon" },
        .{ "Quote", "Quote" },
        .{ "Backquote", "Backquote" },
    });
    return map.get(name);
}

test "parse simple character" {
    const keys = try parseKeyString(std.testing.allocator, "a");
    defer std.testing.allocator.free(keys);

    try std.testing.expectEqual(1, keys.len);
    try std.testing.expectEqualStrings("a", keys[0].key);
    try std.testing.expect(!keys[0].ctrl);
    try std.testing.expect(!keys[0].alt);
    try std.testing.expect(!keys[0].shift);
    try std.testing.expect(!keys[0].super);
}

test "parse sequence" {
    const keys = try parseKeyString(std.testing.allocator, "vg");
    defer std.testing.allocator.free(keys);

    try std.testing.expectEqual(2, keys.len);
    try std.testing.expectEqualStrings("v", keys[0].key);
    try std.testing.expectEqualStrings("g", keys[1].key);
}

test "parse bracketed key with modifier" {
    const keys = try parseKeyString(std.testing.allocator, "<D-k>");
    defer std.testing.allocator.free(keys);

    try std.testing.expectEqual(1, keys.len);
    try std.testing.expectEqualStrings("k", keys[0].key);
    try std.testing.expect(keys[0].super);
    try std.testing.expect(!keys[0].ctrl);
}

test "parse multiple modifiers" {
    const keys = try parseKeyString(std.testing.allocator, "<C-S-a>");
    defer std.testing.allocator.free(keys);

    try std.testing.expectEqual(1, keys.len);
    try std.testing.expectEqualStrings("a", keys[0].key);
    try std.testing.expect(keys[0].ctrl);
    try std.testing.expect(keys[0].shift);
    try std.testing.expect(!keys[0].alt);
    try std.testing.expect(!keys[0].super);
}

test "parse mixed sequence" {
    const keys = try parseKeyString(std.testing.allocator, "<D-k>vg");
    defer std.testing.allocator.free(keys);

    try std.testing.expectEqual(3, keys.len);
    try std.testing.expectEqualStrings("k", keys[0].key);
    try std.testing.expect(keys[0].super);
    try std.testing.expectEqualStrings("v", keys[1].key);
    try std.testing.expect(!keys[1].super);
    try std.testing.expectEqualStrings("g", keys[2].key);
}

test "parse special keys" {
    const keys = try parseKeyString(std.testing.allocator, "<Enter>");
    defer std.testing.allocator.free(keys);

    try std.testing.expectEqual(1, keys.len);
    try std.testing.expectEqualStrings("Enter", keys[0].key);
}

test "parse special key with modifier" {
    const keys = try parseKeyString(std.testing.allocator, "<C-Enter>");
    defer std.testing.allocator.free(keys);

    try std.testing.expectEqual(1, keys.len);
    try std.testing.expectEqualStrings("Enter", keys[0].key);
    try std.testing.expect(keys[0].ctrl);
}

test "parse escape variants" {
    {
        const keys = try parseKeyString(std.testing.allocator, "<Esc>");
        defer std.testing.allocator.free(keys);
        try std.testing.expectEqualStrings("Escape", keys[0].key);
    }
    {
        const keys = try parseKeyString(std.testing.allocator, "<Escape>");
        defer std.testing.allocator.free(keys);
        try std.testing.expectEqualStrings("Escape", keys[0].key);
    }
}

test "parse all modifiers" {
    const keys = try parseKeyString(std.testing.allocator, "<C-A-S-D-x>");
    defer std.testing.allocator.free(keys);

    try std.testing.expectEqual(1, keys.len);
    try std.testing.expectEqualStrings("x", keys[0].key);
    try std.testing.expect(keys[0].ctrl);
    try std.testing.expect(keys[0].alt);
    try std.testing.expect(keys[0].shift);
    try std.testing.expect(keys[0].super);
}

test "error: unterminated bracket" {
    const result = parseKeyString(std.testing.allocator, "<C-k");
    try std.testing.expectError(error.UnterminatedBracket, result);
}

test "error: empty bracket" {
    const result = parseKeyString(std.testing.allocator, "<>");
    try std.testing.expectError(error.EmptyKey, result);
}

test "error: only modifier" {
    const result = parseKeyString(std.testing.allocator, "<C->");
    try std.testing.expectError(error.EmptyKey, result);
}

test "lowercase modifiers" {
    const keys = try parseKeyString(std.testing.allocator, "<c-a-s>");
    defer std.testing.allocator.free(keys);

    try std.testing.expectEqual(1, keys.len);
    try std.testing.expectEqualStrings("s", keys[0].key);
    try std.testing.expect(keys[0].ctrl);
    try std.testing.expect(keys[0].alt);
}

test "parse lt and gt" {
    {
        const keys = try parseKeyString(std.testing.allocator, "<lt>");
        defer std.testing.allocator.free(keys);
        try std.testing.expectEqual(1, keys.len);
        try std.testing.expectEqualStrings("<", keys[0].key);
        try std.testing.expect(!keys[0].shift);
    }
    {
        const keys = try parseKeyString(std.testing.allocator, "<gt>");
        defer std.testing.allocator.free(keys);
        try std.testing.expectEqual(1, keys.len);
        try std.testing.expectEqualStrings(">", keys[0].key);
        try std.testing.expect(!keys[0].shift);
    }
}
