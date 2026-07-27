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
| Mouse: the wheel announced a mode, stranded the user in it, and a drag moved the window instead of selecting | whole change | build the merge-base (`2005b01`) and point `TUIOS_E2E_BIN` at it | `TestWheelScrollShowsScrollbackWithoutAnnouncingAMode`, `TestWheelDownToBottomReturnsToLiveOutput`, `TestTypingWhileScrolledSnapsBackToLiveOutput`, `TestDragSelectionCopiesOnRelease`, `TestDoubleClickCopiesAWordAndTripleClickTheLine` | **caught** |
| Mouse: the wheel over a pane that asked for the mouse | n/a, never broken | same binary | none, and that is correct: `TestMouseTrackingAppKeepsItsOwnWheel` passes on both, because it guards behaviour that already worked | **guard, not a control** |
| Clipboard: every mouse release copies, so a bare single click clobbers the clipboard | n/a, injected | `deliberate := moved \|\| window.ClickCount >= 2` → `deliberate := true` in `internal/input/mouse_select.go` | `TestDoubleClickCopiesAWordAndTripleClickTheLine` ("the gesture wrote the clipboard more times than it should: got [\"b\"], want []") | **caught, and invisible before this change** |
| Drag state never cleared on release, so one click freezes every pane forever | n/a, injected | drop `o.Dragging = false` from the copy-mode branch of `handleMouseRelease` | `TestClickInPaneDoesNotFreezeOutput` | **caught, and invisible before this change** |

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

## The two mouse controls that were invisible until the helpers were fixed

Both rows marked "invisible before this change" were measured, not reasoned
about. The procedure for each: build a binary with the fault, run the **old**
helpers against it (the `origin/main` copy of `e2e/tui`), then the **new** ones.

```sh
git worktree add --detach /tmp/negctl origin/main
# inject one fault in /tmp/negctl, then
(cd /tmp/negctl && go build -o /tmp/tuios-fault ./cmd/tuios)

# old helpers
cd /tmp/negctl/e2e/tui && TUIOS_E2E=1 TUIOS_E2E_BIN=/tmp/tuios-fault go test -count=1 -run '<tests>' .
# new helpers
cd e2e/tui           && TUIOS_E2E=1 TUIOS_E2E_BIN=/tmp/tuios-fault go test -count=1 -run '<tests>' .
```

**Eager clipboard copy.** Old helpers: `TestDoubleClickCopiesAWordAndTripleClickTheLine`
and `TestDragSelectionCopiesOnRelease` both PASS. New helpers: the multi-click
test FAILS on the stray write. `clickAt` used to send n presses and one trailing
release, so the release that follows the first click of a gesture was never
generated and nothing that happens on it could be observed; and
`waitForClipboard` asked only whether the wanted text was somewhere in the list
of writes, which cannot see a write that should not be there. The gesture now
asserts its whole sequence of writes: none for a single click, one for a double,
two for a triple, because a triple passes through the word on its way to the
line and each release is a real release.

**A click that freezes every pane.** Old helpers: all thirteen mouse and
context-menu tests PASS. New helpers: `TestClickInPaneDoesNotFreezeOutput`
FAILS, waiting the full 20s for output that never arrives. `leftClick` and
`shiftRightClick` sent a press and no release at all, and a left press inside a
pane sets `OS.Dragging`, which makes `app.updateTerminals` return early: while
it is set tuios stops polling every pane. Sending this test's click press-only
against a *correct* binary reproduces the same 20s timeout, which is the
measurement that the old shape was not a smaller version of a real gesture but a
state no user can reach.

## What this harness structurally cannot observe

Some things cannot be simulated from here at all. They are listed so that nobody
writes a helper for them, watches it pass, and believes it.

**Pointer motion outside a drag.** `cmd/tuios/run.go` installs
`tea.WithFilter(filterMouseMotion)`, a whitelist that discards every
`tea.MouseMotionMsg` unless a window drag, a window resize, an overlay drag, the
scrollback browser, or a pane running a mouse-tracking application is active. In
any other state the model never receives motion, so hover behaviour is not
merely untested here, it is unobservable: a helper that sends motion and an
assertion on its effect would be asserting on an event the shipping binary
throws away. This is why `mouseHover` carries a warning and is used by exactly
one test, `TestBareMotionReachesAnEventTrackingApp`, which drives the one state
where bare motion does get through. Note in particular that as of this commit
`app.ContextMenuHover` is unreachable in the shipping binary for this reason: an
open context menu is not one of the whitelisted states, so moving the pointer
over a menu row cannot highlight it. If the whitelist gains that state, this
paragraph needs revisiting and hover over a menu becomes testable from here.
Motion *is* delivered during `mouseDrag`, because
the press that opens the drag sets `OS.Dragging` before the first motion report
arrives, so the selection, window-move and resize paths are genuinely covered.

**Chords the user's terminal eats first.** The harness writes bytes into a PTY,
so it can only send what a terminal would send. Anything a real terminal
intercepts before the application sees it, shift+click bindings in kitty being
the usual example, cannot be reproduced here, and neither can the *absence* of
those bytes be distinguished from tuios ignoring them. `TestContextMenuTargets`
asserts that tuios acts on a shift+right-click it receives; whether the user's
terminal will deliver one is outside this suite entirely.

**Typing speed.** `SendKeys` writes a whole string in one `write(2)`, so a
command and its Enter arrive as a single burst that bubbletea parses into
back-to-back key events. That is closer to a paste than to typing. Timing-
sensitive input handling, key repeat coalescing and the insert guard's exact
window are covered by unit tests in `internal/input`, not here.

**Races and narrow timing windows.** See the two rows above about the blank
alt-screen cache and the unlocked emulator resize: the program under test is a
separate process, so `-race` on this package instruments the harness and not
tuios.

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
