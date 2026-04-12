local prise = require("prise")
local utils = require("utils")

---@class Pane
---@field type "pane"
---@field id number
---@field pty Pty
---@field ratio? number

---@class Split
---@field type "split"
---@field split_id number
---@field direction "row"|"col"
---@field ratio? number
---@field children (Pane|Split)[]

---@alias Node Pane|Split

---@class Tab
---@field id integer
---@field title? string
---@field root? Node
---@field last_focused_id? number
---@field zoomed_pane_id? number Saved zoom state for inactive tabs
---@field floating? FloatingPane Floating pane for this tab (if any)

---@class FloatingPane
---@field pane Pane The actual pane
---@field visible boolean Whether the floating pane is visible

---@class PaletteRegion
---@field start_y number
---@field end_y number
---@field index number

---@class PaletteState
---@field visible boolean
---@field input? TextInput
---@field selected number
---@field scroll_offset number
---@field regions PaletteRegion[]
---@field palette_y number

---@class RenameState
---@field visible boolean
---@field input? TextInput

---@class SwapWithIndexState
---@field visible boolean
---@field input? TextInput
---@field target_index? number

---@class SessionPickerState
---@field visible boolean
---@field input? TextInput
---@field selected number
---@field scroll_offset number
---@field sessions string[]
---@field regions PaletteRegion[]
---@field renaming boolean
---@field rename_target? string

---@class FloatingPaneState
---@field visible boolean Whether the floating pane is visible
---@field width number Width in columns (default: 100)
---@field height number Height in rows (default: 30)
---@field pending boolean Whether a floating pane spawn is pending
---@field resize_mode boolean Whether resize mode is active (shows size indicator)

-- Layout definitions for predefined pane arrangements
---@class PriseLayoutPane
---@field type "pane"
---@field ratio? number Relative size (0-1)
---@field cwd? string Working directory for the shell
---@field cmd? string Command to run after shell starts

---@class PriseLayoutSplit
---@field type "split"
---@field direction "row"|"col"
---@field ratio? number Relative size of this split
---@field children (PriseLayoutPane|PriseLayoutSplit)[]

---@alias PriseLayoutNode PriseLayoutPane|PriseLayoutSplit

---@class PriseLayoutFloating
---@field pane PriseLayoutPane The floating pane definition
---@field visible? boolean Start visible (default: true)
---@field width? number Override default floating width
---@field height? number Override default floating height

---@class PriseLayoutTab
---@field title? string Tab title
---@field root PriseLayoutNode The pane/split tree
---@field floating? PriseLayoutFloating Optional floating pane for this tab

---@class PriseLayout
---@field name string Layout identifier
---@field tabs PriseLayoutTab[] Tabs in the layout
---@field active_tab? integer Which tab to focus (default: 1)

---@class LayoutPickerState
---@field visible boolean
---@field selected number
---@field scroll_offset number
---@field regions PaletteRegion[]

---@class State
---@field tabs Tab[]
---@field active_tab integer
---@field next_tab_id integer
---@field focused_id? number
---@field zoomed_pane_id? number
---@field pending_command boolean
---@field timer? Timer
---@field clock_timer? Timer
---@field pending_split? { direction: "row"|"col" }
---@field pending_new_tab? boolean
---@field next_split_id number
---@field palette PaletteState
---@field rename RenameState
---@field rename_tab RenameState
---@field swap_with_index? SwapWithIndexState
---@field session_picker SessionPickerState
---@field screen_cols number
---@field screen_rows number
---@field keybind_matcher? KeybindMatcher
---@field floating FloatingPaneState
---@field layout_picker LayoutPickerState
---@field pending_layout? table

---@class Command
---@field name string|fun(): string
---@field action fun()
---@field shortcut? string
---@field visible? fun(): boolean

---@class PtyAttachEvent
---@field type "pty_attach"
---@field data { pty: Pty }

---@class PtyExitedEvent
---@field type "pty_exited"
---@field data { id: number }

---@class KeyPressEvent
---@field type "key_press"
---@field data PtyKeyData

---@class KeyReleaseEvent
---@field type "key_release"
---@field data PtyKeyData

---@class PasteEvent
---@field type "paste"
---@field data { text: string }

---@class MouseEvent
---@field type "mouse"
---@field data { action: string, button?: string, x: number, y: number, target?: number, target_x?: number, target_y?: number, mods?: table }

---@class WinsizeEvent
---@field type "winsize"
---@field data { cols: number, rows: number }

---@class FocusInEvent
---@field type "focus_in"
---@field data table

---@class FocusOutEvent
---@field type "focus_out"
---@field data table

---@class SplitResizeEvent
---@field type "split_resize"
---@field data { parent_id: number, child_index: integer, ratio: number }

---@class CwdChangedEvent
---@field type "cwd_changed"
---@field data table

---@alias Event PtyAttachEvent|PtyExitedEvent|KeyPressEvent|KeyReleaseEvent|PasteEvent|MouseEvent|WinsizeEvent|FocusInEvent|FocusOutEvent|SplitResizeEvent|CwdChangedEvent

-- Powerline symbols
local POWERLINE_SYMBOLS = {
    right_solid = "",
    right_thin = "",
    left_solid = "",
    left_thin = "",
    left_round = "\u{E0B6}",
    right_round = "\u{E0B4}",
}

---@class PriseThemeOptions
---@field mode_normal? string Color for normal mode indicator
---@field mode_command? string Color for command mode indicator
---@field bg1? string Darkest background
---@field bg2? string Dark background
---@field bg3? string Medium background
---@field bg4? string Lighter background
---@field fg_bright? string Main text color
---@field fg_dim? string Secondary text color
---@field fg_dark? string Dark text (on light backgrounds)
---@field accent? string Accent color
---@field green? string Success/connected color
---@field yellow? string Warning color

---@class PriseTheme
---@field mode_normal string
---@field mode_command string
---@field bg1 string
---@field bg2 string
---@field bg3 string
---@field bg4 string
---@field fg_bright string
---@field fg_dim string
---@field fg_dark string
---@field accent string
---@field green string
---@field yellow string

---@class PriseStatusBarConfig
---@field enabled? boolean Show the status bar (default: true)

---Tab info passed to custom render function
---@class TabInfo
---@field index number Tab index (1-based)
---@field title string Tab title (explicit title if set, otherwise auto-derived)
---@field is_explicit_title boolean True if this is an explicit renamed title
---@field is_active boolean True if this is the active tab
---@field is_hovered boolean True if mouse is hovering over this tab
---@field is_close_hovered boolean True if mouse is hovering over close button
---@field pane_count number Number of panes in this tab
---@field is_zoomed boolean True if this tab has a zoomed pane

---Custom render function for tab bar
---Must return an array of segments compatible with prise.Text()
---@alias TabRenderFunction fun(tabs: TabInfo[], screen_width: number, theme: PriseTheme): table[]

---Optional function to format tab titles for display
---@alias TabFormatFunction fun(title: string, tab_index: number): string

---@class PriseTabBarConfig
---@field show_single_tab? boolean Show tab bar even with one tab (default: false)
---@field render? TabRenderFunction Custom tab bar renderer (overrides default design)
---@field format_title? TabFormatFunction Optional function to format tab titles (default: no formatting)

---Keybinds are a map from key_string to action name
---Example: ["<leader>v"] = "split_horizontal"
---@alias PriseKeybinds table<string, string|function>

---@class PriseBordersConfig
---@field enabled? boolean Show pane borders (default: false)
---@field show_single_pane? boolean Show border when only one pane exists (default: false)
---@field mode? "box"|"separator" Border mode: "box" for full borders, "separator" for tmux-style (default: "box")
---@field style? "none"|"single"|"double"|"rounded" Border line style (default: "single")
---@field focused_color? string Hex color for focused pane border (default: "#89b4fa")
---@field unfocused_color? string Hex color for unfocused borders (default: "#585b70")

---@class PriseFloatingConfig
---@field width? number Width in columns (default: 100)
---@field height? number Height in rows (default: 30)

---@class PriseConfigOptions
---@field theme? PriseThemeOptions Color theme options
---@field borders? PriseBordersConfig Pane border options
---@field status_bar? PriseStatusBarConfig Status bar options
---@field tab_bar? PriseTabBarConfig Tab bar options
---@field floating? PriseFloatingConfig Floating pane options
---@field leader? string Leader key sequence (default: "<D-k>")
---@field keybinds? PriseKeybinds Keybind definitions
---@field macos_option_as_alt? "false"|"left"|"right"|"true" macOS Option key behavior (default: "false")
---@field layouts? table<string, PriseLayout> Named layout definitions
---@field default_layout? string Layout to apply on startup (if no session exists)

---@class PriseConfig
---@field theme PriseTheme
---@field borders PriseBordersConfig
---@field status_bar PriseStatusBarConfig
---@field tab_bar PriseTabBarConfig
---@field floating PriseFloatingConfig
---@field leader string
---@field keybinds PriseKeybinds
---@field layouts table<string, PriseLayout>
---@field default_layout? string

-- Default configuration
---@type PriseConfig
local config = {
    theme = {
        -- Status bar backgrounds (left to right gradient)
        mode_normal = "#89b4fa", -- Blue - normal mode
        mode_command = "#f38ba8", -- Pink - command mode
        bg1 = "#1e1e2e", -- Darkest (mode section)
        bg2 = "#313244", -- Dark (title section)
        bg3 = "#45475a", -- Medium (info section)
        bg4 = "#585b70", -- Lighter (right sections)

        -- Text colors
        fg_bright = "#cdd6f4", -- Main text
        fg_dim = "#a6adc8", -- Secondary text
        fg_dark = "#1e1e2e", -- Dark text on light bg

        -- Accent colors
        accent = "#89b4fa", -- Blue accent
        green = "#a6e3a1", -- Success/connected
        yellow = "#f9e2af", -- Warning
    },
    borders = {
        enabled = false,
        show_single_pane = false,
        mode = "box", -- "box" for full borders, "separator" for tmux-style
        style = "single",
        focused_color = "#89b4fa", -- Blue (matches default theme.accent)
        unfocused_color = "#585b70", -- Gray (matches default theme.bg4)
    },
    status_bar = {
        enabled = true,
    },
    tab_bar = {
        show_single_tab = false,
        render = nil, -- Use default built-in design
        format_title = nil, -- Use titles as-is
    },
    floating = {
        width = 100,
        height = 30,
    },
    leader = "<D-k>",
    keybinds = {
        ["<D-p>"] = "command_palette",
        ["<leader>v"] = "split_horizontal",
        ["<leader>s"] = "split_vertical",
        ["<leader><Enter>"] = "split_auto",
        ["<leader>h"] = "focus_left",
        ["<leader>l"] = "focus_right",
        ["<leader>j"] = "focus_down",
        ["<leader>k"] = "focus_up",
        ["<leader>w"] = "close_pane",
        ["<leader>z"] = "toggle_zoom",
        ["<leader>t"] = "new_tab",
        ["<leader>c"] = "close_tab",
        ["<leader>r"] = "rename_tab",
        ["<leader>n"] = "next_tab",
        ["<leader>p"] = "previous_tab",
        ["<leader><lt>"] = "swap_tab_left",
        ["<leader><gt>"] = "swap_tab_right",
        ["<leader>d"] = "detach_session",
        ["<leader>S"] = "switch_session",
        ["<leader>q"] = "quit",
        ["<leader>H"] = "resize_left",
        ["<leader>L"] = "resize_right",
        ["<leader>J"] = "resize_down",
        ["<leader>K"] = "resize_up",
        ["<leader>1"] = "tab_1",
        ["<leader>2"] = "tab_2",
        ["<leader>3"] = "tab_3",
        ["<leader>4"] = "tab_4",
        ["<leader>5"] = "tab_5",
        ["<leader>6"] = "tab_6",
        ["<leader>7"] = "tab_7",
        ["<leader>8"] = "tab_8",
        ["<leader>9"] = "tab_9",
        ["<leader>0"] = "tab_10",
        ["<leader>f"] = "floating_toggle",
        ["<leader>+"] = "floating_increase_size",
        ["<leader>-"] = "floating_decrease_size",
        ["<leader>o"] = "layout_picker",
    },
    macos_option_as_alt = "false",
    layouts = {},
    default_layout = nil,
}

local merge_config = utils.merge_config

-- Convenience alias for theme access
local THEME = config.theme

---@type State
local state = {
    tabs = {},
    active_tab = 1,
    next_tab_id = 1,
    focused_id = nil,
    zoomed_pane_id = nil,
    app_focused = true,
    pending_command = false,
    timer = nil,
    clock_timer = nil,
    pending_split = nil,
    pending_new_tab = false,
    next_split_id = 1,
    -- Command palette
    palette = {
        visible = false,
        input = nil, -- TextInput handle
        selected = 1,
        scroll_offset = 0,
        regions = {}, -- Click regions for items
        palette_y = 5, -- Y offset of the palette
    },
    -- Rename session prompt
    rename = {
        visible = false,
        input = nil, -- TextInput handle
    },
    -- Rename tab prompt
    rename_tab = {
        visible = false,
        input = nil, -- TextInput handle
    },
    -- Swap tab with index dialog
    swap_with_index = nil,
    -- Session switcher
    session_picker = {
        visible = false,
        input = nil, -- TextInput handle
        selected = 1,
        scroll_offset = 0,
        sessions = {}, -- List of session names
        regions = {}, -- Click regions for items
        renaming = false, -- Whether we're renaming a session
        rename_target = nil, -- The session being renamed
    },
    -- Tab bar hit regions: array of {start_x, end_x, tab_index}
    tab_regions = {},
    -- Tab close button regions: array of {start_x, end_x, tab_index}
    tab_close_regions = {},
    -- Currently hovered tab index (nil if none)
    hovered_tab = nil,
    -- Currently hovered close button tab index (nil if none)
    hovered_close_tab = nil,
    -- Screen dimensions
    screen_cols = 80,
    screen_rows = 24,
    -- Cached git branch (updated on cwd_changed)
    cached_git_branch = nil,
    -- True when detaching (prevents new timers from being scheduled)
    detaching = false,
    -- Keybind matcher (initialized by init_keybinds)
    keybind_matcher = nil,
    -- Floating pane state
    floating = {
        visible = false,
        width = 100,
        height = 30,
        pending = false,
        resize_mode = false,
    },
    -- Layout picker state
    layout_picker = {
        visible = false,
        selected = 1,
        scroll_offset = 0,
        regions = {},
    },
    -- Pending layout to apply (layout node definitions waiting for PTY spawns)
    pending_layout = nil,
}

local M = {}

---Forward declaration for action_handlers (defined after helper functions)
---@type table<string, fun()>
local action_handlers

---Initialize keybinds by compiling the trie
local function init_keybinds()
    if state.keybind_matcher then
        return
    end
    state.keybind_matcher = prise.keybind.compile(config.keybinds, config.leader)
end

---Configure the default UI
---@param opts? PriseConfigOptions Configuration options to merge
function M.setup(opts)
    if opts then
        merge_config(config, opts)
    end
    -- Apply floating pane config to state
    state.floating.width = config.floating.width
    state.floating.height = config.floating.height
    -- Mark keybinds for re-initialization on next key event
    -- (lazy init because UI pointer may not be available during config loading)
    state.keybind_matcher = nil
end

---Get the macos_option_as_alt setting
---@return string
function M.get_macos_option_as_alt()
    return config.macos_option_as_alt or "false"
end

local RESIZE_STEP = 0.05 -- 5% step for keyboard resize
local PALETTE_WIDTH = 60 -- Total width of command palette
local PALETTE_INNER_WIDTH = 56 -- Inner width (PALETTE_WIDTH - 4 for padding)

-- Floating pane size bounds
local FLOATING_MIN_WIDTH = 40
local FLOATING_MAX_WIDTH = 200
local FLOATING_MIN_HEIGHT = 10
local FLOATING_MAX_HEIGHT = 50
local FLOATING_WIDTH_STEP = 5
local FLOATING_HEIGHT_STEP = 2

-- --- Helpers ---

---Handle common text input key events
---@param input TextInput
---@param key_data PtyKeyData
---@return boolean handled
local function handle_text_input_key(input, key_data)
    local k = key_data.key
    local ctrl = key_data.ctrl

    if k == "Backspace" then
        input:delete_backward()
        prise.request_frame()
        return true
    elseif k == "Delete" then
        input:delete_forward()
        prise.request_frame()
        return true
    elseif k == "w" and ctrl then
        input:delete_word_backward()
        prise.request_frame()
        return true
    elseif k == "k" and ctrl then
        input:kill_line()
        prise.request_frame()
        return true
    elseif k == "ArrowLeft" then
        input:move_left()
        prise.request_frame()
        return true
    elseif k == "ArrowRight" then
        input:move_right()
        prise.request_frame()
        return true
    elseif k == "Home" or (k == "a" and ctrl) then
        input:move_to_start()
        prise.request_frame()
        return true
    elseif k == "End" or (k == "e" and ctrl) then
        input:move_to_end()
        prise.request_frame()
        return true
    elseif #k == 1 and not ctrl and not key_data.alt and not key_data.super then
        input:insert(k)
        prise.request_frame()
        return true
    end

    return false
end

---@param node? table
---@return boolean
local function is_pane(node)
    return node ~= nil and node.type == "pane"
end

---@param node? table
---@return boolean
local function is_split(node)
    return node ~= nil and node.type == "split"
end

---Check if a key is a modifier-only key (Shift, Ctrl, Alt, Super)
---@param key string
---@return boolean
local function is_modifier_key(key)
    return key == "ShiftLeft"
        or key == "ShiftRight"
        or key == "ControlLeft"
        or key == "ControlRight"
        or key == "AltLeft"
        or key == "AltRight"
        or key == "MetaLeft"
        or key == "MetaRight"
end

---Cancel all timers and detach from session
local function detach_session()
    state.detaching = true
    if state.clock_timer then
        state.clock_timer:cancel()
        state.clock_timer = nil
    end
    if state.timer then
        state.timer:cancel()
        state.timer = nil
    end
    prise.detach(prise.get_session_name())
end

---Get the currently active tab
---@return Tab?
local function get_active_tab()
    return state.tabs[state.active_tab]
end

---Get the root node of the active tab
---@return Node?
local function get_active_root()
    local tab = get_active_tab()
    return tab and tab.root or nil
end

---Forward declaration for find_node_path
---@type fun(current: Node?, target_id: number, path: Node[]?): Node[]?
local find_node_path

---Find which tab contains a pane by id
---@param pane_id number
---@return integer?, Tab?
local function find_tab_for_pane(pane_id)
    for i, tab in ipairs(state.tabs) do
        if find_node_path(tab.root, pane_id) then
            return i, tab
        end
    end
    return nil
end

---Collect all panes in a node tree
---@param node? Node
---@param acc? Pane[]
---@return Pane[]
local function collect_panes(node, acc)
    acc = acc or {}
    if not node then
        return acc
    end
    if is_pane(node) then
        table.insert(acc, node)
    elseif is_split(node) then
        for _, child in ipairs(node.children) do
            collect_panes(child, acc)
        end
    end
    return acc
end

---Collect all panes belonging to a tab, including its floating pane.
---@param tab? Tab
---@return Pane[]
local function collect_tab_panes(tab)
    local panes = collect_panes(tab and tab.root or nil, {})
    if tab and tab.floating and tab.floating.pane then
        table.insert(panes, tab.floating.pane)
    end
    return panes
end

---Returns a list of nodes from root to the target node [root, ..., target]
---@param current? Node
---@param target_id number
---@param path? Node[]
---@return Node[]?
find_node_path = function(current, target_id, path)
    path = path or {}
    if not current then
        return nil
    end

    table.insert(path, current)

    if is_pane(current) then
        if current.id == target_id then
            return path
        end
    elseif is_split(current) then
        for _, child in ipairs(current.children) do
            if find_node_path(child, target_id, path) then
                return path
            end
        end
    end

    -- Not found in this branch
    table.remove(path)
    return nil
end

---@param node? Node
---@return Pane?
local function get_first_leaf(node)
    if not node then
        return nil
    end
    if node.type == "pane" then
        ---@cast node Pane
        return node
    end
    if node.type == "split" then
        ---@cast node Split
        return get_first_leaf(node.children[1])
    end
    return nil
end

---@param node? Node
---@return Pane?
local function get_last_leaf(node)
    if not node then
        return nil
    end
    if node.type == "pane" then
        ---@cast node Pane
        return node
    end
    if node.type == "split" then
        ---@cast node Split
        return get_last_leaf(node.children[#node.children])
    end
    return nil
end

---Update the cached git branch for the focused pane
local function update_cached_git_branch()
    local root = get_active_root()
    if state.focused_id and root then
        local path = find_node_path(root, state.focused_id)
        if path then
            local pane = path[#path]
            local cwd = pane.pty:cwd()
            if cwd then
                state.cached_git_branch = prise.get_git_branch(cwd)
                return
            end
        end
    end
    state.cached_git_branch = nil
end

---Recursively insert a new pane relative to target_id
---@param node Node
---@param target_id number
---@param new_pane Pane
---@param direction "row"|"col"
---@return Node
local function insert_split_recursive(node, target_id, new_pane, direction)
    if is_pane(node) then
        if node.id == target_id then
            -- Found the target pane. Replace it with a split containing [node, new_pane]
            local split_ratio = node.ratio -- Inherit ratio from the pane being replaced
            node.ratio = nil -- Children start with nil (equal split)
            new_pane.ratio = nil
            local split_id = state.next_split_id
            state.next_split_id = state.next_split_id + 1
            return {
                type = "split",
                split_id = split_id,
                direction = direction,
                ratio = split_ratio,
                children = { node, new_pane },
            }
        else
            return node
        end
    elseif is_split(node) then
        for i, child in ipairs(node.children) do
            node.children[i] = insert_split_recursive(child, target_id, new_pane, direction)
        end
        return node
    end
    return node
end

---Recursively remove a pane and return: new_node, closest_sibling_id
---@param node Node
---@param id number
---@return Node?, number?
local function remove_pane_recursive(node, id)
    if is_pane(node) then
        if node.id == id then
            return nil, nil
        end -- Remove this pane
        return node, nil
    elseif is_split(node) then
        local new_children = {}
        local removed_index = nil
        local closest_id = nil

        for i, child in ipairs(node.children) do
            local res, sibling_from_below = remove_pane_recursive(child, id)

            if res then
                table.insert(new_children, res)
                if sibling_from_below then
                    closest_id = sibling_from_below
                end
            else
                -- This child was removed
                removed_index = i
                if sibling_from_below then
                    closest_id = sibling_from_below
                end
            end
        end

        -- If we found the removed node at this level, pick a sibling
        if removed_index and not closest_id then
            -- Try right sibling first (if exists), then left
            if removed_index < #node.children then
                local neighbor = node.children[removed_index + 1]
                local leaf = get_first_leaf(neighbor)
                if leaf then
                    closest_id = leaf.id
                end
            elseif removed_index > 1 then
                local neighbor = node.children[removed_index - 1]
                local leaf = get_last_leaf(neighbor)
                if leaf then
                    closest_id = leaf.id
                end
            end
        end

        if #new_children == 0 then
            return nil, closest_id
        end

        -- If only one child remains, promote it
        if #new_children == 1 then
            ---@type Node
            local survivor = new_children[1]
            survivor.ratio = node.ratio -- Inherit ratio from parent
            return survivor, closest_id
        end

        node.children = new_children
        return node, closest_id
    end
    return nil, nil
end

---@return Pty?
local function get_focused_pty()
    local root = get_active_root()
    if not state.focused_id or not root then
        return nil
    end
    local path = find_node_path(root, state.focused_id)
    if path then
        return path[#path].pty
    end
    return nil
end

---Get the floating pane's PTY if visible, nil otherwise
---@return Pty?
local function get_visible_floating_pty()
    local tab = get_active_tab()
    if tab and tab.floating and tab.floating.visible and tab.floating.pane then
        return tab.floating.pane.pty
    end
    return nil
end

local function get_auto_split_direction()
    local pty = get_focused_pty()
    if pty then
        local size = pty:size()
        ---@type boolean
        local wider
        if size.width_px > 0 and size.height_px > 0 then
            wider = size.width_px > size.height_px
        else
            wider = size.cols > size.rows
        end
        if wider then
            return "row"
        else
            return "col"
        end
    end
    return "row"
end

local function update_pty_focus(old_id, new_id)
    if old_id == new_id then
        return
    end
    if old_id then
        local _, old_tab = find_tab_for_pane(old_id)
        if old_tab then
            local old_path = find_node_path(old_tab.root, old_id)
            if old_path then
                old_path[#old_path].pty:set_focus(false)
            end
        end
    end
    if new_id and state.app_focused then
        local _, new_tab = find_tab_for_pane(new_id)
        if new_tab then
            local new_path = find_node_path(new_tab.root, new_id)
            if new_path then
                new_path[#new_path].pty:set_focus(true)
            end
        end
    end
end

---Switch to a different tab by index
---@param new_index integer
local function set_active_tab_index(new_index)
    if new_index < 1 or new_index > #state.tabs then
        return
    end
    if new_index == state.active_tab then
        return
    end

    local old_tab = state.tabs[state.active_tab]
    local old_focused = state.focused_id

    -- Save zoom and focus state to old tab
    if old_tab then
        old_tab.zoomed_pane_id = state.zoomed_pane_id
        old_tab.last_focused_id = state.focused_id
    end

    state.active_tab = new_index
    local new_tab = state.tabs[new_index]
    if not new_tab then
        return
    end

    -- Restore zoom state from new tab (clear saved value; active tab uses state.zoomed_pane_id)
    state.zoomed_pane_id = new_tab.zoomed_pane_id
    new_tab.zoomed_pane_id = nil

    -- Pick new focused pane in this tab
    local new_focus_id = new_tab.last_focused_id
    if not new_focus_id or not find_node_path(new_tab.root, new_focus_id) then
        local first_pane = get_first_leaf(new_tab.root)
        new_focus_id = first_pane and first_pane.id or nil
    end

    state.focused_id = new_focus_id
    update_pty_focus(old_focused, new_focus_id)
    update_cached_git_branch()
    prise.request_frame()
end

---Close the current tab
---Close tab at given index
---@param idx integer
local function close_tab(idx)
    if #state.tabs == 0 then
        return
    end

    local tab = state.tabs[idx]
    if not tab then
        return
    end

    -- If this is the last tab, quit the app
    if #state.tabs == 1 then
        local panes = collect_tab_panes(tab)
        for _, pane in ipairs(panes) do
            if pane.pty and pane.pty.close then
                pane.pty:close()
            end
        end
        -- Cancel clock timer before exit
        if state.clock_timer then
            state.clock_timer:cancel()
            state.clock_timer = nil
        end
        prise.exit()
        return
    end

    -- Close all PTYs in this tab
    local panes = collect_tab_panes(tab)
    for _, pane in ipairs(panes) do
        if pane.pty and pane.pty.close then
            pane.pty:close()
        end
    end

    -- Save zoom to the closing tab so we can detect active-tab changes
    local closing_active = (idx == state.active_tab)
    if closing_active then
        tab.zoomed_pane_id = state.zoomed_pane_id
    end

    local old_focused = state.focused_id
    table.remove(state.tabs, idx)

    -- Pick new active tab index
    if idx > #state.tabs then
        idx = #state.tabs
    end
    state.active_tab = idx > 0 and idx or 1

    local new_tab = state.tabs[state.active_tab]
    if new_tab then
        -- Only restore zoom when we landed on a different tab
        if closing_active then
            state.zoomed_pane_id = new_tab.zoomed_pane_id
            new_tab.zoomed_pane_id = nil
        end

        -- Choose focused pane in new tab
        local new_focus_id = new_tab.last_focused_id
        if not new_focus_id or not find_node_path(new_tab.root, new_focus_id) then
            local first_pane = get_first_leaf(new_tab.root)
            new_focus_id = first_pane and first_pane.id or nil
        end
        state.focused_id = new_focus_id
        update_pty_focus(old_focused, new_focus_id)
        update_cached_git_branch()
    else
        -- No tabs left
        state.focused_id = nil
        state.cached_git_branch = nil
    end

    prise.request_frame()
    prise.save()
end

---Close the current tab
local function close_current_tab()
    close_tab(state.active_tab)
end

---Swap two tabs by their indices
---@param idx1 integer
---@param idx2 integer
local function swap_tabs(idx1, idx2)
    if idx1 < 1 or idx1 > #state.tabs or idx2 < 1 or idx2 > #state.tabs then
        return
    end
    if idx1 == idx2 then
        return
    end

    -- Swap the tabs
    state.tabs[idx1], state.tabs[idx2] = state.tabs[idx2], state.tabs[idx1]

    -- Update active_tab if it was swapped
    if state.active_tab == idx1 then
        state.active_tab = idx2
    elseif state.active_tab == idx2 then
        state.active_tab = idx1
    end

    prise.request_frame()
    prise.save()
end

---Remove a pane by id from the appropriate tab
---@param id number
---@return boolean was_last True if this was the last pane in the last tab (app will quit)
local function remove_pane_by_id(id)
    local tab_idx, tab = find_tab_for_pane(id)
    if not tab then
        return false
    end

    -- Clear zoom if the zoomed pane is being removed
    if state.zoomed_pane_id == id then
        state.zoomed_pane_id = nil
    end
    -- Also clear per-tab saved zoom if it references this pane
    for _, t in ipairs(state.tabs) do
        if t.zoomed_pane_id == id then
            t.zoomed_pane_id = nil
        end
    end

    local new_root, next_focus = remove_pane_recursive(tab.root, id)
    tab.root = new_root

    if not tab.root then
        -- Tab is now empty, remove it
        if #state.tabs == 1 then
            table.remove(state.tabs, tab_idx)
            -- Cancel clock timer before exit
            if state.clock_timer then
                state.clock_timer:cancel()
                state.clock_timer = nil
            end
            prise.exit()
            return true
        else
            local old_focused = state.focused_id
            local was_active = (tab_idx == state.active_tab)
            table.remove(state.tabs, tab_idx)

            -- Adjust active_tab if needed
            if state.active_tab > #state.tabs then
                state.active_tab = #state.tabs
            end
            if state.active_tab < 1 then
                state.active_tab = 1
            end

            -- Update focus to new active tab
            local new_tab = state.tabs[state.active_tab]
            if new_tab then
                -- Restore zoom from new tab when the collapsed tab was active
                if was_active then
                    state.zoomed_pane_id = new_tab.zoomed_pane_id
                    new_tab.zoomed_pane_id = nil
                end

                local new_focus_id = new_tab.last_focused_id
                if not new_focus_id or not find_node_path(new_tab.root, new_focus_id) then
                    local first_pane = get_first_leaf(new_tab.root)
                    new_focus_id = first_pane and first_pane.id or nil
                end
                state.focused_id = new_focus_id
                update_pty_focus(old_focused, new_focus_id)
                update_cached_git_branch()
            end
            prise.request_frame()
            return false
        end
    else
        -- Tab still has panes
        if state.focused_id == id then
            local old_id = state.focused_id
            if next_focus then
                state.focused_id = next_focus
            else
                local first = get_first_leaf(tab.root)
                if first then
                    state.focused_id = first.id
                end
            end
            update_pty_focus(old_id, state.focused_id)
            update_cached_git_branch()
        end
        prise.request_frame()
        return false
    end
end

---Count all panes in the tree
---@param node? Node
---@return number
local function count_panes(node)
    if not node then
        return 0
    end
    if is_pane(node) then
        return 1
    end
    if is_split(node) then
        local count = 0
        for _, child in ipairs(node.children) do
            count = count + count_panes(child)
        end
        return count
    end
    return 0
end

---Count all panes belonging to a tab, including its floating pane.
---@param tab? Tab
---@return number
local function count_tab_panes(tab)
    local n = count_panes(tab and tab.root or nil)
    if tab and tab.floating and tab.floating.pane then
        n = n + 1
    end
    return n
end

---Determine if borders should be shown for the active tab
---@return boolean
local function should_show_borders()
    if not config.borders.enabled then
        return false
    end
    if config.borders.show_single_pane then
        return true
    end
    local root = get_active_root()
    return count_panes(root) > 1
end

---Check if a node subtree contains the focused pane
---@param node Node
---@return boolean
local function contains_focused(node)
    if not node or not state.focused_id then
        return false
    end
    if is_pane(node) then
        return node.id == state.focused_id
    elseif is_split(node) then
        for _, child in ipairs(node.children) do
            if contains_focused(child) then
                return true
            end
        end
    end
    return false
end

---Serialize a node tree to a table with pty_ids instead of userdata
---@param node? Node
---@param cwd_lookup? fun(pty_id: number): string?
---@return table?
local function serialize_node(node, cwd_lookup)
    if not node then
        return nil
    end
    if is_pane(node) then
        local pty_id = node.pty:id()
        ---@type string?
        local cwd = nil
        if cwd_lookup then
            cwd = cwd_lookup(pty_id)
        end
        return {
            type = "pane",
            id = node.id,
            pty_id = pty_id,
            cwd = cwd,
            ratio = node.ratio,
        }
    elseif is_split(node) then
        local children = {}
        for _, child in ipairs(node.children) do
            table.insert(children, serialize_node(child, cwd_lookup))
        end
        return {
            type = "split",
            split_id = node.split_id,
            direction = node.direction,
            ratio = node.ratio,
            children = children,
        }
    end
    return nil
end

---Deserialize a node tree, looking up PTYs by id
---@param saved? table
---@param pty_lookup fun(id: number): Pty?
---@return Node?
local function deserialize_node(saved, pty_lookup)
    if not saved then
        return nil
    end
    if saved.type == "pane" then
        local pty = pty_lookup(saved.pty_id)
        if not pty then
            return nil
        end
        return {
            type = "pane",
            id = saved.id,
            pty = pty,
            cwd = saved.cwd, -- Store cwd for spawn fallback
            ratio = saved.ratio,
        }
    elseif saved.type == "split" then
        ---@type Node[]
        local children = {}
        for _, child in ipairs(saved.children) do
            local restored = deserialize_node(child, pty_lookup)
            if restored then
                table.insert(children, restored)
            end
        end
        if #children == 0 then
            return nil
        elseif #children == 1 then
            ---@type Node
            local survivor = children[1]
            survivor.ratio = saved.ratio
            return survivor
        end
        return {
            type = "split",
            split_id = saved.split_id,
            direction = saved.direction,
            ratio = saved.ratio,
            children = children,
        }
    end
    return nil
end

---Serialize a floating pane for session persistence
---@param floating? FloatingPane
---@param cwd_lookup? fun(pty_id: number): string?
---@return table?
local function serialize_floating(floating, cwd_lookup)
    if not floating or not floating.pane or not floating.pane.pty then
        return nil
    end
    local pty_id = floating.pane.pty:id()
    ---@type string?
    local cwd = nil
    if cwd_lookup then
        cwd = cwd_lookup(pty_id)
    end
    return {
        pane = {
            type = "pane",
            id = floating.pane.id,
            pty_id = pty_id,
            cwd = cwd,
        },
        visible = floating.visible,
    }
end

---Deserialize a floating pane, looking up PTY by id
---@param saved? table
---@param pty_lookup fun(id: number): Pty?
---@return FloatingPane?
local function deserialize_floating(saved, pty_lookup)
    if not saved or not saved.pane then
        return nil
    end
    local pty = pty_lookup(saved.pane.pty_id)
    if not pty then
        return nil
    end
    return {
        pane = {
            type = "pane",
            id = saved.pane.id,
            pty = pty,
        },
        visible = saved.visible or false,
    }
end

-- --- Layout Functions ---

---Count the number of panes in a layout node definition
---@param node PriseLayoutNode
---@return number
local function count_layout_panes(node)
    if node.type == "pane" then
        return 1
    elseif node.type == "split" then
        local count = 0
        for _, child in ipairs(node.children) do
            count = count + count_layout_panes(child)
        end
        return count
    end
    return 0
end

---Count total panes in a layout (all tabs + floating panes)
---@param layout PriseLayout
---@return number
local function count_layout_total_panes(layout)
    local count = 0
    for _, tab in ipairs(layout.tabs) do
        count = count + count_layout_panes(tab.root)
        if tab.floating and tab.floating.pane then
            count = count + 1
        end
    end
    return count
end

---Build a pending layout structure from a layout definition
---@param layout PriseLayout
---@return table pending_layout
local function build_pending_layout(layout)
    local pending = {
        layout = layout,
        panes_needed = count_layout_total_panes(layout),
        panes_received = 0,
        pty_queue = {},
        tabs_built = {},
    }
    return pending
end

---Assign a PTY to the next slot in a layout node, building the tree
---@param node PriseLayoutNode
---@param pty_queue Pty[]
---@param queue_idx number
---@return Node?, number new_queue_idx, string? error
local function build_node_from_layout(node, pty_queue, queue_idx)
    assert(node and type(node) == "table", "build_node_from_layout: node must be a table")
    assert(
        node.type == "pane" or node.type == "split",
        "build_node_from_layout: invalid node.type: " .. tostring(node.type)
    )

    if node.type == "pane" then
        local pty = pty_queue[queue_idx]
        if not pty then
            return nil, queue_idx, "no_pty_for_pane"
        end
        ---@type Pane
        local pane = {
            type = "pane",
            id = pty:id(),
            pty = pty,
            ratio = node.ratio,
        }
        return pane, queue_idx + 1, nil
    end

    -- node.type == "split"
    ---@type Node[]
    local children = {}
    local idx = queue_idx
    for _, child_def in ipairs(node.children or {}) do
        local child_node, new_idx, err = build_node_from_layout(child_def, pty_queue, idx)
        if not child_node then
            return nil, idx, err or "failed_child"
        end
        table.insert(children, child_node)
        idx = new_idx
    end
    if #children == 0 then
        return nil, idx, "empty_split"
    elseif #children == 1 then
        children[1].ratio = node.ratio
        return children[1], idx, nil
    end
    local split_id = state.next_split_id
    state.next_split_id = state.next_split_id + 1
    ---@type Split
    local split = {
        type = "split",
        split_id = split_id,
        direction = node.direction,
        ratio = node.ratio,
        children = children,
    }
    return split, idx, nil
end

---Close newly spawned PTYs on layout failure
---@param pty_queue Pty[]
---@param start_idx number
local function cleanup_new_ptys(pty_queue, start_idx)
    for i = start_idx, #pty_queue do
        local pty = pty_queue[i]
        if pty and pty.close then
            pty:close()
        end
    end
end

---Build a floating pane from layout definition
---@param floating_def table The floating definition from layout
---@param pty Pty The PTY for the floating pane
---@return FloatingPane
local function build_floating_pane(floating_def, pty)
    return {
        pane = {
            type = "pane",
            id = pty:id(),
            pty = pty,
        },
        visible = floating_def.visible ~= false,
    }
end

---Close all PTYs in existing tabs
---@param tabs Tab[]
local function close_old_tabs(tabs)
    for _, tab in ipairs(tabs) do
        local panes = collect_panes(tab.root, {})
        for _, pane in ipairs(panes) do
            if pane.pty and pane.pty.close then
                pane.pty:close()
            end
        end
        if tab.floating and tab.floating.pane and tab.floating.pane.pty and tab.floating.pane.pty.close then
            tab.floating.pane.pty:close()
        end
    end
end

---Build tabs from layout definition
---@param layout PriseLayout
---@param pty_queue Pty[]
---@return Tab[]? new_tabs
---@return number? new_floating_width
---@return number? new_floating_height
---@return boolean? new_floating_visible
---@return number queue_idx Final queue index (for verification)
local function build_tabs_from_layout(layout, pty_queue)
    local new_tabs = {}
    local queue_idx = 1
    local new_floating_width = state.floating.width
    local new_floating_height = state.floating.height
    local new_floating_visible = state.floating.visible

    for tab_index, tab_def in ipairs(layout.tabs) do
        local root, new_idx, err = build_node_from_layout(tab_def.root, pty_queue, queue_idx)
        if not root then
            prise.log.error(string.format("Layout: failed to build tab %d: %s", tab_index, err or "unknown"))
            cleanup_new_ptys(pty_queue, queue_idx)
            return nil, nil, nil, nil, queue_idx
        end
        queue_idx = new_idx

        ---@type Tab
        local tab = {
            id = tab_index,
            title = tab_def.title,
            root = root,
            last_focused_id = root and (is_pane(root) and root.id or get_first_leaf(root) and get_first_leaf(root).id),
        }

        if tab_def.floating and tab_def.floating.pane then
            local floating_pty = pty_queue[queue_idx]
            if floating_pty then
                queue_idx = queue_idx + 1
                tab.floating = build_floating_pane(tab_def.floating, floating_pty)
                new_floating_width = tab_def.floating.width or new_floating_width
                new_floating_height = tab_def.floating.height or new_floating_height
                new_floating_visible = tab.floating.visible
            end
        end

        table.insert(new_tabs, tab)
    end

    return new_tabs, new_floating_width, new_floating_height, new_floating_visible, queue_idx
end

---Finalize a pending layout once all PTYs have been received
---@param pending table
local function finalize_layout(pending)
    assert(pending and pending.layout, "finalize_layout: missing pending layout")
    local layout = pending.layout
    local pty_queue = pending.pty_queue or {}

    -- Validate pane count before any destructive operations
    local needed = count_layout_total_panes(layout)
    if needed ~= #pty_queue then
        prise.log.error(string.format("Layout: pane count mismatch (layout=%d, ptys=%d)", needed, #pty_queue))
        cleanup_new_ptys(pty_queue, 1)
        state.pending_layout = nil
        return
    end

    -- Build new tab structures in memory first (before destroying old state)
    local new_tabs, new_floating_width, new_floating_height, new_floating_visible, queue_idx =
        build_tabs_from_layout(layout, pty_queue)
    if not new_tabs then
        state.pending_layout = nil
        return
    end

    -- Verify we used all PTYs
    if queue_idx ~= #pty_queue + 1 then
        prise.log.error(string.format("Layout: PTY mismatch after build (used %d of %d)", queue_idx - 1, #pty_queue))
        cleanup_new_ptys(pty_queue, queue_idx)
        state.pending_layout = nil
        return
    end

    -- Build succeeded - now safely close old PTYs/tabs
    close_old_tabs(state.tabs)

    -- Swap in new state
    state.tabs = new_tabs
    state.next_tab_id = #new_tabs + 1
    state.floating.width = new_floating_width
    state.floating.height = new_floating_height
    state.floating.visible = new_floating_visible
    state.zoomed_pane_id = nil

    -- Set active tab
    state.active_tab = layout.active_tab or 1
    if state.active_tab > #state.tabs then
        state.active_tab = #state.tabs
    end
    if state.active_tab < 1 and #state.tabs > 0 then
        state.active_tab = 1
    end

    -- Set focus to first pane in active tab
    local active_tab = state.tabs[state.active_tab]
    if active_tab and active_tab.root then
        local first = get_first_leaf(active_tab.root)
        if first then
            state.focused_id = first.id
            update_pty_focus(nil, state.focused_id)
        end
    end

    state.pending_layout = nil
    prise.request_frame()
    prise.save()
end

---Expand ~ to home directory in a path and validate it exists
---@param path? string
---@return string?
local function expand_path(path)
    if path == nil then
        return nil
    end
    assert(type(path) == "string", "expand_path: path must be a string")

    local expanded = path
    if path:sub(1, 1) == "~" then
        local home = os.getenv("HOME")
        if home then
            expanded = home .. path:sub(2)
        else
            prise.log.warn("Layout: HOME not set, cannot expand ~")
            return nil
        end
    end

    -- Check if path exists as a file
    local f = io.open(expanded, "r")
    if f then
        f:close()
        return expanded
    end

    -- Check if it's a directory without using shell (avoids command injection)
    local dir_probe = io.open(expanded .. "/.", "r")
    if dir_probe then
        dir_probe:close()
        return expanded
    end

    prise.log.warn("Layout: cwd does not exist: " .. expanded)
    return nil
end

---Spawn PTYs for a layout node recursively
---@param node PriseLayoutNode
local function spawn_for_layout_node(node)
    assert(node and type(node) == "table", "spawn_for_layout_node: node must be a table")
    if node.type == "pane" then
        prise.spawn({ cwd = expand_path(node.cwd), cmd = node.cmd })
    elseif node.type == "split" then
        for _, child in ipairs(node.children or {}) do
            spawn_for_layout_node(child)
        end
    end
end

---Apply a layout by name
---@param layout_name string
---@return boolean
local function apply_layout(layout_name)
    assert(type(layout_name) == "string", "apply_layout: layout_name must be a string")

    if state.pending_layout then
        prise.log.warn("Layout already pending")
        return false
    end

    local layout = config.layouts[layout_name]
    if not layout then
        prise.log.warn("Layout not found: " .. layout_name)
        return false
    end

    -- Validate layout structure
    assert(type(layout) == "table", "Layout must be a table")
    assert(layout.tabs and #layout.tabs > 0, "Layout must have at least one tab")

    local total_panes = count_layout_total_panes(layout)
    if total_panes == 0 then
        prise.log.warn("Layout has no panes: " .. layout_name)
        return false
    end

    -- Build pending layout state
    state.pending_layout = build_pending_layout(layout)

    -- Spawn all needed PTYs
    for _, tab_def in ipairs(layout.tabs) do
        assert(tab_def.root, "Layout tab must have a root node")
        spawn_for_layout_node(tab_def.root)

        -- Spawn floating pane if defined
        if tab_def.floating and tab_def.floating.pane then
            prise.spawn({
                cwd = expand_path(tab_def.floating.pane.cwd),
                cmd = tab_def.floating.pane.cmd,
            })
        end
    end

    return true
end

---Get sorted list of layout names
---@return string[]
local function get_layout_names()
    local names = {}
    for name, _ in pairs(config.layouts) do
        table.insert(names, name)
    end
    table.sort(names)
    return names
end

---Open the layout picker
local function open_layout_picker()
    local names = get_layout_names()
    if #names == 0 then
        prise.log.warn("No layouts defined")
        return
    end
    state.layout_picker.visible = true
    state.layout_picker.selected = 1
    state.layout_picker.scroll_offset = 0
    state.layout_picker.regions = {}
    prise.request_frame()
end

---Close the layout picker
local function close_layout_picker()
    state.layout_picker.visible = false
    prise.request_frame()
end

---Execute the selected layout
local function execute_selected_layout()
    local names = get_layout_names()
    local idx = state.layout_picker.selected
    if idx >= 1 and idx <= #names then
        local layout_name = names[idx]
        close_layout_picker()
        apply_layout(layout_name)
    end
end

local MIN_PANE_SHARE = 0.05

---Get the effective ratio for a child in a split
---@param split Split
---@param idx number
---@return number
local function effective_ratio(split, idx)
    local n = #split.children
    return split.children[idx].ratio or (1.0 / n)
end

---Adjust the ratios of two adjacent siblings, keeping their combined share constant
---@param split Split
---@param left_idx number
---@param right_idx number
---@param delta number Amount to add to left child (negative grows right child)
local function adjust_pair(split, left_idx, right_idx, delta)
    local left_r = effective_ratio(split, left_idx)
    local right_r = effective_ratio(split, right_idx)
    local total = left_r + right_r

    if total < 2 * MIN_PANE_SHARE then
        return
    end

    local new_left = left_r + delta

    -- Clamp so each keeps at least MIN_PANE_SHARE
    if new_left < MIN_PANE_SHARE then
        new_left = MIN_PANE_SHARE
    end
    if new_left > total - MIN_PANE_SHARE then
        new_left = total - MIN_PANE_SHARE
    end

    local new_right = total - new_left

    split.children[left_idx].ratio = new_left
    split.children[right_idx].ratio = new_right
end

---@param dimension "width"|"height"
---@param delta_ratio number
local function resize_pane(dimension, delta_ratio)
    local root = get_active_root()
    if not state.focused_id or not root then
        return
    end

    local path = find_node_path(root, state.focused_id)
    if not path then
        return
    end

    local target_split_dir = (dimension == "width") and "row" or "col"

    -- Traverse up to find a split of the correct direction
    local parent_split = nil
    local child_idx = nil

    for i = #path - 1, 1, -1 do
        if path[i].type == "split" and path[i].direction == target_split_dir then
            parent_split = path[i]
            local node = path[i + 1]

            -- Find index of current node in parent's children
            for k, c in ipairs(parent_split.children) do
                if c == node then
                    child_idx = k
                    break
                end
            end
            break
        end
    end

    if not parent_split or not child_idx then
        return
    end

    local num_children = #parent_split.children

    -- Use pairwise adjustment to enable resizing in both directions from any focused pane.
    if delta_ratio < 0 then
        -- Negative delta: try to move left boundary (focused pane may grow by taking from left)
        if child_idx > 1 then
            -- Left neighbor exists: adjust the left boundary (left shrinks, focused grows)
            adjust_pair(parent_split, child_idx - 1, child_idx, delta_ratio) -- delta_ratio is negative
        elseif child_idx < num_children then
            -- No left neighbor but right exists: adjust right boundary to achieve opposite effect (focused shrinks, right grows)
            -- Since we want a "left resize" effect but no left neighbor, we make focused shrink instead
            adjust_pair(parent_split, child_idx, child_idx + 1, delta_ratio) -- delta_ratio is negative, so focused shrinks, right grows
        else
            return
        end
    else
        -- Positive delta: try to move right boundary (focused pane may grow by taking from right)
        if child_idx < num_children then
            -- Right neighbor exists: adjust the right boundary (focused grows, right shrinks)
            adjust_pair(parent_split, child_idx, child_idx + 1, delta_ratio) -- delta_ratio is positive
        elseif child_idx > 1 then
            -- No right neighbor but left exists: adjust left boundary to achieve opposite effect (left shrinks, focused grows)
            -- Since we want a "right resize" effect but no right neighbor, we make left shrink to grow focused
            adjust_pair(parent_split, child_idx - 1, child_idx, delta_ratio) -- delta_ratio is positive, so left shrinks, focused grows
        else
            return
        end
    end

    prise.request_frame()
    prise.save()
end

---@param direction "left"|"right"|"up"|"down"
local function move_focus(direction)
    local root = get_active_root()
    if not state.focused_id or not root then
        return
    end

    local path = find_node_path(root, state.focused_id)
    if not path then
        return
    end

    -- "left"/"right" implies moving along "horizontal"
    -- "up"/"down" implies moving along "vertical"
    local target_split_type = (direction == "left" or direction == "right") and "row" or "col"
    local forward = (direction == "right" or direction == "down")

    local sibling_node = nil

    -- Traverse up the path to find a split of the correct type where we can move
    -- path is [root, ..., parent, leaf]
    for i = #path - 1, 1, -1 do
        local node = path[i]
        local child = path[i + 1]

        if node.type == "split" and node.direction == target_split_type then
            -- Find index of child
            local idx = 0
            for k, c in ipairs(node.children) do
                if c == child then
                    idx = k
                    break
                end
            end

            if forward then
                if idx < #node.children then
                    sibling_node = node.children[idx + 1]
                    break
                end
            else
                if idx > 1 then
                    sibling_node = node.children[idx - 1]
                    break
                end
            end
        end
    end

    if sibling_node then
        -- Found a sibling tree/pane. Find the closest leaf.
        ---@type Pane?
        local target_leaf
        if forward then
            target_leaf = get_first_leaf(sibling_node)
        else
            target_leaf = get_last_leaf(sibling_node)
        end

        if target_leaf and target_leaf.id ~= state.focused_id then
            local old_id = state.focused_id
            state.focused_id = target_leaf.id
            update_pty_focus(old_id, state.focused_id)
            update_cached_git_branch()
            prise.request_frame()
        end
    end
end

local function get_tab_display_name(tab_index)
    local tab = state.tabs[tab_index]
    if not tab then
        return "Tab " .. tab_index
    end
    if tab.title and tab.title ~= "" then
        return tab.title
    end
    return "Tab " .. tab_index
end

local function open_rename_tab()
    if not state.rename_tab.input then
        state.rename_tab.input = prise.create_text_input()
    end
    local tab = get_active_tab()
    local current_title = (tab and tab.title) or ""
    state.rename_tab.input:clear()
    state.rename_tab.input:insert(current_title)
    state.rename_tab.visible = true
    prise.request_frame()
end

local function close_rename_tab()
    state.rename_tab.visible = false
    prise.request_frame()
end

local function execute_rename_tab()
    if not state.rename_tab.input then
        return
    end
    local new_title = state.rename_tab.input:text()
    local tab = get_active_tab()
    if tab then
        -- If empty, clear title to show index number
        if new_title == "" then
            tab.title = nil
        else
            tab.title = new_title
        end
        prise.save() -- Auto-save on tab renamed
    end
    close_rename_tab()
end

-- Platform-dependent key prefix for shortcuts
local key_prefix = prise.platform == "macos" and "󰘳 +k" or "Super+k"

---Forward declaration for open_rename
---@type fun()
local open_rename

---Forward declaration for open_session_picker
---@type fun()
local open_session_picker

---Command palette commands
---@type Command[]
local commands = {
    {
        name = "Split Horizontal",
        shortcut = key_prefix .. " v",
        action = function()
            local pty = get_focused_pty()
            state.pending_split = { direction = "row" }
            prise.spawn({ cwd = pty and pty:cwd() })
        end,
    },
    {
        name = "Split Vertical",
        shortcut = key_prefix .. " s",
        action = function()
            local pty = get_focused_pty()
            state.pending_split = { direction = "col" }
            prise.spawn({ cwd = pty and pty:cwd() })
        end,
    },
    {
        name = "Split Auto",
        shortcut = key_prefix .. " Enter",
        action = function()
            local pty = get_focused_pty()
            state.pending_split = { direction = get_auto_split_direction() }
            prise.spawn({ cwd = pty and pty:cwd() })
        end,
    },
    {
        name = "Focus Left",
        shortcut = key_prefix .. " h",
        action = function()
            move_focus("left")
        end,
    },
    {
        name = "Focus Right",
        shortcut = key_prefix .. " l",
        action = function()
            move_focus("right")
        end,
    },
    {
        name = "Focus Up",
        shortcut = key_prefix .. " k",
        action = function()
            move_focus("up")
        end,
    },
    {
        name = "Focus Down",
        shortcut = key_prefix .. " j",
        action = function()
            move_focus("down")
        end,
    },
    {
        name = "Close Pane",
        shortcut = key_prefix .. " w",
        action = function()
            local root = get_active_root()
            local path = state.focused_id and find_node_path(root, state.focused_id)
            if path then
                local pane = path[#path]
                pane.pty:close()
                local was_last = remove_pane_by_id(pane.id)
                if not was_last then
                    prise.save()
                end
            end
        end,
    },
    {
        name = "Toggle Zoom",
        shortcut = key_prefix .. " z",
        action = function()
            if state.zoomed_pane_id then
                state.zoomed_pane_id = nil
            elseif state.focused_id then
                state.zoomed_pane_id = state.focused_id
            end
            prise.request_frame()
        end,
    },
    {
        name = "New Tab",
        shortcut = key_prefix .. " t",
        action = function()
            local pty = get_focused_pty()
            state.pending_new_tab = true
            prise.spawn({ cwd = pty and pty:cwd() })
        end,
    },
    {
        name = "Close Tab",
        shortcut = key_prefix .. " c",
        action = function()
            close_current_tab()
        end,
    },
    {
        name = "Rename Tab",
        shortcut = key_prefix .. " r",
        action = function()
            open_rename_tab()
        end,
    },
    {
        name = "Next Tab",
        shortcut = key_prefix .. " n",
        action = function()
            if #state.tabs > 1 then
                local next_idx = state.active_tab % #state.tabs + 1
                set_active_tab_index(next_idx)
            end
        end,
    },
    {
        name = "Previous Tab",
        shortcut = key_prefix .. " p",
        action = function()
            if #state.tabs > 1 then
                local prev_idx = (state.active_tab - 2 + #state.tabs) % #state.tabs + 1
                set_active_tab_index(prev_idx)
            end
        end,
    },
    {
        name = "Swap Tab Left",
        action = function()
            if state.active_tab > 1 then
                swap_tabs(state.active_tab, state.active_tab - 1)
            end
        end,
        visible = function()
            return #state.tabs > 1
        end,
    },
    {
        name = "Swap Tab Right",
        action = function()
            if state.active_tab < #state.tabs then
                swap_tabs(state.active_tab, state.active_tab + 1)
            end
        end,
        visible = function()
            return #state.tabs > 1
        end,
    },
    {
        name = "Swap Tab with Index",
        action = function()
            if #state.tabs <= 1 then
                return
            end
            -- Open a text input for the user to specify the tab index
            state.swap_with_index = {
                visible = true,
                input = prise.create_text_input(),
                target_index = nil,
            }
            prise.request_frame()
        end,
        visible = function()
            return #state.tabs > 1
        end,
    },
    {
        name = "Detach Session",
        shortcut = key_prefix .. " d",
        action = function()
            detach_session()
        end,
    },
    {
        name = "Rename Session",
        action = function()
            open_rename()
        end,
    },
    {
        name = "Switch Session",
        shortcut = key_prefix .. " S",
        action = function()
            open_session_picker()
        end,
    },
    {
        name = "Layouts",
        shortcut = key_prefix .. " o",
        action = function()
            open_layout_picker()
        end,
        visible = function()
            return next(config.layouts) ~= nil
        end,
    },
    {
        name = "Quit",
        shortcut = key_prefix .. " q",
        action = function()
            detach_session()
        end,
    },
    {
        name = "Resize Left",
        shortcut = key_prefix .. " H",
        action = function()
            resize_pane("width", -RESIZE_STEP)
        end,
    },
    {
        name = "Resize Right",
        shortcut = key_prefix .. " L",
        action = function()
            resize_pane("width", RESIZE_STEP)
        end,
    },
    {
        name = "Resize Up",
        shortcut = key_prefix .. " K",
        action = function()
            resize_pane("height", -RESIZE_STEP)
        end,
    },
    {
        name = "Resize Down",
        shortcut = key_prefix .. " J",
        action = function()
            resize_pane("height", RESIZE_STEP)
        end,
    },
    {
        name = function()
            return get_tab_display_name(1)
        end,
        shortcut = key_prefix .. " 1",
        action = function()
            set_active_tab_index(1)
        end,
        visible = function()
            return #state.tabs >= 1
        end,
    },
    {
        name = function()
            return get_tab_display_name(2)
        end,
        shortcut = key_prefix .. " 2",
        action = function()
            set_active_tab_index(2)
        end,
        visible = function()
            return #state.tabs >= 2
        end,
    },
    {
        name = function()
            return get_tab_display_name(3)
        end,
        shortcut = key_prefix .. " 3",
        action = function()
            set_active_tab_index(3)
        end,
        visible = function()
            return #state.tabs >= 3
        end,
    },
    {
        name = function()
            return get_tab_display_name(4)
        end,
        shortcut = key_prefix .. " 4",
        action = function()
            set_active_tab_index(4)
        end,
        visible = function()
            return #state.tabs >= 4
        end,
    },
    {
        name = function()
            return get_tab_display_name(5)
        end,
        shortcut = key_prefix .. " 5",
        action = function()
            set_active_tab_index(5)
        end,
        visible = function()
            return #state.tabs >= 5
        end,
    },
    {
        name = function()
            return get_tab_display_name(6)
        end,
        shortcut = key_prefix .. " 6",
        action = function()
            set_active_tab_index(6)
        end,
        visible = function()
            return #state.tabs >= 6
        end,
    },
    {
        name = function()
            return get_tab_display_name(7)
        end,
        shortcut = key_prefix .. " 7",
        action = function()
            set_active_tab_index(7)
        end,
        visible = function()
            return #state.tabs >= 7
        end,
    },
    {
        name = function()
            return get_tab_display_name(8)
        end,
        shortcut = key_prefix .. " 8",
        action = function()
            set_active_tab_index(8)
        end,
        visible = function()
            return #state.tabs >= 8
        end,
    },
    {
        name = "Tab 9",
        shortcut = key_prefix .. " 9",
        action = function()
            set_active_tab_index(9)
        end,
        visible = function()
            return #state.tabs >= 9
        end,
    },
    {
        name = "Tab 10",
        shortcut = key_prefix .. " 0",
        action = function()
            set_active_tab_index(10)
        end,
        visible = function()
            return #state.tabs >= 10
        end,
    },
    {
        name = "Toggle Floating Pane",
        shortcut = key_prefix .. " f",
        action = function()
            action_handlers.floating_toggle()
        end,
    },
}

-- Action handlers for keybind system
-- Maps action names (from Action enum) to handler functions
action_handlers = {
    split_horizontal = function()
        local pty = get_focused_pty()
        state.pending_split = { direction = "row" }
        prise.spawn({ cwd = pty and pty:cwd() })
    end,
    split_vertical = function()
        local pty = get_focused_pty()
        state.pending_split = { direction = "col" }
        prise.spawn({ cwd = pty and pty:cwd() })
    end,
    split_auto = function()
        local pty = get_focused_pty()
        state.pending_split = { direction = get_auto_split_direction() }
        prise.spawn({ cwd = pty and pty:cwd() })
    end,
    focus_left = function()
        move_focus("left")
    end,
    focus_right = function()
        move_focus("right")
    end,
    focus_up = function()
        move_focus("up")
    end,
    focus_down = function()
        move_focus("down")
    end,
    close_pane = function()
        local root = get_active_root()
        local path = state.focused_id and find_node_path(root, state.focused_id)
        if path then
            local pane = path[#path]
            pane.pty:close()
            local was_last = remove_pane_by_id(pane.id)
            if not was_last then
                prise.save()
            end
        end
    end,
    toggle_zoom = function()
        if state.zoomed_pane_id then
            state.zoomed_pane_id = nil
        elseif state.focused_id then
            state.zoomed_pane_id = state.focused_id
        end
        prise.request_frame()
    end,
    new_tab = function()
        local pty = get_focused_pty()
        state.pending_new_tab = true
        prise.spawn({ cwd = pty and pty:cwd() })
    end,
    close_tab = function()
        close_current_tab()
    end,
    rename_tab = function()
        open_rename_tab()
    end,
    next_tab = function()
        if #state.tabs > 1 then
            local next_idx = state.active_tab % #state.tabs + 1
            set_active_tab_index(next_idx)
        end
    end,
    previous_tab = function()
        if #state.tabs > 1 then
            local prev_idx = (state.active_tab - 2 + #state.tabs) % #state.tabs + 1
            set_active_tab_index(prev_idx)
        end
    end,
    swap_tab_left = function()
        if state.active_tab > 1 then
            swap_tabs(state.active_tab, state.active_tab - 1)
        end
    end,
    swap_tab_right = function()
        if state.active_tab < #state.tabs then
            swap_tabs(state.active_tab, state.active_tab + 1)
        end
    end,
    tab_1 = function()
        set_active_tab_index(1)
    end,
    tab_2 = function()
        set_active_tab_index(2)
    end,
    tab_3 = function()
        set_active_tab_index(3)
    end,
    tab_4 = function()
        set_active_tab_index(4)
    end,
    tab_5 = function()
        set_active_tab_index(5)
    end,
    tab_6 = function()
        set_active_tab_index(6)
    end,
    tab_7 = function()
        set_active_tab_index(7)
    end,
    tab_8 = function()
        set_active_tab_index(8)
    end,
    tab_9 = function()
        set_active_tab_index(9)
    end,
    tab_10 = function()
        set_active_tab_index(10)
    end,
    resize_left = function()
        resize_pane("width", -RESIZE_STEP)
    end,
    resize_right = function()
        resize_pane("width", RESIZE_STEP)
    end,
    resize_up = function()
        resize_pane("height", -RESIZE_STEP)
    end,
    resize_down = function()
        resize_pane("height", RESIZE_STEP)
    end,
    detach_session = function()
        detach_session()
    end,
    rename_session = function()
        open_rename()
    end,
    switch_session = function()
        open_session_picker()
    end,
    quit = function()
        detach_session()
    end,
    floating_toggle = function()
        local tab = get_active_tab()
        if not tab then
            return
        end

        if not tab.floating then
            -- No floating pane created yet, spawn one
            state.floating.pending = true
            local pty = get_focused_pty()
            prise.spawn({ cwd = pty and pty:cwd() })
        else
            -- Toggle visibility
            tab.floating.visible = not tab.floating.visible
            prise.request_frame()
        end
    end,
    floating_increase_size = function()
        state.floating.width = math.min(state.floating.width + FLOATING_WIDTH_STEP, FLOATING_MAX_WIDTH)
        state.floating.height = math.min(state.floating.height + FLOATING_HEIGHT_STEP, FLOATING_MAX_HEIGHT)
        state.floating.resize_mode = true
        prise.request_frame()
    end,
    floating_decrease_size = function()
        state.floating.width = math.max(state.floating.width - FLOATING_WIDTH_STEP, FLOATING_MIN_WIDTH)
        state.floating.height = math.max(state.floating.height - FLOATING_HEIGHT_STEP, FLOATING_MIN_HEIGHT)
        state.floating.resize_mode = true
        prise.request_frame()
    end,
    layout_picker = function()
        open_layout_picker()
    end,
    -- command_palette is added after open_palette is defined
}

---@param query string
---@return Command[]
local function filter_commands(query)
    local results = {}
    for _, cmd in ipairs(commands) do
        local is_visible = not cmd.visible or cmd.visible()
        if is_visible then
            local cmd_name = cmd.name
            if type(cmd_name) == "function" then
                cmd_name = cmd_name()
            end
            if not query or query == "" or cmd_name:lower():find(query:lower(), 1, true) then
                table.insert(results, cmd)
            end
        end
    end
    return results
end

local function open_palette()
    if not state.palette.input then
        state.palette.input = prise.create_text_input()
    end
    state.palette.visible = true
    state.palette.selected = 1
    state.palette.scroll_offset = 0
    state.palette.input:clear()
    prise.request_frame()
end

-- Add command_palette handler now that open_palette is defined
action_handlers.command_palette = function()
    open_palette()
end

local function close_palette()
    state.palette.visible = false
    prise.request_frame()
end

local function execute_selected()
    local filtered = filter_commands(state.palette.input:text())
    if filtered[state.palette.selected] then
        close_palette()
        filtered[state.palette.selected].action()
    end
end

open_rename = function()
    if not state.rename.input then
        state.rename.input = prise.create_text_input()
    end
    local current_name = prise.get_session_name() or ""
    state.rename.input:clear()
    state.rename.input:insert(current_name)
    state.rename.visible = true
    prise.request_frame()
end

local function close_rename()
    state.rename.visible = false
    prise.request_frame()
end

local function execute_rename()
    local new_name = state.rename.input:text()
    if new_name and new_name ~= "" then
        local current_name = prise.get_session_name() or ""
        prise.rename_session(current_name, new_name)
    end
    close_rename()
end

open_session_picker = function()
    if not state.session_picker.input then
        state.session_picker.input = prise.create_text_input()
    end
    state.session_picker.input:clear()
    state.session_picker.sessions = prise.list_sessions() or {}
    state.session_picker.selected = 1
    state.session_picker.scroll_offset = 0
    state.session_picker.visible = true
    prise.request_frame()
end

local function close_session_picker()
    state.session_picker.visible = false
    prise.request_frame()
end

---Filter sessions by fuzzy matching the input text
---@param query string
---@return string[]
local function filter_sessions(query)
    if not query or query == "" then
        return state.session_picker.sessions
    end
    local lower_query = query:lower()
    local matches = {}
    for _, session in ipairs(state.session_picker.sessions) do
        if session:lower():find(lower_query, 1, true) then
            table.insert(matches, session)
        end
    end
    return matches
end

local function execute_session_switch()
    local query = state.session_picker.input:text()
    local filtered = filter_sessions(query)
    if #filtered == 0 then
        close_session_picker()
        return
    end
    local idx = state.session_picker.selected
    if idx >= 1 and idx <= #filtered then
        local target = filtered[idx]
        close_session_picker()
        prise.switch_session(target)
    end
end

local function open_session_rename()
    local query = state.session_picker.input:text()
    local filtered = filter_sessions(query)
    if #filtered == 0 then
        return
    end
    local idx = state.session_picker.selected
    if idx >= 1 and idx <= #filtered then
        local target = filtered[idx]
        state.session_picker.renaming = true
        state.session_picker.rename_target = target
        state.session_picker.input:clear()
        state.session_picker.input:insert(target)
        prise.request_frame()
    end
end

local function close_session_rename()
    state.session_picker.renaming = false
    state.session_picker.rename_target = nil
    state.session_picker.input:clear()
    state.session_picker.selected = 1
    state.session_picker.scroll_offset = 0
    -- Refresh the session list
    state.session_picker.sessions = prise.list_sessions() or {}
    prise.request_frame()
end

local function execute_session_rename()
    local new_name = state.session_picker.input:text()
    local target = state.session_picker.rename_target
    if new_name and new_name ~= "" and target and new_name ~= target then
        local ok, err = pcall(function()
            prise.rename_session(target, new_name)
        end)
        if not ok then
            prise.log.warn("Failed to rename session: " .. tostring(err))
        end
    end
    close_session_rename()
end

-- --- Main Functions ---

---@param event Event
function M.update(event)
    if event.type == "pty_attach" then
        prise.log.info("Lua: pty_attach received")
        ---@type Pty
        local pty = event.data.pty
        ---@type Pane
        local new_pane = { type = "pane", pty = pty, id = pty:id() }
        local old_focused_id = state.focused_id

        -- Apply default layout on first attach (no session restore)
        if #state.tabs == 0 and config.default_layout and not state.pending_layout then
            local applied = apply_layout(config.default_layout)
            if applied then
                if pty and pty.close then
                    pty:close()
                end
                return
            end
        end

        -- Check if we're building a layout
        if state.pending_layout then
            table.insert(state.pending_layout.pty_queue, pty)
            state.pending_layout.panes_received = state.pending_layout.panes_received + 1
            if state.pending_layout.panes_received >= state.pending_layout.panes_needed then
                finalize_layout(state.pending_layout)
            end
            return
        end

        -- Check if this PTY should be assigned to the floating pane
        if state.floating.pending then
            state.floating.pending = false
            local tab = get_active_tab()
            if tab then
                tab.floating = { pane = new_pane, visible = true }
            end
            prise.request_frame()
            return
        end

        if state.pending_new_tab then
            -- Create a new tab with this pane
            state.pending_new_tab = false
            local tab_id = state.next_tab_id
            state.next_tab_id = tab_id + 1
            ---@type Tab
            local new_tab = {
                id = tab_id,
                root = new_pane,
                last_focused_id = new_pane.id,
            }
            table.insert(state.tabs, new_tab)
            set_active_tab_index(#state.tabs)
        elseif #state.tabs == 0 then
            -- First terminal - create first tab
            local tab_id = state.next_tab_id
            state.next_tab_id = tab_id + 1
            ---@type Tab
            local new_tab = {
                id = tab_id,
                root = new_pane,
                last_focused_id = new_pane.id,
            }
            table.insert(state.tabs, new_tab)
            state.active_tab = 1
            state.focused_id = new_pane.id
        else
            state.zoomed_pane_id = nil -- Unzoom to reveal new split pane
            -- Insert into active tab's tree
            local tab = get_active_tab()
            if not tab then
                return
            end

            local direction = (state.pending_split and state.pending_split.direction) or "row"

            if state.focused_id then
                tab.root = insert_split_recursive(tab.root, state.focused_id, new_pane, direction)
            else
                -- Fallback
                if is_split(tab.root) then
                    table.insert(tab.root.children, new_pane)
                else
                    local split_id = state.next_split_id
                    state.next_split_id = state.next_split_id + 1
                    tab.root = {
                        type = "split",
                        split_id = split_id,
                        direction = direction,
                        children = { tab.root, new_pane },
                    }
                end
            end

            state.focused_id = new_pane.id
            state.pending_split = nil
        end
        update_pty_focus(old_focused_id, state.focused_id)
        prise.request_frame()
        prise.save() -- Auto-save on pane added
    elseif event.type == "key_press" then
        -- Handle command palette
        if state.palette.visible then
            ---@type string
            local k = event.data.key
            local filtered = filter_commands(state.palette.input:text())

            prise.log.debug(
                "palette key: "
                    .. tostring(k)
                    .. " len="
                    .. #k
                    .. " ctrl="
                    .. tostring(event.data.ctrl)
                    .. " super="
                    .. tostring(event.data.super)
            )

            if k == "Escape" then
                close_palette()
                return
            elseif k == "Enter" then
                execute_selected()
                return
            elseif k == "ArrowUp" or (k == "p" and event.data.ctrl) then
                if state.palette.selected > 1 then
                    state.palette.selected = state.palette.selected - 1
                    prise.request_frame()
                end
                return
            elseif k == "ArrowDown" or (k == "n" and event.data.ctrl) then
                if state.palette.selected < #filtered then
                    state.palette.selected = state.palette.selected + 1
                    prise.request_frame()
                end
                return
            end

            local old_text = state.palette.input:text()
            if handle_text_input_key(state.palette.input, event.data) then
                if state.palette.input:text() ~= old_text then
                    state.palette.selected = 1
                end
                return
            end
            return
        end

        -- Handle session picker
        if state.session_picker.visible then
            -- Handle session rename mode
            if state.session_picker.renaming then
                local k = event.data.key

                if k == "Escape" then
                    close_session_rename()
                    return
                elseif k == "Enter" then
                    execute_session_rename()
                    return
                end
                handle_text_input_key(state.session_picker.input, event.data)
                return
            end

            local k = event.data.key
            local filtered = filter_sessions(state.session_picker.input:text())

            if k == "Escape" then
                close_session_picker()
                return
            elseif k == "Enter" then
                execute_session_switch()
                return
            elseif k == "ArrowUp" or (k == "p" and event.data.ctrl) then
                if state.session_picker.selected > 1 then
                    state.session_picker.selected = state.session_picker.selected - 1
                    -- Adjust scroll if needed
                    if state.session_picker.selected <= state.session_picker.scroll_offset then
                        state.session_picker.scroll_offset = state.session_picker.selected - 1
                    end
                end
                prise.request_frame()
                return
            elseif k == "ArrowDown" or (k == "n" and event.data.ctrl) then
                if state.session_picker.selected < #filtered then
                    state.session_picker.selected = state.session_picker.selected + 1
                    -- Adjust scroll if needed (items_start_y = 8, plus 1 for bottom padding)
                    local visible_height = math.max(1, state.screen_rows - 9)
                    if state.session_picker.selected > state.session_picker.scroll_offset + visible_height then
                        state.session_picker.scroll_offset = state.session_picker.selected - visible_height
                    end
                end
                prise.request_frame()
                return
            elseif k == "Backspace" then
                state.session_picker.input:delete_backward()
                local new_filtered = filter_sessions(state.session_picker.input:text())
                state.session_picker.selected = math.min(state.session_picker.selected, math.max(1, #new_filtered))
                state.session_picker.scroll_offset = 0
                prise.request_frame()
                return
            elseif k == "D" and event.data.shift then
                -- Delete the selected session (Shift+D)
                if #filtered > 0 then
                    local idx = state.session_picker.selected
                    if idx >= 1 and idx <= #filtered then
                        local target = filtered[idx]
                        local current_session = prise.get_session_name()
                        if target == current_session then
                            -- Can't delete the current session
                            prise.log.warn("Cannot delete the current session")
                            return
                        end
                        prise.delete_session(target)
                        -- Refresh the session list
                        state.session_picker.sessions = prise.list_sessions() or {}
                        state.session_picker.selected = math.min(
                            state.session_picker.selected,
                            math.max(1, #filter_sessions(state.session_picker.input:text()))
                        )
                        prise.request_frame()
                    end
                end
                return
            elseif k == "R" and event.data.shift then
                -- Rename the selected session (Shift+R)
                open_session_rename()
                return
            elseif #k == 1 and not event.data.ctrl and not event.data.alt and not event.data.super then
                state.session_picker.input:insert(k)
                local new_filtered = filter_sessions(state.session_picker.input:text())
                state.session_picker.selected = math.min(state.session_picker.selected, math.max(1, #new_filtered))
                state.session_picker.scroll_offset = 0
                prise.request_frame()
                return
            end
            return
        end

        -- Handle layout picker
        if state.layout_picker.visible then
            local k = event.data.key
            local names = get_layout_names()

            if k == "Escape" then
                close_layout_picker()
                return
            elseif k == "Enter" then
                execute_selected_layout()
                return
            elseif k == "ArrowUp" or (k == "p" and event.data.ctrl) then
                if state.layout_picker.selected > 1 then
                    state.layout_picker.selected = state.layout_picker.selected - 1
                    if state.layout_picker.selected <= state.layout_picker.scroll_offset then
                        state.layout_picker.scroll_offset = state.layout_picker.selected - 1
                    end
                end
                prise.request_frame()
                return
            elseif k == "ArrowDown" or (k == "n" and event.data.ctrl) then
                if state.layout_picker.selected < #names then
                    state.layout_picker.selected = state.layout_picker.selected + 1
                    -- items_start_y = 8, plus 1 for bottom padding
                    local visible_height = math.max(1, state.screen_rows - 9)
                    if state.layout_picker.selected > state.layout_picker.scroll_offset + visible_height then
                        state.layout_picker.scroll_offset = state.layout_picker.selected - visible_height
                    end
                end
                prise.request_frame()
                return
            end
            return
        end

        -- Handle rename session prompt
        if state.rename.visible then
            local k = event.data.key

            if k == "Escape" then
                close_rename()
                return
            elseif k == "Enter" then
                execute_rename()
                return
            end
            handle_text_input_key(state.rename.input, event.data)
            return
        end

        -- Handle rename tab prompt
        if state.rename_tab.visible then
            local k = event.data.key

            if k == "Escape" then
                close_rename_tab()
                return
            elseif k == "Enter" then
                execute_rename_tab()
                return
            end
            handle_text_input_key(state.rename_tab.input, event.data)
            return
        end

        -- Handle swap tab with index prompt
        if state.swap_with_index and state.swap_with_index.visible then
            local k = event.data.key

            if k == "Escape" then
                state.swap_with_index.visible = false
                state.swap_with_index = nil
                prise.request_frame()
                return
            elseif k == "Enter" then
                local text = state.swap_with_index.input:text()
                local target_idx_num = tonumber(text)
                if target_idx_num then
                    local target_idx = math.floor(target_idx_num)
                    if target_idx >= 1 and target_idx <= #state.tabs and target_idx ~= state.active_tab then
                        swap_tabs(state.active_tab, target_idx)
                    end
                end
                state.swap_with_index.visible = false
                state.swap_with_index = nil
                prise.request_frame()
                return
            end
            handle_text_input_key(state.swap_with_index.input, event.data)
            prise.request_frame()
            return
        end

        -- Handle floating pane resize mode
        if state.floating.resize_mode then
            local k = event.data.key
            if k == "+" or (k == "=" and event.data.shift) then
                action_handlers.floating_increase_size()
                return
            elseif k == "-" then
                action_handlers.floating_decrease_size()
                return
            else
                -- Exit resize mode on any other key
                state.floating.resize_mode = false
                prise.request_frame()
                -- Don't return - let the key be processed normally
            end
        end

        -- Handle keybinds via matcher
        init_keybinds()

        -- Ignore modifier-only key presses (Shift, Ctrl, Alt, Super)
        if is_modifier_key(event.data.key) then
            return
        end

        local result = state.keybind_matcher:handle_key(event.data)

        if result.action or result.func then
            -- Cancel any pending timeout
            if state.timer then
                state.timer:cancel()
                state.timer = nil
            end
            state.pending_command = false

            -- Dispatch action
            if result.func then
                result.func()
            elseif result.action then
                local handler = action_handlers[result.action]
                if handler then
                    handler()
                end
            end
            prise.request_frame()
            return
        elseif result.pending then
            -- Key sequence in progress
            state.pending_command = true
            prise.request_frame()

            -- Cancel existing timeout and start new one
            if state.timer then
                state.timer:cancel()
            end
            state.timer = prise.set_timeout(1000, function()
                if state.pending_command then
                    state.pending_command = false
                    state.timer = nil
                    state.keybind_matcher:reset()
                    prise.request_frame()
                end
            end)
            return
        end

        -- No match - reset pending state if we were in one
        if state.pending_command then
            if state.timer then
                state.timer:cancel()
                state.timer = nil
            end
            state.pending_command = false
            prise.request_frame()
            -- Send the unmapped key to the focused PTY
            local root = get_active_root()
            if root and state.focused_id then
                local path = find_node_path(root, state.focused_id)
                if path then
                    local pane = path[#path]
                    pane.pty:send_key(event.data)
                end
            end
            return
        end

        -- Copy selection: Cmd+c (macOS) or Ctrl+Shift+c (Linux)
        if event.data.key == "c" then
            local is_copy = false
            if prise.platform == "macos" then
                is_copy = (event.data.super == true) and not event.data.ctrl and not event.data.alt
            else
                is_copy = (event.data.ctrl == true) and (event.data.shift == true) and not event.data.super
            end
            if is_copy then
                local pty = get_visible_floating_pty() or get_focused_pty()
                if pty then
                    pty:copy_selection()
                end
                return
            end
        end

        -- Route to floating pane if visible, otherwise to focused pane
        local tab = get_active_tab()
        if tab and tab.floating and tab.floating.visible then
            -- Send to floating pane
            if tab.floating.pane and tab.floating.pane.pty then
                tab.floating.pane.pty:send_key(event.data)
            end
            return
        else
            -- Pass key to focused PTY in main layout
            local root = get_active_root()
            if root and state.focused_id then
                local path = find_node_path(root, state.focused_id)
                if path then
                    local pane = path[#path]
                    pane.pty:send_key(event.data)
                end
            end
        end
    elseif event.type == "key_release" then
        local pty = get_visible_floating_pty() or get_focused_pty()
        if pty then
            local data = event.data
            data.release = true
            pty:send_key(data)
        end
    elseif event.type == "paste" then
        local text_sanitized = event.data.text:gsub("[\r\n\t]", " ")
        if state.palette.visible then
            state.palette.input:insert(text_sanitized)
            state.palette.selected = 1
            prise.request_frame()
        elseif state.rename_tab.visible then
            state.rename_tab.input:insert(text_sanitized)
            prise.request_frame()
        elseif state.rename.visible then
            state.rename.input:insert(text_sanitized)
            prise.request_frame()
        elseif state.session_picker.visible then
            state.session_picker.input:insert(text_sanitized)
            prise.request_frame()
        elseif state.swap_with_index and state.swap_with_index.visible then
            state.swap_with_index.input:insert(text_sanitized)
            prise.request_frame()
        else
            local pty = get_visible_floating_pty() or get_focused_pty()
            if pty then
                pty:send_paste(event.data.text)
            end
        end
    elseif event.type == "pty_exited" then
        local id = event.data.id
        prise.log.info("Lua: pty_exited " .. id)

        -- Check if this is a floating pane (check active tab first, then others)
        local active_tab = get_active_tab()
        if active_tab and active_tab.floating and active_tab.floating.pane.id == id then
            active_tab.floating = nil
            prise.request_frame()
            return
        end
        for _, tab in ipairs(state.tabs) do
            if tab ~= active_tab and tab.floating and tab.floating.pane.id == id then
                tab.floating = nil
                prise.request_frame()
                return
            end
        end

        local was_last = remove_pane_by_id(id)
        if not was_last then
            prise.save()
        end
    elseif event.type == "mouse" then
        local d = event.data

        -- Track tab and close button hover state on motion
        if d.action == "motion" and #state.tab_regions > 0 then
            local new_hover = nil
            local new_close_hover = nil
            if d.y < 1 then
                -- Check close button regions first (they're more specific)
                for _, region in ipairs(state.tab_close_regions) do
                    if d.x >= region.start_x and d.x < region.end_x then
                        new_close_hover = region.tab_index
                        new_hover = region.tab_index
                        break
                    end
                end
                -- If not on close button, check tab regions
                if not new_close_hover then
                    for _, region in ipairs(state.tab_regions) do
                        if d.x >= region.start_x and d.x < region.end_x then
                            new_hover = region.tab_index
                            break
                        end
                    end
                end
            end
            if new_hover ~= state.hovered_tab or new_close_hover ~= state.hovered_close_tab then
                state.hovered_tab = new_hover
                state.hovered_close_tab = new_close_hover
                prise.request_frame()
            end
        end

        if d.action == "press" and d.button == "left" then
            -- Check if click is on command palette item
            if state.palette.visible and #state.palette.regions > 0 then
                -- Convert float coords to integer cell positions
                local click_x = math.floor(d.x)
                local click_y = math.floor(d.y)

                local palette_start_x = math.floor((state.screen_cols - PALETTE_WIDTH) / 2)
                local palette_end_x = palette_start_x + PALETTE_WIDTH

                if click_x >= palette_start_x and click_x < palette_end_x then
                    for _, region in ipairs(state.palette.regions) do
                        if click_y >= region.start_y and click_y < region.end_y then
                            if state.palette.selected == region.index then
                                -- Already selected, execute it
                                execute_selected()
                            else
                                -- First click, just highlight it
                                state.palette.selected = region.index
                                prise.request_frame()
                            end
                            return
                        end
                    end
                end
            end

            -- Check if click is on tab bar (y < 1 and we have tab regions)
            if d.y < 1 and #state.tab_regions > 0 then
                -- Check close button regions first
                for _, region in ipairs(state.tab_close_regions) do
                    if d.x >= region.start_x and d.x < region.end_x then
                        close_tab(region.tab_index)
                        return
                    end
                end
                -- Then check tab regions for switching
                for _, region in ipairs(state.tab_regions) do
                    if d.x >= region.start_x and d.x < region.end_x then
                        set_active_tab_index(region.tab_index)
                        return
                    end
                end
            end

            -- Focus the clicked pane
            if d.target and d.target ~= state.focused_id then
                local old_id = state.focused_id
                state.focused_id = d.target
                update_pty_focus(old_id, state.focused_id)
                prise.request_frame()
            end

            -- Hide floating pane when clicking on main pane
            local tab = get_active_tab()
            if
                tab
                and tab.floating
                and tab.floating.visible
                and tab.floating.pane
                and d.target ~= tab.floating.pane.id
            then
                tab.floating.visible = false
                prise.request_frame()
            end
        end
        -- Forward mouse events to floating pane if visible and targeted
        local floating_tab = get_active_tab()
        if
            floating_tab
            and floating_tab.floating
            and floating_tab.floating.visible
            and floating_tab.floating.pane
            and d.target == floating_tab.floating.pane.id
        then
            if floating_tab.floating.pane.pty then
                floating_tab.floating.pane.pty:send_mouse({
                    x = d.target_x or 0,
                    y = d.target_y or 0,
                    button = d.button,
                    event_type = d.action,
                    mods = d.mods,
                })
            end
            return
        end

        -- Forward mouse events to the target PTY if there is one
        local root = get_active_root()
        if d.target and root then
            local path = find_node_path(root, d.target)
            if path then
                local pane = path[#path]
                pane.pty:send_mouse({
                    x = d.target_x or 0,
                    y = d.target_y or 0,
                    button = d.button,
                    event_type = d.action,
                    mods = d.mods,
                })
            end
        end
    elseif event.type == "winsize" then
        state.screen_cols = event.data.cols or state.screen_cols
        state.screen_rows = event.data.rows or state.screen_rows
        prise.request_frame()
    elseif event.type == "focus_in" then
        state.app_focused = true
        local pty = get_visible_floating_pty() or get_focused_pty()
        if pty then
            pty:set_focus(true)
        end
    elseif event.type == "focus_out" then
        state.app_focused = false
        local pty = get_visible_floating_pty() or get_focused_pty()
        if pty then
            pty:set_focus(false)
        end
    elseif event.type == "split_resize" then
        -- Handle mouse drag resize
        local d = event.data
        local split_id = d.parent_id
        local child_index = d.child_index
        local new_ratio = d.ratio

        -- In separator mode, widget children include separators interleaved with panes.
        -- Widget structure: [Pane0, Sep, Pane1, Sep, Pane2, ...]
        -- Lua state structure: [Pane0, Pane1, Pane2, ...]
        -- Map widget child_index to lua pane_index:
        -- - Even widget indices (0, 2, 4, ...) are panes
        -- - Odd widget indices (1, 3, 5, ...) are separators
        -- For a handle at the boundary after widget child N:
        -- - If N is even (a pane), pane_index = N / 2
        -- - If N is odd (a separator), pane_index = (N - 1) / 2 (the pane before the separator)
        local pane_index = child_index
        if config.borders.mode == "separator" and should_show_borders() then
            if child_index % 2 == 0 then
                pane_index = child_index // 2
            else
                pane_index = (child_index - 1) // 2
            end
        end

        -- Find the split by id and update the child's ratio
        local function update_split_ratio(node)
            if not node then
                return false
            end
            if is_split(node) then
                if node.split_id == split_id then
                    -- Found it - update the pane's ratio
                    if node.children[pane_index + 1] then
                        node.children[pane_index + 1].ratio = new_ratio
                    end
                    return true
                end
                for _, child in ipairs(node.children) do
                    if update_split_ratio(child) then
                        return true
                    end
                end
            end
            return false
        end

        local root = get_active_root()
        if update_split_ratio(root) then
            prise.request_frame()
            prise.save() -- Auto-save on layout change
        end
    elseif event.type == "cwd_changed" then
        -- CWD changed for a PTY - update cached git branch
        update_cached_git_branch()
        prise.request_frame()
        prise.save() -- Auto-save on cwd change
    end
end

---Recursive rendering function
---@param node Node
---@param force_unfocused? boolean
---@return table
local function render_node(node, force_unfocused)
    if is_pane(node) then
        local is_focused = (node.id == state.focused_id) and not (force_unfocused == true)
        prise.log.debug(
            "render_node: force_unfocused=" .. tostring(force_unfocused) .. " is_focused=" .. tostring(is_focused)
        )
        local terminal = prise.Terminal({
            pty = node.pty,
            ratio = node.ratio,
            focus = is_focused,
        })

        -- Wrap in Box if borders should be shown (only in box mode)
        if should_show_borders() and config.borders.mode == "box" then
            local border_color = is_focused and config.borders.focused_color or config.borders.unfocused_color

            return prise.Box({
                border = config.borders.style,
                style = { fg = border_color },
                child = terminal,
                ratio = node.ratio, -- Propagate ratio for layout system
            })
        else
            return terminal
        end
    elseif is_split(node) then
        local children_widgets = {}

        -- In separator mode, insert separators between children
        if should_show_borders() and config.borders.mode == "separator" then
            for i, child in ipairs(node.children) do
                -- Add separator before this child (except for first)
                if i > 1 then
                    -- Determine separator color based on adjacency to focused pane
                    local prev_child = node.children[i - 1]
                    local prev_focused = contains_focused(prev_child)
                    local curr_focused = contains_focused(child)
                    local sep_color = (prev_focused or curr_focused) and config.borders.focused_color
                        or config.borders.unfocused_color

                    local sep_axis = node.direction == "row" and "vertical" or "horizontal"
                    table.insert(
                        children_widgets,
                        prise.Separator({
                            axis = sep_axis,
                            style = { fg = sep_color },
                            border = config.borders.style,
                        })
                    )
                end
                table.insert(children_widgets, render_node(child, force_unfocused))
            end
        else
            -- Box mode or no borders: just render children directly
            for _, child in ipairs(node.children) do
                table.insert(children_widgets, render_node(child, force_unfocused))
            end
        end

        local props = {
            children = children_widgets,
            ratio = node.ratio,
            id = node.split_id,
            cross_axis_align = "stretch",
            resizable = true,
        }

        if node.direction == "row" then
            return prise.Row(props)
        else
            return prise.Column(props)
        end
    else
        error("render_node: unknown node type: " .. tostring(node.type))
    end
end

---Format a command palette item with name and right-aligned shortcut
---@param name string
---@param shortcut? string
---@param width number
---@return string
local function format_palette_item(name, shortcut, width)
    if not shortcut then
        return name
    end
    local padding = width - prise.gwidth(name) - prise.gwidth(shortcut)
    if padding < 2 then
        padding = 2
    end
    return name .. string.rep(" ", padding) .. shortcut
end

---Build the command palette overlay
---@return table?
local function build_palette()
    if not state.palette.visible or not state.palette.input then
        state.palette.regions = {}
        return nil
    end

    local text = state.palette.input:text()
    prise.log.debug("build_palette: text='" .. text .. "'")
    local filtered = filter_commands(text)
    local has_commands = #filtered > 0
    if not has_commands then
        table.insert(filtered, { name = "No matches" })
    end
    prise.log.debug("build_palette: filtered count=" .. #filtered)

    local items = {}
    for _, cmd in ipairs(filtered) do
        local cmd_name = cmd.name
        if type(cmd_name) == "function" then
            cmd_name = cmd_name()
        end
        table.insert(items, format_palette_item(cmd_name, cmd.shortcut, PALETTE_INNER_WIDTH))
    end

    local palette_style = { bg = THEME.bg1, fg = THEME.fg_bright }
    local selected_style = { bg = THEME.accent, fg = THEME.fg_dark }
    local input_style = { bg = THEME.bg1, fg = THEME.fg_bright }

    -- Calculate click regions for visible items only (skip if no real commands)
    -- Palette layout: y=5, padding top=1, text input=1 line, separator=1 line
    -- Items start at y = 5 + 1 + 1 + 1 = 8
    local items_start_y = state.palette.palette_y + 1 + 1 + 1
    state.palette.regions = {}
    if has_commands then
        -- Calculate visible height: screen height minus palette header and padding
        -- Subtract: palette_y (5) + padding (2) + input (1) + separator (1) + bottom padding (1)
        local available_height = math.max(1, state.screen_rows - items_start_y - 1)
        local visible_count = math.min(#items - state.palette.scroll_offset, available_height)
        for display_row = 1, visible_count do
            local item_index = state.palette.scroll_offset + display_row
            table.insert(state.palette.regions, {
                start_y = items_start_y + (display_row - 1),
                end_y = items_start_y + display_row,
                index = item_index,
            })
        end
    end

    return prise.Positioned({
        anchor = "top_center",
        y = state.palette.palette_y,
        child = prise.Box({
            border = "none",
            max_width = PALETTE_WIDTH,
            style = palette_style,
            child = prise.Padding({
                top = 1,
                bottom = 1,
                left = 2,
                right = 2,
                child = prise.Column({
                    cross_axis_align = "stretch",
                    children = {
                        prise.TextInput({
                            input = state.palette.input,
                            style = input_style,
                        }),
                        prise.Text({
                            text = string.rep("─", PALETTE_WIDTH),
                            style = { fg = THEME.bg3, bg = THEME.bg1 },
                        }),
                        prise.List({
                            items = items,
                            selected = state.palette.selected,
                            scroll_offset = state.palette.scroll_offset,
                            style = palette_style,
                            selected_style = selected_style,
                        }),
                    },
                }),
            }),
        }),
    })
end

---Build the rename session overlay
---@return table?
local function build_rename()
    if not state.rename.visible or not state.rename.input then
        return nil
    end

    local palette_style = { bg = THEME.bg1, fg = THEME.fg_bright }
    local input_style = { bg = THEME.bg1, fg = THEME.fg_bright }

    return prise.Positioned({
        anchor = "top_center",
        y = 5,
        child = prise.Box({
            border = "none",
            max_width = PALETTE_WIDTH,
            style = palette_style,
            child = prise.Padding({
                top = 1,
                bottom = 1,
                left = 2,
                right = 2,
                child = prise.Column({
                    cross_axis_align = "stretch",
                    children = {
                        prise.Text({ text = "Rename Session", style = { fg = THEME.fg_dim, bg = THEME.bg1 } }),
                        prise.TextInput({
                            input = state.rename.input,
                            style = input_style,
                        }),
                    },
                }),
            }),
        }),
    })
end

---Build the rename tab modal
---@return table?
local function build_rename_tab()
    if not state.rename_tab.visible or not state.rename_tab.input then
        return nil
    end

    local palette_style = { bg = THEME.bg1, fg = THEME.fg_bright }
    local input_style = { bg = THEME.bg1, fg = THEME.fg_bright }

    return prise.Positioned({
        anchor = "top_center",
        y = 5,
        child = prise.Box({
            border = "none",
            max_width = PALETTE_WIDTH,
            style = palette_style,
            child = prise.Padding({
                top = 1,
                bottom = 1,
                left = 2,
                right = 2,
                child = prise.Column({
                    cross_axis_align = "stretch",
                    children = {
                        prise.Text({ text = "Rename Tab", style = { fg = THEME.fg_dim, bg = THEME.bg1 } }),
                        prise.TextInput({
                            input = state.rename_tab.input,
                            style = input_style,
                        }),
                    },
                }),
            }),
        }),
    })
end

---Build the session picker modal
---@return table?
local function build_session_picker()
    if not state.session_picker.visible or not state.session_picker.input then
        state.session_picker.regions = {}
        return nil
    end

    local text = state.session_picker.input:text()
    local filtered = filter_sessions(text)
    local has_sessions = #filtered > 0

    local items = {}
    local current_session = prise.get_session_name()
    for _, session in ipairs(filtered) do
        local display = session
        if session == current_session then
            display = session .. " (current)"
        end
        table.insert(items, display)
    end

    if not has_sessions then
        table.insert(items, "No sessions found")
    end

    local palette_style = { bg = THEME.bg1, fg = THEME.fg_bright }
    local selected_style = { bg = THEME.accent, fg = THEME.fg_dark }
    local input_style = { bg = THEME.bg1, fg = THEME.fg_bright }

    -- Calculate click regions for visible items
    local items_start_y = 5 + 1 + 1 + 1 -- palette_y + padding + input + separator
    state.session_picker.regions = {}
    if has_sessions then
        local available_height = math.max(1, state.screen_rows - items_start_y - 1)
        local visible_count = math.min(#items - state.session_picker.scroll_offset, available_height)
        for display_row = 1, visible_count do
            local item_index = state.session_picker.scroll_offset + display_row
            table.insert(state.session_picker.regions, {
                start_y = items_start_y + (display_row - 1),
                end_y = items_start_y + display_row,
                index = item_index,
            })
        end
    end

    return prise.Positioned({
        anchor = "top_center",
        y = 5,
        focus = true,
        child = prise.Box({
            border = "none",
            max_width = PALETTE_WIDTH,
            style = palette_style,
            focus = true,
            child = prise.Padding({
                top = 1,
                bottom = 1,
                left = 2,
                right = 2,
                child = prise.Column({
                    cross_axis_align = "stretch",
                    children = {
                        prise.Text({ text = "Switch Session", style = { fg = THEME.fg_dim, bg = THEME.bg1 } }),
                        prise.Padding({
                            left = 38,
                            child = prise.Text({
                                text = "Shift + R - rename",
                                style = { fg = THEME.fg_dim, bg = THEME.bg1 },
                            }),
                        }),
                        prise.Padding({
                            left = 38,
                            child = prise.Text({
                                text = "Shift + D - delete",
                                style = { fg = THEME.fg_dim, bg = THEME.bg1 },
                            }),
                        }),
                        prise.TextInput({
                            input = state.session_picker.input,
                            style = input_style,
                            focus = true,
                        }),
                        prise.Text({
                            text = string.rep("─", PALETTE_WIDTH),
                            style = { fg = THEME.bg3, bg = THEME.bg1 },
                        }),
                        prise.List({
                            items = items,
                            selected = state.session_picker.selected,
                            scroll_offset = state.session_picker.scroll_offset,
                            style = palette_style,
                            selected_style = selected_style,
                        }),
                    },
                }),
            }),
        }),
    })
end

---Build the layout picker modal
---@return table?
local function build_layout_picker()
    if not state.layout_picker.visible then
        state.layout_picker.regions = {}
        return nil
    end

    local names = get_layout_names()
    if #names == 0 then
        close_layout_picker()
        return nil
    end

    local items = {}
    for _, name in ipairs(names) do
        local layout = config.layouts[name]
        local tab_count = #layout.tabs
        local pane_count = count_layout_total_panes(layout)
        local desc = string.format("%s (%d tabs, %d panes)", name, tab_count, pane_count)
        table.insert(items, desc)
    end

    local palette_style = { bg = THEME.bg1, fg = THEME.fg_bright }
    local selected_style = { bg = THEME.accent, fg = THEME.fg_dark }

    local items_start_y = 5 + 1 + 1 + 1
    state.layout_picker.regions = {}
    local available_height = math.max(1, state.screen_rows - items_start_y - 1)
    local visible_count = math.min(#items - state.layout_picker.scroll_offset, available_height)
    for display_row = 1, visible_count do
        local item_index = state.layout_picker.scroll_offset + display_row
        table.insert(state.layout_picker.regions, {
            start_y = items_start_y + (display_row - 1),
            end_y = items_start_y + display_row,
            index = item_index,
        })
    end

    return prise.Positioned({
        anchor = "top_center",
        y = 5,
        focus = true,
        child = prise.Box({
            border = "none",
            max_width = PALETTE_WIDTH,
            style = palette_style,
            focus = true,
            child = prise.Padding({
                top = 1,
                bottom = 1,
                left = 2,
                right = 2,
                child = prise.Column({
                    cross_axis_align = "stretch",
                    children = {
                        prise.Text({ text = "Layouts", style = { fg = THEME.fg_dim, bg = THEME.bg1 } }),
                        prise.Text({
                            text = string.rep("─", PALETTE_WIDTH),
                            style = { fg = THEME.bg3, bg = THEME.bg1 },
                        }),
                        prise.List({
                            items = items,
                            selected = state.layout_picker.selected,
                            scroll_offset = state.layout_picker.scroll_offset,
                            style = palette_style,
                            selected_style = selected_style,
                        }),
                    },
                }),
            }),
        }),
    })
end

---Resolve the display title for a tab
---@param tab Tab
---@param is_active boolean
---@return string
local function get_tab_title(tab, is_active)
    ---@type string
    local result = "Terminal"
    if tab.title then
        ---@diagnostic disable-next-line: cast-type-mismatch
        result = tostring(tab.title)
    else
        local focused_id = is_active and state.focused_id or tab.last_focused_id
        if focused_id and tab.root then
            local path = find_node_path(tab.root, focused_id)
            if path then
                local pane = path[#path]
                local pty_title = pane.pty:title()
                if pty_title and #pty_title > 0 then
                    result = pty_title
                else
                    local cwd = pane.pty:cwd()
                    if cwd then
                        result = cwd:match("([^/]+)/?$") or cwd
                    end
                end
            end
        end
    end
    ---@diagnostic disable-next-line: return-type-mismatch
    return result
end

---Build tab bar with default design (rounded pill style)
---@return table
local function build_tab_bar_default()
    local num_tabs = #state.tabs
    local total_width = state.screen_cols
    local endcap_width = 2 -- left_round and right_round are 1 cell each

    -- Calculate tab widths: divide available space evenly
    local base_tab_width = math.floor(total_width / num_tabs)
    local extra_pixels = total_width % num_tabs

    local segments = {}
    local x_pos = 0
    state.tab_regions = {}
    state.tab_close_regions = {}

    for i, tab in ipairs(state.tabs) do
        local is_active = (i == state.active_tab)
        local is_hovered = (i == state.hovered_tab)
        local is_close_hovered = (i == state.hovered_close_tab)

        -- Distribute extra width to last tabs so they fill the line
        local tab_width = base_tab_width
        if i > (num_tabs - extra_pixels) then
            tab_width = tab_width + 1
        end

        -- Close widget: always reserve 2 cells, only show icon when hovered
        local close_widget_width = 2
        local close_text = "  " -- 2 spaces when not hovered
        if is_close_hovered then
            close_text = "\u{F530}" -- md-close_circle (filled)
        elseif is_hovered then
            close_text = "\u{F467}" -- md-close_circle_outline
        end
        -- Pad close_text to exactly close_widget_width cells
        local close_text_width = prise.gwidth(close_text)
        if close_text_width < close_widget_width then
            close_text = close_text .. string.rep(" ", close_widget_width - close_text_width)
        end

        -- Get title
        local title = get_tab_title(tab, is_active)

        -- Tab index shown on the right
        local index_str = tostring(i)
        local index_width = #index_str + 2 -- space + index + space

        -- Always reserve space for endcaps, close widget, and index
        local inner_width = tab_width - endcap_width - close_widget_width - index_width
        local title_width = prise.gwidth(title)

        -- Truncate title if needed
        if title_width > inner_width then
            title = string.sub(title, 1, inner_width - 1) .. "…"
            title_width = prise.gwidth(title)
        end

        -- Center the title
        local padding_total = inner_width - title_width
        local pad_left = math.floor(padding_total / 2)
        local pad_right = padding_total - pad_left
        if pad_left < 0 then
            pad_left = 0
        end
        if pad_right < 0 then
            pad_right = 0
        end

        local label = string.rep(" ", pad_left) .. title .. string.rep(" ", pad_right)
        local index_label = " " .. index_str .. " "

        -- Record close button hit region (after left endcap)
        local close_start = x_pos + 1 -- after left endcap
        table.insert(state.tab_close_regions, {
            start_x = close_start,
            end_x = close_start + close_widget_width,
            tab_index = i,
        })

        -- Record hit region for this tab
        table.insert(state.tab_regions, {
            start_x = x_pos,
            end_x = x_pos + tab_width,
            tab_index = i,
        })
        x_pos = x_pos + tab_width

        local tab_bg, tab_fg
        if is_active then
            tab_bg = THEME.bg4
            tab_fg = THEME.fg_bright
        elseif is_hovered then
            tab_bg = THEME.bg3
            tab_fg = THEME.fg_bright
        else
            tab_bg = THEME.bg2
            tab_fg = THEME.fg_dim
        end

        -- Left endcap
        table.insert(segments, { text = POWERLINE_SYMBOLS.left_round, style = { fg = tab_bg, bg = THEME.bg1 } })
        -- Close widget
        table.insert(segments, { text = close_text, style = { bg = tab_bg, fg = tab_fg } })
        -- Tab content (title)
        table.insert(segments, { text = label, style = { bg = tab_bg, fg = tab_fg, bold = is_active } })
        -- Tab index (right side, dimmed)
        table.insert(segments, { text = index_label, style = { bg = tab_bg, fg = THEME.fg_dim } })
        -- Right endcap
        table.insert(segments, { text = POWERLINE_SYMBOLS.right_round, style = { fg = tab_bg, bg = THEME.bg1 } })
    end

    return segments
end

---@return TabInfo[]
local function build_custom_tab_infos()
    local tab_infos = {}
    for i, tab in ipairs(state.tabs) do
        local title = get_tab_title(tab, i == state.active_tab)
        local is_explicit = tab.title ~= nil

        if config.tab_bar.format_title and not is_explicit then
            title = config.tab_bar.format_title(title, i)
        end

        table.insert(tab_infos, {
            index = i,
            title = title,
            is_explicit_title = is_explicit,
            is_active = (i == state.active_tab),
            is_hovered = (i == state.hovered_tab),
            is_close_hovered = (i == state.hovered_close_tab),
            pane_count = count_tab_panes(tab),
            is_zoomed = (i == state.active_tab and state.zoomed_pane_id ~= nil)
                or (i ~= state.active_tab and tab.zoomed_pane_id ~= nil),
        })
    end

    return tab_infos
end

---Build tab bar with custom renderer
---Calculates actual tab positions from rendered segments to ensure hover detection works correctly
---@return table
local function build_tab_bar_custom()
    local tab_infos = build_custom_tab_infos()

    -- Enhanced custom renderer wrapper that tracks tab boundaries
    local x_pos = 0

    -- First, render segments and calculate their actual widths
    local original_segments = config.tab_bar.render(tab_infos, state.screen_cols, THEME)

    -- Build click regions from rendered segments
    state.tab_regions = {}
    state.tab_close_regions = {}

    -- Calculate actual segment positions
    local segment_positions = {}
    for _, seg in ipairs(original_segments) do
        local width = prise.gwidth(seg.text)
        table.insert(segment_positions, {
            start_x = x_pos,
            end_x = x_pos + width,
        })
        x_pos = x_pos + width
    end

    -- Map segments to tabs
    -- Common pattern: custom renderers output alternating tab/separator segments
    -- Try to detect this pattern and map accordingly
    local tab_count = #tab_infos
    local segment_count = #original_segments

    if segment_count >= tab_count then
        -- Assume segments are ordered: tab1, sep, tab2, sep, ..., tabN
        -- Or just: tab1, tab2, ..., tabN if no separators
        local segments_per_tab = segment_count / tab_count
        local is_alternating = (segments_per_tab >= 1.5 and segments_per_tab <= 2.5)

        if is_alternating then
            -- Likely pattern: tab, separator, tab, separator, ...
            for tab_idx = 1, tab_count do
                local seg_idx = (tab_idx - 1) * 2 + 1
                if seg_idx <= segment_count then
                    local tab_start = segment_positions[seg_idx].start_x
                    -- Include separator in the clickable region if it exists
                    local sep_seg_idx = seg_idx + 1
                    local tab_end
                    if sep_seg_idx <= segment_count and tab_idx < tab_count then
                        -- Include separator
                        tab_end = segment_positions[sep_seg_idx].end_x
                    else
                        -- Last tab, no separator
                        tab_end = segment_positions[seg_idx].end_x
                    end

                    table.insert(state.tab_regions, {
                        start_x = tab_start,
                        end_x = tab_end,
                        tab_index = tab_idx,
                    })
                end
            end
        else
            -- Fallback: divide segments proportionally
            for tab_idx = 1, tab_count do
                local first_seg = math.floor((tab_idx - 1) * segment_count / tab_count) + 1
                local last_seg = math.floor(tab_idx * segment_count / tab_count)

                if first_seg <= segment_count then
                    table.insert(state.tab_regions, {
                        start_x = segment_positions[first_seg].start_x,
                        end_x = segment_positions[math.min(last_seg, segment_count)].end_x,
                        tab_index = tab_idx,
                    })
                end
            end
        end
    end

    return original_segments
end

---Build the tab bar UI
---@return table?
local function build_tab_bar()
    if not config.tab_bar.show_single_tab and #state.tabs <= 1 then
        state.tab_regions = {}
        state.tab_close_regions = {}
        return nil
    end

    -- Use custom renderer if provided
    if config.tab_bar.render then
        local segments = build_tab_bar_custom()
        return prise.Text(segments)
    else
        local segments = build_tab_bar_default()
        return prise.Text(segments)
    end
end

---Build the powerline-style status bar
---@return table
local function build_status_bar()
    local mode_color = state.pending_command and THEME.mode_command or THEME.mode_normal
    local session_name = (prise.get_session_name() or "prise"):upper()
    local mode_text = state.pending_command and " CMD " or (" " .. session_name .. " ")

    -- Use cached git branch (updated on cwd_changed and focus change)
    local git_branch = state.cached_git_branch

    -- Get current time
    local time_str = prise.get_time()

    -- Build segments and track width
    local segments = {}
    local left_width = 0

    -- Mode indicator
    table.insert(segments, { text = mode_text, style = { bg = mode_color, fg = THEME.fg_dark, bold = true } })
    left_width = left_width + prise.gwidth(mode_text)

    -- Track the last background color for proper powerline transitions
    local last_bg = mode_color

    -- Git branch
    if git_branch then
        local branch_text = " \u{F062C} " .. git_branch .. " "
        table.insert(segments, { text = POWERLINE_SYMBOLS.right_solid, style = { fg = last_bg, bg = THEME.bg2 } })
        table.insert(segments, { text = branch_text, style = { bg = THEME.bg2, fg = THEME.fg_bright } })
        left_width = left_width + 1 + prise.gwidth(branch_text)
        last_bg = THEME.bg2
    end

    -- Zoom indicator
    if state.zoomed_pane_id then
        table.insert(segments, { text = POWERLINE_SYMBOLS.right_solid, style = { fg = last_bg, bg = THEME.yellow } })
        table.insert(segments, { text = " ZOOM ", style = { bg = THEME.yellow, fg = THEME.fg_dark, bold = true } })
        left_width = left_width + 1 + 6
        last_bg = THEME.yellow
    end

    -- Floating resize mode indicator
    if state.floating.resize_mode then
        local resize_text = " RESIZE " .. state.floating.width .. "x" .. state.floating.height .. " "
        table.insert(segments, { text = POWERLINE_SYMBOLS.right_solid, style = { fg = last_bg, bg = THEME.accent } })
        table.insert(segments, { text = resize_text, style = { bg = THEME.accent, fg = THEME.fg_dark, bold = true } })
        left_width = left_width + 1 + prise.gwidth(resize_text)
        last_bg = THEME.accent
    end

    -- End left side
    table.insert(segments, { text = POWERLINE_SYMBOLS.right_solid, style = { fg = last_bg, bg = THEME.bg1 } })
    left_width = left_width + 1

    -- Right side content
    local right_text = " " .. time_str .. " "
    local right_width = 1 + prise.gwidth(right_text) -- powerline symbol + time

    -- Calculate padding to fill the middle
    local padding = state.screen_cols - left_width - right_width
    if padding < 0 then
        padding = 0
    end

    -- Add padding
    table.insert(segments, { text = string.rep(" ", padding), style = { bg = THEME.bg1 } })

    -- Add right side
    table.insert(segments, { text = POWERLINE_SYMBOLS.left_solid, style = { fg = THEME.bg3, bg = THEME.bg1 } })
    table.insert(segments, { text = right_text, style = { bg = THEME.bg3, fg = THEME.fg_dim } })

    return prise.Text(segments)
end

---Schedule a clock timer to refresh the display every minute
local function schedule_clock_timer()
    if state.clock_timer or state.detaching then
        return
    end
    state.clock_timer = prise.set_timeout(60000, function()
        state.clock_timer = nil
        if not state.detaching then
            prise.request_frame()
            schedule_clock_timer()
        end
    end)
end

---Build the swap tab with index modal
---@return table|nil
local function build_swap_with_index()
    if not state.swap_with_index or not state.swap_with_index.visible or not state.swap_with_index.input then
        return nil
    end

    local palette_style = { bg = THEME.bg1, fg = THEME.fg_bright }
    local input_style = { bg = THEME.bg1, fg = THEME.fg_bright }

    return prise.Positioned({
        anchor = "top_center",
        y = 5,
        child = prise.Box({
            border = "none",
            max_width = PALETTE_WIDTH,
            style = palette_style,
            child = prise.Padding({
                top = 1,
                bottom = 1,
                left = 2,
                right = 2,
                child = prise.Column({
                    cross_axis_align = "stretch",
                    children = {
                        prise.Text({
                            text = "Swap Tab with Index (1-" .. #state.tabs .. ")",
                            style = { fg = THEME.fg_dim, bg = THEME.bg1 },
                        }),
                        prise.TextInput({
                            input = state.swap_with_index.input,
                            style = input_style,
                        }),
                    },
                }),
            }),
        }),
    })
end

local function build_floating()
    local tab = get_active_tab()
    if
        not tab
        or not tab.floating
        or not tab.floating.visible
        or not tab.floating.pane
        or not tab.floating.pane.pty
    then
        return nil
    end

    -- Create positioned terminal widget with size constraints
    return prise.Positioned({
        anchor = "center",
        child = prise.Box({
            border = config.borders.style,
            style = { fg = config.borders.focused_color },
            max_width = state.floating.width,
            max_height = state.floating.height,
            child = prise.Terminal({
                pty = tab.floating.pane.pty,
                focus = true,
            }),
        }),
    })
end

function M.view()
    local root = get_active_root()
    if not root then
        return prise.Column({
            cross_axis_align = "stretch",
            children = { prise.Text("Waiting for terminal...") },
        })
    end

    -- Start clock timer for status bar updates
    if config.status_bar.enabled then
        schedule_clock_timer()
    end

    local palette = build_palette()
    local rename = build_rename()
    local rename_tab = build_rename_tab()
    local swap_with_index = build_swap_with_index()
    local session_picker = build_session_picker()
    local layout_picker = build_layout_picker()
    local floating = build_floating()
    local tab_bar = build_tab_bar()
    prise.log.debug("view: palette.visible=" .. tostring(state.palette.visible))

    -- When zoomed, render only the zoomed pane
    local tab = get_active_tab()
    local floating_visible = tab and tab.floating and tab.floating.visible
    local overlay_visible = state.palette.visible
        or state.rename.visible
        or state.rename_tab.visible
        or (state.swap_with_index and state.swap_with_index.visible)
        or state.session_picker.visible
        or state.layout_picker.visible
        or floating_visible
    local content
    if state.zoomed_pane_id then
        local path = find_node_path(root, state.zoomed_pane_id)
        if path then
            local pane = path[#path]
            local terminal = prise.Terminal({
                pty = pane.pty,
                focus = not overlay_visible,
            })

            -- Apply borders to zoomed pane if enabled and show_single_pane is true
            -- (zoomed pane is a temporary single-pane view)
            if config.borders.enabled and config.borders.show_single_pane then
                content = prise.Box({
                    border = config.borders.style,
                    style = { fg = config.borders.focused_color }, -- Zoomed pane is always focused
                    child = terminal,
                })
            else
                content = terminal
            end
        else
            state.zoomed_pane_id = nil
            content = render_node(root, overlay_visible)
        end
    else
        content = render_node(root, overlay_visible)
    end

    local status_bar = config.status_bar.enabled and build_status_bar() or nil

    local main_children = {}
    if tab_bar then
        table.insert(main_children, tab_bar)
    end
    table.insert(main_children, content)
    if status_bar then
        table.insert(main_children, status_bar)
    end

    local main_ui = prise.Column({
        cross_axis_align = "stretch",
        children = main_children,
    })

    -- Build overlay stack: floating pane below modals, modals on top
    local overlay_children = { main_ui }
    if floating then
        table.insert(overlay_children, floating)
    end

    local modal = palette or rename or rename_tab or swap_with_index or session_picker or layout_picker
    if modal then
        table.insert(overlay_children, modal)
        return prise.Stack({
            children = overlay_children,
        })
    end

    -- If we only have floating pane (no modal), use Stack if floating exists
    if floating then
        return prise.Stack({
            children = overlay_children,
        })
    end

    return main_ui
end

---@param cwd_lookup? fun(pty_id: number): string?
---@return table
function M.get_state(cwd_lookup)
    -- Serialize all tabs
    local tabs_data = {}
    for _, tab in ipairs(state.tabs) do
        table.insert(tabs_data, {
            id = tab.id,
            title = tab.title,
            root = serialize_node(tab.root, cwd_lookup),
            last_focused_id = tab.last_focused_id,
            floating = serialize_floating(tab.floating, cwd_lookup),
        })
    end

    return {
        tabs = tabs_data,
        active_tab = state.active_tab,
        next_tab_id = state.next_tab_id,
        focused_id = state.focused_id,
        next_split_id = state.next_split_id,
        floating_settings = {
            width = state.floating.width,
            height = state.floating.height,
        },
    }
end

---@param saved? table
---@param pty_lookup fun(id: number): Pty?
function M.set_state(saved, pty_lookup)
    if not saved then
        return
    end

    -- Handle migration from old format (single root) to new format (tabs)
    if saved.tabs == nil and saved.root ~= nil then
        -- Old format: migrate to tabs
        local restored_root = deserialize_node(saved.root, pty_lookup)
        if restored_root then
            local tab_id = 1
            state.tabs = {
                {
                    id = tab_id,
                    root = restored_root,
                    last_focused_id = saved.focused_id,
                },
            }
            state.active_tab = 1
            state.next_tab_id = tab_id + 1
            state.focused_id = saved.focused_id
            state.next_split_id = saved.next_split_id or 1
        end
    else
        -- New format: restore tabs
        state.tabs = {}
        for _, tab_data in ipairs(saved.tabs or {}) do
            local restored_root = deserialize_node(tab_data.root, pty_lookup)
            if restored_root then
                table.insert(state.tabs, {
                    id = tab_data.id,
                    title = tab_data.title,
                    root = restored_root,
                    last_focused_id = tab_data.last_focused_id,
                    floating = deserialize_floating(tab_data.floating, pty_lookup),
                })
            end
        end
        state.active_tab = saved.active_tab or 1
        state.next_tab_id = saved.next_tab_id or (#state.tabs + 1)
        state.focused_id = saved.focused_id
        state.next_split_id = saved.next_split_id or 1

        -- Restore floating pane settings
        if saved.floating_settings then
            state.floating.width = saved.floating_settings.width or state.floating.width
            state.floating.height = saved.floating_settings.height or state.floating.height
        end

        -- Ensure active_tab is valid
        if state.active_tab > #state.tabs then
            state.active_tab = #state.tabs
        end
        if state.active_tab < 1 and #state.tabs > 0 then
            state.active_tab = 1
        end
    end

    -- Ensure focus is valid
    if #state.tabs > 0 and not state.focused_id then
        local tab = state.tabs[state.active_tab]
        if tab then
            local first = get_first_leaf(tab.root)
            if first then
                state.focused_id = first.id
            end
        end
    end

    prise.request_frame()
end

-- Export internal functions for testing
M._test = {
    is_pane = is_pane,
    is_split = is_split,
    collect_panes = collect_panes,
    find_node_path = find_node_path,
    get_first_leaf = get_first_leaf,
    get_last_leaf = get_last_leaf,
    format_palette_item = format_palette_item,
    build_custom_tab_infos = build_custom_tab_infos,
    close_tab = close_tab,
    remove_pane_by_id = remove_pane_by_id,
    set_active_tab_index = set_active_tab_index,
    set_state = function(test_state)
        state.tabs = test_state.tabs or {}
        state.active_tab = test_state.active_tab or 1
        state.next_tab_id = test_state.next_tab_id or (#state.tabs + 1) -- state.tabs already updated above
        state.focused_id = test_state.focused_id
        state.zoomed_pane_id = test_state.zoomed_pane_id
        state.floating = { width = 0.8, height = 0.8, visible = false, pending = false, resize_mode = false }
        state.hovered_tab = nil
        state.hovered_close_tab = nil
        state.tab_regions = {}
        state.tab_close_regions = {}
        state.pending_split = nil
        state.next_split_id = test_state.next_split_id or 1
    end,
    -- Returns a direct reference to internal state, not a copy
    get_state = function()
        return state
    end,
}

return M
