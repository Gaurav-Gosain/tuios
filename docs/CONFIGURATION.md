# Configuration

[config-options.md](config-options.md) lists every option, with its type, its
default and what it does. A test writes that page from the code. This page
tells more about the options that need it.


## Tables

| Table | What it holds |
|-------|---------------|
| [`[appearance]`](#appearance-the-appearance-table) | Theme, backgrounds, borders, windows, mouse, tiling, dock. Sub-tables `scrollbar`, `selection` and `sidebar` |
| [`[notifications]`](#notifications-the-notifications-table) | How long messages stay up, and agent alerts |
| [`[keybindings]`](#keybindings-the-keybindings-table) | The leader key and the sections of bindings |
| [`[daemon]`](#the-daemon-table) | Daemon log level, agent detection, resuming agents, saved history and the size of a shared session |
| [`[startup]`](#the-startup-table) | How a new session starts |
| [`[workspaces]`](#the-workspaces-table) | What happens when a workspace becomes empty |
| [`[launcher]`](#the-launcher-table) | How the app launcher starts a graphical program |
| [`[tape]`](#the-tape-table) | Project tapes |
| [`[debug]`](#the-debug-table) | The keycast overlay |
| [`[screenshot]`](#the-screenshot-table) | Capture format, frame and fonts |
| [`[screensaver]`](#the-screensaver-table) | The idle animation |
| [`[review]`](#the-review-table) | How a review of an agent's changes opens |
| [`[hints]`](#the-hints-table) | What hints mode labels, the label letters and how much the pane dims |
| [`[panes]`](#the-panes-table) | The keys of the pane labels that <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>Q</kbd> shows |
| [`[scratch]`](#the-scratch-table) | The size of the scratch terminal that <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>g</kbd> shows |
| [`[pip]`](#the-pip-table) | The size and corner of the picture-in-picture view that <kbd>p</kbd> pins |
| [`[spotlight]`](#the-spotlight-table) | The beam that dims all but one part of the screen |
| [`[dock]`](#the-dock-table) | Which components the bar draws, and your own |
| [`[hosts]`](#the-hosts-table) | Other machines this daemon links to, and what each may do here |
| [`[tailscale]`](#the-tailscale-table) | Which tailnet machines `tuios hosts add` offers |
| [`[agents]`](#the-agents-table) | The switch for agent features, approvals from the Inbox, risk rules, what a pane may do, the away recap, the queue and checkpoints |
| [`[notify]`](#the-notify-table) | Push notifications to your phone for Inbox items |
| [`[hooks]`](#hooks-the-hooks-table) | Shell commands run on events |
| [`[motion]`](#the-motion-table) | Each animation on its own |
| [`[mouse]`](#the-mouse-table) | Double-click and drag thresholds |
| [`[links]`](#the-links-table) | Rules that turn text in a pane into a link |
| [`[path_picker]`](#the-path_picker-table) | The path picker |
| [`[ssh]`](#ssh-server-limits-the-ssh-table) | Limits of the SSH server |
| [`[mermaid]`](#the-mermaid-table) | The colours of `tuios mermaid` |
| [`[paste_buffers]`](#the-paste_buffers-table) | How many paste buffers tuios keeps |
| [`[worktree]`](#the-worktree-table) | Where `tuios worktree new` puts a worktree |
| [`[files]`](#the-files-table) | Copies of files from another machine |
| [`[shell]`](#shell-prompt-marks-the-shell-table) | Shell integration and login shells |
| [`[actions]`](#actions-and-menus-of-your-own-the-actions-and-menus-tables) | Actions of your own |
| [`[menus]`](#actions-and-menus-of-your-own-the-actions-and-menus-tables) | Menus of your own |
| [`[tasks]`](#the-tasks-table) | The task board |
| [`[plugins]`](#the-plugins-table) | Which plugins run |

## Sidebar file editor

Settings, Sidebar, File editor sets [`appearance.sidebar.editor`](config-options.md#appearance.sidebar.editor), the terminal
editor command used by `Shift+Enter` on a text file. For example:

```toml
[appearance.sidebar]
editor = "micro"
```

Other terminal editors and arguments work too, such as `nano` or `vim -f`.
Quote a path or an argument that contains spaces. tuios splits the command into
arguments and runs no shell. It adds the full path of the file at the end. If
the value is empty, tuios uses `$EDITOR`, then `$VISUAL`, then `vi`. File links,
paths and hints open a file in quick look, and `e` there opens it with the same
command, at the line on top for vi, Emacs, nano, kakoune and helix. tuios reads the first 512 bytes of
a file to check that it is text. It refuses binary files and special files.

Every table of `config.toml` has a section on this page. [Tables](#tables) lists them.

`[notifications]` sets how many seconds a message stays up. `duration` is for info and success messages, and its default is 2. `warning_duration` (default 8) and `error_duration` (default 15) are for warnings and errors. Any value from 1 to 3600 is valid, and 0 uses the default. tuios warns about a negative value and uses the default. tuios warns about a value above 3600 and uses 3600. `style` sets how a message looks. `full` (the default) shows a coloured bar on the left of the message and a line above it that counts down. `plain` shows the icon and the text only. The icon keeps its colour, so an error still looks different from an info message. `level` sets the least severe message the dock shows: `info` (the default) shows every message, `warning` only warnings and errors, and `error` only errors. A message the dock does not show still goes to the log viewer (`Ctrl+B D l`), so the notice for a key that did nothing can be read there. Set `log_hidden = false` to keep those messages out of the log as well. Warnings and errors always go to the log. A message from a pane, an agent or mail does not use `level`. `[notifications.agent]` and `[notifications.mail]` control those messages.

`machine_offer` (default `true`) shows a message when a pane logs in with ssh or mosh to a machine that no `[hosts]` entry reaches. The message is "buildbox is not a machine here. Add it?" with three choices. Add opens a new pane that runs `tuios hosts setup` for the machine. Not now hides the message for that machine until tuios starts again. Never adds the machine to a list in `sidebar.json`, and the Alerts tab of Settings shows that list. Select a machine in that list to remove it. A message that gets no answer goes away after `duration`, as Not now does. The message shows only for a login that reads your keys: a tunnel (`-N`, `-f`, `-n`, `-W`), `-s`, `-T` and a remote command without `-t` do not count. ssh that scp, rsync, git, sftp or sshfs starts does not count. The client must run for 5 seconds. This machine on port 22 and the git hosts (github.com, gitlab.com, bitbucket.org, codeberg.org) are never offered. The check works with `[agents] enabled = false`. Set `machine_offer = false` to turn it off.

`[notifications.mail]` has `enabled`, `notify`, `dock`, `sound` and `between_agents`. Each key except `between_agents` follows the same key in `[notifications.agent]` while it is unset, so a config without the table alerts as before. `between_agents` (default `false`) also alerts on a message from one agent to another. Sound mode, cooldown, cue files and quiet hours always come from `[notifications.agent]`. `tuios set-config notifications.mail.KEY ""` clears a key, and `tuios get-config` then prints `(follows notifications.agent.KEY)`.

`tuios list-options` describes every settable path with its type, default, and accepted values or range, straight from the registry the validator uses. A number that counts something says what: `200 ms`, `2 cells`, `45%` (the `unit` field of `list-options --json`), and the settings page shows the same unit after the value. The in-app settings page (`Ctrl+B ,`) edits and persists the same options, and its rows are derived from that same registry: an option an agent can set is an option a person can reach, and a test fails the build if one is not.

## Sidebar peek

When you put the pointer on a session row in the sidebar, the terminals
section shows the panes of that session. When you move the rail cursor onto a
session row, the same thing occurs. This is the peek. To turn it off, set
[`appearance.sidebar.peek`](config-options.md#appearance.sidebar.peek) to `false`:

```toml
[appearance.sidebar]
peek = false   # true is the default
```

With the peek off, the terminals section shows the panes of the attached
session. It changes only when you attach to a different session. The setting
also shows in Settings, Sidebar, and `tuios set-config appearance.sidebar.peek
false` changes it while tuios runs.

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

`tuios config apply` makes the daemon and every tuios client on this machine
apply the files as they are now. Use it when a save does not reach a client,
for example when you make an included file in a new directory. It also loads
the daemon's `[hooks]` table, so a hook change needs no restart. See
[HOOKS.md](HOOKS.md).

### Where tuios writes a change

The settings page, `tuios set-config`, the keybind manager,
`tuios keybinds unbind`, `tuios hosts add` and `tuios plugins` write the config.
They write only the keys you changed, and never copy the other files into
config.toml. A change made while a file changed on disk keeps the new values
of that file. Each change goes to one file:

- A key that a file sets goes to the last file that sets it. The look file,
  `config.d/look.toml`, is not a target: a change to a key that it sets goes
  to config.toml. See [Looks](THEMES.md#looks).
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

## One config on macOS and Linux

You can share one `config.toml` between a Mac and a Linux machine, for example with a dotfiles repo. tuios reads `opt+` and `option+` as `alt+` on every platform. On Linux, a key such as `opt+1` is `alt+1`.

The `[keybindings.terminal_mode]` table also accepts `middle` for the middle
mouse button, with `ctrl`, `alt` or `shift`, such as `ctrl+middle`. A program
that uses the mouse gets a plain middle press.

tuios ignores a key that it cannot read and loads the rest of the file. The action gets its default key when it has no other key. If another action already has that default key, the action has no key. tuios shows a config problem when it starts, and the log viewer (leader `D` `l`) names each key and its file. `tuios keybinds doctor` and `tuios config apply` list these keys too. A table under `[keybindings]` that tuios does not have, such as `[keybindings.terminal]` for `[keybindings.terminal_mode]`, is a config problem too: tuios ignores every binding in it, and the log viewer and `tuios config apply` name the table and the table you most likely meant.

## Keyboard layout and Option characters

```toml
[keybindings]
keyboard_layout = "us"   # us, other
option_glyphs = "auto"   # auto, alt, bind, type
```

These options are in the [option reference](config-options.md): [`keybindings.keyboard_layout`](config-options.md#keybindings.keyboard_layout), [`keybindings.option_glyphs`](config-options.md#keybindings.option_glyphs).

See [Shifted digits and AZERTY](KEYBINDINGS.md#shifted-digits-and-azerty) and
[One Option key for typing](KEYBINDINGS.md#one-option-key-for-typing).

## Lock mode

```toml
[keybindings]
lock = true
```

This option is in the [option reference](config-options.md): [`keybindings.lock`](config-options.md#keybindings.lock).

See [Lock mode](KEYBINDINGS.md#lock-mode).

## Profile

`profile` is a set of defaults. It goes at the top of the file, before the first table.

```toml
profile = "simple"   # simple or keyboard (default)
```

`keyboard` is the default. It changes nothing.

`simple` is for a person who is new to terminals:

- Every key goes to the pane. Window mode is off (`[keybindings] window_mode = false`).
- The dock is at the bottom. It shows the tabs on the left and a row of buttons on the right: Menu, New, Split, Close, Help and Settings.
- The Menu button opens the short menu. A right-click on a pane opens the pane menu.
- The window controls are a pill on the right of the title bar.
- The UI calls a workspace a "tab".

The leader key, the which-key panel, the command palette and every action still work. A key that you set in the file wins over the profile. The profile does not write to the file.

To change the profile, open Settings, go to Behavior and turn Simple controls on or off. The command palette has Use simple controls and Use keyboard controls. Each one writes the `profile` line and applies at once.

On the first start, when tuios writes a new config.toml, the welcome screen asks how you want to use tuios. Press 1, 2 or 3, or click a choice:

- Simple writes `profile = "simple"`.
- Keyboard writes nothing. This is today's tuios.
- tmux keys writes `[keybindings] preset = "tmux"` and `window_mode = false`. When tuios finds a tmux.conf, this choice is first and uses its prefix.

Esc keeps the keyboard and writes nothing. An existing install does not see the question.

## Window mode off

```toml
[keybindings]
window_mode = false
```

This option is in the [option reference](config-options.md): [`keybindings.window_mode`](config-options.md#keybindings.window_mode).

See [Window mode off](KEYBINDINGS.md#window-mode-off).

## Nest mode

```toml
[keybindings]
nest_exit_key = "f12"

[keybindings.prefix_mode]
nest = ["enter"]
```

This option is in the [option reference](config-options.md): [`keybindings.nest_exit_key`](config-options.md#keybindings.nest_exit_key).

See [Nest mode](KEYBINDINGS.md#nest-mode).

## Opening links

tuios finds two kinds of link in a pane. An OSC 8 hyperlink is a link that a
program marked, such as `ls --hyperlink`, `gcc`, `delta`, `gh` or an agent
CLI. Its text and its target can be different. A bare URL is plain text that
starts with `http://`, `https://`, `ftp://`, `ftps://`, `file://`, `ssh://` or
`git://`. Hints mode and the pointer use the same URL detector. With
`links = "all"`, a file path that is there is a link too, and opens in quick
look (see [Paths in a pane or the agents view](KEYBINDINGS.md#paths-in-a-pane-or-the-agents-view)).

```toml
[appearance]
links = "all"          # off, marked (OSC 8 only), all
link_click = "both"    # both, ctrl, shift, off
link_hover = "safe"    # safe, always
link_opener = ""       # for example "firefox --new-tab" or "open -a Safari %s"
```

These options are in the [option reference](config-options.md): [`appearance.links`](config-options.md#appearance.links), [`appearance.link_click`](config-options.md#appearance.link_click), [`appearance.link_hover`](config-options.md#appearance.link_hover), [`appearance.link_opener`](config-options.md#appearance.link_opener).

The system opener is `open` on macOS, `rundll32` on Windows, `wslview` under
WSL, and `xdg-open` on other systems. tuios runs no shell. It gives the URL to
the opener as one argument. If the opener fails, tuios shows a message and
puts the URL on your clipboard.

### Link rules

A link rule turns plain text that is not a URL into a link, such as a ticket
ID that opens its page in an issue tracker. It works like
[WezTerm's implicit hyperlinks](https://wezterm.org/hyperlinks.html#implicit-hyperlinks).

```toml
[[links.rules]]
regex = '\b([A-Z][A-Z0-9_]+-[1-9][0-9]*)\b'
url = "https://jira.example.com/browse/$1"
```

- `regex` is a regular expression in Go syntax.
- `url` is the address a match opens. `$0` is the whole match, and `$1`, `$2`
  and so on are its groups. A named group is `${name}`. A group followed by a
  letter, a digit or `_` needs braces: write `${1}_x`, because `$1_x` names a
  group called `1_x`. tuios warns about a url that names a group the regex
  does not have.
- Keep the scheme and the host fixed in `url`, and put groups in the path or
  the query only, as in the example. A group before the path, such as
  `https://example.com$1`, lets the text on screen choose the host.
- A match is a link like a bare URL: the hover underline and label (which
  shows the address), the click in `link_click`, `link_opener`, the OSC 8
  output to your terminal, and `Ctrl` and a label in hints mode. A label
  without `Ctrl` copies the text on screen.
- Rules apply when `links` is `all`, as bare URLs do.
- A real URL wins when a rule matches text inside it. An earlier rule wins
  over a later one.
- The address a rule builds goes through the same check as every link. A rule
  whose address is not `http`, `https`, `mailto`, `ftp` or `ftps`, or that
  holds a byte a bare URL cannot hold (a space, a quote, a backtick, an angle
  bracket, `|`, `^`, `{`, `}`, `\` or a control character), makes no link.
- A rule that does not compile, or that matches empty text, is skipped, and
  tuios shows a warning when it reads the config.
- With rules, tuios reads every line of a pane for them, not only the lines
  that hold `://`. With no rules, nothing changes.
- As with bare URLs, the OSC 8 output carries the rule links of the focused
  pane only. In another pane they are found on hover and click.

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

kitty also keeps `Ctrl+Shift+click`, which tuios uses for multifocus. See
[Multifocus](LAYOUT_MODES.md#multifocus) for the two `kitty.conf` lines that
give it to tuios.

`Cmd` is not part of the mouse protocol, so tuios never sees `Cmd+click`. On
macOS, `Cmd+click` opens a link through the terminal, from the OSC 8 that
tuios writes. In Ghostty, a program can ask for `Shift+click` with
`XTSHIFTESCAPE`, or you can set `mouse-shift-capture = true`. tuios does not
ask, because that takes `Shift` selection away from you.

## Pause focus follows mouse

`appearance.focus_follows_mouse = true` focuses the pane under the pointer as
the mouse moves. Set [`appearance.focus_follows_mouse_pause`](config-options.md#appearance.focus_follows_mouse_pause) to hold a key that
pauses it:

```toml
[appearance]
focus_follows_mouse = true
focus_follows_mouse_pause = "ctrl"   # ctrl, alt, or off (the default)
```

- Hold the key and move the mouse. The focus stays on the current pane.
- Release the key. The next mouse move focuses the pane under the pointer.
- The key still goes to the pane. A program that reads the mouse gets the
  motion with the key on it. `Ctrl`+drag and `Alt`+drag work as before.

tuios reads the key from the mouse reports that your terminal sends. Most
terminals send `Ctrl` and `Alt`. `Shift` is not a choice, because most terminals
keep `Shift` with the mouse for their own selection. On macOS, some terminals
use `Option` with the mouse for their own actions. If `alt` does not work in
your terminal, use `ctrl`.

## Neovim pane navigation

[`appearance.nvim_navigation`](config-options.md#appearance.nvim_navigation) is `false` by default. Set it to `true` to enable
pane navigation through the optional
[tuios-nvim-navigator](https://github.com/Tim4c/tuios-nvim-navigator) plugin:

```toml
[appearance]
nvim_navigation = true
```

See [KEYBINDINGS.md](KEYBINDINGS.md#neovim-pane-navigation) for the matching
focus-key mappings and protocol behaviour.

## Wrap focus at the edge

[`appearance.focus_wrap`](config-options.md#appearance.focus_wrap) is `false` by default. Set it to `true` to wrap
directional focus at the edge of a tiled layout, as tmux `select-pane` does:

```toml
[appearance]
focus_wrap = true
```

When no pane is in that direction, focus goes to the pane on the far side that
faces the focused pane. If more than one pane is on the far side, tuios picks
by the rule of a normal focus move. A pane that fills the layout in that
direction keeps focus.

The option applies to every directional focus move in a tiled layout:

- The focus keys in window mode, in terminal mode and after the prefix.
- The Neovim navigator hand-back, when `nvim_navigation` is on.
- `tuios focus-window --direction` and the `FocusDirection` tape command.

In the scrolling layout, up and down wrap inside the column. Left and right
move along the strip and do not wrap. With tiling off, focus does not wrap.
The tmux shim always wraps, as tmux does, and does not read this option.

`persist_scrollback` in `[daemon]` (default `true`) saves each pane's history with its session. After a daemon restart or a reboot, the restored pane shows that history above a dim divider, and the new shell starts under it. `persist_scrollback_lines` (default 1000) and `persist_scrollback_kb` (default 2048) set the most lines and the most KiB one pane saves. The files hold what your panes printed, secrets included. Set `persist_scrollback = false` to stop this. The next time the daemon starts, it deletes the history it saved before. See [SESSIONS.md](SESSIONS.md#pane-history).

The daemon's timings are in `[daemon]` as whole seconds. `0` is the default.
The daemon reads them when it starts, and each has an environment variable,
read at the same time, that wins over the file:

These options are in the [option reference](config-options.md): [`daemon.agent_detect_seconds`](config-options.md#daemon.agent_detect_seconds), [`daemon.agent_stall_seconds`](config-options.md#daemon.agent_stall_seconds), [`daemon.subagent_fade_seconds`](config-options.md#daemon.subagent_fade_seconds), [`daemon.cleanup_seconds`](config-options.md#daemon.cleanup_seconds), [`daemon.pr_poll_seconds`](config-options.md#daemon.pr_poll_seconds), [`daemon.transfer_keep_seconds`](config-options.md#daemon.transfer_keep_seconds), [`daemon.dispatch_ready_seconds`](config-options.md#daemon.dispatch_ready_seconds).

`agent_autodetect` reads `TUIOS_AGENT_AUTODETECT` the same way. A client reads
`TUIOS_CLEANUP_SECONDS` too, for its own buffers.

These timings and limits have no environment variable. `0` is the default, and
a value outside the range moves to the nearer end.

| Option | Default | Range | What it sets |
|--------|---------|-------|--------------|
| `[daemon] save_seconds` | 30 | 10 to 600 | The longest a session whose panes printed waits to be saved. It keeps each pane's folder current for a restore. A change to the layout is saved within 2 seconds either way. |
| `[daemon] exec_timeout_seconds` | 30 | 0 to 3600 | How long a command run with `tuios exec` may take when the caller names no timeout. |
| `[daemon] reconnect_seconds` | 15 | 0 to 300 | How long a client waits for a restarted daemon before it gives up. A daemon that stopped without saying so is waited on for at most 6 seconds. |
| `[daemon] host_heartbeat_seconds` | 15 | 0 to 300 | How long a link to another machine may carry nothing before the daemon pings it. |
| `[daemon] host_heartbeat_timeout_seconds` | 5 | 0 to 60 | How long one ping waits for its reply. Two misses in a row end the link. |
| `[daemon] host_call_timeout_seconds` | 8 | 0 to 120 | How long one listing over a live link may take before the link is dialed again. |
| `[agents] ask_wait_seconds` | 120 | 0 to 3600 | How long `tuios ask-human` waits for your answer when the agent names no timeout. The question stays in the Inbox after the wait. |
| `[agents] undo_window_seconds` | 10 | 0 to 300 | How long after a dismiss, a snooze or a queued reply `u` takes it back. |
| `[files] drop_keep_hours` | 24 | 0 to 8760 | How long a file dropped into a pane of another machine stays in that machine's drop folder. |
| `[files] drop_keep_mb` | 256 | 0 to 65536 | The most MiB the drop folder holds. A drop that does not fit is refused. |

The daemon reads them when it starts. The client reads `reconnect_seconds` and
`undo_window_seconds` from its own config, and a change applies at once. The
time a dial to one machine may take is `connect_timeout` in that machine's
`[hosts.NAME]` table.

`client_follows_upgrade` in `[daemon]` (default `true`) makes a client on this machine follow a live upgrade (`tuios daemon upgrade`). When the new image takes the session back and its build is newer than the client's, the client runs the binary the upgrade named, as `tuios attach` on the same terminal. The session, its panes and the flags you started the client with stay. A client of another machine, over ssh or in a browser, keeps its build. Set it to `false` to keep the old client running; it then says that it still runs the old build.

`window_size` in `[daemon]` sets the size of a session that has more than one client. The names are the names of the tmux `window-size` option. See [SESSIONS.md](SESSIONS.md#session-size-with-more-than-one-client).

| Value | Size of the session |
|---|---|
| `latest` (default) | The client that last had input. A smaller client shows a part of the session. |
| `smallest` | The smallest client. Every client shows the full session. |
| `largest` | The largest client. A smaller client shows a part of the session. |

A client that is larger than the session fills the space the session does not use with dots. A line in that space names the client that sets the size. The rail and the dock stay at the edges of each client's screen.

To change the value of one session while it runs, use `tuios set-config daemon.window_size latest -s NAME`. The value is not saved to the config file. A change in the config file applies when the daemon starts again.

`single_client` in `[daemon]` (default `false`) keeps one client for each session. When a client attaches, every other client of that session detaches. The newest attach wins. Each detached client exits with status 0 and prints "Another client attached to this session." The session continues to run. This is `tuios attach -d` on every attach. Two attaches do not detach the other clients: a view-only client, such as `tuios-web --read-only`, and the attach that tuios makes on its own to get a session back after a host link drops. A change in the config file applies to the next attach.

```toml
[daemon]
single_client = true
```

`ssh_agent` in `[daemon]` (default `"off"`) can be `"follow"`. Then each session has a stable link to the ssh agent socket of the client that attached to the session or used it last. When you ssh to the machine with agent forwarding and run `tuios attach`, your panes use the agent of that ssh connection.

```toml
[daemon]
ssh_agent = "follow"
```

- The link is `agent-<session id>.sock` in the folder of the daemon socket, for example `$XDG_RUNTIME_DIR/tuios/agent-3f2a9c1d-0b7e-4d0a-9c4e-5a1f2b3c4d5e.sock`. `tuios ssh-agent-path` prints it.
- Each new pane of the session gets `SSH_AUTH_SOCK` set to the link.
- A shell that started before you set the option keeps its old value. To use the link in that shell and in every shell, add this line to your shell rc: `p=$(tuios ssh-agent-path 2>/dev/null) && export SSH_AUTH_SOCK="$p"`
- When the client that the link points at detaches, the link moves to the socket of the client before it. When no attached client has a socket, the link points at the `SSH_AUTH_SOCK` of the daemon, if the daemon has one that passes the checks below. Otherwise tuios removes the link.
- The daemon removes its links when it stops and when you set the option to `"off"`. At start, it removes the links that a stopped daemon left.
- tuios uses the `SSH_AUTH_SOCK` of `tuios attach` and `tuios new` only. A client that runs inside a pane does not count. tuios finds such a client by its process, so a process that leaves its pane on purpose (for example, with `setsid` and a clean environment) can still move the link. This check stops mistakes and agents, not a local process that wants to get past it.
- tuios resolves a symbolic link once, so `~/.ssh/agent.sock` and the 1Password agent work. The link then points at the real socket. The real socket must be a Unix socket that you own. Every folder above it must be owned by you or by root, and no other user can write to it, unless it is sticky like `/tmp`. tuios refuses other sockets.
- A client of the tuios SSH server or of `tuios-web` has no agent socket, so it does not move the link. To follow your agent, ssh to the machine with `ssh -A` and run `tuios attach`.
- The agent can follow you to a remote host too. Turn on `ssh_agent = "follow"` on both machines, and turn on agent forwarding for the link to that host. Use `ForwardAgent yes` for the host in `~/.ssh/config`, or `ssh_options = ["-A"]` under `[hosts.NAME]`. tuios does not forward the agent by default. The daemon keeps one link for each such host, `agent-link-<host>-<hash>.sock`. The host name is in lower case and cut to 32 characters, and the hash is of the exact name, so two hosts never share a link. It points at the agent of the person on this machine who attached a session on that host, or typed in one, last. A session of this machine does not move it. The daemon starts the link ssh to that host with `SSH_AUTH_SOCK` set to that link. On the host, the panes of a session you attach through the link use the forwarded agent.
- tuios decides that a host forwards from what `ssh -G` prints for its address with its `ssh_options`. That is how ssh itself reads the options and `~/.ssh/config`. tuios asks again when the config changes, and when `ssh -G` failed the last time. A host that leaves the config takes its link with it.
- Before any client attaches a session on a forwarding host, its link points at the daemon's own `SSH_AUTH_SOCK`. If the daemon has none, or its socket fails the checks above, the panes on that host have no agent until a client attaches. The link ssh to a host that does not forward starts with the environment of the daemon, unchanged. Without forwarding, the panes on the host use the `SSH_AUTH_SOCK` that their own login there sets: the agent of the daemon on the host, or one from keychain or gnome-keyring through PAM, or none.
- If the link to a host shares an ssh master connection, ssh forwards the agent of that master.
- Forward the agent only to a host you trust. Root on the host can use a forwarded agent to sign in to other machines as you while the link is up. If the host pins the hub with `restrict` in `authorized_keys`, add `agent-forwarding` to the options of that key.

A change in the config file applies to the next attach. New panes get the link while the option is on.

`display` in `[daemon]` (default `"follow"`) can be `"off"`. A daemon that started without a display has no `WAYLAND_DISPLAY` or `DISPLAY`. This happens when a host link starts the daemon over ssh. With `"follow"`, the panes of such a daemon get the display of the local client that attached to the session or used it last. Then `wl-copy`, `wl-paste`, `xclip` and image paste work in the panes when you attach from the desktop.

```toml
[daemon]
display = "follow"
```

- A daemon that has a `WAYLAND_DISPLAY` or a `DISPLAY` of its own keeps it. Its panes get the daemon's values, and the option does nothing.
- Each session has a link to the Wayland socket of the client: `wayland-<session id>.sock` in the folder of the daemon socket. A new pane gets `WAYLAND_DISPLAY` set to the full path of the link.
- The link moves to the socket of the newest client. A pane that started under one desktop client still reaches the display after that client detaches and another attaches.
- A new pane also gets `DISPLAY` and `XAUTHORITY` of the client. `DISPLAY` is a name, not a path, so a pane keeps the value it started with.
- A shell that started before a desktop client attached has no display. To get the display in that shell, run this line. To get it in every shell, add it to your shell rc: `eval "$(tuios display-env 2>/dev/null)"`
- When the client detaches, the session uses the display of the client before it. When no attached client has a display, tuios removes the link, and new panes get no display.
- tuios uses the display of `tuios attach` and `tuios new` only. The client must run on this machine, as your user, and outside every pane. tuios ignores a client that comes through a host link, a client of the tuios SSH server and a client of `tuios-web`.
- `WAYLAND_DISPLAY` can be a full path or a name like `wayland-1`. tuios finds a name in the `XDG_RUNTIME_DIR` of the client, then in the one of the daemon, then in `/run/user/<uid>`. The client does not need `XDG_RUNTIME_DIR`.
- The Wayland socket must be a Unix socket that you own. Every folder above it must be owned by you or by root, and no other user can write to it, unless it is sticky like `/tmp`.
- `DISPLAY` must be a display on this machine, `:N` or `:N.S`. Its socket `/tmp/.X11-unix/XN` must be owned by you or by root. tuios ignores a forwarded display such as `localhost:10.0`.
- `XAUTHORITY` must be a file that you own, in folders that pass the same checks.
- The daemon removes its links when it stops and when you set the option to `"off"`. At start, it removes the links that a stopped daemon left.

`keep_awake` in `[daemon]` (default `"off"`) keeps the machine from sleeping while agents work.

| Value | The machine stays awake |
|---|---|
| `off` (default) | Never. The machine sleeps as its own settings say. |
| `agents` | While an agent pane on this machine has the state `working`. |
| `on` | While the daemon runs. |

```toml
[daemon]
keep_awake = "agents"
```

- The daemon holds the machine awake, so it works when no client is attached, over ssh, and on a host.
- On Linux the daemon runs `systemd-inhibit` for idle and sleep. `systemd-inhibit --list` shows a `tuios` row while it holds. On macOS it runs `caffeinate`. On Windows it calls `SetThreadExecutionState`.
- Only sleep is held off. The display can still dim and lock. On a Mac, a closed lid still sleeps.
- A pane whose program runs on another machine, in a global session, does not count. That machine runs the agent.
- With `[agents] enabled = false`, no agent works, so `agents` holds nothing.
- The daemon does no work while nothing changes. It counts the working panes only when the state of a pane changes or a pane closes.
- When the daemon stops, or a live upgrade replaces it, the hold ends. The new daemon takes it again.
- `tuios keep-awake agents` sets the value, applies it at once and saves it to the config file. `tuios keep-awake` shows the hold. `tuios doctor keep-awake` checks that this machine can be held awake. The Daemon tab of the settings has the same option. A change in the config file applies at once.

`[hints]` sets what hints mode (`Ctrl+B F`) labels. See [HINTS.md](HINTS.md).

`[panes]` sets `label_keys`, the keys that the pane labels (`Ctrl+B Q`) use. The default is `1234567890`. Letters `a` to `z` and digits are allowed. See [KEYBINDINGS.md](KEYBINDINGS.md#pane-labels).

`[panes]` also sets `navigator_layout`, the layout the pane navigator (`Ctrl+B /`) opens in: `tree`, `flat` or `cards`. The default is `tree`. Press `v` in the navigator to change the layout. `navigator_search = true` opens it ready to type a search, and `esc` with nothing typed closes it. See [KEYBINDINGS.md](KEYBINDINGS.md#the-pane-navigator).

`[panes] drag_and_drop = true` turns on drag and drop between panes over the kitty drag and drop protocol (OSC 72). A program that speaks it, such as yazi 26.9 or later, can drag files from its pane: hold the left button on a file in terminal mode and carry it to another pane. A pane whose program speaks the protocol takes the drop itself. Any other pane gets the paths as a paste, by the rules of [dragging a file into a pane](#drag-a-file-into-a-pane) below: quoted for the shell in the pane, and refused, with the reason in the label, when that pane could not use a path on this machine. The same rules hold the pane the drag starts from. Dropped text, as against paths, goes only into a pane whose program turned bracketed paste on. tuios reads no file for either side and refuses a drop back into the pane the drag came from. The default is `false`. The protocol is kitty's and still settling, so the switch is off until it is asked for.

`[panes] drag_and_drop_desktop = true`, with `drag_and_drop`, bridges drags to and from the desktop when tuios runs in a terminal that speaks the same protocol itself: kitty 0.47 or later. tuios asks its terminal once at startup and, when it answers, registers as a program that takes drops and offers drags. A file dragged from a file manager or a browser onto the tuios window then lands on the pane under the pointer, by the rules above: a program that speaks the protocol takes it itself, any other pane gets the path as a paste. A file a program in a pane drags, from yazi say, becomes the desktop's drag and can be dropped on any other window; the drag image the desktop shows is the file names and the pane they come from. While the bridge is up the terminal recognises the drag gesture and tuios answers only one whose press went to a pane that offers drags, so moving a pane, selecting text and dragging a row of the rail are as they were. The bridge is for a terminal on this machine: a client over SSH, through tuios-web or attached to another machine's daemon does not register. Both switches take effect without a restart: turning the bridge off takes the registration back, turning it on asks the terminal again. The default is `false`.

`[path_picker]` sets `show_hidden`. With `true`, the path picker (`Alt+;`) shows the names that start with a dot when it opens. The default is `false`. `Alt+H` in the picker shows or hides them. See [KEYBINDINGS.md](KEYBINDINGS.md#pick-a-path).

## Open files from the sidebar

`o` in the sidebar's files section opens the selected file or folder. tuios uses
the system opener: `xdg-open` on Linux, `open` on macOS, `wslview` under WSL,
and the program for the file type on Windows.

Add `[[files.open]]` rules to use another program for some files:

```toml
[[files.open]]
match = "*.pdf"
command = ["zathura", "{path}"]

[[files.open]]
match = "*/"            # every folder
command = ["thunar", "{path}"]
```

- tuios uses the first rule that matches. With no match, it uses the system
  opener.
- `match` is a glob. Case does not matter. A pattern with no `/` matches the
  file name. A pattern with a `/` matches the full path, for example
  `"/home/me/papers/*.pdf"`.
- A `/` at the end of the pattern matches folders only. A pattern without it
  matches files only.
- `command` is a list of words. tuios starts it directly, with no shell.
  `{path}` in a word becomes the path. With no `{path}`, the path goes last.
- tuios starts the command on the machine that runs the client. For a file in
  a listing of another machine, it opens a copy here. See the next section. It
  does not open a file for a remote client.
- When the program stops with an error in the first 3 seconds, a message
  says so. The log has the reason.
- tuios skips a rule with no pattern, a pattern that is not a valid glob, or
  no command, and shows a warning for it when it starts.
- [`appearance.link_opener`](config-options.md#appearance.link_opener) and `$BROWSER` do not apply to files. They name a
  browser for web links.

### Open a file of another machine

`o` on a row of a listing of another machine shows a message: "notes.md is on
build. Click to open a copy here." Click it, or press the prefix and then `j`,
to copy the file or folder here and open the copy. Press `esc` to say not now.

- The copy goes to `~/.cache/tuios/opened/HOST/HASH/NAME`, or under
  `$XDG_CACHE_HOME` when it is set. The hash is of the host and the far path,
  so the same file always goes to the same place.
- The copy uses the same engine as `S` and `tuios cp`. It shows in the Copies
  panel.
- When the far file did not change since the last copy, `o` opens the copy
  here at once. When it changed, tuios copies it again. When the copy here
  changed too, tuios keeps it and opens it. Run
  `tuios transfers clear-opened` to get a new copy.
- tuios does not send changes back. Changes to the copy stay here.
- A folder is copied with all it holds. tuios does not copy links, so a link
  cannot bring in a file from outside the folder.
- Above 200 MiB, or above 1000 files, tuios shows the size and asks again.
  Above 4096 MiB, or above 20000 files, it does not copy. Press `S` to send
  it to a folder here instead.
- tuios replaces each character that a file name cannot hold with `_`. A
  name such as `..\..\x` stays in the folder of its copy.
- tuios deletes a copy that nobody opened for 7 days when the daemon starts,
  and each time `o` makes a copy. `tuios transfers clear-opened` deletes them
  all now.

```toml
[files]
open_copy_ask_mb = 200      # ask first above this size, in MiB
open_copy_max_mb = 4096     # do not copy above this size, in MiB
open_copy_keep_days = 7     # -1 keeps every copy
```

## Drag a file into a pane

[`appearance.sidebar.file_drag`](config-options.md#appearance.sidebar.file_drag) lets you drag a row of the files section onto a
pane. The pane gets the path, quoted for the shell in the pane. It is on by
default. `I` on a row does the same for the focused pane, with this option on
or off. See
[KEYBINDINGS.md](KEYBINDINGS.md#drag-a-file-into-a-pane).

```toml
[appearance.sidebar]
file_drag = false
```

`[scratch]` sets the size of the box in which `Ctrl+B g` shows the scratch terminal. The old `session` key is no longer used. See [SESSIONS.md](SESSIONS.md#the-scratch-terminal).

`[pip]` sets the size (`width`, `height`, border included, in cells or a percent such as `"30%"`, default 40x12) and the first `corner` (default `bottom-right`, also `top-center` and `bottom-center`) of the picture-in-picture view that `p` pins. See [SESSIONS.md](SESSIONS.md#picture-in-picture).

`[[keybindings.command]]` binds a key to a command that you write: a scratch terminal, a popup, a pane or a command with no window. A mistake in an entry is a warning, and tuios ignores that entry. See [KEYBINDINGS.md](KEYBINDINGS.md#command-keys).

`[[actions]]` declares an action of your own, `user:NAME`: a built-in action, a program with its arguments (placeholders such as `{pane.cwd}` fill one argument each, and no shell reads them), or a plugin action. A key, a menu row or `tuios actions run` runs it. See [KEYBINDINGS.md](KEYBINDINGS.md#your-actions).

`[menus.NAME]` declares a menu: a title and a list of rows, each an action or a submenu with an optional `key`, `label` and `when`. `open_menu:NAME` opens it, as a which-key panel from a key and in place from a right-click menu. The target names (`pane`, `session`, `file` and the others) are the shipped right-click menus: `add` puts rows after the row `after` names, `remove` takes rows out, and `items` replaces them. An action's `targets` and `when` decide where its row shows. The which-key panel shows the rows in the order you write them, and a `"---"` row starts a new group. The panel title is the `title` as you write it. tuios ships one menu of its own, `start`: a short menu in plain words with New terminal, Split, Close, Find a file, Select and copy text, All commands, Settings and Help. Bind `open_menu:start` to a key to open it. A `[menus.start]` of your own replaces it. See [KEYBINDINGS.md](KEYBINDINGS.md#your-menus).

`[keybindings.modes.NAME]` declares a key mode: a set of keys that stays on until `esc` or `leave_mode`. `enter_mode:NAME` turns it on. `exclusive` drops the keys the mode does not bind, and `once` turns the mode off after one key. Modes stack, and the dock shows a chip while one is on. See [Key modes](KEYBINDINGS.md#key-modes).

## Animation speed

[`appearance.animation_speed`](config-options.md#appearance.animation_speed) is the percent window animations run at: minimize
and restore, snap, swap, zoom and the workspace slide. 100 is the speed tuios ships with. 200 runs
them in half the time, and 50 in twice the time.

```toml
[appearance]
animation_speed = 200   # a number from 25 to 400
# animation_speed = 0   # 100, the default
```

A value outside 25 to 400 moves to the nearest end. `appearance.motion = "none"`
still turns the animations off, whatever the speed. Overlay fades and the copy
sweep keep their own timing. The copy sweep uses [`appearance.selection.flash_ms`](config-options.md#appearance.selection.flash_ms).

## Calm mode

[`appearance.calm`](config-options.md#appearance.calm) turns calm mode on. Nothing moves or blinks, and only the
messages that need you show in the dock. `Ctrl+B Z` (`toggle_calm`) turns it
on and off and saves the choice here.

```toml
[appearance]
calm = true   # default: false
```

Calm mode and `appearance.motion = "none"` also stop the enter, exit and
skeleton effects of TML surfaces. A surface then shows each part at once.

Calm mode sits over [`appearance.motion`](config-options.md#appearance.motion). When you turn calm mode off, the
motion level you set comes back. See
[KEYBINDINGS.md](KEYBINDINGS.md#calm-mode) for what calm mode changes.

## Motion

The `[motion]` table turns each motion on or off by itself and sets how long it
takes. [`appearance.motion`](config-options.md#appearance.motion) and [`appearance.animation_speed`](config-options.md#appearance.animation_speed) stay on top.
`appearance.motion = "none"` and calm mode stop every motion below. The speed
divides every length.

These options are in the [option reference](config-options.md): [`motion.workspace`](config-options.md#motion.workspace), [`motion.layout`](config-options.md#motion.layout), [`motion.zoom`](config-options.md#motion.zoom), [`motion.overlays`](config-options.md#motion.overlays), [`motion.sidebar`](config-options.md#motion.sidebar), [`motion.scroll`](config-options.md#motion.scroll), [`motion.copy`](config-options.md#motion.copy), [`motion.confetti`](config-options.md#motion.confetti).

`workspace`, `layout`, `zoom`, `overlays` and `scroll` also take `<name>_ms`,
the length in milliseconds at speed 100, from 0 to 1000. 0 is a cut. The
defaults are 200 for `workspace` and `zoom`, 300 for `layout` and `scroll`, and
120 for `overlays`. `layout` also sets the snap and swap length, which is
two thirds of `layout_ms`.

```toml
[motion]
workspace = "fade"
workspace_ms = 250
zoom = "off"        # zoom is a cut. The workspace switch still fades.
```

The old keys [`appearance.workspace_transition`](config-options.md#appearance.workspace_transition), `workspace_transition_ms` and
`zoom_animation` still read. Tuios folds them into [`motion.workspace`](config-options.md#motion.workspace),
[`motion.workspace_ms`](config-options.md#motion.workspace_ms) and [`motion.zoom`](config-options.md#motion.zoom) on load. A `[motion]` key that is set
wins.

## Confirm a pane close

[`appearance.confirm_close`](config-options.md#appearance.confirm_close) sets when closing a pane by hand asks first: with
`close_window`, `close_window`, the pane menu's `Close pane` or the
close control in the title bar.

- `never` closes the pane at once. This is the default.
- `busy` asks when the pane runs something besides its shell.
- `always` asks for every pane.

```toml
[appearance]
confirm_close = "busy"
```

The commands, such as `tuios close-window`, never ask. See
[KEYBINDINGS.md](KEYBINDINGS.md#ask-before-a-pane-closes).

## Theme transition

[`appearance.theme_transition`](config-options.md#appearance.theme_transition) sets how tuios shows a change of theme. The
change can come from the theme picker, `tuios set-config`, a config reload or
the Omarchy theme.

- `none` changes the colors in one frame. This is the default.
- `circle` grows the new theme from the middle of the screen, over the old one.
- `fade` changes every cell from the old colors to the new colors.

```toml
[appearance]
theme_transition = "circle"
theme_transition_duration = 450   # milliseconds, from 100 to 2000
```

[`appearance.animation_speed`](config-options.md#appearance.animation_speed) also applies to the duration. A value outside 100
to 2000 moves to the nearest end. `appearance.motion = "none"` turns the
transition off. The `fade` transition needs a truecolor terminal. On a terminal
with fewer colors, tuios changes the colors in one frame. While the transition
runs, the screen outside the circle does not change.

## Frame rate

[`appearance.max_fps`](config-options.md#appearance.max_fps) is the highest frame rate tuios draws at. Your terminal
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

## How frames reach the terminal

[`appearance.cell_frames`](config-options.md#appearance.cell_frames) picks which clients write their frames as cells. Such
a client compares each frame with the last one and sends the terminal only the
cells that changed. The other clients turn each frame into text, and Bubble
Tea turns the text back into cells before it compares. The result on the
screen is the same; the cell way takes about half the CPU of a keystroke's
frame.

```toml
[appearance]
cell_frames = "all"      # the default
# cell_frames = "served" # only clients over SSH and the web
# cell_frames = "off"    # no client
```

| Value | Clients that write cells |
| --- | --- |
| `"served"` | Clients over `tuios ssh`, `tuios-web` and the browser tour. |
| `"all"` | Those, and the terminal you run `tuios` in, except on Windows. This is the default. |
| `"off"` | None. |

The environment variable `TUIOS_CELL_FRAME` overrides the option: `1` means
`all` and `0` means `off`. A change to the option applies to the next client
that starts.

If the screen looks wrong, for example text in the wrong place or stale cells
after a redraw, set `cell_frames = "off"` or start tuios with
`TUIOS_CELL_FRAME=0`. That brings back the way frames were sent before the
default became `all`. Please report the problem with your terminal's name and
version.

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

## Images on a terminal without graphics

A program in a pane can draw a picture with sixel or with kitty graphics.
Your terminal shows the picture when it supports sixel or kitty graphics.
Some terminals support neither: kmscon, the Linux console, xterm.js hosts
such as VS Code, and many SSH clients. On these, tuios draws the picture with
block characters. Each character cell shows two colours. The picture scrolls
and clears with the text, and a kitty delete (`a=d`, of every image or of one
id) takes it down. Kitty Unicode placeholders (`U=1`) are not drawn this way.

[`appearance.image_symbols`](config-options.md#appearance.image_symbols) sets the characters.

```toml
[appearance]
image_symbols = "auto"    # the default
# image_symbols = "octant"
# image_symbols = "off"   # show a box instead of the picture
```

| Value | Characters | Detail in one cell |
| --- | --- | --- |
| `"auto"` | Chosen from `TERM`. See the next table. | |
| `"octant"` | Octants, Unicode 16 (U+1CD00 and on) | 2 by 4 |
| `"sextant"` | Sextants, Unicode 13 (U+1FB00 and on) | 2 by 3 |
| `"quadrant"` | Quadrants (▖ ▗ ▘ ▝ and others) | 2 by 2 |
| `"half"` | Half blocks (▀ ▄) | 1 by 2 |
| `"off"` | None. tuios shows a box with "image" in it. | |

A finer set shows more detail, but your font must have the characters. A
terminal cannot tell tuios which characters its font has. So `auto` reads
`TERM`:

| `TERM` | `auto` uses | Why |
| --- | --- | --- |
| `kmscon` | Octants | kmscon draws with its built-in Unifont, which has the octants. |
| `linux` | Half blocks | The Linux console uses fonts of 256 or 512 characters. These fonts have the CP437 block characters and nothing finer. |
| Anything else | Quadrants | Quadrants are in the Basic Multilingual Plane. Every font with block characters has them. |

If your font has octants, set `"octant"` for the best picture.

**Colours.** tuios uses the colours your terminal shows.

- 24-bit colour: each cell gets the two colours that fit the picture best.
- 256 colours: tuios mixes palette colours in a dither pattern.
- 16 colours, for example the Linux console: tuios always uses half blocks.
  It mixes the 16 colours in a dither pattern. It uses only the 8 dark
  colours as a background, because the Linux console uses bright
  backgrounds for blink. If a picture keeps too little of its shape in 16
  colours, tuios shows the box instead. On the pictures measured, this
  did not happen. See [KMSCON-GRAPHICS.md](KMSCON-GRAPHICS.md).

**What the program sees.** With block characters on, tuios tells the
program in the pane that sixel works. Programs such as chafa, timg and yazi
then send sixel. With `"off"`, tuios tells the program that sixel does not
work, and the program uses its own text output.

A terminal with sixel or kitty graphics always gets the real picture. It
never gets block characters.

tuios does not draw kitty graphics images as block characters yet. On a
terminal without graphics, a program that only uses kitty graphics uses its
own text output.

## Mermaid diagram colours

`tuios mermaid` builds its colours from your tuios theme. `[mermaid] theme`
picks one of the named diagram themes instead. The `--theme` flag wins over it.

`[mermaid] background` is `theme` (the default) or `none`. With `none`, a kitty
or png picture has no background, so the diagram is on the background of your
terminal. The `--background` flag wins over it.

```toml
[mermaid]
theme = "flexoki"   # tuios (the default), pele, flexoki, flexoki-dark, nord,
                    # everforest, gruvbox, rose-pine, catppuccin-mocha,
                    # catppuccin-latte, tokyo-night, dracula, solarized-light, kanagawa
background = "none" # theme (the default) or none
```

## CharmTone themes

tuios has two built-in themes made from
[CharmTone](https://github.com/charmbracelet/tone), the colour system of Charm.

| Theme | Ground | Text | Accent |
|-------|--------|------|--------|
| `charmtone` | Pepper `#201F26` | Sash `#ECEBF0` | Charple `#6B50FF` |
| `charmtone-light` | Salt `#F7F6FB` | Char `#3A3943` | Charple `#6B50FF` |

```toml
[appearance]
theme = "charmtone"
background = "theme"   # paint the Pepper ground behind the panes
```

The dark theme uses the ANSI colours of Crush, the coding agent from Charm.
Bright black is Squid, so that shell suggestions and comments are easy to read.
The light theme uses a darker shade of each colour.
CharmTone has no dark yellow, so yellow is Cumin, a light brown.
Run `tuios list-themes charmtone-light` to see the contrast of each colour.

## Omarchy themes

On [Omarchy](https://omarchy.org), tuios uses the Omarchy theme when you set
no theme. The panes, the chrome, the rail and the dock take the colours of the
applied theme. The answers to OSC 10, 11 and 4 in a pane are the theme's
colours too.

When you switch the Omarchy theme, tuios changes its colours at the same
moment as your terminal. You do not restart tuios and you do not press a key.
A program in a pane that turned on mode 2031 gets a light or dark report each
time the theme changes. tuios sends no SIGWINCH. See
[Programs That Follow the Theme](THEMES.md#programs-that-follow-the-theme).
On a machine without Omarchy, tuios watches nothing.

| [`appearance.theme`](config-options.md#appearance.theme) | On a machine with Omarchy | On a machine without Omarchy |
|---|---|---|
| empty (the default) | The Omarchy theme | The colours of your terminal |
| `omarchy` | The Omarchy theme | The colours of your terminal, and a warning |
| `none` | The colours of your terminal | The colours of your terminal |
| a theme id | That theme | That theme |

```toml
[appearance]
theme = "omarchy"   # use the Omarchy theme, and warn where Omarchy is missing
```

[`appearance.theme_light`](config-options.md#appearance.theme_light) names a second theme for a light terminal. `theme` is
then the dark theme. See
[THEMES.md](THEMES.md#a-light-and-a-dark-theme).

tuios reads `~/.local/state/omarchy/current/theme/colors.toml` (Omarchy 4),
then `~/.config/omarchy/current/theme/colors.toml` (Omarchy 3). The pane
colours are the ones Omarchy gives kitty, ghostty and foot. The chrome takes:

| Chrome | Omarchy key |
|---|---|
| Accent | `accent` |
| Focused border in window mode | the first colour of `hyprland_active_border`, else `accent` |
| Terminal mode, copy mode, errors, notices | `green`, `yellow`, `red`, `blue` |
| Dialogs | `lighter_background`, with `dark_background` and `darker_background` below it |

Each installed Omarchy theme is also in the theme list as `omarchy/NAME`, such
as `omarchy/gruvbox`. Such an id is fixed. It does not change with the Omarchy
theme.

`tuios list-themes` shows the Omarchy theme it found, and whether tuios uses
it:

```
active: omarchy (default)
omarchy: Tokyo Night, from /home/you/.local/state/omarchy/current. tuios follows it.
```

`SWATCH_OMARCHY_DIR` names another `current` directory to read.

## Colour values

Every colour key in tuios takes the same values: the backgrounds, the border colours, `overlay_border_color`, `scrollbar.tint`, `sidebar.cursor_background`, `[appearance.selection]`, the dock's `fg` and `bg` keys, `[dock.tabs]`, the agent row's `fg`, and a theme file's `chrome` object.

| Value | Example | What it is |
|---|---|---|
| Hex | `"#7dcfff"`, `"#7cf"` | That colour on every theme. |
| ANSI number | `"5"`, `"208"` | `0` to `15` are the sixteen slots below. `16` to `255` are the 256-colour palette. |
| Slot name | `"magenta"`, `"bright_magenta"`, `"fg"`, `"bg"` | One of the theme's sixteen colours, or its foreground or background. `purple` is the same slot as `magenta`. |
| Role name | `"accent"`, `"warning"`, `"surface"` | The colour the chrome uses for that role. See the list below. |
| CharmTone name | `"charple"`, `"guac"` | A colour from Charm's CharmTone palette. |

The role names are `accent`, `accent_bright`, `success`, `warning`,
`error`, `info`, `surface`, `canvas`, `panel` and `card`, the roles a
theme's `chrome` object sets (see [THEMES.md](THEMES.md)). The roles for one
piece of chrome have a dotted name: `border.unfocused`, `border.copy`,
`border.multifocus`, `rule`, `status.attention`, `status.busy`,
`dots.close`, `dots.minimize`, `dots.zoom`, `dock.ground` and `rail.ground`.
`text`, `dim` and `muted` are the three text inks. You can use them, but a
theme cannot set them.

A slot name or a role name follows the theme. Change the theme and the
colour changes with it. At 16 colours, a slot name is the terminal's own
slot, so your terminal's palette paints it. A hex colour stays the same on
every theme.

```toml
[appearance]
border_focused_color = "accent"          # the theme's accent, on every theme
dock_background      = "surface"

[appearance.selection]
bg = "bright_black"

[dock.tabs]
active_bg = "accent"
inactive_fg = "muted"
```

Case does not matter, and `bright-magenta` is the same as
`bright_magenta`. A key that has its own words keeps them: `off` and
`theme` for a background, and `quiet`, `border` and `muted` for the
scrollbar tint. There `muted` is the tint word, not the text ink. tuios
reads each value when the config or the theme changes, not on each frame.
A value that is not a colour is ignored, and the validator says what to
write instead.

## Backgrounds

A cell that has no background of its own is transparent, so your terminal's
own background shows through it. That is the default everywhere: pane content
a program left on the default background, the space between panes, the
borders, the dock and the rail. The background options paint those cells
instead, one surface at a time or all at once.

These options are in the [option reference](config-options.md): [`appearance.background`](config-options.md#appearance.background), [`appearance.pane_background`](config-options.md#appearance.pane_background), [`appearance.desktop_background`](config-options.md#appearance.desktop_background), [`appearance.window_chrome_background`](config-options.md#appearance.window_chrome_background), [`appearance.dock_background`](config-options.md#appearance.dock_background), [`appearance.sidebar.background`](config-options.md#appearance.sidebar.background).

A theme can name a ground for the dock and the rail (`chrome.dock.ground` and
`chrome.rail.ground`, see [THEMES.md](THEMES.md#roles-for-one-piece-of-chrome)).
The theme's ground shows when `dock_background` or `sidebar.background` is
empty and `background` is `off`. A value in either key wins.

What each value paints:

| Value | What is painted |
|---|---|
| `off` | Nothing. The cell is transparent and your terminal's own background shows through. |
| `theme` | The active theme's background, with the theme's foreground on text left in the default colour. With no theme set there is no theme background, so it behaves as `off`. |
| A [colour](#colour-values) | That colour. With a theme set, default-coloured text takes the theme's foreground, lifted until it reads on the colour; with none, it keeps your terminal's own. |

**Precedence.** A surface's own option wins whenever it holds a value, `off`
included. Left empty, it follows [`appearance.background`](config-options.md#appearance.background). So one line paints
everything, and a second line changes or clears one surface:

```toml
[appearance]
background = "theme"          # every surface on the theme's background
dock_background = "#11111b"   # the dock a shade darker
pane_background = "off"       # panes keep the terminal's own background

[appearance.sidebar]
background = "#181825"
```

A value that is not a keyword or a colour resolves to `off` for that
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
its own window, so its pane stays empty. Set [`launcher.gui_command`](config-options.md#launcher.gui_command) to start
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

A program from the launcher closes its pane when it exits. Set
[`launcher.keep_open`](config-options.md#launcher.keep_open) to keep the pane. A line under the output shows the exit
status, as with `tuios new-window --keep-open`. Close the pane when you are
done with it. The option needs a daemon session. With `--standalone` the pane
closes as before.

```toml
[launcher]
keep_open = true
```

## Workspace numbers

A workspace's number is its position. Moving a workspace with
`move_workspace_left` or `move_workspace_right`, or dragging its tab in the
dock, renumbers it. A workspace exists while it holds a pane or is on screen.
When a workspace that held a pane stops existing, because its last pane
closed or moved away and it is not on screen, it loses its name.

The numbers close up only when you change something: close a pane, move a
pane to another workspace, close a workspace or move one. Then every
workspace above an empty place moves down one: closing workspace 2 of 1, 2
and 3 leaves 1 and 2. A pane whose program exits on its own (a command
window with `close_on_exit`, a `tuios xpanes` run, an agent finishing)
leaves the numbers alone, so a script or agent that holds a workspace number
still names the same workspace. Its place stays empty until your next close
or move closes it up. Typing `exit` in a pane's shell is a close. An empty workspace you only looked at goes without
moving anything.

`Alt+N` with a number past the last workspace that holds a pane opens the
next one: with three workspaces, `Alt+5` opens workspace 4. When the
workspace on screen is empty, it is the new one, and `Alt+N` to an unused
number stays on it. A pane put directly on a higher number, with
`tuios new-window --workspace 7` or `send_to_workspace_7`, keeps that number
until the numbers below it close up.

### Starting at zero

Workspaces are numbered from 1 by default. To number them from 0, like tmux's
`base-index`, set:

```toml
[workspaces]
base_index = 0
```

The first workspace is then shown as 0 and the one after it as 1, up to 8.
Every workspace digit key moves with the number: `Alt+0` reaches the first
workspace, `Alt+1` the second, and the same shift applies to
`move_and_follow`, `send_to_workspace`, the workspace prefix chords and the
GUI keys. Edit the configuration file and reload it or restart tuios to change
this option; it is resolved together with the default keybindings when loaded.

This only changes the number a person sees and presses. tuios, its daemon and
tape scripts still number workspaces from 1 internally, so a command such as
`tuios new-window --workspace 7` keeps using that internal numbering.
A binding you set yourself is left alone; only the shipped workspace digit
bindings move.

## When a workspace becomes empty

When the last pane on the workspace on screen closes, tuios shows the
workspace you came from. This happens when the pane's program stops, when you
close the pane, when `tuios xpanes -ss` closes it, and with
`tuios close-workspace` or the `close_workspace` action.

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
dock, go-to, the pane navigator or the command palette.
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

## Paste preview

An image paste (`Ctrl+B V`, the paste key when the clipboard holds only an
image, or the empty paste some terminals send for an image) writes the image
to a file and pastes the file's path. tuios then shows
the image in a small card at the pane's cursor, so you can see what the path
holds:

- The picture is drawn with kitty graphics when your terminal has them, and
  with block glyphs when it does not.
- The line under it names the size, the type and the file.
- The card takes no input. The next key or click takes it down and still goes
  where it was going, and so does a key sent with `tuios send-keys`. A panel
  opened over it, or the pane leaving the screen, takes it down too.
  Otherwise it goes after six seconds.

```toml
[appearance]
paste_preview = false   # paste the path alone; the default is true
```

## Paste buffers

tuios keeps your recent yanks as paste buffers. `Ctrl+B ]` pastes the newest,
and `Ctrl+B #` shows them all (see [KEYBINDINGS.md](KEYBINDINGS.md#paste-buffers)).

```toml
[paste_buffers]
limit = 20     # how many unnamed buffers to keep. 0 keeps none.
max_kb = 16384 # most KiB all buffers hold together, at most 262144. 0 uses 16384.
```

The rules are the tmux rules. A yank makes a buffer that tuios names
`buffer0`, `buffer1` and so on. `limit` counts only these buffers, and past it
the oldest of them goes. A buffer that you name with `set-buffer -b` stays
until you delete it, or until the buffers pass `max_kb`. Past `max_kb` the
oldest unnamed buffer goes first, then the oldest named one. tuios does not
keep a yank that is larger than `max_kb` as a buffer, but the yank still goes
to the clipboard. With `limit = 0`, a yank goes only to the
clipboard.

The daemon keeps the buffers in memory, so every client and every session
shares them. They go when the daemon stops. The daemon reads the setting when
it starts and when the file changes. A smaller limit drops the oldest buffers
at once.

A buffer is yours or one pane's. Your yanks, and what you set from outside
every pane or from a pane that holds `admin`, are yours. What a pane without
`admin` sets is that pane's own. A buffer can hold a secret that you copied,
so a pane without `admin` sees and pastes only its own buffers: not yours,
and not another pane's, in its session or not. It cannot change or replace a
buffer that you set, and it cannot make a named buffer, so a name you keep a
command under stays yours. From inside a pane, reading its buffers needs the
`read` grant, changing them needs `write`, and pasting needs both. See
[What a pane may do](#what-a-pane-may-do).

Large requests to the daemon, such as a big `set-buffer`, a `stash put` or an
image paste, take memory from a budget. You, outside every pane or in a pane
that holds `admin`, have a budget of your own. A pane without `admin` shares
another, so it cannot use up yours.

One thing a pane can still learn: the automatic buffer numbers (`buffer0`,
`buffer1`) come from one counter for everybody. A gap between two of a pane's
own buffer numbers tells it that other buffers were made in between, such as
your yanks. It tells the pane how often you copy, not what. tuios accepts this
as a low risk.

## Master-stack layout

These options shape the master-stack layout. Each workspace starts with them.
The layout keys change the workspace on screen, and the workspace keeps the
change. See [LAYOUT_MODES.md](LAYOUT_MODES.md#master-stack-layout).

These options are in the [option reference](config-options.md): [`appearance.master_position`](config-options.md#appearance.master_position), [`appearance.master_count`](config-options.md#appearance.master_count), [`appearance.master_ratio`](config-options.md#appearance.master_ratio), [`appearance.master_grid`](config-options.md#appearance.master_grid).

This example puts an editor in the middle with terminals on both sides. It
keeps that layout at every pane count:

```toml
[startup]
layout = "master-stack"

[appearance]
master_position = "center"
master_ratio = 50
```

`[startup] layout` applies to a new session, and a new workspace takes the layout of the workspace you open it from.

With `center`, the grid is off, so `master_grid` has no effect. The first pane
of the workspace is the master. To make another pane the master, focus it and
press `Ctrl+B L Enter`.

## Start session and start pane

Two `[startup]` options choose where a plain `tuios` opens. Both are empty by
default.

These options are in the [option reference](config-options.md): [`startup.session`](config-options.md#startup.session), [`startup.window`](config-options.md#startup.window).

```toml
[startup]
session = "home"
window = "start"
```

`tuios attach`, `tuios new` and every other command use the session you give
them, and ignore these options. [`startup.session`](config-options.md#startup.session) uses the daemon even when
[`startup.daemon`](config-options.md#startup.daemon) is `false`. `--standalone` and `TUIOS_NO_DAEMON=1` still run a
local session. Both options are on the settings page, in the Startup section.
See [SESSIONS.md](SESSIONS.md#start-session-and-start-pane).

## Scrolling layout

These options shape the scrolling layout. See
[LAYOUT_MODES.md](LAYOUT_MODES.md#scrolling-layout).

These options are in the [option reference](config-options.md): [`appearance.scroll_column_width`](config-options.md#appearance.scroll_column_width), [`appearance.scroll_column_max`](config-options.md#appearance.scroll_column_max), [`appearance.scroll_column_presets`](config-options.md#appearance.scroll_column_presets).

`scroll_cycle_width` sets the focused column to the next width in the list
that is larger than the current width. After the largest width, it goes back
to the smallest. tuios sorts the list. It skips a width that is larger than
`scroll_column_max`. It ignores a value that is not from 1 to 100 and shows a
config warning. This example cycles a column through a half, two thirds and the
full screen:

```toml
[appearance]
scroll_column_width = 50
scroll_column_max = 100
scroll_column_presets = [50, 67, 100]
```

A change to the list applies when tuios reloads the config. The settings page
does not show this option. Edit it in `config.toml`.

## Drop preview

While you drag a tiled pane in BSP, tuios shows where the pane would land if you let go. See [LAYOUT_MODES.md](LAYOUT_MODES.md). The preview is a frame in the accent colour, drawn with the same border characters as the panes. With `fill`, the area inside the frame gets one tint, and the text under it fades into that tint. On a 16-colour terminal the frame is reverse video and the area inside stays as it is. Two options shape that preview:

These options are in the [option reference](config-options.md): [`appearance.drop_preview`](config-options.md#appearance.drop_preview), [`appearance.drop_preview_delay`](config-options.md#appearance.drop_preview_delay).

```toml
[appearance]
drop_preview = "fill"
drop_preview_delay = "250ms"
```

Neither option changes the drop: with `off`, releasing on an edge still splits that pane, and releasing in the middle still swaps. Both are on the settings page, in the Appearance section, and `tuios set-config appearance.drop_preview off` applies at once. A value that is not one of the above reads as the default, with a warning when the config loads, and `tuios set-config` refuses it.

## Rounded dock pills

[`appearance.dock_pill_caps`](config-options.md#appearance.dock_pill_caps) puts rounded caps on every pill in the dock: the
mode chip, the workspace tabs and the minimized windows. It is `true` by
default. Set it to `false` for flat pills:

```toml
[appearance]
dock_pill_caps = false
```

Under ASCII glyphs the workspace tabs have no caps. The option also controls
the ends that `[dock.tabs] caps` puts on flat tabs. The rail's pills do not
change with this option.

## Mode pill icons

The mode pill at the start of the dock shows an icon for the mode. Three
options set the icons:

These options are in the [option reference](config-options.md): [`appearance.dock_mode_icon_window`](config-options.md#appearance.dock_mode_icon_window), [`appearance.dock_mode_icon_terminal`](config-options.md#appearance.dock_mode_icon_terminal), [`appearance.dock_mode_icon_tiling`](config-options.md#appearance.dock_mode_icon_tiling).

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
  `H`. Set `appearance.dock_mode_split_letter = false` to show the tiling icon
  alone. With the default icons, the pill then has the same width as the pill
  of the leader key, so the workspace tabs do not move when you press the
  leader key.
- [`appearance.dock_mode_pill`](config-options.md#appearance.dock_mode_pill) sets what the pill shows while tiling is on.
  `tiling` (the default) shows the tiling icon in terminal mode and in window
  mode. `input` shows the terminal icon or the window icon, and the split
  letter after it shows that tiling is on.

```toml
[appearance]
dock_mode_pill = "input"
```

To remove the whole mode pill, remove `"mode"` from the `left` list in the
`[dock]` table. See [The dock's components](#the-docks-components).

You can also set the icons on the settings page, in the Dock section, or with
`tuios set-config dock_mode_icon_window WM`. The word `default` puts an icon
back to the icon of the glyph set, in the settings, with set-config and in
`config.toml`. The change applies at once.

## Prefix indicator

When you press the leader key, the mode pill shows a keyboard icon on the
warning colour until you press the next key. With `use_ascii_only`, the icon is
`P`. [`appearance.dock_prefix_indicator`](config-options.md#appearance.dock_prefix_indicator) sets where the armed leader shows:

| Value | What it does |
| --- | --- |
| `pill` | The mode pill shows the armed leader. This is the default |
| `badge` | A `PREFIX` badge shows in the corner across from the dock. The mode pill does not change |

```toml
[appearance]
dock_prefix_indicator = "badge"
```

The badge shows for each value when the dock is hidden or has no mode pill.
The option is on the settings page, in the Dock section.
## Workspace label cap

[`appearance.dock_workspace_label_max`](config-options.md#appearance.dock_workspace_label_max) caps a workspace tab's label in cells,
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

## Workspace name on hover

[`appearance.dock_workspace_tooltip`](config-options.md#appearance.dock_workspace_tooltip) shows the workspace name above a pill
when you hover it. The name shows when the pill cuts the name short. It also
shows when [`appearance.dock_workspace_tab_format`](config-options.md#appearance.dock_workspace_tab_format) has no `{name}`, for
example with number-only tabs. A workspace with no name shows its number,
but only when the tab format has no `{index}`. The option is on by default.

```toml
[appearance]
dock_workspace_tab_format = "{index}"
dock_workspace_tooltip = true
```

## Compact dock

The dock has two rows by default: a rule and the row of pills.
[`appearance.dock_compact`](config-options.md#appearance.dock_compact) removes the rule, so the dock has one row. The panes
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

## Window titles

A pane's title sits on its border. Two options set its shape and its place:

These options are in the [option reference](config-options.md): [`appearance.window_title_style`](config-options.md#appearance.window_title_style), [`appearance.window_title_align`](config-options.md#appearance.window_title_align).

```toml
[appearance]
window_title_style = "plain"
window_title_align = "center"
```

[`appearance.window_title_position`](config-options.md#appearance.window_title_position) sets the row: `top`, `bottom` or
`hidden`. The caps of `pill` come from the glyph set. Both options are on the
settings page, in the Appearance section, and `tuios set-config` applies them
at once.

### The title of a screenshot

A screenshot with a frame draws a title bar. [`screenshot.title_format`](config-options.md#screenshot.title_format) sets its
text, with three tokens:

| Token | Becomes |
|---|---|
| `{title}` | The window's title as its title bar shows it: the pane's name through [`appearance.window_title_format`](config-options.md#appearance.window_title_format) |
| `{index}` | The window's place, from 1 |
| `{cwd}` | The window's folder |

```toml
[screenshot]
title_format = "{title} in {cwd}"
```

The default is `{title}`, the same title the window has on screen. An empty
format draws no title. In a capture of the whole screen or of a region,
`{title}` is "screen" or "region", and `{index}` and `{cwd}` are the focused
window's. An unknown token, such as `{name}`, is shown as written, and tuios warns about
it when it loads the file.

## Host terminal title

[`appearance.host_title`](config-options.md#appearance.host_title) sets the title of the terminal tuios runs in, such as
Ghostty's title bar, from the focused pane. It follows the focus, the
workspace and the session, and the focused pane's own title changes. It is
tmux's `set-titles`. Empty, the default, sets no title. Emptied while a client
is attached, it clears the title it set, as a client does when it detaches.

| Placeholder | What it holds |
|---|---|
| `{title}` | the focused pane's name, as its title on the border shows it |
| `{index}` | the pane's place in its workspace, from 1 |
| `{cwd}` | the pane's working directory |
| `{session}` | the session's name as the rail shows it, its display name if it has one, or `tuios` without a daemon |
| `{workspace}` | the workspace's name, else the name its active pane gave with OSC 1, else its number. With a scratch group open, the workspace it is shown over |

```toml
[appearance]
host_title = "{title} · {session}"
```

With no focused pane on the workspace in view, the title is the session's
name. So it is when the template uses `{title}` and the pane has no name, as a
fresh shell has none, and when the template comes out empty. A title a tool
set with herdr's `terminal title set` wins while it is set. Each client sets
the title of its own terminal. The option is on the settings page, in the
Appearance section, and `tuios set-config` applies it at once.

## Focused border colour

The focused pane's border shows the mode. By default it is the theme's cyan
in window mode and its green in terminal mode. Three keys set it.

These options are in the [option reference](config-options.md): [`appearance.border_focused_color`](config-options.md#appearance.border_focused_color), [`appearance.border_focused_color_window`](config-options.md#appearance.border_focused_color_window), [`appearance.border_focused_color_terminal`](config-options.md#appearance.border_focused_color_terminal).

```toml
[appearance]
border_focused_color_window = "accent"
border_focused_color_terminal = "#ff9e64"
```

- A key for one mode wins over `border_focused_color` in that mode.
- An empty key uses the next key. After the three keys, the theme sets the colour.
- `border_focused_color` also replaces the colours for copy mode and for an
  agent that waits. The keys for one mode do not.
- On many themes the cyan and the green have the same lightness. The default
  terminal mode colour then moves darker, or lighter when darker is too faint.
  You can then see the mode without the hue.
- The default unfocused border is darker than both focused borders on a dark
  background, and lighter on a light one. You can see focus without the hue.
- Each default focused border has at least twice the contrast of the unfocused
  border against the background.
- With no theme, the default unfocused border is a grey near your terminal
  background. The focused borders use a saturated cyan and green.
- With [`appearance.session_border`](config-options.md#appearance.session_border) on, the focused border in window mode uses
  the session colour. In terminal mode it uses the terminal mode colour.
  Terminal mode stands further from the background and is at least as
  saturated, so it is the more visible of the two. The session colour moves
  toward the background to make the step.
- At 16 colours the default borders use bright cyan and bright green, the
  slots of your terminal's palette.
- The settings page has a row for each key, in the Appearance section.

## Focused border gradient

[`appearance.border_focused_gradient`](config-options.md#appearance.border_focused_gradient) draws the focused pane's border in two
or more colours that blend from one to the next.
[`appearance.border_gradient_angle`](config-options.md#appearance.border_gradient_angle) sets the direction in degrees.

These options are in the [option reference](config-options.md): [`appearance.border_focused_gradient`](config-options.md#appearance.border_focused_gradient), [`appearance.border_gradient_angle`](config-options.md#appearance.border_gradient_angle).

```toml
[appearance]
border_focused_gradient = "#33ccff #00ff99"
border_gradient_angle = 45
```

- The gradient is static. It changes only when the pane moves, resizes or
  takes focus.
- The border steps through 8 colours, so the cells of one step share one
  colour code. This keeps the bytes sent to the terminal small.
- The window buttons and the title keep their own colours.
- A focused border in a colour for its state (copy mode or an agent that
  waits, see [THEMES.md](THEMES.md#roles-for-one-piece-of-chrome)) is drawn
  in that colour, not the gradient.
- At 16 colours the border uses one colour, as before.
- A colour value can name a slot or a role, such as `"accent accent_bright"`,
  so the gradient follows the theme.

## Dialog and panel borders

The small dialogs, such as rename, confirm and new space, draw their frame in the pane border style, [`appearance.border_style`](config-options.md#appearance.border_style). The larger panels, such as the command palette, the switchers, the Agents view and the settings page, have no frame unless [`appearance.frame_panels`](config-options.md#appearance.frame_panels) is on. Then they take the same frame.

The scrollback browser and its help modal draw their frame in the border style too.

With [`appearance.pane_overlays`](config-options.md#appearance.pane_overlays) on, a dialog or a panel looks like a pane window:

- Every panel is framed, as if `frame_panels` were on.
- The frame takes the focused pane's border colour, because an open dialog or panel is the focused thing. That is [`appearance.border_focused_color`](config-options.md#appearance.border_focused_color) when it is set. Otherwise it is the theme's colour for the current mode (copy mode or a waiting agent) or the session's tint.
- The frame and everything in it sit on the background of an empty pane cell. That is [`appearance.pane_background`](config-options.md#appearance.pane_background) when it is set, else the terminal's own. The title and hints in the frame line share that background, as they do in a pane.
- The selected row, the row under the pointer and input fields are steps of that background. On a navy pane, a selected row is a darker navy.
- Pills keep their own colours.

The scrollback browser and the copy-mode search bar follow the option too. The browser's frame takes the focused pane's border colour, and its selected row and key pills are steps of the pane's background. The search bar sits on the pane's background.

The option is off by default. Then a dialog or a panel keeps its own shaded background, and its frame keeps its quiet ink. Some cases keep the shade even with the option on:

- Under `hidden`, no frame is drawn, so the shade is a dialog's only edge.
- At 16 colours every background is the terminal's own, so the option changes only the frame's colour.
- With no theme, and a terminal that never reports its background, there is no colour to fill the overlay with or to step its rows from.

[`appearance.overlay_border_color`](config-options.md#appearance.overlay_border_color) sets a colour for every dialog and panel frame, and for the scrollback browser's, with or without `pane_overlays`.

These options are in the [option reference](config-options.md): [`appearance.border_style`](config-options.md#appearance.border_style), [`appearance.frame_panels`](config-options.md#appearance.frame_panels), [`appearance.pane_overlays`](config-options.md#appearance.pane_overlays), [`appearance.overlay_border_color`](config-options.md#appearance.overlay_border_color).

```toml
[appearance]
border_style = "double"
frame_panels = true
pane_overlays = true
overlay_border_color = "accent"
```

How each border style draws a dialog or a framed panel:

- `rounded`, `normal`, `thick`, `double` and `ascii` draw that style's corners and lines.
- `hidden` draws no frame. The dialog keeps its size, and its title and key hints stay where the frame would carry them. A frame that is the only edge of what it holds is drawn as in `rounded` instead: the frame of a panel at 16 colours, the frames round the regions inside a panel, such as an explorer's list and preview, and the frames of the scrollback browser and its help modal, which have no background of their own.
- `block`, `outer-half-block` and `inner-half-block` draw the `rounded` frame. A solid frame is too heavy round a dialog of three rows.
- `glyphs` draws the border of the active glyph set. A part of the border the set does not name is drawn as in `rounded`.
- With `--ascii-only`, every frame is drawn with `+`, `-` and `|`.
- At 16 colours a panel always has a frame, because its background is the terminal's own. The frame takes the border style, except `hidden` as above.

Both options are on the settings page, in the Appearance section. A change applies at once.

## Borderless zoom

`z` zooms the focused pane. By default the zoomed pane keeps its border, and
[`appearance.zoom_size`](config-options.md#appearance.zoom_size) sets how much of the screen it takes.
[`appearance.zoom_borderless`](config-options.md#appearance.zoom_borderless) shows the zoomed pane on the whole pane region
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

## Limits and banners

These options are in the [option reference](config-options.md): [`notifications.rate_limit_ms`](config-options.md#notifications.rate_limit_ms), [`notifications.bell_rate_limit_ms`](config-options.md#notifications.bell_rate_limit_ms), [`appearance.copy_search_max_matches`](config-options.md#appearance.copy_search_max_matches), [`tape.banner_ms`](config-options.md#tape.banner_ms), [`tape.done_linger_ms`](config-options.md#tape.done_linger_ms).

```toml
[notifications]
rate_limit_ms = 1000

[appearance]
copy_search_max_matches = 5000

[tape]
banner_ms = 4000
```

A value of 0 on `copy_search_max_matches`, `banner_ms` or `done_linger_ms` is read
as the default, and a value out of range moves to the nearest end of the range.
All five are on the settings page and apply at once.

## More display options

These options are in the [option reference](config-options.md): [`appearance.multi_copy_dim`](config-options.md#appearance.multi_copy_dim), [`appearance.sidebar.marquee_pause_ms`](config-options.md#appearance.sidebar.marquee_pause_ms), [`appearance.sidebar.marquee_step_ms`](config-options.md#appearance.sidebar.marquee_step_ms), [`dock.meter_interval_ms`](config-options.md#dock.meter_interval_ms).

```toml
[appearance]
multi_copy_dim = 60

[appearance.sidebar]
marquee_pause_ms = 400
marquee_step_ms = 150

[dock]
meter_interval_ms = 5000
```

A value of 0 on `marquee_step_ms` or `meter_interval_ms` is read as the default,
and a value out of range moves to the nearest end of the range. All four are on
the settings page and apply at once.

## Display options

These options are in the [option reference](config-options.md): [`appearance.idle_fps`](config-options.md#appearance.idle_fps), [`appearance.sidebar.git_refresh_ms`](config-options.md#appearance.sidebar.git_refresh_ms), [`debug.show_key_count`](config-options.md#debug.show_key_count).

```toml
[appearance]
idle_fps = 4

[appearance.sidebar]
git_refresh_ms = 5000

[debug]
show_key_count = 8
```

A value of 0 is read as the default, and a value out of range moves to the
nearest end of the range. All three are on the settings page and apply at once.

## Timing options

These `[appearance]` options set how long the interface waits before it acts.
Each is in milliseconds, applies at once, and is on the settings page.

These options are in the [option reference](config-options.md): [`appearance.whichkey_delay_ms`](config-options.md#appearance.whichkey_delay_ms), [`appearance.tooltip_delay_ms`](config-options.md#appearance.tooltip_delay_ms), [`appearance.zen_reveal_ms`](config-options.md#appearance.zen_reveal_ms), [`appearance.paste_preview_ms`](config-options.md#appearance.paste_preview_ms), [`appearance.chord_timeout_ms`](config-options.md#appearance.chord_timeout_ms).

```toml
[appearance]
whichkey_delay_ms = 1000
chord_timeout_ms = 800
```

A value of 0 on `zen_reveal_ms`, `paste_preview_ms` or `chord_timeout_ms` is read
as the default, and a value out of range moves to the nearest end of the range.

## Mouse

The `[mouse]` table sets how tuios reads the pointer's gestures.

These options are in the [option reference](config-options.md): [`mouse.double_click_ms`](config-options.md#mouse.double_click_ms), [`mouse.drag_threshold`](config-options.md#mouse.drag_threshold).

```toml
[mouse]
double_click_ms = 450
drag_threshold = 8
```

Raise `double_click_ms` for a slow hand or a trackpad that spaces its clicks.
The Agents list used 400 ms before this option; it now follows the same value
as the pane, 300 ms by default. Both options are on the settings page, in the
Behavior section, and apply at once. A value of 0 or a value out of range is
read as the default or moved to the nearest end of the range.

## Shell integration

tuios turns on OSC 133 prompt marks in the bash and zsh shells that it starts
in panes, as Ghostty and kitty do. The marks tell tuios where a prompt starts,
where a command starts, and how the command ended. `tuios run`,
`wait-for command-finished`, `capture-pane --last-command`, the scrollback
browser and copy mode read them. A pane with marks also keeps one copy of its
prompt when you make the pane narrower.

tuios does not change your files:

- **zsh**: tuios starts zsh with `ZDOTDIR` set to a folder of its own. The
  `.zshenv` there sets `ZDOTDIR` back to your value, reads your `.zshenv`, and
  adds the marks. zsh then reads your `.zshrc` as usual, and your `.zprofile`
  and `.zlogin` too when it is a login shell (see [Login shells](#login-shells)).
  Programs in the pane see your own `ZDOTDIR`.
- **bash**: tuios starts bash with `--rcfile` set to a file of its own. That
  file reads your `~/.bashrc` and then adds the marks. Your `PS1`, `PS0`,
  `PROMPT_COMMAND` (a string or an array), traps, `BASH_ENV` and `set -e` or
  `set -u` stay as they are. This needs bash 4.4 or newer. An older bash
  starts with no marks.
- **fish 4** and **nushell** send the marks themselves, so tuios adds nothing.
- Other shells start as they are.

When your own setup already sends a mark, for example from a prompt theme or
your own `precmd`, tuios does not send that mark again.

The files are in `$XDG_STATE_HOME/tuios/shell-integration`, by default
`~/.local/state/tuios/shell-integration`. tuios writes them once and checks
them the first time it starts a shell. A shell starts less than 1 ms slower.

To turn it off:

```toml
[shell]
integration = "off"   # default: "auto"
```

The change applies to new panes. To choose for one pane, use
`tuios new-window --shell-integration off`, or `auto` when the config is off.
`tuios doctor shell` shows which panes send marks.

tuios does not set up the shell of a remote pane: a pane that runs `ssh`, or a
window on another machine (`--host`). To get marks there, add the lines that
`tuios doctor shell` prints to the rc file on that machine. Then open a new
pane.

## Login shells

By default tuios starts the shell of a new pane as an interactive shell that
is not a login shell. Such a shell reads `.zshrc` or `.bashrc`, but not
`.zprofile`, `.bash_profile` or `.profile`. Most terminals on macOS start a
login shell, so a `PATH` that you set in `.zprofile` can be missing in tuios.

To start each new pane as a login shell:

```toml
[shell]
login = true   # default: false
```

- **zsh** and **fish** start with `-l`. zsh then reads `.zprofile` and
  `.zlogin`.
- **bash** keeps the file that tuios uses for the prompt marks, because a
  login bash reads no rc file. That file reads `/etc/profile` and then the
  first of `~/.bash_profile`, `~/.bash_login` and `~/.profile`, as a login bash
  does, in place of `~/.bashrc`. Most `~/.bash_profile` files read `~/.bashrc`
  themselves. With `[shell] integration = "off"`, bash starts with `-l`.
- Other shells (`sh`, `dash`, `ksh`, `tcsh`) start with a `-` in front of
  their name, which every Unix shell reads as a login shell.
- On Windows the option does nothing.

The change applies to new panes. A pane that runs a command, such as
`tuios new-window -- htop`, runs the command as it is.

## Splits that follow ssh

The actions `split_ssh_horizontal`, `split_ssh_vertical` and `new_window_ssh`
open a pane on the machine that the focused pane is connected to with ssh. See
[KEYBINDINGS.md](KEYBINDINGS.md#split-into-the-same-ssh).

[`appearance.new_window_follow_ssh`](config-options.md#appearance.new_window_follow_ssh) makes the ordinary actions do the same:
`split_horizontal`, `split_vertical` and `new_window`. When the focused pane
does not run ssh, these actions open an ordinary pane. The default is `false`.

```toml
[appearance]
new_window_follow_ssh = true
```

tuios finds ssh in the foreground of the pane. ssh can run from a shell, or
from `sshpass`, `autossh` or `mosh`. ssh can also run from another program,
such as your own helper script (`tt ssh box`). tuios then follows only an
interactive login: an ssh line with no remote command, and with none of `-s`,
`-N`, `-T` or `-W`. tuios reads only the processes that you own in the pane. It runs the `ssh` or `mosh` that it finds on its own `PATH`,
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
- ssh that a file transfer starts: `scp`, `rsync`, `git`, `sftp` or `sshfs`.
- ssh that another program starts, when the line has a remote command, or
  `-s`, `-N`, `-T` or `-W`.

When a split asks to follow ssh and gets an ordinary pane, the dock says why.

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
[`dock.clock.format`](config-options.md#dock.clock.format). The settings page edits them
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
right  = ["transfers", "notifications", "copy-help", "cpu", "ram", "clock", "session-controls"]

[dock.clock]
format = "15:04"

[dock.custom.branch]
command  = "~/.config/tuios/dock/git-branch.sh"
refresh  = "event:after-focus-change"
on-click = "tuios new-window log -- git log --oneline -20"
```

The lists above are the default: omit the whole table and the bar is unchanged.

The clock draws hours and minutes (`15:04`) unless [`dock.clock.format`](config-options.md#dock.clock.format) or
[`appearance.clock_format`](config-options.md#appearance.clock_format) sets another Go time layout. To show seconds, set
`format = "15:04:05"`. A clock with seconds draws a frame every second. A
clock without seconds draws one frame a minute.
When the `center` list has a clock or a custom cell, the center is at the
middle of the terminal. A notification does not move it or shorten its
minimized pane labels. On a crowded bar, the center moves left to keep the
right-hand cells on the screen. Click a cut notification to read the full
message.
`buttons` shows five buttons: New, Split, Close, Help and Settings. Click a button to run its action. New opens a terminal. Split divides the pane side by side. Close closes the pane. Help shows the help. Settings opens the settings. Point at a button to read what it does. The buttons are always on the right. On a narrow dock, the buttons show icons instead of words. When the icons do not fit, the last buttons go into a more button at the end of the row. Click it to see them.

`transfers` shows the copies between machines. It shows nothing while no copy
exists. One copy is a bar and a percent, `[####--] 61%`. More copies are a
count and a percent, `3 copies 42%`. `waiting` means a link is down, and
`1 failed` stays until you open the Copies panel. A click opens the panel. Add
`transfers` to your own `right` list to see it there.
`session` shows the session name, workspace and pane, `main 2:1`, and is in no
default list. `[dock.session]` takes `format` (`{session}`, `{workspace}`,
`{pane}`) and the cell style keys `fg`, `bg`, `bold`, `cap_left` and
`cap_right`, which `[dock.custom.NAME]` takes too. Colours take any [colour value](#colour-values).
A custom component's first line of stdout becomes its cell, it is hidden when
the command fails, and `tuios list-dock-components` says which and why.
The `command` and the `on-click` command get `TUIOS_SESSION`, `TUIOS_SOCKET`
and `TUIOS_DOCK_COMPONENT`, the name of the component. The `on-click` command
also gets `TUIOS_CLICK_BUTTON`, the mouse button: `1` for left, `2` for middle
and `3` for right.
tuios runs one copy of the command at a time. When an event arrives while the
command runs, tuios runs the command one more time when it ends. An interval
that comes due while the command runs is skipped.

### Workspace tab style

By default the workspace strip draws rounded pills. `[dock.tabs]` can draw
flat tabs instead, the way a tmux status line draws its window list:

```toml
[appearance]
dock_workspace_tab_format = "{name}{zoom} {icon}"

[dock.tabs]
style         = "flat"        # pill (default) | flat
separator     = " • "         # drawn between flat tabs
separator_fg  = "#4c3743"
active_fg     = "#272e33"
active_bg     = "#a7c080"
active_bold   = true
inactive_fg   = "#4f5b58"
number_glyphs = ["I", "II", "III"]  # {icon}: the first entry is workspace 1; past the end prints the number
zoom_marker   = " [ ]"        # {zoom} on the current workspace while a pane is zoomed
show_add      = false         # keep the trailing "+" tab in the flat style
caps          = false         # rounded ends on each flat tab
active_underline = false      # underline the current tab
```

Colours take any [colour value](#colour-values). The colours are overrides:
a flat strip with none set puts the current tab on the theme's accent, in
bold, and draws the others in dim text on the bar's own ground, so it still
shows where you are. A value that is not a colour is ignored, with a
warning, as if it were unset.

The four tab colours (`active_fg`, `active_bg`, `inactive_fg`, `inactive_bg`)
and `inactive_bold` also apply to pills. A pill with no colour set keeps the
theme's look. The current pill is always bold, so `active_bold` is for flat
tabs only.

`active_underline` underlines the tab of the current workspace. When it is not
set, a pill is underlined and a flat tab is not.

`caps = true` gives each flat tab the rounded ends of a pill. Each end has the
colour of its tab's background. A tab with no background gets a blank column
in place of each end, so the tabs keep their width. The ends use the pills'
glyphs: there are none under ASCII glyphs or with
`appearance.dock_pill_caps = false`. Pills always have ends, so `caps` with
the pill style gives a warning.

Flat tabs with your own colours and rounded ends:

```toml
[dock.tabs]
style       = "flat"
caps        = true
active_bg   = "magenta"
active_fg   = "bg"
active_bold = true
inactive_bg = "bright_black"
inactive_fg = "fg"
```

Pills with your own colours and no underline:

```toml
[dock.tabs]
active_bg        = "magenta"
active_fg        = "bg"
inactive_bg      = "bright_black"
active_underline = false
```
Besides `{index}` and `{name}`, the tab format takes `{icon}` and `{zoom}`.
`{name}` is the name you gave the workspace. A workspace you did not name
shows the name its active pane gave with OSC 1. The active pane is the focused
one, or the one that had the focus last. Only OSC 1 counts: shells and agents
set the window title with OSC 0 at every prompt, and that title stays on the
pane's border. A program names its workspace with `printf '\033]1;logs\007'`
and clears the name with an empty OSC 1.
A workspace with neither name has an empty `{name}` when the format has
`{index}`, and the separator next to it (spaces, `:`, `-`, `·` or `|`) is
dropped with it, so `"{index} {name}"` shows `7` for such a workspace and
`7 api` for one named `api`. In a format without `{index}`, such as the
default `{name}`, its `{name}` is its number.
`{index}` is the workspace's number, which is its position: dragging a tab to
another place, or moving the workspace with `move_workspace_left` and
`move_workspace_right`, renumbers it, and `Alt+N` goes to it by its new number.
The flat style shows even a lone workspace and drops the `+` tab unless
`show_add` is on. With no `[dock.tabs]` table the strip is unchanged.

#### Tab marks

Three more keys mark a workspace you are not looking at, in the pill style and
the flat style:

```toml
[dock.tabs]
attention_fg    = "warning"   # the tab's ink while a pane on it waits for you
bell_marker     = "!"         # after the label when a pane on it rang the bell
activity_marker = "#"         # after the label when a pane on it printed
```

- `attention_fg` is the ink of the tab while an agent on that workspace asks
  for input or stopped on an error. It takes any [colour value](#colour-values).
- `bell_marker` and `activity_marker` go after the label, after the label
  cap. The bell marker comes first.
- A mark stays until you look at the workspace.
- The current workspace's tab never carries them.
- With a key empty, tuios draws nothing for it and keeps no record.
- Attached to a daemon, the marks need a daemon of this version or newer.
  An older daemon does not report a pane on another workspace, so its tab
  gets no bell or activity mark.

`examples/dock/README.md` is the full contract and five working recipes.

## Show or hide the rail in every session

`Ctrl+B b` (`prefix_toggle_sidebar`), the settings page and
[`appearance.sidebar.enabled`](config-options.md#appearance.sidebar.enabled) show or hide the rail in every session on the
daemon, not only in the session you are in. tuios saves the choice to
[`appearance.sidebar.enabled`](config-options.md#appearance.sidebar.enabled), so a new session starts the same way. Every
client of a session shows the rail or hides it together.

To keep the old behaviour, where each session keeps its own shown or hidden
rail, set `per_session`:

```toml
[appearance.sidebar]
per_session = true
```

## Two sidebar edges

The existing `[appearance.sidebar]` settings still configure the original rail.
A table for its current edge overrides only the fields it names; the opposite
edge stays hidden unless explicitly enabled. Each edge can have its own width,
section order and custom command:

```toml
[appearance.sidebar]
position = "right"
sections = "sessions,terminals,custom"

[appearance.sidebar.custom]
command = "right-summary"

[appearance.sidebar.left]
enabled = true
width = 28
sections = "sessions,files,custom"

[appearance.sidebar.left.custom]
command = "left-summary"
```

`[appearance.sidebar.right]` has the same `enabled`, `width`, `sections` and
`custom` keys. Both edge tables also take the five `subagent_*` keys. On the original edge, omitted keys retain the legacy settings;
on the other edge, an omitted `sections` uses the default section order.
Writing an edge table without `enabled = true` does not enable that edge.
With `position = "hidden"`, explicitly enabled edge tables still draw: if
both edges are enabled, the left one supplies the primary rail settings and
the right one supplies the opposite rail settings. Unset fields on those
explicit edges use the edge defaults; they do not inherit a hidden legacy
width, section order or custom command. The file's hidden legacy position is
not rewritten.
Both rails can show the same built-in sections: they use the same sessions,
focused pane's directory listing and repository reading, but have independent
section scrolling, keyboard focus and pointer hit areas. A custom command on
each edge runs independently: `rail/custom` belongs to the original rail and
`rail/secondary/custom` to the other. These edge tables are file-only, not
`set-option` targets, because custom commands execute outside panes.

On narrow screens, the optional edge contracts before the original rail; both
may hide to leave room for the panes. Resizing either rail by dragging its
inner border changes only that edge for this client. In sidebar keyboard focus,
`e` moves focus to the other visible edge (and back); `Ctrl+B H` independently
hides or shows the opposite edge, while `Ctrl+B b` keeps its existing legacy
rail toggle. The opposite-edge toggle does not opt in an edge whose
`enabled = true` was never configured.

### Show a daemon session in each rail

To use a rail as a terminal, assign a **local daemon session** to that edge.
Its panes on the session's current workspace stack **top to bottom** in the
rail, independent of the central session's layout and attachment.
For example, create two sessions, then edit `config.toml`:

```sh
tuios new side-left --detach
tuios new side-right --detach
```

```toml
[appearance.sidebar]
enabled = true
position = "right"
width = 36

[appearance.sidebar.right]
session = "side-right"

[appearance.sidebar.left]
enabled = true
width = 36
session = "side-left"
```

Settings → Sidebar exposes separate **Left session**, **Right session**,
width and workspace controls. Entering a session name enables that edge.
An empty session name restores that rail's ordinary sections (including
`sections` and `custom`). While a daemon session occupies the rail, its
ordinary custom-section command does not run. `session_workspace = 0` or an omitted value shows
the rail on every central workspace; set `session_workspace = 2` on one
edge to show it only on workspace 2 (or use `1`–`9`). This controls
*visibility*, not the assigned session's own workspace. Each
edge is independent, so one can follow while the other is pinned.

Click a pane to type in it. With the rail focused, `Ctrl+B` then `-` splits
**downwards**: it creates a normal pane in the assigned daemon session and
stacks it below the others. `Ctrl+B` then `c` also creates a pane there;
`Ctrl+B` then `|` stacks it vertically rather than subdividing a narrow rail
sideways. `Ctrl+B` then `n` or `p` changes the active pane; clicking any
pane's title bar focuses that pane. Each pane uses its normal title and title
position, not the assigned session ID. Its close and minimize buttons target
that daemon pane; zoom temporarily expands the pane within the rail. The
session assignment remains in Settings. There is no extra row above the rail
pane's normal border. The rail crops the daemon's existing grid; it does not
resize a PTY, suppress leading guest whitespace, or shift cursor and mouse
coordinates. A narrow rail does not change the size seen by another viewer.
All visible panes remain on screen. If
the rail is too short to give every pane at least six rows, it shows a
contiguous group containing
the active pane; cycling focus brings the others into view.

Switching central workspaces, hiding the rail or detaching this TUI client
does not end the assigned daemon session or its processes. Reattaching
restores the pane's output. A *daemon restart* is different: it ends running
pane programs and restores saved layouts with new shells. Right-clicking the
rail currently opens Sidebar Settings, not a pane picker. The assignment can
be entered before its daemon session exists: the rail shows an attach error
and retries when the session is created. Standalone mode cannot attach sessions.

## The rail's current row

[`appearance.sidebar.row_style`](config-options.md#appearance.sidebar.row_style) sets how the rail marks the session you are on
and the pane that has focus:

| Value | What it draws |
|---|---|
| `bar` | A bar in the gutter, in the session's colour. This is the default |
| `marker` | A `›` in the gutter, in the session's colour. `>` with ASCII glyphs |
| `underline` | The name underlined, in the session's colour |
| `pill` | The name on a fill of the session's colour, with the pill caps of the glyph set |

```toml
[appearance.sidebar]
row_style = "pill"
```

A row that waits for a person keeps its attention mark in the gutter. The
marker glyph is the `marker` role of the glyph set (see [GLYPHS.md](GLYPHS.md)).
The option is on the settings page, in the Sidebar section.

The focused pane's row in the terminals section carries the mark. Sometimes
the rail does not show that row. The agents row of the pane then carries the
mark in its gutter. This occurs in these cases:

- `sections` does not include `terminals`.
- The rail is narrow and lists an agent pane only in the agents section.
- The terminals section has no room for the row.
- The terminals section shows the panes of another session.

The agents row uses the bar or the marker. With `underline` or `pill`, it
uses the bar.

## Pick a path from the files

The path picker types a path at the cursor of a pane. It lists the same folder
as the files section: the folder that the shell in the pane is in. Press
`Alt+;` in terminal mode, or `Ctrl+B .`. Type to filter, `Tab` to open a
folder, and `Enter` to type the path. See
[KEYBINDINGS.md](KEYBINDINGS.md#pick-a-path).

## Where worktrees go

`tuios worktree new`, `New worktree...` on the rail and `tuios fan` make a
git worktree for each new session. By default it goes in
`$XDG_DATA_HOME/tuios/worktrees/<repo>/<branch>`, outside the repository.
`[worktree] path` puts it somewhere else, given as a path template:

```toml
[worktree]
path = "{{ repo_path }}/.worktrees/{{ branch | slug }}"
```

| Variable | What it holds |
|---|---|
| `repo_path` | The main checkout's directory |
| `repo` | Its base name |
| `branch` | The branch, as given |

`| slug` turns a value into one folder name: `feat/retry` becomes
`feat-retry`. Use it on `branch`. Without it, `{{ branch }}` keeps the slash
and makes a folder per part, so the worktree of `feat/x` lands inside the
worktree of `feat`, and `tuios worktree rm --force` on `feat` deletes both.
A relative path starts at `repo_path`, so `.worktrees/{{ branch | slug }}` is
the same as the example above. A leading `~/` is your home folder. The
template has to use `branch`, or every branch would get the same folder.

The variables are the ones worktrunk's `worktree-path` uses, and `| sanitize`
is another name for `| slug`, so a template copied from a worktrunk config
works as it is. worktrunk's `sanitize` turns only `/` and `\` into `-`, while
`slug` turns every run of characters other than letters, digits, `.` and `_`
into one `-`. A branch with other characters, such as `fix--retry` or
`feat+x`, can get a different folder name from each tool.

When the worktree is inside the repository and git does not ignore it, the
main checkout lists it as untracked, and `git add -A` there would take it in.
tuios then warns after it makes the worktree and names the folder that holds
the worktrees, such as `/.claude/worktrees/` for
`.claude/worktrees/{{ branch | slug }}`, to add to `.gitignore` or
`.git/info/exclude`. It never edits those files itself.

The daemon makes every worktree, so it reads the setting, when it starts and
when the file changes. The path is then the same whichever client asked.
Empty is the default. While it is empty, `TUIOS_WORKTREE_DIR` in the daemon's
environment still moves the default folder (see
[CLI_REFERENCE.md](CLI_REFERENCE.md#tuios_worktree_dir)).

## Setup before the agent

`[worktree] setup` is a shell command that runs in every new worktree before
its agent or shell starts. Use it to install dependencies:

```toml
[worktree]
setup = "pnpm i"
setup_async = false
```

These options are in the [option reference](config-options.md): [`worktree.setup`](config-options.md#worktree.setup), [`worktree.setup_async`](config-options.md#worktree.setup_async).

The new session shows a card at the top right with three stages: the
checkout, the setup with its last four lines, and the agent. The rail row
shows `setup` while it runs and `setup failed` after a failure. A key hides
the card of a finished setup.

Without `setup_async`, the agent starts when the setup passes. When the setup
fails, tuios removes the new worktree and the branch it made. It keeps a
worktree with an uncommitted change or a nested repository, and a branch with
a commit or a branch that was there before. The note on the card says what it
kept. The agent does not start. The session stays, and the setup window stays
open on its output until you press Enter.

With `setup_async = true`, a failed setup keeps the worktree and the branch,
because the agent may already have work in them.

`tuios worktree new --setup` and `tuios fan --setup` set the command for one
call, and `--setup-async` the mode. `--setup ''` runs no setup.

The daemon reads both keys when it starts. A change in the file applies at once
when it clears the command or keeps it. A new command waits for
`tuios config apply` in a terminal outside tuios, or a daemon restart. A
process in a pane can write the file, and the daemon runs the command.

## Where a review opens

The review overlay (`ctrl+b v`) opens with the keys on the diff. `tab` moves
them to the file list. `[review] focus` picks the side a review opens on:

```toml
[review]
# Which side has the keys when a review opens: "diff" or "files" (default: "diff")
focus = "files"
```

It applies to every review that opens, and to an attempt opened from the
compare view. A review with no changes opens on the diff, which says so. `r`,
`u` and `b` read the open review again and keep the side that has the keys.
The setting is on the settings page, in the Behavior section, and
`tuios set-config review.focus files` applies from the next review. A value
other than `diff` or `files` reads as `diff`, with a warning when the config
loads, and `tuios set-config` refuses it. See
[KEYBINDINGS.md](KEYBINDINGS.md#the-review-overlay).

A review shows the changes since the base when it opens. In a plain
repository, the base is the merge base with the upstream branch. Commits you
did not push yet are then part of the review. `[review] scope` picks what a
review shows when it opens:

```toml
[review]
# What a review shows when it opens: "base" or "uncommitted" (default: "base")
scope = "uncommitted"
```

`u` still switches between the two, and `b` still sets another base. A diff of
two attempts in the compare view does not use the setting. The setting is on
the settings page, in the Behavior section, and
`tuios set-config review.scope uncommitted` applies from the next review. A
value other than `base` or `uncommitted` reads as `base`, with a warning when
the config loads, and `tuios set-config` refuses it.

## Repositories in the sessions tree

The sessions section gathers the sessions of a repository into one group. The
main checkout's row is the repository's row, and the worktree rows hang off it.
The main checkout is the session whose directory is the repository itself,
usually on `main` or `master`.

```
tuios main          2 ▾
├─ feat/compact-rail  2
└─ fix/pill-bars      1
```

- An unnamed session on that row shows the repository's name. A session with a
  name you chose keeps that name.
- A repository with no main checkout session has a row of its own.
- A repository with only a main checkout session shows a plain row.
- Only worktree rows hang off the repository's row. With several sessions in the
  main checkout, the first in the rail's order is the repository's row, and the
  others are plain rows in their own places. Folding the group does not hide
  them, and the group's `+N` does not count them. Several sessions in the main
  checkout and no worktree session show plain rows and no group.

Each group's row ends in a fold mark. These controls fold and open the group:

- Click the fold mark.
- Move the rail cursor to the fold mark and press `enter`.
- Right-click the row and select `Fold group` or `Unfold group`.
- Click a repository's own row.

A click on a main checkout's row opens its session. A folded row shows `+N` for
the sessions that it holds, the loudest agent state of the group, and the mark
of the attached session when that session is in the group.

Drag a group's row to move the whole group. Drag a worktree row to move it inside
its group. The reorder keys follow the same rule.

[`appearance.sidebar.group_main`](config-options.md#appearance.sidebar.group_main) gives each repository a row of its own, with the
main checkout under that row. The option is `false` by default. An explicit
`false` gives the same layout as no option. The option is on the settings page,
in the Sidebar section, as `Separate repository row`.

```toml
[appearance.sidebar]
group_main = true
```

Independent of the option, the daemon records the main checkout the way it
records a worktree, so `tuios worktree ls` and the worktree verbs see it with the
worktrees of the repository.

## Keep the files section shut by default

Leave `files` out of [`appearance.sidebar.sections`](config-options.md#appearance.sidebar.sections) to keep the section shut when
you attach:

```toml
[appearance.sidebar]
sections = "sessions:25,terminals,agents:34"
```

Click `files` in the rail's footer to open the section. Click it again to close
it. File search and a click on a folder link also open it.

The section goes after `terminals` with a share of 25, as in the default layout.
A layout with no `terminals` has it after `sessions`, and a layout with neither
has it above `agents`. The section stays open for this client until you close it
or the client exits. The value of [`appearance.sidebar.sections`](config-options.md#appearance.sidebar.sections) does not change.

## The files section style

[`appearance.sidebar.files_style`](config-options.md#appearance.sidebar.files_style) sets how the rail's files section shows a
folder:

| Value | What it shows |
|---|---|
| `list` | One folder. `..` goes up, and a folder row goes into the folder. This is the default |
| `tree` | One tree from the folder. A folder opens under its own row, with indent guides and an open or closed mark |

```toml
[appearance.sidebar]
files_style = "tree"
```

In the tree, the rail has these keys on a name:

| Key | What it does |
|---|---|
| `l`, `right` | Open the folder. On an open folder, go to its first name |
| `h`, `left` | Close the open folder. On another name, go to its folder |
| `enter`, a click | Open or close the folder. On a file, do what the list does |

The file action keys (`a`, `r`, `d`, `y`, `x`, `p`, `Y`, `S`) act on the name
under the cursor. A new file or a paste goes into the folder of that name. On
an open folder, it goes into that folder. With `folder_click = "cd"`, a folder
row types a cd into the pane and does not open. With `both`, a folder row
only opens, because a cd moves the root of the tree to that folder.

In both styles, `g` and `G` on a file go to the first or last name of the
list. Press the key again to go to the first or last row of the rail.

tuios reads a folder only when you open it. It remembers the open folders of
each session until tuios stops. You can open 64 folders at most. Each open
folder shows its first 2000 names. tuios watches each open folder on this
computer and shows a change without a key. A folder that is open under a
folder on another computer shows a change when you open it again. When tuios
sees that an open folder is gone, it closes the folder. A new folder with the
same name starts closed.

## The rail's cursor row

When the rail has the keyboard, the row under the cursor has a lighter
background, and its text is bold and underlined in the accent colour.
[`appearance.sidebar.cursor_background`](config-options.md#appearance.sidebar.cursor_background) sets the background of that row.

```toml
[appearance.sidebar]
cursor_background = "#3b4261"   # or a slot name, such as "blue", or a role, such as "accent"
```

The value is a [colour](#colour-values). Empty, the default, keeps the
theme's background for the cursor row. The colour does not change the text
colour, so choose a colour that your text is readable on. The option sets
only the row under the cursor while the rail has the keyboard. The rows under
the mouse pointer and the cursor of an unfocused rail keep their own
background. At 16 colours, the option has no effect: the cursor row is in
reverse video. The option is on the settings page, in the Sidebar section.

## The rail's section headers

[`appearance.sidebar.header_style`](config-options.md#appearance.sidebar.header_style) sets how a section header is drawn:

| Value | What it draws |
|---|---|
| `auto` | The label, with a rule after it when `header_case` is `uppercase`. This is the default |
| `plain` | The label with no rule |
| `rule` | The label and a rule to the rail's edge |
| `pill` | The label on a quiet fill, with the pill caps of the glyph set |
| `icon` | The section's icon, then the label |

```toml
[appearance.sidebar]
header_style = "icon"
```

The icons are glyph roles: `section_sessions`, `section_terminals`,
`section_agents`, `section_files` and `section_git` (see
[GLYPHS.md](GLYPHS.md)). With Nerd Fonts on, the default icons are Nerd Font
icons. The `unicode` glyph set has its own. With no icon, the header is plain.
`header_case` still sets the case of the label. On a narrow rail, a pill or
an icon can leave no room for a header's `+`.

## The rail's section spacing

[`appearance.sidebar.density`](config-options.md#appearance.sidebar.density) sets the space between the rail's sections.
`normal` (the default) stacks them as the rail always has. `roomy` puts a
blank row in front of each section header after the first. The block pinned
to the bottom keeps its own line, which holds the divider.

```toml
[appearance.sidebar]
density = "roomy"
```

The panes do not move. Only the rail's rows change.

## The rail's cursor row

When the rail has the keyboard, the cursor row has a lighter background. The
text of the row is also bold and underlined in the accent colour. This mark
does not need the background, so you can see the cursor on a see-through
terminal or with `background = "off"`. The gutter keeps its own marks. At 16
colours the cursor row is in reverse video.

## A rail section that overflows

A section with more rows than the lines it is given shows as many as fit. It ends in `… +N` for the rest. The wheel over a section scrolls it. The keyboard cursor scrolls it to keep itself in view.

While rows are out of view, the section draws a scroll thumb in the rail's edge rule, beside its rows and not its heading. The thumb takes the rule's own cell, so the rows keep their full width. Its size is the share of the rows on screen. It reaches the first line only at the top of the section, and the last line only at the bottom, where there is no `+N` row. A section that fits draws the plain rule.

A hover peek of another session's panes shows the peeked list from its top, ending in `+N` when the list is longer than the section. When the terminals section is above the sessions section, the peek keeps the section's resting height, so the session row under the pointer does not move. When it is below, as in the default order, the section is sized for the peeked list like any other. The peek gets a thumb on the same terms, at the top of the column, and leaves the attached session's scroll where it was. There is no thumb on the collapsed strip or on a section of one line.

The glyph is the active glyph set's `scrollbar_thumb` role. It is `┃` by default, `#` under `--ascii-only`, and `█` in the `heavy` set. Where the thumb would be the same glyph as the rule, as with a heavy border style, it is drawn as `█` (`#` under `--ascii-only`). `appearance.scrollbar.*` and [`appearance.hide_scrollbar`](config-options.md#appearance.hide_scrollbar) style the pane scroll bar only, not the rail's thumb.

## The rail's custom section

The rail has one section whose rows are the output of a command you write.
Place `custom` in [`appearance.sidebar.sections`](config-options.md#appearance.sidebar.sections) like any built-in section and
describe it in `[appearance.sidebar.custom]`:

```toml
[appearance.sidebar]
sections = "sessions:25,terminals,custom:35,agents:30"

[appearance.sidebar.custom]
title   = "Brief"
command = "agent-brief render"
refresh = "event:window-focused,agent-state"
```

These options are in the [option reference](config-options.md): [`appearance.sidebar.custom.title`](config-options.md#appearance.sidebar.custom.title), [`appearance.sidebar.custom.command`](config-options.md#appearance.sidebar.custom.command), [`appearance.sidebar.custom.refresh`](config-options.md#appearance.sidebar.custom.refresh).

The command is a dock component that draws on the rail, so the dock's rules
apply: it runs in the client, under a three second timeout, with bounded
output; SGR colour and OSC 8 links survive and every other control sequence
is stripped; a
command that fails, times out or prints nothing leaves the title over an
empty section, never an earlier run's rows; five failures in a row stop it
until the config reloads or `tuios refresh-dock rail/custom`; and
`tuios list-dock-components` lists it as `rail/custom` with why it drew
nothing. Rows are cut to the rail's width and to the lines the section's
share gives it, with `… +N` for the ones below the fold. While some are below
the fold the section draws a scroll thumb in the rail's edge rule (see
[A rail section that overflows](#a-rail-section-that-overflows)).
A row keeps the OSC 8 links its command prints. A click on a link opens it,
a click on the rest of the row opens the row's first link, and
<kbd>Enter</kbd> on the row's cursor does the same. The link opens as a link
in a pane does, through `appearance.link_opener`. A row with no link opens
nothing.

Each run gets the client's environment plus `TUIOS_SESSION`, `TUIOS_SOCKET`,
`TUIOS_RAIL_SECTION` (`custom`), `TUIOS_RAIL_WIDTH` (the columns a row may
use), `TUIOS_RAIL_HEIGHT` (the most rows the section's share can give it; the
rail may give fewer when other sections need the lines),
`TUIOS_ACTIVE_PANE_ID` and `TUIOS_ACTIVE_PANE_CWD` (the focused pane and its
folder, named as `[[keybindings.command]]` names them). They are read for
each run, and an event that lands while a run is going costs one more run
after it, so a quick focus change never leaves the old pane's rows on
screen. `agent-state` fires on every change of a pane's agent state, whichever
states `[notifications.agent]` alerts on.
The command does not run while the rail is hidden or folded, so the width it
is told is never `0`, and it runs again when the rail opens.

[`appearance.sidebar.sections`](config-options.md#appearance.sidebar.sections) is a settable option, so `tuios set-config` can
place the section. `[appearance.sidebar.custom]` is read from the file only:
the command runs outside every pane on every refresh, as dock commands and
hooks do, and in the default open mode any pane can call `set-option`, so
the command stays out of its reach, as `daemon.respond_from_shell` does.

## The rail's pane section

The rail can show a live terminal pane under its own heading. Place `pane` in
[`appearance.sidebar.sections`](config-options.md#appearance.sidebar.sections) and describe it in `[appearance.sidebar.pane]`:

```toml
[appearance.sidebar]
sections = "sessions:25,terminals,files:25,pane,agents:34"

[appearance.sidebar.pane]
title   = "tv"
command = "tuiostv-helper view"
height  = 12
```

These options are in the [option reference](config-options.md): [`appearance.sidebar.pane.title`](config-options.md#appearance.sidebar.pane.title), [`appearance.sidebar.pane.command`](config-options.md#appearance.sidebar.pane.command), [`appearance.sidebar.pane.height`](config-options.md#appearance.sidebar.pane.height).

The program runs in a real terminal in the session, so it survives a detach.
The pane is not one of your panes: tiling, layouts, the terminals section and
`tuios list-windows` leave it out. `tuios list-windows --rail` lists it.

The pane is as wide as the rail's content and as tall as the lines the section
gets. When the rail changes size, the program gets the new size. When the rail
is hidden, folded, narrower than 10 columns or gives the section no lines, the
program keeps running at its last size and the client draws nothing for it. An
idle program costs no frames. The pane shows text only: kitty images are not
drawn in the rail.

Click the pane, or press `P` in window mode (`focus_rail_pane`), to type into
it. A program that asks for the mouse gets clicks, motion and the wheel.
`alt+esc`, a click outside the pane, or the leader key gives the keyboard back.
`esc` goes to the program.

When the program exits, the pane closes and the section says so. Click the
line to start it again. A session has one rail pane, and only the main rail
draws it. If two clients name different commands, the last one to ask
replaces the pane. `[appearance.sidebar.pane]` is read from the file only, for
the reason `[appearance.sidebar.custom]` is.

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

## The pane bell

A program in a pane rings the bell when it prints the BEL character. For
example, `printf '\a'` rings it. [`notifications.bell`](config-options.md#notifications.bell) sets what tuios does then:

```toml
[notifications]
bell = "toast"   # or "forward", "both", "off"
```

- `toast` shows "bell" in the dock. This is the default.
- `forward` sends the bell to the terminal that tuios runs in. That terminal
  then plays its sound, flashes, or marks the window or tab.
- `both` does the two.
- `off` does neither.

tuios sends the bell for every pane, also for the pane you are in. It sends
at most one bell from each pane in 200 milliseconds. A client attached to the
daemon sends the bell to its own terminal. Over `tuios ssh` and in tuios-web,
the bell goes to that client, not to the machine that runs tuios.

The terminal decides what a bell does. In Ghostty, for example, set
`bell-features` to choose a sound, an attention request or a title mark.

The workspace bell marker does not change with this setting. Set
`bell_marker` under `[dock.tabs]` to change it. You can also change
the setting on the settings page, in the "Alerts" tab, as "Pane bell".

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

These options are in the [option reference](config-options.md): [`notify.enabled`](config-options.md#notify.enabled), [`notify.web_url`](config-options.md#notify.web_url), [`notify.content`](config-options.md#notify.content), [`notify.quiet_active_seconds`](config-options.md#notify.quiet_active_seconds), [`notify.cooldown_seconds`](config-options.md#notify.cooldown_seconds), [`notify.max_per_hour`](config-options.md#notify.max_per_hour), [`notify.allow_http_redirects`](config-options.md#notify.allow_http_redirects).

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
It does not send to the phones of `[notify.webpush]`.
It says the result for each provider. See
[CLI_REFERENCE.md](CLI_REFERENCE.md#tuios-notify-test).

### Web Push to a phone

A phone can get Inbox items by Web Push. The phone does not keep a
connection to tuios. Its push service, or its UnifiedPush distributor such as
the ntfy app, wakes it. The phone app registers with the `register-push`
verb, or you register it with `tuios notify push register`. Only you can
register a phone. A program in a pane cannot. See
[protocol.md](protocol.md#register-push).

```toml
[notify.webpush]
subject = "https://example.com/tuios-contact"   # optional
allow_insecure = false
```

These options are in the [option reference](config-options.md): [`notify.webpush.subject`](config-options.md#notify.webpush.subject), [`notify.webpush.allow_insecure`](config-options.md#notify.webpush.allow_insecure).

When `allow_insecure` is `false`, tuios does not send to a loopback or
private address. This applies also to a host name that resolves to one. tuios
never sends to a link-local, multicast or unspecified address. A push does not
follow a redirect.

When an item opens, tuios encrypts it for each phone that asked for its kind
and sends it to the phone's push service. When the item closes, tuios sends a
close, so the phone can remove the notification. A phone gets `approval`,
`plan`, `ask` and `question` unless it asked for other kinds. The triggers,
`quiet_active_seconds` and `cooldown_seconds` are for the other providers.
`enabled = false` stops the pushes of new items. A phone still gets the close
of an item, so it can remove a notification. `max_per_hour` applies to each
phone. An item from another machine is not pushed. That machine pushes to its
own phones.

A push of `approval`, `plan`, `ask` or `question` lives one hour on the push
service. Other pushes live 120 seconds. tuios tries a failed push again after
1, 4 and 15 seconds. When the push service says that the phone is gone (404
or 410), tuios removes the phone and shows an Inbox item about it.

Each registration shows an Inbox item named `phone NAME`. It says who
registered the phone. Another machine can register a phone over a link with
`respond`, but it can not replace a registered phone. Check the item. Remove
a phone you do not know with `tuios notify push rm NAME`. The phones and the VAPID key are in
the state directory, in `push/`, with mode 600.

Run `tuios notify push ls` to see the phones and the VAPID public key. Run
`tuios notify push rm NAME` to remove a phone.

## ACP version

`acp_version` in `[agents]` is the version of the Agent Client Protocol a pane
that `start-agent --protocol acp` opens asks the agent for:

```toml
[agents]
acp_version = 1   # 1 (default) or 2
```

Version 2 is a draft of the spec, which asks clients not to ask for it by
default until it is final. An agent that speaks only version 1 still works
with 2: the pane speaks 1 to it. The daemon reads the value when it starts a
protocol pane, and a change to the file applies to the next one. See
[ACP versions](AGENT_STATE.md#acp-versions).

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

## Show a hooked agent that stops reporting

A hooked agent reports at each prompt and each tool call. When it sends no
report for `silent_after`, tuios shows it as `unknown` with the reason
`silent`, and its row reads "No report for N min":

```toml
[agents]
silent_after = "10m"   # default; "0" or "off" turns it off
```

The value is a Go duration, such as `90s` or `20m`. A value that does not
parse reads as `10m`, with a warning. The daemon reads the value when it
starts and again when the file changes. See
[Silent agents](AGENT_STATE.md#silent-agents).

## Fold the Working group

The agents view (`ctrl+b i`) can open with the Working group folded. A folded
group shows its heading, its count and the state mark of each agent in it.

```toml
[agents]
fold_working = true   # default false
```

`F` folds or opens any group in the view. tuios keeps that choice in
`sidebar.json` in its state folder (`$XDG_STATE_HOME/tuios` on Linux). The
choice wins over `fold_working`. The settings page has this option on the
Behavior tab, as "Fold Working".

## Reply markers

An agent can mark its reply with a short label in double square brackets,
such as `[[Done]]`, `[[Failed: 3 tests]]` or `[[Needs you]]`. The agents
view (`ctrl+b i`) shows each marker as a coloured pill:

| Colour | Group | Labels |
|--------|-------|--------|
| Red | A problem | Failed, Blocked, Broken, Rejected, Unsafe, Down |
| Yellow | For the person | Needs you, Unverified, Pending, Review, Partial, Skipped, In progress, At risk, Todo, Stale, Draft |
| Green | Settled | Done, Passed, Observed, Fixed, Live, Recommended, Keep, Verified, Merged, Shipped, Approved, Ready, Safe, Healthy |
| Solid red | A consequence | Costs money, Destructive, Irreversible |
| Cyan | The kind of work | Brainstorm, Investigate, Preview, Implement, Qualify |
| Solid blue | Ownership | Owned, Shared, Foreign |
| Blue | Context | New, Changed, Inferred, Optional, Decision, and any other label |

Complete, Completed, Finished and OK show as Done. Fail, Failing and Error
show as Failed. A label has at most 32 characters. A marker in a code span or
a code block stays as text. With Nerd Fonts on, a pill has rounded ends and an
icon. A bold lead label at the start of a paragraph, such as **Finding:**,
**Risk:** or **Next:**, and a heading such as Plan or Result, get an icon too.
Bold text takes the link colour, and the marks of a list take the accent. The
view colours ✓, ✗ and →, and dims the lines of a tree (`├──`, `└──`). The
look follows pi-emphasis by John Lindquist (MIT). The
stored reply and a Markdown copy (`Y` in copy mode) keep the marker as the
agent wrote it.

The view draws a fenced `mermaid` block as a diagram and a fenced `tml` block
as a static TML picture. Quick look draws them the same way in a Markdown file.
Click the line above a picture to see its source, and click it again to see the
picture. In the agents view, `P` does the same for the lowest picture on the
screen. `C` copies the source. A block that is not valid shows its source
under a line that says why. With `reply_markers` on, the agents hear of these
blocks too.

A Mermaid diagram is a picture where the terminal has kitty graphics, and
box-drawing text elsewhere. The text in the picture has the size of the
terminal text, and the picture is never wider than the thread. A diagram
taller than 20 rows shows its top 20 rows and a line under them. In the agents
view, click the picture to see all of it. The line above the diagram shows `picture / text`.
Click it to change how that diagram is drawn. In the agents view, `T` does the
same for the lowest diagram on the screen. `[agents] mermaid` sets the default:

```toml
[agents]
mermaid = "text"   # "auto" (default), "picture" or "text"
```

`auto` draws a picture where the terminal has kitty graphics. `picture` also
draws one with block characters on a terminal with 256 colours or more.
`text` always draws text.

`tuios start-agent` can tell Claude Code about the markers. It passes the
guidance with `--append-system-prompt`. This is off by default:

```toml
[agents]
reply_markers = true   # default false
```

## Next after an answer

An answer sent from the agents view (`ctrl+b i`), such as an allow, a reject,
a plan approved or a question answered, moves the cursor to the next agent
that needs you, so a queue of approvals takes the answer keys alone. The note
after the answer says how many more need you. The cursor moves only when it
is still on the agent you answered: a row you moved to while the answer was
on its way keeps the cursor.

```toml
[agents]
answer_advance = false   # default true
```

Off keeps the cursor on the agent you answered. The settings page has this
option on the Behavior tab, as "Next after an answer".

## Naming panes and sessions

When an agent starts in a tuios pane, `tuios agent-hook` adds a few lines to
the agent's context. One of them tells the agent to name its pane after its
task, and the skill (`tuios --skill`) tells an agent that is alone in its
session to label the session too. `name_hint` turns that off:

```toml
[agents]
name_hint = "on"   # default; "off" leaves the naming advice out
```

With `off`, the line that names the pane is replaced by one that tells the
agent not to rename its pane or its session, and to skip the skill's "Name
your work" section. The title the program sets, the name you gave the pane,
and the row a worktree session shows in the sidebar then stay as they are.
The other lines stay, and so does agent state reporting.

An agent reads the hint once, when its session starts, so a change reaches
the agents that start after it. `tuios set-config agents.name_hint off`
sets it in a running session and writes `config.toml`. The settings page has this option on the Behavior
tab, as "Agents name their panes".

## The agent list

On a screen 80 columns or wider, the agents view (`ctrl+b i`) shows the list
of agents as a column beside the focus. `view_list` sets that layout:

```toml
[agents]
view_list = "column"   # default; "strip" or "auto"
list_width = ""        # cells ("52") or a share ("30%"); empty follows the screen
card_preview = "auto"  # the board's card preview: "side", "sheet", "auto" or "off"
card_preview_width = "" # the side panel: cells ("70") or a share ("40%"); empty is 40%
```

`space` on a card of the task board's columns shows the card whole.
`card_preview` says where: `side` in a panel beside the columns, `sheet` over
them, `auto` beside them on a screen 140 columns or wider that keeps three
columns and over them otherwise, and `off` never. The panel is 40% of the
view by default, at least 44 and at most 96 cells. A drag on its edge sets
the width, and the view keeps it; a double click on the edge gives it back to
`card_preview_width`.

| Value | What the view shows |
|---|---|
| `column` | The list column, for any number of agents |
| `strip` | A strip of chips above the focus, when the chips fit on one row |
| `auto` | The strip for two agents or fewer, and the column for more |

The view shows the column for a folded group, the task board, a filter and a
search. On a screen narrower than 80 columns the list fills the screen, and
`l` opens the focus. `\` in the view switches between the column and the
strip. tuios keeps that choice in `sidebar.json` in its state folder. The
choice wins over `view_list`. The settings page has this option on the
Behavior tab, as "Agent list".

## Editor height

A reply to an agent, a reply to many, a mail draft and a new agent's task are
written in an editor that grows as you type, then scrolls. `reply_rows` sets
how many rows it grows to:

```toml
[agents]
reply_rows = 0   # default: each editor's own height
```

| Value | What the editors do |
|---|---|
| `0` | Each keeps its own height: 8 rows for a reply, 6 for a mail draft, 3 for a new agent's task |
| `1` to `200` | Each grows to that many rows |

An editor never takes more rows than its view has: on a short screen it
stops at the view's edge and scrolls. `alt+m` in an editor switches it to the
whole height of its area and back. The settings page has this option on the
Behavior tab, as "Reply rows".

## Report pane states to your terminal

When tuios runs in a terminal that reads OSC 7501, the Program Status
Protocol (Rex does), it reports the agent state of each pane there.
`host_program_status` in `[agents]` says how, or turns it off:

```toml
[agents]
host_program_status = "session"   # session (default), panes, off
```

`session` asks the terminal when a client starts and, when the terminal
answers, reports one record for the attached session: the most urgent state
of its panes, and a message that counts them, such as "1 needs input,
2 working". A terminal shows one record for each of its own panes, so this is
the whole session in the pane tuios runs in. `panes` asks the same way and
reports one record for each pane. `off` never asks. `auto`, the old name of
the default, still reads as `session`. A client reads the value when it starts.
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

tuios also finds plugins in `$XDG_CONFIG_HOME/tuios/plugins/<folder>/`, in
`plugins.lock`, and in herdr's own `plugins.json` and managed checkouts. To
find a plugin runs nothing. A plugin runs only when its id is in `enabled`.

`tuios plugins install SOURCE` writes `plugins.lock` beside `config.toml`.
It pins each installed plugin to one commit. Keep the file with your config,
and run `tuios plugins install` on a new machine to get the same commits.
`tuios plugins uninstall ID` removes the plugin from the file.

An enabled plugin runs its commands with your rights, outside every pane.
Enable only plugins that you trust.

Use the commands to change the table. They change only the `enabled` or
`dirs` line and keep the rest of the file:

```bash
tuios plugins enable example.notes
tuios plugins disable example.notes
tuios plugins link ~/src/my-herdr-plugin
tuios plugins install owner/repo        # also adds the id to enabled
tuios plugins uninstall example.notes   # also removes the id from enabled
```

You must run them from a terminal outside tuios. A change to the file that
removes a plugin from `enabled` applies at once. A change that adds a plugin
to `enabled`, or a folder to `dirs`, waits for `tuios config apply` or a
daemon restart. `tuios plugins enable ID` applies only the plugin that it
names. A new folder waits because it can hold a
plugin with the id of a plugin that you enabled.
[PLUGINS.md](PLUGINS.md) says what runs and when.

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

[agents.mail]
wake = true
questions_to_parent = true

[agents.checkpoints]
enabled = true
keep = 50
max_untracked_mb = 50

[agents.presets.worker]
agent = "claude"
args = ["--verbose"]
add_dirs = ["~/notes"]
grants = ["read", "write", "task"]
model = "opus"
env = ["GOMAXPROCS=4"]
after_ready = ["/remote-control", "Enter", "Escape"]
ready_timeout = 180000
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
  ignored, with a warning. An allow of a risky approval in the Inbox asks
  first and takes `enter`, and the daemon refuses one that does not name the
  rules. `panes_may_allow` lets a pane holding the `respond` grant allow a
  risky call; it is off, so only you can. The shipped rules are listed under
  [Risk rules](AGENT_STATE.md#risk-rules). They are a speed bump, not a
  sandbox. The daemon reads the risk rules again when the file changes. A
  change that adds a rule applies at once. A change that removes a rule, or
  sets `panes_may_allow`, waits, because a process in a pane can write
  config.toml. The rules in force stay, and panes may not allow a risky call.
  Run `tuios config apply` from a terminal outside tuios to apply the change,
  or restart the daemon. Until then the Inbox shows that a change waits.
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
- `[agents.mail]` `wake` types one line into an agent at rest that has mail
  nobody told it about, through the delivery queue: the count, the senders
  and the command that reads the mail, never the mail itself. It is true by
  default. Set it to false to stop it. The daemon reads it at start. A file
  change turns it off at once. Turning it on again waits for `tuios config
  apply` from outside tuios, or a restart. See
  [AGENT_STATE.md](AGENT_STATE.md#mail-that-wakes-an-agent-at-rest).
- `[agents.mail]` `questions_to_parent` sends a question from a child agent
  that `tuios delegate` or `tuios start-agent` started to its parent agent by
  mail first, and lets the parent answer it with `tuios respond`. With it off
  nothing is mailed, and the parent has no right to answer. The Inbox
  shows the question as "With the parent agent" for 10 minutes, and does not
  count it or alert on it. After that, it is a normal Inbox item. It is true by
  default. Set it to false to send every such question to the Inbox at once.
  A file change turns it off at once. Turning it on again waits for `tuios
  config apply` from outside tuios, or a restart. An approval of a child agent
  always goes to the person, and no pane can answer it.
- `[agents.presets.NAME]` names a `tuios start-agent --preset NAME`: the
  agent, and any of `args`, `add_dirs`, `grants`, `model`, `effort`,
  `permission_mode`, `env` (each `NAME=VALUE`), `after_ready` and
  `ready_timeout` (milliseconds). `add_dirs` passes the harness's
  `[launch] add_dir_flag` (Claude Code: `--add-dir`) once per directory,
  before `args`, with `~` as the home folder of the machine the agent starts
  on. A harness with no such flag refuses it. `after_ready` is sent once the
  agent is ready, before `--prompt`, step by step: a step that is one key
  name (`Enter`, `Escape`, `Tab`, `C-c`) is that key, anything else is typed
  as text. A step is refused while the agent waits on a prompt of its own,
  which stops the rest. A file change applies at once.
- `[agents.checkpoints]` saves the git work tree of an agent's pane each time
  the agent finishes a turn that changed a file (`tuios checkpoint`).
  `enabled` is true by default. `keep` is how many checkpoints one pane keeps:
  50 by default, at most 1000. `max_untracked_mb` leaves an untracked file
  larger than this many megabytes out of a checkpoint: 50 by default, and a
  negative value means no limit. `tuios checkpoint list` names the files left
  out, and a restore does not change them. The same limit holds for
  `tuios review`, review-stage, fan compare and `tuios fan diff`, which never
  read such a file: review lists it as not read. Reading one would copy it
  into the repository's object store, and through a clean filter such as Git
  LFS's into that store too, on every read. The daemon reads these at start
  and again when the file changes. See [Turn checkpoints](AGENT_STATE.md#turn-checkpoints).

One rail option goes with them: [`appearance.sidebar.agent_rest_fold`](config-options.md#appearance.sidebar.agent_rest_fold), how long
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

A second rail option goes with it: [`appearance.sidebar.subagent_rows`](config-options.md#appearance.sidebar.subagent_rows), how an
agent row shows its subagents. `rows` (the default) draws a row for each
subagent under its agent's row, with its mark (`●` running, `■` done, `×`
failed, `○` stopped), what it was asked to do, and on a tall section the tool
it runs or how it ended, while the agent's own row says `3 of 4 run`. When the
section has too little room it leaves the finished ones out first, then
shows the newest few and `+2 more`, then none; it never hides an agent row to
show a subagent row. `tree` draws the same rows on tree lines (`├─` and
`└─`, `|-` and `` `- `` in ASCII). `count` draws the count alone, as the rail
did before.
An agent at rest with subagents at work wears `◊` (`%` in ASCII) in the
working ink, on the rail, its title bar and the switcher. The settings page
shows the option on its Sidebar tab once an agent has been seen. See
[Subagents on the rail](AGENT_STATE.md#subagents-on-the-rail).

Four more rail options change how the subagent rows look. Each default draws
the rows as described above.

These options are in the [option reference](config-options.md): [`appearance.sidebar.subagent_collapse`](config-options.md#appearance.sidebar.subagent_collapse), [`appearance.sidebar.subagent_fields`](config-options.md#appearance.sidebar.subagent_fields), [`appearance.sidebar.subagent_max_rows`](config-options.md#appearance.sidebar.subagent_max_rows), [`appearance.sidebar.subagent_hide_finished`](config-options.md#appearance.sidebar.subagent_hide_finished).

A click on the heading, or `enter` on it, opens or closes the rows. `space`
does the same with the rail cursor on the agent row or on its heading. The
rail keeps the state of each pane until the client stops. A closed heading
shows `▸`, and on a tall section its second line names the subagents.

The daemon keeps a finished subagent for 60 seconds (a failed one for 5
minutes). `subagent_hide_finished` can make a row go earlier, but it cannot
keep a row longer than the daemon keeps it.

```toml
[appearance.sidebar]
subagent_rows = "tree"
subagent_collapse = "expanded"
subagent_fields = "mark,name,time"
subagent_max_rows = 4
subagent_hide_finished = "20s"
```

Each of the five subagent options can be set again in
`[appearance.sidebar.left]` or `[appearance.sidebar.right]`, for the rail on
that edge. An option that the edge table does not set uses the value from
`[appearance.sidebar]`.

The agent row in `[appearance.sidebar.agent_row]` has five tokens for what
tuios feeds itself: `now` (what a working agent is doing, drawn only while it
works), `context` (`ctx 84%` in the warning ink, drawn only at 80% or more),
`subagents` (`2 subagents`, how many subagents the agent has at work, drawn
on any row while any run, from Claude Code's hooks, and `3 of 4 run` when
the subagents have rows of their own), `pr` (`PR #12 open
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

## The task board

`b` in the agents view shows the task board: the cards of every session,
one column for each stage ([AGENT_STATE.md](AGENT_STATE.md#the-agents-view)).
`B` shows the stages as a list instead. `[tasks] stages` picks the stages it
shows. The order stays todo, plan, doing, verify,
review, shipped, done whatever the list's order, and a stage the list leaves
out has no column. Empty, or naming no known stage, shows every stage. An
unknown name is reported as a config warning and ignored.

```toml
[tasks]
stages = ["todo", "doing", "verify"]
```

`ctrl+b +`, or "New task" in the palette, adds a card from anywhere
([KEYBINDINGS.md](KEYBINDINGS.md#new-task)). An image in it is attached to
the card, and the prompt names it.

tuios moves a card on its own when it records a fact about the card's work: a
plan held for approval and then approved, a fan check, a pull request
opening, merging or closing ([CLI_REFERENCE.md](CLI_REFERENCE.md#tuios-task)
has the table). A move fires only from the stages that fact moves a card
from, never on a blocked or done card, and the card's history names the
reason. The plan stage is reached only when `[agents.approvals]` holds plans
for the harness and the pane is not in front of you. `auto_stage = false`
leaves every stage to you. A fan's stamp on a card (`tuios fan --task`) is
still written, though the card then stays where it is.

```toml
[tasks]
auto_stage = false
```

`machine_cap` is how many agents may work on this machine before "Any
machine" starts a new one on another machine
([CLI_REFERENCE.md](CLI_REFERENCE.md#any-machine)). Agents that work,
agents that are starting, and picks made in the last 90 seconds that have not
started yet all count. Each machine reads its own cap and says it when asked.
`0`, the default, is no cap.

```toml
[tasks]
machine_cap = 4
```

`tuios task attach` puts files and links on a card
([CLI_REFERENCE.md](CLI_REFERENCE.md#tuios-task)). The daemon stores each file
once under its state folder and removes it when no card names it.
`attachment_max_mib` is the largest file in MiB (default 8, at most 8: the
file travels in one request), `attachments_per_card` the most one card holds
(default 20), and `attachment_session_mib` the most the files of one
session's cards take together (default 256). A file over a limit is refused
with an error that names the option.

```toml
[tasks]
attachment_max_mib = 4
attachments_per_card = 10
```

## What a pane may do

Every pane holds grants that say what a process in it may do through tuios:
`read` (its own session and fan group), `write` (type into its own session,
into panes that hold nothing it does not), `fan` (write in its fan group and
start agents), `respond` (answer prompts without you, and type into a pane
waiting on one), `files` (copy files with `tuios cp`, see below) and `admin`
(everything else, as before grants). A pane started
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

`files` lets a pane copy files without `admin`. A copy may start when each
end on this machine is inside the pane's folder (its worktree, else its
working folder). Each end on a host must be under its home folder, or be its
receive folder. The pane sees and controls only the copies it started.
`admin` includes `files`. To let every pane copy near its work under
`strict`, add it to the list:

```toml
[agents.permissions]
mode = "strict"
grants = ["read", "write", "fan", "files"]
```

`task` lets a pane add, claim and finish the task cards of its own session
(`tuios task`) without `admin`. A pane claims only for itself and changes only
the card it holds. `admin` includes `task`. Under `strict`, give it to the
panes that take work from a conductor:

```toml
[agents.permissions]
mode = "strict"
grants = ["read", "write", "fan", "task"]
```

The daemon reads the table when it starts and again when the file changes. A
change that gives panes less reaches every pane on the default at its next
call. A change that gives more, such as `strict` to `open`, waits, because a
process in a pane can write config.toml. Run `tuios config apply` from a
terminal outside tuios to apply it, or restart the daemon. Until then the
daemon logs it, and `tuios pane-grants` says so. When a start finds that
panes hold more than at the last run, it says so in the log, in
`tuios pane-grants` and in the Inbox. Like
`[agents.approvals]`, it is not in `list-options` and `tuios set-config` cannot
change it, so no pane can loosen it. The [paste buffers](#paste-buffers) hold
what you copied, so a pane needs `read` to read them, `write` to change them,
and both to paste one.
[AGENT_STATE.md](AGENT_STATE.md#what-a-pane-may-do) has the whole model.

## SSH server limits

The `[ssh]` section sets the limits of the `tuios ssh` server. The server reads
it when it starts. The defaults do not stop a normal login.

```toml
[ssh]
handshake_timeout = "120s" # time to finish the login; 0 turns the limit off
idle_timeout = "0"         # close a connection with no traffic for this long; 0 is off
max_handshakes = 32        # connections that log in at the same time; 0 is no limit
max_sessions = 64          # sessions open at the same time; 0 is no limit
```

- `handshake_timeout`: The server closes a connection that does not log in
  within this time. It is the same as `LoginGraceTime` in OpenSSH. The limit
  ends when the login is complete, so it does not close a session that you
  use.
- `idle_timeout`: The server closes a connection after this time with no
  traffic in either direction. It is off by default. A session that shows a
  clock or other output that changes stays open.
- `max_handshakes`: When this many connections are logging in, the server
  closes a new connection at once. A connection that is logged in does not
  count.
- `max_sessions`: When this many sessions are open, over all connections, the
  server refuses a new session. The client sees a message. The refused session
  does not attach to a session and does not open a pane.

A duration is a value such as `90s`, `30m` or `2h`. A value that does not
parse, or a negative value, gives a warning and the default.
`tuios ssh --handshake-timeout`, `--idle-timeout`, `--max-handshakes` and
`--max-sessions` replace these values for one run. The server writes the limits
it uses to its log when it starts.

Like `[hosts]`, the section is not in `list-options`, and `tuios set-config`
cannot change it.

## Move between machines

The `next_host` and `prev_host` keybind actions go to a session on the next or
the previous machine: this machine first, then the `[hosts]` table. They have
no default key. See
[KEYBINDINGS.md](KEYBINDINGS.md#cycle-through-machines).

```toml
[keybindings.window_management]
next_host = ["alt+."]
prev_host = ["alt+,"]
```

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
| `list` | Read: sessions, windows, captures, screenshots, agent state, the Inbox, prompts, waits and the event stream. It also gives a live stream of the bytes of any pane (`stream-pane`). |
| `mail` | Send and read agent mail, and use the stash. |
| `open` | Start processes: sessions, windows, worktrees, fans, `start-agent`, clones of a repository by its URL, and panes this machine runs for it. |
| `write` | Change what is here: type into panes, `run` a line at a prompt, close and move windows, set options, layouts and names, report agent state, and attach. Also read a worktree's work out with `bundle-worktree` (`tuios worktree pull`), since a machine that may type into a shell here can read those files already. |
| `respond` | Answer for the person: prompts, held approvals, `ask-human` questions, dismissing Inbox items, and passing on held mail. Also type into a pane that waits on a prompt, from a pane on the other machine. The person on the other machine, outside every pane, needs only `write` for that. |
| `transcripts` | Read the conversation of an agent here, from the person's own client attached over the link. The client must send the nonce of its attach: an agent in a pane, a link the other machine did not vouch for, and a caller with no nonce are refused. `respond` includes it. |
| `files` | Read and write files here with the file verbs: a file's bytes, copies in and out (`transfer-start` on the other machine), new folders, moves and removes. It is apart from `write`, so you can stop one machine's copies and keep its typing. A write that arrives over the link lands only in the folders of `files_roots` (the receive folder unless you add more, see below), and the drop folder for dropped files, never in `~/.ssh`, `~/.gnupg`, credentials, shell start files, login items or tuios's own config, whatever `allow` and `files_roots` say. A read never returns keys or credentials. It also lets a window here that the other machine draws show kitty images that a program names by path (`kitten icat --transfer-mode=file`): this machine reads the file under the same read rules and `files_read_roots`, and sends the bytes. See [protocol.md](protocol.md#files-and-transfers). |

With no table, a machine may `list`, `mail`, `open`, `write`, `files` and
`transcripts`. `respond` is opt-in. Relaying on to this machine's own hosts needs the five capabilities a
link had before `files` (`list`, `mail`, `open`, `write`, `respond`), because
the next machine sees the relay as coming from this one; a table written for an
older tuios that allows those five still relays. A copy this machine runs for
another one also needs `files`. A table that sets `allow` without `files` keeps
that machine from the file verbs. An `allow` that is set replaces the inherited
list; `allow = []` allows nothing but `hello`.

**Where a link may write: `files_roots`.** A machine with `files` writes only
under the folders `files_roots` names, plus the drop folder. Unset, it is one
receive folder: `~/Downloads/tuios` on macOS and Windows, and
`$XDG_DOWNLOAD_DIR/tuios` (or `~/Downloads/tuios`) on other systems. tuios
makes it on first use. Widen it on purpose, per machine:

```toml
[hosts.laptop]
allow = ["list", "files"]
files_roots = ["~/Downloads/tuios", "~/dev"]
```

A path is absolute or starts with `~`. `files_roots = ["~"]` lets the machine
write anywhere in your home folder; the deny list below still holds inside
every root, so even then it cannot touch `~/.ssh`, a shell start file, or the
config folders of the agent harnesses tuios integrates (Claude Code, Codex,
Gemini CLI, opencode, Cursor and the others), which name commands those tools
run. A write outside the roots is refused, and the refusal says where writes
may land and which setting to change. A reload that widens `files_roots`, like
one that widens `allow`, waits for `tuios config apply`.

**The mode of a file a link writes.** A file that another machine writes here
gets the sender's permission bits under this machine's umask, as `cp` and
`scp` without `-p` do. It never gets group or world write. The execute bits
stay, so a script stays runnable. This holds for a paired device too.

**Where a link may read: `files_read_roots`.** Unset, a machine with `files`
reads any file you can read, apart from the read deny list: keys, credentials
(also the logins and API keys of agent tools such as Gemini CLI, opencode, Qwen
Code, Kilo, Pi, oh-my-pi, Hermes, Amp and the Copilot CLI, Cursor's MCP servers, the sops age keys, atuin's
key and history, Ollama's key, and the OCI, doctl and ngrok credentials),
shell start files, `~/.gitconfig`, keyrings, shell histories, browser profiles,
`/proc` and tuios's own state. A
deny list can never name every secret on a machine. Set `files_read_roots` to
hold reads to the folders you name, plus the folders the machine may write.
That is the stronger choice:

```toml
[hosts.laptop]
files_read_roots = ["~/share"]
```

The rule covers `file-read`, `file-preview`, `file-hash`, `file-walk`, a copy out, and the
names and sizes `file-stat` and `file-list` show. The read deny list still
holds inside every root. A file with more than one name (a hard link) is never
read over a link, inside a root or outside one. Copy it to a new file to share
it. A reload that removes or widens `files_read_roots`
waits for `tuios config apply`. A reload that narrows it applies at once.

**How much a link may write.** A write over a link must leave
`files_min_free` free on its disk: `"1GiB"` unless you set it. The value is a
whole number with `B`, `KB`, `MB`, `GB`, `TB`, `KiB`, `MiB`, `GiB` or `TiB`.
`"0"` turns the check off, and a reload that lowers it waits for
`tuios config apply`. A write that does not fit is refused as
`disk_full` before a byte moves. One machine may have at most 8 write streams
and 64 unfinished part files here at once. Past that, a write is `busy`, and
a copy waits and tries again.

A machine may also make at most `files_max_entries` new files and folders
here in ten minutes: 20,000 unless you set it. An empty file uses no bytes,
so `files_min_free` does not stop a machine from making millions of them.
Past the bound, a new file or folder is `busy`, and a copy waits and goes on.
The count goes down evenly over the ten minutes. A folder copy of 20,000 files
lands at full speed, and a larger one goes on at about 33 files a second.
`0` turns the bound off. A reload that raises it waits for `tuios config apply`. The drop folder holds at most 256 MiB for all
drops together, and a machine writes there only inside a drop that
`file-drop-dir` made.

```toml
[hosts."*"]
files_min_free = "5GiB"
files_max_entries = 50000
```

A copy that another machine starts here with `transfer-start` or `drop-files`
is held to these rules too, also after tuios restarts. When a reload or a
restart takes `files` away from that machine, its copies here stop.

> **Trust.** A machine with `files` can write any file inside its roots and
> read any file outside the deny list (or inside `files_read_roots`). A file copy is a way to run code: a
> binary dropped on your `PATH`, a config a program reads. Give `files`, and a
> wide `files_roots`, only to machines you trust as you trust your own.

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
shell, and can claim any name. A pinned name needs a table of its own. A key
pinned to a name with no `[hosts.NAME]` table may do nothing here, so a key
that outlives its table does not get `[hosts."*"]` or the default. A table with a policy and no `addr` is not
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

The daemon's links never ask for a password, a passphrase or a code. They
run ssh with `BatchMode=yes`. A client that can ask you, such as tuios-gpui,
signs in to a machine itself and keeps that connection open as an ssh master
in the folder `cm` beside the daemon's socket. That folder is
`$XDG_RUNTIME_DIR/tuios/cm`, `/run/user/<uid>/tuios/cm` when
`XDG_RUNTIME_DIR` is not set, or `/tmp/tuios-<uid>/cm` when neither exists. When that folder and its parent exist, are not
symbolic links, belong to you and only you can open them, each link and
`tuios hosts test` run ssh with `-o ControlMaster=no` and `-o ControlPath=<folder>/%C`. The link then
uses the master when one is open for that machine, and connects as usual when
none is. A machine that needs a password or a second factor then works for
as long as the master is open. No secret is stored.

A link connects as usual and does not use the folder in these cases:

- Your `~/.ssh/config` gives the host a `ControlPath`. The link uses the
  master you open there.
- The host forwards the agent (`ForwardAgent`). A connection through a master
  gets the forwarding of the master, so the link would lose its agent.
- A socket path in the folder is too long. The limit is 107 characters on
  Linux and 103 on macOS. Set `XDG_RUNTIME_DIR` to a shorter folder.
- tuios runs on Windows. Win32-OpenSSH has no `ControlMaster`.

A master connection can outlive the network under it, for example when a
laptop sleeps. The link's ssh then waits on it, and the link's keepalives do
not reach it. The link pings its host when it carried nothing for 15 seconds.
When the host does not answer, the link ends, and the next dial runs ssh with
`-o ControlPath=none` and opens a connection of its own. The link does the
same after this machine wakes from sleep, and after a dial that timed out. A machine that needs
a password or a second factor fails that one dial, and the dial after it uses
the master again.

`%C` names a master by the local host, the remote host, the port and the
user. It does not include a `ProxyCommand`, so a host reached through two
different proxy commands shares one master. It includes the jump host only
on OpenSSH 10.6 and later. On an earlier version, a host reached through two
different jump hosts also shares one master.

`tailscale_login` is the origin of a Headscale server that sends the Tailscale
SSH check for this host, for example `"https://headscale.example"`. tuios
shows and opens a sign-in link only on `login.tailscale.com`,
`controlplane.tailscale.com` and this origin, over https, with the host matched
exactly. Anything on the host can print the banner that carries the link. See
[Tailscale SSH check mode](SESSIONS.md#tailscale-ssh-check-mode).

## Applying changes

TUIOS watches the file. A save is applied about 200 ms later, with no restart. The command palette also has a **Reload Config** entry.

A file with an error is not applied. The running settings stay, and the error is shown on screen.

Most of the file applies live. These parts do not:

| Change | When it applies |
|--------|-----------------|
| `[hooks]` | The daemon reads it at start. Run `tuios restart-server` after editing a hook the daemon runs |
| `[daemon]` | The daemon reads it at start. Run `tuios restart-server` after editing it. `tuios set-config daemon.log_level` also changes the log level of a running daemon |
| `[startup]` | The next new session |
| `scrollback_lines` | Panes opened afterwards |
| Custom theme files | See [Custom themes](#custom-themes) |

The daemon also follows the file for `[hosts]`, `[agents]`, `[notify]` and `preferred_shell`. Removing a host closes its link at once. A change that gives panes or other machines more waits for `tuios config apply`. This covers a new host, a wider `[agents.permissions]` and a wider link policy. See [Changes that wait](https://tuios.dev/docs/agents#changes-that-wait).

### Changing a setting at runtime

```bash
tuios list-options                       # every path, with default and accepted values
tuios list-options --section sidebar     # one group
tuios list-options --search "pane bg"    # fuzzy search, best match first
tuios get-config appearance.border_style
tuios set-config appearance.border_style thick
tuios config browse                      # search the options and set one, in an explorer
```

`tuios config browse` shows each option with its type, default, current value and source. Press <kbd>/</kbd> to search. Press <kbd>Enter</kbd> on an option to set it the same way as `set-config`. <kbd>q</kbd> or <kbd>Esc</kbd> leaves it. It needs a running daemon. See [Explorers](CLI_REFERENCE.md#tuios-config).

`set-config` changes a running session. With a client attached, the client applies the value and writes it to the config. With no client attached, the daemon keeps the value for this session. The next client that attaches applies it and writes it to the config. A path the registry does not know is refused. An `[appearance]` option also answers to its bare name, so `border_style` and [`appearance.border_style`](config-options.md#appearance.border_style) are the same path. `set-config` cannot reach `[keybindings]`, `[hooks]`, `[hosts]`, `[tailscale]`, `[agents]` or the `[dock]` lists, because those are maps and lists rather than single values. For `[agents]` this is also a safeguard: a program in a pane cannot loosen it through TUIOS. [`dock.clock.format`](config-options.md#dock.clock.format) is the one `[dock]` key it can set. [`keybindings.window_mode`](config-options.md#keybindings.window_mode) is the one `[keybindings]` key it can set.

### The in-app settings page

The settings page edits every option in the registry, in the same groups `list-options` prints. Open it with:

- <kbd>,</kbd> in window mode (`open_settings`)
- <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>,</kbd> (`open_settings`)
- **Settings** in the <kbd>Ctrl</kbd>+<kbd>P</kbd> command palette

Most rows apply as you change them. Colour options open a colour picker. Two rows open editors of their own: the rail's [sections](#sections) and the dock's [components](#the-dock-table).

The page reopens on the tab, row and search you left it on. <kbd>1</kbd> to <kbd>9</kbd> go to a tab. A row whose value in force differs from its default has a dot after its name. That includes the rows the page draws by hand, such as the theme, glyph set, shared borders, pane gap, the master and column ratios, the rail's sections, max FPS, show keys, spotlight and the screen saver effect. For the pane geometry rows the value compared is the session's, not the config's. <kbd>Backspace</kbd> (or <kbd>Delete</kbd>) resets a changed row to its default, and its description says what the default is. The hand-drawn rows have no reset: <kbd>Backspace</kbd> on one says so, and you change it back with its own control. <kbd>Ctrl</kbd>+<kbd>Z</kbd> undoes the last change made on the page.

<kbd>/</kbd>, or any letter the page does not use, searches every tab. It ranks each row by its name, its config key, its value and its description, and each result names its tab. In the results, <kbd>←</kbd>, <kbd>→</kbd> and <kbd>Enter</kbd> change the row where it is, <kbd>Tab</kbd> goes to the row on its own tab, and <kbd>Esc</kbd> clears the search. The command palette reaches the same rows by name (`settings: pane background`), and `tuios list-options --search` runs the same search from a shell.

> **A save changes only the key.** The settings page writes only the key that you changed. Your comments, blank lines and key order stay. When the config has more than one file, the change goes to the file that sets the key. See [How TUIOS saves a change](https://tuios.dev/docs/config-files#how-tuios-saves-a-change).

### Command-line flags

Interface flags override the file for one run. They are accepted by `tuios`, `tuios new`, `tuios attach`, `tuios ssh` and `tuios tape play`. A flag takes the values its option takes. A value the option does not take stops TUIOS before it starts, with the message `tuios set-config` gives. A switch flag also takes a value, such as `--show-clock=false`. The config file does not change.

| Flag | Option |
|------|--------|
| `--theme` | [`appearance.theme`](config-options.md#appearance.theme) |
| `--border-style` | [`appearance.border_style`](config-options.md#appearance.border_style) |
| `--dockbar-position` | [`appearance.dockbar_position`](config-options.md#appearance.dockbar_position) |
| `--window-title-position` | [`appearance.window_title_position`](config-options.md#appearance.window_title_position) |
| `--window-button-style` | [`appearance.window_button_style`](config-options.md#appearance.window_button_style) |
| `--window-button-position` | [`appearance.window_button_position`](config-options.md#appearance.window_button_position) |
| `--hide-window-buttons` | [`appearance.hide_window_buttons`](config-options.md#appearance.hide_window_buttons) |
| `--hide-scrollbar` | [`appearance.hide_scrollbar`](config-options.md#appearance.hide_scrollbar) |
| `--shared-borders` | [`appearance.shared_borders`](config-options.md#appearance.shared_borders) |
| `--scrollback-lines` | [`appearance.scrollback_lines`](config-options.md#appearance.scrollback_lines) |
| `--zoom-max-width` | [`appearance.zoom_max_width`](config-options.md#appearance.zoom_max_width) |
| `--no-animations` | `appearance.motion = "none"` |
| `--confirm-quit` | [`appearance.confirm_quit`](config-options.md#appearance.confirm_quit) |
| `--show-clock`, `--show-cpu`, `--show-ram` | [`appearance.show_clock`](config-options.md#appearance.show_clock), `show_cpu`, `show_ram` |
| `--show-keys` | [`debug.show_key_events`](config-options.md#debug.show_key_events) |
| `--ascii-only` | none. Draws ASCII instead of Nerd Font glyphs |

## Validation

The file is checked when it loads. Problems are errors or warnings.

**Errors** are keys TUIOS cannot read, such as `"ctrl+nope"` or `"ctrl+"`. TUIOS ignores each of these keys and loads the rest of the file. See [When a key is wrong](#when-a-key-is-wrong).

**Warnings** keep the key. They cover unknown values (which fall back to the default), unknown title placeholders, actions with no key, two actions on one key in the same section, and the macOS `alt+` advice.

TUIOS shows the number of problems when it starts, for example `2 config problems, see the log viewer`. The log viewer (leader <kbd>D</kbd> <kbd>l</kbd>) shows each problem.

> **A file that does not parse.** If `config.toml` is not valid TOML, TUIOS cannot read any of it. At startup, TUIOS uses the built-in defaults and shows a config problem. If you save such a file while TUIOS runs, TUIOS keeps the config that it has.

## When a key is wrong

TUIOS ignores a key that it cannot read, such as `ctrl+nope`. It loads the rest of the file. Your other keys, the leader and the other settings stay as you wrote them.

The action of an ignored key gets a key as follows:

- If the action has another key that TUIOS can read, the action keeps that key.
- If the action has no other key, it gets its default key.
- If another action already has that default key, the action has no key.
- If TUIOS cannot read `leader_key`, the leader is `ctrl+b` until you correct it.

TUIOS does not change your file. Correct the key, then save. The change applies at once.

To find the ignored keys:

- Look for the `config problems` notification when TUIOS starts.
- Open the log viewer (leader <kbd>D</kbd> <kbd>l</kbd>). Each line names the key and its file.
- Run `tuios keybinds doctor`. It lists each key by file and says what TUIOS does instead. See [`tuios keybinds`](CLI_REFERENCE.md#tuios-keybinds).
- Run `tuios config apply`. It lists the keys it ignored.

TUIOS also warns about a name it does not know: a table under `[keybindings]` that is not a keybinding table, and an action name in a table that no action has, such as `copy_mode_wrod`. The keys of such a name do nothing. The warning names the file and the table and, when one is close, the action you meant. It shows in the log viewer and in `tuios config apply`.

The same rule applies to keys in an included file and in `config.d`.

## Troubleshooting

**My config seems ignored.**

1. Run `tuios config path` to confirm which file TUIOS reads. Run `tuios config files` to see every file that TUIOS reads.
2. Look for the `config problems` notification when TUIOS starts. Open the log viewer (leader <kbd>D</kbd> <kbd>l</kbd>) to see each problem.
3. Run `tuios keybinds doctor`. It lists each key that TUIOS cannot read, and the leader that TUIOS uses. TUIOS ignores only those keys. See [When a key is wrong](#when-a-key-is-wrong).
4. If the log says the file does not parse, correct the TOML. TUIOS uses the defaults until then.
5. On Linux, a key copied from a Mac works. `opt+` is `alt+` on every platform.

**A setting changed nothing.**

1. Check the [table of what applies live](#applying-changes). A saved keybinding works at the next key press.
2. Check that the file parsed. Look for an error notification, or run `tuios keybinds list`.
3. Check whether a command-line flag is overriding it.
4. Check whether a later file sets the key. Run `tuios config origin <key>`. An old `config.toml` that sets every key hides the other files. Run `tuios config prune` to remove its default values. See [Split the Config](https://tuios.dev/docs/config-files#clean-up-an-old-configtoml).
5. For a hook, check the event name spelling with `tuios list-hooks`.

**A key does nothing.** Run `tuios keybinds explain <key>` to see every place it is bound, and `tuios keybinds doctor` for conflicts.

## Examples

### Browser-style keys

```toml
[keybindings.window_management]
new_window = ["ctrl+t"]
close_window = ["ctrl+w"]
next_window = ["ctrl+tab"]
prev_window = ["ctrl+shift+tab"]
```

### Screen-style leader

```toml
[keybindings]
leader_key = "ctrl+a"

[keybindings.prefix_mode]
new_window = ["c"]
split_horizontal = ["S"]
split_vertical = ["V"]
prefix_session_switcher = ["s"]
prefix_scrollback = ["/"]
```

In this example, `S` and `s` are taken by the splits and the switcher, so the scrollback browser moves to `/`. Keys inside one section must not collide.

### A quiet, dense layout

```toml
[appearance]
theme = "nord"
border_style = "hidden"
dockbar_position = "hidden"
shared_borders = true
whichkey_enabled = false
window_title_format = "{index}: {title}"
scrollback_lines = 50000
max_fps = 120
```

## Appearance: the `[appearance]` table

All keys below go under `[appearance]`.

### Theme and chrome

These options are in the [option reference](config-options.md): [`appearance.theme`](config-options.md#appearance.theme), [`appearance.glyphs`](config-options.md#appearance.glyphs), [`appearance.border_style`](config-options.md#appearance.border_style), [`appearance.border_focused_color`](config-options.md#appearance.border_focused_color), [`appearance.border_focused_color_window`](config-options.md#appearance.border_focused_color_window), [`appearance.border_focused_color_terminal`](config-options.md#appearance.border_focused_color_terminal), [`appearance.border_unfocused_color`](config-options.md#appearance.border_unfocused_color), [`appearance.frame_panels`](config-options.md#appearance.frame_panels), [`appearance.shared_borders`](config-options.md#appearance.shared_borders), [`appearance.gap`](config-options.md#appearance.gap), [`appearance.dim_unfocused`](config-options.md#appearance.dim_unfocused), [`appearance.dim_foreground`](config-options.md#appearance.dim_foreground), [`appearance.dim_multifocus`](config-options.md#appearance.dim_multifocus), [`appearance.session_colors`](config-options.md#appearance.session_colors), [`appearance.session_border`](config-options.md#appearance.session_border), [`appearance.zen_mode`](config-options.md#appearance.zen_mode), [`appearance.panel_padding`](config-options.md#appearance.panel_padding), [`appearance.motion`](config-options.md#appearance.motion), [`appearance.animation_speed`](config-options.md#appearance.animation_speed), [`appearance.theme_transition`](config-options.md#appearance.theme_transition), [`appearance.theme_transition_duration`](config-options.md#appearance.theme_transition_duration), [`appearance.modal_dim`](config-options.md#appearance.modal_dim), [`appearance.popup_dim`](config-options.md#appearance.popup_dim), [`appearance.max_fps`](config-options.md#appearance.max_fps), [`appearance.wrap_lists`](config-options.md#appearance.wrap_lists).

`animations_enabled`, the key `motion` replaces, is still read from older files: `false` reads as `motion = "none"`, and `true` changes nothing. Where both are set, `motion` wins.

### The `[motion]` table

The `[motion]` table turns each motion on or off by itself and sets how long it takes. [`appearance.motion`](config-options.md#appearance.motion) and `animation_speed` stay on top: `motion = "none"` and calm mode stop every motion below, and the speed divides every length.

These options are in the [option reference](config-options.md): [`motion.workspace`](config-options.md#motion.workspace), [`motion.layout`](config-options.md#motion.layout), [`motion.zoom`](config-options.md#motion.zoom), [`motion.overlays`](config-options.md#motion.overlays), [`motion.sidebar`](config-options.md#motion.sidebar), [`motion.scroll`](config-options.md#motion.scroll), [`motion.copy`](config-options.md#motion.copy), [`motion.confetti`](config-options.md#motion.confetti).

`workspace`, `layout`, `zoom`, `overlays` and `scroll` also take `NAME_ms`, the length in milliseconds at speed 100, from 0 to 1000. `0` is a cut. The defaults are 200 for `workspace` and `zoom`, 300 for `layout` and `scroll`, and 120 for `overlays`. `layout_ms` also sets the snap and swap length, which is two thirds of it.

```toml
[motion]
workspace = "fade"
workspace_ms = 250
zoom = "off"        # zoom is a cut. The workspace switch still fades.
```

The old keys [`appearance.workspace_transition`](config-options.md#appearance.workspace_transition), `workspace_transition_ms` and `zoom_animation` still read: TUIOS folds them into [`motion.workspace`](config-options.md#motion.workspace), [`motion.workspace_ms`](config-options.md#motion.workspace_ms) and [`motion.zoom`](config-options.md#motion.zoom) on load. A `[motion]` key that is set wins.

#### border_style

Values: `rounded` (default), `normal`, `thick`, `double`, `block`, `outer-half-block`, `inner-half-block`, `ascii`, `hidden`, `glyphs`.

`glyphs` uses the border of the active [glyph set](#glyph-sets). `hidden` also hides the window buttons and the scrollbar, because both are drawn on the border.

#### Dialog and panel frames

The small dialogs, such as rename, confirm and new space, draw their frame in `border_style`. The large panels, such as the command palette, the switchers, the Agents view and the settings page, have no frame. Set `frame_panels = true` to give them the same frame.

```toml
[appearance]
border_style = "double"
frame_panels = true
```

- `rounded`, `normal`, `thick`, `double` and `ascii` draw their own corners and lines.
- `hidden` draws no frame. A dialog keeps its size, and its title and key hints stay in place. A frame that is the only edge of a region is drawn as `rounded`. An example is a panel at 16 colours.
- `block`, `outer-half-block` and `inner-half-block` draw the `rounded` frame.
- `glyphs` draws the border of the active glyph set.
- At 16 colours a panel always has a frame, because its background is the background of your terminal.

[`appearance.pane_overlays`](config-options.md#appearance.pane_overlays) draws dialogs and panels like pane windows. Every panel gets a frame in the focused pane's border colour. The frame and its content sit on the background of an empty pane cell. The selected row and input fields are steps of that background. The option is off by default. Under `hidden`, at 16 colours, or with no theme on a terminal that reports no background, a dialog keeps its shade.

[`appearance.overlay_border_color`](config-options.md#appearance.overlay_border_color) sets a colour for every dialog and panel frame: `#89b4fa`, a slot name such as `bright_cyan`, or a role such as `accent`.

```toml
[appearance]
pane_overlays = true
overlay_border_color = "accent"
```

#### max_fps

`max_fps` is the highest frame rate TUIOS draws at. Your terminal and your monitor can show fewer frames. A value above what they can show does not make TUIOS smoother.

```toml
[appearance]
max_fps = 144       # a number from 10 to 240
# max_fps = "auto"  # the refresh rate of your display
# max_fps = 0       # 60, the default
```

| Value | Frame rate |
|-------|------------|
| `0` | 60. This is the default |
| `10` to `240` | That number. A value outside the range moves to the nearest end |
| `"auto"` | The refresh rate of your display, from 10 to 240. If TUIOS cannot find the rate, it uses 60 |

The settings page offers Auto, 30, 60, 90, 120, 144, 165 and 240. With Auto selected, the row shows the rate in use, for example `Auto (144)`. A change applies at once, with no restart.

- **What you see.** TUIOS sends a frame to your terminal, and the terminal draws it on its own schedule. kitty and Ghostty draw at most once for each refresh of the monitor, so a 60 Hz monitor shows at most 60 frames a second. Over SSH, the network and the remote terminal set the limit too.
- **What it costs.** TUIOS draws only when something on the screen changes. The terminal library under it still wakes at `max_fps` while TUIOS is idle. An idle TUIOS at 240 wakes about three times as often as at 60.
- **How auto finds the rate.** TUIOS looks once in the background when it starts, and again when the config file reloads. Until the answer arrives, it draws at 60. On Wayland it asks `hyprctl` on Hyprland, `niri` on niri and `wlr-randr` on other wlroots compositors. On X11, or on another compositor, it asks `xrandr --current`. On macOS it asks `system_profiler SPDisplaysDataType`. On Windows it does not look, and uses 60.
- **Several displays.** A terminal window cannot tell which display it is on, so auto uses the highest rate among them.
- **Remote clients.** Over SSH, in `tuios-web` and in the SSH server, auto uses 60, because the displays on the machine are not the ones you see.

### Windows and titles

These options are in the [option reference](config-options.md): [`appearance.window_title_position`](config-options.md#appearance.window_title_position), [`appearance.window_title_format`](config-options.md#appearance.window_title_format), [`appearance.host_title`](config-options.md#appearance.host_title), [`appearance.window_button_style`](config-options.md#appearance.window_button_style), [`appearance.window_button_position`](config-options.md#appearance.window_button_position), [`appearance.hide_window_buttons`](config-options.md#appearance.hide_window_buttons), [`appearance.window_button_zoom`](config-options.md#appearance.window_button_zoom), [`appearance.hide_scrollbar`](config-options.md#appearance.hide_scrollbar), [`appearance.confirm_quit`](config-options.md#appearance.confirm_quit), [`appearance.preferred_shell`](config-options.md#appearance.preferred_shell), [`appearance.new_window_inherit_cwd`](config-options.md#appearance.new_window_inherit_cwd), [`appearance.new_window_follow_ssh`](config-options.md#appearance.new_window_follow_ssh), [`appearance.whichkey_enabled`](config-options.md#appearance.whichkey_enabled), [`appearance.whichkey_position`](config-options.md#appearance.whichkey_position), [`appearance.prefix_repeat_time`](config-options.md#appearance.prefix_repeat_time).

#### window_title_format

A template for every title. It expands three placeholders:

- `{title}`: the custom name, or the title the program set
- `{index}`: the window's 1-based position in its workspace
- `{cwd}`: the window's working directory

```toml
[appearance]
window_title_format = "{index}: {title} ({cwd})"
```

Any other `{...}` produces a validation warning and is drawn as written.

#### host_title

`host_title` sets the title of the terminal that tuios runs in, such as the title bar of Ghostty. It is the same as `set-titles` in tmux. The title follows the focused pane, the workspace and the session, and it changes when the pane's own title changes.

| Placeholder | What it holds |
| --- | --- |
| `{title}` | The focused pane's name, as the title on its border shows it |
| `{index}` | The pane's position in its workspace, from 1 |
| `{cwd}` | The pane's working directory |
| `{session}` | The session's name as the rail shows it. Without a daemon, `tuios` |
| `{workspace}` | The workspace's name, or its number. With a scratch group open, the workspace that it shows over |

```toml
[appearance]
host_title = "{title} · {session}"
```

- With no focused pane on the workspace in view, the title is the session's name.
- The title is also the session's name when the template uses `{title}` and the pane has no name, as for a new shell, and when the template comes out empty.
- A title that a tool sets with herdr's `terminal title set` wins while it is set.
- Each client sets the title of its own terminal.
- When you empty the option while a client is attached, tuios clears the title that it set.

The option is on the settings page, in the Appearance section. `tuios set-config` applies it at once.

### Mouse and input

These options are in the [option reference](config-options.md): [`appearance.click_to_type`](config-options.md#appearance.click_to_type), [`appearance.auto_enter_terminal_on_focus`](config-options.md#appearance.auto_enter_terminal_on_focus), [`appearance.focus_follows_mouse`](config-options.md#appearance.focus_follows_mouse), [`appearance.alt_drag`](config-options.md#appearance.alt_drag), [`appearance.right_click_opens_menu`](config-options.md#appearance.right_click_opens_menu), [`appearance.copy_on_select`](config-options.md#appearance.copy_on_select), [`appearance.word_characters`](config-options.md#appearance.word_characters), [`appearance.links`](config-options.md#appearance.links), [`appearance.link_click`](config-options.md#appearance.link_click), [`appearance.link_opener`](config-options.md#appearance.link_opener), [`appearance.nvim_navigation`](config-options.md#appearance.nvim_navigation), [`appearance.link_hover`](config-options.md#appearance.link_hover), [`appearance.kitty_placeholders`](config-options.md#appearance.kitty_placeholders), [`appearance.image_symbols`](config-options.md#appearance.image_symbols), [`appearance.paste_preview`](config-options.md#appearance.paste_preview).

#### Links

<kbd>Ctrl</kbd>+click on a link opens it. <kbd>Ctrl</kbd>+drag still moves the pane: a link opens only when the pointer does not move. Only `http`, `https`, `mailto`, `ftp` and `ftps` links go to the opener. TUIOS copies other links. Under `ssh`, on a remote client, or on a machine with no desktop, TUIOS copies the URL, and your terminal can still open the link from the OSC 8 that TUIOS writes. If the opener fails, the dock says so and TUIOS copies the URL. See [Links](https://tuios.dev/docs/links) for the opener, the safety rules and a table of terminals.

### Scrolling

These options are in the [option reference](config-options.md): [`appearance.scrollback_lines`](config-options.md#appearance.scrollback_lines), [`appearance.scroll_lines`](config-options.md#appearance.scroll_lines).

### Tiling and zoom

These options are in the [option reference](config-options.md): [`appearance.tiling_scheme`](config-options.md#appearance.tiling_scheme), [`appearance.master_ratio`](config-options.md#appearance.master_ratio), [`appearance.master_position`](config-options.md#appearance.master_position), [`appearance.master_count`](config-options.md#appearance.master_count), [`appearance.master_grid`](config-options.md#appearance.master_grid), [`appearance.scroll_column_width`](config-options.md#appearance.scroll_column_width), [`appearance.scroll_column_max`](config-options.md#appearance.scroll_column_max), [`appearance.niri_scroll_cells`](config-options.md#appearance.niri_scroll_cells), [`appearance.niri_reverse_scroll`](config-options.md#appearance.niri_reverse_scroll), [`appearance.niri_click_reveals`](config-options.md#appearance.niri_click_reveals), [`appearance.niri_hover_reveals`](config-options.md#appearance.niri_hover_reveals), [`appearance.zoom_size`](config-options.md#appearance.zoom_size), [`appearance.zoom_max_width`](config-options.md#appearance.zoom_max_width), [`appearance.zoom_borderless`](config-options.md#appearance.zoom_borderless), [`appearance.zoom_animation`](config-options.md#appearance.zoom_animation), [`appearance.zoom_follows_focus`](config-options.md#appearance.zoom_follows_focus), [`appearance.drop_preview`](config-options.md#appearance.drop_preview), [`appearance.drop_preview_delay`](config-options.md#appearance.drop_preview_delay).

The layout a new session starts in is [`startup.layout`](config-options.md#startup.layout). See [The startup table](#the-startup-table). Each workspace starts with the master-stack options, and the layout keys change the workspace on screen. See [Master-stack](LAYOUT_MODES.md#master-stack-layout).

### Dock

These options are in the [option reference](config-options.md): [`appearance.dockbar_position`](config-options.md#appearance.dockbar_position), [`appearance.dock_workspace_tabs`](config-options.md#appearance.dock_workspace_tabs), [`appearance.dock_workspace_tab_format`](config-options.md#appearance.dock_workspace_tab_format), [`appearance.dock_workspace_label_max`](config-options.md#appearance.dock_workspace_label_max), [`appearance.dock_workspace_tooltip`](config-options.md#appearance.dock_workspace_tooltip), [`appearance.dock_pill_caps`](config-options.md#appearance.dock_pill_caps), [`appearance.dock_compact`](config-options.md#appearance.dock_compact), [`appearance.dock_mode_icon_window`](config-options.md#appearance.dock_mode_icon_window), [`appearance.dock_mode_icon_terminal`](config-options.md#appearance.dock_mode_icon_terminal), [`appearance.dock_mode_icon_tiling`](config-options.md#appearance.dock_mode_icon_tiling), [`appearance.dock_mode_split_letter`](config-options.md#appearance.dock_mode_split_letter), [`appearance.show_clock`](config-options.md#appearance.show_clock), [`appearance.clock_format`](config-options.md#appearance.clock_format), [`appearance.show_cpu`](config-options.md#appearance.show_cpu), [`appearance.show_ram`](config-options.md#appearance.show_ram), [`appearance.hide_clock`](config-options.md#appearance.hide_clock).

`dock_workspace_label_max` stops one long workspace name from pushing the other tabs off the dock. Set it to `0` to show the whole name. The workspace strip then scrolls. A dock with no room for one full tab still shows the current tab, cut to fit.

`dock_compact` works with the dock at the top and at the bottom. It has no effect when `dockbar_position` is `hidden`. The change applies at once. A compact dock does not show the rule that tells how long a dock message stays. When clients with different docks share a session, the panes keep the largest dock height. A client with a compact dock shows a blank row in that case.

The mode pill icons can be any text up to 8 cells wide. A wide character, such as `終`, counts as two cells. The dock does not accept control characters. For an icon that it does not accept, the dock shows the default icon and the config check shows a warning. The pill adds one space on each side of the icon. If an icon is empty and the mode has no other text, the dock shows no pill in that mode. While tiling is on, the pill also shows the next split direction, `V` or `H`. Set `dock_mode_split_letter = false` to show the tiling icon alone. With the default icons, the pill then has the same width as the pill of the leader key, so the workspace tabs do not move when you press the leader.

```toml
[appearance]
dock_mode_icon_window = "WM"
dock_mode_icon_terminal = ">_"
dock_mode_icon_tiling = ""
```

To remove the whole mode pill, remove `"mode"` from the `left` list in the `[dock]` table. You can also set the icons on the settings page, in the Dock section, or with `tuios set-config appearance.dock_mode_icon_window WM`. The change applies at once.

### Rail options

Two rail options sit directly in `[appearance]`:

These options are in the [option reference](config-options.md): [`appearance.global_session`](config-options.md#appearance.global_session), [`appearance.git_dirty`](config-options.md#appearance.git_dirty).

The rest are in [`[appearance.sidebar]`](#the-session-rail).

### The scrollbar

`[appearance.scrollbar]`:

These options are in the [option reference](config-options.md): [`appearance.scrollbar.style`](config-options.md#appearance.scrollbar.style), [`appearance.scrollbar.thumb`](config-options.md#appearance.scrollbar.thumb), [`appearance.scrollbar.track`](config-options.md#appearance.scrollbar.track), [`appearance.scrollbar.tint`](config-options.md#appearance.scrollbar.tint).

### Selection colours

`[appearance.selection]` sets the colours TUIOS draws over pane content to mark text.

These options are in the [option reference](config-options.md): [`appearance.selection.bg`](config-options.md#appearance.selection.bg), [`appearance.selection.fg`](config-options.md#appearance.selection.fg), [`appearance.selection.bold`](config-options.md#appearance.selection.bold), [`appearance.selection.search_bg`](config-options.md#appearance.selection.search_bg), [`appearance.selection.match_bg`](config-options.md#appearance.selection.match_bg), [`appearance.selection.cursor_bg`](config-options.md#appearance.selection.cursor_bg), [`appearance.selection.flash`](config-options.md#appearance.selection.flash), [`appearance.selection.flash_color`](config-options.md#appearance.selection.flash_color), [`appearance.selection.flash_ms`](config-options.md#appearance.selection.flash_ms), [`appearance.selection.flash_style`](config-options.md#appearance.selection.flash_style), [`appearance.selection.copy_entry`](config-options.md#appearance.selection.copy_entry), [`appearance.selection.multi_format`](config-options.md#appearance.selection.multi_format), [`appearance.selection.osc52_write`](config-options.md#appearance.selection.osc52_write), [`appearance.selection.copy_command`](config-options.md#appearance.selection.copy_command).

An empty foreground keeps the colour the program wrote. An empty background falls back to the default.

### Clipboard writes from a pane

A program in a pane sets the clipboard with OSC 52. An editor uses OSC 52 to yank over ssh. Any output can also carry OSC 52, for example a file that a program prints. [`appearance.selection.osc52_write`](config-options.md#appearance.selection.osc52_write) controls what happens.

| Value | What happens |
|---|---|
| `off` | The host clipboard does not change. |
| `ask` | The dock shows a message. Click the message to copy the text. |
| `focused` | The focused pane copies. A write from another pane shows a message in the dock. This is the default. |
| `on` | Every pane copies. This is the behaviour of v0.8.0. |

```toml
[appearance.selection]
osc52_write = "focused"
```

- When the focused pane copies text with a line break or a control character, the dock shows one line for that pane. A one-line copy shows nothing.
- Each pane keeps one clipboard ask. A click copies only the text that the dock shows. If the text changes first, click the new message.
- A pane that runs without the daemon keeps its own copy of the text. The program reads it back with an OSC 52 query. A pane under the daemon keeps no copy, so an OSC 52 query there always gets an empty answer.
- TUIOS drops a clipboard reply that answers no read request of TUIOS.

Pastes, notifications, OSC 66 text and link addresses lose their control characters. A paste into a pane keeps its tabs and line breaks. Text that holds `ESC[201~` cannot end a bracketed paste early.

### The session rail

`[appearance.sidebar]` configures the rail at the side of the screen.

These options are in the [option reference](config-options.md): [`appearance.sidebar.enabled`](config-options.md#appearance.sidebar.enabled), [`appearance.sidebar.per_session`](config-options.md#appearance.sidebar.per_session), [`appearance.sidebar.position`](config-options.md#appearance.sidebar.position), [`appearance.sidebar.width`](config-options.md#appearance.sidebar.width), [`appearance.sidebar.background`](config-options.md#appearance.sidebar.background), [`appearance.sidebar.cursor_background`](config-options.md#appearance.sidebar.cursor_background), [`appearance.sidebar.agent_rest_fold`](config-options.md#appearance.sidebar.agent_rest_fold), [`appearance.sidebar.subagent_rows`](config-options.md#appearance.sidebar.subagent_rows), [`appearance.sidebar.subagent_collapse`](config-options.md#appearance.sidebar.subagent_collapse), [`appearance.sidebar.subagent_fields`](config-options.md#appearance.sidebar.subagent_fields), [`appearance.sidebar.subagent_max_rows`](config-options.md#appearance.sidebar.subagent_max_rows), [`appearance.sidebar.subagent_hide_finished`](config-options.md#appearance.sidebar.subagent_hide_finished), [`appearance.sidebar.header_case`](config-options.md#appearance.sidebar.header_case), [`appearance.sidebar.sections`](config-options.md#appearance.sidebar.sections), [`appearance.sidebar.show_glyphs`](config-options.md#appearance.sidebar.show_glyphs), [`appearance.sidebar.show_counts`](config-options.md#appearance.sidebar.show_counts), [`appearance.sidebar.show_numbers`](config-options.md#appearance.sidebar.show_numbers), [`appearance.sidebar.marquee`](config-options.md#appearance.sidebar.marquee), [`appearance.sidebar.tooltips`](config-options.md#appearance.sidebar.tooltips), [`appearance.sidebar.file_icons`](config-options.md#appearance.sidebar.file_icons), [`appearance.sidebar.file_icon_colors`](config-options.md#appearance.sidebar.file_icon_colors), [`appearance.sidebar.folder_click`](config-options.md#appearance.sidebar.folder_click), [`appearance.sidebar.file_actions`](config-options.md#appearance.sidebar.file_actions), [`appearance.sidebar.file_delete`](config-options.md#appearance.sidebar.file_delete), [`appearance.sidebar.editor`](config-options.md#appearance.sidebar.editor).

Deprecated keys still load: `show_windows`, `show_agents` and `workspaces` in this table, and the flat `sidebar_*` keys in `[appearance]`. They are folded into the current keys when the file is read.

#### sections

One string sets which sections the rail draws, their order from the top, and the share of the rail each may take.

```toml
[appearance.sidebar]
sections = "sessions:25,terminals,files:25,agents:34"
```

- Separate sections with commas. Add `:N` for a share in percent.
- The section names are `sessions`, `terminals`, `files`, `agents`, `git` and `custom`. Leave a name out to hide that section. See [The custom section](#the-custom-section).
- A share is a ceiling, not a reservation. A section only takes the lines its rows fill. A section with no share takes what the others leave.
- `spacer` draws nothing and takes lines. It is the only name you can repeat. A spacer with a share keeps that share. A spacer with no share takes the lines nothing else wants, which pushes what follows to the bottom.

```toml
[appearance.sidebar]
# Sessions at the top, files at the bottom
sections = "sessions:30,terminals,spacer,files:40"
```

An unknown name, a bad percent or a repeated section is dropped and reported as a config warning.

The settings page has an editor for this: the **Sections** row under Sidebar. Arrows select, <kbd>Shift</kbd>+arrows move, <kbd>Enter</kbd> adds or removes a section, <kbd>←</kbd>/<kbd>→</kbd> change the share, <kbd>r</kbd> restores the default and <kbd>u</kbd> undoes your edits.

#### The files section

The files section lists the focused pane's directory. It is read again when the shell changes directory, when the focus moves and when a client attaches. It is also read again when a file in the listed folder is added, removed or renamed, by the pane or by another program. tuios asks the system to report these changes (inotify on Linux, kqueue on macOS and the BSDs, ReadDirectoryChangesW on Windows). Nothing polls, and a change to the contents of a file does not cause a read. For a pane on another machine, the list changes only when the shell changes directory.

`folder_click` decides what a click on a folder does: `navigate` opens it in the listing, `cd` sends the focused pane there, `both` does both. A `cd` is only typed into a pane that is at a shell prompt.

The file keys are in `[keybindings.sidebar_files]` and work while the rail has the keyboard and the cursor is on a file row. See [Keybindings](KEYBINDINGS.md#editing-sidebar-files). Each action opens a centred dialog. The delete dialog opens on Cancel, so <kbd>Enter</kbd> on an untouched dialog deletes nothing.

Delete uses the freedesktop.org trash in your home directory, so your desktop's trash shows the file and can restore it. <kbd>D</kbd> deletes permanently. Set `file_delete = "permanent"` to make <kbd>d</kbd> permanent too. The per-volume trash is not implemented.

<kbd>Y</kbd> copies the absolute path of the selected file or folder. A folder's context menu has a **Copy path** row that does the same.

On Windows, a shell under MSYS2 or Cygwin reports its folder as `/c/work`. TUIOS reads that as `C:\work`, by the rules of `cygpath`, so the files section lists the right folder.

[`appearance.sidebar.file_drag`](config-options.md#appearance.sidebar.file_drag) lets you drag a row of the files section onto a pane. It is on by default. See [Drag a file into a pane](https://tuios.dev/docs/session-rail#drag-a-file-into-a-pane).

#### The file editor

<kbd>Shift</kbd>+<kbd>Enter</kbd> on a text file opens it in a terminal editor. `editor` sets the command. The settings page shows it as **File editor** under Sidebar.

```toml
[appearance.sidebar]
editor = "micro"
```

- Other terminal editors and arguments work too, such as `nano` or `vim -f`.
- Quote a path or an argument that contains spaces. TUIOS splits the command into arguments and runs no shell.
- TUIOS adds the full path of the file at the end.
- When `editor` is empty, TUIOS uses `$EDITOR`, then `$VISUAL`, then `vi`.
- File links and hints open files with the same command.
- TUIOS reads the first 512 bytes of a file to check that it is text. It refuses binary files and special files.

#### The custom section

The `custom` section shows the output of a command you write. Place `custom` in `sections` and describe it in `[appearance.sidebar.custom]`:

```toml
[appearance.sidebar]
sections = "sessions:25,terminals,custom:35,agents:30"

[appearance.sidebar.custom]
title   = "Brief"
command = "agent-brief render"
refresh = "event:window-focused,agent-state"
```

These options are in the [option reference](config-options.md): [`appearance.sidebar.custom.title`](config-options.md#appearance.sidebar.custom.title), [`appearance.sidebar.custom.command`](config-options.md#appearance.sidebar.custom.command), [`appearance.sidebar.custom.refresh`](config-options.md#appearance.sidebar.custom.refresh).

The command follows the rules of a [dock component](#custom-components). It runs in the client, under a three second timeout, with bounded output. SGR colour and OSC 8 links stay, and every other control sequence is removed. A command that fails, times out or prints nothing leaves the title over an empty section. Five failures in a row stop it until the config reloads or you run `tuios refresh-dock rail/custom`. `tuios list-dock-components` lists it as `rail/custom`. Rows are cut to the rail's width and to the section's lines, with `… +N` for the rows below.

A row can link to a page or a file. Print the link as an OSC 8 hyperlink, as for a link in a pane:

```sh
printf '\033]8;;https://example.com/pull/12\033\\#12 fix the parser\033]8;;\033\\\n'
```

A click on the link opens it. A click on the rest of the row opens the first link of the row. With the rail cursor on the row, <kbd>Enter</kbd> opens the first link. The link opens the same way as a link in a pane, through `appearance.link_opener`. A row with no link opens nothing.

Each run gets the client's environment and these variables: `TUIOS_SESSION`, `TUIOS_SOCKET`, `TUIOS_RAIL_SECTION` (`custom`), `TUIOS_RAIL_WIDTH`, `TUIOS_RAIL_HEIGHT`, `TUIOS_ACTIVE_PANE_ID` and `TUIOS_ACTIVE_PANE_CWD`. An event that arrives during a run causes one more run after it, so a quick focus change never leaves the old pane's rows on screen. The command does not run while the rail is hidden or folded.

`sections` works with `tuios set-config`. `[appearance.sidebar.custom]` is read from the file only, so a program in a pane cannot change the command.

#### The pane section

The rail can show a live terminal under its own heading. Put `pane` in `sections`, and describe it in `[appearance.sidebar.pane]`. tuios reads this table from the file only.

```toml
[appearance.sidebar]
sections = "sessions:25,terminals,files:25,pane,agents:34"

[appearance.sidebar.pane]
title   = "git"
command = "watch -t git status -sb"
height  = 12
```

These options are in the [option reference](config-options.md): [`appearance.sidebar.pane.title`](config-options.md#appearance.sidebar.pane.title), [`appearance.sidebar.pane.command`](config-options.md#appearance.sidebar.pane.command), [`appearance.sidebar.pane.height`](config-options.md#appearance.sidebar.pane.height).

See [The pane section](https://tuios.dev/docs/session-rail#the-pane-section).

#### Group the main checkout

`appearance.sidebar.group_main = true` puts a repository's main checkout under the repository row with its worktrees. The session is labelled by its branch. <kbd>Enter</kbd> or a click on the repository row then opens the main checkout's session. **Fold group** on the row's right-click menu folds the group. The default is `false`.

#### Session peek

When the pointer or the rail cursor is on a session row, the terminals section shows the panes of that session. `appearance.sidebar.peek = false` turns this off. The terminals section then shows the panes of the attached session.

#### Two edges

The rail can show on both edges of the screen at the same time. `[appearance.sidebar]` still configures the original rail. Add `[appearance.sidebar.left]` or `[appearance.sidebar.right]` to configure one edge:

```toml
[appearance.sidebar]
position = "right"
sections = "sessions,terminals,custom"

[appearance.sidebar.custom]
command = "right-summary"

[appearance.sidebar.left]
enabled = true
width = 28
sections = "sessions,files,custom"

[appearance.sidebar.left.custom]
command = "left-summary"
```

These options are in the [option reference](config-options.md): [`appearance.sidebar.left`](config-options.md#appearance.sidebar.left), [`appearance.sidebar.right`](config-options.md#appearance.sidebar.right).

These options are in the [option reference](config-options.md): [`appearance.sidebar.enabled`](config-options.md#appearance.sidebar.enabled), [`appearance.sidebar.width`](config-options.md#appearance.sidebar.width), [`appearance.sidebar.sections`](config-options.md#appearance.sidebar.sections).

- On the edge of `position`, the edge table overrides only the keys it sets. The other keys come from `[appearance.sidebar]`.
- On the other edge, a key you do not set takes its default. An unset `sections` takes the default section order.
- With `position = "hidden"`, an edge table with `enabled = true` still draws. When both edges are on, the left one is the original rail.
- Each rail has its own scroll, keyboard focus and custom command. Both read the same sessions, files and git state.
- On a narrow screen, the second rail closes before the original rail.

Each edge table also takes the five `subagent_*` keys. A key that the edge table does not set uses the value from `[appearance.sidebar]`. See [Subagent rows](https://tuios.dev/docs/session-rail#subagent-rows).

The edge tables are read from the file only. `tuios set-config` cannot change them, because a custom command runs outside the panes. <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>H</kbd> shows or hides the second rail. See [Session Rail](https://tuios.dev/docs/session-rail#both-edges).

#### Agent rows

`[appearance.sidebar.agent_row]` chooses which tokens an agent row shows and styles each one, with optional rules that match on the token's value:

```toml
[appearance.sidebar.agent_row]
tokens = ["session", "need", "harness", "name", "elapsed", "context", "subagents", "pr", "meta", "now", "message"]

[[appearance.sidebar.agent_row.elapsed.rule]]
gt = 30          # minutes in the current state
fg = "warning"
```

The `tokens` above are the shipped list. The other tokens are `state`, `host` and `prompt`. A `tokens` list you wrote keeps its own order and gets no new tokens, so add `subagents` or `pr` to it to see them. `need` says what a row wants from you, such as `approval` or `question`. `context` shows `ctx 84%` once the agent's context is 80% full. `now` is what a working agent is doing, such as `Bash: go test ./...`. `subagents` shows how many subagents a Claude Code agent has at work, such as `3 subagents`, on any row while any run. When the subagents have rows of their own, it reads `3 of 4 run`. `pr` shows the pull request of the session's worktree branch and its checks, such as `PR #12 open pass`. `meta` draws the other keys the pane reported with `tuios set-agent-meta`, and `$` followed by a key, such as `$model` or `$cost`, places one key. See [Session Rail](https://tuios.dev/docs/session-rail#agent-rows).

Each rule has one test: `equals`, `contains`, `starts_with`, `gt` or `lt`. The first matching rule wins. A mistake in this table is a warning, not an error.

## Glyph sets

A theme sets the chrome's colours. A glyph set sets its shapes: border corners, window buttons, rules, separators and rail marks.

```toml
[appearance]
glyphs = "heavy"
```

```bash
tuios list-glyphs            # every set, and every role name
tuios list-glyphs heavy      # what this set draws, role by role
tuios set-config appearance.glyphs heavy
```

| Id | What it is |
|----|------------|
| `default` | Rounded frame, Nerd Font powerline caps, `✕` and `□` buttons |
| `unicode` | Box drawing and geometric shapes, with no Nerd Font glyphs |
| `heavy` | One stroke weight heavier throughout |
| `ascii` | 7-bit ASCII only |

A set's border is used only when `border_style = "glyphs"`. This keeps a set from silently overriding a border style you chose. A set that defines only some border runes gets the rounded border for the rest.

### Writing a set

A set can name the marks of the agent states: `state_working`, `state_needs_input`, `state_idle`, `state_done`, `state_errored`, `state_unknown` and `state_waiting`. A mark may be one cell or two, so `state_needs_input = "✋"` is valid. A state the set leaves alone keeps the shipped mark. Of the built-in sets, only `ascii` names these marks.

Save `<id>.json` in the glyphs directory. `tuios list-glyphs` prints its path.

```json
{
  "display_name": "Mine",
  "inherits": "heavy",
  "bullet": "◦",
  "focus": "▐",
  "border": {
    "top_left": "╔",
    "top_right": "╗",
    "bottom_left": "╚",
    "bottom_right": "╝"
  }
}
```

Every field is optional. The id defaults to the file name. A role you do not set comes from the inherited set, and finally from `default`. Inheritance goes up to eight levels deep.

The roles:

- **Window buttons:** `close`, `maximize`, `minimize`, `dot`, `pill_left`, `pill_right`
- **Rules and separators:** `rule`, `separator`, `arrow_left`, `arrow_right`, `dash_rule`
- **Rail marks:** `focus`, `attention`, `bullet`, `add`, `collapse`, `expand`, `fold_open`, `fold_shut`, `tree_branch`, `tree_last`
- **Files section:** `folder`, `parent`, `file`
- **Scrollbar:** `scrollbar_thumb`, `scrollbar_track`
- **Text:** `ellipsis`, `sigil`
- **Border:** `border.top`, `border.bottom`, `border.left`, `border.right`, the four corners, and the junctions `middle`, `middle_top`, `middle_bottom`, `middle_left`, `middle_right`

Most roles must be exactly one cell wide, because the window buttons' click areas are fixed offsets. A glyph of the wrong width falls back to the default. `separator`, `ellipsis`, `collapse` and `expand` may be any width. To see what was dropped:

```bash
tuios list-glyphs mine --json | jq -r '.problems[]?'
```

The directory is read again on every lookup, so a new file can be selected at once. A file that does not parse is skipped and listed under `problems`.

`--ascii-only` replaces non-ASCII glyphs role by role, so a set keeps every role it already spelled in ASCII. A glyph set never changes colours, and it does not change the dock's icons. A session name, a workspace name or a pane title can hold Nerd Font icons. TUIOS shows each icon in one cell, on the rail, in the dock and in the rest of the chrome. On the Linux console and under `--ascii-only`, TUIOS removes the icons from what it draws, because they show as empty boxes there. The name itself does not change.

The settings page has a **Glyph set** row under **Theme**. Its picker applies each set as you move through the list.

## Themes

345 themes ship with TUIOS.

```bash
tuios --list-themes                   # every theme id
tuios --preview-theme dracula         # the 16 ANSI colours
tuios list-themes --filter gruvbox    # from a running daemon, with --json
tuios list-themes catppuccin_mocha    # colours and their contrast
```

```toml
[appearance]
theme = "dracula"
```

With no theme, TUIOS uses your terminal's own colours. A theme only changes ANSI colours 0 to 15. 256-colour and truecolor output is drawn as the program wrote it.

To pick one in the app, press <kbd>Enter</kbd> on the **Theme** row of the settings page, or choose **Theme Picker** in the command palette. The picker previews as you move and <kbd>Esc</kbd> restores the theme you had. It has no key binding.

### Custom themes

Save one theme per JSON file in `~/.config/tuios/themes/`. Subdirectories are ignored.

```json
{
  "id": "my-theme",
  "display_name": "My Theme",
  "fg": "#e0def4",
  "bg": "#191724",
  "black": "#26233a",
  "red": "#eb6f92",
  "green": "#31748f",
  "yellow": "#f6c177",
  "blue": "#9ccfd8",
  "purple": "#c4a7e7",
  "cyan": "#ebbcba",
  "white": "#e0def4"
}
```

- `id` defaults to the file name, lowercased. `display_name` defaults to `id`.
- The colour names are `black`, `red`, `green`, `yellow`, `blue`, `purple`, `cyan`, `white`, each with a `bright_` variant, plus `fg`, `bg` and `cursor`. Note `purple`, not `magenta`.
- A missing colour gets the xterm default. A missing `bright_*` colour copies its normal colour, so set them if you want bright text to look different.
- An optional `chrome` object sets the UI colours directly (`accent`, `accent_bright`, `success`, `warning`, `error`, `info`, `surface`) without changing the pane palette.
- A file that does not parse is skipped and logged.

`tuios import-theme <file>` converts a kitty, Ghostty, Alacritty or WezTerm scheme into a file here.

Selecting an id that is not registered yet, with `set-config` or the settings page, reads the directory again first. So does `tuios list-themes`. The picker builds its list when it opens.

## Notifications: the `[notifications]` table

`[notifications]` sets how long messages stay on screen, how they look and which ones the dock shows. For a time, `0` uses the built-in default: 2 seconds for info and success messages, 8 for warnings and 15 for errors. A time can be up to 3600 seconds.

These options are in the [option reference](config-options.md): [`notifications.duration`](config-options.md#notifications.duration), [`notifications.warning_duration`](config-options.md#notifications.warning_duration), [`notifications.error_duration`](config-options.md#notifications.error_duration), [`notifications.error_sticky`](config-options.md#notifications.error_sticky), [`notifications.bell`](config-options.md#notifications.bell), [`notifications.style`](config-options.md#notifications.style), [`notifications.level`](config-options.md#notifications.level), [`notifications.log_hidden`](config-options.md#notifications.log_hidden), [`notifications.machine_offer`](config-options.md#notifications.machine_offer).

A message that `level` hides still goes to the log viewer (<kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>D</kbd> <kbd>l</kbd>). Warnings and errors always go to the log. A message from a pane, an agent or mail does not use `level`. `[notifications.agent]` and `[notifications.mail]` control those messages.

### Agent alerts

`[notifications.agent]` controls what happens when an agent in a pane changes state.

These options are in the [option reference](config-options.md): [`notifications.agent.enabled`](config-options.md#notifications.agent.enabled), [`notifications.agent.notify`](config-options.md#notifications.agent.notify), [`notifications.agent.dock`](config-options.md#notifications.agent.dock), [`notifications.agent.sound`](config-options.md#notifications.agent.sound), [`notifications.agent.sound_mode`](config-options.md#notifications.agent.sound_mode), [`notifications.agent.sound_cooldown_seconds`](config-options.md#notifications.agent.sound_cooldown_seconds), [`notifications.agent.command`](config-options.md#notifications.agent.command), [`notifications.agent.settle_seconds`](config-options.md#notifications.agent.settle_seconds), [`notifications.agent.suppress_focused`](config-options.md#notifications.agent.suppress_focused), [`notifications.agent.quiet_hours`](config-options.md#notifications.agent.quiet_hours).

`[notifications.agent.states]` turns alerts on per state: `needs_input`, `errored` and `done` default to `true`. `idle` and `working` default to `false`.

To hear an alert, set `sound = true`. `sound_mode` then decides how:

- `audio` plays a short cue through the audio player of the machine that runs TUIOS: `paplay`, `pw-play` or `aplay` on Linux, `afplay` on macOS, PowerShell on Windows. If the machine has no player, the alert is silent and TUIOS shows no message.
- `bell` sends a BEL character to your terminal. Your terminal decides what a bell does, and some terminals make no sound. In kitty, check that `enable_audio_bell` is `yes`. Other terminals have a similar bell setting.
- `both` does the two.

Over SSH or in the browser, `audio` plays on the server, not where you sit. Use `bell` there. See [Agents](https://tuios.dev/docs/agents#hear-an-alert).

`[notifications.agent.sounds]` replaces the two built-in cues with your own files: `needs_input` (also used for `errored`) and `done` (also used for `idle`). A path that does not exist falls back to the built-in cue.

The `after-agent-state` [hook](HOOKS.md) follows the same rules, including the settle wait.

### Mail alerts

`[notifications.mail]` controls the alerts for mail to you and for session notices. A key that you leave out uses the same key from `[notifications.agent]`.

These options are in the [option reference](config-options.md): [`notifications.agent.enabled`](config-options.md#notifications.agent.enabled), [`notifications.agent.notify`](config-options.md#notifications.agent.notify), [`notifications.agent.dock`](config-options.md#notifications.agent.dock), [`notifications.agent.sound`](config-options.md#notifications.agent.sound), [`notifications.mail.between_agents`](config-options.md#notifications.mail.between_agents).

TUIOS warns when `between_agents` is on and mail alerts are off.

The sound mode, the cooldown, the cue files and the quiet hours always come from `[notifications.agent]`. `tuios set-config notifications.mail.sound ""` clears a key, and `get-config` then prints `(follows notifications.agent.sound)`. See [Agent Messaging](https://tuios.dev/docs/agent-messaging#mail-alerts).

## Polling and display options

These options are in the [option reference](config-options.md): [`appearance.idle_fps`](config-options.md#appearance.idle_fps), [`appearance.sidebar.git_refresh_ms`](config-options.md#appearance.sidebar.git_refresh_ms), [`appearance.sidebar.marquee_pause_ms`](config-options.md#appearance.sidebar.marquee_pause_ms), [`appearance.sidebar.marquee_step_ms`](config-options.md#appearance.sidebar.marquee_step_ms), [`appearance.multi_copy_dim`](config-options.md#appearance.multi_copy_dim), [`dock.meter_interval_ms`](config-options.md#dock.meter_interval_ms), [`debug.show_key_count`](config-options.md#debug.show_key_count).

```toml
[appearance]
idle_fps = 4
multi_copy_dim = 60

[appearance.sidebar]
git_refresh_ms = 5000
marquee_pause_ms = 400
marquee_step_ms = 150

[dock]
meter_interval_ms = 5000

[debug]
show_key_count = 8
```

A value of 0 is read as the default on `idle_fps`, `git_refresh_ms`, `marquee_step_ms`, `meter_interval_ms` and `show_key_count`, and a value out of range moves to the nearest end of the range. All seven are on the settings page and apply at once.

## The `[mouse]` table

The `[mouse]` table sets how TUIOS reads the pointer's gestures.

These options are in the [option reference](config-options.md): [`mouse.double_click_ms`](config-options.md#mouse.double_click_ms), [`mouse.drag_threshold`](config-options.md#mouse.drag_threshold).

```toml
[mouse]
double_click_ms = 450
drag_threshold = 8
```

Raise `double_click_ms` for a slow hand or a trackpad that spaces its clicks. The Agents list used 400 ms before this option. It now follows the same value as the pane, 300 ms by default. Both options are on the settings page, in the Behavior section, and apply at once. A value of 0 or a value out of range is read as the default or moved to the nearest end of the range.

## Keybindings: the `[keybindings]` table

### leader_key

The prefix key. The default is `ctrl+b`.

```toml
[keybindings]
leader_key = "ctrl+a"
```

`alt+space` is already the app launcher.

`opt+` and `option+` mean `alt+` on every platform, `cmd+` and `command+` mean `super+`, and `control+` means `ctrl+`. See [Modifier spellings](KEYBINDINGS.md#modifier-spellings). Press the leader two times in terminal mode to send it to the pane.

If TUIOS cannot read the leader, the leader is `ctrl+b` until you correct it. See [When a key is wrong](#when-a-key-is-wrong).

### lock

Lock mode gives every key in terminal mode to the pane, except the leader.

```toml
[keybindings]
lock = true
```

This option is in the [option reference](config-options.md): [`keybindings.lock`](config-options.md#keybindings.lock).

See [Lock mode](KEYBINDINGS.md#lock-mode).

### window_mode

Set `window_mode = false` to turn window mode off. You then always type into a pane, and you do every action with the leader, the palette, the mouse or the dock. The default is `true`.

```toml
[keybindings]
window_mode = false
```

You can also turn it off on the settings page, in the Behavior section, or with `tuios set-config keybindings.window_mode false`. The change applies at once.

This option is in the [option reference](config-options.md): [`keybindings.window_mode`](config-options.md#keybindings.window_mode).

See [Window mode off](KEYBINDINGS.md#window-mode-off).

### nest_exit_key

The key that ends [nest mode](KEYBINDINGS.md#nest-mode). The default is `"f12"`. A key tuios cannot read goes back to `"f12"`, and a key that programs type is reported as a problem.

### Keybinding sections

Each section is a table under `[keybindings]`. Each action takes a list of keys. The [Keybindings](KEYBINDINGS.md) page lists every default.

| Section | When it is active |
|---------|-------------------|
| `window_management` | Window mode: windows, sessions, zoom, copy |
| `workspaces` | Window mode: switch and move between workspaces |
| `layout` | Window mode: snapping, tiling, swaps, resizes, splits, preselect |
| `mode_control` | Window mode: enter terminal mode, help, settings, quit |
| `restore_minimized` | Window mode: restore a minimized window by number |
| `system` | Window mode: the spotlight |
| `navigation` | Empty. Kept so older files still parse |
| `terminal_mode` | Terminal mode, no prefix |
| `global` | Both modes: the command palette and the launcher |
| `prefix_mode` | After the leader key |
| `window_prefix` | After leader, <kbd>t</kbd> |
| `minimize_prefix` | After leader, <kbd>m</kbd> |
| `workspace_prefix` | After leader, <kbd>w</kbd> |
| `layout_prefix` | After leader, <kbd>L</kbd> |
| `tape_prefix` | After leader, <kbd>T</kbd> |
| `debug_prefix` | After leader, <kbd>D</kbd> |
| `script` | While a tape plays |
| `sidebar` | While the rail has the keyboard |
| `sidebar_files` | While the rail's cursor is on a file row. Checked before `sidebar` |
| `sidebar_agents` | While the rail's cursor is on an agent row. Checked before `sidebar` |
| `inbox` | While the Inbox is open |
| `inbox_peek` | While a prompt is open over the Inbox |
| `mail` | While the mailbox is open |
| `lists` | The cursor keys of every list: the palette, the launcher, the pickers, the menus and the settings page. See [List keys](KEYBINDINGS.md#the-full-list) |
| `gui` | Keys of the native GUI client. The terminal client does not read them |

### Key syntax

- **Modifiers:** `ctrl+`, `alt+`, `shift+`, `super+`. Also `opt+` and `option+`, which mean `alt+`, `cmd+` and `command+`, which mean `super+`, and `control+`, which means `ctrl+`. These spellings work on every platform.
- **Special keys:** `enter`, `return`, `esc`, `escape`, `tab`, `space`, `backspace`, `delete`, `up`, `down`, `left`, `right`, `home`, `end`, `pgup`, `pageup`, `pgdown`, `pagedown`, `f1` to `f12`.
- **Any single character:** letters, digits, symbols, and accented letters.
- **Held modifiers:** `leftalt`, `rightalt`, `leftctrl`, `leftsuper` and the like, for `hold_window_mode` only. They need the Kitty keyboard protocol.

`super+` only reaches TUIOS in terminals that speak the Kitty keyboard protocol. On Linux, `opt+1` is `alt+1`. See [One config on macOS and Linux](#one-config-on-macos-and-linux).

```toml
[keybindings.window_management]
new_window = ["n", "ctrl+t"]   # several keys for one action
close_window = []              # no key: the key goes to the pane
```

An empty list is different from a missing line. A missing action gets its default back at the next load. An empty list stays empty.

`shift+1` and `!` are the same key, and so are `shift+a` and `A`. Binding one spelling binds both. Do not bind the two spellings to different actions.

### Platform defaults

On macOS, the workspace and preselect defaults use `opt+`, and `terminal_mode` adds `opt+tab`, `opt+shift+tab` and `opt+esc`. Writing `alt+` on macOS works but gives an advisory warning. Elsewhere the defaults use `alt+`. See [Keybindings](KEYBINDINGS.md).

## The `[daemon]` table

These options are in the [option reference](config-options.md): [`daemon.agent_binaries`](config-options.md#daemon.agent_binaries), [`daemon.single_client`](config-options.md#daemon.single_client), [`daemon.ssh_agent`](config-options.md#daemon.ssh_agent), [`daemon.display`](config-options.md#daemon.display), [`daemon.respond_from_shell`](config-options.md#daemon.respond_from_shell).

These options are in the [option reference](config-options.md): [`daemon.log_level`](config-options.md#daemon.log_level), [`daemon.agent_autodetect`](config-options.md#daemon.agent_autodetect), [`daemon.agent_detect_seconds`](config-options.md#daemon.agent_detect_seconds), [`daemon.agent_stall_seconds`](config-options.md#daemon.agent_stall_seconds), [`daemon.subagent_fade_seconds`](config-options.md#daemon.subagent_fade_seconds), [`daemon.resume_agents`](config-options.md#daemon.resume_agents), [`daemon.persist_scrollback`](config-options.md#daemon.persist_scrollback), [`daemon.persist_scrollback_lines`](config-options.md#daemon.persist_scrollback_lines), [`daemon.persist_scrollback_kb`](config-options.md#daemon.persist_scrollback_kb), [`daemon.keep_awake`](config-options.md#daemon.keep_awake), [`daemon.window_size`](config-options.md#daemon.window_size), [`daemon.client_follows_upgrade`](config-options.md#daemon.client_follows_upgrade).

A change to `single_client`, `ssh_agent` or `display` in the file applies to the next attach.

### Daemon timings

The daemon's timings are whole seconds in `[daemon]`, and `0` reads as the default. The daemon reads them when it starts. Each has an environment variable, read at the same time, that wins over the file:

These options are in the [option reference](config-options.md): [`daemon.agent_detect_seconds`](config-options.md#daemon.agent_detect_seconds), [`daemon.agent_stall_seconds`](config-options.md#daemon.agent_stall_seconds), [`daemon.subagent_fade_seconds`](config-options.md#daemon.subagent_fade_seconds), [`daemon.cleanup_seconds`](config-options.md#daemon.cleanup_seconds), [`daemon.pr_poll_seconds`](config-options.md#daemon.pr_poll_seconds), [`daemon.transfer_keep_seconds`](config-options.md#daemon.transfer_keep_seconds), [`daemon.dispatch_ready_seconds`](config-options.md#daemon.dispatch_ready_seconds).

`agent_autodetect` reads `TUIOS_AGENT_AUTODETECT` the same way: `false`, `no`, `off` or `0` turns detection off.

These timings and limits have no environment variable. `0` reads as the default, and a value outside the range moves to the nearer end.

These options are in the [option reference](config-options.md): [`daemon.save_seconds`](config-options.md#daemon.save_seconds), [`daemon.exec_timeout_seconds`](config-options.md#daemon.exec_timeout_seconds), [`daemon.reconnect_seconds`](config-options.md#daemon.reconnect_seconds), [`daemon.host_heartbeat_seconds`](config-options.md#daemon.host_heartbeat_seconds), [`daemon.host_heartbeat_timeout_seconds`](config-options.md#daemon.host_heartbeat_timeout_seconds), [`daemon.host_call_timeout_seconds`](config-options.md#daemon.host_call_timeout_seconds), [`agents.ask_wait_seconds`](config-options.md#agents.ask_wait_seconds), [`agents.undo_window_seconds`](config-options.md#agents.undo_window_seconds), [`files.drop_keep_hours`](config-options.md#files.drop_keep_hours), [`files.drop_keep_mb`](config-options.md#files.drop_keep_mb).

The daemon reads them when it starts. The client reads `reconnect_seconds` and `undo_window_seconds` from its own config, and a change applies at once. The time a dial to one machine may take is `connect_timeout` in that machine's `[hosts.NAME]` table.

### The display of the client

A daemon that a host link starts over ssh has no `WAYLAND_DISPLAY` or `DISPLAY`. Its panes have none either, so `wl-copy`, `wl-paste`, `xclip` and image paste fail. With `display = "follow"`, the default, the new panes of such a daemon get the display of the local client that attached to the session or used it last.

```toml
[daemon]
display = "follow"
```

- A daemon that has a `WAYLAND_DISPLAY` or a `DISPLAY` of its own keeps it. The option does nothing then.
- Each session has a link to the Wayland socket of the client: `wayland-<session id>.sock`, in the folder of the daemon socket. A new pane gets `WAYLAND_DISPLAY` set to the full path of the link. The link moves to the socket of the newest client.
- A new pane also gets `DISPLAY` and `XAUTHORITY` of the client. `DISPLAY` is a name, not a path, so a pane keeps the value it started with.
- A shell that started before a desktop client attached has no display. Run `eval "$(tuios display-env 2>/dev/null)"` in that shell, or add the line to your shell rc. See [`tuios display-env`](CLI_REFERENCE.md#tuios-display-env).
- When the client detaches, the session uses the display of the client before it. When no attached client has a display, tuios removes the link, and new panes get no display.
- tuios uses the display of `tuios attach` and `tuios new` only. The client must run on this machine, as your user, and outside every pane. tuios ignores a client that comes through a host link, a client of the tuios SSH server and a client of `tuios-web`.
- A `WAYLAND_DISPLAY` name such as `wayland-1` is looked up in the `XDG_RUNTIME_DIR` of the client, then in the one of the daemon, then in `/run/user/<uid>`. The client does not need `XDG_RUNTIME_DIR`.
- The Wayland socket must be a Unix socket that you own. `DISPLAY` must be `:N` or `:N.S`, and tuios ignores a forwarded display such as `localhost:10.0`. `XAUTHORITY` must be a file that you own.
- The option is read from the file only. `tuios set-config` cannot change it.

To change `window_size` for one session while it runs, use `tuios set-config daemon.window_size smallest -s NAME`. That value is not saved to the file. A change in the file applies when the daemon starts again.

`tuios set-config daemon.log_level messages` changes the level of a running daemon at once. `verbose` and `trace` also log pane content, window titles and paths. `tuios logs` reads the log.

## The `[startup]` table

`[startup]` decides how a new session starts. It never changes a running session.

These options are in the [option reference](config-options.md): [`startup.daemon`](config-options.md#startup.daemon), [`startup.tiled`](config-options.md#startup.tiled), [`startup.layout`](config-options.md#startup.layout), [`startup.open_default_window`](config-options.md#startup.open_default_window), [`startup.start_in_terminal_mode`](config-options.md#startup.start_in_terminal_mode), [`startup.session`](config-options.md#startup.session), [`startup.window`](config-options.md#startup.window).

When the file sets `layout` but not `tiled = true`, new sessions float and do not use the layout. TUIOS says so in the log viewer (<kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>D</kbd> <kbd>l</kbd>) and in `tuios config apply`: `[startup] layout is set, but [startup] tiled is off, so new sessions float and do not use the layout. Set tiled = true to use the layout.`

`session` and `window` change a plain `tuios` only. Other commands use the session you give them. `session` uses the daemon even when `daemon` is `false`.

`--standalone` and `TUIOS_NO_DAEMON=1` override `daemon` for one run or one shell. If the daemon will not start, a bare `tuios` runs standalone and says so.

> **Older config files keep their behaviour.** `daemon` and `tiled` default to `true` in a new install. A config file that already exists and does not mention them reads them as `false`, so an upgrade keeps the floating, standalone start it had. Add the keys to opt in.
>
> In the same way, v0.8.0 changed eight appearance defaults: the rail is on, on the right, 24 columns wide; the dock and the window titles are on top; a zoomed pane takes 95% of the screen; a double click starts typing; and the scrollbar is a track. A config file that sets one of these keys keeps its value. The first-run file older releases wrote names most of them, so such an install keeps the old look until you remove those lines.

A session keeps its own layout once it is running. Attaching never rearranges a session to match your config.

## The `[workspaces]` table

When the last pane on the workspace on screen closes, TUIOS shows the workspace you came from. This happens when the pane's program stops, when you close the pane, when `tuios xpanes -ss` closes it, and with `tuios close-workspace`.

These options are in the [option reference](config-options.md): [`workspaces.return_when_empty`](config-options.md#workspaces.return_when_empty), [`workspaces.new_window_when_empty`](config-options.md#workspaces.new_window_when_empty).

TUIOS goes back in this order:

1. For a workspace that `tuios xpanes` opened, the workspace where you ran `tuios xpanes`.
2. The workspace you showed last that still has panes. After a chain of empty workspaces, TUIOS goes back past each one.
3. The workspace with the lowest number that has panes.

When no workspace has panes, TUIOS shows the splash screen. The workspace on screen is the same for every client of the session, so every client goes back. A pane that moves to another workspace does not close, so TUIOS does not go back.

```toml
[workspaces]
return_when_empty = false
```

The daemon reads the setting when it starts and when the file changes. `tuios set-config workspaces.return_when_empty false` changes it while TUIOS runs.

### A pane on an empty workspace

By default, a workspace with no panes shows the splash screen. To open a pane there when you switch to it, as tabs do in other multiplexers, turn on `new_window_when_empty`:

```toml
[workspaces]
new_window_when_empty = true
```

The new pane starts in the folder of the pane that had focus on the workspace you came from. It starts in the session's start folder (the folder that `tuios new --cwd` gave) when you came from a workspace with no pane, from a scratch terminal, or from a pane on another machine. With `appearance.new_window_follow_ssh = true`, a pane that runs ssh gives a new pane that runs the same ssh, as the new-window key does.

A pane opens only when you switch: with a workspace key, a click on the dock, go-to, the pane navigator or the command palette. These switches do not open a pane:

- `move_and_follow` and other moves that take a pane with them.
- `tuios xpanes`, `tuios select-workspace`, `tuios run-command` and tape scripts.
- The switch back that `return_when_empty` makes.
- The start of TUIOS and an attach to a session.

A workspace gets one pane, also when two clients switch to it at the same time. When you switch away before the pane opens, you stay where you went.

Each client reads the setting from its config file, and a change applies at once. The settings page lists it under Daemon, and `tuios set-config workspaces.new_window_when_empty true` changes it while TUIOS runs.

The daemon must be from the same release or newer. An older daemon opens no pane. Run `tuios restart-server` to load the new daemon.

## The `[launcher]` table

The app launcher starts a desktop entry in a new pane. A graphical program opens its own window, so its pane stays empty. Set [`launcher.gui_command`](config-options.md#launcher.gui_command) to start such an entry with another command. The launcher adds the entry's argv to that command. Entries with `Terminal=true` and programs from `$PATH` still open in a pane.

```toml
[launcher]
gui_command = "tuios-wayland launch --"   # or "niri msg action spawn --"
```

> **Use a command that runs an argv.** Do not use a command that joins its arguments into a shell line, such as `swaymsg exec --` or `hyprctl dispatch exec`. With such a command, the text of a desktop entry becomes shell code. Any installed package can write a desktop entry.

[`launcher.keep_open`](config-options.md#launcher.keep_open) keeps the pane of a launched program open after it exits. A line under the output shows the exit status. The option needs a daemon session.

```toml
[launcher]
keep_open = true
```

## The `[tape]` table

`[tape]` controls project tapes: a `.tuios.tape` file in a directory. See [Tape Scripting](TAPE_SCRIPTING.md).

These options are in the [option reference](config-options.md): [`tape.autorun`](config-options.md#tape.autorun), [`tape.auto_review`](config-options.md#tape.auto_review).

A tape edited after you trusted it is untrusted again.

## The `[debug]` table

These options are in the [option reference](config-options.md): [`debug.show_key_events`](config-options.md#debug.show_key_events).

`--show-keys` and <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>D</kbd> <kbd>k</kbd> toggle the same overlay. See [Showkeys](https://tuios.dev/docs/showkeys).

## The `[screenshot]` table

`[screenshot]` sets the defaults for [`tuios screenshot`](CLI_REFERENCE.md#tuios-screenshot) and the in-app capture (<kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>C</kbd>). Each key has a command flag that overrides it for one capture.

These options are in the [option reference](config-options.md): [`screenshot.format`](config-options.md#screenshot.format), [`screenshot.directory`](config-options.md#screenshot.directory), [`screenshot.copy`](config-options.md#screenshot.copy), [`screenshot.preview`](config-options.md#screenshot.preview), [`screenshot.frame`](config-options.md#screenshot.frame), [`screenshot.background`](config-options.md#screenshot.background), [`screenshot.padding`](config-options.md#screenshot.padding), [`screenshot.radius`](config-options.md#screenshot.radius), [`screenshot.shadow`](config-options.md#screenshot.shadow), [`screenshot.controls`](config-options.md#screenshot.controls), [`screenshot.title_format`](config-options.md#screenshot.title_format), [`screenshot.font_family`](config-options.md#screenshot.font_family), [`screenshot.font_file`](config-options.md#screenshot.font_file), [`screenshot.scale`](config-options.md#screenshot.scale), [`screenshot.cursor`](config-options.md#screenshot.cursor).

```toml
[screenshot]
format = "svg"
frame = "plain"
background = "none"
padding = 24
```

`png` and `svg` carry the frame. `ansi` and `txt` are plain output, so the frame keys do not apply.

## The `[screensaver]` table

`[screensaver]` animates the screen after a period with no input. It is off by default.

These options are in the [option reference](config-options.md): [`screensaver.enabled`](config-options.md#screensaver.enabled), [`screensaver.idle_minutes`](config-options.md#screensaver.idle_minutes), [`screensaver.effect`](config-options.md#screensaver.effect), [`screensaver.while_busy`](config-options.md#screensaver.while_busy).

```toml
[screensaver]
enabled = true
idle_minutes = 5
effect = "matrix"
```

There are 36 effects. `tuios list-options screensaver.effect` prints them. A plugin can add a saver: set `effect = "plugin/ID/SAVER"`. See [A screen saver in a plugin](https://tuios.dev/docs/plugins#a-screen-saver-in-a-plugin). The settings page previews each effect on your current screen.

<kbd>S</kbd> in window mode starts the screen saver now, whatever `enabled` says.

## The `[hints]` table

`[hints]` sets what [hints mode](HINTS.md) (<kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>F</kbd>) labels.

This option is in the [option reference](config-options.md): [`hints.patterns`](config-options.md#hints.patterns).

These options are in the [option reference](config-options.md): [`hints.builtins`](config-options.md#hints.builtins), [`hints.alphabet`](config-options.md#hints.alphabet), [`hints.open`](config-options.md#hints.open), [`hints.dim`](config-options.md#hints.dim), [`hints.all_panes`](config-options.md#hints.all_panes).

See [What hints mode finds](HINTS.md#what-hints-mode-finds) for the pattern names.

## The `[panes]` table

`[panes]` sets the keys of the [pane labels](https://tuios.dev/docs/find-a-pane#pane-labels) (<kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>Q</kbd>) and the first layout of the pane navigator (<kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>/</kbd>).

These options are in the [option reference](config-options.md): [`panes.label_keys`](config-options.md#panes.label_keys), [`panes.navigator_layout`](config-options.md#panes.navigator_layout), [`panes.navigator_search`](config-options.md#panes.navigator_search), [`panes.drag_and_drop`](config-options.md#panes.drag_and_drop), [`panes.drag_and_drop_desktop`](config-options.md#panes.drag_and_drop_desktop).

With `drag_and_drop` on, a program that speaks the protocol, such as yazi 26.9 or later, can drag files from its pane. Hold the left button on a file in terminal mode, and carry it to another pane. A pane whose program speaks the protocol takes the drop itself. Any other pane gets the paths as a paste, quoted for its shell, by the rules of [a drag from the rail](https://tuios.dev/docs/session-rail#drag-a-file-into-a-pane). tuios reads no file, and refuses a drop back into the pane the drag came from. The switch is off because the protocol is still settling.

### Drag and drop with the desktop

Set `drag_and_drop_desktop = true` with `drag_and_drop = true` to drag files between the desktop and the panes. The terminal that tuios runs in must speak the same protocol. Today that is kitty 0.47 or later. tuios asks the terminal once at startup.

```toml
[panes]
drag_and_drop = true
drag_and_drop_desktop = true
```

- **From the desktop**: drag a file from a file manager or a browser onto the tuios window. It lands on the pane under the pointer. A program that speaks the protocol takes it. Any other pane gets the path as a paste, by the rules above.
- **To the desktop**: a file that a program in a pane drags, such as yazi, becomes a drag on the desktop. Drop it on any other window. The desktop shows the file names and the pane they come from.
- tuios takes a drag only when the press went to a pane that offers drags. You still move a pane, select text and drag a row of the rail as before.
- The bridge is for a terminal on this machine. A client over SSH, through tuios-web, or attached to the daemon of another machine does not use it.
- Both switches apply without a restart.

## The `[path_picker]` table

`[path_picker]` sets `show_hidden`. With `true`, the [path picker](KEYBINDINGS.md#pick-a-path) shows the names that start with a dot when it opens. The default is `false`.

## The `[worktree]` table

`[worktree] path` sets where `tuios worktree new`, **New worktree...** on the rail and `tuios fan` put a worktree. The value is a path template. Empty is the default. See [Where a worktree goes](https://tuios.dev/docs/worktrees#where-a-worktree-goes).

```toml
[worktree]
path = "{{ repo_path }}/.worktrees/{{ branch | slug }}"
```

## The `[review]` table

`[review]` sets how a [review](https://tuios.dev/docs/agent-inbox#reviewing-an-agents-changes) opens.

These options are in the [option reference](config-options.md): [`review.focus`](config-options.md#review.focus), [`review.scope`](config-options.md#review.scope).

```toml
[review]
focus = "files"
scope = "uncommitted"
```

`base` shows the changes since the base. In a plain repository, the base is the merge base with the upstream branch, so commits that you did not push yet are part of the review. `uncommitted` shows only the changes that are not committed. In the review, <kbd>u</kbd> still switches between the two, and <kbd>b</kbd> still sets another base. The compare view of two attempts does not use `scope`.

Both keys are on the settings page, in the Behavior section. `tuios set-config review.scope uncommitted` applies from the next review. tuios reads another value as the default and shows a warning when the config loads. `tuios set-config` refuses it.

## The `[tasks]` table

`[tasks]` sets how the [task board](https://tuios.dev/docs/task-boards) works.

This option is in the [option reference](config-options.md): [`tasks.stages`](config-options.md#tasks.stages).

These options are in the [option reference](config-options.md): [`tasks.auto_stage`](config-options.md#tasks.auto_stage), [`tasks.attachment_max_mib`](config-options.md#tasks.attachment_max_mib), [`tasks.attachments_per_card`](config-options.md#tasks.attachments_per_card), [`tasks.attachment_session_mib`](config-options.md#tasks.attachment_session_mib), [`tasks.machine_cap`](config-options.md#tasks.machine_cap).

```toml
[tasks]
stages = ["todo", "doing", "verify"]
auto_stage = false
machine_cap = 4
attachment_max_mib = 4
attachments_per_card = 10
```

A file over a limit is refused with an error that names the option. See [Attachments](https://tuios.dev/docs/task-boards#attachments).

The dispatch settings (mode, preset, caps, quiet hours, verify) are not in the config file. `tuios task dispatch` sets them, and the daemon saves them. See [Dispatch](https://tuios.dev/docs/task-boards#dispatch).

## The `[files]` table

`[files]` sets the copies that <kbd>o</kbd> on a file of another machine makes here, in the rail's files section.

These options are in the [option reference](config-options.md): [`files.open_copy_ask_mb`](config-options.md#files.open_copy_ask_mb), [`files.open_copy_max_mb`](config-options.md#files.open_copy_max_mb), [`files.open_copy_keep_days`](config-options.md#files.open_copy_keep_days).

## Shell prompt marks: the `[shell]` table

TUIOS turns on OSC 133 prompt marks in the bash and zsh shells it starts in panes, as Ghostty and kitty do. The marks say where a prompt starts, where a command starts, and how it ended. `tuios run`, `wait-for command-finished`, `capture-pane --last-command`, the scrollback browser and copy mode read them. A pane with marks also keeps one copy of its prompt when you make the pane narrower.

TUIOS does not change your files:

- **zsh** starts with `ZDOTDIR` set to a folder of TUIOS's own. Its `.zshenv` sets `ZDOTDIR` back to your value, reads your `.zshenv`, and adds the marks. zsh then reads your `.zprofile`, `.zshrc` and `.zlogin` as usual.
- **bash** starts with `--rcfile` set to a file that reads your `~/.bashrc` and then adds the marks. Your `PS1`, `PROMPT_COMMAND`, traps and `set -e` stay as they are. This needs bash 4.4 or newer.
- **fish 4** and **nushell** send the marks themselves, so TUIOS adds nothing. Other shells start as they are.

When your own prompt already sends a mark, TUIOS does not send that mark again. A shell starts less than 1 ms slower. To turn it off:

```toml
[shell]
integration = "off"   # default: "auto"
```

The change applies to new panes. `tuios new-window --shell-integration off` chooses for one pane, and `tuios doctor shell` shows which panes send marks. A pane that runs `ssh`, or a window on another machine, gets no marks from TUIOS: add the lines `tuios doctor shell` prints to the rc file there.

## The `[scratch]` table

`[scratch]` sets the size of the scratch terminal that <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>g</kbd> shows in a popup. See [Scratch terminal](https://tuios.dev/docs/sessions#scratch-terminal).

These options are in the [option reference](config-options.md): [`scratch.width`](config-options.md#scratch.width), [`scratch.height`](config-options.md#scratch.height).

The old `session` key is no longer used. TUIOS ignores it, and the config warnings tell you to remove it.

## The `[pip]` table

`[pip]` sets the size and the first place of the picture-in-picture view.

```toml
[pip]
width = "30%"            # of the pane area, or a number of cells such as 40
height = "25%"
corner = "bottom-center" # or a corner, or top-center
```

See [Picture in picture](SESSIONS.md#picture-in-picture).

These options are in the [option reference](config-options.md): [`pip.width`](config-options.md#pip.width), [`pip.height`](config-options.md#pip.height), [`pip.corner`](config-options.md#pip.corner).

## The `[paste_buffers]` table

`[paste_buffers]` sets how many [paste buffers](https://tuios.dev/docs/paste-buffers) the daemon keeps. The daemon reads it when it starts and when the file changes.

These options are in the [option reference](config-options.md): [`paste_buffers.limit`](config-options.md#paste_buffers.limit), [`paste_buffers.max_kb`](config-options.md#paste_buffers.max_kb).

A smaller limit removes the oldest buffers at once. A yank larger than `max_kb` does not become a buffer, but it still goes to the clipboard.

## Command keys

`[[keybindings.command]]` binds a key to a command that you write: a scratch terminal, a popup, a pane or a command with no window. A mistake in an entry is a warning, and TUIOS ignores that entry. See [Command keys](KEYBINDINGS.md#command-keys).

## Actions and menus of your own: the `[[actions]]` and `[menus]` tables

`[[actions]]` declares an action of your own, `user:NAME`: a built-in action, a program with its arguments (placeholders such as `{pane.cwd}` fill one argument each, and no shell reads them), or a plugin action. A key, a menu row or `tuios actions run` runs it. See [Your actions](KEYBINDINGS.md#your-actions).

`[menus.NAME]` declares a menu: a title and a list of rows, each an action or a submenu with an optional `key`, `label` and `when`. `open_menu:NAME` opens it, as a which-key panel from a key and in place from a right-click menu. The target names (`pane`, `session`, `file` and the others) are the shipped right-click menus: `add` puts rows after the row `after` names, `remove` takes rows out, and `items` replaces them. An action's `targets` and `when` decide where its row shows. The which-key panel shows the rows in the order you write them, and a `"---"` row starts a new group. The panel title is the `title` as you write it. tuios ships one menu of its own, `start`: a short menu in plain words with New terminal, Split, Close, Find a file, Select and copy text, All commands, Settings and Help. Bind `open_menu:start` to a key to open it. A `[menus.start]` of your own replaces it. See [Your menus](KEYBINDINGS.md#your-menus).

`[keybindings.modes.NAME]` declares a key mode: a set of keys that stays on until `esc` or `leave_mode`. `enter_mode:NAME` turns it on. `exclusive` drops the keys the mode does not bind, and `once` turns the mode off after one key. Modes stack, and the dock shows a chip while one is on. See [Key modes](KEYBINDINGS.md#key-modes).

## The `[spotlight]` table

`[spotlight]` lights one part of the screen and dims the rest, for screen sharing. <kbd>B</kbd> in window mode, or <kbd>Ctrl</kbd>+<kbd>B</kbd> <kbd>B</kbd> in any mode, turns it on and off. While it is on, <kbd>Esc</kbd> in window mode or a click on the Spotlight chip in the dock turns it off. See [Spotlight](KEYBINDINGS.md#spotlight).

These options are in the [option reference](config-options.md): [`spotlight.enabled`](config-options.md#spotlight.enabled), [`spotlight.follow`](config-options.md#spotlight.follow), [`spotlight.radius`](config-options.md#spotlight.radius), [`spotlight.dim`](config-options.md#spotlight.dim), [`spotlight.edge`](config-options.md#spotlight.edge), [`spotlight.shake`](config-options.md#spotlight.shake).

`follow = "cursor"` sends fewer bytes to a remote client.

## The `[dock]` table

`[dock]` sets what the bar draws: three ordered lists of component names, plus a table for each component that needs one. Edit it in the file, or with the **Dock > Components** editor on the settings page. `set-config` cannot change the lists.

```toml
[dock]
left   = ["mode", "workspaces", "trail", "tape"]
center = ["windows"]
right  = ["transfers", "notifications", "copy-help", "cpu", "ram", "clock", "session-controls"]

[dock.clock]
format = "15:04"

[dock.custom.branch]
command  = "git branch --show-current"
refresh  = "event:after-focus-change"
on-click = "tuios new-window log -- git log --oneline -20"
```

These lists are the default. A missing list uses the default. An empty list (`left = []`) draws nothing on that side.

The built-in components are `mode`, `workspaces`, `trail`, `tape`, `windows`, `transfers`, `notifications`, `copy-help`, `cpu`, `ram`, `clock`, `session-controls`, `session` and `buttons`. Listing one lets it draw. It still needs its own condition, so `cpu` still needs `show_cpu`. `copy-help` shows the keys of copy mode, multi copy mode and hints mode while the mode is open. See [Mode keys in the dock](KEYBINDINGS.md#mode-keys-in-the-dock).

`session-controls` shows an agents button when the session has an agent pane. The button opens the agents view, the same as `Ctrl+B i`. When agents wait for you, the button shows how many in the attention colour.

`buttons` shows five buttons: New, Split, Close, Help and Settings. Click a button to run its action. New opens a terminal. Split divides the pane side by side. Close closes the pane. Help shows the help. Settings opens the settings. Point at a button to read what it does. The buttons are always on the right. On a narrow dock, the buttons show icons instead of words. When the icons do not fit, the last buttons go into a more button at the end of the row. Click it to see them.

`transfers` shows the copies between machines. It shows nothing while no copy exists. One copy is a bar and a percent, `[####--] 61%`. More copies are a count and a percent, `3 copies 42%`. `waiting` means a link is down. `1 failed` stays until you open the Copies panel. A click opens the [Copies panel](KEYBINDINGS.md#copies-between-machines).

`[dock.clock]` takes `format`, a Go time layout. A layout without seconds refreshes once a minute. `session` shows the session name, the workspace and the pane, such as `main 2:1`. It is in no default list. `[dock.session]` takes `format`, with `{session}`, `{workspace}` and `{pane}`. It also takes the cell style keys `fg`, `bg`, `bold`, `cap_left` and `cap_right`. `[dock.custom.NAME]` takes the same style keys. Colours are `#rrggbb`. When the `center` list has a clock or a custom cell, the centre is at the middle of the terminal. A notification does not move it, and it does not shorten the minimized pane labels. On a crowded bar, the centre moves left to keep the cells on the right on the screen. Click a cut notification to read the full message.

### Custom components

`[dock.custom.NAME]` defines a cell. Put `custom/NAME` in a list to place it.

These options are in the [option reference](config-options.md): [`dock.custom.NAME.command`](config-options.md#dock.custom.NAME.command), [`dock.custom.NAME.refresh`](config-options.md#dock.custom.NAME.refresh), [`dock.custom.NAME.on-click`](config-options.md#dock.custom.NAME.on-click), [`dock.custom.NAME.max-width`](config-options.md#dock.custom.NAME.max-width), [`dock.custom.NAME.fg`](config-options.md#dock.custom.NAME.fg), [`dock.custom.NAME.bg`](config-options.md#dock.custom.NAME.bg), [`dock.custom.NAME.bold`](config-options.md#dock.custom.NAME.bold), [`dock.custom.NAME.cap_left`](config-options.md#dock.custom.NAME.cap_left), [`dock.custom.NAME.cap_right`](config-options.md#dock.custom.NAME.cap_right).

With `push`, the command keeps running and each line it prints replaces the cell. With `event:`, the command runs again when the daemon reports one of those [hook events](HOOKS.md).

A component whose command fails is hidden. `tuios list-dock-components` shows what each component last did and why a cell is missing. `tuios refresh-dock [name]` runs a component again now.

A command runs in the client, with a three second timeout. TUIOS reads at most 64 KiB of its output and stops the command when it prints more. SGR colour stays. TUIOS removes other control sequences, C1 controls, bidirectional and invisible characters, and bytes that are not UTF-8. Wide characters and emoji stay.

## SSH server limits: the `[ssh]` table

The `[ssh]` table sets the limits of the `tuios ssh` server. The server reads it when it starts. The defaults do not stop a normal login.

```toml
[ssh]
handshake_timeout = "120s" # time to finish the login; 0 turns the limit off
idle_timeout = "0"         # close a connection with no traffic for this long; 0 is off
max_handshakes = 32        # connections that log in at the same time; 0 is no limit
max_sessions = 64          # sessions open at the same time; 0 is no limit
```

These options are in the [option reference](config-options.md): [`ssh.handshake_timeout`](config-options.md#ssh.handshake_timeout), [`ssh.idle_timeout`](config-options.md#ssh.idle_timeout), [`ssh.max_handshakes`](config-options.md#ssh.max_handshakes), [`ssh.max_sessions`](config-options.md#ssh.max_sessions).

A duration is a value such as `90s`, `30m` or `2h`. A value that does not parse, or a negative value, gives a warning and the default. `tuios ssh --handshake-timeout`, `--idle-timeout`, `--max-handshakes` and `--max-sessions` replace these values for one run. The server writes the limits it uses to its log when it starts. `tuios set-config` cannot change this table.

## The `[hosts]` table

`[hosts]` names the other machines this daemon links to. Each host is a table.

```toml
[hosts.build]
addr = "gaurav@buildbox"

[hosts.work]
addr = "workstation"
connect_timeout = 5
```

These options are in the [option reference](config-options.md): [`hosts.NAME.addr`](config-options.md#hosts.NAME.addr), [`hosts.NAME.connect_timeout`](config-options.md#hosts.NAME.connect_timeout), [`hosts.NAME.command`](config-options.md#hosts.NAME.command), [`hosts.NAME.ssh_options`](config-options.md#hosts.NAME.ssh_options), [`hosts.NAME.repos_root`](config-options.md#hosts.NAME.repos_root).

You can also manage the table from the shell. These commands edit the file:

```bash
tuios hosts add build gaurav@buildbox
tuios hosts test build
tuios hosts remove build
```

A change that gives a machine less applies at once, also on links that are open. A new host, a host that dials another way, or a change that gives a machine more waits for `tuios config apply` from a terminal outside TUIOS. A daemon restart also applies it. So `tuios hosts add` from a pane waits. A host that you add with "Add a host" on the settings page applies at once. See [Changes that wait](https://tuios.dev/docs/agents#changes-that-wait).

### Rules for ssh_options

`ssh_options` accepts only options that cannot run code or write files on this machine. Write each option as `-o Keyword=value`, with one plain value and no quotes.

- Accepted examples: `-p`, `-i`, `-l`, `-J`, `Port`, `User`, `HostName`, `HostKeyAlias`, `IdentityFile`, `StrictHostKeyChecking`, the `ServerAlive` options and the algorithm lists.
- Refused: `ProxyCommand`, `LocalCommand`, the known hosts files, `ControlPath`, `-F` and socket forwards.
- Refused: port forwards (`-L`, `-R`, `-D`).
- `-J` and `ProxyJump` take host names only. `ForwardAgent` takes only `yes` or `no`.
- A host `addr` or `command` cannot start with a dash.

TUIOS drops a host with a refused option. `tuios hosts` and the Inbox name the reason. Put a refused option in `~/.ssh/config`, and name that host in `addr`.

```toml
[hosts.build]
addr = "gaurav@buildbox"
ssh_options = ["-J", "bastion", "-o", "ServerAliveInterval=30"]
```

> **Upgrading from v0.8.0.** After the upgrade, look in the Inbox or run `tuios hosts`. A host with a refused option is dropped. Move the option, such as `ProxyCommand` or `LocalForward`, into `~/.ssh/config`. Keep port forwards in `~/.ssh/config` too.

The daemon runs ssh with `BatchMode` on, so a link never asks for a password or about a host key. Run ssh to the host once by hand to accept its key.

### Files over a link

A machine with `files` can read your files and write new ones. Three keys in its `[hosts.NAME]` table set the limits:

```toml
[hosts.laptop]
allow = ["list", "files"]
files_roots = ["~/Downloads/tuios", "~/dev"]   # where it may write
files_read_roots = ["~/share"]                 # where it may read
files_min_free = "5GiB"                        # the space a write must leave free
```

These options are in the [option reference](config-options.md): [`hosts.NAME.files_roots`](config-options.md#hosts.NAME.files_roots), [`hosts.NAME.files_read_roots`](config-options.md#hosts.NAME.files_read_roots), [`hosts.NAME.files_min_free`](config-options.md#hosts.NAME.files_min_free), [`hosts.NAME.files_max_entries`](config-options.md#hosts.NAME.files_max_entries).

A path is absolute or starts with `~`. Some places are always closed, whatever the roots say:

- **Writes** never go into `~/.ssh`, `~/.gnupg`, cloud and git credentials, shell start files such as `.bashrc` and `.zshrc`, login items, crontabs, `/proc`, or tuios's own config and state.
- **Reads** never return keys, credentials, keyrings, shell histories, browser profiles, `/proc`, or tuios's own state. A public `*.pub` key in `~/.ssh` is the one exception.
- Reads also never return `~/.gitconfig`, `~/.config/git`, shell start files or `~/.config/environment.d`. The list also covers the snap and flatpak browser folders, Thunderbird, Signal, Discord, Slack, Telegram, VS Code and common crypto wallets. A folder copy over a link leaves these entries out.
- A file with more than one name (a hard link) is never read over a link. Copy it to a new file to share it.

The read rules and `files_read_roots` also apply to a kitty image that a program names by path in a pane that runs on this machine for the other machine. See [Kitty images in the pane](https://tuios.dev/docs/remote-hosts#kitty-images-in-the-pane).

A refused write says where writes may land and which key to change. A deny list can never name every secret on a machine, so `files_read_roots` is the stronger choice.

Other limits:

- One machine may have at most 8 write streams and 64 unfinished copies here at once. Past that, a write is `busy`, and a copy waits and tries again.
- Files dropped on a pane go to a private drop folder. It holds at most 256 MiB for all drops, for 24 hours.
- A copy another machine starts here keeps to the same rules, also after tuios restarts. When `files` is taken away, its copies here stop.
- A change that widens `files_roots` or `files_read_roots`, removes `files_read_roots`, or lowers `files_min_free` waits for `tuios config apply`. A change that narrows applies at once.
- A file that another machine writes here gets the sender's permission bits under this machine's umask. It never gets group or world write. The execute bits stay. This holds for a paired device too.
- A machine with `files` cannot write the config folders of the agent tools that tuios integrates. It also cannot read the logins and API keys of more agent tools and cloud tools.

> **Give files only to machines you trust.** A machine with `files` can write any file inside its roots and read any file outside the deny list. A file copy can run code: a binary in a folder on your `PATH`, or a config file that a program reads. Give `files`, and a wide `files_roots`, only to a machine you trust as you trust your own.

See [Remote Hosts](https://tuios.dev/docs/remote-hosts#copy-files-between-machines) for the copies themselves.

### Using a linked host

Once a host is linked, you can list its sessions and agents (`tuios ls --all-hosts`, `tuios list-agents --host build`), attach to or create sessions on it (`tuios attach --host build api`, `tuios new --host build`), and open a pane whose process runs there (`tuios new-window --host build`). See [`tuios hosts`](CLI_REFERENCE.md#commands-for-other-machines).

### Reaching a host you cannot ssh to directly

A host behind NAT, with no port forward and no jump host, is a network problem to solve under TUIOS. Any tool that pipes stdio to a remote port works as an ssh `ProxyCommand`. `ssh_options` refuses `ProxyCommand`, so name it in `~/.ssh/config`.

Tailscale's [tailcat](https://github.com/tailscale/tailcat) is one such tool. It carries a connection over WireGuard with no Tailscale account and no open port. Run it on the far side against sshd:

```sh
tailcat --serve=22
```

Then add a host to `~/.ssh/config` that uses it:

```text
Host laptop-tailcat
    HostName laptop
    User gaurav
    ProxyCommand tailcat <token> 22
```

Name that host in the host entry:

```toml
[hosts.laptop]
addr = "laptop-tailcat"
```

ssh still decides who may connect: the tunnel only carries bytes to sshd, which asks for your key as usual. A leaked tailcat token gives reachability, not access. TUIOS adds no identity system of its own and opens no listening port.

## The `[tailscale]` table

`[tailscale]` changes which tailnet machines `tuios hosts add` and `tuios hosts tailnet` offer as addresses. It never adds a host by itself.

These options are in the [option reference](config-options.md): [`tailscale.enabled`](config-options.md#tailscale.enabled), [`tailscale.addr`](config-options.md#tailscale.addr), [`tailscale.user`](config-options.md#tailscale.user), [`tailscale.users`](config-options.md#tailscale.users), [`tailscale.os`](config-options.md#tailscale.os), [`tailscale.offline`](config-options.md#tailscale.offline), [`tailscale.self`](config-options.md#tailscale.self), [`tailscale.shared`](config-options.md#tailscale.shared), [`tailscale.include`](config-options.md#tailscale.include), [`tailscale.exclude`](config-options.md#tailscale.exclude), [`tailscale.max`](config-options.md#tailscale.max), [`tailscale.socket`](config-options.md#tailscale.socket).

```toml
[tailscale]
user = "ubuntu"
exclude = ["*-pad-*"]

[tailscale.users]
build = "admin"
```

## The `[agents]` table

`[agents]` sets how TUIOS treats the coding agents in its panes. Except for `enabled`, it is outside `list-options`, and `set-config` cannot change it, so a program in a pane cannot loosen it. The daemon reads it at start and again whenever the file changes. See [Inbox and Review](https://tuios.dev/docs/agent-inbox) for what each part does.

### Agents that stop reporting

A hooked agent reports at each prompt and each tool call. When an agent that reported `working` sends no report for `silent_after`, tuios shows it as `unknown` with the reason `silent`. Its row reads "No report for N min".

```toml
[agents]
silent_after = "10m"   # default; "0" or "off" turns it off
```

The value is a Go duration, such as `90s` or `20m`. A value that does not parse reads as `10m`, with a warning. The daemon reads the value when it starts and when the file changes. See [Agents that stop reporting](https://tuios.dev/docs/agent-inbox#agents-that-stop-reporting).

### Card preview

<kbd>Space</kbd> on a card of the [task board](https://tuios.dev/docs/task-boards#the-card-preview) shows the card whole. `card_preview` says where:

```toml
[agents]
card_preview = "auto"     # "side", "sheet", "auto" or "off"
card_preview_width = ""   # the side panel: cells ("70") or a share ("40%"); empty is 40%
```

`side` puts it in a panel beside the columns, `sheet` over them, and `auto` beside them on a screen 140 columns or wider that keeps three columns, over them otherwise. `off` never shows it. The panel is 40% of the view by default, at least 44 and at most 96 cells. A drag on its edge sets the width, and the view keeps it. A double click on the edge gives the width back to `card_preview_width`.

### The other agent tables

```toml
[agents.approvals]
enabled = ["claude-code", "opencode"]   # harnesses whose permission prompts the Inbox may answer
hold_seconds = 120                      # 10 to 300
hold_plans = true                       # a plan follows enabled

[agents.approvals.risk]
builtin = true                          # keep the shipped risk rules
panes_may_allow = false                 # a pane with the respond grant may not allow a risky call

[[agents.approvals.risk.rule]]
name = "kubectl apply"
tools = ["Bash"]                        # empty: every tool
pattern = '\bkubectl\s+(apply|delete)\b'   # RE2, matched per command

[agents.permissions]
mode = "strict"                         # open (default) or strict
grants = ["read", "write", "fan"]       # what a pane holds under strict

[agents.recap]
mode = "toast"                          # toast, inbox or off
away = "10m"
test_patterns = ["go test", "pytest"]

[agents.queue]
max = 8                                 # at most 64

[agents.mail]
wake = true                             # type one line into an idle agent with unread mail

[agents.checkpoints]
enabled = true
keep = 50                               # at most 1000
max_untracked_mb = 50                   # a negative value means no limit
```

These options are in the [option reference](config-options.md): [`agents.approvals.enabled`](config-options.md#agents.approvals.enabled), [`agents.approvals.hold_seconds`](config-options.md#agents.approvals.hold_seconds), [`agents.approvals.hold_plans`](config-options.md#agents.approvals.hold_plans), [`agents.approvals.risk.builtin`](config-options.md#agents.approvals.risk.builtin), [`agents.approvals.risk.panes_may_allow`](config-options.md#agents.approvals.risk.panes_may_allow), [`agents.approvals.risk.rule`](config-options.md#agents.approvals.risk.rule), [`agents.permissions.mode`](config-options.md#agents.permissions.mode), [`agents.permissions.grants`](config-options.md#agents.permissions.grants), [`agents.recap.mode`](config-options.md#agents.recap.mode), [`agents.recap.away`](config-options.md#agents.recap.away), [`agents.recap.test_patterns`](config-options.md#agents.recap.test_patterns), [`agents.queue.max`](config-options.md#agents.queue.max), [`agents.mail.wake`](config-options.md#agents.mail.wake), [`agents.mail.questions_to_parent`](config-options.md#agents.mail.questions_to_parent), [`agents.checkpoints.enabled`](config-options.md#agents.checkpoints.enabled), [`agents.checkpoints.keep`](config-options.md#agents.checkpoints.keep), [`agents.checkpoints.max_untracked_mb`](config-options.md#agents.checkpoints.max_untracked_mb), [`agents.presets.NAME`](config-options.md#agents.presets.NAME).

After turning on `approvals.enabled`, run `tuios integration install` for that harness again, so its hook is the version that waits. The grants are `read`, `write`, `fan`, `respond`, `files` and `admin`. See [What a pane may do](AGENT_STATE.md#what-a-pane-may-do).

A change to `[agents.permissions]` that gives panes less applies at once. A change that gives more, such as `strict` to `open`, waits. Run `tuios config apply` from a terminal outside TUIOS, or restart the daemon. See [Changes that wait](https://tuios.dev/docs/agents#changes-that-wait).

## The `[notify]` table

`[notify]` sends a push notification when the Inbox gets an item that waits for you. The daemon sends it, so it works when no client is attached. The providers are ntfy, Pushover and a webhook.

```toml
[notify]
web_url = "https://term.example.com/"   # tuios-web, for a link to the item

[notify.ntfy]
url = "https://ntfy.sh/a-long-random-topic"
token_file = "~/.config/tuios/ntfy-token"   # optional
```

These options are in the [option reference](config-options.md): [`notify.enabled`](config-options.md#notify.enabled), [`notify.web_url`](config-options.md#notify.web_url), [`notify.content`](config-options.md#notify.content), [`notify.quiet_active_seconds`](config-options.md#notify.quiet_active_seconds), [`notify.cooldown_seconds`](config-options.md#notify.cooldown_seconds), [`notify.max_per_hour`](config-options.md#notify.max_per_hour), [`notify.allow_http_redirects`](config-options.md#notify.allow_http_redirects).

`[notify.triggers]` selects the Inbox kinds: `approval`, `plan`, `question` and `ask` are on by default, `mail`, `errored` and `finished` are off. Like `[hosts]`, `[notify]` is not in `list-options`, and `tuios set-config` cannot change it. A new address or a new source for a secret waits for `tuios config apply` from a terminal outside TUIOS. `tuios notify test` sends a test notification through each provider. See [Push Notifications](https://tuios.dev/docs/push-notifications) for the providers and the secrets.

### Web Push

`[notify.webpush]` configures the pushes the daemon sends to a phone that registered for Web Push. It needs no provider.

```toml
[notify.webpush]
subject = "https://example.com/tuios-contact"   # optional
allow_insecure = false
```

These options are in the [option reference](config-options.md): [`notify.webpush.subject`](config-options.md#notify.webpush.subject), [`notify.webpush.allow_insecure`](config-options.md#notify.webpush.allow_insecure).

See [Push Notifications](https://tuios.dev/docs/push-notifications#web-push-to-a-phone).

## Hooks: the `[hooks]` table

`[hooks]` runs shell commands on window, workspace, session and agent events. Event names use hyphens, such as `after-new-window`. See [Hooks](HOOKS.md) and `tuios list-hooks`.

## The `[links]` table

`[[links.rules]]` turns text in a pane into a link: `regex` is a regular
expression in Go syntax, and `url` is the address a match opens. `$0` is the
whole match, and `$1`, `$2` or `${name}` are its groups. A real URL wins over a
rule, and an earlier rule wins over a later one. See [Link rules](#link-rules).

## The `[mermaid]` table

`[mermaid]` sets the colours and the background of `tuios mermaid`. See
[Mermaid diagram colours](#mermaid-diagram-colours).

## The `[plugins]` table

`[plugins]` says which plugins tuios runs.

These options are in the [option reference](config-options.md): [`plugins.enabled`](config-options.md#plugins.enabled), [`plugins.dirs`](config-options.md#plugins.dirs).

See [PLUGINS.md](PLUGINS.md).
