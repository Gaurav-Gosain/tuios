# Hints

Hints mode puts a short label on the useful text in the focused pane. Type a
label to copy the text. It works like tmux-fingers and the kitty hints kitten.

## Use it

1. Press `Ctrl+B F`. The labels show and the rest of the pane goes dim.
2. Type a label. tuios copies the text and closes hints mode.

| Keys | What it does |
|---|---|
| a label, such as `a` or `ls` | Copy the text |
| the label with `Shift`, such as `A` | Copy the text and type it into the pane |
| the label with `Ctrl`, such as `Ctrl+A` | Open a URL or a path |
| `backspace` | Remove the last letter you typed |
| `esc`, `Ctrl+C` | Close hints mode |
| `q` | Close hints mode, when `q` is not a label letter |

The nearest text to the cursor gets the shortest label. The same text gets the
same label every time it shows. A key that starts no label does nothing.

The copy uses the same path as a mouse copy. tuios writes the clipboard with
OSC 52, and on a local client also with the system clipboard tool.

You can also open hints mode from the command palette: search for `hints`.

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

Hints mode reads only the text on the screen. If you scroll the pane back, it
reads the lines you scrolled to. A URL that wraps onto the next row is one
match.

## Open

`Ctrl` and a label opens the text:

- A URL opens in your browser. A remote client (`tuios ssh`, the web client)
  copies the URL. It cannot open a browser on your machine.
- A path opens in a new pane with `$EDITOR`. tuios removes a `:line:col` at
  the end. A relative path starts in the pane's directory. A pane on another
  machine copies the path.
- Other text is copied.

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
```

Your own patterns come before the built-in patterns. tuios warns about a
pattern that does not compile when it reads the config, and hints mode skips
that pattern.

`hints.builtins`, `hints.alphabet`, `hints.open` and `hints.dim` are also on
the settings page (Selection tab) and work with `tuios set-config`.
`hints.patterns` is a list, so you set it in the file.

To use a different key, bind the `hints` action:

```toml
[keybindings.prefix_mode]
hints = ["f"]
```
