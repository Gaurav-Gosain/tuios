# tuios in a status bar

`tuios bar-status` prints what your agents need from you as one JSON line.
A status bar shows it as a count, a colour and a tooltip.

```sh
tuios bar-status
```

```json
{"text":"1","alt":"needs-you","tooltip":"1 item needs you.\nApproval  work/api  Allow Bash: make deploy\n2 agents work.","class":"needs-you","needs_you":1,"mail":0,"review":0,"working":2,"rows":[...]}
```

## The classes

`class` and `alt` hold the same word. The first one that is true wins.

| Class | When | `text` |
| --- | --- | --- |
| `needs-you` | An agent waits on an approval, a question or a plan, or an agent failed | The number of these items |
| `mail` | An agent sent you mail | The number of messages |
| `working` | An agent works | The number of agents that work |
| `review` | An agent finished a turn that nobody looked at | The number of turns |
| `idle` | Nothing of the above | Empty |
| `error` | The daemon runs but refused a call of `bar-status`. The tooltip says why | Empty |
| `off` | The tuios daemon does not run | Empty |

The line covers every session on this machine. The items of linked hosts
count too, so an approval on another machine shows here.

`rows` lists the items that need you, oldest first, at most 8. Each row has
`kind`, `host`, `session`, `window`, `name`, `summary` and `since` (Unix
time in nanoseconds). The tooltip lists the same rows. Waybar reads a tooltip
as Pango markup, so tuios escapes `&`, `<` and `>` in the words of an agent.

## Follow the state

`tuios bar-status --follow` prints a line at the start, then a new line each
time the line changes. It waits on the daemon's event stream, so it does no
work while nothing changes. When the daemon stops, it prints an `off` line
and waits for a daemon to start. When the daemon refuses a call, it prints an
`error` line and tries again after a pause that grows to a minute. It does not
exit until it is stopped.

## Waybar

Add a custom module to the Waybar config. The icons are Nerd Font glyphs:

```jsonc
"custom/tuios": {
  "exec": "tuios bar-status --follow",
  "return-type": "json",
  "format": "{icon} {}",
  "format-icons": {
    "needs-you": "",
    "mail": "",
    "working": "",
    "review": "",
    "idle": "",
    "error": "",
    "off": ""
  },
  "on-click": "tuios attach"
}
```

Then put `"custom/tuios"` in `modules-left`, `modules-center` or
`modules-right`. Waybar hides the module while `text` is empty. Colour each
class in `style.css`:

```css
#custom-tuios.needs-you { color: @critical; }
#custom-tuios.working { color: @accent; }
```

## The Omarchy bar

On Omarchy 4, `tuios integration install omarchy` puts the tuios agents widget
in the bar. It follows the state as `--follow` does. See
[OMARCHY.md](OMARCHY.md).

For a module of your own, the Omarchy bar runs a command at an interval. Add
an entry to
`bar.layout` in `~/.config/omarchy/shell.json`:

```json
{ "id": "tuios", "type": "command", "exec": "tuios bar-status", "interval": 5,
  "tooltip": "tuios", "onClick": "xdg-terminal-exec tuios attach" }
```
