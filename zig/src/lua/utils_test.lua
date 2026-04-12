local utils = require("utils")

-- Test: deep_merge merges nested tables
local target = { theme = { bg = "black", fg = "white" } }
local source = { theme = { fg = "gray" } }
utils.deep_merge(target, source)
assert(target.theme.bg == "black", "deep_merge: bg should be preserved")
assert(target.theme.fg == "gray", "deep_merge: fg should be overwritten")

-- Test: deep_merge also merges keybind-like tables (no special handling)
target = { keybinds = { leader = { key = "k", super = true } } }
source = { keybinds = { leader = { key = "a", ctrl = true } } }
utils.deep_merge(target, source)
assert(target.keybinds.leader.key == "a", "deep_merge: key should be replaced")
assert(target.keybinds.leader.ctrl == true, "deep_merge: ctrl should be set")
assert(target.keybinds.leader.super == true, "deep_merge: super should be inherited")

-- Test: merge_config merges nested tables
target = { theme = { bg = "black", fg = "white" } }
source = { theme = { fg = "gray" } }
utils.merge_config(target, source)
assert(target.theme.bg == "black", "merge_config: bg should be preserved")
assert(target.theme.fg == "gray", "merge_config: fg should be overwritten")

-- Test: merge_config replaces keybind tables entirely
target = { keybinds = { leader = { key = "k", super = true } } }
source = { keybinds = { leader = { key = "a", ctrl = true } } }
utils.merge_config(target, source)
assert(target.keybinds.leader.key == "a", "merge_config: key should be replaced")
assert(target.keybinds.leader.ctrl == true, "merge_config: ctrl should be set")
assert(target.keybinds.leader.super == nil, "merge_config: super should not be inherited")

-- Test: merge_config replaces nested keybinds too
target = { keybinds = { palette = { key = "p", super = true, shift = true } } }
source = { keybinds = { palette = { key = "o", alt = true } } }
utils.merge_config(target, source)
assert(target.keybinds.palette.key == "o", "merge_config: key should be replaced")
assert(target.keybinds.palette.alt == true, "merge_config: alt should be set")
assert(target.keybinds.palette.super == nil, "merge_config: super should not be inherited")
assert(target.keybinds.palette.shift == nil, "merge_config: shift should not be inherited")

-- Test: is_keybind correctly identifies keybinds
assert(utils.is_keybind({ key = "a" }) == true, "should detect keybind")
assert(utils.is_keybind({ key = "b", ctrl = true }) == true, "should detect keybind with modifiers")
assert(utils.is_keybind({ foo = "bar" }) == false, "should not detect non-keybind")
assert(utils.is_keybind({}) == false, "should not detect empty table")
