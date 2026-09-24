# Errors, and a daemon that is not running

Failures name the cause and the fix. A bad window target lists the windows that
exist, a bad session name suggests the closest live one, and a wait that times
out tells you to capture the pane. Read the whole error before retrying.
`tuios list-verbs --json` carries the same catalogue with a line per code.

## The codes

Over the socket every failure carries a stable code in the error envelope:
`invalid_request`, `unknown_verb`, `invalid_params`, `session_not_found`,
`session_exists`, `window_not_found`, `no_windows`, `pty_not_found`,
`needs_client`, `option_not_found`, `command_failed`, `timeout`, `not_ready`,
`agent_blocked`, `prompt_stalled`, `loop_refused`, `rate_limited`,
`no_keyboard`, `forbidden`, `not_human`, `prompt_changed`, `not_resumable`,
`no_shell_integration`, `not_at_prompt`, `confirm_required`,
`protocol_mismatch`, `unknown_host`, `host_unreachable`, `host_refused`,
`unknown_pane`, `not_worktree`, `worktree_dirty`, `git_failed`,
`repo_not_found`, `not_repo`, `no_notes`, `queue_full`,
`risk_unacknowledged`, `internal`. A verb `list-verbs` lists that answers
`internal` with "not built yet" is registered ahead of its work: nothing was
done, and retrying will not help. The CLI folds the same information into its
messages.

## What each asks of you

Not one of these is a timeout. Retrying one unchanged fails the same way.

| Code | What to do |
| --- | --- |
| `invalid_params` | The daemon does not take a parameter you sent. If you believed in it, the daemon is older than you think: ask `list-verbs`. |
| `option_not_found` | The hint carries the closest option; `list-options` describes them all. |
| `needs_client` | Splitting, tiling, directional focus and popups need an attached client. Reading, writing, waiting, creating and moving never do. |
| `not_ready` | The target agent is mid-turn, or `unknown`. Wait for it, or look at it and pass `--force`. |
| `agent_blocked` | The target is on `needs_input`. Read its prompt with `peek-prompt` and ask the person. Do not type at it. |
| `prompt_stalled` | The text was typed and the agent showed no sign of taking it. Look at the pane before sending it again. |
| `loop_refused` | You addressed yourself, or would close a cycle of asks. Restructure. |
| `rate_limited` | Stop sending. Two agents are probably answering each other. |
| `no_keyboard` | `human` has no pane. Use `ask-human` or mail to `human`. |
| `forbidden` | Your pane's grants (`tuios --skill grants`), a link's policy on another machine, or sending as `human` from a pane. The message names what was needed. Tell the person; do not look for another way. |
| `not_human` | Only the person may do this: `dismiss-attention`, `respond` (unless your pane holds `respond`) and `reply-approval`. Change your own state, or ask the person. |
| `prompt_changed` | The prompt moved or was answered before `respond` landed. Nothing was pressed. |
| `not_resumable` | The pane has no conversation `resume-agent` can bring back. Nothing was typed. |
| `no_shell_integration`, `not_at_prompt` | From `run`: the shell sends no OSC 133 marks, or is busy. Nothing was typed. Use `send-text` and a marker, or `wait-for command-finished`. |
| `confirm_required` | A write by selector. The hint lists the panes and a token; check them, then call again with `--confirm`. |
| `protocol_mismatch` | The caller's protocol version is outside what this daemon accepts. Use a matching tuios. |
| `unknown_host` | No host by that name. Names are matched exactly and never guessed. |
| `host_unreachable` | The host is not answering. Nothing was queued except mail. `tuios hosts` says why. |
| `host_refused` | The link is up and cannot take another connection. Close one. |
| `unknown_pane` | A pane id on the far machine is gone. Drop it. |
| `not_worktree`, `worktree_dirty`, `git_failed`, `repo_not_found` | From the worktree verbs and `start-agent`. A dirty worktree is left alone until you pass `--stash` or `--force`. `repo_not_found` means that machine has no checkout of the origin: pass `--clone`. |

## When the daemon is not running

`tuios ls` tells a script which situation it is in by its exit code: 0 is a
running daemon (even one with no sessions), 3 is no daemon, and 1 is a failure.
With no daemon, sessions saved on disk are listed anyway:

```
saved: on disk only, with no daemon running to hold it.
```

`tuios attach` starts the daemon and restores the saved sessions before
attaching. From a script, `tuios start-server` restores without taking over the
terminal. A restored session keeps its names, workspace names, window ids and
window names, and is marked:

```
restored: the layout came back from saved state, and the shells are new.
```

The shells are new: scrollback is empty, whatever ran is gone, and mail and
agent state died with the old daemon. Treat a restored session as panes to be
started again, addressed by the ids and names you already know. An agent
conversation can come back with `resume-agent` (`tuios --skill state`).

## The person's setup

`startup.daemon` ships on, so a plain `tuios` attaches to a daemon session. What
decides whether you have a socket is `TUIOS_ENV`, so guard on that. For a
session that will not start, `tuios --standalone` and `TUIOS_NO_DAEMON=1` skip
the daemon for a run and for a shell. `tuios logs` shows the daemon's log.
