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
- [The stall heuristic](#the-stall-heuristic)
- [Indicator](#indicator)
- [Claude Code integration](#claude-code-integration)
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
`argv_path` matches path components of the command line.

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

It prints the `comm`, `argv` and `exe` the daemon read, whether the process
counted as an interpreter and which token was eligible to name an agent, then
every manifest in lookup order: which one matched and on which predicate, and for
each that refused, what it was comparing against.

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
no opinion rather than falling back to a state. Only `needs_input` rules ship
enabled: `working` is already carried by output arriving at all, and a rule keyed
on a spinner glyph is the first thing to break when an agent's TUI changes in a
patch release.

Rules run when a pane writes, throttled, plus once more shortly after it goes
quiet, because the prompt is painted by the last chunk before the silence. A pane
that stays silent costs nothing: there is no ticker.

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

## The stall heuristic

Agents that do not report get a conservative fallback. If a pane reported
`working` but then produces no output for a while, the daemon demotes it to
`idle`, on the assumption that a genuinely busy agent produces output.

Silence alone is not enough to act on, because an agent that finished and an
agent waiting on a human produce exactly the same silence, and `idle` reads as
"finished and fine". So before demoting a pane, the daemon hands it to the screen
tier for a last look, and leaves alone any pane whose screen answers. A look that
finds nothing still demotes: the screen was read and said nothing, which is as
much evidence as there is going to be.

The fallback is strictly secondary to explicit reporting:

- It only ever moves a pane out of `working`, and only ever into `idle`. Any
  other state (`needs_input`, `done`, `errored`) is never touched, so an explicit
  report is never overridden.
- The silence clock is the later of the pane's last output and the time its
  `working` state was set, so a working report is given the full window before it
  can be demoted, and output keeps a pane looking busy.
- It never promotes a pane into `working`; only an explicit report does that.

The silence window defaults to 30 seconds. Override it with the
`TUIOS_AGENT_STALL_SECONDS` environment variable when starting the daemon; set it
to `0` (or a negative value) to disable the heuristic entirely.

## Indicator

tuios draws a one-cell glyph in each window's title:

| State         | Indicator |
| ------------- | --------- |
| `working`     | `●`       |
| `needs_input` | `▲`       |
| `idle`        | `○`       |
| `done`        | `■`       |
| `errored`     | `×`       |
| `none`        | (nothing) |

The glyphs are distinct shapes rather than the same shape in different colors, so
the state reads at a glance and survives a monochrome capture. The indicator
shows even for a window with no name.

## Claude Code integration

A reference shim maps Claude Code's lifecycle hooks to these states. See
[integrations/claude-code](../integrations/claude-code/README.md) for the script
and how to wire it.

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
wired up outside tuios.

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
