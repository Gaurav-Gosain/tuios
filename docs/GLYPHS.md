# Glyph Sets

A theme decides what colour TUIOS's chrome is. A glyph set decides what shape it
is: which corner the border turns, what the window controls are pictures of,
what a rule and a separator are drawn with, which mark the session rail wears on
the row you are on.

Everything here is one option, `appearance.glyphs`, plus a file format for
writing your own.

## Table of Contents

- [Selecting a Set](#selecting-a-set)
- [The Built-in Sets](#the-built-in-sets)
- [The Border](#the-border)
- [Writing a Set](#writing-a-set)
- [Roles](#roles)
- [Cell Widths](#cell-widths)
- [ASCII Mode](#ascii-mode)
- [Limitations](#limitations)

## Selecting a Set

By config file:

```toml
[appearance]
glyphs = "heavy"
```

At runtime, with no restart:

```bash
tuios set-config appearance.glyphs heavy
```

In the settings page (`,` in window mode), the **Glyph set** row sits directly
under **Theme**, which is the other half of the same question. Enter on it opens
a searchable picker: each row draws that set's own corner, window controls and
rail marks, moving the selection applies the set so the chrome behind the panel
is the preview, and the two lines underneath say how many roles the set names
and which of them are not being drawn.

To see what is available and what each one draws:

```bash
tuios list-glyphs
tuios list-glyphs heavy
```

## The Built-in Sets

| Id | What it is |
|---|---|
| `default` | What TUIOS ships: rounded frame, Nerd Font powerline caps, `✕` and `□` controls |
| `unicode` | Box drawing and geometric shapes for the frame, controls and marks, with no Nerd Font private-use glyphs among them, for a good font that is not a patched one. The dock icons below are not roles and are unaffected |
| `heavy` | One stroke weight heavier throughout, border and junctions included |
| `ascii` | Nothing outside 7-bit ASCII |

They are also the sets to `inherit` from when writing your own.

## The Border

A set may carry a border and most do not. The border that draws is whichever
`appearance.border_style` names, and `glyphs` is the value meaning "the active
set's":

```toml
[appearance]
glyphs = "heavy"
border_style = "glyphs"
```

A set could have been allowed to win whenever it defines a border, which is one
fewer thing to say. It would also mean selecting a set silently turned a setting
you had already made into a no-op, with nothing on screen to say why. Naming it
keeps both settings live.

A set that names only some of the border's runes gets the rounded border for the
rest, so "square corners, everything else as usual" is four lines.

## Writing a Set

Write `<id>.json` into `~/.config/tuios/glyphs/` (the exact path is printed by
`tuios list-glyphs`). Give it `inherits` to start from a built-in:

```json
{
  "display_name": "Mine",
  "inherits": "heavy",
  "bullet": "◦",
  "focus": "▐",
  "border": {
    "top_left": "╔",
    "top_right": "╗",
    "bottom_left": "╚",
    "bottom_right": "╝"
  }
}
```

Every field is optional. An absent `id` is taken from the filename, and an
absent role keeps whatever the inherited set says, falling through in the end to
the glyph TUIOS ships. Inheritance is followed up to eight levels and a loop
stops rather than hangs.

The directory is re-read whenever a set is looked up, so a file you have just
written is selectable immediately:

```bash
tuios set-config appearance.glyphs mine
tuios list-glyphs mine
```

A file that does not parse is skipped rather than applied, and `list-glyphs`
reports it under `problems` with the reason.

## Roles

`tuios list-glyphs` prints the full list. In groups:

- **Window controls:** `close`, `maximize`, `minimize`, `dot` (the traffic-light
  disc), `pill_left`, `pill_right`
- **Rules and separators:** `rule` (the hairline, repeated), `separator` (the
  dock's gap between groups), `arrow_left`, `arrow_right` (overflow chevrons)
- **Rail marks:** `focus` ("you are here"), `attention` ("this one wants a
  human"), `bullet` (a resting row), `add`, `collapse`, `expand`
- **Scrollbar:** `scrollbar_thumb`, `scrollbar_track`
- **Text:** `ellipsis`, `sigil`, `dash_rule`
- **Border:** `border.top`, `border.bottom`, `border.left`, `border.right`, the
  four corners, and the five junctions `middle`, `middle_top`, `middle_bottom`,
  `middle_left`, `middle_right`

Roles are named for what they draw rather than where, because one role is
usually drawn in several places: `rule` is the pane's hairline and the dock's,
`bullet` is the rail's resting mark in both the full rail and the collapsed
strip.

`appearance.scrollbar.thumb` and `appearance.scrollbar.track` still exist and
still win over the set, because they are the narrower statement.

## Cell Widths

Most roles must be exactly one cell, and a glyph that misses is dropped back to
the default with a line in `problems` saying so:

```bash
tuios list-glyphs mine --json | jq -r '.problems[]?'
```

```
glyph set mine: close is 2 cells wide and the layout budgets 1, so it keeps the default
```

The reason is the window controls. Their press rectangles are fixed offsets from
the border's trailing corner, measured against buttons of exactly three and four
cells, so a two-cell emoji in `close` would not look bold: it would move every
cell after it and put the button under a different column than the one the
pointer is tested against. You name the one-cell mark and TUIOS owns the
padding.

`separator`, `ellipsis`, `collapse` and `expand` take any width: each is drawn
somewhere that measures it rather than budgeting a column for it. Border runes
must be one cell.

## ASCII Mode

`--ascii-only` (or `ascii_only` in the config) says the terminal cannot draw
more than 7-bit. It overrules a glyph set **per role** rather than throwing the
set away, so a set keeps every role it happened to spell in ASCII and gives up
only the ones it did not. A set that is ASCII throughout, like the built-in
`ascii`, loses nothing.

`tuios list-glyphs <id>` reports `ascii: true` for a set that can be drawn
anywhere.

## Limitations

- **The dock's semantic icons are not roles.** The mode chip, the window and
  workspace counts and the session controls are Nerd Font pictures of a meaning
  rather than shapes in a frame. `--ascii-only` replaces them when the font
  cannot draw them; a glyph set does not.
- **A set changes shape, not colour.** Every glyph is still drawn in the ink the
  contrast model picks for the surface it lands on, so a set cannot make chrome
  illegible. Colour is the theme's job.
- **Flat directory.** Only `*.json` files directly under the glyphs directory
  are loaded; subdirectories are ignored.
- **The hovered traffic-light symbols are fixed.** They are the pill's three
  marks again in a circled form; a set that could name them separately could
  make a hovered dot show a different control than the pill does.

## Related Documentation

- [THEMES.md](THEMES.md) - the colour half of the same question
- [CONFIGURATION.md](CONFIGURATION.md) - the config file and every other option
