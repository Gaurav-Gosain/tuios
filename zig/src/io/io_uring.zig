//! io_uring-based I/O backend for Linux.

const std = @import("std");

const root = @import("../io.zig");

const linux = std.os.linux;
const posix = std.posix;

const log = std.log.scoped(.io_uring);

const RING_ENTRIES: u13 = 256;
const CQE_BATCH_SIZE: usize = 32;

fn noopSignalHandler(_: c_int) callconv(.c) void {}

pub const Loop = struct {
    allocator: std.mem.Allocator,
    ring: linux.IoUring,
    next_id: usize = 1,
    pending: std.AutoHashMap(usize, PendingOp),

    const PendingOp = struct {
        ctx: root.Context,
        kind: OpKind,
        buf: []u8 = &.{},
        timespec: linux.kernel_timespec = undefined,
        fd: posix.socket_t = undefined,
        pid: posix.pid_t = undefined,
        pidfd: posix.fd_t = undefined,
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
        const ring = try linux.IoUring.init(RING_ENTRIES, 0);
        return .{
            .allocator = allocator,
            .ring = ring,
            .pending = std.AutoHashMap(usize, PendingOp).init(allocator),
        };
    }

    pub fn deinit(self: *Loop) void {
        self.pending.deinit();
        self.ring.deinit();
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

        try self.pending.put(id, .{
            .ctx = ctx,
            .kind = .socket,
        });

        _ = try self.ring.socket(id, @intCast(domain), socket_type, protocol, 0);

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

        try self.pending.put(id, .{
            .ctx = ctx,
            .kind = .connect,
            .fd = fd,
        });

        _ = try self.ring.connect(id, @intCast(fd), addr, addr_len);

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

        _ = try self.ring.accept(id, @intCast(fd), null, null, 0);

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn read(
        self: *Loop,
        fd: posix.socket_t,
        buf: []u8,
        ctx: root.Context,
    ) !root.Task {
        const id = self.next_id;
        self.next_id += 1;

        try self.pending.put(id, .{
            .ctx = ctx,
            .kind = .read,
            .buf = buf,
            .fd = fd,
        });

        _ = try self.ring.read(id, @intCast(fd), .{ .buffer = buf }, 0);

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn recv(
        self: *Loop,
        fd: posix.socket_t,
        buf: []u8,
        ctx: root.Context,
    ) !root.Task {
        const id = self.next_id;
        self.next_id += 1;

        try self.pending.put(id, .{
            .ctx = ctx,
            .kind = .recv,
            .buf = buf,
            .fd = fd,
        });

        _ = try self.ring.recv(id, @intCast(fd), .{ .buffer = buf }, 0);

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn send(
        self: *Loop,
        fd: posix.socket_t,
        buf: []const u8,
        ctx: root.Context,
    ) !root.Task {
        const id = self.next_id;
        self.next_id += 1;

        try self.pending.put(id, .{
            .ctx = ctx,
            .kind = .send,
            .buf = @constCast(buf),
            .fd = fd,
        });

        _ = try self.ring.send(id, @intCast(fd), buf, 0);

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn close(
        self: *Loop,
        fd: posix.socket_t,
        ctx: root.Context,
    ) !root.Task {
        const id = self.next_id;
        self.next_id += 1;

        try self.pending.put(id, .{
            .ctx = ctx,
            .kind = .close,
            .fd = fd,
        });

        _ = try self.ring.close(id, @intCast(fd));

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn timeout(
        self: *Loop,
        nanoseconds: u64,
        ctx: root.Context,
    ) !root.Task {
        const id = self.next_id;
        self.next_id += 1;

        const ts: linux.kernel_timespec = .{
            .sec = @intCast(nanoseconds / std.time.ns_per_s),
            .nsec = @intCast(nanoseconds % std.time.ns_per_s),
        };

        try self.pending.put(id, .{
            .ctx = ctx,
            .kind = .timer,
            .timespec = ts,
        });

        // Get pointer to the timespec stored in the pending op
        const op_ptr = self.pending.getPtr(id).?;

        const sqe = try self.ring.get_sqe();
        sqe.prep_timeout(&op_ptr.timespec, 0, 0);
        sqe.user_data = id;

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn waitpid(
        self: *Loop,
        pid: posix.pid_t,
        ctx: root.Context,
    ) !root.Task {
        const id = self.next_id;
        self.next_id += 1;

        // Open a pidfd for the process
        const rc = linux.pidfd_open(pid, 0);
        const pidfd: posix.fd_t = if (linux.E.init(rc) != .SUCCESS)
            return posix.unexpectedErrno(linux.E.init(rc))
        else
            @intCast(rc);

        try self.pending.put(id, .{
            .ctx = ctx,
            .kind = .waitpid,
            .pid = pid,
            .pidfd = pidfd,
        });

        // Poll the pidfd for readability (signals process exit)
        _ = try self.ring.poll_add(id, pidfd, linux.POLL.IN);

        return root.Task{ .id = id, .ctx = ctx };
    }

    pub fn cancel(self: *Loop, id: usize) !void {
        if (!self.pending.contains(id)) {
            return;
        }

        // Submit cancel operation
        const sqe = try self.ring.get_sqe();
        sqe.prep_cancel(@intCast(id), 0);
    }

    pub fn cancelByFd(self: *Loop, fd: posix.socket_t) void {
        var ids_to_cancel: std.ArrayList(usize) = .{};
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
        var cqes: [CQE_BATCH_SIZE]linux.io_uring_cqe = undefined;

        while (true) {
            if (mode == .until_done and self.pending.count() == 0) break;

            _ = try self.ring.submit();

            const wait_nr: u32 = if (mode == .once) 0 else 1;
            const n = self.ring.copy_cqes(&cqes, wait_nr) catch |err| switch (err) {
                error.SignalInterrupt => continue,
                else => return err,
            };

            if (n == 0 and mode == .once) break;

            for (cqes[0..n]) |cqe| {
                const id: usize = @intCast(cqe.user_data);
                const op = self.pending.get(id) orelse continue;
                _ = self.pending.remove(id);

                try self.handleCompletion(op, cqe);
            }

            if (mode == .once) break;
        }
    }

    fn handleCompletion(self: *Loop, op: PendingOp, cqe: linux.io_uring_cqe) !void {
        const ctx = op.ctx;

        if (cqe.err() != .SUCCESS) {
            if (op.kind == .timer and cqe.err() == .TIME) {
                // Timer expired - treat as success
            } else {
                const err = mapCqeError(cqe.err());
                try self.invokeCallback(ctx, .{ .err = err });
                return;
            }
        }

        const result: root.Result = switch (op.kind) {
            .socket => .{ .socket = @intCast(cqe.res) },
            .connect => .{ .connect = {} },
            .accept => .{ .accept = @intCast(cqe.res) },
            .read => .{ .read = @intCast(cqe.res) },
            .recv => .{ .recv = @intCast(cqe.res) },
            .send => .{ .send = @intCast(cqe.res) },
            .close => .{ .close = {} },
            .timer => .{ .timer = {} },
            .waitpid => blk: {
                // Close the pidfd now that the process has exited
                posix.close(op.pidfd);
                // Reap the process to get exit status
                const wait_result = posix.waitpid(op.pid, posix.W.NOHANG);
                break :blk .{ .waitpid = .{
                    .pid = wait_result.pid,
                    .status = wait_result.status,
                } };
            },
        };

        try self.invokeCallback(ctx, result);
    }

    fn mapCqeError(err: std.posix.E) anyerror {
        return switch (err) {
            .CONNREFUSED => error.ConnectionRefused,
            .INPROGRESS, .AGAIN => error.WouldBlock,
            .CANCELED => error.Canceled,
            else => error.IOError,
        };
    }

    fn invokeCallback(self: *Loop, ctx: root.Context, result: root.Result) !void {
        try ctx.cb(@ptrCast(self), .{
            .userdata = ctx.ptr,
            .msg = ctx.msg,
            .callback = ctx.cb,
            .result = result,
        });
    }

    pub const RunMode = enum {
        until_done,
        once,
        forever,
    };
};

test "io_uring loop - init" {
    const testing = std.testing;
    var loop = Loop.init(testing.allocator) catch |err| {
        // If io_uring is not supported or not available, we skip the test
        // In a real CI environment with Linux, this should preferably fail if it's expected to work.
        // But for local dev on mixed systems, this safety is good.
        // However, std.os.linux.IoUring.init might throw various errors.
        log.warn("Failed to init io_uring: {}", .{err});
        return;
    };
    defer loop.deinit();
}

test "io_uring loop - timeout" {
    const testing = std.testing;
    var loop = Loop.init(testing.allocator) catch return;
    defer loop.deinit();

    var completed = false;
    const State = struct {
        completed: *bool,
    };
    var state: State = .{
        .completed = &completed,
    };

    const callback = struct {
        fn cb(l: *root.Loop, completion: root.Completion) !void {
            _ = l;
            const s = completion.userdataCast(State);
            switch (completion.result) {
                .timer => s.completed.* = true,
                .err => |err| return err,
                else => unreachable,
            }
        }
    }.cb;

    _ = try loop.timeout(10 * std.time.ns_per_ms, .{
        .ptr = &state,
        .cb = callback,
    });

    try loop.run(.until_done);
    try testing.expect(completed);
}

test "io_uring loop - socket/connect/accept/recv/send/close" {
    const testing = std.testing;
    var loop = Loop.init(testing.allocator) catch return;
    defer loop.deinit();

    // Setup a listening socket (server)
    const listen_fd = try posix.socket(posix.AF.INET, posix.SOCK.STREAM | posix.SOCK.CLOEXEC, 0);
    defer posix.close(listen_fd);

    var addr = try std.net.Address.parseIp4("127.0.0.1", 0);
    try posix.bind(listen_fd, &addr.any, addr.getOsSockLen());
    try posix.listen(listen_fd, 1);

    // Get the assigned port
    var len = addr.getOsSockLen();
    try posix.getsockname(listen_fd, &addr.any, &len);

    const State = struct {
        client_fd: posix.socket_t = undefined,
        accepted_fd: posix.socket_t = undefined,
        sent_bytes: usize = 0,
        received_bytes: usize = 0,
        received_data: [64]u8 = undefined,
        connect_done: bool = false,
        accept_done: bool = false,
        send_done: bool = false,
        recv_done: bool = false,
        close_client_done: bool = false,
        close_accepted_done: bool = false,
    };
    var state: State = .{};

    const Context = struct {
        state: *State,
        addr: *std.net.Address,
    };
    var ctx_struct: Context = .{ .state = &state, .addr = &addr };

    const Handlers = struct {
        fn close_accepted_cb(_: *root.Loop, completion: root.Completion) !void {
            const s = completion.userdataCast(State);
            if (completion.result == .close) s.close_accepted_done = true;
        }

        fn recv_cb(l: *root.Loop, completion: root.Completion) !void {
            const real_loop: *Loop = @ptrCast(l);
            const s = completion.userdataCast(State);
            switch (completion.result) {
                .recv => |n| {
                    s.received_bytes = n;
                    s.recv_done = true;
                    // Close accepted
                    _ = try real_loop.close(s.accepted_fd, .{
                        .ptr = s,
                        .cb = close_accepted_cb,
                    });
                },
                .err => |err| return err,
                else => unreachable,
            }
        }

        fn accept_cb(l: *root.Loop, completion: root.Completion) !void {
            const real_loop: *Loop = @ptrCast(l);
            const s = completion.userdataCast(State);
            switch (completion.result) {
                .accept => |fd| {
                    s.accepted_fd = fd;
                    s.accept_done = true;
                    // Start recv
                    _ = try real_loop.recv(fd, &s.received_data, .{
                        .ptr = s,
                        .cb = recv_cb,
                    });
                },
                .err => |err| return err,
                else => unreachable,
            }
        }

        fn close_client_cb(_: *root.Loop, completion: root.Completion) !void {
            const s = completion.userdataCast(State);
            if (completion.result == .close) s.close_client_done = true;
        }

        fn send_cb(l: *root.Loop, completion: root.Completion) !void {
            const real_loop: *Loop = @ptrCast(l);
            const s = completion.userdataCast(State);
            switch (completion.result) {
                .send => |n| {
                    s.sent_bytes = n;
                    s.send_done = true;
                    _ = try real_loop.close(s.client_fd, .{
                        .ptr = s,
                        .cb = close_client_cb,
                    });
                },
                .err => |err| return err,
                else => unreachable,
            }
        }

        fn connect_cb(l: *root.Loop, completion: root.Completion) !void {
            const real_loop: *Loop = @ptrCast(l);
            const s = completion.userdataCast(State);
            switch (completion.result) {
                .connect => {
                    s.connect_done = true;
                    const msg = "Hello";
                    _ = try real_loop.send(s.client_fd, msg, .{
                        .ptr = s,
                        .cb = send_cb,
                    });
                },
                .err => |err| return err,
                else => unreachable,
            }
        }

        fn socket_cb(l: *root.Loop, completion: root.Completion) !void {
            const real_loop: *Loop = @ptrCast(l);
            const ctx = completion.userdataCast(Context);
            const s = ctx.state;
            switch (completion.result) {
                .socket => |fd| {
                    s.client_fd = fd;
                    _ = try real_loop.connect(fd, &ctx.addr.any, ctx.addr.getOsSockLen(), .{
                        .ptr = s,
                        .cb = connect_cb,
                    });
                },
                .err => |err| return err,
                else => unreachable,
            }
        }
    };

    _ = try loop.accept(listen_fd, .{ .ptr = &state, .cb = Handlers.accept_cb });

    _ = try loop.socket(posix.AF.INET, posix.SOCK.STREAM, 0, .{
        .ptr = &ctx_struct,
        .cb = Handlers.socket_cb,
    });

    try loop.run(.until_done);

    try testing.expect(state.accept_done);
    try testing.expect(state.connect_done);
    try testing.expect(state.send_done);
    try testing.expect(state.recv_done);
    try testing.expect(state.close_client_done);
    try testing.expect(state.close_accepted_done);
    try testing.expectEqual(@as(usize, 5), state.sent_bytes);
    try testing.expectEqual(@as(usize, 5), state.received_bytes);
    try testing.expectEqualStrings("Hello", state.received_data[0..5]);
}

test "io_uring loop - cancel" {
    const testing = std.testing;
    var loop = Loop.init(testing.allocator) catch return;
    defer loop.deinit();

    // Create a socket pair or pipe
    const fds = try posix.pipe2(.{ .CLOEXEC = true, .NONBLOCK = true });
    defer {
        posix.close(fds[0]);
        posix.close(fds[1]);
    }

    var canceled = false;
    const State = struct {
        canceled: *bool,
    };
    var state: State = .{ .canceled = &canceled };

    const cb = struct {
        fn cb(l: *root.Loop, completion: root.Completion) !void {
            _ = l;
            const s = completion.userdataCast(State);
            switch (completion.result) {
                .err => |err| {
                    if (err == error.Canceled) s.canceled.* = true;
                },
                else => {},
            }
        }
    }.cb;

    var buf: [1]u8 = undefined;
    // Read from pipe[0], nothing written so it should block
    const task = try loop.read(fds[0], &buf, .{ .ptr = &state, .cb = cb });

    try loop.cancel(task.id);

    try loop.run(.until_done);
    try testing.expect(canceled);
}

test "io_uring loop - cancelByFd" {
    const testing = std.testing;
    var loop = Loop.init(testing.allocator) catch return;
    defer loop.deinit();

    const fds = try posix.pipe2(.{ .CLOEXEC = true, .NONBLOCK = true });
    defer {
        posix.close(fds[0]);
        posix.close(fds[1]);
    }

    var canceled = false;
    const State = struct {
        canceled: *bool,
    };
    var state: State = .{ .canceled = &canceled };

    const cb = struct {
        fn cb(l: *root.Loop, completion: root.Completion) !void {
            _ = l;
            const s = completion.userdataCast(State);
            switch (completion.result) {
                .err => |err| {
                    if (err == error.Canceled) s.canceled.* = true;
                },
                else => {},
            }
        }
    }.cb;

    var buf: [1]u8 = undefined;
    _ = try loop.read(fds[0], &buf, .{ .ptr = &state, .cb = cb });

    loop.cancelByFd(fds[0]);

    try loop.run(.until_done);
    try testing.expect(canceled);
}

test "io_uring loop - ignores SignalInterrupt" {
    const testing = std.testing;
    var loop = Loop.init(testing.allocator) catch return;
    defer loop.deinit();

    var old_action: posix.Sigaction = undefined;
    const action: posix.Sigaction = .{
        .handler = .{ .handler = noopSignalHandler },
        .mask = posix.sigemptyset(),
        .flags = 0,
    };
    posix.sigaction(posix.SIG.USR1, &action, &old_action);
    defer posix.sigaction(posix.SIG.USR1, &old_action, null);

    const target_pid = linux.getpid();
    const target_tid = linux.gettid();

    var stop = std.atomic.Value(bool).init(false);
    const SignalState = struct {
        stop: *std.atomic.Value(bool),
        pid: linux.pid_t,
        tid: linux.pid_t,
    };
    var signal_state: SignalState = .{
        .stop = &stop,
        .pid = target_pid,
        .tid = target_tid,
    };

    const signal_thread = try std.Thread.spawn(.{}, struct {
        fn run(state: *const SignalState) void {
            while (!state.stop.load(.acquire)) {
                _ = linux.tgkill(state.pid, state.tid, @intCast(posix.SIG.USR1));
                std.Thread.sleep(1 * std.time.ns_per_ms);
            }
        }
    }.run, .{&signal_state});
    defer {
        stop.store(true, .release);
        signal_thread.join();
    }

    var fired = false;
    const cb = struct {
        fn cb(_: *root.Loop, completion: root.Completion) !void {
            const flag = completion.userdataCast(bool);
            switch (completion.result) {
                .timer => flag.* = true,
                else => {},
            }
        }
    }.cb;

    _ = try loop.timeout(50 * std.time.ns_per_ms, .{ .ptr = &fired, .cb = cb });

    try loop.run(.until_done);
    try testing.expect(fired);
}
