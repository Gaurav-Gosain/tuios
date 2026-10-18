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
- `scripts/binary-size.sh`: builds the stripped release binary for
  linux/amd64 and darwin/arm64 and fails over the budget. See "Binary size
  budget" below.

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

**max_fps goes to 240, and a change applies while tuios runs.** bubbletea
clamps its ticker to 120 in `NewProgram`. `OS.BindProgram` now sets the
ticker's rate after `NewProgram`, past that clamp, and resets it each time
`NormalFPS` changes: a config reload, the settings row, or the display rate
arriving for `max_fps = "auto"`. That also ends the "takes effect at the next
start" rule above. Detection for auto is one short process (`hyprctl`, `niri`,
`wlr-randr`, `xrandr` or `system_profiler`) in a goroutine, so it adds nothing
to the startup path; until it answers, auto draws at 60.

Real binary, one idle shell at 207x55, 10 s, voluntary context switches summed
over every thread (the earlier rows read the main thread's counter), three runs
each:

| | voluntary ctx switches / s | CPU |
|---|---|---|
| before, default (60) | 389 / 369 / 385 | 0.4 / 0.3 / 0.3% |
| before, max_fps 120 | 573 / 589 / 569 | 0.4 / 0.5 / 0.4% |
| after, default (60) | 363 / 367 / 387 | 0.3 / 0.3 / 0.3% |
| after, max_fps 120 | 585 / 624 / 624 | 0.4 / 0.4 / 0.4% |
| after, max_fps 240 | 1028 / 1074 / 1037 | 0.5 / 0.7 / 0.7% |

The default is unchanged. 240 is the cost of a ticker that does not idle, and
it is opt-in. Frames on the wire with a pane printing as fast as it can
(`TestMaxFPS240DrawsPastTheOldClamp`, run with `TUIOS_E2E_PERF=1`): before,
max_fps 240 drew 120 frames a second; after, 236, and auto on a stand-in
240 Hz display drew 227. The `internal/app` benchmarks (`IdleTick`,
`ClientFrame`, `KeystrokeFrame`, `PointerSweep`, six runs each) show no
regression: every change is within noise or faster, and allocations match.

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
a performance pass. Reported rather than fixed. Fixed later in #358: the reload
now calls it.

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
`TestKeysTypedRightAfterEnteringTerminalModeReachThePTY`). Two of those tests
were later removed: `TestPointerSweepOverTheDockPassesOnlyItsTargets` makes the
dock band claim now, and nothing replaces the reflection check on the program
options.

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
one client spends 84% of the process in `vt` parsing those bytes on
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

**The snapshot's cells travel packed** (`vt/snapshot/pack.go`). A style table
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

Those two, with the pooled rows in `snapshot.Capture`, are what the daemon side
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

`composeLayersIn` (`compose.go`) replaces the Compositor and the Canvas. It keeps
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

Profiled `vt` over the SGR-heavy repaint (`BenchmarkBackendDoomFire*`),
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

## 2026-09 manifest engine

The screen scan runs in the daemon on every settle of every agent pane, at most
once per 250 ms while a pane writes plus once when it goes quiet, so its cost
times the number of agent panes is what the manifest engine spends. The engine
gained nested groups, more regions and many more bundled rules, and was measured
with `BenchmarkClassify` (`internal/harness/classify_bench_test.go`) on a Claude
Code working turn and idle prompt, the bundled manifest before and after,
through the new engine (`TUIOS_BENCH_MANIFESTS` points the benchmark at an old
manifest). Five runs of 20000 iterations each, medians, Apple M3 Pro:

| Screen | Old claude-code manifest | New claude-code manifest |
| --- | --- | --- |
| working turn | 4.9 µs, 4 allocs | 10.4 µs, 13 allocs |
| idle prompt | 8.2 µs, 5 allocs | 13.1 µs, 13 allocs |

The first draft of the new manifest cost 116 µs on the working screen. Three
changes brought it down, and they are the rules for anyone writing a manifest:

- **Rules run highest priority first and the first match decides.** A rule that
  could no longer win is never read. It used to run every rule.
- **Substrings before patterns.** Within a rule the plain searches run first, so
  a rule whose words are absent refuses before any pattern runs.
- **Case-insensitive text as a folded substring, not a `(?i)` pattern.** A
  `(?i)` pattern over a screen of box drawing cost 4 to 33 µs each; the same
  words as a substring under `fold_case` cost nanoseconds. Folding itself is
  paid once per line per scan, and only for lines that have an upper case
  letter, which a frame of rules and borders mostly does not.

At twenty agent panes settling four times a second the new manifest costs about
1 ms of daemon CPU per second, against about 0.4 ms before.

## 2026-09 the Inbox

The rail's agents header counts the Inbox while it is live, and the rail
signature folds the Inbox's generation. The rail was measured before and after
with the test binaries of the base commit and of this change run alternately,
six runs each, one second per run. Medians:

| Benchmark | Before | After | Allocations |
| --- | --- | --- | --- |
| `BenchmarkSidebarAgentsRebuild` | 326 µs, 56825 B | 332 µs, 56817 B | 1572, unchanged |
| `BenchmarkSidebarAgentsCached` | 2838 ns | 2894 ns | 0, unchanged |
| `BenchmarkSidebarPanelCached` | 1308 ns | 1356 ns | 0, unchanged |
| `BenchmarkSidebarPanelLinesCached` | 1302 ns | 1372 ns | 0, unchanged |

No change beyond noise: the spread inside each column was larger than the gap
between them. With a live Inbox of sixteen items the header counts the Inbox
instead of the rows, and `BenchmarkSidebarAgentsRebuildWithInbox` measured
351 µs with 1554 allocations, and `BenchmarkSidebarAgentsCachedWithInbox`
2897 ns with none.

The new surfaces, sixteen items across sixteen sessions:

| Benchmark | Median | Allocations |
| --- | --- | --- |
| `BenchmarkInboxRender`, the open overlay | 281 µs, 72411 B | 1642 |
| `BenchmarkInboxApplyBurst`, sixteen events into the mirror | 2.8 µs, 2560 B | 8 |

The overlay is drawn only while it is open, like every other panel. A burst is
one render and one alert, since the watcher gathers events for 150 ms before it
hands them to Update.

On the daemon, the Inbox sits on the session event sink, which every output
chunk passes through. `BenchmarkAttentionOutputEvent`, an output event reaching
the queue, is 6 ns with no allocation: the queue switches on the type and
returns. `BenchmarkAttentionRepeatedBlock`, a pane reporting the same block
again, is 480 ns and publishes nothing. It was 4.4 µs before the secret mask
learned to skip its regular expression for a summary with none of the words it
keys on.

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
text, which no profile changes. `TestComposeFrameKeepsPaneColour` held a
composed frame to its pane's colour; it was later removed, and e2e
`TestListingStyleFidelityAgainstBarePTY` checks colour on the real binary.

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
`vt/snapshot/pack.go`, `tuiclient.go`). The client asked for packed cells, unpacked
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

A second profile of `vt`, this time over replays of real PTY captures
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

**Plain capture reads the scrollback ring as text** (`vt/scrollback_text.go`,
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
was the in-repo benchmark on the default config, since removed.

| Benchmark | before | after | |
|---|---|---|---|
| `LoadUserConfigReal` CPU | 800 us | 567 us | -29.2% (p=0.002) |
| `LoadUserConfigReal` B/op | 1080 KiB | 556 KiB | -48.5% |
| `LoadUserConfigReal` allocs/op | 6,778 | 5,899 | -13.0% |
| `ValidateConfigDefault` CPU | 545 us | 350 us | -35.8% (p=0.002) |
| `ValidateConfigDefault` B/op | 960 KiB | 434 KiB | -54.8% |
| `ValidateConfigDefault` allocs/op | 5,156 | 4,274 | -17.1% |

**Every CLI process does less at init** (`internal/scrollback`, `debug.go`,
`cmd/tuios/diagnostics.go`). The scrollback browser's five regexps compiled at
package init in every process, and only the browser uses them; they compile on
first use now. The daemon log buffer allocated its 1000 entries, about 40 KB, at
init, and a one-shot command never logs to it; they are allocated by the first
`Add`. `dialVerb` probed the socket with its own connection before dialing; it
dials first now and diagnoses only on failure, through `explainDialError`,
which runs the same `DiagnoseDaemon`, so the message and exit status are
unchanged and the daemon accepts one connection per verb command instead of
two. Measured with `GODEBUG=inittrace=1` on `tuios --version`, six alternating
rounds of 20 runs:

| init, per process | before | after | |
|---|---|---|---|
| total clock | 1.84 ms | 1.65 ms | -10.4% (p=0.002, n=120) |
| total bytes | 883 KiB | 766 KiB | -13.3% |
| total allocs | 5,893 | 5,279 | -10.4% |
| `internal/scrollback` plus `internal/session` clock | 245 us | 85 us | -65% |

End to end, `list-sessions --json` against a running daemon measured 9.38 ms
CPU before and 9.18 ms after (six alternating rounds of 40 runs, p=0.18): the
gain is about 2% of a command and below what this machine resolves. It is kept
because the init numbers are exact and the change adds no state. Four unit
tests were added with it and later removed:
`TestDialVerbExplainsAMissingDaemonLikeTheProbe` (the new dial against the old
probe's message and status for an absent daemon and a stale socket),
`TestLogBufferAllocatesOnFirstAdd` (the lazy buffer), and
`TestExtractPathsUsesBothPatterns` and `TestParseBlocksByPromptPatterns` (the
five patterns, which would otherwise fail only on first use).

### Measured and deliberately not changed

- **The spawn poll** (`startDaemonBackground`, 50 ms). A cold `tuios new
  --detach` spends a constant 52.5 ms waiting on it. A 2 ms poll exposed an
  attach-time size race (`TestAttachStartsTheDaemonAndBringsSessionsBack`
  failed 3 of 50 runs), so it waits for that race to be fixed on its own.
- **Flushing bubbletea's first frame at start.** The first frame goes out on
  the first 60 fps tick, 26 to 34 ms after exec on a 12-pane attach. The fix is
  an upstream bubbletea change, and the prototype sent the frame ahead of the
  mode queries `Run` queues, so it is to be filed upstream, not carried.
- **Caching the config validation result on `UserConfig`.** After the
  `ValidateKey` change it would save about 0.4 ms per TUI start, and it adds
  hidden state that goes stale when the settings page edits the config.
- **CoreFoundation and Security**, loaded by dyld because crypto/x509 is
  linked: about 0.9 ms per exec. Removing it would mean a CLI binary without
  net/http and crypto/tls. Notifications and `tuios update` now link them on
  purpose, to drop the runtime need for curl.
- **encoding/gob init**, 0.12 ms: the standard library scanning typelinks.
- **gopsutil/cpu init**, 0.06 ms, which calls host_processor_info twice. On
  darwin gopsutil/process imports it for `ProcessCwd`.
- **The cobra tree**, built whole on every invocation: 110 us and 1476
  allocations.
- **`outputChan`**, 4096 slots and about 224 KB per pane at attach. The
  producer drops data when it is full, so shrinking it trades memory for silent
  loss until backpressure is redesigned.
- **`RestoreTerminalStates`**, serial `GetTerminalState` round trips at about
  0.2 ms per pane on top of about 2 ms fixed, and **`ensureAttachTarget`**, an
  extra verb connection of 0.4 to 0.6 ms at attach.

## 2026-09 pane background

`appearance.pane_background` paints default-background cells in the
compositor, on the cells a pane's layer parses to, and keeps the paint with
those cells. Measured on an M3 Pro at 207x55, `-cpu 4`, with other work on the
machine; allocation counts are exact, times are directional.

**Off, before and after** (`BenchmarkKeystrokeFrame`, `BenchmarkKeystrokeFrameTiled`,
`BenchmarkCompositorGetCanvas`, `BenchmarkRenderTerminalUnfocused`; the two
test binaries run interleaved, eight rounds each): allocations per op are
identical on every benchmark, and no time moved by a significant amount on the
keystroke frames or the unfocused render (p between 0.5 and 1.0). With the
option off the frame pays one string comparison per layer and no map writes;
`TestPaneBackgroundOffAllocatesNothing` holds the resolve to zero allocations.

**On, against off** (`BenchmarkPaneBackground`, six runs each):

| | off | on | allocs/op off to on |
|---|---|---|---|
| keystroke, 1 fullscreen pane | 1.45 ms | 2.31 ms | 1134 to 1302 |
| keystroke, 4 panes | 1.79 ms | 2.01 ms | 1065 to 1168 |
| keystroke, 9 panes | 1.94 ms | 1.97 ms | 1051 to 1196 |
| compositor, 1/4/9 panes, all re-rendered | 518/504/477 us | 535/515/508 us | unchanged |

The lone fullscreen pane pays the most because the fast path that skips the
compositor builds no layer to paint, so it stands down while the option is on.
The extra allocations are in emitting the frame: painted cells carry a
background SGR that bare ones do not. A pane whose layer string did not change
is copied with its paint, which is why the all-dirty compositor numbers, whose
strings are identical from frame to frame, do not move.

## 2026-09 agent detection poll

The daemon reads the foreground process of every pane every two seconds, to
mark and clear agents nobody reported. It was the one idle cost that grew with
the pane count, and it did its reads while holding the session's state write
lock: three sysctls a pane on darwin, so every writer in the session (a client
state sync, a `set-agent-state`, a window closing) queued behind them on every
tick, and each tick built a lifecycle snapshot whether or not anything changed.

### What changed

- The scan lists its panes under the read lock, reads their foreground
  processes with no session lock held, and works out under the read lock what
  the readings would change. An idle tick stops there: no write lock, no
  version bump, no snapshot. The write lock is taken only to apply a change,
  and the apply re-checks each window against the PTY that was read.
- A pane that holds no agent and has printed nothing since its last read is
  read only every fifth tick (`agentDetectQuietBound`, 10 s at the default
  interval). Output makes it due on the next tick, and so does never having
  been read. A pane that holds an agent is read every tick, so the miss count
  and the clearing of an exited agent keep their timing. An agent started with
  no output at all is found within 10 s; one started by typing its command,
  which echoes, within one tick as before.

### Numbers

Idle daemon CPU, measured with the audit's script: a detached daemon with plain
zsh panes, daemon CPU time over 120 s at nice 10, on the maintainer's Mac. The
resolution is 10 ms per reading, so treat these as orders of magnitude.

| Panes | Before | After | Autodetect off (audit) |
| --- | --- | --- | --- |
| 8 | 50 ms/min | 15 ms/min | 20 ms/min |
| 32 | 95 ms/min | 25 to 30 ms/min | 10 ms/min |

Lock hold, from `BenchmarkAgentDetectScanLockWait`: real panes running real
shells, every pane read on every scan (no backoff), scans spaced 2 ms apart
while a writer polls the state lock. Three runs of 300 scans each:

| Panes | Scan | Writer wait before: max, mean | Writer wait after: max, mean |
| --- | --- | --- | --- |
| 8 | 0.28 ms | 0.9 to 1.7 ms, 8.6 to 9.1 µs | 43 to 56 µs, 0.05 µs |
| 32 | 1.0 to 1.2 ms | 2.0 to 7.7 ms, 31 to 54 µs | 65 to 111 µs, 0.07 to 0.13 µs |

The scan costs what it did; it no longer holds the write lock while it runs,
and an idle scan never takes it. The remaining writer wait is scheduling noise
of the polling goroutine.

## 2026-09 every surface's background

`appearance.background` and the per-surface options (desktop, window chrome,
dock, rail) extend the pane background's design to every transparent cell: each
layer's parsed cells are painted by the surface its id belongs to, and the
desktop is the value the canvas is cleared to. Same machine and flags as above
(M3 Pro, 207x55, `-cpu 4`, other work on the machine; allocation counts exact,
times directional).

**Everything off, before and after** (`BenchmarkKeystrokeFrame`,
`BenchmarkKeystrokeFrameTiled`, `BenchmarkCompositorGetCanvas`,
`BenchmarkRenderTerminalUnfocused`; the test binaries of 603eac51 and of this
change run interleaved, eight rounds each): allocations per op are identical on
all fourteen benchmarks, bytes per op within 0.1%, and no time moved by a
significant amount (p between 0.16 and 1.0; geomean 727 us before, 699 us
after). With everything off a frame resolves five settings to off, clears the
canvas as before, and looks nothing up per layer;
`TestBackgroundsOffAllocateNothing` holds the resolve and the clear to zero
allocations.

**All surfaces on, against off** (`BenchmarkBackgrounds`, rail and dock on,
six runs each):

| | off | all | allocs/op off to all |
|---|---|---|---|
| keystroke, 1 pane | 2.30 ms | 3.79 ms | 1253 to 1311 |
| keystroke, 4 panes | 2.06 ms | 2.15 ms | 1057 to 1117 |
| keystroke, 9 panes | 2.16 ms | 2.08 ms | 1045 to 1109 |
| compositor, 1/4/9 panes, all re-rendered | 582/1008/786 us | 678/644/626 us | unchanged |

The one-pane keystroke frame pays the most for the reason the pane background
alone does: its fast path stands down for a painted pane, border or dock, and
only for those, so the desktop and rail backgrounds leave it alone. The extra
allocations are again in emitting the frame, where painted cells carry a
background SGR. The compositor rows do not move beyond noise because a layer
whose string and grounds did not change is copied with its paint; the variance
on the "off" compositor rows is the machine, not the option.

## 2026-09 backgrounds on the fullscreen fast path

The fast path for a lone pane that fills the region stood down whenever the
pane, window chrome or dock background was on, because the paint lived only in
the compositor. It now paints the same rule into its own frame string
(`internal/app/background_fast.go`): the text and escape sequences are copied
through, and the ground's colour SGR is inserted in front of text the pen
leaves without a colour of its own. Which surface a character lands on is
decided by row and by the first and last character of a content row (the
border glyphs), so nothing is measured. A bare reset while a painted colour
is in force is rewritten as a reset of every attribute but that colour, so the
ground is not set again after every styled run; each colour SGR the host
parses boxes a colour, which is an allocation per frame on the client. The
colour sequences are formatted when a ground is resolved and the buffer is
kept, so the paint allocates only the frame string, which the unpainted path
allocates too. `TestFastPathPaintMatchesTheCompositor` renders the same pane
through both paths under eleven background settings and nine pane states
(focused, dock top, bottom and hidden, window mode, unfocused, dimmed, zen,
copy mode cursor and selection, scrolled back with a scrollbar) and requires
identical cells.

Measured on an M3 Pro at 207x55, `-cpu 4`, the test binaries of the benchmark
commit and of this change interleaved old, new, new, old for eight rounds a
side, compared with `benchstat`; the machine was busy, so times carry wide
spreads and are quoted only where p is small. Allocation counts are exact.

| | old | new | allocs/op old to new |
|---|---|---|---|
| keystroke, fullscreen, all off | 1.33 ms | 1.42 ms (p=0.20) | 1134 to 1134 |
| keystroke, fullscreen, all on | 2.08 ms | 1.52 ms (-27%, p=0.005) | 1310 to 1142 |
| keystroke, fullscreen, pane only (`BenchmarkPaneBackground`) | 1.96 ms | 1.45 ms (-26%, p=0.000) | 1302 to 1188 |
| keystroke, 4 panes and rail, all on | 1.87 ms | 1.86 ms | 1117 to 1117 |
| keystroke, 9 panes and rail, all on | 1.75 ms | 1.81 ms | 1109 to 1109 |

With everything off, allocations per op are identical on all forty benchmarks
run (`BenchmarkKeystrokeFrame`, `BenchmarkKeystrokeFrameTiled`,
`BenchmarkCompositorGetCanvas`, `BenchmarkRenderTerminalUnfocused`,
`BenchmarkBackgrounds`, `BenchmarkPaneBackground`), bytes per op within 0.2%,
and no time moved by a significant amount (p between 0.2 and 1.0).

What is left over "off" is not in the compose. Counted on their own, the
compose allocations of a keystroke frame are equal with everything off and
everything on, for the fullscreen pane and for 1, 4 and 9 composed panes. The
rest is the host parsing colour SGRs: the fullscreen frame with every surface
one colour carries 8 more than off; a pane-only ground carries one change of
background per content row, where the border meets the pane (54 more); and a
composed frame starts every row from a bare pen (`frameRenderer.renderLine`,
which matches ultraviolet byte for byte), so it names the ground once per row
(about 60 more). Carrying the pen across rows would remove the last of these,
but changes the frame format for every consumer and was not done.
`PaneBackground/on/panes-9` gained 2 allocations for a reason unrelated to the
paint: `fullscreenFastWindow` no longer returns before `GetVisibleWindows`
when the pane background is on, so it pays what the "off" frame always paid.

The one-pane rows of `BenchmarkBackgrounds` before this change (`panes-1`)
were never on the fast path: that fixture has the rail on, which takes the
fast path away with or without a background. The `fullscreen` rows were added
to measure the case this change is about.

## 2026-09 final pass: re-measured after the r2 agent features

Every baseline above re-measured on main at 62ec9c0c, which adds the r2 agent
features (rich rail rows, Inbox replies, the away recap), the review overlay
and its diff view, the background options and the size cuts, against
e632e021, the commit the last numbers above were taken on.

### Measurement conditions

Apple M3 Pro, 11 cores, macOS, Go 1.27.1, shared with other agents at a load
average of 4 to 8. The test binaries of both commits were built from
`git archive` of each and run alternately (base, new, then new, base) for six
rounds, `-test.cpu 1 -test.benchtime 200ms -test.benchmem` under `nice -n 15`,
compared with `benchstat`. The daemon flood rig ran at `-test.cpu 4` with
`-test.benchtime 3x`. Allocation counts, bytes and wire bytes are exact; a time
is called a change only where `benchstat` gives p < 0.05 and it is larger than
the spread.

### Regressions found

None on the paths this file tracks. Against e632e021:

- **Render path** (`KeystrokeFrame`, `KeystrokeFrameTiled`, `ClientFrame`,
  `CompositorGetCanvas`, `RenderTerminalReal`, `RenderTerminalUnfocused`,
  `RenderWindowBoxFlood`, `Backgrounds`, `PaneBackground`, `PointerSweep`,
  `NotificationTick`): every time reads `~`. Allocations are identical except
  one allocation and about 190 bytes more per keystroke frame (0.1%), below
  anything a frame shows.
- **Rail and Inbox, twelve-agent fleet**: `SidebarAgentsRebuild` fell from
  1217 to 1125 allocations and 44.5 to 43.1 KiB; the cached rail is unchanged
  at 0 allocations. `InboxRender` gained 12 allocations and 3.6 KiB (+4.9%),
  from the reply and recap lines, with time `~`.
- **Daemon**: `PTYOutputChunk`, `PTYBroadcast`, `ScreenSettleArm`,
  `EventPublishNoSubs`, `AttentionOutputEvent` and `AttentionRepeatedBlock`
  still allocate nothing per chunk and read `~` or within 3.5%.
  `WireTerminalStateCaughtUp`, `WireTerminalStateApply` and `SnapshotPack`
  have identical wire bytes and allocations; the packed decodes are 2% to 9%
  faster. A 16 MiB flood through a real daemon (`E2EFlood`) is 240 ms per op on
  both sides, 67 MB/s, with the same batches and one write per op; its profile
  is still the emulator (`Emulator.Write` 28% of the process, the scrollback
  push 13%) and the session's stream and read loop under 5%, so nothing the
  agent features added shows under a flood.
- **vt**: every benchmark reads `~` but `CaptureScrollback`, +2.4% to +3.0%
  (p=0.002) inside a +/-20% spread on the new side. The scrollback's retained memory, from
  `BenchmarkScrollbackRetainedMemory`, is 216.6 bytes a line on both sides,
  1.80 MiB for a 2000 line ring.
- **Input**: `KeyTerminalTyped` 9 allocations, `KeyPrefixChord` 10, the
  motion filter 1, all identical; the resize drag benchmarks read `~`.
- **Manifest engine** (`Classify`): identical allocations, times `~`.
- **Latency** (`TestLatencyEcho`, three rounds a side): echo p50 0.68 to
  0.76 ms at one and four panes on both sides. The daemon round trip is
  bimodal at about 135 us or 200 us on both, as the second daemon pass saw.
- **Idle**: `BenchmarkIdleTick` is still 0 render/tick, 0 work/tick, 296 B/op,
  5 allocs/op. `TestIdleCostStaysLow` writes 0 wire bytes over 10 s on both
  binaries, three runs each, with `ticks` 104 to 105, `work` 0 to 3 and
  `render` 0 to 2 over the whole run on both (the few are boot, not idle).
  Idle daemon CPU, a detached
  daemon with zsh panes over 120 s, from `ps` at 10 ms resolution: 8 panes
  10 ms/min on both, 32 panes 20 ms/min before and 25 ms/min after, the same as
  the agent detection poll entry measured.

`ScrollbackRetainedMemory`, `EmulatorRenderReal`, `PrintCombining` and the
`ResizeDragWithOutput` and `ResizeMotion*` benchmarks were removed from the
tree by the strict test pass. They were measured here by copying the base
commit's benchmark files into a scratch copy of main; nothing was restored.

### New hot spot: the review overlay on a large diff

`BenchmarkReviewFrame` draws the review over a diff of 400 files whose first
file is 5000 lines in one hunk. A frame with nothing changed allocated 2.8 MB.
`reviewRows` lays out every row of the current file, and a frame ran it twice:
once for the diff column and once for the footer, which only asks whether the
cursor is on a note. Both grew their slice by doubling, so each call allocated
about twice the 320 KB the rows need. The footer now reads the rows the diff
column laid out, kept on the review state while `renderReview` runs and
cleared when it returns, and `reviewRows` sizes its slice from the hunks first.
The e2e review tests (`TestReviewOverlayNotesSendCompareAndKeep`,
`TestReviewOverlayAt80x24`, `TestReviewFrameAndStatusLine`,
`TestReviewWrappedNoteIsOneStop`, `TestReviewNotesReachTheAgent`) pass on the
changed binary.

Eight rounds, main against the change:

| `ReviewFrame` | before | after | |
|---|---|---|---|
| `unified/cached` B/op | 2774 KiB | 823 KiB | -70% |
| `unified/cached` time | 1.78 ms | 1.26 ms | -29% (p=0.000) |
| `split/cached` B/op | 2.93 MiB | 1.02 MiB | -65% |
| `split/cached` time | 2.32 ms | 1.76 ms | -24% (p=0.000) |
| `unified/page` B/op | 5.41 MiB | 2.28 MiB | -58% |
| `unified/page` time | 5.07 ms | 3.19 ms | -37% (p=0.002) |
| `unified/first` B/op | 8.47 MiB | 6.56 MiB | -23% |
| `unified/end` B/op | 11.30 MiB | 7.80 MiB | -31% |

A page down still lays the file out once more in `ReviewMove`, which is one
call per key. What is left of a first frame and a jump to the end is chroma
tokenising the lines on screen (`regexp2` and `chroma.matchRules`, about 80%
of the bytes), which is upstream.

### Measured and deliberately not changed

- **`applyInboxEvents`** now costs 11 us, 7.4 KiB and 43 allocations for a
  burst of sixteen events, against 2.8 us, 2.5 KiB and 8 when the Inbox entry
  above was written. The Inbox keeps the cursor on the item it was on
  (`clampInboxSelection`, since 8aae8a68), which builds the overlay's rows
  three times per burst. A burst arrives at most once per 150 ms, so this is
  about 0.007% of a core; caching the rows would add an invalidation surface to
  the Inbox for nothing a person can notice.
- **A twelve-pane agent fleet with the rail on.** A scratch benchmark (tiled,
  207x55, twelve agent panes in every state, rail on) composes a keystroke
  frame in 1.76 ms and 1096 allocations, the same time as nine plain tiled
  panes (1.75 ms, 920 allocations): the rail is served from its cache. A frame
  where one agent changes state costs 2.17 ms and 2554 allocations, the rail
  rebuild. Nothing to take out.
- **The extra allocation on a keystroke frame.** One allocation and about 190
  bytes per frame, identical on every keystroke benchmark. 0.1% of the frame,
  not chased.

### Binary size budget

`.github/workflows/binary-size.yml` runs `scripts/binary-size.sh` on every pull
request and push to main. It builds tuios the way the release does
(`CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w"`) for linux/amd64 and
darwin/arm64 with the Go version go.mod names, prints the size, and fails when
a binary is over its budget.

| target | size at 62ec9c0c (Go 1.26.6) | before the size cuts (e632e021) | size at the last raise (Go 1.26.9) | budget |
|---|---|---|---|---|
| linux/amd64 | 25,182,370 | 26,681,504 | 36,171,938 | 36,275,000 |
| darwin/arm64 | 23,834,594 | 25,265,154 | 34,046,050 | 34,150,000 |

The first budgets were about 3% above the size they were set at and below the
size before the size cuts, so undoing those cuts failed the job. Each raise
since then is recorded below, oldest first.

The linux/amd64 budget went from 26,000,000 to 26,400,000 when hints mode,
mail compose, the host fence and multi copy mode brought the build to
26,001,570 bytes (Go 1.26.6). The new budget is still below the size before the
size cuts, so undoing those cuts still fails the job. A Go 1.27 toolchain
builds binaries up to about 120 KB larger than 1.26.6, which is inside the room.

The budgets went to 27,500,000 (linux/amd64) and 25,970,000 (darwin/arm64)
when the subagent count, the report-agent-activity verb with its rate limit
and the expiry of a subagent that never stopped, brought the build to
27,480,226 and 25,944,850 bytes (Go 1.26.6). Main had 23,022 and 5,998 bytes
of room before it, so the feature could not fit in either.

The budgets went to 27,600,000 (linux/amd64) and 26,060,000 (darwin/arm64)
when the master position and master count (issue #321) brought the build to
27,566,242 and 26,029,442 bytes (Go 1.26.6). Main had 7,486 and 8,270 bytes of
room before it, and the feature adds about 74 KB and 68 KB: the tiler, the
MsgMasterLayout op, ten actions with their palette rows, and seven run-command
commands.

The budgets went to 27,670,000 (linux/amd64) and 26,130,000 (darwin/arm64)
when the window_size policy (the daemon's size policy and the view a client
draws of a larger session) brought the build to 27,639,970 and 26,096,898
bytes (Go 1.26.6). Main had 33,758 and 30,558 bytes of room before it, and the
feature adds about 74 KB and 68 KB.

The budgets went to 27,710,000 (linux/amd64) and 26,170,000 (darwin/arm64)
when the files list learned to follow a pane on another machine (#313): the
daemon pushes a pane's new folder, watches a listed folder for a client, and
answers the wait-dir verb. That brought the build to 27,689,122 and 26,148,114
bytes (Go 1.26.6), about 50 KB more than main, which had about 30 KB and 33 KB
of room.

The budgets went to 27,905,000 (linux/amd64) and 26,330,000 (darwin/arm64)
when the herdr command front (internal/herdrcli) and the herdr pane methods
brought the build to 27,873,442 and 26,298,690 bytes (Go 1.26.6). Main was
27,689,122 and 26,148,114 bytes. The feature adds 184,320 and 150,576 bytes:
a flag for flag port of herdr's CLI, and the pane navigation, swap, zoom,
process and agent start methods.

The budgets went to 27,960,000 (linux/amd64) and 26,395,000 (darwin/arm64)
when the pure Go emulator learned to reflow its screen on a resize. That
brought the build to 27,930,786 and 26,365,842 bytes (Go 1.26.6), 25,786 and
35,842 bytes over the old budgets. The reflow keeps text that a narrowing
resize used to cut for good, and carries the padding flag in snapshots.

The budgets went to 28,000,000 (linux/amd64) and 26,440,000 (darwin/arm64)
after the graphics fixes (#342, #344) and the session cleanup (#334) landed
together. That brought the build to 27,959,458 and 26,399,730 bytes
(Go 1.26.6): 542 bytes under and 4,730 bytes over the old budgets. Each PR fit
on its own; the sum did not.

The budgets went to 28,065,000 (linux/amd64) and 26,485,000 (darwin/arm64)
when the daemon learned to pace a kitty graphics pane to its fastest client
(#345): it cuts a pane's output into text and frames, drops a frame a slow
client has not taken when a newer one replaces it, and holds the pane only
while every client is behind. That brought the build to 28,029,090 and
26,451,634 bytes (Go 1.26.6). Main was 27,992,226 and 26,417,170 bytes, with
7,774 and 22,830 bytes of room, and the change adds 36,864 and 34,464 bytes.
CI measured linux/amd64 about 8 KB above a local build, so the budgets leave
about 35 KB.

The darwin/arm64 budget went to 26,520,000 when a signal-wedged client learned
to exit (#207): the signal policy, its grace exit and the termios restore.
That brought the build to 26,485,042 bytes (Go 1.26.6), 42 bytes over. Main
was 26,451,762 bytes, and the change adds 33,280 bytes. linux/amd64 grew 8,192
bytes to 28,041,378 and still fits.

The linux/amd64 budget went to 28,095,000 when Neovim pane navigation (#318)
landed: OSC 7777, the gate that accepts a focus request only as the answer to
a forwarded focus key, and DECSET 1004 focus reports to panes. That brought
the build to 28,061,858 bytes (Go 1.26.6), 3,142 bytes under the old budget,
which is less than CI can measure above a local build. Main was 28,041,378
bytes, and the change adds 20,480 bytes. darwin/arm64 grew 17,296 bytes to
26,502,338 and still fits.

The budgets went to 28,125,000 (linux/amd64) and 26,555,000 (darwin/arm64)
when Crush's permission prompt could be answered from the Inbox (#320): the
dialog reader in internal/harness, the Crush claim override and process check
in internal/session, and the Inbox row width cap. That brought the build to
28,090,530 and 26,519,058 bytes (Go 1.26.6), with 4,470 and 942 bytes of room
left. Main was 28,061,858 and 26,502,338 bytes, and the change adds 28,672 and
16,720 bytes.

The budgets went to 28,135,000 (linux/amd64) and 26,565,000 (darwin/arm64)
when long messages could be read in full (#381): the message view, the log
viewer on the shared list overlay, the hover hold and label on the dock's
message block, and the linear wrap with its cache. That brought the build to
28,127,394 and 26,552,978 bytes (Go 1.26.6), with 7,606 and 12,022 bytes of room
left. Main was 28,094,626 and 26,519,266 bytes, and the change adds 32,768 and
33,712 bytes.

The budgets went to 28,205,000 (linux/amd64) and 26,620,000 (darwin/arm64)
for the agent switch, `[agents] enabled = false` (#387): the switch and its
refusals in internal/session, the client gates in internal/app, and the CLI
and MCP refusals. Measured with `scripts/binary-size.sh` against main on one
toolchain, the change adds 45,056 and 34,160 bytes, which puts the build at
about 28,172,450 and 26,587,138 bytes on Go 1.26.6.

The budgets went to 28,255,000 (linux/amd64) and 26,665,000 (darwin/arm64)
for the master-stack splits (#403) and the master-stack equalize (#405): the
weights the tiler reads for every pane, their inverse from the rectangles,
and the session state that carries them. On the CI toolchain #403 alone put
the build at 28,213,410 and 26,620,498 bytes, 8,410 and 498 over. Measured
with `scripts/binary-size.sh` against main on one toolchain, #403 adds 28,672
and 16,800 bytes and #405 adds 4,096 and 0.

The budgets went to 28,320,000 (linux/amd64) and 26,725,000 (darwin/arm64)
for the client list (#391): the `list-clients` verb and command, the peer pid
read on each platform, and the `client-session-changed` event. On Go 1.26.6
the build measured 28,283,042 and 26,688,242 bytes, 28,042 and 23,242 over
the old budgets.

The budgets went to 31,092,000 (linux/amd64) and 29,297,000 (darwin/arm64)
for the October 2026 work, which landed as one series. On Go 1.26.6 main
(a640011) measured 28,283,042 and 26,688,242 bytes, and the series brings the
build to 31,051,938 and 29,257,058 bytes: 2,768,896 and 2,568,816 bytes more.
Each row below is what one change adds to the rows above it, measured with the
release flags after applying the changes to main in this order. A linux/amd64
build grows in whole 4 KB pages, so a row of 8 KB or less there is mostly
rounding. The rows add up to 12,288 and 32 bytes less than the totals,
because the measurement left out one conflicting line of the notifications
change and the fixes made in the final review.

| change | linux/amd64 | darwin/arm64 |
|---|---|---|
| Stream races, MCP annotations and protocol versions, ACP cancel | +16,384 | +16,960 |
| A pane TERM with a terminfo entry, the kitty query, DECRQM and DA1 | +8,192 | +64 |
| Hot paths: wait-for coalescing, capture presize, DECOM cache, marker trim | +8,192 | +16,688 |
| Request ids on the wire | +8,192 | +432 |
| tmux shim: command prefixes, tmux 3.4 formats, wait-for, popups, run-shell, if-shell, moves, buffers, environment | +172,032 | +151,872 |
| Terminal protocol: XTVERSION, DA3, XTGETTCAP, DECRQSS, OSC reply terminators, a kitty stack per screen, mouse encodings, modifyOtherKeys, the title stack | +36,864 | +33,520 |
| Turn checkpoints | +147,456 | +134,768 |
| Bulk history readers | +28,672 | +17,648 |
| Inbox push notifications, the web deep link and `tuios notify test`, as first written with curl | +122,880 | +118,480 |
| net/http for notifications and `tuios update`, in place of curl | +1,941,504 | +1,841,440 |
| `tuios ship`, `fan keep --merge` and the pull request badge | +208,896 | +186,016 |
| Mode legends in the dock | +8,192 | -48 |
| Emulator allocations, the scroll window, the idle ticker and memtrim | +12,288 | +17,104 |
| Opening links and OSC 8 in the frame | +28,672 | +17,120 |
| Review fixes: the pushed commit, ignored files on restore, the push address, notification redirects, large untracked files, key state on reattach | +8,192 | +16,720 |

The net/http row is 70% of the growth. tuios linked neither net/http nor
crypto/tls before it. The maintainer accepted the 1.9 MB so that tuios needs
no curl at run time: notifications and `tuios update` used to run curl from
PATH, and a machine without it could not send either.

The budgets went to 31,130,000 (linux/amd64) and 29,345,000 (darwin/arm64)
for the rail's custom section (#399): the `[appearance.sidebar.custom]` table,
its parser and validator, the section in the renderer, and the rail context
and re-run in the dock engine. On Go 1.26.6 the build measured 31,092,898 and
29,307,394 bytes, 898 and 10,394 over the old budgets.

The linux/amd64 budget went to 31,170,000 for the CLI help audit (#430): the
`--json` output of `tape list`, `layout list`, `resurrect`, `set-config` and
`send-text`, and the longer help and error text. On Go 1.26.6 the build
measured 31,133,858 bytes, 3,858 over the old budget. darwin/arm64 stayed
under its budget at 29,341,282 bytes.

The darwin/arm64 budget went to 29,415,000 for the complete `tuios keybinds
list` (#434): its rows, the JSON output, the scoped descriptions and the fixed
key groups shared with the help overlay. On Go 1.26.6 the build measured
29,375,090 bytes, 30,090 over the old budget. linux/amd64 stayed under its
budget at 31,162,530 bytes.

The linux/amd64 budget went to 31,210,000 for the refreshed skill (#429): the
new `checkpoints`, `ship`, `notify`, `clients` and `agents-off` topics that
`tuios --skill` prints from the binary. On Go 1.26.6 the build measured
31,174,818 bytes, 4,818 over the old budget. darwin/arm64 stayed under its
budget at 29,375,138 bytes.

Both budgets went up for tape actions and condition waits (#437): the
`Action`, `Press`, `WaitFor`, `Expect`, `Run` and `Source` commands, the
argument specs every command is checked against, and the action runner
that run-command now reaches. On Go 1.26.6 the build measured 31,256,738
bytes for linux/amd64, 46,738 over the old budget, and 29,458,610 bytes for
darwin/arm64, 43,610 over.

The budgets went to 31,340,000 (linux/amd64) and 29,540,000 (darwin/arm64) for
the herdr methods that plugins call. On Go 1.26.6 the build measured
31,305,890 and 29,509,922 bytes.

The budgets went to 31,670,000 (linux/amd64) and 29,830,000 (darwin/arm64) for
the herdr plugin host. On Go 1.26.6 the build measured 31,633,570 and
29,797,122 bytes.

The budgets went to 31,820,000 (linux/amd64) and 29,980,000 (darwin/arm64)
for the opt-in explorers: `tuios help -i`, `tuios help --json`, `tuios config
browse` and `tuios keybinds browse`, and the `internal/explore` panel behind
them. On Go 1.26.6 the build measured 31,781,026 and 29,947,586 bytes,
111,026 and 117,586 over the old budgets.

The budgets went to 31,945,000 (linux/amd64) and 30,105,000 (darwin/arm64)
for `tuios hosts sync`: the probe, install and restart scripts, the dev
cross-build, the release download by tag, the restart question and the
report. It links no new package. On Go 1.26.6 the build measured 31,908,002
and 30,066,354 bytes, 88,002 and 86,354 over the old budgets.

The budgets went to 32,015,000 (linux/amd64) and 30,155,000 (darwin/arm64)
for the sessionizer work: `tuios switch-session`, its verb, and the start
directory of a session. It links no new package. On Go 1.26.6 the build
measured 31,977,634 and 30,116,962 bytes, 32,634 and 11,962 over the old
budgets.

The budgets went to 32,075,000 (linux/amd64) and 30,210,000 (darwin/arm64)
for Tailscale SSH check mode in the host commands: the gate reader, the wait
for an approval in the link and in `hosts sync`, and the shared ssh
connection of a sync run. It links no new package. On Go 1.26.6 the build
measured 32,034,978 and 30,167,794 bytes, 19,978 and 12,794 over the old
budgets.

The budgets went to 32,140,000 (linux/amd64) and 30,275,000 (darwin/arm64)
for the pane labels (`display_panes`): the block font, the label pass over
the canvas and the `[panes]` section. It links no new package. On Go 1.26.6
the build measured 32,084,130 and 30,219,138 bytes, 9,130 and 9,138 over the
old budgets.

The budgets went to 32,185,000 (linux/amd64) and 30,330,000 (darwin/arm64)
for ssh-aware splits: the ssh and mosh line parser, the process tree checks,
the `ssh -G` host match, and the three actions. It links no new package. On
Go 1.26.6 the build measured 32,141,474 and 30,286,114 bytes, 1,474 and 11,114
over the old budgets.

The budgets went to 32,365,000 (linux/amd64) and 30,480,000 (darwin/arm64)
for the pane navigator (`choose_tree`) and `list-windows --all`: the tree,
its search and preview, the load of the other sessions, and the listing
across sessions and hosts. It links no new package. On Go 1.26.6 the build
measured 32,305,314 and 30,420,946 bytes, 120,314 and 90,946 over the old
budgets.

The budgets went to 32,445,000 (linux/amd64) and 30,550,000 (darwin/arm64)
for the settings page's Agents tab: the integration report the tab shares
with `tuios doctor agents`, the install and uninstall actions, the notice and
its stored dismissals, and the check that a file did not change during an
edit. It links no new package. On Go 1.26.6 the build measured 32,399,522 and
30,505,330 bytes, 34,522 and 25,330 over the old budgets.

The budgets went to 32,600,000 (linux/amd64) and 30,690,000 (darwin/arm64)
for the Tailscale SSH sign-in: the rail's sign-in control and hover, the check
that a sign-in link is on a Tailscale login origin, the `retry-host` verb, and
`tuios hosts signin`. It links no new package. On Go 1.26.6 the build measured
32,571,554 and 30,639,922 bytes: linux/amd64 was 46,554 over its budget of
32,525,000, and darwin/arm64 had 10,078 left of its 30,650,000.

The linux/amd64 budget went from 32,600,000 to 32,700,000 when the rail's
opt-in switch numbers brought the build to 32,600,226 bytes (Go 1.26.6). Main
had 226 bytes of room before it, so the feature could not fit. The
darwin/arm64 budget stays at 30,690,000: the feature brought that build to
30,673,362 bytes, inside the room it already had.

The darwin/arm64 budget went from 30,690,000 to 30,760,000 for the
borderless zoom: the option, its settings row, and the zoom path that drops
the border. With the copy-mode help order before it, the build measured
30,690,498 bytes on Go 1.26.6, 498 over the old budget. The which-key menus
and the dock mode icons come next and add about 20 KB, which fits in the room
this leaves.

The budgets went to 32,825,000 (linux/amd64) and 30,875,000 (darwin/arm64)
for OSC 7501, the Program Status Protocol: the parser and record store in
`internal/progstatus`, the records in the daemon and the window state, the
rail and Inbox display, the reports to the host terminal, and `tuios status`.
It links no new package. On Go 1.26.6 the build measured 32,792,738 and
30,842,578 bytes: linux/amd64 was 92,738 over its budget of 32,700,000, and
darwin/arm64 82,578 over its 30,760,000.

The budgets went to 33,100,000 (linux/amd64) and 31,140,000 (darwin/arm64)
for config includes and config.d (#518): the merge of several files, the
three-way save that edits the lines of the changed key in the file that holds
it, the watcher that follows every file, and the config files, origin and
prune commands. That brought the build to 33,034,402 and 31,077,826 bytes
(Go 1.26.6) on main at 01d72efb, about 209 KB and 203 KB over the old budgets.

The budgets went to 33,172,000 (linux/amd64) and 31,198,000 (darwin/arm64)
for images drawn as block glyphs on a host without graphics: the glyph
encoder (`shot/mosaic`), its glyph tables, the 16-colour floor, the
drawing pass and the `image_symbols` option. It links no new package. On Go
1.26.6 the build measured 33,136,802 and 31,162,594 bytes, 69,632 and 51,248
over origin/main at ebeee191. On linux/amd64, 64 KiB of that is a section
crossing an alignment boundary, not code.

The budgets went to 33,250,000 (linux/amd64) and 31,290,000 (darwin/arm64)
for following the person's ssh agent (#552): the link keeper for sessions and
hosts, the ssh -G check of a host's forwarding, the ssh-agent-path verb and
command, and the agent field of the link handshake. On main at d42b9d06 the
build measured 33,194,146 and 31,230,146 bytes on Go 1.26.6, 22,146 and
32,146 over the old budgets.

The budgets went to 33,450,000 (linux/amd64) and 31,460,000 (darwin/arm64)
for the paste buffers (#514): the store and the session each buffer came
from, the five buffer verbs and their commands, base64 content and uploads,
the chooser, and the tmux shim's use of the daemon's buffers. It links no new
package. On main at 1b570369 the build measured 33,370,274 and 31,383,090
bytes on Go 1.26.6, 120,274 and 93,090 over the old budgets.

The budgets went to 33,500,000 (linux/amd64) and 31,540,000 (darwin/arm64)
for bounding the memory clients can make the daemon hold (#557): the frame
reader that charges a frame before it reads it, the read budgets it shares
with the verb line reader, the connection caps and their refusals, the wait
for a pane's large write slot, and the client's hold and retry of a refused
paste. It links golang.org/x/sync/semaphore, which was already in the module
graph. On Go 1.26.6 the build measured 33,431,714 and 31,467,538 bytes, 45,056
and 51,296 over origin/main at d2f6277b. darwin/arm64 was 7,538 bytes over the
old budget.

The budgets went to 34,000,000 (linux/amd64) and 32,000,000 (darwin/arm64)
when main moved to the go1.26.9 toolchain and newer golang.org/x modules. On
origin/main at 370097e6 the build measured 33,493,154 and 31,536,434 bytes,
6,846 and 3,566 bytes under the old budgets. The glyph fallback for hosts that
store images in their cells (#583, issue 567) added 8,192 and 32 bytes: the
xterm.js check in the startup probe and the handler for a late XTVERSION
answer. That put linux/amd64 1,346 bytes over. Both budgets now have about
500 KB of room, and both stay under the 35 MB ceiling.

The budgets went to 34,600,000 (linux/amd64) and 32,600,000 (darwin/arm64)
for the phone apps. stream-pane, attach-presence and the size lease (#602),
agent-transcript (#603) and Web Push with RFC 8291 encryption (#608) landed
first, and `tuios pair` (#607) adds the QR encoder (rsc.io/qr) and the
pairing listener. With #607 the build measured 34,046,114 and 32,043,810
bytes, 46,114 and 43,810 over the old budgets. Both budgets again have about
550 KB of room, and linux/amd64 is now within 400 KB of the 35 MB ceiling, so
the next feature that needs room should look for bytes to cut first.

The budgets went to 34,900,000 (linux/amd64) and 32,900,000 (darwin/arm64)
for the file transfer engine (#620): bulk streams on the link, the file
verbs, copies the daemon runs and journals, and the confinement of link file
access to `files_roots` and the deny lists. With it the build measured
34,676,898 and 32,634,946 bytes, 524,288 and 489,328 bytes over origin/main
at a8ae79e4 (34,152,610 and 32,145,618), and 76,898 and 34,946 bytes over the
old budgets. Of that, 106,496 bytes are the Unicode normalisation tables of
golang.org/x/text/unicode/norm. The deny lists need them: APFS opens a name
in either Unicode normal form, so a deny match must compare both. The file
previews of #597 stay off main, which keeps out about 217 KB of image
decoders. The ceiling is now 40 MB.

The linux/amd64 budget went to 35,000,000 for tuios pane-view (#630): the
command, the stream-pane client, the query filter, the viewer leases and the
viewer reports. The build measured 34,910,370 bytes in CI, 10,370 over the old
budget; pane-view adds about 74 KB over main (34,705,568 to 34,779,296 with a
local Go 1.26 build). darwin/arm64 stayed inside its budget. This is the last
raise under 35 MB: the next feature looks for bytes to cut first.

The darwin/arm64 budget went to 33,400,000 for the subagent rail options
(#640): tree lines, the heading that opens and closes the rows, the field
list, the row cap and the early hide, each settable per rail edge. The build
measured 32,904,754 bytes in CI, 4,754 over the old budget. The feature adds
about 50 KB over main (32,674,210 to 32,724,722 with a local Go 1.26 build).
No single dependency grew: it is rail code and option checks. The budget has
about 500 KB of room again, under the 40 MB ceiling. linux/amd64 stayed
inside its budget.

The linux/amd64 budget went to 35,500,000 for the display follow (#637): a
desktop client hands new panes its Wayland and X11 display, so clipboard and
image paste work in the panes of a daemon that ssh started. The CI build
measured 35,037,346 bytes, 37,346 over the old budget, on top of the one
daemon per user change (#635) that landed just before. The budget has about
460 KB of room again, under the 40 MB ceiling.

The budgets went to 35,700,000 (linux/amd64) and 33,600,000 (darwin/arm64)
for `tuios cp`, `tuios transfers` and the copies in the TUI: the tree stream,
check-ahead, compression with skip, the conflict state, the speed test, the
two commands and their progress output, the dock item, the Copies panel and
the send and drop flows. On origin/main at b228b387 (34,840,738 and
32,769,954 bytes on Go 1.26.9) the branch measured 35,483,810 and 33,379,042,
643,072 and 609,088 bytes more. Rebased on 377bc074, after pane-view, it
measured 35,565,730 and 33,463,378. The compression is compress/flate, which the
binary held already. No dependency was added.

They went on to 35,900,000 and 33,800,000 with the review fixes of #636 (the
pane's start folder, the os.Root for a pane's copy, the escaping of far names
in the CLI and of C1 controls in the daemon's lines) and main's rail options.
Rebased on e70a72fe the build measured 35,672,226 and 33,564,050 bytes on Go
1.26.9, which left under 40 KB of room.

The cold-path sorts (internal/sortx) cut 331,776 bytes from linux/amd64 and
236,864 from darwin/arm64. Each slices.SortFunc or slices.SortStableFunc call
with a new element type compiles its own copy of the sort. Nineteen calls on
cold paths (CLI output, verb listings, config and manifest load, writes to
disk) now go through sort.Slice and sort.SliceStable, which are compiled once.
The order is the same, ties included, and internal/sortx tests that. Calls on a
render, keystroke, event-burst or broadcast path keep slices. On origin/main at
0cbbffbc (Go 1.26.9) the build measured 35,844,258 and 33,699,490 bytes, and
the branch measured 35,512,482 and 33,462,626.

The budgets went down to 35,570,000 (linux/amd64) and 33,565,000
(darwin/arm64) after the cold-path sorts, so the bytes they freed are not
spent unnoticed. Each budget keeps the room it had before the cut, 57,518 and
102,374 bytes.

Packing the skill files cut 110,592 bytes from linux/amd64 and 115,520 from
darwin/arm64. skills/tuios/*.md (184,379 bytes) go into the binary as one
gzipped tar of 65,676 bytes, unpacked the first time a skill is read. Every
`tuios --skill` output is byte-identical, and TestArchiveMatchesFiles holds
the archive to the files. On origin/main at 1a34acbe, after the cold-path
sorts (Go 1.26.9), the build measured 35,516,578 and 33,462,786 bytes, and the
branch measured 35,405,986 and 33,347,266. `tuios --skill all` takes 1.4 ms
longer to unpack: on an idle machine the median of 30 runs went from 3.81 ms
to 5.23 ms.

The budgets went down again to 35,460,000 (linux/amd64) and 33,450,000
(darwin/arm64) after the skill archive. Each keeps the room it had before the
cut, 54,014 and 102,734 bytes. The two cuts together took 440,000 and 350,000
bytes off the budgets.

The budgets went to 35,560,000 (linux/amd64) and 33,500,000 (darwin/arm64)
for `--server NAME` (#680), which runs each named server as its own daemon
with its own sessions. Main at 4c2de743 measured 35,451,042 and 33,380,818
bytes (Go 1.26.9), 8,958 bytes under the linux/amd64 budget. With #680 on top
the build measured 35,463,330 and 33,397,362, 3,330 bytes over that budget.
The new budgets leave 96,670 and 102,638 bytes of room above that build.

The budgets went to 35,940,000 (linux/amd64) and 33,880,000 (darwin/arm64)
for `tuios gui-bridge` (#658), the process the desktop GUI runs to drive a
session: the bridge itself (internal/guibridge), the layout edits by pane id,
stacks, the gui keybinding scope and window kinds. Main at cfc254fb measured
35,557,538 and 33,499,410 bytes (Go 1.26.9). With #658 on top the build
measured 35,836,066 and 33,775,234, 278,528 and 275,824 bytes more. No
dependency was added. Of the symbols, internal/guibridge is 87 KB and
internal/app 36 KB, spread over the bridge's handlers with no large table to
cut. The new budgets leave 103,934 and 104,766 bytes of room.

The budgets went to 36,110,000 (linux/amd64) and 34,000,000 (darwin/arm64)
for `file-preview` (#659), the verb behind the GUI's Quick Look, and the
bridge's verbs on a host. With #658 under it the build measured 35,836,066
and 33,775,234 bytes (Go 1.26.9), and with #659 36,008,098 and 33,894,034,
172,032 and 118,800 bytes more. The image scaler from golang.org/x/image/draw
(already in go.mod) is 94,208 and 66,640 of it: ApproxBiLinear links a
specialised loop for each pair of image types. A small scaler of our own over
image.RGBA would take most of that back. The new budgets leave 101,902 and
105,966 bytes of room.

The budgets went to 36,275,000 (linux/amd64) and 34,150,000 (darwin/arm64)
for search-panes, command-history, list-ports and started_by (#666). The two
pull requests under it fit the budgets #659 set: the bridge's output path
(#661) added 8,192 and 16,544 bytes, and review-stage (#662) 69,632 and
67,264. With #662 the build measured 36,085,922 and 33,977,842 bytes (Go
1.26.9), and with #666 36,171,938 and 34,046,050, 86,016 and 68,208 bytes
more. That is after the three new sorts went through internal/sortx, which
took 36,864 and 33,552 bytes off. No dependency was added. The new budgets
leave 103,062 and 103,950 bytes of room.

The budgets went to 36,395,000 (linux/amd64) and 34,265,000 (darwin/arm64)
for the session rails (#626), which dock a daemon session's panes in a
sidebar rail. Main at 6287dadc measured 36,212,898 and 34,096,418 bytes (Go
1.26.9), and with #626 36,294,818 and 34,164,034, 81,920 and 67,616 bytes
more. The rail viewer is a second daemon client with its own wiring, state
queue and retry backoff, plus the rail settings. No dependency was added. The
new budgets leave 100,182 and 100,966 bytes of room.

The budgets went to 36,515,000 (linux/amd64) and 34,385,000 (darwin/arm64)
for the Omarchy theme (#734 and the pull requests on it). Main at 4ea588d8
measured 36,303,010 and 34,164,114 bytes (Go 1.26.9), 91,990 and 100,886
bytes under the budgets. The omarchy theme id, the swatch library (the
colors.toml resolver, the Omarchy source and the watcher), the list-themes
report, the live follow and the move of the terminal scheme parsers into
swatch/termconfig bring it to 36,413,602 and 34,282,098, 110,592 and 117,984
bytes more. No third-party dependency was added: fsnotify and go-toml were
already in the build, and tuios.dev/swatch is a module in this repository.
The new budgets leave 101,398 and 102,902 bytes of room.

The budgets went to 36,585,000 (linux/amd64) and 34,415,000 (darwin/arm64)
for the v0.10.0 batch. Main at ce82a422 measured 36,491,426 and 34,350,018
bytes (Go 1.26.9), 23,574 and 34,982 bytes under the budgets. The batch
brings it to 36,552,866 and 34,384,546, 61,440 and 34,528 bytes more. By pull
request, linux/amd64 then darwin/arm64:

| pull request | linux/amd64 | darwin/arm64 |
|---|---|---|
| #749 new-worktree and fan pass the caller's terminal | 0 | 0 |
| #752 stable centered dock components | 0 | 0 |
| #715 `[dock.tabs]` workspace tab styles | 20,480 | 240 |
| #717 `dim_foreground` | 0 | 16,560 |
| #718 tmux-style actions with no default key | 36,864 | 17,040 |
| #754 goreleaser-action 7.3.0 | 0 | 0 |
| #755 charmtone and charmtone-light themes | 4,096 | 688 |

No dependency was added. The new budgets leave 32,134 and 30,454 bytes of
room.

The budgets went to 36,620,000 (linux/amd64) and 34,465,000 (darwin/arm64)
for the theme transition (#757), which shows a change of theme as a circle
that grows from the middle of the screen, or as a fade. Main at 81bd3d46
measured 36,573,346 and 34,401,442 bytes (Go 1.26.9), 11,654 and 13,558 bytes
under the budgets. The transition brings it to 36,589,730 and 34,434,962,
16,384 and 33,520 bytes more: the pass over the composed frame, the blend
table and the two options. No dependency was added. The new budgets leave
30,270 and 30,038 bytes of room.

The budgets went to 36,685,000 (linux/amd64) and 34,535,000 (darwin/arm64)
for the batch of #760 to #767. Main at b676cc47 measured 36,593,826 and
34,434,978 bytes (Go 1.26.9), 26,174 and 30,022 bytes under the budgets. The
batch brings it to 36,655,266 and 34,502,466, 61,440 and 67,488 bytes more. By
pull request, linux/amd64 then darwin/arm64:

| pull request | linux/amd64 | darwin/arm64 |
|---|---|---|
| #760 a heading per space in the rail | 8,192 | 16,592 |
| #761 `ls` groups by space | 4,096 | 32 |
| #762 the client releases its emulator buffers | 4,096 | 128 |
| #764 new-window `--keep-open` and `--wait` | 20,480 | 17,088 |
| #765 the skill stub with the hooks | 16,384 | 16,928 |
| #766 capture-pane `--unwrap` and `--trim` | 8,192 | 16,720 |
| #767 the session start hint | 0 | 0 |

No dependency was added. The new budgets leave 29,734 and 32,534 bytes of
room.

The budgets went to 36,720,000 (linux/amd64) and 34,565,000 (darwin/arm64)
for the batch of #781, #782, #787 and #790. Main at 0a376af9 measured
36,679,842 and 34,519,682 bytes (Go 1.26.9), 5,158 and 15,318 bytes under the
budgets. The batch brings it to 36,688,034 and 34,536,434, 8,192 and 16,752
bytes more. By pull request, linux/amd64 then darwin/arm64:

| pull request | linux/amd64 | darwin/arm64 |
|---|---|---|
| #781 the frame ticker stays idle for a dock frame | 8,192 | 112 |
| #782 the frame ticker stops when the client is idle | 0 | 0 |
| #787 the maintenance tick runs only when a job is due | 0 | 16,640 |
| #790 the clock without seconds by default | 0 | 0 |

No dependency was added. The new budgets leave 31,966 and 28,566 bytes of
room.

The budgets went to 36,820,000 (linux/amd64) and 34,665,000 (darwin/arm64)
for the batch of #772, #780, #789, #791, #788, #792, #773, #796 and #795.
Main at f125f62d measured 36,704,418 and 34,536,674 bytes (Go 1.26.9), 15,582
and 28,326 bytes under the budgets. The batch brings it to 36,790,434 and
34,637,650, 86,016 and 100,976 bytes more. By pull request, linux/amd64 then
darwin/arm64, with the regenerated skill archive counted in the pull request
it follows:

| pull request | linux/amd64 | darwin/arm64 |
|---|---|---|
| #772 CharmTone from charm.land/tone | 0 | 0 |
| #780 one parser for every colour key | 24,576 | 17,232 |
| #789 a theme inherits from a base | 16,384 | 16,848 |
| #791 theme and glyph files reload on change | 16,384 | 33,264 |
| #788 a theme preview only for the client that asked | 8,192 | 96 |
| #792 theme picker keys for shade, shuffle, copy and delete | 12,288 | 16,640 |
| #773 the prefix repeats the real resize and scroll actions | 0 | 208 |
| #796 a list of the actions that repeat after the leader | 0 | 16,512 |
| #795 last_workspace, break_pane, last_session and send_prefix | 8,192 | 176 |

#772 replaces one CharmTone module with another and adds no bytes. The new
budgets leave 29,566 and 27,350 bytes of room.

The budgets went to 36,900,000 (linux/amd64) and 34,740,000 (darwin/arm64)
for the batch of #800, #801, #806, #802 and #805. Main after the batch of #772
to #795 measured 36,790,434 and 34,637,650 bytes (Go 1.26.9), 29,566 and
27,350 bytes under the budgets. The batch brings it to 36,868,258 and
34,707,810, 77,824 and 70,160 bytes more. By pull request, linux/amd64 then
darwin/arm64:

| pull request | linux/amd64 | darwin/arm64 |
|---|---|---|
| #800 keybind presets, with the tmux preset | 53,248 | 50,176 |
| #801 the screen and zellij presets, and leader_twice | 16,384 | 16,688 |
| #806 the preset keys a user binding takes | 8,192 | 16,512 |
| #802 the keybind preset picker and a welcome offer | 36,864 | 17,216 |
| #805 the which-key menus as tables | -36,864 | -30,432 |

#805 gives back most of what #802 adds. No dependency was added. The new
budgets leave 31,742 and 32,190 bytes of room.

The budgets went to 37,517,000 (linux/amd64) and 35,280,000 (darwin/arm64)
for the reply composer and the Inbox cockpit, #769 to #835. Main after #840
measured 36,884,642 and 34,708,434 bytes (Go 1.26.9), 15,358 and 31,566 bytes
under the budgets. The stack brings it to 37,486,754 and 35,249,474, 602,112
and 541,040 bytes more. By pull request, linux/amd64 then darwin/arm64, with
the regenerated skill archive counted in the pull request it follows:

| pull request | linux/amd64 | darwin/arm64 |
|---|---|---|
| #769 a multiline editor for agent replies | 0 | 0 |
| #770 write the reply with the composer | 77,824 | 67,280 |
| #771 open, expand and remove a paste block | 16,384 | 16,608 |
| #812 queue a reply with parts | 40,960 | 50,768 |
| #814 attach a clipboard image to a reply | 36,864 | 33,728 |
| #817 preview attached images | 81,920 | 68,320 |
| #819 the agents view | 61,440 | 67,616 |
| #822 the agent thread | 86,016 | 68,032 |
| #823 view an agent's pane and come back | 8,192 | 16 |
| #825 act on many agents | 53,248 | 50,016 |
| #826 name a pane, an agent or a file with @ | 36,864 | 50,976 |
| #828 the Inbox's one-line editors use the composer | 12,288 | 0 |
| #830 write mail with the composer | 8,192 | 16,544 |
| #832 history of what you sent, and $EDITOR | 73,728 | 51,088 |
| #833 measure image paste per harness | 0 | 0 |
| #835 reply to an agent on a host | 8,192 | 48 |

#769 adds a package that nothing links until #770. #833 adds only an e2e
test. No dependency was added. The new budgets leave 30,246 and 30,526 bytes
of room.

The budgets went to 37,755,000 (linux/amd64) and 35,515,000 (darwin/arm64)
for the theme and chrome styling stack, #811 to #837. Main after #839
measured 37,490,850 and 35,266,018 bytes (Go 1.26.9), 26,150 and 13,982 bytes
under the budgets. The stack brings it to 37,724,322 and 35,484,754, 233,472
and 218,736 bytes more. By pull request, linux/amd64 then darwin/arm64, with
the regenerated skill archive counted in the pull request it follows:

| pull request | linux/amd64 | darwin/arm64 |
|---|---|---|
| #811 show the prefix on the mode pill | 0 | 96 |
| #813 export and share a theme | 28,672 | 16,688 |
| #815 roles for border states, rule, status, dots and grounds | 24,576 | 16,736 |
| #816 match the host terminal's colours with theme none | 8,192 | 112 |
| #818 a light and dark theme pair | 4,096 | 16,592 |
| #820 colour the focused border by state | 0 | 0 |
| #821 a gradient on the focused border | 20,480 | 16,832 |
| #824 title style and place | 8,192 | 192 |
| #827 edit a theme in a side sheet | 77,824 | 100,384 |
| #829 the quick tab, contrast fix and role flash in the theme editor | 16,384 | 16,816 |
| #831 a style for the rail's current row | 8,192 | 144 |
| #834 a style for the rail's section headers, and section icons | 12,288 | 16,800 |
| #836 roomy spacing between the rail's sections | 4,096 | 80 |
| #837 bell, activity and attention marks on workspace tabs | 20,480 | 17,264 |

The theme editor in #827 is most of the growth. No dependency was added. The
new budgets leave 30,678 and 30,246 bytes of room.

The budgets went to 37,865,000 (linux/amd64) and 35,617,000 (darwin/arm64)
for tuios look, #859 and #860. Main after #858 measured 37,748,898 and
35,502,274 bytes (Go 1.26.9), 6,102 and 12,726 bytes under the budgets. The
two bring it to 37,834,914 and 35,586,226, 86,016 and 83,952 bytes more. By
pull request, linux/amd64 then darwin/arm64:

| pull request | linux/amd64 | darwin/arm64 |
|---|---|---|
| #859 apply a shared look with tuios look | 86,016 | 83,952 |
| #860 load a look's theme in the browser with themeJSON | 0 | 0 |

#860 changes only the browser build. No dependency was added. The new
budgets leave 30,086 and 30,774 bytes of room.

The budgets went to 37,956,000 (linux/amd64) and 35,686,000 (darwin/arm64)
when screenshots learned to draw a pane's kitty and sixel images (#678). Main
measured 37,834,914 and 35,586,226 bytes (Go 1.26.9), 30,086 and 30,774 bytes
under the budgets. The feature brings it to 37,925,026 and 35,655,298, 90,112
and 69,072 bytes more: the image layer in tuios.dev/shot with its scaler and
its SVG and HTML output, the payloads the kitty passthrough keeps, and the
code that finds a pane's images and decodes them under a pixel bound. It
uses no package tuios did not link already. The SVG and HTML output copies
the part of an image it shows instead of asking for SubImage through an
interface, which would link that method of every image type. The new budgets
leave 30,974 and 30,702 bytes of room.

The budgets went to 37,988,000 (linux/amd64) and 35,719,000 (darwin/arm64)
when a look learned the layout keys, the dock lists, the dock tabs and the
rail sections. Main measured 37,925,026 and 35,655,298 bytes (Go 1.26.9).
The change adds 32,768 and 33,440 bytes: the checks of the keys the registry
does not carry, and the item checks of the dock lists and the rail sections.
No dependency was added. The new budgets leave 30,206 and 30,262 bytes of
room.

Writing the config file header from two constants cut 32,768 bytes from
linux/amd64 and 33,024 from darwin/arm64. configFileHeader made 104
`strings.Builder.WriteString` calls, and each inlined call compiled to about
200 bytes of code. The output is the same bytes. Main at f49021ed measured
37,965,986 and 35,705,442 bytes (Go 1.26.9), and the change measured
37,933,218 and 35,672,418.

The budgets went down to 37,955,000 (linux/amd64) and 35,686,000
(darwin/arm64) after the cut. Each keeps about the room it had before it,
21,782 and 13,582 bytes.

Packing the bundled harness manifests cut 65,536 bytes from
linux/amd64 and 66,368 from darwin/arm64. internal/harness/manifests/*.toml (99,108 bytes)
go into the binary as one gzipped tar of 23,739 bytes. Load unpacks it each
time it runs and keeps nothing, and TestBundledArchiveMatchesFiles holds the
archive to the files. Main at 586469de measured 37,933,218 and 35,672,418
bytes (Go 1.26.9), and the change measured 37,867,682 and 35,606,050.
A benchmark of `Load()` with no user folder went from 1.21 ms to 1.61 ms.
Load runs once per daemon start, and allocates 60 KB more on the way.

The budgets went down to 37,890,000 (linux/amd64) and 35,620,000
(darwin/arm64) after the cut. Each keeps the room it had before it,
22,318 and 13,950 bytes.

Keeping cold helpers out of line cut 237,568 bytes from
linux/amd64 and 214,512 from darwin/arm64. The compiler inlined small constructors and
wrappers at hundreds of call sites each: invalidParam 369 times,
ShowNotification 513, VerbClient.Close and Call about 120 each, herdrcli's
usage 274. `//go:noinline` on 21 of them, each on an error, notification,
log, CLI or context menu path, leaves one copy. Main at 96a0b240 measured 37,867,682 and 35,606,050
bytes (Go 1.26.9), and the change measured 37,630,114 and 35,391,538. Before a new `//go:noinline`, check that the
function is off every render, keystroke and broadcast path.

The budgets went down to 37,652,000 (linux/amd64) and 35,405,000
(darwin/arm64) after the cut. Each keeps the room it had before it,
21,886 and 13,462 bytes.

The budgets went to 37,664,000 (linux/amd64) and 35,426,000 (darwin/arm64)
for the shared-harness session guard (#852). It keeps another Codex
conversation on the same app-server off a pane: a manifest flag, the guard's
recent owners per pane, and its checks in set-agent-state and
report-agent-activity. Main at d6e6ffc8 measured 37,642,402 and 35,391,858
bytes (Go 1.26.9). The change adds 16,384 and 16,848 bytes. On linux/amd64 it
measured 8,192 at e77a6641; the rest is alignment against #879. No dependency
was added. The new budgets leave 5,214 and 17,294 bytes of room.

The agents cockpit, wave 1 of ~/dev/tuios-notes/cockpit/DESIGN.md, raises the
budgets once per pull request. Each pull request measures main or the branch
under it, and sets the budget about 4 KB over its own size. By pull request,
linux/amd64 then darwin/arm64:

| pull request | linux/amd64 | darwin/arm64 | budgets |
|---|---|---|---|
| the agents cockpit: grouped fleet and rich rows | 53,248 | 50,992 | 37,721,000 and 35,464,000 |
| thread cards from transcripts | 77,824 | 67,840 | 37,798,000 and 35,532,000 |
| the reply area and the image view | 32,768 | 33,248 | 37,831,000 and 35,565,000 |
| the side column: live pane, last turn, checkpoints | 40,960 | 33,968 | 37,872,000 and 35,599,000 |

The fleet pull request measured 37,662,882 and 35,408,882 bytes before it and
37,716,130 and 35,459,874 after (Go 1.26.9). It deletes the Inbox panel's list,
the agents view and the thread panel it replaces. linux/amd64 grows by whole 4 KB pages, so darwin/arm64 is the measure of a change. The thread cards measured
37,793,954 and 35,527,714 bytes. The card append is kept out of line with
`//go:noinline`: inlined at each card it cost 8 KB more on both targets, and
the thread is drawn once per change, never per frame. The reply area and
the image view measured 37,826,722 and 35,560,962; they delete the reply
editor's old frame, which the area replaces. The side column measured
37,867,682 and 35,594,930. Its live pane is read again only after the pane
prints, at most every third pass as the picture-in-picture view is, and
only while the box is on screen; nothing in it runs on a timer. Parking the
hidden host cursor at the caret for input methods (#649) measured 37,879,970
and 35,595,330: 400 bytes on darwin/arm64, and three more pages on
linux/amd64, so only the linux/amd64 budget moved. It scans each frame for
the cursor visibility sequences, copies a parked frame only when it takes
one out, and runs no timer.

To raise a budget, do it on purpose in its own commit: run
`scripts/binary-size.sh` on the Go version in go.mod, set the new budget a
little above the printed size in the `budget` function of that script, and say
in the commit message what grew and why it is worth the bytes. A Go version
bump that grows the runtime is a reason; a dependency added for one helper
function is usually not, and is better replaced.

## 2026-09 chrome colour depth and OKLab blending

Measured on an Apple M3 Pro shared with other work, so every comparison is an
interleaved A/B: the test binaries of origin/main and of this change run
alternately, six rounds, and `benchstat` compares the two files.

The chrome palette (`theme.UI()`) is now built per colour depth and derives a
larger token set, with every ink measured on every ground it is promised on.
That made building it several times dearer, so it is memoised on what it is
built from (the depth, the theme's colours, the theme's chrome):

| Benchmark | Before | After | Allocations |
|---|---|---|---|
| `BenchmarkUIPalette` | 390 ns | 30 ns | 0, unchanged |

The frame benchmarks (`BenchmarkKeystrokeFrame`, `BenchmarkCompositorGetCanvas`,
`BenchmarkBackgrounds`, `BenchmarkSpotlightFrame`, `BenchmarkSpotlightApply`,
`BenchmarkSidebarPanelLinesCached`) show no significant change in time, bytes
or allocations (geomean -2.9%, every row p > 0.05 or faster).

`blendColors`, the dim's and the spotlight's blend, moved from gamma-encoded
sRGB to OKLab (`overlay.MixColors`), which costs cube roots per call. The
spotlight already blends through a cache. The unfocused-pane dim did not, so a
render now carries a 64-entry direct-mapped memo on its stack, which also hands
back colours already boxed. `BenchmarkDimUnfocusedRuns` (new; 480 style runs in
six colours, one memo per pane render):

| Case | Time | Allocations |
|---|---|---|
| before: sRGB, no memo | 17.2 µs | 960 |
| after: OKLab, memo | 13.6 µs | 7 |
| OKLab without the memo | 75 µs | 960 |

The last row is why the memo is there: without it the perceptual blend would
cost four times the old one.

## 2026-09 review diff per colour depth

Measured the same way as the section above: the test binaries of wave1/colors
and of this change run alternately, six rounds, on the shared M3 Pro.

The diff theme is built once per ground and depth, as before. At 256 colours
its grounds are fixed palette entries and at 16 there are none, so a frame
writes shorter sequences. `BenchmarkReviewFrameDepth` (new; the review of
`BenchmarkReviewFrame` at each depth, first frame and cached frame):

| Case | Bytes before | Bytes after | Allocations |
|---|---|---|---|
| truecolor unified, cached | 823 KiB | 823 KiB | 7,556, unchanged |
| truecolor split, cached | 1,021 KiB | 1,021 KiB | 8,948, unchanged |
| 256 unified, cached | 771 KiB | 665 KiB | -2.4% |
| 256 split, cached | 996 KiB | 843 KiB | -1.5% |
| 16 unified, cached | 690 KiB | 527 KiB | -4.5% |
| 16 split, cached | 911 KiB | 628 KiB | -4.4% |

Time showed no significant change in any row (p > 0.05; the machine was under
a load average of 10 to 40, so the spread was 30 to 170%).

`overlay.To256` places a chromatic colour by a linear search of the 240
non-slot palette entries in OKLab, about a microsecond. It runs when a palette
or a diff theme is built, which are memoised, and not per cell;
`BenchmarkDimUnfocusedRuns` does not change.

## 2026-09 modal dim, overlay fade and working-row shimmer

All three are passes over the composed canvas in `composeLayersIn`, so none of
them re-renders a pane or rebuilds the rail: the cached layers are copied in as
on any frame and the pass edits cells in place through the spotlight's
16-level blend cache (`cellShade`). Apple M3 Pro, a shared machine, medians of
five or six runs.

| Benchmark | Case | Time | Allocations |
|---|---|---|---|
| `BenchmarkModalScrimFrame` (new; nine panes, palette open, one pane dirty) | `modal_dim = 0` | 691 µs | 1406 |
| | `modal_dim = 30` | 926 µs | 1406 |
| `BenchmarkShimmerApply` (new; the pass over a twelve-agent rail, truecolor) | | 283 ns | 0 |
| `BenchmarkCompositorGetCanvas/windows-9/one-dirty` | before / after | 158 µs / 151 µs | 110, unchanged |
| `BenchmarkCompositorGetCanvas/windows-9/all-dirty` | before / after | 780 µs / 716 µs | 196, unchanged |
| `BenchmarkIdleTick` | after | 0 render/tick, 0 work/tick | 296 B, 5, unchanged |

The scrim costs about what the spotlight does, a quarter of a millisecond on a
full 207x55 frame, and only on frames drawn while a modal is open; a modal left
open over quiet panes draws none. The compositor rows are within noise.

The shimmer asks for at most 15 frames a second, and only while a working
agent's row is on screen. Each is a frame with nothing dirty plus the pass. A
hovered overflowing rail row (the marquee) costs more per frame, because the
rail is never served from its cache while it scrolls
(`BenchmarkSidebarAgentsRebuild`, about 345 µs).

The motion clock is its own timer, armed after an Update only while a fade runs
or a working row is drawn, and never armed otherwise. `TestIdleCostStaysLow`
and `TestFullMotionWithoutAgentsStaysIdle` (e2e) hold idle at zero bytes and
zero motion frames with the default `motion = full`.

## 2026-09 Wave 0 and Wave 1 merged

Measured on wave01/final against origin/main (c2a16426): the test binaries of
both run alternately, six rounds (ten for `BenchmarkIdleTick`), on the shared
M3 Pro.

- Allocations and bytes per op match origin/main on every render and idle
  benchmark (`KeystrokeFrame`, `KeystrokeFrameTiled`, `ClientFrame`,
  `CompositorGetCanvas`, `Backgrounds`, `SpotlightFrame`,
  `SidebarPanelLinesCached`, `SidebarAgentsCached`, `InboxRender`,
  `PointerSweep`, `RenderTerminalUnfocused`, `IdleTick`), except the cached
  review frame at +13 allocations (+0.17%) from the per-depth diff theme. The
  merge first showed three more allocations per keystroke frame: the colour
  tokens parsed their hex strings on every call. They are parsed once now.
- Time shows no significant change on the frame benchmarks (geomean +0.03%).
- `BenchmarkIdleTick`: 0 render/tick, 0 work/tick, 296 B and 5 allocations,
  unchanged. Its time went from 270 ns to 348 ns a tick: the motion clock
  checks which modal overlays are open after every message. The clock is read
  only when that set changes, and the loading-frame check reads it only while
  a load is out; before that change the tick was 386 ns.

## 2026-10 bulk history readers

Measured on a shared 4-core Xeon at 2.1 GHz with `GOMAXPROCS=2`, three runs
each, before and after in the same session. Times carry the noise of a shared
machine. Bytes are exact.

### What changed

`Scrollback.Line` decodes a stored line into a fresh `uv.Line` of 112-byte
cells and keeps the newest 256 in a cache for the renderer. Every reader of
the whole history went through it: copy-mode search on every key, the history
save, the snapshot sent on attach, the ANSI capture and the image sweep. A
full 10,000-line ring at 207 columns is 251 MB of cells, and the cache was left
holding about 5 MB of them per pane until the pane next printed.

`vt.Terminal` now has three readers that go round the cache. `ScrollbackRows`
decodes each line into one buffer it reuses. `ScrollbackText` hands out each
cell's content and width and builds no cell. `CopyScrollback` copies the lines
in their encoded form, a byte or so a cell, for a reader that decodes them
after it releases the emulator's lock. The ghostty backend reads each line
through the library as `ScrollbackLine` does and does not cache it. The cache
is also bounded at 65,536 cells, about 7 MB, as well as 256 lines.

- Copy-mode search reads the history as text cells and takes a match's
  columns from the cells. It used to count runes as columns, so a cell of
  several runes before a match put the cursor right of it. A key that extends
  the query searches only the lines the last search matched, when the history
  has not changed and the last search was not cut at the match limit.
- The history save copies the encoded rows under `terminalMu` and decodes
  and packs them after.
- The packed snapshot packs the screen under `terminalMu`, copies the history
  and the main screen under an alternate screen, and packs those after.
- The cell snapshot, the ANSI capture, the tape capture, the command output
  read and the image sweep use the row or text reader.

### Numbers

| Benchmark | before | after |
|---|---|---|
| `ScrollbackLineString/w207` (the cached path, unchanged) | 168 ms, 251 MB | 165 ms, 251 MB |
| `ScrollbackRows/w207` (new) | | 64 ms, 5.7 MB |
| `ScrollbackText/w207` (new) | | 1.06 ms, 4.4 KB |
| `CopyModeSearchKey` (one key, 207x55, full ring) | 150 ms, 271 MB | 11 ms, 0.40 MB |
| `CopyModeSearchTyping` (16 keys) | 1.30 s, 2.37 GB | 36 ms, 8.5 MB |
| `HistoryCaptureLocked/buildlog/1000` | 11.8 ms, 25.8 MB | 0.79 ms, 1.36 MB |
| `HistoryCaptureLocked/buildlog/5000` | 67 ms, 124 MB | 1.05 ms, 1.89 MB |
| `HistorySave/buildlog` | 21.0 ms, 28.0 MB | 11.5 ms, 3.57 MB |
| `HistorySave20BusyPanes` | 515 ms, 562 MB | 231 ms, 72 MB |
| `HistorySave20BusyPanes` max-wait | 61 to 72 ms | 4 to 12 ms |
| `HistorySave20BusyPanes` heap-after (new metric) | 159 MB | 51 MB |
| `WireTerminalState/scrollback-1000` (cell form) | 68 ms, 68.8 MB | 53 ms, 44.2 MB |
| `WireTerminalStatePacked/scrollback-1000` (new) | 26 ms, 27.6 MB | 14 ms, 3.2 MB |
| `WireTerminalStatePacked/scrollback-1000` locked-ms | 26 ms | 0.9 ms |

The before row of `WireTerminalStatePacked` timed `GetTerminalStatePacked`,
which held the lock for the whole read. At depth 0 the packed snapshot
allocates 25 KB more than before: the one history row is decoded where the
benchmark's repeated reads used to find it in the cache.

`TestCopyModeSearchKeyBudget` holds one search key to 4 MB, and
`TestHistoryCaptureBudget` holds the locked part of a history save to 4 MB.
Both fail on the tree before this change, at 258 MB and 24.7 MB.

## 2026-10 emulator allocations, scroll window, idle ticker, memory trim

Measured on a shared 4-core Xeon at 2.1 GHz with `GOMAXPROCS=2`, three runs
each, before and after in the same session. Emulator numbers come from a
207x55 pane with 10,000 lines of history, fed in 16 KiB writes. Real-binary
numbers come from `tuios attach` in a 207x57 tmux pane, read from `/proc`
over 20 s after 10 s of settling.

### What changed

- A truecolor cell that misses the 256-slot colour cache boxes its colour
  into a 64-colour slab, not a heap value of its own. The value has the same
  dynamic type, so `==` and type switches behave as before.
- A non-ASCII cluster comes from a 1,024-slot table of recent clusters. A run
  of new clusters copies the rest of the run once after four misses.
- A kitty graphics command is parsed from the one copy of the sequence the
  passthrough gets. `RawPayload` shares those bytes. A passthrough must not
  write to `rawData`.
- A scroll of the whole screen moves a window over a backing array twice the
  screen height. It does not slide every row header, extent and wrap flag.
- The client drops the Bubble Tea frame ticker to `IdleFPS` after 500 ms
  with no composed frame. Input, a raw write or a composed frame wakes it
  with a 1 ms tick for 4 ms, then the normal rate.
- The session-list poll tick no longer composes a frame. It changes nothing
  on screen. The refresh it starts composes one only when the listing it
  fetched changed, since the rail draws other sessions from that listing.
- `internal/memtrim` calls `debug.FreeOSMemory` 2 s after a burst of pane
  closes, after the client goes idle, and from the daemon's 30 s sweep. It
  trims only when the process is quiet and holds 32 MB or more of heap past
  its live data, and at most once in 30 s.

### Numbers

| Benchmark | before | after |
|---|---|---|
| truecolor per character, 7.6 MB | 96 ms, 417,520 allocs | 92 ms, 18,849 allocs |
| CJK and emoji, 2.5 MB | 153 ms, 190,137 allocs | 150 ms, 55,113 allocs |
| `WriteKittyAPC` (1.97 MB frame) | 3.79 ms, 5.90 MB, 2,405 allocs | 3.08 ms, 3.93 MB, 1,924 allocs |
| `yes`, 1 MB | 61.5 ms | 55.1 ms |
| `seq 1 100000` | 23.8 ms | 21.5 ms |
| `EmulatorShortLineScroll/with-scrollback` | 345 ns | 325 ns |

CJK throughput did not move. Its time is grapheme segmentation, not
allocation.

| Real binary | before | after |
|---|---|---|
| Client idle CPU, 1 pane | 0.95% | 0.40% |
| Client idle CPU, 10 panes | 1.23% | 0.47% |
| Client voluntary context switches a second | 355 to 364 | 112 to 118 |
| Daemon RSS 5 s after 49 of 50 panes close | 57 to 77 MB | 41 to 49 MB |
| Client RSS 5 s after 49 of 50 panes close | 58 to 60 MB | 59 to 60 MB |

The ticker change alone gave 0.56% and 0.59%. The poll tick composed a frame
every 3 s, and each frame woke the ticker for 500 ms. The client's own
scavenger already returned its free pages in these runs, so the client trim
did not run.

`TestIdleCostStaysLow` holds 0 idle wire bytes. `TestPerfInputLatency` is
16.7 ms at p50 and 18.4 ms at p95 for 1, 4 and 8 panes, before and after.
The `TestLatency*` tests in `internal/terminal`, `internal/input` and
`internal/app` are within noise.

`TestTruecolorGradientAllocatesPerSlab`, `TestRepeatedClustersAllocateNothing`,
`TestNovelClusterRunAllocatesBoundedly` and `TestKittyFrameCopiesPayloadOnce`
hold the budgets. Each fails on the tree before its change. The kitty one
measures 2.99 times the frame size there.

## 2026-10 frame clock

A guest that animates at the frame rate showed 83 to 88% of its frames at
max_fps 120, with gaps of 17 ms, and max_fps 240 drew at most 125 frames a
second of an animating pane. The client ran two clocks. Each pane's coalescer
signalled on its own, with a fixed 8 ms floor, and every signal composed a
frame. Bubble Tea wrote frames only on its ticker. A frame composed just
after a tick waited almost a whole period, and two guest frames could land
between two ticks while the next period had none. Nine animating panes asked
for about 1000 composes a second, and the ticker wrote 120 of them.

### The harness

`e2e/tui/perf_frames_test.go` runs a guest (`e2e/tui/framepace`) that draws
at a fixed rate and logs when each frame was ready. The host side of the
client's PTY is read without rendering, and every frame end (the end of the
client's synchronized update) is timestamped as it arrives. Each guest frame
carries its sequence number, in a tag at the top left for text and in the
first eight pixel bytes for kitty images, so a host frame is matched to the
guest frame it shows. It reports frame intervals and guest-to-host latency as
p50, p95 and p99, the share of guest frames shown, and client and daemon CPU.
`TUIOS_PERF_PPROF` takes CPU profiles of both over the window, and
`TUIOS_PERF_OUT` keeps every sample as JSON.

```
cd e2e/tui && TUIOS_E2E=1 TUIOS_PERF=1 go test -count=1 -v -run TestPerfFrames .
```

The host is a truecolor terminal (`COLORTERM=truecolor`), as kitty and
ghostty are. Without it the renderer converts every colour to the 256-colour
palette, which was a third of the client's CPU in the first profile.

### What changed

- `Window.SetFrameInterval`: the coalescer floor is one frame at max_fps,
  not 8 ms. It is per pane, and each client sets it on its own panes: in the
  SSH and web servers each connection is its own client, and one client's
  max_fps does not set another's.
- The coalescer and the new frame gate count intervals with the generic cell
  rate algorithm (`terminal.NextFrameTime`, `terminal.FrameSlack`). A frame up
  to a quarter of a period early stands for the end of the period. Counted
  from the moment of the emit, every late Go timer stretched the next interval,
  and the intervals of a 120 Hz guest came out 9 ms long.
- `takePaneOutput`: frames for pane output are spaced one period apart for
  the whole client, and a held frame comes as a `frameDueMsg`. Output that
  answers a key, a paste, a click or a wheel step is not held.
- `kickFlush`, `flushCmd`: when the frame or the cursor changed, one value is
  sent on Bubble Tea's ticker channel, so a composed frame is written at once.
  It is sent when `flushMsg` reaches `Update`, after Bubble Tea has stored the
  frame. A first version sent it on a 100 us timer from `View`, which could
  fire before the store: the renderer then wrote the frame before, and a typed
  key waited a whole tick (p99 17 to 24 ms). `TestKickFlushWritesTheFrame`
  fails on a Bubble Tea release where the channel no longer works.
- Every visible pane with output is drawn in each frame. Unfocused panes were
  drawn on every third pass, which made them uneven once passes were bounded.
- A pane far behind its output is drawn every two frames, not every 250 ms,
  and a frame that costs more keeps its own interval.

### Numbers

207x55, one guest unless noted, three interleaved runs of each build at a
load average of 2.6 to 3.1. Base is origin/main at 582af540.

| Case | base | after |
|---|---|---|
| 120 Hz guest, max_fps 120: frames shown a second | 110, 101, 104 | 120, 120, 120 |
| interval p95 / p99 | 16.8 to 17.1 / 17.4 to 17.5 ms | 9.2 to 9.4 / 9.7 to 9.9 ms |
| latency p50 / p99 | 9.9 to 15.9 / 17.8 to 19.2 ms | 7.4 to 7.5 / 9.0 to 9.4 ms |
| 240 Hz guest, max_fps 240: frames shown | 123, 124, 124 | 239, 240, 240 |
| interval p95 / p99 | 9.3 to 10.0 / 12.1 to 12.7 ms | 5.3 / 5.9 ms |
| 240 Hz guest, max_fps 120: latency p50 | 8.9 to 9.9 ms | 3.5 ms |
| 9 panes at 120 Hz: mean of the nine guests | 108.7, 106.4, 109.3 | 108.4, 109.3, 111.0 |
| slowest guest | 99, 95, 101 | 87, 90, 90 |
| client CPU | 914 to 970 ms/s | 634 to 658 ms/s |
| flood (`framepace scroll`): frames a second | 14, 16, 41 | 66, 68, 68 |
| interval p95 | 100.8, 101.9, 99.9 ms | 17.7, 17.7, 17.6 ms |
| 285 MB flood (`yes \| head`): time, max gap | 5.3 to 5.6 s, 101 ms | 5.1 to 5.2 s, 16 to 21 ms |
| shm frames, 120 and 240 Hz | all shown, p99 9.6 and 5.5 ms | all shown, unchanged |

In the nine-pane case the guests' mean rate is unchanged, but the spread is
wider: most guests show 115 to 120 frames, and one or two show 87 to 98. All
nine guests draw at exactly the frame rate, and with one frame clock for the
whole client, a guest whose frames arrive at the same moment as a frame is
composed has some of them land a frame late and some a frame early. Before,
each pane's signal composed a frame of its own, so every guest was drawn
soon after it wrote. A tighter gate (no early frames, a timer at the end of
the period) and a coalescer without cost pacing were both tried and neither
moved it outside run-to-run noise, so it is left as measured.

Typed keys at the default 60 frames a second, 60 ms apart
(`TestTypingLatencyStaysLow`), two runs each:

| | base | after |
|---|---|---|
| 1 pane: p50 / p99 | 6.2 / 7.4 ms | 4.7 to 4.8 / 5.8 to 6.0 ms |
| 8 panes: p50 / p99 | 6.2 / 22.8 ms | 5.4 to 5.6 / 6.9 to 7.6 ms |

Back to back (`TestPerfInputLatency`), where each key waits for the last
echo, the p50 moves from 16.6 to 16.9 ms to 11.0 to 16.2 ms and the p99 stays
at 17.5 to 18 ms.

`TestFramePacingKeepsTheGuestsRate`, `TestFloodStaysSmooth` and
`TestTypingLatencyStaysLow` assert the rates and the typing p99 under
`TUIOS_E2E_PERF`. `TestMaxFPS240DrawsPastTheOldClamp` measures 63,
243 and 241.

### What it costs

- A pane flooding past what the client can parse is drawn four times as
  often, so it is further behind its guest: latency p50 175 to 209 ms against
  75 to 113 ms. The guest wrote as many lines in the same time.
- At max_fps 240 a pane streaming kitty graphics composes 240 frames a second
  instead of 125. Each is the same frame and is not written, but the client
  takes 312 to 330 ms/s of CPU against 190 to 204.

### Invariants held

```
BenchmarkIdleTick-4   0 render/tick   0 work/tick   296 B/op   5 allocs/op
```

`TestIdleCostStaysLow`: 0 idle wire bytes, 104 ticks, 0 work, 0 renders.

## 2026-10 kitty streams without a compose

A guest that streams kitty images through shared memory, reusing one image id,
has each frame written to the host by the passthrough at once (#344). Each
frame still marked its pane as having new output, so the client composed the
whole screen for it and threw the result away. A frame edit (`a=f`), which the
tuios-wayland viewer sends for small damage, waited in the passthrough's queue
for that compose.

### What changed

- `terminal.graphicsOnly` recognises a write that holds only kitty commands
  leaving every cell alone (no placement without `C=1`, no virtual placement,
  no chunk of a command that began in an earlier write), with cursor moves
  between them, written while the parser is between sequences
  (`Emulator.AtGround`; libghostty-vt cannot say, so it never skips). With
  the guest's cursor hidden, such a write sets `HasGraphicsOutput` instead of
  `HasNewOutput`. The client composes for it only when the passthrough queued
  commands for the next frame (`KittyPassthrough.HasQueued`). A pane output
  signal that marked nothing no longer composes either, except while a drag
  or a resize is open, when it composes as before: those frames show the
  gesture between motion events.
- `forwardAnimation`: an edit of an image that is placed and shown, with no
  synchronized update open, is written at once (`editShowsAtOnce`).

### Numbers

`TestPerfFrames`, 207x55, a frame the size of the pane (2070x1040 pixels):

| Case | before | after |
|---|---|---|
| shm, 240 Hz, max_fps 240: client CPU | 206 ms/s (312 to 336 with the frame clock alone) | 42 to 44 ms/s |
| shm, 120 Hz, max_fps 120: client CPU | 170 to 190 ms/s | 22 ms/s |
| shm, 60 Hz: client CPU | 86 ms/s | 10 to 12 ms/s |
| 64x64 edit at 120 Hz: latency p50 / p95 / p99 | 1.9 / 4.8 / 6.8 ms | 0.6 / 0.7 to 0.8 / 0.8 to 1.0 ms |
| edit: client CPU | 164 ms/s | 14 ms/s |

Every frame and every edit reaches the host on both sides.
`TestKittyStreamCostsLittle` asserts the CPU and the edit latency under
`TUIOS_E2E_PERF`.

## 2026-10 the GUI bridge's CPU

`tuios gui-bridge` runs a model of the session that draws nothing, so all of
its work is overhead the desktop GUI pays for. Three costs were cut. The
palette key printed the whole user config with `fmt.Sprint` on every message
(fixed in the bridge PR). The model's own emulator encoded each line that
scrolled off into a history nothing reads. Each output chunk was copied twice
before it reached the pipe.

### Numbers

CPU ticks of the bridge process (100 a second), from /proc, GOMAXPROCS=4,
nice 19 on cores 0-7 of a machine in use. Idle is 10 s on two idle shells.
Flood is one pane running `seq -f 'flood line %g' 1 1500000` at 127x39, to
the last line at the renderer.

| Build | idle, ticks per 10 s | flood, ticks |
|---|---|---|
| the palette key printed from the config | 78 to 94 | 68 to 102 (review run) |
| the palette fix only (bridge PR) | 5 to 7 | 138 to 219 |
| with the history dropped and the chunk not copied | 4 to 6 | 117 to 120 |

The review run measured the flood on another machine, 68 to 102 ticks before
all three fixes and 37 to 51 after. The rows here are the same flood on this
machine, three runs each. `TestGUIBridgeIdleCostsLittle` holds the idle row
to 30 ticks.

## 2026-10 saving the cursor on the libghostty backend

A reattach now carries the cursor DECSC saved. libghostty keeps it where no
query reaches, so the backend records the cursor at every save (DECSC, SCOSC,
1048, 1049) as the stream goes past: it hands the library the bytes before the
save and reads the cursor, the pen and origin mode. apt saves and restores the
cursor around its status line on every line it prints, so it pays this once a
line. `BenchmarkBackendApt` is that stream: a scroll region above the last row,
2000 lines, each followed by a save, a status line redraw and a restore, at
120x40.

### Numbers

`go test -tags ghostty ./vt/ -bench 'BackendApt$' -benchtime 3s
-count 2`, run twice, GOMAXPROCS=4, nice 19 on cores 0-7 of a machine in use:

| Build | ms per 2000 lines |
|---|---|
| main | 2.7 to 3.2 |
| recording the saved cursor, the screen read from the library | 5.3 to 6.9 |
| recording the saved cursor, the screen read from the cache | 4.3 to 4.9 |

The library read was three `Mode` cgo calls per save and per restore, to ask
which screen was active. The scanner already flips `cachedAltScreen` at the
switch, mid-write, so `liveScreenLocked` reads that instead. What is left is
the flush before the save and the cursor reads, about 0.8 µs a save.
`BenchmarkBackendScroll`, which saves nothing, measured 44 to 64 ms on main
and 47 to 50 ms here in the same runs, which is within the noise of this
machine.

## 2026-10 the message burn tick

An idle client with a message on the dock wrote one `─` cell every 100 to
200 ms. That write is the burn: the rule under the message loses one lit cell
at a time, so each write is a real change. The cost was in the wakeups around
it. While a message was up, the maintenance tick ran at the frame rate (60, or
up to 240 on `max_fps = "auto"`), and every frame the burn drew woke Bubble
Tea's frame ticker to the frame rate for 500 ms. A rule 30 cells wide on a 6 s
message moves 5 times a second, so the client woke 60 or more times a second to
draw 5 frames.

### What changed

- `notifTickWait`: when a message is the only thing that moves, the tick
  sleeps until the rule next loses a cell or a message runs out
  (`notifNextMove`). It never sleeps less than one frame or more than the idle
  tick. A message the last frame did not draw keeps the frame rate.
- A frame the tick composes for the message block alone flushes through
  `kickFlush` and leaves the frame ticker at `IdleFPS` (`quietFrame`).

### Numbers

Throwaway daemon, client in a detached tmux at 120x36, truecolor, one idle
shell, `/proc/<pid>/stat` and the threads' context switches. The wire bytes
are the same before and after.

| Window | | client CPU | client ctxsw/s |
|--------|---|-----------|----------------|
| 60 s message, 30 s sample (1 write per 1.7 s) | before | 0.77% | 478 |
| | after | 0.27% | 129 |
| default 6 s message, first 5 s (3 runs) | before | 1.4 to 1.6% | 574 to 606 |
| | after | 0.8 to 1.2% | 244 to 262 |
| no message, 60 s (0 bytes) | before | 0.13 to 0.15% | 118 to 122 |
| | after | 0.12 to 0.15% | 119 to 121 |

`TestNotificationTickSleepsUntilTheBurnMoves` holds the budget: a 6 s message
on a 30-cell rule takes 60 ticks for 30 frames, and every cell is drawn. With
the frame-rate wait back it takes 360 ticks and fails.

### Measured and deliberately not changed

With no message, an idle client still woke about 20 times a second: the
maintenance tick and Bubble Tea's frame ticker both ran at `IdleFPS`. Neither
wrote a byte. The frame ticker now stops at idle and the maintenance tick runs
only when a job is due. See "the frame ticker stops at idle" and "the
maintenance tick runs only when a job is due" below.

## 2026-10 the daemon at idle

A daemon whose panes sit at a prompt did 7 to 9 context switches a second
with no client, and 22 to 27 with one client attached. Every one came from a
timer. Each background loop polled on a ticker of its own, whatever the panes
were doing:

| Loop | Interval | Wakeups a second |
|---|---|---|
| Resurrection saver, per session | 2 s | 0.5 per session |
| Agent monitor | 2 s | 0.5 |
| Stall monitor | 7.5 s at the default timeout | 0.13 |
| Cleanup loop | 30 s | 0.03 |
| Session listing, asked by a client with the rail open | 3 s | 0.33 per client |

The Go runtime turns each timer wakeup into 10 to 40 context switches: the
netpoller thread wakes, a goroutine runs, and sysmon spins in 20 µs sleeps
before it parks again.

Each loop now sleeps on a latch (`internal/session/wake_latch.go`). The side
that notices a change raises the latch, which costs one atomic load while it
is already up. Only the raise wakes the loop. A loop with work keeps its own
cadence. A loop without work stops its timer.

### Measurement conditions

A throwaway daemon with scratch XDG folders under
`nice -n 19 taskset -c 0-7 GOMAXPROCS=4`, one session with two `/bin/bash`
panes, the client in a detached tmux at 120x36 when attached. Context
switches are the sum of every thread's voluntary and involuntary switches
from `/proc`, over 30 s. Runs of different builds were interleaved, three
runs each. `runtime/trace` grouped the goroutine wakeups by the stack each
goroutine was blocked in.

### What changed

- The resurrection saver sleeps until a session changes. A structural
  change raises `stateDirty` and reaches disk within 2 s, as before. Output in
  a pane, or a use of the session, raises `saveStale` and reaches disk within
  30 s of the last save. That keeps a shell's directory and a pane's history
  current. A session that prints nothing writes nothing and arms no timer.
- The agent monitor sleeps while no pane does anything. A program can only
  start or stop in a pane that takes input, prints, or was just made, and
  each raises the detect latch. After `agentDetectQuietBound` (10 s) with the
  latch down, in which every quiet pane is read once more, the monitor stops
  its timer. It keeps looking while an agent it owns is out of the
  foreground, because the misses that clear it take consecutive looks.
- The stall monitor sleeps until the earliest working pane could stall. With
  no pane working it arms no timer. A change to a session's state raises the
  stall latch, and the monitor works out its deadline again. A pane the
  screen tier keeps working is looked at again every interval, as before.
- The cleanup loop sleeps until something is left to clean. What it releases
  is left by output, input and resizes, and what it expires is a routed
  request. Each raises the cleanup latch. It runs at most twice after the
  last activity.

### Numbers

Client attached, 5 s after the panes opened, 30 s window:

| Build | daemon ctxsw/s |
|---|---|
| main | 25.3, 26.4, 26.4 |
| saver sleeps | 20.7, 19.3, 21.5 |
| agent monitor sleeps | 15.3, 14.5, 13.3 |
| stall monitor sleeps | 10.7, 17.5, 12.4 |
| cleanup loop sleeps | 14.4, 13.3, 12.9 |

The cleanup loop woke once in 30 s, so its row is inside the noise.

40 s after the panes opened, past the quiet bound and the first stale save,
30 s window:

| Build | no client | client attached |
|---|---|---|
| main | 8.1, 9.0, 7.6 | 26.9, 24.8, 21.8 |
| all four loops sleep | 1.0, 2.0, 1.0 | 11.0, 7.7, 15.5 |

With a client attached, what is left is the client's listing poll: one
request every 3 s while the rail is open. The trace shows no other timer.

### The session listing push

The client drew other sessions on its rail from a listing it asked for every
3 s with the rail open, and every 30 s with it hidden. Now the daemon tells a
client when the listing changed (`MsgSessionsChanged`, offered as
`ListingPush` in the welcome and asked for in the hello), and the client asks
then. Everything the listing shows changes through an event the daemon sees:
output, input, a state change, a title, a pane's folder or branch, a client
arriving or leaving, a session made or ended, a request or a verb. Each
raises the listing latch. The notifier builds the listing, hashes it, and
pushes only when the hash moved. It looks at most once every 3 s, the
cadence of the old poll, and arms no timer while nothing happens. A daemon
that does not offer the push is polled as before.

40 s after the panes opened, client attached, 30 s window, three interleaved
runs:

| Build | daemon ctxsw/s | client ctxsw/s |
|---|---|---|
| four loops sleep | 16.0, 7.4, 10.0 | 131.2, 126.2, 130.8 |
| and the listing push | 1.4, 1.0, 1.8 | 127.0, 110.3, 109.2 |

The client's own count is its maintenance tick and Bubble Tea's frame
ticker, 10 a second each. They are the subject of the client idle plan.

`TestListingPushReplacesThePoll` attaches a client that reads the push,
checks that an idle daemon looks at the listing 0 times and pushes nothing,
then renames a window in another session and checks the push arrives and the
listing shows the name. With the latch unable to wake the notifier, the
push never arrives and the test fails. `TestDaemonIdleArmsNoTimers` counts the
notifier too.

These runs start 5 s after the panes open, so the agent monitor is still in
its 10 s quiet bound for the first seconds of the window.

`TestDaemonIdleArmsNoTimers` is the daemon's idle budget. It starts a real
daemon with one shell pane, waits for its loops to settle, and counts each
loop's timer fires over an idle window: the budget is 0. Then it types into
the pane and checks that the agent monitor wakes. It also sets the pane
working and checks that the stall monitor demotes it with one or two fires,
and that input wakes the cleanup loop. With the monitor's sleep
removed the loops never settle and the test fails.

`TestAnIdleSessionDoesNotWakeTheSaver` holds the saver's budget: no write and at most
the first look over four intervals of an idle session, then a structural
change still reaches disk. With the 2 s poll back the saver wakes 4 times
and the test fails.

## 2026-10 the daemon's subscriber queue

The daemon made each stream's output queue whole on subscribe: a channel of
4096 chunks, 56 bytes a slot, about 229 KB for each pane and each attached
client before the pane printed anything. At 50 idle panes and one client
that was 12 MB of the daemon's heap. The earlier ledger entry on
`outputChan` kept it because a smaller channel dropped output. Since then a
full queue gaps the stream and rebuilds it from the ring, so the slot count is
a limit and not a reservation.

### What changed

- `ptyQueue` (`internal/session/subscriber_queue.go`) replaces the channel. It
  is a ring behind a mutex that starts empty, doubles from 16 slots with the
  backlog, and stops at the same 4096. A stream that would take one more is
  gapped and rebuilt from the ring, as before. The order, the byte bound
  (`maxSubscriberQueue`), the catch-up, `route` and `enqueue` are the same.
- The stream writer takes chunks until the queue is empty and only then waits
  on a one-slot wake channel. A push that makes the queue non-empty leaves a
  token there.
- A queue that grew past 16 slots gives its array back after it has stayed
  empty for one second (`queueTrimAfter`). The attach stream trims on a timer
  it arms only for such a queue. `stream-pane` trims on its one-second tick.

### Numbers

Throwaway daemon with `--pprof`, 50 idle bash panes, one client in a detached
tmux at 120x36, 12 s to settle. Three runs each, main and branch interleaved.
Live heap is the `inuse_space` total of the heap profile.

| | main | branch |
|---|---|---|
| daemon HeapInuse | 43.1, 47.4, 50.0 MiB | 28.5, 29.4, 29.8 MiB |
| daemon RSS | 97.4, 97.8, 101.6 MiB | 79.2, 79.5, 83.4 MiB |
| live heap (profile) | 29.0, 29.9, 33.2 MiB | 15.0, 15.0, 15.2 MiB |
| `subscribeLocked` in the live heap | 4.9 to 13.0 MiB | below the sampling floor |
| saved per pane, from the medians | | HeapInuse 0.36 MiB, RSS 0.37 MiB |

HeapInuse and RSS fall by more than the 229 KB the channel held, because the
GC lets the heap grow to twice the live heap before it collects.

`go test -bench 'BenchmarkPTY(OutputChunk|Broadcast)$' -benchmem`, six runs of
each test binary interleaved, GOMAXPROCS=4, nice 19 on cores 0-7 of a machine
in use, benchstat:

| | main | branch | |
|---|---|---|---|
| OutputChunk, 0 subscribers | 860 ns | 864 ns | ~ (p=0.24) |
| OutputChunk, 1 subscriber | 908 ns | 908 ns | ~ (p=1.0) |
| OutputChunk, 4 subscribers | 947 ns | 940 ns | ~ (p=1.0) |
| Broadcast, 1 subscriber | 402 ns | 383 ns | -4.8% (p=0.002) |
| Broadcast, 4 subscribers | 594 ns | 537 ns | ~ (p=0.065) |
| Broadcast, 16 subscribers | 1.42 µs | 1.20 µs | -15.3% (p=0.002) |

Allocations stay at one per op. Broadcast to 16 subscribers shows 81.5 B/op
against 80: the first fill grows each queue to 1024 slots once. The
benchmarks drain each queue with `pop` until it is empty. A drain that asked
`len` before each `pop` took the lock twice a chunk and measured the harness,
not the producer.

`TestIdleSubscriptionMemoryBudget` holds an idle subscription to a sixteenth
of the old channel. It measures 1804 bytes here and 230109 bytes on main,
where it fails. `TestFloodedQueueGivesBackItsPeak` fills a queue to the limit
through wrapped doublings and checks the order, the refused chunk, and the
trim.

### Measured and deliberately not changed

- **Letting the array go at every drain.** That was the first version. A
  stream that drained between bursts grew its queue from 16 slots again at
  every burst. Broadcast was 4.7% to 27% slower and took 1984 B/op with 16
  subscribers. Those runs also used the two-lock drain, so part of the cost
  was the harness. The one-second trim keeps the steady flood at the
  old cost.

## 2026-10 allocations on the output path

The kernel hands the daemon's read loop one line at a time when lines are
short: `yes` arrives in reads of about 160 bytes, `seq` in about 1 KiB. Only
`cat` of long lines fills the 16 KiB buffer. Three steps paid for every read
allocated: the nesting probe scan joined the last read's tail to the new one,
the graphics scan grew a segment slice, and the screen tracker grew a slice
byte by byte for every escape sequence, so coloured output allocated once per
colour. On a `yes` flood that was most of the daemon's garbage. The client
read each output frame through a 4 KiB buffer that doubled up to the frame
size.

### What changed

- The probe scan keeps the last read's tail only from its last ESC. The
  prefix and nonce a probe is recognised by hold one ESC, at the start, so a
  probe cut by a read starts there. The join is copied into an array on the
  PTY.
- The graphics scan hands its segments out in one slice that it reuses. No
  caller keeps them past `broadcast`.
- The screen tracker gathers a sequence in an array on the stack.
- `readFramePayload` starts at twice the bytes a buffered reader already
  holds. A sender that announces more than it sends still costs at most twice
  what it sent.

### Numbers

`BenchmarkPTYOutputScan` and `BenchmarkReadFramePayload`, six interleaved
runs, benchstat:

| Benchmark | before | after |
|---|---|---|
| scan, 128 B chunk | 159 ns, 3 allocs | 51 ns, 0 allocs |
| scan, 1 KiB chunk | 186 ns, 3 allocs | 78 ns, 0 allocs |
| scan, 16 KiB chunk | 640 ns, 3 allocs | 561 ns, 0 allocs |
| scan, 1 KiB coloured | 2.72 µs, 77 allocs | 1.47 µs, 0 allocs |
| read frame, 16 KiB | 5.3 µs, 28 KiB, 3 allocs | 2.7 µs, 16 KiB, 1 alloc |
| read frame, 64 KiB | 21.3 µs, 124 KiB, 5 allocs | 12.0 µs, 64 KiB, 1 alloc |
| `PTYOutputChunk`, 4 subscribers | 1,043 ns, 1 alloc | 935 ns, 0 allocs |

Real binary, one pane, client attached, three interleaved runs. Allocation
and GC counts are from `/debug/pprof/heap` before and after the flood:

| Flood | daemon alloc | daemon GCs | client alloc | client GCs |
|---|---|---|---|---|
| `yes`, 100 MB, before | 754 to 950 MB | 140 to 173 | 353 to 362 MB | 79 to 82 |
| after | 175 to 180 MB | 35 to 37 | 193 to 194 MB | 43 to 46 |
| `seq 1e7`, before | 161 to 198 MB | 32 to 37 | 207 to 216 MB | 40 |
| after | 94 to 96 MB | 19 to 20 | 105 to 106 MB | 20 to 22 |
| `cat` 200 MB, before | 233 to 236 MB | 32 | 523 to 528 MB | 76 to 80 |
| after | 217 to 229 MB | 30 to 32 | 263 to 273 MB | 38 to 48 |

CPU per MB and the time to finish moved inside the noise of a shared machine.

`TestOutputScanAllocatesNothing` holds the budget: a chunk with colours and a
cut OSC allocates nothing. On the tree before it allocates 5 times.
`TestNestProbeSplitAcrossReads` now cuts a probe at every byte.
`TestFrameBodyGrowsWithWhatArrives` checks the bound through a buffered
reader.

## 2026-10 quiet frames for dock components

With `show_clock = true` the dock draws one frame a second. Each of those
frames woke Bubble Tea's frame ticker from `IdleFPS` to the frame rate for
`idleTickerAfter` (500 ms), so one frame of the clock cost about 30 frame
ticks that wrote nothing.

### What changed

- A frame composed for a `dockComponentMsg` alone (the clock, a meter, a
  custom component) sets `quietFrame`, the rule #763 added for the message
  burn. It goes out through `kickFlush` and does not call `noteFrame`, so the
  frame ticker stays at `IdleFPS`.
- `TUIOS_STATS_FILE` now also counts `tickerWakes` and `tickerSleeps`, and
  SIGUSR1 writes the counters on Unix, so a test can read them at both ends
  of an idle window.

### Numbers

Throwaway daemon, client in a detached tmux at 120x36, one idle shell, an
empty folder, 50 s per run (`~/.cache/agent-tmp/idlewake/idle.sh`). Wakeups
are counted over 40 s of each run by a scratch build that counts every
message and every fire of Bubble Tea's frame ticker. The wire bytes are the
same before and after.

| Setup | | client CPU | client ctxsw/s | frame ticker fires/s | other wakeups/s |
|---|---|---|---|---|---|
| clock on, 3 runs | before | 0.40 to 0.56% | 270 to 277 | 40.4 to 41.4 | 10 tick, 1 clock, 1 flush, 0.33 listing |
| | after | 0.38 to 0.50% | 142 to 145 | 11.0 | same |
| clock off, 2 runs and 1 run | before | 0.20 to 0.22% | 124 to 130 | 10.0 | 10 tick, 0.33 listing |
| | after | 0.20% | 135 | 10.0 | same |

The CPU is read in clock ticks of 10 ms, so a 50 s run resolves 0.02%. The
CPU change with the clock on is inside the run-to-run noise. The context
switches halve.

`TestIdleCostStaysLow` now has a clock-on window. It holds the frame ticker
wakes to 1 in 10 s, the maintenance ticks to 110, the full scans to 1 and the
client's voluntary context switches to 2500. With `quietFrame` not set for
the dock, the clock-on window woke the ticker 10 times and failed.

## 2026-10 the frame ticker stops at idle

Bubble Tea writes frames from a ticker that runs for the life of the program.
tuios dropped it to `IdleFPS` 500 ms after the last frame, so an idle client
still woke ten times a second for a ticker that found nothing to write.

### What changed

- `idleFrameTicker` stops the ticker. The `fps` field keeps the frame rate,
  so a renderer that restarts after a suspend or an exec runs at the frame
  rate, and `rendererRestarted` counts the client as awake again.
- `noteFrame` restarts a stopped ticker with `Reset`, as it woke a slow one.
- `kickFlush` sends one value on the stopped ticker's channel, and the
  renderer still receives it. `TestKickFlushWritesTheFrame` now checks that a
  stopped ticker writes nothing on its own and that one kick writes the frame.
- While the ticker is stopped, Update asks for a flush after every message
  but a maintenance tick that slept (`flushWhileIdle`). Bubble Tea writes a
  clipboard set, a colour query or a raw write only from the ticker, and a
  message that composes no frame would otherwise leave it queued.
- Since Go 1.23, `Ticker.Stop` empties the channel, which would drop a kick
  sent just before the stop. One tick is sent after the stop.

### Numbers

Same harness as the section above, 3 runs each, 50 s, counts over 40 s.

| Setup | | client CPU | client ctxsw/s | frame ticker fires/s |
|---|---|---|---|---|
| clock off | before | 0.20 to 0.22% | 124 to 135 | 10.0 |
| | after | 0.12 to 0.14% | 84 to 87 | 0.33 |
| clock on | before | 0.38 to 0.50% | 142 to 145 | 11.0 |
| | after | 0.28 to 0.32% | 94 to 101 | 1.32 |

The ticker fires that are left are kicks: one per listing poll (the flush
after a message), and one per clock frame. The maintenance tick still runs
at `IdleFPS`.

`TestIdleCostStaysLow`, standalone, voluntary context switches over 10 s:
816 to 895 with the clock off and 928 to 989 with it on. With the ticker
dropped to `IdleFPS` instead (the build before), 1049 to 1160 and 1298 to
1362.

First pane output after 20 s idle, 20 runs: a pane program sleeps, takes the
time and writes a marker in the same process, and the latency is the time
until the marker reaches the terminal (tmux `pipe-pane`). 1.4 to 7.4 ms,
median 1.6 ms. The planning build that stopped the ticker the same way
measured 1.4 to 1.5 ms over 10 runs of this probe.

The plan's first probe took the time in one Python process and wrote the
marker from the shell after Python exited. One of its 7 runs took 184 ms. That
run was the only one in which the daemon used real CPU: 0.26 s over 25 s,
against 0.00 to 0.01 s in every other run. The delay was outside the client's
wake path, and 30 runs here did not repeat it.

## 2026-10 the maintenance tick runs only when a job is due

The maintenance tick ran for the life of the client. At idle its gate
(`tickNeedsWork`) found nothing to do, ten times a second, and each of those
ticks woke four goroutines.

### What changed

- `nextWake` says when the next job is due, and Update arms one tick for that
  moment after every message (`tickWakeCmd`). With nothing due, no tick is
  armed. A tick armed later than a new due time is replaced, and the old one is
  dropped by its generation when it fires.
- The due times: the frame rate while something moves (as before), the next
  cell of a burning message and the end of a pointer hold on it, held agent
  and program alerts, the zen reveal and a gesture hold running out, the frame
  ticker's idle switch, the git section's 2 s re-read and the files section's
  retry.
- Work the idle gate finds by looking (an exited process, unflushed output, a
  drifted title, a moved listing, a stranded gesture) is looked at again every
  100 ms while it lasts, which is the old idle rate. Each of those is set on the
  Update goroutine or followed by a message, so asking after every message
  sees it.
- A sticky message keeps no tick running. It used to keep the idle tick.
- `IdleTickCmd`, `TickCmd` and `notifTickCmd` are gone.

### Numbers

Same harness, 3 runs each, 50 s, counts over 40 s.

| Setup | | client CPU | client ctxsw/s | timer wakeups/s |
|---|---|---|---|---|
| clock off | before | 0.12 to 0.14% | 84 to 87 | 10 tick, 0.33 listing |
| | after | 0.04 to 0.06% | 8.0 to 9.1 | 0.33 listing |
| clock on | before | 0.28 to 0.32% | 94 to 101 | 10 tick, 1 clock, 0.33 listing |
| | after | 0.16 to 0.18% | 17.7 to 19.2 | 1 clock, 0.33 listing |

From the start of this work (main before #781, one idle client): clock off,
0.20 to 0.22% CPU and 124 to 135 context switches a second down to 0.04 to
0.06% and 8 to 9. Clock on, 0.40 to 0.56% and 270 to 277 down to 0.16 to
0.18% and 18 to 19. The wire bytes are the same in every run. The listing
poll is the one timer left with the rail open. Each poll is one message, one
flush and one frame ticker kick.

`TestIdleCostStaysLow`, standalone, over 10 s: 0 ticks, and 59 voluntary
context switches with the clock off and 152 with it on. Before, 100 ticks, and
816 to 895 and 928 to 989.

First pane output after 20 s idle, same probe as the section above: 1.4 to
1.8 ms over 20 runs, against 1.4 to 1.6 ms over 10 runs on main.

A scratch build ran the old idle gate every 100 ms next to the deadline tick
and logged work the gate found that no armed tick covered, three looks in a
row. Over the whole e2e suite it found no
job the deadlines missed: each hit was a sticky error message, which needs no
tick, or pane output already on its way in its own `PTYDataMsg`.

`benchstat`, main against this branch, 6 interleaved runs. `BenchmarkIdleTick`
and its two variants: 5 allocations (296 B) to 1 (24 B) a tick, because the
idle tick arms no timer. Time 689 to 714 ns, within the noise (p=0.065). The
gate's pass over the windows now also runs after every message, which is the
cost of the time. `BenchmarkNotificationTick`: 38 to 30 allocations, the same
time. `BenchmarkClientFrame` and `BenchmarkKeystrokeFrame` at 1 to 20 panes:
no change.

### Equal frames and the buffer release

Two follow-ups. A frame equal to the last one no longer wakes the frame
ticker: it has nothing to write. And #762's release of the panes' spare
emulator buffers, which ran from every maintenance tick, now runs once after
a frame changed (`emulatorReleasePending`), at its 30 s interval, through a
deadline in `nextWake`. An idle client releases once and then sleeps.

With the rail's git section on, whose reading every 2 s composes a frame
equal to the last, same harness, 2 runs each:

| | client CPU | client ctxsw/s | frame ticker fires/s |
|---|---|---|---|
| before | 0.14 to 0.18% | 113 to 115 | 17.1 |
| after | 0.08 to 0.10% | 21.7 to 25.4 | 0.8 |

`TestIdleCostStaysLow/git_section` holds it: 260 to 300 context switches in
10 s, and 1186 with an equal frame waking the ticker.
## 2026-10 the clock draws minutes by default

`DefaultClockFormat` is `15:04`. The dock engine arms the clock's timer for the
next change of what it draws, so the default clock wakes the client once a
minute. A layout with seconds still draws them, and still costs one frame a
second.

Same harness, `show_clock = true` and no format, 130 s runs, counts over 120 s.

| Default layout | | client CPU | client ctxsw/s | clock frames | timer wakeups/s |
|---|---|---|---|---|---|
| `15:04:05` (1 run) | before | 0.16% | 18.3 | 120 | 1 clock, 0.33 listing |
| `15:04` (3 runs) | after | 0.02% | 6.7 to 7.5 | 2 | 0.02 clock, 0.33 listing |

### After a suspend

A Go timer counts time the machine is awake. The minute clock's timer is armed
for up to 59 s, and a suspend in that wait left the clock showing the time of
the suspend for the rest of it after the wake. The dock engine now sleeps at
most `dockWallCheck` (15 s) before it reads the wall clock again, and a focus
report from the terminal makes it read it at once. The extra wakes read the
time and sleep again: no message and no frame.

Same harness, default clock, 130 s runs, 2 runs each: 6.6 to 7.6 context
switches a second with the check against 6.1 to 6.7 without, 0.02 to 0.03% CPU
either way, and still 2 clock frames in 120 s.

## 2026-10 workspace tab marks over a daemon

A client attached to a daemon streams only the panes it shows. The dock's
`bell_marker` and `activity_marker` need the bell and output of panes it does
not show. The daemon sends `MsgPaneMark` for them, offered as `PaneMarks` in
the welcome and asked for in the hello (`internal/session/pane_marks.go`).

Each pane keeps one atomic bit per mark. The first output or bell after a
subscription change sets it and sends one message to each client that does
not stream the pane. Every later chunk costs one compare and one atomic load,
and sends nothing until a client subscribes to the pane or drops it. No
timer is armed.

`BenchmarkPaneMarkRaised` (new): the per-chunk cost once the bit is set,
2.1 ns, 0 allocs. A first run passed the event type as a constant, which the
compiler folded, and read 1.1 ns.

`BenchmarkE2EFlood`, 10 runs of 8 x 16 MiB each side, benchstat:
287.6 ms before, 294.2 ms after, p=0.123, no significant change. Allocations
6.52k and 6.48k, p=0.645.

`BenchmarkE2EBackgroundFlood` (new): a pane the client does not stream
prints 16 MiB per iteration. The client gets 1 mark, for the pane's prompt,
then 0 marks and 0 socket reads per flood. It fails on a second mark.
