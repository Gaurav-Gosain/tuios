# Configuration

The configuration reference lives on the docs site: https://tuios.dev/docs/configuration

It covers the whole `config.toml`: the `[appearance]` table and its `sidebar`, `scrollbar`, dock, and window-button options, `[notifications.agent]`, all 19 `[keybindings]` sections, `[daemon]`, `[startup]`, `[tape]`, `[screenshot]`, `[hooks]`, and `[debug]`, along with what hot-reloads and what needs a restart.

`tuios list-options` describes every settable path with its type, default, and accepted values, straight from the registry the validator uses. The in-app settings page (`Ctrl+B ,`) edits and persists the same options, and its rows are derived from that same registry: an option an agent can set is an option a person can reach, and a test fails the build if one is not.

## The pane background

`appearance.pane_background` decides what is behind a pane's content where the
program running in it left the default background:

| Value | What is painted |
|---|---|
| `off` (default) | Nothing. The cell is transparent and your terminal's own background shows through. |
| `theme` | The active theme's background, with the theme's foreground on text left in the default colour. With no theme set there is no theme background, so it behaves as `off`. |
| `#RRGGBB` | That colour. With a theme set, default-coloured text takes the theme's foreground, lifted until it reads on the colour; with none, it keeps your terminal's own. |

```toml
[appearance]
pane_background = "theme"
```

A background the program chose for a cell always wins, and so do the marks
tuios paints over a pane: the selection, search matches and the copy mode
cursor. Only the pane's content area is painted. The border, the title bar, the
gap between panes, the rail and the dock stay on your terminal's background,
because they are chrome rather than the ground a program draws on.

It hot-reloads, it is on the Appearance tab of the settings page (a colour row
that opens the same picker as the border colours, with `off` and `theme` offered
beside the grid), and `tuios set-config appearance.pane_background <value>` sets
it from a script. It reaches every client of the session, SSH and browser ones
included, since they draw the same frame. A screenshot of one pane is drawn on
the painted colour.

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
