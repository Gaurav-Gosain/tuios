# Hooks

Hooks run a shell command when something happens in TUIOS. They are configured
in `config.toml`, they run asynchronously, and they receive context about the
event through environment variables.

## Table of Contents

- [Configuration](#configuration)
- [Events](#events)
- [Environment Variables](#environment-variables)
- [Examples](#examples)
- [Execution Model](#execution-model)
- [Limitations](#limitations)

## Configuration

Hooks live in a `[hooks]` table in your config file (`~/.config/tuios/config.toml`,
see [CONFIGURATION.md](CONFIGURATION.md) for the exact path). Each key is an
event name and each value is either a single command string or an array of
command strings:

```toml
[hooks]
after-new-window = "notify-send 'TUIOS' 'new window'"
after-close-window = [
  "logger tuios: window $TUIOS_WINDOW_NAME closed",
  "~/bin/tuios-window-closed.sh",
]
```

Unknown event names are ignored with a warning in the log, and empty command
strings are dropped. Hooks are read once, when TUIOS starts.

## Events

Three events are wired up and fire:

| Event | Fires when |
|---|---|
| `after-new-window` | A window has been created |
| `after-close-window` | A window has been closed |
| `after-focus-change` | Focus moves to a different window |

Five more event names are accepted by the config parser but are not yet emitted
by anything, so a hook registered for them will never run:
`after-workspace-switch`, `after-attach`, `after-detach`,
`after-layout-change`, `after-resize`. They are listed here so that a hook that
silently does nothing is explainable rather than mysterious.

## Environment Variables

Every hook command is run with the parent environment plus:

| Variable | Value |
|---|---|
| `TUIOS_EVENT` | The event name, for example `after-new-window` |
| `TUIOS_WINDOW_ID` | The UUID of the window the event is about |
| `TUIOS_WINDOW_NAME` | The window's terminal title, as set by the shell or program running in it. This is not the custom name set with `Ctrl+B` `r`, and it is empty for a window whose program has not set a title yet |
| `TUIOS_WORKSPACE` | The current workspace index, zero-based |
| `TUIOS_SESSION_ID` | Always empty at present; the firing path does not populate it |

Note that `TUIOS_WORKSPACE` is the workspace that was current when the event
fired, not necessarily the workspace of the window named by `TUIOS_WINDOW_ID`.

## Examples

Log every window lifecycle event to a file:

```toml
[hooks]
after-new-window = "echo \"$TUIOS_EVENT $TUIOS_WINDOW_NAME ws=$TUIOS_WORKSPACE\" >> ~/.tuios-events.log"
after-close-window = "echo \"$TUIOS_EVENT $TUIOS_WINDOW_NAME\" >> ~/.tuios-events.log"
```

Update an external status bar when focus moves:

```toml
[hooks]
after-focus-change = "~/bin/statusbar-set-title \"$TUIOS_WINDOW_NAME\""
```

## Execution Model

Each command is run as `sh -c "<command>"` in its own goroutine. That means:

- Shell syntax works: pipes, redirection, `&&`, variable expansion.
- Hooks do not block rendering or input. TUIOS never waits for one to finish.
- Several hooks registered for the same event all start at once, in no
  guaranteed order.
- Standard output and standard error are discarded, and the exit status is
  ignored. A hook that fails fails silently.

## Limitations

- **No output, no status.** Because output is discarded, a hook that misbehaves
  gives you nothing to look at. Redirect to a file yourself while developing
  one.
- **No sequencing or deduplication.** `after-focus-change` fires on every focus
  change, which is frequent; a slow command there will spawn processes faster
  than they finish.
- **Fire and forget.** A hook cannot cancel or modify the event that triggered
  it, and there are no `before-*` events.
- **`sh -c` on every platform.** The command is handed to `sh`, so hooks assume a
  POSIX shell is on `PATH`.
- **Loaded at startup only.** Editing `[hooks]` requires a restart to take
  effect.

## Related Documentation

- [CONFIGURATION.md](CONFIGURATION.md) - the config file and every other option
- [SESSIONS.md](SESSIONS.md) - the session model
- [protocol.md](protocol.md) - controlling a daemon session from outside
