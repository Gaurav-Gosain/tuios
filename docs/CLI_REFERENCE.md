# CLI Reference

This document provides a complete reference for TUIOS command-line interface.

## Table of Contents

- [Overview](#overview)
- [Installation](#installation)
- [Usage](#usage)
- [Commands](#commands)
  - [Root Command](#root-command)
  - [Theming](#theming)
  - [tuios ssh](#tuios-ssh)
  - [tuios-web (separate binary)](#tuios-web-separate-binary)
  - [tuios config](#tuios-config)
  - [tuios keybinds](#tuios-keybinds)
  - [tuios completion](#tuios-completion)
  - [tuios help](#tuios-help)
- [Global Flags](#global-flags)
- [Common Usage Examples](#common-usage-examples)
- [Environment Variables](#environment-variables)
- [Exit Codes](#exit-codes)
- [Version Information](#version-information)
- [Command Migration Guide](#command-migration-guide)
- [Related Documentation](#related-documentation)

## Overview

TUIOS uses a modern command-line interface built with Cobra and Fang, providing:
- Subcommand structure for better organization
- Styled help output and error messages
- Shell completion generation
- Man page generation support

## Installation

### Homebrew (macOS/Linux)

```bash
brew tap Gaurav-Gosain/tap
brew install tuios
```

### Arch Linux (AUR)

```bash
# Using yay
yay -S tuios-bin

# Using paru
paru -S tuios-bin
```

### Nix

```bash
# Run directly
nix run github:Gaurav-Gosain/tuios#tuios

# Or add to your configuration
nix-shell -p tuios
```

### Quick Install Script (Linux/macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/Gaurav-Gosain/tuios/main/install.sh | bash
```

### Go Install

```bash
go install github.com/Gaurav-Gosain/tuios/cmd/tuios@latest
```

### Pre-built Binaries

Download from [GitHub Releases](https://github.com/Gaurav-Gosain/tuios/releases)

---

## Usage

```bash
tuios [command] [flags]
```

## Commands

### Root Command

Run TUIOS in local mode (default behavior):

```bash
tuios
```

**Flags:**
- `--theme <name>` - Set color theme (default: "tokyonight")
- `--list-themes` - List all available themes and exit
- `--preview-theme <name>` - Preview a theme's 16 ANSI colors and exit
- `--ascii-only` - Use ASCII characters instead of Nerd Font icons
- `--show-keys` - Enable showkeys overlay (screencaster-style key display)
- `--debug` - Enable debug logging
- `--cpuprofile <file>` - Write CPU profile to file
- `-h, --help` - Show help for tuios
- `-v, --version` - Show version information

**Examples:**
```bash
tuios                          # Start TUIOS normally (tokyonight theme)
tuios --theme dracula          # Start with Dracula theme
tuios --ascii-only             # Start without Nerd Font icons
tuios --show-keys              # Start with showkeys overlay enabled
tuios --list-themes            # List all available themes
tuios --preview-theme nord     # Preview Nord theme colors
tuios --debug                  # Start with debug logging
tuios --cpuprofile cpu.prof    # Start with CPU profiling

# Combine multiple flags
tuios --theme nord --show-keys # Use Nord theme with showkeys enabled

# Interactive theme selection with fzf
tuios --theme $(tuios --list-themes | fzf --preview 'tuios --preview-theme {}')
```

---

## Theming

TUIOS includes 300+ built-in color themes from various sources including Gogh, iTerm2, and custom themes.

### Available Themes

List all available themes:
```bash
tuios --list-themes
```

**Popular themes include:**
- `tokyonight` (default) - A clean, dark theme with vibrant colors
- `dracula` - Dark theme with purple accent
- `nord` - An arctic, north-bluish color palette
- `gruvbox_dark` - Retro groove color scheme
- `catppuccin_mocha` - Soothing pastel theme
- `monokai_pro` - Professional dark theme
- `solarized_dark` - Precision colors for machines and people
- `github` - GitHub's light theme
- `one_dark` - Atom's iconic dark theme

### Preview Themes

Preview a theme's 16 ANSI colors before using it:
```bash
tuios --preview-theme dracula
```

The preview shows all 16 colors (8 standard + 8 bright variants) with their color codes.

### Using Themes

Set a theme at startup:
```bash
tuios --theme nord
```

The theme affects:
- Terminal text colors (ANSI 0-15)
- Window borders
- UI elements (status bar, dock, overlays)
- Default foreground/background colors

**Note:** The theme only affects the 16 base ANSI colors. Applications using 256-color or true color (RGB) will display those colors unchanged.

### Interactive Theme Selection

Use `fzf` for interactive theme selection with live preview:
```bash
tuios --theme $(tuios --list-themes | fzf --preview 'tuios --preview-theme {}')
```

This allows you to browse all themes with a live color preview before selecting one.

### Theme Persistence

Themes are set via command-line flag and not currently stored in configuration. To always use a specific theme:

**Shell alias:**
```bash
# Add to ~/.bashrc, ~/.zshrc, etc.
alias tuios='tuios --theme nord'
```

**Script wrapper:**
```bash
#!/bin/bash
exec tuios --theme dracula "$@"
```

---

### `tuios ssh`

Run TUIOS as an SSH server for remote access.

**Usage:**
```bash
tuios ssh [flags]
```

**Flags:**
- `--host <string>` - SSH server host (default: "localhost")
- `--port <string>` - SSH server port (default: "2222")
- `--key-path <string>` - Path to SSH host key (auto-generated if not specified)

**Examples:**
```bash
# Start SSH server on default port
tuios ssh

# Start on custom port
tuios ssh --port 8022

# Listen on all interfaces
tuios ssh --host 0.0.0.0 --port 2222

# Use custom host key
tuios ssh --key-path /path/to/host_key
```

**Connecting:**
```bash
ssh -p 2222 localhost
```

---

## `tuios-web` (Separate Binary)

**Security Notice:** The web terminal functionality has been extracted to a separate binary (`tuios-web`) to provide better security isolation. This prevents the web server from being used as a potential backdoor in the main TUIOS binary.

**Installation:**
```bash
# Homebrew
brew install tuios-web

# AUR
yay -S tuios-web-bin

# Go install
go install github.com/Gaurav-Gosain/tuios/cmd/tuios-web@latest
```

**Usage:**
```bash
tuios-web [flags]
```

**Flags:**
- `--host <string>` - Web server host (default: "localhost")
- `--port <string>` - Web server port (default: "7681")
- `--read-only` - Disable input from clients (view only mode)
- `--max-connections <int>` - Maximum concurrent connections (default: 0 = unlimited)
- `--theme <name>` - Color theme forwarded to TUIOS instances
- `--show-keys` - Enable showkeys overlay
- `--ascii-only` - Use ASCII characters instead of Nerd Font icons
- `--border-style <style>` - Window border style
- `--dockbar-position <pos>` - Dockbar position
- `--hide-window-buttons` - Hide window control buttons
- `--scrollback-lines <int>` - Scrollback buffer size
- `--debug` - Enable debug logging

**Features:**
- Full TUIOS experience in the browser
- WebGL-accelerated rendering via xterm.js for smooth 60fps
- WebSocket and WebTransport (HTTP/3 over QUIC) protocols
- Bundled JetBrains Mono Nerd Font for proper icon rendering
- Settings panel for transport, renderer, and font size preferences
- Cell-based mouse event deduplication (80-95% traffic reduction)
- Automatic reconnection with exponential backoff
- Self-signed TLS certificate generation for development
- No CGO dependencies (pure Go)

**Examples:**
```bash
# Start web server on default port
tuios-web

# Start on custom port
tuios-web --port 8080

# Bind to all interfaces for remote access
tuios-web --host 0.0.0.0 --port 7681

# Start in read-only mode (view only)
tuios-web --read-only

# Start with theme and show-keys overlay
tuios-web --theme dracula --show-keys

# Limit concurrent connections
tuios-web --max-connections 10
```

**Accessing:**
```bash
# Open in browser
open http://localhost:7681

# For HTTPS/WebTransport (development with self-signed cert)
open https://localhost:7681

# Note: Your browser will show a security warning for the self-signed certificate.
# Click "Advanced" and proceed to accept the certificate.
```

**Protocol Selection:**
The client automatically selects the best available transport:
1. **WebTransport (HTTP/3 over QUIC)** - Lower latency, better multiplexing (requires HTTPS)
2. **WebSocket (fallback)** - Broad browser compatibility

For complete documentation, see [Web Terminal Mode](WEB.md).

---

### `tuios config`

Manage TUIOS configuration file.

**Subcommands:**
- `tuios config path` - Print configuration file path
- `tuios config edit` - Edit configuration in $EDITOR
- `tuios config reset` - Reset configuration to defaults

#### `tuios config path`

Print the location of the TUIOS configuration file.

**Example:**
```bash
tuios config path
# Output: /Users/username/.config/tuios/config.toml
```

#### `tuios config edit`

Open the configuration file in your default editor.

**Requirements:** The `$EDITOR` or `$VISUAL` environment variable must be set. Falls back to vim, vi, nano, or emacs if found.

**Example:**
```bash
export EDITOR=vim
tuios config edit
```

#### `tuios config reset`

Reset the configuration file to default settings.

**Warning:** This will overwrite your existing configuration after confirmation.

**Example:**
```bash
tuios config reset
# Prompts: Are you sure you want to reset to defaults? (yes/no):
```

---

### `tuios keybinds`

View and inspect keybinding configuration.

**Aliases:** `keys`, `kb`

**Subcommands:**
- `tuios keybinds list` - List all configured keybindings
- `tuios keybinds list-custom` - List only customized keybindings

#### `tuios keybinds list`

Display all configured keybindings in formatted tables organized by category.

**Example:**
```bash
tuios keybinds list
```

**Output:** Shows comprehensive tables with all keybindings across categories:
- Window Management
- Workspaces
- Layout
- Modes
- Selection
- System

#### `tuios keybinds list-custom`

Show only keybindings that differ from defaults, with a comparison view.

**Example:**
```bash
tuios keybinds list-custom
```

**Output:** Three-column table showing:
- Action name
- Default keybinding
- Your custom keybinding

---

### `tuios completion`

Generate shell completion scripts for command-line autocompletion.

**Supported shells:**
- bash
- zsh
- fish
- powershell

**Usage:**
```bash
tuios completion [shell]
```

**Examples:**

**Bash:**
```bash
# Generate and install completion
tuios completion bash > /etc/bash_completion.d/tuios

# Or for user-specific completion
tuios completion bash > ~/.local/share/bash-completion/completions/tuios
source ~/.bashrc
```

**Zsh:**
```bash
# Generate and install completion
tuios completion zsh > "${fpath[1]}/_tuios"

# Or add to your .zshrc
echo "autoload -U compinit; compinit" >> ~/.zshrc
tuios completion zsh > ~/.zsh/completions/_tuios
```

**Fish:**
```bash
# Generate and install completion
tuios completion fish > ~/.config/fish/completions/tuios.fish
```

**PowerShell:**
```bash
# Generate completion script
tuios completion powershell > tuios.ps1

# Add to your PowerShell profile
echo ". $(pwd)/tuios.ps1" >> $PROFILE
```

---

### `tuios help`

Get help about any command.

**Usage:**
```bash
tuios help [command]
```

**Examples:**
```bash
tuios help              # Show general help
tuios help ssh          # Show help for ssh command
tuios help config edit  # Show help for config edit subcommand
```

---

## Global Flags

These flags are available on the root command:

- `--theme <name>` - Set color theme (default: "tokyonight")
- `--list-themes` - List all available themes and exit
- `--preview-theme <name>` - Preview a theme's colors and exit
- `--ascii-only` - Use ASCII characters instead of Nerd Font icons
- `--show-keys` - Enable showkeys overlay (screencaster-style key display)
- `--debug` - Enable debug logging
- `--cpuprofile <file>` - Write CPU profile to file
- `-h, --help` - Show help

---

## Common Usage Examples

### Basic Usage

Start TUIOS normally:
```bash
tuios

# Start with showkeys overlay for screencasting
tuios --show-keys
```

### Theming

```bash
# Start with a specific theme
tuios --theme dracula

# List all available themes
tuios --list-themes

# Preview a theme before using it
tuios --preview-theme nord

# Interactive theme selection with fzf
tuios --theme $(tuios --list-themes | fzf --preview 'tuios --preview-theme {}')

# Use ASCII mode (no Nerd Font required)
tuios --ascii-only

# Combine theme with ASCII mode
tuios --theme gruvbox_dark --ascii-only
```

### Configuration Management

```bash
# Find config file location
tuios config path

# Edit configuration
tuios config edit

# View all keybindings
tuios keybinds list

# View your customizations
tuios keybinds list-custom

# Reset to defaults
tuios config reset
```

### SSH Server Setup

```bash
# Start SSH server on default port
tuios ssh

# Start on custom port with remote access
tuios ssh --host 0.0.0.0 --port 8022

# Connect from another machine
ssh -p 8022 your-server-hostname
```

### Web Terminal Setup (tuios-web)

```bash
# Start web terminal on default port
tuios-web

# Start on custom port with remote access
tuios-web --host 0.0.0.0 --port 8080

# Open in browser
open http://localhost:7681

# Start in read-only mode for demonstrations
tuios-web --read-only

# Start with theme and overlay
tuios-web --theme dracula --show-keys

# Limit connections for production use
tuios-web --max-connections 50 --host 0.0.0.0
```

### Development & Debugging

```bash
# Run with debug logging
tuios --debug
# Then press Ctrl+L during runtime to view logs

# CPU profiling
tuios --cpuprofile cpu.prof
# Use the application, then exit
go tool pprof cpu.prof

# Screencasting with showkeys overlay
tuios --show-keys
# Or toggle during runtime with: Ctrl+B D k
```

### Shell Completions

```bash
# Install bash completion
tuios completion bash | sudo tee /etc/bash_completion.d/tuios

# Install zsh completion
tuios completion zsh > "${fpath[1]}/_tuios"

# Install fish completion
tuios completion fish > ~/.config/fish/completions/tuios.fish
```

---

## Man Pages

TUIOS supports man page generation through the Fang framework using mango.

**Generate man page:**
```bash
# This feature is built-in via Fang
# Man page generation will be available in a future release
```

---

## Environment Variables

### `$EDITOR` / `$VISUAL`

Used by `tuios config edit` to determine which editor to open.

**Example:**
```bash
export EDITOR=vim
export VISUAL=code
tuios config edit
```

**Fallback order:** `$EDITOR` → `$VISUAL` → vim → vi → nano → emacs

### `$SHELL`

TUIOS uses your default shell from this variable. If not set, it attempts to detect the appropriate shell for your platform.

### `COLORTERM`

For best color support, set this to `truecolor`:
```bash
export COLORTERM=truecolor
```

---

## Exit Codes

- `0` - Success
- `1` - Error (configuration error, network error, file not found, etc.)

---

## Version Information

The `--version` flag shows detailed build information:

```bash
tuios --version
```

**Output:**
```
tuios version v0.0.24
Commit: a1b2c3d
Built: 2025-01-15T10:30:00Z
By: goreleaser
```

---

## Command Migration Guide

If you're upgrading from an older version of TUIOS, here's how the commands have changed:

| Old Flag | New Command |
|----------|-------------|
| `--config-path` | `tuios config path` |
| `--edit-config` | `tuios config edit` |
| `--reset-config` | `tuios config reset` |
| `--list-keybinds` | `tuios keybinds list` |
| `--list-custom-keybinds` | `tuios keybinds list-custom` |
| `--ssh` | `tuios ssh` |
| `--ssh --host X --port Y` | `tuios ssh --host X --port Y` |
| `--version` | `tuios --version` or `tuios version` |
| `--help` | `tuios --help` or `tuios help` |

---

## Related Documentation

- [Configuration Guide](CONFIGURATION.md) - How to customize TUIOS
- [Keybindings Reference](KEYBINDINGS.md) - Complete keyboard shortcut reference
- [Architecture Guide](ARCHITECTURE.md) - Technical architecture details
- [README](../README.md) - Project overview and quick start
