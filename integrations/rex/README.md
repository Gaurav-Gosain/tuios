# tuios in Rex

`tuios.lua` puts tuios's command palette in
[Rex](https://www.superlogical.com/rex)'s: each time Rex loads its config, it
asks `tuios actions --json` for the actions and defines one Rex action per
row, named `tuios_` and the tuios action. It adds Start an Agent, which opens
tuios's New agent sheet or takes harness, name, prompt, folder and machine,
and a check. Keys are yours to choose. Each action works on the tuios session
shown in the focused Rex pane. [docs/REX.md](../../docs/REX.md) has the list
and the options.

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
or else `lua`. The e2e test `TestRexActionsComeFromTuios` loads this file
against the real binary, and `e2e/tui/testdata/rex-real.sh` runs it in a
real Rex server of its own.

```sh
integrations/rex/check.sh
```

What runs in real Rex, and what does not, is listed in the pull request
that changes it.

## Limits

- Only a tuios client on the same machine as the Rex server is found. A tuios
  session reached over `ssh` is not.
- The tuios actions need the tuios client in the focused pane, since that
  client runs them. With the pane on another session, they act on that one.
