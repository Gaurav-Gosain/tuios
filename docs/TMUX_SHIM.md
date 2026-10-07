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

`tuios tmux-shim` needs a tuios pane (`TUIOS_SESSION` and `TUIOS_PANE_ID`).
The shim is not available on Windows.

## Tools outside tuios

A tool that runs outside tuios can drive tuios as a tmux server. Examples are
a phone bridge such as [Collie](https://github.com/AltanS/collie) or a script
in another terminal. Make a link named `tmux` to the tuios binary, and give
the shim's socket to `-S`. The socket is `tmux/socket` beside the daemon
socket:

```sh
mkdir -p ~/.local/lib/tuios-tmux
ln -sf "$(command -v tuios)" ~/.local/lib/tuios-tmux/tmux
~/.local/lib/tuios-tmux/tmux -S "$XDG_RUNTIME_DIR/tuios/tmux/socket" list-sessions
```

Without `XDG_RUNTIME_DIR`, the socket is `/tmp/tuios-UID/tmux/socket`. Do
not put the link on your PATH. A call without `-S` and without `TMUX` goes to
the real tmux.

For Collie, set `COLLIE_MUX=tmux`, `COLLIE_TMUX_BIN` to the link and
`COLLIE_MUX_ENDPOINT_TMUX` to the socket path. Collie then lists, reads,
types, sends keys, renames, focuses, and opens tabs and sessions. Its panes
show as shells, because tmux has no agent state.

A call with `-S` naming the shim's socket always goes to the shim, whatever
`TMUX` says. The shim never hands such a call to a real tmux. A real tmux
would start a server on that path.

Outside a pane, with `TUIOS_SESSION` unset, every tuios session is a tmux
session:

- A session's id is `$N`. N is a number derived from the tuios session id,
  so a rename does not change it.
- A window's id is `@N`, where N is the session's number times 1000 plus the
  workspace number. A tmux window id names one window on the whole server.
- `new-session -d` starts a tuios session.
- `list-clients` lists one client for each session a tuios client shows.

The shim grants nothing new here. A process outside every pane is the person,
and the tuios CLI already reaches every session for it.

## How tmux maps onto tuios

| tmux | tuios |
|------|-------|
| the server's one session | the tuios session the command runs in |
| every session, outside a pane | every tuios session |
| a window, `@N` | workspace N (outside a pane, see above) |
| a pane, `%N` | a tuios window. N is a number derived from the window id, so the same pane has the same id in every call |

A target can name a pane by `%N`, by the tuios window id (`%` followed by it,
or a prefix of it at least four characters long), or the tmux way:
`session:window.pane`, `@N`, `:N.M`, a workspace name, a session name or
`$N`. In a pane, nothing reaches another session. A target that names one
fails with `can't find session`, as it does on a tmux server that does not
hold it.

A command name can be shortened to any prefix that names one tmux command,
as in tmux: `show-option` is `show-options` and `list-pa` is `list-panes`. A
prefix of more than one command fails with tmux's error, for example
`ambiguous command: kill-se, could be: kill-server, kill-session`.

Format strings (`-F`, `display-message -p`) follow tmux 3.4's format
language:

- `#{name}`, the one-letter aliases (`#D #F #H #h #I #P #S #T #W`), and the
  escapes `##`, `#,` and `#}`.
- Conditionals: `#{?cond,then,else}`.
- Comparisons: `#{==:a,b}`, `#{!=:a,b}`, `#{<:a,b}`, `#{>:a,b}`,
  `#{<=:a,b}`, `#{>=:a,b}`, `#{||:a,b}` and `#{&&:a,b}`.
- Matches: `#{m:pattern,text}`, with `m/r` for a regular expression and `i`
  to ignore case.
- Modifiers: `l` (literal), `b` and `d` (base and directory name), `=N`,
  `=-N` and `=/N/marker` (truncate), `pN` and `p-N` (pad), `n` (length), `w`
  (width), `q` (quote for the shell), `E` and `T` (expand again), `t` and
  `t/p` (time), `a` (a character), and `s/pattern/with/flags` (substitute).
  Join several with `;`, for example `#{=10;s/x/y/:pane_title}`.

The loop modifiers (`S`, `W`, `P`, `L`), `N`, `C`, `c`, `e`, `q/e` and the
strftime form of `t` are not supported. They expand to nothing and are
logged. `#(command)` runs no command.

The variables: `session_name`, `session_id`,
`session_windows`, `session_attached`, `session_activity`, `session_created`,
`session_path`, `window_id`, `window_index`, `window_name`, `window_active`,
`window_panes`, `window_flags`, `window_width`, `window_height`,
`window_zoomed_flag`, `window_layout`, `window_visible_layout`,
`automatic-rename`, `pane_id`, `pane_index`, `pane_title`,
`pane_current_path`, `pane_current_command`, `pane_active`, `pane_width`,
`pane_height`, `pane_left`, `pane_top`, `pane_right`, `pane_bottom`,
`pane_at_left`, `pane_at_top`, `pane_at_right`, `pane_at_bottom`,
`pane_pid`, `pane_tty`, `pane_dead`, `pane_in_mode`, `pane_marked`,
`pane_synchronized`, `history_size`, `host`, `host_short`, `pid`, `version`,
`socket_path`, and `tuios_window_id`, the tuios id of the pane.
`list-clients` adds `client_name`, `client_session`, `client_control_mode`,
`client_activity`, `client_tty`, `client_width`, `client_height`,
`client_termname` and `client_flags`. `list-buffers` adds `buffer_name`,
`buffer_size`, `buffer_sample` and `buffer_created`. A variable the shim
cannot fill expands to nothing, as in tmux, and is logged.

Some values come from tuios facts:

- `session_path` is the directory of the session's first pane.
- `pane_current_command` is the program in the pane's foreground. At a shell
  prompt it is the base name of `$SHELL`.
- `history_size` is the number of scrollback lines above the screen. A daemon
  that does not report `history_rows` leaves it empty.
- `pane_pid` is the process the pane started, and `pane_tty` its terminal
  device, both from the daemon. A pane the shim opened runs its pane holder
  first, so its `pane_pid` is the holder's. A pane on another machine has
  neither.
- `pid` is the daemon's pid: the daemon is the shim's tmux server.
- `pane_at_left` and the others are 1 for a pane on that edge of the box
  around its workspace's panes. `window_zoomed_flag` is 1 when a pane of the
  workspace is zoomed, and `window_flags` then holds `Z`.
- `window_layout` is a tmux layout string. A tiled workspace becomes the
  split tree tmux would hold: columns where a vertical line crosses no pane,
  else rows. Panes that no straight line separates, such as overlapping
  floating windows, are listed side by side in one container at their own
  positions and sizes. tmux cannot describe that layout exactly.
  `select-layout` with such a string changes nothing.
- `automatic-rename` is 1 for a workspace with no name of its own. Such a
  workspace takes the name of its active pane.
- `client_tty` is empty. tuios does not name its clients' terminals.

## Commands

| Command | What the shim does |
|---------|--------------------|
| `split-window` | Opens a pane on the target pane's workspace (`new-window` verb). `-d` leaves the focus where it is, `-c` sets the directory, `-e` the environment, `-P -F` prints the new pane. Where the pane goes is the tuios layout's answer: `-h`, `-v`, `-b`, `-f`, `-l` and `-p` are accepted and do not change it |
| `new-window` | Opens a pane on the lowest empty workspace, or the one `-t` names (it must be empty). `-n` names the workspace |
| `send-keys` | Types into a pane (`send-text`). Key names (`Enter`, `C-c`, `M-x`, `Up`, `F1`, `BSpace`...) become the bytes a terminal sends; anything else is typed as text. `-l` types every argument as text, `-H` takes hex bytes, `-N` repeats |
| `capture-pane -p` | Prints a pane (`capture-pane`). `-S` and `-E` take tmux line numbers (0 is the top of the screen, negative is history, `-` is either end), `-e` keeps the colours. Without `-p` it fails |
| `display-message` | With `-p`, prints a format for the target pane. Without `-p` there is no status line to show it on, so it does nothing |
| `list-panes`, `list-windows`, `list-sessions` | List the panes of a workspace (`-s`: of the session, `-a`: of every session), the workspaces that hold panes (`-a`: in every session), and the sessions |
| `list-clients` | Lists one client for each session that a tuios client shows |
| `detach-client` | Detaches tuios clients (`detach-client` verb). See below |
| `has-session` | Succeeds for a session the shim serves, fails for any other |
| `new-session -d` | Outside a pane, starts a tuios session (`new-session` verb). `-s` names it, `-n` names its first window, `-c` sets the directory, `-P -F` prints the new pane. Without `-d` it fails, because the shim attaches no terminal. In a pane it is refused |
| `load-buffer`, `set-buffer` | Store text in a paste buffer: from a file, from standard input (`-`), or from the argument. `-b` names the buffer, `set-buffer -a` appends |
| `paste-buffer` | Types a buffer into a pane as a paste (`send-text` with `paste`). See below |
| `delete-buffer` | Deletes a buffer |
| `show-options`, `show-window-options` | Print the options that describe tuios, such as `window-size`, `base-index 1` and `history-limit`. `window-size` is the session's `daemon.window_size` (`smallest`, `largest` or `latest`), or `latest` from a daemon that does not report it. `-v` prints the value alone, `-q` hides the error for an unknown option |
| `set-option window-size`, `set-window-option window-size` | Set the session's `daemon.window_size` to `smallest`, `largest` or `latest`. `manual` is refused. See [SESSIONS.md](SESSIONS.md#session-size-with-more-than-one-client) |
| `kill-pane`, `kill-window` | Close a pane, or every pane of a workspace (`close-window`) |
| `select-pane` | Focuses a pane (`focus-window`), or with `-L -R -U -D` the target's neighbour. The neighbour is found from the panes' positions, as tmux finds it, so a session with no client attached answers too. `-T` names the pane (`set-window`) and leaves the focus alone. `-P` (a style) is ignored |
| `last-pane` | Focuses the pane that was active in the window before the current one, from the workspace's focus history. `no last pane` when there is none |
| `select-window`, `rename-window` | Show a workspace, name a workspace |
| `next-window`, `previous-window` | Show the next or previous workspace that holds panes, wrapping around (`select-workspace`) |
| `break-pane` | Moves a pane to the lowest empty workspace, or the empty one `-t` names (`move-window`). `-d` keeps the view where it is, `-n` names the workspace, `-P -F` prints the pane |
| `join-pane`, `move-pane` | Move the `-s` pane to the workspace of the `-t` pane (`move-window`). Where it lands is the tuios layout's answer: `-b`, `-f`, `-h`, `-v` and `-l` do not change it |
| `swap-pane` | Refused. tuios has no verb that exchanges two panes' places |
| `rename-session` | Renames a session (`rename-session` verb) |
| `respawn-pane -k` | Replaces the process of a pane the shim opened, keeping the pane and its id. See below |
| `display-popup` | Opens a tuios popup that runs the command and returns when it exits (`popup` verb). See below |
| `run-shell`, `if-shell` | Run a shell command as the caller. See below |
| `wait-for` | Waits on a channel, signals it (`-S`), or locks (`-L`) and unlocks (`-U`) it. See below |
| `show-buffer`, `save-buffer`, `list-buffers` | Print a buffer, write it to a file (`-` is standard output, `-a` appends), list the buffers newest first |
| `show-environment`, `set-environment` | See below |
| `-V` | Prints `tmux 3.4` |

`set-option` and `set-window-option` of any option except `window-size`,
`set-hook`, `refresh-client`, `select-layout`, `resize-pane` and
`start-server` succeed and do nothing.
tuios owns the layout, the styling and the options. `kill-session`,
`kill-server`, `attach-session` (outside control mode) and `switch-client`
are refused. The shim never attaches a terminal or ends a session. Every other command fails with `unknown command`. Every flag not
listed for a command fails with `unknown flag`. The shim does not accept a
flag and then ignore it.

`show-options history-limit` is the session's `appearance.scrollback_lines`,
or 10000, the shipped default, when nothing set it.

### detach-client

`detach-client` detaches tuios clients through the `detach-client` verb. The
session continues to run, and each detached client exits with a message.
`list-clients` names one client for each session, `tuios-SESSION`.

- `-s SESSION` detaches every client of that session. As in tmux 3.7c,
  `-s` wins: `-a` and `-t` are ignored with it.
- `-t tuios-SESSION` detaches every client of that session.
- With neither, the client used last in the caller's session detaches, as
  tmux detaches the current client.
- `-a` keeps the client used last in the session and detaches the others.
  In tmux, `-a` acts on every client of the server. In the shim, `-a` acts
  on one session only: the session of `-t`, else the caller's session.
- `-P` and `-E` fail with `unknown flag`.

A session out of the shim's reach is not found. From a pane, the command
needs the `admin` grant. Under `[agents.permissions]` mode `open`, the
default, every pane holds `admin`. A tool in a pane can then detach the
person's client through the shim, as a tool under tmux can. To prevent this,
give the pane fewer grants or use mode `strict`.

In control mode, `detach-client` with no flags ends
the control client, as in tmux. With flags, it detaches tuios clients as
above. A read-only control client cannot detach tuios clients.

`attach-session -d` in control mode attaches the control client and
detaches no tuios client. A control client is not a tuios client, and an
app that attaches with `-d` must not take the screen from the person. To
detach the other clients when you attach, use `tuios attach -d`.

### display-popup

`display-popup` opens a tuios popup on the target pane's workspace and
returns when the popup's command exits, as tmux does. This is what
`fzf --tmux` needs: fzf sees `TMUX`, runs itself in the popup, and reads the
choice when the `tmux` call returns. One argument is a shell command line,
several are an argv, and none is your shell. `-d` sets the directory, `-w`
and `-h` the size (cells, or a percentage such as `80%`), `-T` the name, and
`-e` the environment.

The popup needs a tuios client attached to the session, as a tmux popup
needs a client to draw on. A tuios popup always closes when its command
exits. tuios places it and draws its border, so `-x`, `-y`, `-b`, `-B`, `-s`
and `-S` are accepted and logged. `-C` is refused.

### run-shell and if-shell

`run-shell` runs a shell command with `/bin/sh` and prints its output.
`-C` runs a tmux command line instead. `-b` starts the command and returns
at once. `-d` waits that many seconds first. A command that fails prints
`'command' returned N` and the shim exits with N, as tmux does.

`if-shell` runs its first tmux command when the shell command succeeds, or
with `-F` when the format is true, and the second, if given, when it does
not. The shim waits for the shell command even with `-b`, since it has no
server to finish the job after it exits. This is logged.

tmux runs these commands in its server. The shim runs them itself, as you,
from the process that called it, in its directory and environment. So they
reach nothing you could not reach by running the command yourself, and
they do not go through the daemon. A read-only control client cannot run
them. Formats in the command are expanded first, as in tmux. A value that a
program in a pane sets, such as a title, lands in the command as it is.
Quote such a value with `#{q:...}`.

tmux shows the output of `run-shell` in the pane's view mode. The shim has
none, so it prints the output where tmux prints it when there is no pane to
show it in.

### wait-for

`wait-for CHANNEL` blocks until another call signals the channel with
`wait-for -S CHANNEL`. A signal sent while nobody waits is kept for the next
wait, as in tmux. `wait-for -L CHANNEL` takes the channel's lock and blocks
while another call holds it. `wait-for -U CHANNEL` hands the lock to the
call that asked first, or frees it. A channel that is not locked fails with
`channel NAME not locked`.

A channel is a directory in `tmux/w/` beside the daemon socket, which only
you can open. The calls meet there, since each `tmux` call is a process of
its own.

### Environment

`set-environment NAME VALUE` sets a variable for the panes the shim opens in
the session (`-g`: in every session). `-r` marks it to be removed from new
panes, `-u` forgets it, `-h` hides it from `show-environment` without `-h`,
and `-F` expands the value as a format. The panes that `split-window`,
`new-window`, `respawn-pane`, `new-session` and `display-popup` start get
the global variables, then the session's, then the ones `-e` gives.

`show-environment` prints one variable or all of them: `NAME=value`,
`-NAME` for a removed one, or with `-s` as shell commands. The global
environment is your environment with the variables set on top. A session's
environment holds only what was set on it: tuios copies nothing into it when
a client attaches, so tmux's `update-environment` has no counterpart.

The variables are kept in `tmux/env/` beside the daemon socket, readable
only by you.

A line may hold several commands separated by `;` (`tmux a \; b`).

### Paste buffers

The daemon keeps the paste buffers, as the tmux server does. They are the
same buffers that a yank in copy mode adds and that `tuios list-buffers`
shows. So `tmux paste-buffer` pastes your last yank, and a buffer stays
between two calls, as in tmux: `tmux load-buffer notes.txt` and then
`tmux paste-buffer`. Without `-b`, `paste-buffer` and `delete-buffer` use the
newest buffer that `tmux` named. A new buffer without `-b` is named `buffer0`, `buffer1`
and so on. The daemon holds the caller to its pane grants: reading the
buffers needs `read`, and changing them needs `write`.

A daemon from before the paste buffers has no buffers of its own. With such a
daemon, a buffer is a file in `tmux/buffers/` beside the daemon socket, which
only you can read.

`paste-buffer` types the buffer into the target pane:

- Each line feed becomes a carriage return, as in tmux. `-s` sets another
  separator. `-r` keeps the line feeds.
- Control characters other than tab, line feed and carriage return are
  removed, as from every tuios paste. So a buffer cannot end a bracketed
  paste early.
- With `-p`, the text is wrapped in the bracketed paste delimiters when the
  program in the pane turned bracketed paste on.
- `-d` deletes the buffer after the paste.

The paste goes through `send-text`, so the daemon holds it to the caller's
pane grants. A pane without `write` cannot paste into another pane. When the
daemon refuses the paste, nothing is typed and the buffer stays.

`save-buffer` writes the file as you, from the process that called it, so it
reaches only files you could write yourself. A relative path is taken from
the caller's directory. A new file is readable only by you. A path in the
shim's own directory (`tmux/` beside the daemon socket) is refused, so a
buffer cannot overwrite the shim's state.

### Control mode

`tmux -C` and `tmux -CC` start a control client, as in tmux. The client reads
commands from standard input, one per line. It attaches to the session that
`attach-session -t` names. With no command, it attaches to the caller's
session. `new-session` in control mode starts a session and attaches to it.

Each command's output comes between `%begin` and `%end`, or `%error` when the
command fails. The three fields are the time, the command number and the
flags. The flags are 1 for a command from standard input and 0 for a command
on the command line. A read-only client (`attach-session -r` or
`-f read-only`) can run only commands that change nothing.
`attach-session -f no-output` stops the `%output` lines. `-CC` wraps the
output in the DCS sequence that iTerm2 expects.

A command with no target acts on the session the client attached to, and on
that session's active pane, as a command from an attached tmux client does.
The session that the person's tuios client shows does not change this.

The client sends these notifications. The shim reads them from the daemon's
event stream (`subscribe`) and reads the sessions again every 2 seconds:

| Notification | When |
|--------------|------|
| `%session-changed $N name` | The client attaches |
| `%output %N ` | A pane of the session prints. The line carries no bytes. Read the pane with `capture-pane` |
| `%window-add @N`, `%window-close @N` | A workspace gets its first pane, or loses its last |
| `%window-renamed @N name` | A workspace's name changes |
| `%layout-change @N layout layout flags` | A workspace gets or loses a pane, or a pane moves or changes size |
| `%window-pane-changed @N %M` | The active pane of a workspace changes |
| `%session-window-changed $N @M` | The session shows another workspace |
| `%unlinked-window-add`, `-close`, `-renamed` | The same, in another session the shim serves |
| `%sessions-changed` | A session starts or ends, outside a pane |
| `%session-renamed $N name` | The session gets a new name |
| `%exit` | Standard input closes, `detach-client` with no flags runs, the session ends, or the daemon stops |

The shim does not send these notifications: `%pause`, `%continue`,
`%extended-output`, `%subscription-changed`, `%pane-mode-changed`,
`%client-session-changed`, `%client-detached`, `%paste-buffer-changed`,
`%paste-buffer-deleted`, `%message` and `%config-error`. It does not support
flow control (`refresh-client -A`, `-f pause-after`) or format subscriptions
(`refresh-client -B`). `refresh-client -C` succeeds and changes nothing,
because a tuios client sets the size. `%output` carries no bytes, because
the daemon's event stream says that a pane printed, not what it printed.
So a client that draws panes from `%output`, such as iTerm2 with `-CC`,
shows each pane as it was when it last read it with `capture-pane`.

#### What real `%output` needs

This is the plan for `%output` with the pane's bytes. It is not built yet.

- The daemon gets a verb that streams one pane's output on a connection of
  its own: each chunk as a JSON line with its stream position and the bytes
  in base64. It subscribes to the pane's PTY as an attached client does
  (`PTY.Subscribe`), with pacing off, so a slow control client never holds
  the pane's program back. It is a read, held to the same grants as
  `capture-pane`, and it needs entries in the connection scope table, the
  link policy and `list-verbs`.
- When the subscriber falls behind and the PTY marks a gap, the verb sends a
  gap line and starts again from the ring's tail. The graphics frames the
  PTY can drop for a newer one are resolved as `streamPTYOutput` resolves
  them for a client.
- The shim's control loop opens one stream for each pane of the attached
  session, and opens or closes streams when a refresh sees panes come and
  go. It writes `%output %N` with the bytes escaped as tmux escapes them:
  each byte below a space, and the backslash, as a backslash and three
  octal digits. A line goes out between command blocks, never inside a
  `%begin` and `%end` pair.
- After a gap, the client's copy of the pane is wrong. tmux has no
  notification that says so. With `refresh-client -f pause-after` the shim
  can send `%pause` and `%continue`, after which the client reads the pane
  again. Without it, which notification makes iTerm2 read the pane again
  must be checked against iTerm2.

It was not built with the rest of the shim's commands because it adds a
second consumer of the daemon's output path, whose pacing, gap and frame
logic is tied to the client connection, and because iTerm2 is not
available here to check the result end to end.

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
{"time":"2026-09-23T16:19:27Z","argv":["tmux","bind-key","<2 redacted>"],"outcome":"unsupported","detail":["unknown command: bind-key"]}
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
dial with the tuios CLI, and calls verbs the CLI already has: `list-sessions`,
`session-info`, `list-windows`, `list-workspaces`, `new-window`,
`new-session`, `send-text`, `capture-pane`, `close-window`, `set-window`,
`focus-window`, `move-window`, `select-workspace`, `set-workspace-name`,
`rename-session`, `popup`, `get-option`, `hello` and `subscribe`. In a pane,
it adds confinement: every call names the caller's own session, and every
target is resolved inside it. So a stray `-t` cannot touch another session.

`run-shell`, `if-shell`, `save-buffer` and `wait-for` do not go through the
daemon. They run in the process that called the shim, as you, and reach what
that process could reach without the shim.

The holder takes respawn requests on a unix socket in `tmux/p/`, beside the
daemon socket. The shim creates that directory owned by you and mode 0700,
refuses one that belongs to someone else or is a link, and closes one open to
others, so only your own processes can reach a holder: the same processes
that could type into the pane with `tuios send-text`. A request names the
window it is for, and a holder refuses one for any other window, so two panes
whose numbers collide cannot respawn each other.

Every verb the shim calls is held to the caller's
[pane grants](AGENT_STATE.md#what-a-pane-may-do) by the daemon, as for the
CLI. `new-window`, `close-window`, `focus-window`, `set-window`,
`select-workspace` and `set-workspace-name` need `admin`, so from a pane
without `admin` `split-window`, `new-window`, `kill-pane`, `kill-window`,
`select-pane`, `select-window` and `rename-window` are refused with
`forbidden`. A respawn does not go through the daemon, so the
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

## With the agent features off

With `[agents] enabled = false`, tuios does not track which pane waits on a
prompt. A pane without the `respond` grant can then type only into these
panes:

- A pane that it opened. Thus `split-window` and then `send-keys` into the
  new pane work, as in an agent team.
- A pane whose own shell is at its prompt.

The shim refuses `send-keys` into any other pane, with `forbidden`. To let
every pane type into other panes, give panes `respond`:

```toml
[agents.permissions]
grants = ["admin", "respond"]
```

See [Turn off agent features](CONFIGURATION.md#turn-off-agent-features).
