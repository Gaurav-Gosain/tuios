# Layout Modes and Window Navigation

Tiling in TUIOS has three layout modes, and there are two navigation features
that are easy to miss because they have no default keybinding: the aggregate
view and multifocus. This document covers all of them.

> **Note:** `Ctrl+B` is the default leader key. `Ctrl+P` opens the command
> palette, which is how most of the commands here are reached.

## Table of Contents

- [The Three Layout Modes](#the-three-layout-modes)
- [Master-Stack Layout](#master-stack-layout)
- [Scrolling Layout](#scrolling-layout)
- [Aggregate View](#aggregate-view)
- [Multifocus](#multifocus)
  - [Multi copy mode](#multi-copy-mode)

## The Three Layout Modes

Tiling is toggled on and off with `Ctrl+B` `Space`. Which layout it uses when it
is on is a separate choice, made from the command palette:

| Palette command | Mode |
|---|---|
| Layout: BSP Tiling | Binary space partitioning, the default. See [BSP_TILING.md](BSP_TILING.md) |
| Layout: Master-Stack | Master panes on one side, the other panes in a stack beside them. See [Master-Stack Layout](#master-stack-layout) |
| Layout: Scrolling (niri-style) | An infinite horizontal strip of columns, described below |
| Layout: Disable Tiling | Turns tiling off; windows float freely |

Choosing a mode turns tiling on if it was off. The mode is per-session and is
carried in daemon session state, so a scrolling session comes back as a
scrolling session on reattach, and turning tiling off does not forget it. The
layout data itself (the BSP tree, the column strip) is per workspace: each of
the nine workspaces keeps its own.

### Settings

These settings shape the tiling. All of them are in the settings page
(`Ctrl+B ,`) as well as in `config.toml`:

| Setting | What it does |
|---|---|
| `startup.layout` | The mode a **new** session starts in: `bsp`, `master-stack` or `scrolling`. A session that already exists keeps its own. |
| `appearance.master_ratio` | The master pane's share of the screen in master-stack, as a percent (10-90). The `<` and `>` keys, the percentage resizes and a mouse drag on the divider move it for the workspace you are on, and every client attached to the session follows. A workspace nobody has moved it on starts at this setting. |
| `appearance.master_position` | The side the master panes take in master-stack: `left`, `right`, `top`, `bottom` or `center`. The default is `left`. See [Master-Stack Layout](#master-stack-layout). |
| `appearance.master_count` | How many panes are master panes in master-stack, 1-9. The default is 1. |
| `appearance.master_grid` | With one master on the left, show four or more panes as a grid. The default is `true`. Set `false` to keep the master and the stack at every pane count. |
| `appearance.scroll_column_width` | A column's width in the scrolling layout, as a percent of the screen (20-90). |
| `appearance.gap` | Cells of empty ground between neighbouring panes, in every mode. |

`appearance.gap`, `appearance.master_ratio`, `appearance.scroll_column_width`
and `appearance.shared_borders` decide how many cells a pane gets, so they are
settled across a session rather than kept per client: change one on any client
and every client attached to that session follows. The purely visual settings
(theme, border style, glyphs, title position, dimming) stay per client.

### Resizing a pane by percentage

The focused pane can be sized to a percentage of the content region (width) or
of the usable height (height) with the `resize_width_N` and `resize_height_N`
actions, `N` in 10..90. Under the layout prefix (`Ctrl+B L`), the digits `5`
through `9` set the width to 50-90% and `Shift+5` through `Shift+9` the height;
every percentage is a named keybind, so any of them can be rebound or removed in
`[keybindings.layout_prefix]`. The layout still applies its own constraints
(minimum pane size, gaps, and the pane's neighbours), so the resulting size is
the requested percentage wherever the layout allows it.

How a resize is kept depends on the layout:

- **BSP** writes the resize into the split ratios of the tree.
- **Master-stack** writes it into the workspace's splits: the master ratio
  (the width of the master column, or the height of the master row when the
  master is at the top or the bottom) and the share of every other pane: each
  master, each stack pane, and each row and pane of the default grid (four or
  more panes, one master on the left). This works on every side the master can
  take. The keyboard resizes, the percentage resizes and a mouse drag on a
  divider all do this, so the resize survives a retile, and a terminal resize
  is a retile. With the master in the center, a resize moves one divider, and
  the next retile puts the master back in the middle at its new width. The
  splits are session state, so every client attached to the session lays the
  workspace out the same way. When a pane opens or closes, the part of the
  layout it changes goes back to equal shares. Equalize splits (`Ctrl+B =`) puts the
  master back at the configured ratio and gives every other pane an equal
  share.
- **Scrolling layout: width only.** The width actions reach the focused
  column through the scrolling column resizer, which clamps to the column
  width range; the height actions have no scrolling branch, so
  `Shift+5`..`Shift+9` do nothing there (column heights are recomputed as
  equal spans on the next layout pass).

## Master-Stack Layout

The master-stack layout puts the first panes of a workspace in a master area
and the other panes in a stack beside it. The master area is for the pane you
work in most, such as an editor. The layout is close to dwm's tile layout and
to Hyprland's master layout.

### Where the masters go

`appearance.master_position` sets the side:

| Value | Layout |
|---|---|
| `left` | The masters are a column on the left. The stack is a column on the right. This is the default. |
| `right` | The masters are on the right. The stack is on the left. |
| `top` | The masters are a row at the top. The stack is a row under it. |
| `bottom` | The masters are a row at the bottom. The stack is a row above it. |
| `center` | The masters are a column in the middle. The stack panes go to the right and the left in turn. |

With `center`, the first stack pane goes to the right, the next to the left,
and so on. So three panes are three columns with the master in the middle, and
five panes put two panes on each side. When there is only one stack pane, the
master goes to the left and the stack pane takes the right. The layout does
not keep an empty column on one side.

Two panes on a screen that is taller than it is wide stack one above the
other. With `left` the master is at the top. With `right` it is at the bottom.

### More than one master

`appearance.master_count` sets how many panes are masters. The masters share
the master area in equal parts. When every pane is a master, the panes share
the screen in equal parts.

### The grid

With one master on the left, four or more panes show as an equal grid. This is
the layout from before the other sides existed, and it stays the default so
your layout does not change on an upgrade. Set `appearance.master_grid = false`
to keep the master and the stack at every pane count. Any other side, or more
than one master, always keeps the master.

### Change the layout while you work

Each workspace has its own master side and master count. The settings above
are the values a workspace starts with. The keys and commands below change the
workspace on screen, and the workspace keeps the change. In a daemon session
the change goes to the daemon, so every attached client shows the same layout
and the session keeps it after a detach.

| Keys | Action | What it does |
|---|---|---|
| `Ctrl+B L o` | `cycle_master_position` | Move the masters to the next side: left, right, top, bottom, center |
| `Ctrl+B L Enter` | `swap_with_master` | Swap the focused pane with the master. On the master, swap with the first stack pane |
| `Ctrl+B L m` | `focus_master` | Focus the master |
| `Ctrl+B L i` | `add_master` | Make one more pane a master |
| `Ctrl+B L d` | `remove_master` | Make one pane fewer a master |

`set_master_position_left`, `_right`, `_top`, `_bottom` and `_center` set one
side directly. They have no default key. The command palette has an entry for
each.

From a shell:

```sh
tuios set-layout --master-position center
tuios set-layout --masters 2
tuios run-command SwapWithMaster
```

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
| `h` / `l` (window mode) | Focus the column left/right |
| `j` / `k` (window mode), `Alt+Down` / `Alt+Up` (terminal mode) | Focus the window below/above in a column of stacked windows |
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

`niri_reverse_scroll = true` in `[appearance]` inverts the wheel direction, on
whichever axis the gesture arrives on.

The wheel only moves the viewport while `Alt` or `Shift` is held, horizontal
wheel included. A trackpad reports a little sideways drift on almost every
vertical scroll and the terminal forwards that as a horizontal wheel button, so
answering it on its own walked the strip sideways whenever someone scrolled back
through a pane. Unmodified, the wheel belongs to the pane under the pointer.

The drift does not go away once the modifier is held, so the strip follows the
axis the gesture is mostly on and drops the events on the other one. A quarter
second of stillness starts a new gesture. This is what a macOS mouse needs too,
from the other side: the window server swaps the axes for a wheel with shift
held, so a mouse gesture arrives entirely horizontal while a trackpad gesture
arrives mostly vertical.

`appearance.niri_scroll_cells` is how far one wheel event walks the strip, in
cells (1-200, default 8). It is a flat count rather than a share of the screen
because a terminal reports a trackpad as one wheel event per cell the fingers
cross: a flick and its momentum tail are tens of events, and a fifth of the
screen each sent the strip to its end before the fingers had left the glass.
Raise it if you only ever scroll the strip with a wheel.

### Column commands

These have no default keybinding. Run them from the command palette, or bind the
action names yourself in `[keybindings]`:

| Palette command | Action name | What it does |
|---|---|---|
| Scroll: Cycle Column Width | `scroll_cycle_width` | Cycles the focused column through 33%, 50%, 55%, 67% and 90% (and 100% when `appearance.scroll_column_max` is 100) of the screen width |
| Scroll: Maximize Column Width | `scroll_maximize` | Widens the focused column to the configured ceiling, the widest the strip will give it |
| Scroll: Stack Window Below (consume) | `scroll_consume` | Pulls the window from the next column into the focused column, stacking it below |
| Scroll: Split to New Column (expel) | `scroll_expel` | Pushes the bottom window of the focused column out into its own new column |
| (none) | `scroll_focus_left`, `scroll_focus_right` | Focus the column left/right |
| (none) | `scroll_move_left`, `scroll_move_right` | Move the focused column left/right |

A column's width is a proportion of the screen until you resize it with `<` or
`>`, which pins it to a fixed cell count; cycling the width with
`scroll_cycle_width` unpins it again. Each press of `<` or `>` changes the width
by four cells, within a floor of 20 cells and a ceiling set by
`appearance.scroll_column_max` (90% by default, up to 100%).

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

Open it from the command palette: `Ctrl+P`, then "All windows".
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
works. The directory is read from the shell's process on Linux (procfs) and
macOS (libproc); on other platforms it is empty.

Limitations: minimized and floating windows are included and marked rather than
filtered out.

## Multifocus

Multifocus broadcasts your typing to several windows at once, the way tmux's
synchronize-panes does. It is useful for running the same command on several
hosts.

| Input | Action |
|---|---|
| `Ctrl+Shift+Click` on a window | Add or remove that window from the multifocus set |
| Palette: "Toggle multifocus" or action `toggle_multifocus_active` | Add or remove the currently focused window |
| Palette: "Toggle multifocus on all panes" or action `toggle_multifocus_all` | Add every visible window on the workspace. When all of them are in the set, remove them from the set. Windows on other workspaces stay in the set |
| Palette: "Clear multifocus" | Empty the set |

The two actions have no default key. To bind them, add them to a
`[keybindings]` section. For example, this puts them on `Ctrl+B y` and
`Ctrl+B Y`:

```toml
[keybindings.prefix_mode]
toggle_multifocus_active = ["y"]
toggle_multifocus_all = ["Y"]
```

To open many panes with multifocus on in one step, use
[`tuios xpanes`](CLI_REFERENCE.md#tuios-xpanes). It opens one pane per item on
a new workspace, tiled, and puts all of them in the set:

```bash
tuios xpanes --ssh web1 web2 web3
```

From a script, `tuios run-command SetMultifocus <window>...` sets the set to
exactly these windows. With no window, it empties the set.

Windows in the set are drawn with a distinct border color so it is obvious which
ones will receive your keystrokes. With `appearance.dim_unfocused` on, the
windows in the set are not dimmed. Set `appearance.dim_multifocus = true` to
dim them like the other windows. A notification reports the size of the set as
you change it.

While the set is non-empty and you are in **terminal mode**, every keystroke that
would go to the focused window's shell is also sent to each window in the set.
A paste goes to each window in the set too. Each window gets the paste as a
bracketed paste only when the program in that window turned bracketed paste on.
A paste into a scrolled window returns it to live output first, as a typed key
does. In copy mode TUIOS drops the paste.
Keys handled by TUIOS itself (the leader key and its chords, overlays, workspace
switches, copy mode) are not broadcast, because they never reach the forwarding
path.

Limitations:

- **Terminal mode only.** In window management mode nothing is broadcast.
- **Only the panes on screen.** A key goes only to the panes of the set that
  are on the current workspace and are not minimized. The other panes stay in
  the set and get keys again when they are on screen.
- **The set is client-side.** It is not part of session state, so it does not
  survive a detach and it is not shared with other clients attached to the same
  session. Switching sessions clears it.
- **It follows windows, not positions.** The set is keyed by window ID, so
  swapping panes around keeps the same windows selected. Closing a window
  removes it from the set.
- **No popups.** A popup cannot join the set.
- **No default key.** Bind `toggle_multifocus_active` and
  `toggle_multifocus_all` yourself, or use `Ctrl+Shift+Click` or the palette.

### Multi copy mode

Multi copy mode is copy mode on every pane of the multifocus set at the same
time. Use it to search the output of many panes once and copy the same part
from each pane. Each pane has its own cursor and its own selection. Each key
moves all of them together.

To start it, add two or more panes to the multifocus set and focus one of them.
Then press `Ctrl+B [`, the copy mode key. When the focused pane is in the set,
this key starts multi copy mode. When the focused pane is not in the set, or
the set is empty, the key starts copy mode on one pane as before. When
multifocus is on, the palette entry "Multi copy mode" also starts it, and the `Ctrl+B` menu shows
"Multi copy (N)" on the `[` line.

The dock shows `MULTI 3`, where 3 is the number of panes. After a search, it
shows `MULTI 2/3`: the search found text in 2 of the 3 panes.

| Key | Action |
|---|---|
| `h j k l`, `w b e`, `g G`, and the other copy mode motions | Move the cursor in every pane |
| `/` or `?`, then text, then `Enter` | Search every pane. Each pane goes to its own match |
| `n` / `N` | Go to the next or previous match in each pane |
| `v` / `V` | Start a character or line selection in every pane that has a match |
| `Esc` | End the selection. A second `Esc`, or `q`, leaves copy mode in every pane |
| `y` | Copy the selection of each pane to the clipboard, as one block |
| `Y` | Save the selection of each pane to a file |
| `Tab` | Change the format: plain, markdown, json |
| `Shift+Up` / `Shift+Down` | Scroll every pane one line |

A pane where the search finds no text loses the multifocus border color. With
a theme, the pane is also dimmed. It keeps its cursor. It does not take motions or selections until
a later search finds text in it. `Ctrl+L` clears the search.

`y` copies only the text that is selected in each pane. A pane without a
selection adds nothing. The panes are in window order.

The formats:

- **plain**: The selected lines of each pane, one pane after the other. Each
  pane ends with a newline. Nothing is added. Use it when each line already
  names its host.
- **markdown**: For each pane, a `## Pane N: title` heading and the lines in a
  fenced code block.
- **json**: An array with one object for each pane:
  `{"pane": N, "window_id": "...", "title": "...", "lines": ["..."]}`. The pane
  number is the index that `tuios list-windows --json` shows.

The format starts as the value of `appearance.selection.multi_format` (default
`plain`). `Tab` changes it until you leave multi copy mode.

`appearance.selection.copy_command` and a `[[keybindings.copy_pipe]]` key
also work in multi copy mode. The command gets the text that `y` copies, in
the current format, one time for all panes. See
[Pipe a yank through a command](KEYBINDINGS.md#pipe-a-yank-through-a-command).

`Y` opens a prompt at the bottom of the focused pane. The default path is
`~/tuios-copy-<date>-<time>.<ext>`, with `txt`, `md` or `json` for the format.
Type or paste a path. Press `Enter` to save, or press `Esc` to cancel.

- A relative path starts in the directory of the focused pane, when its shell
  reports one. Otherwise it starts in your home directory. The line above the
  prompt shows the full path.
- tuios does not write over a file that exists. It tells you when the folder
  does not exist or when the path is a folder.
- The dock shows the path of the file it wrote. In the browser client and over
  SSH, the file is on the machine that runs tuios, and the dock names that
  machine.

A pane title can come from a remote shell. In the markdown and json formats,
tuios removes control, bidirectional and zero-width characters from the title
and limits it to 200 characters.

Limits:

- Soft-wrapped lines are joined by a width estimate, the same as copy mode on
  one pane.
- Multi copy mode ends when focus moves to a pane that is not in it.
- A pane that you minimize or move to another workspace leaves multi copy mode.
- The clipboard gets the copy through OSC 52. Some terminals cut a long OSC 52
  copy. Above 100 KB, the dock tells you. Use `Y` to save a large copy to a
  file.

## Related Documentation

- [BSP_TILING.md](BSP_TILING.md): the BSP layout in detail
- [KEYBINDINGS.md](KEYBINDINGS.md): default keybindings
- [CONFIGURATION.md](CONFIGURATION.md): binding your own keys to action names
