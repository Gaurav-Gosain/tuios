# swatch

[![Go Reference](https://pkg.go.dev/badge/tuios.dev/swatch.svg)](https://pkg.go.dev/tuios.dev/swatch)

`tuios.dev/swatch` reads the colour theme of the desktop your program runs
on. It gives you a `Palette` with semantic roles: a background, a text
colour, an accent, a border, the status colours and the sixteen ANSI colours.

The first source is [Omarchy](https://omarchy.org). The `omarchy` package
reads the theme Omarchy has applied, from Omarchy 4 and from Omarchy 3. Its
parser is a port of Omarchy's own `omarchy-theme-color`, so a theme resolves
to the colours Omarchy gives every other app. A test holds the port to the
script's output for every theme Omarchy ships.

`swatch.Watch` follows the theme while your program runs, and the
`teaswatch` package turns each change into a Bubble Tea message.

`swatch` and `omarchy` use the standard library and fsnotify, and
`termconfig` adds a TOML parser. Theme files
come from strangers, so swatch parses colours only. Nothing in a theme file is executed or
expanded. An Omarchy file over 64 KiB, or a terminal config over 1 MiB, is not read.

tuios uses swatch to follow the Omarchy theme.

## Install

```sh
go get tuios.dev/swatch
```

The go command reads the import path from tuios.dev. Go 1.25 or later can
fetch it directly. With the default module proxy, any Go version can fetch it.

## Read the applied Omarchy theme

```go
package main

import (
	"errors"
	"fmt"

	"tuios.dev/swatch"
	"tuios.dev/swatch/omarchy"
)

func main() {
	p, err := omarchy.Current().Load()
	if errors.Is(err, swatch.ErrNotFound) {
		fmt.Println("Omarchy is not installed")
		return
	}
	if err != nil {
		panic(err)
	}
	fmt.Println(p.Name, swatch.Hex(p.Accent), p.Light)
	// Tokyo Night #7aa2f7 false
}
```

The fields are `color.Color` values, which Lipgloss v2 takes as they are.

## Follow the theme

```go
w, err := swatch.Watch(omarchy.Current())
if err != nil {
	panic(err) // the platform's file watcher could not be opened
}
defer w.Close()
for p := range w.Changes() {
	fmt.Println("now", p.Name)
}
```

The watcher holds one inotify handle and two goroutines. It runs no timer and
polls nothing while the theme stays the same. It reads the theme 50 ms after
the last change, so one Omarchy switch is one read. A switch to the theme
already in force sends nothing. A directory that does not exist yet is
watched from its nearest parent, so Omarchy installed later is still seen.

## Bubble Tea

```go
import "tuios.dev/swatch/teaswatch"

func (m model) Init() tea.Cmd { return teaswatch.Listen(m.watcher) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case teaswatch.ChangedMsg:
		m.styles = newStyles(msg.Palette)
		return m, teaswatch.Listen(m.watcher)
	}
	return m, nil
}
```

`teaswatch.Terminal` follows the palette of the terminal itself. It asks
for OSC 10, 11 and the sixteen OSC 4 colours, turns on the light and dark
reports (mode 2031), and asks again after each report.

## Read a terminal's colour scheme

The `termconfig` package reads the colour scheme of a kitty, ghostty,
alacritty or wezterm config file. It tells the format from the content.

```go
p, format, err := termconfig.Load(os.ExpandEnv("$HOME/.config/kitty/current-theme.conf"))
```

`termconfig.Source(path)` follows the file with `swatch.Watch`. `Raw` holds
only the colours the file names, under `foreground`, `background`, `cursor`
and `color0` to `color15`.

## List the installed themes

```go
for _, t := range omarchy.Themes() {
	p, err := t.Load()
	if err != nil {
		continue
	}
	fmt.Println(t.Name(), swatch.Hex(p.Background))
}
```

## The palette

| Role | Omarchy key |
|---|---|
| `Background`, `Foreground` | `background`, `foreground` |
| `Cursor` | `bright_foreground` |
| `SelectionBackground`, `SelectionForeground` | `selection_background`, `selection_foreground` |
| `Surface` | `lighter_background` |
| `Muted` | `muted` |
| `Accent` | `accent` |
| `Border` | the first colour of `hyprland_active_border`, else `accent` |
| `Error`, `Success`, `Warning`, `Info` | `red`, `green`, `yellow`, `blue` |
| `ANSI` 0 to 7 | `background`, `red`, `green`, `yellow`, `blue`, `magenta`, `cyan`, `foreground` |
| `ANSI` 8 to 15 | `muted`, then the `bright_` colours, then `bright_foreground` |
| `Light` | `mode = "light"`, a `light.mode` file, or a light background |

`Raw` holds every key the theme defines or Omarchy derives, such as
`orange`, `brown` and `dark_background`.

The ANSI mapping is the one in Omarchy's kitty, ghostty and foot templates,
so a palette matches a terminal that Omarchy themed.

## Where Omarchy keeps the theme

| Omarchy | Applied theme |
|---|---|
| 4 | `~/.local/state/omarchy/current/theme/colors.toml` |
| 3 | `~/.config/omarchy/current/theme/colors.toml` |

Set `SWATCH_OMARCHY_DIR` to a `current` directory to read that one instead.

The API is at v0 and can change between minor versions.

## Licence

MIT. The Omarchy theme fixtures in `omarchy/testdata` are MIT too, under
Omarchy's own licence in `omarchy/testdata/LICENSE.omarchy`.
