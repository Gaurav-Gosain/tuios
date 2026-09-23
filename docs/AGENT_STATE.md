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
- [The Inbox](#the-inbox)
- [Answering a prompt without attaching](#answering-a-prompt-without-attaching)
- [Harness integrations](#harness-integrations)
- [Typing a prompt](#typing-a-prompt)
- [Environment](#environment)
- [Alerts](#alerts)
- [Who can act as the person](#who-can-act-as-the-person)

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

Once `ask-agent` or `fan` has typed a prompt and sent Enter, the state is also
how tuios knows the prompt was taken. The pane has five seconds to turn
`working` or `needs_input`, or to finish a turn (`completion_seq` goes up). A
pane whose harness has no screen or title rule that reports `working` can show
it by printing anything instead. For a harness that has one, output is not
enough, because a TUI that read Enter as a newline redraws its input box too. A
pane that shows none of this is stalled: `ask-agent` fails with
`prompt_stalled` and `fan` records `prompt_status: stalled`. See
[protocol.md](protocol.md).

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
every harness that has a stable prompt, and `working` rules for every harness
whose chrome shows a turn in progress. `idle` rules ship where the idle screen
was measured here (Claude Code, opencode) or where herdr ships an idle rule
from its own live pane reads (Codex, Gemini CLI, Cline, Devin, Grok, Kiro,
Maki, Qwen Code), so an unhooked pane of one of those can say it is back at its
prompt rather than drifting to `unknown` on the silence timer. Most bundled
rules are ported from herdr's manifests (Apache-2.0, see
`internal/harness/manifests/LICENSE-herdr`), and each manifest names the herdr
version it follows. Aider and Crush have no screen rules: herdr has no manifest
for them and none has been written against a live session.

Every bundled screen rule decides at least one captured or derived screen under
`internal/harness/testdata/screens`, and the test suite fails for a rule that
decides none. Each screen says in its header whether it was measured on a live
pane or derived from herdr's manifest and the chrome it quotes.

Rules run when a pane writes, throttled, plus once more shortly after it goes
quiet, because the prompt is painted by the last chunk before the silence. A pane
that stays silent costs nothing: there is no ticker.

### Regions

A rule reads the pane's tail by default. It may name a `region` instead. The
names are herdr's where the meaning is the same, so a ported rule keeps its
region:

| Region                            | What the rule reads                                     |
| --------------------------------- | ------------------------------------------------------- |
| `tail` (default, or `whole_recent`) | The bottom `lines` non-empty lines                    |
| `bottom_non_empty_lines(N)`       | The last N lines of the tail                            |
| `prompt_box` (or `prompt_box_body`) | The lines between the last two border lines of the tail |
| `above_prompt_box`                | Everything in the tail above that box                   |
| `last_non_empty_above_prompt_box` | The one non-empty line just above that box              |
| `after_last_horizontal_rule`      | Everything in the tail under the last border line       |

A border line is a run of at least three box-drawing dashes (`─` or `━`),
optionally opened by a corner (`╭`, `╰`, `┌`, `└` and the like). Claude Code
draws its prompt between two bare dash rules and Gemini CLI inside a rounded
box, and both are found. A box region on a screen with fewer than two border
lines is empty, and a rule reading it matches nothing.
`after_last_horizontal_rule` on a screen with no border line is the whole tail,
as it is in herdr. It is the region that keeps an answered prompt still in the
tail from holding `needs_input`: Claude Code draws a live form under a rule, and
once the form is answered the rule and the form scroll up together.

N in `bottom_non_empty_lines(N)` is a plain number from 1 to 200 and must not be
more than the manifest's `lines`: the loader refuses a rule that asks to read
further up than the manifest reads, rather than widening what every other rule
of the manifest sees. `explain-agent-screen` reports each rule's region, the text
a rule reading a narrower region saw there, and `no_region` for a rule whose
region is not on the screen.

### Predicates

A rule names what must be on its region, and every predicate must hold:

| Field       | Holds when                                          |
| ----------- | --------------------------------------------------- |
| `all`       | every string is present                             |
| `any`       | at least one string is present                      |
| `not`       | no string is present                                |
| `regex`     | every pattern matches                               |
| `not_regex` | no pattern matches                                  |
| `all_of`    | every nested group matches                          |
| `any_of`    | at least one nested group matches                   |
| `none_of`   | no nested group matches                             |

A nested group has the same fields as a rule's own, so it nests the same way,
and it is written as an inline table:

```toml
[[screen.rule]]
state    = "needs_input"
priority = 25
region   = "after_last_horizontal_rule"
all      = ["esc to cancel"]
any_of   = [
  { all = ["enter to confirm"] },
  { all = ["enter to select"], any = ["↑/↓ to navigate", "arrow keys to navigate"] },
]
none_of  = [ { all = ["auto-approved", "yes"] } ]
```

This is how herdr writes its rules, and it is what a flat rule could not say:
"this footer, and one of these three layouts", or "unless these two words appear
together". A group inside `all_of` or `any_of` must name something that has to be
present; a group inside `none_of` may be vetoes only, but not empty. Groups nest
at most eight deep, and one manifest carries at most 512 groups and 1024 strings
and patterns, so the scan the daemon runs on every settle stays priced.

Substrings are matched plainly, lowercased when the manifest sets `fold_case`.
Patterns are RE2 with `^` and `$` anchoring lines, and choose their own case
handling with `(?i)`. A folded substring is a plain search, while a `(?i)`
pattern over a screen of box drawing costs microseconds, so a bundled rule writes
case-insensitive text as a substring. A rule is tried only if it could still
win: rules run highest priority first, and the first match decides.

### Idle rules

No evidence is not rest, so an `idle` rule has to prove the agent is at its
prompt. The loader refuses an idle screen rule unless it reads
`region = "prompt_box"` or carries a pattern on every path to a match that pins
the input box's own structure (opencode's closing edge, Codex's `›` composer at
column zero, Maki's mode label alone on the status bar). A pattern in the rule's
own `regex`, or in any `all_of` group, counts; one in an `any_of` group counts
only when every group of that `any_of` has one. Every bundled idle rule is also
outranked by every `working` and `needs_input` rule of its manifest, because the
prompt box stays on the screen during a turn. herdr ranks some idle rules first
(Kiro's composer placeholder); here they rank last.

A harness with an idle rule, on the screen or in the title, is one whose
`unknown` panes are not ready to be asked: it can show it is at its prompt, so a
quiet pane that has not shown it may be mid-call. Amp, Cline, Devin, Grok,
Hermes, Kiro, Maki and Qwen Code joined that set with these rules; see the
protocol changes in [protocol.md](protocol.md).

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
strings, patterns or nested groups was the reason. A rule reading a region
narrower than the tail also prints the text it read there. `--harness` runs a
harness's rules against a pane nothing has claimed, which is the case when the
rule being written is the one that would attribute it. The title rules follow,
with the pane's title and its last OSC 9;4 progress report.

### Your own manifests

A manifest dropped in `$XDG_CONFIG_HOME/tuios/harnesses` (`~/.config/tuios/harnesses`
when the variable is unset), or in the directory `TUIOS_HARNESS_DIR` names, is
loaded when the daemon starts. A new id adds a harness. An id a bundled manifest
already has replaces that manifest whole: its detect, screen, title, notify,
transcript and input blocks alike, and a block the user file leaves out is gone
rather than inherited. There is no merge, because a rule has no name to merge
by and its priority means something only next to the rules around it. To change
one rule, copy the bundled file from `internal/harness/manifests` and edit the
copy.

`tuios doctor agents` lists the manifests loaded from that directory, says which
replace a bundled one, and names every file there that failed to load and why.
`explain-agent-screen` reports `manifest_source` and `replaces_bundled` for the
pane's harness.

To draft a manifest from one of herdr's, run
`go run ./internal/harness/herdrconv path/to/herdr/agent.toml`. It carries
herdr's nested gates and regions as they are, sends title and progress rules to
the `[title]` block, and names every rule it drops and why: an `unknown` rule
(tuios has no "leave the state alone" rule), a region tuios has no equivalent
for (`top_non_empty_lines(N)`, Codex's prompt-marker regions), or an idle rule
without the proof the loader asks for. Over herdr's 22 manifests it carries 128
of 141 rules; before nested gates it carried 100, several of them only
approximated by flattening.

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

Eight ship enabled. Codex writes `Action Required` when it blocks and a braille
spinner while a turn runs. Claude Code writes a spinner while a turn runs and a
`✳` at rest (`✳ Claude Code`, measured on 2.1.280). Gemini CLI writes its status
after a glyph (`packages/cli/src/utils/windowTitle.ts`): `✋  Action Required`,
`⏲  Working…`, `✦  ` and the model's current thought, and `◇  Ready`, each
followed by the folder. Its rules key on the glyph, so a thought that begins
with "Action required" or "Ready" reads as the turn it is. After herdr's
manifests: Amp writes a spinner during a turn, `Plugin confirmation needed`
when a plugin waits and `<thread> - amp - <dir>` at rest; Grok writes `grok` or
`<session> - grok` at rest, a spinner during a turn and `Action Required` when
a permission prompt waits; Hermes puts `⚠`, `⏳` or `✓` in front; Kiro writes a
spinner and `kiro:` during a turn; and Qwen Code, with `ui.showStatusInTitle`
on, writes `✳` when a confirmation waits and `◐` during a turn. A spinner proves
animation, not work, which is why a title rule only moves a pane some other tier
attributed, and why the silence timer still demotes a pane that stops drawing:
the timer's own last look ignores a `working` title, because a spinner that has
not turned for the whole stall window is a frame left behind, not an answer.
An idle title rule goes through the same confirmation gate as an idle screen
rule. `tuios explain-agent-screen` prints the pane's title and what the title
rules made of it beside the screen half, which is the way to write one.

### Progress rules

A title rule may set `region = "osc_progress"` to read the pane's last OSC 9;4
progress report instead of its title, written as herdr keeps it: `4;<state>` for
the states whose percentage means nothing (0 remove, 3 indeterminate) and
`4;<state>;<percent>` for the others (1 set, 2 error, 4 paused). Every pane
already gets the sequence's published meaning (a bar is working, clearing it is
idle, the error state is errored, paused is needs_input). A harness that uses
the sequence its own way, say indeterminate progress for "waiting on you", gets
its own reading: when a report arrives from a pane whose manifest has
`osc_progress` rules and one of them matches the report, the pane's title block
is read through the ordinary look and the published meaning is not applied.
When none matches, the published meaning applies, so a manifest names only the
reports it reads differently. No bundled manifest has one: herdr's progress
rules for Grok, Kiro, Qwen Code and Claude Code agree with the published
meaning.

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
off the front of the message so it is said once) or when a hook reported the
kind with a message that does not name it (`approval · claude · approve Bash:
make`), `needs input`, `errored` or `finished` when the pane reported no
message of its own. On a narrow rail a row that needs you drops the harness
and metadata before it cuts what the pane is asking. A row that needs you
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

## The Inbox

The Inbox is one list of everything waiting for you, in every session on the
daemon: approvals and questions an agent is blocked on, mail an agent wrote to
you, agents that errored, conversations a daemon restart left to resume, and
finished turns you have not looked at. The daemon
keeps it, so it is the same list in every client, in `tuios list-attention`,
and in the `attention` events of `tuios subscribe`. See
[list-attention](protocol.md#list-attention) for the fields and the rules for
when an item opens and closes. In short, an item closes by itself when what
opened it stops being true: the agent leaves `needs_input` or `errored`, the
mail is read, or a client focuses the pane that finished.

A row follows the pane's latest report. A pane that stays on `needs_input` and
reports again with a new kind or message moves between Questions and Approvals
and shows the new message, so a harness hook that says `approval` after the
screen tier already set `needs_input` lands in the right group. It keeps its
place in the order, because the wait did not start again.

On a daemon restart, finished and errored rows whose pane came back are kept.
Approvals and questions are dropped, since the prompt died with its process,
and so is mail, since the messages it points to do not survive a restart.
Resume rows are opened by the restore itself; see
[Resuming after a restart](#resuming-after-a-restart).

Keys, after the prefix (`ctrl+b` by default):

| Key | What it does |
| --- | --- |
| `i` | Open the Inbox. |
| `o` | Go to the oldest item that needs you (approval, question, mail, errored, resume), switching session and workspace. The prefix stays armed, so `o` again goes to the next one, and past the last it starts over. Finished turns are left to the Inbox. |
| `M` | Open the Inbox on its mail. It used to open the mailbox; `m` in the Inbox does that now. |

Inside the Inbox:

| Key | What it does |
| --- | --- |
| `j` / `k`, arrows | Move. Group headings are skipped. |
| `g` / `G` | First and last item. |
| `enter` | Go to the item's pane, switching session and workspace. On mail, open the thread. On a held approval, give the prompt back to the pane first, so the harness shows it there. |
| `space` | On an approval or a question, read the prompt without leaving the Inbox, and answer it from there. See [Answering a prompt without attaching](#answering-a-prompt-without-attaching). Not on a held approval, which has no prompt on the screen: answer that one with `1`, `2` or `3`. |
| `1` / `2` / `3` | Answer the held approval under the cursor: allow once, always allow, deny. The same order as the harness's own menu. Only the keys the prompt offers work, and only once the prompt has been on screen as it is for 0.4 seconds. The whole prompt, and what `2` adds, is shown under the list. See [Approvals from the Inbox](#approvals-from-the-inbox). |
| `r` | Reply to mail: the thread opens with its reply line. |
| `y` | On a resume row: go to the pane and type the conversation's resume command there. |
| `p` | On a row that says `held for NAME`: pass the mail another machine sent that agent on to it. See [Mail held from another machine](#mail-held-from-another-machine). |
| `d` | Dismiss the item. |
| `f` | Show one kind, then the next, then all of them. |
| `m` | Open the mailbox, with every thread including the ones between agents. |
| `esc` / `q` | Close. |

Rows are grouped under headings in words, Approvals, Questions, Mail, Errored,
Resume, Finished, each with its count, and oldest first inside a group. A row carries
its kind's glyph, the pane's name, what it said, and on the right its session
and how long it has waited (`12m`, `3h`). The heading, the name and the wait are
text, so nothing depends on colour, and the ASCII glyph set covers the marks.

A finished turn that ends in the pane you are looking at is dismissed by your
client as soon as it arrives, the rule the rail applies to its own unread mark.
That dismiss is quiet: if it fails, nothing is shown, since you did not ask for
it. A dismiss you ask for with `d` that finds the item already closed, because
another client or the daemon closed it first, also shows nothing, since the item
is gone either way. Any other failure of `d` is shown.

The rail's agents header counts the Inbox while the client is connected to it:
approvals, questions and errored items are `blocked`, finished items are
`done`, over every session on every machine, or only this session when the
filter says `here`. An item of a machine whose link is down is not counted.
Without the Inbox (an older daemon, or while reconnecting) the header counts its
rows, as it did before.

### Other machines

With hosts in the `[hosts]` table, the Inbox is the whole fleet's. The daemon
follows each linked host's Inbox and agents over the link as they change (see
[Following linked hosts](protocol.md#following-linked-hosts)), so an agent
that blocks on `build` is a row here, reading `build:api` on the right, and
raises the same dock message, notification and sound a local one does, naming
the machine. Enter on it attaches that session on `build` in this client and
lands on the pane; on mail it opens the thread there. The prefix then `o`
visits it like any other. `tuios list-attention` lists it as
`build:api/claude`, with the id `build:17`.

When a host's link drops, its rows stay, drawn in the muted ink, with
`seen 3m ago` in place of the wait. They are what that machine said last, so
they raise no alert, are not counted, are skipped by `o`, and enter on one says
the machine cannot be reached rather than trying. The rail keeps the host's
sessions under its header the same way, muted with no agent glyph, and the
header says `seen 3m ago` where it used to say `offline`. A host that answers
and refuses keeps its reason (`no daemon`, `no tuios`, `version`). When the
link comes back the stream resumes where it stopped and the marks clear.

Dismissing a row of another machine with `d` hides it here and marks nothing
on that machine: whether you have dealt with something is a fact about you,
and a second hub, or a client attached on that machine, still sees it. It
comes back when that agent changes it.

The rail no longer polls a host the daemon streams. The daemon pushes each
change to the attached clients, and the rail lists the hosts again on the
push, with one listing a minute as a backstop. While the client is attached
to a session on another machine the push goes to that machine's daemon, not
to this client, so the rail keeps polling every 5 seconds with the rail open
and 30 without. A host whose tuios is too old
to stream its agents is polled as before, every 5 seconds with the rail open
and 30 without, and `tuios hosts` names it with what to update; what waits on
it is not in the Inbox until it is updated.

Who can clear it: `dismiss-attention` needs the nonce the daemon issued in a
client's attach reply, which the Inbox sends and an agent in a pane does not
have, and it runs the same check as a reply from `human` (see
[Who can act as the person](#who-can-act-as-the-person)): a caller inside a
pane is refused even with a live nonce it copied, and where the kernel gives
both pids the caller must be the process that attached. An agent cannot empty
the list the person reads to find out what the agents want. Reading the
person's inbox with `read-agent-messages --to human` marks the mail read, and
the mail item follows it, but only from outside every pane: from a pane that
read is a peek.

What it does not do yet: a client attached to a session on another machine
sees this machine's Inbox and cannot dismiss or answer from it, and the Inbox
does not answer an item of a linked host, with the peek or with `1`, `2` and
`3`. `tuios peek-prompt` and `tuios respond` reach a pane on another machine
by `HOST:SESSION:WINDOW`.

## Answering a prompt without attaching

An agent blocked on an approval or a question can be answered without going to
its pane. The screen rule that put the pane on `needs_input` already knows what
the prompt looks like; a rule can also say which keys answer it, in an
`[answers]` block. With that, the daemon can show the prompt to the person and
press the right key for them.

In the Inbox, `space` on an approval or a question opens the peek: the prompt as
the pane shows it, behind a bar that marks it as the pane's text, its numbered
options, and how long the agent has waited. From there:

| Key | What it does |
| --- | --- |
| `1` to `9` | Choose that option. |
| `a` | Approve. |
| `A` | Approve and do not ask again, when the menu offers it. |
| `d` | Deny. |
| `tab` | Open a line to type an answer into; `enter` sends it, `esc` drops it. |
| `r` | Read the prompt again. |
| `enter` | Go to the pane instead. |
| `esc` / `q` / `space` | Back to the list. |

The hint row lists only the answers the prompt takes now. An answer that lands
closes the peek, and the dock says what was pressed and what the agent did:
`Sent "1" to claude; it is working now`.

On the command line, the same two steps are two verbs:

```bash
tuios peek-prompt -w review                        # the prompt, its options, its answers
tuios respond -w review --prompt-id 75f8b9fadb5b5dfc approve
tuios respond -w review choose 2
tuios peek-prompt -w buildbox:api:review --json    # a pane on another machine
```

### The answers block

Under a `needs_input` screen or title rule:

```toml
[[screen.rule]]
state    = "needs_input"
kind     = "approval"
all      = ["Do you want"]
any      = ["1. Yes", "❯ 1."]

[screen.rule.answers]
approve        = { option = "yes" }
approve_always = { option = "yes, " }
deny           = { keys = ["esc"] }
choose         = "digit"
```

- `approve`, `approve_always` and `deny` each take `option`, `keys`, or both.
  `option` is the start of a numbered option's label, matched without case, and
  the answer is offered only while such an option is on the screen: with
  `option` alone its digit is pressed. `keys` are pressed as they are, once the
  option (when named) is found. A key is `enter`, `esc`, `tab`, `space`, `up`,
  `down`, `left`, `right`, `backspace`, or one printable character; at most 8.
- `choose = "digit"` lets the person pick any numbered option by its number.
- `text = true` lets the person type an answer, which is pasted and submitted
  the way `ask-agent` types a prompt.

Binding an answer to a label is what keeps it safe across menus. Claude Code's
permission menu has `2. Yes, and don't ask again` on some tools and `2. No` on
others; `approve_always = "2"` would deny on the second. Bound to `yes, `, it is
not offered there at all.

A block on a rule that is not `needs_input`, on a notify rule, with a key name
it does not know, with more than 8 keys, with an answer that names neither keys
nor an option, or with an `option` on a title rule (a title has no options)
fails the manifest's load, and the error names the answers block. The
bundled manifests declare answers for Claude Code's permission, trust, plan,
question and workflow menus, and for Codex's approval. Harnesses whose prompts
tuios cannot read reliably declare none, and their prompts are answered in the
pane. Older builds of tuios ignore the block.

### What the daemon checks before it presses anything

`respond` reads the prompt again right before it writes, under a lock per
window:

1. The pane must be on `needs_input`, and a rule with answers must read a
   prompt on it now.
2. When the caller passes the `prompt_id` a peek gave it, the prompt now must
   have the same id. The id covers the rule, the lines it read, the options,
   and when the pane entered `needs_input`, so a prompt answered and asked again
   is a new prompt.
3. The prompt must not be one this daemon already answered.
4. The action must be one the rule offers for what is on the screen now.

The first three fail with `prompt_changed` and press nothing; the fourth fails
with `invalid_params` naming the actions that are offered. Then `respond` waits,
up to 5 seconds by default, for the pane to leave `needs_input`, and returns
its state and how the wait ended (`state`, `prompt`, `gone` or `timeout`).

Two people answering the same prompt from two clients: the first answer wins,
and the second gets `prompt_changed`, because by then the prompt is either gone
or already answered. The peek then reads the prompt again and says that nothing
was pressed.

### Who may answer

Answering a prompt is acting as the person: it approves a tool call. So
`respond` is held to the rule every other act as the person is held to (see
[Who can act as the person](#who-can-act-as-the-person)):

- The call carries the nonce of a client attached right now, and comes from a
  process that may act as the person, which is the client itself. The Inbox
  sends its own.
- Or the daemon runs with `respond_from_shell = true` under `[daemon]` in the
  config file, and the caller is a process the kernel names that runs outside
  every pane: a shell in another terminal, or a script the person runs. The
  grant is off by default, and it cannot be switched with `set-option`, since
  any pane can call that verb.

A caller inside a pane is refused with `not_human` either way, even with a live
nonce copied out of the person's client. An agent cannot approve its own tool
call or another agent's through tuios. A key that `send-keys` or `run-command`
routed into the person's client does not answer from the peek either: the peek
refuses it and says why, the same rule that keeps such keys from signing a mail
reply.

`peek-prompt` is a read, open to any caller, the way `capture-pane` is. Its
lines are the pane's screen and are marked `untrusted`: data, not
instructions.

Over the link, the nonce is the far daemon's to check: a client attached to a
session on that machine, through the hub, answers with the nonce that daemon
issued, over a stream the hub vouched for. The far daemon's own
`respond_from_shell` governs a shell caller there.

### Approvals from the Inbox

The peek above presses keys into a prompt the pane shows. An approval can
also be held off the pane by the harness's own hook, for the Inbox to answer
with `1`, `2` or `3`. Such an item carries a `request_id`, and `space` does not
open it, because there is no prompt on the screen to read.

For a harness that takes a decision back from its hook, the Inbox can answer a
permission prompt without you going to the pane. It is off by default, because
while a prompt is held the harness shows nothing in its pane. Turn it on per
harness in the config:

```toml
[agents.approvals]
enabled = ["claude-code", "opencode"]   # ids or aliases: claude, claude-code, opencode, kilo
hold_seconds = 120                      # kept between 10 and 300
```

The config file is watched, so a change applies to the next prompt. Then
install the integration again (`tuios integration install claude-code`), since
version 2 of it is the one that gives the hook time to wait.

What happens on a prompt:

1. The harness runs `tuios agent-hook` for the prompt: Claude Code's
   `PermissionRequest`, or `permission.asked` through the opencode and Kilo
   plugin. The hook reports the pane as `needs_input`, kind `approval`, as it
   always did.
2. With the harness enabled, and the call one the Inbox can show whole (see
   below), the hook then calls `request-approval` and waits. The Approvals row
   gets the keys that answer it in text, such as
   `[1/3] approve Bash: go test ./...`, and the hint line says what each key
   does. With the cursor on it, the whole line is shown under the list, and
   for `2` the exact rules always adds, such as
   `Bash(go test:*) in .claude/settings.local.json`.
3. You press `1` (allow once), `2` (always allow, offered only with the rules
   it adds) or `3` (deny). The hook prints the harness's own decision and
   exits, the pane moves to `working`, and the item closes as answered. Every
   other client that was showing it says it was answered elsewhere.

You answer from one line, so a prompt is only held when that line is the whole
request. The call must be one whose effect one argument decides, with nothing
else that changes what it does, and that argument must fit the line exactly:
not cut, not masked as a secret, no newline, tab or doubled space, no control
or invisible character. The calls held are Claude Code's `Bash` (not with
`dangerouslyDisableSandbox`), `Read`, `Glob` and `Grep` without a `path`,
`WebFetch` and `WebSearch`, and opencode's `bash` (not with `workdir`), `read`
and `webfetch`. Everything else is answered in the pane as before: `Write`,
`Edit`, `MultiEdit` and `NotebookEdit`, whose body the line cannot show, MCP
tools, and a command too long for the line. The daemon checks the line again
before it holds, and the Inbox does not answer a line it would draw with
characters left out.

`2` is offered only when every rule it adds can be shown. For Claude Code that
means every `permission_suggestions` entry is an `addRules` that allows, kept
in the session or a settings file, with at most four rules; a `setMode` (such
as `acceptEdits`), an `addDirectories`, a deny or ask rule, or a field tuios
does not know leaves only `1` and `3`. The rules sent back are rebuilt from the
ones shown. For opencode, the rules are the request's own `always` patterns.

A key answers only what you read. The cursor stays on the item you selected
when the list re-sorts, and `1`, `2` and `3` do nothing (and say so) for a held
approval that has been on screen, as it is, for less than 0.4 seconds: one that
just arrived, moved under the cursor, changed its line or started a new hold.
While a hold runs, the item keeps the held call's line even when the same pane
reports another call, and the answer carries the line it was made from, so the
daemon refuses it (`changed`) if the hold is on another call by the time it
arrives.

A hold ends with no decision, and the harness then shows its own prompt as if
tuios were not there, when any of these happens first: `hold_seconds` passes;
you press enter on the item (going to the pane is choosing to answer there);
you focus the pane, or already have it focused when the prompt arrives; you
dismiss the item; the pane leaves `needs_input` or its block turns into a
question; the pane or session closes; a newer prompt from the same pane
arrives; the harness gives up on its hook; the daemon stops or restarts.

What is supported:

| Harness | Decision channel | Offers |
| --- | --- | --- |
| Claude Code | `PermissionRequest` hook output (`hookSpecificOutput.decision`) | once and deny, and always when every `permission_suggestions` entry is a rule tuios can show |
| opencode, Kilo | The plugin posts the reply to opencode's permission route | once and deny, and always when the request lists its `always` patterns |

Claude Code's `AskUserQuestion` and `ExitPlanMode` are not held: their answer
is a choice or a plan, not yes or no, and stays in Claude Code's own dialog.
Codex is not held either: its `PermissionRequest` hook runs before its own
reviewer decides whether to ask at all, so holding it would ask you about calls
Codex would have settled itself.

Safety: the hook prints a decision only when the daemon returned one that the
person made and the harness was offered. Every error prints nothing: no daemon,
a daemon that restarts during the hold or predates approvals, a reply it cannot
read, its own 305 second limit. Printing nothing is how every harness here
says "ask the user", so a failure can only fall back to the harness's own
prompt, never approve. Only a client attached right now can answer, with its
attach nonce, checked the way `dismiss-attention` is (see
[Who can act as the person](#who-can-act-as-the-person)); no agent, mail,
`ask-agent` or keystroke routed through the protocol can. The hold itself may
be requested only for the caller's own pane, and never over a link.

### Resuming after a restart

A daemon restart ends every program in every pane, agents included. The
restore brings back the layout with a new shell in each pane. It does not bring
back the process, and nothing can: whatever turn was running did not finish.
What it can bring back is the conversation, because every harness with a
resume command keeps it on disk, and the pane's hook already told the daemon
its id (`agent_session_id`, see [Session identity](#session-identity)).

So for each restored pane whose agent was still running when the state was
saved, and whose harness manifest has a `[resume]` block, the restore offers the
command that reopens the conversation: `claude --resume <id>`, `codex resume
<id>`, `opencode --session <id>`. What it does is `daemon.resume_agents`:

| Value | What a restore does |
| --- | --- |
| `ask` (default) | A Resume row in the Inbox per pane, its summary the exact command. Your client says once, in the dock, that there are conversations to resume. `y` on the row goes to the pane and types the command; `d` dismisses it. |
| `auto` | Waits for each new shell to draw its prompt, then types the command, 100 ms apart. A pane whose shell is not at its prompt within 10 seconds gets the Resume row instead. |
| `off` | Nothing. The id stays on the pane. |

A pane counts as running an agent when its saved state has an agent state or
a harness attribution, both of which clear when the agent leaves the pane. The
id does not clear, so a pane where you quit the agent and went back to shell
work keeps its id and gets no offer. A restored pane comes back with no agent
state, since its shell is new, so the next save records no live agent there
and a later restart does not offer the same conversation again. An offer is
made once, for the restart that ended the agent, whether you answer it,
dismiss it or leave it.

`tuios resume-agent -w <pane>` types the same command at any time, on any
pane with a recorded id, and
`--dry-run` prints it. The command is typed only when the pane's shell holds
the terminal's foreground, so it never lands in an editor or another agent. A
Resume row closes when the command is typed, when the pane goes to `working` or
`needs_input` (you ran the agent yourself), or when the pane closes.

The bundled manifests carry `[resume]` for Claude Code, Codex, opencode,
Copilot, Cursor Agent, Devin, Droid, Grok, Hermes, Kilo, Kimi, Qoder, Qwen and
Antigravity, after herdr's resume table. A harness has to report its session id
for any of this to apply, which the integrations from `tuios integration
install` do. Add one to your own manifest:

```toml
[resume]
argv   = ["myagent", "--resume", "{session_id}"]
source = "myagent --help"
```

Every token has to be letters, digits and `_ . / : = + -`, with
`{session_id}` somewhere after the program, and a manifest that breaks this
fails to load by name. The id has to be letters, digits and `_ . / : -`, not
starting with `-`, at most 256 bytes, or it is never typed. That is what keeps
an id a pane reported from being read by any shell as anything but one
argument: the command is built from the manifest and the stored id only, and
nothing from the old command line, its prompt or its environment is replayed.

A pane that ran on another machine comes back on this one without its id,
since the conversation is on that machine.

## Harness integrations

A harness with a hooks system reports its own state, which outranks everything
tuios can work out by looking. tuios wires eighteen of them itself:

```sh
tuios integration install claude-code   # any harness below, or --all
tuios integration status                # installed and current, per harness
tuios integration uninstall codex
tuios doctor agents                     # PATH, install state, and panes missing theirs
```

An integration reports one of two things. Eight report the pane's **state**:
their hooks cover the whole turn, from the prompt through approvals to the end.
The other ten report only the **session**: the harness's own id for the
conversation, stored on the pane with `set-agent-session` so it can be resumed,
while the pane's state keeps coming from the manifest's screen and title rules.
Their hooks miss events a state needs, an interrupt, a cancelled approval or the
end of a turn, and a state reported by a hook outranks every screen rule, so one
missed event would hold the pane on `working` until the harness exits. herdr
drew the same line for the same harnesses after running them. `tuios integration
status` and `tuios doctor agents` say which each one is.

| Harness | Reports | What is written | Format source |
| ------- | ------- | --------------- | ------------- |
| Claude Code | state | `hooks` in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR`) | [hooks reference](https://code.claude.com/docs/en/hooks) |
| Codex | state | `~/.codex/hooks.json` (or `$CODEX_HOME`) | [Codex hooks](https://developers.openai.com/codex/hooks) |
| Gemini CLI | state | `hooks` in `~/.gemini/settings.json` | [hooks reference](https://geminicli.com/docs/hooks/reference/) |
| opencode | state | `plugins/tuios-agent-state.js` in `~/.config/opencode` (or `$XDG_CONFIG_HOME/opencode`) | [plugins](https://opencode.ai/docs/plugins/) |
| Kilo | state | `plugin/tuios-agent-state.js` in `~/.config/kilo` (or `$XDG_CONFIG_HOME/kilo`), the opencode plugin under Kilo's id | opencode's plugin API, which Kilo forks |
| Amp | state | `plugins/tuios-agent-state.ts` in `~/.config/amp` (or `$XDG_CONFIG_HOME/amp`) | [plugin API](https://ampcode.com/manual/plugin-api) |
| Kimi Code CLI | state | `[[hooks]]` tables between two marker comments at the end of `~/.kimi-code/config.toml` (or `$KIMI_CODE_HOME`); needs 0.14.0 or newer | [hooks](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/hooks.html) |
| Pi | state | `extensions/tuios-agent-state.ts` in `~/.pi/agent` (or `$PI_CODING_AGENT_DIR`) | herdr's Pi extension |
| Antigravity CLI | session | a `tuios` block in `~/.gemini/config/hooks.json` (or `$ANTIGRAVITY_CLI_CONFIG_DIR`) | herdr's Antigravity installer |
| GitHub Copilot CLI | session | `hooks/tuios.json` in `~/.copilot` (or `$COPILOT_HOME`), a file of its own | [hooks configuration](https://docs.github.com/en/copilot/reference/hooks-configuration) |
| Crush | session | a `PreToolUse` hook in `~/.config/crush/crush.json` (or `$XDG_CONFIG_HOME/crush`) | [hooks](https://github.com/charmbracelet/crush/blob/main/docs/hooks/README.md) |
| Cursor Agent | session | a `sessionStart` hook in `~/.cursor/hooks.json` (or `$CURSOR_CONFIG_DIR`) | [hooks](https://cursor.com/docs/hooks) |
| Devin CLI | session | `hooks` in `config.json` in `$XDG_CONFIG_HOME/devin`, `~/.config/devin` or `%APPDATA%\devin` | herdr's Devin installer |
| Droid | session | `hooks` in `~/.factory/settings.json` | herdr's Droid installer |
| Grok CLI | session | `hooks/tuios.json` in `~/.grok` (or `$GROK_HOME`), a file of its own | herdr's Grok installer |
| Hermes Agent | session | a plugin in `plugins/tuios-agent-state/` under `~/.hermes` (or `$HERMES_HOME`), and `tuios-agent-state` in `plugins.enabled` in its `config.yaml` | herdr's Hermes plugin |
| Qoder CLI | session | `hooks` in `~/.qoder/settings.json` (or `$QODER_CONFIG_DIR`) | [hooks](https://docs.qoder.com/zh/cli/hooks) |
| Qwen Code | session | `hooks` in `~/.qwen/settings.json` (or `$QWEN_HOME`) | herdr's Qwen installer |

Four recognised harnesses have no integration, and `tuios doctor agents` names
them with the reason: aider (its one hook, `notifications-command`, replaces the
user's own and carries nothing), Cline (one executable per event in a directory
that has moved between releases, behind a setting), Kiro (no documented
user-wide hook location or payload) and maki (Lua plugins loaded from the user's
own `init.lua`). Their state comes from their manifests.

A plugin, an extension, and the hook file of its own tuios writes for Copilot
and Grok are files tuios owns whole. Each carries the version marker in its
text; a file at that path that tuios did not write is refused, never
overwritten, and never removed. Every other integration edits a file the user
owns, as below. Hermes touches three files, and every file an integration
touches is worked out before any is written, so a file tuios cannot read leaves
all of them unchanged. Its `config.yaml` is edited line by line, keeping
comments and order; a `plugins.enabled` written as an inline list with items in
it is refused, with the line to add by hand. Uninstall leaves an emptied list as
`enabled: []`.

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
| `PermissionRequest` | `needs_input`, kind `approval`, message `approve <tool>: <command or path>`. With approvals on, then waits for an answer from the Inbox (see [Approvals from the Inbox](#approvals-from-the-inbox)) |
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
drops every event from a child session. Kilo, a fork of opencode, runs the same
plugin under its own id and gets the same map.

Kimi Code CLI maps like Claude Code: `SessionStart` to `idle`,
`UserPromptSubmit` and `PreToolUse` to `working`, `PermissionRequest` to
`needs_input` kind `approval`, `PostToolUse`, `PostToolUseFailure` and
`PermissionResult` to `working` only from `needs_input`, `Stop` to `done`,
`StopFailure` to `errored`, `Interrupt` (which Kimi sends instead of `Stop`) to
`idle`, and `SessionEnd` to `none`. A `PreToolUse` for its `AskUserQuestion`
tool is `needs_input` kind `question` with the question as the message. The Amp
plugin maps `agent.start` to `working` and `agent.end` to `done`, `errored` or
`idle` by its status (`done`, `error`, `cancelled`), and sends the thread id on
`session.start` as a session report. It subscribes to nothing that decides
anything, `tool.call` above all, so it cannot change what Amp permits; Amp's
approval prompts come from its screen rules. The Pi extension maps
`agent_start` to `working`, `agent_settled` to `done`, and `session_start` to
`idle` (`working` after a reload mid-turn), only in Pi's TUI mode.

The session integrations report the conversation id from these events and
nothing else: Copilot, Droid, Qoder, Qwen and Grok `SessionStart` (Grok's id
from `GROK_SESSION_ID` first), Cursor `sessionStart`, Devin `SessionStart` and
`UserPromptSubmit`, Antigravity `PreInvocation` (`conversationId`), Crush
`PreToolUse` (`CRUSH_SESSION_ID` first; it is the only event Crush has), and
Hermes `on_session_start` and `on_session_reset` for an interactive session.

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
the one that owns the pane. These filters keep those events off the pane:

- A subagent's events (`agent_id` set, `SubagentStop`, opencode child sessions)
  are dropped by the reporter.
- An event from a harness other than the one `TUIOS_AGENT` names is dropped by
  the reporter. `TUIOS_AGENT` may name any harness tuios recognises, one with
  no integration included, so a Claude Code hook in a pane given to aider is
  dropped too; a name tuios does not know says nothing either way. A Claude
  Code hook that Cursor or Grok runs is dropped (both read Claude Code's hook
  configuration; Grok marks its hook processes with `GROK_SESSION_ID`), and so
  is a Codex hook whose session is not the `CODEX_THREAD_ID` it inherited.
  Every plugin tuios installs does nothing unless `TUIOS_ENV` or `TUIOS_AGENT`
  is set, so outside tuios it costs nothing.
- A session report (`set-agent-session`) is refused for a pane attributed to
  another harness, and for a pane mid-turn whose id came from another process
  of the same harness. See [Session identity](#session-identity).
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
or the daemon, restarts (see [Resuming after a restart](#resuming-after-a-restart)).
It is kept when the agent exits and replaced when another session reports
into the pane. The harness it belongs to is stored with it
(`agent_session_harness`), since the pane's harness attribution is cleared when
the agent exits and a resume needs both.

A harness whose hooks can name the conversation but cannot be trusted with the
pane's state sends the id alone, with the `set-agent-session` verb (`tuios
set-agent-session` by hand). It stores the id and changes nothing else: not the
state, not the source that holds it, not the harness the pane is attributed to.
A state reported by a hook outranks every screen rule, so a hook that misses an
interrupt or the end of a turn would pin the pane on `working`; an id cannot do
that. The same nested-run guards apply: a pane attributed to another harness
refuses the id, and so does a pane mid-turn whose id came from a different
process of the same harness.

The transcript path is not stored anywhere a client can read. It goes straight
to the transcript source: for a harness whose manifest has a transcript reader
(Claude Code today), the pane is joined to exactly that file, which replaces the
search that refuses whenever two files in one directory could both be the
pane's.

### Failure behaviour

Harnesses run some hooks synchronously, `PreToolUse` and `PermissionRequest`
among them. `tuios agent-hook` exits 0 whatever happens, prints nothing a
harness could read as an answer (Gemini CLI and Antigravity CLI, which parse
stdout, get `{}`), and gives up after 500 ms (`--timeout`) when the daemon is
slow, restarting or gone. The opencode, Kilo, Amp and Pi plugins run it in a
child process they do not wait on, so they never hold their harness up; the
Hermes plugin waits for it, on session start only, for at most two seconds. A session report goes only
to a daemon whose `list-verbs` has `set-agent-session`; an older daemon gets
nothing, and `--explain` says so.

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

The one hook that may wait is a permission prompt with approvals on (see
[Approvals from the Inbox](#approvals-from-the-inbox)). The 500 ms limit still
covers its report; the wait after it is bounded by the daemon's hold and by the
hook's own 305 second limit, below the 310 seconds the Claude Code integration
gives that hook. With approvals off it returns as fast as any other hook.

### The old shim

`integrations/claude-code/tuios-agent-state.sh` is now a wrapper that runs
`tuios agent-hook claude-code`, so settings that point at it get the new map.
Wired alongside an installed integration it reports every event twice;
`status` and `doctor` say so. See
[integrations/claude-code](../integrations/claude-code/README.md).

## Typing a prompt

`ask-agent` and `fan` type a prompt the same way: the text as one paste,
bracketed (`ESC[200~` ... `ESC[201~`) when the pane has DECSET 2004 on, a short
wait, then the submit key. What differs by harness is data in its manifest's
`[input]` block:

```toml
[input]
submit              = "cr"    # or "lf"; the key that submits
bracketed_paste     = true    # false: never bracket, even with DECSET 2004 on
focus_before_submit = false   # true: send a focus-in report (CSI I) first
source              = "measured on ... / documented in ... / not measured"
```

A manifest without the block, and a pane no harness claimed, get the defaults:
carriage return, and a bracketed paste when the pane asks for one. That is what
every prompt got before the block existed. `focus_before_submit` is only acted
on when the pane has focus reporting (DECSET 1004) on, so an application that
never asked for focus events never sees the bytes. Nothing in the block can do
more than choose among these bytes: the text is the caller's, and the verbs
that type it keep their own checks (`ask-agent` still refuses a pane on
`needs_input`).

The answer to "CR or LF" is CR for every bundled harness. Enter in a raw-mode
terminal sends a carriage return, and every input library these harnesses use
reads a line feed as a different key: Ink (Claude Code, Gemini CLI, Qwen Code)
reads it as another key than return, crossterm (Codex) as Ctrl+J, Bubble Tea
(Crush) as Ctrl+J, prompt_toolkit (Aider) as c-j. Claude Code's docs list
Ctrl+J as the way to insert a newline. Each manifest's `source` says where its
values come from:

| Harness | Submit | Bracketed paste | Source |
| --- | --- | --- | --- |
| claude-code | CR | yes | measured on 2.1.280: DECSET 2004 and 1004 on, kitty keyboard flags 5 |
| opencode | CR | yes | measured on 1.18.30: DECSET 2004 on |
| crush | CR | yes | measured on v0.96.1: DECSET 2004 on, kitty keyboard flags 1 |
| codex | CR | yes | crossterm key handling; paste-burst behaviour per herdr; not measured |
| gemini-cli, qwen | CR | yes | Ink key handling; not measured |
| aider | CR | yes | prompt_toolkit key handling; not measured |
| copilot | CR | yes, plus a focus-in report first | herdr: Copilot ignores a synthetic Enter after focus loss until it is told it has focus; not measured |
| the others | CR | yes | the default; not measured |

Kitty keyboard flags 1 and 5 (disambiguate, and report alternate keys) leave
Enter as a carriage return, which is why the two measured harnesses that push
them still take one.

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

### A pane on another machine

A window whose process runs on another machine (`tuios new-window NAME --host
HOST`) exports `TUIOS_PANE_ID`, the window's id on the machine that holds the
window, and `TUIOS_PANE_HOSTED=1`, and no `TUIOS_SOCKET` or `TUIOS_SESSION`.
Hooks and shims work there unchanged: `tuios agent-hook` and `tuios
set-agent-state -w "$TUIOS_PANE_ID"` reach the daemon on the machine the
process runs on, which sends the report to the daemon that holds the window
over the link, and that window takes the state, its Inbox row and its alerts.
Reading and sending mail as the pane, and `wait-for agent-message` on it, work
the same way. See [Reports from a pane on another
machine](protocol.md#reports-from-a-pane-on-another-machine).

What limits it:

- Only a process in that pane is forwarded for, judged by the pid the kernel
  gives for its connection: the pane's process, what it started, or a process
  on the pane's terminal. Any other caller naming the pane is refused with
  `forbidden`.
- The daemon holding the window runs each report as that window and nothing
  else. It ignores the session and window the report names, drops the
  transcript path and harness pid (they name things on the other machine),
  refuses a send from anyone but the window, and treats the caller as inside a
  pane, so it can never speak as the person.
- A message the pane sends can attach only a file in the session's stash, the
  rule a sender on the link is held to. A path it names is a file on the
  machine holding the window, which its process cannot see, so any other path
  is refused before that machine looks at it. Use `tuios stash put -s
  HOST:SESSION FILE` and attach the path it prints.
- A `wait-for agent-message` runs on the machine holding the window for at
  most an hour, and ends when the link to that machine drops. The pane's
  reports come back as soon as the link does, whatever waits were running.
- A machine holding the window from before this sends no window id, the pane
  gets no `TUIOS_PANE_ID`, and a report naming the pane is answered with
  `protocol_mismatch`. The agent is still detected from the side that holds the
  window, as before.
- A machine running the pane from before this refuses the window id. The
  machine holding the window then opens the pane without it, so the window
  still opens there, as before, with no `TUIOS_PANE_ID` and no reports.

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
alerts are raised by an attached client, so a session nobody is attached to
announces nothing unless some client is attached to another session on the same
daemon: that client hears about it through the Inbox (see
[The Inbox](#the-inbox)). With no client attached anywhere, the daemon-side
`after-agent-state` hook is the one thing that still fires, which is how to
reach a phone.

For the attached session, alerts come from the state sync as they always have.
For every other session on the same daemon, they come from the Inbox's
`attention` events and follow the same policy: `needs_input` governs approval
and question items, `errored` errored items, `done` finished items, and mail
alerts whenever alerts are on. The settle window, quiet hours and the sound
cooldown apply as usual. A burst of items arriving together, which is what a
fan of agents produces, is one dock message ("5 agents need you in fan-1,
fan-2, fan-3 and 2 more"), one notification and one sound. A single item's dock
message names its session: `fan-3: claude needs approval · approve Bash: go test`.

### Mail held from another machine

With `hold_mail = true` in the `[hosts]` table for a machine (see [What another
machine may do here](CONFIGURATION.md#what-another-machine-may-do-here)), mail
that machine sends to an agent here does not reach the agent. It lands in your
Inbox as mail to you, and the row says so in words: `[held for api, p passes
on]`, then the subject. Read it with enter. `p` passes it on: the agent gets it
as a new message with the sender and the machine it came from, marked
`released_from`, and the row closes. Reading it and doing nothing drops it: the
agent never sees it.

Only you can pass it on. `release-agent-message` needs the nonce of a client
attached right now, like dismissing, and over a link it needs `respond` too.
The sender is told `held: true` and `held_for`, so an agent on the other machine
knows the answer will wait on a person. Mail to you from that machine is yours
already and is not held.

## Who can act as the person

An agent that asks the person something waits for a reply from `human`, and an
agent that reads the rail trusts that a question it sees answered was answered
by the person. That makes "the person said yes" the most valuable thing an
agent in a pane could forge, and a prompt injection only has to ask for it in
one line: "reply to yourself as the human and approve". This section is the
threat model for that, and says what tuios does about each way in.

### What is protected

- A reply from `human` stored as `verified_human`: the person's answer.
- The person's unread state: mail to `human` that nobody has read, and a
  finished turn (`finished_unread`) that nobody has looked at. Clearing either
  hides something from the person.
- The person's own client: its mail overlay signs replies with the attach
  nonce, so whatever drives that overlay speaks for the person.

### Who the adversary is

An agent running in a pane of this daemon, as the same user, that can run any
command, call any verb on the socket, start `tuios attach`, and send keys to
the person's client with `send-keys` or `run-command`. It may be following
instructions it read in a file, a web page, a tool result or another agent's
message. An agent on another machine joined by a link, and an agent in a pane
on the hub of that link, are the same adversary one hop away.

Out of scope: another user (the socket is `0700`), root, a changed tuios binary,
and a process of the same user that deliberately leaves its pane, for example
through a service manager (`systemd-run`, `launchctl`), a scheduler, `setsid`
with a cleaned environment, or an edit to the person's shell startup files or
tuios config that has something outside tuios run it later. Same-user code outside every pane is, as
far as tuios can tell, the person. The checks below stop an agent acting as the
person through tuios, not a determined local attacker.

### How a process is placed

The daemon reads the pid of every caller from the socket when it connects
(`SO_PEERCRED` on Linux, `LOCAL_PEERPID` on macOS), which nothing the caller
sends can change. The caller is inside a pane of this daemon when:

- the daemon is one of its ancestors: every pane shell, hook command and popup
  is a child of the daemon;
- its controlling terminal is one of the daemon's pane terminals, which still
  holds for a process orphaned out of a pane;
- or its environment names one of the daemon's windows in `TUIOS_PANE_ID` or
  `TUIOS_WINDOW_ID`, or the daemon's socket in `TUIOS_SOCKET`. That still holds
  for an orphan that also left the terminal, and it also places the commands
  the client starts, its hooks and dock components: they are automation the
  person configured, not the person.

A process whose record cannot be read counts as inside a pane. The daemon's own
process does not, which is where a client runs when it is built into the same
binary. Windows and the BSDs do not give this build the peer's pid, so there
every caller is treated as outside a pane and the attach nonce is the only
proof, as before.

### Each way in, and what stops it

| Route | What stops it |
| --- | --- |
| `send-agent-message --from human` from a pane | Refused with `forbidden`. Nothing is stored. |
| `ask-agent --from human` from a pane | Refused with `forbidden`, so no ask is recorded as the person's. |
| `tuios attach` from a pane, to take the nonce from the attach reply | The reply carries no nonce, so nothing sent with it verifies. |
| A nonce copied out of the person's client into another process | A reply verifies only from the process that holds the attach, where the kernel gave both pids. The nonce is never in an environment, on a screen or in a log. |
| `send-keys` or `run-command` driving the person's mail overlay | A reply any routed key opened, edited or sent goes out without the nonce and is stored as `claimed_human`. The reply line reads `automated reply:`. |
| `read-agent-messages -w human` from a pane, to clear the person's unread mail | Served as a peek: nothing is marked read, and the result says `peek_forced`. |
| A client attached from a pane, to clear `finished_unread` by focusing panes | Its state pushes do not mark a finished turn seen. |
| `dismiss-attention` from a pane, to empty the person's Inbox | Refused with `not_human`, even with a live nonce copied from the person's client: the nonce is checked the way a reply's is. |
| `respond` from a pane, to approve its own tool call or another agent's | Refused with `not_human`, with or without a copied nonce, and with or without `respond_from_shell`, which only grants callers outside every pane. Nothing is pressed. |
| `send-keys` or `run-command` driving the person's Inbox peek, to press `a` | The peek sends no answer for a routed key and says why. |
| `reply-approval` from a pane, to approve its own or another agent's call | Refused with `not_human` on the same check. `request-approval` from a pane may hold only that pane's prompt, and returns only what the person answered about it. |
| An agent in a hub pane attaching through the link to this machine | The hub vouches only for a caller outside its panes, in the stream's open frame, which the caller cannot write. The proxy here dials the link-human socket only for a vouched stream. An attach through the plain link socket gets no nonce. |
| An agent in a pane on this machine dialing the link-human socket itself | The same pane check runs on that socket, against the process that dialed it. |
| A hub from before this check | It vouches for nothing, so no attach through it verifies here. |
| An agent in a pane on another machine reporting through the link | It reaches only its own window: the machine holding the window rewrites every report to that window and runs it as a caller inside a pane, so `from human` is refused and reading the person's mail is a peek. An attachment outside the session's stash is refused before its path is looked at, so the pane cannot attach, or learn about, a file on the machine holding the window. |
| A process on the far machine reporting as a hosted pane it is not in | Refused with `forbidden` there, by the pid the kernel gives. The channel the reports travel on needs a token only the machine holding the window saw. |
| A linked host writing Inbox items into this machine | Items are only displayed. Their text is cleaned and cut, a host holds at most 256 rows apart from this machine's, and nothing it sends runs a command, types into a pane or marks anything here. Dismissing one hides it here only. |

### What this does not cover

- **Other panes' prompts.** An agent can still `send-keys` into another pane
  and answer that agent's approval menu itself, or `set-agent-state` on another
  pane to take a `needs_input` off the rail. Neither is acting as the person
  through tuios, and both are what per-pane scoped tokens are for. `ask-agent`
  refuses a pane on `needs_input` (`agent_blocked`), which covers the accident
  but not an agent set on it.
- **The person's screen.** `popup` and a program in a pane can draw anything,
  including a fake question. Read what a prompt asks before answering it.
- **Leaving the pane on purpose.** See above.
- **Pid reuse.** The pid is read at connect time and the process is looked at
  when a check first needs it. A caller that exits and has its pid reused by an
  unrelated process in between is judged as that process. This needs the
  attacker to control pid allocation, and the likely outcome of a miss is a
  refusal.

### What the person notices

Everything the person does from a client started outside tuios is unchanged.
A client started inside a tuios pane of the same daemon, a nested `tuios
attach`, or `tuios-web` or the SSH server started from a pane, counts as inside
a pane: its mail replies are refused with `forbidden`, its reads of the
mailbox do not mark mail read, and its Inbox cannot dismiss an item. Start
those from a terminal outside tuios.
