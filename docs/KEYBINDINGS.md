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

## Keys of another multiplexer

`keybindings.preset` gives the keys of tmux, GNU screen or zellij after the
leader:

```toml
[keybindings]
preset = "tmux"
```

Or press `U` in window mode, or pick "Keybind preset" in the command palette.
The picker shows each preset with three of its keys, and the keys the program
in your pane would lose to it. Press Enter to use the preset. Or run
`tuios keybinds preset tmux`. `tuios keybinds preset` shows the preset in use,
and `tuios keybinds preset list` shows them all.

The welcome screen offers a preset when it finds a `~/.tmux.conf` (or
`~/.config/tmux/tmux.conf`), a `~/.screenrc` or a `~/.config/zellij` folder.
When your tmux.conf sets another prefix, such as `set -g prefix C-a`, the offer
uses that leader. The offer changes nothing until you press `U` and Enter.

| Preset | Leader | What it does |
|---|---|---|
| `tuios` | `ctrl+b` | The tuios keys. This is the default |
| `tmux` | `ctrl+b` | tmux 3.8's keys after the leader |
| `screen` | `ctrl+a` | GNU screen's keys after the leader, also on the control variant (`ctrl+a ctrl+n`). `ctrl+a ctrl+a` goes to the last workspace. `ctrl+a a` sends `ctrl+a` to the pane |
| `zellij` | `ctrl+g` | zellij's unlock-first keys. After `ctrl+g`, `p` opens pane mode (the window menu), `t` tab mode (the workspace menu), `s` copy mode and `o` the session switcher. The next key runs one action. In pane mode `r` splits right, `d` splits down, `f` zooms, `h` `j` `k` `l` move focus and `c` renames. In tab mode `n` opens a workspace, `x` closes it, and `h` and `l` go to the previous and next. It turns on [lock mode](#lock-mode) |

- A tmux window is a workspace here. A tmux pane is a window here. So `c`,
  `n`, `p`, `l`, `0` to `9`, `,` and `&` act on workspaces.
- A preset changes the leader, the keys after the leader and the
  terminal-mode keys. Window mode, the sidebar, copy mode and the agents view keep
  the tuios keys.
- A tuios feature whose key the preset uses moves to the tuios menu. Press the
  leader, then `ctrl+t`, then the key it had. For example, `ctrl+b ctrl+t m`
  opens the minimize menu.
- Your own bindings win. An action you bind keeps your keys. A key you bind
  keeps your action, and the preset's action for that key loses it.
  `tuios keybinds doctor` lists each preset key that your bindings take.
- A binding that has the tuios default key of an action the preset changes
  gives way to the preset. A config.toml from an older tuios holds every
  default, so the preset still applies. In such a file,
  `leader_key = "ctrl+b"` gives way too: under screen and zellij, the leader
  is the preset's. In a file without those default keys, `ctrl+b` stays.
- Only the arrow keys and the resize keys repeat after the leader, as tmux
  `bind -r` does. A letter you type right after `ctrl+b p` goes to the shell.
- `tuios keybinds preset tmux` removes `keybindings.leader_key`, so the
  leader is the preset's.
- `keybindings.leader_twice` is what the leader pressed two times does:
  `send` sends it to the pane (the default) and `last_workspace` goes back to
  the last workspace. The screen preset sets `last_workspace`. `ctrl+c` still
  cancels the leader under every preset.
- A `prefix_mode` binding on the leader key wins over `leader_twice`. With
  `leader_key = "ctrl+j"` and `terminal_focus_down = ["ctrl+j"]`, the leader
  pressed two times focuses the pane below.

`tuios keybinds cheatsheet tmux` shows the keys in three groups: the keys that
work as in tmux, the tuios keys that moved, and what tmux has that tuios does
not. `tuios keybinds preset show tmux` prints the preset file.

## Lock mode

Lock mode gives every key to the program in the pane. It is the locked mode of
zellij. Turn it on in `config.toml`:

```toml
[keybindings]
lock = true
```

The zellij preset turns it on. Set `lock = false` to turn it off under that
preset.

- In terminal mode, tuios takes only the leader. Every other key goes to the
  pane, also the keys that tuios binds. `alt+1` to `alt+9`, `alt+n`,
  `alt+esc`, `ctrl+p`, the arrow keys with `alt`, `shift+up` and the hold key
  all go to the pane.
- The leader unlocks. Press the leader, then the keys of one action. The
  action runs and tuios locks again.
- A repeatable action keeps its menu open. These are the actions in
  `keybindings.repeat`, such as focus moves and resize steps. Press the key
  again to move again. You do not press the leader again.
- The open menu then takes only repeatable keys. Any other key closes the
  menu and goes to the pane, and tuios locks again. For example, `x` after
  `ctrl+b left right` goes to the pane. It does not close a pane. The
  which-key panel of the open menu shows only the repeatable keys and `esc`.
- Press `esc` to close the menu and lock again. `ctrl+c` also closes it.
- Press the leader to close the menu and open a new leader menu.
- A key that the open menu does not bind closes the menu. The key goes to the
  pane.
- To go to window mode, press the leader, then `esc`.
- To send the leader to the pane, press the leader two times.
- Window mode, overlays, copy mode and the mouse do not change.

The mode pill in the dock shows a lock when tuios is locked. It shows an open
lock while the leader or a menu waits for a key. Without Nerd Fonts, the pill
shows `LOCK` and `UNLOCK`. When the dock is hidden, or
`appearance.dock_prefix_indicator` is `"badge"`, the floating badge shows
`PREFIX` and the open lock while a menu waits for a key.

## The full list

`tuios keybinds list` shows every action and the keys that run it, as your
`config.toml` sets them. It has one table for each scope: global, window mode,
terminal mode, the sidebar and its files and agent rows, the agents view, its prompt,
the mailbox, the Copies panel, copy mode, the prefix and each prefix menu, and tape playback. A key in a
prefix menu shows with its chord, such as `ctrl+b L 5`. The command keys have a
table of their own.

After the scopes, the list shows the fixed keys. tuios reads these keys itself,
and you cannot rebind them: copy mode, hints mode, the message view, the ways
out of the spotlight, the list filter keys and the fixed mouse gestures. The middle
button is an exception: terminal mode binds it like a key. The help overlay
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

Focus the sidebar with `s` in window mode or `Ctrl+B e`, or walk into it with
the terminal-mode focus keys from the outermost window (see Moving focus), then
select a file.
With both sidebar edges visible, `e` while the sidebar has keyboard focus
switches focus to the other edge; press it again to return. `Ctrl+B H` toggles
the independently configured opposite rail without changing the original
rail (`Ctrl+B b` still toggles that original rail).

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

`S` sends the selected file or folder to a host. tuios asks which host, then
which folder. The folder field starts as `~/Downloads/tuios/`.
tuios makes the folder when it is not there. A file that is there stays, and
the copy gets the name `name (from HOST).ext`. The context menu of a row has a
Send to host row that does the same. The binding is `file_send` in
`[keybindings.sidebar_files]`.

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

In `[keybindings.terminal_mode]`, `middle` names a press of the middle mouse
button. It accepts `ctrl`, `alt` and `shift`, such as `ctrl+middle`. Other
tables do not accept it. A program that uses the mouse, such as vim with
`mouse=a`, lazygit or tmux, gets a plain middle press. A binding with a
modifier runs over such a program too. Other mouse gestures are fixed.

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

The terminal-mode and prefix keys go on into the sidebar. When no window lies in that direction and the sidebar is shown on that edge, the sidebar gets the keyboard, as `s` in window mode does. The key in the opposite direction gives the keyboard back to the window you came from. A hidden sidebar stays hidden.

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

To open the navigator ready to type, so `Ctrl+B /` then a name finds a pane,
set this in `config.toml`. `esc` with nothing typed then closes it.

```toml
[panes]
navigator_search = true
```

The current session's panes are read from your client, and the preview of
those panes is live. The other sessions are read when the navigator opens.
Their preview shows the screen as it was then, with its colors. The preview
shows colors and text only. Links, clipboard writes and other control sequences
from a pane do not reach your terminal. A click on a pane goes to it. A
click on a session or a workspace opens or closes it.

`tuios list-windows --all --text 40 --json` prints the same rows for a script.
See [CLI_REFERENCE.md](CLI_REFERENCE.md#tuios-list-windows).

### Go to anything

`Ctrl+B G` opens the go-to picker. It lists every place you can go in one list: agents, sessions, the workspaces of each session, and panes. The action is `goto_picker`. The command palette has it as "Go to: an agent, session, workspace or pane".

Each row starts with its kind, then says where the place is:

```
agent      claude · payments › 1 › api    needs you: approve Bash ●
session    payments [space: surge]                          3 panes
workspace  payments › 2 · api-tests                          1 pane
pane       payments › 1 › nvim
```

With nothing typed, the first row is the place you were on last, so `Ctrl+B G` and `enter` take you back to where you were. The agents that need you come right after it, the one waiting longest first, then the other places you were on recently, the most recent first, then every agent, session and workspace. Panes are listed once you type something. A session has many panes, and the ones you were just on are already in the recent places.

Typed words are a fuzzy search over the names a row shows: the session, the workspace, the pane, and for an agent its harness and its last message. A `key:value` term narrows the rows instead, and every term has to match. The terms are the selector the agents view takes (`harness:`, `state:`, `needs:you`, `session:`, `group:`, `host:`, `name:`), and three more:

| Term | Keeps |
|---|---|
| `kind:agent`, `kind:session`, `kind:workspace` (or `kind:ws`), `kind:pane` | Rows of that kind |
| `space:NAME` | Rows of the sessions filed under that space |
| `ws:N` | Agents, workspaces and panes on that workspace, by number or name |

A value is a glob, and commas between values mean any of them, as in `kind:session,workspace`. A term that does not fit a row leaves it out: `harness:` keeps only agents, and `ws:` leaves out the sessions. `@`, `#` or `!` at the start of the query is `kind:agent`, `kind:session` or `kind:workspace`. For example, `#space:surge` lists the sessions in the surge space, and `@needs:you` lists the agents that need you.

| Key | What it does |
|---|---|
| The list keys | Move. See [Lists and panels](#lists-and-panels) |
| `enter` | Go there: the session, then the workspace, then the pane |
| `@`, `#`, `!` first | Filter to agents, sessions or workspaces. The picker's hint shows them as `@ # ! filter` |
| `ctrl+u` | Clear the query |
| `esc` | Close the picker |

Workspaces and panes are listed for the sessions on the machine you are attached to. A session on another machine is listed, and its agents are, and `enter` on it attaches that machine as the rail does. A workspace of a session you are not attached to shows its number, because its name is not sent to your client.

## Neovim pane navigation

The optional [tuios-nvim-navigator](https://github.com/Tim4c/tuios-nvim-navigator)
plugin lets the terminal-mode focus keys move through Neovim splits first. The
feature is off by default. Set `appearance.nvim_navigation = true` to enable
it. At a split edge, TUIOS moves to the adjacent pane instead, or into the
sidebar when the pane is the outermost one on that side. The plugin
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
the settings page, the agents view, the mailbox, the keybind manager, the theme,
glyph and effect pickers, the session, workspace, layout and machine pickers,
the go-to picker, the window picker, the quit and context menus, the dock and rail editors and
the tape manager.

| Keys | What it does |
|---|---|
| `up`, `down`, `ctrl+p`, `ctrl+n` | Move one row. Up on the first row goes to the last, and down on the last to the first |
| `home`, `end` | The first or last row |
| `pgup`, `pgdown` | A page up or down, stopping at the ends |
| `k`, `j`, `g`, `G` | Up, down, first and last, in a list with no filter to type into |
| `ctrl+u` | Clear a typed filter (fixed) |
| wheel | Scroll the list under the pointer, stopping at the ends (fixed) |

The movement keys are the `[keybindings.lists]` section. Each action takes the
keys that do it:

| Action | Default keys |
|---|---|
| `list_up` | `up`, `ctrl+p`, `k` |
| `list_down` | `down`, `ctrl+n`, `j` |
| `list_page_up` | `pgup` |
| `list_page_down` | `pgdown` |
| `list_first` | `home`, `g` |
| `list_last` | `end`, `G` |

A key that is a single printable character is a letter key. Only a list with no
filter to type into reads it, because a list with a filter keeps its letters
for the filter. To move on `ctrl+j` and `ctrl+k` as well as the arrows:

```toml
[keybindings.lists]
list_down = ["down", "ctrl+j"]
list_up   = ["up", "ctrl+k"]
```

An action you name replaces its default keys, so the example above gives
`ctrl+n`, `ctrl+p`, `j` and `k` back to the pane or the filter. An action you
leave out keeps its defaults, and `list_page_down = []` unbinds it.

Set `appearance.wrap_lists = false` to stop at the ends instead of wrapping.
The close-session and file confirmations never wrap, so up from Cancel cannot
land on the answer that deletes.

With nothing typed, the command palette lists its commands under category
headers. The headers are not rows: the cursor steps over them. Once you type,
the commands are ranked by how well they match, and each row names its
category in a quiet column on the right.

Type `#` to search actions instead of commands. `#close` lists every action
with "close" in its name, key or description, with the keys it is on today.
Press `Enter` to run the action. The palette closes first, so the action works
on the pane and the mode underneath, as its key would. Press `ctrl+e` to open
the keybind manager on the action, to rebind or unbind it. The footer shows
that key while a `#` search is open.

Some actions work only in their own view, such as a rail, Inbox, mail or copy
mode key. The palette has nothing for them to act on, so `Enter` on one opens
the keybind manager, as `ctrl+e` does.

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

### The rename dialog

One dialog renames a window, a session and a workspace. It opens holding the current name. Its keys are fixed.

| Keys | What it does |
|---|---|
| `enter` | Save the name |
| `esc` | Cancel |
| `ctrl+u` | Clear the field |
| `ctrl+c` | Clear the field, or cancel when it is already empty |

A click on the `clear` hint in the dialog's border clears the field too. In the help's search, `ctrl+u` clears the query.

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

A pane can address another pane with a `tuios://window/<id>` link, where
`<id>` is the pane's short id from the sidebar or `list-windows`. A plain
`Ctrl+click` there jumps to that pane, and a
`tuios://window/<id>/close` link asks first: the confirmation names the
pane and only a second click closes it.

## Moving a pane with the mouse

Drag a tiled pane by its title bar, or `Ctrl+click` anywhere in it and drag. In BSP tiling, dropping it on the middle of another pane swaps the two, and dropping it near an edge of another pane splits that pane and puts the dragged pane in the half toward that edge. While the button is down, the place the pane will land is highlighted. See [LAYOUT_MODES.md](LAYOUT_MODES.md#moving-a-pane-with-the-mouse).

## Scratch terminal

`Ctrl+B g` shows the scratch terminal, a small layout of panes in a box over
the current layout. Press it again to hide the box. The shells keep running
between shows. Inside the box the window keys act on its panes: split, move
the focus, resize, swap, zoom and close. The key works from inside the box.
The action is `toggle_scratch`, and `[scratch]` sets the size of the box. See
[SESSIONS.md](SESSIONS.md#the-scratch-terminal). To add more scratch terminals
on keys of their own, see [Command keys](#command-keys).

## New worktree

`Ctrl+B U` makes a worktree of the repository of the focused pane's folder.
A small dialog asks for the branch name. `tab` moves to the session name.
Leave the session name empty to use the name the dialog shows. `enter` makes a
new branch in a new worktree and a session in it. The session is grouped under
its repository in the sidebar, and tuios switches to it. This is what
`tuios worktree new` does. `esc` closes the dialog.

If the focused pane is not in a git repository, a message says so. The action
is `new_worktree`, and the command palette has it too. A right-click on a
session or a repository in the sidebar opens the same dialog.

## Session note

`Ctrl+B I` opens a one-line editor with the note of the session. A note says
what the session is for or what comes next. `enter` saves the note. An empty
note removes it. `esc` closes the editor. The action is `set_note`. The command
palette and the menu of a session row in the sidebar have it too.

The dock shows the note when you attach or switch to the session. See
[SESSIONS.md](SESSIONS.md#return-notes).

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

## Pick a path

`Alt+;` in terminal mode opens the path picker at the cursor of the focused
pane. `Ctrl+B .` opens it too. The picker lists the folder that the shell in
the pane is in. Folders come first, then files. Type to filter the names.
Press `Enter` to type the path of the selected name at the cursor.

| Key | What it does |
| --- | --- |
| Text | Filter the names |
| `Up`, `Down`, `Ctrl+P`, `Ctrl+N` | Move the selection |
| `Tab` | Open the selected folder. On a file, complete the name |
| `Shift+Tab`, `Backspace` with no filter text | Go up one folder |
| `/` at the start | Start an absolute path |
| `~/` at the start | Start a path in the home folder |
| `Enter` | Type the path and close the picker |
| `Alt+H` | Show or hide the hidden files |
| `Esc` | Close the picker and type nothing |

The filter line shows the path that you built, for example `src/app/`. A
click on a name types its path. The mouse wheel moves the selection.

The path is relative to the folder of the pane. A path that starts with `/`
or `~/` stays absolute. tuios puts a path with a space or a special character
in quotes. When the program in the pane turned on bracketed paste, tuios
sends the path as a paste. Else it types the path.

The picker works in a pane on another machine. The daemon on that machine
lists the folder, with the same read rules as the sidebar's files section.
When the shell in the pane runs ssh to another machine, the picker does not
open.

The action is `pick_path`. `path_picker.show_hidden = true` shows the hidden
files when the picker opens. The default is `false`.

## Calm mode

`Ctrl+B Z` turns calm mode on and off. Use it to keep your attention on one
task. While calm mode is on:

- Nothing moves. Window slides, panel fades, the working-agent shimmer and the
  copy sweep stop. Your `appearance.motion` level does not change.
- Nothing blinks. The cursor is steady.
- Messages from panes, agents and mail do not show in the dock. The log keeps
  them. An agent that needs you still shows its message, and so do warnings
  and errors.
- The dock tabs do not mark output on other workspaces. The bell mark stays.

The dock shows `calm` while it is on. `calm 3` means that 3 messages are in
the log. When you turn calm mode off, tuios tells you how many messages came
in. The action is `toggle_calm`. To keep calm mode on after a restart, set
`appearance.calm = true`. See [CONFIGURATION.md](CONFIGURATION.md#calm-mode).

## Paste an image

`Ctrl+B V` pastes the image on your clipboard into the focused pane. tuios
saves the image to a file on the machine where the pane runs. Then it pastes
the path of that file into the pane. The pane can run on this machine or on a
host. Claude Code, Codex and other agents read an image from a path in the
prompt. The action is `paste_image`, and the command palette has it too.

The paste binding also pastes an image. `terminal_paste_host` in
`[keybindings.terminal_mode]` is bound to `Ctrl+Shift+V`, `Super+V`,
`Shift+Super+V` and the middle mouse button by default. The middle button
pastes from the clipboard, not from the primary selection. A program that uses
the mouse gets the middle button instead. `paste_clipboard` runs
the same operation outside terminal mode. When the clipboard holds an image
and no text, the paste binding pastes the image. When the clipboard holds text,
it pastes the text. Some terminals send an empty paste when the clipboard holds
only an image. tuios then pastes the image.

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

### A pane on another machine

A pane can run on another machine in three ways. In each, tuios writes the
image on that machine:

- A session on a host, attached with `tuios attach --host NAME`. The daemon on
  the host writes the file.
- A window on a host in a session of this machine. The daemon on the host
  writes the file.
- A pane of this machine that runs `ssh` or `mosh` to another machine, such
  as `ssh build` or `ssh -t build tmux`. tuios reads the ssh command line from
  the process. It resolves that line and each `[hosts]` entry with `ssh -G`.
  It compares the host name, the user, the port and the jump hosts. When
  exactly one entry is the same, tuios copies the image to the drop folder of
  that host and pastes the path there.

The other machine must allow `files` from this machine. If it does not, tuios
pastes nothing and shows the line to add to the config on that machine.

tuios pastes nothing in an ssh pane in these cases. A message says why.

- No `[hosts]` entry is the machine of the ssh command. Add the machine with
  `tuios hosts add NAME ADDR`.
- More than one `[hosts]` entry is that machine. Keep one of them.
- tuios cannot read the ssh command line, for example because it has `-F` or
  `-o ProxyCommand`.
- The shell over there reports a folder on another `[hosts]` machine: the pane
  runs ssh inside ssh. Paste the image in a pane on that machine.

An ssh tunnel (`ssh -N`, or `-f`, `-n` or `-W` without `-t`) does not read
what you type. tuios saves the image on this machine for it. So does a pane
that only reports a folder on another machine and runs no ssh.

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
  - `plugin_action`: the command is the id of a herdr plugin action, for
    example `example.layout.apply`. The daemon runs the action. The plugin
    must be enabled. When no plugin has the action, tuios shows a config
    problem. This works only in a daemon session on this machine. See
    [herdr plugins](AGENT_STATE.md#herdr-plugins).
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

In a session on a different machine, a `scratch`, `popup` or `pane` entry runs
on the machine of the session. The command runs as the user of that machine,
in the folder of the focused pane on that machine. A `~` in the command is the
home folder on that machine. A `popup` or `scratch` entry there needs `"open"`
in the `allow` list of that machine's `[hosts]` table for your machine. Without
it, the dock shows a message that names the table to change. A `shell` entry
runs on the machine that runs the tuios client.

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

The action asks first. The dialog names the workspace and shows how many panes
close and how many agents are in them. `Cancel` is the default row. Scratch
panes stay open.

Right-click a workspace tab in the dock to close that workspace, whether or not
it is the one on screen. The tab's menu has the rows `Switch to`, `Rename` and
`Close`. `Close` is the same `close_workspace` action as the key and the palette
entry, with the same dialog, but it acts on the tab's workspace instead of the
current one. It is dimmed when the workspace has no pane to close. Closing a
workspace that is not on screen leaves the view where it is.

When the closed workspace is not on screen, or when tuios goes back from it,
the workspaces above it move down one, so the numbers follow the positions.
See [CONFIGURATION.md](CONFIGURATION.md#workspace-numbers).

`tuios close-workspace` does the same from a shell. See
[CLI_REFERENCE.md](CLI_REFERENCE.md#tuios-close-workspace).

## Restart the daemon

The `restart_daemon` action stops the daemon and starts it again with the
newest tuios installed. Use it after an update. The action has no default key.
The command palette has the entry "Restart the daemon". These open it too:

- The notice that says the daemon and this window run different versions,
  for example "The daemon runs 0.10.0. ctrl+b j restarts it." Click it, or
  press the key it names. Press `esc` to put it off until the next start.
- The row "Restart the daemon" at the top of the Daemon tab in the settings.
- The rail's menu for this machine.

A dialog shows the daemon's version now and after the restart, and this
window's version when it moves too. It lists the programs that stop, by pane
and session, and what comes back, with the agents that can resume. The text
is the text `tuios restart-server` prints. Press `r` to restart, or `esc` to cancel. A click on a row
does the same. The dialog then shows each step: stop the daemon, start the new
one, attach again. tuios then starts again on the new build, in the same
session, and says "The daemon restarted on 0.10.2. Your session is back."

Other clients attached to the daemon wait up to 15 seconds for the new one.
They then attach to their session again by themselves and say "The daemon
restarted. tuios reattached." While they wait, the dock says "Waiting for the
daemon. Keys are not sent." With the dock hidden, the middle of the screen
says it. Keys typed then are not sent to the panes.

When a step fails, the dialog says what happened and what to do next. When
a program started or stopped after you agreed, the restart does not run, and
the dialog asks you to read the new list. When your session did not come
back, tuios does not attach to it: the dialog says so and points to
`tuios resurrect`. When
the daemon did not start, the row is "Quit tuios", and tuios prints the same
words when it quits. To bind the action:

```toml
[keybindings.prefix_mode]
restart_daemon = ["alt+r"]
```

`tuios restart-server` does the same from a shell. See
[CLI_REFERENCE.md](CLI_REFERENCE.md#tuios-restart-server).

## Move a pane to another workspace

Three default key sets go to workspace N or send the focused pane there. They
work in window mode and in terminal mode.

| Action | macOS | Linux and other | What it does |
| --- | --- | --- | --- |
| `switch_workspace_1` to `switch_workspace_9` | `opt+N` | `alt+N` | Shows workspace N. |
| `move_and_follow_1` to `move_and_follow_9` | `opt+shift+N` | `alt+shift+N` | Sends the focused pane to workspace N and shows workspace N. |
| `send_to_workspace_1` to `send_to_workspace_9` | `ctrl+opt+N` | `ctrl+alt+N` | Sends the focused pane to workspace N and stays on this workspace. Focus goes to another pane here. |

`send_to_workspace_N` needs a terminal that reports Ctrl and Alt with a digit.
A legacy terminal sends `ctrl+alt+N` as `alt+N`, or as a control character for
most digits. The Kitty keyboard protocol reports the key, and so does xterm's
modifyOtherKeys. Ghostty, kitty, WezTerm and foot speak the Kitty protocol. In
another terminal, bind the action to a key the terminal can send:

```toml
[keybindings.workspaces]
send_to_workspace_2 = ["alt+w"]
```

Your `config.toml` may already bind `ctrl+alt+N` to another action in window
mode. Then that binding keeps the key, and `send_to_workspace_N` has none.
`tuios keybinds doctor` lists it.

Windows sends AltGr as Ctrl+Alt. A character typed with AltGr, such as `€` on a
UK keyboard, still reaches the pane and does not move it.

## Cycle through workspaces

The `next_workspace` and `prev_workspace` actions go to the next or the
previous workspace that has panes, and wrap at the end. They skip a workspace
with no panes, so with `workspaces.new_window_when_empty` on they do not open
a pane on each empty workspace. When no other workspace has panes, they do
nothing. The actions have no default key. The command palette has the entries
"Next workspace" and "Previous workspace". To bind them:

```toml
[keybindings.workspaces]
next_workspace = ["alt+y"]
prev_workspace = ["alt+u"]
```

## Actions for a tmux-style keymap

These actions have no default key. Bind them in any
section where you want them.

| Action | What it does |
| --- | --- |
| `mark_pane` | Marks the focused pane, or clears the mark when it is the marked one. The marked pane shows `[marked]` in its title bar. |
| `join_marked_left`, `join_marked_right`, `join_marked_up`, `join_marked_down` | Moves the marked pane next to the focused one, on that side, from this workspace or another. Needs BSP tiling. |
| `resize_pane_left_5`, `resize_pane_right_5`, `resize_pane_up_5`, `resize_pane_down_5` | Moves the focused pane's edge by 5 cells. The `_20` actions move it by 20. Under the leader, the key repeats without a second leader press, as tmux `bind -r` does. |
| `new_workspace` | Goes to the first workspace with no pane, the one the dock's `+` button opens, and opens a pane there. On an empty workspace past the last one in use, that workspace is the new one: it stays there and opens the pane there. |
| `last_workspace` | Goes back to the workspace you were on before this one, as tmux `last-window` does. Press it again to go back and forth. |
| `break_pane` | Moves the focused pane to the first workspace with no pane and goes with it, as tmux `break-pane` does. |
| `last_session` | Goes back to the session you were on before this one, as tmux `switch-client -l` does. |
| `send_prefix` | Sends the leader key to the program in the focused pane, as pressing the leader two times in terminal mode does. Use it for screen's `C-a a`, or for a tmux in the pane. |
| `move_workspace_left`, `move_workspace_right` | Swaps this workspace with the one before or after it, and follows it. The two workspaces trade numbers, so the moved one answers to its new number. |
| `cycle_layout` | Cycles BSP, master-stack and scrolling. |
| `reload_config` | Reads `config.toml` again, as the "Reload config" palette row does. |
| `toggle_mouse` | Stops asking the terminal for mouse reports, so the terminal selects text itself. Run it again to turn the mouse back on. The setting lasts until you quit. |

For tmux's synchronize-panes, bind `toggle_multifocus_all`. It puts every pane
on the workspace in the multifocus set, so typing goes to all of them. See
[LAYOUT_MODES.md](LAYOUT_MODES.md#multifocus).

```toml
[keybindings.window_prefix]
mark_pane = ["m"]
join_marked_right = ["J"]
```

## Menus nested under the leader

A key in a prefix menu can open a menu of your own, so a keymap can nest as
deep as a tmux which-key tree. You declare the menu, then bind the action
`open_menu:NAME` to the key that opens it. tuios has no menus of this kind
until you declare one, and it binds no new key for them.

```toml
[keybindings.window_prefix]
"open_menu:split" = ["s"]

[keybindings.menus.split]
title = "Split"

[keybindings.menus.split.keys]
prefix_split_vertical = ["y"]
prefix_split_horizontal = ["x"]
"open_menu:resize" = ["R"]

[keybindings.menus.resize]
title = "Resize"
sticky = true

[keybindings.menus.resize.keys]
resize_pane_left_5 = ["h"]
resize_pane_down_5 = ["j"]
resize_pane_up_5 = ["k"]
resize_pane_right_5 = ["l"]
```

`Ctrl+B t s y` splits the pane. `Ctrl+B t s R h h h` narrows it by three steps.

- `open_menu:NAME` goes in any section: after the leader, in another menu, or
  in window mode or terminal mode for a menu that opens on one key.
- A menu name has lowercase letters, digits, `-` and `_`.
- `title` heads the which-key panel and defaults to the name. `sticky` keeps
  the menu open after a key runs, as tmux `bind -r` does, until `esc` or a key
  the menu does not bind. In terminal mode that key goes to the pane, so the
  first letter you type after resizing is not lost. `esc` and `ctrl+c` close a
  menu and are never sent to the pane.
- A key that the menu does not bind closes it, and a menu that is not sticky
  drops the key, as the other prefix menus do.
- The which-key panel lists the keys the menu has. Menus come first, with a
  `+`.
- `tuios keybinds list`, `doctor`, `unbind` and `free` read the menus as they
  read any other section. A config warning names a key that opens a menu that
  is not declared, and a menu that no key opens.

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

### Open the scrollback in the editor

`Ctrl+G` in copy mode opens the scrollback of the pane in your editor. The
file has all the scrollback and the screen, as plain text. zellij does the
same with `e`.

| Key | Where | Action |
| --- | --- | --- |
| `Ctrl+G` | Copy mode | `edit_scrollback` |
| `Ctrl+B E` | Any mode | `edit_scrollback` |

The command palette entry is "Edit the scrollback in the editor".

- tuios uses the File editor setting first. Then it uses `$EDITOR`, then
  `$VISUAL`, then `vi`.
- vi, vim, nvim, nano, emacs, kakoune and helix open at the line of the copy
  cursor. Outside copy mode, they open at the top of the screen. Other
  editors open at the start of the file.
- In a session with a daemon, the text comes from the daemon. It has the
  full scrollback, also when your client has only part of it.
- The editor uses the terminal that tuios runs in. tuios comes back when the
  editor exits.
- The file is in a private folder. tuios removes the file when the editor
  exits, and when the client stops.
- A browser client or an SSH client from another machine cannot open the
  editor. tuios shows a message.

In copy mode, `e` and `E` move to the end of a word, as in vim. For this
reason, the editor key is `Ctrl+G`. `Ctrl+G` also opens a reply in the editor.
To use another key, set it in the `copy_mode` section:

```toml
[keybindings.copy_mode]
edit_scrollback = ["ctrl+o"]
```

The tmux preset uses `Ctrl+B E` to make the splits equal. With that preset,
the editor key is `Ctrl+B Ctrl+T E`.

### Other end of a selection

In a `v` or `V` selection, `o` moves the copy cursor to the other end of the
selection. The selected text does not change. The next motion moves the end
that the cursor is on now. Press `o` again to go back. If the other end is
outside the pane, the pane scrolls to show it. tmux and vim use `o` the same
way.

### Clear the scrollback

The `clear_scrollback` action clears the scrollback of the focused pane. It
also removes the rows above the prompt, and the prompt row moves to the top of
the pane. Ghostty, iTerm2 and Terminal.app do the same on `Cmd+K`.

| Action | Default key |
| --- | --- |
| `clear_scrollback` | None |

The command palette entry is "Clear the scrollback".

- tuios sends no key to the program, so the shell does not redraw.
- When the shell marks its prompt with OSC 133, tuios keeps all the lines of
  the prompt.
- In a session with a daemon, the daemon clears the pane. Each client of the
  session shows the clear. An attach later does not bring the old rows back.
- The key also works in copy mode. tuios leaves copy mode.
- tuios clears nothing while a full-screen program, such as vim or less, uses
  the pane. tuios shows a message.

`tuios clear-history` clears the scrollback from the command line, as tmux
`clear-history` does. It keeps the screen. With `--screen`, it does the same
as the key. See [CLI_REFERENCE.md](CLI_REFERENCE.md#tuios-clear-history).

The action has no default key, because the host terminal usually keeps `Cmd+K`
for itself. To use `Cmd+K`, bind `super+k` in the `global` section. A key in
the `global` section works in terminal mode and in window mode.

```toml
[keybindings.global]
clear_scrollback = ["super+k"]
```

Then tell the host terminal to send `Cmd+K` to tuios. In Ghostty, add this line
to the Ghostty config:

```
keybind = cmd+k=unbind
```

In iTerm2 and Terminal.app, `Cmd+K` is a menu item. Remove or change its
shortcut in the settings of the terminal. A terminal that does not send the
Super key cannot send `super+k`. Bind another key, such as `Ctrl+B K`:

```toml
[keybindings.prefix_mode]
clear_scrollback = ["K"]
```

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

## Copies between machines

`Ctrl+B u` opens the Copies panel. It shows one row for each copy between
machines, the newest first. The dock item `transfers` opens it too, with a
click. The command palette row is "Copies between machines".

| Key | Action | What it does |
| --- | --- | --- |
| `j` `k` `Down` `Up` | `transfers_down` `transfers_up` | Move the cursor |
| `p` | `transfers_pause` | Pause the copy, or go on with it |
| `x` | `transfers_cancel` | Cancel the copy. tuios asks first |
| `r` | `transfers_retry` | Try a failed copy again from where it stopped |
| `c` | `transfers_clear` | Remove the copies that ended |
| `Enter` | `transfers_answer` | Answer a copy that waits for an answer |
| `Esc` `q` | `transfers_close` | Close the panel |

The keys are in `[keybindings.transfers]`. The panel key is
`prefix_transfers` in `[keybindings.prefix_mode]`.

A copy waits for an answer when a file is there on the other machine and the
copy was started with `conflict = "ask"`. The dock says `1 copy asks`. Press
`Enter` on the row to choose Replace, Keep both or Skip. Press `Space` to use
the answer for the other files of the copy. tuios does not open this question
by itself, because `tuios cp` in a pane asks the same question there.

When you drop files on a pane whose program runs on another machine, the
terminal pastes their paths. tuios asks whether to send the files to that
machine. Send copies them to a private drop folder there. When the copy is
checked, tuios pastes the path on that machine. Paste the text pastes the paths
as they are. A paste into a pane on this machine is not changed.

## Screenshots over a panel

`Ctrl+B C` opens capture mode over any panel or overlay too: the agents view, the
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

## The agents view

Everything waiting for you, and every agent tuios knows of, is one full-screen
view on `ctrl+b i`. It was the Inbox panel. The agents are grouped by what
each needs from you: Needs you, Ready for review, Working, Idle, Earlier. What
the Inbox held is the Needs you group. See
[AGENT_STATE.md](AGENT_STATE.md#the-agents-view) for what goes in it.

| Keys | What it does |
|---|---|
| `ctrl+b i` | Open the agents view |
| `ctrl+b o` | Go to the oldest item that needs you; `o` again, inside the repeat window, goes to the next |
| `opt+shift+j` / `opt+shift+k` (`alt+shift+j` / `alt+shift+k` off macOS), or `ctrl+b J` / `ctrl+b K` | Next and previous agent pane (`next_agent`, `prev_agent`), in the order the rail's agents section lists them, wrapping at the ends. From a pane with no agent they go to the first and the last. An agent in another session or workspace is switched to, as choosing its row does. The chords also work in terminal mode and while the rail has the keyboard |
| `ctrl+b M` | Open the agents view on the mail for you (`m` there opens the whole mailbox) |

Inside it:

| Keys | What it does |
|---|---|
| `j` `k`, arrows | Move between agents. The focus area shows the selected one |
| `enter` | Go to the agent's pane and close the view. On mail, open the thread |
| `a` `A` `r` `R` | On a held approval: allow once, always allow, reject, reject and say why. `1` `2` `3` still work |
| `r`, `i`, `tab` | With no approval waiting: write a reply to the agent, or to the mail |
| `space` | Read an approval's or a question's prompt and answer it here |
| `n` | Open the New agent sheet. See below |
| `c` | On an attempt of a fan: compare the attempts. See below |
| `b` | Show the task board, the cards grouped by stage, or the agents again |
| `t` | On a card: take it for the pane behind the agents view. `n` on a card opens the New agent sheet on it |
| `o` | Open what the agent has shown, such as a TML form, on its Artifacts tab, and use it. `esc` goes back to the list |
| `f` | Show only what needs you, then each kind of item, then every agent |
| `s` | Step the order inside each group |
| `/` | Search. A `key:value` term is a selector, such as `harness:codex` |
| `d` `z` `u` `S` | Dismiss, snooze, undo, show the snoozed items |
| `y` `p` `m` | Resume, pass held mail on, open the mailbox |
| `x` `*` | Mark an agent, or every agent shown, for a bulk answer. A marked row shows a mark, and the header counts the marks |
| `1` `3` with marks | Allow every marked approval once, after a sheet that lists them, or reject them |
| `[` `]` | Switch the focus tabs: Card on the board, Thread, Changes, Artifacts, Checkpoints and Pane |
| `J` `K` | Scroll the thread, or a long plan waiting for a yes. On the Artifacts tab, move between the artifacts |
| `e` | Show every line of every card in the thread |
| `v` | Review the agent's changes |
| `ctrl+]` | Go to the agent's pane and zoom it. `ctrl+b i` comes back to the same agent |
| `l` `h`, `right` `left` | On a narrow screen, open the focus and go back to the list |
| `?` | All keys |
| `esc` `q` | Close. On a narrow screen, go back from the focus to the list first |

A bulk key acts only on the marked agents the list shows. An agent in a
folded group, or one a search hides, is left out, and closing the view clears
the marks. An approval whose command does not fit on the card is not answered
here: the card says so, and `enter` answers it in the pane.

`c` on an attempt of a fan compares every attempt side by side: its agent,
how long it took, its diff, its check from `tuios fan verify`, its first
changed lines and its files. `j` asks a judge: an agent that reads the
attempts and reports which one to keep. Through tuios it can only read the
attempts: their diffs, screens and scrollback. Its verdict shows on top, with its reasons, and its
pick is marked recommended. `1` to `9` and the arrows pick an attempt, and
`k` twice keeps it: the other worktrees are removed, their changes go to git
stash and their branches stay. The first `k` names every file git ignores in
the other worktrees, such as `.env`: they are deleted, not stashed. `up` and
`down` scroll that list. The second `k` reads the attempts again and keeps
only when nothing changed. When the list grew, or could not be read, it says
so and keeps nothing. `enter` goes
to the attempt's pane, `r` reads the attempts again and `esc` goes back.

The New agent sheet starts an agent with a task. Write the task, then pick
the agent, the model, where it works and the machine. `tab` and `shift+tab`
move between the fields. `left` and `right` change the agent, where it works,
the attempts and the button. `space` or `down` on the agent opens the list of
every agent. `up` and `down` on the machines pick one. `v` on the attempts
gives each attempt a different agent. `ctrl+j` adds a new line to the task.
`enter` starts. `esc` closes the sheet and keeps what you wrote for the next
`n`.

| Field | What it does |
|---|---|
| Task | The first prompt. It is typed when the agent is ready |
| Agent | The agents that run now come first, then the installed ones |
| Model | Passed to the agent as `--model`. Empty uses the agent's default |
| Where it works | A new worktree on its own branch, or this folder |
| Branch, Base | The new branch, named after the task until you change it, and where it starts |
| Machine | This machine and the linked hosts, the most free cores first |
| Attempts | 1 to 8. Each attempt gets its own worktree and branch |

A new worktree goes through the same path as `tuios fan`, and this folder
through `tuios start-agent`. Uncommitted files stay in this folder. On a
linked host the agent works in a new worktree of the host's checkout, which
the host finds by the repository's origin.

The Artifacts tab lists what the agent has shown you: each TML document its
pane opened, open or closed, and each screenshot it took. The newest is
first. The selected one is drawn under the list. An open document is the
same document as its popup or pane, with the same state: what you choose in
one place shows in the other. `o` or a click gives it the keys, and `esc`
gives them back. A closed document shows what it showed last, and your
answer. A document from another session is read-only here.

A TML popup and the agents view do not show at the same time:

- Open the view while an agent's popup floats, and the view selects that
  agent and shows the popup's document, with the keys.
- A document that opens while the view is open shows in the focus when it is
  from the selected agent. From another agent, its row gets a
  `● new artifact` chip, and a popup moves the row to Needs you. A click on
  the chip opens it.
- Close the view while the Artifacts tab shows an open document, and its
  window gets the focus and floats again.

In the image view, which a click on an image chip in the reply opens: `o`
opens the image in the desktop's viewer, `c` saves a copy and copies its
path, `s` saves a copy, `left` and `right` step through the images, `+` and
`-` zoom, and `esc` or a click outside the picture closes it.

The mouse works too. A click on a row selects it, and a double-click goes to
its pane. A click on a group heading folds the group. The buttons on an
approval card answer it. `+ New agent` opens the New agent sheet, and the search box opens
the search. The wheel scrolls the list, and over the focus it scrolls the
card. A search narrows the items with the agents. A selector the search line
cannot read shows its error in the search box.

The layout follows the width of the screen. At 120 columns and wider the list
has two lines per agent beside the focus. From 80 columns the rows are one
line. Under 80 columns the list fills the screen, and `enter`, `right` or a
click opens the focus in its place. With one or two agents the list folds into
a strip of names over the focus. When the list is taller than the screen, its
rows are one line and a long group shows its first 8 rows; `right` or a click
on `+N more` shows the rest.

In the mailbox: `j` and `k` move, `enter` opens a thread, `n` writes a new
message, `esc` closes. `n` opens a list of the agents in this session. Choose
one with `enter`, type the message, and press `enter` to send it. In an open
thread, `r` replies, `o` goes to the pane that last wrote, and `esc` goes back
to the list.

The message editor works as the reply does: `ctrl+j` adds a line, a
long paste shows as one block, and `ctrl+v` attaches the clipboard's image. A
message that does not fit in 8 KB, or that holds an image, goes with each
block and image as a file the message attaches.

In the prompt `space` opens: a digit chooses that option, `a` approves, `A`
approves and does not ask again, `d` rejects, `tab` types an answer, `r` reads
the prompt again, `enter` goes to the pane, `esc` goes back to the list. A
prompt with numbered options offers its digits in the footer and not `a`, `A`
and `d`, which still work. See
[Answering a prompt without attaching](AGENT_STATE.md#answering-a-prompt-without-attaching).

These keys are in three sections of their own, rebindable like any other:
`[keybindings.inbox]` (the list: `inbox_down`, `inbox_up`, `inbox_page_down`,
`inbox_page_up`, `inbox_first`, `inbox_last`, `inbox_go`, `inbox_peek`,
`inbox_dismiss`, `inbox_reply`, `inbox_resume`, `inbox_pass_on`,
`inbox_filter`, `inbox_select`, `inbox_mailbox`, `inbox_close`,
`inbox_write`, `inbox_sort`, `inbox_view_pane`, `inbox_new_agent`,
`inbox_allow`, `inbox_allow_always`, `inbox_open`, `inbox_back`,
`inbox_help`, `inbox_mark`, `inbox_mark_all`, `inbox_tab_prev`,
`inbox_tab_next`, `inbox_expand`, `inbox_compare`, `inbox_artifact`,
`inbox_board`, `inbox_take`),
`[keybindings.inbox_peek]` (the prompt: `peek_approve`, `peek_approve_always`,
`peek_deny`, `peek_type`, `peek_read_again`, `peek_go`, `peek_back`) and
`[keybindings.mail]` (the mailbox: `mail_down`, `mail_up`, `mail_page_down`,
`mail_page_up`, `mail_open`, `mail_reply`, `mail_focus_pane`, `mail_new`,
`mail_back`).
The footers name whatever key the config binds. The digits `1` to `9` are not
bindings: they pick an answer by the number the prompt shows. The selector
line and the reply, message and answer lines take text, so every key there is
typed. The selector line, the reason for a reject and the answer to a prompt are
edited as the reply is: the arrows and the word keys move the cursor, `ctrl+z`
undoes, and a paste keeps to one line.

```toml
[keybindings.inbox]
inbox_dismiss = ["x"]
```

The keys new with the agents view yield to your own: when your
`[keybindings.inbox]` already gives `n`, `R`, `a`, `x` or another new key to an
action, that action keeps it, and the new action has no key. `tuios keybinds
doctor` lists each one. `inbox_agents`, the old `tab` that switched the Inbox
to the agents view, is gone: `inbox_write` has `tab` now, and doctor lists a
config that still binds `inbox_agents`. A config that kept the old default
`inbox_deny_reason = ["n"]` moves it to `R`, so `n` starts a new agent, and
doctor says so. When your binding takes one of an action's new keys, only
that key yields: `inbox_open` keeps `right` when `l` is yours.

The help overlay (`ctrl+b ?`) has an Agents section with all of these, the
prefix chords, the rail's agent keys and the palette's `@` filter, read from
your config. In the command palette the agent actions are named "Agents: ...",
so typing `agent` lists them: what needs you, the mail for you, the oldest
waiting item, and the mailbox.

On an approval tuios holds (`[agents.approvals]`, see
[AGENT_STATE.md](AGENT_STATE.md#approvals-from-the-inbox)), `1` allows it
once, `2` always allows it and `3` rejects it; `enter` gives the prompt back to
the pane. The keys act on the row under the cursor, whose whole prompt, and
the rules `2` adds, are shown on its card, and only once it has been on
screen as it is for 0.4 seconds. `space` does not open a held approval: the
hook keeps its prompt off the pane until you answer, so there is nothing on
the screen to read.

### Review, triage and replies

These keys are bound for the agent review, triage, reply and approval work.
The triage keys work: `ctrl+b O`, `z`, `u` and `S` in the agents view, and `u` and
`z` on a rail agent row (see
[Snoozing, undo and unread](AGENT_STATE.md#snoozing-undo-and-unread)), and so
do the approval keys: `R`, `J`, `K`, `ctrl+d` and `ctrl+u` in the agents view (see
[Reject with a reason](AGENT_STATE.md#reject-with-a-reason)), and the reply keys:
`r` in the agents view on a finished or errored item, and `r` and `x` on a rail
agent row (see [Replying to an agent](AGENT_STATE.md#replying-to-an-agent)),
and the review keys: `ctrl+b v`, and `v` in the agents view and on a rail agent row
(see [The review overlay](#the-review-overlay) below). `ctrl+b v` works
whether or not an agent has been seen, since calling it is an explicit act;
on a pane with no git repository under it, the dock says so and nothing
opens. `ctrl+b O` does what an unbound key does until an agent has been seen:
after `ctrl+b` in terminal mode, the key is typed into the focused pane, so a
person who runs no agents keeps typing `O` into the pane. The prefix menu and
the help overlay list them only once an agent has been seen, like the rest of
the Agents section. Attached to a daemon without `mark-attention` (an older
one, found by asking its `list-verbs` once per attach), the agents view's `z`, `u`
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
| `v` | agents view | Review the changes in the item's pane |
| `z`, then `1` to `4` | agents view | Snooze the item: 15 minutes, 1 hour, until 9:00 tomorrow, or until it changes; any other key cancels. On a snoozed item, wake it |
| `u` | agents view | Undo the last dismiss or snooze, within 10 seconds |
| `S` | agents view | Show or hide snoozed items |
| `R` | agents view | Reject a held approval, or keep a plan planning, with a reason you type (`r` and `3` stay the plain reject) |
| `n` | agents view | Start a new agent |
| `c` | agents view | Compare the attempts of the fan under the cursor |
| `J`, `K`, `ctrl+d`, `ctrl+u` | agents view | Scroll the card, such as a long plan |
| `u` | rail agent row | Mark the pane's finished turn unread, for every client (not the pane in front of you) |
| `z` | rail agent row | Snooze what needs you in the pane: the agents view opens on it with the four lengths |
| `enter` | rail `+N at rest` line | Show the agent rows folded as at rest, until the rail lets go of the keyboard (after a click with the rail not focused, until a click outside the rail or a pane is focused) |
| `r` | agents view, on a finished or errored item | Reply to the agent: the editor under the thread, queued with `enter` and typed when the agent is at rest |
| `r` | rail agent row | Reply to the agent, the same line in the agents view; refused while the pane waits on a prompt |
| `v` | rail agent row | Review the pane's changes |
| `x` | rail agent row with messages queued | Drop the newest queued message still waiting; `u` on the row within 10 seconds queues it again. On a row with nothing queued, `x` opens the rail's menu as before. From `send-keys` or a tape, neither `x` nor the undo touches the queue, since both act as the person |

On a risky approval (one a [risk rule](AGENT_STATE.md#risk-rules) matched),
`1` and `2` allow only on a second press of the same key within 3 seconds, and
so do `a`, `A` and a digit in the peek; any other key resets the first press.
The line the first press shows names the time it lapses.
On a plan, `1` approves, `2` approves and accepts edits for the session, and
`3` keeps it planning; `1` and `2` work once the plan's last line has been
shown. The digits are not bindings.

The agents view's keys are `inbox_review`, `inbox_snooze`, `inbox_undo`,
`inbox_show_snoozed`, `inbox_deny_reason`, `inbox_detail_down` and
`inbox_detail_up` in `[keybindings.inbox]`, and the prefix chords are
`prefix_review`, `prefix_next_finished` and `prefix_agents_settings` in
`[keybindings.prefix_mode]`.
The agent rows' keys are a section of their own,
`[keybindings.sidebar_agents]` (`agent_unread`, `agent_snooze`,
`agent_reply`, `agent_review`, `agent_cancel_queued`, `agent_subagents`). It is consulted before
the rail's own keys and only while the cursor is on an agent row, the way
`[keybindings.sidebar_files]` is on a file row, so `r` and `x` mean the agent
on an agent row and keep renaming and opening the menu on every other row.
`agent_subagents` (`space`) opens or closes the subagent rows under the agent
row. It works on the agent row and on the subagent heading, and only when
`appearance.sidebar.subagent_collapse` is `expanded` or `collapsed`.

The reply editor takes every key: `enter` queues the reply, `ctrl+j`,
`shift+enter` or `\` then `enter` adds a new line, and `esc` closes it and
keeps the draft. A paste keeps its line breaks, and a long one shows as one
block. `ctrl+v` attaches the clipboard's image when it holds one and no text. `@`
opens a list of agents, panes and files to name: `tab` puts one in. `up` on
the first row brings back an earlier message to the same agent, `ctrl+r`
searches them, and `ctrl+g` opens the draft in `$EDITOR`.
`ctrl+o` on a selected image shows it larger, and `o` there opens it in the
desktop's image viewer. The editing keys are in docs/AGENT_STATE.md, "Replying to an agent".
They are not bindings yet. Attached to a daemon without `queue-prompt` (an older one), the
first reply says to restart it, and after that `r` does what it did before:
it says `r` replies to mail in the agents view, and renames on the rail.

### The review overlay

`ctrl+b v` (or `v` in the agents view or on a rail agent row) opens the diff of
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
that types `ш` is the US `i` key, so `ctrl+b` then that key opens the agents view. A
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

In window mode tuios tells the terminal to send every key as a code. tuios
resets this when it stops. If tuios cannot stop correctly, the terminal can
stay in this mode. This occurs when you use `kill -9` on tuios, or when an ssh
connection drops. The shell then shows codes such as `[97u` when you type. To
reset the terminal, run this command or close the tab:

```sh
printf '\033[=0;1u'
```

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

### Shift+Up, Shift+Down, Shift+PageUp and Shift+PageDown

`shift+up` and `shift+down` scroll the pane into its history. They do this
only when all of these are true:

- The program is on the main screen.
- The program did not turn on a mouse mode.

`shift+pgup` and `shift+pgdown` follow the same rules, but scroll one visible
pane page at a time. In other panes, tuios sends the keys to the program.
Programs that use the full screen, such as nvim, less and htop, get them. When
the pane is in copy mode, the keys scroll. This is the same rule as the mouse
wheel, and the same rule as kitty.

To change the keys, set `terminal_scroll_up`, `terminal_scroll_down`,
`terminal_scroll_page_up` and `terminal_scroll_page_down` in
`[keybindings.terminal_mode]`.

## macOS

Option is a compose key on macOS unless the terminal is told otherwise. An
Option chord then arrives as a character, not as Alt. Terminal.app and iTerm2
work like this until you change their settings.

The same character can come from two keys. On a US keyboard, Option+3 types
`£`. On a UK keyboard, Shift+3 types `£`. tuios reads the keyboard layout of
the Mac when it starts:

- On a US or ABC layout, tuios runs the `opt+` binding for the character.
  Option+3 types `£` and switches to workspace 3.
- On any other layout, the character goes to the pane. tuios also does this
  when it cannot read the layout.

If you change the layout while tuios runs, tuios reads it again when such a
character arrives and the last read is more than 30 seconds old. The new
layout applies from the next key. A tuios that serves SSH or web clients from
a Mac uses the layout of that Mac.

`keyboard_layout` overrides the layout that tuios reads. With
`keyboard_layout = "us"`, tuios runs the binding. With
`keyboard_layout = "other"`, the character goes to the pane.

To use the `opt+` bindings on every layout, set your terminal to send Option
as Alt. See [The fix](#the-fix).

Some terminals, such as Ghostty and kitty, send the character and also report
Alt. tuios then runs the `opt+` binding on every layout. For example,
Option+8 types `•` and switches to workspace 8.

To set the behaviour yourself, use `option_glyphs`:

```toml
[keybindings]
option_glyphs = "auto"   # auto, alt, bind, type
```

- `auto` uses the layout of the Mac, as above. This is the default.
- `alt` runs the binding only when the terminal reports Alt.
- `bind` always runs the binding. Use it only on a US keyboard.
- `type` always sends the character to the pane. Use it if you type
  characters such as `#`, `•` or `{` with Option.

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
| Tern | set `option_as_alt` in Tern's `settings.json` |

Ghostty needs two more lines, because its own keybinds rewrite the arrows
before any encoding happens:

```
keybind = alt+left=unbind
keybind = alt+right=unbind
```

tuios shows this on screen one time, when an Option chord arrives as a
character and not as Alt. On a layout other than US or ABC it does not show
it, because the character can be text.

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
| `ctrl+b K` / `ctrl+b J` | Previous and next agent pane |
| `ctrl+b a` | Launcher |
| `ctrl+b 0` to `ctrl+b 9` | Jump to a pane |

The prefix stays armed for half a second after a command worth repeating, so
`ctrl+b` then left left left walks three panes on one prefix press. This is
tmux's `repeat-time`, and `appearance.prefix_repeat_time` changes it. Zero
turns it off.

These actions repeat when you bind them under the leader: the focus moves,
`next_window`, `prev_window`, `next_workspace`, `prev_workspace`, the
`resize_pane_*` steps, the `resize_master_*` and `resize_height_*` steps, and
the `scroll_*` moves and width cycle. `ctrl+b o` and `ctrl+b O` repeat too.
Other actions run once.

`keybindings.repeat` sets your own list, as tmux `bind -r` does. The list
replaces the default list. An empty list turns repeat off:

```toml
[keybindings]
# n and p switch workspaces once, so the next n or p you type reaches the shell
repeat = ["terminal_focus_left", "terminal_focus_right", "terminal_focus_up",
          "terminal_focus_down"]

[keybindings.prefix_mode]
next_workspace = ["n"]
prev_workspace = ["p"]
```

An action in the list repeats only when a key runs it after the leader. tuios
warns about an entry with no such key, and about a misspelt action.

With `keybindings.lock` on, there is no repeat window in terminal mode. A
repeatable action keeps its menu open until you press `esc`, with no time
limit. The open menu takes only repeatable keys. Any other key goes to the
pane, and tuios locks again. See [Lock mode](#lock-mode).

Workspace switching on `opt+1` to `opt+9` works with no configuration on a
US or ABC keyboard layout. It also works in a terminal that reports Alt with
the character, such as Ghostty and kitty. On other layouts in Terminal.app
and iTerm2, Option+1 types its character and does not switch. Set the
terminal to send Option as Alt.
