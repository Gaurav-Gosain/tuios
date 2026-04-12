const std = @import("std");
const vaxis = @import("vaxis");
const build_options = @import("build_options");

/// Custom event type for the tuios client. The vaxis Loop automatically
/// populates fields matching standard vaxis event names (key_press,
/// mouse, winsize, etc.). Custom fields can be posted from other threads.
const Event = union(enum) {
    key_press: vaxis.Key,
    key_release: vaxis.Key,
    mouse: vaxis.Mouse,
    focus_in,
    focus_out,
    winsize: vaxis.Winsize,
    // Custom events from server connection thread
    server_redraw,
    server_disconnected,
};

pub fn main() !void {
    var gpa: std.heap.GeneralPurposeAllocator(.{}) = .{};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    const args = try std.process.argsAlloc(allocator);
    defer std.process.argsFree(allocator, args);

    if (args.len > 1 and std.mem.eql(u8, args[1], "--version")) {
        std.log.info("tuios {s}", .{build_options.version});
        return;
    }

    var tty_buf: [4096]u8 = undefined;
    var tty = try vaxis.Tty.init(&tty_buf);
    defer tty.deinit();

    var vx = try vaxis.Vaxis.init(allocator, .{});
    defer vx.deinit(allocator, tty.writer());

    var loop: vaxis.Loop(Event) = .{ .vaxis = &vx, .tty = &tty };
    try loop.start();
    defer loop.stop();

    try vx.enterAltScreen(tty.writer());
    try vx.setMouseMode(tty.writer(), true);
    try vx.queryTerminal(tty.writer(), 1 * std.time.ns_per_s);

    while (true) {
        const event = loop.nextEvent();
        switch (event) {
            .key_press => |key| {
                if (key.matches('q', .{ .ctrl = true })) return;
            },
            .winsize => |ws| {
                try vx.resize(allocator, tty.writer(), ws);
            },
            else => {},
        }

        // Render
        const win = vx.window();
        win.clear();

        const msg = "tuios-zig (press Ctrl+Q to quit)";
        const col: u16 = if (win.width > msg.len) (win.width - @as(u16, @intCast(msg.len))) / 2 else 0;
        const row = win.height / 2;
        _ = win.printSegment(.{ .text = msg, .style = .{
            .fg = .{ .rgb = .{ 0x88, 0xc0, 0xd0 } },
            .bold = true,
        } }, .{ .col_offset = col, .row_offset = row });

        try vx.render(tty.writer());
    }
}
