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
| `tuios_check` | tuios: Check the Rex Integration | Says which tuios the actions run, its version, the focused pane's session, and whether the default agent is found |

## Install

```sh
tuios integration install rex
```

That copies `tuios.lua` next to Rex's `init.lua`, adds the require line,
checks and reloads Rex's config, and says which tuios the actions run. See
[docs/REX.md](../../docs/REX.md) for the options, `status`, `uninstall`, and
how tuios and the agents are found when Rex's `PATH` lacks them.

`internal/integration/assets/rex/tuios.lua` is a copy of this file for the
binary. Copy a change there and run `go generate ./internal/integration`.

## Checks

`check.sh` parses the file with each Lua on PATH and runs `tuios_test.lua`, a
test against a stub of Rex's Lua API, under LuaJIT (Lua 5.1, as Rex runs it)
or else `lua`.

```sh
integrations/rex/check.sh
```

What runs in real Rex, and what does not, is listed in the pull request
that changes it.

## Limits

- Only a tuios client on the same machine as the Rex server is found. A tuios
  session reached over `ssh` is not.
- The client actions need the tuios client in the focused pane, since that
  client runs them. With the pane on another session, they act on that one.
