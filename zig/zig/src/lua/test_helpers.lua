--- Test helpers for tiling module tests
--- Shared mock constructors and prise mock setup
local M = {}

---Create a mock Pty object
---@param id integer
---@return Pty
function M.mock_pty(id)
    return {
        id = function()
            return id
        end,
        title = function()
            return "mock"
        end,
        cwd = function()
            return nil
        end,
        size = function()
            return { rows = 24, cols = 80, width_px = 0, height_px = 0 }
        end,
        send_key = function() end,
        send_mouse = function() end,
        send_paste = function() end,
        set_focus = function() end,
        close = function() end,
        copy_selection = function() end,
    }
end

---Create a mock Pty that returns a specific cwd
---@param id integer
---@param cwd_val string
---@return Pty
function M.mock_pty_with_cwd(id, cwd_val)
    local pty = M.mock_pty(id)
    pty.cwd = function()
        return cwd_val
    end
    return pty
end

---Create a mock Pty that records send_key/send_mouse calls
---@param id integer
---@return Pty
function M.mock_tracked_pty(id)
    local pty = M.mock_pty(id)
    ---@diagnostic disable-next-line: inject-field
    pty._calls = {}
    pty.send_key = function(_, data)
        table.insert(pty._calls, { method = "send_key", data = data })
    end
    pty.send_mouse = function(_, data)
        table.insert(pty._calls, { method = "send_mouse", data = data })
    end
    return pty
end

---Create a mock Pane
---@param id integer
---@return Pane
function M.mock_pane(id)
    return { type = "pane", id = id, pty = M.mock_pty(id) }
end

---Create a mock Split
---@param id integer
---@param direction "row"|"col"
---@param children (Pane|Split)[]
---@return Split
function M.mock_split(id, direction, children)
    return { type = "split", split_id = id, direction = direction, children = children }
end

---Create a mock Tab with optional floating pane
---@param root Pane|Split
---@param floating_pane? Pane
---@return Tab
function M.mock_tab(root, floating_pane)
    local tab = { id = 1, root = root, last_focused_id = 1 }
    if floating_pane then
        tab.floating = { pane = floating_pane, visible = true }
    end
    return tab
end

---Install the prise mock module with all fields any branch needs.
---Returns the mock table so callers can override specific fields.
---@return table
function M.setup_prise_mock()
    local mock = {
        tiling = function() end,
        ---@param opts { pty: Pty }
        ---@return table
        Terminal = function(opts)
            return { type = "terminal", pty = opts.pty }
        end,
        Text = function(_)
            return { type = "text" }
        end,
        Column = function(_)
            return { type = "column" }
        end,
        Row = function(_)
            return { type = "row" }
        end,
        Stack = function(_)
            return { type = "stack" }
        end,
        Positioned = function(_)
            return { type = "positioned" }
        end,
        TextInput = function(_)
            return { type = "text_input" }
        end,
        List = function(_)
            return { type = "list" }
        end,
        Box = function(_)
            return { type = "box" }
        end,
        Padding = function(_)
            return { type = "padding" }
        end,
        gwidth = function(s)
            return #s
        end,
        request_frame = function() end,
        save = function() end,
        exit = function() end,
        get_session_name = function()
            return "test"
        end,
        -- Agent infrastructure (feat/spawn-pty)
        attach = function() end,
        switch_session = function()
            return true
        end,
        rename_session = function() end,
        create_session = function() end,
        -- Text input (feat/tab-name-from-cwd, feat/rename-tab)
        create_text_input = function()
            return {
                text = function()
                    return ""
                end,
                clear = function() end,
                insert = function() end,
            }
        end,
        -- Logging (multiple branches)
        log = { debug = function() end, info = function() end },
        -- Session switch support
        set_timeout = function(_, _)
            return { cancel = function() end }
        end,
        get_git_branch = function()
            return nil
        end,
        get_time = function()
            return "12:00"
        end,
        -- Session listing
        list_sessions = function()
            return {}
        end,
    }
    package.loaded["prise"] = mock
    return mock
end

return M
