# Rendering audit: crispness, smoothness, memory

Audit of `ba69b2c` on 2026-10-07. Every number here was measured on this
machine against a private demo daemon (`scripts/demo/seed.sh`). The findings
are ranked by impact inside each group. Each finding has a concrete fix.

## How it was measured

- Machine: i7-10700, Intel UHD 630 and RTX 3070 (driver 615.71), Linux 7.2.
- Compositor: `gamescope --backend headless -W 1440 -H 900 -r 60
  --expose-wayland`. The nested Hyprland from `scripts/screenshot.sh` sends
  no frame callbacks while its host window is not on screen, so it drew 0
  frames in 5 s. It was used only for screenshots, where `grim` forces a
  frame. Frame intervals are therefore capped at 60 Hz.
- Window 1440x900, scale 1, JetBrainsMono Nerd Font Mono at 14 px, cell 8x18.
- Load: `busy4` and `busy12` are sessions with 4 or 12 panes that each print
  about 200 coloured log lines a second (timestamps, counters, code words).
  `hist` is one pane with 6,000 lines of history, scrolled by wheel events
  of 40 px every 16 ms through the control socket.
- CPU and memory: `/proc/PID/stat` and `smaps_rollup` over 15 to 30 s, taken
  from the unmodified release build.
- Frame, allocation and shaping numbers: the same build with a counting
  global allocator and timers around `paint_grid`, `render`, `prepare` and
  the shape cache, read through a `stats` control command. The atlas numbers
  come from a patched `gpui-pre-wgpu` that logs each atlas texture and tile.
  None of this instrumentation is in the repository.

## Baseline

| Scenario | App CPU (one core) | Poll children CPU | Bridge CPU | RSS | PSS | Anonymous |
| --- | --- | --- | --- | --- | --- | --- |
| Idle, no agent working | 0.9 % | 0.9 % | 0.2 % | 191 MB | 115 MB | 37 MB |
| Idle, 2 agents working (spinner) | 2.9 % | 0.7 % | 0.2 % | 190 MB | 95 MB | 37 MB |
| 4 busy panes | 29.1 % | 0.9 % | 7.5 % | 206 MB | 131 MB | 51 MB |
| 12 busy panes | 35.8 % | 0.8 % | 15.3 % | 217 MB | 141 MB | 61 MB |

With 4 busy panes the anonymous memory grows to 59 MB after 60 s and then
stays flat, when every pane's 10,000-line history is full.

| Scenario | Frames/s | Frame interval p50 / p95 | `paint_grid` p50 / p95 | prepare p50 / p95 | Allocations per frame | Bytes allocated per frame |
| --- | --- | --- | --- | --- | --- | --- |
| Idle, no agent working | 1.6 | 626 / - ms | - | - | 5,400 | 2.6 MB |
| Idle, spinner | 10 | 100 / 103 ms | 0.40 / 0.67 ms | 0.00 / 0.05 ms | 3,450 | 2.3 MB |
| 4 busy panes | 60 | 16.7 / 18.1 ms | 1.59 / 2.51 ms | 0.70 / 1.11 ms | 6,000 | 3.1 MB |
| 12 busy panes | 60 | 16.7 / 18.1 ms | 1.70 / 2.88 ms | 0.71 / 1.16 ms | 8,000 | 4.3 MB |
| Smooth scroll, 40 px steps | 60 | 16.7 / 18.0 ms | 1.23 / 2.02 ms | 0.37 / 0.60 ms | 4,700 | 2.8 MB |

The app holds 60 Hz in every case, so smoothness is not the problem today.
The cost is CPU, allocation churn and wake-ups, which become dropped frames
on a 240 Hz display, a laptop, or with more panes.

- Startup: 0.35 s to the first frame and 0.37 s to pane content. The GPU
  device takes 230 to 270 ms of that (from 25 ms to about 300 ms). Bridge
  attach takes 54 ms. With only the Intel Vulkan driver the whole start is
  0.15 s.
- Atlas: 2 textures of 1024x1024, a monochrome R8 (1 MB) and a subpixel
  BGRA (4 MB). 420 tiles at startup, 593 after the palette opens, 718 after
  one font size change. That is 54,000 px², 5 % of the textures. Only 41 to
  54 distinct terminal glyph keys are used at scale 1. Tiles are never
  evicted, so each font size adds a full set. Churn under load is near zero.
- Text shaping: a word that misses the shape cache costs 15 to 20 µs. With 4
  busy panes there are about 850 misses a second (17 ms of CPU a second),
  nearly all unique tokens such as timestamps and counters. UI text costs
  about 1 µs per call through GPUI's frame cache, about 12 calls per frame.
- Profile with 4 busy panes (perf, 8 s): taffy flexbox layout 11.5 %,
  memcpy 9.5 %, GPU driver 9.9 %, tuios-gpui's own code 6 %, GPUI scene
  5.3 %, sorting 5 %, malloc and free 5.1 %, ghostty 5.1 %, shaping 3.2 %.

## Crispness

### 1. Subpixel antialiasing on Wayland gives coloured fringes

GPUI's Linux text system asks for `TextRenderingMode::Subpixel` whenever the
GPU supports dual-source blending. Wayland does not report the subpixel
layout, so GPUI assumes RGB stripes. White UI text in the sidebar had 461
pixels with visible colour fringes in a 210x20 sample, and 0 in grayscale.
On a BGR, rotated, OLED or scaled screen the fringes are wrong, not only
visible. Subpixel glyphs also take 4 bytes per pixel in the atlas.

Grayscale alone looks thinner than subpixel. Grayscale with GPUI's
`grayscale_enhanced_contrast` at 2.0 (default 1.0) gives solid stems without
fringes. See `docs/perf/text-antialiasing.png`, top to bottom: subpixel
(today), grayscale, grayscale with contrast 2.0, the same with gamma 1.4.

Fix:

- Call `cx.set_text_rendering_mode(TextRenderingMode::Grayscale)` at
  startup. Add a `text_rendering = "grayscale" | "subpixel"` setting.
- Set `ZED_FONTS_GRAYSCALE_ENHANCED_CONTRAST=2.0` in `main` before GPUI
  starts, unless the user set it. GPUI reads it once when it creates the
  renderer. Expose it as `font_contrast`. Remove it from the environment the
  bridge gets, so programs in panes do not inherit it.

### 2. Fractional scale: the grid stays on the old scale and is not snapped

At scale 1 every glyph sits on a whole device pixel (0 of 2.6 million glyphs
were fractional). At other scales:

| Scale | Glyphs at a fractional device position | Distinct glyph keys |
| --- | --- | --- |
| 1 | 0 % | 41 to 54 |
| 1.25 | 46 % | 110 |
| 1.8 | 98.5 % | 323 |

There are two causes:

- `Metrics` is built once (`ensure_metrics`) and only rebuilt on a font size
  change. A window that opens at scale 1 and then gets scale 1.8 keeps a
  cell of 8 logical px, which is 14.4 device px. Each column then lands on a
  different subpixel phase. Glyphs blur, the atlas holds up to 4 copies of
  each glyph, and background spans get seams. The control dump showed cell
  `[8, 18]` at scale 1.8, where the snapped cell is 7.78 x 17.78.
- The grid origin is not snapped to device pixels. `TOPBAR_H` 38 and
  `PAD_T` 4 at scale 1.25 put the grid at y = 74.5 device px. Glyphs round
  to whole pixels vertically, but the background quads stay at .5 and get
  soft edges.

Fix:

- Store the scale factor in `Metrics`. In `paint_grid`, compare it with
  `window.scale_factor()` and rebuild the metrics and clear every painter
  when it changes (bump `epoch`).
- Snap `origin` to device pixels in `paint_grid`:
  `(v * scale).round() / scale` for x and y. Snap the header and hairline
  positions the same way.

### 3. The terminal font is 14 px, not 14 pt

`font_size` goes to GPUI as pixels. PERFORMANCE.md and the README call it
14 pt. 14 px with JetBrains Mono gives an 8x18 cell, which reads small and
thin on a 1080p screen. Ghostty's default, 13 pt, is about 17 px at 96 dpi.

Fix: correct the docs to px. Try 15 px with the grayscale change above when
the new design is chosen. This is a design call, not a bug.

## Smoothness and CPU

### 4. Every pane output re-renders the whole window

`TuiosApp` is one view. `apply()` calls `cx.notify()` for each batch of pane
output, so the sidebar, top bar and palette are rebuilt and laid out by
taffy at 60 Hz while any pane prints. Taffy layout is 11.5 % of the app's CPU
with 4 busy panes. `render` takes 0.17 to 0.31 ms and makes 530 to 900
allocations (114 to 136 KB) per frame, before layout.

Fix: split the window into views: `Sidebar`, `TopBar`, `Grid` and
`Palette`, each an `Entity` rendered through `AnyView::cached()`. Pane output
notifies only `Grid`. Fleet and state changes notify `Sidebar` and `TopBar`.
GPUI then replays the cached views' layout and scene ranges. The expected
gain is most of the 11.5 % taffy share, plus most of the per-frame
allocations outside the painter.

### 5. Scrolling rebuilds every row on every frame

ghostty marks every row dirty when the viewport moves, and `row_above()`
also sets `full_dirty`. Rows get new generation numbers even when their
content only moved up. Measured rows rebuilt per frame:

- smooth scroll in history: 46 per frame, the whole 47-row pane;
- 4 streaming panes: 98 per frame, every visible row of every pane.

The shape cache stops this from reshaping, but each rebuild still re-plans
the row, looks up every word and rebuilds the glyph and quad lists.
`prepare` is 0.37 to 0.70 ms p50 for this. `build_row`, `plan_row` and
`copy_row` are about 7 % of the app's CPU.

Fix: cache built rows by content, not by position. Compute a 64-bit hash of
each row's cells and text in `copy_row` (it already walks every cell). In
`PanePainter`, keep a small map from hash to `RowCache` (about 2 screens) and
take a row from it before `build_row`. A scroll by one line then builds one
row. Keep the generation check as the fast path for unchanged rows. Remove
`full_dirty = true` from `row_above()`.

### 6. The working spinner redraws the whole window 10 times a second

`spinner()` calls `cx.notify()` every 100 ms while any agent in any session
works. Each tick is a full frame: about 3,450 allocations and 2.3 MB, so
23 MB a second of allocation churn while idle. App CPU at idle is 2.9 % with
the spinner and 0.9 % without it. The timer also calls `all_panes()` 10
times a second for as long as the app runs, even when nothing works, which
clones and formats every pane.

Fix: draw the spinner in its own small cached view (see finding 4), so a
tick re-renders only that view. Drop to 8 steps at 8 Hz, or use a slow pulse.
Keep a `working` count updated in `on_state` and in the fleet poll instead
of calling `all_panes()` in the timer. Stop the timer when the count is 0.

### 7. The sidebar spawns two tuios processes every 1.5 s

`poll_fleet` runs `tuios ls` and `tuios list-agents` every 1.5 s, about 80
process starts a minute. The children use 0.7 to 0.9 % of a core, as much
as the app at idle. The poll also calls `cx.notify()` on every poll when
any agent exists, so the ages can tick. That is about 0.7 full frames a
second (2.6 MB each) for a label that shows minutes.

Fix:

- Add a fleet event to the bridge. The bridge already holds a daemon
  connection, so it can forward agent state for all sessions. Keep the poll
  only as a fallback, at 5 s or slower, and pause it when the window is not
  focused.
- Notify only when the data changed, or when an age label would change.
  Compute the next change time from `fleet::age` (10 s steps under a minute,
  then minutes) and set one timer for it.

### 8. The shape cache allocates on every lookup and forgets everything at once

- `ShapeCache::get` builds a `Box<str>` key for every lookup, hit or miss
  (`self.words.get(&(style as u8, Box::from(word)))`). That is one
  allocation for each word of each rebuilt row.
- When the map reaches 32,768 entries it calls `clear()`. All hot words go
  at once, and the next frames miss on everything.
- Unique tokens (timestamps, hashes, counters) always miss, at 15 to 20 µs
  each. The Unique phase of `--perf` takes 6.4 ms of prepare a frame.

Fix:

- Use four maps, one per font style, `HashMap<Box<str>, Rc<[WordGlyph]>>`,
  and look up with `get(word)` through `Borrow<str>`. No allocation on a hit.
- Replace `clear()` with a two-generation cache: when the current map is
  full, it becomes the old map and a new one starts. A hit in the old map
  moves the entry back.
- Shape runs of ASCII letters and digits through a per-character glyph
  cache (`glyph_for_char` and the fixed cell advance). Send only words that
  hold ligature characters (`-<>=!&|*/:.+~#%?`) or non-ASCII text to the
  shaper. Check first that the chosen font has no ligature or contextual
  alternate between letters and digits only. JetBrains Mono's `calt`
  rules work on symbol sequences.

### 9. Per-frame copies and allocations in the paint path

`paint_grid` allocates 186 to 2,500 times and 0.3 MB per frame. Sources:

- `self.state.clone()` copies the whole `State` (every window and its
  strings) each frame, only to read the session name for the title.
- `attached_panes()` builds a new `PaneInfo` per pane with `format!` names,
  collected into a `HashMap` each frame.
- `self.theme.clone()` each frame; `rows: Vec<(f32, &RowCache)>` per pane per
  frame; `term.snapshot()` called twice per pane.
- `paint_header` shapes 2 or 3 strings per pane per frame, and the
  truncation loop in `ui_line` can shape the same text many times.

Fix: borrow `State` and `Theme` instead of cloning. Cache `PaneInfo` and the
shaped header lines per window and rebuild them only when the state
changes. Iterate rows in place instead of collecting. Call `snapshot()` once.
Truncate by measuring once and cutting at the char that crosses the limit.

### 10. GPUI sorts the scene with a stable sort that allocates

About 1 allocation of 400 KB per frame comes from `Scene::finish`, which
sorts `SubpixelSprite`s with `sort_by_key` (a stable sort that needs a
buffer of half the slice). Sorting is 5 % of the app's CPU under load.
Overall, 2.5 to 3.2 MB per frame is allocated outside tuios-gpui's code.

Fix: this is upstream. `sort_unstable_by_key` on `(order, tile_id)` gives the
same picture, except where two sprites with the same order and tile
overlap. Check that case before the change. Patch it in a vendored
`gpui-pre` or send it upstream. Moving to grayscale (finding 1) does not
remove the sort, but each sprite then goes to the monochrome list.

## Memory

### 11. The GPU driver choice costs 130 MB of RSS

GPUI creates its wgpu instance with `Vulkan | GL` and enumerates every
adapter. On this machine that loads the NVIDIA Vulkan and EGL drivers, the
Intel Vulkan driver, and Mesa's GL (gallium and LLVM). The NVIDIA userspace
driver alone maps about 110 MB.

| Setup | RSS | PSS | Anonymous | Start to content |
| --- | --- | --- | --- | --- |
| Default (NVIDIA picked, all drivers loaded) | 193 MB | 105 MB | 37 MB | 0.25 to 0.29 s |
| Intel Vulkan only, GL still probed | 107 MB | 84 MB | 33 MB | 0.17 s |
| Intel Vulkan only, no GL backend | 61 MB | 47 MB | 18 MB | 0.15 s |

Most of the NVIDIA mapping is shared file pages, so PSS shows less than
RSS. On a hybrid laptop the integrated GPU also saves power, because the
dGPU can stay asleep. When the compositor runs on the dGPU, rendering on the
iGPU adds a copy between GPUs, so this must be a choice, not a default.

Fix:

- Add `gpu = "auto" | "integrated" | "discrete"`. For "integrated", set
  `VK_DRIVER_FILES` to the integrated driver's ICD before GPUI starts, and
  remove it from the bridge's environment so programs in panes do not get
  it.
- Patch `gpui-pre-wgpu` to create the instance with Vulkan only and fall
  back to GL only when no Vulkan adapter works. That saves about 46 MB
  (Mesa gallium and LLVM) on the Intel path.

### 12. Every pane keeps 10,000 lines of history, hidden panes too

`pane::SCROLLBACK` is 10,000 lines for every pane of the attached session,
also the ones on other workspaces. With 4 panes of 29 to 60 columns the
anonymous memory grew from 36 MB to 59 MB in 60 s and then stayed there,
about 5 MB per 60-column pane. A 160-column pane needs about 13 MB. The
daemon holds the same history.

Fix: lower the GUI's history to 2,000 to 3,000 lines and make it a setting.
Give panes on hidden workspaces a small history (about one screen) and grow
it when they are shown. Scrolling past the GUI's history can later ask the
bridge for older lines.

### 13. Smaller items

- Atlas: 5 MB of GPU memory, 5 % used, no churn. Grayscale (finding 1)
  replaces the 4 MB BGRA texture with R8 glyphs. No other change needed.
- The release binary is 239 MB because of `debug = "line-tables-only"`.
  It maps 21 MB of RSS. Use `split-debuginfo = "packed"` or strip for
  packages.
- Startup: the GPU device takes 230 to 270 ms of the 0.35 s. Finding 11
  brings the whole start to 0.15 s. The font database and the bundled font
  take about 25 ms.

## Not a problem

- Glyph placement at scale 1: cells are whole pixels and every glyph is on
  a whole device pixel, so GPUI uses one subpixel variant per glyph.
- Hinting: swash rasterizes with hinting on, and layout uses unhinted
  advances. Glyphs are pinned to cells, so unhinted advances cannot drift
  the grid.
- Frame pacing: 60 Hz held with p95 under 18.2 ms in every scenario.

## Order of work

1. Grayscale text with contrast 2.0, and the scale-factor rebuild with
   snapped origins (findings 1 and 2). Small changes with a visible effect.
2. Split the root view into cached views and move the spinner into its own
   view (findings 4 and 6). This cuts the most CPU and idle wake-ups.
3. The content-addressed row cache and the shape cache fixes (findings 5
   and 8).
4. The fleet event in the bridge (finding 7), the GPU setting and the
   Vulkan-only patch (finding 11), and the history limit (finding 12).
5. Per-frame copies and the upstream sort fix (findings 9 and 10).
