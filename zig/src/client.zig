//! Client-side networking and application state management.

const std = @import("std");
const vaxis = @import("vaxis");
const io = @import("io.zig");
const layout_mod = @import("layout.zig");
const Layout = layout_mod.Layout;
const msgpack = @import("msgpack.zig");
const redraw = @import("redraw.zig");
const rpc = @import("rpc.zig");
const Surface = @import("Surface.zig");


const vaxis_helper = @import("vaxis_helper.zig");
const widget = @import("widget.zig");
const posix = std.posix;

const log = std.log.scoped(.client);

const MAX_PASTE_SIZE = 10 * 1024 * 1024; // 10 MiB

/// Returns true if the env var should be filtered out when forwarding to the PTY.
/// Terminal-specific vars (TERM, COLORTERM, KITTY_*, TERMINFO*) conflict with ghostty-vt.
fn isFilteredEnvVar(key: []const u8) bool {
    return std.mem.eql(u8, key, "TERM") or
        std.mem.eql(u8, key, "COLORTERM") or
        std.mem.startsWith(u8, key, "KITTY_") or
        std.mem.eql(u8, key, "TERMINFO") or
        std.mem.eql(u8, key, "TERMINFO_DIRS");
}

pub const MsgId = enum(u16) {
    spawn_pty = 1,
    attach_pty = 2,
};

/// Map redraw MouseShape to vaxis Mouse.Shape
fn mapMouseShapeToVaxis(shape: redraw.UIEvent.MouseShape.Shape) vaxis.Mouse.Shape {
    return switch (shape) {
        .default => .default,
        .text => .text,
        .pointer => .pointer,
        .help => .help,
        .progress => .progress,
        .wait => .wait,
        .cell => .cell,
        .ew_resize, .col_resize => .@"ew-resize",
        .ns_resize, .row_resize => .@"ns-resize",
        // vaxis has limited shapes, map others to closest match
        .crosshair,
        .move,
        .not_allowed,
        .grab,
        .grabbing,
        .nesw_resize,
        .nwse_resize,
        .all_scroll,
        .zoom_in,
        .zoom_out,
        => .default,
    };
}

pub const UnixSocketClient = struct {
    allocator: std.mem.Allocator,
    fd: ?posix.fd_t = null,
    ctx: io.Context,
    addr: posix.sockaddr.un,

    const Msg = enum {
        socket,
        connect,
    };

    fn handleMsg(loop: *io.Loop, completion: io.Completion) anyerror!void {
        const self = completion.userdataCast(UnixSocketClient);

        switch (completion.msgToEnum(Msg)) {
            .socket => {
                switch (completion.result) {
                    .socket => |fd| {
                        self.fd = fd;
                        _ = try loop.connect(
                            fd,
                            @ptrCast(&self.addr),
                            @sizeOf(posix.sockaddr.un),
                            .{
                                .ptr = self,
                                .msg = @intFromEnum(Msg.connect),
                                .cb = UnixSocketClient.handleMsg,
                            },
                        );
                    },
                    .err => |err| {
                        defer self.allocator.destroy(self);
                        try self.ctx.cb(loop, .{
                            .userdata = self.ctx.ptr,
                            .msg = self.ctx.msg,
                            .callback = self.ctx.cb,
                            .result = .{ .err = err },
                        });
                    },
                    else => unreachable,
                }
            },

            .connect => {
                defer self.allocator.destroy(self);

                switch (completion.result) {
                    .connect => {
                        try self.ctx.cb(loop, .{
                            .userdata = self.ctx.ptr,
                            .msg = self.ctx.msg,
                            .callback = self.ctx.cb,
                            .result = .{ .socket = self.fd.? },
                        });
                    },
                    .err => |err| {
                        try self.ctx.cb(loop, .{
                            .userdata = self.ctx.ptr,
                            .msg = self.ctx.msg,
                            .callback = self.ctx.cb,
                            .result = .{ .err = err },
                        });
                        if (self.fd) |fd| {
                            _ = try loop.close(fd, .{
                                .ptr = null,
                                .cb = struct {
                                    fn noop(_: *io.Loop, _: io.Completion) anyerror!void {}
                                }.noop,
                            });
                        }
                    },
                    else => unreachable,
                }
            },
        }
    }
};

pub fn connectUnixSocket(
    loop: *io.Loop,
    socket_path: []const u8,
    ctx: io.Context,
) !*UnixSocketClient {
    // Precondition: socket path must fit in sockaddr_un.path (typically 104 bytes on macOS)
    std.debug.assert(socket_path.len < 104);
    // Precondition: socket path must not be empty
    std.debug.assert(socket_path.len > 0);

    const client = try loop.allocator.create(UnixSocketClient);
    errdefer loop.allocator.destroy(client);

    var addr: posix.sockaddr.un = undefined;
    addr.family = posix.AF.UNIX;
    @memcpy(addr.path[0..socket_path.len], socket_path);
    addr.path[socket_path.len] = 0;

    client.* = .{
        .allocator = loop.allocator,
        .ctx = ctx,
        .addr = addr,
        .fd = null,
    };

    _ = try loop.socket(
        posix.AF.UNIX,
        posix.SOCK.STREAM | posix.SOCK.CLOEXEC | posix.SOCK.NONBLOCK,
        0,
        .{
            .ptr = client,
            .msg = @intFromEnum(UnixSocketClient.Msg.socket),
            .cb = UnixSocketClient.handleMsg,
        },
    );

    return client;
}

pub const ClientState = struct {
    pty_id: ?i64 = null,
    response_received: bool = false,
    attached: bool = false,
    should_quit: bool = false,
    connection_refused: bool = false,
    next_msgid: u32 = 1,
    pending_requests: std.AutoHashMap(u32, RequestInfo),
    cwd_map: std.AutoHashMap(i64, []const u8),
    allocator: std.mem.Allocator,
    prefix_mode: bool = false,
    pty_validity: ?i64 = null,

    pub const RequestInfo = union(enum) {
        spawn: struct { cwd: ?[]const u8 = null, cmd: ?[]const u8 = null, old_pty_id: ?u32 = null },
        attach: struct { pty_id: i64, cwd: ?[]const u8 = null },
        detach,
        get_server_info,
        copy_selection,
    };

    pub fn init(allocator: std.mem.Allocator) ClientState {
        return .{
            .allocator = allocator,
            .pending_requests = std.AutoHashMap(u32, RequestInfo).init(allocator),
            .cwd_map = std.AutoHashMap(i64, []const u8).init(allocator),
        };
    }

    pub fn deinit(self: *ClientState) void {
        self.pending_requests.deinit();
        var it = self.cwd_map.valueIterator();
        while (it.next()) |val| {
            self.allocator.free(val.*);
        }
        self.cwd_map.deinit();
    }
};

pub const ServerAction = union(enum) {
    none,
    send_attach: i64,
    spawn_pty_with_cwd: struct { cwd: ?[]const u8 },
    redraw: msgpack.Value,
    attached: struct { new_pty_id: i64, old_pty_id: ?u32 = null },
    pty_exited: struct { pty_id: u32, status: u32 },
    cwd_changed: struct { pty_id: u32, cwd: []const u8 },
    detached,
    color_query: ColorQueryTarget,
    server_info: struct { pty_validity: i64 },
    copy_to_clipboard: []const u8,

    pub const ColorQueryTarget = struct {
        pty_id: u32,
        target: Target,
        slot: usize, // Response slot for ordered delivery

        pub const Target = union(enum) {
            palette: u8,
            foreground,
            background,
            cursor,
        };
    };
};

pub const PipeAction = union(enum) {
    none,
    send_key: msgpack.Value, // key map
    send_resize: struct { rows: u16, cols: u16 },
    quit,
};

pub const ClientLogic = struct {
    pub fn processServerMessage(state: *ClientState, msg: rpc.Message) !ServerAction {
        return switch (msg) {
            .response => |resp| processResponse(state, resp),
            .request => {
                log.warn("Got unexpected request from server", .{});
                return .none;
            },
            .notification => |notif| try processNotification(state, notif),
        };
    }

    fn processResponse(state: *ClientState, resp: rpc.Response) ServerAction {
        state.response_received = true;
        log.info("processServerMessage: response msgid={}", .{resp.msgid});
        const request_info = state.pending_requests.fetchRemove(resp.msgid);

        if (resp.err) |err_val| {
            return handleResponseError(err_val, request_info);
        }
        return handleResponseResult(state, resp.result, request_info);
    }

    fn handleResponseError(err_val: msgpack.Value, request_info: ?std.AutoHashMap(u32, ClientState.RequestInfo).KV) ServerAction {
        if (request_info) |entry| {
            if (entry.value == .attach) {
                const attach_info = entry.value.attach;
                if (err_val == .string and std.mem.eql(u8, err_val.string, "PTY not found")) {
                    log.info("PTY {} not found, spawning new PTY with cwd", .{attach_info.pty_id});
                    return .{ .spawn_pty_with_cwd = .{ .cwd = attach_info.cwd } };
                }
            }
        }
        log.err("Error in response: {}", .{err_val});
        return .none;
    }

    fn handleResponseResult(state: *ClientState, result: msgpack.Value, request_info: ?std.AutoHashMap(u32, ClientState.RequestInfo).KV) ServerAction {
        if (request_info) |entry| {
            log.info("processServerMessage: found pending request: {s}", .{@tagName(entry.value)});
            return switch (entry.value) {
                .spawn => |spawn_info| handleSpawnResult(state, result, spawn_info.cwd, spawn_info.old_pty_id),
                .attach => |attach_info| {
                    state.pty_id = attach_info.pty_id;
                    state.attached = true;
                    if (attach_info.cwd) |c| {
                        const owned_cwd = state.allocator.dupe(u8, c) catch return .{ .attached = .{ .new_pty_id = attach_info.pty_id } };
                        state.cwd_map.put(attach_info.pty_id, owned_cwd) catch {
                            state.allocator.free(owned_cwd);
                        };
                    }
                    return .{ .attached = .{ .new_pty_id = attach_info.pty_id } };
                },
                .detach => .detached,
                .get_server_info => handleServerInfoResult(state, result),
                .copy_selection => handleCopySelectionResult(result),
            };
        }
        return handleUnsolicitedResult(state, result);
    }

    fn handleCopySelectionResult(result: msgpack.Value) ServerAction {
        if (result == .string) {
            log.info("handleCopySelectionResult: received selection text ({} bytes)", .{result.string.len});
            return .{ .copy_to_clipboard = result.string };
        }
        if (result == .nil) {
            log.warn("handleCopySelectionResult: no selection (nil result)", .{});
        } else {
            log.warn("handleCopySelectionResult: unexpected result type: {s}", .{@tagName(result)});
        }
        return .none;
    }

    fn handleServerInfoResult(state: *ClientState, result: msgpack.Value) ServerAction {
        if (result != .map) return .none;

        for (result.map) |kv| {
            if (kv.key == .string and std.mem.eql(u8, kv.key.string, "pty_validity")) {
                const validity: i64 = switch (kv.value) {
                    .integer => |i| i,
                    .unsigned => |u| @intCast(u),
                    else => continue,
                };
                state.pty_validity = validity;
                log.info("Got pty_validity: {}", .{validity});
                return .{ .server_info = .{ .pty_validity = validity } };
            }
        }
        return .none;
    }

    fn handleSpawnResult(state: *ClientState, result: msgpack.Value, cwd: ?[]const u8, old_pty_id: ?u32) ServerAction {
        const id: i64 = switch (result) {
            .integer => |i| i,
            .unsigned => |u| @intCast(u),
            else => -1,
        };
        log.info("processServerMessage: spawn result id={}, old_pty_id={?}", .{ id, old_pty_id });
        if (id >= 0) {
            state.pty_id = id;
            state.attached = true;
            if (cwd) |c| {
                const owned_cwd = state.allocator.dupe(u8, c) catch return .{ .attached = .{ .new_pty_id = id, .old_pty_id = old_pty_id } };
                state.cwd_map.put(id, owned_cwd) catch {
                    state.allocator.free(owned_cwd);
                };
            }
            return .{ .attached = .{ .new_pty_id = id, .old_pty_id = old_pty_id } };
        }
        return .none;
    }

    fn handleUnsolicitedResult(state: *ClientState, result: msgpack.Value) ServerAction {
        return switch (result) {
            .integer => |i| {
                if (state.pty_id == null) {
                    state.pty_id = i;
                    return .{ .send_attach = i };
                } else if (!state.attached) {
                    state.attached = true;
                    return .{ .attached = .{ .new_pty_id = i } };
                }
                return .none;
            },
            .unsigned => |u| {
                if (state.pty_id == null) {
                    state.pty_id = @intCast(u);
                    state.attached = true;
                    return .{ .attached = .{ .new_pty_id = @intCast(u) } };
                } else if (!state.attached) {
                    state.attached = true;
                    return .{ .attached = .{ .new_pty_id = @intCast(u) } };
                }
                return .none;
            },
            .string => |s| {
                log.info("{s}", .{s});
                return .none;
            },
            .nil => return .none,
            else => {
                log.info("Unknown result type: {}", .{result});
                return .none;
            },
        };
    }

    fn processNotification(state: *ClientState, notif: rpc.Notification) !ServerAction {
        if (std.mem.eql(u8, notif.method, "redraw")) {
            return .{ .redraw = notif.params };
        } else if (std.mem.eql(u8, notif.method, "pty_exited")) {
            return parsePtyExited(notif.params);
        } else if (std.mem.eql(u8, notif.method, "cwd_changed")) {
            return try handleCwdChanged(state, notif.params);
        } else if (std.mem.eql(u8, notif.method, "color_query")) {
            return parseColorQuery(notif.params);
        }
        return .none;
    }

    fn parseColorQuery(params: msgpack.Value) ServerAction {
        if (params != .map) return .none;

        var pty_id: ?u32 = null;
        var index: ?u8 = null;
        var kind: ?[]const u8 = null;
        var slot: ?usize = null;

        for (params.map) |kv| {
            if (kv.key != .string) continue;
            if (std.mem.eql(u8, kv.key.string, "pty_id")) {
                pty_id = switch (kv.value) {
                    .integer => |i| @intCast(i),
                    .unsigned => |u| @intCast(u),
                    else => null,
                };
            } else if (std.mem.eql(u8, kv.key.string, "index")) {
                index = switch (kv.value) {
                    .integer => |i| @intCast(i),
                    .unsigned => |u| @intCast(u),
                    else => null,
                };
            } else if (std.mem.eql(u8, kv.key.string, "kind")) {
                kind = if (kv.value == .string) kv.value.string else null;
            } else if (std.mem.eql(u8, kv.key.string, "slot")) {
                slot = switch (kv.value) {
                    .integer => |i| @intCast(i),
                    .unsigned => |u| @intCast(u),
                    else => null,
                };
            }
        }

        const pid = pty_id orelse return .none;
        const response_slot = slot orelse return .none;

        if (index) |idx| {
            return .{ .color_query = .{ .pty_id = pid, .target = .{ .palette = idx }, .slot = response_slot } };
        } else if (kind) |k| {
            const target: ServerAction.ColorQueryTarget.Target = if (std.mem.eql(u8, k, "foreground"))
                .foreground
            else if (std.mem.eql(u8, k, "background"))
                .background
            else if (std.mem.eql(u8, k, "cursor"))
                .cursor
            else
                return .none;
            return .{ .color_query = .{ .pty_id = pid, .target = target, .slot = response_slot } };
        }

        return .none;
    }

    fn parsePtyExited(params: msgpack.Value) ServerAction {
        if (params != .array or params.array.len < 2) return .none;

        const pty_id = switch (params.array[0]) {
            .integer => |i| @as(u32, @intCast(i)),
            .unsigned => |u| @as(u32, @intCast(u)),
            else => 0,
        };
        const status = switch (params.array[1]) {
            .integer => |i| @as(u32, @intCast(i)),
            .unsigned => |u| @as(u32, @intCast(u)),
            else => 0,
        };
        return .{ .pty_exited = .{ .pty_id = pty_id, .status = status } };
    }

    fn handleCwdChanged(state: *ClientState, params: msgpack.Value) !ServerAction {
        if (params != .map) return .none;

        var pty_id_val: ?msgpack.Value = null;
        var cwd_val: ?msgpack.Value = null;
        for (params.map) |kv| {
            if (kv.key != .string) continue;
            if (std.mem.eql(u8, kv.key.string, "pty_id")) {
                pty_id_val = kv.value;
            } else if (std.mem.eql(u8, kv.key.string, "cwd")) {
                cwd_val = kv.value;
            }
        }

        const pty_id = switch (pty_id_val orelse return .none) {
            .integer => |i| @as(i64, @intCast(i)),
            .unsigned => |u| @as(i64, @intCast(u)),
            else => return .none,
        };

        const cwd = if (cwd_val) |v| (if (v == .string) v.string else null) else return .none;
        const cwd_str = cwd orelse return .none;

        if (state.cwd_map.getPtr(pty_id)) |entry| {
            state.allocator.free(entry.*);
        }
        const owned_cwd = try state.allocator.dupe(u8, cwd_str);
        try state.cwd_map.put(pty_id, owned_cwd);

        return .{ .cwd_changed = .{ .pty_id = @intCast(pty_id), .cwd = cwd_str } };
    }

    pub fn processPipeMessage(state: *ClientState, value: msgpack.Value) !PipeAction {
        if (value != .array or value.array.len < 1) return .none;

        const msg_type = value.array[0];
        if (msg_type != .string) return .none;

        if (std.mem.eql(u8, msg_type.string, "key")) {
            if (value.array.len < 2 or value.array[1] != .map) return .none;
            const key_map = value.array[1];
            if (state.attached and state.pty_id != null) {
                return .{ .send_key = key_map };
            }
        } else if (std.mem.eql(u8, msg_type.string, "resize")) {
            if (value.array.len < 3) return .none;
            const rows = switch (value.array[1]) {
                .unsigned => |u| @as(u16, @intCast(u)),
                .integer => |i| @as(u16, @intCast(i)),
                else => return .none,
            };
            const cols = switch (value.array[2]) {
                .unsigned => |u| @as(u16, @intCast(u)),
                .integer => |i| @as(u16, @intCast(i)),
                else => return .none,
            };
            return .{ .send_resize = .{ .rows = rows, .cols = cols } };
        } else if (std.mem.eql(u8, msg_type.string, "quit")) {
            state.should_quit = true;
            return .quit;
        }
        return .none;
    }

    pub fn shouldFlush(params: msgpack.Value) bool {
        if (params != .array) return false;
        for (params.array) |event_val| {
            if (event_val != .array or event_val.array.len < 1) continue;
            const event_name = event_val.array[0];
            switch (event_name) {
                .string => |s| {
                    if (std.mem.eql(u8, s, "flush")) return true;
                },
                else => {},
            }
        }
        return false;
    }

    pub fn encodeEvent(allocator: std.mem.Allocator, event: vaxis.Event) !?[]u8 {
        switch (event) {
            .key_press, .key_release => |key| {
                const event_name: []const u8 = if (event == .key_press) "key" else "key_release";

                // Build key map in W3C KeyboardEvent format
                const key_strs = try vaxis_helper.vaxisKeyToStrings(allocator, key);
                defer allocator.free(key_strs.key);
                defer allocator.free(key_strs.code);

                var key_map_kv = try allocator.alloc(msgpack.Value.KeyValue, 6);
                key_map_kv[0] = .{ .key = .{ .string = "key" }, .value = .{ .string = key_strs.key } };
                key_map_kv[1] = .{ .key = .{ .string = "code" }, .value = .{ .string = key_strs.code } };
                key_map_kv[2] = .{ .key = .{ .string = "shiftKey" }, .value = .{ .boolean = key.mods.shift } };
                key_map_kv[3] = .{ .key = .{ .string = "ctrlKey" }, .value = .{ .boolean = key.mods.ctrl } };
                key_map_kv[4] = .{ .key = .{ .string = "altKey" }, .value = .{ .boolean = key.mods.alt } };
                key_map_kv[5] = .{ .key = .{ .string = "metaKey" }, .value = .{ .boolean = key.mods.super } };

                const key_map_val: msgpack.Value = .{ .map = key_map_kv };
                var arr = try allocator.alloc(msgpack.Value, 2);
                arr[0] = .{ .string = event_name };
                arr[1] = key_map_val;

                const result = try msgpack.encodeFromValue(allocator, .{ .array = arr });

                // Clean up temporary allocations
                allocator.free(arr);
                allocator.free(key_map_kv);

                return result;
            },
            .winsize => |ws| {
                return try msgpack.encode(allocator, .{ "resize", ws.rows, ws.cols });
            },
            else => return null,
        }
    }

    pub fn vaxisKeyToString(allocator: std.mem.Allocator, key: vaxis.Key) ![]u8 {
        return vaxis_helper.vaxisKeyToString(allocator, key);
    }
};

pub const DragState = struct {
    handle: widget.SplitHandle,
    start_x: f64,
    start_y: f64,
};

pub const App = struct {
    connected: bool = false,
    fd: posix.fd_t = undefined,
    allocator: std.mem.Allocator,
    recv_buffer: [4096]u8 = undefined,
    msg_buffer: std.ArrayList(u8),
    msg_arena: std.heap.ArenaAllocator,
    send_buffer: ?[]u8 = null,
    send_task: ?io.Task = null,
    recv_task: ?io.Task = null,
    pipe_read_task: ?io.Task = null,
    vx: vaxis.Vaxis = undefined,
    tty: vaxis.Tty = undefined,
    tty_thread: ?std.Thread = null,
    io_loop: ?*io.Loop = null,
    tty_buffer: [4096]u8 = undefined,
    surfaces: std.AutoHashMap(u32, *Surface),
    state: ClientState,
    ui: Layout = undefined,
    first_resize_done: bool = false,
    socket_path: []const u8 = undefined,
    /// Session name to attach to (existing session passed via `tuios session attach <name>`)
    attach_session: ?[]const u8 = null,
    /// User-specified session name for new session (passed via `tuios -s <name>`)
    new_session_name: ?[]const u8 = null,
    initial_cwd: ?[]const u8 = null,
    last_render_time: i64 = 0,
    render_timer: ?io.Task = null,

    // Terminal metrics
    cell_width_px: u16 = 0,
    cell_height_px: u16 = 0,

    // Hit regions for mouse event targeting
    hit_regions: []widget.HitRegion = &.{},
    split_handles: []widget.SplitHandle = &.{},

    // Drag state for split resizing
    drag_state: ?DragState = null,

    // Selection drag state: tracks PTY where left-button selection started
    // to allow scrolling while selecting beyond viewport bounds
    selection_drag_pty: ?u32 = null,

    pipe_read_fd: posix.fd_t = undefined,
    pipe_write_fd: posix.fd_t = undefined,
    parser: vaxis.Parser = undefined,
    pipe_buf: std.ArrayList(u8),
    pipe_recv_buffer: [4096]u8 = undefined,
    colors: Surface.TerminalColors = .{},

    pending_attach_ids: ?[]u32 = null,
    pending_attach_count: usize = 0,
    session_json: ?[]const u8 = null,
    pending_attach_cwd: std.AutoHashMap(u32, []const u8) = undefined,
    // Maps old PTY IDs to new PTY IDs when spawning fresh PTYs due to validity mismatch
    pty_id_remap: std.AutoHashMap(u32, u32) = undefined,

    paste_buffer: ?std.ArrayList(u8) = null,

    // Pending color queries from server (pty_id -> list of targets)
    pending_color_queries: std.ArrayList(PendingColorQuery) = undefined,

    // Current session name (owned by App)
    current_session_name: ?[]const u8 = null,
    // Auto-save timer for debouncing
    autosave_timer: ?io.Task = null,

    pub const PendingColorQuery = struct {
        pty_id: u32,
        target: ServerAction.ColorQueryTarget.Target,
        slot: usize,
    };

    pub const InitError = struct {
        err: anyerror,
        lua_msg: ?[:0]const u8,
    };

    pub const InitResult = union(enum) {
        ok: App,
        err: InitError,
    };

    pub fn init(allocator: std.mem.Allocator) InitResult {
        var app: App = .{
            .allocator = allocator,
            .vx = vaxis.init(allocator, .{
                .kitty_keyboard_flags = .{
                    .report_events = true,
                },
            }) catch |err| {
                return .{ .err = .{ .err = err, .lua_msg = null } };
            },
            .tty = undefined,
            .tty_buffer = undefined,
            .msg_buffer = .empty,
            .msg_arena = std.heap.ArenaAllocator.init(allocator),
            .pipe_buf = .empty,
            .surfaces = std.AutoHashMap(u32, *Surface).init(allocator),
            .state = ClientState.init(allocator),
            .pending_attach_cwd = std.AutoHashMap(u32, []const u8).init(allocator),
            .pty_id_remap = std.AutoHashMap(u32, u32).init(allocator),
            .pending_color_queries = .empty,
        };
        app.parser = .{};

        log.info("Vaxis initialized", .{});

        // Initialize layout UI
        switch (Layout.init(allocator)) {
            .ok => |ui| app.ui = ui,
            .err => |init_err| {
                // Note: we skip vx.deinit here since TTY isn't initialized yet
                // and vx.deinit requires a writer. Resources will be cleaned up on exit.
                return .{ .err = .{ .err = init_err.err, .lua_msg = init_err.lua_msg } };
            },
        }
        // Matcher needs pointer to trie in final memory location
        app.ui.initMatcher();
        log.info("Layout UI initialized", .{});

        // Create pipe for TTY thread -> Main thread communication
        const fds = posix.pipe2(.{ .CLOEXEC = true, .NONBLOCK = true }) catch |err| {
            return .{ .err = .{ .err = err, .lua_msg = null } };
        };
        app.pipe_read_fd = fds[0];
        app.pipe_write_fd = fds[1];
        log.info("Pipe created: read_fd={} write_fd={}", .{ app.pipe_read_fd, app.pipe_write_fd });

        return .{ .ok = app };
    }

    /// Initialize the TTY. Must be called after App is in its final memory location,
    /// since the tty writer holds a pointer to tty_buffer.
    pub fn initTty(self: *App) !void {
        self.tty = try vaxis.Tty.init(&self.tty_buffer);
        log.info("TTY initialized", .{});
    }

    pub fn deinit(self: *App) void {
        log.info("deinit: starting", .{});
        self.ui.deinit();
        self.state.should_quit = true;

        // Wake up TTY thread by sending a Device Status Report request.
        // This causes the terminal to send a response, unblocking the read.
        // Guard: the TTY fd may already be closed if we're exiting due to error.
        if (self.tty.fd >= 0) {
            self.vx.deviceStatusReport(self.tty.writer()) catch {};
        }

        // Cancel pending recv task
        if (self.recv_task) |*task| {
            if (self.io_loop) |loop| {
                task.cancel(loop) catch |err| {
                    log.warn("Failed to cancel recv task: {}", .{err});
                };
            } else {
                log.warn("deinit: io_loop is null, cannot cancel recv task", .{});
            }
            self.recv_task = null;
        }

        if (self.render_timer) |*task| {
            if (self.io_loop) |loop| {
                task.cancel(loop) catch |err| {
                    log.warn("Failed to cancel render timer: {}", .{err});
                };
            }
            self.render_timer = null;
        }

        if (self.autosave_timer) |*task| {
            if (self.io_loop) |loop| {
                task.cancel(loop) catch {};
            }
            self.autosave_timer = null;
        }

        // Close the socket
        if (self.connected) {
            posix.close(self.fd);
        }

        // Wait for TTY thread to exit naturally (it checks should_quit)
        if (self.tty_thread) |thread| {
            thread.join();
        }
        var surface_it = self.surfaces.valueIterator();
        while (surface_it.next()) |surface| {
            surface.*.deinit();
            self.allocator.destroy(surface.*);
        }
        self.surfaces.deinit();

        self.pipe_buf.deinit(self.allocator);
        self.msg_buffer.deinit(self.allocator);
        self.msg_arena.deinit();
        self.state.deinit();
        var cwd_it = self.pending_attach_cwd.valueIterator();
        while (cwd_it.next()) |cwd| {
            self.allocator.free(cwd.*);
        }
        self.pending_attach_cwd.deinit();
        self.pty_id_remap.deinit();
        self.pending_color_queries.deinit(self.allocator);
        if (self.current_session_name) |name| {
            self.allocator.free(name);
        }
        if (self.hit_regions.len > 0) self.allocator.free(self.hit_regions);
        if (self.split_handles.len > 0) self.allocator.free(self.split_handles);
        // Guard: TTY fd may be invalid by the time deinit runs (e.g. timeout kill)
        if (self.tty.fd >= 0) {
            self.vx.deinit(self.allocator, self.tty.writer());
            self.tty.deinit();
        }

        posix.close(self.pipe_read_fd);
        posix.close(self.pipe_write_fd);
    }

    pub fn setup(self: *App, loop: *io.Loop) !void {
        self.io_loop = loop;
        self.ui.setLoop(loop);

        // Exit callback - for when last PTY exits (delete session, don't save)
        self.ui.setExitCallback(self, struct {
            fn exitCb(ctx: *anyopaque) void {
                const app: *App = @ptrCast(@alignCast(ctx));
                // Cancel autosave timer
                if (app.autosave_timer) |*task| {
                    if (app.io_loop) |l| task.cancel(l) catch {};
                    app.autosave_timer = null;
                }
                // Delete session (it's empty)
                app.deleteCurrentSession();
                app.state.should_quit = true;
                if (app.connected) {
                    posix.close(app.fd);
                    app.connected = false;
                }
                if (app.recv_task) |*task| {
                    if (app.io_loop) |l| task.cancel(l) catch {};
                    app.recv_task = null;
                }
                if (app.render_timer) |*task| {
                    if (app.io_loop) |l| task.cancel(l) catch {};
                    app.render_timer = null;
                }
                if (app.pipe_read_task) |*task| {
                    if (app.io_loop) |l| task.cancel(l) catch {};
                    app.pipe_read_task = null;
                }
                if (app.send_task) |*task| {
                    if (app.io_loop) |l| task.cancel(l) catch {};
                    app.send_task = null;
                }
                // Wake up TTY thread so it can exit
                app.vx.deviceStatusReport(app.tty.writer()) catch {};
            }
        }.exitCb);

        try self.vx.enterAltScreen(self.tty.writer());

        // Don't initialize surface yet - wait for first winsize event from TTY thread
        // The surface will be created when we get the window size

        // Register pipe read end with io.Loop
        self.pipe_read_task = try loop.read(self.pipe_read_fd, &self.pipe_recv_buffer, .{
            .ptr = self,
            .cb = onPipeRead,
        });

        // Spawn TTY thread to handle vaxis events and forward via pipe
        log.info("Spawning TTY thread...", .{});
        self.tty_thread = try std.Thread.spawn(.{}, ttyThreadFn, .{self});
        log.info("TTY thread spawned", .{});

        // Send terminal queries to detect capabilities
        try self.vx.queryTerminalSend(self.tty.writer());

        // Query colors
        try self.vx.queryColor(self.tty.writer(), .fg);
        try self.vx.queryColor(self.tty.writer(), .bg);
        try self.vx.queryColor(self.tty.writer(), .cursor);
        for (0..16) |i| {
            try self.vx.queryColor(self.tty.writer(), .{ .index = @intCast(i) });
        }

        // Register spawn callback
        self.ui.setSpawnCallback(self, struct {
            fn spawnCb(ctx: *anyopaque, opts: layout_mod.SpawnOptions) !void {
                const app_ptr: *App = @ptrCast(@alignCast(ctx));
                try app_ptr.spawnPty(opts);
            }
        }.spawnCb);

        // Register redraw callback
        self.ui.setRedrawCallback(self, struct {
            fn redrawCb(ctx: *anyopaque) void {
                const app_ptr: *App = @ptrCast(@alignCast(ctx));
                app_ptr.scheduleRender() catch |err| {
                    log.err("Failed to schedule render: {}", .{err});
                };
            }
        }.redrawCb);

        // Register detach callback
        self.ui.setDetachCallback(self, struct {
            fn detachCb(ctx: *anyopaque, session_name: []const u8) anyerror!void {
                const app_ptr: *App = @ptrCast(@alignCast(ctx));
                try app_ptr.saveSession(session_name);

                // Build array of PTY IDs to detach
                var pty_ids = try app_ptr.allocator.alloc(msgpack.Value, app_ptr.surfaces.count());
                defer app_ptr.allocator.free(pty_ids);
                var i: usize = 0;
                var key_iter = app_ptr.surfaces.keyIterator();
                while (key_iter.next()) |pty_id| {
                    pty_ids[i] = .{ .unsigned = pty_id.* };
                    i += 1;
                }

                // Send single detach_session request with all PTY IDs
                const msgid = app_ptr.state.next_msgid;
                app_ptr.state.next_msgid +%= 1;

                var arr = try app_ptr.allocator.alloc(msgpack.Value, 4);
                arr[0] = .{ .unsigned = 0 }; // request
                arr[1] = .{ .unsigned = msgid };
                arr[2] = .{ .string = "detach_ptys" };
                arr[3] = .{ .array = pty_ids };

                const encoded = msgpack.encodeFromValue(app_ptr.allocator, msgpack.Value{ .array = arr }) catch {
                    app_ptr.allocator.free(arr);
                    return;
                };
                defer app_ptr.allocator.free(encoded);
                app_ptr.allocator.free(arr);

                // Track that we're waiting for detach response
                try app_ptr.state.pending_requests.put(msgid, .detach);

                app_ptr.sendDirect(encoded) catch {};
            }
        }.detachCb);

        // Register save callback
        self.ui.setSaveCallback(self, struct {
            fn saveCb(ctx: *anyopaque) void {
                const app_ptr: *App = @ptrCast(@alignCast(ctx));
                app_ptr.scheduleAutoSave();
            }
        }.saveCb);

        // Register get_session_name callback
        self.ui.setGetSessionNameCallback(self, struct {
            fn getNameCb(ctx: *anyopaque) ?[]const u8 {
                const app_ptr: *App = @ptrCast(@alignCast(ctx));
                return app_ptr.current_session_name;
            }
        }.getNameCb);

        // Register rename_session callback
        self.ui.setRenameSessionCallback(self, struct {
            fn renameCb(ctx: *anyopaque, old_name: []const u8, new_name: []const u8) anyerror!void {
                const app_ptr: *App = @ptrCast(@alignCast(ctx));
                try app_ptr.renameSession(old_name, new_name);
            }
        }.renameCb);

        // Register delete_session callback
        self.ui.setDeleteSessionCallback(self, struct {
            fn deleteCb(ctx: *anyopaque, session_name: []const u8) anyerror!void {
                const app_ptr: *App = @ptrCast(@alignCast(ctx));
                try app_ptr.deleteSession(session_name);
            }
        }.deleteCb);

        // Register switch_session callback
        self.ui.setSwitchSessionCallback(self, struct {
            fn switchCb(ctx: *anyopaque, target_session: []const u8) anyerror!void {
                const app_ptr: *App = @ptrCast(@alignCast(ctx));
                try app_ptr.switchToSession(target_session);
            }
        }.switchCb);

        // Manually trigger initial resize to connect
        const ws = try vaxis.Tty.getWinsize(self.tty.fd);
        try self.handleVaxisEvent(.{ .winsize = ws });
    }

    fn ttyThreadFn(self: *App) void {
        log.info("TTY thread started", .{});

        var buf: [1024]u8 = undefined;
        while (!self.state.should_quit) {
            const n = posix.read(self.tty.fd, &buf) catch |err| {
                log.err("TTY read failed: {}", .{err});
                break;
            };
            if (n == 0) break; // EOF

            // Forward to pipe
            self.writePipeBytes(buf[0..n]) catch |err| {
                log.err("Failed to write to pipe: {}", .{err});
                break;
            };
        }
        log.info("TTY thread exiting", .{});
    }

    fn writePipeBytes(self: *App, data: []const u8) !void {
        var index: usize = 0;
        while (index < data.len) {
            const n = posix.write(self.pipe_write_fd, data[index..]) catch |err| {
                if (err == error.WouldBlock) {
                    std.Thread.sleep(1 * std.time.ns_per_ms);
                    continue;
                }
                return err;
            };
            index += n;
        }
    }

    fn onPipeRead(l: *io.Loop, completion: io.Completion) anyerror!void {
        const app = completion.userdataCast(@This());

        switch (completion.result) {
            .read => |bytes_read| {
                if (bytes_read == 0) {
                    log.warn("Pipe closed", .{});
                    return;
                }

                // Append to pipe buffer
                try app.pipe_buf.appendSlice(app.allocator, app.pipe_recv_buffer[0..bytes_read]);

                // Parse events using vaxis parser
                var i: usize = 0;
                while (i < app.pipe_buf.items.len) {
                    // Guessing parser API: parse(bytes, allocator) -> Result { n, event }
                    const result = try app.parser.parse(app.pipe_buf.items[i..], app.allocator);
                    if (result.n == 0) {
                        // Incomplete sequence, wait for more data
                        break;
                    }
                    i += result.n;

                    if (result.event) |event| {
                        try app.handleVaxisEvent(event);
                    }
                }

                // Remove processed bytes
                if (i > 0) {
                    try app.pipe_buf.replaceRange(app.allocator, 0, i, &.{});
                }

                // Keep reading unless we're quitting
                if (!app.state.should_quit) {
                    app.pipe_read_task = try l.read(app.pipe_read_fd, &app.pipe_recv_buffer, .{
                        .ptr = app,
                        .cb = onPipeRead,
                    });
                }
            },
            .err => |err| {
                log.err("Pipe recv failed: {}", .{err});
            },
            else => unreachable,
        }
    }

    fn handleVaxisEvent(self: *App, event: vaxis.Event) !void {
        log.debug("handleVaxisEvent: {s}", .{@tagName(event)});

        // Handle paste mode: buffer key presses between paste_start and paste_end
        if (event == .paste_start) {
            log.debug("paste_start received", .{});
            self.paste_buffer = .empty;
            return;
        }

        if (event == .paste_end) {
            log.debug("paste_end received", .{});
            if (self.paste_buffer) |*buf| {
                log.debug("sending paste event to lua: {} bytes", .{buf.items.len});
                self.ui.update(.{ .paste = buf.items }) catch |err| {
                    if (err != error.NoUpdateFunction) {
                        log.err("Lua UI update failed: {}", .{err});
                    }
                };
                buf.deinit(self.allocator);
            }
            self.paste_buffer = null;
            return;
        }

        // If we're in paste mode, buffer key presses instead of forwarding
        if (self.paste_buffer != null and event == .key_press) {
            const key = event.key_press;
            if (self.paste_buffer) |*buf| {
                // Handle special keys (enter, tab)
                // Control characters come as Ctrl+letter: Ctrl+J=LF, Ctrl+M=CR, Ctrl+I=Tab
                const is_newline = key.codepoint == vaxis.Key.enter or
                    key.codepoint == '\r' or
                    key.codepoint == '\n' or
                    (key.codepoint == 'j' and key.mods.ctrl) or
                    (key.codepoint == 'm' and key.mods.ctrl);
                if (is_newline) {
                    buf.appendSlice(self.allocator, "\n") catch |err| {
                        log.err("Failed to append paste data: {}", .{err});
                    };
                } else if (key.codepoint == vaxis.Key.tab or key.codepoint == '\t' or (key.codepoint == 'i' and key.mods.ctrl)) {
                    buf.appendSlice(self.allocator, "\t") catch |err| {
                        log.err("Failed to append paste data: {}", .{err});
                    };
                } else if (key.text) |text| {
                    // Regular text input
                    if (buf.items.len + text.len <= MAX_PASTE_SIZE) {
                        buf.appendSlice(self.allocator, text) catch |err| {
                            log.err("Failed to append paste data: {}", .{err});
                        };
                    } else {
                        log.warn("Paste data exceeds maximum size of {} bytes, truncating", .{MAX_PASTE_SIZE});
                    }
                }
            }
            return;
        }

        // Handle mouse events specially - do hit testing and convert to MouseEvent
        if (event == .mouse) {
            const mouse = event.mouse;

            // Convert pixel coordinates to cell coordinates as floats
            const cell_w: f64 = if (self.cell_width_px > 0) @floatFromInt(self.cell_width_px) else 1.0;
            const cell_h: f64 = if (self.cell_height_px > 0) @floatFromInt(self.cell_height_px) else 1.0;
            const x: f64 = @as(f64, @floatFromInt(mouse.col)) / cell_w;
            const y: f64 = @as(f64, @floatFromInt(mouse.row)) / cell_h;

            // Handle ongoing drag
            if (self.drag_state) |*drag| {
                if (mouse.type == .release) {
                    // End drag - calculate final ratio and send to Lua
                    const mouse_pos = switch (drag.handle.axis) {
                        .horizontal => x,
                        .vertical => y,
                    };
                    const new_ratio = drag.handle.calculateNewRatio(mouse_pos);

                    self.ui.update(.{ .split_resize = .{
                        .parent_id = drag.handle.parent_id,
                        .child_index = drag.handle.child_index,
                        .ratio = new_ratio,
                    } }) catch |err| {
                        if (err != error.NoUpdateFunction) {
                            log.err("Lua UI update failed: {}", .{err});
                        }
                    };

                    self.drag_state = null;
                    self.vx.setMouseShape(.default);
                } else if (mouse.type == .drag) {
                    // Continue drag - update ratio
                    const mouse_pos = switch (drag.handle.axis) {
                        .horizontal => x,
                        .vertical => y,
                    };
                    const new_ratio = drag.handle.calculateNewRatio(mouse_pos);

                    self.ui.update(.{ .split_resize = .{
                        .parent_id = drag.handle.parent_id,
                        .child_index = drag.handle.child_index,
                        .ratio = new_ratio,
                    } }) catch |err| {
                        if (err != error.NoUpdateFunction) {
                            log.err("Lua UI update failed: {}", .{err});
                        }
                    };
                }
                return;
            }

            // Check if hovering over a split handle
            if (widget.hitTestSplitHandle(self.split_handles, x, y)) |handle| {
                // Start drag on press
                if (mouse.type == .press and mouse.button == .left) {
                    self.drag_state = .{
                        .handle = handle.*,
                        .start_x = x,
                        .start_y = y,
                    };
                }

                // Set cursor shape based on axis
                switch (handle.axis) {
                    .horizontal => self.vx.setMouseShape(.@"ew-resize"),
                    .vertical => self.vx.setMouseShape(.@"ns-resize"),
                }
                try self.scheduleRender();
                return;
            } else {
                // Not on a split handle
                var target = widget.hitTest(self.hit_regions, x, y);
                var target_x: ?f64 = null;
                var target_y: ?f64 = null;

                // Track selection drag: on left press, remember the target PTY
                if (mouse.button == .left and mouse.type == .press) {
                    self.selection_drag_pty = target;
                } else if (mouse.button == .left and mouse.type == .release) {
                    self.selection_drag_pty = null;
                }

                // During drag, use selection_drag_pty to continue forwarding to
                // the PTY where selection started, even if mouse is outside its region
                if (mouse.type == .drag and self.selection_drag_pty != null) {
                    target = self.selection_drag_pty;
                }

                if (target) |pty_id| {
                    if (widget.findRegion(self.hit_regions, pty_id)) |region| {
                        target_x = x - @as(f64, @floatFromInt(region.x));
                        target_y = y - @as(f64, @floatFromInt(region.y));
                    }
                    // Update mouse cursor shape based on target PTY's mouse_shape
                    if (self.surfaces.get(pty_id)) |surface| {
                        const vaxis_shape = mapMouseShapeToVaxis(surface.mouse_shape);
                        self.vx.setMouseShape(vaxis_shape);
                        try self.scheduleRender();
                    }
                } else {
                    // No target - reset to default
                    self.vx.setMouseShape(.default);
                    try self.scheduleRender();
                }

                const mouse_event = layout_mod.MouseEvent{
                    .x = x,
                    .y = y,
                    .button = mouse.button,
                    .action = mouse.type,
                    .mods = mouse.mods,
                    .target = target,
                    .target_x = target_x,
                    .target_y = target_y,
                };

                self.ui.update(.{ .mouse = mouse_event }) catch |err| {
                    if (err != error.NoUpdateFunction) {
                        log.err("Lua UI update failed: {}", .{err});
                    }
                };
            }
            return;
        }

        // Forward other events to Lua UI
        self.ui.update(.{ .vaxis = event }) catch |err| {
            // Ignore NoUpdateFunction, log others
            if (err != error.NoUpdateFunction) {
                log.err("Lua UI update failed: {}", .{err});
            }
        };

        switch (event) {
            .key_press => |key| {
                // Check for a cursor position response for our explicit width query. This will
                // always be an F3 key with shift = true, and we must be looking for queries
                if (key.codepoint == vaxis.Key.f3 and
                    key.mods.shift and
                    !self.vx.queries_done.load(.unordered))
                {
                    log.info("explicit width capability detected", .{});
                    self.vx.caps.explicit_width = true;
                    self.vx.caps.unicode = .unicode;
                    self.vx.screen.width_method = .unicode;
                    return;
                }
                // Check for a cursor position response for our scaled text query. This will
                // always be an F3 key with alt = true, and we must be looking for queries
                if (key.codepoint == vaxis.Key.f3 and
                    key.mods.alt and
                    !self.vx.queries_done.load(.unordered))
                {
                    log.info("scaled text capability detected", .{});
                    self.vx.caps.scaled_text = true;
                    return;
                }
            },
            .winsize => |ws| {
                // Calculate and store cell metrics
                if (ws.cols > 0 and ws.rows > 0) {
                    self.cell_width_px = if (ws.x_pixel > 0) ws.x_pixel / ws.cols else 0;
                    self.cell_height_px = if (ws.y_pixel > 0) ws.y_pixel / ws.rows else 0;
                }

                if (ws.cols != self.vx.screen.width or ws.rows != self.vx.screen.height) {
                    self.vx.state.in_band_resize = true;
                    try self.vx.resize(self.allocator, self.tty.writer(), ws);

                    // Schedule a render after resize to ensure UI matches new size
                    try self.scheduleRender();
                }
            },
            .cap_kitty_keyboard => {
                log.info("kitty keyboard capability detected", .{});
                self.vx.caps.kitty_keyboard = true;
            },
            .cap_kitty_graphics => {
                if (!self.vx.caps.kitty_graphics) {
                    log.info("kitty graphics capability detected", .{});
                    self.vx.caps.kitty_graphics = true;
                }
            },
            .cap_rgb => {
                log.info("rgb capability detected", .{});
                self.vx.caps.rgb = true;
            },
            .cap_unicode => {
                log.info("unicode capability detected", .{});
                self.vx.caps.unicode = .unicode;
                self.vx.screen.width_method = .unicode;
            },
            .cap_sgr_pixels => {
                log.info("pixel mouse capability detected", .{});
                self.vx.caps.sgr_pixels = true;
            },
            .cap_color_scheme_updates => {
                log.info("color_scheme_updates capability detected", .{});
                self.vx.caps.color_scheme_updates = true;
            },
            .cap_multi_cursor => {
                log.info("multi cursor capability detected", .{});
                self.vx.caps.multi_cursor = true;
            },
            .cap_da1 => {
                self.vx.queries_done.store(true, .unordered);
                try self.vx.enableDetectedFeatures(self.tty.writer());
                // Enable mouse mode (uses pixel coordinates if supported)
                try self.vx.setMouseMode(self.tty.writer(), true);
                // Enable bracketed paste mode
                try self.vx.setBracketedPaste(self.tty.writer(), true);
                // Send init event
                self.ui.update(.init) catch |err| {
                    log.err("Failed to update UI with init: {}", .{err});
                };

                if (!self.connected) {
                    if (self.io_loop) |loop| {
                        log.info("Initiating connection to {s} (da1 received)", .{self.socket_path});
                        _ = try connectUnixSocket(loop, self.socket_path, .{
                            .ptr = self,
                            .cb = App.onConnected,
                        });
                    }
                }
            },
            .color_report => |report| {
                const color: vaxis.Cell.Color = .{ .rgb = report.value };
                log.debug("Received color report: kind={any} color={any}", .{ report.kind, color });
                switch (report.kind) {
                    .fg => self.colors.fg = color,
                    .bg => self.colors.bg = color,
                    .cursor => self.colors.cursor = color,
                    .index => |idx| self.colors.palette[idx] = color,
                }
                // Check if this satisfies a pending color query
                self.processPendingColorQueries(report.kind);
            },

            else => {},
        }
    }

    fn updateSurfaceSize(self: *App, pty_id: u32, rows: u16, cols: u16) void {
        log.info("Updating surface size to {}x{}", .{ cols, rows });

        // Create or resize surface
        if (self.surfaces.get(pty_id)) |surface| {
            surface.resize(rows, cols) catch |err| {
                log.err("Failed to resize surface: {}", .{err});
            };
        } else {
            const surface = self.allocator.create(Surface) catch |err| {
                log.err("Failed to allocate surface: {}", .{err});
                return;
            };
            surface.* = Surface.init(self.allocator, pty_id, rows, cols, self.colors) catch |err| {
                log.err("Failed to create surface: {}", .{err});
                self.allocator.destroy(surface);
                return;
            };
            self.surfaces.put(pty_id, surface) catch |err| {
                log.err("Failed to store surface: {}", .{err});
                surface.deinit();
                self.allocator.destroy(surface);
                return;
            };
            log.info("Surface initialized: {}x{}", .{ cols, rows });
        }
    }

    pub fn sendResize(self: *App, pty_id: u32, rows: u16, cols: u16) !void {
        // Check if surface is already at correct size
        if (self.surfaces.get(pty_id)) |surface| {
            // Only check if this is the active surface
            if (surface.rows == rows and surface.cols == cols) return;

            // Update surface immediately
            try surface.resize(rows, cols);
        }

        const msgid = self.state.next_msgid;
        self.state.next_msgid += 1;

        const width_px = @as(u16, cols) * self.cell_width_px;
        const height_px = @as(u16, rows) * self.cell_height_px;

        const msg = try msgpack.encode(self.allocator, .{ 0, msgid, "resize_pty", .{ pty_id, rows, cols, width_px, height_px } });
        defer self.allocator.free(msg);

        try self.sendDirect(msg);
        log.info("Sent resize request id={} for pty={} to {}x{} ({}x{}px)", .{ msgid, pty_id, cols, rows, width_px, height_px });
    }

    fn handleColorQuery(self: *App, query: ServerAction.ColorQueryTarget) !void {
        log.debug("handleColorQuery: pty={} slot={} target={any}", .{ query.pty_id, query.slot, query.target });

        // Check if we have the color cached
        const cached_color: ?vaxis.Cell.Color = switch (query.target) {
            .foreground => self.colors.fg,
            .background => self.colors.bg,
            .cursor => self.colors.cursor,
            .palette => |idx| self.colors.palette[idx],
        };

        if (cached_color) |color| {
            // Send response immediately
            try self.sendColorResponse(query.pty_id, query.slot, query.target, color);
        } else {
            // Queue for later when we get the color_report
            try self.pending_color_queries.append(self.allocator, .{
                .pty_id = query.pty_id,
                .target = query.target,
                .slot = query.slot,
            });

            // Query the terminal for this color
            switch (query.target) {
                .foreground => try self.vx.queryColor(self.tty.writer(), .fg),
                .background => try self.vx.queryColor(self.tty.writer(), .bg),
                .cursor => try self.vx.queryColor(self.tty.writer(), .cursor),
                .palette => |idx| try self.vx.queryColor(self.tty.writer(), .{ .index = idx }),
            }
        }
    }

    fn processPendingColorQueries(self: *App, kind: vaxis.Color.Kind) void {
        // Find and remove any pending queries that match this color kind
        var i: usize = 0;
        while (i < self.pending_color_queries.items.len) {
            const pending = self.pending_color_queries.items[i];
            const matches = switch (pending.target) {
                .foreground => kind == .fg,
                .background => kind == .bg,
                .cursor => kind == .cursor,
                .palette => |idx| if (kind == .index) kind.index == idx else false,
            };

            if (matches) {
                // Get the cached color (should be set now)
                const color: ?vaxis.Cell.Color = switch (pending.target) {
                    .foreground => self.colors.fg,
                    .background => self.colors.bg,
                    .cursor => self.colors.cursor,
                    .palette => |idx| self.colors.palette[idx],
                };

                if (color) |c| {
                    self.sendColorResponse(pending.pty_id, pending.slot, pending.target, c) catch |err| {
                        log.err("Failed to send color response: {}", .{err});
                    };
                }

                _ = self.pending_color_queries.swapRemove(i);
            } else {
                i += 1;
            }
        }
    }

    fn sendColorResponse(self: *App, pty_id: u32, slot: usize, target: ServerAction.ColorQueryTarget.Target, color: vaxis.Cell.Color) !void {
        const rgb = color.rgb;
        log.debug("sendColorResponse: pty={} slot={} target={any} color=#{x:0>2}{x:0>2}{x:0>2}", .{ pty_id, slot, target, rgb[0], rgb[1], rgb[2] });

        // Build the color_response notification
        // Format: [2, "color_response", {pty_id: N, slot: S, r: R, g: G, b: B, index: M}] or
        //         [2, "color_response", {pty_id: N, slot: S, r: R, g: G, b: B, kind: "foreground"}]
        var map_items = try self.allocator.alloc(msgpack.Value.KeyValue, 6);
        defer self.allocator.free(map_items);

        map_items[0] = .{ .key = .{ .string = "pty_id" }, .value = .{ .unsigned = pty_id } };
        map_items[1] = .{ .key = .{ .string = "slot" }, .value = .{ .unsigned = slot } };
        map_items[2] = .{ .key = .{ .string = "r" }, .value = .{ .unsigned = rgb[0] } };
        map_items[3] = .{ .key = .{ .string = "g" }, .value = .{ .unsigned = rgb[1] } };
        map_items[4] = .{ .key = .{ .string = "b" }, .value = .{ .unsigned = rgb[2] } };

        switch (target) {
            .palette => |idx| {
                map_items[5] = .{ .key = .{ .string = "index" }, .value = .{ .unsigned = idx } };
            },
            .foreground => {
                map_items[5] = .{ .key = .{ .string = "kind" }, .value = .{ .string = "foreground" } };
            },
            .background => {
                map_items[5] = .{ .key = .{ .string = "kind" }, .value = .{ .string = "background" } };
            },
            .cursor => {
                map_items[5] = .{ .key = .{ .string = "kind" }, .value = .{ .string = "cursor" } };
            },
        }

        const params = msgpack.Value{ .map = map_items };
        const msg_bytes = try msgpack.encode(self.allocator, .{ 2, "color_response", params });
        defer self.allocator.free(msg_bytes);

        try self.sendDirect(msg_bytes);
    }

    pub fn handleRedraw(self: *App, params: msgpack.Value) !void {
        log.debug("handleRedraw: received redraw params", .{});
        if (params != .array) return;

        // Find PTY ID from events
        var pty_id: ?u32 = null;
        for (params.array) |event| {
            if (event != .array or event.array.len < 2) continue;
            const name = event.array[0];
            if (name != .string) continue;
            if (std.mem.eql(u8, name.string, "style")) continue;
            if (std.mem.eql(u8, name.string, "flush")) continue;

            const args = event.array[1];
            if (args != .array or args.array.len < 1) continue;

            switch (args.array[0]) {
                .integer => |i| pty_id = @intCast(i),
                .unsigned => |u| pty_id = @intCast(u),
                else => {},
            }
            if (pty_id) |_| break;
        }

        // Handle kitty_graphics events separately (passthrough to host terminal)
        for (params.array) |event| {
            if (event != .array or event.array.len < 2) continue;
            const name = event.array[0];
            if (name != .string) continue;
            if (std.mem.eql(u8, name.string, "kitty_graphics")) {
                self.handleKittyGraphics(event.array[1]);
            }
        }

        if (pty_id) |pid| {
            if (self.surfaces.get(pid)) |surface| {
                try surface.applyRedraw(params);

                // Check if we got a flush event - if so, swap and render
                const should_flush = ClientLogic.shouldFlush(params);
                if (should_flush) {
                    log.debug("handleRedraw: flush event received for pty {}, rendering", .{pid});
                    try self.scheduleRender();
                }
            } else {
                log.warn("handleRedraw: no surface for pty {}, ignoring redraw", .{pid});
            }
        } else {
            log.debug("handleRedraw: could not determine pty_id from events", .{});
        }
    }

    fn handleKittyGraphics(self: *App, args: msgpack.Value) void {
        if (args != .array or args.array.len < 2) return;

        // args = [pty_id, raw_apc_data]
        const pty_id: u32 = switch (args.array[0]) {
            .unsigned => |u| @intCast(u),
            .integer => |i| @intCast(i),
            else => return,
        };
        const data = switch (args.array[1]) {
            .binary => |b| b,
            .string => |s| s,
            else => return,
        };

        if (data.len == 0) return;

        // Find the window position for this PTY from hit regions
        var offset_x: u16 = 0;
        var offset_y: u16 = 0;
        for (self.hit_regions) |region| {
            if (region.pty_id == pty_id) {
                offset_x = region.x;
                offset_y = region.y;
                break;
            }
        }

        // Write APC passthrough directly to host terminal
        // Format: ESC _ <data> ESC \ (APC sequence)
        // But first, we need to position the cursor for image placement
        // The kitty graphics protocol handles positioning via its own parameters,
        // so we just write the raw APC as-is, wrapped in APC delimiters.
        const writer = self.tty.writer();
        writer.writeAll("\x1b_") catch return;
        writer.writeAll(data) catch return;
        writer.writeAll("\x1b\\") catch return;

        // TODO: coordinate translation using offset_x, offset_y for multi-window
        // For now, passthrough works for single/focused window
        log.debug("kitty_graphics: pty={} data_len={} offset=({},{})", .{ pty_id, data.len, offset_x, offset_y });
    }

    fn renderWidget(self: *App, w: widget.Widget, win: vaxis.Window) !void {
        try w.renderTo(win, self.allocator);
    }

    pub fn scheduleRender(self: *App) !void {
        if (self.io_loop) |loop| {
            const now = std.time.milliTimestamp();
            const FRAME_TIME = 8;

            if (now - self.last_render_time >= FRAME_TIME) {
                try self.render();
            } else if (self.render_timer == null) {
                const delay = FRAME_TIME - (now - self.last_render_time);
                // Make sure delay is positive
                const safe_delay = if (delay < 0) 0 else delay;
                self.render_timer = try loop.timeout(@as(u64, @intCast(safe_delay)) * std.time.ns_per_ms, .{
                    .ptr = self,
                    .cb = onRenderTimer,
                });
            }
        } else {
            // Fallback if no loop (e.g. during tests or init)
            try self.render();
        }
    }

    fn onRenderTimer(loop: *io.Loop, completion: io.Completion) anyerror!void {
        _ = loop;
        const app = completion.userdataCast(App);
        app.render() catch |err| {
            log.err("Failed to render frame: {}", .{err});
        };
        app.render_timer = null;
    }

    pub fn render(self: *App) !void {
        log.debug("render: starting render", .{});

        const win = self.vx.window();
        win.hideCursor();
        win.clear();

        var root_widget = self.ui.view() catch |err| {
            log.err("Failed to get view from UI: {}", .{err});
            return;
        };
        defer root_widget.deinit(self.allocator);

        const screen = win.screen;

        const constraints = widget.BoxConstraints{
            .min_width = 0,
            .max_width = screen.width,
            .min_height = 0,
            .max_height = screen.height,
        };

        var w = root_widget;
        _ = w.layout(constraints);

        // Collect hit regions for mouse event targeting
        if (self.hit_regions.len > 0) {
            self.allocator.free(self.hit_regions);
        }
        self.hit_regions = w.collectHitRegions(self.allocator, 0, 0) catch &.{};

        // Collect split handles for resize drag detection
        if (self.split_handles.len > 0) {
            self.allocator.free(self.split_handles);
        }
        self.split_handles = w.collectSplitHandles(self.allocator, 0, 0) catch &.{};

        // Check for surface resize mismatches and send resize events
        const resizes = w.collectSurfaceResizes(self.allocator) catch &.{};
        defer if (resizes.len > 0) self.allocator.free(resizes);
        for (resizes) |resize| {
            log.info("Surface resize needed: pty={} {}x{} -> {}x{}", .{
                resize.pty_id,
                resize.surface.cols,
                resize.surface.rows,
                resize.width,
                resize.height,
            });
            self.sendResize(resize.pty_id, resize.height, resize.width) catch |err| {
                log.err("Failed to send resize: {}", .{err});
            };
        }

        try self.renderWidget(w, win);

        // Cursor passthrough: show focused window's cursor at correct host position
        if (self.ui.mode == .terminal) {
            if (self.ui.focused_id) |fid| {
                if (self.surfaces.get(fid)) |surface| {
                    if (surface.front.cursor_vis) {
                        for (self.hit_regions) |region| {
                            if (region.pty_id == fid) {
                                const host_col = region.x + surface.front.cursor_col;
                                const host_row = region.y + surface.front.cursor_row;
                                if (host_col < screen.width and host_row < screen.height) {
                                    win.showCursor(host_col, host_row);
                                    // Map redraw cursor shape to vaxis cursor shape
                                    const shape: vaxis.Cell.CursorShape = switch (surface.cursor_shape) {
                                        .block => .block,
                                        .beam => .beam,
                                        .underline => .underline,
                                    };
                                    win.setCursorShape(shape);
                                }
                                break;
                            }
                        }
                    }
                }
            }
        }

        log.debug("render: calling vx.render()", .{});
        if (self.state.pty_id) |pid_i64| {
            const pid = @as(u32, @intCast(pid_i64));
            if (self.surfaces.get(pid)) |surface| {
                self.vx.setTitle(self.tty.writer(), surface.getTitle()) catch {};
            }
        }
        try self.vx.render(self.tty.writer());
        log.debug("render: flushing tty", .{});
        try self.tty.tty_writer.interface.flush();
        log.debug("render: complete", .{});

        self.last_render_time = std.time.milliTimestamp();
    }

    pub fn onConnected(l: *io.Loop, completion: io.Completion) anyerror!void {
        const app = completion.userdataCast(@This());

        switch (completion.result) {
            .socket => |fd| {
                app.fd = fd;
                app.connected = true;
                log.info("Connected! fd={}", .{app.fd});

                if (!app.state.should_quit) {
                    // Start receiving from the server
                    app.recv_task = try l.recv(fd, &app.recv_buffer, .{
                        .ptr = app,
                        .cb = onRecv,
                    });

                    // First, get server info to obtain pty_validity
                    // After receiving server_info, onServerInfo will continue with spawn/attach
                    try app.sendGetServerInfo();
                } else {
                    log.info("Connected but should_quit=true, not sending spawn_pty", .{});
                }
            },
            .err => |err| {
                if (err == error.ConnectionRefused) {
                    app.state.connection_refused = true;
                } else {
                    log.err("Connection failed: {}", .{err});
                }
            },
            else => unreachable,
        }
    }

    fn sendGetServerInfo(self: *App) !void {
        const msgid = self.state.next_msgid;
        self.state.next_msgid += 1;
        try self.state.pending_requests.put(msgid, .get_server_info);

        // [0, msgid, "get_server_info", {}]
        const msg = try msgpack.encode(self.allocator, .{ 0, msgid, "get_server_info", .{} });
        try self.sendDirect(msg);
        self.allocator.free(msg);
    }

    fn onServerInfoReceived(self: *App) !void {
        // After receiving server info, proceed with spawn or session attach
        if (self.attach_session) |session_name| {
            // Use the attached session name
            log.info("Setting current_session_name to: {s}", .{session_name});
            self.current_session_name = try self.allocator.dupe(u8, session_name);
            try self.startSessionAttach(session_name);
        } else if (self.new_session_name) |name| {
            // User specified a name for new session
            self.current_session_name = try self.allocator.dupe(u8, name);
            log.info("Starting new session with user-specified name: {s}", .{name});
            try self.spawnInitialPty();
        } else {
            // Generate a new session name for fresh launch
            self.current_session_name = try self.ui.getNextSessionName();
            log.info("Starting new session: {s}", .{self.current_session_name.?});
            try self.spawnInitialPty();
        }
    }

    fn spawnInitialPty(self: *App) !void {
        const ws = try vaxis.Tty.getWinsize(self.tty.fd);

        const msgid = self.state.next_msgid;
        self.state.next_msgid += 1;
        try self.state.pending_requests.put(msgid, .{ .spawn = .{ .cwd = self.initial_cwd } });

        var arena = std.heap.ArenaAllocator.init(self.allocator);
        defer arena.deinit();
        const arena_alloc = arena.allocator();

        var env_map = try std.process.getEnvMap(self.allocator);
        defer env_map.deinit();

        var env_array = std.ArrayList(msgpack.Value).empty;
        defer env_array.deinit(self.allocator);
        var env_it = env_map.iterator();
        while (env_it.next()) |entry| {
            const key = entry.key_ptr.*;
            if (isFilteredEnvVar(key)) continue;
            const env_str = try std.fmt.allocPrint(arena_alloc, "{s}={s}", .{ key, entry.value_ptr.* });
            try env_array.append(self.allocator, .{ .string = env_str });
        }

        const macos_option_as_alt = self.ui.getMacosOptionAsAlt();
        const param_count: usize = if (self.initial_cwd != null) 6 else 5;
        var params_kv = try self.allocator.alloc(msgpack.Value.KeyValue, param_count);
        defer self.allocator.free(params_kv);
        log.info("Sending spawn_pty: rows={} cols={} cwd={?s} env_count={}", .{ ws.rows, ws.cols, self.initial_cwd, env_array.items.len });
        params_kv[0] = .{ .key = .{ .string = "rows" }, .value = .{ .unsigned = ws.rows } };
        params_kv[1] = .{ .key = .{ .string = "cols" }, .value = .{ .unsigned = ws.cols } };
        params_kv[2] = .{ .key = .{ .string = "attach" }, .value = .{ .boolean = true } };
        params_kv[3] = .{ .key = .{ .string = "env" }, .value = .{ .array = env_array.items } };
        params_kv[4] = .{ .key = .{ .string = "macos_option_as_alt" }, .value = .{ .string = macos_option_as_alt } };
        if (self.initial_cwd) |cwd| {
            params_kv[5] = .{ .key = .{ .string = "cwd" }, .value = .{ .string = cwd } };
        }
        const params_val = msgpack.Value{ .map = params_kv };
        const msg = try msgpack.encode(self.allocator, .{ 0, msgid, "spawn_pty", params_val });
        defer self.allocator.free(msg);
        try self.sendDirect(msg);
    }

    fn startSessionAttach(self: *App, session_name: []const u8) !void {
        const home = std.posix.getenv("HOME") orelse return error.NoHomeDirectory;

        const filename = try std.fmt.allocPrint(self.allocator, "{s}.json", .{session_name});
        defer self.allocator.free(filename);

        const path = try std.fs.path.join(self.allocator, &.{ home, ".local", "state", "tuios", "sessions", filename });
        defer self.allocator.free(path);

        const file = std.fs.openFileAbsolute(path, .{}) catch |err| {
            log.err("Failed to open session file {s}: {}", .{ path, err });
            return err;
        };
        defer file.close();

        const json = try file.readToEndAlloc(self.allocator, 1024 * 1024);
        self.session_json = json;

        // Check if pty_validity matches - if not, skip attach and spawn fresh
        const saved_validity = extractPtyValidityFromJson(self.allocator, json);
        const validity_matches = if (saved_validity) |saved| blk: {
            if (self.state.pty_validity) |current| {
                break :blk saved == current;
            }
            break :blk false;
        } else false;

        if (!validity_matches) {
            log.info("pty_validity mismatch (saved={?}, current={?}), spawning fresh PTYs", .{ saved_validity, self.state.pty_validity });
        }

        const pty_ids = try extractPtyIdsFromJson(self.allocator, json);
        if (pty_ids.len == 0) {
            log.err("No PTY IDs found in session", .{});
            self.allocator.free(json);
            self.session_json = null;
            return error.NoSessionsFound;
        }

        // Extract PTY ID + cwd pairs for fallback spawning if attach fails
        self.pending_attach_cwd = try extractPtyIdCwdPairs(self.allocator, json);

        log.info("Attaching to {} PTYs from session {s}", .{ pty_ids.len, session_name });
        self.pending_attach_ids = pty_ids;
        self.pending_attach_count = pty_ids.len;

        for (pty_ids) |pty_id| {
            const cwd = self.pending_attach_cwd.get(pty_id);

            // If validity doesn't match, spawn fresh instead of attaching
            if (!validity_matches) {
                const msgid = self.state.next_msgid;
                self.state.next_msgid += 1;
                try self.state.pending_requests.put(msgid, .{ .spawn = .{ .cwd = cwd, .old_pty_id = pty_id } });
                try self.spawnPtyWithCwd(msgid, cwd);
                continue;
            }

            const msgid = self.state.next_msgid;
            self.state.next_msgid += 1;
            try self.state.pending_requests.put(msgid, .{ .attach = .{ .pty_id = @intCast(pty_id), .cwd = cwd } });

            // Server expects params as array: [pty_id, macos_option_as_alt]
            const macos_option_as_alt = self.ui.getMacosOptionAsAlt();
            var params = try self.allocator.alloc(msgpack.Value, 2);
            params[0] = .{ .unsigned = pty_id };
            params[1] = .{ .string = macos_option_as_alt };
            const params_val = msgpack.Value{ .array = params };

            var arr = try self.allocator.alloc(msgpack.Value, 4);
            arr[0] = .{ .unsigned = 0 };
            arr[1] = .{ .unsigned = msgid };
            arr[2] = .{ .string = "attach_pty" };
            arr[3] = params_val;

            const encoded = try msgpack.encodeFromValue(self.allocator, msgpack.Value{ .array = arr });
            defer self.allocator.free(encoded);
            self.allocator.free(arr);
            self.allocator.free(params);

            try self.sendDirect(encoded);
        }
    }

    fn extractPtyValidityFromJson(allocator: std.mem.Allocator, json: []const u8) ?i64 {
        const parsed = std.json.parseFromSlice(std.json.Value, allocator, json, .{}) catch return null;
        defer parsed.deinit();

        if (parsed.value != .object) return null;
        const validity_val = parsed.value.object.get("pty_validity") orelse return null;
        if (validity_val != .integer) return null;
        return validity_val.integer;
    }

    fn spawnPtyWithCwd(self: *App, msgid: u32, cwd: ?[]const u8) !void {
        const ws = try vaxis.Tty.getWinsize(self.tty.fd);

        var env_map = try std.process.getEnvMap(self.allocator);
        defer env_map.deinit();

        var env_array = std.ArrayList(msgpack.Value).empty;
        defer env_array.deinit(self.allocator);
        var env_it = env_map.iterator();
        while (env_it.next()) |entry| {
            const key = entry.key_ptr.*;
            if (isFilteredEnvVar(key)) continue;
            const env_str = try std.fmt.allocPrint(self.allocator, "{s}={s}", .{ key, entry.value_ptr.* });
            try env_array.append(self.allocator, .{ .string = env_str });
        }

        const macos_option_as_alt = self.ui.getMacosOptionAsAlt();
        const param_count: usize = if (cwd != null) 6 else 5;
        var params_kv = try self.allocator.alloc(msgpack.Value.KeyValue, param_count);
        defer self.allocator.free(params_kv);
        params_kv[0] = .{ .key = .{ .string = "rows" }, .value = .{ .unsigned = ws.rows } };
        params_kv[1] = .{ .key = .{ .string = "cols" }, .value = .{ .unsigned = ws.cols } };
        params_kv[2] = .{ .key = .{ .string = "attach" }, .value = .{ .boolean = true } };
        params_kv[3] = .{ .key = .{ .string = "env" }, .value = .{ .array = env_array.items } };
        params_kv[4] = .{ .key = .{ .string = "macos_option_as_alt" }, .value = .{ .string = macos_option_as_alt } };
        if (cwd) |c| {
            params_kv[5] = .{ .key = .{ .string = "cwd" }, .value = .{ .string = c } };
        }
        const params_val = msgpack.Value{ .map = params_kv };
        const msg = try msgpack.encode(self.allocator, .{ 0, msgid, "spawn_pty", params_val });
        try self.sendDirect(msg);
        self.allocator.free(msg);
    }

    fn onSendComplete(_: *io.Loop, completion: io.Completion) anyerror!void {
        const app = completion.userdataCast(@This());

        if (app.send_buffer) |buf| {
            app.allocator.free(buf);
            app.send_buffer = null;
        }

        switch (completion.result) {
            .send => {},
            .err => |err| {
                log.err("Send failed: {}", .{err});
            },
            else => unreachable,
        }
    }

    fn onRecv(l: *io.Loop, completion: io.Completion) anyerror!void {
        const app = completion.userdataCast(@This());
        defer _ = app.msg_arena.reset(.retain_capacity);
        const arena = app.msg_arena.allocator();

        switch (completion.result) {
            .recv => |initial_bytes_read| {
                if (initial_bytes_read == 0) {
                    log.info("Server closed connection", .{});
                    app.state.should_quit = true;
                    if (app.connected) {
                        posix.close(app.fd);
                        app.connected = false;
                    }
                    app.vx.deviceStatusReport(app.tty.writer()) catch {};
                    return;
                }

                var current_bytes_read = initial_bytes_read;

                while (true) {
                    // Append new data to message buffer
                    try app.msg_buffer.appendSlice(app.allocator, app.recv_buffer[0..current_bytes_read]);

                    // Try to decode as many complete messages as possible
                    while (app.msg_buffer.items.len > 0) {
                        const result = rpc.decodeMessageWithSize(arena, app.msg_buffer.items) catch |err| {
                            if (err == error.UnexpectedEndOfInput) {
                                // Partial message, wait for more data
                                break;
                            }
                            return err;
                        };
                        defer result.message.deinit(arena);

                        const msg = result.message;
                        const bytes_consumed = result.bytes_consumed;

                        // Process message via ClientLogic
                        const action = try ClientLogic.processServerMessage(&app.state, msg);

                        switch (action) {
                            .send_attach => |pty_id| {
                                log.info("Sending attach_pty for session {}", .{pty_id});
                                app.send_buffer = try msgpack.encode(app.allocator, .{ 0, @intFromEnum(MsgId.attach_pty), "attach_pty", .{ pty_id, "false" } });
                                _ = try l.send(app.fd, app.send_buffer.?, .{
                                    .ptr = app,
                                    .cb = onSendComplete,
                                });
                            },
                            .redraw => |params| {
                                app.handleRedraw(params) catch |err| {
                                    log.err("Failed to handle redraw: {}", .{err});
                                };
                            },
                            .attached => |info| {
                                log.info("Attached to session, checking for resize", .{});
                                const pty_id = @as(u32, @intCast(info.new_pty_id));

                                // If this spawn replaced an old PTY (validity mismatch), record the mapping
                                if (info.old_pty_id) |old_id| {
                                    log.info("Recording PTY ID remap: {} -> {}", .{ old_id, pty_id });
                                    app.pty_id_remap.put(old_id, pty_id) catch |err| {
                                        log.err("Failed to record PTY ID remap: {}", .{err});
                                    };
                                }

                                // Ensure surface exists
                                if (!app.surfaces.contains(pty_id)) {
                                    const ws = try vaxis.Tty.getWinsize(app.tty.fd);
                                    log.info("Creating surface for attached session {}: {}x{}", .{ pty_id, ws.rows, ws.cols });
                                    const surface = app.allocator.create(Surface) catch |err| {
                                        log.err("Failed to allocate surface: {}", .{err});
                                        return error.SurfaceInitFailed;
                                    };
                                    surface.* = Surface.init(app.allocator, pty_id, ws.rows, ws.cols, app.colors) catch |err| {
                                        log.err("Failed to create surface: {}", .{err});
                                        app.allocator.destroy(surface);
                                        return error.SurfaceInitFailed;
                                    };
                                    app.surfaces.put(pty_id, surface) catch |err| {
                                        log.err("Failed to store surface: {}", .{err});
                                        surface.deinit();
                                        app.allocator.destroy(surface);
                                        return error.SurfaceInitFailed;
                                    };
                                }

                                if (app.surfaces.get(pty_id)) |surface| {
                                    // Skip pty_attach events during session restore - set_state will restore the UI tree
                                    const is_session_restore = app.pending_attach_count > 0 or app.session_json != null;
                                    if (!is_session_restore) {
                                        log.info("Updating UI with pty_attach for {}", .{pty_id});
                                        // Send pty_attach event to Lua UI
                                        app.ui.update(.{
                                            .pty_attach = .{
                                                .id = pty_id,
                                                .surface = surface,
                                                .app = app,
                                                .send_key_fn = struct {
                                                    fn appSendDirect(ctx: *anyopaque, id: u32, key: layout_mod.KeyData) anyerror!void {
                                                        const self: *App = @ptrCast(@alignCast(ctx));

                                                        var key_map_kv = try self.allocator.alloc(msgpack.Value.KeyValue, 6);
                                                        key_map_kv[0] = .{ .key = .{ .string = "key" }, .value = .{ .string = key.key } };
                                                        key_map_kv[1] = .{ .key = .{ .string = "code" }, .value = .{ .string = key.code } };
                                                        key_map_kv[2] = .{ .key = .{ .string = "shiftKey" }, .value = .{ .boolean = key.shift } };
                                                        key_map_kv[3] = .{ .key = .{ .string = "ctrlKey" }, .value = .{ .boolean = key.ctrl } };
                                                        key_map_kv[4] = .{ .key = .{ .string = "altKey" }, .value = .{ .boolean = key.alt } };
                                                        key_map_kv[5] = .{ .key = .{ .string = "metaKey" }, .value = .{ .boolean = key.super } };

                                                        const key_map_val: msgpack.Value = .{ .map = key_map_kv };

                                                        var params = try self.allocator.alloc(msgpack.Value, 2);
                                                        params[0] = .{ .unsigned = @intCast(id) };
                                                        params[1] = key_map_val;

                                                        const method: []const u8 = if (key.release) "key_release" else "key_input";

                                                        var arr = try self.allocator.alloc(msgpack.Value, 3);
                                                        arr[0] = .{ .unsigned = 2 }; // notification
                                                        arr[1] = .{ .string = method };
                                                        arr[2] = .{ .array = params };

                                                        const encoded_msg = try msgpack.encodeFromValue(self.allocator, .{ .array = arr });
                                                        defer self.allocator.free(encoded_msg);

                                                        // Clean up msgpack structures
                                                        self.allocator.free(arr);
                                                        self.allocator.free(params);
                                                        self.allocator.free(key_map_kv);

                                                        try self.sendDirect(encoded_msg);
                                                    }
                                                }.appSendDirect,
                                                .send_mouse_fn = struct {
                                                    fn appSendMouse(ctx: *anyopaque, id: u32, mouse: layout_mod.MouseData) anyerror!void {
                                                        const self: *App = @ptrCast(@alignCast(ctx));

                                                        var mouse_map_kv = try self.allocator.alloc(msgpack.Value.KeyValue, 7);
                                                        mouse_map_kv[0] = .{ .key = .{ .string = "x" }, .value = .{ .float = mouse.x } };
                                                        mouse_map_kv[1] = .{ .key = .{ .string = "y" }, .value = .{ .float = mouse.y } };
                                                        mouse_map_kv[2] = .{ .key = .{ .string = "button" }, .value = .{ .string = mouse.button } };
                                                        mouse_map_kv[3] = .{ .key = .{ .string = "event_type" }, .value = .{ .string = mouse.event_type } };
                                                        mouse_map_kv[4] = .{ .key = .{ .string = "shiftKey" }, .value = .{ .boolean = mouse.shift } };
                                                        mouse_map_kv[5] = .{ .key = .{ .string = "ctrlKey" }, .value = .{ .boolean = mouse.ctrl } };
                                                        mouse_map_kv[6] = .{ .key = .{ .string = "altKey" }, .value = .{ .boolean = mouse.alt } };

                                                        const mouse_map_val: msgpack.Value = .{ .map = mouse_map_kv };

                                                        var params = try self.allocator.alloc(msgpack.Value, 2);
                                                        params[0] = .{ .unsigned = @intCast(id) };
                                                        params[1] = mouse_map_val;

                                                        var arr = try self.allocator.alloc(msgpack.Value, 3);
                                                        arr[0] = .{ .unsigned = 2 }; // notification
                                                        arr[1] = .{ .string = "mouse_input" };
                                                        arr[2] = .{ .array = params };

                                                        const encoded_msg = try msgpack.encodeFromValue(self.allocator, .{ .array = arr });
                                                        defer self.allocator.free(encoded_msg);

                                                        // Clean up msgpack structures
                                                        self.allocator.free(arr);
                                                        self.allocator.free(params);
                                                        self.allocator.free(mouse_map_kv);

                                                        try self.sendDirect(encoded_msg);
                                                    }
                                                }.appSendMouse,
                                                .send_paste_fn = struct {
                                                    fn appSendPaste(ctx: *anyopaque, id: u32, data: []const u8) anyerror!void {
                                                        const self: *App = @ptrCast(@alignCast(ctx));

                                                        var params = try self.allocator.alloc(msgpack.Value, 2);
                                                        params[0] = .{ .unsigned = @intCast(id) };
                                                        params[1] = .{ .binary = data };

                                                        var arr = try self.allocator.alloc(msgpack.Value, 3);
                                                        arr[0] = .{ .unsigned = 2 }; // notification
                                                        arr[1] = .{ .string = "paste_input" };
                                                        arr[2] = .{ .array = params };

                                                        const encoded_msg = try msgpack.encodeFromValue(self.allocator, .{ .array = arr });
                                                        defer self.allocator.free(encoded_msg);

                                                        self.allocator.free(arr);
                                                        self.allocator.free(params);

                                                        try self.sendDirect(encoded_msg);
                                                        log.debug("Sent paste_input: {} bytes to pty {}", .{ data.len, id });
                                                    }
                                                }.appSendPaste,
                                                .set_focus_fn = struct {
                                                    fn appSendFocus(ctx: *anyopaque, id: u32, focused: bool) anyerror!void {
                                                        const self: *App = @ptrCast(@alignCast(ctx));

                                                        var params = try self.allocator.alloc(msgpack.Value, 2);
                                                        params[0] = .{ .unsigned = @intCast(id) };
                                                        params[1] = .{ .boolean = focused };

                                                        var arr = try self.allocator.alloc(msgpack.Value, 3);
                                                        arr[0] = .{ .unsigned = 2 }; // notification
                                                        arr[1] = .{ .string = "focus_event" };
                                                        arr[2] = .{ .array = params };

                                                        const encoded_msg = try msgpack.encodeFromValue(self.allocator, .{ .array = arr });
                                                        defer self.allocator.free(encoded_msg);

                                                        self.allocator.free(arr);
                                                        self.allocator.free(params);

                                                        try self.sendDirect(encoded_msg);
                                                        log.debug("Sent focus_event: {} to pty {}", .{ focused, id });
                                                    }
                                                }.appSendFocus,
                                                .close_fn = struct {
                                                    fn appClosePty(ctx: *anyopaque, id: u32) anyerror!void {
                                                        const self: *App = @ptrCast(@alignCast(ctx));

                                                        var params = try self.allocator.alloc(msgpack.Value, 1);
                                                        params[0] = .{ .unsigned = @intCast(id) };

                                                        const msgid = self.state.next_msgid;
                                                        self.state.next_msgid +%= 1;

                                                        var arr = try self.allocator.alloc(msgpack.Value, 4);
                                                        arr[0] = .{ .unsigned = 0 }; // request
                                                        arr[1] = .{ .unsigned = msgid };
                                                        arr[2] = .{ .string = "close_pty" };
                                                        arr[3] = .{ .array = params };

                                                        const encoded_msg = try msgpack.encodeFromValue(self.allocator, .{ .array = arr });
                                                        defer self.allocator.free(encoded_msg);

                                                        self.allocator.free(arr);
                                                        self.allocator.free(params);

                                                        try self.sendDirect(encoded_msg);
                                                    }
                                                }.appClosePty,
                                                .cwd_fn = struct {
                                                    fn appGetCwd(ctx: *anyopaque, id: u32) ?[]const u8 {
                                                        const self: *App = @ptrCast(@alignCast(ctx));
                                                        return self.state.cwd_map.get(@intCast(id));
                                                    }
                                                }.appGetCwd,
                                                .copy_selection_fn = struct {
                                                    fn appCopySelection(ctx: *anyopaque, id: u32) anyerror!void {
                                                        const self: *App = @ptrCast(@alignCast(ctx));
                                                        try self.requestCopySelection(id);
                                                    }
                                                }.appCopySelection,
                                                .cell_size_fn = struct {
                                                    fn appGetCellSize(ctx: *anyopaque) layout_mod.CellSize {
                                                        const self: *App = @ptrCast(@alignCast(ctx));
                                                        return .{
                                                            .width = self.cell_width_px,
                                                            .height = self.cell_height_px,
                                                        };
                                                    }
                                                }.appGetCellSize,
                                            },
                                        }) catch |err| {
                                            log.err("Failed to update UI with pty_attach: {}", .{err});
                                        };
                                    }

                                    const width_px = @as(u16, surface.cols) * app.cell_width_px;
                                    const height_px = @as(u16, surface.rows) * app.cell_height_px;
                                    log.info("Sending initial resize: {}x{} ({}x{}px, cell={}x{})", .{ surface.rows, surface.cols, width_px, height_px, app.cell_width_px, app.cell_height_px });
                                    const resize_msg = try msgpack.encode(app.allocator, .{
                                        2, // notification
                                        "resize_pty",
                                        .{ pty_id, surface.rows, surface.cols, width_px, height_px },
                                    });
                                    defer app.allocator.free(resize_msg);
                                    try app.sendDirect(resize_msg);

                                    // Check if we're in session attach mode and all PTYs attached
                                    if (app.pending_attach_count > 0) {
                                        app.pending_attach_count -= 1;
                                        if (app.pending_attach_count == 0) {
                                            log.info("All PTYs attached, restoring session state", .{});
                                            if (app.session_json) |json| {
                                                app.ui.setStateFromJson(json, ptyLookup, app) catch |err| {
                                                    log.err("Failed to restore session state: {}", .{err});
                                                };
                                                app.allocator.free(json);
                                                app.session_json = null;
                                            }
                                            if (app.pending_attach_ids) |ids| {
                                                app.allocator.free(ids);
                                                app.pending_attach_ids = null;
                                            }
                                            try app.scheduleRender();
                                        }
                                    }
                                }
                            },
                            .spawn_pty_with_cwd => |info| {
                                log.info("Spawning new PTY with cwd: {s}", .{if (info.cwd) |c| c else "default"});
                                const msgid = app.state.next_msgid;
                                app.state.next_msgid += 1;
                                try app.state.pending_requests.put(msgid, .{ .spawn = .{ .cwd = info.cwd } });

                                // Build env array from current process environment
                                var env_map = try std.process.getEnvMap(app.allocator);
                                defer env_map.deinit();

                                var env_array = std.ArrayList(msgpack.Value).empty;
                                defer env_array.deinit(app.allocator);
                                var env_it = env_map.iterator();
                                while (env_it.next()) |entry| {
                                    const key = entry.key_ptr.*;
                                    if (isFilteredEnvVar(key)) continue;
                                    const env_str = try std.fmt.allocPrint(app.allocator, "{s}={s}", .{ key, entry.value_ptr.* });
                                    try env_array.append(app.allocator, .{ .string = env_str });
                                }

                                // Build spawn_pty params with env and optional cwd
                                const param_count: usize = if (info.cwd != null) 2 else 1;
                                var kv = try app.allocator.alloc(msgpack.Value.KeyValue, param_count);
                                defer app.allocator.free(kv);
                                kv[0] = .{ .key = .{ .string = "env" }, .value = .{ .array = env_array.items } };
                                if (info.cwd) |cwd| {
                                    if (cwd.len > 0) {
                                        kv[1] = .{ .key = .{ .string = "cwd" }, .value = .{ .string = cwd } };
                                    }
                                }

                                var arr = try app.allocator.alloc(msgpack.Value, 4);
                                defer app.allocator.free(arr);
                                arr[0] = .{ .unsigned = 0 };
                                arr[1] = .{ .unsigned = msgid };
                                arr[2] = .{ .string = "spawn_pty" };
                                arr[3] = .{ .map = kv };

                                const encoded = try msgpack.encodeFromValue(app.allocator, msgpack.Value{ .array = arr });
                                defer app.allocator.free(encoded);

                                try app.sendDirect(encoded);
                            },
                            .pty_exited => |info| {
                                log.info("PTY {} exited with status {}", .{ info.pty_id, info.status });
                                // Clean up the surface for this PTY BEFORE updating UI
                                // so that surfaces.count() is correct when quit callback runs
                                if (app.surfaces.fetchRemove(info.pty_id)) |entry| {
                                    log.info("Cleaning up surface for exited PTY {}", .{info.pty_id});
                                    entry.value.deinit();
                                    app.allocator.destroy(entry.value);
                                }
                                app.ui.update(.{ .pty_exited = .{ .id = info.pty_id, .status = info.status } }) catch |err| {
                                    log.err("Failed to update UI with pty_exited: {}", .{err});
                                };
                                // Delete session file when last PTY exits (if quit wasn't already called)
                                if (app.surfaces.count() == 0 and !app.state.should_quit) {
                                    // Cancel autosave timer to prevent it from recreating the file
                                    if (app.autosave_timer) |*task| {
                                        if (app.io_loop) |loop| task.cancel(loop) catch {};
                                        app.autosave_timer = null;
                                    }
                                    app.deleteCurrentSession();
                                }
                            },
                            .cwd_changed => |info| {
                                log.debug("CWD changed for PTY {}: {s}", .{ info.pty_id, info.cwd });
                                app.ui.update(.{ .cwd_changed = .{ .pty_id = info.pty_id, .cwd = info.cwd } }) catch |err| {
                                    log.err("Failed to update UI with cwd_changed: {}", .{err});
                                };
                            },
                            .detached => {
                                log.info("Detach complete, cleaning up {} surfaces", .{app.surfaces.count()});
                                // Clean up all surfaces before closing
                                var surface_it = app.surfaces.valueIterator();
                                while (surface_it.next()) |surface| {
                                    log.info("Cleaning up surface", .{});
                                    surface.*.deinit();
                                    app.allocator.destroy(surface.*);
                                }
                                log.info("Cleared surfaces", .{});
                                app.surfaces.clearRetainingCapacity();

                                app.state.should_quit = true;

                                // Cancel pending tasks before closing fd
                                if (app.recv_task) |*task| {
                                    task.cancel(l) catch {};
                                    app.recv_task = null;
                                }
                                if (app.render_timer) |*task| {
                                    task.cancel(l) catch {};
                                    app.render_timer = null;
                                }
                                if (app.autosave_timer) |*task| {
                                    task.cancel(l) catch {};
                                    app.autosave_timer = null;
                                }
                                if (app.pipe_read_task) |*task| {
                                    task.cancel(l) catch {};
                                    app.pipe_read_task = null;
                                }
                                if (app.send_task) |*task| {
                                    task.cancel(l) catch {};
                                    app.send_task = null;
                                }

                                log.info("Closing connection", .{});
                                if (app.connected) {
                                    posix.close(app.fd);
                                    app.connected = false;
                                }
                                // Wake up TTY thread so it can exit
                                log.info("Waking TTY thread", .{});
                                app.vx.deviceStatusReport(app.tty.writer()) catch {};
                                log.info("Returning from detach handler", .{});
                                return;
                            },
                            .color_query => |query| {
                                try app.handleColorQuery(query);
                            },
                            .server_info => {
                                try app.onServerInfoReceived();
                            },
                            .copy_to_clipboard => |text| {
                                app.copyToClipboard(text);
                            },
                            .none => {},
                        }

                        // Remove consumed bytes from buffer
                        if (bytes_consumed > 0) {
                            try app.msg_buffer.replaceRange(app.allocator, 0, bytes_consumed, &.{});
                        }
                    }

                    // Try to read more (but not if we're quitting - fd may be closed)
                    if (app.state.should_quit) break;
                    const n = posix.recv(app.fd, &app.recv_buffer, 0) catch |err| {
                        if (err == error.WouldBlock) break;
                        return err;
                    };

                    if (n == 0) {
                        log.info("Server closed connection", .{});
                        app.state.should_quit = true;
                        if (app.connected) {
                            posix.close(app.fd);
                            app.connected = false;
                        }
                        app.vx.deviceStatusReport(app.tty.writer()) catch {};
                        return;
                    }
                    current_bytes_read = n;
                }

                // Keep receiving unless we're quitting
                if (!app.state.should_quit) {
                    app.recv_task = try l.recv(app.fd, &app.recv_buffer, .{
                        .ptr = app,
                        .cb = onRecv,
                    });
                }
            },
            .err => |err| {
                log.err("Recv failed: {}", .{err});
                log.info("NOT resubmitting recv after error", .{});
                // Don't resubmit on error - let it drain
            },
            else => unreachable,
        }
    }

    fn sendDirect(self: *App, data: []const u8) !void {
        var index: usize = 0;
        while (index < data.len) {
            const n = posix.write(self.fd, data[index..]) catch |err| {
                if (err == error.WouldBlock) continue;
                return err;
            };
            index += n;
        }
    }

    pub fn spawnPty(self: *App, opts: layout_mod.SpawnOptions) !void {
        const msgid = self.state.next_msgid;
        self.state.next_msgid += 1;

        log.info("spawnPty: sending request msgid={}", .{msgid});
        try self.state.pending_requests.put(msgid, .{ .spawn = .{ .cwd = opts.cwd, .cmd = opts.cmd } });

        // Don't send client env — let the server use its own clean
        // environment with proper TERM=xterm-256color. Sending the host
        // env causes conflicts (TERM=xterm-kitty, KITTY_* vars, etc).
        var num_params: usize = 3;
        if (opts.cwd != null) num_params += 1;
        if (opts.cmd != null) num_params += 1;
        var map_items = try self.allocator.alloc(msgpack.Value.KeyValue, num_params);
        defer self.allocator.free(map_items);

        map_items[0] = .{ .key = .{ .string = "rows" }, .value = .{ .unsigned = opts.rows } };
        map_items[1] = .{ .key = .{ .string = "cols" }, .value = .{ .unsigned = opts.cols } };
        map_items[2] = .{ .key = .{ .string = "attach" }, .value = .{ .boolean = opts.attach } };
        var idx: usize = 3;
        if (opts.cwd) |cwd| {
            map_items[idx] = .{ .key = .{ .string = "cwd" }, .value = .{ .string = cwd } };
            idx += 1;
        }
        if (opts.cmd) |cmd| {
            map_items[idx] = .{ .key = .{ .string = "cmd" }, .value = .{ .string = cmd } };
        }

        const params = msgpack.Value{ .map = map_items };
        const msg = try msgpack.encode(self.allocator, .{ 0, msgid, "spawn_pty", params });
        defer self.allocator.free(msg);

        try self.sendDirect(msg);
    }

    fn cwdLookup(ctx: *anyopaque, id: i64) ?[]const u8 {
        const self: *App = @ptrCast(@alignCast(ctx));
        return self.state.cwd_map.get(id);
    }

    pub fn requestCopySelection(self: *App, pty_id: u32) !void {
        const msgid = self.state.next_msgid;
        self.state.next_msgid += 1;

        try self.state.pending_requests.put(msgid, .copy_selection);

        var params = try self.allocator.alloc(msgpack.Value, 1);
        defer self.allocator.free(params);
        params[0] = .{ .unsigned = pty_id };

        const msg = try msgpack.encode(self.allocator, .{ 0, msgid, "get_selection", msgpack.Value{ .array = params } });
        defer self.allocator.free(msg);

        try self.sendDirect(msg);
    }

    fn copyToClipboard(self: *App, text: []const u8) void {
        if (text.len == 0) {
            log.warn("copyToClipboard: empty text, nothing to copy", .{});
            return;
        }

        log.info("copyToClipboard: copying {} bytes to clipboard", .{text.len});

        // Try native clipboard utilities first (more reliable than OSC 52)
        self.copyToClipboardNative(text) catch |err| {
            log.warn("Native clipboard copy failed: {}, trying OSC 52", .{err});
            self.copyToClipboardOSC52(text);
        };
    }

    fn copyToClipboardNative(self: *App, text: []const u8) !void {
        const builtin = @import("builtin");
        const clipboard_cmd = switch (builtin.os.tag) {
            .macos => "pbcopy",
            .linux => blk: {
                // Try to detect which clipboard utility is available
                // Prefer wl-copy (Wayland) or xclip (X11)
                const wayland = std.process.hasEnvVar(self.allocator, "WAYLAND_DISPLAY") catch false;
                break :blk if (wayland) "wl-copy" else "xclip";
            },
            else => return error.UnsupportedPlatform,
        };

        const args = if (std.mem.eql(u8, clipboard_cmd, "xclip"))
            &[_][]const u8{ clipboard_cmd, "-selection", "clipboard" }
        else
            &[_][]const u8{clipboard_cmd};

        var child = std.process.Child.init(args, self.allocator);
        child.stdin_behavior = .Pipe;

        try child.spawn();

        try child.stdin.?.writeAll(text);
        child.stdin.?.close();
        child.stdin = null;

        const result = try child.wait();
        if (result != .Exited or result.Exited != 0) {
            return error.ClipboardCopyFailed;
        }

        log.info("Successfully copied to clipboard using {s}", .{clipboard_cmd});
    }

    fn copyToClipboardOSC52(self: *App, text: []const u8) void {
        const encoder = std.base64.standard.Encoder;
        const encoded_len = encoder.calcSize(text.len);
        const encoded = self.allocator.alloc(u8, encoded_len) catch |err| {
            log.err("Failed to allocate memory for OSC 52 encoding: {}", .{err});
            return;
        };
        defer self.allocator.free(encoded);
        _ = encoder.encode(encoded, text);

        const writer = self.tty.writer();
        writer.print("\x1b]52;c;{s}\x1b\\", .{encoded}) catch |err| {
            log.err("Failed to write OSC 52: {}", .{err});
        };
    }

    pub fn saveSession(self: *App, name: []const u8) !void {
        const json = try self.ui.getStateJson(&cwdLookup, self);
        defer self.allocator.free(json);

        // Inject pty_validity into the JSON
        const final_json = try self.injectPtyValidity(json);
        defer self.allocator.free(final_json);

        const home = std.posix.getenv("HOME") orelse return error.NoHomeDirectory;
        const state_dir = try std.fs.path.join(self.allocator, &.{ home, ".local", "state", "tuios", "sessions" });
        defer self.allocator.free(state_dir);

        std.fs.makeDirAbsolute(state_dir) catch |err| {
            if (err != error.PathAlreadyExists) {
                // Try creating parent directories
                const parent = try std.fs.path.join(self.allocator, &.{ home, ".local", "state", "tuios" });
                defer self.allocator.free(parent);
                std.fs.makeDirAbsolute(parent) catch |e| {
                    if (e != error.PathAlreadyExists) return e;
                };
                std.fs.makeDirAbsolute(state_dir) catch |e| {
                    if (e != error.PathAlreadyExists) return e;
                };
            }
        };

        const filename = try std.fmt.allocPrint(self.allocator, "{s}.json", .{name});
        defer self.allocator.free(filename);

        const path = try std.fs.path.join(self.allocator, &.{ state_dir, filename });
        defer self.allocator.free(path);

        const file = try std.fs.createFileAbsolute(path, .{});
        defer file.close();

        try file.writeAll(final_json);
        log.info("Session saved to {s}", .{path});
    }

    pub fn renameCurrentSession(self: *App, new_name: []const u8) !void {
        const old_name = self.current_session_name orelse return error.NoCurrentSession;
        try self.renameSession(old_name, new_name);
    }

    pub fn renameSession(self: *App, old_name: []const u8, new_name: []const u8) !void {
        const home = std.posix.getenv("HOME") orelse return error.NoHomeDirectory;
        const state_dir = try std.fs.path.join(self.allocator, &.{ home, ".local", "state", "tuios", "sessions" });
        defer self.allocator.free(state_dir);

        var dir = std.fs.openDirAbsolute(state_dir, .{}) catch |err| {
            if (err == error.FileNotFound) return error.SessionNotFound;
            return err;
        };
        defer dir.close();

        const old_filename = try std.fmt.allocPrint(self.allocator, "{s}.json", .{old_name});
        defer self.allocator.free(old_filename);

        const new_filename = try std.fmt.allocPrint(self.allocator, "{s}.json", .{new_name});
        defer self.allocator.free(new_filename);

        dir.access(new_filename, .{}) catch |err| {
            if (err != error.FileNotFound) return err;
            dir.rename(old_filename, new_filename) catch |rename_err| {
                return rename_err;
            };
            // Update current session name if renaming the current session
            if (self.current_session_name) |current| {
                if (std.mem.eql(u8, current, old_name)) {
                    self.allocator.free(current);
                    self.current_session_name = try self.allocator.dupe(u8, new_name);
                }
            }
            log.info("Renamed session '{s}' to '{s}'", .{ old_name, new_name });
            return;
        };

        return error.SessionAlreadyExists;
    }

    pub fn switchToSession(self: *App, target_session: []const u8) !void {
        std.debug.assert(target_session.len > 0);
        // Don't switch if already on the target session
        if (self.current_session_name) |current| {
            if (std.mem.eql(u8, current, target_session)) {
                log.info("Already on session '{s}', not switching", .{target_session});
                return;
            }
        }

        // Save current session first
        if (self.current_session_name) |name| {
            log.info("Saving current session '{s}' before switch", .{name});
            try self.saveSession(name);
        }

        // Build arguments for exec
        // Build arguments for exec
        const target_z = try self.allocator.dupeZ(u8, target_session);
        errdefer self.allocator.free(target_z);
        const args = [_]?[*:0]const u8{
            "tuios",
            "session",
            "attach",
            target_z,
            null,
        };

        log.info("Exec'ing tuios session attach '{s}'", .{target_session});

        // Restore terminal state right before exec to minimize window of failure
        const writer = self.tty.writer();
        self.vx.deinit(self.allocator, writer);

        // Use execvpeZ with current environment
        const err = posix.execvpeZ("tuios", @ptrCast(&args), @ptrCast(std.c.environ));

        // If we get here, exec failed - reinitialize terminal
        log.err("Failed to exec tuios: {}", .{err});
        self.vx = vaxis.Vaxis.init(self.allocator, .{}) catch return err;
        self.vx.enterAltScreen(writer) catch {};
        return err;
    }

    pub fn deleteCurrentSession(self: *App) void {
        const name = self.current_session_name orelse return;

        const home = std.posix.getenv("HOME") orelse return;
        const state_dir = std.fs.path.join(self.allocator, &.{ home, ".local", "state", "tuios", "sessions" }) catch return;
        defer self.allocator.free(state_dir);

        const filename = std.fmt.allocPrint(self.allocator, "{s}.json", .{name}) catch return;
        defer self.allocator.free(filename);

        var dir = std.fs.openDirAbsolute(state_dir, .{}) catch return;
        defer dir.close();

        dir.deleteFile(filename) catch |err| {
            log.warn("Failed to delete session file {s}: {}", .{ filename, err });
            return;
        };
        log.info("Deleted session file: {s}", .{filename});
    }

    pub fn deleteSession(self: *App, session_name: []const u8) !void {
        const home = std.posix.getenv("HOME") orelse return error.NoHomeDirectory;
        const state_dir = try std.fs.path.join(self.allocator, &.{ home, ".local", "state", "tuios", "sessions" });
        defer self.allocator.free(state_dir);

        // Validate session_name doesn't contain path separators
        if (std.mem.indexOfAny(u8, session_name, "/\\") != null) {
            return error.InvalidSessionName;
        }

        const filename = try std.fmt.allocPrint(self.allocator, "{s}.json", .{session_name});
        defer self.allocator.free(filename);

        var dir = try std.fs.openDirAbsolute(state_dir, .{});
        defer dir.close();

        try dir.deleteFile(filename);
        log.info("Deleted session file: {s}", .{filename});
    }

    const AUTOSAVE_DELAY_MS = 1000;

    pub fn scheduleAutoSave(self: *App) void {
        const name = self.current_session_name orelse return;
        const loop = self.io_loop orelse return;

        // Cancel existing timer if any
        if (self.autosave_timer) |*task| {
            task.cancel(loop) catch {};
            self.autosave_timer = null;
        }

        // Schedule new timer (timeout takes nanoseconds)
        self.autosave_timer = loop.timeout(AUTOSAVE_DELAY_MS * std.time.ns_per_ms, .{
            .ptr = self,
            .cb = onAutoSaveTimer,
        }) catch |err| {
            log.warn("Failed to schedule autosave timer: {}", .{err});
            return;
        };
        _ = name;
    }

    fn onAutoSaveTimer(loop: *io.Loop, completion: io.Completion) anyerror!void {
        _ = loop;
        const self = completion.userdataCast(App);
        self.autosave_timer = null;

        const name = self.current_session_name orelse return;
        self.saveSession(name) catch |err| {
            log.warn("Auto-save failed: {}", .{err});
        };
    }

    fn injectPtyValidity(self: *App, json: []const u8) ![]u8 {
        const validity = self.state.pty_validity orelse {
            return self.allocator.dupe(u8, json);
        };

        // Simple approach: find the opening brace and inject pty_validity right after
        // JSON from Lua is always a top-level object starting with '{'
        if (json.len < 2 or json[0] != '{') {
            return self.allocator.dupe(u8, json);
        }

        // Build: {"pty_validity":12345,<rest of original json without leading brace>
        var result = std.ArrayList(u8).empty;
        errdefer result.deinit(self.allocator);

        try result.appendSlice(self.allocator, "{\"pty_validity\":");
        var buf: [32]u8 = undefined;
        const validity_str = std.fmt.bufPrint(&buf, "{d}", .{validity}) catch unreachable;
        try result.appendSlice(self.allocator, validity_str);

        // If original has content after '{', add comma and append rest
        if (json.len > 2) {
            // Check if there's content after the opening brace
            const rest = std.mem.trimLeft(u8, json[1..], " \t\n\r");
            if (rest.len > 0 and rest[0] != '}') {
                try result.append(self.allocator, ',');
            }
            try result.appendSlice(self.allocator, json[1..]);
        } else {
            try result.append(self.allocator, '}');
        }

        return result.toOwnedSlice(self.allocator);
    }

    pub fn loadSession(self: *App, name: []const u8) !void {
        const home = std.posix.getenv("HOME") orelse return error.NoHomeDirectory;

        const filename = try std.fmt.allocPrint(self.allocator, "{s}.json", .{name});
        defer self.allocator.free(filename);

        const path = try std.fs.path.join(self.allocator, &.{ home, ".local", "state", "tuios", "sessions", filename });
        defer self.allocator.free(path);

        const file = std.fs.openFileAbsolute(path, .{}) catch |err| {
            log.err("Failed to open session file {s}: {}", .{ path, err });
            return err;
        };
        defer file.close();

        const json = try file.readToEndAlloc(self.allocator, 1024 * 1024);
        defer self.allocator.free(json);

        try self.ui.setStateFromJson(json, ptyLookup, self);
        log.info("Session loaded from {s}", .{path});
    }

    fn extractPtyIdsFromJson(allocator: std.mem.Allocator, json: []const u8) ![]u32 {
        const parsed = try std.json.parseFromSlice(std.json.Value, allocator, json, .{});
        defer parsed.deinit();

        var ids: std.ArrayList(u32) = .empty;
        errdefer ids.deinit(allocator);

        try collectPtyIds(&ids, allocator, parsed.value);
        return ids.toOwnedSlice(allocator);
    }

    fn collectPtyIds(ids: *std.ArrayList(u32), allocator: std.mem.Allocator, value: std.json.Value) !void {
        switch (value) {
            .object => |obj| {
                if (obj.get("type")) |type_val| {
                    if (type_val == .string and std.mem.eql(u8, type_val.string, "pane")) {
                        if (obj.get("pty_id")) |pty_id_val| {
                            if (pty_id_val == .integer) {
                                try ids.append(allocator, @intCast(pty_id_val.integer));
                            }
                        }
                    }
                }
                var it = obj.iterator();
                while (it.next()) |entry| {
                    try collectPtyIds(ids, allocator, entry.value_ptr.*);
                }
            },
            .array => |arr| {
                for (arr.items) |item| {
                    try collectPtyIds(ids, allocator, item);
                }
            },
            else => {},
        }
    }

    fn extractPtyIdCwdPairs(allocator: std.mem.Allocator, json: []const u8) !std.AutoHashMap(u32, []const u8) {
        const parsed = try std.json.parseFromSlice(std.json.Value, allocator, json, .{});
        defer parsed.deinit();

        var pairs: std.AutoHashMap(u32, []const u8) = .init(allocator);
        errdefer {
            var it = pairs.valueIterator();
            while (it.next()) |val| {
                allocator.free(val.*);
            }
            pairs.deinit();
        }

        try collectPtyIdCwd(&pairs, allocator, parsed.value);
        return pairs;
    }

    fn collectPtyIdCwd(pairs: *std.AutoHashMap(u32, []const u8), allocator: std.mem.Allocator, value: std.json.Value) !void {
        switch (value) {
            .object => |obj| {
                if (obj.get("type")) |type_val| {
                    if (type_val == .string and std.mem.eql(u8, type_val.string, "pane")) {
                        if (obj.get("pty_id")) |pty_id_val| {
                            if (pty_id_val == .integer) {
                                const pty_id: u32 = @intCast(pty_id_val.integer);
                                var cwd: ?[]const u8 = null;
                                if (obj.get("cwd")) |cwd_val| {
                                    if (cwd_val == .string) {
                                        cwd = try allocator.dupe(u8, cwd_val.string);
                                    }
                                }
                                try pairs.put(pty_id, cwd orelse try allocator.dupe(u8, ""));
                            }
                        }
                    }
                }
                var it = obj.iterator();
                while (it.next()) |entry| {
                    try collectPtyIdCwd(pairs, allocator, entry.value_ptr.*);
                }
            },
            .array => |arr| {
                for (arr.items) |item| {
                    try collectPtyIdCwd(pairs, allocator, item);
                }
            },
            else => {},
        }
    }

    fn ptyLookup(ctx: *anyopaque, id: u32) ?Layout.PtyLookupResult {
        const self: *App = @ptrCast(@alignCast(ctx));

        // Check if this ID was remapped (old PTY ID -> new PTY ID due to validity mismatch)
        const lookup_id = self.pty_id_remap.get(id) orelse id;
        const surface = self.surfaces.get(lookup_id) orelse return null;

        return .{
            .pty_id = lookup_id,
            .surface = surface,
            .app = self,
            .send_key_fn = struct {
                fn sendKey(app_ctx: *anyopaque, pty_id: u32, key: layout_mod.KeyData) anyerror!void {
                    const app: *App = @ptrCast(@alignCast(app_ctx));

                    var key_map_kv = try app.allocator.alloc(msgpack.Value.KeyValue, 6);
                    key_map_kv[0] = .{ .key = .{ .string = "key" }, .value = .{ .string = key.key } };
                    key_map_kv[1] = .{ .key = .{ .string = "code" }, .value = .{ .string = key.code } };
                    key_map_kv[2] = .{ .key = .{ .string = "shiftKey" }, .value = .{ .boolean = key.shift } };
                    key_map_kv[3] = .{ .key = .{ .string = "ctrlKey" }, .value = .{ .boolean = key.ctrl } };
                    key_map_kv[4] = .{ .key = .{ .string = "altKey" }, .value = .{ .boolean = key.alt } };
                    key_map_kv[5] = .{ .key = .{ .string = "metaKey" }, .value = .{ .boolean = key.super } };

                    const key_map_val: msgpack.Value = .{ .map = key_map_kv };

                    var params = try app.allocator.alloc(msgpack.Value, 2);
                    params[0] = .{ .unsigned = @intCast(pty_id) };
                    params[1] = key_map_val;

                    const method: []const u8 = if (key.release) "key_release" else "key_input";

                    var arr = try app.allocator.alloc(msgpack.Value, 3);
                    arr[0] = .{ .unsigned = 2 };
                    arr[1] = .{ .string = method };
                    arr[2] = .{ .array = params };

                    const encoded_msg = try msgpack.encodeFromValue(app.allocator, .{ .array = arr });
                    defer app.allocator.free(encoded_msg);

                    app.allocator.free(arr);
                    app.allocator.free(params);
                    app.allocator.free(key_map_kv);

                    try app.sendDirect(encoded_msg);
                }
            }.sendKey,
            .send_mouse_fn = struct {
                fn sendMouse(app_ctx: *anyopaque, pty_id: u32, mouse: layout_mod.MouseData) anyerror!void {
                    const app: *App = @ptrCast(@alignCast(app_ctx));

                    var mouse_map_kv = try app.allocator.alloc(msgpack.Value.KeyValue, 7);
                    mouse_map_kv[0] = .{ .key = .{ .string = "x" }, .value = .{ .float = mouse.x } };
                    mouse_map_kv[1] = .{ .key = .{ .string = "y" }, .value = .{ .float = mouse.y } };
                    mouse_map_kv[2] = .{ .key = .{ .string = "button" }, .value = .{ .string = mouse.button } };
                    mouse_map_kv[3] = .{ .key = .{ .string = "event_type" }, .value = .{ .string = mouse.event_type } };
                    mouse_map_kv[4] = .{ .key = .{ .string = "shiftKey" }, .value = .{ .boolean = mouse.shift } };
                    mouse_map_kv[5] = .{ .key = .{ .string = "ctrlKey" }, .value = .{ .boolean = mouse.ctrl } };
                    mouse_map_kv[6] = .{ .key = .{ .string = "altKey" }, .value = .{ .boolean = mouse.alt } };

                    const mouse_map_val: msgpack.Value = .{ .map = mouse_map_kv };

                    var params = try app.allocator.alloc(msgpack.Value, 2);
                    params[0] = .{ .unsigned = @intCast(pty_id) };
                    params[1] = mouse_map_val;

                    var arr = try app.allocator.alloc(msgpack.Value, 3);
                    arr[0] = .{ .unsigned = 2 };
                    arr[1] = .{ .string = "mouse_input" };
                    arr[2] = .{ .array = params };

                    const encoded_msg = try msgpack.encodeFromValue(app.allocator, .{ .array = arr });
                    defer app.allocator.free(encoded_msg);

                    app.allocator.free(arr);
                    app.allocator.free(params);
                    app.allocator.free(mouse_map_kv);

                    try app.sendDirect(encoded_msg);
                }
            }.sendMouse,
            .send_paste_fn = struct {
                fn sendPaste(app_ctx: *anyopaque, pty_id: u32, data: []const u8) anyerror!void {
                    const app: *App = @ptrCast(@alignCast(app_ctx));

                    var params = try app.allocator.alloc(msgpack.Value, 2);
                    params[0] = .{ .unsigned = @intCast(pty_id) };
                    params[1] = .{ .binary = data };

                    var arr = try app.allocator.alloc(msgpack.Value, 3);
                    arr[0] = .{ .unsigned = 2 }; // notification
                    arr[1] = .{ .string = "paste_input" };
                    arr[2] = .{ .array = params };

                    const encoded_msg = try msgpack.encodeFromValue(app.allocator, .{ .array = arr });
                    defer app.allocator.free(encoded_msg);

                    app.allocator.free(arr);
                    app.allocator.free(params);

                    try app.sendDirect(encoded_msg);
                    log.debug("Sent paste_input: {} bytes to pty {}", .{ data.len, pty_id });
                }
            }.sendPaste,
            .set_focus_fn = struct {
                fn sendFocus(app_ctx: *anyopaque, pty_id: u32, focused: bool) anyerror!void {
                    const app: *App = @ptrCast(@alignCast(app_ctx));

                    var params = try app.allocator.alloc(msgpack.Value, 2);
                    params[0] = .{ .unsigned = @intCast(pty_id) };
                    params[1] = .{ .boolean = focused };

                    var arr = try app.allocator.alloc(msgpack.Value, 3);
                    arr[0] = .{ .unsigned = 2 }; // notification
                    arr[1] = .{ .string = "focus_event" };
                    arr[2] = .{ .array = params };

                    const encoded_msg = try msgpack.encodeFromValue(app.allocator, .{ .array = arr });
                    defer app.allocator.free(encoded_msg);

                    app.allocator.free(arr);
                    app.allocator.free(params);

                    try app.sendDirect(encoded_msg);
                    log.debug("Sent focus_event: {} to pty {}", .{ focused, pty_id });
                }
            }.sendFocus,
            .close_fn = struct {
                fn closePty(app_ctx: *anyopaque, pty_id: u32) anyerror!void {
                    const app: *App = @ptrCast(@alignCast(app_ctx));

                    var params = try app.allocator.alloc(msgpack.Value, 1);
                    params[0] = .{ .unsigned = @intCast(pty_id) };

                    // Use 0 for request type (it's a request, but we don't wait for response here as it's fire-and-forget from Lua perspective)
                    // Actually, let's use request type 0 and a dummy msgid, or just notification type 2?
                    // Server handles "close_pty" as a request (returns msgpack.Value.nil).
                    // So we should send a request.
                    const msgid = app.state.next_msgid;
                    app.state.next_msgid +%= 1;

                    var arr = try app.allocator.alloc(msgpack.Value, 4);
                    arr[0] = .{ .unsigned = 0 }; // request
                    arr[1] = .{ .unsigned = msgid };
                    arr[2] = .{ .string = "close_pty" };
                    arr[3] = .{ .array = params };

                    const encoded_msg = try msgpack.encodeFromValue(app.allocator, .{ .array = arr });
                    defer app.allocator.free(encoded_msg);

                    app.allocator.free(arr);
                    app.allocator.free(params);

                    try app.sendDirect(encoded_msg);
                }
            }.closePty,
            .cwd_fn = struct {
                fn getCwd(app_ctx: *anyopaque, pty_id: u32) ?[]const u8 {
                    const app: *App = @ptrCast(@alignCast(app_ctx));
                    return app.state.cwd_map.get(@intCast(pty_id));
                }
            }.getCwd,
            .copy_selection_fn = struct {
                fn copySelection(app_ctx: *anyopaque, pty_id: u32) anyerror!void {
                    const app: *App = @ptrCast(@alignCast(app_ctx));
                    try app.requestCopySelection(pty_id);
                }
            }.copySelection,
            .cell_size_fn = struct {
                fn getCellSize(app_ctx: *anyopaque) layout_mod.CellSize {
                    const app: *App = @ptrCast(@alignCast(app_ctx));
                    return .{
                        .width = app.cell_width_px,
                        .height = app.cell_height_px,
                    };
                }
            }.getCellSize,
        };
    }
};

test "ClientLogic - encodeEvent" {
    const testing = std.testing;
    const allocator = testing.allocator;

    // Test Key Press
    {
        const event = vaxis.Event{
            .key_press = .{
                .codepoint = 'a',
                .mods = .{},
            },
        };
        const msg = (try ClientLogic.encodeEvent(allocator, event)).?;
        defer allocator.free(msg);

        const val = try msgpack.decode(allocator, msg);
        defer val.deinit(allocator);

        try testing.expectEqual(std.meta.Tag(msgpack.Value).array, std.meta.activeTag(val));
        try testing.expectEqual(2, val.array.len);
        try testing.expectEqualStrings("key", val.array[0].string);
        try testing.expectEqual(std.meta.Tag(msgpack.Value).map, std.meta.activeTag(val.array[1]));
    }

    // Test Resize
    {
        const event = vaxis.Event{
            .winsize = .{
                .rows = 24,
                .cols = 80,
                .x_pixel = 0,
                .y_pixel = 0,
            },
        };
        const msg = (try ClientLogic.encodeEvent(allocator, event)).?;
        defer allocator.free(msg);

        const val = try msgpack.decode(allocator, msg);
        defer val.deinit(allocator);

        try testing.expectEqual(std.meta.Tag(msgpack.Value).array, std.meta.activeTag(val));
        try testing.expectEqual(3, val.array.len);
        try testing.expectEqualStrings("resize", val.array[0].string);
        try testing.expectEqual(24, val.array[1].unsigned);
        try testing.expectEqual(80, val.array[2].unsigned);
    }
}

test "ClientLogic - processServerMessage" {
    const testing = std.testing;
    // const allocator = testing.allocator; // Unused in this test

    // Test PTY spawn response (integer)
    {
        var state = ClientState.init(testing.allocator);
        defer state.deinit();
        // Manually add a pending spawn request
        try state.pending_requests.put(1, .{ .spawn = .{} });

        const msg = rpc.Message{
            .response = .{
                .msgid = 1,
                .err = null,
                .result = .{ .integer = 123 },
            },
        };

        const action = try ClientLogic.processServerMessage(&state, msg);
        try testing.expect(state.response_received);
        try testing.expectEqual(123, state.pty_id.?);
        try testing.expectEqual(std.meta.Tag(ServerAction).attached, std.meta.activeTag(action));
        try testing.expectEqual(123, action.attached.new_pty_id);
        try testing.expectEqual(null, action.attached.old_pty_id);
    }

    // Test Attach response (already have pty_id)
    {
        var state = ClientState.init(testing.allocator);
        defer state.deinit();
        try state.pending_requests.put(2, .{ .attach = .{ .pty_id = 123, .cwd = null } });

        const msg = rpc.Message{
            .response = .{
                .msgid = 2,
                .err = null,
                .result = .{ .integer = 0 }, // Result of attach is typically success/pty_id
            },
        };

        const action = try ClientLogic.processServerMessage(&state, msg);
        try testing.expect(state.attached);
        try testing.expectEqual(std.meta.Tag(ServerAction).attached, std.meta.activeTag(action));
        try testing.expectEqual(123, action.attached.new_pty_id);
    }

    // Test Redraw Notification
    {
        var state = ClientState.init(testing.allocator);
        defer state.deinit();
        // Params: [events...]
        const params = msgpack.Value{ .array = &[_]msgpack.Value{} };
        const msg = rpc.Message{
            .notification = .{
                .method = "redraw",
                .params = params,
            },
        };

        const action = try ClientLogic.processServerMessage(&state, msg);
        try testing.expectEqual(std.meta.Tag(ServerAction).redraw, std.meta.activeTag(action));
    }
}

test "ClientLogic - processPipeMessage" {
    const testing = std.testing;
    const allocator = testing.allocator;

    // Test Quit
    {
        var state = ClientState.init(testing.allocator);
        defer state.deinit();
        const encoded = try msgpack.encode(allocator, .{"quit"});
        defer allocator.free(encoded);

        const quit_val = try msgpack.decode(allocator, encoded);
        defer quit_val.deinit(allocator);

        const action = try ClientLogic.processPipeMessage(&state, quit_val);
        try testing.expect(state.should_quit);
        try testing.expectEqual(std.meta.Tag(PipeAction).quit, std.meta.activeTag(action));
    }

    // Test Key Input
    {
        var state = ClientState.init(testing.allocator);
        defer state.deinit();
        state.attached = true;
        state.pty_id = 123;

        // Create a proper msgpack map value
        var key_map_kv = try allocator.alloc(msgpack.Value.KeyValue, 5);
        key_map_kv[0] = .{ .key = .{ .string = "key" }, .value = .{ .string = "a" } };
        key_map_kv[1] = .{ .key = .{ .string = "shiftKey" }, .value = .{ .boolean = false } };
        key_map_kv[2] = .{ .key = .{ .string = "ctrlKey" }, .value = .{ .boolean = false } };
        key_map_kv[3] = .{ .key = .{ .string = "altKey" }, .value = .{ .boolean = false } };
        key_map_kv[4] = .{ .key = .{ .string = "metaKey" }, .value = .{ .boolean = false } };

        const key_map_val: msgpack.Value = .{ .map = key_map_kv };
        var arr = try allocator.alloc(msgpack.Value, 2);
        arr[0] = .{ .string = "key" };
        arr[1] = key_map_val;
        const encoded = try msgpack.encodeFromValue(allocator, .{ .array = arr });
        defer allocator.free(encoded);

        const key_val = try msgpack.decode(allocator, encoded);
        defer key_val.deinit(allocator);
        defer allocator.free(arr);
        defer allocator.free(key_map_kv);

        const action = try ClientLogic.processPipeMessage(&state, key_val);
        try testing.expectEqual(std.meta.Tag(PipeAction).send_key, std.meta.activeTag(action));
        try testing.expect(action.send_key == .map);
    }

    // Test Resize
    {
        var state = ClientState.init(testing.allocator);
        defer state.deinit();

        const encoded = try msgpack.encode(allocator, .{ "resize", 24, 80 });
        defer allocator.free(encoded);

        const resize_val = try msgpack.decode(allocator, encoded);
        defer resize_val.deinit(allocator);

        const action = try ClientLogic.processPipeMessage(&state, resize_val);
        try testing.expectEqual(std.meta.Tag(PipeAction).send_resize, std.meta.activeTag(action));
        try testing.expectEqual(24, action.send_resize.rows);
        try testing.expectEqual(80, action.send_resize.cols);
    }
}

test "ClientLogic - shouldFlush" {
    const testing = std.testing;
    const allocator = testing.allocator;

    // Test with flush event
    {
        const flush_event = try msgpack.encode(allocator, .{"flush"});
        defer allocator.free(flush_event);

        const flush_val = try msgpack.decode(allocator, flush_event);
        defer flush_val.deinit(allocator);

        const events = try allocator.alloc(msgpack.Value, 1);
        events[0] = flush_val;

        const params: msgpack.Value = .{ .array = events };
        defer allocator.free(events);

        try testing.expect(ClientLogic.shouldFlush(params));
    }

    // Test without flush event
    {
        const other_event = try msgpack.encode(allocator, .{"other"});
        defer allocator.free(other_event);

        const other_val = try msgpack.decode(allocator, other_event);
        defer other_val.deinit(allocator);

        const events = try allocator.alloc(msgpack.Value, 1);
        events[0] = other_val;

        const params: msgpack.Value = .{ .array = events };
        defer allocator.free(events);

        try testing.expect(!ClientLogic.shouldFlush(params));
    }

    // Test empty events
    {
        const events = try allocator.alloc(msgpack.Value, 0);
        const params: msgpack.Value = .{ .array = events };
        defer allocator.free(events);

        try testing.expect(!ClientLogic.shouldFlush(params));
    }
}

test "UnixSocketClient - successful connection" {
    const testing = std.testing;

    var loop = try io.Loop.init(testing.allocator);
    defer loop.deinit();

    var connected = false;
    var fd: posix.socket_t = undefined;

    const State = struct {
        connected: *bool,
        fd: *posix.socket_t,
    };

    var state = State{
        .connected = &connected,
        .fd = &fd,
    };

    const callback = struct {
        fn cb(l: *io.Loop, completion: io.Completion) anyerror!void {
            _ = l;
            const s = completion.userdataCast(State);
            switch (completion.result) {
                .socket => |socket_fd| {
                    s.fd.* = socket_fd;
                    s.connected.* = true;
                },
                .err => |err| return err,
                else => unreachable,
            }
        }
    }.cb;

    _ = try connectUnixSocket(&loop, "/tmp/test.sock", .{
        .ptr = &state,
        .cb = callback,
    });

    try loop.run(.once);
    try testing.expect(!connected);

    const socket_fd = blk: {
        var it = loop.pending.iterator();
        while (it.next()) |entry| {
            if (entry.value_ptr.kind == .connect) {
                break :blk entry.value_ptr.fd;
            }
        }
        unreachable;
    };

    try loop.completeConnect(socket_fd);
    try loop.run(.once);
    try testing.expect(connected);
    try testing.expectEqual(socket_fd, fd);
}

test "UnixSocketClient - connection refused" {
    const testing = std.testing;

    var loop = try io.Loop.init(testing.allocator);
    defer loop.deinit();

    var got_error = false;
    var err_value: ?anyerror = null;

    const State = struct {
        got_error: *bool,
        err_value: *?anyerror,
    };

    var state = State{
        .got_error = &got_error,
        .err_value = &err_value,
    };

    const callback = struct {
        fn cb(l: *io.Loop, completion: io.Completion) anyerror!void {
            _ = l;
            const s = completion.userdataCast(State);
            switch (completion.result) {
                .socket => {},
                .err => |err| {
                    s.got_error.* = true;
                    s.err_value.* = err;
                },
                else => unreachable,
            }
        }
    }.cb;

    _ = try connectUnixSocket(&loop, "/tmp/test.sock", .{
        .ptr = &state,
        .cb = callback,
    });

    try loop.run(.once);

    const socket_fd = blk: {
        var it = loop.pending.iterator();
        while (it.next()) |entry| {
            if (entry.value_ptr.kind == .connect) {
                break :blk entry.value_ptr.fd;
            }
        }
        unreachable;
    };

    try loop.completeWithError(socket_fd, error.ConnectionRefused);
    try loop.run(.until_done);
    try testing.expect(got_error);
    try testing.expectEqual(error.ConnectionRefused, err_value.?);
}
