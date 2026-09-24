# Recipes

Each recipe is a whole job, end to end, using only commands described in the
other topics. Change the names and prompts; keep the order.

## A fleet of agents on one task

Three agents, each in its own worktree, allowed to work in their own sessions
and nowhere else, then the best result kept:

```sh
tuios fan 3 --agent claude --grants read,write --name fan/retry 'Add a retry with backoff to the HTTP client. Run the tests before you stop.'
tuios list-agents --select 'group:fan/retry'
tuios wait-for agent-state --select 'group:fan/retry' --until idle,done,errored --every --timeout 3600000
tuios worktree ls --group fan/retry
tuios fan compare api-fan-retry
tuios fan verify api-fan-retry -- go test ./...
tuios worktree diff api-fan-retry-2 --stat
tuios review note -s api-fan-retry-2 api/retry.go:42 'log the attempt number here too'
tuios review send -s api-fan-retry-2
tuios fan keep api-fan-retry-2 --stash
```

While they run, anything that needs the person (an approval, a question, a
first-run choice that held the prompt) is in the Inbox. To tell the whole fleet
something, message it by selector; the first call lists the panes and the token:

```sh
tuios list-agents --select 'group:fan/retry'
tuios send-agent-message --select 'group:fan/retry' --confirm TOKEN 'main moved; rebase before you finish'
```

Use `ask-agent --select ... --yes` only for a question any match may answer.

## Answering from the Inbox

For the person. Every approval, question, error, finished turn and message from
every session and machine is one list:

1. Press the prefix key then `i` to open it, or the prefix key then `o` to go
   straight to the oldest item.
2. `j` and `k` move. `/` narrows the list with a selector such as
   `harness:codex needs:you`.
3. On an approval or a question, `space` shows the prompt as the pane shows it.
   A digit chooses that option, `a` approves, `A` approves and does not ask
   again, `d` denies, `tab` types a free answer.
4. On mail, `r` replies. The reply reaches the agent marked `verified_human`.
5. `enter` goes to the pane instead.

For an agent: make your prompts answerable from there. Report `needs_input`
with `--kind` and a message that is the question, keep the harness's numbered
options on screen, and ask decisions with `ask-human` rather than a free-form
message.

```sh
tuios set-agent-state needs_input -s "$TUIOS_SESSION" -w "$TUIOS_PANE_ID" --kind question -m 'drop the v1 endpoint?'
answer=$(tuios ask-human 'Drop the v1 endpoint?' -o drop -o keep --timeout 90000)
```

## Approvals without going to the pane

To answer Claude Code's, opencode's or Kilo's permission prompts from the
Inbox with `1` (once), `2` (always) or `3` (deny), the person adds this to
config.toml and installs the integration:

```toml
[agents.approvals]
enabled = ["claude-code"]
hold_seconds = 120
```

```sh
tuios integration install claude-code
tuios doctor agents
```

Only a call the Inbox can show whole on one line is held; the rest are asked in
the pane. A held pane shows nothing on screen: wait on its state, not its
screen. For a headless agent (`start-agent --protocol`) the Inbox holds such
requests with no config.

## Agents on another machine

Run the work on a bigger machine, watch it here, and bring the result back:

```sh
tuios hosts add build gaurav@buildbox
tuios hosts test build
tuios fan 2 --host build --agent claude 'Profile the importer and make it faster.'
tuios list-agents --all-hosts --select 'host:build'
tuios list-attention --host build
tuios worktree ls --host build
tuios worktree pull build:api-fan-profile-importer-2
```

Run `fan --host` from inside your checkout: the repository is found on the other
machine by its origin URL (`--clone` clones it there). `worktree pull` makes a
new worktree session here with the commits and the uncommitted work, and changes
nothing there. That machine's `[hosts]` policy decides what this one may do;
a refusal is `forbidden`.

## MCP instead of shell commands

```sh
tuios integration install claude-code --mcp
tuios integration status claude-code
```

Restart the harness in a tuios pane. It then has `tuios_list_agents`,
`tuios_wait_for`, `tuios_send_agent_message` and the rest, reaching only its own
session and fan group. Use `--mcp-write` only for an agent that must type into
panes. `tuios --skill mcp` lists every tool.

## Claude Code agent teams in tuios panes

```sh
tuios tmux-shim -- env CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 claude
```

Each teammate opens as a named pane on your workspace, with its state on the
rail. If a teammate does not appear, the pane lacks `admin`
(`tuios pane-grants`), or the tool used a tmux command the shim does not answer:
run it with `--log-all` and read the log (`tuios --skill tmux`).

## Scoped grants for a fleet

For the person: make every pane start with less, and give more only where it is
needed. In config.toml:

```toml
[agents.permissions]
mode = "strict"
grants = ["read", "write", "fan"]
```

Then a read-only reviewer, and one supervisor allowed to approve its workers'
prompts:

```sh
tuios start-agent claude --name reviewer --grants read
tuios set-pane-grants -w supervisor --grants read,write,fan,respond
tuios pane-grants
```

A pane can give only what it holds and never raise its own grants, and only the
person can give `respond`. From inside an agent's pane, `tuios pane-grants`
shows what it holds.

## A conductor

A pane that hands the next task to each agent that finishes, and leaves
everything else in the Inbox for the person. It never answers a prompt: it holds
no `respond`, and approvals are the person's.

```sh
#!/bin/sh
# conductor.sh: run in a tuios pane beside the fleet. tasks.txt holds one task per line.
tuios subscribe --types attention | while read -r ev; do
  [ "$(printf '%s' "$ev" | jq -r '.action')" = open ] || continue
  [ "$(printf '%s' "$ev" | jq -r '.attention.kind')" = finished ] || continue
  sess=$(printf '%s' "$ev" | jq -r '.attention.session')
  win=$(printf '%s' "$ev" | jq -r '.attention.window')
  task=$(head -n 1 tasks.txt)
  if [ -z "$task" ]; then
    tuios send-agent-message -s "$TUIOS_SESSION" -w human --from "$TUIOS_PANE_ID" 'every task is handed out'
    continue
  fi
  tail -n +2 tasks.txt > tasks.next && mv tasks.next tasks.txt
  tuios ask-agent -s "$sess" -w "$win" --from "$TUIOS_PANE_ID" "$task" &
done
```

Give the conductor only what it needs: `--grants read,write,fan` from the pane
that starts it. `ask-agent` refuses a pane on `needs_input`, so a task never
lands on a prompt. If the stream drops, start it again with `--after-seq` and
`--boot-id` from the last event (`tuios --skill events`). Do not make a
conductor reply to mail automatically without a bound: that is how two agents
loop.

## An alert on your phone

The daemon runs `after-agent-state` even with nobody attached, so a hook can
push to your phone when an agent needs you. With ntfy:

```sh
#!/bin/sh
# ~/.config/tuios/hooks/phone.sh
curl -s -m 5 -H "Title: tuios: $TUIOS_AGENT_STATE" \
  -d "$TUIOS_SESSION_ID/$TUIOS_WINDOW_NAME is $TUIOS_AGENT_STATE" \
  https://ntfy.sh/your-private-topic > /dev/null
```

```toml
[hooks]
after-agent-state = ["sh ~/.config/tuios/hooks/phone.sh"]

[notifications.agent]
suppress_focused = true      # nothing for the pane you are looking at
quiet_hours = "23:00-07:00"
```

`[notifications.agent.states]` decides which states alert (`needs_input`,
`errored` and `done` by default), and `settle_seconds` drops a state the pane
left quickly. The hook sends the pane's name and state, not its message, since
the message leaves your machine. The daemon reads `[hooks]` when it starts, so
this applies from the next daemon start; `tuios list-hooks` shows whether it
ran and what it returned.
