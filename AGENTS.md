# AGENTS.md - Development Guide for AI Assistants

This document provides comprehensive information for AI agents working on the TUIOS codebase. TUIOS is a terminal-based window manager built with Go, using the Charm stack (Bubble Tea v2 and Lipgloss v2).

---

## Essential Commands

### Building and Running

```bash
# Build from source
go build -o tuios ./cmd/tuios

# Build and run
go build -o tuios ./cmd/tuios && ./tuios

# Run directly (not recommended for development)
go run ./cmd/tuios

# Build with goreleaser locally
goreleaser build --snapshot --clean

# Run with specific flags
./tuios --debug                    # Enable debug logging
./tuios --ascii-only              # Disable Nerd Font icons
./tuios --theme dracula           # Use specific theme
./tuios --cpuprofile cpu.prof     # Enable CPU profiling
```

### Testing

```bash
# Run all tests
go test ./...

# Run tests for specific package
go test ./internal/config/...
go test ./internal/tape/...
go test ./internal/app/...

# Run tests with verbose output
go test -v ./...

# Run tests with coverage
go test -cover ./...
```

**Note:** Tests are NOT run in CI (commented out in `.goreleaser.yml`). The project has limited test coverage:
- `internal/tape/lexer_test.go`
- `internal/tape/parser_test.go`
- `internal/app/stylecache_test.go`

### Linting and Formatting

```bash
# Format code (ALWAYS run before committing)
go fmt ./...

# Run linter (via Nix dev shell)
golangci-lint run

# Check for security issues (via Nix dev shell)
gosec ./...

# Update dependencies
go get -u ./...
go mod tidy
```

**Important:** There is NO linting in the GitHub Actions CI. Code quality checks are manual.

### Development with Nix

```bash
# Enter Nix development shell (includes go, gopls, golangci-lint, etc.)
nix develop

# Run directly with Nix
nix run github:Gaurav-Gosain/tuios#tuios

# Format all files with treefmt
nix fmt
```

### Git Workflow

```bash
# Create feature branch
git checkout -b feat/your-feature-name

# Commit with conventional commit format
git commit -m "feat: add your feature description"
git commit -m "fix: fix specific bug"
git commit -m "docs: update documentation"
git commit -m "refactor: restructure component"

# Push and create PR
git push origin feat/your-feature-name
```

---

## Project Structure

```
tuios/
├── cmd/tuios/           # Main entry point (main.go with cobra CLI)
├── internal/            # Internal packages (not importable externally)
│   ├── app/             # Core window manager (OS, render, animations, workspace)
│   ├── config/          # Configuration system (TOML, keybindings, validation)
│   ├── input/           # Input handling (keyboard, mouse, modal routing)
│   ├── layout/          # Window layout and tiling algorithms
│   ├── pool/            # Memory pooling (strings, layers, styles, buffers)
│   ├── server/          # SSH server implementation (Wish v2)
│   ├── system/          # System utilities (CPU/RAM monitoring)
│   ├── tape/            # Tape script system (lexer, parser, executor, player)
│   ├── terminal/        # Terminal window management (PTY, VT integration)
│   ├── theme/           # Theming and color schemes
│   ├── ui/              # UI components (animations, visual effects)
│   └── vt/              # Vendored VT terminal emulator (ANSI/VT100 parser)
├── docs/                # Extensive documentation
│   ├── ARCHITECTURE.md       # Technical architecture and diagrams
│   ├── CLI_REFERENCE.md      # Command-line options
│   ├── CONFIGURATION.md      # User configuration guide
│   ├── CONTRIBUTING.md       # Contributor guide
│   ├── DEPS.md               # Dependency information
│   ├── KEYBINDINGS.md        # Complete keyboard shortcuts
│   ├── RELEASE_AUTOMATION.md # Release process
│   └── STYLE_CACHE.md        # Performance optimization details
├── assets/              # Demo GIFs and tape scripts
├── examples/            # Example tape scripts (automation)
├── nix/                 # Nix packaging and dev environment
├── .github/             # GitHub Actions, issue templates, PR templates
├── go.mod               # Go module definition (Go 1.24+)
├── .goreleaser.yml      # Release configuration (goreleaser)
├── flake.nix            # Nix flake for reproducible builds
└── Dockerfile           # Docker image definition
```

### Key Files

- **`cmd/tuios/main.go`**: CLI entry point using Cobra, handles all subcommands
- **`internal/app/os.go`**: Central OS struct managing all application state (~1000+ lines)
- **`internal/app/render.go`**: Rendering pipeline with viewport culling and caching
- **`internal/app/update.go`**: Bubble Tea Update function (message handling)
- **`internal/input/handler.go`**: Input router for modal interactions
- **`internal/vt/vt.go`**: Vendored VT terminal emulator (DO NOT modify without understanding vendoring)
- **`internal/config/constants.go`**: All constants (FPS, dimensions, timeouts, etc.)
- **`internal/config/registry.go`**: Keybinding registry and default mappings

---

## Architecture Overview

### Design Pattern: Model-View-Update (MVU)

TUIOS follows the MVU pattern via Bubble Tea v2:
1. **Model**: `app.OS` struct holds all state
2. **Update**: Message handlers in `internal/app/update.go` and `internal/input/`
3. **View**: Rendering logic in `internal/app/render.go`

### Modal System

TUIOS has a **two-mode** system:

1. **Window Management Mode** (`app.WindowManagementMode`)
   - Navigate, create, close, resize, move windows
   - Press `n` to create window, `i` or `Enter` to enter terminal mode

2. **Terminal Mode** (`app.TerminalMode`)
   - Input passed directly to focused terminal
   - Press `Ctrl+B d` or `Esc` to return to Window Management Mode

**Copy Mode** is a sub-mode within terminals (stored in `terminal.Window.CopyMode`):
- Vim-style navigation through scrollback buffer (10,000 lines)
- Accessed via `Ctrl+B [` from any mode
- Has its own sub-states: Normal, Visual, Search, CharSearch

### Component Responsibilities

| Package | Purpose |
|---------|---------|
| `app` | Window manager, workspace orchestration, rendering coordination |
| `input` | Keyboard/mouse event routing based on mode |
| `terminal` | PTY management, window lifecycle, VT integration |
| `vt` | ANSI/VT100 terminal emulation with scrollback |
| `layout` | Tiling algorithms, window positioning |
| `config` | TOML configuration, keybinding registry, validation |
| `tape` | Automation scripting (lexer, parser, executor, recorder) |
| `server` | SSH server for remote access (per-connection isolation) |
| `ui` | Animations (minimize, restore, snap) |
| `theme` | Color schemes and styling |
| `pool` | Object pooling to reduce allocations |
| `system` | CPU/RAM monitoring utilities |

### Data Flow

```
User Input → Bubble Tea → input.HandleInput() → Mode Router
                                                    ↓
                          ┌─────────────────────────┴──────────────────────┐
                          ↓                                                 ↓
            WindowManagementMode                              TerminalMode
                   ↓                                                 ↓
          app.OS state changes                          terminal.Window.Pty
                   ↓                                                 ↓
            app.Render()                                    Shell Process
                   ↓                                                 ↓
            Terminal Display                           VT Emulator Parse
                                                                     ↓
                                                            app.Render()
```

---

## Code Conventions

### Go Style

- **Follow standard Go conventions**: [Effective Go](https://go.dev/doc/effective_go)
- **Package comments required**: Every package MUST have a doc comment (recently enforced)
  ```go
  // Package config provides configuration constants, keybinding management, and user settings.
  package config
  ```
- **Exported identifiers**: Document all exported types, functions, constants
- **Run `go fmt` before committing** (non-negotiable)
- **Use descriptive names**: No single-letter variables except in very short scopes (loop indices)

### Project-Specific Patterns

#### 1. **Constants Over Magic Numbers**

All magic numbers are defined in `internal/config/constants.go`:

```go
// Good
if w.Width < config.MinWindowWidth {
    w.Width = config.MinWindowWidth
}

// Bad
if w.Width < 10 {
    w.Width = 10
}
```

#### 2. **Object Pooling**

Use pools from `internal/pool/` to reduce allocations:

```go
// String building
sb := pool.GetStringBuilder()
defer pool.PutStringBuilder(sb)
sb.WriteString("...")

// Layers
layers := pool.GetLayerSlice()
defer pool.PutLayerSlice(layers)

// Byte slices
buf := pool.GetByteSlice()
defer pool.PutByteSlice(buf)
```

#### 3. **Style Caching**

The style cache (`internal/app/stylecache.go`) reduces lipgloss allocations by 40-60%. Always use:

```go
style := getOrCreateCellStyle(cell, focused)
```

Never create raw `lipgloss.NewStyle()` for cell rendering.

#### 4. **Mutex Discipline**

- `terminal.Window.ioMu`: Protects PTY I/O operations
- `app.OS.terminalMu`: Protects terminal list modifications

**Always lock when**:
- Reading/writing to PTY
- Modifying `os.Windows` slice
- Accessing shared state from goroutines

#### 5. **Error Handling**

```go
// Good: Check and handle errors
if err := w.Pty.Write(data); err != nil {
    os.AddNotification("Write failed: "+err.Error(), NotificationError)
    return
}

// Bad: Ignoring errors
w.Pty.Write(data)
```

#### 6. **Naming Conventions**

- **Structs**: PascalCase (e.g., `Window`, `OS`, `CopyMode`)
- **Functions**: camelCase for private, PascalCase for exported
- **Constants**: PascalCase (e.g., `DefaultWindowWidth`, `NormalFPS`)
- **Enums**: Type + Value (e.g., `Mode`, `WindowManagementMode`, `TerminalMode`)

#### 7. **Cobra Commands**

All CLI commands use Cobra (`github.com/spf13/cobra`). Structure:

```go
var someCmd = &cobra.Command{
    Use:     "subcommand",
    Short:   "Brief description",
    Long:    "Detailed description...",
    Example: "  tuios subcommand --flag value",
    RunE: func(cmd *cobra.Command, args []string) error {
        // Implementation
        return nil
    },
}
```

Add flags in `init()`:
```go
func init() {
    rootCmd.AddCommand(someCmd)
    someCmd.Flags().StringVarP(&varName, "flag", "f", "default", "description")
}
```

---

## Performance Considerations

### Optimization Strategies

1. **Style Cache**: Reduces lipgloss allocations (40-60% improvement)
2. **Viewport Culling**: Skip rendering off-screen windows
3. **Adaptive Refresh Rates**:
   - Focused window: 60 FPS
   - Background windows: 30 FPS (throttled)
   - During drag/resize: 30 FPS
4. **Memory Pooling**: Reuse builders, layers, buffers, styles
5. **Frame Skipping**: Don't render if terminal hasn't changed (sequence-based detection)

### Constants (see `internal/config/constants.go`)

```go
NormalFPS = 60                    // Regular refresh rate
InteractionFPS = 30               // During drag/resize
BackgroundWindowUpdateCycle = 3   // Skip 2 out of 3 frames for background windows
```

### When Modifying Rendering

- **Always check `window.Dirty` before rendering**
- **Update `window.LastTerminalSeq`** to track VT changes
- **Use `pool.GetStringBuilder()`** for string concatenation
- **Test with multiple windows** to catch performance regressions
- **Profile with `--cpuprofile`** flag if needed

---

## Testing Patterns

### Limited Test Coverage

The project has **minimal automated tests**. Focus areas:
- Tape script parsing and lexing
- Style cache functionality

### Manual Testing Checklist

When making changes, manually verify:

- [ ] Create/close multiple windows (at least 5)
- [ ] Switch between all 9 workspaces
- [ ] Toggle tiling mode (`t` key)
- [ ] Enter copy mode (`Ctrl+B [`) and navigate
- [ ] Test mouse interactions (click, drag, resize)
- [ ] Run a TUI app (vim, htop) in a window
- [ ] Minimize and restore windows
- [ ] Rename windows (`Ctrl+B t r`)
- [ ] Check help overlay (`Ctrl+B ?`)
- [ ] Verify no crashes on rapid window creation/deletion

### SSH Testing

```bash
# Terminal 1: Start SSH server
./tuios ssh --port 2222

# Terminal 2: Connect
ssh -p 2222 localhost

# Verify per-connection isolation (multiple clients)
```

### Tape Script Testing

```bash
# Validate syntax
./tuios tape validate examples/demo.tape

# Run headless (no TUI)
./tuios tape run examples/demo.tape

# Run with TUI (watch it happen)
./tuios tape play examples/demo.tape
```

---

## Common Gotchas

### 1. **VT Package is Vendored**

`internal/vt/` is a vendored terminal emulator. **DO NOT modify** without understanding the implications. Changes here affect all terminal rendering.

### 2. **Modal Input Routing**

Keybindings behave differently based on mode. When adding keybindings:
- Check `os.Mode` in `input.HandleKeyPress()`
- Global keybindings (e.g., `Ctrl+B` prefix) work in ALL modes
- Modal keybindings only work in specific modes

### 3. **Workspace Switching Doesn't Destroy Windows**

Windows persist when switching workspaces. They're just hidden. Track with:
```go
window.Workspace  // Current workspace (1-9)
```

### 4. **PTY Resize Must Match Window Size**

When resizing windows, always update PTY dimensions:
```go
w.Pty.Resize(w.Width, w.Height)
```

Failure to do this causes rendering glitches.

### 5. **Animation State Tracking**

Windows being animated have flags set:
```go
w.Minimizing      // True during minimize animation
w.IsBeingManipulated  // True during drag/resize
```

Don't modify window position/size during animations.

### 6. **Copy Mode is Per-Window**

Each `terminal.Window` has its own `CopyMode` state:
```go
if w.CopyMode != nil {
    // Copy mode is active for this window
}
```

Don't confuse with global `os.Mode`.

### 7. **Prefix Key Timeout**

The `Ctrl+B` prefix expires after 2 seconds (`config.PrefixCommandTimeout`). Check:
```go
if os.PrefixActive && time.Since(os.LastPrefixTime) > config.PrefixCommandTimeout {
    os.PrefixActive = false
}
```

### 8. **Z-Order Management**

Window stacking is managed by `window.Z` field. When focusing:
```go
w.Z = maxZ + 1  // Bring to front
```

### 9. **No Linting in CI**

There's no automated linting in GitHub Actions. You MUST:
- Run `go fmt ./...` before committing
- Run `golangci-lint run` locally (via Nix shell)
- Manually verify code quality

### 10. **Build Tags and CGO**

Builds use `CGO_ENABLED=0` for static binaries. Don't add dependencies that require CGO.

---

## Keybinding System

### Configuration

Keybindings are stored in TOML format at `~/.config/tuios/config.toml`:

```toml
[keybindings.window_management]
create_window = "n"
close_window = "x"
toggle_tiling = "t"

[keybindings.prefix]
prefix = "ctrl+b"
```

### Adding New Keybindings

1. **Define in `internal/config/registry.go`**:
   ```go
   var DefaultKeybindings = map[string]map[string]string{
       "window_management": {
           "your_action": "key",
       },
   }
   ```

2. **Handle in `internal/input/handler.go`** or `internal/input/actions.go`:
   ```go
   if config.KeyBindings.WindowManagement.YourAction.Matches(msg) {
       // Handle action
   }
   ```

3. **Document in `docs/KEYBINDINGS.md`**

### Kitty Protocol Support

TUIOS supports extended keyboard protocol (Kitty protocol) for advanced key combinations:
- `Shift+Space`, `Ctrl+;`, etc.
- Handled by `internal/config/keynormalizer.go`

---

## Tape Scripting System

### Purpose

Tape scripts (`.tape` files) automate TUIOS interactions for:
- Demo recording
- Automated testing
- CI/CD workflows
- Documentation generation

### Components

| File | Purpose |
|------|---------|
| `tape/lexer.go` | Tokenize `.tape` files |
| `tape/parser.go` | Parse tokens into commands |
| `tape/command.go` | Command type definitions |
| `tape/executor.go` | Execute commands in headless mode |
| `tape/player.go` | Execute commands with TUI visible |
| `tape/recorder.go` | Record user interactions as tape |

### Tape Syntax Example

```tape
# Create a new window
NewWindow
Sleep 500ms

# Type and execute command
Type "echo 'Hello, TUIOS!'"
Enter

# Navigate
Ctrl+B
Type "n"

# Switch workspace
SwitchWorkspace 2
Sleep 1s
```

### Supported Commands

- **Typing**: `Type "text"`, `Type@100ms "text"` (with speed)
- **Special keys**: `Enter`, `Space`, `Tab`, `Escape`, `Backspace`, `Delete`
- **Navigation**: `Up`, `Down`, `Left`, `Right`
- **Modifiers**: `Ctrl+X`, `Alt+X`, `Shift+X`
- **Timing**: `Sleep 500ms`, `Sleep 2s`
- **Window actions**: `NewWindow`, `CloseWindow`, `ToggleTiling`
- **Workspace**: `SwitchWorkspace N`, `MoveToWorkspace N`

### Adding New Tape Commands

1. **Add token in `tape/token.go`**:
   ```go
   TokenYourCommand TokenType = "YOUR_COMMAND"
   ```

2. **Lex in `tape/lexer.go`**:
   ```go
   case "YourCommand":
       return Token{Type: TokenYourCommand, ...}
   ```

3. **Parse in `tape/parser.go`**:
   ```go
   case TokenYourCommand:
       return p.parseYourCommand()
   ```

4. **Execute in `tape/executor.go`** and `tape/player.go`**

---

## Release Process

### Versioning

TUIOS uses semantic versioning (`vX.Y.Z`):
- **Major**: Breaking changes
- **Minor**: New features
- **Patch**: Bug fixes

### Automated Release (Maintainer Only)

1. Tag a release:
   ```bash
   git tag v0.4.0
   git push origin v0.4.0
   ```

2. GitHub Actions automatically:
   - Runs GoReleaser
   - Builds binaries for all platforms
   - Publishes to AUR (Arch Linux)
   - Updates Homebrew tap
   - Creates GitHub release with changelog

### GoReleaser Configuration

- **File**: `.goreleaser.yml`
- **Platforms**: Linux, macOS, Windows, FreeBSD, OpenBSD
- **Architectures**: amd64, arm64, arm (v6, v7), 386
- **CGO**: Disabled for static binaries
- **Tests**: Commented out (not run during release)

### Changelog Format

Uses conventional commits:
- `feat:` → New Features section
- `fix:` → Bug Fixes section
- `perf:` → Performance Improvements section
- `docs:` → Documentation section

Excluded from changelog:
- `docs:`, `test:`, `ci:`, `chore:`
- Merge commits

---

## Dependencies

### Core Libraries

| Library | Version | Purpose |
|---------|---------|---------|
| Bubble Tea | v2.0.0-beta.6 | TUI framework (MVU pattern) |
| Lipgloss | v2.0.0-beta.3 | Terminal styling |
| xpty | v0.1.3 | Cross-platform PTY interface |
| Wish | v2.0.0-20250725 | SSH server framework |
| Cobra | v1.10.1 | CLI framework |
| go-toml | v2.2.4 | TOML configuration parsing |
| uuid | v1.6.0 | Unique window identifiers |
| gopsutil | v4.25.10 | CPU/RAM monitoring |

**Go Version**: 1.24.4+ required

### Vendored Packages

- `internal/vt/`: Terminal emulator (DO NOT modify casually)

### Adding Dependencies

```bash
# Add dependency
go get github.com/some/package@version

# Tidy and verify
go mod tidy

# Test build
go build ./cmd/tuios
```

**Important**: Avoid dependencies requiring CGO (breaks static builds).

---

## Debugging

### Debug Mode

```bash
# Enable debug logging
./tuios --debug
```

### Live Debugging

- **Press `Ctrl+L`**: View live log overlay
- **Press `Shift+C`**: View style cache statistics
- **Press `Ctrl+B D s`**: Show system resource usage

### CPU Profiling

```bash
# Run with profiling
./tuios --cpuprofile cpu.prof

# Analyze profile
go tool pprof cpu.prof
```

### Common Issues

**Problem**: Windows not rendering correctly
- Check `window.Dirty` flag
- Verify PTY resize matches window size
- Check style cache stats (`Shift+C`)

**Problem**: Input not working
- Verify correct mode (`os.Mode`)
- Check keybinding conflicts in `config.toml`
- Test with `./tuios --debug` and check logs

**Problem**: Memory leaks
- Check goroutine cleanup (`window.cancelFunc`)
- Verify pool usage (`pool.Put*` after `pool.Get*`)
- Profile with `pprof`

**Problem**: High CPU usage
- Check animation state (shouldn't be constant)
- Verify background window throttling
- Review style cache hit rate

---

## Contributing Workflow

### 1. Check Existing Issues

Search [GitHub Issues](https://github.com/Gaurav-Gosain/tuios/issues) before starting work.

### 2. Create Feature Branch

```bash
git checkout -b feat/your-feature-name
```

### 3. Make Changes

- Follow code conventions (see above)
- Add package comments if creating new files
- Run `go fmt ./...` frequently
- Test manually (see checklist)

### 4. Commit

```bash
git add .
git commit -m "feat: add your feature description"
```

Use conventional commit prefixes: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`

### 5. Push and Create PR

```bash
git push origin feat/your-feature-name
```

Open PR using template at `.github/PULL_REQUEST_TEMPLATE/pull_request_template.md`

### 6. PR Review

- Maintainer (@Gaurav-Gosain) will review
- Address feedback
- Once approved, PR will be merged
- Your contribution appears in next release

---

## Documentation

### Updating Docs

When changing functionality, update:

- **README.md**: User-facing features, installation, quick start
- **docs/KEYBINDINGS.md**: New keybindings or shortcuts
- **docs/CONFIGURATION.md**: Configuration options
- **docs/CLI_REFERENCE.md**: CLI flags or subcommands
- **docs/ARCHITECTURE.md**: Architectural changes, new components
- **AGENTS.md** (this file): Developer-facing patterns or commands

### Documentation Standards

- Clear, concise language
- Code examples for complex features
- Mermaid diagrams for architecture (where appropriate)
- Keep table of contents updated

---

## Nix Development

### Entering Dev Shell

```bash
nix develop
```

Provides:
- Go 1.24+
- gopls (LSP)
- golangci-lint
- gosec
- gotools
- treefmt

### Custom Commands (in Nix shell)

```bash
# Check security vulnerabilities
go-checksec

# Update dependencies
go-update

# Format everything
nix fmt
```

### Building with Nix

```bash
# Build tuios package
nix build .#tuios

# Run without installing
nix run .#tuios
```

---

## SSH Server Mode

### Architecture

- **Per-connection isolation**: Each SSH client gets independent TUIOS instance
- **No shared state**: Sessions are completely isolated
- **Wish v2 middleware**: Authentication, logging, etc.

### Code Location

- **`internal/server/ssh.go`**: SSH server implementation

### Testing SSH

```bash
# Start server
./tuios ssh --host 0.0.0.0 --port 2222

# Connect from another terminal
ssh -p 2222 localhost

# Multiple clients (test isolation)
ssh -p 2222 localhost  # Client 1
ssh -p 2222 localhost  # Client 2 (separate session)
```

---

## Style and Theming

### Theme System

- **Location**: `internal/theme/`
- **Built-in themes**: Dracula, Catppuccin, Nord, Tokyo Night, etc.
- **User themes**: Configurable via TOML (future feature)

### Style Cache

See `docs/STYLE_CACHE.md` for deep dive.

**Key points**:
- LRU cache with 1024 entries (configurable)
- Hash-based lookup (fg color, bg color, attributes)
- Two-tier caching (full render vs optimized)
- 40-60% allocation reduction
- View stats with `Shift+C`

### Modifying Styles

**DON'T**:
```go
style := lipgloss.NewStyle().Foreground(color)  // Bypasses cache
```

**DO**:
```go
style := getOrCreateCellStyle(cell, focused)  // Uses cache
```

---

## Window Management Internals

### Window Lifecycle

1. **Creation**: `app.createWindow()` → spawn PTY → start shell
2. **Rendering**: `app.renderWindow()` → VT buffer → lipgloss layer
3. **Input**: User input → PTY write → shell process
4. **Output**: Shell writes → PTY read → VT parse → mark dirty
5. **Closure**: `app.closeWindow()` → send SIGTERM → wait → cleanup

### Workspace Management

- **9 workspaces** (numbered 1-9)
- **Per-workspace focus**: `os.WorkspaceFocus[workspace]` remembers focused window
- **Per-workspace layouts**: `os.WorkspaceLayouts[workspace]` stores custom positions
- **Per-workspace tiling**: `os.WorkspaceMasterRatio[workspace]` stores master ratio

### Tiling Algorithm

- **Location**: `internal/layout/tiling.go`
- **Pattern**: Master + stack
- **Master ratio**: Configurable (0.3 - 0.7, default 0.5)
- **Keybindings**:
  - `t`: Toggle tiling
  - `h`/`l`: Adjust master ratio

---

## Animation System

### Animation Types

1. **Minimize**: Window → Dock
2. **Restore**: Dock → Window
3. **Snap**: Window → Snapped position

### Animation Properties

- **Duration**: 200-300ms (see `internal/config/constants.go`)
- **Easing**: Custom easing functions in `internal/ui/animation.go`
- **Frame updates**: 60 FPS during animation
- **State tracking**: `animation.Progress` (0.0 to 1.0)

### Adding Animations

```go
anim := ui.NewMinimizeAnimation(window, dockX, dockY, config.DefaultAnimationDuration)
os.Animations = append(os.Animations, anim)
```

Update in `app.Update()`:
```go
for _, anim := range os.Animations {
    anim.Update()
    if anim.Complete {
        // Cleanup
    }
}
```

---

## Performance Profiling

### CPU Profiling

```bash
# Enable profiling
./tuios --cpuprofile cpu.prof

# Let it run for a bit, then quit

# Analyze
go tool pprof cpu.prof

# Interactive commands in pprof:
(pprof) top10        # Top 10 functions by CPU
(pprof) list funcName  # Source code with annotations
(pprof) web          # Visual graph (requires graphviz)
```

### Memory Profiling

```go
// Add to code temporarily:
import "runtime/pprof"

f, _ := os.Create("mem.prof")
pprof.WriteHeapProfile(f)
f.Close()
```

Analyze:
```bash
go tool pprof mem.prof
```

### Benchmarking

When adding performance-critical code, add benchmarks:

```go
func BenchmarkYourFunction(b *testing.B) {
    for i := 0; i < b.N; i++ {
        YourFunction()
    }
}
```

Run:
```bash
go test -bench=. -benchmem ./internal/yourpackage/
```

---

## Cross-Platform Considerations

### Supported Platforms

- **Linux**: x86_64, arm64, armv6, armv7, i386
- **macOS**: arm64 (Apple Silicon), x86_64 (Intel)
- **Windows**: x86_64, i386 (limited testing)
- **FreeBSD**: x86_64, arm64, i386
- **OpenBSD**: x86_64, arm64

### Platform-Specific Code

Use build tags when necessary:

```go
//go:build linux
// +build linux

package system

// Linux-specific implementation
```

### PTY Differences

PTY behavior varies by platform. The `xpty` library abstracts this, but be aware:
- **Windows**: Uses ConPTY (newer Windows 10+)
- **Unix**: Uses traditional PTY system

---

## Security Considerations

### SSH Server

- **Key generation**: Automatic host key generation on first run
- **Authentication**: Password-based (default) or public key
- **Isolation**: Per-connection TUIOS instances (no shared state)

### Shell Execution

- **Direct shell spawn**: No command injection risk (uses `exec.Command` with args)
- **Environment passthrough**: Be cautious with `$TERM`, `$SHELL`, etc.

### Dependencies

- **Vulnerability scanning**: Run `gosec ./...` (via Nix shell)
- **Keep updated**: Regularly update dependencies with `go get -u ./...`

---

## Future Features (Roadmap)

### Planned

- [ ] Theme and color customization (TOML-based)
- [ ] Session persistence (save/restore workspaces)
- [ ] Split panes (horizontal/vertical)
- [ ] Tabs within windows
- [ ] Plugin system (Lua or Go-based)

### In Progress

Check [GitHub Issues](https://github.com/Gaurav-Gosain/tuios/issues) for current work.

---

## FAQ for AI Agents

### Q: Should I add tests for my changes?

**A:** If your change involves parsing/logic (like tape scripts or config), yes. For rendering/UI, manual testing is sufficient (project has limited test infrastructure).

### Q: Should I run tests before committing?

**A:** Yes, always run `go test ./...` even though tests aren't comprehensive. Also run `go fmt ./...` and `golangci-lint run` (if in Nix shell).

### Q: Can I modify `internal/vt/`?

**A:** Only if absolutely necessary and you understand terminal emulation. This is vendored code. Prefer fixes upstream if possible.

### Q: How do I add a new keybinding?

**A:** See "Keybinding System" section above. Update registry, handler, and documentation.

### Q: Should I update AGENTS.md when I change something?

**A:** Yes, if your change affects developer workflow, commands, patterns, or architecture. Keep this document in sync.

### Q: How do I test SSH functionality?

**A:** See "SSH Server Mode" section. Start server, connect from separate terminal, verify isolation with multiple clients.

### Q: What if my feature requires CGO?

**A:** Avoid if possible. Static binaries (CGO_ENABLED=0) are a project goal. Discuss with maintainer if absolutely necessary.

### Q: How do I profile performance?

**A:** Use `--cpuprofile` flag and `go tool pprof`. See "Performance Profiling" section.

### Q: Can I add a new CLI subcommand?

**A:** Yes. Use Cobra pattern (see "Cobra Commands" section). Add to `cmd/tuios/main.go` and update `docs/CLI_REFERENCE.md`.

### Q: How do I handle terminal resize events?

**A:** Update window size, call `w.Pty.Resize(width, height)`, mark window as dirty. See `app.handleWindowResize()`.

---

## Summary for Quick Reference

**Essential commands**:
```bash
go build -o tuios ./cmd/tuios    # Build
go test ./...                    # Test
go fmt ./...                     # Format (REQUIRED before commit)
./tuios --debug                  # Run with debug logging
```

**Key files**:
- `cmd/tuios/main.go`: CLI entry point
- `internal/app/os.go`: Core application state
- `internal/app/render.go`: Rendering pipeline
- `internal/input/handler.go`: Input routing
- `internal/config/constants.go`: All constants

**Core patterns**:
- MVU architecture (Bubble Tea)
- Two-mode system (Window Management + Terminal)
- Object pooling for performance
- Style caching for rendering
- Modal input routing

**Before committing**:
- [ ] Run `go fmt ./...`
- [ ] Run `go test ./...`
- [ ] Manual testing (create/close windows, switch workspaces)
- [ ] Update documentation if needed
- [ ] Use conventional commit messages

**Getting help**:
- Check `docs/` directory
- Read `docs/ARCHITECTURE.md` for deep dive
- See `docs/CONTRIBUTING.md` for contributor guide
- GitHub Issues for bugs/features
- GitHub Discussions for questions

---

**Document Version**: 1.0  
**Last Updated**: 2025-11-27  
**Maintainer**: @Gaurav-Gosain
