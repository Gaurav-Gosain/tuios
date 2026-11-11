# ovnfs

Opens [ovnfs](https://github.com/stencil-hq/ovnfs) checkouts from tuios. A
checkout is a copy-on-write view of a folder: it opens in about a second at any
size and copies nothing. Work in it, then apply the changes back to the folder.

You need `ovnfs` 0.4.0 or newer and `jq` on your `PATH`:

```sh
go install github.com/stencil-hq/ovnfs@latest
tuios plugins install ovnfs
```

With an older ovnfs the plugin says so and does nothing else. `ovnfs update`
brings it up to date. The plugin needs 0.4.0 because ovnfs 0.3.0 changed what
`ovnfs rm` does: it now deletes the checkout's snapshots unless it is told to
keep them, and the plugin always keeps them.

A tuios that you built yourself takes `tuios plugins install Gaurav-Gosain/tuios/plugins/ovnfs`.

In the command palette:

| Entry | What it does |
|---|---|
| **ovnfs: New checkout here** | Opens a checkout of the focused pane's folder, and a pane with your shell in it |
| **ovnfs: Checkouts** | Lists the checkouts with their changes and idle time. Choose one to diff, apply, apply to a branch, open a pane in it, attach it to a host or remove it. `p` prunes |
| **ovnfs: Diff** | Shows what the checkout changed |
| **ovnfs: Apply** | Shows a dry run, then merges the changes into the folder when you type `yes` |
| **ovnfs: Apply to a branch** | Makes a branch in the folder's repository from the checkout's commits |
| **ovnfs: Attach to a host** | Makes the checkout appear on another machine over ssh. It prints the line that opens a pane there, and the `ovnfs push` line that lets the checkout work there without this machine |

Diff, Apply and the others act on the checkout the focused pane is in, else on
the one checkout of the focused folder, else they ask.

Two hooks run on events and exit:

- When a pane that the plugin opened closes and its checkout has changes,
  the plugin says so and the dock shows `ovnfs N to review`. It never applies.
- When tuios removes a worktree, the plugin removes the checkouts of that
  worktree. It takes a snapshot of a checkout with changes first, and keeps
  the snapshots when it removes the checkout, so `ovnfs new --from SNAP`
  opens them again.

Removing a checkout from the list keeps its snapshots the same way. Prune
keeps them when you include checkouts with changes, and removes them with
checkouts that have none.

The plugin holds the `ui` grant, which shows text only. Its panes hold no
grant. ovnfs runs with your rights: it is not a sandbox.
