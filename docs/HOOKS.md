# Hooks

The docs site has the same reference: https://tuios.gaurav.zip/docs/hooks

Ten events, each running a shell command with `TUIOS_*` environment variables carrying the facts. This page lists every event and every variable. Hooks are read once at startup from the `[hooks]` table.

## Which side runs a hook

A hook runs on the side that owns the fact it reports.

| event | side | fires with nobody attached |
|---|---|---|
| `after-new-window` | daemon | yes |
| `after-close-window` | daemon | yes |
| `after-focus-change` | daemon | yes |
| `after-workspace-switch` | daemon | yes |
| `after-agent-state` | daemon | yes |
| `after-command-finished` | daemon | yes |
| `after-attach` | client | no |
| `after-detach` | client | no |
| `after-resize` | client | no |
| `after-layout-change` | client | no |

The window set, the focused window, the current workspace and a pane's agent state belong to the session, so the daemon runs those commands. They fire on a detached session, and they fire once however many clients are attached.

A client's terminal size, its attach and its detach belong to that one client, and the layout is computed by the attached renderer. Those stay in the client. Three clients attaching is three attaches.

A tuios with no daemon runs every hook itself.

## The table and what a hook gets

Each event takes one command or a list of them, run with `sh -c`:

```toml
[hooks]
after-new-window  = "notify-send 'tuios' \"new window $TUIOS_WINDOW_NAME\""
after-agent-state = ["sh ~/.config/tuios/hooks/phone.sh"]
```

Every command gets these variables. A variable that does not describe the event
is empty, or 0 for a number, so a script can read all of them:

| Variable | What it holds |
|---|---|
| `TUIOS_EVENT` | The event name |
| `TUIOS_SESSION_ID` | The session's name |
| `TUIOS_WINDOW_ID`, `TUIOS_WINDOW_NAME` | The window the event is about |
| `TUIOS_WORKSPACE`, `TUIOS_PREV_WORKSPACE` | The workspace, and the one before an `after-workspace-switch` |
| `TUIOS_LAYOUT` | The layout after an `after-layout-change` |
| `TUIOS_WIDTH`, `TUIOS_HEIGHT` | The size after an `after-resize` |
| `TUIOS_AGENT_STATE`, `TUIOS_AGENT_PREV_STATE` | For `after-agent-state`: the state the pane moved to and from |
| `TUIOS_AGENT_HARNESS`, `TUIOS_AGENT_MESSAGE` | For `after-agent-state`: the harness and the message that came with the state |
| `TUIOS_COMMAND`, `TUIOS_EXIT_CODE`, `TUIOS_DURATION_MS` | For `after-command-finished`, below |

`after-agent-state` fires only for the transitions `[notifications.agent]`
alerts on (`needs_input`, `errored` and `done` by default), after its
`settle_seconds`, outside its `quiet_hours`, and not for the pane an attached
client is showing when `suppress_focused` is on. Because the daemon runs it, it
fires with nobody attached, which is how to reach a phone: `tuios --skill
recipes` has a working ntfy hook. `TUIOS_AGENT_MESSAGE` is the agent's own text,
so think before a hook sends it off the machine.

`after-command-finished` fires when a pane's shell reports, through its OSC 133
prompt marks, that a command finished. A shell without that integration never
fires it. It gets `TUIOS_COMMAND` (the command line, cut to 512 bytes with
likely secrets masked), `TUIOS_EXIT_CODE` (empty when the shell sent no status)
and `TUIOS_DURATION_MS`. Every other event gets those three empty. A tuios with
no daemon does not fire it, since only the daemon follows the marks.

The daemon reads the `[hooks]` table when it starts. Restart the daemon with `tuios kill-server` after you change a hook the daemon runs. The daemon does not reload hooks when the file changes.

A hook the daemon runs gets the daemon's environment, not your shell's. A daemon started by `tuios new` keeps the environment of that first shell. Put full paths in the command when in doubt.

## When a hook does not fire

```sh
tuios list-hooks
```

Every registered command, with how many times it ran, its last exit code, when it last ran and its last error.

- No row at all means the hook was never loaded. Check the event name against the table above.
- `RUNS` of 0 means the command is fine and the event never happened.
- A non-zero exit means the command ran and failed. The error says why.

The daemon also logs a warning for every failing hook, with the exit code and the last 1 KiB of stderr. Run `tuios daemon --log-level=basic` to see one line per firing as well.
