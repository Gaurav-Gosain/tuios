# herdr's command line and socket

herdr is another multiplexer for coding agents. Tools built for herdr work in a
tuios pane, because every pane has herdr's environment:

| Variable | Value |
| --- | --- |
| `HERDR_SOCKET_PATH` | tuios's herdr socket, which answers herdr's socket API |
| `HERDR_PANE_ID` | this pane, `w<session>:p<window>` |
| `HERDR_TAB_ID` | this pane's workspace, `w<session>:t<number>` |
| `HERDR_WORKSPACE_ID` | this pane's session, `w<session>` |
| `HERDR_BIN_PATH` | a link named `herdr` to tuios, which answers herdr's command line |

A herdr workspace is a tuios session, a herdr tab is a tuios workspace, and a
herdr pane is a tuios window. tuios sets `HERDR_TAB_ID` when the pane's shell
starts. A pane moved to another workspace keeps the id it started with.

## The command line

Run herdr's CLI as tools do, through `"$HERDR_BIN_PATH"`:

```sh
"$HERDR_BIN_PATH" pane list
"$HERDR_BIN_PATH" pane split --pane "$HERDR_PANE_ID" --direction right --focus
"$HERDR_BIN_PATH" pane neighbor --pane "$HERDR_PANE_ID" --direction right
"$HERDR_BIN_PATH" pane run w1a2b3c4d5e6:p0f1e2d3c4b5a "make test"
"$HERDR_BIN_PATH" agent start helper --kind claude --pane "$HERDR_PANE_ID"
```

A success prints herdr's answer as one JSON line on stdout and exits 0. An
error prints the answer on stderr and exits 1. A wrong command line prints
the usage on stderr and exits 2. `pane send-text`, `pane send-keys`, `pane
run` and the report commands print nothing when they succeed.

It answers `pane`, `tab`, `workspace`, `agent`, `worktree`,
`notification show`, `api snapshot`, `server reload-config`, `terminal
title`, `status`, `session list` and `plugin`. A method that tuios does not
answer, such as `layout.apply` or `workspace.move`, fails with code
`unsupported`. A command that acts on herdr's own machine (`session stop`,
`plugin install`, `server stop` and the rest) fails with code `unsupported`
and does nothing.

## herdr plugins

tuios runs herdr plugins (`herdr-plugin.toml`) that the person enabled. From
a pane you can run an enabled plugin's action and open its panes:

```bash
"$HERDR_BIN_PATH" plugin list --json
"$HERDR_BIN_PATH" plugin action invoke ACTION --plugin PLUGIN_ID
"$HERDR_BIN_PATH" plugin pane open --plugin PLUGIN_ID --entrypoint PANE_ID
"$HERDR_BIN_PATH" plugin log list --plugin PLUGIN_ID
```

You need `admin` to run an action, open a plugin pane or read the plugin
log. You cannot enable, disable, link or unlink a plugin from a pane: the
call fails with code `forbidden` and changes nothing. Ask the person to run
`tuios plugins enable PLUGIN_ID` from a terminal outside tuios.

## What it may do

Each call holds your pane's grants, as the tuios verb that does the same work
does:

- A read (`pane list`, `pane get`, `pane read`, `pane neighbor`, `pane edges`,
  `pane process-info`) needs `read`. `pane process-info` gives another pane's
  arguments and directories only with `write` on its session or `admin`.
- Typing (`pane send-text`, `pane send-keys`, `pane run`) needs `write`, and
  `respond` to type into a pane that waits on a prompt.
- A split, close, rename, focus, swap, zoom, resize or move, `workspace
  focus`, `workspace report-metadata` and `terminal title`, need `admin`.
- `agent start` needs `fan`, and `write` for the typing.

A refused call fails with code `forbidden` and changes nothing.

## Use tuios's own commands

For your own work, use the tuios verbs: `tuios split-window`, `tuios
send-text`, `tuios wait-for`, `tuios start-agent`. They do more, and their
errors say what to do next. herdr's command line is for tools that already
speak herdr.
