//! Redraw event types and builder for screen updates.

const std = @import("std");

const msgpack = @import("msgpack.zig");

const Allocator = std.mem.Allocator;

/// Prise UI protocol for screen updates
/// All updates are sent as notifications: [2, "redraw", [events]]
pub const UIEvent = union(enum) {
    resize: Resize,
    write: Write,
    cursor_pos: CursorPos,
    cursor_shape: CursorShape,
    mouse_shape: MouseShape,
    style: Style,
    title: Title,
    selection: Selection,
    flush: void,

    /// ["resize", pty, rows, cols]
    pub const Resize = struct {
        pty: u32,
        rows: u16,
        cols: u16,
    };

    /// ["title", pty, title]
    pub const Title = struct {
        pty: u32,
        title: []const u8,
    };

    /// ["selection", pty, start_row, start_col, end_row, end_col]
    /// Coordinates are in viewport space. null/empty means no selection.
    pub const Selection = struct {
        pty: u32,
        start_row: ?u16,
        start_col: ?u16,
        end_row: ?u16,
        end_col: ?u16,
    };

    /// ["write", pty, row, col, cells]
    /// where cells is an array of [grapheme, style_id?, repeat?]
    pub const Write = struct {
        pty: u32,
        row: u16,
        col: u16,
        cells: []Cell,

        pub const Cell = struct {
            grapheme: []const u8,
            style_id: ?u32 = null, // omitted = reuse previous
            repeat: ?u32 = null, // omitted = 1
            width: ?u8 = null, // omitted = 1, 2 = wide char
        };
    };

    /// ["cursor_pos", pty, row, col, visible]
    /// visible: true if cursor should be rendered (in viewport and mode allows)
    pub const CursorPos = struct {
        pty: u32,
        row: u16,
        col: u16,
        visible: bool,
    };

    /// ["cursor_shape", pty, shape]
    /// shape: 0=block, 1=beam, 2=underline
    pub const CursorShape = struct {
        pty: u32,
        shape: Shape,

        pub const Shape = enum(u8) {
            block = 0,
            beam = 1,
            underline = 2,
        };
    };

    /// ["mouse_shape", pty, shape]
    /// Maps to CSS cursor values / OSC 22
    pub const MouseShape = struct {
        pty: u32,
        shape: Shape,

        pub const Shape = enum(u8) {
            default = 0,
            text = 1,
            pointer = 2,
            help = 3,
            progress = 4,
            wait = 5,
            cell = 6,
            crosshair = 7,
            move = 8,
            not_allowed = 9,
            grab = 10,
            grabbing = 11,
            ew_resize = 12,
            ns_resize = 13,
            nesw_resize = 14,
            nwse_resize = 15,
            col_resize = 16,
            row_resize = 17,
            all_scroll = 18,
            zoom_in = 19,
            zoom_out = 20,
        };
    };

    /// ["style", id, attributes]
    pub const Style = struct {
        id: u32,
        attrs: Attributes,

        pub const Attributes = struct {
            fg: ?u32 = null, // RGB
            bg: ?u32 = null, // RGB
            fg_idx: ?u8 = null, // Index
            bg_idx: ?u8 = null, // Index
            bold: bool = false,
            dim: bool = false,
            italic: bool = false,
            underline: bool = false,
            reverse: bool = false,
            blink: bool = false,
            strikethrough: bool = false,
            ul_style: UnderlineStyle = .none,
            ul_color: ?u32 = null, // RGB
        };

        pub const UnderlineStyle = enum(u8) {
            none = 0,
            single = 1,
            double = 2,
            curly = 3,
            dotted = 4,
            dashed = 5,
        };
    };
};

/// Builder for constructing redraw notifications
pub const RedrawBuilder = struct {
    allocator: Allocator,
    events: std.ArrayList(msgpack.Value),
    arena: std.heap.ArenaAllocator,

    pub fn init(allocator: Allocator) RedrawBuilder {
        return .{
            .allocator = allocator,
            .events = std.ArrayList(msgpack.Value).empty,
            .arena = std.heap.ArenaAllocator.init(allocator),
        };
    }

    pub fn deinit(self: *RedrawBuilder) void {
        self.events.deinit(self.allocator);
        self.arena.deinit();
    }

    /// Add a resize event
    pub fn resize(self: *RedrawBuilder, pty: u32, rows: u16, cols: u16) !void {
        const arena = self.arena.allocator();
        // Event format: ["resize", [pty, rows, cols]]
        const event_name: msgpack.Value = .{ .string = try arena.dupe(u8, "resize") };

        const args = try arena.alloc(msgpack.Value, 3);
        args[0] = .{ .unsigned = pty };
        args[1] = .{ .unsigned = rows };
        args[2] = .{ .unsigned = cols };

        const args_array: msgpack.Value = .{ .array = args };

        // Event is [event_name, args]
        const event_arr = try arena.alloc(msgpack.Value, 2);
        event_arr[0] = event_name;
        event_arr[1] = args_array;

        try self.events.append(self.allocator, .{ .array = event_arr });
    }

    /// Add a write event
    pub fn write(
        self: *RedrawBuilder,
        pty: u32,
        row: u16,
        col: u16,
        cells: []const UIEvent.Write.Cell,
    ) !void {
        const arena = self.arena.allocator();
        // Event format: ["write", [pty, row, col, cells]]
        const event_name: msgpack.Value = .{ .string = try arena.dupe(u8, "write") };

        // Build cells array
        const cells_arr = try arena.alloc(msgpack.Value, cells.len);
        for (cells, 0..) |cell, i| {
            var cell_items = std.ArrayList(msgpack.Value).empty;
            defer cell_items.deinit(arena);

            // Always include grapheme
            try cell_items.append(arena, .{ .string = try arena.dupe(u8, cell.grapheme) });

            // Include style_id if present
            if (cell.style_id) |sid| {
                try cell_items.append(arena, .{ .unsigned = sid });
            }

            // Include repeat if present and style_id was included
            if (cell.repeat) |rep| {
                if (cell.style_id == null) {
                    // If no style_id, we need to include nil placeholder
                    try cell_items.insert(arena, 1, msgpack.Value.nil);
                }
                try cell_items.append(arena, .{ .unsigned = rep });
            }

            // Include width if present (for wide chars)
            if (cell.width) |w| {
                // Need placeholders for style_id and repeat if not present
                if (cell.style_id == null and cell.repeat == null) {
                    try cell_items.append(arena, msgpack.Value.nil); // style_id placeholder
                    try cell_items.append(arena, msgpack.Value.nil); // repeat placeholder
                } else if (cell.repeat == null) {
                    try cell_items.append(arena, msgpack.Value.nil); // repeat placeholder
                }
                try cell_items.append(arena, .{ .unsigned = w });
            }

            cells_arr[i] = .{ .array = try cell_items.toOwnedSlice(arena) };
        }

        const args = try arena.alloc(msgpack.Value, 4);
        args[0] = .{ .unsigned = pty };
        args[1] = .{ .unsigned = row };
        args[2] = .{ .unsigned = col };
        args[3] = .{ .array = cells_arr };

        const args_array: msgpack.Value = .{ .array = args };

        const event_arr = try arena.alloc(msgpack.Value, 2);
        event_arr[0] = event_name;
        event_arr[1] = args_array;

        try self.events.append(self.allocator, .{ .array = event_arr });
    }

    /// Add a cursor_pos event
    pub fn cursorPos(self: *RedrawBuilder, pty: u32, row: u16, col: u16, visible: bool) !void {
        const arena = self.arena.allocator();
        const event_name: msgpack.Value = .{ .string = try arena.dupe(u8, "cursor_pos") };

        const args = try arena.alloc(msgpack.Value, 4);
        args[0] = .{ .unsigned = pty };
        args[1] = .{ .unsigned = row };
        args[2] = .{ .unsigned = col };
        args[3] = .{ .boolean = visible };

        const args_array: msgpack.Value = .{ .array = args };

        const event_arr = try arena.alloc(msgpack.Value, 2);
        event_arr[0] = event_name;
        event_arr[1] = args_array;

        try self.events.append(self.allocator, .{ .array = event_arr });
    }

    /// Add a cursor_shape event
    pub fn cursorShape(self: *RedrawBuilder, pty: u32, shape: UIEvent.CursorShape.Shape) !void {
        const arena = self.arena.allocator();
        const event_name: msgpack.Value = .{ .string = try arena.dupe(u8, "cursor_shape") };

        const args = try arena.alloc(msgpack.Value, 2);
        args[0] = .{ .unsigned = pty };
        args[1] = .{ .unsigned = @intFromEnum(shape) };

        const args_array: msgpack.Value = .{ .array = args };

        const event_arr = try arena.alloc(msgpack.Value, 2);
        event_arr[0] = event_name;
        event_arr[1] = args_array;

        try self.events.append(self.allocator, .{ .array = event_arr });
    }

    /// Add a mouse_shape event
    pub fn mouseShape(self: *RedrawBuilder, pty: u32, shape: UIEvent.MouseShape.Shape) !void {
        const arena = self.arena.allocator();
        const event_name: msgpack.Value = .{ .string = try arena.dupe(u8, "mouse_shape") };

        const args = try arena.alloc(msgpack.Value, 2);
        args[0] = .{ .unsigned = pty };
        args[1] = .{ .unsigned = @intFromEnum(shape) };

        const args_array: msgpack.Value = .{ .array = args };

        const event_arr = try arena.alloc(msgpack.Value, 2);
        event_arr[0] = event_name;
        event_arr[1] = args_array;

        try self.events.append(self.allocator, .{ .array = event_arr });
    }

    /// Add a flush event
    pub fn flush(self: *RedrawBuilder) !void {
        const arena = self.arena.allocator();
        const event_name: msgpack.Value = .{ .string = try arena.dupe(u8, "flush") };

        const args = try arena.alloc(msgpack.Value, 0);
        const args_array: msgpack.Value = .{ .array = args };

        const event_arr = try arena.alloc(msgpack.Value, 2);
        event_arr[0] = event_name;
        event_arr[1] = args_array;

        try self.events.append(self.allocator, .{ .array = event_arr });
    }

    /// Add a selection event (null values mean no selection)
    pub fn selection(
        self: *RedrawBuilder,
        pty: u32,
        start_row: ?u16,
        start_col: ?u16,
        end_row: ?u16,
        end_col: ?u16,
    ) !void {
        const arena = self.arena.allocator();
        const event_name: msgpack.Value = .{ .string = try arena.dupe(u8, "selection") };

        const args = try arena.alloc(msgpack.Value, 5);
        args[0] = .{ .unsigned = pty };
        args[1] = if (start_row) |r| .{ .unsigned = r } else msgpack.Value.nil;
        args[2] = if (start_col) |c| .{ .unsigned = c } else msgpack.Value.nil;
        args[3] = if (end_row) |r| .{ .unsigned = r } else msgpack.Value.nil;
        args[4] = if (end_col) |c| .{ .unsigned = c } else msgpack.Value.nil;

        const args_array: msgpack.Value = .{ .array = args };

        const event_arr = try arena.alloc(msgpack.Value, 2);
        event_arr[0] = event_name;
        event_arr[1] = args_array;

        try self.events.append(self.allocator, .{ .array = event_arr });
    }

    /// Add a style event
    pub fn style(
        self: *RedrawBuilder,
        id: u32,
        attrs: UIEvent.Style.Attributes,
    ) !void {
        const arena = self.arena.allocator();
        const event_name: msgpack.Value = .{ .string = try arena.dupe(u8, "style") };

        var items = std.ArrayList(msgpack.Value.KeyValue).empty;
        defer items.deinit(arena);

        if (attrs.fg) |fg| {
            try items.append(arena, .{
                .key = .{ .string = try arena.dupe(u8, "fg") },
                .value = .{ .unsigned = fg },
            });
        } else if (attrs.fg_idx) |fg_idx| {
            try items.append(arena, .{
                .key = .{ .string = try arena.dupe(u8, "fg_idx") },
                .value = .{ .unsigned = fg_idx },
            });
        }

        if (attrs.bg) |bg| {
            try items.append(arena, .{
                .key = .{ .string = try arena.dupe(u8, "bg") },
                .value = .{ .unsigned = bg },
            });
        } else if (attrs.bg_idx) |bg_idx| {
            try items.append(arena, .{
                .key = .{ .string = try arena.dupe(u8, "bg_idx") },
                .value = .{ .unsigned = bg_idx },
            });
        }

        if (attrs.bold) {
            try items.append(arena, .{
                .key = .{ .string = try arena.dupe(u8, "bold") },
                .value = .{ .boolean = true },
            });
        }

        if (attrs.dim) {
            try items.append(arena, .{
                .key = .{ .string = try arena.dupe(u8, "dim") },
                .value = .{ .boolean = true },
            });
        }

        if (attrs.italic) {
            try items.append(arena, .{
                .key = .{ .string = try arena.dupe(u8, "italic") },
                .value = .{ .boolean = true },
            });
        }

        if (attrs.underline) {
            try items.append(arena, .{
                .key = .{ .string = try arena.dupe(u8, "underline") },
                .value = .{ .boolean = true },
            });
        }

        if (attrs.ul_style != .none) {
            try items.append(arena, .{
                .key = .{ .string = try arena.dupe(u8, "ul_style") },
                .value = .{ .unsigned = @intFromEnum(attrs.ul_style) },
            });
        }

        if (attrs.ul_color) |ulc| {
            try items.append(arena, .{
                .key = .{ .string = try arena.dupe(u8, "ul_color") },
                .value = .{ .unsigned = ulc },
            });
        }

        if (attrs.strikethrough) {
            try items.append(arena, .{
                .key = .{ .string = try arena.dupe(u8, "strikethrough") },
                .value = .{ .boolean = true },
            });
        }

        if (attrs.reverse) {
            try items.append(arena, .{
                .key = .{ .string = try arena.dupe(u8, "reverse") },
                .value = .{ .boolean = true },
            });
        }

        if (attrs.blink) {
            try items.append(arena, .{
                .key = .{ .string = try arena.dupe(u8, "blink") },
                .value = .{ .boolean = true },
            });
        }

        const args = try arena.alloc(msgpack.Value, 2);
        args[0] = .{ .unsigned = id };
        args[1] = .{ .map = try items.toOwnedSlice(arena) };

        const args_array: msgpack.Value = .{ .array = args };

        const event_arr = try arena.alloc(msgpack.Value, 2);
        event_arr[0] = event_name;
        event_arr[1] = args_array;

        try self.events.append(self.allocator, .{ .array = event_arr });
    }

    /// Add a title event
    pub fn title(self: *RedrawBuilder, pty: u32, title_text: []const u8) !void {
        const arena = self.arena.allocator();
        const event_name: msgpack.Value = .{ .string = try arena.dupe(u8, "title") };

        const args = try arena.alloc(msgpack.Value, 2);
        args[0] = .{ .unsigned = pty };
        args[1] = .{ .string = try arena.dupe(u8, title_text) };

        const args_array: msgpack.Value = .{ .array = args };

        const event_arr = try arena.alloc(msgpack.Value, 2);
        event_arr[0] = event_name;
        event_arr[1] = args_array;

        try self.events.append(self.allocator, .{ .array = event_arr });
    }

    /// Build the final notification message: [2, "redraw", [events]]
    pub fn build(self: *RedrawBuilder) ![]u8 {
        const arena = self.arena.allocator();
        // Build the notification array
        const notification = try arena.alloc(msgpack.Value, 3);
        notification[0] = .{ .unsigned = 2 }; // type = notification
        notification[1] = .{ .string = try arena.dupe(u8, "redraw") };
        notification[2] = .{ .array = self.events.items };

        const value: msgpack.Value = .{ .array = notification };
        // Don't call deinit - arena cleanup handles it

        return try msgpack.encodeFromValue(self.allocator, value);
    }
};

const testing = std.testing;

test "build resize event" {
    var builder = RedrawBuilder.init(testing.allocator);
    defer builder.deinit();

    try builder.resize(1, 24, 80);
    try builder.flush();

    const msg = try builder.build();
    defer testing.allocator.free(msg);

    const value = try msgpack.decode(testing.allocator, msg);
    defer value.deinit(testing.allocator);

    try testing.expect(value == .array);
    try testing.expectEqual(@as(u64, 2), value.array[0].unsigned);
    try testing.expectEqualStrings("redraw", value.array[1].string);
}

test "build write event" {
    var builder = RedrawBuilder.init(testing.allocator);
    defer builder.deinit();

    const cells = [_]UIEvent.Write.Cell{
        .{ .grapheme = "H", .style_id = 0 },
        .{ .grapheme = "e", .style_id = 0 },
        .{ .grapheme = "l", .style_id = 0 },
        .{ .grapheme = "l", .style_id = 0 },
        .{ .grapheme = "o", .style_id = 0 },
    };

    try builder.write(1, 0, 0, &cells);
    try builder.flush();

    const msg = try builder.build();
    defer testing.allocator.free(msg);

    const value = try msgpack.decode(testing.allocator, msg);
    defer value.deinit(testing.allocator);

    try testing.expect(value == .array);
}

test "build complete redraw notification" {
    var builder = RedrawBuilder.init(testing.allocator);
    defer builder.deinit();

    // Resize pty 1
    try builder.resize(1, 24, 80);

    // Define style 1 (Red foreground)
    try builder.style(1, .{ .fg = 0xFF0000, .bold = true });

    // Write line
    const cells = [_]UIEvent.Write.Cell{
        .{ .grapheme = "H", .style_id = 1 },
        .{ .grapheme = "i", .style_id = 1 },
        .{ .grapheme = " ", .repeat = 5, .style_id = 1 },
    };
    try builder.write(1, 0, 0, &cells);

    // Move cursor
    try builder.cursorPos(1, 0, 7, true);
    try builder.cursorShape(1, .beam);

    try builder.flush();

    const msg = try builder.build();
    defer testing.allocator.free(msg);

    // Decode and verify structure
    const value = try msgpack.decode(testing.allocator, msg);
    defer value.deinit(testing.allocator);

    try testing.expect(value == .array);
    try testing.expectEqual(@as(usize, 3), value.array.len);

    // Check notification structure: [2, "redraw", events]
    try testing.expectEqual(@as(u64, 2), value.array[0].unsigned);
    try testing.expectEqualStrings("redraw", value.array[1].string);
    try testing.expect(value.array[2] == .array);

    // Check we have 6 events
    const events = value.array[2].array;
    try testing.expectEqual(@as(usize, 6), events.len);
}
