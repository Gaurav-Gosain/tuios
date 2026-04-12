//! Binary Space Partitioning tree for window tiling.
//! Ported from Go tuios internal/layout/bsp.go.
//! Uses a flat node pool with indices instead of pointers for cache-friendly access.

const std = @import("std");

const log = std.log.scoped(.bsp);

pub const max_nodes = 128; // Max BSP nodes (64 windows max since each split creates 2 nodes + 1 internal)

pub const SplitDirection = enum {
    none, // Leaf node
    vertical, // Left | Right
    horizontal, // Top / Bottom
};

pub const AutoScheme = enum {
    longest_side,
    alternate,
    spiral,
    smart_split,
};

pub const Rect = struct {
    x: u16,
    y: u16,
    w: u16,
    h: u16,
};

pub const Direction = enum {
    left,
    right,
    up,
    down,
};

const null_idx: u16 = std.math.maxInt(u16);

pub const Node = struct {
    window_id: i32, // -1 for internal nodes
    parent: u16 = null_idx,
    left: u16 = null_idx,
    right: u16 = null_idx,
    split: SplitDirection = .none,
    ratio: f32 = 0.5,
    active: bool = false, // whether this slot is in use

    pub fn isLeaf(self: Node) bool {
        return self.split == .none;
    }

    pub fn isRoot(self: Node) bool {
        return self.parent == null_idx;
    }
};

/// Flat-pool BSP tree. No heap allocation for nodes.
pub const BSPTree = struct {
    nodes: [max_nodes]Node = [_]Node{.{ .window_id = -1 }} ** max_nodes,
    node_count: u16 = 0,
    root: u16 = null_idx,
    auto_scheme: AutoScheme = .spiral,
    default_ratio: f32 = 0.5,

    // Window ID -> node index lookup (sparse; window IDs are u32 from server)
    // Using a small linear scan since we won't have many windows.
    window_map_ids: [64]u32 = [_]u32{0} ** 64,
    window_map_nodes: [64]u16 = [_]u16{null_idx} ** 64,
    window_map_count: u16 = 0,

    pub fn init() BSPTree {
        return .{};
    }

    // ---- Node pool management ----

    fn allocNode(self: *BSPTree) ?u16 {
        if (self.node_count >= max_nodes) return null;
        // Find first inactive slot
        for (0..max_nodes) |i| {
            if (!self.nodes[i].active) {
                self.nodes[i] = .{ .window_id = -1, .active = true };
                self.node_count += 1;
                return @intCast(i);
            }
        }
        return null;
    }

    fn freeNode(self: *BSPTree, idx: u16) void {
        if (idx == null_idx) return;
        self.nodes[idx].active = false;
        self.nodes[idx].window_id = -1;
        self.nodes[idx].parent = null_idx;
        self.nodes[idx].left = null_idx;
        self.nodes[idx].right = null_idx;
        self.nodes[idx].split = .none;
        if (self.node_count > 0) self.node_count -= 1;
    }

    // ---- Window map ----

    fn mapPut(self: *BSPTree, window_id: u32, node_idx: u16) void {
        // Update existing
        for (0..self.window_map_count) |i| {
            if (self.window_map_ids[i] == window_id) {
                self.window_map_nodes[i] = node_idx;
                return;
            }
        }
        // Insert new
        if (self.window_map_count < 64) {
            self.window_map_ids[self.window_map_count] = window_id;
            self.window_map_nodes[self.window_map_count] = node_idx;
            self.window_map_count += 1;
        }
    }

    fn mapGet(self: *const BSPTree, window_id: u32) ?u16 {
        for (0..self.window_map_count) |i| {
            if (self.window_map_ids[i] == window_id) {
                return self.window_map_nodes[i];
            }
        }
        return null;
    }

    fn mapRemove(self: *BSPTree, window_id: u32) void {
        for (0..self.window_map_count) |i| {
            if (self.window_map_ids[i] == window_id) {
                // Swap with last
                const last = self.window_map_count - 1;
                self.window_map_ids[i] = self.window_map_ids[last];
                self.window_map_nodes[i] = self.window_map_nodes[last];
                self.window_map_count -= 1;
                return;
            }
        }
    }

    pub fn hasWindow(self: *const BSPTree, window_id: u32) bool {
        return self.mapGet(window_id) != null;
    }

    pub fn isEmpty(self: *const BSPTree) bool {
        return self.root == null_idx;
    }

    pub fn windowCount(self: *const BSPTree) u16 {
        return self.window_map_count;
    }

    // ---- Core operations ----

    /// Insert a new window by splitting the focused window.
    /// If direction is .none, uses auto scheme.
    pub fn insertWindow(self: *BSPTree, window_id: u32, focused_id: u32, direction: SplitDirection, bounds: Rect) void {
        if (self.hasWindow(window_id)) return;

        const new_idx = self.allocNode() orelse return;
        self.nodes[new_idx].window_id = @intCast(window_id);

        // First window
        if (self.root == null_idx) {
            self.root = new_idx;
            self.mapPut(window_id, new_idx);
            return;
        }

        // Find target to split
        const target_idx = self.mapGet(focused_id) orelse self.findAnyLeafIdx() orelse {
            self.root = new_idx;
            self.mapPut(window_id, new_idx);
            return;
        };

        // Determine split direction
        const split_dir = if (direction != .none) direction else self.determineAutoSplit(target_idx, bounds);

        // Create new internal node replacing target
        const old_leaf_idx = self.allocNode() orelse {
            self.freeNode(new_idx);
            return;
        };
        self.nodes[old_leaf_idx].window_id = self.nodes[target_idx].window_id;

        const internal_idx = self.allocNode() orelse {
            self.freeNode(new_idx);
            self.freeNode(old_leaf_idx);
            return;
        };
        self.nodes[internal_idx].window_id = -1;
        self.nodes[internal_idx].split = split_dir;
        self.nodes[internal_idx].ratio = self.default_ratio;
        self.nodes[internal_idx].left = old_leaf_idx;
        self.nodes[internal_idx].right = new_idx;

        // Set parent pointers
        self.nodes[old_leaf_idx].parent = internal_idx;
        self.nodes[new_idx].parent = internal_idx;

        // Replace target in tree
        const target_parent = self.nodes[target_idx].parent;
        if (target_parent == null_idx) {
            self.root = internal_idx;
        } else {
            self.nodes[internal_idx].parent = target_parent;
            if (self.nodes[target_parent].left == target_idx) {
                self.nodes[target_parent].left = internal_idx;
            } else {
                self.nodes[target_parent].right = internal_idx;
            }
        }

        // Update maps
        const old_window_id: u32 = @intCast(self.nodes[old_leaf_idx].window_id);
        self.mapPut(old_window_id, old_leaf_idx);
        self.mapPut(window_id, new_idx);

        // Free the old target node slot (replaced by internal + old_leaf)
        self.freeNode(target_idx);
    }

    /// Remove a window, collapsing its sibling up.
    pub fn removeWindow(self: *BSPTree, window_id: u32) void {
        const node_idx = self.mapGet(window_id) orelse return;
        self.mapRemove(window_id);

        // Only window
        if (self.nodes[node_idx].parent == null_idx) {
            self.root = null_idx;
            self.freeNode(node_idx);
            return;
        }

        const parent_idx = self.nodes[node_idx].parent;
        const sibling_idx = if (self.nodes[parent_idx].left == node_idx)
            self.nodes[parent_idx].right
        else
            self.nodes[parent_idx].left;

        const grandparent_idx = self.nodes[parent_idx].parent;

        // Sibling takes parent's place
        if (grandparent_idx == null_idx) {
            self.root = sibling_idx;
            self.nodes[sibling_idx].parent = null_idx;
        } else {
            self.nodes[sibling_idx].parent = grandparent_idx;
            if (self.nodes[grandparent_idx].left == parent_idx) {
                self.nodes[grandparent_idx].left = sibling_idx;
            } else {
                self.nodes[grandparent_idx].right = sibling_idx;
            }
        }

        self.freeNode(node_idx);
        self.freeNode(parent_idx);
    }

    /// Compute layout rects for all windows.
    pub fn applyLayout(self: *const BSPTree, bounds: Rect, out_ids: []u32, out_rects: []Rect) u16 {
        var count: u16 = 0;
        if (self.root != null_idx) {
            self.applyLayoutRecursive(self.root, bounds, out_ids, out_rects, &count);
        }
        return count;
    }

    fn applyLayoutRecursive(self: *const BSPTree, idx: u16, bounds: Rect, out_ids: []u32, out_rects: []Rect, count: *u16) void {
        if (idx == null_idx) return;
        const node = self.nodes[idx];

        if (node.isLeaf()) {
            if (count.* < out_ids.len) {
                out_ids[count.*] = @intCast(node.window_id);
                out_rects[count.*] = bounds;
                count.* += 1;
            }
            return;
        }

        // Split into left/right bounds with 1 cell separator
        var left_bounds: Rect = undefined;
        var right_bounds: Rect = undefined;

        if (node.split == .vertical) {
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

        self.applyLayoutRecursive(node.left, left_bounds, out_ids, out_rects, count);
        self.applyLayoutRecursive(node.right, right_bounds, out_ids, out_rects, count);
    }

    /// Collect separator lines for rendering between panes.
    pub fn collectSplits(self: *const BSPTree, bounds: Rect, out_splits: []SplitLine) u16 {
        var count: u16 = 0;
        if (self.root != null_idx) {
            self.collectSplitsRecursive(self.root, bounds, out_splits, &count);
        }
        return count;
    }

    fn collectSplitsRecursive(self: *const BSPTree, idx: u16, bounds: Rect, out_splits: []SplitLine, count: *u16) void {
        if (idx == null_idx) return;
        const node = self.nodes[idx];
        if (node.isLeaf()) return;

        if (count.* >= out_splits.len) return;

        var left_bounds: Rect = undefined;
        var right_bounds: Rect = undefined;

        if (node.split == .vertical) {
            const split_x = bounds.x + @as(u16, @intFromFloat(@as(f32, @floatFromInt(bounds.w)) * node.ratio));
            const clamped_x = std.math.clamp(split_x, bounds.x + 1, bounds.x + bounds.w -| 2);
            out_splits[count.*] = .{
                .vertical = true,
                .pos = clamped_x,
                .from = bounds.y,
                .to = bounds.y + bounds.h -| 1,
            };
            count.* += 1;
            left_bounds = .{ .x = bounds.x, .y = bounds.y, .w = clamped_x - bounds.x, .h = bounds.h };
            right_bounds = .{ .x = clamped_x + 1, .y = bounds.y, .w = bounds.x + bounds.w -| clamped_x -| 1, .h = bounds.h };
        } else {
            const split_y = bounds.y + @as(u16, @intFromFloat(@as(f32, @floatFromInt(bounds.h)) * node.ratio));
            const clamped_y = std.math.clamp(split_y, bounds.y + 1, bounds.y + bounds.h -| 2);
            out_splits[count.*] = .{
                .vertical = false,
                .pos = clamped_y,
                .from = bounds.x,
                .to = bounds.x + bounds.w -| 1,
            };
            count.* += 1;
            left_bounds = .{ .x = bounds.x, .y = bounds.y, .w = bounds.w, .h = clamped_y - bounds.y };
            right_bounds = .{ .x = bounds.x, .y = clamped_y + 1, .w = bounds.w, .h = bounds.y + bounds.h -| clamped_y -| 1 };
        }

        self.collectSplitsRecursive(node.left, left_bounds, out_splits, count);
        self.collectSplitsRecursive(node.right, right_bounds, out_splits, count);
    }

    /// Find neighboring window in a direction using geometry.
    pub fn findNeighbor(self: *const BSPTree, window_id: u32, dir: Direction, bounds: Rect) ?u32 {
        // Compute all layout rects
        var ids: [64]u32 = undefined;
        var rects: [64]Rect = undefined;
        const count = self.applyLayout(bounds, &ids, &rects);

        // Find the focused window's rect
        var focused_rect: ?Rect = null;
        for (0..count) |i| {
            if (ids[i] == window_id) {
                focused_rect = rects[i];
                break;
            }
        }
        const fr = focused_rect orelse return null;

        // Find closest neighbor in direction
        var best_id: ?u32 = null;
        var best_dist: i32 = std.math.maxInt(i32);

        const fc_x: i32 = @as(i32, fr.x) + @divFloor(@as(i32, fr.w), 2);
        const fc_y: i32 = @as(i32, fr.y) + @divFloor(@as(i32, fr.h), 2);

        for (0..count) |i| {
            if (ids[i] == window_id) continue;
            const r = rects[i];
            const rc_x: i32 = @as(i32, r.x) + @divFloor(@as(i32, r.w), 2);
            const rc_y: i32 = @as(i32, r.y) + @divFloor(@as(i32, r.h), 2);

            const valid = switch (dir) {
                .left => rc_x < fc_x,
                .right => rc_x > fc_x,
                .up => rc_y < fc_y,
                .down => rc_y > fc_y,
            };

            if (valid) {
                // Manhattan distance
                const dist = @as(i32, @intCast(@abs(rc_x - fc_x))) + @as(i32, @intCast(@abs(rc_y - fc_y)));
                if (dist < best_dist) {
                    best_dist = dist;
                    best_id = ids[i];
                }
            }
        }

        return best_id;
    }

    /// Rotate the split direction at the parent of a window.
    pub fn rotateSplit(self: *BSPTree, window_id: u32) void {
        const node_idx = self.mapGet(window_id) orelse return;
        const parent_idx = self.nodes[node_idx].parent;
        if (parent_idx == null_idx) return;

        self.nodes[parent_idx].split = if (self.nodes[parent_idx].split == .vertical)
            .horizontal
        else
            .vertical;
    }

    /// Swap positions of two windows.
    pub fn swapWindows(self: *BSPTree, id1: u32, id2: u32) void {
        const idx1 = self.mapGet(id1) orelse return;
        const idx2 = self.mapGet(id2) orelse return;

        // Swap window IDs in nodes
        const tmp = self.nodes[idx1].window_id;
        self.nodes[idx1].window_id = self.nodes[idx2].window_id;
        self.nodes[idx2].window_id = tmp;

        // Update lookup map
        self.mapPut(id1, idx2);
        self.mapPut(id2, idx1);
    }

    /// Set all ratios to 0.5.
    pub fn equalizeRatios(self: *BSPTree) void {
        for (&self.nodes) |*node| {
            if (node.active and !node.isLeaf()) {
                node.ratio = 0.5;
            }
        }
    }

    /// Get all window IDs (in-order traversal).
    pub fn getAllWindowIDs(self: *const BSPTree, out: []u32) u16 {
        var count: u16 = 0;
        if (self.root != null_idx) {
            self.collectIDs(self.root, out, &count);
        }
        return count;
    }

    fn collectIDs(self: *const BSPTree, idx: u16, out: []u32, count: *u16) void {
        if (idx == null_idx) return;
        const node = self.nodes[idx];
        if (node.isLeaf()) {
            if (count.* < out.len) {
                out[count.*] = @intCast(node.window_id);
                count.* += 1;
            }
            return;
        }
        self.collectIDs(node.left, out, count);
        self.collectIDs(node.right, out, count);
    }

    /// Adjust split ratio at a given split handle position.
    pub fn adjustRatio(self: *BSPTree, window_id: u32, new_ratio: f32) void {
        const node_idx = self.mapGet(window_id) orelse return;
        const parent_idx = self.nodes[node_idx].parent;
        if (parent_idx == null_idx) return;
        self.nodes[parent_idx].ratio = std.math.clamp(new_ratio, 0.1, 0.9);
    }

    // ---- Auto split scheme ----

    fn determineAutoSplit(self: *const BSPTree, target_idx: u16, bounds: Rect) SplitDirection {
        switch (self.auto_scheme) {
            .longest_side => {
                // Compute target's rect
                const r = self.nodeRect(target_idx, bounds);
                return if (r.w >= r.h) .vertical else .horizontal;
            },
            .alternate, .spiral => {
                const count = self.countInternalNodes();
                return if (count % 2 == 0) .vertical else .horizontal;
            },
            .smart_split => {
                const r = self.nodeRect(target_idx, bounds);
                if (r.w > r.h * 2) return .vertical;
                if (r.h > r.w) return .horizontal;
                const depth = self.nodeDepth(target_idx);
                return if (depth % 2 == 0) .vertical else .horizontal;
            },
        }
    }

    fn nodeRect(self: *const BSPTree, target: u16, bounds: Rect) Rect {
        // Collect ancestors
        var ancestors: [32]u16 = undefined;
        var depth: u16 = 0;
        var cur = target;
        while (self.nodes[cur].parent != null_idx) : (depth += 1) {
            if (depth >= 32) break;
            ancestors[depth] = cur;
            cur = self.nodes[cur].parent;
        }

        // Walk from root down
        var rect = bounds;
        var i: i32 = @as(i32, depth) - 1;
        while (i >= 0) : (i -= 1) {
            const child = ancestors[@intCast(i)];
            const parent = self.nodes[child].parent;
            if (self.nodes[parent].split == .vertical) {
                const split_x = rect.x + @as(u16, @intFromFloat(@as(f32, @floatFromInt(rect.w)) * self.nodes[parent].ratio));
                if (self.nodes[parent].left == child) {
                    rect.w = split_x - rect.x;
                } else {
                    rect.w = rect.x + rect.w - split_x;
                    rect.x = split_x;
                }
            } else {
                const split_y = rect.y + @as(u16, @intFromFloat(@as(f32, @floatFromInt(rect.h)) * self.nodes[parent].ratio));
                if (self.nodes[parent].left == child) {
                    rect.h = split_y - rect.y;
                } else {
                    rect.h = rect.y + rect.h - split_y;
                    rect.y = split_y;
                }
            }
        }
        return rect;
    }

    fn nodeDepth(self: *const BSPTree, idx: u16) u16 {
        var depth: u16 = 0;
        var cur = idx;
        while (self.nodes[cur].parent != null_idx) {
            depth += 1;
            cur = self.nodes[cur].parent;
        }
        return depth;
    }

    fn countInternalNodes(self: *const BSPTree) u16 {
        var count: u16 = 0;
        for (self.nodes) |node| {
            if (node.active and !node.isLeaf()) count += 1;
        }
        return count;
    }

    fn findAnyLeafIdx(self: *const BSPTree) ?u16 {
        return findLeafInSubtree(self, self.root);
    }

    fn findLeafInSubtree(self: *const BSPTree, idx: u16) ?u16 {
        if (idx == null_idx) return null;
        if (self.nodes[idx].isLeaf()) return idx;
        return findLeafInSubtree(self, self.nodes[idx].left) orelse
            findLeafInSubtree(self, self.nodes[idx].right);
    }
};

pub const SplitLine = struct {
    vertical: bool,
    pos: u16,
    from: u16,
    to: u16,
};

// ---- Tests ----

test "BSPTree: insert and remove" {
    var tree = BSPTree.init();

    // Insert first window
    tree.insertWindow(1, 0, .none, .{ .x = 0, .y = 0, .w = 80, .h = 24 });
    try std.testing.expect(tree.hasWindow(1));
    try std.testing.expectEqual(@as(u16, 1), tree.windowCount());

    // Insert second (splits focused)
    tree.insertWindow(2, 1, .vertical, .{ .x = 0, .y = 0, .w = 80, .h = 24 });
    try std.testing.expect(tree.hasWindow(2));
    try std.testing.expectEqual(@as(u16, 2), tree.windowCount());

    // Layout
    var ids: [64]u32 = undefined;
    var rects: [64]Rect = undefined;
    const count = tree.applyLayout(.{ .x = 0, .y = 0, .w = 80, .h = 24 }, &ids, &rects);
    try std.testing.expectEqual(@as(u16, 2), count);

    // Remove
    tree.removeWindow(1);
    try std.testing.expect(!tree.hasWindow(1));
    try std.testing.expectEqual(@as(u16, 1), tree.windowCount());
}

test "BSPTree: find neighbor" {
    var tree = BSPTree.init();
    const bounds = Rect{ .x = 0, .y = 0, .w = 80, .h = 24 };

    tree.insertWindow(1, 0, .none, bounds);
    tree.insertWindow(2, 1, .vertical, bounds);

    // Window 2 should be to the right of window 1
    const right = tree.findNeighbor(1, .right, bounds);
    try std.testing.expectEqual(@as(?u32, 2), right);

    const left = tree.findNeighbor(2, .left, bounds);
    try std.testing.expectEqual(@as(?u32, 1), left);

    // No neighbor above/below in a vertical split
    try std.testing.expectEqual(@as(?u32, null), tree.findNeighbor(1, .up, bounds));
}

test "BSPTree: three-window spiral layout" {
    var tree = BSPTree.init();
    const bounds = Rect{ .x = 0, .y = 0, .w = 120, .h = 40 };

    tree.insertWindow(1, 0, .none, bounds);
    tree.insertWindow(2, 1, .none, bounds); // auto: vertical (0 internal nodes -> even -> V)
    tree.insertWindow(3, 2, .none, bounds); // auto: horizontal (1 internal node -> odd -> H)

    try std.testing.expectEqual(@as(u16, 3), tree.windowCount());

    var ids: [64]u32 = undefined;
    var rects: [64]Rect = undefined;
    const count = tree.applyLayout(bounds, &ids, &rects);
    try std.testing.expectEqual(@as(u16, 3), count);

    // All three rects should be non-zero
    for (0..count) |i| {
        try std.testing.expect(rects[i].w > 0);
        try std.testing.expect(rects[i].h > 0);
    }
}

test "BSPTree: swap windows" {
    var tree = BSPTree.init();
    const bounds = Rect{ .x = 0, .y = 0, .w = 80, .h = 24 };

    tree.insertWindow(1, 0, .none, bounds);
    tree.insertWindow(2, 1, .vertical, bounds);

    // Get positions before swap
    var ids_before: [64]u32 = undefined;
    var rects_before: [64]Rect = undefined;
    _ = tree.applyLayout(bounds, &ids_before, &rects_before);

    tree.swapWindows(1, 2);

    // Both windows should still exist
    try std.testing.expect(tree.hasWindow(1));
    try std.testing.expect(tree.hasWindow(2));
}

test "BSPTree: rotate split" {
    var tree = BSPTree.init();
    const bounds = Rect{ .x = 0, .y = 0, .w = 80, .h = 24 };

    tree.insertWindow(1, 0, .none, bounds);
    tree.insertWindow(2, 1, .vertical, bounds);

    // After rotate, neighbor directions should change
    tree.rotateSplit(1);

    // Now it should be horizontal: 1 above, 2 below
    const below = tree.findNeighbor(1, .down, bounds);
    try std.testing.expectEqual(@as(?u32, 2), below);
}

test "BSPTree: remove middle window" {
    var tree = BSPTree.init();
    const bounds = Rect{ .x = 0, .y = 0, .w = 120, .h = 40 };

    tree.insertWindow(1, 0, .none, bounds);
    tree.insertWindow(2, 1, .vertical, bounds);
    tree.insertWindow(3, 2, .none, bounds);

    // Remove window 2 — its sibling (3) should take its place
    tree.removeWindow(2);
    try std.testing.expectEqual(@as(u16, 2), tree.windowCount());
    try std.testing.expect(tree.hasWindow(1));
    try std.testing.expect(tree.hasWindow(3));
    try std.testing.expect(!tree.hasWindow(2));
}

test "BSPTree: collect splits" {
    var tree = BSPTree.init();
    const bounds = Rect{ .x = 0, .y = 0, .w = 80, .h = 24 };

    tree.insertWindow(1, 0, .none, bounds);
    tree.insertWindow(2, 1, .vertical, bounds);

    var splits: [32]SplitLine = undefined;
    const split_count = tree.collectSplits(bounds, &splits);
    try std.testing.expectEqual(@as(u16, 1), split_count);
    try std.testing.expect(splits[0].vertical);
}

test "BSPTree: equalize ratios" {
    var tree = BSPTree.init();
    const bounds = Rect{ .x = 0, .y = 0, .w = 80, .h = 24 };

    tree.insertWindow(1, 0, .none, bounds);
    tree.insertWindow(2, 1, .vertical, bounds);

    // Adjust ratio
    tree.adjustRatio(2, 0.7);
    tree.equalizeRatios();

    // All ratios should be 0.5
    for (tree.nodes) |node| {
        if (node.active and !node.isLeaf()) {
            try std.testing.expectEqual(@as(f32, 0.5), node.ratio);
        }
    }
}
