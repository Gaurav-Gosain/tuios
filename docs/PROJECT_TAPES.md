# Project Tapes (`.tuios.tape` autorun)

A project tape is a `.tuios.tape` file in a project directory. When the focused
shell inside TUIOS enters a directory that carries one, TUIOS can build a project
session and layout from it, the same way a `.envrc` file works for direnv.

Because a tape is arbitrary command execution (`Type "curl x | sh" Enter` is a
legal tape), the feature is built around a trust boundary: **an untrusted tape is
inert.** TUIOS only stats it, reads it once, hashes it, and shows it to you.
Nothing runs until you review the content and choose to run or trust it.

## Contents

- [Quick start](#quick-start)
- [The trust model](#the-trust-model)
- [The review dialog](#the-review-dialog)
- [Config: `autorun` modes](#config-autorun-modes)
- [The tape header](#the-tape-header)
- [Scope: what running a tape does](#scope-what-running-a-tape-does)
- [Security properties](#security-properties)

## Quick start

1. A `.tuios.tape` lives in a project you `cd` into inside TUIOS.
2. About 400ms later a passive banner slides in and a `tape ?` badge appears in
   the dock. Nothing has executed. Your shell never lost a keystroke.
3. Press `Ctrl+B` `T` `t` (or open the command palette with `Ctrl+P` and choose
   **Tape: Review Project Tape**). A dialog shows the tape's path, its trust
   status, what it will build, and its full content.
4. Read it, then choose an action:
   - **`r` Run once** - run it now without remembering the decision.
   - **`t` Trust and run** - remember this exact file and run it. Future visits
     follow your configured `autorun` mode.
   - **`n` Never** - never prompt for this path again.
   - **`Esc` Not now** - dismiss; the dock badge stays so it is still reachable.
5. On a later visit the badge reads `tape ✓` (trusted); one keypress runs it,
   which just switches you to the still-existing project session.

## The trust model

Trust is granted per **(canonical path, content hash)** pair, using direnv's
model:

- The path is the `realpath` of the tape; the hash is SHA-256 of its exact bytes.
- **Any edit to the file changes the hash and silently reverts it to untrusted.**
  A `git pull` that changes a trusted tape shows up as an untrusted, "changed
  since you trusted it" encounter, never a silent run.
- Approval and execution use the **same in-memory bytes**. TUIOS reads the file
  once, hashes that buffer, shows that buffer, and runs that buffer. The file on
  disk is never re-read between review and execution, so swapping the file (or a
  symlink target) after you approve changes nothing about what runs.

Trust decisions live in `$XDG_DATA_HOME/tuios/tape-trust.toml` (mode 0600). It is
per-machine state and does not travel with dotfile syncing.

### Ineligible tapes

Before a tape is even offered for trust it must pass hygiene checks, like
`sshd` applies to `authorized_keys`:

- a regular file (after symlink resolution), owned by you, not group- or
  world-writable;
- no world-writable directory (without the sticky bit) on the path to it;
- at most 64 KiB.

A tape that fails is **ineligible**: the dialog shows why and offers only
*Dismiss*. There is no override. This removes the "click through the warning"
failure mode on shared or world-writable directories.

### Denied tapes

**Never** records a deny entry keyed by **path only** (no hash), so editing the
file cannot nag you back into a prompt. A denied path produces no banner, no
badge, and no dialog until you clear it.

## The review dialog

The dialog is only ever opened deliberately - by `Ctrl+B` `T` `t`, the command
palette, or (in `auto` mode, for a still-untrusted tape) the passive path. It
never steals focus because you typed `cd`. It shows:

- the tape's canonical path and trust status (including "changed since you
  trusted it" when a trusted tape was edited);
- a one-line summary of what running it does (e.g. `session "myproject"`), from a
  cheap header parse that executes nothing;
- the **full tape content**, scrollable with `↑`/`↓`. This is the security
  boundary: you approve what you can see.

## Config: `autorun` modes

```toml
[tape]
autorun = "ask"   # off | ask | auto
```

- **`off`** - no scanning, no indicators, feature invisible.
- **`ask`** (default) - detection on; every encounter surfaces the passive banner
  and badge. Nothing runs without you opening the dialog and choosing Run.
- **`auto`** - a trusted, unedited tape runs automatically on entry. An untrusted
  or changed tape behaves exactly as in `ask`: banner, badge, dialog, never an
  autorun. There is no mode in which unreviewed content executes.

The environment variable `TUIOS_TAPE_AUTORUN` overrides the config for one run
(`TUIOS_TAPE_AUTORUN=off tuios ...`), useful for CI or poking at hostile code.

## The tape header

A project tape has an optional declarative header at the very top, before any
body command:

| Directive | Meaning | Default |
|-----------|---------|---------|
| `Session "name"` | Target session name | project directory basename |
| `Scope session\|current` | Where the tape runs | `session` |
| `Workspace N` | Workspace to build in | none |
| `Require "command"` | Skip with a notice if a binary is missing | - |

Header directives must precede any body command; a directive that appears after a
body command is treated as body. A tape with no header runs with the defaults.

## The tape body

The body is a small, explicit layout language - a defined subset tuned for
building a project layout, not the full [recorder tape language](TAPE_SCRIPTING.md)
(whose one-command-per-line grammar cannot express `Type "x" Enter`,
`Split vertical`, or `Focus "name"` the way a project tape needs). One command per
line, keyword case-insensitive:

| Command | Effect |
|---------|--------|
| `Type "text" [Enter]` | Type text into the focused pane; optional `Enter` submits it |
| `Run "cmd"` | Shorthand for `Type "cmd" Enter` |
| `Enter` | Submit a line in the focused pane |
| `Split vertical\|horizontal` | Split the focused pane into a new tiled pane (`v`/`h` accepted) |
| `NewWindow ["name"]` | Create a new tiled pane |
| `RenameWindow "name"` | Name the focused pane (`Rename` is an alias) |
| `Focus "name"` | Focus a pane by name |
| `Sleep <duration>` | Pause (e.g. `500ms`, `1s`) |
| `EnableTiling` / `DisableTiling` | Toggle tiling |

Blank lines and lines starting with `#` are ignored, as is any unrecognized
command. A settle delay is inserted after each `Split`/`NewWindow` so the
asynchronously created pane is ready before the next command types into it.

A typical project tape:

```
# .tuios.tape for myproject
Session "myproject"
Require "pnpm"

RenameWindow "edit"
Type "nvim ." Enter

Split vertical
RenameWindow "serve"
Type "pnpm dev" Enter

Split horizontal
RenameWindow "sh"

Focus "edit"
```

## Scope: what running a tape does

### `Scope session` (default): session per project

When a tape runs, TUIOS:

1. Derives a session name (explicit `Session`, else the project basename).
2. **If a session with that name already exists, it switches to it and does not
   rebuild.** The session is the durable artifact; the tape is its constructor,
   run once. Re-entering the project never duplicates panes.
3. Otherwise it creates the session, seeds a window whose shell starts at the
   project root, builds the layout from the tape body, and switches you there.

This is the tmux-sessionizer model: re-entry is always "take me to the myproject
session", whether that means building it or just switching. Because the tape
always starts from a known state (fresh session, one window at the project root),
a keystroke script is deterministic instead of composing with whatever windows
happened to be open.

Session scope requires a daemon-backed session (`tuios new ...`). Outside one,
TUIOS falls back to running the tape in the current session and says so.

### `Scope current`: opt-in, best-effort

`Scope current` applies the tape to the current session, starting from the
focused window. It composes with whatever state exists, so it is honest
best-effort - good for tiny tapes ("split once, run `make watch`").

### Requirements

`Require "pnpm"` skips the whole tape with a notice if `pnpm` is not on `PATH`,
rather than typing a command into a shell that cannot run it.

## Security properties

- Only a trusted, unedited tape ever runs without a keypress (`auto` mode).
- Run once and Trust and run act on content you reviewed in the dialog.
- Execution uses the exact bytes hashed at review time, never a fresh read.
- A denied path never prompts or runs.
- A tape edited since it was trusted reverts to untrusted and re-prompts; it does
  not silently run the old trusted version.
- Detection is suppressed entirely while a tape is running, and a project root
  handled this run does not re-trigger, so a tape that `cd`s onward cannot chain
  another run.
- Remote (SSH) shells are ignored: TUIOS cannot read or verify a remote file, so
  it neither prompts nor runs.
