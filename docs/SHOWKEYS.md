# Showkeys Overlay

The showkeys overlay displays your keyboard input on screen in real-time, making TUIOS ideal for presentations, screencasts, and live coding demos. When enabled, every key you press appears in a clean, unobtrusive overlay at the bottom of the screen.

> **Note:** Throughout this document, `Ctrl+B` refers to the default leader key. This is configurable via the `leader_key` option in your config file.

## Table of Contents

- [Overview](#overview)
- [Enabling Showkeys](#enabling-showkeys)
- [What Gets Displayed](#what-gets-displayed)
- [Display Format](#display-format)
- [Use Cases](#use-cases)
- [Configuration](#configuration)
- [Technical Details](#technical-details)
- [Troubleshooting](#troubleshooting)

## Overview

Showkeys solves a common problem in terminal screencasts: viewers can't see what keys you're pressing. Without visual feedback, audiences miss important keyboard shortcuts and get confused about how you're navigating.

The showkeys overlay captures and displays:

- Individual keystrokes
- Modifier combinations (Ctrl, Shift, Alt, Cmd)
- Action names (when a key triggers a TUIOS action)
- Key repeat counts
- Timing information

## Enabling Showkeys

### At Startup

Enable showkeys when launching TUIOS:

```bash
tuios --show-keys
```

For session-based workflows:

```bash
tuios new demo --show-keys
tuios attach demo
```

For SSH server mode:

```bash
tuios ssh --show-keys --host 0.0.0.0 --port 2222
```

### Toggle During Runtime

While TUIOS is running, toggle showkeys:

```
Ctrl+B D k               # Via debug prefix menu
```

Showkeys state persists for the current session but resets on restart unless you use `--show-keys` flag.

### In Web Terminal

Pass the flag to tuios-web:

```bash
tuios-web --show-keys --port 7681
```

Showkeys works identically in browser-based terminals.

## What Gets Displayed

### Basic Keys

Single keystrokes appear with their character:

```
a    j    x    1    Space    Enter    Esc
```

### Modifier Combinations

Modifier keys combine with the base key:

```
Ctrl+C        Shift+Tab        Alt+1        Ctrl+Shift+T
```

### Action Names

When a key triggers a TUIOS action, the action name appears:

```
n → new_window
t → toggle_tiling
Alt+2 → switch_workspace_2
Ctrl+B c → prefix_new_window
```

This helps viewers understand not just what key was pressed, but what it did.

### Key Repeats

Holding a key shows a repeat count:

```
j ×3          # Pressed 'j' three times rapidly
Down ×5       # Held down arrow
```

### Special Keys

Common special keys display with readable names:

```
Tab    Enter    Esc    Backspace    Delete
Up     Down     Left   Right        Home    End
F1     F2       ...    F12
PgUp   PgDn     Insert
```

## Display Format

### Overlay Position

The showkeys overlay appears at the bottom-center of the screen, above the status bar but below any windows.

### Ring Buffer

TUIOS shows the last 5 keystrokes by default. Older keys slide out as new ones arrive:

```
[Ctrl+B] [c] [i] [l] [s]
```

The history limit prevents clutter while providing enough context.

### Visual Styling

Keys appear in rounded boxes with theming that matches your current TUIOS theme:

```
┌────────┐ ┌───┐ ┌───────────────┐
│ Ctrl+B │ │ c │ │ new_window    │
└────────┘ └───┘ └───────────────┘
```

### Transparency

The overlay uses semi-transparent backgrounds to avoid completely obscuring terminal content underneath.

## Use Cases

### Screencasting and Tutorials

Perfect for creating terminal tutorials where viewers need to see exactly what you're typing:

```bash
# Record screencast with showkeys
tuios --show-keys
# Start recording with your screen capture tool
# Perform your demo
```

Viewers see both the terminal output and your keystrokes, making it easy to follow along.

### Live Presentations

During conference talks or live coding sessions, showkeys lets audience members see your shortcuts:

```bash
# Before presentation
tuios new presentation --show-keys --theme nord
```

Attendees can take notes on the specific keys you use without constantly asking "what did you just press?"

### Teaching and Onboarding

When teaching new developers how to use TUIOS or terminal workflows:

```bash
tuios --show-keys --theme github
```

Students see exactly how to navigate, create windows, and execute commands.

### Recording Demos for Documentation

Capture animated demos for README files or documentation:

```bash
# Record with showkeys
tuios --show-keys
asciinema rec demo.cast
# Perform demo
exit
```

The resulting recording includes visible keystrokes, making it self-documenting.

### Debugging Keybinding Issues

When troubleshooting keybinding conflicts or testing custom configurations:

```bash
tuios --show-keys --debug
```

Watch the overlay to confirm TUIOS receives the correct key events and resolves them to the expected actions.

## Configuration

### History Size

The default ring buffer shows 5 keys. This is currently hardcoded in the OS struct (`KeyHistoryMaxSize`). To change it, you would need to modify `internal/app/os.go`.

Future versions may expose this as a configuration option.

### Disable Specific Keys

There's no filter to hide specific keys (e.g., passwords). If you're typing sensitive information, toggle showkeys off temporarily:

```
Ctrl+B D k               # Toggle off
# Type sensitive data
Ctrl+B D k               # Toggle back on
```

### Theme Integration

Showkeys automatically adapts to your current theme. The overlay colors derive from your terminal's ANSI palette:

```bash
tuios --theme dracula --show-keys      # Purple/pink theme
tuios --theme nord --show-keys         # Blue/arctic theme
```

No additional configuration needed.

## Technical Details

### Key Capture

Showkeys hooks into the input handling pipeline at `internal/input/keyboard.go`. Every key event passes through the showkeys recorder before normal processing.

### Data Structure

Recent keys are stored in a ring buffer:

```go
type KeyEvent struct {
    Key       string       // "a", "Ctrl+C", "Enter"
    Modifiers []string     // ["Ctrl", "Shift"]
    Timestamp time.Time    // When pressed
    Count     int          // Repeat count
    Action    string       // "new_window", "toggle_tiling"
}
```

The OS struct maintains:

```go
ShowKeys          bool         // Enabled/disabled
RecentKeys        []KeyEvent   // Ring buffer
KeyHistoryMaxSize int          // Buffer size (default: 5)
```

### Rendering

The showkeys overlay renders in `internal/app/render_overlays.go` as a separate layer composited on top of the main view. It's drawn after windows but before notifications.

### Performance Impact

Showkeys has minimal overhead:

- Memory: ~1KB for key history
- CPU: < 0.1% additional processing per keystroke
- Rendering: No performance impact (overlay is lightweight)

### Recording Integration

When tape recording is active, showkeys helps verify that the recorder captures the correct keys. However, showkeys itself doesn't appear in tape playback (since tapes don't include visual state).

## Troubleshooting

### Overlay Not Appearing

**Check if enabled:**

```bash
# Status bar should show indicator when active
```

**Toggle it:**

```
Ctrl+B D k
```

**Verify at startup:**

```bash
tuios --show-keys
```

### Keys Not Showing

**Modifier key issues:**

Some terminals don't send all modifier combinations. For example, `Ctrl+Shift+letter` might arrive as just `Ctrl+letter`.

**Solution:** Use a terminal with proper keyboard protocol support (kitty, wezterm, alacritty).

**Key captured but no action name:**

Not all keys map to TUIOS actions. Only keybindings configured in your config file show action names.

### Overlay Position Issues

**Terminal size too small:**

Showkeys needs at least 3 rows of vertical space. On very small terminals (< 20 rows), the overlay might be cut off.

**Solution:** Resize your terminal to at least 24 rows.

**Overlapping with content:**

The overlay intentionally uses transparency. If it's hard to read over busy terminal output:

**Solution:** Toggle it off temporarily, or use a theme with higher contrast.

### Performance Issues

If you experience slowdown with showkeys enabled:

**Check debug logging:**

```bash
tuios --show-keys --debug
```

Excessive logging can slow things down. Disable debug mode if not needed.

**Reduce active windows:**

With 10+ windows, rendering overhead increases. Showkeys is rarely the issue, but combined overhead can accumulate.

## Related Documentation

- [Keybindings Reference](KEYBINDINGS.md) - See what actions your keys trigger
- [Configuration Guide](CONFIGURATION.md) - Customize keybindings to change action names
- [Tape Recording](TAPE_RECORDING.md) - Record demos with visible keystrokes
- [CLI Reference](CLI_REFERENCE.md) - Command-line flag reference

## Examples

### Example 1: Recording a Tutorial

```bash
# Start TUIOS with showkeys and a clean theme
tuios --show-keys --theme github

# Start recording with asciinema
asciinema rec tuios-demo.cast

# Perform demo:
# - Create windows with 'n'
# - Enable tiling with 't'
# - Navigate with Tab
# - Switch workspaces with Alt+1, Alt+2

# Stop recording
exit
```

Result: A recording where viewers see every keystroke and its corresponding action.

### Example 2: Live Presentation Setup

```bash
# Before audience arrives
tuios new workshop --show-keys --theme nord

# Maximize terminal window
# Adjust font size for visibility

# Detach and prepare
Ctrl+B d

# When ready to present
tuios attach workshop
```

Showkeys state persists in the session.

### Example 3: Teaching Workflow

```bash
# Instructor terminal
tuios --show-keys --theme dracula

# Share screen and demonstrate:
n                    # Students see "n → new_window"
i                    # Students see "i → enter_terminal_mode"
Type commands        # Students see keystrokes
Ctrl+B d             # Students see "Ctrl+B d → enter_window_mode"
t                    # Students see "t → toggle_tiling"
```

Students can follow along precisely.

### Example 4: Debugging Custom Keybindings

```bash
# Test custom keybindings with showkeys
tuios --show-keys

# Edit config to add custom binding
Ctrl+B c e           # (hypothetical custom binding)

# Press the custom key
# Watch showkeys to confirm:
# - Key is received correctly
# - Action name appears (or doesn't if misconfigured)
```

Helps diagnose keybinding conflicts or typos in config.

## Future Enhancements

Potential future additions:

- Configurable history size (currently hardcoded at 5)
- Key filtering (hide passwords, etc.)
- Custom overlay position (top/bottom/left/right)
- Export keystroke log to file
- Integration with screen recording tools

Check the [GitHub issues](https://github.com/Gaurav-Gosain/tuios/issues) for feature requests and discussions.
