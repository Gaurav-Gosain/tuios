# Hooks

TUIOS can run a shell command when something happens in a session: a window
opens, focus moves, the workspace changes, a client attaches. Hooks are
configured in the TOML config file and run asynchronously, so a slow hook does
not stall the interface.

## Table of Contents

- [Configuration](#configuration)
- [Events](#events)
- [Environment Variables](#environment-variables)
- [Examples](#examples)
- [Behavior and Limits](#behavior-and-limits)

## Configuration

Hooks live under a `[hooks]` table in the TUIOS config file (run
`tuios --config-path` to find it). Each key is an event name and each value is
either a single command or a list of commands:

```toml
[hooks]
after-new-window = "notify-send 'TUIOS' \"opened $TUIOS_WINDOW_NAME\""
after-attach = [
  "logger tuios attached to $TUIOS_SESSION_ID",
  "~/.config/tuios/on-attach.sh",
]
```

Commands run through `sh -c`, so pipes, redirection and shell variable
expansion all work. An unknown event name is ignored with a log line naming the
valid events; it is not a fatal config error.

## Events

All eight events fire. Each one lists the fields of the payload that are
meaningful for it; the rest are present but zero.

| Event | Fires when | Payload beyond the common fields |
| --- | --- | --- |
| `after-new-window` | A window has been created, from a keybinding, the command palette, a tape script, or another client | `TUIOS_WINDOW_ID`, `TUIOS_WINDOW_NAME` |
| `after-close-window` | A window has been closed | `TUIOS_WINDOW_ID`, `TUIOS_WINDOW_NAME` |
| `after-focus-change` | Focus has moved to a different window | `TUIOS_WINDOW_ID`, `TUIOS_WINDOW_NAME` |
| `after-workspace-switch` | The visible workspace has changed | `TUIOS_WORKSPACE`, `TUIOS_PREV_WORKSPACE` |
| `after-attach` | This client has attached to a session and restored it, including when switching to a different session | `TUIOS_SESSION_ID` |
| `after-detach` | This client is leaving a session that keeps running | `TUIOS_SESSION_ID` |
| `after-layout-change` | The layout has changed, including tiling being turned on or off | `TUIOS_LAYOUT` |
| `after-resize` | A window has settled at a new size | `TUIOS_WINDOW_ID`, `TUIOS_WIDTH`, `TUIOS_HEIGHT` |

Notes on when these do and do not fire:

- `after-workspace-switch` does not fire when the requested workspace is
  already the visible one.
- `after-resize` fires once per completed resize. A mouse drag produces one
  event on release carrying the final size, not one per mouse-motion event. A
  keyboard resize produces one event per keypress, since each press is a
  finished resize.
- `after-detach` fires when a client detaches from a session that outlives it.
  Quitting kills the session, which is not a detach, so quitting does not fire
  it.
- `after-layout-change` reports the layout that is now in force, not the one
  being left.

## Environment Variables

Every hook command receives the full parent environment plus:

| Variable | Meaning |
| --- | --- |
| `TUIOS_EVENT` | The event name, for example `after-new-window`. Lets one script serve several events |
| `TUIOS_SESSION_ID` | Name of the session the event came from |
| `TUIOS_WORKSPACE` | Workspace the event applies to |
| `TUIOS_WINDOW_ID` | Stable ID of the window, empty for events with no window |
| `TUIOS_WINDOW_NAME` | Window title, empty for events with no window |
| `TUIOS_PREV_WORKSPACE` | Workspace active before an `after-workspace-switch`, `0` otherwise |
| `TUIOS_LAYOUT` | Layout after an `after-layout-change`: `bsp`, `master-stack`, `scrolling` or `floating`. Empty otherwise |
| `TUIOS_WIDTH`, `TUIOS_HEIGHT` | Window size in cells after an `after-resize`, `0` otherwise |

## Examples

Track the focused window for an external status bar:

```toml
[hooks]
after-focus-change = "echo $TUIOS_WINDOW_NAME > /tmp/tuios-focus"
```

Different behavior per workspace:

```toml
[hooks]
after-workspace-switch = "~/.config/tuios/workspace.sh"
```

```bash
#!/bin/sh
# ~/.config/tuios/workspace.sh
case "$TUIOS_WORKSPACE" in
  1) light-theme ;;
  *) dark-theme ;;
esac
logger "tuios: workspace $TUIOS_PREV_WORKSPACE -> $TUIOS_WORKSPACE"
```

One script handling several events, dispatching on `TUIOS_EVENT`:

```toml
[hooks]
after-attach = "~/.config/tuios/session.sh"
after-detach = "~/.config/tuios/session.sh"
```

```bash
#!/bin/sh
# ~/.config/tuios/session.sh
case "$TUIOS_EVENT" in
  after-attach) systemctl --user start my-dev-services ;;
  after-detach) systemctl --user stop my-dev-services ;;
esac
```

## Behavior and Limits

- Hooks run asynchronously, each in its own process. TUIOS does not wait for
  them and does not read their output, so a slow or hanging hook cannot freeze
  the interface.
- The one exception is `after-detach`, which the client waits on for up to two
  seconds before exiting. Without that wait the hook process would be discarded
  unrun, since the client exits immediately after firing it. A hook that takes
  longer is abandoned rather than allowed to hold the client open.
- Output and exit status are discarded. A hook that needs to report something
  should write to a file, a logger, or a notification daemon.
- Hooks run on the client, not the daemon. In a multi-client session each
  attached client fires its own hooks for the events it observes.
- Hooks are read at startup from the config file.
