//! Runtime key matching using compiled trie.
//!
//! Tracks current position in trie. On key press:
//! - If leaf: execute action and reset to root
//! - If branch: descend into subtree
//! - No match: reset to root

const std = @import("std");
const key_string = @import("key_string.zig");
const keybind_compiler = @import("keybind_compiler.zig");
const action_mod = @import("action.zig");

const Key = key_string.Key;
const TrieNode = keybind_compiler.TrieNode;
const Trie = keybind_compiler.Trie;
const Action = action_mod.Action;

pub const MatchResult = union(enum) {
    action: struct {
        action: Action,
        key_string: ?[]const u8,
    },
    pending,
    none,
};

pub const Matcher = struct {
    trie: *const Trie,
    current: *const TrieNode,

    pub fn init(trie: *const Trie) Matcher {
        return .{
            .trie = trie,
            .current = &trie.root,
        };
    }

    pub fn handleKey(self: *Matcher, key: Key) MatchResult {
        var buf: [64]u8 = undefined;
        const key_id = keyToId(&buf, key);

        if (self.current.children.get(key_id)) |child| {
            if (child.isLeaf()) {
                self.reset();
                return .{ .action = .{
                    .action = child.action.?,
                    .key_string = child.key_string,
                } };
            }
            if (child.isBranch()) {
                self.current = child;
                return .pending;
            }
        }

        self.reset();
        return .none;
    }

    pub fn isPending(self: *const Matcher) bool {
        return self.current != &self.trie.root;
    }

    pub fn reset(self: *Matcher) void {
        self.current = &self.trie.root;
    }
};

fn keyToId(buf: *[64]u8, key: Key) []const u8 {
    var len: usize = 0;

    if (key.ctrl) {
        buf[len] = 'C';
        len += 1;
    }
    if (key.alt) {
        buf[len] = 'A';
        len += 1;
    }
    if (key.shift) {
        buf[len] = 'S';
        len += 1;
    }
    if (key.super) {
        buf[len] = 'D';
        len += 1;
    }
    buf[len] = ':';
    len += 1;

    const key_slice = key.key;
    @memcpy(buf[len .. len + key_slice.len], key_slice);
    len += key_slice.len;

    return buf[0..len];
}

test "match single key binding" {
    var compiler = keybind_compiler.Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]keybind_compiler.Keybind{
        .{ .key_string = "<D-k>", .action = .split_horizontal },
    };

    var trie = try compiler.compile(&bindings);
    defer trie.deinit();

    var matcher = Matcher.init(&trie);

    const result = matcher.handleKey(.{ .key = "k", .super = true });
    try std.testing.expect(result == .action);
    try std.testing.expectEqual(Action.split_horizontal, result.action.action);
    try std.testing.expect(!matcher.isPending());
}

test "match key sequence" {
    var compiler = keybind_compiler.Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]keybind_compiler.Keybind{
        .{ .key_string = "<D-k>v", .action = .split_horizontal },
    };

    var trie = try compiler.compile(&bindings);
    defer trie.deinit();

    var matcher = Matcher.init(&trie);

    const result1 = matcher.handleKey(.{ .key = "k", .super = true });
    try std.testing.expect(result1 == .pending);
    try std.testing.expect(matcher.isPending());

    const result2 = matcher.handleKey(.{ .key = "v" });
    try std.testing.expect(result2 == .action);
    try std.testing.expectEqual(Action.split_horizontal, result2.action.action);
    try std.testing.expect(!matcher.isPending());
}

test "no match resets to root" {
    var compiler = keybind_compiler.Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]keybind_compiler.Keybind{
        .{ .key_string = "<D-k>v", .action = .split_horizontal },
    };

    var trie = try compiler.compile(&bindings);
    defer trie.deinit();

    var matcher = Matcher.init(&trie);

    const result1 = matcher.handleKey(.{ .key = "k", .super = true });
    try std.testing.expect(result1 == .pending);

    const result2 = matcher.handleKey(.{ .key = "x" });
    try std.testing.expect(result2 == .none);
    try std.testing.expect(!matcher.isPending());
}

test "manual reset" {
    var compiler = keybind_compiler.Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]keybind_compiler.Keybind{
        .{ .key_string = "<D-k>v", .action = .split_horizontal },
    };

    var trie = try compiler.compile(&bindings);
    defer trie.deinit();

    var matcher = Matcher.init(&trie);

    _ = matcher.handleKey(.{ .key = "k", .super = true });
    try std.testing.expect(matcher.isPending());

    matcher.reset();
    try std.testing.expect(!matcher.isPending());
}

test "multiple bindings same prefix" {
    var compiler = keybind_compiler.Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]keybind_compiler.Keybind{
        .{ .key_string = "<D-k>v", .action = .split_horizontal },
        .{ .key_string = "<D-k>s", .action = .split_vertical },
        .{ .key_string = "<D-k>h", .action = .focus_left },
    };

    var trie = try compiler.compile(&bindings);
    defer trie.deinit();

    var matcher = Matcher.init(&trie);

    _ = matcher.handleKey(.{ .key = "k", .super = true });
    const result = matcher.handleKey(.{ .key = "s" });
    try std.testing.expect(result == .action);
    try std.testing.expectEqual(Action.split_vertical, result.action.action);
}

test "key string preserved in result" {
    var compiler = keybind_compiler.Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]keybind_compiler.Keybind{
        .{ .key_string = "<D-k>v", .action = .split_horizontal },
    };

    var trie = try compiler.compile(&bindings);
    defer trie.deinit();

    var matcher = Matcher.init(&trie);

    _ = matcher.handleKey(.{ .key = "k", .super = true });
    const result = matcher.handleKey(.{ .key = "v" });
    try std.testing.expect(result == .action);
    try std.testing.expectEqualStrings("<D-k>v", result.action.key_string.?);
}

test "uppercase alt bindings stay distinct from lowercase alt bindings" {
    var compiler = keybind_compiler.Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]keybind_compiler.Keybind{
        .{ .key_string = "<A-h>", .action = .focus_left },
        .{ .key_string = "<A-H>", .action = .focus_right },
    };

    var trie = try compiler.compile(&bindings);
    defer trie.deinit();

    var matcher = Matcher.init(&trie);

    const lower = matcher.handleKey(.{ .key = "h", .alt = true });
    try std.testing.expect(lower == .action);
    try std.testing.expectEqual(Action.focus_left, lower.action.action);

    const upper = matcher.handleKey(.{ .key = "H", .alt = true });
    try std.testing.expect(upper == .action);
    try std.testing.expectEqual(Action.focus_right, upper.action.action);
}
