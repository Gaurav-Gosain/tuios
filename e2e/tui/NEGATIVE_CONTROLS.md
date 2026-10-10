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

## Paste buffers (#514)

`paste_buffers_test.go`, `paste_buffers_scope_test.go`,
`paste_buffers_config_test.go`, `paste_buffers_bytes_test.go` and
`paste_buffers_rules_test.go` have seventeen tests and twenty-six controls.
Four unit tests in `internal/session/verb_buffers_test.go` and
`verb_lines_test.go` bound the daemon's memory and keep the person's share of
it, a security boundary, with eight controls of their own. On 2026-10-08 each control below cut one piece of
wiring, built a binary, and ran the named test against it. Every control
failed where shown, and the same tests passed on the branch build.

| Control: what was cut | Test | Where it failed |
| --- | --- | --- |
| The `SaveToPasteBuffers` call in `copyModeEffects.apply` | `TestPasteBuffersKeepYanksAndPasteThem` | after two yanks the daemon holds 0 buffers |
| `d.Register("paste_buffer", ...)` | `TestPasteBuffersKeepYanksAndPasteThem` | prefix `]` shows no "Pasted" |
| `d.Register("choose_buffer", ...)` | `TestPasteBuffersKeepYanksAndPasteThem` | prefix `#` never lists the buffers |
| The bracketed wrap in `verbPasteBuffer` | `TestPasteBuffersKeepYanksAndPasteThem` | no marks with bracketed paste on, after the paste with it off passed |
| `daemonBuffers` in the tmux shim always false | `TestPasteBuffersKeepYanksAndPasteThem` | `tmux show-buffer` does not see the CLI's buffer |
| The `readsBuffers` read-grant check in `checkGrants` | `TestPasteBufferVerbsFollowPaneGrants` | the pane with no grants reads its buffer, and `NONE_SHOW=1` never prints |
| The `GrantWrite` check for `set-buffer` in `checkGrants` | `TestPasteBufferVerbsFollowPaneGrants` | the pane with read alone sets a buffer, and `READ_SET=1` never prints |
| `d.buffers.SetLimits` in `applyUserConfig` | `TestPasteBufferLimitFollowsTheConfig` | after `limit = 1` two buffers stay, with limit 2 |
| The local store's `Add` in `saveLocalBuffer` | `TestPasteBuffersWithoutADaemon` | prefix `]` shows no "Pasted" |
| The owner filter in `paneBuffers` | `TestAPaneSeesOnlyItsOwnBuffers` | the pane reads the person's buffer, and `OTHER_SHOW=1` never prints |
| `list-buffers` sums every buffer for `bytes` | `TestAPaneSeesOnlyItsOwnBuffers` | the pane's listing says 5020 bytes, want its own 8 |
| `bare` gives the person every buffer | `TestThePersonsNewestIsNeverAPanes` | the person's bare `show-buffer` prints the agent's `planted` |
| The chooser's tag uncut and drawn whatever its length | `TestThePersonsNewestIsNeverAPanes` | the agent's long pane name fills the row, and `planted` and `7 bytes` are gone from it |
| The byte test in the store's `trim` | `TestPasteBufferByteCap` | both 600-byte buffers stay under `max_kb = 1` |
| `[paste_buffers]` left out of `DefaultConfig` | `TestPasteBufferKeysLiveInTheirFile` | `config prune --dry-run` does not list `paste_buffers.max_kb` |
| The shim sends a buffer as text in `data`, not `data_b64` | `TestPasteBufferKeepsEveryByte` | the binary file comes back with U+FFFD from byte 128, 520 bytes for 260 |
| The shim sends the parts of an upload as text | `TestPasteBufferKeepsEveryByte` | the 1.8 MB file differs at byte 786430, where the first cut splits a character |
| `SanitizePaste` in `verbPasteBuffer` | `TestPasteBufferSendsNoEscapeSequences` | `^[[201~evil` reaches the pane inside the paste |
| `BufferChooserActivate` pastes into the focused pane, not the one the chooser opened on | `TestPasteBufferChooserPastesWhereItOpened` | the paste lands in the pane that took the focus later |
| The local save in `handlePasteBufferSaveFailed` | `TestPasteBuffersWithAnOldDaemon` | with a daemon that answers `unknown_verb`, prefix `]` shows no "Pasted" |
| The change filter that `verbSetBuffer` passes to `Set` | `TestAPaneCannotReplaceThePersonsNamedBuffer` | the agent's `set-buffer -b release` exits 0, and `SHADOW=1` never prints |
| `find` takes the newest buffer of any kind for no name | `TestPasteBufferNamesFollowTmux` | `show-buffer` prints `named-x`, not `auto-y` |
| `trim` counts every buffer against the limit | `TestPasteBufferNamesFollowTmux` | under `limit = 2` the named buffer goes at the third buffer, so the `-a` check finds two |
| `set-buffer -a` with no name appends to the newest buffer | `TestPasteBufferNamesFollowTmux` | `-a extra` lands in `named-x`, and no new buffer appears |
| `PasteText` keeps every line feed | `TestPasteBufferTurnsLineFeedsIntoReturns` | `cat -v` shows no `LFa^MLFbLFa` |
| The version cut from `pasteBufferNamed` | `TestPasteBufferChooserSkipsAChangedBuffer` | Enter pastes the new text, and "changed after the list" never shows |
| The total check in `uploadPart` | `TestBufferUploadsHoldBoundedMemory` (unit) | two connections hold 1800 bytes under a cap of 1000 |
| One upload per connection in `uploadPart` | `TestBufferUploadsHoldBoundedMemory` (unit) | after a second id the connection holds 902 bytes, want 2 |
| `defer d.dropUploads(cs)` in `handleJSONConnection` | `TestAClosedConnectionDropsItsUpload` (unit) | the daemon still holds 4096 bytes 5 seconds after the connection closed |
| The `maxVerbLine` check in `verbLineReader.next` | `TestVerbLinesHoldBoundedMemory` (unit) | a line of 12582913 bytes is read whole |
| The budget refusal in `verbLineReader.next` | `TestVerbLinesHoldBoundedMemory` (unit) | a 200 KiB line is read with the budget taken |
| The budget release in `verbLineReader.done` | `TestVerbLinesHoldBoundedMemory` (unit) | after a refusal the budget still holds 573 chunks |
| Every line charged to the panes' budget | `TestThePersonsShareSurvivesAFullPanePool` (unit) | with the panes' budget full, the person's 1 MB upload part is refused and the connection closed |
| The upload total counted across the person and the panes | `TestThePersonsShareSurvivesAFullPanePool` (unit) | with the panes' uploads full, the person's part is refused as "too many uploads" |

The tests carry their positive halves. Each refused grant is served once the
grant is given. A pane that may not reach the person's buffer still reaches
its own, and reaches every buffer once it holds `admin`. The agent that may
not set the person's name still makes a buffer with no name. A buffer an admin
pane sets with no name is the person's newest. The bracketed check pastes once
with bracketed paste off, where no marks may show, before the paste where they
must.

```sh
TUIOS_E2E=1 TUIOS_E2E_BIN=/path/to/tuios TUIOS_E2E_FRAMES=/path/to/frames go test \
  -run 'TestPasteBuffer|TestPasteKey|TestAPane.*Buffer|TestThePersons' -count=1 -timeout 5m .
```

With `TUIOS_E2E_FRAMES` set, the run writes `paste-buffers-chooser`,
`paste-buffers-pasted`, `paste-buffers-bracketed`, `paste-buffers-grants`,
`paste-buffers-own-only`, `paste-buffers-planted`, `paste-buffers-escapes`,
`paste-buffers-no-takeover` and `paste-buffers-line-feeds`.

`TestPasteBuffersWithAnOldDaemon` sets `TUIOS_E2E_NO_BUFFER_VERBS=1`. With
`TUIOS_E2E=1` as well, the daemon answers every buffer verb with
`unknown_verb`, as a daemon from before them does. Nothing else reads it.

## OpenCode V2 plugin loading and session isolation

`TestOpenCodeV2PluginReportsOnlyItsSession` installs the shipped plugin, loads
its server and CLI entrypoints under a V2 API driver, and reports through a real
tuios daemon. It records `opencode-v2-session-isolation` state and frame artifacts.
It covers busy/done/error, permission and form prompts, and other-session and
subagent events while the main pane is working.

On 2026-10-06, before adding the V2 entrypoints, the test failed at plugin load:
`OpenCode V2 requires a default plugin definition` (`undefined` instead of
`string`). The same command passed with the fix:

```sh
TUIOS_E2E=1 TUIOS_E2E_BIN=/path/to/tuios TUIOS_E2E_FRAMES=/path/to/artifacts go test \
  -run TestOpenCodeV2PluginReportsOnlyItsSession -count=1 -timeout 3m .
```

The original failure was also observed in the real OpenCode 2.0.23 server log:
`Plugin must export a default definition with an id and an effect or setup function`
(`Missing key ["default"]`). A separate real-client check with OpenCode 2.0.23,
an isolated shared service and a PTY client confirmed automatic CLI plugin
discovery and form create/cancel reporting, while another session's form was
ignored. The driver test does not claim to execute OpenCode itself.

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
| Blur left the last pointer shape standing: the terminal paints the last OSC 22 it was sent wherever the pointer lands after refocus, so resize arrows sat over pane content | whole change | build main before the fix (the `m.ResetPointerShape()` in the `tea.BlurMsg` case of `internal/app/update.go` is the whole fix) and point `TUIOS_E2E_BIN` at it | `TestBlurRetiresThePointerShape` ("after the host lost focus: the host pointer shape is \"nwse-resize\", want \"default\"") | **caught** |
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
| `pane resize` never reaches the client that owns the layout | n/a, injected, cuts the wiring | rename `case "resize_window":` in `internal/app/update.go` | `TestHerdrFrontResize` ("pane resize --direction left exited 1") | **caught** |
| A read-only pane moves a border | n/a, injected | the `herdrAdmit(cs, "set-layout", ...)` check in `herdrPaneResize` passes every caller | `TestHerdrFrontResize` ("pane resize from a read-only pane: exit 0") | **caught** |
| `pane move` unanswered | n/a, injected, cuts the wiring | drop `pane.move` from `herdrMethods` | `TestHerdrFrontReloadMoveRename` ("pane move --new-tab exited 1") | **caught** |
| Workspace metadata is taken and never shown | n/a, injected, cuts the call site | `Tokens: nil` in place of `d.herdrWorkspaceMeta.tokens(...)` in `addHerdrSession` | `TestHerdrFrontReloadMoveRename` ("the workspace's topic token is <nil>") | **caught** |
| An older `seq` overwrites a workspace token | n/a, injected | the seq check in `herdrWorkspaceReportMetadata` never drops a report | `TestHerdrFrontReloadMoveRename` ("the workspace's topic token is older") | **caught** |
| `terminal title set` never reaches the client | n/a, injected, cuts the wiring | rename `case "set_client_title":` in `internal/app/update.go` | `TestHerdrFrontReloadMoveRename` ("terminal title set exited 1") | **caught** |
| `agent rename` takes a name herdr refuses | n/a, injected | the `herdrAgentNameOK` check in `herdrAgentRename` is skipped | `TestHerdrFrontReloadMoveRename` ("agent rename to Bad-Name: exit 0") | **caught** |
| `server reload-config` answered `unsupported` | whole change | the binary from `origin/main` (`c35d2942`) | `TestHerdrFrontReloadMoveRename` ("server reload-config exited 1"), `TestHerdrFrontResize` ("pane resize --direction left exited 1") | **caught** |
| `herdr status server` answered `unsupported`, so tools found no server | n/a, injected, cuts the wiring | drop `"status": parseStatus` from `groups` in `internal/herdrcli/parse.go` | `TestHerdrFrontReloadMoveRename` ("status server exited 2") | **caught** |
| `herdr session list` answered `unsupported`, so herdr-projects found no socket | n/a, injected, cuts the wiring | drop `"session": parseSession` from `groups` in `internal/herdrcli/parse.go` | `TestHerdrFrontReloadMoveRename` ("session list --json exited 2") | **caught** |
| `server agent-manifests` answered `unsupported` | n/a, injected, cuts the wiring | drop `server.agent_manifests` from `herdrMethods` | `TestHerdrFrontReloadMoveRename` ("server agent-manifests exited 1") | **caught** |
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
| Which-key sub-prefix menus showed the default keys after a rebind | n/a, injected | pass `nil` for the registry in `whichKeyMenu`'s menu helper (`internal/app/render_whichkey.go`), so the menus read the shipped keymap | `TestWhichKeyShowsTheConfiguredKeys/dark` and `/light` (the rename row shows `r`, want `e`, and the switch row `1-9`, want `1-4/6-9`) | **caught** (2 of 2 run) |
| Which-key menus kept the old keys after a runtime rebind | n/a, injected | drop `r.live = nil` from `buildMappings` in `internal/config/registry.go`, so the live-key cache outlives a Reload | `TestWhichKeyShowsTheConfiguredKeys/dark` and `/light` (after the saved rebind the rename row still shows `e`, want `y`) | **caught** (2 of 2 run) |
| A which-key row named a key that does not run its action | n/a, injected | pass `nil` for the registry in `whichKeyMenu`'s menu helper (`internal/app/render_whichkey.go`) | `TestWhichKeyRowsAreTheKeysThatRun` ("the new window row shows key \"n\", want y"). The same test presses `y`, `n` and the unbound `x`, and the window count moves only for `y` | **caught** |

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

## State deliveries ordered by change count

A client push keeps the state Version the same. The attach repair compared
Version, and the forward of a push to its peers ran outside `pushMu` with no
check. A peer push forwarded inside a repair let the repair's older snapshot
follow it, and of two pushes at once the older forward could reach the peers
last. The fix counts every change in `noteStateChangeLocked` and orders every
delivery by that count under `pushMu` (`Session.deliverPush`). The reconcile
reply to the pushing client goes the same way, through that client's
broadcast queue, so it can no longer overtake an older state queued ahead of
it. The tests are unit tests in `internal/session/state_push_order_test.go`.
Each lands the second change inside the first through a hook, so each fails
on every run.

| Bug | Fix removed | How | Tests that fail | Verdict |
|---|---|---|---|---|
| A repair sent an older state behind a peer push, and the client ended on the workspace it had left | whole change | `session.go`, `daemon_handlers.go`, `daemon_stream.go` and `state_wait.go` as on main after #561, with only the two test hooks added | `TestAttachRepairDoesNotFollowAPeerPush` ("B read workspace 1 after workspace 7 (states in order: [7 1 7])") | **caught**, 50 of 50 runs; 0 of 50 with the fix |
| Of two pushes at once, the older forward reached the peers last | whole change | as above | `TestConcurrentPushesForwardInOrder` ("C read workspace 2 after workspace 3 (states in order: [3 2 3])") | **caught**, 50 of 50 runs; 0 of 50 with the fix |
| The reconcile reply overtook an older state queued to the same client | whole change | as above | `TestReconcileReplyDoesNotOvertakeAQueuedState` ("A ended on workspace 3 before the marker, the session is on 2") | **caught**, 50 of 50 runs |
| The forward is not checked against what was delivered | n/a, injected, cuts the check | `deliverPush` skips the change count check and always sends to the peers | both tests above, with the same messages | **caught**, 50 of 50 runs each; `TestReconcileReplyDoesNotOvertakeAQueuedState` passes, because the queue alone orders the reply |
| The reconcile reply was dropped for good: a peer forward the fingerprint suppressed counted as delivered, and the reply was no newer | n/a, injected | `deliverPush` drops a snapshot no newer than what was delivered whether or not it carries a reply, with no second snapshot (the first version of this change) | `TestReconcileReplySurvivesASuppressedForward` ("A's push was reconciled, and A read no state that counts it before the next change") | **caught**, 50 of 50 runs; 0 of 50 with the fix. It passes with the whole change removed, because main writes the reply before the forward and never checks it |
| The reconcile reply overtook an older state queued to the same client, which adopted the older state last | n/a, injected, cuts the call site | `handleUpdateState` writes the reply with `sendMessage` before `deliverPush` again, and passes no `toSender` | `TestReconcileReplyDoesNotOvertakeAQueuedState` ("A ended on workspace 3 before the marker, the session is on 2 (states in order: [2 1 3 2])") | **caught**, 50 of 50 runs; 0 of 50 with the fix |

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

`TestEmptyWorkspaceReturnsAfterXpanesSpeedy` then failed in five main runs in
a row (37232733943 to 37243024125), with two panes left after Enter. The
daemon sent xpanes's `SetMultifocus` to the first client its map gave, which
was the second client about half the time. Multifocus is the client's own, so
Enter at the client that ran xpanes reached one pane. `findTUIClient` now
takes the client the person used last.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A routed command goes to any client | origin/main (946add0f), where `findTUIClient` returns the first match in the map | `TestEmptyWorkspaceReturnsAfterXpanesSpeedy` ("Enter closes the held panes: workspace 2 has 2 panes, want 0"), 9 of 30 runs on two cores. With the fix: 30 of 30 pass | **caught** |

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
| An exported layout that does not parse | the tree before the `GenerateTapeScript` fix | `TestLayoutExportValidates` (`Type command expects a string, got IDENTIFIER` at the command line) | **caught** |
| SnapFullscreen snaps to a quarter | the tree before the `SnapByDirection` fix | `TestTapeSnapFullscreenFillsTheScreen` (the window is 60x19 of 120x40) | **caught** |

`TestExampleTapeRuns` plays `examples/actions_and_waits.tape` and has no
control of its own: it fails on any of the first two cuts, since the example
uses both.

## herdr plugins

`TestHerdrPluginTrustAndHooks`, `TestHerdrPluginActions`,
`TestHerdrPluginPanes` and `TestHerdrPluginPalette` run the stand-in plugins in
`testdata/herdrplugins`. Each control cut one line in a shared clone of the
branch, built the binary, and ran the one test named.

The trust test carries its positive half in the same fixture: the plugin
that runs nothing while it is off runs its startup and its hook after the
person enables it.

| Wiring | Cut | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The enabled check on startup | `runStartups`: `!e.Runnable()` made `e.Plugin == nil` | `TestHerdrPluginTrustAndHooks` ("a disabled plugin ran") | **caught** |
| The person-only gate on enable | `herdrPluginSetEnabled`: the `herdrPluginTrust` call cut | `TestHerdrPluginTrustAndHooks` ("cli from a pane was not refused: exit 0") | **caught** |
| The host's start | `Daemon.Start`: `d.plugins.start()` cut | `TestHerdrPluginTrustAndHooks` (startup.log never holds the startup line) | **caught** |
| The event hooks | `pluginHost.start`: `go h.followEvents()` cut | `TestHerdrPluginTrustAndHooks` (events.log never holds the new pane) | **caught** |
| The plugin methods on the herdr socket | `herdrAPICall`: the `herdrPluginCall` branch cut | `TestHerdrPluginActions` ("plugins run --wait: exit status 1") | **caught** |
| The plugin folder as the working folder | `Runner.Start`: `cmd.Dir = j.Plugin.PluginRoot` cut | `TestHerdrPluginActions` ("the action saw cwd=... want .../actions") | **caught** |
| The plugin variables of a pane | `herdrPluginPaneOpen`: `Env: env` cut from the window options | `TestHerdrPluginPanes` (the popup's marker never shows: the script has no state folder to write to) | **caught** |
| The popup placement | `herdrPluginPaneOpen`: `opts.Popup = true` cut | `TestHerdrPluginPanes` ("the plugin popup is 38 rows, want the 12 its entry names") | **caught** |
| The person's change applies only to the plugin it names | `writePlugins`: apply the table read back from config.toml in place of `d.plugins.applied()` with the change | `TestHerdrPluginTrustAndHooks` ("the person's enable of e2e.hooks also enabled e2e.actions, which a pane wrote into config.toml") | **caught** |
| A process a plugin leaves behind is not the person | `baseEnv`: the `TUIOS_SOCKET` entry cut | `TestHerdrPluginTrustAndHooks` ("a process the startup command left behind enabled a plugin") | **caught** |
| The plugin rows in the palette | `rebuildPaletteItems`: the `pluginPaletteItems` call cut | `TestHerdrPluginPalette` ("the palette never listed the plugin action") | **caught** |
| A plugin that is off has no palette row | `pluginPaletteItems`: `!e.Runnable()` made `e.Plugin == nil` | `TestHerdrPluginPalette` ("the palette offers an action of a plugin that is off") | **caught** |
| The grant check on the plugin log | `herdrPluginLogList`: the admin check never refuses | `TestHerdrPluginActions` ("plugin log from a read-only pane: exit 0" with the action's output) | **caught** |

## send-keys in the pane's keyboard mode

`TestSendKeysEncodesKeysForThePaneMode` in `send_keys_encoding_test.go` sends
keys to three panes: one in the kitty keyboard protocol, one in
modifyOtherKeys 2, and one that asked for nothing. The legacy pane is the
positive half: it still gets `ctrl+h` as `08`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The pane mode never read | `sendKeysBytes`: `k.encode(modes)` made `k.bytes(modes.appCursor)` | `TestSendKeysEncodesKeysForThePaneMode` (the kitty pane shows `read1=08`, not `read1=1b5b3130343b3575`) | **caught** |
## The opt-in explorers: help -i, config browse, keybinds browse

`explorers_test.go` saves each frame it checks as text and PNG. Each control
cut one call site from the branch, built a binary, and ran the named test.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| help -i does nothing | the help command's `Run`: the `!interactive` term cut, so -i prints the plain help | `TestHelpExplorer` (the child exits 0 before the explorer's title shows) | **caught** |
| help opens an explorer on a terminal | the help command's `Run`: the plain branch also made to need a stdout that is not a terminal | `TestHelpExplorer` (`tuios help` on a terminal never exits by itself) | **caught** |
| config browse does not set | the explorer's `Apply`: the `setConfigOption` call cut | `TestConfigExplorer` (no refusal for `sideways`, and the source never reads `session`) | **caught** |
| keybinds browse is not wired | `addExplorers`: the keybinds `AddCommand` cut | `TestKeybindsExplorer` (the child prints the keybinds help and exits) | **caught** |

The positive halves: each test leaves its explorer with `q`, and
`TestExplorerEscLeaves` with `esc`, and both wait for the process to exit by
itself with status 0. The "plain on a terminal" checks run `tuios help`,
`tuios help checkpoint`, `tuios config`, `tuios list-options`, `tuios keybinds`
and `tuios keybinds list` in a terminal and check that each exits by itself,
never shows the alternate screen, and ends with the lines it prints with no
terminal.
## A close_on_exit window outlived a command that exited at once

`new-window` with `close_on_exit` closes the window from the process's exit
callback, which finds the window by its PTY. A command that exited before
the window was in the state left the callback nothing to find, and the
window stayed. The control is a unit test in `internal/session`, because it
must hold the add until the process has exited, and only a test hook can
order the two. The callback runs in the same fixture, so its finding the
window is the positive half.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| onExit runs before the window is added | `AddDaemonWindowWith` in `internal/session/session_ops.go`: the `added` gate cut, so onExit runs as the process exits | `TestOnExitSeesTheWindowOfAProcessThatExitedAtOnce` ("onExit ran before the window was in the state") | **caught in `internal/session`** (10 of 10 run) |

## A trim timer reset from outside its synctest bubble

`internal/memtrim` kept one trim timer and reset it on each `Request`. A
window closed inside a synctest bubble armed that timer in the bubble. The
next `Request`, from an ordinary test cleanup, reset it from outside, and the
runtime ended the test binary. Each `Request` now starts its own timer.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| One shared timer, reset on each Request | `internal/memtrim/memtrim.go` from origin/main | `TestRequestAfterABubbleArmedTheTimer` ("fatal error: reset of synctest timer from outside bubble") | **caught in `internal/memtrim`** (1 of 1 run, deterministic) |

## The newest paste buffer lost a tie on mtime

The tmux shim ordered paste buffers by their file's mtime. Two buffers
written in one tick of the kernel's file clock tied, and the name order
picked the top. `paste-buffer` with no `-b` then typed the older buffer.
`writeBuffer` now stamps each buffer after the newest one.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A buffer keeps the mtime the kernel gave it | `internal/tmuxcompat/buffers.go` from origin/main | `TestPasteBufferAcrossCalls` ("the newest buffer typed \"one\\rtwo\\r\", want b2"), 19 of 30 runs. With the fix: 30 of 30 pass | **caught in `internal/tmuxcompat`** |
## tuios hosts sync

`TestHostsSync` syncs four fake hosts: one with no tuios, one with the new
version, one with the old version and a daemon that runs `sleep` in a pane,
and one with no machine behind its address. `TestHostsSyncRestartWithYes`
restarts the daemon of an old host. Each control changed one line, built the
binary, and ran the named test.

| Wiring | Cut | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The install | `runHostsSync`: the `applySyncTarget` call cut | `TestHostsSync` (fresh is not installed, old does not run the new version) | **caught** |
| No restart by default | `planSyncTarget`: `if opts.restart` made `if true` | `TestHostsSync` (the default run asks to restart and prints no result) | **caught** |
| The question before a restart | `runHostsSync`: the `confirmSyncRestarts` call cut | `TestHostsSync` (the refusal is gone and a binary changed) | **caught** |
| The dry run | `runHostsSync`: the `!opts.dryRun` gate on the apply step made `true` | `TestHostsSync` ("the dry run changed a binary on a host") | **caught** |
| A matching version is left alone | `planSyncTarget`: `t.install` made always true | `TestHostsSync` (same plans "would update" and is installed again) | **caught** |
| One failed host does not stop the rest | injected: return an error after the probe when one host failed | `TestHostsSync` (no result for any host) | **caught** |
| The restart | `planSyncTarget`: `t.restart = true` made `false` | `TestHostsSyncRestartWithYes` (the daemon still runs the old version) | **caught** |
| The busy panes | `readDaemonSessions`: the `parsePaneProcs` call cut | `TestHostsSync` (no busy pane, and the refusal does not name `sleep`) | **caught** |

The first CI run of `TestHostsSync` read the plan right after it made the pane
that runs `sleep`, and found no busy pane (run 37232323815). The pane was not
yet running `sleep`. The test now reads the plan again, for up to 20 seconds,
until the pane shows. The busy-pane control still fails after that wait.

## --start on tuios hosts test and tuios hosts sync

`TestHostsTestStart` tests a fake host that has tuios and no daemon.
`TestHostsSyncStart` tests a fake host with no tuios. `TestHostsSync` also
checks that a restart counts one session as "1 session". Each control ran
the named tests against one build.

| Wiring | Cut | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The feature | the binary built from origin/main | `TestHostsTestStart` (the message does not name `PATH start-server` or `--start`, and `--start` is an unknown flag), `TestHostsSyncStart` (`--start` is an unknown flag), `TestHostsSync` ("1 session(s)") | **caught** |
| The start in hosts test | `startHostDaemon`: the `startDaemonWith` call replaced by success | `TestHostsTestStart` (the host still reports no_daemon after `--start`) | **caught** |
| The dry run of hosts test | `startHostDaemon`: `if dryRun` made `if false` | `TestHostsTestStart` ("the dry run changed the host, which now reports \"up\"") | **caught** |
| The start in sync | `startSyncDaemon`: returns at once | `TestHostsSyncStart` (fresh is installed and its daemon is stopped) | **caught** |
| The plan in sync | `planSyncTarget`: `t.start = true` made `false` | `TestHostsSyncStart` (the plan is "would install", and no daemon starts) | **caught** |
| The dry run of sync | `runHostsSync`: the `!opts.dryRun` gate on the apply step made `true` | `TestHostsSyncStart` ("the dry run installed a binary on fresh") | **caught** |

## A pending wait-for and a flood of output

`TestWaitForOutputDoesNotSlowFlood` in `internal/session` compared the wall
time of a flood with a pending waiter against one without it. On shared CI
runners that ratio failed at 2.76x and 2.83x (runs 37241456414 and
37246926645), and it was 1.00x to 1.12x locally on one core and on eight.
The load on a runner changes between the two floods, so the ratio measured
the runner. The test now counts what the waiter does during the flood: its
captures, and how long they hold the emulator lock.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A capture on every output event | `waitOutputMinGap` set to 0 in `internal/session/verb_subscribe.go` | `TestWaitForOutputDoesNotSlowFlood` ("the waiter took 1554 captures in 983ms, more than the 26 its gap allows"), 10 of 10 runs | **caught in `internal/session`** |

With the fix the waiter took 7 to 27 captures, and held the lock for 1% to 2%
of the flood on the pure Go backend and 12% on libghostty-vt. 30 of 30 runs
passed on one core, and 5 of 5 with `-tags ghostty`.

## The tape FAILED indicator, looked for after it went away

`TestTapeExecStopsAtTheFailedLine` failed on CI (run 37255241424, shard 3 of
6) with "the client never showed that the tape failed". The client shows
FAILED for `scriptDoneLinger` (2s) after a tape stops, and the first frame
drawn after that drops it. The test slept 2s before it looked, so any frame
drawn in between, such as a notification's, hid the indicator. The test now
looks for FAILED as soon as exec returns, before the sleep.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A frame drawn past the linger, old order | the test as on main, with a resize 500ms after its 2s sleep | `TestTapeExecStopsAtTheFailedLine` ("WaitForText timed out ... waiting for text \"FAILED\""), 3 of 3 runs. The same resize with the new order: 3 of 3 pass | **caught** |
| No FAILED indicator | `scriptStatus = "FAILED • "` changed to `"DONE • "` in `internal/app/render_overlays.go` | `TestTapeExecStopsAtTheFailedLine` (the same message), 1 of 1 run | **caught** |

## The looks pin and a config save in flight

`TestChromeSetFromTheCommandLineRetilesThePanes` failed in CI on 2 and 3
October with "a pane is at row 0 and 38 rows tall" and the rail at column 28.
That is the dock at the bottom row and the rail of the pinned looks. The
harness pins the looks on every CLI call, and the call reads and rewrites the
config file. Before `e57ecf37` a save truncated the file and then wrote it. A
pin that read the empty file wrote back a file of pins alone after the save
ended, and the client's watcher applied it. `e57ecf37` closed the empty read.
The harness now leaves alone a file it has pinned once and that tuios wrote
since, and it writes through a rename. The test also checks the rows after
the rail step, where the rewrite went unseen before.

Both injections below make the two windows wide enough to hit every time. The
product one is on `e57ecf37~1`: `writeConfigBytes` opens the file with
`O_TRUNC`, sleeps, and then writes. The harness one: a pin that reads an empty
file sleeps 1.2 s before it writes. Without them the race did not show
locally: 30 of 30 runs passed on main on 2 cores, and 30 of 30 on
`e57ecf37~1` on 1 core with a busy loop beside it. No CI run of this test
failed after `e57ecf37`. With this change, 30 of 30 runs pass on main on 2
cores.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The pin rewrites a file read mid-save (the CI failure) | n/a, injected: the product sleeps 1 s after the truncate, the old harness | `TestChromeSetFromTheCommandLineRetilesThePanes` ("the daemon after the dock is hidden: a pane is at row 0 and 38 rows tall, want row 0 and 40 rows", rects at `(28,0) 46x38`, the CI shape) | **caught** (5 of 5) |
| The same, with the harness fix | as above, the new harness | the same test, with the shape of the row before it: "a pane is at row 2 and 38 rows tall". The client's own watcher read the empty file, which `e57ecf37` fixed. No run shows the pins-only shape | **caught**, the product half only (5 of 5) |
| The pin rewrite alone | n/a, injected: the product sleeps 150 ms, inside the 200 ms debounce, the old harness | `TestChromeSetFromTheCommandLineRetilesThePanes` ("the daemon after the rail is turned off: a pane is at row 0 and 38 rows tall, want row 0 and 40 rows"). The pin read the empty file in 10 of 10 runs | **caught** (10 of 10) |
| The same, with the harness fix | as above, the new harness | none. The pin read the empty file in 10 of 10 runs and left it alone | **passes** (10 of 10), the positive half |

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

## Dock pill caps off (#451)

`dock_pill_caps = false` left the caps on the mode chip and the workspace
pills. Their cap accessors did not read the setting. The default then became
rounded, so an unset option draws the caps.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The whole change | build `origin/main` (`1e0b4c89`) and point `TUIOS_E2E_BIN` at it | `TestDockPillCapsFollowTheSetting/off` ("dock_pill_caps = false, and the dock row draws caps", six cap glyphs on the row). The `on` case passes on main, which is correct | **caught** |
| Rounded by default | build `eced6152`, where the default was flat | `TestDockPillCapsFollowTheSetting/default` ("default: the dock row draws only caps [], want caps on the mode chip and the workspace pills"). `off` and `on` pass there, which is correct | **caught** |

## Sessionizer: tuios new --cwd and switch-session (#452)

`tuios new` started the first window in the daemon's directory. No command
switched an attached client, and a session switched to in place ignored
`[startup]`. The tests are in `sessionizer_test.go`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The whole change | build `origin/main` (`1e0b4c89`) and point `TUIOS_E2E_BIN` at it | `TestNewStartsInTheCallersDirectory` ("the first window of plain is in .../cwd, want .../here"), `TestSwitchSessionMovesTheClient` ("the refusal does not name --create": the command does not exist), `TestSwitchSessionReachesAHost` ("switch-session build:far-new: exit status 1"), `TestSwitchToAnUnarrangedSessionAppliesStartup` ("switch-session later: exit status 1") | **caught** |
| No `[startup]` on a switch | `applyStartupToUnarranged` in `internal/app/host_attach.go` does nothing | `TestSwitchToAnUnarrangedSessionAppliesStartup` ("later's pane is 80 wide, want it filling the 120 columns: it came up floating") | **caught** |

## Keybindings in a config saved on disk (#358)

`TestAKeybindingSavedOnDiskReachesTheClient` in `config_watch_test.go` binds
`prefix+alt+h` in a saved file and presses it on the running client. The
positive half is in the same test: `prefix+alt+y` from the startup file fires,
and `prefix+alt+h` does nothing before the save.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The registry never reloaded | `ApplyReloadedConfig`: the `KeybindRegistry.Reload` block cut | `TestAKeybindingSavedOnDiskReachesTheClient` ("a keybinding saved on disk never reached the running client") | **caught** |

## Command entries whose made-up names clash

`TestKeybindsDoctorNamesACommandNameClash` in `keybinds_doctor_test.go` writes
two `[[keybindings.command]]` entries with no name, whose commands agree in the
first 40 characters. The positive half is the same two entries with a name
each: the doctor reports nothing and `keybinds list` shows both.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (1e0b4c89) | `TestKeybindsDoctorNamesACommandNameClash` ("the doctor reports 0 command entries, want 1") | **caught** |
| The report never reads the entries | `Report`: the `CommandProblems` field cut | `TestKeybindsDoctorNamesACommandNameClash` ("the doctor reports 0 command entries, want 1") | **caught** |

## Compact dock

`dock_compact_test.go` drives `appearance.dock_compact` with the dock at the top
and at the bottom. The positive half is in each fixture: the same test starts
a full dock and asserts the pane's edge on the row after the rule.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`43573cf8`) | `TestDockCompactGivesThePaneARow`, `TestDockCompactPillClickSwitchesWorkspace`, `TestDockCompactSwitchesOnConfigSave` ("the pane meets the dock at row 2, want 1" at the top, "row 37, want 38" at the bottom), `TestDockCompactClientSharesASessionWithAFullOne` (the rule drawn on the row a compact client keeps blank), `TestDockCompactFromTheSettingsPanel` (no Compact dock row) | **caught** |
| The dock band stays two rows | `InDockBand`: `config.DockFullHeight` in place of `Settings.DockHeight()` | `TestDockCompactPillClickSwitchesWorkspace` top and bottom ("the pane row next to a compact dock: context menu never showed [Split right Rename]"). The pill click before it passes, which is correct | **caught** |
| The fast path ignores a larger peer reserve | `fullscreenFastWindow`: the `OwnLayoutReserve` check cut | `TestDockCompactClientSharesASessionWithAFullOne` (the dock row drawn on the row the compact client keeps blank, one row above the screen's last row) | **caught** |

## Session number keys and the workspace rename action

`TestSwitchSessionByNumberAndRenameWorkspace` in `session_number_keys_test.go`
binds `switch_session_3`, `switch_session_9` and `rename_workspace` with three
sessions. The positive half is in the same test: slot 9 is empty and says so,
and the rename leaves the session the client left alone.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`43573cf8`) | `TestSwitchSessionByNumberAndRenameWorkspace` ("switch_session_3 never landed on charlie") | **caught** |
| The rename action is not registered | `registerHandlers`: the `rename_workspace` line cut | `TestSwitchSessionByNumberAndRenameWorkspace` ("rename_workspace never opened the editor") | **caught** |

## Tailscale SSH check mode and policy refusals

The tests are in `hosts_tailscale_test.go`. A wrapper in front of the sync
stand-in prints the banner real Tailscale prints in check mode, then waits for
an approval file. A second address prints the policy refusal and exits 255.
The positive half of each test is a host with no gate in the same run, which
syncs or is listed as reached.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`43573cf8`) | `TestHostsSyncReportsATailscaleCheck` (kind "" and "could not read the host. ssh reported: # To authenticate, visit: ...", sync took 1m30s, hosts test reports "unreachable", the listing shows no approval), `TestHostsSyncReportsATailscalePolicyRefusal` (kind "", "ssh could not connect"), `TestHostsSyncOneApprovalCoversTheRun` (no offer to wait) | **caught** |
| No shared connection | `runHostsSync`: the `share.share(&t.host)` line cut | `TestHostsSyncOneApprovalCoversTheRun` (the link asks for a second approval, and sync never finishes) | **caught** |
| No approval desk | `runHostsSync`: the runner gets no desk | `TestHostsSyncOneApprovalCoversTheRun` (sync exits 1 before it offers to wait) | **caught** |
| The link never reads the gate | `link.attempt`: the `ParseSSHGate` call in the preamble wait made nil | `TestHostsSyncReportsATailscaleCheck` (hosts test reports "unreachable", the listing shows no approval) | **caught** |
| A policy refusal read as a plain ssh failure | `remoteRunError`: the `GateFromStderr` check cut | `TestHostsSyncReportsATailscalePolicyRefusal` (kind "") | **caught** |

## Tailscale SSH sign-in on the rail

The tests are in `hosts_signin_test.go`. The rail stand-in (`writeFakeSSH`)
has the Tailscale wrapper of `hosts_tailscale_test.go` in front of it. The link
opener is a script that records each address it gets. `quickbox` ends each
wait after 6 seconds. The tests wait for its fourth dial to end, so the link's
backoff is 16 seconds: a new page within 8 seconds can only come from the
gesture. After that dial the backoff is 32 seconds, and a page within 12
seconds can only come from the quick redial a sign-in starts. `localbox` and
`lookalikebox` print a banner with `http://127.0.0.1:631/admin` and
`https://login.tailscale.com.evil.example/a/...`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released label | `hostStatusLabel`: "sign in" back to "approve", as on origin/main | `TestRailOpensTheTailscaleSignIn` ("the rail never said sign in beside the gated host") | **caught** |
| A click on the name opens the page and does not fold | `SidebarRelease`: a host that waits for a sign-in opens the page instead of folding, as the first version of this PR did | `TestRailOpensTheTailscaleSignIn` ("a click on the name ... did not fold it") | **caught** |
| Any address in the banner is trusted | `SignInURLAllowed`: returns true for any URL-shaped text | `TestRailRefusesAnUntrustedSignInLink` (the rail opened `https://login.tailscale.com.evil.example/...`, `hosts --json` reports both addresses, `hosts signin` repeats and opens them) | **caught** |
| No wake in the link's backoff | `link.supervise`: the `case <-l.wake` line cut | `TestRailSignInAsksForANewPage/click` (15.0s), `/enter` (14.8s) | **caught** |
| No quick redial after a sign-in | `link.supervise`: the `wait = min(wait, approvalRetry)` line cut | `TestRailSignInAsksForANewPage/click` (31.0s), `/enter` (30.9s) | **caught** |
| A page asked for before the daemon had one never opens | `takePendingSignIns` returns nil, and `applyHostRetry` opens nothing | `TestRailSignInAsksForANewPage/click` and `/enter` (the opener was started with []) | **caught** |

Not covered by an e2e test: the 2 second floor between dials that
`retry-host` can wake (`retryMinGap`), and the address left out of `list-hosts`
and `list-host-sessions` for a caller over a link. The address check itself
also has a table test, `TestSignInURLAllowed` in `internal/federation`.

## Copy sweep after a copy-mode yank

`copy_flash_yank_test.go` samples the frames after a copy-mode yank and holds
the light to the yanked text. `TestCopyFlashSweepsACopyModeYank` covers `y` on
a `v` selection, `y` on a `V` selection through a daemon, and a copy-pipe key.
`TestCopyFlashSweepsAMultiCopyYank` covers `y` in multi copy mode. The positive
half is the mouse copy in `copy_flash_test.go`, which sweeps with the same
config, and the multi copy test also asserts no light on the pane without a
selection.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`486ce455`) | `TestCopyFlashSweepsACopyModeYank` visual-y, line-y-daemon and copy-pipe ("no frame showed the sweep"). `TestCopyFlashSweepsAMultiCopyYank` passes, which is correct: multi copy mode already swept | **caught** |
| The yank never draws the sweep | `copyModeEffects.apply`: the `NoteCopyFlashRegion` call cut | `TestCopyFlashSweepsACopyModeYank` visual-y and line-y-daemon ("no frame showed the sweep"). copy-pipe passes, which is correct: it takes another path | **caught** |
| Multi copy mode never draws the sweep | `yankMultiCopy`: the `NoteCopyFlashMany` call cut | `TestCopyFlashSweepsAMultiCopyYank` ("the sweep reached ... in 0 frames" for both selected panes) | **caught** |

## last_pane

`last_pane_test.go` walks the focus with the focus-window CLI, presses `;` to
flip back and forth, then repeats the flip after a new pane (`n`) and after a
switch to an empty workspace (`Alt+2`, `n`), where the flip must return to the
pane focus left on workspace one.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The previous pane is recorded only inside FocusWindow | build `b535edb6` (the PR before the fix) | `TestLastPane` ("first flip landed on c0088b40, want 1b79e71f"): the CLI focus moves never passed through FocusWindow, so nothing recorded them and the flip had no target. The new-pane and workspace cases sit downstream of the first flip and never ran | **caught** |
## Resizing under shared borders

`shared_border_resize_test.go` resizes panes with shared borders on and reads
both the rectangles from `list-windows` and the divider cells from the frame.
Each test asserts that the resize moved something before it asserts the
divider survived, so a fixture that resizes nothing cannot pass.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`486ce455`) | `TestSharedBorderKeyResizeKeepsTheDivider`, all nine cases ("pane 1 starts 0 columns after pane 0 ends, want 1", and "pane 0 and pane 1 overlap" with `gap = 2`), `TestSharedBorderDragKeepsTheDivider` ("the divider during the drag is not on column 47"), `TestSharedBorderDragFromAJunction`, both cases (the master did not move), `TestScrollingSharedBorders` ("pane 1 starts 0 columns after pane 0 ends, want 1") | **caught** |
| The split mover ignores the gap | `adjustTilingNeighborsGeneric`: `gap := 0` in place of `m.separatorGap()` | `TestSharedBorderKeyResizeKeepsTheDivider` (all nine cases), `TestSharedBorderDragKeepsTheDivider` | **caught** |
| No grab on a pane's left divider | `armTiledBorderResize`: the borderless left-edge case cut | `TestSharedBorderDragFromAJunction/master-right` ("the master starts on column 61, want 69"). `master-bottom` passes, which is correct | **caught** |
| No grab on a pane's top divider | `armTiledBorderResize`: the borderless top-edge case cut | `TestSharedBorderDragFromAJunction/master-bottom` ("the master starts on row 20, want 24"). `master-right` passes, which is correct | **caught** |
| The strip keeps its own borders | `panesBorderless`: `&& !m.UseScrollingLayout` put back | `TestScrollingSharedBorders` ("pane 1 starts 0 columns after pane 0 ends, want 1") | **caught** |
| No grab on a strip divider | `armBorderResize`: `armScrollDividerResize` call replaced by `return false` | `TestScrollingSharedBorders` ("the first column is 48 columns wide after the drag, want 38") | **caught** |
| Only the dragged window is told its size | `ScrollingResizeColumnVisual`: `PendingResizes` for the dragged window alone, not every window in the column | `TestScrollingDividerDragResizesEveryStackedWindow` ("the lower window's shell is 48 columns wide, want 38", read with stty) | **caught** |
| The strip clamps under the pointer | `ScrollingResizeColumnVisual`: `ClampViewport` on every motion, and `applyBorderResize` measuring from the column's current `X` | `TestScrollingDividerDragAtTheStripEnd` ("the divider is not under the pointer at column 47 during the drag") | **caught** |
| A covered divider is grabbed | `armTiledBorderResize`: the `paneOver` check cut | `TestSharedBorderPressInsideAZoomedPane` ("the panes moved under the zoomed pane"). The test runs in BSP: in master-stack a resize under a zoom is not recorded, and the retile at the end of the zoom hides it | **caught** |
| A click records a fixed width | `handleMouseRelease`: the width check on the scrolling capture cut | `TestScrollingDividerClickKeepsAProportionalColumn` ("the first column is 48 columns wide after the client grew, want 60") | **caught** |


## Pane labels (display_panes)

The tests are in `pane_labels_test.go`. They read each label's block glyphs
off the screen with the pane's name under them, type the label, and read the
focused pane back from list-windows. The positive half of each jump is a
check that the focus was on another pane before the label was typed. Esc
closes the labels with no jump in the same test. The leak test sets one known
prompt in every pane and shows first, with the labels closed, that the key
and the paste reach the shells and leave the line it looks for.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The action is not registered | `registerPrefixHandlers`: the `display_panes` line cut | `TestPaneLabelsFocusAPane` ("label 1 over alpha never showed"), `TestPaneLabelsTwoKeysAndCustomKeys` ("label a over alpha never showed") | **caught** |
| A key that starts a label is not kept | `PaneLabelsPress`: `prefix = true` made `prefix = false` | `TestPaneLabelsTwoKeysAndCustomKeys` ("after s s: the labels stayed up") | **caught** |
| No list of the panes behind a zoom | `OpenPaneLabels`: the `listOn` line cut | `TestPaneLabelsZoom` ("the hidden panes are not listed") | **caught** |
| The labels do not own the keyboard | `routeKey`: the `PaneLabelsOpen` check cut | `TestPaneLabelsKeepKeysAndPastesFromPanes` (in multifocus, "the label key: 3 reached pane ...") | **caught** |
| A paste passes the labels | `pasteTakenByOverlay`: the `PaneLabelsOpen` check cut | `TestPaneLabelsKeepKeysAndPastesFromPanes` ("a paste with the labels up: pz9 reached pane ...") | **caught** |
| The labels outlive a layout change | `PaneLabelsOpen`: the layout compare made a workspace compare | `TestPaneLabelsFocusAPane` ("after a pane opened: the labels stayed up") | **caught** |

## ssh-aware splits

The tests are in `ssh_split_test.go`. A fake ssh on PATH writes its arguments
to a numbered file and then acts as a shell. `TestSSHSplitRunsTheSSHOnPath`
uses a compiled stand-in (`fakessh/`) on PATH and a copy of it in the pane's
folder, run as `./ssh`. The refusal tests (`TestSSHSplitRefusesAProxyCommand`,
`TestSSHSplitIgnoresTheSSHOfATransfer`) have their positive half in
`TestSSHSplitRunsTheSameSSH` and `TestSSHSplitFindsSSHUnderANestedShell`,
which follow ssh through the same fixture. The stand-in answers `ssh -G` with
the host name of `-o HostName`, or the destination, and records nothing.
`TestSSHSplitPassesTheAgentSocket` starts an `ssh-agent` in the pane and
stops it when it ends. The positive half of the follow
option is in its own fixture: the same split key in a pane with no ssh opens a
shell. `TestSSHSplitFallsBackToAShell` is the positive half of every other
test: the actions open a pane when no ssh runs.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`5c0fa9c1`) | `TestSSHSplitRunsTheSameSSH`, `TestSSHSplitKeepsTheRemoteFolder`, `TestSSHSplitFallsBackToAShell`, `TestSSHSplitFindsSSHUnderANestedShell` (the window count never reached 2), `TestSSHSplitFollowOption` (the fake ssh never ran a run 1) | **caught** |
| The daemon ignores `ssh_from` | `handleExecuteCommand`: the `SSHFrom` block made `if false` | `TestSSHSplitRunsTheSameSSH`, `TestSSHSplitFollowOption`, `TestSSHSplitKeepsTheRemoteFolder`, `TestSSHSplitFindsSSHUnderANestedShell` (the new pane never ran ssh). `TestSSHSplitFallsBackToAShell` passes, which is correct | **caught** |
| The action is not registered | `registerHandlers`: the `split_ssh_vertical` line cut | `TestSSHSplitRunsTheSameSSH`, `TestSSHSplitFallsBackToAShell` (the window count never reached 2) | **caught** |
| The client split does not ask to follow | `splitFocused`: the daemon branch calls `NewWindowHere` whatever `followSSH` says | `TestSSHSplitRunsTheSameSSH` (the split pane never ran ssh) | **caught** |
| The ordinary split ignores the option | `handleSplitVertical`: the `FollowSSHOnNewWindow` check made `false &&` | `TestSSHSplitFollowOption` (the fake ssh never ran a run 1) | **caught** |
| The remote folder is not kept | `placeRecord.announce`: the report stored with its host and no folder | `TestSSHSplitKeepsTheRemoteFolder` (ssh ran with `pollen@fakehost` alone) | **caught** |
| The first PR head, before the review fixes | build `d6fbe24c` | `TestSSHSplitRunsTheSSHOnPath` (the split ran `cwd/ssh`, the binary of the process), `TestSSHSplitRefusesAProxyCommand` and `TestSSHSplitIgnoresTheSSHOfATransfer` (2 runs, want 1) | **caught** |
| The binary is taken from the process | `parseRemoteLogin`: `l.bin = argv[0]` in place of the `lookPath` call | `TestSSHSplitRunsTheSSHOnPath` (the split ran `./ssh`) | **caught** |
| A local-command option is not refused | `sshValueVerdict`: the `sshRefuseConfig` check cut | `TestSSHSplitRefusesAProxyCommand` (2 runs, want 1) | **caught** |
| Any parent may start ssh | `startedByShell`: the `sshLaunchers` check made `false &&` | `TestSSHSplitIgnoresTheSSHOfATransfer` (2 runs, want 1). `TestSSHSplitFindsSSHUnderANestedShell` passes, which is correct | **caught** |
| No `-o RemoteCommand=none` with the cd | `remoteLogin.argv`: only `-t` added | `TestSSHSplitKeepsTheRemoteFolder` (the argv lacks the option) | **caught** |
| The second PR head, with the refusal list | build `f98a72e5` | `TestSSHSplitRefusesALineBreakInAnOption` (2 runs, want 1), `TestSSHSplitKeepsTheFolderOfAnAlias` (no cd for the alias), `TestSSHSplitPassesTheAgentSocket` (the split's ssh had no `SSH_AUTH_SOCK`) | **caught** |
| A control character is not refused | `parseSSHArgs`: the `hasControl` check made `false &&` | `TestSSHSplitRefusesALineBreakInAnOption` (2 runs, want 1) | **caught** |
| The alias is not resolved | `SSHFollowArgv`: `hostMatches` in place of `reportMatches` | `TestSSHSplitKeepsTheFolderOfAnAlias` (no cd). `TestSSHSplitKeepsTheRemoteFolder` passes, which is correct | **caught** |
| The agent socket is not passed | `handleExecuteCommand`: the `newWindowEnv = env` line cut | `TestSSHSplitPassesTheAgentSocket` (the split's ssh had no `SSH_AUTH_SOCK`) | **caught** |
| The third PR head, with -E kept | build `59403a92` | `TestSSHSplitRefusesALogFile` (2 runs, want 1) | **caught** |
| -E is not refused | `sshRefuse`: `E` taken out | `TestSSHSplitRefusesALogFile` (2 runs, want 1) | **caught** |
| No `-o ControlMaster=no` | `remoteLogin.argv`: the append cut | `TestSSHSplitRunsTheSameSSH` (the argv lacks the option) | **caught** |
| ssh -G is not killed as a group | `resolveSSHHostName`: no `killGroupOnCancel`, no `WaitDelay` | No e2e test. `TestResolveSSHHostNameIsBounded` (took 30 s, want about 2 s) | **caught by a unit test only** |
| The agent socket folder is not checked | `ownedSocket`: the folder mode check cut | No e2e test. `TestOwnedSocket` (a socket in a folder others can write to was passed). With `Stat` for `Lstat` too, it fails at the link first | **caught by a unit test only** |
| The allowlist is a refusal list again | `sshValueVerdict`: an unknown keyword refused only when it is `proxycommand` | No e2e test: `TestSSHSplitRefusesALineBreakInAnOption` still fails at the control character check, and `TestSSHSplitRefusesAProxyCommand` at `proxycommand`. `TestParseRemoteLogin` fails 9 cases, `xauth location` and `unknown keyword` among them | **caught by the unit table only** |
| Only the group leader is read | `findRemoteLogin`: the group walk set to nil | `TestSSHSplitFindsSSHUnderANestedShell` (the fake ssh never ran a run 1) | **caught** |

## The pane navigator (choose_tree) and list-windows --all

The tests are in `navigator_test.go`. The target pane prints its marker in
two halves, so only its screen text holds the marker whole. The positive half
of the jump is a check that the focus was on another pane first, and Esc
closes the navigator with no switch in the same test. The grant test runs the
same listing from outside every pane first, where it must hold the other
session's text.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The action is not registered | `registerPrefixHandlers`: the `choose_tree` line cut | `TestNavigatorFindsAPaneByScreenText` ("the navigator never showed") | **caught** |
| The other sessions are never read | `OpenNavigator`: the `navigatorLoad` command not returned | `TestNavigatorFindsAPaneByScreenText` ("the search for needle-7781 never found it") | **caught** |
| The search skips screen text | `navigatorSearchRows`: the text match made false | `TestNavigatorFindsAPaneByScreenText` ("the search for needle-7781 never found it") | **caught** |
| Enter does not focus the pane | `NavigatorActivate`: the `focusWindowByID` call cut | `TestNavigatorFindsAPaneByScreenText` ("enter switched to work but left the focus on ...") | **caught** |
| The other machines are not listed | `navigatorSessions`: the `FederationHosts` loop given no hosts | `TestNavigatorListsAHostPane` ("the navigator never showed" far-shell @ build) | **caught** |
| The daemon does not check pane grants | `dispatchVerbLine`: the `checkGrants` call cut | `TestListWindowsAllHoldsToTheReadGrant` ("a pane holding read alone listed the other session") | **caught** |

### Review fixes for the navigator

`TestNavigatorTakesPastes` sets one known prompt in the shell and shows
first, with the navigator closed, that a paste reaches it. The stale-load,
cap and deadline tests use two seams: `TUIOS_E2E_HOLD_NAV` holds a load
until a file goes, and `TUIOS_E2E_NAV_CAPTURES` lowers the cap to one pane.
The verb client tests are unit tests of the deterministic race kind, in
`internal/session/verb_client_step_test.go`. The listing test is a unit test
of the security boundary kind, in `cmd/tuios/list_every_pane_test.go`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A paste passes the navigator | `pasteTakenByOverlay`: the `NavigatorOpen` block made false | `TestNavigatorTakesPastes` ("a paste in the navigator's list reached the shell: NAV> pz2") | **caught** |
| A stale load is applied | `ApplyNavigatorLoaded`: the `msg.Gen != nav.gen` check cut | `TestNavigatorDropsAStaleLoad` ("the first, cancelled load changed the tree after the second") | **caught** |
| No cap on the screens read | the loader: the cap compare made unreachable | `TestNavigatorSaysWhatItDidNotRead/cap` ("the pane past the cap does not say it was not read") | **caught** |
| No note for a session past the deadline | the loader: the "Did not answer in time" row cut | `TestNavigatorSaysWhatItDidNotRead/deadline` ("a session held past the deadline does not say so") | **caught** |
| A late reply is taken by the next call | `CallWithTimeout`: the `broken` check and the reply id check cut | `TestVerbClientRefusesAReplyOutOfStep` ("the second call returned {"for":"1"} after the first timed out"), `TestVerbClientRefusesAReplyForAnotherRequest` | **caught** |
| Host fields printed raw | `printListedPanes`: `plainLine` on the name and `plainText` on the text cut | `TestListWindowsAllHostsPrintsHostFieldsPlain` ("an escape reached the terminal") | **caught** |

### A scattered name outranks the screen text

`TestNavigatorLooks` failed in CI when a pane's id and its `t.TempDir`
folder spelled `needle-7781` out of order. Every field hit outranked every
text hit, so that pane took the cursor and the preview from logs. Now both
kinds of hit rank by the same fuzzy score. `TestNavigatorRanksScreenTextOverAScatteredName`
names a pane `need a ledge-77 81` so the case is fixed, not random. Its
positive half is in the same wait: the decoy is listed, so the search matched
it and only the order decides. The control was also run on the pre-fix
build, with the same failure.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A field hit outranks every text hit | `navigatorSearchRows`: the field hit's score given a lead of `1<<21` | `TestNavigatorRanksScreenTextOverAScatteredName` ("the search for needle-7781 did not put the cursor and the preview on logs") | **caught** |
| A text hit scores the first spread of the query | `navigatorSearchRows`: the `FindRun` loop replaced by `mt.Find(lq, line)` | `TestNavigatorScoresTheWholeOccurrence` ("the search for needle-7781 did not rank late, which shows it whole, above ...") | **caught** |

`TestNavigatorScoresTheWholeOccurrence` prints `navDecoy` and then the marker
on one line. `Find` aligns the query inside the shortest window that ends
first, so it scores the spread copy, which ties the decoy's name, and the
decoy wins the tie. The control frame lists late under the decoy.

### The navigator's look

The tests are in `navigator_preview_test.go`. `TestNavigatorLooks`, in
`navigator_look_test.go`, saves the frames a person looks at and asserts only
that each screen showed. The colour test reads the ink of the marker inside
the preview box only, so the coloured search line and list row cannot pass it.
No theme is on, so the pane's red and green reach the host as palette slots 1
and 2, which the chrome does not use. The inert test reads every byte tuios
wrote to the host, from the start, after the preview shows the pane's text.
`TestNavParseStyledPassesOnlySGR` is a unit test of the security boundary
kind, for what a daemon that is not tuios could send.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The other session's preview is plain text | `navigatorScreen`: `p.Text` drawn in place of `p.Styled` | `TestNavigatorPreviewKeepsColour` ("does not show RED-4417 in slot [1 9]") | **caught** |
| The attached session's preview is plain text | `navigatorScreen`: the live read through `pipCells` made unreachable | `TestNavigatorPreviewKeepsColour` ("does not show GRN-5523 in slot [2 10]") | **caught** |
| A link in the capture reaches the host | `navCellRow`: the `cell.Link` reset cut | `TestNavigatorPreviewIsInert` ("wrote "evil.example" to the host terminal"), `TestNavParseStyledPassesOnlySGR` ("let "evil" through") | **caught** |
| The capture is written as it came | `navParseStyled`: the raw line kept in place of the redrawn cells | `TestNavigatorPreviewIsInert` ("wrote "evil.example" to the host terminal") | **caught** |
| No tree guides | `navTreeGlyphs`: the branch and the closing guide made blank | `TestNavigatorDrawsTheTree` ("the logs pane has no guide") | **caught** |
| No bar on the cursor row | `navigatorItem`: the focus mark drawn as a blank | `TestNavigatorDrawsTheTree` ("0 rows carry the bar, want one, on work") | **caught** |
| No ground on the cursor row | `navigatorItem` and `renderNavigator`: the rows drawn on the surface and not through `pal.Row` | `TestNavigatorDrawsTheTree` ("the cursor row's ground ... is the ground of the other rows") | **caught** |
| A pane at its prompt names no command | `navPaneCommand`: the session's shell not used | `TestNavigatorDrawsTheTree` ("the logs pane does not say it runs sh") | **caught** |
| A made-up pane name is shown | `navPaneLabel`: the `isDefaultTitle` check cut | `TestNavigatorDrawsTheTree` ("a row shows a made-up pane name: ... Terminal 0ccfc9a8") | **caught** |

## Agents settings tab and the integration notice

`agents_settings_test.go` uses a temporary home with a Claude Code integration
aged from the current version to the one before, and a stand-in `claude` on
PATH. The positive halves are in the same fixtures: the same frame shows Codex
as not installed, the daemon's `list-agents` is read for the harness on both
panes before the "no second toast" half counts, and
`TestAgentsSettingsTabBeforeTape` is the switch-on twin of
`TestAgentsSettingsHiddenWithAgentsOff`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`7411942b`) | `TestAgentsSettingsUpdatesAndUninstalls` ("the prefix key opened no Agents tab"), `TestAgentsIntegrationNoticeOncePerRun` ("no toast for the out of date integration"), `TestAgentsSettingsHiddenWithAgentsOff` (no "Agent features are off" for the key), `TestAgentsSettingsTabBeforeTape` | **caught** |
| The tab is not on the page | `settingsCategories`: the `append(cats, m.agentsCategory())` line cut | `TestAgentsSettingsUpdatesAndUninstalls`, `TestAgentsIntegrationNoticeOncePerRun` (the palette entry opens no tab), `TestAgentsSettingsTabBeforeTape`. The off test passes, which is correct | **caught** |
| The prefix action is not registered | `prefix_actions.go`: the `prefix_agents_settings` Register line cut | `TestAgentsSettingsUpdatesAndUninstalls` ("the prefix key opened no Agents tab") | **caught** |
| The prefix key is not bound | `userconfig.go`: the `"prefix_agents_settings": {"A"}` default cut | `TestAgentsSettingsUpdatesAndUninstalls` (same) | **caught** |
| The report is never asked for | `Update`: the `agentsSyncCmd` call replaced by nil | `TestAgentsSettingsUpdatesAndUninstalls`, `TestAgentsIntegrationNoticeOncePerRun`, `TestAgentsSettingsTabBeforeTape` | **caught** |
| The report never lands | `handleMsg`: the `agentsOverviewMsg` case cut | the same three | **caught** |
| An action's result never lands | `handleMsg`: the `agentsActionMsg` case cut | `TestAgentsSettingsUpdatesAndUninstalls` ("the update said nothing") | **caught** |
| A click on a status row does nothing | `overlayRowClick`: the `controlStatus` case cut | `TestAgentsSettingsUpdatesAndUninstalls` ("a click on the row opened no update action") | **caught** |
| Esc closes the page from the action rows | `handleSettingsInput`: the `SettingsBack` call cut | `TestAgentsSettingsUpdatesAndUninstalls` ("esc did not go back to the list") | **caught** |
| No palette entry | `command_palette.go`: the entry renamed away from `paletteAgentsSettingsName` | `TestAgentsIntegrationNoticeOncePerRun` ("the palette has no Agents settings entry") | **caught** |
| The tab ignores the agent switch | `settingsCategories`: `agentsPageAvailable()` replaced by true | `TestAgentsSettingsHiddenWithAgentsOff` ("the tab before Tape is not Hosts") | **caught** |
| No notice | both `noticeAgentIntegrations` calls cut | `TestAgentsIntegrationNoticeOncePerRun` ("no toast") | **caught** |
| The notice is not once per harness | `noticeAgentIntegrations`: the `panesSeen` and `noticed` checks cut | `TestAgentsIntegrationNoticeOncePerRun` ("esc did not dismiss the toast": it comes back at once) | **caught** |
| The doctor footer counts only missing integrations | `doctorAgents`: `st.State() == StateInstalled` back to `st.Installed` | `TestAgentsIntegrationNoticeOncePerRun` ("the doctor does not name the pane with the out of date integration") | **caught** |

### Review fixes

`TestAgentsSettingsKeepsTheCommandPath` installs with `--command` and the full
path of the binary under test, current and aged.
`TestAgentsSettingsAbsentOverSSH` attaches through `tuios ssh` to the same
fixture. Its positive halves are `TestAgentsSettingsTabBeforeTape` (the menu
line and the tab) and `TestAgentsIntegrationNoticeOncePerRun` (the palette),
with a local client. The notice test now also starts a second client before
the dismissal, which shows the toast, and a third after it, which does not.
The race tests in `internal/integration/file_changed_test.go` change the file
just before the last check, through a test hook.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`52361999`) with this branch's e2e directory | `TestAgentsSettingsKeepsTheCommandPath`, `TestAgentsSettingsTabBeforeTape` (no menu line), `TestAgentsIntegrationNoticeOncePerRun`. `TestAgentsSettingsAbsentOverSSH` passes, which is correct: main has no tab anywhere | **caught** |
| A --command install reads as out of date | `Status`: the `installedPrograms` loop given no programs | `TestAgentsSettingsKeepsTheCommandPath` current ("the row does not say installed") and aged ("the update did not keep ... as the program") | **caught** |
| An update writes a bare tuios | `runAgentAction`: `st.InstallProgram(agentsCommand)` back to `agentsCommand` | `TestAgentsSettingsKeepsTheCommandPath` aged | **caught** |
| The tab on a remote client | `agentsPageAvailable`: the `ClientLocal` and `RemoteClient` checks cut | `TestAgentsSettingsAbsentOverSSH` ("the prefix menu lists the Agents settings key over ssh") | **caught** |
| A dismissal is not stored | `DismissNotifications`: the `noteNoticesDismissed` call cut | `TestAgentsIntegrationNoticeOncePerRun` ("the dismissal was never stored") | **caught** |
| A stored dismissal is not read | `loadSidebarState`: the line that fills `agentNoticesDismissed` cut | `TestAgentsIntegrationNoticeOncePerRun` ("a new client showed the dismissed toast again") | **caught** |
| The suite keeps a harness directory override | `runE2E`: the `harnessDirKeys` unset loop cut, run with `CLAUDE_CONFIG_DIR` exported to a scratch directory | `TestAgentsSettingsUpdatesAndUninstalls` fails in its fixture, and the install landed in the scratch directory. With the loop in place the same run passes and the scratch directory stays empty | **caught** |
| A save during an edit is overwritten | `writeAtomic`: the check after `beforeRename` cut | `TestInstallKeepsASaveMadeWhileItWrites`, `TestInstallReportsAFileThatKeepsChanging`, `TestInstallChecksTheTargetOfASymlink` | **caught** |

Not covered end to end: skipping panes on another machine in the notice and in
`tuios doctor agents`. A pane with a host needs a federation link, which this
fixture does not set up.

### Second review

`TestAgentsNoticeOutlastsAnEsc` presses esc in terminal mode once the toast
shows, checks that nothing was stored, and attaches again: the toast comes
back. It is also the positive half of the stored dismissal, which
`TestAgentsIntegrationNoticeOncePerRun` now makes with a click on the dismiss
end of the toast. `TestInstallNamesWhatAPartialWriteChanged` changes Hermes's
`config.yaml`, the last of its three files, before each rename.
`TestE2EClearsEveryDirOverride` reads `e2e/tui/harness_test.go` from the main
module.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| Esc stores a lasting dismissal | `DismissNotifications`: the `noteNoticesDismissed` call put back | `TestAgentsNoticeOutlastsAnEsc` ("an esc stored a lasting dismissal") | **caught** |
| A click on the dismiss end stores nothing | `NotificationClick`: the `noteNoticesDismissed` call cut | `TestAgentsIntegrationNoticeOncePerRun` ("the dismissal was never stored") | **caught** |
| A partial write reads as nothing changed | `retryChanged`: the message that names the files made unreachable | `TestInstallNamesWhatAPartialWriteChanged` | **caught** |
| The e2e list misses an override | `harnessDirKeys`: `QWEN_HOME` cut | `TestE2EClearsEveryDirOverride` ("reads QWEN_HOME ... neither clears it nor redirects it") | **caught** |

Not covered by a test: the fresh report a new harness pane asks for, the merge
of dismissals on save, the uninstall row for an install with only the MCP
server or the status line left, and the doctor's note for a session whose
hosts it could not read.

## A new session on a host ignores startup.tiled (#480)

`TestRemoteCreateAppliesStartup` in `sessionizer_test.go` uses the second
daemon and the ssh stand-in of the host tests. This client has `[startup]
tiled = true`. The host has no config, so the tiling comes from the config of
the client. From a tiled session, `switch-session --create build:far-new` must
make a tiled session on the host. `tuios new --host build far-fresh` is checked
in the same fixture. It already passed on main, so it is the positive half: the
host and the check of the tiling work.

`TestSwitchToAnEmptySessionTakesItsLayout` in the same file checks the layout
mode. `blank` is master-stack and empty, and `home` is BSP. The client goes
from `home` to `blank` and switches workspace there, which sends its layout to
the daemon. `blank` must stay master-stack. Its positive half is `home`: the
same client reports BSP there.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`b23b1cd6`) with this branch's e2e directory | `TestRemoteCreateAppliesStartup` ("far-new on build is \"floating\""), `TestSwitchToAnEmptySessionTakesItsLayout` ("blank has layout mode \"bsp\"") | **caught** |
| The empty session keeps the tiling of the session left | `adoptEmptySessionVersion`: the `m.AutoTiling = state.AutoTiling` line cut | `TestRemoteCreateAppliesStartup` (the same assertion). `TestSwitchToAnEmptySessionTakesItsLayout` passes, which is correct: tiling is on in both sessions | **caught** |
| The empty session keeps the layout mode of the session left | `adoptEmptySessionVersion`: the `ApplyLayoutModeName` call cut | `TestSwitchToAnEmptySessionTakesItsLayout` ("blank has layout mode \"bsp\""). `TestRemoteCreateAppliesStartup` passes, which is correct: [startup] sets the mode there | **caught** |

## A popup that switches the session (#479)

`TestPopupThatSwitchesSessionCloses` in `popup_test.go` opens a popup whose
command runs `tuios switch-session --create away` and exits. The popup must
leave the window list of `home`, where no client is attached when it exits.
Its positive half is in the same fixture: a popup that exits without a switch
leaves the list too.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`b23b1cd6`) with this branch's e2e directory | `TestPopupThatSwitchesSessionCloses` ("the popup ... is still in home after its command exited", after the positive half passed) | **caught** |
| The daemon does not close an exited popup | `verbPopup`: the `closeWindowOfPTY` call in `onExit` cut | `TestPopupThatSwitchesSessionCloses` (the same assertion) | **caught** |

`TestPopupWaitAfterTheDaemonClosedIt` in `internal/session/popup_wait_test.go`
is a race regression for the close. The daemon closes the popup, and with it
the PTY, when the command exits. `popup --wait` must still return the exit
status. A test-only hook, `popupBeforeWaitHook`, holds the wait until the PTY
is gone, so the order is the same on every run.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The wait reads only the PTY | `verbPopup`: `waitPopupExit` given `nil` in place of the status kept by `onExit` | `TestPopupWaitAfterTheDaemonClosedIt` ("exit_code:-1 ... want exit_code 4") | **caught** |

## A session's start directory across a restart (#478)

`TestRestoredSessionKeepsItsStartDirectory` makes a session with
`tuios new --cwd`, runs `tuios kill-server`, waits for the new daemon to
restore the session, and opens a window on an empty workspace with
`tuios new-window --workspace 3`. Inheritance is off in the config, and the
only pane moves out of the project first, so the start directory is the only
way the window can land in the project. The daemon runs in `base/cwd`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`a596a20b`) with this branch's e2e directory | `TestRestoredSessionKeepsItsStartDirectory` (the window is in `base/cwd`) | **caught** |
| The save leaves the directory out | `ResurrectionState`: the `state.StartDir = s.StartDir()` line cut | `TestRestoredSessionKeepsItsStartDirectory` | **caught** |
| The restore does not set it | `restoreSessionOffers`: the `sess.SetStartDir(state.StartDir)` call cut | `TestRestoredSessionKeepsItsStartDirectory` | **caught** |

### Review fixes

`TestStartDirectoryOutlivesARestartWhileMissing` moves the project folder
away, restarts the daemon (the restore runs with the folder gone), restarts it
again (`kill-server` saves the session first), moves the folder back, restarts
a third time, and opens a window on an empty workspace.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A missing folder is dropped on restore | `restoreSessionOffers`: the `checkWindowCwd` guard put back around `SetStartDir` | `TestStartDirectoryOutlivesARestartWhileMissing` (the window is in `base/cwd`) | **caught** |

Not covered end to end: `absStartDir` on the create paths. The CLI sends an
absolute folder already, so only a raw verb call can send a relative one.

## A pane on a switch to an empty workspace (#477)

`new_window_when_empty_test.go` turns on `workspaces.new_window_when_empty`.
`TestEmptyWorkspaceOpensAPane` checks that startup opens no pane, then
switches by key to workspace 2 (one pane, in the folder of the pane it came
from), exits that pane and switches to workspace 5 (one pane, in the
session's start folder), presses `move_and_follow_7` (workspace 7 holds only
the moved pane), runs `tuios xpanes` (its workspace holds two panes), and
switches with `run-command SwitchWorkspace` and `select-workspace` (no pane).
Inheritance is off, so the folder has one way to be chosen.
`TestEmptyWorkspaceOpensOnePaneForTwoClients` switches one of two attached
clients and counts one pane. `TestEmptyWorkspacePaneSettingReloads` switches
with the setting off (no pane), turns it on in the file and switches again
(one pane). Every count waits until the client that took the keys reports
no pane request in flight (`run-command GetSessionInfo`, `pane_requests`), then
counts again. A request clears when a sync brings its pane, so the daemon has
made every pane the client asked for. The two client test asks each client.
The rows below were run again on the reviewed tree, and each fails as listed.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`a596a20b`) with this branch's e2e directory | all three ("workspace 2 has 0 panes, want 1", "no switch ... opened a pane after the reload") | **caught** |
| The switch does not open a pane | `SwitchToWorkspace`: the `openPaneOnEmptyWorkspace` call cut | all three | **caught** |
| The daemon ignores the source pane | `handleExecuteCommand`: the `session.cwdFrom(payload.CwdFrom)` line cut | `TestEmptyWorkspaceOpensAPane` (the pane is in `start`, want `elsewhere`) | **caught** |
| A move that brings its pane also gets one | `openPaneOnEmptyWorkspace`: the loop that returns on a workspace with panes cut | `TestEmptyWorkspaceOpensAPane` ("move_and_follow to workspace 7: workspace 7 has 2 panes"), `TestEmptyWorkspaceFastSwitches` ("workspace 1 has 2 panes") | **caught** |
| A script switch opens a pane | `OS.SwitchWorkspace` (tape and run-command): back to `SwitchToWorkspace` | `TestEmptyWorkspaceOpensAPane` ("run-command SwitchWorkspace: workspace 4 has 1 panes") | **caught** |
| Every client that follows a switch opens a pane | injected: `ApplyStateSyncFrom` calls `openPaneOnEmptyWorkspace` after it adopts the workspace | `TestEmptyWorkspaceOpensAPane` ("tuios xpanes: workspace 2 has 3 panes"), `TestEmptyWorkspaceOpensOnePaneForTwoClients` ("workspace 2 has 2 panes") | **caught** |

Not covered end to end: a client attached to a session on another machine,
a session without a daemon (the pane starts in the folder of the source
pane's shell, read by this client), and the settings page row.

### Review fixes: fast switches

`TestEmptyWorkspaceFastSwitches` sends Alt+2 Alt+1, then Alt+2 Alt+1 Alt+2,
then Alt+2 Alt+3, each as one write. `TUIOS_E2E_HOLD_PANE` makes the daemon
hold each pane request for a second, so the pushes the later keys make queue
behind it. Once no request is in flight it checks the workspace the session
and the client show (1, 2, 3) and the panes on each workspace (one each).

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The pane counts as a focus move | `AddDaemonWindowWith`: the `s.focusNeutral = true` line cut | `TestEmptyWorkspaceFastSwitches` ("Alt+2 Alt+1: the session shows workspace 2, want 1") | **caught** |
| The daemon focuses the pane and moves the session to it | `handleExecuteCommand`: `FocusIfShown: true` changed to `Focus: true` | `TestEmptyWorkspaceFastSwitches` ("Alt+2 Alt+1: the session shows workspace 2, want 1") | **caught** |
| A switch back asks for a second pane | `openPaneOnEmptyWorkspace`: the `paneRequests` check cut | `TestEmptyWorkspaceFastSwitches` ("Alt+2 Alt+1 Alt+2: workspace 2 has 2 panes, want 1") | **caught** |
| The request holds back the client's pushes | `openPaneOnEmptyWorkspace`: `m.daemonWindowIntent = true` put back after the request | none: `TestEmptyWorkspaceFastSwitches` passes. With the pane focus neutral, the session still ends on workspace 1 | **not caught** |
| The build before the review | `fe57ce01` | none: it has no `TUIOS_E2E_HOLD_PANE`, so its daemon answers before the next key, and it has no `pane_requests` | **not caught** |

Not covered end to end: the version check (`EmptyWorkspacePanes` in the
welcome) against a daemon from before this change, and a switch from a
scratch terminal.

### Second review: one pane per workspace at the daemon

`TUIOS_E2E_HOLD_PANE` now acts only with `TUIOS_E2E=1`, and a file that holds
`refuse` makes the daemon fail the request.
`TestEmptyWorkspaceSlowRequestOpensOnePane` holds the first request for 7 s,
waits 5.5 s, and sends Alt+1 Alt+2, so the client asks again after its 5 s
timeout. `TestEmptyWorkspaceTwoClientsSwitchAtOnce` holds requests for 2 s:
the first client switches to 2, and the second follows, then sends Alt+1
Alt+2, so both clients ask. `TestEmptyWorkspaceFailedRequestKeepsTheSwitch`
refuses the request for Alt+2, presses Alt+1, and waits for the session to
show workspace 1 with no pane on 2. `TestEmptyWorkspacePaneFollowsSSH` turns
on `appearance.new_window_follow_ssh`, runs the fake ssh in the pane on 1,
and switches to 2: the new pane runs the same ssh.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The daemon opens a second pane on a workspace | `AddDaemonWindowWith`: the `ErrWorkspaceHasPane` check cut | `TestEmptyWorkspaceSlowRequestOpensOnePane` ("workspace 2 has 2 panes, want 1"), `TestEmptyWorkspaceTwoClientsSwitchAtOnce` ("workspace 2 has 2 panes, want exactly 1") | **caught** |
| The client does not ask to follow ssh | `openPaneOnEmptyWorkspace`: `sshFrom` not sent | `TestEmptyWorkspacePaneFollowsSSH` ("the fake ssh never ran a run 1") | **caught** |
| The daemon drops the followed ssh | `handleExecuteCommand`: `Command` not passed to `AddDaemonWindowWith` | `TestEmptyWorkspacePaneFollowsSSH` ("the fake ssh never ran a run 1") | **caught** |
| A request holds back the client's pushes | `openPaneOnEmptyWorkspace`: `m.daemonWindowIntent = true` put back | none: `TestEmptyWorkspaceFailedRequestKeepsTheSwitch` passes. A later push from the same client reaches the daemon within the wait | **not caught** |

Not covered end to end: `paneRequests` cleared on a session switch, an
attach and a reconnect, and the `TUIOS_E2E=1` gate itself.

## A session made from the session switcher ignores [startup] tiled (#488)

`TestSwitcherCreateAppliesStartup` in `switcher_create_startup_test.go` sets
`[startup] tiled = true`, opens the session switcher, types `fresh`, a name
no session has, and presses Enter. `fresh` must be tiled, and its first
window must fill the session. The same client then runs the palette's
"New session", which came up tiled on main too: it is the positive half.
`TestRailNewSessionComesUpTiled` covers the rail's "+" and runs with it.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`5d627b1e`) with this branch's e2e directory | `TestSwitcherCreateAppliesStartup` ("fresh is \"floating\" with 0 windows") | **caught** |
| The switcher skips the shared switch | `handleSessionSwitcherInput`: `OpenOrCreateSession` back to a bare `SwitchToSession` | `TestSwitcherCreateAppliesStartup` (the same assertion). `TestRailNewSessionComesUpTiled` passes | **caught** |
| The shared switch does not apply [startup] | `openSession`: the `applyStartupToUnarranged` call cut | `TestSwitcherCreateAppliesStartup` (the same assertion) | **caught** |
| The create handler does not apply [startup] | `SessionCreatedMsg` in `Update`: the `applyStartupToUnarranged` call cut | `TestSwitcherCreateAppliesStartup` ("session-0 is \"floating\" with 1 windows", after `fresh` passed), `TestRailNewSessionComesUpTiled` (the pane is 46 wide) | **caught** |

Not covered end to end: `createRemoteSession`, the picker's create on another
machine, which now uses the same rule. `TestRemoteCreateAppliesStartup`
covers `switch-session --create` and `tuios new --host` there.

## Workspace label cap and tab format

`dock_workspace_label_max_test.go` drives the dock's workspace pill through
`appearance.dock_workspace_label_max` and `appearance.dock_workspace_tab_format`
with a 26-character workspace name. The positive half is in each fixture: the
capped cases show a truncated pill, the uncapped wide case shows the whole name,
and the custom-format case shows the name inside its brackets.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The tab format is dropped when it has no `{index}` | `workspacePillLabel`: the `strings.Contains("{index}")` branch restored, passing the raw name for formats without `{index}` | `TestDockWorkspaceLabelCapAndFormats/custom_format` ("the dock row dropped the tab format around the capped name") | **caught** |
| A narrow dock with the cap off draws no pill at all | `planDockWorkspaceStrip`: the `count == 0` fallback replaced by `addOnlyStrip` | `TestDockWorkspaceLabelCapAndFormats/uncapped_narrow` ("the narrow dock draws no pill at all for the long workspace") | **caught** |
| Minimized names take a live message's room | `shortenDockItemNames`: the budget search replaced by the earlier even split, which subtracts each entry's padding and never counts the label's own " 1: " around the name | `TestDockWorkspaceLabelCapAndFormats/live_message_keeps_its_columns` ("the live message never drew in full on the dock row"): at 140 columns the drawn entries run wider than the room the pass was given, and the message is cut to `Window…  more` | **caught** |

## A new window ignores the OSC 7 folder on Windows (#491)

`TestNewWindowInheritsTheOSC7Folder` in `inherit_cwd_osc7_test.go` starts the
daemon with `TUIOS_E2E=1` and `TUIOS_E2E_NO_PROCESS_CWD=1`, which makes every
process read fail, as it does on Windows, and with
`COMPUTERNAME=TUIOS-E2E-PC`. The pane's shell is `/bin/sh`, which reports
nothing, and the test prints each report. The session starts in
`base/start`. From the focused pane each time:

- an OSC 7 report with this machine's host name in capitals: the new window
  must start there. This is the positive half, and main with the hook passes
  it.
- an OSC 7 report with the host `TUIOS-E2E-PC`: the new window must start
  there.
- an OSC 7 report of a folder that does not exist: the new window must start
  in `base/start`.
- an OSC 9;9 report with a quoted path: the new window must start there.

`TestNewWindowPrefersTheShellsProcessFolder` is the review's regression,
with process reads on. A program in a `/bin/sh` pane prints OSC 7 for
`base/fake`, which exists, and the person then runs `cd base/real`. The new
window must start in `base/real`. The daemon holding the report is the
positive half.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`e82f3bbe`) with this branch's e2e directory | `TestNewWindowInheritsTheOSC7Folder` (the missing folder: the window is in `other`, the process folder, want `start`). Main has no hook, so its process read answers. `TestNewWindowPrefersTheShellsProcessFolder` passes, as it must: it guards main's order | **caught** |
| The released behaviour, with process reads off | origin/main (`5d627b1e`) with only the `ptyspawn` hook added | `TestNewWindowInheritsTheOSC7Folder` (the `TUIOS-E2E-PC` report is dropped: the pane stays in `proj`, want `other`) | **caught** |
| OSC 7 wins over the process | `windowCwd`: the OSC 7 branch put back before the process read, as the first version of this change had it | `TestNewWindowPrefersTheShellsProcessFolder` (the window is in `fake`, want `real`) | **caught** |
| COMPUTERNAME is not a local name | `localHostNames`: COMPUTERNAME not passed to `hostNameSet` | `TestNewWindowInheritsTheOSC7Folder` (the `TUIOS-E2E-PC` report is dropped) | **caught** |
| A record that names a missing folder is inherited | `windowCwd`: the `isLocalDir` check on `win.Cwd` cut | `TestNewWindowInheritsTheOSC7Folder` (the window starts in the daemon's folder, want `start`) | **caught** |
| Only the process is asked | `windowCwd`: the record cut (first version of this change) | `TestNewWindowInheritsTheOSC7Folder` (the first window starts in `start`, want `proj`). This proves the hook turns process reads off | **caught** |
| OSC 9;9 is a notification | `handleNotify9`: the 9;9 branch cut | `TestNewWindowInheritsTheOSC7Folder` (the pane stays in `start`, want `nine`) | **caught** |
| The quotes stay on the OSC 9;9 path | `parseCwdAnnouncement`: the quote strip cut | `TestNewWindowInheritsTheOSC7Folder` (the same assertion) | **caught** |

`TestHostNameSetIsNarrow` in `internal/session/session_place_test.go` pins
the host rule, a security boundary: a short name matches only on darwin,
only for a dotted host name, and only as its whole first label.

`TestPickWindowCwdTrustsAReadableProcess` in the same file pins the second
review's case: the record, which holds the pane's report, is taken only when
no process can be read. A shell whose folder was deleted is readable, and
then the window gets the start folder, not the report.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A deleted process folder falls back to the report | `pickWindowCwd`: the `return ""` after an unusable process folder cut | `TestPickWindowCwdTrustsAReadableProcess` ("a deleted process folder does not fall back to the report") | **caught** |

Not covered end to end: a shell in a deleted folder, a real Windows machine, ConPTY, the short-name match
on macOS, and OSC 9;9 on the libghostty-vt backend, which reads it the same
way in its own parser.

### The slash in front of the drive (#491, second report)

The reporter's `list-windows` showed the folder `\C:\dev\x` for the report
`file://NOTE238/C:/dev/x`: the URL path keeps a slash in front of the drive.
`winpath.FileURL` is now the one reader of an OSC 7 URL, on the client and in
the daemon, and it drops that slash. `winpath.ForOS` turns the folder into a
path for the daemon's OS, and refuses a drive path on any OS but Windows.

`TestParseCwdOnEachOS` in `internal/session/session_place_test.go` runs the
parser for a Windows daemon and for a Linux one on any OS: the reporter's URL,
`file:///C:/x`, percent escapes, OSC 9;9 in both slashes and quoted, UNC paths,
and reports from another machine. It also passes as a Windows binary under
wine. A UNC report, `file://server/share/x`, is read as the folder `/share/x`
on the machine `server`, because OSC 7 names the machine the shell runs on.

`TestSSHSplitIntoWindowsSendsNoDriveFolder` in `ssh_split_test.go` is the
case Linux can show: the far shell reports `file://fakehost/C:/Users/pollen/my%20app`,
and an ssh split must run plain ssh. Before, it ran `cd "/C:/Users/pollen/my app"`
through `sh`, which a Windows ssh server does not have.
`TestSSHSplitKeepsTheRemoteFolder` is the positive half: a POSIX folder from
the same far machine still gets its `cd`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The drive slash stays in the URL path | `dropDriveSlash`: `return p[1:]` made `return p` | `TestSSHSplitIntoWindowsSendsNoDriveFolder` (ssh ran `cd "/C:/Users/pollen/my app"`), `TestParseCwdOnEachOS` (the far-machine rows read `/C:/dev/x`, and Linux takes `file:///C:/x` as a local folder) | **caught** |
| No drive slash is dropped anywhere, as in v0.8.5 | the same, and `FromPOSIX` no longer trims the slash before its drive check | `TestParseCwdOnEachOS` (every Windows row with a drive: the reporter's URL is refused, want `C:\dev\tuios_0.8.5_Windows_x86_64`) | **caught** |
| A UNC folder gets a cd | `safeRemoteDir`: the `//` refusal cut | `TestSSHSplitIntoWindowsSendsNoDriveFolder/unc` (ssh ran `cd "//srv/share"`) | **caught** |

The review's second round added three things. `safeRemoteDir` refuses a
folder that starts with `//`, which is the UNC folder of a Windows shell
(`file://winbox//srv/share`), and the `unc` row of
`TestSSHSplitIntoWindowsSendsNoDriveFolder` covers it. `FileURL` refuses a
path that decodes to a control character (`%00`, `%1b`, `%0a`), and
`TestParseCwdOnEachOS` has rows for those and a positive row for `%C3%A9`.
`hintCwdHost` reads the host with `url.Parse` again, so a report with no
path, `file://far`, still names another machine and still blocks opening a
path here.

Not covered end to end: a real Windows machine. The unit table and the wine run
stand in for it.

## A bare attach picks the session the person used last (#486)

A bare `tuios attach` lands on the session the person used last: the
latest `lastUsed`, then the latest activity. Only `MsgSessionUsed` sets
`lastUsed`. A client sends it for input from the terminal it runs in, at most
once a second per session, and only to a daemon whose welcome set
`SessionUsed`. The daemon takes it only from a client that may act as the
person. Pane input (`MsgInput`) is activity only: a routed `send-keys`, a
tape and a focus report reach the pane that way too. The client reports
input before it handles it, so after a key or a click that switches sessions
it reports once more for the session moved to. `lastUsed` is saved with the
session, at the latest 30 seconds after a use, and restored.

The tests are in `attach_most_recent_test.go`. A client that must not count
as the person is started with no key pressed: `attachIn` presses Alt+Esc,
which is the person using the session.

- `TestBareAttachLandsOnTheSessionTypedInLast` makes four sessions. Each of
  five rounds attaches a client to one session by name and uses it, then a
  bare attach must land there. Rounds 1, 3 and 5 type a command into the
  pane. Rounds 2 and 4 press Alt and a number in window mode.
- `TestBareAttachIgnoresAgentWindowsAndRoutedCommands` has a client sit on
  `watched`. The person types in `person`. Then windows open in `agent`
  from the CLI and `select-workspace` is routed to the client on `watched`.
  A bare attach must land on `person`. The positive half comes first:
  before any typing, the agent's windows make `agent` the pick.
- `TestBareAttachIgnoresKeysSentToATerminalModeClient` is the second
  review's case. A client sits on `watched` in terminal mode. The person
  types in `person`, then `tuios send-keys -s watched` types a command, which
  runs in the pane there. A bare attach must land on `person`.
- `TestBareAttachFollowsASwitchFromTheSessionBrowser` is the third review's
  case. The person attaches to `first`, switches to `second` in the session
  browser, reads it with no key, and detaches. A bare attach must land on
  `second`. Every key the person pressed was used in `first`, which is the
  positive half.
- `TestBareAttachAfterARestartLandsOnTheSessionTypedInLast` types in `alpha`
  of `alpha`, `beta` and `gamma`, runs `kill-server`, and a bare attach that
  starts the daemon again must land on `alpha`.

The fixed build passed three runs in a row.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`0291286b`) with this branch's e2e directory | `TestBareAttachLandsOnTheSessionTypedInLast` ("round 1: ... landed on \"recent-a\", want \"recent-c\""). The other three pass by the luck of map order on this run | **caught** |
| The previous version of this change, which counted pane input as use | build `2e34219e` | `TestBareAttachIgnoresKeysSentToATerminalModeClient` ("landed on \"watched\", want \"person\"") | **caught** |
| The pick by activity | `GetDefaultSession`: `LastUsedSession` back to `MostRecentSession` | `TestBareAttachIgnoresAgentWindowsAndRoutedCommands` ("landed on \"agent\", want \"person\"") | **caught** |
| The daemon drops the report | `handleMessage`: `MsgSessionUsed` answered with nil | `TestBareAttachIgnoresAgentWindowsAndRoutedCommands` (the same assertion) | **caught** |
| The welcome does not offer it | `SessionUsed: false` in the welcome | `TestBareAttachIgnoresAgentWindowsAndRoutedCommands` (the same assertion) | **caught** |
| Pane input counts as use | `handleInput`: `TouchActive` back to `TouchUsed` | `TestBareAttachIgnoresKeysSentToATerminalModeClient` ("landed on \"watched\", want \"person\"") | **caught** |
| The client reports every message | `reportActivity`: `ReportUsed` called for any message, not only terminal input | `TestBareAttachIgnoresAgentWindowsAndRoutedCommands` (an untouched client makes `watched` the pick before any typing) | **caught** |
| The save leaves the time out | `ResurrectionState`: `LastUsed` not stamped | `TestBareAttachAfterARestartLandsOnTheSessionTypedInLast` ("landed on \"gamma\", want \"alpha\"") | **caught** |
| The restore drops the time | `restoreSession`: `setLastUsed` cut | `TestBareAttachAfterARestartLandsOnTheSessionTypedInLast` (the same assertion) | **caught** |
| No report for the session moved to | build `50c78158`, the head before the report on a session change | `TestBareAttachFollowsASwitchFromTheSessionBrowser` ("landed on \"first\", want \"second\"") | **caught** |

Not covered end to end: the save mark in `TouchUsed`, since the attach that
precedes the typing already marks the session for a save, a crash, a client
inside a pane, and a mix of an old client with a new daemon.

## A pane reports a pixel size of zero (#506)

A Textual app quit with `ZeroDivisionError` when the mouse entered its pane.
Textual turns on in-band resize reports (mode 2048) and then SGR-pixel mouse
reports (mode 1016). It takes pixels per cell from the 2048 report and divides
each mouse report by it. The pure Go emulator sent `0;0` for the pixels in
every 2048 report. libghostty-vt sent no 2048 report and no XTWINOPS 14, 16 or
18 answer, because tuios never gave it the size callback. A pane that no
client had measured, such as a detached session's, also had a TIOCGWINSZ pixel
size of zero.

The fix takes every pixel size from one cell: the host's, or the fallback cell
in `internal/cellsize` (10x20) before a client reports one. The 2048 report,
XTWINOPS 14 and 16, the pty's winsize from its spawn on, and the 1016 mouse
reports all use it. A guest with 2048 on gets a new report when the cell size
changes.

The guest in `pixel_size_test.go` reads TIOCGWINSZ, the 2048 report and
XTWINOPS 14 and 16, and prints each SGR-pixel mouse report.
`TestPaneReportsItsPixelSize` runs it in a standalone and a daemon session
with an 8x16 host cell. Every size must match that cell, and two hovers one
cell apart must be reported 8 pixels apart. `TestDetachedPaneReportsAPixelSize`
runs it in a detached session: the sizes must match the fallback cell. A
client with an 8x16 cell then attaches, and the guest's last 2048 report must
be in 8x16 cells. A real Textual 8.2.8 app, run the same way, quit with the
reported traceback on main and mapped six hovers to six consecutive cells with
the fix, on both backends. That run is not in the suite, because CI has no
Textual.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`951dc83e`) | `TestPaneReportsItsPixelSize` (both: the 2048 report is `36;118;0;0`), `TestDetachedPaneReportsAPixelSize` (TIOCGWINSZ is 0x0 pixels, the 2048 report is `22;78;0;0`) | **caught** |
| The 2048 report has no pixels | `sendInBandResize`: pixels written as 0 | `TestPaneReportsItsPixelSize` (both), `TestDetachedPaneReportsAPixelSize` | **caught** |
| No pixel fallback in the winsize | `pixelsOr`: the pixel size passed through as given | `TestDetachedPaneReportsAPixelSize` (TIOCGWINSZ is 0x0 pixels) | **caught** |
| No pixels at spawn | `SpawnTTY`: the first `SetWinsize` cut | `TestDetachedPaneReportsAPixelSize` (TIOCGWINSZ is 0x0 pixels) | **caught** |
| libghostty-vt has no size callback | ghostty build, `WithSizeReport` returns false | `TestPaneReportsItsPixelSize` (both: no 2048, 14 or 16 answer), `TestDetachedPaneReportsAPixelSize` | **caught** |
| A new cell size is not sent to a 2048 guest | `Emulator.SetCellSize`: the report cut | none end to end: the attach also resizes the pane, and that report carries the new cell. `TestConform_PixelSizeReports` in `internal/vt` catches it | **unit only** |

`TestGhosttyDiffPixelSizeReports` (`-tags ghostty`) compares the two backends'
answers to 14, 16, 18 and 2048, with and without a host cell, and after a new
cell size.

Not covered end to end: macOS, a host that reports its cell size itself
rather than through `TUIOS_CELL_SIZE`, and a resize of a detached pane, which
goes through the winsize fallback.

### Review fixes: one client's cell, and no repeated reports

The pane's cell flipped between clients with different fonts: each client set
it on its own attach and its own pane resizes. It is now one client's cell
(`sessionCellSize`). Under `window_size = latest` that is the latest client,
which owns the session's size. Under smallest and largest, no client owns the
size, so it is the client that attached first. A client that reported no cell
is skipped. Every change to who is attached, and to who owns the size, goes
through `recalculateAndBroadcastSize`, which now gives every pane that cell.

`TestPaneCellFollowsOneClient` starts the guest in a detached session with the
fallback cell. Client a attaches with an 8x16 cell and client b with 12x24.
Every 2048 report the guest gets must stay in a's cell. When a leaves, the cell
must move to b's, which is the positive half.

The ghostty backend now ignores the same cell set twice. The library sends a
2048 report on every resize, and the daemon sets the cell on every pane resize
and every attach. `Emulator.SetCellSize` ignores a zero cell, as the ghostty
backend does. `TestPaneReportsItsPixelSize` also checks the absolute x of the
first hover: the centre of the pane cell under it, from the pane's first
column on the screen.

The 2048 reports are not held back during a drag with the winsize hold. The
pure emulator sends its report when it applies the resize in the output
stream, and libghostty sends its own from inside its resize. Holding them back
needs a hold in both backends.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| Each client sets its own cell | the reviewed build, `b4868417` | `TestPaneCellFollowsOneClient` (the guest was told a 12x24 cell, then 8x16 again) | **caught** |
| The last client to attach wins | `sessionCellSize`: the highest `attachSeq` picked | `TestPaneCellFollowsOneClient` (the guest was told a 12x24 cell) | **caught** |
| ghostty passes the same cell through | `GhosttyTerminal.SetCellSize`: the same-cell return cut | `TestGhosttyDiffPixelSizeReports` ("the same cell twice sent ... ghostty=\"\x1b[48;5;20;80;160t\"") | **caught** |

## The rail's terminals header runs into the peeked name

While the pointer peeks a session row, the terminals header names that session
on its right, in front of the add control. The name was sized from half the
rail, which ignored the label. On a narrow rail the header read
"terminalssession-1", and at some widths the add control moved one cell off its
spine. `sidebarHeaderRow` now keeps two blank cells (`sidebarHeaderGap`) after
every header label and cuts the right element from its front. The terminals
header sizes the name from the room the row really has, and shows no name when
fewer than three cells are left. The add, cd and agents controls refuse to draw
closer than the same gap.

`TestRailHeaderKeepsAGapBeforeThePeekedName` peeks a session with a long name
at rail widths 16, 18, 20 and 24. It checks the gap after "terminals", that the
header names the session at 20 and up (the positive half), that the add control
is on its spine, and that a click on it makes a pane.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The build before the fix | `render_sidebar.go` and `render_sidebar_files.go` from `b0bb1e20` | all four widths (" terminalszebr…│" at 16, " terminalszebra-… +│" at 20, one blank cell at 24) | **caught** |
| One cell of gap | `sidebarHeaderGap = 1` | widths 18, 20 and 24 (" terminals zebr… + │" at 20) | **caught** |
| The name sized from half the rail | the terminals `room` set back to `max(cw/2, 1)` | widths 20 and 24 (the backstop cut leaves " terminals  …a-… + │", which does not name the session) | **caught** |

Width 16 passes the two narrow controls because no name fits there. The first
control covers it.

## Frame clock

`TestFramePacingKeepsTheGuestsRate` runs the `framepace` guest at 120 Hz with
max_fps 120 and at 240 Hz with max_fps 240. It asserts the frames shown a
second, the p95 interval and the p50 latency from guest to host.
`TestFloodStaysSmooth` runs the guest as a flood and asserts the host's frame
rate and p95 gap. Both are wall-clock measurements, so they run only with
`TUIOS_E2E_PERF` set. `TestKickFlushWritesTheFrame` in `internal/app` pins
the Bubble Tea behaviour the flush kick depends on.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`582af540`) | `TestFramePacingKeepsTheGuestsRate` (103 frames a second and a p95 of 17 ms at 120; 126 at 240), `TestFloodStaysSmooth` (13 and 24 frames a second, p95 100 ms) | **caught** |
| No flush kick | `View`: the `kickFlush` call cut | `TestFramePacingKeepsTheGuestsRate` (240: 194 frames a second, p95 8.7 ms) | **caught** |
| Intervals counted from the emit | `NextFrameTime` returns now | `TestFramePacingKeepsTheGuestsRate` (120: p95 14.6 ms) | **caught** |
| The floor stays at 8 ms | `applyFrameRate`: the `SetFrameInterval` call cut | `TestFramePacingKeepsTheGuestsRate` (240: 129 frames a second) | **caught** |
| A pane behind is drawn every 250 ms | `catchUpCoalesceInterval` returns 250 ms | `TestFloodStaysSmooth` (15 frames a second, p95 101 ms) | **caught** |
| The kick does nothing | `sendTick`: the send cut | `TestKickFlushWritesTheFrame` | **caught** |
| The kick sent on a timer from View | build `cc61f3b8`, before `flushCmd` | `TestTypingLatencyStaysLow` (p99 17.4 ms with 1 pane, 24.1 ms with 8) | **caught** |

`TestTypingLatencyStaysLow` also fails on origin/main with 8 panes (p99 22.8
ms), which had the same tail for a different reason.

Not covered: the latency assertion was added after the first controls ran.
On the released build the 120 Hz guest measured a p50 latency of 14.8 ms and
with the kick cut 14.9 ms, both over the 12 ms bound. The frame gate across
panes (`takePaneOutput`) has no control of its own: with nine guests the
shown rate is noisy on a shared machine, and the gate's effect is CPU, 600
against 908 ms/s, which the suite does not assert.

## Kitty streams without a compose

`TestKittyStreamCostsLittle` runs the `framepace` guest as a 240 Hz shared
memory stream and asserts the client's CPU, and as a 120 Hz stream of 64x64
frame edits and asserts the p95 latency from guest to host. Under
`TUIOS_E2E_PERF`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`582af540`) | `TestKittyStreamCostsLittle` (206 ms/s of CPU; an edit takes 3.8 ms at p95) | **caught** |
| The frame clock without this change | build `cc61f3b8` | `TestKittyStreamCostsLittle` (338 ms/s; 3.1 ms) | **caught** |
| Every write marks the pane | `outputWriter`: the `graphicsOnly` branch cut | `TestKittyStreamCostsLittle/shm-240` (344 ms/s) | **caught** |
| Edits wait for a compose | `forwardAnimation`: the `editShowsAtOnce` flush cut | `TestKittyStreamCostsLittle/patch-120` (2.5 ms) | **caught** |

The edit control measured 2.5 ms against a 2 ms bound when it ran; the bound
is now 1.5 ms, against 0.9 to 0.95 ms with the change.

`TestGraphicsOnlyRefusesAChunkOfAnEarlierCommand` (`internal/terminal`) fails
on the classifier before the review fix: it accepted the last chunk,
`\x1b_Gm=0;...`, of an a=T without C=1 whose first chunk came in an earlier
write. `FuzzGraphicsOnlyAfterAWrite` keeps the two inputs it found, a write
after half a UTF-8 character and one after an open sixel DCS.

Not covered: a guest whose cursor is visible, which takes the usual path, and
a placement without `C=1`, which `graphicsOnly` refuses.

## Live CPU and RAM settings leave the samplers off

Enabling `appearance.show_cpu` or `appearance.show_ram` changed the rendered
meter but did not add its sampler to the dock engine. The settings page's save
is a self-write, so the config watcher correctly ignores it and cannot repair
the missing schedule. `DockMetersSyncCmd`, called after the message handler,
now reloads the dock only when a placed meter's enabled state differs from its
sampler membership. It uses the existing engine cancellation and listener path.

`TestDockMetersFollowLiveSettings` drives both the settings page and
`set-config` on a real attached client. It checks sampled text, the two-second
interval and advancing `last_run`, then disables, re-enables and disables both
meters. Frames and JSON listings are written through `TUIOS_E2E_FRAMES`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| Missing post-handler hook | build on `f3d89294` with a Go overlay cutting the `DockMetersSyncCmd` call from `Update` and its unused batch variable; helper left intact | `TestDockMetersFollowLiveSettings/settings`, `/set-config`, and `TestDockMeterSyncDoesNotReloadAnUnchangedPlan/placed=true`: enabled CPU has `refresh=render` and empty interval, text and last_run | **caught** (3 subtests) |

The negative run used `-count=1`; both settings paths and the placed meter's
positive half timed out at the sampler assertion, not at the settings control.
The omitted meter's positive half passes the control because adding it through
the config watcher already rebuilds the engine. All four subtests pass with
the post-handler hook wired in.

`TestDockMeterSyncDoesNotReloadAnUnchangedPlan` checks enabled meters omitted
from the dock, disabled placed meters, and repeated settings. A custom `once`
component records its starts; unchanged sampler membership must not restart it.
The same fixture then enables or places CPU: its sampler must start and the
custom component must restart exactly once. Both layout cases pass on the fixed
build. This patch does not change the clock's sampler lifecycle.

## Home and End in copy mode (#515)

`copy_mode_home_end_test.go` puts the copy cursor on the middle of a line.
`TestCopyModeHomeAndEnd` requires `Home` and `End` to reach the columns `0`
and `$` reach, and reads the clipboard after `v Home y` and `v End y`.
`TestCopyModeEndRebinds` moves `copy_mode_line_end` to `ctrl+e`. `End` must
then leave the cursor on the match, and `ctrl+e` must move it to the column
`$` reaches. The unit test `TestEveryDefaultBindingReachesItsAction` presses
both default keys in both modes.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`a9a54261`) | `TestCopyModeHomeAndEnd` ("Home: the copy cursor did not move off column 42"), `TestCopyModeEndRebinds` ("ctrl+e: the copy cursor did not move"). The unbound `End` check passes, which is correct: `End` did nothing before | **caught** |
| Copy mode never looks the key up | `HandleCopyModeKey` passes `""` for the action | both tests, as the released build | **caught** |
| A selection's end does not move | `runCopyModeAction`: the `updateVisualEnd` call cut | `TestCopyModeHomeAndEnd` (`v Home y copied "m", want "HOME-start m"`) | **caught** |
| No default keys | `getDefaultCopyModeKeybinds` binds nothing | `TestCopyModeHomeAndEnd`. `TestCopyModeEndRebinds` passes, which is its positive half: the config still binds `ctrl+e` | **caught** |
| A config without the section gets no defaults | the `fillMapDefaults` call for `CopyMode` cut | `TestCopyModeHomeAndEnd`, whose config has no `copy_mode` section | **caught** |

Not covered end to end: multi copy mode looks the action up at its own call
site in `handleMultiCopyKey`, and no test presses `Home` there.

### Help order and the doctor for copy_mode keys

`TestHelpListsHomeAndEndWithTheLineKeys` opens the help overlay, unfiltered,
on its copy mode section and wants the `home` and `end` rows right after the
`0, ^, $` row. `TestKeybindsDoctorNamesACopyModeKeyUsedTwice` binds
`copy_mode_line_start` to `v` and `copy_mode_line_end` to a copy pipe's `p`,
and wants the doctor to name both and not the default `home` and `end`. Its
positive half runs the defaults and wants no copy mode problem.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`26469546`) | both tests (the doctor "reports 0 copy mode keys, want 2", and the rows after `0, ^, $` are `gg, G` and `ctrl+u, ctrl+d`) | **caught** |
| The report never carries the problems | `Report`: `CopyModeProblems` set to nil | `TestKeybindsDoctorNamesACopyModeKeyUsedTwice/clashes` | **caught** |
| The bound rows at the end of the section | `generateCopyModeBindings` appends them, as before | `TestHelpListsHomeAndEndWithTheLineKeys` | **caught** |

## A copy trims the whitespace it covers (#516)

`copy_whitespace_test.go` prints indented lines and reads each copy off the
wire as OSC 52, untrimmed. `TestCopyKeepsPrintedWhitespace` copies a line
with `V y` and with `0 v $ y`, three lines with `V j j y`, a line with a triple
click and three lines with a drag. The lines hold leading spaces, interior
runs and a tab. `TestCopyKeepsSpacesAtASoftWrap` copies one long indented line
that the pane wraps over more than three rows, with a space on the last column
of at least one row. Both run alone and through a daemon.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`a9a54261`) | both tests, alone and through the daemon (`V y copied "LEAD-one  two   x", want "    LEAD-one  two   x"`, and the wrapped line lost its indent and joined `abab` at each wrap) | **caught** |
| The copy trims its ends | `extractVisualText` returns `strings.TrimSpace` again | both tests, alone and through the daemon | **caught** |
| A line selection starts at the first printed cell | `enterVisualLine` takes `startX` from `getLineContentBounds` | `TestCopyKeepsPrintedWhitespace`, both modes. The wrapped test passes, which is correct: `j` moves the start to the first column | **caught** |
| The wrap flag ignored | `selectionRowWraps` returns false | `TestCopyKeepsSpacesAtASoftWrap`, both modes (a newline at every wrap). `TestCopyKeepsPrintedWhitespace` passes, which is its positive half: none of its lines wrap | **caught** |
| Screen rows read at the window's width | `selectionRowCells` sized by `window.Width`, which counts the border | `TestCopyKeepsSpacesAtASoftWrap`, both modes (two extra spaces at every wrap) | **caught** |
| The pad a wide character leaves at a wrap copied as a space | `extractVisualText` keeps the last cell of a padded row | `TestCopyKeepsSpacesAtASoftWrap`, both modes ("the line wrapped before a wide character copied as" a space before the wide character). The pure Go build and the ghostty build both fail | **caught** |
| The ghostty backend reports no pad | `ghosttySpacerHead` returns false, as `RowPadded` did before | `TestCopyKeepsSpacesAtASoftWrap` on a `-tags ghostty` build, both modes. The same test passes on the ghostty build with the fix | **caught** |

The wide case prints a line that fills the pane to one column short of its
width, then a wide character, so the character wraps early on any pane width.

## Borderless zoom

`TestZoomBorderless` zooms one of two panes with `appearance.zoom_borderless`
on. It asserts the shell's size is the whole pane region, that no border cell
is drawn there, that a drag from the region's first cell copies the text there,
and that the pane's edge is back after zooming out. On the daemon run it also
turns the option off and on with `set-config` while the pane is zoomed, and
the border follows. It runs standalone, on a
daemon, under shared borders at zoom_size 90, with zoom_max_width 60, in a
light theme, on a floating pane and beside the rail. The boxed run is the
positive half: with the option off, the same probe finds the border and a
shell two cells smaller each way.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The option is never read | `ApplyAppearanceConfig`: the `s.ZoomBorderless` line cut | all 7 borderless runs ("the zoomed shell is 98x26, want the whole region 100x28"; shared: 91x28, the camera; max-width: 58x26) | **caught** (7 of 7 run) |
| The zoom box keeps the border | `applyZoomRectAnimated`: the line that drops the border under the option cut | 6 of 7 borderless runs (98x26). `borderless/shared` passes: shared borders already drop the border, and the zoom is still the whole region | **caught** |
| Zooming out keeps the pane borderless | `ToggleZoom`: the `settleUnzoomedBorder` call cut | `borderless/floating` ("no border after zooming out"). The tiled runs pass: the retile gives a tiled pane its border back on its own | **caught** |

## Mode pill icons from the config

`TestDockModeIconsFollowTheConfig` sets `appearance.dock_mode_icon_window`,
`_terminal` and `_tiling`, and reads the dock row of the real binary. It
checks the configured icon in place of the default, a four-cell icon that
grows the pill by its width and moves the workspace readout by the same
amount, an empty icon that draws no pill, a nine-cell icon that falls back to
the default, and the same wide icon in the compact dock. The unset row is the
positive half of the hidden and too-wide cases.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The config never reaches the dock | cut the three assignments in `ApplyAppearanceConfig` | `/custom`, `/terminal`, `/hidden`, `/compact`. `/too-wide` passes, which is correct: it expects the default | **caught** |
| An empty label still draws a pill | `buildDockLeftText`: the empty label padded to two spaces | `/hidden` (6 caps on the row, want 4) | **caught** |
| No width limit | `DockModeIconUsable`: the width check cut | `/too-wide` (the nine-cell icon is drawn) | **caught** |
| The word `default` in config.toml drawn as text | `dockModeIcon`: the `DockModeIconDefault` check cut | `/default-word` (the dock row shows `default`) | **caught** |

## OSC 7501, the Program Status Protocol

`program_status_test.go` runs a script in a shell pane that reports through
OSC 7501 and `tuios status`. `TestProgramStatusDrivesTheRailAndInbox` follows
each report to `get-agent-state`, the rail, the Inbox and the dock, and covers
the `?` reply, child records, a clear, a hostile message, the script's exit,
OSC 133 A while the program still runs, and typing.
`TestProgramStatusQueryStandalone` asks the query without a daemon.
`TestProgramStatusReachesTheHostTerminal` reads what tuios reports to
`hostterm -program-status`. Its subtests for a host that does not answer and a
config that turns the reports off are the negative halves, in the same
fixture. Each control cuts one call site. All three tests pass on the pure Go
build and on a `-tags ghostty` build.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released behaviour | build origin/main (`86cac4e1`) | all three (`HOSTTERM-7501=?` never printed, the host never asked) | **caught** |
| The emulator does not read OSC 7501 | the `RegisterOscHandler(progstatus.Command, ...)` call cut | all three: no reply, and the pane's own report never reaches the host | **caught** |
| The daemon drops the change | the `applyProgramStatus` call in the event sink cut | the rail test (`working with progress: never held`, no records), the host test | **caught** |
| Records but no agent state | the `ApplyAgentReport` call in `applyProgramStatus` cut | the rail test (records present, state `none`), the host test | **caught** |
| The prompt mark is ignored | the `noteProgramStatusMark` call in the `SemanticMark` callback cut | the rail test, step 7 (`the prompt mark: never held` within 3 seconds, while the program still runs) | **caught** |
| A program's exit is ignored | the `endProgramStatusAtShell` call in the agent detector cut | the rail test, step 6 (the root `working` record outlives the script) | **caught** |
| Typing is ignored | the `noteProgramStatusInput` call in `handleInput` cut | the rail test, step 8 (the done records stay) | **caught** |
| Invisible characters kept | `programStatusWire` copies title and msg without `programStatusText` | the rail test, step 5 (the record keeps U+202E and U+200B). The rail still drew the text inert, through `printableTitle` | **caught** |
| The host is never asked | the `hostProgramStatusProbe` call in `Init` cut | the host test, `supported` and `host does not answer` (the host was not asked) | **caught** |
| The config cannot turn it off | `hostProgramStatusOn` reads `""` in place of the setting | the host test, `off in the config` (asked and reported to). `supported` passes, its positive half | **caught** |
| No app on the rail | the summary record's app is not used by the `harness` token | the rail test, step 1 (`cargo` never shown) | **caught** |
| No progress on the rail | `progress` left out of the shipped token list | the rail test, step 1 (`40%` never shown) | **caught** |

### Review fixes

The review of #546 found that a working record went while its program still
ran, and four smaller faults. `program_status_rules_test.go` adds
`TestProgramStatusOutlivesTheShellCheck` (a pane whose own process is the
script, and a background job: both keep their working record past two
detector readings), `TestProgramStatusYieldsToAHook` (a hook's state keeps the
pane against a report and survives the records ending), and
`TestProgramStatusAuthIsAnsweredInThePane`. The host test now also detaches
and wants a clear for the record it left. Step 6 of the rail test is the
positive half of the first: a script that reported from the foreground and
exited loses its working record.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The released branch (`8910b204`) | build the pull request before the fixes | `TestProgramStatusOutlivesTheShellCheck`, both cases (the record gone within 2 seconds), `TestProgramStatusYieldsToAHook` (the program took the pane from the hook), `TestProgramStatusAuthIsAnsweredInThePane` (the reason named no login), and the host test (no clear on detach) | **caught** |
| Every report arms the exit rule | `armProgramStatusExit` stores the flag without reading the foreground group | `TestProgramStatusOutlivesTheShellCheck`, both cases | **caught** |
| `program` ranks with `report` | the rank back to 40 | `TestProgramStatusYieldsToAHook` (the hook's state taken) | **caught** |
| A detach leaves the records | the `HostProgramStatusClear` write in the attach path cut | the host test, `supported` (no second clear) | **caught** |
| An auth block offers an answer | the `PromptKindAuth` check in `lookAtPrompt` cut | `TestProgramStatusAuthIsAnsweredInThePane` | **caught** |
| The ghostty scanner keeps C0 controls | the C0 skip in the scanner's OSC state cut | `TestGhosttyDiffProgramStatus/C0_inside` (unit, `-tags ghostty`) | **caught** |

The rest of the review is held by unit tests: a malformed id, strict base64
and ASCII-only trimming by `TestParse` and the oracle in `FuzzParse`, and the
control bytes inside the string by `TestConform_ProgramStatusReports` on both
backends.

### Second review fixes

The second review found that one foreground exit ended a background job's
record too, that the alert limit dropped a real change of state, and that a
released pane got back a weaker source's state that might be stale.
`TestProgramStatusOutlivesTheShellCheck` gains a background job that reports
first and a foreground program beside it, whose exit must end only its own
record, and a program that lives a fifth of a second. The new
`TestProgramStatusAlertAfterTheGap` blocks a pane on a login and makes it done
three seconds later: the done notification must not reach the stand-in host
terminal inside the 30 seconds and must reach it after.
`TestProgramStatusYieldsToAHook` now wants the pane left with no state when
the program lets go of a pane it took from the screen tier.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| One flag for the pane | any ended group drops every working, blocked and idle record, as the single flag did | `TestProgramStatusOutlivesTheShellCheck/a_foreground_program_beside_a_background_job` (the job's record gone) | **caught** |
| A change inside the gap dropped | `programAlertOutside` holds nothing back | `TestProgramStatusAlertAfterTheGap` (the done alert never came) | **caught** |
| No limit | `fireAgentAlert` skips `programAlertOutside` | `TestProgramStatusAlertAfterTheGap` (the done alert inside the gap) | **caught** |

Not covered end to end: a program that reports and exits in the same instant
(the race the docs describe: on an idle machine 0 of 20 such programs kept
their record with the read in the PTY reader, and 0 of 20 on the build that
read it at parse time; the review saw 2 of 10 under load), and the exit
rule for a pane on another machine, which needs a second host.

Not covered end to end: the exit of the pane's own process (`noteExit`),
because the pane closes with it; OSC 9;4 being ignored after OSC 7501, since
the `program` claim outranks OSC 9;4 while records exist, so no screen differs;
and a full reset, which the vt conformance test covers on both backends.

## A config split over several files

`config_include_test.go` and `config_layers_test.go` drive #518 through the
real binary and real files. A store file is a 0444 file in a 0555 directory,
linked into the config directory, the way Nix and home-manager put it there.

- `TestIncludedConfigFilesLoadAndReload`: a binding in an included file works at
  start. Saves to the included file, to a new `config.d` file and to an include
  that was missing at start each reach the running client. The positive half:
  the second chord does not fire before its file is saved.
- `TestSetConfigWritesTheFileThatHoldsTheKey`: `set-config` writes the included
  file that holds the key, and config.toml keeps its include list. A key that a
  0444 file holds goes to config.toml, the file stays byte for byte the same,
  and the client shows "cannot write".
- `TestConfigCommandsWriteTheFileThatHoldsTheKey`: `config files`,
  `hosts add`, `hosts remove`, `keybinds unbind` and `config origin` against
  includes, a cycle, a missing file and a 0444 file.
- `TestFirstStartWithNixStyleConfigD`: no config.toml, and a store link in
  `config.d`. The first start writes a config.toml with no setting but
  `[startup]`, the store file's binding works, and `set-config` of a key the
  store holds adds exactly one line to config.toml.
- `TestReadOnlyConfigTomlLink`: config.toml is a store link that includes a
  writable `local.toml`. A new key goes to `local.toml` with its comment kept. A
  key the store config.toml holds fails with "cannot save". With `local.toml`
  read-only too, the save fails with "cannot write" and changes nothing.
- `TestSymlinkedIncludeEditedInPlace`: an include is a link to a file in
  another directory, and an edit in place to the target reaches the client.
- `TestMixedNamedAndUnnamedCommandEntries`: an unnamed `[[keybindings.command]]`
  entry changes the named entry with its key, a named entry the later file does
  not mention stays, a `disabled = true` entry removes its match, and a new
  entry is added.
- `TestMergeOrderIncludeThenConfigDThenMain`: one chord in three files.
  config.toml wins, then `config.d`, then the include.
- `TestConfigPruneLetsIncludesApply`: `config prune` removes the keys at their
  default, keeps comments, the include list, a chosen value and `[startup]`, and
  the included file's value then applies.
- `TestSaveDoesNotPinValuesTheModelHasNotSeen`: an included file changes where
  the client cannot see it. A save of another key writes neither the client's
  old value into config.toml nor back into the file.
- `TestIncludeMistakesAreNamed`: a missing `?` include is silent, a missing
  plain include warns, an include below a table warns, a self include says so,
  a link loop in a `?` include or in `config.d` is skipped with a warning, and
  a directory include fails with a message that names `config.d`.
- `TestRemovingAHostKeepsTheNextTablesComment`: a host cleared on the settings
  page leaves config.toml without its table. The comment above the next table
  stays, one blank line below the table before it.
- `TestIncludedFileIsNeverRewrittenWhole`: an included file sets the key in an
  inline table, which the line editor cannot change. The file stays byte for
  byte the same, the change goes to config.toml, and the client says so.
- `TestConfigCommandsWriteTheFileThatHoldsTheKey` also checks that a write to a
  file with CR LF line endings keeps them.

Artifacts: frames, the files after each save, and the command transcripts,
under `$TUIOS_E2E_FRAMES/<test name>/`.

Run on 2026-10-07, each control on its own binary, all thirteen tests each time:

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The watcher follows config.toml alone | `Watcher.relevant`: `name == cw.path` only | `LoadAndReload`, `SymlinkedInclude` | **caught** |
| config.d is not watched | `Watcher.relevant`: the two `cw.dropIn` checks cut | `LoadAndReload` | **caught** |
| A link target is not watched | `Watcher.follow`: the `l.Real` line cut | `SymlinkedInclude` | **caught** |
| Includes are never read | `loadLayered`: `Layered` forced false, and `Bytes` returns config.toml | nine of eleven | **caught** |
| Merge order reversed | `loadLayered`: config.d read before the includes | `MixedNamed`, `MergeOrder` | **caught** |
| An array of tables replaced whole | `mergeTables`: the `mergeEntries` call cut | `LoadAndReload`, `MixedNamed` | **caught** |
| Tombstones ignored | `mergeEntries`: the tombstone case cut, and the `stripTombstones` call cut | `MixedNamed` ("the tombstone did not remove beta") | **caught** |
| A save renders the whole config | `saveConfigData` writes the header and the whole model | `SetConfig`, `ConfigCommands`, `FirstStart`, `ReadOnlyConfigTomlLink`, `SaveDoesNotPin`, `RemovingAHost`, `NeverRewrittenWhole` | **caught** |
| A two-way save | `RenderUserConfig`: the baseline cut | `SaveDoesNotPin` ("wrote the client's old window_button_style back into look.toml") | **caught** |
| A full first-start file | `createDefaultConfig` writes `DefaultConfig()` | `FirstStart` | **caught** |
| Every file is writable | `ConfigLayer.Writable` returns true | `SetConfig`, `ConfigCommands`, `FirstStart`, `ReadOnlyConfigTomlLink` | **caught** |
| A read-only holder is written | `layerWriter.target`: the owner's `writable` check cut | `SetConfig`, `ConfigCommands`, `FirstStart`, `ReadOnlyConfigTomlLink` | **caught** |
| The fallback is always config.toml | `layerWriter.target`: `lastWritable` cut | `ReadOnlyConfigTomlLink` | **caught** |
| No line edit | `editMain` and `flush`: the line edit result never used | `SetConfig`, `ConfigCommands`, `FirstStart`, `ReadOnlyConfigTomlLink` ("lost its comment"), `SaveDoesNotPin`, `RemovingAHost`, `NeverRewrittenWhole` | **caught** |
| A removal takes the next table's comment | `tomlEdit.remove`: the cut runs to the next header | `RemovingAHost` | **caught** |
| An included file is rewritten whole | `flush`: a failed line edit on an include writes the file from its values | `NeverRewrittenWhole` ("tuios wrote look.toml again") | **caught** |
| CR LF line endings dropped | `newTOMLEdit`: `crlf` forced false | `ConfigCommands` ("did not keep the CR LF line endings") | **caught** |
| A link loop stops the load | `visit`: the skip for an optional include and config.d cut | `IncludeMistakes` | **caught** |
| hosts add is not routed | `SetHostInFile` writes the main path | `ConfigCommands` | **caught** |

Cutting only one of the two tombstone paths is not caught, and that is by
design: the matched entry merges the `disabled` key and the strip removes it.

Not covered here: the tombstones a save writes when it removes an entry that a
read-only file holds, and the tombstone it writes before an entry that must
not merge with an earlier file's entry. No screen and no command in tuios
writes an array of tables today, so no end-to-end test can reach these paths.
They wait for the first one that does.

```sh
go build -o /tmp/tuios ./cmd/tuios
cd e2e/tui && TUIOS_E2E=1 TUIOS_E2E_BIN=/tmp/tuios go test -count=1 \
  -run 'TestIncludedConfigFiles|TestSetConfigWritesTheFile|TestConfigCommandsWrite|TestFirstStartWithNix|TestReadOnlyConfigTomlLink|TestSymlinkedInclude|TestMixedNamed|TestMergeOrder|TestConfigPrune|TestSaveDoesNotPin|TestIncludeMistakes|TestRemovingAHost|TestIncludedFileIsNeverRewrittenWhole' .
```

## Single-client attach and detach-client (#549)

`detach_client_test.go` runs real clients against one daemon.
`TestAttachDetachOthersEndsTheOtherClients` attaches two clients the plain
way, which is the positive half: both stay attached. A third client with
`attach -d` then makes the first two exit 0 with "Another client attached to
this session." `TestSingleClientOptionDetachesOnEveryAttach` does the same
with `[daemon] single_client = true` and a plain attach.
`TestDetachClientByIDAndSession` detaches one of two clients by its id, while
the other stays. A pane with read, write and fan is refused the command, and
the tmux shim's `detach-client -s` takes the last client off. Each session
still runs at the end.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| `attach -d` is not read | `handleAttach`: `payload.DetachOthers` cut from the gate | `TestAttachDetachOthersEndsTheOtherClients` ("tuios never exited") | **caught** |
| `single_client` is not read | `handleAttach`: `d.singleClient.Load()` cut from the gate | `TestSingleClientOptionDetachesOnEveryAttach` ("tuios never exited") | **caught** |
| A pane needs no grant | `verbScopes`: `detach-client` classed `scopeOpen` | `TestDetachClientByIDAndSession` (the client exits, and `DC_EXIT=1` never shows) | **caught** |
| The shim does not answer `detach-client` | `commands` in `internal/tmuxcompat/shim.go`: the entry cut | `TestDetachClientByIDAndSession` (`tuios tmux detach-client -s` exits 1) | **caught** |

### Review fixes: racing attaches, viewers and `-a`

`TestConcurrentAttachDetachLeavesOneClient` attaches one client and then
starts two `attach -d` clients at the same moment, for six rounds. Each round
must end with one client attached and two that exit 0 with the message.
`TestSingleClientIgnoresAViewOnlyClient` attaches a `TUIOS_VIEW_ONLY=1`
client under `single_client`, which must leave the owner attached. A plain
attach after it detaches both, which is the positive half.
`TestDetachClientAllOther` keeps one client with `--client ID --all-other`,
and keeps the newest with the shim's `detach-client -a -s`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The first version of the pull request | built from its head, `4847ca2` | `TestConcurrentAttachDetachLeavesOneClient` (round 1: one client neither exits nor stays attached), `TestSingleClientIgnoresAViewOnlyClient` (the viewer detaches the owner) | **caught** |
| No session lock and no reply check | `handleAttach`: the `attachMu` lines cut, and `repliedSession` cut from `detachOthers` and `ejectDetached` | `TestConcurrentAttachDetachLeavesOneClient` (round 6: a client exits 1) | **caught** |
| No session lock only | the `attachMu` lines cut | none | **not caught**: the reply check alone keeps a notice from reaching a client before its reply |
| No reply check only | `repliedSession` cut | none | **not caught**: the lock alone orders the attaches |
| A notice before the handler is lost | `OnSessionEnded`: the pending notice never delivered | none | **not caught**: the window between the read loop and the wiring is too short to hit here |
| A viewer counts as exclusive | `exclusiveAttach`: the `ViewOnly` test cut | `TestSingleClientIgnoresAViewOnlyClient` | **caught** |
| `all_other` is not read | `verbDetachClient`: `p.AllOther = false` | `TestDetachClientAllOther` (the first client never exits) | **caught** |

### Second review: the attach lock, the reply check and the shim's -s

These deterministic regressions live in `internal/session/detach_client_test.go`.
Each lands one event inside an attach through the `attachSnapshotTaken` hook.

- `TestAPanicInsideAnAttachLeavesTheSessionUnlocked` panics once inside an
  attach. The next attach to the session must return.
- `TestADetachInsideAnAttachLeavesTheAttachingClientAlone` runs
  `detach-client -s` inside a second client's attach. The fully attached first
  client goes. The attaching client gets its session and stays attached.
- `TestADetachNoticeBeforeTheHandlerIsDelivered` detaches a client after its
  read loop starts and before its handler is set. The handler must still get
  the notice.
- `TestDetachClientAllOther` now checks the shim against tmux 3.7c, which was
  checked by hand on a private tmux server. `-a -t tuios-S` keeps the newest
  client. `-a -s S` detaches every client of S.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The lock is released only after the reply | `handleAttach`: `attachLocked` starts false, so only the plain unlock runs | `TestAPanicInsideAnAttachLeavesTheSessionUnlocked` (the second attach never returns) | **caught** |
| No reply check | `repliedSession` cut from both `markOthers` and `detachClientFrom` | `TestADetachInsideAnAttachLeavesTheAttachingClientAlone` ("unexpected response: 30") | **caught** |
| No reply check in `markOthers` only | that one check cut | none | **not caught**: `detachClientFrom` checks again |
| No reply check in `detachClientFrom` only | that one check cut | none | **not caught**: `markOthers` checks first |
| A notice before the handler is lost | `OnSessionEnded`: the pending notice never delivered | `TestADetachNoticeBeforeTheHandlerIsDelivered` | **caught** |
| The shim's `-s` honours `-a` | `detachClient` in the shim: `all_other` set with `-s` | `TestDetachClientAllOther` (the newest client is kept after `-a -s`) | **caught** |

## Images on a host without graphics (kmscon)

A pane's sixel image on a host with neither sixel nor kitty graphics, such as
the Linux console under kmscon, used to show as a box with "image" in it. It is
now drawn as block glyphs, one glyph with two colours per image cell
(`appearance.image_symbols`, `internal/mosaic`). The pane is told it can draw
sixel, so programs send the picture.

`TestImageSymbolsOnAHostWithoutGraphics` draws a generated picture (sky, sun,
hills, a red band, a checker) in a pane, for each glyph set, in a daemon
session, and at 256 colours. It reads the picture back off the host's screen:
each glyph is turned back into its shape, and its two colours are compared with
the source averaged over the same sub-cells. The glyphs must also beat a flat
cell on the cells with an edge in them. It then scrolls the picture partly out
of the pane and clears it. `TestSixelFallbacks/plain` checks the default (the
picture is drawn) and `plain-off` the positive half (the box, and no sixel in
DA1). `TestImageSymbolsWithChafa` runs chafa, which picks sixel from the DA1.
chafa draws octants of its own when it does not pick sixel, so the test reads
the passthrough's debug log: the octant run must register a sixel image drawn
as glyphs, and the `off` run none.

The daemon variant first ran standalone without anyone noticing: its config
file had no `startup.daemon`, and the controls on the daemon wiring passed. The
test now writes `startup.daemon = true` when it asks for a daemon, and both
controls below fail.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The glyph pass draws nothing | `drawImageSymbols`: returns at once | `octant-standalone` (error 377 against a ceiling of 10, nothing to clear) | **caught** |
| The daemon does not count a glyph client | `refreshTreeOps`: `cs.symbolImages` cut | `octant-daemon` (pane DA1 is `62;22`) | **caught** |
| The hello does not carry it | `Connect`: `hello.SymbolImages` cut | `octant-daemon` (pane DA1 is `62;22`) | **caught** |
| The sextant table is off by one | `buildSextants`: first code point U+1FB01 | `sextant-standalone` (error 15.3, edge cells 49.9 against a flat 32.8) | **caught** |
| Default off | `imageSymbolKind`: auto gives `Off` | `TestSixelFallbacks/plain` (DA1 has no 4, the box is shown) | **caught** |
| The pane is not told sixel | `Advertised`: false in symbols mode | `TestImageSymbolsWithChafa/octant` (no image registered, 20 glyph cells) | **caught** |

The octant and sextant tables were also checked once against the Unicode 16
character names (Python's `unicodedata`): every one of the 256 and 64 shapes
names the right cells. The e2e decode reads plane 1 glyphs through
`mosaic.Shape`, the table's inverse, so a table error that is its own inverse
would pass it; the name check is what covers that.

### The glyph set by terminal, and the 16-colour floor

`auto` picks the glyph set from `TERM`: octants on kmscon, half blocks on the
Linux console, quadrants elsewhere. At 16 colours the cells are half blocks
dithered to the ANSI colours, with no bright background, and a picture under
`mosaic.MinFidelity` shows the box. A host that answers for sixel or kitty
never gets glyphs.

`TestImageSymbolsAutoPicksByTerminal` runs one row per choice with the setting
at `auto`. `kmscon` must show octants and no sextants, at the same error
ceiling. `other` (`TERM=xterm-256color`) must show no sextant or octant.
`linux` must hold only half blocks, every colour an ANSI index, no background
above 7, and a lightness correlation of at least 0.8 with the picture (0.92
measured). `linux-truecolor` must hold half blocks in 24-bit colour.
`kmscon-with-sixel` must be sent sixel and draw no glyph. `TestSixelFallbacks`
also checks that the sixel and kitty hosts' image cells are not drawn as text.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| kmscon gets quadrants | `imageSymbolKind`: `Quadrant` for kmscon | `kmscon` (0 octant cells) | **caught** |
| The Linux console gets quadrants | `imageSymbolKind`: `Quadrant` for linux | `linux-truecolor` (quadrant glyphs) | **caught** |
| No 16-colour path | `symbolColors`: `TrueColor` at `Depth16` | `linux` (correlation 0.26, bright backgrounds) | **caught** |
| Bright backgrounds allowed | `halfCell16`: the dark-background case taken always | `linux` (bright background 12) | **caught** |
| The threshold shows the box | `MinFidelity` 1.01 | `linux` (the box is shown) | **caught**; this is also the positive half of the box path |
| Default octants elsewhere | `imageSymbolKind`: `Octant` by default | `other` (102 octant cells) | **caught** |
| Glyphs before sixel | `chooseSixelMode`: the symbols case first | `kmscon-with-sixel` (no sixel sent, 336 glyph cells) | **caught** |

Not caught end to end: the dither itself. Without it the test picture, which
is at full contrast, still correlates above 0.8; the loss shows only on
low-contrast pictures.

### Shading, the drawing budget and colour

The glyphs used to be put on the frame after every shading pass, so a modal's
scrim and an unfocused pane's dim left the picture at full brightness.
`TestImageSymbolsAreShaded/modal` opens the command palette over a picture
(`modal_dim = 30`) and requires at least 90% of the picture's light cells
outside the palette to be darker; the pane border must be darker too, which is
the positive half. `unfocused` opens a second pane with `dim_unfocused = 40`
under a theme and requires the same of the first pane's picture.

`TestImageSymbolsFlood` sends 150 pictures of 100x30 cells back to back. The
debug log must show a picture that waited for the pane's drawing budget and
the budget coming back, and the last picture must be drawn after the flood.
The test first waits until tuios has sent the host nothing for a second: its
startup hint draws a frame every 300 ms for about six seconds, and one of
those frames drew the waiting picture, which hid a cut wake. Each draw is made
to cost 300 ms (`TUIOS_E2E_SYMBOL_DRAW_COST`), so the debt at the end of the
flood takes over a second to pay back on any machine.

`TestImageSymbolsKeepAPictureOnScreen` draws picture A (20x10 cells), then
redraws a 25x12 picture beside it 160 times, which is past the pane's 16 MiB
image budget. A is the oldest image and on screen throughout, so it must still
be drawn. The glyph pass used to swap every marker before the scan, so the
frame's visible set stayed empty and the eviction took A. The debug log must
record all 161 images, so the budget was really filled.

`TestImageSymbolsWithoutColour` runs with `NO_COLOR=1`: the pane's DA1 must
not list sixel and a picture shows the box. The positive half is
`TestImageSymbolsOnAHostWithoutGraphics`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| Glyphs drawn after the scrim | `composeLayersIn`: `drawImageSymbols` before `applyScrim` cut | `TestImageSymbolsAreShaded/modal` (0 of 420 light cells darker) | **caught** |
| Glyphs not given the pane's dim | `drawImageSymbols`: the `dimCell` step cut | `TestImageSymbolsAreShaded/unfocused` (0 of 480 light cells darker) | **caught** |
| No colour still draws glyphs | `symbolColors`: ASCII and NoTTY reported as coloured | `TestImageSymbolsWithoutColour` (DA1 `62;4;22`, 234 glyph cells) | **caught** |
| No frame when the budget is back | `wakeWhenBudgetLocked`: the wake cut | `TestImageSymbolsFlood` (0 glyph cells after 10 s) | **caught**; it was not before the test waited out the startup frames |
| Glyph images not counted as on screen | `SetFrame`: the images drawn as glyphs left out of `visible` | `TestImageSymbolsKeepAPictureOnScreen` (A has 0 glyph cells and no box) | **caught** |

A run with the reader drawing only one cell of each picture, so the frame pass
draws the rest, passes `TestImageSymbolsOnAHostWithoutGraphics`: the part drawn
later meets the part drawn first.

Not covered end to end: a local terminal that answers the startup DA1 after the
probe gave up. The test terminal answers DA1 itself, at once, so the late
answer cannot be staged. Nor is `symbol_images` in an SSH client's hello: no
e2e test runs an SSH client on a host without graphics.

Not covered: a real kmscon. It needs a free VT and DRM master, which this
machine's test run must not take. A kitty graphics image on such a host is not
drawn: the pane is still told kitty graphics are absent, and programs fall back
to their own output.

## The ssh agent link (#549)

`TestSSHAgentLinkFollowsTheNewestClient` runs with `[daemon] ssh_agent =
"follow"` and fake agent sockets in folders the test makes. Two clients
attach with different sockets, and the link on disk follows the attach order.
A client whose `SSH_AUTH_SOCK` is a symlink moves the link to the socket the
symlink names. Clients with a socket in a folder anyone can write to, a
symlink to such a socket, a socket in a private folder under a folder anyone
can write to, and a path that is not there leave the link where it was. The
daemon has no socket of its own here. A new pane prints
the link's name from `SSH_AUTH_SOCK` and finds a socket there. A client that
runs inside a pane attaches a second session and makes no link, and the same
socket from a client outside every pane does: that is the positive half. A
detach moves the link back to the first client, and when the last client
drops, the link is gone.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| An attach does not move the link | `handleAttach`: the `agentNoteUse` call cut | the first attach ("no such file or directory") | **caught** |
| Sockets are not checked | `ssh_agent_follow.go`: both `ownedSocket` calls made to pass | the client with a socket in a folder anyone can write to moves the link | **caught** |
| A client in a pane counts | `agentNoteUse`: the `mayActAsHuman` test cut | the client in the pane links `e2e-agent-other` | **caught** |
| A leave does not move the link | `notifyClientLeft`: the `agentForget` call cut | the link of `e2e-agent-other` stays after its last client leaves | **caught** |
| New panes do not get the link | `Manager.CreateSession`: the `cfg.AgentEnv` stamp cut | the new pane never prints the link's name | **caught** |

The rows above were run on the first version of the pull request. The rows
below were run on the review fixes, with
`TestSSHAgentLinkFallsBackToTheDaemonsSocket` added. That test starts the
daemon with a socket of its own. The link points at it before any client
attaches and after the last client leaves. `kill-server` removes the link. A
stale link in the folder is swept when the daemon starts again, and the link
of a daemon on another socket in the same folder is kept.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| Symlinks are not resolved | `resolveAgentSocket`: `filepath.EvalSymlinks` replaced by the path as given | `TestSSHAgentLinkFollowsTheNewestClient` (the client with a symlink does not move the link) | **caught** |
| No fallback to the daemon's socket | `relinkAgentLocked`: the `f.fallback` branch cut | `TestSSHAgentLinkFallsBackToTheDaemonsSocket` (no link before any client) | **caught** |
| No sweep at start | `startSSHAgent`: the `sweepAgentLinks` call cut | `TestSSHAgentLinkFallsBackToTheDaemonsSocket` (the stale link stays) | **caught** |
| No sweep at stop | shutdown: the `sweepAgentLinks` call cut | `TestSSHAgentLinkFallsBackToTheDaemonsSocket` (the link stays after `kill-server`) | **caught** |
| Only the socket's own folder is checked | `ownedSocket`: the walk stops after the first folder | `TestSSHAgentLinkFollowsTheNewestClient` (the socket under a folder anyone can write to moves the link) | **caught** |

`TestSSHAgentFollowsThroughAHost` runs a hub and a remote daemon on one
machine. Both have `ssh_agent = "follow"`, and the link has
`ssh_options = ["-A"]`. Neither daemon has an agent of its own. The ssh
stand-in forwards the agent the way `ssh -A` does: the far command gets a
relay socket, and each connection to the relay reaches the socket that the
stand-in's own `SSH_AUTH_SOCK` names. A person attaches the far session from
the hub with a real `ssh-agent` that holds one key. `ssh-add -L` in the far
pane must print that key's comment.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The link ssh gets the daemon's own environment | `setupFederation`: `SSHDialer` in place of `SSHDialerEnv(..., d.linkSSHEnv)` | `TestSSHAgentFollowsThroughAHost` (the link's ssh has no `SSH_AUTH_SOCK`) | **caught** |
| The hub does not note the person's socket | `verbOpenHostConnection`: the `agentNote` call cut | `TestSSHAgentFollowsThroughAHost` | **caught** |
| The proxy does not report the forwarded socket | `DialForLink`: `agent` left empty | `TestSSHAgentFollowsThroughAHost` | **caught** |
| A client over a link offers no socket | `agentSockOf`: `""` for a link connection | `TestSSHAgentFollowsThroughAHost` | **caught** |

The rows below were run on the second review's fixes. Each host now has its
own link, and only a host that forwards gets it.

- `TestSSHAgentHostLinkFollowsOnlyThatHostsClients` uses three real
  ssh-agents. A attaches the far session through the link, and B attaches a
  session of the hub. The far pane must still reach A. C then attaches the
  far session, and the far pane reaches C, which is the positive half. A
  types in the far pane, and the hub's link for the host moves back to A.
- `TestSSHAgentLinkLeavesANonForwardingHostAlone` starts the hub with an
  agent of its own and three hosts: `-A`, `ssh -G` saying `forwardagent yes`,
  and neither. The two that forward start with their own host link. The
  third starts with the daemon's `SSH_AUTH_SOCK`.
- `TestSSHAgentFollowWithAnOlderDaemon` needs `TUIOS_E2E_OLD_BIN`, a build of
  main from before the feature. It was run with main at 01d72efb. The client
  must draw the far session with an old far daemon and with an old hub
  daemon.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A session of the hub moves a host's link | `agentNoteUse`: also notes `hostAgentKey("build")` | `TestSSHAgentHostLinkFollowsOnlyThatHostsClients` (after B attached, the far pane reaches B) | **caught** |
| Input through the relay does not count | `usedReader`: never calls `used` | `TestSSHAgentHostLinkFollowsOnlyThatHostsClients` (the link stays on C after A typed) | **caught** |
| Every host gets the link | `hostForwardsAgent`: always yes | `TestSSHAgentLinkLeavesANonForwardingHostAlone` (plain starts with its host link) | **caught** |
| No retry against an old hub daemon | `openHostConnectionAgent`: the retry cut | `TestSSHAgentFollowWithAnOlderDaemon/hub` (the client exits 1) | **caught** |
| No retry against an old far daemon | `dialLinkSocket`: the retry cut | `TestSSHAgentFollowWithAnOlderDaemon/far` (the client exits 1) | **caught** |

The rows below were run on the third round. `TestSSHAgentHostLinkNames` gives
the hub three forwarding hosts: Pair and pair, which differ only in case, and
a name of 70 characters. Each one starts with its own link, and Pair's and
pair's links do not differ only in case. A host with `-A` then `-a` keeps the
daemon's environment, as `ssh -G` says. A reload that drops pair removes
pair's link and keeps Pair's. `kill-server` sweeps every host link, the long
one included.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| `-A` in `ssh_options` decides without `ssh -G` | `hostForwardsAgent`: yes when the options hold `-A` | `TestSSHAgentHostLinkNames` (undone starts with its host link) | **caught** |
| The host name is used as it is | `hostLinkName`: `link-` and the exact name | `TestSSHAgentHostLinkNames` (Pair's link is `agent-link-Pair.sock`). The test spells the name out itself, so this fails at the first host | **caught** |
| A host that leaves the config keeps its link | `applyUserConfig`: the `agentPruneHosts` call cut | `TestSSHAgentHostLinkNames` (pair's link stays) | **caught** |
| The sweep does not see host links | `isOwnAgentLink`: false for `link-` | `TestSSHAgentHostLinkNames` (Pair's link stays after `kill-server`) | **caught** |

These two are not covered by an e2e test. A late note from the relay's timer
after a connection closed is now refused under the same lock the close
forgets the client under. A failed `ssh -G` is no longer cached.

## A glyph image in an unfocused pane

`TestImageSymbolsDimEvenly` draws a white disc with a light-grey speckle as
glyphs, with `theme = "dracula"` and `dim_unfocused = 50`. It runs at
truecolor with octants and quadrants, at 256 colours, and at 16 colours with
half blocks. It then moves the focus to a second pane. Every sub-block inside
the disc must stay within 12 of the focused spread, and the disc must be
darker than when it had the focus. The frames are `focused.png` and
`unfocused.png` under each subtest's artifact directory.

The control is main at 1b570369, where the glyph pass still calls `dimCell`.
It was run on 2026-10-07.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A glyph's foreground dims toward its own background | `drawImageSymbols`: `dimCell` in place of `dimGlyphCell` | `TestImageSymbolsDimEvenly`, all four subtests. The unfocused spread is 191 to 222 against a focused spread of 12 to 47, white (251,251,251) next to grey (129,130,135) at truecolor | **caught** |

## Send-keys with a capital letter

`TestSendKeysCapitalAfterPrefix` sends `PREFIX p` and then `PREFIX P` with
`tuios send-keys`. The first must not open the command palette, and the
second must open it.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The client's key parser lowercases a capital | `parseKeyToMessage`: the capital branch cut, so `P` is sent as `p` | `TestSendKeysCapitalAfterPrefix` (the palette does not open after `PREFIX P`) | **caught** |

## Three flakes under load (2026-10-08)

Each test ran 30 times in a row on one machine. Eight busy loops ran at nice 19
on the same eight cores during each run. The "before" build is main at
`af79c927`, which is the control for each code fix.

| Test | Cause | Before | After |
| --- | --- | --- | --- |
| `TestProgramStatusDrivesTheRailAndInbox` | The script reports a working root record and exits. The PTY reader can read the foreground group after the shell has it back. The read found no group, and the report then disarmed a record that had a group. The record outlived the script. A record now keeps its group when the read finds none. | 9 of 30 fail at step 6 (`the script exited: never held`, root `working` left) | 0 of 30 |
| `TestPiPWatchesAPaneFromTheCorner` | `View` reused a cached frame when the frame was skipped, but it read the cursor fresh. The cached frame had the box where the cursor was before. `View` now composes again when the box covers the cursor it shows. | 3 of 30 fail (`the view covered the cursor in 1 samples`, cursor (79,36)) | 0 of 30 |
| `TestChromeLooksAndFooters/dark-120x40-16` | The fixture started the approval hook and did not wait for it. The hook has a 500 ms deadline, and a hook that misses it exits and holds nothing. `startHookIn` now waits until the daemon holds the prompt, and runs a hook that exits first again. | 0 of 30 | 0 of 30, and no hook needed a second run |

The chrome flake did not show on this machine. A hook that always misses its
deadline (`--timeout 1ns`) gives the CI failure exactly:
`chrome_looks_test.go:211: inbox never drew`.

## Frame memory and the connection cap

`TestDaemonBoundsStalledFrames` opens 32 connections. Each one announces a
16 MiB input frame, sends 15 MiB of it and stops. The daemon must grow by
less than 200 MB. A 1 MiB hello must get a refusal, and the next frame on
the same connection must get its answer. Of 300 idle connections to the link
socket, the daemon must refuse at least the 44 over the link cap, and `tuios
ls` must still answer. Of 1100 idle connections to the main socket, the
daemon must refuse at least the 76 over its cap and log the refusal, and
`tuios ls` must say the daemon has too many connections. `tuios ls` must
answer after the connections close. The measurements are in
`frame-memory.txt` under the test's artifact directory.

The controls were run on 2026-10-08 on the branch fix/bound-frame-memory.
The rows for the read budget and the connection cap were run again on the
design that charges a frame whole from its header. Without the change, the
test passes on that design with the daemon growing by 93 MB.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The build before the change | main at af79c927 | `TestDaemonBoundsStalledFrames`: the daemon grew by 517 MB, and the 1 MiB hello was decoded and answered "invalid hello payload" | **caught** |
| No read budget | `readClientFrame`: the acquire skipped, so no frame is charged | `TestDaemonBoundsStalledFrames`: the daemon grew by 907 MB | **caught** |
| No connection cap | `acceptLoop`: the `admitConnection` call cut | `TestDaemonBoundsStalledFrames`: the daemon refused 0 of 1100 connections, and `tuios ls` listed the sessions where it should say the daemon has too many connections | **caught** |
| The link sockets share the main cap | `acceptLinkOn`: `admitConnection` given `openConns` and `maxConnections` | `TestDaemonBoundsStalledFrames`: the daemon refused 0 of 300 link connections | **caught** |
| No short limit | `daemonFrameLimit`: the default case returns `maxFrameBytes` | `TestDaemonBoundsStalledFrames`: the 1 MiB hello was read and answered "invalid hello payload" | **caught** |

The unit tests in `internal/session/frame_budget_test.go` and
`verb_lines_test.go` have their own controls, each run once. Each fails at
least one of them:

- The budget taken in 1 MiB pieces, each piece kept while the next waits.
  Six concurrent 16 MiB frames and six concurrent 1 MiB verb lines all time
  out.
- A keystroke-sized frame charged to the budget.
- A link given the person's budget.
- An input frame's charge held while the input is written to the pane.
- A verb line's charge held until the verb returns. Two send-texts blocked
  in panes that do not read make the next set-buffer fail as busy.
- No bound of one waiting large input per pane, in `PTY.Write`.
- A second large write refused at once, with no wait for the pane's slot.
  Five of six 1 MiB send-texts into `cat` fail as busy.
- A second large write that waits for the slot with no end. A paste or
  send-text into a pane that does not read is never refused.
- No time limit on the client's hold behind a paste. With no pong, Enter
  is never sent.
- No check for a lost connection while input is held. Input to the held
  pane is queued with no error.
- No retry of a busy paste in the client.
- No hold of later input behind a paste: Enter overtakes the retried paste.
- The person working through a hub given the peers' budget.
- A refused connection closed without being told why.
- The body allocated whole from the header.
- No short limit.
- No cap in `admitConnection`.

## A Mac config.toml on Linux, and keys tuios cannot read (issue #556)

`TestMacConfigWorksOnLinux` loads the 25 `opt+` keys and the leader of the
reporter's config on Linux. `tuios keybinds doctor --json` must list no key
problem and note all 25 keys as read as `alt+`. In a daemon session, Alt+2 and
Alt+1 switch workspaces, Ctrl+S opens the prefix menu, and the log viewer has no
`Config:` line.

`TestUnreadableKeyKeepsTheRestOfTheFile` starts a daemon session with
`leader_key = 'ctrl+s'`, `switch_workspace_2 = ['ctrl+nope']` and
`terminal_exit_mode = ['hyper+esc']`. It waits for "2 config problems". Alt+2
must switch to workspace 2 on the default key, and Ctrl+S must open the prefix
menu.

`TestDoctorMatchesTheAppWithBadKeys` is the review repro of #559: an
unreadable leader and `switch_workspace_2 = ['alt+1']` in config.toml, and
`switch_workspace_1 = ['hyper+1']` in `config.d/keys.toml`. The doctor must say
the leader is `ctrl+b` and name each file. explain must say Alt+1 runs
`switch_workspace_2`. In the app, Alt+1 goes to workspace 2, Ctrl+B opens the
prefix menu, and the log viewer names both files.

`TestReloadWithABadKeyAppliesTheRest` saves a config with a bad key into a
running session, then saves one without it. The first reload must name
`ctrl+nope` and apply Alt+F7. The second must apply Alt+F8.

`TestConfigApplyListsTheKeysItCannotRead` runs `tuios config apply` with
`switch_workspace_3 = ['hyper+3']` in `config.d/keys.toml`. The output must
name the key, its file and the default key the action uses.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| `opt+` is rejected off macOS again | `ValidateKey`: the old `opt+`/`option+` rejection put back for `!isMacOS` | `TestMacConfigWorksOnLinux` (the doctor lists 25 keys tuios cannot read). With the doctor checks skipped, it fails at the log viewer, which lists the 25 keys | **caught** |
| The load rejects the whole file again | `LoadUserConfig`: return an error when `ValidateConfig` has errors, before the call to `DropUnreadableKeys` | `TestUnreadableKeyKeepsTheRestOfTheFile` (the TUI shows "1 config problem", the load failure, and not "2 config problems") | **caught** |
| The build before the fix | main at `d2f6277b` | `TestUnreadableKeyKeepsTheRestOfTheFile` (no config problem is shown). With that wait skipped, it fails at the prefix menu wait | **caught** |
| No default comes back | `DropUnreadableKeys`: the emptied action is left with no key | `TestUnreadableKeyKeepsTheRestOfTheFile` (Alt+2 leaves the session on workspace 1) | **caught** |
| The doctor reads the file as written | `loadKeybindConfig`: the call to `DropUnreadableKeys` cut | `TestDoctorMatchesTheAppWithBadKeys` (the doctor says the leader is `hyper+a`). The app half passes, as it should: the app was right | **caught** |
| The fallback default does not yield | `DropUnreadableKeys`: the `keyHolder` check cut | `TestDoctorMatchesTheAppWithBadKeys` (explain says Alt+1 runs `switch_workspace_1`). With the doctor checks skipped, Alt+1 leaves the session on workspace 1 | **caught** |
| Every dropped key names config.toml | `DropUnreadableKeys`: `fileOf` returns `config.toml` | `TestDoctorMatchesTheAppWithBadKeys` (the doctor names `config.toml` for `hyper+1`). With the doctor checks skipped, the log viewer does not name `config.d/keys.toml` | **caught** |
| The reload rejects a bad key again | `validateLayered`: return an error when `ValidateConfig` has errors | `TestReloadWithABadKeyAppliesTheRest` (the TUI says "Config not reloaded" and never names `ctrl+nope`) | **caught** |
| `config apply` lists no dropped key | `verbApplyConfig`: the `dropped_keys` field cut from the reply | `TestConfigApplyListsTheKeysItCannotRead` (the output does not say "tuios cannot read 1 key") | **caught** |

The key presses alone cannot catch the first control. A dropped `opt+1` falls
back to the default `alt+1`, which is the same key on Linux. The doctor and the
log viewer are what tell the two builds apart.

## The saver's frame rate, and the tuios-web window limit

`TestScreensaverPaintsAtSixtyAtMost` reads the saver's frame rate off the
screen as the time an effect hides a marker. middleout counts frames (99), and
tuffbaby paces its clip by the clock, and its bound at max_fps 240 is 1.8 times
its own time at max_fps 60 on the same machine. The max_fps 30 case is the
positive half: it fails if max_fps never reaches the saver.

`TestWebClampsAWindowPastTheLimit` sends tuios-web a resize to 300x60, then
one to 1500x900. The first must reach the pane, which is the positive half.
Since sip v0.8.5 the second is clamped to 1200 columns and 208 rows (250000
cells), and the pane must take exactly that size less the chrome measured at
300x60. Before v0.8.5 sip ignored the second resize, and the test was
`TestWebIgnoresAWindowPastTheLimit`.

Each control below was run on 2026-10-08 at a load average near 30.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The saver ticks at NormalFPS again | main at `1db4d240` | `TestScreensaverPaintsAtSixtyAtMost` (middleout at max_fps 240 hides the screen for 0.48 s, want at least 1.2 s) | **caught** |
| The clock disagrees with the tick | `screensaverBuild`: `NewVirtualClock(s.NormalFPS)` in place of `screensaverRate(s)` | `TestScreensaverPaintsAtSixtyAtMost` (tuffbaby at max_fps 240 keeps the marker hidden for more than 16.81 s, which is 1.8 times its 9.34 s at max_fps 60. With the fix: 9.52 s at 60 and 10.08 s at 240. Rerun at a load average near 5) | **caught** |
| max_fps never reaches the saver | `screensaverRate` always returns `screensaverFPS` | `TestScreensaverPaintsAtSixtyAtMost` (middleout at max_fps 30 hides the screen for 1.62 s, want at least 2.6 s) | **caught** |
| No window limit | `cmd/tuios-web/main.go`: the `MaxWindowDims` line cut (main at `1db4d240`, sip v0.8.4) | `TestWebIgnoresAWindowPastTheLimit` (tuios-web grows from 45 MB to 857 MB, and the shell stops answering within 10 s) | **caught** |
| No window limit, sip v0.8.5 | `cmd/tuios-web/main.go`: `MaxWindowDims` 4096x4096 and `MaxWindowCells` 1 << 30 (branch `chore/sip-0.8.5`) | `TestWebClampsAWindowPastTheLimit` (the shell stops answering within 10 s of the 1500x900 resize) | **caught** |

With the sip v0.8.4 fix, tuios-web held 55 MB before the oversized resize and
59 MB after it. With sip v0.8.5 and the clamp, it held 57 MB before and 240 MB
after, at 1174x204 cells in the pane. Cutting only the `MaxWindowCells` line
leaves sip's default of 250000 cells, which is the same limit, so no test
fails on that change. The unit test `TestSaverClockRunsAtTheRateThePaintingDoes` in
`internal/app` holds the clock to the tick at 10, 30, 60, 120 and 240 fps, and
fails on the second control too.

## A theme from a config file reaches the panes

`TestAThemeInAConfigDFileReachesThePanes` saves a new config.d file that sets
`theme = "dracula"` and `border_style = "double"`. The double border is the
positive half: it shows that the reload reached the client. The test then
prints SGR 31 in the pane, and that colour must no longer reach the host as
palette index 1.

The control was run on 2026-10-08.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The reload does not push the palette into the panes | `ApplyReloadedConfig`: the `m.UpdateAllWindowThemes()` call cut (main at `b9522fb7`) | `TestAThemeInAConfigDFileReachesThePanes` (the double border appears, and SGR 31 still reaches the host as index 1) | **caught** |

A probe on the same build showed the bug for every kind of file: a new
config.d file, an edit to a config.d file, an included file and config.toml,
with and without a daemon. In each case the border took the new theme's colour
and the pane kept its old palette.

## Option characters and keyboard layouts (#566, #575)

`option_layout_keys_test.go` has nine tests. Each one sends the bytes that a
terminal writes for the key: WezTerm with a composing right Option key,
Terminal.app with Option on "Normal", iTerm2 with Esc+ on US and on French
AZERTY, and Kitty protocol reports for AZERTY and for a composing Option key.
`TUIOS_E2E_PLATFORM=darwin` puts tuios on its macOS defaults and its macOS key
paths. The default is `option_glyphs = "bind"`, the behaviour Terminal.app
and iTerm2 users already had, and `"type"` is the opt-in for #566. Every test
that says a key does nothing also presses a key that does something, in the
same session. Each control below cut one piece of wiring, built a binary, and
ran the named test against it, on 2026-10-08 on branch
`fix/option-composition-layouts`.

| Control: what was cut | Test | Where it failed |
| --- | --- | --- |
| `bindingKeys` asks for a composed character's chord as a plain key, not in the Option-glyph tier | `TestComposedOptionCharacterReachesThePane` | the shell never prints `x2•¡y` |
| `expandInto` skips the `OptionGlyphKey` claim | `TestNormalOptionCharacterSwitchesWorkspaceByDefault` | the default config leaves the session on workspace 1 for `£`, want 3 |
| `bindingKeys` never asks for the US-layout tier | `TestEscPlusOptionChordsOnAUSLayout` | ESC # leaves the session on workspace 1, want 3 |
| `expandInto` ignores `keyboard_layout = "other"` | `TestAzertyEscPlusWithLayoutOther` | ESC & moves the pane to workspace 7 |
| `expandInto` claims a US alias as a plain key | `TestOwnBindingBeatsTheUSAlias` | ESC & moves the pane to workspace 7, want workspace 2 |
| `bindingKeys` asks for the US-layout tier without `KeyFitsUSLayout` | `TestAzertyKittyReportsPickTheRightWorkspace` | AZERTY Option and the 1 key moves the pane to workspace 7 |
| `bindingKeys` drops the `shiftedKey` spelling | `TestAzertyKittyReportsPickTheRightWorkspace` | AZERTY Option, Shift and the 1 key leaves the session on workspace 3, want 1 |
| `lookupAction` does not hand back the host's base-layout key | `TestAzertyKittyReportsPickTheRightWorkspace` | AZERTY Option and the 1 key moves the pane to workspace 7 |
| `isLeaderKey` drops the `composedChords` loop | `TestOptionLeaderFiresOnTheComposedCharacter` | `¡` with `leader_key = "opt+1"` never opens the prefix menu |
| `expandInto` fills the Option-glyph tier whatever `option_glyphs` says | `TestKittyComposedCharacterFollowsOptionGlyphs` | the Kitty `CSI 176;4u` moves the pane to workspace 8 under `"type"` |
| The input handler drops the `optionGlyphsApply` check before the note | `TestKittyComposedCharacterFollowsOptionGlyphs` | the note about Option shows under `"type"` |
| `composedChords` ignores the base-layout key | `TestAzertyCedillaIsNotOptionC` | AZERTY ç with base-layout key 9 moves the pane to workspace 4 |

Against main at `797ea69e`, the four AZERTY and alias tests fail as
expected, and `TestEscPlusOptionChordsOnAUSLayout` passes, as the positive
half should. The tests that need the macOS key paths cannot be judged on a
build of main, because main does not read `TUIOS_E2E_PLATFORM`. The controls
above are the evidence for those.

Against the previous head of this branch, `6a4af01e`, run in macOS mode, the
tests the review asked for fail where they should:

| Test | Where it failed on `6a4af01e` |
| --- | --- |
| `TestOptionLeaderFiresOnTheComposedCharacter` | `¡` never opens the prefix menu |
| `TestKittyComposedCharacterFollowsOptionGlyphs` | the Kitty `°` moves the pane to workspace 8 under `"type"` |
| `TestAzertyCedillaIsNotOptionC` | AZERTY ç with base-layout key 9 moves the pane to workspace 4 |

## A default config in a browser session (clienttests)

The two tests are in `clienttests/config.spec.mjs`. They read the log viewer
of a browser session. The first one attaches to a server whose file sets
`notify = true`. The log must hold an INFO notice for it and no WARN line.
That is the positive half. The second one attaches to a server whose file has
no `[notifications]` table. Its log must hold no `[notifications.agent]` line.

The controls were run on 2026-10-08.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The browser check reads the resolved default | `browserAlertNotices`: the `alerts.Notify != nil` gate cut | the default test (the log holds the notify line). The positive half still passes. | **caught** |
| The whole fix removed | tuios-web built from main at `2fcc83a4` | both tests (the line is a WARN config problem, and the default config logs it) | **caught** |

## tuios-web hands --allow-host to sip

`TestWebAllowHostLetsAProxyNameIn` starts tuios-web on loopback with
`--no-auth --allow-host term.example`. A handshake with Host `term.example`
must get a session, which is the reverse proxy case. A handshake with Host
`evil.example` must get a 403, which is the DNS rebinding case. The loopback
address and `localhost` must get a session, so a server that refuses every
name cannot pass.

sip v0.8.5 checks the Host header before tuios-web's own middleware runs. A
name that tuios-web does not put in `sip.Config.AllowedHosts` gets a 403 from
sip, whatever tuios-web allows.

Each control below was run on 2026-10-08 on branch `chore/sip-0.8.5`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| tuios-web does not hand the names to sip | `webAccess.apply`: the `cfg.AllowedHosts = a.allowHosts` line cut | `TestWebAllowHostLetsAProxyNameIn` (Host `term.example` gets 403, want 101), and the unit test `TestAllowHostAddsAName` | **caught** |
| The Host check is off | `webAccess.apply`: `"*"` added to `cfg.AllowedHosts` | `TestWebAllowHostLetsAProxyNameIn` (Host `evil.example` gets 101, want 403) | **caught** |

## State a reattach has to carry that no cell shows

`TestWireCarriesTheCursorState` and `TestWireNarrowsAWiderClient` in
`internal/session` restore a snapshot, feed both emulators the sequence that
reads the state, and compare them. The ghostty build runs the same cases pure to
ghostty, ghostty to pure and ghostty to ghostty
(`TestGhosttyWireCarriesTheCursorState`, `TestGhosttyWireNarrowsAWiderClient`).
Each case runs in both wire forms, so one case is two subtests. These are unit
tests, kept because they hold the wire. On 2026-10-08 each control cut one call
site and ran the tests on both builds. Every control failed where shown, and the
same tests passed on the branch.

On `main` at `797ea69e`, with only the tests added, 32 of 56 pure cases fail,
and so do all 8 wider-client cases. On the ghostty build 28, 52 and 44 of 56
fail pure to ghostty, ghostty to pure and ghostty to ghostty.

| Control: what was cut | Where it failed |
| --- | --- |
| The OSC 8 the ghostty restore writes for the pen's link | 12 subtests: the three `open-link` cases into a ghostty client |
| `penExtrasLocked` in the ghostty `CursorPen` | 16 subtests: the `open-link` cases from a ghostty daemon, and into one where the client's pen is read |
| `RestoreProtectedCells` for the active screen in `ApplyTerminalState` | 12 pure, 48 ghostty: the six cases a selective erase reads on the active screen. `protected-cells-ed-erases`, the positive half, passes |
| `RestoreProtectedCells` for the main screen | 2 pure, 8 ghostty: `protected-under-alt` |
| `RestoreCursorProtected` | 2 pure, 8 ghostty: `protected-pen` |
| The ghostty painter's protection (`appendStyledLineProt` given none) | 28 subtests: seven `protected-*` cases into a ghostty client |
| The ghostty shadow grid's `setProtected` from the library's cells | 28 subtests: seven `protected-*` cases from a ghostty daemon |
| `RestoreLastPrinted` | 14 pure: every `rep-*` case but the one after a reset. 40 ghostty, where a ghostty client still gets the character the restore painted last |
| The ghostty restore's print and erase of REP's character | 8 subtests: `rep-line-drawing` and `rep-not-last-on-screen` into a ghostty client |
| The ghostty reprint of a pending wrap through the character set | 4 subtests: `rep-line-drawing-under-pending-wrap` into a ghostty client |
| The ghostty scanner's record of an ASCII character | 18 subtests: the `rep-*` cases from a ghostty daemon |
| `RestoreSavedCursor` for the active screen | 20 pure, 72 ghostty: every `saved-*` case |
| `RestoreSavedCursor` for the main screen | 2 pure, 8 ghostty: `saved-under-alt` |
| The ghostty restore's saved cursor on the active screen | 32 subtests: eight `saved-*` cases into a ghostty client |
| The ghostty restore's main saved cursor before 1049 | 4 subtests: `saved-under-alt` into a ghostty client |
| The DECSC hook in the ghostty scanner | 28 subtests: seven `saved-*` cases from a ghostty daemon |
| The pure emulator's cursor restore when 1049 is reset | 2 pure, 4 ghostty: `saved-under-alt` into a pure client |
| The narrowing resize in `ApplyTerminalState` | 8 pure, 32 ghostty: every wider-client case |

### Review fixes

The review found a regression, a cost and untested paths. Each new test was run
against the code it covers with that code cut, on 2026-10-08.

| Control: what was cut | Test | Where it failed |
| --- | --- | --- |
| The per-screen saved character sets (the branch before this fix, one slot for both screens) | `TestConform_AlternateScreen` | both cases where a save on the alternate screen must leave the primary screen's sets |
| The C1 check in the scanner's record of REP's character | `TestLastPrintedSkipsControls`, ghostty build | NEL and CSI sent as UTF-8 |
| The protection rotation in `rotateExt` | `TestConform_SelectiveErase` | IL, DL, and RI in a region |
| `moveProtected` | `TestConform_SelectiveErase` | IL and DL inside side margins |
| The protection clear in `FillArea` | `TestConform_SelectiveErase` | an erase in line unprotects the cells it erases |
| The protection clear in `blankRows` | `TestConform_SelectiveErase` | a line scrolled in at the bottom is not protected |
| The protection scroll in `scrollWindow` | `TestConform_SelectiveErase` | protection scrolls with its row |
| The protection shift in ICH, then in DCH | `TestConform_SelectiveErase` | protection moves with an insert, and with a delete |
| The height in `ApplyTerminalState`'s sizing (width only) | `TestWireNarrowsAWiderClient`, `TestGhosttyWireNarrowsAWiderClient` | every taller and bigger case, `line-feed-on-the-last-row` among them |
| `liveScreenLocked` always the main screen | `TestGhosttyWireCarriesTheCursorState` | `saved-on-each-screen` from a ghostty daemon, 4 subtests |
| The ghostty restore's switch always 1049 | `TestGhosttyWireCarriesTheCursorState` | `saved-on-each-screen` into a ghostty client, 4 subtests |
| `ResizeEmulatorToSnapshot` in `RestoreTerminalStates` | `TestRehydrationMatrix` | **not caught**: `ApplyTerminalState` sizes the emulator too, so the call only mirrors `primePaneFromDaemon` |

`TestGhosttyDivergence_ProtectionLost` pins two places the backends still
differ: a reflow drops protection on the pure emulator, and DECSTR stops it
there. Each case fails when the backends start to agree.

## herdr plugins on Windows (#579)

These are unit and platform-boundary tests in `internal/shimlink`,
`internal/herdrplugin` and `internal/stopevent`. No machine here runs Windows,
and Wine has no AF_UNIX, so no end-to-end test reaches this code. On
2026-10-09 each control below cut one piece of wiring, built the Windows test
binary, and ran the named test under Wine 10. The sweep control ran on Linux.
Every control failed where shown, and the same tests passed on the branch
build.

| Control: what was cut | Test | Where it failed |
| --- | --- | --- |
| The `withExtension` call in `ResolveProgram` | `TestResolveProgramOnThisPlatform` | `bin/herdr-nvim`, `./bin/herdr-nvim` and the absolute path all `plugin_command_not_found` |
| The job assignment in `track` (`if useJobs`) | `TestStopPluginKillsWhatTheCommandStarted` | the child of a running command still runs after `StopAll` |
| The job assignment in `track` (`if useJobs`) | `TestPluginProcessesEndWithTheDaemon` | the plugin's child still runs after the host is terminated |
| The `ntResumeProcess` call in `track` | `TestStartWithoutAJob` | the command never runs: no pid written in 20 s |
| The `setLimits(g.job, 0)` call in `release` | `TestAFinishedCommandLeavesItsProgramRunning` | the program a finished command opened is killed |
| The `Alive` check in `sweep` | `TestInstallFileSweepsLeftovers` | the running process's `.new-78` file is removed |
| The `ERROR_FILE_NOT_FOUND` case in `stopevent.Request` | `TestRequestReachesTheDaemon` | a request with no daemon returns "File not found", not `ErrNotWaiting` |

Two things are not caught:

- Cutting the `group.release()` call in the runner leaves every test green.
  The job then stays open with its kill-on-close limit, so a program a
  finished command opened ends with the daemon, and the handle leaks. No test
  ends the daemon after a command finishes.
- `TestInstallReplacesARunningLink` passes under Wine, but nothing shows that
  Wine refuses to replace a running program, so the rename-aside path may not
  run there. The test runs on real Windows in the `go test (windows)` job,
  which is the proof of that path.
## An xterm.js host gets images as glyphs (issue 567)

`TestCellBoundImageHostGetsGlyphs` runs tuios under `testdata/hostterm` with
`-xtversion`, so the host names itself the way Netcatty's xterm.js does.
tuitest's emulator answers the kitty query OK and lists sixel in DA1, so the
host claims both protocols. A pane draws a kitty picture, scrolls, and draws a
sixel picture. The xterm.js host must be sent no kitty graphics and no sixel,
and must show the sixel picture as glyphs on every one of its cells. The
positive half names the host kitty with the same probe and must get kitty
graphics and no glyphs, so a probe that never reads XTVERSION cannot pass.

The test runs in the standalone TUI and against a daemon. Two more cases play a
slow terminal: hostterm answers DA1 itself, 500 ms after the question, which is
after the startup probe gives up. In `xtermjs-late-da1` only DA1 is late. In
`xtermjs-late-both` and `xtermjs-late-both-daemon` XTVERSION is late too, so
only the program's input reader sees it.

Each control below was run on 2026-10-08 on branch `fix/kitty-image-ghosting`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| The probe does not drop graphics for xterm.js | `probeTerminal`: the `dropCellBoundGraphics(caps, response)` line cut | `TestCellBoundImageHostGetsGlyphs/xtermjs-standalone` and `/xtermjs-daemon` (kitty and sixel sent, 0 of 80 cells painted) | **caught** |
| Sixel is not pinned off | `dropCellBoundGraphics`: `caps.SixelPinned = true` cut | `TestCellBoundImageHostGetsGlyphs/xtermjs-late-da1` (the late DA1 turned sixel on: sixel sent, 0 of 80 cells painted) | **caught** |
| A late XTVERSION answer is not read | `OS.Update`: the `handleHostVersion(msg)` call cut | `TestCellBoundImageHostGetsGlyphs/xtermjs-late-both` and `/xtermjs-late-both-daemon` (kitty and sixel sent) | **caught** |
| The first fix only, before the late answer was handled | binary built from `cb0a899b` | `xtermjs-late-both` and `xtermjs-late-both-daemon` fail, `xtermjs-late-da1` passes | **caught** |

The SSH server path, where `sixelProbe` now asks XTVERSION as well as DA1, has
no e2e harness here. It was checked by hand: xterm.js 6.1 with its image addon,
in headless Chromium, connected to `tuios ssh` through a real `ssh`. Before the
change, `chafa` drew a sixel picture that stayed on screen after `clear`. After
it, the picture was block glyphs and `clear` removed it.
## A link rides the ssh master a person opened

`TestLinkRidesTheSharedSSHMaster` gives the hub daemon one host, `build`,
reached through an ssh stand-in that records every argv it runs. With a
folder `$XDG_RUNTIME_DIR/tuios/cm` of mode 0700, the link's ssh and the ssh
of `tuios hosts test` must carry `ControlMaster=no` and
`ControlPath=<folder>/%C`, and the link must still come up. With no folder,
with a folder of mode 0777, and with an ssh config that gives the host a
`ControlPath` of its own, neither ssh may name the folder.

Each control below was run on 2026-10-08 on branch
`feat/link-shares-gui-ssh-master`. The log is
`~/.cache/agent-tmp/proof/v2/wave4/e2e/negative-controls-master.txt`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A host with no path gets no sharing options | `sharingOptions`: `return reuseOptions()` replaced by `return nil` | `TestLinkRidesTheSharedSSHMaster/a_master_folder_of_the_user's_own` (the link's ssh names no master) | **caught** |
| A folder others can write is trusted | `reuseOptions`: the mode check cut | `TestLinkRidesTheSharedSSHMaster/a_master_folder_others_can_write` (the link's ssh names the folder) | **caught** |
| The link may open a master of its own | `reuseOptions`: `ControlMaster=auto` in place of `no` | `TestLinkRidesTheSharedSSHMaster/a_master_folder_of_the_user's_own` (`ControlMaster=no` missing) | **caught** |
| The person's own ControlPath is overridden | `reuseOptions`: the `configSharesConnections` check cut (run later on 2026-10-08, log `~/.cache/agent-tmp/proof/v2/wave4/review/negative-controls-master.txt`) | `TestLinkRidesTheSharedSSHMaster/the_user's_ssh_config_shares_connections_itself` (the link's ssh names the folder) | **caught** |

## The shared ssh master: review fixes

The unit tests are in `internal/federation/reuse_unix_test.go`. They guard
the security boundary and the timeout of the shared master folder.
`TestLinkRidesTheSharedSSHMaster` gains the case
`the_host_forwards_the_agent`: an ssh config that gives the host
`forwardagent yes` must keep the link's ssh off the folder.

Each control below was run on 2026-10-08 on branch
`feat/link-shares-gui-ssh-master`.

| Control | How | Tests that fail | Verdict |
| --- | --- | --- | --- |
| A child of ssh -G holds the pipes past the timeout | `querySSHConfig`: the `cmd.WaitDelay` line cut | `TestSSHConfigReturnsWhenAChildHoldsThePipes` (ssh -G took 30.0s, want at most 4.5s) | **caught** |
| ssh -G runs at every dial | `ReadSSHConfig`: the cache read cut | `TestSSHConfigIsAskedOncePerHost` (ran 4 times, want 2) | **caught** |
| A socket path too long for sun_path is used | `reuseOptions`: the length check cut | `TestReuseSkipsAControlPathTooLongForASocket` (a 67 byte folder is used, sockets of 108 bytes) | **caught** |
| A host that forwards the agent rides the master | `reuseOptions`: the `ForwardAgent` check cut | `TestReuseSkipsAHostThatForwardsTheAgent` (`forwardagent yes` and a socket path ride), `TestLinkRidesTheSharedSSHMaster/the_host_forwards_the_agent` (the link's ssh names the folder) | **caught** |
| A master folder that is a symbolic link is trusted | `privateDir`: `os.Stat` in place of `os.Lstat` | `TestReuseNeedsPrivateFolders` (the symbolic link is used) | **caught** |
| The parent folder is not checked | `reuseOptions`: the `privateDir(filepath.Dir(dir))` check cut | `TestReuseNeedsPrivateFolders` (a group-writable parent is used) | **caught** |
| A folder of another user is trusted | `privateDir`: the uid comparison cut | `TestPrivateDirRefusesAFolderOfAnotherUser` (`/proc/1/fd` counts as the user's own) | **caught** |
| ssh's stale socket line is reported | `cmdTransport.Diagnostic`: `withoutStaleMasterLines` cut | `TestDiagnosticDropsTheStaleMasterLine` (the `Control socket connect(...)` line is kept) | **caught** |
## The screen and scrollback from before tuios (discussion #588)

`TestExitKeepsTheHostScreen` starts tuios from a shell that has printed 60
lines, leaves it, and reads the host screen and the raw host stream. The exit
reset began with RIS (`ESC c`). RIS cleared the screen that leaving the
alternate screen had put back, and kitty and ghostty also drop the scrollback
on it. The fix replaces RIS with DECSTR, the soft reset. Run on 2026-10-09.

The positive half: each run waits until tuios has covered the 60 lines, and
asserts that tuios sent `CSI ? 1049 l`.

| Control: what was cut | Test | Result |
| --- | --- | --- |
| None: `main` at `8d3e07b9` | `standalone-quit`, `daemon-detach` | **caught**: the lines are gone, one RIS in each stream |
| The fix: `\033c` put back at the front of `ResetSequence` | `standalone-quit`, `daemon-detach` | **caught**: the same failures |
| `tape play`'s own copy of the reset | none | **not caught**: no test leaves `tape play` |
| `forceExit`, the reset after a wedged quit | none | **not caught**: no test wedges the client |

`TestExitTurnsOffWhatItTurnedOn` sets a bar cursor in a pane, quits from
terminal mode and from window management mode, and reads the last value of
each DEC private mode, the cursor style, the kitty keyboard flags,
modifyOtherKeys and the pointer shape in the host stream. Without RIS, each of
these needs its own reset. It passes on `main` too, because RIS is not in the
list it reads: it guards the explicit resets that now carry the load.

| Control: what was cut | Test | Result |
| --- | --- | --- |
| `\033[?2031l` in `ResetSequence` | both subtests | **caught**: "tuios left DEC mode 2031 at h" |
| The OSC 22 reset in `ResetSequence` | none | **not caught**: no run moves the mouse, so tuios never sets a pointer shape |

## A worktree path the Inbox line cuts short

`TestRiskInWorktree` and `TestRiskInWorktreeRedacted` report a Claude Code
`Edit` of `demo/greet.py` inside a worktree that `worktree new` made under
`XDG_DATA_HOME`. One pane is the worktree session's own pane. The other is a
pane of another session that starts in the worktree. The Inbox line is clipped
to 100 characters, and in the second test a part of the path is also redacted
to `***`. The outside-the-worktree rule read what the line showed as the whole
path, and marked the call. The fix reads a path in the line only up to the cut
or the stars. Run on 2026-10-09.

The positive half: in the same daemon, an `Edit` of `/etc/hosts` is marked
`outside the worktree`. An `Edit` of a long path under `/etc` is marked
`outside the worktree` and `cut short`. Each test also checks that the line is
cut inside the worktree's path, so the case is not vacuous.

| Control: what was cut | Test | Result |
| --- | --- | --- |
| None: `main` at `937cc693` | both | **caught**: both panes are marked `outside the worktree` |
| `Shown: true` in `riskOfLine` | both | **caught**: the same failure |
| The `shownOutside` call in `outside` (always false) | `TestRiskInWorktree` | **caught**: the long `/etc` path is not marked `outside the worktree` |
| The `***` arm of the cut short mark in `riskOfLine` | `TestRiskInWorktree` | **caught**: `/etc/***.conf` is not marked `cut short` |

## A "..." that is not a clip mark

`TestRiskLineClipOnlyAtEnd` reports approval lines with `set-agent-state` from
a pane whose root is a linked worktree at a short path. The first fix cut a
path at any `...`. So `approve Write: <root cut 2 short>...` on a line that was
not clipped read as a path on the way into the worktree, and was not marked.
That failed open. The fix cuts a path at `...` only in the last word of a line
that `integration.Clipped` says was clipped. A `***` cuts a path in any word.
The test also covers a `...` in the middle of a clipped line, a clipped line
whose last word is not a path, a clipped line cut inside the root, and `***`
in a word before the last one. Run on 2026-10-09.

| Control: what was cut | Test | Result |
| --- | --- | --- |
| None: the first fix at `64c6df2d` | `TestRiskLineClipOnlyAtEnd` | **caught**: `lit-write`, `lit-mid` and `lit-mid-clip` are not marked `outside the worktree` |
| `Clipped: false` in `riskOfLine` | `TestRiskLineClipOnlyAtEnd` | **caught**: `clip-path`, cut inside the root, is marked `outside the worktree` |
| `Clipped: false` in `riskOfLine` | `TestRiskInWorktree` | **caught**: both panes mark the `Edit` inside the worktree `outside the worktree` |
| The `***` arm of `shownPart` | `TestRiskLineClipOnlyAtEnd` | **caught**: `mask-in` is marked `outside the worktree` |
## stream-pane, attach-presence and the size lease

`TestPhoneStreamsAPaneAndAnswersTheInbox` runs the real `tuios stdio-proxy
--as phone` and plays the phone on its stdin and stdout. On 2026-10-09 each
control below cut one line of wiring, built a binary, and ran the test
against it. Each control failed where shown, and the test passed on the
branch build. The transcript the test writes is
`stream-pane-transcript.txt` under `TUIOS_E2E_FRAMES`.

The positive halves: a made-up nonce gets `not_human` before the presence
nonce answers, `attach-presence` on a stream the hub did not vouch for gets
`forbidden` before a vouched stream gets a nonce, and the nonce of a closed
presence gets `not_human` before a new presence answers the same hold.

| Control: what was cut | Result |
| --- | --- |
| The presence clause in `matchHumanNonceClient` (`match := same && attach`) | **caught**: "the presence nonce did not answer the held approval", `not_human` |
| The `leasedSizeLocked` call in `PTY.Resize` | **caught**: "a resize under the lease changed the leased pane" |
| The lease release in `paneStream.close` | **caught**: "the pane did not go back to the size asked for" |
| The `decModes` call in `snapshotVT` | **caught**: "the snapshot did not set DEC mode 1" (and 2004, 1000, 1006, 66) |
| The alternate screen block in `snapshotVT` | **caught**: "the snapshot of a pane on the alternate screen: alt false" |
| The gap rebuild in `paneStream.stream` (the `recoverGap` call) | **caught**: the stream goes silent after the phone falls behind, "read a pane frame: stream read timed out" |

## stream-pane limits

`e2e/tui/stream_pane_limits_test.go` holds stream-pane to the limits around
its happy path. On 2026-10-09 each control below put back one line of the
review fixes, built a binary, and ran the test against it. Each control
failed where shown, and each test passed on the fixed build. The branch
before the fixes failed the same three tests the same way.

| Control: what was put back | Test | Result |
| --- | --- | --- |
| `maxPaneInputFrame = 1 << 20` | `TestStreamPaneClientFrameMemory` | **caught**: "the daemon grew by 96 MiB for 96 streams" (fixed build: +0 MiB) |
| `PTY.SetLease` resizing on every call, with no `leaseInterval` | `TestStreamPaneLeaseChurn` | **caught**: "301 lease frames resized the pane 301 times in 1.73s" (fixed build: 2 times) |
| The `recheck` call in `paneStream.stream` left out | `TestStreamPaneFromAPane` | **caught**: "the stream outlived the read grant" |

`TestStreamPaneHeldToTheLinkPolicy` and `TestStreamPaneEndsWithItsWindow`
passed on the branch before the fixes. They hold behaviour the review
checked and found correct: input and lease frames from a peer without
`write` get `forbidden`, a presence nonce does not answer from a stream the
hub did not vouch for, and a stream ends with `X` when its window closes or
its session is killed. `TestStreamPaneLeaseOnTheDesktop` saves the frames
`stream-lease-held` and `stream-lease-released` under `TUIOS_E2E_FRAMES`: an
attached client draws the leased pane at 30 by 6 in its full rectangle, and at
58 by 36 again once the lease ends.

## stream-pane review fixes

`e2e/tui/stream_pane_review_test.go` holds the fixes from the review of the
stream-pane pull request. On 2026-10-09 each control below cut one line of a
fix, built a binary, and ran the test against it. Each control failed where
shown, and each test passed on the fixed build. The tests write
`presence-nonce-scope.txt`, `stream-lease-expiry.txt`,
`stream-lease-behind-input.txt` and `stream-snapshot-charge.txt` under
`TUIOS_E2E_FRAMES`.

| Control: what was cut | Test | Result |
| --- | --- | --- |
| The `covers` check in `humanNonceFor` (`if !ok \|\| !h.covers(sessionID)` to `if !ok`) | `TestPresenceNonceScope` | **caught**: "a presence for session phone answered in session other: reply-approval ... applied:true" |
| The `expireLease` call in `paneStream.stream` | `TestStreamPaneLeaseExpires` | **caught**: "the lease of a silent client did not end within 40s" (fixed build: `lease_expired` after 30s) |
| `queueInput` writing to the pane on the goroutine that reads the client | `TestStreamPaneLeaseBehindBlockedInput` | **caught**: "a lease frame behind input the pane does not read was not applied within 5s" |
| The read budget charge in `snapshotSubscribe` (charge 0) | `TestStreamPaneSnapshotCharged` | **caught**: "a second snapshot was taken while the first held the budget" (fixed build: `busy`) |

The positive halves: the scoped presence still acts in its own session, a
presence with no session answers the approval in the other session, a client
that renews its lease every 10 seconds keeps it past 30 seconds, input past
the queue gets `E busy`, and the budget frees once the first stream ends.
## agent-transcript and the transcript event

`TestPersonReadsAgentTranscript` joins a pane to a Claude Code transcript
fixture with the real `agent-hook claude-code` SessionStart, and reads it
through the real `tuios stdio-proxy --as phone` with a presence nonce. On
2026-10-09 each control below cut one line, built a binary, and ran the
test against it. Each control failed where shown, and the test passed on
the branch build. The artifact is `agent-transcript.txt` under
`TUIOS_E2E_FRAMES`.

The positive halves: before the read that works, a read without a nonce, a
made-up nonce, and the person's nonce on a stream the hub did not vouch for
get `not_human`. A link whose host lacks `respond` gets `forbidden`. A
process in the pane gets `forbidden` for its own presence and `not_human`
for a live local presence nonce.

| Control: what was cut | Result |
| --- | --- |
| The `humanNonceHeld` and `humanNonceFor` checks in `verbAgentTranscript` (rerun 2026-10-09 after the rebase onto nonce scope; with only `humanNonceHeld` cut, `humanNonceFor` still refuses, so it is not caught) | **caught**: "a read without a nonce: want not_human, got <nil>" |
| `respond` for `agent-transcript` in `verbCapabilities` (made `list`) | **caught**: "a link without respond: want forbidden naming respond, got <nil>" |
| The `noteTranscriptGrowth` call in `readAgentTranscript` | **caught**: "no transcript event: stream read timed out" |
| `foldResults` in `forward` | **caught**: "the appended call: want the text and a running Bash call" |
| The file id comparison in `parseCursor` | **caught**: "a made-up cursor: want reset with the newest entries" |
| The first record in `fileID` | **caught**: "a replaced file: want reset with its two entries", got `AFTER-REPLACE` alone with reset false |
| The secret mask in `transcriptText` | **caught**: the Bash call's target carries `sk-live-e2e-SECRET` |
| `ansi.Strip` in `transcriptText` | **caught**: the Bash result and the last answer carry `[31m` and `[2J` |

### A transcript directory deleted and made again

`TestTranscriptWatchOutlivesItsDirectory` joins two panes to transcripts in
one directory, deletes the directory, and makes it again. It then checks
that a new join in the directory and the old join both get `transcript`
events. On 2026-10-09 each build below ran the test. The test passed on the
fixed build three runs in a row. The artifact is `transcript-watch.txt` under
`TUIOS_E2E_FRAMES`.

| Control | Result |
| --- | --- |
| The watcher before the fix (`transcript_watcher.go` from `fb5d07fd`) | **caught**: "no transcript event: stream read timed out" for the new join |
| The `markLost` call cut from `TranscriptWatcher.run` | **caught**: "no transcript event: stream read timed out" for the new join |

### Paging back through a transcript with before

`TestPersonPagesBackThroughATranscript` reads a transcript of 80 entries from
its newest page back to its first with `before`, 7 entries a page. Each
record gives two entries, so pages end inside records. The pages joined
must equal one read of everything. On 2026-10-09 each build below ran the
test, and the test passed on the branch build. The artifact is
`transcript-pages.txt` under `TUIOS_E2E_FRAMES`.

| Control | Result |
| --- | --- |
| The branch before the change (no `before`, no `older`) | **caught**: "one read of everything: want 80 entries and no older", `older` missing |
| The filter that keeps only the entries before the cursor, cut from `back` | **caught**: "the pages joined" with entries twice |
| The record at a cursor inside a record left out of `back`'s window (`stop = next` cut) | **caught**: "page 10 is empty" |

### Diff colours for the phone

`TestPersonSeesDiffColours` reads edits of a Go file, a Python file and a
text file. It checks the `spans` and `words` of each diff line, in UTF-16
offsets, with a line that holds an emoji. Then it reads twelve Writes of a
300-line Go file and checks that only the newest has spans. On 2026-10-09
each build below ran the test, and the test passed on the branch build three
runs in a row. The artifact is `transcript-colours.txt` under
`TUIOS_E2E_FRAMES`.

| Control | Result |
| --- | --- |
| The branch before the change (no `spans`, no `words`) | **caught**: "the Go context line: want func as kd and greet as nf, got []" |
| Byte offsets sent in place of UTF-16 offsets (`utf16Index.at` returns its input) | **caught**: "a bad span {S:23 E:27 K:nx}" past the end of the emoji line |
| The pairing of removed and added lines cut from `styleHunk` | **caught**: "the changed words: want name at [20,24) and fullName at [20,28), got [] and []" |
| The budget check cut from `reader.style` | **caught**: "the colour budget: Write 0 (newest false) has plain false and 2700 spans" |

### agent-transcript review fixes

`agent_transcript_review_test.go` holds the review fixes of draft PR #603. On
2026-10-09 each control below cut one line, built a binary, and ran the named
test against it. Each control failed where shown. The same tests passed on
the branch build three runs in a row. The artifacts are under
`TUIOS_E2E_FRAMES`, one file for each test.

The positive halves: a presence for the pane's own session and a presence
with no session read the pane. The same local connection reads before it
restricts itself. A limit of 1 reads. A pane names its own transcript and is
joined. The newest edit of a page is matched. The `.env` keys, the `BEGIN`
and `END` lines and an ordinary Python edit stay in the reply.

| Control: what was cut | Test | Result |
| --- | --- | --- |
| The `humanNonceFor` check in `verbAgentTranscript` | `TestTranscriptNonceScope` | **caught**: "a presence for session other read session convo" |
| `agent-transcript` made `scopeOpen` in `verbScopes` | `TestTranscriptNonceScope` | **caught**: "a restricted connection: want forbidden, got not_human" |
| The limit check made `*p.Limit < 0` | `TestTranscriptNonceScope` | **caught**: "limit 0: want invalid_params, got <nil>" |
| The `transcriptPathAllowed` call in `joinReportedTranscript` | `TestTranscriptPathRules` | **caught**: "a path outside the projects folder: want transcript_refused" |
| The own-pane check in `joinReportedTranscript` | `TestTranscriptPathRules` | **caught**: "pane A named a transcript for pane B: want transcript_refused" |
| `transcript.OpenRegular` replaced by `os.Open` in `transcriptview.Read` | `TestTranscriptPathRules` | **caught**: the read after the FIFO swap: "stream read timed out" |
| `Unwatch` clears every callback of the path again (`clear(fns)`) | `TestTranscriptSharedFileWatch` | **caught**: "no transcript event: stream read timed out" |
| Every entry finished in `decode`, oldest first | `TestTranscriptDiffBudget` | **caught**: "limit 1: the newest edit: want it matched", got `whole_replace` true |
| The cells not taken from the budget in `lineDiff` | `TestTranscriptDiffBudget` | **caught**: "limit 2: the older edit: want a whole replace", got whole false |
| The `CleanLines` call in `addHunk` | `TestTranscriptMasksMultiLineSecrets` | **caught**: "a diff carries a secret ENVMARKER" |
| The `maskSecretLines` call in `transcriptText` | `TestTranscriptMasksMultiLineSecrets` | **caught**: "the Bash result carries a secret PEMBODYMARKER" |

Not caught here: the bound on memory for one call. `decode` keeps about
twice `limit` entries, and their text is cut before `Clean`, but no test
measures the daemon's memory. The diff controls above show that an entry the
page does not carry is not finished.
## Web Push to a phone

`TestWebPushToAPhone` runs a real daemon, a stub push service on loopback and
the phone link. The test is the phone. It registers with `register-push` and
the nonce of `attach-presence`. The real Claude Code hook holds an approval.
The test decrypts the push with its own RFC 8291 code, and checks the
payload, the headers and the VAPID JWT. Then it answers the approval and
decrypts the close. Run on 2026-10-09.

The positive halves: the same `register-push` with the person's nonce, over
the phone link, is taken. `tuios notify push register` outside every pane is
taken. A phone whose service answers 201 stays registered. The 503 phone gets
its push on the retry.

| Control: what was cut | Where it failed |
| --- | --- |
| The `n.web.note` call in `pushNotifier.note` | **caught**: no push to `/s/pixel` |
| The `requirePushPerson` call in `verbRegisterPush` | **caught**: a call with no nonce registers |
| `register-push` needs `list` instead of `respond` in the link policy | **caught**: the link without `respond` registers |
| The `ErrGone` arm in `webPusher.deliver` | **caught**: the phone whose service answered 410 is still listed |
| The retry in `webPusher.deliver` | **caught**: the payload the 503 refused never arrives |
| The pane check in `matchHumanNonceClient` | **not run**: it is the check `reply-approval` uses, and `TestAPaneCannotAnswerAsThePerson` and the stream-pane test cover it |

### Review: local addresses, redirects, a corrupt phones file

The security review of the branch found that `register-push` took an `https`
endpoint on any address. A linked machine with `respond` could aim the daemon
at this machine's loopback or network, and `list-push` told it what answered.
A push also followed a redirect on the same host, and tried a refused
redirect again. A phones file the daemon could not read was written over by
the next register. Run on 2026-10-09. The branch before the fixes fails all
three cases below.

`TestWebPushRefusesLocalAddresses` runs with no `allow_insecure`. It refuses
`https` to `127.0.0.1`, `[::1]`, `localhost`, `169.254.169.254` and
`0.0.0.0`. It registers `tuios-test.localhost`, a name that resolves to
loopback, and checks that the daemon never connects to the listener on that
port. The positive half is `TestWebPushToAPhone`: with `allow_insecure`, the
loopback stub gets every push, also at `http://127.0.0.1:<port>/up...?up=1`,
the endpoint shape an Android emulator gives through `adb reverse`.

`TestWebPushToAPhone` also registers a phone whose service answers 307, with
`notify.allow_http_redirects = true`. The redirect target must get nothing,
and no payload may arrive twice. `TestWebPushKeepsACorruptPhonesFile` starts
the daemon on an unreadable `subscriptions.json` and registers a phone.

| Control: what was put back | Test | Result |
| --- | --- | --- |
| The `hostAllowed` check cut from the `https` arm of `CheckEndpoint` | `TestWebPushRefusesLocalAddresses` | **caught**: "register-push to https://127.0.0.1:38833/s/x without allow_insecure: want invalid_params, got <nil>" |
| The dialer's `Control` hook never set | `TestWebPushRefusesLocalAddresses` | **caught**: "the daemon connected 1 times to a loopback address it reached by name" |
| The push client's `CheckRedirect` set to nil, so it follows | `TestWebPushToAPhone` | **caught**: "the push followed the redirect: /s/landed got 2 requests" |
| `CheckRedirect` refuses with an error, which retries | `TestWebPushToAPhone` | **caught**: "the push the redirect refused was tried again" |
| The rename cut from `webPusher.loadLocked` | `TestWebPushKeepsACorruptPhonesFile` | **caught**: "the unreadable phones file was not kept aside" |

### Review: nonce scope, Inbox items, secrets, padding, TTL

The second review of the branch asked for these fixes. Each control below
puts one fix back and runs `TestWebPushToAPhone`. Run on 2026-10-09, on the
branch rebased onto stream-pane at 26a474cc.

- A phone gets the Inbox of every session, so `register-push`, `list-push`
  and `remove-push` are an act in no one session. A presence made for one
  session can not do them.
- The VAPID `sub` is `https://tuios.dev/push` on every machine. The push
  service reads the JWT, and the old default named the machine.
- Each registration opens the Inbox item `phone NAME`, and says when it
  replaced a phone of that name. A link can not replace a phone. A removal
  over a link and a 404 or 410 from the push service open the item too.
- `tuios notify push register` reads the subscription from a file or stdin.
  The command line takes none of its secrets.
- Each payload is padded to 256, 512, 1024, 2048 or 3072 bytes.
- The open and the close of `approval`, `plan`, `ask` and `question` have a
  TTL of 3600 seconds.
- A payload past 3 KiB is cut, not dropped, and its options stay. The JSON
  has no HTML escapes.

The positive halves are in the same test: the unscoped presence registers,
lists and removes; the CLI registers from stdin; the person replaces `desk`
on this machine; every other push arrives and decrypts.

| Control: what was put back | Result |
| --- | --- |
| The `humanNonceFor` check cut from `requirePushPerson` | **caught**: "register-push with a presence made for one session: want not_human for another session, got <nil>" |
| The default subject with the host name after it | **caught**: "JWT sub "https://tuios.dev/push/HOST", want https://tuios.dev/push, with no machine name (HOST) in it" |
| The `notePhone` call cut from `verbRegisterPush` | **caught**: "no Inbox item about phone desk says "by this machine"" |
| `phoneRegisteredNote` never says replaced | **caught**: "no Inbox item about phone desk says "in place of the phone of that name"" |
| `register` always may replace, also over a link | **caught**: "register-push over a link in place of a registered phone: want forbidden, got <nil>" |
| The `notePhone` call cut from the `ErrGone` arm | **caught**: "no Inbox item about phone old says "no longer knows this phone"" |
| The `notePhone` call cut from a remove over a link | **caught**: "no Inbox item about phone pixel says "Removed from Web Push by the link from phone"" |
| The `--endpoint`, `--p256dh` and `--auth` flags put back | **caught**: "notify push register took the subscription's secrets on the command line" |
| `PaddedSize` returns the length unpadded | **caught**: "the push to /s/pixel pads a 221-byte payload to 221 bytes, not to a bucket" |
| `pushTTLWaiting` set back to 120 | **caught**: the open's headers show `Ttl:120`, and "the close of an approval has TTL "120", want 3600 like its open" |
| The old `fitPayload`: `json.Marshal` with HTML escapes, and options dropped to fit | **caught**: "the ask's push lost its options: got <nil>" |

The RFC 8291 Appendix A example is a wire compatibility test in
`internal/pushnotify` (`TestEncryptRFC8291AppendixA`). With the delimiter
`0x02` changed to `0x01` it fails.
## tuios pair

`e2e/tui/pair_test.go` acts as the phone against a real `tuios pair`. It
reads the link, makes the MAC, posts the key and checks the reply.
`TestPairedKeyOpensOnlyTheLink` then logs in with the installed key through a
scratch sshd (its own host key, port and authorized keys file, run as the
user) and links a second daemon through it. `TestPairQRCodeHoldsTheLink`
decodes the drawn code with `zbarimg`.

Each control below was run on 2026-10-09 on branch `feat/pair`. The logs are
in `~/.cache/agent-tmp/pair/neg/`.

| Control: what was cut | Tests that fail | Verdict |
| --- | --- | --- |
| The MAC check | `TestPairRefusesWrongRequests/wrong_mac`, `/swapped_key` (status 200, the key is written) | **caught** |
| `command="..."` in the installed line | `TestPairAddsAPhoneKey` (the line), `TestPairedKeyOpensOnlyTheLink` (ssh runs `echo NOT-FORCED`, and typing goes through) | **caught** |
| `restrict` in the installed line | `TestPairedKeyOpensOnlyTheLink` (`ssh -R` keeps running) | **caught** |
| `--as DEVICE` in the forced command | `TestPairedKeyOpensOnlyTheLink` (send-text goes through: the hub's own name gets the default policy) | **caught** |
| The token is spent on success | `TestPairAddsAPhoneKey` (the code pairs a second key) | **caught** |
| The timeout | `TestPairCodeExpires` (tuios pair does not end) | **caught** |
| The 8 KiB body cap | `TestPairRefusesWrongRequests/oversize_body` (status 200) | **caught** |
| The key type check | `TestPairAddsAPhoneKey` (an RSA 2048 key is written) | **caught** |
| The device name check | `TestPairAddsAPhoneKey` (`../evil` is written) | **caught** |
| The duplicate key check | `TestPairLeavesWhatIsThereAlone` (status 200, the file changes) | **caught** |
| The case-blind `[hosts]` table check | `TestPairLeavesWhatIsThereAlone` (`[hosts.phone]` is added beside `[hosts.Phone]`) | **caught** |
| The limit of wrong requests | `TestPairStopsAfterTooManyWrongRequests` (a good request after three wrong ones pairs) | **caught** |
| The limit set to 4 in place of 3 | `TestPairStopsAfterTooManyWrongRequests` (the same) | **caught** |
| The reply MAC over `tuios-pair-v1-ok` | `TestPairAddsAPhoneKey` (the reply MAC does not match) | **caught** |
| The QR code holds the printed link | `TestPairQRCodeHoldsTheLink/colour`, `/plain` (the link plus `x`) | **caught** |
| The `--listen-all` check | `TestPairListensOnlyWhereTheCodeSays` (`0.0.0.0:0`, `[::]:0` and `:0` run) | **caught** |
| The `--listen-all` warning | `TestPairListensOnlyWhereTheCodeSays` (no warning) | **caught** |
| The default binds each found address | `TestPairListensOnlyWhereTheCodeSays` (bound to `0.0.0.0` gives a listener on `[::]`) | **caught** |
| The question before the key is added | `TestPairAsksBeforeItAddsTheKey/n` (the key is written after `n`), `/y` (no question) | **caught** |
| The constant-time compare (`hmac.Equal`) | none | **not caught**: an end-to-end test cannot measure the timing |

### tuios pair: security review fixes

Each control below was run on 2026-10-09 on branch `feat/pair`. Each one
builds `cmd/tuios` with one fix taken out and runs the test that covers it.
The build from `bd0316ed`, before the fixes, fails the same tests. The logs
are in `~/.cache/agent-tmp/pair-review/neg/`.

| Control: what was cut | Tests that fail | Verdict |
| --- | --- | --- |
| The write deadline for the reply in `serveConn` | `TestPairReplyReachesAPhoneAfterASlowAnswer` (a `y` after 13 seconds: the phone gets EOF, and the key is written) | **caught** |
| The stop check before the key write, and the stop case in `confirm` | `TestPairStopDuringTheQuestionWritesNothing` (a `y` after Ctrl+C writes the key and the phone gets ok, while tuios says pairing is stopped) | **caught** |
| The check of the bound address in `openPairListeners` | `TestPairListensOnlyWhereTheCodeSays` (`--listen 0:0` runs on `[::]` without `--listen-all`) | **caught** |
| Skipping a connection closed before its first byte | `TestPairIgnoresConnectionsClosedUnused` (the fourth connection is refused: three unused ones stopped the pairing) | **caught** |
| `checkAuthorizedKeysModes` | `TestPairRefusesAKeysFileAnyoneCanWrite` (a file and a folder with mode 0666 and 0777 get a code; a 0660 file gives no warning) | **caught** |
| The one-line check of the key field | `TestPairAddsAPhoneKey` (`"not a key\n"+KEY` gets 200 and is written) | **caught** |

`TestPairIgnoresConnectionsClosedUnused` waits 400 ms between connections.
With 100 ms, the build before the fix passed: a wrong request holds the
listener for its 200 ms pause, so some connections got 429 and were not
counted.

### tuios pair: second review

Each control below was run on 2026-10-09 on branch `feat/pair`. Each one
builds `cmd/tuios` with one fix taken out and runs the tests named. The logs
are in `~/.cache/agent-tmp/pair-r2/neg/`. The build from `874cf2d2`, before
these fixes, fails every test named here. Part of that is the
`--accept-local` flag, which the test helpers pass and that build does not
have.

| Control: what was cut | Tests that fail | Verdict |
| --- | --- | --- |
| The stop when the `[hosts.DEVICE]` write fails | `TestPairWritesThePolicyBeforeTheKey` (config.toml is read-only: the key is written, the phone gets 200) | **caught** |
| The default `allow` of `list, mail` without `--allow` | `TestPairAsksBeforeItAddsTheKey/n`, `/y` (no allow list in the question, no `[hosts.pixel-9]` table) | **caught** |
| `pairNameInUse`, at the request and for `--name` | `TestPairRefusesANameInUse`, `TestPairLeavesWhatIsThereAlone` (the name of `[hosts.Phone]` gets 500 and stops the pairing, not 409) | **caught** |
| The names that pinned keys in authorized_keys use | `TestPairRefusesANameInUse` (`tablet` pairs beside a key pinned as `Tablet`, and `--name TABLET` shows a code) | **caught** |
| The allow list in the question | `TestPairAsksBeforeItAddsTheKey/n`, `/y` | **caught** |
| The refusal of this machine's addresses | `TestPairRefusesRequestsFromThisMachine/loopback`, `/own_lan_address` (200, the key is written) | **caught** |
| The refusal of the addresses of `[hosts]` | `TestPairRefusesRequestsFromThisMachine/host_in_config` (200 with `--accept-local`, the key is written) | **caught** |
| `check` in the reply | `TestPairAddsAPhoneKey` (the reply check code is empty) | **caught** |
| The check code in the question | `TestPairAsksBeforeItAddsTheKey/n`, `/y` | **caught** |
| The `tuios` on PATH in the forced command | `TestPairForcedCommandUsesTheTuiosOnPath/link_on_path` (the command names the file the link points to) | **caught** |
| The public address check before the bind | `TestPairListensOnlyWhereTheCodeSays` (`--listen 8.8.8.8:0` fails at the bind, with no word of `--listen-all`) | **caught** |
| Counting only pairing requests with a wrong MAC | `TestPairCountsOnlyPairingRequests` (three requests that are not pairing requests stop the listener) | **caught** |
| The folders above the authorized keys folder, up to the home folder | `TestPairChecksTheHomeFolder/anyone`, `/group` (a home with mode 0777 gets a code, 0770 gives no warning) | **caught** |
| The public address check for addresses tuios finds | none | **not caught**: the test machine has no public address to find |
| The DNS and tailnet lookups of `[hosts]` addresses | none | **not caught**: the tests give an IP in `addr`. A name needs a resolver or a tailnet the test controls |

### Workspace cycling (#612)

Run on 2026-10-10. The control is the head of PR 616 (`bfa8054f`), whose
cycle visits every workspace, built as `cmd/tuios` and run against
`TestWorkspaceCycleSkipsEmptyWorkspaces` and `TestWorkspaceCycleOpensNoPanes`.

| Control: what was cut | Tests that fail | Verdict |
| --- | --- | --- |
| The skip of workspaces with no panes in `cycleWorkspace` (the cycle visits every workspace) | `TestWorkspaceCycleSkipsEmptyWorkspaces` (alt+y from 1 shows 2, not 3), `TestWorkspaceCycleOpensNoPanes` (three presses leave 4 panes, not 1) | **caught** |
