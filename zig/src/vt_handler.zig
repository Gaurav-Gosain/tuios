//! Virtual terminal escape sequence handler.

const std = @import("std");

const ghostty_vt = @import("ghostty-vt");
const main = @import("main.zig");

const log = std.log.scoped(.vt_handler);

// Helper aliases for ghostty types
const Terminal = ghostty_vt.Terminal;
const Screen = ghostty_vt.Screen;
const modes = ghostty_vt.modes;
const osc_color = ghostty_vt.osc.color;
const kitty_color = ghostty_vt.kitty.color;
const Action = ghostty_vt.StreamAction;

pub const ColorTarget = osc_color.Target;

/// Custom VT handler that wraps the ghostty Terminal and can be extended
/// to handle responses to queries (e.g. for device attributes, etc.)
pub const Handler = struct {
    /// The terminal state to modify.
    terminal: *Terminal,

    /// Optional callback for sending data back to the PTY
    /// (for responding to queries, etc.)
    write_fn: ?*const fn (ctx: ?*anyopaque, data: []const u8) anyerror!void = null,
    write_ctx: ?*anyopaque = null,

    /// Optional callback for notifying title changes
    title_fn: ?*const fn (ctx: ?*anyopaque, title: []const u8) anyerror!void = null,
    title_ctx: ?*anyopaque = null,

    /// Optional callback for notifying cwd changes (OSC 7)
    cwd_fn: ?*const fn (ctx: ?*anyopaque, cwd: []const u8) anyerror!void = null,
    cwd_ctx: ?*anyopaque = null,

    /// Optional callback for color query requests (OSC 4/10/11/12)
    color_query_fn: ?*const fn (ctx: ?*anyopaque, target: osc_color.Target) anyerror!void = null,
    color_query_ctx: ?*anyopaque = null,

    /// Optional callback for DA1 (primary device attributes) query
    da1_fn: ?*const fn (ctx: ?*anyopaque) anyerror!void = null,
    da1_ctx: ?*anyopaque = null,

    /// Optional callback for APC sequences (kitty graphics protocol)
    apc_fn: ?*const fn (ctx: ?*anyopaque, data: []const u8) anyerror!void = null,
    apc_ctx: ?*anyopaque = null,

    /// APC accumulation buffer (kitty graphics payloads)
    apc_buf: [65536]u8 = undefined,
    apc_len: usize = 0,
    apc_active: bool = false,

    pub fn init(terminal: *Terminal) Handler {
        return .{
            .terminal = terminal,
        };
    }

    pub fn deinit(self: *Handler) void {
        _ = self;
    }

    /// Set the callback for writing data back to the PTY
    pub fn setWriteCallback(
        self: *Handler,
        ctx: ?*anyopaque,
        write_fn: *const fn (ctx: ?*anyopaque, data: []const u8) anyerror!void,
    ) void {
        self.write_ctx = ctx;
        self.write_fn = write_fn;
    }

    /// Set the callback for notifying title changes
    pub fn setTitleCallback(
        self: *Handler,
        ctx: ?*anyopaque,
        title_fn: *const fn (ctx: ?*anyopaque, title: []const u8) anyerror!void,
    ) void {
        self.title_ctx = ctx;
        self.title_fn = title_fn;
    }

    /// Set the callback for notifying cwd changes (OSC 7)
    pub fn setCwdCallback(
        self: *Handler,
        ctx: ?*anyopaque,
        cwd_fn: *const fn (ctx: ?*anyopaque, cwd: []const u8) anyerror!void,
    ) void {
        self.cwd_ctx = ctx;
        self.cwd_fn = cwd_fn;
    }

    /// Set the callback for color query requests (OSC 4/10/11/12)
    pub fn setColorQueryCallback(
        self: *Handler,
        ctx: ?*anyopaque,
        color_query_fn: *const fn (ctx: ?*anyopaque, target: osc_color.Target) anyerror!void,
    ) void {
        self.color_query_ctx = ctx;
        self.color_query_fn = color_query_fn;
    }

    /// Set the callback for DA1 (primary device attributes) query
    pub fn setDa1Callback(
        self: *Handler,
        ctx: ?*anyopaque,
        da1_fn: *const fn (ctx: ?*anyopaque) anyerror!void,
    ) void {
        self.da1_ctx = ctx;
        self.da1_fn = da1_fn;
    }

    /// Set callback for APC sequences (kitty graphics passthrough)
    pub fn setApcCallback(
        self: *Handler,
        ctx: ?*anyopaque,
        apc_fn: *const fn (ctx: ?*anyopaque, data: []const u8) anyerror!void,
    ) void {
        self.apc_ctx = ctx;
        self.apc_fn = apc_fn;
    }

    /// Write data back to the PTY (if callback is set)
    fn write(self: *Handler, data: []const u8) !void {
        if (self.write_fn) |func| {
            try func(self.write_ctx, data);
        }
    }

    pub fn vt(
        self: *Handler,
        comptime action: Action.Tag,
        value: Action.Value(action),
    ) void {
        self.vtFallible(action, value) catch |err| {
            log.warn("error handling VT action action={} err={}", .{ action, err });
        };
    }

    fn vtFallible(
        self: *Handler,
        comptime action: Action.Tag,
        value: Action.Value(action),
    ) !void {
        switch (action) {
            .print => try self.terminal.print(value.cp),
            .print_repeat => try self.terminal.printRepeat(value),
            .backspace => self.terminal.backspace(),
            .carriage_return => self.terminal.carriageReturn(),
            .linefeed => try self.terminal.linefeed(),
            .index => try self.terminal.index(),
            .next_line => {
                try self.terminal.index();
                self.terminal.carriageReturn();
            },
            .reverse_index => self.terminal.reverseIndex(),

            .cursor_up => self.terminal.cursorUp(value.value),
            .cursor_down => self.terminal.cursorDown(value.value),
            .cursor_left => self.terminal.cursorLeft(value.value),
            .cursor_right => self.terminal.cursorRight(value.value),
            .cursor_pos => self.terminal.setCursorPos(value.row, value.col),
            .cursor_col => self.terminal.setCursorPos(self.terminal.screens.active.cursor.y + 1, value.value),
            .cursor_row => self.terminal.setCursorPos(value.value, self.terminal.screens.active.cursor.x + 1),
            .cursor_col_relative => self.terminal.setCursorPos(
                self.terminal.screens.active.cursor.y + 1,
                self.terminal.screens.active.cursor.x + 1 +| value.value,
            ),
            .cursor_row_relative => self.terminal.setCursorPos(
                self.terminal.screens.active.cursor.y + 1 +| value.value,
                self.terminal.screens.active.cursor.x + 1,
            ),

            .cursor_style => try self.handleCursorStyle(value),

            .erase_display_below => self.terminal.eraseDisplay(.below, value),
            .erase_display_above => self.terminal.eraseDisplay(.above, value),
            .erase_display_complete => self.terminal.eraseDisplay(.complete, value),
            .erase_display_scrollback => self.terminal.eraseDisplay(.scrollback, value),
            .erase_display_scroll_complete => self.terminal.eraseDisplay(.scroll_complete, value),
            .erase_line_right => self.terminal.eraseLine(.right, value),
            .erase_line_left => self.terminal.eraseLine(.left, value),
            .erase_line_complete => self.terminal.eraseLine(.complete, value),
            .erase_line_right_unless_pending_wrap => self.terminal.eraseLine(.right_unless_pending_wrap, value),

            .delete_chars => self.terminal.deleteChars(value),
            .erase_chars => self.terminal.eraseChars(value),
            .insert_lines => self.terminal.insertLines(value),
            .insert_blanks => self.terminal.insertBlanks(value),
            .delete_lines => self.terminal.deleteLines(value),
            .scroll_up => try self.terminal.scrollUp(value),
            .scroll_down => self.terminal.scrollDown(value),

            .horizontal_tab => try self.horizontalTab(value),
            .horizontal_tab_back => try self.horizontalTabBack(value),
            .tab_clear_current => self.terminal.tabClear(.current),
            .tab_clear_all => self.terminal.tabClear(.all),
            .tab_set => self.terminal.tabSet(),
            .tab_reset => self.terminal.tabReset(),

            .set_mode => try self.setMode(value.mode, true),
            .reset_mode => try self.setMode(value.mode, false),
            .save_mode => self.terminal.modes.save(value.mode),
            .restore_mode => {
                const v = self.terminal.modes.restore(value.mode);
                try self.setMode(value.mode, v);
            },

            .top_and_bottom_margin => self.terminal.setTopAndBottomMargin(value.top_left, value.bottom_right),
            .left_and_right_margin => self.terminal.setLeftAndRightMargin(value.top_left, value.bottom_right),
            .left_and_right_margin_ambiguous => try self.handleLeftRightMarginAmbiguous(),

            .save_cursor => self.terminal.saveCursor(),
            .restore_cursor => self.terminal.restoreCursor(),

            .invoke_charset => self.terminal.invokeCharset(value.bank, value.charset, value.locking),
            .configure_charset => self.terminal.configureCharset(value.slot, value.charset),

            .set_attribute => switch (value) {
                .unknown => {},
                else => self.terminal.setAttribute(value) catch {},
            },
            .protected_mode_off => self.terminal.setProtectedMode(.off),
            .protected_mode_iso => self.terminal.setProtectedMode(.iso),
            .protected_mode_dec => self.terminal.setProtectedMode(.dec),

            .mouse_shift_capture => self.terminal.flags.mouse_shift_capture = if (value) .true else .false,
            .kitty_keyboard_push => self.terminal.screens.active.kitty_keyboard.push(value.flags),
            .kitty_keyboard_pop => self.terminal.screens.active.kitty_keyboard.pop(@intCast(value)),
            .kitty_keyboard_set => self.terminal.screens.active.kitty_keyboard.set(.set, value.flags),
            .kitty_keyboard_set_or => self.terminal.screens.active.kitty_keyboard.set(.@"or", value.flags),
            .kitty_keyboard_set_not => self.terminal.screens.active.kitty_keyboard.set(.not, value.flags),
            .modify_key_format => try self.handleModifyKeyFormat(value),

            .active_status_display => self.terminal.status_display = value,
            .decaln => try self.terminal.decaln(),
            .full_reset => self.terminal.fullReset(),

            .start_hyperlink => try self.terminal.screens.active.startHyperlink(value.uri, value.id),
            .end_hyperlink => self.terminal.screens.active.endHyperlink(),

            .semantic_prompt => try self.terminal.semanticPrompt(value),

            .mouse_shape => self.terminal.mouse_shape = value,

            .color_operation => try self.colorOperation(value.op, &value.requests),
            .kitty_color_report => try self.kittyColorOperation(value),

            .device_attributes => try self.handleDeviceAttributes(value),

            .dcs_hook,
            .dcs_put,
            .dcs_unhook,
            => {},

            .apc_start => {
                self.apc_active = true;
                self.apc_len = 0;
            },
            .apc_put => |byte_val| {
                if (self.apc_active and self.apc_len < self.apc_buf.len) {
                    self.apc_buf[self.apc_len] = @intFromEnum(byte_val);
                    self.apc_len += 1;
                }
            },
            .apc_end => {
                if (self.apc_active and self.apc_len > 0) {
                    if (self.apc_fn) |func| {
                        func(self.apc_ctx, self.apc_buf[0..self.apc_len]) catch |err| {
                            log.err("APC callback error: {}", .{err});
                        };
                    }
                }
                self.apc_active = false;
                self.apc_len = 0;
            },

            .window_title => {
                if (self.title_fn) |func| {
                    try func(self.title_ctx, value.title);
                }
            },

            .request_mode => try self.requestMode(value.mode),
            .request_mode_unknown => try self.requestModeUnknown(value.mode, value.ansi),

            .kitty_keyboard_query => try self.handleKittyKeyboardQuery(),

            .report_pwd => try self.handleReportPwd(value.url),

            .xtversion => try self.handleXtversion(),

            .device_status => try self.handleDeviceStatus(value),

            .bell,
            .enquiry,
            .size_report,
            .show_desktop_notification,
            .progress_report,
            .clipboard_contents,
            .title_push,
            .title_pop,
            => {},
        }
    }

    /// Handle cursor style action by setting blink mode and style.
    fn handleCursorStyle(self: *Handler, value: anytype) !void {
        const blink = switch (value) {
            .default, .steady_block, .steady_bar, .steady_underline => false,
            .blinking_block, .blinking_bar, .blinking_underline => true,
        };
        const style: Screen.CursorStyle = switch (value) {
            .default, .blinking_block, .steady_block => .block,
            .blinking_bar, .steady_bar => .bar,
            .blinking_underline, .steady_underline => .underline,
        };
        self.terminal.modes.set(.cursor_blinking, blink);
        self.terminal.screens.active.cursor.cursor_style = style;
    }

    /// Handle left-right margin ambiguous action.
    fn handleLeftRightMarginAmbiguous(self: *Handler) !void {
        if (self.terminal.modes.get(.enable_left_and_right_margin)) {
            self.terminal.setLeftAndRightMargin(0, 0);
        } else {
            self.terminal.saveCursor();
        }
    }

    /// Handle modify key format action.
    fn handleModifyKeyFormat(self: *Handler, value: anytype) !void {
        self.terminal.flags.modify_other_keys_2 = false;
        switch (value) {
            .other_keys_numeric => self.terminal.flags.modify_other_keys_2 = true,
            else => {},
        }
    }

    /// Handle device attributes query with appropriate response.
    fn handleDeviceAttributes(self: *Handler, value: anytype) !void {
        switch (value) {
            .primary => {
                // Primary DA (CSI c) - if callback is set, defer the response
                // to allow color queries to be answered first
                if (self.da1_fn) |func| {
                    try func(self.da1_ctx);
                } else {
                    // No callback, respond immediately
                    try self.write("\x1b[?1;2c");
                }
            },
            .secondary => {
                // Secondary DA (CSI > c) - report terminal type and version
                // ESC [ > 0 ; 0 ; 0 c
                try self.write("\x1b[>0;0;0c");
            },
            .tertiary => {
                // Tertiary DA (CSI = c) - usually ignored
            },
        }
    }

    /// Handle kitty keyboard query by reporting current state.
    fn handleKittyKeyboardQuery(self: *Handler) !void {
        const flags = self.terminal.screens.active.kitty_keyboard.current();
        var buf: [32]u8 = undefined;
        const resp = std.fmt.bufPrint(&buf, "\x1b[?{}u", .{flags.int()}) catch return;
        try self.write(resp);
    }

    /// Handle XTVERSION query by reporting tuios version.
    fn handleXtversion(self: *Handler) !void {
        var buf: [64]u8 = undefined;
        const resp = std.fmt.bufPrint(&buf, "\x1bP>|tuios {s}\x1b\\", .{main.version}) catch return;
        try self.write(resp);
    }

    /// Handle device status requests (DSR).
    fn handleDeviceStatus(self: *Handler, value: anytype) !void {
        switch (value.request) {
            .operating_status => {
                // CSI 5 n - Report terminal is ready (no malfunction)
                try self.write("\x1b[0n");
            },
            .cursor_position => {
                // CSI 6 n - Report cursor position as CSI row ; col R
                const cursor = self.terminal.screens.active.cursor;
                var buf: [32]u8 = undefined;
                const resp = std.fmt.bufPrint(&buf, "\x1b[{};{}R", .{
                    cursor.y + 1,
                    cursor.x + 1,
                }) catch return;
                try self.write(resp);
            },
            .color_scheme => {
                // CSI ? 996 n - Report color scheme (1=dark, 2=light)
                // TODO: setup client notifications for color scheme changes and
                // have the server track/respond with the actual scheme
                try self.write("\x1b[?997;1n");
            },
        }
    }

    /// Handle report current working directory (OSC 7).
    fn handleReportPwd(self: *Handler, url: []const u8) !void {
        if (self.cwd_fn) |func| {
            // Parse file:// URL to extract path
            // Format: file://hostname/path or file:///path
            const path = if (std.mem.startsWith(u8, url, "file://")) blk: {
                const after_scheme = url[7..];
                // Skip hostname (find next /)
                if (std.mem.indexOfScalar(u8, after_scheme, '/')) |idx| {
                    break :blk after_scheme[idx..];
                }
                break :blk after_scheme;
            } else url;
            if (path.len > 0) {
                try func(self.cwd_ctx, path);
            }
        }
    }

    inline fn horizontalTab(self: *Handler, count: u16) !void {
        for (0..count) |_| {
            const x = self.terminal.screens.active.cursor.x;
            self.terminal.horizontalTab();
            if (x == self.terminal.screens.active.cursor.x) break;
        }
    }

    inline fn horizontalTabBack(self: *Handler, count: u16) !void {
        for (0..count) |_| {
            const x = self.terminal.screens.active.cursor.x;
            self.terminal.horizontalTabBack();
            if (x == self.terminal.screens.active.cursor.x) break;
        }
    }

    fn setMode(self: *Handler, mode: modes.Mode, enabled: bool) !void {
        self.terminal.modes.set(mode, enabled);

        switch (mode) {
            .autorepeat, .reverse_colors, .enable_mode_3, .synchronized_output, .linefeed, .focus_event => {},
            .origin => self.terminal.setCursorPos(1, 1),
            .enable_left_and_right_margin => self.handleLeftRightMargin(enabled),
            .alt_screen_legacy => try self.terminal.switchScreenMode(.@"47", enabled),
            .alt_screen => try self.terminal.switchScreenMode(.@"1047", enabled),
            .alt_screen_save_cursor_clear_enter => try self.terminal.switchScreenMode(.@"1049", enabled),
            .save_cursor => try self.handleSaveCursor(enabled),
            .@"132_column" => try self.terminal.deccolm(
                self.terminal.screens.active.alloc,
                if (enabled) .@"132_cols" else .@"80_cols",
            ),
            .in_band_size_reports => try self.handleInBandSizeReports(enabled),
            .mouse_event_x10,
            .mouse_event_normal,
            .mouse_event_button,
            .mouse_event_any,
            => self.handleMouseEvent(mode, enabled),
            .mouse_format_utf8,
            .mouse_format_sgr,
            .mouse_format_urxvt,
            .mouse_format_sgr_pixels,
            => self.handleMouseFormat(mode, enabled),
            else => {},
        }
    }

    fn handleLeftRightMargin(self: *Handler, enabled: bool) void {
        if (!enabled) {
            self.terminal.scrolling_region.left = 0;
            self.terminal.scrolling_region.right = self.terminal.cols - 1;
        }
    }

    fn handleSaveCursor(self: *Handler, enabled: bool) !void {
        if (enabled) {
            self.terminal.saveCursor();
        } else {
            self.terminal.restoreCursor();
        }
    }

    fn handleInBandSizeReports(self: *Handler, enabled: bool) !void {
        if (!enabled) return;
        var buf: [64]u8 = undefined;
        const report = std.fmt.bufPrint(&buf, "\x1b[48;{};{};{};{}t", .{
            self.terminal.rows,
            self.terminal.cols,
            self.terminal.height_px,
            self.terminal.width_px,
        }) catch return;
        try self.write(report);
    }

    fn handleMouseEvent(self: *Handler, mode: modes.Mode, enabled: bool) void {
        self.terminal.flags.mouse_event = if (enabled) switch (mode) {
            .mouse_event_x10 => .x10,
            .mouse_event_normal => .normal,
            .mouse_event_button => .button,
            .mouse_event_any => .any,
            else => unreachable,
        } else .none;
    }

    fn handleMouseFormat(self: *Handler, mode: modes.Mode, enabled: bool) void {
        self.terminal.flags.mouse_format = if (enabled) switch (mode) {
            .mouse_format_utf8 => .utf8,
            .mouse_format_sgr => .sgr,
            .mouse_format_urxvt => .urxvt,
            .mouse_format_sgr_pixels => .sgr_pixels,
            else => unreachable,
        } else .x10;
    }

    fn colorOperation(
        self: *Handler,
        op: osc_color.Operation,
        requests: *const osc_color.List,
    ) !void {
        _ = op;
        if (requests.count() == 0) return;

        var it = requests.constIterator(0);
        while (it.next()) |req| {
            switch (req.*) {
                .set => |set| {
                    switch (set.target) {
                        .palette => |i| {
                            self.terminal.flags.dirty.palette = true;
                            self.terminal.colors.palette.set(i, set.color);
                        },
                        .dynamic => |dynamic| switch (dynamic) {
                            .foreground => self.terminal.colors.foreground.set(set.color),
                            .background => self.terminal.colors.background.set(set.color),
                            .cursor => self.terminal.colors.cursor.set(set.color),
                            .pointer_foreground,
                            .pointer_background,
                            .tektronix_foreground,
                            .tektronix_background,
                            .highlight_background,
                            .tektronix_cursor,
                            .highlight_foreground,
                            => {},
                        },
                        .special => {},
                    }
                },

                .reset => |target| switch (target) {
                    .palette => |i| {
                        self.terminal.flags.dirty.palette = true;
                        self.terminal.colors.palette.reset(i);
                    },
                    .dynamic => |dynamic| switch (dynamic) {
                        .foreground => self.terminal.colors.foreground.reset(),
                        .background => self.terminal.colors.background.reset(),
                        .cursor => self.terminal.colors.cursor.reset(),
                        .pointer_foreground,
                        .pointer_background,
                        .tektronix_foreground,
                        .tektronix_background,
                        .highlight_background,
                        .tektronix_cursor,
                        .highlight_foreground,
                        => {},
                    },
                    .special => {},
                },

                .reset_palette => {
                    const mask = &self.terminal.colors.palette.mask;
                    var mask_it = mask.iterator(.{});
                    while (mask_it.next()) |i| {
                        self.terminal.flags.dirty.palette = true;
                        self.terminal.colors.palette.reset(@intCast(i));
                    }
                    mask.* = .initEmpty();
                },

                .query => |target| {
                    if (self.color_query_fn) |func| {
                        try func(self.color_query_ctx, target);
                    }
                },
                .reset_special => {},
            }
        }
    }

    fn kittyColorOperation(
        self: *Handler,
        request: kitty_color.OSC,
    ) !void {
        for (request.list.items) |item| {
            switch (item) {
                .set => |v| switch (v.key) {
                    .palette => |palette| {
                        self.terminal.flags.dirty.palette = true;
                        self.terminal.colors.palette.set(palette, v.color);
                    },
                    .special => |special| switch (special) {
                        .foreground => self.terminal.colors.foreground.set(v.color),
                        .background => self.terminal.colors.background.set(v.color),
                        .cursor => self.terminal.colors.cursor.set(v.color),
                        else => {},
                    },
                },
                .reset => |key| switch (key) {
                    .palette => |palette| {
                        self.terminal.flags.dirty.palette = true;
                        self.terminal.colors.palette.reset(palette);
                    },
                    .special => |special| switch (special) {
                        .foreground => self.terminal.colors.foreground.reset(),
                        .background => self.terminal.colors.background.reset(),
                        .cursor => self.terminal.colors.cursor.reset(),
                        else => {},
                    },
                },
                .query => |key| {
                    if (self.color_query_fn) |func| {
                        const target: osc_color.Target = switch (key) {
                            .palette => |p| .{ .palette = p },
                            .special => |s| .{ .dynamic = switch (s) {
                                .foreground => .foreground,
                                .background => .background,
                                .cursor => .cursor,
                                else => continue,
                            } },
                        };
                        try func(self.color_query_ctx, target);
                    }
                },
            }
        }
    }

    fn requestMode(self: *Handler, mode: modes.Mode) !void {
        const tag: modes.ModeTag = @bitCast(@intFromEnum(mode));

        const code: u8 = switch (mode) {
            // Modes handled by ghostty Terminal
            .cursor_keys,
            .cursor_visible,
            .keypad_keys,
            .origin,
            .enable_left_and_right_margin,
            .alt_screen_legacy,
            .alt_screen,
            .alt_screen_save_cursor_clear_enter,
            .save_cursor,
            .@"132_column",
            .reverse_colors,
            .grapheme_cluster,
            => if (self.terminal.modes.get(mode)) 1 else 2,

            // Modes we provide support for
            .synchronized_output,
            .in_band_size_reports,
            .mouse_event_x10,
            .mouse_event_normal,
            .mouse_event_button,
            .mouse_event_any,
            .mouse_format_utf8,
            .mouse_format_sgr,
            .mouse_format_urxvt,
            .mouse_format_sgr_pixels,
            => if (self.terminal.modes.get(mode)) 1 else 2,

            // Not recognized
            else => 0,
        };

        var buf: [32]u8 = undefined;
        const resp = std.fmt.bufPrint(
            &buf,
            "\x1B[{s}{};{}$y",
            .{
                if (tag.ansi) "" else "?",
                tag.value,
                code,
            },
        ) catch return;
        try self.write(resp);
    }

    fn requestModeUnknown(self: *Handler, mode_raw: u16, ansi: bool) !void {
        var buf: [32]u8 = undefined;
        const resp = std.fmt.bufPrint(
            &buf,
            "\x1B[{s}{};0$y",
            .{
                if (ansi) "" else "?",
                mode_raw,
            },
        ) catch return;
        try self.write(resp);
    }
};

pub const Stream = ghostty_vt.Stream(Handler);
