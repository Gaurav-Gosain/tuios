//! Compile flat keybind definitions into a nested trie structure.
//!
//! Takes a list of (key_string, action) pairs and builds a trie where:
//! - Each node represents a key in the sequence
//! - Leaf nodes have an action attached
//! - Branch nodes have children for the next key in sequences
//!
//! The compiler validates that no key can be both a leaf and a branch
//! (e.g., binding both "<D-k>" and "<D-k>v" is a conflict).
//!
//! Leader expansion: The special key "<leader>" is replaced with the
//! configured leader key sequence before trie insertion.

const std = @import("std");
const key_string = @import("key_string.zig");
const action_mod = @import("action.zig");

const Key = key_string.Key;
const Action = action_mod.Action;

pub const CompileError = error{
    ConflictingBinding,
    InvalidKeyString,
    OutOfMemory,
};

pub const TrieNode = struct {
    children: std.StringHashMapUnmanaged(*TrieNode) = .empty,
    action: ?Action = null,
    key_string: ?[]const u8 = null,

    pub fn deinit(self: *TrieNode, allocator: std.mem.Allocator) void {
        var iter = self.children.iterator();
        while (iter.next()) |entry| {
            allocator.free(entry.key_ptr.*);
            entry.value_ptr.*.deinit(allocator);
            allocator.destroy(entry.value_ptr.*);
        }
        self.children.deinit(allocator);
        if (self.key_string) |ks| {
            allocator.free(ks);
        }
    }

    pub fn isLeaf(self: *const TrieNode) bool {
        return self.action != null;
    }

    pub fn isBranch(self: *const TrieNode) bool {
        return self.children.count() > 0;
    }
};

pub const Keybind = struct {
    key_string: []const u8,
    action: Action,
};

pub const Trie = struct {
    root: TrieNode,
    allocator: std.mem.Allocator,

    pub fn init(allocator: std.mem.Allocator) Trie {
        return .{
            .root = .{},
            .allocator = allocator,
        };
    }

    pub fn deinit(self: *Trie) void {
        self.root.deinit(self.allocator);
    }

    pub fn lookup(self: *const Trie, keys: []const Key) ?*const TrieNode {
        var node: *const TrieNode = &self.root;
        var buf: [64]u8 = undefined;
        for (keys) |key| {
            const key_id = keyToId(&buf, key);
            if (node.children.get(key_id)) |child| {
                node = child;
            } else {
                return null;
            }
        }
        return node;
    }
};

pub const Compiler = struct {
    allocator: std.mem.Allocator,
    leader: ?[]const Key,
    leader_owned: bool,

    pub fn init(allocator: std.mem.Allocator) Compiler {
        return .{
            .allocator = allocator,
            .leader = null,
            .leader_owned = false,
        };
    }

    pub fn deinit(self: *Compiler) void {
        if (self.leader_owned) {
            if (self.leader) |leader| {
                self.allocator.free(leader);
            }
        }
    }

    pub fn setLeader(self: *Compiler, leader_str: []const u8) !void {
        if (self.leader_owned) {
            if (self.leader) |old| {
                self.allocator.free(old);
            }
        }
        self.leader = try key_string.parseKeyString(self.allocator, leader_str);
        self.leader_owned = true;
    }

    pub fn setLeaderKeys(self: *Compiler, keys: []const Key) void {
        if (self.leader_owned) {
            if (self.leader) |old| {
                self.allocator.free(old);
            }
        }
        self.leader = keys;
        self.leader_owned = false;
    }

    pub fn compile(self: *Compiler, bindings: []const Keybind) CompileError!Trie {
        var trie = Trie.init(self.allocator);
        errdefer trie.deinit();

        for (bindings) |binding| {
            try self.insertBinding(&trie, binding);
        }

        return trie;
    }

    fn insertBinding(self: *Compiler, trie: *Trie, binding: Keybind) CompileError!void {
        const expanded = self.expandLeader(binding.key_string) catch return error.InvalidKeyString;
        defer self.allocator.free(expanded);

        const keys = key_string.parseKeyString(self.allocator, expanded) catch return error.InvalidKeyString;
        defer self.allocator.free(keys);

        if (keys.len == 0) return error.InvalidKeyString;

        var node: *TrieNode = &trie.root;

        var buf: [64]u8 = undefined;
        for (keys, 0..) |key, i| {
            const is_last = i == keys.len - 1;
            const key_id_slice = keyToId(&buf, key);

            if (node.children.get(key_id_slice)) |existing| {
                if (is_last) {
                    if (existing.isLeaf()) {
                        return error.ConflictingBinding;
                    }
                    if (existing.isBranch()) {
                        return error.ConflictingBinding;
                    }
                    existing.action = binding.action;
                    existing.key_string = try self.allocator.dupe(u8, binding.key_string);
                } else {
                    if (existing.isLeaf()) {
                        return error.ConflictingBinding;
                    }
                    node = existing;
                }
            } else {
                const key_id = try self.allocator.dupe(u8, key_id_slice);
                errdefer self.allocator.free(key_id);

                const new_node = try self.allocator.create(TrieNode);
                errdefer self.allocator.destroy(new_node);
                new_node.* = .{};

                if (is_last) {
                    new_node.action = binding.action;
                    new_node.key_string = try self.allocator.dupe(u8, binding.key_string);
                }

                node.children.put(self.allocator, key_id, new_node) catch return error.OutOfMemory;
                node = new_node;
            }
        }
    }

    fn expandLeader(self: *Compiler, input: []const u8) ![]u8 {
        const leader_tag = "<leader>";
        const leader_tag_upper = "<Leader>";

        var result: std.ArrayListUnmanaged(u8) = .empty;
        errdefer result.deinit(self.allocator);

        var i: usize = 0;
        while (i < input.len) {
            if (i + leader_tag.len <= input.len) {
                const slice = input[i .. i + leader_tag.len];
                if (std.mem.eql(u8, slice, leader_tag) or std.mem.eql(u8, slice, leader_tag_upper)) {
                    if (self.leader) |leader_keys| {
                        for (leader_keys) |key| {
                            try self.appendKeyToString(&result, key);
                        }
                    }
                    i += leader_tag.len;
                    continue;
                }
            }
            try result.append(self.allocator, input[i]);
            i += 1;
        }

        return result.toOwnedSlice(self.allocator);
    }

    fn appendKeyToString(self: *Compiler, list: *std.ArrayListUnmanaged(u8), key: Key) !void {
        const has_mods = key.ctrl or key.alt or key.shift or key.super;
        const is_special = key.key.len > 1;

        if (has_mods or is_special) {
            try list.append(self.allocator, '<');
            if (key.ctrl) try list.appendSlice(self.allocator, "C-");
            if (key.alt) try list.appendSlice(self.allocator, "A-");
            if (key.shift) try list.appendSlice(self.allocator, "S-");
            if (key.super) try list.appendSlice(self.allocator, "D-");
            try list.appendSlice(self.allocator, key.key);
            try list.append(self.allocator, '>');
        } else {
            try list.appendSlice(self.allocator, key.key);
        }
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

test "compile single binding" {
    var compiler = Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]Keybind{
        .{ .key_string = "<D-k>", .action = .split_horizontal },
    };

    var trie = try compiler.compile(&bindings);
    defer trie.deinit();

    const keys = try key_string.parseKeyString(std.testing.allocator, "<D-k>");
    defer std.testing.allocator.free(keys);

    const node = trie.lookup(keys);
    try std.testing.expect(node != null);
    try std.testing.expectEqual(Action.split_horizontal, node.?.action.?);
}

test "compile sequence binding" {
    var compiler = Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]Keybind{
        .{ .key_string = "<D-k>v", .action = .split_horizontal },
    };

    var trie = try compiler.compile(&bindings);
    defer trie.deinit();

    const keys = try key_string.parseKeyString(std.testing.allocator, "<D-k>v");
    defer std.testing.allocator.free(keys);

    const node = trie.lookup(keys);
    try std.testing.expect(node != null);
    try std.testing.expectEqual(Action.split_horizontal, node.?.action.?);

    const partial_keys = try key_string.parseKeyString(std.testing.allocator, "<D-k>");
    defer std.testing.allocator.free(partial_keys);

    const partial_node = trie.lookup(partial_keys);
    try std.testing.expect(partial_node != null);
    try std.testing.expect(partial_node.?.action == null);
    try std.testing.expect(partial_node.?.isBranch());
}

test "compile multiple bindings" {
    var compiler = Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]Keybind{
        .{ .key_string = "<D-k>v", .action = .split_horizontal },
        .{ .key_string = "<D-k>s", .action = .split_vertical },
        .{ .key_string = "<D-k>h", .action = .focus_left },
    };

    var trie = try compiler.compile(&bindings);
    defer trie.deinit();

    {
        const keys = try key_string.parseKeyString(std.testing.allocator, "<D-k>v");
        defer std.testing.allocator.free(keys);
        const node = trie.lookup(keys);
        try std.testing.expectEqual(Action.split_horizontal, node.?.action.?);
    }

    {
        const keys = try key_string.parseKeyString(std.testing.allocator, "<D-k>s");
        defer std.testing.allocator.free(keys);
        const node = trie.lookup(keys);
        try std.testing.expectEqual(Action.split_vertical, node.?.action.?);
    }

    {
        const keys = try key_string.parseKeyString(std.testing.allocator, "<D-k>h");
        defer std.testing.allocator.free(keys);
        const node = trie.lookup(keys);
        try std.testing.expectEqual(Action.focus_left, node.?.action.?);
    }
}

test "conflict: same key twice" {
    var compiler = Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]Keybind{
        .{ .key_string = "<D-k>", .action = .split_horizontal },
        .{ .key_string = "<D-k>", .action = .split_vertical },
    };

    const result = compiler.compile(&bindings);
    try std.testing.expectError(error.ConflictingBinding, result);
}

test "conflict: prefix is leaf" {
    var compiler = Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]Keybind{
        .{ .key_string = "<D-k>", .action = .split_horizontal },
        .{ .key_string = "<D-k>v", .action = .split_vertical },
    };

    const result = compiler.compile(&bindings);
    try std.testing.expectError(error.ConflictingBinding, result);
}

test "conflict: leaf would shadow branch" {
    var compiler = Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]Keybind{
        .{ .key_string = "<D-k>v", .action = .split_vertical },
        .{ .key_string = "<D-k>", .action = .split_horizontal },
    };

    const result = compiler.compile(&bindings);
    try std.testing.expectError(error.ConflictingBinding, result);
}

test "leader expansion" {
    var compiler = Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    try compiler.setLeader("<D-k>");

    const bindings = [_]Keybind{
        .{ .key_string = "<leader>v", .action = .split_horizontal },
    };

    var trie = try compiler.compile(&bindings);
    defer trie.deinit();

    const keys = try key_string.parseKeyString(std.testing.allocator, "<D-k>v");
    defer std.testing.allocator.free(keys);

    const node = trie.lookup(keys);
    try std.testing.expect(node != null);
    try std.testing.expectEqual(Action.split_horizontal, node.?.action.?);
}

test "leader expansion multiple keys" {
    var compiler = Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    try compiler.setLeader("<C-a>b");

    const bindings = [_]Keybind{
        .{ .key_string = "<leader>x", .action = .close_pane },
    };

    var trie = try compiler.compile(&bindings);
    defer trie.deinit();

    const keys = try key_string.parseKeyString(std.testing.allocator, "<C-a>bx");
    defer std.testing.allocator.free(keys);

    const node = trie.lookup(keys);
    try std.testing.expect(node != null);
    try std.testing.expectEqual(Action.close_pane, node.?.action.?);
}

test "lookup nonexistent key" {
    var compiler = Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]Keybind{
        .{ .key_string = "<D-k>v", .action = .split_horizontal },
    };

    var trie = try compiler.compile(&bindings);
    defer trie.deinit();

    const keys = try key_string.parseKeyString(std.testing.allocator, "<D-k>x");
    defer std.testing.allocator.free(keys);

    const node = trie.lookup(keys);
    try std.testing.expect(node == null);
}

test "lookup partial sequence" {
    var compiler = Compiler.init(std.testing.allocator);
    defer compiler.deinit();

    const bindings = [_]Keybind{
        .{ .key_string = "<D-k>vg", .action = .split_horizontal },
    };

    var trie = try compiler.compile(&bindings);
    defer trie.deinit();

    {
        const keys = try key_string.parseKeyString(std.testing.allocator, "<D-k>");
        defer std.testing.allocator.free(keys);
        const node = trie.lookup(keys);
        try std.testing.expect(node != null);
        try std.testing.expect(node.?.action == null);
        try std.testing.expect(node.?.isBranch());
    }

    {
        const keys = try key_string.parseKeyString(std.testing.allocator, "<D-k>v");
        defer std.testing.allocator.free(keys);
        const node = trie.lookup(keys);
        try std.testing.expect(node != null);
        try std.testing.expect(node.?.action == null);
        try std.testing.expect(node.?.isBranch());
    }

    {
        const keys = try key_string.parseKeyString(std.testing.allocator, "<D-k>vg");
        defer std.testing.allocator.free(keys);
        const node = trie.lookup(keys);
        try std.testing.expect(node != null);
        try std.testing.expectEqual(Action.split_horizontal, node.?.action.?);
    }
}
