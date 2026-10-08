# Keybindings

The keybinding reference lives on the docs site: https://tuios.dev/docs/keybindings

Every binding lives in one of the 24 sections under `[keybindings]` in `config.toml` and is rebindable; the site page lists each section's defaults, the prefix chords, copy mode, and the key syntax.

To send the leader to the program in the pane, press it two times in terminal
mode. The pane gets the leader that `leader_key` names, such as `ctrl+a`,
encoded as the pane expects: CSI u for a pane using the Kitty keyboard
protocol, legacy bytes otherwise. A leader with no legacy encoding is dropped.

The `[keybindings]` sections can also be in a file that config.toml includes,
or in a file in `config.d`. The tables merge key by key. A
`[[keybindings.command]]` entry in a later file changes the earlier entry with
the same `name`, or with the same `key` when one of them has no name. Add
`disabled = true` to an entry to remove the earlier entry it matches. The
keybind manager and `tuios keybinds unbind` write a change to the file that
sets the action. See
[Split the config into several files](CONFIGURATION.md#split-the-config-into-several-files).

To inspect your own effective bindings, use the binary rather than any document: `tuios keybinds list`, `tuios keybinds doctor` for conflicts, `tuios keybinds explain <key>` for everything one key does, or the in-app keybind manager on `Ctrl+B k`.

## The full list

`tuios keybinds list` shows every action and the keys that run it, as your
`config.toml` sets them. It has one table for each scope: global, window mode,
terminal mode, the sidebar and its files and agent rows, the Inbox, its prompt,
the mailbox, copy mode, the prefix and each prefix menu, and tape playback. A key in a
prefix menu shows with its chord, such as `ctrl+b L 5`. The command keys have a
table of their own.

After the scopes, the list shows the fixed keys. tuios reads these keys itself,
and you cannot rebind them: copy mode, hints mode, the message view, the ways
out of the spotlight, the list keys and the mouse. The help overlay
(`Ctrl+B ?`) shows the same rows. The last table shows the actions that have no
default key. Bind one in `config.toml`, or run it from the command palette.

`tuios keybinds list --json` prints the same rows as a JSON array. Each row has
`scope`, `scope_name`, `chord`, `section`, `action`, `keys` and `description`.
A fixed key has `fixed: true`. An action with no key has `unbound: true`. A key
that a different action takes first is in `shadowed`.

```sh
tuios keybinds list --json | jq -r '.[] | select(.action == "toggle_tiling") | .keys[]'
```

`tuios keybinds browse` opens the same rows in an explorer. Press `/` to
search, and `tab` or a click on a tab to show one scope. The detail pane shows
the keys, the action, the scope and the description. `q` or `esc` leaves it.
It opens only when you run `browse`. `tuios keybinds list` stays plain text,
on a terminal too.

## Editing sidebar files

Focus the sidebar with `s` in window mode or `Ctrl+B e`, then select a file.
`Enter` opens a folder or copies the path of a file. `Shift+Enter` opens a text
file in a new pane. The pane runs the editor set under Settings, Sidebar, File
editor. A folder keeps its navigation action. tuios does not edit a file that is
not text. The binding is `file_edit` in `[keybindings.sidebar_files]`.

`Y` copies the absolute path of the selected file or folder. The context menu
of a folder has a Copy path row that does the same. The binding is
`file_copy_path` in `[keybindings.sidebar_files]`.

When the session runs on another machine, tuios does not check the file on your
machine. If the editor is not on that machine, no pane opens. tuios shows a
dock note that names the editor and the machine, so you know what to check.

With the File editor setting empty, a remote session uses `$EDITOR` from this
machine. Set the File editor if the far machine has a different editor.

tuios can not edit a file that lists from a different machine than the one the
session runs on.

`Shift+Enter` needs a terminal that reports modified Enter, such as one that
uses the Kitty keyboard protocol. On other terminals, use the Edit row in the
context menu of a file, or bind `file_edit` to a different key.

`Ctrl+B f` searches file names below the directory of the focused pane. It also
works while you type in a pane. `Ctrl+F` searches below the folder that the
sidebar shows, while the sidebar has focus.
Type a name or a relative path and press `Enter`. tuios shows the sidebar, opens
the folder of that file and selects the file. The search skips `.git`,
`node_modules`, `.venv` and `target`. It stops after about 2.5 seconds, and its
title says `partial` when tuios did not search the whole tree. You can rebind
the actions in the keybind manager (`Ctrl+B k`) or in `config.toml`, for
example:

```toml
[keybindings.prefix_mode]
prefix_file_search = ["f"]

[keybindings.sidebar]
file_search = ["ctrl+f"]
```

## Modifier spellings

A key in `config.toml` can spell a modifier in more than one way. tuios reads each spelling as the same key.

| Write | tuios reads it as | Where |
|---|---|---|
| `opt+`, `option+` | `alt+` | all platforms |
| `cmd+`, `command+` | `super+` | all platforms |
| `control+` | `ctrl+` | all platforms |

The order of the modifiers does not matter, so `shift+ctrl+x` is `ctrl+shift+x`. This applies to `leader_key`, to every binding table, and to `tuios keybinds explain`, `free` and `unbind`. `tuios keybinds explain opt+f12` shows `opt+f12 (tuios reads it as alt+f12)`. `tuios keybinds doctor` lists each key that tuios cannot read.

You can use one `config.toml` on macOS and on Linux. On Linux, `opt+1` is `alt+1`. `tuios keybinds doctor` lists these keys for information.

tuios ignores a key that it cannot read, such as `ctrl+nope`, and loads the rest of the file. The action gets its default key when it has no other key. If another action already has that default key, the action has no key. A leader that tuios cannot read changes to `ctrl+b`. tuios shows a config problem when it starts. The log viewer (leader `D` `l`) names each key and its file. `tuios keybinds doctor` shows the same information.

A `super+` chord needs a terminal that sends the Super key. Most macOS terminals keep Command chords for their own menus. Ghostty and kitty send an unbound Command chord under the Kitty keyboard protocol.

## Moving focus

Four keys move focus to the window in a direction. Each key has a binding in window mode, in terminal mode, and after the prefix.

| Direction | Window mode | Terminal mode | After the prefix key |
|---|---|---|---|
| Left | `h` | `alt+left` | `left` |
| Down | `j` | `alt+down` | `down` |
| Up | `k` | `alt+up` | `up` |
| Right | `l` | `alt+right` | `right` |

Focus goes to the nearest window that lies in that direction and faces the focused window. At the edge of a tiled layout, focus stays where it is. With tiling off, a window that does not face the focused window can also get focus.

With tiling off, `h` and `l` snap the focused window to the left or right half of the screen. `j` and `k` always move focus. The actions are `snap_left`, `snap_right`, `focus_down` and `focus_up` in `[keybindings.layout]`.

In the scrolling layout, left and right move between columns. Up and down move between the windows in one column.

## Finding a pane

### Pane labels

`Ctrl+B Q` puts a large label on each pane of the workspace. Type a label to
focus that pane. `esc` closes the labels. `backspace` takes back the last key.
`q` also closes, when `q` is not a label key. The action is `display_panes`,
as in tmux.

The labels are digits by default, in the order that `select_window_1` to
`select_window_9` count the panes. Thus label `3` is the pane that `3` selects
in window mode. A minimised pane gets no label. With more panes than keys, a
label has two keys. A key with `Shift` is the same key.

Set the keys in `[panes]`. Letters `a` to `z` and digits are allowed. The keys
that come first go to the first panes.

```toml
[panes]
label_keys = "asdfghjkl"
```

When a pane is zoomed, the zoomed pane shows its label. A list under it
shows the labels of the panes that the zoom hides. A label of a hidden pane
moves the zoom to that pane. A pane that is mostly off the screen, or too
small for its label, is in the same list.

The labels close when the layout changes: a pane opens, closes, moves or
zooms, or the workspace or the session changes. A click or the mouse wheel
also closes them. In multifocus, a label key goes to the labels and
not to the panes.

### The pane navigator

`Ctrl+B /` opens the pane navigator. It shows a tree of sessions, workspaces
and panes on the left, and a preview of the highlighted row on the right. The
sessions on the machines in `[hosts]` are in the tree too. The action is
`choose_tree`, as in tmux.

| Key | What it does |
|---|---|
| `j`, `k`, arrows | Move |
| `l`, `→` | Open a session or a workspace |
| `h`, `←` | Close it, or go to its parent row |
| `space` | Open or close |
| `enter` | Go to the row: the session, the workspace and the pane |
| `/` | Search |
| `v` | Change the layout: tree, flat, cards |
| `esc`, `q` | Close the navigator |

The navigator has three boxes. Search is at the top and shows how many panes
match, as `3/7`. Panes is the list. Preview shows the screen of the pane under
the cursor, in the colors of that pane.

The list has three layouts. Tree shows the sessions, their workspaces and their
panes. Flat shows one row for each pane, with its session and workspace. Cards
shows two rows for each pane: the name and the command, then the session, the
workspace and the folder. `v` changes the layout. Your client keeps the last
layout until it closes. `[panes] navigator_layout` sets the first layout. See
[CONFIGURATION.md](CONFIGURATION.md).

A session name has the color of the session in the sidebar. A dot in front of
a pane shows the state of its agent. After the pane name is the command that
runs in the pane, or the shell when the pane is at its prompt. A pane that has
no name and no title shows its folder as its name. A search shows the matched characters in
the accent color.

Press `/` and type to search all panes. The search reads each pane's name,
title, folder, running command, session and workspace. It also reads the last
40 lines of each screen. A pane that matches only by its screen text shows the
matching line on its row. `esc` stops the search and keeps the results. A
second `esc` clears the search.

The current session's panes are read from your client, and the preview of
those panes is live. The other sessions are read when the navigator opens.
Their preview shows the screen as it was then, with its colors. The preview
shows colors and text only. Links, clipboard writes and other control sequences
from a pane do not reach your terminal. A click on a pane goes to it. A
click on a session or a workspace opens or closes it.

`tuios list-windows --all --text 40 --json` prints the same rows for a script.
See [CLI_REFERENCE.md](CLI_REFERENCE.md#tuios-list-windows).

## Neovim pane navigation

The optional [tuios-nvim-navigator](https://github.com/Tim4c/tuios-nvim-navigator)
plugin lets the terminal-mode focus keys move through Neovim splits first. The
feature is off by default. Set `appearance.nvim_navigation = true` to enable
it. At a split edge, TUIOS moves to the adjacent pane instead. The plugin
announces when it is active, so the same keys keep their normal TUIOS behaviour
in shells and other programs.

By default, both projects use `alt+left`, `alt+down`, `alt+up` and
`alt+right`. If either side is customized, its four mappings must match the
`terminal_focus_left`, `terminal_focus_down`, `terminal_focus_up` and
`terminal_focus_right` bindings in TUIOS.

The plugin uses the private OSC 7777 messages below. They are consumed by
TUIOS and never shown in pane output:

```text
OSC 7777 ; tuios-nvim-navigator ; state ; active BEL
OSC 7777 ; tuios-nvim-navigator ; state ; inactive BEL
OSC 7777 ; tuios-nvim-navigator ; focus ; <direction> BEL
```

`<direction>` is `left`, `down`, `up` or `right`. A custom Neovim integration
may emit the same messages. TUIOS accepts focus requests only from the focused
pane in terminal mode, and only immediately after it forwarded the matching
focus key to that pane. Window-management mode always keeps the focus keys for
TUIOS itself.

## Lists and panels

Every list in the TUI moves the same way: the command palette, the launcher,
the settings page, the Inbox, the mailbox, the keybind manager, the theme,
glyph and effect pickers, the session, workspace, layout and machine pickers,
the window picker, the quit and context menus, the dock and rail editors and
the tape manager.

| Keys | What it does |
|---|---|
| `up`, `down`, `ctrl+p`, `ctrl+n` | Move one row. Up on the first row goes to the last, and down on the last to the first |
| `home`, `end` | The first or last row |
| `pgup`, `pgdown` | A page up or down, stopping at the ends |
| `k`, `j`, `g`, `G` | Up, down, first and last, in a list with no filter to type into |
| `ctrl+u` | Clear a typed filter |
| wheel | Scroll the list under the pointer, stopping at the ends |

Set `appearance.wrap_lists = false` to stop at the ends instead of wrapping.
The close-session and file confirmations never wrap, so up from Cancel cannot
land on the answer that deletes.

With nothing typed, the command palette lists its commands under category
headers. The headers are not rows: the cursor steps over them. Once you type,
the commands are ranked by how well they match, and each row names its
category in a quiet column on the right.

The prefix menu (which-key) shows the keys after the leader in sections
(Windows, Panes, Sessions, Modes, Menus, Tools, and Agents once an agent has
run), laid out in as many columns as the screen holds. A key marked with `+`
opens a further menu. On a screen too narrow for every column, the
descriptions are cut before any key is left out.

The key hints at the foot of a panel stay on one row. When they do not fit,
they shorten in steps. First, `ctrl+` becomes `^`, `alt+` becomes `M-` and
`shift+` becomes `S-`. Then the least important hints go, one whole hint at a
time. The way out, such as `esc`, stays. Only when the most important hints
still do not fit do their labels go, from the last hint back. A hint is never
cut in the middle of a word.

### Mode keys in the dock

Copy mode, multi copy mode and hints mode show their keys at the right end of
the dock while the mode is open. The keys follow the rule for panel footers
above, so a narrow screen shows fewer keys and never part of one. The main
action and the way out of the mode always show. In hints mode, `?` shows all of
its keys in the help. A message that arrives while a mode is open shows in the
place of the keys until the message goes. The dock component for the keys is
`copy-help`.

An empty list says why it is empty in the middle of the panel, with the one key
worth pressing next under it. A list that is still loading draws nothing for
its first half second, so a fast load never flashes a "reading" line.

## Messages

A message shows at the right end of the dock. A message that is too long
for the dock stops after its last whole word, with `…` and `more`. A word with
no space in it, such as a long path, is cut where the room ends.

- Put the pointer on a message to hold it. It does not go away while the
  pointer is on it. A key press, or the terminal losing focus, ends the hold.
  The hold ends after 60 seconds.
- Put the pointer on a long message to see its first lines above the dock.
- Click a long message to read all of it in the message view. A click on a
  pane's request to set the clipboard allows it.
- Click a short message from a pane to go to that pane.

| Keys | Where | What it does |
|---|---|---|
| `ctrl+b N` | anywhere | Show the last message from a pane, or the last message that was too long for the dock, also after it went from the dock |
| `ctrl+b j` | anywhere | Go to the pane of the newest message |
| `j`, `k`, `up`, `down`, wheel | message view | Scroll one line |
| `ctrl+d`, `ctrl+u`, `space`, `pgdown`, `pgup` | message view | Scroll half a page |
| `g`, `G`, `home`, `end` | message view | Go to the first or last line |
| `y` | message view | Copy the message |
| `enter` | message view | Go to the pane the message came from |
| `esc`, `q` | message view | Close the message view |

The log viewer (`ctrl+b D l`) lists every message the dock showed, and every
warning and error. The entry under the cursor shows wrapped under the list.
Press `enter`, or click an entry, to read it in the message view. `E` copies
the errors and `A` copies the whole log.

## Hints

`Ctrl+B F` puts a short label on each URL, path, hash, address and number in
the focused pane. Type a label to copy the text. Type it with `Shift` to copy
the text and type it into the pane. Type it with `Ctrl` to open a URL or a
path. `?` shows all of the keys of hints mode. `esc` closes. The dock shows
the keys while the labels show. See [HINTS.md](HINTS.md) for the patterns and the
`[hints]` settings. The action is `hints`, so you can bind it to a different
key.

The `hints_all_panes` action puts labels on all panes that the workspace
shows. The focused pane gets the shortest labels. `Shift` and a label types
the text into the focused pane. This action has no default key. Bind it, or
run it from the command palette. Set `hints.all_panes = true` to make
`Ctrl+B F` do the same.

## Links

`Ctrl+click` on a link opens it. `Shift+click` also opens it, but most
terminals keep `Shift+click` for their own selection. Hover a link to see
where it goes. The label under the pointer shows the real target, also when
the text on the screen is different.

`Ctrl+click` and drag still moves the pane. A link opens only when the
pointer does not move. `appearance.link_click` sets which click opens a
link. See [CONFIGURATION.md](CONFIGURATION.md#opening-links) for the
settings, the opener and a table of terminals.

## Scratch terminal

`Ctrl+B g` shows the scratch terminal, a small layout of panes in a box over
the current layout. Press it again to hide the box. The shells keep running
between shows. Inside the box the window keys act on its panes: split, move
the focus, resize, swap, zoom and close. The key works from inside the box.
The action is `toggle_scratch`, and `[scratch]` sets the size of the box. See
[SESSIONS.md](SESSIONS.md#the-scratch-terminal). To add more scratch terminals
on keys of their own, see [Command keys](#command-keys).

## Picture in picture

`p` in window mode pins the focused pane as the picture-in-picture view, a
small live copy of the pane in a corner of the screen. Press `p` again, on any
pane, to unpin it. A click on the view focuses the pane. The action is
`toggle_pip`, and `[pip]` sets the size and the corner. See
[SESSIONS.md](SESSIONS.md#picture-in-picture).

## Spotlight

`B` in window mode turns the spotlight on and off. The spotlight dims the
screen outside a circle around the pointer. `Ctrl+B B` turns it on and off in
any mode.

While the spotlight is on, the dock shows a Spotlight chip with the key that
turns it off:

| Where | Turn off the spotlight |
| --- | --- |
| Window mode | `Esc` |
| Terminal mode | `Ctrl+B B` |
| Any mode | Click the Spotlight chip in the dock |

In terminal mode `Esc` goes to the program in the pane, so vim and other
programs get it. An open dialog, menu or popup closes before `Esc` turns the
spotlight off. The actions are `toggle_spotlight` and
`prefix_toggle_spotlight`, and `[spotlight]` sets the size and the dimming.
The key was `b` in earlier versions.

## Paste an image

`Ctrl+B V` pastes the image on your clipboard into the focused pane. tuios
saves the image to a file on the machine where the pane runs. Then it pastes
the path of that file into the pane. The pane can run on this machine or on a
host. Claude Code, Codex and other agents read an image from a path in the
prompt. The action is `paste_image`, and the command palette has it too.

The paste key also pastes an image. The paste key is `Ctrl+Shift+V`, `Super+V`
or `paste_clipboard`. When the clipboard holds an image and no text, the paste
key pastes the image. When the clipboard holds text, the paste key pastes the
text. Some terminals send an empty paste when the clipboard holds only an
image. tuios then pastes the image.

A browser can put a link or text beside a copied image. The paste key then
pastes the text. `Ctrl+B V` always pastes the image.

The paste key waits at most 0.3 seconds for the list of clipboard types. When
the clipboard tool is slower, or cannot list the clipboard, the paste key
pastes text. It then does not ask the tool again until tuios restarts.

`Ctrl+V` alone goes to the program in the pane. Claude Code reads the
clipboard on `Ctrl+V` itself, and that only works in a pane on this machine.
For a pane on a host, use `Ctrl+B V`.

tuios reads the image with the clipboard tool of your system:

| System | Tool |
| --- | --- |
| Linux, Wayland | `wl-paste` from wl-clipboard |
| Linux, X11 | `xclip`. `xsel` cannot read an image. |
| macOS | `osascript`, or `pngpaste` when it is installed |
| Windows | PowerShell |

The file is a PNG, JPEG, GIF, WebP, BMP or TIFF image of 8 MB or less. Only
you can read it. tuios deletes it after one hour, or when the daemon stops.
The file is in the `paste` folder next to the tuios socket. The folder keeps
at most 50 images and 100 MB. When it is full, tuios deletes the oldest image.
If the `paste` folder is a symbolic link, tuios does not paste.

tuios does not read the clipboard in these cases:

- The client runs in a browser. Save the image to a file and paste the path.
- The client runs on another machine through `tuios ssh`.
- The client runs inside ssh. The clipboard there is the other machine's.
  An X display that `ssh -X` forwards is the exception: tuios reads it.
- The paste comes from `send-keys`.

## Command keys

A `[[keybindings.command]]` entry binds a key to a command that you write.

```toml
[[keybindings.command]]
key = "prefix+alt+g"
type = "scratch"
command = "lazygit"
description = "Lazygit"
width = "80%"
height = "80%"
```

- `key` is the key that runs the command. A key that starts with `prefix+`
  works after the leader (`Ctrl+B`). Any other key works in window mode and
  in terminal mode.
- `type` is one of these:
  - `scratch`: a scratch terminal of its own. The key shows it and hides it,
    as `Ctrl+B g` does for the built-in one. Each entry has its own layout:
    you can split it. One scratch terminal is on the screen at a time. When
    the command exits, its pane closes. When no pane is left, the next press
    starts the command again. With no `command`, the first pane runs your
    shell.
  - `popup`: a popup that closes when the command exits. This is the default.
  - `pane`: a new pane in the layout, next to the focused pane. It closes when
    the command exits.
  - `shell`: the command runs with no window. You do not see its output. When
    the command fails, the dock shows a message.
- `command` runs with `sh -c`, so you can write pipes and quotes. It starts in
  the folder of the focused pane.
- `description` is the name in the command palette and in
  `tuios keybinds list`. It is optional.
- `name` keeps a scratch terminal under a fixed name. It is optional. Without it,
  tuios makes the name from the description or the command. tuios uses only
  the first 40 characters, so two entries can get the same name. tuios then
  ignores the second entry, and `tuios keybinds doctor` shows it. Give one of
  the two entries a `name`.
- `width` and `height` set the size of a scratch or popup entry, in cells
  (`100`) or percent (`80%`). The default is `80%`.

The command gets these variables:

| Variable | Value |
|---|---|
| `TUIOS_SESSION` | The session in which you pressed the key |
| `TUIOS_SOCKET` | The socket of the daemon |
| `TUIOS_ACTIVE_PANE_ID` | The pane that had the focus |
| `TUIOS_ACTIVE_PANE_CWD` | The folder in which the command starts |

A command key never takes a key from a tuios action or from the leader key.
When the key is taken, `tuios keybinds doctor` shows the entry as dead. When
two entries have the same key, the first entry in `config.toml` runs, and the
doctor shows the other as dead. To move an entry to a different key, edit its
`key` in `config.toml`. The command palette lists each entry, and
`tuios keybinds list` shows them under Commands.

A key with no modifier and no `prefix+`, for example `g`, works in terminal
mode too. It takes that letter from every pane, so tuios shows a warning. Use
`prefix+g` or a key with a modifier, for example `alt+g`.

When a scratch command stops in its first moments, for example because the
program is not installed, the dock shows its exit code. The next press starts
it again. tuios does not start it again by itself.

The command and the palette row of an entry change when you save
`config.toml`. A changed key works at the next key press, as for the other
keybindings. When you remove or rename an entry, or change the description of
an entry that has no `name`, the panes of its scratch terminal close at the
next reload. The
dock shows a message.

In a session on a different machine, a `scratch` or `popup` entry does not
run. The dock shows a message. A `pane` entry runs on the machine of the
session. A `shell` entry runs on the machine that runs the tuios client.

Only a key press or the command palette runs a command key. A change to
`config.toml` does not run a command. A pane that has the respond grant can
press keys as you through `run-command`. It can press a command key, also from
a session on a different machine, and a `shell` entry then runs on this
machine. Give the respond grant only to a pane that you trust.

### Recipes

These are command keys for common tasks. Copy an entry into `config.toml` and
change the key to taste. Run `tuios keybinds doctor` after you add one, to
check the key is free.

**Per-session notes.** This scratch entry opens `$EDITOR` on a notes file
named after the session, and makes the notes folder first. It falls back to
`vi` when `$EDITOR` is not set.

```toml
[[keybindings.command]]
key = "prefix+alt+n"
type = "scratch"
command = "mkdir -p ~/notes && ${EDITOR:-vi} ~/notes/$TUIOS_SESSION.md"
description = "Session notes"
```

**Browser pane.** This scratch entry opens a page in terminal-browser. It
needs terminal-browser installed, and a terminal with kitty graphics.

```toml
[[keybindings.command]]
key = "prefix+alt+b"
type = "scratch"
command = "terminal-browser open https://example.com"
description = "Browser"
```

**lazygit.** This is the example at the top of this section. It opens lazygit
in the focused pane's folder.

```toml
[[keybindings.command]]
key = "prefix+alt+g"
type = "scratch"
command = "lazygit"
description = "Lazygit"
width = "80%"
height = "80%"
```

**Pick a file into the focused pane.** This popup runs `fzf` and types the
pick into the pane you came from, with
[`tuios send-text`](CLI_REFERENCE.md#tuios-send-text) and the pane id from
`TUIOS_ACTIVE_PANE_ID`.

```toml
[[keybindings.command]]
key = "prefix+alt+p"
type = "popup"
command = "tuios send-text -w \"$TUIOS_ACTIVE_PANE_ID\" \"$(fzf)\""
description = "Pick a file"
```

**Open a project.** This popup runs `fzf` over the folders in `~/dev` and
switches to the session of the folder you pick. When the session does not
exist, [`tuios switch-session`](CLI_REFERENCE.md#tuios-switch-session) makes
it in that folder. See [Sessionizer](SESSIONS.md#sessionizer).

```toml
[[keybindings.command]]
key = "prefix+alt+s"
type = "popup"
command = 'dir=$(find ~/dev -mindepth 1 -maxdepth 1 -type d | fzf) && tuios switch-session --create --cwd "$dir" "$(basename "$dir")"'
description = "Open a project"
```

**Copy the pane's folder.** This shell entry copies the focused pane's folder
to the clipboard. It tries `pbcopy`, then `wl-copy`, then `xclip`, and uses the
first one it finds.

```toml
[[keybindings.command]]
key = "prefix+alt+y"
type = "shell"
command = "printf %s \"$TUIOS_ACTIVE_PANE_CWD\" | (pbcopy || wl-copy || xclip -selection clipboard)"
description = "Copy pane folder"
```

## Master-stack layout

The layout prefix (`Ctrl+B L`) has keys for the master-stack layout. They act
on the workspace on screen.

| Keys | Action | What it does |
|---|---|---|
| `Ctrl+B L o` | `cycle_master_position` | Move the master panes to the next side: left, right, top, bottom, center |
| `Ctrl+B L Enter` | `swap_with_master` | Swap the focused pane with the master pane |
| `Ctrl+B L m` | `focus_master` | Focus the master pane |
| `Ctrl+B L i` | `add_master` | Make one more pane a master pane |
| `Ctrl+B L d` | `remove_master` | Make one pane fewer a master pane |
| `Ctrl+B =`, or `=` in window mode | `prefix_equalize_splits`, `equalize_splits` | Give the master panes and the stack their default sizes again |

The resize keys move the divider between the master panes and the stack. With
the master on the left, the right or in the center, the width keys move it,
such as `<` and `>` in window mode. With the master at the top or the bottom,
the height keys move it. The workspace keeps the new size.

`set_master_position_left`, `set_master_position_right`,
`set_master_position_top`, `set_master_position_bottom` and
`set_master_position_center` have no default key. To bind one, add it to a
section:

```toml
[keybindings.layout_prefix]
set_master_position_center = ["c"]
```

See [LAYOUT_MODES.md](LAYOUT_MODES.md#master-stack-layout).

## Close every pane on a workspace

The `close_workspace` action closes every pane on the current workspace. It is
the same as tmux `kill-window`. Use it to close the panes that `tuios xpanes`
opened. The action has no default key. The command palette has the entry
"Close workspace". To bind it, add it to a section. This example uses
`Ctrl+B Alt+X`, because `Ctrl+B X` closes the session:

```toml
[keybindings.prefix_mode]
close_workspace = ["alt+x"]
```

The action asks first. The dialog shows how many panes close and how many
agents are in them. `Cancel` is the default row. Scratch panes stay open.
`tuios close-workspace` does the same from a shell. See
[CLI_REFERENCE.md](CLI_REFERENCE.md#tuios-close-workspace).

## Split into the same ssh

These actions open a pane on the machine that the focused pane is connected to
with ssh:

| Action | What it does |
| --- | --- |
| `split_ssh_horizontal` | Splits the pane top and bottom. |
| `split_ssh_vertical` | Splits the pane left and right. |
| `new_window_ssh` | Opens a new window. |

The new pane runs ssh again, to the same destination as the same user, with the connection options only.
tuios does not run the remote command again. It also removes `-N`, `-f`, `-T`,
`-W`, port forwards and the other options that stop a shell. `mosh` works the
same way.

When the focused pane does not run ssh, the action opens an ordinary pane. You
can use the keys in every pane. Some ssh lines also get an ordinary pane. See
[CONFIGURATION.md](CONFIGURATION.md#lines-that-are-not-followed).

The actions have no default key. The command palette has an entry for each
one. To bind them, add them to a section. This example uses keys for
window-management mode:

```toml
[keybindings.layout]
split_ssh_vertical = ["alt+v"]
split_ssh_horizontal = ["alt+s"]
new_window_ssh = ["alt+w"]
```

To make the ordinary split and new-window keys do the same, set
`appearance.new_window_follow_ssh`. To start the new pane in the remote
folder, make the remote shell report its folder. See
[CONFIGURATION.md](CONFIGURATION.md#splits-that-follow-ssh).

## Copy mode

`Ctrl+B [` starts copy mode on the focused pane. The copy cursor starts on the
terminal cursor, which is usually the prompt line. tmux does the same. To start
in the middle row, set `appearance.selection.copy_entry` to `center`.

| Key | Action |
| --- | --- |
| `/` | Search forward, down to the newest output |
| `?` | Search backward, up into the scrollback |
| `n` | Go to the next match in the direction of the last search |
| `N` | Go to the next match in the opposite direction |
| `Esc` in the search prompt | Cancel the search and move the cursor back |

The prompt shows `/` or `?` to show the direction. The search starts at the
copy cursor. When no match is found in that direction, the search continues
from the other end of the buffer. `n` and `N` start at the copy cursor, so they
find the nearest match after you move the cursor.

Two actions start copy mode and open the search prompt with one key:

| Action | Key |
| --- | --- |
| `copy_mode_search_forward` | None |
| `copy_mode_search_backward` | None |

They are the same as tmux `bind-key b copy-mode \; send-keys ?`. They have no
default key. Bind one in any section. This example uses `Ctrl+B /`:

```toml
[keybindings.prefix_mode]
copy_mode_search_backward = ["/"]
```

The command palette also has the entries "Copy mode: search forward" and
"Copy mode: search backward". If the pane is already in copy mode, the action
opens the prompt and does not move the cursor. In multi copy mode, the prompt
opens in each pane of the mode.

In copy mode, a key in the `global`, `terminal_mode` or `window_management`
section starts the action. A key under the leader does not start it, because
copy mode uses `Ctrl+B` for page up. In copy mode, `/` and `?` open the same
prompts.

When a search finds more than 1000 matches, the prompt shows `1000+`.

### Line start and line end

`Home` moves the copy cursor to the start of the line, as `0` does. `End`
moves it to the end of the line, as `$` does. Both keys also move the end of a
`v` or `V` selection.

| Action | Default key |
| --- | --- |
| `copy_mode_line_start` | `Home` |
| `copy_mode_line_end` | `End` |

The keys are in the `copy_mode` section. They work only in copy mode. In the
search prompt, they do not move the cursor. `0` and `$` are fixed keys, and
you cannot rebind them.

Do not bind a key here that copy mode already uses, such as `y` or `v`, or a
key of a `[[keybindings.copy_pipe]]` entry. `tuios keybinds doctor` and the
config check warn about such a key. A copy pipe key runs the pipe, and the
binding does not run. A key that copy mode uses loses its copy mode action.

`Ctrl+A` and `Ctrl+E` are not defaults. `Ctrl+A` is a common leader key, and
`Ctrl+E` scrolls one line in tmux copy mode. To use them, add them to the
section:

```toml
[keybindings.copy_mode]
copy_mode_line_start = ["home", "ctrl+a"]
copy_mode_line_end = ["end", "ctrl+e"]
```

tuios cannot put two actions on one key. `tuios send-keys` cannot do it either,
because a key from `send-keys` does not go to copy mode.

### Pipe a yank through a command

A yank can go through a command before it goes to the clipboard. tmux calls
this `copy-command` and `copy-pipe`. tuios has two parts:

- `appearance.selection.copy_command` runs one command on every yank in copy
  mode.
- A `[[keybindings.copy_pipe]]` entry binds a key in copy mode to its own
  command.

```toml
[appearance.selection]
copy_command = "tmux-copy-it"

[[keybindings.copy_pipe]]
key = "p"
command = "tr '\n' ' '"
cancel = true
description = "flatten"
```

The command runs with `sh -c`. It gets the selection on stdin. The clipboard
gets what the command writes to stdout. When the selection does not end with
a new line, tuios removes one new line from the end of the output.

The clipboard gets the selection itself in these cases:

- The command writes nothing. Use this for a command that sends the text to a
  socket or a file.
- The command stops with an exit code that is not 0. The dock shows the exit
  code and the first line of stderr.
- The command runs for more than 10 seconds. tuios stops the command and the
  processes that it started.

tuios waits until the command closes its stdout. `xclip` starts a process that
stays open after `xclip` stops, and keeps stdout open. tuios then waits 1
second more. To prevent the wait, send the stdout of `xclip` to `/dev/null`.
This example also puts each yank in the X primary selection:

```toml
[appearance.selection]
copy_command = "xclip -selection primary >/dev/null"
```

The fields of a `[[keybindings.copy_pipe]]` entry:

- `key` is the key in copy mode. Write it as in the other sections.
- `command` gets the selection on stdin.
- `cancel` says what happens after the yank. With `false`, the default, copy
  mode stays on and the cursor stays where it is. tuios clears the selection.
  This is tmux `copy-pipe`. With `true`, copy mode stops, as with `q`. This is tmux
  `copy-pipe-and-cancel`.
- `description` names the entry in the dock messages. It is optional.

The entry takes its key from copy mode. An entry on `y` replaces the plain
yank. Copy mode does not use `p` or `P`. When an entry takes `q`, `Esc`, `v`, `V`, `/`,
`?` or a digit, tuios shows a warning when it loads the config. When no text is selected, the key
shows a message and runs nothing. While you type a search, the key goes into
the search.

Without `copy_command`, `y` copies the selection as it is. With
`copy_command`, `y` and `c` pipe the selection through that command. A
`[[keybindings.copy_pipe]]` key always uses its own command. A mouse
selection that copies on release does not use `copy_command`. A drag does not
run a command.

In multi copy mode, the command gets the text that `y` copies: the selection
of each pane, joined in the current format. It runs one time for all panes.
Use the `json` format to process each pane separately, for example with `jq`.
With `cancel = true`, multi copy mode stops in every pane.

The command runs on the machine that runs the tuios client. That is the same
machine as for a `shell` command key. For the SSH and web clients, it is the
server. The command starts in the folder of the focused pane and gets the
variables of a command key: `TUIOS_SESSION`, `TUIOS_SOCKET`,
`TUIOS_ACTIVE_PANE_ID` and `TUIOS_ACTIVE_PANE_CWD`. You can use tuios while
the command runs.

### Paste buffers

tuios keeps your recent yanks as paste buffers, as tmux does. A yank in copy
mode adds a buffer, and so does a mouse selection that you copy. The yank also
goes to the clipboard, as before.

| Key | Action | What it does |
| --- | --- | --- |
| `Ctrl+B ]` | `paste_buffer` | Paste the newest buffer into the focused pane |
| `Ctrl+B #` | `choose_buffer` | Show the buffers, newest first, to choose one |

`Ctrl+B ]` takes the newest of your own buffers: what you copied, or set
yourself. A buffer that a program in a pane without `admin` set is never your
newest, so no such program can set what the key pastes. The paste goes to the pane that was focused when you pressed
the key, also when the focus moves before the text arrives. Each line feed
becomes a carriage return, as in tmux.

In the list, `Enter` or a click pastes the buffer, `d` deletes it, and `Esc`
or `q` closes the list. The list shows "from pane" and the pane's name on a
buffer that a program in a pane set, because you did not copy that text. A paste goes
in the bracketed paste marks when the program in the pane asks for them, as a
clipboard paste does.

tmux shows the list on `=`. In tuios, `Ctrl+B =` makes the splits equal, so
the list is on `#`, the tmux key for `list-buffers`. To use the tmux keys, move
the splits key and give `=` to the list:

```toml
[keybindings.prefix_mode]
prefix_equalize_splits = ["E"]
choose_buffer = ["="]
```

The daemon keeps the buffers, so every client and every session shares them.
A client with no daemon keeps its own. The buffers are in memory only, and
they go when the daemon stops. `[paste_buffers]` in `config.toml` sets how
many to keep. The commands `tuios list-buffers`, `show-buffer`, `set-buffer`,
`delete-buffer` and `paste-buffer` read and change the same buffers, and so
does `tmux` under the tmux shim.

## Screenshots over a panel

`Ctrl+B C` opens capture mode over any panel or overlay too: the Inbox, the
review, the palette, settings, a menu or a dialog. The panel stays open and is
part of the capture. The panes are under it, so capture mode does not offer
one: `enter`, `f` or a click takes the whole screen, and a drag takes a region
of it.

While a panel is open, the leader is held for one key. If that key is not the
screenshot key, the panel gets the leader and then the key, in that order, as
it did before. Copy mode and the scrollback browser keep the leader at once,
because both page up on `ctrl+b`.

## Settings

`,` in window-management mode, or `ctrl+b ,`, opens the settings page. It
reopens on the tab, row and search it was left on.

| Keys | What it does |
|---|---|
| `left`, `right`, `h`, `l` | Change the row's value |
| `enter`, `space` | Toggle, cycle, or open the row's picker or editor |
| `tab`, `shift+tab`, `[`, `]` | Next or previous tab, wrapping |
| `1` to `9` | Go to that tab |
| `/`, or a letter the page does not use | Search every tab |
| `backspace`, `delete` | Reset the row to its default |
| `ctrl+z` | Undo the last change made on the page |
| `esc`, `q` | Close |

A row changed from its default carries a dot after its name, and its
description says what the default is.

The search ranks every row of every tab by its name, its config key, its value
and its description, with the letters that matched lit, and each result names
its tab. In the search, `up` and `down` move through the results, `left`,
`right` and `enter` act on the row where it is, `tab` goes to the row on its
own tab, `delete` resets it, and `esc` clears the search and puts the page back
where it was; a second `esc` closes it. The command palette reaches the same
rows by name (`settings: pane background`), and `tuios list-options --search`
runs the same search from a shell.

## The Inbox

Everything waiting for you in every session is one list, the Inbox. See
[AGENT_STATE.md](AGENT_STATE.md#the-inbox) for what goes in it.

| Keys | What it does |
|---|---|
| `ctrl+b i` | Open the Inbox |
| `ctrl+b o` | Go to the oldest item that needs you; `o` again, inside the repeat window, goes to the next |
| `ctrl+b M` | Open the Inbox on its mail (`m` there opens the whole mailbox) |

Inside it: `j` and `k` move, `enter` goes to the pane, `space` reads an
approval's or a question's prompt so you can answer it there, `r` replies to
mail, or to the agent of a finished or errored item, `y` resumes a conversation a restart left, `p` passes held mail on, `d`
dismisses, `f` steps through the kinds, `/` types a selector that narrows the
list (such as `harness:codex needs:you`; `enter` applies it, an empty line
clears it), `m` opens the mailbox, `esc` closes. `ctrl+b o` is `o` because
`ctrl+b a` is the launcher's.

The footer offers the keys that act on the row under the cursor, the one that
answers it first: `space answer` on an approval or a question, `r reply` on
mail and on a finished or errored item, `y resume` on a resume row. It offers `m mailbox` on a mail row, though
`m` works on every row.

In the mailbox: `j` and `k` move, `enter` opens a thread, `n` writes a new
message, `esc` closes. `n` opens a list of the agents in this session. Choose
one with `enter`, type the message, and press `enter` to send it. In an open
thread, `r` replies, `o` goes to the pane that last wrote, and `esc` goes back
to the list.

In the prompt `space` opens: a digit chooses that option, `a` approves, `A`
approves and does not ask again, `d` denies, `tab` types an answer, `r` reads
the prompt again, `enter` goes to the pane, `esc` goes back to the list. A
prompt with numbered options offers its digits in the footer and not `a`, `A`
and `d`, which still work. See
[Answering a prompt without attaching](AGENT_STATE.md#answering-a-prompt-without-attaching).

These keys are in three sections of their own, rebindable like any other:
`[keybindings.inbox]` (the list: `inbox_down`, `inbox_up`, `inbox_page_down`,
`inbox_page_up`, `inbox_first`, `inbox_last`, `inbox_go`, `inbox_peek`,
`inbox_dismiss`, `inbox_reply`, `inbox_resume`, `inbox_pass_on`,
`inbox_filter`, `inbox_select`, `inbox_mailbox`, `inbox_close`),
`[keybindings.inbox_peek]` (the prompt: `peek_approve`, `peek_approve_always`,
`peek_deny`, `peek_type`, `peek_read_again`, `peek_go`, `peek_back`) and
`[keybindings.mail]` (the mailbox: `mail_down`, `mail_up`, `mail_page_down`,
`mail_page_up`, `mail_open`, `mail_reply`, `mail_focus_pane`, `mail_new`,
`mail_back`).
The footers name whatever key the config binds. The digits `1` to `9` are not
bindings: they pick an answer by the number the prompt shows. The selector
line and the reply, message and answer lines take text, so every key there is
typed.

```toml
[keybindings.inbox]
inbox_dismiss = ["x"]
```

The help overlay (`ctrl+b ?`) has an Agents section with all of these, the
prefix chords, the rail's agent keys and the palette's `@` filter, read from
your config. In the command palette the agent actions are named "Agents: ...",
so typing `agent` lists them: the Inbox, the Inbox on its mail, the oldest
waiting item, and the mailbox.

On an approval the Inbox is holding (`[agents.approvals]`, see
[AGENT_STATE.md](AGENT_STATE.md#approvals-from-the-inbox)), `1` allows it
once, `2` always allows it and `3` denies it; `enter` gives the prompt back to
the pane. The keys act on the item under the cursor, whose whole prompt, and
the rules `2` adds, are shown under the list, and only once it has been on
screen as it is for 0.4 seconds. `space` does not open a held approval: the
hook keeps its prompt off the pane until the Inbox answers, so there is
nothing on the screen to read.

### Review, triage and replies

These keys are bound for the agent review, triage, reply and approval work.
The triage keys work: `ctrl+b O`, `z`, `u` and `S` in the Inbox, and `u` and
`z` on a rail agent row (see
[Snoozing, undo and unread](AGENT_STATE.md#snoozing-undo-and-unread)), and so
do the approval keys: `n`, `J`, `K`, `ctrl+d` and `ctrl+u` in the Inbox (see
[Deny with a reason](AGENT_STATE.md#deny-with-a-reason)), and the reply keys:
`r` in the Inbox on a finished or errored item, and `r` and `x` on a rail
agent row (see [Replying to an agent](AGENT_STATE.md#replying-to-an-agent)),
and the review keys: `ctrl+b v`, and `v` in the Inbox and on a rail agent row
(see [The review overlay](#the-review-overlay) below). `ctrl+b v` works
whether or not an agent has been seen, since calling it is an explicit act;
on a pane with no git repository under it, the dock says so and nothing
opens. `ctrl+b O` does what an unbound key does until an agent has been seen:
after `ctrl+b` in terminal mode, the key is typed into the focused pane, so a
person who runs no agents keeps typing `O` into the pane. The prefix menu and
the help overlay list them only once an agent has been seen, like the rest of
the Agents section. Attached to a daemon without `mark-attention` (an older
one, found by asking its `list-verbs` once per attach), the Inbox's `z`, `u`
and `S` and the rail's `z` are not offered and do what an unbound key does,
and the rail's `u` clears only this client's seen marks. Attached to a daemon
without `review-diff`, found the same way, `ctrl+b v` and the two `v` keys are
not offered and do what an unbound key does: after `ctrl+b` in terminal mode,
`v` is typed into the focused pane.

| Keys | Where | What it does |
| --- | --- | --- |
| `ctrl+b v` | anywhere | Review the focused pane's changes |
| `ctrl+b O` | anywhere | Go to the newest finished turn nobody has seen; `O` again, inside the repeat window, goes to the next older one, and a turn that finishes meanwhile starts over |
| `ctrl+b A` | anywhere | Open the Agents tab of the settings page, where you install, update and uninstall each harness's integration |
| `v` | Inbox | Review the changes in the item's pane |
| `z`, then `1` to `4` | Inbox | Snooze the item: 15 minutes, 1 hour, until 9:00 tomorrow, or until it changes; any other key cancels. On a snoozed item, wake it |
| `u` | Inbox | Undo the last dismiss or snooze, within 10 seconds |
| `S` | Inbox | Show or hide snoozed items |
| `n` | Inbox | Deny a held approval, or keep a plan planning, with a reason you type (`3` stays the plain deny) |
| `J`, `K`, `ctrl+d`, `ctrl+u` | Inbox | Scroll the detail under the list, such as a long plan |
| `u` | rail agent row | Mark the pane's finished turn unread, for every client (not the pane in front of you) |
| `z` | rail agent row | Snooze the pane's Inbox item: the Inbox opens on it with the four lengths |
| `enter` | rail `+N at rest` line | Show the agent rows folded as at rest, until the rail lets go of the keyboard (after a click with the rail not focused, until a click outside the rail or a pane is focused) |
| `r` | Inbox, on a finished or errored item | Reply to the agent: a line under the list, queued with `enter` and typed when the agent is at rest |
| `r` | rail agent row | Reply to the agent, the same line in the Inbox; refused while the pane waits on a prompt |
| `v` | rail agent row | Review the pane's changes |
| `x` | rail agent row with messages queued | Drop the newest queued message still waiting; `u` on the row within 10 seconds queues it again. On a row with nothing queued, `x` opens the rail's menu as before. From `send-keys` or a tape, neither `x` nor the undo touches the queue, since both act as the person |

On a risky approval (one a [risk rule](AGENT_STATE.md#risk-rules) matched),
`1` and `2` allow only on a second press of the same key within 3 seconds, and
so do `a`, `A` and a digit in the peek; any other key resets the first press.
The line the first press shows names the time it lapses.
On a plan, `1` approves, `2` approves and accepts edits for the session, and
`3` keeps it planning; `1` and `2` work once the plan's last line has been
shown. The digits are not bindings.

The Inbox's keys are `inbox_review`, `inbox_snooze`, `inbox_undo`,
`inbox_show_snoozed`, `inbox_deny_reason`, `inbox_detail_down` and
`inbox_detail_up` in `[keybindings.inbox]`, and the prefix chords are
`prefix_review`, `prefix_next_finished` and `prefix_agents_settings` in
`[keybindings.prefix_mode]`.
The agent rows' keys are a section of their own,
`[keybindings.sidebar_agents]` (`agent_unread`, `agent_snooze`,
`agent_reply`, `agent_review`, `agent_cancel_queued`). It is consulted before
the rail's own keys and only while the cursor is on an agent row, the way
`[keybindings.sidebar_files]` is on a file row, so `r` and `x` mean the agent
on an agent row and keep renaming and opening the menu on every other row.

In the reply line every printable key is typed, and a paste is typed as one
line; `enter` queues it, `backspace` deletes, `esc` closes it and sends
nothing. Attached to a daemon without `queue-prompt` (an older one), the
first reply says to restart it, and after that `r` does what it did before:
it says `r` replies to mail in the Inbox, and renames on the rail.

### The review overlay

`ctrl+b v` (or `v` in the Inbox or on a rail agent row) opens the diff of
the pane's changes over the whole screen, once the daemon has read it: the
file list on the left, the file under it on the right, and your notes under
the lines they are on. The code is coloured by its file type in the
active theme's colours, added and removed lines sit on green and red grounds,
and the words that changed in a changed line are marked. It owns every key
while it is open, in either mode.
The keys are the overlay's own, not bindings, like the scrollback browser's:

| Keys | What it does |
| --- | --- |
| `j` / `k`, arrows | Move by line; with the file list focused, move through the files |
| `space`, `pgdown`, `ctrl+d` / `pgup`, `ctrl+u` | Move by a page |
| `g` / `G` | First or last line |
| `]` / `[` | Next or previous hunk, going on into the next or previous file |
| `}` / `{` | Next or previous file |
| `s` | One column, or the old and new sides next to each other where the diff column is wide enough (about 120 columns of screen) |
| `h` / `l`, `left` / `right` | Scroll the code sideways |
| `tab` | Focus the file list or the diff |
| `enter` | In the file list: open that file |
| `c` | A note on the line under the cursor |
| `C` | A note on the whole hunk |
| `e` | Edit the note under the cursor |
| `x` | Resolve (remove) the note under the cursor |
| `S` | Send every unsent note to the pane's agent as one message, typed when it is at rest |
| `u` | Switch between the changes since the base and the uncommitted ones only |
| `b` | Diff from another base (a line, filled with the current one; empty for the default) |
| `w` | The compare view, for a pane in a fan |
| `r` | Read the diff again |
| `esc`, `q` | Close (from a review opened in the compare view, go back to it) |

In the note line and the other one-line prompts every printable key is text,
a paste is typed as one line, `enter` saves and `esc` drops.

The compare view lists the attempts of the fan, with what each changed
against the fan's base and its last check:

| Keys | What it does |
| --- | --- |
| `j` / `k` | Move |
| `enter` | Review that attempt; `esc` comes back |
| `m` | Mark an attempt; two marks enable `d` |
| `d` | Diff the two marked attempts with each other |
| `V` | Run a command in every attempt (a line filled with the last one) |
| `K` | Keep the attempt under the cursor: a question names what is removed, `y` keeps it and removes the others, any other key keeps everything |
| `esc`, `q`, `w` | Back to the review |

A key from `send-keys` or a tape may move around the review but never acts as
you: a note it typed is not saved, and `S`, `x`, `V` and the keep
confirmation refuse it. See
[Reviewing an agent's changes](AGENT_STATE.md#reviewing-an-agents-changes).

## Other keyboard layouts

Bindings work with layouts for non-Latin scripts, such as Cyrillic, Greek,
Hebrew or Arabic. When no binding matches the character a key types, tuios uses
the key at the same position on a US layout. With a Ukrainian layout, the key
that types `ш` is the US `i` key, so `ctrl+b` then that key opens the Inbox. A
binding on the typed character wins, so you can still bind `ш` yourself. Text
that you type into a pane, a rename or a search stays the character that you
typed.

Latin layouts, such as AZERTY, QWERTZ or Dvorak, use the letters on their keys.
A letter or a `ctrl` chord means the key that it types, not the US key at its
position. On Dvorak, the key that types `b` is the US `n` key, and `ctrl` with
it is `ctrl+b`, the leader. The showkeys strip, the binding recorder and a
tape spell the chord the same way. A Latin letter with no binding does nothing.

This needs a terminal that sends the US-layout key through the Kitty keyboard
protocol: Ghostty, kitty, WezTerm or foot. Other terminals send only the typed
character. With those, switch to a Latin layout for tuios commands.

### Shifted digits and AZERTY

On a US keyboard `&` is `shift+7`. Some terminals send the chord and others send
the character. By default, a binding on `opt+shift+7` therefore also matches
`opt+&`, and a binding on `!` also matches `shift+1`. These US aliases apply
only when no binding names the key itself. A binding that you write for a key
always wins over an alias.

On other layouts these keys are in other places. On a French Mac, `&` is the
unshifted 1 key and the digits need Shift. tuios turns off the US aliases for a
key when the terminal reports that the key is not where a US layout has it.
Terminals that send this through the Kitty keyboard protocol include Ghostty,
kitty, WezTerm and foot. Other terminals do not report the layout. To turn off
the US aliases for every key, set this:

```toml
[keybindings]
keyboard_layout = "other"   # the default is "us"
```

`tuios keybinds explain opt+&` shows a binding that the key runs through a US
alias.

#### A recipe for French AZERTY on macOS

Set the left Option key to send Alt. In iTerm2 this is "Esc+". In WezTerm it
is `send_composed_key_when_left_alt_is_pressed = false`. Then:

- Option and Shift with a number key types the digit. The default `opt+1` to
  `opt+9` switch workspaces with it.
- The default `opt+shift+1` to `opt+shift+9` need Shift and a digit together,
  which AZERTY cannot type. Bind the move to the unshifted keys:

```toml
[keybindings]
keyboard_layout = "other"

[keybindings.workspaces]
move_and_follow_1 = ["opt+&"]
move_and_follow_2 = ["opt+é"]
move_and_follow_3 = ['opt+"']
move_and_follow_4 = ["opt+'"]
move_and_follow_5 = ["opt+("]
move_and_follow_6 = ["opt+§"]
move_and_follow_7 = ["opt+è"]
move_and_follow_8 = ["opt+!"]
move_and_follow_9 = ["opt+ç"]
```

To type `[ ] { } |` with Option, set the right Option key to compose. See
[One Option key for typing](#one-option-key-for-typing). With
`keyboard_layout = "other"`, tuios sends those characters to the pane.

In window mode tuios tells the terminal to send every key as a code. tuios
resets this when it stops. If tuios cannot stop correctly, the terminal can
stay in this mode. This occurs when you use `kill -9` on tuios, or when an ssh
connection drops. The shell then shows codes such as `[97u` when you type. To
reset the terminal, run this command or close the tab:

```sh
printf '\033[=0;1u'
```

## Keys sent to a pane

In terminal mode, tuios sends these keys to the program in the pane:

- The keypad keys, with Num Lock on or off. Keypad `Enter` sends Enter.
- `Begin`, the centre key of the keypad with Num Lock off.
- F13 and higher. A program on the legacy encoding gets them in the form
  xterm sends.
- `Insert`, `Delete`, `PageUp` and `PageDown` with their modifiers.
  `ctrl+Delete` stays `ctrl+Delete`.

A program on the Kitty keyboard protocol gets each key with the number the
protocol gives it. A key the protocol has no number for goes in its legacy
form. Caps Lock, Num Lock and a modifier key pressed alone reach that program
only when it asks for every key.

### Shift+Up and Shift+Down

`shift+up` and `shift+down` scroll the pane into its history. They do this
only when all of these are true:

- The program is on the main screen.
- The program did not turn on a mouse mode.

In other panes, tuios sends the keys to the program. Programs that use the
full screen, such as nvim, less and htop, get them. When the pane is in copy
mode, the keys scroll. This is the same rule as the mouse wheel, and the same
rule as kitty.

To change the keys, set `terminal_scroll_up` and `terminal_scroll_down` in
`[keybindings.terminal_mode]`.

## macOS

Option is a compose key on macOS unless the terminal is told otherwise, so an
Option chord usually arrives as a character rather than as Alt. tuios reads
the characters that a US layout composes back into the chord that types them.
When Option+8 types `•`, tuios runs the `opt+8` binding and switches to
workspace 8.

If you use Option to type characters such as `#`, `•` or `{`, set this. tuios
then sends the character to the pane:

```toml
[keybindings]
option_glyphs = "type"   # the default is "bind"
```

With `keyboard_layout = "other"`, tuios does not use the US characters, and
the characters go to the pane.

Two kinds of chord cannot be read:

- **Dead keys.** Option+e, i, n, u and backtick emit nothing at all until a
  second key ends the composition. `alt+n` is bound to "next pane" in terminal
  mode, and on a stock macOS terminal it takes two presses.
- **Rewritten chords.** Option+Left and Option+Right are sent as the readline
  word motions, `ESC b` and `ESC f`. Nothing in what arrives says an arrow key
  was pressed. Ghostty ships keybinds that do this, and they win even with
  Option-as-Alt turned on.

Command chords never reach a program inside a terminal at all; macOS routes
them to the menu bar.

### The fix

Turn on your terminal's Option-as-Alt setting:

| Terminal | Setting |
|---|---|
| Ghostty | `macos-option-as-alt = true` in `~/.config/ghostty/config` |
| Terminal.app | Settings, Profiles, Keyboard, tick "Use Option as Meta Key" |
| iTerm2 | Settings, Profiles, Keys, set Left Option key to "Esc+" |
| kitty | `macos_option_as_alt yes` in `~/.config/kitty/kitty.conf` |
| WezTerm | `send_composed_key_when_left_alt_is_pressed = false` |
| Alacritty | `option_as_alt = "Both"` under `[window]` |
| VS Code | turn on `terminal.integrated.macOptionIsMeta` |

Ghostty needs two more lines, because its own keybinds rewrite the arrows
before any encoding happens:

```
keybind = alt+left=unbind
keybind = alt+right=unbind
```

tuios says all of this on screen the first time it sees a chord that did not
arrive as it was meant to.

### One Option key for typing

Some terminals let you set the two Option keys differently. Use the left Option
key for tuios and the right Option key to type characters such as `#`, `•` or
`{`. In WezTerm:

```lua
config.send_composed_key_when_left_alt_is_pressed = false
config.send_composed_key_when_right_alt_is_pressed = true
```

In iTerm2, set Left Option key to "Esc+" and Right Option key to "Normal".
Then set this, so that the right Option key types characters into the pane:

```toml
[keybindings]
option_glyphs = "type"
```

The left Option key sends Alt and runs tuios bindings. The right Option key
types characters into the pane.

### What works without changing anything

The prefix. Every navigation command has a prefix binding, and the prefix is an
ordinary `ctrl` chord that no terminal interferes with:

| Keys | What it does |
|---|---|
| `ctrl+b` then an arrow | Focus the pane in that direction |
| `ctrl+b n` / `ctrl+b p` | Next and previous pane |
| `ctrl+b (` / `ctrl+b )` | Previous and next session |
| `ctrl+b a` | Launcher |
| `ctrl+b 0` to `ctrl+b 9` | Jump to a pane |

The prefix stays armed for half a second after a command worth repeating, so
`ctrl+b` then left left left walks three panes on one prefix press. This is
tmux's `repeat-time`, and `appearance.prefix_repeat_time` changes it. Zero
turns it off.

Workspace switching on `opt+1` to `opt+9` works with no configuration, because
those Option chords compose to characters tuios can read back. That table is
built for a US layout.
