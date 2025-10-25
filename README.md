# TUIOS - Terminal UI Operating System

<p>
    <a href="https://github.com/Gaurav-Gosain/tuios/releases"><img src="https://img.shields.io/github/release/Gaurav-Gosain/tuios.svg" alt="Latest Release"></a>
    <a href="https://pkg.go.dev/github.com/Gaurav-Gosain/tuios?tab=doc"><img src="https://godoc.org/github.com/Gaurav-Gosain/tuios?status.svg" alt="GoDoc"></a>
    <a href="https://deepwiki.com/Gaurav-Gosain/tuios"><img src="https://deepwiki.com/badge.svg" alt="Ask DeepWiki"></a>
</p>

<a title="This tool is Tool of The Week on Terminal Trove, The $HOME of all things in the terminal" href="https://terminaltrove.com/"><img src="https://cdn.terminaltrove.com/media/badges/tool_of_the_week/png/terminal_trove_tool_of_the_week_green_on_dark_grey_bg.png" alt="Terminal Trove Tool of The Week" style="width: 250px;" /></a>

![TUIOS](./assets/demo.gif)

TUIOS is a terminal-based window manager that provides a modern, efficient interface for managing multiple terminal sessions. Built with Go using the Charm stack (Bubble Tea v2 and Lipgloss v2), TUIOS offers a vim-like modal interface with comprehensive keyboard shortcuts, workspace support, and mouse interaction.

## Documentation

- **[Keybindings Reference](docs/KEYBINDINGS.md)** - Complete keyboard shortcut reference
- **[Configuration Guide](docs/CONFIGURATION.md)** - Customize keybindings and settings
- **[CLI Reference](docs/CLI_REFERENCE.md)** - Command-line options and flags
- **[Architecture Guide](docs/ARCHITECTURE.md)** - Technical architecture and design

<details>

<summary>Table of Contents</summary>

<!--toc:start-->

- [TUIOS - Terminal UI Operating System](#tuios-terminal-ui-operating-system)
  - [Documentation](#documentation)
  - [Installation](#installation)
  - [Features](#features)
  - [Quick Start](#quick-start)
  - [Architecture](#architecture)
  - [Performance](#performance)
  - [Troubleshooting](#troubleshooting)
  - [Roadmap](#roadmap)
  - [Development](#development)
  - [Star History](#star-history)
  - [License](#license)
  - [Acknowledgments](#acknowledgments)
  <!--toc:end-->

</details>

## Installation

**Quick Install (Linux/macOS):**
```bash
curl -fsSL https://raw.githubusercontent.com/Gaurav-Gosain/tuios/main/install.sh | bash
```

**Arch Linux (AUR):**
```bash
# Using yay
yay -S tuios-bin

# Using paru
paru -S tuios-bin
```

**Alternative Methods:**
- **[GitHub Releases](https://github.com/Gaurav-Gosain/tuios/releases)** - Download pre-built binaries
- **Go Install:** `go install github.com/Gaurav-Gosain/tuios/cmd/tuios@latest`
- **Docker:** `docker run -it --rm ghcr.io/gaurav-gosain/tuios:latest`
- **Build from Source:** See [Development](#development) section below

**Requirements:**
- A terminal with true color support (most modern terminals)
- Go 1.24+ (if building from source)

## Features

![TUIOS](./assets/tuios.gif)

- **Multiple Terminal Windows**: Create and manage multiple terminal sessions
- **9 Workspaces**: Organize windows across independent workspaces
- **Modal Interface**: Vim-inspired Window Management and Terminal modes
- **Automatic Tiling**: Optional grid-based window layouts
- **Vim-Style Copy Mode**: Navigate scrollback (10,000 lines), search, and select text with vim keybindings
- **Customizable Keybindings**: TOML configuration file with full keybinding customization
- **Mouse Support**: Click, drag, and resize with full mouse interaction
- **SSH Server Mode**: Remote terminal multiplexing with per-connection isolation
- **Smart Performance**: Style caching, viewport culling, adaptive refresh (60Hz/30Hz)

## Quick Start

**Launch TUIOS:**
```bash
tuios
```

**Essential Keys:**

*Window Management Mode (default):*
- `n` - Create new window
- `i` or `Enter` - Enter Terminal Mode

*Works from any mode:*
- `Ctrl+B c` - Create new window
- `Ctrl+B ?` - Toggle help overlay
- `Ctrl+B [` - Enter copy mode (vim-style scrollback)
- `Ctrl+B d` or `Esc` - Return to Window Management Mode
- `Ctrl+B q` - Quit

*Alternative (Ctrl+B t submenu):*
- `Ctrl+B t n` - Create new window
- `Ctrl+B t x` - Close window
- `Ctrl+B t r` - Rename window

**SSH Server Mode:**
```bash
tuios ssh                            # Start on localhost:2222
tuios ssh --host 0.0.0.0 --port 8022 # Custom host/port
ssh -p 2222 localhost                # Connect
```

**Configuration:**
```bash
tuios config edit                    # Edit keybindings in $EDITOR
tuios config path                    # Print config file location
tuios config reset                   # Reset to defaults
tuios keybinds list                  # View all current keybindings
tuios keybinds list-custom           # View only your customizations
```

**Shell Completions:**
```bash
# Bash
tuios completion bash > /etc/bash_completion.d/tuios

# Zsh
tuios completion zsh > "${fpath[1]}/_tuios"

# Fish
tuios completion fish > ~/.config/fish/completions/tuios.fish

# PowerShell
tuios completion powershell > tuios.ps1
```

For complete keybindings, configuration options, and CLI flags, see the [Documentation](#documentation) section above.

## Architecture

TUIOS is built on the Bubble Tea v2 framework following the Model-View-Update pattern. For detailed architecture diagrams and technical documentation, see [Architecture Guide](docs/ARCHITECTURE.md).

**Key Technologies:**
- **[Bubble Tea v2](https://github.com/charmbracelet/bubbletea)** - Event-driven TUI framework
- **[Lipgloss v2](https://github.com/charmbracelet/lipgloss)** - Terminal styling
- **[go-pty](https://github.com/aymanbagabas/go-pty)** - Cross-platform PTY interface
- **[Wish v2](https://github.com/charmbracelet/wish)** - SSH server
- **Vendored VT** - ANSI/VT100 terminal emulator

**Core Components:**
- **Window Manager** (`internal/app/os.go`) - Central state and workspace orchestration
- **Terminal Emulation** (`internal/vt/`) - ANSI parser with 10,000 line scrollback
- **Rendering Engine** (`internal/app/render.go`) - Layer composition with viewport culling
- **Input System** (`internal/input/`) - Modal routing and 100+ configurable keybindings
- **Configuration** (`internal/config/`) - TOML-based keybinding customization

## Performance

**Optimization Strategies:**
- Smart caching with sequence-based change detection
- Viewport culling for off-screen windows
- Adaptive refresh rates (60Hz focused, 20Hz background)
- Memory pooling for strings, buffers, and styles
- LRU style cache (40-60% allocation reduction)
- Frame skipping when no changes detected

## Troubleshooting

**Common Issues:**
- **Colors not displaying**: Set `COLORTERM=truecolor` environment variable
- **SSH connection issues**: Use `--host 0.0.0.0` for remote access, check firewall settings
- **Performance with many windows**: Enable tiling mode (`t` key)

**Debug Mode:** Press `Ctrl+L` to view system logs.

## Roadmap

**Completed:**
- ✓ Multiple windows with 9 workspaces
- ✓ Automatic tiling and snapping
- ✓ Vim-style copy mode with search
- ✓ SSH server mode
- ✓ Performance optimizations
- ✓ TOML configuration system with custom keybindings

**Planned:**
- [ ] Theme and color customization
- [ ] Session persistence
- [ ] Split panes and tabs
- [ ] Plugin system

## Development

Contributions are welcome! Feel free to open issues or pull requests.

**Build from Source:**
```bash
git clone https://github.com/gaurav-gosain/tuios.git
cd tuios
go build -o tuios ./cmd/tuios
./tuios
```

**Run Tests:**
```bash
go test ./...
```

**Support:** [![ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/B0B81N8V1R)

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=Gaurav-Gosain/tuios&type=Date&theme=dark)](https://star-history.com/#Gaurav-Gosain/tuios&Date)

<p style="display:flex;flex-wrap:wrap;"> 
<img alt="GitHub Language Count" src="https://img.shields.io/github/languages/count/Gaurav-Gosain/tuios" style="padding:5px;margin:5px;" /> 
<img alt="GitHub Top Language" src="https://img.shields.io/github/languages/top/Gaurav-Gosain/tuios" style="padding:5px;margin:5px;" /> 
<img alt="Repo Size" src="https://img.shields.io/github/repo-size/Gaurav-Gosain/tuios" style="padding:5px;margin:5px;" /> 
<img alt="GitHub Issues" src="https://img.shields.io/github/issues/Gaurav-Gosain/tuios" style="padding:5px;margin:5px;" /> 
<img alt="GitHub Closed Issues" src="https://img.shields.io/github/issues-closed/Gaurav-Gosain/tuios" style="padding:5px;margin:5px;" /> 
<img alt="GitHub Pull Requests" src="https://img.shields.io/github/issues-pr/Gaurav-Gosain/tuios" style="padding:5px;margin:5px;" /> 
<img alt="GitHub Closed Pull Requests" src="https://img.shields.io/github/issues-pr-closed/Gaurav-Gosain/tuios" style="padding:5px;margin:5px;" /> 
<img alt="GitHub Contributors" src="https://img.shields.io/github/contributors/Gaurav-Gosain/tuios" style="padding:5px;margin:5px;" /> 
<img alt="GitHub Last Commit" src="https://img.shields.io/github/last-commit/Gaurav-Gosain/tuios" style="padding:5px;margin:5px;" /> 
<img alt="GitHub Commit Activity (Week)" src="https://img.shields.io/github/commit-activity/w/Gaurav-Gosain/tuios" style="padding:5px;margin:5px;" /> 
</p>

## License

This project is licensed under the MIT License -
see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- The Charm team for Bubble Tea and Lipgloss libraries
- The Go terminal ecosystem for PTY and VT implementations
- The vim and tmux communities for interface design inspiration
