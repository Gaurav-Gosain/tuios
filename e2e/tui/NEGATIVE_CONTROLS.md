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
| File search cannot open from a terminal pane through the leader shortcut | whole change | build main before the file search change and point `TUIOS_E2E_BIN` at it | `TestSidebarFileSearch/standalone` and `/daemon` (both time out waiting for `Search files`) | **caught** (2 of 2 run) |
| Sidebar file search absent | whole feature | build main before the file search change and point `TUIOS_E2E_BIN` at it | `TestSidebarFileSearch/standalone` and `/daemon` (both time out waiting for `Search files`) | **caught** (2 of 2 run) |
| Sidebar Shift+Enter never reaches the editor action | n/a, injected | keep the feature but clear the default `file_edit` binding in `internal/config/userconfig.go` | `TestSidebarFileEditor/standalone` and `/daemon` (both time out waiting for the binary-file refusal, before the positive editor assertion) | **caught** (2 of 2 run) |
| Freeze: render path took the window I/O read lock twice | `6ca26b1` | revert `internal/app/render_terminal.go` hunk | `TestSustainedOutputKeepsRendering` (hangs at round 1/6), `TestSoakMixedActivity` (hangs at cycle 2/8) | **caught** |
| Blank pane: `clipWindowContent` measured width from `lines[0]` | `b9f770b` | revert `internal/app/render_helpers.go` hunk | `TestAltScreenPaneSurvivesFocusSwitch`, `TestLeftmostTileWithBlankFirstLineIsNotDiscarded` | **caught** |
| Blank pane: a transient blank frame became the render cache | `11a0023` | neuter the `isBlankRender` guard in `cacheRender` | none | **not caught** |
| Torn cell buffer: emulator resized without the window I/O lock | `fd1463e` | drop both `LockIO`/`UnlockIO` pairs around `Terminal.Resize` in `internal/app/session.go` | none (2 full runs) | **not caught** |
| Mouse: the wheel announced a mode, stranded the user in it, and a drag moved the window instead of selecting | whole change | build the merge-base (`2005b01`) and point `TUIOS_E2E_BIN` at it | `TestWheelScrollShowsScrollbackWithoutAnnouncingAMode`, `TestWheelDownToBottomReturnsToLiveOutput`, `TestTypingWhileScrolledSnapsBackToLiveOutput`, `TestDragSelectionCopiesOnRelease`, `TestDoubleClickCopiesAWordAndTripleClickTheLine` | **caught** |
| Mouse: the wheel over a pane that asked for the mouse | n/a, never broken | same binary | none, and that is correct: `TestMouseTrackingAppKeepsItsOwnWheel` passes on both, because it guards behaviour that already worked | **guard, not a control** |
| Clipboard: every mouse release copies, so a bare single click clobbers the clipboard | n/a, injected | `deliberate := moved \|\| window.ClickCount >= 2` → `deliberate := true` in `internal/input/mouse_select.go` | `TestDoubleClickCopiesAWordAndTripleClickTheLine` ("the gesture wrote the clipboard more times than it should: got [\"b\"], want []") | **caught, and invisible before this change** |
| Drag state never cleared on release, so one click freezes every pane forever | n/a, injected | drop `o.Dragging = false` from the copy-mode branch of `handleMouseRelease` | `TestClickInPaneDoesNotFreezeOutput` | **caught, and invisible before this change** |
| Agent-state feature absent (no verb, no indicator) | whole feature | build `origin/main` and point `TUIOS_E2E_BIN` at it | `TestAgentStateIndicatorRenders` (fails at the set step: `Unknown command "set-agent-state"`) | **caught** |
| Two clients with different chrome laid the panes out in different boxes, dragging the shared PTYs between the two answers | n/a, see verdict | `paneReserve` in `internal/app/os_geometry.go` returns `m.OwnLayoutReserve()` instead of folding in `m.SessionReserve` | none | **not caught**, see below |
| Pane body one column wider than the renderer vouches for, with the wrap skipped | n/a, injected | append a space to every row of the cell loop's output in `internal/app/render_terminal.go` while still reporting `maxX` | `TestWideRunesKeepThePaneRectangleOnScreen` ("pane has no right border glyph beside its content at column 119, its body is not 78 columns wide"), `TestSkippingTheWrapDrawsTheSameScreenAsWrapping` | **caught** |
| A workspace round trip threw away where the strip was scrolled to | n/a, see verdict | `ScrollingOnFocusChange` in `internal/app/os_scrolling.go` calls `sl.ScrollToFocusedColumn` instead of `sl.EnsureFocusedVisible` | `TestWorkspaceRoundTripKeepsTheScrolledStrip` ("a workspace round trip moved the strip. The focused column was on screen the whole time") | **caught** |
| The strip stops revealing a focused column with none of it on screen | n/a, injected | `EnsureFocusedVisible` in `internal/layout/scrolling.go` returns without calling `reveal` | `TestWorkspaceRoundTripRevealsAHiddenColumn` ("the round trip left the focused column off screen") | **caught**, and it is the positive control on the row above |
| A click that only focuses a borderless pane untiled it, resized it, and retiled it, so the shell took two SIGWINCH and printed a new line | n/a, see verdict | put the untile back on the press: move the `win.Tiled = false` / `win.Resize` pair from `untilePaneForDrag` (called from the drag branch of `handleMouseMotion`) back into `beginWindowDrag` in `internal/input/mouse_click.go` | `TestClickToFocusAddsNoLineToThePane` ("the pane took 6 resizes across three click-to-focus round trips") | **caught** |
| A pane being dragged keeps the borderless allowance, so it draws no border | n/a, injected | make `untilePaneForDrag` return before it clears `Tiled` | `TestDraggingAPaneDoesResizeIt`, since replaced by `TestDraggingTheDividerResizesOnce` | **caught** at the time; see the drag-announcement rows below for why that test is gone |
| A drag announced every size it passed through, so rearranging tiles left a new line in each pane | n/a, see verdict | drop `m.holdGestureAnnouncements()` from the `tea.MouseClickMsg` case in `internal/app/update.go` | `TestDragIntoASameSizeSlotResizesNothing` ("the pane took 2 resizes across a drag that returned it to the same size"), `TestDragIntoADifferentSizeSlotResizesOnce` ("announced 3 of them, want 1"); unit `TestDragBackIntoASameSizeSlotTellsTheGuestNothing` (since removed) | **caught** |
| Nothing ends the hold, so a pane never learns its size again | n/a, injected, cuts all four call sites | drop `o.ReleaseGestureAnnouncements()` from the release defer in `internal/input/mouse_release.go` and from the `MouseReleaseMsg` case in `internal/input/handler.go`, `m.releaseGestureAnnouncements()` from `endLostGesture`, and `m.releaseStaleAnnounceHold()` from the maintenance tick | `TestDraggingTheDividerResizesOnce` ("announced 0 resizes, want 1"), `TestDragIntoADifferentSizeSlotResizesOnce` ("announced 0 of them, want 1"); unit `TestDraggingTheDividerTellsTheGuestOnce` | **caught**, and it is the positive control on the row above |
| The release handler alone stops ending the hold | n/a, injected | drop only the two `o.ReleaseGestureAnnouncements()` calls in `internal/input` | none here, and that is correct: the maintenance tick's backstop still ends the hold inside the three seconds these tests wait, which is the redundancy it exists for. Unit `TestDraggingTheDividerTellsTheGuestOnce` fails ("told the guest [], want one size") | **not caught here, caught in `internal/input`** |
| The idle diet sleeps through a stranded hold | n/a, injected, cuts the call site | make the `m.staleAnnounceHold()` branch in `tickNeedsWork` unreachable, or drop `m.releaseStaleAnnounceHold()` from the tick body | unit `TestAReleaseThatWentMissingStillEndsTheHold`, `TestAPointerGoneSilentStillEndsTheHold` ("the hold survived a tick with no button held") | **caught in `internal/app`** |
| A retile inside a gesture ends the gesture's own hold | n/a, injected | `ReleaseAnnouncements` in `internal/terminal/window_geometry.go` back to clearing the count instead of decrementing it | none here; unit `TestALayoutUpdateInsideAGestureDoesNotEndItsHold` ("a layout update inside the gesture told the pane [[58 28]]") | **not caught here, caught in `internal/app`** |
| Keyboard focus resizes the pane it moves to (a report from macOS, after the click and drag fixes; no keyboard path does this on Linux, so there is nothing to remove) | n/a, injected | add `Resize(Width-1, Height)` then `Resize(Width+1, Height)` to `FocusWindow` in `internal/app/os_window.go`, after the cache invalidation, and point `TUIOS_E2E_BIN` at that build | `TestFocusTabAddsNoLine`, `TestFocusAltArrowsAddNoLine`, `TestFocusAltArrowsStandaloneAddNoLine`, `TestFocusAltArrowsScrollingAddNoLine` (each "the pane took 3 resizes across ..., and printed a line for each"); positive half `TestSwapIntoANarrowerSlotIsTold` in the same fixture | **caught** (4 of 4 run) |
| The same, with fish in the panes, animations on, a simulated kitty host and the ghostty backend, after a recording showed the bug with fish on macOS | n/a, injected | the same `FocusWindow` mutant | `TestFocusAltArrowsFishAddNoLine`, `TestFocusTabFishAddsNoLine` ("the pane took 3 resizes") | **caught** |
| The render trace stops recording the sizes handed to a guest, so the diagnostic the macOS report is asked to run says nothing | n/a, injected, cuts the wiring | drop `terminal.AnnounceTrace = traceAnnounce` from the `init` in `internal/app/render_trace.go` and point `TUIOS_E2E_BIN` at that build | `TestRenderTraceRecordsEachAnnouncement` ("the trace holds 0 announce lines after a swap of two panes, want at least 2"); unit `TestAnnounceTraceSeesEverySizeHandedToTheGuest` (since removed) fails when the call in `tellGuest` is cut ("a resize to 80x40 traced [], want [[80 40]]"), and a unit test, since removed, failed when the `LogBasic` in `PTY.Resize` was cut ("log has 0 lines") | **caught** |
| Two clients whose configs disagree on shared borders partitioned the box with different arithmetic, dragging the shared PTYs between the two answers | whole change | build the pre-fix tree (`20f17bbd`) and point `TUIOS_E2E_BIN` at it | `TestGeometryConfigDisagreementDoesNotMovePanes` ("the second client's attach moved the panes": 61,0 59x38 dragged to 60,0 60x38) | **caught** |
| Raising a floating pane reshuffled the others: `RecalcZOrder` renumbered by list position | n/a, injected | drop the `slices.SortStableFunc` over the current Z in `internal/app/os_window.go` | `TestClickingAPaneKeepsTheOthersInOrder` ("clicking A put C over B"); unit `TestRecalcZOrderKeepsTheStackingOrder` | **caught** |
| Floating band unbounded at 999 plus Z, over the which-key overlay, the clock, the log viewer and the scrollback browser | n/a, injected | `ZIndexSeparators`/`ZIndexAnimating` back to 998/999 and `windowLayerZ` back to `ZIndexSeparators + 1 + Z` | `TestAFloatingPaneStaysUnderTheWhichKeyOverlay` ("row 33 still shows the floating pane through the which-key overlay"); unit `TestFloatingWindowsStayBelowEveryOverlay` | **caught** |
| Tiling off under the scrolling layout left the columns past the edge at x = -144 | n/a, injected | drop `m.bringPanesIntoView()` from `leaveTiling` in `internal/app/tiling.go` | `TestTilingOffBringsTheStripOnScreen`, all four doors ("3 pane(s) are still off screen"); unit `TestTilingOffBringsTheStripOnScreen`, `TestEveryWayOfTurningTilingOffAgrees` | **caught** |
| Tape `DisableTiling` flipped the flag and nothing else, leaving borderless panes with no dividers | n/a, injected | `DisableTiling` in `internal/app/os_tape_executor.go` back to `m.AutoTiling = false` | `TestTapeTilingCommandsSettleTheBorders` ("after DisableTiling: 0 pane corners on screen, want 2"); unit `TestEveryWayOfTurningTilingOffAgrees/tape_DisableTiling` | **caught** |
| A peer that watched tiling turn off kept its panes borderless | n/a, injected | gate the `tilingWasOn && !m.AutoTiling` case in `ApplyStateSync` behind `false` | `TestAPeerSeesTilingTurnOff` ("the peer: 0 pane corners on screen, want 2"); unit `TestPeerTurningTilingOffClearsTheBorderFlags` (since removed) | **caught** |
| The rest of the tiling-switch change: the fresh tree on enable, the stale-tree rebuild, the palette row's own copy, the preselection left armed, the float's column left in the strip, a dragged float under the other floats, the minimized pane's border flag, session-info saying `bsp`, and a door that never flips | n/a, injected, one hunk each | see `internal/app/tiling_switch_test.go`; nine mutations | unit tests only, each fails as an assertion | **caught** (14 of 14 mutations in this change) |
| The spotlight dimmed the text outside the beam and left the screen that text sat on lit, at every setting up to the maximum | n/a, injected | `buildRun` in `internal/app/spotlight.go` blends the background toward `s.groundBg` again instead of `spotlightDark` | `TestSpotlightTurnsTheBackgroundDownOnScreen` ("the background outside the beam carries 114 of light against the ground's 106"); unit `TestSpotlightTurnsTheLightDownOnTheBackground` (since removed), `TestSpotlightDimsALightThemeDownwards` | **caught** |
| A cell that named no background of its own was given none, which is most of a real screen | n/a, injected | `dimCell` writes the background only when the cell already carried one | `TestSpotlightTurnsTheBackgroundDownOnScreen` ("the marker outside the beam came back with no background at all"); unit `TestSpotlightDimsTextTheGuestLeftAtTheDefault` (since removed) | **caught** |
| The beam followed the focused pane's cursor rather than the pointer | n/a, injected | `defaultSpotlightConfig` in `internal/config/spotlight.go` back to `SpotlightFollowCursor` | `TestSpotlightFollowsTheMouse` ("the marker never came back to full brightness after the pointer moved onto it"); unit `TestSpotlightDefaultsAreTheOnesTheRegistryPublishes` (since removed) | **caught** |
| Nothing read the pointer's position into the beam | n/a, injected, cuts the call site | drop the `LastMouseX`/`LastMouseY` branch from `spotlightAnchor` in `internal/app/spotlight.go` | `TestSpotlightFollowsTheMouse` | **caught** |
| A new BSP window split the last pane in the tree, the bottom-right one, whatever had focus, and a daemon session dropped the preselection (discussion #347) | whole change | build `origin/main` before the fix and point `TUIOS_E2E_BIN` at it | `TestBSPNewWindowSplitsTheFocusedPane`, all six cases ("the new pane is not inside the focused pane's old box": the new pane split the pane at 80,23), `TestBSPNewWindowSplitsTheFocusedPaneStandalone`, all three cases ("pane {X:80 Y:23 ...} moved") | **caught** |
| The daemon sync stops splitting the focused pane | n/a, injected, cuts the call site | drop the `m.insertSyncedWindow(created[0], focusBefore)` call from `adoptSyncedWindows` in `internal/app/session.go` | `TestBSPNewWindowSplitsTheFocusedPane`, all six cases ("the new pane is not inside the focused pane's old box") | **caught** |
| A daemon session drops the preselection on its way to the sync | n/a, injected, cuts the call site | make the `m.PreselectionDir` branch in the daemon half of `AddWindowIn` in `internal/app/os_window.go` unreachable | `TestBSPNewWindowSplitsTheFocusedPane`, the four preselect cases ("the new pane is not left of the focused pane", and above, below, right of); the two cases with no preselection pass, which is their positive half | **caught** |
| The standalone TUI stops splitting the focused pane | n/a, injected, cuts the call site | drop `targetID = fw.ID` from `AddWindowToBSPTree` in `internal/app/tiling_bsp.go` | `TestBSPNewWindowSplitsTheFocusedPaneStandalone`, all three cases; the daemon test passes, since that path does not run there | **caught** |
| An `a=T` carrying `U=1` (ntcharts/picture, `kitten icat --unicode-placeholder`) was treated as a real placement: rows reserved, which scrolled the guest's screen every frame, and the image placed at the cursor | whole change | stash the five hunks in `internal/app/kitty_passthrough_forward.go`, `kitty_file_medium.go`, `kitty_passthrough.go`, `kitty_passthrough_lifecycle.go` and `internal/vt/utf8.go` | `TestKittyVirtualPlacementLeavesThePaneToTheGuest/b64`, `/file`, `/daemon-b64` ("39 placements were drawn at a cursor position", "the guest's PLACEHOLDER-HEADER is gone from the pane"), `TestKittyPlaceholderSplitAcrossWritesNamesTheHostImage`, `TestKittyPlaceholdersPrintedBeforeTheImageNameIt` | **caught** (3 of 3 transports run; `/shm` skips without `/dev/shm`) |
| The same, the row reservation alone | n/a, injected, cuts the call site | drop the `if cmd.Virtual { break }` from the `TransmitPlace` case of `ForwardCommand` | `/file` ("PLACEHOLDER-HEADER is gone"); `/b64` passes, and that is correct: a chunked transmission's last chunk arrives as `a=t` and never reaches that branch | **caught** |
| The same, the chunked direct path alone | n/a, injected, cuts the call site | drop the `pending.AndPlace && pending.Virtual` block from `forwardTransmit` | `/b64`, `/daemon-b64` ("no virtual placement reached the host", "39 placements were drawn at a cursor position"), and the split and early tests through the lost text | **caught** |
| The same, the file and shared-memory path alone | n/a, injected, cuts the call site | drop the `andPlace && cmd.Virtual` block from `forwardFileTransmit` | `/file` ("40 placements of image 1 were positioned by tuios") | **caught** |
| The same, over ssh: a reused id went out as a self-placed `a=T` at the cursor | n/a, injected, cuts the call site | drop the `andPlace && cmd.Virtual` block from `forwardFileTransmitInline` | unit `TestKittyVirtualPlacementOverSSHIsNeverSelfPlaced` ("3 frames sent 1 transmissions and 0 virtual placements") | **caught in `internal/app`** |
| A placeholder cell split across two PTY reads was re-drawn with the guest's image id | n/a, injected, cuts the call site | drop the `rewriteKittyPlaceholder` call from `extendOpenGrapheme` in `internal/vt/utf8.go` | `TestKittyPlaceholderSplitAcrossWritesNamesTheHostImage` ("241 placeholder cells reached the host naming image 7"); every transport of the placement test too, with a cell or two each, which is where the pty's own read boundaries fell | **caught** |
| Placeholder cells printed before the image was transmitted kept the guest's id | n/a, injected, cuts the call site | the translator in `setupKittyPassthrough` calls `HostImageID` instead of `HostImageIDForPlaceholder` | `TestKittyPlaceholdersPrintedBeforeTheImageNameIt` ("cells by id: map[7:4012]") | **caught**, and it is the positive control on the row above: with the id allocated on demand the split test still passes, so the two hunks are tested apart |
| A daemon state broadcast flipped a streamed pane back to the normal screen, and the passthrough deleted the alternate-screen image | n/a, injected, cuts the call site | `updateWindowFromState` in `internal/app/session.go` calls `w.SetAltScreen(ws.IsAltScreen)` for every window again, without the `SubscribedPTYs` check (the tree before the fix) | `TestKittyAltScreenImageSurvivesStateSync` ("a daemon state broadcast took the image down: a=d a=d,d=i,i=1,q=2") | **caught** |
| A reused shared memory frame waited for the render loop: an `a=t` on arrival and an `a=p` on the next pass | n/a, injected, cuts the call site | drop the `streamFrameFitsPlacement` branch from `forwardFileTransmit` in `internal/app/kitty_passthrough_forward.go` (the tree before the fix) | `TestKittySharedMemoryStreamIsWrittenAtOnce` ("only 0 frames went out at once as a=T") | **caught** |
| A shared memory frame tuios dropped as a repeat of the one on screen stayed in `/dev/shm`: nobody read it, so nobody deleted it | n/a, injected, cuts the call site | drop the `releaseKittyMedium` call from the identical-frame branch of `forwardFileTransmit` in `internal/app/kitty_passthrough_forward.go` | `TestKittySharedMemoryFramesAreReleased/repeats` ("39 shared memory objects were never sent to the host and never deleted") | **caught**; `/daemon-unwatched` still passes, so the two call sites are tested apart |
| A shared memory frame in a daemon pane with no client attached stayed in `/dev/shm` | n/a, injected, cuts the call site | drop the `releaseUnreadKittyMedium` call from the kitty callback in `createPTY` in `internal/session/session.go` | `TestKittySharedMemoryFramesAreReleased/daemon-unwatched` ("40 shared memory objects were never sent to the host and never deleted") | **caught**; `/repeats` still passes |
| The daemon deleted any regular file in `/dev/shm` a pane named, whatever its size, so printed text could remove another program's segment | n/a, injected, cuts the call site | put back the bare `info.Mode().IsRegular()` test in place of `vt.KittyMediumIsFrame` in `releaseUnreadKittyMedium`, `internal/session/kitty_medium.go` | `TestKittySharedMemoryFramesAreReleased/daemon-unwatched-wrong-size` ("40 of 40 objects of the wrong size were deleted") | **caught**; `/daemon-unwatched` still passes, its positive half: the same path deletes frames of the right size |
| The client deleted an object of another size after it read the frame inline | n/a, injected, cuts the call site | drop the `vt.KittyMediumIsFrame` test from `releaseKittyMedium` in `internal/app/kitty_medium_release.go` | Unit `TestKittyMediumReleaseChecksTheFrame/wrong-size` ("tuios deleted an object whose size is not the frame's") | **caught**; `/right-size` passes, its positive half |
| The client deleted the object a pane named while graphics were off, a frame it never looked at | n/a, injected, cuts the call site | put back a `kittyMediumPath` and `releaseKittyMedium` call in the `!kp.enabled` branch of `ForwardCommand` in `internal/app/kitty_passthrough_forward.go` | Unit `TestKittyMediumReleaseChecksTheFrame/graphics-off` ("tuios deleted an object while graphics are off") | **caught** |
| The frame check ignored the owner or the size | n/a, injected | make `kittyOwnedByMe` always true, or drop the size compare, in `vt.KittyMediumIsFrame` (`internal/vt/kitty_medium_release.go`) | Unit `TestKittyMediumIsFrame/another_user` ("a frame owned by another user passed"); without the size compare, the four wrong-size cases fail | **caught**. A file owned by another user takes root to make, so the test plays another user through the `kittyUID` seam |
| An `a=t` followed by one `a=p` with no cell count never showed: the refresh pass deleted the placement in the flush that sent it | n/a, injected, cuts the call site | `forwardPlace` in `internal/app/kitty_passthrough_forward.go` back to sizing the record with `kp.calculateImageCells(cmd)` instead of `kp.placeCells(cmd, pixelW, pixelH)` (the tree before the fix) | `TestKittyPlaceAfterTransmitShows/b64`, `/shm`, `/daemon-b64` ("the image was placed 1 times, then taken down by `a=d,d=i,i=1,q=2` and never placed again") | **caught** (3 of 3) |
| The same for a PNG: `a=t,f=100` names no `s=` or `v=`, so the `a=p` after it still had no size and was deleted | n/a, injected | `pngSize` in `internal/app/kitty_passthrough_forward.go` returns `0, 0, false` at the top | `TestKittyPlaceAfterTransmitShows/png`, `/pngz`, `/daemon-png` ("the image was placed 1 times, then taken down by `a=d,d=i,i=1,q=2` and never placed again"); `/b64`, `/shm`, `/daemon-b64` pass, which is correct: they state `s=` and `v=` | **caught** (3 of 3) |
| The same, a PNG sent with `o=z` alone | n/a, injected | skip the `zlibbed` branch of `pngSize` | `TestKittyPlaceAfterTransmitShows/pngz` (same message); `/png` and `/daemon-png` pass | **caught** |
| The slowest client set the pace of a graphics pane: one stopped client froze the guest and every other client | n/a, injected | `everyClientBehind` in `internal/session/graphics_backpressure.go` returns true when any client is behind, and `maxGraphicsHold` back to 10 s, which is the design this PR started with | `TestKittyStuckClientDoesNotSlowTheOthers` ("the reading client got 8 frames while the other was stopped, 101 before", "the guest wrote 1 frames while a client was stopped, 180 before") | **caught** |
| A client that fell behind on a graphics pane queued every frame until its stream was cut | n/a, injected, cuts the call site | `commitFrame` in `internal/session/kitty_frames.go` never drops the older frame | `TestKittyStuckClientDoesNotSlowTheOthers` ("the daemon cut 1 client streams", "the stopped client skipped no frames"); unit `TestStuckClientQueueStaysBounded`. main fails the same test with 1 delete and image data printed as text | **caught** |
| A graphics pane whose only client read in bursts was read at full speed, and the client printed image data | n/a, injected | `holdForSlowSubscribers` returns at once; main as well | `TestKittySlowClientHoldsTheGuest` ("the guest wrote 180 frames while its only client read in bursts, 180 while it read", "the daemon never held a pane"); on main also "image data was printed into the pane as text" | **caught** |
| A text pane was held like a graphics pane | n/a, injected | `streamsGraphics` returns true for every pane | `TestTextFloodPaneIsNeverHeld` ("the daemon held a pane that draws no graphics 3 times") | **caught** |
| A client attached, or resumed after a gap, inside a frame printed the rest of that frame's base64 as text | n/a, injected, cuts the call site | drop the `spanAt` block from `subscribeLocked` in `internal/session/session.go`; main as well | `TestKittyAttachDuringAStreamPrintsNoImageData` ("attach 2 of 8 printed image data into the pane as text"); each attach lands at a random point, so one run of 8 attaches passed: 16 attaches now. On main "attach 7 of 16" | **caught** (2 of 3 runs at 8 attaches) |
| The pane was held again on every read after a hold ran out, so a stuck client cost 250 ms per read | n/a, injected | `holdForSlowSubscribers` ignores `holdSpent` | unit `TestPacingHoldIsCappedOncePerEpisode` ("read 0 after the cap ran out was held for 50ms") | **caught in `internal/session`** |
| A read-only viewer held a graphics pane | n/a, injected | `everyClientBehind` counts clients with `noPace` set | unit `TestPacingNeedsEveryPacingClientBehind` ("the hold did not end within 2s") | **caught in `internal/session`** |
| A frame was dropped that a placement after it named, or whose cursor move the text after it used, or that its client had no chance to take | n/a, injected, one hunk each | in `commitFrame`, drop `!old.pinned`; drop the `setsCursor` pin in `route` (since replaced: a frame that moves the cursor is never dropped, see the row on the lost scroll); drop `old.read < p.reads` | `FuzzFrameSkipping` ("the slow client lost frame 0-42 of image 1, which nothing replaced" twice; "the client that kept up got 80 bytes, want 122"); found by the mutator in under 90 s each, and the three inputs are kept under `internal/session/testdata/fuzz/FuzzFrameSkipping/` | **caught in `internal/session`** |
| A header split across two reads ended the scan, so the next chunk of a frame went out as text | n/a, found while writing the scanner | the scan loop stops at the end of the buffer in the header state | `FuzzGfxScanner` seed ("the cut depends on where the reads fall") | **caught in `internal/session`** |
| A frame that moved the cursor was dropped when a cursor move came after it, and the slow client lost the scroll that frame made at the bottom row | n/a, injected, cuts the call site | in `classify`, set `frameKeep` without `frameMoves`, so `route` never pins the frame | `TestKittySkippedFrameKeepsTheScroll` (kept up shows rows from `row05`, stopped from `row03`); unit `TestFrameSkippingSweep` ("327 of 3000 streams failed ... the client that fell behind ends in another state") and `FuzzFrameSkipping` seed 6 | **caught** |
| The same, the pin alone | n/a, injected, cuts the call site | in `route`, open the frame with `pinned: sg.keep && !sg.moves` | unit `TestFrameSkippingSweep` ("1039 of 3000 streams failed"), `FuzzFrameSkipping` seed 6 and `b0811bedc23db0b9` | **caught in `internal/session`** |
| A skipped frame sent as a shared memory object or temp file (`t=s`, `t=t`) left its object on disk, because no terminal read it | n/a, injected, cuts the call site | in `classify`, `frameKeep` ignores the medium | unit `TestSkippedFrameNeverLeaksSharedMemory/t=s` ("54 frames skipped, 54 of 60 shared memory objects left in /dev/shm"); its `t=d` half skips frames with the fix in | **caught in `internal/session`** |
| A frame with no image id (mpv's `--vo=kitty`) was never skipped, so a stopped client's stream was cut | n/a, injected, cuts the call site | in `classify`, make the id-less frame case false | `TestKittyStuckClientSkipsIDlessFrames` ("0 skipped, 1 streams cut"); unit `TestIDlessFramesAreSkipped/mpv` and `/mpv_with_f` ("0 frames skipped, 1 streams cut") | **caught** |
| The same, the drop in `commitFrame` | n/a, injected, cuts the call site | in `commitFrame`, drop the `idless(f, old)` test, so an id-less frame never finds the frame it replaces | unit `TestIDlessFramesAreSkipped/mpv` ("0 frames skipped, 1 streams cut") | **caught in `internal/session`** |
| A frame with no id replaced one at another place or of another size, or one with text between | n/a, injected, one hunk each | in `idless`, drop `f.key == older.key`; drop `f.follows` | unit `TestIDlessFramesAreSkipped/two_places`, `/other_sizes` ("39 frames skipped: none of these may replace another"), `/text_between`; `TestFrameSkippingSweep` (11 of 3000 for `follows`) | **caught in `internal/session`** |
| The scanner kept only the last 32 spans, so a catch-up from the ring's start began inside a frame | n/a, injected | keep 32 spans in `closeSpan` | unit `TestCatchUpNeverStartsInsideAFrame` ("22 of 50 catch-ups printed image data as text") | **caught in `internal/session`** |
| The same, no bound on the spans kept | n/a, injected, cuts the call site | drop the `pruneSpans` call from `broadcast` | unit `TestCatchUpNeverStartsInsideAFrame` ("the scanner keeps 400 spans for a 64 KiB ring of 1.5 KB frames") | **caught in `internal/session`** |
| A client that attached inside a graphics command that is not a frame printed the rest of it as text | n/a, injected, cuts the call site | drop the `skipSpan` line from `subscribeLocked` | unit `TestCatchUpSkipsTheRestOfACommand` (f=32 case, "133 of 134 catch-ups printed image data as text"). `TestKittyAttachDuringIDlessStreamPrintsNoImageData` passed with this and the id-less frames both removed, 16 attaches | **caught in `internal/session`**, **not caught** end to end |
| One kitty query made a pane a graphics pane for 5 s, so a text flood after it was held | n/a, injected, cuts the call site | in `scan`, set `saw` for a query too | unit `TestKittyQueryIsNotGraphicsOutput` ("a text flood was held 1 times"); its frame half is held with the fix in | **caught in `internal/session`** |
| A frame of image 1 on the main screen was replaced by a frame of image 1 on the alternate screen, though a terminal keeps one image store per screen, so a client that fell behind lost the main screen's image | n/a, injected, cuts the call site | in `route`, open the frame without `alt: sg.alt` | unit `TestSkippedFrameKeepsTheOtherScreensImage` (`?1049h`, `?47h` and `?1;1047h` cases: "1 frames skipped; the client that fell behind ends in another state"); its one-screen half skips 1 frame with the fix in. `TestFrameSkippingSweep` (57 of 3000 streams), `FuzzFrameSkipping` seed 9 ("lost frame 80-163 of image 3, which nothing replaced") | **caught in `internal/session`** |
| The same, the scanner never sees a screen switch | n/a, injected, cuts the call site | drop the `trackScreen` call after the `ESC _ G` test in `scan` | the same unit test; `TestFrameSkippingSweep` (38 of 3000 streams), `FuzzFrameSkipping` seed 9 ("the client that fell behind ends in another state") | **caught in `internal/session`** |
| A graphics pane whose only client read in bursts was read at full speed (the test now reads the time held, not only the guest's rate, which flaked at 60.3% against 60% on two cores) | n/a, injected | `holdForSlowSubscribers` returns at once | `TestKittySlowClientHoldsTheGuest` ("held the guest for 0s of 3s"; "wrote 180 frames ... 180 while it read"). With the fix: held 2.24 to 2.34 s of 3 s in 5 runs on two cores | **caught** |
| The rest of the spotlight dim change: the blend cache, a coloured blank, the beam's own middle, a cell that names no colour, a wide glyph's placeholder, the registry's published default, the cursor stand-in, and the stand-in latched on the first frame | n/a, injected, one hunk each | see `internal/app/spotlight_test.go`, and `internal/config/spotlight_test.go`, which has since been removed; seven mutations | unit tests only, each fails as an assertion | **caught** (11 of 11 unit mutations, 4 of 4 here) |
| Untheme the beam ignored the dim setting: the whole screen went to SGR 2, so 10 and 95 drew the same frame | n/a, injected | `dimmable` in `internal/app/spotlight.go` back to `return s.groundBg != nil`, which is the blanket faint the pass used to take | `TestSpotlightDimsAThemelessScreenByTheSetting` ("dim 10 left the far border at 605 and dim 95 at 605; the setting draws the same frame at both ends"); two unit tests that also failed have since been removed | **caught** |
| Nothing wrote SGR 2 for a colour the pass cannot resolve, so a themeless screen kept most of itself at full brightness | n/a, injected, cuts the call site | drop the `cell.Style.Attrs \|= uv.AttrFaint` write from `dimCell` | `TestSpotlightGoesFaintOnWhatItCannotResolve` ("with no theme a cell carrying no colour was left at full brightness outside the beam"); the unit test that also failed has since been removed | **caught**, and it is the positive control on the row above |
| A config saved by an editor never reached the running client: the watch followed the inode vim renames away | n/a, injected | `w.Add(filepath.Dir(cw.path))` in `internal/config/watcher.go` back to `w.Add(cw.path)` | `TestConfigEditedOnDiskReachesTheScreen` ("a second config save never reached the screen, so the watch died on the first one"), `TestBrokenConfigOnDiskKeepsWhatIsRunning`; unit `TestWatcherSeesAnEditorsSave` (since removed) | **caught** |
| A config file that does not parse was ignored in silence, so every later save looked like it had worked | n/a, injected, cuts the call site | drop the `p.Send(app.ConfigReloadFailedMsg{...})` branch from the watcher callback in `cmd/tuios/run.go` | `TestBrokenConfigOnDiskKeepsWhatIsRunning` ("a config file that does not parse said nothing on screen") | **caught** |
| The rest of the themeless dim and the config watch: the xterm-default guess for the host's sixteen, a theme no longer settling them, the debounce, a broken file's content recorded as in force, the unchanged-content guard, a broken file delivered as the defaults, tuios's own save coming back as an edit (both halves), the beam not reseeded from the file, the failure notification, the four-of-seven section fill, and the reloaded config never reaching the model | n/a, injected, one hunk each | see `internal/app/spotlight_test.go` and `internal/config/watcher_test.go`, plus `internal/app/config_live_test.go`, which has since been removed (e2e `config_watch` and `spotlight` cover what it held); twelve mutations | unit tests only, each fails as an assertion | **caught** (15 of 15 unit mutations, 4 of 4 here) |
| The shake gesture never reached Update: the motion filter is a whitelist and it had no clause for the gesture | n/a, injected, cuts the wiring | drop the `os.ShakeGestureOn()` clause from `FilterMouseMotion` in `internal/app/program_options.go` | `TestShakingTheMouseWorksWithLinkHoverOff` ("shaking the pointer with links off did not turn the beam on; the motion filter is dropping every event the gesture needs") | **caught** |
| Nothing fed the pointer to the shake detector | n/a, injected, cuts the call site | drop `m.noteShakeMotion(mm, time.Now())` from the mouse-motion case in `internal/app/update.go` | `TestShakingTheMouseTogglesTheSpotlight`; unit `TestShakingTheRealPointerTogglesTheBeam` | **caught** |
| The gesture fired for a client that never asked for it | n/a, injected | drop the `!m.ShakeGestureOn()` guard from `noteShakeMotion` in `internal/app/shake.go` | `TestTheShakeGestureIsOffByDefault` ("the shake fired with spotlight.shake unset, which is how it ships"); unit `TestTheGestureIsOffUntilItIsAskedFor` (since removed), `TestTheGestureIsOffUnlessItIsTurnedOn` (since removed) | **caught** |
| window_size latest: input in a client never reached the size calculation | n/a, injected, cuts the wiring | drop the `MsgClientActivity` case from `handleMessage` in `internal/session/daemon.go` | `TestWindowSizeLatestFollowsInput` ("after input in the small client: the session is 200x50 under latest, want 80x24") | **caught** |
| window_size latest: the client never reported input | n/a, injected, cuts the call site | drop `m.DaemonClient.ReportActivity(m.msgClock)` from `Update` in `internal/app/update.go` | `TestWindowSizeLatestFollowsInput` (same message) | **caught** |
| window_size latest: the daemon recorded input and never recalculated | n/a, injected | `handleClientActivity` in `internal/session/window_size.go` returns before `recalculateAndBroadcastSize` | `TestWindowSizeLatestFollowsInput` (same message) | **caught** |
| window_size: the configured policy ignored, latest always | n/a, injected | `sessionWindowSize` returns `latest` first | `TestWindowSizeSmallestIgnoresInput` ("with both attached: the session is 200x50 under latest, want 80x24") | **caught** |
| window_size latest: no hold, so two clients typing in turn flap the layout | n/a, injected | `latestHold` set to `0` | `TestWindowSizeLatestDebounce` ("while both clients typed the session was 200x50 80x24, want only 200x50") | **caught**. Its last step is the positive half: the session goes to the last typer once the hold runs out, with no input |
| window_size: a client that cannot draw a larger session gets one | n/a, injected | drop the `!c.capable` check from `effectiveWindowSize` | `TestWindowSizeLegacyClient` ("with an old client attached: the session is 200x50 under latest, want 80x24"). Also run with `TUIOS_E2E_OLD_CLIENT` naming a build of `origin/main`, where it passes on the fixed tree | **caught**. `TestWindowSizeLatestFollowsInput` is its positive half in the same fixture |
| window_size: the daemon never learns a client's capability | n/a, injected, cuts the wiring | drop `cs.windowSizeCap = payload.WindowSize` from `handleHello`, or `hello.WindowSize = ...` from the client handshake | `TestWindowSizeLatestFollowsInput` ("with the big client latest: the session is 80x24 under smallest, want 200x50") | **caught** (both cuts) |
| A smaller client's view does not follow the cursor | n/a, injected | `followOffset` in `internal/app/pane_view.go` returns 0 | `TestWindowSizeViewFollowsCursorAndClicks` ("the view never followed the cursor to the right") | **caught** |
| A smaller client composes the panes unshifted | n/a, injected, cuts the call site | `GetCanvas` calls `composeLayers` instead of `composeLayersIn` | `TestWindowSizeViewFollowsCursorAndClicks` (same message) | **caught** |
| A click in a smaller client's view lands on the pane at the same layout position | n/a, injected, cuts the call site | drop `msg = o.MapPointer(msg)` from `HandleInput` in `internal/input/handler.go`, and also with `MapPointer` returning its input | `TestWindowSizeViewFollowsCursorAndClicks` ("a click on the right pane in the small client did not focus it") | **caught** (both) |
| The rail of a smaller client laid out at the session's height | n/a, injected | `ViewUsableHeight` returns the layout height | `TestWindowSizeRailStaysWhole` ("the rail or the dock is not at the small client's own edges") | **caught**. A first version of the test checked only the dock rule and passed on this mutant, because the dock is drawn over the rail. It now checks the rail's footer row too |
| `tmux set -g window-size` ignored by the shim | n/a, injected, cuts the call site | make the `set-option window-size` branch of `runOne` in `internal/tmuxcompat/shim.go` unreachable | `TestWindowSizeTmuxShim` (times out waiting for `SHIM_largest_DONE`) | **caught** |
| Capture mode in a smaller client's view captured the pane at the clicked layout position, and drew its outline off screen | n/a, injected | `renderCaptureMode` records the panes at their layout rectangles instead of `paneOnScreen` | `TestWindowSizeCaptureInView` ("a click on the right pane captured another pane", the file holds the left pane's L line) | **caught** |
| Copy mode in a smaller client's view snapped to the pane's top left corner | n/a, injected | `paneCursor` returns no position while copy mode is shown | `TestWindowSizeCopyModeInView` ("entering copy mode moved the view off the copy cursor") | **caught**. Its next step (0 brings the view home) is the positive half |
| The copy-mode search prompt drawn at the pane's layout position, below a 24-row screen | n/a, injected | the search layer placed at the layout position, without `paneChromeAt` | `TestWindowSizeCopyModeInView` ("the copy-mode search prompt is not on the view's last row") | **caught** |
| The multi copy "Save to" prompt drawn at the pane's layout position | n/a, injected | `multiCopySaveLayer` places the prompt without `paneChromeAt` | `TestWindowSizeMultiCopySaveInView` ("the Save to prompt is not on the view's last row") | **caught** |
| A program that hides its cursor showed only the top of its pane | n/a, injected | `paneCursor` returns no position when the cursor is hidden | `TestWindowSizeHiddenCursorInView` ("the view did not follow the hidden cursor to the input row") | **caught** |
| A `MsgClientActivity` frame of any size up to 16 MiB was read | n/a, injected | drop the `MsgClientActivity` case from `daemonFrameLimit` | `TestWindowSizeActivityFrameLimit` ("the daemon answered nothing to a 65536-byte activity frame") | **caught** |
| The attach notice named the configured policy, not the one in force | n/a, injected | `attachWindowSize` back to reading `get-option` | `TestWindowSizeAttachNoticeNamesThePolicyInForce` (the notice says `window_size latest` while an old client holds the session at smallest) | **caught**. A first mutant changed only the verb and left the struct reading `window_size`, so it read nothing, fell back to the smallest wording and passed. The control now restores the whole function |
| A read-only viewer sized the session under largest | n/a, injected | `sizingClients` returns every client | `TestWindowSizeViewersDoNotSize/largest` ("the session is 200x50 under largest, want 80x24") | **caught** |
| A read-only viewer stopped counting under smallest | n/a, injected | `sizingClients` leaves viewers out under smallest too | `TestWindowSizeViewersDoNotSize/smallest` ("the session is 200x50 under smallest, want 80x24") | **caught**, and it is the positive half of the row above |
| The view mark drawn over the top of the panes | n/a, injected | `renderViewMark` puts the mark on the first row of the view's pane area | `TestWindowSizeRailStaysWhole` ("the rail or the dock is not at the small client's own edges") | **caught** |
| A client sending activity faster than it should recalculated on every report | n/a | the 50 ms gate in `handleClientActivity` | none: a recalculation that changes nothing has no effect a test can see | **not caught**. The frame limit above bounds what such a client can send |
| One turn of the pointer was a shake, so crossing the screen and coming back toggled the beam | n/a, injected | drop the `m.shake.count < shakeReversalsToFire` guard from `noteShakeMotion` | `TestSweepingTheMouseLeavesTheSpotlightAlone` ("a sweep across the screen and back toggled the beam"); unit `TestASweepAcrossTheScreenAndBackIsNotAShake` (since removed) | **caught** |
| The gesture changed the screen and said nothing | n/a, injected, cuts the call site | drop the `ShowNotification` from `noteShakeMotion` | `TestShakingTheMouseTogglesTheSpotlight` (the notification is the only thing it asserts on) | **caught** |
| The rest of the shake gesture: motion with a button held, the amplitude threshold, the gap between turns, the suspension after firing, the rest that re-arms it, the screen saver, the ToggleSpotlight funnel, the settings row, the option spec, and an allocation per motion event | n/a, injected, one hunk each | see `internal/app/shake_test.go` and `internal/input/shake_gesture_test.go`; ten mutations | unit tests only, each fails as an assertion | **caught** (14 of 14 unit mutations, 5 of 5 here) |
| A session on a host dropped every eleven seconds, with no network involved: the relay kept the ten second write deadline `writeVerbResponse` armed before it took the connection over | n/a, injected, cuts the fix | drop `conn.SetWriteDeadline(time.Time{})` from `relayHostConnection` in `internal/session/verb_host_connection.go` | `TestAHostSessionStaysAttached` ("the session on build dropped on its own, with no network between the two machines. 8.01s after the attach"); unit `TestARelayIsNotEndedByTheDeadlineFromItsOwnReply` (since removed) | **caught** |
| A dropped link closed every pane and put the person back on their own machine, instead of being dialed again | n/a, injected, cuts the call site | make `beginHostReconnect` in `internal/app/host_reconnect.go` return nil at the top | `TestALinkThatDropsIsDialedAgainAndThePaneComesBack` ("the client did not say it was dialing the link again"), `TestAScrolledPaneIsStillScrolledAfterAReconnect`; unit `TestALostLinkKeepsThePaneOnScreen` | **caught** |
| Coming back after a reconnect rebuilt every pane, which loses the history and the place a person was reading | n/a, injected | make `samePaneSet` in `internal/app/host_reconnect.go` always report false | unit `TestTheSamePanesAreKeptAcrossAReconnect`; the e2e scroll row above covers the effect | **caught** |
| Two control calls on one link read each other's replies | n/a, injected, cuts the fix | drop the `lockOrDone(ctx, &c.one)` pair from `caller.call` in `internal/federation/call.go` | `TestTwoListingsAtOnceDoNotReadEachOthersAnswers` **panics** rather than asserting: two goroutines on one `bufio.Reader` crash the daemon | **not caught as an assertion**, and the panic is the shipped behaviour at `3aebb8da` |
| The rail never learns of a host added from the command line: a client whose daemon has no hosts stops polling for them, which is the default install | n/a, injected, cuts the call site | drop `d.broadcastHostsChanged(change)` from `ApplyHosts` in `internal/session/daemon_hosts.go` | `TestRailShowsAHostAddedWhileAttached` ("the rail did not show the host added while the client was attached") | **caught** |
| The client takes the daemon's push and never re-arms its own poll gate | n/a, injected, cuts the wiring | the `HostsChangedMsg` case in `internal/app/update.go` returns `m, nil` | `TestRailShowsAHostAddedWhileAttached`; unit `TestHostsChangedPushStartsThePollAgain` ("the hosts-changed push did not arm the poll") (since removed) | **caught** |
| Clicking a session on another machine hoists that machine to the top of the section, so the row that was clicked moves out from under the pointer and every other row moves with it | n/a, injected, restores the layout this change replaced | `sidebarMachineRows` in `internal/app/sidebar_hosts.go` pins `attached` first instead of `federation.LocalHostName` | `TestRailAttachesARemoteSessionInThisClient` ("the row \"▾ local\" moved from screen row 1 to 3 on the switch"); unit `TestMachineOrderHoldsAcrossASwitch` | **caught** |
| A machine's group does not fold | n/a, injected, cuts the wiring | invert the empty-name guard in `SidebarToggleHostCollapsed` so every call returns early | `TestRailAttachesARemoteSessionInThisClient` ("the click on the header did not fold the group"); the unit test that also failed has since been removed | **caught** |
| A folded machine header does not say how many sessions it is holding, so the fold reads as an empty machine | n/a, injected | gate the `collapsed && node.WindowCount > 0` case of `sidebarHostRow` behind `false` | `TestRailAttachesARemoteSessionInThisClient` ("the click on the header did not fold the group": the wait reads the count) | **caught** |
| The machine header wears "@" again, which is the mark the report asked to be rid of | n/a, injected | `GetRailFoldOpenGlyph` in `internal/config/glyphs.go` returns "@" in both glyph modes | unit `TestSidebarDrawsHostGroups` ("the rail still marks a machine with @") (since removed) | **caught in `internal/app`** |
| Folding the attached machine hides the row wearing the focus mark, so "where am I" leaves the rail | n/a, injected | `sidebarHostRow` passes `false` to `sidebarGutter` instead of `here && collapsed` | an `internal/app` unit test failed ("the folded header of the attached machine does not wear the focus mark"); it was since removed, and not rerun against anything that replaced it | **caught in `internal/app` at the time; that test was removed** |
| The machine order, the folds and the per-machine session orders are never written to the sidebar state file | n/a, injected | `saveSidebarState` in `internal/app/sidebar_state.go` writes `nil` for `HostsOrder`, `HostsCollapsed` and `HostSessionOrder` | unit `TestSidebarStatePersistsTheMachineLayout` ("the state file has no \"hosts_order\"") | **caught in `internal/app`** |
| One drag order is shared across every machine, so a drag while attached on build writes build's session names over this machine's order | n/a, injected | `sidebarSessionOrderFor` in `internal/app/sidebar_hosts.go` returns `m.SidebarOrder` for every host | unit `TestRemoteSessionOrderIsPerMachine` ("build's rows ignore build's order") | **caught in `internal/app`** |
| A session row on a machine that answers is muted like one on a machine that does not, so the ink says less than the row does: a click on it attaches the session | n/a, injected, restores the ink this change replaced | `sidebarRemoteSessionRow` in `internal/app/sidebar_hosts.go` back to a flat `pal.FgMute` | unit `TestRailInksARemoteRowByItsLink` ("the row of a session on a machine that answers is not drawn in the resting ink") (since removed) | **caught in `internal/app`** |
| A retired poll timer still fires, so the snapshot's re-arm and the tick's own re-arm both stand and the number of live host polls doubles every period | n/a, injected | gate the generation check in the `FederationRefreshTickMsg` case of `internal/app/update.go` behind `false` | unit `TestAStaleFederationTickIsDropped` ("a tick from a retired generation still polled") | **caught in `internal/app`** |
| A rail too narrow for a name and a number keeps the number on a machine's rows and drops it on this machine's, so the same row type answers the width two ways | n/a, injected | drop `variant == sidebarVariantFull` from the count in `sidebarRemoteSessionRow` in `internal/app/sidebar_hosts.go` | an `internal/app` unit test failed ("a row on another machine keeps its count on a rail too narrow for one"), whose positive half was the same row carrying the count at the full variant; it was since removed, and not rerun against anything that replaced it | **caught in `internal/app` at the time; that test was removed** |
| The width never reaches the row: `drawHostRow` takes the variant and hands the row a constant | n/a, injected, cuts the wiring | `drawHostRow` passes `sidebarVariantFull` to `sidebarRemoteSessionRow` instead of `variant` | the same removed unit test, which drove `drawHostRow` rather than the row function for this reason | **caught in `internal/app` at the time; that test was removed** |
| A drag on a machine header draws its draft order and throws it away on release, so the rail snaps back | n/a, injected, cuts the wiring | drop `m.SidebarHostOrder = d.Order` from the host branch of `SidebarRelease` in `internal/app/sidebar_mouse.go` | `TestDraggingAMachineHeaderReordersTheRail` ("the drag did not put offline above build") | **caught** |
| The reconnect tests read "@ local" to mean "this client is on build". The machine groups removed that row, so the three assertions were re-spelled as the rail's focus mark | n/a, see verdict | the two tests are each other's halves in one helper, which is rule 1 above: `TestALinkThatDropsIsDialedAgainAndThePaneComesBack` passes only while `home` is **not** marked current, and `TestAHostThatNeverComesBackGivesUpAndSaysWhy` passes only when it **is** | `TestAHostSessionStaysAttached` also fails on a mark that never moves, which `order-hoists-attached` and `fold-is-a-noop` above both demonstrate | **not weakened** |
| The review cursor stepped onto each wrapped row of a note, and only the row under it was drawn selected | n/a, injected, cuts the wiring | `ReviewMove` in `internal/app/review_overlay.go` sets `r.cursor = min(max(r.cursor+delta, 0), len(rows)-1)` instead of calling `reviewStep` | `TestReviewWrappedNoteIsOneStop` ("j past the note: mark on row 5, want row 9") | **caught** |
| `attach --host NAME --ssh` with no `--command` sends a bare `tuios`, so a host whose tuios is only in `~/.local/bin` says "not found" | n/a, injected, cuts the call site | `OpenArgs` in `internal/federation/open.go` sends `h.Command` (or `tuios` when it is empty) with the arguments instead of `h.remoteCommand(false, ...)`, so the probe never runs | `TestAttachOnAHostWithSSHFindsTuiosOutsideThePath` ("the far tuios in ~/.local/bin never drew the session over --ssh: child exited (code 1)"); `TestAttachOnAHostWithSSHRunsTheFarTuios` passes, correctly, since its host has a configured command | **caught** (macOS) |
| `attach --ssh` is ignored and the session is attached over the link, so the far tuios never runs | n/a, injected, cuts the wiring | `runAttachOnHost(..., attachSSH)` in `cmd/tuios/main.go` passes `false` | `TestAttachOnAHostWithSSHRunsTheFarTuios` ("no nested client for nested-target"), `TestAttachOnAHostWithSSHFindsTuiosOutsideThePath` ("--ssh did not run the tuios found at .../far-home/.local/bin/tuios") | **caught** (macOS), and only since the process list is read with `ps`, see below |
| A link attach runs ssh and a nested far client instead | n/a, injected, cuts the wiring | the same call passes `true` | `TestAttachOnAHostIsDrawnByThisClient` ("a nested client is running for far-shell: .../tuios attach far-shell"). With the old `pgrep -af` helper on macOS, `noNestedClient` stayed silent and the test failed 10 seconds later on an unrelated wait for the rail | **caught** (macOS), see below |
| On a one-line agent row the harness prefix was kept while two cells of the name were left, so a narrow rail read "claude/depl…" | whole change | build the tree before it (`a44708fe`) and point `TUIOS_E2E_BIN` at it | `TestNarrowRailKeepsTheAgentNameBeforeItsHarness` ("the rail cut the agent's name \"deploy\" to make room for its harness; agent rows: [\"│▎● claude/depl…\" ...]") | **caught** |
| The same, with only the call site cut | n/a, injected, cuts the call site | the `sidebarAgentPrefixRun` call in `sidebarAgentRow` (`internal/app/render_sidebar.go`) passes `min(nameW, 2)` instead of `nameW`, which is the old two-cell rule | `TestNarrowRailKeepsTheAgentNameBeforeItsHarness`, the same assertion. The unit test calls the function directly and does not see this cut | **caught** |
| The rail draws no prefix at all, which would pass the name half for the wrong reason | n/a, injected | the same call passes `nil` for the tokens | `TestNarrowRailKeepsTheAgentNameBeforeItsHarness` ("the rail never drew the short agent with its harness"), which is its positive half: `db` still has room for `claude/` | **caught** |
| A 16-column rail kept the word beside a machine's name and cut the name, so a header read "wo… offline" (issue 181) | whole change | build the tree before it (`07a467ca`) and point `TUIOS_E2E_BIN` at it | `TestTheNarrowRailNamesAMachineWhole/narrow` ("the rail cut the machine's name at width 16: \" ▾ wo…  offline│\""). The `wide` half passes on both builds, which is its job | **caught** |
| An agent row dropped its last token first, so a long name took the cells of the state after it (issue 181) | whole change | the same build | `TestNarrowRailKeepsWhatAnAgentIsDoing/state` ("the long name took the cells of the state beside it: \"│▎● deploy-the-api-gate…\"") | **caught** |
| The harness prefix is budgeted like any other token, so it is kept beside a long name cut to its eight-cell keep | n/a, injected | `sidebarAgentBudget` in `internal/app/sidebar_agent_tokens.go` builds the prefix tokens with `Whole: false` | `TestNarrowRailKeepsWhatAnAgentIsDoing/shipped` ("the row kept a prefix beside a name it had cut: \"│▎● claude/deploy-the-a…\""). `TestNarrowRailKeepsTheAgentNameBeforeItsHarness` passes on this build: "deploy" is shorter than the keep, so the two rules agree on it | **caught** |
| `-w` took a unique id prefix before an exact name, so a pane named with hex digits ("db") was missed whenever another pane's uuid started with them: set-agent-state reported success and set the other pane. This was the "lost agent state" flake in `TestNarrowRailKeepsTheAgentNameBeforeItsHarness`, about 2 in 100 | n/a, the build before the fix | `findWindowStateIndex` in `internal/session/session_ops.go` as it was, trying the id prefix before the names | `TestAWindowNameWinsOverAnIdPrefix`, 3 of 3 runs ("the pane named \"d07e9a9b\" is at \"none\", want working"; "the pane whose id starts with ... took the state meant for the pane of that name"). The same build failed `TestNarrowRailKeepsTheAgentNameBeforeItsHarness` 5 times in 300 runs, run beside a second copy of the loop; with the fix it passed 300 of 300 under the same load. A shell loop of the rail test's CLI steps, eight at a time, lost the state on `db` 8 times in 200 before the fix, every time with another pane's id starting with `db`, and 0 times in 320 after | **caught** |
| A client push carrying agent fields kept them over the daemon's, so a stale snapshot could put an old agent state back; the foreground command, shell pid, directory and machine were taken from the push the same way whenever it named one | n/a, the build before the fix | `retainDaemonExclusive` in `internal/session/state_merge.go` as it was. No E2E test reaches this: the TUI never sends these fields, so only a raw socket client could | the unit cases in `TestAClientSyncKeepsTheDaemonsWindowFields` ("a stale agent state in the push", "a stale foreground command, pid and directory in the push") and `TestTheMachineIsCarriedByWindow` ("named differently") all fail | **caught** (unit) |
| Teardown: a process that is still running passes the teardown check | n/a, injected into the fixture | the check is tuitest's own again, from 8db5fa3 on (pinned at 8912ebb), which counts a process in X as exited: every terminal starts through `tuitest.StartT`, and the `closeTerm` workaround that re-read its survivor list is gone. Rerun against `StartT` alone: a throwaway test starts `/bin/sh` with a child that calls `setsid` and takes root as its real uid through a setuid bit, so its owner cannot signal it. Linux container (golang:1.26), test run as an unprivileged user | "tuitest: ptyproc: 1 process(es) survived teardown: [2552]" on 8db5fa3 and "[2663]" on 8912ebb, and the test fails. Its positive half, the same shell with an ordinary `sleep` child, passes. The earlier run of this control against `closeTerm` failed `TestFocusAltArrowsPixelHostAddNoLine` the same way ("survived teardown: [1586465]"), and its positive half, 2000 teardowns under fork load with a process caught in X twice and passed, is what 8db5fa3 moved into tuitest | **caught** |
| The rail learned about another session's agent from the 3 second listing poll while the dock said it at once | n/a, injected | `foreignAgentRefreshCmd` in `internal/app/inbox.go` returns nil | `TestRailMarksAForeignAgentWhenTheDockDoes` ("the rail did not mark e2e-ask-a within 1.2s of its agent asking"), 3 of 3 runs | **caught** |
| A straight box-drawing line in a capture was two arms meeting mid-cell, so every row of a border had a notch | n/a, the build before the fix | `drawArms` in `internal/shot/boxdraw.go` without the one-rect case for two equal arms on one axis | `TestScreenshotDrawsAStraightBorderWithoutNotches` ("the stroke at x=8 loses ink at y=16: 417 against 556") | **caught** |
| On a light theme the rail and the dock wrote the dark ramp's near-white inks on the theme's near-white ground | n/a, injected | `GroundUI` in `internal/theme/ui.go` returns `UI()` unchanged | `TestLightThemeRailAndDockAreReadable` (session names and the dock notice at 1.09:1 against a 4.5:1 floor) | **caught** |
| An Inbox that opened empty and then filled kept the empty state's top, so the full list sat on the bottom edge | n/a, injected | `overlayAnchorY` in `internal/app/overlay_hit.go` returns the held top whenever the screen height is unchanged | `TestInboxThatFillsAfterOpeningIsCentred` ("13 rows above it and 1 below") | **caught** |
| The review footer kept a margin on its left only, so a key strip that fitted to the cell ran into the frame | n/a, injected | `reviewHints` in `internal/app/render_review.go` fits against `width-1` | `TestReviewFooterKeepsItsRightMargin` (footer ends "esc close│") | **caught** |
| The review's "too narrow for two columns" line stayed over the unified view it had recommended | n/a, injected | the `statusID` reset in `ReviewToggleSplit` disabled | `TestReviewNarrowSplitNoticeGoesWithTheSplit` (still on screen 2s after the second s) | **caught** |
| A panel wider than the panes' columns covered all of the rail but a column or two, leaving fragments of its rows down the panel's edge | n/a, injected | `panelCenterX` in `internal/app/overlay_hit.go` centres on the screen alone | `TestAPanelWiderThanThePanesCoversTheWholeRail` (row 2 ends in the rail's ground) | **caught** |
| The which-key overlay took the screen's bottom-right corner and sat over the rail with two of its columns showing | n/a, injected | the which-key corners taken from the whole screen in `internal/app/render_overlays.go` | `TestWhichKeySitsBesideTheRail` (row 2 of the rail changed under the overlay) | **caught** |
| The which-key list was cut to the screen's rows and ran up over a dock at the top | n/a, injected | the list measured from the top of the screen in `internal/app/render_overlays.go` | `TestWhichKeySitsBesideTheRail` ("the which-key title is on row 1, over the dock") | **caught** |
| A panel taller than the rows under a dock at the top was fitted to the whole screen and ran up over the dock | n/a, injected | `panelRoomHeight` returns the screen's rows and `overlayOrigin` places from row 0 | `TestATallPanelStaysUnderTheDock` (the settings page covered the dock's pills) | **caught** |
| A session made by a command with no terminal (a script, CI, an agent) gave every pane TERM=dumb, so `clear` did nothing and full-screen programs ran without cursor movement | n/a, the build before the fix | `sendHello` in `internal/session/client.go` without the case that names no TERM when detection answers dumb and stdout is not a terminal | `TestSessionMadeWithoutATerminalGivesItsPanesARealTerm` ("gave its pane \"TERM=dumb COLORTERM=truecolor\"") on macOS; in an ubuntu:24.04 container with TERM=dumb, as on a GitHub runner, `TestScreenshotDrawsAStraightBorderWithoutNotches` also fails ("the stroke at x=8 runs from 5 to 9": the typed command stayed on row 0), which is how CI found it | **caught** |
| Every box-drawing rule and border in a capture had a faint brighter nub beside it at each cell boundary, because each cell overdrew half a pixel into its neighbours and the overlap drew the stroke's anti-aliased edge twice | n/a, the build before the fix | `internal/shot/boxdraw.go`, `png.go` and `svg.go` as of b0ab22a1, with `rect` bleeding past every cell edge it touches | `TestScreenshotDrawsAHorizontalRuleWithoutNubs` ("row y=15 of the rule is not one colour: 439 at x=16 against 378 at its start (x=0)") | **caught** |
| The chrome was drawn in truecolor and stepped down a colour at a time: panels and the selected row navy (17) at 256 colours, and at 16 a blue (4) ground and panels with no edge | whole change | build `origin/main` (c2a16426) and point `TUIOS_E2E_BIN` at it | `TestChromeAtEveryColourDepth` dark-16, light-16 ("ground index 4"), dark-256, light-256 ("ground index 17"); the two truecolor runs pass on both builds, which is their positive half | **caught** |
| The same, with the depth cut at its wiring | n/a, injected | `theme.SetColorProfile` no longer calls `overlay.SetDepth` | `TestChromeAtEveryColourDepth` 16 and 256 runs, dark and light ("ground index 17" / "ground index 4"); truecolor passes | **caught** |
| At 16 colours the command palette's selected row has no ground and nothing in its place | n/a, injected | `renderCommandPalette` appends `paletteRow(...)` without `pal.Row` | `TestChromeAtEveryColourDepth` dark-16 and light-16 ("selected row cell (16,16) is not reverse video"); 256 and truecolor pass | **caught** |
| At 16 colours a workspace pill's fill is the terminal's own ground, and its caps were drawn in the default ink, so white half circles framed a label on nothing and read as brackets | whole fix | the wave01/final merge before the fix (`workspacePill` without its `overlay.IsNoColor(ground)` branch), pointed to by `TUIOS_E2E_BIN` | `TestChromeAtEveryColourDepth` dark-16 and light-16 ("pill cap ... is drawn in the default ink"); the 256 and truecolor runs pass on both builds, which is their positive half | **caught** |
| At 16 colours a chip on a light slot took the light ink: the PREFIX badge was slot 15 on slot 3, which a light theme paints close to its own ground, because `ContrastText` measured a slot as the dim VGA colour | whole fix | the build before the fix (`ContrastText` without `slotAsXTerm`), pointed to by `TUIOS_E2E_BIN` | `TestOverlayLayouts` dark-120x40-16 and light-120x40-16 ("is slot 15 on slot 3: light ink on a light ground"); the 256 and truecolor runs pass on both builds, which is their positive half | **caught** |
| A list without the keyboard hid its cursor: the review's file list while the diff is focused, and the diff's cursor while the list is | whole change | build `origin/main` (c2a16426) | `TestUnfocusedListKeepsItsCursor` truecolor ("sits on the resting ground") and 16 ("want underlined") | **caught** |
| The same, cut at the file list's ground | n/a, injected | the file list row takes `pal.Surface` unless it is the focused cursor, instead of `pal.Ground(st, ...)` | `TestUnfocusedListKeepsItsCursor/truecolor` ("the unfocused file list's cursor sits on the resting ground") | **caught** |
| The review diff's tints were stepped down a colour at a time: an added and a removed line on the same grey at 256 colours (239 dark, 253 light), and at 16 colours on a painted ground with no mark on the changed words | whole change | build `wave1/colors` (e5baef1b) and point `TUIOS_E2E_BIN` at it | `TestReviewDiffAtEveryColourDepth` dark-256 ("the removed line's ground is ... 239, want palette entry 52"), light-256 ("... 253, want palette entry 224"), dark-16 and light-16 ("the ... line's code has ground ... at 16 colours"); the two truecolor runs pass on both builds, which is their positive half | **caught** |
| A saturated colour stepped down to 256 landed on a grey: `colorprofile` places Catppuccin Latte's red on 241, so a removed line's number was grey | n/a, injected | `overlay.To256` returns `colorprofile.ANSI256.Convert(c)` instead of `nearest256(c)` | `TestReviewDiffAtEveryColourDepth/light-256` ("the removed line's number is drawn in ... Index:241, a grey"); dark-256 passes | **caught** |
| At 16 colours the diff's cursor reversed the whole row, which drops the code's colours | n/a, injected | `reviewDraw.row` finishes line rows with `pal.Mark` over the whole row, as for the other rows | `TestReviewDiffAtEveryColourDepth` dark-16 and light-16 ("the code under the cursor is reversed") | **caught** |
| The palette tagged every row with `[Category]`, the which-key menu was one 36-row column with `...` submenus, and footers wrapped onto a second row taken from the body | whole change | build `wave1/colors` (e5baef1b) and point `TUIOS_E2E_BIN` at it | `TestOverlayLayouts`, all eight runs ("the palette's first row is \"› [Run] Run a program ... alt+space\", want the Run header"); `TestHintFootersShortenInTiers` 50, 44 and 40 columns ("still holds \"ctrl+\"", "the footer wrapped onto a second row") | **caught** |
| The palette lists its commands with no category headers | n/a, injected, cuts the wiring | `paletteGrouped` in `internal/app/command_palette.go` returns false | `TestOverlayLayouts` dark-120x40-truecolor and light-80x24-truecolor ("the palette's first row is \"› Run a program alt+space Run\", want the Run header") | **caught** |
| The which-key menu gets its lines with no sections | n/a, injected, cuts the wiring | `whichKeyMenu` in `internal/app/render_whichkey.go` returns the prefix menu as one untitled group | `TestOverlayLayouts` dark-120x40-truecolor and light-80x24-truecolor ("no two which-key headings share a row: the sections are not in columns") | **caught** |
| An empty list says so in a line at its top left, with no key to press | n/a, injected, cuts the call site | `renderListOverlay` in `internal/app/render_list_overlay.go` appends the message as one line instead of `overlay.Empty.Lines` | `TestOverlayLayouts` dark-120x40-truecolor ("\"No match for 'qqqq'\" is centred on column 30, the body's middle is 48", "no \"create it\" under \"No match\"") and light-80x24-truecolor | **caught** |
| At 16 colours the which-key panel is the ground's colour and has no edge, so it cuts the window border under it off mid-line | n/a, injected, cuts the call site | drop the `overlay.FrameBlock` call from `renderWhichKey` | `TestOverlayLayouts` dark-120x40-16 and light-120x40-16 ("the which-key panel has no frame at 16 colours"); the truecolor and 256 runs pass, which is their positive half | **caught** |
| The screen behind a modal overlay is not dimmed | n/a, injected | `composeLayers` no longer calls `m.applyScrim(canvas)` before the first modal layer | `TestModalDimsTheScreenBehind`, all six runs, dark and light at 16, 256 and truecolor ("not darker", "want faint", "the rail's session name ... was not dimmed") | **caught** |
| An overlay does not fade in | n/a, injected | `composeLayers` no longer calls `m.applyFade` after an overlay's layer | `TestOverlayFadesIn/truecolor-full` ("appeared at its final colour on every one of three opens"); `256-full` and `truecolor-basic`, which must not fade, pass, which is their positive half | **caught** |
| A working agent's rail row does not shimmer | n/a, injected | `composeLayers` never runs `m.applyShimmer` (the rail flag forced false) | `TestWorkingAgentRowShimmers/truecolor-full` and `16-full` ("took 1 looks over 2.5s"); the two `none` runs pass | **caught** |
| The motion clock runs with no working row on screen | n/a, injected | `shimmerActive` drops its `len(m.motion.rail) > 0` term | `TestFullMotionWithoutAgentsStaysIdle` ("motion=84"). The wire stayed at 0 bytes, because the extra frames drew nothing new: the client's own motion frame count is what catches it | **caught** |
| The glyphs ignore the terminal's locale and TERM | n/a, injected | `loadAndApplyConfig` no longer stores `DetectGlyphEnv`'s answer in `config.Global.GlyphEnv` | `TestGlyphsFollowTheTerminal/c-locale`, `lc-all-latin1` ("╭ ... outside ASCII") and `linux-console` ("U+E0B6, a private-use (Nerd Font) glyph"); `c-locale-heavy-chosen` passes, which is its positive half | **caught** |
| `animations_enabled = false` from an old config is not carried into the motion level | n/a, injected | both calls to `migrateAnimationsEnabled` removed (load and `ApplyAppearanceConfig`) | `TestOldAnimationSwitchMigrates/animations_enabled=false` ("the palette faded = true"); the `true` run passes | **caught** |
| Qwen Code, GitHub Copilot CLI and Cursor Agent hooks reported the session id only, so the pane's state never followed their turn | n/a, injected | the `Qwen`, `Copilot` and `CursorAgent` cases taken out of `Translate` in `internal/integration/hook.go` | `TestHookIntegrationsReportTheTurn` (qwen's `SessionStart` left the pane on `none`) | **caught** |
| The Inbox could not answer a Qwen Code approval: the hook had no decision shape for Qwen Code | n/a, injected | the `Qwen` case taken out of `Approval.Answer` in `internal/integration/approval.go` | `TestQwenApprovalFromTheInbox` (the hook printed nothing after `1`) | **caught** |
| A Pi prompt that waits on the person (`ui_prompt_start`) read as working | n/a, injected | the `ui_prompt_start` subscription renamed away in `internal/integration/assets/pi/tuios-agent-state.ts` | `TestPluginsReportBlockingPrompts` (pi stayed `working`) | **caught** |
| An Amp question asked with `ask_user_choice` never reached the Inbox | n/a, injected | the `tool.call` subscription renamed away in `internal/integration/assets/amp/tuios-agent-state.ts` | `TestPluginsReportBlockingPrompts` (amp stayed `working`) | **caught** |
| An opencode turn announced only by `session.status` idle, which replaces the deprecated `session.idle`, never read done | n/a, injected | the `idle` case taken out of `translateOpenCode`'s `session.status` switch | `TestPluginsReportBlockingPrompts` (opencode stayed `working`) | **caught** |
| A Crush pane was not told where tuios accepts herdr's protocol, so Crush reported nothing | n/a, injected | the `HerdrEnv` call taken out of `buildEnvFor` in `internal/session/session.go` | `TestHerdrProtocolReportsACrushPane` (the stand-in printed NO-HERDR) | **caught** |
| `herdr_protocol = "always"` did not reach a shell pane | n/a, injected | the always case taken out of `Manager.HerdrEnv` | `TestHerdrProtocolAlwaysTellsShellPanes` (the shell read `HE=unset`) | **caught** |
| A Codex pane on a host with neither kitty graphics nor sixel was told `TERM_PROGRAM=TUIOS`, so Codex rang the bell instead of sending OSC 9 | n/a, injected | `TermProgramFor` made to answer `TermProgram`'s name for Codex too | `TestCodexPaneNotificationsArrive` (the stand-in saw `TUIOS`) | **caught** |
| goose had no manifest, so its pane was never named and its prompts never read | n/a, injected | `internal/harness/manifests/goose.toml` removed | `TestGooseIsRecognisedWhereItsInstallerPutsIt` (the pane stayed `none`) | **caught** |
| Any binary named goose, pressly's migration tool included, was taken for an agent | n/a, injected | `goose` put back in `defaultAgentBinaries` | `TestGooseIsRecognisedWhereItsInstallerPutsIt` (the Go tool read as an unnamed agent, `working`) | **caught** |
| ctrl+b C did nothing while the Inbox (in terminal mode) or the review was open, so neither could be captured | n/a, the build before the fix and injected | the `routeOverlayScreenshot` call cut from `HandleKeyPress` in `internal/input/handler.go` | `TestScreenshotOverTheInbox`, `TestScreenshotOverTheReview` (both "capture mode never opened") | **caught** |
| A region dragged over the review selected nothing, because the review ate the click ahead of capture mode | n/a, injected | the `ReviewOpen` guard put back ahead of `CaptureActive` for mouse clicks in `internal/input/handler.go` | `TestScreenshotOverTheReview` ("only 1 files landed ... want 2") | **caught** |
| A capture over the review opened its preview underneath the review, where it held the keyboard and could not be seen | n/a, injected | the `overlayKindShot` case cut from `overlayZ` in `internal/app/overlay_hit.go` | `TestScreenshotOverTheReview` ("the preview never appeared over the review") | **caught** |
| The host terminal's focus events were ignored, so nothing could tell whether the person was looking | n/a, the build before the fix and injected | the `tea.FocusMsg` and `tea.BlurMsg` cases in `internal/app/update.go` return without `noteHostFocus` | `TestHostFocusHoldsAlertsForThePaneYouAreLookingAt` (host_focus stays "unknown"; before the fix, host_focus is missing) | **caught** |
| suppress_focused held the alert for the focused pane even with the terminal out of focus | n/a, injected | `HostLooking` cut from the suppress check in `considerAgentAlert` (`internal/app/agent_alert.go`) | `TestHostFocusHoldsAlertsForThePaneYouAreLookingAt` ("notifications [], hook needs_input") | **caught** |
| The daemon's after-agent-state hook was held for the shown pane with every client's terminal out of focus | n/a, injected | the `sessionHostFocus` check cut from `suppressed` in `internal/session/daemon_hooks.go` | `TestHostFocusHoldsAlertsForThePaneYouAreLookingAt` (the notification arrives and the hook file stays empty) | **caught** |
| TUIOS_SOCKET set to a path with no daemon was ignored, and the command created its session in the daemon XDG_RUNTIME_DIR names | n/a, the build before the fix and injected | the `CheckSocketEnv` call cut from `ensureDaemon` (`cmd/tuios/daemon_start.go`); separately, the `checkSocketEnv` call cut from `GetSocketPath` (`internal/session/manager_unix.go`) | `TestSocketEnvNamingNoDaemonIsRefused` (first: the refusal never names TUIOS_SOCKET; second: `ls` and `list-windows` are not refused; before the fix: the session is created) | **caught** |
| Panes were told the terminal is black: with no theme and the pane background off, a pane's OSC 11, OSC 10 and OSC 4 queries got the emulator's defaults, and the rail and the dock were drawn for a dark ground on a light terminal | whole change | build `origin/main` (`c2a16426`) and point `TUIOS_E2E_BIN` at it | `TestHostColorsReachPanesAndChrome` at all three depths ("a pane's OSC 11 query was answered \"rgb:0000/0000/0000\", want the host's \"rgb:fdfd/f6f6/e3e3\""; "the rail's attached session ... 1.04:1, under the 4.5:1 floor"; "the pane frame ... 1.05:1") | **caught** |
| The client never tells the daemon the host's colours | n/a, injected, cuts the call site | `BuildSessionState` in `internal/app/session.go` drops the `paneReportHex()` assignment | `TestHostColorsReachPanesAndChrome/truecolor` (every pane answer is black and white) | **caught** |
| The host colour messages are never handled | n/a, injected, cuts the call site | drop the `m.handleHostColorMsg(msg)` branch from `handleMsg` in `internal/app/update.go` | `TestHostColorsReachPanesAndChrome/truecolor` (the first push never goes, and "tuios did not ask for the background again after the switch") | **caught** |
| What the startup probe learned is not pushed until some input causes a push | n/a, injected | gate the settle message in `hostColorQueries` behind `false` | `TestHostColorsReachPanesAndChrome/truecolor` ("a pane's OSC 11 query was answered \"rgb:0000/0000/0000\"") | **caught** |
| Mode 2031 is never turned on, so a light and dark switch is never reported | n/a, injected, cuts the call site | drop `tea.Raw(hostSchemeReportsOn)` from `hostColorQueries` | `TestHostColorsReachPanesAndChrome/truecolor` ("tuios had not turned on mode 2031 when the host switched") | **caught** |
| No hysteresis: a mid grey after a light background flips the chrome to the dark ramp | n/a, injected | judge every background afresh in `setHostBg` (`case h.bg == nil` to `case true`) | `TestHostColorsReachPanesAndChrome/truecolor` ("on the mid grey #707070 after a light host the pill's fill is ... the dark ramp's") | **caught**; the grey-after-dark step is its positive half |
| The chrome palette ignores the host's ground | n/a, injected | `groundUI` in `internal/app/host_colors.go` never takes the host branch | `TestHostColorsReachPanesAndChrome/truecolor` ("the rail's attached session ... 1.04:1, under the 4.5:1 floor") | **caught** |
| The no-theme border colours are not lifted for the host's ground | n/a, injected | `hostBorderInk` in `internal/theme/theme.go` returns the fixed colour | `TestHostColorsReachPanesAndChrome/truecolor` ("the pane frame at (24,9) is {175 255 255 255} ... 1.05:1, under the 3.0:1 mark floor") | **caught** |
| Rail rows measured session colours against black, so bright cyan was drawn unlifted on a light terminal | n/a, injected | `rowGround` in `internal/app/session_color.go` falls back to `theme.TerminalBg()` again | `TestHostColorsReachPanesAndChrome/truecolor` and `/256` ("the dot of \"e2e-other\" ... 1.16:1, under the 3.0:1 mark floor") | **caught** |
| A terminal that answers no colour query (mosh) | n/a, never broken | same binary | none, and that is correct: `TestHostColorsSilentTerminal` passes on `origin/main` too, because it guards the fallback (a pane gets the emulator's defaults and tuios starts without waiting) | **guard, not a control** |
| Session restore on macOS brought every pane back in the daemon's directory, because the daemon read a shell's directory only from procfs | n/a, injected (the darwin read landed in `1dc1f4b9` with no end-to-end test) | two builds: `ProcessCwd` in `internal/ptyspawn/cwd_darwin.go` answering nothing; and the `pty.ProcessCwd()` call in `Session.ResurrectionState` disabled | `TestRestoredPaneComesBackInItsDirectory` ("the restored pane's shell did not start in .../restored-here"), on macOS with both builds | **caught** |
| A pane resized once per client as it opened sent macOS `/bin/bash` 3.2 a SIGWINCH per resize, and one landing while readline set LINES and COLUMNS through malloc killed the shell | n/a, injected | `winsizeDue` in `internal/session/pty_winsize.go` always due, so every resize is written at once | focused test, not this suite: `go test ./internal/session -run TestResizeBurstAtStartup` killed 3, 1 and 4 of 400 shells (2 and 5 of 1000 with `TUIOS_RESIZE_BURST_PANES=1000`); with the coalescing, 0 of 400 and 0 of 1000 in three runs | **caught** |
| Light theme: every dialog drew on the constant dark ramp | n/a, injected, cuts the call site | make the light branch in `buildUI` (`internal/theme/ui.go`) unreachable | `TestChromeLooksAndFooters/latte-120x40-truecolor` and `latte-120x40-256`: every overlay fails "the panel's ground {58 58 58} is light=false under the latte look", and the Inbox, the sessions list and keybinds fail the glyph floor ("×" at 2.11:1) | **caught** |
| Light theme: the modal dim carried the screen toward black | n/a, injected, cuts the call site | drop `s.setToward(m.scrimToward())` from `applyScrim` in `internal/app/scrim.go` | `TestChromeLooksAndFooters/latte-120x40-truecolor` ("the dim moved the light ground behind the palette from {239 241 245} to {148 149 152}, 2.65:1 apart", "the rail's text did not fade toward the ground") | **caught** |
| A light scrim spelled the ground out, so at 256 colours the pane ground turned the stepped-down background | n/a, injected | make the keep-no-ground case in `buildRun` (`internal/app/spotlight.go`) unreachable | `TestChromeLooksAndFooters/gruvbox_light-120x40-256` ("from {251 241 199} to {255 255 215}, 1.11:1 apart"); `latte-120x40-256` passes, since latte's background steps to a grey 1.03:1 from it | **caught** on gruvbox_light |
| Structure ink stepped down by the terminal at 256 colours | n/a, injected | skip the 256-colour walk at the end of `Structure` (`internal/overlay/contrast.go`) | `TestChromeLooksAndFooters/gruvbox_light-120x40-256` ("keybinds: a glyph measures 1.53:1") | **caught** |
| Footer dropped labels from the end before any hint, so the Inbox read "d   esc" | n/a, injected | put the old label-dropping tier back in front of tier 3 in `fitHints` (`internal/overlay/hints.go`) | `TestChromeLooksAndFooters/dark-120x40-truecolor` and `dark-120x40-16` ("the footer shows "d" with no label", "the footer has no labelled way out") | **caught** |
| The Inbox's optional hints were not marked, so the footer gave up dismiss before answering in the pane | n/a, injected, cuts the call sites | drop the `overlay.Optional` around "answer in pane" in `inboxApprovalHints` and around "deny with reason" in `inboxDetailExtras` | `TestChromeLooksAndFooters/dark-120x40-truecolor` and `dark-80x24-truecolor` ("the footer lost "d dismiss"") | **caught** |
| The palette's match count sat in the list | n/a, injected, cuts the call site | draw the plain search line instead of `searchWithCount` in `renderCommandPalette` | `TestChromeLooksAndFooters/dark-120x40-truecolor` ("the match count is on row -1, want the search line 14") | **caught** |
| Light-theme inks stepped to the grey ramp or a neighbouring hue at 256 colours | n/a, injected, cuts the call site | in `liftOnLight` (`internal/theme/ui.go`) replace `overlay.ReadableEntry256` with `To256(ReadableAt(ink, surface, floor))` | `TestChromeLooksAndFooters/latte-120x40-256` ("inbox: a glyph measures 2.76:1": the warning mark on the cursor row, and the sessions and workspaces lists at 2.75:1) | **caught** |
| The cursor row on a tinted surface at 256 colours landed on the surface's own entry | n/a, injected | `apart256` (`internal/overlay/palette.go`) returns `q` for a cube entry instead of `nearestApart` | `TestChromeLooksAndFooters/gruvbox_light-120x40-256` ("the selected row's ground {Index:187} is the panel's own") | **caught** |
| The quiet ink not held on the cursor rows | n/a, injected | hold `FgMute` on `Surface` only in `checkContrast` | none: once the cursor row's step is tinted, the quiet ink clears 3:1 on it under both light themes here (3.04:1 on latte, 3.15:1 on gruvbox_light at 256), so the loop does not bind. It failed at 2.66:1 while the row stepped to grey 251, before the row above was fixed | **not caught**, guard only |
| Hints mode unreachable | whole feature, cuts the wiring | drop `d.Register("hints", handleOpenHints)` from `registerPrefixHandlers` in `internal/input/prefix_actions.go` | every `TestHint*` in `hints_test.go` except the footer test ("no hint label on ... WaitFor timed out") | **caught** (7 of 7) |
| A URL the pane wrapped copies as its first row | n/a, injected | make the row join in `internal/app/hints.go` return false | `TestHintWrappedURLCopiesWhole` ("the wrapped half of the URL is not lit", and the clipboard got the URL cut at the row end) | **caught** |
| A line that fills its row and ends is joined to the next | n/a, injected | make `rowWraps` in `internal/app/hints.go` read a full last column as a wrap, as it did first | `TestHintFullLineDoesNotJoinTheNext` (the clipboard got the URL with the next line glued on) | **caught** |
| Hints stay open after their pane leaves the screen | n/a, injected, cuts the wiring | make `closeStaleHints` a no-op and drop the `CloseHints` call from `switchToWorkspaceHeld` | `TestHintsCloseWhenTheirPaneExits` ("no new window after 'n'"), `TestHintsCloseOnWorkspaceSwitch` ("the pane did not come back without labels") | **caught** |
| A label over half a wide glyph shifts the rest of the row | n/a, injected, cuts the call site | drop the `fixHintsWideCells` call from `applyHints` in `internal/app/hints_render.go` | `TestHintWideCharacters` ("the label on the wide glyph moved the rest of the row") | **caught** |
| A pane whose agent ended its turn with subagents at work shows nothing of them | whole feature | build the base commit (`4ae6e8af`) and point `TUIOS_E2E_BIN` at it | `TestSubagentCountOnARestingRow` ("the pane's metadata is map[prompt:review the api in parallel], want map[subagents:3 subagents]") | **caught** |
| The count drops off a narrow row behind the harness and the context warning | n/a, injected, cuts the call site | drop the `sidebarNoteKeepSubagents` call from `sidebarAgentNoteRow` in `internal/app/render_sidebar.go` | `TestSubagentCountOnARestingRow` ("waiting for the count beside the context warning": the row read `claude · ctx 91% · Han…`) | **caught** |
| A subagent of another conversation is counted on a pane at rest | n/a, injected | drop the session clause from `activityReportGuard` in `internal/session/agent_state.go` | `TestSubagentCountOnARestingRow` ("another conversation's subagent was counted on the pane": `4 subagents`) | **caught** |
| A resting row with a subagent at work folds into "+N at rest" | n/a, injected, cuts the wiring | drop the subagents check from `sidebarAgentRests` in `internal/app/sidebar_agents.go` | `TestSubagentCountOnARestingRow` ("waiting for the resting row with a subagent out of the fold") | **caught** |
| The rail's files section keeps a file deleted in the pane (#313) | n/a, injected, cuts the call site | drop the `syncFileWatch` call from `HandleFileList` in `internal/app/sidebar_files.go` | `TestRailFilesSectionFollowsTheDisk/standalone` and `/daemon` (both: "gone.txt stayed on the rail after the pane deleted it"). The v0.8.4 binary fails the same way | **caught** (2 of 2 run) |
| Scrolling zoom gap: a zoomed column moved to the next one, and the strip showed empty ground between columns while it slid | whole change | build `origin/main` (`051da3ac`) and point `TUIOS_E2E_BIN` at it | `TestScrollingZoomMoveShowsNoGap`, all 5 runs (zoom 95 and 100, standalone and daemon, and animations off): "left1, frame 1 of 17: the border row ends at column 72 of 120" | **caught** (5 of 5 run) |
| The strip resized a column before it slid, so a column that changed width jumped to it at the old position | n/a, hunk revert | `internal/app/os_scrolling.go` from `origin/main`, the rest of the fix kept | `TestScrollingZoomMoveShowsNoGap`, all 5 runs ("blank cells at columns 69 and 70 of the border row") | **caught** (5 of 5 run) |
| A slide rounded each pane's width on its own, so two columns that share an edge landed a cell apart | n/a, injected | in `Animation.UpdateAt` (`internal/ui/animation.go`) interpolate `StartWidth` to `EndWidth` and `StartHeight` to `EndHeight` again, the rest of the fix kept | `TestScrollingZoomMoveShowsNoGap/zoom95/*` ("a blank cell at column 117, before a pane's corner") | **caught** (2 of 2 run) |
| The master-stack tiler ignores the master position and count (#321) | n/a, injected, cuts the wiring | `tileLayoutsIn` in `internal/app/tiling.go` calls the old `CalculateMasterStackLayout` instead of `CalculateMasterLayout` with `masterParams()` | `TestMasterPositionFromConfig` (every right, top, bottom and center case; left passes, as it must), `TestMasterLayoutIsTheSessions`, `TestMasterKeys`, `TestMasterResizeKeepsItsRatio`, `TestMasterDividerDragInTheCenter`. `TestDirectionalFocusMasterPositions` passes: it checks focus moves, not where the master is | **caught** |
| A run-time master layout change is not sent to the daemon (#321) | n/a, injected, cuts the call site | drop the `SendMasterLayout` call in `setMasterLayout` | `TestMasterLayoutIsTheSessions` (the second client puts the default layout back) | **caught** |
| A client does not adopt the session's master layout from a state sync (#321) | n/a, injected, cuts the call site | `masterRetile := false` in place of the `adoptWorkspaceMasterLayout` call in `ApplyStateSyncFrom` | `TestMasterLayoutIsTheSessions` (after a swap on the client that did not make the change). The first version of the test checked only the session's geometry and passed: a client draws its peer's rectangles until it retiles, so the test now retiles from each client | **caught** |
| The layout prefix has no key to move the master (#321) | n/a, injected, cuts the binding | `cycle_master_position` bound to nothing in the default `layout_prefix` | `TestMasterKeys` | **caught** |
| A master-stack resize is not kept in the ratio on every side (#321) | n/a, injected, cuts the call site | drop `m.setMasterRatio(ratio)` in `SyncMasterStackFromGeometry` | `TestMasterResizeKeepsItsRatio` (the retile takes the master back from 80 to 60 columns), `TestMasterDividerDragInTheCenter` | **caught** |
| A daemon restart drops each workspace's master layout (#321) | n/a, injected, cuts the call site | drop the `RestoreMasterLayouts` call in `daemon_resurrect.go` | `TestMasterLayoutSurvivesADaemonRestart` (the restored session puts the master back on the left) | **caught** |
| A client with the default config does not settle the master layout, so a later client with another config moves it (#321) | n/a, injected | `seedMasterLayout` returns early when the configured shape is the default, as it first did | `TestFirstClientSettlesTheMasterLayout` (the right-hand client moves the master to the right on both screens) | **caught** |
| Hiding the rail leaves the daemon holding the old pane rectangles: the toggle retiled against the stale session reserve, and the retile on the daemon's resize answer was never pushed | whole change | build `origin/main` at `0f70da9d` and point `TUIOS_E2E_BIN` at it | `TestHidingTheRailGrowsThePanes/bsp`, `/master-stack` and `/scrolling` (each at "the daemon after the rail is hidden": the panes still start at column 28) | **caught** (3 of 3 run) |
| The same, with the rest of the change in place | n/a, injected, cuts the call site | `if false && m.reserveOwed` in the `SessionResizeMsg` case in `internal/app/update.go` | `TestHidingTheRailGrowsThePanes/bsp`, `/master-stack` and `/scrolling`, at the same step | **caught** (3 of 3 run) |
| Chrome moved by a command (set-config) is never laid out or announced | whole change | as above, main at `0f70da9d` | `TestChromeSetFromTheCommandLineRetilesThePanes` (at "the daemon after the dock is hidden": the panes stay at row 2, 38 rows tall) | **caught** |
| The same, with the rest of the change in place | n/a, injected, cuts the call site | drop the `m.settleChrome()` call in `OS.Update` | `TestChromeSetFromTheCommandLineRetilesThePanes`, at the same step | **caught** |
| Whether the rail is shown is each client's own, so hiding it on one client hides it nowhere else | whole change | as above, main at `0f70da9d` | `TestHidingTheRailHidesItOnEveryClient` (at "the second client attaching": it reads its own config and draws no rail) | **caught** |
| A client does not adopt the session's rail from a state sync | n/a, injected, cuts the call site | `sidebarRetile := false` in place of the `adoptSidebarVisibility` call in `ApplyStateSyncFrom` | `TestHidingTheRailHidesItOnEveryClient` (at "the first client after it hid the rail": the second client keeps its rail, so the session keeps the rail's columns and the first client's panes do not grow) | **caught** |
| A daemon restart forgets that the rail was hidden | whole change | as above, main at `0f70da9d` | `TestHiddenRailSurvivesADaemonRestart` (at "the daemon before the restart", the stale-rectangle bug above) | **caught** |
| The same, with the rest of the change in place | n/a, injected, cuts the call site | drop the `RestoreSidebar` call in `daemon_resurrect.go` | `TestHiddenRailSurvivesADaemonRestart` (at "the client after the restart": it shows the rail its config asks for) | **caught** |
| An old client is made to follow the session's rail | n/a, injected, removes the gate | `c.sidebarOps.Store(welcome.SidebarOps)` in the welcome, ignoring `TUIOS_SIDEBAR_LEGACY` | `TestOldClientKeepsItsOwnRail` (at "the old client after the new one hid its rail": the old client hides its rail too) | **caught** |
| (the old-client test on main) | whole change | as above, main at `0f70da9d` | `TestOldClientKeepsItsOwnRail` (at "the daemon with both rails hidden", the stale-rectangle bug above) | **caught** |
| A switch to an empty session skips taking and offering the rail, so the next client to attach settles it from its own config | round 1 of the change | build `91985c0` and point `TUIOS_E2E_BIN` at it | `TestAnEmptySessionTakesTheRailOfItsMaker` (at "the second client attaching to the empty session shows the rail") | **caught** |
| The same, with the rest of the change in place | n/a, injected, cuts the call site | drop the `joinSession` call in `adoptEmptySessionVersion` | `TestAnEmptySessionTakesTheRailOfItsMaker`, at the same step | **caught** |
| A rail opened for the keyboard scope counts in the reserve and moves every client's panes | round 1 of the change | as above, `91985c0` | `TestRailOpenedForTheKeyboardMovesNoPanes` (at "the daemon with the scope open": the panes start at column 28) | **caught** |
| The same, with the rest of the change in place | n/a, injected | `OwnLayoutReserve` counts the rail whatever `SidebarRevealedForFocus` says | `TestRailOpenedForTheKeyboardMovesNoPanes`, at the same step | **caught** |
| Two clients of different widths under `window_size = latest`: the daemon keeps the layout of a middle answer | round 1 of the change | as above, `91985c0` | `TestDaemonKeepsTheLayoutTheClientsDraw` (3 of 3 runs, at toggle 0 or 2: the daemon holds panes no client draws) | **caught** |
| The same, with the push after a newer layout generation cut out | n/a, injected, cuts the call site | `if false && m.layoutGenApplied > m.layoutGenPushed` in the `SessionResizeMsg` case | `TestDaemonKeepsTheLayoutTheClientsDraw` (2 of 2 runs) | **caught** |
| The daemon takes the rectangles of a push tiled in an older layout generation | n/a, injected | `keepRectsOfStaleLayout` returns before it compares generations | none in this suite (`TestDaemonKeepsTheLayoutTheClientsDraw` passes 5 of 5: each client pushes again for every newer generation, so the last push to land is current). `TestAPushTiledInAnOlderLayoutKeepsTheSessionsRectangles` in `internal/session` fails | **not caught** here, caught by the unit test |
| A narrowing resize erases a wide rune from history for good (found through Collie v1.15.0) | n/a, injected, cuts the call site | put back the history blanking call in `vt.Emulator.Resize` | `TestWideRuneInHistorySurvivesANarrowPane` ("the history line \"WRA世…\" holds 29 of its 30 runes") | **caught** |
| A screenshot draws a history row one column wider than the pane | n/a, injected, cuts the call site | drop the `vt.ClipHistoryRow` call in `gridOf`, `internal/session/screenshot_grid.go` | `TestWideRuneInHistorySurvivesANarrowPane` ("a screenshot row is 49 columns wide in a 48-column pane") | **caught** |
| A scrolled-back history row spills over the pane's border (the reason the history blanking existed) | n/a | no single call site: on a build without the blanking and without any per-row clip, the client's frame still stops the row at the border, so the test's frame check is a guard on that and has no control | `TestWideRuneInHistorySurvivesANarrowPane` passes | **not applicable** |
| The pane renderer draws a history row one column wider than the pane, and the frame's width cut drops the whole wide cell at the edge with its background, copy cursor and selection | n/a, injected, cuts the call site | drop the `vt.ClipHistoryRow` call in `renderTerminal`, `internal/app/render_terminal.go` | `TestHistoryRowWithAWideRuneAtTheEdgeIsClippedToThePane` in `internal/app` ("a history row is 13 columns wide in a 12-column pane"). `TestWideRuneInHistorySurvivesANarrowPane` still passes: its frame keeps the background at the edge in that layout | **caught** by the kept render test, **not caught** end to end |
| The files list of a pane on another machine stays on the folder the pane started in, and a file deleted over there stays on it (#313, the follow-up) | whole change | build `origin/main` (`051da3ac`) and point `TUIOS_E2E_BIN` at it | `TestFilesFollowAWindowOnAHost` ("the files list did not follow the far shell's cd"), `TestFilesFollowAPaneInASessionOnAHost` ("a file removed on the far machine stayed on the list"; its cd step passes on main only because the test's two machines are one, so the client read the far shell's pid on its own disk, which a real second machine never allows) | **caught** (2 of 2 run) |
| The client takes only the daemon's first folder for a pane on another machine | n/a, injected, cuts the call site | `takeDaemonCwd` in `internal/app/sidebar_files.go` calls `adoptWindowCwd` for every pane | `TestFilesFollowAWindowOnAHost`, `TestFilesFollowAPaneInASessionOnAHost` (both "the files list did not follow the far shell's cd") | **caught** (2 of 2 run) |
| Nothing watches a folder on another machine | n/a, injected, cuts the wiring | the welcome in `internal/session/daemon_handlers.go` sends `DirWatch: false`, so no client asks | `TestFilesFollowAWindowOnAHost`, `TestFilesFollowAPaneInASessionOnAHost` (both "a file removed on the far machine stayed on the list"); the cd step passes in both, which is the positive half | **caught** (2 of 2 run) |
| The near daemon stops watching a folder on another machine after its first report | n/a, injected, cuts the call site | `watchRemoteDir` in `internal/session/daemon_dirwatch.go` returns after the first wait-dir answer instead of asking again on the same connection | `TestFilesFollowAWindowOnAHost` ("a file made on the far machine after a removal never reached the list"); the removal step passes, which is the positive half | **caught** (1 of 1 run) |
| A pane running ssh lists this machine's folder of the same name | whole change | build `origin/main` (`051da3ac`) | `TestFilesDoNotListALocalFolderForAnSSHShell/client_pane` and `/daemon_pane` (both "the files list still shows this machine's folder for a shell on another machine"). The last step of each, the list coming back when the shell has the terminal again, is the positive half | **caught** (2 of 2 run) |
| The daemon does not report the machine an ssh shell announced | n/a, injected, cuts the call site | drop `w.CwdHost = place.elsewhere` from `fillLiveFacts` in `internal/session/session.go` | `TestFilesDoNotListALocalFolderForAnSSHShell/daemon_pane`. `/client_pane` passes, and that is correct: a pane the client runs itself is judged in the client | **caught** (1 of 1 that uses it) |
| herdr plugins got the tuios binary as `HERDR_BIN_PATH`, which knew only `pane` and `notification` | n/a, injected, cuts the wiring | `bin = link` becomes `_ = link` in `internal/session/daemon.go`, so `SetHerdrBin` gets the binary | `TestHerdrFrontTerminalBrowserSplit` (server reload-config printed `unknown command "server" for "tuios"` instead of herdr's `unsupported` error), `TestHerdrFrontStartsAnAgentAndShowsAWorkspace` ("workspace list exited 1"). The three tests that use only `pane` pass, because `tuios pane` answers through the same front | **caught** (2 of 5; the other 3 are correct to pass) |
| A pane had no `HERDR_TAB_ID`, which terminal-browser reads as its own tab | n/a, injected | drop the `HERDR_TAB_ID` append from `Manager.HerdrEnv` | `TestHerdrFrontTerminalBrowserSplit` ("the pane's HERDR_PANE_ID and HERDR_TAB_ID are", with the tab id empty) | **caught** |
| `pane swap` never reaches the client that owns the split tree | n/a, injected, cuts the wiring | rename `case "swap_windows":` in `internal/app/update.go` | `TestHerdrFrontSplitLeftSwaps` ("pane swap exited 1") | **caught** |
| `pane edges`, `pane focus --direction` and `pane zoom` unanswered | n/a, injected, cuts the wiring | drop `pane.edges`, `pane.focus_direction` and `pane.zoom` from `herdrMethods` | `TestHerdrFrontVimNavigation` ("pane edges exited 1") | **caught** |
| A read-only pane swaps panes | n/a, injected | replace the `herdrAdmit(cs, "set-layout", ...)` check in `herdrPaneSwap` with a nil error | `TestHerdrFrontHoldsThePaneToItsGrants` ("swap from a read-only pane: exit 0", and "a refused call changed the layout"). The same cut in the focus and zoom methods is not caught, and that is correct: `focus-window` and `run-command` refuse the pane on the same grant | **caught** for swap |
| `agent start` unanswered | n/a, injected, cuts the wiring | drop `agent.start` and `workspace.focus` from `herdrMethods` | `TestHerdrFrontStartsAnAgentAndShowsAWorkspace` ("agent start exited 1") | **caught** |
| `workspace focus` unanswered | n/a, injected, cuts the wiring | drop `workspace.focus` from `herdrMethods` | `TestHerdrFrontStartsAnAgentAndShowsAWorkspace` ("workspace focus exited 1") | **caught** |
| `pane zoom` zoomed the pane that had the focus before, not the one named (review finding 1) | n/a, injected, cuts the wiring | drop `m.FocusWindow(i)` for the named pane in `ZoomWindowByID` (`internal/app/os_minimize.go`) | `TestHerdrFrontVimNavigation` ("pane zoom --on exited 1": the client zoomed the focused pane and reported the named one unzoomed). Stress on `taskset -c 0-1`: the binary before the fix failed the new test 8 of 20, after it 0 of 20 | **caught** |
| `agent start` quoted fish arguments the POSIX way (review finding 2) | n/a, injected | the `fish` case of `herdrShellQuote` returns the POSIX quoting | `TestHerdrFrontAgentStartQuotesForTheShell` ("the agent in the fish pane got the arguments" changed). In this corpus the touch commands did not run under the control, so the catch is the changed arguments | **caught** |
| `pane split` answered before the tiled rectangles reached the daemon (review finding 3) | n/a, injected | the settle wait in `verbSplitWindow` stops at the first state that holds the new pane | none here: `TestHerdrFrontSplitLeftSwaps` passed 30 of 30 on `taskset -c 0-1` and 50 of 50 on one core with the wait cut. The binary before the whole review fix failed it 1 of 30 on two cores. The review saw 3 of 30 on its machine | **not caught here** |
| A split whose new pane was late fell back to `new-window` and made a second pane (review finding 3) | n/a, injected | force `created = ""` in `verbSplitWindow`, and restore the fallback in `herdrPaneSplit` | with the fallback: `TestHerdrFrontSplitLeftSwaps` ("pane swap did not swap the new pane with the caller": the swap found the pane `new-window` made). With the fix and the same injection: "pane split exited 1" with `pane_split_failed`, and no pane is made | **caught** |
| `pane process-info` gave a read-only pane another pane's argv, command line and directory (review finding 4) | n/a, injected | `full = true` in `herdrPaneProcessInfo` | `TestHerdrFrontHoldsThePaneToItsGrants` ("pane process-info of another pane from a read-only pane" shows `sleep 7654321` and `cwd`) | **caught** |
| The front answered a bad agent kind and an unknown option with exit 1 instead of herdr's 2 (review finding 5) | n/a, injected | skip the `herdrAgentKind` check in `agentStart` and the `launchOptions` check in `Parse` | `TestHerdrFrontStartsAnAgentAndShowsAWorkspace` ("agent start --kind nope: exit 1", "herdr --foo: exit 1") | **caught** |
| A shorter pane cut the rows below the cursor off the bottom of the screen | n/a, cuts the call site | restore `internal/vt/screen.go` and `internal/vt/emulator.go` from the merge base, which drops the `shrinkRows` call in `Screen.Resize` | `TestScrollbackResizeShorterKeepsRowsBelowCursor` ("a row below the cursor is gone from history and screen"); vt cases in `TestConform_ResizeRows` fail the same way | **caught** |
| A shorter pane under an alternate screen cut the main screen's prompt and the rows above it | the same | the same build | `TestScrollbackResizeUnderAltScreenKeepsMainRows` ("witness lines [11 ... 30] are gone from the daemon") | **caught** |
| A restored pane put saved history rows wider than the saved screen onto the screen, which cut them | n/a, cuts the call site | drop the `rowExtent` loop from `restoreHistory` in `internal/session/scrollback_persist.go` | `TestScrollbackResizeRestartKeepsWideHistory` ("23 lines were whole in the daemon's history before the restart and are cut after it") | **caught** |
| capture-pane with history after a run of resizes | n/a, never broken | the two builds above | none, and that is correct: `TestScrollbackResizeCaptureCountsMatch` passes on all three, because it guards capture behaviour that already worked | **guard, not a control** |
| A narrowing resize cut the screen's rows at the new width, so widening again could not bring the tail back | n/a, cuts the call site | `reflowMain := false && ...` in `vt.Emulator.Resize` | `TestScrollbackResizeNarrowKeepsScreenLine` ("the daemon lost the tail of a screen line"), `TestScrollbackResizeDragKeepsClientInStepWithDaemon` ("panes whose line is whole: daemon 2, client 1"); vt `TestConform_Reflow`, `TestResizeConservesText` and the marks, remap and prompt tests fail the same way | **caught** |
| A divider drag costs the daemon one emulator resize per pane | n/a, never broken | the build above | none, and that is correct: `TestScrollbackResizeDragResizesDaemonOnce` passes on both, because it guards a cost that already held | **guard, not a control** |
| A drag reflowed the client at every step while the daemon resized once, so around a parked cursor the two laid lines out apart | n/a, cuts the call site | `ResizeVisual` in `internal/terminal/window_geometry.go` resizes the emulator again when the stream owns the size | `TestScrollbackResizeDragAgreesWithTheDaemonAroundAParkedCursor/ends_where_it_started` ("the client and the daemon show different lines after the drag"). Its `ends_narrower` half passes on that build: the daemon's resize at the end lines the two up again | **caught** |
| An open prompt's row was cut at a narrower width, and a drag that ended where it started never got a repaint | n/a, injected | the build above, and `frozenTail` replaced by nil in `internal/vt/reflow.go` | `TestScrollbackResizeDragKeepsATypedCommand` ("1 of 2 panes show the whole typed command"); vt `TestReflowKeepsAnOpenPromptRowWhole` fails the same way | **caught** |
| A lone OSC 133 A froze the command output under it, and the freeze cut it | n/a, injected | a lone A opens the prompt in `vt.Emulator.reflowMain`, and `frozenTail` replaced by nil | `TestScrollbackResizeKeepsOutputUnderALoneA` ("output under a lone OSC 133 A lost its tail"). With tails kept and only the lone-A rule put back, nothing is lost and the E2E test passes; vt `TestReflowLeavesAMarkedPromptToTheShell` ("lone A") pins the rule | **caught** |
| The padding flag did not travel in a snapshot or in saved history | n/a, cuts the call site | drop `RestorePads` from `ApplyTerminalState`, or drop `ScrollbackPads` from `historyRows.state` | `TestSnapshotCarriesPadding`, `TestSavedHistoryCarriesPadding` in `internal/session` ("client and daemon differ after widening") | **caught in `internal/session`** |
| A shell's repaint of a frozen prompt row through the ASCII fast path kept the row's tail, so widening showed text from before the repaint | n/a, cuts the call site | drop `dropTail` from the narrow-run branch of the print path in `internal/vt/utf8.go` | vt `TestReflowOpenPromptRepaintDropsTheTail` | **caught in `internal/vt`** |
| The cursor on a frozen prompt row came back at the narrow width's last column | n/a, injected | `s.wideCol = false && ...` in `Screen.reflow` | vt `TestReflowCursorOnAnOpenPromptComesBack` (Z at 29, not 34), `TestResizeConservesText` ("the cursor moved off its text") | **caught in `internal/vt`** |
| A history saved while a prompt was frozen narrower lost the row's tail | n/a, cuts the call site | `captureHistoryRows` without the `MainRowTail` lookup | `TestSavedHistoryKeepsAFrozenPromptsTail` in `internal/session` | **caught in `internal/session`** |
| A session switched to latest started from the client that attached last, once clients stopped reporting input under other policies | n/a, cuts the call site | the policy change case in `reportActivity` (`internal/app/update.go`) made unreachable | `TestWindowSizeSwitchToLatestStartsFromInput` ("after the switch to latest the session is 80x24, want the big client's 200x50") | **caught** |
| A client that knew its session was smallest kept that policy after a switch into a latest session, and reported no input | the attach reply policy and the reset on switch | build the PR head before this fix (`origin/refactor/simplify-app` at `bc2f0601`) and point `TUIOS_E2E_BIN` at it | `TestWindowSizeSwitchIntoLatestReportsInput` ("after input in the small client, which moved into ws: the session is 200x50 under latest, want 80x24") | **caught** |
| The client that attached second never learned smallest, so it reported every key | the same build | the same | `TestWindowSizeSmallestSendsNoActivity` ("under smallest the daemon received 5 activity reports, want 0"); positive half in the same fixture: 6 reports once the policy is latest | **caught** |
| A quiet folder refresh rebuilt the rail twice, even with nothing changed, and ran up to ten times a second | whole change | keep the `rail=` count in `internal/app/tick_stats.go` and `sidebar_cache.go`, put the rest of `internal/app` back to the parent commit, and point `TUIOS_E2E_BIN` at that build | `TestRailFilesRefreshIsPaced/rename` (58 rebuilds, budget 25) and `/swap` (71 rebuilds, budget 12); with the change, 17 and 5. Positive half: `/rename` waits for the last name on the rail | **caught** (2 of 2 run) |
| The daemon parses only the control keys of a graphics command and stopped answering EINVAL to a payload that is not base64 | n/a, injected | drop the `kittyPayloadErrorOwed` decode from the header-only branch of `parseKittyCommand` in `internal/vt/kitty_parser.go` | `TestKittyPayloadErrorReplies/daemon` (the `i=41;EINVAL` reply read "0 times, want once", the same for the query `i=42`, which got `OK`); `/standalone` passes, which is its positive half: the client decodes every payload. Unit `FuzzKittyHeader` fails on its seeds | **caught**. Removing `terminal.SetKittyHeaderOnly(true)` from `internal/session/session.go` is not caught, and that is correct: it changes CPU, not replies. `TestKittyHeaderLeavesThePayloadAlone` holds the parse to no payload copy |
| The header-only parse left `FilePath` empty for `t=s` and `t=t`, so the daemon had no name to delete and, with the shared memory cleanup (#341), every unread frame stayed in `/dev/shm` | n/a, injected, cuts the call site | make the `kittyHeaderNamesObject` case in the header-only branch of `parseKittyCommand` in `internal/vt/kitty_parser.go` never match | Unit `TestKittyHeaderNamesTheObject` (`t=s` and `t=t` named "", want the name); `t=d` and `t=f` stay unnamed, its positive half. On a local merge with #341, `TestKittySharedMemoryFramesAreReleased/daemon-unwatched` read "40 frames: 0 forwarded, 40 still in /dev/shm", and 0 with the fix | **caught** |
| A pane in SGR-pixel mode (1016) was told the cell centre, never the pixel under the pointer | the call that asks the host for 1016 | in `internal/app/update.go`, replace `m.hostPixelMouseCmd(msg)` with `tea.Cmd(nil)` | `TestSGRPixelMouseCarriesTheHostPixel/standalone` and `/daemon` ("tuios never turned on SGR-pixel reports in its terminal") | **caught** (2 of 2 run) |
| The same, with 1016 on in the host but the pixel not passed to the pane | the pixel argument at the forwarding call | in `internal/input/mouse.go`, pass `vt.MousePixel{}` in place of `o.PointerPixelIn(...)` | `TestSGRPixelMouseCarriesTheHostPixel/standalone` and `/daemon` ("the pane never got the report PX35;474;308."; it got the cell centre) | **caught** (2 of 2 run) |
| The host stayed in 1016 after the pane turned it off, so later cell reports were read as pixels | the request that turns 1016 off | in `internal/app/host_pixel_mouse.go`, return nil in place of `tea.Raw(hostPixelMouseOff)` | `TestSGRPixelMouseCarriesTheHostPixel/standalone` and `/daemon` ("tuios never turned off SGR-pixel reports in its terminal"). This is the negative half: cell reports reach the pane unchanged once 1016 is off | **caught** (2 of 2 run) |
| After $EDITOR, tuios still read every report as pixels: Bubble Tea writes 1006 when it takes the terminal back, which ends 1016, and the client state kept 1016 active | n/a, injected | in `internal/app/host_pixel_mouse.go`, make `forget` do nothing | `TestSGRPixelMouseAfterTheEditor/standalone` and `/daemon` ("the pane never got the report"; the pane got `PX35;54;5.`, the cell report divided by the cell size) | **caught** |
| Turning 1016 off left the terminal in X10, not SGR, in ghostty, xterm and kitty | n/a, injected | `hostPixelMouseOff` back to `"\x1b[?1016l\x1b[?1016$p"` | `TestSGRPixelMouseCarriesTheHostPixel/standalone` and `/daemon` ("never turned off SGR-pixel reports and put SGR back"). This is a check of the bytes: the tuitest host does not drop to X10 | **caught** |
| A panel closed while a lone pane was fullscreen kept its hit area, which took the motion over its old rectangle from the pane | n/a, injected | drop the `m.OverlayHits = m.OverlayHits[:0]` from the fast path in `composeFrame` (`internal/app/render.go`) | `TestSGRPixelMouseAfterTheEditor/standalone` and `/daemon` ("the pane never got the report"; nothing reached the pane after the palette closed) | **caught** |
| The harness logged each client's PTY to an unbounded `pty.log`, and a client attached to a kitty stream wrote 473 MB in 19 s | n/a, cuts the call site | in `startIn` (`harness_test.go`), hand tuitest a plain `os.Create(logPath)` in place of `newBoundedLog` | `TestPtyLogStaysBounded` ("the pty log is 262094560 bytes, over its limit of 262144"); with the fix, 149203 bytes. Positive half: the log starts with the dropped-bytes header and still holds the last 4 KiB the host was sent | **caught** |
| A capped log that keeps its oldest bytes and stops writing at the limit, so a failure shows the start of the run, not the end | n/a, injected | in `boundedLog.Write` (`ptylog_test.go`), drop every write that would pass the limit | `TestPtyLogStaysBounded` ("the log lost its newest bytes: the last 4096 bytes sent to the host are not in it") | **caught** |
| A kitty stand-in killed by a signal left its object in `/dev/shm`, one per run of a shared memory test | n/a, cuts the call site | remove the `t.Cleanup` registration in `standInShmPrefix` (`ptylog_test.go`) | `TestKittyStandInShmRemovedAfterTest` ("1 shared memory objects stayed in /dev/shm after the test"). Positive half: the subtest asserts the object is still there after SIGKILL ends the stand-in | **caught** |
| `TestKittyStandInShmRemovedAfterTest` looked for the object once, right after the stand-in reported its geometry. The stand-in creates the object after that report, so the test failed when it looked first (main, run 37220244828) | n/a, injected | in `e2e/tui/frameloop/main.go`, sleep one second between `report` and `allocate`; run the test as it was | `TestKittyStandInShmRemovedAfterTest` ("the stand-in made no object with prefix"), the same message as CI. With the condition wait: passes with the delay, and 10 of 10 without it | **caught** |
| A pane that asked for releases got a press with no release while the host sent none (no protocol, or the moment before the host switched flags), so a compositor in the pane held the key and its client repeated it | whole change, then the call site | build `origin/main` (`4e689aa1`); then drop the `rawInput = append(rawInput, releaseNow...)` line in `internal/input/keyboard_terminal.go` | `TestPaneGetsReleaseTheHostCannotSend` ("the pane received `\x1b[120u`", the press alone) both times; positive half `TestPaneGetsRepeatAsRepeat`, where the host sends the release and the pane gets exactly one | **caught** |
| A held key's repeats reached the pane as more presses of a key that was down | whole change, then the call site | `origin/main`; then drop the `field += ":2"` line in `kittyModField`, `internal/vt/kitty_keyboard.go` | `TestPaneGetsRepeatAsRepeat` (the pane received `\x1b[97u` three times) both times | **caught** |
| The lock modifiers were dropped for a pane that asked for every key, so a compositor in it could not learn that Num Lock was on | whole change, then the gate | `origin/main`; then make the report-all-keys gate in `kittyModParamFor` always return early | `TestPaneGetsLockState` both times; the negative half (no lock bits under disambiguate) is the `internal/vt` case `up, numlock on, has no lock bits` | **caught** |
| A multifocus peer got the focused pane's bytes, so a shell peer got kitty releases as text ("1:3u: command not found") | the call site | send `rawInput` to each peer instead of `paneKeyBytes(host, w, o, false)` in `internal/input/keyboard_terminal.go`; also the build before the change | `TestMultifocusPeerGetsItsOwnEncoding` (the shell never printed PEER-42) both times | **caught** |
| The leader pressed twice sent a press with no release | the call site | drop the release from the `paneKeyBytes` result in `forwardKeyToFocusedWindow`; also the build before the change | `TestLeaderTwiceGetsItsRelease` (the pane received the press alone) both times | **caught** |
| Enter, Tab and Backspace got CSI u and a release under event types without report-all-keys | the gate | remove the `isKittyLegacyTextKey` checks in `internal/vt/kitty_keyboard.go`; also the build before the change | `TestNoReleaseForEnterWithoutAllKeys`; unit `TestEncoderMatchesKitty` (49 and 13 mismatches against kitty's encoder) | **caught** |
| A release left out the alternate keys | the call site | drop the `kittyAlternateKeys` call in `EncodeKeyReleaseCSIu` | unit `TestEncoderMatchesKitty` (13 mismatches) | **caught** |
| Shift+Up and Shift+Down never reached a full-screen program or a mouse pane | whole change | the build before the change | `TestShiftUpReachesAFullScreenProgram`, `TestShiftUpReachesAMousePane` (the pane received nothing); positive half `TestShiftUpScrollsTheShell` passes on both | **caught** |
| `agent-statusline` panicked with SIGSEGV when the daemon could not be dialed (issue #374): the dial closure returned a typed-nil `*VerbClient` and the deferred close called `Close` on it | whole change | build `origin/main` (`3f8df65b`) and point `TUIOS_E2E_BIN` at it | `TestStatusLineWithADeadDaemonStillRenders`, all four cases (missing and stale socket, with and without a named pane): "exit status 2", "panic: runtime error: invalid memory address or nil pointer dereference" | **caught** (4 of 4 run). Three layers each stop the crash on their own: the closures return an untyped nil, `reportStatusLine` assigns the client only after a good dial, and `Close` is safe on a nil receiver |
| A built-in theme with no cursor colour (horizon and 11 others) put a nil `*tint.Color` into a `color.Color`, so an OSC 12 query in a pane and `tuios list-themes horizon` panicked. Found in the #374 sweep | whole change | build `origin/main` (`3f8df65b`) and point `TUIOS_E2E_BIN` at it | `TestOSC12QueryUnderAThemeWithNoCursorColour/horizon` (the shell marker never came, the client died), `TestListThemesDescribesAThemeWithNoCursorColour` ("list-themes horizon failed: failed to read response: EOF"). The `dracula` halves pass on both builds, and are the positive half | **caught** |
| The emulator gets the theme's nil cursor as a non-nil `color.Color` again | n/a, injected, cuts the call site | `TerminalCursor` in `internal/theme/theme.go` returns `t.Cursor` instead of `AsColor(t.Cursor)` | `TestOSC12QueryUnderAThemeWithNoCursorColour/horizon`; `/dracula` passes. `TestListThemesDescribesAThemeWithNoCursorColour` passes, and that is correct: `Describe` has its own guard | **caught** |
| The width keys sent with send-keys resized the PTYs and left the tiles at equal shares: `RemoteKeysDoneMsg` dropped the workspace's BSP tree and tiled it again from nothing | the call site (the fix only deletes it) | build `origin/main` (`11335985`), which still has the rebuild | `TestWidthPercentAfterAClose` (screen two halves, daemon 32 and 128 columns), `TestWidthPercentWithNoClose` (the same with three panes). Positive half: `TestWidthPercentFromTheKeyboard`, the same keys on the client's keyboard, passes on both builds | **caught** |
| An active Neovim pane could move focus with an unsolicited OSC request | n/a, injected | remove `!m.matchesPendingNvimNavigation(msg)` from the rejection condition in `internal/app/nvim_navigation.go` | `TestNvimNavigationOSCNeedsForwardedKey` ("active pane moved focus ... without a forwarded key") | **caught** |
| A switch to an empty workspace sent no focus-out, so the pane read two focus-ins in a row | the focus reports by window id | build the PR's own focus report commit (`ea43bbfb`), which reports only inside `FocusWindow` | `TestPaneFocusEventsFollowTheFocus` ("switch to an empty workspace: the pane read `^[[O^[[I`, want `^[[O^[[I^[[O`") | **caught** |
| The client's last messages after a detach reported the focus again, so a detached pane read focus-out and then focus-in | n/a, injected | drop the `detachFired` check from `reportFocusChange` in `internal/app/daemon_wiring.go` | `TestPaneFocusEventsFollowTheFocus` ("detach: the pane read ...`^[[O^[[I`") | **caught** |
| A program that set focus reporting (DECSET 1004) in the focused pane of a daemon session read no focus-in report | the call site | replace `p.noteFocusReporting(focusOn)` in `vtWriter` (`internal/session/session.go`) with `_ = focusOn` | `TestFocusReportOnEnable/daemon` ("focused pane read \"\" after setting 1004"); `/standalone` passes | **caught** |
| The same in a standalone pane | the gate line | make the `FocusReportingEnabled` edge check in the PTY reader (`internal/terminal/window_io.go`) `if false && ...` | `TestFocusReportOnEnable/standalone` ("focused pane read \"\" after setting 1004"); `/daemon` passes | **caught** |
| The daemon sent the focus-in report with the host terminal out of focus | the host focus term | make `sessionShown` (`internal/session/focus_report.go`) accept a client that reported losing focus | `TestFocusReportOnEnable/daemon` ("pane read \"\\x1b[I\" with the host out of focus") | **caught** |
| A Crush permission dialog stays on working when Crush reports working after blocked | n/a, injected, cuts the wiring | drop the `claim.screenWins` branch from `blockerOverridesClaim` in `internal/session/agent_state.go` | `TestCrushPermissionAnsweredFromTheInbox` ("the pane is {State:working ... Source:report ...}, want state needs_input") | **caught** |
| The Inbox cannot answer a Crush permission dialog | n/a, injected, cuts the wiring | drop the `[screen.rule.answers]` block from `internal/harness/manifests/crush.toml` | `TestCrushPermissionAnsweredFromTheInbox` ("the pane never showed [ALLOWED]") | **caught** |
| An Inbox row with a long pane name shows no summary | n/a, injected, cuts the wiring | drop the name-width cap from `inboxItemRow` in `internal/app/render_inbox.go` | `TestCrushPermissionAnsweredFromTheInbox` ("the Inbox never listed the Crush approval with its summary") | **caught** |
| A Crush command that scrolls in its dialog is allowed on one press, its risk unseen | n/a, injected, cuts the call site | drop the `harness.PartialSuffix` check from `riskOfLine` in `internal/session/approval_risk.go` | `TestCrushRiskyCommandTakesTwoPresses` ("the approval is not marked cut short"); the PR head before the fix fails it too (the message was the description, "approve bash: Install the tool") | **caught** |
| The dialog's words quoted in Crush's chat become an answerable approval | n/a, injected, cuts the call site | make the dialog check in `Classify` (`internal/harness/classify.go`) always true | `TestCrushQuotedDialogIsNotAPrompt` ("the quoted dialog made the pane needs_input"); the PR head before the fix fails it too | **caught** |
| A Crush killed at its dialog leaves the pane on needs_input at the shell | n/a, injected, cuts the wiring | drop the herdr fields copy onto the blocker claim in `applyAgentReport` (`internal/session/agent_state.go`); separately, drop the blocker case from `herdrHeld` in `detectionPass` (`internal/session/agent_detect.go`) | `TestCrushCrashAtItsDialogClearsThePane` ("the pane is {State:needs_input ...}, want state none"), both controls; the PR head before the fix fails it too. A first version passed under both controls: in a short pane the shell's output after the kill scrolled the dialog off, and a pane with no dialog clears by another route. The test now runs in a tiled pane and checks that the dialog is still on the screen | **caught** |
| A program that only reports itself as Crush gets Crush's answers | n/a, injected, cuts the call site | make the `paneRunsHarness` check in `lookAtPrompt` (`internal/session/verb_respond.go`) false | `TestOnlyCrushGetsCrushAnswers` ("a program that only says it is Crush is offered answers"); the PR head before the fix fails it too | **caught** |
| A screen rule that is not a dialog rule loses its kind when its line starts with "approve " | n/a, injected, cuts the call site | drop the `RuleShowsDialog` condition from `screenRuleMessage` in `internal/session/agent_screen.go` | `TestScreenMessageKeepsItsKindOutsideDialogs` ("the pane's agent state never held \"message\": \"approval: approve deploy: prod now? [y/n]\"") | **caught** |
| A pane on a hidden workspace stayed listed after its program exited: the daemon sent PTYClosed to subscribed clients only, and a client streams only the shown workspace | the call site | put the `cs.ptySubscriptions[ptyID]` check back in `notifyPTYClosed` | `TestAPaneOnAHiddenWorkspaceClosesWhenItsProgramExits` (the session still lists the pane after 15 s). Positive half: the pane is listed before it exits. `TestAPaneShownByFocusWindowStreamsItsOutput` passes on this build | **caught** |
| focus-window on a pane on a hidden workspace showed it blank, and its kitty frames never reached the host: a workspace switch adopted from a state sync did not subscribe the panes it brought on screen | the call site | drop the `m.reconcilePaneStreams()` call at the end of `ApplyStateSyncFrom` | `TestAPaneShownByFocusWindowStreamsItsOutput` (neither EARLYMARK nor LIVEMARK shows). `TestAPaneOnAHiddenWorkspaceClosesWhenItsProgramExits` passes on this build. Both tests also fail on `origin/main` (`11335985`) | **caught** |
| A long message was cut in the dock with no way to read the rest, and the log viewer cut it too or did not hold it (#381) | whole change | build `origin/main` (`e1af811a`) and point `TUIOS_E2E_BIN` at it | `TestLongNotificationOpensInFull` ("hovering the cut message showed no label", "a click on the cut message did not show its end"), `TestLogViewerShowsALongEntryInFull` ("the log viewer does not list the long message"), `TestMessageViewScrolls` ("prefix N did not open the message"), `TestHoverHoldsAMessage` ("the message burned down under the pointer"), `TestLongNoSpaceMessageKeepsTheClientResponsive`, `TestViewersOverTheRailAndTheLeader`. `TestClipboardAskIsAllowedByAClick` and `TestKeyPressEndsTheHold` pass on main, which is correct: they guard regressions the first version of this change brought in | **caught** |
| A click on a cut message goes to its pane instead of showing the message | n/a, injected, cuts the call site | drop the `m.notifHit.Cut` branch from `clickVisibleNotification` in `internal/app/notification_jump.go` | `TestLongNotificationOpensInFull` ("a click on the cut message did not show its end"); the other three pass | **caught** |
| Prefix N does nothing | n/a, injected, cuts the call site | drop `d.Register("prefix_last_message", ...)` from `internal/input/prefix_actions.go` | `TestLongNotificationOpensInFull` ("prefix N did not show the last message in full"), `TestMessageViewScrolls` | **caught** |
| Enter on a log entry does nothing | n/a, injected, cuts the call site | drop the `enter` case from `handleLogViewerKey` in `internal/input/handler.go` | `TestLogViewerShowsALongEntryInFull` ("enter on the long entry did not show its end") | **caught** |
| The wheel does not scroll the message view | n/a, injected, cuts the call site | drop the `overlayKindMessage` case from `OverlayMouseWheel` in `internal/app/overlay_mouse.go` | `TestMessageViewScrolls` ("the wheel did not reach the last line"); the keys before it pass | **caught** |
| The hover label is never drawn over a lone fullscreen pane, because the fast path skips tooltips | n/a, injected | drop the `m.Tooltip.Source != tooltipNone` clause from `fullscreenFastWindow` in `internal/app/render.go` | `TestLongNotificationOpensInFull` ("hovering the cut message showed no label") | **caught** |
| Motion over the message block is not tracked, so it neither holds the message nor shows the label | n/a, injected, cuts the call site | drop `o.DockNotifHoverAt(...)` from `handleMouseMotion` in `internal/input/mouse_motion.go` | `TestHoverHoldsAMessage` ("the message burned down under the pointer"), `TestLongNotificationOpensInFull` | **caught** |
| An info message the dock showed is logged only with verbose logging on | n/a, injected | log it with `m.LogInfo` again in `showNotification` (`internal/app/os_notify.go`) | `TestLogViewerShowsALongEntryInFull` ("the log viewer does not list the long message") | **caught** |
| Keys in terminal mode pass by the open message view | n/a, injected, cuts the call site | drop the `MessageViewOpen` route from `internal/input/keyboard_terminal.go` | `TestLongNotificationOpensInFull` ("esc did not close the message view"), `TestLogViewerShowsALongEntryInFull`, `TestMessageViewScrolls` ("G did not reach the last line") | **caught** |
| The first version of the #381 change, before review: a click on a clipboard ask opened the view, which cannot allow it; an 8000 byte OSC 9 with no spaces froze the client; a key press did not end the hover hold; the rail kept the view's keys; a paste reached the pane under the view; the leader was swallowed | that review's fixes | build the PR head before them (`c2e76b45`) | `TestClipboardAskIsAllowedByAClick` ("a click on the ask did not allow the write"), `TestLongNoSpaceMessageKeepsTheClientResponsive` ("the message view did not open in time"), `TestKeyPressEndsTheHold` ("the message stayed after a key press"), `TestViewersOverTheRailAndTheLeader` (leader, paste and rail steps) | **caught** |
| A click on a clipboard ask opens the message view instead of allowing it | n/a, injected, cuts the call site | drop `&& !isClipboardAsk(visible.Target)` from `clickVisibleNotification` | `TestClipboardAskIsAllowedByAClick` | **caught** |
| A key press does not end the hover hold | n/a, injected, cuts the call site | drop `m.NotifHoldEnd()` from the `tea.KeyPressMsg` case in `internal/app/update.go` | `TestKeyPressEndsTheHold` | **caught** |
| The focused rail takes the message view's keys | n/a, injected | drop `!o.MessageViewOpen() && !o.ShowLogs` from the rail exception in `internal/input/handler.go` | `TestViewersOverTheRailAndTheLeader` ("esc did not reach the view over the rail") | **caught** |
| A paste while a viewer is open reaches the pane | n/a, injected, cuts the call site | drop the viewer clause from `pasteTakenByOverlay` | `TestViewersOverTheRailAndTheLeader` ("a paste made while the view was open reached the pane") | **caught** |
| The leader is swallowed while the message view is open | n/a, injected | `viewerTakesKey` dropped from the message view route in `internal/input/keyboard_terminal.go` | `TestViewersOverTheRailAndTheLeader` ("the leader did not work over the message view") | **caught** |
| The 512 byte cap on a notification's text | n/a, injected | drop `capText` from `showNotification` and from the pane path in `internal/app/notify.go` | none. Correct: the linear `wrapPlain` and the wrap cache keep an 8000 byte message fast on their own. The cap bounds memory and the log | **not caught alone** |
| The linear `wrapPlain` | n/a, injected | put back the quadratic `wrapPlain` from `origin/main`, cap kept | none. Correct: under the 512 byte cap the quadratic version is fast enough. The PR head row above removes both and is caught | **not caught alone** |
| An info flood pushes the errors out of the log | n/a, injected | `appendLog` never picks an info entry to drop | unit `TestLogKeepsErrorsThroughAnInfoFlood` ("the error was pushed out by info entries") | **caught** |
| A pane on one machine typed into a pane on another machine that waits on a prompt: typing verbs over a link skipped `typingRefusal` | the call site | make the `holdLinkTyping` branch at the top of `checkGrants` (`internal/session/pane_grants.go`) `if false && ...` | `TestAPaneOverALinkCannotAnswerAPrompt/without_respond` ("a hub pane typed into a prompt on build without respond", RESP_EXIT=0). Positive half: `with_respond`, the same call with `respond` in build's link policy, passes on both builds, and the person's own send-text over the link reaches the prompted pane | **caught** |
| A dock component that floods its stdout fills the client's heap: the engine read all of it with `cmd.Output` before it cut it to 64 KiB | whole change | build `origin/main` (`2121db1c`) and point `TUIOS_E2E_BIN` at it | `TestDockFloodingComponentStaysBounded` ("the client holds 562344 KiB while a dock component floods its stdout (budget 307200 KiB)", 0.13s into the flood). The fixed build peaks at 41 MiB in 3 of 3 runs | **caught** |
| The capped writer stops the flood but does not kill the command | n/a, injected | `kill` in the `dockCappedWriter` of `runOnce` in `internal/app/dock_engine.go` made a no-op | none. Correct: the failed write ends the copy, os/exec closes the pipe, and `yes` dies of SIGPIPE. The kill is for a command that ignores SIGPIPE, which costs CPU until the 3 s timeout and no memory | **not caught**, a redundancy |
| Bidi controls, C1 controls and invisible characters reach the host terminal from a dock cell | whole change | build `origin/main` (`2121db1c`) and point `TUIOS_E2E_BIN` at it; run the unit test against `origin/main`'s `dock_engine.go` | `TestDockComponentCannotSendUnsafeCharacters` (U+202E, U+009B and U+200B each on the wire); unit `TestDockSanitizeDropsUnsafeCharacters` (20 of 20 unsafe rows fail, 5 of 5 positive rows pass) | **caught** |
| The invisible characters, with the C1 check kept | the call site | drop `s = invisible.Strip(s)` from `dockSanitize` | `TestDockComponentCannotSendUnsafeCharacters` (U+202E and U+200B on the wire); unit, 15 rows (every bidi and invisible row) | **caught** |
| The C1 controls, with the invisible characters kept | the call site | drop `\|\| (r >= 0x80 && r <= 0x9f)` from `dockSanitize` | `TestDockComponentCannotSendUnsafeCharacters` (U+009B on the wire); unit, the 4 encoded C1 rows. The raw 0x9B byte row still passes, because the invalid UTF-8 check drops it | **caught** |
| A terminal resize put the master-stack panes back to equal shares: only the master ratio and the ratio of a stack of exactly two were kept | whole change | point `TUIOS_E2E_BIN` at `origin/main` (`524e2f46`) | `TestMasterStackKeepsSplitsAcrossAResize/grid` ("pane 0 is 75 columns wide, want about 61.2"), `/stack-of-three` and `/no-shared-borders` ("pane 2 is 14 rows tall, want about 7.9"); `/stack-of-two` passes on both, its positive half: that split was already kept | **caught** (3 of 3) |
| The same, the resize no longer written into the splits | n/a, injected, cuts the call site | drop the `m.setWorkspaceMasterSplits` call from `SyncMasterStackFromGeometry` in `internal/app/tiling_resize.go` | the same three cases, the same messages | **caught** (3 of 3) |
| Equalize splits did nothing in the master-stack layout: `EqualizeSplits` looked only for a BSP tree | whole change | point `TUIOS_E2E_BIN` at `origin/main` (`524e2f46`) | `TestEqualizeSplitsInMasterStack`, all four cases ("the daemon after the equalize: pane 0 is 48 columns wide, want about 60.0") | **caught** (4 of 4) |
| The same, the master-stack branch alone | n/a, injected, cuts the call site | make the `m.inMasterStack()` branch at the top of `EqualizeSplits` in `internal/app/tiling_bsp.go` unreachable | the same four cases, the same message | **caught** (4 of 4) |
| After a session switch, hiding the rail left its columns to nobody: the switch kept the layout generation of the session it left, so every resize answer of a session with a lower count was dropped as stale | whole change | point `TUIOS_E2E_BIN` at `origin/main` (`524e2f46`) | `TestSwitchedSessionTakesItsOwnRail` ("the client after the rail is hidden in bravo: want the rail shown=false and the first pane at column 0, got shown=false panes [2,28]") | **caught** |
| The same, the generation reset alone | n/a, injected, cuts the call site | drop the `c.sessionLayoutGen = 0` reset from `takeAttachReply` in `internal/session/tuiclient.go` | `TestSwitchedSessionTakesItsOwnRail`, the same message; `TestSwitchIntoASmallerSessionKeepsItsSize` passes, its positive half for the row below | **caught** |
| A switch into a session a smaller client holds drew the panes at the switching client's own size | whole change | point `TUIOS_E2E_BIN` at `origin/main` (`524e2f46`) | `TestSwitchIntoASmallerSessionKeepsItsSize` ("the wide client after the switch: the panes span 120 columns, want 80") | **caught** |
| The same, the box alone | n/a, injected | `rebuildForSession` in `internal/app/os.go` sets `EffectiveWidth` and `EffectiveHeight` to the client's own size after `RestoreFromState` again | `TestSwitchIntoASmallerSessionKeepsItsSize`, the same message; `TestSwitchedSessionTakesItsOwnRail` passes | **caught** |
| A workspace with resized panes kept the rectangles of a box the session had left: a session resize while it was off screen left its panes 120 columns wide in a session of 100 | whole change | point `TUIOS_E2E_BIN` at `origin/main` (`524e2f46`) | `TestCustomWorkspaceFollowsTheSessionSize/master-stack` and `/bsp` ("workspace 2 after the session shrank: the panes are 120 columns wide in all, want 100") | **caught** (2 of 2) |
| The same, the staleness term alone | n/a, injected, cuts the call site | drop `\|\| m.tiledLayoutStale()` from the retile condition in `SwitchToWorkspace`, `internal/app/workspace.go` | the same two cases, the same message | **caught** (2 of 2) |
| Two clients attach, resize, drag, change the rail and leave under smallest and largest | n/a, never broken | `origin/main` (`524e2f46`) | none, and that is correct: `TestTwoClientsKeepOneLayout` passes on both. It checks the session size, the daemon's rectangles, each shell's size against its pane, the user's shares, a leave reaching the remaining client within a second, and the dock's client count | **guard, not a control** |
| A config save read by the watcher half written: `os.WriteFile` truncates and then fills the file, and a writer descheduled between the two for more than the 200 ms debounce let the watcher apply an empty or cut file. The completed save was then dropped as tuios's own | n/a, injected: a scheduling gap made deterministic | on `origin/main` (`ecb525ad`), `writeConfigBytes` in `internal/config/save.go` opens the file with `O_TRUNC` and sleeps 400 ms before it writes the bytes | `TestChromeSetFromTheCommandLineRetilesThePanes` ("the daemon after the dock is hidden: a pane is at row 2 and 38 rows tall, want row 0 and 40 rows"); the same 400 ms sleep put between the temporary file and its bytes on the fixed tree passes, with the config watch, live appearance and settings tests | **caught** with the gap injected. The CI failures of this test end with the panes at row 0 and 38 rows tall, a different shape, so this race is not proven to be their cause |
| The client listing verb is not registered | n/a, cuts the call site | delete the `"list-clients"` entry from `verbRegistry` in `internal/session/verb_protocol.go` | `TestListClientsTracksSwitcherSwitches` (`list-clients failed: unknown verb list-clients.`) | **caught** |
| Client session switches are not published | n/a, cuts the call site | drop the `EventClientSessionChanged` publish from `handleAttach` in `internal/session/daemon_handlers.go` | `TestListClientsTracksSwitcherSwitches` (`subscribe did not print the attach event`) | **caught** |
| A client detach event does not name the session it left | n/a, cuts the call site | drop `Session` from the `EventClientSessionChanged` publish in `detachClient` in `internal/session/daemon_handlers.go` | `TestListClientsTracksSwitcherSwitches` (`first switch event = {... Session: ...}, want client ... leaving client-one`) | **caught** |
| A session rename does not tell its clients' readers the new name | n/a, cuts the call site | drop the publish loop over `moved` from `onSessionRenamed` in `internal/session/daemon.go` | `TestListClientsTracksSwitcherSwitches` (`subscribe did not print the rename event`) | **caught** |
| A client leave event looks the session name up after a kill-session deleted the session | n/a, injected | set `Session` to `d.sessionNameByID(sessionID)` in the leave publish of the disconnect path in `handleConnectionOn` (`internal/session/daemon.go`) | `TestListClientsTracksSwitcherSwitches` (`kill event = {... Session: ...}, want client ... leaving client-renamed`) | **caught** |
| The same, in `detachClient` | n/a, injected | the same lookup in the leave publish of `detachClient` (`internal/session/daemon_handlers.go`) | unit `TestDetachFromKilledSessionNamesIt` (`leave event session = "", want "work"`). The e2e test does not reach this path: the TUI client disconnects when its session is killed | **caught** |
| The rail's custom section absent from the layout map, so the parser kept the name and nothing drew it | n/a, injected, cuts the wiring | drop the `custom` entry from `sidebarSectionByName` in `internal/app/sidebar_layout.go` | `TestRailCustomSectionPlacementAndEditorRoundTrip` ("at attach: the rail never showed "Brief"") | **caught** |
| The section's command never reached the engine | n/a, injected, cuts the call site | drop the `comps = append(comps, rail)` from `InitDockComponents` in `internal/app/dock_runtime.go` | `TestRailCustomSectionRefreshModes` (all three, on the first wait: "once: the rail never showed "RUN-1"", "an interval of one second: the rail never showed "RUN-3"", "the run at attach: the rail never showed "RUN-""), `TestRailCustomSectionEmptyOnFailure/exit` ("before the failure: the rail never showed "GOOD"") | **caught** |
| Rows drawn past the section's lines | n/a, injected | `drawCustom` in `internal/app/render_sidebar.go` loops over the rows rather than the count | `TestRailCustomSectionCutsRowsToSize/long` ("the section hides rows and does not own up to them with +N below its title") | **caught** |
| Control sequences kept in the rows | n/a, injected, cuts the call site | `dockLines` in `internal/app/dock_engine.go` appends the raw line | unit `TestDockLinesStripsControlSequences` (`dockLines = "\x1b[31mRED\x1b[0m \x1b[2J\x1b]0;TITLE\aPLAIN\n..."`, want the SGR kept and the rest gone) | **caught by the unit test only**; `TestRailCustomSectionStripsControlSequences` still passes on this build, and so does a check of the bytes written to the host terminal, because the compositor drops the OSC and the erase. The e2e test keeps the SGR colour check as its positive half |
| A failed run kept the previous run's rows | n/a, injected | `applyUpdate` sets `text = c.text` instead of `text = ""` when `u.Err` is set | `TestRailCustomSectionEmptyOnFailure/exit` ("after the command failed: the rail still shows "GOOD"") | **caught**; the refresh after the fix is its positive half. Making the blank conditional on `!c.MultiLine` does nothing, because a failed run never carries text, so it is not a control |
| The run never told the focus or the size | n/a, injected, cuts the call sites | drop both `SetRailContext` calls (`syncRailContext` in `internal/app/sidebar_custom.go`, `InitDockComponents`) | `TestRailCustomSectionEnvFollowsFocus` ("after focusing SECOND: the rail never showed "ID=...") | **caught** |
| The command settable over the control protocol | n/a, injected | delete the skip and add a registry entry for `appearance.sidebar.custom.command` | unit `TestOptionRegistryCoversEveryScalarField` (skip alone: `config field "appearance.sidebar.custom.command" has no registry entry`, and the same for `.title` and `.refresh`); `TestRailCustomCommandIsNotSettable` ("set-config set the rail command:") with the entry | **caught**; placing the section with set-config is its positive half |
| An event mid-run dropped | n/a, injected, cuts the branch | drop the `Coalesce` branch from `fire` | `TestRailCustomSectionKeepsOnePendingRerun` ("after an event landed mid-run: the rail never showed "THREE""); unit `TestDockEngineCoalescesAnEventMidRun` ("the one pending re-run") | **caught**; the three-run count is its positive half |
| A focus moved by a state sync woke no focus component | n/a, injected, cuts the call site | drop the `NotifyDockEvent(AfterFocusChange)` block from `ApplyStateSyncFrom` in `internal/app/session.go` | `TestRailCustomSectionRefreshModes/event` ("after a focus change: the rail never showed "RUN-2""), `TestRailCustomSectionEnvFollowsFocus` ("after focusing SECOND: the rail never showed "ID=...") | **caught** |
| The per-message sync parsed the refresh on every message | n/a, injected | `railCustomWanted` in `internal/app/sidebar_custom.go` calls `railCustomRunnable()` instead of reading `railCustom.runnable` | unit `TestIdleTickRailCustomAllocatesNoMore` ("an idle tick allocates 6 times with the rail's custom section running and 5 without it") | **caught** |
| A folded or hidden rail ran the section's command with a width of zero, so five triggers while shut stopped it | n/a, injected, cuts the gate line | drop the `if rail.Width <= 0` skip from `runOnce` in `internal/app/dock_engine.go` | `TestRailCustomSectionWaitsWhileTheRailIsShut/folded`, `/off` and `/position-hidden` ("the command ran 5 times while the rail was shut, with widths [0 0 0 0 0], want none") | **caught** (3 of 3); the build before the fix fails the same three with the same message, and one focus change with the rail open, one run at 26, is the positive half |
| Opening the rail did not re-run the section, so it kept the rows from before it shut | n/a, injected, cuts the call site | drop the `m.dockEngine.Rerun(railCustomComponent)` call from `syncRailContext` in `internal/app/sidebar_custom.go` | `TestRailCustomSectionWaitsWhileTheRailIsShut/folded`, `/off` and `/position-hidden` ("after opening the rail: the rail never showed "AFTER"", with BEFORE still on the rail) | **caught** (3 of 3) |

| A pane got the client's `TERM` unchecked, so `xterm-kitty` from a kitty client broke `clear`, `tput` and curses programs on a machine without that terminfo entry | n/a, cuts the call site | `buildEnvFor` in `internal/session/session.go` puts `s.config.Term` (or `xterm-256color` when empty) into `TERM=` instead of `guestenv.PaneTerm(term)` | `TestPaneTermNeedsATerminfoEntry/absent` ("a client with TERM=tuios-e2e-no-such-term gave its pane TERM=tuios-e2e-no-such-term, want xterm-256color", "tput in the pane could not read its terminal: \"failed\""); `/host` and `/home` pass, its positive halves | **caught** |
| The terminfo lookup never finds an entry, so every pane gets the fallback | n/a, injected | `PaneTerm` in `internal/guestenv/terminfo.go` skips `hasTerminfo` and returns `FallbackTerm` | `TestPaneTermNeedsATerminfoEntry/host` ("TERM=screen-256color gave its pane TERM=xterm-256color"), `/home` ("TERM=tuios-e2e-home-term gave its pane TERM=xterm-256color"); `/absent` passes | **caught**, and it is the positive control on the row above |
| The daemon answered the kitty graphics query OK on a host that draws no kitty graphics, so the guest drew nothing instead of its text fallback | n/a, cuts the call site | drop the `kittyAdvertised` gate at the top of `kittyQueryResponse` in `internal/session/kitty_query.go` | `TestKittyQueryFollowsTheHostTerminal/daemon/plain-host` ("a host without kitty graphics, and the pane was answered as if it had them"); the standalone runs and `/daemon/kitty-host` pass | **caught** |
| Nothing records that an attached client draws kitty graphics | n/a, cuts the call site | drop `session.SetKittyAdvertised(kittyImages)` from `refreshTreeOps` in `internal/session/daemon_handlers.go` | `TestKittyQueryFollowsTheHostTerminal/daemon/kitty-host` ("a host that draws kitty graphics, and the pane was not told OK"); `/daemon/plain-host` passes | **caught**, and it is the positive control on the row above |
| DECRQM reported set for a mode the emulator does not implement, after the guest set it (`?1005`, `?1015`, `?3`, `?45`, `?9999`, ANSI 2) | n/a, cuts the call site | make the `modeRecognised` test in `handleMode` (`internal/vt/csi_mode.go`) unreachable | unit `TestConform_DECRQM` (six "set first" cases), `TestConform_ModesThisEmulatorIgnores` and `TestConform_CSIParameters` (the `unhandled` check) | **caught in `internal/vt`** |
| DECRQM ?2027 answered 1 or 2, though widths are always measured by grapheme cluster | n/a, injected | `?2027` back to `ansi.ModeSet` in `defaultModes` (`internal/vt/mode.go`) | unit `TestConform_DECRQM` (the three grapheme clustering cases) | **caught in `internal/vt`** |
| DA1 claimed 132 columns, selective erase, national and technical character sets and user windows | n/a, injected | `DeviceAttributes` in `internal/vt/sixel_report.go` back to `62;1;6;9;15;18;22` | unit `TestConform_DeviceAttributes` ("claims attribute 1 that this emulator does not implement", and 6, 9, 15, 18) | **caught in `internal/vt`** |
| During a restore a failed subscribe's `MsgError` answered the next pane's state request, because the client matched replies by type | n/a, cuts the call site | the welcome in `handshake` (`internal/session/tuiclient.go`) stores `false` into `c.requestIDs` instead of `welcome.RequestIDs` | unit `TestSubscribeErrorDoesNotAnswerAStateRequest` ("round 0: the state request for pane B was answered with \"get terminal state failed: PTY pane-a-gone not found\""), `TestRequestIDsAcrossPeerVintages/new_client,_new_daemon`; its plain-refusal positive half passes | **caught in `internal/session`** |
| A reply that came after its request timed out answered the next request, so pane B's screen was painted into pane C | n/a, injected | `routeReply` ignores `msg.ReqID` and `accept`, and `sendAndWaitMatching` registers by type only, which is the client before this change | unit `TestLateReplyIsNotTakenByTheNextRequest` ("pane C was given the state of pane B (width 6)"), `TestRequestIDsAcrossPeerVintages/new_client,_old_daemon` ("the client took the state of another pane"), and the row above's test | **caught in `internal/session`** |
| The daemon answers a tagged state request with an untagged error | n/a, cuts the call site | the PTY-not-found refusal in `handleGetTerminalState` (`internal/session/daemon_handlers.go`) back to `d.sendError` | unit `TestSubscribeErrorDoesNotAnswerAStateRequest` and `TestRequestIDsAcrossPeerVintages/new_client,_new_daemon` (both "timeout waiting for response" after 30 s) | **caught in `internal/session`** |
| Against a daemon without request ids, a stale snapshot about another pane is taken | n/a, cuts the call site | `GetTerminalState` passes `nil` instead of `forThisPane` to `sendAndWaitMatching` | unit `TestRequestIDsAcrossPeerVintages/new_client,_old_daemon` ("the daemon answered for pane stale-pa, not pane-b": the later check refuses it, so the request fails instead of waiting for its own reply) | **caught in `internal/session`** |

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

**The shutdown WaitGroup race** is a data race in the daemon. A subscribe
writes its ack and then starts the event streamer on the daemon's WaitGroup.
A client that read the ack and stopped the daemon at once made that `Add` run
concurrently with shutdown's `Wait`. The same reason as above keeps it out of
this suite. Coverage lives in
`internal/session/shutdown_waitgroup_test.go`
(`TestSubscribeDuringStopRegistersNoGoroutine`), run under `-race`. The control
put `d.wg.Go` back at the call site in `startPendingStream`, with the
`goTracked` helper left in place. The test failed in 20 of 20 runs with the
call site cut and passed in 20 of 20 with the fix.

Those are genuine gaps in *this* suite, not in the project's coverage.
The general lesson is that end-to-end screen assertions are the right tool for
bugs whose symptom is a wrong screen that persists, and the wrong tool for bugs
whose symptom is a narrow timing window or a memory race.

## Why the two-client chrome test catches nothing

Since the rail became session state, a current second client shows the rail
with the first, so the test runs its second client with
`TUIOS_SIDEBAR_LEGACY=1`, as a build from before that. Chrome that differs
between two clients is still possible against an older client, and the test
still guards that case.

`TestOneClientsRailDoesNotMoveAnotherClientsPanes` was written expecting to fail
with the agreed layout reserve removed, and it does not. It is kept as a
deliberate passes-both-ways control, and the reason is worth having written
down, because it says what this harness can and cannot see about multi-client
layout.

With the reserve removed, the two clients still reach the same frame, by
fighting to it. The client with the rail reads the other's rectangles as a
layout for somebody else's screen, works out its own and pushes it; the client
without one reads what comes back as settled, because a layout that sits inside
a wider box and still reaches its far edges cannot be told from one that belongs
there. So the wider client always yields and the frames converge.

What that convergence costs is not on the grid: each round trip resizes the
shared PTYs twice, once to the pushed rectangles and once back. That is what
damages scrollback, and it was counted rather than looked at by two
`internal/app` unit tests, which failed with the reserve removed (four resizes
per pane switch, and the two clients running the same shells at different
sizes). Both have since been removed; `TestSessionHoldsOneSizeForTwoClients`,
`TestGeometryConfigDisagreementDoesNotMovePanes` and the `internal/app`
convergence harness (`TestMultiClientConvergence`) now cover two clients
agreeing on pane sizes, and the reserve-removed mutation has not been rerun
against them. The frame test guards the property a resize count cannot see:
that what the two people are looking at is the same layout. It would catch a change that bought
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

## The ssh fallback tests failed on every Mac, and the cause was pgrep

`TestAttachOnAHostWithSSHFindsTuiosOutsideThePath` and
`TestAttachOnAHostWithSSHRunsTheFarTuios` passed on linux CI and failed on
every macOS run with "--ssh did not run the tuios found at ...". The product
was working: both tests had already seen the far tuios draw the session, and
failed only on the check after it, which proves which program drew it.

That check ran `pgrep -af PATTERN` and searched the output for a command line.
On linux (procps) `-a` means "print the full command line". On macOS and the
BSDs `-a` means "include the ancestors of each match", and pgrep prints bare
pids unless it is also given `-l`. So on a Mac the output held numbers and
nothing else, the positive assertions in those two tests could never pass, and
the negative assertion in `noNestedClient` (`host_attach_test.go`) could never
fail. The last one is the worse half: `TestAttachOnAHostIsDrawnByThisClient`
guards against a nested client and, on a Mac, would have let one through.

The three call sites now use `commandLinesContaining` in `harness_test.go`,
which reads `ps -A -ww -o args=`. That prints every process's untruncated
command line on both systems. The three rows above were run on macOS against
the fixed helper; each also passes on the unmodified build.

## The agent switch, `[agents] enabled = false`

`agents_off_test.go` was run against a build of origin/main, which has no
switch, and against this branch with one call site cut at a time.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| No switch at all | build origin/main and point `TUIOS_E2E_BIN` at it | `TestAgentsOffShowsNoAgentRow/off` (the rail lists the fake agent), `TestAgentsOffRefusesAgentCommands` (every command succeeds and start-agent opens a pane), `TestAgentsOffRespondGrantStillHolds` (A answers B's prompt), `TestAgentsSwitchAppliesOnReload` (the agents section stays) | **caught** (4 of 4) |
| Strict typing rule | `typingRefusal`: the `agentsOff` branch made `if false` | `TestAgentsOffRespondGrantStillHolds` (RESP_EXIT=0 and ANSWERED:y without respond) | **caught** |
| Verb refusal | `admitVerb`: the `agentsOffRefusal` call cut | `TestAgentsOffRefusesAgentCommands` (list-attention and send-agent-message succeed, no `agents_disabled`) | **caught** |
| Reload | `applyUserConfig`: the `SetAgentsEnabled` call cut | `TestAgentsSwitchAppliesOnReload` (get-agent-state still served after the switch went off) | **caught** |
| Detection tick | `agentMonitor`: the `agentsOff` skip cut | `TestAgentsOffShowsNoAgentRow/off` (the pane holds `working`) | **caught** |
| Client rail gate | `sidebarAgents`: the `agentsOn` term cut | none | **not caught** |
| Queue drop | `clearAgentsForOff`: the `dropAllQueued` call cut | `TestAgentsOffDropsTheQueue` (the message queued before the switch is typed after it) | **caught** |
| Inbox watcher after a start with the switch off | `applyAgentsSwitch`: start the watcher only when the switch moved | `TestAgentsOnAfterStartingOffStartsTheInbox` (the Inbox never shows the other session's prompt) | **caught** |
| Typing into a pane the caller opened | `typingRefusal`: the `offTypingAllowed` term cut | `TestAgentsOffTmuxShimTypesIntoItsOwnPane` (the shim's send-keys is refused) | **caught** |
| The switch is the person's | `verbSetOption`: the pane check made `if false` | `TestAgentsSwitchIsThePersons` (the pane's set-config exits 0) | **caught** |
| This machine's switch on a call to another | `VerbClient.CallWithTimeout`: the `HostCallGuard` check made `if false` | `TestAgentsOffHereRefusesACallToAnotherMachine` (list-agents on build:far succeeds) | **caught** |
| Agent keys in the help | `HelpCategories`: the `withoutAgentHelp` call cut | `TestAgentsOffShowsNoAgentRow/off` (the help search finds "Open the Inbox on its mail") | **caught** |
| Another machine's agent marks | `withHostGroups`: the `clearAgentMarks` call cut | `TestAgentsOffHidesAnotherMachinesAgentMarks` (the mark stays on build's row) | **caught** |
| get-config reads the switch | `verbGetOption`: the `agents.enabled` branch cut | `TestAgentsOffGetConfigSaysOff` (prints `true` with source `default` while every agent verb is refused) | **caught** |

Two fixes have no end-to-end test. Each closes a race that a test cannot
time:
- A `request-approval` admitted just before the switch goes off now closes
  its own hold.
- A detection pass that read the switch before it went off clears what it
  wrote.

The section editor and the length of the notice have no test.

The client rail gate is not caught because the daemon reads the same file
and holds no agent state, so the rail has nothing to list either way. The
gate matters only for a client whose config differs from its daemon's, which
this suite does not set up.

## max_fps up to 240, and auto

Each control cut one line, built the binary, and ran `TestMaxFPS` (the frame
rate test with `TUIOS_E2E_PERF=1`).

| Wiring | Cut | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The cap | `MaxFPSCap` back to 120 | `TestMaxFPSLoadsAndShows` (list-options says 120, the row never shows 240), `TestMaxFPSAutoReadsTheDisplay` (the row never shows `Auto (144)`) | **caught** |
| Detection at startup | `BindProgram`: the `detectDisplayRate` call cut | `TestMaxFPSAutoReadsTheDisplay` (the row never shows `Auto (144)`) | **caught** |
| The answer reaching the model | `Update`: the `displayRateMsg` case cut | `TestMaxFPSAutoReadsTheDisplay` | **caught** |
| No detection over SSH | `Detect`: the `Local` check cut | `TestMaxFPSAutoReadsTheDisplay` (the SSH client shows `Auto (144)`, not `Auto (60)`; the local client is the positive half) | **caught** |
| Binding the attached client | `session_commands.go`: the `BindProgram` call cut | `TestMaxFPSAutoReadsTheDisplay` | **caught** |
| A save writes a number bare and auto quoted | `MarshalUserConfig`: `EnableMarshalerInterface` cut | `TestMaxFPSLoadsAndShows` (the file never has `max_fps = 240`) | **caught** |
| The detected rate reaching the ticker | `handleDisplayRate`: the `applyFrameRate` call cut | `TestMaxFPS240DrawsPastTheOldClamp` (auto on a 240 Hz display draws 60 frames a second) | **caught** |
| The 120 clamp lifted | build origin/main and point `TUIOS_E2E_BIN` at it | `TestMaxFPS240DrawsPastTheOldClamp` (max_fps 240 draws 120 frames a second) | **caught** |

The frame rate test needs a machine that composes a frame in well under 4 ms,
so it runs only with `TUIOS_E2E_PERF` set. The served-client gate in
`detectsDisplay` (`ClientLocal`) has no control: the suite cannot run an SSH
server client next to a desktop.

## wait-for window-output slowed a flooding pane

A pending `wait-for window-output` captured the whole scrollback on every
output event. A flooding pane raises one event per PTY read. The perf budget
for this is a unit test in `internal/session`, because it times the daemon's
emulator and not a client. It runs the same flood with and without a waiter
in one fixture, so the run without the waiter is its positive half.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A capture per output event | `waitWindowOutput` in `internal/session/verb_subscribe.go`: the `waitOutputMinGap` coalescing cut, so the `sub.ch` case calls `matches()` on every event | `TestWaitForOutputDoesNotSlowFlood` ("a pending wait-for made the flood 3.37x slower", then 3.27x and 3.57x, against 0.92x to 0.98x with the fix) | **caught in `internal/session`** (3 of 3 run) |

## The tmux shim: prefixes, formats and the commands tools send

Each test in `tmux_shim_compat_test.go` writes the `tuios tmux` calls it made
and their output to `transcript.txt` in its artifact directory. The bug
fixes were cut at their call sites, one build each. The new commands were
checked against origin/main (a640011), which answers each of them with
`unknown command`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| No change at all | build origin/main and point `TUIOS_E2E_BIN` at it | all nine `TestTmuxShim*` tests in the file: `show-option`, `wait-for`, `run-shell`, `show-buffer`, `last-pane` and `display-popup` are unknown commands, `pane_pid` is empty, `select-pane -L` needs a client, and the control client answers for `e2e-ctrlp` | **caught** (9 of 9) |
| Command prefixes | `lookupCommand`: a name matches only when it is the whole word | `TestTmuxShimCommandPrefixes` (`unknown command: show-option`) | **caught** |
| pane_pid and pane_tty | `addOnePaneMeta`: the `pid` and `tty` lines cut | `TestTmuxShimPaneFormats` (`pane_pid = ""`) | **caught** |
| history-limit printed `""` | `historyLimit`: the string case cut and `""` returned when the daemon does not say, as before | `TestTmuxShimPaneFormats` (`history-limit = ""`) | **caught** |
| select-pane direction on a detached session, and from the target | `selectPane`: the `selectDirection` call replaced by the old `focus-window` direction call | `TestTmuxShimMovesPanes` (needs an attached client), `TestTmuxShimSelectPaneDirection` (`no window right of the focused one`) | **caught** (2 of 2) |
| Control mode used the wrong session | `runControl`: the `s.attached = c.session` line cut | `TestTmuxShimControlModeTargetsItsSession` (answers for `e2e-ctrlp`) | **caught** |
| set-environment reaches new panes | `splitWindow`: `paneEnvFor` cut from the pane's environment | `TestTmuxShimBuffersAndEnvironment` (the pane sees `/`) | **caught** |

`TestExpandMatchesTmux` in `internal/tmuxcompat` is the wire-compatibility
table for the format language: every expected string is what tmux 3.4
printed for the same format and values. It has no e2e counterpart.

## Pane input encoding: mouse forms, modifyOtherKeys, kitty flags per screen

Each control removed one fix from the current tree, built the binary, and ran
the named test, standalone and against a daemon. Each test has a positive
half in the same fixture: the SGR step for the mouse forms, the plain and
kitty steps for modifyOtherKeys, and the main-screen step for the kitty flags.

| Fix | Cut | Tests that fail | Verdict |
| --- | --- | --- | --- |
| Mouse reports in the X10, UTF-8 (1005) and urxvt (1015) forms | `mouseReport.encode` in `internal/vt/mouse_encode.go`: every non-SGR encoding sent through the old `ansi.MouseX10` call | `TestPaneMouseReportEncoding/standalone` and `/daemon`: the x10 step reads column 101 as UTF-8 and column 231 as BEL, the utf8 and urxvt steps read the X10 form. The sgr step passes | **caught** (2 of 2 run) |
| modifyOtherKeys reaching the pane | `paneKeyBytes` in `internal/input/pane_key.go`: the `EncodeModifyOtherKeys` call cut | `TestPaneModifyOtherKeys/standalone` and `/daemon`: the level1 and level2 steps read the legacy bytes. The plain and kitty steps pass | **caught** (2 of 2 run) |
| A kitty flag stack for each screen | `setAltScreenMode` in `internal/vt/csi_mode.go`: the `SetAltScreen` call cut | `TestPaneKittyFlagsPerScreen/standalone` and `/daemon`: the alt step reads `CSI 97 ; 5 u`. The main step passes | **caught** (2 of 2 run) |
| modifyOtherKeys turned off while the pane is hidden | `ApplyTerminalState` in `internal/session/session.go`: the `ModifyOtherKeysKnown` term cut, so only a level above 0 is restored | `TestPaneModifyOtherKeysOffWhileHidden`: the pane reads `CSI 27 ; 5 ; 97 ~`, want `0x01`. `TestPaneKittyMainStackAcrossAttach` passes | **caught** (1 of 1 run) |
| The main screen's kitty stack across an attach under the alternate screen | `ApplyTerminalState` in `internal/session/session.go`: the `RestoreKittyKeyboardMainStack` call cut | `TestPaneKittyMainStackAcrossAttach`: the pane reads `0x01`, want `CSI 97 ; 5 u`. `TestPaneModifyOtherKeysOffWhileHidden` passes | **caught** (1 of 1 run) |
| The 1005 form writes a button of 128 or more (back, forward) as UTF-8, as xterm does | `mouseReport.encode` in `internal/vt/mouse_encode.go`: the button written as one raw byte again | unit `TestConform_MouseEncoding` ("UTF-8 form, the back button is UTF-8" and "forward with ctrl"). The X10 back button case passes, its positive half | **caught in `internal/vt`** |

## Turn checkpoints and undo

Each test in `checkpoint_test.go` writes the `tuios` calls it made and their
output to `transcript.txt` in its artifact directory. Each control cut one
call site from the current tree, built a binary, and ran the named test
against it.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| No checkpoint at the end of a turn | the session event sink: the `noteCheckpointEvent` call cut | `TestCheckpointUndoesATurn` ("after turn 1 the pane has 0 checkpoints, want 1") | **caught** |
| No safety checkpoint before a restore | `verbRestoreCheckpoint`: the safety `SaveCheckpoint` call replaced by the newest checkpoint | `TestCheckpointUndoesATurn` (the restore names checkpoint 2 as the undo, and the list has no safety checkpoint 3) | **caught** |
| The restore uses the person's index | `RestoreTree`: the temporary `GIT_INDEX_FILE` dropped from `read-tree` and `checkout-index` | `TestCheckpointUndoesATurn` (the index holds `notes.txt` after the restore) | **caught** |
| A removed worktree keeps its checkpoints | `verbRemoveWorktree`: the `dropCheckpointsOf` call cut | `TestCheckpointsGoWithTheirWorktree` (both refs are still there) | **caught** |
| A restore writes over an ignored file | `RestoreTree`: the `blockingPath` check absent (the tree before the fix) | `TestCheckpointRestoreKeepsIgnoredFiles` (the restore succeeds, and `.env` holds turn 1's text) | **caught** |
| A refused restore keeps its safety checkpoint | `verbRestoreCheckpoint`: the `DeleteCheckpoints` call cut from the `OverwriteError` refusal | `TestCheckpointRestoreKeepsIgnoredFiles` (the list holds a safety checkpoint 3 after a refusal that says nothing was changed) | **caught** |
| A large untracked file in a checkpoint | `SaveCheckpoint`: snapshots with no exclusions and records nothing left out (the tree before the change) | `TestCheckpointLeavesOutLargeUntrackedFiles` (the 2 MiB file is in the diff and the object store, and the list names nothing) | **caught** |

The positive halves are in the same tests: the refused restore while the
agent works runs after restores that succeed, and the main checkout's
checkpoint stays after the worktree's goes. The pruning to `keep` and the
refusal of a checkpoint taken in another work tree have no end-to-end test.

## Copy-mode search columns in the scrollback

Copy-mode search now reads the history as text cells and takes a match's
columns from the cells. It used to count runes as columns, so a cell of
several runes before a match moved the cursor right of it. The ASCII needle
in the same fixture is the positive half: the column check passes on both
builds for a line of one rune a cell. The memory budgets for the search and
for the history save are unit tests in `internal/input` and
`internal/session`, because they measure bytes and not a screen.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| Search columns counted in runes | whole change: build the tree before it and point `TUIOS_E2E_BIN` at it | `TestCopyModeSearchColumnInScrollback` ("needle-cm: the copy cursor is at row 10 column 40, want the match's column 36"); the `needle-ascii` half passes | **caught** |
| A search key decoded every history line | whole change, same build | unit `TestCopyModeSearchKeyBudget` ("one search key allocated 258.1 MB, the budget is 4 MB") | **caught in `internal/input`** |
| A history save decoded its rows under the pane's lock | whole change, same build | unit `TestHistoryCaptureBudget` ("the capture allocated 24.7 MB under the pane's lock, the budget is 4 MB") | **caught in `internal/session`** |
| The line cache bounded by its line count only | whole change, same build | unit `TestScrollbackCacheIsBounded` ("cache holds 102400 decoded cells after a walk of a 400-column ring, want at most 65536") | **caught in `internal/vt`** |

## Push notifications for the Inbox, `[notify]`

Each control cut one call site from the current tree, built the binary, and
ran `TestNotify` (the four tests in `notify_test.go`). The fake provider is
an HTTP server in the test process that stands in for ntfy, Pushover and a
webhook. Each test has its positive half in the same fixture: the first test
proves the line changed (`list-attention` shows the new line) before it counts
that nothing more was sent, the quiet test sends an approval after the client
left, and the held test sends the held approval itself.

`TestNotifyPushesAnApprovalNobodyIsLookingAt` sets `cooldown_seconds = 0`.
With the default cooldown of 60 seconds, the de-dupe control passed: the
cooldown for the same pane and kind held back the second send on its own, so
the test did not reach the per-item check it names. With no cooldown the
per-item check is the only thing in the way, and the control fails.

| Fix | Cut | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The daemon sends a notification for an Inbox item | `d.notify.start()` in `Daemon.Start` (`internal/session/daemon.go`) | `TestNotifyPushesAnApprovalNobodyIsLookingAt` (no request after the approval), `TestNotifyHoldsWhileAPersonTypesAtAClient` (no request after the person left), and `TestNotifySendsAHeldItemWhenThePersonStaysAway` (no request after the quiet time) | **caught** (3 of 3) |
| One notification per item | `if n.sent[it.ID] { return }` in `pushNotifier.note` | `TestNotifyPushesAnApprovalNobodyIsLookingAt`: 6 requests after the repeated reports and the changed line, want 3 | **caught** (with `cooldown_seconds = 0`; not caught at the default cooldown, see above) |
| Quiet while a person types at a client | the quiet test in `pushNotifier.considerLocked` made false | `TestNotifyHoldsWhileAPersonTypesAtAClient`: 3 requests while the client was active, want 0 | **caught** |
| A key typed into a pane counts as activity | `cs.lastInput.Store(...)` in `Daemon.handleInput` (`internal/session/daemon_handlers.go`) | `TestNotifyHoldsWhileAPersonTypesAtAClient`: 3 requests while the client was active, want 0 | **caught** |
| A held item is sent when the person stays away | `n.armLocked(...)` in `pushNotifier.considerLocked` | `TestNotifySendsAHeldItemWhenThePersonStaysAway`: no request after the quiet time | **caught** |
| A notification's link opens the Inbox in tuios-web | `daemonOpts.OpenInboxItem = sessionInboxItem(...)` in `createTUIOSHandler` (`cmd/tuios-web/main.go`) | `TestNotificationLinkOpensTheInboxOnItsItem` in `cmd/tuios-web` (main module, run with `go test`): the browser with the cookie never shows the Inbox. Its positive half is the browser with no cookie, which shows no Inbox | **caught** |
| Notifications need no curl | whole change: build the tree with the curl `internal/pushnotify/send.go` and `pushnotify.go` and point `TUIOS_E2E_BIN` at it | `TestNotifyNeedsNoCurl` ("after the approval, with no curl: want 1 request per provider, got ntfy 0, pushover 0, webhook 0"). The other three `TestNotify` tests pass on that build, since curl is on PATH for them. Positive half: the same test passes on the net/http build | **caught** |

The test of a secret in the daemon log reads `daemon.log` at
`TUIOS_LOG_LEVEL=trace` and first checks that the log records the ntfy send,
so a log that is empty or in another place fails it. No control injects a
secret into the log. `TestNotifySecretsStayOutOfDumps` in `internal/config`
is the unit test of the redaction itself. It caught a real leak while it was
written: fmt prints a pointer field with `%s` or `%q` through its error path,
which skips `String` and `Format`, so a token in `[notify.ntfy]` printed whole.
The provider tables now format themselves.

## Shipping a worktree: commit, merge, push, pull request

The tests in `ship_test.go` use a throwaway repository with a bare
repository as its origin, a fake `gh` on `PATH` that records its arguments
and answers with canned JSON, and a fake `gpg` that signs anything. Each
writes its `tuios` calls to `transcript.txt`, and the main test writes the
fake `gh`'s calls to `gh-calls.txt`. Each control cut one line from the
current tree, built a binary, and ran the named test against it.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A conflicting merge left in place | `abortMerge`: the `git reset --merge` call cut | `TestShipFromWorktreeToPullRequest` (the main checkout changed, is not clean, has `MERGE_HEAD`, and README holds conflict markers) | **caught** |
| A push without a confirmation | `shipOutboundGate`: the confirm token check made false | `TestShipFromWorktreeToPullRequest` ("the unconfirmed push reached the origin: refs/heads/feat/ship") | **caught** |
| The poll not started on attach | `handleAttach`: the `kickPRPoll` call cut | `TestShipFromWorktreeToPullRequest` (the badge stays `PR #7 open pending` after gh says the checks passed) | **caught** |
| A commit that is not the person's | `CommitAll`: the commit run with the checkpoint identity and `--no-gpg-sign` | `TestShipFromWorktreeToPullRequest` (the commit is by `tuios`, and has no `gpgsig`) | **caught** |
| A push from a pane with no question | `shipCallerIsPerson`: answers true for every caller | `TestShipPushFromAPaneAsksThePerson` (the wait for the Inbox question times out, and the push has already gone out) | **caught** |
| A push sends the branch as it is after the question | `worktree.Push`: pushes `refs/heads/<branch>` instead of the resolved commit (the tree before the fix) | `TestShipPushSendsTheCommitThePersonAllowed` (the origin gets the commit made after the question was put) | **caught** |
| The question names only the remote | `shipQuestion`: asks "to origin?" with no address (the tree before the change) | `TestShipPushQuestionNamesWhereThePushGoes` (the wait for `elsewhere.example/someone/else.git` in the question times out) | **caught** |
| `fan keep --merge` that does not merge | `verbKeepFan`: the `if p.Merge` block made false | `TestFanKeepMergesTheKeptAttempt` (the keep into a dirty main checkout is not refused) | **caught** |

The positive halves are in the same tests: the commit refused while the
agent works is followed by one that succeeds, the push refused without a
confirmation is followed by one with `--yes` that reaches the origin, the
conflicting and dirty merges follow a fast-forward that lands, and the
refused keep is followed by one that merges. A push that is not a
fast-forward, a squash or ff-only merge, a pull request that is already
open, and gh missing or not logged in have no end-to-end test.

## The way out of the spotlight

People who turned the spotlight on by mistake saw a short message and then a
dimmed screen with no way out on it. The dock now shows a Spotlight chip with
the key that turns it off. Esc turns it off in window mode. In terminal mode
esc stays with the program, and leader, B turns it off. A click on the chip
turns it off in any mode. The window-mode key moved from `b` to `B`.

All seven tests in `spotlight_exit_test.go` fail on origin/main at b0cd61cb,
because that build draws no chip. For `TestEscReachesTheProgramWhileTheSpotlightIsOn`
that failure is only the positive half: esc already reached the pane there. The
injected controls below change one call site each from the current tree, built the
binary, and ran the named tests.

| Fix | Cut | Tests that fail | Verdict |
| --- | --- | --- | --- |
| Esc turns the spotlight off in window mode | the `TurnOffSpotlight` call in the esc block of `HandleWindowManagementModeKey` (`internal/input/keyboard_wm.go`) | `TestEscTurnsTheSpotlightOffInWindowMode`, `TestEscClosesAnOverlayBeforeTheSpotlight` ("did not turn the spotlight off") | **caught** |
| An open overlay takes esc before the spotlight | the esc check moved to the top of `HandleWindowManagementModeKey`, ahead of the overlays | `TestEscClosesAnOverlayBeforeTheSpotlight` ("esc did not close the palette") | **caught** |
| A click on the chip turns the spotlight off | the `SpotlightChipAt` branch in the dock click path (`internal/input/mouse_click.go`) | `TestClickingTheSpotlightChipTurnsItOff` | **caught** |
| Leader, B turns it off in terminal mode | the `prefix_toggle_spotlight` default binding (`internal/config/userconfig.go`) | `TestTheLeaderChordTurnsTheSpotlightOffInTerminalMode` (the chip says "click turn off", and the chord does nothing) | **caught** |
| Esc in terminal mode reaches the program | esc in `keyboard_terminal.go` turns the spotlight off and is not forwarded, which is what a double-esc exit does to the second esc | `TestEscReachesTheProgramWhileTheSpotlightIsOn` ("the two escs did not reach cat -v") | **caught** |
| A single esc in terminal mode reaches the program without delay | `HandleTerminalModeKey` (`internal/input/keyboard_terminal.go`) sleeps 300 ms on esc while the spotlight is on, which is the hold a double-esc exit needs | `TestEscReachesTheProgramWhileTheSpotlightIsOn` ("a single esc took 365ms to reach the pane, a plain key 16ms; esc is held back") | **caught** |
| The chip is not dimmed | `applySpotlight` passes no lit spans (`internal/app/spotlight.go`) | `TestSpotlightKeyShowsTheChipAndTheWayOut` ("the chip label is dimmed") | **caught** |
| The on message says how to turn it off | `spotlightMessage` returns "Spotlight is on." (`internal/app/spotlight_exit.go`) | `TestSpotlightKeyShowsTheChipAndTheWayOut` | **caught** |

## Mode legends in the dock, and messages cut at a word

Hints mode said its keys in a message, and the dock cut it in the middle of a
word: at 80 columns the base build shows `Type a label to copy. Shift+labe…`,
and once the message burnt down the dock showed no keys at all while hints
mode stayed open. Copy mode, multi copy mode and hints mode now show a legend
in the dock, fitted by whole keys. Each control cut one call site from the
current tree in a copy of it, built the binary, and ran the named tests. The
positive half of the fitting control is the 200-column run, which passes on
the broken build because the whole legend fits there. The positive half of the
word-cut control is the 80 and 81 column runs, where a cut by width falls on a
word boundary by chance and passes.

| Fix | Cut | Tests that fail | Verdict |
| --- | --- | --- | --- |
| Hints mode shows its keys in the dock | the `m.hints != nil` branch in `modeLegend` (`internal/app/mode_legend.go`) | `TestHintsModeLegend` 60x20, 80x24 and 200x50 ("no hints legend on the dock") | **caught** (3 of 3) |
| The legend is fitted by whole keys, and esc stays | `renderModeLegend` draws `overlay.HintStrip` unfitted, so the dock's generic cut ends it | `TestHintsModeLegend/80x24` ("the dock shows "ope", which is not a whole key or label", "the dock does not show esc"), `/60x20` ("the dock lost the key "esc""); `TestCopyModeLegend/60x20` and `/80x24` (no legend with its exit key); both 200x50 runs pass | **caught** |
| A cut message ends on a whole word | `notifFit` back to `strings.TrimRight(truncateToWidth(...), " ")` without `notifWordCut` (`internal/app/render_dock_notification.go`) | `TestCutMessageEndsOnAWholeWord/82x24` ("word 6 of the cut message is "1", want 106"), `/83x24` ("is "10""); 80 and 81 pass | **caught** (2 of 4 widths, as designed) |
| `?` in hints mode shows all of its keys | the `r == '?'` branch in `handleHintsKey` (`internal/input/hints_input.go`) | `TestHintsHelpKeyListsAllKeys` ("? did not open the help on hints mode's keys") | **caught** |

Copy mode also said its keys in a message ("Copy mode (hjkl, q to exit)"),
which held the dock's right end over the legend for twice the message time.
No control is recorded for its removal. The legend tests wait up to the UI
timeout, which is longer than the message lasts, so they pass with it put back.
Five tests waited for that message as the sign that copy mode opened, and now
wait for `y yank` in the legend. Two of them, `TestScrollbackModeShowsEarlierOutput`
and `TestSessionSwitchKeepsScrollback`, were missed at first and failed on
every run of the whole suite until they were changed too.

## Opening links, OSC 8 in the frame, and the URL detector

Each control cut one call site from the current tree, built the binary, and
ran the named test against it. The opener in these tests is a script set
through `appearance.link_opener` that appends its argument to a file, so a
link that opened is a line in that file and a link that was refused is none.
The positive halves are in the same fixtures: the refused file and script
links come before a shift+click that must open, the `link_click = "ctrl"`
test ends with a ctrl+click that must open, and the markdown test copies a
Wikipedia URL that keeps its brackets.

| Fix | Cut | Tests that fail | Verdict |
| --- | --- | --- | --- |
| Ctrl+click opens a link (shift+click never reaches tuios in most terminals) | whole change: build the tree before it | `TestCtrlClickOpensLinksWithTheirTarget` ("ctrl+click on an OSC 8 label: the opener was started with [], want [https://example.com/real-target]") | **caught** |
| The ctrl+click release opens the link | `handleMouseRelease` in `internal/input/mouse_release.go`: the `CtrlClickLink` branch made unreachable | `TestCtrlClickOpensLinksWithTheirTarget` (same message) | **caught** |
| A focused pane sends its links to the outer terminal as OSC 8 | the cell loop of `renderTerminal`: the link transition made unreachable, which is the old behaviour | `TestCtrlClickOpensLinksWithTheirTarget`: no OSC 8 for the labelled link, the `id=` link, the bare URL and the wrapped bare URL | **caught** (4 of 4 assertions) |
| A logical line with "://" is drawn again with its bare URLs as OSC 8 | the cell loop of `renderTerminal`: `lineHasScheme` never set | `TestCtrlClickOpensLinksWithTheirTarget`: no OSC 8 for the bare URL and the wrapped bare URL. The two marked links pass, which is the positive half | **caught** |
| A `file://` link with a foreign host does not open here | `linkFilePath` takes any host as this machine | `TestCtrlClickOpensLinksWithTheirTarget` (the link opens `/etc/hostname` in a new editor pane, which covers the fixture: "\"script link\" is not on screen") | **caught** |
| A `javascript:` link does not reach the opener | `"javascript"` added to `linkOpenSchemes` | `TestCtrlClickOpensLinksWithTheirTarget` (the record holds the script address) | **caught** |
| Hovering one run of an `id=` link lights every run | `hoverByID` forced false in `renderTerminal` | `TestHoverLightsEveryRunOfAnIDLink` (the wait for part-two's underline times out) | **caught** |
| `link_click` is read | `linkClickAllows` returns true for shift under `ctrl` | `TestLinkClickSettingIsHonoured` (the record holds the URL twice) | **caught** |
| A closing bracket the URL did not open ends it | `urlEnd` in `internal/hints/urls.go`: the `)` and `]` cases cut | `TestHintsFindURLsInMarkdown` (the clipboard got `https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/B0B81N8V1R)`) | **caught** |
| `</p>` is not a path | the rooted-path check in the path pattern made false | `TestHintsFindURLsInMarkdown` ("column 1 of \"</p>\" is drawn as a hint label") | **caught** |
| A bare URL cut by the viewport opens its whole address | `link_hover.go`, `link_emit.go` and `render_terminal.go` in `internal/app` as before the fix, so the line is read from the rows on screen only | `TestACutURLOpensWhole`: the click on the tail of a URL whose head is in the scrollback opens nothing. With that click left out, the click on the head of a URL whose tail is below a scrolled-back pane opens the address cut at the pane's last row | **caught** (2 of 2 halves) |
| A daemon snapshot carries OSC 8 targets | n/a, never broken | none: `TestLinkOpensFromARehydratedPane` passes on both builds | **guard, not a control** |

## The session-list poll tick composes no frame

The poll tick stopped composing a frame to save idle CPU. The rail draws
other sessions from the listing the tick's refresh fetches, and that refresh
answered with no message, so a change in another session reached the screen
only when something else drew a frame. The refresh now answers with
`foreignSessionsChangedMsg` when the listing changed. The control is the
tree with the tick change and without that message.

| Fix | Cut | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A changed listing draws a frame | `refreshForeignSessionsCmd` in `internal/app/update.go` returns nil after the refresh, as before the fix | `TestSubagentCountOnARestingRow` ("waiting for the count on the finished row": the daemon holds `3 subagents` and the rail never shows it), 3 of 3 runs. a640011 passes, since its tick drew a frame | **caught** (3 of 3 run) |

## A loaded layout shows its shells (#411)

`TestALoadedLayoutShowsItsShells` saves a layout of three panes and loads it
on an empty workspace of a daemon session.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released bug | build v0.8.5 and point `TUIOS_E2E_BIN` at it | `TestALoadedLayoutShowsItsShells` (0 of 3 prompts on workspace 2) | **caught** |
| The stream reconcile after a sync | `ApplyStateSyncFrom`: the `reconcilePaneStreams` call cut | `TestALoadedLayoutShowsItsShells` (0 of 3 prompts on workspace 2) | **caught** |

The fix is e1af811a (#384), which landed after v0.8.5. The panes of a loaded
layout reach the client in a state sync, and before that fix nothing
subscribed a pane that arrived that way.

## Copy path on a folder row (#414)

Each control cut one line, built the binary, and ran
`TestSidebarFolderCopyPath`.

| Wiring | Cut | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The menu row | `fileRowMenu`: the Copy path row for a folder made `if false` | `TestSidebarFolderCopyPath/standalone` and `/daemon` (the menu never shows Copy path) | **caught** |
| The action | `handleSidebarFileAction`: the `file_copy_path` case cut | `TestSidebarFolderCopyPath/standalone` and `/daemon` (the menu row writes nothing) | **caught** |
| The key | `getDefaultSidebarFilesKeybinds`: `Y` cut | `TestSidebarFolderCopyPath/standalone` and `/daemon` (`Y` on the listing writes nothing) | **caught** |

## The scratch group before a daemon restart

`TestScratchGroupSurvivesADaemonRestart` typed into the scratch group as soon
as it pressed the scratch key. The group opens through the daemon, so the
text could reach the pane that had the focus before. The hide then left that
marker on screen, and the test failed (main, run 37025035958). The test now
waits for each pane of the group before it types.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A slow scratch open | `createScratch`: sleep 1.5 s before `scratchOpener`; run the test as it was | `TestScratchGroupSurvivesADaemonRestart` ("the group hidden before the restart: [PANEONE-2 PANETWO-5] still on screen"), the same message as CI. With the waits: passes with the sleep, and 10 of 10 without it | **caught** |

## Back from an empty workspace (discussion #273)

The tests are in `empty_workspace_test.go`. Each control built the binary and
ran the five `TestEmptyWorkspace` tests or the one named.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (c35d2942) and point `TUIOS_E2E_BIN` at it | `TestEmptyWorkspaceReturnsAfterXpanesSuperSpeedy`, `TestEmptyWorkspaceReturnsAfterXpanesSpeedy` and `TestEmptyWorkspaceReturnsWhereXpanesRan` ("the session shows workspace 2, want 1"), `TestEmptyWorkspaceWalksBackAChain` ("the session shows workspace 3, want 2") | **caught** |
| The xpanes origin | `runXpanes`: `params["return_to"] = origin` cut | `TestEmptyWorkspaceReturnsWhereXpanesRan` ("the session shows workspace 3, want 1") | **caught** |
| The setting | `returnsWhenEmptyLocked` returns true first | `TestEmptyWorkspaceStaysWhenTheSettingIsOff` ("the session went to workspace 1 with return_when_empty off, want 2") | **caught** |

`TestEmptyWorkspaceStaysWhenTheSettingIsOff` passes on origin/main, because
it holds the old behaviour.

## CLI output for scripts, and names that do not exist

The tests are in `cli_scripting_test.go`. One build cut the three checks
below at once. Each assertion reads one of them, so each failure names its
cut.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (4f6232b3) | `TestCLIListsPrintJSON` (`layout list --json` exits 1 on the unknown flag), every `TestCLIRefusesNamesThatDoNotExist` case (`layout delete nope` and `--preview-theme nope` exit 0, `layout export` and `tape show` do not name the list command) | **caught** |
| A missing layout | `layout delete`: the `findLayoutTemplate` check made `&& false` | `TestCLIRefusesNamesThatDoNotExist/layout_delete_nope` (exits 0) | **caught** |
| An unknown theme | `previewThemeColors`: the `AvailableThemes` check made `false &&` | `TestCLIRefusesNamesThatDoNotExist/--preview-theme_nope` (prints the default palette and exits 0) | **caught** |
| A value only recorded | `runSetConfig`: the `Not applied` print cut | `TestCLIListsPrintJSON` (stderr is empty with no client attached) | **caught** |

## The complete keybinds list (#434)

The test is `TestKeybindsListShowsEveryScope` in `keybinds_list_test.go`. One
build made both cuts. Each assertion reads one of them.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (4f6232b3) | `TestKeybindsListShowsEveryScope` (`keybinds list --json` exits 1 on the unknown flag) | **caught** |
| The rail files scope | `keybindRows`: registry bindings in `sidebar.files` skipped | `TestKeybindsListShowsEveryScope` (no `file_copy_path` row on `Y`) | **caught** |
| Actions with no key | `keybindRows`: the loop over unbound actions made to range over nothing | `TestKeybindsListShowsEveryScope` (no `close_workspace` row) | **caught** |

The unit test `TestKeybindsListCoversEveryActionAndDefault` in `cmd/tuios`
checks every action and default key on Linux and on macOS. It catches the
same two cuts.

## A dock component a switch turns off

`list-dock-components` listed the clock and the meters as drawn when
`show_clock`, `show_cpu` or `show_ram` kept them off the bar.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| No switch check | `dockSwitchedOff` returns `""` | `TestDockListSaysWhichSwitchTurnsAComponentOff` ("cpu: off = \"\", want \"show_cpu = false\"", then the same for clock) | **caught** |

## Subscribe types and close-window

The tests are in `event_types_test.go`. Each control is its own build.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| Unknown types accepted | the `checkEventTypes` call in `verbSubscribe` made `false &&` | `TestSubscribeRefusesUnknownTypes` (both `window-creted` and `after-new-window` subscribe, stream nothing and are killed at the deadline) | **caught** |
| The window sent empty | `runCloseWindow` dials with `dialSessionTarget` and puts the name in `params`, which overwrites it with the empty target window | `TestCloseWindowClosesOnePane` (the focused pane, `keep`, is closed and `build` is still listed) | **caught** |

The positive halves are in the same tests: `--types window-created` streams
the event of a new window, and `keep` is still listed after `build` closes.

## Tapes: actions, condition waits, failures and recording

The tests are in `tape_exec_test.go`. Each control is its own build, made by
cutting one call site from the tree the tests were written against.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| WaitFor never armed | the `CommandTypeWaitFor` case cut from the player's tick in `internal/app/update.go` | `TestTapeExecPlaysActionsAndWaits` (WaitFor reaches `Execute`, which refuses it, and exec exits 1) | **caught** |
| Action never run | the `CommandTypeAction` case cut from `CommandExecutor.Execute` | `TestTapeExecPlaysActionsAndWaits` (exec exits 1 at the Action line) | **caught** |
| A timed-out wait does not fail the tape | the `failScript` call cut from `checkScriptWait` | `TestTapeExecStopsAtTheFailedLine` (exec exits 0, and the line after the wait runs) | **caught** |
| A failure placed on the sent text | `script.Locate` cut from `runTapeExec` | `TestTapeExecStopsAtTheFailedLine` (the message names line 3, not `lib.tape line 2`) | **caught** |
| run-command knows only tape commands | the `IsActionName` fallback in `resolveCommandName` made `false &&` | `TestRunCommandRunsAnyAction` (`run-command open_settings` exits 1) | **caught** |
| A recording drops actions | the Action branch of `Recorder.RecordAction` made to return | `TestTapeRecordingReplaysActions` (the saved tape has no `Action open_settings`) | **caught** |
| SnapFullscreen snaps to a quarter | the tree before the `SnapByDirection` fix | `TestTapeSnapFullscreenFillsTheScreen` (the window is 60x19 of 120x40) | **caught** |

`TestExampleTapeRuns` plays `examples/actions_and_waits.tape` and has no
control of its own: it fails on any of the first two cuts, since the example
uses both.

## What this harness structurally cannot observe

Some things cannot be simulated from here at all. They are listed so that nobody
writes a helper for them, watches it pass, and believes it.

**Pointer motion outside a drag.** `app.ProgramOptions` (internal/app/program_options.go), which every client runs with, installs
`tea.WithFilter(app.FilterMouseMotion)`, a whitelist that discards every
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

The frame check in `TestWideRuneInHistorySurvivesANarrowPane` reads the
pane's border cell, and the last column's background in one layout. It
cannot see a style lost at the edge in every layout, because the frame cuts
an overlong row in a way that depends on the render path. The render test in
`internal/app` is the check for that.
