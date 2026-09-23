# Performance baselines

Idle cost is the number every milestone's Gate defends. "Low idle" (see the M2
plan) is: one attached client, sidebar on, N idle shells, clock off => zero
timer-driven renders, bounded per-tick work, no session-list polls without a
visible consumer, idle CPU under ~0.5%.

## How to measure

- `go test ./internal/app/ -run '^$' -bench BenchmarkIdleTick -benchmem`: work,
  allocations, and ns per maintenance tick at idle. `work/tick` is the fraction
  of ticks that ran the full-window maintenance scans; at idle it must trend to
  zero.
- `go test ./internal/app/ -run TestIdleTickSkipsScans`: asserts idle ticks
  take the skip path (no scan work), read from the `tickStats` counter.
- `TUIOS_PERF=1 go test ./internal/{terminal,input,app}/ -run TestLatency -v`:
  input latency cut into hops, reported p50/p95/p99/max. See "2026-08 input
  latency" below for what each one includes and excludes.
- `TUIOS_E2E=1 go test ./e2e/tui/ -run TestIdleCostStaysLow`: boots the real
  binary, opens three idle shells, idles 10s, and asserts the app writes
  ~nothing to the wire (render count bounded). `TUIOS_STATS_FILE` makes the
  process dump its tick counters on clean exit.

## Numbers

`BenchmarkIdleTick`: 3 idle daemon windows, one tick per op:

| Milestone | ns/op | B/op | allocs/op | work/tick | render/tick |
|-----------|-------|------|-----------|-----------|-------------|
| M2 baseline (48c9c51) | 470 | 568 | 9 | 1.00 | 0 |
| M2 idle diet          | 260 | 296 | 5 | 0.00 | 0 |
| M3 dock components    | 260 | 296 | 5 | 0.00 | 0 |

`TestIdleCostStaysLow`: boot + 3 windows + 10s idle:

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

`BenchmarkSidebarPanelLinesCached`: steady-state rail compose, nothing changed:
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

`BenchmarkPTYOutputChunk` / `BenchmarkPTYBroadcast` (new): the daemon's cost per
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

`BenchmarkScreenSettleArm` (new): the agent screen-settle timer, re-armed once
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

`BenchmarkSidebarPanelCached` (new): the rail through the call the compositor
makes. The row cache did its job and then `renderSidebar` joined the rows back
into one string on every composed frame, including frames the cache had just
declared unchanged.

| | before | after |
|---|---|---|
| allocs/op | 1 | **0** |
| B/op | 2304 | **0** |
| sec/op | 1910 ns | 930 ns (-51.3%, p=0.002) |

`BenchmarkWireTerminalStateCaughtUp` (new): the rehydration message, per pane
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

`BenchmarkEventPublishNoSubs` (new): the control-plane publish every chunk
raises, on a hub every pane shares. 56 ns serial, 110 ns with four panes
publishing at once, 0 allocs. That is 5-10% of what a chunk already costs, and
removing it means making the sequence counter atomic and advancing it outside
the lock, which is what tells a fresh subscriber its baseline. Not worth it.

`BenchmarkBlankFill` (new): verifies, rather than inherits, the earlier claim
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

`BenchmarkUIPalette` (new): the chrome palette overlays and the dock ask for:
~1.0 µs under load, 0 allocs, 85% of it the contrast derivation. A composed
frame with 4 panes and the sidebar on makes 4 calls, about 0.3% of a 1.4 ms
frame. Memoising it would add a theme-change invalidation surface for a gain
nobody can perceive.

`renderTerminal`'s builder growth: tried and reverted. The allocation profile's
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

The string builder pool that `renderTerminal` once used dropped the buffer via
`Reset`, keeping only the 16-byte header. Reusing the buffer would be wrong:
`strings.Builder.String()` returns a string aliasing that buffer, so reusing it
would corrupt strings already handed out. Since the pool kept nothing of value,
it was removed in favour of a local `strings.Builder`, with no change in
allocs/op on `BenchmarkRenderTerminalReal`.

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
| `internal/app` `TestLatencyStateSync` | the state push every key pays for | build, gob encode, socket write | nothing |
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

## 2026-09 vt parse and scroll pass

Profiled `internal/vt` over the SGR-heavy repaint (`BenchmarkBackendDoomFire*`),
the short-line and long-line scroll floods (`BenchmarkEmulatorShortLineScroll`,
`BenchmarkEmulatorScrollThroughput`) and the log replays
(`BenchmarkEmulatorWriteHeavyOutput`), then took each hotspot as its own commit
so a bad one can be reverted alone.

### Measurement conditions

The machine was shared with other agents, load average 13 to 18 on 11 cores,
and wall time swung by +/-100% between identical runs. So every figure below is
process CPU time (user plus sys) per iteration: each benchmark runs at a fixed
`-test.benchtime Nx` with `-test.cpu 1`, old and new binaries alternate, six
rounds a side, compared with `benchstat`. CPU time still includes the process
start and the benchmark setup, which dilutes a change towards zero but never
inflates it. Allocation counts are exact.

The noise floor is the base binary against a copy of itself under the same
alternation: every one of the fifteen benchmarks reads `~` (p=0.37 to 1.00),
geomean -1.0%, with intervals of +/-4% to +/-43%. A figure is quoted below
only where the change clears that with p < 0.05.

### What moved

**CSI parameter bytes skip the transition table** (`parser.go`). Every byte of
`38;2;r;g;b` went through `parser.Table.Transition` and the `performAction`
switch to reach three lines of arithmetic, and in a truecolor repaint those
bytes are most of the stream. `advance` now does the digit, `:` and `;` update
directly when the state is `CsiParamState`, which is exactly what the table
does for 0x30 to 0x3B there. `TestSeqParserCsiParamsMatchUpstream` feeds random
CSI input to this parser and to upstream's and requires the same action, state
and dispatch for every byte.

| CPU per op | before | after | |
|---|---|---|---|
| `BackendDoomFire158x40` | 99.4 ms | 82.0 ms | -17.5% (p=0.002) |
| every other vt benchmark | | | `~` |

**A plain letter enters the scrollback as its byte** (`scrollback.go`).
`encodeLine` ran `packStyle` and a two-string link compare on every cell of a
line leaving the screen, about 20 ns a cell, to find out nothing had changed.
While no style or link is in force, a narrow unstyled ASCII cell now appends its
byte directly, which is exactly what the full path writes for it.
`TestEncodeLinePlainShortcutWritesTheSameBytes` compares the output against the
full path on random mixed lines.

| CPU per op | before | after | |
|---|---|---|---|
| `EmulatorScrollThroughput/with-scrollback` | 3.45 us | 2.78 us | -19.6% (p=0.004) |
| `Emulator_PlainTextWrite` | 21.4 us | 17.1 us | -20.0% (p=0.002) |
| `PrintASCII` | 915 us | 700 us | -23.5% (p=0.002) |
| `BackendScroll` | 100.8 ms | 86.7 ms | -14.1% (p=0.037) |
| `EmulatorWriteHeavyOutput/colored-log` | 61.7 us | 56.7 us | -8.1% (p=0.026) |
| every other vt benchmark | | | `~` |

**An ASCII run is stored straight into its row** (`utf8.go`). `printASCIIRun`
wrote each byte through `Screen.SetCell`, `grid.SetCell` and `uv.Line.Set`:
three calls and two bounds-checked reads per byte, all of which come down to
one store when the cell being overwritten is one column wide. When the row
exists and every cell under the run is narrow, the run now stores its cells
directly; anything wide under it, or a row nothing has written, takes the old
path. `TestASCIIRunMatchesPerCharacterPath` holds the result to the
per-character path on random input with wide characters in the way.

| CPU per op | before | after | |
|---|---|---|---|
| `Emulator_PlainTextWrite` | 17.1 us | 12.9 us | -25.0% (p=0.002) |
| `EmulatorScrollThroughput/with-scrollback` | 2.80 us | 2.13 us | -24.1% (p=0.002) |
| `PrintASCII` | 685 us | 531 us | -22.5% (p=0.002) |
| `BackendTUI` | 6.37 ms | 5.17 ms | -18.9% (p=0.002) |
| `EmulatorWriteHeavyOutput/plain-log` | 51.7 us | 44.6 us | -13.7% (p=0.006) |
| `EmulatorWriteHeavyOutput/colored-log` | 54.2 us | 48.8 us | -10.0% (p=0.002) |
| `EmulatorWriteHeavyOutput/fullscreen-repaint` | 93.1 us | 85.0 us | -8.7% (p=0.002) |
| `EmulatorScrollThroughput/alt-screen-no-scrollback` | 1.71 us | 1.55 us | -9.5% (p=0.028) |
| `EmulatorShortLineScroll/alt-screen-no-scrollback` | 600 ns | 545 ns | -9.2% (p=0.015) |
| every other vt benchmark | | | `~` |

**A row knows where its text ends** (`grid.go`). On a short-line flood 72% of
the time was the blank tail of each 207-column row: the scrollback's
trailing-blank trim (`isBlankCell`, 39%) and the blanking of the row brought in
at the bottom (`blankRows`, 33%), each walking about 22 KB of cells for a line
of ten characters. This is the lever the `BlankFill` entry above left open:
moving fewer bytes. The grid now keeps, per row, a column past which every cell
is a plain blank. `SetCell` raises it, a write through `row()` and the ASCII
run's direct store raise it, a full-width line shift and the whole-screen
rotation carry it with the row, and blanking a row resets it. `blankRows`,
`Clear` and the scrollback push stop there.
`TestGridMatchesUVBufferUnderRandomOperations` now checks the invariant after
every grid operation, and
`TestGridExtentHoldsUnderGeneratedInput` checks it on both screens after every
step of generated terminal input. It costs one int per row.

| CPU per op | before | after | |
|---|---|---|---|
| `EmulatorShortLineScroll/with-scrollback` | 1120 ns | 240 ns | -78.6% (p=0.002) |
| `EmulatorShortLineScroll/alt-screen-no-scrollback` | 555 ns | 195 ns | -64.9% (p=0.002) |
| `EmulatorWriteHeavyOutput/plain-log` | 46.3 us | 24.2 us | -47.8% (p=0.002) |
| `EmulatorWriteHeavyOutput/colored-log` | 50.0 us | 26.7 us | -46.7% (p=0.002) |
| `BackendScroll` | 71.3 ms | 47.1 ms | -33.9% (p=0.002) |
| `Emulator_ANSIColorWrite` | 993 ns | 693 ns | -30.2% (p=0.002) |
| every other vt benchmark | | | `~` |

**A truecolor repaint stops allocating per colour** (`csi_sgr.go`, `utf8.go`).
It made four objects per cell. Two were `ansi.ReadStyleColor` boxing a
`color.RGBA` into a `color.Color`, once for the foreground and once for the
background. SGR `38;2;r;g;b` and `38:2:r:g:b` (and 48, 58) now take the boxed
value from a 256-slot direct-mapped cache on the emulator, made on the first
truecolor SGR: in the unthemed path when the SGR is that colour alone, and in
the themed path wherever the colour appears. A third was `string(e.grapheme)`
in `extendOpenGrapheme`: SGR leaves a non-ASCII cluster open, so every next
character was tested against it through a fresh string. The test now runs on a
reused byte buffer and only a rune that really extends the cluster pays for a
string. `TestRGBParamsMatchReadStyleColor` and `TestHandleSgrMatchesReadStyle`
hold the colour path to ansi's and uv's readers on random parameter lists.

| `BackendDoomFire158x40` | before | after | |
|---|---|---|---|
| allocs/op | 757,200 | 189,600 | -75.0% |
| B/op | 5.83 MiB | 1.51 MiB | -74.2% |
| CPU per op | 83.8 ms | 78.8 ms | -6.0% (p=0.032) |
| every other vt benchmark | | | `~`, allocations unchanged |

### Measured and deliberately not changed

The fourth allocation of a truecolor cell is `string(e.grapheme)` in
`flushGraphemeAtWriteEnd`, which keeps the open cluster's text across the SGR.
A single-rune string cache would remove it, one allocation per non-ASCII cell
before an escape sequence. The CPU gain of the three removed above was 6%, so
the fourth is worth at most a third of that, and a string cache is one more
piece of per-emulator state to reason about. Left for when a profile of a real
workload asks for it.

## 2026-09 agents rail grouping and metadata

The agents section gained a needs-you order, a need token, a meta token and a
metadata fold in the rail signature. The rail is on the hot path, so it was
measured before and after with two new benchmarks: `BenchmarkSidebarAgentsRebuild`,
a full rebuild of a rail watching twelve agents in every state, and
`BenchmarkSidebarAgentsCached`, the cached frame over the same fleet, whose
signature folds every agent pane.

Measured with the two test binaries run alternately, eight runs each, one second
per run, so machine load hit both sides alike. Medians:

| Benchmark | Before | After | Allocations |
| --- | --- | --- | --- |
| `BenchmarkSidebarAgentsRebuild` | 345 µs, 55288 B | 342 µs, 55992 B | 1572, unchanged |
| `BenchmarkSidebarAgentsCached` | 2670 ns | 2667 ns | 0, unchanged |
| `BenchmarkSidebarPanelCached` | 1207 ns | 1208 ns | 0, unchanged |
| `BenchmarkSidebarPanelLinesCached` | 1215 ns | 1206 ns | 0, unchanged |

No change beyond noise in time. A rebuild allocates 704 bytes more, from the two
extra tokens in the shipped row. The signature folds a pane's metadata only when
it has some, so a rail with no metadata pays nothing per frame for it, and a
state sync that leaves a pane's metadata unchanged reuses the pane's list rather
than allocating a new one.

## 2026-09 second profiling pass: render

Profiled the compositor over a keystroke frame (`BenchmarkKeystrokeFrame`,
`BenchmarkKeystrokeFrameTiled`), a nine-pane flood (`BenchmarkClientFrame`),
the dock-row pointer sweep (`BenchmarkPointerSweep`) and a connect toast, and
took each hotspot as its own commit.

### Measurement conditions

Apple M3 Pro, 11 cores, shared with other agents at a load average of 7 to 10.
Every figure is process CPU time (user plus sys) per iteration from
`/usr/bin/time`: one process per run, `-test.cpu 1`, a fixed
`-test.benchtime Nx`, `nice -n 15`, the old and new test binaries alternating,
eight rounds a side, compared with `benchstat`. Allocation counts are exact.
Each change is measured against the commit before it.

Both binaries run with `CLICOLOR_FORCE=1 COLORTERM=truecolor`. Before the first
change below, a frame composed under `go test` went through `lipgloss.Sprint`,
which found that stdout was not a TTY and stripped every escape from it. A
default-environment A/B of that change therefore compares a colourless base
frame against a coloured new one, and reads as a regression that is not there.

### What moved

**A composed frame is no longer copied through `lipgloss.Sprint`**
(`render.go`). `composeFrame` ended with `lipgloss.Sprint(canvas.Render())`.
On a truecolor terminal that is two more copies of a 40 to 50 KB frame. On a
256-colour one it downsampled the frame that the bubbletea renderer downsamples
again per cell, from the same `colorprofile.Detect` on the same stdout. On a
headless server it stripped every colour, which is why `internal/server` and
`tuios-web` pinned `lipgloss.Writer.Profile` to truecolor. Both pins are gone:
the only other `lipgloss.Writer` users are `lipgloss.Sprintf` calls over plain
text, which no profile changes. `TestComposeFrameKeepsPaneColour` holds a
composed frame to its pane's colour.

With colour in test frames, `TestEffectsWithNoOpeningNeverHideTheScreen`
failed for `highlight` (98 of 142 glyphs readable at 40x12). The 44 missing
cells are the dock hairline (`#4d4b4f` on the terminal background) and the
rounded caps of the dock pills, which the untouched screen already draws below
the 3:1 contrast the test calls readable. Every glyph that is readable on the
settled screen stays readable on every frame of the effect, so the effect was
right and the test now holds only the settled screen's legible glyphs to the
claim. Flagging `burn` or `rings` as keeping the screen still fails it.

| per op | before | after | |
|---|---|---|---|
| `KeystrokeFrame/panes-4` B/op | 124.2 KiB | 84.1 KiB | -32.3% (p=0.000) |
| `KeystrokeFrameTiled/panes-9` B/op | 208.2 KiB | 172.1 KiB | -17.3% (p=0.000) |
| `ClientFrame/panes-9/whole` B/op | 2.54 MiB | 2.14 MiB | -15.7% (p=0.000) |
| allocs/op | | | -5 on each |
| CPU | | | `~` on all three (p=0.16 to 0.63) |

The copies are large and few, so the CPU they cost is below what this machine
resolves. The change is kept for the bytes, for the headless colour it no
longer depends on a global for, and because every test and benchmark that
composes a frame now sees the frame a terminal gets.

**The divider overlay is reused while its inputs hold** (`render_border_grid.go`).
`renderSeparatorOverlay` rebuilt a map-keyed grid, the SGR strings, a
`fmt.Sprintf` id and a `lipgloss.NewLayer` per run on every frame, and a
vertical divider is one run per row: about 120 layers at nine panes, 8% of a
tiled keystroke frame. A keystroke changes none of what it reads. It now keeps
the last overlay with its inputs (bounds, view size, chrome rules, border
style, focus perimeter, the two SGR strings that carry the theme and the mode,
and the divider and pane-stack slices) and hands the same layers back when they
match. The same layer pointers also let the compositor copy their cells rather
than parse them. `TestSeparatorOverlayMemoFollowsItsInputs` changes focus,
mode, border style, theme, dock position and screen size in turn and requires
the reused overlay to match a fresh draw after each.

| per op | before | after | |
|---|---|---|---|
| `KeystrokeFrameTiled/panes-9` CPU | 2.02 ms | 1.81 ms | -10.4% (p=0.005) |
| `KeystrokeFrameTiled/panes-9` allocs | 2302 | 1569 | -31.8% |
| `KeystrokeFrameTiled/panes-9` B/op | 172.1 KiB | 88.0 KiB | -48.9% |
| `KeystrokeFrameTiled/panes-4` CPU | 2.01 ms | 1.90 ms | -5.2% (p=0.001) |
| `KeystrokeFrameTiled/panes-4` allocs | 2082 | 1540 | -26.0% |
| `KeystrokeFrame/panes-4` (floating, no dividers) | | | `~` |

**A window inside the viewport is not clipped** (`render.go`). For every
redrawn pane, `clipWindowContent` split the box, measured every row by grapheme
and joined it back, and `lipgloss.NewLayer` then measured it again. `GetCanvas`
already knows when a window lies wholly inside the viewport, and the box is
drawn to the window's own rectangle, so there it now goes to the layer as it is.
`TestFullyVisibleWindowBoxFitsItsRectangle` holds `renderWindowBox` to that
rectangle over plain, overlong and malformed-UTF-8 titles and bodies, on both
body paths and with zen mode on and off.

| per op | before | after | |
|---|---|---|---|
| `ClientFrame/panes-9/compose` CPU | 4.95 ms | 4.33 ms | -12.6% (p=0.008) |
| `ClientFrame/panes-9/compose` B/op | 1.49 MiB | 1.39 MiB | -6.7% |
| `KeystrokeFrameTiled/panes-4` B/op | 85.0 KiB | 77.7 KiB | -8.5% |
| `ClientFrame/panes-2/compose`, `KeystrokeFrameTiled/panes-4` CPU | | | `~` |

The profiler's -6.7% for `ClientFrame/panes-2/compose` did not reproduce: two
panes redraw too little per frame for the measurement to matter.

**The dock hairline is cached styled** (`render_dock.go`). The dock cached the
repeated glyph of its hairline and then styled it again on every frame with
`lipgloss.NewStyle().Width(w).Foreground(...)`, and the width makes lipgloss
wrap the whole row by grapheme: 43% of the dock's time in a tiled keystroke
frame. The cache now holds the styled row, keyed on width, glyph and the rule
colour. `TestDockSeparatorFollowsTheTheme` switches theme and requires the
hairline to take the new rule colour.

| per op | before | after | |
|---|---|---|---|
| `KeystrokeFrameTiled/panes-4` allocs | 1538 | 891 | -42.1% |
| `KeystrokeFrameTiled/panes-4` B/op | 77.7 KiB | 68.4 KiB | -12.0% |
| `KeystrokeFrame/panes-2` allocs | 1968 | 1321 | -32.9% |
| `KeystrokeFrame/panes-2` B/op | 104.6 KiB | 95.3 KiB | -8.9% |
| CPU on both | | | `~` (p=0.17 and 0.24) |

The CPU this saves is about 3% of a frame, below what this machine resolves;
the allocations are exact.

**The dock band passes a motion only when the dock's hover would change**
(`program_options.go`, `tooltip_dock.go`). The motion filter passed every cell
a pointer crossed on the dock row, bare bar included, so a sweep along it
composed one full frame per cell on every served client, each identical to the
last: 1.000 frames per event on `BenchmarkPointerSweep/chrome/filtered`. The
clause now also asks `dockHoverChangesAt`, which resolves the control and the
clipped pill under the pointer from the hit lists the handler reads and
compares them with the lit control and the label. Onto a control, from one to
another and off one still pass. `TestDockHoverChangesAtAgreesWithTheHandler`
walks every dock cell from every hover state, with tooltips on and off, and
requires the prediction to match what `DockSessionHoverAt` and
`DockWorkspaceHoverAt` then do. `TestPointerSweepOverTheDockPassesOnlyItsTargets`
holds the sweep to two passes per target.

| `PointerSweep/chrome/filtered` | before | after | |
|---|---|---|---|
| frames/event | 1.000 | 0 | |
| time per event | 206 us | 66 ns | -99.97% (p=0.000) |
| allocs/op | 205 | 2 | -99.0% |
| B/op | 20,326 | 80 | -99.6% |
| `PointerSweep/content/filtered` | | | unchanged |

CPU per op on the new side is below the resolution of `/usr/bin/time`: the
whole 20,000-event process takes under 10 ms, so the time row is the
benchmark's own ns/op over the same alternating rounds. The profiling pass
measured the served end of it, an in-process SSH and web server with a client
sweeping the dock row, at 256 us to 52 us of server CPU per event over SSH and
276 us to 102 us over the web.

**A live message composes a frame only when its burn moves a cell**
(`render_dock_notification.go`, `update.go`). While a message is up the tick
runs at the frame rate, and it composed on every tick, although the only part
of the message block that moves on its own is the rule, which burns down one
cell at a time: about as many changes over the message's life as the block is
wide. A web client in its first three seconds, with the connect toast up,
composed 171 frames to put 18 on the wire. The dock now records what it drew
of the message (its id, the queue count, the lit cells and the span), and the
tick composes when `notifBurnMoved` finds any of them would draw differently.
The tick rate, expiry and the frame a new message brings are unchanged. A
message the dock did not draw leaves the record behind, so the tick composes
every time, as it did before. `TestNotificationTickComposesOnlyWhenTheBurnMoves`
ages a message by fractions of a cell and requires a frame exactly on the
tick that moves the rule and after a new message.

`BenchmarkNotificationTick` is new: one op is one 60 Hz tick with a live info
message, aged by one frame per op, with the View a composing tick is followed
by.

| per op | before | after | |
|---|---|---|---|
| `NotificationTick` render/tick | 1.000 | 0.086 | -91.4% |
| `NotificationTick` CPU | 224 us | 23 us | -89.7% (p=0.000) |
| `NotificationTick` allocs | 340 | 38 | -88.8% |
| `NotificationTick` B/op | 16.7 KiB | 2.1 KiB | -87.5% |
| `IdleTick` | 0 render/tick, 0 work/tick, 296 B, 5 allocs | the same | `~` |

## 2026-09 second profiling pass: daemon

The output path from the daemon's PTY to a client's emulator, idle memory per
pane, the render signal of a local pane, and the snapshot a workspace switch
waits on. Six changes landed and one was measured and dropped.

### Measurement conditions

Apple M3 Pro (11 cores), macOS, Go 1.27.1. macOS has no `taskset`, so every run
used `nice -n 15`. Other work shared the machine, with a load average of 4 to 9,
so timings are quoted only where `benchstat` clears them. Allocation counts,
bytes, frame and compose counts are exact or nearly so.

Every comparison is two test binaries, the commit before the change and the
change, built from `git archive` of each commit, run alternately (old, new, then
new, old) for 6 to 10 rounds and compared with `benchstat`. Benchmarks run with
`-test.cpu 1` and a fixed `-test.benchtime Nx`, except the in-process flood
rigs noted below.

The flood and state harnesses were scratch test files, not committed:

- A 16 MiB flood, `yes 99x | head -c 16MiB`, through a real daemon and a
  `TUIClient` with a wire-only handler, reporting process CPU (getrusage, which
  covers daemon and client) per MB, frames per MB and allocations.
- The same flood into a real client `terminal.Window` with its emulator, from
  the `internal/app` rig, waiting until the end marker is on the client screen.
- The ephemeral SSH and web servers flooded with 200,000 styled lines, with
  CPU, composes and wire bytes counted in the server process.
- Heap after GC held by 16 idle stream subscriptions and 16 idle daemon windows.

The two flood rigs run at `-test.cpu 4`. At one P, daemon and client share it,
the subscriber is starved, and gap resync drops most of the flood, which is an
artifact of running both in one process.

None of this was measured on Linux, where PTY reads are larger, so the frame
counts there start lower and the first change gains less.

### What moved

**A flooding pane is sent at most one frame per millisecond**
(`daemon_stream.go`). A macOS PTY read is about 330 bytes, and the stream
goroutine drains faster than the reader fills, so a flood went out one wire
frame per read: about 2,600 frames per MB, each a daemon write, a client read
and several goroutine wakeups. `streamPTYOutput` now counts the bytes sent in
the current 1 ms window. Once a window has carried 4096 bytes, the next chunk
waits for the window to end and goes out with whatever queued meanwhile. Output
under 4 KB per millisecond, and a resize, are never held.
`stream_frame_gate_test.go` pins both sides: a flood is held to one frame per
window, and a small echo, a byte after a quiet window and a resize go at once.

| 16 MiB flood | before | after | |
|---|---|---|---|
| wire rig, frames per MB | 2,618 | 15.8 | -99.4% |
| wire rig, CPU per MB | 44.5 ms | 34.8 ms | -21.8% (p=0.000) |
| wire rig, allocations per op | 390.5k | 263.8k | -32% |
| client window, CPU per MB | 72.1 ms | 55.0 ms | -23.7% (p=0.002) |
| client window, allocations per MB | 23.4k | 14.7k | -37% |
| client window, wall per MB | 17.0 ms | 15.7 ms | ~ (p=0.39) |

Bytes allocated per MB in the client window rose 3.4%, since fewer and larger
frames are read.

The cap must not delay a keystroke. `TestLatencyEcho` (keystroke to a composed
frame, 200 keystrokes a run, 6 runs a side) and `TestLatencyDaemonRoundTrip`
showed no significant change at p50, p95 or p99:

| p50 | before | after | |
|---|---|---|---|
| echo, 1 pane | 864 us | 763 us | ~ (p=0.13) |
| echo, 4 panes | 804 us | 802 us | ~ (p=0.82) |
| daemon round trip to the client emulator | 144 us | 206 us | ~ (p=0.13) |

The spreads under this load were 18% to 70%, so these say the cap adds nothing
measurable to a quiet pane, which is what the gate's design says too. The echo
into a pane that is itself flooding can wait up to 1 ms, below the client's own
8 ms coalescing interval.

**The client no longer copies PTY output it already owns**
(`window_io.go`). `WriteOutputAsync` copied every frame, but its one caller
hands it a slice of a payload that `readMessageBody` allocates fresh for each
frame and never touches again. The window now queues the slice and documents
that it takes ownership; `TestPTYOutputHandlerOwnsItsBytes` pins the client side
of that contract. `streamPTYOutput` also keeps a pending resize as a value, since
taking the address of the receive variables made them escape.

| 16 MiB flood | before | after | |
|---|---|---|---|
| client window, bytes allocated per MB | 3.70 MiB | 2.36 MiB | -36% (p=0.000) |
| client window, allocations per MB | 12.3k | 6.4k | -48% |
| wire rig, allocations per op | 233k | 111k | -52% |
| CPU, both rigs | | | ~ |

**The output batch buffers grow on first use.** `streamPTYOutput` and a
window's `outputWriter` each made a 256 KiB batch buffer when their goroutine
started, so every client and pane subscription on the daemon, and every daemon
window on the client, held 256 KiB whether or not the pane ever printed. Both
start from a nil slice now; the first output grows it and the capacity is kept.

| heap after GC, per idle instance | before | after |
|---|---|---|
| daemon stream subscription | 415 KiB | 159 KiB |
| client daemon window, 80x24 | 519 KiB | 262 KiB |

The same to within 1 KiB in all 6 rounds: exactly the 256 KiB buffer. A 12 pane
attach allocates 3 MiB less before its first frame.

**A local pane signals the renderer through the coalescer**
(`window.go`, `window_io.go`). A local PTY pane, which is every pane of an
ephemeral SSH or web session and the fallback when the daemon cannot start,
signalled `PTYDataChan` after every PTY read, so a flood composed one frame per
read. It now starts the same `renderCoalescer` a daemon pane uses: the first
output after a quiet spell signals at once, and a burst signals at most once per
interval. `TestLocalPaneFloodSignalsAtTheCoalescerRate` counts the signals and
checks the trailing one; with the old reader it counted 4,870 signals in 166 ms
against a limit of 23.

| ephemeral flood, 200k lines | before | after | |
|---|---|---|---|
| SSH, CPU | 1.68 s | 0.97 s | -42% (p=0.009) |
| SSH, composes | 1,600 | 80 | -95% |
| SSH, bytes allocated | 98.5 MiB | 9.3 MiB | -91% |
| web, CPU | 1.55 s | 0.95 s | -39% (p=0.002) |
| web, composes | 1,475 | 298 | -80% |
| web, bytes allocated | 95.8 MiB | 19.1 MiB | -80% |

Keystroke to render signal on a quiet local pane running `cat` (60 keys a run,
8 runs a side): p50 34 us before and 45 us after, p90 86 and 97 us, neither
significant (p=0.23, p=0.57). The coalescer adds at most one goroutine handoff.

**A snapshot is applied straight from its packed form** (`session.go`,
`snapshot_pack.go`, `tuiclient.go`). The client asked for packed cells, unpacked
them into `[][]CellState`, and `ApplyTerminalState` then turned each `CellState`
back into an emulator cell and resolved its style once per cell. The client now
keeps the reply packed and only checks on receipt that every row decodes, so a
malformed snapshot still fails the request. `ApplyTerminalState` resolves the
style table once and walks the packed rows into the emulator. This replaces the
2026-09 daemon and wire pass's "the client unpacks the reply as soon as it is
decoded"; `Unpack` is kept, exported, for test oracles.

`BenchmarkWireTerminalStateApply */apply` (decode, receipt check, apply), 207x55,
6 rounds, all p <= 0.041:

| | before | after | |
|---|---|---|---|
| packed palette, screen only | 1.57 ms, 2.77 MiB | 1.04 ms, 1.50 MiB | -34% |
| packed palette, 1000 rows | 19.6 ms, 51.4 MiB | 12.0 ms, 26.7 MiB | -39% |
| packed truecolor, screen only | 1.97 ms, 13.6k allocs | 1.15 ms, 3.6k allocs | -42% |
| packed truecolor, 1000 rows | 24.1 ms, 228.6k allocs | 13.8 ms, 17.1k allocs | -43% |
| packed gradient, screen only | 2.72 ms | 2.42 ms | -11% |

The cost is on the path only a new client talking to an older daemon takes: a
snapshot that arrives as cells is packed on receipt. Palette screen 2.14 to 2.45
ms (+15%), and the every-cell-different gradient screen 2.63 to 6.14 ms.

**The daemon packs a requested snapshot as it reads the rows**
(`GetTerminalStatePacked`). A packed request made the daemon build the whole
`[][]CellState` under `terminalMu` and then pack it. It now reads each row into
one scratch row and packs it at once, in the order `Pack` packs the grids, so
the style table and every byte on the wire are the same.
`TestDirectPackMatchesPack` holds the two paths to identical results on
randomized emulators; packing the main screen one step early makes it fail.

| 207x55, 20x, 8 rounds | before | after | |
|---|---|---|---|
| packed reply, screen only | 701 us, 1,406 KiB | 444 us, 180 KiB | -37% (p=0.000) |
| packed reply, 1000 rows | 18.7 ms, 50.4 MiB | 16.9 ms, 27.0 MiB | -9% (p=0.000) |
| time under `terminalMu`, screen only (10 rounds) | 535 us | 437 us | -18% (p=0.000) |
| time under `terminalMu`, 1000 rows (10 rounds) | 16.7 ms | 17.3 ms | ~ (p=0.19) |

Wire bytes are identical. The 1000 row lock hold does not fall because packing
now happens under the lock; the 194k allocations left in it are the vt
scrollback building the cells it hands back.

### Measured and deliberately not changed

- **Skipping a resurrection save whose bytes have not changed.** Each idle
  session rewrites its state file every 30 s, eight small writes and renames a
  minute at four sessions. Skipping the write when the marshalled bytes match
  what this saver last wrote, and the file still exists, costs a marshal and a
  stat instead: 9.9 us against 165 us for the write (`-cpu 1`, 6 rounds). That
  is about 5 us of CPU a second per session, and idle daemon CPU sits at the
  resolution floor either way. It would also change what users see: the
  "saved" column of `tuios ls` and the `LastActive` of a saved session come from
  the file's mtime, and an idle live session would show "saved 3h ago" as if
  saving had stopped. Not worth the visible change.

### Invariants held

`ProtocolVersion` and the wire form are unchanged: `TestDirectPackMatchesPack`
compares bytes, and `TestOlderPeerReadsTheWire` now also applies an answer that
carried cells and checks the pane comes back. The quiet-pane echo tests above
are the check that no change here holds a keystroke.

## 2026-09 second profiling pass: vt

A second profile of `internal/vt`, this time over replays of real PTY captures
rather than synthetic floods, plus the scrollback's memory and the SGR diff in
the pure Go renderer. Each change below is its own commit.

### Measurement conditions

The captures were recorded at 160x45 with `TERM=xterm-256color` and
`COLORTERM=truecolor` and are not in the repository: `btop` (btop -u 100 for
8 s, 1.9 MB), `nvim` (nvim --clean paging through a Go file, 0.5 MB),
`nvimcfg` (the same with a full user config, 5.3 MB), `uni` (6000 lines of
mixed scripts, emoji clusters, box drawing and braille, 1 MB) and `claude`
(`testdata/claude_title_ghost.raw` repeated 100 times). A throwaway benchmark
replays each one in 4 KB writes into `NewWithScrollback(160, 45, 10000)`.

The method is the one above: process CPU time (user plus sys) per op at a fixed
`-test.benchtime Nx` with `-test.cpu 1` under `nice`, old and new test
binaries alternating for eight rounds, compared with `benchstat`. Each change
is measured against the commit before it. On this machine slot noise is about
+/-8% per sample, so a figure is quoted only where it clears that with
p < 0.05. Allocation counts are exact. Before each commit the cells, styles,
links and scrollback of every capture were hashed under random write splits and
random resizes, and the hashes matched the commit before.

### What moved

**Mode changes stop formatting a debug line** (`csi_mode.go`,
`kitty_keyboard.go`). `setMode` and the kitty keyboard handlers called
`e.logf` on every call. Go boxes the variadic arguments before `logf` sees
that no logger is set, and no production code sets one. nvim toggles `?25`
around every redraw and Claude Code sends `?2026` and `?25` on every frame, so
on the `claude` replay this was 56% of all bytes the emulator allocated. The
"unhandled sequence" lines, which the conformance corpus reads, stay.
`TestPerFrameModeSetsAllocateNothing` pins a mode set and a kitty keyboard
push, pop and set at zero allocations.

| | before | after | |
|---|---|---|---|
| `claude` CPU per op | 15.91 ms | 15.04 ms | -5.4% (p=0.002) |
| `claude` allocs/op | 34.62k | 19.72k | -43% |
| `claude` B/op | 1068 KiB | 379 KiB | -64% |
| `nvim` allocs/op | 600 | 125 | -79%, CPU `~` |
| `nvimcfg` allocs/op | 15.93k | 15.10k | -5%, CPU `~` |

**An erase stores its cells instead of setting each one** (`grid.go`). On the
`claude` replay `grid.FillArea` was 33% of CPU: ED 2 from `Screen.Clear` and
EL on every frame went through `grid.SetCell` and `uv.Line.Set` once per cell,
blank tail included. A fill with a one-column cell now calls `Set` on the two
end cells of each row's span only, because only their wide-character repair can
reach outside the span, and stores the fill cell into every cell between. A
blank fill also stops at the row's extent, past which every cell is blank
already, and pulls the extent in to where it started when it reaches it. A wide
fill cell keeps the old loop. `TestGridFillAreaMatchesUVBuffer` holds the grid
to `uv.Buffer.FillArea` with fill cells of width 0 to 3, rows seeded with wide
characters, and areas that start left of the grid and end past it.

| CPU per op | before | after | |
|---|---|---|---|
| `claude` | 15.96 ms | 9.87 ms | -38.2% (p<0.001) |
| `nvim` | 6.84 ms | 5.37 ms | -21.5% (p<0.001) |
| `nvimcfg`, `btop`, `BackendTUI` | | | `~`, allocations unchanged |

**A lone symbol's cell comes from a static string** (`utf8.go`,
`emulator.go`). This is the fourth truecolor allocation the vt parse and scroll
pass left for "a profile of a real workload": `string(e.grapheme)` in
`flushGraphemeAtWriteEnd` and `renderGraphemeBuffer`. The real captures ask
for it. It was 70% of the bytes allocated on `btop` and 36% on `nvimcfg`, and
in both about 99% of those cells were a single U+25xx character between two
SGRs. The grapheme buffer is now UTF-8 bytes rather than runes, which removes
the scratch buffer and the re-encode and decode loops around it, and a buffer
holding one rune in U+2000 to U+2BFF (punctuation, arrows, maths, box drawing,
blocks, shapes, dingbats, braille) is served as a substring of a 9 KB string
built once at init. Anything else still allocates its string. A per-emulator
cache keyed by rune was tried first and thrashed on `uni`, doubling its
allocations. `TestStyledSymbolCellsAllocateNothing` pins styled symbol cells
at zero allocations, `TestGraphemeStringServesEverySymbolWhole` checks the
table over the whole range and its edges, and
`TestSymbolClusterExtendsAcrossWrites` checks that a symbol served from the
table still takes a combining mark from the next write.

| | before | after | |
|---|---|---|---|
| `BackendDoomFire158x40` CPU per op | 77.3 ms | 71.9 ms | -7.1% (p<0.001) |
| `BackendDoomFire158x40` allocs/op | 189,604 | 4 | B/op -96% |
| `btop` CPU per op | 21.2 ms | 20.2 ms | -5.0% (p=0.010), inside slot noise |
| `btop` allocs/op | 30.86k | 12.44k | -60%, B/op -41% |
| `nvimcfg` allocs/op | 15,104 | 309 | -98%, B/op -63%, CPU `~` |
| `uni` allocs/op | 84.03k | 73.81k | -12%, B/op -20%, CPU `~` |

On the real streams the gain is allocation and GC work rather than measured
CPU: this benchmark process has a small heap, and the collector's share grows
with the heap of a real client.

**A narrowing resize skips scrollback lines with no wide cell**
(`scrollback.go`). `Scrollback.blankWideRunesCutByTheEdge` walked every stored
line token by token to the new last column on every width change, so dragging
a split border paid for the whole history on every step, in the client and
the daemon. With 10k lines of scrollback it was 86% of a resize. A wide cell
is only ever stored behind an `sbCell` byte, which UTF-8 never uses, so a line
without that byte is now skipped after one `bytes.IndexByte`. The byte can
also appear inside a uvarint, which only sends that line down the old walk.
`TestBlankWideRunesCutByTheEdgeSkipsOnlyNarrowLines` holds the result to the
full walk over random lines whose colours and links contain the byte, and
fails if the skip is keyed on the wrong token.

| CPU per op | before | after | |
|---|---|---|---|
| `RealResizeLog` (12k ASCII lines, 160 to 60 columns and back) | 3.68 ms | 0.36 ms | -90.2% (p<0.001) |
| `RealResize` (6k mixed-script plus 4k log lines, 160 to 100 and back) | 6.45 ms | 4.37 ms | -32.3% (p<0.001) |
| `uni`, `EmulatorWriteHeavyOutput/colored-log` | | | `~`, allocations unchanged |

**Scrollback links and clusters live in their lines** (`scrollback.go`). The
ring interned hyperlinks and multi-rune clusters in per-pane tables that
nothing pruned, not eviction and not `Clear`, capped only at 1M entries each.
A pane printing a link to a different file on every line, as agents and
compilers printing `file:line` do, grew by about 150 bytes per link for its
whole life, in the daemon and in every client, whatever the ring had let go
of. A link is now written into the line that uses it: its URL and params the
first time the line uses it, and a one-byte reference to that when the line
comes back to it, so a line alternating between two links pays for each once.
A cluster is written as its bytes. Both go when the line does. The colour
table stays, because only a colour type no emulator path makes reaches it.

A line writes each link it uses, so a guest that leaves one enormous link open
would store a copy in every line it prints after. The ring therefore keeps a
link only up to the limits VTE applies to OSC 8, 2083 bytes of URL and 256 of
params; a longer one scrolls in without its link. The interned table dropped
links past its cap in the same way.

`TestScrollbackForgetsWhatItEvicts` pushes 49,000 lines, each with a new link
and cluster, through a full 1000-line ring: the heap grew 7.2 MB before and
stays flat now. `TestScrollbackWritesALinkOncePerLine`,
`TestScrollbackDropsAnOversizedLink` and
`TestScrollbackDecodesEveryTruncationSafely` cover the back reference, the
bound, and a record cut at every byte.

| Live heap of a 207x55 pane | before | after |
|---|---|---|
| 10k lines, one unique OSC 8 link each | 3.88 MiB | 2.78 MiB |
| 100k lines, one unique OSC 8 link each | 17.93 MiB | 2.78 MiB (flat) |
| plain log, colored log, full-width truecolor | | unchanged |

CPU per op is `~` on a replay of 30,000 linked lines, `uni`, the plain and
colored log floods, the short-line and long-line scrolls and `RealResize`, and
the linked replay allocates 14% fewer bytes. The cells of one link still
decode sharing its strings.

**A palette colour change skips uv's style diff** (`emulator.go`). Every
unfocused dirty pane is drawn by `Emulator.Render`, which called
`uv.Style.Diff` at every pen change. The diff builds an `ansi.Style` and
allocates its string each time, and in a nine-pane flood compose it was 21% of
CPU. `penDiff` answers the common case, a change of a basic or indexed
foreground or background and nothing else, from two 272-entry tables built at
init with the same `ansi.Style` call uv makes, and hands everything else to
uv. A per-render memo keyed by the style pair was tried first: allocations
fell 54% but CPU rose 5.8% (p=0.002), because hashing six interfaces costs more
than the diff. `TestPenDiffMatchesStyleDiff` holds `penDiff` to uv byte for
byte over two million random pairs of nil, basic, indexed, RGBA, theme-resolved
and truecolor colours with random attributes, and
`TestPaletteSGRChangesAllocateNothing` pins the fast path at zero allocations.
Themed ANSI 0 to 15 resolve to RGBA and truecolor is RGB, so both still go to
uv, and the ghostty backend renders through `uv.Line.Render` and gets nothing
from this.

`TestCompositorSkipsCleanWindows` compared one dirty pane against half of all
nine dirty. With a pane this cheap to render the frame's fixed cost dominated
both totals and the ratio failed with damage tracking intact, so it now
dirties an unfocused pane, not the focused one, which is drawn by a different
path, and compares what each case adds over a frame with nothing dirty: 12
allocations against 199.

| | before | after | |
|---|---|---|---|
| `RenderWindowBoxFlood/unfocused` CPU per op | 1448 us | 799 us | -44.9% (p<0.001) |
| `RenderWindowBoxFlood/unfocused` allocs/op | 29,028 | 6,409 | -78%, B/op -34% |
| `ClientFrame/panes-9/compose` CPU per op | 5.10 ms | 4.37 ms | -14.4% (p=0.005) |
| `ClientFrame/panes-9/compose` allocs/op | 42.49k | 11.78k | -72%, B/op -17% |
| `RenderTerminalUnfocused` allocs/op | 127 | 13 | CPU `~` |
| `KeystrokeFrame/panes-4` | | | `~` |

### Measured and deliberately not changed

- **Storing each scrollback line at its exact size.** A styled line grows
  past the `len(cells)+16` bytes `push` gives it and keeps the spare half of
  an append doubling, 56% slack on a colored log. Encoding into a scratch
  buffer and copying to an exact-size slice cut a colored-log pane from 3.53 to
  2.93 MiB, but reusing the evicted line's storage then needs a size window,
  and on a colored log whose line lengths vary most evicted lines miss it: a
  200-line write into a full ring went from 3 allocations and 1 KB to 154 and
  21.6 KB, and the push microbenchmark from `~` to +9.2% CPU (p=0.028). The
  inline links and clusters above were kept without it.
- **Keeping the mode-change debug lines behind a level check.** No production
  code sets a logger on the emulator, so a check would guard output nobody can
  receive; deleting the calls is the same saving with less code. The
  "unhandled sequence" lines, which the conformance corpus reads, stay.
- **Rekeying the modes map to avoid boxing.** `handleMode` boxes
  `ansi.DECMode(param)` into an `ansi.Mode` for the map, one small allocation
  per mode set (29 MB over the `claude` replay). After the logf removal it is
  the only allocation left on that path, and removing it means changing the
  map's key type everywhere modes are read.
- **A direct-indexed table for CSI finals.** The handler map lookup is about
  3.6% of `nvimcfg`, inside the slot noise, for a second dispatch table to keep
  in step with the map.
- **The ASCII run's row store.** `printASCIIRun`'s store into the row is 20% of
  `nvimcfg`, and it is memory bandwidth: a 112-byte `uv.Cell` per character, as
  the `BlankFill` entry above already found.

## 2026-09 second profiling pass: startup

What a CLI process and an attach cost before any work is done, and the daemon
work behind the verbs an agent calls in a loop. A one-shot command such as
`list-windows` takes about 6 ms, and the daemon round trip is 0.02 to 0.15 ms of
that: 1.8 ms is the Go process floor, about 0.9 ms is dyld loading
CoreFoundation and Security for crypto/x509, and about 1.2 ms is package init.
The largest cost found was not in the CLI at all but in how the daemon reads a
pane's scrollback for `capture-pane` and `wait-for window-output`.

### Measurement conditions

Two test binaries, one built from the base commit with the new benchmark file
copied in and one from the change, run alternately, six rounds, each run
`-test.cpu 1 -test.benchtime Nx -test.benchmem` under `nice -n 10`, compared
with `benchstat`. The CPU column is user plus system time of the process over
N, so it includes the benchmark's setup: for the capture benchmarks that setup
is writing 12,000 lines into the emulator, the same on both sides, and it puts
a floor of about 1.3 ms under the new path's CPU column. The machine was shared,
so timings carry the spread benchstat prints; allocation counts are exact.

### What changed

**Plain capture reads the scrollback ring as text** (`internal/vt/scrollback_text.go`,
`session.go`). `capture-pane` with scrollback, `wait-for window-output` and
`ask-agent` all took the plain text of a pane through
`ScrollbackLine(i).String()` for every line of history. That decodes each
packed line into a fresh `uv.Line` of 112-byte cells, caches it in a map that
thrashes past its cap, and walks the cells back to text: about 100 MB and
462k allocations per capture of a full 10,000-line ring at 80 columns.
`Emulator.AppendScrollbackText` walks the packed records and writes the text
directly, ASCII bytes copied straight from the record. It follows exactly the
two rules of `uv.Line.String`: a cell equal to the zero `Cell` is skipped, a
cell equal to `EmptyCell` is a pending space dropped at the end of the line,
and anything else releases the pending spaces and writes its content. Both
rules need the style and link in force to be zero, so a painted blank, a blank
inside a link and a styled wide-rune spacer all count as text. The daemon uses
it through an optional interface, so the ghostty backend keeps the line by
line loop. The ANSI capture is unchanged.

`BenchmarkCaptureScrollback` is one waiter check: capture a full ring plus the
screen, run a regexp that does not match.

| Benchmark | before | after | |
|---|---|---|---|
| `CaptureScrollback/w80` time | 29.9 ms | 1.25 ms | -95.8% (p=0.002) |
| `CaptureScrollback/w80` B/op | 95.5 MiB | 1.90 MiB | -98.0% |
| `CaptureScrollback/w80` allocs/op | 462,137 | 156 | -99.97% |
| `CaptureScrollback/w207` time | 72.4 ms | 1.33 ms | -98.2% (p=0.002) |
| `CaptureScrollback/w207` B/op | 243 MiB | 1.91 MiB | -99.2% |
| `CaptureScrollback/w207` allocs/op | 482,184 | 203 | -99.96% |

**A cell of one ASCII byte no longer allocates** (`Scrollback.readContent`).
It returned `string(rune(r))` for ASCII, which allocates, against its own
comment. `string(data[i:i+1])` is served from the runtime's table of one-byte
strings. This is the part the client's scrollback browser and the ANSI capture
still go through:

| `ScrollbackLineString` | before | after | |
|---|---|---|---|
| w80 time | 30.0 ms | 27.0 ms | -10.1% (p=0.009) |
| w80 allocs/op | 461,980 | 60,000 | -87.0% |
| w207 time | 71.8 ms | 69.5 ms | `~` (p=0.24) |
| w207 allocs/op | 481,980 | 80,000 | -83.4% |

**The wait-for backstop skips a check that cannot change** (`verb_subscribe.go`).
A `wait-for window-output` waiter re-ran the full capture and match every
200 ms even on an idle pane, to catch an output event the slow-subscriber
policy dropped. Each check now records the pane's `captureState`, the stream
position the emulator has applied plus its width and height, read under the
same lock as the capture. The backstop re-checks only when that state has
moved. Output and resizes are the only things that change a pane's content,
and a resize does not advance the stream position, which is why the size is
part of the key. The profiling pass measured one idle waiter on a pane with a
full ring at 31% of a core on the daemon before, and none after; with about
200 output events a second the waiter's daemon CPU went from 1033 cs to 34 cs
per 5.2 s, the two changes above together.

Guards: `TestScrollbackTextOfRandomLines` holds the text path to the cell path
on random lines of every cell kind, whole and cut at every byte, in a ring
that wraps. It found one divergence while this was written, a styled cell of
width 0 after pending blanks, which `uv.Line.String` flushes and a first draft
skipped; `TestScrollbackTextOfHandBuiltLines` pins that case with the zero
cell, wide-rune spacers, painted blanks, blanks in links and empty lines.
`TestScrollbackTextFollowsTheRing` wraps a seven-line ring forty times,
`TestScrollbackTextUnderGeneratedInput` runs vtgen scripts with resizes, and
`TestPlainCaptureMatchesLineByLine` compares the daemon's capture with the line
by line one. `TestWaitForOutputBackstopSeesUnannouncedChange` changes a pane
behind the waiter's back, once through applied output and once through a
resize, and fails if the backstop keys on the stream position alone.

**ValidateKey stops building its tables per call** (`internal/config/keynormalizer.go`).
It built a 7-entry modifier map and a 37-entry special-key map as literals on
every call, and it runs for every binding, twice per TUI start: once in
`LoadUserConfig` and once when `NewOS` asks for config warnings. The key table
is a package variable now and the modifiers a switch; the accepted set and every
message are unchanged. `TestValidateKeyModifiersOnEachPlatform` pins both on
macOS and elsewhere and passes on the old code too, and
`TestValidateKeyDoesNotAllocateForAPlainBinding` fails on it (5 allocations per
call). The real config is the maintainer's 548-line one; `ValidateConfigDefault`
is the in-repo benchmark on the default config.

| Benchmark | before | after | |
|---|---|---|---|
| `LoadUserConfigReal` CPU | 800 us | 567 us | -29.2% (p=0.002) |
| `LoadUserConfigReal` B/op | 1080 KiB | 556 KiB | -48.5% |
| `LoadUserConfigReal` allocs/op | 6,778 | 5,899 | -13.0% |
| `ValidateConfigDefault` CPU | 545 us | 350 us | -35.8% (p=0.002) |
| `ValidateConfigDefault` B/op | 960 KiB | 434 KiB | -54.8% |
| `ValidateConfigDefault` allocs/op | 5,156 | 4,274 | -17.1% |
