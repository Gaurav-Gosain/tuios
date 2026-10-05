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

`tuios new` starts the session's windows in the directory you run it from.
`--cwd DIR` names another one:

```sh
tuios new --detach api --cwd /src/api
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
id=$(tuios new-window -s work job --print-id)
```

`--no-focus` keeps the person where they are. `--json` says where the
pane went. `unplaced: true` means the session is detached and the pane has a
nominal size until a client places it, so do not compute anything from its
geometry yet.

Close what you open. On a detached session a window whose shell has exited
stays in the list until something closes it:

```sh
tuios close-window -s work "$id"
```

## A pane on the machine a pane is ssh'd into

The actions `split_ssh_vertical`, `split_ssh_horizontal` and `new_window_ssh`
open a pane that runs the focused pane's ssh again, to the same host as the
same user. The new line keeps only the connection options, with no remote
command. A pane that does not run ssh gets an ordinary pane. So does an ssh
line with an `-o` option outside a fixed list (`ProxyCommand`,
`XAuthLocation` and similar), with `-F` or `-E`, or ssh that `scp` or `git`
started. They are keybinding actions, so `run-command` runs them, and the
same grant rules apply as for any other `run-command`:

```sh
tuios focus-window -s work build
tuios run-command -s work split_ssh_vertical
```

## Many panes at once

`tuios xpanes` opens one pane per item on a new workspace, tiled, with
multifocus on, like tmux-xpanes. Items come from the arguments or from stdin:

```sh
tuios xpanes --ssh web1 web2 web3                   # ssh to each host, held until Enter
tuios xpanes -ss --interval 1 -c 'curl -s {}' a b   # no shell, closes on exit, 1 s apart
printf 'a\nb\n' | tuios xpanes -c 'make test-{}'    # {} is the item, shell quoted
tuios xpanes --no-sync -l even-horizontal -c 'tail -f {}' app.log db.log
```

Each pane gets `TUIOS_XPANES_ITEM` and `TUIOS_XPANES_INDEX`, and is named after
its item. Without `-s`, `-c` is typed into the pane's shell, which stays.
`-s` runs it with no shell and holds the pane until Enter; `-ss` closes the
pane when it exits. `--interval` spaces the panes. `--session` names the
session (`-s` is speedy mode, as in tmux-xpanes). `tuios close-workspace N`
closes them all, like tmux kill-window. When the last of them closes, the
session goes back to the workspace that ran `xpanes`. `--json` prints the window ids. The layout and
multifocus need an attached client; without one the panes open and a warning
says so. More than 64 panes needs `--force`. For work you drive yourself, open
panes with `new-window` and keep the ids; xpanes is for a person who wants to
type into all of them.

## Text from many panes

For a script, loop over the panes and use `capture-pane`:

```sh
tuios list-windows --json | jq -r '.windows[] | "\(.index)\t\(.window_id)"' |
  while IFS=$'\t' read -r index id; do
    printf '## Pane %s\n' "$index"
    tuios capture-pane -w "$id" --lines 20
  done
```

A person can do the same by hand with multi copy mode. They add the panes to
multifocus, press `Ctrl+B [`, search once, press `V` and `y`. tuios copies the
selected line of each pane as plain text, markdown or JSON. `Y` saves it to a
file. The format starts as `appearance.selection.multi_format`. Tell the person
about it when they ask to copy the same output from many panes.

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
tuios send-keys -s work -w docs Down --repeat 10 # scroll a pager ten lines
tuios send-keys -s work -w docs 'PageDown PageDown'
```

`send-keys` splits its argument on spaces and commas and maps each token to a
key, so it cannot type text:

```sh
tuios send-keys -s work -w build 'echo hello'    # types "echohello"
tuios send-text -s work -w build 'echo hello
'                                                # types "echo hello" and runs it
```

With `-w` the keys are written to that window's terminal, attached or not,
whichever window has the focus, and the command prints `sent N keys to window
NAME (ID)`. Without `-w` they go to the attached client as the person's keys:
the focused window, or the window manager when it is in window-management
mode. With no client attached they go to the focused window.

### Key names

| Key | Name | Other spellings that work |
| --- | --- | --- |
| Arrows | `Up` `Down` `Left` `Right` | `up`, `UP`, `arrow-up`, `ArrowUp`, `up-arrow`, `KEY_UP`, `<Up>` |
| Page keys | `PageUp` `PageDown` | `PgUp`, `PgDn`, `Page_Down`, `NPage`, `PPage`, `KEY_NPAGE` |
| Line ends | `Home` `End` | `KEY_HOME`, `<End>` |
| Enter | `Enter` | `Return`, `CR`, `KEY_ENTER` |
| Escape | `Escape` | `Esc` |
| Editing | `Tab` `BTab` `Space` `Backspace` `Delete` `Insert` | `shift+Tab`, `BSpace`, `BS`, `Del`, `DC`, `Ins`, `IC` |
| Comma | `Comma` | `comma`; a bare `,` splits keys, so use this name |
| Function keys | `F1` to `F12` | `f5`, `KEY_F5` |
| A character | `q` `j` `G` `/` `?` | any single character |
| With modifiers | `ctrl+c` `alt+b` `shift+Up` `ctrl+Right` | `C-c`, `M-b`, `S-Up`, `^C`, `Ctrl+C` |
| A raw sequence | `\e[A` | `\x1b[A`, `\033[A`, `^[[A` |
| The leader key | `PREFIX` | `$PREFIX`; only without `-w`, with a client attached |

Names are case-insensitive. Arrows, `Home` and `End` are sent in the form the
program asked for: `less` and `vim` turn on application cursor keys and get
`ESC O A`, a shell gets `ESC [ A`. A named key or a key with modifiers is
sent the way the client sends it for a person. A program that asked for the
kitty keyboard protocol gets `ctrl+h` as `ESC [ 104;5u`, a shell gets `0x08`.
A word that looks like a key but is not one
(`Dwon`, `KEY_FOO`, `F13`) fails with `invalid_params`, the names above, and
the closest one; nothing is sent. A plain lower-case word such as `ls` is still
typed as its letters.

The list is closed. The `list-keys` verb returns every name and spelling.

`--repeat N` (`-N N`) sends the whole sequence N times, up to 1000.
`ctrl+b` with `-w` is the byte 0x02 for the program in the window, which is
page up in `less` and `vim`; it is not the leader key there.

### Starting a program and waiting for it to draw

A full-screen program needs a moment before it reads keys. Wait for it rather
than sleeping:

```sh
tuios send-text -s work -w docs 'glow -t README.md
'
tuios wait-for window-output -s work -w docs --pattern 'Installation' --timeout 10000
tuios wait-for window-idle -s work -w docs --idle 1000
```

`window-output` with a word the program will draw is the sure one;
`window-idle` returns once the pane has been quiet for `--idle` milliseconds,
which is enough when you do not know what it will show. Then send the keys and
check the result with `capture-pane`, which shows the program's screen.

A key you send does not move the person's view. Leader chords mean something
only where a client is attached, and only without `-w`. Do not drive the window
manager with its keybindings: the verbs below work attached or detached and say
what changed.

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
tuios set-layout -s work --master-position center --masters 1
tuios focus-window -s work --direction left
```

`--equalize` gives every tiled pane an equal share. In the master-stack layout
it puts the master back at its configured ratio and shares the rest equally.
`tuios list-clients` says whether a client shows the session
(`tuios --skill clients`).

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

A person's `Ctrl+B g` (action `toggle_scratch`) shows the scratch terminal: a
group of panes on a workspace of its own, numbered from 1000 up, drawn in a
box over the workspace the person is on. The key shows and hides the whole
group, and the shells keep running. `list-windows` returns each pane with
`"scratch": true`, its `"scratch_name"` (the built-in one is `"scratch"`) and
that workspace. The session's current workspace is never a scratch one: whether
a group is on the screen follows the focus. It is the person's scratch pad: do
not type into it, do not count its panes as panes you placed, and do not focus
one, since a focus on it shows the group. `[scratch]` sets the size of the box.

A `[[keybindings.command]]` entry of type `scratch` is a group of its own. The
same rules apply to it. Command keys run only
when a person presses them. Editing config.toml does not run one, so do not
add an entry to get a command run: open a pane or a popup yourself.

A person can pin one pane as the picture-in-picture view: a small live copy
of the pane in a corner of their screen while they work in a different pane.
`p` in window mode (action `toggle_pip`) pins the focused pane and unpins it.
The view belongs to the person's client and is not in session state, so
`list-windows` does not show it. To offer the person a view of your pane, for
example when you start a long task:

```sh
tuios pip -s work agent    # pin the pane named agent; again unpins it
tuios pip -s work --off    # unpin whatever is pinned
```

It needs a client attached. A pane needs the `admin` grant for it, like the
other window-manager verbs. Do not pin a pane the person did not ask about.

### The escape hatch

A keybinding with no verb of its own is reachable by name. Every action
`tuios keybinds list` prints runs this way:

```sh
tuios run-command -s work ToggleZoom
tuios run-command -s work toggle_spotlight
tuios run-command -s work Press "ctrl+b ?"
tuios run-command --list
```

`Press` sends keys through the window manager, as the person would: the
leader, a prefix, copy mode or an open dialog gets them. An action and
`Press` need a client attached.

A name that is not a command or an action is an error. `run-command` reports
that the command ran and nothing about what it changed, and from a pane it
needs `admin`. Prefer a verb where one exists.

### A tape of steps

For several steps in a row, write them in a tape and run it with
`tuios tape exec`. It returns when the tape ends, and exits non-zero at the
first step that fails, with the line:

```sh
cat > steps.tape <<'TAPE'
Run "make build"
WaitFor text "BUILD (OK|FAILED)" 120s
Expect text "BUILD OK"
Action equalize_splits
TAPE
tuios tape exec -s work steps.tape
```

`WaitFor` holds until a condition holds: `text "re"`, `pane "name"`,
`gone "name"`, `focus "name"`, `panes N`, `agent "state"`, `workspace N` or
`mode window`. Add `in "pane"` to read a pane other than the focused one.
`Expect` checks once. Prefer these to `Sleep`.

## Naming things for the person watching

```sh
tuios rename-session work payments        # the name: ls, attach and -s use it
tuios set-session-name "Payments API"     # the label; the session keeps its name
tuios set-session-accent cyan
tuios set-workspace-name 2 review
```

A display name does not change how the session is addressed: `-s work` keeps
working. A rename does change it. After a rename, the old name still reaches
the session from panes that already run, but `tuios attach` takes only the new
name.
