# AGENTS.md: Agent Guide for TUIOS

This file is for an agent **working on the TUIOS codebase**: how it is laid out,
how to build it, and the conventions to follow when changing it.

An agent **running inside a TUIOS pane** wants the other document. Run
`tuios --skill` for the recipes that drive a running session: addressing panes,
reading and writing them, opening panes to run work in, waiting on conditions,
and reporting agent state. The source is [skills/tuios/SKILL.md](skills/tuios/SKILL.md),
embedded in the binary so the printed copy always matches the build.

## Project Overview

TUIOS (Terminal UI Operating System) is a terminal-based window manager built in Go using the Charm stack (Bubble Tea v2, Lipgloss v2). It provides vim-like modal interface, workspace support, mouse interaction, and SSH server mode.

**Note:** The web terminal functionality is provided by the separate `tuios-web` binary for security isolation. See `cmd/tuios-web/` and [docs/WEB.md](docs/WEB.md) for details.

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

# Run tests
go test ./...

# Leave the machine usable. The maintainer works on this box while the suite
# runs, and an uncapped build or test run takes every core. taskset confines the
# whole process tree to half the cores, and the affinity is inherited, so it
# also holds for the daemons and shells the e2e suite spawns. GOMAXPROCS and
# `go test -p` do not, because they only bind the Go runtime.
nice -n 10 taskset -c 0-7 go test ./... -timeout 20m

# Run specific package tests
go test ./internal/config/...
go test ./internal/tape/...

# Run with race detection
go test -race ./...

# Run the tests of the ghostty backend. `go build -tags ghostty ./...` compiles
# it and runs none of its tests, so a change that passes the plain suite can
# still be wrong on the backend scripts/install.sh builds by default. This is
# the command the ghostty-vt workflow runs.
PKG_CONFIG_PATH="$PWD/.ghostty-vt/native/pkgconfig" \
  go test -tags ghostty -count=1 -short \
    ./internal/vt/ ./internal/session/ ./internal/terminal/
```

### Browser build (Learn tuios)

`cmd/tuios-wasm` is tuios compiled to WebAssembly for the guided tour at
tuios.gaurav.zip/learn: the real app in Learn mode, with a fake shell
(`internal/webshell`) in every pane and an event stream for lessons
(`internal/learn`). Browser-only code is behind `js` build tags or in those
three directories, so the native build is unchanged. The page API and event
contract are in [cmd/tuios-wasm/README.md](cmd/tuios-wasm/README.md).

```bash
cmd/tuios-wasm/build.sh out/                  # the files the docs site needs
node cmd/tuios-wasm/serve.mjs out/ 8765       # try it at http://127.0.0.1:8765
go test ./internal/webshell/ ./internal/learn/  # native tests of the Go side
```

A change to `internal/app` or `internal/input` can break the js build without
breaking any native one. The learn-web workflow builds it on every pull
request.

### Development with Nix

```bash
nix develop    # Enter development shell
nix build      # Build package
nix run        # Run directly
```

### Docker

```bash
docker build -t tuios .
docker run -it --rm tuios
```

## Code Organization

```
tuios/
├── cmd/tuios/              # CLI entry point (main.go with cobra commands)
├── cmd/tuios-web/          # Web terminal server binary (separate for security)
├── cmd/tuios-wasm/         # Browser build for the Learn tuios tour (js/wasm)
├── internal/
│   ├── app/                # Core window manager, OS model, rendering
│   │   ├── os.go           # Central state (OS struct), window lifecycle
│   │   ├── render.go       # View generation, layer composition
│   │   ├── update.go       # Bubble Tea Update() handler
│   │   ├── stylecache.go   # LRU style caching (40-60% allocation reduction)
│   │   ├── workspace.go    # Multi-workspace support (1-9)
│   │   └── animations.go   # Visual transitions
│   ├── config/             # Configuration and keybindings
│   │   ├── userconfig.go   # TOML config loading, defaults
│   │   ├── registry.go     # Keybind action lookup
│   │   └── validation.go   # Config validation
│   ├── input/              # Input handling and modal routing
│   │   ├── handler.go      # Main input coordinator
│   │   ├── keyboard.go     # Key event dispatch
│   │   ├── mouse.go        # Mouse interactions
│   │   ├── actions.go      # Action handlers (the prefix handlers are in prefix_actions.go)
│   │   └── copymode_*.go   # Vim-style copy mode (50+ motions)
│   ├── terminal/           # Terminal window management
│   │   ├── window.go       # Window struct, PTY lifecycle
│   │   └── window_unix.go / window_windows.go  # Platform-specific window and PTY glue
│   ├── ptyspawn/           # The one path every PTY-backed process is spawned through (spawn_unix.go, spawn_windows.go)
│   ├── vt/                 # Terminal emulation: pure Go, plus libghostty-vt behind -tags ghostty
│   │   ├── emulator.go     # Parser state machine
│   │   ├── screen.go       # Screen buffer management
│   │   └── scrollback.go   # 10,000 line history
│   ├── session/            # The daemon: sessions, PTYs (pty_unix.go, pty_windows.go), wire protocol, JSON verbs
│   ├── federation/         # The link layer between this daemon and the daemons on other machines
│   ├── worktree/           # Git worktrees: detect, create, and remove without losing uncommitted work
│   ├── gitstate/           # Branch and upstream drift for the sidebar
│   ├── capture/            # Turns a screenshot request and config into what shot renders
│   ├── shot/               # Renders a cell grid to SVG, PNG, ANSI, HTML or text
│   ├── release/            # Finds published releases and verifies a downloaded binary (tuios update)
│   ├── netutil/            # Small network helpers the servers share
│   ├── harness/            # Agent harness manifests and detection
│   ├── learn/              # Learn tuios: tour model, event contract, page commands
│   ├── webshell/           # In-memory pty and fake shell for the browser build
│   ├── hooks/              # Shell hooks on window/session/agent events
│   ├── scrollback/         # OSC 133 scrollback browser
│   ├── overlay/            # Panel and dialog primitives for chrome
│   ├── sessiontree/        # Sidebar session tree model
│   ├── tape/               # Tape scripting automation
│   │   ├── lexer.go        # Tokenizer
│   │   ├── parser.go       # AST generation
│   │   ├── executor.go     # Command execution
│   │   └── player.go       # Playback engine
│   ├── server/             # SSH server (Wish v2)
│   ├── theme/              # Color theming
│   ├── layout/             # Window tiling algorithms
│   ├── pool/               # Memory pooling
│   └── ui/                 # Animation system
│                           # (plus sound, transcript, guestenv, perf, fuzz, testutil)
├── pkg/                    # Embeddable facade (tuios), applist, fuzzy
├── docs/                   # Documentation
│   ├── ARCHITECTURE.md     # Technical architecture diagrams
│   ├── KEYBINDINGS.md      # Complete keybinding reference
│   ├── CONFIGURATION.md    # Config options
│   └── CLI_REFERENCE.md    # CLI flags and commands
├── examples/               # Tape script examples, and dock components under examples/dock/
├── skills/                 # The tuios skill (skills/tuios/SKILL.md), embedded and printed by tuios --skill
├── integrations/           # Harness integrations, such as the claude-code agent-state shim
├── e2e/                    # End-to-end tests; e2e/tui is its own Go module
├── clienttests/            # Playwright tests for the web client
└── nix/                    # Nix packaging
```

## Architecture Patterns

### Bubble Tea MVU Pattern

TUIOS follows Model-View-Update:
- **Model**: `app.OS` struct in `internal/app/os.go`
- **View**: `OS.View()` in `internal/app/render.go`
- **Update**: `OS.Update()` in `internal/app/update.go`

### Modal Input System

Two primary modes:
1. **WindowManagementMode**: Window manipulation, navigation
2. **TerminalMode**: Input forwarded to focused terminal PTY

Input routing: `internal/input/handler.go` → mode-specific handlers

### Prefix Key System (tmux-style)

Leader key (`Ctrl+B` by default) activates prefix mode with sub-menus:
- `Ctrl+B` then `w` → Workspace prefix
- `Ctrl+B` then `m` → Minimize prefix
- `Ctrl+B` then `t` → Window prefix
- `Ctrl+B` then `L` → Layout prefix
- `Ctrl+B` then `D` → Debug prefix
- `Ctrl+B` then `T` → Tape manager prefix

Tiling itself toggles on `Ctrl+B` `Space` (or bare `t` in window-management mode).

### Window Lifecycle

1. `OS.AddWindow()` creates window with PTY
2. PTY spawns shell process with I/O polling goroutines
3. VT emulator parses ANSI output
4. Screen buffer updates trigger render
5. `OS.DeleteWindow()` cleans up PTY and removes window

## Key Dependencies

- **Bubble Tea v2** (`charm.land/bubbletea/v2`): TUI framework
- **Lipgloss v2** (`charm.land/lipgloss/v2`): Styling
- **Wish v2** (`charm.land/wish/v2`): SSH server
- **Ultraviolet** (`github.com/charmbracelet/ultraviolet`): Terminal emulation base
- **Cobra** (`github.com/spf13/cobra`): CLI commands
- **xpty** (`github.com/charmbracelet/x/xpty`): Cross-platform PTY
- **libghostty-vt** (`go.mitchellh.com/libghostty`, behind `-tags ghostty`): Alternative VT emulation backend; `scripts/install.sh` builds it (see `docs/ghostty-vt.md`)
- **sip** (`github.com/Gaurav-Gosain/sip`): WebGL terminal serving for `tuios-web`

> **Note:** As of December 2025, the Charm stack packages have migrated from `github.com/charmbracelet/*` to `charm.land/*` module paths.

## Coding Conventions

### Go Style

- Follow standard Go conventions ([Effective Go](https://go.dev/doc/effective_go))
- Run `go fmt` before committing
- Package comments on all packages (see existing `internal/*/` packages)
- Meaningful variable names (avoid single letters except loop indices)

### Error Handling

- Wrap errors with context: `fmt.Errorf("failed to X: %w", err)`
- Log warnings for non-fatal issues: `log.Printf("Warning: ...")`
- Return early on errors

### Documentation

- Package-level doc comments required
- Exported types/functions need doc comments
- Use godoc-style comments

### Testing

- Table-driven tests preferred (see `internal/tape/lexer_test.go`)
- Test file naming: `*_test.go`
- Benchmarks with `Benchmark*` prefix
- Use `t.Run()` for subtests

## Testing Approach

### Unit Tests

```bash
# All tests
go test ./...

# Specific package
go test ./internal/tape/...

# With verbose output
go test -v ./internal/config/...

# Run benchmarks
go test -bench=. ./internal/app/...
```

### Terminal Emulator Conformance

`internal/vt/` carries a table-driven conformance corpus: an input byte
sequence, and the screen it should produce, on a screen small enough that a
diff is readable. It runs with the ordinary suite.

```bash
# The corpus, the unicode sweeps and the generated-input sweep
go test ./internal/vt/

# Skips the exhaustive write-boundary sweep, which is the slow part
go test -short ./internal/vt/
```

Three things in it are worth knowing about before adding a case:

- A case that leaves `unhandled` false asserts the emulator recognised every
  sequence in its input. A sequence the emulator ignores leaves a screen
  indistinguishable from one it handled by doing nothing, which is how NEL and
  DECALN stayed missing under green tests.
- A case marked `knownBug` is expected to fail, and the test complains if it
  starts passing, so a fixed bug cannot sit on the list pretending to still be
  one.
- The unicode sweeps are driven by pinned UCD data files under
  `internal/vt/testdata/unicode/`, with provenance and licence in the README
  there.

### Terminal Emulator Fuzzing

`internal/fuzz/vtgen` generates terminal input by grammar rather than by byte,
so the parser reaches the code past its ground state. A failing run reduces by
delta debugging to a script of named sequences a person can read.

```bash
# Deterministic seeds, part of the ordinary suite
go test ./internal/vt/ -run TestVTGen

# Coverage-guided, for a real campaign
go test ./internal/vt/ -run XXX -fuzz FuzzEmulatorScript -fuzztime 10m
```

### Differential Testing Against tmux

An independent implementation catches what a hand-written expectation cannot,
because a test can be wrong in the same way the code is. Needs the `tmux` binary
and spawns processes, so it is behind a build tag.

```bash
go test -tags differential ./internal/vt/ -run TestDifferential -v
```

Cases where tmux is the one diverging are listed in `tmuxDiffers` with the
reasoning. The test fails if one of them starts agreeing, so the list cannot
rot.

### Manual Testing Checklist

When testing UI/UX changes:
- [ ] Create/close multiple windows
- [ ] Switch between workspaces (Alt+1-9)
- [ ] Test tiling mode (t key)
- [ ] Test copy mode (Ctrl+B, [)
- [ ] Verify keybindings work
- [ ] Check terminal output rendering
- [ ] Test mouse interactions (drag, resize)

### Tape Script Testing

```bash
# Validate tape syntax
go run ./cmd/tuios tape validate examples/demo.tape

# Run tape with visible TUI
go run ./cmd/tuios tape play examples/demo.tape
```

## Common Gotchas

### Bubble Tea v2 Specifics

- Use `tea.KeyPressMsg` not `tea.KeyMsg` (v2 change)
- Mouse events are separate types: `tea.MouseClickMsg`, `tea.MouseMotionMsg`, etc.
- `tea.WithFilter()` for event filtering (used for mouse motion filtering)

### VT Emulator

- Theme colors only apply to ANSI colors 0-15
- RGB/truecolor passes through unchanged
- Background is transparent (nil) for TUI app compatibility

### Performance Considerations

- Style cache in `internal/app/stylecache.go`: check hit rates with `Ctrl+B, D, c`
- Object pools in `internal/pool/pool.go` reduce GC pressure
- Viewport culling skips off-screen windows
- Rendering is event-driven: idle sessions schedule no timer renders (see `docs/perf.md` for the measured baselines)

### Platform Differences

- PTY handling differs: `internal/terminal/window_unix.go` vs `window_windows.go`,
  `internal/session/pty_unix.go` vs `pty_windows.go`, and
  `internal/ptyspawn/spawn_unix.go` vs `spawn_windows.go`
- Workspace keybinds differ: `opt+N` on macOS, `alt+N` on Linux
- See `internal/config/userconfig.go` → `getDefaultWorkspaceKeybinds()`

### Keybind Registry

- Leader key configurable but defaults to `ctrl+b`
- Check action descriptions in `internal/config/registry.go`
- Key normalization handles `opt+` → `alt+` conversion on macOS

## Important Files to Know

| Purpose | File |
|---------|------|
| Main entry point | `cmd/tuios/main.go` |
| Central state | `internal/app/os.go` |
| Rendering | `internal/app/render.go` |
| Input handling | `internal/input/handler.go` |
| Terminal window | `internal/terminal/window.go` |
| PTY spawn (every path) | `internal/ptyspawn/spawn.go` |
| VT emulation | `internal/vt/emulator.go` |
| Configuration | `internal/config/userconfig.go` |
| Keybind registry | `internal/config/registry.go` |
| Tape scripting | `internal/tape/parser.go` |

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
- Tag format: `v*.*.*` (e.g., `v0.3.4`)
- Builds for Linux, macOS, Windows, FreeBSD
- Publishes to AUR, Homebrew, Docker

## Additional Resources

- **Architecture**: `docs/ARCHITECTURE.md`. Technical diagrams and component details
- **Keybindings**: `docs/KEYBINDINGS.md`. Complete keyboard shortcut reference
- **Configuration**: `docs/CONFIGURATION.md`. TOML config options
- **Contributing**: `docs/CONTRIBUTING.md`. Contribution guidelines
- **Tape Scripting**: `docs/TAPE_SCRIPTING.md`. Automation script syntax
- **Web Terminal**: `docs/WEB.md`. Web terminal documentation (tuios-web binary)
- **VT Backends**: `docs/ghostty-vt.md`. The pure Go and libghostty-vt emulators, and how to build each
- **Rehydration**: `docs/REHYDRATION.md`. The snapshot-vs-stream contract for pane content on attach
- **Performance**: `docs/perf.md`. Measured baselines and the "measured and not changed" ledger
- **Sip**: https://github.com/Gaurav-Gosain/sip. The library serving Bubble Tea apps as web apps (used by `cmd/tuios-web`)
