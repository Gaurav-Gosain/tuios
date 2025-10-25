# Keybindings Reference

Complete keyboard shortcut reference for TUIOS. All keybindings are customizable through the [configuration file](CONFIGURATION.md).

## Table of Contents

- [Modes](#modes)
- [Window Management](#window-management)
- [Workspaces](#workspaces)
- [Window Layout](#window-layout)
- [Copy Mode](#copy-mode)
- [Prefix Commands](#prefix-commands)
- [System Controls](#system-controls)

## Modes

TUIOS has two main modes:

- **Window Management Mode** - Navigate and manage windows (default on startup)
- **Terminal Mode** - Input goes directly to the focused terminal

| Key | Action |
|-----|--------|
| `i` or `Enter` | Enter Terminal Mode |
| `Ctrl+B` then `d` or `Esc` | Return to Window Management Mode (from Terminal Mode) |
| `?` (Window Mode) or `Ctrl+B ?` (universal) | Toggle help overlay |
| `q` (Window Mode) or `Ctrl+B q` (universal) | Quit TUIOS |

## Window Management

| Key | Action |
|-----|--------|
| `n` | Create new window |
| `w` or `x` | Close focused window |
| `r` | Rename focused window |
| `m` | Minimize focused window |
| `Shift+M` | Restore all minimized windows |
| `Tab` | Focus next window |
| `Shift+Tab` | Focus previous window |
| `1-9` | Select window by number |
| `Shift+1-9` or `!@#$%^&*(` | Restore minimized window by number |

## Workspaces

TUIOS supports 9 workspaces for organizing windows.

| Key | Action |
|-----|--------|
| `Alt+1` through `Alt+9` | Switch to workspace 1-9 |
| `Alt+Shift+1` through `Alt+Shift+9` | Move window to workspace and follow |

**macOS:** Use `Option+1` through `Option+9` (automatically configured by default)

## Window Layout

### Manual Snapping (Non-Tiling Mode)

| Key | Action |
|-----|--------|
| `h` | Snap window to left half |
| `l` | Snap window to right half |
| `f` | Fullscreen window |
| `u` | Unsnap/restore window |
| `1` | Snap to top-left corner |
| `2` | Snap to top-right corner |
| `3` | Snap to bottom-left corner |
| `4` | Snap to bottom-right corner |

### Tiling Mode

| Key | Action |
|-----|--------|
| `t` | Toggle automatic tiling mode |
| `Shift+H` or `Ctrl+Left` | Swap with window to the left |
| `Shift+L` or `Ctrl+Right` | Swap with window to the right |
| `Shift+K` or `Ctrl+Up` | Swap with window above |
| `Shift+J` or `Ctrl+Down` | Swap with window below |

## Copy Mode

Enter copy mode with `Ctrl+B` `[` to navigate scrollback and select text using vim-style commands.

### Basic Navigation

| Key | Action |
|-----|--------|
| `Ctrl+B` `[` | Enter copy mode |
| `h` `j` `k` `l` | Move cursor left/down/up/right |
| `w` `b` `e` | Word forward / word backward / word end |
| `0` `^` `$` | Start of line / first non-blank / end of line |
| `gg` | Jump to top of scrollback |
| `G` | Jump to bottom (live output) |
| `{number}G` | Jump to line number (e.g., `10G`) |
| `{` `}` | Jump to previous/next paragraph |
| `Ctrl+U` `Ctrl+D` | Half page up/down |
| `Ctrl+B` `Ctrl+F` | Full page up/down |
| `i` | Return to terminal mode |
| `q` or `Esc` | Exit copy mode |

### Count Prefix

Prefix any motion with a number to repeat it:
- `10j` - Move down 10 lines
- `5w` - Move forward 5 words
- `3{` - Jump up 3 paragraphs

### Character Search

| Key | Action |
|-----|--------|
| `f{char}` | Find next occurrence of char on line |
| `F{char}` | Find previous occurrence of char on line |
| `t{char}` | Move cursor before next char |
| `T{char}` | Move cursor after previous char |
| `;` | Repeat last character search |
| `,` | Repeat last search (opposite direction) |

### Search

| Key | Action |
|-----|--------|
| `/` | Search forward |
| `?` | Search backward |
| `n` | Next match |
| `N` | Previous match |
| `Ctrl+L` | Clear search highlights |

### Visual Selection

| Key | Action |
|-----|--------|
| `v` | Enter visual character mode |
| `V` | Enter visual line mode |
| `y` or `c` | Yank (copy) selection to clipboard |
| `Esc` or `q` | Exit visual mode |

### Other Commands

| Key | Action |
|-----|--------|
| `%` | Jump to matching bracket |

## Prefix Commands

Press `Ctrl+B`, release, then press the command key (tmux-style).

### Main Prefix (`Ctrl+B`)

| Key Sequence | Action |
|--------------|--------|
| `Ctrl+B` `c` | Create new window |
| `Ctrl+B` `x` | Close current window |
| `Ctrl+B` `,` or `r` | Rename window |
| `Ctrl+B` `n` or `Tab` | Next window |
| `Ctrl+B` `p` or `Shift+Tab` | Previous window |
| `Ctrl+B` `0-9` | Jump to window |
| `Ctrl+B` `Space` | Toggle tiling mode |
| `Ctrl+B` `z` | Fullscreen current window |
| `Ctrl+B` `w` | Enter workspace prefix menu |
| `Ctrl+B` `m` | Enter minimize prefix menu |
| `Ctrl+B` `t` | Enter window prefix menu |
| `Ctrl+B` `D` | Enter debug prefix menu |
| `Ctrl+B` `[` | Enter copy mode |
| `Ctrl+B` `d` or `Esc` | Detach (exit terminal mode) |
| `Ctrl+B` `q` | Quit TUIOS |
| `Ctrl+B` `?` | Toggle help |
| `Ctrl+B` `Ctrl+B` | Send literal Ctrl+B to terminal |

### Workspace Prefix (`Ctrl+B` `w`)

| Key Sequence | Action |
|--------------|--------|
| `Ctrl+B` `w` `1-9` | Switch to workspace |
| `Ctrl+B` `w` `Shift+1-9` | Move window to workspace and follow |
| `Ctrl+B` `w` `Esc` | Cancel |

### Minimize Prefix (`Ctrl+B` `m`)

| Key Sequence | Action |
|--------------|--------|
| `Ctrl+B` `m` `m` | Minimize focused window |
| `Ctrl+B` `m` `1-9` | Restore minimized window by number |
| `Ctrl+B` `m` `Shift+M` | Restore all minimized windows |
| `Ctrl+B` `m` `Esc` | Cancel |

### Window Prefix (`Ctrl+B` `t`)

Alternative prefix-based access to window commands:

| Key Sequence | Action |
|--------------|--------|
| `Ctrl+B` `t` `n` | Create new window |
| `Ctrl+B` `t` `x` | Close window |
| `Ctrl+B` `t` `r` | Rename window |
| `Ctrl+B` `t` `Tab` | Next window |
| `Ctrl+B` `t` `Shift+Tab` | Previous window |
| `Ctrl+B` `t` `t` | Toggle tiling mode |
| `Ctrl+B` `t` `Esc` | Cancel |

### Debug Prefix (`Ctrl+B` `D`)

Access debug and development tools:

| Key Sequence | Action |
|--------------|--------|
| `Ctrl+B` `D` `l` | Toggle log viewer |
| `Ctrl+B` `D` `c` | Toggle cache statistics |
| `Ctrl+B` `D` `Esc` | Cancel |

**Log Viewer Keys:**
- `q`, `Esc` - Exit log viewer
- `j`, `k`, `↑`, `↓` - Scroll up/down one line
- `Ctrl+U`, `Ctrl+D`, `PgUp`, `PgDn` - Scroll half page
- `g`, `Home` - Go to top
- `G`, `End` - Go to bottom

**Cache Statistics Keys:**
- `q`, `Esc`, `c` - Exit cache stats viewer
- `r` - Reset cache statistics

## Mouse Controls

- **Left Click**: Focus window
- **Left Drag**: Move window (non-tiling) or swap windows (tiling)
- **Right Drag**: Resize window (non-tiling only)
- **Title Bar Buttons**: Minimize, maximize, or close window
- **Click Dock Item**: Restore minimized window
- **Copy Mode Click**: Move cursor to position
- **Copy Mode Drag**: Select text (enters visual mode)

## Customization

All keybindings can be customized in the configuration file. See the [Configuration Guide](CONFIGURATION.md) for details.

### Quick Customization

```bash
# Edit your keybindings
tuios --edit-config

# View current configuration
tuios --list-keybinds

# View only your customizations
tuios --list-custom-keybinds
```

## Platform-Specific Notes

### macOS

Default workspace switching uses Option key:
- `Option+1` through `Option+9` - Switch workspace
- `Option+Shift+1` through `Option+Shift+9` - Move window to workspace

In your terminal, you can still type Option key unicode characters (¡™£¢∞§¶•ª) in Terminal Mode.

### Linux

Uses standard Alt key for workspace switching:
- `Alt+1` through `Alt+9`
- `Alt+Shift+1` through `Alt+Shift+9`

## Related Documentation

- [Configuration Guide](CONFIGURATION.md) - Customize keybindings
- [CLI Reference](CLI_REFERENCE.md) - Command-line options
- [README](../README.md) - Project overview
