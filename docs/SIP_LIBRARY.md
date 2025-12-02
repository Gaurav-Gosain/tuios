# Sip - Drinking Tea Through the Browser

> **Repository:** https://github.com/Gaurav-Gosain/sip  
> **Status:** Planning Phase (v0.1.0)  
> **Tagline:** "Drinking tea through the browser"

## Vision

Sip is a planned library that will abstract the web terminal functionality from tuios-web into a standalone, reusable package for the Bubble Tea ecosystem. It will allow any Bubble Tea application to be served through a web browser with minimal code changes.

## Motivation

The web terminal implementation in `tuios-web` provides:
- Full terminal emulation via xterm.js
- WebGL-accelerated rendering for 60fps
- Dual transport (WebTransport/WebSocket)
- Bundled Nerd Font support
- Settings panel for customization
- Mouse event optimization

This functionality should be available to **all** Bubble Tea developers, not just TUIOS users.

## Proposed API

```go
package main

import (
    tea "github.com/charmbracelet/bubbletea/v2"
    "github.com/your-org/sip"
)

func main() {
    // Your existing Bubble Tea app
    myApp := NewMyBubbleTeaApp()
    
    // Option 1: Simple wrapper
    sip.Serve(myApp, sip.Config{
        Host: "localhost",
        Port: "8080",
    })
    
    // Option 2: Explicit control
    server := sip.NewServer(sip.Config{
        Host:           "localhost",
        Port:           "8080",
        ReadOnly:       false,
        MaxConnections: 100,
    })
    
    server.ServeApp(func() tea.Model {
        return NewMyBubbleTeaApp()
    })
}
```

## Features

### Core Functionality
- ✅ xterm.js integration for terminal emulation
- ✅ WebGL rendering via xterm-addon-webgl
- ✅ WebTransport (QUIC) support with WebSocket fallback
- ✅ Bundled JetBrains Mono Nerd Font
- ✅ Settings panel (transport, renderer, font size)
- ✅ Cell-based mouse event deduplication
- ✅ Automatic reconnection with exponential backoff
- ✅ Self-signed TLS certificate generation

### Server Features
- ✅ Pure Go (no CGO dependencies)
- ✅ Embedded static assets (go:embed)
- ✅ Configurable read-only mode
- ✅ Connection limits
- ✅ Graceful shutdown
- ✅ Structured logging

### Client Features
- ✅ Responsive settings panel
- ✅ Transport selection (Auto/WebTransport/WebSocket)
- ✅ Renderer selection (Auto/WebGL/Canvas/DOM)
- ✅ Font size adjustment (10-24px)
- ✅ localStorage preferences persistence

## Architecture

```
┌─────────────────────────────────────────┐
│  Developer's Bubble Tea App             │
│  (implements tea.Model)                 │
└────────────────┬────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────┐
│         Sip Library                     │
├─────────────────────────────────────────┤
│  • HTTP Server (static files)           │
│  • WebTransport Server (QUIC)           │
│  • WebSocket Server                     │
│  • Session Manager                      │
│  • PTY → xterm.js adapter               │
└────────────────┬────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────┐
│         Browser (Client)                │
├─────────────────────────────────────────┤
│  • xterm.js (terminal emulator)         │
│  • terminal.js (client logic)           │
│  • WebGL/Canvas renderer                │
│  • WebTransport/WebSocket transport     │
└─────────────────────────────────────────┘
```

## Repository Structure

```
sip/
├── cmd/
│   └── example/           # Example Bubble Tea apps using sip
│       ├── basic/
│       ├── chat/
│       └── editor/
├── pkg/
│   └── sip/
│       ├── server.go      # Main server implementation
│       ├── session.go     # Session management
│       ├── handlers.go    # HTTP/WS/WT handlers
│       ├── pty.go         # PTY integration
│       └── static/        # Embedded web assets
│           ├── index.html
│           ├── terminal.js
│           ├── terminal.css
│           └── fonts/
├── examples/              # Full example applications
├── docs/                  # Documentation
├── README.md
├── LICENSE
└── go.mod
```

## Use Cases

### 1. Dev Tools
Transform CLI dev tools into web-accessible dashboards:
```go
// Dashboard for your logs, metrics, etc.
sip.Serve(NewDashboard(), sip.Config{Port: "3000"})
```

### 2. Remote TUIs
Make any TUI app remotely accessible:
```go
// Remote monitoring tool
sip.Serve(NewMonitoringApp(), sip.Config{
    Host:     "0.0.0.0",
    ReadOnly: true,
})
```

### 3. Demos & Documentation
Interactive documentation for TUI apps:
```go
// Live demo of your app
sip.Serve(NewDemoApp(), sip.Config{
    MaxConnections: 50,
    ReadOnly:       true,
})
```

## Roadmap

### Phase 1: Extraction (Current)
- [x] Extract web functionality from tuios into tuios-web
- [ ] Identify reusable components
- [ ] Design public API

### Phase 2: Library Development
- [ ] Create standalone sip repository
- [ ] Implement core server functionality
- [ ] Add comprehensive tests
- [ ] Write documentation and examples

### Phase 3: Integration
- [ ] Update tuios-web to use sip library
- [ ] Create example apps
- [ ] Publish to GitHub
- [ ] Announce to Charm community

### Phase 4: Community
- [ ] Gather feedback from Bubble Tea developers
- [ ] Add requested features
- [ ] Create more examples
- [ ] Build ecosystem integrations

## Technical Considerations

### PTY Abstraction
The library needs to abstract PTY handling to work with any Bubble Tea app:
- Spawn app in PTY
- Capture stdout/stderr
- Forward input from browser
- Handle window resize signals

### State Management
- Session lifecycle management
- Connection pooling
- Graceful shutdown
- Memory cleanup

### Performance
- Buffer pools (sync.Pool)
- Atomic counters
- Efficient message batching
- Mouse event deduplication

### Security
- Input sanitization
- Rate limiting
- CORS configuration
- TLS certificate handling
- Read-only mode enforcement

## License

MIT (same as TUIOS)

## Contributing

This is a future project. Contributions and ideas are welcome! Please open an issue in the TUIOS repository to discuss features or implementation details.

## Related Projects

- [ttyd](https://github.com/tsl0922/ttyd) - Share terminal over the web (C, libwebsockets)
- [gotty](https://github.com/yudai/gotty) - Share terminal as web application (Go, older)
- [xterm.js](https://xtermjs.org/) - Terminal emulator in browser
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - TUI framework for Go

## Acknowledgments

The sip library will build on the web terminal work done in tuios-web, which itself was inspired by projects like ttyd and gotty. Special thanks to the Charm team for creating the Bubble Tea framework that makes all of this possible.
