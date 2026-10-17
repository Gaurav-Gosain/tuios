# Dock components

The dock is three ordered lists of named components. A component is either a
built-in or a command of yours whose first line of stdout becomes a cell.

There is nothing to install. Sharing a component is copying a script and a
five-line table into your config; that is the whole distribution story, and it
is why there is no index, no manifest and no version to break.

## The shortest possible one

```toml
[dock]
right = ["custom/branch", "cpu", "ram", "session-controls"]

[dock.custom.branch]
command = "git branch --show-current 2>/dev/null"
refresh = "event:after-focus-change"
```

`tuios list-dock-components` says whether it drew and, if it did not, why.

## The lists

```toml
[dock]
left   = ["mode", "workspaces", "trail", "tape"]
center = ["windows"]
right  = ["notifications", "copy-help", "cpu", "ram", "clock", "session-controls"]
```

That is the default. Omit the whole `[dock]` table and you get exactly this;
omit a name and that segment is not drawn; reorder the names and the bar
reorders. A list you write as `[]` draws nothing on that side, which is
different from leaving the key out.

The built-ins are `mode`, `workspaces`, `trail`, `tape`, `windows`,
`notifications`, `copy-help`, `cpu`, `ram`, `clock`, `session-controls`,
`transfers`, `session`. Each
keeps its own condition: naming `cpu` makes it eligible to draw, `show_cpu`
still says whether it is on.

Two of them are not really cells. `notifications` and `copy-help` take the
right-hand end for as long as they have something to say, wherever they sit in
the list, because they are transient claims rather than segments.
`session-controls` always holds the bar's right-hand end and is never truncated.

## The session cell and cell colours

`session` shows the session name, the current workspace and the focused pane's
place in it, `main 2:1`. It is in no default list; add it where you want it.
`[dock.session]` sets its text and look:

```toml
[dock]
left = ["mode", "workspaces", "session"]

[dock.session]
format    = "{session} {workspace}:{pane}"  # the default
fg        = "#272e33"
bg        = "#7fbbb3"
bold      = true
cap_left  = "\ue0b6"                     # drawn in bg, round the cell's ends
cap_right = "\ue0b4"
```

`fg`, `bg`, `bold`, `cap_left` and `cap_right` work the same way in a
`[dock.custom.NAME]` table. Colours take hex, an ANSI number, a slot name such
as `bright_magenta`, a role such as `accent`, or a CharmTone name. See "Colour
values" in docs/CONFIGURATION.md. Anything else is warned about and ignored. With none of them set a cell draws as it always has.

## The clock

```toml
[dock]
right = ["clock", "session-controls"]

[dock.clock]
format = "15:04"
```

The format is a Go time layout. It also sets the cadence: a layout showing
seconds refreshes once a second, one without refreshes on the minute. The same
format drives the status badge that `show_clock` turns on.

## Workspace tabs

```toml
[dock.tabs]
style         = "flat"   # pill (default) | flat
separator     = " • "
active_bg     = "#a7c080"
active_bold   = true
number_glyphs = ["I", "II", "III"]   # the first entry is workspace 1
zoom_marker   = " [ ]"
```

`style = "flat"` draws the `workspaces` component as a tmux-like window list
instead of pills. `number_glyphs` and `zoom_marker` feed the `{icon}` and
`{zoom}` placeholders of `appearance.dock_workspace_tab_format`. With no
colours set, the current tab sits on the theme's accent. `attention_fg`,
`bell_marker` and `activity_marker` mark a workspace out of view whose pane
waits for you, rang the bell or printed. Attached to a daemon, the daemon
reports the first bell and the first output of such a pane, once. The full
key list is in
`docs/CONFIGURATION.md`.

## Writing a component

```toml
[dock.custom.NAME]
command   = "..."     # run through sh -c; first line of stdout is the cell
refresh   = "once"    # once | a duration like "30s" | push | event:TYPE
on-click  = "..."     # optional; run like a hook when the cell is clicked
max-width = 24        # optional; the cell is truncated to this
```

Place it by putting `custom/NAME` in one of the lists.

Your command gets the session's environment plus `TUIOS_DOCK_COMPONENT`,
`TUIOS_SESSION` and `TUIOS_SOCKET`. An `on-click` command also gets
`TUIOS_CLICK_BUTTON`. That is the entire contract: environment in, one line of
text out. There is no API behind it, so there is nothing a tuios release can
break.

Colour works. SGR escapes survive; every other control sequence is stripped,
because a dock cell that can move the cursor is a dock cell that can redraw
somebody else's screen.

### Choosing a refresh

In order of preference, because the order is also the order of cost:

- **`event:TYPE`** re-runs when something happens. Free at idle: events only
  arrive when something happened, and something happening is when the bar was
  going to redraw anyway. The types are the hook events
  (`after-focus-change`, `after-agent-state`, `after-workspace-switch`,
  `after-new-window`, `after-close-window`, `after-layout-change`,
  `after-attach`, `after-detach`, `after-resize`) and other names for them.
  `window-focused`, `agent-state`, `workspace-switched`, `window-created` and
  `window-closed` are the names `tuios subscribe` uses. `layout-changed`,
  `attached`, `detached` and `resized` are names only the dock knows: the
  daemon sends no event by those names.
  Several at once: `refresh = "event:after-focus-change,after-new-window"`.
- **`push`** keeps your command running and takes each line it writes as an
  update. Bring your own `inotifywait`, `upower --monitor`, or a loop around
  `tuios wait-for`.
  Wakes are driven by the pipe, never by a clock.
- **`"30s"`** polls. The floor is one second. One timer is armed for the
  earliest deadline across every polling component, and a value that has not
  moved draws no frame, so a one-second cell watching something that changes
  every five minutes costs sixty executions an hour and about zero renders.
- **`once`** (the default) runs at startup and then only when you ask, with
  `tuios refresh-dock NAME`. That verb is what makes a component scriptable:

  ```toml
  [hooks]
  after-agent-state = "tuios refresh-dock agents"
  ```

A dock with no polling component arms no timer at all, which is why adding
components costs an idle session nothing.

## When it breaks

A component that fails, times out, or prints nothing is **hidden**. The bar
never breaks, and a cell never keeps showing a value its command can no longer
produce, because a stale cell is confidently wrong.

You are told once per failure streak, as a dock message and a log line, and the
detail is always available:

```console
$ tuios list-dock-components
╭─────────────────┬───────┬─────────┬──────────────────────────┬────────┬──────────────────────╮
│ COMPONENT       │ SIDE  │ SOURCE  │ REFRESH                  │ STATE  │ READS                │
├─────────────────┼───────┼─────────┼──────────────────────────┼────────┼──────────────────────┤
│ mode            │ left  │ builtin │ render                   │ drawn  │                      │
│ custom/branch   │ left  │ custom  │ event:after-focus-change │ drawn  │ feat/dock-components │
│ custom/k8s      │ right │ custom  │ interval 30s             │ failed │ exit 127: ...        │
╰─────────────────┴───────┴─────────┴──────────────────────────┴────────┴──────────────────────╯
```

Fix the script, then `tuios refresh-dock k8s`. A component that failed five
times in a row stops being polled on its own schedule; an explicit refresh
revives it, so you never have to restart the session.

Limits, so a component someone else wrote cannot take the session with it: a
three second timeout, bounded reads, and only the first line is used.

## Where a component runs

It runs where the client runs, because the bar is composed in the client.

- Local session: your machine. What you expect.
- `tuios-web`: the machine running tuios-web, not the browser.
- SSH: the SSH host, not your terminal.

So a battery cell over SSH reports the server's battery, which servers do not
have. tuios says so in the config warnings when it can see that is your
situation. Every attached client runs its own copy, the way waybar runs one per
monitor.

Anything that has to happen while nothing is attached is a hook, not a
component.

## The rail's custom section

The same contract draws a section of the rail. Put `custom` in
`appearance.sidebar.sections` and write the table:

```toml
[appearance.sidebar]
sections = "sessions:25,terminals,custom:35,agents:30"

[appearance.sidebar.custom]
title   = "Brief"
command = "~/.config/tuios/dock/brief.sh"
refresh = "event:window-focused,agent-state"
```

Three differences from a dock cell. Every line of stdout is a row, cut to the
rail's width and to the section's lines. There is no `push`: a command that
stays running keeps the environment it started with, and the rows are about
the focused pane, so events re-run the command with fresh values instead,
and an event that lands mid-run costs one more run after it. And the
environment says where the rows go: `TUIOS_RAIL_SECTION=custom`,
`TUIOS_RAIL_WIDTH`, `TUIOS_RAIL_HEIGHT` (a ceiling), `TUIOS_ACTIVE_PANE_ID`
and `TUIOS_ACTIVE_PANE_CWD`, beside `TUIOS_SESSION` and `TUIOS_SOCKET`.
The command does not run while the rail is hidden or folded, so
`TUIOS_RAIL_WIDTH` is never `0`, and it runs again when the rail opens.

Everything else is the dock's: the timeout, the bounded read, the sanitiser,
the empty section on failure, the five-failure stop, and
`tuios list-dock-components`, which lists it as `rail/custom` on side `rail`.
`tuios refresh-dock rail/custom` re-runs it. The table is read from the
config file only; `set-config` can place the section and cannot set its
command.

## The recipes

| file | what it is | refresh |
|---|---|---|
| [`git-branch.sh`](git-branch.sh) | branch of the focused pane's directory, dirty marker | `event:after-focus-change` |
| [`battery.sh`](battery.sh) | charge and charging state, coloured under 20% | `60s` |
| [`kube-context.sh`](kube-context.sh) | current kubectl context and namespace | `30s` |
| [`agents.sh`](agents.sh) | how many panes are working, waiting, or done | `event:after-agent-state` |
| [`inbox.sh`](inbox.sh) | unread count, pushed rather than polled | `push` |

[`dock.toml`](dock.toml) is all five wired up, ready to paste.

Copy the scripts wherever you like, `chmod +x` them, and point `command` at
them.
