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
and value rules), `[dock]`, `[hooks]`, `[hosts]`, `[agents.approvals]`,
`[agents.permissions]` and the keybindings. The file is watched; a hook the
daemon runs needs `tuios kill-server` to take effect.

## Ricing: the four surfaces

| Surface | What it decides | How to set it |
|---|---|---|
| **Colour** | the twenty terminal colours, the accents, the borders | `appearance.theme`, `list-themes` |
| **Shape** | the characters the chrome is drawn with | `appearance.glyphs`, `list-glyphs` |
| **Spacing** | ground between panes, padding inside overlay panels | `appearance.gap`, `appearance.panel_padding` |
| **Composition** | what a window title, a workspace tab and the clock carry | `window_title_format`, `dock_workspace_tab_format`, `clock_format` |

The 163 options above are scalars, and spacing and composition are set with them
like any other. Colour and shape are names from an open set, each standing for a
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
are answered with the painted colours.

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
Ctrl+I and Tab, Ctrl+M and Enter, and Ctrl+[ and Esc are the same byte unless
the terminal disambiguates them.

```sh
tuios keybinds unbind close_window w   # one key off one action
tuios keybinds free alt+left           # hand the key back to the pane
```

Both write an empty list on an action that runs out of keys. In config.toml an
action set to `[]` stays empty, while an action left out is filled from the
defaults. `free` cannot take the leader key or the keys the input path reads
directly.
