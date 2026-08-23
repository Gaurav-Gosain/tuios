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

**`send-keys` is not for typing text.** It splits its argument on spaces and
commas and maps each token to a key, so the spaces are gone by the time anything
reaches the shell:

```sh
tuios send-keys -s work -w build 'echo hello'    # types "echohello"
tuios send-text -s work -w build 'echo hello
'                                                # types "echo hello" and runs it
```

Nothing warns you: the first form exits 0 and the pane shows a command that does
not exist. If what you are sending would be typed by a human on a keyboard, use
`send-text` and end it with a newline.

`--literal --raw` pushes characters through unparsed, which is `send-text` with
extra steps.

Leader chords only mean something where a client is attached, because the
bindings live in that client's interface. On a detached session `ctrl+b,n` is
delivered to the shell as the two bytes it spells, which is almost never what you
wanted. Do not drive the window manager by sending its keybindings: there are
verbs for that, they work attached or detached, and they tell you what changed.
See "Arranging panes" below.

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

To make the pane's process the program itself rather than a shell, put the argv
after the name. Nothing re-parses it, so nothing needs quoting, and the pane
closes when the program exits:

```sh
tuios new-window -s work htop /usr/bin/htop
```

The window is created by the daemon whether or not anyone is attached, so this
works on a detached session. Naming it means you never have to hold on to the
uuid. To keep the id instead:

```sh
id=$(tuios new-window -s work --json | jq -r .window_id)
```

Say where it goes and what it starts in, rather than creating one and moving it:

```sh
tuios new-window -s work tests --workspace 2 --cwd /src/api --no-focus
```

`--no-focus` is the one to reach for when you are opening a pane to work in
later. Without it the new pane takes the focus, which pulls the user out of
whatever they were doing.

The result says where the pane went, so you never have to read it back:

```sh
tuios new-window -s work tests --workspace 2 --json
```

```json
{"window_id":"19ba76b4-...","name":"tests","workspace":2,"pty_id":"198ec9d0-...","focused":true,"unplaced":true}
```

`unplaced` is worth understanding. The daemon has no viewport, so on a detached
session it gives a new pane a nominal box and says so. The width and height in
`list-windows` are that placeholder until a client attaches and places it. Do not
compute anything from a pane's geometry while `unplaced` is true.

Close it when the work is done:

```sh
tuios run-command -s work CloseWindow "$id"
```

On a detached session, a window whose shell has exited stays in the list until
something closes it, and `capture-pane` still reads its final screen. Close what
you open, or a loop that opens a window per run quietly accumulates dead ones.

## Arranging panes

Every arrangement has a verb. Use these rather than sending the keybinding that
triggers them: they work whether or not a client is attached, they do not depend
on the user's keymap, and each reports what actually changed.

```sh
tuios list-workspaces -s work                  # what exists and what is on it
tuios focus-window -s work build               # focus a named pane
tuios focus-window -s work --relative next     # cycle within the workspace
tuios move-window -s work 2 -w build --follow  # send a pane to workspace 2
tuios select-workspace -s work 2               # show workspace 2
tuios set-window -s work -w build --name "api tests"
tuios set-window -s work -w build --minimize
```

```
$ tuios list-workspaces -s work
 WS  NAME    WINDOWS
 *1  -       3
  2  review  1
  3  -       0
```

Focusing a window switches to that window's workspace, so `focus-window` is
usually all you need to get to a pane wherever it is.

### What needs a client attached

The daemon owns the window set, so where a pane is and which one has the focus
are its facts and it answers them detached. Geometry is the attached client's:
only something with a viewport can measure a split or a direction. These need a
client and say `needs_client` when there is none:

```sh
tuios split-window -s work vertical -w build --name logs
tuios set-layout -s work --tiling true --equalize
tuios focus-window -s work --direction left
```

`split-window` divides an existing pane and gives you the new one's id, which is
the placement you want when the panes should sit side by side. It needs tiling
on. Reading, writing, waiting, creating and moving never need a client.

### The escape hatch

A keybinding with no verb of its own is still reachable by name:

```sh
tuios run-command -s work ToggleZoom
tuios run-command --list
```

Prefer a verb where one exists. `run-command` reports that the command ran and
nothing about what it changed.

## Configuring the appearance

The sidebar, the dock, the borders, the scrollbar and the rest are all settable
at runtime. Find the option rather than guessing it:

```sh
tuios list-options --section sidebar
tuios list-options appearance.dock
tuios list-options --json | jq -r '.options[].path'
```

```
 PATH                              TYPE    DEFAULT  ACCEPTED
 appearance.sidebar.enabled        bool    false
 appearance.sidebar.position       string  left     left, right, hidden
 appearance.sidebar.width          int     28
 appearance.sidebar.show_agents    bool    true
```

Then set it and read it back:

```sh
tuios set-config appearance.sidebar.enabled true
tuios set-config appearance.sidebar.position right
tuios get-config appearance.dockbar_position
```

The path and the value are both checked, so a misspelled path or a value outside
the accepted set fails and says what it should have been. A call that reports
success changed something.

Two things to read in the result. `applied` says whether an attached client put
the change on screen; when it is false, `reason` says whether that is because
nobody is attached (the value is recorded and applies on the next attach) or
because the client refused it. And `get-config` answers with the value in effect,
with `source` saying whether it came from this session or from the default, so an
option nobody has touched still reads.

```sh
tuios get-config appearance.sidebar.position --json
```

```json
{"key":"appearance.sidebar.position","value":"left","source":"default","default":"left","option_type":"string"}
```

## Ricing: the four surfaces

A rice is not a palette. Four things decide what tuios looks like, and a request
like "make it look like X" usually means some of each:

| Surface | What it decides | How to set it |
|---|---|---|
| **Colour** | the twenty terminal colours, the accents, the borders | `appearance.theme`, `list-themes` |
| **Shape** | the characters the chrome is drawn with: border, controls, rules, rail marks | `appearance.glyphs`, `list-glyphs` |
| **Spacing** | ground between panes, padding inside overlay panels | `appearance.gap`, `appearance.panel_padding` |
| **Composition** | what a window title, a workspace tab and the clock carry | `window_title_format`, `dock_workspace_tab_format`, `clock_format` |

Colour and shape are the two that have their own verbs, because both are a name
from an open set standing for a document kept in a directory rather than a value
in the config file. Everything else is an option in `list-options`.

### Colour: themes

A theme's value is a name drawn from an open set of several hundred, standing
for twenty colours kept as JSON in a directory of their own. So it has its own
verb.

```sh
tuios list-themes --filter catppuccin
```

```
  catppuccin_frappe     catppuccin_latte      catppuccin_macchiato  catppuccin_mocha

4 of 343 registered themes.

active: gruvbox_dark (session)
themes dir: /home/you/.config/tuios/themes
```

Filter before you guess. Theme ids use underscores, so the name a human says
("Catppuccin Mocha") and the name that resolves (`catppuccin_mocha`) differ, and
this is where you find out which. Setting a name that does not resolve is an
error naming the closest one, not a silent no-op:

```sh
tuios set-config appearance.theme catppuccin_mocha
```

### Seeing what you just chose

You cannot see the screen. `capture-pane` gives you the text, not the palette,
so ask for the palette:

```sh
tuios list-themes catppuccin_mocha
```

```
catppuccin_mocha  (Catppuccin Mocha)  dark, background #1e1e2e

   fg             #cdd6f3  11.33:1  needs 4.5
   cursor         #f5e0dc  12.95:1  needs 3.0
 ! black          #454759   1.80:1  needs 3.0
   red            #f38ba8   7.08:1  needs 3.0
 ! bright_black   #585b70   2.46:1  needs 3.0
   ...
```

Each colour is measured against that theme's own background. The floor is 4.5
for the foreground, which is prose, and 3.0 for everything drawn as a glyph or a
block. `!` marks a colour that does not clear it, and `--json` puts the same
names in `.palette.illegible`:

```sh
tuios list-themes catppuccin_mocha --json | jq -r '.palette.illegible[]'
```

Two failing entries is normal and not a reason to reject a theme: almost every
palette keeps its blacks dim on purpose, and tuios lifts a border drawn from one
of them. A dozen failing entries means the palette is wrong. Text printed inside
a pane is never lifted, so a foreground under 4.5 is the one to act on.

### Writing a theme

Ricing usually means authoring a palette rather than picking one. Write
`<id>.json` into the themes directory that `list-themes` reported:

```json
{
  "id": "mine",
  "display_name": "Mine",
  "dark": true,
  "fg": "#c0caf5", "bg": "#1a1b26", "cursor": "#c0caf5",
  "black": "#15161e", "red": "#f7768e", "green": "#9ece6a", "yellow": "#e0af68",
  "blue": "#7aa2f7", "purple": "#bb9af7", "cyan": "#7dcfff", "white": "#a9b1d6",
  "bright_black": "#414868", "bright_red": "#f7768e", "bright_green": "#9ece6a",
  "bright_yellow": "#e0af68", "bright_blue": "#7aa2f7", "bright_purple": "#bb9af7",
  "bright_cyan": "#7dcfff", "bright_white": "#c0caf5"
}
```

Every field is optional except a way to name it: an absent `id` is taken from the
filename, and an absent colour falls back to its xterm default. It is `purple`,
not `magenta`. The directory is re-read whenever a theme is looked up, so the
file you just wrote is selectable immediately with no restart:

```sh
tuios set-config appearance.theme mine
tuios list-themes mine
```

A file that does not parse is skipped rather than applied, and `list-themes`
reports it under `problems` with the reason, which is how you find out that the
theme you wrote is not the theme you selected.

### From a terminal's own theme

Kitty, ghostty, alacritty and wezterm colour schemes convert directly. Do not
transcribe one by hand; one colour in the wrong slot looks exactly like a theme
that half-applied.

```sh
tuios import-theme ~/.config/kitty/current-theme.conf --name mine
tuios set-config appearance.theme mine
```

The format is read from the file's content, so the extension does not matter.
A scheme that sets only some of the twenty imports as far as it goes. Wezterm's
Lua scheme files are not read; its toml ones are.

### Shape: glyph sets

A theme moves the colours. A glyph set moves the characters: which corner the
border turns, what the window controls are pictures of, what a rule and a
separator are drawn with, which mark the rail wears on the row you are on.

```sh
tuios list-glyphs
```

```
  ascii                 default               heavy                 unicode

4 glyph set(s).

roles: add, arrow_left, arrow_right, attention, border.bottom, border.bottom_left,
border.bottom_right, border.left, border.middle, ... scrollbar_track, separator, sigil

active: default (default)
glyphs dir: /home/you/.config/tuios/glyphs
```

The four built-ins are `default` (what tuios ships), `unicode` (box drawing
only, no Nerd Font private-use glyphs), `heavy` (one stroke weight heavier
throughout, border included) and `ascii` (7-bit throughout).

```sh
tuios set-config appearance.glyphs heavy
```

**A set's border needs `border_style` to ask for it.** A set can carry a border
and most do not; the one that draws is whichever `appearance.border_style`
names, and `glyphs` is the value meaning "the active set's".

```sh
tuios set-config appearance.glyphs heavy
tuios set-config appearance.border_style glyphs
```

That is deliberate rather than a missing convenience: a set that won silently
would turn an option the user had already set into a no-op with nothing on
screen to say why. Both settings stay live and the one in charge is the one that
was named.

#### Seeing what a set actually draws

You cannot see the screen, and a set states only the roles it changes, so its
file is not the answer to "what will this look like". Ask:

```sh
tuios list-glyphs heavy
```

```
heavy  (Heavy)

   attention             █      █
   border.top_left       ┏      ┏
   bullet                ▪      ▪
   close                 -      ✕
   collapse              -      «
   rule                  ━      ━
   ...

columns: role, what the set says, what draws. ! marks a role the set
named and did not get. A role whose glyph was the wrong width for its
slot was dropped on load and is listed under problems below.
```

Two columns because they differ in two ways that matter. A role the set says
nothing about reads `-` on the left and shows the built-in on the right, which
is normal. A role that shows `!` was named and did not take, which under
`--ascii-only` means the glyph is not 7-bit.

#### Writing a set

Write `<id>.json` into the glyphs directory `list-glyphs` reported. Give it
`inherits` to start from a built-in and change one mark:

```json
{
  "display_name": "Mine",
  "inherits": "heavy",
  "bullet": "◦",
  "focus": "▐",
  "border": { "top_left": "╔", "top_right": "╗", "bottom_left": "╚", "bottom_right": "╝" }
}
```

Every field is optional and an absent `id` is taken from the filename. The
directory is re-read whenever a set is looked up, so a file you just wrote is
selectable with no restart:

```sh
tuios set-config appearance.glyphs mine
tuios list-glyphs mine
```

**Every role has a cell width and a glyph that misses it is dropped.** The
window controls' press rectangles are fixed offsets measured against buttons of
exactly three and four cells, so a two-cell emoji would not look bold, it would
move the close button out from under the pointer. `close`, `maximize`,
`minimize`, `focus`, `attention`, `bullet` and `add` are **one cell**; you name
the mark and the renderer owns the padding. `separator`, `ellipsis`, `collapse`
and `expand` take any width, because each is drawn somewhere that measures it. A dropped
role is reported rather than silently defaulted:

```sh
tuios list-glyphs mine --json | jq -r '.problems[]?'
```

```
glyph set mine: close is 2 cells wide and the layout budgets 1, so it keeps the default
```

That line is the one thing to check after writing a set. On screen a dropped
role looks exactly like a set that half applied.

### Spacing and composition

```sh
tuios set-config appearance.gap 2              # empty ground between tiled panes
tuios set-config appearance.panel_padding 4    # columns inside every overlay panel
tuios set-config appearance.dim_unfocused 40   # quiet the panes you are not in
tuios set-config appearance.clock_format "Mon 3:04PM"
tuios set-config appearance.window_title_format "{index}: {title}"
```

`appearance.gap` is i3's inner gap and is inner only. `clock_format` is a Go
time layout, so any spelling the standard library takes works; a layout with no
time in it is warned about rather than refused, because a fixed label is a
legitimate thing to want.

`appearance.dim_unfocused` is a percentage, 0 to 90, and 0 is off. It quiets the
**content** of panes that are not focused, which is most of the frame, and is
the setting to reach for when the user says they cannot tell which pane they are
in. It composes with `zen_mode` rather than duplicating it: zen takes the chrome
away, this quiets the content. Two things to tell the user:

- It reaches only cells a program coloured itself unless a theme is set. With no
  theme tuios emits colour indices and the host terminal decides what they look
  like, so a cell drawn in the terminal's own default has no colour tuios can
  carry anywhere. Set a theme first, or expect a plain shell prompt to stay
  bright.
- It dims content only. The border, the title bar, the scrollbar, the rail, the
  dock and every overlay are untouched, on purpose.

### A restyle, end to end

"Make it look like Catppuccin Mocha, heavier frame, roomy, sidebar on the
right, and I keep losing track of which pane I am in."

Work the four surfaces in order, because each one is checkable before the next:

```sh
# 1. Colour. Filter before you guess; the ids use underscores.
tuios list-themes --filter catppuccin
tuios set-config appearance.theme catppuccin_mocha
tuios list-themes catppuccin_mocha --json | jq -r '.palette.illegible[]'

# 2. Shape. The set, and then the border style that asks for the set's border.
tuios list-glyphs
tuios set-config appearance.glyphs heavy
tuios set-config appearance.border_style glyphs
tuios list-glyphs heavy --json | jq -r '.problems[]?'

# 3. Spacing.
tuios set-config appearance.gap 2
tuios set-config appearance.panel_padding 4

# 4. Composition, and the thing they actually asked for.
tuios set-config appearance.window_title_format "{index}: {title}"
tuios set-config appearance.dim_unfocused 45
tuios set-config appearance.sidebar.enabled true
tuios set-config appearance.sidebar.position right
```

Read back what you changed, not what you sent:

```sh
tuios get-config appearance.border_style --json
tuios list-themes --json | jq -r .active
tuios list-glyphs --json | jq -r .active
```

**Record the old values first.** There is no preview and no undo, and each call
lands as it is made:

```sh
for k in appearance.theme appearance.glyphs appearance.border_style \
         appearance.gap appearance.dim_unfocused; do
  printf '%s=%s\n' "$k" "$(tuios get-config "$k" --json | jq -r .value)"
done
```

### Restyling a terminal that cannot draw much

`--ascii-only` says the running terminal cannot manage more than 7-bit, and it
overrules a glyph set **per role** rather than throwing the set away: a set
keeps every role it spelled in ASCII and gives up only the ones it did not. So a
set written for a good font still behaves sensibly there, and the `ascii`
built-in is the one to inherit from when the terminal is the constraint.

`appearance.gap`, `appearance.panel_padding`, `appearance.dim_unfocused` and
`clock_format` are unaffected by ASCII mode: none of them is a glyph.

### What is set and what is derived

The line matters, because asking for the derived half wastes a call and a bad
answer to it would break something:

- **Set:** the theme, the glyph set, the border style, the gap, the padding, the
  dim, the format strings, the border colour overrides. All of it is in
  `list-options` or has a verb.
- **Derived, and not settable:** the contrast of every chrome label, mark and
  rule against whatever ground it lands on. tuios measures each against a floor
  (4.5:1 for a label, 3:1 for a mark, about 1.9:1 for a decorative rule) and
  lifts it until it clears. That is why a theme's dim blacks still produce a
  readable border, and why a border colour you set by hand is honoured while the
  chrome drawn on top of it is not left to chance.
- **Derived, and not settable:** the padded width of a window control. You name
  the one-cell mark; the three- and four-cell buttons the press rectangles are
  measured against are built from it.

### What this cannot do

Be honest with the user about these rather than working around them:

- **There is no preview and no undo.** A rice is applied one option at a time
  and each one takes effect as it lands. If the fifth call fails, the first four
  are still on. Record the values before you change them and put them back the
  same way:

  ```sh
  tuios get-config appearance.border_style --json | jq -r .value
  ```

- **There is no verb for keybindings or hooks.** Both are maps rather than fixed
  paths, so `list-options` does not carry them and `set-config` cannot set one.
  They are edited in the config file.

- **A glyph set cannot change the dock's semantic icons.** The mode chip, the
  window and workspace counts and the session controls are Nerd Font pictures of
  a meaning rather than shapes in a frame, so they are not roles. `--ascii-only`
  is what replaces them when the font cannot draw them.

- **Spacing is inner only, and horizontal in overlays.** `appearance.gap` puts
  ground between panes and none around the outside; `appearance.panel_padding`
  widens a panel's margins and does not change its rows.

- **The chrome is not themed.** Overlays, the settings page and the dock's
  furniture sit on a constant neutral ramp on purpose, the way a window manager
  keeps its chrome constant. A theme moves the panes, the borders, the accents
  and the tabs. If the user asks why the palette "did not apply" to a popup,
  that is why.

- **You cannot read the user's actual terminal colours.** With no theme set,
  tuios emits colour indices and the host terminal fills them in, so the sixteen
  on screen are the user's and tuios does not know what they are. "Match my
  terminal" means importing that terminal's scheme file, not asking tuios.
## Ricing: the dock's components

The dock is three ordered lists of named components. That is the whole
customisation model: reorder the names, drop the ones you do not want, or add a
command of your own whose first line of stdout becomes a cell.

```sh
tuios list-dock-components
```

Every placed component, in draw order, with the side it is on, how it refreshes,
what its cell reads now, and what its command last did. This is the enumeration
half; the last two columns are the verification half.

To add one, write a script and five lines of TOML. There is no manifest, no
install step and no restart: the config file is watched, so the cell appears
when you save.

```toml
[dock]
right = ["custom/agents", "cpu", "ram", "session-controls"]

[dock.custom.agents]
command  = "~/.config/tuios/dock/agents.sh"
refresh  = "event:after-agent-state"
on-click = "tuios list-windows"
```

Then check it landed:

```sh
tuios refresh-dock agents
tuios list-dock-components --json | jq '.components[] | select(.name=="custom/agents")'
```

The contract is environment in, one line of text out. Your command gets the
session environment plus `TUIOS_DOCK_COMPONENT`, `TUIOS_SESSION` and
`TUIOS_SOCKET`, so a component can call tuios verbs without being told where the
session is. SGR colour survives; every other escape is stripped.

`refresh` is one of four, and the order below is the order of cost:

| value | when it runs | idle cost |
|---|---|---|
| `event:TYPE` | when that event fires (`after-agent-state`, `after-focus-change`, `after-workspace-switch`, …; the event-hub spellings `agent-state`, `window-focused`, … also work) | none |
| `push` | the command stays running; each line it writes is an update | none |
| `"30s"` | polling, floored at one second | one timer for all pollers, no frame when the value has not moved |
| `once` | at startup, and on `tuios refresh-dock NAME` | none |

Prefer `event:` and `push`. A dock with no polling component arms no timer at
all, and that is a property worth keeping: it is why the built-in clock no
longer redraws the screen sixty times a second.

### When a component is not drawing

A component that fails, times out, or prints nothing is **hidden**, so the
absence is the symptom and `list-dock-components` is where the cause is. It
carries the exit code, the error and the last run time. After five consecutive
failures it stops being polled; `tuios refresh-dock NAME` revives it, which is
what to run after fixing the script.

Never conclude a component works because the config parsed. Read it back.

### Two things to know before you write one

- **A component runs where the client runs.** Locally that is the user's
  machine. Under `tuios-web` it is the machine running tuios-web, and over SSH
  it is the SSH host. A battery cell on a server reports the server's battery.
  Every attached client runs its own copy.
- **Anything that must happen while nothing is attached is a hook, not a
  component.** Components are UI and die with the client that drew them.

`examples/dock/` in the repo has five working recipes and a `dock.toml` that
wires all of them up.

## Checking the keybinds

tuios is a multiplexer, so its bindings compete with whatever runs inside it.
`keybinds doctor` reports both halves of that, and `--json` gives you the same
analysis the keybind overlay draws.

```sh
tuios keybinds doctor
tuios keybinds doctor --json | jq -r '.collisions[] | "\(.press) runs \(.winner)"'
tuios keybinds explain ctrl+w --json
```

Every finding carries an `evidence` field, and it decides how much weight the
finding takes:

- `certain` comes from tuios's own registry and dispatch order. A key claimed
  twice, or a key tuios withholds from the pane, is a fact about tuios.
- `observed` was read from a pane at that moment: the foreground process name,
  the alternate screen, the kitty keyboard flags the pane's program pushed. From
  the CLI there is no pane, so this tier is empty and the report says nothing
  about one.
- `reference` is a curated list of what common programs bind by default. Nothing
  is detected and nothing is asked. Treat it as a hint about where to look, never
  as a statement about the user's actual vim config.

Two conflicts are worth acting on. `collisions` are keys bound twice in one
scope, where `winner` is the action that runs and everything in `losers` is
dead; the `cross_section` ones are the ones the config file gives no hint of,
because the tables look unrelated and the later one is copied over the earlier.
`terminal_mode_swallowed` is every key that never reaches the program in a pane,
which is the list to check before telling a user their editor is broken.

`explain` answers for one key: every scope it acts in, whether the pane would
have received it, and the terminal-level pair it belongs to. Ctrl+I and Tab are
the same byte, as are Ctrl+M and Enter and Ctrl+[ and Esc, so binding one of
those binds the other unless the host terminal grants key disambiguation. Do not
suggest a binding on `ctrl+i`, `ctrl+m` or `ctrl+[` without saying so.

## Waiting instead of polling

Do not capture in a loop with a sleep. The daemon watches its own events and will
block for you, which is both exact and cheaper:

```sh
tuios wait-for window-output -s work -w build --pattern 'ok\s+github' --timeout 120000
tuios wait-for window-idle   -s work -w build --idle 2000
tuios wait-for window-exit   -s work -w build --timeout 600000
tuios wait-for session-exists -s work
tuios wait-for agent-state   -s work --until needs_input
```

- `window-output` matches a Go regular expression against what the pane prints,
  including scrollback. It is the right one when your command prints a marker.
- `window-idle` returns once the pane has printed nothing for `--idle`
  milliseconds. It is the right one when a command has no marker to match.
- `window-exit` returns when the pane's shell exits, which is what you want for a
  window opened to run one thing.
- `agent-state` returns when an agent pane reaches one of the `--until` states
  (comma-separated). With `-w` it watches that pane; without it, any agent in
  the session matches, so "tell me when an agent needs input" is one blocking
  call rather than a poll loop over `get-agent-state`.

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

There is no verb that runs a command and hands back its exit status: the daemon
writes bytes to a shell and reads what comes back, and it has no idea where one
command ends. Put the status in the marker and you get it for free:

```sh
n=$(date +%s)
tuios send-text -s work -w build "go test ./... ; printf 'done_%s_rc=%s\n' $n \$?
"
tuios wait-for window-output -s work -w build --pattern "done_${n}_rc=" --timeout 300000
tuios capture-pane -s work -w build --scrollback --lines 60 | grep -o "done_${n}_rc=[0-9]*"
```

```
done_1786700000_rc=0
```

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
tuios wait-for agent-state -s work --until needs_input,idle,done  # an agent got there
tuios get-agent-state -s work -w build               # what that pane reports now
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

`option_not_found` means the path names no option in this build, and its hint
carries the closest match; `list-options` describes them all.

`needs_client` means the operation needs a rendered interface and the session has
nobody attached. Reading, writing, waiting, creating and moving never need one;
splitting, tiling and directional focus do.

A parameter the verb does not take is refused rather than ignored, and the
failure lists what the verb does take. This matters more than it sounds: a call
carrying a name the daemon does not know would otherwise report success and
quietly do something else. If you get `invalid_params` naming a parameter you
believed in, the daemon you are talking to is older than you think, and
`list-verbs` will say what it has.

## The rest of the surface

Every command above is a wrapper over the daemon's verb protocol. To see the
whole protocol, with parameters, defaults and examples:

```sh
tuios list-verbs
tuios list-verbs capture-pane
tuios list-verbs --json
```

`list-verbs` is the whole contract: every verb, every parameter with its type and
accepted values, the shape of what comes back, the stable error codes, and the
request envelope. It is meant to be enough on its own, so if you are unsure what
something takes or returns, ask it rather than guessing.

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
- Use a verb, not a keybinding, to move things around. `send-keys` with a leader
  chord depends on the user's keymap, needs a client attached, and reports
  nothing about what happened.
- Call `list-options` before setting an option and `list-verbs` before calling an
  unfamiliar verb. Both are cheap, and both are exact about this build rather
  than about the version something was documented at.

## A note on the user's setup

A user can set `startup.daemon = true`, which makes a plain `tuios` attach to a
daemon-backed session instead of running a standalone one. It changes nothing
about how you drive tuios: what decides whether you have a socket is
`TUIOS_ENV`, which is what to guard on either way. If you are helping someone
debug a session that will not start, `tuios --standalone` and `TUIOS_NO_DAEMON=1`
both bypass that setting for a run and a shell respectively.
