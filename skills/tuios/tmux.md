# The tmux shim

There is no tmux in a tuios pane, so a tool that opens its workers in tmux panes,
such as Claude Code agent teams, cannot. Run it under the shim and its `tmux`
calls answer in this session instead:

```sh
tuios tmux-shim -- env CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 claude
```

Each teammate then opens as a tuios pane on your workspace, named after it, with
its agent state on the rail and in the Inbox. The shim is opt-in: only what you
start under `tuios tmux-shim` sees it. With no command it starts your shell,
and every `tmux` call from that shell goes to the shim.

## What it answers

A tmux window is a workspace (`@N`) and a pane is a tuios window (`%N`). It
answers `split-window`, `new-window`, `send-keys`, `capture-pane -p`,
`display-message -p`, `list-panes`, `list-windows`, `list-sessions`,
`has-session`, `kill-pane`, `kill-window`, `select-pane`, `select-window`,
`rename-window` and `respawn-pane -k`. Layout and style commands succeed and do
nothing, since tuios owns the layout. Commands that start, attach or end a
session are refused. Anything else fails rather than pretending.

Ask it one question directly, from any tuios pane:

```sh
tuios tmux display-message -p '#{pane_id} #{window_id} #{tuios_window_id}'
tuios tmux list-panes -F '#{pane_id} #{pane_title}'
```

A call whose `TMUX` or `-S` names a real tmux server still goes to the real tmux
on `PATH`.

## What it can reach

It never reaches another session: every target resolves inside the caller's
own. It is held to your pane's grants: opening, closing, focusing and naming
panes, showing or naming a workspace, and respawning any pane but your own
need `admin` (`tuios pane-grants` shows what you hold). It is not a
sandbox; for an agent held to its own session, use `tuios mcp` or give its pane
fewer grants.

## When a tool does not work under it

Calls the shim could not fully answer are recorded, as JSON lines, in
`$XDG_STATE_HOME/tuios/tmux-shim.log` (the platform's state directory when that
is unset), or the file `--log` names. `--log-all` records every call. The log
never records what was typed.

```sh
tuios tmux-shim --log-all --log /tmp/shim.log -- mytool
tail -5 /tmp/shim.log
```

Prefer the tuios verbs for your own work; the shim is for tools that only know
tmux.
