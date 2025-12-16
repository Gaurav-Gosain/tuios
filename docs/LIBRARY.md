# Using TUIOS as a Library

TUIOS can be imported and used as a library in your own Go applications. This allows you to embed a full-featured terminal window manager in your Bubble Tea applications.

## Installation

```bash
go get github.com/Gaurav-Gosain/tuios/pkg/tuios
```

## Quick Start

### Basic Usage

```go
package main

import (
    "log"

    "github.com/Gaurav-Gosain/tuios/pkg/tuios"
    tea "github.com/charmbracelet/bubbletea/v2"
)

func main() {
    // Create a new TUIOS instance with default options
    model := tuios.New()

    // Create the Bubble Tea program with recommended options
    p := tea.NewProgram(model, tuios.ProgramOptions()...)

    // Run the program
    if _, err := p.Run(); err != nil {
        log.Fatal(err)
    }
}
```

### With Custom Options

```go
model := tuios.New(
    tuios.WithTheme("dracula"),
    tuios.WithShowKeys(true),
    tuios.WithAnimations(false),
    tuios.WithWorkspaces(9),
    tuios.WithBorderStyle("rounded"),
    tuios.WithDockbarPosition("bottom"),
    tuios.WithScrollbackLines(20000),
)
```

### With Mouse Motion Filtering

For better performance, use the provided mouse motion filter:

```go
p := tea.NewProgram(
    model,
    tea.WithFPS(60),
    tea.WithFilter(tuios.FilterMouseMotion),
)
```

## Options Reference

### WithTheme(name string)

Set the color theme. Available themes include "dracula", "nord", "tokyonight", and 300+ others from bubbletint.

```go
tuios.WithTheme("dracula")
```

### WithShowKeys(enabled bool)

Enable the showkeys overlay to display pressed keys (useful for demos).

```go
tuios.WithShowKeys(true)
```

### WithAnimations(enabled bool)

Enable or disable window animations. When disabled, windows snap instantly.

```go
tuios.WithAnimations(false)
```

### WithASCIIOnly(enabled bool)

Use ASCII characters instead of Nerd Font icons for compatibility.

```go
tuios.WithASCIIOnly(true)
```

### WithWorkspaces(n int)

Set the number of workspaces (1-9).

```go
tuios.WithWorkspaces(4)
```

### WithBorderStyle(style string)

Set the window border style. Valid values:
- `"rounded"` (default)
- `"normal"`
- `"thick"`
- `"double"`
- `"hidden"`
- `"block"`
- `"ascii"`

```go
tuios.WithBorderStyle("thick")
```

### WithDockbarPosition(position string)

Set the dockbar position. Valid values:
- `"bottom"` (default)
- `"top"`
- `"hidden"`

```go
tuios.WithDockbarPosition("top")
```

### WithHideWindowButtons(hide bool)

Hide the minimize/maximize/close buttons in window title bars.

```go
tuios.WithHideWindowButtons(true)
```

### WithScrollbackLines(lines int)

Set the scrollback buffer size (100-1000000).

```go
tuios.WithScrollbackLines(50000)
```

### WithSize(width, height int)

Set the initial terminal size. Usually not needed as TUIOS auto-detects.

```go
tuios.WithSize(120, 40)
```

### WithSSHMode(enabled bool)

Enable SSH mode for running over SSH connections.

```go
tuios.WithSSHMode(true)
```

### WithUserConfig(cfg *config.UserConfig)

Provide a custom user configuration instead of loading from file.

```go
cfg := tuios.Config.DefaultConfig()
cfg.Keybindings.LeaderKey = "ctrl+a"
tuios.WithUserConfig(cfg)
```

## Web Terminal Integration

TUIOS can be served through the browser using the [sip library](https://github.com/Gaurav-Gosain/sip):

```go
package main

import (
    "context"
    "log"

    "github.com/Gaurav-Gosain/sip"
    "github.com/Gaurav-Gosain/tuios/pkg/tuios"
    tea "github.com/charmbracelet/bubbletea/v2"
)

func main() {
    server := sip.NewServer(sip.DefaultConfig())

    err := server.Serve(context.Background(), func(sess sip.Session) (tea.Model, []tea.ProgramOption) {
        pty := sess.Pty()

        // Create TUIOS for the web session
        model := tuios.New(
            tuios.WithSize(pty.Width, pty.Height),
            tuios.WithTheme("dracula"),
        )

        return model, tuios.ProgramOptions()
    })

    if err != nil {
        log.Fatal(err)
    }
}
```

## SSH Server Integration

For SSH server integration, use the Wish library:

```go
package main

import (
    "context"

    "github.com/Gaurav-Gosain/tuios/pkg/tuios"
    tea "github.com/charmbracelet/bubbletea/v2"
    "github.com/charmbracelet/wish/v2"
    "github.com/charmbracelet/wish/v2/bubbletea"
)

func main() {
    s, _ := wish.NewServer(
        wish.WithAddress(":2222"),
        wish.WithMiddleware(
            bubbletea.Middleware(func(sess ssh.Session) (tea.Model, []tea.ProgramOption) {
                pty, _, _ := sess.Pty()

                model := tuios.New(
                    tuios.WithSize(pty.Window.Width, pty.Window.Height),
                    tuios.WithSSHMode(true),
                )

                return model, tuios.ProgramOptions()
            }),
        ),
    )

    s.ListenAndServe()
}
```

## Configuration Access

The `tuios.Config` struct provides access to configuration utilities:

```go
// Load user config from file
cfg, err := tuios.Config.LoadUserConfig()

// Get default config
cfg := tuios.Config.DefaultConfig()

// Get config file path
path, err := tuios.Config.GetConfigPath()
```

## Model Methods

The TUIOS model provides several public methods:

### Window Management

- `AddWindow(title string)` - Create a new terminal window
- `DeleteWindow(i int)` - Close window at index
- `FocusWindow(i int)` - Focus window at index
- `GetFocusedWindow()` - Get the currently focused window

### Workspace Management

- `SwitchWorkspace(n int)` - Switch to workspace n (1-9)
- `MoveWindowToWorkspace(windowIndex, workspace int)` - Move a window

### Layout

- `ToggleTiling()` - Toggle automatic tiling mode
- `TileAllWindows()` - Retile all windows

### Cleanup

- `Cleanup()` - Clean up resources (call when done)

## Example: Custom Wrapper

You can wrap TUIOS in your own model for additional functionality:

```go
type MyApp struct {
    tuios *tuios.Model
    // your additional state
}

func (m *MyApp) Init() tea.Cmd {
    return m.tuios.Init()
}

func (m *MyApp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    // Handle your custom messages first
    switch msg := msg.(type) {
    case myCustomMsg:
        // handle it
        return m, nil
    }

    // Delegate to TUIOS
    updated, cmd := m.tuios.Update(msg)
    m.tuios = updated.(*tuios.Model)
    return m, cmd
}

func (m *MyApp) View() string {
    return m.tuios.View()
}
```

## Related Documentation

- [Architecture](ARCHITECTURE.md) - Technical architecture
- [Keybindings](KEYBINDINGS.md) - Keyboard shortcuts
- [Configuration](CONFIGURATION.md) - Config file options
- [Web Terminal](WEB.md) - Browser-based access
- [Sip Library](SIP_LIBRARY.md) - Web serving library
