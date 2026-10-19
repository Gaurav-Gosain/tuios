# plot

[![Go Reference](https://pkg.go.dev/badge/tuios.dev/plot.svg)](https://pkg.go.dev/tuios.dev/plot)

`tuios.dev/plot` draws charts as terminal cells. It has six chart kinds:
bar (horizontal and vertical), line, area, sparkline, gauge and histogram.

A chart draws into a grid of the size you give it. Each cell holds one
grapheme and a role, such as `series1`, `mute` or `track`. Your program maps
each role to a colour at the colour depth of the terminal. The package holds
no colour and writes no escape sequence.

```go
bar := plot.Bar{
	Values:     []float64{6.2, 4.8, 3.1},
	Labels:     []string{"session", "app", "vt"},
	Horizontal: true,
}
g := bar.Draw(30, 3, plot.Blocks)
fmt.Println(g.String())
```

```
session ██████████████████ 6.2
app     █████████████▉     4.8
vt      █████████          3.1
```

## Modes

A mode sets the glyphs a chart can use. Use the best mode that the terminal
and its font can show.

| Mode | Lines and areas | Bars, sparklines, gauges |
| --- | --- | --- |
| `Braille` | Braille dots, 2 by 4 per cell | Eighth blocks |
| `Blocks` | Eighth blocks | Eighth blocks |
| `ASCII` | `*` and `:` | `#`, `-` and `[ ]` |

In ASCII each series of a line chart has its own mark, so the series stay
apart without colour.

## Rules

- Every chart is total. NaN and infinite values count as missing. Empty data
  draws an empty grid.
- A grid is always the size you ask for, and each row is exactly that wide.
- Labels are cut with an ellipsis to fit. Control characters are dropped and
  invalid UTF-8 shows as `?`.
- A line chart with more values than cells is reduced with largest triangle
  three buckets, which keeps the peaks.

Widths come from `github.com/charmbracelet/x/ansi`, the same width functions
that tuios uses. The package imports nothing else outside the standard
library.

`tuios.dev/loom` uses this package to draw the TML chart components.
