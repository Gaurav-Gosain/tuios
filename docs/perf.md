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
