# AGENTS.md: Agent Guide for TUIOS

This file is for an agent **working on the TUIOS codebase**: how it is laid out,
how to build it, and the conventions to follow when changing it.

An agent **running inside a TUIOS pane** wants the other document. Run
`tuios --skill` for the core that drives a running session: addressing panes,
reading and writing them, running work and waiting on it, reporting agent
state, and talking to other agents safely. `tuios --skill TOPIC` prints one
topic, `tuios --skill all` prints every topic, and a name it does not know
lists the topics. The source is [skills/tuios/SKILL.md](skills/tuios/SKILL.md)
and the other files in `skills/tuios/`, embedded in the binary so the printed
copy always matches the build. `cmd/tuios` tests resolve every command the
skill shows against the command tree.

## Project Overview

TUIOS (Terminal UI Operating System) is a terminal-based window manager built in Go using the Charm stack (Bubble Tea v2, Lipgloss v2). It provides vim-like modal interface, workspace support, mouse interaction, and SSH server mode.

**Note:** The web terminal functionality is provided by the separate `tuios-web` binary for security isolation. See `cmd/tuios-web/` and [docs/WEB.md](docs/WEB.md) for details.

## Layout in short

- `cmd/tuios` is the CLI. `cmd/tuios-web`, `cmd/tuios-wasm` and `cmd/tuios-fuzz` are the other binaries.
- `internal/app` is the window manager (the `OS` model, rendering, update). `internal/input` routes keys and the mouse.
- `internal/session` is the daemon: sessions, PTYs, the wire protocol and the JSON verbs.
- `internal/terminal` and `internal/ptyspawn` own the windows and their PTYs. `internal/config` holds the config and the keybind registry.
- `vt/`, `tml/`, `harness/` and the other top-level folders with a `go.mod` are libraries ([docs/agents/libraries.md](docs/agents/libraries.md)).
- `e2e/tui` is the e2e suite, its own Go module.

The whole tree is in [docs/agents/code-map.md](docs/agents/code-map.md).

## Topic files

Read a topic file when your task reaches it. Each one holds the full rules for its subject.

| File | Read it when you |
|------|------------------|
| [docs/agents/architecture.md](docs/agents/architecture.md) | change the window manager, input, prefix keys or the window lifecycle, pick colours, or want the gotchas and the other docs |
| [docs/agents/code-map.md](docs/agents/code-map.md) | look for the package that owns something |
| [docs/agents/source-checks.md](docs/agents/source-checks.md) | trip a source check, or add a marker such as `//tuios:allow-exec` |
| [docs/agents/ci.md](docs/agents/ci.md) | want to know which CI jobs and e2e tests a change runs, a test fails on main, or the shard plan job fails |
| [docs/agents/e2e.md](docs/agents/e2e.md) | write or run an e2e test, use the sim backend, or keep a unit test |
| [docs/agents/fuzzing.md](docs/agents/fuzzing.md) | change `vt/`, add a conformance case, or turn a fuzz finding into a regression |
| [docs/agents/libraries.md](docs/agents/libraries.md) | change a library folder (`vt/`, `tml/`, `harness/` and the rest) or move a pin |
| [docs/agents/generated-files.md](docs/agents/generated-files.md) | edit a file that `go generate` packs, or add a JSON verb |
| [docs/agents/browser-build.md](docs/agents/browser-build.md) | change `cmd/tuios-wasm`, `internal/learn` or `internal/webshell`, or a lesson |
| [docs/agents/builds.md](docs/agents/builds.md) | add a build of `./cmd/tuios`, add a package to the noinline list, test the ghostty backend, or build with Nix or Docker |
| [docs/agents/platform.md](docs/agents/platform.md) | touch PTY, daemon or probe code that differs per platform |

## Essential Commands

### Build & Run

```bash
# Build from source
go build -o tuios ./cmd/tuios
go build -o tuios-web ./cmd/tuios-web

# Run directly
go run ./cmd/tuios
go run ./cmd/tuios-web

# Run with debug logging
go run ./cmd/tuios --debug
go run ./cmd/tuios-web --debug

# Run the unit tests of every module. ./... stops at each library's go.mod,
# so the libraries are named one by one. `just test` runs this line under the
# limits below.
go test ./... ./progstatus/... ./vt/... ./swatch/... ./shot/... ./tml/... \
  ./plot/... ./loom/... ./sketch/... ./acp/... ./harness/... ./nitpick/... ./progresume/...

# Leave the machine usable. The maintainer works on this box while the suite
# runs, and an uncapped build or test run takes every core. taskset confines the
# whole process tree to half the cores, and the affinity is inherited, so it
# also holds for the daemons and shells the e2e suite spawns. GOMAXPROCS and
# `go test -p` do not, because they only bind the Go runtime.
nice -n 10 taskset -c 0-7 go test ./... -timeout 20m

# Run one package
go test ./internal/config/...

# Run with race detection
go test -race ./...
```

### The justfile

The `justfile` at the root wraps these commands for local work. `just` lists
the recipes. They build into `.dev/`, run tests under `nice` and `taskset`,
and run throwaway daemons under a scratch directory. Only `install`,
`install-ghostty`, `deploy`, `deploy-hosts` and `restart` touch the live
daemon. `just deploy` runs `tuios hosts sync --dev --src <checkout> --local
--restart --yes`.

### The check gate

Run `just check --changed` before you mark a pull request ready. It runs
`scripts/check.sh` on the packages the branch changed against `origin/main`.
`just check` runs it on every module, which is the form for main.

| Check | What it runs | Fails the gate |
|-------|--------------|----------------|
| `fmt` | `gofmt -l` and `goimports -l` | yes |
| `vet` | `go vet`, also with `-tags ghostty` when libghostty-vt is built for the pinned release, in `$GHOSTTY_VT_CACHE` or `.ghostty-vt` | yes |
| `lint` | `golangci-lint` with `.golangci.yml`, at the CI version | yes |
| `tuioslint` | the `internal/lint` tests | yes |
| `vuln` | `govulncheck`, for each module the branch touched | yes |
| `fix` | the `go fix` modernizers, one line per finding | no, report |
| `gopls` | `gopls check` on the changed Go files | yes |
| `tidy` | `go mod tidy -diff` per module, `scripts/library-pins.sh check` | yes |
| `cross` | `go build` for linux, darwin, windows and freebsd | yes |
| `deadcode` | functions no binary or test reaches, outside `pkg/` | no, report |

`--only vet,lint` runs a subset. Each check writes its full output to
`.dev/check/NAME.log`. The tools run through `go run` at pinned versions, so
the gate needs nothing installed beyond Go. `gopls` fails only on the lines
the branch added. `lint` covers the whole of each changed package. Fix a
finding at the root. A `//nolint` needs its reason on the same line.

The gate runs no unit tests beyond `internal/lint`. Run the tests of the
packages you changed as well, and see [docs/agents/source-checks.md](docs/agents/source-checks.md).

### Land a pull request

A pull request lands on main by fast-forward (`ff-merge`), after the whole
suite passed on its head:

1. Rebase the branch on main, so main can fast-forward to the head.
2. Add the `full-suite` label. The label runs every CI job and the whole e2e
   suite on the head, on the cells path and the real binary.
3. Wait until `CI result` and `e2e-tui result` pass on that run.
   `scripts/full-suite-passed.sh OWNER/REPO SHA` exits 0 when both did.
4. Fast-forward main to that exact SHA.

A push after the label runs the whole suite again. The push to main then
skips the real cells shards it already ran. The string frame shards and the
sim shards run every night on main, because each costs as much runner time
as the cells path. A red sim shard does not fail `e2e-tui result`. A red
string shard on main is a bug on main: fix it at once.

### Binary size

The binary-size job builds tuios as the release does (`CGO_ENABLED=0`,
`-trimpath`, `-ldflags "-s -w"`) for linux/amd64, darwin/arm64 and
windows/amd64. It fails when one is over the 50 MB ceiling in
`scripts/binary-size.sh`, and it comments the size change against main on the pull request. `just size` runs
the same check. State the size cost of a change in the pull request body.
The room under the ceiling is not a budget to spend: each pull request still
reports its size delta and trims what it can.
Raise the ceiling only on purpose, in its own commit, with `docs/perf.md`
("Binary size budget").

The noinline list, which every build of `./cmd/tuios` reads, is in [docs/agents/builds.md](docs/agents/builds.md).

## Hard rules

The source checks in [docs/agents/source-checks.md](docs/agents/source-checks.md) enforce most of these. The ones a change trips most often:

- Daemon code starts a process with `childproc.Command`, and every `git` with `childproc.Git`. Never `exec.Command` or `os.StartProcess`.
- A client quits through `OS.QuitCmd`, never a bare `tea.Quit`.
- Every `tea.NewProgram` takes `app.ProgramOptions()`.
- A package with tests has a `TestMain` that calls `testutil.RunIsolated`.
- No emoji in a string or rune literal under `internal/` and `cmd/`.

## Coding Conventions

### Go Style

- Follow standard Go conventions ([Effective Go](https://go.dev/doc/effective_go))
- Run `go fmt` before committing, and `just check --changed` before a pull request is marked ready
- Package comments on all packages (see existing `internal/*/` packages)
- Meaningful variable names (avoid single letters except loop indices)

### Colours in Render Code

- Take every chrome colour from `theme.UI()` (dialogs) or `theme.GroundUI()`
  (the rail and the dock), both an `overlay.Palette`, or from a named token in
  `internal/theme`. Never write a literal: `internal/lint` rejects
  `lipgloss.Color("#...")`, constant `color.RGBA{...}`, CharmTone colours (`tone.*`) and
  constant `ansi.BasicColor`/`IndexedColor` in `internal/app`,
  `internal/overlay` and `internal/composer`. A deliberate exception carries `//tuios:allow-color <reason>`.

### Error Handling

- Wrap errors with context: `errfmt.Errorf("failed to X: %w", err)`. It is
  `fmt.Errorf` kept out of line, which saves about 115 bytes of binary per
  call. `internal/lint` rejects `fmt.Errorf` in the binary's packages, and
  holds the `errfmt.Errorf` strings to staticcheck's ST1005, which only sees
  `fmt.Errorf`
- Encode and decode JSON with `jsonx.Marshal` and `jsonx.Unmarshal`, for the
  same reason. `internal/lint` rejects `json.Marshal` and `json.Unmarshal`
- Log warnings for non-fatal issues: `log.Printf("Warning: ...")`
- Return early on errors

### Documentation

- Package-level doc comments required
- Exported types/functions need doc comments
- Use godoc-style comments

### Testing

Follow the Testing rules under Testing Approach. For the unit tests that stay:

- Test file naming: `*_test.go`
- Benchmarks with `Benchmark*` prefix, and only for a perf budget
- Use `t.Run()` for subtests

## Testing Approach

### Testing rules

- Never write unit tests after you write code.
- Highly prefer E2E tests as the sole testing mechanism. Use them to verify complex features work. At the end of E2E tests, produce a verifiable and repeatable artifact.
- If you must test a system in isolation, first write down all the ways it could fail, then write the code.

A unit test is kept only when it catches a real bug the E2E suite misses. The
kinds that stay are VT conformance, fuzz, wire compatibility, security
boundaries, deterministic race regressions and perf budgets. Do not add one
outside those kinds.

## Commit Message Format

Use conventional commits:
- `feat:` New feature
- `fix:` Bug fix
- `docs:` Documentation changes
- `refactor:` Code refactoring
- `test:` Adding or updating tests
- `chore:` Maintenance tasks

Examples:
```
feat: add configurable dockbar position
fix: panic when closing last window on Linux
docs: update keybindings reference
```

## Release Process

Releases are automated via GitHub Actions with GoReleaser:
- Tag format: `v*.*.*` (e.g., `v0.12.1`). Tagging is the maintainer's call
- Builds for Linux, macOS, Windows, FreeBSD and OpenBSD
- GoReleaser publishes to AUR and Homebrew. `docker-publish.yml` pushes the
  image, and `update-nix.yml` moves `tuios.nix` to the release
