//! Test utilities for widget rendering verification.
//!
//! Provides helpers to convert vaxis.Screen contents to ASCII strings
//! for visual comparison in tests.

const std = @import("std");
const vaxis = @import("vaxis");

/// Convert a vaxis.Screen to an ASCII string representation.
/// Multi-byte graphemes are collapsed to '?' for visual debugging.
/// Each row is separated by a newline.
pub fn screenToAscii(
    allocator: std.mem.Allocator,
    screen: *const vaxis.Screen,
    width: u16,
    height: u16,
) ![]u8 {
    var buf: std.ArrayList(u8) = .empty;
    errdefer buf.deinit(allocator);

    for (0..height) |row| {
        if (row != 0) try buf.append(allocator, '\n');
        var skip_cols: u16 = 0;
        for (0..width) |col| {
            if (skip_cols > 0) {
                skip_cols -= 1;
                continue;
            }
            const cell = screen.readCell(@intCast(col), @intCast(row));

            if (cell) |c| {
                const g = c.char.grapheme;
                if (g.len == 0) {
                    try buf.append(allocator, ' ');
                } else if (g.len == 1) {
                    try buf.append(allocator, g[0]);
                } else {
                    try buf.appendSlice(allocator, g);
                }
                if (c.char.width > 1) {
                    skip_cols = c.char.width - 1;
                }
            } else {
                try buf.append(allocator, ' ');
            }
        }
    }

    return buf.toOwnedSlice(allocator);
}

/// Convert a vaxis.Screen to an ASCII string with row numbers for debugging.
pub fn screenToAsciiDebug(
    allocator: std.mem.Allocator,
    screen: *const vaxis.Screen,
    width: u16,
    height: u16,
) ![]u8 {
    const base = try screenToAscii(allocator, screen, width, height);
    defer allocator.free(base);

    var buf: std.ArrayList(u8) = .empty;
    errdefer buf.deinit(allocator);

    var it = std.mem.splitScalar(u8, base, '\n');
    var row: usize = 0;
    while (it.next()) |line| {
        if (row != 0) try buf.append(allocator, '\n');
        try buf.writer(allocator).print("{d:02}: {s}", .{ row, line });
        row += 1;
    }

    return buf.toOwnedSlice(allocator);
}

/// Compare ASCII representations and print both on failure.
pub fn expectAsciiEqual(expected: []const u8, actual: []const u8) !void {
    if (!std.mem.eql(u8, expected, actual)) {
        std.debug.print("\n=== EXPECTED ({d} bytes) ===\n{s}\n=== ACTUAL ({d} bytes) ===\n{s}\n=== END ===\n", .{
            expected.len,
            expected,
            actual.len,
            actual,
        });
    }
    try std.testing.expectEqualStrings(expected, actual);
}

/// Create a root vaxis.Window from a Screen.
pub fn windowFromScreen(screen: *vaxis.Screen) vaxis.Window {
    return .{
        .x_off = 0,
        .y_off = 0,
        .parent_x_off = 0,
        .parent_y_off = 0,
        .width = screen.width,
        .height = screen.height,
        .screen = screen,
    };
}

/// Create a vaxis.Screen with the given dimensions.
pub fn createScreen(allocator: std.mem.Allocator, width: u16, height: u16) !vaxis.Screen {
    return vaxis.Screen.init(allocator, .{
        .cols = width,
        .rows = height,
        .x_pixel = 0,
        .y_pixel = 0,
    });
}

test "screenToAscii - empty screen" {
    const allocator = std.testing.allocator;

    var screen = try createScreen(allocator, 5, 2);
    defer screen.deinit(allocator);

    const ascii = try screenToAscii(allocator, &screen, 5, 2);
    defer allocator.free(ascii);

    const expected =
        \\     
        \\     
    ;
    try expectAsciiEqual(expected, ascii);
}

test "screenToAscii - with content" {
    const allocator = std.testing.allocator;

    var screen = try createScreen(allocator, 5, 2);
    defer screen.deinit(allocator);

    screen.writeCell(0, 0, .{ .char = .{ .grapheme = "H", .width = 1 } });
    screen.writeCell(1, 0, .{ .char = .{ .grapheme = "i", .width = 1 } });
    screen.writeCell(0, 1, .{ .char = .{ .grapheme = "!", .width = 1 } });

    const ascii = try screenToAscii(allocator, &screen, 5, 2);
    defer allocator.free(ascii);

    const expected =
        \\Hi   
        \\!    
    ;
    try expectAsciiEqual(expected, ascii);
}

test "windowFromScreen - write through window" {
    const allocator = std.testing.allocator;

    var screen = try createScreen(allocator, 10, 3);
    defer screen.deinit(allocator);

    var win = windowFromScreen(&screen);
    win.writeCell(0, 0, .{ .char = .{ .grapheme = "A", .width = 1 } });
    win.writeCell(9, 2, .{ .char = .{ .grapheme = "Z", .width = 1 } });

    const ascii = try screenToAscii(allocator, &screen, 10, 3);
    defer allocator.free(ascii);

    const expected =
        \\A         
        \\          
        \\         Z
    ;
    try expectAsciiEqual(expected, ascii);
}

test "screenToAscii - unicode graphemes" {
    const allocator = std.testing.allocator;

    var screen = try createScreen(allocator, 5, 1);
    defer screen.deinit(allocator);

    screen.writeCell(0, 0, .{ .char = .{ .grapheme = "╭", .width = 1 } });
    screen.writeCell(1, 0, .{ .char = .{ .grapheme = "─", .width = 1 } });
    screen.writeCell(2, 0, .{ .char = .{ .grapheme = "─", .width = 1 } });
    screen.writeCell(3, 0, .{ .char = .{ .grapheme = "─", .width = 1 } });
    screen.writeCell(4, 0, .{ .char = .{ .grapheme = "╮", .width = 1 } });

    const ascii = try screenToAscii(allocator, &screen, 5, 1);
    defer allocator.free(ascii);

    try expectAsciiEqual("╭───╮", ascii);
}
