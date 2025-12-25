# Tape Recording

TUIOS can record your terminal sessions into `.tape` files that can be replayed, shared, or used for automated testing. Think of it as recording a macro of everything you do in TUIOS.

> **Note:** Throughout this document, `Ctrl+B` refers to the default leader key. This is configurable via the `leader_key` option in your config file.

## Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [Recording Commands](#recording-commands)
- [What Gets Recorded](#what-gets-recorded)
- [Recording Workflow](#recording-workflow)
- [Managing Recordings](#managing-recordings)
- [Best Practices](#best-practices)
- [Technical Details](#technical-details)
- [Integration with Playback](#integration-with-playback)

## Overview

Tape recording captures your interactions with TUIOS in real-time, generating a `.tape` script that can be played back later. This is useful for:

- Creating reproducible workflows
- Documenting complex terminal procedures
- Building automated test suites
- Generating tutorial materials
- Sharing terminal workflows with team members

Recordings capture actions, not raw output. The tape file contains commands like "create window", "type text", "switch workspace" rather than raw terminal bytes. This makes recordings portable and editable.

## Quick Start

Start recording within TUIOS using the tape prefix menu:

```
Ctrl+B T r    # Start recording
Ctrl+B T s    # Stop recording
```

When you start recording, TUIOS will prompt for a recording name. After stopping, the tape file is saved to `~/.local/share/tuios/tapes/`.

Playback your recording:

```bash
tuios tape play ~/.local/share/tuios/tapes/my-recording.tape
```

## Recording Commands

All recording commands use the tape prefix menu accessed with `Ctrl+B T`:

| Key Sequence | Action |
|--------------|--------|
| `Ctrl+B` `T` `r` | Start recording (prompts for name) |
| `Ctrl+B` `T` `s` | Stop recording and save |
| `Ctrl+B` `T` `Esc` | Cancel (exit tape prefix menu) |

Recording status appears in the status bar while active.

## What Gets Recorded

### Captured Actions

- **Window Management**: New window, close window, rename window, minimize/restore
- **Navigation**: Next/previous window, select window by number
- **Workspaces**: Switch workspace, move window to workspace
- **Layout**: Toggle tiling, snap operations, split operations
- **Mode Switches**: Enter terminal mode, enter window management mode
- **Keyboard Input**: Text typed in terminals, special keys (Enter, Tab, etc.)
- **Timing**: Automatic sleep insertion between actions

### Not Captured

- **Mouse Events**: Clicks, drags, and resizes are not recorded
- **Terminal Output**: Only input is recorded, not the programs' output
- **Visual State**: Window positions/sizes in non-tiling mode
- **Copy Mode Actions**: Scrollback navigation and text selection

This is intentional. Recordings focus on reproducible actions that work across different terminal sizes and states.

## Recording Workflow

### Starting a Recording

1. Launch TUIOS normally
2. Press `Ctrl+B T r`
3. Enter a name when prompted (e.g., "dev-setup")
4. A notification confirms recording started
5. The status bar shows "REC" indicator

### During Recording

Perform your workflow normally. The recorder tracks:

- Every window operation you perform
- All text you type in terminals
- Mode switches between window management and terminal mode
- Workspace navigation

Time delays between actions are automatically calculated and inserted as `Sleep` commands.

### Stopping a Recording

1. Press `Ctrl+B T s`
2. Recording stops immediately
3. Tape file written to `~/.local/share/tuios/tapes/NAME.tape`
4. Notification shows file path

### Initial State Capture

When recording starts, TUIOS captures the current state:

- Current mode (WindowManagementMode or TerminalMode)
- Active workspace number
- Tiling enabled/disabled

The tape file begins with commands that restore this state during playback.

## Managing Recordings

### List All Recordings

```bash
tuios tape list
```

Shows all recordings in your tapes directory with file sizes and modification times.

### Show Tape Directory

```bash
tuios tape dir
```

Prints the path to the tapes directory (`~/.local/share/tuios/tapes/`).

### View Tape Contents

```bash
tuios tape show my-recording
```

Displays the tape file contents. The `.tape` extension is optional.

### Delete a Recording

```bash
tuios tape delete my-recording
```

Permanently removes a tape file.

### Rename/Move Recordings

Tape files are just text files. Rename or move them with standard Unix commands:

```bash
cd ~/.local/share/tuios/tapes
mv old-name.tape new-name.tape
```

## Best Practices

### Recording Strategy

**Keep recordings focused**: Record one task per tape rather than entire sessions. Shorter recordings are easier to replay and maintain.

**Add pauses for visual clarity**: When recording demos, pause briefly between major actions (e.g., after creating a window) to give viewers time to see what happened.

**Start in a clean state**: Begin recordings with no windows open or in a known state (e.g., one window in workspace 1).

**Avoid mistakes**: If you make a mistake while recording, stop the recording and start over. Editing tape files is possible but tedious.

### Naming Conventions

Use descriptive names that indicate the recording's purpose:

```
dev-setup.tape            # Good: describes what it does
project-build.tape        # Good: clear purpose
recording-1.tape          # Bad: meaningless
test.tape                 # Bad: too generic
```

### Recording for Playback

If you plan to replay recordings (not just archive them), keep these in mind:

**Use tiling mode**: Tiled layouts are reproducible across different terminal sizes. Manual window positioning is not recorded.

**Avoid time-sensitive operations**: Don't rely on specific command execution times. Use `WaitUntilRegex` in manually edited tapes if you need to wait for output.

**Test your recordings**: Always play back a recording once to verify it works as expected.

### Recording for Documentation

When creating tutorial or documentation recordings:

**Explain as you go**: Add comments to the tape file after recording to document what each section does.

**Use meaningful window titles**: Rename windows to descriptive titles (e.g., "Editor", "Build Output") during recording.

**Show the help overlay**: Press `Ctrl+B ?` during recording to show available keybindings.

## Technical Details

### File Format

Recordings are saved as standard `.tape` files using the TUIOS tape scripting language. See [TAPE_SCRIPTING.md](TAPE_SCRIPTING.md) for the complete language reference.

### Storage Location

Default: `~/.local/share/tuios/tapes/`

This follows the XDG Base Directory specification. The directory is created automatically on first recording.

### Timing Capture

The recorder tracks time between actions and inserts `Sleep` commands automatically. Timing granularity:

- Minimum sleep: 50ms
- Action grouping: Multiple rapid keystrokes are grouped into single `Type` commands
- Mode switches: Recorded immediately with precise timing

### Typing Buffer Optimization

Rapid typing is automatically merged into single `Type` commands to keep recordings compact. A flush occurs when:

- 500ms passes with no typing
- Mode switches occur
- Window management actions occur
- Recording stops

This means if you type "hello world" quickly, it appears as one `Type "hello world"` command rather than 11 separate key presses.

### Metadata

Each recording stores metadata:

```tape
# Recorded: 2024-12-18 15:30:45
# TUIOS Recording
# Name: dev-setup
```

This appears as comments at the top of the tape file.

## Integration with Playback

Recordings are designed for seamless playback:

```bash
# Interactive playback (watch it happen)
tuios tape play my-recording.tape

# Headless execution (background, no TUI)
tuios tape run my-recording.tape

# Validate syntax without executing
tuios tape validate my-recording.tape
```

For detailed playback options, see [TAPE_SCRIPTING.md](TAPE_SCRIPTING.md).

### Editing Recordings

Tape files are plain text. You can edit them after recording to:

- Add comments explaining steps
- Insert additional `Sleep` commands for pacing
- Add `WaitUntilRegex` for output synchronization
- Remove mistakes or unwanted actions
- Combine multiple recordings

Example of editing a recording:

```tape
# Original recorded content
WindowManagementMode
NewWindow
Sleep 500ms
TerminalMode
Type "echo hello"
Enter

# After editing - added comments and wait
WindowManagementMode
NewWindow
Sleep 500ms

# Start server
TerminalMode
Type "npm start"
Enter
WaitUntilRegex "Server listening" 10000

# Open another window for testing
WindowManagementMode
NewWindow
Sleep 300ms
```

### Playback Compatibility

Recordings made in one TUIOS version generally work in newer versions, but:

- New features won't appear in old recordings
- Removed features may cause playback errors
- Keybinding changes don't affect recordings (recordings store actions, not keys)

### Performance Considerations

Recording overhead is minimal:

- Memory: ~100 bytes per recorded action
- CPU: Negligible (< 0.1% on modern systems)
- Disk: Typical recording is 1-10KB depending on length

Recording does not impact terminal performance or increase latency.

## Troubleshooting

### Recording Not Starting

**Check the tape directory exists:**
```bash
ls -la ~/.local/share/tuios/tapes/
```

If it doesn't exist, TUIOS should create it automatically. If creation fails, check filesystem permissions.

**Check for existing recording:**
Only one recording can be active at a time. Stop any existing recording with `Ctrl+B T s` before starting a new one.

### Recording Playback Issues

**Missing timing information:**
If a recording plays back too fast, add `Sleep` commands manually:

```tape
NewWindow
Sleep 500ms  # Add pauses for visual clarity
```

**Actions not working:**
Validate the tape file for syntax errors:

```bash
tuios tape validate my-recording.tape
```

**Window state mismatches:**
Recordings assume a clean starting state. Start TUIOS fresh before playback, or edit the tape to match your current state.

### File Not Found Errors

Recordings are saved with the `.tape` extension automatically. When using tape commands, the extension is optional:

```bash
tuios tape show dev-setup       # Works
tuios tape show dev-setup.tape  # Also works
```

## Related Documentation

- [Tape Scripting Language](TAPE_SCRIPTING.md) - Complete DSL reference for editing tapes
- [CLI Reference](CLI_REFERENCE.md) - All tape-related CLI commands
- [Keybindings](KEYBINDINGS.md) - Recording keybindings reference

## Examples

### Example 1: Recording a Development Environment Setup

```
# Start TUIOS
tuios

# Start recording
Ctrl+B T r
> Enter name: "dev-env-setup"

# Create windows and set up layout
n                           # New window
i                           # Enter terminal mode
vim main.go                 # Type command
Enter
Ctrl+B d                    # Back to window management mode
n                           # Another new window
i
go run .
Enter
Ctrl+B d
t                           # Enable tiling

# Stop recording
Ctrl+B T s
```

Result: `~/.local/share/tuios/tapes/dev-env-setup.tape` containing the workflow.

### Example 2: Recording Multi-Workspace Workflow

```
# Start recording
Ctrl+B T r
> "multi-workspace"

# Set up workspace 1: development
n
i
Type "cd ~/project && vim"
Enter
Ctrl+B d

# Set up workspace 2: testing  
Alt+2                       # Switch to workspace 2
n
i
Type "npm test -- --watch"
Enter
Ctrl+B d

# Set up workspace 3: documentation
Alt+3
n
i
Type "mdbook serve docs"
Enter

# Stop recording
Ctrl+B T s
```

This creates a multi-workspace development environment that can be recreated instantly.

### Example 3: Recording for CI/CD

Record a test execution workflow:

```
# Start recording
Ctrl+B T r
> "ci-test-suite"

# Run test suite
n
i
Type "npm test"
Enter
# Wait for completion manually...
Ctrl+C
Ctrl+B d

# Stop and edit
Ctrl+B T s

# Edit the tape file to add proper waiting:
vim ~/.local/share/tuios/tapes/ci-test-suite.tape
# Add: WaitUntilRegex "Tests passed" 30000
```

Run in CI:

```bash
#!/bin/bash
tuios tape run ci-test-suite.tape
```

This allows you to test TUIOS-based workflows in continuous integration.
