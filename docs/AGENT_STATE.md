# Agents in tuios

This is the guide to running coding agents in tuios, and to agents that drive
tuios. It starts with what you get and how to set it up, then maps every agent
feature to where it is described, then gives the reference.

An agent reading this from inside a pane wants `tuios --skill` instead: a short
core, and `tuios --skill TOPIC` for the rest. It ships in the binary, so it
matches the build.

## What you get

- **One state per pane.** Every pane running an agent shows `working`,
  `needs_input`, `idle`, `done` or `errored`, as a shape in its title and as a
  row on the rail, so no state rests on colour alone. The
  agent reports it through a hook tuios installs, and tuios falls back to
  reading the process, the screen and the title when nothing reports.
- **One place to look.** The Inbox (`ctrl+b i`) lists everything waiting for
  you in every session on every machine: approvals, questions, mail, errors,
  finished turns and conversations to resume. `ctrl+b o` jumps to the oldest.
  You answer from there: a digit, `a` or `d` on a prompt, `1`, `2` or `3` on an
  approval the Inbox holds, `r` on mail.
- **Agents that talk safely.** Agents mail each other and ask each other
  questions. Everything one agent reads from another is fenced as data. Only
  you can answer a prompt or speak as `human`, and a reply from you is marked
  verified.
- **Fleets.** `tuios fan` starts one prompt in several agents, each in its own
  git worktree. `tuios start-agent` starts one helper beside you. Selectors
  address a whole group.
- **Other machines.** Agents, worktrees and the Inbox work across `tuios hosts`,
  and `tuios worktree pull` brings the work back.
- **Limits you set.** Pane grants say what an agent's pane may do through
  tuios. Link policy says what another machine may do here.
- **Other ways in.** `tuios mcp` serves the same surface as MCP tools, the tmux
  shim runs tools that only know tmux (such as Claude Code agent teams), and
  `tuios subscribe` streams every change.

## Set it up

1. Wire each harness you use to report its state and its conversation id:

   ```bash
   tuios integration install claude-code    # or codex, gemini-cli, opencode, ..., or --all
   tuios doctor agents                      # what is installed, and agent panes missing one
   ```

2. Learn two keys: `ctrl+b i` opens the Inbox and `ctrl+b o` goes to the
   oldest item. The rail is on by default from v0.8.0. A config file that sets
   it off keeps it off; turn it on with:

   ```bash
   tuios set-config appearance.sidebar.enabled true
   ```

3. Optional: let the Inbox answer permission prompts, so you do not have to go
   to the pane. In config.toml:

   ```toml
   [agents.approvals]
   enabled = ["claude-code"]
   ```

4. Optional: start every pane with less than `admin`, and give more where it is
   needed:

   ```toml
   [agents.permissions]
   mode = "strict"
   grants = ["read", "write", "fan"]
   ```

5. Optional: give your agents the tools. Either the skill, which an agent reads
   with `tuios --skill`, or MCP:

   ```bash
   tuios integration install claude-code --mcp
   ```

6. Optional: an alert on your phone when an agent needs you, with nobody
   attached: an `after-agent-state` hook ([HOOKS.md](HOOKS.md); `tuios --skill
   recipes` has a working one).

## Where to find what

| To | Read |
| --- | --- |
| Understand the states and who decides them | [States](#states), [Sources and precedence](#sources-and-precedence) |
| Make a harness report, or see why a pane is or is not an agent | [Harness integrations](#harness-integrations), [Recognising a harness](#recognising-a-harness), [Screen rules](#screen-rules) |
| Use the Inbox and answer prompts | [The Inbox](#the-inbox), [Answering a prompt without attaching](#answering-a-prompt-without-attaching), [Approvals from the Inbox](#approvals-from-the-inbox), [KEYBINDINGS.md](KEYBINDINGS.md#the-inbox) |
| Let agents ask you something | [Questions an agent asks you](#questions-an-agent-asks-you) |
| Let agents talk to each other | [Agents talking to agents](#agents-talking-to-agents) |
| Run a fleet of agents | [Fleets](#fleets), [Selectors](#selectors), [Headless agents over a protocol](#headless-agents-over-a-protocol) |
| Bring a conversation back after a restart | [Resuming after a restart](#resuming-after-a-restart) |
| Run agents on other machines | [Other machines](#other-machines), [SESSIONS.md](SESSIONS.md#agents-and-worktrees-on-another-machine) |
| Limit what an agent may do | [What a pane may do](#what-a-pane-may-do), [Who can act as the person](#who-can-act-as-the-person), [CONFIGURATION.md](CONFIGURATION.md#what-another-machine-may-do-here) |
| Get alerts, or reach a phone | [Alerts](#alerts), [HOOKS.md](HOOKS.md) |
| Drive tuios from an agent | `tuios --skill`, [The MCP server](#the-mcp-server), [TMUX_SHIM.md](TMUX_SHIM.md), [protocol.md](protocol.md) |
| Look up a command | [CLI_REFERENCE.md](CLI_REFERENCE.md) |

## Agents talking to agents

An agent pane is a window, addressed with `-w` by id or name like any other.
Four verbs carry everything between agents and the person:

| Verb | What it does |
| --- | --- |
| `tuios send-agent-message` | Leaves a message in a pane's inbox, or `human`'s, without touching its keyboard. Threads with `--reply-to`, files with `--attach` or the session stash |
| `tuios read-agent-messages`, `tuios wait-for agent-message` | Read the inbox, or block until mail arrives |
| `tuios ask-agent` | Types a question at an agent that is at its prompt, submits it, and returns what the pane printed. Refuses a pane on `needs_input` (`agent_blocked`), since the text would answer its prompt |
| `tuios ask-human` | Puts a question with fixed answers in the Inbox and returns your answer |

What keeps it safe: every body an agent reads is fenced as untrusted data and
`--from` is a claim; a pane cannot send as `human`, and your replies carry
`verified_human`; a pane cannot address itself, a cycle of asks is refused, and
a sender is rate limited. Mail lives in memory, 256 messages per session, and
dies with the daemon. `tuios --skill mail` has the whole contract.

## Fleets

| Command | What it does |
| --- | --- |
| `tuios worktree new BRANCH --agent claude` | A git worktree and a session in it, with an agent. The rail groups these by repository |
| `tuios fan N --agent claude 'PROMPT'` | N worktrees, an agent in each, and the prompt typed into each once it is at its prompt. `--agent 'claude,codex'` mixes agents, `--prompt` repeated gives each its own |
| `tuios start-agent claude --name reviewer` | One agent in a new pane beside you, returning once it is ready. `--protocol acp` or `codex` runs it headless |
| `tuios worktree ls`, `tuios worktree diff`, `tuios fan keep SESSION` | Watch them, read what one changed, keep one and remove the rest without losing uncommitted work |
| `tuios fan compare SESSION`, `tuios fan verify SESSION -- CMD`, `tuios fan diff A B` | Every attempt side by side with its changes and last check, one check run in all of them, and what two did differently |
| `tuios review [SESSION]`, `tuios review note FILE:LINE 'TEXT'`, `tuios review send` | What the agent in a pane changed against its base, notes on its lines, and the notes sent to it as one message when it rests. See [Reviewing an agent's changes](#reviewing-an-agents-changes) |
| `--select 'group:fan/retry needs:you'` | Address every agent pane a selector matches, on `list-agents`, `list-attention`, `wait-for`, `send-agent-message` and `ask-agent` |
| `--grants read,write` | On `fan`, `start-agent` and `new-window`: what the new panes may do |

A prompt that cannot be typed because the agent is stuck on a first-run choice
turns into an Inbox question after 30 seconds. See
[CLI_REFERENCE.md](CLI_REFERENCE.md#tuios-fan) for the flags and
`tuios --skill fleet` for the agent's view.

## Other machines

With `tuios hosts add NAME ADDR`, the daemon keeps an ssh link to that machine.
Its agents appear on the rail and in `list-agents --all-hosts`, what waits there
is in your Inbox, `-s HOST:SESSION` and `-w HOST:SESSION:WINDOW` reach it from
any command, and `fan`, `worktree` and `start-agent` take `--host`.
`tuios worktree pull HOST:SESSION` brings a worktree's commits and uncommitted
work into a new worktree here. Mail to a machine whose link is down waits and is
sent when it is back. Each machine's `[hosts]` table decides what the others
may do there; answering prompts is off by default. See
[SESSIONS.md](SESSIONS.md#windows-on-another-machine) and
[A pane on another machine](#a-pane-on-another-machine).

## Reference

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
- [Selectors](#selectors)
- [Answering a prompt without attaching](#answering-a-prompt-without-attaching)
- [Harness integrations](#harness-integrations)
- [Headless agents over a protocol](#headless-agents-over-a-protocol)
- [Typing a prompt](#typing-a-prompt)
- [Environment](#environment)
- [Alerts](#alerts)
- [Who can act as the person](#who-can-act-as-the-person)
- [What a pane may do](#what-a-pane-may-do)

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

`unknown` is a display state and not a ready one. `fan` and `start-agent` wait
for `idle` or `done`, `ask-agent` also takes `errored` and `none`, and none of
them types into an
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
`prompt_stalled`, `fan` records `prompt_status: stalled`, and `start-agent`
answers `prompt_status: stalled`. See
[protocol.md](protocol.md).

`start-agent` starts one agent in a new pane, here or on another machine with
`-s HOST:SESSION`, and types its first prompt through the same wait and the
same check. On another machine the agent runs there and reports there: its
state is that machine's, and reaches this one the way every host's agents do,
in the rail, `list-agents --all-hosts` and the Inbox.

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

### Teammates opened through the tmux shim

A pane that [the tmux shim](TMUX_SHIM.md) opens, a Claude Code teammate say,
runs `tuios tmux-pane` as its process, and the holder runs the teammate. The
holder gives its command a process group of its own and makes it the
terminal's foreground group, so the detector reads the teammate (or the
`sh -c` running it, which it walks as a wrapper) exactly as it reads an agent
started at a shell prompt. Its state, the Inbox and the rail work unchanged.
`explain-agent-detect` shows the teammate's process, not the holder.

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
`4;<state>;<percent>` for the others (1 set, 2 error, 4 paused). Every agent's
pane already gets the sequence's published meaning (a bar is working, clearing
it is idle, the error state is errored, paused is needs_input). A harness that uses
the sequence its own way, say indeterminate progress for "waiting on you", gets
its own reading: when a report arrives from a pane whose manifest has
`osc_progress` rules and one of them matches the report, the pane's title block
is read through the ordinary look and the published meaning is not applied.
When none matches, the published meaning applies, so a manifest names only the
reports it reads differently. No bundled manifest has one: herdr's progress
rules for Grok, Kiro, Qwen Code and Claude Code agree with the published
meaning.

The sequence counts as agent state only on a pane already known to hold an
agent: one with a state from any source, a harness named on it, or a harness
process recorded in it. Package managers, build tools and downloaders draw
their progress bars with the same sequence, and on its own it says a program
is busy, not that an agent is there. So a plain shell pane that emits OSC 9;4
gets no state mark, no silence timer and no Inbox entry. The report is still
kept on the pane, where `osc_progress` rules read it. An agent that is
detected a moment after its first report picks up the next one; a harness that
reports only through the sequence is recognised by its process, as any other.

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

tuios draws a one-cell mark for each pane's state. It is the same mark, in the
same colour, on every surface that shows a state: the rail and its collapsed
strip, the window title, the command palette, the session switcher, the
aggregate view, the Inbox and the dock's notifications.

| State                     | Mark      | Colour   | `--ascii-only` |
| ------------------------- | --------- | -------- | -------------- |
| `working`                 | `●`       | info     | `*`            |
| `needs_input` (needs you) | `▲`       | warning  | `!`            |
| `done`, not yet looked at | `■`       | success  | `#`            |
| `done`, looked at         | `○`       | muted    | `o`            |
| `idle`                    | `○`       | muted    | `o`            |
| `errored`                 | `×`       | error    | `x`            |
| `unknown`                 | `□`       | muted    | `?`            |
| `none`                    | (nothing) |          |                |

The marks are distinct shapes rather than the same shape in different colours,
so the state reads at a glance and survives a monochrome capture. None of them
is an emoji code point, and each is one cell wide. The mark shows even for a
window with no name.

Every mark is East Asian Ambiguous width, so a terminal that draws ambiguous
characters two cells wide (common with CJK locales) should run with
`--ascii-only` (or `appearance.ascii_only`, or the `ascii` glyph set). The
ASCII forms keep one meaning per character: mail is `@`, a thread from another
machine `~`, an Inbox item to resume `>` and mail waiting to be sent `^`, and
none of those is a state's mark.

The dock's notifications about an agent wear the state's mark in its colour.
They used to wear a Nerd Font severity icon, a different shape for the same
state that showed as a box without a patched font. Other notifications keep
their severity icon.

Mail is marked `@` in both modes: on the rail's agents header and rows, in the
mailbox and in the Inbox. It was the envelope U+2709, an emoji code point that
a terminal falling back to an emoji font drew as a colour picture. The mailbox
says a thread's kind in words ("docs asks api", "Notice to all") rather than
with a mark of its own.

This table used to say `unknown` draws nothing. It has drawn `□` since the state
was given a glyph, because a pane with an agent in it that drew nothing read as
a pane with no agent.

## Before the first agent

A person who never runs an agent is not shown controls for one. Until an agent
has been seen, the client leaves out:

- the Inbox lines of the prefix menu (`i`, `o`, `M`);
- the palette's "Agents: ..." entries and its `@ state` hint;
- the help overlay's Agents section;
- the agent rows of the Alerts settings, folded under an "Agents" row that
  says how many it holds and unfolds them when changed.

Nothing is removed: every key, the palette's `@` filter and every option work
the same before and after. An agent counts as seen once any pane in any session
this client can see has an agent state or a named harness, the Inbox holds an
item, mail arrives, or `tuios integration install` has put tuios's hooks into a
harness on this machine (read once at start). The client remembers it in its
rail state (`agents_seen` in `sidebar.json`), so the controls stay once they
have appeared. The rail's agents section already stayed hidden until an agent
existed.

## The rail's agents section

The agents section of the session rail lists every pane running an agent, in
every session, and is ordered by what each one needs from you. The header's
sort control (`o` with the rail focused, or a click on it) steps through three
orders: `you`, `pri` and `rec`.

`you`, the default, draws four groups, top to bottom:

1. **Needs you**: `needs_input` (an approval or a question) and `errored`.
2. **Done**: `done` that you have not looked at yet.
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
above), and a finished pane you have looked at draws `○`, idle's glyph, where it
used to draw `■` in a muted colour: read and unread finished panes differed only
in ink. The title bar, the palette, the session switcher and the aggregate view
follow the same rule; they drew `■` for a read finished pane until the agent UI
polish pass.

The row's second line carries a word for what the row needs, the `need` token:
`approval` or `question` when a screen rule read the prompt (the kind is taken
off the front of the message so it is said once) or when a hook reported the
kind with a message that does not name it (`approval · claude · approve Bash:
make`), `needs you`, `errored` or `done` when the pane reported no
message of its own. On a narrow rail a row that needs you drops the harness
and metadata before it cuts what the pane is asking. A row that needs you
also shows how long it has waited: at the right edge of the first line when the
rail is wide enough for the elapsed column, and after the need word otherwise
(`approval 12m`, or `waiting 12m` when the message stands in for the word).

A row says how long only once the pane has been in its state for five minutes;
before that the age is left off, because a fresh `<1m` on every row said nothing
the mark did not. The row under the keyboard cursor or the pointer shows its
age at any size, and so does the hover tooltip.

On a rail 30 columns wide or narrower (the shipped rail is 24), a pane running
an agent is listed once, in the agents section, where its note line has room
to say what it wants. The terminals section keeps the panes that run no agent,
and the focused pane's focus mark moves to its agents row. Before this an
agent at 24 columns was listed three times: its terminals row, its agents row,
and that row's note line. A wider rail lists agent panes in both sections as
before, and so does a peek at another session, which asks to see its panes. A
rail too short to draw the agents section (under 8 lines) keeps every pane in
terminals.

Every in-flight state draws the one working glyph, `●`, whichever source
reported it (a hook, an OSC 9;4 progress report, a screen rule or the process
detector).

### What the second line says

The second line says the one thing the row's group is about:

- **Working:** what the agent is doing now, such as `Bash: go test ./...` or
  `Edit: src/app.tsx`, from the `now` key its hooks feed (see
  [Agent metadata](#agent-metadata)). With nothing running now, the message it
  reported, as before.
- **Needs you:** the need word and what the pane asks, as above.
- **Finished and not yet seen:** the turn's first line, which with the Claude
  Code and Codex hooks is the first line of what the agent last said.
- **At rest** (idle, unknown, or finished and seen): nothing. A row at rest
  used to keep its last message; that note is old news, and the row's glyph
  already says it is at rest.

Two figures join them. Once the agent's context is 80% full or more, the line
starts with `ctx 84%` in the warning ink, the moment it is worth a look; below
that it says nothing. And while messages wait in the pane's queue (see
[Queued messages](#queued-messages)), the right edge of the first line says
`1 queued` in place of the elapsed time. A row that needs you keeps its wait
there, since nothing queued is typed until the prompt is answered.

```
│ agents      2 need you
│▎▲ review          4m
│    risky · rm -rf bu…
│ ■ docs
│    Updated CHANGELOG…
│ ● api       1 queued
│    ctx 84% · Bash: go…
│ ● web
│    Edit: src/app.tsx
```

The model and the cost are not on the rail unless you place them: they are in
the Inbox's detail for a finished item and in the header of a prompt `space`
opens, as `claude · opus 4.7 · 42% ctx · $1.20`.

These are row tokens like the rest (see `[appearance.sidebar.agent_row]` in
[CONFIGURATION.md](CONFIGURATION.md)): `now` (only while working), `context`
(only at 80% or more) and `prompt` (the first line of the last prompt you gave
the agent, not shipped on the row), beside `$model`, `$cost`, `$plan` and
`$key` for any key, which draw a value on any row. The shipped order is
`session, need, harness, name, elapsed, context, meta, now, message`: `now`
comes last so a long command loses its tail before anything else does.

### Agent metadata

A pane can report short facts about its agent with `set-agent-meta`: the
model, how full its context is, the cost of the turn, a one-line summary.

```sh
tuios set-agent-meta -w "$TUIOS_PANE_ID" --source statusline --ttl 60s model=opus context=42%
```

The `meta` row token draws every key on the second line, values only, in the
order the pane first reported them, so write values that read on their own
(`42% ctx` rather than `42`). It leaves out the keys tuios feeds itself
(`now`, `prompt`, `model`, `context`, `cost` and `plan`), which have tokens of
their own, so the model and the cost of every agent are not on every row; it
drew them until the rich rows landed. `$name` places one key, and `meta` then
leaves that key out:

```toml
[appearance.sidebar.agent_row]
tokens = ["session", "need", "harness", "name", "elapsed", "$model", "$context", "meta", "now", "message"]

[appearance.sidebar.agent_row."$context"]
fg = "warning"
```

Metadata is display only. It never changes a state, a wait, an alert or a
message. It is capped at 16 keys per call and 32 per pane, values are cut to 80
characters with control characters removed, keys set with a TTL are dropped by
the daemon when it runs out, and everything clears when the agent leaves the
pane. `get-agent-state` and `list-agents` report it as a `meta` object. See
[the protocol reference](protocol.md#set-agent-meta).

The Claude Code and Codex hooks feed three keys from what the agent does (see
[What the agent has been doing](#what-the-agent-has-been-doing)):

- `now`: the tool it is running and on what, such as `Bash: go test ./...` or
  `Edit: src/app.tsx`. It is cleared when the tool fails, when the turn ends,
  when a new prompt starts, and whenever the pane comes to rest: any move to
  a state other than `working` or `needs_input` clears it, whether a hook
  reported the move with activity, without it (an `idle_prompt`, a Codex
  `Interrupt`, a Gemini `AfterAgent`), or a screen rule, an OSC sequence or
  the silence timer made it.
- `prompt`: the first line of the last prompt you gave it.
- `model`: the model the harness named (Codex names it on every event), unless
  the pane already shows that model from another feed.

`now` and `prompt` are tuios's own: `set-agent-meta` refuses them, and its
`--clear` leaves them. Writing a key the value it already holds changes
nothing and sends nothing to attached clients, and a TTL is renewed only once
less than half of it is left, so a status line may write on every tick.

#### What feeds it

Three feeds write the keys `model`, `context` (`42%`), `cost` (`$1.20`) and
`plan` (`3/7`, steps done of the plan's steps). Each writes only what its
harness states: a field the harness does not send is never written, and its
token draws nothing.

| Feed | Keys | Source | How it is turned on |
| ---- | ---- | ------ | ------------------- |
| Claude Code's status line, through `tuios agent-statusline` | `model`, `context`, `cost` | `statusline` | `tuios integration install claude-code --statusline` (opt in) |
| The opencode and Kilo plugin, through `tuios agent-statusline` | `model`, `cost` (the sum of the session's assistant messages) | `statusline` | the plugin `integration install opencode` writes |
| A protocol pane (`start-agent --protocol`) | `model`, `context`, `plan` from Codex; `model`, `context`, `cost`, `plan` from ACP | `protocol` | always |

The status line feed is opt in because Claude Code has one status line slot
and it may be yours. Install never replaces a status line you wrote: it
refuses and prints the command that keeps it, `--then` with your command,
which the wrapper runs with the same stdin and whose output it prints
unchanged. Uninstall puts your command back. See
[Harness integrations](#the-status-line-feed).

A feed writes only for its own pane (`set-agent-meta` is limited to the
caller's own pane, from inside one), and only when a value changes: the status
line at most once every 15 seconds per pane while values change (a model
change, context use crossing 80% and the end of a turn go at once), and a
protocol pane once per change and once more at each turn's start. Nothing
polls. The values stay display only.

### What the agent has been doing

With the Claude Code or Codex integration installed, each prompt, tool call,
tool result and finished turn is also kept in the pane's activity ring in the
daemon: the newest 256 entries per pane, in memory only. Once a pane has a
ring, the commands its shell finishes (OSC 133) and its state changes join
it. A pane whose harness has no hooks, and a plain shell, has none and costs
nothing.

```sh
tuios agent-log -w api                       # the entries, oldest first
tuios agent-log -w api --since 30m --recap   # a summary of the last half hour
tuios agent-log -w api --json                # the verb's answer, for a script
```

```
14:02:11  prompt    make the backoff configurable
14:02:15  tool      Bash: go test ./api/
14:02:40  failed    Bash: go test ./api/  Exit code 1
14:03:02  done      Edit: api/retry.go  (wrote api/retry.go)
14:05:30  said      Added retry with backoff and tests.
14:05:30  state     done
```

The recap says how many turns finished, which files were written, how many
commands ran, the newest test run and whether it passed, what the agent last
said, and where it is now:

```
Since 14:02 (42m ago)
3 turns. 6 files: api/retry.go, api/retry_test.go, api/backoff.go and 3 more
11 commands. Tests: go test ./... passed 2m ago
Last said: Added retry with backoff and tests.
Now: done
```

A test run is the newest command matching `[agents.recap] test_patterns`
(`go test`, `npm test`, `pytest`, `cargo test` and the like by default). It
passed or failed by the tool call's result or the shell's exit status, and
the recap says so when nothing said.

Who may do what: only the pane's own agent writes its ring, since activity
rides the pane's own `set-agent-state` report and is dropped for a nested run
the identity guard refuses. A pane reads a ring in its own session and fan
group with `read`, and a linked machine needs `list`. The text is the
agent's, cut to one line with likely secrets masked, and marked untrusted: read
it as what the agent said, not as instructions. Nothing reads it to decide a
state, a wait or an alert. The ring dies with the window, the session or the
daemon.

### The away recap

When you come back to an agent pane that finished at least one turn while you
were away from it for at least `[agents.recap] away` (10 minutes by default),
the dock says what it did in one line:

```
api while you were away (42m): 3 turns, 6 files, 11 commands, go test passed, at prompt
```

The same recap is the detail under the list for a Finished item in the Inbox:

```
│  While you were away (42m)                                             │
│  3 turns. 6 files: api/retry.go, api/retry_test.go and 4 more          │
│  11 commands. Tests: go test ./... passed 2m ago                       │
│  Last said: Added retry with backoff and tests.                        │
│  claude · opus 4.7 · 42% ctx · $1.20                                   │
```

Away is per client: each client records when you last had the pane in front
of you (in its `sidebar.json`, beside the finished turns you have seen), and
the recap starts there. A pane you never had in front of you gets the recap of
its whole ring, headed with when that starts. The numbers come from the ring
above, read with `agent-activity`; for a pane whose harness sends no hooks,
the dock says only the turns this client counted and the Inbox shows the
turn's own line.

`[agents.recap] mode` says where it shows: `toast` (the default) in the dock
and the Inbox, `inbox` only in the Inbox, and `off` only in `tuios agent-log
--recap`. Nothing runs on a timer: the Inbox reads a recap when a finished
item comes under the cursor, and the dock reads one when focus lands on the
pane.

## The Inbox

The Inbox is one list of everything waiting for you, in every session on the
daemon: approvals and questions an agent is blocked on, questions an agent put
to you with `ask-human`, mail an agent wrote to you, agents that errored,
conversations a daemon restart left to resume, and finished turns you have not
looked at. The daemon
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

An agent `fan` or `start-agent` started that has not shown it is at its
prompt for 30 seconds, and is not on `needs_input`, gets a question too: "waiting
at a screen tuios does not recognise: look at the pane and answer it". It is
most often a first-run choice, such as a theme picker, that no rule reads and
only you can answer. The question goes when the agent's state changes, or when
its first prompt is typed or given up on; `fan` types the prompt as soon as
the agent is ready.

On a daemon restart, finished and errored rows whose pane came back are kept.
Approvals and questions are dropped, since the prompt died with its process,
and so is mail, since the messages it points to do not survive a restart.
Resume rows are opened by the restore itself; see
[Resuming after a restart](#resuming-after-a-restart).

Keys, after the prefix (`ctrl+b` by default):

| Key | What it does |
| --- | --- |
| `i` | Open the Inbox. |
| `o` | Go to the oldest item that needs you (approval, ask, question, mail, errored, resume), switching session and workspace. A question an agent asked you opens the Inbox on it instead, since that is where it is answered. The prefix stays armed, so `o` again goes to the next one, and past the last it starts over. Finished turns are left to the Inbox and to `O`. |
| `O` | Go to the newest finished turn you have not seen. `O` again within 5 seconds goes to the next older one; a turn that finishes meanwhile starts the walk over at it. It waits until an agent has been seen: before that the key after the prefix reaches the pane as it always did. |
| `M` | Open the Inbox on its mail. It used to open the mailbox; `m` in the Inbox does that now. |

Inside the Inbox:

| Key | What it does |
| --- | --- |
| `j` / `k`, arrows | Move. Group headings are skipped. |
| `g` / `G` | First and last item. |
| `enter` | Go to the item's pane, switching session and workspace. On mail, open the thread. On a held approval, give the prompt back to the pane first, so the harness shows it there. |
| `space` | On an approval or a question, read the prompt without leaving the Inbox, and answer it from there. See [Answering a prompt without attaching](#answering-a-prompt-without-attaching). Not on a held approval, which has no prompt on the screen: answer that one with `1`, `2` or `3`. |
| `1` / `2` / `3` | Answer the held approval under the cursor: allow once, always allow, deny. The same order as the harness's own menu. Only the keys the prompt offers work, and only once the prompt has been on screen as it is for 0.4 seconds. The whole prompt, and what `2` adds, is shown under the list. On a risky approval, `1` and `2` need a second press of the same key within 3 seconds. On a plan they are approve and approve with accept edits, and work once the plan's last line has been shown; `3` keeps it planning. See [Approvals from the Inbox](#approvals-from-the-inbox), [Risk rules](#risk-rules) and [Plans](#plans). |
| `n` | On a held approval or plan whose harness takes a reason: deny, or keep planning, with a reason you type on the `Reason:` line. `enter` sends it, `esc` drops it. See [Deny with a reason](#deny-with-a-reason). |
| `J` / `K`, `ctrl+d` / `ctrl+u` | Scroll a plan shown under the list. |
| `1` to `9` | On a question put with `ask-human`, pick that answer. The question and its numbered answers are shown under the list, and the keys work once it has been on screen as it is for 0.4 seconds. See [Questions an agent asks you](#questions-an-agent-asks-you). |
| `r` | Reply to mail: the thread opens with its reply line. |
| `y` | On a resume row: go to the pane and type the conversation's resume command there. |
| `p` | On a row that says `held for NAME`: pass the mail another machine sent that agent on to it. See [Mail held from another machine](#mail-held-from-another-machine). |
| `d` | Dismiss the item. The dock says `u` undoes it, and for 10 seconds it does. |
| `z` | Snooze the item, then `1` 15 minutes, `2` an hour, `3` until 9:00 tomorrow, `4` until it changes. The footer shows the four while it waits for the digit; any other key cancels. On a snoozed item, wake it. See [Snoozing, undo and unread](#snoozing-undo-and-unread). |
| `u` | Undo the last dismiss or snooze made in the last 10 seconds. Again, the one before. |
| `S` | Show or hide the snoozed items, muted, under a Snoozed heading at the bottom. |
| `f` | Show one kind, then the next, then all of them. |
| `/` | Type a selector that narrows the list, such as `harness:codex needs:you` or `session:api-fan-*`: the syntax of [Selectors](#selectors). `enter` applies it and an empty line clears it; `esc` closes the line and keeps what was in force. While the line is open every key is text. The selector stays until you change it, and the title says it in words, `[select harness:codex]`, so a narrowed Inbox never reads as an empty one. It works with `f`. |
| `m` | Open the mailbox, with every thread including the ones between agents. |
| `esc` / `q` | Close. |

These are the default keys. Every one but the digits can be rebound under
`[keybindings.inbox]`, `[keybindings.inbox_peek]` and `[keybindings.mail]`
(see [KEYBINDINGS.md](KEYBINDINGS.md#the-inbox)), and the footers name the key
the config binds. The footer lists only the keys that act on the selected row,
the one that answers it first.

Rows are grouped under headings in words, Approvals, Questions, Mail, Errored,
Resume, Done, each with its count, and oldest first inside a group. Questions
holds both a question an agent's prompt asks and one put with `ask-human`; they
were two groups, "Questions" and "Asked you", and `f` now steps over the
second. A row names its session as the rail does, by its display name, or by
its directory when tuios made the name up. A row carries
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
approvals, questions, asks and errored items are `blocked`, finished items are
`done`, over every session on every machine, or only this session when the
filter says `here`. An item of a machine whose link is down is not counted.
Without the Inbox (an older daemon, or while reconnecting) the header counts its
rows, as it did before.

### Questions an agent asks you

An agent, or any script, that needs a decision calls `tuios ask-human` with a
question and its answers:

```sh
tuios ask-human 'Deploy the branch to staging?' -o yes -o no -o later
```

The question is a row under Questions in the Inbox, and the call waits for you:

- When your client shows the pane that asked, the Inbox opens on the question
  by itself. This is the popup: you are looking at the agent, so its question
  comes to you. Press the answer's digit. For a moment after it opens, every
  key is dropped with a word, so a key you were typing to the agent does not
  dismiss, close or answer the question. It does not pop, and alerts instead,
  while you are typing into the pane, or while an overlay is open, the Inbox
  included: an open Inbox keeps its cursor, peek and text line.
- When the question comes from any other pane, nothing takes the keyboard. It
  raises the usual alert (dock message, notification, sound, under
  `[notifications.agent]` like a `needs_input`), and waits in the Inbox.
- With nobody attached, it waits in the Inbox for the next attach.

While the question is open, the pane that asked reads as needing you on every
surface: the `▲` mark on the rail and its title bar, a row in the rail's agents
section carrying the question, and a match for `@n` in the palette. Asking
changes no agent state on the daemon (`list-agents` is unchanged); the client
draws it from the open Inbox item, and the pane goes back to its own state
once the question is answered or dismissed.

Only you can answer. The daemon takes an answer only from a client attached
right now, with the nonce its attach carried, from a process outside every
pane, the same proof `reply-approval` takes; an agent calling `answer-ask`
gets `not_human`. The answer has to be one of the question's own answers, and
the question has to be the one you read.

When the agent's wait (two minutes by default) runs out first, the call returns
`pending` and the question stays. Your answer, when you give it, is mailed to
the asking pane from `human`, marked `verified_human`, where `wait-for
agent-message` picks it up. The same happens when the call was killed while
it waited, as a harness does to a command that runs past its limit. `d`
dismisses a question, and the call returns
`dismissed`. A pane has one open question: asking another supersedes the
first. Closing the pane ends its question.

A question and each answer are one line of printable text, at most 160 and 60
bytes, shown exactly as written. For anything longer, the agent writes you mail
and asks the short question here. Questions do not survive a daemon restart.

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

## Selectors

A selector addresses every agent pane that fits a description, where a window
id addresses one. It is one line of terms, read the same way by every place
that takes one:

```
harness:codex state:idle,done session:api-fan-*
```

Terms are separated by spaces and all of them must match. A term is
`key:value`, and a comma inside the value gives alternatives, any of which may
match.

| Key | Matches |
| --- | --- |
| `harness:` | The harness id, or a program name a manifest detects: `claude` is `claude-code`. A bare word also matches the id it starts (`gemini` is `gemini-cli`). |
| `state:` | The agent state, one of the states above. |
| `needs:you` | A pane a person has to act on: `needs_input` or `errored`. |
| `session:` | The session name, a glob. |
| `group:` | The fan-out group of the pane's session, the branch stem `fan` used, a glob. A session outside a fan-out has none. |
| `host:` | The machine: `local` (or this machine's own name), or a host from `[hosts]`, a glob. |
| `name:` | The window's name, a glob. |
| `cwd:` | The pane's directory, or any directory under it. `~` is the home directory. |

A glob is `*`, `?` and `[...]`, and `*` does not cross a slash, so `group:fan/*`
is every group under `fan/`. A term the pane cannot answer does not match, so
a selector never includes a pane by accident: a pane with no group fails every
`group:` term, and a row from a host too old to send its group fails one too.

Where a selector is read:

| Where | What it does |
| --- | --- |
| `list-agents --select` | Lists the agent panes it matches in every session, or in `--session`. Over every session, the answer carries `confirm`, the token for exactly those panes. |
| `list-agents --all-hosts --select` | The same over every machine. `host:` picks the machine. |
| `list-attention --select` | Keeps the Inbox items it matches. An item's state is the one its kind stands for: `needs_input` for an approval or a question, `errored`, `done` for finished. |
| `wait-for agent-state --select` | Waits for the first matching pane to reach an `--until` state, or with `--every` for all of them. Panes that open during the wait are watched too. |
| `send-agent-message --select` | One directed message to each matching pane, after confirmation. At most 32. |
| `ask-agent --select` | Asks each matching pane at once, after confirmation. At most 16. |
| The Inbox, `/` | Narrows the list. `cwd:` is not known there and matches no item. |

A write by selector is never silent. Without a `confirm` token it sends nothing
and fails with `confirm_required`, whose hint lists the panes in `available`
and carries the token in `confirm`. The token is a hash of the set of panes, so
the second call goes ahead only if the selector still matches exactly the panes
the caller looked at; a pane that joined or left in between gets the call
refused again, with the new set. `list-agents --select` gives the same token
for the same set. The CLI prints the set and asks at a terminal, and takes
`--yes` or `--confirm TOKEN` otherwise.

Each pane of a write goes through every check a single call makes. A message
is charged to the sender's rate cap per pane, and `from` may name the sender's
own pane in another session by its exact id. An ask refuses a pane on
`needs_input` with `agent_blocked` in that pane's row, waits for a working pane
unless `force`, and records the exchange in the pane's own session; one pane
that refuses does not stop the others.

A selector reaches panes on this machine and nothing else for a write, and a
pane on another machine whose calls run through its owner (see
[A pane on another machine](#a-pane-on-another-machine)) cannot use one at
all: its calls act as the window it is drawn in, and a selector would reach
every session of the owner. That call is refused with `forbidden`.

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
- Or the caller is a pane the person gave the `respond` grant (see
  [What a pane may do](#what-a-pane-may-do)), answering a pane in its own
  session, in its fan group when it also holds `fan`, or anywhere when it also
  holds `admin`. The result names it in `by_pane` and the daemon log records
  it. No mode gives `respond` by default, `admin` does not include it, and a
  pane can give it only when it holds it, so it comes from the person: a
  supervisor agent the person trusts to approve its workers' tool calls.

A caller inside a pane without the `respond` grant is refused with
`not_human`, even with a live nonce copied out of the person's client. Such an
agent cannot approve its own tool call or another agent's through tuios. A key that `send-keys` or `run-command`
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
version 2 of it is the one that gives the hook time to wait. A pane
`start-agent --protocol` opened needs none of this: see
[Headless agents over a protocol](#headless-agents-over-a-protocol).

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

Claude Code's `AskUserQuestion` is not held: its answer is a choice, not yes or
no, and stays in Claude Code's own dialog. `ExitPlanMode` is held as a plan:
see [Plans](#plans).
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

### Snoozing, undo and unread

Some of what lands in the Inbox is for later. `z` on a row snoozes it: it
leaves the list and the counts, and comes back, with its place in the order,
at the time you picked (15 minutes, an hour, 9:00 tomorrow), or when what it
is about changes. A pane that reports a new question, a new error, another
finished turn, or a hook that starts holding the approval wakes it early; the
same report again does not. When what it was about ends while it sleeps (the
agent moves on, you look at the pane) it is gone rather than woken. The list
says how many are snoozed and `S` shows them under a muted Snoozed heading,
each with when it wakes; `z` on one wakes it now. `tuios list-attention
--snoozed` lists them too.

Snoozing is for what can wait: finished turns, errors, mail, resume rows, and
approvals and questions no hook is holding. An approval the Inbox holds for
your answer, a plan and a question put with `ask-human` are answered or
dismissed, because an agent is waiting on them.

A dismiss or a snooze can be undone for 10 seconds with `u` in the Inbox,
and the dock says so. Mail waiting for another machine is not restored (its
dismiss discarded the mail), nor is a question put with `ask-human` (its asker
was told it was dismissed).
A held approval or plan comes back without its hold, since the dismiss ended
it and gave the prompt back to the pane: a plan returns as the pane's
approval, to answer in the pane.

On the rail, with the cursor on an agent row, `u` marks the pane's finished
turn unread: the row reads as finished again, and its Done row opens in every
client's Inbox marked unread until someone looks at the pane. The pane in
front of you cannot be marked, since looking at it is what marks it seen. `z`
opens the Inbox on the pane's row with the four lengths, and closes it once
one is picked.

Rows long at rest fold: agent rows that have been idle, unknown, or done and
already seen for longer than `appearance.sidebar.agent_rest_fold` (an hour by
default) become one muted line at the end of the agents section, `+3 at rest`,
with their names under it when the section has room. `enter` on it or a
click shows them until the rail lets go of the keyboard, or, for a click
while the rail did not have the keyboard, until a click outside the rail or
a pane is focused. A row that needs
you, a turn you have not seen, a working agent, the pane you are in and one
with messages queued never fold, and one row alone does not.

Who may do what: only you, from an attached client. Snooze, wake, unread and
restore are `mark-attention`, which takes the nonce your client got when it
attached, is refused to a pane without the `admin` grant and to anything
inside a pane even with the nonce, and needs `respond` over a link. An agent
cannot hide, reorder or bring back what you read. Snoozing another machine's
item hides it on this one only, like a dismiss, and `u` on the rail acts on
this machine's panes.

Attached to an older daemon, one whose `list-verbs` has no `mark-attention`,
the client finds that out once per attach and leaves these keys out: the
Inbox footer offers no `z`; `z`, `u` and `S` do what an unbound key does, a
dismiss does not say `u undoes`, and `u` on the rail clears only this
client's seen marks. The keys come back once the daemon is restarted with a
newer tuios.

Away since: the client also records, per agent pane, when you last had it in
front of you (`agent_seen_at` in `sidebar.json`, by window id, beside the
seen turn counts). It is written as the focus enters and as it leaves a pane
an agent has reported on, never for a plain shell, so for a pane out of view
it is when you looked away. It is where "while you were away" starts.

None of it costs anything without agents: the one timer that wakes snoozed
items exists only while something is snoozed with a time, the fold reads
the state stamps the rail already has, and a focus change between plain
shells records nothing.

### Risk rules

An approval whose command matches a risk rule is marked risky: its row reads
`risky:` before the line, the rail's need word is `risky`, and the detail under
the list names each rule and why. `1` and `2` then allow it only on a second
press of the same key within 3 seconds; the first press says
`Press 1 again by 14:03:07 to allow rm -rf build/` and sends nothing. The time
is when the first press lapses: nothing redraws the line then, so after it a
press of `1` only starts over. Any other key, a cursor move or the item
changing resets it. `3` and `d` deny with one press.
The peek holds `a`, `A` and a digit to the same rule on a risky prompt.

The daemon enforces this, not only the Inbox: an allow of a risky call must
name exactly the rules it matched (`risk_ack`), or it is refused and nothing is
answered. A client older than the rules sends none, so it cannot allow a risky
call from the Inbox at all; answer it in the pane. A pane holding the `respond`
grant may deny a risky prompt and never allow one, unless
`panes_may_allow = true`.

The shipped rules, each matched against every command of a shell call, after
`sudo`, `env` and similar wrappers. A call is split on `;`, `&&`, `||`, `|`,
`&`, newlines and a subshell's `(` and `)`, and followed into `$( )`,
backticks, `<( )` and `>( )`, a shell's command line (`sh -c`, and a flag
cluster holding `c`, such as `bash -lc` or `sh -ec`) and `eval`. The words
that can come before a command without being it (`{`, `}`, `!`, `if`, `then`,
`elif`, `else`, `while`, `until`, `do`) are skipped, so `if true; then rm -rf
x; fi` and `(cd build && rm -rf out)` read as `rm`:

| Rule | Matches |
| --- | --- |
| recursive delete | `rm` with both `-r` (or `-R`, `--recursive`) and `-f` (or `--force`), in any order |
| force push | `git push` with `--force`, `-f`, `--force-with-lease`, or a `+` refspec |
| hard reset | `git reset --hard` |
| clean | `git clean -f`, `-fd`, `-fx` |
| discard changes | `git checkout -- .`, `git checkout .`, `git restore .` |
| pipe to shell | `curl` or `wget` piped to `sh`, `bash`, `zsh`, `python`, `node`, or to anything under `sudo`; or run by a shell, `eval` or `source` through a substitution, as in `bash <(curl ...)`, `sh -c "$(curl ...)"` and `eval "$(curl ...)"` |
| sudo | any command under `sudo` or `doas` |
| disk | `dd of=`, `mkfs`, a redirect to `/dev/sd*`, `/dev/nvme*` and the like |
| wide permissions | `chmod -R 777`, `chown -R` on `/` or `~` |
| database | `DROP TABLE`, `DROP DATABASE`, `TRUNCATE TABLE`, in any case |
| infrastructure | `terraform apply` or `destroy`, `kubectl delete`, `docker system prune`, `npm publish`, `cargo publish` |
| outside the worktree | a redirect, `tee`, `cp`, `mv`, `ln`, `install`, `rm`, `touch`, `sed -i` and the like naming an absolute or `~` path outside the pane's worktree root (else its working directory), or a `Write` or `Edit` approval of such a path. `/dev/null` and the terminal are not outside |

Add your own, or turn the shipped ones off, in the config:

```toml
[agents.approvals.risk]
builtin = true            # keep the shipped rules
panes_may_allow = false   # a pane with the respond grant may not allow a risky call

[[agents.approvals.risk.rule]]
name = "kubectl apply"
tools = ["Bash"]   # empty: every tool
pattern = '\bkubectl\s+(apply|delete)\b'   # RE2, matched per command
```

A rule's `tools` are the names the harness gives its tools, compared without
case. Naming any one shell tool (`Bash`, `bash`, `shell`, `exec_command`,
`local_shell`, `run_shell_command`, `execute`, `terminal`) covers them all and
a line with no tool, since harnesses name the same tool differently: a
protocol pane's line reads `approve execute: <command>` for a command and
`approve edit: <path>` for a file change. Naming any one file tool (`Write`,
`Edit`, `MultiEdit`, `NotebookEdit`, `write`, `edit`, `multiedit`, `patch`,
`write_file`, `replace`) likewise covers them all.

The rules are read from the file and again when it changes, and cannot be set
with `set-option`, so a pane cannot switch them off through tuios. An approval
nobody holds is matched on its line: tuios's own hooks report
`approve <Tool>: <what>`, read as that tool and argument; any other line is
read as a command. That line is clipped to 100 characters, so a risky part
past the cut is not there to match. A clipped line is therefore marked
`cut short` besides whatever the rules found: the allow takes the second
press, and a pane with the `respond` grant cannot give it. A held call is
matched on the whole command the hook sends, not on the line.

The rules are a speed bump, not a sandbox. A command written to hide what it
does (a variable holding `rm`, an alias, a script file) passes them. The
harness's own permission system stays the boundary; the rules make an allow of
a dangerous call take two deliberate presses.

### Plans

With approvals on for Claude Code, a plan it asks you to approve when it leaves
plan mode (its `ExitPlanMode` tool) is held too, under its own group, Plans,
between Approvals and the questions. The row is the plan's title and length,
`Refactor the retry loop (14 lines)`, and the rail's need word is `plan`. With
the cursor on it the plan is shown whole under the list, scrolled with `J` and
`K`:

| Key | What it does |
| --- | --- |
| `1` | Approve. Claude Code leaves plan mode and still asks before each edit. |
| `2` | Approve and accept edits for this session. Offered only when Claude Code suggests exactly that mode change, and the line under the plan says so. `bypassPermissions` and `auto` are never offered. |
| `3` | Keep planning. |
| `n` | Keep planning, with a reason you type, which Claude reads. |

`1` and `2` work only once the plan's last line has been on screen, and 0.4
seconds after that. On a screen too short to hold the whole Inbox panel, whose
bottom is then cut off, the last line does not count as shown: the detail says
so, and Enter answers the plan in the pane. The answer names the digest of the plan that was shown
(`plan_sha`), and the daemon refuses an approve for any other plan. Note the
order: `1` is the safest approval, which differs from Claude Code's own menu on
purpose. Turn plans off, and keep approvals, with `hold_plans = false` under
`[agents.approvals]`.

A plan is held only when its text fits (32 KiB) and it carries nothing but the
plan and its file. One whose `allowedPrompts` asks for command permissions,
which Claude Code before 2.1.205 granted with the plan, is answered in the
pane. When the hold ends without an answer the item is the pane's approval
again, and Claude Code shows its own dialog.

What the hook prints for an approve is the `PermissionRequest` decision with
`behavior: allow` and `updatedInput` set to the plan's input as it came:
Claude Code marks `ExitPlanMode` as a tool that needs the user, and it takes an
allow for such a tool only with `updatedInput` (checked against the hooks
reference and the 2.1.281 build). `2` adds one permission update,
`{"type": "setMode", "mode": "acceptEdits", "destination": "session"}`.

### Deny with a reason

`n` on a held approval or plan opens a `Reason:` line under it. `enter` denies
with what you typed, which the hook hands to the model as the deny's message;
an empty line sends the default ("The user denied this from the tuios Inbox.").
It is offered where the harness passes a reason on: Claude Code, opencode and
Kilo. The reason is cleaned and cut to 500 bytes, only you can send it (it is
a `reply-approval` with your attach nonce), and it reaches only the hold it
answers. A reason typed by `send-keys` is never sent.

### Review, triage, replies and safer approvals (being built)

The daemon already knows the shape of four pieces of work that are being
built, so their rules are fixed before any of them does anything:

- **Reviewing a pane's changes** (`review-diff`, `review-note`,
  `send-review`), and comparing the attempts of a fan (`compare-fan`,
  `verify-fan`, `keep-fan`). The review verbs are built, with `tuios review`:
  see [Reviewing an agent's changes](#reviewing-an-agents-changes). The
  review overlay in the client is not yet.
- **Triage in the Inbox** (`mark-attention`): snooze, wake, mark unread and
  undo. A snoozed item closes with the reason `snoozed` and opens again with
  the same id. This one is built: see
  [Snoozing, undo and unread](#snoozing-undo-and-unread).
- **Richer rows and queued replies** (`agent-activity`, `queue-prompt`,
  `list-queued`, `cancel-queued`): a message queued for a busy agent is typed
  when it comes to rest, and the rail shows how many wait (`queued` in
  `get-agent-state` and `list-agents`). This one is built: see
  [What the second line says](#what-the-second-line-says),
  [Queued messages](#queued-messages),
  [Replying to an agent](#replying-to-an-agent) and
  [The away recap](#the-away-recap).
- **Safer approvals** (`get-approval`, `risk_ack` and `plan_sha`): a new
  Inbox kind, `plan`, for a plan an agent in plan mode asks you to approve,
  which shares the pane's blocking item with its approval, so it closes when
  the pane leaves `needs_input`, and risk rules that mark an approval risky.
  The risk rules are a speed bump, not a sandbox: an obfuscated command can
  avoid a pattern, and the harness's permission system stays the boundary.
  `get-agent-state` and `list-agents`).
- **Safer approvals** (`get-approval`, `risk_ack` and `plan_sha`) are built:
  see [Risk rules](#risk-rules), [Plans](#plans) and
  [Deny with a reason](#deny-with-a-reason).

The activity ring behind `agent-activity` has landed: see
[What the agent has been doing](#what-the-agent-has-been-doing).

Comparing a fan has landed. `compare-fan` counts what each attempt changed
against the fan's base without touching its index or files, and reports the
last `verify-fan` check and the last command a shell in it finished.
`verify-fan` runs a command you give, never one read from the repository, in
a window named `verify` in each attempt; the window holds no grants, closes
when the check passes and stays open when it fails. `keep-fan` is `tuios fan
keep` moved into the daemon, so the TUI and the CLI share it.

Who may do what is settled now, whatever is built. A pane without the `admin`
grant reads a diff, a comparison, an activity ring, a queue or a held approval
only in its own session and fan group; writes notes and queues messages only
with `write`, and only into a pane that holds nothing it does not and is not
waiting on a prompt unless it holds `respond`; runs a check in a fan only with
`fan`; and never keeps a fan or changes the Inbox, which only you can, with the
nonce your attached client holds. Over a link, a diff needs `write` because it
carries file contents, and changing the Inbox needs `respond`. Every verb is
built; until a piece of the TUI lands its keys do what they did before they
were bound. [protocol.md](protocol.md#agent-review-triage-and-queue-verbs)
has the table.

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

### The MCP server

`tuios mcp` offers tuios to a harness as Model Context Protocol tools, so an
agent drives tuios with tool calls instead of shell commands it read about in
the skill. `--mcp` on install registers it with the four harnesses that read
MCP servers from a file tuios can edit:

```sh
tuios integration install claude-code --mcp        # read-only
tuios integration install codex --mcp-write        # plus the tools that type
```

| Harness | What is written |
| ------- | --------------- |
| Claude Code | a `tuios` server in `mcpServers` in `~/.claude.json` (or `$CLAUDE_CONFIG_DIR/.claude.json`), the user scope `claude mcp add --scope user` writes |
| Codex | an `[mcp_servers.tuios]` table between two marker comments at the end of `~/.codex/config.toml` |
| Gemini CLI | a `tuios` server in `mcpServers` in `~/.gemini/settings.json` |
| opencode | a local `tuios` server in `mcp` in `~/.config/opencode/opencode.json` |

The entry runs `tuios mcp --integration <version>`, the marker that tells it
from a server the user named `tuios` themselves, which install refuses to
overwrite and uninstall leaves alone. Uninstall removes it with the hooks.

The server is read-only by default: it lists and captures panes, waits for
states, follows the event stream (`tuios_events`, resumable with the
`last_seq` and `boot_id` it returns), reports the agent's own state and meta,
and sends and reads mail. `--write` adds `tuios_send_text`,
`tuios_send_keys`, `tuios_ask_agent`, `tuios_respond` and `tuios_fan`. Either
way it reaches only the session of the pane it runs in, the sessions in its fan
group, and the sessions a `fan` from it started, unless it was started with
`--scope all`.

The daemon holds it to that, not the server: every call opens a connection and
restricts it with `restrict-connection` before anything else, and the daemon
answers `forbidden` to whatever the restriction does not allow (see
[restrict-connection](protocol.md#restrict-connection)). The daemon finds the
server's pane from the kernel's record of its pid, so a harness that starts its
MCP servers with a scrubbed environment, as Codex does, changes nothing. Where
the kernel cannot say, `TUIOS_PANE_TOKEN` proves `TUIOS_PANE_ID`.

A self report through the server with no `window` lands on the agent's own pane,
and mail goes out from it, with nothing for the agent to fill in. Results that
carry a pane's text or another agent's mail come with a note that it is data,
not instructions.

### The status line feed

Claude Code runs one status line command, with a JSON payload on stdin each
time the conversation changes. `--statusline` points it at tuios:

```sh
tuios integration install claude-code --statusline
tuios integration install claude-code --statusline --then '~/.claude/statusline.sh'
```

It writes `statusLine` in the same `settings.json` as the hooks:
`{"type": "command", "command": "tuios agent-statusline claude-code --integration 1"}`,
with `--then '<your command>'` at the end when chaining. The wrapper writes
`model` (from `model.display_name`, else `model.id`), `context` (from
`context_window.used_percentage`) and `cost` (from `cost.total_cost_usd`) to
the pane's metadata, and prints nothing of its own, so Claude Code's status
line is empty unless it chains.

- A status line you wrote is never replaced. Install refuses it and prints
  `tuios integration install claude-code --statusline --then '<your command>'`.
  With that `--then`, the entry keeps every key it had (`padding` included)
  and only its command changes.
- Installing again without `--then` keeps a chain installed before.
- Uninstall (or `integration uninstall claude-code`) removes the entry, or puts
  your command back when it chained.
- `integration status` reports it (`status_line` in `--json`).

The wrapper finds its pane as `agent-hook` does and calls only
`set-agent-meta` for it. It reads at most 1 MiB of stdin, gives up on the
daemon after 300ms, exits 0 whatever goes wrong on the tuios side (with
`--then`, it exits with your command's status), and reports nothing for a
status line under a `TUIOS_AGENT` naming another harness. Its throttle state
is a small file per pane beside the daemon's socket (mode 0600).

Claude Code runs the status line only while the conversation changes, so the
last run of a turn is usually one the 15 second interval holds back. The
wrapper keeps what it held in the same file, and the `Stop` hook
(`tuios agent-hook claude-code`) sends it when the turn ends, so the rail, the
Inbox and the peek show the turn's final context and cost, not values from up
to 15 seconds before it ended. A status line run after the `Stop` goes at once
for the same reason.

### What each event reports

`tuios agent-hook` reads the payload on stdin (the Codex `notify` payload
arrives as the last argument) and sends one `set-agent-state`, or nothing. A
report that ends a turn also sends, with `set-agent-meta`, what the pane's
status line feed held back (see above).

| Claude Code event | Reports |
| ----------------- | ------- |
| `SessionStart` | `idle`, with the session id and transcript path. `source: compact` reports nothing |
| `UserPromptSubmit`, `PreToolUse` | `working`, with the prompt's first line or the tool call as activity |
| `PermissionRequest` | `needs_input`, kind `approval`, message `approve <tool>: <command or path>`. With approvals on, then waits for an answer from the Inbox (see [Approvals from the Inbox](#approvals-from-the-inbox)) |
| `PostToolUse`, `PostToolUseFailure`, `PermissionDenied`, `ElicitationResult` | `working`, only if the pane is `needs_input`. The first two also carry the tool's result as activity, which is kept whether or not the state applies |
| `UserPromptSubmit`, `PreToolUse` | `working` |
| `PermissionRequest` | `needs_input`, kind `approval`, message `approve <tool>: <command or path>`, or `plan: <the plan's title>` for `ExitPlanMode`. With approvals on, then waits for an answer from the Inbox (see [Approvals from the Inbox](#approvals-from-the-inbox) and [Plans](#plans)) |
| `PostToolUse`, `PostToolUseFailure`, `PermissionDenied`, `ElicitationResult` | `working`, only if the pane is `needs_input` |
| `Notification` `permission_prompt` | `needs_input`, kind `approval` |
| `Notification` `elicitation_dialog`, `elicitation_url_dialog`, `agent_needs_input` | `needs_input`, kind `question` |
| `Notification` `idle_prompt` | `idle`, only if the pane is `working` or `unknown` |
| `Notification` `auth_success` and the rest | nothing |
| `Stop` | `done`, with the first line of `last_assistant_message` as its message and as activity. A `Stop` without the field (older Claude Code) or with an empty one reports `done` with no message and a `turn_end` activity with no text |
| `StopFailure` | `errored`, message `stopped on <error_type>` |
| `SessionEnd` | `none` |
| `SubagentStop`, anything with `agent_id` | nothing |

Codex maps the same events the same way, plus `Interrupt` to `idle`, with the
same activity (its `apply_patch` files come from the patch's header lines, and
the model from every event). Gemini CLI maps `BeforeAgent` and `BeforeTool` to
`working` (`BeforeTool` with its tool name as activity), `AfterTool` to `working` only
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

Activity is sent only to a daemon whose `set-agent-state` lists it, like the
other hook fields; an older daemon gets the report without it. Its text, the
`done` message from `last_assistant_message` included, is the first line only,
held to the same rule as a message below.

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

## Headless agents over a protocol

Hooks, screen rules and transcripts read an agent that draws its own TUI.
Some agents also speak a structured protocol, and for those tuios can be the
client: `start-agent --protocol acp` (the Agent Client Protocol, version 1) or
`--protocol codex` (the Codex app-server) runs the agent headless, and the
pane shows the conversation as a plain transcript with a prompt line under it.

```sh
tuios start-agent --protocol acp 'opencode acp' --name helper --prompt 'List the TODOs.'
tuios start-agent --protocol codex codex --name tests
```

The pane's process is `tuios agent-proto`. It knows the agent's state from the
protocol itself, not from its screen, and reports it for its own pane with
`set-agent-state`, source `report`, under the harness the manifest named (or
`acp` or `codex` when none did):

| What happened | Report |
|---------------|--------|
| The conversation opened | `idle` |
| A prompt was sent | `working` |
| The turn ended | `done`, with the reply's first line |
| The turn was cancelled | `idle` |
| The turn failed, or the agent refused | `errored`, with why |
| The agent asked permission | `needs_input`, kind `approval`, with the request's line |
| The permission was answered | `working`, only if still on `needs_input` |
| The agent exited or did not start | `errored` |

`start-agent` is ready on that report alone: `unknown` never counts for a
protocol pane, whatever its harness. `list-agents` shows `protocol` for it.

It also writes the agent's model, context use, cost and plan progress to the
pane's [metadata](#agent-metadata), source `protocol`, each key once per
change:

| Key | Codex app-server | ACP |
| --- | ---------------- | --- |
| `model` | `thread/start`'s model, then `model/rerouted` | `session/new`'s `models` (the name of `currentModelId`), which ACP marks unstable |
| `context` | `thread/tokenUsage/updated`: `last.totalTokens` of `modelContextWindow`, when the window is known | `usage_update`: `used` of `size` |
| `cost` | not sent | `usage_update`: `cost.amount` in `cost.currency` |
| `plan` | `turn/plan/updated`: completed steps of all steps | `plan`: completed entries of all entries |

The `usage_update` shape is the ACP schema's (release 0.11: `used` and `size`
in tokens, and an optional `cost` of `{amount, currency}` marked unstable),
which is what opencode's ACP agent sends. Every field is read on its own, so a
missing or changed field costs only that key. A Codex pane now shows the
turn's plan in the transcript, as an ACP pane always has, and an ACP pane
whose agent names its model now shows it in the first line, `connected to
opencode 1.2 (Claude Sonnet 4)`, as a Codex pane always has. Every key is sent
again at the start of each turn, so values the daemon lost (a restart, or a
clear to `none`) come back without waiting for the agent to change them, and a
call that failed is tried again a few times.

Each prompt, tool call (once as it starts, once as it ends) and finished turn
also goes to the daemon as `set-agent-state` activity. Its state part is the
state the pane last reported, with `if_state` `none`: the daemon records the
activity and refuses the state part on any pane that has a state, so it
changes no state, keeps the time the pane entered it (the rail's elapsed time)
and pushes nothing. Only a pane whose state was lost mid-turn gets it back.
Activity and metadata go from a goroutine of their own on a connection of
their own, so a slow daemon never holds the pane's typing or its state
reports. Activity is sent only to a daemon whose `set-agent-state` lists
`activity`.

A permission is shown in the pane with a number key per answer, the agent's own
words for each. When one line shows the whole request (a command whose input
the title holds in full; never a diff, an edit, output, or input to a running
command), `agent-proto` also holds it for the Inbox with `request-approval`,
exactly as a hook would, except that a protocol pane needs no
`[agents.approvals]`: choosing the protocol is the opt in. The Inbox offers
allow once and deny, which mean exactly one call; the answers that allow more,
such as ACP's `allow_always` or Codex's `acceptForSession`, are the pane's. The
first answer wins: a key in the pane ends the hold, and the Inbox's answer is
written into the pane with who gave it. A digit counts only once the question
has been on screen for half a second, and a paste or Enter never answers.
A Codex file change approval is always the pane's, including one for an item
the pane has not seen, whose files it cannot show. When it carries
`grantRoot`, a request to allow writes under that directory for the rest of
the session, the pane says so under the title.

What the agent can do through tuios is nothing it could not do in its own TUI.
`agent-proto` advertises no file system and no terminal capability, answers
every request it does not handle with method not found (which Codex reads as
not approved), starts the agent in a session of its own with no controlling
terminal so it cannot write to the pane directly, and removes every escape
sequence and control character from what the agent sends before it reaches the
pane, so a reply cannot set the pane's state, title or clipboard.

## Typing a prompt

`ask-agent`, `fan`, `start-agent` and the delivery queue type a prompt the same way: the text as one paste,
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

## Queued messages

A message for an agent that is in the middle of a turn has nowhere good to go:
typed now, it lands in the middle of the turn, and `ask-agent` blocks its
caller until the agent is done. The delivery queue holds it in the daemon and
types it the moment the agent comes to rest.

```bash
tuios queue -w build 'make the backoff jitter configurable'
tuios queue ls
tuios queue rm q3
```

The verbs are `queue-prompt`, `list-queued` and `cancel-queued`
([protocol.md](protocol.md#agent-review-triage-and-queue-verbs)).

**When it is typed.** Nothing polls. The daemon acts on the agent-state
changes it already sees, and a pane with nothing queued costs nothing. A
message is typed when:

- the pane is at rest the way `fan` judges a fresh agent ready: `idle` or
  `done`, and `unknown` only for a harness whose rules can never show idle;
- it has been at rest for a second, so a message does not land in the moment
  an agent flickers to idle between two tool calls;
- after an earlier message, the rest was reached after that message was
  typed: one message per rest, and the next waits for the next rest. This
  holds when the queue emptied in between, so a message you queue just after
  the last one was typed does not land while the agent is still on it. The
  typing time is when the Enter went out, so a turn that ends while the daemon
  still watches for the agent to take the message counts.

A harness whose rules cannot show working (Aider, Crush, or a pane with no
harness) may sit on `unknown` before and after it takes a message, so no
state change marks the next rest. For such a pane a rest after a typed message
is also new output followed by 5 seconds of silence. Only such a pane, with
something queued, is looked at on a timer.

It is typed the way `fan` types its first prompt (see
[Typing a prompt](#typing-a-prompt)), and the daemon then waits for the agent
to show it took it: a state change, a finished turn, or, for a harness whose
rules cannot show working, new output. Taken, the message leaves the queue.
Not taken within the stall window (5 seconds), it is marked `stalled` and is
never typed again, so text is never typed twice, and the Inbox gets a
question on the pane: "your queued message was typed but api did not take
it: look at the pane". A stalled message holds the ones behind it until the
pane next shows `working` (the agent took it late, or you dealt with the
pane), which drops it, or until you drop it with `tuios queue rm`.

**What ends a queue.** It lives in the daemon's memory only. A daemon restart
drops every queue, since the conversation a message was for ended with the
process. A queue is also dropped when its pane closes, when the agent leaves
the pane (state `none`), and when its session ends, and a message a pane
queued is dropped when that pane closes. A pane holds at most
`[agents.queue] max` messages (8 by default), each at most 16 KiB; one more is
refused with `queue_full`.

**The count.** Each pane's window state carries `agent_queued`, the length of
its queue, and `get-agent-state` and `list-agents` report it as `queued`. It
is daemon-owned, like the agent state: a client never sets it.

**Who may do what.** Who queued a message is decided by the daemon from the
connection, never from a parameter, and `list-queued` shows it as `by`:

- `human`: the Inbox, with the nonce of a client attached now. A nonce that
  does not verify is refused with `not_human`, and a process inside a pane can
  never use one. Nothing more is checked when it is typed: you consented, as
  when you type.
- A pane's window id: a process in a pane. At queue time it is held like
  `send-text` (see [Typing into another pane](#typing-into-another-pane)): the
  `write` grant, a target in its own session (or fan group with `fan`) that
  holds nothing it does not, and not on `needs_input` without `respond`. When
  the message is typed, the pane's grants as they are then are checked against
  the target again, and a message they no longer cover is dropped, logged, and
  never typed. A pane can queue only as itself: `from` naming another window
  is refused.
- `link:HOST`: a machine linked to this one, which needs `write` in its
  `[hosts]` policy. The policy is read again when the message is typed, so
  taking `write` away drops what that machine queued.
- `shell`: a process outside every pane, such as your own shell or a script.
  It is held to nothing new, now or when typed.

Whoever queued it, the daemon refuses to type over a prompt: the pane is
checked for `needs_input` right before the message is typed, so a queued
message can never answer an approval or a question.

`cancel-queued` drops messages before they are typed. You, with the nonce, may
drop any. A pane may drop only what it queued, a linked machine only what it
queued, and a shell every message but yours. A message being typed cannot be
dropped. Dropping a stalled message closes its Inbox question and lets the
ones behind it be typed at the pane's next rest, one reached after the stalled
message was typed, since its text may still sit in the input box. Linked
machines are told apart by the name their link gave; two that gave none each
drop only what they queued on the same connection. A pane on another machine,
whose calls arrive through its report channel, may neither queue nor drop.

### Replying to an agent

`r` on a Finished or Errored item in the Inbox, or on a rail agent row, opens
a line under the Inbox list:

```
  Reply to api (done 4m): make the backoff jitter configurable_
  ↵ send when ready  esc cancel
```

`enter` queues it with `queue-prompt` as you: if the agent is at rest it is
typed within a second or so, and otherwise the row says `1 queued` and it is
typed the moment the agent comes to rest. From the rail the Inbox opens with
the line and closes again when you send or cancel. On a pane waiting on a
prompt, or with an `ask-human` question open (the rail draws both as waiting
on you), `r` says so and opens nothing: `api is waiting on a prompt. Answer it
first (space to peek).` A pane on another machine is replied to from a client
attached there.

`x` on a rail row with messages queued drops the newest one still waiting
(`cancel-queued`), and the dock says `Dropped 1 queued message to api. u
undoes.`: `u` on the same row within 10 seconds queues its text again, at the
end of the queue. Only a message this client queued can be put back, since
only its text is here; a message being typed cannot be dropped.

Who may do what: the reply carries this client's attach nonce, so the daemon
records it as `human`, and a nonce that does not verify is refused with
`not_human`; a process in a pane can never use one. A reply that any key from
`send-keys` or a tape touched is not sent at all, since a message queued as
you is typed without a check of whoever drove the keys. For the same reason
`x` and `u` on a rail row from `send-keys` or a tape do nothing to the queue
and the dock says so: both act as you, with the nonce. The drop stays, so
your own `u` within the 10 seconds still puts it back. Nothing new is
allowed: the daemon still refuses to type over a prompt, right before it
types.

## Reviewing an agent's changes

When an agent finishes, you read what it changed, leave notes on the lines
you want changed, and send the notes back as one message.

```bash
tuios review api-fan-retry-2              # the diff, with notes under their lines
tuios review note -s api-fan-retry-2 api/retry.go:42 'log the attempt number here too'
tuios review notes -s api-fan-retry-2
tuios review send -s api-fan-retry-2      # typed when the agent comes to rest
```

The verbs are `review-diff`, `review-note` and `send-review`
([protocol.md](protocol.md#review-diff)).

**What is diffed.** The pane's repository: the session's worktree when the
pane is in it, else the repository holding the pane's directory. The diff
runs from a base, in this order: the `base` you name; the base the worktree
was made from (`fan`, `worktree new`), or for one tuios made from `HEAD` the
main checkout's branch, as `compare-fan` counts it; the merge base with the
branch's upstream; else `HEAD`, which shows only what is not committed. A named or
recorded base is taken through its merge base with `HEAD`, so a base that
moved on since the branch left it does not show its own commits as removed.
`uncommitted` asks for `HEAD` directly. `against` names another attempt of the
same fan and diffs the two attempts with each other instead.

Committed and uncommitted work show together, untracked files included (status
`U`) and ignored files left out. The working state is written as a git tree
through a temporary copy of the worktree's index, so the repository's index,
its files and what the agent staged are never changed; the objects the tree
needs are loose objects that `git gc` collects. A diff stops at 400 files,
2 MiB of text or 5000 lines in one file, and the files past a limit are
listed with their counts only (`truncated`). Binary files have counts only.
All the git calls of one diff are bounded at 10 seconds together, and nothing
runs until a diff is asked for. The repository is read on the machine the
daemon runs on. Reviewing a session on a linked machine is not supported yet:
`tuios review HOST:SESSION` is refused before anything is dialled, and a pane
of a session here whose process runs on another machine is refused with
`not_repo`. Attach to that machine and review there, or bring the work here
with `tuios worktree pull HOST:SESSION` and review that.

**Notes.** A note sits on a line (`FILE:LINE`, on the new side, or with
`side: old` on a removed line numbered as in the base) or on a whole hunk (by
its header). It keeps the text of its line, filled in from the file when the
caller gives none, and every `review-diff` finds the line again: the same text
within 50 lines of where it was, else the nearest match in the file. White
space at the ends of a line, and a change in indentation, do not count. A
note whose line is gone is marked `outdated`, and is found again if the line
comes back. A note on a hunk moves to the hunk with the same header, else the
one that holds its line.

Notes are held by the daemon, so every client and the CLI see the same ones.
They are kept per worktree and per pane, at most 200 on a worktree and 1000
bytes each, and saved to `review/notes.json` under the state directory,
readable by you only. A restart keeps the notes of panes that came back. A
pane closing drops its notes, and so does removing its worktree
(`remove-worktree`, `worktree rm`, `fan keep`).

**Sending.** `send-review` composes the pane's unsent notes (or the ones named
with `ids`, sent before or not) into one message and hands it to the delivery
queue ([Queued messages](#queued-messages)): typed now if the agent is at rest,
else when it next comes to rest, and never over a prompt. With `now` it is sent
only when the agent is at rest with nothing queued, and otherwise refused with
`not_ready` or `agent_blocked`. The notes are marked `sent_at` when they are
queued. The agent receives:

```
Review notes on your changes (vs origin/main), from the person:

1. api/retry.go:42, on "if err == nil {"
   log the attempt number here too
2. api/retry.go:100-105 (hunk "@@ -88,4 +100,6 @@")
   wrap with context

Address each note, then say which you changed.
```

Notes are ordered by file, then line. A quoted line is cut to 120 characters
with control characters left out and anything shaped like a secret masked,
and a note's text keeps its lines with every other control character left
out, so nothing in it can end the paste it is typed in. A message longer than
16 KiB is refused: send fewer notes at a time with `ids`.

**Who may do what.** Who wrote a note, and who sent a message, is the daemon's
reading of the connection, never a parameter, as for the queue:

- `human` only with the nonce of a client attached now. A process in a pane
  can never use one, so a pane cannot write a note that reads as yours, and
  the message says "from the person" only when you sent it. From a pane it
  says "from pane NAME", from a linked machine "from a caller on HOST", and
  from a shell "from a script".
- A note written by someone other than the sender carries its author under
  its place in the message: "(written by pane NAME, not by the person)" when
  you send a pane's note. Each note is also typed with its author's authority
  as it is now: a note by a pane that may not type into the agent's pane now
  (it holds more than the pane), or whose pane is gone, and one by a linked
  machine that may no longer write, is withheld, not sent and not marked
  sent. Edit such a note to make it yours, or remove it.
- A pane without `admin` reads a diff only in its own session and fan group
  (`read`), and `against` names a session it must reach too. It writes notes
  with `write`, in the same reach, and adds or edits them only on a pane it
  could type into (one that holds nothing it does not), since a note is typed
  there when it is sent. It sends notes only into a pane that holds nothing it
  does not and is not on `needs_input` unless it holds `respond`, and the
  queue checks its grants again when the message is typed.
- A note is changed or removed only by whoever may speak for its author: you
  any note, a pane or a linked machine only the notes it wrote, and a shell
  every note but yours. `clear` removes what the caller may remove and keeps
  the rest, and says how many it kept.
- Over a link, all three need `write`, because a diff carries file contents
  and the other two write. A pane on another machine, whose calls arrive
  through its report channel, may neither write notes nor send them.

The diff is the repository's text and the notes are whoever wrote them, so
`review-diff` marks its answer `untrusted`.

## Environment

When tuios spawns a pane it exports the environment a state-reporting shim needs:

| Variable          | Meaning                                          |
| ----------------- | ------------------------------------------------ |
| `TUIOS_ENV`       | `1` when running under tuios                      |
| `TUIOS_SOCKET`    | Daemon socket path                               |
| `TUIOS_PANE_ID`   | The pane's window id                             |
| `TUIOS_WINDOW_ID` | The pane's window id (alias of `TUIOS_PANE_ID`)  |
| `TUIOS_SESSION`   | The session name                                 |
| `TUIOS_PANE_TOKEN` | Proves `TUIOS_PANE_ID` to `restrict-connection` and `pane-grants` where the kernel cannot name the caller's pane. Good for one pane of one daemon start |
| `TUIOS_PANE_GRANTS` | What the pane may do through tuios as it starts, comma separated (`read,write,fan`, `admin`, `none`). `tuios pane-grants` gives the current answer. See [What a pane may do](#what-a-pane-may-do) |

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
[the configuration reference](https://tuios.dev/docs/configuration#notifications) for the keys
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

### Mail waiting for another machine

Mail to a session on a machine whose link is down (`tuios send-agent-message
-s build:api ...`) waits on this machine and goes when the link is back, in
the order it was sent. The Inbox has one row per machine under **Waiting to
send**, `for build` on the right: `2 messages wait for the link to build`.
The rail's header for that machine says `2 queued` beside `seen 3m ago`, and
`tuios hosts` says so below its table. The row closes when everything went.

A send also waits when the link is up but the other machine did not answer in
time (10 seconds), or had no room for another stream, and when earlier mail for
that machine still waits, so it cannot overtake it. With the link up the daemon
tries again at once, then after 1 second, doubling to 30 seconds between tries,
rather than waiting for the link to drop and come back.

If the other machine refuses a message when it arrives (its session is gone,
or its link policy does not let this machine mail), the message is dropped and
the row says so, with the reason, until you dismiss it. `d` on the row also
discards what still waits. Enter says what will happen; the row is not a pane
and goes nowhere. It raises no alert and `o` skips it: it waits on a network,
not on you. The queue is on disk, so a restart of this daemon keeps it, and it
holds at most 64 messages per machine.

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
| `respond` from a pane, to approve its own tool call or another agent's | Refused with `not_human`, with or without a copied nonce, and with or without `respond_from_shell`, which only grants callers outside every pane. Nothing is pressed. Only a pane the person gave the `respond` grant may answer, and no pane can give itself that grant. |
| `set-pane-grants` from a pane, to widen itself or another pane | Refused with `forbidden`: a pane may change only its own grants unless it holds `admin`, and never give more than it holds. `admin` cannot give `respond`. |
| `send-text`, `send-keys`, `run` or `ask-agent` into a sibling pane that holds more, to have its shell run `tuios set-pane-grants` | Refused with `forbidden` for a pane without `admin`: a pane types only into panes that hold nothing it does not, since what it types runs with the target's grants. |
| `send-keys` or `send-text` into another pane's approval menu, to answer it | Refused with `forbidden` for a pane without `admin` unless it holds `respond`: typing into a pane on `needs_input` answers its prompt. |
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
  through tuios. Through `tuios mcp` the second is not possible at all, since
  its connections write only the agent's own pane's record, and the first is
  possible only with `--write` and only inside the agent's own session and fan
  group (see [The MCP server](#the-mcp-server)). Through the CLI both still
  are under the default `mode = "open"`, where every pane holds `admin`. Pane
  grants close both for a pane without `admin`, under `mode = "strict"` or for
  a pane started with narrower grants: every call from the pane is held to
  them, however it was made, and typing into a pane on `needs_input` needs
  `respond` (see [What a pane may do](#what-a-pane-may-do)). The check is made
  when the call is checked and again right before the text is written, so
  what is left is a prompt that comes up after the write. A pane holding
  `admin` is not held to it, and `ask-agent` still refuses a pane on
  `needs_input` (`agent_blocked`) for every caller unless it passes
  `allow_blocked`.
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

## What a pane may do

Every pane holds a set of grants that says what a process in it may do through
tuios, and the daemon checks every call from a pane against them: every JSON
verb, from the CLI, a script or `tuios mcp`, and every message of the client
protocol, before anything runs. The person's own shell and client, outside
every pane, are held to nothing new.

| Grant | What the pane may do |
| --- | --- |
| `read` | Read its own session and its fan group: list, capture, agent state, waits, the event stream, mail |
| `write` | Type into the panes of its own session (`send-text`, `send-keys`, `ask-agent`, `run`) that hold nothing it does not, and leave mail and stashed files there |
| `fan` | Do what `write` does in its fan group and the sessions it launched, and start agents with `fan` and `start-agent` |
| `respond` | Answer another pane's prompt with `respond`, without the person (see [Who may answer](#who-may-answer)), and type into a pane waiting on a prompt |
| `admin` | Everything else, as every pane could before grants: every session, the listings across sessions, windows, layouts, options, `kill-session`, `run-command`, attach. Includes `read`, `write` and `fan`, never `respond` |

Whatever it holds, a pane can always report about itself (`set-agent-state`,
`set-agent-meta`, `set-agent-session`, `ask-human`, `request-approval`, on its
own pane only), and ask what it holds with `tuios pane-grants`. So
`tuios agent-hook` works in every pane, whatever the pane holds.

### Typing into another pane

What a pane types into another pane runs with whatever that pane may do. A
shell on the open default holds `admin`, so text typed into it could run
`tuios set-pane-grants` and widen the pane that typed it. So a pane without
`admin` is held to two more rules when it types into any pane but its own,
with `send-text`, `send-keys`, `run`, `ask-agent` or `queue-prompt`:

- The target must hold nothing the caller does not. A pane holding `read` and
  `write` types into a sibling that holds `read` and `write` too, or less, and
  not into one on the open default. Under `mode = "strict"`, where every pane
  holds the same default, this changes nothing.
- The target must not be waiting on a prompt (`needs_input`), unless the
  caller holds `respond`: keys typed there answer the prompt, which is what
  `respond` is for. `ask-agent` without `allow_blocked` keeps its own refusal,
  `agent_blocked`.

A call with no window means the focused pane, and it is pinned to that pane
when it is checked, so a focus change cannot send it elsewhere. Both rules are
checked again right before the text is written. A pane's own pane is always
its own to type into. A pane without `admin` that sends keys always has them
written to the target's terminal, never through an attached client, where
the prefix key would drive the window manager.

### Where a pane's grants come from

- The grants it was started with: `tuios start-agent --grants`,
  `tuios fan --grants` and `tuios new-window --grants`, or the `grants` param
  of those verbs.
- Or the ones `tuios set-pane-grants` gave it later.
- Or else the default of `[agents.permissions]` in config.toml:

```toml
[agents.permissions]
mode = "strict"                    # open (the default) or strict
grants = ["read", "write", "fan"]  # what a pane holds under strict
```

Under `mode = "open"`, which is the default, a pane given no grants holds
`admin`, so every existing script in a pane keeps working exactly as it did.
Under `mode = "strict"` it holds the `grants` list, `read`, `write` and `fan`
when the list is not set. A mode tuios does not know is read as strict and an
unknown grant is dropped, so a typo never turns the protection off. A change
to the table reaches every pane on the default at its next call.

A pane can never give more than it holds. A pane without `admin` that starts
an agent without `--grants` gives it its own grants, a pane can change only
its own grants unless it holds `admin`, and `admin` cannot give `respond`. A
script can therefore drop its pane's grants before it starts an agent, and no
agent can raise its own, neither by asking nor by typing into a pane that
holds more (see [Typing into another pane](#typing-into-another-pane)):

```bash
tuios set-pane-grants --grants read,write && exec claude
```

The grants are saved with the window, so a restored pane holds what it held,
and `tuios list-windows --json` shows `grants` on every pane that was given
its own.

### A refusal

A call the grants do not cover does nothing and answers `forbidden`, with a
hint that names the grant it needed, what the pane holds and where that came
from, and how the person gives more:

```
send-text is refused for this pane: writing into the pane's own session needs the write grant
```

The daemon log records every refusal. From a pane without `admin`, a verb
that names no session means the pane's own session, not the most recently
active one, and an event stream carries only the sessions the pane may read.

### How the daemon knows the pane

The kernel's record of the caller's pid comes first, walked up to a pane's
shell or matched by its terminal, the same test [How a process is placed](#how-a-process-is-placed)
describes; a process in a pane that is still being created is placed by the
`TUIOS_PANE_ID` in its environment, since every pane's grants are in force
before its process starts. On Windows and the BSDs, where the daemon cannot
read the pid, the CLI presents `TUIOS_PANE_ID` and `TUIOS_PANE_TOKEN` on
every connection it opens from a pane, and the daemon holds that connection to
the pane the token proves. A process there that strips both from its
environment is treated as the person.

A pane on another machine is held by that machine, and a call over a link by
the link's policy (see [CONFIGURATION.md](CONFIGURATION.md#what-another-machine-may-do-here)).
A process in a pane this machine runs for another machine has no session
here: a call it makes to this machine's daemon directly holds this machine's
default and reaches no session, so under `strict` it can only ask what it
holds. Its reports travel to the machine that owns the pane as before.

### What grants do not cover

Grants scope accidents and prompt-injected agents that use tuios the ordinary
way. They are not a sandbox: a process that leaves its pane on purpose, the
way [Who can act as the person](#who-can-act-as-the-person) lists, is not
placed in it and is treated as the person. So is a process that writes a
respawn request to a [tmux shim](TMUX_SHIM.md) pane holder's socket itself;
the shim's own `respawn-pane` is held to the caller's grants. Grants say what a pane may do
through tuios; what its process may do to files and other programs is the
operating system's business.
