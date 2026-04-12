//! Native Zig layout system replacing Lua-based UI.
//! Manages terminal surfaces, BSP tiling, workspaces, keybinds,
//! and mode switching (terminal/window management).

const std = @import("std");
const vaxis = @import("vaxis");

const widget = @import("widget.zig");
const Surface = @import("Surface.zig");
const io = @import("io.zig");
const vaxis_helper = @import("vaxis_helper.zig");
const bsp = @import("bsp.zig");

const log = std.log.scoped(.layout);

// ---- Constants ----

const prefix_timeout_ns: i128 = 2 * std.time.ns_per_s;
const max_workspaces = 9;
const status_bar_height = 1;
const focused_border_color = vaxis.Color{ .rgb = .{ 0x48, 0x65, 0xf2 } }; // #4865f2
const unfocused_border_color = vaxis.Color{ .rgb = .{ 0x55, 0x55, 0x55 } }; // #555555
const prefix_indicator_color = vaxis.Color{ .rgb = .{ 0xff, 0xa5, 0x00 } }; // orange
const status_bg_color = vaxis.Color{ .rgb = .{ 0x1e, 0x1e, 0x2e } }; // dark bg

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

// ---- Mode ----

pub const Mode = enum {
    terminal,
    window_management,
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

    // BSP tiling (one per workspace)
    workspaces: [max_workspaces]bsp.BSPTree = [_]bsp.BSPTree{bsp.BSPTree.init()} ** max_workspaces,
    active_workspace: u8 = 0,

    // Window-to-workspace mapping
    window_workspace: [64]u32 = [_]u32{0} ** 64, // window_id
    window_ws_idx: [64]u8 = [_]u8{0} ** 64, // workspace index
    window_ws_count: u16 = 0,

    // Mode and prefix
    mode: Mode = .terminal,
    prefix_active: bool = false,
    prefix_time: i128 = 0,
    zoomed: bool = false,
    zoomed_id: ?u32 = null,
    pending_split_dir: bsp.SplitDirection = .none, // for explicit split commands

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

    // ---- BSP helpers ----

    fn currentTree(self: *Layout) *bsp.BSPTree {
        return &self.workspaces[self.active_workspace];
    }

    fn currentTreeConst(self: *const Layout) *const bsp.BSPTree {
        return &self.workspaces[self.active_workspace];
    }

    fn tileBounds(self: *const Layout) bsp.Rect {
        // Full screen minus status bar
        const h = if (self.screen_rows > status_bar_height) self.screen_rows - status_bar_height else self.screen_rows;
        return .{ .x = 0, .y = 0, .w = self.screen_cols, .h = h };
    }

    fn setWindowWorkspace(self: *Layout, window_id: u32, ws: u8) void {
        for (0..self.window_ws_count) |i| {
            if (self.window_workspace[i] == window_id) {
                self.window_ws_idx[i] = ws;
                return;
            }
        }
        if (self.window_ws_count < 64) {
            self.window_workspace[self.window_ws_count] = window_id;
            self.window_ws_idx[self.window_ws_count] = ws;
            self.window_ws_count += 1;
        }
    }

    fn removeWindowWorkspace(self: *Layout, window_id: u32) void {
        for (0..self.window_ws_count) |i| {
            if (self.window_workspace[i] == window_id) {
                const last = self.window_ws_count - 1;
                self.window_workspace[i] = self.window_workspace[last];
                self.window_ws_idx[i] = self.window_ws_idx[last];
                self.window_ws_count -= 1;
                return;
            }
        }
    }

    fn updateFocusState(self: *Layout) void {
        // Tell all PTYs whether they're focused
        for (self.ptys.values()) |pty| {
            pty.set_focus_fn(pty.app, pty.id, self.focused_id == pty.id) catch {};
        }
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

                // Insert into BSP tree for current workspace
                const tree = self.currentTree();
                const focused = self.focused_id orelse info.id;
                const dir = self.pending_split_dir;
                self.pending_split_dir = .none;
                tree.insertWindow(info.id, focused, dir, self.tileBounds());
                self.setWindowWorkspace(info.id, self.active_workspace);

                // Focus the new window (on first PTY or explicit split)
                self.focused_id = info.id;
                self.updateFocusState();
                self.requestRedraw();
            },
            .pty_exited => |info| {
                // Remove from BSP tree
                const tree = self.currentTree();
                tree.removeWindow(info.id);
                self.removeWindowWorkspace(info.id);

                _ = self.ptys.orderedRemove(info.id);

                // If focused PTY exited, focus next in BSP
                if (self.focused_id == info.id) {
                    self.focused_id = self.findNextFocusable();
                }

                // Un-zoom if zoomed window exited
                if (self.zoomed and self.zoomed_id == info.id) {
                    self.zoomed = false;
                    self.zoomed_id = null;
                }

                // If no more PTYs on any workspace, exit
                if (self.ptys.count() == 0) {
                    if (self.exit_callback) |cb| cb(self.exit_ctx);
                }
                self.updateFocusState();
                self.requestRedraw();
            },
            .vaxis => |vx_event| {
                switch (vx_event) {
                    .winsize => |ws| {
                        self.screen_cols = ws.cols;
                        self.screen_rows = ws.rows;
                        if (self.needs_initial_spawn and ws.cols > 0 and ws.rows > 0) {
                            self.needs_initial_spawn = false;
                            self.spawnWindow();
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
                        try self.handleMouse(mouse);
                    },
                    else => {},
                }
            },
            .mouse => |mouse_event| {
                if (mouse_event.target) |target_id| {
                    // Click to focus
                    if (mouse_event.action == .press) {
                        if (self.focused_id != target_id and self.ptys.contains(target_id)) {
                            self.focused_id = target_id;
                            self.updateFocusState();
                            self.requestRedraw();
                        }
                    }
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
                if (self.ptys.count() == 0 and self.focused_id == null) {
                    if (self.screen_cols > 0 and self.screen_rows > 0) {
                        self.spawnWindow();
                    } else {
                        self.needs_initial_spawn = true;
                    }
                }
            },
            .cwd_changed => {},
            .split_resize => |resize| {
                // Adjust BSP split ratio when user drags a separator
                // The resize event comes from client.zig's split handle drag
                if (resize.parent_id) |_| {
                    // Find which split this corresponds to and adjust ratio
                    // For now, adjust the ratio on the focused window's parent
                    if (self.focused_id) |fid| {
                        self.currentTree().adjustRatio(fid, resize.ratio);
                        self.requestRedraw();
                    }
                }
            },
        }
    }

    // ---- Key handling ----

    fn handleKeyPress(self: *Layout, key: vaxis.Key, release: bool) !void {
        // Never intercept releases for prefix
        if (release) {
            return self.forwardKeyToFocused(key, true);
        }

        // Check prefix timeout
        if (self.prefix_active) {
            const now = std.time.nanoTimestamp();
            if (now - self.prefix_time > prefix_timeout_ns) {
                self.prefix_active = false;
            }
        }

        // Ctrl+B activates prefix in any mode
        if (!self.prefix_active) {
            if (key.codepoint == 'b' and key.mods.ctrl and !key.mods.alt and !key.mods.shift and !key.mods.super) {
                self.prefix_active = true;
                self.prefix_time = std.time.nanoTimestamp();
                self.requestRedraw(); // show prefix indicator
                return;
            }
        }

        // Handle prefix commands
        if (self.prefix_active) {
            self.prefix_active = false;
            self.handlePrefixKey(key);
            self.requestRedraw();
            return;
        }

        // Alt+1-9 switches workspace directly (no prefix needed)
        if (key.mods.alt and !key.mods.ctrl and !key.mods.super) {
            if (key.codepoint >= '1' and key.codepoint <= '9') {
                const ws: u8 = @intCast(key.codepoint - '1');
                self.switchWorkspace(ws);
                return;
            }
        }

        // In window management mode, hjkl moves focus without prefix
        if (self.mode == .window_management) {
            if (self.handleWMKey(key)) return;
        }

        // Forward to focused PTY
        return self.forwardKeyToFocused(key, false);
    }

    fn handlePrefixKey(self: *Layout, key: vaxis.Key) void {
        const cp = key.codepoint;
        const shift = key.mods.shift;

        // Split commands
        if (cp == '|' or (cp == '\\' and shift)) {
            self.splitFocused(.vertical);
            return;
        }
        if (cp == '-') {
            self.splitFocused(.horizontal);
            return;
        }

        // Focus movement
        if (cp == 'h') { self.moveFocus(.left); return; }
        if (cp == 'j') { self.moveFocus(.down); return; }
        if (cp == 'k') { self.moveFocus(.up); return; }
        if (cp == 'l') { self.moveFocus(.right); return; }

        // Window operations
        if (cp == 'c') { self.spawnWindow(); return; }
        if (cp == 'x') { self.closeFocused(); return; }
        if (cp == 'z') { self.toggleZoom(); return; }
        if (cp == 'n' or cp == '\t') { self.cycleFocus(true); return; }
        if (cp == 'p') { self.cycleFocus(false); return; }

        // Workspace (Ctrl+B, w then number — simplified: Ctrl+B, 1-9)
        if (cp >= '1' and cp <= '9') {
            const ws: u8 = @intCast(cp - '1');
            self.switchWorkspace(ws);
            return;
        }

        // Mode switching
        if (cp == 'w') {
            self.mode = .window_management;
            return;
        }

        // Rotate split
        if (cp == 'r') {
            if (self.focused_id) |fid| {
                self.currentTree().rotateSplit(fid);
            }
            return;
        }

        // Equalize
        if (cp == '=') {
            self.currentTree().equalizeRatios();
            return;
        }

        // Detach
        if (cp == 'd') {
            if (self.detach_callback) |cb| {
                cb(self.detach_ctx, "default") catch {};
            }
            return;
        }

        // Quit
        if (cp == 'q') {
            if (self.exit_callback) |cb| cb(self.exit_ctx);
            return;
        }

        // Escape cancels prefix (already deactivated above)
        if (cp == vaxis.Key.escape) return;
    }

    fn handleWMKey(self: *Layout, key: vaxis.Key) bool {
        const cp = key.codepoint;

        // hjkl for focus
        if (cp == 'h') { self.moveFocus(.left); self.requestRedraw(); return true; }
        if (cp == 'j') { self.moveFocus(.down); self.requestRedraw(); return true; }
        if (cp == 'k') { self.moveFocus(.up); self.requestRedraw(); return true; }
        if (cp == 'l') { self.moveFocus(.right); self.requestRedraw(); return true; }

        // | and - for splits
        if (cp == '|') { self.splitFocused(.vertical); self.requestRedraw(); return true; }
        if (cp == '-') { self.splitFocused(.horizontal); self.requestRedraw(); return true; }

        // x to close
        if (cp == 'x') { self.closeFocused(); self.requestRedraw(); return true; }

        // z to zoom
        if (cp == 'z') { self.toggleZoom(); self.requestRedraw(); return true; }

        // Escape or i returns to terminal mode
        if (cp == vaxis.Key.escape or cp == 'i') {
            self.mode = .terminal;
            self.requestRedraw();
            return true;
        }

        return false;
    }

    fn forwardKeyToFocused(self: *Layout, key: vaxis.Key, release: bool) !void {
        if (self.focused_id) |id| {
            if (self.ptys.get(id)) |pty| {
                const strings = vaxis_helper.vaxisKeyToStrings(self.allocator, key) catch return;
                defer self.allocator.free(strings.key);
                defer self.allocator.free(strings.code);

                pty.send_key_fn(pty.app, pty.id, .{
                    .key = strings.key,
                    .code = strings.code,
                    .ctrl = key.mods.ctrl,
                    .alt = key.mods.alt,
                    .shift = key.mods.shift,
                    .super = key.mods.super,
                    .release = release,
                }) catch {};
            }
        }
    }

    // ---- Window operations ----

    fn spawnWindow(self: *Layout) void {
        if (self.spawn_callback) |cb| {
            const bounds = self.tileBounds();
            cb(self.spawn_ctx, .{
                .rows = if (bounds.h > 0) bounds.h else self.screen_rows,
                .cols = if (bounds.w > 0) bounds.w else self.screen_cols,
                .attach = true,
            }) catch |err| {
                log.err("Failed to spawn window: {}", .{err});
            };
        }
    }

    fn splitFocused(self: *Layout, direction: bsp.SplitDirection) void {
        self.pending_split_dir = direction;
        self.spawnWindow();
    }

    fn closeFocused(self: *Layout) void {
        if (self.focused_id) |id| {
            if (self.ptys.get(id)) |pty| {
                pty.close_fn(pty.app, pty.id) catch {};
            }
        }
    }

    fn toggleZoom(self: *Layout) void {
        if (self.zoomed) {
            self.zoomed = false;
            self.zoomed_id = null;
        } else {
            self.zoomed = true;
            self.zoomed_id = self.focused_id;
        }
        self.requestRedraw();
    }

    fn moveFocus(self: *Layout, dir: bsp.Direction) void {
        const fid = self.focused_id orelse return;
        const tree = self.currentTreeConst();
        if (tree.findNeighbor(fid, dir, self.tileBounds())) |neighbor_id| {
            self.focused_id = neighbor_id;
            self.updateFocusState();
        }
    }

    fn cycleFocus(self: *Layout, forward: bool) void {
        const tree = self.currentTreeConst();
        var ids: [64]u32 = undefined;
        const count = tree.getAllWindowIDs(&ids);
        if (count <= 1) return;

        const fid = self.focused_id orelse return;

        // Find current index
        var cur_idx: ?usize = null;
        for (0..count) |i| {
            if (ids[i] == fid) {
                cur_idx = i;
                break;
            }
        }
        const ci = cur_idx orelse return;

        const next = if (forward)
            (ci + 1) % count
        else if (ci == 0) count - 1 else ci - 1;

        self.focused_id = ids[next];
        self.updateFocusState();
        self.requestRedraw();
    }

    fn findNextFocusable(self: *Layout) ?u32 {
        const tree = self.currentTreeConst();
        var ids: [64]u32 = undefined;
        const count = tree.getAllWindowIDs(&ids);
        if (count > 0) return ids[0];

        // Check other workspaces
        for (0..max_workspaces) |ws| {
            if (ws == self.active_workspace) continue;
            var ws_ids: [64]u32 = undefined;
            const ws_count = self.workspaces[ws].getAllWindowIDs(&ws_ids);
            if (ws_count > 0) return ws_ids[0];
        }
        return null;
    }

    fn switchWorkspace(self: *Layout, ws: u8) void {
        if (ws >= max_workspaces) return;
        if (ws == self.active_workspace) return;

        self.active_workspace = ws;

        // Find a window to focus in the new workspace
        var ids: [64]u32 = undefined;
        const count = self.workspaces[ws].getAllWindowIDs(&ids);
        if (count > 0) {
            self.focused_id = ids[0];
        }
        self.updateFocusState();
        self.requestRedraw();
    }

    fn handleMouse(self: *Layout, mouse: vaxis.Mouse) !void {
        _ = self;
        _ = mouse;
        // Mouse is handled via the .mouse event (hit-tested by client.zig)
    }

    fn emptyWidget(self: *Layout) !widget.Widget {
        const spans = try self.allocator.alloc(widget.Text.Span, 0);
        return widget.Widget{ .kind = .{ .text = .{ .spans = spans } } };
    }

    fn requestRedraw(self: *Layout) void {
        if (self.redraw_callback) |cb| cb(self.redraw_ctx);
    }

    // ---- Widget tree construction ----

    pub fn view(self: *Layout) !widget.Widget {
        const alloc = self.allocator;

        // Build the tiling content area
        const content = try self.buildTilingWidget();

        // Build status bar
        const status = try self.buildStatusBar();

        // Column: content (flex) + status bar (fixed 1 row)
        const children = try alloc.alloc(widget.Widget, 2);
        children[0] = content;
        children[0].ratio = 1.0; // takes remaining space
        children[1] = status;

        return widget.Widget{
            .kind = .{ .column = .{
                .children = children,
                .cross_axis_align = .stretch,
            } },
        };
    }

    fn buildTilingWidget(self: *Layout) !widget.Widget {
        // Zoomed: show single window fullscreen
        if (self.zoomed) {
            if (self.zoomed_id) |zid| {
                if (self.ptys.get(zid)) |pty| {
                    return try self.buildBorderedSurface(zid, pty.surface, true);
                }
            }
        }

        const tree = self.currentTreeConst();

        // Empty workspace
        if (tree.isEmpty()) {
            return self.emptyWidget();
        }

        // Single window — no borders needed, maximize space
        if (tree.windowCount() == 1) {
            var ids: [64]u32 = undefined;
            const count = tree.getAllWindowIDs(&ids);
            if (count > 0) {
                if (self.ptys.get(ids[0])) |pty| {
                    return widget.Widget{
                        .kind = .{ .surface = .{
                            .pty_id = ids[0],
                            .surface = pty.surface,
                        } },
                        .focus = self.focused_id == ids[0],
                    };
                }
            }
            return self.emptyWidget();
        }

        // Multiple windows — build from BSP tree
        return try self.buildBSPWidget(tree.root, self.tileBounds());
    }

    fn buildBSPWidget(self: *Layout, node_idx: u16, bounds: bsp.Rect) !widget.Widget {
        const alloc = self.allocator;
        const null_idx: u16 = std.math.maxInt(u16);

        if (node_idx == null_idx) {
            return self.emptyWidget();
        }

        const tree = self.currentTreeConst();
        const node = tree.nodes[node_idx];

        // Leaf node — render surface with border
        if (node.isLeaf()) {
            const wid: u32 = @intCast(node.window_id);
            if (self.ptys.get(wid)) |pty| {
                return try self.buildBorderedSurface(wid, pty.surface, self.focused_id == wid);
            }
            return self.emptyWidget();
        }

        // Internal node — split into two children with a separator
        const is_vertical = node.split == .vertical;

        // Build children: left, separator, right
        const children = try alloc.alloc(widget.Widget, 3);

        // Compute child bounds for recursive calls
        var left_bounds: bsp.Rect = undefined;
        var right_bounds: bsp.Rect = undefined;

        if (is_vertical) {
            const split_x = bounds.x + @as(u16, @intFromFloat(@as(f32, @floatFromInt(bounds.w)) * node.ratio));
            const clamped_x = std.math.clamp(split_x, bounds.x + 1, bounds.x + bounds.w -| 2);
            left_bounds = .{ .x = bounds.x, .y = bounds.y, .w = clamped_x - bounds.x, .h = bounds.h };
            right_bounds = .{ .x = clamped_x + 1, .y = bounds.y, .w = bounds.x + bounds.w -| clamped_x -| 1, .h = bounds.h };
        } else {
            const split_y = bounds.y + @as(u16, @intFromFloat(@as(f32, @floatFromInt(bounds.h)) * node.ratio));
            const clamped_y = std.math.clamp(split_y, bounds.y + 1, bounds.y + bounds.h -| 2);
            left_bounds = .{ .x = bounds.x, .y = bounds.y, .w = bounds.w, .h = clamped_y - bounds.y };
            right_bounds = .{ .x = bounds.x, .y = clamped_y + 1, .w = bounds.w, .h = bounds.y + bounds.h -| clamped_y -| 1 };
        }

        children[0] = try self.buildBSPWidget(node.left, left_bounds);
        children[0].ratio = node.ratio;

        children[1] = widget.Widget{
            .kind = .{ .separator = .{
                .axis = if (is_vertical) .vertical else .horizontal,
                .style = .{ .fg = unfocused_border_color },
                .border = .rounded,
            } },
        };

        children[2] = try self.buildBSPWidget(node.right, right_bounds);
        children[2].ratio = 1.0 - node.ratio;

        if (is_vertical) {
            return widget.Widget{
                .kind = .{ .row = .{
                    .children = children,
                    .cross_axis_align = .stretch,
                    .resizable = true,
                } },
            };
        } else {
            return widget.Widget{
                .kind = .{ .column = .{
                    .children = children,
                    .cross_axis_align = .stretch,
                    .resizable = true,
                } },
            };
        }
    }

    fn buildBorderedSurface(self: *Layout, wid: u32, surface: *Surface, focused: bool) !widget.Widget {
        const alloc = self.allocator;

        const child = try alloc.create(widget.Widget);
        child.* = .{
            .kind = .{ .surface = .{
                .pty_id = wid,
                .surface = surface,
            } },
            .focus = focused,
        };

        return widget.Widget{
            .kind = .{ .box = .{
                .child = child,
                .border = .rounded,
                .style = .{
                    .fg = if (focused) focused_border_color else unfocused_border_color,
                },
            } },
        };
    }

    fn buildStatusBar(self: *Layout) !widget.Widget {
        const alloc = self.allocator;

        // Build status text spans
        var spans: std.ArrayList(widget.Text.Span) = .empty;

        // Mode indicator
        const mode_text = switch (self.mode) {
            .terminal => if (self.prefix_active) " PREFIX " else " T ",
            .window_management => " WM ",
        };
        const mode_fg = switch (self.mode) {
            .terminal => if (self.prefix_active) vaxis.Color{ .rgb = .{ 0, 0, 0 } } else vaxis.Color{ .rgb = .{ 0xbb, 0xbb, 0xbb } },
            .window_management => vaxis.Color{ .rgb = .{ 0, 0, 0 } },
        };
        const mode_bg = switch (self.mode) {
            .terminal => if (self.prefix_active) prefix_indicator_color else status_bg_color,
            .window_management => focused_border_color,
        };

        try spans.append(alloc, .{
            .text = try alloc.dupe(u8, mode_text),
            .style = .{ .fg = mode_fg, .bg = mode_bg, .bold = true },
        });

        // Workspace indicator
        var ws_buf: [16]u8 = undefined;
        const ws_text = std.fmt.bufPrint(&ws_buf, " [{d}] ", .{self.active_workspace + 1}) catch " [?] ";
        try spans.append(alloc, .{
            .text = try alloc.dupe(u8, ws_text),
            .style = .{ .fg = focused_border_color, .bg = status_bg_color },
        });

        // Window count
        const wcount = self.currentTreeConst().windowCount();
        var wc_buf: [32]u8 = undefined;
        const wc_text = std.fmt.bufPrint(&wc_buf, " {d} window{s} ", .{ wcount, if (wcount != 1) "s" else "" }) catch " ? ";
        try spans.append(alloc, .{
            .text = try alloc.dupe(u8, wc_text),
            .style = .{ .fg = .{ .rgb = .{ 0x88, 0x88, 0x88 } }, .bg = status_bg_color },
        });

        // Zoom indicator
        if (self.zoomed) {
            try spans.append(alloc, .{
                .text = try alloc.dupe(u8, " ZOOM "),
                .style = .{ .fg = .{ .rgb = .{ 0, 0, 0 } }, .bg = .{ .rgb = .{ 0xff, 0x55, 0x55 } }, .bold = true },
            });
        }

        return widget.Widget{
            .kind = .{ .text = .{
                .spans = try spans.toOwnedSlice(alloc),
            } },
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
};
