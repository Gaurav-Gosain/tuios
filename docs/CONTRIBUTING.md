# Contributing

The full guide (setup, PR process, code style, testing) lives on the docs site: https://tuios.dev/docs/contributing

For working in this tree, [AGENTS.md](../AGENTS.md) is the orientation document: package map, build and test commands, and the testing infrastructure.

The short version:

```bash
git clone https://github.com/Gaurav-Gosain/tuios.git
cd tuios

go build -o tuios ./cmd/tuios   # pure Go backend, needs only go (1.26+)
go test ./...

# Or build and install onto your PATH, pure Go emulator by default.
# `ghostty` installs the ghostty backend (needs zig; see docs/ghostty-vt.md).
./scripts/install.sh
```

## Local development

The `justfile` at the root holds the dev commands. Install [just](https://just.systems), then run `just` to list them. The main ones:

| Recipe | What it does |
|--------|--------------|
| `just build` | Builds `tuios` and `tuios-web` into `.dev/bin`, stamped `dev+COMMIT`. |
| `just install` | Installs both to `~/.local/bin` and keeps the old ones as `.bak`. |
| `just deploy [host...]` | Installs this checkout here and on the `[hosts]` hosts, and restarts old daemons. The restart ends the programs in their panes. |
| `just restart` | Restarts the live daemon. This ends the programs in every pane. |
| `just sandbox [args]` | Runs tuios against a throwaway daemon. It does not touch your own daemon. |
| `just run-debug` | Runs the TUI with `--debug` against a throwaway daemon. |
| `just logs` | Follows the live daemon's log file. |
| `just test` | Runs the unit tests on half the cores, at low priority. |
| `just e2e [pattern]` | Builds the binary and runs the end-to-end suite, or the tests that match the pattern. |
| `just e2e-flake TEST N` | Runs one end-to-end test N times and counts the failures. |
| `just lint` | Runs golangci-lint, `go vet` for each OS, and `internal/lint`. |
| `just ci` | Runs the local part of CI: format, tidy, lint, unit tests and binary size. |
| `just clean` | Stops the throwaway daemons and removes what the recipes built. |

Only `install`, `install-ghostty`, `deploy`, `deploy-hosts` and `restart` touch the daemon you work in.

Use conventional commit prefixes (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`), keep commits focused, and run `go fmt` before committing. A commit message ends with its body: please add no trailers, such as `Co-authored-by:`. Security reports go through [SECURITY.md](../SECURITY.md), not the issue tracker.
