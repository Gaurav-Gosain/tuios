# Configuration, appearance, the dock, hooks and keybindings

## Options

Everything scalar is settable at runtime. Find the option rather than guessing
it:

```sh
tuios list-options --section sidebar
tuios list-options appearance.dock
tuios list-options --json | jq -r '.options[].path'
```

Each option gives its path, type, default, what it does, and the accepted values
when the set is closed. Then set it and read it back:

```sh
tuios set-config appearance.sidebar.width 30
tuios set-config appearance.sidebar.position left
tuios get-config appearance.dockbar_position --json
```

```json
{"key":"appearance.dockbar_position","value":"top","source":"default","default":"top","option_type":"string"}
```

The path and the value are both checked, so a typo fails and says what it
should have been. `applied` in the result says whether an attached client put
the change on screen; when false, `reason` says whether nobody is attached (it
applies on the next attach) or the client refused it. `get-config` answers with
the value in effect and its `source`.

Everything here is also reachable by the person on the settings page (`,` in
window mode), whose rows come from the same registry. Say so when you change
something for someone: there is a control they can adjust.

Tables are not scalar options and are edited in config.toml:
`[appearance.sidebar.agent_row]` (which tokens an agent row draws, their looks
and value rules; `now`, `context`, `subagents` and `prompt` read what the hooks
and status line feed, and `meta` leaves those keys out), `[dock]`, `[appearance.sidebar.custom]`, `[hooks]`, `[hosts]`, `[notify]`, `[agents.approvals]`, `[agents.checkpoints]`,
`[agents.permissions]`, `[agents] herdr_protocol` and the keybindings. The file is watched; a hook the
daemon runs needs `tuios kill-server` to take effect.

The config can be more than one file. config.toml can name more files in a
top-level `include = [...]` list, and the `*.toml` files in `config.d` next to
it are read too. config.toml wins over all of them. Before you edit a table by
hand, find the file that sets it:

```sh
tuios config files                     # every file, in merge order, read-only ones marked
tuios config origin hosts              # the file that sets each key under [hosts]
```

Edit that file, not config.toml. `set-config`, `tuios hosts add` and
`tuios keybinds unbind` already write only the changed key, to the file that
holds it. They never write a read-only file: the change goes to the last
writable file, and they say so. config.toml wins over every other file, so do
not copy a value into it that another file already sets.

A change to `[agents.permissions]` or `[hosts]` that gives panes or other
machines more waits for the person, and the Inbox says so. The person applies
it with `tuios config apply` in a terminal outside tuios. From a pane that
command is refused. Do not edit config.toml to widen what you hold: it waits
for the person, and the change tells them what you did.

`agents.enabled` is the person's switch for every agent feature. When it is
false, every agent verb fails with `agents_disabled`. Only the person can
change it: `set-config` from a pane gets `forbidden`. `tuios --skill
agents-off` says what still works.

`appearance.dock_compact` draws the dock as one row with no rule. The panes get
one more row. It works with the dock at the top and at the bottom.

`appearance.zoom_borderless` shows a zoomed pane on the whole pane region with
no border and no title bar. The pane's program gets the full size. With it on,
`zoom_size` and `zoom_max_width` have no effect.

`appearance.dock_mode_icon_window`, `dock_mode_icon_terminal` and
`dock_mode_icon_tiling` set the icon in the dock's mode pill. An empty value
shows no icon, and `default` puts back the icon of the glyph set. An icon can
be at most 8 cells wide, with no control characters. To remove the whole pill,
remove `"mode"` from `[dock] left`.

`daemon.window_size` sets the size of a session with more than one client:
`smallest` (the default), `largest`, or `latest`, the client that last had
input. `tuios set-config daemon.window_size latest` applies it to the session
at once, and `session-info` reports the policy in use as `window_size`. A
client smaller than the session shows the part around the focused pane's
cursor, so a pane can be wider than a person's screen. Change it only when the
person asks.

`daemon.ssh_agent = "follow"` keeps a link for each session to the ssh agent
socket of the client that attached or used it last. New panes get
`SSH_AUTH_SOCK` set to the link. A shell that started earlier keeps its old
value: `p=$(tuios ssh-agent-path 2>/dev/null) && export SSH_AUTH_SOCK="$p"` points
it at the link, and leaves the value alone when the option is off.
Through a host, it needs the option on both machines and agent forwarding
on the link (`ssh_options = ["-A"]` or `ForwardAgent yes`), which tuios never
turns on by itself. The option is in the file only. Do not change it, or the
forwarding, unless the person asks.

Hints mode (`Ctrl+B F`, the `hints` action) labels the URLs, paths, hashes and
addresses in the focused pane, and a typed label copies one. The
`hints_all_panes` action, or the `hints.all_panes` option, labels every pane
on the workspace. `hints.builtins`, `hints.alphabet`, `hints.open`,
`hints.dim` and `hints.all_panes` are options. `hints.patterns`
is a list of Go regular expressions in the file. It is for the person at the
keyboard: to read a pane, use `capture-pane`.

The pane labels (`Ctrl+B Q`, the `display_panes` action) put a large label
on each pane of the workspace, and a typed label focuses that pane.
`panes.label_keys` sets the keys (default `1234567890`). It is for the person
at the keyboard: to focus a pane, use `focus-window`.

The pane navigator (`Ctrl+B /`, the `choose_tree` action) is a tree of every
session, workspace and pane, with a preview and a search over names, folders,
commands and screen text. `panes.navigator_layout` sets the layout it opens in:
`tree` (default), `flat` or `cards`. It is for the person at the keyboard: a script reads
the same rows with `tuios list-windows --all --text N --json`.

## What the person sees

These are for the person at the keyboard. Know them so you can answer a
question about the screen. Do not change them unless the person asks.

- `appearance.max_fps` is the highest frame rate the client draws at: 10 to
  240, `0` for 60, or `auto` for the display's refresh rate.
- The spotlight (`B` in window mode, `Ctrl+B B` anywhere) dims the screen
  outside a beam. The dock shows a Spotlight chip with the key that turns it
  off. `[spotlight]` options set its size and dimming. It changes nothing a
  pane prints, so `capture-pane` is unaffected.
- Copy mode, multi copy mode and hints mode show a legend of their
  keys in the dock while the mode is open.
- A dock message that is too long ends with `more`. A click on it, or
  `Ctrl+B N` for the last message, opens the message view with the whole text.
  A notice you post with `send-agent-message` to the session can show there.
- `Ctrl+click` on a link opens it. `appearance.links`,
  `appearance.link_click` and `appearance.link_opener` set which links,
  which click and which opener.
- The rail's custom section shows the rows a command prints. The person sets
  it in `[appearance.sidebar.custom]` in the file, and places it with
  `appearance.sidebar.sections`. `set-config` cannot set its command.
  `tuios refresh-dock rail/custom` runs it again, and `list-dock-components`
  lists it as `rail/custom`.
- In the rail's files section, `Y` or the folder menu's Copy path copies a
  path to the person's clipboard.
- When the last pane on the workspace on screen closes, the session shows
  the workspace the person came from: the one that ran `xpanes`, else the
  ones shown before, else the lowest with panes. `workspaces.return_when_empty
  = false` keeps the empty workspace on screen.
- `workspaces.new_window_when_empty = true` opens a pane when the person
  switches to an empty workspace by key or click. `move_and_follow`,
  `xpanes`, `select-workspace` and `run-command` switches open none.
- `Ctrl+B =` (or `tuios set-layout --equalize`) gives tiled panes equal
  shares. In the master-stack layout it puts the master back at its
  configured ratio.

## Ricing: the four surfaces

| Surface | What it decides | How to set it |
|---|---|---|
| **Colour** | the twenty terminal colours, the accents, the borders | `appearance.theme`, `list-themes` |
| **Shape** | the characters the chrome is drawn with | `appearance.glyphs`, `list-glyphs` |
| **Spacing** | ground between panes, padding inside overlay panels | `appearance.gap`, `appearance.panel_padding` |
| **Composition** | what a window title, a workspace tab and the clock carry | `window_title_format`, `dock_workspace_tab_format`, `clock_format` |

The dock's workspace tabs take two composition knobs of their own:
`dock_workspace_tab_format` is the format string each tab prints, and
`appearance.dock_workspace_label_max` caps the label in cells, so one long
name cannot push the other pills off the bar. The cap is 12; `0` draws the
whole name and lets the strip scroll.

The options `list-options` prints are scalars, and spacing and composition are
set with them like any other. Colour and shape are names from an open set, each standing for a
file in a directory, so each has a verb of its own.

### Colour: themes

```sh
tuios list-themes --filter catppuccin
```

```
  catppuccin_frappe     catppuccin_latte      catppuccin_macchiato  catppuccin_mocha

4 of 343 registered themes.

active: gruvbox_dark (session)
themes dir: /home/you/.config/tuios/themes
```

Filter before you guess: ids use underscores. You cannot see the screen, so ask
for the palette and its contrast:

```sh
tuios set-config appearance.theme catppuccin_mocha
tuios list-themes catppuccin_mocha
tuios list-themes catppuccin_mocha --json | jq -r '.palette.illegible[]'
```

Each colour is measured against the theme's own background: 4.5 for the
foreground, 3.0 for everything else. `!` (and `.palette.illegible`) marks one
that does not clear it. Two dim blacks is normal; a foreground under 4.5 is the
one to act on.

To write a theme, put `<id>.json` in the themes dir `list-themes` reported (keys
`fg`, `bg`, `cursor`, `black` through `white` and `bright_black` through
`bright_white`; it is `purple`, not `magenta`). It is selectable at once. A file
that does not parse is listed under `problems`. To convert a kitty, ghostty,
alacritty or wezterm scheme rather than transcribe it:

```sh
tuios import-theme ~/.config/kitty/current-theme.conf --name mine
tuios set-config appearance.theme mine
```

### Shape: glyph sets

```sh
tuios list-glyphs
tuios set-config appearance.glyphs heavy
tuios set-config appearance.border_style glyphs
tuios list-glyphs heavy --json | jq -r '.problems[]?'
```

The built-ins are `default`, `unicode`, `heavy` and `ascii`. A set's border is
drawn only when `appearance.border_style` is `glyphs`. A set file goes in the
glyphs dir `list-glyphs` reported and can `inherits` a built-in. `close`,
`maximize`, `minimize`, `focus`, `attention`, `bullet` and `add` must be one
cell wide; a glyph of the wrong width is dropped and named under `problems`,
which is the one thing to check after writing a set.

### Spacing and composition

```sh
tuios set-config appearance.gap 2
tuios set-config appearance.panel_padding 4
tuios set-config appearance.dim_unfocused 40
tuios set-config appearance.clock_format "Mon 3:04PM"
tuios set-config appearance.window_title_format "{index}: {title}"
```

`dim_unfocused` (0 to 90) quiets the content of unfocused panes. It reaches only
cells a program coloured itself unless a theme is set.

`appearance.modal_dim` (0 to 90, default 30) darkens the screen behind an open
panel such as the command palette; 0 turns it off. `appearance.motion` is
`none`, `basic` (window slides and the copy sweep) or `full` (the default: also the panel
fade-in and the shimmer on a working agent's rail row). The old
`animations_enabled` still works and maps `false` to `none`.

**Record the old values first.** There is no preview and no undo:

```sh
for k in appearance.theme appearance.glyphs appearance.border_style \
         appearance.gap appearance.dim_unfocused; do
  printf '%s=%s\n' "$k" "$(tuios get-config "$k" --json | jq -r .value)"
done
```

### What this cannot do

- **There is no preview and no undo.** Each call lands as it is made.
- **Recording the old value and putting it back does not always work.** An
  option whose default is the empty string while its accepted set has no empty
  value cannot be written back to that default. 3 options are in that state today:
  `appearance.sidebar_position`, `appearance.whichkey_position` and
  `notifications.agent.sound_mode`. A
  `value` of `""` with `source` `default` means you cannot set it back; tell
  the person which options you changed and cannot restore.
- **There is no verb for keybindings, and hooks are read only.** Both are edited
  in the config file.
- **A glyph set cannot change the dock's semantic icons.** `--ascii-only` is
  what replaces them.
- **The chrome is not themed.** Overlays and the settings page sit on a
  constant neutral ramp on purpose.
- **You cannot read the person's terminal colours.** With no theme set, the
  terminal fills the colour indices. "Match my terminal" means importing its
  scheme file.

### Colour: the backgrounds

```sh
tuios set-config appearance.background theme                 # every surface
tuios set-config appearance.pane_background '#1e1e2e'         # one surface
tuios set-config appearance.dock_background off               # keep one bare
tuios set-config appearance.sidebar.background ''             # follow background again
```

A cell with no background of its own is transparent, so the person's terminal
shows through. The background options paint it instead: `off` paints nothing,
`theme` paints the theme's background and gives default-coloured text the
theme's foreground, and `#RRGGBB` paints that colour. `appearance.background`
(default `off`) covers every surface; `pane_background`,
`desktop_background` (gaps, the space around panes, an empty workspace),
`window_chrome_background` (borders, title bars, shared-border lines),
`dock_background` and `sidebar.background` each override it for one surface,
and empty follows it. A colour a program or the chrome set itself always wins,
so a border keeps its ink. `theme` with no theme set paints nothing. A colour
literal with no theme keeps the terminal's own text colour, so pick one that
reads under it. While panes are painted, a program's OSC 11 and OSC 10 queries
are answered with the painted colours. With no theme and nothing painted,
OSC 10, OSC 11 and OSC 4 for the sixteen are answered with the host
terminal's own colours, which the attached client asks its terminal for, so a
pane on a light terminal is told it is light. A terminal that answers no colour
query (mosh) leaves the defaults: black and white.

## The dock's components

The dock is three ordered lists of named components, in the `[dock]` table.
A custom component is a command whose first line of stdout becomes a cell:

```toml
[dock]
right = ["custom/agents", "cpu", "ram", "session-controls"]

[dock.custom.agents]
command  = "~/.config/tuios/dock/agents.sh"
refresh  = "event:after-agent-state"
on-click = "tuios list-windows"
```

```sh
tuios refresh-dock agents
tuios list-dock-components --json | jq '.components[] | select(.name=="custom/agents")'
```

`refresh` is `event:TYPE` (no idle cost), `push` (the command stays running and
each line is an update), a polling interval such as `"30s"`, or `once`. A
component that fails or prints nothing is hidden, and `list-dock-components`
says why. A component runs where the client runs and dies with it: anything that
must happen while nothing is attached is a hook. `examples/dock/` in the repo has
working recipes.

## Hooks

A hook runs a shell command on an event, with `TUIOS_*` variables carrying the
facts. The daemon runs `after-new-window`, `after-close-window`,
`after-focus-change`, `after-workspace-switch`, `after-agent-state` and
`after-command-finished`, so they fire with nobody attached. `after-attach`,
`after-detach`, `after-resize` and `after-layout-change` run in the client.

```toml
[hooks]
after-agent-state = ["~/.config/tuios/hooks/alert.sh"]
```

`after-agent-state` fires for the states `[notifications.agent]` alerts on, and
gets `TUIOS_AGENT_STATE`, `TUIOS_AGENT_PREV_STATE`, `TUIOS_AGENT_HARNESS`,
`TUIOS_AGENT_MESSAGE`, `TUIOS_WINDOW_ID`, `TUIOS_WINDOW_NAME` and
`TUIOS_SESSION_ID` (the session's name). `tuios --skill recipes` has a phone
alert built on it.

```sh
tuios list-hooks
```

No row means the event name is wrong. `RUNS` of 0 means the event never
happened. A non-zero exit means the command failed, and the error says why.

## Checking the keybinds

```sh
tuios keybinds doctor
tuios keybinds doctor --json | jq -r '.collisions[] | "\(.press) runs \(.winner)"'
tuios keybinds explain ctrl+w --json
tuios keybinds doctor --guest nvim
```

`certain` findings come from tuios's own registry, `observed` ones from a pane,
and `reference` ones from a list of common programs' defaults (a hint, never a
fact about the person's config). `collisions` are keys bound twice in one scope;
`terminal_mode_swallowed` is every key that never reaches a pane's program.
`key_problems` lists every key in config.toml that tuios cannot read. Ctrl+I and
Tab, Ctrl+M and Enter, and Ctrl+[ and Esc are the same byte unless the terminal
disambiguates them.

A modifier has more than one spelling. `opt+` and `option+` mean `alt+` on every
platform, `cmd+` and `command+` mean `super+`, and `control+` means `ctrl+`. The
leader, every binding table, `explain`, `free` and `unbind` read all spellings
as one key. `explain` and `doctor` show the spelling tuios matches, for example
`opt+f12 (tuios reads it as alt+f12)`.

On a layout for a non-Latin script, bindings match the physical key. A key that
types `ш` on a Ukrainian layout runs the binding on `i`, the US key at the same
position, unless `ш` has a binding of its own. This needs Ghostty, kitty,
WezTerm or foot. Latin layouts (AZERTY, QWERTZ, Dvorak) match the key that is
typed, with or without `ctrl`: Dvorak `ctrl+b` is the leader, and showkeys and
the recorder name it `ctrl+b`.
`keybinds explain` checks the key as written, so give it the Latin key.

A shifted digit has US aliases: `opt+shift+7` also matches `opt+&`. A binding
written for the key itself wins over an alias, and `explain` lists an alias
match as `on a US layout`. Set `keybindings.keyboard_layout = "other"` to turn
the aliases off on AZERTY and other layouts. On macOS a character that Option
composed runs the Option binding it stands for on a US
layout. Set `keybindings.option_glyphs = "type"` to send it to the pane
instead.

```sh
tuios keybinds unbind close_window w   # one key off one action
tuios keybinds free alt+left           # hand the key back to the pane
```

Both write an empty list on an action that runs out of keys. In config.toml an
action set to `[]` stays empty, while an action left out is filled from the
defaults. `free` cannot take the leader key or the keys the input path reads
directly.

### Copy mode for a tmux user

Copy mode starts with its cursor on the terminal cursor, usually the prompt
line. `appearance.selection.copy_entry = "center"` starts it on the middle row.
In copy mode, `/` searches down and `?` searches up. `n` repeats the last search
in its direction, and `N` goes the other way.

tmux `bind-key b copy-mode \; send-keys ?` is one action in tuios:
`copy_mode_search_backward`. `copy_mode_search_forward` opens `/`. Neither
action has a default key. Add the binding to config.toml:

```toml
[keybindings.prefix_mode]
copy_mode_search_backward = ["/"]
```

A key under the leader does not work inside copy mode, because copy mode uses
`Ctrl+B` for page up. A key in `global` or `terminal_mode` works in both.

tuios cannot put two actions on one key. `tuios send-keys` cannot drive copy
mode, because copy mode ignores remote keys.

tmux `copy-command` is `appearance.selection.copy_command`: every yank in copy
mode goes to that command on stdin, and its stdout goes to the clipboard.
tmux `copy-pipe` and `copy-pipe-and-cancel` are `[[keybindings.copy_pipe]]`
entries. `cancel = true` leaves copy mode after the yank, and `false` stays
where it is with the selection cleared:

```toml
# tmux: bind -T copy-mode-vi p send -X copy-pipe-and-cancel 'tr "\n" " "'
[[keybindings.copy_pipe]]
key = "p"
command = "tr '\n' ' '"
cancel = true
description = "flatten"
```

When the command writes nothing, fails or runs past 10 seconds, the clipboard
gets the plain selection, and a failure shows the exit code and the first
line of stderr on the dock. The command runs on the machine of the tuios
client, with the command-key variables (`TUIOS_SESSION`,
`TUIOS_ACTIVE_PANE_ID`, `TUIOS_ACTIVE_PANE_CWD`). In multi copy mode it runs
once, on the text `y` copies in the current format.

tmux paste buffers are tuios paste buffers. Each yank is also kept as a buffer
in the daemon, which every client and session shares. `PREFIX ]` pastes the
newest (`paste_buffer`), and `PREFIX #` lists them to choose one
(`choose_buffer`): tmux's `=` is equalize splits here. `[paste_buffers]`
`limit` (default 20, counts only the buffers tuios named, 0 keeps none) and `max_kb` (default 16384, which is 16 MiB) bound them.
`tuios list-buffers`, `show-buffer`, `set-buffer`, `delete-buffer` and
`paste-buffer` work on the same buffers, as does `tmux` under the shim.
