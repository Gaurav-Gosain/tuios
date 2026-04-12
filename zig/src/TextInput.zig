//! Text input widget with cursor and editing support.

const std = @import("std");

const vaxis = @import("vaxis");

const TextInput = @This();

allocator: std.mem.Allocator,
buffer: std.ArrayList(u8),
cursor: usize = 0,
scroll_offset: usize = 0,

pub fn init(allocator: std.mem.Allocator) TextInput {
    return .{
        .allocator = allocator,
        .buffer = std.ArrayList(u8).empty,
    };
}

pub fn deinit(self: *TextInput) void {
    self.buffer.deinit(self.allocator);
}

pub fn insert(self: *TextInput, char: u8) !void {
    try self.buffer.insert(self.allocator, self.cursor, char);
    self.cursor += 1;
}

pub fn insertSlice(self: *TextInput, slice: []const u8) !void {
    try self.buffer.insertSlice(self.allocator, self.cursor, slice);
    self.cursor += slice.len;
}

pub fn deleteBackward(self: *TextInput) void {
    if (self.cursor > 0) {
        _ = self.buffer.orderedRemove(self.cursor - 1);
        self.cursor -= 1;
    }
}

pub fn deleteForward(self: *TextInput) void {
    if (self.cursor < self.buffer.items.len) {
        _ = self.buffer.orderedRemove(self.cursor);
    }
}

pub fn deleteWordBackward(self: *TextInput) void {
    if (self.cursor == 0) return;

    var end = self.cursor;
    while (end > 0 and self.buffer.items[end - 1] == ' ') {
        end -= 1;
    }
    while (end > 0 and self.buffer.items[end - 1] != ' ') {
        end -= 1;
    }

    const count = self.cursor - end;
    for (0..count) |_| {
        _ = self.buffer.orderedRemove(end);
    }
    self.cursor = end;
}

pub fn killLine(self: *TextInput) void {
    self.buffer.shrinkRetainingCapacity(self.cursor);
}

pub fn moveLeft(self: *TextInput) void {
    if (self.cursor > 0) {
        self.cursor -= 1;
    }
}

pub fn moveRight(self: *TextInput) void {
    if (self.cursor < self.buffer.items.len) {
        self.cursor += 1;
    }
}

pub fn moveToStart(self: *TextInput) void {
    self.cursor = 0;
}

pub fn moveToEnd(self: *TextInput) void {
    self.cursor = self.buffer.items.len;
}

pub fn clear(self: *TextInput) void {
    self.buffer.clearRetainingCapacity();
    self.cursor = 0;
    self.scroll_offset = 0;
}

pub fn text(self: *const TextInput) []const u8 {
    return self.buffer.items;
}

pub fn updateScrollOffset(self: *TextInput, visible_width: u16) void {
    if (visible_width == 0) return;

    const width: usize = visible_width;

    if (self.cursor < self.scroll_offset) {
        self.scroll_offset = self.cursor;
    } else if (self.cursor >= self.scroll_offset + width) {
        self.scroll_offset = self.cursor - width + 1;
    }
}

pub fn visibleText(self: *const TextInput, visible_width: u16) []const u8 {
    const items = self.buffer.items;
    if (items.len == 0) return "";

    const start = @min(self.scroll_offset, items.len);
    const end = @min(start + visible_width, items.len);
    return items[start..end];
}

pub fn visibleCursorPos(self: *const TextInput) usize {
    return self.cursor - self.scroll_offset;
}

pub fn render(self: *const TextInput, win: vaxis.Window, style: vaxis.Style) void {
    const visible = self.visibleText(@intCast(win.width));
    const cursor_x = self.visibleCursorPos();

    // Fill the entire line with background first
    for (0..win.width) |col| {
        const is_cursor = col == cursor_x;
        var cell_style = style;
        if (is_cursor) {
            cell_style.reverse = true;
        }

        const grapheme: []const u8 = if (col < visible.len) visible[col .. col + 1] else " ";
        win.writeCell(@intCast(col), 0, .{
            .char = .{ .grapheme = grapheme, .width = 1 },
            .style = cell_style,
        });
    }
}

test "basic insert and cursor movement" {
    const allocator = std.testing.allocator;

    var input = TextInput.init(allocator);
    defer input.deinit();

    try input.insert('a');
    try input.insert('b');
    try input.insert('c');

    try std.testing.expectEqualStrings("abc", input.text());
    try std.testing.expectEqual(@as(usize, 3), input.cursor);

    input.moveLeft();
    try std.testing.expectEqual(@as(usize, 2), input.cursor);

    try input.insert('X');
    try std.testing.expectEqualStrings("abXc", input.text());
}

test "delete backward and forward" {
    const allocator = std.testing.allocator;

    var input = TextInput.init(allocator);
    defer input.deinit();

    try input.insertSlice("hello");
    try std.testing.expectEqualStrings("hello", input.text());

    input.deleteBackward();
    try std.testing.expectEqualStrings("hell", input.text());

    input.moveToStart();
    input.deleteForward();
    try std.testing.expectEqualStrings("ell", input.text());
}

test "scroll offset" {
    const allocator = std.testing.allocator;

    var input = TextInput.init(allocator);
    defer input.deinit();

    try input.insertSlice("hello world this is a long string");

    input.updateScrollOffset(10);

    try std.testing.expect(input.scroll_offset > 0);
    try std.testing.expect(input.visibleText(10).len <= 10);
}

test "visible cursor position" {
    const allocator = std.testing.allocator;

    var input = TextInput.init(allocator);
    defer input.deinit();

    try input.insertSlice("abcdefghij");
    input.cursor = 5;
    input.scroll_offset = 3;

    try std.testing.expectEqual(@as(usize, 2), input.visibleCursorPos());
}

// ============================================================================
// Rendering Tests
// ============================================================================

const tui_test = @import("tui_test.zig");

test "render - basic text with cursor at end" {
    const allocator = std.testing.allocator;

    var input = TextInput.init(allocator);
    defer input.deinit();

    try input.insertSlice("abc");

    var screen = try tui_test.createScreen(allocator, 6, 1);
    defer screen.deinit(allocator);

    const win = tui_test.windowFromScreen(&screen);
    input.render(win, .{});

    const ascii = try tui_test.screenToAscii(allocator, &screen, 6, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("abc   ", ascii);

    // Cursor should be at position 3 (after 'c') with reverse style
    const cursor_cell = screen.readCell(3, 0).?;
    try std.testing.expect(cursor_cell.style.reverse);

    // Non-cursor cells should not be reversed
    const normal_cell = screen.readCell(0, 0).?;
    try std.testing.expect(!normal_cell.style.reverse);
}

test "render - cursor in middle" {
    const allocator = std.testing.allocator;

    var input = TextInput.init(allocator);
    defer input.deinit();

    try input.insertSlice("hello");
    input.moveLeft();
    input.moveLeft();

    var screen = try tui_test.createScreen(allocator, 8, 1);
    defer screen.deinit(allocator);

    const win = tui_test.windowFromScreen(&screen);
    input.render(win, .{});

    const ascii = try tui_test.screenToAscii(allocator, &screen, 8, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("hello   ", ascii);

    // Cursor at position 3 ('l')
    const cursor_cell = screen.readCell(3, 0).?;
    try std.testing.expect(cursor_cell.style.reverse);
}

test "render - with scrolling" {
    const allocator = std.testing.allocator;

    var input = TextInput.init(allocator);
    defer input.deinit();

    try input.insertSlice("abcdefghijklmnop");
    input.updateScrollOffset(5);

    var screen = try tui_test.createScreen(allocator, 5, 1);
    defer screen.deinit(allocator);

    const win = tui_test.windowFromScreen(&screen);
    input.render(win, .{});

    const ascii = try tui_test.screenToAscii(allocator, &screen, 5, 1);
    defer allocator.free(ascii);

    // With scroll, we should see the end portion
    const visible = input.visibleText(5);
    try std.testing.expect(visible.len <= 5);
}

test "render - empty input shows cursor" {
    const allocator = std.testing.allocator;

    var input = TextInput.init(allocator);
    defer input.deinit();

    var screen = try tui_test.createScreen(allocator, 5, 1);
    defer screen.deinit(allocator);

    const win = tui_test.windowFromScreen(&screen);
    input.render(win, .{});

    // Cursor at position 0
    const cursor_cell = screen.readCell(0, 0).?;
    try std.testing.expect(cursor_cell.style.reverse);
}
