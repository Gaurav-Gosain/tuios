//! Entry point for the tuios terminal multiplexer client.

const std = @import("std");
const builtin = @import("builtin");
const build_options = @import("build_options");
const io = @import("io.zig");
const msgpack = @import("msgpack.zig");
const rpc = @import("rpc.zig");
const server = @import("server.zig");
const client = @import("client.zig");
const posix = std.posix;

pub const version = build_options.version;

const log = std.log.scoped(.main);

/// Result of parsing command-line arguments.
const ParseResult = struct {
    /// Session name to attach to (existing session)
    attach_session: ?[]const u8 = null,
    /// Session name for a new session (user-specified)
    new_session_name: ?[]const u8 = null,
};

var log_file: ?std.fs.File = null;

pub const std_options: std.Options = .{
    .logFn = fileLogFn,
    .log_scope_levels = &.{
        .{ .scope = .page_list, .level = .warn },
    },
};

var log_buffer: [4096]u8 = undefined;

fn fileLogFn(
    comptime level: std.log.Level,
    comptime scope: @TypeOf(.enum_literal),
    comptime format: []const u8,
    args: anytype,
) void {
    const file = log_file orelse return;
    const scope_prefix = if (scope == .default) "" else "(" ++ @tagName(scope) ++ ") ";
    const prefix = "[" ++ comptime level.asText() ++ "] " ++ scope_prefix;
    const msg = std.fmt.bufPrint(&log_buffer, prefix ++ format ++ "\n", args) catch return;
    _ = file.write(msg) catch {};
}

const MAX_LOG_SIZE = 64 * 1024 * 1024; // 64 MiB

fn getLogDir(buf: []u8) ?[]const u8 {
    const home = posix.getenv("HOME") orelse return null;
    return std.fmt.bufPrint(buf, "{s}/.cache/tuios", .{home}) catch null;
}

fn initLogFile(filename: []const u8) void {
    var path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const log_dir = getLogDir(&path_buf) orelse return;

    std.fs.makeDirAbsolute(log_dir) catch |err| switch (err) {
        error.PathAlreadyExists => {},
        else => return,
    };

    var file_path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const log_path = std.fmt.bufPrint(&file_path_buf, "{s}/{s}", .{ log_dir, filename }) catch return;

    if (std.fs.openFileAbsolute(log_path, .{ .mode = .read_write })) |file| {
        const stat = file.stat() catch {
            file.close();
            return;
        };
        if (stat.size > MAX_LOG_SIZE) {
            file.setEndPos(0) catch {};
            file.seekTo(0) catch {};
        } else {
            file.seekFromEnd(0) catch {};
        }
        log_file = file;
    } else |_| {
        log_file = std.fs.createFileAbsolute(log_path, .{}) catch return;
    }
}

pub fn main() !void {
    var debug_allocator: std.heap.DebugAllocator(.{}) = .init;
    const allocator, const is_debug = gpa: {
        break :gpa switch (builtin.mode) {
            .Debug, .ReleaseSafe => .{ debug_allocator.allocator(), true },
            .ReleaseFast, .ReleaseSmall => .{ std.heap.smp_allocator, false },
        };
    };
    defer if (is_debug) {
        _ = debug_allocator.deinit();
    };

    const uid = posix.getuid();
    var socket_buffer: [256]u8 = undefined;
    const socket_path = try std.fmt.bufPrint(&socket_buffer, "/tmp/tuios-{d}.sock", .{uid});

    const result = try parseArgs(allocator, socket_path) orelse return;
    defer {
        if (result.attach_session) |s| allocator.free(s);
        if (result.new_session_name) |s| allocator.free(s);
    }
    runClient(allocator, socket_path, result) catch |err| {
        var log_dir_buf: [std.fs.max_path_bytes]u8 = undefined;

        if (err == error.ServerNotRunning) {
            try std.fs.File.stderr().writeAll(
                \\Server not running. Start with:
                \\    tuios serve
                \\
                \\For automatic startup, see 'man tuios.7'
                \\
            );
        } else if (getLogDir(&log_dir_buf)) |log_dir| {
            var msg_buf: [1024]u8 = undefined;
            const msg = try std.fmt.bufPrint(&msg_buf,
                \\Unexpected error: {s}
                \\
                \\Check logs for details:
                \\    {s}/client.log
                \\    {s}/server.log
                \\
            , .{ @errorName(err), log_dir, log_dir });
            try std.fs.File.stderr().writeAll(msg);
        } else {
            return err;
        }
        return;
    };
}

fn parseArgs(allocator: std.mem.Allocator, socket_path: []const u8) !?ParseResult {
    var args = try std.process.argsWithAllocator(allocator);
    defer args.deinit();
    _ = args.skip();

    var result: ParseResult = .{};
    errdefer if (result.new_session_name) |s| allocator.free(s);

    while (args.next()) |arg| {
        if (std.mem.eql(u8, arg, "--version") or std.mem.eql(u8, arg, "-v")) {
            try printVersion();
            return null;
        } else if (std.mem.eql(u8, arg, "--help") or std.mem.eql(u8, arg, "-h")) {
            try printHelp();
            return null;
        } else if (std.mem.eql(u8, arg, "-s") or std.mem.eql(u8, arg, "--session")) {
            const name = args.next() orelse {
                printSessionNameError("error: -s/--session requires a session name\n");
                return error.MissingArgument;
            };
            validateSessionName(name) catch |err| {
                printSessionNameValidationError(err);
                return err;
            };
            if (sessionExists(allocator, name)) {
                var err_buf: [128]u8 = undefined;
                const msg = std.fmt.bufPrint(&err_buf, "error: session '{s}' already exists\n", .{name}) catch return error.SessionAlreadyExists;
                printSessionNameError(msg);
                return error.SessionAlreadyExists;
            }
            result.new_session_name = try allocator.dupe(u8, name);
        } else if (std.mem.eql(u8, arg, "serve")) {
            initLogFile("server.log");
            try server.startServer(allocator, socket_path);
            return null;
        } else if (std.mem.eql(u8, arg, "session")) {
            if (result.new_session_name != null) {
                printSessionNameError("error: -s/--session cannot be used with 'session attach'\n");
                return error.ConflictingOptions;
            }
            const session_result = try handleSessionCommand(allocator, &args) orelse return null;
            result.attach_session = session_result.attach_session;
            return result;
        } else if (std.mem.eql(u8, arg, "pty")) {
            _ = try handlePtyCommand(allocator, &args, socket_path);
            return null;
        } else {
            log.err("Unknown command: {s}", .{arg});
            try printHelp();
            return error.UnknownCommand;
        }
    }

    return result;
}

fn printVersion() !void {
    var buf: [128]u8 = undefined;
    var stdout = std.fs.File.stdout().writer(&buf);
    defer stdout.interface.flush() catch {};
    try stdout.interface.print("tuios {s}\n", .{version});
}

const MAX_SESSION_NAME_LEN = 64;

fn validateSessionName(name: []const u8) !void {
    if (name.len == 0) return error.SessionNameEmpty;
    if (name.len > MAX_SESSION_NAME_LEN) return error.SessionNameTooLong;
    for (name) |c| {
        if (!std.ascii.isAlphanumeric(c) and c != '-' and c != '_') {
            return error.SessionNameInvalid;
        }
    }
}

fn sessionExists(allocator: std.mem.Allocator, name: []const u8) bool {
    const result = getSessionsDir(allocator) catch return false;
    defer allocator.free(result.path);
    var dir = result.dir;
    defer dir.close();

    var filename_buf: [256]u8 = undefined;
    const filename = std.fmt.bufPrint(&filename_buf, "{s}.json", .{name}) catch return false;

    dir.access(filename, .{}) catch return false;
    return true;
}

fn printSessionNameError(msg: []const u8) void {
    std.fs.File.stderr().writeAll(msg) catch {};
}

fn printSessionNameValidationError(err: anyerror) void {
    const msg = switch (err) {
        error.SessionNameEmpty => "error: session name cannot be empty\n",
        error.SessionNameTooLong => "error: session name must be 64 characters or fewer\n",
        error.SessionNameInvalid => "error: session name may only contain letters, numbers, dashes, and underscores\n",
        else => "error: invalid session name\n",
    };
    printSessionNameError(msg);
}

fn printHelp() !void {
    var buf: [4096]u8 = undefined;
    var stdout = std.fs.File.stdout().writer(&buf);
    defer stdout.interface.flush() catch {};
    try stdout.interface.print(
        \\tuios - Terminal multiplexer
        \\
        \\Usage: tuios [options] [command]
        \\
        \\Commands:
        \\  (none)     Start client, connect to server (spawns server if needed)
        \\  serve      Start the server in the foreground
        \\  session    Manage sessions (attach, list, rename, delete)
        \\  pty        Manage PTYs (list, kill)
        \\
        \\Options:
        \\  -s, --session <name>  Create a new session with the specified name
        \\  -h, --help            Show this help message
        \\  -v, --version         Show version
        \\
        \\Run 'tuios <command> --help' for more information on a command.
        \\
    , .{});
}

fn printSessionHelp() !void {
    try printSessionHelpTo(std.fs.File.stdout());
}

fn printSessionHelpTo(file: std.fs.File) !void {
    var buf: [4096]u8 = undefined;
    var writer = file.writer(&buf);
    defer writer.interface.flush() catch {};
    try writer.interface.print(
        \\tuios session - Manage sessions
        \\
        \\Usage: tuios session <command> [args]
        \\
        \\Commands:
        \\  attach [name]            Attach to a session (most recent if no name given)
        \\  list                     List all sessions
        \\  rename <old> <new>       Rename a session
        \\  delete <name>            Delete a session
        \\
        \\Options:
        \\  -h, --help               Show this help message
        \\
    , .{});
}

fn handleSessionCommand(allocator: std.mem.Allocator, args: *std.process.ArgIterator) !?ParseResult {
    const subcmd = args.next() orelse {
        try printSessionHelp();
        return error.MissingCommand;
    };

    if (std.mem.eql(u8, subcmd, "--help") or std.mem.eql(u8, subcmd, "-h")) {
        try printSessionHelp();
        return null;
    } else if (std.mem.eql(u8, subcmd, "attach")) {
        const session = if (args.next()) |s|
            try allocator.dupe(u8, s)
        else
            findMostRecentSession(allocator) catch |err| {
                if (err == error.NoSessionsFound) {
                    std.fs.File.stderr().writeAll("No sessions found\n") catch {};
                    return null;
                }
                return err;
            };
        if (!sessionExists(allocator, session)) {
            var buf: [256]u8 = undefined;
            const msg = std.fmt.bufPrint(&buf, "Session '{s}' not found\n", .{session}) catch return null;
            std.fs.File.stderr().writeAll(msg) catch {};
            allocator.free(session);
            return null;
        }
        return .{ .attach_session = session };
    } else if (std.mem.eql(u8, subcmd, "list")) {
        try listSessions(allocator);
        return null;
    } else if (std.mem.eql(u8, subcmd, "rename")) {
        const old_name = args.next() orelse {
            std.fs.File.stderr().writeAll("Missing session name. Usage: tuios session rename <old-name> <new-name>\n\nAvailable sessions:\n") catch {};
            try listSessionsTo(allocator, std.fs.File.stderr());
            return error.MissingArgument;
        };
        const new_name = args.next() orelse {
            std.fs.File.stderr().writeAll("Missing new session name. Usage: tuios session rename <old-name> <new-name>\n") catch {};
            return error.MissingArgument;
        };
        try renameSession(allocator, old_name, new_name);
        return null;
    } else if (std.mem.eql(u8, subcmd, "delete")) {
        const name = args.next() orelse {
            std.fs.File.stderr().writeAll("Missing session name. Usage: tuios session delete <name>\n\nAvailable sessions:\n") catch {};
            try listSessionsTo(allocator, std.fs.File.stderr());
            return error.MissingArgument;
        };
        try deleteSession(allocator, name);
        return null;
    } else {
        var buf: [256]u8 = undefined;
        const msg = std.fmt.bufPrint(&buf, "Unknown session command: {s}\n\n", .{subcmd}) catch return error.UnknownCommand;
        std.fs.File.stderr().writeAll(msg) catch {};
        try printSessionHelpTo(std.fs.File.stderr());
        return error.UnknownCommand;
    }
}

fn printPtyHelp() !void {
    try printPtyHelpTo(std.fs.File.stdout());
}

fn printPtyHelpTo(file: std.fs.File) !void {
    var buf: [4096]u8 = undefined;
    var writer = file.writer(&buf);
    defer writer.interface.flush() catch {};
    try writer.interface.print(
        \\tuios pty - Manage PTYs
        \\
        \\Usage: tuios pty <command> [args]
        \\
        \\Commands:
        \\  list                     List all PTYs
        \\  kill <id>                Kill a PTY by ID
        \\
        \\Options:
        \\  -h, --help               Show this help message
        \\
    , .{});
}

fn handlePtyCommand(allocator: std.mem.Allocator, args: *std.process.ArgIterator, socket_path: []const u8) !?ParseResult {
    const subcmd = args.next() orelse {
        try printPtyHelp();
        return error.MissingCommand;
    };

    if (std.mem.eql(u8, subcmd, "--help") or std.mem.eql(u8, subcmd, "-h")) {
        try printPtyHelp();
        return null;
    } else if (std.mem.eql(u8, subcmd, "list")) {
        try listPtys(allocator, socket_path);
        return null;
    } else if (std.mem.eql(u8, subcmd, "kill")) {
        const id_str = args.next() orelse {
            std.fs.File.stderr().writeAll("Missing PTY ID. Usage: tuios pty kill <id>\n\nUse 'tuios pty list' to see available PTYs.\n") catch {};
            return error.MissingArgument;
        };
        const pty_id = std.fmt.parseInt(u32, id_str, 10) catch {
            var buf: [256]u8 = undefined;
            const msg = std.fmt.bufPrint(&buf, "Invalid PTY ID: {s}\n", .{id_str}) catch return error.InvalidArgument;
            std.fs.File.stderr().writeAll(msg) catch {};
            return error.InvalidArgument;
        };
        try killPty(allocator, socket_path, pty_id);
        return null;
    } else {
        var buf: [256]u8 = undefined;
        const msg = std.fmt.bufPrint(&buf, "Unknown pty command: {s}\n\n", .{subcmd}) catch return error.UnknownCommand;
        std.fs.File.stderr().writeAll(msg) catch {};
        try printPtyHelpTo(std.fs.File.stderr());
        return error.UnknownCommand;
    }
}

fn runClient(allocator: std.mem.Allocator, socket_path: []const u8, args: ParseResult) !void {
    std.fs.accessAbsolute(socket_path, .{}) catch |err| {
        if (err == error.FileNotFound) {
            log.err("Server not running. Start it with: tuios serve", .{});
            return error.ServerNotRunning;
        }
        return err;
    };

    initLogFile("client.log");
    log.info("Connecting to server at {s}", .{socket_path});

    var loop = try io.Loop.init(allocator);
    defer loop.deinit();

    var app = switch (client.App.init(allocator)) {
        .ok => |a| a,
        .err => |init_err| {
            var buf: [512]u8 = undefined;
            var stderr = std.fs.File.stderr().writer(&buf);
            defer stderr.interface.flush() catch {};
            switch (init_err.err) {
                error.InitLuaMustReturnTable => stderr.interface.print("error: init.lua must return a UI table\n  example: return require('tuios').tiling()\n  see: man 5 tuios\n", .{}) catch {},
                error.InitLuaFailed => {
                    if (init_err.lua_msg) |lua_msg| {
                        stderr.interface.print("error: {s}\n  see: man 5 tuios\n", .{lua_msg}) catch {};
                    } else {
                        stderr.interface.print("error: failed to load init.lua\n  see: man 5 tuios\n", .{}) catch {};
                    }
                },
                error.DefaultUIFailed => {
                    if (init_err.lua_msg) |lua_msg| {
                        stderr.interface.print("error: failed to load default UI: {s}\n", .{lua_msg}) catch {};
                    } else {
                        stderr.interface.print("error: failed to load default UI\n", .{}) catch {};
                    }
                },
                else => {},
            }
            return init_err.err;
        },
    };
    defer app.deinit();

    // Initialize TTY after App is in its final memory location
    // (tty writer holds pointer to tty_buffer)
    try app.initTty();

    app.socket_path = socket_path;
    app.attach_session = args.attach_session;
    app.new_session_name = args.new_session_name;

    var cwd_buf: [std.fs.max_path_bytes]u8 = undefined;
    app.initial_cwd = posix.getcwd(&cwd_buf) catch null;

    try app.setup(&loop);
    try loop.run(.until_done);

    if (app.state.connection_refused) {
        log.err("Connection refused. Server may have crashed. Start it with: tuios serve", .{});
        posix.unlink(socket_path) catch {};
        return error.ConnectionRefused;
    }
}

fn getSessionsDir(allocator: std.mem.Allocator) !struct { dir: std.fs.Dir, path: []const u8 } {
    const home = std.posix.getenv("HOME") orelse return error.NoHomeDirectory;
    const sessions_dir = try std.fs.path.join(allocator, &.{ home, ".local", "state", "tuios", "sessions" });

    const dir = std.fs.openDirAbsolute(sessions_dir, .{ .iterate = true }) catch |err| {
        allocator.free(sessions_dir);
        if (err == error.FileNotFound) {
            return error.NoSessionsFound;
        }
        return err;
    };

    return .{ .dir = dir, .path = sessions_dir };
}

fn listSessions(allocator: std.mem.Allocator) !void {
    try listSessionsTo(allocator, std.fs.File.stdout());
}

fn listSessionsTo(allocator: std.mem.Allocator, file: std.fs.File) !void {
    var buf: [4096]u8 = undefined;
    var writer = file.writer(&buf);
    defer writer.interface.flush() catch {};

    const result = getSessionsDir(allocator) catch |err| {
        if (err == error.NoSessionsFound) {
            try writer.interface.print("No sessions found.\n", .{});
            return;
        }
        return err;
    };
    defer allocator.free(result.path);
    var dir = result.dir;
    defer dir.close();

    var count: usize = 0;

    var iter = dir.iterate();
    while (try iter.next()) |entry| {
        if (entry.kind != .file) continue;
        if (!std.mem.endsWith(u8, entry.name, ".json")) continue;

        const name_without_ext = entry.name[0 .. entry.name.len - 5];
        try writer.interface.print("{s}\n", .{name_without_ext});
        count += 1;
    }

    if (count == 0) {
        try writer.interface.print("No sessions found.\n", .{});
    }
}

fn renameSession(allocator: std.mem.Allocator, old_name: []const u8, new_name: []const u8) !void {
    var buf: [4096]u8 = undefined;
    var stdout = std.fs.File.stdout().writer(&buf);
    defer stdout.interface.flush() catch {};

    const result = getSessionsDir(allocator) catch |err| {
        if (err == error.NoSessionsFound) {
            try stdout.interface.print("Session '{s}' not found.\n", .{old_name});
            return error.SessionNotFound;
        }
        return err;
    };
    defer allocator.free(result.path);
    var dir = result.dir;
    defer dir.close();

    var old_filename_buf: [256]u8 = undefined;
    const old_filename = std.fmt.bufPrint(&old_filename_buf, "{s}.json", .{old_name}) catch {
        try stdout.interface.print("Session name too long.\n", .{});
        return error.NameTooLong;
    };

    var new_filename_buf: [256]u8 = undefined;
    const new_filename = std.fmt.bufPrint(&new_filename_buf, "{s}.json", .{new_name}) catch {
        try stdout.interface.print("Session name too long.\n", .{});
        return error.NameTooLong;
    };

    dir.access(old_filename, .{}) catch {
        try stdout.interface.print("Session '{s}' not found.\n", .{old_name});
        return error.SessionNotFound;
    };

    dir.access(new_filename, .{}) catch |err| {
        if (err != error.FileNotFound) return err;
        dir.rename(old_filename, new_filename) catch |rename_err| {
            try stdout.interface.print("Failed to rename session: {}\n", .{rename_err});
            return rename_err;
        };
        try stdout.interface.print("Renamed session '{s}' to '{s}'.\n", .{ old_name, new_name });
        return;
    };

    try stdout.interface.print("Session '{s}' already exists.\n", .{new_name});
    return error.SessionAlreadyExists;
}

fn deleteSession(allocator: std.mem.Allocator, name: []const u8) !void {
    var buf: [4096]u8 = undefined;
    var stdout = std.fs.File.stdout().writer(&buf);
    defer stdout.interface.flush() catch {};

    const result = getSessionsDir(allocator) catch |err| {
        if (err == error.NoSessionsFound) {
            try stdout.interface.print("Session '{s}' not found.\n", .{name});
            return error.SessionNotFound;
        }
        return err;
    };
    defer allocator.free(result.path);
    var dir = result.dir;
    defer dir.close();

    var filename_buf: [256]u8 = undefined;
    const filename = std.fmt.bufPrint(&filename_buf, "{s}.json", .{name}) catch {
        try stdout.interface.print("Session name too long.\n", .{});
        return error.NameTooLong;
    };

    dir.deleteFile(filename) catch |err| {
        if (err == error.FileNotFound) {
            try stdout.interface.print("Session '{s}' not found.\n", .{name});
            return error.SessionNotFound;
        }
        try stdout.interface.print("Failed to delete session: {}\n", .{err});
        return err;
    };

    try stdout.interface.print("Deleted session '{s}'.\n", .{name});
}

fn listPtys(allocator: std.mem.Allocator, socket_path: []const u8) !void {
    var buf: [4096]u8 = undefined;
    var stdout = std.fs.File.stdout().writer(&buf);
    defer stdout.interface.flush() catch {};

    const sock = posix.socket(posix.AF.UNIX, posix.SOCK.STREAM, 0) catch |err| {
        log.err("Failed to create socket: {}", .{err});
        return error.SocketError;
    };
    defer posix.close(sock);

    var addr: posix.sockaddr.un = .{ .path = undefined };
    @memcpy(addr.path[0..socket_path.len], socket_path);
    addr.path[socket_path.len] = 0;

    posix.connect(sock, @ptrCast(&addr), @sizeOf(posix.sockaddr.un)) catch |err| {
        if (err == error.ConnectionRefused or err == error.FileNotFound) {
            try stdout.interface.print("Server not running.\n", .{});
            return;
        }
        return err;
    };

    const request = try msgpack.encode(allocator, .{ 0, 1, "list_ptys", .{} });
    defer allocator.free(request);

    _ = try posix.write(sock, request);

    var response_buf: [16384]u8 = undefined;
    const n = try posix.read(sock, &response_buf);
    if (n == 0) {
        try stdout.interface.print("No response from server.\n", .{});
        return;
    }

    const msg = rpc.decodeMessage(allocator, response_buf[0..n]) catch |err| {
        log.err("Failed to decode response: {}", .{err});
        return error.DecodeError;
    };
    defer msg.deinit(allocator);

    if (msg != .response) {
        try stdout.interface.print("Unexpected response type.\n", .{});
        return;
    }

    if (msg.response.err) |err_val| {
        const err_str = if (err_val == .string) err_val.string else "unknown error";
        try stdout.interface.print("Server error: {s}\n", .{err_str});
        return;
    }

    const result = msg.response.result;
    if (result != .map) {
        try stdout.interface.print("Invalid response format.\n", .{});
        return;
    }

    var ptys: ?[]const msgpack.Value = null;
    for (result.map) |kv| {
        if (kv.key == .string and std.mem.eql(u8, kv.key.string, "ptys")) {
            if (kv.value == .array) {
                ptys = kv.value.array;
            }
        }
    }

    if (ptys == null or ptys.?.len == 0) {
        try stdout.interface.print("No PTYs running.\n", .{});
        return;
    }

    for (ptys.?) |pty_val| {
        if (pty_val != .map) continue;

        var id: ?u64 = null;
        var cwd: []const u8 = "";
        var title: []const u8 = "";
        var clients: u64 = 0;

        for (pty_val.map) |kv| {
            if (kv.key != .string) continue;
            const key = kv.key.string;

            if (std.mem.eql(u8, key, "id")) {
                id = if (kv.value == .unsigned) kv.value.unsigned else null;
            } else if (std.mem.eql(u8, key, "cwd")) {
                cwd = if (kv.value == .string) kv.value.string else "";
            } else if (std.mem.eql(u8, key, "title")) {
                title = if (kv.value == .string) kv.value.string else "";
            } else if (std.mem.eql(u8, key, "attached_client_count")) {
                clients = if (kv.value == .unsigned) kv.value.unsigned else 0;
            }
        }

        if (id) |pty_id| {
            const title_display = if (title.len > 0) title else "(no title)";
            try stdout.interface.print("{d}: {s} [{s}] ({d} clients)\n", .{ pty_id, cwd, title_display, clients });
        }
    }
}

fn killPty(allocator: std.mem.Allocator, socket_path: []const u8, pty_id: u32) !void {
    var buf: [4096]u8 = undefined;
    var stdout = std.fs.File.stdout().writer(&buf);
    defer stdout.interface.flush() catch {};

    const sock = posix.socket(posix.AF.UNIX, posix.SOCK.STREAM, 0) catch |err| {
        log.err("Failed to create socket: {}", .{err});
        return error.SocketError;
    };
    defer posix.close(sock);

    var addr: posix.sockaddr.un = .{ .path = undefined };
    @memcpy(addr.path[0..socket_path.len], socket_path);
    addr.path[socket_path.len] = 0;

    posix.connect(sock, @ptrCast(&addr), @sizeOf(posix.sockaddr.un)) catch |err| {
        if (err == error.ConnectionRefused or err == error.FileNotFound) {
            try stdout.interface.print("Server not running.\n", .{});
            return;
        }
        return err;
    };

    const request = try msgpack.encode(allocator, .{ 0, 1, "close_pty", .{.{ "id", pty_id }} });
    defer allocator.free(request);

    _ = try posix.write(sock, request);

    var response_buf: [16384]u8 = undefined;
    const n = try posix.read(sock, &response_buf);
    if (n == 0) {
        try stdout.interface.print("No response from server.\n", .{});
        return;
    }

    const msg = rpc.decodeMessage(allocator, response_buf[0..n]) catch |err| {
        log.err("Failed to decode response: {}", .{err});
        return error.DecodeError;
    };
    defer msg.deinit(allocator);

    if (msg != .response) {
        try stdout.interface.print("Unexpected response type.\n", .{});
        return;
    }

    if (msg.response.err) |err_val| {
        const err_str = if (err_val == .string) err_val.string else "unknown error";
        try stdout.interface.print("Server error: {s}\n", .{err_str});
        return;
    }

    if (msg.response.result == .string) {
        try stdout.interface.print("Error: {s}\n", .{msg.response.result.string});
        return;
    }

    try stdout.interface.print("PTY {d} killed.\n", .{pty_id});
}

fn findMostRecentSession(allocator: std.mem.Allocator) ![]const u8 {
    const result = getSessionsDir(allocator) catch |err| {
        if (err == error.NoSessionsFound) {
            log.err("No sessions directory found", .{});
        }
        return err;
    };
    defer allocator.free(result.path);
    var dir = result.dir;
    defer dir.close();

    var most_recent: ?[]const u8 = null;
    var most_recent_time: i128 = 0;

    var iter = dir.iterate();
    while (try iter.next()) |entry| {
        if (entry.kind != .file) continue;
        if (!std.mem.endsWith(u8, entry.name, ".json")) continue;

        const stat = dir.statFile(entry.name) catch continue;
        const mtime = stat.mtime;

        if (mtime > most_recent_time) {
            if (most_recent) |old| {
                allocator.free(old);
            }
            most_recent_time = mtime;
            const name_without_ext = entry.name[0 .. entry.name.len - 5];
            most_recent = try allocator.dupe(u8, name_without_ext);
        }
    }

    if (most_recent) |name| {
        log.info("Attaching to most recent session: {s}", .{name});
        return name;
    }

    log.err("No session files found", .{});
    return error.NoSessionsFound;
}

test {
    _ = @import("io/mock.zig");
    _ = @import("server.zig");
    _ = @import("msgpack.zig");
    _ = @import("rpc.zig");
    _ = @import("pty.zig");
    _ = @import("client.zig");
    _ = @import("redraw.zig");
    _ = @import("Surface.zig");
    _ = @import("widget.zig");
    _ = @import("TextInput.zig");
    _ = @import("tui_test.zig");
    _ = @import("key_encode.zig");
    _ = @import("mouse_encode.zig");
    _ = @import("vaxis_helper.zig");
    _ = @import("lua_test.zig");
    _ = @import("key_string.zig");
    _ = @import("action.zig");
    _ = @import("keybind.zig");
    _ = @import("keybind_compiler.zig");
    _ = @import("keybind_matcher.zig");

    if (builtin.os.tag.isDarwin() or builtin.os.tag.isBSD()) {
        _ = @import("io/kqueue.zig");
    } else if (builtin.os.tag == .linux) {
        _ = @import("io/io_uring.zig");
    }
}
