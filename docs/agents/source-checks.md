# Source checks

Some tests read the source and reject what the compiler accepts. These are
the ones a change trips most often. Each test's doc comment gives the reason.

| Rule | Test | Exception marker |
|------|------|------------------|
| No colour literal in render code | `internal/lint` `TestNoHardCodedColoursInRenderCode` | `//tuios:allow-color <reason>` |
| Daemon code starts a process with `childproc.Command`, never `exec.Command` or `os.StartProcess` | `internal/lint` `TestDaemonChildrenGoThroughChildproc` | `//tuios:allow-exec <reason>` |
| Every `git` starts through `childproc.Git`, which turns off repository hooks and filters | `internal/lint` `TestGitGoesThroughChildprocGit` | `//tuios:allow-exec <reason>` |
| Every build of `./cmd/tuios` reads `scripts/noinline-packages.txt` | `internal/lint` `TestEveryTuiosBuildReadsTheNoinlineList` | `tuios:allow-build <reason>` |
| A client quits through `OS.QuitCmd`, never a bare `tea.Quit` | `internal/lint` `TestEveryClientQuitGoesThroughQuitCmd` | `//tuios:allow-quit <reason>` |
| A pane's history is read through the window, never from `w.Terminal` directly | `internal/lint` `TestPaneHistoryIsReadThroughTheWindow` | `//tuios:allow-history <reason>` |
| Lessons name real actions, overlays, events and keys | `internal/lint` `TestLessonsMatchTheApp` | none |
| The Dockerfile's Go version equals go.mod's toolchain line | `internal/lint` `TestDockerfileGoMatchesToolchain` | none |
| A task card's lifecycle fields are written only in `internal/session/task_move.go` (`Session.moveCard`) | `internal/lint` `TestOnlyTaskMoveWritesACardsLifecycle` | none |
| A task event is published by `Daemon.taskEvent`, inside the state change that made it | `internal/lint` `TestTaskEventsArePublishedInsideTheirChange` | none |
| A file transfer job's state is written only by `transferJob.to` and `reopen` | `internal/lint` `TestOnlyToWritesATransferState` | none |
| Every `tea.NewProgram` takes `app.ProgramOptions()`, with no other `tea.With*` option than `WithOutput`, and a transport's `MakeOptions` before it | `internal/app` `TestEveryProgramTakesTheSharedOptions` | none |
| No emoji in a string or rune literal under `internal/` and `cmd/` | `internal/app` `TestNoEmojiInSourceStrings` | `emojiSourceAllowed` |
| A new overlay kind has a place in `overlayKindOrder` | `internal/app` `TestEveryOverlayKindHasAPlaceInTheStack` | none |
| An `ErrVerb*` code is in the `list-verbs` catalogue | `internal/session` `TestErrorCodeCatalogHasEveryErrVerbConstant` | none |
| Every verb has a doc in the `.verbs.json` file beside the Go file that registers it, with the same parameter and result names | `internal/session` `TestVerbDocsMatchTheRegistry` | none |
| A package with tests has `func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }` | `internal/testutil` `TestEveryTestPackageIsolatesTheXDGTree` | none |

Only the `internal/lint` rows run in the check gate. The others run with
their package's tests.

## go fix

Apply `go fix` one package at a time, and read every hunk before you keep it.
`go fix -diff ./...` in a module prints the patches. Keep the index form of a
loop that deletes from the slice it walks, and of a loop on a render path
that would copy a cell per step under `range`. Leave out the files an open
pull request changes, and a library folder unless you will move its pin.
