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
scrolling session on reattach. The layout data itself (the BSP tree, the column
strip) is per workspace: each of the nine workspaces keeps its own.

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
| Horizontal wheel (if your terminal sends it) | Scroll the viewport |
| `H` / `L` or `Ctrl+Left` / `Ctrl+Right` (window mode) | Move the focused column left/right along the strip |
| `<` and `>` (window mode) | Shrink and grow the focused column |

Keyboard navigation scrolls the focused column into view, centered, so the
neighboring columns peek in at the edges. Clicking a partially visible column
does not recenter, on the reasoning that a column you can already see and click
does not need the viewport to jump.

`niri_reverse_scroll = true` in `[appearance]` inverts the wheel direction.

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

Windows stacked in one column split its height evenly.

### Limitations

- **Shared borders are not drawn in scrolling mode.** `shared_borders` applies
  to BSP tiling only; scrolling columns always draw their own borders.
- **There is no gap between columns.** The column gap exists in the layout code
  but is always zero, and the `gap` appearance option does not feed it.
- **Column widths and the strip order are not saved.** The layout mode survives
  a detach, but the column arrangement is rebuilt from the window list on
  reattach.
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

Limitations: the list has no per-window working directory (the field exists but
is always empty, so searching by directory does not work), and minimized and
floating windows are included and marked rather than filtered out.

## Multifocus

Multifocus broadcasts your typing to several windows at once, the way tmux's
synchronize-panes does. It is useful for running the same command on several
hosts.

| Input | Action |
|---|---|
| `Ctrl+Click` on a window | Add or remove that window from the multifocus set |
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
  `Ctrl+Click` or the palette.

## Related Documentation

- [BSP_TILING.md](BSP_TILING.md) - the BSP layout in detail
- [KEYBINDINGS.md](KEYBINDINGS.md) - default keybindings
- [CONFIGURATION.md](CONFIGURATION.md) - binding your own keys to action names
