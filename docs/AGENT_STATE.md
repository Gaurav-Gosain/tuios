# Agent state

tuios tracks a semantic state for each window's pane so a session can show which
panes need attention. A pane running a coding agent reports what it is doing;
tuios stores that state per window, syncs it to every attached client, and draws
an indicator for it.

This page is the reference for the feature. An agent that wants to use it from
inside a pane should run `tuios --skill`, which prints the reporting recipes
alongside the rest of the pane-driving surface.

## Table of Contents

- [States](#states)
- [Reporting state](#reporting-state)
- [Sources and precedence](#sources-and-precedence)
- [Recognising a harness](#recognising-a-harness)
- [Screen rules](#screen-rules)
- [Title rules](#title-rules)
- [Notification rules](#notification-rules)
- [The stall heuristic](#the-stall-heuristic)
- [Finished turns](#finished-turns)
- [Indicator](#indicator)
- [The rail's agents section](#the-rails-agents-section)
- [Harness integrations](#harness-integrations)
- [Environment](#environment)
- [Alerts](#alerts)

## States

| State         | Meaning                                          |
| ------------- | ------------------------------------------------ |
| `none`        | Not running an agent, or not reporting (default) |
| `working`     | Actively working on a task                       |
| `needs_input` | Blocked waiting for the user                     |
| `idle`        | Not working and not blocked                      |
| `done`        | Finished its task                                |
| `errored`     | Stopped because of an error                      |
| `unknown`     | An agent is present and nothing says what it does |

`needs_input` is one state with a reason attached, not a family of states. An
agent waiting for approval of a tool call and one asking a question are both
blocked on a person; the `message` says which. `get-agent-state`, `list-agents`
and `explain-agent-detect` also report `needs_you`, true for `needs_input` and
`errored`, so a consumer that only wants "does a person have to act" does not
have to know which states mean it.

`get-agent-state` and `list-agents` also report `blocked_by`, `approval` or
`question`, for a pane on `needs_input`. A screen, title or notify rule
supplies it from its `kind` (named in the manifest, or guessed from the rule's
words), and a report supplies it with the `kind` param of `set-agent-state`, as
`tuios agent-hook` does. A report that carries no kind has it guessed from the
reported message the same way: a message that mentions approval, permission,
allowing, proceeding, confirming or trust reads as `approval`, anything else as
`question`. An empty message gives an empty `blocked_by`, which means the
source did not say.

Both verbs report `ready`, which is whether `ask-agent` would type at the pane
now: true for `idle`, `done`, `errored` and `none`. It is false for
`needs_input`: an agent there is waiting on a prompt, text typed at it answers
the prompt, and `ask-agent` refuses it with `agent_blocked`. It is false for
`unknown` too, for the reason below. Until these changed, `ready` was true for
both, and an ask could type straight into a permission menu. See the protocol
changes in [protocol.md](protocol.md).

`unknown` exists so that no evidence is never reported as at rest. The silence
timer writes it, not `idle`, when the screen tier looked at a quiet pane and
found nothing: `idle` says nothing needs you, and a pane that went quiet on a
prompt no rule knows would be lying. A client that predates the state draws no
glyph for it.

`unknown` is a display state and not a ready one. `fan` waits for `idle` or
`done`, `ask-agent` also takes `errored` and `none`, and neither types into an
`unknown` pane, because a quiet pane with nothing on its screen may be in the
middle of a long tool call. A harness whose manifest reads its prompt box
reaches `idle` instead (see [Screen rules](#screen-rules)); for any other, pass `force` to `ask-agent`, or
send a fan prompt with `send-text` once the pane is at its prompt.

State is daemon-owned per-window state. It rides the same versioned state sync
every other window property uses, so it survives detach/reattach and reaches all
clients. `none` is the zero value and is never persisted, so older sessions and
older clients simply read every pane as `none`.

## Reporting state

A pane reports its own state through the `set-agent-state` verb, the same way it
would call `send-keys` or `capture-pane`:

```sh
# From inside a pane
tuios set-agent-state working
tuios set-agent-state needs_input -m "awaiting approval"
tuios set-agent-state done
tuios set-agent-state none          # clear it
```

Read it back with `get-agent-state`:

```sh
tuios get-agent-state               # prints the state name
tuios get-agent-state -w build --json
```

Both are ordinary control-protocol verbs, so they appear in `tuios list-verbs`
and can be called over the daemon socket directly. `list-windows --json` also
reports each window's `agent_state`, so a cross-session view can read every
pane's state in one call.

Targeting follows the same rules as the other window verbs: `-s`/`--session`
selects the session (default: most recently active), `-w`/`--window` selects the
window by id or name (default: the focused window).

## Sources and precedence

More than one thing can have an opinion about a pane. `set-agent-state` takes an
optional `source` saying where the state came from, and the daemon uses it to
decide which opinion wins:

| Source   | Meaning                                          |
| -------- | ------------------------------------------------ |
| `report` | The agent reporting for itself (default)         |
| `osc`    | An escape sequence the pane emitted              |
| `screen` | A rule matched against the pane's rendered text  |
| `stall`  | The silence timer                                |
| `detect` | The foreground process of the pane               |

A source may write over a claim ranked at or below its own and never over one
ranked above it, so a screen rule cannot overwrite what an agent reported for
itself. A source updating its own claim is always allowed. A report that loses
comes back with `"applied": false` and the state that stands, rather than an
error.

Omitting `source` means `report`, so a caller that never sets it behaves exactly
as it always has. `get-agent-state` reports the winning `source` and, when one
was named, the `harness_id`, so a surprising indicator can be traced to the thing
that set it.

### Confidence

The ranking is the confidence model, for state and for identity alike. A rank
rather than a score is deliberate: a score invites adding weak signals together
until they clear a threshold, which is how a directory name in a script path
once came to count as an agent. A rank means a weak signal can never add up to
a strong verdict, because nothing is added.

For state, the source says how much to trust it: a `report` or a `transcript`
is the agent's own account, `osc` is a sequence it emitted, `screen` is a rule
reading its display, `detect` is the detector assuming `working` from presence,
and `stall` is a timer. For identity, `get-agent-state` reports `identity` and
`confidence`:

| `identity`  | `confidence` | Meaning                                              |
| ----------- | ------------ | ---------------------------------------------------- |
| `report`    | `certain`    | The harness named itself with `--harness`            |
| `manifest`  | `strong`     | A manifest rule matched the process's own identity   |
| `list`      | `strong`     | The built-in or user name list matched its name      |
| `hint`      | `strong`     | `TUIOS_AGENT` on the foreground process named it     |
| (empty)     | `none`       | Nothing has named a harness                          |

A screen rule never names a harness, and a word inside an argument never counts
at all, so there is no `weak` tier: evidence that weak creates no claim.

### Attribution outlives a report

`harness_id` answers a different question from the state: which harness the pane
is running, which is what tells the screen tier whose rules to match against it.
The foreground-process detector owns it. It names the harness when it sees the
binary and clears it when the agent leaves the foreground, which is the only
event that can say a pane is no longer running one.

A report may name a `harness` to attribute a pane the detector could not, for
instance one running behind a wrapper. A report that names none is silent about
attribution and leaves it standing, so a hook that reports only a state does not
cost its pane the screen rules that cover the prompts its hooks do not. The one
report that clears attribution is `none`, which says outright that the pane is
not running an agent.

### The one exception: a visible blocker

Ranking alone has a hole in it. A harness that reports `working` for itself and
then stops on a permission prompt without saying anything further keeps its
claim, and the screen rule that can read the prompt ranks below it, so the pane
shows `working` for as long as the user is being waited for.

So a screen rule that matched a **blocking** state may write over a higher-ranked
claim that has gone stale. Stale is checked, not assumed, and all of this has to
hold:

- The rule's state is `needs_input`. A rule claiming `working` or `idle` is
  guessing at a process from how it looks and never overrides anything.
- The claim does not already say `needs_input`, so a harness reporting the prompt
  properly keeps its own claim.
- The pane has produced output since the claim was stamped, so the claim is
  describing a screen that has been painted over rather than merely being old.
- The claim has stood unrefreshed for two seconds, which is the fair-chance
  window: a harness with a hook reports the prompt itself in far less than that,
  and it is the better answer.

Only a report refreshes a claim. A title or screen look that reads back the
claim its own source already holds (same state, message and harness) writes
nothing: no new stamp, no version bump, no push. Before this, a spinner frame
left in the title restamped its `working` claim on every look, so the claim never
went two seconds unrefreshed and the prompt under it never showed. A hook or a
`tuios set-agent-state` caller repeating itself still restamps, since that is a
source actively reporting.

The override is a loan. It records the claim it displaced, and the next look that
finds no rule matching puts that claim back exactly as it was, source and state
together. A prompt can only leave a screen by being painted over, and painting
runs a look, so the pane returns to the ordinary tiers as soon as the prompt is
gone rather than sticking on `needs_input`.

`get-agent-state` reports `screen` as the source while the override stands, so
this is visible rather than magic. Only the daemon's own screen tier can take the
exception, because only it has read the pane: a caller passing `source: screen`
to `set-agent-state` carries no observation and is refused as before.

## Recognising a harness

Before anything can report on a pane, something has to decide the pane is running
an agent at all. The daemon resolves the foreground process group of each pane's
terminal and reads three descriptions of the process, because no one of them is
reliable alone:

| Reading | What it is | How it lies |
| ------- | ---------- | ----------- |
| `comm`  | the name the kernel reports | truncated at 15 bytes, and rewritable by the process (Gemini CLI reports `MainThread`) |
| `argv`  | the command line | names an interpreter, not the agent, whenever one is used |
| `exe`   | the resolved executable | a version number rather than a name for installers that keep one binary per release |

A manifest in `internal/harness/manifests` matches on any of them. `comm` and
`argv0` match a base name, `exe_glob` matches the executable path, and
`argv_path` matches a package name in the path of the script an interpreter
runs.

### A directory name is not a program name

Only the process's own identity counts: its name, its `argv[0]`, its
executable, and for an interpreter the script it was asked to run. No other
directory in any of those paths is read. A deploy script under `~/claude/`, a
tool under `~/dev/codex/` and a binary built under `~/dev/claude-code/` were all
agents to the shipped matcher, which scanned every path component of the
executable and of the run token against the name list; none of them is one.

`argv_path` is the one predicate that reads a directory, and it reads a package
directory: the name must sit right after `node_modules`, `site-packages` or
`dist-packages` (or after an npm scope that does), or be an npm scope itself
(`@openai/codex`), or be the whole token (`npx opencode@latest`). So
`node_modules/@anthropic-ai/claude-code/cli.js` is Claude Code and
`~/dev/crush/scripts/build.sh` is a script in a checkout. `explain-agent-detect`
lists every such word it saw and did not count.

### Behind a wrapper

The foreground process group leader is not always the agent. `sh -c 'claude;
true'`, a wrapper script that does not `exec`, `timeout 600 claude`, `npx
@openai/codex`, `uvx aider-chat`, `mise exec -- opencode` and `nix develop -c
claude` all leave a shell, an interpreter or a launcher as the leader with the
agent as a child of it. When the leader is not an agent but is one of those, the
detector reads the other members of its foreground process group, depth first
and bounded (24 processes, 4 levels), and attributes the pane to the first agent
it finds. `explain-agent-detect` names the wrapper chain. A leader that is
neither an agent nor a wrapper, an editor say, ends the search: its children are
not its identity. On Linux the walk reads `/proc/<pid>/task/*/children`; on
macOS one `kern.proc.pgrp` sysctl lists the group. A process in another process
group, a background job, is never read.

Not covered by the walk: an agent in a container or over `ssh`, whose process
is not a descendant of the pane, and an agent run under `go run` or `cargo run`,
since build tools are not walked. For those, name the harness on the wrapper:

```sh
TUIOS_AGENT=claude-code docker run -it sandbox claude
TUIOS_AGENT=codex ssh devbox codex
```

When neither the foreground process nor anything behind it is recognised, the
detector reads `TUIOS_AGENT` from that process's environment (`/proc/<pid>/environ`
on Linux, `kern.procargs2` on macOS) and, if it names a manifest by id or by
program name, attributes the pane to that harness with identity `hint`, so its
screen and title rules run. A real agent binary always wins over the hint, a
value naming no manifest is ignored, and a process whose environment cannot be
read (another user's, or one of macOS's own platform binaries) has no hint.
Only this one variable is read. `explain-agent-detect` reports the match as
"named by TUIOS_AGENT=<id> on pid N". Set it per command, not in the pane's
shell profile, or every program the pane runs is taken for that agent.

### Losing an agent

A held claim clears at once when the pane's own shell is back in the
foreground, which is the agent exiting. Any other program in the foreground, an
editor or a pager the agent opened, is counted, and the claim clears after six
consecutive ticks of it (twelve seconds at the default interval). One missed
read is not an exit.

### argv is read only for an interpreter, and only one token of it

`argv_path` is the one predicate that reads the command line, so it is the one
predicate that is gated. A process that names itself is described by its own
name; only a stand-in for another program has any reason for its arguments to be
treated as identity. So `argv_path` is consulted only when `comm` or `exe` is a
known interpreter (`node`, `python3`, `npx`, `bun`, a shell, and so on), and even
then it sees a single token: the first non-flag argument, skipping a runner
subcommand so `bun run x` names `x`.

Anything looser mislabels panes. Scanning every argument for the substring
`/opencode/` makes `tail -f ~/dev/opencode/main.go` an agent, and
`python3 -m pytest tests/aider/test_x.py` is a test run in aider's own repository,
not aider. Mislabelling an unrelated pane is worse than missing a real agent, so
the token an interpreter was actually handed is the only place a name in `argv`
is taken to mean anything.

`argv_path` compares path components rather than substrings, so `/opencode/` does
not match `opencode-legacy`. Its last component also accepts a version pin, so
`npx opencode@latest` still resolves.

### exe_glob matches components

`*` and `?` stay inside one path component, `**` spans any number, and a pattern
that does not start with `/` matches any suffix of the path. So `**/claude` and
`*/claude` both match `/usr/bin/claude`.

### Corroborating a short name

A name is not always enough to act on. `pi` is a coding agent, and also a
plotting tool, a pi calculator and a plausible alias. A manifest can demand
evidence beyond the name with a `[detect.require]` block, which constrains
`comm` and `argv0` only:

```toml
[detect.require]
exe_base = ["node", "nodejs", "bun", "deno"]
exe_glob = ["**/pi-coding-agent/**"]
```

pi runs as `comm=pi`, `argv=["pi"]`, `exe=.../node/bin/node`, so the Node runtime
behind the name is what distinguishes it. A process whose executable cannot be
read fails the requirement: silence is not evidence. Any manifest matching on a
name shorter than five characters must carry such a block.

### Platform support

Linux reads all of this from procfs. macOS reads it from two sysctls,
`kern.proc.pid` for the terminal's foreground process group and `kern.procargs2`
for the executable path and arguments; both are readable by an ordinary user for
their own processes, and neither needs cgo or a subprocess. Platforms with
neither report no foreground process, so auto-detection simply has no opinion and
a harness reporting for itself still works.

### Seeing what the detector saw

```
tuios explain-agent-detect                 # the focused pane
tuios explain-agent-detect -w build --json
```

It leads with a verdict in plain words and the evidence it rests on:

```
This pane runs claude-code behind timeout.
  The foreground process timeout is a wrapper, so tuios read the processes behind it.
  The process claude matched the manifest claude-code on comm=claude.
  A process name is strong evidence.
```

or, for a pane that is not an agent, every word it saw and did not count:

```
This pane does not run an agent. The foreground process is build.sh.
  The process build.sh is a wrapper. None of the 1 processes behind it is an agent.

words tuios saw and did not count:
  The argument "/home/u/dev/crush/scripts/build.sh" contains the word "crush". A word inside an argument is not evidence.
```

Then the `comm`, `argv` and `exe` the daemon read, whether the process counted
as an interpreter and which token was eligible to name an agent, the processes
read behind a wrapper, and every manifest in lookup order: which one matched and
on which predicate, and for each that refused, what it was comparing against.

## Screen rules

An agent waiting on a human is the state that matters most and the hardest one
to hear about. Measured under a real PTY, Claude Code sitting on a permission
prompt paints the question once and then emits nothing at all: no further
output, no title, and no progress sequence. Every contractual channel carries
silence, so the only place the fact exists is the painted screen.

A harness manifest may therefore carry screen rules, matched against the bottom
of the pane. They report as `source: screen`, below both a harness reporting for
itself and an escape sequence it emitted (except when one of them has gone stale
with a prompt on the pane, see [the one exception](#the-one-exception-a-visible-blocker)),
and a rule that stops matching returns
no opinion rather than falling back to a state. `needs_input` rules ship for
every harness that has a stable prompt. `working` and `idle` rules ship for
Claude Code, Codex, Gemini CLI and opencode, after herdr's manifests, so an
unhooked pane of one of those can say it is back at its prompt rather than
drifting to `unknown` on the silence timer.

Rules run when a pane writes, throttled, plus once more shortly after it goes
quiet, because the prompt is painted by the last chunk before the silence. A pane
that stays silent costs nothing: there is no ticker.

### Regions

A rule reads the pane's tail by default. It may name a `region` instead:

| Region             | What the rule reads                                        |
| ------------------ | ---------------------------------------------------------- |
| `tail` (default)   | The bottom `lines` non-empty lines                         |
| `prompt_box`       | The lines between the last two border lines of the tail    |
| `above_prompt_box` | Everything in the tail above that box                      |

A border line is a run of at least three box-drawing dashes (`─` or `━`),
optionally opened by a corner (`╭`, `╰`, `┌`, `└` and the like). Claude Code
draws its prompt between two bare dash rules and Gemini CLI inside a rounded
box, and both are found. A box region on a screen with fewer than two border
lines is empty, and a rule reading it matches nothing. `explain-agent-screen`
reports each rule's region, and `no_region` for a rule whose region is not on
the screen.

### Idle rules

No evidence is not rest, so an `idle` rule has to prove the agent is at its
prompt. The loader refuses an idle screen rule unless it reads
`region = "prompt_box"` or carries a `regex` that pins the input box's own
structure (opencode's closing edge, Codex's `›` composer at column zero). Every
bundled idle rule is also outranked by every `working` and `needs_input` rule of
its manifest, because the prompt box stays on the screen during a turn.

An idle reading is then held before it is published:

- A pane that is `working` moves to `idle` only when the reading holds on three
  further looks at least 100 ms apart, or has held for 700 ms, whichever comes
  first. The daemon schedules those looks itself. A look that reads anything
  else cancels the wait, so a frame that shows the box between two spinner
  frames never flaps the pane.
- For 3 seconds after a harness is first seen in a pane, no idle reading counts:
  a TUI that is starting paints its frame in pieces.
- A pane in any other state takes the idle at once.

An idle reading also gives way to a louder reading from the other tier: a rest
glyph in the title does not hide a permission prompt on the screen, and an empty
prompt box does not hide a spinner in the title. A spinner in the title does not
hide a permission prompt either: when the screen reads `needs_input`, the look
drops a `working` title reading rather than applying it first and leaving the
prompt to argue with a claim stamped a moment earlier. When a look later finds no rule
matching at all, the screen stops defending an idle it took, and the pane goes
back to the tiers that handled it before.

These are herdr's numbers. An idle rule in a user manifest goes through the same
gate, which is a change: before it, a user's idle screen rule was published on
the first look.

### A rest claim gives way to a prompt at once

The visible-blocker exception below normally waits two seconds for a stale claim
to refresh itself. A claim that says the agent is at rest (`idle` or `unknown`)
gets no such wait: a rest glyph or a cleared progress bar is not a source midway
through describing a new prompt, and the settle look that sees the prompt runs
well inside two seconds.

### Seeing what a rule would match

Writing a rule against text nobody can see is guesswork, so there is a command
for it:

```
tuios explain-agent-screen                              # the focused pane
tuios explain-agent-screen -w build --harness codex     # try another harness's rules
tuios explain-agent-screen --lines 20 --json            # look further up
```

It prints the pane's tail exactly as the classifier reads it, then every rule of
the harness, which one fired, and for each rule that refused, which of its
strings was the reason. `--harness` runs a harness's rules against a pane nothing
has claimed, which is the case when the rule being written is the one that would
attribute it.

## Title rules

The window title is the other thing an agent publishes about itself, with OSC 0
or OSC 2, and tuios kept the string for the window's name without ever reading
it. Codex writes `Action Required` there when it is waiting on a person. Claude
Code puts a spinner there while it works.

A manifest may carry a `[title]` block, the same rule shape as `[screen]`:

```toml
[title]
enabled   = true
fold_case = true

[[title.rule]]
state    = "needs_input"
priority = 10
kind     = "approval"
message  = "Codex says an action is required"
any      = ["action required"]
```

Two things bound what a title rule may do, and both are about what a title can
honestly prove.

**It never creates a claim.** A title proves that something set a title, not
that the something is an agent, and any program can set any string. A pane that
no other tier has recognised has nothing here to move. Title rules only change
the state of a pane already attributed to a harness.

**A substring has to match a whole token.** The screen tier matches anywhere,
because a rendered frame is prose. A title is mostly paths, branches and program
names, so matching anywhere finds an agent's name inside words that are not it:
a rule for `opencode` would match a pane sitting in `~/src/opencode-blinker`,
and the false positive would arrive wearing the right label. Letters, digits,
`_`, `-` and `.` continue a token; everything else ends one. A predicate that
carries its own boundary, like `action required:`, is matched plainly at that
end.

Title rules report as `source: osc`, because that is what they are: an escape
sequence the program emitted about itself, alongside the progress sequence
already read there.

Three ship enabled. Codex writes `Action Required` when it blocks and a braille
spinner while a turn runs. Claude Code writes a spinner while a turn runs and a
`✳` at rest (`✳ Claude Code`, measured on 2.1.280). Gemini CLI writes its status
after a glyph: `Action Required`, `Working` and `Ready`. A spinner proves
animation, not work, which is why a title rule only moves a pane some other tier
attributed, and why the silence timer still demotes a pane that stops drawing:
the timer's own last look ignores a `working` title, because a spinner that has
not turned for the whole stall window is a frame left behind, not an answer.
An idle title rule goes through the same confirmation gate as an idle screen
rule. `tuios explain-agent-screen` prints the pane's title and what the title
rules made of it beside the screen half, which is the way to write one.

## Notification rules

A harness that wants its user sends a desktop notification with OSC 9, OSC 777
or OSC 99. The daemon now reads them. Every one is published on the event
stream as a `notification` event carrying `title` and `body` (each capped at 512
bytes), for panes in any session, attached or not. One from a pane attributed to
a harness is also matched against the manifest's `[notify]` block, which has the
shape of `[title]` but matches substrings anywhere, as prose. The title and body
are read as one text, title first.

```toml
[notify]
enabled   = true
fold_case = true

[[notify.rule]]
state    = "needs_input"
priority = 10
kind     = "approval"
any      = ["approval requested", "wants to edit"]

[[notify.rule]]
state    = "done"
priority = 0
regex    = ['\S']
```

A notify rule may say `done`, which no screen or title rule may: the
notification is the harness speaking, and "the turn finished" is a claim only
the harness can make. The claim is `source: osc`, and its message is the
notification's own words, fronted by the rule's kind. Because a notification is
sent once and never repeated, its claim goes stale the moment the pane writes
again: from then on a title or screen look may replace it, so an approval given
and followed by work does not leave the pane on `needs_input`.

Claude Code (`needs your permission` as an approval, `waiting for your input` as
`idle`) and Codex (`approval requested` as an approval, anything else as the
turn finishing) ship rules. A state change from a notification reaches the rail,
the alert policy and the `after-agent-state` hook like any other, so a harness
asking for approval in a session nobody is attached to still runs the hook.

## The stall heuristic

Agents that do not report get a conservative fallback. If a pane reported
`working` but then produces no output for a while, the daemon demotes it to
`idle`, on the assumption that a genuinely busy agent produces output.

Silence alone is not enough to act on, because an agent that finished and an
agent waiting on a human produce exactly the same silence, and `idle` reads as
"finished and fine". So before demoting a pane, the daemon hands it to the screen
tier for a last look, and leaves alone any pane whose screen answers. A look that
finds nothing still demotes, to `unknown`: the screen was read and said nothing,
which is as much evidence as there is going to be, and none of it says the pane
is at rest. Only a daemon with no screen tier at all writes `idle`.

The fallback is strictly secondary to explicit reporting:

- It only ever moves a pane out of `working`, and only ever into `unknown` (or
  `idle` with no screen tier). Any other state (`needs_input`, `done`,
  `errored`) is never touched, so an explicit report is never overridden.
- The silence clock is the later of the pane's last output and the time its
  `working` state was set, so a working report is given the full window before it
  can be demoted, and output keeps a pane looking busy.
- It never promotes a pane into `working`; only an explicit report does that.

The silence window defaults to 30 seconds. Override it with the
`TUIOS_AGENT_STALL_SECONDS` environment variable when starting the daemon; set it
to `0` (or a negative value) to disable the heuristic entirely.

## Finished turns

`done` comes only from an explicit report or the Claude transcript, so an
unhooked pane never used to say it had finished. The daemon now counts turns:
each window carries a `completion_seq` that goes up by one every time its state
moves from `working` to `idle`, `done` or `unknown` after at least 5 seconds of
work. An explicit `done` counts however short the turn. `needs_input` in the
middle of a turn does not end it, and `errored` is not a finished turn. When the
silence timer ends a turn, the turn is measured to the pane's last output rather
than to when the timer noticed, so a redraw that bumped a detected pane to
`working` for an instant is not counted.

The count rides the window state (`completion_seq`, additive, omitted when
zero) and the session listing, so an older client ignores it. `list-agents`
reports `completion_seq` and `finished_unread`: the daemon's own view, true while
the pane is at rest and has finished a turn since an attached client last pushed
state with it focused.

"Has this person looked at it" is per client, so the rail keeps its own record:
the count each pane had when this client's user last focused it, saved beside
the other window-keyed rail state. A pane at rest (`idle` or `unknown`) whose
count has moved past that is drawn exactly as an unread `done` pane is, and
focusing it puts it back to its own state. A turn that ends in the focused pane
counts as seen.

## Indicator

tuios draws a one-cell glyph in each window's title:

| State         | Indicator |
| ------------- | --------- |
| `working`     | `●`       |
| `needs_input` | `▲`       |
| `idle`        | `○`       |
| `done`        | `■`       |
| `errored`     | `×`       |
| `unknown`     | `□`       |
| `none`        | (nothing) |

The glyphs are distinct shapes rather than the same shape in different colors, so
the state reads at a glance and survives a monochrome capture. The indicator
shows even for a window with no name. With `--ascii-only` the glyphs are `*`,
`!`, `o`, `#`, `x` and `?`.

This table used to say `unknown` draws nothing. It has drawn `□` since the state
was given a glyph, because a pane with an agent in it that drew nothing read as
a pane with no agent.

## The rail's agents section

The agents section of the session rail lists every pane running an agent, in
every session, and is ordered by what each one needs from you. The header's
sort control (`o` with the rail focused, or a click on it) steps through three
orders: `you`, `pri` and `rec`.

`you`, the default, draws four groups, top to bottom:

1. **Needs you**: `needs_input` (an approval or a question) and `errored`.
2. **Finished**: `done` that you have not looked at yet.
3. **Working**: `working`.
4. **At rest**: `idle`, `unknown`, `done` you have already looked at, and any
   state this build does not know.

Inside a group the rows keep spawn order: sessions in the order the daemon made
them (or the order you dragged them into), panes in the order they were opened.
A row moves only when its group changes, which is when what it wants from you
changed. It never moves because a neighbour did something.

`pri` is the older order, which was the default before `you` existed: errored,
then needs_input, working, done unread, done read, idle. `rec` is newest state
change first. A rail whose saved state names `pri` keeps it; only a rail that
never picked an order moves to `you`.

The collapsed strip lists its agents in the same order as the section.

### What a row says without colour

Colour is never the only signal. Every state has its own glyph (the table
above), and a finished pane you have looked at draws `○`, idle's glyph, in the
rail, where it used to draw `■` in a muted colour: read and unread finished
panes differed only in ink. The title bar still draws `■`, because it has no
unread bit to show.

The row's second line carries a word for what the row needs, the `need` token:
`approval` or `question` when a screen rule read the prompt (the kind is taken
off the front of the message so it is said once), `needs input`, `errored` or
`finished` when the pane reported no message of its own. A row that needs you
also shows how long it has waited: at the right edge of the first line when the
rail is wide enough for the elapsed column, and after the need word otherwise
(`approval 12m`, or `waiting 12m` when the message stands in for the word).

Every in-flight state draws the one working glyph, `●`, whichever source
reported it (a hook, an OSC 9;4 progress report, a screen rule or the process
detector). What the agent is doing goes on the second line, from its message.

### Agent metadata

A pane can report short facts about its agent with `set-agent-meta`: the
model, how full its context is, the cost of the turn, a one-line summary.

```sh
tuios set-agent-meta -w "$TUIOS_PANE_ID" --source statusline --ttl 60s model=opus context=42%
```

The `meta` row token draws every key on the second line, values only, in the
order the pane first reported them, so write values that read on their own
(`42% ctx` rather than `42`). `$name` places one key, and `meta` then leaves
that key out:

```toml
[appearance.sidebar.agent_row]
tokens = ["session", "need", "harness", "name", "elapsed", "$context", "meta", "message"]

[appearance.sidebar.agent_row."$context"]
fg = "warning"
```

Metadata is display only. It never changes a state, a wait, an alert or a
message. It is capped at 16 keys per call and 32 per pane, values are cut to 80
characters with control characters removed, keys set with a TTL are dropped by
the daemon when it runs out, and everything clears when the agent leaves the
pane. `get-agent-state` and `list-agents` report it as a `meta` object. No
harness feeds it yet; a hook or a statusline command is where a feed goes. See
[the protocol reference](protocol.md#set-agent-meta).

## Harness integrations

A harness with a hooks system reports its own state, which outranks everything
tuios can work out by looking. tuios wires four of them itself:

```sh
tuios integration install claude-code   # or codex, gemini-cli, opencode, or --all
tuios integration status                # installed and current, per harness
tuios integration uninstall codex
tuios doctor agents                     # PATH, install state, and panes missing theirs
```

| Harness     | What is written                                     | Format source |
| ----------- | --------------------------------------------------- | ------------- |
| Claude Code | `hooks` in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR`) | [hooks reference](https://code.claude.com/docs/en/hooks) |
| Codex       | `~/.codex/hooks.json` (or `$CODEX_HOME`)            | [Codex hooks](https://developers.openai.com/codex/hooks) |
| Gemini CLI  | `hooks` in `~/.gemini/settings.json`                | [hooks reference](https://geminicli.com/docs/hooks/reference/) |
| opencode    | `plugins/tuios-agent-state.js` in `~/.config/opencode` (or `$XDG_CONFIG_HOME/opencode`) | [plugins](https://opencode.ai/docs/plugins/) |

Every hook entry runs `tuios agent-hook <harness> --integration <version>`. The
version marker is how a later install replaces an older entry, how uninstall
finds exactly what tuios wrote, and how status tells current from out of date.
The installer keeps everything else in the file, in its order and with the
user's own text as written (`&&`, `<` and `>` in a hook command are not
escaped), replaces the file atomically, and writes nothing when nothing
changed. The first time it rewrites a file it keeps the file as it was as
`<file>.tuios.bak`, and later writes leave that copy alone, so it is always the
file from before tuios touched it. A settings file that is a symlink, as a
dotfile manager leaves it, stays a symlink: the file it points to is the one
rewritten. A symlink to a missing file is refused. It refuses a file it cannot parse rather than
rewrite it, and refuses when the harness's configuration directory does not
exist yet (run the harness once first). `--command` names the program the hooks
run when `tuios` is not on the harness's PATH.

Codex gets hooks rather than the older `notify` command: `notify` takes one
command only, so it would replace a user's own, and it reports only that a turn
finished. Hooks are on by default in Codex; `status` notes a `config.toml` that
turns them off with `[features] hooks = false`. `tuios agent-hook codex` still
reads a `notify` payload, so a hand-wired `notify` reports `done`.

### What each event reports

`tuios agent-hook` reads the payload on stdin (the Codex `notify` payload
arrives as the last argument) and sends one `set-agent-state`, or nothing.

| Claude Code event | Reports |
| ----------------- | ------- |
| `SessionStart` | `idle`, with the session id and transcript path. `source: compact` reports nothing |
| `UserPromptSubmit`, `PreToolUse` | `working` |
| `PermissionRequest` | `needs_input`, kind `approval`, message `approve <tool>: <command or path>` |
| `PostToolUse`, `PostToolUseFailure`, `PermissionDenied`, `ElicitationResult` | `working`, only if the pane is `needs_input` |
| `Notification` `permission_prompt` | `needs_input`, kind `approval` |
| `Notification` `elicitation_dialog`, `elicitation_url_dialog`, `agent_needs_input` | `needs_input`, kind `question` |
| `Notification` `idle_prompt` | `idle`, only if the pane is `working` or `unknown` |
| `Notification` `auth_success` and the rest | nothing |
| `Stop` | `done` |
| `StopFailure` | `errored`, message `stopped on <error_type>` |
| `SessionEnd` | `none` |
| `SubagentStop`, anything with `agent_id` | nothing |

Codex maps the same events the same way, plus `Interrupt` to `idle`. Gemini CLI
maps `BeforeAgent` and `BeforeTool` to `working`, `AfterTool` to `working` only
from `needs_input`, `Notification` `ToolPermission` to `needs_input` kind
`approval`, `AfterAgent` to `done`, and `SessionStart` and `SessionEnd` as
above. The opencode plugin maps `session.status` busy and `chat.message` to
`working`, `permission.asked` to `needs_input` kind `approval`,
`question.asked` to kind `question`, the replies to `working` from
`needs_input`, `session.idle` to `done`, `session.error` to `errored`, and
drops every event from a child session.

`idle_prompt` does not report `done`: `Stop` already did, and `done` has to stay
so the person still sees that the turn finished. It only corrects a pane still
showing `working` after a `Stop` that never arrived.

Nothing defaults. A payload that is not a JSON object, an empty stdin, an event
with no mapping and a notification type it does not know all report nothing,
and an explicit `--explain` says why on stderr. This replaces the old shim,
which read every `Notification`, `auth_success` included, as `needs_input`, and
needed `python3`.

A `needs_input` message is cut to 100 characters, with whitespace collapsed and
anything that looks like a credential replaced by `***`: `NAME=value` where the
name says token, secret, password or key, `--token value`, `Bearer` headers,
credentials in a URL, and long runs mixing letters and digits. The message
leaves the pane: it reaches every attached client, alerts and
`after-agent-state` hooks.

### Finding the pane

A hook is a child of the harness, which usually carries the pane's environment.
The pane is taken from, in order:

1. `--window` (and `--session`) on the command line.
2. `TUIOS_PANE_ID` (and `TUIOS_SESSION`).
3. The hook process's session id. A pane's shell leads the session of the
   pane's terminal, so every process whose controlling terminal is the pane
   shares that id, however deep. The daemon's `resolve-pane` verb matches it.
4. The hook process's parent chain, for a process that left the terminal's
   session: the first ancestor that is a pane's shell names the pane.

Steps 3 and 4 are what keep a harness or a sandbox that scrubs the environment
reported for. Only panes on the daemon's own machine are matched.

### Nested and foreign events

Hooks are configured per user, so they fire for every harness process, not only
the one that owns the pane. Three filters keep those events off the pane:

- A subagent's events (`agent_id` set, `SubagentStop`, opencode child sessions)
  are dropped by the reporter.
- An event from a harness other than the one `TUIOS_AGENT` names is dropped by
  the reporter. So is a Claude Code hook that Cursor runs (it reads the same
  hook configuration), and a Codex hook whose session is not the
  `CODEX_THREAD_ID` it inherited.
- The daemon refuses a report whose `agent_session_id` differs from the pane's
  while the pane's own harness is `working` or `needs_input` by its own report,
  and one from a different harness in the same case. That is the `claude -p` a
  tool call started inside the pane: without this its `Stop` would mark the pane
  `done` mid-turn. At rest a new session takes the pane over, as `/clear`,
  `/resume` and a restart should, and the pane's `agent_session_id` becomes the
  new one.
- Mid-turn, a new session from the same harness process also takes the pane
  over. Claude Code fires no `Stop` when you interrupt a turn with Esc, so the
  pane stays `working`, and a `/clear` or `/resume` after that starts a new
  session id in the same process. Without this exception every report for the
  new session would be refused until the harness exits. The hook sends the
  harness's pid as `harness_pid`: the nearest ancestor of the hook process that
  is not a shell or `env`. A nested run is a process of its own, so it is still
  refused. A hook that cannot read its ancestors (Windows, or a sandbox with its
  own pid namespace) sends no pid and gets the plain rule.

### Session identity

The session id a hook reports is stored on the pane as `agent_session_id`,
returned by `get-agent-state` and `list-agents`, and persisted with the
session, so the conversation a pane last ran can be resumed after the agent,
or the daemon, restarts. It is kept when the agent exits and replaced when
another session reports into the pane.

The transcript path is not stored anywhere a client can read. It goes straight
to the transcript source: for a harness whose manifest has a transcript reader
(Claude Code today), the pane is joined to exactly that file, which replaces the
search that refuses whenever two files in one directory could both be the
pane's.

### Failure behaviour

Harnesses run some hooks synchronously, `PreToolUse` and `PermissionRequest`
among them. `tuios agent-hook` exits 0 whatever happens, prints nothing a
harness could read as an answer (Gemini CLI, which parses stdout, gets `{}`),
and gives up after 500 ms (`--timeout`) when the daemon is slow, restarting or
gone.

A daemon keeps running across a tuios upgrade, so a new hook talking to an
older daemon is the ordinary case right after one. Such a daemon does not
reject the hook fields: it ignores a param it does not know and applies the
report without it. So before it reports, the hook asks `list-verbs` which
params `set-agent-state` takes, and sends only those. A report with `if_state`
is not sent at all to a daemon without it, because applied unconditionally it
would do what the condition rules out: Claude Code's `idle_prompt` would turn
`done` into `idle` a minute after every turn, and a late `PostToolUse` would
turn `done` back into `working`. `--explain` lists the fields it left out as
`unsupported`. `tuios set-agent-state --if-state` makes the same check and
fails rather than send the report without its condition.

### The old shim

`integrations/claude-code/tuios-agent-state.sh` is now a wrapper that runs
`tuios agent-hook claude-code`, so settings that point at it get the new map.
Wired alongside an installed integration it reports every event twice;
`status` and `doctor` say so. See
[integrations/claude-code](../integrations/claude-code/README.md).

## Environment

When tuios spawns a pane it exports the environment a state-reporting shim needs:

| Variable          | Meaning                                          |
| ----------------- | ------------------------------------------------ |
| `TUIOS_ENV`       | `1` when running under tuios                      |
| `TUIOS_SOCKET`    | Daemon socket path                               |
| `TUIOS_PANE_ID`   | The pane's window id                             |
| `TUIOS_WINDOW_ID` | The pane's window id (alias of `TUIOS_PANE_ID`)  |
| `TUIOS_SESSION`   | The session name                                 |

A shim guards on these and no-ops when they are unset, so it is safe to leave
wired up outside tuios. `tuios agent-hook` uses `TUIOS_PANE_ID` and
`TUIOS_SESSION` when they are set and finds the pane from its process otherwise
(see [Finding the pane](#finding-the-pane)).

One variable goes the other way: `TUIOS_AGENT` is set by you, on a wrapper, to
name the harness it runs. See [Behind a wrapper](#behind-a-wrapper).

## Alerts

A state change can raise a notification, an audible cue or a bell, a clickable
dock message, and a shell command of your choosing. What fires, for which
transitions, and when it is held back is the `[notifications.agent]` table; see
[the configuration reference](https://tuios.gaurav.zip/docs/configuration#notifications) for the keys
and [HOOKS.md](HOOKS.md) for the command contract.

Two things are worth knowing here rather than there. The notification is an
in-band escape sequence written into the same stream the interface is drawn
through, so it reaches whatever terminal is in front of you even when the session
is on another machine; a desktop notification raised by tuios would appear on the
host running the daemon, which under `tuios ssh` is not where you are. The same
is true of the audio cue, which is played by the client through a system audio
player, so over `tuios ssh` it comes out of your laptop rather than the host. And
alerts are raised by an attached client, so a detached session tracks state
without announcing it.
