# TUIOS - Terminal UI Operating System

<p>
    <a href="https://github.com/Gaurav-Gosain/tuios/releases"><img src="https://img.shields.io/github/release/Gaurav-Gosain/tuios.svg" alt="Latest Release"></a>
    <a href="https://pkg.go.dev/github.com/Gaurav-Gosain/tuios?tab=doc"><img src="https://godoc.org/github.com/Gaurav-Gosain/tuios?status.svg" alt="GoDoc"></a>
    <a href="https://deepwiki.com/Gaurav-Gosain/tuios"><img src="https://deepwiki.com/badge.svg" alt="Ask DeepWiki"></a>
</p>

<a title="This tool is Tool of The Week on Terminal Trove, The $HOME of all things in the terminal" href="https://terminaltrove.com/"><img src="https://cdn.terminaltrove.com/media/badges/tool_of_the_week/png/terminal_trove_tool_of_the_week_green_on_dark_grey_bg.png" alt="Terminal Trove Tool of The Week" style="width: 250px;" /></a>

![TUIOS](./assets/demo.gif)

TUIOS is a terminal-based window manager that provides a modern, efficient
interface for managing multiple terminal sessions. Built with Go using the
Charm stack (Bubble Tea v2 and Lipgloss v2), TUIOS offers a vim-like modal
interface with comprehensive keyboard shortcuts, workspace support, and mouse
interaction.

<details>

<summary>Table of Contents</summary>

<!--toc:start-->

- [TUIOS - Terminal UI Operating System](#tuios-terminal-ui-operating-system)
  - [Installation](#installation)
    - [Quick Install (Linux/macOS)](#quick-install-linuxmacos)
    - [Download the latest release](#download-the-latest-release)
    - [Install using Go](#install-using-go)
    - [Run using Docker](#run-using-docker)
    - [Build from Source](#build-from-source)
    - [Prerequisites](#prerequisites)
    - [Dependencies](#dependencies)
  - [Features](#features)
    - [Core Functionality](#core-functionality)
    - [Window Management](#window-management)
    - [Advanced Features](#advanced-features)
  - [Usage](#usage)
    - [Getting Started](#getting-started)
    - [CLI Options](#cli-options)
    - [Connecting to SSH Mode](#connecting-to-ssh-mode)
    - [Keyboard Shortcuts](#keyboard-shortcuts)
      - [Window Management](#window-management)
      - [Workspace Management](#workspace-management)
      - [Window Layout (Non-Tiling Mode)](#window-layout-non-tiling-mode)
      - [Tiling Mode](#tiling-mode)
      - [Mode Switching](#mode-switching)
      - [Text Selection & Clipboard](#text-selection-clipboard)
      - [Window Navigation](#window-navigation)
      - [System Controls](#system-controls)
      - [Prefix Mode (<kbd>Ctrl</kbd>+<kbd>B</kbd>)](#prefix-mode-kbdctrlkbdkbdbkbd)
      - [Window Prefix (<kbd>Ctrl</kbd>+<kbd>B</kbd>, <kbd>t</kbd>)](#window-prefix-kbdctrlkbdkbdbkbd-kbdtkbd)
    - [Mouse Controls](#mouse-controls)
    - [Status Bar](#status-bar)
  - [Configuration](#configuration)
  - [Architecture](#architecture)
    - [Technical Stack](#technical-stack)
    - [Core Components](#core-components)
  - [Performance](#performance)
    - [Optimization Strategies](#optimization-strategies)
    - [Benchmarks](#benchmarks)
  - [Troubleshooting](#troubleshooting)
    - [Common Issues](#common-issues)
    - [Debug Mode](#debug-mode)
  - [Dependencies](#dependencies)
  - [Roadmap](#roadmap)
    - [High Priority](#high-priority)
    - [Medium Priority](#medium-priority)
    - [Low Priority](#low-priority)
  - [Local Development](#local-development)
    - [Run locally using Docker](#run-locally-using-docker)
    - [Build from source](#build-from-source)
    - [Testing](#testing)
  - [Contribution](#contribution)
    - [Development Setup](#development-setup)
    - [Creating Releases](#creating-releases)
  - [Star History](#star-history)
  - [License](#license)
  - [Acknowledgments](#acknowledgments)
  <!--toc:end-->

</details>

## Installation

You can install TUIOS using one of the following methods:

### Quick Install (Linux/macOS)

Install the latest release with a single command:

```bash
curl -fsSL https://raw.githubusercontent.com/Gaurav-Gosain/tuios/main/install.sh | bash
```

Or if you prefer to inspect the script first:

```bash
curl -fsSL https://raw.githubusercontent.com/Gaurav-Gosain/tuios/main/install.sh -o install.sh
chmod +x install.sh
./install.sh
```

The script will automatically:

- Detect your OS and architecture
- Download the appropriate binary from the latest release
- Install it to `/usr/local/bin` (or `~/bin` if no sudo access)
- Verify the installation

### Download the latest release

Grab the latest release from the
[releases page](https://github.com/Gaurav-Gosain/tuios/releases) and extract
the archive to a location of your choice.

### Install using Go

> [!NOTE]
> Prerequisite: [Go](https://go.dev/) 1.24 or later installed on your system.

You can also install TUIOS using the `go install` command:

```bash
go install github.com/Gaurav-Gosain/tuios/cmd/tuios@latest
```

### Run using Docker

You can pull the latest docker image from the
[GitHub Docker Container Registry](https://github.com/Gaurav-Gosain/tuios/pkgs/container/tuios)
and run it using the following command:

```bash
docker run -it --rm ghcr.io/gaurav-gosain/tuios:latest
```

### Build from Source

If you prefer to build from source, follow these steps:

1. Clone the repository:

   ```bash
   git clone https://github.com/gaurav-gosain/tuios.git
   ```

2. Navigate to the project directory:

   ```bash
   cd tuios
   ```

3. Build the executable:

   ```bash
   go build -o tuios ./cmd/tuios
   ```

4. Run TUIOS:

   ```bash
   ./tuios
   ```

   Or check the version:

   ```bash
   ./tuios --version
   ```

### Prerequisites

- Go 1.24 or later (for building from source or using `go install`)
- A terminal with true color support (most modern terminals)

### Core Dependencies

TUIOS is built on the following libraries:

- **[Bubble Tea v2](https://github.com/charmbracelet/bubbletea)**: Event-driven terminal UI framework
- **[Lipgloss v2](https://github.com/charmbracelet/lipgloss)**: Terminal styling and layout library
- **[xpty](https://github.com/aymanbagabas/go-pty)**: Cross-platform pseudo-terminal (PTY) interface
- **[Wish v2](https://github.com/charmbracelet/wish)**: SSH server library for remote multiplexing
- **Vendored VT**: Virtual terminal emulator (vendored from charmbracelet/x/ansi)

## Features

![TUIOS](./assets/tuios.gif)

### Core Functionality

- **Multiple Terminal Windows**: Create and manage multiple terminal sessions simultaneously
- **Workspace Support**: Organize windows across 9 independent workspaces
- **Modal Interface**: Vim-inspired design with Window Management Mode and
  Terminal Mode
- **Automatic Tiling**: Optional automatic window tiling with intelligent layouts
- **Window Minimization**: Minimize windows to dock with smooth animations
- **Real-time Updates**: 60Hz polling for responsive terminal content updates
- **Mouse Support**: Full mouse interaction for window management, dragging,
  and resizing

### Window Management

- **Dynamic Layouts**: Snap windows to screen edges, corners, or fullscreen
- **Workspace Organization**: 9 workspaces for organizing terminal sessions
- **Tiling Mode**: Automatic window arrangement with grid-based layouts
- **Drag and Drop**: Move windows by dragging with mouse (with tiling swap support)
- **Resize Support**: Right-click and drag to resize windows (disabled in tiling mode)
- **Window Minimization**: Minimize to dock with pill-style indicators
- **Z-index Management**: Automatic window layering and focus management
- **Visual Feedback**: Clear border colors indicating focus state and mode

### Advanced Features

- **Tmux-style Prefix Mode**: <kbd>Ctrl</kbd>+<kbd>B</kbd> prefix for advanced commands
- **Window Renaming**: Custom names for easy identification
- **Smart Animations**: Smooth transitions for minimize/restore/snap operations
- **CPU Monitoring**: Real-time CPU usage graph in status bar
- **Comprehensive Help System**: Built-in contextual help overlay
- **Text Selection & Copying**: Mouse and keyboard-based text selection with
  clipboard integration
- **Vim-Style Copy Mode**: Comprehensive vim keybindings for scrollback navigation,
  search, and text selection (10,000 line buffer)
- **Smart Notifications**: Responsive notification system with overflow protection
- **SSH Server Mode**: Run TUIOS as SSH server for remote terminal multiplexing
- **Session Isolation**: Each SSH connection gets dedicated TUIOS instance

## Usage

### Getting Started

**Local Mode (Default)**

Launch TUIOS from your terminal:

```bash
./tuios
```

The application starts with a welcome screen. Press <kbd>n</kbd> to create your first terminal window.

**SSH Server Mode**

Run TUIOS as an SSH server to allow remote connections:

```bash
# Start SSH server on default port 2222
./tuios --ssh

# Custom port and host
./tuios --ssh --port 2223 --host 0.0.0.0

# Specify custom SSH host key
./tuios --ssh --key-path ~/.ssh/custom_tuios_key
```

Each SSH connection gets its own isolated TUIOS instance with full terminal multiplexer capabilities.

### CLI Options

| Flag         | Description       | Default        |
| ------------ | ----------------- | -------------- |
| `--ssh`      | Run as SSH server | `false`        |
| `--port`     | SSH server port   | `2222`         |
| `--host`     | SSH server host   | `localhost`    |
| `--key-path` | SSH host key path | Auto-generated |

### Connecting to SSH Mode

```bash
# Connect to TUIOS SSH server
ssh -p 2222 localhost

# Connect from remote machine
ssh -p 2222 your-server-ip
```

### Keyboard Shortcuts

#### Window Management

| Key                             | Action                                   |
| ------------------------------- | ---------------------------------------- |
| <kbd>n</kbd>                    | Create new window                        |
| <kbd>x</kbd>                    | Close current window                     |
| <kbd>r</kbd>                    | Rename window                            |
| <kbd>m</kbd>                    | Minimize window                          |
| <kbd>Shift</kbd>+<kbd>M</kbd>   | Restore all minimized windows            |
| <kbd>Tab</kbd>                  | Focus next window                        |
| <kbd>Shift</kbd>+<kbd>Tab</kbd> | Focus previous window                    |
| <kbd>1</kbd>-<kbd>9</kbd>       | Select window by number (in tiling mode) |

#### Workspace Management

> [!NOTE]
> On macOS, use <kbd>Option</kbd> instead of <kbd>Alt</kbd>. The <kbd>Ctrl</kbd>+<kbd>B</kbd> prefix alternatives work universally and are recommended for tiling window managers like Aerospace.

| Key                                                                                    | Action                                        |
| -------------------------------------------------------------------------------------- | --------------------------------------------- |
| <kbd>Alt</kbd>+<kbd>1</kbd>-<kbd>9</kbd>                                               | Switch to workspace 1-9                       |
| <kbd>Alt</kbd>+<kbd>Shift</kbd>+<kbd>1</kbd>-<kbd>9</kbd>                              | Move window to workspace and follow           |
| <kbd>Ctrl</kbd>+<kbd>B</kbd>, <kbd>w</kbd>, <kbd>1</kbd>-<kbd>9</kbd>                  | Switch to workspace (prefix alternative)      |
| <kbd>Ctrl</kbd>+<kbd>B</kbd>, <kbd>w</kbd>, <kbd>Shift</kbd>+<kbd>1</kbd>-<kbd>9</kbd> | Move window to workspace (prefix alternative) |

#### Window Layout (Non-Tiling Mode)

| Key                       | Action                                                                  |
| ------------------------- | ----------------------------------------------------------------------- |
| <kbd>h</kbd>              | Snap window to left half                                                |
| <kbd>l</kbd>              | Snap window to right half                                               |
| <kbd>f</kbd>              | Fullscreen window                                                       |
| <kbd>u</kbd>              | Unsnap/restore window                                                   |
| <kbd>1</kbd>-<kbd>4</kbd> | Snap to corner (1=top-left, 2=top-right, 3=bottom-left, 4=bottom-right) |

#### Tiling Mode

| Key                                                          | Action                        |
| ------------------------------------------------------------ | ----------------------------- |
| <kbd>t</kbd>                                                 | Toggle automatic tiling       |
| <kbd>Shift</kbd>+<kbd>H</kbd> / <kbd>Ctrl</kbd>+<kbd>←</kbd> | Swap with window to the left  |
| <kbd>Shift</kbd>+<kbd>L</kbd> / <kbd>Ctrl</kbd>+<kbd>→</kbd> | Swap with window to the right |
| <kbd>Shift</kbd>+<kbd>K</kbd> / <kbd>Ctrl</kbd>+<kbd>↑</kbd> | Swap with window above        |
| <kbd>Shift</kbd>+<kbd>J</kbd> / <kbd>Ctrl</kbd>+<kbd>↓</kbd> | Swap with window below        |

#### Mode Switching

| Key                                        | Action              |
| ------------------------------------------ | ------------------- |
| <kbd>i</kbd>, <kbd>Enter</kbd>             | Enter Terminal Mode |
| <kbd>?</kbd>                               | Toggle help overlay (close with Esc) |
| <kbd>q</kbd>, <kbd>Ctrl</kbd>+<kbd>C</kbd> | Quit application (q closes help if showing) |

#### Text Selection & Clipboard

| Key                                    | Action                                        |
| -------------------------------------- | --------------------------------------------- |
| <kbd>s</kbd>                           | Toggle selection mode                         |
| <kbd>Ctrl</kbd>+<kbd>S</kbd>           | Toggle selection mode from terminal mode      |
| <kbd>Mouse drag</kbd>                  | Select text (mouse-based)                     |
| <kbd>Arrow keys</kbd>                  | Move cursor in selection mode                 |
| <kbd>Shift</kbd>+<kbd>Arrow keys</kbd> | Extend selection                              |
| <kbd>c</kbd>                           | Copy selected text to clipboard               |
| <kbd>Ctrl</kbd>+<kbd>V</kbd>           | Paste from clipboard (works in terminal mode) |
| <kbd>Esc</kbd>                         | Clear current selection                       |

#### Copy Mode (Vim-Style Scrollback Navigation)

Enter copy mode with <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>[</kbd> to navigate and select text using vim keybindings.

**Basic Navigation**

| Key                                      | Action                                   |
| ---------------------------------------- | ---------------------------------------- |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>[</kbd> | Enter copy mode                          |
| <kbd>h</kbd> / <kbd>j</kbd> / <kbd>k</kbd> / <kbd>l</kbd> | Move cursor left/down/up/right |
| <kbd>w</kbd> / <kbd>b</kbd> / <kbd>e</kbd> | Word forward / word backward / word end |
| <kbd>0</kbd> / <kbd>^</kbd>              | Start of line / first non-blank character |
| <kbd>$</kbd>                             | End of line                              |
| <kbd>g</kbd><kbd>g</kbd>                 | Jump to top of scrollback                |
| <kbd>G</kbd>                             | Jump to bottom (live output)             |
| <kbd>{number}</kbd><kbd>G</kbd>          | Jump to specific line number (e.g., 10G goes to line 10) |
| <kbd>{</kbd> / <kbd>}</kbd>              | Jump to previous/next paragraph          |
| <kbd>Ctrl</kbd>+<kbd>U</kbd> / <kbd>Ctrl</kbd>+<kbd>D</kbd> | Half page up/down |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> / <kbd>Ctrl</kbd>+<kbd>F</kbd> | Full page up/down |

**Count Prefix**

Most motion commands can be prefixed with a count to repeat them (vim-style):
- <kbd>10</kbd><kbd>j</kbd> - Move down 10 lines
- <kbd>5</kbd><kbd>w</kbd> - Move forward 5 words
- <kbd>3</kbd><kbd>{</kbd> - Jump up 3 paragraphs
- <kbd>2</kbd><kbd>n</kbd> - Jump to 2nd next search match

**Character Search**

| Key                          | Action                                            |
| ---------------------------- | ------------------------------------------------- |
| <kbd>f</kbd>+char            | Find next occurrence of char on line (forward)    |
| <kbd>F</kbd>+char            | Find previous occurrence of char on line (backward)|
| <kbd>t</kbd>+char            | Till next char (cursor before char)               |
| <kbd>T</kbd>+char            | Till previous char (cursor after char)            |
| <kbd>;</kbd>                 | Repeat last character search                      |
| <kbd>,</kbd>                 | Repeat last character search (opposite direction) |

**Search**

| Key                          | Action                                          |
| ---------------------------- | ----------------------------------------------- |
| <kbd>/</kbd>                 | Search forward (type query, press Enter)        |
| <kbd>?</kbd>                 | Search backward (type query, press Enter)       |
| <kbd>n</kbd>                 | Next match in search direction (down for /, up for ?) |
| <kbd>N</kbd>                 | Previous match (opposite direction)             |
| <kbd>Ctrl</kbd>+<kbd>L</kbd> | Clear search highlights                         |

**Visual Selection**

| Key                          | Action                                   |
| ---------------------------- | ---------------------------------------- |
| <kbd>v</kbd>                 | Enter visual mode (character-wise)       |
| <kbd>V</kbd>                 | Enter visual line mode                   |
| <kbd>y</kbd>                 | Yank (copy) selection to clipboard       |
| <kbd>Esc</kbd>               | Exit visual mode                         |

**Other Commands**

| Key                          | Action                                   |
| ---------------------------- | ---------------------------------------- |
| <kbd>%</kbd>                 | Jump to matching bracket                 |
| <kbd>i</kbd>                 | Return to terminal mode                  |
| <kbd>q</kbd>                 | Exit copy mode                           |

#### Window Navigation

| Key                                       | Action                          |
| ----------------------------------------- | ------------------------------- |
| <kbd>Ctrl</kbd>+<kbd>↑</kbd>/<kbd>↓</kbd> | Swap windows or maximize/unsnap |

#### System Controls

| Key                          | Action            |
| ---------------------------- | ----------------- |
| <kbd>Ctrl</kbd>+<kbd>L</kbd> | Toggle log viewer |

#### Prefix Mode (<kbd>Ctrl</kbd>+<kbd>B</kbd>)

Similar to tmux, TUIOS supports a prefix key for advanced commands. Press <kbd>Ctrl</kbd>+<kbd>B</kbd>, release, then press the command key:

| Key Sequence                                                                         | Action                                      |
| ------------------------------------------------------------------------------------ | ------------------------------------------- |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>c</kbd>                                            | Create new window                           |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>x</kbd>                                            | Close current window                        |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>,</kbd> / <kbd>r</kbd>                             | Rename window                               |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>n</kbd> / <kbd>Tab</kbd>                           | Next window                                 |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>p</kbd> / <kbd>Shift</kbd>+<kbd>Tab</kbd>          | Previous window                             |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>0</kbd>-<kbd>9</kbd>                               | Jump to window                              |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>Space</kbd>                                        | Toggle tiling mode                          |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>w</kbd> <kbd>1</kbd>-<kbd>9</kbd>                  | Switch to workspace (universal alternative) |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>w</kbd> <kbd>Shift</kbd>+<kbd>1</kbd>-<kbd>9</kbd> | Move window to workspace                    |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>m</kbd> <kbd>m</kbd>                               | Minimize focused window                     |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>m</kbd> <kbd>1</kbd>-<kbd>9</kbd>                  | Restore minimized window by number          |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>m</kbd> <kbd>Shift</kbd>+<kbd>M</kbd>              | Restore all minimized windows               |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>[</kbd>                                            | Enter scrollback mode                       |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>t</kbd> ...                                        | Window prefix (see below)                   |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>d</kbd> / <kbd>Esc</kbd>                           | Detach from terminal (exit terminal mode)   |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>s</kbd>                                            | Toggle selection mode                       |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>q</kbd>                                            | Quit application                            |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>Ctrl</kbd>+<kbd>B</kbd>                            | Send literal Ctrl+B to terminal             |

#### Window Prefix (<kbd>Ctrl</kbd>+<kbd>B</kbd>, <kbd>t</kbd>)

For users who prefer prefix-based controls matching normal mode keybinds, the window prefix provides an alternative way to access common window management commands:

| Key Sequence                                                              | Action             |
| ------------------------------------------------------------------------- | ------------------ |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>t</kbd> <kbd>n</kbd>                    | Create new window  |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>t</kbd> <kbd>x</kbd>                    | Close window       |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>t</kbd> <kbd>r</kbd>                    | Rename window      |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>t</kbd> <kbd>Tab</kbd>                  | Next window        |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>t</kbd> <kbd>Shift</kbd>+<kbd>Tab</kbd> | Previous window    |
| <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>t</kbd> <kbd>t</kbd>                    | Toggle tiling mode |

### Mouse Controls

- **Left Click**: Focus window
- **Left Drag**: Move window (swap in tiling mode) or select text (in selection mode)
- **Right Drag**: Resize window (disabled in tiling mode)
- **Window Controls**: Click minimize, maximize, or close buttons in title bar
- **Dock Click**: Click minimized windows in dock to restore

### Status Bar

The bottom status bar displays:

- **Mode Indicator**: `[W]` for Window Mode, `[T]` for Terminal Mode, `[T]` for Tiling
- **Workspace Indicator**: Shows current workspace and windows per workspace
- **Minimized Windows**: Pill-style dock items with custom names
- **CPU Graph**: Real-time CPU usage visualization

## Configuration

TUIOS uses sensible defaults but can be customized through code constants:

- **Window Defaults**: 20x5 characters for new windows
- **Animation Speed**: 300ms standard duration
- **Refresh Rate**: 60 FPS normal, 30 FPS during interactions
- **Buffer Sizes**: 100 log messages, efficient terminal rendering

## Architecture

TUIOS follows a layered architecture built on the Model-View-Update (MVU) pattern provided by Bubble Tea.

### System Architecture

```mermaid
graph TB
    subgraph "User Interface Layer"
        UI[Terminal Display]
        Input[Keyboard & Mouse Input]
    end

    subgraph "Application Layer"
        OS[OS - Window Manager]
        IH[Input Handler]
        WS[Workspace Manager 1-9]
        ANI[Animation System]
    end

    subgraph "Window Management"
        WIN[Terminal Windows]
        LAYOUT[Layout System]
        TILE[Tiling Manager]
    end

    subgraph "Terminal Emulation"
        VT[VT Emulator]
        PTY[PTY Interface]
        SCROLL[Scrollback Buffer]
    end

    subgraph "Rendering Pipeline"
        RENDER[Rendering Engine]
        CACHE[Style Cache]
        POOL[Object Pools]
    end

    subgraph "External Integration"
        SSH[SSH Server Wish]
        SHELL[Shell Process]
    end

    Input --> IH
    IH --> OS
    OS --> WS
    OS --> WIN
    OS --> ANI
    WIN --> VT
    WIN --> LAYOUT
    LAYOUT --> TILE
    VT --> PTY
    VT --> SCROLL
    PTY --> SHELL
    OS --> RENDER
    RENDER --> CACHE
    RENDER --> POOL
    RENDER --> UI
    SSH --> OS

    style OS fill:#1d3557
    style VT fill:#2d6a4f
    style RENDER fill:#9d0208
    style SSH fill:#457b9d
```

### Data Flow Architecture

```mermaid
sequenceDiagram
    participant User
    participant Input
    participant OS as Window Manager
    participant Window
    participant VT as VT Emulator
    participant PTY
    participant Shell
    participant Render

    User->>Input: Keyboard/Mouse Event
    Input->>OS: Route Event

    alt Terminal Mode
        OS->>Window: Forward to Active Window
        Window->>PTY: Write to stdin
        PTY->>Shell: Execute Command
        Shell-->>PTY: Output (ANSI)
        PTY-->>VT: Parse ANSI Stream
        VT-->>Window: Update Screen Buffer
        Window-->>OS: Mark Content Dirty
    else Window Management Mode
        OS->>OS: Process WM Command
        OS->>Window: Create/Close/Focus/Snap
    end

    OS->>Render: Generate View
    Render->>Render: Cull Off-screen
    Render->>Render: Apply Style Cache
    Render->>Render: Compose Layers
    Render-->>User: Display ANSI Output
```

### Terminal Emulation Stack

```mermaid
graph LR
    subgraph "Shell Process"
        SHELL[Shell stdout/stderr]
    end

    subgraph "PTY Layer"
        PTY[Pseudo Terminal]
    end

    subgraph "VT Emulator"
        PARSER[ANSI Parser]
        STATE[State Machine]
        SCREEN[Screen Buffer]
        ALT[Alternate Screen]
        SCROLL[Scrollback 10k lines]
    end

    subgraph "Window Layer"
        CACHE[Content Cache]
        SEL[Selection State]
    end

    subgraph "Rendering"
        STYLE[Style Application]
        LAYER[Layer Composition]
    end

    SHELL -->|ANSI Codes| PTY
    PTY -->|Raw Bytes| PARSER
    PARSER -->|Control Sequences| STATE
    STATE -->|Updates| SCREEN
    STATE -.->|TUI Apps| ALT
    SCREEN -->|Overflow| SCROLL
    SCREEN --> CACHE
    CACHE --> SEL
    SEL --> STYLE
    STYLE --> LAYER

    style PARSER fill:#457b9d
    style SCREEN fill:#2d6a4f
    style CACHE fill:#9d0208
```

### Rendering Pipeline

```mermaid
graph TD
    START[OS.View Called] --> CULL[Viewport Culling]
    CULL -->|Visible Windows| COMP[Layer Composition]
    CULL -->|Skip| OFF[Off-screen Windows]

    COMP --> CHECK{Content Dirty?}
    CHECK -->|Yes| BUILD[Build Cell Content]
    CHECK -->|No| REUSE[Reuse Cached Layer]

    BUILD --> BORDER[Add Window Borders]
    BORDER --> STYLE[Apply Styles]
    STYLE -->|Cache Lookup| CACHE{Style in Cache?}
    CACHE -->|Hit| APPLY[Apply Cached Style]
    CACHE -->|Miss| CREATE[Create & Cache Style]
    CREATE --> APPLY
    APPLY --> STACK[Stack by Z-Index]
    REUSE --> STACK

    STACK --> OVERLAY[Add Overlays]
    OVERLAY --> DOCK[Dock Minimized Windows]
    DOCK --> STATUS[Status Bar]
    STATUS --> NOTIF[Notifications]
    NOTIF --> ANSI[Generate ANSI Codes]
    ANSI --> OUTPUT[Return to Bubble Tea]

    style CACHE fill:#7209b7
    style APPLY fill:#2d6a4f
    style ANSI fill:#9d0208
```

### SSH Server Architecture

```mermaid
graph TB
    subgraph "SSH Clients"
        C1[SSH Client 1]
        C2[SSH Client 2]
        C3[SSH Client N]
    end

    subgraph "TUIOS SSH Server :2222"
        WISH[Wish v2 Middleware]
        AUTH[Session Handler]
    end

    subgraph "Isolated Instances"
        OS1[OS Instance 1]
        OS2[OS Instance 2]
        OS3[OS Instance N]
    end

    subgraph "Terminal Sessions"
        W1[Windows + PTY + Shell]
        W2[Windows + PTY + Shell]
        W3[Windows + PTY + Shell]
    end

    C1 -->|SSH Connection| WISH
    C2 -->|SSH Connection| WISH
    C3 -->|SSH Connection| WISH

    WISH --> AUTH
    AUTH -->|Dedicated Context| OS1
    AUTH -->|Dedicated Context| OS2
    AUTH -->|Dedicated Context| OS3

    OS1 --> W1
    OS2 --> W2
    OS3 --> W3

    style WISH fill:#457b9d
    style OS1 fill:#1d3557
    style OS2 fill:#1d3557
    style OS3 fill:#1d3557
```

### Core Components

| Component | File | Purpose |
|-----------|------|---------|
| **Window Manager** | `app/os.go` | Central state management, workspace orchestration, mode handling |
| **Terminal Windows** | `terminal/window.go` | PTY lifecycle, VT emulator integration, content caching |
| **Input Handler** | `input/handler.go` | Modal routing, prefix commands, keyboard/mouse processing |
| **VT Emulator** | `vt/emulator.go` | ANSI parsing, screen buffer management, scrollback |
| **Rendering Engine** | `app/render.go` | Layer composition, viewport culling, ANSI generation |
| **Layout System** | `layout/tiling.go` | Grid calculations, window positioning, tiling algorithms |
| **SSH Server** | `server/server.go` | Wish middleware, per-session isolation |
| **Style Cache** | `app/stylecache.go` | Lipgloss style pooling, 40-60% allocation reduction |
| **Object Pools** | `pool/pool.go` | String/byte/layer pooling, GC pressure reduction |

## Performance

### Optimization Strategies

- **Smart Caching**: Terminal content cached with sequence-based change detection
- **Enhanced Viewport Culling**: Aggressive off-screen window culling with animation-aware margins
- **Adaptive Refresh**: Dynamic FPS based on activity (60Hz normal, 30Hz during interactions)
- **Expanded Memory Pooling**: Object pools for strings, byte buffers, layers, and styles
- **Intelligent Updates**: Background windows throttled to ~20Hz vs 60Hz for focused windows
- **Throttled Background Updates**: Focused windows update at 60Hz, background windows at ~20Hz
- **Frame Skipping**: Skip rendering when no changes and no active animations

### Benchmarks

| Operation        | Performance     |
| ---------------- | --------------- |
| Window Creation  | < 50ms          |
| Window Switch    | < 1ms           |
| Workspace Switch | < 5ms           |
| Full Render      | < 16ms (60 FPS) |

## Troubleshooting

### Common Issues

**Window not closing with exit/Ctrl+D**

- Fixed with improved process exit detection and context-based goroutine cleanup
- Windows now properly detect shell termination and clean up resources

**Performance issues with many windows**

- Enable tiling mode for better performance
- Background windows automatically throttle updates to ~20Hz
- Enhanced memory pooling reduces allocation overhead
- Smart caching prevents unnecessary terminal re-renders

**Resource leaks or high memory usage**

- Fixed with proper goroutine lifecycle management using context cancellation
- Enhanced I/O operation cleanup prevents hanging processes
- Object pooling reduces garbage collection pressure

**Mouse not working**

- Ensure your terminal supports mouse events
- Try running with `--mouse` flag if available

**Colors not displaying correctly**

- Verify terminal supports true color (24-bit)
- Set `COLORTERM=truecolor` environment variable

**Text selection not working**

- Ensure you're in selection mode (press `s` first)
- Use mouse drag or arrow keys to select text
- Text remains highlighted until copied or cleared with Escape

**Clipboard copying fails**

- Ensure your system has clipboard support
- Try alternative copy method if primary clipboard isn't available
- Selected text must be highlighted before copying with `c`

**SSH connection issues**

- Ensure TUIOS SSH server is running: `./tuios --ssh`
- Check firewall settings allow connections on the specified port
- SSH host key will be auto-generated on first run in `~/.ssh/tuios_host_key`
- For remote access, use `--host 0.0.0.0` to bind to all interfaces

**SSH authentication problems**

- TUIOS SSH server accepts any SSH connection (no authentication required)
- This is intended for development/local use - add authentication for production
- SSH keys are handled by the client's SSH configuration

### Debug Mode

Press <kbd>Ctrl</kbd>+<kbd>L</kbd> to open the log viewer and see system messages.

## Roadmap

### Implemented Features ✓

TUIOS has successfully implemented:
- Multiple terminal windows with workspace management (9 workspaces)
- Automatic tiling and window snapping
- Full mouse and keyboard control
- SSH server mode for remote access
- Text selection with clipboard integration
- Scrollback buffers (10,000 lines) with vim-style copy mode navigation
- Real-time performance optimizations (style caching, viewport culling, object pooling)
- Search within scrollback history with match highlighting
- Vim keybindings for text selection and copying

### Planned Features

#### High Priority

- [x] **Search Functionality**
  - [x] Search within scrollback history
  - [x] Search and select in terminal output
  - [x] Incremental search with highlighting
- [ ] **Configuration System**
  - [ ] Custom key bindings
  - [ ] Theme and color customization
  - [ ] Configuration file support (YAML/TOML)
  - [ ] Performance tuning options

#### Medium Priority

- [ ] **Session Management**
  - [ ] Workspace layouts persistence
  - [ ] Window state restoration
  - [ ] Session save/restore
- [ ] **Advanced Selection**
  - [ ] Block/column selection mode
  - [ ] Regular expression search
- [ ] **Terminal Features**
  - [ ] Split panes within windows
  - [ ] Terminal tabs
  - [ ] Bell notifications

#### Future Considerations

- [ ] **Plugin System**: Extension support for custom widgets and themes
- [ ] **SSH Enhancements**: Authentication, session sharing, tunneling
- [ ] **Accessibility**: Screen reader support, high contrast themes

## Local Development

### Testing

Run the test suite:

```bash
go test ./...
```

For building from source or running with Docker, see the [Installation](#installation) section.

## Contribution

Contributions are welcome! Whether you want to add new features,
fix bugs, or improve documentation, feel free to open a pull request.

[![ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/B0B81N8V1R)

### Development Setup

```bash
git clone https://github.com/gaurav-gosain/tuios.git
cd tuios
go mod tidy
go build -o tuios ./cmd/tuios
```

### Creating Releases

TUIOS uses [GoReleaser](https://goreleaser.com/) for automated releases. To create a new release:

1. Tag the commit with a semantic version:

   ```bash
   git tag -a v1.0.0 -m "Release v1.0.0"
   ```

2. Push the tag to trigger the release workflow:
   ```bash
   git push origin v1.0.0
   ```

The GitHub Actions workflow will automatically:

- Build binaries for multiple platforms (Linux, macOS, Windows, FreeBSD, OpenBSD)
- Create archives with checksums
- Generate a changelog
- Publish the release to GitHub

You can test the release configuration locally (requires [goreleaser](https://goreleaser.com/install/) installed):

```bash
goreleaser release --snapshot --clean
```

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
