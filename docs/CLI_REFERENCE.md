# CLI Reference

This document provides a complete reference for TUIOS command-line interface.

## Table of Contents

- [Overview](#overview)
- [Installation](#installation)
- [Usage](#usage)
- [Commands](#commands)
  - [Root Command](#root-command)
  - [Theming](#theming)
  - [Agent Skill](#agent-skill)
  - [Daemon Mode (Session Persistence)](#daemon-mode-session-persistence)
  - [Remote Control Commands](#remote-control-commands)
  - [Inspection Commands](#inspection-commands)
  - [More Commands](#more-commands)
  - [Scripting Examples](#scripting-examples)
  - [tuios ssh](#tuios-ssh)
  - [tuios-web (separate binary)](#tuios-web-separate-binary)
  - [tuios config](#tuios-config)
  - [tuios keybinds](#tuios-keybinds)
  - [tuios update](#tuios-update)
  - [tuios layout](#tuios-layout)
  - [tuios completion](#tuios-completion)
  - [tuios help](#tuios-help)
- [Global Flags](#global-flags)
- [Common Usage Examples](#common-usage-examples)
- [Environment Variables](#environment-variables)
- [When Something Goes Wrong](#when-something-goes-wrong)
- [Exit Codes](#exit-codes)
- [Version Information](#version-information)
- [Command Migration Guide](#command-migration-guide)
- [Related Documentation](#related-documentation)

## Overview

TUIOS uses a modern command-line interface built with Cobra and Fang, providing:
- Subcommand structure for better organization
- Styled help output and error messages
- Shell completion generation

## Installation

### Homebrew (macOS/Linux)

```bash
brew install tuios
```

### Arch Linux (AUR)

```bash
# Using yay
yay -S tuios-bin

# Using paru
paru -S tuios-bin
```

### Nix

```bash
# Run directly
nix run github:Gaurav-Gosain/tuios#tuios

# Or add to your configuration
nix-shell -p tuios
```

### Quick Install Script (Linux/macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/Gaurav-Gosain/tuios/main/install.sh | bash
```

### Go Install

```bash
go install github.com/Gaurav-Gosain/tuios/cmd/tuios@latest
```

### Pre-built Binaries

Download from [GitHub Releases](https://github.com/Gaurav-Gosain/tuios/releases)

---

## Usage

```bash
tuios [command] [flags]
```

## Commands

### Root Command

Run TUIOS. A plain `tuios` attaches to a daemon-backed session, because
`startup.daemon` ships on. Add `--standalone` for a local session that lives and
dies with this process:

```bash
tuios
tuios --standalone
```

**Flags:**

`tuios --help` is the authoritative list. These are the flags it shows today:

- `--standalone`: Run a standalone session without the daemon, overriding `startup.daemon`. `TUIOS_NO_DAEMON=1` does the same for a whole shell
- `--theme <name>`: Color theme to use, such as dracula, nord or tokyonight. Leave it empty (the default) to use the terminal's own colors without theming
- `--list-themes`: List all available themes and exit
- `--preview-theme <name>`: Preview a theme's 16 ANSI colors and exit
- `--skill [topic]`: Print the embedded agent skill and exit: the core, one topic, or `all` (see [Agent Skill](#agent-skill))
- `--ascii-only`: Use ASCII characters instead of Nerd Font icons
- `--show-keys`: Enable showkeys overlay (screencaster-style key display)
- `--border-style <style>`: Window border style: rounded, normal, thick, double, hidden, block, ascii, outer-half-block, inner-half-block, glyphs (default: from config or rounded)
- `--dockbar-position <pos>`: Dockbar position: bottom, top, hidden (default: from config or top)
- `--hide-window-buttons`: Hide window control buttons (minimize, maximize, close)
- `--window-button-style <style>`: How the window controls are drawn: `dots` (default, macOS traffic lights) or `pill`
- `--window-button-position <position>`: Which end of the title bar the window controls sit on: `left` (default, macOS) or `right`
- `--window-title-position <pos>`: Window title position: bottom, top, hidden (default: from config or top)
- `--scrollback-lines <num>`: Number of lines in scrollback buffer (default: from config or 10000, 100 to 1000000)
- `--hide-scrollbar`: Hide the window scrollbar thumb on the border
- `--zoom-max-width <cells>`: Max width in cells for zoom mode (0 is fullscreen)
- `--confirm-quit`: Always show the quit confirmation dialog
- `--hide-clock`: Hide the clock overlay (deprecated, the clock is hidden by default)
- `--show-clock`: Show the clock overlay
- `--show-cpu`: Show a CPU graph in the dock
- `--show-ram`: Show RAM usage in the dock
- `--no-animations`: Disable UI animations for instant transitions
- `--shared-borders`: Share borders between adjacent tiled windows
- `--debug`: Enable debug logging
- `--cpuprofile <file>`: Write CPU profile to file
- `--pprof <addr>`: Serve /debug/pprof profiles on this address for live profiling, such as localhost:6060. Delta profiles (`?seconds=` on heap and the like) are not served; take two and compare them with `go tool pprof -diff_base`
- `-h, --help`: Show help for tuios
- `-v, --version`: Show version information

**Examples:**
```bash
tuios                          # Start TUIOS normally
tuios --theme dracula          # Start with Dracula theme
tuios --ascii-only             # Start without Nerd Font icons
tuios --show-keys              # Start with showkeys overlay enabled
tuios --list-themes            # List all available themes
tuios --preview-theme nord     # Preview Nord theme colors
tuios --skill                  # Print the agent skill and exit
tuios --skill recipes          # Print one topic of it
tuios --debug                  # Start with debug logging
tuios --cpuprofile cpu.prof    # Start with CPU profiling

# Combine multiple flags
tuios --theme nord --show-keys # Use Nord theme with showkeys enabled

# Interactive theme selection with fzf
tuios --theme $(tuios --list-themes | fzf --preview 'tuios --preview-theme {}')
```

---

## Theming

TUIOS includes 300+ built-in color themes from various sources including Gogh, iTerm2, and custom themes.

### Available Themes

List all available themes:
```bash
tuios --list-themes
```

With no theme set, tuios uses the terminal's own colors.

**Popular themes include:**
- `tokyonight`: A clean, dark theme with vibrant colors
- `dracula`: Dark theme with purple accent
- `nord`: An arctic, north-bluish color palette
- `gruvbox_dark`: Retro groove color scheme
- `catppuccin_mocha`: Soothing pastel theme
- `monokai_pro`: Professional dark theme
- `solarized_dark`: Precision colors for machines and people
- `github`: GitHub's light theme
- `one_dark`: Atom's iconic dark theme

### Preview Themes

Preview a theme's 16 ANSI colors before using it:
```bash
tuios --preview-theme dracula
```

The preview shows all 16 colors (8 standard + 8 bright variants) with their color codes.

### Using Themes

Set a theme at startup:
```bash
tuios --theme nord
```

The theme affects:
- Terminal text colors (ANSI 0-15)
- Window borders
- UI elements (status bar, dock, overlays)
- Default foreground/background colors

**Note:** The theme only affects the 16 base ANSI colors. Applications using 256-color or true color (RGB) will display those colors unchanged.

### Interactive Theme Selection

Use `fzf` for interactive theme selection with live preview:
```bash
tuios --theme $(tuios --list-themes | fzf --preview 'tuios --preview-theme {}')
```

This allows you to browse all themes with a live color preview before selecting one.

### Theme Persistence

Themes are set via command-line flag and not currently stored in configuration. To always use a specific theme:

**Shell alias:**
```bash
# Add to ~/.bashrc, ~/.zshrc, etc.
alias tuios='tuios --theme nord'
```

**Script wrapper:**
```bash
#!/bin/bash
exec tuios --theme dracula "$@"
```

---

## Agent Skill

`tuios --skill` prints the agent skill embedded in the binary and exits. The
skill teaches an agent to drive TUIOS from inside a pane. It is split so an
agent loads only what it needs:

- `tuios --skill` prints the core (about 250 lines): how to tell it is in a
  pane, what its pane may do, addressing, reading and writing panes, running
  work and waiting for it, reporting its own state, talking to other agents and
  the person safely, and a table of the topics.
- `tuios --skill TOPIC` prints one topic: `panes`, `state`, `inbox`, `mail`,
  `fleet`, `hosts`, `events`, `mcp`, `tmux`, `grants`, `config`, `errors` or
  `recipes`. `recipes` has end-to-end recipes: a fleet of agents, answering
  from the Inbox, approvals, agents on another machine, MCP setup, the tmux
  shim, scoped grants, a conductor pane and a phone alert.
- `tuios --skill all` prints the core and every topic.
- An unknown topic is an error that lists the topics.

```bash
tuios --skill
tuios --skill fleet
tuios --skill all
```

The text ships inside the binary as `skills/tuios/SKILL.md` and the other
files in `skills/tuios/`, so it always describes the TUIOS that printed it.
Nothing is fetched and no daemon is needed. `tuios --skill=TOPIC` works too.

**Examples:**
```bash
# Install the core where an agent harness looks for skills. The core tells
# the agent to run tuios --skill TOPIC for the rest.
mkdir -p ~/.claude/skills/tuios
tuios --skill > ~/.claude/skills/tuios/SKILL.md
```

---

## Daemon Mode (Session Persistence)

TUIOS supports persistent sessions through a daemon process, similar to tmux or screen. Sessions continue running in the background even when you disconnect, allowing you to reattach later with all windows and content preserved.

### `tuios new`

Create a new persistent session.

**Usage:**
```bash
tuios new [session-name] [flags]
```

**Flags:**
- `--theme <name>`: Set color theme for the session
- `--ascii-only`: Use ASCII characters instead of Nerd Font icons
- `--show-keys`: Enable showkeys overlay
- `--no-animations`: Disable UI animations
- `-d, --detach`: Create the session headless without attaching a client
- `--host <name>`: Create the session on this host from the `[hosts]` table
- `--ssh`: With `--host`, run ssh to the host and its own tuios instead of attaching here
- `--global`: Create a global session, which holds panes from more than one machine
- `--hold`: After a failure, wait for enter before the command exits
- The appearance flags of the root command (`tuios new --help` lists them)

**Examples:**
```bash
tuios new                      # Create session with auto-generated name
tuios new mysession            # Create session named "mysession"
tuios new work --theme dracula # Create session with Dracula theme
tuios new ci --detach          # Create a headless session and return
tuios new --host build         # Create a session on the host build and attach it
tuios new deploy --global      # Create a global session
```

### `tuios attach`

Attach to an existing session.

**Usage:**
```bash
tuios attach [session-name] [flags]
```

**Flags:**
- `-c, --create`: Create session if it doesn't exist
- `--host <name>`: Attach to a session on this host from the `[hosts]` table
- `--ssh`: With `--host`, run ssh to the host and its own tuios instead of attaching here
- `--hold`: After a failure, wait for enter before the command exits
- Same as `tuios new` (theme, ascii-only, etc.)

**Examples:**
```bash
tuios attach                   # Attach to most recent session (or only session)
tuios attach mysession         # Attach to session named "mysession"
tuios attach mysession -c      # Attach or create if doesn't exist
tuios attach mysession --theme nord  # Attach with different theme
tuios attach --host build api  # Attach the session api on the host build
```

### `tuios ls`

List all TUIOS sessions.

**Usage:**
```bash
tuios ls [flags]
```

**Flags:**
- `--json`: Output as JSON. Saved sessions carry `"saved": true`
- `--all-hosts`: Also list the sessions on every host in the `[hosts]` table. A host that does not answer gets a row saying so and never fails the command. Its sessions from when it last answered follow, under "As of ..., when it last answered", and are not counted in the total
- `--host <name>`: List the sessions on one host (`local` means this machine)

With no daemon running, `tuios ls` lists the sessions saved on disk instead,
marked `saved`, and exits 3.

**Output:**
Shows a table with:
- Session name
- Number of windows
- Status (attached/detached)
- Creation time
- Last activity time

**Example output:**
```
╭───────────────┬─────────┬──────────┬───────────────┬─────────────────╮
│ NAME          │ WINDOWS │ STATUS   │ CREATED       │ LAST ACTIVE     │
├───────────────┼─────────┼──────────┼───────────────┼─────────────────┤
│ work          │ 3       │ detached │ 2 hours ago   │ 5 mins ago      │
│ dev           │ 2       │ attached │ 1 day ago     │ just now        │
╰───────────────┴─────────┴──────────┴───────────────┴─────────────────╯
```

`tuios ls --json` prints one object per session. Two fields say where the session's focused pane is:

- `dir` is the base name of the shell's directory. It is `~` for the home directory.
- `branch` is the git branch checked out there. A detached HEAD reads as its short hash.

Both come from the directory the shell last reported over OSC 7, or the directory the shell started in. Both are omitted when unknown. The sidebar labels a session with a generated name (`session-0`) by `dir`, and shows `branch` after any session name.

A session whose directory is inside a linked git worktree (not a main
checkout) also carries `worktree`: the repository (`repo`, `repo_root`), the
`branch` and the worktree's `path`, `gone: true` once the directory no longer
exists, and for one tuios made, its `base`, its fan `group` and the prompt a
fan sent it. It is what the rail groups by. `global: true` marks a global session, and
`restored: true` one rebuilt from saved state that nobody has attached to yet.

### `tuios kill-session`

Kill a specific session.

**Usage:**
```bash
tuios kill-session <session-name>
```

**Examples:**
```bash
tuios kill-session mysession   # Kill session named "mysession"
```

### `tuios worktree`

A git worktree as a session. `worktree new` makes a worktree of the repository
you are in and a session whose directory is that worktree. The rail groups these
sessions under the repository and labels each by its branch.

**Usage:**
```bash
tuios worktree new <branch> [--repo <dir>] [--base <ref>] [--name <session>] [--agent <cli>] [--host <host> [--clone]] [--detach]
tuios worktree ls [--repo <name>] [--group <stem>] [--host <host>] [--json]
tuios worktree rm [<host>:]<session> [--stash | --force] [--keep-session]
tuios worktree diff <session> [--stat]
tuios worktree pull <host>:<session> [--repo <dir>] [--branch <name>] [--name <session>] [--detach] [--json]
```

**Examples:**
```bash
tuios worktree new feat/retry                          # A new branch from HEAD, attached
tuios worktree new feat/retry --agent claude --detach  # Headless, running Claude Code
tuios worktree ls                                      # Repo, branch, agent state, changes
tuios worktree rm api-feat-retry --stash               # Keep the changes in git stash
```

The session is named `<repo>-<branch>`, with every slash turned into a hyphen.
Worktrees go under `$XDG_DATA_HOME/tuios/worktrees/<repo>/<branch>`.

`worktree rm` refuses a worktree with uncommitted changes and removes nothing.
`--stash` keeps the changes in git stash as `tuios: <branch>`. `--force`
discards them, and is the only option that does. The branch is never deleted.
A session whose worktree directory was removed under it shows `gone` in the
listing and is kept.

**On another machine.** `--host` runs `worktree new` and `worktree ls` on a
machine from the `[hosts]` table, and `worktree rm` takes `HOST:SESSION`. Run
them inside your checkout: the repository is sent as its origin URL, and the
host finds its own checkout of it under `[hosts.NAME] repos_root` (set with
`tuios hosts add NAME ADDR --repos-root ~/src`), or else under `~/src`,
`~/dev`, `~/code`, `~/projects`, `~/repos`, `~/git`, `~/work` and `~/go/src`
there. `--clone` clones it there when it has none; only https, ssh and git URLs
are cloned. With `--host`, `--repo` names a directory on the host. A host whose
tuios is too old for this is named, with what to upgrade.

`worktree diff` reads files on this machine, so it refuses `HOST:SESSION` and
names `worktree pull`.

**Pulling work back.** `worktree pull HOST:SESSION` copies a worktree session's
work on another machine into a new worktree session here. Its commits cross as
a git bundle and are fetched into a new branch of the repository you are in,
named as there or by `--branch`. The branch carried is the one the worktree's
HEAD is on now, so an agent that made its own branch there has that branch
pulled. A worktree with a detached HEAD is refused. When the commits cannot be
fetched, or the branch they make does not end where the other machine said,
the new branch is removed again, so the pull can simply be run again. Its uncommitted work, untracked files
included, crosses as a patch and is applied, uncommitted, in the new worktree.
Only the commits past the worktree's base cross when this repository has the
base commit, and the whole branch otherwise. A branch that already exists here
is refused and nothing is overwritten. A repository here whose origin differs
from the one there is refused. Nothing on the other machine changes. When the
patch does not apply, it is kept under the temporary directory and the command
names it.

```bash
tuios worktree new feat/retry --host build --clone --detach
tuios worktree ls --host build
tuios worktree pull build:api-feat-retry --detach
tuios worktree rm build:api-feat-retry --stash
```

### `tuios fan`

Fan a prompt out across several agents, each in its own worktree.

**Usage:**
```bash
tuios fan <count> --agent <agent>[,<agent>...] [--env NAME[=VALUE]]... [--repo <dir>] [--base <ref>] [--name <stem>] [--host <host> [--clone]] [--wait] <prompt>
tuios fan [<count>] --agent <agent>[,<agent>...] --prompt <prompt> --prompt <prompt>... [flags]
tuios fan keep [<host>:]<session> [--stash | --force]
tuios fan compare [[<host>:]<session>] [--no-changes] [--json]
tuios fan verify [<host>:]<session> [--timeout <duration>] [--no-wait] [--json] -- <command>...
tuios fan diff <session-a> <session-b> [--stat]
```

**Flags:**
- `--agent <agent>`: The agent as you would type it, arguments included: `claude`, `'codex --model o5'`, or any program. Several, comma-separated or repeated, are cycled across the sessions (required)
- `--prompt <prompt>`: One session's prompt, repeated once per session, in place of the prompt argument. The count is then how many there are
- `--env NAME` or `--env NAME=VALUE`: Pass your value of a variable, or set one, for every agent. Repeatable. `PATH` is always sent, so the agents are found where your shell finds them
- `--grants <grant>[,<grant>...]`: What every agent may do through tuios, as for `start-agent`
- `--repo <dir>`, `--base <ref>`, `--name <stem>`, `--wait`, `--json`

**Examples:**
```bash
tuios fan 3 --agent claude 'Add a retry with backoff to the HTTP client.'
tuios fan 3 --agent 'claude,codex --model o5,gemini' 'Add a retry with backoff.'
tuios fan --agent claude --env ANTHROPIC_API_KEY --prompt 'Add a retry.' --prompt 'Add a timeout.'
tuios worktree ls --group fan/add-retry-backoff-http   # Which prompts were sent, what changed
tuios worktree diff api-fan-add-retry-backoff-http-2   # What one of them produced
tuios fan compare api-fan-add-retry-backoff-http       # Every attempt side by side
tuios fan verify api-fan-add-retry-backoff-http -- go test ./...
tuios fan diff api-fan-add-retry-backoff-http api-fan-add-retry-backoff-http-2
tuios fan keep api-fan-add-retry-backoff-http-2 --stash
```

The branches are a stem, then `stem-2`, `stem-3`. The stem is `fan/` and the
first words of the prompt, or `--name`. Each prompt is typed once its agent
shows it is at its prompt, as one paste submitted with a carriage return (the
Enter key), so the command returns at once and `tuios worktree ls` shows
`pending`, `held: look at the pane`, `sent`, `not sent` or `stalled` per
session. `held` means the agent has not been ready for 30 seconds, most often
because it shows a first-run choice; the Inbox asks you to look, and the
prompt is typed once it is ready. A program no harness manifest recognises is
ready only once it reports a state (`tuios set-agent-state idle`). `stalled`
means the prompt was typed and the agent showed no sign of taking it within
five seconds: it did not turn working or needs_input. Look at the pane before
sending it again, since the text may be in the agent's input box. `--wait`
blocks until every prompt is sent or given up on.

The agent string is split into words the way a shell splits them and exec'd
directly: nothing in it is expanded. `TUIOS_` variables, `TMUX` and
`TMUX_PANE` cannot be passed with `--env`.

`fan compare` prints one line per attempt of the fan the session belongs to:
its agent and state, the files and lines it changed against the fan's base
(committed or not, untracked files included, ignored files left out), and its
last check. The check is the last `fan verify` result, or else the last
command a shell in the session finished, from its OSC 133 marks. The counts
run from where each attempt left the base, so a base that moves on after the
fan started (a fetch, or one attempt merged into it) does not change them.
They run git in every worktree, at most 10 seconds each; `--no-changes` skips
them. The worktrees are read
through a temporary index, so nothing in them changes.

```
fan/retry in api, 3 attempts against main
  api-fan-retry    claude  done     4 files  +120 -31  go test ./... passed 14m ago
  api-fan-retry-2  codex   working  2 files    +40 -3  no check yet
  api-fan-retry-3  claude  errored  0 files     +0 -0  last command exited 1 (make lint)
Keep one with 'tuios fan keep <session>'. Compare two with 'tuios fan diff A B'.
```

`fan verify` runs the command after `--` in every attempt, in a window named
`verify` in each session, with `sh -c` in the worktree and your `PATH`. One
word after `--` is a shell line, so `'make lint && make test'` keeps its `&&`.
Several words are one command and its arguments: each is quoted for the shell
as you gave it, so `-- go test -run 'TestA|TestB' ./...` passes the pattern
as one argument. The command is always yours: tuios never reads one from the repository. The
window holds no grants, so the check cannot drive tuios. A window whose check
passed closes; one whose check failed stays open with the output until you
press enter in it. The command waits for every check, prints one line each,
and exits 1 when any failed. `--timeout` fails a check that runs longer and
closes its window. `--no-wait` returns once the checks are started. A check
still running in an attempt is stopped first, and the window an earlier
failed check left open is closed.

`fan diff A B` shows what B's files hold that A's do not, committed or not,
untracked files included. git runs on this machine, as for `worktree diff`,
so both sessions must be here.

`fan keep` removes every sibling of the session you keep, the way `worktree rm`
does: a sibling with uncommitted changes is left in place unless `--stash` or
`--force` is passed, and the command exits 1 to say so. The daemon does the
removal (`keep-fan`), so the TUI and the CLI share it; against a daemon from
before that verb the CLI removes the siblings itself, with the same result.

`--host` runs the fan-out on another machine, in its checkout of the repository
you are in, found and cloned the way `worktree new --host` does it. The agent
must be installed there, and is looked up on that machine's `PATH`: yours is
not sent, and `--env` is refused. The sessions show in the rail under the
host. `fan keep HOST:SESSION` keeps one there, and `worktree pull
HOST:SESSION` brings its work here.

```bash
tuios fan 3 --host build --agent claude 'Add a retry.'
tuios worktree pull build:api-fan-add-retry-2
tuios fan keep build:api-fan-add-retry-2 --stash
```

### `tuios start-agent`

Start an agent in a new pane, here or on another machine, and return once it
is ready for a prompt.

**Usage:**
```bash
tuios start-agent <agent> [-s [<host>:]<session>] [--name <name>] [--cwd <dir> | --repo <dir> | --clone] [--workspace <n>] [--focus] [--prompt <prompt>] [--ready-timeout <ms>] [--protocol acp|codex] [--env NAME[=VALUE]]... [--grants <grant>[,<grant>...]] [--json] [-- <args>...]
```

`--grants` says what the agent may do through tuios: `read`, `write`, `fan`,
`respond`, `admin`, or `none` (see [`tuios pane-grants`](#tuios-pane-grants)).
Without it the pane holds the default of `[agents.permissions]`, or, started
from a pane without `admin`, that pane's own grants.

**Examples:**
```bash
tuios start-agent claude --name reviewer
tuios ask-agent -w reviewer 'review the diff on this branch'
tuios start-agent 'codex --model o5' --name tests --prompt 'Run the tests and fix what fails.'
```

`<agent>` is written as for `fan`. The command waits until the agent shows it
is at its prompt (idle or done), then types `--prompt` if one was given, and
prints the pane and the evidence. It exits 1 when the agent is not ready: it
stopped on a question of its own (the pane is kept for you to answer, and the
Inbox shows it), showed nothing before `--ready-timeout` (default 120000), or
exited. It also exits 1 when a `--prompt` was not taken. The pane is not
focused unless `--focus` is passed.

The command sends your `PATH` with any `--env`, as `fan` does, when the
session is on this machine. For a session on another machine
(`-s host:session`) it sends no `PATH`, and the agent is looked up on that
machine's `PATH`. `--env` there is refused with `forbidden`, because
variables do not cross machines.

The session `-s` names is created when it does not exist. The agent starts in
`--cwd`, or the main checkout of the repository `--repo` names, or else the
focused pane's directory. With `-s HOST:SESSION` and neither, it starts in that
machine's checkout of the repository you are in, found and cloned the way
`worktree new --host` does it (`--clone` clones it there when it has none).
Arguments after `--` are passed to the agent as an argv.

```bash
tuios start-agent -s build:api claude --prompt 'Profile the build.' -- --model opus
```

`--protocol` runs the agent headless over a structured protocol instead of in
its own TUI, and the pane shows the conversation as a transcript you type
prompts into:

```bash
tuios start-agent --protocol acp 'opencode acp' --name helper --prompt 'List the TODOs.'
tuios start-agent --protocol codex codex --name tests
```

`acp` is the Agent Client Protocol, version 1, for any agent command that
speaks it. `codex` is the Codex app-server, and `app-server` is added to the
`codex` command. The pane runs [`tuios agent-proto`](#tuios-agent-proto),
which reports the agent's state itself, so the command returns once that
report says idle. A permission the agent asks for is answered in the pane with
a number key, or from the Inbox when one line shows the whole request, with no
`[agents.approvals]` needed. A daemon older than `--protocol` refuses it by
name.

### `tuios agent-proto`

The pane program of `start-agent --protocol`: run an agent headless over ACP
or the Codex app-server, and show it as a transcript with a prompt line.

**Usage:**
```bash
tuios agent-proto --protocol acp|codex [--harness <id>] [--cwd <dir>] -- <agent> [args...]
```

It starts the agent with pipes, in a session of its own with no controlling
terminal, and offers it no file system and no terminal, so the agent acts
under its own sandbox and approval settings. Everything the agent sends is
cleaned of escape sequences before it is written. Type a prompt and press
Enter to send it; a paste arrives as one prompt. Ctrl+C cancels a turn, and
Ctrl+D on an empty line quits. A permission is shown with a number key per
answer; a key counts once the question has been up for half a second, and a
paste never answers one.

Inside a tuios pane it reports the pane's state under `--harness` (default:
the protocol): `idle`, `working`, `done` or `errored`, and `needs_input` with
kind `approval` for a permission, which it also holds for the Inbox with
`request-approval` when one line shows the whole request. Outside tuios it
reports nothing and works the same. When the agent exits, the pane shows the
end of its stderr and waits for Enter.

It also writes what the agent says about itself to the pane's agent metadata,
under the source `protocol`, each key only when its value changes: `model`
(the model the conversation opened with, from Codex's `thread/start` or ACP's
`session/new` models, and Codex's `model/rerouted`), `context` (tokens in
context of the model's window, from Codex's `thread/tokenUsage/updated` or
ACP's `usage_update`), `cost` (the session's cost from ACP's `usage_update`;
Codex sends none) and `plan` (steps done of the plan's steps, `3/7`). Every
field is optional: one the agent does not send is not written, and every key
is sent again at the start of each turn. Each prompt, tool call and finished
turn also goes to the daemon as `set-agent-state` activity, when the daemon's
`set-agent-state` lists that parameter; the activity changes no state (its
`if_state` is `none`). Both are sent off the pane's input loop. A Codex pane
now shows the turn's plan in the transcript, as an ACP pane does, and an ACP
pane whose agent names its model shows it in the first line, `connected to
opencode 1.2 (Claude Sonnet 4)`, as a Codex pane does.

```bash
tuios agent-proto --protocol acp -- opencode acp
```

### `tuios agent-statusline`

What Claude Code's status line runs once `tuios integration install
claude-code --statusline` is installed: read the status line payload on stdin
and write the model, context use and cost to the pane's agent metadata.

**Usage:**
```bash
tuios agent-statusline claude-code|opencode|kilo [--then CMD] [--turn-end] [--explain]
```

| Payload field | Metadata key |
| --- | --- |
| `model.display_name`, else `model.id` (Claude Code); `modelID` (opencode, Kilo) | `model` |
| `context_window.used_percentage` (Claude Code) | `context`, as `42%` |
| `cost.total_cost_usd` (Claude Code); `cost` (opencode, Kilo) | `cost`, as `$1.20` |

Every field is optional, and one that is missing, null or of another type is
not written. The keys go under the source `statusline`, with no TTL: they
clear when the agent leaves the pane. It prints nothing of its own. `--then`
runs your own status line command through `sh -c` with the same stdin and
prints its output unchanged, and the command exits with its status; that is
how a status line of your own is kept.

The pane is found from `-w`, then `TUIOS_PANE_ID`, then the process's terminal
and parent processes, as for `agent-hook`; a process in no pane asks the
daemon again at most once a minute. It calls only `set-agent-meta`, for that
pane, at most once every 15 seconds while the values change, and not at all
while they stay the same. A model change, context use crossing 80%, and a
change passed with `--turn-end` (the opencode plugin passes it when the
session goes idle) go at once, and unchanged values are sent again after 10
minutes so a daemon that restarted gets them back. The last values sent, and
any the interval held back, are kept in `statusline-<session>-<pane>.json`
(mode 0600) beside the daemon's socket. Held values go with the next run that
is due, or when the turn ends: Claude Code's `Stop` hook (`tuios agent-hook
claude-code`) sends them, and a run after the `Stop` goes at once. It reads at most 1 MiB of stdin, gives up on the daemon after 300ms
(`--timeout`), and exits 0 whatever goes wrong on the tuios side.

```bash
tuios agent-statusline claude-code --then '~/.claude/statusline.sh'
echo '{"model":{"display_name":"Opus"},"context_window":{"used_percentage":42}}' | tuios agent-statusline claude-code --explain
```

### `tuios kill-server`

Stop the TUIOS daemon process. This stops all sessions.

**Usage:**
```bash
tuios kill-server
```

**Contract:** the command is synchronous. It returns only once the daemon has
written every session's resurrection state and removed its socket, so a script
may start a new daemon as soon as it returns:

```bash
tuios kill-server && tuios start-server   # safe: no race
```

The daemon unlinks its socket last, after the final saves, and that unlink is
what this command waits for. A refused connection is not the same signal: the
daemon closes its listener at the start of shutdown, while state is still
unsaved, so polling the socket for connectivity can report "stopped" before
anything has been persisted.

If the daemon has not finished within 10 seconds the command fails, naming the
pid and the socket, rather than returning success while the old process is still
running. Re-running it re-checks. Force killing with `kill -9` skips the final
save and loses any state written since the last periodic save.

When no daemon is running, the command reports that and removes a stale socket
if one is present. It exits 0 in that case.

### `tuios daemon`

Run the daemon in the foreground (for debugging).

**Usage:**
```bash
tuios daemon [flags]
```

**Flags:**
- `--log-level <level>`: Debug log level: `off`, `errors`, `basic`, `messages`, `verbose`, `trace`
- `--no-restore`: Do not restore saved sessions on start. Run `tuios resurrect` to restore one on demand

**Debug log levels:**
- `off`: No debug output (default)
- `errors`: Only error messages
- `basic`: Connection events and errors
- `messages`: All protocol messages except PTY I/O
- `verbose`: All messages including PTY I/O
- `trace`: Full payload hex dumps

**Note:** This is primarily for debugging. The daemon starts automatically in the background when you run `tuios new` or `tuios attach`. Use this command to run the daemon in the foreground with debug logging.

### Workflow Example

```bash
# Start a new session for work
tuios new work

# ... do some work, then detach with Ctrl+B d ...

# Later, list your sessions
tuios ls

# Reattach to continue working
tuios attach work

# When done, kill the session
tuios kill-session work
```

---

## Remote Control Commands

TUIOS provides commands to control a running session from external scripts and tools. These commands communicate with the TUIOS daemon to send keystrokes, execute commands, and query state. This enables powerful scripting, automation, and integration with external tools.

> **Note:** When sending TUIOS commands via `send-keys`, `Ctrl+B` refers to the default leader key. This is configurable via the `leader_key` option in your config file.

### `tuios send-keys`

Send keys to the program in a window: arrows, page keys, Enter, `ctrl+c`.

With `-w` the keys go to that window's terminal, whether or not a client is
attached and whichever window has the focus. Without `-w` they go to the
attached client as if the person pressed them, which drives the window manager
or the focused window; with no client attached they go to the focused window.

**Usage:**
```bash
tuios send-keys <keys> [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <target>`: Target window: full id, the index `list-windows` prints, a unique id prefix, or the exact name (default: the attached client, else the focused window)
- `-N, --repeat <n>`: Send the whole sequence n times, 1 to 1000 (default 1)
- `-l, --literal`: Write the argument to the window's terminal unchanged, with no key names
- `-r, --raw`: Treat each character as a separate key (no splitting on space/comma)
- `--json`: Output the result as JSON

**Output:**
Where the keys went:

```
sent 5 keys to window docs (3a42ab8f)
```

or `sent 2 keys to the attached client (...)` without `-w`. `--json` gives
`sent_to` (`window` or `client`), `keys`, and for a window `window_id` and
`window`.

**Keys:** the argument is split on spaces and commas. Names are
case-insensitive.

| Key | Name | Other spellings that work |
| --- | --- | --- |
| Arrows | `Up` `Down` `Left` `Right` | `up`, `UP`, `arrow-up`, `ArrowUp`, `up-arrow`, `KEY_UP`, `<Up>` |
| Page keys | `PageUp` `PageDown` | `PgUp`, `PgDn`, `Page_Down`, `NPage`, `PPage`, `KEY_NPAGE` |
| Line ends | `Home` `End` | `KEY_HOME`, `<End>` |
| Enter | `Enter` | `Return`, `CR`, `KEY_ENTER` |
| Escape | `Escape` | `Esc` |
| Editing | `Tab` `BTab` `Space` `Backspace` `Delete` `Insert` | `shift+Tab`, `BSpace`, `BS`, `Del`, `DC`, `Ins`, `IC` |
| Function keys | `F1` to `F12` | `f5`, `KEY_F5` |
| A character | `q` `j` `G` `/` | any single character |
| With modifiers | `ctrl+c` `alt+b` `shift+Up` `ctrl+Right` | `C-c`, `M-b`, `S-Up`, `^C`, `Ctrl+C` |
| A raw sequence | `\e[A` | `\x1b[A`, `\033[A`, `^[[A` |
| The leader key | `PREFIX` | `$PREFIX`; only without `-w`, with a client attached |

Arrows, `Home` and `End` are sent in the form the program asked for
(application cursor keys). A word that looks like a key but is not one (`Dwon`,
`KEY_FOO`, `F13`) is refused with the key names and the closest one, and
nothing is sent. A plain lower-case word such as `ls` is typed as its letters.

**send-keys is not for text.** `send-keys 'echo hello'` types `echohello`. Use
[`tuios send-text`](#tuios-send-text).

**Examples:**
```bash
# Scroll the pager in the window named docs
tuios send-keys -w docs Down
tuios send-keys -w docs Down --repeat 10
tuios send-keys -w docs 'PageDown PageDown'

# Interrupt what runs in the window named build
tuios send-keys -w build ctrl+c

# A window by the id new-window printed
id=$(tuios new-window logs --print-id)
tuios send-keys -w "$id" End

# Keys for the window manager: the leader key and then q, no -w
tuios send-keys "ctrl+b q"
tuios send-keys "\$PREFIX q"

# Enter terminal mode in the attached client (press 'i')
tuios send-keys i
```

### `tuios send-text`

Write text verbatim to a pane's PTY, with no key parsing at all.

Nothing in the argument is interpreted, so spaces, quotes and punctuation
arrive as typed. A trailing newline is the Enter that runs the line, which
makes this one call where `send-keys` needs two.

**Usage:**
```bash
tuios send-text <text> [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <id-or-name>`: Target window (default: focused)

**Examples:**
```bash
# Run a command in the focused pane (the trailing newline submits it)
tuios send-text 'go build ./...
'

# Type without submitting
tuios send-text -w build 'partial input'

# Text with spaces, quotes and commas needs no flags
tuios send-text -w build 'git commit -m "fix: cache, retries"'
```

### `tuios new-window`

Open a new window in a session and print its id.

The window is created by the daemon whether or not a client is attached, so
this works on a detached session. Give it a name to address it later without
holding on to the id.

**Usage:**
```bash
tuios new-window [name] [command...] [flags]
```

Words after the name are the argv the window runs instead of a shell, with no
shell in between: `tuios new-window htop /usr/bin/htop`. Put `--` before a
command that has flags of its own, or tuios reads them as its own flags:
`tuios new-window log -- git log --oneline -20`.

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `--workspace <n>`: Workspace to open the window on (default: the current one)
- `--cwd <dir>`: Directory to start the shell in (default: the daemon's)
- `--no-focus`: Leave the focus where it is
- `--host <name>`: Run the window's process on this machine from the `[hosts]` table (default: this machine)
- `--grants <grant>[,<grant>...]`: What the window's process may do through tuios: `read`, `write`, `fan`, `respond`, `admin`, or `none` (default: `[agents.permissions]`). See [`tuios pane-grants`](#tuios-pane-grants)
- `--json`: Output result as JSON
- `--print-id`: Print only the new window's full id

**Output:**
The 8-character window id and the window's name, separated by two spaces:

```
a1b2c3d4  build
```

That id prefix, and the name, are what `-w` accepts everywhere else. With
`--print-id` the output is the full id alone, for `id=$(tuios new-window build --print-id)`.

**Examples:**
```bash
# Open an unnamed window
tuios new-window

# Open a named window and run something in it
tuios new-window build
tuios send-text -w build 'go build ./...
'

# Capture the new window's id for scripting
id=$(tuios new-window build --print-id)
tuios new-window --json | jq -r .window_id

# JSON output carries the full id and the name
tuios new-window --json build
# Output: {"focused":true,"host":"","message":"command executed","name":"build","pty_id":"8fe359af-0f9e-4efe-9595-7b1d3467dc50","success":true,"unplaced":true,"window_id":"a6e55709-071e-4345-9363-ff4ef0a63c02","workspace":1}

# Target a specific session
tuios new-window -s mysession dev
```

### `tuios popup`

Run a command in a floating pane centred over the layout, and print its id.

The pane closes when the command exits. Nothing re-parses the arguments after
`--`, so nothing needs quoting. This is how a picker becomes an overlay: run
fzf, gum or any other full-screen program in it.

It needs an attached client, because a popup is a thing on a screen. The pane is
not tiled, it is not in the window cycle, and it cannot be minimized.

The popup writes to its own screen, not to this command's output. With
`--wait` this command stays open until the popup's command exits and exits
with its status (130 when the popup was closed by hand). With
`--capture-stdout`, which implies `--wait`, the command's standard output
comes here instead of into the popup: a picker such as fzf or gum draws on the
terminal and prints only the choice, so the choice is what this command
prints. Capture is not supported on Windows. Without either, redirect inside
the popup or send the selection to another pane.

**Usage:**
```bash
tuios popup [flags] -- <command> [args...]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `--width <size>`: Width in cells (`60`) or percent (`60%`). Default `80%`
- `--height <size>`: Height in cells (`20`) or percent (`50%`). Default `60%`

Neither size flag has a short form: `-w` selects a window everywhere else, and
`-h` is help.
- `--name <name>`: Name for the popup
- `--cwd <dir>`: Directory to run the command in
- `--workspace <n>`: Workspace to open the popup on
- `--wait`: Stay open until the command exits, and exit with its status
- `--capture-stdout`: Print the command's standard output here instead of in the popup (implies `--wait`)
- `--timeout <ms>`: With `--wait`, milliseconds to wait before failing with `timeout`; the popup stays open (default: as long as it is open)
- `--json`: Output result as JSON

A size larger than the pane region is cut down to the region.

**Lifetime:**

A popup lives as long as its command. Detaching leaves it running, and it is
still there on the next attach. A daemon restart does not bring it back: the
restore respawns a shell rather than the command, which is not the popup. Press
esc in window mode to close one by hand, or close it like any other pane.

**Examples:**
```bash
# Pick a file in a centred popup and use the answer
file=$(tuios popup --capture-stdout -- fzf)

# Wait for a confirmation and branch on it
tuios popup --wait -- gum confirm "Deploy?" && ./deploy.sh

# Keep the answer in a file instead
tuios popup -- sh -c 'ls | fzf > /tmp/pick'

# Send the selection straight to the pane you came from
tuios popup -- sh -c 'tuios send-text -w main "$(ls | fzf)"'

# A small popup, in cells
tuios popup --width 60 --height 20 -- gum choose one two three

# Watch something, then press q to close it
tuios popup --width 90% --height 80% -- htop

# Capture the popup's id for scripting
tuios popup --json -- fzf | jq -r .window_id
```

### `tuios ask-human`

Ask the person a question with a fixed set of answers, wait for them to pick
one, and print it.

**Usage:**
```bash
tuios ask-human [flags] <question> -o <answer> [-o <answer>...]
tuios ask-human --request-id <id>
```

The question goes in the Inbox as a row under Questions. A client that shows the
asking pane opens the Inbox on it at once, unless the person is typing into
the pane or has an overlay open, and the digits 1 to 9 pick an answer once it
has been on screen for a moment; from any other pane it waits there with the usual alert, and with
nobody attached it waits for the next attach. Only the person at an attached
client can answer; an agent cannot.

When `--timeout` runs out first, the command exits `2` and the question stays.
The answer is then mailed to the asking pane from `human`, marked
`verified_human`, so `tuios wait-for agent-message` picks it up; or come back
with `--request-id`. From inside a pane the question is asked as that pane,
and naming another is refused. An answer to a call that was killed while it
waited is mailed the same way. A caller whose tool stops commands after a
while (two minutes for many agent harnesses) keeps `--timeout` below that, or
asks with `--no-wait` and waits with `tuios wait-for agent-message`.

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <id-or-name>`: The pane asking, where a late answer is mailed (default: your own pane; none from outside every pane)
- `-o, --option <answer>`: An answer the person can pick; give 1 to 9, each one line of at most 60 bytes
- `--timeout <ms>`: Milliseconds to wait for the answer (default: 120000, at most one hour)
- `--no-wait`: Ask and return at once, exit `2`; the answer arrives as mail
- `--request-id <id>`: Come back for a question already asked
- `--json`: Output the result as JSON: `request_id`, `status`, `answer`, `answer_index`, `answered_by`, `verified_human`

The question is one line of printable text, at most 160 bytes.

**Exit status:** `0` with the answer alone on stdout; `2` when there is no
answer yet; `1` when the question ended without one (dismissed, superseded by
a newer question from the same pane, or its pane closed).

**Examples:**
```bash
# Ask, and branch on the answer
if [ "$(tuios ask-human 'Deploy to staging?' -o yes -o no)" = yes ]; then ./deploy.sh; fi

# Ask and move on; the answer arrives as mail
tuios ask-human 'Which region?' -o us -o eu --no-wait

# Come back for an answer
tuios ask-human --request-id 9f86d081884c7d65
```

### `tuios run-command`

Execute a TUIOS command (same commands available via tape scripts).

**Usage:**
```bash
tuios run-command <command> [args...] [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `--json`: Output result as JSON (useful for scripting)
- `--list`: List all available commands

`run-command` sends a client protocol message, so from inside a pane it needs
the `admin` grant (see [`tuios pane-grants`](#tuios-pane-grants)). Under the
default `mode = "open"` every pane holds `admin`. Prefer a verb where one
exists: `tuios get-window` and `tuios list-windows` read windows with `read`.

**Available Commands:**

`tuios run-command --list` prints this list from the binary.

| Command | Arguments | Description |
|---------|-----------|-------------|
| `NewWindow` | `[name]` | Create a new terminal window |
| `CloseWindow` | `[name]` | Close the focused window, or every window with that name |
| `NextWindow` | | Focus the next window |
| `PrevWindow` | | Focus the previous window |
| `FocusWindow` | `<name>` | Focus a window by name |
| `RenameWindow` | `<name>` or `<old> <new>` | Rename the focused or a named window |
| `MinimizeWindow` | `[name]` | Minimize the focused or a named window |
| `RestoreWindow` | `[name]` | Restore the focused or a named window |
| `TerminalMode` | | Switch to terminal mode |
| `WindowManagementMode` | | Switch to window management mode |
| `ToggleTiling`, `EnableTiling`, `DisableTiling` | | Turn tiling mode on or off |
| `SnapLeft`, `SnapRight`, `SnapFullscreen` | | Snap the focused window |
| `Split` | `horizontal` or `vertical` | Split the focused window |
| `RotateSplit` | | Rotate the split direction |
| `EqualizeSplits` | | Equalize all split ratios |
| `Screenshot` | | Save the focused window as an image |
| `SwitchWorkspace` | `<1-9>` | Switch to workspace |
| `MoveToWorkspace` | `<1-9>` | Move focused window to workspace |
| `EnableAnimations`, `DisableAnimations`, `ToggleAnimations` | | Turn UI animations on or off |
| `SetDockbarPosition` | `<position>` | Set dockbar position (top/bottom/hidden) |
| `SetBorderStyle` | `<style>` | Change window border style |
| `SetTheme` | `<theme>` | Change the color theme |
| `ShowNotification` | `<message> [type]` | Show a notification |
| `ListWindows`, `GetWindow [id-or-name]`, `GetSessionInfo` | | The older spellings of `list-windows`, `get-window` and `session-info`, which are the ones to use |

**Examples:**
```bash
# List all available commands
tuios run-command --list

# Create a new window
tuios run-command NewWindow "my-terminal"

# Create window and get JSON output with window ID
tuios run-command --json NewWindow "my-terminal"
# Output: {"message":"command executed","name":"my-terminal","success":true,"window_id":"75684348-98ba-4e60-a9c4-0b1e1920b25e"}

# Switch workspace
tuios run-command SwitchWorkspace 2

# Toggle tiling
tuios run-command ToggleTiling

# Close focused window
tuios run-command CloseWindow

# Target a specific session
tuios run-command -s mysession NewWindow "dev"
```

### `tuios set-config`

Change TUIOS configuration at runtime.

**Usage:**
```bash
tuios set-config <path> <value> [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)

**Available Paths:**
| Path | Values | Description |
|------|--------|-------------|
| `dockbar_position` | `bottom`, `top`, `hidden` | Dockbar position (default `top`) |
| `border_style` | `rounded`, `normal`, `thick`, `double`, `block`, `outer-half-block`, `inner-half-block`, `ascii`, `hidden`, `glyphs` | Border style |
| `animations_enabled` | `true`, `false` | Enable/disable animations |
| `hide_window_buttons` | `true`, `false` | Hide window buttons |
| `window_button_style` | `pill`, `dots` | How the window controls are drawn |
| `window_button_position` | `right`, `left` | Which end of the title bar they sit on |
| `background` | `off`, `theme`, `#RRGGBB` | Background painted on every surface not set on its own: panes, desktop, window chrome, dock and rail (default `off`) |
| `pane_background` | `off`, `theme`, `#RRGGBB`, or empty | Background behind pane content where the program left the default; empty follows `background` |
| `desktop_background` | `off`, `theme`, `#RRGGBB`, or empty | Background behind and between panes; empty follows `background` |
| `window_chrome_background` | `off`, `theme`, `#RRGGBB`, or empty | Background under pane borders and title bars, which keep their own ink; empty follows `background` |
| `dock_background` | `off`, `theme`, `#RRGGBB`, or empty | Background under the dock; empty follows `background` |
| `appearance.sidebar.background` | `off`, `theme`, `#RRGGBB`, or empty | Background under the rail; empty follows `background` |

**Examples:**
```bash
# Change dockbar position
tuios set-config dockbar_position bottom

# Change border style
tuios set-config border_style rounded

# Turn animations off
tuios set-config animations_enabled false

# Hide window buttons
tuios set-config hide_window_buttons true
tuios set-config window_button_style dots
tuios set-config window_button_position left

# Paint the theme's background behind pane content, or a colour of your own
tuios set-config pane_background theme
tuios set-config pane_background '#1e1e2e'

# Paint every surface at once, then give the dock its own colour and leave
# the rail on the terminal's background. A surface's own value wins, and
# empty puts it back to following background.
tuios set-config background theme
tuios set-config dock_background '#11111b'
tuios set-config appearance.sidebar.background off
tuios set-config appearance.sidebar.background ''

# Target a specific session
tuios set-config -s mysession dockbar_position hidden
```

### `tuios wait-for`

Block until the daemon reports that a condition matched.

The daemon watches its own events, so a script stops polling a pane and
sleeping between captures. The command exits `0` when the condition matches,
and non-zero with the `timeout` error when it does not match before
`--timeout`.

**Usage:**
```bash
tuios wait-for <condition> [flags]
```

**Conditions:**
| Condition | Matches when |
|-----------|--------------|
| `session-exists` | The named session is present |
| `window-output` | The window's content matches `--pattern` |
| `window-exit` | The window's shell exited |
| `window-idle` | The window printed nothing for `--idle` milliseconds |
| `agent-state` | An agent reached one of the `--until` states; without `--window`, any agent pane in the session matches; with `--any-session`, any agent pane in any session; with `--select`, any agent pane the selector matches, or with `--every` all of them |
| `agent-message` | A message arrived. With `--window`, the first unread message in that inbox; without it, any message left in the session after the wait began |
| `command-finished` | A shell that marks its commands with OSC 133 finished one. With `--window`, that pane's next command, or with `--command-seq N` the first once the pane has finished more than N (already true if it happened before the wait). Without a window, any pane in the session. Prints the exit code |

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <id-or-name>`: Target window (default: focused; `agent-state`: any window)
- `--pattern <regexp>`: Go regular expression (RE2), required by `window-output`
- `--until <states>`: Agent state(s) to wait for, comma-separated, required by `agent-state`
- `--thread <id>`: Only match a message in this thread, for `agent-message`. Pass any message id in it
- `--idle <ms>`: Milliseconds of silence that count as idle (default: 500)
- `--timeout <ms>`: Milliseconds to wait before giving up (default: 30000)
- `--any-session`: For `agent-state`: watch every session on the daemon, including ones created during the wait. Takes no `--session` or `--window`, and the result names the session that matched
- `--command-seq <N>`: For `command-finished` with `--window`: match once the pane has finished more than N commands. Read N from `list-windows --json` before starting the command
- `--select <selector>`: For `agent-state`: watch the agent panes a [selector](AGENT_STATE.md#selectors) matches, in every session, including panes that open during the wait. Takes no `--session`, `--window` or `--any-session`
- `--every`: With `--select`: wait until at least one pane matches and every matched pane is in one of the `--until` states
- `--json`: Output result as JSON

The `window-output` pattern is matched against the window's scrollback, so
output that has already scrolled off the visible screen still matches, and
output printed before the wait started matches immediately.

That last part has a sharp edge. A pane echoes the command it was sent, so a
marker written literally in the command matches its own echo and the wait
returns before the work has run. A marker left in the scrollback by an earlier
run matches the same way. Assemble the marker so the literal appears only in the
output (`printf "BUILD_%s\n" OK`), or use `window-exit` in a window opened for
the one command.

**Examples:**
```bash
# Wait for a build to print its marker
tuios wait-for window-output -w build --pattern 'BUILD OK'

# Wait for a pane to go quiet for two seconds
tuios wait-for window-idle -w build --idle 2000

# Wait for a command's shell to exit, allowing ten minutes
tuios wait-for window-exit -w build --timeout 600000

# Wait for a session to appear
tuios wait-for session-exists -s work

# Wait until any agent in the session is waiting on a human
tuios wait-for agent-state -s work --until needs_input

# Wait until an agent in any session is waiting on a human
tuios wait-for agent-state --any-session --until needs_input

# Wait until every agent of a fan-out has finished its turn
tuios wait-for agent-state --select 'group:fan/add-retry' --until idle,done --every --timeout 3600000

# Wait for mail in your own inbox
tuios wait-for agent-message -s work -w "$TUIOS_PANE_ID" --timeout 600000

# Branch on the result
if tuios wait-for window-output -w build --pattern 'BUILD OK' --timeout 60000; then
    echo "build finished"
else
    echo "build timed out"
fi

# Wait for the command after the 4th in the build pane, with its exit code
tuios wait-for command-finished -w build --command-seq 4 --timeout 600000
```

### `tuios run`

Type one command line at a pane's shell prompt, wait for it to finish, print
what it printed, and exit with its exit status.

It reads where the command starts and ends from the shell's own OSC 133 marks,
so it needs a shell with prompt integration: fish and zsh with it on, bash with
a setup, or any shell a terminal injects its integration script into. It never
types into a running program.

**Usage:**
```bash
tuios run [flags] -- <command line>
```

The words after `--` are joined with spaces into one line that the shell
parses, so quote for the shell as you would when typing it.

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <id-or-name>`: Target window (default: focused)
- `--timeout <ms>`: Milliseconds to wait for the command (default: 30000). A timeout does not stop the command
- `--lines <N>`: Keep only the last N lines of the output (0 keeps all)
- `--json`: Output the whole result as JSON: `exit_code`, `output`, `cmdline`, `duration_ms`, `command_seq`, `truncated`

**Exit status:** the command's own. When the shell sent no status, run says so
on stderr and exits `0`. A refusal exits `1`: `not_at_prompt` when a command is
already running in the pane or another `run` there has not ended,
`no_shell_integration` when its shell sends no marks. Nothing is typed in
either case. A shell that marks its prompts and not its commands (bash before
4.4, which ignores the recipe's PS0) is found out by the first `run` in the
pane: the line is typed, and when the shell draws a new prompt with no command
mark, `run` fails at once with `no_shell_integration`. Every later `run` there
is refused before typing.

**Examples:**
```bash
# Run the tests in the build pane and branch on the status
tuios run -w build --timeout 600000 -- go test ./... && echo passed

# Only the last 40 lines of a long build
tuios run -w build --lines 40 -- make

# The whole result
tuios run -w build --json -- make lint
```

### `tuios subscribe`

Print the daemon's event stream, one JSON object per line.

The first line is the subscribe ack. It carries `seq`, the last event number
assigned when the stream went live, and `boot_id`, a random id for this daemon
start. Every event after it carries its own `seq` and `boot_id`. The event
types and their fields are listed in [protocol.md](protocol.md#event-stream).

Without `--session` the stream covers every session on the daemon. Most
commands read an omitted session as the most recently active one; this one
does not.

**Usage:**
```bash
tuios subscribe [flags]
```

**Flags:**
- `-s, --session <name>`: Only events from this session (default: every session)
- `-w, --window <id>`: Only events about this window id
- `--types <list>`: Only these event types, comma-separated (default: all)
- `--after-seq <n>`: Replay the retained events after this seq before streaming live
- `--boot-id <id>`: The `boot_id` the `--after-seq` came with, so a daemon restart shows as a gap. Needs `--after-seq`
- `--count <n>`: Exit after printing this many events, gap lines included (default: run until the stream ends)
- `--hosts`: Also print the `agent-state`, `session-created` and `session-closed` events of every linked host, each with `host` set. Without it, the only events about other machines are `attention` for their Inbox items and `host-changed`, and only when no `--session` or `--window` is given

To resume after a disconnect, pass the `seq` of the last event you printed and
its `boot_id`. The daemon keeps the last 4096 events, apart from `output`
events, and replays the ones after that seq before carrying on live. When it
cannot replay everything you missed, a `gap` line comes first with a `reason`:
`evicted` (the oldest events you missed are gone), `boot_changed` (the daemon
restarted), `not_retained` (your filter includes `output`, which is never
replayed) or, on a live stream, `overflow` (you read too slowly). After a gap,
read current state again with `tuios list-agents` or `tuios list-windows`.

**Examples:**
```bash
# Every agent state change on the daemon
tuios subscribe --types agent-state

# One session's window lifecycle
tuios subscribe -s work --types window-created,window-closed

# Resume where a previous run stopped
tuios subscribe --types agent-state --after-seq 118 --boot-id 9f2c41d07a3e8b65

# Wait for the next bell anywhere, then exit
tuios subscribe --types bell --count 1

# Follow the Inbox: every item that opens, changes or closes
tuios subscribe --types attention

# Every agent on every machine, and every host coming or going
tuios subscribe --hosts --types agent-state,host-changed
```

### `tuios list-attention`

List the Inbox: every approval and question an agent is blocked on, mail to
you, errored agents, conversations a daemon restart left to resume, and
finished turns nobody has looked at, in every session on the daemon and on
every linked host it streams. Rows are grouped Approvals, Questions (an
agent's own and those put with `ask-human`), Mail, Errored, Resume, Done,
oldest first, with how long each has waited, its
id, its session and pane, and what it said. The TUI's Inbox (prefix `i`) is
the same list. A row of another machine names it,
`build:api/claude`, and a row of a machine whose link is down ends in
`[unreachable, seen 5m ago]`: it is what that machine said last.

An item closes by itself when what opened it stops being true. Dismissing one
is for the person at an attached client and is done from the Inbox; there is
no command for it, because a command run from a pane is exactly what must not
be able to clear the person's queue. Snoozing, marking unread and undo are the
person's acts too and have no command either. `--snoozed` lists the items the
person snoozed, after the rest under a Snoozed heading, each ending in
`[snoozed until 15:30]` or `[snoozed until it changes]`. Answering a held approval (`1`, `2`, `3`
in the Inbox) has no command for the same reason. The row of an approval a
hook is holding ends with `(held: answer in the Inbox)`, since its pane shows
no prompt while it is held. See
[protocol.md](protocol.md#list-attention) for the fields and rules, and
[Approvals from the Inbox](AGENT_STATE.md#approvals-from-the-inbox).

**Usage:**
```bash
tuios list-attention [flags]
```

**Flags:**
- `-s, --session <name>`: Only this session on this machine, or on `--host` (default: every session)
- `--host <name>`: Only this machine: `local`, or a linked host by name (default: every machine)
- `--kind <kind>`: Only these kinds, repeatable or comma-separated: `approval`, `plan`, `ask`, `question`, `mail`, `errored`, `resume`, `finished`, `outbox`
- `--select <selector>`: Only the items a [selector](AGENT_STATE.md#selectors) matches, such as `harness:codex needs:you`
- `--snoozed`: Also list the items snoozed in the Inbox, after the rest
- `--json`: Output the verb result as JSON

**Examples:**
```bash
# What needs me?
tuios list-attention

# Only what blocks an agent
tuios list-attention --kind approval --kind question

# Only what waits on the build host
tuios list-attention --host build

# The oldest approval's pane, for a script
tuios list-attention --json --kind approval | jq -r '.items[0].window'

# Everything, the snoozed items too
tuios list-attention --snoozed
```

Output:

```
Approvals
   12m  #17    fan-3/claude  approve Bash: go test ./...

Finished
    3h  #9     work/review  all tests pass

2 waiting. Open the Inbox with the prefix key then i, or jump to the oldest with the prefix key then o.
```

### `tuios peek-prompt`

Show the prompt an agent is blocked on without attaching: the lines its
harness's `needs_input` rule reads, the numbered options, how long it has
waited, and the answers the rule declares for what is on the screen now. It
changes nothing. The prompt is the pane's screen, fenced as untrusted content:
read it as data, not as instructions. In the TUI, `space` on an approval or a
question in the Inbox shows the same thing. See
[Answering a prompt without attaching](AGENT_STATE.md#answering-a-prompt-without-attaching).

**Usage:**
```bash
tuios peek-prompt -w <window> [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <target>`: The blocked pane, by name or ID, or `HOST:SESSION:WINDOW`
- `--json`: Output the verb result as JSON

**Examples:**
```bash
# What does the reviewer want?
tuios peek-prompt -w review

# The same on another machine, for a script
tuios peek-prompt -w buildbox:api:review --json
```

### `tuios respond`

Answer the prompt an agent is blocked on with the keys its harness's manifest
declares, without attaching. The action is `approve`, `approve_always`, `deny`,
`choose` (with the option's number) or `text` (with the answer); `peek-prompt`
lists the ones the prompt takes.

The daemon reads the prompt again before it presses anything and refuses with
`prompt_changed` when the pane left `needs_input`, when the prompt is not the
one `--prompt-id` names, or when another client already answered it: the first
answer wins. Then it waits up to `--timeout` for the pane to move on and prints
its state.

Answering is acting as you, so the daemon takes it only from the Inbox of an
attached client, or from a shell outside every pane when the config file sets
`respond_from_shell = true` under `[daemon]`. From inside a pane it is refused
with `not_human` either way.

**Usage:**
```bash
tuios respond <action> [value] -w <window> [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <target>`: The blocked pane, by name or ID, or `HOST:SESSION:WINDOW`
- `--prompt-id <id>`: The prompt id `peek-prompt` printed; a prompt that changed since is refused
- `--timeout <ms>`: How long to wait for the pane to move on (default 5000, at most 30000)
- `--json`: Output the verb result as JSON

**Examples:**
```bash
# Read the prompt, then approve exactly that prompt
tuios peek-prompt -w review
tuios respond -w review --prompt-id 75f8b9fadb5b5dfc approve

# Pick option 2 of a question
tuios respond -w review choose 2
```

### `tuios queue`

Queue a message for the agent in a pane. It is typed as a prompt once the
agent has been at rest (`idle` or `done`, or `unknown` for a harness that can
never show idle) for a second, one message per rest, and never over a prompt
the agent is waiting on. For a harness that cannot show working, a rest after a
typed message is also new output followed by 5 seconds of silence. If the agent is at rest now,
it is typed right away. The daemon then waits for the agent to show it took
the message, the way `fan` waits for its first prompt. A message the agent
shows no sign of taking is marked `stalled`, is never typed again, and opens a
question in the Inbox; it holds the messages behind it until the agent next
works or you drop it, and the next message is then typed at the next rest.

A pane holds at most `[agents.queue] max` messages (8 by default) of at most
16 KiB each. The queue lives in the daemon's memory, so a daemon restart drops
it, and so do the pane closing and the agent leaving the pane. From inside a
pane the message is checked like `send-text` when it is queued, and against
the pane's grants again when it is typed. See
[Queued messages](AGENT_STATE.md#queued-messages).

**Usage:**
```bash
tuios queue [flags] TEXT...
tuios queue ls [-w <window>] [flags]
tuios queue rm ID | --all -w <window> [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <target>`: The agent's pane, by name or ID, or `HOST:SESSION:WINDOW` (default: the focused pane; for `ls`, every pane of the session)
- `--all` (`rm`): Drop every message queued for the pane that you may drop
- `--json`: Output the verb result as JSON

The words of `TEXT` are joined with spaces. To queue a message that reads
`ls` or `rm`, put `--` before it. `ls` prints one line per message: its id,
state (`waiting`, `delivering` or `stalled`), who queued it (`human` from the
Inbox, `shell` from outside every pane, a pane's id, or `link:HOST`), its age,
the pane, and the message's first line. `rm` from inside a pane drops only
what that pane queued, and from a shell every message but the ones you queued
from the Inbox, which only the attached client can drop.

**Examples:**
```bash
# Reply to the agent in the build pane once it finishes its turn
tuios queue -w build 'make the backoff jitter configurable'

# What waits, and drop one message
tuios queue ls
tuios queue rm q3

# Everything queued for one pane
tuios queue rm --all -w build
```

### `tuios review`

Show what the agent in a pane changed, leave notes on its lines, and send the
notes to the agent. The diff is the pane's worktree against the base it was
made from, or for a plain repository against the merge base with its upstream
branch, else only what is not committed. Committed and uncommitted work show
together, untracked files included and ignored files left out. It is read by
the daemon through a temporary git index, so the repository, its index and its
files are not changed. See
[Reviewing an agent's changes](AGENT_STATE.md#reviewing-an-agents-changes).

**Usage:**
```bash
tuios review [SESSION] [-w <window>] [--base <ref> | --against <session> | --uncommitted] [--stat] [--path <path>]... [--context <n>] [--json]
tuios review note [-w <window>] FILE:LINE TEXT... [--side old]
tuios review note [-w <window>] --hunk '<header>' FILE TEXT...
tuios review note --edit ID TEXT... | --remove ID
tuios review notes [-w <window>] [--clear] [--json]
tuios review send [-w <window>] [--id ID]... [--now] [--json]
```

**Flags:**
- `-s, --session <name>`: Target session, also accepted as the argument (default: this pane's, else the most recently active)
- `-w, --window <target>`: The pane, by name or ID (default: the focused pane)
- `--base <ref>`: Diff against this branch, tag or commit, through its merge base with `HEAD`
- `--against <session>`: Diff against another attempt of the same fan
- `--uncommitted`: Only what is not committed yet
- `--stat`: The list of changed files with their counts, without the diff
- `--path <path>`: Only this path, relative to the repository root. Repeatable
- `--context <n>`: Lines of context around each change, 0 to 20 (default 3)
- `--side old` (`note`): `LINE` is a removed line, numbered as in the base
- `--hunk <header>` (`note`): The note is on the whole hunk with this header
- `--edit <id>`, `--remove <id>` (`note`): Change or drop a note
- `--clear` (`notes`): Remove every note you may remove
- `--id <id>` (`send`): Send only this note, sent before or not. Repeatable
- `--now` (`send`): Send only if the agent is at rest with nothing queued; never queue
- `--json`: Output the verb result as JSON

`tuios review` prints a heading, one line per changed file (its status `A`,
`M`, `D`, `R`, or `U` for untracked, and its counts), then each file's hunks
with both line numbers and the notes under the lines they are on. A diff stops
at 400 files, 2 MiB of text or 5000 lines in one file; files past that show
their counts only.

A note keeps its line's text and follows it when the file changes; one whose
line is gone is marked outdated. `review notes` lists each note's id, place,
author (`human`, `shell`, a pane, or `link:HOST`), state and text. From inside
a pane only the notes that pane wrote can be edited or removed; from a shell,
any but the ones left from the attached client.

`review send` sends the unsent notes as one message through the delivery
queue ([`tuios queue`](#tuios-queue)): typed when the agent is at rest, never
over a prompt. The message says who sent it: "a script" from a shell, the pane
from inside one, and "the person" only from the attached client. A note
written by someone else says who wrote it. A note whose author may not type
into the pane now, or whose pane is gone, is withheld and listed after the
summary. From inside a pane, notes can be added only on a pane you could type
into.

The review commands work on this machine's sessions. A `HOST:SESSION` or
`HOST:SESSION:WINDOW` target is refused: attach to that machine and review
there, or bring the work here with
[`tuios worktree pull`](#tuios-worktree) and review that.

**Examples:**
```bash
# What the agent in the focused pane changed
tuios review

# A fan attempt against its base, or against another attempt
tuios review api-fan-retry-2
tuios review api-fan-retry-2 --against api-fan-retry

# Leave two notes and send them
tuios review note -s api-fan-retry-2 api/retry.go:42 'log the attempt number here too'
tuios review note -s api-fan-retry-2 --hunk '@@ -88,4 +100,6 @@' api/retry.go 'wrap with context'
tuios review send -s api-fan-retry-2
```

### `tuios set-agent-state`

Report a pane's agent state so the session can show which panes need
attention.

**Usage:**
```bash
tuios set-agent-state <state> [flags]
```

**States:** `none`, `working`, `needs_input`, `idle`, `done`, `errored`, `unknown`

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <id-or-name>`: Target window (default: focused)
- `-m, --message <text>`: Short note reported with the state
- `--source <source>`: Where the state came from: `report`, `osc`, `screen`, `stall` (default: `report`)
- `--harness <id>`: Id of the harness the state is about, e.g. `claude-code`
- `--kind <kind>`: What a `needs_input` state waits for: `approval` or `question`. Only valid with `needs_input`
- `--agent-session-id <id>`: The harness's own conversation id. Stored on the pane, and turns on the nested-session guard (below)
- `--transcript-path <path>`: The transcript file the harness writes. For a harness whose manifest has a transcript reader, the pane is joined to this exact file
- `--if-state <states>`: Apply only when the pane is in one of these comma-separated states

**Hook fields:**
The last four flags are what `tuios agent-hook` sends, and each is sent only when
set, so a call that uses none of them works against an older daemon. An older
daemon ignores these fields rather than refusing them, so `--if-state` asks the
daemon first and fails, sending nothing, when the daemon does not support it.
`--if-state` is how "the tool ran after an approval" moves a pane from
`needs_input` back to `working` without also turning a `done` pane back to
`working`. A report with `--agent-session-id` is refused while the pane's own
agent is `working` or `needs_input` by its own report and the report names a
different session, or a different harness: that is a nested run, such as a
`claude -p` a tool call started inside the pane. At rest a different session
takes the pane over, as `/clear` or a restart should. `tuios agent-hook` also
sends the harness's pid, which lets a new session from the same harness process
take the pane over mid-turn, as `/clear` after an interrupted turn does. This
command has no flag for it, so a report from here with a different
`--agent-session-id` is refused mid-turn.

**Sources and precedence:**
More than one source can have an opinion about the same pane. Each source is
ranked, and a source may write over a claim ranked at or below its own and
never over one ranked above it. A source updating its own claim is the
same-rank case and is always allowed.

| Source | Rank | What it is |
|--------|------|------------|
| `report` | highest | The harness, or its hook shim, calling for itself |
| `osc` | | An escape sequence the pane emitted |
| `screen` | | A rule matched against the pane's rendered text |
| `stall` | lowest | The output-stall heuristic |

There is one exception, for the case ranking alone gets wrong: a `screen` rule
that matched a blocking prompt may write over a higher-ranked claim that has
gone stale, meaning the pane has painted since that claim was stamped and the
claim has stood unrefreshed for two seconds. The displaced claim is put back the
moment a later look finds the prompt gone. It applies only to the daemon's own
screen tier, which has actually read the pane; passing `--source screen` here
never overrides anything. See
[Agent state](AGENT_STATE.md#the-one-exception-a-visible-blocker).

`report` is the default, so a caller written before sources existed keeps its
authority. A report the daemon declines prints to stderr and leaves the state
alone:

```
Not applied: a higher-ranked source owns this pane. It still reports working.
```

The other refusals name their own reason on that line: an `--if-state` that did
not hold, or a nested run from another conversation or another harness.

**Examples:**
```bash
# Mark the focused pane as working
tuios set-agent-state working

# Mark a specific pane as needing input, with a note
tuios set-agent-state needs_input -w build -m "awaiting approval"

# Say the block is an approval, for a named conversation
tuios set-agent-state needs_input --kind approval --agent-session-id 5f1c -m "approve Bash: make"

# Clear a block after the tool ran, and leave any other state alone
tuios set-agent-state working --if-state needs_input

# Report on behalf of a named harness, from an escape sequence
tuios set-agent-state working --source osc --harness claude-code

# Clear a pane's agent state
tuios set-agent-state none
```

### `tuios set-agent-meta`

Record short facts about the agent in a pane, such as its model, how full its
context is, or a one-line summary of the task. The rail draws them on the
second line of the agent's row. They are display only: they never change the
agent state, a wait, an alert or a message.

**Usage:**
```bash
tuios set-agent-meta [key=value ...] [flags]
```

Each argument is `key=value`, and `key=` removes the key. A key is 1 to 24
lower-case letters, digits, `_` or `-`, starting with a letter. A value has its
control characters replaced and is cut to 80 characters; the keys that were cut
are named on stderr. One call sets at most 16 keys, and a pane holds at most 32.
Keys keep the position they first arrived in, so an update does not reorder the
row. The metadata clears when the agent leaves the pane.

A call that repeats the values the pane already holds changes nothing and sends
nothing to attached clients, and renews a TTL only once less than half of it is
left, so a feed may write on every tick. The keys `now` and `prompt` are
written by tuios from what the harness hooks report, and are refused here; see
[`tuios agent-log`](#tuios-agent-log).

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <id-or-name>`: Target window (default: focused)
- `--source <name>`: Who is writing, so `--clear` removes only this writer's keys
- `--ttl <duration>`: Drop the keys this call sets after this long, at most `24h` (default: keep until removed)
- `--clear`: Remove every key this source wrote (every key with no `--source`) first. The keys tuios writes, `now` and `prompt`, stay
- `--json`: Print the result

**Examples:**
```bash
# From a statusline or hook: the model and context use, for a minute
tuios set-agent-meta -w "$TUIOS_PANE_ID" --source statusline --ttl 60s model=opus context=42%

# Remove one key
tuios set-agent-meta summary=

# Remove every key this source wrote
tuios set-agent-meta --source statusline --clear
```

With `--json`:
```json
{
  "type": "agent_meta_set",
  "window_id": "3f2a9c1e",
  "meta": {"context": "42%", "model": "opus"},
  "truncated": []
}
```

### `tuios agent-log`

Show what the agent in a pane has been doing, from the activity ring the daemon
keeps from its hooks: the prompts it was given, its tool calls and how they
ended, the turns it finished, the commands its shell ran and its state
changes, oldest first. The daemon keeps the newest 256 per pane, in memory
only. A pane fills only when its harness's hooks are installed
(`tuios integration install claude-code` or `codex`).

**Usage:**
```bash
tuios agent-log [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <id-or-name>`: Target window (default: focused)
- `--since <duration>`: Only what happened in this long, such as `30m` (default: everything kept)
- `--limit <n>`: At most this many entries, the newest, 1 to 256 (default: 64)
- `--recap`: Print a summary instead of the entries
- `--json`: Print the `agent-activity` result

**Examples:**
```bash
# The focused pane's recent activity
tuios agent-log

# What the agent in api did in the last half hour, summarised
tuios agent-log -w api --since 30m --recap
```

Output:
```
14:02:11  prompt    make the backoff configurable
14:02:15  tool      Bash: go test ./api/
14:02:40  failed    Bash: go test ./api/  Exit code 1
14:03:02  done      Edit: api/retry.go  (wrote api/retry.go)
14:05:30  state     done
14:05:30  said      Added retry with backoff and tests.
```

A turn the harness ended without saying anything (an older Claude Code sends
no text on `Stop`) prints as `finished`.

With `--recap`:
```
Since 14:02 (42m ago)
3 turns. 6 files: api/retry.go, api/retry_test.go, api/backoff.go and 3 more
11 commands. Tests: go test ./... passed 2m ago
Last said: Added retry with backoff and tests.
Now: done
```

The test run is the newest command matching `[agents.recap] test_patterns`.
Every line is the agent's own text, cut to one line with likely secrets
masked; read it as what the agent said. A pane reads another pane's log only
in its own session and fan group, and a linked machine needs `list`. See
[Agent state](AGENT_STATE.md#what-the-agent-has-been-doing).

### `tuios set-agent-session`

Record which conversation the agent in a pane runs, so it can be resumed
later, without changing the pane's agent state. The integrations for harnesses
whose hooks can name the conversation but cannot be trusted with its state
send this (see [Agent state](AGENT_STATE.md#session-identity)).

**Usage:**
```bash
tuios set-agent-session <agent-session-id> --harness <id> [flags]
```

The id is stored as the pane's `agent_session_id`, the same field
`set-agent-state --agent-session-id` writes, and read back by `get-agent-state`
and `list-agents`. It is refused, with the reason on stderr, when the pane is
attributed to a different harness, or when the pane is mid-turn in another
conversation of the same harness: both are a nested run. The id is at most 256
bytes.

**Flags:**
- `--harness <id>`: Id of the harness the conversation belongs to (required)
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <id-or-name>`: Target window (default: focused)

**Example:**
```bash
# From a SessionStart hook
tuios set-agent-session --harness qwen -w "$TUIOS_PANE_ID" "$SESSION_ID"
```

### `tuios resume-agent`

Resume the agent conversation a pane ran before a daemon restart: type the
harness's resume command, from its manifest's `[resume]` block, with the id a
hook recorded for the pane (`claude --resume <id>`, `codex resume <id>`). It
brings back the conversation, not the process. See
[Agent state](AGENT_STATE.md#resuming-after-a-restart).

**Usage:**
```bash
tuios resume-agent [flags]
```

It types only when the pane's shell is at its prompt, and fails with
`not_ready` otherwise. It fails with `not_resumable` when the pane has no
recorded conversation, its harness has no resume command, the recorded id is
not one plain shell token, or the pane runs on another machine. Nothing is
typed on a failure. A pane's Resume row in the Inbox closes when it succeeds.
It works on any pane with a recorded conversation, including one whose agent
had exited before the restart, which the restore itself does not offer.

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <id-or-name>`: Target window (default: focused)
- `--dry-run`: Print the command without typing it
- `--json`: Output result as JSON

**Example:**
```bash
tuios resume-agent -w build --dry-run
# claude --resume 5f1c2b7e-9a3d-4c1e-8f00-1234567890ab

tuios resume-agent -w build
# Resumed claude-code conversation 5f1c2b7e-... in 3f2a9c1e: claude --resume 5f1c2b7e-...
```

```json
{
  "success": true,
  "message": "command executed",
  "window_id": "3f2a9c1e-0000-4000-8000-000000000000",
  "harness": "claude-code",
  "agent_session_id": "5f1c2b7e",
  "argv": ["claude", "--resume", "5f1c2b7e"],
  "command": "claude --resume 5f1c2b7e",
  "typed": true
}
```

What a daemon restart does with these conversations is
`daemon.resume_agents`: `ask` (default) opens a Resume row per pane in the
Inbox, answered with `y`; `auto` types the command into each restored shell;
`off` does neither.

### `tuios set-session-name`

Set the label a session shows in the sidebar and the dock.

The session keeps its own name for addressing, persistence and
`TUIOS_SESSION`, so a script that targets it by name keeps working. Pass no
name to clear the label.

**Usage:**
```bash
tuios set-session-name [name] [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)

**Examples:**
```bash
# Label the current session
tuios set-session-name "Payments API"

# Label a specific session
tuios set-session-name -s work "Payments API"

# Clear the label
tuios set-session-name
```

### `tuios set-session-accent`

Set the accent a session uses. It is shared by every client attached to the
session and kept across a reattach. Pass no accent to clear it.

**Usage:**
```bash
tuios set-session-accent [accent] [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)

**Examples:**
```bash
# Accent the current session
tuios set-session-accent cyan

# Accent a specific session
tuios set-session-accent -s work cyan

# Clear the accent
tuios set-session-accent
```

### `tuios set-workspace-name`

Name a workspace so the dock and the sidebar show the label instead of the
number. The number stays the workspace's identity, and is the label an unnamed
workspace shows. Pass no name to clear it.

**Usage:**
```bash
tuios set-workspace-name <workspace> [name] [flags]
```

**Arguments:**
- `workspace`: Workspace number, 1-based
- `name`: Label for the workspace. Omit to clear it.

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)

**Examples:**
```bash
# Name workspace 2
tuios set-workspace-name 2 review

# Name a workspace in a specific session
tuios set-workspace-name -s work 2 review

# Clear the name
tuios set-workspace-name 2
```

---

## Inspection Commands

Query the state of a running TUIOS session. These commands are designed for scripting and return structured data about windows and session state.

**Note:** These commands query the daemon's stored state directly and work even when no TUI client is attached to the session. This makes them ideal for background scripting and monitoring.

### `tuios list-verbs`

Print the control protocol's verb catalog: every verb with its parameter schema,
accepted values, and example requests, plus the protocol version and the stable
error codes.

```bash
tuios list-verbs [verb] [--json]
```

This is the discovery entry point for scripting and for agents driving TUIOS. It
needs no documentation to interpret: the schema, the value sets, and the error
vocabulary are all in the output.

**Examples:**

```bash
# Every verb with its parameters
tuios list-verbs

# Just one verb
tuios list-verbs capture-pane

# Machine-readable
tuios list-verbs --json | jq '.verbs[].verb'
```

### `tuios list-hooks`

List the hooks and what each one last did.

**Usage:**
```bash
tuios list-hooks [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `--event <name>`: Only the hooks on this event
- `--json`: Output as JSON (default is human-readable table)

**Example output:**
```
╭────────────────────┬─────────┬──────────────────────────┬──────┬────────┬───────────────────────────╮
│ EVENT              │ SIDE    │ COMMAND                  │ RUNS │ STATE  │ LAST                      │
├────────────────────┼─────────┼──────────────────────────┼──────┼────────┼───────────────────────────┤
│ after-new-window   │ session │ ~/.config/tuios/new.sh   │ 2    │ ran    │ 2026-08-31T21:16:32+04:00 │
│ after-agent-state  │ session │ notify-send "$TUIOS_AG…  │ 0    │ waiting│                           │
│ after-attach       │ client  │ tmux-style-banner        │ 1    │ failed │ exit 127: command not fo… │
╰────────────────────┴─────────┴──────────────────────────┴──────┴────────┴───────────────────────────╯
```

A hook runs its command for the side effect, so a command that was never found
used to look exactly like one that worked. This is where the difference is
visible. `RUNS` of 0 means the command is fine and the event never happened. A
row that is missing altogether means the event name is misspelled and the hook
was never loaded.

`SIDE` says which process runs it. The daemon runs the hooks for the facts it
owns, so those fire on a detached session and fire once however many clients are
attached. The client runs the ones that need its terminal, so they are only
listed while a client is attached.

**JSON Output Structure:**
```json
{
  "success": true,
  "hooks": [
    {
      "event": "after-new-window",
      "side": "session",
      "command": "~/.config/tuios/new.sh",
      "runs": 2,
      "last_exit": 0,
      "last_run": "2026-08-31T21:16:32+04:00",
      "last_error": "",
      "last_ms": 16
    }
  ],
  "total": 1,
  "events": ["after-new-window", "..."],
  "client_attached": false
}
```

`events` lists every name a hook can be written on. `client_attached` says
whether a client answered for its half of the table: false means the client rows
are missing because nobody is attached, not that no client hooks exist.

### `tuios list-dock-components`

List the dock's components: what the bar is made of, what each cell reads, and
what each component's command last did.

**Usage:**
```bash
tuios list-dock-components [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `--json`: Output as JSON (default is human-readable table)

**Example output:**
```
╭───────────────┬───────┬─────────┬──────────────────────────┬────────┬──────────────────────╮
│ COMPONENT     │ SIDE  │ SOURCE  │ REFRESH                  │ STATE  │ READS                │
├───────────────┼───────┼─────────┼──────────────────────────┼────────┼──────────────────────┤
│ mode          │ left  │ builtin │ render                   │ drawn  │                      │
│ workspaces    │ left  │ builtin │ render                   │ drawn  │                      │
│ custom/branch │ left  │ custom  │ event:after-focus-change │ drawn  │ feat/dock-components │
│ windows       │ center│ builtin │ render                   │ drawn  │                      │
│ custom/k8s    │ right │ custom  │ interval 30s             │ failed │ exit 127: ...        │
╰───────────────┴───────┴─────────┴──────────────────────────┴────────┴──────────────────────╯
```

A component whose command fails is hidden from the bar rather than left showing
a stale value, so this is where the failure is visible. `STATE` is `drawn`,
`hidden`, `failed`, or `gave up`; `READS` carries the cell text, or the exit
code and error when there is one.

The dock is composed by the attached client, so this needs a client attached.

**JSON Output Structure:**
```json
{
  "success": true,
  "components": [
    {
      "name": "custom/branch",
      "side": "left",
      "source": "custom",
      "refresh": "event",
      "events": "after-focus-change",
      "command": "~/.config/tuios/dock/git-branch.sh",
      "on_click": "",
      "max_width": 24,
      "text": "\u001b[35m\uf418\u001b[0m main",
      "visible": true,
      "last_exit": 0,
      "last_run": "2026-08-23T07:02:11Z",
      "last_error": "",
      "stopped": false
    }
  ]
}
```

`source` is `builtin` or `custom`. `refresh` is `render` (drawn from model state
on frames that were happening anyway), `once`, `interval` (with `interval`),
`push`, or `event` (with `events`). `text` is the cell as drawn, escapes
included. `last_error` and `last_exit` describe the component's last run, and
`stopped` says it has failed enough times to be left alone until a
`refresh-dock`.

### `tuios refresh-dock`

Re-run a dock component now, whatever its `refresh` mode says.

**Usage:**
```bash
tuios refresh-dock [component] [flags]
```

With no argument every component is re-run. Refreshing also clears a give-up, so
a component that failed five times in a row starts working again once its script
is fixed, without restarting the session.

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `--json`: Output as JSON

**Examples:**
```bash
# After editing the script it runs
tuios refresh-dock agents

# From a hook, so the cell updates the moment the thing it reports changes
#   [hooks]
#   after-agent-state = "tuios refresh-dock agents"
```

See `examples/dock/` for working components and the config that wires them up.

### `tuios list-windows`

List all windows in a TUIOS session.

**Usage:**
```bash
tuios list-windows [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `--json`: Output as JSON (default is human-readable table)

**Examples:**
```bash
# List windows in table format
tuios list-windows

# Output as JSON for scripting
tuios list-windows --json

# Query a specific session
tuios list-windows -s mysession --json
```

**Example output:**
```
╭─────┬──────────┬───────┬────┬───────┬───────╮
│ IDX │ ID       │ NAME  │ WS │ SIZE  │ AGENT │
├─────┼──────────┼───────┼────┼───────┼───────┤
│ *0  │ 0ceb415a │ dev   │ 1  │ 60x38 │ none  │
│ 1   │ ce9ae44c │ build │ 1  │ 60x38 │ none  │
╰─────┴──────────┴───────┴────┴───────┴───────╯

2 window(s). * marks the focused one.
```

The `ID` column is the 8-character prefix that `-w` accepts. With no windows
the command says so and points at `tuios new-window`.

**JSON Output Structure:**
```json
{
  "current_workspace": 1,
  "focused_index": 0,
  "focused_window_id": "0ceb415a-9f5c-44d8-a57c-60c132f54572",
  "message": "command executed",
  "success": true,
  "total": 2,
  "windows": [
    {
      "agent_state": "none",
      "custom_name": "dev",
      "cwd": "/home/me/src/api",
      "display_name": "dev",
      "focused": true,
      "height": 38,
      "index": 0,
      "minimized": false,
      "pty_id": "fd022e1f-7d42-4c45-8ddf-5ecaf8bee488",
      "title": "Terminal 0ceb415a",
      "width": 60,
      "window_id": "0ceb415a-9f5c-44d8-a57c-60c132f54572",
      "workspace": 1,
      "x": 0,
      "y": 0
    },
    {
      "agent_state": "none",
      "custom_name": "build",
      "cwd": "/home/me/src/api",
      "display_name": "build",
      "focused": false,
      "height": 38,
      "index": 1,
      "minimized": false,
      "pty_id": "23ed3d8a-b6f4-4047-b01a-3d2f1dd83515",
      "title": "build",
      "width": 60,
      "window_id": "ce9ae44c-7e64-424c-acd9-48cb21ac55c5",
      "workspace": 1,
      "x": 60,
      "y": 0
    }
  ],
  "workspace_windows": [2, 0, 0, 0, 0, 0, 0, 0, 0]
}
```

`custom_name` is present only on a window that was given a name, `cwd` only
when the shell has reported its directory, and `host` only when the window's
process runs on another machine. `agent_message` and `agent_state_at` appear
once a pane has reported an agent state. The daemon answers this from its own
state, so the shape is the same whether or not a client is attached.

### `tuios get-window`

Get detailed information about a specific window.

**Usage:**
```bash
tuios get-window [id-or-name] [flags]
```

**Arguments:**
- `id-or-name`: Window ID or custom name. If omitted, returns the focused window.

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active).
  `host:session` reads a session on another machine.
- `--json`: Output as JSON (default is human-readable)

It reads with the `get-window` verb, so from inside a pane it needs only the
`read` grant, on the pane's own session and its fan group. Against a daemon
from before that verb it sends the client protocol's `GetWindow` command, as
it used to.

**Examples:**
```bash
# Get focused window info
tuios get-window

# Get focused window as JSON
tuios get-window --json

# Get specific window by name
tuios get-window dev --json

# Get window by ID
tuios get-window a1b2c3d4 --json

# Query a specific session
tuios get-window -s mysession dev --json
```

**Example output:**
```
name           build
id             e5f6a7b8-1c2d-4e3f-9a8b-7c6d5e4f3a2b
index          2
title          Terminal e5f6a7b8
workspace      1
size           120x40
focused        false
minimized      false
agent          working
agent message  awaiting approval
```

The `agent message` line appears only when the pane reported one.

**JSON Output Structure:**

With a client attached, the client answers:

```json
{
  "cursor_visible": true,
  "cursor_x": 40,
  "cursor_y": 0,
  "custom_name": "build",
  "display_name": "build",
  "focused": false,
  "fullscreen": false,
  "has_foreground_process": false,
  "height": 38,
  "id": "ce9ae44c-7e64-424c-acd9-48cb21ac55c5",
  "message": "command executed",
  "minimized": false,
  "pty_id": "23ed3d8a-b6f4-4047-b01a-3d2f1dd83515",
  "scrollback_lines": 0,
  "shell_pgid": 46889,
  "success": true,
  "title": "build",
  "width": 60,
  "workspace": 1,
  "x": 60,
  "y": 0
}
```

With no client attached, the daemon answers with one entry of the
`list-windows` shape above: `window_id` rather than `id`, plus `index`, `cwd`
and `agent_state`, and no cursor or process fields.

### `tuios session-info`

Get information about the TUIOS session state.

**Usage:**
```bash
tuios session-info [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `--json`: Output as JSON (default is human-readable)

**Examples:**
```bash
# Get session info in human-readable format
tuios session-info

# Get session info as JSON
tuios session-info --json

# Query a specific session
tuios session-info -s mysession --json
```

**Example output:**
```
session        work
display name   Payments API
accent         cyan
windows        3
workspace      1 of 9
tiling         tiling
size           120x40
attached       true
named          2=review
```

The `display name`, `accent` and `named` lines appear only when those are set.

**JSON Output Structure:**
```json
{
  "accent": "",
  "current_workspace": 1,
  "display_name": "",
  "height": 40,
  "layout_mode": "bsp",
  "master_ratio": 0.5,
  "message": "command executed",
  "mode": "unknown",
  "num_workspaces": 9,
  "session_id": "2e131c6c-4555-4d08-8c4e-7abb25f5522f",
  "session_name": "work",
  "success": true,
  "tiling_mode": "tiling",
  "tui_attached": true,
  "width": 120,
  "window_count": 2,
  "workspace_names": {},
  "workspace_order": null
}
```

**Fields:**
| Field | Description |
|-------|-------------|
| `session_name` | The session's name, the one `-s` takes |
| `session_id` | The session's id |
| `display_name` | The session's display name, empty when none is set |
| `accent` | The session's accent, empty when none is set |
| `current_workspace` | Active workspace number |
| `num_workspaces` | Number of workspaces the session has |
| `workspace_names` | Named workspaces, keyed by number. A workspace with no name is left out |
| `workspace_order` | The workspace order when it was rearranged, `null` for the plain ascending order |
| `window_count` | Number of windows across all workspaces |
| `tiling_mode` | `tiling` or `floating` |
| `layout_mode` | The tiling layout in use: `bsp`, `master-stack` or `scrolling`, or `unknown` before a client has reported one |
| `master_ratio` | The master pane's share of the width in the `master-stack` layout |
| `mode` | Always `unknown`. The input mode belongs to the attached client, which the daemon does not ask |
| `width`, `height` | The session's size in cells |
| `tui_attached` | Whether a client is attached |

The theme is not listed here. It is a session option: read it with
`tuios get-config appearance.theme`.

### `tuios capture-pane`

Capture the content of a terminal pane and write it to stdout.

**Usage:**
```bash
tuios capture-pane [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <id-or-name>`: Target window (default: focused)
- `-S, --scrollback`: Include the full scrollback history
- `--lines <N>`: Keep only the last N lines (0 keeps all)
- `--ansi`: Preserve ANSI escape codes (colors, styles)
- `--resolved`: Rewrite ANSI index colours (31, 91, 38;5;n, ...) to 24-bit
  RGB, so a capture matches what a themed client paints. Indices below 16 come
  from the palette; the rest of the standard 256-colour cube and grey ramp use
  their fixed values. Implies `--ansi`. Without a palette this uses the xterm
  defaults.
- `--palette <#rrggbb,...>`: The 16 hex colours a client's theme paints ANSI
  indices 0-15 with, used by `--resolved`. Must have exactly 16 entries.
- `--last-command`: Capture only what the last finished command printed, as
  plain text, read between the shell's OSC 133 marks. Fails with
  `no_shell_integration` when no command has finished under them.

A `--ansi` capture without `--resolved` keeps the guest's SGR indices (e.g.
`\x1b[31m`); the consumer resolves them against its own palette. `--resolved`
is for consumers that render the capture verbatim, and implies `--ansi`.

`--lines` counts from the last line that has content, so the blank rows below
the cursor do not count. This is how you read the tail of a long scrollback
without pulling all of it.

**Examples:**
```bash
# Capture the focused window's visible screen
tuios capture-pane

# Capture a specific window with its scrollback
tuios capture-pane -w build --scrollback

# Read the last 40 lines a build printed
tuios capture-pane -w build --scrollback --lines 40

# Capture with ANSI colors preserved
tuios capture-pane --ansi

# Capture with colours resolved to RGB against the current theme palette
tuios capture-pane --ansi --resolved --palette "#45475a,#f38ba8,#a6e3a1,#f9e2af,#89b4fa,#f5c2e7,#94e2d5,#bac2de,#585b70,#f38ba8,#a6e3a1,#f9e2af,#89b4fa,#f5c2e7,#94e2d5,#a6adc8"

# Pipe to a file
tuios capture-pane -w editor --scrollback > pane.txt

# What the last command in the build pane printed, and nothing else
tuios capture-pane -w build --last-command
```

---

### `tuios screenshot`

Render a window to a styled image and save it.

**Usage:**
```bash
tuios screenshot [flags]
```

**Flags:**
- `-s, --session <name>`: Target session (default: most recently active)
- `-w, --window <id-or-name>`: Target window (default: focused)
- `-f, --format <fmt>`: `png` (default), `svg`, `ansi`, `html` or `txt`
- `--frame <style>`: `window` (default), `plain` or `none`
- `--theme <name>`: Render in this palette instead of the session's
- `-o, --out <path>`: Write here instead of a generated name
- `-S, --scrollback`: Put the pane's history above the screen
- `--lines <N>`: Bound the history to the last N rows
- `--cursor`: Draw the cursor cell
- `--no-copy`: Do not try to copy the image to the clipboard
- `--json`: Output the result as JSON

The daemon renders and writes the file, so this works on a detached session
with nobody attached. It prints the path it wrote.

Everything about the frame comes from the `screenshot.*` options: padding, a
wash derived from your theme, corner radius, shadow, title bar and window
control marks. `png` and `svg` carry the frame; `ansi` and `txt` are the bare
stream.

With no theme set, basic and indexed colors fall back to the xterm reference
defaults. Only your terminal knows its own palette, so that is a guess and the
result says so on stderr. `--theme` renders in a named palette instead.

A capture is drawn in your terminal's own font where the terminal will say
which font that is. kitty answers that question, so a capture from a kitty
client comes out with your Nerd Font icons in it and no setting to make.

Where the terminal does not answer, `screenshot.font_family` names the font to
draw with, and `screenshot.font_file` points straight at a file and wins over
everything else. The daemon renders with those two as well, because it has no
terminal to ask. With none of them the capture is drawn in a built in font that
has no icons, and a missing glyph draws as a dotted outline box rather than a
silent blank.

`screenshot.font_file` also embeds that font in SVG and HTML output so those
files stand alone, which makes them megabytes rather than kilobytes. Only that
setting does: a font tuios found by asking your terminal is used to draw the
picture and is not copied into an export you did not ask to be standalone.

**Examples:**
```bash
# The focused window, as a PNG under screenshot.directory
tuios screenshot

# A named window on a named session, detached is fine
tuios screenshot -s work -w build

# With history above the screen
tuios screenshot --scrollback --lines 200

# An SVG for a README
tuios screenshot --format svg --out demo.svg

# Re-render in another palette
tuios screenshot --theme catppuccin_mocha

# For a script
tuios screenshot --json
```

Region and full screen captures are not CLI commands. They need a viewport and
composed chrome, which only an attached client has: press the leader then `C`
in the TUI and drag, or press `f`.

---

## More Commands

These commands have no section of their own here. Each one's `--help` lists
its flags and examples, and `tuios --skill` shows how an agent in a pane uses
them.

**Windows and workspaces:**

| Command | What it does |
|---------|--------------|
| `tuios focus-window [window]` | Move the focus to a pane, by name, id, `--relative next` or `--direction left` |
| `tuios move-window <workspace>` | Move a window to another workspace (`--follow` goes with it) |
| `tuios select-workspace <workspace>` | Show a workspace |
| `tuios list-workspaces` | List the workspaces in a session and how many windows each holds |
| `tuios set-window` | Rename a window (`--name`), minimize it (`--minimize`) or restore it (`--restore`) |
| `tuios split-window <horizontal\|vertical>` | Divide a pane and open a new one beside it. Needs an attached client and tiling on |
| `tuios set-layout` | Turn tiling on or off (`--tiling`), reset split ratios (`--equalize`), or flip the focused split (`--rotate`) |

**Agents:**

| Command | What it does |
|---------|--------------|
| `tuios list-agents` | List the agent panes in a session and what each is doing. `--all-sessions` lists every session on this machine, each row named `session/name`; `--all-hosts` lists every session on every host, with a SESSION column, and a host that is down shows the rows it last gave. `--select` lists the panes a [selector](AGENT_STATE.md#selectors) matches, in every session, and prints the `--confirm` token for them |
| `tuios list-attention` | List the Inbox: what is waiting for you in every session, on this machine and on every linked host (see below). `--host` narrows it to one machine, `--select` to what a selector matches |
| `tuios peek-prompt` | Show the prompt an agent is blocked on, its options and the answers it takes, without attaching |
| `tuios respond <action> [value]` | Answer the prompt an agent is blocked on. Only from the person: an attached client's Inbox, or a shell outside every pane with `[daemon] respond_from_shell`, or from a pane the person gave the `respond` grant |
| `tuios get-agent-state` | Read a pane's reported agent state |
| `tuios set-agent-meta [key=value ...]` | Record display metadata about a pane's agent (model, context, a summary) for the rail |
| `tuios set-agent-session <id> --harness <h>` | Record which conversation a pane's agent runs, for a later resume, without changing its state |
| `tuios resume-agent [-w pane] [--dry-run]` | Type the pane's recorded conversation's resume command into its shell, after a daemon restart |
| `tuios send-agent-message <text>` | Leave a message in another agent's inbox, or post a notice to the session. `--from human` from inside a pane is refused with `forbidden`: only the person at an attached client can send as `human` (see [Who can act as the person](AGENT_STATE.md#who-can-act-as-the-person)). With `-s HOST:SESSION` and that host's link down, the message waits on this machine and goes when the link is back; the Inbox shows it under Waiting to send. `--select` sends one message to every agent pane a selector matches, after listing them: it asks at a terminal, and takes `--yes` or `--confirm TOKEN` otherwise |
| `tuios read-agent-messages` | Read the messages agents have left in this session. Reading `-w human` from inside a pane is always a peek |
| `tuios ask-agent <text>` | Ask another agent a question and wait for its answer. Fails with `prompt_stalled` when the target shows no sign of taking the question within `--stall-timeout` (5000 ms) of Enter. `--select` asks every agent pane a selector matches, at most 16 at once, after the same confirmation as `send-agent-message --select`; a pane on `needs_input` is refused in its own row |
| `tuios queue <text>` | Leave a message for an agent that is typed as a prompt when it comes to rest, never over a prompt it waits on and never twice. `queue ls` lists what waits, `queue rm ID` or `queue rm --all -w PANE` drops it. See [`tuios queue`](#tuios-queue) |
| `tuios review [SESSION]` | Show what the agent in a pane changed against its base, with the notes left on it. `review note FILE:LINE TEXT` leaves one, `review notes` lists them, `review send` sends the unsent ones to the agent as one queued message. See [`tuios review`](#tuios-review) |
| `tuios start-agent <agent>` | Start an agent in a new pane and return once it shows it is at its prompt, optionally typing a first `--prompt`. `-s HOST:SESSION` starts it on another machine, in its checkout of the repository you are in. `--protocol acp\|codex` runs it headless as a transcript. See [above](#tuios-start-agent) |
| `tuios agent-proto --protocol P -- <agent>` | The pane program of `start-agent --protocol`: run an agent headless over ACP or the Codex app-server and show it as a transcript. See [above](#tuios-agent-proto) |
| `tuios explain-agent-detect` | Show what the agent detector sees in a pane |
| `tuios explain-agent-screen` | Show what a harness's screen and title rules make of a pane: the tail, each rule's region and the text it read there, why each refusal refused (strings, patterns, nested groups), the title and last OSC 9;4 progress report, and which manifest file is in force |
| `tuios integration install [harness...]` | Write tuios's managed hook entries or plugin into a harness's configuration: claude-code, codex, gemini-cli, opencode, kilo, amp, kimi and pi report state; antigravity, copilot, crush, cursor-agent, devin, droid, grok, hermes, qoder and qwen report the session id only (`--all` for every harness that has run here, `--command` for a tuios not on PATH). `--mcp` also registers `tuios mcp` as an MCP server named tuios with claude-code, codex, gemini-cli and opencode; `--mcp-write` registers it with `--write`. `--statusline` points Claude Code's status line at `tuios agent-statusline`, which feeds the model, context use and cost to the rail; a status line of your own is never replaced, and `--then CMD` (which implies `--statusline`) chains to it. See [Agent state](AGENT_STATE.md#harness-integrations) |
| `tuios integration uninstall [harness...]` | Remove the hook entries tuios wrote, the MCP server entry it wrote and the Claude Code status line it wrote (putting back the command it chained to), and nothing else |
| `tuios integration status [harness...]` | Say whether each integration is installed and current, and whether it reports state or the session id, for the four harnesses with an MCP registration whether `tuios mcp` is registered, and for Claude Code whether the status line feed is installed (`--json`, with `reports`, `mcp` and `status_line`) |
| `tuios mcp` | Serve tuios to an agent harness as an MCP server over stdio. Read-only by default and held to the session of the pane it runs in; `--write` adds the tools that type into panes, `--scope all` reaches every session. See [tuios mcp](#tuios-mcp) |
| `tuios doctor shell` | Per pane: whether its shell marks its commands with OSC 133, which `tuios run`, `wait-for command-finished` and `capture-pane --last-command` need, and, when one does not, the lines that turn the marks on for your `$SHELL` (zsh, and bash 4.4 or newer; fish 4 sends them itself). A pane that marks its prompts and ran a command without marking it is flagged as prompt marks only, and one that has not run a command yet is said to mark its prompts (`-s`, `--json`, with `command_mark_seen` and `prompt_marks_only`) |
| `tuios doctor agents` | Per harness: on PATH or not, integration installed and current or not, what it reports, the recognised harnesses with no integration and why, the running agent panes missing theirs, and the harness manifests loaded from the user manifest directory, which of them replace a bundled one, and the files there that failed to load (`--json`) |
| `tuios agent-hook <harness> [event]` | What an installed hook runs: read the hook payload on stdin and report the pane's state, or for a session integration only its conversation id (`set-agent-session`). For Claude Code and Codex the prompt, tool and Stop events also carry the event as activity for [`tuios agent-log`](#tuios-agent-log), and a `Stop` reports `done` with the first line of what the agent said last. A report that ends a turn also sends what the pane's `agent-statusline` feed held back (`set-agent-meta`). `--explain` prints the decision to stderr. With `[agents.approvals]` naming the harness, a permission prompt (Claude Code `PermissionRequest`, including an `ExitPlanMode` plan unless `hold_plans = false`, opencode or Kilo `permission.asked`) then waits for an answer from the Inbox and prints the harness's decision, or nothing when there is none. See [Agent state](AGENT_STATE.md#harness-integrations) and [Approvals from the Inbox](AGENT_STATE.md#approvals-from-the-inbox) |
| `tuios agent-statusline <harness>` | What the Claude Code status line `integration install --statusline` writes runs, and what the opencode and Kilo plugins run for the model and cost: write the model, context use and cost on stdin to the pane's agent metadata. `--then CMD` chains to your own status line. See [above](#tuios-agent-statusline) |
| `tuios tmux-shim [-- command]` | Run a command (your shell when none is given) with a `tmux` on PATH that answers in this tuios session, so a tool that drives tmux, such as Claude Code agent teams (`tuios tmux-shim -- env CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 claude`), opens its panes here. Off until you run it. `--log FILE` moves the log of calls the shim could not answer from `$XDG_STATE_HOME/tuios/tmux-shim.log`; `--log-all` records every call. Not on Windows. See [The tmux shim](TMUX_SHIM.md) |
| `tuios tmux <tmux arguments>` | The shim asked for by name: answer one tmux command line in the caller's session (`tuios tmux display-message -p '#{pane_id}'`). A tmux session is the tuios session, a window `@N` is workspace N, a pane `%N` is a tuios window. See [The tmux shim](TMUX_SHIM.md#commands) for the commands it answers |
| `tuios pane-grants` | Show the pane this runs in and what it may do through tuios. See [below](#tuios-pane-grants) |
| `tuios set-pane-grants` | Give a pane grants (`--grants read,write`), or the default back (`--reset`). See [below](#tuios-pane-grants) |
| `tuios stash put <file>` | Copy a file into the session store and print the stored path |
| `tuios stash get <stored-path> [file]` | Copy a stashed file out of the session store, across a link |
| `tuios stash list` | List the files in the session store |

**Other machines:**

| Command | What it does |
|---------|--------------|
| `tuios hosts` | List the machines in the `[hosts]` config table and the state of each link. A host whose tuios is too old to stream its agents is named below the table, with what to update: its agents are polled and what waits there is not in the Inbox. A host with mail waiting here for its link says how many |
| `tuios hosts add <name> <addr>` | Add a machine. `--tailnet` takes the address from your tailnet; `--command`, `--ssh-option` and `--connect-timeout` tune the link; `--repos-root DIR` says where the host keeps its checkouts, for `fan --host`, `worktree new --host` and `start-agent -s HOST:SESSION`, and is kept when the host is added again without it, as its link policy fields are |
| `tuios hosts remove <name>` | Remove a machine |
| `tuios hosts test <name>` | Open one link to a host and report what happened |
| `tuios hosts tailnet` | List the machines on your tailnet and which are offered as addresses |
| `tuios stdio-proxy [--as NAME]` | Hidden. What the other machine's daemon runs over ssh for a link. `--as` pins the name this machine's link policy is resolved for, whatever the other machine calls itself: put it in a forced command in `authorized_keys` (`command="tuios stdio-proxy --as laptop",restrict ...`) to make the policy a boundary. See [What another machine may do here](CONFIGURATION.md#what-another-machine-may-do-here) |

A call over a link that the far machine's policy does not allow fails with
`forbidden`, and the message names the capability and the `[hosts]` table on
that machine that grants it. By default a linked machine may not answer
prompts: `tuios respond -w HOST:SESSION:WINDOW` needs `"respond"` in `allow`
there.

**Configuration and appearance:**

| Command | What it does |
|---------|--------------|
| `tuios list-options [prefix]` | List every settable configuration option, with its type, default and accepted values |
| `tuios list-options --search <query>` | The options matching a fuzzy query on path, value or description, best first, as the settings page searches |
| `tuios get-config <path>` | Read a configuration option from a running session |
| `tuios list-themes [theme]` | List the themes, and describe one |
| `tuios import-theme <file>` | Convert a terminal colour scheme into a tuios theme |
| `tuios list-glyphs [set]` | List the glyph sets, and describe one |
| `tuios logs` | View daemon logs |

**Tape scripts:**

| Command | What it does |
|---------|--------------|
| `tuios tape play <file.tape>` | Run a tape file in interactive mode |
| `tuios tape exec <file.tape>` | Execute a tape file in a running session |
| `tuios tape validate <file.tape>` | Validate a tape file without running it |
| `tuios tape list` | List all saved tape recordings |
| `tuios tape show <name>` | Display the contents of a tape file |
| `tuios tape delete <name>` | Delete a tape recording |
| `tuios tape dir` | Show the tape recordings directory path |

### `tuios pane-grants`

Show what the pane this command runs in may do through tuios.

**Usage:**
```bash
tuios pane-grants [--json]
tuios set-pane-grants [-s <session>] [-w <window>] (--grants <grant>[,<grant>...] | --reset) [--json]
```

Every pane holds grants, and the daemon checks every call from a pane against
them, however it was made:

| Grant | What the pane may do |
|-------|----------------------|
| `read` | Read its own session and its fan group, with `list-windows`, `get-window`, `capture-pane` and the rest |
| `write` | Type into the panes of its own session that hold nothing it does not, and leave mail there |
| `fan` | Write in its fan group and start agents with `fan` and `start-agent` |
| `respond` | Answer another pane's prompt with `respond`, without the person, and type into a pane waiting on a prompt |
| `admin` | Everything else, as every pane could before grants, such as `run-command` and `attach`. Includes `read`, `write` and `fan`, never `respond` |

A pane without `admin` types (`send-text`, `send-keys`, `run`, `ask-agent`,
`queue`) into another pane only when that pane holds nothing it does not,
since what it types runs with the target's grants, and into a pane on
`needs_input` only when it holds `respond`. A message it queues is checked
again against its grants as they are when the message is typed, and dropped
if they no longer cover the target. Its `send-keys` go to the target's
terminal, never through an attached client, so they cannot drive the window
manager.

A pane holds the grants it was started with (`--grants` on `start-agent`,
`fan` and `new-window`), the ones `set-pane-grants` gave it, or else the
default of `[agents.permissions]` (see
[CONFIGURATION.md](CONFIGURATION.md#what-a-pane-may-do)): `admin` under
`mode = "open"`, the default, and the `grants` list under `mode = "strict"`.
A call the grants do not cover fails with `forbidden`, and the message names
the grant it needed.

`set-pane-grants` from outside every pane may give anything. From a pane it
may change only that pane's own grants unless the pane holds `admin`, and
never give more than the pane holds, so an agent cannot widen itself, and it
cannot type into a pane that holds more to have that pane do it.

**Output:**
```
Pane a1b2c3d4 in session work holds read, write, fan (the default of [agents.permissions], mode strict).
```

**Examples:**
```bash
# From inside an agent's pane
tuios pane-grants

# Let the reviewer only read
tuios set-pane-grants -w reviewer --grants read

# Drop this pane's grants, then start an agent in it
tuios set-pane-grants --grants read,write && exec claude
```

---

## Scripting Examples

These remote control and inspection commands enable powerful scripting workflows.

### Create and Focus Windows

```bash
#!/bin/bash
# Create a development layout

# Create windows and capture their IDs
EDITOR_ID=$(tuios run-command --json NewWindow "editor" | jq -r '.window_id')
TERMINAL_ID=$(tuios run-command --json NewWindow "terminal" | jq -r '.window_id')
LOGS_ID=$(tuios run-command --json NewWindow "logs" | jq -r '.window_id')

# Enable tiling
tuios run-command ToggleTiling

# Send commands to each window
tuios send-keys --literal --raw "nvim ." && tuios send-keys Enter
tuios run-command FocusWindow "$TERMINAL_ID"
tuios run-command FocusWindow "$LOGS_ID"
tuios send-keys --literal --raw "tail -f /var/log/system.log" && tuios send-keys Enter
```

### Query and React to State

```bash
#!/bin/bash
# Wait for a specific condition

# Wait until there are at least 3 windows
while true; do
    WINDOW_COUNT=$(tuios session-info --json | jq '.window_count')
    if [ "$WINDOW_COUNT" -ge 3 ]; then
        echo "Ready with $WINDOW_COUNT windows"
        break
    fi
    sleep 0.5
done
```

### Run a Command and Wait for It

When the pane's shell marks its commands with OSC 133, one call does it and
exits with the build's own status:

```bash
tuios new-window build
tuios run -w build --timeout 120000 --lines 40 -- go build ./...
```

Without shell integration, assemble a marker:

```bash
#!/bin/bash
# Open a window, run a build in it, and block until it finishes.
# The marker is assembled by printf so the literal BUILD_OK never appears in
# the command line the pane echoes, which would match the wait immediately.

tuios new-window build
tuios send-text -w build 'go build ./... ; printf "BUILD_%s %s\n" OK "$?"
'

if tuios wait-for window-output -w build --pattern 'BUILD_OK' --timeout 120000; then
    tuios capture-pane -w build --scrollback --lines 5
else
    tuios capture-pane -w build --scrollback --lines 40
    exit 1
fi
```

### Integration with Other Tools

```bash
#!/bin/bash
# Use fzf to select and focus a window

WINDOW=$(tuios list-windows --json | \
    jq -r '.windows[] | "\(.display_name)\t\(.window_id)"' | \
    fzf --with-nth=1 | \
    cut -f2)

if [ -n "$WINDOW" ]; then
    tuios run-command FocusWindow "$WINDOW"
fi
```

### Automated Testing

```bash
#!/bin/bash
# Run a command and verify output

tuios send-keys --literal --raw "echo 'test-marker-12345'" && tuios send-keys Enter
sleep 0.5

# The marker shows twice once the command ran: in the echoed command line and
# in its output
COUNT=$(tuios capture-pane | grep -c 'test-marker-12345')
if [ "$COUNT" -ge 2 ]; then
    echo "command ran"
fi
```

---

### `tuios mcp`

Serve tuios to an agent harness as a Model Context Protocol server over stdin
and stdout. The harness starts it; nobody runs it by hand.

```bash
tuios mcp [--write] [--scope own|all]
```

**Flags:**

- `--write`: Also list the tools that type into panes: `tuios_send_text`,
  `tuios_send_keys`, `tuios_ask_agent`, `tuios_respond` and `tuios_fan`
- `--scope`: `own` (the default) reaches only the session of the pane the
  server runs in, its fan group, and the sessions a `fan` from it started.
  `all` reaches every session, for a harness that runs outside tuios

**Tools, by default:** `tuios_list_agents`, `tuios_list_windows`,
`tuios_get_agent_state`, `tuios_capture_pane`, `tuios_peek_prompt`,
`tuios_wait_for`, `tuios_read_agent_messages`, `tuios_send_agent_message`,
`tuios_set_agent_state`, `tuios_set_agent_meta` and `tuios_events`. Each is a
daemon verb, and its input schema is generated from the verb table, so it takes
the verb's own parameters. `tuios_events` follows the event stream: it returns
what happened since `after_seq`, or waits up to `wait_ms` for the next events,
and answers with the `last_seq` and `boot_id` to pass to the next call.
`last_seq` is where the daemon's stream stood when the call returned, even when
none of the events since were ones the call may see, so a server held to a
quiet session still moves forward. Only when `max_events` cuts the replay
short is it the seq of the last event returned.

**How it is held to its grant:** every tool call opens its own connection to
the daemon and calls `restrict-connection` on it first, so the daemon refuses
what the flags do not grant, with `forbidden`. The server finds its pane from
the kernel's record of its pid, and where the kernel cannot say, from
`TUIOS_PANE_ID` and `TUIOS_PANE_TOKEN`. A server with `--scope own` that runs
in no pane reaches nothing. A daemon too old to know `restrict-connection` runs
nothing, and the tool says to restart it. Text read from a pane or a message is
marked as data, not instructions. See [restrict-connection](protocol.md#restrict-connection).

The daemon's socket is `TUIOS_SOCKET` when the harness passes it, else the one
`tuios` always uses. A harness that starts its MCP servers without
`XDG_RUNTIME_DIR` still finds a daemon under `/run/user/<uid>` on Linux.

**Registering it:**

```bash
# Hooks and the read-only MCP server
tuios integration install claude-code --mcp

# With the tools that type into panes
tuios integration install codex --mcp-write

# By hand, in Claude Code
claude mcp add --scope user tuios -- tuios mcp
```

---

### `tuios ssh`

Run TUIOS as an SSH server for remote access.

By default, SSH sessions connect to the TUIOS daemon for persistent sessions with multi-client support. This means:
- Sessions persist even when clients disconnect
- Multiple clients can view/control the same session simultaneously
- Session state (windows, workspaces) is preserved across reconnections

**Usage:**
```bash
tuios ssh [flags]
```

**Flags:**
- `--host <string>`: SSH server host (default: "localhost")
- `--port <string>`: SSH server port (default: "2222")
- `--key-path <string>`: Path to SSH host key (auto-generated if not specified)
- `--default-session <string>`: Default session name for all connections
- `--ephemeral`: Run in ephemeral mode (standalone, no daemon)
- `--authorized-keys <string>`: Path to the public keys allowed to connect (default: `~/.config/tuios/authorized_keys`, then `~/.ssh/authorized_keys`)
- `--no-auth`: Give every connection a shell without checking who it is (trusted networks only)

**Who can connect:**

Every connection gets a shell on the machine running the server, so the server
checks who is connecting. It reads public keys from
`~/.config/tuios/authorized_keys`, and from `~/.ssh/authorized_keys` when the
first file is absent.

- With keys: only the holders of those keys connect. Add a key while the server
  runs and it works on the next connection.
- With no keys on `localhost`: every connection is accepted and the server
  prints one warning at startup. This keeps a single-user laptop working with
  no setup.
- With no keys on any other host: the server refuses to start and prints the
  ways forward. Pass `--no-auth` to serve anyway, on a network you trust.

A keys file that cannot be read, does not parse, or holds no key stops startup.
Only an absent file means "no keys are configured".

```bash
# Let one key in
mkdir -p ~/.config/tuios
cat ~/.ssh/id_ed25519.pub >> ~/.config/tuios/authorized_keys
tuios ssh --host 0.0.0.0 --port 2222
```

The interface flags (`--theme`, `--border-style`, `--dockbar-position`,
`--ascii-only`, and the rest of the appearance set) apply to every served
session, layered over the server's config file the same way a local run
layers them.

Settings changed from the in-app settings page apply to that session only;
the server operator's config file is never written from an SSH client.

**Session Selection Priority:**
1. `--default-session` flag (if specified)
2. SSH username (if not generic like "tuios", "root", "anonymous")
3. SSH command argument (e.g., `ssh host attach mysession`)
4. First available session or create new

**Examples:**
```bash
# Start SSH server on default port (daemon mode)
tuios ssh

# Start on custom port
tuios ssh --port 8022

# Listen on all interfaces (needs an authorized_keys file, or --no-auth)
tuios ssh --host 0.0.0.0 --port 2222

# Read the allowed public keys from somewhere else
tuios ssh --authorized-keys /etc/tuios/authorized_keys

# Use custom host key
tuios ssh --key-path /path/to/host_key

# All clients share a single session
tuios ssh --default-session shared

# Run in ephemeral mode (no session persistence)
tuios ssh --ephemeral
```

**Connecting:**
```bash
# Basic connection
ssh -p 2222 localhost

# Connect to a specific session via username
ssh -p 2222 mysession@localhost

# Connect to a specific session via command
ssh -p 2222 localhost attach mysession
```

**Multi-Client Behavior:**
- When multiple clients connect to the same session, the effective terminal size is the minimum of all client dimensions
- State changes (window create/move, workspace switch, etc.) are broadcast to all clients in real-time
- Clients are notified when others join or leave the session

---

## `tuios-web` (Separate Binary)

**Security Notice:** The web terminal functionality has been extracted to a separate binary (`tuios-web`) to provide better security isolation. This prevents the web server from being used as a potential backdoor in the main TUIOS binary.

By default, web sessions connect to the TUIOS daemon for persistent sessions with multi-client support. This means:
- Sessions persist even when browser tabs close
- Multiple browsers/tabs can view/control the same session simultaneously
- Session state (windows, workspaces) is preserved across reconnections
- Settings changed from the in-app settings page apply to that session only; the server's config file is never written from a browser

**Installation:**
```bash
# Homebrew
brew install tuios-web

# AUR
yay -S tuios-web-bin

# Go install
go install github.com/Gaurav-Gosain/tuios/cmd/tuios-web@latest
```

**Usage:**
```bash
tuios-web [flags]
```

**Flags:**
- `--host <string>`: Web server host (default: "localhost")
- `--port <string>`: Web server port (default: "7681")
- `--read-only`: Disable input from clients (view only mode)
- `--max-connections <int>`: Maximum concurrent connections (default: 0 = unlimited)
- `--cert <path>`: TLS certificate in PEM form (serves HTTPS; required to bind a non-loopback host)
- `--key <path>`: TLS private key in PEM form (required with `--cert`)
- `--auto-tls`: Generate and serve a self-signed certificate (managed with `tuios-web cert`)
- `--insecure`: Serve a non-loopback host over plain HTTP, unencrypted (trusted networks only)
- `--touch <auto|on|off>`: Touch support and the on-screen key bar (default: auto-detect)
- `--default-session <string>`: Default session name for all connections (creates shared session)
- `--ephemeral`: Disable daemon mode (sessions don't persist)
- `--theme <name>`: Color theme forwarded to TUIOS instances
- `--show-keys`: Enable showkeys overlay
- `--ascii-only`: Use ASCII characters instead of Nerd Font icons
- `--border-style <style>`: Window border style
- `--dockbar-position <pos>`: Dockbar position
- `--hide-window-buttons`: Hide window control buttons
- `--window-button-style <style>`: Window control style: `pill`, `dots`
- `--window-button-position <position>`: Which end they sit on: `right`, `left`
- `--scrollback-lines <int>`: Scrollback buffer size
- `--no-animations`: Disable UI animations
- `--debug`: Enable debug logging

**Subcommands:**
- `tuios-web cert`: Show the status of the self-signed TLS certificate `--auto-tls` uses
- `tuios-web cert new|info|path|remove`: Rotate, explain, locate, or delete it

**Features:**
- Full TUIOS experience in the browser
- WebGL-accelerated rendering via xterm.js for smooth 60fps
- WebSocket and WebTransport (HTTP/3 over QUIC) protocols
- Bundled JetBrains Mono Nerd Font for proper icon rendering
- Settings panel for transport, renderer, and font size preferences
- Cell-based mouse event deduplication (80-95% traffic reduction)
- Automatic reconnection with exponential backoff
- Self-signed TLS certificate generation for development
- No CGO dependencies (pure Go)
- **Persistent sessions via daemon mode** (default)
- **Multi-client support**: multiple browsers share the same session

**Examples:**
```bash
# Start web server on default port (daemon mode)
tuios-web

# Start on custom port
tuios-web --port 8080

# Reach the server from a phone on the same network, over TLS with a
# self-signed certificate tuios-web generates and keeps for you
tuios-web --host 0.0.0.0 --port 7681 --auto-tls

# Or bring your own certificate
tuios-web --host 0.0.0.0 --port 7681 --cert tuios-cert.pem --key tuios-key.pem

# Same, on a network you trust, with nothing encrypted
tuios-web --host 0.0.0.0 --port 7681 --insecure

# Start in read-only mode (view only)
tuios-web --read-only

# Start with theme and show-keys overlay
tuios-web --theme dracula --show-keys

# Limit concurrent connections
tuios-web --max-connections 10

# All clients share a single session
tuios-web --default-session shared

# Run in ephemeral mode (no session persistence)
tuios-web --ephemeral
```

**Multi-Client Behavior:**
- When multiple clients connect to the same session, the effective terminal size is the minimum of all client dimensions
- State changes (window create/move, workspace switch, etc.) are broadcast to all clients in real-time
- Clients are notified when others join or leave the session

**Accessing:**
```bash
# Open in browser
open http://localhost:7681

# For HTTPS/WebTransport (development with self-signed cert)
open https://localhost:7681

# Note: Your browser will show a security warning for the self-signed certificate.
# Click "Advanced" and proceed to accept the certificate.
```

**Protocol Selection:**
The client automatically selects the best available transport:
1. **WebTransport (HTTP/3 over QUIC)**: Lower latency, better multiplexing (requires HTTPS)
2. **WebSocket (fallback)**: Broad browser compatibility

For complete documentation, see [Web Terminal Mode](WEB.md).

---

### `tuios config`

Manage TUIOS configuration file.

**Subcommands:**
- `tuios config path`: Print configuration file path
- `tuios config edit`: Edit configuration in $EDITOR
- `tuios config reset`: Reset configuration to defaults

#### `tuios config path`

Print the location of the TUIOS configuration file.

**Example:**
```bash
tuios config path
# Output: /Users/username/.config/tuios/config.toml
```

#### `tuios config edit`

Open the configuration file in your default editor.

**Requirements:** The `$EDITOR` or `$VISUAL` environment variable must be set. Falls back to vim, vi, nano, or emacs if found.

**Example:**
```bash
export EDITOR=vim
tuios config edit
```

#### `tuios config reset`

Reset the configuration file to default settings.

**Warning:** This will overwrite your existing configuration after confirmation.

**Example:**
```bash
tuios config reset
# Prompts: Are you sure you want to reset to defaults? (yes/no):
```

---

### `tuios keybinds`

View and inspect keybinding configuration.

**Aliases:** `keys`, `kb`

**Subcommands:**
- `tuios keybinds list`: List all configured keybindings
- `tuios keybinds list-custom`: List only customized keybindings
- `tuios keybinds doctor`: Report every key claimed twice and every key tuios takes from the pane
- `tuios keybinds explain <key>`: Say what tuios does with one key
- `tuios keybinds unbind <action> [key]`: Take a key off one action
- `tuios keybinds free <key>`: Hand a key back to the program in the pane

#### `tuios keybinds list`

Display the common keybindings, as configured, in formatted tables organized
by category. `tuios keybinds doctor` lists every scope.

**Example:**
```bash
tuios keybinds list
```

**Output:** One table per category, and a category with nothing bound is left
out:
- Global
- Window Management
- Workspaces
- Layout
- Modes
- Selection
- System

#### `tuios keybinds list-custom`

Show only keybindings that differ from defaults, with a comparison view.

**Example:**
```bash
tuios keybinds list-custom
```

**Output:** Three-column table showing:
- Action name
- Default keybinding
- Your custom keybinding

#### `tuios keybinds unbind`

Take a key off one action and write the change to `config.toml`.

```bash
# Stop w closing a window, leaving x
tuios keybinds unbind close_window w

# Leave the action with no key at all
tuios keybinds unbind close_window
```

An action with no keys is written as an empty list:

```toml
[keybindings.window_management]
close_window = []
```

That is not the same as leaving the action out of the file. An action the file
does not mention gets its default back the next time tuios starts. An empty list
stays empty.

#### `tuios keybinds free`

Take one key off every action in every scope, so the program in your pane
receives it.

```bash
# Give alt+left back to your shell
tuios keybinds free alt+left
```

Every scope at once is the point. A key tuios still claims anywhere is a key the
program never sees. Two keys cannot be freed this way: the leader key, which is
`keybindings.leader_key` and is moved rather than unbound, and a handful of keys
the input path reads directly. The command says so instead of reporting success.

You can do both from inside tuios as well. Open the keybind manager with the
leader key then `k`, or from the command palette, and press `ctrl+d` on a
binding to remove it or `ctrl+x` to take its key off every action. Typing `#`
in the command palette searches actions rather than commands.

---

### `tuios update`

Replace this tuios with the newest published release.

```bash
# See whether there is a newer release, without installing it
tuios update --check

# Install it
tuios update

# Include prereleases
tuios update --check --pre
```

**Flags:**
- `--check`: Report what would be installed and change nothing
- `--pre`: Count a prerelease as the newest release

**What it will and will not replace.** This only updates a binary that came from
a release archive, which is what the [install script](#quick-install-script-linuxmacos)
downloads. Every other way of installing tuios has something that owns the file,
and overwriting one of those leaves its records describing a file that is no
longer there. The command detects where the binary came from and refuses with
the right command instead:

| Installed by | `tuios update` | What to run |
| --- | --- | --- |
| Install script, or a release archive unpacked by hand | Updates it | `tuios update` |
| Homebrew | Refuses | `brew upgrade --cask tuios` |
| AUR or another system package | Refuses | `yay -S tuios-bin` |
| Nix | Refuses | `nix profile upgrade tuios` |
| `go install` | Refuses | `go install github.com/Gaurav-Gosain/tuios/cmd/tuios@latest` |
| `scripts/install.sh` from a checkout | Refuses | `git pull && ./scripts/install.sh` |

**tuios-web** is updated at the same time when it sits beside `tuios`. The two
talk to one daemon and it compares their versions, so they move together or not
at all: both are downloaded and verified before either is put in place.

**Checksums.** Every download is checked against the release's `checksums.txt`.
A file that does not match is discarded, nothing is installed, and the old
binary is untouched.

**The running daemon** keeps the old build. Sessions you have open go on
working. To move them to the new build, detach, run `tuios kill-server`, then
start tuios again. Panes are restored; the programs that were running in them
are not.

Set `GITHUB_TOKEN` or `GH_TOKEN` to raise the release lookup's rate limit. It is
never required and no token is created for you.

The lookup and the download run through `curl`, which must be on `PATH`. It
honours `HTTPS_PROXY` and `NO_PROXY`, and the token is handed to it on stdin,
not on its command line.

---

### `tuios layout`

Manage saved layout templates.

**Subcommands:**
- `tuios layout list`: List all saved layout templates
- `tuios layout delete <name>`: Delete a saved layout template
- `tuios layout dir`: Print the layout templates directory path
- `tuios layout export <name>`: Export a layout template as JSON

#### `tuios layout list`

List all saved layout templates.

**Example:**
```bash
tuios layout list
```

#### `tuios layout delete`

Delete a saved layout template by name.

**Usage:**
```bash
tuios layout delete <name>
```

**Example:**
```bash
tuios layout delete dev-layout
```

#### `tuios layout dir`

Print the path to the layout templates directory.

**Example:**
```bash
tuios layout dir
# Output: /home/user/.config/tuios/layouts
```

#### `tuios layout export`

Export a saved layout template as JSON for sharing or backup.

**Usage:**
```bash
tuios layout export <name>
```

**Example:**
```bash
tuios layout export dev-layout
```

---

### `tuios completion`

Generate shell completion scripts for command-line autocompletion.

**Supported shells:**
- bash
- zsh
- fish
- powershell

**Usage:**
```bash
tuios completion [shell]
```

**Examples:**

**Bash:**
```bash
# Generate and install completion
tuios completion bash > /etc/bash_completion.d/tuios

# Or for user-specific completion
tuios completion bash > ~/.local/share/bash-completion/completions/tuios
source ~/.bashrc
```

**Zsh:**
```bash
# Generate and install completion
tuios completion zsh > "${fpath[1]}/_tuios"

# Or add to your .zshrc
echo "autoload -U compinit; compinit" >> ~/.zshrc
tuios completion zsh > ~/.zsh/completions/_tuios
```

**Fish:**
```bash
# Generate and install completion
tuios completion fish > ~/.config/fish/completions/tuios.fish
```

**PowerShell:**
```bash
# Generate completion script
tuios completion powershell > tuios.ps1

# Add to your PowerShell profile
echo ". $(pwd)/tuios.ps1" >> $PROFILE
```

---

### `tuios help`

Get help about any command.

**Usage:**
```bash
tuios help [command]
```

**Examples:**
```bash
tuios help              # Show general help
tuios help ssh          # Show help for ssh command
tuios help config edit  # Show help for config edit subcommand
```

---

## Global Flags

The root command's flags are listed under [Root Command](#root-command).
`--debug`, `--cpuprofile` and `--pprof` are accepted by every subcommand too.

---

## Common Usage Examples

### Basic Usage

Start TUIOS normally:
```bash
tuios

# Start with showkeys overlay for screencasting
tuios --show-keys
```

### Theming

```bash
# Start with a specific theme
tuios --theme dracula

# List all available themes
tuios --list-themes

# Preview a theme before using it
tuios --preview-theme nord

# Interactive theme selection with fzf
tuios --theme $(tuios --list-themes | fzf --preview 'tuios --preview-theme {}')

# Use ASCII mode (no Nerd Font required)
tuios --ascii-only

# Combine theme with ASCII mode
tuios --theme gruvbox_dark --ascii-only
```

### Configuration Management

```bash
# Find config file location
tuios config path

# Edit configuration
tuios config edit

# View all keybindings
tuios keybinds list

# View your customizations
tuios keybinds list-custom

# Reset to defaults
tuios config reset
```

### Daemon Mode (Session Persistence)

```bash
# Create a new persistent session
tuios new mysession

# List all sessions
tuios ls

# Attach to an existing session
tuios attach mysession

# Detach from session (inside TUIOS)
# Press Ctrl+B d

# Kill a session
tuios kill-session mysession

# Stop the daemon (kills all sessions)
tuios kill-server
```

### SSH Server Setup

```bash
# Start SSH server on default port
tuios ssh

# Start on custom port with remote access. A host outside this machine
# needs keys, so add one first.
mkdir -p ~/.config/tuios
cat ~/.ssh/id_ed25519.pub >> ~/.config/tuios/authorized_keys
tuios ssh --host 0.0.0.0 --port 8022

# Connect from another machine, with the matching private key
ssh -p 8022 your-server-hostname
```

### Web Terminal Setup (tuios-web)

```bash
# Start web terminal on default port
tuios-web

# Start on custom port with remote access
tuios-web --host 0.0.0.0 --port 8080

# Open in browser
open http://localhost:7681

# Start in read-only mode for demonstrations
tuios-web --read-only

# Start with theme and overlay
tuios-web --theme dracula --show-keys

# Limit connections for production use
tuios-web --max-connections 50 --host 0.0.0.0
```

### Development & Debugging

```bash
# Run with debug logging
tuios --debug
# Then press Ctrl+L during runtime to view logs

# CPU profiling
tuios --cpuprofile cpu.prof
# Use the application, then exit
go tool pprof cpu.prof

# Screencasting with showkeys overlay
tuios --show-keys
# Or toggle during runtime with: Ctrl+B D k
```

### Shell Completions

```bash
# Install bash completion
tuios completion bash | sudo tee /etc/bash_completion.d/tuios

# Install zsh completion
tuios completion zsh > "${fpath[1]}/_tuios"

# Install fish completion
tuios completion fish > ~/.config/fish/completions/tuios.fish
```

---

## Environment Variables

### `$EDITOR` / `$VISUAL`

Used by `tuios config edit` to determine which editor to open.

**Example:**
```bash
export EDITOR=vim
export VISUAL=code
tuios config edit
```

**Fallback order:** `$EDITOR` → `$VISUAL` → vim → vi → nano → emacs

### `TUIOS_NO_DAEMON`

Set to `1` to make a plain `tuios` run a standalone session without the daemon,
the same as `--standalone`, for every run in that shell.

```bash
export TUIOS_NO_DAEMON=1
tuios
```

### `TUIOS_AGENT`

Set on a wrapper that runs an agent tuios cannot see, such as one in a
container or a VM, to name its harness. The daemon reads it from the pane's
foreground process when nothing else identifies the process, and attributes
the pane to that harness, so its screen and title rules run. `tuios agent-hook`
ignores a hook from a different harness than the one it names, which may be any
harness tuios recognises, one with no integration included. The plugins tuios
installs report only when `TUIOS_ENV` or `TUIOS_AGENT` is set.

```bash
TUIOS_AGENT=claude-code docker run -it sandbox claude
```

### Variables tuios sets in a pane

`TUIOS_ENV`, `TUIOS_SOCKET`, `TUIOS_PANE_ID`, `TUIOS_WINDOW_ID`,
`TUIOS_SESSION`, `TUIOS_HOST`, `TUIOS_PANE_TOKEN` and `TUIOS_PANE_GRANTS`. See
[Environment](AGENT_STATE.md#environment) for what each one means.

### Agent detection

`TUIOS_AGENT_AUTODETECT`, `TUIOS_AGENT_DETECT_SECONDS`,
`TUIOS_AGENT_BINARIES` and `TUIOS_AGENT_STALL_SECONDS`, read by the daemon as
it starts. See
[Turning detection off or widening it](AGENT_STATE.md#turning-detection-off-or-widening-it)
and [The stall heuristic](AGENT_STATE.md#the-stall-heuristic).

### `TUIOS_WORKTREE_DIR`

Where the daemon puts the worktrees `tuios worktree new` and `tuios fan`
create, instead of `$XDG_DATA_HOME/tuios/worktrees`. Set it in the daemon's
environment, before it starts.

### `TUIOS_LOG_LEVEL`

The daemon's log level from the start: `off`, `errors`, `basic`, `messages`,
`verbose` or `trace` (or `0` to `5`). It wins over `daemon.log_level`.
`tuios logs` reads what it records.

### `TUIOS_NO_SOUND`

Any value silences the agent alert sounds, whatever
`notifications.agent.sound` says. For a CI job or a recording.

### `TUIOS_SSH`

The ssh program to run instead of `ssh` on `PATH`: for the daemon's links to
the `[hosts]` machines (set it where the daemon starts), and for `--ssh` and
`tuios hosts test`.

### `TUIOS_HOST_RECONNECT_BUDGET`

How long a client attached to a session on another machine keeps dialing a
dropped link before it gives up, as a Go duration (`10m`). Three minutes when
unset. Read once, when the client starts.

### `TUIOS_CELL_SIZE`

The terminal's cell size in pixels, as `WIDTHxHEIGHT` (`10x20`), for a terminal
that does not answer the pixel geometry query. Images and captures drawn in
cells are sized from it. The SSH server reads the same variable.

### `TUIOS_KITTY_GRAPHICS`, `TUIOS_KITTY_PLACEHOLDERS`, `TUIOS_KITTY_ANIMATION`, `TUIOS_SIXEL_GRAPHICS`

`1` or `0` overrides what tuios detected about the host terminal's kitty
graphics, kitty Unicode placeholders, kitty animation and sixel support.

### `$SHELL`

TUIOS uses your default shell from this variable. If not set, it attempts to detect the appropriate shell for your platform.

### `COLORTERM`

For best color support, set this to `truecolor`:
```bash
export COLORTERM=truecolor
```

---

## When Something Goes Wrong

Every failure `tuios` reports answers three questions in order: what failed, the
most likely cause, and the exact command that fixes it. If you meet a message
that does not, it is a bug worth reporting.

### The daemon is not running

There are three distinct versions of this, and they have different fixes:

| Message says | What it means | Fix |
| --- | --- | --- |
| "is not running" | No socket exists. The daemon has never run, or was stopped. | `tuios new` |
| "a stale socket is left over at ..." | The daemon crashed without cleaning up. | `tuios kill-server`, which removes it |
| "Permission denied ... socket" | The socket belongs to another user, or its mode changed. | Check `ls -l` on the path; set `XDG_RUNTIME_DIR` to a directory you own |

### The daemon is older than the CLI

After upgrading TUIOS, the old daemon keeps running and serving the socket. It
cannot speak the control protocol the new CLI uses, so commands fail:

```
The running TUIOS daemon does not speak this CLI's control protocol (daemon 0.9.0, CLI 1.4.0).
Most likely cause: TUIOS was upgraded while the daemon kept running, so the old daemon is still serving the socket.
Fix: run 'tuios kill-server', then run this command again.
```

Run `tuios kill-server`. This ends every program running in your panes. Each
session's layout, window names and working directories are saved before the
daemon exits and come back when it next starts, each pane with a new shell;
`tuios resurrect` lists what is restorable.

### A session name is not found

The message lists the sessions that do exist and suggests the closest name:

```
Session "wrok" was not found.
Most likely cause: the name does not match any live session.
Did you mean "work"?
Sessions: notes, work.
Fix: run 'tuios ls' to list sessions, or 'tuios new wrok' to create this one.
```

A session killed with `tuios kill-session` is gone for good: killing is a
deliberate teardown, so its saved state is removed too. A session that merely
outlived its daemon is still restorable with `tuios resurrect`.

### A saved session will not restore

`tuios resurrect <name>` distinguishes three cases: no saved state at all, state
that is corrupt, and state written by a newer TUIOS. Unreadable state is moved
into an archive directory rather than deleted, and the message names that
directory so you can inspect or recover the file.

### The terminal cannot host the interface

`tuios attach`, `tuios new`, and `tuios resurrect` check the terminal before
taking over the screen, and refuse with an explanation when it is too small
(minimum 40x12), when `TERM` is unset or `dumb`, or when stdout is not a
terminal at all. For non-interactive use, drive a session with `tuios send-keys`
and `tuios capture-pane` instead of attaching.

### A session was killed while you were attached

The client exits with a non-zero status and says so, rather than leaving you in a
dead UI:

```
Session "work" was terminated while you were attached.
```

The same applies when the daemon itself goes away, which reports a lost
connection instead.

### Discovering the control protocol

`tuios list-verbs` prints every verb the daemon supports with its parameters,
accepted values, and runnable examples, plus the stable error codes. It is the
discovery entry point that the error hints point at, and `--json` makes it
machine-readable for an agent or a script.

```bash
tuios list-verbs                 # everything
tuios list-verbs capture-pane    # one verb
tuios list-verbs --json          # for scripting
```

---

## Exit Codes

- `0`: Success
- `1`: Error (configuration error, network error, file not found, etc.)
- `3`: `tuios ls` found no daemon running. It lists the sessions saved on disk
  instead, so a script can tell a stopped daemon from a running one with no
  sessions, which exits `0`

A `tuios attach` that ends because its session was killed, or because the daemon
was lost, exits `1`. A normal detach exits `0`.

---

## Version Information

The `--version` flag shows detailed build information:

```bash
tuios --version
```

**Output:**
```
tuios version v0.0.24 [pure-Go backend]
Commit: a1b2c3d
Built: 2025-01-15T10:30:00Z
By: goreleaser
```

The bracket names the terminal emulator backend the binary was built with.

---

## Command Migration Guide

If you're upgrading from an older version of TUIOS, here's how the commands have changed:

| Old Flag | New Command |
|----------|-------------|
| `--config-path` | `tuios config path` |
| `--edit-config` | `tuios config edit` |
| `--reset-config` | `tuios config reset` |
| `--list-keybinds` | `tuios keybinds list` |
| `--list-custom-keybinds` | `tuios keybinds list-custom` |
| `--ssh` | `tuios ssh` |
| `--ssh --host X --port Y` | `tuios ssh --host X --port Y` |
| `--version` | `tuios --version` |
| `--help` | `tuios --help` or `tuios help` |

---

## Related Documentation

- [Configuration Guide](CONFIGURATION.md): How to customize TUIOS
- [Keybindings Reference](KEYBINDINGS.md): Complete keyboard shortcut reference
- [Architecture Guide](ARCHITECTURE.md): Technical architecture details
- [README](../README.md): Project overview and quick start
