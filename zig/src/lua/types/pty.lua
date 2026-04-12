---@meta

---PTY size information
---@class PtySize
---@field rows integer Number of rows
---@field cols integer Number of columns
---@field width_px integer Width in pixels
---@field height_px integer Height in pixels

---Key data for send_key
---@class PtyKeyData
---@field key string The key character
---@field code? string The key code
---@field ctrl? boolean Ctrl modifier
---@field alt? boolean Alt modifier
---@field shift? boolean Shift modifier
---@field super? boolean Super/Cmd modifier
---@field release? boolean Key release event

---Mouse data for send_mouse
---@class PtyMouseData
---@field col? integer Column position
---@field row? integer Row position
---@field button? string Mouse button ("left", "right", "middle")
---@field action? string Mouse action ("press", "release", "move")
---@field ctrl? boolean Ctrl modifier
---@field alt? boolean Alt modifier
---@field shift? boolean Shift modifier

---PTY userdata representing a pseudo-terminal
---@class Pty
local Pty = {}

---Get the PTY's numeric ID
---@return integer
function Pty:id() end

---Get the PTY's title
---@return string
function Pty:title() end

---Get the current working directory
---@return string?
function Pty:cwd() end

---Get the PTY's size information
---@return PtySize
function Pty:size() end

---Send a key event
---@param key PtyKeyData
function Pty:send_key(key) end

---Send a mouse event
---@param mouse PtyMouseData
function Pty:send_mouse(mouse) end

---Send pasted text
---@param text string
function Pty:send_paste(text) end

---Set the focus state
---@param focused boolean
function Pty:set_focus(focused) end

---Close the PTY
function Pty:close() end

---Copy the current selection to clipboard
function Pty:copy_selection() end
