local helpers = require("test_helpers")
local mock_pane = helpers.mock_pane
local mock_split = helpers.mock_split
helpers.setup_tuios_mock()

local tiling = require("tiling")
local t = tiling._test

-- === zoom state across tabs ===

t.set_state({
    tabs = {
        { id = 1, root = mock_pane(1), last_focused_id = 1 },
        { id = 2, root = mock_pane(2), last_focused_id = 2, zoomed_pane_id = 2 },
    },
    active_tab = 1,
    focused_id = 1,
    zoomed_pane_id = 1,
})
t.set_active_tab_index(2)
local zoom_state = t.get_state()
assert(zoom_state.active_tab == 2, "zoom state: switched to tab 2")
assert(zoom_state.zoomed_pane_id == 2, "zoom state: restored zoom on tab 2")
assert(zoom_state.tabs[1].zoomed_pane_id == 1, "zoom state: saved zoom on tab 1")
assert(zoom_state.focused_id == 2, "zoom state: restored focused pane on tab 2")

-- === zoom clears when zoomed pane exits ===

t.set_state({
    tabs = {
        {
            id = 1,
            root = mock_split(10, "row", {
                mock_pane(1),
                mock_pane(2),
            }),
            last_focused_id = 2,
        },
    },
    active_tab = 1,
    focused_id = 2,
    zoomed_pane_id = 2,
})
tiling.update({ type = "pty_exited", data = { id = 2 } })
zoom_state = t.get_state()
assert(zoom_state.zoomed_pane_id == nil, "pty_exited: clears active zoom state")
assert(zoom_state.tabs[1].root.id == 1, "pty_exited: removes exited pane from layout")

-- === split while zoomed clears zoom ===

t.set_state({
    tabs = {
        {
            id = 1,
            root = mock_pane(1),
            last_focused_id = 1,
        },
    },
    active_tab = 1,
    focused_id = 1,
    zoomed_pane_id = 1,
})
-- Simulate a pending split then a new PTY attaching
local s = t.get_state()
s.pending_split = { direction = "row" }
tiling.update({ type = "pty_attach", data = { pty = helpers.mock_pty(2) } })
zoom_state = t.get_state()
assert(zoom_state.zoomed_pane_id == nil, "split while zoomed: clears zoom")
assert(zoom_state.tabs[1].root.type == "split", "split while zoomed: created split node")

-- === zoomed pane exits on inactive tab ===

t.set_state({
    tabs = {
        { id = 1, root = mock_pane(1), last_focused_id = 1 },
        {
            id = 2,
            root = mock_split(10, "row", {
                mock_pane(2),
                mock_pane(3),
            }),
            last_focused_id = 2,
            zoomed_pane_id = 2,
        },
    },
    active_tab = 1,
    focused_id = 1,
})
tiling.update({ type = "pty_exited", data = { id = 2 } })
zoom_state = t.get_state()
assert(zoom_state.tabs[2].zoomed_pane_id == nil, "pty_exited inactive tab: clears saved zoom")
assert(zoom_state.tabs[2].root.id == 3, "pty_exited inactive tab: remaining pane promoted")

-- === close active tab restores zoom from new tab ===

t.set_state({
    tabs = {
        { id = 1, root = mock_pane(1), last_focused_id = 1 },
        { id = 2, root = mock_pane(2), last_focused_id = 2, zoomed_pane_id = 2 },
    },
    active_tab = 1,
    focused_id = 1,
    zoomed_pane_id = nil,
})
t.close_tab(1)
zoom_state = t.get_state()
assert(zoom_state.zoomed_pane_id == 2, "close active tab: restored zoom from new tab")
assert(zoom_state.active_tab == 1, "close active tab: active tab index adjusted")
assert(zoom_state.focused_id == 2, "close active tab: focus moved to new tab")

-- === last pane exits on active tab restores zoom from new tab ===

t.set_state({
    tabs = {
        { id = 1, root = mock_pane(1), last_focused_id = 1 },
        { id = 2, root = mock_pane(2), last_focused_id = 2, zoomed_pane_id = 2 },
    },
    active_tab = 1,
    focused_id = 1,
    zoomed_pane_id = nil,
})
t.remove_pane_by_id(1)
zoom_state = t.get_state()
assert(zoom_state.zoomed_pane_id == 2, "pane collapse: restored zoom from new tab")
assert(#zoom_state.tabs == 1, "pane collapse: empty tab removed")
assert(zoom_state.focused_id == 2, "pane collapse: focus moved to remaining tab")

-- === closing background tab preserves active zoom ===

t.set_state({
    tabs = {
        { id = 1, root = mock_pane(1), last_focused_id = 1 },
        { id = 2, root = mock_pane(2), last_focused_id = 2 },
    },
    active_tab = 1,
    focused_id = 1,
    zoomed_pane_id = 1,
})
t.close_tab(2)
zoom_state = t.get_state()
assert(zoom_state.zoomed_pane_id == 1, "close background tab: preserves active zoom")
assert(#zoom_state.tabs == 1, "close background tab: tab removed")
assert(zoom_state.focused_id == 1, "close background tab: focus unchanged")

-- === inactive tab tab-bar zoom state ===

t.set_state({
    tabs = {
        { id = 1, root = mock_pane(1), last_focused_id = 1 },
        { id = 2, root = mock_pane(2), last_focused_id = 2, zoomed_pane_id = 2 },
    },
    active_tab = 1,
    focused_id = 1,
    zoomed_pane_id = nil,
})
local tab_infos = t.build_custom_tab_infos()
assert(tab_infos[1].is_zoomed == false, "tab infos: active tab is not zoomed")
assert(tab_infos[2].is_zoomed == true, "tab infos: inactive tab reflects saved zoom state")
