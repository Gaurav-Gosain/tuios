# Configuration Guide

TUIOS supports user-configurable keybindings through a TOML configuration file, following the XDG Base Directory specification.

## Table of Contents

- [Quick Start](#quick-start)
- [Configuration File Location](#configuration-file-location)
- [Configuration Structure](#configuration-structure)
- [Keybinding Sections](#keybinding-sections)
- [Key Syntax](#key-syntax)
- [Platform-Specific Configuration](#platform-specific-configuration)
- [Best Practices](#best-practices)
- [Troubleshooting](#troubleshooting)

## Quick Start

### Find Your Configuration

```bash
tuios --config-path
```

### Edit Configuration

```bash
tuios --edit-config
```

### View Current Keybindings

```bash
# View all keybindings
tuios --list-keybinds

# View only your customizations
tuios --list-custom-keybinds
```

### Reset to Defaults

```bash
tuios --reset-config
```

## Configuration File Location

**Default path:** `~/.config/tuios/config.toml`

On first launch, TUIOS automatically creates a default configuration file. The exact location follows the XDG Base Directory specification:

- Linux/macOS: `~/.config/tuios/config.toml`
- Custom: `$XDG_CONFIG_HOME/tuios/config.toml` (if `XDG_CONFIG_HOME` is set)

## Configuration Structure

The configuration file uses TOML format with the following structure:

```toml
[keybindings.window_management]
new_window = ["n"]
close_window = ["w", "x"]
# ... more keybindings

[keybindings.workspaces]
switch_workspace_1 = ["alt+1"]
# ... more workspaces

[keybindings.layout]
snap_left = ["h"]
# ... more layouts

# ... additional sections
```

### Minimal Configuration (Recommended)

You only need to specify what you want to customize. TUIOS automatically fills in missing keybindings with defaults:

```toml
# ~/.config/tuios/config.toml
# Only customize what you need!

[keybindings.window_management]
new_window = ["ctrl+t"]
close_window = ["ctrl+w"]

# Everything else uses defaults automatically
```

**Benefits:**
- Shorter, cleaner configuration
- Automatic updates when new features are added
- Easy to see what you've customized
- Less maintenance required

## Keybinding Sections

### window_management
Window creation, navigation, and control.

**Available actions:**
- `new_window` - Create new terminal window
- `close_window` - Close focused window
- `rename_window` - Rename focused window
- `minimize_window` - Minimize focused window
- `restore_all` - Restore all minimized windows
- `next_window` - Focus next window
- `prev_window` - Focus previous window
- `select_window_1` through `select_window_9` - Select window by number

### workspaces
Workspace switching and window movement.

**Available actions:**
- `switch_workspace_1` through `switch_workspace_9` - Switch to workspace N
- `move_and_follow_1` through `move_and_follow_9` - Move window to workspace N and follow

### layout
Window positioning and tiling.

**Available actions:**
- `snap_left` - Snap window to left half
- `snap_right` - Snap window to right half
- `snap_fullscreen` - Fullscreen window
- `unsnap` - Unsnap window from position
- `snap_corner_1` through `snap_corner_4` - Snap to corners (TL, TR, BL, BR)
- `toggle_tiling` - Toggle automatic tiling mode
- `swap_left`, `swap_right`, `swap_up`, `swap_down` - Swap windows in tiling mode

### mode_control
Mode switching and application control.

**Available actions:**
- `enter_terminal_mode` - Enter terminal mode (input goes to terminal)
- `enter_window_mode` - Enter window management mode
- `toggle_help` - Toggle help overlay
- `quit` - Quit TUIOS

### system
System-level controls. This section is currently empty as debug commands have been moved to the debug_prefix submenu.

**Note:** Debug commands (logs, cache stats) are accessible via `Ctrl+B D` submenu and are not directly configurable as keybindings. See the `debug_prefix` section below.

### navigation
Arrow key navigation for window movement and selection extension.

**Available actions:**
- `nav_up`, `nav_down`, `nav_left`, `nav_right` - Arrow key navigation
- `select_up`, `select_down`, `select_left`, `select_right` - Shift + arrow keys for selection extension

### restore_minimized
Individual minimized window restoration by number.

**Available actions:**
- `restore_minimized_1` through `restore_minimized_9` - Restore specific minimized window by number (Shift+1 through Shift+9)

### prefix_mode
Tmux-style prefix commands (Ctrl+B followed by another key). Not directly configurable - prefix commands are hardcoded.

### window_prefix, minimize_prefix, workspace_prefix
Sub-menus accessible after prefix key (Ctrl+B + w/m/t). These provide alternative access to window management, minimize, and workspace commands through the prefix interface.

### debug_prefix
Debug and development tools submenu (Ctrl+B + D).

**Available actions:**
- `debug_prefix_logs` - Toggle log viewer (Ctrl+B D l)
- `debug_prefix_cache` - Toggle cache statistics (Ctrl+B D c)
- `debug_prefix_cancel` - Cancel debug prefix mode (Esc)

## Key Syntax

### Modifier Keys

**Supported modifiers:**
- `ctrl+` - Control key
- `alt+` - Alt key
- `shift+` - Shift key
- `opt+`, `option+` - Option key (macOS only, equivalent to alt)

**Not supported:**
- `cmd+`, `super+` - Not supported (typically captured by OS)

### Special Keys

- `enter`, `return` - Enter key
- `esc`, `escape` - Escape key
- `tab` - Tab key
- `space` - Space bar
- `backspace` - Backspace key
- `delete` - Delete key
- `up`, `down`, `left`, `right` - Arrow keys
- `home`, `end` - Home/End keys
- `pgup`, `pageup`, `pgdown`, `pagedown` - Page Up/Down
- `f1` through `f12` - Function keys

### Key Combinations

```toml
"ctrl+shift+t"  # Control + Shift + T
"alt+enter"     # Alt + Enter
"shift+tab"     # Shift + Tab
"opt+1"         # Option + 1 (macOS only)
```

### Multiple Keybindings

Bind multiple keys to the same action:

```toml
new_window = ["n", "ctrl+n", "ctrl+t"]
```

### Removing Keybindings

Use an empty array to disable a keybinding:

```toml
close_window = []  # Disables this action
```

## Platform-Specific Configuration

### macOS

On macOS, TUIOS supports the Option key (displayed as "opt" or "option" on Mac keyboards).

**Default workspace switching:**
```toml
[keybindings.workspaces]
switch_workspace_1 = ["opt+1"]
switch_workspace_2 = ["opt+2"]
# ... etc
```

**Key expansion:** When you use `opt+1`, TUIOS automatically handles:
1. The actual `alt+1` key combination
2. The unicode character produced by Option+1 (¡)

**Typing unicode characters:** You can still type Option key unicode characters in terminal mode. Only in window management mode do these trigger actions.

**Equivalent notations:**
- `opt+1` - Recommended (Mac-friendly)
- `option+1` - Also supported
- `alt+1` - Works but less intuitive for Mac users

### Linux/Other Platforms

Use standard modifiers only:
- `alt+1`, `ctrl+1`, etc.
- `opt+` and `option+` are not valid and will cause configuration errors

## Best Practices

### Use Minimal Configuration

Only specify customizations:

```toml
# Good - only your changes
[keybindings.window_management]
new_window = ["ctrl+t"]

# Avoid - copying entire default config
# (makes updates harder and obscures your customizations)
```

### Group Related Customizations

```toml
# Browser-style shortcuts
[keybindings.window_management]
new_window = ["ctrl+t"]
close_window = ["ctrl+w"]
next_window = ["ctrl+tab"]
prev_window = ["ctrl+shift+tab"]
```

### Check Your Customizations

```bash
tuios --list-custom-keybinds
```

This shows only what you've changed, making it easy to review.

### Comment Your Configuration

```toml
[keybindings.window_management]
new_window = ["ctrl+t"]  # Browser-style new tab
close_window = ["ctrl+w"]  # Browser-style close
```

## Troubleshooting

### Configuration Not Loading

1. Check file location:
```bash
tuios --config-path
```

2. Verify TOML syntax:
   - Strings must be quoted: `"key"`
   - Arrays use brackets: `["key1", "key2"]`
   - Section headers: `[keybindings.section_name]`

3. Check startup logs (run with `--debug`):
```bash
tuios --debug
```

### Invalid Key Syntax Errors

Common errors:
- `"cmd+t"` - cmd/super not supported
- `"opt+1"` on Linux - opt only valid on macOS
- `"ctrl+"` - incomplete combination
- `"ctrl+ctrl+a"` - duplicate modifier

### Keybinding Conflicts

If the same key is bound to multiple actions, TUIOS will warn you during startup. The last binding takes precedence.

View conflicts:
```bash
tuios --list-keybinds | grep <your-key>
```

### Platform Detection Issues

If macOS-specific keys aren't working:

```bash
echo $GOOS    # Should be "darwin" on macOS
echo $OSTYPE  # Should contain "darwin" on macOS
```

### Applying Changes

Configuration is loaded on startup. To apply changes:

1. Edit configuration
2. Quit TUIOS (press `q` in window management mode)
3. Restart TUIOS

## Example Configurations

### Vim-Style

```toml
[keybindings.mode_control]
enter_terminal_mode = ["i", "a"]
enter_window_mode = ["esc"]

[keybindings.window_management]
new_window = ["ctrl+t"]
close_window = ["ctrl+w"]
```

### Browser-Style

```toml
[keybindings.window_management]
new_window = ["ctrl+t"]
close_window = ["ctrl+w"]
next_window = ["ctrl+tab"]
prev_window = ["ctrl+shift+tab"]
```

### Tmux-Like

```toml
[keybindings.prefix_mode]
prefix_new_window = ["c"]
prefix_close_window = ["x"]
prefix_next_window = ["n"]
prefix_prev_window = ["p"]
```

## Related Documentation

- [CLI Reference](CLI_REFERENCE.md) - Command-line options
- [Keybindings Reference](KEYBINDINGS.md) - Default keybindings
- [README](../README.md) - Project overview
