# Contributing to TUIOS

Thank you for considering contributing to TUIOS! This guide will help you get started.

## Ways to Contribute

- **Bug Reports**: Use the [bug report template](https://github.com/Gaurav-Gosain/tuios/issues/new?template=bug_report.yml)
- **Feature Requests**: Use the [feature request template](https://github.com/Gaurav-Gosain/tuios/issues/new?template=feature_request.yml)
- **Code Contributions**: Submit pull requests for bug fixes, features, or improvements
- **Documentation**: Improve or expand documentation in `docs/` or README
- **Testing**: Test on different platforms and report issues

**Have questions?** Use [GitHub Discussions](https://github.com/Gaurav-Gosain/tuios/discussions).

---

## Development Setup

### Prerequisites

- **Go 1.24+** (required for building)
- A terminal with true color support
- Git

### Quick Start

```bash
# Clone the repository
git clone https://github.com/Gaurav-Gosain/tuios.git
cd tuios

# Build from source
go build -o tuios ./cmd/tuios

# Run
./tuios

# Run tests
go test ./...
```

### Alternative Development Environments

**Using Nix:**
```bash
nix develop  # Enters development shell
```

**Using Docker:**
```bash
docker build -t tuios .
docker run -it --rm tuios
```

---

## Project Structure

```
tuios/
├── cmd/tuios/          # Main entry point
├── internal/
│   ├── app/            # Window manager and core orchestration
│   ├── config/         # Configuration and keybindings
│   ├── input/          # Input handling and modal routing
│   ├── layout/         # Window layout and tiling
│   ├── pool/           # Memory pooling
│   ├── server/         # SSH server implementation
│   ├── system/         # System utilities
│   ├── terminal/       # Terminal window management
│   ├── theme/          # Theming and styles
│   ├── ui/             # UI components
│   └── vt/             # Terminal emulation (ANSI/VT100)
├── docs/               # Documentation
└── nix/                # Nix packaging
```

See [ARCHITECTURE.md](docs/ARCHITECTURE.md) for detailed technical documentation.

---

## Making Changes

### Before You Start

1. **Check existing issues** to see if someone is already working on it
2. **Open an issue** to discuss major changes before implementing
3. **Fork the repository** and create a new branch for your changes

### Code Guidelines

- Follow standard Go conventions ([Effective Go](https://go.dev/doc/effective_go))
- Run `go fmt` before committing
- Ensure tests pass: `go test ./...`
- Keep commits focused and atomic
- Write clear commit messages

### Pull Request Process

1. **Create a branch** from `main`:
   ```bash
   git checkout -b feat/your-feature-name
   ```

2. **Make your changes** and commit:
   ```bash
   git add .
   git commit -m "feat: add your feature description"
   ```

3. **Test thoroughly** across platforms if possible (see [Testing](#testing) below)

4. **Push and create a PR**:
   ```bash
   git push origin feat/your-feature-name
   ```
   Then open a pull request using the [PR template](.github/PULL_REQUEST_TEMPLATE/pull_request_template.md)

5. **Fill out the PR template** with:
   - What the PR does
   - Motivation/context
   - Key changes
   - How to verify
   - Platform(s) tested
   - Installation method tested

### Commit Message Format

Use conventional commit prefixes:
- `feat:` - New feature
- `fix:` - Bug fix
- `docs:` - Documentation changes
- `refactor:` - Code refactoring
- `test:` - Adding or updating tests
- `chore:` - Maintenance tasks

Examples:
```
feat: add configurable dockbar position
fix: panic when closing last window on Linux
docs: update keybindings reference
```

---

## Testing

### Cross-Platform Testing

TUIOS supports multiple platforms. If possible, test your changes on:

**Platforms:**
- Linux (x86_64, arm64, armv6, armv7, i386)
- macOS / Darwin (arm64, x86_64)
- FreeBSD (x86_64, arm64, i386)
- Windows (x86_64, i386)

**Installation Methods:**
- Build from source (`go build`)
- Homebrew (macOS/Linux)
- AUR (Arch Linux)
- Nix
- Docker
- Bash install script

**You don't need to test everything** - but mention what you tested in your PR.

### Running Tests

```bash
# Run all tests
go test ./...

# Run specific package tests
go test ./internal/config/...
```

### Manual Testing Checklist

When testing UI/UX changes:
- [ ] Create/close multiple windows
- [ ] Switch between workspaces
- [ ] Test tiling mode
- [ ] Test copy mode navigation
- [ ] Verify keybindings work as expected
- [ ] Check terminal output rendering
- [ ] Test mouse interactions

---

## Documentation

When contributing, consider updating:

- **README.md** - For user-facing features
- **docs/KEYBINDINGS.md** - For new keybindings
- **docs/CONFIGURATION.md** - For configuration options
- **docs/CLI_REFERENCE.md** - For CLI flags/commands
- **docs/ARCHITECTURE.md** - For architectural changes

---

## Code Review Process

1. Maintainer (@Gaurav-Gosain) will review your PR
2. Address any requested changes
3. Once approved, your PR will be merged
4. Your contribution will be included in the next release

---

## Release Process

TUIOS uses automated releases via GitHub Actions:
- Releases are tagged (e.g., `v0.3.4`)
- Binaries are built for all platforms
- Docker images are published
- Package managers are updated automatically

You don't need to worry about this as a contributor - the maintainer handles releases.

---

## Getting Help

- **Questions**: [GitHub Discussions](https://github.com/Gaurav-Gosain/tuios/discussions)
- **Bugs**: [Bug Report Template](https://github.com/Gaurav-Gosain/tuios/issues/new?template=bug_report.yml)
- **Features**: [Feature Request Template](https://github.com/Gaurav-Gosain/tuios/issues/new?template=feature_request.yml)
- **Security**: See [SECURITY.md](SECURITY.md)

---

## Code of Conduct

Be respectful, constructive, and collaborative. We're all here to make TUIOS better.

---

## Support the Project

If you find TUIOS useful, consider:
- Starring the repository
- Sharing it with others
- [Supporting on Ko-fi](https://ko-fi.com/B0B81N8V1R)

---

**Thank you for contributing to TUIOS!**
