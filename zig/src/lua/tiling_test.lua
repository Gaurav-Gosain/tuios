local helpers = require("test_helpers")
local mock_pane = helpers.mock_pane
local mock_split = helpers.mock_split
helpers.setup_tuios_mock()

local tiling = require("tiling")
local t = tiling._test

-- === is_pane / is_split ===

-- Test: is_pane with pane node
assert(t.is_pane({ type = "pane", id = 1 }) == true, "is_pane: pane node")

-- Test: is_pane with split node
assert(t.is_pane({ type = "split" }) == false, "is_pane: split node")

-- Test: is_pane with nil
assert(not t.is_pane(nil), "is_pane: nil")

-- Test: is_split with split node
assert(t.is_split({ type = "split", children = {} }) == true, "is_split: split node")

-- Test: is_split with pane node
assert(t.is_split({ type = "pane" }) == false, "is_split: pane node")

-- Test: is_split with nil
assert(not t.is_split(nil), "is_split: nil")

-- === collect_panes ===

-- Test: collect_panes with nil
local panes = t.collect_panes(nil)
assert(#panes == 0, "collect_panes: nil returns empty")

-- Test: collect_panes with single pane
local single_pane = mock_pane(1)
panes = t.collect_panes(single_pane)
assert(#panes == 1, "collect_panes: single pane count")
assert(panes[1].id == 1, "collect_panes: single pane id")

-- Test: collect_panes with split containing panes
local split_node = mock_split(1, "row", {
    mock_pane(1),
    mock_pane(2),
})
panes = t.collect_panes(split_node)
assert(#panes == 2, "collect_panes: split with 2 panes")
assert(panes[1].id == 1, "collect_panes: first pane")
assert(panes[2].id == 2, "collect_panes: second pane")

-- Test: collect_panes with nested splits
local nested = mock_split(1, "col", {
    mock_pane(1),
    mock_split(2, "row", {
        mock_pane(2),
        mock_pane(3),
    }),
})
panes = t.collect_panes(nested)
assert(#panes == 3, "collect_panes: nested splits")
assert(panes[1].id == 1, "collect_panes: nested first")
assert(panes[2].id == 2, "collect_panes: nested second")
assert(panes[3].id == 3, "collect_panes: nested third")

-- === find_node_path ===

-- Test: find_node_path with nil
local path = t.find_node_path(nil, 1)
assert(path == nil, "find_node_path: nil returns nil")

-- Test: find_node_path with single pane (found)
local pane1 = mock_pane(1)
path = t.find_node_path(pane1, 1)
assert(path ~= nil, "find_node_path: single pane found")
assert(#path == 1, "find_node_path: path length 1")
assert(path[1].id == 1, "find_node_path: path contains pane")

-- Test: find_node_path with single pane (not found)
path = t.find_node_path(pane1, 99)
assert(path == nil, "find_node_path: single pane not found")

-- Test: find_node_path in split
local split_for_path = mock_split(1, "row", {
    mock_pane(1),
    mock_pane(2),
})
path = t.find_node_path(split_for_path, 2)
assert(path ~= nil, "find_node_path: found in split")
assert(#path == 2, "find_node_path: path through split")
assert(path[1].type == "split", "find_node_path: first is split")
assert(path[2].id == 2, "find_node_path: second is target pane")

-- Test: find_node_path in nested splits
local nested_for_path = mock_split(1, "col", {
    mock_pane(1),
    mock_split(2, "row", {
        mock_pane(2),
        mock_pane(3),
    }),
})
path = t.find_node_path(nested_for_path, 3)
assert(path ~= nil, "find_node_path: found in nested")
assert(#path == 3, "find_node_path: nested path length")
assert(path[1].type == "split", "find_node_path: nested first is split")
assert(path[2].type == "split", "find_node_path: nested second is split")
assert(path[3].id == 3, "find_node_path: nested third is target")

-- Test: find_node_path not found in nested
path = t.find_node_path(nested_for_path, 99)
assert(path == nil, "find_node_path: not found in nested")

-- === get_first_leaf ===

-- Test: get_first_leaf with nil
assert(t.get_first_leaf(nil) == nil, "get_first_leaf: nil")

-- Test: get_first_leaf with pane
local leaf_pane = mock_pane(5)
local leaf = t.get_first_leaf(leaf_pane)
assert(leaf ~= nil, "get_first_leaf: pane not nil")
assert(leaf.id == 5, "get_first_leaf: pane returns self")

-- Test: get_first_leaf with split
local split_for_leaf = mock_split(1, "row", {
    mock_pane(10),
    mock_pane(20),
})
leaf = t.get_first_leaf(split_for_leaf)
assert(leaf ~= nil, "get_first_leaf: split not nil")
assert(leaf.id == 10, "get_first_leaf: returns first child")

-- Test: get_first_leaf with nested splits
local nested_for_leaf = mock_split(1, "col", {
    mock_split(2, "row", {
        mock_pane(100),
        mock_pane(200),
    }),
    mock_pane(300),
})
leaf = t.get_first_leaf(nested_for_leaf)
assert(leaf ~= nil, "get_first_leaf: nested not nil")
assert(leaf.id == 100, "get_first_leaf: returns deepest first")

-- === get_last_leaf ===

-- Test: get_last_leaf with nil
assert(t.get_last_leaf(nil) == nil, "get_last_leaf: nil")

-- Test: get_last_leaf with pane
leaf = t.get_last_leaf(leaf_pane)
assert(leaf ~= nil, "get_last_leaf: pane not nil")
assert(leaf.id == 5, "get_last_leaf: pane returns self")

-- Test: get_last_leaf with split
leaf = t.get_last_leaf(split_for_leaf)
assert(leaf ~= nil, "get_last_leaf: split not nil")
assert(leaf.id == 20, "get_last_leaf: returns last child")

-- Test: get_last_leaf with nested splits
leaf = t.get_last_leaf(nested_for_leaf)
assert(leaf ~= nil, "get_last_leaf: nested not nil")
assert(leaf.id == 300, "get_last_leaf: returns deepest last")

-- Test: get_last_leaf with right-heavy nesting
local right_nested = mock_split(1, "col", {
    mock_pane(1),
    mock_split(2, "row", {
        mock_pane(2),
        mock_pane(3),
    }),
})
leaf = t.get_last_leaf(right_nested)
assert(leaf ~= nil, "get_last_leaf: right-heavy nested should return leaf")
assert(leaf.id == 3, "get_last_leaf: right-heavy nested")

-- === format_palette_item ===

-- Test: format_palette_item without shortcut
local item = t.format_palette_item("Close Pane", nil, 50)
assert(item == "Close Pane", "format_palette_item: no shortcut")

-- Test: format_palette_item with shortcut (using ASCII for predictable byte length)
item = t.format_palette_item("Close", "C-w", 20)
-- "Close" (5) + padding + "C-w" (3) = 20, padding = 12
assert(item:sub(1, 5) == "Close", "format_palette_item: name preserved")
assert(item:sub(-3) == "C-w", "format_palette_item: shortcut at end")
assert(#item == 20, "format_palette_item: correct width")

-- Test: format_palette_item with minimum padding
item = t.format_palette_item("Very Long Command Name", "C-x", 10)
-- Width is too small, should use minimum padding of 2
assert(item == "Very Long Command Name  C-x", "format_palette_item: minimum padding")
