//! Server that manages PTYs and client connections.

const std = @import("std");
const builtin = @import("builtin");

const ghostty_vt = @import("ghostty-vt");

const io = @import("io.zig");
const key_encode = @import("key_encode.zig");
const key_parse = @import("key_parse.zig");
const main = @import("main.zig");
const mouse_encode = @import("mouse_encode.zig");
const msgpack = @import("msgpack.zig");
const pty = @import("pty.zig");
const redraw = @import("redraw.zig");
const rpc = @import("rpc.zig");
const vt_handler = @import("vt_handler.zig");

const csig = @cImport({
    @cInclude("signal.h");
    @cInclude("unistd.h");
});

const posix = std.posix;

const log = std.log.scoped(.server);

/// Resource limits to prevent unbounded growth in the long-running daemon.
pub const LIMITS = struct {
    pub const CLIENTS_MAX: usize = 64;
    pub const PTYS_MAX: usize = 256;
    pub const MESSAGE_SIZE_MAX: usize = 16 * 1024 * 1024; // 16MB
    pub const SEND_QUEUE_MAX: usize = 1024;
    pub const TITLE_LEN_MAX: usize = 4096;
    pub const CWD_LEN_MAX: usize = 4096; // typical PATH_MAX
    pub const COLOR_QUERY_MAX: usize = 32;
    pub const RESPONSE_QUEUE_MAX: usize = 64;
    pub const COLOR_QUERY_TIMEOUT_MS: i64 = 5000;
};

var signal_write_fd: posix.fd_t = undefined;

fn signalHandler(sig: c_int) callconv(std.builtin.CallingConvention.c) void {
    // Ignore further signals
    const ignore: posix.Sigaction = .{
        .handler = .{ .handler = posix.SIG.IGN },
        .mask = posix.sigemptyset(),
        .flags = 0,
    };
    posix.sigaction(posix.SIG.INT, &ignore, null);
    posix.sigaction(posix.SIG.TERM, &ignore, null);

    _ = sig;
    _ = posix.write(signal_write_fd, "s") catch {};
}

/// Writes all data to the file descriptor, looping on partial writes.
/// Handles WouldBlock by retrying with a short sleep.
fn writeAllFd(fd: posix.fd_t, data: []const u8) !void {
    var index: usize = 0;
    while (index < data.len) {
        const n = posix.write(fd, data[index..]) catch |err| {
            if (err == error.WouldBlock) {
                std.Thread.sleep(1 * std.time.ns_per_ms);
                continue;
            }
            return err;
        };
        index += n;
    }
}

const Pty = struct {
    // Type declarations must come before fields
    const ColorQuery = struct {
        target: vt_handler.ColorTarget,
        timestamp_ms: i64,
        response_slot: usize, // Index into response_queue for this query's response
    };

    const QueuedResponse = struct {
        const Tag = enum { pending_color, ready };
        tag: Tag,
        data: [64]u8 = undefined,
        len: usize = 0,

        fn ready(response: []const u8) QueuedResponse {
            var r: QueuedResponse = .{ .tag = .ready, .len = @min(response.len, 64) };
            @memcpy(r.data[0..r.len], response[0..r.len]);
            return r;
        }

        fn pendingColor() QueuedResponse {
            return .{ .tag = .pending_color };
        }
    };

    id: usize,
    process: pty.Process,
    clients: std.ArrayList(*Client),
    read_thread: ?std.Thread = null,
    running: std.atomic.Value(bool),
    terminal: ghostty_vt.Terminal,
    allocator: std.mem.Allocator,

    // Title of the terminal window
    title: std.ArrayList(u8),
    title_dirty: bool = false,

    // Current working directory (from OSC 7)
    cwd: std.ArrayList(u8),
    cwd_dirty: bool = false,

    // Pending color query requests from PTY applications
    color_queries_buf: [LIMITS.COLOR_QUERY_MAX]ColorQuery = undefined,
    color_queries_len: usize = 0,
    color_queries_mutex: std.Thread.Mutex = .{},

    // Response queue for ordered response delivery (ring buffer with absolute slots)
    response_queue: [LIMITS.RESPONSE_QUEUE_MAX]QueuedResponse = undefined,
    response_queue_head: usize = 0, // Next slot to flush (absolute, wraps)
    response_queue_tail: usize = 0, // Next slot to allocate (absolute, wraps)
    response_queue_start: i64 = 0, // Timestamp when first item was queued

    // Track color queries sent vs responses received
    color_queries_sent: usize = 0,
    color_queries_received: usize = 0,

    // Pending DA1 response - held until color queries are resolved or timeout
    da1_pending: bool = false,
    da1_timestamp_ms: i64 = 0,

    // Exit state
    exited: std.atomic.Value(bool) = std.atomic.Value(bool).init(false),
    exit_status: std.atomic.Value(u32) = std.atomic.Value(u32).init(0),

    // Synchronization for terminal access
    terminal_mutex: std.Thread.Mutex = .{},

    // Dirty signaling
    pipe_fds: [2]posix.fd_t,
    exit_pipe_fds: [2]posix.fd_t,
    dirty_signal_buf: [1]u8 = undefined,
    last_render_time: i64 = 0,
    render_timer: ?io.Task = null,
    render_state: ghostty_vt.RenderState,

    // Selection state: stores click position for drag selection as a Pin
    // to survive viewport scrolling during drag operations
    selection_start: ?ghostty_vt.PageList.Pin = null,
    // Click counting for double/triple click
    left_click_count: u8 = 0,
    left_click_time: i64 = 0, // milliseconds timestamp

    // Pointer to server for callbacks (opaque to avoid circular type dependency)
    server_ptr: *anyopaque = undefined,

    fn init(allocator: std.mem.Allocator, id: usize, process_instance: pty.Process, size: pty.Winsize) !*Pty {
        // Precondition: terminal size must be positive (zero would crash ghostty-vt)
        std.debug.assert(size.ws_col > 0);
        std.debug.assert(size.ws_row > 0);
        // Precondition: process must have valid master fd
        std.debug.assert(process_instance.master >= 0);

        const instance = try allocator.create(Pty);
        errdefer allocator.destroy(instance);

        const pipe_fds = try posix.pipe2(.{ .NONBLOCK = true, .CLOEXEC = true });
        errdefer {
            posix.close(pipe_fds[0]);
            posix.close(pipe_fds[1]);
        }

        const exit_pipe_fds = try posix.pipe2(.{ .NONBLOCK = true, .CLOEXEC = true });
        errdefer {
            posix.close(exit_pipe_fds[0]);
            posix.close(exit_pipe_fds[1]);
        }

        instance.* = .{
            .id = id,
            .process = process_instance,
            .clients = std.ArrayList(*Client).empty,
            .running = std.atomic.Value(bool).init(true),
            .terminal = try ghostty_vt.Terminal.init(allocator, .{
                .cols = size.ws_col,
                .rows = size.ws_row,
            }),
            .allocator = allocator,
            .title = std.ArrayList(u8).empty,
            .title_dirty = false,
            .cwd = std.ArrayList(u8).empty,
            .cwd_dirty = false,
            .pipe_fds = pipe_fds,
            .exit_pipe_fds = exit_pipe_fds,
            .render_state = .empty,
        };

        // Postcondition: instance initialized in running state with no clients
        std.debug.assert(instance.running.load(.seq_cst) == true);
        std.debug.assert(instance.clients.items.len == 0);

        return instance;
    }

    /// Cancel pending IO operations.
    fn cancelPendingIO(self: *Pty, loop: *io.Loop) void {
        // Cancel any pending render timer
        if (self.render_timer) |*task| {
            task.cancel(loop) catch {};
            self.render_timer = null;
        }

        // Cancel pending read on dirty signal pipe
        loop.cancelByFd(self.pipe_fds[0]);
    }

    /// Join read thread and free resources (call after event loop exits)
    fn joinAndFree(self: *Pty, allocator: std.mem.Allocator) void {
        // Precondition: all clients must be detached before freeing
        std.debug.assert(self.clients.items.len == 0);
        // Precondition: running flag must be false (stopAndCancelIO was called)
        std.debug.assert(!self.running.load(.seq_cst));

        if (self.read_thread) |thread| {
            thread.join();
        }

        // If the process is still running (thread was killed before it could reap),
        // ensure we terminate it with SIGKILL before cleanup
        if (!self.exited.load(.acquire)) {
            _ = posix.kill(self.process.pid, posix.SIG.KILL) catch {};
            // Give it a moment to die
            std.Thread.sleep(10 * std.time.ns_per_ms);
        }

        self.process.close();

        posix.close(self.pipe_fds[0]);
        posix.close(self.pipe_fds[1]);
        posix.close(self.exit_pipe_fds[0]);
        posix.close(self.exit_pipe_fds[1]);
        self.terminal.deinit(allocator);
        self.render_state.deinit(allocator);
        self.clients.deinit(allocator);
        self.title.deinit(allocator);
        self.cwd.deinit(allocator);
        allocator.destroy(self);
    }

    fn deinit(self: *Pty, allocator: std.mem.Allocator, loop: *io.Loop) void {
        // Precondition: all clients must be detached before deinit
        std.debug.assert(self.clients.items.len == 0);

        self.stopAndCancelIO(loop);
        self.joinAndFree(allocator);
    }

    fn addClient(self: *Pty, allocator: std.mem.Allocator, client: *Client) !void {
        // Precondition: PTY must be running to accept new clients
        std.debug.assert(self.running.load(.seq_cst));
        // Precondition: client must not already be attached (no duplicates)
        for (self.clients.items) |c| {
            std.debug.assert(c != client);
        }
        // Precondition: must not exceed client limit
        std.debug.assert(self.clients.items.len < LIMITS.CLIENTS_MAX);

        const prev_len = self.clients.items.len;
        try self.clients.append(allocator, client);

        // Postcondition: client count increased by exactly one
        std.debug.assert(self.clients.items.len == prev_len + 1);
    }

    fn removeClient(self: *Pty, client: *Client) void {
        for (self.clients.items, 0..) |c, i| {
            if (c == client) {
                _ = self.clients.swapRemove(i);
                return;
            }
        }
        // Client not found - already removed (e.g., via detach_pty)
    }

    fn setTitle(self: *Pty, title: []const u8) !void {
        // Mutex is already held by readThread when this is called via callback

        // Truncate title to prevent unbounded growth
        const truncated = if (title.len > LIMITS.TITLE_LEN_MAX) title[0..LIMITS.TITLE_LEN_MAX] else title;

        // Update internal title
        self.title.clearRetainingCapacity();
        try self.title.appendSlice(self.allocator, truncated);
        self.title_dirty = true;
    }

    fn setCwd(self: *Pty, cwd: []const u8) !void {
        // Mutex is already held by readThread when this is called via callback
        const truncated = if (cwd.len > LIMITS.CWD_LEN_MAX) cwd[0..LIMITS.CWD_LEN_MAX] else cwd;

        self.cwd.clearRetainingCapacity();
        try self.cwd.appendSlice(self.allocator, truncated);
        self.cwd_dirty = true;
    }

    fn queueColorQuery(self: *Pty, target: vt_handler.ColorTarget) void {
        self.color_queries_mutex.lock();
        defer self.color_queries_mutex.unlock();

        const now_ms = std.time.milliTimestamp();

        // Check response queue capacity first
        const queue_len = self.response_queue_tail - self.response_queue_head;
        if (queue_len >= LIMITS.RESPONSE_QUEUE_MAX) {
            log.warn("Response queue full, dropping color query", .{});
            return;
        }

        if (self.color_queries_len >= LIMITS.COLOR_QUERY_MAX) {
            log.warn("Color query queue full, dropping query", .{});
            return;
        }

        // Allocate a slot in the response queue for this color query (absolute slot)
        const slot = self.response_queue_tail;
        self.response_queue[slot % LIMITS.RESPONSE_QUEUE_MAX] = QueuedResponse.pendingColor();
        self.response_queue_tail += 1;
        if (queue_len == 0) {
            self.response_queue_start = now_ms;
        }

        self.color_queries_buf[self.color_queries_len] = .{
            .target = target,
            .timestamp_ms = now_ms,
            .response_slot = slot,
        };
        self.color_queries_len += 1;

        // Signal dirty to trigger sending color queries to client
        _ = posix.write(self.pipe_fds[1], "c") catch {};
    }

    /// Queue a DA1 response to be sent after all other responses are flushed.
    fn queueDa1(self: *Pty) void {
        self.color_queries_mutex.lock();
        defer self.color_queries_mutex.unlock();

        self.da1_pending = true;
        self.da1_timestamp_ms = std.time.milliTimestamp();

        // Try to send immediately if queue is empty, otherwise signal dirty
        _ = self.flushResponsesUnlocked();
        _ = posix.write(self.pipe_fds[1], "d") catch {};
    }

    /// Queue a response to be sent to the PTY in order.
    fn queueResponse(self: *Pty, response: []const u8) void {
        self.color_queries_mutex.lock();
        defer self.color_queries_mutex.unlock();

        const queue_len = self.response_queue_tail - self.response_queue_head;
        if (queue_len >= LIMITS.RESPONSE_QUEUE_MAX) {
            log.warn("Response queue full, dropping response", .{});
            return;
        }

        self.response_queue[self.response_queue_tail % LIMITS.RESPONSE_QUEUE_MAX] = QueuedResponse.ready(response);
        self.response_queue_tail += 1;
        if (queue_len == 0) {
            self.response_queue_start = std.time.milliTimestamp();
        }
    }

    /// Fill in a color query response at its reserved slot.
    fn fillColorResponse(self: *Pty, slot: usize, response: []const u8) void {
        self.color_queries_mutex.lock();
        defer self.color_queries_mutex.unlock();

        // Slot must be within the current queue range (head <= slot < tail)
        if (slot >= self.response_queue_head and slot < self.response_queue_tail) {
            self.response_queue[slot % LIMITS.RESPONSE_QUEUE_MAX] = QueuedResponse.ready(response);
        }
    }

    /// Try to flush ready responses from the front of the queue.
    /// Returns true if there are still pending responses.
    fn flushResponses(self: *Pty) bool {
        self.color_queries_mutex.lock();
        defer self.color_queries_mutex.unlock();
        return self.flushResponsesUnlocked();
    }

    /// Flush responses without taking the lock (caller must hold color_queries_mutex).
    fn flushResponsesUnlocked(self: *Pty) bool {
        const now_ms = std.time.milliTimestamp();
        const queue_len = self.response_queue_tail - self.response_queue_head;
        const timed_out = queue_len > 0 and
            (now_ms - self.response_queue_start > LIMITS.COLOR_QUERY_TIMEOUT_MS);

        // Flush consecutive ready responses from the head
        while (self.response_queue_head < self.response_queue_tail) {
            const resp = &self.response_queue[self.response_queue_head % LIMITS.RESPONSE_QUEUE_MAX];
            if (resp.tag == .pending_color) {
                if (timed_out) {
                    // Skip timed-out pending responses
                    self.response_queue_head += 1;
                    continue;
                }
                break; // Wait for this response
            }
            // Write ready response to PTY
            writeAllFd(self.process.master, resp.data[0..resp.len]) catch |err| {
                log.err("Failed to write response to PTY: {}", .{err});
            };
            self.response_queue_head += 1;
        }

        // Update start time if queue still has items
        const new_queue_len = self.response_queue_tail - self.response_queue_head;
        if (new_queue_len > 0 and new_queue_len < queue_len) {
            self.response_queue_start = now_ms;
        }

        // Send DA1 if queue is empty and DA1 is pending
        if (self.da1_pending and new_queue_len == 0) {
            self.da1_pending = false;
            writeAllFd(self.process.master, "\x1b[?1;2c") catch |err| {
                log.err("Failed to write DA1 response to PTY: {}", .{err});
            };
        }

        return new_queue_len > 0;
    }

    // Removed broadcast - we'll send msgpack-RPC redraw notifications instead

    fn readThread(self: *Pty, server: *Server) void {
        _ = server;
        // Precondition: PTY must be in running state when thread starts
        std.debug.assert(self.running.load(.seq_cst));
        // Precondition: process master fd must be valid
        std.debug.assert(self.process.master >= 0);

        // 4096 bytes matches typical pipe buffer size and is large enough to
        // batch multiple VT sequences per read, reducing syscall overhead while
        // staying small enough for stack allocation.
        var buffer: [4096]u8 = undefined;

        var handler = vt_handler.Handler.init(&self.terminal);
        defer handler.deinit();

        // Set up the write callback so the handler can respond to queries.
        // Responses are queued to ensure proper ordering with async color responses.
        handler.setWriteCallback(self, struct {
            fn writeToPty(ctx: ?*anyopaque, data: []const u8) !void {
                const pty_inst: *Pty = @ptrCast(@alignCast(ctx));
                pty_inst.queueResponse(data);
                _ = pty_inst.flushResponses();
            }
        }.writeToPty);

        // Set up title callback
        handler.setTitleCallback(self, struct {
            fn onTitle(ctx: ?*anyopaque, title: []const u8) !void {
                const pty_inst: *Pty = @ptrCast(@alignCast(ctx));
                pty_inst.setTitle(title) catch |err| {
                    log.err("Failed to set title: {}", .{err});
                };
            }
        }.onTitle);

        // Set up cwd callback (OSC 7)
        handler.setCwdCallback(self, struct {
            fn onCwd(ctx: ?*anyopaque, cwd: []const u8) !void {
                const pty_inst: *Pty = @ptrCast(@alignCast(ctx));
                pty_inst.setCwd(cwd) catch |err| {
                    log.err("Failed to set cwd: {}", .{err});
                };
            }
        }.onCwd);

        // Set up color query callback (OSC 4/10/11/12)
        handler.setColorQueryCallback(self, struct {
            fn onColorQuery(ctx: ?*anyopaque, target: vt_handler.ColorTarget) !void {
                const pty_inst: *Pty = @ptrCast(@alignCast(ctx));
                pty_inst.queueColorQuery(target);
            }
        }.onColorQuery);

        // Set up DA1 callback - defer response until color queries are resolved
        handler.setDa1Callback(self, struct {
            fn onDa1(ctx: ?*anyopaque) !void {
                const pty_inst: *Pty = @ptrCast(@alignCast(ctx));
                pty_inst.queueDa1();
            }
        }.onDa1);

        var stream = vt_handler.Stream.initAlloc(self.allocator, handler);
        defer stream.deinit();

        var poll_fds = [_]posix.pollfd{
            .{ .fd = self.process.master, .events = posix.POLL.IN, .revents = 0 },
            .{ .fd = self.exit_pipe_fds[0], .events = posix.POLL.IN, .revents = 0 },
        };

        while (self.running.load(.seq_cst)) {
            // Tight loop: drain PTY buffer
            while (true) {
                const n = posix.read(self.process.master, &buffer) catch |err| {
                    if (err == error.WouldBlock) break; // Buffer empty, time to poll
                    log.err("PTY read error: {}", .{err});
                    self.running.store(false, .seq_cst);
                    break;
                };
                if (n == 0) {
                    log.info("PTY {} master returned EOF", .{self.id});
                    self.running.store(false, .seq_cst);
                    break;
                }

                // Lock mutex and update terminal state
                self.terminal_mutex.lock();
                // Parse the data through ghostty-vt to update terminal state
                stream.nextSlice(buffer[0..n]);
                // Check synchronized_output while still holding the mutex
                const should_signal = !self.terminal.modes.get(.synchronized_output);
                self.terminal_mutex.unlock();

                // Notify main thread by writing to pipe
                // Ignore EAGAIN (pipe full means already dirty)
                // Skip signaling during synchronized_output mode (DEC mode 2026) because
                // the application is in the middle of an atomic update. We'll render when
                // the mode is cleared, avoiding partial/flickering frames.
                if (should_signal) {
                    _ = posix.write(self.pipe_fds[1], "x") catch |err| {
                        if (err != error.WouldBlock) {
                            log.err("Failed to signal dirty: {}", .{err});
                        }
                    };
                }
            }

            if (!self.running.load(.seq_cst)) break;

            // Poll for more data or exit signal
            _ = posix.poll(&poll_fds, -1) catch |err| {
                log.err("Poll error: {}", .{err});
                break;
            };

            if (poll_fds[1].revents & posix.POLL.IN != 0) {
                break;
            }

            // Check for POLLHUP on master (process closed its side)
            if (poll_fds[0].revents & posix.POLL.HUP != 0) {
                self.running.store(false, .seq_cst);
                break;
            }
        }
        log.info("PTY read thread exiting for PTY {}", .{self.id});

        // Ghostty-style kill loop: repeatedly signal and poll until process exits
        const status = self.killAndReap();
        self.exit_status.store(status, .seq_cst);
        self.exited.store(true, .seq_cst);

        // Signal main thread that process has exited (reuse dirty pipe)
        _ = posix.write(self.pipe_fds[1], "e") catch {};
    }

    /// Kill the process group and reap the child. Returns exit status.
    /// Closes master fd first (triggers kernel SIGHUP), then escalates signals.
    fn killAndReap(self: *Pty) u32 {
        const pid = self.process.pid;

        // Close master fd first - this triggers kernel SIGHUP to the process group
        // when the slave side detects the hangup condition
        posix.close(self.process.master);
        self.process.master = -1;

        // Get the process group ID, waiting for setsid if needed
        const pgid = getpgid(pid) orelse {
            // Process doesn't exist, try to reap anyway
            const res = posix.waitpid(pid, posix.W.NOHANG);
            return res.status;
        };

        // Wait a bit for kernel-triggered SIGHUP to take effect
        for (0..10) |_| {
            const res = posix.waitpid(pid, posix.W.NOHANG);
            if (res.pid != 0) {
                log.info("PTY {} process exited with status {}", .{ self.id, res.status });
                return res.status;
            }
            std.Thread.sleep(10 * std.time.ns_per_ms);
        }

        // Signal escalation: SIGHUP -> SIGTERM -> SIGKILL
        const signals = [_]c_int{ csig.SIGHUP, csig.SIGTERM, csig.SIGKILL };
        const iterations_per_signal: usize = 10; // 10 * 10ms = 100ms per signal

        for (signals) |sig| {
            _ = csig.killpg(pgid, sig);

            for (0..iterations_per_signal) |_| {
                const res = posix.waitpid(pid, posix.W.NOHANG);
                if (res.pid != 0) {
                    log.info("PTY {} process exited with status {}", .{ self.id, res.status });
                    return res.status;
                }
                std.Thread.sleep(10 * std.time.ns_per_ms);
            }
        }

        // SIGKILL should always work, but keep trying
        log.warn("PTY {} process still alive after SIGKILL, polling", .{self.id});
        while (true) {
            const res = posix.waitpid(pid, posix.W.NOHANG);
            if (res.pid != 0) {
                log.info("PTY {} process exited with status {}", .{ self.id, res.status });
                return res.status;
            }
            std.Thread.sleep(10 * std.time.ns_per_ms);
        }
    }
};

/// Get the process group ID for a pid, waiting for setsid if needed.
/// Returns null if the process doesn't exist.
fn getpgid(pid: posix.pid_t) ?posix.pid_t {
    // Get our own process group ID
    const my_pgid = csig.getpgid(0);

    // Loop while pgid == my_pgid (setsid not yet called by child)
    while (true) {
        const pgid = csig.getpgid(pid);

        // If still in parent's group, setsid() hasn't completed yet
        if (pgid == my_pgid) {
            std.Thread.sleep(10 * std.time.ns_per_ms);
            continue;
        }

        // Invalid or error cases
        if (pgid == 0) return null;
        if (pgid < 0) return null;

        return pgid;
    }
}

/// Map ghostty MouseShape to redraw MouseShape
fn mapMouseShape(shape: ghostty_vt.MouseShape) redraw.UIEvent.MouseShape.Shape {
    return switch (shape) {
        .default => .default,
        .text => .text,
        .pointer => .pointer,
        .help => .help,
        .progress => .progress,
        .wait => .wait,
        .cell => .cell,
        .crosshair => .crosshair,
        .move => .move,
        .not_allowed => .not_allowed,
        .no_drop => .not_allowed,
        .grab => .grab,
        .grabbing => .grabbing,
        .ew_resize, .e_resize, .w_resize => .ew_resize,
        .ns_resize, .n_resize, .s_resize => .ns_resize,
        .nesw_resize, .ne_resize, .sw_resize => .nesw_resize,
        .nwse_resize, .nw_resize, .se_resize => .nwse_resize,
        .col_resize => .col_resize,
        .row_resize => .row_resize,
        .all_scroll => .all_scroll,
        .zoom_in => .zoom_in,
        .zoom_out => .zoom_out,
        .context_menu, .alias, .copy, .vertical_text => .default,
    };
}

/// Convert ghostty style to Prise Style Attributes
fn getStyleAttributes(style: ghostty_vt.Style) redraw.UIEvent.Style.Attributes {
    var attrs: redraw.UIEvent.Style.Attributes = .{};

    // Convert foreground color
    switch (style.fg_color) {
        .none => {},
        .palette => |idx| {
            attrs.fg_idx = @intCast(idx);
        },
        .rgb => |rgb| {
            // Convert RGB struct to u32: 0xRRGGBB
            attrs.fg = (@as(u32, rgb.r) << 16) | (@as(u32, rgb.g) << 8) | @as(u32, rgb.b);
        },
    }

    // Convert background color
    switch (style.bg_color) {
        .none => {},
        .palette => |idx| {
            attrs.bg_idx = @intCast(idx);
        },
        .rgb => |rgb| {
            // Convert RGB struct to u32: 0xRRGGBB
            attrs.bg = (@as(u32, rgb.r) << 16) | (@as(u32, rgb.g) << 8) | @as(u32, rgb.b);
        },
    }

    // Convert underline color
    switch (style.underline_color) {
        .none => {},
        .palette => |idx| {
            _ = idx;
        },
        .rgb => |rgb| {
            attrs.ul_color = (@as(u32, rgb.r) << 16) | (@as(u32, rgb.g) << 8) | @as(u32, rgb.b);
        },
    }

    // Convert flags
    attrs.bold = style.flags.bold;
    attrs.dim = style.flags.faint;
    attrs.italic = style.flags.italic;
    attrs.reverse = style.flags.inverse;
    attrs.blink = style.flags.blink;
    attrs.strikethrough = style.flags.strikethrough;

    // Handle underline variants
    attrs.ul_style = switch (style.flags.underline) {
        .none => .none,
        .single => .single,
        .double => .double,
        .curly => .curly,
        .dotted => .dotted,
        .dashed => .dashed,
    };

    return attrs;
}

/// Captured screen state for building redraw notifications
pub const RenderMode = enum { full, incremental };

/// State passed between redraw helper functions
const RedrawContext = struct {
    builder: *redraw.RedrawBuilder,
    temp_alloc: std.mem.Allocator,
    pty_id: usize,
    rows: usize,
    cols: usize,
    default_style: ghostty_vt.Style,
    styles_map: std.AutoHashMap(u64, u32),
    next_style_id: u32,
    last_style: ?ghostty_vt.Style,
    last_style_id: u32,
};

fn emitTitle(builder: *redraw.RedrawBuilder, pty_instance: *Pty, mode: RenderMode) !void {
    if (mode == .full or pty_instance.title_dirty) {
        try builder.title(@intCast(pty_instance.id), pty_instance.title.items);
        pty_instance.title_dirty = false;
    }
}

fn emitResize(builder: *redraw.RedrawBuilder, pty_id: usize, rows: usize, cols: usize) !void {
    try builder.resize(@intCast(pty_id), @intCast(rows), @intCast(cols));
}

fn initStylesContext(temp_alloc: std.mem.Allocator, builder: *redraw.RedrawBuilder, pty_id: usize, rows: usize, cols: usize) !RedrawContext {
    var styles_map = std.AutoHashMap(u64, u32).init(temp_alloc);
    const default_style: ghostty_vt.Style = .{
        .fg_color = .none,
        .bg_color = .none,
        .underline_color = .none,
        .flags = .{},
    };
    const default_hash = std.hash.Wyhash.hash(0, std.mem.asBytes(&default_style));
    try styles_map.put(default_hash, 0);
    try builder.style(0, .{});

    return .{
        .builder = builder,
        .temp_alloc = temp_alloc,
        .pty_id = pty_id,
        .rows = rows,
        .cols = cols,
        .default_style = default_style,
        .styles_map = styles_map,
        .next_style_id = 1,
        .last_style = null,
        .last_style_id = 0,
    };
}

fn resolveStyle(ctx: *RedrawContext, vt_style: ghostty_vt.Style) !u32 {
    if (ctx.last_style) |last| {
        if (std.meta.eql(last, vt_style)) {
            return ctx.last_style_id;
        }
    }

    const style_hash = std.hash.Wyhash.hash(0, std.mem.asBytes(&vt_style));
    if (ctx.styles_map.get(style_hash)) |id| {
        ctx.last_style = vt_style;
        ctx.last_style_id = id;
        return id;
    }

    const style_id = ctx.next_style_id;
    ctx.next_style_id += 1;
    try ctx.styles_map.put(style_hash, style_id);
    const attrs = getStyleAttributes(vt_style);
    try ctx.builder.style(style_id, attrs);
    ctx.last_style = vt_style;
    ctx.last_style_id = style_id;
    return style_id;
}

const CellSlices = struct {
    raw: []const ghostty_vt.page.Cell,
    style: []const ghostty_vt.Style,
    grapheme: []const []const u21,
};

fn encodeGraphemeToUtf8(temp_alloc: std.mem.Allocator, cluster: []const u21) ![]const u8 {
    if (cluster.len == 0) return try temp_alloc.dupe(u8, " ");

    var utf8_buf: [4]u8 = undefined;
    var stack_buf: [64]u8 = undefined;
    var stack_len: usize = 0;
    for (cluster) |cp| {
        if (stack_len + 4 > stack_buf.len) break;
        const len = std.unicode.utf8Encode(cp, &utf8_buf) catch continue;
        @memcpy(stack_buf[stack_len..][0..len], utf8_buf[0..len]);
        stack_len += len;
    }
    return try temp_alloc.dupe(u8, stack_buf[0..stack_len]);
}

fn resolveCellText(
    ctx: *RedrawContext,
    raw_cell: ghostty_vt.page.Cell,
    grapheme_slice: []const u21,
    is_direct_color: bool,
) ![]const u8 {
    if (is_direct_color) return " ";

    var one_grapheme_buf: [1]u21 = undefined;
    const cluster: []const u21 = switch (raw_cell.content_tag) {
        .codepoint => blk: {
            if (raw_cell.content.codepoint != 0) {
                one_grapheme_buf[0] = raw_cell.content.codepoint;
                break :blk &one_grapheme_buf;
            }
            break :blk &[_]u21{};
        },
        .codepoint_grapheme => blk: {
            const base_cp = raw_cell.content.codepoint;
            const full_cluster = try ctx.temp_alloc.alloc(u21, 1 + grapheme_slice.len);
            full_cluster[0] = base_cp;
            @memcpy(full_cluster[1..], grapheme_slice);
            break :blk full_cluster;
        },
        else => &[_]u21{' '},
    };
    return try encodeGraphemeToUtf8(ctx.temp_alloc, cluster);
}

fn detectRepeat(
    raw_cell: ghostty_vt.page.Cell,
    slices: CellSlices,
    x: usize,
    cols: usize,
    is_direct_color: bool,
) usize {
    var repeat: usize = 1;
    var next_x = x + 1;
    if (raw_cell.wide == .wide) next_x += 1;

    while (next_x < cols) {
        const next_raw = slices.raw[next_x];
        if (raw_cell.wide != next_raw.wide) break;
        if (!cellsMatch(raw_cell, next_raw, slices.grapheme, x, next_x, is_direct_color)) break;

        repeat += 1;
        next_x += 1;
        if (next_raw.wide == .wide) next_x += 1;
    }
    return repeat;
}

fn cellsMatch(
    raw_cell: ghostty_vt.page.Cell,
    next_raw: ghostty_vt.page.Cell,
    grapheme_slice: []const []const u21,
    x: usize,
    next_x: usize,
    is_direct_color: bool,
) bool {
    if (next_raw.content_tag != raw_cell.content_tag or next_raw.style_id != raw_cell.style_id) return false;

    if (is_direct_color) {
        if (raw_cell.content_tag == .bg_color_rgb) {
            return std.meta.eql(raw_cell.content.color_rgb, next_raw.content.color_rgb);
        } else if (raw_cell.content_tag == .bg_color_palette) {
            return raw_cell.content.color_palette == next_raw.content.color_palette;
        }
        return false;
    }

    if (raw_cell.content_tag == .codepoint) {
        return next_raw.content.codepoint == raw_cell.content.codepoint;
    } else if (raw_cell.content_tag == .codepoint_grapheme) {
        return std.mem.eql(u21, grapheme_slice[x], grapheme_slice[next_x]);
    }
    return true;
}

fn emitRow(ctx: *RedrawContext, y: usize, slices: CellSlices) !void {
    var cells_buf = std.ArrayList(redraw.UIEvent.Write.Cell).empty;
    var last_hl_id: u32 = 0;
    var x: usize = 0;

    while (x < ctx.cols) {
        const raw_cell = slices.raw[x];
        if (raw_cell.wide == .spacer_tail) {
            x += 1;
            continue;
        }

        var vt_style = if (raw_cell.style_id > 0) slices.style[x] else ctx.default_style;
        var is_direct_color = false;

        if (raw_cell.content_tag == .bg_color_rgb) {
            const cell_rgb = raw_cell.content.color_rgb;
            vt_style.bg_color = .{ .rgb = .{ .r = cell_rgb.r, .g = cell_rgb.g, .b = cell_rgb.b } };
            is_direct_color = true;
        } else if (raw_cell.content_tag == .bg_color_palette) {
            vt_style.bg_color = .{ .palette = raw_cell.content.color_palette };
            is_direct_color = true;
        }

        const style_id = try resolveStyle(ctx, vt_style);
        const text = try resolveCellText(ctx, raw_cell, slices.grapheme[x], is_direct_color);
        const repeat = detectRepeat(raw_cell, slices, x, ctx.cols, is_direct_color);

        const hl_id_to_send: ?u32 = if (style_id != last_hl_id) style_id else null;
        if (hl_id_to_send) |id| last_hl_id = id;

        try cells_buf.append(ctx.temp_alloc, .{
            .grapheme = text,
            .style_id = hl_id_to_send,
            .repeat = if (repeat > 1) @intCast(repeat) else null,
            .width = if (raw_cell.wide == .wide) 2 else null,
        });

        x += repeat;
        if (raw_cell.wide == .wide) x += repeat;
    }

    if (cells_buf.items.len > 0) {
        try ctx.builder.write(@intCast(ctx.pty_id), @intCast(y), 0, cells_buf.items);
    }
}

fn emitRows(ctx: *RedrawContext, rs: *ghostty_vt.RenderState, effective_mode: RenderMode) !void {
    const row_data_slice = rs.row_data.slice();
    const row_cells = row_data_slice.items(.cells);
    const row_dirties = row_data_slice.items(.dirty);

    for (0..ctx.rows) |y| {
        if (effective_mode == .incremental and !row_dirties[y]) continue;
        row_dirties[y] = false;

        const rs_cells = row_cells[y];
        const rs_cells_slice = rs_cells.slice();
        const slices: CellSlices = .{
            .raw = rs_cells_slice.items(.raw),
            .style = rs_cells_slice.items(.style),
            .grapheme = rs_cells_slice.items(.grapheme),
        };
        try emitRow(ctx, y, slices);
    }
}

fn emitCursor(builder: *redraw.RedrawBuilder, pty_id: usize, rs: *const ghostty_vt.RenderState) !void {
    const cursor_visible = rs.cursor.visible and rs.cursor.viewport != null;
    if (rs.cursor.viewport) |vp| {
        try builder.cursorPos(@intCast(pty_id), @intCast(vp.y), @intCast(vp.x), cursor_visible);
    } else {
        try builder.cursorPos(@intCast(pty_id), @intCast(rs.cursor.active.y), @intCast(rs.cursor.active.x), cursor_visible);
    }
    const shape: redraw.UIEvent.CursorShape.Shape = switch (rs.cursor.visual_style) {
        .block, .block_hollow => .block,
        .bar => .beam,
        .underline => .underline,
    };
    try builder.cursorShape(@intCast(pty_id), shape);
}

fn emitSelection(builder: *redraw.RedrawBuilder, pty_id: usize, rs: *const ghostty_vt.RenderState) !void {
    const row_selections = rs.row_data.slice().items(.selection);
    var sel_start_row: ?u16 = null;
    var sel_start_col: ?u16 = null;
    var sel_end_row: ?u16 = null;
    var sel_end_col: ?u16 = null;

    for (row_selections, 0..) |sel_range, y| {
        if (sel_range) |range| {
            if (sel_start_row == null) {
                sel_start_row = @intCast(y);
                sel_start_col = @intCast(range[0]);
            }
            sel_end_row = @intCast(y);
            sel_end_col = @intCast(range[1]);
        }
    }
    try builder.selection(@intCast(pty_id), sel_start_row, sel_start_col, sel_end_row, sel_end_col);
}

/// Build redraw message directly from PTY render state
fn buildRedrawMessageFromPty(
    allocator: std.mem.Allocator,
    pty_instance: *Pty,
    mode: RenderMode,
) ![]u8 {
    var builder = redraw.RedrawBuilder.init(allocator);
    defer builder.deinit();

    var temp_arena = std.heap.ArenaAllocator.init(allocator);
    defer temp_arena.deinit();
    const temp_alloc = temp_arena.allocator();

    const mouse_shape = mouse_shape: {
        pty_instance.terminal_mutex.lock();
        defer pty_instance.terminal_mutex.unlock();

        // Skip rendering during synchronized output mode - the application is in the
        // middle of an atomic update and the terminal state may be inconsistent
        if (pty_instance.terminal.modes.get(.synchronized_output)) {
            return error.SynchronizedOutput;
        }

        pty_instance.render_state.update(pty_instance.allocator, &pty_instance.terminal) catch |err| {
            // If update fails, reset render state to recover from potentially corrupt state
            log.warn("render_state.update failed: {}, resetting render state", .{err});
            pty_instance.render_state.deinit(pty_instance.allocator);
            pty_instance.render_state = .empty;
            return err;
        };

        // Emit title while mutex is held (title.items is modified by read thread)
        try emitTitle(&builder, pty_instance, mode);

        break :mouse_shape mapMouseShape(pty_instance.terminal.mouse_shape);
    };

    const rs = &pty_instance.render_state;

    var effective_mode = mode;
    if (rs.dirty == .full) effective_mode = .full;
    rs.dirty = .false;

    const rows = rs.rows;
    const cols = rs.cols;

    if (effective_mode == .full) {
        try emitResize(&builder, pty_instance.id, rows, cols);
    }

    var ctx = try initStylesContext(temp_alloc, &builder, pty_instance.id, rows, cols);
    try emitRows(&ctx, rs, effective_mode);
    try emitCursor(&builder, pty_instance.id, rs);
    try builder.mouseShape(@intCast(pty_instance.id), mouse_shape);
    try emitSelection(&builder, pty_instance.id, rs);

    try builder.flush();
    return builder.build();
}

const Client = struct {
    fd: posix.fd_t,
    server: *Server,
    // 4096 bytes is sufficient for typical RPC messages while staying
    // small enough for stack allocation. Larger messages are handled
    // via msg_buffer accumulation.
    recv_buffer: [4096]u8 = undefined,
    msg_buffer: std.ArrayList(u8),
    send_buffer: ?[]u8 = null,
    send_offset: usize = 0,
    send_queue: std.ArrayList([]u8),
    attached_ptys: std.ArrayList(usize),
    closing: bool = false,
    macos_option_as_alt: key_encode.OptionAsAlt = .false,
    // Map style ID to its last known definition hash/attributes to detect changes
    // We store the Attributes struct directly.
    // style_cache: std.AutoHashMap(u16, redraw.UIEvent.Style.Attributes),

    fn sendData(self: *Client, loop: *io.Loop, data: []const u8) !void {
        if (self.closing) return;

        std.debug.assert(data.len > 0);
        std.debug.assert(data.len <= LIMITS.MESSAGE_SIZE_MAX);
        std.debug.assert(self.fd >= 0);

        const buf = try self.server.allocator.dupe(u8, data);
        errdefer self.server.allocator.free(buf);

        // If there's a pending send, queue this one
        if (self.send_buffer != null) {
            if (self.send_queue.items.len >= LIMITS.SEND_QUEUE_MAX) {
                self.server.allocator.free(buf);
                return error.SendQueueFull;
            }
            try self.send_queue.append(self.server.allocator, buf);
            return;
        }

        // Otherwise send immediately
        self.send_buffer = buf;
        _ = try loop.send(self.fd, buf, .{
            .ptr = self,
            .cb = onSendComplete,
        });
    }

    fn onSendComplete(loop: *io.Loop, completion: io.Completion) anyerror!void {
        const client = completion.userdataCast(Client);
        const allocator = client.server.allocator;

        switch (completion.result) {
            .send => |n| {
                const buf = client.send_buffer orelse return;
                client.send_offset += n;

                // Check for partial send
                if (client.send_offset < buf.len) {
                    // Re-arm for remaining bytes
                    _ = try loop.send(client.fd, buf[client.send_offset..], .{
                        .ptr = client,
                        .cb = onSendComplete,
                    });
                    return;
                }

                // Buffer fully sent, free it
                allocator.free(buf);
                client.send_buffer = null;
                client.send_offset = 0;

                // If client is closing, finish cleanup
                if (client.closing) {
                    client.finishClose(loop);
                    return;
                }

                // Send next queued message if any
                if (client.send_queue.items.len > 0) {
                    const next_buf = client.send_queue.orderedRemove(0);
                    client.send_buffer = next_buf;
                    _ = try loop.send(client.fd, next_buf, .{
                        .ptr = client,
                        .cb = onSendComplete,
                    });
                }
            },
            .err => |err| {
                if (err != error.BrokenPipe) {
                    log.err("Send failed: {}", .{err});
                }
                // Free current buffer
                if (client.send_buffer) |buf| {
                    allocator.free(buf);
                    client.send_buffer = null;
                    client.send_offset = 0;
                }
                // Clear queue on error
                for (client.send_queue.items) |buf| {
                    allocator.free(buf);
                }
                client.send_queue.clearRetainingCapacity();

                if (client.closing) {
                    client.finishClose(loop);
                }
            },
            else => unreachable,
        }
    }

    fn finishClose(self: *Client, loop: *io.Loop) void {
        const server = self.server;
        const allocator = server.allocator;

        // Free any remaining queued sends
        for (self.send_queue.items) |buf| {
            allocator.free(buf);
        }
        self.send_queue.deinit(allocator);
        self.msg_buffer.deinit(allocator);
        self.attached_ptys.deinit(allocator);

        // Remove from server's client list
        for (server.clients.items, 0..) |c, i| {
            if (c == self) {
                _ = server.clients.swapRemove(i);
                break;
            }
        }

        _ = loop.close(self.fd, .{
            .ptr = null,
            .cb = struct {
                fn noop(_: *io.Loop, _: io.Completion) anyerror!void {}
            }.noop,
        }) catch {};

        allocator.destroy(self);
        std.log.debug("Total clients: {}", .{server.clients.items.len});
        server.checkExit() catch {};
    }

    fn processMessage(self: *Client, loop: *io.Loop, msg: rpc.Message) !void {
        std.debug.assert(!self.closing);
        std.debug.assert(self.fd >= 0);

        switch (msg) {
            .request => |req| {
                try self.handleRpcRequest(loop, req);
            },
            .notification => |notif| {
                try self.handleNotification(notif);
            },
            .response => {
                // std.log.warn("Client sent response, ignoring", .{});
            },
        }
    }

    /// Handle RPC request, send response with result.
    fn handleRpcRequest(self: *Client, loop: *io.Loop, req: rpc.Request) !void {
        std.debug.assert(req.method.len > 0);

        const result = self.server.handleRequest(self, req.method, req.params) catch |err| {
            return self.sendErrorResponse(loop, req.msgid, err);
        };
        defer result.deinit(self.server.allocator);

        const response_arr = try self.server.allocator.alloc(msgpack.Value, 4);
        defer self.server.allocator.free(response_arr);
        response_arr[0] = msgpack.Value{ .unsigned = 1 }; // type
        response_arr[1] = msgpack.Value{ .unsigned = req.msgid }; // msgid
        response_arr[2] = msgpack.Value.nil; // no error
        response_arr[3] = result; // result

        const response_value = msgpack.Value{ .array = response_arr };
        const response_bytes = try msgpack.encodeFromValue(self.server.allocator, response_value);
        defer self.server.allocator.free(response_bytes);

        std.debug.assert(response_bytes.len <= LIMITS.MESSAGE_SIZE_MAX);
        try self.sendData(loop, response_bytes);
    }

    fn sendErrorResponse(self: *Client, loop: *io.Loop, msgid: u32, err: anyerror) !void {
        const response_arr = try self.server.allocator.alloc(msgpack.Value, 4);
        defer self.server.allocator.free(response_arr);
        response_arr[0] = msgpack.Value{ .unsigned = 1 }; // type
        response_arr[1] = msgpack.Value{ .unsigned = msgid }; // msgid
        response_arr[2] = msgpack.Value{ .string = @errorName(err) }; // error
        response_arr[3] = msgpack.Value.nil; // no result

        const response_value = msgpack.Value{ .array = response_arr };
        const response_bytes = try msgpack.encodeFromValue(self.server.allocator, response_value);
        defer self.server.allocator.free(response_bytes);

        std.debug.assert(response_bytes.len <= LIMITS.MESSAGE_SIZE_MAX);
        try self.sendData(loop, response_bytes);
    }

    /// Dispatch notification to appropriate handler.
    fn handleNotification(self: *Client, notif: rpc.Notification) !void {
        std.debug.assert(notif.method.len > 0);

        if (std.mem.eql(u8, notif.method, "write_pty")) {
            try self.handleWritePty(notif);
        } else if (std.mem.eql(u8, notif.method, "paste_input")) {
            try self.handlePasteInput(notif);
        } else if (std.mem.eql(u8, notif.method, "key_input") or
            std.mem.eql(u8, notif.method, "key_release"))
        {
            try self.handleKeyEvent(notif);
        } else if (std.mem.eql(u8, notif.method, "mouse_input")) {
            try self.handleMouseInput(notif);
        } else if (std.mem.eql(u8, notif.method, "resize_pty")) {
            try self.handleResizePty(notif);
        } else if (std.mem.eql(u8, notif.method, "detach_pty")) {
            try self.handleDetachPty(notif);
        } else if (std.mem.eql(u8, notif.method, "focus_event")) {
            try self.handleFocusEvent(notif);
        } else if (std.mem.eql(u8, notif.method, "color_response")) {
            try self.handleColorResponse(notif);
        }
    }

    /// Write binary data to PTY input.
    fn handleWritePty(self: *Client, notif: rpc.Notification) !void {
        if (notif.params != .array or notif.params.array.len < 2) {
            log.warn("write_pty notification: invalid params", .{});
            return;
        }

        const pty_id = parsePtyId(notif.params.array[0]) orelse {
            log.warn("write_pty notification: invalid pty_id type", .{});
            return;
        };

        const input_data = parseInputData(notif.params.array[1]) orelse {
            log.warn("write_pty notification: invalid data type", .{});
            return;
        };

        if (self.server.ptys.get(pty_id)) |pty_instance| {
            _ = posix.write(pty_instance.process.master, input_data) catch |err| {
                log.err("Write to PTY failed: {}", .{err});
            };
        } else {
            log.warn("write_pty notification: PTY {} not found", .{pty_id});
        }
    }

    /// Handle clipboard paste with optional bracketed paste mode.
    fn handlePasteInput(self: *Client, notif: rpc.Notification) !void {
        if (notif.params != .array or notif.params.array.len < 2) {
            log.warn("paste_input notification: invalid params", .{});
            return;
        }

        const pty_id = parsePtyId(notif.params.array[0]) orelse {
            log.warn("paste_input notification: invalid pty_id type", .{});
            return;
        };

        const paste_data = parseInputData(notif.params.array[1]) orelse {
            log.warn("paste_input notification: invalid data type", .{});
            return;
        };

        if (self.server.ptys.get(pty_id)) |pty_instance| {
            pty_instance.terminal_mutex.lock();
            const bracketed = pty_instance.terminal.modes.get(.bracketed_paste);
            pty_instance.terminal_mutex.unlock();

            if (bracketed) {
                writeAllFd(pty_instance.process.master, "\x1b[200~") catch |err| {
                    log.err("Write to PTY failed: {}", .{err});
                };
                writeAllFd(pty_instance.process.master, paste_data) catch |err| {
                    log.err("Write to PTY failed: {}", .{err});
                };
                writeAllFd(pty_instance.process.master, "\x1b[201~") catch |err| {
                    log.err("Write to PTY failed: {}", .{err});
                };
            } else {
                const mutable_data = self.server.allocator.dupe(u8, paste_data) catch |err| {
                    log.err("Failed to allocate paste buffer: {}", .{err});
                    return;
                };
                defer self.server.allocator.free(mutable_data);
                std.mem.replaceScalar(u8, mutable_data, '\n', '\r');
                writeAllFd(pty_instance.process.master, mutable_data) catch |err| {
                    log.err("Write to PTY failed: {}", .{err});
                };
            }
        } else {
            log.warn("paste_input notification: PTY {} not found", .{pty_id});
        }
    }

    /// Handle keyboard press/release events.
    fn handleKeyEvent(self: *Client, notif: rpc.Notification) !void {
        if (notif.params != .array or notif.params.array.len < 2) {
            log.warn("key_input notification: invalid params", .{});
            return;
        }

        const is_release = std.mem.eql(u8, notif.method, "key_release");
        const pty_id = parsePtyId(notif.params.array[0]) orelse {
            log.warn("key_input notification: invalid pty_id type", .{});
            return;
        };
        const key_map = notif.params.array[1];

        if (self.server.ptys.get(pty_id)) |pty_instance| {
            const action: ghostty_vt.input.KeyAction = if (is_release) .release else .press;
            const key = key_parse.parseKeyMapWithAction(key_map, action) catch |err| {
                log.err("Failed to parse key map: {}", .{err});
                return;
            };

            var encode_buf: [32]u8 = undefined;
            var writer = std.Io.Writer.fixed(&encode_buf);

            pty_instance.terminal_mutex.lock();
            key_encode.encode(&writer, key, &pty_instance.terminal, self.macos_option_as_alt) catch |err| {
                log.err("Failed to encode key: {}", .{err});
                pty_instance.terminal_mutex.unlock();
                return;
            };

            const encoded = writer.buffered();
            if (encoded.len > 0 and !key.key.modifier()) {
                pty_instance.terminal.scrollViewport(.bottom);
                pty_instance.terminal.screens.active.select(null) catch {};
            }
            pty_instance.terminal_mutex.unlock();

            if (encoded.len > 0 and !key.key.modifier()) {
                _ = posix.write(pty_instance.pipe_fds[1], "x") catch {};
            }

            if (encoded.len > 0) {
                _ = posix.write(pty_instance.process.master, encoded) catch |err| {
                    log.err("Write to PTY failed: {}", .{err});
                };
            }
        } else {
            log.warn("key_input notification: PTY {} not found", .{pty_id});
        }
    }

    /// Handle mouse input (click, drag, scroll, motion).
    fn handleMouseInput(self: *Client, notif: rpc.Notification) !void {
        if (notif.params != .array or notif.params.array.len < 2) {
            log.warn("mouse_input notification: invalid params", .{});
            return;
        }

        const pty_id = parsePtyId(notif.params.array[0]) orelse {
            log.warn("mouse_input notification: invalid pty_id type", .{});
            return;
        };
        const mouse_map = notif.params.array[1];

        if (self.server.ptys.get(pty_id)) |pty_instance| {
            const mouse = key_parse.parseMouseMap(mouse_map) catch |err| {
                log.err("Failed to parse mouse map: {}", .{err});
                return;
            };

            const is_wheel = switch (mouse.button) {
                .wheel_up, .wheel_down, .wheel_left, .wheel_right => true,
                else => false,
            };

            const State = struct {
                terminal: mouse_encode.TerminalState,
                active_screen: ghostty_vt.ScreenSet.Key,
            };
            const state: State = state: {
                pty_instance.terminal_mutex.lock();
                defer pty_instance.terminal_mutex.unlock();
                break :state .{
                    .terminal = mouse_encode.TerminalState.init(&pty_instance.terminal),
                    .active_screen = pty_instance.terminal.screens.active_key,
                };
            };

            if (is_wheel and state.terminal.flags.mouse_event == .none) {
                try self.handleMouseWheel(pty_instance, mouse, state.terminal, state.active_screen);
            } else if (mouse.button == .left and state.terminal.flags.mouse_event == .none) {
                try self.handleMouseSelection(pty_instance, mouse);
            } else {
                try self.handleMouseReport(pty_instance, mouse, state.terminal);
            }
        } else {
            log.warn("mouse_input notification: PTY {} not found", .{pty_id});
        }
    }

    /// Handle mouse wheel scrolling.
    fn handleMouseWheel(
        self: *Client,
        pty_instance: *Pty,
        mouse: key_parse.MouseEvent,
        terminal: mouse_encode.TerminalState,
        active_screen: ghostty_vt.ScreenSet.Key,
    ) !void {
        _ = self;
        if (active_screen == .alternate and terminal.modes.get(.mouse_alternate_scroll)) {
            const seq: []const u8 = if (terminal.modes.get(.cursor_keys))
                (if (mouse.button == .wheel_up) "\x1bOA" else "\x1bOB")
            else
                (if (mouse.button == .wheel_up) "\x1b[A" else "\x1b[B");
            _ = posix.write(pty_instance.process.master, seq) catch |err| {
                log.err("Write to PTY failed: {}", .{err});
            };
        } else {
            const delta: isize = switch (mouse.button) {
                .wheel_up => -1,
                .wheel_down => 1,
                else => 0,
            };
            if (delta != 0) {
                pty_instance.terminal_mutex.lock();
                pty_instance.terminal.scrollViewport(.{ .delta = delta });
                pty_instance.terminal_mutex.unlock();
                _ = posix.write(pty_instance.pipe_fds[1], "x") catch {};
            }
        }
    }

    /// Handle left-click selection: single, double, triple, and drag.
    fn handleMouseSelection(self: *Client, pty_instance: *Pty, mouse: key_parse.MouseEvent) !void {
        _ = self;
        const col: u16 = @intFromFloat(@max(0, @floor(mouse.x)));
        const row: u16 = @intFromFloat(@max(0, @floor(mouse.y)));
        const CLICK_INTERVAL_MS: i64 = 500;

        switch (mouse.type) {
            .press => {
                // Update click count based on timing
                const now = std.time.milliTimestamp();
                if (pty_instance.left_click_count > 0 and
                    (now - pty_instance.left_click_time) < CLICK_INTERVAL_MS)
                {
                    pty_instance.left_click_count += 1;
                    if (pty_instance.left_click_count > 3) {
                        pty_instance.left_click_count = 1;
                    }
                } else {
                    pty_instance.left_click_count = 1;
                }
                pty_instance.left_click_time = now;

                pty_instance.terminal_mutex.lock();
                defer pty_instance.terminal_mutex.unlock();

                const screen = pty_instance.terminal.screens.active;
                const terminal_cols = pty_instance.terminal.cols;
                const clamped_col: u16 = @min(col, terminal_cols -| 1);
                const clamped_row: u16 = @min(row, pty_instance.terminal.rows -| 1);
                const pin = screen.pages.pin(.{ .viewport = .{
                    .x = clamped_col,
                    .y = clamped_row,
                } }) orelse return;

                // Store the pin so it survives viewport scrolling during drag
                pty_instance.selection_start = pin;

                switch (pty_instance.left_click_count) {
                    1 => screen.select(null) catch {},
                    2 => {
                        if (screen.selectWord(pin, &[_]u21{})) |sel| {
                            screen.select(sel) catch {};
                        }
                    },
                    3 => {
                        if (screen.selectLine(.{ .pin = pin })) |sel| {
                            screen.select(sel) catch {};
                        }
                    },
                    else => {},
                }
                _ = posix.write(pty_instance.pipe_fds[1], "x") catch {};
            },
            .drag => {
                if (pty_instance.selection_start) |start| {
                    pty_instance.terminal_mutex.lock();
                    defer pty_instance.terminal_mutex.unlock();

                    const screen = pty_instance.terminal.screens.active;
                    const terminal_rows = pty_instance.terminal.rows;
                    const terminal_cols = pty_instance.terminal.cols;

                    // Auto-scroll when dragging beyond viewport edges
                    const raw_y = mouse.y;
                    if (raw_y < 0) {
                        pty_instance.terminal.scrollViewport(.{ .delta = -1 });
                    } else if (raw_y >= @as(f64, @floatFromInt(terminal_rows))) {
                        pty_instance.terminal.scrollViewport(.{ .delta = 1 });
                    }

                    // Clamp to valid viewport range after scrolling
                    const clamped_col: u16 = @min(col, terminal_cols -| 1);
                    const clamped_row: u16 = @intFromFloat(@max(0, @min(
                        @floor(raw_y),
                        @as(f64, @floatFromInt(terminal_rows -| 1)),
                    )));

                    // start is already a Pin (stored during press event)
                    const start_pin = start;
                    const end_pin = screen.pages.pin(.{ .viewport = .{
                        .x = clamped_col,
                        .y = clamped_row,
                    } }) orelse return;

                    switch (pty_instance.left_click_count) {
                        1 => {
                            const sel = ghostty_vt.Selection.init(start_pin, end_pin, false);
                            screen.select(sel) catch {};
                        },
                        2 => {
                            const word_start = screen.selectWord(start_pin, &[_]u21{});
                            const word_end = screen.selectWord(end_pin, &[_]u21{});
                            if (word_start != null and word_end != null) {
                                const sel = if (end_pin.before(start_pin))
                                    ghostty_vt.Selection.init(word_end.?.start(), word_start.?.end(), false)
                                else
                                    ghostty_vt.Selection.init(word_start.?.start(), word_end.?.end(), false);
                                screen.select(sel) catch {};
                            }
                        },
                        3 => {
                            const line_start = screen.selectLine(.{ .pin = start_pin });
                            const line_end = screen.selectLine(.{ .pin = end_pin });
                            if (line_start != null and line_end != null) {
                                const sel = if (end_pin.before(start_pin))
                                    ghostty_vt.Selection.init(line_end.?.start(), line_start.?.end(), false)
                                else
                                    ghostty_vt.Selection.init(line_start.?.start(), line_end.?.end(), false);
                                screen.select(sel) catch {};
                            }
                        },
                        else => {},
                    }
                    _ = posix.write(pty_instance.pipe_fds[1], "x") catch {};
                }
            },
            .release => {
                pty_instance.selection_start = null;
            },
            .motion => {},
        }
    }

    /// Handle mouse report for sending to terminal.
    fn handleMouseReport(
        self: *Client,
        pty_instance: *Pty,
        mouse: key_parse.MouseEvent,
        terminal: mouse_encode.TerminalState,
    ) !void {
        _ = self;
        var encode_buf: [32]u8 = undefined;
        var writer = std.Io.Writer.fixed(&encode_buf);

        mouse_encode.encode(&writer, mouse, terminal) catch |err| {
            log.err("Failed to encode mouse: {}", .{err});
            return;
        };

        const encoded = writer.buffered();
        if (encoded.len > 0) {
            _ = posix.write(pty_instance.process.master, encoded) catch |err| {
                log.err("Write to PTY failed: {}", .{err});
            };
        }
    }

    /// Resize PTY and update terminal dimensions.
    fn handleResizePty(self: *Client, notif: rpc.Notification) !void {
        if (notif.params != .array or notif.params.array.len < 3) {
            log.warn("resize_pty notification: invalid params", .{});
            return;
        }

        const pty_id = parsePtyId(notif.params.array[0]) orelse {
            log.warn("resize_pty notification: invalid pty_id type", .{});
            return;
        };
        const rows = parseU16(notif.params.array[1]) orelse {
            log.warn("resize_pty notification: invalid rows type", .{});
            return;
        };
        const cols = parseU16(notif.params.array[2]) orelse {
            log.warn("resize_pty notification: invalid cols type", .{});
            return;
        };

        var x_pixel: u16 = 0;
        var y_pixel: u16 = 0;
        if (notif.params.array.len >= 5) {
            x_pixel = parseU16(notif.params.array[3]) orelse 0;
            y_pixel = parseU16(notif.params.array[4]) orelse 0;
        }

        // Guard: zero dimensions crash ghostty terminal resize
        if (rows == 0 or cols == 0) {
            log.warn("resize_pty: ignoring zero dimensions {}x{}", .{ cols, rows });
            return;
        }

        if (self.server.ptys.get(pty_id)) |pty_instance| {
            pty_instance.terminal_mutex.lock();

            log.info("resize_pty: pty={} requested={}x{} ({}x{}px) current_terminal={}x{}", .{
                pty_id,                     cols,                       rows, x_pixel, y_pixel,
                pty_instance.terminal.cols, pty_instance.terminal.rows,
            });

            const size: pty.Winsize = .{
                .ws_row = rows,
                .ws_col = cols,
                .ws_xpixel = x_pixel,
                .ws_ypixel = y_pixel,
            };
            var pty_mut = pty_instance.process;
            pty_mut.setSize(size) catch |err| {
                log.err("Resize PTY failed: {}", .{err});
            };
            if (pty_instance.terminal.rows != rows or pty_instance.terminal.cols != cols) {
                log.info("resize_pty: resizing terminal from {}x{} to {}x{}", .{
                    pty_instance.terminal.cols,
                    pty_instance.terminal.rows,
                    cols,
                    rows,
                });
                pty_instance.terminal.resize(
                    pty_instance.allocator,
                    cols,
                    rows,
                ) catch |err| {
                    log.err("Resize terminal failed: {}", .{err});
                };
                pty_instance.terminal.screens.active.select(null) catch {};
            }
            // Update pixel dimensions for mouse encoding
            pty_instance.terminal.width_px = x_pixel;
            pty_instance.terminal.height_px = y_pixel;

            // Send in-band size report if mode 2048 is enabled
            const in_band_enabled = pty_instance.terminal.modes.get(.in_band_size_reports);
            log.info("resize_pty: in_band_size_reports mode={}", .{in_band_enabled});
            if (in_band_enabled) {
                var report_buf: [64]u8 = undefined;
                const report = std.fmt.bufPrint(&report_buf, "\x1b[48;{};{};{};{}t", .{
                    rows,
                    cols,
                    y_pixel,
                    x_pixel,
                }) catch unreachable;
                log.info("resize_pty: sending in-band report: {s}", .{report});
                _ = posix.write(pty_instance.process.master, report) catch |err| {
                    log.err("Failed to send in-band size report: {}", .{err});
                };
            }

            pty_instance.terminal_mutex.unlock();

            // Send full redraw to client so the resized terminal content is visible
            // immediately, without waiting for the child process to produce output
            const msg = buildRedrawMessageFromPty(self.server.allocator, pty_instance, .full) catch |err| {
                log.warn("resize_pty: failed to build redraw message: {}", .{err});
                return;
            };
            defer self.server.allocator.free(msg);

            self.server.sendRedraw(self.server.loop, pty_instance, msg, self) catch |err| {
                log.warn("resize_pty: failed to send redraw: {}", .{err});
            };

            log.info("resize_pty: completed for pty={}", .{pty_id});
        } else {
            log.warn("resize_pty notification: PTY {} not found", .{pty_id});
        }
    }

    /// Detach client from PTY.
    fn handleDetachPty(self: *Client, notif: rpc.Notification) !void {
        if (notif.params != .array or notif.params.array.len < 2) {
            log.warn("detach_pty notification: invalid params", .{});
            return;
        }

        const pty_id = parsePtyId(notif.params.array[0]) orelse {
            log.warn("detach_pty notification: invalid pty_id type", .{});
            return;
        };
        const client_fd = parseFd(notif.params.array[1]) orelse {
            log.warn("detach_pty notification: invalid client_fd type", .{});
            return;
        };

        if (self.server.ptys.get(pty_id)) |pty_instance| {
            for (self.server.clients.items) |c| {
                if (c.fd == client_fd) {
                    pty_instance.removeClient(c);
                    for (c.attached_ptys.items, 0..) |pid, i| {
                        if (pid == pty_id) {
                            _ = c.attached_ptys.swapRemove(i);
                            break;
                        }
                    }
                    log.info("Client {} detached from PTY {}", .{ c.fd, pty_id });
                    break;
                }
            }
        } else {
            log.warn("detach_pty notification: PTY {} not found", .{pty_id});
        }
    }

    /// Send focus in/out event to terminal.
    fn handleFocusEvent(self: *Client, notif: rpc.Notification) !void {
        if (notif.params != .array or notif.params.array.len < 2) {
            log.warn("focus_event notification: invalid params", .{});
            return;
        }

        const pty_id = parsePtyId(notif.params.array[0]) orelse {
            log.warn("focus_event notification: invalid pty_id type", .{});
            return;
        };
        const focused: bool = switch (notif.params.array[1]) {
            .boolean => |b| b,
            else => {
                log.warn("focus_event notification: invalid focused type", .{});
                return;
            },
        };

        if (self.server.ptys.get(pty_id)) |pty_instance| {
            pty_instance.terminal_mutex.lock();
            const focus_event_enabled = pty_instance.terminal.modes.get(.focus_event);
            pty_instance.terminal_mutex.unlock();

            if (focus_event_enabled) {
                const seq: []const u8 = if (focused) "\x1b[I" else "\x1b[O";
                _ = posix.write(pty_instance.process.master, seq) catch |err| {
                    log.err("Failed to write focus event to PTY: {}", .{err});
                };
                log.debug("Sent focus {} to PTY {}", .{ focused, pty_id });
            }
        } else {
            log.warn("focus_event notification: PTY {} not found", .{pty_id});
        }
    }

    /// Handle color_response notification from client.
    /// Formats and writes OSC color response to the PTY.
    fn handleColorResponse(self: *Client, notif: rpc.Notification) !void {
        if (notif.params != .map) {
            log.warn("color_response notification: invalid params (expected map)", .{});
            return;
        }

        var pty_id: ?usize = null;
        var r: ?u8 = null;
        var g: ?u8 = null;
        var b: ?u8 = null;
        var index: ?u8 = null;
        var kind: ?[]const u8 = null;
        var slot: ?usize = null;

        for (notif.params.map) |kv| {
            if (kv.key != .string) continue;
            if (std.mem.eql(u8, kv.key.string, "pty_id")) {
                pty_id = parsePtyId(kv.value);
            } else if (std.mem.eql(u8, kv.key.string, "r")) {
                r = parseU8(kv.value);
            } else if (std.mem.eql(u8, kv.key.string, "g")) {
                g = parseU8(kv.value);
            } else if (std.mem.eql(u8, kv.key.string, "b")) {
                b = parseU8(kv.value);
            } else if (std.mem.eql(u8, kv.key.string, "index")) {
                index = parseU8(kv.value);
            } else if (std.mem.eql(u8, kv.key.string, "kind")) {
                kind = if (kv.value == .string) kv.value.string else null;
            } else if (std.mem.eql(u8, kv.key.string, "slot")) {
                slot = parsePtyId(kv.value);
            }
        }

        const pid = pty_id orelse {
            log.warn("color_response: missing pty_id", .{});
            return;
        };
        const red = r orelse {
            log.warn("color_response: missing r", .{});
            return;
        };
        const green = g orelse {
            log.warn("color_response: missing g", .{});
            return;
        };
        const blue = b orelse {
            log.warn("color_response: missing b", .{});
            return;
        };
        const response_slot = slot orelse {
            log.warn("color_response: missing slot", .{});
            return;
        };

        const pty_instance = self.server.ptys.get(pid) orelse {
            log.warn("color_response: PTY {} not found", .{pid});
            return;
        };

        // Format OSC response: rgb:RRRR/GGGG/BBBB (16-bit scaled)
        // Scale 8-bit to 16-bit by duplicating: 0xAB -> 0xABAB
        const r16 = @as(u16, red) * 0x101;
        const g16 = @as(u16, green) * 0x101;
        const b16 = @as(u16, blue) * 0x101;

        var buf: [64]u8 = undefined;
        const response: []const u8 = if (index) |idx|
            // OSC 4 response: \x1b]4;INDEX;rgb:RRRR/GGGG/BBBB\x1b\\
            std.fmt.bufPrint(&buf, "\x1b]4;{};rgb:{x:0>4}/{x:0>4}/{x:0>4}\x1b\\", .{ idx, r16, g16, b16 }) catch return
        else if (kind) |k|
            // OSC 10/11/12 response
            if (std.mem.eql(u8, k, "foreground"))
                std.fmt.bufPrint(&buf, "\x1b]10;rgb:{x:0>4}/{x:0>4}/{x:0>4}\x1b\\", .{ r16, g16, b16 }) catch return
            else if (std.mem.eql(u8, k, "background"))
                std.fmt.bufPrint(&buf, "\x1b]11;rgb:{x:0>4}/{x:0>4}/{x:0>4}\x1b\\", .{ r16, g16, b16 }) catch return
            else if (std.mem.eql(u8, k, "cursor"))
                std.fmt.bufPrint(&buf, "\x1b]12;rgb:{x:0>4}/{x:0>4}/{x:0>4}\x1b\\", .{ r16, g16, b16 }) catch return
            else {
                log.warn("color_response: unknown kind '{s}'", .{k});
                return;
            }
        else {
            log.warn("color_response: missing index or kind", .{});
            return;
        };

        // Fill the response into its slot and try to flush
        pty_instance.fillColorResponse(response_slot, response);
        _ = pty_instance.flushResponses();

        log.debug("Filled color response slot {} for PTY {}: {s}", .{ response_slot, pid, response });
    }

    /// Parse u8 from msgpack value, returns null if invalid type.
    fn parseU8(val: msgpack.Value) ?u8 {
        return switch (val) {
            .unsigned => |u| if (u <= 255) @intCast(u) else null,
            .integer => |i| if (i >= 0 and i <= 255) @intCast(i) else null,
            else => null,
        };
    }

    /// Parse PTY ID from msgpack value, returns null if invalid type.
    fn parsePtyId(val: msgpack.Value) ?usize {
        return switch (val) {
            .unsigned => |u| @intCast(u),
            .integer => |i| @intCast(i),
            else => null,
        };
    }

    /// Parse u16 from msgpack value, returns null if invalid type.
    fn parseU16(val: msgpack.Value) ?u16 {
        return switch (val) {
            .unsigned => |u| @intCast(u),
            .integer => |i| @intCast(i),
            else => null,
        };
    }

    /// Parse file descriptor from msgpack value, returns null if invalid type.
    fn parseFd(val: msgpack.Value) ?posix.fd_t {
        return switch (val) {
            .unsigned => |u| @intCast(u),
            .integer => |i| @intCast(i),
            else => null,
        };
    }

    /// Parse input data (string or binary) from msgpack value, returns null if invalid type.
    fn parseInputData(val: msgpack.Value) ?[]const u8 {
        return switch (val) {
            .binary => |b| b,
            .string => |s| s,
            else => null,
        };
    }

    fn onRecv(loop: *io.Loop, completion: io.Completion) anyerror!void {
        const client = completion.userdataCast(Client);
        const allocator = client.server.allocator;

        switch (completion.result) {
            .recv => |bytes_read| {
                if (bytes_read == 0) {
                    // EOF - client disconnected
                    log.debug("Client fd={} disconnected (EOF)", .{client.fd});
                    client.server.removeClient(client);
                    return;
                }

                // Accumulate received data
                client.msg_buffer.appendSlice(allocator, client.recv_buffer[0..bytes_read]) catch |err| {
                    log.err("Failed to append to msg_buffer: {}", .{err});
                    client.server.removeClient(client);
                    return;
                };

                // Process all complete messages in the buffer
                while (client.msg_buffer.items.len > 0) {
                    const result = rpc.decodeMessageWithSize(allocator, client.msg_buffer.items) catch |err| {
                        if (err == error.EndOfStream or err == error.UnexpectedEndOfInput) {
                            // Incomplete message, wait for more data
                            break;
                        }
                        log.err("Failed to decode message: {}", .{err});
                        client.server.removeClient(client);
                        return;
                    };
                    defer result.message.deinit(allocator);

                    client.processMessage(loop, result.message) catch |err| {
                        log.err("Failed to process message: {}", .{err});
                    };

                    // Remove consumed bytes from buffer
                    const remaining = client.msg_buffer.items.len - result.bytes_consumed;
                    if (remaining > 0) {
                        std.mem.copyForwards(u8, client.msg_buffer.items[0..remaining], client.msg_buffer.items[result.bytes_consumed..]);
                    }
                    client.msg_buffer.shrinkRetainingCapacity(remaining);
                }

                // Keep receiving
                _ = try loop.recv(client.fd, &client.recv_buffer, .{
                    .ptr = client,
                    .cb = onRecv,
                });
            },
            .err => {
                log.debug("Client fd={} disconnected (error)", .{client.fd});
                client.server.removeClient(client);
            },
            else => unreachable,
        }
    }
};

const Server = struct {
    allocator: std.mem.Allocator,
    loop: *io.Loop,
    listen_fd: posix.fd_t,
    socket_path: []const u8,
    clients: std.ArrayList(*Client),
    ptys: std.AutoHashMap(usize, *Pty),
    next_pty_id: usize = 0,
    accepting: bool = true,
    accept_task: ?io.Task = null,
    exit_on_idle: bool = false,
    signal_pipe_fds: [2]posix.fd_t,
    signal_buf: [1]u8 = undefined,
    /// Timestamp (ms since epoch) when server started - used to detect server restarts
    start_time_ms: i64 = 0,

    fn parseSpawnPtyParams(params: msgpack.Value) struct { size: pty.Winsize, attach: bool, cwd: ?[]const u8, env: ?[]const msgpack.Value, macos_option_as_alt: key_encode.OptionAsAlt, cmd: ?[]const u8 } {
        var rows: u16 = 24;
        var cols: u16 = 80;
        var attach: bool = false;
        var cwd: ?[]const u8 = null;
        var env: ?[]const msgpack.Value = null;
        var macos_option_as_alt: key_encode.OptionAsAlt = .false;
        var cmd: ?[]const u8 = null;

        if (params == .map) {
            for (params.map) |kv| {
                if (kv.key != .string) continue;
                if (std.mem.eql(u8, kv.key.string, "rows") and kv.value == .unsigned) {
                    rows = @intCast(kv.value.unsigned);
                } else if (std.mem.eql(u8, kv.key.string, "cols") and kv.value == .unsigned) {
                    cols = @intCast(kv.value.unsigned);
                } else if (std.mem.eql(u8, kv.key.string, "attach") and kv.value == .boolean) {
                    attach = kv.value.boolean;
                } else if (std.mem.eql(u8, kv.key.string, "cwd") and kv.value == .string) {
                    cwd = kv.value.string;
                } else if (std.mem.eql(u8, kv.key.string, "env") and kv.value == .array) {
                    env = kv.value.array;
                } else if (std.mem.eql(u8, kv.key.string, "macos_option_as_alt")) {
                    macos_option_as_alt = parseMacosOptionAsAlt(kv.value);
                } else if (std.mem.eql(u8, kv.key.string, "cmd") and kv.value == .string) {
                    cmd = kv.value.string;
                }
            }
        }

        return .{
            .size = .{
                .ws_row = rows,
                .ws_col = cols,
                .ws_xpixel = 0,
                .ws_ypixel = 0,
            },
            .attach = attach,
            .cwd = cwd,
            .env = env,
            .macos_option_as_alt = macos_option_as_alt,
            .cmd = cmd,
        };
    }

    fn prepareSpawnEnv(allocator: std.mem.Allocator, env_map: *std.process.EnvMap) !std.ArrayList([]const u8) {
        try env_map.put("TERM", "xterm-256color");
        try env_map.put("COLORTERM", "truecolor");

        var env_list = std.ArrayList([]const u8).empty;
        var it = env_map.iterator();
        while (it.next()) |entry| {
            const key_eq_val = try std.fmt.allocPrint(allocator, "{s}={s}", .{ entry.key_ptr.*, entry.value_ptr.* });
            try env_list.append(allocator, key_eq_val);
        }
        return env_list;
    }

    fn parseClosePtyParams(params: msgpack.Value) !usize {
        return parsePtyId(params);
    }

    fn parseAttachPtyParams(params: msgpack.Value) !struct { pty_id: usize, macos_option_as_alt: key_encode.OptionAsAlt } {
        const pty_id = try parsePtyId(params);
        const macos_option_as_alt = if (params == .array and params.array.len >= 2)
            parseMacosOptionAsAlt(params.array[1])
        else
            .false;
        return .{ .pty_id = pty_id, .macos_option_as_alt = macos_option_as_alt };
    }

    fn parsePtyId(params: msgpack.Value) !usize {
        if (params != .array or params.array.len < 1) {
            return error.InvalidParams;
        }
        return switch (params.array[0]) {
            .unsigned => |u| @intCast(u),
            .integer => |i| @intCast(i),
            else => error.InvalidParams,
        };
    }

    fn parseMacosOptionAsAlt(value: msgpack.Value) key_encode.OptionAsAlt {
        if (value == .string) {
            if (std.mem.eql(u8, value.string, "left")) {
                return .left;
            } else if (std.mem.eql(u8, value.string, "right")) {
                return .right;
            } else if (std.mem.eql(u8, value.string, "true")) {
                return .true;
            }
        } else if (value == .boolean and value.boolean) {
            return .true;
        }
        return .false;
    }

    fn parseWritePtyParams(params: msgpack.Value) !struct { id: usize, data: []const u8 } {
        if (params != .array or params.array.len < 2 or params.array[0] != .unsigned or params.array[1] != .binary) {
            return error.InvalidParams;
        }
        return .{
            .id = @intCast(params.array[0].unsigned),
            .data = params.array[1].binary,
        };
    }

    fn parseResizePtyParams(params: msgpack.Value) !struct { id: usize, rows: u16, cols: u16, x_pixel: u16, y_pixel: u16 } {
        if (params != .array or params.array.len < 3 or params.array[0] != .unsigned or params.array[1] != .unsigned or params.array[2] != .unsigned) {
            return error.InvalidParams;
        }

        var x_pixel: u16 = 0;
        var y_pixel: u16 = 0;

        if (params.array.len >= 5) {
            x_pixel = switch (params.array[3]) {
                .unsigned => |u| @intCast(u),
                .integer => |i| @intCast(i),
                else => 0,
            };
            y_pixel = switch (params.array[4]) {
                .unsigned => |u| @intCast(u),
                .integer => |i| @intCast(i),
                else => 0,
            };
        }

        return .{
            .id = @intCast(params.array[0].unsigned),
            .rows = @intCast(params.array[1].unsigned),
            .cols = @intCast(params.array[2].unsigned),
            .x_pixel = x_pixel,
            .y_pixel = y_pixel,
        };
    }

    fn handleSpawnPty(self: *Server, client: *Client, params: msgpack.Value) !msgpack.Value {
        if (self.ptys.count() >= LIMITS.PTYS_MAX) {
            log.warn("PTY limit reached ({})", .{LIMITS.PTYS_MAX});
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "PTY limit reached") };
        }

        var parsed = parseSpawnPtyParams(params);
        // Ensure minimum dimensions (zero crashes ghostty terminal init)
        if (parsed.size.ws_row == 0) parsed.size.ws_row = 24;
        if (parsed.size.ws_col == 0) parsed.size.ws_col = 80;
        const cwd = parsed.cwd orelse posix.getenv("HOME");
        log.info("spawn_pty: rows={} cols={} attach={} cwd={?s} has_client_env={}", .{ parsed.size.ws_row, parsed.size.ws_col, parsed.attach, cwd, parsed.env != null });

        var shell: []const u8 = "/bin/sh";
        var env_list = std.ArrayList([]const u8).empty;
        defer {
            for (env_list.items) |item| {
                self.allocator.free(item);
            }
            env_list.deinit(self.allocator);
        }

        if (parsed.env) |client_env| {
            for (client_env) |val| {
                if (val == .string) {
                    const env_str = try self.allocator.dupe(u8, val.string);
                    try env_list.append(self.allocator, env_str);
                    if (std.mem.startsWith(u8, val.string, "SHELL=")) {
                        shell = env_str[6..];
                    }
                }
            }
            const term_str = try self.allocator.dupe(u8, "TERM=xterm-256color");
            try env_list.append(self.allocator, term_str);
            const colorterm_str = try self.allocator.dupe(u8, "COLORTERM=truecolor");
            try env_list.append(self.allocator, colorterm_str);
        } else {
            var env_map = try std.process.getEnvMap(self.allocator);
            defer env_map.deinit();
            const prepared = try prepareSpawnEnv(self.allocator, &env_map);
            env_list = prepared;
            if (posix.getenv("SHELL")) |s| {
                shell = s;
            }
        }

        const process = try pty.Process.spawn(self.allocator, parsed.size, &.{shell}, @ptrCast(env_list.items), cwd);

        const pty_id = self.next_pty_id;
        self.next_pty_id += 1;

        const pty_instance = try Pty.init(self.allocator, pty_id, process, parsed.size);
        pty_instance.server_ptr = self;

        try self.ptys.put(pty_id, pty_instance);
        std.debug.assert(self.ptys.count() <= LIMITS.PTYS_MAX);

        pty_instance.read_thread = try std.Thread.spawn(.{}, Pty.readThread, .{ pty_instance, self });

        _ = try self.loop.read(pty_instance.pipe_fds[0], &pty_instance.dirty_signal_buf, .{
            .ptr = pty_instance,
            .cb = onPtyDirty,
        });

        if (parsed.attach) {
            client.macos_option_as_alt = parsed.macos_option_as_alt;
            try pty_instance.addClient(self.allocator, client);
            try client.attached_ptys.append(self.allocator, pty_id);

            log.info("Sending initial redraw for PTY {}", .{pty_id});
            const msg = try buildRedrawMessageFromPty(self.allocator, pty_instance, .full);
            defer self.allocator.free(msg);
            try self.sendRedraw(self.loop, pty_instance, msg, client);
        }

        // Write initial command to PTY if specified
        if (parsed.cmd) |cmd| {
            log.info("Writing initial command to PTY {}: {s}", .{ pty_id, cmd });
            const cmd_with_newline = try std.fmt.allocPrint(self.allocator, "{s}\n", .{cmd});
            defer self.allocator.free(cmd_with_newline);

            var total_written: usize = 0;
            while (total_written < cmd_with_newline.len) {
                const written = posix.write(process.master, cmd_with_newline[total_written..]) catch |err| {
                    log.warn("Failed to write initial command to PTY {}: {}", .{ pty_id, err });
                    break;
                };
                total_written += written;
            }
        }

        log.info("Created PTY {} with PID {}", .{ pty_id, process.pid });

        return msgpack.Value{ .unsigned = pty_id };
    }

    fn handleClosePty(self: *Server, params: msgpack.Value) !msgpack.Value {
        const pty_id = parseClosePtyParams(params) catch {
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "invalid params") };
        };

        if (self.ptys.get(pty_id)) |pty_instance| {
            // Signal read thread to exit - it will handle killing and reaping the process
            pty_instance.running.store(false, .seq_cst);
            _ = posix.write(pty_instance.exit_pipe_fds[1], "q") catch {};

            log.info("Signaled PTY {} to close", .{pty_id});
            return msgpack.Value.nil;
        } else {
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "PTY not found") };
        }
    }

    fn handleAttachPty(self: *Server, client: *Client, params: msgpack.Value) !msgpack.Value {
        log.info("attach_pty called with params: {}", .{params});
        const parsed = parseAttachPtyParams(params) catch |err| {
            log.warn("attach_pty: invalid params: {}", .{err});
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "invalid params") };
        };

        log.info("attach_pty: pty_id={} client_fd={} macos_option_as_alt={}", .{ parsed.pty_id, client.fd, parsed.macos_option_as_alt });

        const pty_instance = self.ptys.get(parsed.pty_id) orelse {
            log.warn("attach_pty: PTY {} not found", .{parsed.pty_id});
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "PTY not found") };
        };

        client.macos_option_as_alt = parsed.macos_option_as_alt;

        try pty_instance.addClient(self.allocator, client);
        try client.attached_ptys.append(self.allocator, parsed.pty_id);
        log.info("Client {} attached to PTY {}", .{ client.fd, parsed.pty_id });

        const msg = try buildRedrawMessageFromPty(self.allocator, pty_instance, .full);
        defer self.allocator.free(msg);

        try self.sendRedraw(self.loop, pty_instance, msg, client);

        return msgpack.Value{ .unsigned = parsed.pty_id };
    }

    fn handleWritePty(self: *Server, params: msgpack.Value) !msgpack.Value {
        const args = parseWritePtyParams(params) catch {
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "invalid params") };
        };

        const pty_instance = self.ptys.get(args.id) orelse {
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "PTY not found") };
        };

        _ = posix.write(pty_instance.process.master, args.data) catch |err| {
            log.err("Write to PTY failed: {}", .{err});
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "write failed") };
        };

        return msgpack.Value.nil;
    }

    fn handleResizePty(self: *Server, client: *Client, params: msgpack.Value) !msgpack.Value {
        const args = parseResizePtyParams(params) catch {
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "invalid params") };
        };

        const pty_instance = self.ptys.get(args.id) orelse {
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "PTY not found") };
        };

        // Lock mutex before any terminal state access to avoid race with read thread
        pty_instance.terminal_mutex.lock();

        log.info("resize_pty request: pty={} requested={}x{} ({}x{}px) current={}x{}", .{
            args.id,
            args.cols,
            args.rows,
            args.x_pixel,
            args.y_pixel,
            pty_instance.terminal.cols,
            pty_instance.terminal.rows,
        });

        const size: pty.Winsize = .{
            .ws_row = args.rows,
            .ws_col = args.cols,
            .ws_xpixel = args.x_pixel,
            .ws_ypixel = args.y_pixel,
        };

        var pty_mut = pty_instance.process;
        pty_mut.setSize(size) catch |err| {
            log.err("Resize PTY failed: {}", .{err});
            pty_instance.terminal_mutex.unlock();
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "resize failed") };
        };
        if (pty_instance.terminal.rows != args.rows or pty_instance.terminal.cols != args.cols) {
            log.info("resize_pty request: resizing terminal from {}x{} to {}x{}", .{
                pty_instance.terminal.cols,
                pty_instance.terminal.rows,
                args.cols,
                args.rows,
            });
            pty_instance.terminal.resize(pty_instance.allocator, args.cols, args.rows) catch |err| {
                log.err("Resize terminal failed: {}", .{err});
            };
        }

        pty_instance.terminal.width_px = args.x_pixel;
        pty_instance.terminal.height_px = args.y_pixel;

        const in_band_enabled = pty_instance.terminal.modes.get(.in_band_size_reports);
        log.info("resize_pty request: in_band_size_reports mode={}", .{in_band_enabled});
        if (in_band_enabled) {
            var report_buf: [64]u8 = undefined;
            const report = std.fmt.bufPrint(&report_buf, "\x1b[48;{};{};{};{}t", .{
                args.rows,
                args.cols,
                args.y_pixel,
                args.x_pixel,
            }) catch unreachable;
            log.info("resize_pty request: sending in-band report", .{});
            _ = posix.write(pty_instance.process.master, report) catch |err| {
                log.err("Failed to send in-band size report: {}", .{err});
            };
        }

        pty_instance.terminal_mutex.unlock();

        // Send full redraw to client so the resized terminal content is visible
        // immediately, without waiting for the child process to produce output
        const msg = buildRedrawMessageFromPty(self.allocator, pty_instance, .full) catch |err| {
            log.warn("resize_pty request: failed to build redraw message: {}", .{err});
            return msgpack.Value.nil;
        };
        defer self.allocator.free(msg);

        self.sendRedraw(self.loop, pty_instance, msg, client) catch |err| {
            log.warn("resize_pty request: failed to send redraw: {}", .{err});
        };

        log.info("Resized PTY {} to {}x{} ({}x{}px)", .{ args.id, args.cols, args.rows, args.x_pixel, args.y_pixel });
        return msgpack.Value.nil;
    }

    fn handleDetachPty(self: *Server, client: *Client, params: msgpack.Value) !msgpack.Value {
        const pty_id: usize = switch (params) {
            .unsigned => |u| u,
            .integer => |i| @intCast(i),
            .array => |arr| blk: {
                if (arr.len < 1) return msgpack.Value{ .string = try self.allocator.dupe(u8, "invalid params") };
                break :blk switch (arr[0]) {
                    .unsigned => |u| u,
                    .integer => |i| @intCast(i),
                    else => return msgpack.Value{ .string = try self.allocator.dupe(u8, "invalid params") },
                };
            },
            else => return msgpack.Value{ .string = try self.allocator.dupe(u8, "invalid params") },
        };

        const pty_instance = self.ptys.get(pty_id) orelse {
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "PTY not found") };
        };

        pty_instance.removeClient(client);
        for (client.attached_ptys.items, 0..) |pid, i| {
            if (pid == pty_id) {
                _ = client.attached_ptys.swapRemove(i);
                break;
            }
        }
        log.info("Client {} detached from PTY {}", .{ client.fd, pty_id });

        return msgpack.Value.nil;
    }

    fn handleDetachPtys(self: *Server, client: *Client, params: msgpack.Value) !msgpack.Value {
        const pty_ids = switch (params) {
            .array => |arr| arr,
            else => return msgpack.Value{ .string = try self.allocator.dupe(u8, "invalid params") },
        };

        for (pty_ids) |pty_id_val| {
            const pty_id: usize = switch (pty_id_val) {
                .unsigned => |u| u,
                .integer => |i| @intCast(i),
                else => continue,
            };

            if (self.ptys.get(pty_id)) |pty_instance| {
                pty_instance.removeClient(client);
                for (client.attached_ptys.items, 0..) |pid, i| {
                    if (pid == pty_id) {
                        _ = client.attached_ptys.swapRemove(i);
                        break;
                    }
                }
                std.log.info("Client {} detached from PTY {}", .{ client.fd, pty_id });
            }
        }

        return msgpack.Value.nil;
    }

    fn handleGetSelection(self: *Server, params: msgpack.Value) !msgpack.Value {
        const pty_id = parsePtyId(params) catch {
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "invalid params") };
        };

        const pty_instance = self.ptys.get(pty_id) orelse {
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "PTY not found") };
        };

        pty_instance.terminal_mutex.lock();
        defer pty_instance.terminal_mutex.unlock();

        const screen = pty_instance.terminal.screens.active;
        const sel = screen.selection orelse {
            return msgpack.Value.nil;
        };

        const result_z = screen.selectionString(self.allocator, .{
            .sel = sel,
            .trim = true,
        }) catch |err| {
            std.log.err("Failed to get selection string: {}", .{err});
            return msgpack.Value.nil;
        };

        // selectionString returns a sentinel-terminated slice ([:0]const u8).
        // msgpack.Value stores plain []const u8 and later deinit() calls
        // allocator.free on that plain slice. If we pass result_z directly,
        // deinit will free len bytes instead of len+1, triggering allocator
        // size-mismatch crashes when copying selections.
        defer self.allocator.free(result_z);
        const result = self.allocator.dupe(u8, result_z) catch |err| {
            std.log.err("Failed to duplicate selection string: {}", .{err});
            return msgpack.Value.nil;
        };

        return msgpack.Value{ .string = result };
    }

    fn handleClearSelection(self: *Server, params: msgpack.Value) !msgpack.Value {
        const pty_id = parsePtyId(params) catch {
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "invalid params") };
        };

        const pty_instance = self.ptys.get(pty_id) orelse {
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "PTY not found") };
        };

        pty_instance.terminal_mutex.lock();
        const screen = pty_instance.terminal.screens.active;
        screen.select(null) catch {};
        pty_instance.terminal_mutex.unlock();

        _ = posix.write(pty_instance.pipe_fds[1], "x") catch {};

        return msgpack.Value.nil;
    }

    fn handleGetServerInfo(self: *Server) !msgpack.Value {
        const entries = try self.allocator.alloc(msgpack.Value.KeyValue, 2);
        entries[0] = .{
            .key = .{ .string = try self.allocator.dupe(u8, "version") },
            .value = .{ .string = try self.allocator.dupe(u8, main.version) },
        };
        entries[1] = .{
            .key = .{ .string = try self.allocator.dupe(u8, "pty_validity") },
            .value = .{ .integer = self.start_time_ms },
        };
        return .{ .map = entries };
    }

    fn handleListPtys(self: *Server) !msgpack.Value {
        const pty_count = self.ptys.count();
        const ptys_array = try self.allocator.alloc(msgpack.Value, pty_count);
        errdefer self.allocator.free(ptys_array);

        var i: usize = 0;
        var iter = self.ptys.iterator();
        while (iter.next()) |entry| {
            const pty_instance = entry.value_ptr.*;
            const pty_entries = try self.allocator.alloc(msgpack.Value.KeyValue, 4);

            // Lock mutex to safely dupe cwd and title (read thread may reallocate)
            const cwd_str, const title_str = blk: {
                pty_instance.terminal_mutex.lock();
                defer pty_instance.terminal_mutex.unlock();
                break :blk .{
                    try self.allocator.dupe(u8, pty_instance.cwd.items),
                    try self.allocator.dupe(u8, pty_instance.title.items),
                };
            };

            pty_entries[0] = .{
                .key = .{ .string = try self.allocator.dupe(u8, "id") },
                .value = .{ .unsigned = @intCast(pty_instance.id) },
            };
            pty_entries[1] = .{
                .key = .{ .string = try self.allocator.dupe(u8, "cwd") },
                .value = .{ .string = cwd_str },
            };
            pty_entries[2] = .{
                .key = .{ .string = try self.allocator.dupe(u8, "title") },
                .value = .{ .string = title_str },
            };
            pty_entries[3] = .{
                .key = .{ .string = try self.allocator.dupe(u8, "attached_client_count") },
                .value = .{ .unsigned = @intCast(pty_instance.clients.items.len) },
            };

            ptys_array[i] = .{ .map = pty_entries };
            i += 1;
        }

        const result_entries = try self.allocator.alloc(msgpack.Value.KeyValue, 2);
        result_entries[0] = .{
            .key = .{ .string = try self.allocator.dupe(u8, "pty_validity") },
            .value = .{ .integer = self.start_time_ms },
        };
        result_entries[1] = .{
            .key = .{ .string = try self.allocator.dupe(u8, "ptys") },
            .value = .{ .array = ptys_array },
        };
        return .{ .map = result_entries };
    }

    fn handleRequest(self: *Server, client: *Client, method: []const u8, params: msgpack.Value) !msgpack.Value {
        if (std.mem.eql(u8, method, "ping")) {
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "pong") };
        } else if (std.mem.eql(u8, method, "get_server_info")) {
            return self.handleGetServerInfo();
        } else if (std.mem.eql(u8, method, "list_ptys")) {
            return self.handleListPtys();
        } else if (std.mem.eql(u8, method, "spawn_pty")) {
            return self.handleSpawnPty(client, params);
        } else if (std.mem.eql(u8, method, "close_pty")) {
            return self.handleClosePty(params);
        } else if (std.mem.eql(u8, method, "attach_pty")) {
            return self.handleAttachPty(client, params);
        } else if (std.mem.eql(u8, method, "write_pty")) {
            return self.handleWritePty(params);
        } else if (std.mem.eql(u8, method, "resize_pty")) {
            return self.handleResizePty(client, params);
        } else if (std.mem.eql(u8, method, "detach_pty")) {
            return self.handleDetachPty(client, params);
        } else if (std.mem.eql(u8, method, "detach_ptys")) {
            return self.handleDetachPtys(client, params);
        } else if (std.mem.eql(u8, method, "get_selection")) {
            return self.handleGetSelection(params);
        } else if (std.mem.eql(u8, method, "clear_selection")) {
            return self.handleClearSelection(params);
        } else {
            return msgpack.Value{ .string = try self.allocator.dupe(u8, "unknown method") };
        }
    }

    fn shouldExit(self: *Server) bool {
        return self.exit_on_idle and self.clients.items.len == 0;
    }

    fn checkExit(self: *Server) !void {
        if (self.shouldExit() and self.accepting) {
            self.accepting = false;
            if (self.accept_task) |*task| {
                try task.cancel(self.loop);
                self.accept_task = null;
            }
        }
    }

    fn onAccept(loop: *io.Loop, completion: io.Completion) anyerror!void {
        const self = completion.userdataCast(Server);

        switch (completion.result) {
            .accept => |client_fd| {
                std.log.debug("Accepted client connection fd={}", .{client_fd});

                if (self.clients.items.len >= LIMITS.CLIENTS_MAX) {
                    std.log.warn("Client limit reached ({}), rejecting connection", .{LIMITS.CLIENTS_MAX});
                    _ = try loop.close(client_fd, .{
                        .ptr = null,
                        .cb = struct {
                            fn noop(_: *io.Loop, _: io.Completion) anyerror!void {}
                        }.noop,
                    });
                    // Queue next accept if still accepting
                    if (self.accepting) {
                        self.accept_task = try loop.accept(self.listen_fd, .{
                            .ptr = self,
                            .cb = onAccept,
                        });
                    }
                    return;
                }

                const client = try self.allocator.create(Client);
                client.* = .{
                    .fd = client_fd,
                    .server = self,
                    .msg_buffer = std.ArrayList(u8).empty,
                    .send_queue = std.ArrayList([]u8).empty,
                    .attached_ptys = std.ArrayList(usize).empty,
                    // .style_cache = std.AutoHashMap(u16, redraw.UIEvent.Style.Attributes).init(self.allocator),
                };
                try self.clients.append(self.allocator, client);
                std.debug.assert(self.clients.items.len <= LIMITS.CLIENTS_MAX);
                std.log.debug("Total clients: {}", .{self.clients.items.len});

                // Start recv to detect disconnect
                _ = try loop.recv(client_fd, &client.recv_buffer, .{
                    .ptr = client,
                    .cb = Client.onRecv,
                });

                // Queue next accept if still accepting
                if (self.accepting) {
                    self.accept_task = try loop.accept(self.listen_fd, .{
                        .ptr = self,
                        .cb = onAccept,
                    });
                }
            },
            .err => |err| {
                std.log.err("Accept error: {}", .{err});
            },
            else => unreachable,
        }
    }

    fn removeClient(self: *Server, client: *Client) void {
        std.log.debug("Removing client fd={}", .{client.fd});

        // Remove client from any PTYs it was attached to (but don't kill them)
        for (client.attached_ptys.items) |pty_id| {
            if (self.ptys.get(pty_id)) |pty_instance| {
                pty_instance.removeClient(client);
            }
        }

        // Mark as closing to prevent new sends
        client.closing = true;

        // Cancel pending recv on this client's FD
        self.loop.cancelByFd(client.fd);

        // cancelByFd silently drops pending sends — onSendComplete will never fire.
        // Free the cancelled send buffer so finishClose always runs.
        if (client.send_buffer) |buf| {
            self.allocator.free(buf);
            client.send_buffer = null;
            client.send_offset = 0;
        }

        client.finishClose(self.loop);
    }

    /// Send redraw notification (bytes) to attached clients
    fn sendRedraw(self: *Server, loop: *io.Loop, pty_instance: *Pty, msg: []const u8, target_client: ?*Client) !void {
        // Send to each client attached to this session
        for (self.clients.items) |client| {
            // If we have a target client, skip others
            if (target_client) |target| {
                if (client != target) {
                    continue;
                }
            }

            // Check if client is attached to this pty
            var attached = false;
            for (client.attached_ptys.items) |pid| {
                if (pid == pty_instance.id) {
                    attached = true;
                    break;
                }
            }
            if (!attached) {
                continue;
            }

            try client.sendData(loop, msg);
        }
    }

    fn renderFrame(self: *Server, pty_instance: *Pty) void {
        if (pty_instance.clients.items.len == 0) return;

        const msg = buildRedrawMessageFromPty(
            self.allocator,
            pty_instance,
            .incremental,
        ) catch |err| {
            std.log.err("Failed to build redraw message for session {}: {}", .{ pty_instance.id, err });
            return;
        };
        defer self.allocator.free(msg);

        // Build and send redraw notifications
        self.sendRedraw(self.loop, pty_instance, msg, null) catch |err| {
            std.log.err("Failed to send redraw for session {}: {}", .{ pty_instance.id, err });
        };

        // Send cwd_changed notification if cwd is dirty
        // Lock mutex to read+clear cwd_dirty and dupe cwd (read thread may reallocate)
        const cwd_update = blk: {
            pty_instance.terminal_mutex.lock();
            defer pty_instance.terminal_mutex.unlock();
            if (!pty_instance.cwd_dirty) break :blk @as(?[]u8, null);
            pty_instance.cwd_dirty = false;
            break :blk self.allocator.dupe(u8, pty_instance.cwd.items) catch null;
        };
        if (cwd_update) |cwd_copy| {
            defer self.allocator.free(cwd_copy);
            self.sendCwdChanged(pty_instance, cwd_copy) catch |err| {
                std.log.err("Failed to send cwd_changed for pty {}: {}", .{ pty_instance.id, err });
            };
        }

        // Send pending color_query notifications
        self.sendColorQueries(pty_instance) catch |err| {
            std.log.err("Failed to send color_query for pty {}: {}", .{ pty_instance.id, err });
        };

        // Update timestamp
        pty_instance.last_render_time = std.time.milliTimestamp();
    }

    /// Build and send pty_exited notification to all clients
    fn sendPtyExited(self: *Server, pty_id: usize, exit_status: u32) !void {
        const params = .{ pty_id, exit_status };
        const msg_bytes = try msgpack.encode(self.allocator, .{ 2, "pty_exited", params });
        defer self.allocator.free(msg_bytes);

        std.log.info("Sending pty_exited for session {} status {}", .{ pty_id, exit_status });

        // Send to all clients
        for (self.clients.items) |client| {
            try client.sendData(self.loop, msg_bytes);
        }
    }

    fn sendCwdChanged(self: *Server, pty_instance: *Pty, cwd: []const u8) !void {
        if (cwd.len == 0) return;

        var map_items = try self.allocator.alloc(msgpack.Value.KeyValue, 2);
        defer self.allocator.free(map_items);
        map_items[0] = .{ .key = .{ .string = "pty_id" }, .value = .{ .unsigned = pty_instance.id } };
        map_items[1] = .{ .key = .{ .string = "cwd" }, .value = .{ .string = cwd } };

        const params = msgpack.Value{ .map = map_items };
        const msg_bytes = try msgpack.encode(self.allocator, .{ 2, "cwd_changed", params });
        defer self.allocator.free(msg_bytes);

        log.info("Sending cwd_changed for pty {}: {s}", .{ pty_instance.id, cwd });

        // Send to each client attached to this pty (same pattern as sendRedraw)
        for (self.clients.items) |client| {
            var attached = false;
            for (client.attached_ptys.items) |pid| {
                if (pid == pty_instance.id) {
                    attached = true;
                    break;
                }
            }
            if (attached) {
                try client.sendData(self.loop, msg_bytes);
            }
        }
    }

    fn sendColorQueries(self: *Server, pty_instance: *Pty) !void {
        pty_instance.color_queries_mutex.lock();
        defer pty_instance.color_queries_mutex.unlock();

        const now_ms = std.time.milliTimestamp();

        while (pty_instance.color_queries_len > 0) {
            const query = pty_instance.color_queries_buf[0];

            // Helper to remove first element
            const removeFirst = struct {
                fn remove(p: *Pty) void {
                    const remaining = p.color_queries_len - 1;
                    if (remaining > 0) {
                        std.mem.copyForwards(
                            Pty.ColorQuery,
                            p.color_queries_buf[0..remaining],
                            p.color_queries_buf[1..][0..remaining],
                        );
                    }
                    p.color_queries_len -= 1;
                }
            }.remove;

            // Skip expired queries
            if (now_ms - query.timestamp_ms > LIMITS.COLOR_QUERY_TIMEOUT_MS) {
                removeFirst(pty_instance);
                continue;
            }

            // Build notification params based on target type (include slot for response routing)
            var map_items = try self.allocator.alloc(msgpack.Value.KeyValue, 3);
            defer self.allocator.free(map_items);

            map_items[0] = .{ .key = .{ .string = "pty_id" }, .value = .{ .unsigned = pty_instance.id } };
            map_items[2] = .{ .key = .{ .string = "slot" }, .value = .{ .unsigned = query.response_slot } };

            switch (query.target) {
                .palette => |idx| {
                    map_items[1] = .{ .key = .{ .string = "index" }, .value = .{ .unsigned = idx } };
                },
                .dynamic => |dyn| {
                    const name: []const u8 = switch (dyn) {
                        .foreground => "foreground",
                        .background => "background",
                        .cursor => "cursor",
                        else => {
                            removeFirst(pty_instance);
                            continue;
                        },
                    };
                    map_items[1] = .{ .key = .{ .string = "kind" }, .value = .{ .string = name } };
                },
                .special => {
                    removeFirst(pty_instance);
                    continue;
                },
            }

            const params = msgpack.Value{ .map = map_items };
            const msg_bytes = try msgpack.encode(self.allocator, .{ 2, "color_query", params });
            defer self.allocator.free(msg_bytes);

            // Send to each client attached to this pty
            for (self.clients.items) |client| {
                var attached = false;
                for (client.attached_ptys.items) |pid| {
                    if (pid == pty_instance.id) {
                        attached = true;
                        break;
                    }
                }
                if (attached) {
                    client.sendData(self.loop, msg_bytes) catch |err| {
                        log.err("Failed to send color_query: {}", .{err});
                    };
                }
            }

            pty_instance.color_queries_sent += 1;
            removeFirst(pty_instance);
        }

        // Try to flush any ready responses (lock is already held)
        _ = pty_instance.flushResponsesUnlocked();
    }

    fn shutdown(self: *Server) void {
        std.log.info("Shutting down server...", .{});

        // Stop accepting
        self.accepting = false;
        if (self.accept_task) |*task| {
            task.cancel(self.loop) catch {};
            self.accept_task = null;
        }

        // Close all clients
        while (self.clients.items.len > 0) {
            const client = self.clients.items[0];
            self.removeClient(client);
        }

        // Cancel signal watcher
        self.loop.cancelByFd(self.signal_pipe_fds[0]);

        // Signal all PTYs to stop and cancel their IO
        var it = self.ptys.valueIterator();
        while (it.next()) |pty_instance| {
            pty_instance.*.running.store(false, .seq_cst);
            pty_instance.*.cancelPendingIO(self.loop);
            _ = posix.write(pty_instance.*.exit_pipe_fds[1], "q") catch {};
        }
    }

    fn onSignal(loop: *io.Loop, completion: io.Completion) anyerror!void {
        const self = completion.userdataCast(Server);

        switch (completion.result) {
            .read => |n| {
                if (n == 0) return;
                // Drain
                var buf: [128]u8 = undefined;
                while (true) {
                    _ = posix.read(self.signal_pipe_fds[0], &buf) catch |err| {
                        if (err == error.WouldBlock) break;
                        break;
                    };
                }

                self.shutdown();
            },
            .err => |err| {
                std.log.err("Signal pipe error: {}", .{err});
            },
            else => {},
        }
        _ = loop;
    }

    fn onRenderTimer(loop: *io.Loop, completion: io.Completion) anyerror!void {
        _ = loop;
        const pty_instance = completion.userdataCast(Pty);
        pty_instance.render_timer = null;
        if (!pty_instance.running.load(.seq_cst)) return;
        const server: *Server = @ptrCast(@alignCast(pty_instance.server_ptr));
        server.renderFrame(pty_instance);
    }

    fn onPtyDirty(loop: *io.Loop, completion: io.Completion) anyerror!void {
        const pty_instance = completion.userdataCast(Pty);
        const server: *Server = @ptrCast(@alignCast(pty_instance.server_ptr));

        switch (completion.result) {
            .read => |n| {
                if (n == 0) return;

                // Check if this is an exit signal from the read thread
                if (pty_instance.dirty_signal_buf[0] == 'e') {
                    // Process has exited - read thread already reaped it
                    server.handleProcessExit(loop, pty_instance);
                    return;
                }

                // Drain pipe (there may be more signals)
                var buf: [128]u8 = undefined;
                var saw_exit = false;
                while (true) {
                    const bytes = posix.read(pty_instance.pipe_fds[0], &buf) catch |err| {
                        if (err == error.WouldBlock) break;
                        break;
                    };
                    // Check for exit signal in drained data
                    for (buf[0..bytes]) |b| {
                        if (b == 'e') saw_exit = true;
                    }
                }

                if (saw_exit) {
                    server.handleProcessExit(loop, pty_instance);
                    return;
                }

                const now = std.time.milliTimestamp();
                // 8ms (~120fps) balances responsiveness with efficiency. Lower values
                // increase CPU usage with diminishing perceptual benefit; higher values
                // cause visible lag during fast output (e.g., `cat large_file`).
                // See ARCHITECTURE.md "Event-Oriented Frame Scheduler".
                const FRAME_TIME = 8;

                if (now - pty_instance.last_render_time >= FRAME_TIME) {
                    server.renderFrame(pty_instance);
                } else if (pty_instance.render_timer == null) {
                    const delay = FRAME_TIME - (now - pty_instance.last_render_time);
                    // Make sure delay is positive
                    const safe_delay = if (delay < 0) 0 else delay;
                    pty_instance.render_timer = try loop.timeout(@as(u64, @intCast(safe_delay)) * std.time.ns_per_ms, .{
                        .ptr = pty_instance,
                        .cb = onRenderTimer,
                    });
                }

                // Re-arm only if still running
                if (pty_instance.running.load(.seq_cst)) {
                    _ = try loop.read(pty_instance.pipe_fds[0], &pty_instance.dirty_signal_buf, .{
                        .ptr = pty_instance,
                        .cb = onPtyDirty,
                    });
                }
            },
            .err => |err| {
                std.log.err("Pty dirty pipe error: {}", .{err});
            },
            else => {},
        }
    }

    fn handleProcessExit(self: *Server, loop: *io.Loop, pty_instance: *Pty) void {
        const status = pty_instance.exit_status.load(.seq_cst);
        log.info("PTY {} process exited with status {}", .{ pty_instance.id, status });

        // Send exit notification to clients
        self.sendPtyExited(pty_instance.id, status) catch |err| {
            std.log.err("Failed to send pty_exited: {}", .{err});
        };

        // Render final frame
        self.renderFrame(pty_instance);

        // Remove from server's pty map
        _ = self.ptys.fetchRemove(pty_instance.id);

        // Cancel pending IO and join read thread
        pty_instance.cancelPendingIO(loop);

        if (pty_instance.read_thread) |thread| {
            thread.join();
            pty_instance.read_thread = null;
        }

        // Free PTY resources
        pty_instance.process.close();
        posix.close(pty_instance.pipe_fds[0]);
        posix.close(pty_instance.pipe_fds[1]);
        posix.close(pty_instance.exit_pipe_fds[0]);
        posix.close(pty_instance.exit_pipe_fds[1]);
        pty_instance.terminal.deinit(self.allocator);
        pty_instance.render_state.deinit(self.allocator);
        pty_instance.clients.deinit(self.allocator);
        pty_instance.title.deinit(self.allocator);
        pty_instance.cwd.deinit(self.allocator);
        self.allocator.destroy(pty_instance);
    }
};

pub fn startServer(allocator: std.mem.Allocator, socket_path: []const u8) !void {
    std.log.info("Starting server on {s}", .{socket_path});

    var loop = try io.Loop.init(allocator);
    defer loop.deinit();

    // Check if socket exists and if a server is already running
    if (std.fs.accessAbsolute(socket_path, .{})) {
        // Socket exists - test if server is alive
        const test_fd = posix.socket(posix.AF.UNIX, posix.SOCK.STREAM | posix.SOCK.CLOEXEC, 0) catch |err| {
            std.log.err("Failed to create test socket: {}", .{err});
            return err;
        };
        defer posix.close(test_fd);

        var addr: posix.sockaddr.un = undefined;
        addr.family = posix.AF.UNIX;
        @memcpy(addr.path[0..socket_path.len], socket_path);
        addr.path[socket_path.len] = 0;

        if (posix.connect(test_fd, @ptrCast(&addr), @sizeOf(posix.sockaddr.un))) {
            // Connection succeeded - server is already running
            std.log.err("Server is already running on {s}", .{socket_path});
            return error.AddressInUse;
        } else |err| {
            if (err == error.ConnectionRefused or err == error.FileNotFound) {
                // Stale socket
                std.log.info("Removing stale socket", .{});
                posix.unlink(socket_path) catch {};
            } else {
                std.log.err("Failed to test socket: {}", .{err});
                return err;
            }
        }
    } else |err| {
        if (err != error.FileNotFound) return err;
        // Socket doesn't exist, continue
    }

    // Create socket
    const listen_fd = try posix.socket(posix.AF.UNIX, posix.SOCK.STREAM | posix.SOCK.CLOEXEC, 0);
    errdefer posix.close(listen_fd);

    // Bind to socket path
    var addr: posix.sockaddr.un = undefined;
    addr.family = posix.AF.UNIX;
    @memcpy(addr.path[0..socket_path.len], socket_path);
    addr.path[socket_path.len] = 0;

    try posix.bind(listen_fd, @ptrCast(&addr), @sizeOf(posix.sockaddr.un));

    // Listen
    try posix.listen(listen_fd, 128);

    // Create signal pipe
    const signal_pipe_fds = try posix.pipe2(.{ .NONBLOCK = true, .CLOEXEC = true });
    signal_write_fd = signal_pipe_fds[1];

    var sa: posix.Sigaction = .{
        .handler = .{ .handler = signalHandler },
        .mask = posix.sigemptyset(),
        .flags = 0,
    };
    posix.sigaction(posix.SIG.INT, &sa, null);
    posix.sigaction(posix.SIG.TERM, &sa, null);

    var server: Server = .{
        .allocator = allocator,
        .loop = &loop,
        .listen_fd = listen_fd,
        .socket_path = socket_path,
        .clients = std.ArrayList(*Client).empty,
        .ptys = std.AutoHashMap(usize, *Pty).init(allocator),
        .signal_pipe_fds = signal_pipe_fds,
        .start_time_ms = std.time.milliTimestamp(),
    };
    defer {
        posix.close(signal_pipe_fds[0]);
        posix.close(signal_pipe_fds[1]);
        for (server.clients.items) |client| {
            posix.close(client.fd);
            client.attached_ptys.deinit(allocator);
            // client.style_cache.deinit();
            allocator.destroy(client);
        }
        server.clients.deinit(allocator);
        server.ptys.deinit();
    }

    // Start accepting connections
    server.accept_task = try loop.accept(listen_fd, .{
        .ptr = &server,
        .cb = Server.onAccept,
    });

    // Register signal watcher
    _ = try loop.read(signal_pipe_fds[0], &server.signal_buf, .{
        .ptr = &server,
        .cb = Server.onSignal,
    });

    // Run until server decides to exit
    try loop.run(.until_done);

    // Block signals during thread cleanup to prevent EINTR interrupting joins
    var block_mask = posix.sigemptyset();
    posix.sigaddset(&block_mask, posix.SIG.INT);
    posix.sigaddset(&block_mask, posix.SIG.TERM);
    posix.sigprocmask(posix.SIG.BLOCK, &block_mask, null);

    // Join all PTY threads before exiting
    var it = server.ptys.valueIterator();
    while (it.next()) |pty_instance| {
        pty_instance.*.joinAndFree(allocator);
    }
    server.ptys.clearRetainingCapacity();

    // Cleanup
    posix.close(listen_fd);
    posix.unlink(socket_path) catch {};
}

test "server lifecycle - shutdown when no clients" {
    const testing = std.testing;

    var loop = try io.Loop.init(testing.allocator);
    defer loop.deinit();

    var server: Server = .{
        .allocator = testing.allocator,
        .loop = &loop,
        .listen_fd = 100,
        .socket_path = "/tmp/test.sock",
        .clients = std.ArrayList(*Client).empty,
        .ptys = std.AutoHashMap(usize, *Pty).init(testing.allocator),
        .exit_on_idle = true,
        .signal_pipe_fds = undefined,
    };
    defer server.clients.deinit(testing.allocator);
    defer server.ptys.deinit();

    server.accept_task = try loop.accept(100, .{
        .ptr = &server,
        .cb = Server.onAccept,
    });

    try testing.expect(server.accepting);
    try testing.expect(server.shouldExit());

    try server.checkExit();

    try testing.expect(!server.accepting);
    try testing.expect(server.accept_task == null);
}

test "server lifecycle - accept client connection" {
    const testing = std.testing;

    var loop = try io.Loop.init(testing.allocator);
    defer loop.deinit();

    var server: Server = .{
        .allocator = testing.allocator,
        .loop = &loop,
        .listen_fd = 100,
        .socket_path = "/tmp/test.sock",
        .clients = std.ArrayList(*Client).empty,
        .ptys = std.AutoHashMap(usize, *Pty).init(testing.allocator),
        .signal_pipe_fds = undefined,
    };
    defer {
        for (server.clients.items) |client| {
            testing.allocator.destroy(client);
        }
        server.clients.deinit(testing.allocator);
        server.ptys.deinit();
    }

    server.accept_task = try loop.accept(100, .{
        .ptr = &server,
        .cb = Server.onAccept,
    });

    try loop.completeAccept(100);
    try loop.run(.once);

    try testing.expectEqual(@as(usize, 1), server.clients.items.len);
    try testing.expect(server.accepting);
}

test "server lifecycle - client disconnect triggers shutdown" {
    const testing = std.testing;

    var loop = try io.Loop.init(testing.allocator);
    defer loop.deinit();

    var server: Server = .{
        .allocator = testing.allocator,
        .loop = &loop,
        .listen_fd = 100,
        .socket_path = "/tmp/test.sock",
        .clients = std.ArrayList(*Client).empty,
        .ptys = std.AutoHashMap(usize, *Pty).init(testing.allocator),
        .exit_on_idle = true,
        .signal_pipe_fds = undefined,
    };
    defer {
        for (server.clients.items) |client| {
            testing.allocator.destroy(client);
        }
        server.clients.deinit(testing.allocator);
        server.ptys.deinit();
    }

    server.accept_task = try loop.accept(100, .{
        .ptr = &server,
        .cb = Server.onAccept,
    });

    try loop.completeAccept(100);
    try loop.run(.once);

    try testing.expectEqual(@as(usize, 1), server.clients.items.len);
    const client_fd = server.clients.items[0].fd;

    try loop.completeRecv(client_fd, "");
    try loop.run(.once);

    try testing.expectEqual(@as(usize, 0), server.clients.items.len);
    try testing.expect(!server.accepting);
}

test "server lifecycle - multiple clients" {
    const testing = std.testing;

    var loop = try io.Loop.init(testing.allocator);
    defer loop.deinit();

    var server: Server = .{
        .allocator = testing.allocator,
        .loop = &loop,
        .listen_fd = 100,
        .socket_path = "/tmp/test.sock",
        .clients = std.ArrayList(*Client).empty,
        .ptys = std.AutoHashMap(usize, *Pty).init(testing.allocator),
        .exit_on_idle = true,
        .signal_pipe_fds = undefined,
    };
    defer {
        for (server.clients.items) |client| {
            testing.allocator.destroy(client);
        }
        server.clients.deinit(testing.allocator);
        server.ptys.deinit();
    }

    server.accept_task = try loop.accept(100, .{
        .ptr = &server,
        .cb = Server.onAccept,
    });

    try loop.completeAccept(100);
    try loop.run(.once);
    try testing.expectEqual(@as(usize, 1), server.clients.items.len);

    try loop.completeAccept(100);
    try loop.run(.once);
    try testing.expectEqual(@as(usize, 2), server.clients.items.len);

    try loop.completeAccept(100);
    try loop.run(.once);
    try testing.expectEqual(@as(usize, 3), server.clients.items.len);

    const client1_fd = server.clients.items[0].fd;
    const client2_fd = server.clients.items[1].fd;
    const client3_fd = server.clients.items[2].fd;

    try loop.completeRecv(client2_fd, "");
    try loop.run(.until_done);
    try testing.expectEqual(@as(usize, 2), server.clients.items.len);

    try loop.completeRecv(client1_fd, "");
    try loop.run(.until_done);
    try testing.expectEqual(@as(usize, 1), server.clients.items.len);

    try loop.completeRecv(client3_fd, "");
    try loop.run(.until_done);
    try testing.expectEqual(@as(usize, 0), server.clients.items.len);
    try testing.expect(!server.accepting);
}

test "server lifecycle - recv error triggers disconnect" {
    const testing = std.testing;

    var loop = try io.Loop.init(testing.allocator);
    defer loop.deinit();

    var server: Server = .{
        .allocator = testing.allocator,
        .loop = &loop,
        .listen_fd = 100,
        .socket_path = "/tmp/test.sock",
        .clients = std.ArrayList(*Client).empty,
        .ptys = std.AutoHashMap(usize, *Pty).init(testing.allocator),
        .exit_on_idle = true,
        .signal_pipe_fds = undefined,
    };
    defer {
        for (server.clients.items) |client| {
            testing.allocator.destroy(client);
        }
        server.clients.deinit(testing.allocator);
        server.ptys.deinit();
    }

    server.accept_task = try loop.accept(100, .{
        .ptr = &server,
        .cb = Server.onAccept,
    });

    try loop.completeAccept(100);
    try loop.run(.once);
    try testing.expectEqual(@as(usize, 1), server.clients.items.len);
    const client_fd = server.clients.items[0].fd;

    try loop.completeWithError(client_fd, error.ConnectionReset);
    try loop.run(.once);
    try testing.expectEqual(@as(usize, 0), server.clients.items.len);
    try testing.expect(!server.accepting);
}

test "parseSpawnPtyParams" {
    const testing = std.testing;

    // Empty params - defaults
    const p1 = Server.parseSpawnPtyParams(.{ .map = &.{} });
    try testing.expectEqual(@as(u16, 24), p1.size.ws_row);
    try testing.expectEqual(@as(u16, 80), p1.size.ws_col);
    try testing.expectEqual(false, p1.attach);
    try testing.expectEqual(@as(?[]const u8, null), p1.cwd);

    // Full params
    var params = [_]msgpack.Value.KeyValue{
        .{ .key = .{ .string = "rows" }, .value = .{ .unsigned = 40 } },
        .{ .key = .{ .string = "cols" }, .value = .{ .unsigned = 100 } },
        .{ .key = .{ .string = "attach" }, .value = .{ .boolean = true } },
    };
    const p2 = Server.parseSpawnPtyParams(.{ .map = &params });
    try testing.expectEqual(@as(u16, 40), p2.size.ws_row);
    try testing.expectEqual(@as(u16, 100), p2.size.ws_col);
    try testing.expectEqual(true, p2.attach);
    try testing.expectEqual(@as(?[]const u8, null), p2.cwd);

    // With cwd param
    var params_with_cwd = [_]msgpack.Value.KeyValue{
        .{ .key = .{ .string = "rows" }, .value = .{ .unsigned = 30 } },
        .{ .key = .{ .string = "cols" }, .value = .{ .unsigned = 120 } },
        .{ .key = .{ .string = "cwd" }, .value = .{ .string = "/tmp" } },
    };
    const p3 = Server.parseSpawnPtyParams(.{ .map = &params_with_cwd });
    try testing.expectEqual(@as(u16, 30), p3.size.ws_row);
    try testing.expectEqual(@as(u16, 120), p3.size.ws_col);
    try testing.expectEqualStrings("/tmp", p3.cwd.?);
}

test "prepareSpawnEnv" {
    const testing = std.testing;
    var env_map = std.process.EnvMap.init(testing.allocator);
    defer env_map.deinit();

    try env_map.put("EXISTING", "value");

    var list = try Server.prepareSpawnEnv(testing.allocator, &env_map);
    defer {
        for (list.items) |item| testing.allocator.free(item);
        list.deinit(testing.allocator);
    }

    var found_term = false;
    var found_colorterm = false;
    var found_existing = false;

    for (list.items) |item| {
        if (std.mem.startsWith(u8, item, "TERM=")) found_term = true;
        if (std.mem.startsWith(u8, item, "COLORTERM=")) found_colorterm = true;
        if (std.mem.startsWith(u8, item, "EXISTING=")) found_existing = true;
    }

    try testing.expect(found_term);
    try testing.expect(found_colorterm);
    try testing.expect(found_existing);
}

test "parseAttachPtyParams" {
    const testing = std.testing;

    var valid_args = [_]msgpack.Value{.{ .unsigned = 42 }};
    const result = try Server.parseAttachPtyParams(.{ .array = &valid_args });
    try testing.expectEqual(@as(usize, 42), result.pty_id);
    try testing.expectEqual(key_encode.OptionAsAlt.false, result.macos_option_as_alt);

    var valid_args_with_opt = [_]msgpack.Value{ .{ .unsigned = 42 }, .{ .string = "left" } };
    const result2 = try Server.parseAttachPtyParams(.{ .array = &valid_args_with_opt });
    try testing.expectEqual(@as(usize, 42), result2.pty_id);
    try testing.expectEqual(key_encode.OptionAsAlt.left, result2.macos_option_as_alt);

    var invalid_args = [_]msgpack.Value{};
    try testing.expectError(error.InvalidParams, Server.parseAttachPtyParams(.{ .array = &invalid_args }));
}

test "parseWritePtyParams" {
    const testing = std.testing;

    var valid_args = [_]msgpack.Value{
        .{ .unsigned = 42 },
        .{ .binary = "hello" },
    };
    const args = try Server.parseWritePtyParams(.{ .array = &valid_args });
    try testing.expectEqual(@as(usize, 42), args.id);
    try testing.expectEqualStrings("hello", args.data);
}

test "parseResizePtyParams" {
    const testing = std.testing;

    var valid_args = [_]msgpack.Value{
        .{ .unsigned = 42 },
        .{ .unsigned = 50 },
        .{ .unsigned = 80 },
    };
    const args = try Server.parseResizePtyParams(.{ .array = &valid_args });
    try testing.expectEqual(@as(usize, 42), args.id);
    try testing.expectEqual(@as(u16, 50), args.rows);
    try testing.expectEqual(@as(u16, 80), args.cols);
    try testing.expectEqual(@as(u16, 0), args.x_pixel);
    try testing.expectEqual(@as(u16, 0), args.y_pixel);

    var pixel_args = [_]msgpack.Value{
        .{ .unsigned = 42 },
        .{ .unsigned = 50 },
        .{ .unsigned = 80 },
        .{ .unsigned = 800 },
        .{ .unsigned = 600 },
    };
    const p_args = try Server.parseResizePtyParams(.{ .array = &pixel_args });
    try testing.expectEqual(@as(usize, 42), p_args.id);
    try testing.expectEqual(@as(u16, 50), p_args.rows);
    try testing.expectEqual(@as(u16, 80), p_args.cols);
    try testing.expectEqual(@as(u16, 800), p_args.x_pixel);
    try testing.expectEqual(@as(u16, 600), p_args.y_pixel);
}

test "buildRedrawMessageFromPty" {
    const testing = std.testing;
    const allocator = testing.allocator;

    var pty_inst: Pty = .{
        .id = 1,
        .process = .{ .master = -1, .slave = -1, .pid = 0 },
        .clients = std.ArrayList(*Client).empty,
        .running = std.atomic.Value(bool).init(true),
        .terminal = try ghostty_vt.Terminal.init(allocator, .{ .cols = 80, .rows = 24 }),
        .allocator = allocator,
        .title = std.ArrayList(u8).empty,
        .title_dirty = false,
        .cwd = std.ArrayList(u8).empty,
        .cwd_dirty = false,
        .pipe_fds = undefined,
        .exit_pipe_fds = undefined,
        .render_state = .empty,
        .server_ptr = undefined,
    };
    defer {
        pty_inst.terminal.deinit(allocator);
        pty_inst.render_state.deinit(allocator);
        pty_inst.clients.deinit(allocator);
        pty_inst.title.deinit(allocator);
        pty_inst.cwd.deinit(allocator);
    }

    const msg = try buildRedrawMessageFromPty(allocator, &pty_inst, .full);
    defer allocator.free(msg);

    try testing.expect(msg.len > 0);

    const value = try msgpack.decode(allocator, msg);
    defer value.deinit(allocator);
    try testing.expect(value == .array);
}

test "style optimization" {
    const testing = std.testing;
    const allocator = testing.allocator;

    var pty_inst: Pty = .{
        .id = 1,
        .process = .{ .master = -1, .slave = -1, .pid = 0 },
        .clients = std.ArrayList(*Client).empty,
        .running = std.atomic.Value(bool).init(true),
        .terminal = try ghostty_vt.Terminal.init(allocator, .{ .cols = 10, .rows = 5 }),
        .allocator = allocator,
        .title = std.ArrayList(u8).empty,
        .title_dirty = false,
        .cwd = std.ArrayList(u8).empty,
        .cwd_dirty = false,
        .pipe_fds = undefined,
        .exit_pipe_fds = undefined,
        .render_state = .empty,
        .server_ptr = undefined,
    };
    defer {
        pty_inst.terminal.deinit(allocator);
        pty_inst.render_state.deinit(allocator);
        pty_inst.clients.deinit(allocator);
        pty_inst.title.deinit(allocator);
        pty_inst.cwd.deinit(allocator);
    }

    const handler = vt_handler.Handler.init(&pty_inst.terminal);
    var stream = vt_handler.Stream.initAlloc(allocator, handler);
    defer stream.deinit();

    // Row 0: A (Default), B (Red), C (Default)
    stream.nextSlice(&[_]u8{'A'});
    stream.nextSlice("\x1b[31m");
    stream.nextSlice(&[_]u8{'B'});
    stream.nextSlice("\x1b[0m");
    stream.nextSlice(&[_]u8{'C'});

    // Newline to start Row 1
    stream.nextSlice("\r\n");

    // D (Red) - testing switching from Default (C) to Red (D) across rows/cells
    stream.nextSlice("\x1b[31m");
    stream.nextSlice(&[_]u8{'D'});

    const msg = try buildRedrawMessageFromPty(allocator, &pty_inst, .full);
    defer allocator.free(msg);

    const value = try msgpack.decode(allocator, msg);
    defer value.deinit(allocator);

    // Verify results by inspecting msgpack events
    const events = value.array[2].array;
    var style_def_red: ?u32 = null;
    var found_row0 = false;
    var found_row1 = false;

    for (events) |evt_val| {
        const name = evt_val.array[0].string;
        const args = evt_val.array[1].array;

        if (std.mem.eql(u8, name, "style")) {
            const id = @as(u32, @intCast(args[0].unsigned));
            const attrs_map = args[1].map;
            for (attrs_map) |kv| {
                if (std.mem.eql(u8, kv.key.string, "fg") and kv.value.unsigned == 0xFF0000) {
                    style_def_red = id;
                }
                if (std.mem.eql(u8, kv.key.string, "fg_idx") and kv.value.unsigned == 1) {
                    style_def_red = id;
                }
            }
        } else if (std.mem.eql(u8, name, "write")) {
            const row = @as(u16, @intCast(args[1].unsigned));
            const cells_arr = args[3].array;

            if (row == 0) {
                found_row0 = true;
                // Cell 1 should be B with red style
                const cell1 = cells_arr[1].array;
                try testing.expectEqualStrings("B", cell1[0].string);
                if (cell1.len > 1 and cell1[1] != .nil) {
                    const sid = @as(u32, @intCast(cell1[1].unsigned));
                    try testing.expectEqual(style_def_red.?, sid);
                }
            }
            if (row == 1) {
                found_row1 = true;
                // Cell 0 should be D with red style
                const cell0 = cells_arr[0].array;
                try testing.expectEqualStrings("D", cell0[0].string);
                if (cell0.len > 1 and cell0[1] != .nil) {
                    const sid = @as(u32, @intCast(cell0[1].unsigned));
                    try testing.expectEqual(style_def_red.?, sid);
                }
            }
        }
    }

    try testing.expect(style_def_red != null);
    try testing.expect(found_row0);
    try testing.expect(found_row1);
}

test "server - pty exit notification" {
    const testing = std.testing;
    const allocator = testing.allocator;

    var loop = try io.Loop.init(allocator);
    defer loop.deinit();

    var server: Server = .{
        .allocator = allocator,
        .loop = &loop,
        .listen_fd = 100,
        .socket_path = "/tmp/test.sock",
        .clients = std.ArrayList(*Client).empty,
        .ptys = std.AutoHashMap(usize, *Pty).init(allocator),
        .signal_pipe_fds = undefined,
    };
    defer {
        // cleanup
        for (server.clients.items) |client| {
            if (client.send_buffer) |buf| allocator.free(buf);
            for (client.send_queue.items) |buf| allocator.free(buf);
            client.send_queue.deinit(allocator);
            client.attached_ptys.deinit(allocator);
            allocator.destroy(client);
        }
        server.clients.deinit(allocator);
        // PTY is now cleaned up by onPtyDirty when it exits, so just deinit the map
        server.ptys.deinit();
    }

    // Add a client
    const client = try allocator.create(Client);
    client.* = .{
        .fd = 200,
        .server = &server,
        .msg_buffer = std.ArrayList(u8).empty,
        .send_queue = std.ArrayList([]u8).empty,
        .attached_ptys = std.ArrayList(usize).empty,
    };
    try server.clients.append(allocator, client);

    // Create a dummy Pty
    const pipe_fds = try posix.pipe2(.{ .NONBLOCK = true, .CLOEXEC = true });
    const exit_pipe_fds = try posix.pipe2(.{ .NONBLOCK = true, .CLOEXEC = true });
    const pty_inst = try allocator.create(Pty);
    pty_inst.* = .{
        .id = 1,
        .process = .{ .master = -1, .slave = -1, .pid = 0 },
        .clients = std.ArrayList(*Client).empty,
        .running = std.atomic.Value(bool).init(true),
        .terminal = try ghostty_vt.Terminal.init(allocator, .{ .cols = 80, .rows = 24 }),
        .allocator = allocator,
        .title = std.ArrayList(u8).empty,
        .title_dirty = false,
        .cwd = std.ArrayList(u8).empty,
        .cwd_dirty = false,
        .pipe_fds = pipe_fds,
        .exit_pipe_fds = exit_pipe_fds,
        .render_state = .empty,
        .server_ptr = &server,
        .exited = std.atomic.Value(bool).init(false),
        .exit_status = std.atomic.Value(u32).init(0),
    };

    try server.ptys.put(1, pty_inst);

    // Register dirty pipe read (like in spawn_pty)
    _ = try loop.read(pty_inst.pipe_fds[0], &pty_inst.dirty_signal_buf, .{
        .ptr = pty_inst,
        .cb = Server.onPtyDirty,
    });

    // Simulate process exit: set status and send "e" signal through pipe
    pty_inst.exit_status.store(123, .seq_cst);
    pty_inst.exited.store(true, .seq_cst);

    // Complete the read with "e" signal
    try loop.completeRead(pty_inst.pipe_fds[0], "e");

    // Run loop to process onPtyDirty -> handleProcessExit
    try loop.run(.once);

    // Check pending sends
    var found_send = false;
    var it = loop.pending.iterator();
    while (it.next()) |entry| {
        if (entry.value_ptr.kind == .send and entry.value_ptr.fd == 200) {
            found_send = true;

            // Verify content
            const msg = try rpc.decodeMessage(allocator, entry.value_ptr.buf);
            defer msg.deinit(allocator);

            try testing.expect(msg == .notification);
            try testing.expectEqualStrings("pty_exited", msg.notification.method);
            try testing.expectEqual(@as(usize, 2), msg.notification.params.array.len);
            try testing.expectEqual(@as(u64, 1), msg.notification.params.array[0].unsigned);
            try testing.expectEqual(@as(u64, 123), msg.notification.params.array[1].unsigned);
        }
    }
    try testing.expect(found_send);
}
