# NAME

prise - configuration file

# SYNOPSIS

*~/.config/prise/init.lua*

# DESCRIPTION

Prise is configured through a Lua file at **~/.config/prise/init.lua**. This
file must return a UI table that implements the prise UI interface.

If no configuration file is present, prise uses the default tiling UI.

The simplest configuration uses the built-in tiling UI:

```lua
return require("prise").tiling()
```

# SETUP OPTIONS

The tiling UI can be customized by calling **setup()** before returning:

```lua
local ui = require("prise").tiling()

ui.setup({
    theme = { ... },
    status_bar = { ... },
    tab_bar = { ... },
    floating = { ... },
    keybinds = { ... },
    layouts = { ... },
    default_layout = "...",
})

return ui
```

# THEME

The **theme** table configures colors. All values are hex color strings.

**mode_normal**
:   Color for normal mode indicator in status bar. Default: **#89b4fa**

**mode_command**
:   Color for command mode indicator. Default: **#f38ba8**

**bg1**
:   Darkest background. Default: **#1e1e2e**

**bg2**
:   Dark background. Default: **#313244**

**bg3**
:   Medium background. Default: **#45475a**

**bg4**
:   Lighter background. Default: **#585b70**

**fg_bright**
:   Main text color. Default: **#cdd6f4**

**fg_dim**
:   Secondary text color. Default: **#a6adc8**

**fg_dark**
:   Dark text (on light backgrounds). Default: **#1e1e2e**

**accent**
:   Accent color. Default: **#89b4fa**

**green**
:   Success/connected color. Default: **#a6e3a1**

**yellow**
:   Warning color. Default: **#f9e2af**

Example:

```lua
ui.setup({
    theme = {
        accent = "#ff79c6",
        mode_normal = "#50fa7b",
    },
})
```

# BORDERS

The **borders** table configures pane borders for visual separation.

**enabled**
:   Enable or disable pane borders globally. Default: **false**

**show_single_pane**
:   Show border when only one pane exists. Default: **false**

**mode**
:   Border rendering mode. Options: **"box"**, **"separator"**. Default: **"box"**

**style**
:   Border drawing style. Options: **"none"**, **"single"**, **"double"**, **"rounded"**. Default: **"single"**

**focused_color**
:   Hex color code for the focused pane's border. Default: **#89b4fa**

**unfocused_color**
:   Hex color code for unfocused pane borders. Default: **#585b70**

Available border modes:

- **"box"** - Full borders around each pane (default)
- **"separator"** - Tmux-style borders drawn only between panes

Available border styles:

- **"single"** - Single-line borders: `┌─┐│└┘`
- **"double"** - Double-line borders: `╔═╗║╚╝`
- **"rounded"** - Rounded corners: `╭─╮│╰╯`
- **"none"** - Invisible borders (for consistent spacing)

Example:

```lua
ui.setup({
    borders = {
        enabled = true,
        mode = "box",                       -- or "separator" for tmux-style
        show_single_pane = false,           -- Hide border for single pane
        style = "rounded",
        focused_color = "#f38ba8",          -- Pink
        unfocused_color = "#313244",        -- Dark gray
    },
})
```

# STATUS BAR

The **status_bar** table configures the bottom status bar.

**enabled**
:   Show the status bar. Default: **true**

Example:

```lua
ui.setup({
    status_bar = { enabled = false },
})
```

# TAB BAR

The **tab_bar** table configures the tab bar.

**show_single_tab**
:   Show the tab bar even with only one tab. Default: **false**

# LAYOUTS

The **layouts** table defines named layout presets for tabs and panes.

Each layout has:

**tabs**
:   List of tabs. Each tab needs a **root** node and can set **title** or **floating**.

**root**
:   A pane or split definition.

**pane**
:   `{ type = "pane", cwd = "...", cmd = "...", ratio = 0.5 }`

**split**
:   `{ type = "split", direction = "horizontal"|"vertical", ratio = 0.5, children = { ... } }`

**floating**
:   Optional floating pane for a tab. `{ pane = { ... }, visible = true, width = 120, height = 40 }`

**default_layout**
:   Name of a layout to apply on startup when no session exists.

Example:

```lua
ui.setup({
    layouts = {
        work = {
            name = "work",
            tabs = {
                {
                    title = "editor",
                    root = {
                        type = "split",
                        direction = "horizontal",
                        children = {
                            { type = "pane", cwd = "~/code" },
                            { type = "pane", cmd = "git status" },
                        },
                    },
                    floating = {
                        pane = { type = "pane", cmd = "htop" },
                        visible = false,
                    },
                },
            },
        },
    },
    default_layout = "work",
})
```

# FLOATING PANE

The **floating** table configures the floating terminal pane.

**width**
:   Width in columns. Default: **100**

**height**
:   Height in rows. Default: **30**

Example:

```lua
ui.setup({
    floating = {
        width = 120,
        height = 40,
    },
})
```

# MACOS OPTION KEY

**macos_option_as_alt**
:   Controls how the macOS Option key behaves. Options: **"false"**, **"left"**, **"right"**, **"true"**. Default: **"false"**

When set to **"false"**, Option produces special characters (e.g., Option+e for é).
When set to **"left"**, **"right"**, or **"true"**, the corresponding Option key(s)
act as Alt for keybindings and terminal applications.

Example:

```lua
ui.setup({
    macos_option_as_alt = "true",
})
```

# LEADER KEY

**leader**
:   The leader key sequence used as a prefix for keybindings. Uses vim-style
    notation (see Key Notation below). Default: **"<D-k>"** (Super+k)

Example:

```lua
ui.setup({
    leader = "<C-a>",  -- Use Ctrl+a as leader
})
```

# CUSTOM KEYBINDS

The **keybinds** table maps vim-style key strings to either built-in actions
or custom Lua functions.

Each entry maps a key string to:

- A **string**: The name of a built-in action (e.g., "split_horizontal")
- A **function**: A custom Lua function to execute

Key strings use vim-style notation with angle brackets for modifiers and
special keys. Plain characters can be written directly.

## Modifiers

- **<C-x>** - Ctrl+x
- **<A-x>** - Alt+x
- **<S-x>** - Shift+x
- **<D-x>** - Super/Cmd+x
- **<leader>** - Expands to the configured leader key

Modifiers can be combined: **<C-A-x>** for Ctrl+Alt+x, **<C-S-D-a>** for
Ctrl+Shift+Super+a.

## Special Keys

- **<Enter>**, **<Return>**, **<CR>** - Enter key
- **<Tab>** - Tab key
- **<Esc>**, **<Escape>** - Escape key
- **<Space>** - Space bar
- **<BS>**, **<Backspace>** - Backspace key
- **<Del>**, **<Delete>** - Delete key
- **<Up>**, **<Down>**, **<Left>**, **<Right>** - Arrow keys
- **<Home>**, **<End>** - Home/End keys
- **<PageUp>**, **<PageDown>** - Page Up/Down keys
- **<Insert>** - Insert key
- **<F1>** through **<F12>** - Function keys

## Examples

- **a** - The letter "a"
- **<C-a>** - Ctrl+a
- **<D-k>v** - Super+k followed by v
- **<leader>s** - Leader followed by s
- **<C-S-Tab>** - Ctrl+Shift+Tab

Example:

```lua
local prise = require("prise")
local ui = prise.tiling()

ui.setup({
    leader = "<C-a>",  -- Use Ctrl+a as leader
    keybinds = {
        -- Built-in action
        ["<leader>v"] = "split_horizontal",

        -- Custom function
        ["<leader>g"] = function()
            prise.log.info("Custom keybind executed!")
        end,
    },
})

return ui
```

# BUILT-IN ACTIONS

The following actions can be used as values in the **keybinds** table.

## Pane Management

**split_horizontal**
:   Split the current pane horizontally (side by side)

**split_vertical**
:   Split the current pane vertically (stacked)

**split_auto**
:   Split automatically based on pane dimensions (horizontal if wide, vertical if tall)

Splitting a zoomed pane exits zoom first so the new pane is visible immediately.

**close_pane**
:   Close the current pane

**toggle_zoom**
:   Toggle zoom on the current pane (maximize/restore)

Zoom state is remembered per tab and restored when you switch back to that tab.

## Focus Navigation

**focus_left**
:   Move focus to the pane on the left

**focus_right**
:   Move focus to the pane on the right

**focus_up**
:   Move focus to the pane above

**focus_down**
:   Move focus to the pane below

## Pane Resizing

**resize_left**
:   Shrink the current pane horizontally

**resize_right**
:   Grow the current pane horizontally

**resize_up**
:   Shrink the current pane vertically

**resize_down**
:   Grow the current pane vertically

## Tab Management

**new_tab**
:   Create a new tab

**close_tab**
:   Close the current tab

**rename_tab**
:   Rename the current tab

**next_tab**
:   Switch to the next tab

**previous_tab**
:   Switch to the previous tab

**tab_1** through **tab_10**
:   Switch to tab by number

## Session Management

**detach_session**
:   Detach from the current session

**rename_session**
:   Rename the current session

**quit**
:   Quit prise (same as detach)

## Other

**layout_picker**
:   Open the layout picker

**command_palette**
:   Open the command palette

## Floating Pane

**floating_toggle**
:   Toggle the floating terminal pane

**floating_increase_size**
:   Increase floating pane size

**floating_decrease_size**
:   Decrease floating pane size

# DEFAULT KEYBINDS

The tiling UI uses a leader key sequence. Press the leader key (default:
**Super+k**), then one of:

**v**
:   Split horizontal

**s**
:   Split vertical

**Enter**
:   Split auto (horizontal if wide, vertical if tall)

**h**, **j**, **k**, **l**
:   Focus left, down, up, right

**H**, **J**, **K**, **L**
:   Resize pane left, down, up, right

**w**
:   Close pane

**z**
:   Toggle zoom (maximize current pane)

**t**
:   New tab

**c**
:   Close tab

**n**, **p**
:   Next/previous tab

**1-9**
:   Switch to tab by number

**r**
:   Rename current tab

**d**
:   Detach from session

**q**
:   Quit prise

**0**
:   Switch to tab 10

**f**
:   Toggle floating pane

**o**
:   Open layout picker

**+**
:   Increase floating pane size

**-**
:   Decrease floating pane size

The command palette (**Super+p**) provides fuzzy search for all commands.

# SEE ALSO

[prise(1)](prise.1.html), [prise(7)](prise.7.html)
