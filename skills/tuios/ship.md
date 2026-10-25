# Ship: commit, merge, push and open a pull request

`tuios ship` takes the work in a pane's worktree to a merged change. git runs
with the person's own identity, signing and hooks. A call from a pane runs git
with no hooks, and with no program that only the repository's own config
names. tuios adds nothing to a commit message and never forces a merge or a
push.

```sh
tuios ship commit -s api-feat-retry -m 'Add a retry with backoff'
tuios ship merge -s api-feat-retry
tuios ship merge -s api-feat-retry --squash -m 'Add a retry with backoff'
tuios ship push -s api-feat-retry
tuios ship pr -s api-feat-retry --draft
tuios ship status -s api-feat-retry --refresh --json
tuios fan keep api-fan-add-retry-2 --merge --squash
```

## Commit and merge stay on this machine

- `ship commit` (`ship-commit`) stages every change in the pane's work tree,
  untracked files included and ignored ones not, and commits on its branch.
  Without `-m` the message is the pane's last prompt or turn summary. It is
  refused with `not_ready` while the agent works (`--force` overrides) and
  with `nothing_to_commit` on a clean tree.
- `ship merge` (`ship-merge`) merges the branch into its base in the main
  checkout. `--into` names another branch, `--squash` makes one commit,
  `--ff-only` refuses a branch that cannot fast-forward. A conflict is undone
  and fails with `merge_conflict`, the files in the hint. A main checkout with
  changes, or on another branch, is `checkout_dirty`. Tell the person. Do not
  clean their checkout.
- `fan keep --merge` merges the kept attempt first, and removes nothing when
  the merge fails.

## Push and pull request leave the machine

`ship push` (`ship-push`) and `ship pr` (`ship-pr`) send work off the machine,
so they ask first.

1. The first call sends nothing. It is `confirm_required`, with what would be
   sent in `available` and a token in `confirm`. The CLI shows this and asks.
   `--yes` skips the CLI's question.
2. From a pane, the call then puts `Push BRANCH (SHA) to REMOTE (HOST/PATH)?` in the
   person's Inbox and waits for `allow` (`--wait`, 2 minutes).
3. A timeout is `not_ready` with a `request_id`. Wait again with
   `--request ID`. Do not ask a second time.
4. A `deny` is `forbidden`. Do not ask again unless the person tells you to.

The push sends the commit the person allowed, not the branch tip at push
time. A commit made after the question is not pushed. The remote is the one
the branch follows, else the only one, else `origin`. `--remote` names
another. When the remote branch moved on, git refuses the push: rebase in the
worktree and push again.

`ship pr` pushes and then runs the person's `gh`. `gh_unavailable` means gh is
missing or not logged in, which only the person can fix. A branch with an open
pull request gets the push and no new pull request.

## Watching the pull request

`ship status` (`ship-status`) shows the pull request and its checks as tuios
last read them. `--refresh` asks gh now. The daemon reads an open pull request
again every minute while a client is attached. The rail shows it on the
session's agent rows (`PR #12 open pass`), and it is `pr` in
`worktree ls --json` and `ls --json`.

## In a jj workspace

jj checks out no branch and has no staging area, so ship goes by bookmark.
The branch is the bookmark on `@`, else on `@-` when `@` is empty. Without
one, ship refuses: create it with `jj bookmark create <name> -r @-`.

- `ship commit` runs `jj commit`. The bookmarks on the commit it was made
  on move to the new commit, trunk's excepted.
- `ship merge` only fast-forwards the target's bookmark, trunk's by default.
  A branch the target moved past is refused with the `jj rebase` to run.
  `--squash` is refused: squash with `jj squash` first.
- `ship push` runs `jj git push` of the bookmark, and is refused when the
  bookmark moved after the person allowed the push.

## Grants

From a pane, `ship-commit` and `ship-merge` need `write` on a pane that holds
nothing you do not. `ship-status` needs `read`. A push or a pull request from a
pane always waits for the person in the Inbox, whatever the pane holds.
