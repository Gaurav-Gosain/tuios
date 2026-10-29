# Hooks

The docs site has the same reference: https://tuios.dev/docs/hooks

Thirteen events, each running a shell command with `TUIOS_*` environment variables carrying the facts. This page lists every event and every variable. Hooks are read once at startup from the `[hooks]` table.

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
| `after-new-worktree` | daemon | yes |
| `after-settle` | daemon | yes |
| `after-attach` | client | no |
| `after-detach` | client | no |
| `after-resize` | client | no |
| `after-layout-change` | client | no |
| `after-theme-change` | local client | no |

The window set, the focused window, the current workspace and a pane's agent state belong to the session, so the daemon runs those commands. They fire on a detached session, and they fire once however many clients are attached.

A client's terminal size, its attach, its detach and its theme belong to that one client, and the layout is computed by the attached renderer. Those stay in the client. Three clients attaching is three attaches.

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
| `TUIOS_THEME`, `TUIOS_THEME_BG`, `TUIOS_THEME_LIGHT` | For `after-theme-change`, below |
| `TUIOS_WORKTREE_PATH`, `TUIOS_WORKTREE_BRANCH`, `TUIOS_WORKTREE_REPO_ROOT`, `TUIOS_WORKTREE_CREATED_BRANCH` | For `after-new-worktree`, below. `after-settle` gets the first three for an agent in a worktree session |
| `TUIOS_SETTLE_REASON` | For `after-settle`: what settled the agent, below |

`after-agent-state` fires only for the transitions `[notifications.agent]`
alerts on (`needs_input`, `errored` and `done` by default), after its
`settle_seconds`, outside its `quiet_hours`, and not for the pane an attached
client is showing when `suppress_focused` is on. A terminal that reports focus
events tells tuios when its window loses focus, and while every attached
client's terminal is out of focus nobody counts as looking, so the hook fires
for the shown pane too. `tuios session-info` shows this as `host focus`. Because the daemon runs it, it
fires with nobody attached, which is how to reach a phone: `tuios --skill
recipes` has a working ntfy hook. The `[notify]` table does the same without a
script, for Inbox items, and links to the item on tuios-web. See
[CONFIGURATION.md](CONFIGURATION.md#push-notifications-to-your-phone). `TUIOS_AGENT_MESSAGE` is the agent's own text,
so think before a hook sends it off the machine.

`after-command-finished` fires when a pane's shell reports, through its OSC 133
prompt marks, that a command finished. A shell without that integration never
fires it. It gets `TUIOS_COMMAND` (the command line, cut to 512 bytes with
likely secrets masked), `TUIOS_EXIT_CODE` (empty when the shell sent no status)
and `TUIOS_DURATION_MS`. Every other event gets those three empty. A tuios with
no daemon does not fire it, since only the daemon follows the marks.

`after-theme-change` fires when other theme colours come into force in the
client. This occurs when you pick a theme, when the Omarchy theme changes, when
tuios follows `theme_light`, and with no theme when the terminal's background
changes. It fires once for each change:

- While the theme picker or the theme editor is open, it does not fire. When
  the picker closes on a theme, it fires once. When you cancel the picker, it
  does not fire.
- It does not fire when tuios starts.

It gets these variables:

- `TUIOS_THEME`: the theme id, or `none` when no theme is in force.
- `TUIOS_THEME_BG`: the background the panes get, as `#rrggbb`. It is empty
  when tuios does not know the background.
- `TUIOS_THEME_LIGHT`: `1` for a light background, `0` for a dark background.
  It uses the same rule as the mode 2031 notice that the panes get.

Put `"$TUIOS_THEME"` in double quotes in a hook. A theme file can set an id
with spaces or other characters that the shell reads.

Only a local client runs this hook. A client of `tuios ssh` or `tuios-web`
does not run it. When two local clients are attached, each client runs it for
its own theme.

Use it for a tool that does not follow the theme by itself:

```toml
[hooks]
after-theme-change = "~/.config/tuios/hooks/theme.sh"
```

`after-new-worktree` fires when tuios makes a worktree session. This occurs
for `tuios worktree new`, for "New worktree..." on the rail and for each
session of `tuios fan`. It runs after `git worktree add` and before the
session's first window starts. tuios waits for it, so the shell or the agent
in that window finds what the hook put into the tree. Other clients and
sessions go on while it runs.

It starts in the worktree's directory and gets these variables:

- `TUIOS_SESSION_ID`: the new session's name.
- `TUIOS_WORKTREE_PATH`: the worktree's directory.
- `TUIOS_WORKTREE_BRANCH`: the branch the worktree has checked out.
- `TUIOS_WORKTREE_REPO_ROOT`: the main checkout the worktree was made from.
- `TUIOS_WORKTREE_CREATED_BRANCH`: `1` when the branch is new, `0` when it
  existed.

Use it to copy the ignored files a checkout needs, such as `.env`:

```toml
[hooks]
after-new-worktree = 'cp "$TUIOS_WORKTREE_REPO_ROOT/.env" .'
```

Each command has 60 seconds. tuios stops a command that runs longer, and the
programs it started. With a list of commands, they run in order, and the first
one that fails stops the rest. When a command fails or runs out of time, tuios
keeps the worktree and the session and opens the first window. The rail, `tuios
worktree new` and `tuios fan` show the exit code and the last lines of the
command's stderr. The verb result has them in `hook_error`.

`after-settle` fires when an agent is settled: put away on the Settled shelf
of the agents view, with `D` or with `mark-attention` `settle`. It fires once
for each settle, also with nobody attached. It does not fire when the agent
comes back, or for an agent that moves to Settled after an hour at rest.
tuios does not stop the agent or close its pane. Do that in the hook if you
want it.

The command starts in the session's worktree when tuios made one, else in the
folder the pane started in. It gets these variables:

- `TUIOS_SESSION_ID`, `TUIOS_WINDOW_ID`, `TUIOS_WINDOW_NAME` and
  `TUIOS_WORKSPACE`: the agent's pane.
- `TUIOS_SETTLE_REASON`: `person` for a settle you made.
- `TUIOS_WORKTREE_PATH`, `TUIOS_WORKTREE_BRANCH` and
  `TUIOS_WORKTREE_REPO_ROOT`: the worktree, for an agent in a worktree
  session. Empty otherwise.

Use it to free the disk space of an agent you are done with:

```toml
[hooks]
after-settle = '[ -n "$TUIOS_WORKTREE_PATH" ] && cargo clean'
```

The daemon reads the `[hooks]` table when it starts. Restart the daemon with `tuios restart-server` after you change a hook the daemon runs. The daemon does not reload hooks when the file changes.

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
