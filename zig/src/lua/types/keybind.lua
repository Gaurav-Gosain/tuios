---@meta

---Result from keybind matcher handle_key
---@class KeybindMatchResult
---@field action? string Action name if matched (for built-in actions)
---@field func? function Lua function if matched (for custom functions)
---@field key_string? string Original key string that matched
---@field pending? boolean True if key sequence in progress
---@field none? boolean True if no match

---Keybind matcher for matching key events against compiled keybinds
---@class KeybindMatcher
local KeybindMatcher = {}

---Process a key event and return the match result
---@param key_data table Key event data with key, ctrl, alt, shift, super fields
---@return KeybindMatchResult
function KeybindMatcher:handle_key(key_data) end

---Check if a key sequence is in progress
---@return boolean
function KeybindMatcher:is_pending() end

---Reset the matcher to initial state
function KeybindMatcher:reset() end

---Keybind module for compiling and matching keybinds
---@class KeybindModule
local KeybindModule = {}

---Compile keybinds into a matcher
---@param keybinds table<string, string|function> Map of key_string to action name or function
---@param leader? string Leader key sequence for <leader> expansion
---@return KeybindMatcher
function KeybindModule.compile(keybinds, leader) end

---Parse a key string into key objects
---@param input string Vim-style key string like "<D-k>v"
---@return table[] Array of key objects
function KeybindModule.parse_key_string(input) end

return KeybindModule
