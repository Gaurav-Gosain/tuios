# Panes: opening, running, waiting and arranging

The part of the skill about panes as places to run work: making a session,
opening panes, getting an exit code back, waiting without being fooled, and
moving panes around with verbs rather than keybindings.

## A session of your own

To set up a workspace instead of driving one that exists, create the session
first:

```sh
tuios new --detach scratch
tuios new-window -s scratch build --cwd /src/api
```

Over the control protocol this is the `new-session` verb, which does both in one
call and returns the ids:

```json
{"id":1,"verb":"new-session","params":{"name":"scratch","window_name":"build","cwd":"/src/api"}}
```

The session runs detached until somebody attaches. Pass `"window": false` for an
empty session. A name the daemon already holds comes back as `session_exists`
with the names that do exist, so pick another name rather than assuming you took
it over.

`tuios session-info -s work` reports the workspace you are on, how many exist,
the tiling mode, any workspace names and, when the workspaces were rearranged,
their display `order`. They keep their numbers, so `select-workspace 2` still
means workspace 2.

## Opening a pane and running work in it

```sh
tuios new-window -s work build
tuios send-text -s work -w build 'go test ./... 2>&1 | tee /tmp/test.log
'
```

To make the pane's process the program itself rather than a shell, put the argv
after the name. Nothing re-parses it, and the pane closes when the program
exits. Put `--` before a command that has flags of its own:

```sh
tuios new-window -s work htop /usr/bin/htop
tuios new-window -s work log -- git log --oneline -20
```

The daemon creates the window whether or not anyone is attached. Say where it
goes and what it starts in, and keep the id if you need it:

```sh
tuios new-window -s work tests --workspace 2 --cwd /src/api --no-focus
id=$(tuios new-window -s work --json | jq -r .window_id)
```

`--no-focus` keeps the person where they are. The JSON result says where the
pane went. `unplaced: true` means the session is detached and the pane has a
nominal size until a client places it, so do not compute anything from its
geometry yet.

Close what you open. On a detached session a window whose shell has exited
stays in the list until something closes it:

```sh
tuios run-command -s work CloseWindow "$id"
```

## Showing someone a pane

`capture-pane` gives you the text. `screenshot` renders the pane as an image,
with colours and a frame, and prints the path it wrote. It works on a detached
session:

```sh
tuios screenshot -s work -w build
```

`--format` takes `png`, `svg`, `ansi`, `html` or `txt`; `--out` names the file;
`--scrollback` puts history above the screen; `--theme NAME` renders in another
theme; `--frame` takes `window`, `plain` or `none`. The file can be attached to a
message. `capture-pane --ansi --resolved` gives colours as 24-bit RGB against
xterm's palette or the 16 values you pass to `--palette`.

## Typing into a pane

```sh
tuios send-text -s work -w build 'go build ./...
'
tuios send-keys -s work -w build ctrl+c          # interrupt what is running
tuios send-keys -s work -w build Escape
tuios send-keys -s work -w build 'ctrl+b,n'      # a tuios leader chord
```

`send-keys` splits its argument on spaces and commas and maps each token to a
key, so it cannot type text:

```sh
tuios send-keys -s work -w build 'echo hello'    # types "echohello"
tuios send-text -s work -w build 'echo hello
'                                                # types "echo hello" and runs it
```

A key you send does not move the person's view. Leader chords mean something
only where a client is attached; on a detached session `ctrl+b,n` reaches the
shell as the bytes it spells. Do not drive the window manager with its
keybindings: the verbs below work attached or detached and say what changed.

## Waiting instead of polling

```sh
tuios wait-for window-output -s work -w build --pattern 'ok\s+github' --timeout 120000
tuios wait-for window-idle   -s work -w build --idle 2000
tuios wait-for window-exit   -s work -w build --timeout 600000
tuios wait-for session-exists -s work
tuios wait-for agent-state   -s work --until needs_input
tuios wait-for agent-state   --any-session --until needs_input
tuios wait-for agent-message -s work -w "$TUIOS_PANE_ID" --timeout 600000
tuios wait-for command-finished -s work -w build --timeout 600000
```

- `window-output` matches a Go regular expression against what the pane
  prints, including scrollback.
- `window-idle` returns once the pane has printed nothing for `--idle`
  milliseconds. Use it when a command has no marker.
- `window-exit` returns when the pane's process exits.
- `agent-state` returns when an agent pane reaches one of the `--until` states.
  Without `-w`, any agent in the session matches; `--any-session` watches every
  session; `--select` watches a set of panes (`tuios --skill fleet`).
- `agent-message` returns when mail arrives (`tuios --skill mail`).
- `command-finished` returns when the pane's next shell command finishes, with
  its exit code (see below).

A match exits 0. A timeout exits non-zero with the `timeout` error. `--timeout`
is milliseconds and defaults to 30000.

### The one trap in window-output

`window-output` matches the whole scrollback, including text that was there
before you started waiting. The pane echoes the command you typed, so a marker in
the command matches its own echo at once, and a fixed marker from an earlier run
matches again the next time. Make the marker fresh and let the pane assemble it:

```sh
n=$(date +%s)
tuios send-text -s work -w build "go test ./... ; printf 'tests_done_%s\n' $n
"
tuios wait-for window-output -s work -w build --pattern "tests_done_$n" --timeout 300000
tuios capture-pane -s work -w build --scrollback --lines 60
```

The echo shows `printf 'tests_done_%s\n' 1786700000`, which the pattern does not
match; the output shows `tests_done_1786700000`, which it does.

### Running a command and getting its exit code

When the pane's shell marks its commands with OSC 133 (fish 4 on its own, zsh
and bash 4.4 or newer with the lines `tuios doctor shell` prints), `run` types
the line at the prompt, waits for the shell to say it finished, prints exactly
what that command printed, and exits with its status:

```sh
tuios run -s work -w build --timeout 600000 -- go test ./...
echo "tests exited $?"
tuios run -s work -w build --json -- make lint    # exit_code, output, duration_ms
```

`run` never types into a running program. A busy pane is refused with
`not_at_prompt`, and a pane whose shell sends no marks with
`no_shell_integration`; nothing is typed either way. `list-windows --json` shows
`at_prompt`, `command_seq`, `last_exit_code` and `last_cmdline` for a pane whose
shell marks its commands. A timeout does not stop the command: the error names
the `wait-for command-finished --command-seq N` that picks it up.
`capture-pane --last-command` prints only what the last finished command
printed.

Without shell integration, put the status in the marker:

```sh
n=$(date +%s)
tuios send-text -s work -w build "go test ./... ; printf 'done_%s_rc=%s\n' $n \$?
"
tuios wait-for window-output -s work -w build --pattern "done_${n}_rc=" --timeout 300000
tuios capture-pane -s work -w build --scrollback --lines 60 | grep -o "done_${n}_rc=[0-9]*"
```

Or run the work in a window that exits, and wait for the exit:

```sh
tuios new-window -s work job
tuios send-text -s work -w job 'go test ./... > /tmp/test.log 2>&1; exit
'
tuios wait-for window-exit -s work -w job --timeout 300000
tail -60 /tmp/test.log
```

## Arranging panes

Every arrangement has a verb. They work whether or not a client is attached,
do not depend on the person's keymap, and report what changed.

```sh
tuios list-workspaces -s work
tuios focus-window -s work build               # focus a named pane, on any workspace
tuios focus-window -s work --relative next
tuios move-window -s work 2 -w build --follow  # send a pane to workspace 2
tuios select-workspace -s work 2
tuios set-window -s work -w build --name "api tests"
tuios set-window -s work -w build --minimize
tuios set-window -s work -w build --restore
```

Geometry belongs to the attached client, so these need one and say
`needs_client` when there is none:

```sh
tuios split-window -s work vertical -w build --name logs
tuios set-layout -s work --tiling true --equalize
tuios set-layout -s work --rotate
tuios focus-window -s work --direction left
```

Reading, writing, waiting, creating and moving never need a client, and neither
does anything to do with agents.

### A popup for one command

`tuios popup` runs one command in a floating pane centred over the layout. It
closes when the command exits, and it is not tiled or in the window cycle. It
needs a client attached.

```sh
tuios popup -s work --width 60 --height 20 -- gum choose one two three
file=$(tuios popup -s work --capture-stdout -- fzf)
tuios popup -s work --wait -- gum confirm "Deploy?" && ./deploy.sh
```

`--width` and `--height` take cells or a percent. `--wait` returns the command's
status; `--capture-stdout` prints its standard output. A popup closed by hand
exits 130. Capture is not on Windows.

### The escape hatch

A keybinding with no verb of its own is reachable by name:

```sh
tuios run-command -s work ToggleZoom
tuios run-command --list
```

A name that is not a command is an error. `run-command` reports that the command
ran and nothing about what it changed, and from a pane it needs `admin`. Prefer
a verb where one exists.

## Naming things for the person watching

```sh
tuios set-session-name "Payments API"     # the label; the session keeps its name
tuios set-session-accent cyan
tuios set-workspace-name 2 review
```

A display name does not change how the session is addressed: `-s work` keeps
working.
