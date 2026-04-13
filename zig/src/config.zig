//! Configuration system for tuios.
//! Loads user preferences from ~/.config/tuios/config.json.
//! Falls back to sensible defaults when no config file exists.

const std = @import("std");
const theme_mod = @import("theme.zig");

const log = std.log.scoped(.config);

pub const BorderStyle = enum {
    rounded,
    single,
    double,
    hidden,
    ascii,
};

pub const DockPosition = enum {
    bottom,
    top,
    hidden,
};

pub const Config = struct {
    leader_key: []const u8 = "<C-b>",
    border_style: BorderStyle = .rounded,
    border_focused_color: ?[3]u8 = null, // null = use theme
    border_unfocused_color: ?[3]u8 = null, // null = use theme
    theme_name: []const u8 = "",
    scrollback_lines: u32 = 10000,
    preferred_shell: []const u8 = "",
    gap: u8 = 0,
    dock_position: DockPosition = .bottom,
    shared_borders: bool = true,
    confirm_quit: bool = false,
    max_fps: u8 = 60,

    /// Load config from default path. Returns defaults if file not found.
    pub fn load(allocator: std.mem.Allocator) Config {
        const home = std.posix.getenv("HOME") orelse return .{};

        var path_buf: [std.fs.max_path_bytes]u8 = undefined;
        const path = std.fmt.bufPrint(&path_buf, "{s}/.config/tuios/config.json", .{home}) catch return .{};

        const file = std.fs.openFileAbsolute(path, .{}) catch return .{};
        defer file.close();

        var buf: [4096]u8 = undefined;
        const len = file.readAll(&buf) catch return .{};
        if (len == 0) return .{};

        return parseJson(allocator, buf[0..len]) catch |err| {
            log.warn("Failed to parse config: {}", .{err});
            return .{};
        };
    }

    fn parseJson(allocator: std.mem.Allocator, data: []const u8) !Config {
        var config: Config = .{};

        const parsed = try std.json.parseFromSlice(std.json.Value, allocator, data, .{});
        defer parsed.deinit();

        const root = parsed.value;
        if (root != .object) return config;

        if (root.object.get("leader_key")) |v| {
            if (v == .string) config.leader_key = v.string;
        }
        if (root.object.get("theme")) |v| {
            if (v == .string) config.theme_name = v.string;
        }
        if (root.object.get("border_style")) |v| {
            if (v == .string) {
                if (std.mem.eql(u8, v.string, "rounded")) config.border_style = .rounded
                else if (std.mem.eql(u8, v.string, "single")) config.border_style = .single
                else if (std.mem.eql(u8, v.string, "double")) config.border_style = .double
                else if (std.mem.eql(u8, v.string, "hidden")) config.border_style = .hidden
                else if (std.mem.eql(u8, v.string, "ascii")) config.border_style = .ascii;
            }
        }
        if (root.object.get("dock_position")) |v| {
            if (v == .string) {
                if (std.mem.eql(u8, v.string, "top")) config.dock_position = .top
                else if (std.mem.eql(u8, v.string, "hidden")) config.dock_position = .hidden;
            }
        }
        if (root.object.get("scrollback_lines")) |v| {
            if (v == .integer) config.scrollback_lines = @intCast(@max(100, @min(1000000, v.integer)));
        }
        if (root.object.get("gap")) |v| {
            if (v == .integer) config.gap = @intCast(@max(0, @min(10, v.integer)));
        }
        if (root.object.get("max_fps")) |v| {
            if (v == .integer) config.max_fps = @intCast(@max(10, @min(120, v.integer)));
        }
        if (root.object.get("shared_borders")) |v| {
            if (v == .bool) config.shared_borders = v.bool;
        }
        if (root.object.get("confirm_quit")) |v| {
            if (v == .bool) config.confirm_quit = v.bool;
        }

        return config;
    }

    /// Resolve theme from config. Returns default theme if not found.
    pub fn resolveTheme(self: Config) theme_mod.Theme {
        if (self.theme_name.len > 0) {
            if (theme_mod.getTheme(self.theme_name)) |t| {
                return t.*;
            }
        }
        return theme_mod.default_theme;
    }
};
