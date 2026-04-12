//! Minimal Markdown parser for man page generation.
//!
//! Supports: headings, paragraphs, bold, italic, code, code blocks,
//! bullet lists, definition lists, and links.
//!
//! Usage:
//!   var arena = std.heap.ArenaAllocator.init(std.heap.page_allocator);
//!   defer arena.deinit();
//!   const doc = try parse(arena.allocator(), source);

const std = @import("std");
const Allocator = std.mem.Allocator;

pub const Node = union(enum) {
    heading: Heading,
    paragraph: []const Span,
    code_block: CodeBlock,
    bullet_list: []const []const Span,
    definition: Definition,

    pub const Heading = struct {
        level: u8, // 1 or 2
        text: []const u8,
    };

    pub const CodeBlock = struct {
        language: ?[]const u8,
        content: []const u8,
    };

    pub const Definition = struct {
        term: []const Span,
        description: []const Span,
    };
};

pub const Span = union(enum) {
    text: []const u8,
    bold: []const u8,
    italic: []const u8,
    code: []const u8,
    link: Link,

    pub const Link = struct {
        text: []const u8,
        url: []const u8,
    };
};

pub const Document = struct {
    nodes: []const Node,
};

/// Parse markdown source into a Document.
/// All allocations use the provided allocator (typically an arena).
/// Caller owns the allocator lifetime.
pub fn parse(allocator: Allocator, source: []const u8) !Document {
    var p = Parser{ .allocator = allocator, .source = source, .pos = 0 };
    return p.parse();
}

const Parser = struct {
    allocator: Allocator,
    source: []const u8,
    pos: usize,

    fn parse(self: *Parser) !Document {
        var nodes: std.ArrayListUnmanaged(Node) = .empty;

        while (!self.isAtEnd()) {
            self.skipBlankLines();
            if (self.isAtEnd()) break;

            if (try self.parseNode()) |node| {
                try nodes.append(self.allocator, node);
            }
        }

        return .{ .nodes = try nodes.toOwnedSlice(self.allocator) };
    }

    fn parseNode(self: *Parser) !?Node {
        const line = self.peekLine();

        // Heading: # or ##
        if (std.mem.startsWith(u8, line, "## ")) {
            _ = self.consumeLine();
            return .{ .heading = .{ .level = 2, .text = line[3..] } };
        }
        if (std.mem.startsWith(u8, line, "# ")) {
            _ = self.consumeLine();
            return .{ .heading = .{ .level = 1, .text = line[2..] } };
        }

        // Code block: ```
        if (std.mem.startsWith(u8, line, "```")) {
            return self.parseCodeBlock();
        }

        // Bullet list: - item
        if (std.mem.startsWith(u8, line, "- ")) {
            return try self.parseBulletList();
        }

        // Definition list: next line starts with :
        if (self.isDefinitionStart()) {
            return try self.parseDefinition();
        }

        // Default: paragraph
        return try self.parseParagraph();
    }

    fn parseCodeBlock(self: *Parser) Node {
        const opening = self.consumeLine();
        const language = if (opening.len > 3) opening[3..] else null;

        const start = self.pos;
        while (!self.isAtEnd()) {
            const line = self.peekLine();
            if (std.mem.startsWith(u8, line, "```")) {
                const content = if (self.pos > start)
                    self.source[start .. self.pos - 1] // exclude trailing newline
                else
                    "";
                _ = self.consumeLine(); // consume closing ```
                return .{ .code_block = .{ .language = language, .content = content } };
            }
            _ = self.consumeLine();
        }

        // Unclosed code block - treat rest as content
        return .{ .code_block = .{ .language = language, .content = self.source[start..] } };
    }

    fn parseBulletList(self: *Parser) !Node {
        var items: std.ArrayListUnmanaged([]const Span) = .empty;

        while (!self.isAtEnd()) {
            const line = self.peekLine();
            if (!std.mem.startsWith(u8, line, "- ")) break;

            _ = self.consumeLine();
            const spans = try self.parseInline(line[2..]);
            try items.append(self.allocator, spans);
        }

        return .{ .bullet_list = try items.toOwnedSlice(self.allocator) };
    }

    fn parseDefinition(self: *Parser) !Node {
        const term_line = self.consumeLine();
        const term = try self.parseInline(term_line);

        // Consume : line and any continuation lines (indented with spaces)
        const desc_line = self.consumeLine();
        const desc_text = std.mem.trimLeft(u8, desc_line[1..], " \t"); // skip ':' and whitespace

        var full_desc: std.ArrayListUnmanaged(u8) = .empty;
        try full_desc.appendSlice(self.allocator, desc_text);

        // Continue consuming indented lines
        while (!self.isAtEnd()) {
            const next_line = self.peekLine();
            // Stop at blank line or non-indented line
            if (next_line.len == 0) break;
            if (!std.mem.startsWith(u8, next_line, "    ")) break;

            _ = self.consumeLine();
            try full_desc.append(self.allocator, ' ');
            try full_desc.appendSlice(self.allocator, std.mem.trimLeft(u8, next_line, " \t"));
        }

        const description = try self.parseInline(full_desc.items);

        return .{ .definition = .{ .term = term, .description = description } };
    }

    fn parseParagraph(self: *Parser) !Node {
        var text: std.ArrayListUnmanaged(u8) = .empty;

        while (!self.isAtEnd()) {
            const line = self.peekLine();
            // Stop at blank line or block-level construct
            if (line.len == 0 or
                std.mem.startsWith(u8, line, "# ") or
                std.mem.startsWith(u8, line, "## ") or
                std.mem.startsWith(u8, line, "```") or
                std.mem.startsWith(u8, line, "- "))
            {
                break;
            }
            if (self.isDefinitionStart()) break;

            if (text.items.len > 0) try text.append(self.allocator, ' ');
            try text.appendSlice(self.allocator, self.consumeLine());
        }

        const spans = try self.parseInline(text.items);
        return .{ .paragraph = spans };
    }

    fn parseInline(self: *Parser, text: []const u8) ![]const Span {
        var spans: std.ArrayListUnmanaged(Span) = .empty;

        var i: usize = 0;
        var text_start: usize = 0;

        while (i < text.len) {
            // Bold: **text**
            if (i + 1 < text.len and std.mem.eql(u8, text[i .. i + 2], "**")) {
                if (i > text_start) {
                    try spans.append(self.allocator, .{ .text = text[text_start..i] });
                }
                const end = std.mem.indexOf(u8, text[i + 2 ..], "**") orelse {
                    i += 1;
                    continue;
                };
                try spans.append(self.allocator, .{ .bold = text[i + 2 .. i + 2 + end] });
                i = i + 4 + end;
                text_start = i;
                continue;
            }

            // Italic: *text* (but not **)
            if (text[i] == '*' and (i + 1 >= text.len or text[i + 1] != '*')) {
                if (i > text_start) {
                    try spans.append(self.allocator, .{ .text = text[text_start..i] });
                }
                const end = std.mem.indexOf(u8, text[i + 1 ..], "*") orelse {
                    i += 1;
                    continue;
                };
                try spans.append(self.allocator, .{ .italic = text[i + 1 .. i + 1 + end] });
                i = i + 2 + end;
                text_start = i;
                continue;
            }

            // Code: `text`
            if (text[i] == '`') {
                if (i > text_start) {
                    try spans.append(self.allocator, .{ .text = text[text_start..i] });
                }
                const end = std.mem.indexOf(u8, text[i + 1 ..], "`") orelse {
                    i += 1;
                    continue;
                };
                try spans.append(self.allocator, .{ .code = text[i + 1 .. i + 1 + end] });
                i = i + 2 + end;
                text_start = i;
                continue;
            }

            // Link: [text](url)
            if (text[i] == '[') {
                if (parseLink(text, i)) |result| {
                    if (i > text_start) {
                        try spans.append(self.allocator, .{ .text = text[text_start..i] });
                    }
                    try spans.append(self.allocator, .{ .link = result.link });
                    i = result.end;
                    text_start = i;
                    continue;
                }
            }

            i += 1;
        }

        if (text_start < text.len) {
            try spans.append(self.allocator, .{ .text = text[text_start..] });
        }

        return try spans.toOwnedSlice(self.allocator);
    }

    const LinkResult = struct { link: Span.Link, end: usize };

    fn parseLink(text: []const u8, start: usize) ?LinkResult {
        const close_bracket = std.mem.indexOf(u8, text[start + 1 ..], "]") orelse return null;
        const text_end = start + 1 + close_bracket;

        if (text_end + 1 >= text.len or text[text_end + 1] != '(') return null;

        const close_paren = std.mem.indexOf(u8, text[text_end + 2 ..], ")") orelse return null;
        const url_end = text_end + 2 + close_paren;

        return .{
            .link = .{
                .text = text[start + 1 .. text_end],
                .url = text[text_end + 2 .. url_end],
            },
            .end = url_end + 1,
        };
    }

    // --- Line utilities ---

    fn peekLine(self: *Parser) []const u8 {
        const end = std.mem.indexOf(u8, self.source[self.pos..], "\n") orelse self.source.len - self.pos;
        return self.source[self.pos .. self.pos + end];
    }

    fn consumeLine(self: *Parser) []const u8 {
        const line = self.peekLine();
        self.pos += line.len;
        if (self.pos < self.source.len and self.source[self.pos] == '\n') {
            self.pos += 1;
        }
        return line;
    }

    fn skipBlankLines(self: *Parser) void {
        while (!self.isAtEnd() and self.peekLine().len == 0) {
            _ = self.consumeLine();
        }
    }

    fn isAtEnd(self: *Parser) bool {
        return self.pos >= self.source.len;
    }

    fn isDefinitionStart(self: *Parser) bool {
        // Look for a non-blank line followed by a line starting with ':'
        const line = self.peekLine();
        if (line.len == 0) return false;
        if (std.mem.startsWith(u8, line, "- ")) return false;

        // Find next line
        const next_pos = self.pos + line.len + 1;
        if (next_pos >= self.source.len) return false;

        const rest = self.source[next_pos..];
        const next_end = std.mem.indexOf(u8, rest, "\n") orelse rest.len;
        const next_line = rest[0..next_end];

        return std.mem.startsWith(u8, next_line, ":");
    }
};

// --- Tests ---

test "parse h1 heading" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();

    const doc = try parse(arena.allocator(), "# NAME\n");
    try std.testing.expectEqual(1, doc.nodes.len);
    try std.testing.expectEqual(1, doc.nodes[0].heading.level);
    try std.testing.expectEqualStrings("NAME", doc.nodes[0].heading.text);
}

test "parse h2 heading" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();

    const doc = try parse(arena.allocator(), "## Subsection\n");
    try std.testing.expectEqual(1, doc.nodes.len);
    try std.testing.expectEqual(2, doc.nodes[0].heading.level);
    try std.testing.expectEqualStrings("Subsection", doc.nodes[0].heading.text);
}

test "parse paragraph" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();

    const doc = try parse(arena.allocator(), "Hello world.\n");
    try std.testing.expectEqual(1, doc.nodes.len);
    const spans = doc.nodes[0].paragraph;
    try std.testing.expectEqual(1, spans.len);
    try std.testing.expectEqualStrings("Hello world.", spans[0].text);
}

test "parse bold" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();

    const doc = try parse(arena.allocator(), "This is **bold** text.\n");
    const spans = doc.nodes[0].paragraph;
    try std.testing.expectEqual(3, spans.len);
    try std.testing.expectEqualStrings("This is ", spans[0].text);
    try std.testing.expectEqualStrings("bold", spans[1].bold);
    try std.testing.expectEqualStrings(" text.", spans[2].text);
}

test "parse italic" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();

    const doc = try parse(arena.allocator(), "This is *italic* text.\n");
    const spans = doc.nodes[0].paragraph;
    try std.testing.expectEqual(3, spans.len);
    try std.testing.expectEqualStrings("This is ", spans[0].text);
    try std.testing.expectEqualStrings("italic", spans[1].italic);
    try std.testing.expectEqualStrings(" text.", spans[2].text);
}

test "parse inline code" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();

    const doc = try parse(arena.allocator(), "Run `prise serve` now.\n");
    const spans = doc.nodes[0].paragraph;
    try std.testing.expectEqual(3, spans.len);
    try std.testing.expectEqualStrings("Run ", spans[0].text);
    try std.testing.expectEqualStrings("prise serve", spans[1].code);
    try std.testing.expectEqualStrings(" now.", spans[2].text);
}

test "parse link" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();

    const doc = try parse(arena.allocator(), "See [the docs](https://example.com) here.\n");
    const spans = doc.nodes[0].paragraph;
    try std.testing.expectEqual(3, spans.len);
    try std.testing.expectEqualStrings("See ", spans[0].text);
    try std.testing.expectEqualStrings("the docs", spans[1].link.text);
    try std.testing.expectEqualStrings("https://example.com", spans[1].link.url);
    try std.testing.expectEqualStrings(" here.", spans[2].text);
}

test "parse code block" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();

    const doc = try parse(arena.allocator(),
        \\```bash
        \\prise serve
        \\```
        \\
    );
    try std.testing.expectEqual(1, doc.nodes.len);
    const cb = doc.nodes[0].code_block;
    try std.testing.expectEqualStrings("bash", cb.language.?);
    try std.testing.expectEqualStrings("prise serve", cb.content);
}

test "parse bullet list" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();

    const doc = try parse(arena.allocator(),
        \\- First item
        \\- Second item
        \\- Third item
        \\
    );
    try std.testing.expectEqual(1, doc.nodes.len);
    const items = doc.nodes[0].bullet_list;
    try std.testing.expectEqual(3, items.len);
    try std.testing.expectEqualStrings("First item", items[0][0].text);
    try std.testing.expectEqualStrings("Second item", items[1][0].text);
    try std.testing.expectEqualStrings("Third item", items[2][0].text);
}

test "parse definition list" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();

    const doc = try parse(arena.allocator(),
        \\**-v**, **--verbose**
        \\:   Enable verbose output
        \\
    );
    try std.testing.expectEqual(1, doc.nodes.len);
    const def = doc.nodes[0].definition;
    try std.testing.expectEqualStrings("-v", def.term[0].bold);
    try std.testing.expectEqualStrings("Enable verbose output", def.description[0].text);
}

test "parse mixed inline formatting" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();

    const doc = try parse(arena.allocator(), "Use **bold** and *italic* and `code` together.\n");
    const spans = doc.nodes[0].paragraph;
    try std.testing.expectEqual(7, spans.len);
    try std.testing.expectEqualStrings("bold", spans[1].bold);
    try std.testing.expectEqualStrings("italic", spans[3].italic);
    try std.testing.expectEqualStrings("code", spans[5].code);
}

test "parse multiple paragraphs" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();

    const doc = try parse(arena.allocator(),
        \\First paragraph.
        \\
        \\Second paragraph.
        \\
    );
    try std.testing.expectEqual(2, doc.nodes.len);
    try std.testing.expectEqualStrings("First paragraph.", doc.nodes[0].paragraph[0].text);
    try std.testing.expectEqualStrings("Second paragraph.", doc.nodes[1].paragraph[0].text);
}

test "parse full document" {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();

    const doc = try parse(arena.allocator(),
        \\# NAME
        \\
        \\prise - terminal multiplexer
        \\
        \\## SYNOPSIS
        \\
        \\**prise** [*options*] [*command*]
        \\
        \\## OPTIONS
        \\
        \\**-h**, **--help**
        \\:   Show help message
        \\
    );
    try std.testing.expectEqual(6, doc.nodes.len);
    try std.testing.expectEqualStrings("NAME", doc.nodes[0].heading.text);
    try std.testing.expectEqual(@as(u8, 1), doc.nodes[0].heading.level);
    try std.testing.expectEqualStrings("SYNOPSIS", doc.nodes[2].heading.text);
    try std.testing.expectEqual(@as(u8, 2), doc.nodes[2].heading.level);
}
