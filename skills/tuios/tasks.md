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
| `tuios task done t1` | Say your work on it is done and let it go: it goes on to `verify` or `review` (see below) |
| `tuios task rm t1` | Remove it. From a pane this needs `admin` |

tuios lets a card go on its own when its pane closes, when the agent leaves the
pane, and when the daemon restarts. The card's history records the reason:
`pane closed`, `agent left` or `restart`.

## When you are done: verify and review

`tuios task done ID` from the pane that holds the card does not close it. The
card is let go, so you are free for your next one, and it goes to `review`, or
to `verify` when the board has a verify command. tuios then runs that command
in your worktree (your pane's repository, on your machine). A pass moves the
card to `review`. A fail sends it back to `todo`: its `attempts` goes up by
one and its `note` is the last line of the output (`tuios task get ID` shows
the end of the output). If your agent errors while it holds a card that
dispatch gave it, the card goes back to `todo` the same way, with your last
message as the note. The person closes a card (`done`), or its pull request
merging moves it to `shipped`.

The verify command is the person's: `tuios task dispatch verify CMD` is
refused from every pane. From a pane you also cannot move your card to
`review` or `shipped` while the board has one. Run `tuios task done` and let
the verify decide.

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
Done is set by a finish, or by the person. The history entry is by `tuios` and names the
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
`action`: `added`, `staged`, `verified`, `blocked`, `unblocked`, `released` or `removed`. A
release or a stage move tuios made on its own carries the reason in `reason`.

`--select 'task:t1'` names the pane that holds `t1`, whatever else it holds.
`task:*` names every pane that holds a card. `stage:verify` names every pane
that holds a card in verify; a done card has no holder.

`tuios task ls --all-sessions` lists every session's cards. The person sees
them as the board: `b` in the agents view (`ctrl+b i`) shows one column for
each stage, with each holder's state on its card. The person moves a card
there by key or by drag, and the move is a `task-changed` event with action
`staged`, like `tuios task stage`.

## Cards for a repository, across machines

| Command | What it does |
| --- | --- |
| `tuios task add --project . TITLE` | Add a card for this folder's repository (by its origin URL). It shows in every session of the repository and on linked machines |
| `tuios task ls --project . --hosts` | Every card of the repository, here and on each linked machine. HOST names the owner |
| `tuios task claim t3 -s build:api` | From your pane, take card t3 of session api, which machine build owns |
| `tuios task done t3 -s build:api` | Finish it there. `release` the same way |

The machine that added a card owns it. Run the claim in your own pane: your
daemon passes it to the owner and lets the card go there when your pane
closes. If a claim fails with a lost answer, run it again: a claim by the pane
that holds the card succeeds. A card without a project stays on its machine.

## Dispatch: work that comes to you

On a board in pull mode, finishing your card with `tuios task done ID` gives
you the board's next open card, while your last card is verified. Ending your
turn does not finish it: the card stays yours, in doing, until you run
`tuios task done ID` or `tuios task release ID`. It comes as
mail from tuios: read it with `tuios read-agent-messages -w "$TUIOS_PANE_ID"`.
It names the card, its prompt, and the command that finishes it. You hold the
card already, so do not claim it. If you cannot do it, `tuios task release ID`.

On a board in auto mode, tuios starts a new agent for each open card, in its
own worktree. If that is you, your first mail is the card: its done command
carries `-s SESSION` (or `-s HOST:SESSION` on another machine), because the
card lives in the session or on the machine that made it.

| Command | What it does |
| --- | --- |
| `tuios task dispatch` | The boards' modes and the switches |
| `tuios task dispatch pause` | Stop all dispatch at once. Any pane may pause |
| `tuios set-agent-state errored --kind usage_limit -m TEXT` | Say you hit a usage limit: all dispatch stops |

A mode, `resume` and quiet hours are the person's. Dispatch never answers an
approval and never gives a card to a pane that waits for the person.

## Grants

Changing a card needs the `task` grant (`tuios --skill grants`). `admin`
includes it. Reading the cards needs `read`. The cards are your own session's:
`-s` reaches another session only with `admin`.

## Errors

- `task_held`: another pane holds the card. Take another with `--next`.
- `no_task`: no card has that id, or nothing is open to take.
