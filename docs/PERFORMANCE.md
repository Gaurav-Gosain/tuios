# Performance

The harness draws a dense 160x50 pane through the same emulator and painter
as the app, in a real window, and times each part of every frame. Run it with:

```sh
cargo run --release -- --perf --perf-out perf.json
```

Each phase runs 240 timed frames after 20 warm-up frames. The raw results of
the two runs below are in `docs/perf/perf-release-1.json` and
`docs/perf/perf-release-2.json`.

## What is timed

- **feed**: writing the frame's bytes into the pane's libghostty-vt.
- **prepare**: copying the rows ghostty marked dirty into Rust, planning them
  (runs, backgrounds, decorations, box drawing) and shaping the words that are
  not yet in the shape cache.
- **paint**: building the GPUI scene for the pane: one background quad, the
  merged background spans, about 6,000 glyphs, decorations and the cursor.
- **interval**: time from one frame to the next. It includes GPUI's layout of
  the chrome, the GPU submit and the wait for the display. It cannot be lower
  than the refresh period.

GPU time is not measured separately.

## Machine

- Intel Core i7-10700, Intel UHD 630 and NVIDIA RTX 3070, Linux 7.2, Hyprland
  0.56 on Wayland, two 1920x1080 monitors at 240 Hz, scale 1.
- GPUI `gpui-pre 0.3.8` (wgpu 29, Vulkan), release build.
- JetBrainsMono Nerd Font Mono 14 pt, cell 8 x 18 px.

## Results

Milliseconds, p50 / p95, run 2. Run 1 agrees within 0.1 ms except one Full
interval p95 of 7.9 ms.

| Phase | feed | prepare | paint | interval | glyphs per frame |
| --- | --- | --- | --- | --- | --- |
| Full: every row new, code-like words | 0.37 / 0.44 | 0.81 / 0.87 | 0.65 / 0.67 | 4.16 / 4.34 | 5,967 |
| Unique: every row new, every word unseen | 0.21 / 0.24 | 6.40 / 6.72 | 0.75 / 0.76 | 12.50 / 12.70 | 7,000 |
| Stream: 5 new lines per frame, screen scrolls | 0.05 / 0.06 | 0.84 / 0.88 | 0.65 / 0.66 | 4.17 / 4.24 | 5,974 |
| Typing: one cell changes | 0.00 / 0.00 | 0.05 / 0.06 | 0.65 / 0.68 | 4.17 / 4.22 | 5,953 |
| Idle: nothing changes | 0.00 / 0.00 | 0.00 / 0.00 | 0.65 / 0.70 | 4.17 / 4.22 | 6,002 |
| Smooth scroll: 3 px per frame in history | 0.00 / 0.00 | 0.01 / 0.92 | 0.67 / 0.70 | 4.17 / 4.22 | 6,044 |

Every phase except Unique holds the display's 240 Hz. Unique, the worst case
for shaping, holds 80 frames a second.

## What made the difference

- **Words are shaped once.** The first version shaped each row's runs when the
  row changed. GPUI's line layout cache keeps a layout only from one frame to
  the next, so a row that moved (scrolling) or came back was shaped again. On
  the Full phase that cost 7.7 ms of prepare at p50 and held the frame rate to
  80 per second. A cache of shaped words shared by every pane (`ShapeCache` in
  `src/painter.rs`) brought prepare to 0.8 ms. Ligatures stay intact: they sit
  inside words, and a space breaks them in every monospace font.
- **Rows are rebuilt only when ghostty marks them dirty.** A row keeps its
  generation number until it changes, and the painter's cached glyphs and
  quads for that row are replayed until then. Idle frames do no preparation.
- **Glyphs are pinned to their cell.** Each glyph is placed at its cell's
  column, so fallback fonts with other advances (CJK, emoji) never drift the
  grid.
- **One layer per pane.** Backgrounds and glyphs are painted inside
  `Window::paint_layer`, as herdr-gpui found, which skips GPUI's bounds-tree
  insert for every primitive.
- **Generations are unique per process.** A resize rebuilds the screen; an
  early version restarted the generation counter, and the painter replayed
  stale rows that happened to share a number. The counter is now global, with
  a regression test (`generations_never_repeat_across_resizes`).

## Compared with herdr-gpui

herdr-gpui's `PERFORMANCE.md` reports scene construction for the same 160x50
grid at p50 11.5 ms warm and 11.8 ms cold, on an Apple M4 Max with GPUI 0.2.2.
Its timing covers event delivery and `Window::draw`. The paint column above
covers only the pane's own scene, so the two are not the same measurement.
The interval column bounds the whole frame from above: 4.2 ms here.

## Bytes on the wire

The harness also counts what each frame costs to send.

| Phase | VT bytes per frame | the same frame as a cell grid |
| --- | --- | --- |
| Full | 37,404 | 104,345 |
| Stream | 3,731 | 104,335 |
| Typing | 1 | 2,418 |
| Smooth scroll | 0 (local) | 34,801 |

The cell-grid column assumes herdr's encoding: per changed cell its text plus
about 12 bytes of colour and attributes. A pane that streams output is about
28 times cheaper to send as bytes, because a scroll changes every row. And
because each GUI pane has its own emulator, scrolling the history costs no
traffic at all.
