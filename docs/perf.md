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
- `TUIOS_PERF=1 go test ./internal/{terminal,input,app}/ -run TestLatency -v` —
  input latency cut into hops, reported p50/p95/p99/max. See "2026-08 input
  latency" below for what each one includes and excludes.
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
| M3 dock components    | 260 | 296 | 5 | 0.00 | 0 |

`TestIdleCostStaysLow` — boot + 3 windows + 10s idle:

| Milestone | idle wire bytes / 10s | ticks | work | render |
|-----------|-----------------------|-------|------|--------|
| M2 baseline (48c9c51) | 0 | 104 | 104 | 0 |
| M2 idle diet          | 0 | 104 | 1   | 0 |
| M3 dock components    | 0 | 104 | 1   | 0 |

Frame-skip already held at baseline (zero idle renders). The diet's win is
per-tick work: baseline ran the full-window scans on every one of the ~100 idle
ticks; the diet skips them behind a cheap gate, so `work` stays flat while
`ticks` climbs (104 idle ticks, 1 did scan work). The residual ~260 ns / 5
allocs per tick is the bubbletea `tea.Tick` re-arm and the Update panic barrier,
not sidebar or window work.

The dock's components changed neither number, which was the constraint they were
designed under: the refresh engine arms no timer at all unless an interval
component is configured, so a default dock costs what it always cost.
`TestDockIdleCostWithComponentsStaysLow` is the same 10s window with a `once`
and a `push` component loaded, and it measures the same 0 wire bytes and 0 idle
renders.

## What the components deleted

`NeedsDockTick()` used to pin the maintenance tick to `NormalFPS` and mark every
one of those ticks as needing a render whenever the clock or either meter was
on. A clock showing seconds needs one frame a second; the meters need one every
two. Measured on the real binary with `show_clock = true`, one shell, a ~19s
idle window:

| | ticks | work | render |
|---|-------|------|--------|
| before (`NeedsDockTick`) | 1108 | 1108 | 1108 |
| after (clock as a component) | 189 | 0 | 0 |

The tick drops back to `IdleFPS` and does no work at all; the clock's own
redraws come from the component channel at its configured cadence, which is once
a second for a format carrying seconds and once a minute for one that does not.
A component whose value has not changed draws no frame either way.

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

## 2026-08 input latency

Input latency is the most easily perceived number this project has, and until
this pass it could not be decomposed. `e2e/tui/perf_test.go` already timed the
whole echo loop against the real binary, which is the honest end-to-end figure,
but one figure cannot say which hop to go and fix, and it reported min/med/p90
over 16 keystrokes, where a "p99" would have been the maximum relabelled.

Latency is felt at the tail, so everything below is quoted p50/p95/p99/max and
nothing is quoted as a mean. The echo distribution turned out to be visibly
bimodal, and a mean of it names a duration no keystroke ever took.

### The harness

Four measurement points at three altitudes. Each says what it includes, because
a benchmark that omits the daemon is useful only if it admits to doing so.

| where | what it measures | includes | excludes |
|---|---|---|---|
| `internal/terminal` `TestLatencyCoalescer` | pane output to render signal | the render coalescer, alone | daemon, guest, compositor, host |
| `internal/input` `TestLatencyLocal` | a key tuios answers itself, to the frame | key routing, the action, `composeFrame` | daemon, guest, host terminal |
| `internal/app` `TestLatencyEcho` | keystroke to the composed frame carrying its echo | socket, daemon, PTY, guest, ring, broadcast, client emulator, coalescer, compose | host terminal, bubbletea stdin decode, the diff written to the tty |
| `internal/app` `TestLatencyDaemonRoundTrip` | keystroke to the client's own emulator | everything above except compose | the compositor |
| `internal/app` `TestLatencyFrameEmit` | pane output to the render signal, on the rig | coalescer with a real guest in front of it | compose |
| `internal/app` `TestLatencyStateSync` | the state push every key pays for | build, gob encode, socket write | — |
| `e2e/tui` `TestPerfInputLatency` | the whole loop, real binary in a real PTY | everything, including the host | nothing |

All in-process measurements run at 207x55 with n=200 (n=500 for the local ones,
n=300 for the coalescer), so a quoted p99 is a keystroke that really happened.

```
go test ./internal/terminal/ -run TestLatencyCoalescer -v      # needs TUIOS_PERF=1
go test ./internal/input/    -run TestLatencyLocal      -v     # needs TUIOS_PERF=1
go test ./internal/app/      -run TestLatency           -v     # needs TUIOS_PERF=1
cd e2e/tui && TUIOS_E2E=1 TUIOS_PERF=1 go test -count=1 -v -run TestPerf ./...
```

`internal/perf` holds the shared `Dist`, so the e2e numbers and the in-process
ones are the same quantiles computed the same way and can be put side by side.
Quantiles are nearest-rank rather than interpolated: an interpolated p99 invents
a duration nobody experienced, and the question here is which real keystroke was
the slow one.

### Measurement conditions

Another agent was bisecting a compositor regression on this machine for much of
this pass, and load average ranged from 0.4 to 25. **Every before/after pair
below was taken in the same quiet window (load 0.9 to 4.1), back to back, by
checking the old implementation out and re-running the same harness.** Numbers
taken under load are not compared against numbers taken without it. Counts and
allocations are load-independent and are quoted as exact.

### Where the time went

Before anything was changed, at 207x55 (load under 2):

| hop | p50 | p99 |
|---|---|---|
| key routing, 4 panes | 574 ns | 916 ns |
| `SyncStateToDaemon`, per key, 4 panes | 17.7 µs | 29.7 µs |
| daemon round trip, key to client emulator | 1.15 ms | 1.29 ms |
| **render coalescer, quiet pane** | **5.01 ms** | **8.07 ms** |
| compose one frame, 4 panes | 4.44 ms | 5.16 ms |
| echo to composed frame, 1 pane | 9.75 ms | 10.26 ms |
| echo to composed frame, 4 panes | 9.52 ms | 10.76 ms |

Three things fall out of that table.

**The wire is not the problem.** The daemon round trip, which is the part a
multiplexer adds over a bare terminal and therefore the part worth defending,
is 1.15 ms at p50 and barely moves at the tail. It is about a tenth of the echo.

**Routing costs nothing.** 574 ns against a 4.44 ms frame is 0.013%. Deciding
what a key means is free; drawing the result is the entire local cost.

**The coalescer was half the echo.** `renderCoalescer` polled a flag on a
free-running 8 ms ticker, so output was shown at the next tick edge regardless
of what the pane had been doing. That charged a pane silent for a minute the
same wait as one mid-flood, and a silent pane is exactly the state a pane is in
when a user types at it.

The coalescer's distribution is the clearest evidence in this pass:

```
coalescer/quiet pane output -> signal   n=300  min 16.6µs  p50 4.03ms  p95 8.03ms  p99 8.04ms  max 8.08ms
```

A textbook uniform distribution over [0, 8 ms]. Note the sampling detail: a
fixed quiet period between samples locks every one to the same tick phase and
reported min 6.01 ms / max 7.09 ms, which reads like a tight well-behaved hop
and is one phase measured 300 times. The jitter in the sample loop is what makes
the number honest.

### What changed

`renderCoalescer` now emits on the leading edge and rate-limits after it. The
cap it exists for is unchanged, at most one render per 8 ms, so a flooding pane
still cannot make the compositor draw partial frames; what goes away is the wait
for a pane with nothing to coalesce against.

Matched runs, same quiet window:

| | before p50 | after p50 | before p99 | after p99 |
|---|---|---|---|---|
| coalescer, quiet pane | 5.01 ms | **20.1 µs** | 8.07 ms | **63.6 µs** |
| frame emit, on the rig | 2.08 ms | **98.9 µs** | 2.25 ms | **199.2 µs** |
| echo, 1 pane | 9.75 ms | **1.78 ms** | 10.26 ms | **2.15 ms** |
| echo, 4 panes | 9.52 ms | **2.54 ms** | 10.76 ms | **3.31 ms** |
| daemon round trip | 1.15 ms | 1.16 ms | 1.29 ms | 1.32 ms |

The daemon round trip is the control: it is not on the changed path and it did
not move, which is what says the rest of the table is the coalescer rather than
the weather.

Whole binary, `e2e/tui`, same quiet window, n=200:

| | before p50 | after p50 | before p99 | after p99 |
|---|---|---|---|---|
| 1 pane | 9.05 ms | **8.16 ms** | 17.61 ms | **9.64 ms** |
| 4 panes | 15.77 ms | **8.15 ms** | 17.44 ms | **16.41 ms** |
| 8 panes | 16.67 ms | 16.62 ms | 24.66 ms | **17.63 ms** |
| typing, 1 pane flooding | 16.12 ms | **9.65 ms** | 18.00 ms | 20.00 ms |
| typing, 3 panes flooding | 29.49 ms | 28.54 ms | 50.23 ms | 54.30 ms |

The end-to-end win is real but smaller than the in-process one, because the
whole-binary path carries terms the in-process harness excludes (see below). At
eight panes and under a three-pane flood the compositor dominates and swamps the
coalescer's contribution entirely, which is consistent with everything else
here.

Idle cost improved rather than regressed. The old coalescer was a standing
ticker per daemon window, so every open pane woke 125 times a second for the
life of the process whether or not it had anything to draw; the timer is now
armed only when there is output. `BenchmarkIdleTick` is unchanged at `0
render/tick, 0 work/tick, 296 B/op, 5 allocs/op`, and `TestIdleCostStaysLow`
reports 0 wire bytes over 10 s with `ticks=104 work=0 render=0`.

### Measured and deliberately not changed

**A keystroke to a pane composes a frame worth nothing.** bubbletea composes
after every message, and `Update` forces a fresh frame for every key ("Any user
input must produce a fresh frame"). For a key forwarded to a daemon pane, that
frame is composed before anything has changed: the bytes went out on the socket,
the guest has not answered, and the pane holds what it held. The echo composes a
second frame later, and that is the one carrying it.
`TestKeystrokeToPaneComposesAnIdenticalFrame` pins that the first frame is
byte-identical to the one before the key, so this is waste rather than latency,
and it is a count so it holds whatever else the machine is doing. It costs 1.66
ms at one pane and 4.44 ms at four, and because it runs on the Update goroutine
it also delays the echo message queued behind it.

This is now the largest remaining term and it is left alone deliberately. Fixing
it means letting `internal/input` tell `Update` that a key was forwarded
verbatim and changed nothing else, so the compose can be skipped. The set of
keys that *do* change something locally is large and easy to get wrong (mode
changes, prefixes, overlays, copy mode, showkeys, which literally draws the key
you pressed), and getting it wrong produces the worst class of bug this project
can ship: a character that does not appear until something else redraws. It
wants a damage-tracking design and a verification pass on the real screen, not a
predicate bolted onto a latency fix.

**The compositor has no hotspot to delete.** A CPU profile over `GetCanvas` at
207x55 attributes 37.8% cumulative to `ansi.stringWidth` and its grapheme
cluster iteration, 26.0% to `ultraviolet.StyledString.Draw`, and 34.7% to
`renderWindowBox` as the caller. It is upstream text shaping reached through the
ordinary path, not a tuios routine sitting on the critical section. There is no
single change that makes a frame meaningfully cheaper, which is why the lever is
composing fewer frames rather than faster ones. Note also that
`BenchmarkCompositorGetCanvas` at nine windows costs 1.00 ms even when only one
window is dirty: that is the per-frame floor that damage cannot avoid.

**`roundTripMu` is not on the keystroke path.** It was the first thing suspected
and the suspicion is wrong. `WritePTY` does not take it; it guards only attach,
PTY create, session list and terminal state. What it does bound is the
head-of-line case: the daemon dispatches inline on the connection goroutine, so
a keystroke can sit unread in the socket buffer behind a `MsgGetTerminalState`
issued on the same connection, which is what a workspace switch does. That is a
real stall but it is a switch-time stall, not a typing-time one, and it is
bounded to one outstanding round trip by the mutex being blamed for it.

**`SyncStateToDaemon` per keystroke is not worth touching.** `internal/input`
calls it after every key on a daemon session, synchronously on the Update
goroutine: a full session-state build, a gob encode, and a blocking socket write
under the client mutex. It reads like an obvious problem and it is 17.7 µs at
p50 with four panes, which is 0.4% of the frame it shares a keystroke with. It
does spike (p99 reached 312 µs at one pane and 802 µs at eight, presumably the
write blocking), but not often enough or far enough to be worth the invalidation
surface that skipping unchanged pushes would add.

**Per-keystroke `debugLog` in `handleInput`.** The daemon's input handler calls
`debugLog` per keystroke, which evaluates its arguments into an `...any` slice
and calls `os.Getenv` (a process-wide lock) before the flag can be checked. This
is the same pattern the 2026-08 hotspot pass fixed in `broadcast`, still present
here. Left alone: a chunk's whole cost is ~100 ns, so this is noise against a
millisecond-scale budget, and it is recorded only so the next person does not
rediscover it and assume it matters.

**Four write syscalls per keystroke.** `WritePTYInput` writes the length, the
header, a 36-byte padded id and the payload as four separate `conn.Write` calls
on an unbuffered socket, for a one-byte keystroke with 42 bytes of framing. The
daemon round trip measures 1.15 ms end to end including the guest, so whatever
the extra syscalls cost is inside that and is not what makes typing feel slow.
Recorded rather than done.

### Invariants held

```
BenchmarkIdleTick-16   0 render/tick   0 work/tick   296 B/op   5 allocs/op
```

No standing `tea.Tick` was introduced, and one standing per-pane `time.Ticker`
was removed. No blocking daemon round trip was moved onto the Update goroutine.
`TestIdleCostStaysLow`: 0 idle wire bytes over 10 s, `ticks=104 work=0
render=0`. Full `e2e/tui` suite passes.

## 2026-09 input and event loop

Profiled the input path with pprof (CPU and allocation) over the key a person
types most, a plain letter into a focused shell, and measured the idle process
with `/proc` context-switch counters and a CPU profile of the real binary. The
2026-08 pass measured a window-mode key and concluded routing costs nothing;
that key reads the flattened keymap, and the typed letter takes a different
road.

Four agents shared the machine for most of this pass. **Every A/B below was
taken under the bench lock, interleaved A, A-again, B for six rounds (three
for the latency and idle rows), with the A-again column as the noise floor.**
Load was 2 to 8 during the runs and the timing noise floor is wide (benchstat
reports ±30% to ±120% on A versus A-again, p>0.18 on every row); allocation
counts, frame counts and context-switch counts are exact. No time below is
quoted as a win unless it is an order of magnitude past that floor.

### Where the time went

`BenchmarkKeyTerminalTyped` (new), one letter through `HandleInput` to the
pane's writer, before anything changed:

| key | ns/op | allocs/op |
|---|---|---|
| letter typed into a shell | 18,900 | 177 |
| ctrl chord into a shell | 28,300 | 186 |
| leader then prefix key | 36,000 | 325 |
| Tab in window mode (what 2026-08 measured) | 650 | 7 |

95% of the allocations were `KeyNormalizer.ExpandKeys` under
`KeybindRegistry.sectionKeyMap`. Every lookup in a prefix, global, script,
terminal-mode or rail section rebuilt the section from the config: sort the
action names, normalize every key, allocate a map, read one key, drop the map.
A typed letter passes the terminal-mode section twice, the global section and
the main map on its way to the PTY, and each gate tries two spellings, so one
keystroke was eight rebuilds.

### What changed

**The registry resolves each section once** (`buildMappings`, rebuilt by
`Reload`, which every in-place editor already calls).

| | before | after | noise (A vs A) |
|---|---|---|---|
| typed letter, allocs/op | 177 | **14** | exact |
| typed letter, sec/op | 56.8 µs | **3.7 µs** (-93%, p=0.002) | ±105% |
| prefix chord, allocs/op | 325 | **10** | exact |
| prefix chord, sec/op | 100.6 µs | **2.8 µs** (-97%, p=0.002) | ±79% |
| Tab in window mode (control) | 1.5 µs / 7 allocs | 2.2 µs / 7 allocs | within noise (p=0.31) |

`TestLatencyTypedKey` (new), the letter as a distribution, n=500, three runs
each:

| | p50 | p99 |
|---|---|---|
| before | 25.3 / 29.7 / 27.9 µs | 72 µs / 1.74 ms / 102 µs |
| before, again (noise) | 28.9 / 32.9 / 25.9 µs | 1.71 ms / 652 µs / 1.01 ms |
| after | **1.5 / 1.5 / 1.5 µs** | **3.0 / 2.6 / 3.2 µs** |
| window-mode key, control, before | 1.3 / 1.6 / 1.3 µs | 3.0 / 2.6 / 3.8 µs |
| window-mode key, control, after | 1.5 / 1.9 / 1.4 µs | 6.7 / 4.1 / 6.7 µs |

The control's p50 spread across all nine runs (1.3 to 1.9 µs) is the noise
floor; the typed key's p50 fell twenty times past it, and its p99 tail (which
reached 1.7 ms on two of the six baseline runs) is gone.

**The motion filter asks the pane for a link instead of passing every cell.**
The link clause passed every motion over any pane's content box, and bubbletea
composes a frame after each: a pointer sweep across an idle shell cost one full
compose per cell crossed. `BenchmarkPointerSweep/content/filtered` (120x40,
four blank panes) and `BenchmarkMouseSweepContent` (new; 207x55, a pane full of
plain text, links = all):

| | frames/event before | after | sec/op before | after |
|---|---|---|---|---|
| sweep, 120x40 | 0.966 | **0** | 3.63 ms | **406 ns** |
| sweep, 207x55 plain text | 1 per cell | **0** | 5.50 ms | **10.8 µs** (the bare-URL row scan) |

The filter now answers the question the handler was going to ask anyway
(`PointerOverLink`: one cell read for an OSC 8 link, one row scan for a bare
URL) and passes the motion only when there is a link. Three things had ridden
the old clause with no clause of their own, and were dead with `links = off`
or over the chrome: a ctrl-click grab waiting to become a drag, zen mode's
mouse variant, and the dock's session-control hover and workspace-pill
tooltips, which no clause passed on any client. Each has a clause and a test
now. A floating pane spawns at the pointer's last reported position, which the
filter records for every motion, so the spawn no longer depends on which
hover last let an event through.

`BenchmarkMouseMotionHover` moved the other way (0.49 µs to 5.2 µs, 1 to 4
allocs): it measures the filter plus `Update` with no frame, over content with
no link, so it now pays the row scan and saves a compose it never counted.

**The render ticker runs at the configured max_fps.** bubbletea flushes frames
from a standing ticker for the life of the program, pending frame or not, and
tuios set its rate to the ceiling `max_fps` is clamped to, which bubbletea caps
at 120. Real binary, one idle shell at 207x55, 10 s, `/proc` counters:

| | voluntary ctx switches / s | CPU |
|---|---|---|
| before (ticker at 120) | 583 / 565 / 586 | 1.0 / 1.0 / 1.1% |
| before, again (noise) | 607 / 615 / 584 | 1.1 / 0.9 / 1.1% |
| after (ticker at the default 60) | **364 / 376 / 358** | **0.6 / 0.7 / 0.7%** |

Attributed by building the binary at 120, 60 and 10: 590, 369 and 118 switches
a second, 1.0%, 0.6% and 0.2%. The residual at 10 is tuios's own 10 Hz idle
tick plus the runtime. **This is the one change in the pass that alters a
documented behaviour**: raising `max_fps` above the value the client started
with now takes effect at the next start (the settings row says so). It is its
own commit so it can be reverted alone. The real fix is upstream, a bubbletea
ticker that idles when nothing is pending.

**Keys typed right after entering terminal mode reach the pane.**
`HandleTerminalModeKey` dropped every unmodified printable key for 150 ms
after entering terminal mode, a guard against mouse-sequence fragments from a
host mouse-mode switch the client no longer makes (the view holds all-motion
tracking for the whole session). On the real binary a line typed 0 ms or 50 ms
after pressing `i` never reached the shell; after 500 ms it did. The guard is
gone and all three reach it. Not a performance change; recorded here because a
silently dropped keystroke is the input bug that matters most.

### Measured and deliberately not changed

`bindingKeys` is computed once per gate rather than once per key: four times
for a typed letter, 8 of the 14 remaining allocations, about 0.4 µs. Threading
one spelling list through the gates is an API change for 0.02% of the frame
that follows the key.

`SendInput`'s `debugLogf` evaluates `time.Now().Format`, `string(input)` and
the hex form of the input before the flag can be checked, three of the 14
allocations. `internal/terminal`, handed to that pass.

`ApplyReloadedConfig` never calls `KeybindRegistry.Reload`, so keybindings do
not follow a config-file reload on any road. Pre-existing, unchanged by the
section cache (the flattened map was already frozen the same way), and outside
a performance pass. Reported rather than fixed.

### Invariants held

```
BenchmarkIdleTick-8   0 render/tick   0 work/tick   296 B/op   5 allocs/op   (n=6, A and B)
```

No standing `tea.Tick` was introduced. The bubbletea render ticker was slowed,
not added. Negative controls: seven, all valid, each a mutation of shipped code
that fails a named assertion (`TestMotionFilterPassesPaneContentForLinks`,
`TestMotionFilterPassesACtrlDragGrab`, `TestMotionFilterFeedsZenMouseMode`,
`TestMotionFilterRecordsThePointerItDrops`, `TestMotionFilterPassesTheDockBand`,
`TestProgramOptionsReachTheProgram`,
`TestKeysTypedRightAfterEnteringTerminalModeReachThePTY`).

## 2026-09 daemon and wire pass

What `internal/session` costs between the PTY and the client's emulator,
measured on a real daemon over a real unix socket with a real shell in the
pane. The harness is `internal/session/wire_e2e_bench_test.go`: it starts a
daemon, attaches a `TUIClient`, subscribes to one pane and counts every read
and write on the client's socket, so a syscall per keystroke is a number and
not an inference.

```
go test ./internal/session/ -run '^$' -bench 'E2E'                     # keystroke echo, flood
go test ./internal/session/ -run '^$' -bench 'WireTerminalStateApply'  # the snapshot, both sides
go test ./internal/session/ -run '^$' -bench 'SnapshotPack'            # the packed form alone
```

### Measurement conditions

Timings are rounds under the bench lock with `nice -n 10 taskset -c 0-7`,
compared with `benchstat`. Each round runs old, new, new, old. That order
cancels any drift across the round, and the two old runs sit at the widest
positions apart, so old against old is an upper bound on what position and
ambient load alone can produce.

That floor is what decides which timings are quoted:

- Client decode and apply: old against old is -2.5% by geomean and neither
  case is significant (p=0.21 and p=0.27), on 18 samples a side. The measured
  effect is -36% to -44%. It is quoted.
- Daemon snapshot build: old against old is -14.5% by geomean, and two of the
  three cases come out "significant" on identical code (p=0.011 and p=0.019).
  The effect is the same size as that floor, so **no daemon-side timing is
  quoted here**. The allocation counts carry that claim instead, and they are
  exact.

The benchmark is GC-bound, which is why its floor is so much wider than the
client's: one op churns 23 MB, and when the collector runs depends on what
else has the machine.

Counts, allocations and wire bytes do not move with load and are exact.

### Where the time went

**The flood is the emulator's.** A pane printing 16 MiB as fast as it can into
one client spends 84% of the process in `internal/vt` parsing those bytes on
the daemon side, 3.3% in `readOutput` (ring append, broadcast, event publish)
and 1.6% in `streamPTYOutput` (batch and socket write). The wire is not where a
flood is slow, and nothing in this package was changed for it.

**The keystroke is syscalls and scheduler.** The echo of one key, client write
to client handler, is 33% in syscalls and most of the rest in goroutine
wakeups. The client read three times per frame: the length prefix, the two
header bytes and the payload, each straight off the socket.

**The snapshot is where the wire is expensive.** `TerminalState` carries its
cells as `[][]CellState`, and gob writes every cell as a struct, so a 207x55
screen is 147 KB and a thousand rows of history 2.9 MB, per pane, and the
client decodes every one of those structs by reflection on its UI goroutine
during a workspace switch and then resolves each cell's colours through
`fmt.Sscanf`. One palette pane cost the client 11 ms per switch on the box
these numbers were taken on, before any of its cells were painted, and a pane
with a thousand rows of history behind it cost 123 ms.

### What moved

**The snapshot's cells travel packed** (`snapshot_pack.go`). A style table
once, cells in one style run together, two bytes per plain letter, blank row
tails left off. Negotiated per request with `GetTerminalStatePayload.Packed`,
so no protocol bump: a daemon that predates the field answers with cells and a
client that predates it never asks. The client unpacks the reply as soon as it
is decoded, so everything past `TUIClient.GetTerminalState` still reads cells.
Wire bytes per pane, at 207x55, exact:

| | cells | packed | |
|---|---|---|---|
| palette colours, screen only (a workspace switch) | 146,716 | **22,749** | 6.4x |
| palette colours, 1000 rows of history (a cold attach) | 2,879,037 | **416,713** | 6.9x |
| truecolor in runs of eight, screen only | 195,611 | **42,149** | 4.6x |
| truecolor in runs of eight, 1000 rows | 3,704,103 | **587,539** | 6.3x |
| a different colour on every cell, screen only | 195,611 | 170,850 | 1.1x |

The last row is the worst case for any style table and is quoted so nobody
expects the packed form to help an image viewer's pane.

The gob decode alone, cells against packed in one build, is 11,161 allocations
to **435** for a palette screen and 206,965 to **580** for a thousand rows of
history.

What the client pays for a whole snapshot, decode and apply together, is the
number a workspace switch waits on. Old cells against new packed:

| | before | after | |
|---|---|---|---|
| palette colours, screen only | 11.25 ms | **7.25 ms** | -36% (p<0.001) |
| palette colours, 1000 rows of history | 123.3 ms | **68.8 ms** | -44% (p<0.001) |

Allocations for the same two: 23,113 to **889**, and 427,712 to **4,035**.
Bytes allocated fall by a third. The two timings are taken on a loaded box, so
read the ratio and not the absolute figures. The noise floor for that pair, on
identical code, is -2.5% and not significant.

**Colours are parsed and printed by hand.** `colorFromWire` used `fmt.Sscanf`
per cell and `colorToWire` built a string per styled cell. Palette strings are
now built once for the process, RGB strings once per distinct colour per
snapshot, and the hex is read by hand. A client answered by an older daemon
gets this part too, because it does not depend on the packed form.

**The apply path allocates nothing per cell.** `stateToCell` returned a fresh
`*uv.Cell` for every cell, and both emulators copy what they are handed. One
cell now serves a whole snapshot.

Those two, with the pooled rows in `TerminalStateOf`, are what the daemon side
gained. Building and encoding one snapshot in the cell form is what an older
client is still answered with, and its allocations are:

| | before | after | |
|---|---|---|---|
| screen only | 10,775 | **107** | -99.0% |
| 100 rows of history | 43,695 | **118** | -99.7% |
| 1000 rows of history | 510,544 | **194,130** | -62.0% |

Bytes allocated fall 3.7% to 5.7% over the same three. The wall time falls too,
but by no more than this benchmark's own noise floor, so it is not quoted. See
"Measurement conditions". The depth-1000 count is still large because
`ScrollbackLine` builds the cells it hands back, which this pass did not touch.

One cost, not a saving: the four new fields put 102 more bytes of gob type
description into every snapshot, packed or not. That is 0.07% of a screen.

**The client reads a frame in one syscall.** `TUIClient` reads through a
`bufio.Reader`. `TestClientReadsAFrameInOneSyscall` counts 40 echoed keystrokes
at one read each; make the client read the socket directly again and the same
test counts 120 reads for the same 40 frames, which is the length prefix, the
two header bytes and the payload each on their own. The keystroke's wall time
is within noise on this box.

**Neither read loop polls.** Both loops waited between frames with a 100 ms
deadline so they could look at their done channels, ten wakeups a second per
side per connection, forever, and every one of them a read that returned
nothing. Neither needed it: everything that closes those channels closes the
connection with them. `TestIdleConnectionMakesNoReads` holds an idle client at
zero reads on its socket and the process under ten read syscalls over 1.5 s.
Put either deadline back and the same test counts 15 client reads and 20
process reads in that 1.5 s, which is the ten-a-second poll.

**A state sync from one client no longer builds the merged state unless
something reads it**, which is the reconcile reply and the peer broadcast; the
one-client accepted case, which is nearly every sync, skipped a full state
copy. **A broadcast encodes once** instead of once per peer goroutine.

### Measured and deliberately not changed

- **Per-chunk copies on the daemon.** A chunk is copied out of the read buffer,
  into the ring, into the batch and into the frame. In the flood profile all
  of `readOutput` is 3.3% and `writePTYFrame` 1.2%; removing a copy would not
  show.
- **The per-chunk event publish and settle timer.** Already priced in the
  2026-08 pass; invisible in the flood profile behind the emulator.
- **The client's second copy of every pane's history.** Still there. The
  packed form makes the cold attach seven times smaller but does not change
  who holds what. `docs/REHYDRATION.md` sketches fetch-on-scroll, which is the
  change that would; it is not built.

### Invariants held

```
BenchmarkIdleTick-8   0 render/tick   0 work/tick   296 B/op   5 allocs/op
```

`ProtocolVersion` is still 3 and `VerbProtocolVersion` still 1. Every message
type keeps its number. `TestWireCarriesTheWholeCell` and the ghostty wire
matrix both run every shape under both cell forms.
`TestOlderPeerReadsTheWire` covers the two skews this package cannot build from
its own daemon and client: an older daemon reads the new request, and a newer
client reads the older answer.

## 2026-09 client draw path

Profiled `composeFrame` plus the diff-and-emit half of a client frame
(`frameSink`, see `frame_pipeline_bench_test.go`) for the frame a multiplexer
draws most: one character typed into one of N panes, the rest idle
(`BenchmarkKeystrokeFrame`, `BenchmarkKeystrokeFrameTiled`). Half of that frame
was the compose, and more than half of the compose was work the frame did not
need: lipgloss's Compositor measured every layer's string twice per frame, then
re-parsed every layer's string into cells, the unchanged panes included, onto a
canvas that paid a damage comparison per cell for a buffer rebuilt from scratch
each frame. Turning the cells back into the frame string cost a fresh SGR diff
per style change and two more copies to trim it.

### What changed

`composeLayers` (`compose.go`) replaces the Compositor and the Canvas. It keeps
the Compositor's order, root and unstable sort included, and draws each layer
from a `cellLayer`: the cells its string parsed to the last time it was seen,
kept by layer id while the layer is on screen. A pane's layer keeps its string
between keystrokes in other panes, so it is parsed once per rebuild and copied
afterwards, a row copy when its edges meet no wide cell. `frameRenderer`
(`frame_render.go`) writes the same bytes as `Lines.Render` plus `TrimSpace`,
remembering the diff for each pair of styles and trimming each line as it is
written. The output is identical: `TestComposeLayersRandomMatchesCompositor`
and `TestFrameRenderMatchesUltraviolet` draw random inputs through both.

The focused pane's cell loop no longer holds a `*uv.Cell` across the next
`CellAt` call; it keeps the previous style as a value. Same output
(`TestRenderTerminalKeepsEveryStyleRun` parses the frame back and compares
every cell), and it lets the VT layer stop handing out stable cell addresses.

### Numbers

Taken against main at 9b640746, twenty rounds of A, B and a second A
interleaved in one session on an otherwise idle machine, `nice -n 10 taskset -c
0-7`. The noise floor is the same main binary against itself across those
rounds: every benchmark reads `~`, intervals of +/-6% to +/-25%, geomean
+2.65%. Every figure below clears that floor with p <= 0.001. Allocation counts
do not move with load: on the noise floor every one of them is identical.

| Benchmark (207x55) | main | branch | |
|---|---|---|---|
| `CompositorGetCanvas/windows-9/one-dirty` | 862 us | 149 us | -83% (p<0.001, n=20) |
| `CompositorGetCanvas/windows-4/one-dirty` | 966 us | 239 us | -75% (p<0.001) |
| `CompositorGetCanvas/windows-1/one-dirty` | 1.52 ms | 708 us | -53% (p<0.001) |
| `CompositorGetCanvas/windows-9/all-dirty` | 1.57 ms | 890 us | -43% (p<0.001) |
| `ClientFrame/panes-9/compose` (flood) | 10.12 ms | 6.77 ms | -33% (p<0.001) |
| `ClientFrame/panes-9/whole` (flood) | 17.65 ms | 14.92 ms | -15% (p<0.001) |
| `KeystrokeFrame/panes-9` (compose+emit) | 3.72 ms | 2.52 ms | -32% (p<0.001) |
| `KeystrokeFrame/panes-1` | 4.13 ms | 3.29 ms | -20% (p=0.001) |
| `KeystrokeFrameTiled/panes-9` | 4.10 ms | 2.81 ms | -31% (p<0.001) |
| `IdleTick` | | | unchanged |

The cell loop was measured on its own, forty rounds against a +/-1% to +/-8%
floor, because the style-as-a-value change touches every cell of the focused
pane. Every `RenderTerminalReal` case reads `~`: geomean -1.55% against a
-1.83% floor for main against itself. It costs nothing and allocates nothing
extra.

| Allocations per frame | main | branch |
|---|---|---|
| `KeystrokeFrame/panes-1` | 2853 allocs | 1800 allocs |
| `KeystrokeFrame/panes-9` | 4228 allocs | 1417 allocs |
| `KeystrokeFrameTiled/panes-9` | 4027 allocs | 2196 allocs |
| `CompositorGetCanvas/windows-9/one-dirty` | 138 allocs | 111 allocs |
| `ClientFrame/panes-9/compose` (flood) | 79.2k allocs | 42.5k allocs |
| `ClientFrame/panes-9/whole` (flood) | 98.2k allocs | 61.9k allocs |
| `RenderTerminalReal/*` (cell loop) | unchanged | unchanged |

`bytes/frame` is the same on both sides of every benchmark that reports it, so
the frame that leaves the client is unchanged.

### What it costs

The cells are kept, so the client holds more. Nine panes at 207x55, three
thousand keystroke frames, live heap after two collections:

| | main | branch |
|---|---|---|
| live heap | 10.81 MiB | 12.51 MiB |
| resident | 42-51 MiB | 46-55 MiB |

The live heap is +1.7 MiB and repeats to within 1 KiB across runs. Resident sits
well above live heap on both sides and the two ranges overlap. That gap is Go
headroom at the default GOGC, not a leak. The cache holds only the layers on the
current frame, so it grows with the panes on screen and not with the panes that
exist.

The whole keystroke frame moved less than the compose did, because the compose is
only half of it. The other half is bubbletea's renderer: it parses the frame
string into cells again, and its `cellbuf.Clear()` touches every line, so the
diff's `transformLine` runs on all 55 rows each frame and `lineHasDrift` calls
`StringWidth` twice per cell on each of them. In the keystroke profile that one
function was 37% of the emit half and about a fifth of the frame. It lives in
ultraviolet's `TerminalRenderer`, outside this repo.

### Invariants held

```
BenchmarkIdleTick-8   0 render/tick   0 work/tick   296 B/op   5 allocs/op
```
