//! Roff/man page output renderer.

const std = @import("std");
const parser = @import("parser.zig");
const Node = parser.Node;
const Span = parser.Span;
const Document = parser.Document;

/// Render a Document to roff format.
/// All allocations use the provided allocator (typically an arena).
pub fn render(allocator: std.mem.Allocator, doc: Document, name: []const u8, section: []const u8) ![]const u8 {
    var buffer: std.ArrayListUnmanaged(u8) = .empty;
    const writer = buffer.writer(allocator);

    // Header
    var upper_name: [64]u8 = undefined;
    const name_upper = std.ascii.upperString(&upper_name, name);
    try writer.print(".TH \"{s}\" \"{s}\"\n", .{ name_upper, section });

    for (doc.nodes) |node| {
        try renderNode(allocator, writer, node, &upper_name);
    }

    return buffer.toOwnedSlice(allocator);
}

fn renderNode(allocator: std.mem.Allocator, writer: anytype, node: Node, upper_buf: *[64]u8) !void {
    switch (node) {
        .heading => |h| {
            if (h.level == 1) {
                const upper = std.ascii.upperString(upper_buf, h.text);
                try writer.print(".SH \"{s}\"\n", .{upper});
            } else {
                try writer.print(".SS \"{s}\"\n", .{h.text});
            }
        },
        .paragraph => |spans| {
            try writer.writeAll(".PP\n");
            try renderSpans(allocator, writer, spans);
            try writer.writeAll("\n");
        },
        .code_block => |cb| {
            try writer.writeAll(".PP\n.nf\n");
            try escapeAndWrite(writer, cb.content);
            try writer.writeAll("\n.fi\n");
        },
        .bullet_list => |items| {
            for (items) |item| {
                try writer.writeAll(".IP \\(bu 2\n");
                try renderSpans(allocator, writer, item);
                try writer.writeAll("\n");
            }
        },
        .definition => |def| {
            try writer.writeAll(".TP\n");
            try renderSpans(allocator, writer, def.term);
            try writer.writeAll("\n");
            try renderSpans(allocator, writer, def.description);
            try writer.writeAll("\n");
        },
    }
}

fn renderSpans(allocator: std.mem.Allocator, writer: anytype, spans: []const Span) !void {
    _ = allocator;
    for (spans) |span| {
        switch (span) {
            .text => |t| try escapeAndWrite(writer, t),
            .bold => |t| {
                try writer.writeAll("\\fB");
                try escapeAndWrite(writer, t);
                try writer.writeAll("\\fR");
            },
            .italic => |t| {
                try writer.writeAll("\\fI");
                try escapeAndWrite(writer, t);
                try writer.writeAll("\\fR");
            },
            .code => |t| {
                try writer.writeAll("\\fB");
                try escapeAndWrite(writer, t);
                try writer.writeAll("\\fR");
            },
            .link => |l| {
                try writer.writeAll("\n.UR ");
                try writer.writeAll(l.url);
                try writer.writeAll("\n");
                try escapeAndWrite(writer, l.text);
                try writer.writeAll("\n.UE\n");
            },
        }
    }
}

fn escapeAndWrite(writer: anytype, text: []const u8) !void {
    for (text) |c| {
        switch (c) {
            '\\' => try writer.writeAll("\\\\"),
            '-' => try writer.writeAll("\\-"),
            else => try writer.writeByte(c),
        }
    }
}
