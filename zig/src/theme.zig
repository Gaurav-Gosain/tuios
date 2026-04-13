//! Theme system for tuios. Provides color palettes for UI elements.
//! Embeds popular themes as comptime data.

const vaxis = @import("vaxis");

pub const Color = [3]u8;

pub const Theme = struct {
    name: []const u8,
    fg: Color,
    bg: Color,
    cursor: Color,
    // 16 ANSI colors
    black: Color,
    red: Color,
    green: Color,
    yellow: Color,
    blue: Color,
    purple: Color,
    cyan: Color,
    white: Color,
    bright_black: Color,
    bright_red: Color,
    bright_green: Color,
    bright_yellow: Color,
    bright_blue: Color,
    bright_purple: Color,
    bright_cyan: Color,
    bright_white: Color,

    // Semantic colors derived from theme
    pub fn borderFocused(self: Theme) vaxis.Color {
        return .{ .rgb = self.blue };
    }

    pub fn borderUnfocused(self: Theme) vaxis.Color {
        return .{ .rgb = self.bright_black };
    }

    pub fn statusBg(self: Theme) vaxis.Color {
        return .{ .rgb = self.bg };
    }

    pub fn statusFg(self: Theme) vaxis.Color {
        return .{ .rgb = self.fg };
    }

    pub fn prefixIndicator(self: Theme) vaxis.Color {
        return .{ .rgb = self.yellow };
    }

    pub fn wmModeIndicator(self: Theme) vaxis.Color {
        return .{ .rgb = self.blue };
    }

    pub fn accentColor(self: Theme) vaxis.Color {
        return .{ .rgb = self.cyan };
    }

    pub fn errorColor(self: Theme) vaxis.Color {
        return .{ .rgb = self.red };
    }

    pub fn dimColor(self: Theme) vaxis.Color {
        return .{ .rgb = self.bright_black };
    }
};

// ---- Embedded themes ----

pub const default_theme = catppuccin_mocha;

pub const catppuccin_mocha = Theme{
    .name = "catppuccin_mocha",
    .fg = .{ 0xcd, 0xd6, 0xf4 },
    .bg = .{ 0x1e, 0x1e, 0x2e },
    .cursor = .{ 0xf5, 0xe0, 0xdc },
    .black = .{ 0x45, 0x47, 0x5a },
    .red = .{ 0xf3, 0x8b, 0xa8 },
    .green = .{ 0xa6, 0xe3, 0xa1 },
    .yellow = .{ 0xf9, 0xe2, 0xaf },
    .blue = .{ 0x89, 0xb4, 0xfa },
    .purple = .{ 0xcb, 0xa6, 0xf7 },
    .cyan = .{ 0x94, 0xe2, 0xd5 },
    .white = .{ 0xba, 0xc2, 0xde },
    .bright_black = .{ 0x58, 0x5b, 0x70 },
    .bright_red = .{ 0xf3, 0x8b, 0xa8 },
    .bright_green = .{ 0xa6, 0xe3, 0xa1 },
    .bright_yellow = .{ 0xf9, 0xe2, 0xaf },
    .bright_blue = .{ 0x89, 0xb4, 0xfa },
    .bright_purple = .{ 0xcb, 0xa6, 0xf7 },
    .bright_cyan = .{ 0x94, 0xe2, 0xd5 },
    .bright_white = .{ 0xa6, 0xad, 0xc8 },
};

pub const dracula = Theme{
    .name = "dracula",
    .fg = .{ 0xf8, 0xf8, 0xf2 },
    .bg = .{ 0x28, 0x2a, 0x36 },
    .cursor = .{ 0xf8, 0xf8, 0xf2 },
    .black = .{ 0x21, 0x22, 0x2c },
    .red = .{ 0xff, 0x55, 0x55 },
    .green = .{ 0x50, 0xfa, 0x7b },
    .yellow = .{ 0xf1, 0xfa, 0x8c },
    .blue = .{ 0xbd, 0x93, 0xf9 },
    .purple = .{ 0xff, 0x79, 0xc6 },
    .cyan = .{ 0x8b, 0xe9, 0xfd },
    .white = .{ 0xf8, 0xf8, 0xf2 },
    .bright_black = .{ 0x62, 0x72, 0xa4 },
    .bright_red = .{ 0xff, 0x6e, 0x6e },
    .bright_green = .{ 0x69, 0xff, 0x94 },
    .bright_yellow = .{ 0xff, 0xff, 0xa5 },
    .bright_blue = .{ 0xd6, 0xac, 0xff },
    .bright_purple = .{ 0xff, 0x92, 0xdf },
    .bright_cyan = .{ 0xa4, 0xff, 0xff },
    .bright_white = .{ 0xff, 0xff, 0xff },
};

pub const nord = Theme{
    .name = "nord",
    .fg = .{ 0xd8, 0xde, 0xe9 },
    .bg = .{ 0x2e, 0x34, 0x40 },
    .cursor = .{ 0xd8, 0xde, 0xe9 },
    .black = .{ 0x3b, 0x42, 0x52 },
    .red = .{ 0xbf, 0x61, 0x6a },
    .green = .{ 0xa3, 0xbe, 0x8c },
    .yellow = .{ 0xeb, 0xcb, 0x8b },
    .blue = .{ 0x81, 0xa1, 0xc1 },
    .purple = .{ 0xb4, 0x8e, 0xad },
    .cyan = .{ 0x88, 0xc0, 0xd0 },
    .white = .{ 0xe5, 0xe9, 0xf0 },
    .bright_black = .{ 0x4c, 0x56, 0x6a },
    .bright_red = .{ 0xbf, 0x61, 0x6a },
    .bright_green = .{ 0xa3, 0xbe, 0x8c },
    .bright_yellow = .{ 0xeb, 0xcb, 0x8b },
    .bright_blue = .{ 0x81, 0xa1, 0xc1 },
    .bright_purple = .{ 0xb4, 0x8e, 0xad },
    .bright_cyan = .{ 0x8f, 0xbc, 0xbb },
    .bright_white = .{ 0xec, 0xef, 0xf4 },
};

pub const gruvbox = Theme{
    .name = "gruvbox",
    .fg = .{ 0xeb, 0xdb, 0xb2 },
    .bg = .{ 0x28, 0x28, 0x28 },
    .cursor = .{ 0xeb, 0xdb, 0xb2 },
    .black = .{ 0x28, 0x28, 0x28 },
    .red = .{ 0xcc, 0x24, 0x1d },
    .green = .{ 0x98, 0x97, 0x1a },
    .yellow = .{ 0xd7, 0x99, 0x21 },
    .blue = .{ 0x45, 0x85, 0x88 },
    .purple = .{ 0xb1, 0x62, 0x86 },
    .cyan = .{ 0x68, 0x9d, 0x6a },
    .white = .{ 0xa8, 0x99, 0x84 },
    .bright_black = .{ 0x92, 0x83, 0x74 },
    .bright_red = .{ 0xfb, 0x49, 0x34 },
    .bright_green = .{ 0xb8, 0xbb, 0x26 },
    .bright_yellow = .{ 0xfa, 0xbd, 0x2f },
    .bright_blue = .{ 0x83, 0xa5, 0x98 },
    .bright_purple = .{ 0xd3, 0x86, 0x9b },
    .bright_cyan = .{ 0x8e, 0xc0, 0x7c },
    .bright_white = .{ 0xeb, 0xdb, 0xb2 },
};

pub const tokyo_night = Theme{
    .name = "tokyo_night",
    .fg = .{ 0xc0, 0xca, 0xf5 },
    .bg = .{ 0x1a, 0x1b, 0x26 },
    .cursor = .{ 0xc0, 0xca, 0xf5 },
    .black = .{ 0x15, 0x16, 0x1e },
    .red = .{ 0xf7, 0x76, 0x8e },
    .green = .{ 0x9e, 0xce, 0x6a },
    .yellow = .{ 0xe0, 0xaf, 0x68 },
    .blue = .{ 0x7a, 0xa2, 0xf7 },
    .purple = .{ 0xbb, 0x9a, 0xf7 },
    .cyan = .{ 0x7d, 0xcf, 0xff },
    .white = .{ 0xa9, 0xb1, 0xd6 },
    .bright_black = .{ 0x41, 0x48, 0x68 },
    .bright_red = .{ 0xf7, 0x76, 0x8e },
    .bright_green = .{ 0x9e, 0xce, 0x6a },
    .bright_yellow = .{ 0xe0, 0xaf, 0x68 },
    .bright_blue = .{ 0x7a, 0xa2, 0xf7 },
    .bright_purple = .{ 0xbb, 0x9a, 0xf7 },
    .bright_cyan = .{ 0x7d, 0xcf, 0xff },
    .bright_white = .{ 0xc0, 0xca, 0xf5 },
};

// ---- Theme lookup ----

pub const all_themes = [_]*const Theme{
    &catppuccin_mocha,
    &dracula,
    &nord,
    &gruvbox,
    &tokyo_night,
};

pub fn getTheme(name: []const u8) ?*const Theme {
    for (all_themes) |t| {
        if (std.mem.eql(u8, t.name, name)) return t;
    }
    return null;
}

const std = @import("std");
