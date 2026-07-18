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

In the running app, the command palette (`Ctrl+P`) has a **Theme Picker** entry,
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

Themes are read once, at startup. Adding or editing a theme file requires a
restart.

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

## Limitations

- **Startup only.** New or edited theme files are picked up on the next launch,
  not live. Switching between already-registered themes from the picker or the
  settings page does apply immediately.
- **Flat directory.** Only `*.json` files directly under the themes directory
  are loaded; subdirectories are ignored.
- **No validation beyond parsing.** A syntactically valid file with meaningless
  colors loads happily. Use `tuios --preview-theme <id>` to check the result.
- **Border color overrides are separate.** `border_focused_color` and
  `border_unfocused_color` in `[appearance]` override the theme's border colors
  and are not part of the theme file.
- **Some overlays are not themed.** The which-key popup, in particular, draws
  with fixed colors regardless of the active theme.

## Related Documentation

- [CONFIGURATION.md](CONFIGURATION.md) - the config file and every other option
- [CLI_REFERENCE.md](CLI_REFERENCE.md) - `--theme`, `--list-themes`, `--preview-theme`
