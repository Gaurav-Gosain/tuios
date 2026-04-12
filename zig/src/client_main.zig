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
        const stdout = std.io.getStdOut().writer();
        try stdout.print("tuios {s}\n", .{build_options.version});
        return;
    }

    var app = try App.init(allocator);
    defer app.deinit();

    try app.run();
}

/// The tuios TUI client. Renders terminal windows using libvaxis,
/// handles input, communicates with the server daemon.
const App = struct {
    allocator: std.mem.Allocator,
    vx: vaxis.Vaxis,
    tty: vaxis.Tty,
    loop: vaxis.Loop(Event),

    pub fn init(allocator: std.mem.Allocator) !App {
        var tty = try vaxis.Tty.init();
        var vx = try vaxis.Vaxis.init(allocator, .{});

        var self = App{
            .allocator = allocator,
            .vx = vx,
            .tty = tty,
            .loop = .{ .vaxis = &vx, .tty = &tty },
        };
        // Fix: point loop fields at self's owned copies
        self.loop.vaxis = &self.vx;
        self.loop.tty = &self.tty;
        return self;
    }

    pub fn deinit(self: *App) void {
        self.loop.stop();
        self.vx.deinit(self.allocator, self.tty.anyWriter());
        self.tty.deinit();
    }

    pub fn run(self: *App) !void {
        try self.loop.start();
        try self.vx.enterAltScreen(self.tty.anyWriter());
        try self.vx.setMouseMode(self.tty.anyWriter(), true);
        try self.vx.queryTerminal(self.tty.anyWriter(), 1 * std.time.ns_per_s);

        while (true) {
            const event = self.loop.nextEvent();
            switch (event) {
                .key_press => |key| {
                    // Ctrl+Q to quit
                    if (key.matches('q', .{ .ctrl = true })) return;
                },
                .winsize => |ws| {
                    try self.vx.resize(self.allocator, self.tty.anyWriter(), ws);
                },
                else => {},
            }
            try self.render();
        }
    }

    fn render(self: *App) !void {
        const win = self.vx.window();
        win.clear();

        // Placeholder: draw a centered message
        const msg = "tuios-zig (press Ctrl+Q to quit)";
        const col = if (win.width > msg.len) (win.width - @as(u16, @intCast(msg.len))) / 2 else 0;
        const row = win.height / 2;
        _ = win.printSegment(.{ .text = msg, .style = .{
            .fg = .{ .rgb = .{ 0x88, 0xc0, 0xd0 } },
            .bold = true,
        } }, .{ .col_offset = col, .row_offset = row });

        try self.vx.render(self.tty.anyWriter());
    }
};
