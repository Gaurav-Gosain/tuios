# Configuration

The configuration reference lives on the docs site: https://tuios.dev/docs/configuration

It covers the whole `config.toml`: the `[appearance]` table and its `sidebar`, `scrollbar`, dock, and window-button options, `[notifications.agent]`, all 19 `[keybindings]` sections, `[daemon]`, `[startup]`, `[tape]`, `[screenshot]`, `[hooks]`, and `[debug]`, along with what hot-reloads and what needs a restart.

`tuios list-options` describes every settable path with its type, default, and accepted values, straight from the registry the validator uses. The in-app settings page (`Ctrl+B ,`) edits and persists the same options, and its rows are derived from that same registry: an option an agent can set is an option a person can reach, and a test fails the build if one is not.

## Backgrounds

A cell that has no background of its own is transparent, so your terminal's
own background shows through it. That is the default everywhere: pane content
a program left on the default background, the space between panes, the
borders, the dock and the rail. The background options paint those cells
instead, one surface at a time or all at once.

| Option | Surface | Values | Default |
|---|---|---|---|
| `appearance.background` | Every surface below that is not set on its own | `off`, `theme`, `#RRGGBB` | `off` |
| `appearance.pane_background` | Pane content, the thin scrollbar over it and the scrollback browser | `off`, `theme`, `#RRGGBB`, or empty | empty (follows `background`) |
| `appearance.desktop_background` | Behind and between panes: gaps between tiled panes, the space around floating ones, an empty workspace and its welcome splash | `off`, `theme`, `#RRGGBB`, or empty | empty (follows `background`) |
| `appearance.window_chrome_background` | Pane borders and title bars, the lines between shared-border panes, the capture marquee | `off`, `theme`, `#RRGGBB`, or empty | empty (follows `background`) |
| `appearance.dock_background` | The dock | `off`, `theme`, `#RRGGBB`, or empty | empty (follows `background`) |
| `appearance.sidebar.background` | The rail | `off`, `theme`, `#RRGGBB`, or empty | empty (follows `background`) |

What each value paints:

| Value | What is painted |
|---|---|
| `off` | Nothing. The cell is transparent and your terminal's own background shows through. |
| `theme` | The active theme's background, with the theme's foreground on text left in the default colour. With no theme set there is no theme background, so it behaves as `off`. |
| `#RRGGBB` | That colour. With a theme set, default-coloured text takes the theme's foreground, lifted until it reads on the colour; with none, it keeps your terminal's own. |

**Precedence.** A surface's own option wins whenever it holds a value, `off`
included. Left empty, it follows `appearance.background`. So one line paints
everything, and a second line changes or clears one surface:

```toml
[appearance]
background = "theme"          # every surface on the theme's background
dock_background = "#11111b"   # the dock a shade darker
pane_background = "off"       # panes keep the terminal's own background

[appearance.sidebar]
background = "#181825"
```

A value that is not a keyword or a `#RRGGBB` literal resolves to `off` for that
surface (it does not fall back to `background`), and the validator warns about
it, as it does about `theme` with no theme set.

**What is kept.** Only cells with no background of their own are painted. A
background a program chose for a cell always wins, and so do the marks tuios
paints over a pane (the selection, search matches, the copy mode cursor) and
every colour the chrome sets itself: a border's ink stays its focus colour, the
title bar's buttons keep theirs, the dock's pills and the rail's highlighted
rows keep their fills. The overlay panels (the palette, settings, which-key,
the Inbox, every picker, the tooltips and badges) are not on the list because
they have no transparent cells: each fills its whole rectangle with its own
surface colour.

**Programs that ask.** A program can ask the terminal for its background with
OSC 11 and its default text colour with OSC 10, and some pick a dark or light
palette from the answer. While the pane background paints a colour, a pane's
OSC 11 is answered with that colour and OSC 10 with the text colour tuios gives
default text there, so the program sees what it is drawn on. A program that set
its own colours with OSC 10 or 11 gets its own back. With the pane background
off the answers are what they always were. In a daemon session the daemon's
emulator answers, and the client tells it the painted pair when it syncs; with
several clients attached, the last one to sync decides.

All six hot-reload and are on the **Backgrounds** tab of the settings page
(`Ctrl+B ,`, then `]`): an All surfaces row and one row per surface, each a
colour row that opens the same picker as the border colours, with `off` and
`theme` beside the grid and `x` to clear a surface back to following All
surfaces. `tuios set-config appearance.dock_background <value>` sets one from a
script. They reach every client of the session, SSH and browser ones included,
since they draw the same frame. A screenshot of one pane is drawn on the pane's
painted colour, and a screen or region capture carries every painted surface
as it is drawn.

## The dock's components

The `[dock]` table is the one part of the configuration that is not a set of
scalar options, so `list-options` does not carry it. The settings page edits it
through an editor of its own rather than a row: **Dock → Components**, where the
three regions and what is in them are one list. Shifted arrows move a component
and carry it into the next region off the end of its own, Enter takes one off
the bar or puts it back, `u` undoes the session's edits and `r` restores the
defaults. It is three ordered lists of component names, plus a table per custom
component:

```toml
[dock]
left   = ["mode", "workspaces", "trail", "tape"]
center = ["windows"]
right  = ["notifications", "copy-help", "cpu", "ram", "clock", "session-controls"]

[dock.clock]
format = "15:04"

[dock.custom.branch]
command  = "~/.config/tuios/dock/git-branch.sh"
refresh  = "event:after-focus-change"
on-click = "tuios new-window log -- git log --oneline -20"
```

The lists above are the default: omit the whole table and the bar is unchanged.
A custom component's first line of stdout becomes its cell, it is hidden when
the command fails, and `tuios list-dock-components` says which and why.

`examples/dock/README.md` is the full contract and five working recipes.

## Approvals from the Inbox

The `[agents.approvals]` table lets the Inbox answer a harness's permission
prompt, so you do not have to go to the pane. It is off by default: while a
prompt is held for the Inbox, the harness shows nothing in its pane.

```toml
[agents.approvals]
enabled = ["claude-code", "opencode"]
hold_seconds = 120
```

`enabled` names the harnesses, by id or alias: `claude-code` (or `claude`),
`opencode` and `kilo` have a decision channel tuios can answer through. Other
names are accepted and hold nothing. Even for these, only a call the Inbox can
show whole on one line is held, such as a short shell command or a file read;
an edit, an MCP tool or a long command is answered in the pane.
`hold_seconds` is how long a prompt waits for your answer before the harness
asks in its pane after all: 120 when unset, kept between 10 and 300.

The daemon reads the table when it starts and again when the file changes; a
change applies to the next prompt. Like `[dock]` and `[hosts]`, it is not in
`list-options` and `set-option` cannot change it. Turning it on gives no
program the power to answer: only you, at an attached client, can. It also
needs version 2 of the integration:
run `tuios integration install claude-code` (or `opencode`, `kilo`) again after
upgrading. [AGENT_STATE.md](AGENT_STATE.md#approvals-from-the-inbox) says how
a prompt is held, answered and handed back.

## Plans, risk rules, the recap and the queue

These tables configure the agent review, triage, reply and approval work,
which is being built. The daemon reads and checks them now; a table whose work
has not landed changes nothing yet. Every value has a default, so a file
without them behaves as the defaults say. Like `[agents.approvals]`, they are
file-plane config: not in `list-options`, and `set-option` cannot change
them, so a pane cannot switch a risk rule off through tuios.

```toml
[agents.approvals]
hold_plans = true                 # a plan follows enabled

[agents.approvals.risk]
builtin = true                    # keep the shipped rules
panes_may_allow = false           # a pane with the respond grant may not allow a risky call

[[agents.approvals.risk.rule]]
name = "kubectl apply"
tools = ["Bash", "bash", "shell"]
pattern = '\bkubectl\s+(apply|delete)\b'

[agents.recap]
mode = "toast"                    # toast, inbox or off
away = "10m"
test_patterns = ["go test", "pytest"]

[agents.queue]
max = 8
```

- `hold_plans` also hands a plan an agent in plan mode asks to have approved
  to the Inbox, for the harnesses `enabled` names. Unset is true.
- `[agents.approvals.risk]` marks an approval risky when its command matches a
  rule: `builtin` keeps the shipped rules (default true), each `rule` adds one
  with a name, the tools it applies to (empty for every tool) and an RE2
  `pattern`. A rule with no name or a pattern that does not compile is
  ignored, with a warning. `panes_may_allow` lets a pane holding the `respond`
  grant allow a risky call; it is off, so only you can.
- `[agents.recap]` is the summary of what an agent did while you were away:
  `mode` says where it is shown (`toast` in the dock when you come back to the
  pane, and in the Inbox; `inbox` only in the Inbox; `off` only in
  `agent-log`), `away` how long you must have been away for the dock to show
  it, and `test_patterns` which commands count as a test run. The daemon
  reads `test_patterns` for `tuios agent-log --recap` and the
  `agent-activity` verb, and picks up a change when the file is saved.
- `[agents.queue]` bounds the messages waiting to be typed to one agent when
  it comes to rest: `max`, 8 by default, at most 64.

One rail option goes with them: `appearance.sidebar.agent_rest_fold`, how long
an agent row rests (idle, unknown, or done and already seen) before the rail
folds it into one line, as a duration such as `1h` (the default) or `off`. The
settings page shows it on its Sidebar tab once an agent has been seen.

The agent row's `$name` tokens in `[appearance.sidebar.agent_row]` can place
the metadata keys tuios now feeds: `$model`, `$context`, `$cost` and `$plan`.
They come from Claude Code's status line once `tuios integration install
claude-code --statusline` is installed, from the opencode and Kilo plugin, and
from protocol panes, and a key the harness never states draws nothing (see
[Agent metadata](AGENT_STATE.md#what-feeds-it)). No option is needed to turn
the feeds on, and there is nothing to configure for them.

## What a pane may do

Every pane holds grants that say what a process in it may do through tuios:
`read` (its own session and fan group), `write` (type into its own session,
into panes that hold nothing it does not), `fan` (write in its fan group and
start agents), `respond` (answer prompts without you, and type into a pane
waiting on one) and `admin` (everything else, as before grants). A pane started
with `--grants` (`tuios start-agent`, `fan`, `new-window`) or given grants
with `tuios set-pane-grants` holds those. Every other pane holds the default
this table sets:

```toml
[agents.permissions]
mode = "strict"
grants = ["read", "write", "fan"]
```

`mode` is `open`, the default, or `strict`. Under `open` a pane holds `admin`,
so every script in a pane works as it always has. Under `strict` it holds
`grants`, which is `read`, `write` and `fan` when unset; an empty list gives
nothing but the right to report about itself. Any other `mode` is read as
`strict`, and an unknown grant is dropped: both are reported as config
warnings, and both fail toward less. `admin` never includes `respond`, and no
default gives it, so listing `respond` here is how you let every pane answer
prompts for you.

The daemon reads the table when it starts and again when the file changes; a
change reaches every pane on the default at its next call. Like
`[agents.approvals]`, it is not in `list-options` and `set-option` cannot
change it, so no pane can loosen it.
[AGENT_STATE.md](AGENT_STATE.md#what-a-pane-may-do) has the whole model.

## What another machine may do here

A `[hosts.NAME]` table names a machine this one links to. Read on the machine
a link arrives at, the same table also says what the machine of that name may
do there:

```toml
[hosts.laptop]
addr = "laptop"                  # optional: without it nothing is dialled
allow = ["list", "mail", "open", "write", "respond"]
hold_mail = true
hosted_grace = "10m"

[hosts."*"]                      # every machine with no table of its own
allow = ["list", "mail"]
```

`allow` is the capabilities, and anything not in it is refused with
`forbidden` and nothing done:

| Capability | What it lets the other machine do here |
| --- | --- |
| `list` | Read: sessions, windows, captures, screenshots, agent state, the Inbox, prompts, waits and the event stream. |
| `mail` | Send and read agent mail, and use the stash. |
| `open` | Start processes: sessions, windows, worktrees, fans, `start-agent`, clones of a repository by its URL, and panes this machine runs for it. |
| `write` | Change what is here: type into panes, `run` a line at a prompt, close and move windows, set options, layouts and names, report agent state, and attach. Also read a worktree's work out with `bundle-worktree` (`tuios worktree pull`), since a machine that may type into a shell here can read those files already. |
| `respond` | Answer for the person: prompts, held approvals, `ask-human` questions, dismissing Inbox items, and passing on held mail. |

With no table, a machine may `list`, `mail`, `open` and `write`, which is what
every link could do before the policy existed. `respond` is opt-in. Relaying on
to this machine's own hosts needs all five, because the next machine sees the
relay as coming from this one. An `allow` that is set replaces the inherited
list; `allow = []` allows nothing but `hello`.

`hold_mail` holds mail from that machine to any agent here in your Inbox,
marked `held for NAME`, until you pass it on with `p` there (or
`release-agent-message`). Mail to you is yours already and is not held.

`hosted_grace` is how long a pane this machine runs for the other one (a
window opened with `--host` there) outlives a dropped link, still running and
keeping its last 64 KB of output, waiting to be reattached: a Go duration,
`"0"` to end it with the link as before, 10 minutes when unset, at most 24
hours. See [Limits](SESSIONS.md#limits).

Each field inherits from `[hosts."*"]`, which inherits from the default. The
name is matched without regard to case. It is the one the other machine gives
for itself, its host name up to the first dot, unless the ssh key it logs in
with pins one:

```
command="tuios stdio-proxy --as laptop",restrict ssh-ed25519 AAAA...
```

Only a pinned name is a boundary: a key that may run any command can run a
shell, and can claim any name. A table with a policy and no `addr` is not
dialled and not listed by `tuios hosts`, and `[hosts."*"]` never is.

The daemon follows the file, and a change applies to the next call on every
link, including links already open. `tuios hosts add` on a known name keeps
these fields. [protocol.md](protocol.md#what-a-linked-machine-may-do-here) has
the verb by verb table.
