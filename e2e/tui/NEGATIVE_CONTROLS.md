# Negative controls for the end-to-end suite

A regression test that has never been observed to fail on broken code is not
evidence. Every test in this package that claims to cover a specific bug was run
against a binary built with that bug's fix removed, and the result is recorded
below. Two of them are **not** caught here; that is written down rather than
papered over, because a suite whose real coverage is unknown is worse than no
suite.

## Two rules for every control

Three green suites hid three real defects in one week. All three had a negative
control, and all three controls passed. These two rules are what the misses have
in common. They apply to every test in the project, not only this suite.

### 1. A negative test needs its positive half

A test that asserts "X does not happen when Y" must also assert, in the same
fixture, "X happens when not Y".

Without the positive half, the negative half may never have tested Y at all. A
gate written `if A && B && C` is only a test of `C` while `A` and `B` are true.
One privacy gate was asserted by a fixture that failed an earlier term, so the
test read the right answer off the wrong reason, and the gate could be deleted
with three packages still green.

The positive half costs one more row in the table. If it fails, the negative
half was never testing what its name says.

### 2. The control deletes the call site, not the function

When a pull request adds a feature, cut its wiring: the `case`, the `Register`,
the route, the gate line. Do not cut the function under it.

A control on the function proves the test and the function are bound to each
other. It says nothing about whether anything calls the function. One feature
registered 18 actions that no key reached. Its eight controls all passed,
because all eight cut the handler and the fault was the switch above it.

Cut the wiring, run the named test, and watch it fail. If it does not fail, the
test enters the code below the fault, and the fault is what nobody is testing.

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
| Agent-state feature absent (no verb, no indicator) | whole feature | build `origin/main` and point `TUIOS_E2E_BIN` at it | `TestAgentStateIndicatorRenders` (fails at the set step: `Unknown command "set-agent-state"`) | **caught** |
| Two clients with different chrome laid the panes out in different boxes, dragging the shared PTYs between the two answers | n/a, see verdict | `paneReserve` in `internal/app/os_geometry.go` returns `m.OwnLayoutReserve()` instead of folding in `m.SessionReserve` | none | **not caught** — see below |
| Pane body one column wider than the renderer vouches for, with the wrap skipped | n/a, injected | append a space to every row of the cell loop's output in `internal/app/render_terminal.go` while still reporting `maxX` | `TestWideRunesKeepThePaneRectangleOnScreen` ("pane has no right border glyph beside its content at column 119, its body is not 78 columns wide"), `TestSkippingTheWrapDrawsTheSameScreenAsWrapping` | **caught** |
| A workspace round trip threw away where the strip was scrolled to | n/a, see verdict | `ScrollingOnFocusChange` in `internal/app/os_scrolling.go` calls `sl.ScrollToFocusedColumn` instead of `sl.EnsureFocusedVisible` | `TestWorkspaceRoundTripKeepsTheScrolledStrip` ("a workspace round trip moved the strip. The focused column was on screen the whole time") | **caught** |
| The strip stops revealing a focused column with none of it on screen | n/a, injected | `EnsureFocusedVisible` in `internal/layout/scrolling.go` returns without calling `reveal` | `TestWorkspaceRoundTripRevealsAHiddenColumn` ("the round trip left the focused column off screen") | **caught**, and it is the positive control on the row above |
| A click that only focuses a borderless pane untiled it, resized it, and retiled it, so the shell took two SIGWINCH and printed a new line | n/a, see verdict | put the untile back on the press: move the `win.Tiled = false` / `win.Resize` pair from `untilePaneForDrag` (called from the drag branch of `handleMouseMotion`) back into `beginWindowDrag` in `internal/input/mouse_click.go` | `TestClickToFocusAddsNoLineToThePane` ("the pane took 6 resizes across three click-to-focus round trips") | **caught** |
| A pane being dragged keeps the borderless allowance, so it draws no border | n/a, injected | make `untilePaneForDrag` return before it clears `Tiled` | `TestDraggingAPaneDoesResizeIt`, since replaced by `TestDraggingTheDividerResizesOnce` | **caught** at the time; see the drag-announcement rows below for why that test is gone |
| A drag announced every size it passed through, so rearranging tiles left a new line in each pane | n/a, see verdict | drop `m.holdGestureAnnouncements()` from the `tea.MouseClickMsg` case in `internal/app/update.go` | `TestDragIntoASameSizeSlotResizesNothing` ("the pane took 2 resizes across a drag that returned it to the same size"), `TestDragIntoADifferentSizeSlotResizesOnce` ("announced 3 of them, want 1"); unit `TestDragBackIntoASameSizeSlotTellsTheGuestNothing` | **caught** |
| Nothing ends the hold, so a pane never learns its size again | n/a, injected, cuts all four call sites | drop `o.ReleaseGestureAnnouncements()` from the release defer in `internal/input/mouse_release.go` and from the `MouseReleaseMsg` case in `internal/input/handler.go`, `m.releaseGestureAnnouncements()` from `endLostGesture`, and `m.releaseStaleAnnounceHold()` from the maintenance tick | `TestDraggingTheDividerResizesOnce` ("announced 0 resizes, want 1"), `TestDragIntoADifferentSizeSlotResizesOnce` ("announced 0 of them, want 1"); unit `TestDraggingTheDividerTellsTheGuestOnce` | **caught**, and it is the positive control on the row above |
| The release handler alone stops ending the hold | n/a, injected | drop only the two `o.ReleaseGestureAnnouncements()` calls in `internal/input` | none here, and that is correct: the maintenance tick's backstop still ends the hold inside the three seconds these tests wait, which is the redundancy it exists for. Unit `TestDraggingTheDividerTellsTheGuestOnce` fails ("told the guest [], want one size") | **not caught here, caught in `internal/input`** |
| The idle diet sleeps through a stranded hold | n/a, injected, cuts the call site | make the `m.staleAnnounceHold()` branch in `tickNeedsWork` unreachable, or drop `m.releaseStaleAnnounceHold()` from the tick body | unit `TestAReleaseThatWentMissingStillEndsTheHold`, `TestAPointerGoneSilentStillEndsTheHold` ("the hold survived a tick with no button held") | **caught in `internal/app`** |
| A retile inside a gesture ends the gesture's own hold | n/a, injected | `ReleaseAnnouncements` in `internal/terminal/window_geometry.go` back to clearing the count instead of decrementing it | none here; unit `TestALayoutUpdateInsideAGestureDoesNotEndItsHold` ("a layout update inside the gesture told the pane [[58 28]]") | **not caught here, caught in `internal/app`** |
| Keyboard focus resizes the pane it moves to (a report from macOS, after the click and drag fixes; no keyboard path does this on Linux, so there is nothing to remove) | n/a, injected | add `Resize(Width-1, Height)` then `Resize(Width+1, Height)` to `FocusWindow` in `internal/app/os_window.go`, after the cache invalidation, and point `TUIOS_E2E_BIN` at that build | `TestFocusTabAddsNoLine`, `TestFocusAltArrowsAddNoLine`, `TestFocusAltArrowsStandaloneAddNoLine`, `TestFocusAltArrowsScrollingAddNoLine` (each "the pane took 3 resizes across ..., and printed a line for each"); positive half `TestSwapIntoANarrowerSlotIsTold` in the same fixture | **caught** (4 of 4 run) |
| The same, with fish in the panes, animations on, a simulated kitty host and the ghostty backend, after a recording showed the bug with fish on macOS | n/a, injected | the same `FocusWindow` mutant | `TestFocusAltArrowsFishAddNoLine`, `TestFocusTabFishAddsNoLine` ("the pane took 3 resizes") | **caught** |
| The render trace stops recording the sizes handed to a guest, so the diagnostic the macOS report is asked to run says nothing | n/a, injected, cuts the wiring | drop `terminal.AnnounceTrace = traceAnnounce` from the `init` in `internal/app/render_trace.go` and point `TUIOS_E2E_BIN` at that build | `TestRenderTraceRecordsEachAnnouncement` ("the trace holds 0 announce lines after a swap of two panes, want at least 2"); unit `TestAnnounceTraceSeesEverySizeHandedToTheGuest` fails when the call in `tellGuest` is cut ("a resize to 80x40 traced [], want [[80 40]]"), and `TestPTYResizeIsLogged` when the `LogBasic` in `PTY.Resize` is cut ("log has 0 lines") | **caught** |
| Two clients whose configs disagree on shared borders partitioned the box with different arithmetic, dragging the shared PTYs between the two answers | whole change | build the pre-fix tree (`20f17bbd`) and point `TUIOS_E2E_BIN` at it | `TestGeometryConfigDisagreementDoesNotMovePanes` ("the second client's attach moved the panes": 61,0 59x38 dragged to 60,0 60x38) | **caught** |
| Raising a floating pane reshuffled the others: `RecalcZOrder` renumbered by list position | n/a, injected | drop the `slices.SortStableFunc` over the current Z in `internal/app/os_window.go` | `TestClickingAPaneKeepsTheOthersInOrder` ("clicking A put C over B"); unit `TestRecalcZOrderKeepsTheStackingOrder` | **caught** |
| Floating band unbounded at 999 plus Z, over the which-key overlay, the clock, the log viewer and the scrollback browser | n/a, injected | `ZIndexSeparators`/`ZIndexAnimating` back to 998/999 and `windowLayerZ` back to `ZIndexSeparators + 1 + Z` | `TestAFloatingPaneStaysUnderTheWhichKeyOverlay` ("row 33 still shows the floating pane through the which-key overlay"); unit `TestFloatingWindowsStayBelowEveryOverlay` | **caught** |
| Tiling off under the scrolling layout left the columns past the edge at x = -144 | n/a, injected | drop `m.bringPanesIntoView()` from `leaveTiling` in `internal/app/tiling.go` | `TestTilingOffBringsTheStripOnScreen`, all four doors ("3 pane(s) are still off screen"); unit `TestTilingOffBringsTheStripOnScreen`, `TestEveryWayOfTurningTilingOffAgrees` | **caught** |
| Tape `DisableTiling` flipped the flag and nothing else, leaving borderless panes with no dividers | n/a, injected | `DisableTiling` in `internal/app/os_tape_executor.go` back to `m.AutoTiling = false` | `TestTapeTilingCommandsSettleTheBorders` ("after DisableTiling: 0 pane corners on screen, want 2"); unit `TestEveryWayOfTurningTilingOffAgrees/tape_DisableTiling` | **caught** |
| A peer that watched tiling turn off kept its panes borderless | n/a, injected | gate the `tilingWasOn && !m.AutoTiling` case in `ApplyStateSync` behind `false` | `TestAPeerSeesTilingTurnOff` ("the peer: 0 pane corners on screen, want 2"); unit `TestPeerTurningTilingOffClearsTheBorderFlags` | **caught** |
| The rest of the tiling-switch change: the fresh tree on enable, the stale-tree rebuild, the palette row's own copy, the preselection left armed, the float's column left in the strip, a dragged float under the other floats, the minimized pane's border flag, session-info saying `bsp`, and a door that never flips | n/a, injected, one hunk each | see `internal/app/tiling_switch_test.go`; nine mutations | unit tests only, each fails as an assertion | **caught** (14 of 14 mutations in this change) |
| The spotlight dimmed the text outside the beam and left the screen that text sat on lit, at every setting up to the maximum | n/a, injected | `buildRun` in `internal/app/spotlight.go` blends the background toward `s.groundBg` again instead of `spotlightDark` | `TestSpotlightTurnsTheBackgroundDownOnScreen` ("the background outside the beam carries 114 of light against the ground's 106"); unit `TestSpotlightTurnsTheLightDownOnTheBackground`, `TestSpotlightDimsALightThemeDownwards` | **caught** |
| A cell that named no background of its own was given none, which is most of a real screen | n/a, injected | `dimCell` writes the background only when the cell already carried one | `TestSpotlightTurnsTheBackgroundDownOnScreen` ("the marker outside the beam came back with no background at all"); unit `TestSpotlightDimsTextTheGuestLeftAtTheDefault` | **caught** |
| The beam followed the focused pane's cursor rather than the pointer | n/a, injected | `defaultSpotlightConfig` in `internal/config/spotlight.go` back to `SpotlightFollowCursor` | `TestSpotlightFollowsTheMouse` ("the marker never came back to full brightness after the pointer moved onto it"); unit `TestSpotlightDefaultsAreTheOnesTheRegistryPublishes` | **caught** |
| Nothing read the pointer's position into the beam | n/a, injected, cuts the call site | drop the `LastMouseX`/`LastMouseY` branch from `spotlightAnchor` in `internal/app/spotlight.go` | `TestSpotlightFollowsTheMouse` | **caught** |
| The rest of the spotlight dim change: the blend cache, a coloured blank, the beam's own middle, a cell that names no colour, a wide glyph's placeholder, the registry's published default, the cursor stand-in, and the stand-in latched on the first frame | n/a, injected, one hunk each | see `internal/app/spotlight_test.go` and `internal/config/spotlight_test.go`; seven mutations | unit tests only, each fails as an assertion | **caught** (11 of 11 unit mutations, 4 of 4 here) |
| Untheme the beam ignored the dim setting: the whole screen went to SGR 2, so 10 and 95 drew the same frame | n/a, injected | `dimmable` in `internal/app/spotlight.go` back to `return s.groundBg != nil`, which is the blanket faint the pass used to take | `TestSpotlightDimsAThemelessScreenByTheSetting` ("dim 10 left the far border at 605 and dim 95 at 605; the setting draws the same frame at both ends"); unit `TestSpotlightDimsWhatItCanResolveWithNoTheme`, `TestSpotlightDimReachesThePassLive` | **caught** |
| Nothing wrote SGR 2 for a colour the pass cannot resolve, so a themeless screen kept most of itself at full brightness | n/a, injected, cuts the call site | drop the `cell.Style.Attrs \|= uv.AttrFaint` write from `dimCell` | `TestSpotlightGoesFaintOnWhatItCannotResolve` ("with no theme a cell carrying no colour was left at full brightness outside the beam"); unit `TestSpotlightDimsWhatItCanResolveWithNoTheme` | **caught**, and it is the positive control on the row above |
| A config saved by an editor never reached the running client: the watch followed the inode vim renames away | n/a, injected | `w.Add(filepath.Dir(cw.path))` in `internal/config/watcher.go` back to `w.Add(cw.path)` | `TestConfigEditedOnDiskReachesTheScreen` ("a second config save never reached the screen, so the watch died on the first one"), `TestBrokenConfigOnDiskKeepsWhatIsRunning`; unit `TestWatcherSeesAnEditorsSave` | **caught** |
| A config file that does not parse was ignored in silence, so every later save looked like it had worked | n/a, injected, cuts the call site | drop the `p.Send(app.ConfigReloadFailedMsg{...})` branch from the watcher callback in `cmd/tuios/run.go` | `TestBrokenConfigOnDiskKeepsWhatIsRunning` ("a config file that does not parse said nothing on screen") | **caught** |
| The rest of the themeless dim and the config watch: the xterm-default guess for the host's sixteen, a theme no longer settling them, the debounce, a broken file's content recorded as in force, the unchanged-content guard, a broken file delivered as the defaults, tuios's own save coming back as an edit (both halves), the beam not reseeded from the file, the failure notification, the four-of-seven section fill, and the reloaded config never reaching the model | n/a, injected, one hunk each | see `internal/app/spotlight_test.go`, `internal/app/config_live_test.go` and `internal/config/watcher_test.go`; twelve mutations | unit tests only, each fails as an assertion | **caught** (15 of 15 unit mutations, 4 of 4 here) |

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

## Why the two-client chrome test catches nothing

`TestOneClientsRailDoesNotMoveAnotherClientsPanes` was written expecting to fail
with the agreed layout reserve removed, and it does not. It is kept as a
deliberate passes-both-ways control, and the reason is worth having written
down, because it says what this harness can and cannot see about multi-client
layout.

With the reserve removed, the two clients still reach the same frame - by
fighting to it. The client with the rail reads the other's rectangles as a
layout for somebody else's screen, works out its own and pushes it; the client
without one reads what comes back as settled, because a layout that sits inside
a wider box and still reaches its far edges cannot be told from one that belongs
there. So the wider client always yields and the frames converge.

What that convergence costs is not on the grid: each round trip resizes the
shared PTYs twice, once to the pushed rectangles and once back. That is what
damages scrollback, and it is counted rather than looked at, by
`TestFocusSwitchResizesNothing` and `TestTwoClientsAgreeOnEveryPaneSize` in
`internal/app` - both of which do fail with the reserve removed (four resizes
per pane switch, and the two clients running the same shells at different
sizes). The frame test guards the property those cannot see: that what the two
people are looking at is the same layout. It would catch a change that bought
the resizes back by letting the frames drift apart.

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
