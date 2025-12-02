# Web Terminal Migration Guide

## Overview

Starting from the next release, the `tuios web` command has been **removed** from the main TUIOS binary and extracted into a separate `tuios-web` binary.

## Why the Change?

**Security Isolation**: The web server functionality posed a potential security risk as it could be used as a backdoor. By separating it into its own binary:

- The main `tuios` binary has a smaller attack surface
- Users who don't need web functionality don't have it installed
- The web server can be deployed separately with proper security measures
- Future library extraction: The web terminal logic will be abstracted into a standalone library called **sip** (tagline: "drinking tea through the browser") to help other Bubble Tea developers serve their TUIs as web apps

## Migration Steps

### Before (Old)

```bash
tuios web --port 8080 --theme dracula
```

### After (New)

```bash
# Install the separate binary
brew install tuios-web
# or
yay -S tuios-web-bin
# or
go install github.com/Gaurav-Gosain/tuios/cmd/tuios-web@latest

# Run it
tuios-web --port 8080 --theme dracula
```

## Installation

### Homebrew (macOS/Linux)

```bash
brew install tuios-web
```

### Arch Linux (AUR)

```bash
# Using yay
yay -S tuios-web-bin

# Using paru
paru -S tuios-web-bin
```

### Go Install

```bash
go install github.com/Gaurav-Gosain/tuios/cmd/tuios-web@latest
```

### From GitHub Releases

Download the `tuios-web_*` archive for your platform from [GitHub Releases](https://github.com/Gaurav-Gosain/tuios/releases).

## Command Compatibility

All flags and functionality remain the same:

| Old Command | New Command |
|------------|-------------|
| `tuios web` | `tuios-web` |
| `tuios web --port 8080` | `tuios-web --port 8080` |
| `tuios web --host 0.0.0.0` | `tuios-web --host 0.0.0.0` |
| `tuios web --theme dracula` | `tuios-web --theme dracula` |
| `tuios web --read-only` | `tuios-web --read-only` |
| `tuios web --max-connections 10` | `tuios-web --max-connections 10` |

## Systemd Service Update

If you're running tuios-web as a systemd service, update your unit file:

### Before

```ini
[Service]
ExecStart=/usr/local/bin/tuios web --host 127.0.0.1 --port 7681
```

### After

```ini
[Service]
ExecStart=/usr/local/bin/tuios-web --host 127.0.0.1 --port 7681
```

## Docker

If you're using Docker, update your Dockerfile or docker-compose.yml:

### Before

```dockerfile
CMD ["tuios", "web", "--host", "0.0.0.0"]
```

### After

```dockerfile
CMD ["tuios-web", "--host", "0.0.0.0"]
```

## Future: Sip Library

The web terminal implementation will eventually be extracted into a standalone library called **sip** ("drinking tea through the browser"). This will allow any Bubble Tea application to be served as a web app with:

- xterm.js integration
- WebGL/Canvas rendering
- WebTransport/WebSocket support
- Bundled Nerd Fonts
- Settings panel

Stay tuned for announcements about the sip library!

## Questions?

If you have any questions or issues with the migration, please:

1. Check the [Web Terminal documentation](WEB.md)
2. Open an issue on [GitHub](https://github.com/Gaurav-Gosain/tuios/issues)
3. Join the discussion in our community

## Timeline

- **Previous releases**: `tuios web` command available in main binary
- **Next release**: `tuios web` removed, `tuios-web` binary released separately
- **Future**: `sip` library extracted for general Bubble Tea apps
