# Task card model

This folder holds a Lean 4 model of the task card lifecycle. The model is the
specification of `TaskState.apply` in `internal/session/task_move.go`.

The work has three parts:

1. An executable model of `apply` for every move kind (`TaskSpec/Model.lean`).
2. Proofs of the rules a card must keep (`TaskSpec/Proofs.lean`).
3. A differential test that runs random move streams through the model and
   through the Go code, and compares each step
   (`internal/session/task_move_spec_test.go`, build tag `spec`).

The Lean code is not part of the tuios build. The Go test is a test file, so
the binary does not change.

## Run it

Install elan, then run the recipe from the repository root. The install script
is the one of elan v4.2.4, pinned by commit and checked by its sha256, as CI
does:

```sh
curl -sSfL https://raw.githubusercontent.com/leanprover/elan/227caca133724d5516bee25c2aeb3e609478f2d8/elan-init.sh -o elan-init.sh
echo 'a620ff1641616222c8d37c54845492004bb84d6877cdbc944dd65c1aa685bf53  elan-init.sh' | sha256sum -c -
sh elan-init.sh -y --default-toolchain none
just spec
```

`just spec` builds the model and checks the proofs (`lake build`). Then it
runs 100,000 random streams through both sides. A failed proof fails the
build. A warning fails the build too, so a proof left as `sorry` cannot pass.

The test takes these flags:

| Flag | What it does |
| --- | --- |
| `-spec.seed=N` | Use seed N. 0 picks a seed from the clock. The test logs the seed |
| `-spec.n=N` | Run N streams. The default is 100,000 |
| `-spec.bin=PATH` | Use the model executable at PATH |

On a difference, the test prints the seed, the shortest stream it found that
still shows the difference, and the step from each side. To run that stream
on the model alone, give the stream line to `.lake/build/bin/taskspec` on
stdin.

## The model executable

`taskspec` reads one JSON object per line. Each object has a start card
(`card`) and a list of moves (`moves`), each with the time it is made at
(`now`). For each line, it writes one line with the result of each move:
the action, the holder before the move, the refusal of a claim, and the card
after the move.

## What is proved

The proofs hold for every card and every move, not only for the tested ones.

| Theorem | What it says |
| --- | --- |
| `refused_changes_nothing` | Only a claim and a block of a done card are refused, and a refused move changes nothing |
| `noop_changes_nothing` | A move with no action changes nothing. A refresh claim by the holder is the one exception: it writes `dispatched` and the holder's names |
| `change_notes_one` | A move that changes the card adds one history entry with its action, `by`, time, stage and reason |
| `history_bounded` | The history never grows past 100 entries |
| `stageAt_follows_stage` | `stageAt` changes only when the stage changes |
| `attempts_monotone` | `attempts` never goes down |
| `letting_go_moves_let_go` | A hand in, a verify result, and a move to verify that runs the command leave the card with no holder |
| `doneFree_preserved` | A done card has no holder and no block, for the moves in `Admissible` |
| `holderClean_preserved` | A card with no holder has no holder time or names |
| `claim_names_a_pane` | A claim with no window is refused and changes nothing |
| `erroredHeld_preserved` | A card blocked on its errored agent has a holder, when block-task never blocks as tuios (`KeepsErroredHeld`) |
| `letting_go_lifts_errored_block` | A move that lets the holder go lifts the block on its errored agent: the block belongs to the holder |
| `block_of_done_refused` | A block of a done card is refused with `task_done` and changes nothing |
| `block_keeps_holder_and_stage` | A block of an open card keeps the holder and the stage |
| `release_keeps_stage` | A release keeps the stage, except for an errored card, which goes back to todo with no block |
| `claim_starts_work` | A claim moves a card in todo or plan to doing |
| `claim_by_holder_succeeds` | A claim by the pane that holds the card is never refused |
| `claim_refusals` | A claim refuses a card another pane holds, then a done card, then a blocked card |
| `auto_leaves_blocked_and_done` | An automatic move does not change a blocked card or a done card |
| `verified_keeps_a_blocked_card` | A verify result on a blocked card keeps its stage and its block. The note says what the verify found |
| `verified_holds_result` | A verify result that ends on a blocked card is held in the record with the stage it leads to |
| `unblock_applies_held` | When the block lifts on a card in verify, the held result applies: a pass to review, a fail to todo with one attempt more |
| `heldOk_preserved` | A held result exists only on a blocked card in verify |
| `blocked_card_never_moves_on_its_own` | Neither an automatic move nor a verify result changes the stage of a blocked card |

## What is only tested

- The Go code and the model give the same result on random streams. The
  differential test shows this for the streams it runs. It does not prove it.
- The JSON layer on both sides.
- The verbs that make each move. The `Admissible` and `KeepsErroredHeld`
  conditions say what the proofs need from them. The proofs do not check
  the verbs.
- `Session.moveCard`: the dispatch outcome, the debug log and the
  `card_check` knob.
