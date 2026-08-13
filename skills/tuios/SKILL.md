---
name: tuios
description: Drive tuios from inside one of its panes. Find out where you are running, read and write other panes, open panes and run work in them, wait on conditions instead of polling, and report your own state so the session shows it.
---

# Driving tuios from a pane

tuios is a terminal window manager with a daemon. Sessions hold windows, each
window owns one pane with a shell in it, and windows are grouped into numbered
workspaces. The `tuios` command talks to the daemon over a unix socket, so
everything below works from inside a pane, from a plain shell, and from a script.

This file is printed by `tuios --skill` and ships inside the binary, so it always
describes the tuios you are actually running.

## Am I inside tuios

```sh
[ "$TUIOS_ENV" = "1" ] || echo "not in a tuios pane"
```

A daemon-managed pane has these set:

```
TUIOS_ENV=1
TUIOS_PANE_ID=98db8226-1829-468e-89a8-41a2baa0ddab
TUIOS_WINDOW_ID=98db8226-1829-468e-89a8-41a2baa0ddab
TUIOS_SESSION=work
TUIOS_SOCKET=/run/user/1000/tuios/tuios.sock
```

`TUIOS_PANE_ID` and `TUIOS_WINDOW_ID` are the same uuid under two names: your own
window. Pass it to `-w` whenever you mean yourself rather than whatever happens
to be focused.

A pane in a standalone `tuios` (started without a daemon) gets only
`TUIOS_WINDOW_ID`. There is no socket to talk to, so guard on `TUIOS_ENV` and
degrade quietly when it is unset.

## Addressing things

Sessions are addressed by name with `-s`. Omit `-s` and the most recently active
session is used, which is usually the one you are in, and is a guess when several
are live. Inside a pane, prefer `-s "$TUIOS_SESSION"`.

Windows are addressed by `-w` and accept, in order:

- the full uuid
- a unique id prefix of 8 or more characters (`98db8226`)
- the exact window name, checking a name you gave it first and its shell's title
  second

Omit `-w` and the session's focused window is used. The index column in
`list-windows` output is a position, not an address; do not pass it to `-w`.

Workspaces are 1-based integers.

## Seeing what is there

```sh
tuios ls
tuios list-windows -s work
```

```
╭─────┬──────────┬───────────────────┬────┬───────┬───────╮
│ IDX │ ID       │ NAME              │ WS │ SIZE  │ AGENT │
├─────┼──────────┼───────────────────┼────┼───────┼───────┤
│ 0   │ d772540d │ Terminal d772540d │ 1  │ 80x24 │ none  │
│ 1   │ 98db8226 │ build             │ 1  │ 80x24 │ idle  │
│ *2  │ 499f9287 │ runner            │ 1  │ 80x24 │ none  │
╰─────┴──────────┴───────────────────┴────┴───────┴───────╯

3 window(s). * marks the focused one.
```

Every read command takes `--json` when you want to parse rather than read:

```sh
tuios list-windows -s work --json | jq -r '.windows[] | "\(.window_id) \(.display_name)"'
tuios session-info -s work --json | jq -r .current_workspace
tuios get-window -s work build --json | jq -r .agent_state
```

`tuios session-info` reports the workspace you are on, how many exist, the tiling
mode, and any workspace names:

```
session        work
display name   Payments API
accent         cyan
windows        3
workspace      1 of 9
tiling         floating
size           183x42
attached       true
named          2=review
```

## Reading another pane

```sh
tuios capture-pane -s work -w build
```

That is the visible screen, which is the pane's full height, so it ends in the
blank rows below the cursor. For the tail of what a pane actually printed,
including history that has scrolled off:

```sh
tuios capture-pane -s work -w build --scrollback --lines 40
```

`--lines` counts from the last line with content, so a quiet pane still gives you
its last 40 real lines. Add `--ansi` when you need the colors; leave it off when
you are matching text, which is almost always.

## Typing into a pane

`send-text` writes bytes to the pane's PTY with no parsing. Whatever you pass
arrives exactly as written, and a trailing newline is the Enter that runs it:

```sh
tuios send-text -s work -w build 'go build ./...
'
```

Use `send-keys` for keys that have no character: control combinations, arrows,
function keys, and tuios's own leader chords.

```sh
tuios send-keys -s work -w build ctrl+c          # interrupt what is running
tuios send-keys -s work -w build Escape
tuios send-keys -s work -w build 'ctrl+b,n'      # a tuios leader chord
```

`send-keys` parses its argument as key names by default and routes tuios's own
bindings to the interface. Use `--literal --raw` to push characters through to
the pane instead, though `send-text` is the simpler way to do that.

Sending input to a pane that is running an interactive agent will be read by that
agent as if a human typed it. Do not answer another agent's prompts on its behalf
unless you were asked to.

## Opening a pane and running work in it

```sh
tuios new-window -s work build
tuios send-text -s work -w build 'go test ./... 2>&1 | tee /tmp/test.log
'
```

```
7ddbb502  build
```

The window is created by the daemon whether or not anyone is attached, so this
works on a detached session. Naming it means you never have to hold on to the
uuid. To keep the id instead:

```sh
id=$(tuios new-window -s work --json | jq -r .window_id)
```

Close it when the work is done:

```sh
tuios run-command -s work CloseWindow "$id"
```

## Waiting instead of polling

Do not capture in a loop with a sleep. The daemon watches its own events and will
block for you, which is both exact and cheaper:

```sh
tuios wait-for window-output -s work -w build --pattern 'ok\s+github' --timeout 120000
tuios wait-for window-idle   -s work -w build --idle 2000
tuios wait-for window-exit   -s work -w build --timeout 600000
tuios wait-for session-exists -s work
```

- `window-output` matches a Go regular expression against what the pane prints,
  including scrollback. It is the right one when your command prints a marker.
- `window-idle` returns once the pane has printed nothing for `--idle`
  milliseconds. It is the right one when a command has no marker to match.
- `window-exit` returns when the pane's shell exits, which is what you want for a
  window opened to run one thing.

A match exits 0. A timeout exits non-zero with the `timeout` error and a hint
telling you to capture the pane and see what it actually printed. `--timeout` is
milliseconds and defaults to 30000, so raise it for anything slow.

The reliable pattern for running work somewhere else is to make the end of the
work observable:

```sh
tuios new-window -s work build
tuios send-text -s work -w build 'go test ./... ; echo "TESTS_EXIT=$?"
'
tuios wait-for window-output -s work -w build --pattern 'TESTS_EXIT=' --timeout 300000
tuios capture-pane -s work -w build --scrollback --lines 60
```

## Reporting your own state

tuios draws a per-pane indicator from a state your pane reports. Reporting it is
one command, and it is the difference between a session that shows which pane
needs a human and one that guesses from process names.

```sh
tuios set-agent-state working -m "running the test suite"
tuios set-agent-state needs_input -m "waiting for approval to push"
tuios set-agent-state done
tuios set-agent-state none                  # clear it
```

The states are `none`, `working`, `needs_input`, `idle`, `done`, `errored`. With
no `-w` the report lands on the focused window, which is wrong when you are not
the focused pane. From inside a pane, always name yourself:

```sh
tuios set-agent-state working -s "$TUIOS_SESSION" -w "$TUIOS_PANE_ID" -m "building"
```

Name your harness so anything reading the state knows what reported it:

```sh
tuios set-agent-state working -s "$TUIOS_SESSION" -w "$TUIOS_PANE_ID" --harness claude-code
```

`--source` says where the state came from and decides who wins when two things
report on the same pane. The ranks are `report` (highest), `osc`, `screen`, then
tuios's own process detection, then `stall` (lowest). A source cannot overwrite a
claim from a higher-ranked one, and a report that loses is refused rather than
applied:

```
Not applied: a higher-ranked source owns this pane; it still reports working.
```

Leave `--source` alone unless you are writing a detector. Reporting for yourself
is `report`, the default and the highest rank, which is correct: you know what you
are doing and nothing watching from outside does.

Read it back, yours or another pane's:

```sh
tuios get-agent-state -s work -w build
tuios get-agent-state -s work -w build --json
```

```json
{
  "state": "working",
  "message": "running the test suite",
  "source": "report",
  "harness_id": "claude-code",
  "agent_state_at": 1786610813544385500,
  "window_id": "293f8b0c-8fe4-467f-8efb-225ff5d7da5c",
  "success": true
}
```

If your harness has a hooks system, wire these calls to its lifecycle events once
instead of calling them by hand. `integrations/claude-code/` in the tuios repo is
a working example: a shim that maps session start to `working`, a notification to
`needs_input`, and stop to `done`, and no-ops when `TUIOS_ENV` is unset.

## Noticing that something finished

Three signals, in order of how definite they are:

```sh
tuios wait-for window-exit -s work -w build          # the shell exited
tuios get-agent-state -s work -w build               # what that pane reports
tuios list-windows -s work --json | jq -r '.windows[] | select(.agent_state=="needs_input")'
```

A pane that reports its own state is the only one you can trust to say
`needs_input`. A pane that does not report has agent state `none` no matter what
is happening inside it, so fall back to `window-idle` or an exit marker there.

## Naming things for the human watching

```sh
tuios set-session-name "Payments API"     # the label; the session keeps its name
tuios set-session-accent cyan
tuios set-workspace-name 2 review
```

Setting a session's display name does not change how it is addressed, so `-s work`
keeps working afterwards.

## When something goes wrong

Failures name the cause and the fix. A bad window target lists the windows that
do exist; a bad session name suggests the closest live one; a wait that times out
tells you to capture the pane. Read the whole error before retrying.

Stable error codes, for when you are matching on them rather than reading them:
`invalid_request`, `unknown_verb`, `invalid_params`, `session_not_found`,
`window_not_found`, `no_windows`, `pty_not_found`, `needs_client`,
`option_not_found`, `command_failed`, `timeout`, `protocol_mismatch`, `internal`.

`needs_client` means the operation needs a rendered interface and the session has
nobody attached. Reading, writing and waiting never need one.

## The rest of the surface

Every command above is a wrapper over the daemon's verb protocol. To see the
whole protocol, with parameters, defaults and examples:

```sh
tuios list-verbs
tuios list-verbs capture-pane
tuios list-verbs --json
```

Some verbs have no wrapper, notably `subscribe`, which opens a live event stream
instead of answering once. Reach those by writing newline-delimited JSON to
`$TUIOS_SOCKET` and reading one JSON line back per request:

```sh
python3 -c '
import json, os, socket, sys
s = socket.socket(socket.AF_UNIX); s.connect(os.environ["TUIOS_SOCKET"])
s.sendall(json.dumps({"id": 1, "verb": "subscribe", "params": {"types": ["window-created", "window-exit"]}}).encode() + b"\n")
for line in s.makefile():
    print(line.strip()); sys.stdout.flush()
'
```

```
{"id":1,"result":{"seq":133,"type":"subscribed"}}
{"seq":134,"type":"window-created","session":"work","window":"86e5e19f-...","title":"Terminal 86e5e19f","time":1786611217427984525}
```

Events arrive from the moment you subscribe, with no backfill, so subscribe
before you start the thing you want to watch. `wait-for` is the same machinery
with the bookkeeping done for you; reach for `subscribe` only when you need to
watch several things at once.

`tuios run-command --list` shows the window-management commands (focus, move,
minimize, switch workspace, close) that are not verbs.

## Habits worth having

- Pass `-s "$TUIOS_SESSION"` and `-w "$TUIOS_PANE_ID"` from inside a pane. The
  defaults follow focus, and focus moves under you.
- Bound every capture with `--lines`. A scrollback is up to 10,000 lines.
- Wait on a condition; never sleep and capture in a loop.
- Report `working` when you start and `done` or `needs_input` when you stop. The
  indicator is the only thing telling a human which pane wants them.
- Give a window a name when you create it, and address it by that name.
