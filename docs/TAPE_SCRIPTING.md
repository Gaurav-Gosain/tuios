# Tape Scripting Language Reference

## Overview

TUIOS Tape is a domain-specific language (DSL) for automating terminal window management workflows. Tape scripts allow you to record, replay, and test complex TUIOS interactions programmatically.

## Table of Contents

1. [Basic Syntax](#basic-syntax)
2. [Commands Reference](#commands-reference)
3. [Mode Management](#mode-management)
4. [Window Operations](#window-operations)
5. [Workspace Management](#workspace-management)
6. [Keyboard Input](#keyboard-input)
7. [Timing and Synchronization](#timing-and-synchronization)
8. [Best Practices](#best-practices)
9. [Examples](#examples)
10. [Running Tape Scripts](#running-tape-scripts)
11. [Remote Tape Execution](#remote-tape-execution)

---

## Basic Syntax

### Comments

Lines starting with `#` are comments and are ignored by the parser:

```tape
# This is a comment
NewWindow  # Inline comments are not supported
```

### Command Structure

Commands are case-sensitive and follow this general format:

```tape
CommandName [arguments...]
```

### String Literals

Strings can be quoted with double quotes, single quotes, or backticks:

```tape
Type "hello world"
Type 'hello world'
Type `hello world`
```

Escape sequences are supported in double-quoted strings:

```tape
Type "Line 1\nLine 2\tTabbed"
```

### Duration Literals

Durations support standard Go duration formats:

```tape
Sleep 500ms     # Milliseconds
Sleep 2s        # Seconds
Sleep 1.5s      # Decimal seconds
Sleep 1m        # Minutes
Sleep 1h        # Hours
```

---

## Commands Reference

### Mode Management

TUIOS has two primary modes: **Window Management Mode** (for managing windows) and **Terminal Mode** (for interacting with terminal content).

#### `WindowManagementMode`

Switch to window management mode for window operations.

```tape
WindowManagementMode
```

#### `TerminalMode`

Switch to terminal mode to send input to the focused window.

```tape
TerminalMode
```

**Workflow Pattern:**
```tape
WindowManagementMode  # Navigate/manage windows
NewWindow
TerminalMode          # Type into the window
Type "ls -la"
Enter
WindowManagementMode  # Back to managing windows
```

---

### Window Operations

#### `NewWindow`

Create a new terminal window in the current workspace.

```tape
NewWindow
Sleep 500ms  # Wait for window to initialize
```

#### `CloseWindow`

Close the currently focused window.

```tape
CloseWindow
```

#### `NextWindow`

Focus the next window in the current workspace.

```tape
NextWindow
Sleep 200ms
```

#### `PrevWindow`

Focus the previous window in the current workspace.

```tape
PrevWindow
Sleep 200ms
```

#### `FocusWindow <id>`

Focus a specific window by ID (advanced usage).

```tape
FocusWindow "window-uuid-here"
```

#### `RenameWindow <name>`

Rename the currently focused window.

```tape
RenameWindow "My Terminal"
```

#### `MinimizeWindow`

Minimize the currently focused window to the dock.

```tape
MinimizeWindow
Sleep 300ms  # Wait for animation
```

#### `RestoreWindow`

Restore a minimized window.

```tape
RestoreWindow
Sleep 300ms
```

---

### Tiling and Layout

#### `ToggleTiling`

Toggle tiling mode on/off for the current workspace.

```tape
ToggleTiling
```

#### `EnableTiling`

Explicitly enable tiling mode.

```tape
EnableTiling
Sleep 300ms
```

#### `DisableTiling`

Explicitly disable tiling mode.

```tape
DisableTiling
Sleep 300ms
```

#### `SnapLeft`

Snap the focused window to the left half of the screen.

```tape
SnapLeft
Sleep 200ms
```

#### `SnapRight`

Snap the focused window to the right half of the screen.

```tape
SnapRight
Sleep 200ms
```

#### `SnapFullscreen`

Snap the focused window to fullscreen.

```tape
SnapFullscreen
Sleep 200ms
```

---

### Workspace Management

TUIOS supports 9 workspaces (numbered 1-9).

#### `SwitchWorkspace <number>`

Switch to a specific workspace (1-9).

```tape
SwitchWorkspace 2
Sleep 400ms
```

#### `MoveToWorkspace <number>`

Move the focused window to another workspace (without following it).

```tape
MoveToWorkspace 3
```

#### `MoveAndFollowWorkspace <number>`

Move the focused window to another workspace and switch to it.

```tape
MoveAndFollowWorkspace 2
Sleep 400ms
```

---

### Keyboard Input

All keyboard input commands require **Terminal Mode** to be active.

#### `Type <text>`

Type a string of text into the focused terminal.

```tape
TerminalMode
Type "echo 'Hello, World!'"
```

#### Basic Keys

```tape
Enter         # Press Enter
Space         # Press Space
Tab           # Press Tab
Backspace     # Press Backspace
Delete        # Press Delete
Escape        # Press Escape
```

#### Navigation Keys

```tape
Up            # Arrow up
Down          # Arrow down
Left          # Arrow left
Right         # Arrow right
Home          # Home key
End           # End key
```

#### Key Combinations

Use `+` to combine modifier keys with regular keys:

```tape
Ctrl+c        # Control + C
Ctrl+b        # TUIOS leader key
Alt+1         # Alt + 1
Shift+Tab     # Shift + Tab
Ctrl+Alt+t    # Multiple modifiers
```

**Modifiers:**
- `Ctrl` - Control key
- `Alt` - Alt/Option key
- `Shift` - Shift key

---

### Timing and Synchronization

#### `Sleep <duration>`

Pause script execution for a specified duration.

```tape
Sleep 500ms
Sleep 1s
Sleep 1.5s
```

#### `Wait <duration>` (Alias for Sleep)

Alternative syntax for waiting.

```tape
Wait 500ms
```

#### `WaitUntilRegex <pattern> [timeout]`

Wait until the terminal output matches a regex pattern (with optional timeout in milliseconds).

```tape
TerminalMode
Type "ls -la"
Enter
# Wait for command to complete (look for prompt)
WaitUntilRegex "\\$" 3000
```

**Timeout:**
- Default: 5000ms (5 seconds)
- Specify custom timeout as second argument

```tape
WaitUntilRegex "test" 10000  # 10 second timeout
```

---

## Best Practices

### 1. Always Add Sleeps After Actions

Give TUIOS time to process animations and state changes:

```tape
NewWindow
Sleep 500ms    # Wait for window creation

MinimizeWindow
Sleep 300ms    # Wait for minimize animation
```

### 2. Explicit Mode Switching

Always switch modes explicitly before performing mode-specific actions:

```tape
# GOOD
WindowManagementMode
NewWindow
TerminalMode
Type "ls"

# BAD (mode might be wrong)
NewWindow
Type "ls"
```

### 3. Use Comments Generously

Document complex workflows:

```tape
# Setup development environment
WindowManagementMode
EnableTiling

# Create editor window
NewWindow
TerminalMode
Type "vim main.go"
Enter
Sleep 800ms

# Create build window
WindowManagementMode
NewWindow
TerminalMode
Type "go build -watch"
Enter
```

### 4. Workspace Organization

Use workspaces to group related windows:

```tape
# Workspace 1: Development
SwitchWorkspace 1
NewWindow  # Editor
NewWindow  # Terminal

# Workspace 2: Monitoring
SwitchWorkspace 2
NewWindow  # Logs
NewWindow  # System stats

# Workspace 3: Documentation
SwitchWorkspace 3
NewWindow  # Browser/docs
```

### 5. Error Recovery

Use longer timeouts for potentially slow operations:

```tape
TerminalMode
Type "npm install"
Enter
WaitUntilRegex "completed" 60000  # 60 second timeout for npm
```

### 6. Consistent Timing

Use consistent sleep durations for similar actions:

```tape
# Window creation: 500ms
NewWindow
Sleep 500ms

# Window navigation: 200ms
NextWindow
Sleep 200ms

# Animations: 300ms
MinimizeWindow
Sleep 300ms
```

---

## Examples

### Example 1: Basic Workflow

```tape
# Create a simple dev environment
WindowManagementMode
EnableTiling
Sleep 300ms

# Editor window
NewWindow
Sleep 500ms
TerminalMode
Type "vim README.md"
Enter
Sleep 800ms

# Terminal window
WindowManagementMode
NewWindow
Sleep 500ms
TerminalMode
Type "ls -la"
Enter
```

### Example 2: Multi-Workspace Setup

```tape
# Setup project workspaces
WindowManagementMode

# Workspace 1: Coding
SwitchWorkspace 1
EnableTiling
NewWindow
TerminalMode
Type "vim main.go"
Enter
Sleep 500ms

WindowManagementMode
NewWindow
TerminalMode
Type "go run ."
Enter

# Workspace 2: Testing
WindowManagementMode
SwitchWorkspace 2
NewWindow
TerminalMode
Type "go test -v ./..."
Enter

# Workspace 3: Git
WindowManagementMode
SwitchWorkspace 3
NewWindow
TerminalMode
Type "git status"
Enter

# Return to main workspace
WindowManagementMode
SwitchWorkspace 1
```

### Example 3: Conditional Waiting

```tape
WindowManagementMode
NewWindow
Sleep 500ms

TerminalMode
# Start a long-running build
Type "make build"
Enter

# Wait for build completion
WaitUntilRegex "Build complete" 120000

# Run the result
Type "./output"
Enter
```

### Example 4: Window Layout

```tape
WindowManagementMode
DisableTiling  # Use manual positioning

# Left half
NewWindow
Sleep 300ms
SnapLeft
Sleep 200ms

# Right half top
NewWindow
Sleep 300ms
SnapRight
Sleep 200ms

# Fullscreen overlay
NewWindow
Sleep 300ms
SnapFullscreen
Sleep 200ms

# Minimize to see others
MinimizeWindow
Sleep 300ms
```

---

## Advanced Features

### Future Enhancements (Roadmap)

The following features are planned for future releases:

#### Variables

```tape
Set windowName "My Terminal"
RenameWindow $windowName
```

#### Loops

```tape
Repeat 5 {
  NewWindow
  Sleep 200ms
}
```

#### Conditionals

```tape
If WindowCount > 5 {
  MinimizeWindow
}
```

#### Functions

```tape
Define CreateDevWindow {
  NewWindow
  TerminalMode
  Type "vim"
  Enter
}

Call CreateDevWindow
```

#### Output Capture

```tape
Output "screenshot.png"
Type "neofetch"
Enter
Sleep 2s
```

---

## Troubleshooting

### Script Not Working?

1. **Check mode:** Ensure you're in the correct mode (Window Management vs Terminal)
2. **Add delays:** Increase `Sleep` durations if actions are happening too fast
3. **Validate syntax:** Run `tuios tape validate script.tape` to check for errors
4. **Check regex:** Test regex patterns separately before using in `WaitUntilRegex`

### Common Errors

**"Unknown command"**: Check spelling and capitalization (commands are case-sensitive)

**"Unexpected token"**: Check for missing quotes around strings

**"Timeout waiting for pattern"**: Increase timeout or verify regex pattern matches expected output

---

## Running Tape Scripts

### Headless Execution

Run without showing the TUI (for CI/CD):

```bash
tuios tape run script.tape
```

### Interactive Playback

Watch the script execute in real-time:

```bash
tuios tape play script.tape
```

### Validation Only

Check syntax without running:

```bash
tuios tape validate script.tape
```

### Recording

Create scripts from live interactions (coming soon):

```bash
tuios tape record output.tape
```

---

## Remote Tape Execution

The `tuios tape exec` command allows you to run tape scripts against an already running TUIOS session. This enables automation workflows where TUIOS is running in daemon mode or in another terminal.

### Basic Usage

```bash
# Execute a tape script against the current session
tuios tape exec script.tape

# Execute against a specific session
tuios tape exec --session mysession script.tape
tuios tape exec -s mysession script.tape
```

### Progress Display

Remote execution shows a progress indicator during script execution:

```
Executing script.tape...
Progress: 5/12 [███████░░░░░░░░░░░░░] 41%
```

### Use Cases

**Automation pipelines:**
```bash
# Start a session in the background
tuios new automation &
sleep 2

# Execute setup script
tuios tape exec -s automation setup.tape

# Run tests
tuios tape exec -s automation test-workflow.tape

# Cleanup
tuios kill-session automation
```

**Development workflows:**
```bash
# In terminal 1: Start TUIOS
tuios new dev

# In terminal 2: Send scripts to configure environment
tuios tape exec -s dev environment-setup.tape
```

**Integration testing:**
```bash
#!/bin/bash
# Run multiple scripts and check results

tuios tape exec test-suite-1.tape
tuios tape exec test-suite-2.tape

# Query state after tests
WINDOWS=$(tuios list-windows --json | jq '.total')
echo "Test created $WINDOWS windows"
```

### Combining with CLI Commands

Remote tape execution works seamlessly with other CLI commands:

```bash
# Execute script then query state
tuios tape exec setup.tape
tuios session-info --json | jq '.total_windows'

# Execute script and capture window ID
tuios tape exec create-window.tape
WINDOW_ID=$(tuios get-window --json | jq -r '.id')
echo "Created window: $WINDOW_ID"
```

### Differences from `tape play`

| Feature | `tape play` | `tape exec` |
|---------|-------------|-------------|
| Starts TUIOS | Yes | No (requires running session) |
| Shows TUI | Yes | No (progress bar only) |
| Interactive | Yes | No |
| For automation | No | Yes |
| Session persistence | No | Yes (works with daemon) |

---

## File Format

Tape files use the `.tape` extension and are plain text files with UTF-8 encoding.

**Recommended structure:**

```tape
# Header comment describing the script
# Author: Your Name
# Purpose: What this script does
# Version: 1.0

# Main script body
WindowManagementMode
# ... commands ...

# Footer
Sleep 1s
# End of script
```

---

## See Also

- [TUIOS Keybindings Documentation](KEYBINDINGS.md)
- [TUIOS Configuration Guide](CONFIGURATION.md)
- [Example Tape Scripts](../examples/)
- [AGENTS.md](../AGENTS.md) - Development guide

---

**Last Updated:** 2025-11-27  
**TUIOS Version:** 0.4.0+
