# E2E tests and the sim backend

How to run the e2e suite, the negative-control rule, the sim backend, the unit tests that stay, and manual checks.

## E2E Tests

The E2E suite runs a real `tuios` binary against real daemons. It is under
`e2e/tui`, its own Go module, and skips unless `TUIOS_E2E` is set. Without
`TUIOS_E2E_BIN` it builds the binary itself.

```bash
go build -o /tmp/tuios ./cmd/tuios
cd e2e/tui && TUIOS_E2E=1 TUIOS_E2E_BIN=/tmp/tuios go test -count=1 -timeout 40m .
```

`TestDnDDesktopBridge` runs the drag and drop bridge against a real kitty
under Xvfb. It skips unless `TUIOS_E2E_KITTY` names a kitty binary, and
needs `Xvfb`, `xdotool` and `xclip`; `TUIOS_E2E_YAZI` adds yazi as a source.
The e2e-kitty workflow runs it on request and once a week.

A test that claims to cover a bug must fail on a build with the fix removed.
`e2e/tui/NEGATIVE_CONTROLS.md` records those runs and the rules for them.

`TUIOS_E2E_BACKEND` picks how a test runs the tuios client. `real`, the
default, runs the built binary on a PTY. `sim` runs the client and the daemon
in the test process (`e2e/tui/backend_test.go`). The CLI calls and hooks still
run the built binary. A test keeps the same `*tuitest.Terminal` on both.

```bash
cd e2e/tui && TUIOS_E2E=1 TUIOS_E2E_BIN=/tmp/tuios TUIOS_E2E_BACKEND=sim go test -count=1 -run '^TestX$' .
```

Under `sim` the tests of a package share one process environment, so a test
has one isolation root and its knobs go through `testknob.Set`. A test that
needs something only a process of its own gives (real ssh, another tuios
binary, a live upgrade, pids or signals) calls `requireReal(t, why)` first,
which skips it under `sim`. `TUITEST_TRACE=FILE` writes the observation trace
of each test, on either backend: every `tuiosCLI` call and every wait and
input of each client. The same test has the same trace on both backends.

## Kept Unit Tests

```bash
# Every module outside e2e/tui (go test ./... covers the app module only)
just test

# One package
go test ./vt/

# Perf budgets
go test -bench=. ./internal/app/...
```

## Manual Testing Checklist

When testing UI/UX changes:
- [ ] Create/close multiple windows
- [ ] Switch between workspaces (Alt+1-9)
- [ ] Test tiling mode (t key)
- [ ] Test copy mode (Ctrl+B, [)
- [ ] Verify keybindings work
- [ ] Check terminal output rendering
- [ ] Test mouse interactions (drag, resize)

## Tape Script Testing

```bash
# Validate tape syntax
go run ./cmd/tuios tape validate examples/demo.tape

# Run tape with visible TUI
go run ./cmd/tuios tape play examples/demo.tape
```
