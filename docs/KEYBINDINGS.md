# Keybindings

The keybinding reference lives on the docs site: https://tuios.dev/docs/keybindings

Every binding lives in one of the 23 sections under `[keybindings]` in `config.toml` and is rebindable; the site page lists each section's defaults, the prefix chords, copy mode, and the key syntax.

To inspect your own effective bindings, use the binary rather than any document: `tuios keybinds list`, `tuios keybinds doctor` for conflicts, `tuios keybinds explain <key>` for everything one key does, or the in-app keybind manager on `Ctrl+B k`.

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
`mail_page_up`, `mail_open`, `mail_reply`, `mail_focus_pane`, `mail_back`).
The footers name whatever key the config binds. The digits `1` to `9` are not
bindings: they pick an answer by the number the prompt shows. The selector
line and the reply and answer lines take text, so every key there is typed.

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
agent row (see [Replying to an agent](AGENT_STATE.md#replying-to-an-agent)).
The rest are being built: until its work lands, a key does what it did before it
was bound: nothing in the Inbox, the rail's own binding on an agent row, and
after `ctrl+b` in terminal mode, the key typed into the focused pane, with no
repeat window opened. `ctrl+b O` also does that until an agent has been seen,
so a person who runs none keeps typing `O` into the pane. The prefix menu and
the help overlay list them only once an agent has been seen, like the rest of
the Agents section. Attached to a daemon without `mark-attention` (an older
one, found by asking its `list-verbs` once per attach), the Inbox's `z`, `u`
and `S` and the rail's `z` are not offered and do what an unbound key does,
and the rail's `u` clears only this client's seen marks.

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
