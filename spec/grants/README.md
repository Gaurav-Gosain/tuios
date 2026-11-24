# Pane grants model

This folder holds a Lean 4 model of pane grants and verb scopes: which caller
may call which verb on which target. The model is the specification of the
checks in `internal/session` that decide this.

The decision is in four places. A call meets them in this order:

| Place | File | What it checks |
| --- | --- | --- |
| `checkGrants` | `pane_grants.go` | What the caller's pane holds. It calls `holdToOwnTree` (`own_tree.go`), `holdToPane` (`conn_scope.go`), `holdTypingTarget` and `holdOwnRecord` |
| `checkScope` | `conn_scope.go` | What a restricted connection kept. It calls `holdToPane` too |
| `launchGrants` and `depthRefusal` | `pane_grants.go`, `agent_depth.go` | The grants a start gives, and `agents.max_depth`. The handlers of `start-agent` and `fan` call them |
| `verbSetPaneGrants` | `pane_grants.go` | Who may set which grants. It checks in its handler |

The work has three parts:

1. An executable model with one decision function, `State.decide`, and
   `allowed` on top of it (`GrantSpec/Model.lean`).
2. Proofs of the rules the docs promise (`GrantSpec/Proofs.lean`).
3. A differential test that runs random states and calls through the model
   and through the Go code, and compares each answer
   (`internal/session/grants_spec_test.go`, build tag `spec`).

The Lean code is not part of the tuios build. The Go test is a test file, so
the binary does not change.

## Run it

Install elan as `spec/tasks/README.md` says. Then run the recipe from the
repository root:

```sh
just spec
```

`just spec` builds both models and checks their proofs (`lake build`). Then
it runs both differential tests. A failed proof fails the build. A warning
fails the build too, so a proof left as `sorry` cannot pass.

The grants test takes these flags:

| Flag | What it does |
| --- | --- |
| `-spec.seed=N` | Use seed N. 0 picks a seed from the clock. The test logs the seed |
| `-grantspec.n=N` | Run N random states of 8 calls each. The default is 50,000 |
| `-grantspec.bin=PATH` | Use the model executable at PATH |

On a difference, the test prints the seed, the state, the call and the answer
of each side. To run that case on the model alone, give the case line to
`.lake/build/bin/grantspec` on stdin.

## The Go files the model describes

`go-files.txt` lists the Go files of `internal/session` that hold the grant
checks. `scripts/ci-changes.sh` starts the spec job when a pull request
changes one of them. `go-files.sh` writes the list, and the spec job runs
`go-files.sh check`, which fails when the checks reach a file the list does
not name. After a change that moves the checks, run `spec/grants/go-files.sh`
and commit the list.

## The model executable

`grantspec` reads one JSON object per line. Each object has a state
(`state`) and a list of calls (`calls`). A state has sessions, panes and
the config. A call has a caller, a verb and its parameters. For each line,
the program writes one line with the answer to each call: the decision
(`allow`, `forbidden`, `invalid`, `blocked` or `error`) and, for a start
that is served, the grants the new pane holds.

The Go test puts each random state on one test daemon: four sessions of three
panes each. It writes the windows' records, the worktree records, the grant
table and the config, and places the caller with `setApprovalPeer`.

## What is proved

The proofs hold for every state, caller, verb and parameters, not only for
the tested ones.

| Theorem | What it says |
| --- | --- |
| `expand_respond` | Admin does not give respond |
| `covers_respond`, `covers_admin` | A pane gives respond or admin only when it holds it |
| `covers_trans`, `covers_refl`, `covers_meet` | What a pane holds covers what it gives on |
| `pane_stays_in_scope` | Outside the mail rule, a pane without admin reaches through a verb that names a session only its own session, a session of its fan group (with read for a read, with fan for the rest), or the attempts it judges |
| `write_outside_needs_fan` | Outside the mail rule, a pane without admin types into, or leaves mail in, another session only with fan, and only in its fan group. This is also the reach of respond |
| `holdMail_verb`, `holdMail_needs_mail` | The mail rule holds only `send-agent-message` and `list-agents`, and only for a pane that holds mail. It types into nothing |
| `mail_sent_as_itself` | Mail the mail rule passes is sent as the caller's own pane |
| `tasks_and_reports_stay_home` | A task write and a report of a pane without admin stay in its own session |
| `reports_only_for_own_pane` | A pane reports only about its own pane unless it holds respond, with admin too |
| `admin_verbs_need_admin` | A pane without admin calls a verb that needs admin only as a copy with files, or as a close of a pane of its own tree |
| `fan_closes_only_its_tree` | A pane without admin closes only a pane below it in its own tree, never itself |
| `inOwnTree_desc` | The own tree holds only panes below the root, by the records the daemon writes |
| `depth_within_cap` | A start is served only when the new pane is within `agents.max_depth` |
| `restricted_calls_only_listed` | A restricted connection calls only the verbs the table in `docs/protocol.md` lists |
| `read_only_never_writes` | A read-only connection never types, starts a pane, writes a task card or changes the paste buffers |
| `own_scope_stays_in_scope` | A connection restricted to its own scope reads nothing across sessions or from the paste buffers, does nothing from outside every pane, and names only sessions in its scope |
| `launch_gives_no_more`, `started_pane_holds_no_more` | A start gives the new pane no more than the caller holds |
| `respond_only_from_a_holder` | Only a holder of respond starts a pane with respond |
| `set_grants_gives_no_more` | set-pane-grants from a pane gives no more than the pane holds, and a pane without admin sets only its own grants |
| `types_only_into_no_more` | A pane without admin types into another pane only when that pane holds nothing the caller does not, and is not on a prompt unless the caller holds respond |

`pane_stays_in_scope` leaves out the mail rule, which lets a pane with mail
leave mail in any session and list the agents of every session. It leaves
out the own tree rule too. That rule lets a pane
with fan close a pane of its tree in any session, and wait on its tree with
`wait-for --select`. The `wait-for` handler refuses `select` with a session,
a window or `any_session`, so such a wait reaches no other session.

## What is only tested

- The Go code and the model give the same answer on random states. The
  differential test shows this for the states it runs. It does not prove it.
- The JSON layer on both sides.
- The verb table of the model. It holds 34 verbs, at least one of each
  kind. `TestRestrictedTableNamesEveryVerbItAllows` holds the table in
  `docs/protocol.md` to the verb registry for every verb.

## What is not in the model

- Callers over a link, panes this machine runs for another machine, and
  native plugins. `checkGrants` holds them before these rules.
- The folder part of the typing rule for a pane that holds files. Every pane
  of the test daemon has the same folder.
- The typing rule when the agent features are off, and a prompt that is
  still on the screen after its state changed (`promptStillShown`).
- A start that names a parent pane. `depthRefusal` then also takes the
  parent's depth.
- How a caller is placed in a pane (`paneAuthority`). The model takes the
  pane as given.
- The `host` and `all` parameters of the mail rule, and the check of a mail
  attachment for a session out of write reach (`mailAttachmentRefusal`).
- What `admitVerb` (`verb_protocol.go`) does before `checkGrants`: it checks
  the parameter names, refuses some verbs with the agent features off or over
  a link, and rewrites the session and window with `followOwnPane`,
  `followRenamedSession` and `followMovedWindow`. The model and the test
  start from the parameters after these rewrites. The checks hold for any
  target they get.
