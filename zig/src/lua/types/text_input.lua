---@meta

---TextInput userdata for capturing user text input
---@class TextInput
local TextInput = {}

---Get the TextInput's numeric ID
---@return integer
function TextInput:id() end

---Get the current text content
---@return string
function TextInput:text() end

---Insert text at the cursor position
---@param text string
function TextInput:insert(text) end

---Delete the character before the cursor
function TextInput:delete_backward() end

---Delete the character after the cursor
function TextInput:delete_forward() end

---Delete the word before the cursor
function TextInput:delete_word_backward() end

---Delete from cursor to end of line
function TextInput:kill_line() end

---Move the cursor one position to the left
function TextInput:move_left() end

---Move the cursor one position to the right
function TextInput:move_right() end

---Move the cursor to the start of the text
function TextInput:move_to_start() end

---Move the cursor to the end of the text
function TextInput:move_to_end() end

---Clear all text and reset cursor
function TextInput:clear() end

---Destroy the TextInput and free resources
function TextInput:destroy() end
