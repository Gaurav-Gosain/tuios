//! Widget system for building terminal UI layouts.

const std = @import("std");

const vaxis = @import("vaxis");
const ziglua = @import("zlua");

const lua_event = @import("lua_event.zig");
const Surface = @import("Surface.zig");
const TextInput = @import("TextInput.zig");

const log = std.log.scoped(.widget);

pub const BoxConstraints = struct {
    min_width: u16,
    max_width: ?u16,
    min_height: u16,
    max_height: ?u16,
};

pub const Size = struct {
    width: u16,
    height: u16,
};

pub const HitRegion = struct {
    pty_id: u32,
    x: u16,
    y: u16,
    width: u16,
    height: u16,

    pub fn contains(self: HitRegion, px: f64, py: f64) bool {
        const x_start: f64 = @floatFromInt(self.x);
        const y_start: f64 = @floatFromInt(self.y);
        const x_end: f64 = @floatFromInt(self.x + self.width);
        const y_end: f64 = @floatFromInt(self.y + self.height);
        return px >= x_start and px < x_end and py >= y_start and py < y_end;
    }
};

pub const SurfaceResize = struct {
    pty_id: u32,
    surface: *Surface,
    width: u16,
    height: u16,
};

pub const SplitHandle = struct {
    parent_id: ?u32,
    child_index: u16,
    axis: Axis,
    boundary: u16,
    start: u16,
    end: u16,
    child_current_size: u16,
    total_size: u16,
    separator_space: u16 = 0, // Total space used by separators in this container
    container_start: u16 = 0, // Start position of the container (for ratio calculation)

    pub const Axis = enum { horizontal, vertical };

    pub fn contains(self: SplitHandle, px: f64, py: f64) bool {
        const boundary: f64 = @floatFromInt(self.boundary);
        const start: f64 = @floatFromInt(self.start);
        const end: f64 = @floatFromInt(self.end);

        const has_separator = self.separator_space > 0;

        if (has_separator) {
            const cell_x: u16 = @intFromFloat(px);
            const cell_y: u16 = @intFromFloat(py);
            return switch (self.axis) {
                .horizontal => cell_x == self.boundary and cell_y >= self.start and cell_y < self.end,
                .vertical => cell_y == self.boundary and cell_x >= self.start and cell_x < self.end,
            };
        }

        return switch (self.axis) {
            .horizontal => @abs(px - boundary) < 0.5 and py >= start and py < end,
            .vertical => @abs(py - boundary) < 0.5 and px >= start and px < end,
        };
    }

    pub fn calculateNewRatio(self: SplitHandle, mouse_pos: f64) f32 {
        const total: f64 = @floatFromInt(self.total_size);
        const sep_space: f64 = @floatFromInt(self.separator_space);
        const container_start: f64 = @floatFromInt(self.container_start);

        // Available space for panes (total minus separator space)
        const pane_space = total - sep_space;
        if (pane_space <= 0) return 0.5;

        // Calculate new ratio based on mouse position relative to container start
        const relative_pos = mouse_pos - container_start;

        // Subtract separator space that comes before the mouse position
        // For now, approximate by assuming separators are evenly distributed
        // This gives us the position within the pane-only space
        var ratio = relative_pos / total;

        // Adjust for separator space - the ratio should be relative to pane space only
        // If separator_space > 0, we need to scale the ratio
        if (sep_space > 0) {
            // Estimate how many separators are before the mouse position
            // Each separator takes 1 cell, positioned between panes
            const sep_before = (relative_pos / total) * sep_space;
            const pane_pos = relative_pos - sep_before;
            ratio = pane_pos / pane_space;
        }

        if (ratio < 0.1) ratio = 0.1;
        if (ratio > 0.9) ratio = 0.9;
        return @floatCast(ratio);
    }
};

pub const Widget = struct {
    x: u16 = 0,
    y: u16 = 0,
    width: u16 = 0,
    height: u16 = 0,
    ratio: ?f32 = null,
    id: ?u32 = null,
    focus: bool = false,
    kind: WidgetKind,

    pub fn deinit(self: *Widget, allocator: std.mem.Allocator) void {
        switch (self.kind) {
            .surface => {},
            .text_input => {},
            .text => |*t| {
                for (t.spans) |span| {
                    allocator.free(span.text);
                }
                allocator.free(t.spans);
            },
            .list => |*l| {
                for (l.items) |item| {
                    allocator.free(item.text);
                }
                allocator.free(l.items);
            },
            .box => |*b| {
                b.child.deinit(allocator);
                allocator.destroy(b.child);
            },
            .padding => |*p| {
                p.child.deinit(allocator);
                allocator.destroy(p.child);
            },
            .column => |*c| {
                for (c.children) |*child| {
                    child.deinit(allocator);
                }
                allocator.free(c.children);
            },
            .row => |*r| {
                for (r.children) |*child| {
                    child.deinit(allocator);
                }
                allocator.free(r.children);
            },
            .stack => |*s| {
                for (s.children) |*child| {
                    child.deinit(allocator);
                }
                allocator.free(s.children);
            },
            .positioned => |*p| {
                p.child.deinit(allocator);
                allocator.destroy(p.child);
            },
            .separator => {},
        }
    }

    pub fn layout(self: *Widget, constraints: BoxConstraints) Size {
        const size: Size = switch (self.kind) {
            .surface => .{
                .width = constraints.max_width.?,
                .height = constraints.max_height.?,
            },
            .text_input => .{
                .width = constraints.max_width orelse 20,
                .height = 1,
            },
            .list => |l| .{
                .width = constraints.max_width orelse 20,
                .height = @min(@as(u16, @intCast(l.items.len)), constraints.max_height orelse @as(u16, @intCast(l.items.len))),
            },
            .box => |*b| layoutBoxImpl(b, constraints),
            .padding => |*p| layoutPaddingImpl(p, constraints),
            .column => |*col| layoutColumnImpl(col, constraints),
            .row => |*row| layoutRowImpl(row, constraints),
            .text => |*text| layoutTextImpl(text, constraints),
            .stack => |*stack| layoutStackImpl(stack, constraints),
            .positioned => |*pos| layoutPositionedImpl(pos, constraints),
            .separator => |sep| switch (sep.axis) {
                // Vertical separator: 1 cell wide, fills available height
                .vertical => .{
                    .width = 1,
                    .height = constraints.max_height orelse 1,
                },
                // Horizontal separator: 1 cell tall, fills available width
                .horizontal => .{
                    .width = constraints.max_width orelse 1,
                    .height = 1,
                },
            },
        };
        self.width = size.width;
        self.height = size.height;
        return size;
    }

    pub fn paint(self: *const Widget) !void {
        switch (self.kind) {
            .surface => |surface| {
                _ = surface;
                // TODO: paint surface at self.x, self.y, self.width, self.height
            },
            .text => {},
            .text_input => {},
            .list => {},
            .box => |*b| {
                try b.child.paint();
            },
            .column => |*col| {
                for (col.children) |*child| {
                    try child.paint();
                }
            },
            .row => |*row| {
                for (row.children) |*child| {
                    try child.paint();
                }
            },
            .stack => |*stack| {
                for (stack.children) |*child| {
                    try child.paint();
                }
            },
            .positioned => |*pos| {
                try pos.child.paint();
            },
            .separator => {},
        }
    }

    /// Render widget to a vaxis Window.
    pub fn renderTo(self: *const Widget, win: vaxis.Window, allocator: std.mem.Allocator) !void {
        switch (self.kind) {
            .surface => |surf| {
                surf.surface.render(win, self.focus);
            },
            .text_input => |ti| {
                ti.input.updateScrollOffset(@intCast(win.width));
                ti.input.render(win, ti.style);
            },
            .text => |text| {
                var iter = Text.Iterator{
                    .text = text,
                    .max_width = win.width,
                    .allocator = allocator,
                };

                var row: usize = 0;
                while (try iter.next()) |line| {
                    defer allocator.free(line.segments);

                    if (text.style.bg != .default) {
                        for (0..win.width) |c| {
                            win.writeCell(@intCast(c), @intCast(row), .{
                                .char = .{ .grapheme = " ", .width = 1 },
                                .style = text.style,
                            });
                        }
                    }

                    var col: usize = 0;

                    const free_space = if (win.width > line.width) win.width - line.width else 0;
                    const start_col: u16 = switch (text.@"align") {
                        Text.Align.left => 0,
                        Text.Align.center => free_space / 2,
                        Text.Align.right => free_space,
                    };

                    col = start_col;

                    for (line.segments) |seg| {
                        _ = win.printSegment(seg, .{ .row_offset = @intCast(row), .col_offset = @intCast(col) });
                        col += vaxis.gwidth.gwidth(seg.text, .unicode);
                    }

                    row += 1;
                    if (row >= win.height) break;
                }
            },
            .list => |list| {
                const visible_rows = win.height;
                const start = list.scroll_offset;
                const end = @min(start + visible_rows, list.items.len);

                for (start..end) |i| {
                    const row: u16 = @intCast(i - start);
                    const item = list.items[i];
                    const is_selected = list.selected != null and list.selected.? == i;

                    const item_style = if (is_selected)
                        list.selected_style
                    else
                        item.style orelse list.style;

                    for (0..win.width) |c| {
                        win.writeCell(@intCast(c), row, .{
                            .char = .{ .grapheme = " ", .width = 1 },
                            .style = item_style,
                        });
                    }

                    var col: u16 = 0;
                    var giter = vaxis.unicode.graphemeIterator(item.text);
                    while (giter.next()) |grapheme| {
                        if (col >= win.width) break;
                        const bytes = grapheme.bytes(item.text);
                        const gw: u8 = @intCast(vaxis.gwidth.gwidth(bytes, .unicode));
                        win.writeCell(col, row, .{
                            .char = .{ .grapheme = bytes, .width = gw },
                            .style = item_style,
                        });
                        col += gw;
                    }
                }
            },
            .box => |b| {
                const chars = b.borderChars();
                const style = b.style;

                for (0..win.height) |row| {
                    for (0..win.width) |col| {
                        win.writeCell(@intCast(col), @intCast(row), .{
                            .char = .{ .grapheme = " ", .width = 1 },
                            .style = style,
                        });
                    }
                }

                if (b.border != .none and win.width >= 2 and win.height >= 2) {
                    win.writeCell(0, 0, .{ .char = .{ .grapheme = chars.tl, .width = 1 }, .style = style });
                    win.writeCell(win.width - 1, 0, .{ .char = .{ .grapheme = chars.tr, .width = 1 }, .style = style });
                    win.writeCell(0, win.height - 1, .{ .char = .{ .grapheme = chars.bl, .width = 1 }, .style = style });
                    win.writeCell(win.width - 1, win.height - 1, .{ .char = .{ .grapheme = chars.br, .width = 1 }, .style = style });

                    if (win.width > 2) {
                        for (1..win.width - 1) |col| {
                            win.writeCell(@intCast(col), 0, .{ .char = .{ .grapheme = chars.h, .width = 1 }, .style = style });
                            win.writeCell(@intCast(col), win.height - 1, .{ .char = .{ .grapheme = chars.h, .width = 1 }, .style = style });
                        }
                    }

                    if (win.height > 2) {
                        for (1..win.height - 1) |row| {
                            win.writeCell(0, @intCast(row), .{ .char = .{ .grapheme = chars.v, .width = 1 }, .style = style });
                            win.writeCell(win.width - 1, @intCast(row), .{ .char = .{ .grapheme = chars.v, .width = 1 }, .style = style });
                        }
                    }
                }

                const child_win = win.child(.{
                    .x_off = b.child.x,
                    .y_off = b.child.y,
                    .width = b.child.width,
                    .height = b.child.height,
                });
                try b.child.renderTo(child_win, allocator);
            },
            .padding => |p| {
                const child_win = win.child(.{
                    .x_off = p.child.x,
                    .y_off = p.child.y,
                    .width = p.child.width,
                    .height = p.child.height,
                });
                try p.child.renderTo(child_win, allocator);
            },
            .column => |col| {
                for (col.children) |child| {
                    const child_win = win.child(.{
                        .x_off = child.x,
                        .y_off = child.y,
                        .width = child.width,
                        .height = child.height,
                    });
                    try child.renderTo(child_win, allocator);
                }
            },
            .row => |row| {
                for (row.children) |child| {
                    const child_win = win.child(.{
                        .x_off = child.x,
                        .y_off = child.y,
                        .width = child.width,
                        .height = child.height,
                    });
                    try child.renderTo(child_win, allocator);
                }
            },
            .stack => |stack| {
                for (stack.children) |child| {
                    const child_win = win.child(.{
                        .x_off = child.x,
                        .y_off = child.y,
                        .width = child.width,
                        .height = child.height,
                    });
                    try child.renderTo(child_win, allocator);
                }
            },
            .positioned => |pos| {
                const child_win = win.child(.{
                    .x_off = pos.child.x,
                    .y_off = pos.child.y,
                    .width = pos.child.width,
                    .height = pos.child.height,
                });
                try pos.child.renderTo(child_win, allocator);
            },
            .separator => |sep| {
                const line_char = sep.lineChar();
                const style = sep.style;

                for (0..win.height) |row| {
                    for (0..win.width) |col| {
                        win.writeCell(@intCast(col), @intCast(row), .{
                            .char = .{ .grapheme = line_char, .width = 1 },
                            .style = style,
                        });
                    }
                }
            },
        }
    }

    pub fn collectHitRegions(self: *const Widget, allocator: std.mem.Allocator, offset_x: u16, offset_y: u16) ![]HitRegion {
        var regions = std.ArrayList(HitRegion).empty;
        errdefer regions.deinit(allocator);

        try self.collectHitRegionsRecursive(allocator, &regions, offset_x, offset_y);

        if (regions.items.len == 0) return &.{};
        return regions.toOwnedSlice(allocator);
    }

    fn collectHitRegionsRecursive(self: *const Widget, allocator: std.mem.Allocator, regions: *std.ArrayList(HitRegion), offset_x: u16, offset_y: u16) !void {
        const abs_x = offset_x + self.x;
        const abs_y = offset_y + self.y;

        switch (self.kind) {
            .surface => |surface| {
                try regions.append(allocator, .{
                    .pty_id = surface.pty_id,
                    .x = abs_x,
                    .y = abs_y,
                    .width = self.width,
                    .height = self.height,
                });
            },
            .column => |col| {
                for (col.children) |*child| {
                    try child.collectHitRegionsRecursive(allocator, regions, abs_x, abs_y);
                }
            },
            .row => |row| {
                for (row.children) |*child| {
                    try child.collectHitRegionsRecursive(allocator, regions, abs_x, abs_y);
                }
            },
            .text => {},
            .text_input => {},
            .list => {},
            .box => |b| {
                try b.child.collectHitRegionsRecursive(allocator, regions, abs_x, abs_y);
            },
            .padding => |p| {
                try p.child.collectHitRegionsRecursive(allocator, regions, abs_x, abs_y);
            },
            .stack => |stack| {
                for (stack.children) |*child| {
                    try child.collectHitRegionsRecursive(allocator, regions, abs_x, abs_y);
                }
            },
            .positioned => |pos| {
                try pos.child.collectHitRegionsRecursive(allocator, regions, abs_x, abs_y);
            },
            .separator => {},
        }
    }

    pub fn collectSplitHandles(self: *const Widget, allocator: std.mem.Allocator, offset_x: u16, offset_y: u16) ![]SplitHandle {
        var handles = std.ArrayList(SplitHandle).empty;
        errdefer handles.deinit(allocator);

        try self.collectSplitHandlesRecursive(allocator, &handles, offset_x, offset_y);

        if (handles.items.len == 0) return &.{};
        return handles.toOwnedSlice(allocator);
    }

    fn collectSplitHandlesRecursive(self: *const Widget, allocator: std.mem.Allocator, handles: *std.ArrayList(SplitHandle), offset_x: u16, offset_y: u16) !void {
        const abs_x = offset_x + self.x;
        const abs_y = offset_y + self.y;

        switch (self.kind) {
            .surface => {},
            .text => {},
            .text_input => {},
            .list => {},
            .box => |b| {
                try b.child.collectSplitHandlesRecursive(allocator, handles, abs_x, abs_y);
            },
            .row => |row| {
                // Count separator space (separators have width 1 in a row)
                var sep_space: u16 = 0;
                for (row.children) |*c| {
                    if (c.kind == .separator) sep_space += c.width;
                }

                // Horizontal split handles (between children in a row)
                for (row.children, 0..) |*child, i| {
                    // Recurse first
                    try child.collectSplitHandlesRecursive(allocator, handles, abs_x, abs_y);

                    // Add handle after this child (except for last child), only if resizable
                    // Skip separator widgets - the handle is on the separator itself, not after it
                    if (row.resizable and i < row.children.len - 1 and child.kind != .separator) {
                        try handles.append(allocator, .{
                            .parent_id = self.id,
                            .child_index = @intCast(i),
                            .axis = .horizontal,
                            .boundary = abs_x + child.x + child.width,
                            .start = abs_y,
                            .end = abs_y + self.height,
                            .child_current_size = child.width,
                            .total_size = self.width,
                            .separator_space = sep_space,
                            .container_start = abs_x,
                        });
                    }
                }
            },
            .column => |col| {
                // Count separator space (separators have height 1 in a column)
                var sep_space: u16 = 0;
                for (col.children) |*c| {
                    if (c.kind == .separator) sep_space += c.height;
                }

                // Vertical split handles (between children in a column)
                for (col.children, 0..) |*child, i| {
                    // Recurse first
                    try child.collectSplitHandlesRecursive(allocator, handles, abs_x, abs_y);

                    // Add handle after this child (except for last child), only if resizable
                    // Skip separator widgets - the handle is on the separator itself, not after it
                    if (col.resizable and i < col.children.len - 1 and child.kind != .separator) {
                        try handles.append(allocator, .{
                            .parent_id = self.id,
                            .child_index = @intCast(i),
                            .axis = .vertical,
                            .boundary = abs_y + child.y + child.height,
                            .start = abs_x,
                            .end = abs_x + self.width,
                            .child_current_size = child.height,
                            .total_size = self.height,
                            .separator_space = sep_space,
                            .container_start = abs_y,
                        });
                    }
                }
            },
            .stack => |stack| {
                for (stack.children) |*child| {
                    try child.collectSplitHandlesRecursive(allocator, handles, abs_x, abs_y);
                }
            },
            .positioned => |pos| {
                try pos.child.collectSplitHandlesRecursive(allocator, handles, abs_x, abs_y);
            },
            .padding => |p| {
                try p.child.collectSplitHandlesRecursive(allocator, handles, abs_x, abs_y);
            },
            .separator => {},
        }
    }

    pub fn collectSurfaceResizes(self: *const Widget, allocator: std.mem.Allocator) ![]SurfaceResize {
        var resizes = std.ArrayList(SurfaceResize).empty;
        errdefer resizes.deinit(allocator);

        try self.collectSurfaceResizesRecursive(allocator, &resizes);

        if (resizes.items.len == 0) return &.{};
        return resizes.toOwnedSlice(allocator);
    }

    fn collectSurfaceResizesRecursive(self: *const Widget, allocator: std.mem.Allocator, resizes: *std.ArrayList(SurfaceResize)) !void {
        switch (self.kind) {
            .surface => |surf| {
                if (self.width != surf.surface.cols or self.height != surf.surface.rows) {
                    try resizes.append(allocator, .{
                        .pty_id = surf.pty_id,
                        .surface = surf.surface,
                        .width = self.width,
                        .height = self.height,
                    });
                }
            },
            .column => |col| {
                for (col.children) |*child| {
                    try child.collectSurfaceResizesRecursive(allocator, resizes);
                }
            },
            .row => |row| {
                for (row.children) |*child| {
                    try child.collectSurfaceResizesRecursive(allocator, resizes);
                }
            },
            .text => {},
            .text_input => {},
            .list => {},
            .box => |b| {
                try b.child.collectSurfaceResizesRecursive(allocator, resizes);
            },
            .padding => |p| {
                try p.child.collectSurfaceResizesRecursive(allocator, resizes);
            },
            .stack => |stack| {
                for (stack.children) |*child| {
                    try child.collectSurfaceResizesRecursive(allocator, resizes);
                }
            },
            .positioned => |pos| {
                try pos.child.collectSurfaceResizesRecursive(allocator, resizes);
            },
            .separator => {},
        }
    }
};

pub fn hitTest(regions: []const HitRegion, x: f64, y: f64) ?u32 {
    var i = regions.len;
    while (i > 0) {
        i -= 1;
        if (regions[i].contains(x, y)) {
            return regions[i].pty_id;
        }
    }
    return null;
}

pub fn findRegion(regions: []const HitRegion, pty_id: u32) ?HitRegion {
    for (regions) |region| {
        if (region.pty_id == pty_id) {
            return region;
        }
    }
    return null;
}

pub fn hitTestSplitHandle(handles: []const SplitHandle, x: f64, y: f64) ?*const SplitHandle {
    var i = handles.len;
    while (i > 0) {
        i -= 1;
        if (handles[i].contains(x, y)) {
            return &handles[i];
        }
    }
    return null;
}

pub const WidgetKind = union(enum) {
    surface: SurfaceWidget,
    text: Text,
    text_input: TextInputWidget,
    list: List,
    box: Box,
    padding: Padding,
    column: Column,
    row: Row,
    stack: Stack,
    positioned: Positioned,
    separator: Separator,
};

pub const CrossAxisAlignment = enum {
    start,
    center,
    end,
    stretch,
};

pub const Column = struct {
    children: []Widget,
    cross_axis_align: CrossAxisAlignment = .center,
    resizable: bool = false,
};

pub const Row = struct {
    children: []Widget,
    cross_axis_align: CrossAxisAlignment = .center,
    resizable: bool = false,
};

pub const Stack = struct {
    children: []Widget,
};

pub const Anchor = enum {
    top_left,
    top_center,
    top_right,
    center_left,
    center,
    center_right,
    bottom_left,
    bottom_center,
    bottom_right,
};

pub const Positioned = struct {
    child: *Widget,
    x: ?u16 = null,
    y: ?u16 = null,
    anchor: Anchor = .top_left,
};

pub const SurfaceWidget = struct {
    pty_id: u32,
    surface: *Surface,
};

pub const TextInputWidget = struct {
    input_id: u32,
    input: *TextInput,
    style: vaxis.Style = .{},
};

pub const List = struct {
    items: []Item,
    selected: ?usize = null,
    scroll_offset: usize = 0,
    style: vaxis.Style = .{},
    selected_style: vaxis.Style = .{},

    pub const Item = struct {
        text: []const u8,
        style: ?vaxis.Style = null,
    };
};

pub const Box = struct {
    child: *Widget,
    border: Border = .single,
    style: vaxis.Style = .{},
    max_width: ?u16 = null,
    max_height: ?u16 = null,

    pub const Border = enum {
        none,
        single,
        double,
        rounded,
    };

    pub fn borderChars(self: Box) struct { tl: []const u8, tr: []const u8, bl: []const u8, br: []const u8, h: []const u8, v: []const u8 } {
        return switch (self.border) {
            .none => .{ .tl = " ", .tr = " ", .bl = " ", .br = " ", .h = " ", .v = " " },
            .single => .{ .tl = "┌", .tr = "┐", .bl = "└", .br = "┘", .h = "─", .v = "│" },
            .double => .{ .tl = "╔", .tr = "╗", .bl = "╚", .br = "╝", .h = "═", .v = "║" },
            .rounded => .{ .tl = "╭", .tr = "╮", .bl = "╰", .br = "╯", .h = "─", .v = "│" },
        };
    }
};

pub const Separator = struct {
    axis: Axis,
    style: vaxis.Style = .{},
    border: Box.Border = .single,

    pub const Axis = enum { horizontal, vertical };

    /// Returns the line character based on border style
    pub fn lineChar(self: Separator) []const u8 {
        return switch (self.axis) {
            .horizontal => switch (self.border) {
                .none => " ",
                .single, .rounded => "─",
                .double => "═",
            },
            .vertical => switch (self.border) {
                .none => " ",
                .single, .rounded => "│",
                .double => "║",
            },
        };
    }
};

pub const Padding = struct {
    child: *Widget,
    top: u16 = 0,
    bottom: u16 = 0,
    left: u16 = 0,
    right: u16 = 0,
};

pub const Text = struct {
    spans: []Span,
    wrap: Wrap = .none,
    // We must quote align because it is a keyword
    @"align": Align = .left,
    style: vaxis.Style = .{},

    pub const Span = struct {
        text: []const u8,
        style: vaxis.Style,
    };

    pub const Wrap = enum {
        none,
        word,
        char,
    };

    pub const Align = enum {
        left,
        center,
        right,
    };

    pub const Line = struct {
        width: u16,
        segments: []vaxis.Segment,
    };

    pub const Iterator = struct {
        text: Text,
        max_width: u16,
        allocator: std.mem.Allocator,

        // State
        span_idx: usize = 0,
        byte_offset: usize = 0, // offset into current span text
        done: bool = false,

        pub fn next(self: *Iterator) !?Line {
            if (self.done) return null;
            if (self.text.spans.len == 0) {
                self.done = true;
                return null;
            }
            if (self.span_idx >= self.text.spans.len) {
                self.done = true;
                return null;
            }

            var line_segments = std.ArrayList(vaxis.Segment).empty;
            errdefer line_segments.deinit(self.allocator);

            var current_width: u16 = 0;

            while (self.span_idx < self.text.spans.len) {
                const span = self.text.spans[self.span_idx];
                const remaining_text = span.text[self.byte_offset..];

                // If we are at the start of a span and it's empty, skip it (unless it's the only thing?)
                if (remaining_text.len == 0) {
                    self.span_idx += 1;
                    self.byte_offset = 0;
                    continue;
                }

                // If no wrap, take everything
                if (self.text.wrap == .none) {
                    try line_segments.append(self.allocator, .{
                        .text = remaining_text,
                        .style = span.style,
                    });
                    current_width += vaxis.gwidth.gwidth(remaining_text, .unicode);
                    self.span_idx += 1;
                    self.byte_offset = 0;
                    continue;
                }

                // Calculate how much we can fit
                const available = if (self.max_width > current_width) self.max_width - current_width else 0;

                // If no space left on this line and we have content, break line
                if (available == 0 and line_segments.items.len > 0) {
                    break;
                }

                // Find split point
                // This is a simplified logic. For robust wrapping we need proper grapheme/word boundary analysis.
                // Using gwidth on substrings is slow but accurate.

                var fit_len: usize = 0;
                var fit_width: u16 = 0;
                // Iterate graphemes to find fit
                var iter = std.unicode.Utf8View.init(remaining_text) catch unreachable; // Should be valid UTF8
                var iterator = iter.iterator();

                var last_space_idx: ?usize = null;
                var last_space_width: u16 = 0;

                while (iterator.nextCodepointSlice()) |grapheme| {
                    const w = vaxis.gwidth.gwidth(grapheme, .unicode);
                    if (current_width + fit_width + w > self.max_width) {
                        // Overflow
                        break;
                    }
                    fit_width += w;
                    fit_len += grapheme.len;

                    // Track word boundaries (very basic: space)
                    if (std.mem.indexOfScalar(u8, grapheme, ' ') != null) {
                        last_space_idx = fit_len;
                        last_space_width = fit_width;
                    }
                }

                // If we couldn't fit anything
                if (fit_len == 0) {
                    // If line is empty, force take at least one grapheme to avoid infinite loop
                    if (line_segments.items.len == 0) {
                        var it = iter.iterator();
                        if (it.nextCodepointSlice()) |g| {
                            fit_len = g.len;
                            fit_width = vaxis.gwidth.gwidth(g, .unicode);
                        }
                    } else {
                        // Line not empty, push to next line
                        break;
                    }
                } else {
                    // If wrap word, back up to last space if we didn't finish the span
                    // But only if we are actually breaking the line (fit_len < remaining_text.len)
                    // AND we found a space
                    if (self.text.wrap == .word and fit_len < remaining_text.len) {
                        if (last_space_idx) |idx| {
                            fit_len = idx;
                            fit_width = last_space_width;
                        }
                    }
                }

                const segment_text = remaining_text[0..fit_len];
                try line_segments.append(self.allocator, .{
                    .text = segment_text,
                    .style = span.style,
                });
                current_width += fit_width;
                self.byte_offset += fit_len;

                // If we finished this span, move to next
                if (self.byte_offset >= span.text.len) {
                    self.span_idx += 1;
                    self.byte_offset = 0;
                }

                // If we broke early (didn't consume full remaining text), then the line is full
                if (fit_len < remaining_text.len) {
                    break;
                }
            }

            if (line_segments.items.len == 0 and !self.done) {
                // Ensure we don't return empty lines indefinitely if something goes wrong,
                // but valid empty text might result in empty line.
                // If done=false and loop finished with 0 segments, it means we exhausted spans.
                self.done = true;
                // If we were invoked, we should return at least one line if text is empty?
                // Logic at start handles spans.len == 0.
                return null;
            }

            return Line{
                .width = current_width,
                .segments = try line_segments.toOwnedSlice(self.allocator),
            };
        }
    };
};

pub fn parseWidget(lua: *ziglua.Lua, allocator: std.mem.Allocator, index: i32) !Widget {
    if (lua.typeOf(index) != .table) {
        return error.InvalidWidget;
    }

    _ = lua.getField(index, "type");
    if (lua.typeOf(-1) != .string) {
        lua.pop(1);
        return error.MissingWidgetType;
    }
    const widget_type = try lua.toString(-1);
    lua.pop(1);

    var focus: bool = false;
    _ = lua.getField(index, "focus");
    const focus_type = lua.typeOf(-1);
    log.debug("parseWidget: focus field type={}", .{focus_type});
    if (focus_type == .boolean) {
        focus = lua.toBoolean(-1);
        log.debug("parseWidget: focus={}", .{focus});
    } else {
        // Legacy support for show_cursor
        lua.pop(1);
        _ = lua.getField(index, "show_cursor");
        if (lua.typeOf(-1) == .boolean) {
            focus = lua.toBoolean(-1);
        }
    }
    lua.pop(1);

    var ratio: ?f32 = null;
    _ = lua.getField(index, "ratio");
    if (lua.typeOf(-1) == .number) {
        ratio = @floatCast(lua.toNumber(-1) catch 0.0);
    }
    lua.pop(1);

    var id: ?u32 = null;
    _ = lua.getField(index, "id");
    if (lua.typeOf(-1) == .number) {
        id = @intCast(lua.toInteger(-1) catch 0);
    }
    lua.pop(1);

    if (std.mem.eql(u8, widget_type, "terminal")) {
        _ = lua.getField(index, "pty");

        const pty_info = lua_event.getPtyInfo(lua, -1) catch {
            lua.pop(1);
            return error.MissingPtyId;
        };
        lua.pop(1);

        return .{ .ratio = ratio, .id = id, .focus = focus, .kind = .{ .surface = .{ .pty_id = pty_info.id, .surface = pty_info.surface } } };
    } else if (std.mem.eql(u8, widget_type, "text_input")) {
        _ = lua.getField(index, "input");

        const input_info = lua_event.getTextInputInfo(lua, -1) catch {
            lua.pop(1);
            return error.MissingTextInputId;
        };
        lua.pop(1);

        var style = vaxis.Style{};
        _ = lua.getField(index, "style");
        if (lua.typeOf(-1) == .table) {
            style = parseStyle(lua, -1) catch .{};
        }
        lua.pop(1);

        return .{ .ratio = ratio, .id = id, .focus = focus, .kind = .{ .text_input = .{ .input_id = input_info.id, .input = input_info.input, .style = style } } };
    } else if (std.mem.eql(u8, widget_type, "list")) {
        _ = lua.getField(index, "items");
        if (lua.typeOf(-1) != .table) {
            lua.pop(1);
            return error.MissingListItems;
        }

        var items = std.ArrayList(List.Item).empty;
        errdefer {
            for (items.items) |item| allocator.free(item.text);
            items.deinit(allocator);
        }

        const len = lua.rawLen(-1);
        for (1..len + 1) |i| {
            _ = lua.getIndex(-1, @intCast(i));
            if (lua.typeOf(-1) == .string) {
                const text = try lua.toString(-1);
                try items.append(allocator, .{
                    .text = try allocator.dupe(u8, text),
                    .style = null,
                });
            } else if (lua.typeOf(-1) == .table) {
                _ = lua.getField(-1, "text");
                const text = lua.toString(-1) catch "";
                lua.pop(1);

                var item_style: ?vaxis.Style = null;
                _ = lua.getField(-1, "style");
                if (lua.typeOf(-1) == .table) {
                    item_style = parseStyle(lua, -1) catch null;
                }
                lua.pop(1);

                try items.append(allocator, .{
                    .text = try allocator.dupe(u8, text),
                    .style = item_style,
                });
            }
            lua.pop(1);
        }
        lua.pop(1); // items

        var selected: ?usize = null;
        _ = lua.getField(index, "selected");
        if (lua.isInteger(-1)) {
            const sel = lua.toInteger(-1) catch 0;
            if (sel > 0) selected = @intCast(sel - 1); // Lua 1-indexed
        }
        lua.pop(1);

        var scroll_offset: usize = 0;
        _ = lua.getField(index, "scroll_offset");
        if (lua.isInteger(-1)) {
            const off = lua.toInteger(-1) catch 0;
            if (off > 0) scroll_offset = @intCast(off);
        }
        lua.pop(1);

        var style = vaxis.Style{};
        _ = lua.getField(index, "style");
        if (lua.typeOf(-1) == .table) {
            style = parseStyle(lua, -1) catch .{};
        }
        lua.pop(1);

        var selected_style = vaxis.Style{};
        _ = lua.getField(index, "selected_style");
        if (lua.typeOf(-1) == .table) {
            selected_style = parseStyle(lua, -1) catch .{};
        }
        lua.pop(1);

        return .{ .ratio = ratio, .id = id, .focus = focus, .kind = .{ .list = .{
            .items = try items.toOwnedSlice(allocator),
            .selected = selected,
            .scroll_offset = scroll_offset,
            .style = style,
            .selected_style = selected_style,
        } } };
    } else if (std.mem.eql(u8, widget_type, "box")) {
        _ = lua.getField(index, "child");
        if (lua.typeOf(-1) != .table) {
            lua.pop(1);
            return error.MissingBoxChild;
        }
        const child_widget = try parseWidget(lua, allocator, -1);
        lua.pop(1);

        const child = try allocator.create(Widget);
        errdefer allocator.destroy(child);
        child.* = child_widget;

        var border: Box.Border = .single;
        _ = lua.getField(index, "border");
        if (lua.typeOf(-1) == .string) {
            const b = lua.toString(-1) catch "";
            if (std.mem.eql(u8, b, "none")) border = .none;
            if (std.mem.eql(u8, b, "single")) border = .single;
            if (std.mem.eql(u8, b, "double")) border = .double;
            if (std.mem.eql(u8, b, "rounded")) border = .rounded;
        }
        lua.pop(1);

        var style = vaxis.Style{};
        _ = lua.getField(index, "style");
        if (lua.typeOf(-1) == .table) {
            style = parseStyle(lua, -1) catch .{};
        }
        lua.pop(1);

        var max_width: ?u16 = null;
        _ = lua.getField(index, "max_width");
        if (lua.typeOf(-1) == .number) {
            max_width = @intCast(lua.toInteger(-1) catch 0);
        }
        lua.pop(1);

        var max_height: ?u16 = null;
        _ = lua.getField(index, "max_height");
        if (lua.typeOf(-1) == .number) {
            max_height = @intCast(lua.toInteger(-1) catch 0);
        }
        lua.pop(1);

        return .{ .ratio = ratio, .id = id, .focus = focus, .kind = .{ .box = .{
            .child = child,
            .border = border,
            .style = style,
            .max_width = max_width,
            .max_height = max_height,
        } } };
    } else if (std.mem.eql(u8, widget_type, "padding")) {
        _ = lua.getField(index, "child");
        if (lua.typeOf(-1) != .table) {
            lua.pop(1);
            return error.MissingPaddingChild;
        }
        const child_widget = try parseWidget(lua, allocator, -1);
        lua.pop(1);

        const child = try allocator.create(Widget);
        errdefer allocator.destroy(child);
        child.* = child_widget;

        var top: u16 = 0;
        var bottom: u16 = 0;
        var left: u16 = 0;
        var right: u16 = 0;

        _ = lua.getField(index, "all");
        if (lua.typeOf(-1) == .number) {
            const all: u16 = @intCast(lua.toInteger(-1) catch 0);
            top = all;
            bottom = all;
            left = all;
            right = all;
        }
        lua.pop(1);

        _ = lua.getField(index, "top");
        if (lua.typeOf(-1) == .number) top = @intCast(lua.toInteger(-1) catch 0);
        lua.pop(1);

        _ = lua.getField(index, "bottom");
        if (lua.typeOf(-1) == .number) bottom = @intCast(lua.toInteger(-1) catch 0);
        lua.pop(1);

        _ = lua.getField(index, "left");
        if (lua.typeOf(-1) == .number) left = @intCast(lua.toInteger(-1) catch 0);
        lua.pop(1);

        _ = lua.getField(index, "right");
        if (lua.typeOf(-1) == .number) right = @intCast(lua.toInteger(-1) catch 0);
        lua.pop(1);

        return .{ .ratio = ratio, .id = id, .focus = focus, .kind = .{ .padding = .{
            .child = child,
            .top = top,
            .bottom = bottom,
            .left = left,
            .right = right,
        } } };
    } else if (std.mem.eql(u8, widget_type, "column")) {
        _ = lua.getField(index, "children");
        if (lua.typeOf(-1) != .table) {
            lua.pop(1);
            return error.MissingColumnChildren;
        }

        var children = std.ArrayList(Widget).empty;
        errdefer {
            for (children.items) |*c| c.deinit(allocator);
            children.deinit(allocator);
        }

        const len = lua.rawLen(-1);
        for (1..len + 1) |i| {
            _ = lua.getIndex(-1, @intCast(i));
            // recursive call
            const child = try parseWidget(lua, allocator, -1);
            try children.append(allocator, child);
            lua.pop(1);
        }
        lua.pop(1); // children

        var cross_align: CrossAxisAlignment = .center;
        _ = lua.getField(index, "cross_axis_align");
        if (lua.typeOf(-1) == .string) {
            const s = try lua.toString(-1);
            if (std.mem.eql(u8, s, "start")) cross_align = .start;
            if (std.mem.eql(u8, s, "center")) cross_align = .center;
            if (std.mem.eql(u8, s, "end")) cross_align = .end;
            if (std.mem.eql(u8, s, "stretch")) cross_align = .stretch;
        }
        lua.pop(1);

        var resizable = false;
        _ = lua.getField(index, "resizable");
        if (lua.typeOf(-1) == .boolean) {
            resizable = lua.toBoolean(-1);
        }
        lua.pop(1);

        return .{ .ratio = ratio, .id = id, .focus = focus, .kind = .{ .column = .{
            .children = try children.toOwnedSlice(allocator),
            .cross_axis_align = cross_align,
            .resizable = resizable,
        } } };
    } else if (std.mem.eql(u8, widget_type, "row")) {
        _ = lua.getField(index, "children");
        if (lua.typeOf(-1) != .table) {
            lua.pop(1);
            return error.MissingRowChildren;
        }

        var children = std.ArrayList(Widget).empty;
        errdefer {
            for (children.items) |*c| c.deinit(allocator);
            children.deinit(allocator);
        }

        const len = lua.rawLen(-1);
        for (1..len + 1) |i| {
            _ = lua.getIndex(-1, @intCast(i));
            // recursive call
            const child = try parseWidget(lua, allocator, -1);
            try children.append(allocator, child);
            lua.pop(1);
        }
        lua.pop(1); // children

        var cross_align: CrossAxisAlignment = .center;
        _ = lua.getField(index, "cross_axis_align");
        if (lua.typeOf(-1) == .string) {
            const s = try lua.toString(-1);
            if (std.mem.eql(u8, s, "start")) cross_align = .start;
            if (std.mem.eql(u8, s, "center")) cross_align = .center;
            if (std.mem.eql(u8, s, "end")) cross_align = .end;
            if (std.mem.eql(u8, s, "stretch")) cross_align = .stretch;
        }
        lua.pop(1);

        var resizable = false;
        _ = lua.getField(index, "resizable");
        if (lua.typeOf(-1) == .boolean) {
            resizable = lua.toBoolean(-1);
        }
        lua.pop(1);

        return .{ .ratio = ratio, .id = id, .focus = focus, .kind = .{ .row = .{
            .children = try children.toOwnedSlice(allocator),
            .cross_axis_align = cross_align,
            .resizable = resizable,
        } } };
    } else if (std.mem.eql(u8, widget_type, "text")) {
        _ = lua.getField(index, "content");
        if (lua.typeOf(-1) != .table) {
            lua.pop(1);
            return error.MissingTextContent;
        }

        var spans = std.ArrayList(Text.Span).empty;
        errdefer {
            for (spans.items) |span| allocator.free(span.text);
            spans.deinit(allocator);
        }

        const len = lua.rawLen(-1);
        for (1..len + 1) |i| {
            _ = lua.getIndex(-1, @intCast(i));
            defer lua.pop(1);

            if (lua.typeOf(-1) == .string) {
                const text = try lua.toString(-1);
                try spans.append(allocator, .{
                    .text = try allocator.dupe(u8, text),
                    .style = .{},
                });
            } else if (lua.typeOf(-1) == .table) {
                _ = lua.getField(-1, "text");
                if (lua.typeOf(-1) != .string) {
                    lua.pop(1);
                    continue;
                }
                const text = try lua.toString(-1);
                lua.pop(1);

                var style = vaxis.Style{};

                _ = lua.getField(-1, "style");
                if (lua.typeOf(-1) == .table) {
                    style = try parseStyle(lua, -1);
                }
                lua.pop(1);

                try spans.append(allocator, .{
                    .text = try allocator.dupe(u8, text),
                    .style = style,
                });
            }
        }
        lua.pop(1); // content

        var wrap: Text.Wrap = .none;
        _ = lua.getField(index, "wrap");
        if (lua.typeOf(-1) == .string) {
            const s = try lua.toString(-1);
            if (std.mem.eql(u8, s, "word")) wrap = .word;
            if (std.mem.eql(u8, s, "char")) wrap = .char;
        }
        lua.pop(1);

        var @"align": Text.Align = .left;
        _ = lua.getField(index, "align");
        if (lua.typeOf(-1) == .string) {
            const s = try lua.toString(-1);
            if (std.mem.eql(u8, s, "center")) @"align" = .center;
            if (std.mem.eql(u8, s, "right")) @"align" = .right;
        }
        lua.pop(1);

        var text_style = vaxis.Style{};
        _ = lua.getField(index, "style");
        if (lua.typeOf(-1) == .table) {
            text_style = parseStyle(lua, -1) catch .{};
        }
        lua.pop(1);

        return .{ .ratio = ratio, .id = id, .focus = focus, .kind = .{ .text = .{
            .spans = try spans.toOwnedSlice(allocator),
            .wrap = wrap,
            .@"align" = @"align",
            .style = text_style,
        } } };
    } else if (std.mem.eql(u8, widget_type, "stack")) {
        _ = lua.getField(index, "children");
        if (lua.typeOf(-1) != .table) {
            lua.pop(1);
            return error.MissingStackChildren;
        }

        var children = std.ArrayList(Widget).empty;
        errdefer {
            for (children.items) |*c| c.deinit(allocator);
            children.deinit(allocator);
        }

        const len = lua.rawLen(-1);
        for (1..len + 1) |i| {
            _ = lua.getIndex(-1, @intCast(i));
            const child = try parseWidget(lua, allocator, -1);
            try children.append(allocator, child);
            lua.pop(1);
        }
        lua.pop(1); // children

        return .{ .ratio = ratio, .id = id, .focus = focus, .kind = .{ .stack = .{
            .children = try children.toOwnedSlice(allocator),
        } } };
    } else if (std.mem.eql(u8, widget_type, "positioned")) {
        _ = lua.getField(index, "child");
        if (lua.typeOf(-1) != .table) {
            lua.pop(1);
            return error.MissingPositionedChild;
        }

        const child_ptr = try allocator.create(Widget);
        errdefer allocator.destroy(child_ptr);
        child_ptr.* = try parseWidget(lua, allocator, -1);
        lua.pop(1);

        var x: ?u16 = null;
        _ = lua.getField(index, "x");
        if (lua.typeOf(-1) == .number) {
            x = @intCast(lua.toInteger(-1) catch 0);
        }
        lua.pop(1);

        var y: ?u16 = null;
        _ = lua.getField(index, "y");
        if (lua.typeOf(-1) == .number) {
            y = @intCast(lua.toInteger(-1) catch 0);
        }
        lua.pop(1);

        var anchor: Anchor = .top_left;
        _ = lua.getField(index, "anchor");
        if (lua.typeOf(-1) == .string) {
            const s = try lua.toString(-1);
            if (std.mem.eql(u8, s, "top_left")) anchor = .top_left;
            if (std.mem.eql(u8, s, "top_center")) anchor = .top_center;
            if (std.mem.eql(u8, s, "top_right")) anchor = .top_right;
            if (std.mem.eql(u8, s, "center_left")) anchor = .center_left;
            if (std.mem.eql(u8, s, "center")) anchor = .center;
            if (std.mem.eql(u8, s, "center_right")) anchor = .center_right;
            if (std.mem.eql(u8, s, "bottom_left")) anchor = .bottom_left;
            if (std.mem.eql(u8, s, "bottom_center")) anchor = .bottom_center;
            if (std.mem.eql(u8, s, "bottom_right")) anchor = .bottom_right;
        }
        lua.pop(1);

        return .{ .ratio = ratio, .id = id, .focus = focus, .kind = .{ .positioned = .{
            .child = child_ptr,
            .x = x,
            .y = y,
            .anchor = anchor,
        } } };
    } else if (std.mem.eql(u8, widget_type, "separator")) {
        // Parse axis (required)
        var axis: Separator.Axis = .vertical;
        _ = lua.getField(index, "axis");
        if (lua.typeOf(-1) == .string) {
            const a = lua.toString(-1) catch "";
            if (std.mem.eql(u8, a, "horizontal")) axis = .horizontal;
            if (std.mem.eql(u8, a, "vertical")) axis = .vertical;
        }
        lua.pop(1);

        // Parse border style
        var border: Box.Border = .single;
        _ = lua.getField(index, "border");
        if (lua.typeOf(-1) == .string) {
            const b = lua.toString(-1) catch "";
            if (std.mem.eql(u8, b, "none")) border = .none;
            if (std.mem.eql(u8, b, "single")) border = .single;
            if (std.mem.eql(u8, b, "double")) border = .double;
            if (std.mem.eql(u8, b, "rounded")) border = .rounded;
        }
        lua.pop(1);

        // Parse style
        var style = vaxis.Style{};
        _ = lua.getField(index, "style");
        if (lua.typeOf(-1) == .table) {
            style = parseStyle(lua, -1) catch .{};
        }
        lua.pop(1);

        return .{ .ratio = ratio, .id = id, .focus = focus, .kind = .{ .separator = .{
            .axis = axis,
            .border = border,
            .style = style,
        } } };
    }

    return error.UnknownWidgetType;
}

fn parseStyle(lua: *ziglua.Lua, index: i32) !vaxis.Style {
    var style = vaxis.Style{};

    if (lua.typeOf(index) != .table) return style;

    // Colors
    _ = lua.getField(index, "fg");
    if (lua.typeOf(-1) == .string) {
        style.fg = try parseColor(try lua.toString(-1));
    }
    lua.pop(1);

    _ = lua.getField(index, "bg");
    if (lua.typeOf(-1) == .string) {
        style.bg = try parseColor(try lua.toString(-1));
    }
    lua.pop(1);

    // Attributes
    _ = lua.getField(index, "bold");
    if (lua.typeOf(-1) == .boolean) style.bold = lua.toBoolean(-1);
    lua.pop(1);

    _ = lua.getField(index, "dim");
    if (lua.typeOf(-1) == .boolean) style.dim = lua.toBoolean(-1);
    lua.pop(1);

    _ = lua.getField(index, "italic");
    if (lua.typeOf(-1) == .boolean) style.italic = lua.toBoolean(-1);
    lua.pop(1);

    _ = lua.getField(index, "underline");
    if (lua.typeOf(-1) == .boolean and lua.toBoolean(-1)) style.ul_style = .single;
    lua.pop(1);

    _ = lua.getField(index, "blink");
    if (lua.typeOf(-1) == .boolean) style.blink = lua.toBoolean(-1);
    lua.pop(1);

    _ = lua.getField(index, "reverse");
    if (lua.typeOf(-1) == .boolean) style.reverse = lua.toBoolean(-1);
    lua.pop(1);

    _ = lua.getField(index, "strikethrough");
    if (lua.typeOf(-1) == .boolean) style.strikethrough = lua.toBoolean(-1);
    lua.pop(1);

    return style;
}

fn parseColor(str: []const u8) !vaxis.Color {
    if (std.mem.startsWith(u8, str, "#")) {
        if (str.len != 7) return .default;
        const r = std.fmt.parseInt(u8, str[1..3], 16) catch return .default;
        const g = std.fmt.parseInt(u8, str[3..5], 16) catch return .default;
        const b = std.fmt.parseInt(u8, str[5..7], 16) catch return .default;
        return .{ .rgb = .{ r, g, b } };
    }
    if (std.mem.eql(u8, str, "red")) return .{ .rgb = .{ 255, 0, 0 } };
    if (std.mem.eql(u8, str, "green")) return .{ .rgb = .{ 0, 255, 0 } };
    if (std.mem.eql(u8, str, "blue")) return .{ .rgb = .{ 0, 0, 255 } };
    if (std.mem.eql(u8, str, "black")) return .{ .rgb = .{ 0, 0, 0 } };
    if (std.mem.eql(u8, str, "white")) return .{ .rgb = .{ 255, 255, 255 } };
    if (std.mem.eql(u8, str, "yellow")) return .{ .rgb = .{ 255, 255, 0 } };
    if (std.mem.eql(u8, str, "magenta")) return .{ .rgb = .{ 255, 0, 255 } };
    if (std.mem.eql(u8, str, "cyan")) return .{ .rgb = .{ 0, 255, 255 } };
    return .default;
}

// ============================================================================
// Test Helpers
// ============================================================================

fn fixedConstraints(width: u16, height: u16) BoxConstraints {
    return .{
        .min_width = width,
        .max_width = width,
        .min_height = height,
        .max_height = height,
    };
}

fn boundsConstraints(max_width: u16, max_height: u16) BoxConstraints {
    return .{
        .min_width = 0,
        .max_width = max_width,
        .min_height = 0,
        .max_height = max_height,
    };
}

fn expectRect(w: *const Widget, x: u16, y: u16, width: u16, height: u16) !void {
    try std.testing.expectEqual(x, w.x);
    try std.testing.expectEqual(y, w.y);
    try std.testing.expectEqual(width, w.width);
    try std.testing.expectEqual(height, w.height);
}

// ============================================================================
// Tests
// ============================================================================

test "Column Layout - Intrinsic + Proportional" {
    const testing = std.testing;
    const allocator = testing.allocator;

    // Create widgets manually
    var child1: Widget = .{
        .kind = .{ .text = .{ .spans = try allocator.dupe(Text.Span, &.{.{ .text = "Fixed", .style = .{} }}) } },
    };
    // Manually copy string because deinit will try to free it
    child1.kind.text.spans[0].text = try allocator.dupe(u8, "Fixed");

    const child2: Widget = .{
        .kind = .{ .surface = .{ .pty_id = 1, .surface = undefined } },
    };

    var children = [_]Widget{ child1, child2 };
    var col: Widget = .{
        .kind = .{ .column = .{ .children = &children } },
    };

    // Layout in 100x20 box
    const constraints: BoxConstraints = .{
        .min_width = 0,
        .max_width = 100,
        .min_height = 0,
        .max_height = 20,
    };

    const size = col.layout(constraints);

    try testing.expectEqual(@as(u16, 20), size.height);
    // Child 1 (text, intrinsic) should be 1 high
    try testing.expectEqual(@as(u16, 1), children[0].height);
    try testing.expectEqual(@as(u16, 0), children[0].y);

    // Child 2 (surface, nil ratio) should take remaining 19 high
    try testing.expectEqual(@as(u16, 19), children[1].height);
    try testing.expectEqual(@as(u16, 1), children[1].y);

    // Clean up manually since we constructed manually
    allocator.free(children[0].kind.text.spans[0].text);
    allocator.free(children[0].kind.text.spans);
}

test "Column Layout - Stretch Width" {
    const testing = std.testing;
    const allocator = testing.allocator;

    // Child 1: Text "Small"
    var child1: Widget = .{
        .kind = .{ .text = .{ .spans = try allocator.dupe(Text.Span, &.{.{ .text = "Small", .style = .{} }}) } },
    };
    child1.kind.text.spans[0].text = try allocator.dupe(u8, "Small");

    // Child 2: Surface (Full Width)
    const child2: Widget = .{
        .kind = .{ .surface = .{ .pty_id = 1, .surface = undefined } },
    };

    var children = [_]Widget{ child1, child2 };
    var col: Widget = .{
        .kind = .{ .column = .{ .children = &children, .cross_axis_align = .stretch } },
    };

    const constraints: BoxConstraints = .{
        .min_width = 0,
        .max_width = 100,
        .min_height = 0,
        .max_height = 20,
    };

    const size = col.layout(constraints);

    try testing.expectEqual(@as(u16, 100), size.width);

    // Child 2 should be 100 wide
    try testing.expectEqual(@as(u16, 100), children[1].width);

    // Child 1 should ALSO be 100 wide (stretched)
    try testing.expectEqual(@as(u16, 100), children[0].width);

    allocator.free(children[0].kind.text.spans[0].text);
    allocator.free(children[0].kind.text.spans);
}

test "parseWidget - text" {
    const testing = std.testing;
    const allocator = testing.allocator;

    var lua = try ziglua.Lua.init(allocator);
    defer lua.deinit();

    // Test simple text
    {
        lua.createTable(0, 2);
        _ = lua.pushString("text");
        lua.setField(-2, "type");
        lua.createTable(1, 0);
        _ = lua.pushString("Hello");
        lua.rawSetIndex(-2, 1);
        lua.setField(-2, "content");

        var w = try parseWidget(lua, allocator, -1);
        defer w.deinit(allocator);

        try testing.expectEqual(std.meta.Tag(WidgetKind).text, std.meta.activeTag(w.kind));
        try testing.expectEqualStrings("Hello", w.kind.text.spans[0].text);
    }

    // Test attributes
    {
        lua.createTable(0, 2);
        _ = lua.pushString("text");
        lua.setField(-2, "type");
        lua.createTable(1, 0);
        _ = lua.pushString("Content");
        lua.rawSetIndex(-2, 1);
        lua.setField(-2, "content");
        _ = lua.pushString("word");
        lua.setField(-2, "wrap");
        _ = lua.pushString("center");
        lua.setField(-2, "align");

        var w = try parseWidget(lua, allocator, -1);
        defer w.deinit(allocator);

        try testing.expectEqual(Text.Wrap.word, w.kind.text.wrap);
        try testing.expectEqual(Text.Align.center, w.kind.text.@"align");
    }
}

test "parseWidget - column" {
    const testing = std.testing;
    const allocator = testing.allocator;

    var lua = try ziglua.Lua.init(allocator);
    defer lua.deinit();

    // Create column widget table
    lua.createTable(0, 2);
    _ = lua.pushString("column");
    lua.setField(-2, "type");

    // Create children table
    lua.createTable(2, 0);

    // Child 1: text
    lua.createTable(0, 2);
    _ = lua.pushString("text");
    lua.setField(-2, "type");
    lua.createTable(1, 0);
    _ = lua.pushString("Child 1");
    lua.rawSetIndex(-2, 1);
    lua.setField(-2, "content");
    lua.rawSetIndex(-2, 1);

    // Child 2: text
    lua.createTable(0, 2);
    _ = lua.pushString("text");
    lua.setField(-2, "type");
    lua.createTable(1, 0);
    _ = lua.pushString("Child 2");
    lua.rawSetIndex(-2, 1);
    lua.setField(-2, "content");
    lua.rawSetIndex(-2, 2);

    lua.setField(-2, "children");

    var w = try parseWidget(lua, allocator, -1);
    defer w.deinit(allocator);

    try testing.expectEqual(std.meta.Tag(WidgetKind).column, std.meta.activeTag(w.kind));
    try testing.expectEqual(@as(usize, 2), w.kind.column.children.len);
    try testing.expectEqualStrings("Child 1", w.kind.column.children[0].kind.text.spans[0].text);

    // Default alignment
    try testing.expectEqual(CrossAxisAlignment.center, w.kind.column.cross_axis_align);

    // Test explicit alignment
    lua.createTable(0, 2);
    _ = lua.pushString("column");
    lua.setField(-2, "type");
    lua.createTable(0, 0);
    lua.setField(-2, "children");
    _ = lua.pushString("stretch");
    lua.setField(-2, "cross_axis_align");

    var w2 = try parseWidget(lua, allocator, -1);
    defer w2.deinit(allocator);

    try testing.expectEqual(CrossAxisAlignment.stretch, w2.kind.column.cross_axis_align);
}

test "Text Iterator" {
    const testing = std.testing;
    const allocator = testing.allocator;

    // Test simple wrapping
    var spans = [_]Text.Span{
        .{ .text = "Hello World", .style = .{} },
    };
    const text: Text = .{ .spans = &spans, .wrap = .word };

    var iter: Text.Iterator = .{
        .text = text,
        .max_width = 5,
        .allocator = allocator,
    };

    const l1 = (try iter.next()).?;
    defer allocator.free(l1.segments);
    try testing.expectEqualStrings("Hello", l1.segments[0].text);

    const l2 = (try iter.next()).?;
    defer allocator.free(l2.segments);
    try testing.expectEqualStrings(" ", l2.segments[0].text);

    const l3 = (try iter.next()).?;
    defer allocator.free(l3.segments);
    try testing.expectEqualStrings("World", l3.segments[0].text);

    try testing.expect((try iter.next()) == null);

    // Test no wrap
    const text_nowrap: Text = .{ .spans = &spans, .wrap = .none };
    var iter_nowrap: Text.Iterator = .{
        .text = text_nowrap,
        .max_width = 8,
        .allocator = allocator,
    };

    const l_nowrap = (try iter_nowrap.next()).?;
    defer allocator.free(l_nowrap.segments);
    try testing.expectEqualStrings("Hello World", l_nowrap.segments[0].text);
    try testing.expect((try iter_nowrap.next()) == null);
}

test "Stack Layout" {
    const testing = std.testing;

    const child1: Widget = .{
        .kind = .{ .surface = .{ .pty_id = 1, .surface = undefined } },
    };

    const child2: Widget = .{
        .kind = .{ .surface = .{ .pty_id = 2, .surface = undefined } },
    };

    var children = [_]Widget{ child1, child2 };
    var stack: Widget = .{
        .kind = .{ .stack = .{ .children = &children } },
    };

    const constraints: BoxConstraints = .{
        .min_width = 0,
        .max_width = 80,
        .min_height = 0,
        .max_height = 24,
    };

    const size = stack.layout(constraints);

    try testing.expectEqual(@as(u16, 80), size.width);
    try testing.expectEqual(@as(u16, 24), size.height);

    try testing.expectEqual(@as(u16, 0), children[0].x);
    try testing.expectEqual(@as(u16, 0), children[0].y);
    try testing.expectEqual(@as(u16, 0), children[1].x);
    try testing.expectEqual(@as(u16, 0), children[1].y);
}

test "Positioned Layout - explicit position" {
    const testing = std.testing;

    var child: Widget = .{
        .kind = .{ .surface = .{ .pty_id = 1, .surface = undefined } },
    };

    var pos: Widget = .{
        .kind = .{ .positioned = .{
            .child = &child,
            .x = 10,
            .y = 5,
            .anchor = .top_left,
        } },
    };

    const constraints: BoxConstraints = .{
        .min_width = 0,
        .max_width = 80,
        .min_height = 0,
        .max_height = 24,
    };

    _ = pos.layout(constraints);

    try testing.expectEqual(@as(u16, 10), child.x);
    try testing.expectEqual(@as(u16, 5), child.y);
}

test "Positioned Layout - center anchor" {
    const testing = std.testing;

    const span: Text.Span = .{ .text = "Hello", .style = .{} };
    var spans = [_]Text.Span{span};
    var child: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    var pos: Widget = .{
        .kind = .{ .positioned = .{
            .child = &child,
            .x = null,
            .y = null,
            .anchor = .center,
        } },
    };

    const constraints: BoxConstraints = .{
        .min_width = 0,
        .max_width = 80,
        .min_height = 0,
        .max_height = 24,
    };

    _ = pos.layout(constraints);

    try testing.expectEqual(@as(u16, 37), child.x);
    try testing.expectEqual(@as(u16, 11), child.y);
}

test "Row Layout - equal split" {
    const left: Widget = .{ .kind = .{ .surface = .{ .pty_id = 1, .surface = undefined } } };
    const right: Widget = .{ .kind = .{ .surface = .{ .pty_id = 2, .surface = undefined } } };

    var children = [_]Widget{ left, right };
    var row: Widget = .{
        .kind = .{ .row = .{ .children = &children, .cross_axis_align = .stretch } },
    };

    _ = row.layout(fixedConstraints(80, 24));

    try expectRect(&row, 0, 0, 80, 24);
    try expectRect(&children[0], 0, 0, 40, 24);
    try expectRect(&children[1], 40, 0, 40, 24);
}

test "Row Layout - with ratio" {
    const left: Widget = .{ .kind = .{ .surface = .{ .pty_id = 1, .surface = undefined } }, .ratio = 0.25 };
    const right: Widget = .{ .kind = .{ .surface = .{ .pty_id = 2, .surface = undefined } } };

    var children = [_]Widget{ left, right };
    var row: Widget = .{
        .kind = .{ .row = .{ .children = &children, .cross_axis_align = .stretch } },
    };

    _ = row.layout(fixedConstraints(80, 24));

    try expectRect(&children[0], 0, 0, 20, 24);
    try expectRect(&children[1], 20, 0, 60, 24);
}

test "Padding Layout" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "X", .style = .{} }};
    const text_widget: Widget = .{ .kind = .{ .text = .{ .spans = &spans } } };

    const child = try allocator.create(Widget);
    defer allocator.destroy(child);
    child.* = text_widget;

    var padded: Widget = .{
        .kind = .{ .padding = .{
            .child = child,
            .top = 1,
            .bottom = 1,
            .left = 2,
            .right = 2,
        } },
    };

    _ = padded.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 2), child.x);
    try std.testing.expectEqual(@as(u16, 1), child.y);
    try std.testing.expectEqual(@as(u16, 5), padded.width);
    try std.testing.expectEqual(@as(u16, 3), padded.height);
}

test "Box Layout - with max_width constraint" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "Content", .style = .{} }};
    const text_widget: Widget = .{ .kind = .{ .text = .{ .spans = &spans } } };

    const child = try allocator.create(Widget);
    defer allocator.destroy(child);
    child.* = text_widget;

    var box: Widget = .{
        .kind = .{ .box = .{
            .child = child,
            .border = .single,
            .style = .{},
            .max_width = 12,
            .max_height = null,
        } },
    };

    _ = box.layout(boundsConstraints(80, 24));

    try std.testing.expectEqual(@as(u16, 9), box.width);
    try std.testing.expectEqual(@as(u16, 3), box.height);
}

test "parseWidget - stack" {
    const testing = std.testing;
    const allocator = testing.allocator;

    var lua = try ziglua.Lua.init(allocator);
    defer lua.deinit();

    lua.createTable(0, 2);
    _ = lua.pushString("stack");
    lua.setField(-2, "type");

    lua.createTable(2, 0);

    lua.createTable(0, 2);
    _ = lua.pushString("text");
    lua.setField(-2, "type");
    lua.createTable(1, 0);
    _ = lua.pushString("Background");
    lua.rawSetIndex(-2, 1);
    lua.setField(-2, "content");
    lua.rawSetIndex(-2, 1);

    lua.createTable(0, 2);
    _ = lua.pushString("text");
    lua.setField(-2, "type");
    lua.createTable(1, 0);
    _ = lua.pushString("Overlay");
    lua.rawSetIndex(-2, 1);
    lua.setField(-2, "content");
    lua.rawSetIndex(-2, 2);

    lua.setField(-2, "children");

    var w = try parseWidget(lua, allocator, -1);
    defer w.deinit(allocator);

    try testing.expectEqual(std.meta.Tag(WidgetKind).stack, std.meta.activeTag(w.kind));
    try testing.expectEqual(@as(usize, 2), w.kind.stack.children.len);
}

test "parseWidget - positioned" {
    const testing = std.testing;
    const allocator = testing.allocator;

    var lua = try ziglua.Lua.init(allocator);
    defer lua.deinit();

    lua.createTable(0, 4);
    _ = lua.pushString("positioned");
    lua.setField(-2, "type");
    _ = lua.pushInteger(10);
    lua.setField(-2, "x");
    _ = lua.pushInteger(5);
    lua.setField(-2, "y");
    _ = lua.pushString("center");
    lua.setField(-2, "anchor");

    lua.createTable(0, 2);
    _ = lua.pushString("text");
    lua.setField(-2, "type");
    lua.createTable(1, 0);
    _ = lua.pushString("Popup");
    lua.rawSetIndex(-2, 1);
    lua.setField(-2, "content");
    lua.setField(-2, "child");

    var w = try parseWidget(lua, allocator, -1);
    defer w.deinit(allocator);

    try testing.expectEqual(std.meta.Tag(WidgetKind).positioned, std.meta.activeTag(w.kind));
    try testing.expectEqual(@as(?u16, 10), w.kind.positioned.x);
    try testing.expectEqual(@as(?u16, 5), w.kind.positioned.y);
    try testing.expectEqual(Anchor.center, w.kind.positioned.anchor);
}

fn layoutBoxImpl(b: *Box, constraints: BoxConstraints) Size {
    const border_size: u16 = if (b.border == .none) 0 else 2;

    // Apply box's own constraints on top of parent constraints
    const effective_max_w = if (b.max_width) |bw|
        if (constraints.max_width) |cw| @min(bw, cw) else bw
    else
        constraints.max_width;
    const effective_max_h = if (b.max_height) |bh|
        if (constraints.max_height) |ch| @min(bh, ch) else bh
    else
        constraints.max_height;

    const inner_max_w = if (effective_max_w) |w| (if (w > border_size) w - border_size else 0) else null;
    const inner_max_h = if (effective_max_h) |h| (if (h > border_size) h - border_size else 0) else null;

    const child_size = b.child.layout(.{
        .min_width = 0,
        .max_width = inner_max_w,
        .min_height = 0,
        .max_height = inner_max_h,
    });
    b.child.x = if (b.border == .none) 0 else 1;
    b.child.y = if (b.border == .none) 0 else 1;
    b.child.width = child_size.width;
    b.child.height = child_size.height;

    return .{
        .width = child_size.width + border_size,
        .height = child_size.height + border_size,
    };
}

fn layoutPaddingImpl(p: *Padding, constraints: BoxConstraints) Size {
    const h_padding = p.left + p.right;
    const v_padding = p.top + p.bottom;

    const inner_max_w = if (constraints.max_width) |w| (if (w > h_padding) w - h_padding else 0) else null;
    const inner_max_h = if (constraints.max_height) |h| (if (h > v_padding) h - v_padding else 0) else null;

    const child_size = p.child.layout(.{
        .min_width = 0,
        .max_width = inner_max_w,
        .min_height = 0,
        .max_height = inner_max_h,
    });
    p.child.x = p.left;
    p.child.y = p.top;
    p.child.width = child_size.width;
    p.child.height = child_size.height;

    return .{
        .width = child_size.width + h_padding,
        .height = child_size.height + v_padding,
    };
}

fn layoutColumnImpl(col: *Column, constraints: BoxConstraints) Size {
    const total_height = constraints.max_height orelse 0;
    var width: u16 = 0;

    // Pass 1: Measure intrinsic children (text, separator) and count proportional children
    var intrinsic_height: u16 = 0;
    var nil_count: u16 = 0;

    for (col.children) |*child| {
        const is_intrinsic = child.ratio == null and (child.kind == .text or child.kind == .text_input or child.kind == .list or child.kind == .separator);
        if (is_intrinsic) {
            const remaining = if (total_height > intrinsic_height) total_height - intrinsic_height else 0;
            const child_constraints: BoxConstraints = .{
                .min_width = 0,
                .max_width = constraints.max_width,
                .min_height = 0,
                .max_height = remaining,
            };
            const child_size = child.layout(child_constraints);
            child.height = child_size.height;
            child.width = child_size.width;
            intrinsic_height += child_size.height;
            if (child_size.width > width) width = child_size.width;
        } else if (child.ratio == null) {
            nil_count += 1;
        }
    }

    // Available space for ratio/nil children
    const available = if (total_height > intrinsic_height) total_height - intrinsic_height else 0;

    // Pass 2: Allocate ratio children
    var used_by_ratio: u16 = 0;
    for (col.children) |*child| {
        if (child.ratio) |r| {
            const share: u16 = @intFromFloat(@as(f32, @floatFromInt(available)) * r);
            const child_constraints: BoxConstraints = .{
                .min_width = 0,
                .max_width = constraints.max_width,
                .min_height = share,
                .max_height = share,
            };
            const child_size = child.layout(child_constraints);
            child.height = child_size.height;
            child.width = child_size.width;
            used_by_ratio += share;
            if (child_size.width > width) width = child_size.width;
        }
    }

    // Pass 3: Split remaining among nil-ratio non-intrinsic children equally
    const remaining_for_nil = if (available > used_by_ratio) available - used_by_ratio else 0;
    if (nil_count > 0 and remaining_for_nil > 0) {
        var remaining = remaining_for_nil;
        var count = nil_count;
        for (col.children) |*child| {
            const is_intrinsic = child.kind == .text or child.kind == .text_input or child.kind == .list or child.kind == .separator;
            if (child.ratio == null and !is_intrinsic) {
                const share = remaining / count;
                remaining -= share;
                count -= 1;

                const child_constraints: BoxConstraints = .{
                    .min_width = 0,
                    .max_width = constraints.max_width,
                    .min_height = share,
                    .max_height = share,
                };
                const child_size = child.layout(child_constraints);
                child.height = child_size.height;
                child.width = child_size.width;
                if (child_size.width > width) width = child_size.width;
            }
        }
    }

    // For end/stretch, use container width if available
    const cross_width: u16 = switch (col.cross_axis_align) {
        .start, .center => width,
        .end, .stretch => constraints.max_width orelse width,
    };

    // Final pass: Position children
    var current_y: u16 = 0;
    var final_height: u16 = 0;
    for (col.children) |*child| {
        log.debug("Column child kind={} h={}", .{ child.kind, child.height });
        child.y = current_y;

        switch (col.cross_axis_align) {
            .start => child.x = 0,
            .center => child.x = if (width > child.width) (width - child.width) / 2 else 0,
            .end => child.x = if (cross_width > child.width) cross_width - child.width else 0,
            .stretch => {
                child.x = 0;
                child.width = cross_width;
            },
        }

        current_y += child.height;
        final_height += child.height;
    }

    return .{
        .width = if (col.cross_axis_align == .end or col.cross_axis_align == .stretch) cross_width else width,
        .height = final_height,
    };
}

fn layoutRowImpl(row: *Row, constraints: BoxConstraints) Size {
    const total_width = constraints.max_width orelse 0;
    var height: u16 = 0;

    // Pass 1: Measure intrinsic children (text, separator) and count proportional children
    var intrinsic_width: u16 = 0;
    var nil_count: u16 = 0;

    for (row.children) |*child| {
        const is_intrinsic = child.ratio == null and (child.kind == .text or child.kind == .separator);
        if (is_intrinsic) {
            const remaining = if (total_width > intrinsic_width) total_width - intrinsic_width else 0;
            const child_constraints: BoxConstraints = .{
                .min_width = 0,
                .max_width = remaining,
                .min_height = 0,
                .max_height = constraints.max_height,
            };
            const child_size = child.layout(child_constraints);
            child.height = child_size.height;
            child.width = child_size.width;
            intrinsic_width += child_size.width;
            if (child_size.height > height) height = child_size.height;
        } else if (child.ratio == null) {
            nil_count += 1;
        }
    }

    // Available space for ratio/nil children
    const available = if (total_width > intrinsic_width) total_width - intrinsic_width else 0;

    // Pass 2: Allocate ratio children
    var used_by_ratio: u16 = 0;
    for (row.children) |*child| {
        if (child.ratio) |r| {
            const share: u16 = @intFromFloat(@as(f32, @floatFromInt(available)) * r);
            const child_constraints: BoxConstraints = .{
                .min_width = share,
                .max_width = share,
                .min_height = 0,
                .max_height = constraints.max_height,
            };
            const child_size = child.layout(child_constraints);
            child.height = child_size.height;
            child.width = child_size.width;
            used_by_ratio += share;
            if (child_size.height > height) height = child_size.height;
        }
    }

    // Pass 3: Split remaining among nil-ratio non-intrinsic children equally
    const remaining_for_nil = if (available > used_by_ratio) available - used_by_ratio else 0;
    if (nil_count > 0 and remaining_for_nil > 0) {
        var remaining = remaining_for_nil;
        var count = nil_count;
        for (row.children) |*child| {
            const is_intrinsic = child.kind == .text or child.kind == .separator;
            if (child.ratio == null and !is_intrinsic) {
                const share = remaining / count;
                remaining -= share;
                count -= 1;

                const child_constraints: BoxConstraints = .{
                    .min_width = share,
                    .max_width = share,
                    .min_height = 0,
                    .max_height = constraints.max_height,
                };
                const child_size = child.layout(child_constraints);
                child.height = child_size.height;
                child.width = child_size.width;
                if (child_size.height > height) height = child_size.height;
            }
        }
    }

    // For end/stretch, use container height if available
    const cross_height: u16 = switch (row.cross_axis_align) {
        .start, .center => height,
        .end, .stretch => constraints.max_height orelse height,
    };

    // Final pass: Position children
    var current_x: u16 = 0;
    var final_width: u16 = 0;
    for (row.children) |*child| {
        child.x = current_x;

        switch (row.cross_axis_align) {
            .start => child.y = 0,
            .center => child.y = if (height > child.height) (height - child.height) / 2 else 0,
            .end => child.y = if (cross_height > child.height) cross_height - child.height else 0,
            .stretch => {
                child.y = 0;
                child.height = cross_height;
            },
        }

        current_x += child.width;
        final_width += child.width;
    }

    return .{
        .width = final_width,
        .height = height,
    };
}

fn layoutTextImpl(text: *Text, constraints: BoxConstraints) Size {
    // For layout, we need to iterate to calculate height.
    // But iterator allocates. Since layout is called often, we should probably
    // try to minimize allocation. However, strict correctness for wrapping requires
    // the full iteration logic.
    //
    // We can use a GPA for layout which is acceptable as layout is usually
    // per-frame or per-resize.
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    const alloc = gpa.allocator();
    defer _ = gpa.deinit();

    const max_width = constraints.max_width orelse 65535;

    var iter: Text.Iterator = .{
        .text = text.*,
        .max_width = max_width,
        .allocator = alloc,
    };

    var intrinsic_width: u16 = 0;
    var height: u16 = 0;

    while (iter.next() catch null) |line| {
        defer alloc.free(line.segments);
        if (line.width > intrinsic_width) intrinsic_width = line.width;
        height += 1;
    }

    const width: u16 = switch (text.@"align") {
        .left => intrinsic_width,
        .center, .right => if (constraints.max_width) |mw| mw else intrinsic_width,
    };

    return .{
        .width = width,
        .height = height,
    };
}

fn layoutStackImpl(stack: *Stack, constraints: BoxConstraints) Size {
    var max_width: u16 = 0;
    var max_height: u16 = 0;

    for (stack.children) |*child| {
        const child_size = child.layout(constraints);
        child.x = 0;
        child.y = 0;
        if (child_size.width > max_width) max_width = child_size.width;
        if (child_size.height > max_height) max_height = child_size.height;
    }

    return .{
        .width = max_width,
        .height = max_height,
    };
}

fn layoutPositionedImpl(pos: *Positioned, constraints: BoxConstraints) Size {
    const child_size = pos.child.layout(constraints);

    const container_width = constraints.max_width orelse child_size.width;
    const container_height = constraints.max_height orelse child_size.height;

    const anchor_x: u16 = switch (pos.anchor) {
        .top_left, .center_left, .bottom_left => 0,
        .top_center, .center, .bottom_center => if (container_width > child_size.width) (container_width - child_size.width) / 2 else 0,
        .top_right, .center_right, .bottom_right => if (container_width > child_size.width) container_width - child_size.width else 0,
    };
    const anchor_y: u16 = switch (pos.anchor) {
        .top_left, .top_center, .top_right => 0,
        .center_left, .center, .center_right => if (container_height > child_size.height) (container_height - child_size.height) / 2 else 0,
        .bottom_left, .bottom_center, .bottom_right => if (container_height > child_size.height) container_height - child_size.height else 0,
    };

    pos.child.x = pos.x orelse anchor_x;
    pos.child.y = pos.y orelse anchor_y;
    pos.child.width = child_size.width;
    pos.child.height = child_size.height;

    return .{
        .width = pos.child.x + child_size.width,
        .height = pos.child.y + child_size.height,
    };
}

// ============================================================================
// Rendering Tests
// ============================================================================

const tui_test = @import("tui_test.zig");

test "render Text - simple" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{
        .{ .text = "Hello", .style = .{} },
    };
    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    _ = w.layout(boundsConstraints(10, 1));

    var screen = try tui_test.createScreen(allocator, 10, 1);
    defer screen.deinit(allocator);

    const win = tui_test.windowFromScreen(&screen);
    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 10, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("Hello     ", ascii);
}

test "render Text - centered" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{
        .{ .text = "Hi", .style = .{} },
    };
    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .@"align" = .center } },
    };

    _ = w.layout(boundsConstraints(10, 1));

    var screen = try tui_test.createScreen(allocator, 10, 1);
    defer screen.deinit(allocator);

    const win = tui_test.windowFromScreen(&screen);
    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 10, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("    Hi    ", ascii);
}

test "render Text - right aligned" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{
        .{ .text = "End", .style = .{} },
    };
    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .@"align" = .right } },
    };

    _ = w.layout(boundsConstraints(10, 1));

    var screen = try tui_test.createScreen(allocator, 10, 1);
    defer screen.deinit(allocator);

    const win = tui_test.windowFromScreen(&screen);
    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 10, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("       End", ascii);
}

test "render List - basic items" {
    const allocator = std.testing.allocator;

    var items = [_]List.Item{
        .{ .text = "One", .style = null },
        .{ .text = "Two", .style = null },
        .{ .text = "Three", .style = null },
    };
    var w: Widget = .{
        .kind = .{ .list = .{
            .items = &items,
            .selected = null,
            .scroll_offset = 0,
            .style = .{},
            .selected_style = .{},
        } },
    };

    _ = w.layout(boundsConstraints(8, 3));

    var screen = try tui_test.createScreen(allocator, 8, 3);
    defer screen.deinit(allocator);

    const win = tui_test.windowFromScreen(&screen);
    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 8, 3);
    defer allocator.free(ascii);

    const expected =
        \\One     
        \\Two     
        \\Three   
    ;
    try tui_test.expectAsciiEqual(expected, ascii);
}

test "render List - with scroll offset" {
    const allocator = std.testing.allocator;

    var items = [_]List.Item{
        .{ .text = "A", .style = null },
        .{ .text = "B", .style = null },
        .{ .text = "C", .style = null },
        .{ .text = "D", .style = null },
    };
    var w: Widget = .{
        .kind = .{ .list = .{
            .items = &items,
            .selected = null,
            .scroll_offset = 2,
            .style = .{},
            .selected_style = .{},
        } },
    };

    _ = w.layout(boundsConstraints(5, 2));

    var screen = try tui_test.createScreen(allocator, 5, 2);
    defer screen.deinit(allocator);

    const win = tui_test.windowFromScreen(&screen);
    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 5, 2);
    defer allocator.free(ascii);

    const expected =
        \\C    
        \\D    
    ;
    try tui_test.expectAsciiEqual(expected, ascii);
}

test "render Box - single border" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{
        .{ .text = "Hi", .style = .{} },
    };
    var text_widget: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    var w: Widget = .{
        .kind = .{ .box = .{
            .child = &text_widget,
            .border = .single,
            .style = .{},
            .max_width = null,
            .max_height = null,
        } },
    };

    _ = w.layout(boundsConstraints(6, 3));

    var screen = try tui_test.createScreen(allocator, 6, 3);
    defer screen.deinit(allocator);

    const win = tui_test.windowFromScreen(&screen);
    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 6, 3);
    defer allocator.free(ascii);

    const expected =
        \\┌────┐
        \\│Hi  │
        \\└────┘
    ;
    try tui_test.expectAsciiEqual(expected, ascii);
}

test "render Box - rounded border" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{
        .{ .text = "X", .style = .{} },
    };
    var text_widget: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    var w: Widget = .{
        .kind = .{ .box = .{
            .child = &text_widget,
            .border = .rounded,
            .style = .{},
            .max_width = null,
            .max_height = null,
        } },
    };

    _ = w.layout(boundsConstraints(5, 3));

    var screen = try tui_test.createScreen(allocator, 5, 3);
    defer screen.deinit(allocator);

    const win = tui_test.windowFromScreen(&screen);
    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 5, 3);
    defer allocator.free(ascii);

    const expected =
        \\╭───╮
        \\│X  │
        \\╰───╯
    ;
    try tui_test.expectAsciiEqual(expected, ascii);
}

test "render Column - stacked text widgets" {
    const allocator = std.testing.allocator;

    var spans1 = [_]Text.Span{.{ .text = "Top", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "Bot", .style = .{} }};

    var children = [_]Widget{
        .{ .kind = .{ .text = .{ .spans = &spans1 } } },
        .{ .kind = .{ .text = .{ .spans = &spans2 } } },
    };

    var w: Widget = .{
        .kind = .{ .column = .{ .children = &children, .cross_axis_align = .start } },
    };

    _ = w.layout(boundsConstraints(5, 2));

    var screen = try tui_test.createScreen(allocator, 5, 2);
    defer screen.deinit(allocator);

    const win = tui_test.windowFromScreen(&screen);
    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 5, 2);
    defer allocator.free(ascii);

    const expected =
        \\Top  
        \\Bot  
    ;
    try tui_test.expectAsciiEqual(expected, ascii);
}

test "render Row - side by side text" {
    const allocator = std.testing.allocator;

    var spans1 = [_]Text.Span{.{ .text = "A", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "B", .style = .{} }};

    var children = [_]Widget{
        .{ .kind = .{ .text = .{ .spans = &spans1 } } },
        .{ .kind = .{ .text = .{ .spans = &spans2 } } },
    };

    var w: Widget = .{
        .kind = .{ .row = .{ .children = &children, .cross_axis_align = .start } },
    };

    _ = w.layout(boundsConstraints(10, 1));

    var screen = try tui_test.createScreen(allocator, 10, 1);
    defer screen.deinit(allocator);

    const win = tui_test.windowFromScreen(&screen);
    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 10, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("AB        ", ascii);
}

// ============================================================================
// Text Layout Tests
// ============================================================================

test "layout Text - alignment affects width for center and right" {
    var spans = [_]Text.Span{
        .{ .text = "Hello", .style = .{} },
    };

    var left: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .@"align" = .left } },
    };
    var center: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .@"align" = .center } },
    };
    var right: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .@"align" = .right } },
    };

    const constraints = boundsConstraints(20, 5);

    const left_size = left.layout(constraints);
    const center_size = center.layout(constraints);
    const right_size = right.layout(constraints);

    try std.testing.expectEqual(@as(u16, 5), left_size.width);
    try std.testing.expectEqual(@as(u16, 1), left_size.height);
    try std.testing.expectEqual(@as(u16, 20), center_size.width);
    try std.testing.expectEqual(@as(u16, 20), right_size.width);
}

test "render Text - left alignment" {
    const allocator = std.testing.allocator;

    var screen = try tui_test.createScreen(allocator, 10, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    var spans = [_]Text.Span{
        .{ .text = "Hello", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .@"align" = .left } },
    };
    _ = w.layout(boundsConstraints(10, 1));

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 10, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("Hello     ", ascii);
}

test "render Text - center alignment" {
    const allocator = std.testing.allocator;

    var screen = try tui_test.createScreen(allocator, 10, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    var spans = [_]Text.Span{
        .{ .text = "Hello", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .@"align" = .center } },
    };
    _ = w.layout(boundsConstraints(10, 1));

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 10, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("  Hello   ", ascii);
}

test "render Text - right alignment" {
    const allocator = std.testing.allocator;

    var screen = try tui_test.createScreen(allocator, 10, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    var spans = [_]Text.Span{
        .{ .text = "Hello", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .@"align" = .right } },
    };
    _ = w.layout(boundsConstraints(10, 1));

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 10, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("     Hello", ascii);
}

test "layout Text - single line intrinsic width" {
    var spans = [_]Text.Span{
        .{ .text = "Test", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    const size = w.layout(boundsConstraints(100, 10));

    try std.testing.expectEqual(@as(u16, 4), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);
}

test "layout Text - multiple spans same line" {
    var spans = [_]Text.Span{
        .{ .text = "Hello", .style = .{} },
        .{ .text = " ", .style = .{} },
        .{ .text = "World", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    const size = w.layout(boundsConstraints(100, 10));

    try std.testing.expectEqual(@as(u16, 11), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);
}

test "layout Text - empty spans" {
    var spans = [_]Text.Span{};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    const size = w.layout(boundsConstraints(100, 10));

    try std.testing.expectEqual(@as(u16, 0), size.width);
    try std.testing.expectEqual(@as(u16, 0), size.height);
}

test "layout Text - wrap none ignores max_width" {
    var spans = [_]Text.Span{
        .{ .text = "Hello World", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .none } },
    };

    const size = w.layout(boundsConstraints(5, 10));

    try std.testing.expectEqual(@as(u16, 11), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);
}

test "render Text - wrap none single line" {
    const allocator = std.testing.allocator;

    var screen = try tui_test.createScreen(allocator, 15, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    var spans = [_]Text.Span{
        .{ .text = "Hello World", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .none } },
    };
    _ = w.layout(boundsConstraints(15, 1));

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 15, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("Hello World    ", ascii);
}

test "layout Text - wrap word multi-line" {
    var spans = [_]Text.Span{
        .{ .text = "Hello World Test", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .word } },
    };

    const size = w.layout(boundsConstraints(8, 10));

    try std.testing.expectEqual(@as(u16, 3), size.height);
}

test "render Text - wrap word breaks at spaces" {
    const allocator = std.testing.allocator;

    var screen = try tui_test.createScreen(allocator, 8, 3);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    var spans = [_]Text.Span{
        .{ .text = "Hello World", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .word } },
    };
    _ = w.layout(boundsConstraints(8, 3));

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 8, 3);
    defer allocator.free(ascii);

    const expected =
        \\Hello   
        \\World   
        \\        
    ;
    try tui_test.expectAsciiEqual(expected, ascii);
}

test "layout Text - wrap char breaks mid-word" {
    var spans = [_]Text.Span{
        .{ .text = "HelloWorld", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .char } },
    };

    const size = w.layout(boundsConstraints(4, 10));

    try std.testing.expectEqual(@as(u16, 4), size.width);
    try std.testing.expectEqual(@as(u16, 3), size.height);
}

test "render Text - wrap char breaks mid-word" {
    const allocator = std.testing.allocator;

    var screen = try tui_test.createScreen(allocator, 4, 3);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    var spans = [_]Text.Span{
        .{ .text = "HelloWorld", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .char } },
    };
    _ = w.layout(boundsConstraints(4, 3));

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 4, 3);
    defer allocator.free(ascii);

    const expected =
        \\Hell
        \\oWor
        \\ld  
    ;
    try tui_test.expectAsciiEqual(expected, ascii);
}

test "render Text - multiple spans concatenate on single line" {
    const allocator = std.testing.allocator;

    var screen = try tui_test.createScreen(allocator, 12, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    var spans = [_]Text.Span{
        .{ .text = "Hello ", .style = .{} },
        .{ .text = "World", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .none } },
    };
    _ = w.layout(boundsConstraints(12, 1));

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 12, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("Hello World ", ascii);
}

test "render Text - multiple spans with word wrap" {
    const allocator = std.testing.allocator;

    var screen = try tui_test.createScreen(allocator, 8, 2);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    var spans = [_]Text.Span{
        .{ .text = "One ", .style = .{} },
        .{ .text = "Two ", .style = .{} },
        .{ .text = "Three", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .word } },
    };
    _ = w.layout(boundsConstraints(8, 2));

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 8, 2);
    defer allocator.free(ascii);

    const expected =
        \\One Two 
        \\Three   
    ;
    try tui_test.expectAsciiEqual(expected, ascii);
}

test "render Text - span with leading space consumed by word wrap" {
    const allocator = std.testing.allocator;

    var screen = try tui_test.createScreen(allocator, 5, 2);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    var spans = [_]Text.Span{
        .{ .text = "One", .style = .{} },
        .{ .text = " Two", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .word } },
    };
    _ = w.layout(boundsConstraints(5, 2));

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 5, 2);
    defer allocator.free(ascii);

    // Word wrap consumes trailing/leading spaces at line breaks
    const expected =
        \\One  
        \\Two  
    ;
    try tui_test.expectAsciiEqual(expected, ascii);
}

test "layout Text - multi-line height calculation with word wrap" {
    var spans = [_]Text.Span{
        .{ .text = "Hello world this wraps", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .word } },
    };

    const size = w.layout(boundsConstraints(10, 10));

    try std.testing.expectEqual(@as(u16, 3), size.height);
    try std.testing.expect(size.width <= 10);
}

test "layout Text - multi-line height with char wrap" {
    var spans = [_]Text.Span{
        .{ .text = "ABCDEFGHIJKLMNO", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .char } },
    };

    const size = w.layout(boundsConstraints(5, 10));

    try std.testing.expectEqual(@as(u16, 3), size.height);
    try std.testing.expectEqual(@as(u16, 5), size.width);
}

test "layout Text - no wrap reports full width single line" {
    var spans = [_]Text.Span{
        .{ .text = "A long line of text", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .none } },
    };

    const size = w.layout(boundsConstraints(10, 5));

    try std.testing.expectEqual(@as(u16, 1), size.height);
    try std.testing.expectEqual(@as(u16, 19), size.width);
}

test "layout Text - flex in Column with ratio uses intrinsic height for positioning" {
    var spans1 = [_]Text.Span{.{ .text = "Top", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "Bottom", .style = .{} }};

    var children = [_]Widget{
        .{ .ratio = 0.5, .kind = .{ .text = .{ .spans = &spans1 } } },
        .{ .ratio = 0.5, .kind = .{ .text = .{ .spans = &spans2 } } },
    };

    var col: Widget = .{
        .kind = .{ .column = .{ .children = &children } },
    };

    _ = col.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 0), children[0].y);
    try std.testing.expectEqual(@as(u16, 1), children[1].y);
    try std.testing.expectEqual(@as(u16, 1), children[0].height);
    try std.testing.expectEqual(@as(u16, 1), children[1].height);
}

test "layout Text - flex in Row with ratio uses intrinsic width for positioning" {
    var spans1 = [_]Text.Span{.{ .text = "Left", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "Right", .style = .{} }};

    var children = [_]Widget{
        .{ .ratio = 0.5, .kind = .{ .text = .{ .spans = &spans1 } } },
        .{ .ratio = 0.5, .kind = .{ .text = .{ .spans = &spans2 } } },
    };

    var row: Widget = .{
        .kind = .{ .row = .{ .children = &children } },
    };

    _ = row.layout(boundsConstraints(20, 5));

    try std.testing.expectEqual(@as(u16, 0), children[0].x);
    try std.testing.expectEqual(@as(u16, 4), children[1].x);
    try std.testing.expectEqual(@as(u16, 4), children[0].width);
    try std.testing.expectEqual(@as(u16, 5), children[1].width);
}

test "layout Text - flex in Row with ratio constrains word wrap" {
    var spans1 = [_]Text.Span{.{ .text = "Hello World", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "Test", .style = .{} }};

    var children = [_]Widget{
        .{ .ratio = 0.5, .kind = .{ .text = .{ .spans = &spans1, .wrap = .word } } },
        .{ .ratio = 0.5, .kind = .{ .text = .{ .spans = &spans2 } } },
    };

    var row: Widget = .{
        .kind = .{ .row = .{ .children = &children } },
    };

    _ = row.layout(boundsConstraints(16, 5));

    try std.testing.expectEqual(@as(u16, 2), children[0].height);
    try std.testing.expectEqual(@as(u16, 6), children[0].width);
}

test "layout Text - flex in Row with center align fills allocated width" {
    var spans1 = [_]Text.Span{.{ .text = "Hi", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "There", .style = .{} }};

    var children = [_]Widget{
        .{ .ratio = 0.5, .kind = .{ .text = .{ .spans = &spans1, .@"align" = .center } } },
        .{ .ratio = 0.5, .kind = .{ .text = .{ .spans = &spans2, .@"align" = .center } } },
    };

    var row: Widget = .{
        .kind = .{ .row = .{ .children = &children } },
    };

    _ = row.layout(boundsConstraints(20, 5));

    try std.testing.expectEqual(@as(u16, 10), children[0].width);
    try std.testing.expectEqual(@as(u16, 10), children[1].width);
    try std.testing.expectEqual(@as(u16, 0), children[0].x);
    try std.testing.expectEqual(@as(u16, 10), children[1].x);
}

test "layout Text - empty spans array returns zero dimensions" {
    var spans = [_]Text.Span{};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 0), size.width);
    try std.testing.expectEqual(@as(u16, 0), size.height);
}

test "render Text - empty spans array renders without crash" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    _ = w.layout(boundsConstraints(10, 5));

    var screen = try tui_test.createScreen(allocator, 10, 5);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 10, 5);
    defer allocator.free(ascii);

    const expected =
        \\          
        \\          
        \\          
        \\          
        \\          
    ;
    try tui_test.expectAsciiEqual(expected, ascii);
}

test "layout Text - single span with empty string returns zero dimensions" {
    var spans = [_]Text.Span{.{ .text = "", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 0), size.width);
    try std.testing.expectEqual(@as(u16, 0), size.height);
}

test "render Text - single span with empty string renders without crash" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    _ = w.layout(boundsConstraints(10, 5));

    var screen = try tui_test.createScreen(allocator, 10, 5);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 10, 5);
    defer allocator.free(ascii);

    const expected =
        \\          
        \\          
        \\          
        \\          
        \\          
    ;
    try tui_test.expectAsciiEqual(expected, ascii);
}

// ============================================================================
// Unicode/Wide Character Tests
// ============================================================================

test "layout Text - CJK double-width characters" {
    // CJK characters are double-width (each takes 2 columns)
    // "你好" = 2 characters × 2 width = 4 columns
    var spans = [_]Text.Span{.{ .text = "你好", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 4), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);
}

test "layout Text - mixed ASCII and CJK" {
    // "Hi你好" = 2 ASCII (2 cols) + 2 CJK (4 cols) = 6 columns
    var spans = [_]Text.Span{.{ .text = "Hi你好", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 6), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);
}

test "layout Text - CJK with word wrap" {
    // "你好世界" = 4 CJK chars × 2 width = 8 columns
    // With max_width=6, should wrap: "你好世" (6 cols) on line 1, "界" (2 cols) on line 2
    // Note: char wrap breaks at character boundaries
    var spans = [_]Text.Span{.{ .text = "你好世界", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .char } },
    };

    const size = w.layout(boundsConstraints(6, 10));

    try std.testing.expectEqual(@as(u16, 2), size.height);
}

test "render Text - CJK characters" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "你好", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    _ = w.layout(boundsConstraints(10, 1));

    var screen = try tui_test.createScreen(allocator, 10, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 10, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("你好      ", ascii);
}

test "layout Text - emoji single codepoint" {
    // Simple emoji like ⭐ is typically double-width
    var spans = [_]Text.Span{.{ .text = "⭐", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    // Star emoji is typically width 2
    try std.testing.expectEqual(@as(u16, 2), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);
}

test "layout Text - emoji mixed with text" {
    // "Hi⭐" = 2 ASCII + 2 emoji width = 4 columns
    var spans = [_]Text.Span{.{ .text = "Hi⭐", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 4), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);
}

test "render Text - emoji" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "A⭐B", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    _ = w.layout(boundsConstraints(10, 1));

    var screen = try tui_test.createScreen(allocator, 10, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 10, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("A⭐B      ", ascii);
}

test "layout Text - box drawing characters" {
    // Box drawing chars like ╭─╮ are single-width
    var spans = [_]Text.Span{.{ .text = "╭──╮", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 4), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);
}

test "render Text - box drawing characters" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "╭─╮", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    _ = w.layout(boundsConstraints(5, 1));

    var screen = try tui_test.createScreen(allocator, 5, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 5, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("╭─╮  ", ascii);
}

test "layout Text - accented characters" {
    // Characters with combining marks: é can be e + combining acute
    // But as a precomposed char (é U+00E9), it's single-width
    var spans = [_]Text.Span{.{ .text = "café", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 4), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);
}

test "render Text - accented characters" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "café", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    _ = w.layout(boundsConstraints(6, 1));

    var screen = try tui_test.createScreen(allocator, 6, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 6, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("café  ", ascii);
}

test "layout Text - CJK centered alignment" {
    var spans = [_]Text.Span{.{ .text = "你好", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .@"align" = .center } },
    };

    const size = w.layout(boundsConstraints(10, 1));

    // Centered text takes full constraint width
    try std.testing.expectEqual(@as(u16, 10), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);
}

test "render Text - CJK centered" {
    const allocator = std.testing.allocator;

    // "你好" = 4 columns, centered in 10 = 3 spaces on each side
    var spans = [_]Text.Span{.{ .text = "你好", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .@"align" = .center } },
    };

    _ = w.layout(boundsConstraints(10, 1));

    var screen = try tui_test.createScreen(allocator, 10, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 10, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("   你好   ", ascii);
}

// ============================================================================
// Text Edge Case Tests
// ============================================================================

test "layout Text - long word exceeds container with word wrap" {
    // Word wrap with a word longer than max_width should break character-by-character
    var spans = [_]Text.Span{.{ .text = "Supercalifragilistic", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .word } },
    };

    const size = w.layout(boundsConstraints(5, 10));

    // Should wrap into multiple lines
    try std.testing.expectEqual(@as(u16, 5), size.width);
    try std.testing.expectEqual(@as(u16, 4), size.height); // 20 chars / 5 = 4 lines
}

test "render Text - long word exceeds container with word wrap" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "ABCDEFGH", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .word } },
    };

    _ = w.layout(boundsConstraints(3, 4));

    var screen = try tui_test.createScreen(allocator, 3, 4);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 3, 4);
    defer allocator.free(ascii);

    // Word wrap falls back to char wrap for long words
    try tui_test.expectAsciiEqual(
        \\ABC
        \\DEF
        \\GH 
        \\   
    , ascii);
}

test "layout Text - text exactly fills width" {
    var spans = [_]Text.Span{.{ .text = "12345", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    const size = w.layout(boundsConstraints(5, 1));

    try std.testing.expectEqual(@as(u16, 5), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);
}

test "render Text - text exactly fills width" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "ABCDE", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    _ = w.layout(boundsConstraints(5, 1));

    var screen = try tui_test.createScreen(allocator, 5, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 5, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("ABCDE", ascii);
}

test "layout Text - whitespace only" {
    var spans = [_]Text.Span{.{ .text = "   ", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    const size = w.layout(boundsConstraints(10, 1));

    try std.testing.expectEqual(@as(u16, 3), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);
}

test "render Text - whitespace only" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "   ", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    _ = w.layout(boundsConstraints(5, 1));

    var screen = try tui_test.createScreen(allocator, 5, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 5, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("     ", ascii);
}

test "layout Text - empty span in middle" {
    var spans = [_]Text.Span{
        .{ .text = "Hello", .style = .{} },
        .{ .text = "", .style = .{} },
        .{ .text = "World", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    const size = w.layout(boundsConstraints(20, 1));

    try std.testing.expectEqual(@as(u16, 10), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);
}

test "render Text - empty span in middle" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{
        .{ .text = "AB", .style = .{} },
        .{ .text = "", .style = .{} },
        .{ .text = "CD", .style = .{} },
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    _ = w.layout(boundsConstraints(10, 1));

    var screen = try tui_test.createScreen(allocator, 10, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 10, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("ABCD      ", ascii);
}

test "layout Text - very narrow container width 1" {
    var spans = [_]Text.Span{.{ .text = "ABC", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .char } },
    };

    const size = w.layout(boundsConstraints(1, 10));

    try std.testing.expectEqual(@as(u16, 1), size.width);
    try std.testing.expectEqual(@as(u16, 3), size.height);
}

test "render Text - very narrow container width 1" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "XYZ", .style = .{} }};

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans, .wrap = .char } },
    };

    _ = w.layout(boundsConstraints(1, 3));

    var screen = try tui_test.createScreen(allocator, 1, 3);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 1, 3);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual(
        \\X
        \\Y
        \\Z
    , ascii);
}

test "render Text - span style preservation" {
    const allocator = std.testing.allocator;

    // Create spans with different styles
    var spans = [_]Text.Span{
        .{ .text = "AB", .style = .{ .fg = .{ .index = 1 } } }, // red
        .{ .text = "CD", .style = .{ .fg = .{ .index = 2 } } }, // green
    };

    var w: Widget = .{
        .kind = .{ .text = .{ .spans = &spans } },
    };

    _ = w.layout(boundsConstraints(10, 1));

    var screen = try tui_test.createScreen(allocator, 10, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    // Verify text renders correctly
    const ascii = try tui_test.screenToAscii(allocator, &screen, 10, 1);
    defer allocator.free(ascii);
    try tui_test.expectAsciiEqual("ABCD      ", ascii);

    // Verify styles are preserved - check foreground colors
    const cell_a = screen.readCell(0, 0).?;
    const cell_b = screen.readCell(1, 0).?;
    const cell_c = screen.readCell(2, 0).?;
    const cell_d = screen.readCell(3, 0).?;

    // First span (AB) should have index 1
    try std.testing.expectEqual(@as(u8, 1), cell_a.style.fg.index);
    try std.testing.expectEqual(@as(u8, 1), cell_b.style.fg.index);
    // Second span (CD) should have index 2
    try std.testing.expectEqual(@as(u8, 2), cell_c.style.fg.index);
    try std.testing.expectEqual(@as(u8, 2), cell_d.style.fg.index);
}

// ============================================================================
// List Widget Layout Tests
// ============================================================================

test "layout List - basic sizing" {
    var items = [_]List.Item{
        .{ .text = "Item 1" },
        .{ .text = "Item 2" },
        .{ .text = "Item 3" },
    };

    var w: Widget = .{
        .kind = .{ .list = .{ .items = &items } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 20), size.width);
    try std.testing.expectEqual(@as(u16, 3), size.height);
}

test "layout List - constrained height" {
    var items = [_]List.Item{
        .{ .text = "Item 1" },
        .{ .text = "Item 2" },
        .{ .text = "Item 3" },
        .{ .text = "Item 4" },
        .{ .text = "Item 5" },
    };

    var w: Widget = .{
        .kind = .{ .list = .{ .items = &items } },
    };

    const size = w.layout(boundsConstraints(20, 3));

    try std.testing.expectEqual(@as(u16, 20), size.width);
    try std.testing.expectEqual(@as(u16, 3), size.height);
}

test "layout List - empty" {
    var items = [_]List.Item{};

    var w: Widget = .{
        .kind = .{ .list = .{ .items = &items } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 20), size.width);
    try std.testing.expectEqual(@as(u16, 0), size.height);
}

test "render List - selection style applied" {
    const allocator = std.testing.allocator;

    var items = [_]List.Item{
        .{ .text = "A" },
        .{ .text = "B" },
        .{ .text = "C" },
    };

    var w: Widget = .{
        .kind = .{ .list = .{
            .items = &items,
            .selected = 1,
            .selected_style = .{ .bg = .{ .index = 4 } },
        } },
    };

    _ = w.layout(boundsConstraints(3, 3));

    var screen = try tui_test.createScreen(allocator, 3, 3);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 3, 3);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual(
        \\A  
        \\B  
        \\C  
    , ascii);

    const cell_b = screen.readCell(0, 1).?;
    try std.testing.expectEqual(@as(u8, 4), cell_b.style.bg.index);
}

// ============================================================================
// Box Widget Layout Tests
// ============================================================================

test "layout Box - single border sizing" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "Hi", .style = .{} }};
    const child = try allocator.create(Widget);
    child.* = .{ .kind = .{ .text = .{ .spans = &spans } } };
    defer allocator.destroy(child);

    var w: Widget = .{
        .kind = .{ .box = .{ .child = child, .border = .single } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 4), size.width);
    try std.testing.expectEqual(@as(u16, 3), size.height);
}

test "layout Box - no border sizing" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "Hi", .style = .{} }};
    const child = try allocator.create(Widget);
    child.* = .{ .kind = .{ .text = .{ .spans = &spans } } };
    defer allocator.destroy(child);

    var w: Widget = .{
        .kind = .{ .box = .{ .child = child, .border = .none } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 2), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);
}

test "render Box - double border style" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "X", .style = .{} }};
    const child = try allocator.create(Widget);
    child.* = .{ .kind = .{ .text = .{ .spans = &spans } } };
    defer allocator.destroy(child);

    var w: Widget = .{
        .kind = .{ .box = .{ .child = child, .border = .double } },
    };

    _ = w.layout(boundsConstraints(10, 5));

    var screen = try tui_test.createScreen(allocator, 3, 3);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 3, 3);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual(
        \\╔═╗
        \\║X║
        \\╚═╝
    , ascii);
}

// ============================================================================
// Padding Widget Tests
// ============================================================================

test "layout Padding - uniform" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "X", .style = .{} }};
    const child = try allocator.create(Widget);
    child.* = .{ .kind = .{ .text = .{ .spans = &spans } } };
    defer allocator.destroy(child);

    var w: Widget = .{
        .kind = .{ .padding = .{
            .child = child,
            .top = 1,
            .bottom = 1,
            .left = 2,
            .right = 2,
        } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 5), size.width);
    try std.testing.expectEqual(@as(u16, 3), size.height);
}

test "layout Padding - asymmetric" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "AB", .style = .{} }};
    const child = try allocator.create(Widget);
    child.* = .{ .kind = .{ .text = .{ .spans = &spans } } };
    defer allocator.destroy(child);

    var w: Widget = .{
        .kind = .{ .padding = .{
            .child = child,
            .top = 0,
            .bottom = 2,
            .left = 1,
            .right = 0,
        } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 3), size.width);
    try std.testing.expectEqual(@as(u16, 3), size.height);
}

test "render Padding - positions child correctly" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "X", .style = .{} }};
    const child = try allocator.create(Widget);
    child.* = .{ .kind = .{ .text = .{ .spans = &spans } } };
    defer allocator.destroy(child);

    var w: Widget = .{
        .kind = .{ .padding = .{
            .child = child,
            .top = 1,
            .bottom = 1,
            .left = 2,
            .right = 2,
        } },
    };

    _ = w.layout(boundsConstraints(20, 10));

    var screen = try tui_test.createScreen(allocator, 5, 3);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 5, 3);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual(
        \\     
        \\  X  
        \\     
    , ascii);
}

test "render Padding - asymmetric" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "AB", .style = .{} }};
    const child = try allocator.create(Widget);
    child.* = .{ .kind = .{ .text = .{ .spans = &spans } } };
    defer allocator.destroy(child);

    var w: Widget = .{
        .kind = .{ .padding = .{
            .child = child,
            .top = 0,
            .bottom = 2,
            .left = 1,
            .right = 0,
        } },
    };

    _ = w.layout(boundsConstraints(20, 10));

    var screen = try tui_test.createScreen(allocator, 3, 3);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 3, 3);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual(
        \\ AB
        \\   
        \\   
    , ascii);
}

// ============================================================================
// Column Widget Tests
// ============================================================================

test "layout Column - stacks children vertically" {
    var spans1 = [_]Text.Span{.{ .text = "AAA", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "BB", .style = .{} }};

    var children = [_]Widget{
        .{ .kind = .{ .text = .{ .spans = &spans1 } } },
        .{ .kind = .{ .text = .{ .spans = &spans2 } } },
    };

    var w: Widget = .{
        .kind = .{ .column = .{ .children = &children } },
    };

    _ = w.layout(boundsConstraints(10, 10));

    try expectRect(&children[0], 0, 0, 3, 1);
    try expectRect(&children[1], 0, 1, 2, 1);
}

test "layout Column - cross axis center alignment" {
    const allocator = std.testing.allocator;

    var spans1 = [_]Text.Span{.{ .text = "AAAA", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "BB", .style = .{} }};

    var children = [_]Widget{
        .{ .kind = .{ .text = .{ .spans = &spans1 } } },
        .{ .kind = .{ .text = .{ .spans = &spans2 } } },
    };

    var w: Widget = .{
        .kind = .{ .column = .{ .children = &children, .cross_axis_align = .center } },
    };

    _ = w.layout(boundsConstraints(10, 10));

    try std.testing.expectEqual(@as(u16, 0), children[0].x);
    try std.testing.expectEqual(@as(u16, 1), children[1].x);

    _ = allocator;
}

test "render Column - cross axis end alignment" {
    const allocator = std.testing.allocator;

    var spans1 = [_]Text.Span{.{ .text = "AAAA", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "BB", .style = .{} }};

    var children = [_]Widget{
        .{ .kind = .{ .text = .{ .spans = &spans1 } } },
        .{ .kind = .{ .text = .{ .spans = &spans2 } } },
    };

    var w: Widget = .{
        .kind = .{ .column = .{ .children = &children, .cross_axis_align = .end } },
    };

    _ = w.layout(boundsConstraints(6, 10));

    var screen = try tui_test.createScreen(allocator, 6, 2);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 6, 2);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual(
        \\  AAAA
        \\    BB
    , ascii);
}

test "layout Column - cross axis stretch alignment" {
    var spans1 = [_]Text.Span{.{ .text = "A", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "B", .style = .{} }};

    var children = [_]Widget{
        .{ .kind = .{ .text = .{ .spans = &spans1 } } },
        .{ .kind = .{ .text = .{ .spans = &spans2 } } },
    };

    var w: Widget = .{
        .kind = .{ .column = .{ .children = &children, .cross_axis_align = .stretch } },
    };

    _ = w.layout(boundsConstraints(6, 10));

    try std.testing.expectEqual(@as(u16, 6), children[0].width);
    try std.testing.expectEqual(@as(u16, 6), children[1].width);
}

// ============================================================================
// Row Widget Layout Tests
// ============================================================================

test "layout Row - places children horizontally" {
    const allocator = std.testing.allocator;

    var spans1 = [_]Text.Span{.{ .text = "AA", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "BB", .style = .{} }};

    var children = [_]Widget{
        .{ .kind = .{ .text = .{ .spans = &spans1 } } },
        .{ .kind = .{ .text = .{ .spans = &spans2 } } },
    };

    var w: Widget = .{
        .kind = .{ .row = .{ .children = &children } },
    };

    _ = w.layout(boundsConstraints(10, 5));

    try expectRect(&children[0], 0, 0, 2, 1);
    try expectRect(&children[1], 2, 0, 2, 1);

    _ = allocator;
}

test "layout Row - cross axis end alignment" {
    var spans1 = [_]Text.Span{.{ .text = "A", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "BB", .style = .{} }};

    var children = [_]Widget{
        .{ .kind = .{ .text = .{ .spans = &spans1 } } },
        .{ .kind = .{ .text = .{ .spans = &spans2, .wrap = .char } } },
    };

    var w: Widget = .{
        .kind = .{ .row = .{ .children = &children, .cross_axis_align = .end } },
    };

    _ = w.layout(boundsConstraints(2, 5));

    try std.testing.expectEqual(@as(u16, 0), children[0].x);
    try std.testing.expectEqual(@as(u16, 4), children[0].y);
    try std.testing.expectEqual(@as(u16, 1), children[1].x);
    try std.testing.expectEqual(@as(u16, 3), children[1].y);
    try std.testing.expectEqual(@as(u16, 2), children[1].height);
}

test "layout Row - cross axis stretch alignment" {
    var spans1 = [_]Text.Span{.{ .text = "A", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "BBB", .style = .{} }};

    var children = [_]Widget{
        .{ .kind = .{ .text = .{ .spans = &spans1 } } },
        .{ .kind = .{ .text = .{ .spans = &spans2, .wrap = .char } } },
    };

    var w: Widget = .{
        .kind = .{ .row = .{ .children = &children, .cross_axis_align = .stretch } },
    };

    _ = w.layout(boundsConstraints(2, 5));

    try std.testing.expectEqual(@as(u16, 5), children[0].height);
    try std.testing.expectEqual(@as(u16, 5), children[1].height);
}

// ============================================================================
// Stack Widget Tests
// ============================================================================

test "layout Stack - takes max size of children" {
    const allocator = std.testing.allocator;

    var spans1 = [_]Text.Span{.{ .text = "Short", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "LongerText", .style = .{} }};

    var children = [_]Widget{
        .{ .kind = .{ .text = .{ .spans = &spans1 } } },
        .{ .kind = .{ .text = .{ .spans = &spans2 } } },
    };

    var w: Widget = .{
        .kind = .{ .stack = .{ .children = &children } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 10), size.width);
    try std.testing.expectEqual(@as(u16, 1), size.height);

    _ = allocator;
}

test "layout Stack - children at origin" {
    const allocator = std.testing.allocator;

    var spans1 = [_]Text.Span{.{ .text = "A", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "B", .style = .{} }};

    var children = [_]Widget{
        .{ .kind = .{ .text = .{ .spans = &spans1 } } },
        .{ .kind = .{ .text = .{ .spans = &spans2 } } },
    };

    var w: Widget = .{
        .kind = .{ .stack = .{ .children = &children } },
    };

    _ = w.layout(boundsConstraints(10, 5));

    try std.testing.expectEqual(@as(u16, 0), children[0].x);
    try std.testing.expectEqual(@as(u16, 0), children[0].y);
    try std.testing.expectEqual(@as(u16, 0), children[1].x);
    try std.testing.expectEqual(@as(u16, 0), children[1].y);

    _ = allocator;
}

test "render Stack - last child on top" {
    const allocator = std.testing.allocator;

    var spans1 = [_]Text.Span{.{ .text = "AAA", .style = .{} }};
    var spans2 = [_]Text.Span{.{ .text = "B", .style = .{} }};

    var children = [_]Widget{
        .{ .kind = .{ .text = .{ .spans = &spans1 } } },
        .{ .kind = .{ .text = .{ .spans = &spans2 } } },
    };

    var w: Widget = .{
        .kind = .{ .stack = .{ .children = &children } },
    };

    _ = w.layout(boundsConstraints(5, 1));

    var screen = try tui_test.createScreen(allocator, 5, 1);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 5, 1);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual("BAA  ", ascii);
}

// ============================================================================
// Positioned Widget Tests
// ============================================================================

test "layout Positioned - explicit position" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "X", .style = .{} }};
    const child = try allocator.create(Widget);
    child.* = .{ .kind = .{ .text = .{ .spans = &spans } } };
    defer allocator.destroy(child);

    var w: Widget = .{
        .kind = .{ .positioned = .{
            .child = child,
            .x = 5,
            .y = 3,
        } },
    };

    const size = w.layout(boundsConstraints(20, 10));

    try std.testing.expectEqual(@as(u16, 5), child.x);
    try std.testing.expectEqual(@as(u16, 3), child.y);
    try std.testing.expectEqual(@as(u16, 6), size.width);
    try std.testing.expectEqual(@as(u16, 4), size.height);
}

test "layout Positioned - anchor center" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "XX", .style = .{} }};
    const child = try allocator.create(Widget);
    child.* = .{ .kind = .{ .text = .{ .spans = &spans } } };
    defer allocator.destroy(child);

    var w: Widget = .{
        .kind = .{ .positioned = .{
            .child = child,
            .anchor = .center,
        } },
    };

    _ = w.layout(boundsConstraints(10, 5));

    try std.testing.expectEqual(@as(u16, 4), child.x);
    try std.testing.expectEqual(@as(u16, 2), child.y);
}

test "layout Positioned - anchor bottom_right" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "Z", .style = .{} }};
    const child = try allocator.create(Widget);
    child.* = .{ .kind = .{ .text = .{ .spans = &spans } } };
    defer allocator.destroy(child);

    var w: Widget = .{
        .kind = .{ .positioned = .{
            .child = child,
            .anchor = .bottom_right,
        } },
    };

    _ = w.layout(boundsConstraints(10, 5));

    try std.testing.expectEqual(@as(u16, 9), child.x);
    try std.testing.expectEqual(@as(u16, 4), child.y);
}

test "render Positioned - explicit position" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "X", .style = .{} }};
    const child = try allocator.create(Widget);
    child.* = .{ .kind = .{ .text = .{ .spans = &spans } } };
    defer allocator.destroy(child);

    var w: Widget = .{
        .kind = .{ .positioned = .{
            .child = child,
            .x = 2,
            .y = 1,
        } },
    };

    _ = w.layout(boundsConstraints(5, 3));

    var screen = try tui_test.createScreen(allocator, 5, 3);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 5, 3);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual(
        \\     
        \\  X  
        \\     
    , ascii);
}

test "render Positioned - anchor center" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "O", .style = .{} }};
    const child = try allocator.create(Widget);
    child.* = .{ .kind = .{ .text = .{ .spans = &spans } } };
    defer allocator.destroy(child);

    var w: Widget = .{
        .kind = .{ .positioned = .{
            .child = child,
            .anchor = .center,
        } },
    };

    _ = w.layout(boundsConstraints(5, 3));

    var screen = try tui_test.createScreen(allocator, 5, 3);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 5, 3);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual(
        \\     
        \\  O  
        \\     
    , ascii);
}

test "render Positioned - anchor bottom_right" {
    const allocator = std.testing.allocator;

    var spans = [_]Text.Span{.{ .text = "Z", .style = .{} }};
    const child = try allocator.create(Widget);
    child.* = .{ .kind = .{ .text = .{ .spans = &spans } } };
    defer allocator.destroy(child);

    var w: Widget = .{
        .kind = .{ .positioned = .{
            .child = child,
            .anchor = .bottom_right,
        } },
    };

    _ = w.layout(boundsConstraints(5, 3));

    var screen = try tui_test.createScreen(allocator, 5, 3);
    defer screen.deinit(allocator);
    const win = tui_test.windowFromScreen(&screen);

    try w.renderTo(win, allocator);

    const ascii = try tui_test.screenToAscii(allocator, &screen, 5, 3);
    defer allocator.free(ascii);

    try tui_test.expectAsciiEqual(
        \\     
        \\     
        \\    Z
    , ascii);
}
