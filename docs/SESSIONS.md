# Sessions

TUIOS runs in one of two modes: a local session that lives and dies with the
process you started, and a daemon session that lives in a background process and
survives the client that draws it. This document covers both, what attaching and
detaching do, and exactly what does and does not come back after each kind of
interruption.

> **Note:** `Ctrl+B` is the default leader key throughout. It is configurable via
> `leader_key`, see [CONFIGURATION.md](CONFIGURATION.md).

## Table of Contents

- [Local Sessions](#local-sessions)
- [Daemon Sessions](#daemon-sessions)
- [Attaching and Detaching](#attaching-and-detaching)
- [What Survives](#what-survives)
- [Resurrection](#resurrection)
- [The resurrect Command](#the-resurrect-command)
- [Where State Lives](#where-state-lives)
- [Limitations](#limitations)
- [Related Documentation](#related-documentation)

## Local Sessions

```bash
tuios
```

Running `tuios` with no subcommand starts a local session. Everything, the
window manager, the terminal emulators and the shell processes, lives inside
that one process. No daemon is started, no socket is created and no state is
written to disk. When the process exits, for any reason, the session is gone:
there is nothing to attach to and nothing to restore.

Local sessions are the right choice when you want a window manager for the
lifetime of one terminal window. Everything in this document from
[Attaching and Detaching](#attaching-and-detaching) onward applies only to
daemon sessions.

## Daemon Sessions

A daemon session lives in a separate `tuios` daemon process. The daemon owns the
shell processes (PTYs) and runs a terminal emulator for each one, so output keeps
being parsed whether or not anyone is watching. A client is only a viewer: it
subscribes to PTY output, draws it, and forwards your keystrokes back.

```bash
tuios new mysession          # create a persistent session and attach to it
tuios new mysession --detach # create it headless, attach later
tuios attach mysession       # attach to an existing session
tuios attach                 # attach to the most recent session
tuios attach mysession -c    # attach, creating the session if it is missing
tuios ls                     # list live sessions
tuios ls --json              # the same list, machine readable
tuios kill-session mysession # terminate a session and all its windows
```

The daemon starts automatically when you create or attach to a session. You can
also run it explicitly:

```bash
tuios daemon                 # run in the foreground (useful for debugging)
tuios daemon --log-level=messages
tuios kill-server            # stop the daemon and all its sessions
```

`tuios kill-server` is synchronous. It returns only after every session's state
has been written and the daemon's socket has been removed, so a new daemon can
be started as soon as it returns.

More than one client can be attached to the same session at once. All of them
see the same windows and output, and the session renders at the smallest
attached client's size. See [MULTI_CLIENT.md](MULTI_CLIENT.md).

### In-app session switching

`Ctrl+B` `S` opens the session switcher. Type to fuzzy-filter, `Enter` to switch
to the highlighted session, `Ctrl+D` to delete one (with a confirmation prompt,
and never the session you are currently on). If your query matches no existing
session, `Enter` creates a session with that name and switches to it.

Switching is not the same as detaching and reattaching: the client tears down
its view of the current session and builds a view of the target, in place. The
session you left keeps running.

## Attaching and Detaching

**Detach:** `Ctrl+B` `d`. The client pushes its current state to the daemon so
the session you come back to is the one you left, then quits. The session, its
windows and its shell processes keep running.

**Quit:** `Ctrl+B` `q`. This is not a detach. In a daemon session, quitting kills
the session, on the reasoning that quitting is the user saying the session is
over. A confirmation dialog appears first if a window is running a foreground
process; set `confirm_quit = true` to always show it.

**Exit terminal mode:** `Ctrl+B` `Esc`, or `Alt+Esc` as a direct shortcut. A
bare `Esc` in terminal mode is forwarded to the shell, as it must be for vim and
friends to work. Note that `Ctrl+B` `d` exits terminal mode only when there is no
daemon session to detach from; in a daemon session it detaches.

A client that dies without detaching (its terminal is closed, the SSH connection
drops, the process is killed) is equivalent to a detach as far as the session is
concerned. The daemon notices the connection go away and keeps the session
running. Nothing is lost, because nothing the session needs lived in the client.

## What Survives

Four tiers of interruption, and what comes back after each. "Structure" means
window count, geometry, workspace assignment, custom names, minimize state, the
BSP tree and the layout mode.

| | Client exits (detach, crash, SSH drop) | Daemon restart (`kill-server`, `SIGTERM`) | Daemon crash (`SIGKILL`, OOM) | Reboot |
|---|---|---|---|---|
| Session exists afterwards | Yes | Yes, restored on daemon start | Yes, restored on daemon start | Yes, restored on daemon start |
| Window structure | Yes | Yes | Partial: as of the last save, up to 30s stale | Partial: as of the last save |
| Shell processes | Yes, they keep running | No, fresh shells are spawned | No, fresh shells are spawned | No, fresh shells are spawned |
| Working directories | Yes | Yes, on Linux (see below) | Partial: the cwd from the last save | Partial: the cwd from the last save |
| Screen contents | Yes | No | No | No |
| Scrollback | Yes | No | No | No |
| Running programs (vim, tail, a build) | Yes | No | No | No |
| Copy-mode position, selection | No, per-client | No | No | No |
| Input mode (window vs terminal) | No, per-client | No | No | No |

The client column is the important one: a detach costs you nothing, because the
daemon holds the PTYs and keeps a terminal emulator fed for each. On reattach the
client asks the daemon for each window's screen and scrollback and repaints it.
Copy-mode state and input mode are deliberately per-client and are not restored,
so that one client entering terminal mode does not change what another client is
doing.

The three daemon columns are all resurrection, described next. Nothing survives
a daemon exit except what was written to disk.

## Resurrection

Resurrection is how a session comes back after the daemon that held it is gone.

Each live session writes its state to a JSON file every 30 seconds, and again on
a clean shutdown (`kill-server`, `SIGTERM` or `SIGINT`). The final save happens
while the shells are still alive, which is what makes the working directories in
it accurate. The write is atomic: a temp file is renamed into place, so a crash
mid-write cannot leave a half-written file where a good one used to be.

The state file holds the session's structure: its windows with their geometry,
titles, custom names, workspace, minimize state, its focus, its BSP trees, its
layout mode, and each window's working directory. It does not hold screen
contents, scrollback, or anything about the processes that were running.

When the daemon starts, it restores every session it finds saved state for. For
each window it spawns a **fresh shell** in that window's saved working directory
(falling back to the shell's default directory if the saved path no longer
exists), and writes a dim one-line notice into it:

```
-- tuios: session restored, fresh shell in /home/you/project --
```

Restored shells get `TUIOS_RESTORED=1` in their environment, so your shell rc can
react to a restore without relying on the banner.

What this means in practice: your layout comes back and each pane is sitting in
the right directory, but whatever was running in those panes is not. A `vim` you
had open is closed, a build you had running is dead, and the scrollback above the
prompt is empty.

Start the daemon with `--no-restore` to skip automatic restoration; saved state
is left on disk and can still be restored on demand with `tuios resurrect`.

A session killed with `tuios kill-session` has its saved state deleted, because
an explicit kill is a deliberate teardown and must not leave the session
restorable. Quitting a daemon session from inside the client (`Ctrl+B` `q`) kills
the session and so does the same.

If a state file is corrupt, or was written by a newer TUIOS whose format this
build does not understand, it is moved into an archive directory rather than
deleted, and skipped. One bad file can never block the daemon from starting or
prevent other sessions from being restored.

## The resurrect Command

```bash
tuios resurrect              # list the sessions that can be restored
tuios resurrect mysession    # restore that session and attach to it
```

With no arguments, `tuios resurrect` prints a table of every saved session with
its window count, whether it is already live, and how long ago its state was
saved.

With a name, it starts the daemon if necessary, asks it to restore that session
from saved state, and attaches. It is a no-op if the daemon already restored the
session on start, in which case you simply attach to the live one. `restore` is
an alias for the same command.

If the restore fails, the command says which of the reasons applies: there is no
saved state under that name, the state is corrupt, or the state was written by a
newer TUIOS. In the last two cases it also prints where the file was archived.

## Where State Lives

| What | Path |
|---|---|
| Saved session state | `$XDG_STATE_HOME/tuios/sessions/<name>.json` (typically `~/.local/state/tuios/sessions/`) |
| Archived bad state | `$XDG_STATE_HOME/tuios/sessions/archive/` |
| Daemon socket | `$XDG_RUNTIME_DIR/tuios/tuios.sock`, falling back to `/tmp/tuios-<uid>/tuios.sock` |
| Daemon PID file | the socket path with `.pid` appended |

The socket lives in the runtime directory and does not survive a reboot, which is
correct: the daemon does not either. Saved session state lives in the state
directory and does survive, which is why a session can be resurrected after a
reboot.

## Limitations

- **Screen contents and scrollback never survive the daemon.** They are held in
  the daemon's memory, not on disk. Only a detach preserves them.
- **Working directory capture is Linux-only.** The daemon reads
  `/proc/<pid>/cwd` to learn where each shell is. On platforms without procfs the
  read fails and restoration falls back to spawning the shell in its default
  directory. Everything else about the restore is unaffected.
- **A crash loses up to 30 seconds of structural change.** Saves are periodic;
  only a clean shutdown forces a final one. A window created 5 seconds before a
  `SIGKILL` will not be in the restored session.
- **Restored shells use the daemon's environment.** No client is connected at
  restore time, so the shell comes from the daemon process's `$SHELL` and
  inherits the daemon's environment, not that of whichever terminal you later
  attach from.
- **Resurrection restores structure, not work.** It is a way to get your layout
  and directories back, not a way to survive a crash without losing anything.

## Related Documentation

- [MULTI_CLIENT.md](MULTI_CLIENT.md) - several clients attached to one session
- [CLI_REFERENCE.md](CLI_REFERENCE.md) - every command-line option
- [protocol.md](protocol.md) - the JSON verb protocol for controlling the daemon
- [KEYBINDINGS.md](KEYBINDINGS.md) - default keybindings
- [HOOKS.md](HOOKS.md) - shell commands run on session and window events
