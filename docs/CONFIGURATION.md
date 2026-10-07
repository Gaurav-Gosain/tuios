# Configuration

## Sidebar file editor

Settings, Sidebar, File editor sets `appearance.sidebar.editor`, the terminal
editor command used by `Shift+Enter` on a text file. For example:

```toml
[appearance.sidebar]
editor = "micro"
```

Other terminal editors and arguments work too, such as `nano` or `vim -f`.
Quote a path or an argument that contains spaces. tuios splits the command into
arguments and runs no shell. It adds the full path of the file at the end. If
the value is empty, tuios uses `$EDITOR`, then `$VISUAL`, then `vi`. File links
and hints open files with the same command. tuios reads the first 512 bytes of
a file to check that it is text. It refuses binary files and special files.

The configuration reference lives on the docs site: https://tuios.dev/docs/configuration

It covers the whole `config.toml`: the `[appearance]` table and its `sidebar`, `scrollbar`, dock, and window-button options, `[notifications.agent]`, `[notifications.mail]` (see [AGENT_STATE.md](AGENT_STATE.md#the-inbox)), all 23 `[keybindings]` sections, `[daemon]`, `[startup]`, `[tape]`, `[screenshot]`, `[hooks]`, and `[debug]`, along with what hot-reloads and what needs a restart.

`[notifications.mail]` has `enabled`, `notify`, `dock`, `sound` and `between_agents`. Each key except `between_agents` follows the same key in `[notifications.agent]` while it is unset, so a config without the table alerts as before. `between_agents` (default `false`) also alerts on a message from one agent to another. Sound mode, cooldown, cue files and quiet hours always come from `[notifications.agent]`. `tuios set-config notifications.mail.KEY ""` clears a key, and `tuios get-config` then prints `(follows notifications.agent.KEY)`.

`tuios list-options` describes every settable path with its type, default, and accepted values, straight from the registry the validator uses. The in-app settings page (`Ctrl+B ,`) edits and persists the same options, and its rows are derived from that same registry: an option an agent can set is an option a person can reach, and a test fails the build if one is not.

## Split the config into several files

You can keep part of the config in other files. Use this to share one
config.toml between machines and keep the settings for one machine, such as
its hosts, in a separate file. A Nix or home-manager setup can also put its
settings in a file that it manages, and leave config.toml to tuios.

There are two ways to add files. Use one or both.

1. Put an `include` list at the top of config.toml, before the first table:

   ```toml
   include = ["hosts.toml", "~/.config/tuios/local.toml", "?work.toml"]
   ```

   A relative path is relative to the file that has the `include` line. `~`
   is your home directory. A name that starts with `?` is optional: when the
   file is not there, tuios says nothing. An included file can have its own
   `include` list.

   On Windows, write an include path with forward slashes, or put it in a
   single-quoted literal string. In a double-quoted TOML string, a backslash
   starts an escape:

   ```toml
   include = ["C:/Users/me/tuios/local.toml", 'C:\Users\me\tuios\work.toml']
   ```

2. Put `*.toml` files in a `config.d` directory next to config.toml. tuios
   reads them in name order, so `10-theme.toml` comes before `50-hosts.toml`.

### Which value wins

tuios reads the files in this order. A later file wins over an earlier file.

1. The files in the `include` list, in list order. An included file's own
   includes come before it.
2. The files in `config.d`, in name order.
3. config.toml.

config.toml wins, and it holds only what you changed. On the first start,
tuios writes a config.toml with comments and no settings. The one exception
is `[startup]`: tuios writes `tiled = true` and `daemon = true` there, unless
another file sets them. Each change from tuios adds or changes only the key
you changed. So an included file applies everywhere except where you chose a
value of your own.

A config.toml from an older tuios sets every key, and so it hides every other
file. Run `tuios config prune` to remove the keys that have their default
value. When another file sets one of those keys, its value then applies. The
command lists those keys and asks before it changes the file. Use
`tuios config prune --dry-run` to see the keys and `--yes` to skip the
question. A run with no terminal needs `--yes`.

The files merge with these rules:

- Tables merge key by key, at every depth. `[hosts.NAME]` tables merge by
  name, so each file can add its own hosts.
- For a single value, the later file wins.
- An array of values replaces the earlier array. It does not add to it.
- An array of tables, such as `[[keybindings.command]]`, merges entry by entry.
  It is never replaced whole. A later entry matches an earlier entry by `name`
  when both have a name, and by `key` when they do not. A matched entry merges
  key by key. An entry that matches nothing goes at the end.
- To remove an entry that an earlier file sets, add an entry with the same
  `name` or `key` and `disabled = true`:

  ```toml
  [[keybindings.command]]
  name = "deploy"
  disabled = true
  ```

A relative file path in an included file, such as a `[plugins] dirs` entry or
a `token_file`, is relative to that file.

These are not errors:

- An include that names a file that does not exist. tuios shows a warning and
  skips it, so one machine can include a file that only it has.
- An include cycle, or a file that includes itself. tuios shows a warning and
  reads each file once.
- An optional include or a `config.d` file that tuios cannot read, such as a
  link that points to itself. tuios shows a warning and skips it. A required
  include that tuios cannot read stops the load.
- An `include` key below a table header. TOML puts it in that table, so it
  includes nothing. tuios shows a warning.

An include that names a directory stops the load. Put a directory of files in
`config.d`. A file that exists and has a TOML error also stops the load, the
same as an error in config.toml.

### Find where a key comes from

```sh
tuios config files                     # every file, in merge order
tuios config origin                    # every key and the file that sets it
tuios config origin appearance.theme   # one key
```

`tuios config origin` also names the earlier files that set the same key. Their
values lose.

### Hot reload

tuios watches every file it reads: config.toml, each included file, each file
in `config.d`, and the `config.d` directory itself. When a file is a link,
tuios also watches the file that the link points to. A save to any of them
takes effect at once. An included file that was missing applies when you make
it, if its directory exists.

### Where tuios writes a change

The settings page, `tuios set-config`, the keybind manager,
`tuios keybinds unbind`, `tuios hosts add` and `tuios plugins` write the config.
They write only the keys you changed, and never copy the other files into
config.toml. A change made while a file changed on disk keeps the new values
of that file. Each change goes to one file:

- A key that a file sets goes to the last file that sets it.
- A new entry in a table of entries, such as a new `[hosts.NAME]`, goes to the
  last writable file that has entries in that table.
- Any other new key goes to config.toml.
- A removed key is removed from each file that sets it.

tuios changes only the lines of the key, so the comments, the blank lines and
the line endings of the file stay. A comment above a table stays with that
table. tuios never writes a file you wrote again from its values. When it
cannot express a change in the lines of an included file, it writes the change
to config.toml and tells you. When it cannot remove a key that way, the save
fails and the message names the file. Only config.toml itself is written again
whole in that case, without its comments.

tuios never writes a read-only file. This includes a file that Nix or
home-manager links in from the store. When a read-only file sets the key,
tuios writes the change to config.toml and tells you. When config.toml is
read-only too, tuios writes the change to the last writable file that config.toml
includes. The change must go to a file that comes after every file that sets
the key. If there is no such file, the save fails and the message names the
file to change. tuios cannot remove a key from a read-only file. It removes an
array entry with a `disabled = true` entry in a writable file. When an entry
that tuios writes must not take keys back from an earlier file's entry, tuios
writes a `disabled = true` entry first and the new entry after it.

`tuios config reset` writes the first-start config.toml again, with your
`include` list, and lists the other files that still apply. When config.toml
has an error, the reset still keeps an `include` list that it can read.

## Opening links

tuios finds two kinds of link in a pane. An OSC 8 hyperlink is a link that a
program marked, such as `ls --hyperlink`, `gcc`, `delta`, `gh` or an agent
CLI. Its text and its target can be different. A bare URL is plain text that
starts with `http://`, `https://`, `ftp://`, `ftps://`, `file://`, `ssh://` or
`git://`. Hints mode and the pointer use the same URL detector.

```toml
[appearance]
links = "all"          # off, marked (OSC 8 only), all
link_click = "both"    # both, ctrl, shift, off
link_opener = ""       # for example "firefox --new-tab" or "open -a Safari %s"
```

| Key | Default | What it does |
|---|---|---|
| `links` | `all` | The links that tuios finds. `marked` finds only OSC 8 links. |
| `link_click` | `both` | The click that opens a link. `both` is `Ctrl+click` and `Shift+click`. `Ctrl+click` also works on macOS, in Ghostty, kitty, WezTerm and Terminal.app. `Cmd+click` does not open a link there. |
| `link_opener` | empty | The command that opens a web link. tuios puts the URL where `%s` is, or at the end. If this is empty, tuios uses `$BROWSER`, then the system opener. |

The system opener is `open` on macOS, `rundll32` on Windows, `wslview` under
WSL, and `xdg-open` on other systems. tuios runs no shell. It gives the URL to
the opener as one argument. If the opener fails, tuios shows a message and
puts the URL on your clipboard.

### Which machine opens the link

- A local client opens the link on your machine.
- Under `ssh`, tuios does not use the system opener, because it would open the
  browser on the remote machine. tuios puts the URL on your clipboard. Set
  `$BROWSER` or `link_opener` to a command that forwards the URL, if you have
  one.
- A remote client (`tuios ssh`, the web client) puts the URL on your
  clipboard.
- A Linux machine with no `DISPLAY` and no `WAYLAND_DISPLAY` has no desktop.
  tuios puts the URL on your clipboard.

In all these cases, tuios also sends each link to your terminal as OSC 8. Use
the link click of your terminal to open it on your machine.

### Safety

- Only `http`, `https`, `mailto`, `ftp` and `ftps` links go to the opener.
  tuios copies other links, such as `javascript:`, `data:` or the scheme of an
  application.
- A `file://` link opens in an editor pane only when the file is on this
  machine. A `file://` link with the name of another host does not open.
- tuios refuses an address with control characters or spaces.
- The hover label shows the target. After a click, the message names the host.
  tuios does not ask before it opens a link. The label and the scheme list
  give the protection, and a prompt on each click teaches people to accept it.

### OSC 8 in the output of tuios

tuios draws each pane into its own frame, so your terminal sees the output of
tuios and not the output of the program. tuios writes every link in the
focused pane as OSC 8. An OSC 8 link keeps its URI and its `id=`. A bare URL
gets an `id=` from tuios, so a URL that wraps to the next row is one link.
Your terminal can then show and open the real target. In a pane that is not
focused, tuios writes the OSC 8 links of the program. Your terminal finds the
bare URLs itself.

### Terminals

A terminal sends a click to tuios only when the terminal does not use the
click itself. Most terminals keep `Shift+click` for their own selection when
a program reads the mouse. That is why `Ctrl+click` is the main click.

| Terminal | `Shift+click` gets to tuios | `Ctrl+click` gets to tuios | The terminal opens OSC 8 links |
|---|---|---|---|
| kitty | No. kitty opens the URL under the pointer. | Yes | Yes, with `Shift+click` or `Ctrl+Shift+click` |
| Ghostty | No. Ghostty extends its selection. | Yes. On a link, Ghostty opens it. | Yes, with `Ctrl+click` (Linux) or `Cmd+click` (macOS) |
| WezTerm | No. `Shift` bypasses mouse reporting. | Yes | Yes |
| Alacritty | No. Alacritty opens a hinted URL. | Yes | Yes, with `Shift+click` |
| foot | No. `Shift` bypasses mouse reporting. | Yes | Yes, in URL mode (`Ctrl+Shift+O`) |
| iTerm2 | Yes | Yes | Yes, with `Cmd+click` |
| Windows Terminal | No. `Shift` bypasses mouse mode. | Yes | Yes, with `Ctrl+click` when it gets the click |
| xterm | No. `Shift` bypasses mouse reporting. | Yes | No |

`Cmd` is not part of the mouse protocol, so tuios never sees `Cmd+click`. On
macOS, `Cmd+click` opens a link through the terminal, from the OSC 8 that
tuios writes. In Ghostty, a program can ask for `Shift+click` with
`XTSHIFTESCAPE`, or you can set `mouse-shift-capture = true`. tuios does not
ask, because that takes `Shift` selection away from you.

## Neovim pane navigation

`appearance.nvim_navigation` is `false` by default. Set it to `true` to enable
pane navigation through the optional
[tuios-nvim-navigator](https://github.com/Tim4c/tuios-nvim-navigator) plugin:

```toml
[appearance]
nvim_navigation = true
```

See [KEYBINDINGS.md](KEYBINDINGS.md#neovim-pane-navigation) for the matching
focus-key mappings and protocol behaviour.

`persist_scrollback` in `[daemon]` (default `true`) saves each pane's history with its session. After a daemon restart or a reboot, the restored pane shows that history above a dim divider, and the new shell starts under it. `persist_scrollback_lines` (default 1000) and `persist_scrollback_kb` (default 2048) set the most lines and the most KiB one pane saves. The files hold what your panes printed, secrets included. Set `persist_scrollback = false` to stop this. The next time the daemon starts, it deletes the history it saved before. See [SESSIONS.md](SESSIONS.md#pane-history).

`window_size` in `[daemon]` sets the size of a session that has more than one client. The names are the names of the tmux `window-size` option. See [SESSIONS.md](SESSIONS.md#session-size-with-more-than-one-client).

| Value | Size of the session |
|---|---|
| `smallest` (default) | The smallest client. Every client shows the full session. |
| `largest` | The largest client. A smaller client shows a part of the session. |
| `latest` | The client that last had input. A smaller client shows a part of the session. |

To change the value of one session while it runs, use `tuios set-config daemon.window_size latest -s NAME`. The value is not saved to the config file. A change in the config file applies when the daemon starts again.

`single_client` in `[daemon]` (default `false`) keeps one client for each session. When a client attaches, every other client of that session detaches. The newest attach wins. Each detached client exits with status 0 and prints "Another client attached to this session." The session continues to run. This is `tuios attach -d` on every attach. A change in the config file applies to the next attach.

```toml
[daemon]
single_client = true
```

`[hints]` sets what hints mode (`Ctrl+B F`) labels. See [HINTS.md](HINTS.md).

`[panes]` sets `label_keys`, the keys that the pane labels (`Ctrl+B Q`) use. The default is `1234567890`. Letters `a` to `z` and digits are allowed. See [KEYBINDINGS.md](KEYBINDINGS.md#pane-labels).

`[panes]` also sets `navigator_layout`, the layout the pane navigator (`Ctrl+B /`) opens in: `tree`, `flat` or `cards`. The default is `tree`. Press `v` in the navigator to change the layout. See [KEYBINDINGS.md](KEYBINDINGS.md#the-pane-navigator).

`[scratch]` sets the size of the box in which `Ctrl+B g` shows the scratch terminal. The old `session` key is no longer used. See [SESSIONS.md](SESSIONS.md#the-scratch-terminal).

`[pip]` sets the size (`width`, `height`, border included, default 40x12) and the first `corner` (default `bottom-right`) of the picture-in-picture view that `p` pins. See [SESSIONS.md](SESSIONS.md#picture-in-picture).

`[[keybindings.command]]` binds a key to a command that you write: a scratch terminal, a popup, a pane or a command with no window. A mistake in an entry is a warning, and tuios ignores that entry. See [KEYBINDINGS.md](KEYBINDINGS.md#command-keys).

## Frame rate

`appearance.max_fps` is the highest frame rate tuios draws at. Your terminal
and your monitor can show fewer frames. A value above what they can show does
not make tuios smoother.

```toml
[appearance]
max_fps = 144     # a number from 10 to 240
# max_fps = "auto"  # the refresh rate of your display
# max_fps = 0       # 60, the default
```

| Value | Frame rate |
| --- | --- |
| `0` | 60. This is the default. |
| `10` to `240` | That number. A value outside the range moves to the nearest end. |
| `"auto"` | The refresh rate of your display, from 10 to 240. If tuios cannot find the rate, it uses 60. |

The settings page offers Auto, 30, 60, 90, 120, 144, 165 and 240. With Auto
selected, the row shows the rate in use, for example `Auto (144)`. A change
applies at once, with no restart.

**What limits the frames you see.** tuios sends a frame to your terminal. The
terminal then draws the frame on its own schedule. kitty and ghostty draw at
most once for each refresh of the monitor, so a 60 Hz monitor shows at most 60
frames a second, whatever max_fps is. Over SSH, the network and the remote
terminal set the limit too.

**What it costs.** tuios draws only when something on the screen changes.
The terminal library under it still wakes at max_fps while tuios is idle. An
idle tuios at 240 wakes about three times as often as at 60. See
[perf.md](perf.md) for the measured numbers.

**How auto finds the rate.** tuios looks once, in the background, when it
starts. It looks again when the config file is reloaded. It never delays the
first frame. Until the answer arrives, tuios draws at 60.

- Linux on Wayland: tuios asks `hyprctl` on Hyprland, `niri` on niri, and
  `wlr-randr` on other wlroots compositors. It reads the current mode of each
  output that is on.
- Linux on X11, or on a Wayland compositor that none of these tools speaks
  for: tuios asks `xrandr --current`. Under XWayland the answer comes from the
  compositor and can be less exact.
- macOS: tuios asks `system_profiler SPDisplaysDataType`. Some built-in
  displays do not report a rate. tuios then uses 60.
- Windows: tuios does not look, and uses 60.

A terminal window cannot tell which display it is on. With several displays,
auto uses the highest rate among them.

Auto looks only when tuios runs on the same machine as your desktop. Over SSH,
in tuios-web and in the SSH server, auto uses 60, because the displays on the
machine are not the ones you see.

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
its own colours with OSC 10 or 11 gets its own back. With a theme on and the
pane background off, the answers are the theme's colours. With no theme and the
pane background off, the answers are the host terminal's own: tuios asks the
terminal it runs in for its background, its text colour and its sixteen ANSI
colours when it starts or attaches, and tells panes those, so a program on a
light terminal picks its light palette. OSC 4 queries for the sixteen are
answered the same way. Each client asks its own terminal, so an SSH or browser
client on a light terminal and a local one on a dark terminal each get the
right answer for the session while they are the one typing. In a daemon
session the daemon's emulator answers, and the client tells it what to answer
when it syncs; with several clients attached, the last one to sync decides.

With no theme the chrome is drawn for the host's background too: on a light
terminal the rail, the dock and the unfocused pane borders use the light
chrome ramp a light theme gets, measured against the terminal's own colour.

**Following light and dark.** A local client turns on mode 2031, and a terminal
that supports it (ghostty, kitty, contour and others) then tells tuios when the
system appearance switches between light and dark. tuios asks for the colours
again, tells panes, and redraws the chrome for the new background. The light
and dark verdict has hysteresis: a dark verdict turns light only above an 8-bit
luminance of 140, and a light one turns dark only below 110, so a mid grey
background does not flip the chrome back and forth. SSH and browser clients
ask once when they attach and do not follow the switch, because only the local
client turns the mode off again on exit.

**Terminals that do not answer.** A terminal that answers no colour query,
such as mosh, leaves everything as it was: panes are told the emulator's
defaults (a black background and white text) and the chrome uses its dark ramp.
Set a theme, or `pane_background`, to give panes a real answer there.

All six hot-reload and are on the **Backgrounds** tab of the settings page
(`Ctrl+B ,`, then `]`): an All surfaces row and one row per surface, each a
colour row that opens the same picker as the border colours, with `off` and
`theme` beside the grid and `x` to clear a surface back to following All
surfaces. `tuios set-config appearance.dock_background <value>` sets one from a
script. They reach every client of the session, SSH and browser ones included,
since they draw the same frame. A screenshot of one pane is drawn on the pane's
painted colour, and a screen or region capture carries every painted surface
as it is drawn.

## Graphical programs from the launcher

The launcher starts a desktop entry in a new pane. A graphical program opens
its own window, so its pane stays empty. Set `launcher.gui_command` to start
such an entry with another command. The launcher adds the entry's argv to that
command. Entries with `Terminal=true` and programs from `$PATH` still open in
a pane.

```toml
[launcher]
gui_command = "tuios-wayland launch --"   # or "niri msg action spawn --"
```

Use only a command that takes an argv and runs it as an argv. Do not use a
command that joins its arguments into a shell line, such as `swaymsg exec --`
or `hyprctl dispatch exec`. With such a command, the text of a desktop entry
becomes shell code. Any installed package can write a desktop entry.

## When a workspace becomes empty

When the last pane on the workspace on screen closes, tuios shows the
workspace you came from. This happens when the pane's program stops, when you
close the pane, when `tuios xpanes -ss` closes it, and with
`tuios close-workspace`.

tuios goes back in this order:

1. For a workspace that `tuios xpanes` opened, the workspace where you ran
   `tuios xpanes`.
2. The workspace you showed last that still has panes. After a chain of
   empty workspaces, tuios goes back past each one.
3. The workspace with the lowest number that has panes.

When no workspace has panes, tuios shows the splash screen. The workspace on
screen is the same for every client of the session, so every client goes
back. Moving a pane to another workspace does not close it, so tuios does not
go back.

To stay on the empty workspace, turn the setting off:

```toml
[workspaces]
return_when_empty = false
```

The daemon reads the setting when it starts and when the file changes.
`tuios set-config workspaces.return_when_empty false` changes it while tuios
runs.

## When you switch to an empty workspace

By default, a workspace with no panes shows the splash screen. To open a
pane there when you switch to it, as the new-window key does, turn on this
setting:

```toml
[workspaces]
new_window_when_empty = true
```

The new pane starts in the folder of the pane that had focus on the
workspace you came from. The new pane starts in the session's start folder
in these cases:

- You came from a workspace with no pane.
- You came from a scratch terminal.
- The pane you came from runs on another machine.

The start folder is the folder that `tuios new --cwd` gave.

A pane opens only when you switch: with a workspace key, a click on the
dock, the workspace switcher, the pane navigator or the command palette.
These switches do not open a pane:

- `move_and_follow` and other moves that take a pane with them.
- `tuios xpanes`, `tuios select-workspace`, `tuios run-command` and tape
  scripts. These bring their own panes or run from a script.
- The switch back from an empty workspace that `return_when_empty` makes.
- The start of tuios and an attach to a session. The workspace on screen
  stays as it is.

When two clients show the same session, the client that switches opens the
pane. The other client shows it. A workspace gets one pane, also when two
clients switch to it at the same time. When you switch away before the pane
opens, you stay where you went, and a switch back does not open a second
pane.

With `appearance.new_window_follow_ssh = true`, a pane that runs ssh gives a
new pane that runs the same ssh, as the new-window key does.

The daemon must be this version of tuios or newer. With an older daemon, a
switch opens no pane. Run `tuios kill-server` and start tuios again to load
the new daemon.

Each client reads the setting from its config file, and a change applies at
once. The settings page has it under Daemon, and
`tuios set-config workspaces.new_window_when_empty true` changes it while
tuios runs.

## Master-stack layout

These options shape the master-stack layout. Each workspace starts with them.
The layout keys change the workspace on screen, and the workspace keeps the
change. See [LAYOUT_MODES.md](LAYOUT_MODES.md#master-stack-layout).

| Option | What it does | Values | Default |
|---|---|---|---|
| `appearance.master_position` | The side the master panes take. With `center`, the stack panes go to the right and the left in turn | `left`, `right`, `top`, `bottom`, `center` | `left` |
| `appearance.master_count` | How many panes are master panes | 1-9 | 1 |
| `appearance.master_ratio` | The share of the screen the master panes take, as a percent | 10-90 | 50 |
| `appearance.master_grid` | With one master on the left, show four or more panes as a grid | `true`, `false` | `true` |

This example puts an editor in the middle with terminals on both sides. It
keeps that layout at every pane count:

```toml
[startup]
layout = "master-stack"

[appearance]
master_position = "center"
master_ratio = 50
```

With `center`, the grid is off, so `master_grid` has no effect. The first pane
of the workspace is the master. To make another pane the master, focus it and
press `Ctrl+B L Enter`.

## Rounded dock pills

`appearance.dock_pill_caps` puts rounded caps on every pill in the dock: the
mode chip, the workspace tabs and the minimized windows. It is `true` by
default. Set it to `false` for flat pills:

```toml
[appearance]
dock_pill_caps = false
```

Under ASCII glyphs the workspace tabs have no caps. The rail's pills do not
change with this option.

## Mode pill icons

The mode pill at the start of the dock shows an icon for the mode. Three
options set the icons:

| Option | Mode |
| --- | --- |
| `appearance.dock_mode_icon_window` | Window mode |
| `appearance.dock_mode_icon_terminal` | Terminal mode |
| `appearance.dock_mode_icon_tiling` | Window mode and terminal mode while tiling is on |

```toml
[appearance]
dock_mode_icon_window = "WM"
dock_mode_icon_terminal = ">_"
dock_mode_icon_tiling = ""
```

- When an option is not set, the pill shows the icon of the glyph set. With
  `use_ascii_only`, that icon is `W`, `T` or `#`.
- An empty string shows no icon. If the mode has no other text, the dock shows
  no pill in that mode.
- An icon can be at most 8 cells wide. A wide character, such as `終`, counts
  as two cells. The dock does not accept control characters. For an icon that
  it does not accept, the dock shows the default icon and the config check
  shows a warning.
- The pill adds one space on each side of the icon. You do not need to add
  spaces.
- While tiling is on, the pill also shows the next split direction, `V` or
  `H`.

To remove the whole mode pill, remove `"mode"` from the `left` list in the
`[dock]` table. See [The dock's components](#the-docks-components).

You can also set the icons on the settings page, in the Dock section, or with
`tuios set-config dock_mode_icon_window WM`. The word `default` puts an icon
back to the icon of the glyph set, in the settings, with set-config and in
`config.toml`. The change applies at once.

## Workspace label cap

`appearance.dock_workspace_label_max` caps a workspace tab's label in cells,
so one long name cannot push the other pills off the bar. The cap is `12` by
default and counts the tab format's own characters, so `"<{name}>"` loses two
of them to the brackets. Set it to `0` to draw the whole name and let the
strip scroll instead:

```toml
[appearance]
dock_workspace_label_max = 0
```

A dock too narrow for even one pill keeps the current workspace's tab, cut to
whatever room is left, so the workspaces always have something to click. You
can also set it on the settings page, in the Appearance section. The change
applies at once.

## Compact dock

The dock has two rows by default: a rule and the row of pills.
`appearance.dock_compact` removes the rule, so the dock has one row. The panes
get that row. The default is `false`.

```toml
[appearance]
dock_compact = true
```

The option works with the dock at the top and at the bottom. It has no effect
when `dockbar_position` is `hidden`. You can also set it on the settings page,
in the Dock section, or with `tuios set-config dock_compact true`. The change
applies at once.

The rule shows how long a dock message stays. A compact dock does not show
this.

When clients with different docks share a session, the panes keep the
largest dock height. A client with a compact dock shows a blank row in that
case.

## Borderless zoom

`z` zooms the focused pane. By default the zoomed pane keeps its border, and
`appearance.zoom_size` sets how much of the screen it takes.
`appearance.zoom_borderless` shows the zoomed pane on the whole pane region
with no border and no title bar. The program in the pane gets the full size.
The dock and the rail stay on the screen. The default is `false`.

```toml
[appearance]
zoom_borderless = true
```

With this option on, `zoom_size` and `zoom_max_width` have no effect. The
option also applies to a floating pane. When you zoom out, the pane gets its
border back. You can also set it on the settings page, in the Advanced section,
or with `tuios set-config appearance.zoom_borderless true`. The change applies
at once.

## Splits that follow ssh

The actions `split_ssh_horizontal`, `split_ssh_vertical` and `new_window_ssh`
open a pane on the machine that the focused pane is connected to with ssh. See
[KEYBINDINGS.md](KEYBINDINGS.md#split-into-the-same-ssh).

`appearance.new_window_follow_ssh` makes the ordinary actions do the same:
`split_horizontal`, `split_vertical` and `new_window`. When the focused pane
does not run ssh, these actions open an ordinary pane. The default is `false`.

```toml
[appearance]
new_window_follow_ssh = true
```

tuios finds ssh in the foreground of the pane. ssh can run from a shell, or
from `sshpass`, `autossh` or `mosh`. tuios reads only the processes that you
own in the pane. It runs the `ssh` or `mosh` that it finds on its own `PATH`,
with no shell on this machine. It never runs the program that the pane runs.

The new ssh gets the same environment as an ordinary new pane. If the ssh
that tuios follows has an `SSH_AUTH_SOCK` that names your own agent socket,
the new ssh gets that value too. An agent that your shell started in the pane
then works in the split. The socket must not be a link, and its folder must
be yours and closed to other users.

The new ssh line keeps only what it needs to reach the same host as the same
user. It always gets `-o ControlMaster=no`. Your `~/.ssh/config` applies to it
as usual, so a setting that you put there still works in the split.

### Lines that are not followed

tuios follows an ssh line only when each `-o` option is on one of two lists.
The new pane keeps these options:

`AddressFamily`, `BatchMode`, `CertificateFile`, `CheckHostIP`, `Ciphers`,
`Compression`, `ConnectionAttempts`, `ConnectTimeout`, `EscapeChar`,
`HostKeyAlgorithms`, `HostName`, `IdentitiesOnly`, `IdentityFile`,
`KbdInteractiveAuthentication`, `KexAlgorithms`, `LogLevel`, `MACs`,
`PasswordAuthentication`, `Port`, `PreferredAuthentications`, `ProxyJump`,
`PubkeyAcceptedAlgorithms`, `PubkeyAuthentication`, `ServerAliveCountMax`,
`ServerAliveInterval`, `StrictHostKeyChecking`, `TCPKeepAlive` and `User`.

The new pane does not get these options, and tuios still follows the line:

- Forwards and sessions: `LocalForward`, `RemoteForward`, `DynamicForward`,
  `GatewayPorts`, `ExitOnForwardFailure`, `ClearAllForwardings`, `Tunnel`,
  `TunnelDevice`, `RemoteCommand`, `SessionType`, `StdinNull`,
  `ForkAfterAuthentication` and `RequestTTY`.
- Files, sockets, environment and forwarding: `ControlMaster`,
  `ControlPersist`, `ControlPath`, `UserKnownHostsFile`,
  `GlobalKnownHostsFile`, `HostKeyAlias`, `IdentityAgent`, `SendEnv`,
  `SetEnv`, `ForwardAgent`, `ForwardX11` and `ForwardX11Trusted`.
- The flags `-A`, `-X`, `-Y`, `-S`, `-L`, `-R`, `-D`, `-W`, `-w`, `-N`, `-f`,
  `-n`, `-T`, `-t`, `-M` and `-s`.

These ssh lines get an ordinary pane:

- An `-o` option that is not on the two lists above. `ProxyCommand`,
  `LocalCommand`, `XAuthLocation` and `Match` are examples.
- An option or a value with a control character, such as a line break.
- `-E`, `-F`, `-I`, `-G`, `-V`, `-Q` and `-O`, and an option that tuios does
  not know.
- A `-J` or `ProxyJump` hop that is not `[user@]host[:port]` or the `ssh://`
  form, made of letters, digits and `._:@[]-`, or a hop that starts with `-`.
- A mosh destination that starts with `-`.
- mosh with `--ssh`, `--client` or `--server`.
- ssh that another program starts, such as `scp`, `rsync`, `git` or `sftp`.

Put a `ProxyCommand` or a `ProxyJump` in `~/.ssh/config` to use it with a
split. A split with `-J` on the command line is followed.

### Start in the remote folder

The new pane starts in the remote folder when the remote shell reports its
folder with OSC 7. The host name in the report must be the same as the host
in the ssh line, or as the `HostName` that `ssh -G` gives for that line.
`reachy-mini` matches `pollen@reachy-mini`. When `~/.ssh/config` makes `prod`
an alias for `box.example.com`, a report from `box.example.com` or from `box`
matches `ssh prod`. A report from `box.other.com` does not. When there is no
match, the new pane starts in the remote home folder.

fish sends the report by default. For bash, add this line to `~/.bashrc` on
the remote machine:

```bash
PROMPT_COMMAND='printf "\033]7;file://%s%s\033\\" "$HOSTNAME" "$PWD"'${PROMPT_COMMAND:+;$PROMPT_COMMAND}
```

For zsh, add these lines to `~/.zshrc` on the remote machine:

```zsh
autoload -Uz add-zsh-hook
_tuios_osc7() { printf '\e]7;file://%s%s\e\\' "$HOST" "$PWD" }
add-zsh-hook precmd _tuios_osc7
```

To go to the folder, tuios adds `-t`, `-o RemoteCommand=none` and the
command `exec sh -c 'cd "FOLDER" ...'` to ssh. This works with every login
shell that can start `sh`. If the folder is gone, the new pane starts in the
home folder. A folder name with a quote, `$`, a backslash or `!` is not used.

## The dock's components

The `[dock]` table's region lists and custom components are not scalar
options, so `list-options` does not carry them; of the table it carries only
`dock.clock.format`. The settings page edits them
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

## The rail's custom section

The rail has one section whose rows are the output of a command you write.
Place `custom` in `appearance.sidebar.sections` like any built-in section and
describe it in `[appearance.sidebar.custom]`:

```toml
[appearance.sidebar]
sections = "sessions:25,terminals,custom:35,agents:30"

[appearance.sidebar.custom]
title   = "Brief"
command = "agent-brief render"
refresh = "event:window-focused,agent-state"
```

| Key | Meaning |
|---|---|
| `title` | The section's heading. Defaults to `Custom`. |
| `command` | Run through `sh -c`, as dock commands are. Every line of stdout is a row. |
| `refresh` | `once` (the default), a duration such as `"30s"` with a one-second floor, or `event:TYPE[,TYPE...]` with the dock's event types. `push` is refused: a command that stays running cannot see the focus or the section's size change. |

The command is a dock component that draws on the rail, so the dock's rules
apply: it runs in the client, under a three second timeout, with bounded
output; SGR colour survives and every other control sequence is stripped; a
command that fails, times out or prints nothing leaves the title over an
empty section, never an earlier run's rows; five failures in a row stop it
until the config reloads or `tuios refresh-dock rail/custom`; and
`tuios list-dock-components` lists it as `rail/custom` with why it drew
nothing. Rows are cut to the rail's width and to the lines the section's
share gives it, with `… +N` for the ones below the fold.

Each run gets the client's environment plus `TUIOS_SESSION`, `TUIOS_SOCKET`,
`TUIOS_RAIL_SECTION` (`custom`), `TUIOS_RAIL_WIDTH` (the columns a row may
use), `TUIOS_RAIL_HEIGHT` (the most rows the section's share can give it; the
rail may give fewer when other sections need the lines),
`TUIOS_ACTIVE_PANE_ID` and `TUIOS_ACTIVE_PANE_CWD` (the focused pane and its
folder, named as `[[keybindings.command]]` names them). They are read for
each run, and an event that lands while a run is going costs one more run
after it, so a quick focus change never leaves the old pane's rows on
screen. `agent-state` fires for the states `[notifications.agent]` alerts on.
The command does not run while the rail is hidden or folded, so the width it
is told is never `0`, and it runs again when the rail opens.

`appearance.sidebar.sections` is a settable option, so `tuios set-config` can
place the section. `[appearance.sidebar.custom]` is read from the file only:
the command runs outside every pane on every refresh, as dock commands and
hooks do, and in the default open mode any pane can call `set-option`, so
the command stays out of its reach, as `daemon.respond_from_shell` does.

## Turn off agent features

Use this setting if you want only the multiplexer:

```toml
[agents]
enabled = false
```

The default is `true`. You can also change it on the settings page. It is the
first row, "Agent features". While a tuios client is open, you can also run
`tuios set-config agents.enabled false` from a shell outside tuios.

When the agent features are off:

- The daemon does not look for agents. It does not read the process table,
  pane titles, screens or agent transcripts. It ignores the agent state that
  programs send.
- The rail shows no agents section. The other sections move up to fill the
  space.
- The Inbox, agent mail, approvals and agent alerts are off. The Inbox key
  shows a message and does nothing else.
- The prefix menu, the command palette, the help and the settings page do not
  show agent items.
- Agent hooks, such as `after-agent-state`, do not run.
- Agent commands, such as `tuios start-agent`, `tuios fan` and
  `tuios list-attention`, stop and print this message: "Agent features are
  off. Set agents.enabled = true in the config to use this command." A
  program that calls an agent verb gets the error code `agents_disabled`
  and this message: "Agent features are off on this machine. Set
  agents.enabled = true in its config.toml to turn them on."

The change applies at once. You do not have to restart tuios. When you turn
the features off, the daemon clears the agent state of every pane and closes
every Inbox item. A held approval goes back to the agent, which asks in its
pane. When you turn the features on, the daemon looks at every pane again.

Typing between panes is stricter when the features are off. A pane can answer
another pane's prompt if it types into that pane. Usually tuios knows which
pane waits on a prompt. With the features off it does not know. Thus a pane
that does not hold the `respond` grant can type only into these panes:

- A pane that it opened, for example with `tuios new-window` or the tmux
  shim's `split-window`.
- A pane whose own shell is at its prompt, with no program running.

It cannot type into other panes. This includes `send-keys`, `send-text` and
`run` from a script in a pane. Your own keys, and commands from a shell outside
tuios, are not affected. To let every pane type into other panes, give panes
`respond`:

```toml
[agents.permissions]
grants = ["admin", "respond"]
```

See [What a pane may do](#what-a-pane-may-do).

Only you can change the switch: on the settings page, in config.toml, or with
`tuios set-config` from a shell outside tuios. A process in a pane gets
`forbidden`. `tuios mcp` started with the features off does not list the
agent tools. An agent command aimed at another machine, such as
`tuios list-agents -s build:work`, follows this machine's switch.

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
`opencode`, `kilo` and `qwen` have a decision channel tuios can answer through. Other
names are accepted and hold nothing. Even for these, only a call the Inbox can
show whole on one line is held, such as a short shell command or a file read;
an edit, an MCP tool or a long command is answered in the pane.
`hold_seconds` is how long a prompt waits for your answer before the harness
asks in its pane after all: 120 when unset, kept between 10 and 300.

The daemon reads the table when it starts and again when the file changes; a
change applies to the next prompt. Like `[dock]` and `[hosts]`, it is not in
`list-options` and `tuios set-config` cannot change it. Turning it on gives no
program the power to answer: only you, at an attached client, can. It also
needs version 2 of the integration:
run `tuios integration install claude-code` (or `opencode`, `kilo`, `qwen`)
again after upgrading. [AGENT_STATE.md](AGENT_STATE.md#approvals-from-the-inbox) says how
a prompt is held, answered and handed back.

## Push notifications to your phone

The `[notify]` table sends a push notification when the Inbox gets an item
that waits for you. The daemon sends it, so it works when no client is
attached. Set one provider or more. Each provider gets every notification.

```toml
[notify]
web_url = "https://term.example.com/"   # tuios-web, for a link to the item
content = "summary"                     # or "title"
quiet_active_seconds = 120
cooldown_seconds = 60
max_per_hour = 30

[notify.triggers]
approval = true
plan = true
question = true
ask = true
mail = false
errored = false
finished = false

[notify.ntfy]
url = "https://ntfy.sh/a-long-random-topic"
token_file = "~/.config/tuios/ntfy-token"   # optional

[notify.pushover]
user_env = "PUSHOVER_USER"
token_file = "~/.config/tuios/pushover-token"

[notify.webhook]
url = "https://hooks.example.com/tuios"
token_env = "TUIOS_HOOK_TOKEN"              # optional, sent as a bearer token
```

| Key | Default | What it does |
| --- | --- | --- |
| `enabled` | `true` | Turns every notification off when `false`. Nothing is sent without a provider. |
| `web_url` | empty | The address of tuios-web. Each notification links to `web_url/inbox?item=ID`, which opens the Inbox on the item. See [WEB.md](WEB.md#open-the-inbox-from-a-notification). |
| `content` | `summary` | `summary` sends the kind, the pane name and the one line the Inbox shows. `title` sends only the kind and the session. |
| `quiet_active_seconds` | `120` | Holds a notification while you typed into a pane at an attached client in this many seconds. When you stay away that long and the item is still open, tuios sends it. `0` sends at once. |
| `cooldown_seconds` | `60` | The shortest time between two notifications for the same pane and kind. |
| `max_per_hour` | `30` | The most notifications in one hour. `0` sets no limit. |
| `allow_http_redirects` | `false` | Lets a provider redirect to a plain `http` address. |

`[notify.triggers]` selects the Inbox kinds that send a notification. The
kinds that wait for you are on by default: `approval`, `plan`, `question` and
`ask` (a question from `tuios ask-human`). The kinds that only report are off:
`mail`, `errored` and `finished`.

Each item sends one notification at most. A repeated state report, or a new
line on the same item, sends nothing. An item from a linked host sends
nothing here. That host sends its own.

### Providers

- `[notify.ntfy]`: `url` is the topic address. `priority` is 1 to 5. When it
  is unset, an item that waits for you gets 4 and other items get 3. The
  link goes in the `Click` header.
- `[notify.pushover]`: your user key and an application token. `url`
  replaces the Pushover API address, for a relay.
- `[notify.webhook]`: tuios sends a JSON `POST` to `url` with these fields:
  `event` (`tuios.inbox`), `kind`, `title`, `body`, `link`, `urgent`,
  `item_id`, `session`, `window`, `harness`, and `test` for a test
  notification. At `content = "title"` the body is empty.

### Secrets

Give each token in one of three ways. tuios uses the first one that is set.

- `token = "..."` in config.toml.
- `token_env = "NAME"`: an environment variable of the daemon. The daemon
  keeps the environment of the shell that started it.
- `token_file = "PATH"`: a file that only you can read (mode 600 or 400).

Pushover uses `user`, `user_env` and `user_file` for the user key in the
same way. tuios reads the value each time it sends, so a new token in the
file applies at once.

tuios sends each notification itself and needs no other program. It uses
`HTTPS_PROXY`, `NO_PROXY` and the system certificates. Each send waits at
most 10 seconds. It follows at most 3 redirects. The token goes in a request
header and is never logged.

tuios does not write a secret to its logs or its output. The address is
also secret on a public ntfy server, so logs and `tuios notify test` show
only the host. A redirect to a plain `http` address is refused, unless you
set `allow_http_redirects`. A redirect to another host is always refused,
because the message and its token would go there. Set the url to the
address the server redirects to.

### Apply a change

The daemon reads `[notify]` when it starts, and again when the file changes.
A change that turns notifications off, or changes the triggers, applies at
once. A new address, or a new source for a secret, waits for
`tuios config apply` from a terminal outside tuios, or a daemon restart. A
program in a pane can write config.toml, and this stops it from sending your
Inbox to another address. Like `[hosts]`, `[notify]` is not in
`list-options`, and `tuios set-config` cannot change it.

Run `tuios notify test` to send a test notification through each provider.
It says the result for each provider. See
[CLI_REFERENCE.md](CLI_REFERENCE.md#tuios-notify-test).

## Harnesses that report to herdr

Crush, and other agents that herdr lists as reporting by themselves, report
their state to herdr, another multiplexer, when they find herdr's environment
in their pane. tuios accepts the same reports on a socket of its own,
`<daemon socket>.herdr`. `herdr_protocol` in `[agents]` says which panes get
herdr's environment:

```toml
[agents]
herdr_protocol = "always"   # always (default), agents, off
```

`always` gives it to every pane, as herdr does, so a Crush that you start from
a shell prompt reports. `agents` gives it only to a pane that starts a known
reporter directly, as `tuios new-window NAME crush`, `start-agent crush` or
`fan --agent crush` do. `off` gives it to no pane.

A pane with herdr's environment gets `HERDR_ENV=1`, `HERDR_SOCKET_PATH` naming
tuios's socket, `HERDR_PANE_ID`, `HERDR_TAB_ID` and `HERDR_WORKSPACE_ID` naming
the pane, its workspace and its session, and `HERDR_BIN_PATH` naming a `herdr`
link to tuios that answers herdr's command line. Programs that check
`HERDR_ENV` read the pane as a herdr pane. With the default, `herdr` refuses to
start inside a tuios pane. Set `herdr_protocol = "agents"` to run herdr nested.
The socket also answers herdr's socket API, so tools built for herdr work with
tuios (see [herdr compatibility](AGENT_STATE.md#herdr-compatibility)).
herdr's own hook scripts, if installed, report to tuios from such a pane.

An unknown value reads as `always`, with a warning. The daemon reads the value
when it starts and again when the file changes. A change applies to the next
pane. [AGENT_STATE.md](AGENT_STATE.md#herdrs-pane-state-protocol) says what is
accepted.

## Report pane states to your terminal

When tuios runs in a terminal that reads OSC 7501, the Program Status
Protocol (Rex does), it reports the agent state of each pane there.
`host_program_status` in `[agents]` turns this off:

```toml
[agents]
host_program_status = "auto"   # auto (default), off
```

`auto` asks the terminal when a client starts, and reports only when the
terminal answers. `off` never asks. A client reads the value when it starts.
An unknown value reads as `auto`, with a warning. See
[PROGRAM_STATUS.md](PROGRAM_STATUS.md#tuios-inside-a-terminal-that-reads-osc-7501).

## herdr plugins

tuios runs herdr plugins: folders with a `herdr-plugin.toml`. The `[plugins]`
table says which plugins run and where tuios finds more of them:

```toml
[plugins]
enabled = ["example.notes"]          # the ids of the plugins that run
dirs = ["~/src/my-herdr-plugin"]     # more plugin folders, or manifests
```

tuios also finds plugins in `$XDG_CONFIG_HOME/tuios/plugins/<folder>/` and in
herdr's own `plugins.json` and managed checkouts. To find a plugin runs
nothing. A plugin runs only when its id is in `enabled`.

An enabled plugin runs its commands with your rights, outside every pane.
Enable only plugins that you trust.

Use the commands to change the table. They change only the `enabled` or
`dirs` line and keep the rest of the file:

```bash
tuios plugins enable example.notes
tuios plugins disable example.notes
tuios plugins link ~/src/my-herdr-plugin
```

You must run them from a terminal outside tuios. A change to the file that
removes a plugin from `enabled` applies at once. A change that adds a plugin
to `enabled`, or a folder to `dirs`, waits for `tuios config apply` or a
daemon restart. `tuios plugins enable ID` applies only the plugin that it
names. A new folder waits because it can hold a
plugin with the id of a plugin that you enabled.
[AGENT_STATE.md](AGENT_STATE.md#herdr-plugins) says what runs and when.

## Plans, risk rules, the recap and the queue

These tables configure the agent review, triage, reply and approval work,
which is built. Every value has a default, so a file without them behaves as
the defaults say. Like `[agents.approvals]`, they are
file-plane config: not in `list-options`, and `tuios set-config` cannot change
them, so a pane cannot switch a risk rule off through tuios.

```toml
[agents.approvals]
hold_plans = true                 # a plan follows enabled

[agents.approvals.risk]
builtin = true                    # keep the shipped rules
panes_may_allow = false           # a pane with the respond grant may not allow a risky call

[[agents.approvals.risk.rule]]
name = "kubectl apply"
tools = ["Bash"]
pattern = '\bkubectl\s+(apply|delete)\b'

[agents.recap]
mode = "toast"                    # toast, inbox or off
away = "10m"
test_patterns = ["go test", "pytest"]

[agents.queue]
max = 8

[agents.checkpoints]
enabled = true
keep = 50
max_untracked_mb = 50
```

- `hold_plans` also hands a plan an agent in plan mode asks to have approved
  to the Inbox, for the harnesses `enabled` names (Claude Code's
  `ExitPlanMode`). Unset is true. See
  [Plans](AGENT_STATE.md#plans).
- `[agents.approvals.risk]` marks an approval risky when its command matches a
  rule: `builtin` keeps the shipped rules (default true), each `rule` adds one
  with a name, the tools it applies to (empty for every tool) and an RE2
  `pattern`, matched against each command of a shell call and the whole text
  of any other. Naming one shell tool, such as `Bash`, covers every shell
  tool, including `execute`, the word a protocol pane's line uses for a
  command, and a line with no tool; naming one file tool, such as `Write`,
  covers every file tool, including a protocol pane's `edit`. A daemon that
  could read no config file at all still uses the shipped rules. A rule with no name or a pattern that does not compile is
  ignored, with a warning. An allow of a risky approval takes a second press
  of the same key in the Inbox, and the daemon refuses one that does not name
  the rules. `panes_may_allow` lets a pane holding the `respond` grant allow a
  risky call; it is off, so only you can. The shipped rules are listed under
  [Risk rules](AGENT_STATE.md#risk-rules). They are a speed bump, not a
  sandbox.
- `[agents.recap]` is the summary of what an agent did while you were away:
  `mode` says where it is shown (`toast` in the dock when you come back to the
  pane, and in the Inbox; `inbox` only in the Inbox; `off` only in
  `agent-log`), `away` how long you must have been away for the dock to show
  it, and `test_patterns` which commands count as a test run. The daemon
  reads `test_patterns` for `tuios agent-log --recap` and the
  `agent-activity` verb, and picks up a change when the file is saved; the
  client reads `mode` and `away` when it loads the config. The recap is
  built: see [The away recap](AGENT_STATE.md#the-away-recap).
- `[agents.queue]` bounds the messages waiting to be typed to one agent when
  it comes to rest (`tuios queue`, `queue-prompt`): `max`, 8 by default, at
  most 64. A message queued past it is refused with `queue_full`. The daemon
  reads it at start and again when the file changes; a queue already longer
  keeps what it holds. The queue is built: see
  [AGENT_STATE.md](AGENT_STATE.md#queued-messages).
- `[agents.checkpoints]` saves the git work tree of an agent's pane each time
  the agent finishes a turn that changed a file (`tuios checkpoint`).
  `enabled` is true by default. `keep` is how many checkpoints one pane keeps:
  50 by default, at most 1000. `max_untracked_mb` leaves an untracked file
  larger than this many megabytes out of a checkpoint: 50 by default, and a
  negative value means no limit. `tuios checkpoint list` names the files left
  out, and a restore does not change them. The daemon reads these at start
  and again when the file changes. See [Turn checkpoints](AGENT_STATE.md#turn-checkpoints).

One rail option goes with them: `appearance.sidebar.agent_rest_fold`, how long
an agent row rests (idle, unknown, or done and already seen) before the rail
folds it into one line, as a duration such as `1h` (the default) or `off`. The
settings page shows it on its Sidebar tab once an agent has been seen. The
fold is one muted `+3 at rest` line at the end of the agents section; `enter`
or a click on it shows the rows until the rail lets go of the keyboard, or,
when the rail did not have the keyboard, until a click outside the rail or a
pane is focused. It
takes two rows or more, and never a row that needs you, a finished turn not
yet seen, a working agent, the pane you are in, one with messages queued, or
one whose agent has subagents at work.

The agent row in `[appearance.sidebar.agent_row]` has five tokens for what
tuios feeds itself: `now` (what a working agent is doing, drawn only while it
works), `context` (`ctx 84%` in the warning ink, drawn only at 80% or more),
`subagents` (`2 subagents`, how many subagents the agent has at work, drawn
on any row while any run, from Claude Code's hooks), `pr` (`PR #12 open
pass`, the pull request of the session's worktree branch and its checks, in
the error ink for failing checks, the warning ink for pending ones and the
success ink for passing ones and a merge, see
[Shipping a worktree](AGENT_STATE.md#shipping-a-worktree)) and `prompt` (the
first line of the last prompt, not shipped on the row). The shipped `tokens`
list is now `["session", "need", "harness", "name", "progress", "elapsed",
"context", "subagents", "pr", "meta", "now", "message"]`. `progress` (`40%`)
is the progress a program reported with OSC 7501, drawn while it works or
waits (see [PROGRAM_STATUS.md](PROGRAM_STATUS.md)). A list you wrote keeps its own
order and gains nothing. Add `subagents` or `pr` to it to see them. The `meta` token no longer
draws the fed keys (`now`, `prompt`, `model`, `context`, `cost`, `plan`,
`subagents`), so place any of them you want with its own token. See
[What the second line says](AGENT_STATE.md#what-the-second-line-says).

The `$name` tokens can place the metadata keys tuios now feeds: `$model`,
`$context`, `$cost` and `$plan`.
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

The daemon reads the table when it starts and again when the file changes. A
change that gives panes less reaches every pane on the default at its next
call. A change that gives more, such as `strict` to `open`, waits, because a
process in a pane can write config.toml. Run `tuios config apply` from a
terminal outside tuios to apply it, or restart the daemon. Until then the
daemon logs it, and `tuios pane-grants` says so. When a start finds that
panes hold more than at the last run, it says so in the log, in
`tuios pane-grants` and in the Inbox. Like
`[agents.approvals]`, it is not in `list-options` and `tuios set-config` cannot
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
| `respond` | Answer for the person: prompts, held approvals, `ask-human` questions, dismissing Inbox items, and passing on held mail. Also type into a pane that waits on a prompt, from a pane on the other machine. The person on the other machine, outside every pane, needs only `write` for that. |

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

The daemon follows the file. A change that gives a machine less applies to the
next call on every link, including links already open. A change that gives a
machine more, a new host, or a host that dials another way waits for
`tuios config apply` from a terminal outside tuios, or a daemon restart.
`tuios hosts add` applies its change that way, so from a pane it waits.
`tuios hosts add` on a known name keeps these fields.

`ssh_options` takes only options that cannot run code or write files on this
machine, each written as `-o Keyword=value` with one plain value: for example
`-p`, `-i`, `-l`, `-J`, `Port`, `User`, `HostName`, `HostKeyAlias`,
`IdentityFile`, `StrictHostKeyChecking`, the `ServerAlive` options and the
algorithm lists. `ProxyCommand`, `LocalCommand`, the known hosts files,
`ControlPath`, socket forwards, `-F` and the like are refused. `-J` and
`ProxyJump` take host names only. `addr` and `command` may not start with a
dash. A host with a refused entry is ignored, and `tuios hosts` and the Inbox
name the reason. Put such options in `~/.ssh/config`. [protocol.md](protocol.md#what-a-linked-machine-may-do-here) has
the verb by verb table.

`tailscale_login` is the origin of a Headscale server that sends the Tailscale
SSH check for this host, for example `"https://headscale.example"`. tuios
shows and opens a sign-in link only on `login.tailscale.com`,
`controlplane.tailscale.com` and this origin, over https, with the host matched
exactly. Anything on the host can print the banner that carries the link. See
[Tailscale SSH check mode](SESSIONS.md#tailscale-ssh-check-mode).
