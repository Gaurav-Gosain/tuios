const std = @import("std");
const build_options = @import("build_options");

pub fn main() !void {
    var gpa: std.heap.GeneralPurposeAllocator(.{}) = .{};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    const args = try std.process.argsAlloc(allocator);
    defer std.process.argsFree(allocator, args);

    if (args.len > 1 and std.mem.eql(u8, args[1], "--version")) {
        std.log.info("tuios-server {s}", .{build_options.version});
        return;
    }

    std.log.info("tuios-server {s} starting...", .{build_options.version});

    var server = try Server.init(allocator);
    defer server.deinit();

    try server.run();
}

/// The tuios daemon server. Owns PTYs, runs ghostty-vt terminals,
/// streams render state diffs to connected clients.
const Server = struct {
    allocator: std.mem.Allocator,

    pub fn init(allocator: std.mem.Allocator) !Server {
        return .{ .allocator = allocator };
    }

    pub fn deinit(self: *Server) void {
        _ = self;
    }

    pub fn run(self: *Server) !void {
        _ = self;
        std.log.info("server: listening (stub)", .{});
    }
};
