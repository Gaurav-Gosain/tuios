# Hints

Hints mode puts a short label on the useful text in the focused pane. Type a
label to copy the text. It works like tmux-fingers and the kitty hints kitten.
Hints mode can also put labels on all panes at the same time. See
[All panes](#all-panes).

## Use it

1. Press `Ctrl+B F`. The labels show and the rest of the pane goes dim. The
   mode pill in the dock shows `HINTS`, and the other end of the dock shows
   the keys.
2. Type a label. tuios copies the text and closes hints mode.

| Keys | What it does |
|---|---|
| a label, such as `a` or `ls` | Copy the text |
| the label with `Shift`, such as `A` | Copy the text and type it into the pane |
| the label with `Ctrl`, such as `Ctrl+A` | Open a URL or a path |
| `backspace` | Remove the last letter you typed |
| `?` | Close hints mode and show all of its keys in the help |
| `esc`, or the leader key | Close hints mode |
| `q` | Close hints mode, when `q` is not a label letter |
| `Ctrl+C`, `Ctrl+G` | Close hints mode, when `c` or `g` is not a label letter |

The nearest text to the cursor gets the shortest label. The same text gets the
same label every time it shows. A key that starts no label does nothing.

When `c` or `g` is a label letter, `Ctrl+C` and `Ctrl+G` open that label like
any other `Ctrl` and label. `g` is in the default letters, so `Ctrl+G` opens
the label `g`. Use `esc` to close. The leader key always closes hints mode,
also when it is `Ctrl` and a label letter.

Hints mode keeps all input from the pane while the labels show. tuios drops a
paste, and it drops a key release and a mouse move over the pane. A mouse
click or the mouse wheel closes hints mode first and then works as usual.
Hints mode also closes when its pane closes, when the focus moves to a
different pane, and when you change the workspace.

`Shift` and a label types the text. A terminal that reports Caps Lock (the
kitty keyboard protocol) lets tuios read an upper case letter from Caps Lock
as a plain label letter. In a terminal that does not report Caps Lock, turn
Caps Lock off before you type a label.

The copy uses the same path as a mouse copy. tuios writes the clipboard with
OSC 52, and on a local client also with the system clipboard tool.

You can also open hints mode from the command palette: search for `hints`.

## All panes

The `hints_all_panes` action puts labels on all panes that the workspace
shows. This includes tiled panes, floating panes and the focused pane.

- tuios gives no label to text that you cannot see. This includes a
  minimized pane, a pane behind a zoomed pane, a pane off the screen, and
  text under a different pane.
- Each label is different on all panes.
- The focused pane gets the shortest labels. The panes nearest to it get the
  next shortest labels.
- The same text gets the same label on all panes. A path or a `file://` URL
  gets one label for each pane, because each pane has its own folder.
- A label copies the text from any pane.
- `Shift` and a label types the text into the focused pane. The text can come
  from a different pane.
- `Ctrl` and a label opens the text from the pane that shows it. tuios uses
  that pane's folder and machine.
- Hints mode closes when one of these panes closes, moves or changes size.

The action has no default key. Run it from the command palette, or bind it:

```toml
[keybindings.prefix_mode]
hints_all_panes = ["A"]
```

To make `Ctrl+B F` put labels on all panes, set `hints.all_panes`:

```toml
[hints]
all_panes = true
```

## What hints mode finds

| Name | Examples |
|---|---|
| `url` | `https://example.com/a`, `git@github.com:user/repo.git` |
| `path` | `/etc/hosts`, `./run.sh`, `~/notes.md`, `main.go:12:5` |
| `diff` | The file in `diff --git a/x b/x`, `--- a/x`, and `modified: x` |
| `sha` | `149c8a8f`, a full 40-character hash |
| `ip` | `10.0.0.1`, `10.0.0.0/8`, `192.168.1.2:8080`, `fe80::1` |
| `uuid` | `550e8400-e29b-41d4-a716-446655440000` |
| `color` | `#1e1e2e`, `#fa0` |
| `hex` | `0xdeadbeef` |
| `number` | Numbers of 4 digits or more |
| `email` | `ops@example.com` |
| `id` | `pod/web-1`, `deployment.apps/web`, pod names, `sha256:` digests |

`path` and `email` accept letters in all scripts, with accents and
combining marks. The other built-in patterns use only ASCII.

A URL ends at a space, a quote, a backtick or an angle bracket. A `)` or a
`]` ends the URL when the URL did not open it. A pair that the URL opens
stays in it, as in `https://en.wikipedia.org/wiki/Go_(language)`. tuios
removes `.,;:!?` from the end. So the markdown badge
`[![x](https://a.example/b.svg)](https://a.example/c)` gives two URLs. The
pointer uses the same rules to find a link.

A path that starts with `/` must not come directly after `<`, a letter, a
digit or `_`. So `</p>` in HTML is not a path.

Hints mode reads only the text on the screen. If you scroll the pane back, it
reads the lines you scrolled to. A URL that wraps onto the next row is one
match. tuios joins two rows only when the terminal wrapped the text. A line
that fills the row and then ends is not joined to the next line. Both
terminal backends record the wrap, and the record goes with the text when
you attach again or change the workspace. On the ghostty backend, the wrap
from the newest history row into the first screen row is not restored, so
tuios reads that row as a line that ends. A daemon from before this change
sends no wrap record, and tuios then reads every restored row as a line
that ends.

## Open

`Ctrl` and a label opens the text:

- A URL opens in your browser. A remote client (`tuios ssh`, the web client)
  copies the URL. It cannot open a browser on your machine.
- A path or a `file://` URL opens in a new pane with `$EDITOR`. tuios removes
  a `:line:col` at the end. A relative path starts in the pane's directory.
- Other text is copied.

tuios opens a file only when the file is on this machine. It copies the path
and tells you why in these cases:

- The pane runs on another machine.
- The session runs on another machine (`tuios attach` to a host).
- The client is remote (`tuios ssh`, the web client).
- The pane runs `ssh`, `autossh`, `mosh`, `et`, `telnet`, `tsh`, `kitten`
  (for example `kitten ssh`), `docker`, `kubectl` or `podman`.
- The shell reported a folder on another machine.
- The path is relative and a program runs in front of the shell. tuios knows
  only the folder the shell reported, and the program can be somewhere else.
- The path is relative and tuios does not know the pane's folder.

tuios never gives the text to a shell. It gives the text to the opener as one
argument. Only `http`, `https`, `mailto`, `ftp` and `ftps` URLs open.

## Settings

Put the settings in `config.toml`:

```toml
[hints]
# Built-in patterns: "all", "none", or names such as "url,path,sha".
builtins = "all"
# More patterns, as Go regular expressions. A group named "match"
# sets the part that is copied.
patterns = ['JIRA-\d+', 'branch: (?P<match>\S+)']
# The letters of the labels, easiest first. Lowercase letters only.
alphabet = "asdfghjkl"
# Let Ctrl and a label open the text.
open = true
# How much the text around the labels dims, in percent (10 to 90).
dim = 60
# Put labels on all panes on the workspace, not only on the focused pane.
all_panes = false
```

Your own patterns come before the built-in patterns. tuios warns about a
pattern that does not compile when it reads the config, and hints mode skips
that pattern.

`hints.builtins`, `hints.alphabet`, `hints.open`, `hints.dim` and
`hints.all_panes` are also on the settings page (Selection tab) and work with
`tuios set-config`.
`hints.patterns` is a list, so you set it in the file.

If your config already binds `F` in `[keybindings.prefix_mode]` to a
different action, tuios keeps your binding and gives `hints` no key.
`tuios keybinds doctor` tells you this. To use a different key, bind the
`hints` action:

```toml
[keybindings.prefix_mode]
hints = ["f"]
```
