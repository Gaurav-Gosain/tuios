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
`no_shell_integration`, `not_at_prompt`, `confirm_required`, `no_transcript`,
`full_screen`,
`unsupported_harness`,
`protocol_mismatch`, `unknown_host`, `host_unreachable`, `host_refused`,
`unknown_pane`, `not_worktree`, `worktree_dirty`, `worktree_detached`, `worktree_nested`, `git_failed`, `fan_exists`,
`repo_not_found`, `not_repo`, `no_notes`, `hunk_changed`, `index_changed`,
`whole_file_only`, `no_checkpoint`, `nothing_to_commit`,
`merge_conflict`, `checkout_dirty`, `no_remote`, `gh_unavailable`, `queue_full`,
`task_held`, `no_task`,
`no_buffer`, `risk_unacknowledged`, `agents_disabled`, `busy`, `too_many_connections`,
`no_file`, `file_exists`, `no_permission`, `hash_mismatch`, `source_changed`,
`cross_device`, `disk_full`, `no_transfer`, `same_session`, `pane_not_movable`,
`invalid_tml`, `no_surface`, `too_many_surfaces`,
`internal`. `internal` is a failure inside the daemon
that none of the others names; its message says what went wrong. The CLI folds
the same information into its messages.

## What each asks of you

A retry without a change fails the same way, except for `busy`, `too_many_connections` and `timeout`. The first two clear when other requests finish or other connections close.

| Code | What to do |
| --- | --- |
| `invalid_request` | The line was not a valid request, or the connection was in the wrong state for the verb. Check the JSON envelope (`tuios --skill events`). |
| `unknown_verb` | The daemon has no such verb. The hint names the closest one. `tuios list-verbs` lists them all. |
| `session_not_found` | No session has that name. The hint lists the live ones. Run `tuios ls`, or pass `-s "$TUIOS_SESSION"` for your own. |
| `session_exists` | A session already has that name. Pick another name. Do not assume you own the old session. |
| `window_not_found` | The `-w` target matched no pane. The hint lists the panes. Run `tuios list-windows -s "$TUIOS_SESSION"` and use a name or an id from it. |
| `no_windows` | The session has no panes. Open one with `tuios new-window`. |
| `pty_not_found` | The pane's program has exited, so nothing can read or type there. Close the pane, or open a new one. |
| `command_failed` | The attached client could not do what the verb asked. The message says why. |
| `timeout` | A `wait-for` condition did not match in time. Read the pane with `capture-pane` before you wait again, then wait with a larger `--timeout`. |
| `risk_unacknowledged` | An allow for an approval that matches a risk rule did not name those rules in `risk_ack`. Nothing was answered. Read the rules with `get-approval`, or let the person answer. |
| `internal` | The daemon failed in a way no other code names. The message says what went wrong. Run `tuios logs`, and tell the person. |
| `invalid_params` | The daemon does not take a parameter you sent. If you believed in it, the daemon is older than you think: ask `list-verbs`. |
| `option_not_found` | The hint carries the closest option; `list-options` describes them all. |
| `needs_client` | Splitting, tiling, directional focus and popups need an attached client. Reading, writing, waiting, creating and moving never do. |
| `not_ready` | The target agent is mid-turn, or `unknown`. Wait for it, or look at it and pass `--force`. |
| `agent_blocked` | The target is on `needs_input`. Read its prompt with `peek-prompt` and ask the person. Do not type at it. |
| `prompt_stalled` | The text was typed and the agent showed no sign of taking it. Look at the pane before sending it again. |
| `loop_refused` | You addressed yourself, or would close a cycle of asks. Restructure. |
| `rate_limited` | Stop sending. Two agents are probably answering each other. |
| `no_keyboard` | `human` has no pane. Use `ask-human` or mail to `human`. |
| `queue_full` | The pane already holds `[agents.queue] max` queued messages. Wait for the agent to take them, or drop one with `tuios queue rm`. |
| `task_held` | Another pane holds the card you claimed. Nothing changed. Take another with `tuios task claim --next`, or wait for `task-changed` released. |
| `no_task` | No card has that id, or `claim --next` found none open: every card is held, blocked or done. `tuios task ls` lists them. Stop, or wait for `task-changed` added. |
| `no_buffer` | From the paste buffer verbs: no buffer has that name, or there are no buffers. Nothing was read, pasted or deleted. `tuios list-buffers` shows the names. |
| `busy` | The daemon had no room for the request now: other large requests held its memory, or the pane has not read the last large input. From `transfer-start`: another copy writes that path now. Nothing was done. Try again after a short wait, or when that copy ends. |
| `too_many_connections` | The daemon already serves as many connections as it takes, and closed yours. Nothing was done. Close some clients or commands, then try again. |
| `agents_disabled` | The person turned the agent features off with `[agents] enabled = false`, and the verb is one of them. Nothing was done. Do not retry. Tell the person. |
| `not_repo` | From `review-diff` and `review-note`: no git repository is under the pane, or its process runs on another machine. Nothing was read. |
| `no_notes` | From `send-review`: no unsent notes. Add one with `tuios review note`, or name sent ones with `--id` to send them again. |
| `hunk_changed` | From `review-stage`: the file or the hunk is not in the diff now. Nothing was changed. Read the diff again with `review-diff` and the same `index` and `context`. |
| `index_changed` | From `review-stage` undo: the index changed after the call. Nothing was changed. Read the diff again. |
| `whole_file_only` | From `review-stage`: a rename, a binary file or a very large file is staged only whole. Nothing was changed. Leave `hunk` and `lines` out. |
| `no_checkpoint` | From `checkpoint-diff` and `restore-checkpoint`: the pane has no checkpoint by that number. The hint lists the ones it has. Nothing was changed. |
| `nothing_to_commit` | From `ship-commit`: the work tree has no change. From `ship-push` and `ship-pr`: the branch has no commit. Nothing was changed. |
| `merge_conflict` | From `ship-merge`: the merge conflicted and was undone. The hint lists the files. Rebase the branch in the worktree, resolve them there, and merge again. |
| `checkout_dirty` | From `ship-merge`: the main checkout has changes, a merge in progress, or another branch checked out. Nothing was merged. Tell the person. Do not clean their checkout. |
| `no_remote` | From `ship-push` and `ship-pr`: no remote, or not the one named. Nothing was pushed. |
| `gh_unavailable` | From `ship-pr` and `ship-status`: gh is not installed or not logged in. Nothing was pushed. Tell the person to run `gh auth login`. |
| `forbidden` | Your pane's grants (`tuios --skill grants`), a link's policy on another machine, or sending as `human` from a pane. The message names what was needed. Tell the person; do not look for another way. |
| `not_human` | Only the person may do this: `dismiss-attention`, `respond` (unless your pane holds `respond`) and `reply-approval`. Change your own state, or ask the person. |
| `prompt_changed` | The prompt moved or was answered before `respond` landed. Nothing was pressed. |
| `not_resumable` | The pane has no conversation `resume-agent` can bring back. Nothing was typed. |
| `no_shell_integration`, `not_at_prompt` | From `run`: the shell sends no OSC 133 marks, or is busy. Nothing was typed. Use `send-text` and a marker, or `wait-for command-finished`. |
| `full_screen` | From `clear-history`: a full-screen program uses the pane. Nothing was cleared. Wait for the program to exit. |
| `confirm_required` | A write by selector, or a `ship-push` or `ship-pr`. The hint lists the panes, or what would be sent, and a token. Check them, then call again with the token. |
| `no_transcript`, `unsupported_harness` | From `agent-transcript`: the pane is not joined to a transcript, or its harness keeps none this daemon reads. Read the pane with `capture-pane`. |
| `protocol_mismatch` | The caller's protocol version is outside what this daemon accepts. Use a matching tuios. |
| `unknown_host` | No host by that name. Names are matched exactly and never guessed. |
| `host_unreachable` | The host is not answering: its link is down or does not answer. Nothing was queued except mail. `tuios hosts` says why. |
| `host_refused` | The link is up and cannot take another connection. Close one. |
| `unknown_pane` | A pane id on the far machine is gone. Drop it. |
| `not_worktree`, `worktree_dirty`, `worktree_detached`, `worktree_nested`, `git_failed`, `repo_not_found` | From the worktree verbs and `start-agent`. A dirty worktree is left alone until you pass `--stash` or `--force`. A worktree on a detached HEAD holding commits no branch has is refused — removing it would lose those commits; commit them onto a branch, or pass `--force` to lose them. A worktree holding a nested git repository in an ignored or untracked path is refused — removing it would delete that repository with its history, and no stash can save it; remove the nested repository first, or pass `--force` to lose it. `repo_not_found` means that machine has no checkout of the origin: pass `--clone`. |
| `fan_exists` | From `fan`: a fan by that name is in that repository. Nothing was started. Pick another name, or pass `--join` with a session of that fan to add attempts to it. |
| `no_file`, `file_exists`, `no_permission` | From the file verbs and `transfer-start`: the path is not there, the destination is there and the call did not say what to do with it, or the user the daemon runs as may not touch it. Nothing was changed. For `file_exists`, ask the person, then pass `conflict`. |
| `hash_mismatch` | A copy's bytes did not match the original's sha256. The copy was removed, and the transfer copies that file once more by itself. A second mismatch fails the transfer: the disk or the file may be changing. `tuios cp` exits 6. |
| `source_changed` | A file changed on its machine while it was copied: its size or time is not what the copy started from. Nothing was put in place for it. Copy it again when nothing writes it. `tuios cp` exits 6. |
| `cross_device` | `file-rename` cannot move a path to another disk. Copy it with `transfer-start` and `move`. |
| `disk_full` | The disk had no room. What was written stays in the part, and `transfer-resume` goes on from it once there is space. |
| `no_transfer` | No transfer has that id. A finished transfer leaves the list 30 minutes after it ends. |
| `same_session`, `pane_not_movable` | From `move-window` with `to_session`: the window is already in that session, or it is a popup, a scratch pane, a TML pane or a pane on another machine. Nothing moved. |
| `invalid_tml` | A TML document does not parse or validate. The error lists each problem with a JSON Pointer to the node and the fix. Run `tuios tml validate`. |
| `no_surface` | No open TML surface has that id. It closed, or the id is wrong. |
| `too_many_surfaces` | The caller holds as many open TML surfaces as it may: 8 for a pane, 32 per session for the callers outside the panes. Close one first. |

## The exit codes of tuios cp

`tuios cp` and `tuios transfers wait` fold the codes into exit codes
(`tuios --skill files`):

| Exit | What it means |
| --- | --- |
| `0` | Every file was copied, or was there with the same bytes |
| `1` | The copy failed. The message names the cause and the fix |
| `2` | The command line is wrong: `invalid_params`, or `unknown_host` |
| `3` | Done, but some items were not copied: links, pipes, devices, files made at the destination during the copy, or files that differ with no `--conflict` given |
| `4` | `forbidden`: your pane grants or the host's link policy. That is the person's decision. Tell them |
| `5` | `host_unreachable` or `host_refused` for `--timeout`. The copy still waits in the daemon |
| `6` | `hash_mismatch` twice, or `source_changed` |
| `130` | Cancelled |

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
