# Themes

TUIOS ships a large set of built-in color themes and can load custom ones from
JSON files in your config directory. A theme supplies the 16 ANSI colors plus
foreground, background and cursor; TUIOS derives its own UI colors (borders,
overlays, the dockbar) from them.

## Table of Contents

- [Selecting a Theme](#selecting-a-theme)
- [Custom Themes](#custom-themes)
- [Theme File Format](#theme-file-format)
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

Leaving the theme unset disables theming entirely and TUIOS uses your terminal's
own colors. An unknown theme name logs a warning and leaves the colors as they
were, rather than failing to start.

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

The directory is read at startup, and read again in two cases: when a theme id
that is not registered yet is selected with `tuios set-config appearance.theme
<id>`, and on every `tuios list-themes`. So a new theme file can be
selected without a restart: write the file, then select its id. Selecting a
theme that is already registered does not re-read its file, so to see an edit
to a theme that is already loaded, save it under a new id or restart tuios.

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

## Defaults for Omitted Colors

Every color field is optional. A field you leave out is filled in rather than
left unset, so a partial theme is valid:

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

A field that is not a hex color is dropped on its own and that role derives as
usual, so a typo costs one color rather than the theme.

### The surface

The dialogs are not painted from the sixteen at all. They sit on a constant
neutral ramp, the way a window manager keeps its chrome constant, so an overlay
stays legible over any terminal content. That ramp is four greys at fixed
spacing (canvas, panel, surface, card), and the spacing is what makes a panel
read as raised and a chip as inset.

On a light theme the ramp is built from the theme's background instead: the
background is the canvas, a dialog's surface sits one small step below it, the
cursor row one ramp step below that, and inset cards above it. The text tiers
are measured on it the same way, and the accent, the key colour and the status
colours are carried toward dark ink only as far as they need to read on it
(4.5:1). So a dialog on a light theme is a light panel, not a dark slab. The
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
  `chrome.surface` is the knob that moves it.

## Related Documentation

- [GLYPHS.md](GLYPHS.md): the shape half of the same question, the characters
  the chrome is drawn with
- [CONFIGURATION.md](CONFIGURATION.md): the config file and every other option
- [CLI_REFERENCE.md](CLI_REFERENCE.md): `--theme`, `--list-themes`, `--preview-theme`
