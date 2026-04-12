//! Pseudo-terminal (PTY) creation and management.

const std = @import("std");
const builtin = @import("builtin");

const posix = std.posix;

const log = std.log.scoped(.pty);

const c = switch (builtin.os.tag) {
    .macos => @cImport({
        @cInclude("sys/ioctl.h");
        @cInclude("util.h");
        @cInclude("termios.h");
        @cInclude("unistd.h");
    }),
    else => @cImport({
        @cInclude("sys/ioctl.h");
        @cInclude("pty.h");
        @cInclude("termios.h");
        @cInclude("unistd.h");
    }),
};

const TIOCSCTTY = if (builtin.os.tag == .macos) 536900705 else c.TIOCSCTTY;
const TIOCSWINSZ = if (builtin.os.tag == .macos) 2148037735 else c.TIOCSWINSZ;

pub const Winsize = c.winsize;

pub const Process = struct {
    master: posix.fd_t,
    slave: posix.fd_t,
    pid: posix.pid_t,

    pub const OpenError = error{
        OpenptyFailed,
        SetFlagsFailed,
        ForkFailed,
        SetsidFailed,
        IoctlFailed,
        ExecFailed,
        ChdirFailed,
    };

    pub fn spawn(
        allocator: std.mem.Allocator,
        size: Winsize,
        argv: []const []const u8,
        env: ?[]const []const u8,
        cwd: ?[]const u8,
    ) OpenError!Process {
        // Precondition: terminal size must be positive (zero crashes terminal emulators)
        std.debug.assert(size.ws_row > 0);
        std.debug.assert(size.ws_col > 0);
        // Precondition: must have at least one argv element (the program to run)
        std.debug.assert(argv.len > 0);

        var master_fd: c_int = undefined;
        var slave_fd: c_int = undefined;

        var size_copy = size;
        if (c.openpty(&master_fd, &slave_fd, null, null, @ptrCast(&size_copy)) < 0) {
            return error.OpenptyFailed;
        }
        errdefer posix.close(master_fd);
        errdefer posix.close(slave_fd);

        const flags = posix.fcntl(master_fd, posix.F.GETFD, 0) catch {
            return error.SetFlagsFailed;
        };
        _ = posix.fcntl(master_fd, posix.F.SETFD, flags | posix.FD_CLOEXEC) catch {
            return error.SetFlagsFailed;
        };

        const fl_flags = posix.fcntl(master_fd, posix.F.GETFL, 0) catch {
            return error.SetFlagsFailed;
        };
        var fl_o: posix.O = @bitCast(@as(u32, @intCast(fl_flags)));
        fl_o.NONBLOCK = true;
        _ = posix.fcntl(master_fd, posix.F.SETFL, @as(u32, @bitCast(fl_o))) catch {
            return error.SetFlagsFailed;
        };

        var attrs: c.termios = undefined;
        if (c.tcgetattr(master_fd, &attrs) != 0) {
            return error.OpenptyFailed;
        }
        attrs.c_iflag |= c.IUTF8;
        if (c.tcsetattr(master_fd, c.TCSANOW, &attrs) != 0) {
            return error.OpenptyFailed;
        }

        const pid = posix.fork() catch {
            return error.ForkFailed;
        };

        if (pid == 0) {
            childProcess(allocator, slave_fd, master_fd, argv, env, cwd) catch |err| {
                log.err("child process failed: {}", .{err});
                posix.exit(1);
            };
            unreachable;
        }

        posix.close(slave_fd);

        return .{
            .master = master_fd,
            .slave = -1,
            .pid = pid,
        };
    }

    fn childProcess(
        allocator: std.mem.Allocator,
        slave_fd: posix.fd_t,
        master_fd: posix.fd_t,
        argv: []const []const u8,
        env: ?[]const []const u8,
        cwd: ?[]const u8,
    ) !void {
        var sa: posix.Sigaction = .{
            .handler = .{ .handler = posix.SIG.DFL },
            .mask = posix.sigemptyset(),
            .flags = 0,
        };
        posix.sigaction(posix.SIG.HUP, &sa, null);
        posix.sigaction(posix.SIG.INT, &sa, null);
        posix.sigaction(posix.SIG.QUIT, &sa, null);
        posix.sigaction(posix.SIG.TERM, &sa, null);
        posix.sigaction(posix.SIG.CHLD, &sa, null);

        const rc = c.setsid();
        if (rc < 0) {
            return error.SetsidFailed;
        }

        switch (posix.errno(c.ioctl(slave_fd, TIOCSCTTY, @as(c_ulong, 0)))) {
            .SUCCESS => {},
            else => return error.IoctlFailed,
        }

        try setupFd(slave_fd, posix.STDIN_FILENO);
        try setupFd(slave_fd, posix.STDOUT_FILENO);
        try setupFd(slave_fd, posix.STDERR_FILENO);

        if (slave_fd > 2) posix.close(slave_fd);
        posix.close(master_fd);

        if (cwd) |dir| {
            posix.chdir(dir) catch return error.ChdirFailed;
        }

        const argv_z = try allocator.alloc(?[*:0]const u8, argv.len + 1);
        defer allocator.free(argv_z);
        for (argv, 0..) |arg, i| {
            argv_z[i] = (try allocator.dupeZ(u8, arg)).ptr;
        }
        argv_z[argv.len] = null;

        const env_z = if (env) |e| blk: {
            const env_arr = try allocator.alloc(?[*:0]const u8, e.len + 1);
            for (e, 0..) |val, i| {
                env_arr[i] = (try allocator.dupeZ(u8, val)).ptr;
            }
            env_arr[e.len] = null;
            break :blk env_arr.ptr;
        } else null;

        const err = if (env_z) |ez|
            posix.execvpeZ(argv_z[0].?, @ptrCast(argv_z[0..argv.len :null]), @ptrCast(ez))
        else
            posix.execveZ(argv_z[0].?, @ptrCast(argv_z[0..argv.len :null]), @ptrCast(std.c.environ));
        log.err("execvpe failed: {}", .{err});
        return error.ExecFailed;
    }

    fn setupFd(src: posix.fd_t, target: i32) !void {
        switch (builtin.os.tag) {
            .linux => {
                const linux = std.os.linux;
                while (true) {
                    const rc = linux.dup3(src, target, 0);
                    switch (posix.errno(rc)) {
                        .SUCCESS => break,
                        .INTR => continue,
                        .BUSY, .INVAL => return error.Unexpected,
                        .MFILE => return error.ProcessFdQuotaExceeded,
                        else => |err| return posix.unexpectedErrno(err),
                    }
                }
            },
            .macos => {
                const flags = try posix.fcntl(src, posix.F.GETFD, 0);
                if (flags & posix.FD_CLOEXEC != 0) {
                    _ = try posix.fcntl(src, posix.F.SETFD, flags & ~@as(u32, posix.FD_CLOEXEC));
                }
                try posix.dup2(src, target);
            },
            else => @compileError("unsupported OS"),
        }
    }

    pub fn setSize(self: *Process, size: Winsize) !void {
        // Precondition: dimensions must be positive
        std.debug.assert(size.ws_row > 0);
        std.debug.assert(size.ws_col > 0);
        // Precondition: master fd must be valid (not closed)
        std.debug.assert(self.master >= 0);

        if (c.ioctl(self.master, TIOCSWINSZ, @intFromPtr(&size)) < 0) {
            return error.IoctlFailed;
        }
    }

    pub fn close(self: *Process) void {
        if (self.master != -1) posix.close(self.master);
        if (self.slave != -1) posix.close(self.slave);
    }
};

test "pty constants" {
    const testing = std.testing;

    try testing.expect(TIOCSCTTY > 0);
    try testing.expect(TIOCSWINSZ > 0);
}
