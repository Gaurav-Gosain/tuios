# CLI Reference

This document provides a complete reference for TUIOS command-line interface.

## Table of Contents

- [Overview](#overview)
- [Installation](#installation)
- [Usage](#usage)
- [Commands](#commands)
  - [Root Command](#root-command)
  - [Theming](#theming)
  - [Agent Skill](#agent-skill)
  - [Daemon Mode (Session Persistence)](#daemon-mode-session-persistence)
  - [Remote Control Commands](#remote-control-commands)
  - [Inspection Commands](#inspection-commands)
  - [Scripting Examples](#scripting-examples)
  - [tuios ssh](#tuios-ssh)
  - [tuios-web (separate binary)](#tuios-web-separate-binary)
  - [tuios config](#tuios-config)
  - [tuios keybinds](#tuios-keybinds)
  - [tuios layout](#tuios-layout)
  - [tuios completion](#tuios-completion)
  - [tuios help](#tuios-help)
- [Global Flags](#global-flags)
- [Common Usage Examples](#common-usage-examples)
- [Environment Variables](#environment-variables)
- [When Something Goes Wrong](#when-something-goes-wrong)
- [Exit Codes](#exit-codes)
- [Version Information](#version-information)
- [Command Migration Guide](#command-migration-guide)
- [Related Documentation](#related-documentation)

## Overview

TUIOS uses a modern command-line interface built with Cobra and Fang, providing:
- Subcommand structure for better organization
- Styled help output and error messages
- Shell completion generation
- Man page generation support

## Installation

### Homebrew (macOS/Linux)

```bash
brew tap Gaurav-Gosain/tap
brew install tuios
```

### Arch Linux (AUR)

```bash
# Using yay
yay -S tuios-bin

# Using paru
paru -S tuios-bin
```

### Nix

```bash
# Run directly
nix run github:Gaurav-Gosain/tuios#tuios

# Or add to your configuration
nix-shell -p tuios
```

### Quick Install Script (Linux/macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/Gaurav-Gosain/tuios/main/install.sh | bash
```

### Go Install

```bash
go install github.com/Gaurav-Gosain/tuios/cmd/tuios@latest
```

### Pre-built Binaries

Download from [GitHub Releases](https://github.com/Gaurav-Gosain/tuios/releases)

---

## Usage

```bash
tuios [command] [flags]
```

## Commands

### Root Command

Run TUIOS in local mode (default behavior):

```bash
tuios
```

**Flags:**
- `--theme <name>` - Set color theme (default: "tokyonight")
- `--list-themes` - List all available themes and exit
- `--preview-theme <name>` - Preview a theme's 16 ANSI colors and exit
- `--skill` - Print the embedded agent skill and exit
- `--ascii-only` - Use ASCII characters instead of Nerd Font icons
- `--show-keys` - Enable showkeys overlay (screencaster-style key display)
- `--border-style <style>` - Window border style (rounded, normal, thick, double, hidden, block, ascii)
- `--dockbar-position <pos>` - Dockbar position (bottom, top, hidden)
- `--hide-window-buttons` - Hide window control buttons (minimize, maximize, close)
- `--window-button-style <style>` - How the window controls are drawn: `pill` (default) or `dots` (macOS traffic lights)
- `--window-button-position <position>` - Which end of the title bar the window controls sit on: `right` (default) or `left` (macOS)
- `--scrollback-lines <num>` - Number of lines in scrollback buffer (100-1000000)
- `--window-title-position <pos>` - Window title position (bottom, top, hidden)
- `--hide-clock` - Hide the clock overlay
- `--no-animations` - Disable UI animations for instant transitions
- `--show-clock` - Show clock in the status area
- `--show-cpu` - Show CPU usage in the status area
- `--show-ram` - Show RAM usage in the status area
- `--shared-borders` - Enable shared borders between tiled windows
- `--debug` - Enable debug logging
- `--cpuprofile <file>` - Write CPU profile to file
- `-h, --help` - Show help for tuios
- `-v, --version` - Show version information

**Examples:**
```bash
tuios                          # Start TUIOS normally (tokyonight theme)
tuios --theme dracula          # Start with Dracula theme
tuios --ascii-only             # Start without Nerd Font icons
tuios --show-keys              # Start with showkeys overlay enabled
tuios --list-themes            # List all available themes
tuios --preview-theme nord     # Preview Nord theme colors
tuios --skill                  # Print the agent skill and exit
tuios --debug                  # Start with debug logging
tuios --cpuprofile cpu.prof    # Start with CPU profiling

# Combine multiple flags
tuios --theme nord --show-keys # Use Nord theme with showkeys enabled

# Interactive theme selection with fzf
tuios --theme $(tuios --list-themes | fzf --preview 'tuios --preview-theme {}')
```

---

## Theming

TUIOS includes 300+ built-in color themes from various sources including Gogh, iTerm2, and custom themes.

### Available Themes

List all available themes:
```bash
tuios --list-themes
```

**Popular themes include:**
- `tokyonight` (default) - A clean, dark theme with vibrant colors
- `dracula` - Dark theme with purple accent
- `nord` - An arctic, north-bluish color palette
- `gruvbox_dark` - Retro groove color scheme
- `catppuccin_mocha` - Soothing pastel theme
- `monokai_pro` - Professional dark theme
- `solarized_dark` - Precision colors for machines and people
- `github` - GitHub's light theme
- `one_dark` - Atom's iconic dark theme

### Preview Themes

Preview a theme's 16 ANSI colors before using it:
```bash
tuios --preview-theme dracula
```

The preview shows all 16 colors (8 standard + 8 bright variants) with their color codes.

### Using Themes

Set a theme at startup:
```bash
tuios --theme nord
```

The theme affects:
- Terminal text colors (ANSI 0-15)
- Window borders
- UI elements (status bar, dock, overlays)
- Default foreground/background colors

**Note:** The theme only affects the 16 base ANSI colors. Applications using 256-color or true color (RGB) will display those colors unchanged.

### Interactive Theme Selection

Use `fzf` for interactive theme selection with live preview:
```bash
tuios --theme $(tuios --list-themes | fzf --preview 'tuios --preview-theme {}')
```

This allows you to browse all themes with a live color preview before selecting one.

### Theme Persistence

Themes are set via command-line flag and not currently stored in configuration. To always use a specific theme:

**Shell alias:**
```bash
# Add to ~/.bashrc, ~/.zshrc, etc.
alias tuios='tuios --theme nord'
```

**Script wrapper:**
```bash
#!/bin/bash
exec tuios --theme dracula "$@"
```

---

## Agent Skill

`tuios --skill` prints the agent skill embedded in the binary and exits. The
skill teaches an agent to drive TUIOS from inside a pane: how to tell it is in
one, how to address sessions and windows, how to read and write other panes,
how to wait on a condition, and how to report its own state.

```bash
tuios --skill
```

The text ships inside the binary as `skills/tuios/SKILL.md`, so it always
describes the TUIOS that printed it. Nothing is fetched and no daemon is
needed.

**Examples:**
```bash
# Read it
tuios --skill

# Install it where an agent harness looks for skills
mkdir -p ~/.claude/skills/tuios
tuios --skill > ~/.claude/skills/tuios/SKILL.md
```

---

## Daemon Mode (Session Persistence)

TUIOS supports persistent sessions through a daemon process, similar to tmux or screen. Sessions continue running in the background even when you disconnect, allowing you to reattach later with all windows and content preserved.

### `tuios new`

Create a new persistent session.

**Usage:**
```bash
tuios new [session-name] [flags]
```

**Flags:**
- `--theme <name>` - Set color theme for the session
- `--ascii-only` - Use ASCII characters instead of Nerd Font icons
- `--show-keys` - Enable showkeys overlay
- `--no-animations` - Disable UI animations

**Examples:**
```bash
tuios new                      # Create session with auto-generated name
tuios new mysession            # Create session named "mysession"
tuios new work --theme dracula # Create session with Dracula theme
```

### `tuios attach`

Attach to an existing session.

**Usage:**
```bash
tuios attach [session-name] [flags]
```

**Flags:**
- `-c, --create` - Create session if it doesn't exist
- Same as `tuios new` (theme, ascii-only, etc.)

**Examples:**
```bash
tuios attach                   # Attach to most recent session (or only session)
tuios attach mysession         # Attach to session named "mysession"
tuios attach mysession -c      # Attach or create if doesn't exist
tuios attach mysession --theme nord  # Attach with different theme
```

### `tuios ls`

List all TUIOS sessions.

**Usage:**
```bash
tuios ls
```

**Output:**
Shows a table with:
- Session name
- Number of windows
- Status (attached/detached)
- Creation time
- Last activity time

**Example output:**
```
╭───────────────┬─────────┬──────────┬───────────────┬─────────────────╮
│ NAME          │ WINDOWS │ STATUS   │ CREATED       │ LAST ACTIVE     │
├───────────────┼─────────┼──────────┼───────────────┼─────────────────┤
│ work          │ 3       │ detached │ 2 hours ago   │ 5 mins ago      │
│ dev           │ 2       │ attached │ 1 day ago     │ just now        │
╰───────────────┴─────────┴──────────┴───────────────┴─────────────────╯
```

### `tuios kill-session`

Kill a specific session.

**Usage:**
```bash
tuios kill-session <session-name>
```

**Examples:**
```bash
tuios kill-session mysession   # Kill session named "mysession"
```

### `tuios kill-server`

Stop the TUIOS daemon process. This stops all sessions.

**Usage:**
```bash
tuios kill-server
```

**Contract:** the command is synchronous. It returns only once the daemon has
written every session's resurrection state and removed its socket, so a script
may start a new daemon as soon as it returns:

```bash
tuios kill-server && tuios start-server   # safe: no race
```

The daemon unlinks its socket last, after the final saves, and that unlink is
what this command waits for. A refused connection is not the same signal: the
daemon closes its listener at the start of shutdown, while state is still
unsaved, so polling the socket for connectivity can report "stopped" before
anything has been persisted.

If the daemon has not finished within 10 seconds the command fails, naming the
pid and the socket, rather than returning success while the old process is still
running. Re-running it re-checks. Force killing with `kill -9` skips the final
save and loses any state written since the last periodic save.

When no daemon is running, the command reports that and removes a stale socket
if one is present. It exits 0 in that case.

### `tuios daemon`

Run the daemon in the foreground (for debugging).

**Usage:**
```bash
tuios daemon [flags]
```

**Flags:**
- `--log-level <level>` - Debug log level: `off`, `errors`, `basic`, `messages`, `verbose`, `trace`

**Debug log levels:**
- `off` - No debug output (default)
- `errors` - Only error messages
- `basic` - Connection events and errors
- `messages` - All protocol messages except PTY I/O
- `verbose` - All messages including PTY I/O
- `trace` - Full payload hex dumps

**Note:** This is primarily for debugging. The daemon starts automatically in the background when you run `tuios new` or `tuios attach`. Use this command to run the daemon in the foreground with debug logging.

### Workflow Example

```bash
# Start a new session for work
tuios new work

# ... do some work, then detach with Ctrl+B d ...

# Later, list your sessions
tuios ls

# Reattach to continue working
tuios attach work

# When done, kill the session
tuios kill-session work
```

---

## Remote Control Commands

TUIOS provides commands to control a running session from external scripts and tools. These commands communicate with the TUIOS daemon to send keystrokes, execute commands, and query state. This enables powerful scripting, automation, and integration with external tools.

> **Note:** When sending TUIOS commands via `send-keys`, `Ctrl+B` refers to the default leader key. This is configurable via the `leader_key` option in your config file.

### `tuios send-keys`

Send keystrokes to a TUIOS session.

**Usage:**
```bash
tuios send-keys <keys> [flags]
```

**Flags:**
- `-s, --session <name>` - Target session (default: most recently active)
- `-l, --literal` - Send keys directly to terminal PTY (bypass TUIOS key handling)
- `-r, --raw` - Treat each character as a separate key (no splitting on space/comma). **Required when sending text containing spaces or commas**

**Key Format:**
- Single keys: `i`, `n`, `Enter`, `Escape`, `Space`
- Key combos: `ctrl+b`, `alt+1`, `shift+Enter` (case-insensitive)
- Sequences: space or comma separated, e.g. `"ctrl+b q"` or `"ctrl+b,q"`
- Special token: `$PREFIX` or `PREFIX` expands to configured leader key

**IMPORTANT:** By default, spaces and commas separate multiple key arguments. To send literal text containing spaces (e.g., to type in a terminal), use BOTH `--literal` and `--raw` flags together

**Special Keys:** `Enter`, `Return`, `Space`, `Tab`, `Escape`, `Esc`, `Backspace`, `Delete`, `Up`, `Down`, `Left`, `Right`, `Home`, `End`, `PageUp`, `PageDown`, `F1`-`F12`

**Modifiers:** `ctrl`, `alt`, `shift`, `super`, `meta`

**Examples:**
```bash
# Enter terminal mode (press 'i')
tuios send-keys i

# Press Enter
tuios send-keys Enter

# Trigger prefix key followed by 'q' (quit)
tuios send-keys "ctrl+b q"
tuios send-keys "\$PREFIX q"

# Send Ctrl+C to TUIOS
tuios send-keys ctrl+c

# Send literal text directly to terminal PTY (use --raw to prevent space splitting)
tuios send-keys --literal --raw "echo hello"

# Send text with spaces (each character is a key)
tuios send-keys --raw "hello world"

# Send to a specific session
tuios send-keys --session mysession Escape
tuios send-keys -s mysession Escape
```

### `tuios send-text`

Write text verbatim to a pane's PTY, with no key parsing at all.

Nothing in the argument is interpreted, so spaces, quotes and punctuation
arrive as typed. A trailing newline is the Enter that runs the line, which
makes this one call where `send-keys` needs two.

**Usage:**
```bash
tuios send-text <text> [flags]
```

**Flags:**
- `-s, --session <name>` - Target session (default: most recently active)
- `-w, --window <id-or-name>` - Target window (default: focused)

**Examples:**
```bash
# Run a command in the focused pane (the trailing newline submits it)
tuios send-text 'go build ./...
'

# Type without submitting
tuios send-text -w build 'partial input'

# Text with spaces, quotes and commas needs no flags
tuios send-text -w build 'git commit -m "fix: cache, retries"'
```

### `tuios new-window`

Open a new window in a session and print its id.

The window is created by the daemon whether or not a client is attached, so
this works on a detached session. Give it a name to address it later without
holding on to the id.

**Usage:**
```bash
tuios new-window [name] [flags]
```

**Flags:**
- `-s, --session <name>` - Target session (default: most recently active)
- `--json` - Output result as JSON

**Output:**
The 8-character window id and the window's name, separated by two spaces:

```
a1b2c3d4  build
```

That id prefix is what `-w` accepts everywhere else.

**Examples:**
```bash
# Open an unnamed window
tuios new-window

# Open a named window and run something in it
tuios new-window build
tuios send-text -w build 'go build ./...
'

# Capture the new window's id for scripting
tuios new-window --json | jq -r .window_id

# JSON output carries the full id and the name
tuios new-window --json build
# Output: {"success":true,"message":"command executed","window_id":"a1b2c3d4-...","name":"build"}

# Target a specific session
tuios new-window -s mysession dev
```

### `tuios run-command`

Execute a TUIOS command (same commands available via tape scripts).

**Usage:**
```bash
tuios run-command <command> [args...] [flags]
```

**Flags:**
- `-s, --session <name>` - Target session (default: most recently active)
- `--json` - Output result as JSON (useful for scripting)
- `--list` - List all available commands

**Available Commands:**
| Command | Arguments | Description |
|---------|-----------|-------------|
| `NewWindow` | `[name]` | Create a new terminal window |
| `CloseWindow` | | Close the focused window |
| `FocusNext` | | Focus the next window |
| `FocusPrev` | | Focus the previous window |
| `FocusWindow` | `<id-or-name>` | Focus a specific window |
| `ToggleFullscreen` | | Toggle fullscreen mode |
| `ToggleTiling` | | Toggle tiling mode |
| `SetTheme` | `<theme>` | Change the color theme |
| `SwitchWorkspace` | `<1-9>` | Switch to workspace |
| `MoveToWorkspace` | `<1-9>` | Move focused window to workspace |
| `MinimizeWindow` | | Minimize focused window |
| `RestoreWindow` | `<id-or-name>` | Restore a minimized window |
| `SetDockbarPosition` | `<position>` | Set dockbar position (top/bottom/left/right) |

**Examples:**
```bash
# List all available commands
tuios run-command --list

# Create a new window
tuios run-command NewWindow "my-terminal"

# Create window and get JSON output with window ID
tuios run-command --json NewWindow "my-terminal"
# Output: {"success":true,"message":"Created window 'my-terminal'","data":{"window_id":"abc123","name":"my-terminal"}}

# Switch workspace
tuios run-command SwitchWorkspace 2

# Toggle tiling
tuios run-command ToggleTiling

# Close focused window
tuios run-command CloseWindow

# Target a specific session
tuios run-command -s mysession NewWindow "dev"
```

### `tuios set-config`

Change TUIOS configuration at runtime.

**Usage:**
```bash
tuios set-config <path> <value> [flags]
```

**Flags:**
- `-s, --session <name>` - Target session (default: most recently active)

**Available Paths:**
| Path | Values | Description |
|------|--------|-------------|
| `dockbar_position` | `top`, `bottom`, `left`, `right` | Dockbar position |
| `border_style` | `rounded`, `normal`, `thick`, `double`, `hidden`, `block`, `ascii` | Border style |
| `animations` | `true`, `false`, `toggle` | Enable/disable animations |
| `hide_window_buttons` | `true`, `false` | Hide window buttons |
| `window_button_style` | `pill`, `dots` | How the window controls are drawn |
| `window_button_position` | `right`, `left` | Which end of the title bar they sit on |

**Examples:**
```bash
# Change dockbar position
tuios set-config dockbar_position top

# Change border style
tuios set-config border_style rounded

# Toggle animations
tuios set-config animations toggle

# Hide window buttons
tuios set-config hide_window_buttons true
tuios set-config window_button_style dots
tuios set-config window_button_position left

# Target a specific session
tuios set-config -s mysession dockbar_position bottom
```

### `tuios wait-for`

Block until the daemon reports that a condition matched.

The daemon watches its own events, so a script stops polling a pane and
sleeping between captures. The command exits `0` when the condition matches,
and non-zero with the `timeout` error when it does not match before
`--timeout`.

**Usage:**
```bash
tuios wait-for <condition> [flags]
```

**Conditions:**
| Condition | Matches when |
|-----------|--------------|
| `session-exists` | The named session is present |
| `window-output` | The window's content matches `--pattern` |
| `window-exit` | The window's shell exited |
| `window-idle` | The window printed nothing for `--idle` milliseconds |
| `agent-state` | An agent reached one of the `--until` states; without `--window`, any agent pane in the session matches |

**Flags:**
- `-s, --session <name>` - Target session (default: most recently active)
- `-w, --window <id-or-name>` - Target window (default: focused; `agent-state`: any window)
- `--pattern <regexp>` - Go regular expression (RE2), required by `window-output`
- `--until <states>` - Agent state(s) to wait for, comma-separated, required by `agent-state`
- `--idle <ms>` - Milliseconds of silence that count as idle (default: 500)
- `--timeout <ms>` - Milliseconds to wait before giving up (default: 30000)
- `--json` - Output result as JSON

The `window-output` pattern is matched against the window's scrollback, so
output that has already scrolled off the visible screen still matches, and
output printed before the wait started matches immediately.

That last part has a sharp edge. A pane echoes the command it was sent, so a
marker written literally in the command matches its own echo and the wait
returns before the work has run. A marker left in the scrollback by an earlier
run matches the same way. Assemble the marker so the literal appears only in the
output (`printf "BUILD_%s\n" OK`), or use `window-exit` in a window opened for
the one command.

**Examples:**
```bash
# Wait for a build to print its marker
tuios wait-for window-output -w build --pattern 'BUILD OK'

# Wait for a pane to go quiet for two seconds
tuios wait-for window-idle -w build --idle 2000

# Wait for a command's shell to exit, allowing ten minutes
tuios wait-for window-exit -w build --timeout 600000

# Wait for a session to appear
tuios wait-for session-exists -s work

# Wait until any agent in the session is waiting on a human
tuios wait-for agent-state -s work --until needs_input

# Branch on the result
if tuios wait-for window-output -w build --pattern 'BUILD OK' --timeout 60000; then
    echo "build finished"
else
    echo "build timed out"
fi
```

### `tuios set-agent-state`

Report a pane's agent state so the session can show which panes need
attention.

**Usage:**
```bash
tuios set-agent-state <state> [flags]
```

**States:** `none`, `working`, `needs_input`, `idle`, `done`, `errored`

**Flags:**
- `-s, --session <name>` - Target session (default: most recently active)
- `-w, --window <id-or-name>` - Target window (default: focused)
- `-m, --message <text>` - Short note reported with the state
- `--source <source>` - Where the state came from: `report`, `osc`, `screen`, `stall` (default: `report`)
- `--harness <id>` - Id of the harness the state is about, e.g. `claude-code`

**Sources and precedence:**
More than one source can have an opinion about the same pane. Each source is
ranked, and a source may write over a claim ranked at or below its own and
never over one ranked above it. A source updating its own claim is the
same-rank case and is always allowed.

| Source | Rank | What it is |
|--------|------|------------|
| `report` | highest | The harness, or its hook shim, calling for itself |
| `osc` | | An escape sequence the pane emitted |
| `screen` | | A rule matched against the pane's rendered text |
| `stall` | lowest | The output-stall heuristic |

There is one exception, for the case ranking alone gets wrong: a `screen` rule
that matched a blocking prompt may write over a higher-ranked claim that has
gone stale, meaning the pane has painted since that claim was stamped and the
claim has stood unrefreshed for two seconds. The displaced claim is put back the
moment a later look finds the prompt gone. It applies only to the daemon's own
screen tier, which has actually read the pane; passing `--source screen` here
never overrides anything. See
[Agent state](AGENT_STATE.md#the-one-exception-a-visible-blocker).

`report` is the default, so a caller written before sources existed keeps its
authority. A report the daemon declines prints to stderr and leaves the state
alone:

```
Not applied: a higher-ranked source owns this pane; it still reports working.
```

**Examples:**
```bash
# Mark the focused pane as working
tuios set-agent-state working

# Mark a specific pane as needing input, with a note
tuios set-agent-state needs_input -w build -m "awaiting approval"

# Report on behalf of a named harness, from an escape sequence
tuios set-agent-state working --source osc --harness claude-code

# Clear a pane's agent state
tuios set-agent-state none
```

### `tuios set-session-name`

Set the label a session shows in the sidebar and the dock.

The session keeps its own name for addressing, persistence and
`TUIOS_SESSION`, so a script that targets it by name keeps working. Pass no
name to clear the label.

**Usage:**
```bash
tuios set-session-name [name] [flags]
```

**Flags:**
- `-s, --session <name>` - Target session (default: most recently active)

**Examples:**
```bash
# Label the current session
tuios set-session-name "Payments API"

# Label a specific session
tuios set-session-name -s work "Payments API"

# Clear the label
tuios set-session-name
```

### `tuios set-session-accent`

Set the accent a session uses. It is shared by every client attached to the
session and kept across a reattach. Pass no accent to clear it.

**Usage:**
```bash
tuios set-session-accent [accent] [flags]
```

**Flags:**
- `-s, --session <name>` - Target session (default: most recently active)

**Examples:**
```bash
# Accent the current session
tuios set-session-accent cyan

# Accent a specific session
tuios set-session-accent -s work cyan

# Clear the accent
tuios set-session-accent
```

### `tuios set-workspace-name`

Name a workspace so the dock and the sidebar show the label instead of the
number. The number stays the workspace's identity, and is the label an unnamed
workspace shows. Pass no name to clear it.

**Usage:**
```bash
tuios set-workspace-name <workspace> [name] [flags]
```

**Arguments:**
- `workspace` - Workspace number, 1-based
- `name` - Label for the workspace. Omit to clear it.

**Flags:**
- `-s, --session <name>` - Target session (default: most recently active)

**Examples:**
```bash
# Name workspace 2
tuios set-workspace-name 2 review

# Name a workspace in a specific session
tuios set-workspace-name -s work 2 review

# Clear the name
tuios set-workspace-name 2
```

---

## Inspection Commands

Query the state of a running TUIOS session. These commands are designed for scripting and return structured data about windows and session state.

**Note:** These commands query the daemon's stored state directly and work even when no TUI client is attached to the session. This makes them ideal for background scripting and monitoring.

### `tuios list-verbs`

Print the control protocol's verb catalog: every verb with its parameter schema,
accepted values, and example requests, plus the protocol version and the stable
error codes.

```bash
tuios list-verbs [verb] [--json]
```

This is the discovery entry point for scripting and for agents driving TUIOS. It
needs no documentation to interpret: the schema, the value sets, and the error
vocabulary are all in the output.

**Examples:**

```bash
# Every verb with its parameters
tuios list-verbs

# Just one verb
tuios list-verbs capture-pane

# Machine-readable
tuios list-verbs --json | jq '.verbs[].verb'
```

### `tuios list-windows`

List all windows in a TUIOS session.

**Usage:**
```bash
tuios list-windows [flags]
```

**Flags:**
- `-s, --session <name>` - Target session (default: most recently active)
- `--json` - Output as JSON (default is human-readable table)

**Examples:**
```bash
# List windows in table format
tuios list-windows

# Output as JSON for scripting
tuios list-windows --json

# Query a specific session
tuios list-windows -s mysession --json
```

**Example output:**
```
╭─────┬──────────┬───────┬────┬────────┬─────────╮
│ IDX │ ID       │ NAME  │ WS │ SIZE   │ AGENT   │
├─────┼──────────┼───────┼────┼────────┼─────────┤
│ *1  │ a1b2c3d4 │ dev   │ 1  │ 120x40 │         │
│ 2   │ e5f6a7b8 │ build │ 1  │ 120x40 │ working │
╰─────┴──────────┴───────┴────┴────────┴─────────╯

2 window(s). * marks the focused one.
```

The `ID` column is the 8-character prefix that `-w` accepts. With no windows
the command says so and points at `tuios new-window`.

**JSON Output Structure:**
```json
{
  "windows": [
    {
      "id": "a1b2c3d4",
      "title": "Terminal a1b2c3d4",
      "custom_name": "dev",
      "display_name": "dev",
      "workspace": 1,
      "focused": true,
      "minimized": false,
      "fullscreen": false,
      "x": 0,
      "y": 0,
      "width": 120,
      "height": 40,
      "cursor_x": 5,
      "cursor_y": 10,
      "cursor_visible": true,
      "scrollback_lines": 1000,
      "shell_pid": 12345,
      "has_foreground_process": false
    }
  ],
  "total": 1,
  "focused_id": "a1b2c3d4"
}
```

### `tuios get-window`

Get detailed information about a specific window.

**Usage:**
```bash
tuios get-window [id-or-name] [flags]
```

**Arguments:**
- `id-or-name` - Window ID or custom name. If omitted, returns the focused window.

**Flags:**
- `-s, --session <name>` - Target session (default: most recently active)
- `--json` - Output as JSON (default is human-readable)

**Examples:**
```bash
# Get focused window info
tuios get-window

# Get focused window as JSON
tuios get-window --json

# Get specific window by name
tuios get-window dev --json

# Get window by ID
tuios get-window a1b2c3d4 --json

# Query a specific session
tuios get-window -s mysession dev --json
```

**Example output:**
```
name           build
id             e5f6a7b8-1c2d-4e3f-9a8b-7c6d5e4f3a2b
index          2
title          Terminal e5f6a7b8
workspace      1
size           120x40
focused        false
minimized      false
agent          working
agent message  awaiting approval
```

The `agent message` line appears only when the pane reported one.

**JSON Output Structure:**
```json
{
  "id": "a1b2c3d4",
  "title": "Terminal a1b2c3d4",
  "custom_name": "dev",
  "display_name": "dev",
  "workspace": 1,
  "focused": true,
  "minimized": false,
  "fullscreen": false,
  "x": 0,
  "y": 0,
  "width": 120,
  "height": 40,
  "cursor_x": 5,
  "cursor_y": 10,
  "cursor_visible": true,
  "scrollback_lines": 1000,
  "shell_pid": 12345,
  "has_foreground_process": false
}
```

### `tuios session-info`

Get information about the TUIOS session state.

**Usage:**
```bash
tuios session-info [flags]
```

**Flags:**
- `-s, --session <name>` - Target session (default: most recently active)
- `--json` - Output as JSON (default is human-readable)

**Examples:**
```bash
# Get session info in human-readable format
tuios session-info

# Get session info as JSON
tuios session-info --json

# Query a specific session
tuios session-info -s mysession --json
```

**Example output:**
```
session        work
display name   Payments API
accent         cyan
windows        3
workspace      1 of 9
tiling         bsp
size           120x40
attached       true
named          2=review
```

The `display name`, `accent` and `named` lines appear only when those are set.

**JSON Output Structure:**
```json
{
  "current_workspace": 1,
  "total_windows": 3,
  "mode": "terminal",
  "tiling_enabled": true,
  "tiling_mode": "bsp",
  "theme": "tokyonight",
  "dockbar_position": "bottom",
  "animations_enabled": true,
  "script_mode": false,
  "workspace_windows": [2, 1, 0, 0, 0, 0, 0, 0, 0]
}
```

**Fields:**
| Field | Description |
|-------|-------------|
| `current_workspace` | Active workspace number (1-9) |
| `total_windows` | Total number of windows across all workspaces |
| `mode` | Current input mode: `terminal` or `window_management` |
| `tiling_enabled` | Whether tiling mode is active |
| `tiling_mode` | Tiling algorithm: `bsp`, `horizontal`, `vertical` |
| `theme` | Current color theme |
| `dockbar_position` | Dockbar location: `top`, `bottom`, `left`, `right` |
| `animations_enabled` | Whether animations are enabled |
| `script_mode` | Whether in tape script execution mode |
| `workspace_windows` | Array of window counts per workspace (indices 0-8 for workspaces 1-9) |

### `tuios capture-pane`

Capture the content of a terminal pane and write it to stdout.

**Usage:**
```bash
tuios capture-pane [flags]
```

**Flags:**
- `-s, --session <name>` - Target session (default: most recently active)
- `-w, --window <id-or-name>` - Target window (default: focused)
- `-S, --scrollback` - Include the full scrollback history
- `--lines <N>` - Keep only the last N lines (0 keeps all)
- `--ansi` - Preserve ANSI escape codes (colors, styles)

`--lines` counts from the last line that has content, so the blank rows below
the cursor do not count. This is how you read the tail of a long scrollback
without pulling all of it.

**Examples:**
```bash
# Capture the focused window's visible screen
tuios capture-pane

# Capture a specific window with its scrollback
tuios capture-pane -w build --scrollback

# Read the last 40 lines a build printed
tuios capture-pane -w build --scrollback --lines 40

# Capture with ANSI colors preserved
tuios capture-pane --ansi

# Pipe to a file
tuios capture-pane -w editor --scrollback > pane.txt
```

---

## Scripting Examples

These remote control and inspection commands enable powerful scripting workflows.

### Create and Focus Windows

```bash
#!/bin/bash
# Create a development layout

# Create windows and capture their IDs
EDITOR_ID=$(tuios run-command --json NewWindow "editor" | jq -r '.data.window_id')
TERMINAL_ID=$(tuios run-command --json NewWindow "terminal" | jq -r '.data.window_id')
LOGS_ID=$(tuios run-command --json NewWindow "logs" | jq -r '.data.window_id')

# Enable tiling
tuios run-command ToggleTiling

# Send commands to each window
tuios send-keys --literal --raw "nvim ." && tuios send-keys Enter
tuios run-command FocusWindow "$TERMINAL_ID"
tuios run-command FocusWindow "$LOGS_ID"
tuios send-keys --literal --raw "tail -f /var/log/system.log" && tuios send-keys Enter
```

### Query and React to State

```bash
#!/bin/bash
# Wait for a specific condition

# Wait until there are at least 3 windows
while true; do
    WINDOW_COUNT=$(tuios session-info --json | jq '.total_windows')
    if [ "$WINDOW_COUNT" -ge 3 ]; then
        echo "Ready with $WINDOW_COUNT windows"
        break
    fi
    sleep 0.5
done
```

### Run a Command and Wait for It

```bash
#!/bin/bash
# Open a window, run a build in it, and block until it finishes.
# The marker is assembled by printf so the literal BUILD_OK never appears in
# the command line the pane echoes, which would match the wait immediately.

tuios new-window build
tuios send-text -w build 'go build ./... ; printf "BUILD_%s %s\n" OK "$?"
'

if tuios wait-for window-output -w build --pattern 'BUILD_OK' --timeout 120000; then
    tuios capture-pane -w build --scrollback --lines 5
else
    tuios capture-pane -w build --scrollback --lines 40
    exit 1
fi
```

### Integration with Other Tools

```bash
#!/bin/bash
# Use fzf to select and focus a window

WINDOW=$(tuios list-windows --json | \
    jq -r '.windows[] | "\(.display_name)\t\(.id)"' | \
    fzf --with-nth=1 | \
    cut -f2)

if [ -n "$WINDOW" ]; then
    tuios run-command FocusWindow "$WINDOW"
fi
```

### Automated Testing

```bash
#!/bin/bash
# Run a command and verify output

tuios send-keys --literal --raw "echo 'test-marker-12345'" && tuios send-keys Enter
sleep 0.5

# Check if command completed (cursor moved)
CURSOR_Y=$(tuios get-window --json | jq '.cursor_y')
echo "Cursor at line: $CURSOR_Y"
```

---

### `tuios ssh`

Run TUIOS as an SSH server for remote access.

By default, SSH sessions connect to the TUIOS daemon for persistent sessions with multi-client support. This means:
- Sessions persist even when clients disconnect
- Multiple clients can view/control the same session simultaneously
- Session state (windows, workspaces) is preserved across reconnections

**Usage:**
```bash
tuios ssh [flags]
```

**Flags:**
- `--host <string>` - SSH server host (default: "localhost")
- `--port <string>` - SSH server port (default: "2222")
- `--key-path <string>` - Path to SSH host key (auto-generated if not specified)
- `--default-session <string>` - Default session name for all connections
- `--ephemeral` - Run in ephemeral mode (standalone, no daemon)

The interface flags (`--theme`, `--border-style`, `--dockbar-position`,
`--ascii-only`, and the rest of the appearance set) apply to every served
session, layered over the server's config file the same way a local run
layers them.

Settings changed from the in-app settings page apply to that session only;
the server operator's config file is never written from an SSH client.

**Session Selection Priority:**
1. `--default-session` flag (if specified)
2. SSH username (if not generic like "tuios", "root", "anonymous")
3. SSH command argument (e.g., `ssh host attach mysession`)
4. First available session or create new

**Examples:**
```bash
# Start SSH server on default port (daemon mode)
tuios ssh

# Start on custom port
tuios ssh --port 8022

# Listen on all interfaces
tuios ssh --host 0.0.0.0 --port 2222

# Use custom host key
tuios ssh --key-path /path/to/host_key

# All clients share a single session
tuios ssh --default-session shared

# Run in ephemeral mode (no session persistence)
tuios ssh --ephemeral
```

**Connecting:**
```bash
# Basic connection
ssh -p 2222 localhost

# Connect to a specific session via username
ssh -p 2222 mysession@localhost

# Connect to a specific session via command
ssh -p 2222 localhost attach mysession
```

**Multi-Client Behavior:**
- When multiple clients connect to the same session, the effective terminal size is the minimum of all client dimensions
- State changes (window create/move, workspace switch, etc.) are broadcast to all clients in real-time
- Clients are notified when others join or leave the session

---

## `tuios-web` (Separate Binary)

**Security Notice:** The web terminal functionality has been extracted to a separate binary (`tuios-web`) to provide better security isolation. This prevents the web server from being used as a potential backdoor in the main TUIOS binary.

By default, web sessions connect to the TUIOS daemon for persistent sessions with multi-client support. This means:
- Sessions persist even when browser tabs close
- Multiple browsers/tabs can view/control the same session simultaneously
- Session state (windows, workspaces) is preserved across reconnections
- Settings changed from the in-app settings page apply to that session only; the server's config file is never written from a browser

**Installation:**
```bash
# Homebrew
brew install tuios-web

# AUR
yay -S tuios-web-bin

# Go install
go install github.com/Gaurav-Gosain/tuios/cmd/tuios-web@latest
```

**Usage:**
```bash
tuios-web [flags]
```

**Flags:**
- `--host <string>` - Web server host (default: "localhost")
- `--port <string>` - Web server port (default: "7681")
- `--read-only` - Disable input from clients (view only mode)
- `--max-connections <int>` - Maximum concurrent connections (default: 0 = unlimited)
- `--cert <path>` - TLS certificate in PEM form (serves HTTPS; required to bind a non-loopback host)
- `--key <path>` - TLS private key in PEM form (required with `--cert`)
- `--auto-tls` - Generate and serve a self-signed certificate (managed with `tuios-web cert`)
- `--insecure` - Serve a non-loopback host over plain HTTP, unencrypted (trusted networks only)
- `--touch <auto|on|off>` - Touch support and the on-screen key bar (default: auto-detect)
- `--default-session <string>` - Default session name for all connections (creates shared session)
- `--ephemeral` - Disable daemon mode (sessions don't persist)
- `--theme <name>` - Color theme forwarded to TUIOS instances
- `--show-keys` - Enable showkeys overlay
- `--ascii-only` - Use ASCII characters instead of Nerd Font icons
- `--border-style <style>` - Window border style
- `--dockbar-position <pos>` - Dockbar position
- `--hide-window-buttons` - Hide window control buttons
- `--window-button-style <style>` - Window control style: `pill`, `dots`
- `--window-button-position <position>` - Which end they sit on: `right`, `left`
- `--scrollback-lines <int>` - Scrollback buffer size
- `--no-animations` - Disable UI animations
- `--debug` - Enable debug logging

**Subcommands:**
- `tuios-web cert` - Show the status of the self-signed TLS certificate `--auto-tls` uses
- `tuios-web cert new|info|path|remove` - Rotate, explain, locate, or delete it

**Features:**
- Full TUIOS experience in the browser
- WebGL-accelerated rendering via xterm.js for smooth 60fps
- WebSocket and WebTransport (HTTP/3 over QUIC) protocols
- Bundled JetBrains Mono Nerd Font for proper icon rendering
- Settings panel for transport, renderer, and font size preferences
- Cell-based mouse event deduplication (80-95% traffic reduction)
- Automatic reconnection with exponential backoff
- Self-signed TLS certificate generation for development
- No CGO dependencies (pure Go)
- **Persistent sessions via daemon mode** (default)
- **Multi-client support** - multiple browsers share the same session

**Examples:**
```bash
# Start web server on default port (daemon mode)
tuios-web

# Start on custom port
tuios-web --port 8080

# Reach the server from a phone on the same network, over TLS with a
# self-signed certificate tuios-web generates and keeps for you
tuios-web --host 0.0.0.0 --port 7681 --auto-tls

# Or bring your own certificate
tuios-web --host 0.0.0.0 --port 7681 --cert tuios-cert.pem --key tuios-key.pem

# Same, on a network you trust, with nothing encrypted
tuios-web --host 0.0.0.0 --port 7681 --insecure

# Start in read-only mode (view only)
tuios-web --read-only

# Start with theme and show-keys overlay
tuios-web --theme dracula --show-keys

# Limit concurrent connections
tuios-web --max-connections 10

# All clients share a single session
tuios-web --default-session shared

# Run in ephemeral mode (no session persistence)
tuios-web --ephemeral
```

**Multi-Client Behavior:**
- When multiple clients connect to the same session, the effective terminal size is the minimum of all client dimensions
- State changes (window create/move, workspace switch, etc.) are broadcast to all clients in real-time
- Clients are notified when others join or leave the session

**Accessing:**
```bash
# Open in browser
open http://localhost:7681

# For HTTPS/WebTransport (development with self-signed cert)
open https://localhost:7681

# Note: Your browser will show a security warning for the self-signed certificate.
# Click "Advanced" and proceed to accept the certificate.
```

**Protocol Selection:**
The client automatically selects the best available transport:
1. **WebTransport (HTTP/3 over QUIC)** - Lower latency, better multiplexing (requires HTTPS)
2. **WebSocket (fallback)** - Broad browser compatibility

For complete documentation, see [Web Terminal Mode](WEB.md).

---

### `tuios config`

Manage TUIOS configuration file.

**Subcommands:**
- `tuios config path` - Print configuration file path
- `tuios config edit` - Edit configuration in $EDITOR
- `tuios config reset` - Reset configuration to defaults

#### `tuios config path`

Print the location of the TUIOS configuration file.

**Example:**
```bash
tuios config path
# Output: /Users/username/.config/tuios/config.toml
```

#### `tuios config edit`

Open the configuration file in your default editor.

**Requirements:** The `$EDITOR` or `$VISUAL` environment variable must be set. Falls back to vim, vi, nano, or emacs if found.

**Example:**
```bash
export EDITOR=vim
tuios config edit
```

#### `tuios config reset`

Reset the configuration file to default settings.

**Warning:** This will overwrite your existing configuration after confirmation.

**Example:**
```bash
tuios config reset
# Prompts: Are you sure you want to reset to defaults? (yes/no):
```

---

### `tuios keybinds`

View and inspect keybinding configuration.

**Aliases:** `keys`, `kb`

**Subcommands:**
- `tuios keybinds list` - List all configured keybindings
- `tuios keybinds list-custom` - List only customized keybindings

#### `tuios keybinds list`

Display all configured keybindings in formatted tables organized by category.

**Example:**
```bash
tuios keybinds list
```

**Output:** Shows comprehensive tables with all keybindings across categories:
- Window Management
- Workspaces
- Layout
- Modes
- Selection
- System

#### `tuios keybinds list-custom`

Show only keybindings that differ from defaults, with a comparison view.

**Example:**
```bash
tuios keybinds list-custom
```

**Output:** Three-column table showing:
- Action name
- Default keybinding
- Your custom keybinding

---

### `tuios layout`

Manage saved layout templates.

**Subcommands:**
- `tuios layout list` - List all saved layout templates
- `tuios layout delete <name>` - Delete a saved layout template
- `tuios layout dir` - Print the layout templates directory path
- `tuios layout export <name>` - Export a layout template as JSON

#### `tuios layout list`

List all saved layout templates.

**Example:**
```bash
tuios layout list
```

#### `tuios layout delete`

Delete a saved layout template by name.

**Usage:**
```bash
tuios layout delete <name>
```

**Example:**
```bash
tuios layout delete dev-layout
```

#### `tuios layout dir`

Print the path to the layout templates directory.

**Example:**
```bash
tuios layout dir
# Output: /home/user/.config/tuios/layouts
```

#### `tuios layout export`

Export a saved layout template as JSON for sharing or backup.

**Usage:**
```bash
tuios layout export <name>
```

**Example:**
```bash
tuios layout export dev-layout
```

---

### `tuios completion`

Generate shell completion scripts for command-line autocompletion.

**Supported shells:**
- bash
- zsh
- fish
- powershell

**Usage:**
```bash
tuios completion [shell]
```

**Examples:**

**Bash:**
```bash
# Generate and install completion
tuios completion bash > /etc/bash_completion.d/tuios

# Or for user-specific completion
tuios completion bash > ~/.local/share/bash-completion/completions/tuios
source ~/.bashrc
```

**Zsh:**
```bash
# Generate and install completion
tuios completion zsh > "${fpath[1]}/_tuios"

# Or add to your .zshrc
echo "autoload -U compinit; compinit" >> ~/.zshrc
tuios completion zsh > ~/.zsh/completions/_tuios
```

**Fish:**
```bash
# Generate and install completion
tuios completion fish > ~/.config/fish/completions/tuios.fish
```

**PowerShell:**
```bash
# Generate completion script
tuios completion powershell > tuios.ps1

# Add to your PowerShell profile
echo ". $(pwd)/tuios.ps1" >> $PROFILE
```

---

### `tuios help`

Get help about any command.

**Usage:**
```bash
tuios help [command]
```

**Examples:**
```bash
tuios help              # Show general help
tuios help ssh          # Show help for ssh command
tuios help config edit  # Show help for config edit subcommand
```

---

## Global Flags

These flags are available on the root command:

- `--theme <name>` - Set color theme (default: "tokyonight")
- `--list-themes` - List all available themes and exit
- `--preview-theme <name>` - Preview a theme's colors and exit
- `--skill` - Print the embedded agent skill and exit
- `--ascii-only` - Use ASCII characters instead of Nerd Font icons
- `--show-keys` - Enable showkeys overlay (screencaster-style key display)
- `--show-clock` - Show clock in the status area
- `--show-cpu` - Show CPU usage in the status area
- `--show-ram` - Show RAM usage in the status area
- `--shared-borders` - Enable shared borders between tiled windows
- `--debug` - Enable debug logging
- `--cpuprofile <file>` - Write CPU profile to file
- `-h, --help` - Show help

---

## Common Usage Examples

### Basic Usage

Start TUIOS normally:
```bash
tuios

# Start with showkeys overlay for screencasting
tuios --show-keys
```

### Theming

```bash
# Start with a specific theme
tuios --theme dracula

# List all available themes
tuios --list-themes

# Preview a theme before using it
tuios --preview-theme nord

# Interactive theme selection with fzf
tuios --theme $(tuios --list-themes | fzf --preview 'tuios --preview-theme {}')

# Use ASCII mode (no Nerd Font required)
tuios --ascii-only

# Combine theme with ASCII mode
tuios --theme gruvbox_dark --ascii-only
```

### Configuration Management

```bash
# Find config file location
tuios config path

# Edit configuration
tuios config edit

# View all keybindings
tuios keybinds list

# View your customizations
tuios keybinds list-custom

# Reset to defaults
tuios config reset
```

### Daemon Mode (Session Persistence)

```bash
# Create a new persistent session
tuios new mysession

# List all sessions
tuios ls

# Attach to an existing session
tuios attach mysession

# Detach from session (inside TUIOS)
# Press Ctrl+B d

# Kill a session
tuios kill-session mysession

# Stop the daemon (kills all sessions)
tuios kill-server
```

### SSH Server Setup

```bash
# Start SSH server on default port
tuios ssh

# Start on custom port with remote access
tuios ssh --host 0.0.0.0 --port 8022

# Connect from another machine
ssh -p 8022 your-server-hostname
```

### Web Terminal Setup (tuios-web)

```bash
# Start web terminal on default port
tuios-web

# Start on custom port with remote access
tuios-web --host 0.0.0.0 --port 8080

# Open in browser
open http://localhost:7681

# Start in read-only mode for demonstrations
tuios-web --read-only

# Start with theme and overlay
tuios-web --theme dracula --show-keys

# Limit connections for production use
tuios-web --max-connections 50 --host 0.0.0.0
```

### Development & Debugging

```bash
# Run with debug logging
tuios --debug
# Then press Ctrl+L during runtime to view logs

# CPU profiling
tuios --cpuprofile cpu.prof
# Use the application, then exit
go tool pprof cpu.prof

# Screencasting with showkeys overlay
tuios --show-keys
# Or toggle during runtime with: Ctrl+B D k
```

### Shell Completions

```bash
# Install bash completion
tuios completion bash | sudo tee /etc/bash_completion.d/tuios

# Install zsh completion
tuios completion zsh > "${fpath[1]}/_tuios"

# Install fish completion
tuios completion fish > ~/.config/fish/completions/tuios.fish
```

---

## Man Pages

TUIOS supports man page generation through the Fang framework using mango.

**Generate man page:**
```bash
# This feature is built-in via Fang
# Man page generation will be available in a future release
```

---

## Environment Variables

### `$EDITOR` / `$VISUAL`

Used by `tuios config edit` to determine which editor to open.

**Example:**
```bash
export EDITOR=vim
export VISUAL=code
tuios config edit
```

**Fallback order:** `$EDITOR` → `$VISUAL` → vim → vi → nano → emacs

### `$SHELL`

TUIOS uses your default shell from this variable. If not set, it attempts to detect the appropriate shell for your platform.

### `COLORTERM`

For best color support, set this to `truecolor`:
```bash
export COLORTERM=truecolor
```

---

## When Something Goes Wrong

Every failure `tuios` reports answers three questions in order: what failed, the
most likely cause, and the exact command that fixes it. If you meet a message
that does not, it is a bug worth reporting.

### The daemon is not running

There are three distinct versions of this, and they have different fixes:

| Message says | What it means | Fix |
| --- | --- | --- |
| "is not running" | No socket exists. The daemon has never run, or was stopped. | `tuios new` |
| "a stale socket is left over at ..." | The daemon crashed without cleaning up. | `tuios kill-server`, which removes it |
| "Permission denied ... socket" | The socket belongs to another user, or its mode changed. | Check `ls -l` on the path; set `XDG_RUNTIME_DIR` to a directory you own |

### The daemon is older than the CLI

After upgrading TUIOS, the old daemon keeps running and serving the socket. It
cannot speak the control protocol the new CLI uses, so commands fail:

```
The running TUIOS daemon does not speak this CLI's control protocol (daemon 0.9.0, CLI 1.4.0).
Most likely cause: TUIOS was upgraded while the daemon kept running, so the old daemon is still serving the socket.
Fix: run 'tuios kill-server', then run this command again.
```

Run `tuios kill-server`. Sessions are saved before the daemon exits and restored
when it next starts; `tuios resurrect` lists what is restorable.

### A session name is not found

The message lists the sessions that do exist and suggests the closest name:

```
Session "wrok" was not found.
Most likely cause: the name does not match any live session.
Did you mean "work"?
Sessions: notes, work.
Fix: run 'tuios ls' to list sessions, or 'tuios new wrok' to create this one.
```

A session killed with `tuios kill-session` is gone for good: killing is a
deliberate teardown, so its saved state is removed too. A session that merely
outlived its daemon is still restorable with `tuios resurrect`.

### A saved session will not restore

`tuios resurrect <name>` distinguishes three cases: no saved state at all, state
that is corrupt, and state written by a newer TUIOS. Unreadable state is moved
into an archive directory rather than deleted, and the message names that
directory so you can inspect or recover the file.

### The terminal cannot host the interface

`tuios attach`, `tuios new`, and `tuios resurrect` check the terminal before
taking over the screen, and refuse with an explanation when it is too small
(minimum 40x12), when `TERM` is unset or `dumb`, or when stdout is not a
terminal at all. For non-interactive use, drive a session with `tuios send-keys`
and `tuios capture-pane` instead of attaching.

### A session was killed while you were attached

The client exits with a non-zero status and says so, rather than leaving you in a
dead UI:

```
Session "work" was terminated while you were attached.
```

The same applies when the daemon itself goes away, which reports a lost
connection instead.

### Discovering the control protocol

`tuios list-verbs` prints every verb the daemon supports with its parameters,
accepted values, and runnable examples, plus the stable error codes. It is the
discovery entry point that the error hints point at, and `--json` makes it
machine-readable for an agent or a script.

```bash
tuios list-verbs                 # everything
tuios list-verbs capture-pane    # one verb
tuios list-verbs --json          # for scripting
```

---

## Exit Codes

- `0` - Success
- `1` - Error (configuration error, network error, file not found, etc.)

A `tuios attach` that ends because its session was killed, or because the daemon
was lost, exits `1`. A normal detach exits `0`.

---

## Version Information

The `--version` flag shows detailed build information:

```bash
tuios --version
```

**Output:**
```
tuios version v0.0.24
Commit: a1b2c3d
Built: 2025-01-15T10:30:00Z
By: goreleaser
```

---

## Command Migration Guide

If you're upgrading from an older version of TUIOS, here's how the commands have changed:

| Old Flag | New Command |
|----------|-------------|
| `--config-path` | `tuios config path` |
| `--edit-config` | `tuios config edit` |
| `--reset-config` | `tuios config reset` |
| `--list-keybinds` | `tuios keybinds list` |
| `--list-custom-keybinds` | `tuios keybinds list-custom` |
| `--ssh` | `tuios ssh` |
| `--ssh --host X --port Y` | `tuios ssh --host X --port Y` |
| `--version` | `tuios --version` or `tuios version` |
| `--help` | `tuios --help` or `tuios help` |

---

## Related Documentation

- [Configuration Guide](CONFIGURATION.md) - How to customize TUIOS
- [Keybindings Reference](KEYBINDINGS.md) - Complete keyboard shortcut reference
- [Architecture Guide](ARCHITECTURE.md) - Technical architecture details
- [README](../README.md) - Project overview and quick start
