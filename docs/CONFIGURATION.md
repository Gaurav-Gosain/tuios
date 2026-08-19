# Configuration Guide

TUIOS supports user-configurable keybindings through a TOML configuration file, following the XDG Base Directory specification.

## Table of Contents

- [Quick Start](#quick-start)
- [Configuration File Location](#configuration-file-location)
- [Configuration Structure](#configuration-structure)
- [Keybinding Sections](#keybinding-sections)
- [Notification Settings](#notification-settings)
- [Startup Settings](#startup-settings)
- [Hooks](#hooks)
- [Key Syntax](#key-syntax)
- [Platform-Specific Configuration](#platform-specific-configuration)
- [Best Practices](#best-practices)
- [Troubleshooting](#troubleshooting)

## Related Documentation

- **Command-Line Options**: See CLI documentation for runtime flags like `--theme` and `--ascii-only`
- **Keybinding Reference**: See [KEYBINDINGS.md](KEYBINDINGS.md) for complete list of default keybindings
- **Architecture Overview**: See [ARCHITECTURE.md](ARCHITECTURE.md) for system internals and component structure
- **Hooks**: See [HOOKS.md](HOOKS.md) for running shell commands on session events

**Note**: Many system constants (window sizes, animation speeds, refresh rates) are currently hardcoded in `internal/config/constants.go` and cannot be configured via TOML.

## Quick Start

### Find Your Configuration

```bash
tuios config path
```

### Edit Configuration

```bash
tuios config edit
```

### View Current Keybindings

```bash
# View all keybindings
tuios keybinds list

# View only your customizations
tuios keybinds list-custom
```

### Reset to Defaults

```bash
tuios config reset
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

[appearance]
border_style = "rounded"
dockbar_position = "bottom"
hide_window_buttons = false
scrollback_lines = 10000

[notifications]
duration = 6
warning_duration = 8
error_sticky = true

[startup]
open_default_window = false
tiled = false
start_in_terminal_mode = false
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
- `resize_master_shrink` - Decrease master window width in tiling mode
- `resize_master_grow` - Increase master window width in tiling mode
- `resize_height_shrink` - Decrease focused window height in tiling mode
- `resize_height_grow` - Increase focused window height in tiling mode

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
Arrow key navigation.

**Available actions:**
- `nav_up`, `nav_down`, `nav_left`, `nav_right` - Arrow key navigation

Text selection is copy mode's: select with the mouse, or enter copy mode and use `v`/`V`.

### restore_minimized
Individual minimized window restoration by number.

**Available actions:**
- `restore_minimized_1` through `restore_minimized_9` - Restore specific minimized window by number (Shift+1 through Shift+9)

### prefix_mode
Tmux-style prefix commands (the leader key followed by another key). Every
action in this section is configurable, and the leader key itself is set by
`leader_key` under `[keybindings]`.

**Example:**

```toml
[keybindings]
leader_key = "ctrl+a"

[keybindings.prefix_mode]
prefix_new_window = ["y"]
```

### window_prefix, minimize_prefix, workspace_prefix
Sub-menus accessible after prefix key (Ctrl+B + w/m/t). These provide alternative access to window management, minimize, and workspace commands through the prefix interface.

### debug_prefix
Debug and development tools submenu (Ctrl+B + D).

**Available actions:**
- `debug_prefix_logs` - Toggle log viewer (Ctrl+B D l)
- `debug_prefix_cache` - Toggle cache statistics (Ctrl+B D c)
- `debug_prefix_cancel` - Cancel debug prefix mode (Esc)

## Appearance Configuration

The `[appearance]` section controls the visual presentation of TUIOS.

**Available options:**

```toml
[appearance]
border_style = "rounded"
dockbar_position = "bottom"
hide_window_buttons = false
scrollback_lines = 10000
```

### border_style

Controls the style of window borders.

**Valid values:**
- `"rounded"` - Rounded corners (default)
- `"normal"` - Standard straight borders
- `"thick"` - Bold/thick borders
- `"double"` - Double-line borders
- `"hidden"` - No borders (automatically hides window buttons)
- `"block"` - Block-style borders
- `"ascii"` - ASCII-only characters for compatibility
- `"outer-half-block"` - Half-block style (outer)
- `"inner-half-block"` - Half-block style (inner)

**Default:** `"rounded"`

**CLI override:** `--border-style <style>`

### dockbar_position

Controls the position of the dockbar.

**Valid values:**
- `"bottom"` - Position dockbar at the bottom (default)
- `"top"` - Position dockbar at the top
- `"hidden"` - Hide dockbar

**Default:** `"bottom"`

**CLI override:** `--dockbar-position <position>`

### hide_window_buttons

Controls whether window control buttons (minimize, maximize, close) are displayed in the title bar.

**Valid values:**
- `false` - Show window buttons (default)
- `true` - Hide window buttons

**Default:** `false`

**Note:** Window buttons are automatically hidden when `border_style = "hidden"` regardless of this setting.

**CLI override:** `--hide-window-buttons`

### scrollback_lines

Controls the number of lines stored in the scrollback buffer for each terminal window.

**Valid values:** Integer between 100 and 1,000,000

**Default:** `10000`

**Note:** Values outside the valid range are automatically clamped. Higher values consume more memory.

**CLI override:** `--scrollback-lines <number>`

### scroll_lines

Controls how many lines a single mouse wheel notch scrolls in scrollback, copy mode and the scrollback browser.

**Valid values:** Integer between 1 and 50

**Default:** `3`

**Note:** Values outside the valid range are automatically clamped. Also settable from the in-app settings page (Advanced, "Scroll lines").

### copy_on_select

Controls whether releasing a mouse selection puts the text on the clipboard straight away, the way X11's primary selection and kitty's `copy_on_select` do. With it off, a selection stays highlighted and is copied only by pressing `y` in copy mode.

A click that never moved is not a selection and never writes to the clipboard, whatever this is set to.

A drag copies the moment the button comes up. A double-click or triple-click waits about a third of a second first, because a double-click is also the beginning of a triple-click and only the finished gesture should reach the clipboard. The highlight is immediate either way; only the clipboard write waits.

**Valid values:** `true`, `false`

**Default:** `true`

**Note:** The clipboard is written with OSC 52, the same path copy mode's `y` uses, so it reaches whatever the host terminal supports (including over SSH). Also settable from the in-app settings page (Advanced, "Copy on select").

### alt_drag

Controls whether Alt + left-drag moves a pane, the gesture nearly every desktop window manager binds. It works from anywhere on the pane, including its content while you are typing in it, and typing resumes when the pane lands.

Ctrl + left-drag moves a pane as well and is unaffected by this; the two are aliases, kept because one is already in people's fingers and the other is what a newcomer tries first.

Alt + right-drag resizes a pane from the nearest corner whatever this is set to. That is the ordinary right-drag resize, with Alt only keeping the context menu out of the way, so there is nothing here to turn off.

**Valid values:** `true`, `false`

**Default:** `true`

**Note:** With this on, Alt + left-drag is taken from a pane running an application that asked for the mouse (vim, less, htop), the same way Ctrl + left-drag already is. Set it to `false` to hand the gesture back to such an application; the pane then treats Alt + left-drag as ordinary text selection.

### click_to_type

Controls what a left click on a pane's content does while you are in window management mode, where the keyboard drives the window manager.

By default a click focuses the pane and enters terminal mode, so clicking a pane is enough to start typing in it. That is a mode change you did not ask for when all you wanted was to select a pane or click something else next, which is what the other two values are for.

**Valid values:**

- `"single"` - A click focuses the pane and enters terminal mode (default)
- `"double"` - A click focuses the pane; a double click enters terminal mode
- `"off"` - A click only focuses the pane, and never changes mode

**Default:** `"single"`

A double click here is the same gesture as the one that selects a word: the same two clicks, close together on the same cell. The clicks that change mode select nothing themselves, and the next click in the pane starts a fresh selection rather than counting as a third.

Nothing else about the mouse changes. Dragging a title bar or a border, Alt + left-drag, the context menu, the dock, the rail and the overlays all behave the same under every value, and a click in terminal mode is untouched.

**Note:** A pane running an application that asked for the mouse (vim, less, htop) is only sent mouse events in terminal mode, which is true today as well. Under `"double"` the second click gets you in; under `"off"` the way in is the key bound to `enter_terminal_mode` (Enter by default). Pick `"double"` if you live in such applications. Also settable from the in-app settings page (Behavior, "Click to type").

### word_characters

The punctuation that counts as part of a word when a double-click selects one. Letters and digits always count and do not need listing.

The default is chosen for what terminal output looks like: a path, a query string, a version number or a flag such as `--no-vm` selects as one word instead of breaking at every punctuation mark. A colon is deliberately absent, so `host:port` and `file:line` select as their parts; add `:` if you would rather have them whole.

**Valid values:** Any string of characters. An empty string means only letters and digits are word characters.

**Default:** `"@-./_~?&=%+#"`

**Note:** Triple-click selects the whole line and is not affected by this. Also settable from the in-app settings page (Advanced, "Word characters").

### window_title_position

Controls where window titles are displayed. Titles show the custom name if set by the user, otherwise the terminal's title (e.g., from shell prompt).

**Valid values:**
- `"bottom"` - Show title centered on the bottom border (default)
- `"top"` - Show title centered on the top border (with window buttons on the right)
- `"hidden"` - Hide window titles entirely

**Default:** `"bottom"`

**Note:** When set to `"hidden"`, the rename window keybinding (`r`) is disabled since there's no visible title to rename.

**CLI override:** `--window-title-position <position>`

### hide_clock

Controls whether the clock/status overlay is hidden.

**Valid values:**
- `false` - Show clock (default)
- `true` - Hide clock

**Default:** `false`

**Note:** The clock will still appear when recording a tape (red background) or when prefix mode is active (shows "PREFIX | time").

**CLI override:** `--hide-clock`

### animations_enabled

Controls whether UI animations are enabled.

**Valid values:**
- `true` - Enable animations (default)
- `false` - Disable animations for instant transitions

**Default:** `true`

**CLI override:** `--no-animations`

### show_clock

Controls whether the clock is shown in the status area.

**Valid values:**
- `false` - Hide clock (default)
- `true` - Show clock

**Default:** `false`

**CLI override:** `--show-clock`

### show_cpu

Controls whether CPU usage is shown in the status area.

**Valid values:**
- `false` - Hide CPU usage (default)
- `true` - Show CPU usage

**Default:** `false`

**CLI override:** `--show-cpu`

### show_ram

Controls whether RAM usage is shown in the status area.

**Valid values:**
- `false` - Hide RAM usage (default)
- `true` - Show RAM usage

**Default:** `false`

**CLI override:** `--show-ram`

### shared_borders

Controls whether windows share borders when tiling (reducing visual clutter).

**Valid values:**
- `false` - Each window draws its own borders (default)
- `true` - Adjacent windows share a single border line

**Default:** `false`

**CLI override:** `--shared-borders`

### dock_workspace_tab_format

Controls how each workspace tab in the dock strip is labelled. The format string supports two placeholders:

- `{index}` - The workspace number (e.g. `2`)
- `{name}` - The workspace name, or its number when it has no name

**Default:** empty, which is the same as `{name}` (a named workspace shows its name, an unnamed one shows its number).

**Examples:**

```toml
[appearance]
# Show the index and the name: "2: dev", "3: prod"
dock_workspace_tab_format = "{index}: {name}"

# Show the name with the index in parens: "dev (2)"
dock_workspace_tab_format = "{name} ({index})"
```

**Note:** Labels are still capped to fit the dock strip; a long name is truncated as usual.

### whichkey_enabled

Controls the which-key popup: a panel listing the keys available in the current
prefix chord, shown after you press the leader key and then wait.

The popup appears 500 milliseconds after the leader key is pressed and lists the
bindings for whichever chord is active (the top-level prefix, or the workspace,
minimize, window, debug, tape or layout submenu). It disappears as soon as you
press the next key, so it costs nothing if you already know the chord. It is
suppressed while the help overlay is open.

**Valid values:**
- `true` - Show the popup (default)
- `false` - Never show it

**Default:** `true`

**Also settable from:** the in-app settings page (`Ctrl+B` `,`), which persists
the change back to the config file.

**Note:** the popup draws with fixed colors and does not follow the active theme.

### whichkey_position

Which corner the which-key popup appears in.

**Valid values:**
- `"bottom-right"` (default)
- `"bottom-left"`
- `"top-right"`
- `"top-left"`
- `"center"`

**Default:** `"bottom-right"`

**Also settable from:** the in-app settings page.

### niri_reverse_scroll

Reverses the mouse wheel direction when scrolling the viewport in the scrolling
(niri-style) layout. Has no effect in the other layout modes. See
[LAYOUT_MODES.md](LAYOUT_MODES.md).

**Valid values:**
- `false` - Wheel down scrolls the strip right (default)
- `true` - Inverted

**Default:** `false`

### session_colors

Gives every session a colour of its own and marks it wherever more than one
session is visible at once: the sidebar's sessions section, the sidebar's
agents section (on the rows whose pane lives in another session), and the
session switcher. The panes of the session you are attached to are not
coloured, because you only ever see one session's panes at a time and a colour
there would tell them apart from nothing.

The colour is derived from the session's name, so it is the same in every
attached client and after a daemon restart, and it is unchanged by renaming the
session's label. Six hues from the active theme are available; where two
sessions would ask for the same one, the sidebar settles it so no two visible
sessions share a colour, up to six. Beyond six, colours repeat.

To pin a session to a colour of your choosing, overriding the automatic one,
send the `set-session-accent` control verb (see [protocol.md](protocol.md)):

```json
{"id": 1, "verb": "set-session-accent", "params": {"session": "work", "accent": "cyan"}}
```

The accent takes a colour name from the ANSI sixteen (`red`, `bright blue`,
`magenta`, …) or a hex literal (`#89b4fa`). An empty accent clears it and
returns the session to its automatic colour. A hue an accent has claimed is not
handed out automatically to another session.

The colour rides marks the sidebar already draws, so a terminal that cannot
show colour renders what it always did, apart from one mark on the agent rows
whose pane is in another session. Those rows also name that session in words.

**Valid values:**
- `true` - Sessions carry their colours (default)
- `false` - Renders exactly as it did before the colours existed

**Default:** `true`

### theme

The color theme to use, by ID. Custom themes loaded from
`~/.config/tuios/themes/` can be named here exactly like built-in ones. Leave it
unset to disable theming and use your terminal's own colors. See
[THEMES.md](THEMES.md).

**CLI override:** `--theme <id>`

## Notification Settings

The `[notifications]` section controls how long a message stays in the dock's
right-hand block. All durations are in **seconds**.

```toml
[notifications]
duration = 6           # info and success
warning_duration = 8   # warnings
error_duration = 15    # errors, only used when error_sticky = false
error_sticky = true    # errors wait for esc instead of expiring
```

Every message is dismissible with `esc`, in any mode. `esc` is not consumed:
it dismisses whatever is on the dock and still reaches the shell, copy mode or
whatever overlay is open, so it never costs you the keypress you meant.

### duration

How long an info or success message stays up.

A configured duration is a **floor**, not a cap. A message that a caller
deliberately asked to show for longer still gets the longer time; this value is
the minimum any message of that severity is given.

**Default:** `6`

**Note on short values:** durations under 4 seconds are applied as written but
produce a config warning. A message that disappears before it can be read is a
time limit on reading content with no way to extend it, which fails WCAG 2.2.1
Level A. Four seconds is the shortest the evidence supports (tmux-sensible
overrides tmux's own 750ms to 4s); VS Code purges at 10, 12 and 15 seconds by
severity.

### warning_duration

How long a warning stays up. Warnings get longer than routine messages because
they usually name something you have to decide about.

**Default:** `8`

### error_duration

How long an error stays up, used **only** when `error_sticky = false`.

**Default:** `15`

### error_sticky

When true, errors do not expire at all: they stay until dismissed with `esc`.
The dock's hairline above a sticky error is lit end to end and stops moving,
which is the affordance that it is waiting for you rather than counting down.

Nothing carrying a failure should vanish on a timer the user did not start, so
this is on by default. Set it to `false` if you would rather errors time out
like everything else, in which case `error_duration` applies.

**Valid values:**
- `true` - Errors wait for `esc` (default)
- `false` - Errors expire after `error_duration` seconds

**Default:** `true`

### The `[notifications.agent]` table

What tuios does when a pane's agent state changes. Every key can be turned off,
and `enabled = false` turns off all of them at once.

```toml
[notifications.agent]
enabled = true          # master switch for everything below
notify = true           # in-band notification to the terminal you are attached to
sound = false           # make the alert audible
sound_mode = "audio"    # "audio" plays a cue, "bell" writes a BEL, "both" does each
sound_cooldown_seconds = 3  # shortest gap between two cues, across every pane
dock = true             # the clickable message in tuios's own dock
settle_seconds = 2      # hold an alert this long, drop it if the pane moves on
suppress_focused = true # say nothing about the pane you are already looking at
quiet_hours = ""        # "22:00-08:00" local time; empty means never quiet
command = ""            # shell command to run on an alert

[notifications.agent.sounds]
done = ""               # replaces the built-in "agent stopped" cue
needs_input = ""        # replaces the built-in "agent wants you" cue

[notifications.agent.states]
needs_input = true      # blocked on you
errored = true          # stopped on an error
done = true             # finished its task
idle = false            # went quiet, which the stall timer guesses at
working = false         # started working, which is not news
```

**Which transitions alert.** Only the three that mean the agent has stopped.
`working` and `idle` are silent by default and should usually stay that way:
`idle` is what the silence timer produces from a guess, so an agent that pauses
between tool calls would alert every time.

**How the alert reaches you.** `notify` writes an OSC 9 escape sequence into the
same stream the interface is rendered through, so it arrives at whatever terminal
is in front of you, including over `tuios ssh` and inside tmux (which needs
`set -g allow-passthrough on`). This is deliberately not a desktop notification
raised by tuios itself: with `tuios ssh` the interface runs on the *remote* host,
where such a notification would pop on a machine nobody is sitting at.

`sound` makes the alert audible, and `sound_mode` says how.

- `"audio"` (the default) plays one of two short cues through whatever audio
  player the machine already has: `paplay`, `pw-play`, `aplay`, `ffplay` or
  `mpv` on Linux, `afplay` on macOS, PowerShell's `SoundPlayer` on Windows.
  There are two cues rather than five: one for the agent wanting you
  (`needs_input`, `errored`) and one for the agent having stopped (`done`,
  `idle`). They are meant to be told apart by ear without looking.
- `"bell"` writes a BEL and lets your terminal decide what that means - a
  sound, a flash, a mark in the tab, or nothing. This is what `sound` did
  before the cues existed.
- `"both"` does each, for a setup where either alone might be missed.

The cue is played by the client, which is the process with a human in front of
it. Over `tuios ssh` that means it comes out of your laptop, not the host the
session is running on.

**When there is no audio.** A machine with no player on `PATH`, or no device
behind it - a container, a CI job, an SSH session with no forwarding - goes
quiet. Nothing is printed and nothing is retried: the player list is resolved
once, and a short run of failed plays switches audio off for the life of the
process rather than spawning five doomed players per alert. If you want to be
sure of hearing *something* on such a machine, set `sound_mode = "both"`.

`sound_cooldown_seconds` is the shortest gap between two cues, counted across
every pane, so a workspace where six agents finish together makes one sound
rather than six overlapping ones. Set it to `0` to hear every alert. It does not
apply to the bell.

`[notifications.agent.sounds]` replaces a cue with a file of your own. Any format
your system player reads will do; WAV is the safest, since `aplay` and `paplay`
decode nothing else. A path that does not exist falls back to the built-in cue,
so a typo costs you the custom sound rather than all sound.

Setting `TUIOS_NO_SOUND` in the environment silences the cues however tuios is
configured, which is the right lever for a recording or a shared machine.

`dock` is the in-app message. It is the only one you can click: clicking it (or
pressing the notification-jump binding) focuses the pane that raised it,
switching workspace and session on the way.

**Anti-flicker.** `settle_seconds` holds an alert and drops it if the pane leaves
the state before the wait is up, so an agent that flips into `needs_input` and
straight back out says nothing at all, and one that stays says it once. Set it to
`0` to alert immediately.

**quiet_hours** silences every sink inside a local-time window written
`HH:MM-HH:MM`. A window that wraps midnight (`"22:00-08:00"`) is understood. An
unparseable value is reported as a config warning and ignored.

**command** is shorthand for registering a command under the `after-agent-state`
hook, which is where the full contract is documented; see
[HOOKS.md](HOOKS.md). It is how you wire a notifier tuios does not ship - ntfy,
Pushover, a webhook, or `notify-send` on a machine that actually has a display:

```toml
[notifications.agent]
command = 'curl -s -d "$TUIOS_WINDOW_NAME is $TUIOS_AGENT_STATE" ntfy.sh/my-topic'
```

**When nothing fires.** Alerts are raised by an attached client, so a session
with nobody attached raises none - there is no terminal to write to and no dock
to draw on. With two clients attached, each raises its own, in its own terminal.
A pane that is already in a state when a client first sees it is not a
transition and says nothing, so reattaching does not replay every agent's state
at you.

## Startup Settings

The `[startup]` section controls what a session looks like the moment it starts.
All options default to `false`, so by default a session comes up empty and
floating, in window-management mode, and you open the first window yourself.

```toml
[startup]
open_default_window = false
tiled = false
start_in_terminal_mode = false
```

### open_default_window

Opens one terminal window automatically when a session starts with none, so you
land in a shell instead of an empty screen. It only acts on an empty session:
attaching to a session that already has windows leaves them untouched.

**Valid values:**
- `false` - Start empty; press `n` (or the leader key then `c`) to open the first window (default)
- `true` - Open one terminal window automatically on start

**Default:** `false`

**Also settable from:** the in-app settings page (`Ctrl+B` `,`, under Startup),
which persists the change back to the config file. The change applies on the
next launch.

### tiled

Starts a new session with tiling enabled instead of floating. Windows are laid
out with the default BSP tiling layout, and windows opened afterwards tile
automatically. Like `open_default_window`, this only seeds a fresh session;
attaching to an existing session restores that session's own layout.

Combine it with `open_default_window` to launch straight into a tiled session
with one terminal already open.

**Valid values:**
- `false` - Start in floating mode (default)
- `true` - Start with tiling on (BSP layout)

**Default:** `false`

**Also settable from:** the in-app settings page (`Ctrl+B` `,`, under Startup).
The change applies on the next launch.

### start_in_terminal_mode

Starts focused in terminal mode instead of window-management mode, so keystrokes
go straight to the focused terminal and you can start typing in the shell
immediately rather than having your keys interpreted as window-manager commands.

Terminal mode needs a window to type into, so this only takes effect when a
window is present and focused at startup. On its own it does nothing on an empty
session; pair it with `open_default_window` so there is a shell to land in. If no
window is present, the session stays in window-management mode.

**Valid values:**
- `false` - Start in window-management mode (default)
- `true` - Start in terminal mode when a window is present

**Default:** `false`

**Also settable from:** the in-app settings page (`Ctrl+B` `,`, under Startup).
The change applies on the next launch.

### Combining the startup options

The three options are designed to stack. The intended full combination is:

```toml
[startup]
open_default_window = true
tiled = true
start_in_terminal_mode = true
```

which launches straight into a tiled session with one terminal already open and
the cursor in the shell, ready to type. `start_in_terminal_mode` depends on a
focused window, so it is only meaningful alongside `open_default_window` (or an
attach that restores a window); enabling it alone leaves an empty session in
window-management mode.

## Hooks

The `[hooks]` table runs shell commands on session events: windows created,
closed or focused, workspace switches, layout changes, resizes, attach and
detach:

```toml
[hooks]
after-new-window = "notify-send 'TUIOS' 'new window'"
```

See [HOOKS.md](HOOKS.md) for the event list, the environment variables passed to
each command, and the execution model.

## Project Tapes

The `[tape]` table controls per-directory project tapes (`.tuios.tape`). When the
focused shell enters a directory that carries one, TUIOS can build a project
session from it - after you review and trust the content.

```toml
[tape]
autorun = "ask"        # off | ask | auto (default: ask)
auto_review = false    # auto-open the review dialog on detection (default: false)
```

- `off` - no scanning, no indicators, feature invisible.
- `ask` (default) - a detected tape surfaces a passive banner and a `tape` dock
  badge; nothing runs until you open the review dialog (`Ctrl+B` `T` `t`) and
  choose Run.
- `auto` - a trusted, unedited tape runs automatically on entry; an untrusted or
  changed tape falls back to `ask` and never auto-runs.
- `auto_review` - when `true`, entering a directory with a reviewable tape opens
  the review dialog automatically instead of only showing the passive banner. It
  never runs anything on its own (you still choose Run/Trust/Never/Not now), never
  auto-opens for a denied or ineligible tape, and pops at most once per directory
  per session. Configurable from the settings menu (`Ctrl+B` `,` -> Tape).

`TUIOS_TAPE_AUTORUN` overrides this for a single run. An untrusted tape is inert:
it is never parsed as a program or executed until you review its content and
choose to run or trust it. See [PROJECT_TAPES.md](PROJECT_TAPES.md) for the trust
model, the review dialog, the tape header (`Session`, `Scope`, `Workspace`,
`Require`), and session scope.

## Keybindings Prefix Configuration

### leader_key

Controls the prefix key for window management commands (the tmux-style leader key).

**Valid values:** Any valid key combination (see [Key Syntax](#key-syntax) section)

**Default:** `"ctrl+b"`

**Examples:**
```toml
[keybindings]
# Use Ctrl+A instead of Ctrl+B (like GNU Screen)
leader_key = "ctrl+a"

# Use Alt+Space
leader_key = "alt+space"

# Use Ctrl+Space
leader_key = "ctrl+space"
```

**Note:** When using a custom leader key, you'll need to press it twice to send the literal key to the terminal (e.g., press `ctrl+a` twice to send `ctrl+a` to the terminal if `ctrl+a` is your leader key).

**Affected keybindings:**
This changes the prefix key for all prefix-based commands:
- Window management: `leader + c` (new window), `leader + x` (close), etc.
- Workspaces: `leader + w` submenu
- Tiling: `leader + t` submenu
- Minimize: `leader + m` submenu
- Debug: `leader + D` submenu
- Copy mode: `leader + [`

**CLI override:** Currently no CLI override exists; must be set in config file.

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

In `[keybindings.terminal_mode]` an unbound key is handed back to the shell in the pane. This matters most for `alt+left` and `alt+right`, which TUIOS binds to directional pane focus and which readline, fish and zsh use for word-wise cursor movement:

```toml
[keybindings.terminal_mode]
terminal_focus_left = []
terminal_focus_right = []
```

See [Terminal Mode Keys](KEYBINDINGS.md#terminal-mode-keys).

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
tuios keybinds list-custom
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
tuios config path
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
tuios keybinds list | grep <your-key>
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
- [Hooks](HOOKS.md) - Run shell commands on session events
- [Keybindings Reference](KEYBINDINGS.md) - Default keybindings
- [Hooks](HOOKS.md) - Shell commands run on window events
- [Themes](THEMES.md) - Built-in and custom themes
- [Layout Modes](LAYOUT_MODES.md) - BSP, master-stack and scrolling layouts
- [Sessions](SESSIONS.md) - Local and daemon sessions, persistence
- [README](../README.md) - Project overview
