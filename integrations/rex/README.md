# tuios in Rex

`tuios.lua` adds tuios actions to [Rex](https://www.superlogical.com/rex)'s
command palette, and keys for them if you want keys. Each action works on the
tuios session shown in the focused Rex pane.

| Action | Palette title | What it does |
| --- | --- | --- |
| `tuios_agents` | tuios: Open the Agents View | Opens the agents view, as `Ctrl+B i` does |
| `tuios_mail` | tuios: Open the Mail for You | Opens the agents view on your mail, as `Ctrl+B M` does |
| `tuios_next_waiting` | tuios: Jump to the Agent That Waits | Moves the focus to the oldest item that needs you, as `Ctrl+B o` does |
| `tuios_start_agent` | tuios: Start an Agent | Starts an agent in a new tuios pane and focuses it. Takes `harness`, `name` and `prompt` |

## Install

Copy `tuios.lua` next to your Rex `init.lua` (see Rex's
[configuration file](https://www.superlogical.com/rex/docs/customize/config.md)
page for where it lives), then add to `init.lua`:

```lua
require("tuios").setup{}
```

Run `rex config reload`. The actions are in the palette under "tuios".

## Options

```lua
require("tuios").setup{
  bin = "tuios",          -- the tuios program; a name is looked up in PATH
  harness = "claude",     -- the agent tuios_start_agent starts by default
  keys = {                -- no keys unless you name them
    tuios_agents = "cmd+shift+a",
    tuios_next_waiting = "cmd+shift+j",
  },
}
```

A key for an action that does not exist stops `setup` with an error, so a
typo shows when Rex loads the file.

From the command line:

```sh
rex do tuios_start_agent harness=codex name=review prompt='Review the diff on this branch.'
```

## How it finds the session

An action asks Rex for the focused pane's foreground process. When that is a
tuios client, `tuios list-clients --json` has its pid with the session it
shows. A pane that runs a shell, or `ssh` to another machine, shows no tuios
client on this machine, and the action says so and does nothing.

The client actions run `tuios run-command -s SESSION Action NAME`, which the
client that shows the session runs as its key would. `tuios_start_agent` runs
`tuios start-agent` on its own, because an agent can take longer to be ready
than the 30 seconds Rex gives an action.

## Checks

`check.sh` parses the file with each Lua on PATH and runs `tuios_test.lua`, a
test against a stub of Rex's Lua API, under LuaJIT (Lua 5.1, as Rex runs it)
or else `lua`.

```sh
integrations/rex/check.sh
```

What was run in real Rex: a script loaded with `rex do FILE --action` that
calls the session lookup on the focused pane, which found the session its
tuios client showed. The four actions were not run in real Rex, since each
changes what the person sees. The key bindings were not loaded into a real
`init.lua`.

## Limits

- Only a tuios client on the same machine as the Rex server is found. A tuios
  session reached over `ssh` is not.
- The client actions need the tuios client in the focused pane, since that
  client runs them. With the pane on another session, they act on that one.
