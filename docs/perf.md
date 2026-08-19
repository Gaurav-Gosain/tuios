# Performance baselines

Idle cost is the number every milestone's Gate defends. "Low idle" (see the M2
plan) is: one attached client, sidebar on, N idle shells, clock off => zero
timer-driven renders, bounded per-tick work, no session-list polls without a
visible consumer, idle CPU under ~0.5%.

## How to measure

- `go test ./internal/app/ -run '^$' -bench BenchmarkIdleTick -benchmem` — work,
  allocations, and ns per maintenance tick at idle. `work/tick` is the fraction
  of ticks that ran the full-window maintenance scans; at idle it must trend to
  zero.
- `go test ./internal/app/ -run TestIdleTickSkipsScans` — asserts idle ticks
  take the skip path (no scan work), read from the `tickStats` counter.
- `TUIOS_E2E=1 go test ./e2e/tui/ -run TestIdleCostStaysLow` — boots the real
  binary, opens three idle shells, idles 10s, and asserts the app writes
  ~nothing to the wire (render count bounded). `TUIOS_STATS_FILE` makes the
  process dump its tick counters on clean exit.

## Numbers

`BenchmarkIdleTick` — 3 idle daemon windows, one tick per op:

| Milestone | ns/op | B/op | allocs/op | work/tick | render/tick |
|-----------|-------|------|-----------|-----------|-------------|
| M2 baseline (48c9c51) | 470 | 568 | 9 | 1.00 | 0 |
| M2 idle diet          | 260 | 296 | 5 | 0.00 | 0 |

`TestIdleCostStaysLow` — boot + 3 windows + 10s idle:

| Milestone | idle wire bytes / 10s | ticks | work | render |
|-----------|-----------------------|-------|------|--------|
| M2 baseline (48c9c51) | 0 | 104 | 104 | 0 |
| M2 idle diet          | 0 | 104 | 1   | 0 |

Frame-skip already held at baseline (zero idle renders). The diet's win is
per-tick work: baseline ran the full-window scans on every one of the ~100 idle
ticks; the diet skips them behind a cheap gate, so `work` stays flat while
`ticks` climbs (104 idle ticks, 1 did scan work). The residual ~260 ns / 5
allocs per tick is the bubbletea `tea.Tick` re-arm and the Update panic barrier,
not sidebar or window work.

`BenchmarkSidebarPanelLinesCached` — steady-state rail compose, nothing changed:
288 ns/op, 0 allocs (an unchanged frame reuses the cache). A forced rebuild is
82000 ns / 178 allocs, so a pane printing output no longer restyles the rail.

## 2026-08 hotspot pass

Profiled with pprof (CPU and allocation) over the render path, the daemon's
per-chunk output path, and the rehydration wire, then fixed what the profiles
pointed at. Every number below was taken on a machine running several other
agents, load average 9 to 46. **Allocation and byte counts are exact and
load-independent; times are directional and carry wide confidence intervals.**
Where a time is quoted it came from `benchstat` over 6+ runs with its p-value.

### What moved

`BenchmarkPTYOutputChunk` / `BenchmarkPTYBroadcast` (new) — the daemon's cost per
chunk of PTY output: catch-up ring append plus subscriber fan-out. `broadcast`
called `debugLog` per chunk and again per subscriber, and the arguments are
evaluated before the flag can be checked, so each call boxed ints into an
`...any` slice and then called `os.Getenv`, which takes the process-wide
environment lock.

| | allocs/op before | after | sec/op |
|---|---|---|---|
| OutputChunk, 0 subscribers | 2 | **0** | -53% |
| OutputChunk, 4 subscribers | 10 | **0** | -61% |
| Broadcast, 1 subscriber | 4 | **0** | -90% |
| Broadcast, 16 subscribers | 34 | **0** | -39% |

`BenchmarkScreenSettleArm` (new) — the agent screen-settle timer, re-armed once
per chunk. The settle scan itself is load-bearing and unchanged: a harness
waiting on a human paints its prompt in its last chunk and then goes silent, so
the throttle alone drops the one look that would see it. Only the arm changed,
from a fresh `time.AfterFunc` per chunk to one timer reset, plus the caller's
closure built once per pane instead of once per chunk.

| | before | after |
|---|---|---|
| allocs/op | 2 | **0** |
| B/op | 128 | **0** |
| sec/op | 247 ns | 68 ns (-72.6%, p=0.002) |

`BenchmarkSidebarPanelCached` (new) — the rail through the call the compositor
makes. The row cache did its job and then `renderSidebar` joined the rows back
into one string on every composed frame, including frames the cache had just
declared unchanged.

| | before | after |
|---|---|---|
| allocs/op | 1 | **0** |
| B/op | 2304 | **0** |
| sec/op | 1910 ns | 930 ns (-51.3%, p=0.002) |

`BenchmarkWireTerminalStateCaughtUp` (new) — the rehydration message, per pane
per workspace switch, at 207x55 with a 1000-row daemon buffer. A switch re-primes
every pane on the target workspace, and the reply carried up to 1000 scrollback
rows that the client discards: its emulator survived, so it keeps its own history
and merges only what scrolled off while it was away. The request now says how
many rows the caller holds and the daemon sends only the rows past it.

| | wire-bytes | allocs/op | encode |
|---|---|---|---|
| before (client behind by 1000, i.e. cold attach) | 2,878,917 | 316,544 | 47.6 ms |
| caught-up (the common switch) | **146,598** | **10,775** | **2.4 ms** |
| behind by 50 | 287,398 | 30,134 | 5.5 ms |

95% fewer bytes and 97% fewer allocations for a caught-up pane. A cold attach is
byte-for-byte unchanged, and version skew is safe both ways. At four panes a
switch drops from ~11.5 MB and ~190 ms of daemon encode to ~0.6 MB and ~10 ms.

### Measured and deliberately not changed

`BenchmarkEventPublishNoSubs` (new) — the control-plane publish every chunk
raises, on a hub every pane shares. 56 ns serial, 110 ns with four panes
publishing at once, 0 allocs. That is 5-10% of what a chunk already costs, and
removing it means making the sequence counter atomic and advancing it outside
the lock, which is what tells a fresh subscriber its baseline. Not worth it.

`BenchmarkBlankFill` (new) — verifies, rather than inherits, the earlier claim
that blanking the rows a scroll brings in is already at memory bandwidth. It
holds, and more strongly than it was put: the per-cell store loop is *faster*
than the alternatives.

| | sec/op | B/s |
|---|---|---|
| `hot/fill-loop` (what the code does) | 473.0 ns ±4% | 45.65 GiB/s |
| `hot/fill-copy` (the previously reverted bulk copy) | 547.1 ns ±5% | 39.46 GiB/s |
| `hot/fill-zero` (pointer-free zeroing) | 440.5 ns ±4% | 49.02 GiB/s |
| `hot/byte-move` (raw `copy()`, same byte count) | 523.1 ns ±5% | 41.27 GiB/s |

The decisive pair is fill-loop against byte-move: the loop moves 207
pointer-carrying 112-byte cells faster than `copy()` moves the same number of
bytes, so no rearrangement of the same stores wins. fill-zero bounds what
removing the pointers could ever buy at 7%. The only lever left is moving fewer
bytes: a smaller `uv.Cell` (upstream) or not blanking eagerly.

`BenchmarkUIPalette` (new) — the chrome palette overlays and the dock ask for:
~1.0 µs under load, 0 allocs, 85% of it the contrast derivation. A composed
frame with 4 panes and the sidebar on makes 4 calls, about 0.3% of a 1.4 ms
frame. Memoising it would add a theme-change invalidation surface for a gain
nobody can perceive.

`renderTerminal`'s builder growth — tried and reverted. The allocation profile's
largest single item by object count (59%) is `strings.Builder.WriteString`, and
the builder is pre-grown to `contentW * contentH` (10,865) while a real frame is
~52 KB of ANSI, so it looks like it must double several times per focused frame.
Sizing the estimate from the previous frame's `CachedContent` instead measured
52,375 -> 52,373 B/op and 374 -> 374 allocs/op over 8 runs: no change, so it was
reverted. The premise was wrong. B/op already equals the finished string, so the
builder allocates its buffer once; the 374 allocations are per-style-run work
inside ultraviolet's `renderLine`, which the profile attributes upward to the
builder because that is where the bytes land. Recorded because the profile line
is genuinely misleading.

`pool.PutStringBuilder` drops the buffer via `Reset`, keeping only the 16-byte
header, which reads like the exact bug this pass was hunting. It is not:
`strings.Builder.String()` returns a string aliasing that buffer, so reusing it
would corrupt strings already handed out. Left alone deliberately.

The rail's signature fold runs even when the sidebar reserves no columns, at
about 0.5 µs per frame. Guarding it is correct but worth 0.006% of a frame at
120 fps, so it is recorded rather than done.

### Invariants held

`BenchmarkIdleTick` after this pass, unchanged from before it:

```
BenchmarkIdleTick-16   0 render/tick   0 work/tick   296 B/op   5 allocs/op
```

No standing `tea.Tick` was introduced. Full `e2e/tui` suite passes (217 s).
