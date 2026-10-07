# Performance

Two tools measure the app:

- `scripts/measure.py` runs the real app against a private daemon and
  reports CPU, frames, paint time and memory for five everyday cases.
- `tuios-gpui --perf` draws a dense 160x50 pane through the same emulator
  and painter in a real window, and times each part of every frame.

The numbers below compare the build before the redesign (`ba69b2c`, plus
the `stats` control command) with the redesign. The design and its budget
are in [design/FINAL.md](design/FINAL.md) section 10. The audit that
listed the work is [PERF-AUDIT.md](PERF-AUDIT.md).

## Machine and method

- Intel Core i7-10700, Intel UHD 630 and NVIDIA RTX 3070, Linux 7.2.
- Release builds, scale 1, window 1440x900.
- Both tools run in `gamescope --backend headless -r 60`, so frame
  intervals cannot go below 16.7 ms. The runs on the 240 Hz desktop before
  the redesign are in `perf/perf-release-1.json` and `perf-release-2.json`.
- CPU is from `/proc`, in percent of one core. "Children" are the `tuios`
  processes the app starts itself. RSS and anonymous memory are from
  `/proc/self/smaps_rollup`.

```sh
scripts/measure.py TUIOS BIN OUT.json        # TUIOS: the bridge-branch tuios
GUI_CONFIG_HOME=DIR SCENARIOS=busy4 scripts/measure.py TUIOS BIN OUT.json
gamescope --backend headless -W 1440 -H 900 -r 60 -- BIN --perf --perf-out perf.json
```

## The app, before and after

The cases:

- **idle-quiet**: the demo session. No agent works, and one pane prints a
  timer once a second.
- **idle-spin**: the same, with two agents working.
- **busy4**: four panes, each printing about 200 coloured lines a second.
- **busy1**: three panes, one of them printing.
- **scroll**: one pane with 6,000 lines of history, wheel steps of 40 px
  every 16 ms.

| Case | App CPU | Children CPU | Frames/s | Stage paints/s | Paint p50 / p95 | RSS | Anonymous |
| --- | --- | --- | --- | --- | --- | --- | --- |
| idle-quiet, before | 0.6 % | 0.53 % | 1.7 | 1.7 | 0.27 / 0.33 ms | 177 MB | 37.2 MB |
| idle-quiet, after | 0.2 % | 0 % | 1.0 | 1.0 | 0.24 / 0.25 ms | 178 MB | 36.6 MB |
| idle-spin, before | 3.2 % | 0.67 % | 11.7 | 11.7 | 0.25 / 0.50 ms | 178 MB | 37.7 MB |
| idle-spin, after | 1.1 % | 0 % | 11.1 | 1.0 | 0.24 / 0.35 ms | 177 MB | 36.8 MB |
| busy4, before | 14.5 % | 0.67 % | 42 | 42 | 0.96 / 1.23 ms | 184 MB | 44.0 MB |
| busy4, after | 8.3 % | 0 % | 45 | 38 | 0.87 / 0.94 ms | 183 MB | 42.4 MB |
| busy1, before | 10.8 % | 0.60 % | 41 | 41 | 0.60 / 0.66 ms | 180 MB | 39.5 MB |
| busy1, after | 5.7 % | 0 % | 44 | 36 | 0.49 / 0.55 ms | 179 MB | 37.9 MB |
| scroll, before | 13.9 % | 0.50 % | 60 | 60 | 0.56 / 0.63 ms | 178 MB | 38.4 MB |
| scroll, after | 6.8 % | 0 % | 60 | 60 | 0.42 / 0.46 ms | 178 MB | 37.7 MB |

"Paint" is the stage's paint per frame: the stage and headers, every pane
that changed, and the splits. Before, it was the whole grid. The raw
results are in `perf/measure-before.json`, `perf/measure-before-busy1.json`
and `perf/measure-after.json`.

With `gpu = "integrated"` the GUI loads only the Intel Vulkan driver
(`perf/measure-after-integrated-gpu.json`):

| Case | App CPU | RSS | Anonymous |
| --- | --- | --- | --- |
| busy4 | 10.3 % | 148 MB | 37.0 MB |
| busy1 | 5.4 % | 144 MB | 33.3 MB |

Against the budget in FINAL.md section 10:

| Budget | Result |
| --- | --- |
| Idle: 0 frames/s except the cursor blink | Met. The one frame a second in idle-quiet is a pane that prints a timer. |
| Agents working: 10 frames/s, only the icon views, under 1 % CPU | 10 frames/s, and the stage and sidebar do not redraw. CPU is 1.1 %, of which about 0.2 % is the timer pane. |
| One pane streaming: only that pane redraws | Met: each pane is a cached view. |
| Paint p95 under 2 ms with 4 busy panes | Met: 0.94 ms. |
| Anonymous memory under 40 MB with 4 panes | Met with `gpu = "integrated"` (37.0 MB). With the NVIDIA driver it is 42.4 MB, about 5 MB of which the driver allocates. Met on every driver since the OpenGL probe was dropped: 32.1 MB (see below). |

After the padded layout (section 11 of FINAL.md: each pane's body is one
view with its padding), `perf/measure-after-insets.json`, where the
integrated GPU run is marked as such:

| Case | App CPU | Frames/s | Paint p50 / p95 | RSS | Anonymous |
| --- | --- | --- | --- | --- | --- |
| busy4 | 9.9 % | 57 | 0.59 / 0.82 ms | 180 MB | 42.2 MB |
| busy1 | 6.3 % | 43 | 0.49 / 0.80 ms | 175 MB | 38.0 MB |
| busy4, `gpu = "integrated"` | 10.3 % | 55 | 0.64 / 0.87 ms | 144 MB | 36.6 MB |

`gpu = "auto"` now picks the integrated GPU by itself, but only when that
GPU drives every connected display. On this machine both displays are on
the NVIDIA card, and a window drawn on the Intel GPU comes up black in a
compositor that runs on NVIDIA, so "auto" keeps every driver here and
anonymous memory stays 2 MB over the budget.

### The OpenGL probe

A later run of busy4 on the same build measured 41.8 MB anonymous and
223 MB RSS (`perf/measure-before-egl.json`). A 90 s run, long enough for
the row pool to drop rows that scrolled out of view 30 s before, measured
42.3 MB. The row caches were not the cost.

`/proc/PID/smaps` showed where the memory went: 22.8 MB of heap, and about
10 MB of relocated pages in driver libraries. GPUI asks wgpu for Vulkan and
OpenGL both, and the OpenGL probe loads every EGL driver: NVIDIA's EGL and
GL cores, and Mesa's gallium with LLVM. The window never uses them.

Now the GUI sets an empty EGL vendor list before GPUI starts, when a Vulkan
driver is installed and `gpu` is not "any". Panes do not inherit it.
`perf/measure-after-egl.json`:

| Case | App CPU | Frames/s | Paint p50 / p95 | RSS | Anonymous |
| --- | --- | --- | --- | --- | --- |
| idle-quiet | 0.2 % | 1.0 | 0.24 / 0.26 ms | 175 MB | 26.9 MB |
| busy4, before | 9.7 % | 59 | 0.61 / 0.81 ms | 223 MB | 41.8 MB |
| busy4 | 8.1 % | 49 | 0.77 / 0.85 ms | 180 MB | 32.1 MB |
| busy1 | 5.3 % | 43 | 0.48 / 0.53 ms | 176 MB | 28.5 MB |

The budget of 40 MB with 4 busy panes is met with every driver loaded,
with 8 MB to spare. Most of the remaining RSS is mapped driver files.

## The painter, before and after

`--perf`, milliseconds, p50 / p95. Interval is capped at 16.7 ms by the
60 Hz headless output in both runs. Before: JetBrainsMono Nerd Font Mono at
14 px, cell 8x18. After: JetBrains Mono at 15 px, cell 9x20.

| Phase | prepare before | prepare after | paint before | paint after |
| --- | --- | --- | --- | --- |
| Full: every row new, code-like words | 0.83 / 0.87 | 0.88 / 0.97 | 0.69 / 0.72 | 0.67 / 0.74 |
| Unique: every row new, every word unseen | 6.79 / 8.97 | 0.81 / 0.95 | 0.79 / 0.85 | 0.76 / 0.81 |
| Stream: 5 new lines per frame | 0.89 / 1.08 | 0.88 / 0.92 | 0.69 / 1.04 | 0.67 / 0.77 |
| Typing: one cell changes | 0.08 / 0.11 | 0.02 / 0.02 | 0.79 / 0.98 | 0.69 / 0.72 |
| Idle: nothing changes | 0.00 / 0.01 | 0.00 / 0.00 | 0.71 / 0.92 | 0.69 / 1.11 |
| Smooth scroll: 3 px per frame | 0.01 / 1.17 | 0.01 / 1.00 | 0.74 / 0.96 | 0.71 / 0.92 |

The raw results are in `perf/perf-harness-before.json` and
`perf/perf-harness-after.json`. In Stream, prepare is now mostly the copy
of each row out of ghostty, which the scroll marks dirty.

## What made the difference

- **Each part of the window is a cached view.** The title band, the
  sidebar, the stage and each pane are views of their own. Pane output
  redraws only that pane; the stage's headers and splits are one cheap
  canvas, and the sidebar and band replay last frame's layout and scene.
  Before, every batch of output rebuilt and laid out the whole window.
- **The working icon has a view of its own.** The sidebar and the pane
  headers draw the icon's ring and record where its arc goes. The spinner
  timer redraws only the arcs, 10 times a second, and stops when no agent
  works or the window is not active.
- **Fleet events instead of polling.** The bridge watches the daemon's
  event stream and sends every session and agent when they change. The app
  no longer starts `tuios ls` and `tuios list-agents` every 1.5 s (about
  80 processes a minute). Ages redraw once a minute. Against an older
  bridge the app polls every 5 s, and only while the window is active.
- **ASCII skips the shaper.** With ligatures off (the default), a printable
  ASCII character shapes the same alone as in a word, and a monospace font
  has no kerning. Each one is shaped once per font style, so timestamps,
  counters and hashes cost no shaping. This is the Unique phase's drop from
  6.8 ms to 0.8 ms.
- **The shape cache keeps its hot words.** One map per font style, looked
  up by `&str` with no allocation, in two generations: when the current
  one fills, it becomes the old one, and a hit there moves the word back.
- **Rows are cached by content.** ghostty-vt hashes each row's cells and
  text. A row that changed position but not content (a scroll) takes what
  was built for it from a pool, so a scroll by one line plans one row.
- **Words are shaped once.** A cache of shaped words is shared by every
  pane. GPUI's own line cache keeps a layout only from one frame to the
  next.
- **Rows are rebuilt only when ghostty marks them dirty.** Unchanged rows
  replay a cached list of glyphs and rectangles.
- **Glyphs are pinned to their cell.** Each glyph sits at its cell's
  column, so fallback fonts with other advances never drift the grid.
- **One layer per pane.** Backgrounds and glyphs are painted inside
  `Window::paint_layer`, as herdr-gpui found, which skips GPUI's bounds-tree
  insert for every primitive.
- **Generations are unique per process**, with a regression test
  (`generations_never_repeat_across_resizes`).
- **Less history.** Panes keep 3,000 lines by default (`scrollback`),
  instead of 10,000. Panes on other workspaces drop their row caches.

## Crispness

- **Whole device pixels.** The cell is `round(advance x size x scale)` by
  `round(max(natural, size x line_height) x scale)` device pixels: 9x20 at
  scale 1, 11x25 at scale 1.25. The metrics are rebuilt when the window's
  scale changes. The grid origin, the stage, the headers, the hairlines
  and the ring are on whole device pixels.
- **Grayscale text with extra contrast.** `TextRenderingMode::Grayscale`
  and `ZED_FONTS_GRAYSCALE_ENHANCED_CONTRAST=2.0` (the `text_contrast`
  setting). A crop of sidebar text has no coloured pixels; the subpixel
  default had 461 in the audit's sample. The atlas holds 1 byte per pixel.
- **Cell-filling characters are shapes.** Box drawing, blocks, braille and
  Powerline separators are drawn as rectangles and polygons on the pixel
  grid.

## Compared with herdr-gpui

herdr-gpui's `PERFORMANCE.md` reports scene construction for the same
160x50 grid at p50 11.5 ms warm and 11.8 ms cold, on an Apple M4 Max with
GPUI 0.2.2. Its timing covers event delivery and `Window::draw`. The paint
column above covers only the pane's own scene, so the two are not the same
measurement.

## Bytes on the wire

The harness also counts what each frame costs to send.

| Phase | VT bytes per frame | the same frame as a cell grid |
| --- | --- | --- |
| Full | 37,404 | 104,345 |
| Stream | 3,731 | 104,335 |
| Typing | 1 | 2,418 |
| Smooth scroll | 0 (local) | 31,321 |

The cell-grid column assumes herdr's encoding: per changed cell its text plus
about 12 bytes of colour and attributes. A pane that streams output is about
28 times cheaper to send as bytes, because a scroll changes every row. And
because each GUI pane has its own emulator, scrolling the history costs no
traffic at all.
