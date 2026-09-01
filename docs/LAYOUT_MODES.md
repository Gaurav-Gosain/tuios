# Layout Modes and Window Navigation

Tiling in TUIOS has three layout modes, and there are two navigation features
that are easy to miss because they have no default keybinding: the aggregate
view and multifocus. This document covers all of them.

> **Note:** `Ctrl+B` is the default leader key. `Ctrl+P` opens the command
> palette, which is how most of the commands here are reached.

## Table of Contents

- [The Three Layout Modes](#the-three-layout-modes)
- [Scrolling Layout](#scrolling-layout)
- [Aggregate View](#aggregate-view)
- [Multifocus](#multifocus)

## The Three Layout Modes

Tiling is toggled on and off with `Ctrl+B` `Space`. Which layout it uses when it
is on is a separate choice, made from the command palette:

| Palette command | Mode |
|---|---|
| Layout: BSP Tiling | Binary space partitioning, the default. See [BSP_TILING.md](BSP_TILING.md) |
| Layout: Master-Stack | One master pane on the left, the rest stacked on the right |
| Layout: Scrolling (niri-style) | An infinite horizontal strip of columns, described below |
| Layout: Disable Tiling | Turns tiling off; windows float freely |

Choosing a mode turns tiling on if it was off. The mode is per-session and is
carried in daemon session state, so a scrolling session comes back as a
scrolling session on reattach, and turning tiling off does not forget it. The
layout data itself (the BSP tree, the column strip) is per workspace: each of
the nine workspaces keeps its own.

### Settings

Four settings shape the tiling, and all four are in the settings page
(`Ctrl+B ,`) as well as in `config.toml`:

| Setting | What it does |
|---|---|
| `startup.layout` | The mode a **new** session starts in: `bsp`, `master-stack` or `scrolling`. A session that already exists keeps its own. |
| `appearance.master_ratio` | The master pane's share of the screen in master-stack, as a percent (30-70). The `<` and `>` keys move it for the workspace you are on, and every client attached to the session follows. A workspace nobody has moved it on starts at this setting. |
| `appearance.scroll_column_width` | A column's width in the scrolling layout, as a percent of the screen (20-90). |
| `appearance.gap` | Cells of empty ground between neighbouring panes, in every mode. |

`appearance.gap`, `appearance.master_ratio`, `appearance.scroll_column_width`
and `appearance.shared_borders` decide how many cells a pane gets, so they are
settled across a session rather than kept per client: change one on any client
and every client attached to that session follows. The purely visual settings -
theme, border style, glyphs, title position, dimming - stay per client.

### Resizing a pane by percentage

The focused pane can be sized to a percentage of the content region (width) or
of the usable height (height) with the `resize_width_N` and `resize_height_N`
actions, `N` in 10..90. Under the layout prefix (`Ctrl+B L`), the digits `5`
through `9` set the width to 50-90% and `Shift+5` through `Shift+9` the height;
every percentage is a named keybind, so any of them can be rebound or removed in
`[keybindings.layout_prefix]`. The layout still applies its own constraints -
minimum pane size, gaps, and the pane's neighbours - so the resulting size is
the requested percentage wherever the layout allows it.

Two layout caveats, documented rather than hidden:

- **Scrolling layout: width only.** The width actions reach the focused
  column through the scrolling column resizer, which clamps to the column
  width range; the height actions have no scrolling branch, so
  `Shift+5`..`Shift+9` do nothing there (column heights are recomputed as
  equal spans on the next layout pass).
- **Master-stack: not durable.** In master-stack the rectangles are
  recomputed from `appearance.master_ratio` on every retile, so a percentage
  resize is dropped the next time the layout is recomputed. This is older
  than the percentage feature itself; BSP (the default) keeps the split
  ratio, so the percentage survives there.

## Scrolling Layout

The scrolling layout is modeled on the niri window manager. Windows are arranged
as **columns** on a strip that is wider than the screen, and the screen is a
viewport onto that strip. A column holds one window by default and can hold
several stacked vertically. Instead of shrinking every pane to make room for a
new one, a new column is inserted after the focused one and the viewport scrolls.

New columns are inserted immediately to the right of the focused column, not at
the end of the strip. Closing the last window in a column removes the column and
moves focus to its left.

### Navigating

| Input | Action |
|---|---|
| `Alt+Left` / `Alt+Right` (terminal mode) | Focus the column left/right |
| `Alt+P` / `Alt+N` | Focus the column left/right (these cycle windows in the other layout modes) |
| `Opt+Shift+Tab` / `Opt+Tab` (macOS) | Focus the column left/right |
| `Alt+Wheel` or `Shift+Wheel` | Scroll the viewport horizontally, one fifth of a screen per notch |
| `Alt` or `Shift` + horizontal wheel (if your terminal sends it) | Scroll the viewport |
| `H` / `L` or `Ctrl+Left` / `Ctrl+Right` (window mode) | Move the focused column left/right along the strip |
| `<` and `>` (window mode) | Shrink and grow the focused column |

Keyboard navigation scrolls the focused column into view, centered, so the
neighboring columns peek in at the edges. Clicking a partially visible column
does not recenter, on the reasoning that a column you can already see and click
does not need the viewport to jump.

Where the strip is scrolled to is part of the session, not of one window onto
it. Every client attached to the session shares the offset, the way they share
the focus and the workspace: move the focus or turn the wheel on one, and the
others follow to the same place. That works because the panes' box is settled
across the session, so one offset shows every client the same columns whatever
size their terminals are.

`niri_reverse_scroll = true` in `[appearance]` inverts the wheel direction.

The wheel only moves the viewport while `Alt` or `Shift` is held, horizontal
wheel included. A trackpad reports a little sideways drift on almost every
vertical scroll and the terminal forwards that as a horizontal wheel button, so
answering it on its own walked the strip sideways whenever someone scrolled back
through a pane. Unmodified, the wheel belongs to the pane under the pointer.

### Column commands

These have no default keybinding. Run them from the command palette, or bind the
action names yourself in `[keybindings]`:

| Palette command | Action name | What it does |
|---|---|---|
| Scroll: Cycle Column Width | `scroll_cycle_width` | Cycles the focused column through 33%, 50%, 55%, 67% and 90% of the screen width |
| Scroll: Stack Window Below (consume) | `scroll_consume` | Pulls the window from the next column into the focused column, stacking it below |
| Scroll: Split to New Column (expel) | `scroll_expel` | Pushes the bottom window of the focused column out into its own new column |
| (none) | `scroll_focus_left`, `scroll_focus_right` | Focus the column left/right |
| (none) | `scroll_move_left`, `scroll_move_right` | Move the focused column left/right |

A column's width is a proportion of the screen until you resize it with `<` or
`>`, which pins it to a fixed cell count; cycling the width with
`scroll_cycle_width` unpins it again. Each press of `<` or `>` changes the width
by four cells, within a floor of 20 cells and a ceiling of 90% of the screen.

Windows stacked in one column split its height evenly, less `appearance.gap`
between them.

A new column is `appearance.scroll_column_width` percent of the screen wide.
The default of 55% is deliberately over half, so two columns never quite fit
side by side and the strip reads as something you scroll.

### Limitations

- **Shared borders are not drawn in scrolling mode.** `shared_borders` applies
  to BSP and master-stack tiling only; scrolling columns always draw their own
  borders.
- **Column widths and the strip order are not shared or saved.** The layout mode
  and the scroll offset are session state; the column arrangement is not, and is
  rebuilt from the window list on reattach and on each client.
- **Transitions always animate.** The viewport slide is kept even when
  animations are disabled, because the jump is disorienting without it.

## Aggregate View

The aggregate view is a searchable list of **every window across every
workspace**, with a short preview of each window's content. It is the fastest
way to find a pane when you have windows spread over several workspaces.

Open it from the command palette: `Ctrl+P`, then "Aggregate View (All Windows)".
It has no default keybinding.

| Key | Action |
|---|---|
| Type | Fuzzy-filter by title, workspace number or preview text |
| `Up` / `Down`, `Ctrl+P` / `Ctrl+N` | Move the selection |
| `Enter` | Jump to the selected window |
| `Backspace` | Delete a character from the query |
| `Ctrl+U` | Clear the query |
| `Esc`, `Ctrl+C` | Close |

Jumping switches to the window's workspace, restores it if it was minimized, and
focuses it.

The preview is the first three non-empty lines of the window's current screen,
joined with ` | ` and truncated to 80 characters. It is a snapshot taken when the
list is built, not a live view.

Each entry carries the window's working directory, so searching by directory
works. The directory is read from the shell's process on Linux only; on other
platforms it is empty.

Limitations: minimized and floating windows are included and marked rather than
filtered out.

## Multifocus

Multifocus broadcasts your typing to several windows at once, the way tmux's
synchronize-panes does. It is useful for running the same command on several
hosts.

| Input | Action |
|---|---|
| `Ctrl+Shift+Click` on a window | Add or remove that window from the multifocus set |
| Palette: "Toggle Multifocus" | Add or remove the currently focused window |
| Palette: "Clear Multifocus" | Empty the set |

Windows in the set are drawn with a distinct border color so it is obvious which
ones will receive your keystrokes. A notification reports the size of the set as
you change it.

While the set is non-empty and you are in **terminal mode**, every keystroke that
would go to the focused window's shell is also sent to each window in the set.
Keys handled by TUIOS itself (the leader key and its chords, overlays, workspace
switches, copy mode) are not broadcast, because they never reach the forwarding
path.

Limitations:

- **Terminal mode only.** In window management mode nothing is broadcast.
- **The set is client-side.** It is not part of session state, so it does not
  survive a detach and it is not shared with other clients attached to the same
  session. Switching sessions clears it.
- **It follows windows, not positions.** The set is keyed by window ID, so
  swapping panes around keeps the same windows selected. Closing a window
  removes it from the set.
- **No key.** There is no default keybinding for either palette command; use
  `Ctrl+Shift+Click` or the palette.

## Related Documentation

- [BSP_TILING.md](BSP_TILING.md) - the BSP layout in detail
- [KEYBINDINGS.md](KEYBINDINGS.md) - default keybindings
- [CONFIGURATION.md](CONFIGURATION.md) - binding your own keys to action names
