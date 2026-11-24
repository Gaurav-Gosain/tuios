# Platform differences

What differs between Linux, macOS and Windows, and where the split is in the code.

- PTY handling differs: `internal/terminal/window_unix.go` vs `window_nounix.go`,
  and `internal/ptyspawn/spawn_unix.go` vs `spawn_windows.go`. The daemon's
  `PTY` in `internal/session/session.go` has no per-platform file: it resizes
  through `ptyspawn.SetWinsize`, which falls back to a plain resize on ConPTY.
  The daemon's platform split is in `internal/session/daemon_unix.go` vs
  `daemon_windows.go` and `manager_unix.go` vs `manager_windows.go`
- Allocating a pane's pty differs: `internal/ptyspawn/newpty_native.go` vs
  `newpty_windows.go`. On Windows a pane opens on the `conpty.dll` and
  `OpenConsole.exe` beside `tuios.exe` when both are there, and on the inbox
  ConPTY otherwise
- Opening the host terminal for the capability probe differs:
  `internal/app/probe_tty_other.go` opens `/dev/tty`, and
  `probe_tty_windows.go` opens the console's `CONIN$` and `CONOUT$`
- Workspace keybinds differ: `opt+N` on macOS, `alt+N` on Linux
- See `internal/config/userconfig.go` → `getDefaultWorkspaceKeybinds()`
