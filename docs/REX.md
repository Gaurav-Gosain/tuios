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

The actions are tuios's own: every row of the tuios command palette that
runs as a key would, read from `tuios actions --json` each time Rex loads its
config. A new tuios action shows in Rex's palette with no change to
`tuios.lua`. Each is named `tuios_` and the tuios action name, and titled
`tuios:` and the palette row, such as `tuios_prefix_inbox`, "tuios: Agents:
What Needs You". `rex actions | grep tuios_` lists them all. Among them:

| Action | Palette title | What it does |
| --- | --- | --- |
| `tuios_goto_picker` | tuios: Go To: An Agent, Session, Workspace or Pane | Opens tuios's go-to picker, with each agent's state |
| `tuios_prefix_inbox` | tuios: Agents: What Needs You | Opens the agents view, as `Ctrl+B i` does |
| `tuios_prefix_mail` | tuios: Agents: Mail for You | Opens the agents view on your mail |
| `tuios_prefix_next_attention` | tuios: Agents: Go to the Oldest Waiting | Moves the focus to the oldest item that needs you |
| `tuios_new_window`, `tuios_layout_bsp`, `tuios_prefix_review`, `tuios_prefix_selection`, `tuios_screenshot`, ... | | The window, layout, review, copy mode and screenshot rows of the tuios palette |
| `tuios_user_NAME` | tuios: your label | Your own `[[actions]]` and `[[keybindings.command]]` entries, `user:NAME` |

Your own actions come from tuios's `config.toml` as Rex's server finds it
(`$XDG_CONFIG_HOME/tuios`, else `~/.config/tuios`). A dash in the name is an
underscore in Rex, since a Rex action name has only lowercase letters, digits
and underscores: `user:open-in-editor` is `tuios_user_open_in_editor`. Two
names that come out the same, such as `a-b` and `a_b`, keep the first, and
`tuios_check` names the other. The palette title is your `label` as you wrote
it. An action offered only on a card, a file or another target that is not a
pane is left out, since Rex runs it on the focused pane. When tuios cannot
read `config.toml`, Rex still gets the built-in actions and `tuios_check`
says why. Run `rex config reload` after you add an action.

Two more are written in `tuios.lua`:

| Action | Palette title | What it does |
| --- | --- | --- |
| `tuios_start_agent` | tuios: Start an Agent | With no field given, opens tuios's New agent sheet. With fields, starts the agent in a new tuios pane and focuses it |
| `tuios_check` | tuios: Check the Rex Integration | Says which tuios the actions run, its version, the focused pane's session, whether the default agent is found, and whether Rex has the actions this tuios lists |

Start an Agent takes `harness`, `name`, `prompt`, `folder` and `machine` (a
machine of tuios's `[hosts]` table). The harnesses tuios knows are the
choices `rex actions --json` shows for `harness`. Rex does not hold a value
to that list, so any program start-agent can run works too.

```sh
rex do tuios_start_agent harness=codex name=review prompt='Review the diff.' folder=~/src/app
```

The go-to picker and the New agent sheet are tuios's own, drawn in the
focused pane. A Lua action in Rex has no picker or form of its own to open,
and a list made when Rex loads its config is out of date by the time it is
used, while tuios's own are always current.

### When tuios is updated

`tuios.lua` reads the list at each config load, so `rex config reload` picks
up the actions of a new tuios. `tuios integration install rex` reloads too.
`tuios integration status rex` says when the list Rex loaded is not the one
the tuios on disk gives, and to reload.

When tuios cannot give the list (none is found, or one older than this
`tuios.lua`), Rex still loads the config, with the go-to, agents view, mail,
oldest waiting, Start an Agent and check actions. `tuios_check` says why.

### Keys

Keys are off until you name them:

```lua
require("tuios").setup{
  bin = "tuios",          -- the tuios program; see "Finding tuios"
  harness = "claude",     -- the agent tuios_start_agent starts when no harness is given
  keys = { tuios_prefix_inbox = "cmd+shift+a", tuios_prefix_next_attention = "cmd+shift+j" },
}
```

The names of the earlier `tuios.lua`, `tuios_agents`, `tuios_mail` and
`tuios_next_waiting`, still bind the same actions. A key for an action Rex
does not have is left unbound, and `tuios_check` names it, so a key for an
action a later tuios drops never stops Rex loading the rest of `init.lua`.

## Agents that wait

When an agent needs you (a question, an approval, a login), the tuios client
in the Rex pane tells Rex in two ways, with nothing to set up:

- **A notification.** tuios writes OSC 9 with the pane's name and what the
  agent asked, such as "review needs you · approve Bash: make". Rex raises it
  as a desktop notification when Settings > Notifications > System
  Notifications > Terminal notifications is on. `[notifications.agent]` in
  tuios's config.toml says which states alert.
- **A status record.** tuios reports its agents' state with OSC 7501, the
  Program Status Protocol. Rex shows it on the tab and in the session picker,
  and, with Settings > Notifications > Program Activity > Show system
  notifications, raises a macOS notification while Rex is in the background.
  Rex's docs say a click on that one opens the program's tab. See
  "Per-pane status" below for one record per agent.

**None for what you are looking at.** tuios asks its terminal for focus
events, and Rex sends them when its focus moves between panes. While Rex
shows the tuios pane, tuios writes no notification for the tuios pane that
has the focus (`suppress_focused`, on by default). Another tuios pane still
notifies, and so does the focused one while Rex shows another pane or tab.

**Jump to it.** Rex has no notification with a button, and a Lua action
cannot raise one. Once Rex shows the tuios pane, the action
`tuios_prefix_next_attention` ("tuios: Agents: Go to the Oldest Waiting")
moves the focus to the agent that has waited longest, and
`tuios_prefix_inbox` opens the agents view on what needs you. Bind one to a
key (see Keys).

**Two notifications for one question.** With Rex in the background and both
Rex settings above on, one question can give two macOS notifications, one
from each. Keep one: turn off Rex's Terminal notifications, or set
`notify = false` under `[notifications.agent]` in tuios. The status record
still reaches Rex.

Checked in a real Rex server (`e2e/tui/testdata/rex-real.sh`, "agents that
wait"): another pane's question and the focused pane's question while Rex
showed another pane each gave one `terminal.desktop_notification`; the
focused pane's question while Rex showed tuios gave none, before and after
Rex's focus events. What the Mac app draws for them was not checked there,
since that server has no app attached.

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

The tuios actions run `tuios run-command -s SESSION Action NAME`, which the
client showing the session runs as its key would. Start an Agent runs
`tuios start-agent --no-wait`, which answers once the agent has started, well
inside the 30 seconds Rex gives an action, so a failure shows as tuios's own
message. The prompt is typed once the agent is ready.

## Limits

- Only a tuios client on the same machine as the Rex server is found.
- The tuios actions need the tuios client in the focused pane, since that
  client runs them.
- Rex's palette lists the agent actions before any agent has run, while
  tuios's own palette waits for one.
- `install` talks to the local Rex server, or the one `REX_SERVER` names.
