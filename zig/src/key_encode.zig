//! Keyboard input encoding for terminal protocols.

const std = @import("std");

const ghostty = @import("ghostty-vt");

const log = std.log.scoped(.key_encode);

pub const OptionAsAlt = @TypeOf(@as(ghostty.input.KeyEncodeOptions, undefined).macos_option_as_alt);

pub fn encode(
    writer: anytype,
    key: ghostty.input.KeyEvent,
    terminal: *const ghostty.Terminal,
    macos_option_as_alt: OptionAsAlt,
) !void {
    var opts = ghostty.input.KeyEncodeOptions.fromTerminal(terminal);
    opts.macos_option_as_alt = macos_option_as_alt;
    try ghostty.input.encodeKey(writer, key, opts);
}

test "encode key" {
    var buf: [32]u8 = undefined;
    var writer = std.Io.Writer.fixed(&buf);

    const key: ghostty.input.KeyEvent = .{
        .key = .arrow_up,
        .utf8 = "",
        .mods = .{},
    };

    // Test normal mode
    var opts: ghostty.input.KeyEncodeOptions = .{
        .cursor_key_application = false,
        .keypad_key_application = false,
        .ignore_keypad_with_numlock = false,
        .alt_esc_prefix = false,
        .modify_other_keys_state_2 = false,
        .kitty_flags = .{},
        .macos_option_as_alt = .false,
    };

    try ghostty.input.encodeKey(&writer, key, opts);
    try std.testing.expectEqualSlices(u8, "\x1b[A", writer.buffered());

    // Test application mode
    writer.end = 0;
    opts.cursor_key_application = true;
    try ghostty.input.encodeKey(&writer, key, opts);
    try std.testing.expectEqualSlices(u8, "\x1bOA", writer.buffered());
}
