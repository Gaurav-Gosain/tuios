# Agent state

tuios tracks a semantic state for each window's pane so a session can show which
panes need attention. A pane running a coding agent reports what it is doing;
tuios stores that state per window, syncs it to every attached client, and draws
an indicator for it.

## Table of Contents

- [States](#states)
- [Reporting state](#reporting-state)
- [Sources and precedence](#sources-and-precedence)
- [The stall heuristic](#the-stall-heuristic)
- [Indicator](#indicator)
- [Claude Code integration](#claude-code-integration)
- [Environment](#environment)

## States

| State         | Meaning                                          |
| ------------- | ------------------------------------------------ |
| `none`        | Not running an agent, or not reporting (default) |
| `working`     | Actively working on a task                       |
| `needs_input` | Blocked waiting for the user                     |
| `idle`        | Not working and not blocked                      |
| `done`        | Finished its task                                |
| `errored`     | Stopped because of an error                      |

State is daemon-owned per-window state. It rides the same versioned state sync
every other window property uses, so it survives detach/reattach and reaches all
clients. `none` is the zero value and is never persisted, so older sessions and
older clients simply read every pane as `none`.

## Reporting state

A pane reports its own state through the `set-agent-state` verb, the same way it
would call `send-keys` or `capture-pane`:

```sh
# From inside a pane
tuios set-agent-state working
tuios set-agent-state needs_input -m "awaiting approval"
tuios set-agent-state done
tuios set-agent-state none          # clear it
```

Read it back with `get-agent-state`:

```sh
tuios get-agent-state               # prints the state name
tuios get-agent-state -w build --json
```

Both are ordinary control-protocol verbs, so they appear in `tuios list-verbs`
and can be called over the daemon socket directly. `list-windows --json` also
reports each window's `agent_state`, so a cross-session view can read every
pane's state in one call.

Targeting follows the same rules as the other window verbs: `-s`/`--session`
selects the session (default: most recently active), `-w`/`--window` selects the
window by id or name (default: the focused window).

## Sources and precedence

More than one thing can have an opinion about a pane. `set-agent-state` takes an
optional `source` saying where the state came from, and the daemon uses it to
decide which opinion wins:

| Source   | Meaning                                          |
| -------- | ------------------------------------------------ |
| `report` | The agent reporting for itself (default)         |
| `osc`    | An escape sequence the pane emitted              |
| `screen` | A rule matched against the pane's rendered text  |
| `stall`  | The silence timer                                |

A source may write over a claim ranked at or below its own and never over one
ranked above it, so a screen rule cannot overwrite what an agent reported for
itself. A source updating its own claim is always allowed. A report that loses
comes back with `"applied": false` and the state that stands, rather than an
error.

Omitting `source` means `report`, so a caller that never sets it behaves exactly
as it always has. `get-agent-state` reports the winning `source` and, when one
was named, the `harness_id`, so a surprising indicator can be traced to the thing
that set it.

## The stall heuristic

Agents that do not report get a conservative fallback. If a pane reported
`working` but then produces no output for a while, the daemon demotes it to
`idle`, on the assumption that a genuinely busy agent produces output. The
fallback is strictly secondary to explicit reporting:

- It only ever moves a pane out of `working`, and only ever into `idle`. Any
  other state (`needs_input`, `done`, `errored`) is never touched, so an explicit
  report is never overridden.
- The silence clock is the later of the pane's last output and the time its
  `working` state was set, so a working report is given the full window before it
  can be demoted, and output keeps a pane looking busy.
- It never promotes a pane into `working`; only an explicit report does that.

The silence window defaults to 30 seconds. Override it with the
`TUIOS_AGENT_STALL_SECONDS` environment variable when starting the daemon; set it
to `0` (or a negative value) to disable the heuristic entirely.

## Indicator

tuios draws a one-cell glyph in each window's title:

| State         | Indicator |
| ------------- | --------- |
| `working`     | `●`       |
| `needs_input` | `▲`       |
| `idle`        | `○`       |
| `done`        | `■`       |
| `errored`     | `×`       |
| `none`        | (nothing) |

The glyphs are distinct shapes rather than the same shape in different colors, so
the state reads at a glance and survives a monochrome capture. The indicator
shows even for a window with no name.

## Claude Code integration

A reference shim maps Claude Code's lifecycle hooks to these states. See
[integrations/claude-code](../integrations/claude-code/README.md) for the script
and how to wire it.

## Environment

When tuios spawns a pane it exports the environment a state-reporting shim needs:

| Variable          | Meaning                                          |
| ----------------- | ------------------------------------------------ |
| `TUIOS_ENV`       | `1` when running under tuios                      |
| `TUIOS_SOCKET`    | Daemon socket path                               |
| `TUIOS_PANE_ID`   | The pane's window id                             |
| `TUIOS_WINDOW_ID` | The pane's window id (alias of `TUIOS_PANE_ID`)  |
| `TUIOS_SESSION`   | The session name                                 |

A shim guards on these and no-ops when they are unset, so it is safe to leave
wired up outside tuios.
