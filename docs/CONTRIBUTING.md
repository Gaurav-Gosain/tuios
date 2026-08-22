# Contributing

The full guide (setup, PR process, code style, testing) lives on the docs site: https://tuios.gaurav.zip/docs/contributing

For working in this tree, [AGENTS.md](../AGENTS.md) is the orientation document: package map, build and test commands, and the testing infrastructure.

The short version:

```bash
git clone https://github.com/Gaurav-Gosain/tuios.git
cd tuios

go build -o tuios ./cmd/tuios   # pure Go backend, needs only go (1.26+)
go test ./...

# Or build and install onto your PATH, ghostty backend by default
# (needs zig; see docs/ghostty-vt.md). `pure` installs the pure Go emulator.
./scripts/install.sh
```

Use conventional commit prefixes (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`), keep commits focused, and run `go fmt` before committing. Security reports go through [SECURITY.md](../SECURITY.md), not the issue tracker.
