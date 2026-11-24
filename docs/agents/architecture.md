# Architecture and gotchas

The MVU model, input modes, prefix keys, the window lifecycle, key dependencies, common gotchas and the files to know.

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

A plain `tuios` is a daemon session: the daemon (`internal/session`) owns the
PTYs and one emulator per pane, and the client keeps a second emulator per
window. `docs/ARCHITECTURE.md` draws both sides.

1. `OS.AddWindow()` (`internal/app/os_window.go`) sends a `NewWindow` intent
   to the daemon
2. The daemon spawns the pane's process through `ptyspawn` and pushes the new
   state
3. The client places the window when the push lands, and subscribes to the
   pane's stream from a snapshot of the daemon's emulator
4. Each chunk of the stream feeds the window's emulator and marks it dirty
5. `OS.DeleteWindow()` asks the daemon to close the window, and the client
   drops its copy when the push lands

`tuios --standalone` skips the daemon: the window spawns and owns its PTY.

## Key Dependencies

- **Bubble Tea v2** (`charm.land/bubbletea/v2`): TUI framework
- **Lipgloss v2** (`charm.land/lipgloss/v2`): Styling
- **Wish v2** (`charm.land/wish/v2`): SSH server
- **Ultraviolet** (`github.com/charmbracelet/ultraviolet`): Terminal emulation base
- **Cobra** (`github.com/spf13/cobra`): CLI commands
- **xpty** (`github.com/charmbracelet/x/xpty`): Cross-platform PTY
- **libghostty-vt** (`go.mitchellh.com/libghostty`, behind `-tags ghostty`): Alternative VT emulation backend; `scripts/install.sh ghostty` builds it (see `docs/ghostty-vt.md`)
- **sip** (`github.com/Gaurav-Gosain/sip`): WebGL terminal serving for `tuios-web`

> **Note:** Bubble Tea, Lipgloss, Wish, ssh, log and tone import from `charm.land/*`.
> Ultraviolet, colorprofile and the `x/*` packages stay under `github.com/charmbracelet/*`.
> `internal/fang` is a trimmed copy of `github.com/charmbracelet/fang`.

## Common gotchas

### Bubble Tea v2 Specifics

- Use `tea.KeyPressMsg` not `tea.KeyMsg` (v2 change)
- Mouse events are separate types: `tea.MouseClickMsg`, `tea.MouseMotionMsg`, etc.
- `View()` returns a `tea.View`, not a string
- Program options live in `app.ProgramOptions()` (`internal/app/program_options.go`):
  the frame rate, no signal handler, and the one `tea.WithFilter` slot (motion
  filter and size filter). Spread it into every `tea.NewProgram` (see [source-checks.md](source-checks.md))

### VT Emulator

- Theme colors only apply to ANSI colors 0-15
- RGB/truecolor passes through unchanged
- Background is transparent (nil) for TUI app compatibility. `appearance.background` and
  the per-surface background options paint default-background cells in the compositor
  (`internal/app/background.go`), never in the emulator, so the cells themselves stay nil

### Performance Considerations

- Style cache in `internal/app/stylecache.go`: check hit rates with `Ctrl+B, D, c`
- Object pools in `internal/pool/pool.go` reduce GC pressure
- Viewport culling skips off-screen windows
- Rendering is event-driven: idle sessions schedule no timer renders (see `docs/perf.md` for the measured baselines)

### Keybind Registry

- Leader key configurable but defaults to `ctrl+b`
- Every action is one entry of `actionTable` in `internal/config/actions.go`, with its description; `ActionDescriptions` and `LookupAction` are derived from it
- Key normalization handles `opt+` → `alt+` conversion on macOS

## Important Files to Know

| Purpose | File |
|---------|------|
| Main entry point | `cmd/tuios/main.go` |
| Central state | `internal/app/os.go` |
| Window lifecycle | `internal/app/os_window.go` |
| Bubble Tea options | `internal/app/program_options.go` |
| Daemon | `internal/session/daemon.go` |
| Rendering | `internal/app/render.go` |
| Input handling | `internal/input/handler.go` |
| Terminal window | `internal/terminal/window.go` |
| PTY spawn (every path) | `internal/ptyspawn/spawn.go` |
| VT emulation | `vt/emulator.go` |
| Configuration | `internal/config/userconfig.go` |
| Keybind registry | `internal/config/registry.go` |
| Tape scripting | `internal/tape/parser.go` |

## Colours in render code

The literal rule is in [AGENTS.md](../../AGENTS.md). The palette rules:

- The palette is built per colour depth (`overlay.Depth`: truecolor, 256, 16).
  At 16 colours every ground is `overlay.NoColor`, so a state that a ground
  would show has to go through `pal.Row` or `pal.Mark`.
- Lists follow one rule: `overlay.RowState{Cursor, Focused, Hover}`, the row's
  ground from `pal.Ground(st, base)`, and the finished row through
  `pal.Row(content, width, st, base)`. An unfocused list keeps its cursor.
- Blend with `overlay.MixColors` (OKLab); measure with `overlay.Readable` and
  `ReadableAt`, which measure the colour the depth will actually show.

## Additional Resources

- **Architecture**: `docs/ARCHITECTURE.md`. Technical diagrams and component details
- **Keybindings**: `docs/KEYBINDINGS.md`. Complete keyboard shortcut reference
- **Configuration**: `docs/CONFIGURATION.md`. TOML config options
- **Contributing**: `docs/CONTRIBUTING.md`. Contribution guidelines
- **Tape Scripting**: `docs/TAPE_SCRIPTING.md`. Automation script syntax
- **Plugins**: `docs/PLUGINS.md`. The plugin API, the contract that keeps it stable, and the trust rules
- **Web Terminal**: `docs/WEB.md`. Web terminal documentation (tuios-web binary)
- **VT Backends**: `docs/ghostty-vt.md`. The pure Go and libghostty-vt emulators, and how to build each
- **Rehydration**: `docs/REHYDRATION.md`. The snapshot-vs-stream contract for pane content on attach
- **Wire protocol**: `docs/protocol.md`. Binary frames, JSON verbs, and who owns session state
- **Performance**: `docs/perf.md`. Measured baselines and the "measured and not changed" ledger
- **Sip**: https://github.com/Gaurav-Gosain/sip. The library serving Bubble Tea apps as web apps (used by `cmd/tuios-web`)
