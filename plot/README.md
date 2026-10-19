# plot

[![Go Reference](https://pkg.go.dev/badge/tuios.dev/plot.svg)](https://pkg.go.dev/tuios.dev/plot)

`tuios.dev/plot` draws charts as terminal cells. It has eight chart kinds:
bar (horizontal and vertical), line, area, sparkline, gauge, histogram,
heatmap and pie (a donut or a full disc).

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

Heatmaps use whole cells, or half blocks when the rows do not fit. A pie is a
disc of half-block pixels. In ASCII each series of a line chart, each slice
of a pie and each heat level has its own mark, so they stay apart without
colour.

## Axes and legends

- Columns, lines, areas and histograms can have a y axis with round tick
  labels, a base line, and the first, middle and last x label under it.
- Columns whose values all sit far above zero start from a round base under
  the smallest value. The axis labels that base.
- Horizontal bars keep a value column at the right that never clips. Labels
  give way first.
- `Line.Legend` and `Pie.Legend` return the items for a legend, and
  `DrawLegend` draws them.

A cell can also have a background role, `Cell.Bg`. Half blocks use it to show
two pixels in one cell. The heat roles `heat1` to `heat5` are a ramp from low
to high that the host derives.

## Rules

- Every chart is total. NaN and infinite values count as missing. Empty data
  draws an empty grid.
- A grid is always the size you ask for, and each row is exactly that wide.
- Sizes that a caller could pass unchecked are bounded: a grid is at most 4096 cells a side, a histogram has at most one bin per cell and 1000 bins, a line draws at most 64 series, and a pie draws 12 slices, the rest folded into one called "other".
- Labels are cut with an ellipsis to fit. Control characters are dropped and
  invalid UTF-8 shows as `?`.
- A line chart with more values than cells is reduced with largest triangle
  three buckets, which keeps the peaks.

Widths come from `github.com/charmbracelet/x/ansi`, the same width functions
that tuios uses. The package imports nothing else outside the standard
library.

`tuios.dev/loom` uses this package to draw the TML chart components.
