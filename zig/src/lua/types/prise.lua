---@meta

---Log functions provided by tuios
---@class PriseLog
---@field debug fun(msg: string) Log a debug message
---@field info fun(msg: string) Log an info message
---@field warn fun(msg: string) Log a warning message
---@field err fun(msg: string) Log an error message
---@field error fun(msg: string) Log an error message (alias)

---Spawn options for creating new PTYs
---@class SpawnOptions
---@field cwd? string Working directory for the new process

---The tuios module provides core functionality for the terminal multiplexer
---@class tuios
---@field log PriseLog Logging functions
---@field platform "macos"|"linux"|"windows"|"unknown" Current platform
---@field keybind KeybindModule Keybind compilation and matching
local tuios = {}

---Load the tiling UI module
---@return PriseUI
function tuios.tiling() end

---Create a terminal widget that displays a PTY
---@param opts TerminalOpts
---@return table Terminal widget
function tuios.Terminal(opts) end

---Create a text widget with optional styling and segments
---@param opts string|TextSegment[]|TextOpts
---@return table Text widget
function tuios.Text(opts) end

---Create a column layout that arranges children vertically
---@param opts table[]|LayoutOpts
---@return table Column widget
function tuios.Column(opts) end

---Create a row layout that arranges children horizontally
---@param opts table[]|LayoutOpts
---@return table Row widget
function tuios.Row(opts) end

---Create a stacked layout that overlays children on top of each other
---@param opts table[]|LayoutOpts
---@return table Stack widget
function tuios.Stack(opts) end

---Create a positioned widget that places a child at absolute coordinates
---@param opts PositionedOpts
---@return table Positioned widget
function tuios.Positioned(opts) end

---Create a text input widget for capturing user input
---@param opts TextInputOpts
---@return table TextInput widget
function tuios.TextInput(opts) end

---Create a list widget with items and optional selection
---@param opts ListOpts|string[]
---@return table List widget
function tuios.List(opts) end

---Create a box widget with border and styling options
---@param opts BoxOpts
---@return table Box widget
function tuios.Box(opts) end

---Create a padding widget that adds spacing around a child
---@param opts PaddingOpts
---@return table Padding widget
function tuios.Padding(opts) end

---Set a timeout to call a function after a delay
---@param ms integer Milliseconds to wait
---@param callback fun() Function to call
---@return Timer
function tuios.set_timeout(ms, callback) end

---Exit the application (deletes session)
function tuios.exit() end

---Spawn a new PTY process
---@param opts? SpawnOptions
function tuios.spawn(opts) end

---Request a frame redraw
function tuios.request_frame() end

---Detach from the current session
---@param session_name? string Optional session name to switch to
function tuios.detach(session_name) end

---Get the next available session name
---@return string
function tuios.next_session_name() end

---Trigger an auto-save of the session
function tuios.save() end

---Get the current session name
---@return string?
function tuios.get_session_name() end

---Rename a session
---@param old_name string The current session name
---@param new_name string The new session name
function tuios.rename_session(old_name, new_name) end

---Delete a session
---@param session_name string
function tuios.delete_session(session_name) end

---List all available sessions
---@return string[]
function tuios.list_sessions() end

---Switch to a different session
---@param target_session string The session name to switch to
function tuios.switch_session(target_session) end

---Create a new TextInput handle
---@return TextInput
function tuios.create_text_input() end

---Get the grapheme width of a string (for proper Unicode handling)
---@param str string
---@return integer
function tuios.gwidth(str) end

---Get the current time formatted as HH:MM
---@return string
function tuios.get_time() end

---Get the git branch for a directory
---@param cwd string The directory to check
---@return string? The branch name, or nil if not a git repo
function tuios.get_git_branch(cwd) end

return tuios
