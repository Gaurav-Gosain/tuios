# tuios-slim

tuios-slim is a smaller build of tuios. It keeps the terminal multiplexer and
leaves out the agent, host, SSH and automation features. The full `tuios`
binary keeps every feature. tuios-slim is an additional build, not a
replacement.

| Binary | linux/amd64 | darwin/arm64 |
|---|---|---|
| tuios | 28,057,762 bytes (26.8 MiB) | 26,485,522 bytes (25.3 MiB) |
| tuios-slim | 17,174,690 bytes (16.4 MiB) | 16,349,410 bytes (15.6 MiB) |

The sizes are for release builds (`-trimpath -ldflags "-s -w"`, Go 1.26.6).
`scripts/binary-size.sh` measures both binaries and holds each to a budget.

tuios-slim is 39% smaller than tuios on linux/amd64. The table below says
where the bytes went, measured on linux/amd64.

| Left out | Bytes saved | How it was measured |
|---|---|---|
| The daemon's agent, host, worktree, review, herdr and screenshot code | 2,613,248 | slim before and after the cut |
| The SSH server | 1,253,376 | full build without the command |
| The review overlay, the diff view and its syntax highlighter | 1,212,416 | slim before and after the cut |
| The screen saver, the effect picker and the effects engine | 978,944 | slim before and after the cut |
| The agent commands | 978,944 | full build without the commands |
| The Inbox, mail, alerts and their sounds | 700,416 | slim before and after the cut |
| The worktree, fan and review commands | 196,608 | full build without the commands |
| The hosts settings and the SSH session type | 172,032 | slim before and after the cut |
| The herdr command line | 167,936 | full build without it |
| The tape manager, recording and project tapes | 139,264 | slim before and after the cut |
| The tmux shim | 131,072 | full build without it |
| The agent skill documents | 126,976 | full build without them |
| `tuios update` | 122,880 | full build without the command |
| The screenshot panel and capture mode | 110,592 | slim before and after the cut |
| The hosts commands | 102,400 | full build without the commands |
| The MCP server | 77,824 | full build without the command |
| The tape command | 45,056 | full build without the command |

The rows were measured on linux/amd64 with Go 1.26.6, on the main branch of
2026-10-02. "Full build without the command" is what one feature costs on its
own.
"Slim before and after the cut" is what the cut saved at that point of the
work. Features share code, so the rows do not add up to the total.

## Get it

Each release has `tuios-slim_*` archives for the same platforms as `tuios`.
The binary in them is `tuios-slim`.

To build it from source, use the `slim` build tag:

```bash
go build -tags slim -o tuios-slim ./cmd/tuios
```

`tuios-slim --version` prints `slim` after the version number.

## What tuios-slim has

- The daemon, with sessions that survive a detach, attach from many clients,
  and session restore after a restart.
- Panes, workspaces and every tiling layout: BSP, master and scrolling.
- The keybindings, the prefix key and its menus.
- The mouse.
- Copy mode, the scrollback and the scrollback browser.
- The rail, the dock and its components.
- The command palette, the launcher, settings and the keybinding editor.
- The config file and every theme.
- Kitty graphics and sixel images in panes.
- Hooks, popups, picture-in-picture, xpanes and pane grants.
- The CLI commands that drive a session: `attach`, `new`, `ls`,
  `kill-session`, `kill-server`, `send-keys`, `send-text`, `capture-pane`,
  `split-window`, `new-window`, `run-command`, `set-config` and the other
  window, workspace and layout commands.

Some features stay in tuios-slim on purpose:

- Kitty graphics and sixel cost about 170 KB of code. Users expect images to
  work in a terminal, so tuios-slim keeps them.
- Every theme stays. The theme table is about 22 KB.
- Pane grants stay. They limit what a program in a pane can do through tuios,
  so a strict config must not lose them.
- The prompt state stays. A program that sends the OSC 9;4 warning state
  waits on a prompt. Another pane must hold the respond grant to type into
  it. Any other OSC 9;4 report ends the prompt state. tuios-slim reads this
  state on every pane. The full tuios reads it only on a pane that it knows
  runs an agent.

## What tuios-slim leaves out

| Feature | Commands | What tuios-slim does instead |
|---|---|---|
| The SSH server | `ssh` | Prints the line below. |
| The MCP server | `mcp` | Prints the line below. |
| Agents: agent state, the Inbox, mail, approvals, recaps, alerts, agent detection, harness integrations, start-agent, ACP and Codex | `set-agent-state`, `list-agents`, `send-agent-message`, `ask-agent`, `respond`, `queue`, `agent-hook`, `integration`, `start-agent` and the other agent commands | Prints the line below. The daemon ignores agent reports from panes, except the prompt state. |
| Worktrees, fan-out and the review | `worktree`, `fan`, `review` | Prints the line below. |
| Hosts and links to other machines | `hosts`, `stdio-proxy` | Prints the line below. The `--host`, `--all-hosts` and `--global` flags print `<flag> is not in tuios-slim. Install the full tuios to use it.` |
| The herdr API and its command line | `pane`, `notification` | Prints the line below. |
| The tmux shim | `tmux`, `tmux-shim`, `tmux-pane` | Prints the line below. |
| Tape files: playback, recording, the tape manager and project tapes | `tape` | Prints the line below. `run-command` still runs single tape commands. |
| Screenshots and capture mode | `screenshot` | The screenshot key shows a message. |
| The screen saver and the effect picker | | The `[screensaver]` section loads and does nothing. |
| `tuios update` | `update` | Prints the line below. |
| The agent skill | `--skill` | Prints `--skill is not in tuios-slim. Install the full tuios to use it.` |
| Event streams and shell runs | `subscribe`, `run` | Prints the line below. |

A command that tuios-slim leaves out prints one line and exits with status 1:

```
ssh is not in tuios-slim. Install the full tuios to use this command.
```

The palette, the settings page, the help and the prefix menu do not show the
features that tuios-slim leaves out. The rail shows no agent rows. The prefix
keys of the Inbox (`i`, `M` and `o`) and of screenshots (`C`) show a message
that the feature is not in tuios-slim.

## Extensions

tuios-slim looks for an extension before it prints that line. An extension is
an executable named `tuios-<command>`, for example `tuios-ssh`. tuios-slim
looks in `$XDG_DATA_HOME/tuios/ext` first, then on `PATH`.

Before it runs an extension, tuios-slim runs it with the one argument
`--tuios-extension-info`. The extension prints a JSON object with its protocol
version, its command name and the tuios version it was built from. tuios-slim
refuses an extension built from another version and says so:

```
.../tuios-ssh is built for tuios v0.8.4, and this tuios is v0.8.5. Update both to the same version.
```

tuios-slim then runs the extension with the arguments after the command name,
the same terminal, and its own environment. It adds `TUIOS_SOCKET`, the daemon
socket, and `TUIOS_EXT_PROTOCOL`, `TUIOS_EXT_HOST_VERSION` and
`TUIOS_EXT_HOST`. It exits with the status of the extension.
`internal/extension` holds the protocol.

No extension ships yet.

## Mixing tuios and tuios-slim

A tuios-slim client can attach to a tuios daemon, and a tuios client can
attach to a tuios-slim daemon.

- The welcome message carries the daemon's edition. A slim daemon sends
  `slim`. A full daemon sends nothing, as older daemons do.
- A slim daemon answers a verb it does not have with the error code
  `unknown_verb` and the message `<verb> is not in tuios-slim. Install the full
  tuios to use it.`
- A slim daemon ignores agent reports from panes: herdr environment variables,
  `set-agent-state` from a full tuios, and every OSC sequence except the
  OSC 9;4 prompt state.
- A slim client drops the agent mail a full daemon sends.
- A slim daemon refuses `new --global` from a full tuios, as the slim CLI does.
- A full client on a slim daemon says the Inbox is not in tuios-slim.

## Config

tuios-slim reads the same config file as tuios. The sections for features it
leaves out load without an error and have no effect. One config file works for
both binaries.
