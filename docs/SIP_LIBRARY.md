# Sip - Serve Bubble Tea Apps in the Browser

> **Repository:** https://github.com/Gaurav-Gosain/sip  
> **Status:** Released (v0.1.7+)  
> **Tagline:** "Drinking tea through the browser"

## Overview

Sip is a Go library that serves any Bubble Tea application through a web browser. It provides the web terminal infrastructure that powers `tuios-web`, extracted into a reusable package for the entire Bubble Tea ecosystem.

## Installation

```bash
go get github.com/Gaurav-Gosain/sip@latest
```

## Quick Start

```go
package main

import (
    "context"
    
    tea "github.com/charmbracelet/bubbletea/v2"
    "github.com/Gaurav-Gosain/sip"
)

// Your Bubble Tea model
type model struct {
    count int
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyPressMsg:
        switch msg.String() {
        case "q":
            return m, tea.Quit
        case " ":
            m.count++
        }
    }
    return m, nil
}

func (m model) View() string {
    return fmt.Sprintf("Count: %d\n\nPress SPACE to increment, q to quit", m.count)
}

func main() {
    server := sip.NewServer(sip.Config{
        Host: "localhost",
        Port: "7681",
    })
    
    // Serve your app - handler is called for each browser connection
    server.Serve(context.Background(), func(sess sip.Session) (tea.Model, []tea.ProgramOption) {
        pty := sess.Pty()
        return model{}, []tea.ProgramOption{
            tea.WithAltScreen(),
        }
    })
}
```

## Features

### Server Features
- **Dual Transport**: WebTransport (HTTP/3 over QUIC) with WebSocket fallback
- **Self-signed TLS**: Automatic certificate generation for development
- **Connection Limits**: Configurable max connections
- **Read-only Mode**: Disable input for view-only deployments
- **Structured Logging**: Via charmbracelet/log

### Client Features (Bundled)
- **xterm.js**: Full terminal emulation
- **WebGL Rendering**: Hardware-accelerated 60fps rendering
- **JetBrains Mono Nerd Font**: Bundled for icon support
- **Settings Panel**: Transport, renderer, and font size preferences
- **Mouse Optimization**: Cell-based deduplication (80-95% traffic reduction)
- **Auto-reconnect**: Exponential backoff on disconnect

## API Reference

### Types

```go
// Handler creates a model for each new browser session
type Handler func(sess Session) (tea.Model, []tea.ProgramOption)

// ProgramHandler for more control over tea.Program creation
type ProgramHandler func(sess Session) *tea.Program

// Session provides access to terminal info and I/O
type Session interface {
    Pty() Pty                         // Terminal dimensions
    Context() context.Context         // Session lifecycle
    Read(p []byte) (n int, err error) // Read input
    Write(p []byte) (n int, err error)// Write output
    Fd() uintptr                      // File descriptor for TTY detection
    PtySlave() *os.File               // Underlying PTY for raw mode
    WindowChanges() <-chan WindowSize // Resize events
}

// Config for server setup
type Config struct {
    Host           string        // Default: "localhost"
    Port           string        // Default: "7681"
    ReadOnly       bool          // Disable input
    MaxConnections int           // 0 = unlimited
    IdleTimeout    time.Duration // 0 = no timeout
    AllowOrigins   []string      // CORS origins
    TLSCert        string        // Custom TLS cert path
    TLSKey         string        // Custom TLS key path
    Debug          bool          // Verbose logging
}
```

### Functions

```go
// Create a new server
func NewServer(config Config) *Server

// Serve with simple handler
func (s *Server) Serve(ctx context.Context, handler Handler) error

// Serve with custom program handler
func (s *Server) ServeWithProgram(ctx context.Context, handler ProgramHandler) error

// Get default options for tea.NewProgram (sets up PTY I/O correctly)
func MakeOptions(sess Session) []tea.ProgramOption
```

## Usage in TUIOS

`tuios-web` uses sip internally:

```go
// From cmd/tuios-web/main.go
server := sip.NewServer(sip.Config{
    Host: webHost,
    Port: webPort,
    Debug: debugMode,
})

server.Serve(ctx, func(sess sip.Session) (tea.Model, []tea.ProgramOption) {
    pty := sess.Pty()
    tuiosInstance := &app.OS{
        Width:  pty.Width,
        Height: pty.Height,
        // ... other fields
    }
    return tuiosInstance, []tea.ProgramOption{
        tea.WithFPS(60),
    }
})
```

## Important: Color Support

When running a web server, `os.Stdout` is not a TTY, so lipgloss defaults to no colors. You must force TrueColor before creating any styles:

```go
import (
    "github.com/charmbracelet/colorprofile"
    "github.com/charmbracelet/lipgloss/v2"
)

func main() {
    // MUST be called before any lipgloss.NewStyle()
    lipgloss.Writer.Profile = colorprofile.TrueColor
    
    // Now start server...
}
```

## Architecture

```
┌─────────────────────────────────────────┐
│  Your Bubble Tea App                    │
│  (implements tea.Model)                 │
└────────────────┬────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────┐
│         Sip Library                     │
├─────────────────────────────────────────┤
│  • HTTP Server (embedded static files)  │
│  • WebTransport Server (QUIC/UDP)       │
│  • WebSocket Server (fallback)          │
│  • PTY management (xpty)                │
│  • Session lifecycle                    │
└────────────────┬────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────┐
│         Browser Client                  │
├─────────────────────────────────────────┤
│  • xterm.js terminal emulator           │
│  • WebGL/Canvas/DOM renderer            │
│  • WebTransport or WebSocket            │
│  • Settings panel                       │
└─────────────────────────────────────────┘
```

## Use Cases

### 1. Interactive Demos
```go
// Live demo of your TUI on your docs site
server := sip.NewServer(sip.Config{
    Port:     "8080",
    ReadOnly: true,  // Users can only view
    MaxConnections: 100,
})
server.Serve(ctx, demoHandler)
```

### 2. Remote Tools
```go
// Access your monitoring TUI from anywhere
server := sip.NewServer(sip.Config{
    Host: "0.0.0.0",  // Listen on all interfaces
    Port: "7681",
})
server.Serve(ctx, monitorHandler)
```

### 3. Collaborative Apps
```go
// Shared terminal experience
server.Serve(ctx, func(sess sip.Session) (tea.Model, []tea.ProgramOption) {
    return sharedModel, nil  // Same model for all connections
})
```

## Related Projects

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - TUI framework for Go
- [Wish](https://github.com/charmbracelet/wish) - SSH server for Bubble Tea apps
- [xterm.js](https://xtermjs.org/) - Terminal emulator for browsers
- [TUIOS](https://github.com/Gaurav-Gosain/tuios) - Terminal window manager using sip

## License

MIT
