//! kqueue-based I/O backend for macOS and BSD.

const std = @import("std");

const root = @import("../io.zig");

const c = std.c;
const posix = std.posix;

const log = std.log.scoped(.io_kqueue);

const EVENT_BATCH_SIZE: usize = 32;

pub const Loop = struct {
    allocator: std.mem.Allocator,
    kq: i32,
    next_id: usize = 1,
    pending: std.AutoHashMap(usize, PendingOp),

    const PendingOp = struct {
        ctx: root.Context,
        kind: OpKind,
        fd: posix.fd_t = undefined,
        buf: []u8 = &.{},
        addr: *const posix.sockaddr = undefined,
        addr_len: posix.socklen_t = 0,
        pid: posix.pid_t = undefined,
    };

    const OpKind = enum {
        socket,
        connect,
        accept,
        read,
        recv,
        send,
        close,
        timer,
        waitpid,
    };

    pub fn init(allocator: std.mem.Allocator) !Loop {
        const kq = try posix.kqueue();
        return .{
            .allocator = allocator,
            .kq = kq,
            .pending = std.AutoHashMap(usize, PendingOp).init(allocator),
        };
    }

    pub fn deinit(self: *Loop) void {
        self.pending.deinit();
        posix.close(self.kq);
    }

    pub fn socket(
        self: *Loop,
        domain: u32,
        socket_type: u32,
        protocol: u32,
        ctx: root.Context,
    ) !root.Task {
        const id = self.next_id;
        self.next_id += 1;

        // Socket creation is synchronous, complete immediately
        const fd = posix.socket(domain, socket_type, protocol) catch |err| {
            try ctx.cb(@ptrCast(self), .{
                .userdata = ctx.ptr,
                .msg = ctx.msg,
                .callback = ctx.cb,
                .result = .{ .err = err },
            });
            return root.Task{ .id = id, .ctx = ctx };
        };

        try ctx.cb(@ptrCast(self), .{
            .userdata = ctx.ptr,
            .msg = ctx.msg,
            .callback = ctx.cb,
            .result = .{ .socket = fd },
        });

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn connect(
        self: *Loop,
        fd: posix.socket_t,
        addr: *const posix.sockaddr,
        addr_len: posix.socklen_t,
        ctx: root.Context,
    ) !root.Task {
        const id = self.next_id;
        self.next_id += 1;

        // Try non-blocking connect
        const rc = posix.system.connect(fd, addr, addr_len);

        if (rc == 0) {
            // Connected immediately
            try ctx.cb(@ptrCast(self), .{
                .userdata = ctx.ptr,
                .msg = ctx.msg,
                .callback = ctx.cb,
                .result = .{ .connect = {} },
            });
        } else {
            const err = posix.errno(rc);
            if (err == .INPROGRESS) {
                // Connection in progress, wait for writability
                try self.pending.put(id, .{
                    .ctx = ctx,
                    .kind = .connect,
                    .fd = fd,
                });

                var changes = [_]posix.Kevent{.{
                    .ident = @intCast(fd),
                    .filter = c.EVFILT.WRITE,
                    .flags = c.EV.ADD | c.EV.ENABLE | c.EV.ONESHOT,
                    .fflags = 0,
                    .data = 0,
                    .udata = id,
                }};
                _ = try posix.kevent(self.kq, &changes, &[_]posix.Kevent{}, null);
            } else {
                // Immediate error - map known errors
                const result_err = switch (err) {
                    .CONNREFUSED => error.ConnectionRefused,
                    .ACCES => error.AccessDenied,
                    .TIMEDOUT => error.ConnectionTimedOut,
                    .NETUNREACH => error.NetworkUnreachable,
                    else => posix.unexpectedErrno(err),
                };
                try ctx.cb(@ptrCast(self), .{
                    .userdata = ctx.ptr,
                    .msg = ctx.msg,
                    .callback = ctx.cb,
                    .result = .{ .err = result_err },
                });
            }
        }

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn accept(
        self: *Loop,
        fd: posix.socket_t,
        ctx: root.Context,
    ) !root.Task {
        const id = self.next_id;
        self.next_id += 1;

        try self.pending.put(id, .{
            .ctx = ctx,
            .kind = .accept,
            .fd = fd,
        });

        var changes = [_]posix.Kevent{.{
            .ident = @intCast(fd),
            .filter = c.EVFILT.READ,
            .flags = c.EV.ADD | c.EV.ENABLE | c.EV.ONESHOT,
            .fflags = 0,
            .data = 0,
            .udata = id,
        }};
        _ = try posix.kevent(self.kq, &changes, &[_]posix.Kevent{}, null);

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn read(
        self: *Loop,
        fd: posix.socket_t,
        buf: []u8,
        ctx: root.Context,
    ) !root.Task {
        // Precondition: buffer must have capacity to receive data
        std.debug.assert(buf.len > 0);

        const id = self.next_id;
        self.next_id += 1;

        try self.pending.put(id, .{
            .ctx = ctx,
            .kind = .read,
            .fd = fd,
            .buf = buf,
        });

        var changes = [_]posix.Kevent{.{
            .ident = @intCast(fd),
            .filter = c.EVFILT.READ,
            .flags = c.EV.ADD | c.EV.ENABLE | c.EV.ONESHOT,
            .fflags = 0,
            .data = 0,
            .udata = id,
        }};
        _ = try posix.kevent(self.kq, &changes, &[_]posix.Kevent{}, null);

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn recv(
        self: *Loop,
        fd: posix.socket_t,
        buf: []u8,
        ctx: root.Context,
    ) !root.Task {
        // Precondition: buffer must have capacity to receive data
        std.debug.assert(buf.len > 0);

        const id = self.next_id;
        self.next_id += 1;

        try self.pending.put(id, .{
            .ctx = ctx,
            .kind = .recv,
            .fd = fd,
            .buf = buf,
        });

        var changes = [_]posix.Kevent{.{
            .ident = @intCast(fd),
            .filter = c.EVFILT.READ,
            .flags = c.EV.ADD | c.EV.ENABLE | c.EV.ONESHOT,
            .fflags = 0,
            .data = 0,
            .udata = id,
        }};
        _ = try posix.kevent(self.kq, &changes, &[_]posix.Kevent{}, null);

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn send(
        self: *Loop,
        fd: posix.socket_t,
        buf: []const u8,
        ctx: root.Context,
    ) !root.Task {
        // Precondition: must have data to send
        std.debug.assert(buf.len > 0);

        const id = self.next_id;
        self.next_id += 1;

        try self.pending.put(id, .{
            .ctx = ctx,
            .kind = .send,
            .fd = fd,
            .buf = @constCast(buf),
        });

        var changes = [_]posix.Kevent{.{
            .ident = @intCast(fd),
            .filter = c.EVFILT.WRITE,
            .flags = c.EV.ADD | c.EV.ENABLE | c.EV.ONESHOT,
            .fflags = 0,
            .data = 0,
            .udata = id,
        }};
        _ = try posix.kevent(self.kq, &changes, &[_]posix.Kevent{}, null);

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn close(
        self: *Loop,
        fd: posix.socket_t,
        ctx: root.Context,
    ) !root.Task {
        const id = self.next_id;
        self.next_id += 1;

        posix.close(fd);

        try ctx.cb(@ptrCast(self), .{
            .userdata = ctx.ptr,
            .msg = ctx.msg,
            .callback = ctx.cb,
            .result = .{ .close = {} },
        });

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn timeout(
        self: *Loop,
        nanoseconds: u64,
        ctx: root.Context,
    ) !root.Task {
        const id = self.next_id;
        self.next_id += 1;

        try self.pending.put(id, .{
            .ctx = ctx,
            .kind = .timer,
        });

        // Convert nanoseconds to milliseconds for kqueue timer
        const milliseconds = nanoseconds / std.time.ns_per_ms;

        var changes = [_]posix.Kevent{.{
            .ident = id,
            .filter = c.EVFILT.TIMER,
            .flags = c.EV.ADD | c.EV.ENABLE | c.EV.ONESHOT,
            .fflags = 0,
            .data = @intCast(milliseconds),
            .udata = id,
        }};
        _ = try posix.kevent(self.kq, &changes, &[_]posix.Kevent{}, null);

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn waitpid(
        self: *Loop,
        pid: posix.pid_t,
        ctx: root.Context,
    ) !root.Task {
        const id = self.next_id;
        self.next_id += 1;

        try self.pending.put(id, .{
            .ctx = ctx,
            .kind = .waitpid,
            .pid = pid,
        });

        var changes = [_]posix.Kevent{.{
            .ident = @intCast(pid),
            .filter = c.EVFILT.PROC,
            .flags = c.EV.ADD | c.EV.ENABLE | c.EV.ONESHOT,
            .fflags = c.NOTE.EXIT,
            .data = 0,
            .udata = id,
        }};
        _ = try posix.kevent(self.kq, &changes, &[_]posix.Kevent{}, null);

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn cancel(self: *Loop, id: usize) !void {
        const op = self.pending.get(id) orelse {
            return;
        };

        // Try to remove from kqueue by sending EV_DELETE
        // Note: may fail if event was ONESHOT and already fired
        const ident: usize = switch (op.kind) {
            .timer => id,
            .waitpid => @intCast(op.pid),
            else => @intCast(op.fd),
        };
        var changes = [_]posix.Kevent{.{
            .ident = ident,
            .filter = switch (op.kind) {
                .read, .recv, .accept => c.EVFILT.READ,
                .send, .connect => c.EVFILT.WRITE,
                .timer => c.EVFILT.TIMER,
                .waitpid => c.EVFILT.PROC,
                else => return,
            },
            .flags = c.EV.DELETE,
            .fflags = 0,
            .data = 0,
            .udata = id,
        }};

        _ = posix.kevent(self.kq, &changes, &[_]posix.Kevent{}, null) catch {};
        _ = self.pending.remove(id);
    }

    pub fn cancelByFd(self: *Loop, fd: posix.socket_t) void {
        var ids_to_cancel = std.ArrayList(usize){};
        defer ids_to_cancel.deinit(self.allocator);

        var it = self.pending.iterator();
        while (it.next()) |entry| {
            const kind = entry.value_ptr.kind;
            if (kind != .timer and kind != .waitpid and entry.value_ptr.fd == fd) {
                ids_to_cancel.append(self.allocator, entry.key_ptr.*) catch {};
            }
        }

        for (ids_to_cancel.items) |id| {
            self.cancel(id) catch {};
        }
    }

    pub fn run(self: *Loop, mode: RunMode) !void {
        var events: [EVENT_BATCH_SIZE]posix.Kevent = undefined;

        while (true) {
            if (mode == .until_done and self.pending.count() == 0) {
                break;
            }

            if (mode == .until_done and self.pending.count() > 0) {}

            const wait_timeout: ?*const posix.timespec = if (mode == .once) &.{ .sec = 0, .nsec = 0 } else null;
            const n = posix.kevent(self.kq, &[_]posix.Kevent{}, events[0..], wait_timeout) catch |err| {
                if (err == error.Unexpected) continue;
                return err;
            };

            if (n == 0 and mode == .once) break;

            for (events[0..n]) |ev| {
                const id: usize = @intCast(ev.udata);
                const op = self.pending.get(id) orelse continue;
                _ = self.pending.remove(id);

                try self.handleCompletion(op, ev);
            }

            if (mode == .once) break;
        }
    }

    fn handleCompletion(self: *Loop, op: PendingOp, ev: posix.Kevent) !void {
        const ctx = op.ctx;

        if (ev.flags & c.EV.ERROR != 0) {
            try self.invokeCallback(ctx, .{ .err = error.IOError });
            return;
        }

        const result: root.Result = switch (op.kind) {
            .connect => self.completeConnect(op.fd),
            .accept => self.completeAccept(op.fd),
            .read => self.completeRead(op.fd, op.buf),
            .recv => self.completeRecv(op.fd, op.buf),
            .send => self.completeSend(op.fd, op.buf),
            .timer => .{ .timer = {} },
            .waitpid => self.completeWaitpid(op.pid),
            .socket, .close => unreachable,
        };

        try self.invokeCallback(ctx, result);
    }

    fn invokeCallback(self: *Loop, ctx: root.Context, result: root.Result) !void {
        try ctx.cb(@ptrCast(self), .{
            .userdata = ctx.ptr,
            .msg = ctx.msg,
            .callback = ctx.cb,
            .result = result,
        });
    }

    fn completeConnect(_: *Loop, fd: posix.fd_t) root.Result {
        var err_code: i32 = undefined;
        var len: posix.socklen_t = @sizeOf(i32);
        _ = posix.system.getsockopt(fd, posix.SOL.SOCKET, posix.SO.ERROR, @ptrCast(&err_code), &len);

        if (err_code == 0) {
            return .{ .connect = {} };
        }
        return .{ .err = posix.unexpectedErrno(@enumFromInt(err_code)) };
    }

    fn completeAccept(_: *Loop, fd: posix.fd_t) root.Result {
        const client_fd = posix.accept(fd, null, null, posix.SOCK.CLOEXEC) catch |err| {
            return .{ .err = err };
        };
        return .{ .accept = client_fd };
    }

    fn completeRead(_: *Loop, fd: posix.fd_t, buf: []u8) root.Result {
        const bytes_read = posix.read(fd, buf) catch |err| {
            return .{ .err = err };
        };
        return .{ .read = bytes_read };
    }

    fn completeRecv(_: *Loop, fd: posix.fd_t, buf: []u8) root.Result {
        const bytes_read = posix.recv(fd, buf, 0) catch |err| {
            return .{ .err = err };
        };
        return .{ .recv = bytes_read };
    }

    fn completeSend(_: *Loop, fd: posix.fd_t, buf: []const u8) root.Result {
        const bytes_sent = posix.send(fd, buf, 0) catch |err| {
            return .{ .err = err };
        };
        return .{ .send = bytes_sent };
    }

    fn completeWaitpid(_: *Loop, pid: posix.pid_t) root.Result {
        const wait_result = posix.waitpid(pid, posix.W.NOHANG);
        return .{ .waitpid = .{
            .pid = wait_result.pid,
            .status = wait_result.status,
        } };
    }

    pub const RunMode = enum {
        until_done,
        once,
        forever,
    };
};

test "kqueue: timer" {
    const allocator = std.testing.allocator;
    var loop = try Loop.init(allocator);
    defer loop.deinit();

    var fired = false;
    const Cb = struct {
        fn cb(_: *root.Loop, comp: root.Completion) !void {
            const f = comp.userdataCast(bool);
            f.* = true;
        }
    };

    _ = try loop.timeout(10 * std.time.ns_per_ms, .{
        .ptr = &fired,
        .cb = Cb.cb,
    });

    try loop.run(.until_done);
    try std.testing.expect(fired);
}

test "kqueue: socket creation" {
    const allocator = std.testing.allocator;
    var loop = try Loop.init(allocator);
    defer loop.deinit();

    var fired = false;
    var socket_fd: posix.socket_t = undefined;

    const Cb = struct {
        fn cb(_: *root.Loop, comp: root.Completion) !void {
            const ctx = comp.userdataCast(struct { fired: *bool, fd: *posix.socket_t });
            ctx.fired.* = true;
            switch (comp.result) {
                .socket => |fd| ctx.fd.* = fd,
                .err => |err| return err,
                else => unreachable,
            }
        }
    };

    var ctx_data = struct { fired: *bool, fd: *posix.socket_t }{ .fired = &fired, .fd = &socket_fd };

    _ = try loop.socket(posix.AF.INET, posix.SOCK.STREAM, 0, .{
        .ptr = &ctx_data,
        .cb = Cb.cb,
    });

    try loop.run(.until_done);
    try std.testing.expect(fired);
    posix.close(socket_fd);
}

test "kqueue: connect and accept" {
    const allocator = std.testing.allocator;
    var loop = try Loop.init(allocator);
    defer loop.deinit();

    // Setup listener
    const listener = try posix.socket(posix.AF.INET, posix.SOCK.STREAM, 0);
    defer posix.close(listener);

    var addr = posix.sockaddr.in{
        .family = posix.AF.INET,
        .port = 0, // ephemeral
        .addr = 0, // loopback (0.0.0.0)
    };
    try posix.bind(listener, @ptrCast(&addr), @sizeOf(posix.sockaddr.in));
    try posix.listen(listener, 1);

    var len: posix.socklen_t = @sizeOf(posix.sockaddr.in);
    try posix.getsockname(listener, @ptrCast(&addr), &len);

    // Client socket
    const client_fd = try posix.socket(posix.AF.INET, posix.SOCK.STREAM, 0);
    defer posix.close(client_fd);

    var connected = false;
    var accepted = false;
    var server_client_fd: posix.socket_t = undefined;

    const Cb = struct {
        fn onConnect(_: *root.Loop, comp: root.Completion) !void {
            const flag = comp.userdataCast(bool);
            switch (comp.result) {
                .connect => flag.* = true,
                .err => |err| return err,
                else => unreachable,
            }
        }

        fn onAccept(_: *root.Loop, comp: root.Completion) !void {
            const ctx = comp.userdataCast(struct { flag: *bool, fd: *posix.socket_t });
            switch (comp.result) {
                .accept => |fd| {
                    ctx.flag.* = true;
                    ctx.fd.* = fd;
                },
                .err => |err| return err,
                else => unreachable,
            }
        }
    };

    var accept_ctx = struct { flag: *bool, fd: *posix.socket_t }{ .flag = &accepted, .fd = &server_client_fd };
    _ = try loop.accept(listener, .{
        .ptr = &accept_ctx,
        .cb = Cb.onAccept,
    });

    _ = try loop.connect(client_fd, @ptrCast(&addr), @sizeOf(posix.sockaddr.in), .{
        .ptr = &connected,
        .cb = Cb.onConnect,
    });

    try loop.run(.until_done);

    try std.testing.expect(connected);
    try std.testing.expect(accepted);
    posix.close(server_client_fd);
}

test "kqueue: send and recv" {
    const allocator = std.testing.allocator;
    var loop = try Loop.init(allocator);
    defer loop.deinit();

    var fds: [2]posix.socket_t = undefined;
    {
        const listener = try posix.socket(posix.AF.INET, posix.SOCK.STREAM, 0);
        defer posix.close(listener);
        var addr = posix.sockaddr.in{ .family = posix.AF.INET, .port = 0, .addr = 0 };
        try posix.bind(listener, @ptrCast(&addr), @sizeOf(posix.sockaddr.in));
        try posix.listen(listener, 1);
        var len: posix.socklen_t = @sizeOf(posix.sockaddr.in);
        try posix.getsockname(listener, @ptrCast(&addr), &len);

        fds[0] = try posix.socket(posix.AF.INET, posix.SOCK.STREAM, 0);
        try posix.connect(fds[0], @ptrCast(&addr), len);
        fds[1] = try posix.accept(listener, null, null, 0);
    }
    defer {
        posix.close(fds[0]);
        posix.close(fds[1]);
    }

    const msg = "hello world";
    var recv_buf: [128]u8 = undefined;
    var sent = false;
    var received = false;
    var bytes_recv: usize = 0;

    const Cb = struct {
        fn onSend(_: *root.Loop, comp: root.Completion) !void {
            const flag = comp.userdataCast(bool);
            switch (comp.result) {
                .send => |n| {
                    if (n == msg.len) flag.* = true;
                },
                .err => |err| return err,
                else => unreachable,
            }
        }
        fn onRecv(_: *root.Loop, comp: root.Completion) !void {
            const ctx = comp.userdataCast(struct { flag: *bool, bytes: *usize });
            switch (comp.result) {
                .recv => |n| {
                    ctx.flag.* = true;
                    ctx.bytes.* = n;
                },
                .err => |err| return err,
                else => unreachable,
            }
        }
    };

    _ = try loop.send(fds[0], msg, .{ .ptr = &sent, .cb = Cb.onSend });
    var recv_ctx = struct { flag: *bool, bytes: *usize }{ .flag = &received, .bytes = &bytes_recv };
    _ = try loop.recv(fds[1], &recv_buf, .{ .ptr = &recv_ctx, .cb = Cb.onRecv });

    try loop.run(.until_done);
    try std.testing.expect(sent);
    try std.testing.expect(received);
    try std.testing.expectEqualStrings(msg, recv_buf[0..bytes_recv]);
}

test "kqueue: close" {
    const allocator = std.testing.allocator;
    var loop = try Loop.init(allocator);
    defer loop.deinit();

    const fd = try posix.socket(posix.AF.INET, posix.SOCK.STREAM, 0);

    var closed = false;
    const Cb = struct {
        fn cb(_: *root.Loop, comp: root.Completion) !void {
            const f = comp.userdataCast(bool);
            switch (comp.result) {
                .close => f.* = true,
                .err => |err| return err,
                else => unreachable,
            }
        }
    };

    _ = try loop.close(fd, .{ .ptr = &closed, .cb = Cb.cb });
    try loop.run(.until_done);
    try std.testing.expect(closed);
}

test "kqueue: read" {
    const allocator = std.testing.allocator;
    var loop = try Loop.init(allocator);
    defer loop.deinit();

    var fds: [2]posix.socket_t = undefined;
    {
        const listener = try posix.socket(posix.AF.INET, posix.SOCK.STREAM, 0);
        defer posix.close(listener);
        var addr = posix.sockaddr.in{ .family = posix.AF.INET, .port = 0, .addr = 0 };
        try posix.bind(listener, @ptrCast(&addr), @sizeOf(posix.sockaddr.in));
        try posix.listen(listener, 1);
        var len: posix.socklen_t = @sizeOf(posix.sockaddr.in);
        try posix.getsockname(listener, @ptrCast(&addr), &len);

        fds[0] = try posix.socket(posix.AF.INET, posix.SOCK.STREAM, 0);
        try posix.connect(fds[0], @ptrCast(&addr), len);
        fds[1] = try posix.accept(listener, null, null, 0);
    }
    defer {
        posix.close(fds[0]);
        posix.close(fds[1]);
    }

    const msg = "read me";
    var read_buf: [128]u8 = undefined;
    var sent = false;
    var received = false;
    var bytes_read: usize = 0;

    const Cb = struct {
        fn onSend(_: *root.Loop, comp: root.Completion) !void {
            const flag = comp.userdataCast(bool);
            switch (comp.result) {
                .send => |n| {
                    if (n == msg.len) flag.* = true;
                },
                .err => |err| return err,
                else => unreachable,
            }
        }
        fn onRead(_: *root.Loop, comp: root.Completion) !void {
            const ctx = comp.userdataCast(struct { flag: *bool, bytes: *usize });
            switch (comp.result) {
                .read => |n| {
                    ctx.flag.* = true;
                    ctx.bytes.* = n;
                },
                .err => |err| return err,
                else => unreachable,
            }
        }
    };

    _ = try loop.send(fds[0], msg, .{ .ptr = &sent, .cb = Cb.onSend });
    var read_ctx = struct { flag: *bool, bytes: *usize }{ .flag = &received, .bytes = &bytes_read };
    _ = try loop.read(fds[1], &read_buf, .{ .ptr = &read_ctx, .cb = Cb.onRead });

    try loop.run(.until_done);
    try std.testing.expect(sent);
    try std.testing.expect(received);
    try std.testing.expectEqualStrings(msg, read_buf[0..bytes_read]);
}

test "kqueue: cancelByFd" {
    const allocator = std.testing.allocator;
    var loop = try Loop.init(allocator);
    defer loop.deinit();

    var fds: [2]posix.socket_t = undefined;
    {
        const listener = try posix.socket(posix.AF.INET, posix.SOCK.STREAM, 0);
        defer posix.close(listener);
        var addr = posix.sockaddr.in{ .family = posix.AF.INET, .port = 0, .addr = 0 };
        try posix.bind(listener, @ptrCast(&addr), @sizeOf(posix.sockaddr.in));
        try posix.listen(listener, 1);
        var len: posix.socklen_t = @sizeOf(posix.sockaddr.in);
        try posix.getsockname(listener, @ptrCast(&addr), &len);

        fds[0] = try posix.socket(posix.AF.INET, posix.SOCK.STREAM, 0);
        try posix.connect(fds[0], @ptrCast(&addr), len);
        fds[1] = try posix.accept(listener, null, null, 0);
    }
    defer {
        posix.close(fds[0]);
        posix.close(fds[1]);
    }

    var fired = false;
    const Cb = struct {
        fn cb(_: *root.Loop, comp: root.Completion) !void {
            const f = comp.userdataCast(bool);
            f.* = true;
        }
    };

    var buf: [1]u8 = undefined;
    // Start a read
    _ = try loop.read(fds[0], &buf, .{ .ptr = &fired, .cb = Cb.cb });

    // Cancel all ops for this FD
    loop.cancelByFd(fds[0]);

    // Timer to ensure we don't hang
    var timer_fired = false;
    _ = try loop.timeout(10 * std.time.ns_per_ms, .{ .ptr = &timer_fired, .cb = Cb.cb });

    try loop.run(.until_done);

    try std.testing.expect(!fired);
    try std.testing.expect(timer_fired);
}

test "kqueue: cancel" {
    const allocator = std.testing.allocator;
    var loop = try Loop.init(allocator);
    defer loop.deinit();

    var fds: [2]posix.socket_t = undefined;
    {
        const listener = try posix.socket(posix.AF.INET, posix.SOCK.STREAM, 0);
        defer posix.close(listener);
        var addr = posix.sockaddr.in{ .family = posix.AF.INET, .port = 0, .addr = 0 };
        try posix.bind(listener, @ptrCast(&addr), @sizeOf(posix.sockaddr.in));
        try posix.listen(listener, 1);
        var len: posix.socklen_t = @sizeOf(posix.sockaddr.in);
        try posix.getsockname(listener, @ptrCast(&addr), &len);

        fds[0] = try posix.socket(posix.AF.INET, posix.SOCK.STREAM, 0);
        try posix.connect(fds[0], @ptrCast(&addr), len);
        fds[1] = try posix.accept(listener, null, null, 0);
    }
    defer {
        posix.close(fds[0]);
        posix.close(fds[1]);
    }

    var fired = false;
    const Cb = struct {
        fn cb(_: *root.Loop, comp: root.Completion) !void {
            const f = comp.userdataCast(bool);
            f.* = true;
        }
    };

    var buf: [1]u8 = undefined;
    // Start a read that won't complete because we don't write
    const task = try loop.read(fds[0], &buf, .{ .ptr = &fired, .cb = Cb.cb });

    // Cancel it
    try loop.cancel(task.id);

    // Run loop - should return immediately as no pending ops (if cancel worked)
    // But to be sure it doesn't block, let's schedule a timer too.
    var timer_fired = false;
    _ = try loop.timeout(10 * std.time.ns_per_ms, .{ .ptr = &timer_fired, .cb = Cb.cb });

    try loop.run(.until_done);

    try std.testing.expect(!fired);
    try std.testing.expect(timer_fired);
}
