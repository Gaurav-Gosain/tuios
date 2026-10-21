# plot

[![Go Reference](https://pkg.go.dev/badge/tuios.dev/plot.svg)](https://pkg.go.dev/tuios.dev/plot)

`tuios.dev/plot` draws charts as terminal cells. It has ten chart kinds:
bar (horizontal and vertical), line, area, sparkline, gauge, histogram,
heatmap, pie (a donut or a full disc), mirrored bars, and inline bars for
table cells.

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

## Mirrored bars

`Mirror` draws two series around one baseline: `Up` rises above it and
`Down` hangs below it. Use it for download and upload, tokens in and out, or
reads and writes. Each value is one column of braille dots, and the newest
value is at the right edge.

```go
m := plot.Mirror{
	Up:    plot.Series{Name: "▼ 35.1 MB/s", Values: rx, Role: plot.Series3},
	Down:  plot.Series{Name: "▲ 207 KB/s", Values: tx, Role: plot.Series5},
	Peaks: true, // • 120M over the highest column of each half
	Stats: true, // peak · avg · Σ at the right of each corner row
}
g := m.Draw(100, 10, plot.Braille)
```

- Each half scales to its own peak. `Shared` gives both halves one scale.
- The columns are graded by height. The row next to the baseline is
  dimmest. `UpRamp` and `DownRamp` set the roles, low to high.
- Where a column has no value, the baseline shows as a dotted track.
- In `Blocks` mode each value is one cell of eighth blocks. In `ASCII`
  mode it is `#` and `.`.

The idea for mirrored and inline bars comes from
[iptop](https://github.com/franlol/iptop) (MIT), a network monitor by franlol.

## Inline bars

`InlineBar` is a bar one row high for a table cell. It fills the cell to
`Value / Max` and draws no label. By default it is a lower half block,
exact to half a cell, so the bars of a table stay apart. `Style: plot.InlineBlocks` fills the row with eighth blocks, exact to an
eighth of a cell. `DivergingBar`
grows left from a centre mark for `Left` and right for `Right`, such as
download and upload of one service.

```go
for _, h := range hosts {
	bar := plot.InlineBar{Value: h.Rate, Max: busiest, Track: true}
	cell := bar.Draw(12, 1, plot.Blocks) // ▄▄▄▄▄▖ in the bar column
	_ = cell
}
```

- Give every row the same `Max`, so the bars compare.
- `Track` shows the empty part in the `track` role.
- Block elements that grow from the right come only in an eighth and a
  half. With `InlineBlocks` on a track, the left side of a `DivergingBar` is
  still exact to an eighth: its partial cell is a left block in the track
  colour on a ground of the bar colour.
- A value above zero always shows.

## Fades

`Fade(role, k)` returns a role faded `k` steps toward the ground, such as
`series1/2`. `MaxFade` is 3. Map fade `k` to the base colour mixed about `k`
fifths of the way toward the ground. At 16 colours, use the normal ANSI slot
of a bright colour for fade 2 and 3, or the base colour. `Role.Faded` splits
the name into the base role and the step.

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
