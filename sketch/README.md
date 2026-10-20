# sketch

`tuios.dev/sketch` draws Mermaid diagrams as SVG, in pure Go.

It is a port of [pele](https://github.com/obsidianmd/pele), the Mermaid
renderer by [kepano](https://github.com/kepano) of
[Obsidian](https://obsidian.md), under the MIT licence. The layout, the
shapes, the safety rules and the test corpus are pele's work. The port
follows pele v1.7.0, commit `8603bf3313add9143cd417015d4a6bd6c522dada`.
[LICENSE](LICENSE) has the notice of pele and of the port.
[raster/fonts/NOTICE](raster/fonts/NOTICE) has the licences of the fonts that
sketch/raster draws text in: Arimo (SIL Open Font License 1.1) and Go Mono.

## Status

It draws flowcharts, sequence diagrams, state diagrams, class diagrams, ER
diagrams, pie charts, mindmaps, gantt charts, XY charts, timelines, quadrant
charts and user journeys. Each other diagram type gives an error with the code
`unsupported-diagram` until its port is done.

The output is the same as the output of pele, byte for byte. The test suite
compares each diagram with an SVG that pele drew from the same source.

| Type | Goldens that match |
|---|---:|
| flowchart | 114/114 |
| sequence | 38/38 |
| state | 22/22 |
| class | 38/38 |
| er | 25/25 |
| pie | 2/2 |
| mindmap | 13/13 |
| gantt | 11/11 |
| xychart | 8/8 |
| timeline | 14/14 |
| quadrantChart | 3/3 |
| journey | 1/1 |

`testdata/extra` holds more cases that pele drew, which its corpus does not
cover, such as Chinese and Japanese labels that wrap. They match too.

## Use

```go
svg, err := sketch.Render("flowchart LR\n  A --> B\n", sketch.RenderOptions{})
if err != nil {
	// err is an *sketch.Error with a code: syntax, semantic, limit or unsupported-diagram.
}
os.WriteFile("out.svg", []byte(svg.Data), 0o644)
```

`svg.Type` is pele's name for the diagram type, such as `flowchart`.
`svg.MermaidType` is the name Mermaid gives it, such as `flowchart-v2` for
`graph TD`. A `Diagram` and an `Error` carry both names too. A parse error
names the diagram type even when the parser of the type does not, as pele
v1.7.0 does.

## Text

`tuios.dev/sketch/text` draws flowcharts, sequence diagrams, state diagrams,
class diagrams, ER diagrams, pie charts, mindmaps, gantt charts, timelines, XY
charts, quadrant charts and user journeys with box-drawing characters, for a
terminal that shows no pictures. For a graph it takes the layout that sketch
works out for the SVG and maps it to cells. The labels stay real text. A pie
chart is a bar for each slice, with its value. pele leaves a slice under 1%
out of the pie, and shows values only with `showData`. A mindmap is a tree. A
gantt chart is bars on a date axis. A timeline runs across, and runs down when
it does not fit the columns. So does a user journey. An XY chart draws its
bars and lines with tuios.dev/plot, and turns its categories into rows when
they do not fit across. It writes point labels and bar values where they cover
nothing, and leaves out a label that has no room. A quadrant chart is a box in
four parts, with its points as dots.

```go
d, _ := sketch.Parse(src, sketch.ParseOptions{})
lines, err := text.Draw(d, text.Options{Cols: 100})
```

`text.ErrTooWide` says the drawing does not fit the columns. The package first
asks pele for a narrower layout, which turns a flowchart that runs across to
run down. A drawing past `text.MaxCells` gives `text.ErrTooLarge`. Every
character a terminal acts on, such as ESC in a label, is written as U+FFFD.

## Layout of the code

Each Go file is the port of one pele file. The header of the file names it.
The Go files keep the names, the functions and the comments of pele, so that a
change upstream maps to a change here.

| Folder | pele folder |
|---|---|
| `.` | `src/core.ts`, `src/detect.ts`, `src/preprocess.ts` |
| `internal/types` | `src/types.ts`, `src/errors.ts` |
| `internal/text` | `src/text` |
| `internal/svg` | `src/svg` |
| `internal/layout` | `src/layout` |
| `internal/util` | `src/util` |
| `internal/diagrams/...` | `src/diagrams/...` |
| `internal/js` | none: JavaScript semantics the port needs |

`scripts/upstream.sh` lists the pele commits after the pin and the files that
each commit changes.

## Limits

The limits of pele apply. The source can have 50,000 characters. A flowchart can
have 5,000 edges. The SVG can have 4,000,000 characters. A host can set lower
limits in `RenderOptions`.

## Tests

```sh
go test ./...
```

`TestGolden` draws the 522 sources of the pele documentation corpus. It shows
how many match for each type. It fails when a diagram of a ported type does not
match.

`go test -tags differential ./internal/js/` needs node on the path. It checks
the port of V8's `Math.log` and `Math.log10` against node on 1,086,347 inputs,
and `10 ** k` for every whole `k` from -400 to 400. They match bit for bit.
