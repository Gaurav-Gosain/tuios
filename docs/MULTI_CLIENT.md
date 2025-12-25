# Multi-Client Sessions

TUIOS supports multiple clients connecting to the same session simultaneously, enabling collaborative workflows and flexible access patterns.

> **Note:** Throughout this document, `Ctrl+B` refers to the default leader key. This is configurable via the `leader_key` option in your config file.

## What is Multi-Client Mode?

Multi-client mode allows multiple terminal instances (SSH, web, or local TUI clients) to attach to the same TUIOS session concurrently. All clients see the same windows, input, and output in real-time.

## Use Cases

### Pair Programming
```bash
# Developer A creates and attaches to session
developer-a$ tuios attach pairing-session

# Developer B joins the same session
developer-b$ ssh remote-server
remote$ tuios attach pairing-session
```

Both developers see identical terminal output and can take turns typing.

### Remote Troubleshooting
```bash
# User shares their session
user$ tuios attach support-session

# Support engineer connects
support$ tuios attach support-session
```

The support engineer sees exactly what the user sees and can provide guidance or take control.

### Multi-Device Workflow
```bash
# Work laptop
laptop$ tuios attach dev-work

# Later, from home desktop
desktop$ ssh work-server
work-server$ tuios attach dev-work
```

Seamlessly resume the same session from different devices.

### Screen Sharing / Presentation
```bash
# Presenter
presenter$ tuios attach demo

# Multiple viewers (via SSH or web)
viewer1$ tuios attach demo
viewer2$ tuios-web  # Navigate to session "demo" in browser
```

## How It Works

### Session State Synchronization

When multiple clients attach to a session:

1. **Shared PTY ownership:** Daemon owns the PTYs, not individual clients
2. **Input broadcast:** Any client's input is written to the PTY
3. **Output fanout:** PTY output is broadcast to all attached clients
4. **State sync:** Window positions, focus, workspaces sync across clients

### Terminal Size Coordination

To handle different client terminal sizes, TUIOS uses the **minimum size** strategy:

```
Client A: 120x30 (wide monitor)
Client B: 80x24  (laptop)
Client C: 100x40 (desktop)

Effective size: min(120, 80, 100) x min(30, 24, 40) = 80x24
```

All clients render at the smallest common size. When a client disconnects, the size recalculates based on remaining clients.

**Why minimum size?**
- Prevents content being cut off for smaller terminals
- Ensures all clients see the same content
- Alternative (maximum size) would cause scrolling/wrapping differences

### Client Join/Leave Events

The daemon tracks client connections and notifies sessions:

```
15:32:01 Client A attached (80x24)
15:32:15 Client B attached (120x30) - resizing to 80x24
15:34:22 Client A detached - resizing to 120x30
```

## Commands

### List Sessions
```bash
tuios ls
```

Output:
```
Available sessions:
  - dev-work (2 clients, 4 windows, created 2 hours ago)
  - pairing-session (1 client, 1 window, created 15 minutes ago)
```

### Attach to Session
```bash
# Create if missing
tuios attach my-session

# Attach to existing (fail if doesn't exist)
tuios attach my-session --no-create
```

### Detach from Session
```
Ctrl+B, d  # Default prefix key
```

Or programmatically from another terminal:
```bash
# Send detach key combo to the session
tuios send-keys --session my-session "ctrl+b" "d"
```

### Kill Session
```bash
# Kill specific session
tuios kill my-session

# Kill all sessions
tuios kill-server
```

## Multi-Client Scenarios

### Scenario 1: SSH + Local TUI

```bash
# Terminal 1 (local)
# Note: daemon starts automatically with 'tuios new' or 'tuios attach'
local$ tuios new dev
# Or attach to existing session (creates if doesn't exist):
# local$ tuios attach dev

# Terminal 2 (SSH from remote)
remote$ ssh myserver
myserver$ tuios attach dev
```

Both terminals share the same session.

### Scenario 2: Web + SSH

```bash
# SSH client
ssh$ tuios attach web-demo

# Browser
https://myserver:8080
# Select "web-demo" from session list
```

### Scenario 3: Three Simultaneous Clients

```bash
# Client 1 (local TUI)
local$ tuios attach collab

# Client 2 (SSH)
ssh$ tuios attach collab

# Client 3 (web)
web: Connect to "collab" session
```

All three clients share input/output. If Client 1 runs `ls`, all three see the output.

## Technical Details

### State Synchronization

The daemon sends these update messages to all clients:

- `WindowsUpdate`: Window list changed
- `FocusUpdate`: Active window changed
- `WorkspaceUpdate`: Workspace switched
- `ResizeUpdate`: Effective terminal size changed
- `PTYOutput`: Output from PTY

### Input Handling

- Clients send input via `MsgInput` protocol messages
- Daemon writes input to PTY
- PTY echoes back (normal terminal behavior)
- All clients receive echoed output

### Conflict Resolution

**What happens when multiple clients type simultaneously?**

Characters interleave at the PTY level (standard Unix behavior):
```
Client A types: "hello"
Client B types: "world"
PTY receives:   "hweolrllod" (interleaved)
```

**Best practice:** Coordinate who has "keyboard control" via communication (voice, chat, etc.).

### Performance Considerations

- **Bandwidth:** Each client receives full PTY output (no deduplication)
- **Latency:** Input from any client has same latency as single-client mode
- **Scalability:** Tested with up to 10 simultaneous clients
- **CPU:** Daemon uses select/epoll for efficient multi-client I/O

## Configuration

Multi-client mode is enabled by default when using daemon mode. No special configuration required.

### Disable Multi-Client (force single client)

Not currently supported. To prevent multiple connections, use external access controls (SSH configuration, firewall rules, etc.).

## Security Implications

### Trust Model

All clients attached to a session have **full control**:
- Can view all output
- Can send input (keystrokes)
- Can manipulate windows

**Use multi-client mode only with trusted collaborators.**

### Authentication

- **Local clients:** Unix socket permissions (same user)
- **SSH clients:** SSH authentication
- **Web clients:** Currently no authentication (TODO for production use)

### Audit Trail

Enable daemon logging to track client connections:
```bash
# Run daemon in foreground with debug logging
tuios daemon --log-level=messages

# Or set log level when creating a session
# (daemon starts automatically in background)
tuios new mysession --log-level=messages
```

Available log levels: `off`, `errors`, `basic`, `messages`, `verbose`, `trace`

Log format:
```
2025-12-25 15:32:01 [INFO] Client connected: id=abc123, addr=192.168.1.100
2025-12-25 15:32:01 [INFO] Client attached to session 'dev-work'
2025-12-25 15:34:22 [INFO] Client detached: id=abc123
```

## Troubleshooting

### Terminal size flickering

**Cause:** Clients with very different terminal sizes joining/leaving

**Solution:** Use terminals of similar size, or resize smaller clients to match

### Input lag with many clients

**Cause:** Network latency or bandwidth saturation

**Solution:**
- Reduce number of simultaneous clients
- Use SSH compression: `ssh -C`
- Check network conditions

### Client sees garbled output

**Cause:** Terminal size mismatch or encoding issues

**Solution:**
- Detach and reattach: `Ctrl+B, d` then `tuios attach <session>`
- Check locale settings: `echo $LANG` (should match across clients)

### Session not visible in list

**Cause:** Session on different daemon, or daemon not running

**Solution:**
```bash
# Check if daemon is running and list all sessions
tuios ls

# Verify daemon is running by checking for sessions
# (if no error, daemon is running)
tuios list-windows --json 2>&1 | grep -q "success" && echo "Daemon running" || echo "Daemon not running"
```

## Comparison with tmux/screen

| Feature | TUIOS | tmux | screen |
|---------|-------|------|--------|
| Multi-client | ✅ Yes | ✅ Yes | ✅ Yes |
| Size strategy | Minimum | Minimum | Minimum |
| Real-time sync | ✅ Yes | ✅ Yes | ✅ Yes |
| Web clients | ✅ Yes (tuios-web) | ❌ No (external tools) | ❌ No |
| BSP tiling | ✅ Yes | ❌ No | ❌ No |
| Daemon architecture | ✅ Yes | ✅ Yes | ✅ Yes |

## Examples

### Example 1: Code Review Session

```bash
# Reviewer creates session
reviewer$ tuios attach code-review-42
reviewer$ cd ~/projects/myapp
reviewer$ git diff main

# Author joins
author$ tuios attach code-review-42
# Both see the same git diff output

# Reviewer points out an issue by moving cursor to line
reviewer$ vim src/handler.go:123

# Author sees cursor movement in real-time
```

### Example 2: Training Session

```bash
# Trainer
trainer$ tuios attach training
trainer$ # Demonstrates commands

# 5 trainees join
trainee1$ tuios attach training
trainee2$ tuios attach training
# ... etc

# All trainees see trainer's demonstration in real-time
# Trainees can try commands themselves (input interleaved)
```

### Example 3: Debugging Production Issue

```bash
# On-call engineer
oncall$ ssh prod-server
prod-server$ tuios attach incident-debug
prod-server$ tail -f /var/log/application.log

# Senior engineer joins to help
senior$ ssh prod-server
prod-server$ tuios attach incident-debug
# Both see live log output

# Senior suggests a fix
senior$ # Types command to check status
prod-server$ systemctl status myapp
```

## Related Documentation

- [CLI Reference](CLI_REFERENCE.md) - Session commands
- [Architecture](ARCHITECTURE.md) - Multi-client architecture details
- [SSH Server](SSH.md) - SSH-based multi-client access
- [Web Terminal](WEB.md) - Web-based multi-client access

## Future Enhancements

Potential future features:

- **Access control:** Read-only clients, keyboard locking
- **Client identity:** Display which client sent each input
- **Cursor tracking:** Show multiple client cursors in copy mode
- **Voice chat integration:** Built-in voice for remote pairing
- **Whiteboard/annotations:** Collaborative markup of terminal output
