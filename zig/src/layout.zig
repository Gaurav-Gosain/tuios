//! Native Zig layout system replacing Lua-based UI.
//! Manages terminal surfaces, BSP tiling, workspaces, keybinds,
//! and mode switching (terminal/window management).

const std = @import("std");
const vaxis = @import("vaxis");

const widget = @import("widget.zig");
const Surface = @import("Surface.zig");
const io = @import("io.zig");

const log = std.log.scoped(.layout);

// ---- Event types (replaces lua_event.zig) ----

pub const CellSize = struct {
    width: u16,
    height: u16,
};

pub const PtyAttachInfo = struct {
    id: u32,
    surface: *Surface,
    app: *anyopaque,
    send_key_fn: *const fn (app: *anyopaque, id: u32, key: KeyData) anyerror!void,
    send_mouse_fn: *const fn (app: *anyopaque, id: u32, mouse: MouseData) anyerror!void,
    send_paste_fn: *const fn (app: *anyopaque, id: u32, data: []const u8) anyerror!void,
    set_focus_fn: *const fn (app: *anyopaque, id: u32, focused: bool) anyerror!void,
    close_fn: *const fn (app: *anyopaque, id: u32) anyerror!void,
    cwd_fn: *const fn (app: *anyopaque, id: u32) ?[]const u8,
    copy_selection_fn: *const fn (app: *anyopaque, id: u32) anyerror!void,
    cell_size_fn: *const fn (app: *anyopaque) CellSize,
};

pub const PtyExitedInfo = struct {
    id: u32,
    status: u32,
};

pub const CwdChangedInfo = struct {
    pty_id: u32,
    cwd: []const u8,
};

pub const Event = union(enum) {
    vaxis: vaxis.Event,
    mouse: MouseEvent,
    split_resize: SplitResizeEvent,
    paste: []const u8,
    pty_attach: PtyAttachInfo,
    pty_exited: PtyExitedInfo,
    cwd_changed: CwdChangedInfo,
    init: void,
};

pub const SplitResizeEvent = struct {
    parent_id: ?u32,
    child_index: u16,
    ratio: f32,
};

pub const MouseEvent = struct {
    x: f64,
    y: f64,
    button: vaxis.Mouse.Button,
    action: vaxis.Mouse.Type,
    mods: vaxis.Mouse.Modifiers,
    target: ?u32,
    target_x: ?f64,
    target_y: ?f64,
};

pub const KeyData = struct {
    key: []const u8,
    code: []const u8,
    ctrl: bool,
    alt: bool,
    shift: bool,
    super: bool,
    release: bool = false,
};

pub const MouseData = struct {
    x: f64,
    y: f64,
    button: []const u8,
    event_type: []const u8,
    ctrl: bool,
    alt: bool,
    shift: bool,
};

pub const SpawnOptions = struct {
    rows: u16,
    cols: u16,
    attach: bool,
    cwd: ?[]const u8 = null,
    cmd: ?[]const u8 = null,
};

// ---- Pty wrapper (holds send callbacks for a single PTY) ----

const Pty = struct {
    id: u32,
    surface: *Surface,
    app: *anyopaque,
    send_key_fn: *const fn (*anyopaque, u32, KeyData) anyerror!void,
    send_mouse_fn: *const fn (*anyopaque, u32, MouseData) anyerror!void,
    send_paste_fn: *const fn (*anyopaque, u32, []const u8) anyerror!void,
    set_focus_fn: *const fn (*anyopaque, u32, bool) anyerror!void,
    close_fn: *const fn (*anyopaque, u32) anyerror!void,
    cwd_fn: *const fn (*anyopaque, u32) ?[]const u8,
    copy_selection_fn: *const fn (*anyopaque, u32) anyerror!void,
    cell_size_fn: *const fn (*anyopaque) CellSize,
};

// ---- Layout ----

pub const Layout = struct {
    allocator: std.mem.Allocator,
    loop: ?*io.Loop = null,

    // Callbacks (set by client.zig)
    exit_callback: ?*const fn (ctx: *anyopaque) void = null,
    exit_ctx: *anyopaque = undefined,
    spawn_callback: ?*const fn (ctx: *anyopaque, opts: SpawnOptions) anyerror!void = null,
    spawn_ctx: *anyopaque = undefined,
    redraw_callback: ?*const fn (ctx: *anyopaque) void = null,
    redraw_ctx: *anyopaque = undefined,
    detach_callback: ?*const fn (ctx: *anyopaque, session_name: []const u8) anyerror!void = null,
    detach_ctx: *anyopaque = undefined,
    save_callback: ?*const fn (ctx: *anyopaque) void = null,
    save_ctx: *anyopaque = undefined,
    switch_session_callback: ?*const fn (ctx: *anyopaque, target: []const u8) anyerror!void = null,
    switch_session_ctx: *anyopaque = undefined,
    get_session_name_callback: ?*const fn (ctx: *anyopaque) ?[]const u8 = null,
    get_session_name_ctx: *anyopaque = undefined,
    rename_session_callback: ?*const fn (ctx: *anyopaque, old: []const u8, new: []const u8) anyerror!void = null,
    rename_session_ctx: *anyopaque = undefined,
    delete_session_callback: ?*const fn (ctx: *anyopaque, name: []const u8) anyerror!void = null,
    delete_session_ctx: *anyopaque = undefined,

    // State
    ptys: std.AutoArrayHashMap(u32, Pty),
    focused_id: ?u32 = null,
    screen_cols: u16 = 0,
    screen_rows: u16 = 0,
    needs_initial_spawn: bool = false,

    pub const InitResult = union(enum) {
        ok: Layout,
        err: struct { err: anyerror, lua_msg: ?[:0]const u8 },
    };

    pub fn init(allocator: std.mem.Allocator) InitResult {
        return .{ .ok = .{
            .allocator = allocator,
            .ptys = std.AutoArrayHashMap(u32, Pty).init(allocator),
        } };
    }

    pub fn deinit(self: *Layout) void {
        self.ptys.deinit();
    }

    // ---- Callback setters (mirror ui.zig interface) ----

    pub fn setLoop(self: *Layout, loop: *io.Loop) void {
        self.loop = loop;
    }

    pub fn setExitCallback(self: *Layout, ctx: *anyopaque, cb: *const fn (ctx: *anyopaque) void) void {
        self.exit_ctx = ctx;
        self.exit_callback = cb;
    }

    pub fn setSpawnCallback(self: *Layout, ctx: *anyopaque, cb: *const fn (ctx: *anyopaque, opts: SpawnOptions) anyerror!void) void {
        self.spawn_ctx = ctx;
        self.spawn_callback = cb;
    }

    pub fn setRedrawCallback(self: *Layout, ctx: *anyopaque, cb: *const fn (ctx: *anyopaque) void) void {
        self.redraw_ctx = ctx;
        self.redraw_callback = cb;
    }

    pub fn setDetachCallback(self: *Layout, ctx: *anyopaque, cb: *const fn (ctx: *anyopaque, session_name: []const u8) anyerror!void) void {
        self.detach_ctx = ctx;
        self.detach_callback = cb;
    }

    pub fn setSaveCallback(self: *Layout, ctx: *anyopaque, cb: *const fn (ctx: *anyopaque) void) void {
        self.save_ctx = ctx;
        self.save_callback = cb;
    }

    pub fn setGetSessionNameCallback(self: *Layout, ctx: *anyopaque, cb: *const fn (ctx: *anyopaque) ?[]const u8) void {
        self.get_session_name_ctx = ctx;
        self.get_session_name_callback = cb;
    }

    pub fn setRenameSessionCallback(self: *Layout, ctx: *anyopaque, cb: *const fn (ctx: *anyopaque, old: []const u8, new: []const u8) anyerror!void) void {
        self.rename_session_ctx = ctx;
        self.rename_session_callback = cb;
    }

    pub fn setDeleteSessionCallback(self: *Layout, ctx: *anyopaque, cb: *const fn (ctx: *anyopaque, name: []const u8) anyerror!void) void {
        self.delete_session_ctx = ctx;
        self.delete_session_callback = cb;
    }

    pub fn setSwitchSessionCallback(self: *Layout, ctx: *anyopaque, cb: *const fn (ctx: *anyopaque, target: []const u8) anyerror!void) void {
        self.switch_session_ctx = ctx;
        self.switch_session_callback = cb;
    }

    // ---- Event processing ----

    pub fn update(self: *Layout, event: Event) !void {
        switch (event) {
            .pty_attach => |info| {
                try self.ptys.put(info.id, .{
                    .id = info.id,
                    .surface = info.surface,
                    .app = info.app,
                    .send_key_fn = info.send_key_fn,
                    .send_mouse_fn = info.send_mouse_fn,
                    .send_paste_fn = info.send_paste_fn,
                    .set_focus_fn = info.set_focus_fn,
                    .close_fn = info.close_fn,
                    .cwd_fn = info.cwd_fn,
                    .copy_selection_fn = info.copy_selection_fn,
                    .cell_size_fn = info.cell_size_fn,
                });
                // Auto-focus first PTY
                if (self.focused_id == null) {
                    self.focused_id = info.id;
                }
                // Set focus on the PTY
                info.set_focus_fn(info.app, info.id, self.focused_id == info.id) catch {};
                self.requestRedraw();
            },
            .pty_exited => |info| {
                _ = self.ptys.orderedRemove(info.id);
                // If focused PTY exited, focus next one
                if (self.focused_id == info.id) {
                    self.focused_id = if (self.ptys.count() > 0)
                        self.ptys.keys()[0]
                    else
                        null;
                }
                // If no more PTYs, exit
                if (self.ptys.count() == 0) {
                    if (self.exit_callback) |cb| cb(self.exit_ctx);
                }
                self.requestRedraw();
            },
            .vaxis => |vx_event| {
                switch (vx_event) {
                    .winsize => |ws| {
                        self.screen_cols = ws.cols;
                        self.screen_rows = ws.rows;
                        // Spawn initial terminal once we have real dimensions
                        if (self.needs_initial_spawn and ws.cols > 0 and ws.rows > 0) {
                            self.needs_initial_spawn = false;
                            log.info("Spawning initial terminal: {}x{}", .{ ws.cols, ws.rows });
                            if (self.spawn_callback) |cb| {
                                cb(self.spawn_ctx, .{
                                    .rows = ws.rows,
                                    .cols = ws.cols,
                                    .attach = true,
                                }) catch |err| {
                                    log.err("Failed to spawn initial terminal: {}", .{err});
                                };
                            }
                        }
                        self.requestRedraw();
                    },
                    .key_press => |key| {
                        try self.handleKeyPress(key, false);
                    },
                    .key_release => |key| {
                        try self.handleKeyPress(key, true);
                    },
                    .mouse => |mouse| {
                        // Forward mouse to focused PTY
                        try self.handleMouse(mouse);
                    },
                    else => {},
                }
            },
            .mouse => |mouse_event| {
                // Pre-processed mouse event with hit-test results
                if (mouse_event.target) |target_id| {
                    if (self.ptys.get(target_id)) |pty| {
                        pty.send_mouse_fn(pty.app, pty.id, .{
                            .x = mouse_event.target_x orelse 0,
                            .y = mouse_event.target_y orelse 0,
                            .button = @tagName(mouse_event.button),
                            .event_type = @tagName(mouse_event.action),
                            .ctrl = mouse_event.mods.ctrl,
                            .alt = mouse_event.mods.alt,
                            .shift = mouse_event.mods.shift,
                        }) catch {};
                    }
                }
            },
            .paste => |data| {
                if (self.focused_id) |id| {
                    if (self.ptys.get(id)) |pty| {
                        pty.send_paste_fn(pty.app, pty.id, data) catch {};
                    }
                }
            },
            .init => {
                // Defer spawn until we get a winsize event with real dimensions
                self.needs_initial_spawn = true;
            },
            .cwd_changed => {},
            .split_resize => {},
        }
    }

    fn handleKeyPress(self: *Layout, key: vaxis.Key, release: bool) !void {
        // Phase 1: forward all keys to focused PTY
        if (self.focused_id) |id| {
            if (self.ptys.get(id)) |pty| {
                const key_data = vaxisKeyToKeyData(key, release);
                pty.send_key_fn(pty.app, pty.id, key_data) catch {};
            }
        }
    }

    fn handleMouse(self: *Layout, mouse: vaxis.Mouse) !void {
        _ = self;
        _ = mouse;
        // Mouse handling will be added in Phase 2+
    }

    fn requestRedraw(self: *Layout) void {
        if (self.redraw_callback) |cb| cb(self.redraw_ctx);
    }

    // ---- Widget tree construction ----

    pub fn view(self: *Layout) !widget.Widget {
        // Phase 1: single terminal fills the screen
        if (self.focused_id) |id| {
            if (self.ptys.get(id)) |pty| {
                return widget.Widget{
                    .kind = .{ .surface = .{
                        .pty_id = id,
                        .surface = pty.surface,
                    } },
                    .focus = true,
                };
            }
        }

        // No PTY yet — empty screen
        const spans = try self.allocator.alloc(widget.Text.Span, 1);
        spans[0] = .{ .text = "tuios: waiting for terminal...", .style = .{} };
        return widget.Widget{
            .kind = .{ .text = .{ .spans = spans } },
        };
    }

    // ---- Types needed by client.zig ----

    pub const PtyLookupResult = struct {
        pty_id: u32,
        surface: *Surface,
        app: *anyopaque,
        send_key_fn: *const fn (app: *anyopaque, id: u32, key: KeyData) anyerror!void,
        send_mouse_fn: *const fn (app: *anyopaque, id: u32, mouse: MouseData) anyerror!void,
        send_paste_fn: *const fn (app: *anyopaque, id: u32, data: []const u8) anyerror!void,
        set_focus_fn: *const fn (app: *anyopaque, id: u32, focused: bool) anyerror!void,
        close_fn: *const fn (app: *anyopaque, id: u32) anyerror!void,
        cwd_fn: *const fn (app: *anyopaque, id: u32) ?[]const u8,
        copy_selection_fn: *const fn (app: *anyopaque, id: u32) anyerror!void,
        cell_size_fn: *const fn (app: *anyopaque) CellSize,
    };

    pub const PtyLookupFn = *const fn (ctx: *anyopaque, id: u32) ?PtyLookupResult;
    pub const CwdLookupFn = *const fn (ctx: *anyopaque, id: i64) ?[]const u8;

    // ---- Session state (stubs for now) ----

    pub fn getNextSessionName(self: *Layout) ![]const u8 {
        _ = self;
        return "default";
    }

    pub fn getMacosOptionAsAlt(self: *Layout) []const u8 {
        _ = self;
        return "false";
    }

    pub fn getStateJson(self: *Layout, cwd_lookup_fn: ?CwdLookupFn, cwd_lookup_ctx: *anyopaque) ![]u8 {
        _ = cwd_lookup_fn;
        _ = cwd_lookup_ctx;
        return try self.allocator.dupe(u8, "{}");
    }

    pub fn setStateFromJson(self: *Layout, json: []const u8, pty_lookup_fn: PtyLookupFn, pty_lookup_ctx: *anyopaque) !void {
        _ = self;
        _ = json;
        _ = pty_lookup_fn;
        _ = pty_lookup_ctx;
    }

    // ---- Helpers ----

    fn vaxisKeyToKeyData(key: vaxis.Key, release: bool) KeyData {
        // Convert vaxis key to W3C-style key data for the server protocol
        var key_buf: [32]u8 = undefined;
        var code_buf: [32]u8 = undefined;
        const key_str = vaxisKeyToString(key.codepoint, &key_buf);
        const code_str = vaxisCodeToString(key.codepoint, &code_buf);
        return .{
            .key = key_str,
            .code = code_str,
            .ctrl = key.mods.ctrl,
            .alt = key.mods.alt,
            .shift = key.mods.shift,
            .super = key.mods.super,
            .release = release,
        };
    }

    fn vaxisKeyToString(codepoint: u21, buf: *[32]u8) []const u8 {
        if (codepoint < 128) {
            buf[0] = @intCast(codepoint);
            return buf[0..1];
        }
        const len = std.unicode.utf8Encode(codepoint, buf) catch return " ";
        return buf[0..len];
    }

    fn vaxisCodeToString(codepoint: u21, buf: *[32]u8) []const u8 {
        // For now, return same as key (proper W3C code mapping added later)
        return vaxisKeyToString(codepoint, buf);
    }
};
