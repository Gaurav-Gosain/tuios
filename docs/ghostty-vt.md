# The ghostty emulator backend

tuios has two terminal emulator implementations behind one interface
(`vt.Terminal`), selected at build time:

- **Pure Go** (default): `internal/vt`'s own emulator. `go install` works
  with no toolchain, `CGO_ENABLED=0` cross-compilation works, nothing
  changes.
- **libghostty-vt** (`-tags ghostty`): parsing and grid state run inside
  [Ghostty](https://github.com/ghostty-org/ghostty)'s battle-tested VT
  library through the official Go bindings
  (`go.mitchellh.com/libghostty`). Release artifacts named `-ghostty` are
  built this way.

Exactly one implementation is compiled into a binary. The two never run
together in one process; comparing them is the differential harness's job
(`internal/vt/ghostty_differential_test.go`,
`internal/session/ghostty_wire_test.go`), which feeds identical bytes to
both and asserts screens, cursor, scrollback, modes and wire snapshots
agree.

## Building with the tag

```sh
./scripts/ghostty-lib.sh            # needs zig >= 0.16; caches in .ghostty-vt/
PKG_CONFIG_PATH="$PWD/.ghostty-vt/native/pkgconfig" go build -tags ghostty ./...
```

`ghostty-lib.sh` pins the ghostty commit; bumping it is a deliberate
change, reviewed like a dependency bump, and `go.mod` pins the binding
commit to match. Cross targets: pass a zig target triple
(`x86_64-windows-gnu`, `aarch64-macos`, ...). Linux and Windows artifacts
cross-compile from Linux with zig as the C compiler; darwin builds run
natively on a macOS runner in the release workflow because Apple's SDK
does not cross-compile cleanly.

## Architecture

`GhosttyTerminal` (`internal/vt/ghostty_*.go`) owns what tuios needs on
top of the library:

- **Stream scanner** (`ghostty_scan.go`, pure Go, unit-tested without
  cgo): tokenizes the PTY stream once, forwards everything to the
  library, and intercepts what tuios owns: kitty APC and sixel DCS
  (withheld; tuios's passthrough pipeline is their only consumer, and
  forwarding sixel would render it twice), OSC 52/4/104/10/11/12
  (answered Go-side so the library cannot answer queries a second time),
  OSC 66 text sizing, OSC 133 semantic markers, and the CSI/ESC
  dispatches whose state the library keeps but does not expose (DECSTBM
  values, charset designations, the kitty keyboard stack).
- **Shadow grid**: a uv-typed copy of the viewport, refreshed from the
  render state's dirty rows on the first read after a write. Each cell
  costs one Go memory read: the packed cell layout is decoded using the
  manifest the library publishes at runtime (`TypeJSON`), never
  hardcoded, with cgo getters as the fallback. Only grapheme clusters,
  hyperlinks and style-cache misses call into the library.
- **Snapshot restore**: the `Restore*`/`SetCell` family used by daemon
  reattach buffers everything and flushes as one synthesized VT stream in
  a canonical order, so it does not depend on `ApplyTerminalState`'s call
  order. Painted rows never run through the last column: the library
  would treat them as wrapped logical lines and reflow them into each
  other on the next resize.
- **Query answering**: DA1 is configured to answer exactly what the pure
  emulator answers; CPR/DSR/DECRQM come from the library itself through
  the `WritePty` callback into the same response pipe.

## Accepted divergences

Tracked in `TestGhosttyKnownDivergences`; each entry names which side is
right, and an entry that starts agreeing must be deleted.

- SGR 21 (double underline): the library honors it, the pure emulator
  drops it. The library is right.
- Resize semantics: the library reflows wrapped lines and moves rows
  between screen and history; the pure emulator clips. Both are valid
  terminal behaviors; the reflow is what Ghostty itself does.
- `SetScrollbackMaxLines` after construction is a no-op on the library
  backend (the limit is a construction option).
- The pen's open OSC 8 hyperlink is not carried across a wire snapshot
  (cell hyperlinks are).

## Blast radius

The emulator runs inside the daemon, so this is the part to be honest
about: a Go panic in the pure emulator is also fatal to the daemon, but
it produces a stack and dies cleanly; a memory fault inside the C library
is a SIGSEGV that cannot be recovered and takes every session with it.
Three things bound that risk:

- The library is the same code Ghostty ships to production terminals,
  fuzzed and exercised far beyond what `internal/vt` sees.
- `FuzzGhosttyTerminalWrite` fuzzes tuios's own cgo boundary: arbitrary
  bytes in adversarial chunkings through the full adapter path,
  interleaved with reads and resizes.
- Every path that reaches the library checks for a concurrent Close
  after taking the adapter lock, so teardown cannot race a reader into
  freed memory.

What it buys: the conformance classes the pure emulator keeps paying for
(CPR transposition, SO/SI, IRM, margins) cannot regress here, and the
parser is 1.6x faster on full-screen truecolor repaints, 2.7x on
editor-style redraws and 15x on plain scroll at the emulator level
(`BenchmarkBackend*`, which runs against whichever backend the build
selected). Running the real DOOM-fire game in a pane: ~260 fps pure,
~330 fps ghostty on the same machine.
