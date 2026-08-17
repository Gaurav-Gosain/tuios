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
# Homebrew (macOS/Linux) - ships via the maintainer's tap
brew tap Gaurav-Gosain/tap
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
| `--cert` | | TLS certificate (PEM); serves HTTPS |
| `--key` | | TLS private key (PEM); required with `--cert` |
| `--auto-tls` | `false` | Serve HTTPS from a self-signed certificate `tuios-web` generates and keeps |
| `--cert-dir` | | Where `--auto-tls` keeps its keypair (default: `sip` in your user config dir) |
| `--cert-host` | | Extra DNS name or IP in the `--auto-tls` certificate (repeatable) |
| `--cert-days` | `0` | Days an `--auto-tls` certificate is valid for (0 = 365) |
| `--insecure` | `false` | Serve a non-loopback host unencrypted |
| `--touch` | `auto` | Whether a client is driven by a finger: `auto`, `on`, `off` |
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

### On a phone

A touch device gets sip's key bar, carrying the TUIOS chord row over the keys a
phone keyboard does not have, and sip's touch layer on the terminal: a tap is a
click, a long press is a right click, and a press, hold and drag is a drag.

TUIOS widens two gestures for a finger, because a cell is about 8px across and
18px tall:

- **A pane division** can be grabbed from the columns either side of it, not
  just the one it is drawn in.
- **A long press on a pane** opens the pane menu even while you are typing in
  it. That menu is the finger-sized way to close, zoom, rename or split, since
  the title bar's own buttons are one row tall. A pointer reaches the same menu
  with ctrl or shift held, as before.

Neither changes anything for a pointer. Whether a client is a finger is decided
from the connection's user agent, which is a guess: sip does not put the answer
on the wire, and Safari on an iPad asking for the desktop site has no answer at
all. `--touch on` and `--touch off` settle it by hand.

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

### Binding a LAN address (reaching the server from a phone)

`--host localhost` keeps traffic inside the machine, so it needs no
certificate. Any other host is on a network, where an unencrypted terminal
means every keystroke is readable by anyone else on it, so `tuios-web`
refuses that bind until you say which way you want it:

```bash
# Over HTTPS, from a certificate tuios-web generates on first use and keeps
tuios-web --host 192.168.1.31 --auto-tls

# Over HTTPS, from a certificate you already have
tuios-web --host 192.168.1.31 --cert cert.pem --key key.pem

# In clear text, on a network you trust and no other
tuios-web --host 192.168.1.31 --insecure
```

`--auto-tls` uses the keypair [sip](https://github.com/Gaurav-Gosain/sip)
manages for this user, in `sip` inside your user config directory. It signs for
`localhost`, this machine's hostname and `hostname.local`, and every
non-loopback address on every interface, so the LAN address you actually type
works. `--cert-host` adds names only your router's DNS knows. A certificate
that stops covering the address being bound, which is what a moved DHCP lease
looks like, is regenerated rather than served into a name mismatch the browser
will not let you click through.

The certificate signs for itself, so **the first visit from any browser shows a
warning**: "Your connection is not private", `NET::ERR_CERT_AUTHORITY_INVALID`,
or "Potential Security Risk Ahead". That is expected. Choose Advanced, then
Proceed. The connection is encrypted either way; what the browser cannot do is
vouch for who is on the other end. To stop seeing it, copy the `.crt` to the
device and install it as a trusted certificate: on Android under Settings,
Encryption & credentials, Install a certificate, CA certificate; on iOS open
the file, install the profile, then enable it under About, Certificate Trust
Settings. `tuios-web` prints all of this the first time it generates one.

```bash
tuios-web cert            # where it is, what it covers, when it expires, its fingerprint
tuios-web cert new        # generate one (--force to replace an existing one)
tuios-web cert rm --force # delete it
tuios-web cert path       # just the path, for a unit file (--key for the key's)
```

No command in this group asks a question, and neither does the refusal above,
so a systemd unit or a container gets the same behaviour and the same exit code
as a shell does.

The private key is written `0600` inside a `0700` directory, and its path is
printed by `tuios-web cert path --key` and nowhere else.

WebTransport needs a certificate, so the `--insecure` route runs over the
WebSocket fallback alone. Chrome accepts a self-signed certificate for
WebTransport only when it is valid for under 14 days, so `--auto-tls`'s
year-long default keeps `--auto-tls` deployments on the WebSocket fallback too.
`--cert-days 10` trades re-accepting the browser warning every ten days for
getting WebTransport back.

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
