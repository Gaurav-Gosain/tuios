# tuios in Rex

[Rex](https://www.superlogical.com/rex) is a terminal multiplexer app. tuios
adds actions to Rex's command palette that work on the tuios session shown in
the focused Rex pane.

## Install

```sh
tuios integration install rex
```

This does four things:

1. It writes `tuios.lua` next to Rex's `init.lua`. Rex finds `init.lua` at
   `REX_CONFIG`, else `$XDG_CONFIG_HOME/rex/init.lua`, else
   `~/.config/rex/init.lua`.
2. It adds one line to `init.lua`:
   `require("tuios").setup{} -- added by tuios integration`. Nothing else in
   the file changes. When `init.lua` already requires tuios on a line of your
   own, that line is kept and no line is added.
3. It runs `rex config check`. When Rex refuses the config, both files are put
   back as they were and the command prints Rex's message.
4. It runs `rex config reload`, then the `tuios_check` action inside Rex, and
   prints which tuios the actions run and whether Start an Agent finds its
   agent.

The first time it rewrites `init.lua`, the file as it was is kept as
`init.lua.tuios.bak`. A `tuios.lua` with changes of your own is left alone;
`--force` replaces it and keeps yours as `tuios.lua.tuios.bak`. An older
`tuios.lua` that tuios wrote is replaced.

```sh
tuios integration status rex      # the files, whether Rex loaded them, what tuios_check says
tuios integration uninstall rex   # removes the marked line and tuios.lua, then reloads
```

## The actions

| Action | Palette title | What it does |
| --- | --- | --- |
| `tuios_agents` | tuios: Open the Agents View | Opens the agents view, as `Ctrl+B i` does |
| `tuios_mail` | tuios: Open the Mail for You | Opens the agents view on your mail, as `Ctrl+B M` does |
| `tuios_next_waiting` | tuios: Jump to the Agent That Waits | Moves the focus to the oldest item that needs you, as `Ctrl+B o` does |
| `tuios_start_agent` | tuios: Start an Agent | Starts an agent in a new tuios pane and focuses it. Takes `harness`, `name` and `prompt` |
| `tuios_check` | tuios: Check the Rex Integration | Says which tuios the actions run, its version, the focused pane's session, and whether the default agent is found |

Keys are off until you name them:

```lua
require("tuios").setup{
  bin = "tuios",          -- the tuios program; see "Finding tuios"
  harness = "claude",     -- the agent tuios_start_agent starts by default
  keys = { tuios_agents = "cmd+shift+a", tuios_next_waiting = "cmd+shift+j" },
}
```

From the command line: `rex do tuios_start_agent harness=codex name=review prompt='Review the diff.'`

## Finding tuios and the agents

The Rex server is started by the app, so its `PATH` has none of the folders a
login shell adds, such as `~/.local/bin`.

- `tuios.lua` looks for tuios on `PATH`, then in `~/.local/bin`,
  `/opt/homebrew/bin`, `/usr/local/bin`, `$GOBIN`, `$GOPATH/bin`, `~/go/bin`
  and the Nix profiles. When none has it, the action says where it looked.
  Give the full path then: `setup{ bin = "/full/path/tuios" }`.
- `tuios start-agent` looks for the agent on the `PATH` its caller sent, then
  on the daemon's `PATH`, the one a new pane gets, and runs the agent with
  that `PATH`. So an agent you can start in a tuios pane starts from Rex too.

## How an action finds the session

An action asks Rex for the focused pane's foreground process. When that is a
tuios client, `tuios list-clients --json` has its pid with the session it
shows. A pane that runs a shell, or `ssh` to another machine, shows no tuios
client on this machine, and the action says so.

The client actions run `tuios run-command -s SESSION Action NAME`, which the
client showing the session runs as its key would. Start an Agent runs
`tuios start-agent --no-wait`, which answers once the agent has started, well
inside the 30 seconds Rex gives an action, so a failure shows as tuios's own
message. The prompt is typed once the agent is ready.

## Limits

- Only a tuios client on the same machine as the Rex server is found.
- The client actions need the tuios client in the focused pane, since that
  client runs them.
- `install` talks to the local Rex server, or the one `REX_SERVER` names.
