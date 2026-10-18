# Themes

TUIOS ships a large set of built-in color themes and can load custom ones from
JSON files in your config directory. A theme supplies the 16 ANSI colors plus
foreground, background and cursor; TUIOS derives its own UI colors (borders,
overlays, the dockbar) from them.

## Table of Contents

- [Selecting a Theme](#selecting-a-theme)
- [Edit a Theme](#edit-a-theme)
- [A Light and a Dark Theme](#a-light-and-a-dark-theme)
- [Match Your Terminal](#match-your-terminal)
- [Programs That Follow the Theme](#programs-that-follow-the-theme)
- [Custom Themes](#custom-themes)
- [Theme File Format](#theme-file-format)
- [Start from Another Theme](#start-from-another-theme)
- [Export and Share a Theme](#export-and-share-a-theme)
- [Looks](#looks)
- [Defaults for Omitted Colors](#defaults-for-omitted-colors)
- [Chrome Colors](#chrome-colors)
- [Colour Depth](#colour-depth)
- [Lists: Focus and Hover](#lists-focus-and-hover)
- [Limitations](#limitations)

## Selecting a Theme

By config file:

```toml
[appearance]
theme = "dracula"
```

By command line, which takes precedence over the config file:

```bash
tuios --theme dracula
tuios --list-themes                  # every registered theme ID, custom ones included
tuios --preview-theme dracula        # print the theme's 16 ANSI colors
tuios --theme $(tuios --list-themes | fzf --preview 'tuios --preview-theme {}')
```

Against a running daemon, `tuios list-themes` (the subcommand, not the flag)
lists everything the daemon can apply, filterable and with `--json`, re-reading
the themes directory on each call. `tuios import-theme <file>` converts a kitty,
ghostty, alacritty, or wezterm colour scheme into a theme file here, sniffing
the format from the file's content.

In the running app, the command palette (`Ctrl+P`) has a **Theme picker** entry,
and the settings page (`Ctrl+B` `,`) has a Theme row that opens the same picker.
The picker is searchable and shows a color swatch for each theme; cancelling
restores the theme that was active when you opened it.

Type to filter the list. The picker also takes these keys:

| Key | What it does |
|---|---|
| `ctrl+e` | Edit the highlighted theme in the theme editor |
| `ctrl+o` | Make a new theme from the highlighted one, and open it in the theme editor |
| `ctrl+f` | Show all themes, then only dark ones, then only light ones |
| `ctrl+r` | Go to a random theme in the list |
| `ctrl+y` | Copy the id of the highlighted theme |
| `ctrl+d` | Delete the highlighted theme file. For an edited built-in theme, remove your edit. Press `y` to confirm |

A theme from a file in your themes directory shows `(yours)`. A file that
uses the id of a built-in theme shows `(edited)`. You cannot delete the theme
that is in use, or a theme file that another file names as its `base`.

The arrow keys preview the highlighted theme on the whole screen. In a client
of `tuios ssh` or `tuios-web` they do not: the theme applies to every client of
that server, so the picker shows a sample line in the highlighted theme
instead. Enter applies the theme. The glyph picker works the same way.

`charmtone` and `charmtone-light` use the colours of Charm's CharmTone. Their
chrome uses the Charple accent. See
[CharmTone themes](CONFIGURATION.md#charmtone-themes).

Leaving the theme unset disables theming entirely and TUIOS uses your terminal's
own colors. An unknown theme name logs a warning and leaves the colors as they
were, rather than failing to start.

## Edit a Theme

The theme editor changes a theme's colours while you look at the whole screen.
It opens as a sheet on the side with no rail. The panes do not move.

To open it:

- Command palette (`Ctrl+P`): **Edit theme**, for the theme in use.
- Settings > Appearance: **Edit theme**.
- Theme picker: `ctrl+e` edits the highlighted theme. `ctrl+o` makes a new
  theme from it, named `<theme>-custom`.

The sheet has three tabs:

- **Quick**: two seeds, the accent and the surface, and whether the theme is
  dark. The rest of the chrome follows them: the dialog greys and the text
  inks from the surface, the pills and marks from the accent and the sixteen.
  A new theme opens here.
- **Roles**: the chrome roles, in groups: Accents, Frame, Dock and rail,
  Dialogs and Status. `Text`, `Dim text` and `Quiet text` show their measured
  colour. You cannot set them.
- **Pane colours**: the foreground, the background, the selection, the cursor
  and the sixteen. Programs in panes use these colours.

Each row shows the role, a swatch, where the colour comes from, its hex and,
for a colour with text or a mark on it, its contrast. Where the colour comes
from is one of `custom` (a hex in this theme), `slot` or `role` (a name in
this theme), `from base`, or `set in config.toml` (a key in the config wins).
A contrast under its floor shows `!`: 4.5:1 for text, 3:1 for a mark, a pill
or a border. At 256 colours the contrast is for the colours the terminal
shows. At 16 colours the sheet shows no contrast, because the terminal
decides those colours.

| Key | What it does |
|---|---|
| `↑` `↓` | Move between rows |
| `tab`, `shift+tab` | Go to the next or the previous tab |
| `enter` | Open the colour picker on the row. A slot chip stores the slot's name, so the colour follows the theme. On **Dark theme**, switch between dark and light |
| `e` | Type a value: hex, a slot, a role or a CharmTone name. `tab` completes the name |
| `x` | Give the row back to the base |
| `f` | Fix the row's contrast: make it lighter or darker until it reads on its ground. A strong colour that cannot get light or dark enough also becomes less strong. Not at 16 colours |
| `u` | Undo the last change |
| `ctrl+s` | Save the theme file |
| `space` | Hide the sheet to see the whole screen. Press `space` again to show it |
| `esc` | Close the editor. With changes that are not saved, press `esc` two times |

- When the cursor lands on a role, every piece of the screen in that role
  shows in its inverse colour for a moment, so you can see where the role is
  used. A role that may not be on the screen, such as a notification or the
  copy-mode pill, shows a sample under the list.
- At 256 colours, the sheet says when two status colours look the same.
- Each change shows on the whole screen at once. In a client of `tuios ssh`
  or `tuios-web` the theme is the server's, so only the sheet shows the
  change.
- `ctrl+s` writes the theme's file with only what differs from the base. A
  new theme saves as `<id>.json` in the themes directory. A built-in theme
  saves as an edited copy under its own id. Every client on the machine reads
  the file again.
- If the file changed on disk after the editor read it, `ctrl+s` tells you
  and writes nothing. Press `ctrl+s` again to replace the file.
- A client of `tuios ssh` or `tuios-web` cannot save.
- When the editor closes, the theme that was in use comes back. If you saved
  the theme in use, it comes back with your changes.

## A Light and a Dark Theme

`theme_light` sets a second theme for when your terminal is light. `theme` is
then the dark theme:

```toml
[appearance]
theme = "catppuccin_mocha"
theme_light = "catppuccin_latte"
```

- tuios asks the terminal for its background when it starts, and uses
  `theme_light` on a light background.
- A terminal that follows the system's light and dark switch (ghostty, kitty
  and others) tells tuios when the switch happens. tuios then changes the
  theme.
- With `theme_light` set, the theme picker asks what a pick is for. Press `d`
  for the dark theme, `l` for the light theme or `b` for both. Any other key
  goes back to the list.
- A `--theme` flag wins over `theme_light`. tuios then uses the theme of
  the flag on a light and on a dark terminal.
- Only the local client follows the switch. A client of `tuios ssh` or
  `tuios-web` uses `theme`.

## Match Your Terminal

If your terminal already has a theme, in kitty, ghostty or another terminal,
tuios can use it with no theme file:

```toml
[appearance]
theme = "none"
```

- The panes use the terminal's own colours. A program in a pane that asks for
  the background or the sixteen colours gets the terminal's answer.
- The dialogs, the rail and the dock use a ramp built from the terminal's
  background. On a dark background the dialogs sit one step lighter than it.
  On a light background they sit one small step darker.
- The accents stay tuios's own. To change them, use a theme file with a
  `chrome` object.
- `tuios theme reload` also asks each terminal for its colours again. Run it
  after a tool changes the terminal's colours.

An empty `theme` keeps the terminal's colours for the panes too, but the
dialogs keep tuios's constant ramp on a dark terminal. In a client of
`tuios ssh` or `tuios-web`, the dialogs keep the constant ramp, because one
server serves many terminals.

## Programs That Follow the Theme

A program in a pane can ask tuios for the theme colours. OSC 10 gives the
text colour, OSC 11 gives the background and OSC 4 gives one of the sixteen
colours. The answers are the colours that the pane shows.

A program can also ask tuios to tell it when the colours change:

- A program that turns on mode 2031 (`CSI ? 2031 h`) gets `CSI ? 997 ; 1 n`
  on a dark background or `CSI ? 997 ; 2 n` on a light background.
- tuios sends this notice each time the theme colours change. This includes a
  change from one dark theme to another dark theme.
- tuios sends one notice for each theme change, also when the background, the
  text colour and the sixteen colours all change.
- While the theme picker or the theme editor is open, tuios sends no notice.
  The panes show each theme. When you pick a theme, tuios sends one notice.
  When you cancel, tuios sends nothing.
- With two or more clients in one session, the panes report the theme of the
  client that you used last. A client that you do not use does not change it.
- `CSI ? 996 n` asks for the same report at any time.

Neovim 0.11 and later asks for the background again when the notice comes, and
sets `'background'`. opencode with the `system` theme asks for all the colours
again and draws with the new colours.

A program that uses only the sixteen ANSI colours changes colour with the theme
and needs no notice.

For a program that reads its colours from a file, use the `after-theme-change`
hook. The hook gets the theme id, the background and a light flag. See
[HOOKS.md](HOOKS.md).

## Custom Themes

Custom themes are `.json` files in the themes directory:

```
~/.config/tuios/themes/
```

More precisely `$XDG_CONFIG_HOME/tuios/themes/`, following the same XDG rules as
the config file. The directory is created for you.

Every `*.json` file directly in that directory is loaded at startup and
registered alongside the built-in themes, which means a custom theme can be
selected by `theme = "..."`, by `--theme`, and from the picker exactly like a
built-in one. Subdirectories are not scanned. A file that fails to parse is
skipped with a warning in the log and does not prevent the other themes, or the
app, from loading.

Every tuios client watches the themes and glyphs directories. When you save a
file there, the client reads the files again and repaints. An edit to the
theme in use shows at once, with no restart and no new id. A file that does
not load shows a warning, and the other themes stay. New output in a pane
takes the edited colours.

`tuios theme reload` does the same in every client on this machine. Use it
when a program writes a theme file and you want the clients to read it now:

```sh
# pywal: write a tuios theme from the wallpaper, then reload
wal -i ~/wall.png -o "tuios theme reload"
```

For matugen, write the theme from a template and set `post_hook = "tuios theme
reload"` on that template.

## Theme File Format

The file is a JSON object. Colors may be written either as a hex string or as an
RGBA object:

```json
{
  "id": "my-theme",
  "display_name": "My Theme",

  "fg": "#e5e5e5",
  "bg": "#101014",
  "cursor": "#e5e5e5",

  "black":   "#1b1b23",
  "red":     "#e06c75",
  "green":   "#98c379",
  "yellow":  "#e5c07b",
  "blue":    "#61afef",
  "purple":  "#c678dd",
  "cyan":    "#56b6c2",
  "white":   "#abb2bf",

  "bright_black":  "#4b5263",
  "bright_red":    "#ef7a83",
  "bright_green":  "#a9d18a",
  "bright_yellow": "#f0cc8c",
  "bright_blue":   "#72bcff",
  "bright_purple": "#d788ee",
  "bright_cyan":   "#67c5d3",
  "bright_white":  "#ffffff"
}
```

The RGBA form for any color field is `{"r": 255, "g": 0, "b": 0, "a": 255}`.

Two fields control identity:

- `id` is the name you select the theme by. If it is omitted, it is derived from
  the filename: `~/.config/tuios/themes/My-Theme.json` becomes `my-theme`
  (lowercased, extension stripped).
- `display_name` is what the picker shows. If omitted it falls back to the `id`.

Note the color names: TUIOS uses `purple`, not `magenta`.

## Start from Another Theme

`base` names a theme to start from. A field the file leaves out comes from the
base:

```json
{
  "id": "dracula-ember",
  "base": "dracula",
  "red": "#ff5555",
  "chrome": { "accent": "bright_red", "accent_bright": "#ff9e64" }
}
```

- The base is a built-in theme or another file in the themes directory. A base
  can have a base of its own.
- `dark` comes from the base unless the file sets it.
- The base's `chrome` fields are kept unless the file sets them. A slot name
  or a role name in `chrome` is read after the merge, so `"accent":
  "bright_red"` follows a `bright_red` the file changes.
- A file can use the id of a built-in theme and name that theme as its base.
  This edits the built-in theme for you only.
- Files that name each other as base load as none of them. `tuios list-themes`
  shows the loop.

## Export and Share a Theme

`tuios theme export <theme>` prints a theme as a theme file:

```sh
tuios theme export dracula-ember > dracula-ember.json
```

- A theme from your themes directory is written against the built-in theme it
  starts from. The file has the `base` and only the colours that are
  different. A base that is another file of yours is followed to the built-in
  theme, so the file loads on a machine that does not have your other file.
- A built-in theme is written with all its colours and no `base`.
- `--full` writes all the colours of any theme, with no `base`.
- `--format base16` writes a base16 scheme in YAML for other programs.
  `base00` is the background. `base01` and `base02` are the theme's `surface`
  and `card` when it sets a `surface`. `base08`, `base0A`, `base0B`, `base0C`
  and `base0D` are `error`, `warning`, `success`, `accent_bright` and
  `accent`.

`tuios theme share <theme>` prints the same file as one line, for a chat
message or a comment:

```
tuios-theme:v1:eyJpZCI6ImRyYWN1bGEtZW1iZXIiLCJiYXNlIjoiZHJhY3VsYSIs...
```

The part after `tuios-theme:v1:` is the file in base64url with no padding.

## Looks

A look is one TOML file that sets how tuios looks: theme, glyph set, borders,
gaps, dock and rail style. It can also hold a theme and a glyph set. The
[gallery](https://tuios.dev/gallery) shares looks, and `tuios look apply`
installs one. [CLI_REFERENCE.md](CLI_REFERENCE.md#looks-tuios-look) describes
the commands.

```toml
look = 1                        # the format version, required
name = "Tokyo blocks"           # optional
author = "someone"              # optional

[appearance]
border_style = "rounded"
gap = 1

[appearance.sidebar]
row_style = "pill"
sections = "sessions,terminals,git"

[dock]
left = ["mode", "workspaces"]
right = ["clock", "session-controls"]

[dock.tabs]
style = "flat"
number_glyphs = ["一", "二", "三"]

[theme]                         # optional: a theme, with the keys of a theme file
id = "tokyo"
base = "tokyonight"
bg = "#16161e"

[theme.chrome]
accent = "#7aa2f7"

[glyphs]                        # optional: a glyph set, with the keys of a glyph set file
id = "tokyo"
close = "x"
```

- `look = 1` is the format version. A tuios that reads an older version
  refuses a newer look and tells you to update.
- The option tables take the look keys only. `tuios look keys` lists them.
  The keys set colour, theme, glyphs, borders, gaps, titles, the dock, the
  rail, the scrollbar, selection colours and the spotlight. They also set the
  layout: `tiled` and `layout` in `[startup]`, the side and size of the
  master pane, the width of a scrolling column, and the zoom size.
- A look cannot set a key that runs a program, names a file or a folder, or
  changes a host, the daemon, keybindings, hooks or other startup keys. tuios
  leaves out such a key and names it.
- A look can set the dock lists `left`, `center` and `right` in `[dock]`, and
  the tab keys in `[dock.tabs]`. A dock list can name built-in cells only.
  tuios leaves out an item such as `custom/clock` and names it, and keeps the
  other items. A look cannot set `[dock.custom]`.
- `appearance.sidebar.sections` can name every rail section but `custom`.
  tuios leaves out `custom` and keeps the other sections. A layout with no
  section left, such as `spacer` alone, is left out. A look cannot set
  `[appearance.sidebar.custom]`.
- tuios checks each value as `tuios set-config` does. A value that is not
  correct stops the look. A string cannot hold a control character.
- `[theme]` takes the keys of a [theme file](#theme-file-format). Put the
  chrome roles in `[theme.chrome]`. `id` is required. tuios installs the theme
  as `look-<id>`, and `appearance.theme` names it. If you set
  `appearance.theme`, set it to the theme's `id`.
- `[glyphs]` takes the keys of a glyph set file
  ([GLYPHS.md](GLYPHS.md)). `id` is required. tuios installs it as
  `look-<id>`, and `appearance.glyphs` names it.
- A look file is 64 KiB at most. A theme or a glyph set in it is 32 KiB at
  most.

tuios writes the look to `config.d/look.toml` and does not change
`config.toml`. `config.toml` wins over `config.d`, so a key that
`config.toml` sets hides the same key in the look. `tuios look apply` names
those keys. When you change a look key in the settings, tuios writes the
change to `config.toml`. The next `tuios look apply` or `tuios look remove`
then keeps your change.

tuios does not replace or delete a file of your own. If `config.d/look.toml`
is a file that tuios did not write, `tuios look` stops. It also stops if a
theme or glyph set with the `look-<id>` name is there and no look installed
it. Rename that file, then apply the look again.

## Defaults for Omitted Colors

Every color field is optional. A field you leave out is filled in rather than
left unset, so a partial theme is valid. With a `base`, the base fills it.
Without one:

| Field | Fallback |
|---|---|
| `fg` | `#e5e5e5` |
| `bg` | `#000000` |
| `cursor` | the resolved `fg` |
| `black`, `red`, `green`, `yellow`, `blue`, `purple`, `cyan`, `white` | the xterm defaults (`#000000`, `#cd0000`, `#00cd00`, `#cdcd00`, `#0000ee`, `#cd00cd`, `#00cdcd`, `#e5e5e5`) |
| any `bright_*` | its non-bright counterpart |

This means a theme that defines only the eight normal colors will render with
bright text indistinguishable from normal text, which is usually not what you
want. Define the bright variants explicitly.

## Chrome Colors

The sixteen ANSI slots are the emulator's color table: they are what a program
running inside a pane paints with. They are also where TUIOS takes its own
furniture colors from, which means a palette whose accent is amber could only
get an amber logo by putting amber in `bright_blue`, recoloring every bold blue
`ls` prints.

An optional `chrome` object names those colors directly, leaving the sixteen to
the panes:

```json
{
  "id": "amber",
  "bright_blue": "#5c5cff",

  "chrome": {
    "accent":        "#ffb454",
    "accent_bright": "#ffd580",
    "success":       "#aad94c",
    "warning":       "#ffb454",
    "error":         "#ff3333",
    "info":          "#59c2ff"
  }
}
```

Every field is optional, and one you leave out derives exactly as it did before,
so a theme written without a `chrome` object renders identically:

| Field | What it colors | Derived from when absent |
|---|---|---|
| `accent` | logo, selected row, window-mode pill, info tier of the chrome | `bright_blue` |
| `accent_bright` | secondary accent, focused border in window mode | `bright_cyan` |
| `success` | terminal-mode pill, focused border in terminal mode, success notifications | `bright_green` |
| `warning` | copy-mode pill, warning notifications | `yellow` |
| `error` | error notifications, the chrome's alert ink | `red` |
| `info` | info notifications | `blue` |
| `surface` | the fill of every dialog: command palette, pickers, context menu, which-key | a constant, see below |
| `canvas` | the darkest step of the chrome's neutral ramp | derived from `surface` |
| `panel` | the outer band and the selected-row bar | derived from `surface` |
| `card` | inset chips and input fields | derived from `surface` |

A field takes any colour value from
[CONFIGURATION.md](CONFIGURATION.md#colour-values): hex, an ANSI number, a slot
name, a CharmTone name, or another role. A slot name is a colour of this
theme's own sixteen, so `"accent": "bright_magenta"` takes this file's
`bright_purple`. A role name is another field of the same object:

```json
"chrome": {
  "accent": "bright_red",
  "info":   "accent",
  "card":   "#2a2a37"
}
```

A role that the object does not set is the colour that role derives from, so
`"info": "warning"` with no `warning` field is the theme's `yellow`. Hex can be
written with or without the `#`, as before. A number with 3 or 6 digits and no
`#` is read as hex. `text`, `dim` and `muted` are not fields, because tuios
measures the text inks from the surface.

A field that is not a colour is dropped on its own and that role derives as
usual, so a typo costs one color rather than the theme. Roles that name each
other in a loop, such as `"accent": "info"` and `"info": "accent"`, have no
colour. tuios does not load that file, and `tuios list-themes` names the
roles in the loop.

### Roles for one piece of chrome

These fields colour one piece of the screen each. They sit in nested objects
by surface. Each one you leave out draws as before:

```json
"chrome": {
  "border": { "unfocused": "bright_black", "copy": "yellow", "multifocus": "magenta" },
  "rule":   "surface",
  "status": { "attention": "bright_red", "busy": "accent" },
  "dots":   { "close": "red", "minimize": "yellow", "zoom": "green" },
  "dock":   { "ground": "#181825" },
  "rail":   { "ground": "#181825" }
}
```

| Field | What it colors | When absent |
|---|---|---|
| `border.unfocused` | the border of a pane without focus | `red` |
| `border.copy` | the copy-mode pill, and the border of the focused pane in copy mode | `warning` for the pill. The border keeps the mode's colour |
| `border.multifocus` | the border of each pane that takes the keys with multifocus, and the multi copy pill | slot 3, and `border.copy` for the pill |
| `rule` | the line under the dock and the lines on the rail | measured from the ground |
| `status.attention` | an agent that waits for input, on the rail, and the border of the focused pane while its agent waits | `warning` on the rail. The border keeps the mode's colour |
| `status.busy` | a working agent on the rail, and the shimmer on its row | `info`, and `accent_bright` for the shimmer |
| `dots.close`, `dots.minimize`, `dots.zoom` | the window buttons in the dots style | red, amber and green |
| `dock.ground` | the background of the dock | none |
| `rail.ground` | the background of the rail | none |

- `appearance.dock_background` and `appearance.sidebar.background` in the
  config win over `dock.ground` and `rail.ground`, `off` included. So does an
  `appearance.background` that is a colour or `theme`. See
  [CONFIGURATION.md](CONFIGURATION.md).
- `appearance.border_unfocused_color` in the config wins over
  `border.unfocused`. `appearance.border_focused_color` wins over the focused
  border's colours by state.
- In a config colour, write a role by its full name, such as
  `fg = "status.attention"`.
- At 16 colours these fields are not used. Each piece uses its slot, as before.

With a theme, three more pieces take their colours from the roles above: the
link under the pointer uses `accent_bright`, the scrollback position on a
pane's border uses `warning`, and the leader key in the key cast uses
`accent_bright`. With no theme they keep their fixed colours.

### The surface

The dialogs are not painted from the sixteen at all. They sit on a constant
neutral ramp, the way a window manager keeps its chrome constant, so an overlay
stays legible over any terminal content. That ramp is four greys at fixed
spacing (canvas, panel, surface, card), and the spacing is what makes a panel
read as raised and a chip as inset.

On a light theme the ramp is built from the theme's background instead: the
background is the canvas, a dialog's surface sits one small step below it, the
cursor row one ramp step below that, and inset cards above it. The text tiers
are measured on it the same way. The accent and the key colour are darkened at
their own hue only as far as they need to read on it as text (4.5:1), and the
status colours as far as they need to read as marks (3:1); at 256 colours each
is the palette entry nearest its hue that does. So a dialog on a light theme is a light panel, not a dark slab. The
screen behind a modal panel fades toward the theme's background rather than
darkening, since 30% toward black turns a near-white screen a mid grey. A
theme that names its own ramp keeps it, and at 16 colours the panels paint no
ground at all, as below.

`surface` moves the whole ramp. Name it and the other three steps are derived
at the same spacing, and the three text tiers (primary, secondary, quiet) are
re-derived against it at the contrast ratios the constant palette has. The
inks are picked by measurement rather than fixed, so a light surface gets its
text in dark ink:

```json
{
  "id": "walnut",
  "chrome": {
    "accent":  "#ffb454",
    "surface": "#2b2118"
  }
}
```

`canvas`, `panel` and `card` are for a theme that wants an exact ramp rather
than a generated one. Each one you leave out is derived from `surface`, and all
three are the constants when `surface` is too. The text tiers are never named:
they are their contrast ratios, and a text color a file could set is a text
color that can be set unreadable.

A very dark or very light `surface` has less room on one side of it. The step
past black or white clamps, so a near-black surface gets a black canvas, and a
near-white one a white card. The focused border and the mode pills are accent
colors and do not follow `surface`; a light surface under an accent picked for
a dark one is the case to raise if a pill disappears.

## Colour Depth

The chrome is designed separately for each colour depth a terminal can have,
rather than drawn in truecolor and stepped down one colour at a time. tuios
reads the depth from the terminal the way every other program does, from
`TERM`, `COLORTERM` and `NO_COLOR`, and it is the same depth the frame writer
uses.

| Depth | Detected when | What the chrome does |
|---|---|---|
| truecolor | `COLORTERM=truecolor` or `24bit`, or a terminal known to have it (kitty, ghostty, wezterm, alacritty, foot) | The designed ramp, as described above. |
| 256 | `TERM` ends in `256color` and there is no `COLORTERM`: Apple Terminal, mosh, tmux without `Tc` | The neutral ramp is set by hand on the xterm grey ramp (232 to 255), so no panel or selected row turns navy. The accent and status colours step to their nearest palette entry, and every ink is measured against the grey it is actually drawn on. |
| 16 | `TERM=xterm`, `TERM=linux`, `NO_COLOR` | No grounds are painted: panels use your terminal's own background and frame themselves with a thin line. Text is your terminal's own foreground, quiet text is bright black, colours are your palette's slots (the accent is bright blue, keys bright cyan), the selected row is reverse video, a title chip is reverse video in the accent, and a workspace tab on the dock has no fill, so it drops its rounded ends. Text on a coloured chip takes the dark ink on your yellow, green and cyan slots and the light ink on the others. |

With a theme loaded, the 16-colour chrome still uses slot numbers, so it takes
its colours from whatever palette your terminal is set to; set the terminal to
the same theme to see the theme's colours. A theme's `chrome` colours are RGB
and have no slot, so at 16 colours they give way to the slots their roles
derive from.

A colour stepped down to 256 goes to the palette entry nearest it in OKLab, and
a colour with almost no chroma to the grey ramp. Saturated colours never land
on a grey: a theme's red stays red.

**The review diff.** The diff in the review overlay is designed per depth too,
so added and removed lines stay distinct without the `+` and `-` column:

| Depth | Added and removed lines | Changed words | Cursor row |
|---|---|---|---|
| truecolor | Tints of the theme's green and red mixed into the pane's ground | A stronger tint of the same colour | The accent mixed into the row |
| 256 | Fixed palette entries: 22 and 52 on a dark ground, 194 and 224 on a light one (the ones Codex uses) | One step brighter: 28 and 88 on dark, 157 and 217 on light | The line number column takes the accent; an added or removed line keeps its colour |
| 16 | No line grounds. The line numbers and the sign are green for an added line and red for a removed one | Bold and underlined | Reverse video on the line numbers only, so the code keeps its colours and marks |

Light or dark at 256 colours follows the ground the review is drawn on: the
pane background, the theme's background, or the chrome's surface.

**mosh.** mosh 1.4 and later pass 24-bit colour through, but mosh does not
forward `COLORTERM`, so tuios over mosh sees 256 colours. If your terminal has
truecolor, set it on the remote side:

```bash
export COLORTERM=truecolor
```

## Lists: Focus and Hover

Every list in tuios (the rail, the Inbox, the review overlay, settings, the
command palette and every picker) follows one rule:

- **The cell is reserved.** Whatever the cursor row shows, a mark or a bold
  name, has its cell kept on every other row, so nothing moves when the cursor
  does.
- **Only colour changes.** The cursor, the pointer and focus change grounds and
  inks, never the layout.
- **A list without the keyboard still shows its cursor**, on a quieter ground.
  In the review overlay, the file list keeps its cursor while the diff has the
  keys, and the diff keeps its cursor while the file list does.

At 16 colours the grounds become attributes: the cursor of the focused list is
reverse video, and the cursor of an unfocused list and the row under the
pointer are underlined.

## Limitations

- **Selection re-reads the directory.** Switching themes applies immediately,
  and selecting a theme id that is not yet registered re-scans the themes
  directory before failing, so "write the file, then select it" works without a
  restart. The in-app picker's list is built when it opens.
- **Flat directory.** Only `*.json` files directly under the themes directory
  are loaded; subdirectories are ignored.
- **No validation beyond parsing.** A syntactically valid file with meaningless
  colors loads happily. Use `tuios --preview-theme <id>` to check the result.
- **Border color overrides are separate.** `border_focused_color` and
  `border_unfocused_color` in `[appearance]` override the theme's border colors
  and are not part of the theme file. Both rows in the settings page open a
  colour picker, seeded on the colour the border is currently drawn in; clearing
  one there unsets the override and hands the border back to the theme.
- **The chrome's neutrals do not follow the sixteen.** Dialogs and the
  which-key popup draw on a constant grey ramp on a dark theme, and on a ramp
  built from the background on a light one, whatever the sixteen are;
  `chrome.surface` is the knob that moves it. With `theme = "none"` the ramp
  follows the terminal's background (see
  [Match Your Terminal](#match-your-terminal)).

## Related Documentation

- [GLYPHS.md](GLYPHS.md): the shape half of the same question, the characters
  the chrome is drawn with
- [CONFIGURATION.md](CONFIGURATION.md): the config file and every other option
- [CLI_REFERENCE.md](CLI_REFERENCE.md): `--theme`, `--list-themes`, `--preview-theme`
