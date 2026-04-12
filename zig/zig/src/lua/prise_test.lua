local prise = require("prise")

---Create a mock Pty for testing
---@return Pty
local function mock_pty()
    ---@type Pty
    local pty = {
        id = function()
            return 1
        end,
        title = function()
            return "test"
        end,
        cwd = function()
            return "/tmp"
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
    return pty
end

---Create a mock TextInput for testing
---@return TextInput
local function mock_text_input()
    ---@type TextInput
    local input = {
        id = function()
            return 1
        end,
        text = function()
            return ""
        end,
        insert = function() end,
        delete_backward = function() end,
        delete_forward = function() end,
        move_left = function() end,
        move_right = function() end,
        move_to_start = function() end,
        move_to_end = function() end,
        clear = function() end,
        destroy = function() end,
    }
    return input
end

-- Test: Terminal creates correct widget
local term = prise.Terminal({ pty = mock_pty(), ratio = 0.5, id = "t1", focus = true, show_cursor = false })
assert(term.type == "terminal", "Terminal: type should be 'terminal'")
assert(term.pty ~= nil, "Terminal: pty should be set")
assert(term.ratio == 0.5, "Terminal: ratio should be set")
assert(term.id == "t1", "Terminal: id should be set")
assert(term.focus == true, "Terminal: focus should be set")
assert(term.show_cursor == false, "Terminal: show_cursor should be set")

-- Test: Text with string creates content array
local text1 = prise.Text("hello")
assert(text1.type == "text", "Text(string): type should be 'text'")
assert(text1.content[1] == "hello", "Text(string): content should contain the string")

-- Test: Text with array of segments
local text2 = prise.Text({ { text = "a" }, { text = "b" } })
assert(text2.type == "text", "Text(array): type should be 'text'")
assert(text2.content[1].text == "a", "Text(array): first segment should be preserved")
assert(text2.content[2].text == "b", "Text(array): second segment should be preserved")

-- Test: Text with single segment (has 'text' but not 'content')
local text3 = prise.Text({ text = "single", style = { fg = "red" } })
assert(text3.type == "text", "Text(segment): type should be 'text'")
assert(text3.content[1].text == "single", "Text(segment): should wrap in content array")
assert(text3.content[1].style.fg == "red", "Text(segment): style should be preserved")

-- Test: Text with TextOpts (has 'content' key)
local text4 = prise.Text({ content = { "x", "y" }, show_cursor = true })
assert(text4.type == "text", "Text(opts): type should be 'text'")
assert(text4.content[1] == "x", "Text(opts): content should be set")
assert(text4.show_cursor == true, "Text(opts): show_cursor should be set")

-- Test: Text with empty TextOpts
local text5 = prise.Text({})
assert(text5.type == "text", "Text(empty): type should be 'text'")
assert(#text5.content == 0, "Text(empty): content should be empty array")

-- Test: Column with array of children
local col1 = prise.Column({ { type = "text" }, { type = "text" } })
assert(col1.type == "column", "Column(array): type should be 'column'")
assert(#col1.children == 2, "Column(array): should have 2 children")

-- Test: Column with LayoutOpts
local col2 = prise.Column({
    children = { { type = "text" } },
    ratio = 0.3,
    id = "col1",
    cross_axis_align = "center",
    resizable = true,
    show_cursor = false,
})
assert(col2.type == "column", "Column(opts): type should be 'column'")
assert(#col2.children == 1, "Column(opts): should have 1 child")
assert(col2.ratio == 0.3, "Column(opts): ratio should be set")
assert(col2.id == "col1", "Column(opts): id should be set")
assert(col2.cross_axis_align == "center", "Column(opts): cross_axis_align should be set")
assert(col2.resizable == true, "Column(opts): resizable should be set")
assert(col2.show_cursor == false, "Column(opts): show_cursor should be set")

-- Test: Row with array of children
local row1 = prise.Row({ { type = "text" }, { type = "text" } })
assert(row1.type == "row", "Row(array): type should be 'row'")
assert(#row1.children == 2, "Row(array): should have 2 children")

-- Test: Row with LayoutOpts
local row2 = prise.Row({
    children = { { type = "text" } },
    ratio = 0.7,
    id = "row1",
    cross_axis_align = "stretch",
    resizable = false,
    show_cursor = true,
})
assert(row2.type == "row", "Row(opts): type should be 'row'")
assert(#row2.children == 1, "Row(opts): should have 1 child")
assert(row2.ratio == 0.7, "Row(opts): ratio should be set")
assert(row2.id == "row1", "Row(opts): id should be set")
assert(row2.cross_axis_align == "stretch", "Row(opts): cross_axis_align should be set")
assert(row2.resizable == false, "Row(opts): resizable should be set")
assert(row2.show_cursor == true, "Row(opts): show_cursor should be set")

-- Test: Stack with array of children
local stack1 = prise.Stack({ { type = "text" }, { type = "box" } })
assert(stack1.type == "stack", "Stack(array): type should be 'stack'")
assert(#stack1.children == 2, "Stack(array): should have 2 children")

-- Test: Stack with LayoutOpts
local stack2 = prise.Stack({ children = { { type = "text" } }, ratio = 0.5, id = "s1" })
assert(stack2.type == "stack", "Stack(opts): type should be 'stack'")
assert(#stack2.children == 1, "Stack(opts): should have 1 child")
assert(stack2.ratio == 0.5, "Stack(opts): ratio should be set")
assert(stack2.id == "s1", "Stack(opts): id should be set")

-- Test: Stack with empty opts
local stack3 = prise.Stack({})
assert(stack3.type == "stack", "Stack(empty): type should be 'stack'")
assert(#stack3.children == 0, "Stack(empty): children should be empty array")

-- Test: Positioned with opts
local pos1 =
    prise.Positioned({ child = { type = "text" }, x = 10, y = 20, anchor = "top_left", ratio = 0.5, id = "p1" })
assert(pos1.type == "positioned", "Positioned: type should be 'positioned'")
assert(pos1.child.type == "text", "Positioned: child should be set")
assert(pos1.x == 10, "Positioned: x should be set")
assert(pos1.y == 20, "Positioned: y should be set")
assert(pos1.anchor == "top_left", "Positioned: anchor should be set")
assert(pos1.ratio == 0.5, "Positioned: ratio should be set")
assert(pos1.id == "p1", "Positioned: id should be set")

-- Test: Positioned with child as first array element
local pos2 = prise.Positioned({ { type = "box" }, x = 5 })
assert(pos2.child.type == "box", "Positioned(array): should use first element as child")
assert(pos2.x == 5, "Positioned(array): x should be set")

-- Test: TextInput
local input = prise.TextInput({ input = mock_text_input(), style = { fg = "blue" }, focus = true })
assert(input.type == "text_input", "TextInput: type should be 'text_input'")
assert(input.input ~= nil, "TextInput: input should be set")
assert(input.style.fg == "blue", "TextInput: style should be set")
assert(input.focus == true, "TextInput: focus should be set")

-- Test: List with ListOpts
local list1 = prise.List({
    items = { "a", "b", "c" },
    selected = 2,
    scroll_offset = 1,
    style = { bg = "black" },
    selected_style = { bg = "blue" },
})
assert(list1.type == "list", "List(opts): type should be 'list'")
assert(#list1.items == 3, "List(opts): items should have 3 elements")
assert(list1.selected == 2, "List(opts): selected should be set")
assert(list1.scroll_offset == 1, "List(opts): scroll_offset should be set")
assert(list1.style.bg == "black", "List(opts): style should be set")
assert(list1.selected_style.bg == "blue", "List(opts): selected_style should be set")

-- Test: List with array shorthand
local list2 = prise.List({ "x", "y" })
assert(list2.type == "list", "List(array): type should be 'list'")
assert(list2.items[1] == "x", "List(array): should use array as items")
assert(list2.items[2] == "y", "List(array): should preserve order")

-- Test: Box with BoxOpts
local box1 = prise.Box({
    child = { type = "text" },
    border = "single",
    style = { bg = "gray" },
    max_width = 80,
    max_height = 24,
})
assert(box1.type == "box", "Box(opts): type should be 'box'")
assert(box1.child.type == "text", "Box(opts): child should be set")
assert(box1.border == "single", "Box(opts): border should be set")
assert(box1.style.bg == "gray", "Box(opts): style should be set")
assert(box1.max_width == 80, "Box(opts): max_width should be set")
assert(box1.max_height == 24, "Box(opts): max_height should be set")

-- Test: Box with child as first array element
local box2 = prise.Box({ { type = "row" }, border = "double" })
assert(box2.child.type == "row", "Box(array): should use first element as child")
assert(box2.border == "double", "Box(array): border should be set")

-- Test: Padding with all options
local pad1 = prise.Padding({
    child = { type = "text" },
    all = 2,
    top = 1,
    bottom = 3,
    left = 4,
    right = 5,
})
assert(pad1.type == "padding", "Padding(opts): type should be 'padding'")
assert(pad1.child.type == "text", "Padding(opts): child should be set")
assert(pad1.all == 2, "Padding(opts): all should be set")
assert(pad1.top == 1, "Padding(opts): top should be set")
assert(pad1.bottom == 3, "Padding(opts): bottom should be set")
assert(pad1.left == 4, "Padding(opts): left should be set")
assert(pad1.right == 5, "Padding(opts): right should be set")

-- Test: Padding with child as first array element
local pad2 = prise.Padding({ { type = "column" }, all = 1 })
assert(pad2.child.type == "column", "Padding(array): should use first element as child")
assert(pad2.all == 1, "Padding(array): all should be set")
