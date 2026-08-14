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
- the index that `list-windows` prints, when the target is all digits
- a unique id prefix (`98db8226`, or any shorter prefix that matches one window)
- the exact window name, checking a name you gave it first and its shell's title
  second

An ambiguous prefix or name is an error rather than a guess. The index is a
position: it shifts when a window earlier in the list closes, so it is handy at
the keyboard and wrong in a script that holds on to it. Store the id or the name
instead.

A session's display name and accent are labels for humans; addressing always
uses the session name. Workspaces are 1-based integers.

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

The listing and info commands all take `--json` when you want to parse rather
than read. `capture-pane` is the exception: its output is the pane text itself.

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

## When the daemon is not running

`tuios ls` tells a script exactly which situation it is in through its exit
code: 0 is a running daemon (even one holding no sessions), 3 is no daemon, and
1 is a failure. With no daemon, sessions saved on disk are listed anyway, marked
`saved`:

```
│ work │ 2       │ saved  │ -       │ 2 min ago   │

1 session(s)
saved: on disk only, with no daemon running to hold it.
```

`tuios attach` starts the daemon when none is running and restores the saved
sessions before attaching. From a script, `tuios start-server` does the same
restore without taking over the terminal. A restored session keeps its name,
display name, accent, workspace names, window ids and window names, and is
marked `restored` in the listing:

```
restored: layout came back from saved state; the shells are new.
```

The shells are new. Scrollback is empty, whatever was running is gone, and each
restored pane opens on a banner saying so. A marker you were waiting for and any
agent state you reported died with the old daemon, so treat a `restored` session
as panes to be started over, addressed by the ids and names you already know.

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

The bindings live in the attached client's interface. On a session with nobody
attached, a leader chord is accepted, exits 0, and does nothing. For window
management that works attached or detached, use `run-command`:

```sh
tuios run-command -s work SwitchWorkspace 2
tuios run-command --list
```

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

On a detached session, a window whose shell has exited stays in the list until
something closes it, and `capture-pane` still reads its final screen. Close what
you open, or a loop that opens a window per run quietly accumulates dead ones.

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

### The one trap in window-output

`window-output` matches the pane's whole scrollback, including text that was
already there before you started waiting. Two things follow, and both bite.

The pane echoes the command you typed. If your marker appears in the command, the
wait matches that echo and returns at once, before any work has run:

```sh
tuios send-text -s work -w build 'sleep 4; echo DONE_MARKER
'
tuios wait-for window-output -s work -w build --pattern DONE_MARKER   # returns in 8ms
```

And a marker from an earlier run is still in the scrollback, so a fixed marker
works exactly once per pane: the same wait in the same pane matches the old
output instantly the second time. Both were measured at around 5ms.

One recipe avoids both. Make the marker fresh for this run, and let the pane
assemble it so the literal never appears in the command line:

```sh
n=$(date +%s)
tuios send-text -s work -w build "go test ./... ; printf 'tests_done_%s\n' $n
"
tuios wait-for window-output -s work -w build --pattern "tests_done_$n" --timeout 300000
tuios capture-pane -s work -w build --scrollback --lines 60
```

The echo shows `printf 'tests_done_%s\n' 1786700000`, which the pattern does not
match; the output shows `tests_done_1786700000`, which it does. The timestamp
makes the previous run's marker a different string.

Or run the work in a window that exits, and wait for the exit. Nothing has to be
matched at all, so nothing can match early. Send the output somewhere you can
read it afterwards:

```sh
tuios new-window -s work build
tuios send-text -s work -w build 'go test ./... > /tmp/test.log 2>&1; exit
'
tuios wait-for window-exit -s work -w build --timeout 300000
tail -60 /tmp/test.log
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
the focused pane. From inside a pane, always name yourself, and name your harness
so anything reading the state knows what reported it:

```sh
tuios set-agent-state working -s "$TUIOS_SESSION" -w "$TUIOS_PANE_ID" --harness claude-code -m "building"
```

### Wire it to your harness once

If your harness has a hooks system, map its lifecycle events to these calls once
instead of remembering to call them by hand. `integrations/claude-code/` in the
tuios repo is a working shim: session start and prompt submit report `working`,
a notification reports `needs_input` with the notification's message, stop
reports `done`, and every path exits 0 untouched when `TUIOS_ENV` is unset, so
it is safe to leave wired up outside tuios. The same mapping fits any harness
that can run a command on its lifecycle events.

A harness that emits OSC 9;4 progress reports needs no wiring at all: tuios
reads them from the pane. Setting a bar maps to `working`, clearing it to
`idle`, the error state to `errored`, and the warning state to `needs_input`.

Without either, tuios recognises common agents (claude-code, codex, gemini-cli,
cursor-agent, droid, aider, crush, opencode) by their foreground process and
marks the pane `working` while one runs. That is a coarse fallback: it can never
say `needs_input`, which is the state a human actually acts on. Your own report
always outranks it.

### Who wins when reports disagree

`--source` says where a state came from and decides who wins when two things
report on the same pane. The ranks are `report` (highest), `osc`, `screen`, then
tuios's own process detection, then `stall` (lowest). A source cannot overwrite a
claim from a higher-ranked one. Leave `--source` alone unless you are writing a
detector: reporting for yourself is `report`, the default and the highest rank.

`set-agent-state` prints nothing when the report is applied. A report that loses
is refused, still exits 0, and says so on stderr:

```
Not applied: a higher-ranked source owns this pane; it still reports working.
```

A script that must know whether its report took should match that line, since
the exit code will not say.

Read the state back, yours or another pane's:

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

Over the socket, every failure carries a stable code in the error envelope, for
when you are matching rather than reading: `invalid_request`, `unknown_verb`,
`invalid_params`, `session_not_found`, `window_not_found`, `no_windows`,
`pty_not_found`, `needs_client`, `option_not_found`, `command_failed`,
`timeout`, `protocol_mismatch`, `internal`. The CLI folds the same information
into its messages.

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
{"seq":134,"type":"window-created","session":"work","window":"86e5e19f-...","pty_id":"b158e731-...","title":"Terminal 86e5e19f","time":1786611217427984525}
```

Events arrive from the moment you subscribe, with no backfill, so subscribe
before you start the thing you want to watch. `wait-for` is the same machinery
with the bookkeeping done for you; reach for `subscribe` only when you need to
watch several things at once.

## Habits worth having

- Pass `-s "$TUIOS_SESSION"` and `-w "$TUIOS_PANE_ID"` from inside a pane. The
  defaults follow focus, and focus moves under you.
- Bound every capture with `--lines`. A scrollback is up to 10,000 lines.
- Wait on a condition; never sleep and capture in a loop.
- Report `working` when you start and `done` or `needs_input` when you stop. The
  indicator is the only thing telling a human which pane wants them.
- Give a window a name when you create it, and address it by that name. An index
  is fine at the keyboard and stale the moment an earlier window closes.
- Check `tuios ls` exit 3 before assuming a session is gone: it may be saved on
  disk, one `tuios start-server` away from being back.
