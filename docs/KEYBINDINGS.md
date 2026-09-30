# Keybindings

The keybinding reference lives on the docs site: https://tuios.dev/docs/keybindings

Every binding lives in one of the 23 sections under `[keybindings]` in `config.toml` and is rebindable; the site page lists each section's defaults, the prefix chords, copy mode, and the key syntax.

To send the leader to the program in the pane, press it two times in terminal
mode. The pane gets the leader that `leader_key` names, such as `ctrl+a`,
encoded as the pane expects: CSI u for a pane using the Kitty keyboard
protocol, legacy bytes otherwise. A leader with no legacy encoding is dropped.

To inspect your own effective bindings, use the binary rather than any document: `tuios keybinds list`, `tuios keybinds doctor` for conflicts, `tuios keybinds explain <key>` for everything one key does, or the in-app keybind manager on `Ctrl+B k`.

## Editing sidebar files

Focus the sidebar with `s` in window mode or `Ctrl+B e`, then select a file.
`Enter` opens a folder or copies the path of a file. `Shift+Enter` opens a text
file in a new pane. The pane runs the editor set under Settings, Sidebar, File
editor. A folder keeps its navigation action. tuios does not edit a file that is
not text. The binding is `file_edit` in `[keybindings.sidebar_files]`.

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
| `opt+`, `option+` | `alt+` | macOS only |
| `cmd+`, `command+` | `super+` | all platforms |
| `control+` | `ctrl+` | all platforms |

The order of the modifiers does not matter, so `shift+ctrl+x` is `ctrl+shift+x`. This applies to `leader_key`, to every binding table, and to `tuios keybinds explain`, `free` and `unbind`. `tuios keybinds explain opt+f12` shows `opt+f12 (tuios reads it as alt+f12)`. `tuios keybinds doctor` lists each key that tuios cannot read.

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
they shorten in steps: `ctrl+` becomes `^`, `alt+` becomes `M-` and `shift+`
becomes `S-`; then labels are dropped from the last hint backwards, keeping the
keys; then the last hints are dropped for `…`.

An empty list says why it is empty in the middle of the panel, with the one key
worth pressing next under it. A list that is still loading draws nothing for
its first half second, so a fast load never flashes a "reading" line.

## Hints

`Ctrl+B F` puts a short label on each URL, path, hash, address and number in
the focused pane. Type a label to copy the text. Type it with `Shift` to copy
the text and type it into the pane. Type it with `Ctrl` to open a URL or a
path. `esc` closes. See [HINTS.md](HINTS.md) for the patterns and the
`[hints]` settings. The action is `hints`, so you can bind it to a different
key.

The `hints_all_panes` action puts labels on all panes that the workspace
shows. The focused pane gets the shortest labels. `Shift` and a label types
the text into the focused pane. This action has no default key. Bind it, or
run it from the command palette. Set `hints.all_panes = true` to make
`Ctrl+B F` do the same.

## Scratch session

`Ctrl+B g` shows the `scratch` session in a popup over the layout. Press it
again to hide the popup. The session keeps running between shows. The key
works from inside the popup. The action is `toggle_scratch`, and `[scratch]`
sets the session name and the popup size. See
[SESSIONS.md](SESSIONS.md#the-scratch-session).

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

tuios cannot put two actions on one key. `tuios send-keys` cannot do it either,
because a key from `send-keys` does not go to copy mode.

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
`prefix_review` and `prefix_next_finished` in `[keybindings.prefix_mode]`.
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

## macOS

Option is a compose key on macOS unless the terminal is told otherwise, so an
Option chord usually arrives as a character rather than as Alt. tuios reads the
composed characters back into the chord they stand for, which covers most of
them, but two kinds cannot be recovered:

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
