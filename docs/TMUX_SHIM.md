# The tmux shim

Some tools drive tmux to open panes for their own workers. Claude Code agent
teams is the main one: with teammates in split panes, it opens one tmux pane
per teammate, starts the teammate in it, and closes it when the teammate is
done. Inside tuios there is no tmux, so those teammates cannot get panes.

`tuios tmux-shim` fixes that for one command. The command runs with a `tmux`
on its PATH that answers in the tuios session you ran it from, so each
teammate opens as a tuios pane: on the rail, in the Inbox, with its agent
state, beside the pane that started it.

The shim is off until you run it, and it changes nothing outside the command
it starts.

## Claude Code agent teams

In a tuios pane:

```sh
tuios tmux-shim -- env CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 claude
```

Claude Code sees `TMUX` set, picks its tmux backend, and every teammate it
spawns opens as a pane on the same workspace, named after the teammate. The
panes close when the teammates finish.

Flags after the command are the command's: `tuios tmux-shim claude --resume`
passes `--resume` to `claude`. With no command, the shim starts your shell,
and every `tmux` call from that shell goes to the shim.

## What it does

`tuios tmux-shim [--log FILE] [--log-all] [-- command [args...]]`:

- puts a link named `tmux`, pointing at the tuios binary, first on PATH. The
  link lives in `tmux/bin/` beside the daemon socket
  (`$XDG_RUNTIME_DIR/tuios/tmux/bin`, or `/tmp/tuios-UID/tmux/bin`);
- sets `TMUX` to name the shim as the tmux server, and `TMUX_PANE` to the
  pane you ran it in;
- runs the command with that environment.

When the tuios binary runs under the name `tmux`, it checks whom the call is
for. A call whose `TMUX` names the shim is answered by the shim. Any other call
(`TMUX` unset or naming a real tmux server, or `-L` or `-S` naming another
server) goes to the next `tmux` on PATH, so a real tmux keeps working inside
the command.

`tuios tmux <tmux arguments>` is the shim asked for by name, from any tuios
pane, with no launcher: `tuios tmux list-panes -F '#{pane_id} #{pane_title}'`.

It needs a tuios pane (`TUIOS_SESSION` and `TUIOS_PANE_ID`), and it is not
available on Windows.

## How tmux maps onto tuios

| tmux | tuios |
|------|-------|
| the server's one session | the tuios session the command runs in |
| a window, `@N` | workspace N |
| a pane, `%N` | a tuios window. N is a number derived from the window id, so the same pane has the same id in every call |

A target can name a pane by `%N`, by the tuios window id (`%` followed by it,
or a prefix of it at least four characters long), or the tmux way:
`session:window.pane`, `@N`, `:N.M`, a workspace name. Nothing reaches another
session: a target naming one fails with `can't find session`, as it does on a
tmux server that does not hold it.

Format strings (`-F`, `display-message -p`) support `#{name}`, the one-letter
aliases (`#D #F #H #h #I #P #S #T #W`), `##`, `#{?cond,then,else}`,
`#{==:a,b}` and `#{!=:a,b}`. The variables: `session_name`, `session_id`,
`session_windows`, `session_attached`, `window_id`, `window_index`,
`window_name`, `window_active`, `window_panes`, `window_flags`,
`window_width`, `window_height`, `pane_id`, `pane_index`, `pane_title`,
`pane_current_path`, `pane_active`, `pane_width`, `pane_height`, `pane_left`,
`pane_top`, `pane_right`, `pane_bottom`, `pane_dead`, `pane_in_mode`,
`pane_marked`, `pane_synchronized`, `host`, `host_short`, `pid`, `version`,
`socket_path`, and `tuios_window_id`, the tuios id of the pane. A variable the
shim cannot fill expands to nothing, as in tmux, and is logged.

## Commands

| Command | What the shim does |
|---------|--------------------|
| `split-window` | Opens a pane on the target pane's workspace (`new-window` verb). `-d` leaves the focus where it is, `-c` sets the directory, `-e` the environment, `-P -F` prints the new pane. Where the pane goes is the tuios layout's answer: `-h`, `-v`, `-b`, `-f`, `-l` and `-p` are accepted and do not change it |
| `new-window` | Opens a pane on the lowest empty workspace, or the one `-t` names (it must be empty). `-n` names the workspace |
| `send-keys` | Types into a pane (`send-text`). Key names (`Enter`, `C-c`, `M-x`, `Up`, `F1`, `BSpace`...) become the bytes a terminal sends; anything else is typed as text. `-l` types every argument as text, `-H` takes hex bytes, `-N` repeats |
| `capture-pane -p` | Prints a pane (`capture-pane`). `-S` and `-E` take tmux line numbers (0 is the top of the screen, negative is history, `-` is either end), `-e` keeps the colours. Without `-p` it fails: the shim keeps no paste buffers |
| `display-message` | With `-p`, prints a format for the target pane. Without `-p` there is no status line to show it on, so it does nothing |
| `list-panes`, `list-windows`, `list-sessions` | List the panes of a workspace (`-s` or `-a`: of the session), the workspaces that hold panes, and the session |
| `has-session` | Succeeds for the caller's session, fails for any other |
| `kill-pane`, `kill-window` | Close a pane, or every pane of a workspace (`close-window`) |
| `select-pane` | Focuses a pane (`focus-window`), or with `-L -R -U -D` its neighbour. `-T` names the pane (`set-window`) and leaves the focus alone. `-P` (a style) is ignored |
| `select-window`, `rename-window` | Show a workspace, name a workspace |
| `respawn-pane -k` | Replaces the process of a pane the shim opened, keeping the pane and its id. See below |
| `-V` | Prints `tmux 3.4` |

`set-option`, `set-window-option`, `set-hook`, `refresh-client`,
`select-layout`, `resize-pane` and `start-server` succeed and do nothing:
tuios owns the layout, the styling and the options. `kill-session`,
`kill-server`, `new-session`, `attach-session`, `switch-client` and
`detach-client` are refused: the shim never starts, attaches or ends a
session. Every other command fails with `unknown command`, and every flag not
listed for a command fails with `unknown flag`, rather than being accepted and
not honoured.

A line may hold several commands separated by `;` (`tmux a \; b`).

### respawn-pane and the pane holder

Claude Code opens each teammate's pane running `cat` as a placeholder and then
replaces it with `respawn-pane -k` and the teammate's command. A tuios window's
process cannot be swapped from outside it, so every pane the shim opens runs
`tuios tmux-pane`, a small holder. It runs the pane's command as its child, in
a process group of its own that holds the terminal's foreground, as a shell's
job does. So ctrl+c reaches the command, and agent detection reads the
command, not the holder: a teammate shows on the rail as the agent it is. On
`respawn-pane` the holder ends the child's process group (SIGHUP, then SIGKILL
after two seconds) and starts the new command in its place. When the running
command exits, the holder exits with its status and the pane closes.

The pane's processes see `TMUX` and `TMUX_PANE` naming the shim and the pane,
as they would in tmux, so a tool that calls `tmux` from a pane it opened
reaches the shim too.

`respawn-pane` works only on panes the shim opened. On any other pane it fails
and says the pane has no holder.

## The log

Every call the shim could not fully answer is recorded as one JSON line in
`$XDG_STATE_HOME/tuios/tmux-shim.log` (or the file `--log` names): an unknown
command, a flag it does not take, a format variable it could not fill, a
refused command. That file is the list of what to add next. `--log-all`
records every call.

```json
{"time":"2026-09-23T16:19:27Z","argv":["tmux","wait-for","<1 redacted>"],"outcome":"unsupported","detail":["unknown command: wait-for"]}
```

`outcome` is `ok`, `ignored` (a command that does nothing here), `partial`
(it succeeded, and `detail` says what was not honoured), `unsupported` or
`error`. The log never records what was typed or run. It keeps the global
flags, the name of a known tmux command and the flags of a command the shim
parses, and replaces the rest with a marker or a count:

- the text arguments of `send-keys`, `split-window`, `new-window` and
  `respawn-pane`, and every `VAR=value`;
- the arguments of every command the shim does not answer, including the
  ignored and refused ones;
- a word in command position that is not a known tmux command, logged as
  `<unknown command>` (a word ending in `;` ends a command, so text can land
  there). The error on stderr still names it; the log does not;
- every word that is not a flag, when the global flags do not parse (for
  example `tmux -c 'shell command'`). The file is created mode 0600 and is
moved to `tmux-shim.log.1` past 1 MiB.

## What it can reach

The shim grants nothing. It runs as you, dials the daemon socket you could
dial with the tuios CLI, and calls verbs the CLI already has: `list-windows`,
`list-workspaces`, `new-window`, `send-text`, `capture-pane`, `close-window`,
`set-window`, `focus-window`, `select-workspace` and `set-workspace-name`.
What it adds is confinement: every call names the caller's own session, and
every target is resolved inside it, so a stray `-t` cannot touch another
session.

The holder takes respawn requests on a unix socket in `tmux/p/`, beside the
daemon socket. The shim creates that directory owned by you and mode 0700,
refuses one that belongs to someone else or is a link, and closes one open to
others, so only your own processes can reach a holder: the same processes
that could type into the pane with `tuios send-text`. A request names the
window it is for, and a holder refuses one for any other window, so two panes
whose numbers collide cannot respawn each other.

Every verb the shim calls is held to the caller's
[pane grants](AGENT_STATE.md#what-a-pane-may-do) by the daemon, as for the
CLI. `new-window` and `close-window` need `admin`, so from a pane without
`admin` `split-window`, `new-window`, `kill-pane` and `kill-window` are
refused with `forbidden`. A respawn does not go through the daemon, so the
shim holds it to the same rule itself: it asks the daemon what the caller
holds (`pane-grants`), and from a pane without `admin` it respawns only the
caller's own pane. A caller in no pane, and a daemon from before pane grants,
respawn any pane the shim opened, as before; if `pane-grants` fails any other
way, the respawn is refused.

It is not a sandbox. A process under the shim can still run the tuios CLI and
reach what its pane's grants allow, and a process that writes to a holder's
socket itself, rather than through the shim, is not held to them, like any
process that leaves its pane on purpose. For an agent held to its own session,
use `tuios mcp`, which restricts its connections (see
[protocol.md](protocol.md#restrict-connection)), or give its pane fewer grants.
