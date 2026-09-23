# Pane grants: what a pane may do

Every pane holds grants that say what a process in it may do through tuios. The
daemon checks every call from a pane against them before anything runs: the
CLI, a script on the socket, `tuios mcp` and the tmux shim alike. The person's
own shell and client, outside every pane, are held to nothing new.

```sh
tuios pane-grants
tuios pane-grants --json | jq -r '.grants | join(",")'
```

| Grant | What it lets you do |
| --- | --- |
| `read` | Read your own session and your fan group: list, `get-window`, capture, agent state, waits, the event stream, mail |
| `write` | Type into the panes of your own session that hold nothing you do not, and leave mail and stashed files there |
| `fan` | Write in your fan group and the sessions you launched, and start agents with `fan` and `start-agent` |
| `respond` | Answer another pane's prompt with `respond`, for the person, and type into a pane waiting on a prompt |
| `admin` | Everything else: other sessions, listings across sessions, windows, layouts, options, `kill-session`, `run-command`, attach. Includes `read`, `write` and `fan`, never `respond` |

Whatever you hold, you can report about your own pane (`set-agent-state`,
`set-agent-meta`, `set-agent-session`, `ask-human`, `request-approval`) and ask
what you hold. So `tuios agent-hook` works in every pane.

`TUIOS_PANE_GRANTS` is what the pane held when its process started;
`tuios pane-grants` is what it holds now.

## Typing into another pane

What you type into a pane runs with that pane's grants. So without `admin`,
typing into any pane but your own (`send-text`, `send-keys`, `run`,
`ask-agent`) is refused when the target holds a grant you do not, and when the
target is on `needs_input` unless you hold `respond`, because keys typed there
answer its prompt. Your keys always go to the target's terminal, never to the
window manager.

## A refusal

A call your grants do not cover does nothing and answers `forbidden`, with a
hint naming the grant it needed, what you hold and where that came from:

```
send-text is refused for this pane: writing into the pane's own session needs the write grant
```

That is the person's decision. Do the work inside what you hold, or ask the
person (`tuios ask-human`, or mail to `human`). Do not look for another verb or
process that does the same thing, and do not try to raise your own grants: you
cannot, and trying says the wrong thing about what you are doing. From a pane
without `admin`, a call that names no session means your own session.

## Where grants come from

- `--grants` when the pane was started: `tuios start-agent`, `tuios fan` and
  `tuios new-window` take it.
- Or `tuios set-pane-grants` later.
- Or else the default of `[agents.permissions]` in config.toml: `admin` under
  `mode = "open"` (the default), and the `grants` list under `mode = "strict"`
  (`read`, `write` and `fan` when unset).

A pane can never give more than it holds. A pane without `admin` that starts an
agent without `--grants` gives it its own grants. A pane changes only its own
grants unless it holds `admin`, and `admin` cannot give `respond`, so `respond`
comes only from the person.

## Giving a helper less

```sh
tuios start-agent claude --name reviewer --grants read
tuios fan 3 --agent codex --grants read,write 'Add a retry with backoff.'
tuios new-window -s work sandbox --grants read,write
tuios set-pane-grants -w reviewer --grants read
tuios set-pane-grants -w reviewer --reset
```

To drop your own pane's grants before you start an agent in it:

```sh
tuios set-pane-grants --grants read,write && exec claude
```

A supervisor the person trusts to approve its workers' tool calls is the one
pane that should hold `respond`, and the person gives it:

```sh
tuios set-pane-grants -w supervisor --grants read,write,fan,respond
```

The grants are saved with the window, so a restored pane holds what it held.
`list-windows --json` shows `grants` on every pane given its own.

## How the daemon knows the pane

The daemon places a caller by the kernel's record of its pid. Where the kernel
cannot say (Windows, the BSDs), the CLI presents `TUIOS_PANE_ID` and
`TUIOS_PANE_TOKEN` on every connection, and the daemon holds the connection to
the pane the token proves. You never pass either by hand.

Grants scope accidents and prompt-injected agents that use tuios the ordinary
way. They are not a sandbox: a process that leaves its pane on purpose is not
placed in it. What your process may do to files and other programs is the
operating system's business.
