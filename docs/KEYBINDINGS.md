# Keybindings

The keybinding reference lives on the docs site: https://tuios.gaurav.zip/docs/keybindings

Every binding lives in one of the 19 sections under `[keybindings]` in `config.toml` and is rebindable; the site page lists each section's defaults, the prefix chords, copy mode, and the key syntax.

To inspect your own effective bindings, use the binary rather than any document: `tuios keybinds list`, `tuios keybinds doctor` for conflicts, `tuios keybinds explain <key>` for everything one key does, or the in-app keybind manager on `Ctrl+B k`.

## The Inbox

Everything waiting for you in every session is one list, the Inbox. See
[AGENT_STATE.md](AGENT_STATE.md#the-inbox) for what goes in it.

| Keys | What it does |
|---|---|
| `ctrl+b i` | Open the Inbox |
| `ctrl+b o` | Go to the oldest item that needs you; `o` again, inside the repeat window, goes to the next |
| `ctrl+b M` | Open the Inbox on its mail (`m` there opens the whole mailbox) |

Inside it: `j` and `k` move, `enter` goes to the pane, `space` reads an
approval's or a question's prompt, `r` replies to mail, `d` dismisses, `f` steps
through the kinds, `m` opens the mailbox, `esc` closes. `ctrl+b o` is `o`
because `ctrl+b a` is the launcher's.

In the prompt `space` opens: a digit chooses that option, `a` approves, `A`
approves and does not ask again, `d` denies, `tab` types an answer, `r` reads
the prompt again, `enter` goes to the pane, `esc` goes back to the list. See
[Answering a prompt without attaching](AGENT_STATE.md#answering-a-prompt-without-attaching).

On an approval the Inbox is holding (`[agents.approvals]`, see
[AGENT_STATE.md](AGENT_STATE.md#approvals-from-the-inbox)), `1` allows it
once, `2` always allows it and `3` denies it; `enter` gives the prompt back to
the pane. The keys act on the item under the cursor, whose whole prompt, and
the rules `2` adds, are shown under the list, and only once it has been on
screen as it is for 0.4 seconds. `space` does not open a held approval: the
hook keeps its prompt off the pane until the Inbox answers, so there is
nothing on the screen to read.

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
