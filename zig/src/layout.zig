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
const key_string = @import("key_string.zig");
const keybind_compiler = @import("keybind_compiler.zig");
const keybind_matcher = @import("keybind_matcher.zig");
const Action = @import("action.zig").Action;
const theme_mod = @import("theme.zig");

const log = std.log.scoped(.layout);

// ---- Constants ----

const max_workspaces = 9;
const max_floating = 16;
const min_window_width = 10;
const min_window_height = 3;

pub const FloatingWindow = struct {
    id: u32,
    x: u16,
    y: u16,
    w: u16,
    h: u16,
};

pub const DragState = struct {
    window_id: u32,
    mode: DragMode,
    start_x: u16,
    start_y: u16,
    orig_x: u16,
    orig_y: u16,
    orig_w: u16,
    orig_h: u16,
    corner: ResizeCorner = .bottom_right,

    pub const DragMode = enum { move, resize };
    pub const ResizeCorner = enum { top_left, top_right, bottom_left, bottom_right };
};
const status_bar_height = 2;

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
    zoomed: bool = false,
    zoomed_id: ?u32 = null,
    pending_split_dir: bsp.SplitDirection = .none,
    show_help: bool = false,

    // Floating windows
    floating: [max_floating]FloatingWindow = undefined,
    floating_count: u8 = 0,

    // Mouse drag state
    drag: ?DragState = null,

    // Keybind system
    trie: keybind_compiler.Trie = undefined,
    matcher: keybind_matcher.Matcher = undefined,
    trie_initialized: bool = false,

    // Theme
    theme: theme_mod.Theme = theme_mod.default_theme,

    pub const InitResult = union(enum) {
        ok: Layout,
        err: struct { err: anyerror, lua_msg: ?[:0]const u8 },
    };

    // Default keybinds: leader is <C-b>
    const default_bindings = [_]keybind_compiler.Keybind{
        // Splits
        .{ .key_string = "<leader><S-Backslash>", .action = .split_vertical }, // Ctrl+B, |
        .{ .key_string = "<leader>-", .action = .split_horizontal },
        // Focus
        .{ .key_string = "<leader>h", .action = .focus_left },
        .{ .key_string = "<leader>j", .action = .focus_down },
        .{ .key_string = "<leader>k", .action = .focus_up },
        .{ .key_string = "<leader>l", .action = .focus_right },
        // Window ops
        .{ .key_string = "<leader>c", .action = .new_window },
        .{ .key_string = "<leader>x", .action = .close_pane },
        .{ .key_string = "<leader>z", .action = .toggle_zoom },
        .{ .key_string = "<leader>n", .action = .cycle_next },
        .{ .key_string = "<leader><Tab>", .action = .cycle_next },
        .{ .key_string = "<leader>p", .action = .cycle_prev },
        // Resize (Shift+hjkl in prefix)
        .{ .key_string = "<leader><S-h>", .action = .resize_left },
        .{ .key_string = "<leader><S-j>", .action = .resize_down },
        .{ .key_string = "<leader><S-k>", .action = .resize_up },
        .{ .key_string = "<leader><S-l>", .action = .resize_right },
        .{ .key_string = "<leader><Left>", .action = .resize_left },
        .{ .key_string = "<leader><Right>", .action = .resize_right },
        .{ .key_string = "<leader><Up>", .action = .resize_up },
        .{ .key_string = "<leader><Down>", .action = .resize_down },
        // Workspace switch
        .{ .key_string = "<leader>1", .action = .workspace_1 },
        .{ .key_string = "<leader>2", .action = .workspace_2 },
        .{ .key_string = "<leader>3", .action = .workspace_3 },
        .{ .key_string = "<leader>4", .action = .workspace_4 },
        .{ .key_string = "<leader>5", .action = .workspace_5 },
        .{ .key_string = "<leader>6", .action = .workspace_6 },
        .{ .key_string = "<leader>7", .action = .workspace_7 },
        .{ .key_string = "<leader>8", .action = .workspace_8 },
        .{ .key_string = "<leader>9", .action = .workspace_9 },
        // Workspace switch (Alt, no prefix)
        .{ .key_string = "<A-1>", .action = .workspace_1 },
        .{ .key_string = "<A-2>", .action = .workspace_2 },
        .{ .key_string = "<A-3>", .action = .workspace_3 },
        .{ .key_string = "<A-4>", .action = .workspace_4 },
        .{ .key_string = "<A-5>", .action = .workspace_5 },
        .{ .key_string = "<A-6>", .action = .workspace_6 },
        .{ .key_string = "<A-7>", .action = .workspace_7 },
        .{ .key_string = "<A-8>", .action = .workspace_8 },
        .{ .key_string = "<A-9>", .action = .workspace_9 },
        // Move to workspace (Shift+number in prefix)
        .{ .key_string = "<leader><S-1>", .action = .move_to_workspace_1 },
        .{ .key_string = "<leader><S-2>", .action = .move_to_workspace_2 },
        .{ .key_string = "<leader><S-3>", .action = .move_to_workspace_3 },
        .{ .key_string = "<leader><S-4>", .action = .move_to_workspace_4 },
        .{ .key_string = "<leader><S-5>", .action = .move_to_workspace_5 },
        .{ .key_string = "<leader><S-6>", .action = .move_to_workspace_6 },
        .{ .key_string = "<leader><S-7>", .action = .move_to_workspace_7 },
        .{ .key_string = "<leader><S-8>", .action = .move_to_workspace_8 },
        .{ .key_string = "<leader><S-9>", .action = .move_to_workspace_9 },
        // Mode
        .{ .key_string = "<leader>w", .action = .enter_wm_mode },
        // Split manipulation
        .{ .key_string = "<leader>r", .action = .rotate_split },
        .{ .key_string = "<leader>=", .action = .equalize_splits },
        .{ .key_string = "<leader>{", .action = .swap_left },
        .{ .key_string = "<leader>}", .action = .swap_right },
        // Floating
        .{ .key_string = "<leader>f", .action = .floating_toggle },
        // Session/misc
        .{ .key_string = "<leader>d", .action = .detach_session },
        .{ .key_string = "<leader>q", .action = .quit },
        .{ .key_string = "<leader>?", .action = .help_toggle },
        .{ .key_string = "<leader><Space>", .action = .toggle_zoom },
        .{ .key_string = "<leader>[", .action = .enter_copy_mode },
    };

    pub fn init(allocator: std.mem.Allocator) InitResult {
        // Compile default keybinds
        var compiler = keybind_compiler.Compiler.init(allocator);
        defer compiler.deinit();

        compiler.setLeader("<C-b>") catch |err| {
            return .{ .err = .{ .err = err, .lua_msg = null } };
        };

        const trie = compiler.compile(&default_bindings) catch |err| {
            log.err("Failed to compile keybinds: {}", .{err});
            return .{ .err = .{ .err = err, .lua_msg = null } };
        };

        return .{ .ok = .{
            .allocator = allocator,
            .ptys = std.AutoArrayHashMap(u32, Pty).init(allocator),
            .trie = trie,
            .matcher = undefined, // must call initMatcher() after Layout is in final location
            .trie_initialized = true,
        } };
    }

    /// Must be called after Layout is in its final memory location (after init returns).
    /// Sets up the matcher to point at the trie.
    pub fn initMatcher(self: *Layout) void {
        if (self.trie_initialized) {
            self.matcher = keybind_matcher.Matcher.init(&self.trie);
        }
    }

    pub fn deinit(self: *Layout) void {
        self.ptys.deinit();
        if (self.trie_initialized) {
            self.trie.deinit();
        }
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
                // Remove from BSP tree (search all workspaces)
                for (&self.workspaces) |*ws_tree| {
                    if (ws_tree.hasWindow(info.id)) {
                        ws_tree.removeWindow(info.id);
                        break;
                    }
                }
                self.removeWindowWorkspace(info.id);

                // Remove from floating list if floating
                self.removeFloating(info.id);

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
                if (resize.parent_id) |pid| {
                    // Map widget ID back to BSP node index
                    if (pid >= 0x10000) {
                        const node_idx: u16 = @intCast(pid - 0x10000);
                        const tree = self.currentTree();
                        if (node_idx < bsp.max_nodes and tree.nodes[node_idx].active) {
                            tree.nodes[node_idx].ratio = std.math.clamp(resize.ratio, 0.1, 0.9);
                            self.requestRedraw();
                        }
                    }
                }
            },
        }
    }

    // ---- Vaxis Key → key_string.Key conversion ----

    fn vaxisToKeyString(vkey: vaxis.Key) key_string.Key {
        return .{
            .key = vaxisCodepointToName(vkey.codepoint),
            .ctrl = vkey.mods.ctrl,
            .alt = vkey.mods.alt,
            .shift = vkey.mods.shift,
            .super = vkey.mods.super,
        };
    }

    fn vaxisCodepointToName(cp: u21) []const u8 {
        return switch (cp) {
            vaxis.Key.escape => "Escape",
            vaxis.Key.enter => "Enter",
            vaxis.Key.tab => "Tab",
            vaxis.Key.backspace => "Backspace",
            vaxis.Key.space => "Space",
            vaxis.Key.delete => "Delete",
            vaxis.Key.insert => "Insert",
            vaxis.Key.left => "Left",
            vaxis.Key.right => "Right",
            vaxis.Key.up => "Up",
            vaxis.Key.down => "Down",
            vaxis.Key.home => "Home",
            vaxis.Key.end => "End",
            vaxis.Key.page_up => "PageUp",
            vaxis.Key.page_down => "PageDown",
            vaxis.Key.f1 => "F1",
            vaxis.Key.f2 => "F2",
            vaxis.Key.f3 => "F3",
            vaxis.Key.f4 => "F4",
            vaxis.Key.f5 => "F5",
            vaxis.Key.f6 => "F6",
            vaxis.Key.f7 => "F7",
            vaxis.Key.f8 => "F8",
            vaxis.Key.f9 => "F9",
            vaxis.Key.f10 => "F10",
            vaxis.Key.f11 => "F11",
            vaxis.Key.f12 => "F12",
            '\\' => "Backslash",
            else => blk: {
                // For printable ASCII, use the character itself (stored as static strings)
                if (cp >= 0x21 and cp <= 0x7E) {
                    const table = comptime init_ascii_table();
                    break :blk table[cp - 0x21];
                }
                break :blk "?";
            },
        };
    }

    fn init_ascii_table() [94][]const u8 {
        var table: [94][]const u8 = undefined;
        for (0..94) |i| {
            const c: u8 = @intCast(i + 0x21);
            table[i] = &[_]u8{c};
        }
        return table;
    }

    // ---- Key handling ----

    fn handleKeyPress(self: *Layout, key: vaxis.Key, release: bool) !void {
        // Never intercept releases
        if (release) {
            return self.forwardKeyToFocused(key, true);
        }

        // Dismiss help overlay
        if (self.show_help) {
            if (key.codepoint == vaxis.Key.escape or key.codepoint == '?' or key.codepoint == 'q') {
                self.show_help = false;
                self.requestRedraw();
                return;
            }
            return;
        }

        // In WM mode, handle direct keys (no prefix needed)
        if (self.mode == .window_management) {
            if (self.handleWMKey(key)) return;
        }

        // Feed key through trie matcher
        const ks = vaxisToKeyString(key);
        const result = self.matcher.handleKey(ks);

        switch (result) {
            .action => |act| {
                self.dispatchAction(act.action);
                self.requestRedraw();
            },
            .pending => {
                // Matcher is waiting for more keys (e.g., after Ctrl+B)
                self.requestRedraw();
            },
            .none => {
                // No binding matched — forward to focused PTY
                return self.forwardKeyToFocused(key, false);
            },
        }
    }

    fn handleWMKey(self: *Layout, key: vaxis.Key) bool {
        const cp = key.codepoint;
        if (key.mods.ctrl or key.mods.alt or key.mods.super) return false;

        const acted = true;
        if (cp == 'h') { self.moveFocus(.left); }
        else if (cp == 'j') { self.moveFocus(.down); }
        else if (cp == 'k') { self.moveFocus(.up); }
        else if (cp == 'l') { self.moveFocus(.right); }
        else if (cp == '|') { self.splitFocused(.vertical); }
        else if (cp == '-') { self.splitFocused(.horizontal); }
        else if (cp == 'x') { self.closeFocused(); }
        else if (cp == 'z') { self.toggleZoom(); }
        else if (cp == vaxis.Key.escape or cp == 'i') { self.mode = .terminal; }
        else return false;

        if (acted) self.requestRedraw();
        return true;
    }

    // ---- Action dispatch ----

    fn dispatchAction(self: *Layout, action: Action) void {
        switch (action) {
            .split_vertical => self.splitFocused(.vertical),
            .split_horizontal => self.splitFocused(.horizontal),
            .split_auto => self.splitFocused(.none),
            .focus_left => self.moveFocus(.left),
            .focus_right => self.moveFocus(.right),
            .focus_up => self.moveFocus(.up),
            .focus_down => self.moveFocus(.down),
            .cycle_next => self.cycleFocus(true),
            .cycle_prev => self.cycleFocus(false),
            .new_window => self.spawnWindow(),
            .close_pane => self.closeFocused(),
            .toggle_zoom => self.toggleZoom(),
            .resize_left => self.resizeFocused(.left),
            .resize_right => self.resizeFocused(.right),
            .resize_up => self.resizeFocused(.up),
            .resize_down => self.resizeFocused(.down),
            .rotate_split => {
                if (self.focused_id) |fid| self.currentTree().rotateSplit(fid);
            },
            .equalize_splits => self.currentTree().equalizeRatios(),
            .swap_left => self.swapWithNeighbor(.left),
            .swap_right => self.swapWithNeighbor(.right),
            .workspace_1 => self.switchWorkspace(0),
            .workspace_2 => self.switchWorkspace(1),
            .workspace_3 => self.switchWorkspace(2),
            .workspace_4 => self.switchWorkspace(3),
            .workspace_5 => self.switchWorkspace(4),
            .workspace_6 => self.switchWorkspace(5),
            .workspace_7 => self.switchWorkspace(6),
            .workspace_8 => self.switchWorkspace(7),
            .workspace_9 => self.switchWorkspace(8),
            .move_to_workspace_1 => self.moveWindowToWorkspace(0),
            .move_to_workspace_2 => self.moveWindowToWorkspace(1),
            .move_to_workspace_3 => self.moveWindowToWorkspace(2),
            .move_to_workspace_4 => self.moveWindowToWorkspace(3),
            .move_to_workspace_5 => self.moveWindowToWorkspace(4),
            .move_to_workspace_6 => self.moveWindowToWorkspace(5),
            .move_to_workspace_7 => self.moveWindowToWorkspace(6),
            .move_to_workspace_8 => self.moveWindowToWorkspace(7),
            .move_to_workspace_9 => self.moveWindowToWorkspace(8),
            .enter_wm_mode => { self.mode = .window_management; },
            .enter_terminal_mode => { self.mode = .terminal; },
            .enter_copy_mode => {}, // TODO: Phase D
            .floating_toggle => self.toggleFloating(),
            .command_palette => {}, // TODO: Phase D
            .session_switcher => {}, // TODO: Phase D
            .help_toggle => { self.show_help = !self.show_help; },
            .detach_session => {
                if (self.detach_callback) |cb| cb(self.detach_ctx, "default") catch {};
            },
            .rename_session => {}, // TODO
            .switch_session => {}, // TODO
            .quit => {
                if (self.exit_callback) |cb| cb(self.exit_ctx);
            },
        }
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

    fn toggleFloating(self: *Layout) void {
        const fid = self.focused_id orelse return;

        // Check if already floating
        for (0..self.floating_count) |i| {
            if (self.floating[i].id == fid) {
                // Un-float: remove from floating list, add back to BSP
                const fw = self.floating[i];
                _ = fw;
                // Shift remaining floating windows
                var j = i;
                while (j + 1 < self.floating_count) : (j += 1) {
                    self.floating[j] = self.floating[j + 1];
                }
                self.floating_count -= 1;

                // Re-insert into BSP tree
                const tree = self.currentTree();
                var ids: [64]u32 = undefined;
                const count = tree.getAllWindowIDs(&ids);
                const focused = if (count > 0) ids[0] else fid;
                tree.insertWindow(fid, focused, .none, self.tileBounds());
                self.requestRedraw();
                return;
            }
        }

        // Float: remove from BSP, add to floating list
        if (self.floating_count >= max_floating) return;

        self.currentTree().removeWindow(fid);

        // Center on screen at 60% size
        const bounds = self.tileBounds();
        const fw_w = @max(bounds.w * 6 / 10, min_window_width);
        const fw_h = @max(bounds.h * 6 / 10, min_window_height);
        const fw_x = (bounds.w -| fw_w) / 2;
        const fw_y = (bounds.h -| fw_h) / 2;

        self.floating[self.floating_count] = .{
            .id = fid,
            .x = fw_x,
            .y = fw_y,
            .w = fw_w,
            .h = fw_h,
        };
        self.floating_count += 1;
        self.requestRedraw();
    }

    fn removeFloating(self: *Layout, wid: u32) void {
        for (0..self.floating_count) |i| {
            if (self.floating[i].id == wid) {
                var j = i;
                while (j + 1 < self.floating_count) : (j += 1) {
                    self.floating[j] = self.floating[j + 1];
                }
                self.floating_count -= 1;
                return;
            }
        }
    }

    fn isFloating(self: *const Layout, wid: u32) bool {
        for (0..self.floating_count) |i| {
            if (self.floating[i].id == wid) return true;
        }
        return false;
    }

    fn getFloating(self: *Layout, wid: u32) ?*FloatingWindow {
        for (0..self.floating_count) |i| {
            if (self.floating[i].id == wid) return &self.floating[i];
        }
        return null;
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

    fn resizeFocused(self: *Layout, dir: bsp.Direction) void {
        const fid = self.focused_id orelse return;
        const tree = self.currentTree();
        const node_idx = tree.mapGet(fid) orelse return;
        const parent_idx = tree.nodes[node_idx].parent;
        if (parent_idx == bsp.null_idx) return;

        const delta: f32 = 0.05; // 5% per step
        const is_left_child = tree.nodes[parent_idx].left == node_idx;
        const parent_split = tree.nodes[parent_idx].split;

        // Determine if this resize direction affects the parent's split
        const affects = switch (dir) {
            .left, .right => parent_split == .vertical,
            .up, .down => parent_split == .horizontal,
        };

        if (!affects) return;

        // Growing: left child grows → ratio increases; right child grows → ratio decreases
        const grow = switch (dir) {
            .right, .down => is_left_child,
            .left, .up => !is_left_child,
        };

        const new_ratio = if (grow)
            tree.nodes[parent_idx].ratio + delta
        else
            tree.nodes[parent_idx].ratio - delta;

        tree.nodes[parent_idx].ratio = std.math.clamp(new_ratio, 0.1, 0.9);
        self.requestRedraw();
    }

    fn swapWithNeighbor(self: *Layout, dir: bsp.Direction) void {
        const fid = self.focused_id orelse return;
        const tree = self.currentTree();
        if (tree.findNeighbor(fid, dir, self.tileBounds())) |neighbor_id| {
            tree.swapWindows(fid, neighbor_id);
            self.requestRedraw();
        }
    }

    fn moveWindowToWorkspace(self: *Layout, target_ws: u8) void {
        if (target_ws >= max_workspaces) return;
        if (target_ws == self.active_workspace) return;
        const fid = self.focused_id orelse return;

        // Remove from current workspace BSP
        self.currentTree().removeWindow(fid);

        // Insert into target workspace BSP
        // Find something to split against, or just insert as first
        var target_focused: u32 = fid;
        var target_ids: [64]u32 = undefined;
        const target_count = self.workspaces[target_ws].getAllWindowIDs(&target_ids);
        if (target_count > 0) {
            target_focused = target_ids[0];
        }
        self.workspaces[target_ws].insertWindow(fid, target_focused, .none, self.tileBounds());

        // Update workspace mapping
        self.setWindowWorkspace(fid, target_ws);

        // Focus next window in current workspace
        self.focused_id = self.findNextFocusable();
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
        const mx: u16 = @intCast(@max(0, mouse.col));
        const my: u16 = @intCast(@max(0, mouse.row));

        switch (mouse.type) {
            .press => {
                // Check if click is on a floating window
                if (self.findFloatingAt(mx, my)) |fw| {
                    // Focus the floating window
                    self.focused_id = fw.id;
                    self.updateFocusState();

                    if (mouse.button == .right) {
                        // Right-click: start resize
                        const corner = self.detectCorner(&fw, mx, my);
                        self.drag = .{
                            .window_id = fw.id,
                            .mode = .resize,
                            .start_x = mx,
                            .start_y = my,
                            .orig_x = fw.x,
                            .orig_y = fw.y,
                            .orig_w = fw.w,
                            .orig_h = fw.h,
                            .corner = corner,
                        };
                    } else {
                        // Left-click: start move
                        self.drag = .{
                            .window_id = fw.id,
                            .mode = .move,
                            .start_x = mx,
                            .start_y = my,
                            .orig_x = fw.x,
                            .orig_y = fw.y,
                            .orig_w = fw.w,
                            .orig_h = fw.h,
                        };
                    }
                    self.requestRedraw();
                }
            },
            .drag => {
                if (self.drag) |d| {
                    const fw = self.getFloating(d.window_id) orelse return;
                    const bounds = self.tileBounds();

                    switch (d.mode) {
                        .move => {
                            const dx: i32 = @as(i32, mx) - @as(i32, d.start_x);
                            const dy: i32 = @as(i32, my) - @as(i32, d.start_y);
                            const new_x = @as(i32, d.orig_x) + dx;
                            const new_y = @as(i32, d.orig_y) + dy;
                            fw.x = @intCast(std.math.clamp(new_x, 0, @as(i32, bounds.w) - @as(i32, min_window_width)));
                            fw.y = @intCast(std.math.clamp(new_y, 0, @as(i32, bounds.h) - @as(i32, min_window_height)));
                        },
                        .resize => {
                            const dx: i32 = @as(i32, mx) - @as(i32, d.start_x);
                            const dy: i32 = @as(i32, my) - @as(i32, d.start_y);
                            self.applyResize(fw, d, dx, dy, bounds);
                        },
                    }
                    self.requestRedraw();
                }
            },
            .release => {
                self.drag = null;
            },
            else => {},
        }
    }

    fn findFloatingAt(self: *const Layout, mx: u16, my: u16) ?FloatingWindow {
        // Search in reverse order (topmost first)
        var i: i32 = @as(i32, self.floating_count) - 1;
        while (i >= 0) : (i -= 1) {
            const fw = self.floating[@intCast(i)];
            if (mx >= fw.x and mx < fw.x + fw.w and my >= fw.y and my < fw.y + fw.h) {
                return fw;
            }
        }
        return null;
    }

    fn detectCorner(self: *const Layout, fw: *const FloatingWindow, mx: u16, my: u16) DragState.ResizeCorner {
        _ = self;
        const mid_x = fw.x + fw.w / 2;
        const mid_y = fw.y + fw.h / 2;
        if (mx < mid_x) {
            return if (my < mid_y) .top_left else .bottom_left;
        } else {
            return if (my < mid_y) .top_right else .bottom_right;
        }
    }

    fn applyResize(self: *const Layout, fw: *FloatingWindow, d: DragState, dx: i32, dy: i32, bounds: bsp.Rect) void {
        _ = self;
        switch (d.corner) {
            .bottom_right => {
                fw.w = @intCast(@max(min_window_width, @as(i32, d.orig_w) + dx));
                fw.h = @intCast(@max(min_window_height, @as(i32, d.orig_h) + dy));
            },
            .bottom_left => {
                const new_x = @as(i32, d.orig_x) + dx;
                const new_w = @as(i32, d.orig_w) - dx;
                if (new_w >= min_window_width and new_x >= 0) {
                    fw.x = @intCast(new_x);
                    fw.w = @intCast(new_w);
                }
                fw.h = @intCast(@max(min_window_height, @as(i32, d.orig_h) + dy));
            },
            .top_right => {
                fw.w = @intCast(@max(min_window_width, @as(i32, d.orig_w) + dx));
                const new_y = @as(i32, d.orig_y) + dy;
                const new_h = @as(i32, d.orig_h) - dy;
                if (new_h >= min_window_height and new_y >= 0) {
                    fw.y = @intCast(new_y);
                    fw.h = @intCast(new_h);
                }
            },
            .top_left => {
                const new_x = @as(i32, d.orig_x) + dx;
                const new_w = @as(i32, d.orig_w) - dx;
                if (new_w >= min_window_width and new_x >= 0) {
                    fw.x = @intCast(new_x);
                    fw.w = @intCast(new_w);
                }
                const new_y = @as(i32, d.orig_y) + dy;
                const new_h = @as(i32, d.orig_h) - dy;
                if (new_h >= min_window_height and new_y >= 0) {
                    fw.y = @intCast(new_y);
                    fw.h = @intCast(new_h);
                }
            },
        }
        // Clamp to viewport
        if (fw.x + fw.w > bounds.w) fw.w = bounds.w -| fw.x;
        if (fw.y + fw.h > bounds.h) fw.h = bounds.h -| fw.y;
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
        const col_children = try alloc.alloc(widget.Widget, 2);
        col_children[0] = content;
        col_children[0].ratio = 1.0;
        col_children[1] = status;

        const base = widget.Widget{
            .kind = .{ .column = .{
                .children = col_children,
                .cross_axis_align = .stretch,
            } },
        };

        // Determine number of overlay layers
        var overlay_count: usize = 0;
        if (self.floating_count > 0) overlay_count += self.floating_count;
        if (self.show_help) overlay_count += 1;

        if (overlay_count == 0) return base;

        // Build stack: base + floating windows + optional help
        const stack_children = try alloc.alloc(widget.Widget, 1 + overlay_count);
        stack_children[0] = base;

        var si: usize = 1;

        // Add floating windows (render order: unfocused first, focused last)
        for (0..self.floating_count) |i| {
            const fw = self.floating[i];
            if (self.focused_id != fw.id) {
                stack_children[si] = try self.buildFloatingWidget(fw);
                si += 1;
            }
        }
        // Focused floating window on top
        for (0..self.floating_count) |i| {
            const fw = self.floating[i];
            if (self.focused_id == fw.id) {
                stack_children[si] = try self.buildFloatingWidget(fw);
                si += 1;
            }
        }

        if (self.show_help) {
            stack_children[si] = try self.buildHelpOverlay();
            si += 1;
        }

        // Trim if we allocated too much (shouldn't happen but safe)
        return widget.Widget{
            .kind = .{ .stack = .{ .children = stack_children[0..si] } },
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

        // Empty workspace — show hint
        if (tree.isEmpty()) {
            const alloc = self.allocator;
            var spans: std.ArrayList(widget.Text.Span) = .empty;
            try spans.append(alloc, .{
                .text = try alloc.dupe(u8, "  Empty workspace. Press Ctrl+B c to create a window."),
                .style = .{ .fg = .{ .rgb = .{ 0x66, 0x66, 0x66 } } },
            });
            return widget.Widget{
                .kind = .{ .text = .{
                    .spans = try spans.toOwnedSlice(alloc),
                } },
            };
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

        if (node_idx == bsp.null_idx) {
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
                .style = .{ .fg = self.theme.borderUnfocused() },
                .border = .rounded,
            } },
        };

        children[2] = try self.buildBSPWidget(node.right, right_bounds);
        children[2].ratio = 1.0 - node.ratio;

        // Use node_idx + offset as widget ID for split_resize mapping
        const split_widget_id: u32 = @as(u32, node_idx) + 0x10000;

        if (is_vertical) {
            return widget.Widget{
                .kind = .{ .row = .{
                    .children = children,
                    .cross_axis_align = .stretch,
                    .resizable = true,
                } },
                .id = split_widget_id,
            };
        } else {
            return widget.Widget{
                .kind = .{ .column = .{
                    .children = children,
                    .cross_axis_align = .stretch,
                    .resizable = true,
                } },
                .id = split_widget_id,
            };
        }
    }

    fn buildFloatingWidget(self: *Layout, fw: FloatingWindow) !widget.Widget {
        const alloc = self.allocator;
        const focused = self.focused_id == fw.id;

        if (self.ptys.get(fw.id)) |pty| {
            const bordered = try self.buildBorderedSurface(fw.id, pty.surface, focused);
            const child = try alloc.create(widget.Widget);
            child.* = bordered;
            child.width = fw.w;
            child.height = fw.h;

            return widget.Widget{
                .kind = .{ .positioned = .{
                    .child = child,
                    .x = fw.x,
                    .y = fw.y,
                    .anchor = .top_left,
                } },
            };
        }
        return self.emptyWidget();
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

        const border_color = if (focused) self.theme.borderFocused() else self.theme.borderUnfocused();

        return widget.Widget{
            .kind = .{ .box = .{
                .child = child,
                .border = .rounded,
                .style = .{ .fg = border_color },
            } },
            .id = wid,
        };
    }

    fn buildHelpOverlay(self: *Layout) !widget.Widget {
        const alloc = self.allocator;

        const help_lines = [_][]const u8{
            "  tuios keybinds (press ? or Esc to close)",
            "",
            "  Ctrl+B  |     Split vertical",
            "  Ctrl+B  -     Split horizontal",
            "  Ctrl+B  h/j/k/l  Focus left/down/up/right",
            "  Ctrl+B  c     New window",
            "  Ctrl+B  x     Close window",
            "  Ctrl+B  z     Toggle zoom",
            "  Ctrl+B  n/Tab Next window",
            "  Ctrl+B  p     Previous window",
            "  Ctrl+B  r     Rotate split",
            "  Ctrl+B  =     Equalize splits",
            "  Ctrl+B  1-9   Switch workspace",
            "  Ctrl+B  Shift+1-9  Move to workspace",
            "  Ctrl+B  H/J/K/L  Resize pane",
            "  Ctrl+B  arrows   Resize pane",
            "  Ctrl+B  {/}   Swap with neighbor",
            "  Ctrl+B  w     Window management mode",
            "  Ctrl+B  d     Detach session",
            "  Ctrl+B  q     Quit",
            "  Alt+1-9       Switch workspace (no prefix)",
            "",
            "  Window Management Mode:",
            "  h/j/k/l  Focus left/down/up/right",
            "  |/-      Split vertical/horizontal",
            "  x        Close window",
            "  z        Toggle zoom",
            "  Esc/i    Return to terminal mode",
        };

        var spans = std.ArrayList(widget.Text.Span).empty;
        for (help_lines) |line| {
            if (spans.items.len > 0) {
                try spans.append(alloc, .{
                    .text = try alloc.dupe(u8, "\n"),
                    .style = .{ .fg = .{ .rgb = .{ 0xbb, 0xbb, 0xbb } }, .bg = .{ .rgb = .{ 0x1e, 0x1e, 0x2e } } },
                });
            }
            try spans.append(alloc, .{
                .text = try alloc.dupe(u8, line),
                .style = .{
                    .fg = if (line.len > 2 and std.mem.startsWith(u8, line, "  tuios"))
                        self.theme.borderFocused()
                    else if (line.len > 2 and std.mem.startsWith(u8, line, "  Window"))
                        self.theme.borderFocused()
                    else
                        .{ .rgb = .{ 0xcc, 0xcc, 0xcc } },
                    .bg = .{ .rgb = .{ 0x1e, 0x1e, 0x2e } },
                    .bold = std.mem.startsWith(u8, line, "  tuios") or std.mem.startsWith(u8, line, "  Window"),
                },
            });
        }

        const text_widget = try alloc.create(widget.Widget);
        text_widget.* = .{
            .kind = .{ .text = .{
                .spans = try spans.toOwnedSlice(alloc),
                .wrap = .word,
            } },
        };

        const padded = try alloc.create(widget.Widget);
        padded.* = .{
            .kind = .{ .padding = .{
                .child = text_widget,
                .top = 1,
                .bottom = 1,
                .left = 2,
                .right = 2,
            } },
        };

        return widget.Widget{
            .kind = .{ .positioned = .{
                .child = padded,
                .anchor = .center,
            } },
        };
    }

    fn buildStatusBar(self: *Layout) !widget.Widget {
        const alloc = self.allocator;

        // Build dock: separator line + content bar (2 rows like Go tuios)
        const dock_children = try alloc.alloc(widget.Widget, 2);

        // Row 1: Separator line
        dock_children[0] = widget.Widget{
            .kind = .{ .separator = .{
                .axis = .horizontal,
                .style = .{ .fg = self.theme.borderUnfocused() },
                .border = .single,
            } },
        };

        // Row 2: Status content with window tabs
        var spans: std.ArrayList(widget.Text.Span) = .empty;

        // Mode indicator pill
        const is_pending = self.matcher.isPending();
        const mode_text = switch (self.mode) {
            .terminal => if (is_pending) " PREFIX " else " T ",
            .window_management => " WM ",
        };
        const mode_fg: vaxis.Color = switch (self.mode) {
            .terminal => if (is_pending) .{ .rgb = .{ 0, 0, 0 } } else self.theme.statusFg(),
            .window_management => .{ .rgb = .{ 0, 0, 0 } },
        };
        const mode_bg = switch (self.mode) {
            .terminal => if (is_pending) self.theme.prefixIndicator() else self.theme.statusBg(),
            .window_management => self.theme.wmModeIndicator(),
        };

        try spans.append(alloc, .{
            .text = try alloc.dupe(u8, mode_text),
            .style = .{ .fg = mode_fg, .bg = mode_bg, .bold = true },
        });

        // Workspace number
        var ws_buf: [8]u8 = undefined;
        const ws_text = std.fmt.bufPrint(&ws_buf, " {d} ", .{self.active_workspace + 1}) catch "?";
        try spans.append(alloc, .{
            .text = try alloc.dupe(u8, ws_text),
            .style = .{ .fg = self.theme.accentColor(), .bg = self.theme.statusBg(), .bold = true },
        });

        // Separator
        try spans.append(alloc, .{
            .text = try alloc.dupe(u8, "\xe2\x94\x82"), // │
            .style = .{ .fg = self.theme.dimColor(), .bg = self.theme.statusBg() },
        });

        // Window tabs — show each window as a tab pill
        const tree = self.currentTreeConst();
        var win_ids: [64]u32 = undefined;
        const win_count = tree.getAllWindowIDs(&win_ids);

        // Also include floating windows
        for (0..self.floating_count) |fi| {
            if (win_count + fi < 64) {
                win_ids[win_count + fi] = self.floating[fi].id;
            }
        }
        const total_windows = win_count + self.floating_count;

        for (0..total_windows) |i| {
            const wid = win_ids[i];
            const focused = self.focused_id == wid;
            const is_float = self.isFloating(wid);

            // Get window title from surface
            var title_buf: [16]u8 = undefined;
            var title: []const u8 = "shell";
            if (self.ptys.get(wid)) |pty| {
                const surface_title = pty.surface.getTitle();
                if (surface_title.len > 0) {
                    const max_len = @min(surface_title.len, 12);
                    @memcpy(title_buf[0..max_len], surface_title[0..max_len]);
                    title = title_buf[0..max_len];
                }
            }

            // Float indicator
            const prefix: []const u8 = if (is_float) " \xe2\x96\xa3 " else " "; // ▣ for floating
            const suffix: []const u8 = " ";

            var tab_buf: [32]u8 = undefined;
            const tab_text = std.fmt.bufPrint(&tab_buf, "{s}{s}{s}", .{ prefix, title, suffix }) catch " ? ";

            const tab_fg: vaxis.Color = if (focused) .{ .rgb = .{ 0, 0, 0 } } else self.theme.dimColor();
            const tab_bg: vaxis.Color = if (focused) self.theme.borderFocused() else self.theme.statusBg();

            try spans.append(alloc, .{
                .text = try alloc.dupe(u8, tab_text),
                .style = .{ .fg = tab_fg, .bg = tab_bg, .bold = focused },
            });
        }

        // Zoom indicator on the right
        if (self.zoomed) {
            try spans.append(alloc, .{
                .text = try alloc.dupe(u8, " ZOOM "),
                .style = .{ .fg = .{ .rgb = .{ 0, 0, 0 } }, .bg = self.theme.errorColor(), .bold = true },
            });
        }

        dock_children[1] = widget.Widget{
            .kind = .{ .text = .{
                .spans = try spans.toOwnedSlice(alloc),
            } },
        };

        return widget.Widget{
            .kind = .{ .column = .{
                .children = dock_children,
                .cross_axis_align = .stretch,
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
        const alloc = self.allocator;
        var buf = std.ArrayList(u8).empty;
        const writer = buf.writer(alloc);

        try writer.writeAll("{\"workspaces\":[");
        for (0..max_workspaces) |ws| {
            if (ws > 0) try writer.writeAll(",");
            try writer.writeAll("{\"windows\":[");
            var ids: [64]u32 = undefined;
            const count = self.workspaces[ws].getAllWindowIDs(&ids);
            for (0..count) |i| {
                if (i > 0) try writer.writeAll(",");
                try writer.writeAll("{\"id\":");
                try writer.print("{d}", .{ids[i]});
                // Include CWD if available
                if (cwd_lookup_fn) |lookup| {
                    if (lookup(cwd_lookup_ctx, @intCast(ids[i]))) |cwd| {
                        try writer.writeAll(",\"cwd\":\"");
                        // Simple JSON string escape
                        for (cwd) |c| {
                            if (c == '"') {
                                try writer.writeAll("\\\"");
                            } else if (c == '\\') {
                                try writer.writeAll("\\\\");
                            } else {
                                try writer.writeByte(c);
                            }
                        }
                        try writer.writeAll("\"");
                    }
                }
                try writer.writeAll("}");
            }
            try writer.writeAll("]}");
        }
        try writer.writeAll("],\"active_workspace\":");
        try writer.print("{d}", .{self.active_workspace});
        try writer.writeAll("}");

        return try buf.toOwnedSlice(alloc);
    }

    pub fn setStateFromJson(self: *Layout, json: []const u8, pty_lookup_fn: PtyLookupFn, pty_lookup_ctx: *anyopaque) !void {
        // Parse JSON to restore window layout
        // For now, just use the simple approach: parse windows and attach them
        _ = self;
        _ = pty_lookup_fn;
        _ = pty_lookup_ctx;

        if (json.len <= 2) return; // "{}" — nothing to restore
        // Full restoration would parse the JSON, spawn PTYs for each saved window
        // with their cwds, and rebuild the BSP trees. This requires server-side
        // coordination. For now, the client handles session restore by spawning
        // PTYs from the saved session file.
        log.info("setStateFromJson: received {d} bytes (restore via session file)", .{json.len});
    }
};
