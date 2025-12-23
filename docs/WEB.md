# Web Terminal Mode (tuios-web)

**Security Notice:** The web terminal functionality is provided as a separate binary (`tuios-web`) to isolate the web server from the main TUIOS binary. This prevents the web server from being used as a potential backdoor.

TUIOS can be accessed through any modern web browser using the `tuios-web` binary.

## Table of Contents

- [Installation](#installation)
- [Overview](#overview)
- [Quick Start](#quick-start)
- [Features](#features)
- [Architecture](#architecture)
- [Configuration](#configuration)
- [Transport Protocols](#transport-protocols)
- [Rendering](#rendering)
- [Performance](#performance)
- [Security](#security)
- [Troubleshooting](#troubleshooting)

---

## Installation

**Separate Binary Required:**

```bash
# Homebrew (macOS/Linux)
brew install tuios-web

# Arch Linux (AUR)
yay -S tuios-web-bin
# or
paru -S tuios-web-bin

# Go Install
go install github.com/Gaurav-Gosain/tuios/cmd/tuios-web@latest

# From GitHub Releases
# Download tuios-web_*_<platform>_<arch>.tar.gz
# Extract and run ./tuios-web
```

---

## Overview

The `tuios-web` command starts a web server that serves a full TUIOS experience in the browser. It is powered by [**sip**](https://github.com/Gaurav-Gosain/sip), a standalone library for serving Bubble Tea apps through the browser.

**Key technologies:**
- **xterm.js** for terminal emulation
- **WebGL/Canvas** for hardware-accelerated rendering
- **WebTransport (QUIC)** or **WebSocket** for real-time communication
- **JetBrains Mono Nerd Font** for proper icon rendering

> **Note:** The web terminal functionality has been extracted into the [sip library](SIP_LIBRARY.md), which can be used to serve any Bubble Tea application through the browser.

## Quick Start

```bash
# Start web server on default port (7681)
tuios-web

# Open in browser
open http://localhost:7681

# With custom port
tuios-web --port 8080

# With TUIOS flags forwarded
tuios-web --theme dracula --show-keys
```

## Features

- **Full TUIOS Experience**: All TUIOS features work in the browser
- **WebGL Rendering**: GPU-accelerated terminal rendering for smooth 60fps
- **Dual Protocol Support**: WebTransport (QUIC) with WebSocket fallback
- **Bundled Nerd Fonts**: No client-side font installation required
- **Settings Panel**: Configure transport, renderer, and font size
- **Mouse Support**: Full mouse interaction with cell-based optimization
- **Auto-Reconnect**: Automatic reconnection with exponential backoff
- **Read-Only Mode**: View-only sessions for demonstrations

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        Browser                               │
├─────────────────────────────────────────────────────────────┤
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────────┐  │
│  │  xterm.js   │◄──►│ terminal.js │◄──►│ WebTransport/WS │  │
│  │  (WebGL)    │    │  (client)   │    │   (transport)   │  │
│  └─────────────┘    └─────────────┘    └────────┬────────┘  │
└─────────────────────────────────────────────────┼───────────┘
                                                  │
                                    ┌─────────────┴─────────────┐
                                    │     QUIC (UDP:7682)       │
                                    │  or WebSocket (TCP:7681)  │
                                    └─────────────┬─────────────┘
                                                  │
┌─────────────────────────────────────────────────┼───────────┐
│                     Server                      │           │
├─────────────────────────────────────────────────┼───────────┤
│  ┌──────────────┐    ┌──────────────┐    ┌─────┴─────┐     │
│  │ HTTP Server  │    │  WT Server   │    │  Session  │     │
│  │  (static)    │    │   (QUIC)     │    │  Manager  │     │
│  │  :7681       │    │   :7682      │    └─────┬─────┘     │
│  └──────────────┘    └──────────────┘          │           │
│                                          ┌─────┴─────┐     │
│                                          │    PTY    │     │
│                                          │  (TUIOS)  │     │
│                                          └───────────┘     │
└─────────────────────────────────────────────────────────────┘
```

### Data Flow

1. **Client → Server**: Keyboard/mouse input sent as binary messages
2. **Server → Client**: Terminal output streamed with message batching
3. **Framing**: WebTransport uses 4-byte length prefixes (streams don't preserve boundaries)

### Message Protocol

| Type | Code | Direction | Description |
|------|------|-----------|-------------|
| Input | `0` | C→S | Keyboard/mouse input |
| Output | `1` | S→C | Terminal output data |
| Resize | `2` | C→S | Terminal size change |
| Ping | `3` | C→S | Keep-alive ping |
| Pong | `4` | S→C | Keep-alive response |
| Title | `5` | S→C | Window title update |
| Options | `6` | S→C | Session configuration |
| Close | `7` | S→C | Session ended |

## Configuration

### Command Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `7681` | HTTP server port |
| `--host` | `localhost` | Server bind address |
| `--read-only` | `false` | Disable client input |
| `--max-connections` | `0` | Max concurrent sessions (0=unlimited) |
| `--default-session` | | Default session name for all connections |
| `--ephemeral` | `false` | Disable daemon mode (sessions don't persist) |

### Daemon Mode (Default)

By default, `tuios-web` connects to the TUIOS daemon for persistent sessions:

```bash
# Start web server with daemon mode (default)
tuios-web

# All clients share a specific session
tuios-web --default-session shared

# Disable daemon mode (standalone sessions)
tuios-web --ephemeral
```

**Benefits of daemon mode:**
- Sessions persist when browser tabs close
- Multiple browsers/tabs can view the same session
- State (windows, workspaces) preserved across reconnections
- Integrates with `tuios ls`, `tuios attach`, and other session commands

**Multi-client behavior:**
- Terminal size uses minimum of all connected client dimensions
- State changes broadcast to all clients in real-time
- Clients notified when others join/leave

### TUIOS Flags

All TUIOS flags are forwarded to the spawned instance:

```bash
# Theme and appearance
tuios-web --theme nord --border-style rounded

# Debug mode
tuios-web --debug --show-keys

# ASCII-only mode
tuios-web --ascii-only

# Disable animations for instant transitions
tuios-web --no-animations
```

### Client Settings

Click the ⚙ button in the browser to access:

- **Transport**: Auto, WebTransport, or WebSocket
- **Renderer**: Auto, WebGL, Canvas, or DOM
- **Font Size**: 10-24px

Settings are persisted in localStorage.

## Transport Protocols

### WebTransport (QUIC)

- **Port**: HTTP port + 1 (default: 7682)
- **Protocol**: HTTP/3 over QUIC (UDP)
- **Benefits**: Lower latency, better multiplexing, connection migration
- **Requirements**: Chrome 97+, Edge 97+, or compatible browser

Uses self-signed certificates with `serverCertificateHashes` for development. Certificates are valid for 10 days (Chrome requirement).

### WebSocket (Fallback)

- **Port**: Same as HTTP (default: 7681)
- **Protocol**: WebSocket over TCP
- **Benefits**: Universal browser support
- **Used when**: WebTransport unavailable or explicitly selected

## Rendering

### WebGL (Default)

GPU-accelerated rendering using xterm.js WebGL addon:
- Smooth 60fps scrolling and updates
- Lower CPU usage
- Hardware-accelerated text rendering

### Canvas (Fallback)

2D canvas rendering:
- Good performance on most devices
- Used when WebGL unavailable or context lost

### DOM (Fallback)

Standard DOM-based rendering:
- Most compatible option
- Higher CPU usage
- Used when Canvas addon unavailable

## Performance

### Server Optimizations

- **Buffer Pools**: Reusable buffers reduce GC pressure
- **Atomic Counters**: Lock-free connection counting
- **Direct Streaming**: No intermediate buffering for PTY output
- **Structured Logging**: charmbracelet/log with configurable levels

### Client Optimizations

- **requestAnimationFrame Batching**: Terminal writes batched per frame
- **Mouse Deduplication**: Only sends events when cell position changes
- **Pre-allocated Buffers**: Reusable send/receive buffers
- **Cached DOM Elements**: No repeated querySelector calls

### Typical Performance

| Metric | Value |
|--------|-------|
| Latency (local) | <5ms |
| Latency (LAN) | <20ms |
| Mouse events filtered | 80-95% |
| Memory (per session) | ~10MB |

## Security

### Certificate Handling

For development, TUIOS generates a self-signed ECDSA P-256 certificate:
- Valid for 10 days (Chrome WebTransport requirement)
- Hash provided via `/cert-hash` endpoint
- No browser certificate warning needed for WebTransport

### Production Recommendations

1. Use a reverse proxy (nginx, Caddy) with proper TLS
2. Set `--host 127.0.0.1` and proxy external traffic
3. Use `--max-connections` to limit resource usage
4. Consider `--read-only` for public demos

### CORS

All origins allowed by default. For production, configure `AllowOrigins` in the server config.

## Troubleshooting

### WebTransport Not Connecting

1. Check browser support (Chrome 97+)
2. Verify UDP port 7682 is accessible
3. Check console for certificate hash errors
4. Try forcing WebSocket in settings

### Blank Terminal

1. Check browser console for errors
2. Verify fonts loaded (`document.fonts.check()`)
3. Try switching renderer in settings
4. Check if TUIOS process started (server logs)

### High Latency

1. Check network conditions
2. Prefer WebTransport over WebSocket
3. Use WebGL renderer for smoother updates
4. Check server CPU usage

### Session Not Closing

If pressing `q` doesn't close the web session:
1. Server sends `MsgClose` when PTY exits
2. Check for browser console errors
3. Verify session cleanup in server logs

### Debug Mode

```bash
# Enable verbose logging
tuios-web --debug
```

Server logs include:
- Connection attempts and session lifecycle
- Bytes sent/received per session
- Terminal resize events
- Error details

---

## Related Documentation

- [CLI Reference](CLI_REFERENCE.md) - Complete command reference
- [Configuration](CONFIGURATION.md) - TOML configuration options
- [Keybindings](KEYBINDINGS.md) - Keyboard shortcuts
- [Architecture](ARCHITECTURE.md) - Technical architecture
