# Fleets: selectors, worktrees, fan and start-agent

How to start several agents, give each its own checkout, address them as a
group, and keep or drop their work.

## Many panes at once: selectors

A window id names one pane. A selector names every agent pane that fits a
description, in every session on this machine:

```sh
tuios list-agents --select 'harness:codex state:idle,done'
tuios list-agents --select 'group:fan/add-retry needs:you'
tuios list-attention --select 'harness:claude'
tuios wait-for agent-state --select 'group:fan/add-retry' --until idle,done --every --timeout 3600000
```

Terms are separated by spaces and every term must match. A term is `key:value`,
and a comma gives alternatives.

| Key | Matches |
| --- | --- |
| `harness:` | the harness id, or the program name that starts it (`claude` is `claude-code`) |
| `state:` | the agent state |
| `needs:you` | a pane on `needs_input` or `errored` |
| `session:` | the session name, a glob |
| `group:` | the fan-out group, the branch stem `fan` used, a glob |
| `host:` | the machine: `local`, or a host name, a glob |
| `name:` | the window's name, a glob |
| `cwd:` | the directory or anything under it; `~` is the home directory |

A glob's `*` does not cross a slash. A term the pane cannot answer does not
match. `list-agents --all-hosts --select` reads every machine.

`wait-for agent-state --select` watches every matching pane, including panes
that open during the wait, and ends on the first to reach an `--until` state.
With `--every` it ends only when at least one pane matches and all of them are
there: "wait until the whole fan-out is done". Put the state you wait for in
`--until`, not in the selector.

Writing to a selection never happens by accident. `send-agent-message --select`
and `ask-agent --select` first list the panes and send nothing; over the socket
that is `confirm_required`, whose hint carries the panes and a token. Send again
with the token. The token is a hash of exactly that set, so a pane joining or
leaving in between is refused again. `list-agents --select` prints the same
token:

```sh
tuios list-agents --select 'group:fan/add-retry'
tuios send-agent-message --select 'group:fan/add-retry' --confirm 3f9a0c2b7d41e865 'main moved, rebase before you push'
tuios ask-agent --select 'group:fan/add-retry state:idle,done' --yes 'summarise your change in one line'
```

`--yes` sends to whatever matches at that moment, so use it only when any match
is fine. An ask by selector asks at most 16 panes at once, each the way a single
ask is; a write reaches at most 32 panes.

## A worktree as a session

A git worktree is the unit of isolation for one agent: its own checkout and
branch. tuios makes one and a session in it with one command, and the rail
groups such sessions under their repository.

```sh
tuios worktree new feat/retry --base main --detach
tuios worktree new feat/retry-2 --agent claude --detach
tuios worktree ls
tuios worktree diff api-feat-retry --stat
```

The session is named `<repo>-<branch>` with slashes turned into hyphens.
`--agent` names an agent CLI the way you type it and starts it instead of a
shell. `worktree ls` (the `list-worktrees` verb) reports each session's rolled-up
`state` and `gone` when the directory was removed; the verb with
`"changes": true` also counts uncommitted changes and commits ahead of the base.

## Fan-out

One prompt across several agents, each in its own worktree:

```sh
tuios fan 3 --agent claude 'Add a retry with backoff to the HTTP client.'
tuios worktree ls --group fan/add-retry-backoff-http
tuios fan keep api-fan-add-retry-backoff-http-2 --stash
```

The branches are a stem and then `stem-2`, `stem-3`; the stem is `fan/` and the
first words of the prompt, or `--name`. The prompt is typed once the agent is at
its prompt (`idle` or `done`), never over a start-up screen, and checked the way
`ask-agent` checks it. `worktree ls` says `prompt_status` per session:
`pending`, `held` (not ready after 30 seconds; the Inbox gets a question, since
it is most often a first-run choice only the person can answer), `sent`,
`not_sent` or `stalled`. A stalled prompt may sit in the agent's input box: look
before sending it again. `--wait` blocks until every prompt is sent or given up.

`--agent` takes several agents, comma separated, cycled across the sessions, and
`--prompt` once per session gives each its own prompt:

```sh
tuios fan 3 --agent 'claude,codex --model o5,gemini' 'Add a retry with backoff.'
tuios fan --agent claude --env ANTHROPIC_API_KEY --prompt 'Add a retry.' --prompt 'Add a timeout.'
```

The words are split as a shell splits them and exec'd directly; nothing is
expanded. Any program works; one no manifest recognises gets its prompt only
once it reports a state. The CLI sends your `PATH`, and `--env NAME` sends one
more variable. `TUIOS_` names, `TMUX` and `TMUX_PANE` are refused.

### Comparing the attempts

```sh
tuios fan compare api-fan-add-retry-backoff-http
tuios fan verify api-fan-add-retry-backoff-http -- go test ./...
tuios fan diff api-fan-add-retry-backoff-http api-fan-add-retry-backoff-http-2
```

`fan compare` (the `compare-fan` verb) gives one row per attempt: agent,
state, files and lines changed against the fan's base (committed or not,
untracked included), `verify`, and `last_command`, the last command a shell in
the session finished. An agent's own tool runs are not shell commands, so run
the check you trust with `fan verify` (`verify-fan`): it opens a window named
`verify` in each attempt, runs your command with `sh -c` in the worktree, and
records `passed` or `failed` with the exit status. Several words after `--`
are quoted one by one; one word is a shell line, for `&&` and pipes. The
window holds no grants. It closes on a pass and stays open on a failure so the
output can be read, until the next check closes it. The counts run from where
each attempt left the base, so they hold still when main moves on.
`fan verify` waits and exits 1 when any failed; `--no-wait` returns at once and
`fan compare` shows the results. From a pane, `verify-fan` needs the `fan`
grant and `compare-fan` needs `read`, and both reach only your own fan group.
`fan diff` shows what the second attempt's files hold that the first's do not.

## One agent beside you: start-agent

`start-agent` opens a pane with an agent in the session you are in and returns
once the agent is at its prompt:

```sh
tuios start-agent claude --name reviewer
tuios ask-agent -w reviewer 'review the diff on this branch and list anything risky'
tuios start-agent 'codex --model o5' --name tests --prompt 'Run the test suite and fix what fails.'
```

```
reviewer (4be1c09a) is ready: it reads idle.
```

The pane is not focused unless you pass `--focus`. An agent that stops on a
question of its own, such as trusting the folder, is not ready: the command
prints what it waits on and exits non-zero, and the pane is kept for the person.
`--repo` starts it in a repository's main checkout, and a session `-s` names
that does not exist is created.

Give a helper no more than its job needs:

```sh
tuios start-agent claude --name reviewer --grants read
```

`--grants` works the same on `fan` and `new-window`. You can give only what you
hold (`tuios --skill grants`).

### Headless, over a protocol

With `--protocol`, `start-agent` runs the agent headless and the pane shows the
conversation as a plain transcript: prompts, replies, tool calls, plans and
diffs.

```sh
tuios start-agent --protocol acp 'opencode acp' --name helper --prompt 'List the TODOs in this repository.'
tuios start-agent --protocol codex codex --name tests
tuios ask-agent -w helper 'which of those is the oldest?'
```

`acp` is the Agent Client Protocol, version 1; name the agent's ACP command.
`codex` is the Codex app-server; `app-server` is added for you. The pane reports
its own state (`idle`, `working`, `done` or `errored`), so waits and `ask-agent`
work as for any agent, and `capture-pane` reads a transcript with no escape
sequences from the agent in it. A permission request shows in the pane with a
number key per answer and reads `needs_input` with kind `approval`; when one line
shows the whole request, the Inbox holds it too and the first answer wins. Do not
type a digit into a blocked pane: that is the person's answer to give.

## Removing a worktree is the sharp edge

`tuios worktree rm` runs `git worktree remove` and kills the session. A worktree
with uncommitted changes is refused with `worktree_dirty` and nothing is removed:

```sh
tuios worktree rm api-feat-retry --stash    # keep the changes in git stash
tuios worktree rm api-feat-retry --force    # discard them
tuios worktree rm api-feat-retry --keep-session
```

The branch is never deleted. `tuios fan keep <session>` (the `keep-fan` verb)
applies the same rule to every sibling of the session you keep, and leaves a
dirty sibling in place. Only the person or a pane with `admin` may keep a fan.
Nothing here runs `git worktree prune`.

Agents and worktrees on another machine, and `worktree pull`, are in
`tuios --skill hosts`.
