# NAME

prise - architecture and concepts

# DESCRIPTION

Prise is a terminal multiplexer that uses a client-server architecture. The
server manages PTY sessions and persists them across client connections. Clients
connect to render the UI and handle input.

# ARCHITECTURE

## Server

The server runs as a background process, started with **prise serve** or via
a system service. It:

- Creates a Unix domain socket at */tmp/prise-{uid}.sock*
- Manages PTY sessions (spawning shells, handling I/O)
- Persists session state to *~/.local/share/prise/sessions/*
- Sends screen updates to connected clients
- Logs to *~/.cache/prise/server.log*

## Client

Each client connects to the server socket and:

- Renders the terminal UI using the local terminal
- Forwards keyboard and mouse input to the server
- Receives screen updates and repaints

Multiple clients can connect to the same session or pty simultaneously.

# SESSIONS

A **session** is a named collection of tabs and panes that persists across
client connections. Sessions are stored in *~/.local/share/prise/sessions/*.

**prise session list**
:   List all sessions

**prise session attach** [*name*]
:   Attach to a session (default: most recent)

**prise session delete** *name*
:   Delete a session

**prise session rename** *old* *new*
:   Rename a session

Sessions are visible to Lua code (via **prise.list_sessions()**) immediately
upon creation, before the autosave timer writes them to disk.

# SERVICE CONFIGURATION

The server should run continuously in the background. Prise provides service
files for automatic startup.

## macOS (launchd)

Install and enable with:

```
zig build --prefix ~/.local
zig build enable-service --prefix ~/.local
```

This creates a launchd plist at *~/Library/LaunchAgents/sh.prise.server.plist*
that starts the server at login.

To disable:

```
launchctl unload ~/Library/LaunchAgents/sh.prise.server.plist
```

## Linux (systemd)

Install and enable with:

```
zig build --prefix ~/.local
zig build enable-service --prefix ~/.local
```

This creates a systemd user service at
*~/.config/systemd/user/prise.service*.

To disable:

```
systemctl --user disable --now prise.service
```

# UI CONCEPTS

## Panes

A **pane** is a single terminal view backed by a PTY. Panes can be split
horizontally or vertically to create layouts.

## Tabs

A **tab** contains a layout of panes. Switch between tabs without affecting
the running processes.

## Command Mode

Press the leader key (default: **Super+k**) to enter command mode. The status
bar changes color to indicate command mode. Then press a key to execute a
command (e.g., **v** for horizontal split).

## Command Palette

Press **Super+p** to open the command palette. Type to fuzzy-search commands,
then press Enter to execute.

## Session Picker

In command mode (leader key), press **S** to open the session picker. Type to
filter sessions by name, use arrow keys to navigate, and press Enter to switch.

Press **R** in command mode to rename the current session.

# CUSTOM UI

The UI is implemented in Lua and can be customized or replaced entirely. See
**prise**(5) for configuration options.

The **prise** Lua module provides widget primitives:

- **Terminal**: Display a PTY
- **Text**: Static text with styling
- **Row**, **Column**: Layout containers
- **Stack**: Overlay widgets
- **Box**: Border and background
- **Padding**: Add spacing
- **List**: Scrollable list
- **TextInput**: Text input field
- **Positioned**: Absolute positioning

A custom UI must return a table with:

- **update(event)**: Handle input events
- **view()**: Return widget tree to render
- **get_state(cwd_lookup)**: Serialize state for persistence (optional)
- **set_state(saved, pty_lookup)**: Restore state (optional)

# SEE ALSO

[prise(1)](prise.1.html), [prise(5)](prise.5.html)
