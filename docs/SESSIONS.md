# Sessions

TUIOS runs in one of two modes: a daemon session that lives in a background
process and survives the client that draws it, and a local session that lives
and dies with the process you started. A plain `tuios` gives you a daemon
session. This document covers both, what attaching and detaching do, and exactly
what does and does not come back after each kind of interruption.

> **Note:** `Ctrl+B` is the default leader key throughout. It is configurable via
> `leader_key`, see [CONFIGURATION.md](CONFIGURATION.md).

## Table of Contents

- [Local Sessions](#local-sessions)
- [Daemon Sessions](#daemon-sessions)
- [Attaching and Detaching](#attaching-and-detaching)
- [What Survives](#what-survives)
- [Resurrection](#resurrection)
- [The resurrect Command](#the-resurrect-command)
- [Windows on Another Machine](#windows-on-another-machine)
- [Global Sessions](#global-sessions)
- [Machines on a Tailnet](#machines-on-a-tailnet)
- [Where State Lives](#where-state-lives)
- [Limitations](#limitations)
- [Related Documentation](#related-documentation)

## Local Sessions

```bash
tuios --standalone
```

A local session keeps everything, the window manager, the terminal emulators and
the shell processes, inside one process. No daemon is started, no socket is
created and no state is written to disk. When the process exits, for any reason,
the session is gone: there is nothing to attach to and nothing to restore.

Local sessions are the right choice when you want a window manager for the
lifetime of one terminal window. Everything in this document from
[Attaching and Detaching](#attaching-and-detaching) onward applies only to
daemon sessions.

Ask for a local session in one of three ways:

```bash
tuios --standalone           # this one run
TUIOS_NO_DAEMON=1 tuios      # every tuios in this shell
```

```toml
[startup]
daemon = false               # every tuios, from the config file
```

The flag and the environment variable both beat the config file. They are the
way back to a terminal when the daemon is the thing that will not start.

## Daemon Sessions

```bash
tuios
```

Running `tuios` with no subcommand attaches to a daemon-backed session, and
starts the daemon first if none is running. This is `startup.daemon`, and it
ships on. A daemon that will not start does not leave you without a terminal:
`tuios` says so and runs the session standalone for that one run.

An install that already has a config file keeps what that file says. The
`[startup]` booleans are only read from the file, so `tuios` on an existing
machine goes on doing what it did.

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
attached client's size.

Every attached client has full control: it sees all output, sends input, and
manipulates windows. There is no per-client permission tier, so share a session
only with people you would hand the keyboard to. Local clients are gated by the
socket's Unix permissions (same user only), SSH clients by SSH authentication,
and web clients by whatever stands in front of `tuios-web`. See
[Multi-client sessions](https://tuios.gaurav.zip/docs/sessions) on the site for
the full picture.

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
| Window structure | Yes | Yes | Partial: as of the last save, a couple of seconds stale | Partial: as of the last save |
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

Each live session writes its state to a JSON file within a couple of seconds of
any change to its structure, again every 30 seconds whether or not anything
changed (which is how each window's working directory stays current, since
typing `cd` changes no structure), and once more on a clean shutdown
(`kill-server`, `SIGTERM` or `SIGINT`). A session nothing has changed costs one
write per 30 seconds, as it always did. The final save happens
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

The session itself is marked too, so you can tell a session that just came back
from one that has been running for days without opening a pane. A restored
session shows a `restored` tag in `tuios ls`, in the sidebar and in the session
switcher, and `tuios attach` says so before it hands over the screen:

```
Session "work" was restored: layout came back from saved state; the shells are new.
```

The mark is cleared by the first attach, on the reasoning that once you have
looked at the session the question has been answered. It never comes back for
that session unless the daemon restores it again.

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

## Windows on Another Machine

A window's process does not have to run on the machine the session is on.

```bash
tuios new-window deploy --host build
```

The window belongs to the session it was created in. It is drawn here, sized by
the layout here, and closed here; only the process is on `build`. The machine
comes from the `[hosts]` table, the same one `tuios hosts` lists, and a name
that is not in it is refused before anything is started.

A session holding such a window is still an ordinary session, so `tuios ls`, the
verbs, the mailbox, hooks and resurrection keep working on it with no special
case. What makes the window different is one field recording where its process
is.

Panes on two machines can sit side by side in one layout, because each pane is
a window of this session and the layout does not care where any of their
processes are.

### What it looks like

A pane whose shell is elsewhere says so on its title bar, as `build:name`. This
is not optional: two panes side by side are otherwise identical, and the same
typed line is a different act depending on which machine answers it.

### What crosses, and what does not

The machine supplying the process supplies a process and a pty, and nothing
else. It runs no terminal emulator for the pane and keeps no scrollback for it,
and it does not know which session the pane belongs to. All of that is here, on
the daemon that owns the window, exactly as it is for a pane of its own.

That division has a consequence worth knowing: the pane is **not** a window of
any session on the other machine. It will not appear in `tuios ls` there, and
it is not enrolled in that machine's size negotiation, so a layout here can
never shrink a session someone is working in there.

### What it needs

Both machines need a tuios new enough to speak `open-pane`. An older one
refuses by name and says to update it.

### Limits

- **The window ends when the link does.** The process is reached over the link,
  so losing the link ends the pane, the same way closing it does. It does not
  come back on redial.
- **A resurrected session brings the window back on this machine.** Resurrection
  respawns a shell from saved state, and it does not redial a host to do it.
- **A resurrected window runs a local shell.** The layout comes back and the
  pane in it is on this machine, whatever it said before.

## Global Sessions

A session whose panes are all on one machine is that machine's session. A
session holding panes from several is not, and the rail says so: once a second
machine is reachable, it draws a group called `global` above the machines.

```
global                +
  deploy
local                 +
  work
build                 +
  api
```

Sessions in the global group work like any other. What is different is that
every way of making a window in one asks which machine it should run on, by
every route: the key, the rail's `+`, the command palette. That question has one
sensible answer in an ordinary session and is worth asking in this one, which is
why the picker appears here and nowhere else.

Make one from the `+` on the group header, or from the shell:

```bash
tuios new deploy --global
```

A global session is created with no windows, since the first pane is the one you
pick a machine for.

The group holds as many sessions as you make. It is drawn above the machines
because a global session is not any machine's: it is held by a daemon, the way
any session is, but where it is held says nothing about where its panes run.

Turn the group off with `global_session = false` in the config. Sessions that
already exist stay listed.

## Machines on a Tailnet

If this machine is on a [Tailscale](https://tailscale.com) tailnet, tuios can
list the machines on it and offer them as addresses:

```bash
tuios hosts tailnet
```

```
   arch-btw          arch-btw.example.ts.net          offline
 + ente              ente.example.ts.net
 = forgejo           forgejo.example.ts.net           already the host forgejo
   my-phone          my-phone.example.ts.net          cannot run tuios (iOS)
```

Add one:

```bash
tuios hosts add ente --tailnet
```

**Nothing is added on its own.** This is the same rule the ssh_config aliases
follow: a host exists because you named it. What is discovered is what to type,
not what to connect to.

**Nothing new is dialled either.** A host added this way is reached over ssh like
every other host. A MagicDNS name resolves like any other name, so the tailnet is
how the name resolves and how the traffic is carried, and tuios does not open a
tailnet connection itself. That also means ssh over a tailnet already worked
before this existed: you could always write the MagicDNS name as an address by
hand. This saves you the typing and tells you what is there.

tuios asks the `tailscaled` already running on this machine, through the local
API, which is the same thing `tailscale status` asks. It needs no root, no
auth key, and no operator setting. A machine with no tailscale on it gets an
empty list and behaves exactly as it did before.

Every machine is listed, offered or not, and one that is not offered says why.
By default a machine is left out when it is offline, when it is this machine,
when it was shared in from another tailnet, or when it runs an operating system
that cannot host a tuios daemon.

Change any of that in the `[tailscale]` table:

```toml
[tailscale]
# Offer tailnet machines as addresses at all.
enabled = true
# Which form of address: "dns" is the MagicDNS name and works anywhere on the
# tailnet, "name" is the short name and needs a search domain, "ip" is the
# 100.x address and needs no DNS.
addr = "dns"
# An ssh login put in front of every address.
user = "ubuntu"
# The operating systems to offer. An empty list offers every machine.
os = ["linux", "macOS", "windows"]
# Offer machines that are offline, this machine, and machines shared in.
offline = false
self = false
shared = false
# Glob patterns matched against the short name and the MagicDNS name.
# exclude wins over include.
include = ["*"]
exclude = ["*-pad-*"]
# How many to offer.
max = 50
# Where the tailscaled local API socket is, if it is not in the usual place.
socket = ""

# Per-machine logins, which win over the user above. This has to come last,
# because everything after a sub-table heading belongs to it.
[tailscale.users]
build = "root"
```

For a script or an agent, `tuios hosts tailnet --json` gives every machine with
`offered` and, when it is false, `skipped` saying which rule left it out.

## Where State Lives

| What | Path |
|---|---|
| Saved session state | `$XDG_STATE_HOME/tuios/sessions/<name>.json` (typically `~/.local/state/tuios/sessions/`) |
| Archived bad state | `$XDG_STATE_HOME/tuios/sessions/archive/`, pruned after 14 days |
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
- **A crash loses the last couple of seconds of structural change, and up to 30
  seconds of working-directory drift.** Structural changes are saved within a
  couple of seconds; the directory each shell is sitting in is captured on the
  30-second tick, so a `cd` immediately before a `SIGKILL` may not survive.
- **Restored shells use the daemon's environment.** No client is connected at
  restore time, so the shell comes from the daemon process's `$SHELL` and
  inherits the daemon's environment, not that of whichever terminal you later
  attach from.
- **Resurrection restores structure, not work.** It is a way to get your layout
  and directories back, not a way to survive a crash without losing anything.

## Related Documentation

- [CLI_REFERENCE.md](CLI_REFERENCE.md) - every command-line option
- [protocol.md](protocol.md) - the JSON verb protocol for controlling the daemon
- [KEYBINDINGS.md](KEYBINDINGS.md) - default keybindings
- [HOOKS.md](HOOKS.md) - shell commands run on session and window events
