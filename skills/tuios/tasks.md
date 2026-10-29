# Task cards: hand out work from the daemon

A task card is one unit of work in a session: a title, the prompt for the pane
that takes it, a stage, and the pane that holds it. The daemon keeps the cards,
so they outlive the pane that added them. After a daemon restart they come
back with every holder let go.

```sh
tuios task add -p 'Read api/retry.go and add a jitter option, with a test.' make the backoff jitter configurable
tuios task ls --open
tuios task get t1
```

## Take a card, do it, finish it

From inside a pane:

```sh
line=$(tuios task claim --next) || exit 0   # no_task: nothing open
id=${line%% *}
tuios task get "$id" --json | jq -r .prompt
# ... do the work ...
tuios task done "$id"
```

`claim --next` takes the oldest card that no pane holds and that is not blocked
or done. Two panes that claim at the same moment get two different cards.
`claim` prints the card's id and title on its first line. A card in `todo` or
`plan` moves to `doing` when you claim it.

You claim only for your own pane. You finish, stage, block or release only the
card you hold. A pane that holds `admin` can act on any card, and so can the
person or a shell outside every pane. Outside a pane, name the pane:
`tuios task claim t3 -w worker`.

## Stages, blocks and letting go

| Command | What it does |
| --- | --- |
| `tuios task stage t1 review` | Move the card to a stage: `todo`, `plan`, `doing`, `verify`, `review`, `shipped`, `done` |
| `tuios task block t1 waiting on the staging key` | Mark it stuck. It keeps its holder and stage, and `claim --next` skips it |
| `tuios task block t1 --clear` | Lift the block |
| `tuios task release t1` | Let it go without finishing. It keeps its stage, and another pane can claim it |
| `tuios task done t1` | Mark it done and let it go |
| `tuios task rm t1` | Remove it. From a pane this needs `admin` |

tuios lets a card go on its own when its pane closes, when the agent leaves the
pane, and when the daemon restarts. The card's history records the reason:
`pane closed`, `agent left` or `restart`.

## Stages that move on their own

tuios also moves a card when it sees the work move. A plan your agent holds for
the person moves your cards from `todo` or `doing` to `plan`, and the person
approving it moves them from `plan` to `doing`. That needs `[agents.approvals]`
to hold plans for your harness and your pane to be out of view; a plan the
person keeps planning, or answers in your pane, leaves the card where it is.
`tuios fan verify` moves the
fan's cards from `doing` to `verify`, and back to `doing` when the check fails
in every attempt. A pull request on the worktree's branch moves the cards held
there, or the fan's, to `review` when it opens and to `shipped` when it merges,
out of `plan` too, since the pull request shows the plan went through. Closed
without a merge, it moves them from `review` back to `doing`.

Each move fires only from those stages, so a card you put somewhere else by
hand stays there. A blocked card and a done card never move on their own.
Done is always yours to set. The history entry is by `tuios` and names the
reason. The person can turn this off with `[tasks] auto_stage = false`.

## Hand a card to a fan or a new agent

```sh
tuios fan 3 --agent claude --task t3 'Add a retry with backoff.'
tuios start-agent claude --name t4-worker --task t4 --prompt 'Take task t4: tuios task get t4.'
```

`fan --task` stamps the card with the fan, and with `auto_stage` on moves it
to `doing`. No pane holds it, since the attempts work on it, and `claim --next`
skips it while the fan lasts. `--task-session` names the card's session when
it is not yours. `start-agent --task` claims the card for the new pane as soon
as the pane exists. Both check the card before they make anything: it must be
held by no pane (`fan` also takes one your own pane holds), not blocked and
not done.

## Hear changes

```sh
tuios subscribe -s "$TUIOS_SESSION" --types task-claimed,task-finished,task-changed
```

Each event carries the card's row in `task`. `task-claimed` and `task-finished`
carry the holder's pane in `window`. `task-changed` says what happened in
`action`: `added`, `staged`, `blocked`, `unblocked`, `released` or `removed`. A
release or a stage move tuios made on its own carries the reason in `reason`.

`--select 'task:t1'` names the pane that holds `t1`, whatever else it holds.
`task:*` names every pane that holds a card. `stage:verify` names every pane
that holds a card in verify; a done card has no holder.

`tuios task ls --all-sessions` lists every session's cards. The person sees
them as the board: `b` in the agents view (`ctrl+b i`) groups the cards by
stage, with each holder's state beside its card.

## Grants

Changing a card needs the `task` grant (`tuios --skill grants`). `admin`
includes it. Reading the cards needs `read`. The cards are your own session's:
`-s` reaches another session only with `admin`.

## Errors

- `task_held`: another pane holds the card. Take another with `--next`.
- `no_task`: no card has that id, or nothing is open to take.
