# Research: a native GPU client for tuios

This note records what was studied before tuios-gpui was built, and the
decisions that came out of it. Paths are given so each claim can be checked.
Dates are from 2026-10-05.

## 1. How herdr-gpui works

herdr-gpui is a Rust and GPUI client for the herdr daemon (Apache-2.0,
`github.com/penso/herdr-gpui`, studied at `~/.cache/agent-tmp/herdr-gpui`). It
pins `gpui-pre =0.3.6` and `gpui-pre-platform =0.3.6` with the features
`font-kit runtime_shaders wayland x11`, and Rust 1.96.1
(`Cargo.toml`, `rust-toolchain.toml`).

### Transport and protocol

- A Unix socket (a named pipe on Windows), `crates/herdr-client/src/transport.rs`.
- Each frame is a u32 little-endian length and a bincode-2 payload
  (`crates/herdr-protocol/src/codec.rs`). Enums are encoded by position, so
  field order is the contract (`wire.rs:1-3`).
- The handshake and the sidebar snapshot are JSON inside a bincode envelope
  (`EndpointControl{kind: "endpoint.hello.v1"}`, `"shell.snapshot.v1"`).
- **The daemon sends rendered cells, not PTY bytes.** `CellData { symbol:
  String, fg: u32, bg: u32, modifier: u16, skip: bool, hyperlink }`
  (`wire.rs:259-286`). One `PaneSurfaceFrame` covers the whole window grid,
  with every pane's rectangle in it. Changes arrive as `PaneSurfacePatch`
  rows, fenced by revision, plus optional scroll, delta and reuse encodings.
- **The client sends meaning, not bytes.** Keys arrive at the daemon as
  `ClientPaneInputEvent::Key{code, modifiers, kind}`, text as `TextCommit`,
  and the daemon encodes them. Bracketed paste is wrapped by the daemon.
- An I/O thread polls the socket and a wake socketpair with `rustix::poll`,
  and sends `ClientEvent`s over a bounded crossbeam channel (`connect.rs`,
  `queue.rs`). A GUI thread applies them to an `Arc<Mutex<LiveState>>`, which
  the UI thread polls each frame through `window.on_next_frame`.

### The painter

`crates/herdr-gpui/src/terminal_painter.rs` and `terminal_painter/*`:

- No custom `Element`: everything is drawn in `canvas(prepaint, paint)`.
- Cell width is the shaped width of `"M"`; line height is `size * 20/14`.
- **Each cell is shaped on its own** and placed at `column * cell_width`. The
  shape cache is keyed by (bold/italic style, symbol), not colour, with an
  ASCII fast table and a 4096-entry limit (`terminal_painter/glyphs.rs`).
  There are no ligatures.
- Glyphs are painted with `Window::paint_glyph`/`paint_emoji` directly instead
  of `ShapedLine::paint`, to skip the layer the latter pushes per call
  (`terminal_painter.rs:205-236`).
- `Window::paint_layer` groups backgrounds, text and decorations into three
  layers, which skips GPUI's per-primitive bounds-tree insert.
- Backgrounds of equal colour are merged per row into one quad. Box-drawing
  and block characters (U+2500 to U+259F) are drawn as pixel-snapped quads.
- Unchanged pane regions keep a cached GPUI view, so GPUI replays their scene
  (`window/regions.rs`).

### Input, window, performance

- Keys go through `on_key_down`; printable text goes through
  `EntityInputHandler`, which keeps IME and dead keys working.
- Wheel deltas are divided by the cell height with the remainder kept; there
  is no pixel-smooth scrolling of content.
- Selection is client-side on half-cell anchors; copy uses
  `cx.write_to_clipboard`.
- `PERFORMANCE.md` (M4 Max, 160x50 grid, release build, GPUI 0.2.2) measured
  scene construction going from p50 49 ms to 11.8 ms on a cold cache and
  11.5 ms warm, by removing per-cell shaping on warm frames (6,981 shapes to
  0) and merging backgrounds (8,000 quads to 50). The harness only runs on
  macOS: it opens a real window and injects AppKit events.

### What tuios-gpui borrows

Under Apache-2.0, with attribution in `NOTICE` and in the code comments:

- painting glyphs with `paint_glyph` inside `paint_layer` instead of
  `ShapedLine::paint`;
- merging equal backgrounds per row;
- drawing box and block characters as rectangles snapped to the grid;
- the measuring approach of the performance harness.

No herdr-gpui code was copied verbatim. tuios-gpui's painter differs on the
point that matters most: it shapes whole runs per row (so ligatures work) and
caches the result per row, keyed by the emulator's row generation.

## 2. GPUI

| Crate | Version | Source |
| --- | --- | --- |
| `gpui` | 0.2.2 (2025-10-22) | crates.io, last stable release |
| `gpui-pre` | 0.3.8 (2026-10-05), weekly | crates.io, snapshot of `zed@279fe07` |
| `gpui-pre-platform` | 0.3.8 | crates.io; enables nothing by default |

- tuios-gpui uses `gpui-pre =0.3.8` with `gpui-pre-platform` features
  `font-kit wayland x11`. Without a platform feature, text silently goes to a
  no-op system (noted in herdr-gpui's `Cargo.toml`).
- **Linux renders through wgpu 29 (Vulkan)**, crate `gpui-pre-wgpu`. Blade is
  gone. Wayland and X11 are both supported (`gpui-pre-linux`), chosen at run
  time.
- **Text on Linux is cosmic-text with swash rasterisation**
  (`gpui-pre-wgpu/src/cosmic_text_system.rs`). `FontFallbacks` gives an explicit
  chain tried before the system fallback.
- Found while building: the Linux text system **removes a font from its
  database when it is loaded by name and has no glyph for `m`**
  (`cosmic_text_system.rs`, the `charmap().map('m') == 0` check). Naming
  "Noto Color Emoji" in a fallback chain therefore deletes it, and every emoji
  turns into a box. tuios-gpui leaves emoji to the shaper's own fallback.
- Custom drawing: `canvas()` or an `Element` impl; `Window::paint_quad`,
  `paint_glyph`, `paint_emoji`, `paint_layer`, `with_content_mask`.
  `TextSystem::shape_line` returns glyph ids, byte indices and positions;
  GPUI caches line layouts only from one frame to the next.
- `Window::dispatch_event` is public, so tests can drive real input paths.
  `Window::render_to_image` needs the `test-support` feature.

### How Zed's terminal paints (GPL, studied for ideas only)

`crates/terminal_view/src/terminal_element.rs` (GPL-3.0-or-later, emulator
`alacritty_terminal`). No code was taken from it.

- Cells are batched into `BatchedTextRun`s of identical style, colour
  included, and each batch is shaped every frame with
  `shape_line(.., force_width: Some(cell_width))`, which pins every glyph to
  the cell width; GPUI's frame-to-frame layout cache absorbs repeats.
- Backgrounds are collected as `BackgroundRegion`s and merged across rows into
  rectangles (`merge_background_regions`).
- Block elements are drawn on a sub-cell grid (8 columns by 24 lines).

tuios-gpui takes the same broad shape (runs per row, merged backgrounds,
quads for blocks) but caches shaped rows across frames and keeps colour out
of the run key, so a colourful row is still one shaping call per font style.

## 3. libghostty-vt from Rust

- The C API (`include/ghostty/vt.h` and `vt/*.h`, about 11,500 lines) has
  everything a GUI needs:
  - `ghostty_terminal_*`: create, `vt_write`, resize, scroll the viewport,
    query modes, kitty keyboard flags, title, scrollbar.
  - The render state (`vt/render.h`): a dirty flag per screen and per row, a
    row iterator, a cell iterator with resolved fg and bg, style, grapheme
    codepoints, and the row's selection range.
  - Key and mouse encoders (`vt/key/encoder.h`, `vt/mouse/encoder.h`) that read
    the terminal's modes (`setopt_from_terminal`), including the kitty
    protocol.
  - Selection (`vt/selection.h`): grid refs, word and line selection, and
    formatting a selection as text. Paste encoding (`vt/paste.h`).
- Existing bindings: `libghostty-vt 0.2.2` and `libghostty-vt-sys 0.2.2`
  (`github.com/uzaaft/libghostty-rs`, MIT or Apache-2.0). They pin ghostty
  `a887df42` (2026-07-11). tuios pins `27e8b3fa` (2026-09-20) in
  `scripts/ghostty-lib.sh`, 1,307 commits later; the headers differ by +3,118
  and -345 lines. The crate's checked-in bindings do not match the library
  tuios builds.
- **Decision:** generate bindings with `bindgen 0.71.1` from the headers of the
  tuios prebuilt (`.ghostty-vt/native/include`) into
  `crates/ghostty-vt/src/ffi.rs`, and link its static `libghostty-vt.a`
  (`crates/ghostty-vt/build.rs`). The archive needs only libc and libm.
  Zig 0.16 builds the archive (`zig build -Demit-lib-vt -Dcpu=baseline`); the
  vendored crate would also need Zig, plus a network fetch.
- `crates/ghostty-vt` wraps the parts the GUI uses in about 900 lines of safe
  Rust. Its tests cover dirty rows, wide and grapheme cells, truecolour and
  inverse, key encoding with the kitty protocol, bracketed paste, selection,
  scrollback and the row above the viewport.

## 4. The tuios daemon protocol

From `internal/session` on `origin/main` (`52361999`):

- One Unix socket at `$XDG_RUNTIME_DIR/tuios/tuios.sock` (or
  `/tmp/tuios-<uid>/tuios.sock`) carries two protocols. The daemon peeks at the
  first byte: `{` starts line-delimited JSON verbs, anything else is the
  binary protocol (`daemon.go`, `verb_protocol.go:2112`).
- Binary frames: 4-byte big-endian length, 1 type byte, 1 codec byte, then a
  **gob** payload (`protocol.go:186-201`). PTY input and output are raw frames
  with a 36-byte PTY id (`protocol.go:1013-1082`).
- Attaching (`docs/REHYDRATION.md`): hello and welcome; attach, which returns
  the whole `SessionState`; then per pane `GetTerminalState` (a packed
  snapshot with `Seq`), then `SubscribePTY{FromSeq: Seq, FromSnapshot: true}`.
  Output frames carry no sequence numbers; resizes arrive in band as
  `PTYResized` and must be applied in order with the bytes.
- The JSON verbs cover listing sessions and windows, input (`send-text`),
  resize, workspaces, splits, agent state and an event stream (`subscribe`).
  **No verb streams pane bytes or returns a snapshot that rebuilds an
  emulator**: `capture-pane` returns text, and the `output` event only counts
  bytes (`docs/protocol.md:4848`).
- **The daemon does not compute pane rectangles.** It stores layout intent (BSP
  trees, ratios, layout mode); every client tiles from it with
  `internal/layout`, places new windows, and answers routed commands such as
  `split-window` (`docs/protocol.md:1474-1481`, `internal/app/tiling.go`).

### Three ways to reach it from Rust

- **(a) A language-neutral stream in the daemon.** Clean in the long run, but
  it still leaves the tiling, window placement and routed commands to
  reimplement in Rust, which is most of `internal/app`.
- **(b) A Go bridge subcommand.** It runs inside the tuios tree, so it can use
  `internal/session` and `internal/app` directly.
- **(c) A cgo c-archive linked into Rust.** It shares the bridge's advantages
  but puts a Go runtime, its signal handling and its threads inside the GUI
  process, and couples the two builds.

**Chosen: (b), with the full client model inside the bridge.** `tuios
gui-bridge` runs `app.OS`, the same model the terminal client runs, with
`tea.WithoutRenderer()`. Tiling, window placement, workspaces, routed
commands and agent state therefore behave exactly as in the TUI, with no
second implementation to drift. The bridge sends the GUI:

- the layout as JSON after every model update that changes it;
- each pane's stream through a new hook, `app.StreamTap`: the snapshot the
  pane was primed from, converted to VT bytes that rebuild it in a fresh
  emulator, then the live output and in-band resizes, in apply order.

The GUI sends input bytes (written straight to the daemon) and commands
(resize, focus, workspace, any tape command through the routed-command queue).
Framing is the simplest that works: a u32 length, a kind byte, then JSON or
raw bytes (`internal/guibridge/bridge.go`). The change is on the local branch
`exp/gpui-bridge` (see the README).

## 5. Architecture

- **The daemon owns** PTYs, the authoritative emulators, layout intent,
  sessions and agent state.
- **The bridge owns** the client model: tiling into the GUI's grid, placement,
  focus, routed commands.
- **The GUI owns** one libghostty-vt per pane, fed by the stream; rendering;
  fonts and shaping; chrome; keyboard, mouse and paste encoding (through
  ghostty's encoders, so the kitty protocol and mouse modes follow what each
  program asked for); selection, scrollback and smooth scrolling, which are
  local and never reach the daemon.

### Emulator per pane, or daemon-sent cells?

herdr-gpui paints cells the daemon renders. tuios-gpui feeds bytes into its own
emulator. Measured with the harness (release build, `docs/PERFORMANCE.md`):

- Feeding a full 160x50 screen of dense styled text costs a p50 of
  0.25 ms, small next to painting.
- On the wire, VT bytes are smaller than a cell grid. For the full-screen
  phase the harness counts about 23 KB of VT per frame against about 105 KB
  for the same frame as a cell grid. For streaming output it is 4.7 KB against
  about 104 KB, because every scrolled row is a changed row.
- A local emulator gives scrollback, selection, search and smooth scrolling
  without a round trip, and lets ghostty's encoders see the exact modes the
  program set.
- Its costs are memory (a scrollback per pane) and a snapshot transfer on
  attach. tuios already pays the latter for its TUI clients.

So the GUI runs ghostty per pane, which is also what tuios's own clients do.
