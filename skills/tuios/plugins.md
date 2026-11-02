# Plugins

A plugin is a folder with a `tuios-plugin.toml` and programs. The daemon runs
the programs outside every pane. docs/PLUGINS.md is the contract.

An enabled plugin runs with the person's rights. Grants limit what it asks of
tuios. Grants are not a sandbox. Tell the person this before you suggest a
plugin.

## What you cannot do from a pane

Only the person installs, approves, enables or removes a plugin. From a pane,
or from a plugin's process, these fail and change nothing. Do not try to go
around that. Give the person the command to run in a terminal outside tuios:

```sh
tuios plugins install ./my-plugin
tuios plugins update me.commits
tuios plugins approve me.commits
tuios plugins enable me.commits
tuios plugins uninstall me.commits
```

## Look at the plugins

Reading the list runs no plugin code, and it works from a pane.

```sh
tuios plugins list --json
tuios plugins info me.commits --json
```

Each record has `plugin_id` and `enabled`. A plugin that cannot run also has `error`,
with `code` and `message`.

## Write one

```toml
api = 1
id = "me.commits"
name = "Commits"
version = "0.1.0"

[[process]]
id = "show"
run = "pane"
title = "Last commits"
placement = "popup"
command = ["./show.sh"]
```

`run` is `action`, `pane` or `startup`. An action or a pane costs nothing until
the person uses it. A `startup` process stays in memory. Use an action or a pane
when you can.

`[permissions] grants` is `read`, `write`, `fan`, `ui` or `admin`. Ask for the least
you need. A plugin that only opens a pane needs no grant. A plugin never holds
`respond`.

A key that tuios does not know is refused, so a typo fails at once. A manifest
for a newer tuios is refused with the command to run.

## What a plugin's process gets

| Variable | Value |
| --- | --- |
| `TUIOS_BIN` | The tuios binary that runs the plugin. Use it instead of `tuios` |
| `TUIOS_PLUGIN_ROOT` | The plugin folder |
| `TUIOS_PLUGIN_CONFIG_DIR` | A folder for its settings |
| `TUIOS_PLUGIN_STATE_DIR` | A folder for its data |
| `TUIOS_PLUGIN_ENTRY` | The id of the process, for an action or a pane |
| `TUIOS_PLUGIN_SESSION` | The session of the focused pane, for `-s` |
| `TUIOS_PLUGIN_PANE` | The focused pane, for `-w` |
| `TUIOS_PLUGIN_CWD` | The folder of the focused pane |
| `TUIOS_PLUGIN_CONTEXT_JSON` | The whole context as JSON |

## Run one the person enabled

With `admin` you can run an action and read the log:

```sh
tuios plugins run me.notes save --wait
tuios plugins log me.notes
```

An action's output goes to the log. The person does not see it.

## Show the person something

A plugin with the `ui` grant names a cell in `[[cell]]` and sets it from any of
its processes. Only a plugin's process can do this. From a pane or a shell it
fails. The daemon keeps the text, so the process can exit.

```sh
tuios plugin-cell set status "CI ok" --action open
tuios plugin-cell clear status
tuios plugin-notify "The build failed" --level error
```

## Which verbs a plugin can use

```sh
tuios list-verbs
tuios pane-grants --json
```

A refusal is `forbidden`, and it names the grant. In a plugin's process,
`tuios pane-grants --json` also shows `plugin`.
