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

tuios ignores a key that it cannot read and loads the rest of the file. The action gets its default key when it has no other key. If another action already has that default key, the action has no key. tuios shows a config problem when it starts, and the log viewer (leader `D` `l`) names each key and its file. `tuios keybinds doctor` and `tuios config apply` list these keys too.

## Keyboard layout and Option characters

```toml
[keybindings]
keyboard_layout = "us"   # us, other
option_glyphs = "auto"   # auto, alt, bind, type
```

| Key | Default | What it does |
|---|---|---|
| `keyboard_layout` | `us` | `us` lets a binding on `opt+shift+7` also match `opt+&`, as on a US keyboard. Set `other` for AZERTY, QWERTZ and other layouts. A key that the terminal reports through the Kitty keyboard protocol uses that report. |
| `option_glyphs` | `auto` | What a character that macOS Option typed does. `auto` reads the keyboard layout of the Mac when tuios starts. It reads it again when such a character arrives and the last read is more than 30 seconds old. On a US or ABC layout it runs the Option binding for the character. On other layouts the character goes to the pane. A `keyboard_layout` that you set overrides the layout of the Mac. `alt` runs the binding only when the terminal also reports Alt. `bind` always runs it. `type` always sends the character to the pane. A tuios that serves SSH or web clients uses the layout of the Mac it runs on. |

See [Shifted digits and AZERTY](KEYBINDINGS.md#shifted-digits-and-azerty) and
[One Option key for typing](KEYBINDINGS.md#one-option-key-for-typing).

## Lock mode

```toml
[keybindings]
lock = true
```

| Key | Default | What it does |
|---|---|---|
| `lock` | `false` | `true` gives every key to the pane in terminal mode, except the leader. The leader unlocks for one action, and then tuios locks again. A repeatable action keeps its menu open until you press `esc`. That menu takes only repeatable keys, and any other key goes to the pane. The zellij preset sets `true`. |

See [Lock mode](KEYBINDINGS.md#lock-mode).

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
link_hover = "safe"    # safe, always
link_opener = ""       # for example "firefox --new-tab" or "open -a Safari %s"
```

| Key | Default | What it does |
|---|---|---|
| `links` | `all` | The links that tuios finds. `marked` finds only OSC 8 links. |
| `link_click` | `both` | The click that opens a link. `both` is `Ctrl+click` and `Shift+click`. `Ctrl+click` also works on macOS, in Ghostty, kitty, WezTerm and Terminal.app. `Cmd+click` does not open a link there. |
| `link_hover` | `safe` | The hover underline and label. `safe` skips a pane whose program tracks the mouse. `always` draws them in every pane. |
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

## Pause focus follows mouse

`appearance.focus_follows_mouse = true` focuses the pane under the pointer as
the mouse moves. Set `appearance.focus_follows_mouse_pause` to hold a key that
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

`[hints]` sets what hints mode (`Ctrl+B F`) labels. See [HINTS.md](HINTS.md).

`[panes]` sets `label_keys`, the keys that the pane labels (`Ctrl+B Q`) use. The default is `1234567890`. Letters `a` to `z` and digits are allowed. See [KEYBINDINGS.md](KEYBINDINGS.md#pane-labels).

`[panes]` also sets `navigator_layout`, the layout the pane navigator (`Ctrl+B /`) opens in: `tree`, `flat` or `cards`. The default is `tree`. Press `v` in the navigator to change the layout. See [KEYBINDINGS.md](KEYBINDINGS.md#the-pane-navigator).

`[scratch]` sets the size of the box in which `Ctrl+B g` shows the scratch terminal. The old `session` key is no longer used. See [SESSIONS.md](SESSIONS.md#the-scratch-terminal).

`[pip]` sets the size (`width`, `height`, border included, default 40x12) and the first `corner` (default `bottom-right`) of the picture-in-picture view that `p` pins. See [SESSIONS.md](SESSIONS.md#picture-in-picture).

`[[keybindings.command]]` binds a key to a command that you write: a scratch terminal, a popup, a pane or a command with no window. A mistake in an entry is a warning, and tuios ignores that entry. See [KEYBINDINGS.md](KEYBINDINGS.md#command-keys).

## Animation speed

`appearance.animation_speed` is the percent window animations run at: minimize
and restore, snap, swap and zoom. 100 is the speed tuios ships with. 200 runs
them in half the time, and 50 in twice the time.

```toml
[appearance]
animation_speed = 200   # a number from 25 to 400
# animation_speed = 0   # 100, the default
```

A value outside 25 to 400 moves to the nearest end. `appearance.motion = "none"`
still turns the animations off, whatever the speed. Overlay fades and the copy
sweep keep their own timing. The copy sweep uses `appearance.selection.flash_ms`.

## Theme transition

`appearance.theme_transition` sets how tuios shows a change of theme. The
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

`appearance.animation_speed` also applies to the duration. A value outside 100
to 2000 moves to the nearest end. `appearance.motion = "none"` turns the
transition off. The `fade` transition needs a truecolor terminal. On a terminal
with fewer colors, tuios changes the colors in one frame. While the transition
runs, the screen outside the circle does not change.

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

## Images on a terminal without graphics

A program in a pane can draw a picture with sixel. Your terminal shows the
picture when it supports sixel or kitty graphics. Some terminals support
neither: kmscon, the Linux console, and many SSH clients. On these, tuios
draws the picture with block characters. Each character cell shows two
colours.

`appearance.image_symbols` sets the characters.

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

| `appearance.theme` | On a machine with Omarchy | On a machine without Omarchy |
|---|---|---|
| empty (the default) | The Omarchy theme | The colours of your terminal |
| `omarchy` | The Omarchy theme | The colours of your terminal, and a warning |
| `none` | The colours of your terminal | The colours of your terminal |
| a theme id | That theme | That theme |

```toml
[appearance]
theme = "omarchy"   # use the Omarchy theme, and warn where Omarchy is missing
```

`appearance.theme_light` names a second theme for a light terminal. `theme` is
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

Every colour key in tuios takes the same values: the backgrounds, the
border colours, `scrollbar.tint`, `sidebar.cursor_background`,
`[appearance.selection]`, the dock's `fg` and `bg` keys, `[dock.tabs]`,
the agent row's `fg`, and a theme file's `chrome` object.

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

| Option | Surface | Values | Default |
|---|---|---|---|
| `appearance.background` | Every surface below that is not set on its own | `off`, `theme`, a [colour](#colour-values) | `off` |
| `appearance.pane_background` | Pane content, the thin scrollbar over it and the scrollback browser | `off`, `theme`, a colour, or empty | empty (follows `background`) |
| `appearance.desktop_background` | Behind and between panes: gaps between tiled panes, the space around floating ones, an empty workspace and its welcome splash | `off`, `theme`, a colour, or empty | empty (follows `background`) |
| `appearance.window_chrome_background` | Pane borders and title bars, the lines between shared-border panes, the capture marquee | `off`, `theme`, a colour, or empty | empty (follows `background`) |
| `appearance.dock_background` | The dock | `off`, `theme`, a colour, or empty | empty (follows `background`) |
| `appearance.sidebar.background` | The rail | `off`, `theme`, a colour, or empty | empty (follows `background`) |

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

## Drop preview

While you drag a tiled pane in BSP, tuios shows where the pane would land if you let go. See [LAYOUT_MODES.md](LAYOUT_MODES.md). The preview is a frame in the accent colour, drawn with the same border characters as the panes. With `fill`, the area inside the frame gets one tint, and the text under it fades into that tint. On a 16-colour terminal the frame is reverse video and the area inside stays as it is. Two options shape that preview:

| Option | What it does | Values | Default |
|---|---|---|---|
| `appearance.drop_preview` | What the preview draws. `fill` draws the frame and tints the area inside it, `outline` draws only the frame, and `off` draws nothing | `fill`, `outline`, `off` | `fill` |
| `appearance.drop_preview_delay` | How long the pointer stays on one drop zone before its preview shows. A zone you leave before the delay is never drawn, so carrying a pane across the screen does not flash a zone over every pane on the way | a duration such as `120ms`, or `0` to show it at once | `120ms` |

```toml
[appearance]
drop_preview = "outline"
drop_preview_delay = "250ms"
```

Neither option changes the drop: with `off`, releasing on an edge still splits that pane, and releasing in the middle still swaps. Both are on the settings page, in the Appearance section, and `tuios set-config appearance.drop_preview off` applies at once. A value that is not one of the above reads as the default, with a warning when the config loads, and `tuios set-config` refuses it.

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
  `H`. Set `appearance.dock_mode_split_letter = false` to show the tiling icon
  alone. With the default icons, the pill then has the same width as the pill
  of the leader key, so the workspace tabs do not move when you press the
  leader key.

To remove the whole mode pill, remove `"mode"` from the `left` list in the
`[dock]` table. See [The dock's components](#the-docks-components).

You can also set the icons on the settings page, in the Dock section, or with
`tuios set-config dock_mode_icon_window WM`. The word `default` puts an icon
back to the icon of the glyph set, in the settings, with set-config and in
`config.toml`. The change applies at once.

## Prefix indicator

When you press the leader key, the mode pill shows a keyboard icon on the
warning colour until you press the next key. With `use_ascii_only`, the icon is
`P`. `appearance.dock_prefix_indicator` sets where the armed leader shows:

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

## Workspace name on hover

`appearance.dock_workspace_tooltip` shows the workspace name above a pill
when you hover it. The name shows when the pill cuts the name short. It also
shows when `appearance.dock_workspace_tab_format` has no `{name}`, for
example with number-only tabs. A workspace with no name shows its number,
but only when the tab format has no `{index}`. The option is on by default.

```toml
[appearance]
dock_workspace_tab_format = "{index}"
dock_workspace_tooltip = true
```

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

## Window titles

A pane's title sits on its border. Two options set its shape and its place:

| Option | Values | Default |
|---|---|---|
| `appearance.window_title_style` | `pill`: the name on a fill in the border's colour, with rounded caps. `tab`: the fill with no caps. `plain`: the name in the border's colour, with no fill | `pill` |
| `appearance.window_title_align` | `auto`: on the top row, the end away from the window buttons; on the bottom row, the middle. `left`, `center`, `right`: that place in the room the buttons leave | `auto` |

```toml
[appearance]
window_title_style = "plain"
window_title_align = "center"
```

`appearance.window_title_position` sets the row: `top`, `bottom` or
`hidden`. The caps of `pill` come from the glyph set. Both options are on the
settings page, in the Appearance section, and `tuios set-config` applies them
at once.

## Focused border colour

The focused pane's border shows the mode. By default it is the theme's cyan
in window mode and its green in terminal mode. Three keys set it.

| Option | Values | Default |
|---|---|---|
| `appearance.border_focused_color` | a [colour](#colour-values). It sets the border in both modes | empty |
| `appearance.border_focused_color_window` | a [colour](#colour-values). It sets the border in window mode | empty |
| `appearance.border_focused_color_terminal` | a [colour](#colour-values). It sets the border in terminal mode | empty |

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
- With `appearance.session_border` on, the focused border in window mode uses
  the session colour. In terminal mode it uses the terminal mode colour. That
  colour moves in lightness away from the session colour, so the two modes
  look different.
- At 16 colours the default borders use bright cyan and bright green, the
  slots of your terminal's palette.
- The settings page has a row for each key, in the Appearance section.

## Focused border gradient

`appearance.border_focused_gradient` draws the focused pane's border in two
or more colours that blend from one to the next.
`appearance.border_gradient_angle` sets the direction in degrees.

| Option | Values | Default |
|---|---|---|
| `appearance.border_focused_gradient` | two or more [colours](#colour-values), separated by spaces or commas. Empty draws one colour | empty |
| `appearance.border_gradient_angle` | `0` to `359`. `0` runs left to right, `90` top to bottom, `45` from the top left corner to the bottom right one | `45` |

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

The small dialogs, such as rename, confirm and new space, draw their frame in the pane border style, `appearance.border_style`. The larger panels, such as the command palette, the switchers, the Agents view and the settings page, have no frame unless `appearance.frame_panels` is on. Then they take the same frame.

| Option | Values | Default |
|---|---|---|
| `appearance.border_style` | `rounded`, `normal`, `thick`, `double`, `hidden`, `block`, `ascii`, `outer-half-block`, `inner-half-block`, `glyphs` | `rounded` |
| `appearance.frame_panels` | `true` frames every panel in the border style, `false` leaves the panels without a frame | `false` |

```toml
[appearance]
border_style = "double"
frame_panels = true
```

How each border style draws a dialog or a framed panel:

- `rounded`, `normal`, `thick`, `double` and `ascii` draw that style's corners and lines.
- `hidden` draws no frame. The dialog keeps its size, and its title and key hints stay where the frame would carry them. A frame that is the only edge of what it holds is drawn as in `rounded` instead: the frame of a panel at 16 colours, and the frames round the regions inside a panel, such as an explorer's list and preview.
- `block`, `outer-half-block` and `inner-half-block` draw the `rounded` frame. A solid frame is too heavy round a dialog of three rows.
- `glyphs` draws the border of the active glyph set. A part of the border the set does not name is drawn as in `rounded`.
- With `--ascii-only`, every frame is drawn with `+`, `-` and `|`.
- At 16 colours a panel always has a frame, because its background is the terminal's own. The frame takes the border style, except `hidden` as above.

Both options are on the settings page, in the Appearance section. A change applies at once.

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
right  = ["transfers", "notifications", "copy-help", "cpu", "ram", "clock", "session-controls"]

[dock.clock]
format = "15:04"

[dock.custom.branch]
command  = "~/.config/tuios/dock/git-branch.sh"
refresh  = "event:after-focus-change"
on-click = "tuios new-window log -- git log --oneline -20"
```

The lists above are the default: omit the whole table and the bar is unchanged.

The clock draws hours and minutes (`15:04`) unless `dock.clock.format` or
`appearance.clock_format` sets another Go time layout. To show seconds, set
`format = "15:04:05"`. A clock with seconds draws a frame every second. A
clock without seconds draws one frame a minute.
When the `center` list has a clock or a custom cell, the center is at the
middle of the terminal. A notification does not move it or shorten its
minimized pane labels. On a crowded bar, the center moves left to keep the
right-hand cells on the screen. Click a cut notification to read the full
message.
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
```

Colours take any [colour value](#colour-values). The colours are overrides:
a flat strip with none set puts the current tab on the theme's accent, in
bold, and draws the others in dim text on the bar's own ground, so it still
shows where you are. A value that is not a colour is ignored, with a
warning, as if it were unset.
Besides `{index}` and `{name}`, the tab format takes `{icon}` and `{zoom}`.
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

`appearance.sidebar.row_style` sets how the rail marks the session you are on
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

## The files section style

`appearance.sidebar.files_style` sets how the rail's files section shows a
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
`appearance.sidebar.cursor_background` sets the background of that row.

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

`appearance.sidebar.header_style` sets how a section header is drawn:

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

`appearance.sidebar.density` sets the space between the rail's sections.
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

The glyph is the active glyph set's `scrollbar_thumb` role. It is `┃` by default, `#` under `--ascii-only`, and `█` in the `heavy` set. Where the thumb would be the same glyph as the rule, as with a heavy border style, it is drawn as `█` (`#` under `--ascii-only`). `appearance.scrollbar.*` and `appearance.hide_scrollbar` style the pane scroll bar only, not the rail's thumb.

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
share gives it, with `… +N` for the ones below the fold. While some are below
the fold the section draws a scroll thumb in the rail's edge rule (see
[A rail section that overflows](#a-rail-section-that-overflows)).

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

## The pane bell

A program in a pane rings the bell when it prints the BEL character. For
example, `printf '\a'` rings it. `notifications.bell` sets what tuios does then:

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

| Key | Default | What it does |
| --- | --- | --- |
| `enabled` | `true` | Turns every notification off when `false`. Nothing is sent without a provider. |
| `web_url` | empty | The address of tuios-web. Each notification links to `web_url/inbox?item=ID`, which opens the agents view on the item. See [WEB.md](WEB.md#open-the-agents-view-from-a-notification). |
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

| Key | Default | What it does |
| --- | --- | --- |
| `subject` | `https://tuios.dev/push` | The VAPID subject: a URL that a push service can use to contact you. It must be an `https` URL or a `mailto:` URI. The push service reads it, so the default names no machine. |
| `allow_insecure` | `false` | Lets tuios send to a push service on a loopback or private address, by `https` or `http`, for a push service on your own network. A change to `true` waits for `tuios config apply`. |

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

[agents.mail]
wake = true

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

A second rail option goes with it: `appearance.sidebar.subagent_rows`, how an
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

| Option | Values | Default | What it does |
| --- | --- | --- | --- |
| `subagent_collapse` | `off`, `expanded`, `collapsed` | `off` | `expanded` and `collapsed` draw a heading line, `▾ 4 subagents`, over each agent's subagent rows. The value is the state at the start. |
| `subagent_fields` | a comma list of `mark`, `name`, `type`, `time`, `note` | `mark,name,time,note` | The parts each subagent row shows. `type` is the agent type, such as `Explore`. `note` is the second line. |
| `subagent_max_rows` | `0` to `16` | `0` | The most lines one agent's subagents use, the `+N more` line included. `0` means no limit. |
| `subagent_hide_finished` | a duration such as `30s`, or `off` | `off` | Removes the row of a finished subagent this long after it ends. |

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
| `files` | Read and write files here with the file verbs: a file's bytes, copies in and out (`transfer-start` on the other machine), new folders, moves and removes. It is apart from `write`, so you can stop one machine's copies and keep its typing. A write that arrives over the link lands only in the folders of `files_roots` (the receive folder unless you add more, see below), and the drop folder for dropped files, never in `~/.ssh`, `~/.gnupg`, credentials, shell start files, login items or tuios's own config, whatever `allow` and `files_roots` say. A read never returns keys or credentials. It also lets a window here that the other machine draws show kitty images that a program names by path (`kitten icat --transfer-mode=file`): this machine reads the file under the same read rules and `files_read_roots`, and sends the bytes. See [protocol.md](protocol.md#files-and-transfers). |

With no table, a machine may `list`, `mail`, `open`, `write` and `files`,
which is what every link could do before the policy existed. `respond` is
opt-in. Relaying on to this machine's own hosts needs the five capabilities a
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
every root, so even then it cannot touch `~/.ssh` or a shell start file. A
write outside the roots is refused, and the refusal says where writes may land
and which setting to change. A reload that widens `files_roots`, like one that
widens `allow`, waits for `tuios config apply`.

**Where a link may read: `files_read_roots`.** Unset, a machine with `files`
reads any file you can read, apart from the read deny list: keys, credentials,
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
a copy waits and tries again. The drop folder holds at most 256 MiB for all
drops together, and a machine writes there only inside a drop that
`file-drop-dir` made.

```toml
[hosts."*"]
files_min_free = "5GiB"
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
