# CLI Reference

This document provides a complete reference for TUIOS command-line interface.

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
- `--debug` - Enable debug logging
- `--cpuprofile <file>` - Write CPU profile to file
- `-h, --help` - Show help for tuios
- `-v, --version` - Show version information

**Example:**
```bash
tuios                      # Start TUIOS normally
tuios --debug              # Start with debug logging
tuios --cpuprofile cpu.prof  # Start with CPU profiling
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

These flags are available on all commands:

- `--debug` - Enable debug logging
- `--cpuprofile <file>` - Write CPU profile to file
- `-h, --help` - Show help

---

## Common Usage Examples

### Basic Usage

Start TUIOS normally:
```bash
tuios
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

### Development & Debugging

```bash
# Run with debug logging
tuios --debug
# Then press Ctrl+L during runtime to view logs

# CPU profiling
tuios --cpuprofile cpu.prof
# Use the application, then exit
go tool pprof cpu.prof
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
