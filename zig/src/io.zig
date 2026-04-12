//! Async I/O abstraction layer with platform-specific backends.

const std = @import("std");
const builtin = @import("builtin");

const posix = std.posix;

const log = std.log.scoped(.io);

const backend = if (builtin.is_test)
    @import("io/mock.zig")
else switch (builtin.os.tag) {
    .linux => @import("io/io_uring.zig"),
    .macos => @import("io/kqueue.zig"),
    else => @compileError("unsupported platform"),
};

pub const Loop = backend.Loop;

pub const Context = struct {
    ptr: ?*anyopaque = null,
    msg: u16 = 0,
    cb: *const fn (*Loop, Completion) anyerror!void,
};

pub const Completion = struct {
    userdata: ?*anyopaque,
    msg: u16,
    callback: *const fn (*Loop, Completion) anyerror!void,
    result: Result,

    pub fn userdataCast(self: Completion, comptime T: type) *T {
        return @ptrCast(@alignCast(self.userdata.?));
    }

    pub fn msgToEnum(self: Completion, comptime T: type) T {
        return @enumFromInt(self.msg);
    }
};

pub const Result = union(enum) {
    socket: posix.socket_t,
    connect: void,
    accept: posix.socket_t,
    read: usize,
    recv: usize,
    send: usize,
    close: void,
    timer: void,
    waitpid: WaitPidResult,
    err: anyerror,
};

pub const WaitPidResult = struct {
    pid: posix.pid_t,
    status: u32,
};

pub const Task = struct {
    id: usize,
    ctx: Context,

    pub fn cancel(self: *Task, loop: *Loop) !void {
        try loop.cancel(self.id);
    }
};
