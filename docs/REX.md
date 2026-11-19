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

Four more are written in `tuios.lua`:

| Action | Palette title | What it does |
| --- | --- | --- |
| `tuios_start_agent` | tuios: Start an Agent | Asks for the agent, the machine, the folder and the first prompt, then starts the agent in a new tuios pane and focuses it. See "Start an agent" |
| `tuios_switch_session` | tuios: Switch or Attach a Session | Lists the tuios sessions. The tuios client in the focused pane switches to the session you pick. From a pane with no tuios client, a new Rex window attaches it |
| `tuios_answer` | tuios: Answer an Agent | Lists the agents that wait on an approval or a question, shows the answers of the one you pick, and sends the answer you pick. See "Answer an agent" |
| `tuios_check` | tuios: Check the Rex Integration | Says which tuios the actions run, its version, the focused pane's session, whether the default agent is found, and whether Rex has the actions this tuios lists |

### Start an agent

`tuios_start_agent` asks each question in Rex's palette:

1. The agent. The list holds the harnesses tuios knows, with the default
   (`harness` in `setup`) first. Type a command to start another program.
2. The machine, only when tuios's `[hosts]` table has a machine.
3. The folder. The list holds the folders of the session's panes, with the
   focused pane's folder first. Type a path to start somewhere else. On
   another machine you type the folder, and an empty field means the home
   folder.
4. The first prompt. An empty field starts the agent with no prompt.

The action takes `harness`, `name`, `prompt`, `folder` and `machine` as
arguments, and asks only what they leave out. An empty value counts as
given, so `prompt=` starts with no prompt. The harnesses tuios knows are the
choices `rex actions --json` shows for `harness`. Rex does not hold a value
to that list, so any program start-agent can run works too.

```sh
rex do tuios_start_agent harness=codex name=review prompt='Review the diff.' folder=~/src/app
```

When no Rex app can show a question (`no_client` or `unsupported`), a run
with no argument opens tuios's New agent sheet in the focused pane. A run
with arguments starts the agent with the defaults for the rest: the default
harness, this machine, the focused pane's folder and no prompt. When you
close a question, nothing starts. `timeout`, `busy` and `failed` stop the
action with a message, as for "Answer an agent".

### Switch or attach a session

`tuios_switch_session` lists the tuios sessions on this machine. Each row
shows the number of panes and whether a client is attached. The session the
focused pane shows says "shown here". Type a new name to create a session.

- When the focused Rex pane shows a tuios client, that client switches to
  the session, as the go-to picker does (`tuios switch-session`).
- Otherwise, a new Rex window runs `tuios attach` for the session.

`session` skips the question, and `create=true` creates a missing session:

```sh
rex do tuios_switch_session session=api
```

The go-to picker (`tuios_goto_picker`) is tuios's own, drawn in the focused
pane. It also lists the agents, the workspaces and the panes.

### Answer an agent

`tuios_answer` answers an agent that waits on an approval or a question:

1. It lists the agents that wait, oldest first, from `tuios list-attention`.
   Each row shows the pane's name, the question, the session and how long
   the agent has waited. Pick one.
2. It reads the prompt with `tuios peek-prompt` and shows the answers the
   prompt takes. An approval shows Allow, Allow always and Deny. A question
   shows its numbered options. Type an answer opens a text field when the
   prompt takes text.
3. It sends the answer you pick with `tuios respond`, with the id of the
   prompt it showed you. When the prompt changed in between, tuios refuses
   the answer and presses nothing.

It sends only the answer you pick. It works from any Rex pane, also a pane
that does not show tuios. Rex shows each question in every connected Rex
app, and the first answer wins.

tuios takes an answer from Rex only when you allow it. Add this to tuios's
`config.toml`, then restart the tuios daemon, which reads it when it starts:

```toml
[daemon]
respond_from_shell = true
```

This lets each program that runs outside a tuios pane answer for you, Rex's
server too. A program in a tuios pane is refused either way. Without the
setting, the action says what to add and sends nothing. An approval that
matches a rule of `[agents.approvals.risk]` is refused too. Answer it in the
tuios agents view.

When nobody answers a question, the action stops and sends nothing:

| Reason | What the action says |
| --- | --- |
| `cancelled` | Nothing. You closed the question. |
| `timeout` | Nobody answered the question in time. |
| `no_client` | No Rex app is connected to show the question. |
| `unsupported` | No connected Rex app can show a question. Update the Rex app. |
| `busy` | Each Rex app already shows another question. Answer that one, then run this again. |
| `failed` | Rex could not show the question, and Rex's reason. |

Each message ends with the reason in brackets, such as `(no_client)`, for a
script.

With arguments, the action asks only what they leave out. `window` (with
`session`, or as `HOST:SESSION:WINDOW`) skips the list of agents.
`answer` skips both questions, and needs `window` and the `prompt_id` that
`tuios peek-prompt --json` prints. `choose` takes the option number in
`value`, and `text` takes the text in `value`.

```sh
tuios peek-prompt -w review --json     # prints the prompt_id
rex do tuios_answer window=review answer=approve prompt_id=75f8b9fadb5b5dfc
```

### When tuios is updated

`tuios.lua` reads the list at each config load, so `rex config reload` picks
up the actions of a new tuios. `tuios integration install rex` reloads too.
`tuios integration status rex` says when the list Rex loaded is not the one
the tuios on disk gives, and to reload.

When tuios cannot give the list (none is found, or one older than this
`tuios.lua`), Rex still loads the config, with the go-to, agents view, mail,
oldest waiting, Start an Agent, Switch or Attach a Session, Answer an Agent
and check actions. `tuios_check` says why.

### Keys

Keys are off until you name them. `keys = "suggested"` binds one key,
`ctrl+cmd+t`, that enters the `tuios` key mode. In the mode, one letter
runs a tuios action:

| Key | Action |
| --- | --- |
| `ctrl+cmd+t`, then `a` | `tuios_prefix_inbox`: the agents view on what needs you |
| `ctrl+cmd+t`, then `j` | `tuios_prefix_next_attention`: jump to the agent that has waited longest |
| `ctrl+cmd+t`, then `r` | `tuios_answer`: answer an agent |
| `ctrl+cmd+t`, then `g` | `tuios_goto_picker`: go to an agent, session, workspace or pane |
| `ctrl+cmd+t`, then `s` | `tuios_switch_session`: switch or attach a session |
| `ctrl+cmd+t`, then `m` | `tuios_prefix_mail`: your mail |
| `ctrl+cmd+t`, then `n` | `tuios_start_agent`: start an agent |

The mode ends after one of these keys. `Escape` also ends it. In the mode,
a key that is not in the table does nothing, so you cannot type into the
terminal by mistake. Outside the mode, the letters do what they did before.

`ctrl+cmd+t` holds `cmd`, which a Mac never sends to a program in a
terminal, so no key of tuios's (such as `ctrl+b`) or of your shell's is
taken. It is not a default key of Rex for Mac (its Mac Actions page) or a
macOS `ctrl+cmd` shortcut. A key you bound in Rex's settings, or with
`rex.bind` above the require line, is replaced by the newest binding, so
put your own `rex.bind` lines after it. `rex keymap` lists the mode and
its keys.

```lua
require("tuios").setup{
  bin = "tuios",          -- the tuios program; see "Finding tuios"
  harness = "claude",     -- the agent tuios_start_agent starts when no harness is given
  keys = "suggested",
}

-- The set with changes: another key to enter the mode, false to leave a
-- key out, tuios/KEY for a key in the mode, and a key outside the mode.
require("tuios").setup{
  keys = {
    suggested = true,
    mode = "cmd+shift+t",
    tuios_prefix_mail = false,
    tuios_new_window = "tuios/w",
    tuios_prefix_inbox = "cmd+shift+a",
  },
}

-- Only keys of your own, with no mode.
require("tuios").setup{
  keys = { tuios_prefix_inbox = "cmd+shift+a", tuios_prefix_next_attention = "cmd+shift+j" },
}
```

`mode = false` binds the keys in the mode and no key to enter it. Bind
`client.mode.enter` with `{ name = "tuios", once = true }` yourself then.

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
`tuios_prefix_inbox` opens the agents view on what needs you.
`tuios_answer` answers the agent from Rex, with no need to show the tuios
pane (see "Answer an agent"). In the
suggested keys (see Keys) they are `ctrl+cmd+t` then `j`, and `ctrl+cmd+t`
then `a`.

**Two notifications for one question.** With Rex in the background and both
Rex settings above on, one question can give two macOS notifications, one
from each. Keep one: turn off Rex's Terminal notifications, or set
`notify = false` under `[notifications.agent]` in tuios. The status record
still reaches Rex.

Checked in a real Rex server (`e2e/tui/testdata/rex-real.sh`, "agents that
wait"): another pane's question and the focused pane's question while Rex
showed another pane each gave one `terminal.desktop_notification`; the
focused pane's question while Rex showed tuios gave none, before and after
Rex's focus events. The tuios pane in Rex has focus events on (mode 1004). What the Mac app draws for them was not checked there,
since that server has no app attached.

## Per-pane status

tuios gives Rex one status record (OSC 7501) for each tuios session by
default: the most urgent state of its agents, and a message that counts them,
such as "1 needs input, 2 working". Rex shows one status for each Rex pane,
so a tuios session with many agents shows as one.

For one record per agent, set this in tuios's config.toml:

```toml
[agents]
host_program_status = "panes"
```

Each tuios pane with an agent then has a record of its own, with the pane's
name as the title and what the agent asked or is doing as the message, under
the id `SESSION/PANE`. `"off"` sends none. A client reads the value when it
starts and when config.toml changes. `tuios integration status rex` says which
one is set.

Checked in a real Rex server (`rex-real.sh`, "per-pane status"): by default
the Rex pane's `program_status` holds the one record `demo`; with `"panes"`
it holds `demo/PANE` for each pane with an agent, such as `demo/3816ea3d`,
state blocked, kind permission, with the pane's title and the agent's
question as the message. How the Mac app's sidebar and
Activity view draw several records from one Rex pane, and what the iOS app
shows, was not checked: that Rex server has no app attached, and the
person's own Rex is not used for tests.

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
