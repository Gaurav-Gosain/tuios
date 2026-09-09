# Hooks

The hooks reference lives on the docs site: https://tuios.gaurav.zip/docs/hooks

Nine events, each running a shell command with `TUIOS_*` environment variables carrying the facts; the site page lists every event and every variable. Hooks are read once at startup from the `[hooks]` table; see the [configuration reference](https://tuios.gaurav.zip/docs/configuration) for the table itself.

## Which side runs a hook

A hook runs on the side that owns the fact it reports.

| event | side | fires with nobody attached |
|---|---|---|
| `after-new-window` | daemon | yes |
| `after-close-window` | daemon | yes |
| `after-focus-change` | daemon | yes |
| `after-workspace-switch` | daemon | yes |
| `after-agent-state` | daemon | yes |
| `after-attach` | client | no |
| `after-detach` | client | no |
| `after-resize` | client | no |
| `after-layout-change` | client | no |

The window set, the focused window, the current workspace and a pane's agent state belong to the session, so the daemon runs those commands. They fire on a detached session, and they fire once however many clients are attached.

A client's terminal size, its attach and its detach belong to that one client, and the layout is computed by the attached renderer. Those stay in the client. Three clients attaching is three attaches.

A tuios with no daemon runs every hook itself.

The daemon reads the `[hooks]` table when it starts. Restart the daemon with `tuios kill-server` after you change a hook the daemon runs.

## When a hook does not fire

```sh
tuios list-hooks
```

Every registered command, with how many times it ran, its last exit code, when it last ran and its last error.

- No row at all means the hook was never loaded. Check the event name against the table above.
- `RUNS` of 0 means the command is fine and the event never happened.
- A non-zero exit means the command ran and failed. The error says why.

The daemon also logs a warning for every failing hook, with the exit code and the last 1 KiB of stderr. Run `tuios daemon --log-level=basic` to see one line per firing as well.
