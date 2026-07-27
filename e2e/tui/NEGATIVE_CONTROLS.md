# Negative controls for the end-to-end suite

A regression test that has never been observed to fail on broken code is not
evidence. Every test in this package that claims to cover a specific bug was run
against a binary built with that bug's fix removed, and the result is recorded
below. Two of them are **not** caught here; that is written down rather than
papered over, because a suite whose real coverage is unknown is worse than no
suite.

## How to rebuild a control

The controls are built by removing one fix from the current tree, so they differ
from the shipping binary only in that fix. Checking out the pre-fix ancestor
instead would drag in unrelated differences and prove less.

```sh
git clone --shared . /tmp/negctl && cd /tmp/negctl
git checkout -f <base>          # the commit the suite was written against

# then either revert the fix hunk:
git show <fix-sha> -- <file> | git apply -R -
# or inject the fault by hand, when later commits have moved the code

go build -o /tmp/tuios-broken ./cmd/tuios
```

Run the suite against it from `e2e/tui`:

```sh
TUIOS_E2E=1 TUIOS_E2E_BIN=/tmp/tuios-broken go test -count=1 -timeout 550s .
```

`-count=1` is mandatory. Go's test cache will happily replay a previous PASS
across a change of `TUIOS_E2E_BIN`, which during the writing of this suite made
a working negative control look like a broken one for half an hour.

## Results

| Bug | Fix removed | How | Tests that fail | Verdict |
| --- | --- | --- | --- | --- |
| Freeze: render path took the window I/O read lock twice | `6ca26b1` | revert `internal/app/render_terminal.go` hunk | `TestSustainedOutputKeepsRendering` (hangs at round 1/6), `TestSoakMixedActivity` (hangs at cycle 2/8) | **caught** |
| Blank pane: `clipWindowContent` measured width from `lines[0]` | `b9f770b` | revert `internal/app/render_helpers.go` hunk | `TestAltScreenPaneSurvivesFocusSwitch`, `TestLeftmostTileWithBlankFirstLineIsNotDiscarded` | **caught** |
| Blank pane: a transient blank frame became the render cache | `11a0023` | neuter the `isBlankRender` guard in `cacheRender` | none | **not caught** |
| Torn cell buffer: emulator resized without the window I/O lock | `fd1463e` | drop both `LockIO`/`UnlockIO` pairs around `Terminal.Resize` in `internal/app/session.go` | none (2 full runs) | **not caught** |
| Mouse: the wheel announced a mode, stranded the user in it, and a drag moved the window instead of selecting | whole change | build the merge-base (`2005b01`) and point `TUIOS_E2E_BIN` at it | `TestWheelScrollShowsScrollbackWithoutAnnouncingAMode`, `TestWheelDownToBottomReturnsToLiveOutput`, `TestTypingWhileScrolledSnapsBackToLiveOutput`, `TestDragSelectionCopiesOnRelease`, `TestDoubleClickCopiesTheWord` | **caught** |
| Mouse: the wheel over a pane that asked for the mouse | n/a, never broken | same binary | none, and that is correct: `TestMouseTrackingAppKeepsItsOwnWheel` passes on both, because it guards behaviour that already worked | **guard, not a control** |
| Mouse: a triple-click wrote the word to the clipboard before the line | whole change | build `f5a6f17` (the merge of #106 and #107) and point `TUIOS_E2E_BIN` at it | `TestTripleClickCopiesTheLineExactlyOnce`, reporting `2 clipboard writes: ["/opt/dblclick/word.txt" "PREFIX /opt/dblclick/word.txt SUFFIX"]` | **caught** |

### The mouse row is a whole-change control, not a single-hunk one

The mouse tests were written against a change whose whole point is a different
interaction, not against a bug with one faulty line, so the control is the
merge-base binary rather than a hunk revert:

```sh
git worktree add --detach /tmp/negctl origin/main
(cd /tmp/negctl && go build -o /tmp/tuios-main ./cmd/tuios)
cd e2e/tui && TUIOS_E2E=1 TUIOS_E2E_BIN=/tmp/tuios-main go test -count=1 \
  -run 'TestWheel|TestTypingWhileScrolled|TestMouseTracking|TestDragSelection|TestDoubleClick' -timeout 900s .
```

Each failure names the old behaviour: "COPY MODE" on the dock during a scroll,
`echo` never producing its marker because the keystrokes were eaten as vim
motions, and an empty list of clipboard writes because a drag moved the window
instead of selecting.

The triple-click control is run the same way against `f5a6f17`. It is worth
noting why it needed the suite's own `clickAt` helper fixed first: the helper
used to send three presses and one trailing release, so the whole gesture
produced a single release and the intermediate word copy never happened. The
bug was real and the harness could not see it. `clickAt` now sends a release
per press, which is what a mouse does.

### Why the two blank-pane and torn-buffer entries are not caught, and what does cover them

**The blank-frame cache (`11a0023`)** needs a render to land in the gap between
a full-screen application clearing the alternate screen and painting it. Once
the application does paint, that output re-marks the window dirty and the pane
repairs itself, so a black-box observer sees the correct screen either way.
Widening the gap artificially does not help: during a deliberately long gap the
pane is legitimately blank on the fixed build too, so there is nothing to
distinguish. The fix's own commit message says the same thing, and its tests
assert on `renderTerminal`'s output and on the cache directly for exactly this
reason. Coverage lives in `internal/app/blank_alt_screen_cache_test.go`.

**The unlocked emulator resize (`fd1463e`)** is a data race. It corrupts the
cell buffer only when a state sync lands mid-write or mid-render, which needs
the race detector on tuios's own goroutines to observe reliably. This package
runs tuios as a child process, so `-race` on the test binary instruments the
harness and not the program under test. Coverage lives in
`internal/app/state_sync_race_test.go`, which floods a daemon window while
applying geometry-changing state syncs under `-race`.

Both of those are genuine gaps in *this* suite, not in the project's coverage.
The general lesson is that end-to-end screen assertions are the right tool for
bugs whose symptom is a wrong screen that persists, and the wrong tool for bugs
whose symptom is a narrow timing window or a memory race.

## Tests without a specific negative control

`TestScrolledOutputRendersCorrectly`, `TestScrollbackModeShowsEarlierOutput`,
and the interactive-surface tests (`TestWindowCreateAndClose`,
`TestRenameWindow`, `TestFocusCycleWithRapidKeyRepeat`, `TestWorkspaceSwitch`,
`TestMinimizeAndRestore`, `TestZoomToggle`, `TestResizeKeepsPaneContent`,
`TestTwoClientsSeeConsistentState`) are not tied to one commit. They cover
surface that had no test at all. They were written to fail loudly rather than
silently: each waits for content a shell computed, so a frozen or blanked UI
fails on the step it broke instead of passing against a stale screen.

Two of them earned their keep during development by failing against the
*fixed* binary for real reasons, which is documented in the commit history:
`countWindows` originally misread the dock, and the tiling assertion originally
waited on a toast that other toasts push off screen.
