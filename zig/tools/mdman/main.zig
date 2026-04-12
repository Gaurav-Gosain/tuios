//! mdman - Markdown to man page (and HTML) converter

const std = @import("std");
const parser = @import("parser.zig");
const roff = @import("roff.zig");
const html = @import("html.zig");

pub fn main() !void {
    var gpa: std.heap.GeneralPurposeAllocator(.{}) = .init;
    defer _ = gpa.deinit();

    var arena = std.heap.ArenaAllocator.init(gpa.allocator());
    defer arena.deinit();
    const allocator = arena.allocator();

    const args = try std.process.argsAlloc(allocator);

    if (args.len < 2) {
        try printUsage();
        std.process.exit(1);
    }

    var input_file: ?[]const u8 = null;
    var output_format: enum { roff, html } = .roff;
    var name: []const u8 = "prise";
    var section: []const u8 = "1";
    var html_fragment: bool = false;

    var i: usize = 1;
    while (i < args.len) : (i += 1) {
        const arg = args[i];
        if (std.mem.eql(u8, arg, "-h") or std.mem.eql(u8, arg, "--help")) {
            try printUsage();
            return;
        } else if (std.mem.eql(u8, arg, "--html")) {
            output_format = .html;
        } else if (std.mem.eql(u8, arg, "--html-fragment")) {
            output_format = .html;
            html_fragment = true;
        } else if (std.mem.eql(u8, arg, "--roff")) {
            output_format = .roff;
        } else if (std.mem.eql(u8, arg, "-n") or std.mem.eql(u8, arg, "--name")) {
            i += 1;
            if (i >= args.len) {
                std.debug.print("error: --name requires an argument\n", .{});
                std.process.exit(1);
            }
            name = args[i];
        } else if (std.mem.eql(u8, arg, "-s") or std.mem.eql(u8, arg, "--section")) {
            i += 1;
            if (i >= args.len) {
                std.debug.print("error: --section requires an argument\n", .{});
                std.process.exit(1);
            }
            section = args[i];
        } else if (!std.mem.startsWith(u8, arg, "-")) {
            input_file = arg;
        } else {
            std.debug.print("error: unknown option: {s}\n", .{arg});
            std.process.exit(1);
        }
    }

    const file_path = input_file orelse {
        std.debug.print("error: no input file specified\n", .{});
        std.process.exit(1);
    };

    const source = try std.fs.cwd().readFileAlloc(allocator, file_path, 1024 * 1024);
    const doc = try parser.parse(allocator, source);

    const output = switch (output_format) {
        .roff => try roff.render(allocator, doc, name, section),
        .html => try html.render(allocator, doc, name, .{ .fragment = html_fragment }),
    };

    var buf: [4096]u8 = undefined;
    var stdout = std.fs.File.stdout().writer(&buf);
    defer stdout.interface.flush() catch {};
    try stdout.interface.writeAll(output);
}

fn printUsage() !void {
    var buf: [4096]u8 = undefined;
    var stderr = std.fs.File.stderr().writer(&buf);
    defer stderr.interface.flush() catch {};
    try stderr.interface.writeAll(
        \\Usage: mdman [OPTIONS] <FILE>
        \\
        \\Convert Markdown to man page (roff) or HTML.
        \\
        \\Options:
        \\  --roff           Output roff format (default)
        \\  --html           Output full HTML page
        \\  --html-fragment  Output HTML fragment (no wrapper)
        \\  -n, --name       Man page name (default: prise)
        \\  -s, --section    Man page section (default: 1)
        \\  -h, --help       Show this help
        \\
    );
}

test {
    _ = @import("parser.zig");
}
