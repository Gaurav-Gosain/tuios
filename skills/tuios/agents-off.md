# When the agent features are off

`[agents] enabled = false` in config.toml turns off every agent feature. The
person uses it to keep only the multiplexer. Panes, windows, workspaces,
`send-text`, `send-keys`, `capture-pane`, `run`, `wait-for` on windows and
commands, worktrees and layouts all work as before.

Check it before you rely on an agent feature:

```sh
tuios get-config agents.enabled
```

## What changes for you

- Every agent verb fails with `agents_disabled` and does nothing: agent state
  (`set-agent-state`, `set-agent-meta`, `get-agent-state`), `list-agents`,
  mail, `ask-agent`, the Inbox and `ask-human`, approvals, `peek-prompt`,
  `respond`, the queue, `send-review`, `start-agent` and `fan`.
- `wait-for agent-state` and `wait-for agent-message` fail the same way. The
  window conditions still work.
- The CLI prints `Agent features are off. Set agents.enabled = true in the
  config to use this command.`
- A call to an agent on another machine whose features are off fails with
  `agents_disabled` and names that machine.
- Harness hooks report nothing, and no agent is detected. Your pane shows no
  agent state on the rail.
- The typing rule fails closed. A pane without the `respond` grant can type
  only into a pane it opened, or a pane whose shell is at its prompt.

## What to do

Do not retry. Do not edit config.toml to turn the features on. Only the
person can change `agents.enabled`: `set-config` from a pane gets
`forbidden`. Tell the person the feature you need is off, and carry on with
the verbs that still work. Coordinate through files, `send-text` to a pane
you opened, and `wait-for window-output`.

When the person turns the features on again, detection starts at once.
Queued messages and Inbox items from before were dropped when the features
went off, and they do not come back.
