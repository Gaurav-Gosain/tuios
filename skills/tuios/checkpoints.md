# Checkpoints: undo an agent's turn

When an agent's turn ends (`working` to `done`, `idle` or `needs_input`) and
its pane is in a git work tree, the daemon saves the work tree as a
checkpoint. It is a commit under `refs/tuios/checkpoints/<window id>/<n>`. The
index, `HEAD`, the branch and the stash do not change. A turn that changed no
file gets no checkpoint.

```sh
tuios checkpoint list -s "$TUIOS_SESSION" -w build
tuios checkpoint diff -s "$TUIOS_SESSION" -w build 3
tuios checkpoint restore -s "$TUIOS_SESSION" -w build 2
tuios checkpoint list -s "$TUIOS_SESSION" -w build --json
```

- `checkpoint list` (`list-checkpoints`) shows each checkpoint, oldest first,
  with its turn, the agent's state, the time and its label (the turn's prompt).
- `checkpoint diff N` (`checkpoint-diff`) shows what turn N changed, against
  the checkpoint before it, or `HEAD` for the first one. Without `N` it shows
  the newest.
- `checkpoint restore N` (`restore-checkpoint`) first saves the work tree as a
  `safety` checkpoint, then writes back only the files that differ. To undo
  the restore, restore the safety checkpoint. `git status` then shows the
  restored files as changes.

## What a checkpoint holds

Tracked and untracked files. Ignored files are left out, and so is an
untracked file larger than `max_untracked_mb` (50 by default). `checkpoint
list` names the files it left out, and a restore does not change them.

## When a restore is refused

Every refusal changes nothing in the work tree.

- `not_ready`: the agent is `working` or `needs_input`, and would write over the
  restored files. Wait for the turn to end. `--force` restores anyway.
- `invalid_params` with the paths in `available`: the restore would write over
  an ignored or new file that no checkpoint holds. Tell the person. Do not
  delete the files to make the restore work.
- `invalid_params`: the checkpoint was taken in another work tree than the
  one the pane is in now.
- `no_checkpoint`: no checkpoint has that number. The hint lists the ones
  the pane has.

## Grants and limits

From a pane, the list and the diff need `read`. A restore needs `write` on a
pane that holds nothing you do not. Checkpoints work on this machine's panes
only. For a pane on another machine, attach to that machine. `[agents.checkpoints]`
in config.toml turns them off (`enabled`), sets how many a pane keeps (`keep`,
50) and the untracked file limit (`max_untracked_mb`). A removed worktree takes
its checkpoints with it.
