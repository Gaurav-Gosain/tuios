# Reporting state

tuios draws a per-pane indicator from the state your pane reports. It is what
tells the person which pane needs them, and what tells another agent whether
you can be asked a question.

## The report

```sh
tuios set-agent-state working -s "$TUIOS_SESSION" -w "$TUIOS_PANE_ID" --harness claude-code -m "building"
tuios set-agent-state needs_input -s "$TUIOS_SESSION" -w "$TUIOS_PANE_ID" --kind question -m "which retry policy?"
tuios set-agent-state done -s "$TUIOS_SESSION" -w "$TUIOS_PANE_ID"
tuios set-agent-state none -s "$TUIOS_SESSION" -w "$TUIOS_PANE_ID"    # clear it
```

The states are `none`, `working`, `needs_input`, `idle`, `done`, `errored` and
`unknown`. `unknown` is what the daemon writes to a pane it has lost track of:
an agent is there and nothing says what it is doing, so `ask-agent` and `fan`
do not type into it. With no `-w` the report lands on the focused window, which
is wrong when you are not the focused pane.

`get-agent-state` and `list-agents` also report `needs_you` (true for
`needs_input` and `errored`), `blocked_by` (`approval` or `question`, from
`--kind`), `completion_seq` (turns the pane has finished), `finished_unread`
(true while a pane rests after a turn nobody has looked at since) and `queued`
(messages waiting to be typed to the agent when it comes to rest).

Facts that are not a state (model, context use, a one-line summary) go in
metadata. The rail draws it under your row. It is display only. `key=` removes a
key, and `--ttl` makes a feed that stops writing leave nothing stale:

```sh
tuios set-agent-meta -s "$TUIOS_SESSION" -w "$TUIOS_PANE_ID" --source statusline --ttl 60s model=opus context=42%
tuios set-agent-meta -s "$TUIOS_SESSION" -w "$TUIOS_PANE_ID" summary=
```

## Wire it to your harness once

For eighteen harnesses tuios writes the hooks for you:

```sh
tuios integration install claude-code    # or any other harness, or --all
tuios integration status                 # installed, current, and what it reports
tuios doctor agents                      # also lists agent panes missing theirs
```

Claude Code, Codex, Gemini CLI, opencode, Kilo, Amp, Kimi and Pi report the
pane's state. Antigravity, Copilot, Crush, Cursor Agent, Devin, Droid, Grok,
Hermes, Qoder and Qwen report only the conversation id, so the pane can be
resumed, and their state keeps coming from screen rules.

Each installed hook runs `tuios agent-hook <harness>`, which reads the hook
payload on stdin and reports for the pane it runs in: a prompt or tool call is
`working`, a permission request is `needs_input` with kind `approval`, the tool
finishing after an approval is `working` again, the end of the turn is `done`,
and the harness's session id is stored (`agent_session_id`). It always exits 0
and gives up after 500ms, so a dead daemon never slows the harness.
`integrations/claude-code/` in the tuios repo holds the older shell shim, which
now runs the same reporter. Outside tuios, with `TUIOS_ENV` unset, it reports
nothing.

To report the same things by hand from another harness's hooks:

```sh
tuios set-agent-state needs_input -s "$TUIOS_SESSION" -w "$TUIOS_PANE_ID" --kind approval --agent-session-id "$SID" -m "approve Bash: make"
tuios set-agent-state working -s "$TUIOS_SESSION" -w "$TUIOS_PANE_ID" --if-state needs_input
tuios set-agent-session --harness qwen -s "$TUIOS_SESSION" -w "$TUIOS_PANE_ID" "$SID"
```

`--if-state` applies the report only when the pane is in one of the states
named, so a "tool finished" event clears a block without turning a finished
pane back to `working`. A report carrying `--agent-session-id` is refused while
the pane's own agent is mid-turn in a different session, which keeps a
`claude -p` run inside the pane from marking it done. `set-agent-session`
stores the conversation id and changes nothing else.

An agent in a container or a VM is invisible to process detection. Set
`TUIOS_AGENT` to its harness id on the wrapper, as in
`TUIOS_AGENT=claude-code docker run -it box claude`.

A harness tuios recognises that emits OSC 9;4 progress reports needs no more
wiring: setting a bar is `working`, clearing it `idle`, the error state
`errored`, and the warning state `needs_input`. Progress drives state only on a
pane already known to hold an agent (a detected harness, `TUIOS_AGENT`, or any
report), so a build tool's progress bar in a plain shell never makes it one. A desktop notification (OSC 9, OSC 777 or OSC 99) from a
recognised harness is read through that harness's notification rules, and every
notification is published on `subscribe` as a `notification` event.

## Detection

Without a report, tuios recognises 22 agent CLIs by their foreground process
(through shells, interpreters and launchers such as `npx`), and by screen and
title rules. The set comes from manifest files and a user can add their own, so
ask rather than assume:

```sh
tuios explain-agent-detect -s work -w build --json | jq -r '.manifests[].id'
tuios explain-agent-screen -s work -w build --harness codex --lines 20
```

`explain-agent-detect` gives a verdict, the evidence, and every word that looks
like an agent's name and was not counted. `explain-agent-screen` shows the
screen tail as the rules read it and which rule fired or why each did not.

Process detection can never say `needs_input`, and a screen rule is a guess.
Your own report outranks both, and `confidence` in `get-agent-state` says which
one named the pane. A file in `~/.config/tuios/harnesses` (or
`$TUIOS_HARNESS_DIR`) with a bundled harness's id replaces that manifest whole;
`tuios doctor agents` lists the files in force and the ones that failed.

## Who wins when reports disagree

`--source` says where a state came from. Highest first, the ranks are `report`, `transcript`,
`osc`, `screen`, `detect`, then `stall`. A source cannot overwrite a claim from
a higher-ranked one. Only `report`, `osc`, `screen` and `stall` are accepted
over the socket. Leave `--source` alone unless you are writing a detector.

`set-agent-state` prints nothing when the report is applied. A report that
loses still exits 0 and says so on stderr:

```
Not applied: a higher-ranked source owns this pane. It still reports working.
```

A script that must know whether its report took should match that line. Over
the socket the result carries `applied: false` and a `reason` of `outranked`,
`if_state`, `foreign_session` or `foreign_harness`.

## Reading state back

```sh
tuios get-agent-state -s work -w build --json
```

```json
{"activity":"working","confidence":"certain","harness_id":"claude-code","identity":"report",
 "message":"running the test suite","needs_you":false,"source":"report","state":"working",
 "success":true,"window_id":"739bc078-7522-4a37-bb9b-e5140e918666"}
```

Three signals say something finished, most definite first: the process exiting
(`wait-for window-exit`), an agent reaching rest (`wait-for agent-state --until
idle,done,needs_input`), and what the pane reports now (`get-agent-state`). A
pane that does not report reads `none` whatever happens inside it, so fall back
to `window-idle` or an exit marker there.

## Resuming after a daemon restart

A restart ends every program in every pane. For a pane where an agent was
running at save, whose harness has a `[resume]` command in its manifest (Claude
Code, Codex, opencode, Copilot, Cursor Agent, Qwen and more), the restore offers
to run it with the stored conversation id. `daemon.resume_agents` decides how:
`ask` (the default) puts a Resume row in the Inbox, `auto` types the command,
`off` does neither. So report your id early, from the session start hook.

```sh
tuios resume-agent -s work -w build --dry-run   # print the command
tuios resume-agent -s work -w build             # type it into that pane's shell
```

`resume-agent` types only into a shell at its prompt (`not_ready` otherwise) and
answers `not_resumable` for a pane with no id, a harness with no `[resume]`
block, or a pane on another machine. The command comes from the manifest and
the stored id only.
